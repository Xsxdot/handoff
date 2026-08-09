// adapter.go —— Claude Code 语义到 executor.Adapter 契约的翻译层。
//
// 职责：
//   - 五动作编排：Start（物料 → socket → tmux 进程 → 等 init → 投首回合 prompt）、
//     Send（fifo 续接）、RespondPermission（裁决回发 socket）、Stop（kill + 收摊）、
//     Events（事件通道）
//   - stream 消息 → AdapterEvent 映射：init → progress(SessionID)；text_delta →
//     render.log + 节流 progress；result → trailer 分类（ask→question / finish→result /
//     none→兜底 git 实况裁决）；handoff_exit 哨兵 → 失败 result
//   - 权限：perm.sock 的 ask 回调 → permission 事件（PermissionID = 裸 tool_use_id）
//
// 边界：
//   - 不写 store、不做审批判断（executor 包级边界）：会话 id 经事件交 manager 落库
//   - 不做状态机迁移：6 状态迁移全在 manager
//   - 不重试、不决策：解析宽容（未知消息 Debug 跳过、绝不 panic）
//
// 事件映射以 2026-08-08 真实采样与 2026-08-09 探针为准（claude 2.1.220/2.1.226）：
//   - 文本载体：stream_event.event.content_block_delta.delta 的 text_delta 是模型
//     正文增量（thinking_delta 是思考过程，隔离不进 render.log 与回合文本，与
//     opencode 的 reasoning 隔离一致）；assistant.message.content 的 text 块是整块
//     文本（render.log 已由 delta 写过则不重复追加，只做回合文本累积）
//   - 回合收尾：result.subtype=success 时 result.result 即最后一条 assistant 正文，
//     正是 turn.ParseTrailer 的输入；subtype!=success 时按失败处理
//   - 死亡：脚本在进程退出后往 out.jsonl 追加 handoff_exit 哨兵（带退出码），
//     随事件流天然送达本层，是本 adapter 唯一可靠的死亡信号
//   - 权限：完全走 socket 旁路（perm.go），out.jsonl 里那次 mcp__handoff__ask 的
//     tool_use 只当普通工具调用渲染，不产生 permission 事件（避免同一请求出两次）
package claudecode

import (
	"bytes"
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

	"github.com/google/uuid"
	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/turn"
)

// 心跳节流与权限描述防失控参数：
//   - progressThrottle：progress 事件节流，每任务至多每 30s 一条，防止高频文本增量
//     刷爆事件库（与 opencode 同值）
//   - permTextHardLimit：权限描述的**防失控**硬上限（64KB，与 opencode 同值）。
//     不是给审核者看的展示上限——AdapterEvent.Text 是黑名单扫描、模型审批与工单
//     全文的唯一真相源，展示截断由 manager 的 permEventText() 负责；本上限只防
//     失控输出（grok adapter 曾在此翻车：危险片段落在 200 字符截断之后被静默放行）
const (
	progressThrottle   = 30 * time.Second
	permTextHardLimit  = 64 << 10
	persistOffsetEvery = 5 * time.Second
)

// Adapter 是 claude 的 executor.Adapter 实现（语义翻译层）。
//
// 并发安全：runs 表由 mu 保护；每个任务的运行态（回合累积、事件通道）只被
// 该任务自己的 streamLoop goroutine 访问，不做跨任务共享。
type Adapter struct {
	log  *slog.Logger
	mu   sync.Mutex
	runs map[string]*runState // taskID -> 运行态
}

// New 创建 claude adapter。
//
// 参数：
//   - log: 本模块日志入口（nil 时退回 slog.Default()）
func New(log *slog.Logger) *Adapter {
	if log == nil {
		log = slog.Default()
	}
	return &Adapter{log: log, runs: make(map[string]*runState)}
}

