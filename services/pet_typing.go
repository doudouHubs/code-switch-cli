package services

import (
	"errors"
	"sync"
	"time"
)

const (
	// PetTypingEventName 是独立宠物 renderer 订阅的输入活动事件。
	PetTypingEventName = "pet.typing"

	// PetTypingTimeout 是最后一次按键到恢复普通模式的固定等待时间。
	PetTypingTimeout = 3 * time.Second
)

var (
	// ErrPetKeyboardUnsupported 表示当前平台没有实现桌面级键盘采样。
	// 非 Windows 保留明确错误，不伪造一个只监听当前窗口的降级实现。
	ErrPetKeyboardUnsupported    = errors.New("global keyboard activity is unsupported on this platform")
	ErrPetKeyboardCallbackNil    = errors.New("global keyboard activity callback is nil")
	ErrPetKeyboardMonitorStopped = errors.New("global keyboard activity monitor stopped during startup")
)

// PetTypingState 是桌面输入活动的轻量运行时快照，不写入 PetState 或数据库。
// LastKeyAt/ExpiresAt 只用于 renderer 首次加载时补齐错过的边沿事件。
type PetTypingState struct {
	Active    bool  `json:"active"`
	LastKeyAt int64 `json:"lastKeyAt,omitempty"`
	ExpiresAt int64 `json:"expiresAt,omitempty"`
}

// PetTypingEmitter 是输入状态机到宿主事件总线的最小边界。
// 回调只在 active/inactive 发生变化时触发，连续按键不会制造事件洪水。
type PetTypingEmitter func(PetTypingState)

// PetTypingControllerOptions 允许测试替换时钟；生产环境使用 time.Now。
type PetTypingControllerOptions struct {
	Timeout time.Duration
	Now     func() time.Time
	Emit    PetTypingEmitter
}

// PetTypingController 只负责“最后一次按键 + 3 秒超时”的状态机。
// Windows hook 负责采样，前端负责把快照映射到动画，三者不互相持有平台细节。
type PetTypingController struct {
	mu      sync.Mutex
	timeout time.Duration
	now     func() time.Time
	emit    PetTypingEmitter

	active     bool
	lastKeyAt  time.Time
	expiresAt  time.Time
	generation uint64
	timer      *time.Timer
	closed     bool
}

// NewPetTypingController 创建输入活动状态机。
func NewPetTypingController(options PetTypingControllerOptions) *PetTypingController {
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = PetTypingTimeout
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &PetTypingController{
		timeout: timeout,
		now:     now,
		emit:    options.Emit,
	}
}

// NotifyKey 记录一次键盘活动。按键内容、修饰键和目标窗口都不会进入该层。
func (c *PetTypingController) NotifyKey() {
	if c == nil {
		return
	}
	now := c.currentTime()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	wasActive := c.active
	oldTimer := c.timer
	c.generation++
	generation := c.generation
	c.active = true
	c.lastKeyAt = now
	c.expiresAt = now.Add(c.timeout)
	c.timer = time.AfterFunc(c.timeout, func() {
		c.expire(generation)
	})
	emit := c.emit
	state := c.stateLocked()
	c.mu.Unlock()

	if oldTimer != nil {
		// Stop 放在解锁后，避免 timer 回调反过来等待本状态机锁；generation
		// 仍然是旧回调不能清理新输入状态的最终保护。
		oldTimer.Stop()
	}
	if !wasActive && emit != nil {
		emit(state)
	}
}

// State 返回当前输入活动快照，供页面挂载时补读当前状态。
func (c *PetTypingController) State() PetTypingState {
	if c == nil {
		return PetTypingState{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stateLocked()
}

// Close 停止超时任务并关闭状态机。关闭阶段不再发送伪造的 inactive 事件。
func (c *PetTypingController) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.active = false
	timer := c.timer
	c.timer = nil
	c.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
}

func (c *PetTypingController) currentTime() time.Time {
	now := c.now()
	if now.IsZero() {
		return time.Now()
	}
	return now
}

func (c *PetTypingController) stateLocked() PetTypingState {
	state := PetTypingState{Active: c.active}
	if !c.lastKeyAt.IsZero() {
		state.LastKeyAt = c.lastKeyAt.UnixMilli()
	}
	if c.active && !c.expiresAt.IsZero() {
		state.ExpiresAt = c.expiresAt.UnixMilli()
	}
	return state
}

func (c *PetTypingController) expire(generation uint64) {
	if c == nil {
		return
	}
	now := c.currentTime()

	c.mu.Lock()
	if c.closed || !c.active || generation != c.generation {
		c.mu.Unlock()
		return
	}
	if now.Before(c.expiresAt) {
		// 系统调度提前唤醒 timer 或测试时钟被替换时，按事实截止时间重新排期，
		// 不能把“提前回调”当成停止输入。
		remaining := c.expiresAt.Sub(now)
		c.timer = time.AfterFunc(remaining, func() {
			c.expire(generation)
		})
		c.mu.Unlock()
		return
	}
	c.active = false
	c.timer = nil
	emit := c.emit
	state := c.stateLocked()
	c.mu.Unlock()

	if emit != nil {
		emit(state)
	}
}

// PetTypingAPI 是 Wails 暴露的只读状态入口；输入状态不提供前端写入方法，
// 防止 renderer 伪造键盘活动或绕过 Windows hook 的生命周期。
type PetTypingAPI struct {
	controller *PetTypingController
}

func NewPetTypingAPI(controller *PetTypingController) *PetTypingAPI {
	return &PetTypingAPI{controller: controller}
}

func (a *PetTypingAPI) State() PetTypingState {
	if a == nil || a.controller == nil {
		return PetTypingState{}
	}
	return a.controller.State()
}

// PetKeyboardMonitor 是平台级键盘采样器的公共生命周期接口。
type PetKeyboardMonitor interface {
	Start() error
	Stop() error
}
