package services

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

var runtimeDiagnosticMu sync.Mutex

const runtimeDiagnosticAsyncQueueCapacity = 512

type runtimeDiagnosticRecord struct {
	event   string
	details []string
}

var (
	runtimeDiagnosticAsyncOnce  sync.Once
	runtimeDiagnosticAsyncQueue chan runtimeDiagnosticRecord
)

// ConfigureRuntimeCrashOutput 为下一次未处理 Go panic/致命运行时错误保留
// 独立现场。它不能捕获 WebView2 自身的 native crash，但能把“无 app-run-return”
// 从无证据退出变成可读的 goroutine/stack 现场；SetCrashOutput 会复制句柄，
// 因此本函数可以在启动阶段安全关闭临时文件。
func ConfigureRuntimeCrashOutput() {
	home, err := getUserHomeDir()
	if err != nil {
		return
	}
	path := filepath.Join(home, appSettingsDir, "codeswitch-crash.log")
	if err := EnsureDir(filepath.Dir(path)); err != nil {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	if err := debug.SetCrashOutput(file, debug.CrashOptions{}); err != nil {
		_ = file.Close()
		return
	}
	_ = file.Close()
}

// WriteRuntimeDiagnosticAsync 把旁路诊断投递到唯一写入 worker。诊断不能与
// Codex stdout 消费争抢线程；队列满时丢弃诊断也不能影响聊天、工具和关闭流程。
func WriteRuntimeDiagnosticAsync(event string, details ...string) {
	runtimeDiagnosticAsyncOnce.Do(func() {
		runtimeDiagnosticAsyncQueue = make(chan runtimeDiagnosticRecord, runtimeDiagnosticAsyncQueueCapacity)
		go func() {
			for record := range runtimeDiagnosticAsyncQueue {
				WriteRuntimeDiagnostic(record.event, record.details...)
			}
		}()
	})

	record := runtimeDiagnosticRecord{
		event:   event,
		details: append([]string(nil), details...),
	}
	select {
	case runtimeDiagnosticAsyncQueue <- record:
	default:
		// 诊断是故障旁路，不允许磁盘慢或日志洪峰反向制造阻塞。
	}
}

// WriteRuntimeDiagnostic 将启动和窗口切换边界写入独立文件。
// GUI 进程启用了 windowsgui 后没有可见 stderr；如果进程在 Wails 初始化阶段退出，
// ConsoleService 还没来得及接管 stdout，这份文件仍能保留“谁在什么时候做了什么”。
func WriteRuntimeDiagnostic(event string, details ...string) {
	event = strings.TrimSpace(event)
	if event == "" {
		event = "unknown"
	}

	home, err := getUserHomeDir()
	if err != nil {
		return
	}
	path := filepath.Join(home, appSettingsDir, "codeswitch-runtime-debug.log")
	if err := EnsureDir(filepath.Dir(path)); err != nil {
		return
	}

	executable, err := os.Executable()
	if err != nil || strings.TrimSpace(executable) == "" {
		executable = os.Args[0]
	}
	fields := []string{
		fmt.Sprintf("time=%s", time.Now().Format(time.RFC3339Nano)),
		fmt.Sprintf("pid=%d", os.Getpid()),
		fmt.Sprintf("ppid=%d", os.Getppid()),
		fmt.Sprintf("exe=%q", executable),
		fmt.Sprintf("event=%s", event),
	}
	fields = append(fields, details...)

	runtimeDiagnosticMu.Lock()
	defer runtimeDiagnosticMu.Unlock()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.WriteString(strings.Join(fields, " ") + "\n")
}