// runState 是单任务运行的完整状态。
//
// evCh 的写入由 emitMu + evClosed 保护（streamLoop 与 Stop 竞态）；关闭权归
// streamLoop 的 defer（见 closeEvents）。ready 是 Start 等待「init 就绪」的信号，
// markReady 只关闭一次。回合累积文本 turnBuf 只在 streamLoop 内读写，无需加锁。
type runState struct {
	taskID       string
	taskDir      string
	repoPath     string
	session      string
	proc         *Proc
	perm         *permServer
	runCtx       context.Context
	runCancel    context.CancelFunc
	evCh         chan executor.AdapterEvent
	stopCh       chan struct{}
	stopOnce     sync.Once
	closeOnce    sync.Once
	renderPath   string
	emitMu       sync.Mutex // 保护 evCh 的写入与关闭
	evClosed     bool       // evCh 已关闭，emit 必须静默丢弃（防 send on closed channel）
	turnMu       sync.Mutex // 保护 turnBuf/lastProgress（maybeProgress 在 mapAssistant 调用）
	turnBuf      strings.Builder
	lastProgress time.Time
	startCommit  string // 本回合起点 commit（git 兜底分类的基线，每回合结束后刷新）
	turnEnded    bool   // 本回合是否已收尾（handoff_exit code=0 判断用）
	ready        chan struct{}
	readyOnce    sync.Once
}

// newRun 创建并登记一个任务的运行态。
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
	r.runCtx, r.runCancel = context.WithCancel(context.Background())
	a.mu.Lock()
	a.runs[taskID] = r
	a.mu.Unlock()
	return r
}

// lookup 按任务 id 取运行态。
func (a *Adapter) lookup(taskID string) *runState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.runs[taskID]
}

// drop 注销一个任务的运行态（启动失败清理用）。
func (a *Adapter) drop(taskID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.runs, taskID)
}

// markReady 标记会话就绪（init 事件到达），只关闭一次。
func (r *runState) markReady() {
	r.readyOnce.Do(func() { close(r.ready) })
}

