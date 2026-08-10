// ptyservice owner-side PTY 会话服务。
//
// 职责：
//   - 创建并持有 Workspace cwd 下的登录 shell
//   - 用 command_id 保证 session/incarnation 幂等且旧 ID 永不重绑
//   - 提供 input/resize/close 与原子 replay + live output 订阅
//   - 把 create/active/exit 元数据交给 Repository 与 machine outbox 同事务持久化
//
// 边界：
//   - 不解析 ANSI、不保存 terminal bytes 到 SQLite 或 machine event
//   - 不处理 HTTP/WebSocket/peer；adapter 只消费本服务的强类型端口
//   - 不使用 SSH/tmux，进程始终由 Workspace 所属机器的 agentd 持有
package ptyservice

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/store"
	"github.com/xushixin/handoff/internal/workspaceapi"
)

const (
	defaultRingFrames       = 4096
	defaultRingBytes        = 4 << 20
	defaultSubscriberBuffer = 256
	defaultMaxSubscribers   = 8
	maxInputBytes           = 1 << 20
	closeGracePeriod        = 2 * time.Second
	exitPersistenceAttempts = 3
	exitPersistenceBackoff  = 10 * time.Millisecond
)

// Repository 是 PTY Service 所需的 provider-owned durable 事实端口。
type Repository interface {
	CreatePtySessionWithMachineEvent(context.Context, string, string, workspaceapi.PtySession) (workspaceapi.PtySession, bool, controlplane.MachineEvent, error)
	UpdatePtySessionWithMachineEvent(context.Context, string, workspaceapi.PtySession, controlplane.MachineEventKind) (controlplane.MachineEvent, error)
	GetPtySession(context.Context, string) (workspaceapi.PtySession, error)
	GetPtySessionByCommandID(context.Context, string) (workspaceapi.PtySession, error)
	EndActivePtySessionsWithMachineEvents(context.Context, string) (int, error)
}

// Options 控制 PTY replay 和订阅边界；零值使用安全默认值。
type Options struct {
	RingFrames       int
	RingBytes        int
	SubscriberBuffer int
	MaxSubscribers   int
	Shell            string
}

// Service 持有当前 agentd 进程拥有的全部普通 PTY runtime。
type Service struct {
	repo      Repository
	machineID string
	log       *slog.Logger
	opts      Options
	mu        sync.Mutex
	sessions  map[string]*runtimeSession
	commands  map[string]string
	closed    bool
	notify    func()
}

type runtimeSession struct {
	mu          sync.Mutex
	inputMu     sync.Mutex
	persistMu   sync.Mutex
	metadata    workspaceapi.PtySession
	process     ptyProcess
	ring        *Ring
	subscribers map[string]*ptySubscriber
	ended       chan struct{}
	endOnce     sync.Once
	finishErr   error
}

type ptySubscriber struct {
	events chan workspaceapi.PtyServerFrame
	done   chan error
	closed chan struct{}
	once   sync.Once
}

func (s *ptySubscriber) finish(err error) {
	s.once.Do(func() {
		s.done <- err
		close(s.done)
		close(s.events)
		close(s.closed)
	})
}

// NewService 创建 PTY 服务，并把上次 agentd 进程遗留的 active/starting session
// 原位标 ended。恢复失败会阻止服务启动，避免旧 ID 被误当成仍可 attach。
func NewService(repo Repository, machineID string, logger *slog.Logger) (*Service, error) {
	return NewServiceWithOptions(repo, machineID, logger, Options{})
}

// NewServiceWithOptions 创建可注入边界的 PTY 服务（测试使用小 ring/buffer）。
func NewServiceWithOptions(repo Repository, machineID string, logger *slog.Logger, options Options) (*Service, error) {
	if repo == nil || machineID == "" {
		return nil, fmt.Errorf("PTY service 缺 repository 或 machine_id")
	}
	if logger == nil {
		logger = slog.Default()
	}
	options = normalizeOptions(options)
	ended, err := repo.EndActivePtySessionsWithMachineEvents(context.Background(), machineID)
	if err != nil {
		return nil, fmt.Errorf("恢复遗留 PTY session: %w", err)
	}
	service := &Service{repo: repo, machineID: machineID, log: logger, opts: options,
		sessions: make(map[string]*runtimeSession), commands: make(map[string]string)}
	logger.Info("PTY service 已初始化", "machine_id", machineID, "recovered_ended", ended,
		"ring_frames", options.RingFrames, "ring_bytes", options.RingBytes,
		"max_subscribers", options.MaxSubscribers)
	return service, nil
}

