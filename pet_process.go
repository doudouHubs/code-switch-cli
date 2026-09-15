package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"codeswitch/services"
)

const (
	petProcessModeEnv       = "CODESWITCH_PET_MODE"
	petProcessExeEnv        = "CODESWITCH_PET_EXECUTABLE"
	petSettingsUpdatedEvent = "pet.settings.updated"
)

type petTrayRefreshParams struct {
	PetID string `json:"petId,omitempty"`
}

// petProcessManager 是 main 包唯一的独立桌宠编排入口。它不暴露业务方法，
// 只负责选择嵌入/外置模式、启动并联动宿主；宠物行为仍由 PetService owner 处理。
type petProcessManager struct {
	supervisor *services.PetProcessSupervisor
	external   bool

	mu      sync.Mutex
	started bool
}

func newPetProcessManager() (*petProcessManager, error) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv(petProcessModeEnv)))
	if mode == "embedded" || mode == "in-process" {
		return &petProcessManager{}, nil
	}
	if runtime.GOOS != "windows" {
		// 独立 IPC 首期使用 Windows 命名管道；其它平台继续复用已验证的内嵌窗口。
		if mode == "external" {
			return nil, errors.New("独立宠物 IPC 首期仅支持 Windows")
		}
		return &petProcessManager{}, nil
	}

	executable := strings.TrimSpace(os.Getenv(petProcessExeEnv))
	if executable == "" {
		executable = findPetExecutable()
	}

	hostPID := strconv.Itoa(os.Getpid())
	hostIdentity := services.PetHostProcessIdentity{PID: uint32(os.Getpid())}
	if identity, identityErr := services.CurrentPetHostProcessIdentity(); identityErr == nil {
		hostIdentity = identity
	}
	supervisor, err := services.NewPetProcessSupervisor(services.PetProcessOptions{
		Executable: executable,
		Args:       []string{"--host-pid=" + hostPID},
		Env: []string{
			"CODESWITCH_PET_HOST_PID=" + hostPID,
			"CODESWITCH_PET_PARENT=" + hostPID,
		},
		HostPID:               hostIdentity.PID,
		HostStartTimeUnixNano: hostIdentity.StartTimeUnixNano,
		StartTimeout:          15 * time.Second,
		StopTimeout:           5 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	manager := &petProcessManager{supervisor: supervisor, external: true}
	// 主程序创建内嵌窗口之前先探测默认管道。这样用户先启动独立 Pet 时，
	// 即使当前工作目录没有 CodeSwitchPet.exe，也会选代理而不是第二个窗口。
	probeCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	probeErr := supervisor.Probe(probeCtx)
	cancel()
	if probeErr == nil {
		return manager, nil
	}
	if !errors.Is(probeErr, services.ErrPetLinkNotFound) {
		return nil, fmt.Errorf("探测独立宠物失败: %w", probeErr)
	}
	if executable != "" {
		return manager, nil
	}
	if mode == "external" {
		return nil, errors.New("CODESWITCH_PET_MODE=external 但未找到运行中的 Pet 或 CodeSwitchPet.exe；请设置 CODESWITCH_PET_EXECUTABLE")
	}
	return &petProcessManager{}, nil
}

func findPetExecutable() string {
	var current, cwd string
	if value, err := os.Executable(); err == nil {
		current = value
	}
	if value, err := os.Getwd(); err == nil {
		cwd = value
	}
	for _, candidate := range petExecutableCandidates(current, cwd) {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func petExecutableCandidates(current, cwd string) []string {
	candidates := make([]string, 0, 6)
	if current != "" {
		dir := filepath.Dir(current)
		candidates = append(candidates,
			filepath.Join(dir, "CodeSwitchPet.exe"),
			filepath.Join(dir, "pet", "CodeSwitchPet.exe"),
		)
	}
	if cwd != "" {
		candidates = append(candidates,
			filepath.Join(cwd, "CodeSwitchPet.exe"),
			filepath.Join(cwd, "bin", "CodeSwitchPet.exe"),
			filepath.Join(cwd, "bin", "pet", "CodeSwitchPet.exe"),
		)
	}
	return candidates
}

func (m *petProcessManager) Enabled() bool {
	return m != nil && m.external && m.supervisor != nil
}

func (m *petProcessManager) Start(ctx context.Context) error {
	if !m.Enabled() {
		return nil
	}
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	if err := m.supervisor.Start(ctx); err != nil {
		return fmt.Errorf("启动独立宠物失败: %w", err)
	}
	m.mu.Lock()
	m.started = true
	m.mu.Unlock()
	return nil
}

func (m *petProcessManager) Stop(ctx context.Context) error {
	if !m.Enabled() {
		return nil
	}
	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		return nil
	}
	m.started = false
	m.mu.Unlock()
	return m.supervisor.Stop(ctx)
}

func (m *petProcessManager) Linked() bool {
	return m.Enabled() && m.supervisor.Linked()
}

// RefreshTrayIcon 通知独立宠物重新读取当前皮肤。图标数据不经过主程序，避免
// 设置事件把 atlas 的大图复制进命名管道，也让独立进程继续掌握自己的资源边界。
func (m *petProcessManager) RefreshTrayIcon(ctx context.Context, petID string) error {
	if !m.Enabled() {
		return nil
	}
	petID = strings.TrimSpace(petID)
	if petID == "" {
		petID = services.DefaultPetID
	}
	return m.supervisor.Call(ctx, services.PetLinkMethodRefreshTrayIcon, petTrayRefreshParams{PetID: petID}, nil)
}
