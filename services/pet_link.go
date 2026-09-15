package services

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const (
	// PetLinkProtocol 是独立宠物与主程序之间的协议标识。标识和版本一起写入
	// 每个请求/响应，避免未来升级时把不同 wire contract 静默混在一条管道上。
	PetLinkProtocol        = "codeswitch.pet-link"
	PetLinkProtocolVersion = 1

	// DefaultPetLinkPipeName 是 Windows 命名管道路径。命名管道默认使用创建进程
	// 的安全描述符，主程序与宠物在同一用户会话内运行时无需开放给其他用户。
	DefaultPetLinkPipeName = `\\.\pipe\codeswitch-pet`

	// PetLinkMaxFrameSize 防止对端不受限地把内存吃光；宠物状态和动作请求远小于
	// 这个上限，超过上限的帧直接断开，不进入 JSON 解析和业务处理。
	PetLinkMaxFrameSize = 1 << 20

	PetLinkMethodAttach   = "link.attach"
	PetLinkMethodPing     = "link.ping"
	PetLinkMethodShutdown = "link.shutdown"
)

var (
	// ErrPetLinkUnsupported 表示当前平台没有实现命名管道传输。非 Windows 仍保留
	// 完整公共类型，便于主程序跨平台编译；调用传输 API 时会明确返回该错误。
	ErrPetLinkUnsupported    = errors.New("pet link is unsupported on this platform")
	ErrPetLinkNotLinked      = errors.New("pet link is not linked")
	ErrPetLinkClosed         = errors.New("pet link is closed")
	ErrPetLinkAlreadyRunning = errors.New("pet link instance is already running")
	ErrPetLinkNotFound       = errors.New("pet link instance not found")
)

// PetLinkRequest 是一条请求帧。Params 使用 RawMessage 保持传输层与宠物业务解耦，
// 业务 handler 可以按 method 选择自己的参数结构，不需要把所有业务类型塞入公共包。
type PetLinkRequest struct {
	Protocol string          `json:"protocol"`
	Version  int             `json:"version"`
	ID       string          `json:"id"`
	Method   string          `json:"method"`
	Params   json.RawMessage `json:"params,omitempty"`
}

// PetLinkResponse 是一条请求对应的响应帧。Result 只有在 OK 时才有意义；Error
// 使用稳定的 code/message 结构，调用端不必依赖远端的具体 Go error 文本。
type PetLinkResponse struct {
	Protocol string           `json:"protocol"`
	Version  int              `json:"version"`
	ID       string           `json:"id"`
	OK       bool             `json:"ok"`
	Result   json.RawMessage  `json:"result,omitempty"`
	Error    *PetLinkRPCError `json:"error,omitempty"`
}

// PetLinkRPCError 是远端拒绝请求时的可序列化错误。
type PetLinkRPCError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *PetLinkRPCError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return e.Message
	}
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