func normalizeOptions(options Options) Options {
	if options.RingFrames <= 0 {
		options.RingFrames = defaultRingFrames
	}
	if options.RingBytes <= 0 {
		options.RingBytes = defaultRingBytes
	}
	if options.SubscriberBuffer <= 0 {
		options.SubscriberBuffer = defaultSubscriberBuffer
	}
	if options.MaxSubscribers <= 0 {
		options.MaxSubscribers = defaultMaxSubscribers
	}
	if options.Shell == "" {
		options.Shell = defaultShell()
	}
	return options
}

// SetOutboxNotifier 注入本机 outbox 快速唤醒；durable outbox 仍是事实源。
func (s *Service) SetOutboxNotifier(notify func()) {
	s.mu.Lock()
	s.notify = notify
	s.mu.Unlock()
}

func (s *Service) notifyOutbox() {
	s.mu.Lock()
	notify := s.notify
	s.mu.Unlock()
	if notify != nil {
		notify()
	}
}

// Create 幂等创建 Workspace 登录 shell。相同 command_id 永远返回首个
// session/incarnation；即使该 session 已 ended，也不会偷偷启动新 shell。
func (s *Service) Create(ctx context.Context, ws workspaceapi.WorkspaceRef,
	command workspaceapi.CreateTerminalCommand) (workspaceapi.PtySession, error) {
	started := time.Now()
	if command.CommandID == "" || ws.WorkspaceID == "" || ws.MachineID != s.machineID {
		return workspaceapi.PtySession{}, commandProblem("PTY create 缺 command/workspace 或 owner 不匹配", nil)
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return workspaceapi.PtySession{}, unavailableProblem("PTY service 已关闭", nil)
	}
	if sessionID := s.commands[command.CommandID]; sessionID != "" {
		runtime := s.sessions[sessionID]
		s.mu.Unlock()
		return s.resolveCachedCommand(ctx, command.CommandID, sessionID, ws.WorkspaceID, runtime)
	}
	existing, err := s.repo.GetPtySessionByCommandID(ctx, command.CommandID)
	if err == nil {
		if existing.WorkspaceID != ws.WorkspaceID {
			s.mu.Unlock()
			return workspaceapi.PtySession{}, commandProblem("command_id 已绑定其他 Workspace", nil)
		}
		s.mu.Unlock()
		existing, recovered, recoverErr := s.recoverMissingRuntime(ctx, existing)
		if recoverErr != nil {
			return workspaceapi.PtySession{}, recoverErr
		}
		if recovered {
			s.notifyOutbox()
		}
		s.mu.Lock()
		s.commands[command.CommandID] = existing.TerminalSessionID
		s.mu.Unlock()
		return existing, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		s.mu.Unlock()
		return workspaceapi.PtySession{}, fmt.Errorf("查询 PTY command %s: %w", command.CommandID, err)
	}
	// 只有真正创建新 identity 才需要 Workspace path 仍可访问；幂等命令先于
	// 目录检查收敛，因此目录删除/卸载不会改变旧 command 的权威结果。
	if !filepath.IsAbs(ws.RootPath) {
		s.mu.Unlock()
		return workspaceapi.PtySession{}, commandProblem("Workspace root 必须是 owner 绝对路径", nil)
	}
	info, err := os.Stat(ws.RootPath)
	if err != nil || !info.IsDir() {
		s.mu.Unlock()
		return workspaceapi.PtySession{}, notFoundProblem("Workspace root 不可访问", err)
	}
	defer s.mu.Unlock()

	metadata := workspaceapi.PtySession{
		TerminalSessionID: uuid.NewString(), Incarnation: uuid.NewString(), WorkspaceID: ws.WorkspaceID,
		State: workspaceapi.PtyStateStarting, Shell: s.opts.Shell,
	}
	created, inserted, _, err := s.repo.CreatePtySessionWithMachineEvent(ctx, s.machineID, command.CommandID, metadata)
	if err != nil {
		return workspaceapi.PtySession{}, err
	}
	s.notifyUnlocked()
	if !inserted {
		if created.WorkspaceID != ws.WorkspaceID {
			return workspaceapi.PtySession{}, commandProblem("command_id 已绑定其他 Workspace", nil)
		}
		s.mu.Unlock()
		created, recovered, recoverErr := s.recoverMissingRuntime(ctx, created)
		if recovered {
			s.notifyOutbox()
		}
		s.mu.Lock()
		if recoverErr != nil {
			return workspaceapi.PtySession{}, recoverErr
		}
		s.commands[command.CommandID] = created.TerminalSessionID
		return created, nil
	}

	process, err := startPtyProcess(s.opts.Shell, ws.RootPath, normalizedSize(command.Cols, 120), normalizedSize(command.Rows, 30))
	if err != nil {
		exitCode := 127
		metadata.State = workspaceapi.PtyStateEnded
		metadata.ExitCode = &exitCode
		runtime := newEndedRuntime(metadata, s.opts)
		s.sessions[metadata.TerminalSessionID] = runtime
		s.commands[command.CommandID] = metadata.TerminalSessionID
		persistErr := s.persistExitWithRetry(context.Background(), metadata)
		runtime.finishErr = persistErr
		if persistErr == nil {
			s.notifyUnlocked()
		}
		s.log.Error("PTY spawn 失败", "machine_id", s.machineID, "workspace_id", ws.WorkspaceID,
			"terminal_session_id", metadata.TerminalSessionID, "incarnation", metadata.Incarnation,
			"cause", err, "persistence_error", persistErr)
		if persistErr != nil {
			return workspaceapi.PtySession{}, unavailableProblem("PTY spawn 失败且 ended 状态持久化失败",
				errors.Join(err, persistErr))
		}
		return workspaceapi.PtySession{}, &workspaceapi.Error{Code: workspaceapi.ErrorCapabilityUnsupported,
			Message: "当前平台或 shell 无法创建 PTY", Cause: err}
	}
	runtime := &runtimeSession{metadata: metadata, process: process,
		ring: NewRing(s.opts.RingFrames, s.opts.RingBytes), subscribers: make(map[string]*ptySubscriber), ended: make(chan struct{})}
	metadata.State = workspaceapi.PtyStateActive
	runtime.metadata = metadata
	if _, err := s.repo.UpdatePtySessionWithMachineEvent(ctx, s.machineID, metadata, controlplane.MachineEventPtyUpsert); err != nil {
		_ = process.Kill()
		exitCode, waitErr := process.Wait()
		if waitErr != nil || exitCode < 0 {
			exitCode = 1
		}
		metadata.State = workspaceapi.PtyStateEnded
		metadata.ExitCode = &exitCode
		failedRuntime := newEndedRuntime(metadata, s.opts)
		s.sessions[metadata.TerminalSessionID] = failedRuntime
		s.commands[command.CommandID] = metadata.TerminalSessionID
		persistErr := s.persistExitWithRetry(context.Background(), metadata)
		failedRuntime.finishErr = persistErr
		if persistErr == nil {
			s.notifyUnlocked()
		}
		s.log.Error("PTY active 状态持久化失败", "machine_id", s.machineID,
			"workspace_id", ws.WorkspaceID, "terminal_session_id", metadata.TerminalSessionID,
			"incarnation", metadata.Incarnation, "cause", err, "exit_persistence_error", persistErr)
		return workspaceapi.PtySession{}, unavailableProblem("PTY active 状态持久化失败",
			errors.Join(err, persistErr))
	}
	s.notifyUnlocked()
	s.sessions[metadata.TerminalSessionID] = runtime
	s.commands[command.CommandID] = metadata.TerminalSessionID
	go s.readLoop(runtime)
	s.log.Info("PTY session 已创建", "machine_id", s.machineID, "workspace_id", ws.WorkspaceID,
		"terminal_session_id", metadata.TerminalSessionID, "incarnation", metadata.Incarnation,
		"cols", command.Cols, "rows", command.Rows, "elapsed_ms", time.Since(started).Milliseconds())
	return metadata, nil
}

