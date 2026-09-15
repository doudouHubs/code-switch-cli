//go:build windows

package services

import (
	"errors"
	"fmt"
	"log"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	petKeyboardHookLowLevel       = 13 // WH_KEYBOARD_LL
	petKeyboardWaitObject0        = 0x00000000
	petKeyboardWaitFailed         = 0xffffffff
	petKeyboardInfinite           = 0xffffffff
	petKeyboardQueueAllInput      = 0x04ff // QS_ALLINPUT
	petKeyboardWaitInputAvailable = 0x0004 // MWMO_INPUTAVAILABLE
	petKeyboardPeekNoRemove       = 0x0000 // PM_NOREMOVE
	petKeyboardPeekRemove         = 0x0001 // PM_REMOVE
	petKeyboardMessageKeyDown     = 0x0100
	petKeyboardMessageSysKeyDown  = 0x0104
	petKeyboardMessageQuit        = 0x0012
)

var (
	user32PetKeyboard                  = windows.NewLazySystemDLL("user32.dll")
	setWindowsHookExPetKeyboardProc    = user32PetKeyboard.NewProc("SetWindowsHookExW")
	unhookWindowsHookExPetKeyboardProc = user32PetKeyboard.NewProc("UnhookWindowsHookEx")
	callNextHookExPetKeyboardProc      = user32PetKeyboard.NewProc("CallNextHookEx")
	translateMessagePetKeyboardProc    = user32PetKeyboard.NewProc("TranslateMessage")
	dispatchMessagePetKeyboardProc     = user32PetKeyboard.NewProc("DispatchMessageW")
	peekMessagePetKeyboardProc         = user32PetKeyboard.NewProc("PeekMessageW")
	msgWaitForMultipleObjectsExProc    = user32PetKeyboard.NewProc("MsgWaitForMultipleObjectsEx")
)

// petKeyboardMessage 是 user32 MSG 的 x64/x86 兼容布局。消息循环只读取
// message 字段，但保留完整结构可以避免 PeekMessage 写越界破坏 Go 栈。
type petKeyboardMessage struct {
	hwnd    windows.HWND
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	point   struct {
		x int32
		y int32
	}
}

type petKeyboardMonitor struct {
	onKey func()

	mu        sync.Mutex
	done      chan struct{}
	stopEvent windows.Handle
	hook      uintptr
	callback  uintptr
	starting  bool
	started   bool
	stopping  bool
}

func NewPetKeyboardMonitor(onKey func()) (PetKeyboardMonitor, error) {
	if onKey == nil {
		return nil, ErrPetKeyboardCallbackNil
	}
	return &petKeyboardMonitor{onKey: onKey}, nil
}

func (m *petKeyboardMonitor) Start() error {
	if m == nil {
		return errors.New("global keyboard activity monitor is nil")
	}
	m.mu.Lock()
	if m.done != nil {
		// Start 在已经运行或正在启动时保持幂等；真正的启停状态仍由同一
		// 消息循环线程维护，避免重复安装多个全局 hook。
		m.mu.Unlock()
		return nil
	}
	stopEvent, err := windows.CreateEvent(nil, windows.CREATE_EVENT_MANUAL_RESET, 0, nil)
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("create global keyboard monitor stop event: %w", err)
	}
	done := make(chan struct{})
	ready := make(chan error, 1)
	m.done = done
	m.stopEvent = stopEvent
	m.starting = true
	m.stopping = false
	m.mu.Unlock()

	go m.run(done, ready, stopEvent)
	if err := <-ready; err != nil {
		<-done
		m.clearLifecycle(done)
		return err
	}
	return nil
}

func (m *petKeyboardMonitor) Stop() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	done := m.done
	stopEvent := m.stopEvent
	m.stopping = done != nil
	m.mu.Unlock()
	if done == nil {
		return nil
	}

	var stopErr error
	if stopEvent != 0 {
		// 退出信号走内核 event 而不是依赖线程消息队列；即使消息队列尚未
		// 创建或线程正在收尾，等待中的消息循环也能被唤醒并执行
		// UnhookWindowsHookEx。
		stopErr = windows.SetEvent(stopEvent)
	}
	<-done
	m.clearLifecycle(done)
	return stopErr
}

