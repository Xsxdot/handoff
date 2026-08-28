// Package agy 提供 agy (Antigravity CLI) 的 executor.Adapter 契约实现。
package agy

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/rawtap"
	"github.com/Xsxdot/handoff/internal/executor/turn"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/google/uuid"
)

const (
	progressThrottle   = 30 * time.Second
	persistOffsetEvery = 5 * time.Second
)

// Adapter 是 agy 的 executor.Adapter 实现。
type Adapter struct {
	log  *slog.Logger
	mu   sync.Mutex
	runs map[string]*runState
}

// New 创建 agy adapter。
func New(log *slog.Logger) *Adapter {
	if log == nil {
		log = slog.Default()
	}
	return &Adapter{log: log, runs: make(map[string]*runState)}
}

type runState struct {
	taskID       string
	taskDir      string
	repoPath     string
	session      string
	proc         *Proc
	runCtx       context.Context
	runCancel    context.CancelFunc
	evCh         chan executor.AdapterEvent
	stopCh       chan struct{}
	stopOnce     sync.Once
	closeOnce    sync.Once
	renderPath   string
	frames       *turn.FrameWriter
	seg          *turn.Segmenter
	textPart     string
	emitMu       sync.Mutex
	evClosed     bool
	turnMu       sync.Mutex
	turnBuf      strings.Builder
	lastProgress time.Time
	startCommit  string
	turnEnded    bool
	exitHandled  bool
	ready        chan struct{}
	readyOnce    sync.Once
	startOffset  int64
}

func (a *Adapter) newRun(taskID, taskDir, repoPath string) *runState {
	r := &runState{
		taskID:     taskID,
		taskDir:    taskDir,
		repoPath:   repoPath,
		evCh:       make(chan executor.AdapterEvent, 16),
		stopCh:     make(chan struct{}),
		renderPath: filepath.Join(taskDir, renderFileName),
		ready:      make(chan struct{}),
	}
	fw, err := turn.WriterFor(taskDir, a.log)
	if err != nil {
		a.log.Warn("创建帧写入器失败，本任务无结构化帧", "task", taskID, "cause", err)
	}
	r.frames = fw
	r.seg = turn.NewSegmenter(nil)
	r.runCtx, r.runCancel = context.WithCancel(context.Background())
	a.mu.Lock()
	a.runs[taskID] = r
	a.mu.Unlock()
	return r
}

func (a *Adapter) lookup(taskID string) *runState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.runs[taskID]
}

func (a *Adapter) drop(taskID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.runs, taskID)
}

func (r *runState) markReady() {
	r.readyOnce.Do(func() { close(r.ready) })
}

