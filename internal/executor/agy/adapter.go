// Package agy 提供 agy (Antigravity CLI) 的 executor.Adapter 契约实现。
package agy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/rawtap"
	"github.com/Xsxdot/handoff/internal/executor/turn"
	"github.com/Xsxdot/handoff/internal/prochost"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/google/uuid"
)

const (
	progressThrottle   = 30 * time.Second
	persistOffsetEvery = 5 * time.Second
	permTextHardLimit  = 64 * 1024
)

// Adapter 是 agy 的 executor.Adapter 实现。
type Adapter struct {
	log  *slog.Logger
	mu   sync.Mutex
	runs map[string]*runState
}

// managedTaskTmpEnv derives the task-private environment from TaskDir.
func managedTaskTmpEnv(taskDir, taskID string) (string, []string) {
	dataDir := filepath.Dir(filepath.Dir(taskDir))
	if filepath.Base(filepath.Dir(taskDir)) != "tasks" {
		dataDir = filepath.Dir(taskDir)
	}
	tmpDir := executor.TaskTmpDir(dataDir, taskID)
	return tmpDir, []string{
		"TMPDIR=" + tmpDir,
		"GOTMPDIR=" + tmpDir,
		"GOCACHE=" + filepath.Join(tmpDir, "gocache"),
		"HOME=" + filepath.Join(taskDir, agyHomeDirName),
	}
}