func (m *petKeyboardMonitor) run(done chan struct{}, ready chan<- error, stopEvent windows.Handle) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(done)

	m.mu.Lock()
	stopping := m.stopping
	m.mu.Unlock()
	if stopping {
		ready <- ErrPetKeyboardMonitorStopped
		return
	}

	// 低级 hook 回调在安装线程的消息循环中派发；callback 指针必须由 monitor
	// 持有到 Unhook 完成，不能只保留一个局部变量让 Go 回收其回调入口。
	callback := windows.NewCallback(func(nCode int, wParam, lParam uintptr) uintptr {
		return m.handleHook(nCode, wParam, lParam)
	})
	hook, _, callErr := setWindowsHookExPetKeyboardProc.Call(
		petKeyboardHookLowLevel,
		callback,
		0,
		0,
	)
	if hook == 0 {
		err := petKeyboardCallError("SetWindowsHookExW", callErr)
		m.mu.Lock()
		m.starting = false
		m.mu.Unlock()
		ready <- err
		return
	}

	// 先 Peek 一次创建消息队列，再向调用方报告 ready；MsgWaitForMultipleObjectsEx
	// 后续同时等待 stop event 和该线程的输入队列。
	var message petKeyboardMessage
	peekMessagePetKeyboardProc.Call(
		uintptr(unsafe.Pointer(&message)),
		0,
		0,
		0,
		petKeyboardPeekNoRemove,
	)

	m.mu.Lock()
	stopping = m.stopping
	if stopping {
		m.mu.Unlock()
		unhookWindowsHookExPetKeyboardProc.Call(hook)
		ready <- ErrPetKeyboardMonitorStopped
		return
	}
	m.hook = hook
	m.callback = callback
	m.starting = false
	m.started = true
	m.mu.Unlock()
	ready <- nil

	for {
		result, _, waitErr := msgWaitForMultipleObjectsExProc.Call(
			1,
			uintptr(unsafe.Pointer(&stopEvent)),
			petKeyboardInfinite,
			petKeyboardQueueAllInput,
			petKeyboardWaitInputAvailable,
		)
		switch result {
		case petKeyboardWaitObject0:
			goto stopped
		case petKeyboardWaitObject0 + 1:
			if !drainPetKeyboardMessages(&message) {
				goto stopped
			}
		case petKeyboardWaitFailed:
			log.Printf("[PetKeyboard] MsgWaitForMultipleObjectsEx failed: %v", petKeyboardCallError("MsgWaitForMultipleObjectsEx", waitErr))
			goto stopped
		default:
			log.Printf("[PetKeyboard] MsgWaitForMultipleObjectsEx returned unexpected result: %#x", result)
			goto stopped
		}
	}

stopped:
	unhookWindowsHookExPetKeyboardProc.Call(hook)
	m.mu.Lock()
	m.hook = 0
	m.callback = 0
	m.started = false
	m.starting = false
	m.mu.Unlock()
}

func drainPetKeyboardMessages(message *petKeyboardMessage) bool {
	for {
		result, _, _ := peekMessagePetKeyboardProc.Call(
			uintptr(unsafe.Pointer(message)),
			0,
			0,
			0,
			petKeyboardPeekRemove,
		)
		if result == 0 {
			return true
		}
		if message.message == petKeyboardMessageQuit {
			return false
		}
		translateMessagePetKeyboardProc.Call(uintptr(unsafe.Pointer(message)))
		dispatchMessagePetKeyboardProc.Call(uintptr(unsafe.Pointer(message)))
	}
}

func (m *petKeyboardMonitor) handleHook(nCode int, wParam, lParam uintptr) uintptr {
	if nCode >= 0 && petKeyboardIsKeyDownMessage(wParam) {
		// Hook 回调中不读取 lParam 的 KBDLLHOOKSTRUCT，既满足“只捕获活动”
		// 的隐私边界，也减少低级 hook 线程上的工作量。
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					log.Printf("[PetKeyboard] activity callback panicked: %v", recovered)
				}
			}()
			m.onKey()
		}()
	}
	result, _, _ := callNextHookExPetKeyboardProc.Call(0, uintptr(nCode), wParam, lParam)
	return result
}

func (m *petKeyboardMonitor) clearLifecycle(done chan struct{}) {
	m.mu.Lock()
	if m.done == done {
		m.done = nil
		stopEvent := m.stopEvent
		m.stopEvent = 0
		m.hook = 0
		m.callback = 0
		m.started = false
		m.starting = false
		m.stopping = false
		m.mu.Unlock()
		if stopEvent != 0 {
			_ = windows.CloseHandle(stopEvent)
		}
		return
	}
	m.mu.Unlock()
}

func petKeyboardIsKeyDownMessage(wParam uintptr) bool {
	return uint32(wParam) == petKeyboardMessageKeyDown || uint32(wParam) == petKeyboardMessageSysKeyDown
}

func petKeyboardCallError(operation string, callErr error) error {
	if callErr == nil || callErr == windows.ERROR_SUCCESS {
		return fmt.Errorf("%s failed", operation)
	}
	return fmt.Errorf("%s failed: %w", operation, callErr)
}