// Start 启动任务执行并立即返回。
func (a *Adapter) Start(ctx context.Context, req executor.StartReq) (err error) {
	a.log.Info("agy adapter 开始启动任务", "task", req.Task.ID,
		"task_dir", req.TaskDir, "workdir", req.Task.Workdir())
	defer func() {
		if err != nil {
			a.log.Error("agy adapter 启动任务失败", "task", req.Task.ID, "cause", err)
		}
	}()

	sessionID := uuid.NewString()
	r := a.newRun(req.Task.ID, req.TaskDir, req.Task.Workdir())
	if err := r.frames.BeginTurn("dispatch", ""); err != nil {
		a.log.Warn("写 turn_start 帧失败，不影响回合", "task", req.Task.ID, "cause", err)
	}
	a.reportTiming(r, r.seg.BeginTurn(r.frames.Turn()))
	r.textPart = r.frames.NextPart()
	r.session = sessionID

	rollback := func() {
		if r.proc != nil {
			r.proc.Kill()
		}
		r.runCancel()
		a.drop(req.Task.ID)
	}

	promptText, err := turn.RenderPrompt(req.Task.ID, req.PlanContent, req.Discipline)
	if err != nil {
		rollback()
		return fmt.Errorf("渲染 prompt: %w", err)
	}

	proc, err := startProc(ctx, StartProcReq{
		RepoPath:  req.Task.Workdir(),
		TaskID:    req.Task.ID,
		TaskDir:   req.TaskDir,
		SessionID: sessionID,
		Model:     req.Task.Model,
		Env:       req.Env,
		MarkRoot:  "",
	}, a.log)
	if err != nil {
		rollback()
		return err
	}
	r.proc = proc

	go r.streamLoop(a)
	go a.watchdog(r)

	if err := proc.WriteInput(promptText); err != nil {
		rollback()
		return fmt.Errorf("投递首回合 prompt: %w", err)
	}
	a.log.Info("agy 初始 prompt 已投递", "task", req.Task.ID, "prompt_len", len(promptText))

	select {
	case <-r.ready:
		a.log.Info("agy 就绪", "task", req.Task.ID, "session", r.session)
	case <-time.After(startReadyTimeout):
		tail := agyLogTail(req.TaskDir)
		rollback()
		a.log.Error("agy 就绪超时", "task", req.Task.ID, "stderr_tail", tail)
		return fmt.Errorf("agy 就绪超时（%s）: %s", startReadyTimeout, tail)
	case <-ctx.Done():
		rollback()
		return fmt.Errorf("启动被取消: %w", ctx.Err())
	}

	r.captureStartCommit(a)
	a.emit(r, executor.AdapterEvent{Type: "progress", SessionID: r.session, Text: "会话就绪"})
	return nil
}

func (r *runState) captureStartCommit(a *Adapter) {
	if out, err := exec.Command("git", "-C", r.repoPath, "rev-parse", "HEAD").Output(); err != nil {
		a.log.Warn("捕获任务起点 commit 失败，兜底按无新提交处理",
			"task", r.taskID, "repo", r.repoPath, "cause", err)
	} else {
		r.startCommit = strings.TrimSpace(string(out))
	}
}

// Events 返回任务的事件流通道。
func (a *Adapter) Events(taskID string) <-chan executor.AdapterEvent {
	if r := a.lookup(taskID); r != nil {
		return r.evCh
	}
	ch := make(chan executor.AdapterEvent)
	close(ch)
	return ch
}

// Send 往同一会话续发指令。
func (a *Adapter) Send(ctx context.Context, taskID, text string) (err error) {
	r := a.lookup(taskID)
	if r == nil {
		return fmt.Errorf("任务 %s: %w", taskID, executor.ErrTaskNotRunning)
	}
	select {
	case <-r.stopCh:
		return fmt.Errorf("任务 %s 已停止，不能续接", taskID)
	default:
	}
	a.log.Info("agy adapter 收到续接指令", "task", taskID, "text", turn.TruncateRunes(text, 80))
	defer func() {
		if err != nil {
			a.log.Error("agy adapter 续接指令发送失败", "task", taskID, "cause", err)
		} else {
			a.log.Info("agy adapter 续接指令已发送", "task", taskID)
		}
	}()
	if r.proc == nil {
		return fmt.Errorf("任务 %s: %w", taskID, executor.ErrTaskNotRunning)
	}
	if err := r.frames.BeginTurn("send", text); err != nil {
		a.log.Warn("写 turn_start 帧失败，不影响回合", "task", taskID, "cause", err)
	}
	a.reportTiming(r, r.seg.BeginTurn(r.frames.Turn()))
	r.textPart = r.frames.NextPart()
	r.turnEnded = false
	if err := r.proc.WriteInput(text); err != nil {
		return fmt.Errorf("任务 %s: %w", taskID, executor.ErrTaskNotRunning)
	}
	return nil
}

// RespondPermission 应答权限门。
func (a *Adapter) RespondPermission(ctx context.Context, taskID, permID, decision, reason string) error {
	r := a.lookup(taskID)
	if r == nil {
		return fmt.Errorf("任务 %s: %w", taskID, executor.ErrTaskNotRunning)
	}
	// agy 启用了 --dangerously-skip-permissions，如收到响应按普通续接/确认处理
	return nil
}