// Start 按「perm server → 任务物料 → tmux 进程 → 等 init → 投首回合 prompt →
// 会话就绪」流程启动任务执行并立即返回。
//
// 参数：
//   - ctx: 控制启动阶段的超时/取消（tmux 启动、等 init 受其约束）；
//     不代表执行生命周期（执行延续到 Stop）
//   - req: 任务快照、计划原文与任务工作目录
//
// 返回：
//   - 任一启动阶段失败返回错误，调用方（manager）应把任务标记 failed
//
// 注意：
//   - 已建资源（perm server / tmux 会话）在失败时逐级回滚，避免半启动残留
//   - req.Env 必须原样透传进 StartProc（B19）：漏传编译照过、用户 env 静默失效
func (a *Adapter) Start(ctx context.Context, req executor.StartReq) (err error) {
	a.log.Info("claude adapter 开始启动任务", "task", req.Task.ID,
		"task_dir", req.TaskDir, "workdir", req.Task.Workdir())
	defer func() {
		if err != nil {
			a.log.Error("claude adapter 启动任务失败", "task", req.Task.ID, "cause", err)
		}
	}()

	sessionID := uuid.NewString()
	sockPath := filepath.Join(req.TaskDir, sockFileName)
	r := a.newRun(req.Task.ID, req.TaskDir, req.Task.Workdir())
	r.session = sessionID
	// 回滚顺序与创建顺序相反：先停 socket 受理、再 kill 进程、最后注销运行态
	rollback := func() {
		if r.perm != nil {
			r.perm.Close()
		}
		if r.proc != nil {
			r.proc.Kill()
		}
		r.runCancel()
		a.drop(req.Task.ID)
	}

	// 1. 裁决 socket：必须先于 claude 进程存在——claude 加载 mcp.json 会立刻拉起
	// permission-mcp 子进程连它，socket 未就绪会让子进程一直重试（fail-closed）
	perm, err := newPermServer(sockPath, a.log, func(ask permAsk) { a.onPermissionAsk(r, ask) })
	if err != nil {
		return err
	}
	r.perm = perm

	// 2. 任务物料（settings/mcp/prompt）；裁决 MCP server 的启动命令就是 handoff 自身
	bin, err := os.Executable()
	if err != nil {
		rollback()
		return fmt.Errorf("定位 handoff 二进制: %w", err)
	}
	settingsPath, mcpPath, promptText, err := WriteTaskEnv(req.TaskDir, req.Task.ID, req.PlanContent, sockPath, bin)
	if err != nil {
		rollback()
		return err
	}

	// 3. 进程：Env 必须原样透传（见 StartProcReq.Env 的注意）
	proc, err := StartProc(ctx, StartProcReq{
		RepoPath:     req.Task.Workdir(),
		TaskID:       req.Task.ID,
		TaskDir:      req.TaskDir,
		SessionID:    sessionID,
		Model:        req.Task.Model,
		SettingsPath: settingsPath,
		MCPPath:      mcpPath,
		Env:          req.Env,
	}, a.log)
	if err != nil {
		rollback()
		return err
	}
	r.proc = proc

	// 4. 事件流主循环（内部从 offset 0 起读，遇 init 关闭 r.ready）
	go r.streamLoop(a)

	// 5. 等 init（claude 冷启动要加载 settings/plugins/MCP 子进程，30s 上限）
	select {
	case <-r.ready:
		a.log.Info("claude 就绪", "task", req.Task.ID, "session", r.session)
	case <-time.After(startReadyTimeout):
		tail := claudeLogTail(req.TaskDir)
		rollback()
		a.log.Error("claude 就绪超时", "task", req.Task.ID, "stderr_tail", tail)
		return fmt.Errorf("claude 就绪超时（%s）: %s", startReadyTimeout, tail)
	case <-ctx.Done():
		rollback()
		return fmt.Errorf("启动被取消: %w", ctx.Err())
	}

	// 6. 投首回合 prompt（plan 全文 + 回合纪律，turn.RenderPrompt 产物）
	if err := proc.WriteInput(promptText); err != nil {
		rollback()
		return fmt.Errorf("投递首回合 prompt: %w", err)
	}
	a.log.Info("claude 初始 prompt 已投递", "task", req.Task.ID, "prompt_len", len(promptText))

	// 7. 记录任务起点 commit（git 兜底分类的基线）并补发「会话就绪」信号
	r.captureStartCommit(a)
	a.emit(r, executor.AdapterEvent{Type: "progress", SessionID: sessionID, Text: "会话就绪"})
	return nil
}

// captureStartCommit 记录任务起点 commit：git 兜底分类（fallbackClassify）用
// 「是否有新提交」裁决的基线；非 git 仓库或查询失败时留空，兜底一律按无新提交处理。
func (r *runState) captureStartCommit(a *Adapter) {
	if out, err := exec.Command("git", "-C", r.repoPath, "rev-parse", "HEAD").Output(); err != nil {
		a.log.Warn("捕获任务起点 commit 失败，兜底按无新提交处理",
			"task", r.taskID, "repo", r.repoPath, "cause", err)
	} else {
		r.startCommit = strings.TrimSpace(string(out))
	}
}

// Events 返回任务的事件流通道（Start 后可用；Stop 或执行终结后关闭）。
//
// 注意（P1-11 同款）：任务不在运行（未启动/已 Stop/运行态已随终结注销）时返回
// **已关闭的通道**而非 nil——契约是「通道关闭 = 执行终结」，nil 会让 for-range 永久阻塞。
func (a *Adapter) Events(taskID string) <-chan executor.AdapterEvent {
	if r := a.lookup(taskID); r != nil {
		return r.evCh
	}
	ch := make(chan executor.AdapterEvent)
	close(ch)
	return ch
}