func (s *Service) resolveCachedCommand(ctx context.Context, commandID, sessionID, workspaceID string,
	runtime *runtimeSession) (workspaceapi.PtySession, error) {
	if runtime != nil {
		runtime.mu.Lock()
		boundWorkspaceID := runtime.metadata.WorkspaceID
		runtime.mu.Unlock()
		if boundWorkspaceID != workspaceID {
			return workspaceapi.PtySession{}, commandProblem("command_id 已绑定其他 Workspace", nil)
		}
		session, recovered, err := s.reconcileRuntimeExit(ctx, runtime)
		if recovered {
			s.notifyOutbox()
		}
		return session, err
	}
	// command cache 只保存稳定身份，进程重启恢复出来的 ended session 没有
	// runtime；此时必须回查 durable metadata，不能返回零值假成功。
	session, err := s.repo.GetPtySession(ctx, sessionID)
	if err != nil {
		return workspaceapi.PtySession{}, fmt.Errorf("读取已缓存 PTY command %s: %w", commandID, err)
	}
	if session.WorkspaceID != workspaceID {
		return workspaceapi.PtySession{}, commandProblem("command_id 已绑定其他 Workspace", nil)
	}
	session, recovered, err := s.recoverMissingRuntime(ctx, session)
	if recovered {
		s.notifyOutbox()
	}
	return session, err
}