// PetLinkEvent 为后续需要从宠物端主动推送状态变化保留统一 wire contract。
// 当前传输核心先实现 request/response；Event 不会被 Call 当作响应消费。
type PetLinkEvent struct {
	Protocol string          `json:"protocol"`
	Version  int             `json:"version"`
	Name     string          `json:"name"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

// PetLinkAttachParams 在主程序和宠物进程建立联动时发送。宿主句柄由宠物端按 PID
// 打开并持有，因此宿主退出后即使 PID 很快复用，旧句柄也仍然会被 signaled，不能
// 把新进程误当成旧主程序。
type PetLinkAttachParams struct {
	HostPID               uint32 `json:"hostPid"`
	HostStartTimeUnixNano int64  `json:"hostStartTimeUnixNano,omitempty"`
	SessionID             string `json:"sessionId,omitempty"`
}

// PetHostProcessIdentity 标识需要被宠物监控的宿主进程。
type PetHostProcessIdentity struct {
	PID               uint32 `json:"pid"`
	StartTimeUnixNano int64  `json:"startTimeUnixNano,omitempty"`
}

// PetLinkHandler 处理一条已经通过版本校验的请求。返回值会被 JSON 编码为 Result。
type PetLinkHandler func(context.Context, PetLinkRequest) (any, error)

// NewPetLinkRequest 创建带协议版本和唯一请求 ID 的请求。
func NewPetLinkRequest(method string, params any) (PetLinkRequest, error) {
	method = strings.TrimSpace(method)
	if method == "" {
		return PetLinkRequest{}, errors.New("pet link method is empty")
	}
	request := PetLinkRequest{
		Protocol: PetLinkProtocol,
		Version:  PetLinkProtocolVersion,
		ID:       uuid.NewString(),
		Method:   method,
	}
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return PetLinkRequest{}, fmt.Errorf("marshal pet link params: %w", err)
		}
		request.Params = encoded
	}
	return request, nil
}

// MarshalPetLinkRequest/Response 提供测试、诊断和非 net.Conn 适配器可复用的协议
// 边界。真正的连接读写仍由 Client/Server 负责换行帧。
func MarshalPetLinkRequest(request PetLinkRequest) ([]byte, error) {
	if err := validatePetLinkRequest(request); err != nil {
		return nil, err
	}
	return json.Marshal(request)
}

func UnmarshalPetLinkRequest(data []byte) (PetLinkRequest, error) {
	var request PetLinkRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return PetLinkRequest{}, fmt.Errorf("decode pet link request: %w", err)
	}
	if err := validatePetLinkRequest(request); err != nil {
		return PetLinkRequest{}, err
	}
	return request, nil
}

func MarshalPetLinkResponse(response PetLinkResponse) ([]byte, error) {
	if err := validatePetLinkResponse(response); err != nil {
		return nil, err
	}
	return json.Marshal(response)
}

func UnmarshalPetLinkResponse(data []byte) (PetLinkResponse, error) {
	var response PetLinkResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return PetLinkResponse{}, fmt.Errorf("decode pet link response: %w", err)
	}
	if err := validatePetLinkResponse(response); err != nil {
		return PetLinkResponse{}, err
	}
	return response, nil
}

func validatePetLinkRequest(request PetLinkRequest) error {
	if request.Protocol != PetLinkProtocol {
		return fmt.Errorf("unsupported pet link protocol %q", request.Protocol)
	}
	if request.Version != PetLinkProtocolVersion {
		return fmt.Errorf("unsupported pet link version %d", request.Version)
	}
	if strings.TrimSpace(request.ID) == "" {
		return errors.New("pet link request id is empty")
	}
	if strings.TrimSpace(request.Method) == "" {
		return errors.New("pet link request method is empty")
	}
	return nil
}

func validatePetLinkResponse(response PetLinkResponse) error {
	if response.Protocol != PetLinkProtocol {
		return fmt.Errorf("unsupported pet link protocol %q", response.Protocol)
	}
	if response.Version != PetLinkProtocolVersion {
		return fmt.Errorf("unsupported pet link version %d", response.Version)
	}
	if strings.TrimSpace(response.ID) == "" {
		return errors.New("pet link response id is empty")
	}
	if response.OK && response.Error != nil {
		return errors.New("successful pet link response cannot contain error")
	}
	if !response.OK && response.Error == nil {
		return errors.New("failed pet link response must contain error")
	}
	return nil
}

// PetLinkClient 是请求/响应连接。单连接按请求串行化，保证一条换行 JSON 流不会
// 因并发调用交叉写入或把响应错配到另一个请求；上层如需高吞吐可建立多个连接。
type PetLinkClient struct {
	conn   net.Conn
	reader *bufio.Reader
	mu     sync.Mutex
	closed atomic.Bool
}

func newPetLinkClient(conn net.Conn) *PetLinkClient {
	return &PetLinkClient{conn: conn, reader: bufio.NewReader(conn)}
}

// Call 调用远端 method 并把 result 解码到调用方提供的目标对象。ctx 的 deadline
// 同时作用于写入和读取，取消时主动刷新连接 deadline，避免 goroutine 永久卡在管道 IO。
func (c *PetLinkClient) Call(ctx context.Context, method string, params any, result any) error {
	if c == nil || c.conn == nil {
		return ErrPetLinkNotLinked
	}
	if ctx == nil {
		ctx = context.Background()
	}
	request, err := NewPetLinkRequest(method, params)
	if err != nil {
		return err
	}
	return c.callRequest(ctx, request, result)
}

func (c *PetLinkClient) callRequest(ctx context.Context, request PetLinkRequest, result any) error {
	if err := validatePetLinkRequest(request); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return ErrPetLinkClosed
	}

	stopDeadlineWatcher, err := setPetLinkDeadline(c.conn, ctx)
	if err != nil {
		return fmt.Errorf("set pet link deadline: %w", err)
	}
	defer stopDeadlineWatcher()

	payload, err := MarshalPetLinkRequest(request)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if err := writePetLinkFrame(c.conn, payload); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("write pet link request: %w", err)
	}

	frame, err := readPetLinkFrame(c.reader)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("read pet link response: %w", err)
	}
	response, err := UnmarshalPetLinkResponse(frame)
	if err != nil {
		return err
	}
	if response.ID != request.ID {
		return fmt.Errorf("pet link response id %q does not match request %q", response.ID, request.ID)
	}
	if !response.OK {
		return response.Error
	}
	if result != nil && len(response.Result) > 0 && string(response.Result) != "null" {
		if err := json.Unmarshal(response.Result, result); err != nil {
			return fmt.Errorf("decode pet link result: %w", err)
		}
	}
	return nil
}

// Close 关闭连接；它不会向远端发送 shutdown，进程生命周期由 Supervisor.Stop 或
// 业务端显式调用 link.shutdown 控制。
func (c *PetLinkClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	return c.conn.Close()
}

// Conn 暴露底层连接仅用于诊断或特殊协议扩展；普通业务应使用 Call，以维持请求
// ID 校验和串行读写约束。
func (c *PetLinkClient) Conn() net.Conn {
	if c == nil {
		return nil
	}
	return c.conn
}

// PetLinkServerOptions 描述宠物端的传输和标准生命周期钩子。
type PetLinkServerOptions struct {
	PipeName   string
	Handler    PetLinkHandler
	Lifecycle  *PetLinkLifecycle
	OnHostExit func()
	OnShutdown func() error
}

// PetLinkServer 接受主程序连接并逐条处理请求。它只关心协议和生命周期，不依赖
// Wails 或 PetService；宠物 EXE 可以把业务 handler 绑定到自己的独立服务。
type PetLinkServer struct {
	options PetLinkServerOptions

	mu       sync.Mutex
	listener net.Listener
	release  func() error
	ctx      context.Context
	cancel   context.CancelFunc
	conns    map[net.Conn]struct{}
	started  bool
	closed   bool
	runID    uint64
	wg       sync.WaitGroup
}

func NewPetLinkServer(options PetLinkServerOptions) (*PetLinkServer, error) {
	options.PipeName = PetLinkPipeName(options.PipeName)
	return &PetLinkServer{options: options, conns: make(map[net.Conn]struct{})}, nil
}

// Start 启动后台 accept 循环。ctx 取消只会停止传输服务，不会替调用方退出 Wails
// 或其他业务进程；OnShutdown 仍由 link.shutdown 请求显式触发。
func (s *PetLinkServer) Start(ctx context.Context) error {
	if s == nil {
		return errors.New("pet link server is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	release, err := acquirePetLinkInstance(s.options.PipeName)
	if err != nil {
		return err
	}
	listener, err := listenPetLink(s.options.PipeName)
	if err != nil {
		_ = release()
		return err
	}
	s.mu.Lock()
	if s.started && !s.closed {
		s.mu.Unlock()
		_ = listener.Close()
		_ = release()
		return errors.New("pet link server already started")
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.runID++
	runID := s.runID
	runCtx := s.ctx
	s.listener = listener
	s.release = release
	s.started = true
	s.closed = false
	s.mu.Unlock()

	s.wg.Add(1)
	go s.acceptLoop()
	go func() {
		select {
		case <-runCtx.Done():
			s.closeForRun(runID)
		}
	}()
	return nil
}

func (s *PetLinkServer) closeForRun(runID uint64) {
	s.mu.Lock()
	current := s.runID == runID && !s.closed
	s.mu.Unlock()
	if current {
		_ = s.Close()
	}
}

func (s *PetLinkServer) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed || errors.Is(err, net.ErrClosed) {
				return
			}
			// Listener 错误不可恢复时退出 accept 循环；已有连接仍由 Close 收口。
			return
		}
		s.mu.Lock()
		s.conns[conn] = struct{}{}
		s.mu.Unlock()
		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

func (s *PetLinkServer) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		_ = conn.Close()
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
	}()

	reader := bufio.NewReader(conn)
	for {
		frame, err := readPetLinkFrame(reader)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				// 协议错误无法可靠关联 request ID；直接关闭当前会话，避免在
				// 半截 JSON 上继续读导致后续帧边界失真。
			}
			return
		}
		request, err := UnmarshalPetLinkRequest(frame)
		if err != nil {
			return
		}
		response := s.dispatch(request)
		payload, err := MarshalPetLinkResponse(response)
		if err != nil {
			return
		}
		if err := writePetLinkFrame(conn, append(payload, '\n')); err != nil {
			return
		}
	}
}

func (s *PetLinkServer) dispatch(request PetLinkRequest) PetLinkResponse {
	response := PetLinkResponse{
		Protocol: PetLinkProtocol,
		Version:  PetLinkProtocolVersion,
		ID:       request.ID,
	}
	var (
		value any
		err   error
	)

	s.mu.Lock()
	ctx := s.ctx
	s.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	switch request.Method {
	case PetLinkMethodPing:
		value = map[string]any{"linked": s.linked()}
	case PetLinkMethodAttach:
		if s.options.Lifecycle != nil {
			var params PetLinkAttachParams
			if len(request.Params) == 0 {
				err = errors.New("link.attach params are required")
			} else if decodeErr := json.Unmarshal(request.Params, &params); decodeErr != nil {
				err = fmt.Errorf("decode link.attach params: %w", decodeErr)
			} else {
				identity := PetHostProcessIdentity{
					PID:               params.HostPID,
					StartTimeUnixNano: params.HostStartTimeUnixNano,
				}
				err = s.options.Lifecycle.Attach(ctx, identity, s.options.OnHostExit)
			}
		}
		if err == nil {
			value = map[string]any{"linked": true}
		}
	case PetLinkMethodShutdown:
		if s.options.OnShutdown != nil {
			err = s.options.OnShutdown()
		}
		if err == nil {
			value = map[string]any{"accepted": true}
		}
	default:
		if s.options.Handler == nil {
			err = fmt.Errorf("unsupported pet link method %q", request.Method)
		} else {
			value, err = s.options.Handler(ctx, request)
		}
	}
	if err != nil {
		response.Error = &PetLinkRPCError{Code: petLinkErrorCode(err), Message: err.Error()}
		return response
	}
	response.OK = true
	if value != nil {
		encoded, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			response.OK = false
			response.Error = &PetLinkRPCError{Code: "encode_result", Message: marshalErr.Error()}
			return response
		}
		response.Result = encoded
	}
	return response
}

func (s *PetLinkServer) linked() bool {
	if s.options.Lifecycle == nil {
		return true
	}
	return s.options.Lifecycle.Linked()
}

func petLinkErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrPetLinkUnsupported) {
		return "unsupported"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	return "request_failed"
}

// Close 停止 listener 并关闭所有连接。关闭连接会解除正在读取的 handler，随后
// 等待 accept/connection goroutine 收口，保证主程序退出时不会遗留 pipe handle。
func (s *PetLinkServer) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	listener := s.listener
	release := s.release
	cancel := s.cancel
	conns := make([]net.Conn, 0, len(s.conns))
	for conn := range s.conns {
		conns = append(conns, conn)
	}
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if listener != nil {
		_ = listener.Close()
	}
	for _, conn := range conns {
		_ = conn.Close()
	}
	s.wg.Wait()
	if release != nil {
		_ = release()
	}
	if s.options.Lifecycle != nil {
		return s.options.Lifecycle.Close()
	}
	return nil
}

// PetLinkLifecycle 保存“已联动后不可静默退回独立模式”的状态，并负责启动宿主
// 进程监控。宿主退出回调通常应调用应用框架的 Quit；监控器本身不依赖 Wails。
type PetLinkLifecycle struct {
	mu       sync.Mutex
	attachMu sync.Mutex
	linked   bool
	monitor  *PetHostProcessMonitor
	onExit   func()
	revision uint64
}

func NewPetLinkLifecycle(onHostExit ...func()) *PetLinkLifecycle {
	var callback func()
	if len(onHostExit) > 0 {
		callback = onHostExit[0]
	}
	return &PetLinkLifecycle{onExit: callback}
}

func (l *PetLinkLifecycle) Attach(ctx context.Context, identity PetHostProcessIdentity, onHostExit ...func()) error {
	if l == nil {
		return errors.New("pet link lifecycle is nil")
	}
	// attach 与宿主退出回调必须串行化：重连期间旧宿主可能已经退出，
	// 但它的 WaitForSingleObject 回调还没来得及执行。回调若先清掉新连接，
	// 会把“已重连”的宠物误判成应退出。revision 让旧监控在新监控提交后失效。
	l.attachMu.Lock()
	callback := l.onExit
	if len(onHostExit) > 0 && onHostExit[0] != nil {
		callback = onHostExit[0]
	}
	l.mu.Lock()
	nextRevision := l.revision + 1
	l.mu.Unlock()
	monitor, err := WatchPetHostProcess(ctx, identity, func() {
		l.attachMu.Lock()
		l.mu.Lock()
		if l.revision != nextRevision {
			l.mu.Unlock()
			l.attachMu.Unlock()
			return
		}
		l.linked = false
		l.monitor = nil
		l.mu.Unlock()
		l.attachMu.Unlock()
		if callback != nil {
			callback()
		}
	})
	if err != nil {
		l.attachMu.Unlock()
		return err
	}
	l.mu.Lock()
	l.revision = nextRevision
	oldMonitor := l.monitor
	l.monitor = monitor
	l.linked = true
	l.mu.Unlock()
	l.attachMu.Unlock()
	if oldMonitor != nil {
		_ = oldMonitor.Close()
	}
	return nil
}

func (l *PetLinkLifecycle) Linked() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.linked
}

func (l *PetLinkLifecycle) Close() error {
	if l == nil {
		return nil
	}
	l.attachMu.Lock()
	l.mu.Lock()
	// 递增 revision 使已经排队但尚未执行的宿主退出回调失效；主动关闭
	// 不应再次触发 OnHostExit，更不能在主程序正常退出时把自己重新拉起。
	l.revision++
	monitor := l.monitor
	l.monitor = nil
	l.linked = false
	l.mu.Unlock()
	l.attachMu.Unlock()
	if monitor != nil {
		return monitor.Close()
	}
	return nil
}

// PetHostProcessMonitor 是平台相关进程等待器的公共句柄。Windows 实现持有真实
// process handle；非 Windows 返回 ErrPetLinkUnsupported，但保持相同 API 让上层可编译。
type PetHostProcessMonitor struct {
	stopCh    chan struct{}
	doneCh    chan struct{}
	closeOnce sync.Once
}

func newPetHostProcessMonitorBase() *PetHostProcessMonitor {
	return &PetHostProcessMonitor{stopCh: make(chan struct{}), doneCh: make(chan struct{})}
}

func (m *PetHostProcessMonitor) Close() error {
	if m == nil {
		return nil
	}
	m.closeOnce.Do(func() { close(m.stopCh) })
	<-m.doneCh
	return nil
}

func (m *PetHostProcessMonitor) Done() <-chan struct{} {
	if m == nil {
		return nil
	}
	return m.doneCh
}

// WatchPetHostProcess 启动宿主退出监控。回调只会在监控到宿主自然退出时触发，
// 主动 Close 不触发回调，避免正常停止流程再次触发 Quit。
func WatchPetHostProcess(ctx context.Context, identity PetHostProcessIdentity, onExit func()) (*PetHostProcessMonitor, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return newPetHostProcessMonitor(ctx, identity, onExit)
}

// CurrentPetHostProcessIdentity 获取当前进程的 PID 和平台可用的启动时间。Windows
// 会读取进程对象的 creation time；非 Windows 返回 ErrPetLinkUnsupported，调用方
// 仍可在只需要 PID 的场景中省略 StartTimeUnixNano。
func CurrentPetHostProcessIdentity() (PetHostProcessIdentity, error) {
	return currentPetHostProcessIdentity()
}

// PetProcessOptions 控制主程序如何接入独立宠物。Start 总会先探测已有实例；只有
// 管道明确不存在且 Executable 非空时才会启动该进程。为空时 Start 等价于 Attach，
// 用于复用用户先手动启动的宠物。Args/Env/WorkingDir 只影响 supervisor 启动的子进程。
type PetProcessOptions struct {
	PipeName              string
	Executable            string
	Args                  []string
	Env                   []string
	WorkingDir            string
	HostPID               uint32
	HostStartTimeUnixNano int64
	StartTimeout          time.Duration
	StopTimeout           time.Duration
}

type petProcessHandle struct {
	cmd  *exec.Cmd
	done chan error
}

// PetProcessSupervisor 是主程序侧的生命周期协调器。Start/Attach 成功后，宠物端
// 已收到 link.attach；Stop 会发送 link.shutdown，所以即便宠物最初是独立启动的，
// 一旦接入主程序也会遵循“随主程序退出”的规则。
type PetProcessSupervisor struct {
	options PetProcessOptions

	mu      sync.Mutex
	startMu sync.Mutex
	client  *PetLinkClient
	process *petProcessHandle
	linked  bool
	closed  bool
}

func NewPetProcessSupervisor(options PetProcessOptions) (*PetProcessSupervisor, error) {
	options.PipeName = PetLinkPipeName(options.PipeName)
	if options.StartTimeout <= 0 {
		options.StartTimeout = 10 * time.Second
	}
	if options.StopTimeout <= 0 {
		options.StopTimeout = 5 * time.Second
	}
	return &PetProcessSupervisor{options: options}, nil
}

// Probe 只探测默认管道上的现有宠物，不启动新进程，也不建立宿主退出监控。
// ErrPetLinkNotFound 表示管道不存在；其它错误表示管道存在但协议、权限或连接
// 状态异常，调用方必须报错而不能静默回退到第二个内嵌宠物。
func (s *PetProcessSupervisor) Probe(ctx context.Context) error {
	if s == nil {
		return errors.New("pet process supervisor is nil")
	}
	s.startMu.Lock()
	defer s.startMu.Unlock()
	return s.probe(ctx)
}

func (s *PetProcessSupervisor) probe(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.Linked() {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrPetLinkClosed
	}
	pipeName := s.options.PipeName
	s.mu.Unlock()

	conn, err := dialPetLink(ctx, pipeName)
	if err != nil {
		if isPetLinkNotFound(err) {
			return ErrPetLinkNotFound
		}
		return fmt.Errorf("probe pet link: %w", err)
	}
	client := newPetLinkClient(conn)
	defer client.Close()
	if err := client.Call(ctx, PetLinkMethodPing, nil, nil); err != nil {
		return fmt.Errorf("probe pet link protocol: %w", err)
	}
	return nil
}

// Start 按需启动宠物进程并建立联动。若 Executable 为空，则只连接已有实例。
func (s *PetProcessSupervisor) Start(ctx context.Context) error {
	if s == nil {
		return errors.New("pet process supervisor is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if s.Linked() {
		return nil
	}
	// 先探测已手动启动的实例，满足“独立启动后再打开主程序”时复用原
	// 进程的规则；探测只给短窗口，不能让不存在宠物时的启动白等十秒。
	probeTimeout := 300 * time.Millisecond
	if s.options.StartTimeout > 0 && s.options.StartTimeout < probeTimeout {
		probeTimeout = s.options.StartTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	probeErr := s.probe(probeCtx)
	cancel()
	if probeErr == nil {
		// Probe 只做轻量 ping；真正的 attach 仍需发送宿主身份并启动退出监控。
		return s.attach(ctx)
	}
	if !errors.Is(probeErr, ErrPetLinkNotFound) {
		return probeErr
	}
	if strings.TrimSpace(s.options.Executable) == "" {
		return ErrPetLinkNotFound
	}
	s.mu.Lock()
	launchedBeforeStart := s.process != nil
	s.mu.Unlock()
	if err := s.Launch(); err != nil {
		return err
	}
	if err := s.attach(ctx); err != nil {
		// Start 启动的新子进程如果连接失败，必须立即终止并等待；否则主程序
		// 后续重试会叠加多个孤儿宠物进程，且命名管道实例归属变得不确定。
		if !launchedBeforeStart {
			s.stopLaunchedProcess()
		}
		return err
	}
	return nil
}

// Launch 只启动由 options.Executable 指定的宠物进程，不建立主程序联动；用于需要
// 先让独立宠物完成窗口初始化、再由后续 Attach 接管的启动编排。
func (s *PetProcessSupervisor) Launch() error {
	if s == nil {
		return errors.New("pet process supervisor is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrPetLinkClosed
	}
	if s.process != nil {
		return nil
	}
	if strings.TrimSpace(s.options.Executable) == "" {
		return nil
	}
	cmd := hideWindowCmd(s.options.Executable, s.options.Args...)
	if s.options.WorkingDir != "" {
		cmd.Dir = s.options.WorkingDir
	}
	if s.options.Env != nil {
		cmd.Env = append(os.Environ(), s.options.Env...)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start pet process: %w", err)
	}
	handle := &petProcessHandle{cmd: cmd, done: make(chan error, 1)}
	s.process = handle
	go func() {
		err := cmd.Wait()
		handle.done <- err
		close(handle.done)
		s.mu.Lock()
		if s.process == handle {
			s.process = nil
			if s.client != nil {
				_ = s.client.Close()
				s.client = nil
			}
			s.linked = false
		}
		s.mu.Unlock()
	}()
	return nil
}

// Attach 连接已存在的宠物实例并发送宿主身份。它不会启动新进程，适用于用户先
// 双击宠物 EXE、之后再打开主程序的顺序。
func (s *PetProcessSupervisor) Attach(ctx context.Context) error {
	if s == nil {
		return errors.New("pet process supervisor is nil")
	}
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if s.Linked() {
		return nil
	}
	return s.attach(ctx)
}

func (s *PetProcessSupervisor) attach(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrPetLinkClosed
	}
	s.mu.Unlock()

	dialCtx := ctx
	if _, ok := ctx.Deadline(); !ok && s.options.StartTimeout > 0 {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, s.options.StartTimeout)
		defer cancel()
	}
	conn, err := dialPetLinkWithRetry(dialCtx, s.options.PipeName)
	if err != nil {
		return err
	}
	client := newPetLinkClient(conn)
	params := PetLinkAttachParams{
		HostPID:               s.options.HostPID,
		HostStartTimeUnixNano: s.options.HostStartTimeUnixNano,
		SessionID:             uuid.NewString(),
	}
	if params.HostPID == 0 {
		params.HostPID = uint32(os.Getpid())
	}
	if err := client.Call(dialCtx, PetLinkMethodAttach, params, nil); err != nil {
		_ = client.Close()
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = client.Close()
		return ErrPetLinkClosed
	}
	s.client = client
	s.linked = true
	s.mu.Unlock()
	return nil
}

// Call 将业务请求转发给已联动的宠物端。
func (s *PetProcessSupervisor) Call(ctx context.Context, method string, params any, result any) error {
	if s == nil {
		return ErrPetLinkNotLinked
	}
	s.mu.Lock()
	client := s.client
	linked := s.linked
	s.mu.Unlock()
	if !linked || client == nil {
		return ErrPetLinkNotLinked
	}
	return client.Call(ctx, method, params, result)
}

func (s *PetProcessSupervisor) Linked() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.linked && s.client != nil
}

// Stop 通知宠物退出，然后关闭连接并等待由 supervisor 启动的子进程；如果子进程
// 不响应 shutdown，则在 StopTimeout 到期后 Kill，防止主程序退出被宠物卡住。
func (s *PetProcessSupervisor) Stop(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	client := s.client
	process := s.process
	s.client = nil
	s.linked = false
	s.mu.Unlock()

	stopCtx := ctx
	var stopCancel context.CancelFunc
	if _, ok := ctx.Deadline(); !ok && s.options.StopTimeout > 0 {
		stopCtx, stopCancel = context.WithTimeout(ctx, s.options.StopTimeout)
		defer stopCancel()
	}
	var firstErr error
	if client != nil {
		if err := client.Call(stopCtx, PetLinkMethodShutdown, nil, nil); err != nil && !errors.Is(err, net.ErrClosed) {
			firstErr = err
		}
		if err := client.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if process != nil {
		waitErr := s.waitProcess(stopCtx, process)
		if waitErr != nil && firstErr == nil {
			firstErr = waitErr
		}
	}
	return firstErr
}

func (s *PetProcessSupervisor) stopLaunchedProcess() {
	s.mu.Lock()
	process := s.process
	s.mu.Unlock()
	if process == nil {
		return
	}
	if process.cmd.Process != nil {
		_ = process.cmd.Process.Kill()
	}
	// Wait 通常会在 Kill 后立即返回；阻塞在这里可以确保失败重试不会与
	// 上一实例的退出并发，避免两个进程争抢同一个命名管道名称。
	<-process.done
	s.mu.Lock()
	if s.process == process {
		s.process = nil
	}
	s.mu.Unlock()
}

func (s *PetProcessSupervisor) waitProcess(ctx context.Context, process *petProcessHandle) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case err := <-process.done:
		return err
	case <-ctx.Done():
		if process.cmd.Process != nil {
			_ = process.cmd.Process.Kill()
		}
		select {
		case err := <-process.done:
			return fmt.Errorf("wait pet process after kill: %w", err)
		default:
			return ctx.Err()
		}
	}
}

// Close 是 Stop 的 background-context 别名，方便 main 在不需要单独预算时注册到
// 关闭阶段；需要统一关闭预算时应直接调用 Stop(ctx)。
func (s *PetProcessSupervisor) Close() error {
	return s.Stop(context.Background())
}

func PetLinkPipeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return DefaultPetLinkPipeName
	}
	return normalizePetLinkPipeName(name)
}

func writePetLinkFrame(writer io.Writer, frame []byte) error {
	if len(frame) == 0 || len(frame) > PetLinkMaxFrameSize+1 {
		return fmt.Errorf("pet link frame size %d is invalid", len(frame))
	}
	for len(frame) > 0 {
		n, err := writer.Write(frame)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}

func readPetLinkFrame(reader *bufio.Reader) ([]byte, error) {
	var frame []byte
	for {
		part, err := reader.ReadSlice('\n')
		frame = append(frame, part...)
		if len(frame) > PetLinkMaxFrameSize+1 {
			return nil, fmt.Errorf("pet link frame exceeds %d bytes", PetLinkMaxFrameSize)
		}
		if err == nil {
			frame = frame[:len(frame)-1]
			frame = []byte(strings.TrimSpace(string(frame)))
			if len(frame) == 0 {
				return nil, errors.New("empty pet link frame")
			}
			return frame, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, err
		}
	}
}

func setPetLinkDeadline(conn net.Conn, ctx context.Context) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, err
		}
		return func() { _ = conn.SetDeadline(time.Time{}) }, nil
	}
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.SetDeadline(time.Now())
		case <-stop:
		}
	}()
	return func() {
		close(stop)
		_ = conn.SetDeadline(time.Time{})
	}, nil
}

func dialPetLinkWithRetry(ctx context.Context, pipeName string) (net.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var lastErr error
	for {
		conn, err := dialPetLink(ctx, pipeName)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		if errors.Is(err, ErrPetLinkUnsupported) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			if lastErr != nil && !errors.Is(lastErr, context.Canceled) && !errors.Is(lastErr, context.DeadlineExceeded) {
				return nil, fmt.Errorf("dial pet link: %w (context: %v)", lastErr, ctx.Err())
			}
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