// Send 往同一会话续发指令（fifo 续接：上下文完整保留）。
//
// 参数：
//   - text: 审核者的回答/修改指令，原样透传，不得加工
//
// 注意：
//   - 进程不在（fifo 无读端，O_NONBLOCK 打开失败）时包装 executor.ErrTaskNotRunning
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
	a.log.Info("claude adapter 收到续接指令", "task", taskID, "text", turn.TruncateRunes(text, 80))
	defer func() {
		if err != nil {
			a.log.Error("claude adapter 续接指令发送失败", "task", taskID, "cause", err)
		} else {
			a.log.Info("claude adapter 续接指令已发送", "task", taskID)
		}
	}()
	if r.proc == nil {
		return fmt.Errorf("任务 %s: %w", taskID, executor.ErrTaskNotRunning)
	}
	if err := r.proc.WriteInput(text); err != nil {
		return fmt.Errorf("任务 %s: %w", taskID, executor.ErrTaskNotRunning)
	}
	return nil
}

// RespondPermission 把审核者的权限裁决回发到 perm.sock。
//
// 参数：
//   - permID: 与 permission 事件中的 PermissionID 一致（manager 还原后的裸 tool_use_id）
//   - decision: "once"（批准本次）或 "reject"（拒绝）
//
// 注意：
//   - decision 取值**除 once 外一律 deny**：不认识的裁决绝不当成放行
//     （与 manager 侧 gateDecision「allow 之外一律 reject」同一条纪律）
//   - 找不到挂起请求（进程已死 / 请求已被重试替换）时按 ErrTaskNotRunning 处置
func (a *Adapter) RespondPermission(ctx context.Context, taskID, permID, decision string) (err error) {
	r := a.lookup(taskID)
	if r == nil {
		return fmt.Errorf("任务 %s: %w", taskID, executor.ErrTaskNotRunning)
	}
	select {
	case <-r.stopCh:
		return fmt.Errorf("任务 %s 已停止，不能应答权限", taskID)
	default:
	}
	a.log.Info("claude adapter 收到权限应答", "task", taskID, "perm", permID, "decision", decision)
	defer func() {
		if err != nil {
			a.log.Error("claude adapter 权限应答转发失败", "task", taskID, "perm", permID, "cause", err)
		} else {
			a.log.Info("claude adapter 权限应答已转发", "task", taskID, "perm", permID)
		}
	}()
	if r.perm == nil {
		return fmt.Errorf("任务 %s: %w", taskID, executor.ErrTaskNotRunning)
	}
	behavior, msg := "allow", ""
	if decision != "once" {
		behavior, msg = "deny", "审核者拒绝了本次操作"
	}
	return r.perm.Respond(permID, behavior, msg)
}

// Stop 终止任务执行：停 socket 受理 → kill tmux 会话 → 事件通道关闭 → 注销运行态。
//
// 注意：
//   - 幂等：重复 Stop 不 panic；事件通道只关闭一次（由 streamLoop 的 defer 持有关闭权）
//   - kill 失败时仍注销运行态（claude 没有 opencode 那类必须保句柄的端口/凭据回收场景，
//     tmux 会话残留由人工 attach/kill-session 兜底，与 opencode Kill 同规则）
func (a *Adapter) Stop(taskID string) (err error) {
	r := a.lookup(taskID)
	if r == nil {
		return fmt.Errorf("任务 %s: %w", taskID, executor.ErrTaskNotRunning)
	}
	a.log.Info("claude adapter 停止任务", "task", taskID)
	defer func() {
		if err != nil {
			a.log.Error("claude adapter 停止任务失败", "task", taskID, "cause", err)
		} else {
			a.log.Info("claude adapter 任务已停止", "task", taskID)
		}
	}()
	// 先关 stopCh（让 emit 让路）再取消运行 ctx（打断 streamLoop）
	r.stopOnce.Do(func() {
		close(r.stopCh)
		r.runCancel()
	})
	if r.perm != nil {
		r.perm.Close()
	}
	if r.proc != nil {
		if kerr := r.proc.Kill(); kerr != nil {
			return kerr
		}
	}
	a.drop(taskID)
	return nil
}