// ensureTaskTmp creates the task-private directory immediately before a new process is started.
func ensureTaskTmp(taskID, tmpDir string, log *slog.Logger) error {
	if err := os.MkdirAll(tmpDir, 0700); err != nil {
		log.Error("创建任务临时目录失败", "task", taskID, "tmp_dir", tmpDir, "cause", err)
		return err
	}
	log.Info("任务临时目录已就绪", "task", taskID, "tmp_dir", tmpDir)
	return nil
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
	actualModel  string
	proc         *Proc
	permSrv      *permServer
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
	r.actualModel = req.Task.Model

	rollback := func() {
		if r.permSrv != nil {
			_ = r.permSrv.Close()
		}
		if r.proc != nil {
			r.proc.Kill()
		}
		if restoreErr := RestoreTaskEnv(req.TaskDir); restoreErr != nil {
			a.log.Error("agy 启动回滚还原 hooks 失败", "task", req.Task.ID, "task_dir", req.TaskDir, "cause", restoreErr)
		}
		r.runCancel()
		a.drop(req.Task.ID)
	}

	tmpDir, managedEnv := managedTaskTmpEnv(req.TaskDir, req.Task.ID)
	if err := ensureTaskTmp(req.Task.ID, tmpDir, a.log); err != nil {
		rollback()
		return fmt.Errorf("创建任务临时目录 %s: %w", tmpDir, err)
	}
	env := append(append([]string{}, req.Env...), managedEnv...)

	sockPath := filepath.Join(req.TaskDir, "perm.sock")
	permSrv, err := newPermServerFn(sockPath, a.log, func(ask permAsk) {
		a.onPermissionAsk(r, ask)
	})
	if err != nil {
		rollback()
		return fmt.Errorf("启动权限服务端: %w", err)
	}
	r.permSrv = permSrv

	selfExe, err := os.Executable()
	if err != nil {
		selfExe = "handoff"
	}
	_, promptText, err := WriteTaskEnv(req.Task.Workdir(), req.TaskDir, req.Task.ID, req.PlanContent, sockPath, selfExe, req.Discipline)
	if err != nil {
		rollback()
		return fmt.Errorf("准备任务环境: %w", err)
	}

	proc, err := startProc(ctx, StartProcReq{
		RepoPath:  req.Task.Workdir(),
		TaskID:    req.Task.ID,
		TaskDir:   req.TaskDir,
		SessionID: sessionID,
		Model:     req.Task.Model,
		Env:       env,
		MarkRoot:  prochost.ResolveMarkRoot(req.Task.Workdir(), req.Task.WorktreeManaged),
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
	a.emit(r, executor.AdapterEvent{Type: "progress", SessionID: r.session, ActualModel: r.actualModel, Text: "会话就绪"})
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

// DenyReasonInBand 表明本 adapter 把拒绝理由与裁决同帧送达模型（通过 PreToolUse hook 的 reason 字段）。
// manager 据此跳过 B50 的带外挂起注入，避免模型被同一条理由说两遍。
func (a *Adapter) DenyReasonInBand() bool { return true }

// RespondPermission 应答权限门。
func (a *Adapter) RespondPermission(ctx context.Context, taskID, permID, decision, reason string) error {
	r := a.lookup(taskID)
	if r == nil {
		return fmt.Errorf("任务 %s: %w", taskID, executor.ErrTaskNotRunning)
	}
	if r.permSrv == nil {
		return fmt.Errorf("任务 %s 没有活动的权限服务端", taskID)
	}
	behavior := "allow"
	message := ""
	if decision != "once" {
		behavior = "deny"
		if reason != "" {
			message = turn.DenyGuidanceText(reason)
		} else {
			message = "协调者拒绝了本次操作"
		}
	}
	return r.permSrv.Respond(permID, behavior, message)
}

func (a *Adapter) onPermissionAsk(r *runState, ask permAsk) {
	text, req := permTextAndRequest(ask.ToolName, ask.Input)
	if strings.TrimSpace(text) == "" {
		text = "agy 权限请求（" + ask.ToolUseID + "）"
	}
	a.emit(r, executor.AdapterEvent{
		Type:         "permission",
		PermissionID: ask.ToolUseID,
		Text:         turn.TruncateMarked(text, permTextHardLimit),
		Perm:         req,
	})
}

func permTextAndRequest(toolName string, input json.RawMessage) (string, *executor.PermRequest) {
	switch strings.ToLower(toolName) {
	case "run_command":
		var in struct {
			CommandLine string `json:"CommandLine"`
			Command     string `json:"command"`
		}
		if json.Unmarshal(input, &in) == nil {
			cmd := in.CommandLine
			if cmd == "" {
				cmd = in.Command
			}
			if cmd != "" {
				return "run_command: " + cmd, &executor.PermRequest{
					Tool:    executor.PermToolBash,
					Command: cmd,
				}
			}
		}
	case "write_file", "write_to_file", "replace_file_content", "multi_replace_file_content", "sed_file":
		var in struct {
			TargetFile   string `json:"TargetFile"`
			AbsolutePath string `json:"AbsolutePath"`
			FilePath     string `json:"file_path"`
			Path         string `json:"path"`
		}
		if json.Unmarshal(input, &in) == nil {
			p := in.TargetFile
			if p == "" {
				p = in.AbsolutePath
			}
			if p == "" {
				p = in.FilePath
			}
			if p == "" {
				p = in.Path
			}
			if p != "" {
				toolType := executor.PermToolWrite
				if strings.ToLower(toolName) != "write_to_file" && strings.ToLower(toolName) != "write_file" {
					toolType = executor.PermToolEdit
				}
				return fmt.Sprintf("%s: %s", toolName, p), &executor.PermRequest{
					Tool:  toolType,
					Paths: []string{p},
				}
			}
		}
	case "view_file", "read_file", "grep_search", "find_by_name", "list_dir":
		p := permPathFromInput(input, "AbsolutePath", "TargetFile", "SearchPath", "DirectoryPath", "Path", "path")
		if p != "" {
			return fmt.Sprintf("%s: %s", toolName, p), &executor.PermRequest{
				Tool:  executor.PermToolEdit,
				Paths: []string{p},
			}
		}
	case "read_url_content", "search_web":
		var in struct {
			URL   string `json:"Url"`
			Query string `json:"query"`
		}
		if json.Unmarshal(input, &in) == nil {
			arg := in.URL
			if arg == "" {
				arg = in.Query
			}
			if arg != "" {
				return fmt.Sprintf("%s: %s", toolName, arg), &executor.PermRequest{
					Tool: executor.PermToolWebFetch,
				}
			}
		}
	}
	text := fmt.Sprintf("%s: %s", toolName, turn.TruncateRunes(string(input), 200))
	return text, &executor.PermRequest{Tool: executor.PermToolOther}
}

func permPathFromInput(input json.RawMessage, keys ...string) string {
	var raw map[string]any
	if json.Unmarshal(input, &raw) != nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := raw[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
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
	if r.permSrv != nil {
		_ = r.permSrv.Close()
	}
	var killErr error
	if r.proc != nil {
		if kerr := r.proc.Kill(); kerr != nil {
			a.log.Error("agy 终止执行者失败", "task", taskID, "cause", kerr)
			killErr = kerr
		}
	}
	restoreErr := RestoreTaskEnv(r.taskDir)
	if killErr != nil {
		if restoreErr != nil {
			a.log.Error("agy 终止失败且 hooks 还原失败", "task", taskID, "cause", restoreErr)
		}
		return killErr
	}
	a.drop(taskID)
	if restoreErr != nil {
		return restoreErr
	}
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
		if m.Init != nil && m.Init.Model != "" {
			r.actualModel = m.Init.Model
		}
		a.log.Info("agy 会话就绪", "task", r.taskID, "session", r.session)
		r.markReady()
		a.emit(r, executor.AdapterEvent{Type: "progress", SessionID: r.session, ActualModel: r.actualModel, Text: "会话就绪"})
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
				a.emit(r, executor.AdapterEvent{Type: "usage", ActualModel: r.actualModel, Usage: u})
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
	u := ParseUsage(res.Usage)
	spend, hasSpend := parseSpend(res.Usage, r.session)
	var spendPtr *proto.SpendEntry
	if hasSpend {
		spendPtr = &spend
	}
	if u != nil || spendPtr != nil {
		a.emit(r, executor.AdapterEvent{Type: "usage", ActualModel: r.actualModel, Usage: u, Spend: spendPtr})
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
			a.emit(r, executor.AdapterEvent{
				Type:      "result",
				SessionID: r.session,
				Result: &executor.Result{
					OK:         false,
					SessionID:  r.session,
					FailReason: "回合结束但零文本产出（可能是供应商流中断）；executor 仍在线，可 continue 续接重试",
					VoidReason: executor.VoidReasonTurnDiscipline,
				},
			})
			return
		}
		a.emit(r, executor.AdapterEvent{Type: "question", Text: turn.ClampQuestion(text)})
		return
	}
	a.log.Warn("兜底判定有新提交，但模型未宣布完成，转失败交协调者裁决",
		"task", r.taskID, "branch", branch, "commit", commit)
	a.emit(r, executor.AdapterEvent{
		Type:   "result",
		Result: turn.NoTrailerResult(r.session, branch, commit, text),
	})
}

func (a *Adapter) mapExit(r *runState, m streamMsg) {
	r.exitHandled = true
	r.markReady()
	a.log.Info("agy 进程已退出", "task", r.taskID, "exit_code", m.ExitCode)
	if !r.turnEnded {
		if m.ExitCode == 0 {
			r.turnMu.Lock()
			text := r.turnBuf.String()
			r.turnMu.Unlock()
			a.fallbackClassify(r, text)
		} else {
			tail := agyLogTail(r.taskDir)
			a.emit(r, executor.AdapterEvent{Type: "result", Result: &executor.Result{
				OK: false, SessionID: r.session,
				FailReason: fmt.Sprintf("agy 异常退出 (code %d): %s", m.ExitCode, tail),
			}})
		}
		r.clearTurn()
		r.turnEnded = true
	}
}

func (r *runState) clearTurn() {
	r.turnMu.Lock()
	r.turnBuf.Reset()
	r.lastProgress = time.Time{}
	r.turnMu.Unlock()
}

func (a *Adapter) maybeProgress(r *runState) {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	now := time.Now()
	if now.Sub(r.lastProgress) < progressThrottle {
		return
	}
	r.lastProgress = now
	text := turn.TruncateRunes(r.turnBuf.String(), 200)
	a.emit(r, executor.AdapterEvent{
		Type:      "progress",
		SessionID: r.session,
		Text:      text,
	})
}

func (a *Adapter) reportTiming(r *runState, entries []proto.TimingEntry) {
	for i := range entries {
		e := entries[i]
		a.emit(r, executor.AdapterEvent{
			Type:   "usage",
			Timing: &e,
		})
	}
}

func (a *Adapter) emit(r *runState, ev executor.AdapterEvent) {
	r.emitMu.Lock()
	defer r.emitMu.Unlock()
	if r.evClosed {
		return
	}
	select {
	case r.evCh <- ev:
	case <-r.stopCh:
	}
}

func (r *runState) closeEvents() {
	r.emitMu.Lock()
	defer r.emitMu.Unlock()
	if !r.evClosed {
		r.evClosed = true
		close(r.evCh)
	}
}