// Stop 终止任务执行并回收资源。
func (a *Adapter) Stop(taskID string) (err error) {
	r := a.lookup(taskID)
	if r == nil {
		return fmt.Errorf("任务 %s: %w", taskID, executor.ErrTaskNotRunning)
	}
	a.log.Info("agy adapter 停止任务", "task", taskID)
	defer func() {
		if err != nil {
			a.log.Error("agy adapter 停止任务失败", "task", taskID, "cause", err)
		} else {
			a.log.Info("agy adapter 任务已停止", "task", taskID)
		}
	}()
	r.stopOnce.Do(func() {
		close(r.stopCh)
		r.runCancel()
	})
	if r.proc != nil {
		if kerr := r.proc.Kill(); kerr != nil {
			return kerr
		}
	}
	a.drop(taskID)
	return nil
}

const (
	watchdogInterval      = 2 * time.Second
	watchdogFailThreshold = 3
)

func (a *Adapter) watchdog(r *runState) {
	failures := 0
	ticker := time.NewTicker(watchdogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-r.runCtx.Done():
			return
		case <-ticker.C:
			if r.proc != nil && r.proc.Alive() {
				failures = 0
				continue
			}
			failures++
			if failures >= watchdogFailThreshold {
				a.log.Error("agy 探活失败达阈值，判定死亡", "task", r.taskID, "failures", failures)
				r.runCancel()
				return
			}
		}
	}
}

func (r *runState) streamLoop(a *Adapter) {
	defer func() {
		r.closeOnce.Do(r.closeEvents)
		select {
		case <-r.stopCh:
		default:
			a.drop(r.taskID)
		}
		a.log.Info("agy 事件流退出", "task", r.taskID)
	}()
	tl := newTailer(filepath.Join(r.taskDir, outFileName), r.startOffset, a.log)
	tl.rawTap = rawtap.Open("agy", r.taskID, a.log)
	var lastPersist time.Time
	err := tl.Run(r.runCtx, func(m streamMsg) {
		if now := time.Now(); now.Sub(lastPersist) >= persistOffsetEvery {
			if r.session != "" && r.proc != nil {
				if perr := writeProcInfo(r.taskDir, &procInfo{
					Handle: r.proc.Handle, SessionID: r.session, Offset: tl.Offset(),
				}); perr != nil {
					a.log.Warn("持久化 agy offset 失败", "task", r.taskID, "cause", perr)
				}
				lastPersist = now
			}
		}
		a.mapMessage(r, m)
	})
	select {
	case <-r.stopCh:
		return
	default:
	}
	if r.exitHandled {
		return
	}
	tail := agyLogTail(r.taskDir)
	if err != nil {
		a.log.Error("agy 事件流损坏", "task", r.taskID, "cause", err)
		a.emit(r, executor.AdapterEvent{Type: "result", Result: &executor.Result{
			OK: false, SessionID: r.session,
			FailReason: "agy 事件流损坏: " + turn.TailRunes(fmt.Sprint(err), 200),
		}})
		return
	}
	a.log.Warn("agy 事件流意外中断，按失败结束回合", "task", r.taskID, "stderr_tail", tail)
	a.emit(r, executor.AdapterEvent{Type: "result", Result: &executor.Result{
		OK: false, SessionID: r.session,
		FailReason: "agy 事件流意外中断: " + tail,
	}})
}

func (a *Adapter) mapMessage(r *runState, m streamMsg) {
	switch {
	case m.Event == "init":
		if m.ConversationID != "" {
			r.session = m.ConversationID
		}
		a.log.Info("agy 会话就绪", "task", r.taskID, "session", r.session)
		r.markReady()
		a.emit(r, executor.AdapterEvent{Type: "progress", SessionID: r.session, Text: "会话就绪"})
	case m.Event == "step_update" && m.StepUpdate != nil:
		a.mapStepUpdate(r, m.StepUpdate)
	case m.Event == "result" && m.Result != nil:
		a.mapResult(r, m.Result)
	case m.Type == "handoff_exit":
		a.mapExit(r, m)
	default:
		a.log.Debug("未知消息类型，跳过", "task", r.taskID, "event", m.Event, "type", m.Type)
	}
}