// streamLoop 是单任务的事件流主循环（唯一持有 evCh 关闭权的 goroutine）：
// 从 offset 0 起读 out.jsonl → mapMessage 逐条映射产出 AdapterEvent；顺带每
// persistOffsetEvery 持久化一次 claude.json 的 offset（agentd 重启后续读的依据）。
func (r *runState) streamLoop(a *Adapter) {
	defer func() {
		r.closeOnce.Do(r.closeEvents)
		select {
		case <-r.stopCh:
		default:
			a.drop(r.taskID)
		}
		a.log.Info("claude 事件流退出", "task", r.taskID)
	}()
	tl := newTailer(filepath.Join(r.taskDir, outFileName), 0, a.log)
	var lastPersist time.Time
	err := tl.Run(r.runCtx, func(m streamMsg) {
		// offset 持久化节流：高频文本增量下不每行都写盘
		if now := time.Now(); now.Sub(lastPersist) >= persistOffsetEvery {
			if r.session != "" && r.proc != nil {
				if perr := writeProcInfo(r.taskDir, &procInfo{
					TmuxSession: r.proc.TmuxSession, SessionID: r.session, Offset: tl.Offset(),
				}); perr != nil {
					a.log.Warn("持久化 claude offset 失败", "task", r.taskID, "cause", perr)
				}
				lastPersist = now
			}
		}
		a.mapMessage(r, m)
	})
	select {
	case <-r.stopCh:
		return // 正常 Stop 关停：静默退出，不产事件
	default:
	}
	tail := claudeLogTail(r.taskDir)
	if err != nil {
		a.log.Error("claude 事件流损坏", "task", r.taskID, "cause", err)
		a.emit(r, executor.AdapterEvent{Type: "result", Result: &executor.Result{
			OK: false, SessionID: r.session,
			FailReason: "claude 事件流损坏: " + turn.TailRunes(fmt.Sprint(err), 200),
		}})
		return
	}
	a.log.Warn("claude 事件流意外中断，按失败结束回合", "task", r.taskID, "stderr_tail", tail)
	a.emit(r, executor.AdapterEvent{Type: "result", Result: &executor.Result{
		OK: false, SessionID: r.session,
		FailReason: "claude 事件流意外中断: " + tail,
	}})
}

// mapMessage 是事件映射唯一入口（表见 spec §4.2）。宽容解析：未知消息 Debug 跳过。
func (a *Adapter) mapMessage(r *runState, m streamMsg) {
	switch {
	case m.Type == "system" && m.Subtype == "init":
		a.log.Info("claude 会话就绪", "task", r.taskID, "session", m.SessionID)
		r.session = m.SessionID
		r.markReady()
		a.emit(r, executor.AdapterEvent{Type: "progress", SessionID: m.SessionID, Text: "会话就绪"})
	case m.Type == "system":
		// thinking_tokens 等系统副消息：仅留痕
		a.log.Debug("system 消息跳过", "task", r.taskID, "subtype", m.Subtype)
	case m.Type == "stream_event":
		a.mapStreamEvent(r, m.Event)
	case m.Type == "assistant":
		a.mapAssistant(r, m.Message)
	case m.Type == "user":
		a.mapUserMessage(r, m.Message)
	case m.Type == "result":
		a.mapResult(r, m)
	case m.Type == "handoff_exit":
		a.mapExit(r, m)
	case m.Type == "rate_limit_event":
		a.log.Debug("限流事件跳过", "task", r.taskID)
	default:
		a.log.Debug("未知消息类型，跳过", "task", r.taskID, "type", m.Type)
	}
}

// mapStreamEvent 处理流式增量：text_delta 追加 render.log（实况流式来源），
// 不产生 AdapterEvent（spec §4.2）；thinking_delta 被 textDelta 过滤掉。
func (a *Adapter) mapStreamEvent(r *runState, ev json.RawMessage) {
	text, ok := textDelta(ev)
	if !ok {
		return
	}
	if err := turn.AppendRender(r.renderPath, text); err != nil {
		a.log.Warn("追加 render.log 失败", "task", r.taskID, "cause", err)
	}
}