func (s *Service) notifyUnlocked() {
	if s.notify != nil {
		s.notify()
	}
}

func normalizedSize(value, fallback uint16) uint16 {
	if value == 0 {
		return fallback
	}
	return value
}

func newEndedRuntime(metadata workspaceapi.PtySession, options Options) *runtimeSession {
	ended := make(chan struct{})
	close(ended)
	return &runtimeSession{metadata: metadata, ring: NewRing(options.RingFrames, options.RingBytes),
		subscribers: make(map[string]*ptySubscriber), ended: ended}
}

func (s *Service) recoverMissingRuntime(ctx context.Context,
	session workspaceapi.PtySession) (workspaceapi.PtySession, bool, error) {
	if session.State == workspaceapi.PtyStateEnded {
		return session, false, nil
	}
	session.State = workspaceapi.PtyStateEnded
	if err := s.persistExitWithRetry(ctx, session); err != nil {
		return workspaceapi.PtySession{}, false, err
	}
	s.log.Warn("PTY 缺失 runtime 已收敛为 ended", "machine_id", s.machineID,
		"workspace_id", session.WorkspaceID, "terminal_session_id", session.TerminalSessionID,
		"incarnation", session.Incarnation, "through_seq", session.ThroughSeq)
	return session, true, nil
}

func (s *Service) reconcileRuntimeExit(ctx context.Context,
	runtime *runtimeSession) (workspaceapi.PtySession, bool, error) {
	runtime.persistMu.Lock()
	defer runtime.persistMu.Unlock()
	runtime.mu.Lock()
	metadata, finishErr := runtime.metadata, runtime.finishErr
	runtime.mu.Unlock()
	if finishErr == nil {
		return metadata, false, nil
	}
	if metadata.State != workspaceapi.PtyStateEnded {
		return workspaceapi.PtySession{}, false, unavailableProblem("PTY runtime 持久化状态不一致", finishErr)
	}
	if err := s.persistExitWithRetry(ctx, metadata); err != nil {
		runtime.mu.Lock()
		runtime.finishErr = err
		runtime.mu.Unlock()
		return workspaceapi.PtySession{}, false, err
	}
	runtime.mu.Lock()
	runtime.finishErr = nil
	runtime.mu.Unlock()
	s.log.Info("PTY exit 状态已重新收敛", "machine_id", s.machineID,
		"workspace_id", metadata.WorkspaceID, "terminal_session_id", metadata.TerminalSessionID,
		"incarnation", metadata.Incarnation, "through_seq", metadata.ThroughSeq)
	return metadata, true, nil
}

// Get 返回 live runtime 或 durable ended session 元数据；不会为旧 ID 创建进程。
func (s *Service) Get(ctx context.Context, sessionID string) (workspaceapi.PtySession, error) {
	s.mu.Lock()
	runtime := s.sessions[sessionID]
	s.mu.Unlock()
	if runtime != nil {
		session, recovered, err := s.reconcileRuntimeExit(ctx, runtime)
		if recovered {
			s.notifyOutbox()
		}
		return session, err
	}
	session, err := s.repo.GetPtySession(ctx, sessionID)
	if errors.Is(err, store.ErrNotFound) {
		return workspaceapi.PtySession{}, notFoundProblem("PTY session 不存在", err)
	}
	if err != nil {
		return workspaceapi.PtySession{}, err
	}
	session, recovered, err := s.recoverMissingRuntime(ctx, session)
	if recovered {
		s.notifyOutbox()
	}
	return session, err
}

