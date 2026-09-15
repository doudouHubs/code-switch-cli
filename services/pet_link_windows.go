//go:build windows

package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func normalizePetLinkPipeName(name string) string {
	if len(name) >= len(`\\.\pipe\`) && name[:len(`\\.\pipe\`)] == `\\.\pipe\` {
		return name
	}
	return `\\.\pipe\` + name
}

func isPetLinkNotFound(err error) bool {
	return errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND)
}

func acquirePetLinkInstance(pipeName string) (func() error, error) {
	// Local\ 命名空间按 Windows 用户会话隔离；用 pipe 名称的 hash 生成固定
	// mutex 名，避免路径中的反斜杠等字符破坏内核对象名称。
	digest := sha256.Sum256([]byte(strings.TrimSpace(pipeName)))
	mutexName := `Local\CodeSwitchPet-` + hex.EncodeToString(digest[:16])
	namePtr, err := windows.UTF16PtrFromString(mutexName)
	if err != nil {
		return nil, fmt.Errorf("encode pet link mutex name: %w", err)
	}
	// 不请求 initial owner：Windows mutex 所有权绑定创建线程，若在另一个关闭
	// goroutine 调用 ReleaseMutex 会得到 ERROR_NOT_OWNER，导致句柄关闭后对象
	// 仍被旧线程持有。这里仅把 mutex 当作跨进程存在标记，持有句柄即可。
	handle, err := windows.CreateMutex(nil, false, namePtr)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		return nil, ErrPetLinkAlreadyRunning
	}
	if err != nil {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		return nil, fmt.Errorf("create pet link mutex: %w", err)
	}
	if handle == 0 {
		return nil, errors.New("create pet link mutex returned an empty handle")
	}
	return func() error {
		return windows.CloseHandle(handle)
	}, nil
}

func listenPetLink(name string) (net.Listener, error) {
	listener, err := winio.ListenPipe(name, &winio.PipeConfig{
		// 默认安全描述符继承当前用户 token 的 DACL，主程序与宠物在同一用户
		// 会话中可以互连，同时不把管道无条件开放给其他本地用户。
		MessageMode: false,
	})
	if err != nil {
		return nil, fmt.Errorf("listen pet link pipe %q: %w", name, err)
	}
	return listener, nil
}

func dialPetLink(ctx context.Context, name string) (net.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	conn, err := winio.DialPipeContext(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("dial pet link pipe %q: %w", name, err)
	}
	return conn, nil
}

// newPetHostProcessMonitor 通过真实 process handle 等待宿主退出。PID 只用于
// OpenProcess，句柄一旦打开便指向同一个进程对象，后续 PID 复用不会影响等待结果。
func newPetHostProcessMonitor(ctx context.Context, identity PetHostProcessIdentity, onExit func()) (*PetHostProcessMonitor, error) {
	if identity.PID == 0 {
		return nil, errors.New("pet host process pid is empty")
	}
	if identity.PID == windows.GetCurrentProcessId() {
		return nil, errors.New("pet host pid must not be the pet process")
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, identity.PID)
	if err != nil {
		return nil, fmt.Errorf("open pet host process %d: %w", identity.PID, err)
	}
	if identity.StartTimeUnixNano != 0 {
		startTime, timeErr := petWindowsProcessStartTime(handle)
		if timeErr != nil {
			_ = windows.CloseHandle(handle)
			return nil, fmt.Errorf("read pet host process %d start time: %w", identity.PID, timeErr)
		}
		if startTime.UnixNano() != identity.StartTimeUnixNano {
			_ = windows.CloseHandle(handle)
			return nil, fmt.Errorf("pet host process %d identity does not match", identity.PID)
		}
	}
	monitor := newPetHostProcessMonitorBase()
	go func() {
		defer close(monitor.doneCh)
		defer windows.CloseHandle(handle)
		for {
			result, waitErr := windows.WaitForSingleObject(handle, 250)
			if waitErr != nil {
				return
			}
			switch result {
			case uint32(windows.WAIT_OBJECT_0):
				if onExit != nil {
					onExit()
				}
				return
			case uint32(windows.WAIT_TIMEOUT):
				select {
				case <-monitor.stopCh:
					return
				case <-ctx.Done():
					return
				default:
				}
			default:
				return
			}
		}
	}()
	return monitor, nil
}

func petWindowsProcessStartTime(handle windows.Handle) (time.Time, error) {
	var creationTime windows.Filetime
	var exitTime windows.Filetime
	var kernelTime windows.Filetime
	var userTime windows.Filetime
	if err := windows.GetProcessTimes(handle, &creationTime, &exitTime, &kernelTime, &userTime); err != nil {
		return time.Time{}, err
	}
	return time.Unix(0, creationTime.Nanoseconds()).UTC(), nil
}

func currentPetHostProcessIdentity() (PetHostProcessIdentity, error) {
	handle, err := windows.GetCurrentProcess()
	if err != nil {
		return PetHostProcessIdentity{}, err
	}
	startTime, err := petWindowsProcessStartTime(handle)
	if err != nil {
		return PetHostProcessIdentity{}, err
	}
	return PetHostProcessIdentity{
		PID:               windows.GetCurrentProcessId(),
		StartTimeUnixNano: startTime.UnixNano(),
	}, nil
}