// mapAssistant 处理 assistant 整块消息：text 块做回合文本累积（供收尾分类与
// progress），tool_use 块往 render.log 追加动作摘要。
//
// why（text 块不重复追加 render.log）：render.log 已由 text_delta 增量写过，
// 整块再写会出两遍正文；这里只累积进 turnBuf 供分类用。
func (a *Adapter) mapAssistant(r *runState, msg json.RawMessage) {
	var m struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if err := json.Unmarshal(msg, &m); err != nil {
		a.log.Debug("assistant 消息解析失败，跳过", "task", r.taskID, "cause", err)
		return
	}
	for _, block := range m.Content {
		switch block.Type {
		case "text":
			if block.Text == "" {
				continue
			}
			r.turnMu.Lock()
			r.turnBuf.WriteString(block.Text)
			r.turnMu.Unlock()
			a.maybeProgress(r)
		case "tool_use":
			a.appendActionSummary(r, block.Name, block.Input)
		}
	}
}

// appendActionSummary 往 render.log 追加一行工具动作摘要（tmux 窗口 1 的旁观内容）。
func (a *Adapter) appendActionSummary(r *runState, toolName string, input json.RawMessage) {
	line := "→ " + toolName
	if toolName == "Bash" {
		var in struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(input, &in) == nil && in.Command != "" {
			line += ": " + firstLine(in.Command)
		}
	} else {
		line += ": " + compactJSON(input)
	}
	if err := turn.AppendRender(r.renderPath, "\n"+line+"\n"); err != nil {
		a.log.Warn("追加 render.log 失败", "task", r.taskID, "cause", err)
	}
}

// mapUserMessage 处理 user 消息：tool_result 块往 render.log 追加结果摘要。
func (a *Adapter) mapUserMessage(r *runState, msg json.RawMessage) {
	var m struct {
		Content []struct {
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
		} `json:"content"`
	}
	if err := json.Unmarshal(msg, &m); err != nil {
		a.log.Debug("user 消息解析失败，跳过", "task", r.taskID, "cause", err)
		return
	}
	for _, block := range m.Content {
		if block.Type != "tool_result" {
			continue
		}
		summary := compactJSON(block.Content)
		if s, ok := jsonString(block.Content); ok {
			summary = firstLine(s)
		}
		line := "↩ " + turn.TruncateRunes(summary, 200)
		if err := turn.AppendRender(r.renderPath, "\n"+line+"\n"); err != nil {
			a.log.Warn("追加 render.log 失败", "task", r.taskID, "cause", err)
		}
	}
}

// mapResult 处理回合收尾：result.result 是最后一条 assistant 正文，正是
// turn.ParseTrailer 的输入；subtype!=success 时按失败处理（带 claude.log 尾部）。
func (a *Adapter) mapResult(r *runState, m streamMsg) {
	if m.Subtype != "success" || m.IsError {
		tail := claudeLogTail(r.taskDir)
		a.log.Error("claude 回合异常结束", "task", r.taskID, "subtype", m.Subtype, "stderr_tail", tail)
		a.emit(r, executor.AdapterEvent{Type: "result", Result: &executor.Result{
			OK: false, SessionID: r.session,
			FailReason: "claude 回合异常结束（subtype=" + m.Subtype + "）: " + tail,
		}})
		r.turnEnded = true
		return
	}
	text := m.Result
	if strings.TrimSpace(text) == "" {
		r.turnMu.Lock()
		text = r.turnBuf.String()
		r.turnMu.Unlock()
	}
	kind, tr := turn.ParseTrailer(text)
	switch kind {
	case "ask":
		a.emit(r, executor.AdapterEvent{Type: "question", Text: turn.ClampQuestion(tr.Question)})
	case "finish":
		a.emit(r, executor.AdapterEvent{Type: "result", Result: &executor.Result{
			OK: true, Branch: tr.Branch, CommitHash: tr.Commit,
			Summary: tr.Summary, SessionID: r.session,
		}})
	default:
		a.fallbackClassify(r, text)
	}
	r.clearTurn()
	// 兜底分类的 git 基线按回合刷新（C-1）：基线若固定在 run 起点，第一回合提交之后
	// 每个无 trailer 回合都会「相对起点有新提交」谎报 completed
	r.captureStartCommit(a)
	r.turnEnded = true
}