// Connect 原子注册 replay + live 订阅；订阅断开只移除 observer，不杀 shell。
func (s *Service) Connect(ctx context.Context, sessionID, incarnation string, after int64) (*workspaceapi.PtySubscription, error) {
	if after < 0 {
		return nil, commandProblem("PTY after 必须大于等于 0", nil)
	}
	s.mu.Lock()
	runtime := s.sessions[sessionID]
	s.mu.Unlock()
	if runtime == nil {
		session, err := s.Get(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		if session.Incarnation != incarnation {
			return nil, commandProblem("PTY incarnation 不匹配", nil)
		}
		if session.State != workspaceapi.PtyStateEnded {
			return nil, unavailableProblem("PTY runtime 不在当前 owner 进程中", nil)
		}
		// agentd 崩溃后 durable through_seq 可能落后于客户端已见游标。旧 identity
		// 已确定 ended，故只把 exit 呈现在客户端游标上，不伪造丢失的输出字节。
		if after > session.ThroughSeq {
			session.ThroughSeq = after
			return endedSubscription(session, []workspaceapi.PtyServerFrame{exitFrame(session)}, false, nil), nil
		}
		if after < session.ThroughSeq {
			snapshot := workspaceapi.PtyServerFrame{Version: 1, Kind: workspaceapi.PtyFrameSnapshot,
				TerminalSessionID: session.TerminalSessionID, Incarnation: session.Incarnation,
				WorkspaceID: session.WorkspaceID, Seq: session.ThroughSeq, ThroughSeq: session.ThroughSeq}
			return endedSubscription(session, nil, true, &snapshot), nil
		}
		return endedSubscription(session, []workspaceapi.PtyServerFrame{exitFrame(session)}, false, nil), nil
	}
	if _, recovered, err := s.reconcileRuntimeExit(ctx, runtime); err != nil {
		return nil, err
	} else if recovered {
		s.notifyOutbox()
	}

	runtime.mu.Lock()
	if runtime.metadata.Incarnation != incarnation {
		runtime.mu.Unlock()
		return nil, commandProblem("PTY incarnation 不匹配", nil)
	}
	if after > runtime.metadata.ThroughSeq {
		runtime.mu.Unlock()
		return nil, commandProblem("PTY after 超出 owner 当前输出游标", nil)
	}
	maxSubscribers := s.opts.MaxSubscribers
	if maxSubscribers <= 0 {
		maxSubscribers = defaultMaxSubscribers
	}
	if runtime.metadata.State != workspaceapi.PtyStateEnded && len(runtime.subscribers) >= maxSubscribers {
		subscriberCount := len(runtime.subscribers)
		workspaceID := runtime.metadata.WorkspaceID
		runtime.mu.Unlock()
		s.log.Warn("PTY 订阅被容量门禁拒绝", "machine_id", s.machineID, "workspace_id", workspaceID,
			"terminal_session_id", sessionID, "incarnation", incarnation,
			"subscriber_count", subscriberCount, "max_subscribers", maxSubscribers)
		return nil, unavailableProblem("PTY session 订阅数已达上限", nil)
	}
	output, expired, snapshot := runtime.ring.Replay(after)
	replay := make([]workspaceapi.PtyServerFrame, 0, len(output))
	for _, frame := range output {
		replay = append(replay, dataFrame(runtime.metadata, frame))
	}
	var snapshotFrame *workspaceapi.PtyServerFrame
	if expired {
		frame := workspaceapi.PtyServerFrame{Version: 1, Kind: workspaceapi.PtyFrameSnapshot,
			TerminalSessionID: sessionID, Incarnation: incarnation, WorkspaceID: runtime.metadata.WorkspaceID, Seq: snapshot.ThroughSeq,
			ThroughSeq: snapshot.ThroughSeq, DataBase64: base64.StdEncoding.EncodeToString(snapshot.Data)}
		snapshotFrame = &frame
	}
	if runtime.metadata.State == workspaceapi.PtyStateEnded {
		if runtime.finishErr != nil {
			finishErr := runtime.finishErr
			runtime.mu.Unlock()
			return nil, finishErr
		}
		session := runtime.metadata
		if expired {
			runtime.mu.Unlock()
			return endedSubscription(session, replay, true, snapshotFrame), nil
		}
		replay = append(replay, exitFrame(session))
		runtime.mu.Unlock()
		return endedSubscription(session, replay, false, nil), nil
	}
	subscriberID := uuid.NewString()
	subscriber := &ptySubscriber{events: make(chan workspaceapi.PtyServerFrame, s.opts.SubscriberBuffer),
		done: make(chan error, 1), closed: make(chan struct{})}
	runtime.subscribers[subscriberID] = subscriber
	session := runtime.metadata
	runtime.mu.Unlock()

	var cancelOnce sync.Once
	cancel := func() {
		cancelOnce.Do(func() { s.removeSubscriber(runtime, subscriberID, nil) })
	}
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-subscriber.closed:
		}
	}()
	s.log.Info("PTY 已订阅", "machine_id", s.machineID, "workspace_id", session.WorkspaceID,
		"terminal_session_id", sessionID, "incarnation", incarnation, "after_seq", after,
		"through_seq", session.ThroughSeq, "replay_count", len(replay), "cursor_expired", expired)
	return workspaceapi.NewPtySubscription(session, replay, subscriber.events, subscriber.done,
		expired, snapshotFrame, func(sendCtx context.Context, frame workspaceapi.PtyClientFrame) error {
			return s.HandleFrame(sendCtx, sessionID, incarnation, frame)
		}, cancel), nil
}

