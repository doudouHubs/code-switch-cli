package main

import (
	"context"
	"sync"

	"codeswitch/services"
)

const (
	petLinkWindowOpenMethod      = "pet.window.open"
	petLinkWindowCloseMethod     = "pet.window.close"
	petLinkWindowToggleMethod    = "pet.window.toggle"
	petLinkWindowStateMethod     = "pet.window.state"
	petLinkWindowModeMethod      = "pet.window.mode"
	petLinkWindowMoveMethod      = "pet.window.move"
	petLinkWindowIdleMethod      = "pet.window.idle"
	petLinkWindowResizeMethod    = "pet.window.resize"
	petLinkWindowFocusMethod     = "pet.window.focus"
	petLinkWindowTopmostMethod   = "pet.window.topmost"
	petLinkWindowLayerMethod     = "pet.window.layer"
	petLinkWindowPlatformsMethod = "pet.window.platforms"
)

type petWindowLinkParams struct {
	Mode        string `json:"mode,omitempty"`
	X           int    `json:"x,omitempty"`
	Y           int    `json:"y,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	AlwaysOnTop bool   `json:"alwaysOnTop,omitempty"`
	PlatformID  string `json:"platformId,omitempty"`
}

// petWindowLinkProxy 把主程序已有 PetWindowAPI 方法转成 link 请求，保持前端
// 契约不变。状态使用最近一次成功操作的快照缓存，State() 的旧无 error 签名不会
// 让一次短暂断线把设置页误判成“窗口已关闭”。
type petWindowLinkProxy struct {
	manager *petProcessManager
	mu      sync.RWMutex
	state   services.PetWindowState
}

func newPetWindowLinkProxy(manager *petProcessManager) *petWindowLinkProxy {
	return &petWindowLinkProxy{
		manager: manager,
		state: services.PetWindowState{
			Version:      services.PetSchemaVersion,
			Mode:         services.PetWindowPassive,
			ClickThrough: true,
		},
	}
}

func (p *petWindowLinkProxy) call(method string, params any, result any) error {
	if p == nil || p.manager == nil || p.manager.supervisor == nil {
		return services.ErrPetLinkNotLinked
	}
	return p.manager.supervisor.Call(context.Background(), method, params, result)
}

func (p *petWindowLinkProxy) refreshState() {
	var state services.PetWindowState
	if err := p.call(petLinkWindowStateMethod, nil, &state); err != nil {
		return
	}
	p.mu.Lock()
	p.state = state
	p.mu.Unlock()
}

func (p *petWindowLinkProxy) Open() error {
	if err := p.call(petLinkWindowOpenMethod, nil, nil); err != nil {
		return err
	}
	p.refreshState()
	return nil
}

func (p *petWindowLinkProxy) Close() error {
	if err := p.call(petLinkWindowCloseMethod, nil, nil); err != nil {
		return err
	}
	p.refreshState()
	return nil
}

func (p *petWindowLinkProxy) Toggle() error {
	if err := p.call(petLinkWindowToggleMethod, nil, nil); err != nil {
		return err
	}
	p.refreshState()
	return nil
}

func (p *petWindowLinkProxy) SetMode(mode string) error {
	if err := p.call(petLinkWindowModeMethod, petWindowLinkParams{Mode: mode}, nil); err != nil {
		return err
	}
	p.refreshState()
	return nil
}

func (p *petWindowLinkProxy) Move(x, y int) error {
	if err := p.call(petLinkWindowMoveMethod, petWindowLinkParams{X: x, Y: y}, nil); err != nil {
		return err
	}
	return nil
}

func (p *petWindowLinkProxy) IdleSeconds() (int, error) {
	var result int
	if err := p.call(petLinkWindowIdleMethod, nil, &result); err != nil {
		return 0, err
	}
	return result, nil
}

func (p *petWindowLinkProxy) Resize(width, height int) error {
	if err := p.call(petLinkWindowResizeMethod, petWindowLinkParams{Width: width, Height: height}, nil); err != nil {
		return err
	}
	return nil
}

func (p *petWindowLinkProxy) Focus() error {
	if err := p.call(petLinkWindowFocusMethod, nil, nil); err != nil {
		return err
	}
	return nil
}

func (p *petWindowLinkProxy) SetAlwaysOnTop(alwaysOnTop bool) error {
	if err := p.call(petLinkWindowTopmostMethod, petWindowLinkParams{AlwaysOnTop: alwaysOnTop}, nil); err != nil {
		return err
	}
	p.refreshState()
	return nil
}

func (p *petWindowLinkProxy) SetPlatformLayer(platformID string) error {
	if err := p.call(petLinkWindowLayerMethod, petWindowLinkParams{PlatformID: platformID}, nil); err != nil {
		return err
	}
	return nil
}

func (p *petWindowLinkProxy) GetPlatforms() (services.PetWindowPlatformSnapshot, error) {
	var result services.PetWindowPlatformSnapshot
	if err := p.call(petLinkWindowPlatformsMethod, nil, &result); err != nil {
		return services.PetWindowPlatformSnapshot{}, err
	}
	return result, nil
}

func (p *petWindowLinkProxy) State() services.PetWindowState {
	if p == nil {
		return services.PetWindowState{Version: services.PetSchemaVersion, Mode: services.PetWindowPassive, ClickThrough: true}
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state
}