func (a *Adapter) mapStepUpdate(r *runState, su *agyStepUpdateData) {
	switch su.StepType {
	case "agent_response":
		if su.TextDelta != "" {
			if err := turn.AppendRender(r.renderPath, su.TextDelta); err != nil {
				a.log.Warn("追加 render.log 失败", "task", r.taskID, "cause", err)
			}
			if err := r.frames.Text(r.textPart, su.TextDelta); err != nil {
				a.log.Warn("写 text 帧失败，不影响回合", "task", r.taskID, "cause", err)
			}
			r.turnMu.Lock()
			r.turnBuf.WriteString(su.TextDelta)
			r.turnMu.Unlock()
			a.maybeProgress(r)
		}
		if su.State == "DONE" && su.Usage != nil {
			if u := ParseUsage(su.Usage); u != nil {
				a.emit(r, executor.AdapterEvent{Type: "usage", Usage: u})
			}
		}
	case "tool":
		partID := fmt.Sprintf("tool_%d", su.StepIndex)
		if su.State == "ACTIVE" {
			params := ""
			if su.ToolInfo != nil && len(su.ToolInfo.Parameters) > 0 {
				params = string(su.ToolInfo.Parameters)
			}
			line := fmt.Sprintf("→ %s: %s", su.ToolName, turn.TruncateRunes(params, 120))
			_ = turn.AppendRender(r.renderPath, "\n"+line+"\n")
			a.reportTiming(r, r.seg.ToolStart(partID, su.ToolName, turn.TruncateRunes(params, 60)))
			if err := r.frames.ToolCall(partID, su.ToolName, params); err != nil {
				a.log.Warn("写 tool_call 帧失败", "task", r.taskID, "cause", err)
			}
		} else if su.State == "DONE" || su.State == "ERROR" {
			outStr := ""
			status := "ok"
			if su.State == "ERROR" {
				status = "error"
				if su.ToolInfo != nil && len(su.ToolInfo.Error) > 0 {
					outStr = string(su.ToolInfo.Error)
				}
			} else if su.ToolInfo != nil && len(su.ToolInfo.Output) > 0 {
				outStr = string(su.ToolInfo.Output)
			}
			dur, entries := r.seg.ToolEnd(partID)
			if err := r.frames.ToolResult(partID, status, outStr, dur); err != nil {
				a.log.Warn("写 tool_result 帧失败", "task", r.taskID, "cause", err)
			}
			a.reportTiming(r, entries)
		}
	}
}

func (a *Adapter) mapResult(r *runState, res *agyResultData) {
	a.reportTiming(r, r.seg.EndTurn())
	if res.ConversationID != "" {
		r.session = res.ConversationID
	}
	if u := ParseUsage(res.Usage); u != nil {
		a.emit(r, executor.AdapterEvent{Type: "usage", Usage: u})
	}
	if res.Status != "SUCCESS" {
		fail := res.Error
		if fail == "" {
			fail = "agy 执行未成功"
		}
		a.log.Error("agy 回合异常结束", "task", r.taskID, "status", res.Status, "error", fail)
		a.emit(r, executor.AdapterEvent{Type: "result", Result: &executor.Result{
			OK: false, SessionID: r.session,
			FailReason: fail,
		}})
		r.turnEnded = true
		return
	}

	text := res.Response
	if strings.TrimSpace(text) == "" {
		r.turnMu.Lock()
		text = r.turnBuf.String()
		r.turnMu.Unlock()
	}
	kind, tr := turn.ParseTrailer(text)
	a.log.Info("agy 回合收尾", "task", r.taskID, "kind", kind, "final_text_len", len([]rune(text)))
	switch kind {
	case "ask":
		a.emit(r, executor.AdapterEvent{Type: "question", Text: turn.ClampQuestion(tr.Question)})
	case "finish":
		a.emit(r, executor.AdapterEvent{Type: "result", Result: &executor.Result{
			OK: true, Branch: tr.Branch, CommitHash: tr.Commit,
			Summary: tr.Summary, SessionID: r.session,
			FinalText: turn.FinalText(text),
		}})
	default:
		a.fallbackClassify(r, text)
	}
	r.clearTurn()
	r.captureStartCommit(a)
	r.turnEnded = true
}