// endedSubscription 返回不会再挂起的 ended 订阅。游标过期时 exit 放进已关闭的
// live 队列，使 adapter 先发送 snapshot，再发送最终 exit。
func endedSubscription(session workspaceapi.PtySession, replay []workspaceapi.PtyServerFrame,
	cursorExpired bool, snapshot *workspaceapi.PtyServerFrame) *workspaceapi.PtySubscription {
	buffer := 0
	if cursorExpired {
		buffer = 1
	}
	events := make(chan workspaceapi.PtyServerFrame, buffer)
	if cursorExpired {
		events <- exitFrame(session)
	}
	close(events)
	done := make(chan error, 1)
	done <- nil
	close(done)
	return workspaceapi.NewPtySubscription(session, replay, events, done, cursorExpired, snapshot,
		func(context.Context, workspaceapi.PtyClientFrame) error {
			return commandProblem("PTY session 已结束", nil)
		}, func() {})
}

// HandleFrame 校验稳定身份后处理 input/resize/ack；日志不记录 input 内容。
func (s *Service) HandleFrame(ctx context.Context, sessionID, incarnation string, frame workspaceapi.PtyClientFrame) error {
	if frame.Version != 1 || frame.TerminalSessionID != sessionID || frame.Incarnation != incarnation {
		return commandProblem("PTY client frame 身份或版本不匹配", nil)
	}
	switch frame.Kind {
	case workspaceapi.PtyClientFrameInput:
		data, err := base64.StdEncoding.DecodeString(frame.DataBase64)
		if err != nil || len(data) > maxInputBytes {
			return commandProblem("PTY input base64 无效或超过限制", err)
		}
		return s.Input(ctx, sessionID, incarnation, data)
	case workspaceapi.PtyClientFrameResize:
		return s.Resize(ctx, sessionID, incarnation, frame.Cols, frame.Rows)
	case workspaceapi.PtyClientFrameAck:
		session, err := s.Get(ctx, sessionID)
		if err != nil {
			return err
		}
		if frame.AckSeq < 0 || frame.AckSeq > session.ThroughSeq {
			return commandProblem("PTY ack_seq 超出当前输出范围", nil)
		}
		return nil
	default:
		return commandProblem("未知 PTY client frame kind", nil)
	}
}

// Input 把原始字节写入 owner PTY；不记录或持久化内容。
func (s *Service) Input(_ context.Context, sessionID, incarnation string, data []byte) error {
	runtime, err := s.activeRuntime(sessionID, incarnation)
	if err != nil {
		return err
	}
	runtime.inputMu.Lock()
	defer runtime.inputMu.Unlock()
	if _, err := runtime.process.Write(data); err != nil {
		return unavailableProblem("PTY input 写入失败", err)
	}
	s.log.Debug("PTY input 已写入", "machine_id", s.machineID, "terminal_session_id", sessionID,
		"incarnation", incarnation, "byte_count", len(data))
	return nil
}

// Resize 更新 owner PTY 窗口尺寸。
func (s *Service) Resize(_ context.Context, sessionID, incarnation string, cols, rows uint16) error {
	if cols == 0 || rows == 0 {
		return commandProblem("PTY resize cols/rows 必须大于 0", nil)
	}
	runtime, err := s.activeRuntime(sessionID, incarnation)
	if err != nil {
		return err
	}
	if err := runtime.process.Resize(cols, rows); err != nil {
		return unavailableProblem("PTY resize 失败", err)
	}
	s.log.Info("PTY resize 完成", "machine_id", s.machineID, "terminal_session_id", sessionID,
		"incarnation", incarnation, "cols", cols, "rows", rows)
	return nil
}