// fallbackClassify 是「模型未按纪律输出协议 trailer」的兜底分类。
//
// why（兜底分类规则）：回合结束但 turn.ParseTrailer 判 none——模型可能干完活却
// 忘了写 {"branch":...} 协议。拿 git 实况裁决：相对任务起点有新 commit → 认定
// 干完了（result OK，summary 取回合末 200 字符）；没有新 commit → 把回合全文交给
// 审核者裁决（question），流程不卡死。
func (a *Adapter) fallbackClassify(r *runState, text string) {
	a.log.Warn("回合未输出协议 trailer，走 git 兜底", "task", r.taskID,
		"turn_tail", turn.TailRunes(text, 120))
	branch, commit, hasNew, err := turn.GitTurnStatus(r.repoPath, r.startCommit)
	if err != nil || !hasNew {
		if err != nil {
			a.log.Error("git 兜底查询失败", "task", r.taskID, "cause", err)
		}
		a.log.Info("兜底判定无新提交，转提问交审核者裁决", "task", r.taskID, "has_new", hasNew)
		a.emit(r, executor.AdapterEvent{Type: "question", Text: turn.ClampQuestion(text)})
		return
	}
	a.emit(r, executor.AdapterEvent{Type: "result", Result: &executor.Result{
		OK: true, Branch: branch, CommitHash: commit,
		Summary: turn.TailRunes(text, 200), SessionID: r.session,
	}})
}

// mapExit 处理死亡哨兵：code=0 且本回合已收尾视为正常终结（result 已产出，
// 不再产事件）；否则按失败结束，FailReason 带退出码与 claude.log 尾部。
func (a *Adapter) mapExit(r *runState, m streamMsg) {
	if m.ExitCode == 0 && r.turnEnded {
		a.log.Info("claude 进程正常退出（回合已收尾）", "task", r.taskID, "code", m.ExitCode)
	} else {
		tail := claudeLogTail(r.taskDir)
		a.log.Error("claude 进程退出", "task", r.taskID, "code", m.ExitCode, "stderr_tail", tail)
		a.emit(r, executor.AdapterEvent{Type: "result", Result: &executor.Result{
			OK: false, SessionID: r.session,
			FailReason: fmt.Sprintf("claude 进程退出（code=%d）: %s", m.ExitCode, tail),
		}})
	}
	// 无论正常与否，进程都已不在：回收 tmux 会话（窗口 1 的 tail 会一直吊着会话）
	// 并结束事件流
	if r.proc != nil {
		if kerr := r.proc.Kill(); kerr != nil {
			a.log.Warn("哨兵后回收 tmux 会话失败", "task", r.taskID, "cause", kerr)
		}
	}
	r.runCancel()
}

// onPermissionAsk 把 perm.sock 的 ask 请求转成 permission 事件。
//
// Text = "<ToolName>: <关键入参>"：Bash 取 command，Edit/Write 取 file_path，
// 其余取 input 的紧凑 JSON。**不在本层做展示级截断**（黑名单扫描/模型审批/工单
// 全文都以 Text 为真相源），只有 64KB 防失控硬上限；空描述给兜底文本。
func (a *Adapter) onPermissionAsk(r *runState, ask permAsk) {
	text := permText(ask.ToolName, ask.Input)
	if strings.TrimSpace(text) == "" {
		a.log.Warn("claude 权限请求无可读描述，按未说明权限交审核者",
			"task", r.taskID, "perm", ask.ToolUseID)
		text = "claude 未提供可读描述（tool_use_id " + ask.ToolUseID + "），请 tmux attach 查看现场"
	}
	a.emit(r, executor.AdapterEvent{
		Type: "permission", PermissionID: ask.ToolUseID,
		Text: turn.TruncateMarked(text, permTextHardLimit),
	})
}