func (a *Adapter) fallbackClassify(r *runState, text string) {
	a.log.Warn("回合未输出协议 trailer，走 git 兜底", "task", r.taskID, "turn_tail", turn.TailRunes(text, 120))
	branch, commit, hasNew, err := turn.GitTurnStatus(r.repoPath, r.startCommit)
	if err != nil || !hasNew {
		if err != nil {
			a.log.Error("git 兜底查询失败", "task", r.taskID, "cause", err)
		}
		if strings.TrimSpace(text) == "" {
			a.log.Warn("回合零文本且无新提交，转失败结果交协调者", "task", r.taskID)
			a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.session,
				Result: &executor.Result{
					OK: false, SessionID: r.session,
					FailReason: "回合结束但零文本产出；executor 仍在线，可 continue 续接重试",
					VoidReason: executor.VoidReasonTurnDiscipline,
				}})
			return
		}
		a.emit(r, executor.AdapterEvent{Type: "question", Text: turn.ClampQuestion(text)})
		return
	}
	a.log.Warn("兜底判定有新提交，但模型未宣布完成，转失败交协调者裁决",
		"task", r.taskID, "branch", branch, "commit", commit)
	a.emit(r, executor.AdapterEvent{Type: "result",
		Result: turn.NoTrailerResult(r.session, branch, commit, text)})
}

func (a *Adapter) mapExit(r *runState, m streamMsg) {
	if m.ExitCode == 0 && r.turnEnded {
		a.log.Info("agy 进程正常退出", "task", r.taskID, "code", m.ExitCode)
	} else {
		tail := agyLogTail(r.taskDir)
		a.log.Error("agy 进程退出", "task", r.taskID, "code", m.ExitCode, "stderr_tail", tail)
		a.emit(r, executor.AdapterEvent{Type: "result", Result: &executor.Result{
			OK: false, SessionID: r.session,
			FailReason: fmt.Sprintf("agy 进程退出（code=%d）: %s", m.ExitCode, tail),
		}})
	}
	r.exitHandled = true
	if r.proc != nil {
		if kerr := r.proc.Kill(); kerr != nil {
			a.log.Warn("哨兵后回收执行者进程失败", "task", r.taskID, "cause", kerr)
		}
	}
	r.runCancel()
}

func (a *Adapter) maybeProgress(r *runState) {
	now := time.Now()
	if now.Sub(r.lastProgress) < progressThrottle {
		return
	}
	r.lastProgress = now
	r.turnMu.Lock()
	text := r.turnBuf.String()
	r.turnMu.Unlock()
	a.emit(r, executor.AdapterEvent{Type: "progress", Text: turn.TailRunes(text, 200)})
}

func (a *Adapter) reportTiming(r *runState, entries []proto.TimingEntry) {
	for i := range entries {
		e := entries[i]
		if !a.emit(r, executor.AdapterEvent{Type: "usage", Timing: &e}) {
			return
		}
	}
}

func (r *runState) clearTurn() {
	r.turnMu.Lock()
	r.turnBuf.Reset()
	r.turnMu.Unlock()
}

func (a *Adapter) emit(r *runState, ev executor.AdapterEvent) bool {
	r.emitMu.Lock()
	defer r.emitMu.Unlock()
	if r.evClosed {
		return false
	}
	select {
	case r.evCh <- ev:
		return true
	case <-r.stopCh:
		return false
	}
}

func (r *runState) closeEvents() {
	r.emitMu.Lock()
	defer r.emitMu.Unlock()
	if r.evClosed {
		return
	}
	r.evClosed = true
	close(r.evCh)
}