func (s *Service) activeRuntime(sessionID, incarnation string) (*runtimeSession, error) {
	s.mu.Lock()
	runtime := s.sessions[sessionID]
	s.mu.Unlock()
	if runtime == nil {
		return nil, notFoundProblem("PTY session 不存在或已结束", nil)
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.metadata.Incarnation != incarnation {
		return nil, commandProblem("PTY incarnation 不匹配", nil)
	}
	if runtime.metadata.State != workspaceapi.PtyStateActive {
		return nil, commandProblem("PTY session 已结束", nil)
	}
	return runtime, nil
}

// CloseTerminal 显式终止原 session；不删除 metadata、不重绑 incarnation。
func (s *Service) CloseTerminal(ctx context.Context, sessionID, incarnation string) (workspaceapi.PtySession, error) {
	runtime, err := s.activeRuntime(sessionID, incarnation)
	if err != nil {
		s.mu.Lock()
		endedRuntime := s.sessions[sessionID]
		s.mu.Unlock()
		if endedRuntime != nil {
			session, recovered, reconcileErr := s.reconcileRuntimeExit(ctx, endedRuntime)
			if recovered {
				s.notifyOutbox()
			}
			if reconcileErr != nil {
				return workspaceapi.PtySession{}, reconcileErr
			}
			if session.Incarnation == incarnation && session.State == workspaceapi.PtyStateEnded {
				return session, nil
			}
		}
		if session, getErr := s.Get(ctx, sessionID); getErr == nil && session.Incarnation == incarnation && session.State == workspaceapi.PtyStateEnded {
			return session, nil
		}
		return workspaceapi.PtySession{}, err
	}
	if err := runtime.process.Terminate(); err != nil {
		s.log.Warn("PTY graceful terminate 失败", "terminal_session_id", sessionID, "incarnation", incarnation, "cause", err)
	}
	timer := time.NewTimer(closeGracePeriod)
	defer timer.Stop()
	select {
	case <-runtime.ended:
	case <-timer.C:
		if err := runtime.process.Kill(); err != nil {
			s.log.Warn("PTY force kill 失败", "terminal_session_id", sessionID, "incarnation", incarnation, "cause", err)
		}
		select {
		case <-runtime.ended:
		case <-ctx.Done():
			return workspaceapi.PtySession{}, unavailableProblem("等待 PTY 结束被取消", ctx.Err())
		}
	}
	session, recovered, reconcileErr := s.reconcileRuntimeExit(ctx, runtime)
	if recovered {
		s.notifyOutbox()
	}
	return session, reconcileErr
}

func (s *Service) readLoop(runtime *runtimeSession) {
	buffer := make([]byte, 32<<10)
	for {
		n, err := runtime.process.Read(buffer)
		if n > 0 {
			s.publishData(runtime, buffer[:n])
		}
		if err != nil {
			exitCode, waitErr := runtime.process.Wait()
			if waitErr != nil && !errors.Is(waitErr, io.EOF) {
				s.log.Debug("PTY process wait 返回错误", "terminal_session_id", runtime.metadata.TerminalSessionID, "cause", waitErr)
			}
			s.finishRuntime(runtime, exitCode)
			return
		}
	}
}

func (s *Service) publishData(runtime *runtimeSession, data []byte) {
	runtime.mu.Lock()
	output := runtime.ring.Append(data)
	runtime.metadata.ThroughSeq = output.Seq
	frame := dataFrame(runtime.metadata, output)
	var slow []*ptySubscriber
	for id, subscriber := range runtime.subscribers {
		select {
		case subscriber.events <- frame:
		default:
			delete(runtime.subscribers, id)
			slow = append(slow, subscriber)
		}
	}
	runtime.mu.Unlock()
	for _, subscriber := range slow {
		subscriber.finish(&workspaceapi.Error{Code: workspaceapi.ErrorSlowConsumer,
			Message: "PTY 客户端消费过慢，请按 seq 重连"})
	}
	if len(slow) > 0 {
		s.log.Warn("PTY 慢订阅已断开", "terminal_session_id", frame.TerminalSessionID,
			"incarnation", frame.Incarnation, "through_seq", frame.ThroughSeq, "subscriber_count", len(slow))
	}
}

func (s *Service) finishRuntime(runtime *runtimeSession, exitCode int) {
	runtime.endOnce.Do(func() {
		// 与后续 Get/Connect/Close 的 reconciliation 共用 persistMu；先占锁再
		// 发布 ended metadata，避免观察者在首次 durable 写入尚未完成时误报成功。
		runtime.persistMu.Lock()
		runtime.mu.Lock()
		runtime.metadata.State = workspaceapi.PtyStateEnded
		runtime.metadata.ThroughSeq = runtime.ring.ThroughSeq()
		runtime.metadata.ExitCode = &exitCode
		metadata := runtime.metadata
		subscribers := make([]*ptySubscriber, 0, len(runtime.subscribers))
		for id, subscriber := range runtime.subscribers {
			delete(runtime.subscribers, id)
			subscribers = append(subscribers, subscriber)
		}
		runtime.mu.Unlock()
		persistErr := s.persistExitWithRetry(context.Background(), metadata)
		if persistErr != nil {
			s.log.Error("PTY exit 持久化失败", "machine_id", s.machineID,
				"terminal_session_id", metadata.TerminalSessionID, "incarnation", metadata.Incarnation, "cause", persistErr)
		}
		runtime.mu.Lock()
		runtime.finishErr = persistErr
		runtime.mu.Unlock()
		runtime.persistMu.Unlock()
		if persistErr == nil {
			// notifier 需要 s.mu，必须在释放 persistMu 后调用；Create 的幂等
			// reconciliation 会按相反方向取锁，持锁通知会形成 AB/BA 死锁。
			s.notifyOutbox()
		}
		frame := exitFrame(metadata)
		slowCount := 0
		for _, subscriber := range subscribers {
			var reason error = persistErr
			// durable ended 写入失败时不能先发布 exit：peer 把 exit 视为协议
			// 终帧，后发的 problem 将永远不可见。失败只结束 Done(error)，
			// reconciliation 成功后的下一次 attach 才会得到权威 exit。
			if persistErr == nil {
				select {
				case subscriber.events <- frame:
				default:
					slowCount++
					reason = &workspaceapi.Error{Code: workspaceapi.ErrorSlowConsumer,
						Message: "PTY 客户端消费过慢，最终状态需按 seq 重连"}
				}
			}
			subscriber.finish(reason)
		}
		close(runtime.ended)
		s.log.Info("PTY session 已结束", "machine_id", s.machineID, "workspace_id", metadata.WorkspaceID,
			"terminal_session_id", metadata.TerminalSessionID, "incarnation", metadata.Incarnation,
			"through_seq", metadata.ThroughSeq, "exit_code", exitCode, "slow_subscriber_count", slowCount)
	})
}

func (s *Service) persistExitWithRetry(ctx context.Context, metadata workspaceapi.PtySession) error {
	var lastErr error
	for attempt := 1; attempt <= exitPersistenceAttempts; attempt++ {
		if _, err := s.repo.UpdatePtySessionWithMachineEvent(ctx, s.machineID, metadata,
			controlplane.MachineEventPtyExit); err == nil {
			return nil
		} else {
			lastErr = err
			s.log.Warn("PTY exit 持久化重试", "machine_id", s.machineID,
				"terminal_session_id", metadata.TerminalSessionID, "incarnation", metadata.Incarnation,
				"attempt", attempt, "max_attempts", exitPersistenceAttempts, "cause", err)
		}
		if attempt < exitPersistenceAttempts {
			timer := time.NewTimer(exitPersistenceBackoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return unavailableProblem("PTY exit 状态持久化被取消", errors.Join(lastErr, ctx.Err()))
			case <-timer.C:
			}
		}
	}
	return unavailableProblem("PTY exit 状态持久化失败", lastErr)
}

func (s *Service) removeSubscriber(runtime *runtimeSession, subscriberID string, reason error) {
	runtime.mu.Lock()
	subscriber := runtime.subscribers[subscriberID]
	delete(runtime.subscribers, subscriberID)
	runtime.mu.Unlock()
	if subscriber != nil {
		subscriber.finish(reason)
	}
}

func dataFrame(session workspaceapi.PtySession, output OutputFrame) workspaceapi.PtyServerFrame {
	return workspaceapi.PtyServerFrame{Version: 1, Kind: workspaceapi.PtyFrameData,
		TerminalSessionID: session.TerminalSessionID, Incarnation: session.Incarnation, WorkspaceID: session.WorkspaceID,
		Seq: output.Seq, ThroughSeq: output.Seq, DataBase64: base64.StdEncoding.EncodeToString(output.Data)}
}

func exitFrame(session workspaceapi.PtySession) workspaceapi.PtyServerFrame {
	return workspaceapi.PtyServerFrame{Version: 1, Kind: workspaceapi.PtyFrameExit,
		TerminalSessionID: session.TerminalSessionID, Incarnation: session.Incarnation, WorkspaceID: session.WorkspaceID,
		Seq: session.ThroughSeq, ThroughSeq: session.ThroughSeq, State: session.State, ExitCode: session.ExitCode}
}

// Close 终止本进程持有的全部 PTY；多次调用安全。
func (s *Service) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	runtimes := make([]*runtimeSession, 0, len(s.sessions))
	for _, runtime := range s.sessions {
		runtimes = append(runtimes, runtime)
	}
	s.mu.Unlock()
	for _, runtime := range runtimes {
		runtime.mu.Lock()
		session := runtime.metadata
		runtime.mu.Unlock()
		if session.State != workspaceapi.PtyStateActive {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), closeGracePeriod*2)
		_, _ = s.CloseTerminal(ctx, session.TerminalSessionID, session.Incarnation)
		cancel()
	}
	return nil
}

func commandProblem(message string, cause error) error {
	return &workspaceapi.Error{Code: workspaceapi.ErrorCommandConflict, Message: message, Cause: cause}
}

func notFoundProblem(message string, cause error) error {
	return &workspaceapi.Error{Code: workspaceapi.ErrorResourceNotFound, Message: message, Cause: cause}
}

func unavailableProblem(message string, cause error) error {
	return &workspaceapi.Error{Code: workspaceapi.ErrorUnavailable, Message: message, Retryable: true, Cause: cause}
}