// permText 组装权限描述文本（spec §5.3）。
func permText(toolName string, input json.RawMessage) string {
	switch toolName {
	case "Bash":
		var in struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(input, &in) == nil && in.Command != "" {
			return "Bash: " + in.Command
		}
	case "Edit", "Write":
		var in struct {
			FilePath string `json:"file_path"`
		}
		if json.Unmarshal(input, &in) == nil && in.FilePath != "" {
			return toolName + ": " + in.FilePath
		}
	}
	return toolName + ": " + compactJSON(input)
}

// maybeProgress 节流发 progress：每任务至多每 progressThrottle 一条（与 opencode 同款）。
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

// clearTurn 清空回合累积（回合分类终结时调用）。
func (r *runState) clearTurn() {
	r.turnMu.Lock()
	r.turnBuf.Reset()
	r.turnMu.Unlock()
}

// emit 向事件通道投递一条 AdapterEvent 并打产出日志（所有事件统一的出口）。
//
// 并发：streamLoop 与可能并发触发的应答路径（本实现中仅 streamLoop）写通道，
// 统一由 emitMu 串行化，evClosed 使关闭后的迟到投递静默丢弃而非 panic。
func (a *Adapter) emit(r *runState, ev executor.AdapterEvent) bool {
	switch ev.Type {
	case "permission":
		a.log.Info("claude 产出权限事件", "task", r.taskID, "type", ev.Type,
			"perm", ev.PermissionID, "text", turn.TruncateRunes(ev.Text, 120))
	case "question":
		a.log.Info("claude 产出提问事件", "task", r.taskID, "type", ev.Type,
			"text", turn.TruncateRunes(ev.Text, 80))
	case "progress":
		a.log.Info("claude 产出进度事件", "task", r.taskID, "type", ev.Type,
			"text", turn.TruncateRunes(ev.Text, 80))
	case "result":
		ok := ev.Result != nil && ev.Result.OK
		a.log.Info("claude 产出结果事件", "task", r.taskID, "type", ev.Type, "ok", ok)
	default:
		a.log.Info("claude 产出未知事件", "task", r.taskID, "type", ev.Type)
	}
	r.emitMu.Lock()
	defer r.emitMu.Unlock()
	if r.evClosed {
		a.log.Debug("事件通道已关闭，丢弃迟到事件", "task", r.taskID, "type", ev.Type)
		return false
	}
	select {
	case r.evCh <- ev:
		return true
	case <-r.stopCh:
		return false
	}
}

// closeEvents 关闭事件通道（只由 streamLoop 的 defer 调用，关闭权唯一）。
func (r *runState) closeEvents() {
	r.emitMu.Lock()
	defer r.emitMu.Unlock()
	if r.evClosed {
		return
	}
	r.evClosed = true
	close(r.evCh)
}

// claudeLogTail 读 claude.log 尾部最多 500 字节，供就绪超时/死亡诊断。
//
// why（读文件而非 capture-pane）：claude 所在窗格随命令退出关闭，capture-pane 读不到
// 已关闭窗格；claude.log 是 tee 落盘的持久副本，进程死后仍可读。
func claudeLogTail(taskDir string) string {
	f, err := os.Open(filepath.Join(taskDir, stderrFileName))
	if err != nil {
		return ""
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	const n = 4 << 10
	offset := fi.Size() - n
	if offset < 0 {
		offset = 0
	}
	buf := make([]byte, fi.Size()-offset)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return ""
	}
	return tail(string(buf), 500)
}

// compactJSON 把任意 JSON 压成一行（render.log 摘要与权限描述用）。
func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

// jsonString 尝试把 JSON 值当作字符串读出。
func jsonString(raw json.RawMessage) (string, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// firstLine 取字符串首行（render.log 摘要用）。
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
