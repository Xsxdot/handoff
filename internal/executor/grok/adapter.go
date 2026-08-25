// adapter.go —— ACP 语义到 executor.Adapter 契约的翻译层。
//
// 职责：
//   - 把 StartServe / DialACP / initialize / session.new / session.prompt 编排成
//     Adapter 的五动作
//   - ACP 消息 → AdapterEvent 映射：session/request_permission → permission 事件；
//     agent_message_chunk 累积成回合正文（thought 与 tool_call 只进 render.log）；
//     session/prompt 的响应（stopReason）作回合边界 → turn.ParseTrailer 分类
//   - 可见性：回合文本增量追加到 <taskDir>/render.log，供 handoff attach 旁观
//
// 边界：
//   - 不写 store、不做审批判断（见 executor.go 包级边界）：会话 id 等持久化诉求
//     经事件（progress「会话就绪」/ Result.SessionID）交 manager 落库
//   - 不做任务状态机迁移：6 状态迁移完全由 manager 负责
//
// 与 opencode adapter 的两处结构性差异：
//   - 回合边界是 session/prompt 的**响应**而非从 idle 事件推断，因此不需要
//     opencode 的 idleGrace 去抖与 scheduleIdle/resolveIdle/cancelPendingIdle 竞态处理
//   - 权限是阻塞式 JSON-RPC 请求，需维护 permID → 请求 id 的挂起表（见 perm.go）
package grok

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/turn"
	"github.com/Xsxdot/handoff/internal/prochost"
	"github.com/Xsxdot/handoff/internal/proto"
)

const (
	progressThrottle = 30 * time.Second // 与 opencode 同值：防高频增量刷爆事件库
	// permTextHardLimit 是权限描述的**防失控**硬上限（不是给协调者看的上限）。
	//
	// adapter 发出的 AdapterEvent.Text 是权限描述的唯一真相源，manager 拿它做三件事：
	//   - shouldConsultApprover 的黑名单正则扫描（命中即跳过审批者升级人工）
	//   - 第 1 层模型审批者 Decide 的输入
	//   - 权限工单里保存的全文（协调者裁决的依据）
	// 展示用的短截断由 manager 的 permEventText() 单独负责，只作用于事件 payload，
	// 且带显式截断标记。**在 adapter 层提前砍短会让黑名单只扫到截断前缀，危险片段
	// 落在其后即静默放行**——这是 B6 修掉过的根因，不能在此复活。64KB 只防失控输出。
	permTextHardLimit = 64 << 10
)

// Adapter 是 grok 的 executor.Adapter 实现。
//
// 并发安全：runs 表由 mu 保护；每个任务的运行态只被该任务自己的回调路径访问。
type Adapter struct {
	log  *slog.Logger
	mu   sync.Mutex
	runs map[string]*runState
}

// managedTaskTmpEnv derives the task-private environment from TaskDir.
//
// The managed entries are appended after user entries at the process seam so
// handoff's isolation values cannot be overridden by task configuration.
func managedTaskTmpEnv(taskDir, taskID string) (string, []string) {
	dataDir := filepath.Dir(filepath.Dir(taskDir))
	if filepath.Base(filepath.Dir(taskDir)) != "tasks" {
		// Unit seams may use a direct temporary TaskDir; production manager
		// requests always use <DataDir>/tasks/<id>.
		dataDir = filepath.Dir(taskDir)
	}
	tmpDir := executor.TaskTmpDir(dataDir, taskID)
	return tmpDir, []string{
		"TMPDIR=" + tmpDir,
		"GOTMPDIR=" + tmpDir,
		"GOCACHE=" + filepath.Join(tmpDir, "gocache"),
	}
}

// ensureTaskTmp creates the task-private directory immediately before a new
// process is started; hot reattach paths must not create it.
func ensureTaskTmp(taskID, tmpDir string, log *slog.Logger) error {
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		log.Error("创建任务临时目录失败", "task", taskID, "tmp_dir", tmpDir, "cause", err)
		return err
	}
	log.Info("任务临时目录已就绪", "task", taskID, "tmp_dir", tmpDir)
	return nil
}

// New 创建 grok adapter。
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
type runState struct {
	taskID      string
	taskDir     string
	repoPath    string
	sessionID   string
	startCommit string

	proc *Proc
	cli  *ACPClient

	// lastAuthSync 是上一次凭据巡检的时刻，用于把巡检节流到 authSyncInterval。
	//
	// **刻意不加锁**：只被该任务自己的看门狗 goroutine 读写，无竞争。别顺手把它
	// 塞进 turnMu 的保护范围——那会把一个无竞争的字段绑到高频回合锁上。
	lastAuthSync time.Time

	// stopping 是主动停止标记：Stop 先置位再关连接，onClosed 据此知道这是用户
	// 主动停止而非执行失败，不产出「ACP 连接断开」的假失败结果
	stopping bool

	evCh     chan executor.AdapterEvent
	emitMu   sync.Mutex
	evClosed bool

	turnMu       sync.Mutex
	acc          *turnAccumulator
	lastProgress time.Time
	rejected     []string // 本回合被拒的权限描述（perm.go 写入，回合收尾交代）
	// askedViaTool 记「本回合已经通过原生 ask_user_question 给协调者递过问题」。
	// 收尾兜底据此不再把回合叙述文本补成第二张工单（见 finishTurn 的 default 分支）。
	askedViaTool bool

	// ctxWindow 是当前模型的上下文窗口上限，由 _x.ai/models/update 带来（0=未知）。
	// 为什么要暂存：分子与分母来自**不同的帧**——窗口在会话建立后立刻到，
	// 占用在每次模型调用后到。只发分子的话分母永远补不上
	//（manager 的「nil=不更新」保护的是已落库的值，不是从没落过库的值）。
	ctxWindow int
	// actualModel 是 grok 报回的实际模型名（同上，随用量一起发出去）。
	actualModel string

	frames   *turn.FrameWriter // 结构化回合帧；构造失败时为 nil，方法对 nil 安全
	seg      *turn.Segmenter   // 耗时段切分器；与 frames 同款 nil 安全约定
	textPart string            // 本回合正文/思维链的 part 标识

	pendMu  sync.Mutex
	pending map[string]pendingPerm // toolCallId -> 待裁决权限（perm.go 使用）
}

// pendingPerm 是挂起表中一条待裁决的权限请求。
//
// reqID 是应答回发必需的 ACP 请求 id；desc 是给人看的权限描述——RespondPermission
// 拒绝时用它记入被拒清单（用 toolCallId 会让协调者看到一串不透明 id，等于没说清
// 模型刚才想干什么）。
type pendingPerm struct {
	reqID json.RawMessage
	desc  string
}

// newRun 造一个 grok 运行态。
//
// **唯一构造点**：首发（Start）与冷恢复（Resume）都必须经这里。它们曾经各搓
// 一份字面量，代价是每次给 runState 加可见性字段，都只有首发那份被改到——
// frames 漏了一次（结构化帧在恢复后整轮消失），seg 又漏了一次（耗时账目在
// 恢复后整轮为空）。两次都无声：这两个字段对 nil 接收者都是空操作，漏了不报错、
// 不留日志，只是数据从此不再产生。另外三家 executor 一直只有一个构造点，
// 所以从没犯过这个错——这是形状问题，不是手误。
//
// 参数：proc 是已起好的 serve 进程；sessionID 由调用方在返回后按各自来源赋值
// （首发是 session/new 的结果，恢复是任务记录里的旧会话）。
//
// 注意：本函数**不**把 r 登记进 a.runs——两个调用点的登记时机不同（恢复要替换
// 先前的占位），登记留给调用方。
func (a *Adapter) newRun(taskID, taskDir, repoPath string, proc *Proc) *runState {
	r := &runState{
		taskID: taskID, taskDir: taskDir, repoPath: repoPath,
		proc: proc, evCh: make(chan executor.AdapterEvent, 64),
		acc: newTurnAccumulator(), pending: map[string]pendingPerm{},
	}
	// 帧写入器构造失败不挡任务：可见性是增强能力，方法对 nil 接收者是空操作
	fw, err := turn.WriterFor(taskDir, a.log)
	if err != nil {
		a.log.Warn("创建帧写入器失败，本任务无结构化帧", "task", taskID, "cause", err)
	}
	r.frames = fw
	// 段切分器不依赖文件 IO，构造不会失败，与 frames 的 nil 兜底无关。
	// 回合号取自 frames（见 FrameWriter.Turn 注释），恢复后从磁盘续号，
	// 不会撞掉上一次运行的账目键
	r.seg = turn.NewSegmenter(nil)
	// 回合起点 commit：兜底分类要靠「是否有新提交」这个事实裁决
	if _, c, _, gerr := turn.GitTurnStatus(repoPath, ""); gerr == nil {
		r.startCommit = c
	} else {
		a.log.Warn("读取回合起点 commit 失败，兜底裁决将退化", "task", taskID, "cause", gerr)
	}
	return r
}

func renderStartPrompt(taskID, planContent, disciplineBlock string) (string, error) {
	return turn.RenderPrompt(taskID, planContent, disciplineBlock)
}

// Start 异步启动执行并立即返回。
//
// 步骤：物料与 serve（StartServe）→ ACP 连接 → initialize → session/new →
// 不等待地发 session/prompt → emit progress{SessionID}「会话就绪」。
//
// 注意：session/prompt 要跑完一整个回合才响应，因此用 CallAsync 并由独立
// goroutine 等待其终局作回合边界。
func (a *Adapter) Start(ctx context.Context, req executor.StartReq) (err error) {
	taskID := req.Task.ID
	start := time.Now()
	a.log.Info("grok 启动任务", "task", taskID, "repo", req.Task.Workdir(),
		"task_dir", req.TaskDir, "model", req.Task.Model)
	defer func() {
		if err != nil {
			a.log.Error("grok 启动任务失败", "task", taskID, "cause", err)
		}
	}()

	tmpDir, managedEnv := managedTaskTmpEnv(req.TaskDir, taskID)
	if err := ensureTaskTmp(taskID, tmpDir, a.log); err != nil {
		return fmt.Errorf("创建任务临时目录 %s: %w", tmpDir, err)
	}
	env := append(append([]string{}, req.Env...), managedEnv...)
	proc, err := startServe(ctx, req.Task.Workdir(), taskID,
		prochost.ResolveMarkRoot(req.Task.Workdir(), req.Task.WorktreeManaged),
		req.TaskDir, req.Task.Model, env, a.log)
	if err != nil {
		return err
	}
	// 之后任一步失败都要回收 serve，否则留下一个没人管的执行者进程
	defer func() {
		if err != nil {
			_ = proc.Kill()
		}
	}()

	r := a.newRun(taskID, req.TaskDir, req.Task.Workdir(), proc)

	cli, err := DialACP(ctx, proc.WSURL(), &acpHandler{a: a, r: r}, a.log)
	if err != nil {
		return err
	}
	r.cli = cli
	defer func() {
		if err != nil {
			_ = cli.Close()
		}
	}()

	if err := a.openSession(ctx, r, req.Task.Workdir()); err != nil {
		return err
	}

	prompt, err := renderStartPrompt(taskID, req.PlanContent, req.Discipline)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.Discipline) == "" {
		a.log.Info("grok 未注入纪律块", "task", taskID)
	} else {
		a.log.Info("grok 纪律块已注入 prompt", "task", taskID, "bytes", len(req.Discipline))
	}
	if err := r.frames.BeginTurn("dispatch", ""); err != nil {
		a.log.Warn("写 turn_start 帧失败，不影响回合", "task", taskID, "cause", err)
	}
	a.reportTiming(r, r.seg.BeginTurn(r.frames.Turn()))
	r.textPart = r.frames.NextPart()
	// 不等待：session/prompt 要跑完一整个回合才响应，Start 必须立即返回
	resCh, err := cli.CallAsync("session/prompt", map[string]any{
		"sessionId": r.sessionID,
		"prompt":    []any{map[string]any{"type": "text", "text": prompt}},
	})
	if err != nil {
		return fmt.Errorf("ACP session/prompt: %w", err)
	}

	a.mu.Lock()
	a.runs[taskID] = r
	a.mu.Unlock()

	go a.awaitTurn(r, resCh)
	go a.watchdog(r)

	// 「会话就绪」信号：审核主路径常以 question 收尾、result 永不出现，
	// progress 是会话 id 到达 manager 的可靠通道
	a.emit(r, executor.AdapterEvent{Type: "progress", SessionID: r.sessionID,
		Text: "grok 会话已就绪"})
	a.log.Info("grok 任务已启动", "task", taskID, "session", r.sessionID,
		"port", proc.Port, "elapsed_ms", time.Since(start).Milliseconds())
	return nil
}

// openSession 完成 ACP 握手与会话建立：initialize + session/new。
//
// 单独抽出：TestSessionNewAuthErrorGivesActionableMessage 需要不起 serve 进程
// 地打到「凭据错误 → 可操作指引」这条路径。
func (a *Adapter) openSession(ctx context.Context, r *runState, cwd string) error {
	a.log.Info("grok ACP 会话建立中", "task", r.taskID, "cwd", cwd)
	if _, err := r.cli.Call(ctx, "initialize", initializeParams()); err != nil {
		return fmt.Errorf("ACP initialize: %w", err)
	}
	newRes, err := r.cli.Call(ctx, "session/new", map[string]any{
		"cwd": cwd, "mcpServers": []any{},
	})
	if err != nil {
		// 凭据问题重试一万次也不会好，给出可操作的指引（spec §8）
		if strings.Contains(err.Error(), "Authentication required") {
			return fmt.Errorf("grok 未登录或凭据已失效，请在本机执行 `grok login` 后重试: %w", err)
		}
		return fmt.Errorf("ACP session/new: %w", err)
	}
	var sess struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(newRes, &sess); err != nil || sess.SessionID == "" {
		return fmt.Errorf("ACP session/new 未返回 sessionId: %s", newRes)
	}
	r.sessionID = sess.SessionID
	a.log.Info("grok ACP 会话已建立", "task", r.taskID, "session", r.sessionID)
	return nil
}

// Events 返回该任务的事件流通道（Start 后可用）。通道关闭表示执行终结。
func (a *Adapter) Events(taskID string) <-chan executor.AdapterEvent {
	r := a.lookup(taskID)
	if r == nil {
		return nil
	}
	return r.evCh
}

// Send 回答提问 / 回发修改指令，对同一会话续接执行。text 原样透传不加工。
func (a *Adapter) Send(ctx context.Context, taskID, text string) error {
	r := a.lookup(taskID)
	if r == nil {
		return fmt.Errorf("任务 %s 无运行态: %w", taskID, executor.ErrTaskNotRunning)
	}
	// 事件通道已关闭 = 这条运行态已被 fatal 路径判死。此时开新回合是最坏的
	// 结果：session/prompt 发得出去、模型真的会跑，但产出的一切事件都会在
	// emit 里被 evClosed 短路丢弃，任务停在 running 直到 2h 看门狗（B92）。
	//
	// 返回 ErrTaskNotRunning 而不是自定义错误：manager 的四级恢复阶梯以
	// errors.Is(err, ErrTaskNotRunning) 为触发条件，会尝试冷恢复重建运行态
	// ——正是这种情况下该做的事。一个明确的错误哪怕语义不完美，也比无声
	// 无息好一个数量级。
	r.emitMu.Lock()
	closed := r.evClosed
	r.emitMu.Unlock()
	if closed {
		return fmt.Errorf("任务 %s 的事件通道已关闭，运行态已终结: %w", taskID, executor.ErrTaskNotRunning)
	}
	a.log.Info("grok 续接回合", "task", taskID, "session", r.sessionID)
	if err := r.frames.BeginTurn("send", text); err != nil {
		a.log.Warn("写 turn_start 帧失败，不影响回合", "task", taskID, "cause", err)
	}
	a.reportTiming(r, r.seg.BeginTurn(r.frames.Turn()))
	r.textPart = r.frames.NextPart()
	// 续接即发新的 session/prompt，回合边界由它的响应（stopReason）标记
	resCh, err := r.cli.CallAsync("session/prompt", map[string]any{
		"sessionId": r.sessionID,
		"prompt":    []any{map[string]any{"type": "text", "text": text}},
	})
	if err != nil {
		// CallAsync 只会因两件事失败：连接已关闭、写 stdio 失败。两者都等于
		// 「指令送不进 executor」——而 runs 表里那条运行态是陈的（进程死了没人摘，
		// lookup 照样返回它），所以这里必须补上哨兵，否则 manager 的四级恢复阶梯
		// （触发条件 errors.Is(err, ErrTaskNotRunning)，见 manager.go continue 的 doc）
		// 整个不启动，continue 直接 500——2026-08-09 grok 端到端验收撞的就是这个。
		return fmt.Errorf("任务 %s 的 ACP 连接不可用（%v）: %w", taskID, err, executor.ErrTaskNotRunning)
	}
	go a.awaitTurn(r, resCh)
	return nil
}

// Stop 终止执行并回收资源：置 stopping → 关 ACP → kill 执行者进程组 → 关事件通道。
func (a *Adapter) Stop(taskID string) error {
	r := a.lookup(taskID)
	if r == nil {
		return fmt.Errorf("任务 %s 无运行态: %w", taskID, executor.ErrTaskNotRunning)
	}
	a.log.Info("grok 停止任务", "task", taskID)
	// 先置 stopping 再关连接：让 onClosed 知道这是主动停止，不要产出假的失败结果
	r.emitMu.Lock()
	r.stopping = true
	r.emitMu.Unlock()
	if r.cli != nil {
		_ = r.cli.Close()
	}
	if r.proc != nil {
		if kerr := r.proc.Kill(); kerr != nil {
			// 不能丢弃：Kill 现在会在「已发 SIGKILL 但复核仍存活」时返回
			// prochost.ErrStillAlive，丢掉它等于让孤儿进程无声无息（B47）
			a.log.Error("grok 进程回收失败", "task", taskID, "cause", kerr)
			return fmt.Errorf("kill grok: %w", kerr)
		}
	}
	r.closeEvents()
	a.drop(taskID)
	a.log.Info("grok 任务已停止", "task", taskID)
	return nil
}

// lookup 取任务运行态；不存在返回 nil。
func (a *Adapter) lookup(taskID string) *runState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.runs[taskID]
}

// drop 注销任务运行态。
func (a *Adapter) drop(taskID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.runs, taskID)
}

// dropIf 仅当 runs 表里那条仍是 r 时才摘除。
//
// 为什么不能用 drop：冷恢复会把新运行态换进 runs 表，而旧连接的 OnClosed
// 回调可能在那之后才到（读循环退出有延迟）。无条件删就会把刚恢复好的运行态
// 删掉，任务凭空失去运行态——比不摘更坏。
func (a *Adapter) dropIf(taskID string, r *runState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if cur, ok := a.runs[taskID]; ok && cur == r {
		delete(a.runs, taskID)
	}
}

// emit 向事件通道投递一个事件；通道已关闭时静默丢弃并返回 false。
//
// 为什么要 emitMu + evClosed 而不是裸 send：事件可能来自读循环、看门狗、
// 回合终局三个 goroutine，而关闭权只有一处——没有这把锁会 send on closed channel。
func (a *Adapter) emit(r *runState, ev executor.AdapterEvent) bool {
	r.emitMu.Lock()
	defer r.emitMu.Unlock()
	if r.evClosed {
		a.log.Debug("事件通道已关闭，丢弃事件", "task", r.taskID, "type", ev.Type)
		return false
	}
	select {
	case r.evCh <- ev:
		return true
	default:
		a.log.Warn("事件通道满，丢弃事件", "task", r.taskID, "type", ev.Type)
		return false
	}
}

// reportTiming 把段切分器产出的条目逐条经 usage 事件上报。
//
// 为什么走 usage 而不是新事件类型：Usage（当前占用）、Spend（累计消耗）与
// Timing（耗时）是同一次模型调用结束时的三样产物；拆成两个事件，两者之间
// 就能插进一次 agentd 重启（契约文档 §6.3 的拍板记录）。
//
// entries 为空是常态（不是错误），静默返回。
func (a *Adapter) reportTiming(r *runState, entries []proto.TimingEntry) {
	for i := range entries {
		e := entries[i]
		if !a.emit(r, executor.AdapterEvent{Type: "usage", Timing: &e}) {
			a.log.Debug("grok 耗时条目未能上报（事件通道已终止或已满）",
				"task", r.taskID, "key", e.Key, "remaining", len(entries)-i)
			return
		}
	}
}

// emitTurnFailed 产出一个**回合级**失败终局，**不关闭事件通道**。
//
// 参数：reason 为给协调者看的失败原因原文
//
// 为什么不关通道：回合失败 ≠ 这个 executor 完了。serve 进程还活着，协调者
// 一个 continue 就能接着干——那正是 continue 的用途。以前这里一律 closeEvents，
// 于是 Send 在同一个 runstate 上开新回合，新回合的一切事件在 emit 里被 evClosed
// 短路静默丢弃，manager 的 mediate 循环也早已随通道关闭退出，任务停在 running
// 直到 2h 看门狗落 stalled（而 stalled 只唤醒不修复）。B92 根因报告的对照组：
// 3 个 grok 任务 failed 后全哑火，3 个 opencode 任务 failed 后全被 continue
// 救活——差异就在这一行，opencode/claudecode 都不因回合失败关通道。
//
// 一次性语义不受影响：跨回合的去重不需要（finishTurn 每回合只调一次，
// 两条分支互斥），与 fatal 路径之间的去重仍由 evClosed 承担。
func (a *Adapter) emitTurnFailed(r *runState, reason string) {
	a.log.Error("grok 回合失败", "task", r.taskID, "reason", reason)
	a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.sessionID,
		Result: &executor.Result{OK: false, SessionID: r.sessionID, FailReason: reason}})
}

// emitFatal 产出**执行级**失败终局并关闭事件通道。
//
// 参数：reason 为给协调者看的失败原因原文
//
// 用于连接已断或进程已死——这条运行态真的不可用了，必须关通道让 manager 的
// mediate 循环退出走对账。
//
// 一次性语义：断开处置与看门狗判死两条路径可能同时到达，closeEvents 的幂等
// 保证只有先到者生效，后到者的 emit 被 evClosed 丢弃，不会双重终结。
func (a *Adapter) emitFatal(r *runState, reason string) {
	a.log.Error("grok 执行终结", "task", r.taskID, "reason", reason)
	a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.sessionID,
		Result: &executor.Result{OK: false, SessionID: r.sessionID, FailReason: reason}})
	r.closeEvents()
}

// closeEvents 关闭事件通道（幂等）。
func (r *runState) closeEvents() {
	r.emitMu.Lock()
	defer r.emitMu.Unlock()
	if r.evClosed {
		return
	}
	r.evClosed = true
	close(r.evCh)
}

// noteAskedViaTool 标记本回合已通过原生提问工具向协调者递过问题。
func (r *runState) noteAskedViaTool() {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	r.askedViaTool = true
}

// takeAskedViaTool 取走并清空本回合的「已走工具提问」标记。
//
// 取走式（而非只读）是刻意的：标记的生命周期就是一个回合，收尾读一次即失效，
// 否则下一回合的兜底会被上一回合的提问误抑制，真出现静默结束就没人兜了。
func (r *runState) takeAskedViaTool() bool {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	asked := r.askedViaTool
	r.askedViaTool = false
	return asked
}

// turnTextAndReset 取走本回合正文并清空累积器，为下一回合做准备。
func (r *runState) turnTextAndReset() string {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	s := r.acc.turnText()
	r.acc.reset()
	return s
}

// flushRender 把累积的可见性增量落进 render.log，并按节流产出 progress。
//
// 失败只 Warn 不中断：可见性是增强能力，不值得为它挂掉回合。
func (a *Adapter) flushRender(r *runState) {
	r.turnMu.Lock()
	delta := r.acc.takeRender()
	due := time.Since(r.lastProgress) >= progressThrottle
	if due {
		r.lastProgress = time.Now()
	}
	r.turnMu.Unlock()

	if delta == "" {
		return
	}
	if err := turn.AppendRender(filepath.Join(r.taskDir, renderLogName), delta); err != nil {
		a.log.Warn("追加 render.log 失败，不影响回合", "task", r.taskID, "cause", err)
	}
	if due {
		a.emit(r, executor.AdapterEvent{Type: "progress", SessionID: r.sessionID,
			Text: turn.TruncateMarked(strings.TrimSpace(delta), 500)})
	}
}

// firstNonEmpty 返回第一个非空串（trailer 值优先于 git 实测值）。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// awaitTurn 等一个回合的终局并交 finishTurn 分类；同时把最后的可见性增量落盘。
func (a *Adapter) awaitTurn(r *runState, ch <-chan ACPResult) {
	res := <-ch
	a.flushRender(r)
	a.finishTurn(r, res)
}

// finishTurn 处理一个回合的终局：按 stopReason 与 trailer 分类产出事件。
//
// 为什么 stopReason != end_turn 一律判失败：那意味着回合没跑完（拒答、达到
// 上限、被取消），此时模型的产出不可信，交协调者比替它猜测安全。
func (a *Adapter) finishTurn(r *runState, res ACPResult) {
	// 回合收尾在最前面：本函数有三条出口（res.Err、stopReason 异常、正常路径），
	// 放在开头是唯一能同时覆盖三条的位置。EndTurn 幂等，重复触发无害。
	a.reportTiming(r, r.seg.EndTurn())
	if res.Err != nil {
		a.emitTurnFailed(r, fmt.Sprintf("回合异常终止: %v", res.Err))
		return
	}
	var out struct {
		StopReason string          `json:"stopReason"`
		Meta       json.RawMessage `json:"_meta"` // 整回合的 usage 与 costUsdTicks（B83）
	}
	_ = json.Unmarshal(res.Result, &out)
	// 累计消耗要**先于** stopReason 判定记账：回合没跑完（拒答、超限、被取消）
	// 这些 token 也已经烧掉了，漏记就成了系统性少算。
	// 注意这与 onUsageNotification 取的是**两套口径**、缓存算法相反（见 spend.go 文件头）。
	// 解析失败**有意不记日志**：awaitTurn 每回合都跑，失败只是这条没入账，不构成告警。
	if len(out.Meta) > 0 {
		if e, ok := parseTurnMetaSpend(out.Meta); ok {
			if e.CostState == proto.CostUnknown {
				a.log.Info("grok 本回合没有花费戳（pool/OAuth 路径或 cost_is_partial），"+
					"token 照常入账、花费记未知", "task", r.taskID, "prompt", e.Key)
			}
			a.emit(r, executor.AdapterEvent{Type: "usage", Spend: &e})
		}
	}
	if out.StopReason != "end_turn" {
		a.emitTurnFailed(r, "回合非正常收尾 stopReason="+out.StopReason)
		return
	}
	// 本回合有被拒权限时优先交代：模型被拒后可能悄悄绕路，人不知情
	if rej := r.takeRejected(); len(rej) > 0 {
		a.emit(r, executor.AdapterEvent{Type: "question",
			Text: turn.ClampQuestion(rejectedTurnQuestion(rej))})
		return
	}

	text := r.turnTextAndReset()
	askedViaTool := r.takeAskedViaTool()
	kind, tr := turn.ParseTrailer(text)
	branch, commit, hasNew, gerr := turn.GitTurnStatus(r.repoPath, r.startCommit)
	if gerr != nil {
		a.log.Warn("git 回合取证失败，降级只用 trailer", "task", r.taskID, "cause", gerr)
	}
	a.log.Info("grok 回合收尾", "task", r.taskID, "kind", kind,
		"has_new_commit", hasNew, "branch", branch, "asked_via_tool", askedViaTool,
		"final_text_len", len([]rune(text)))

	switch kind {
	case "ask":
		a.emit(r, executor.AdapterEvent{Type: "question", Text: turn.ClampQuestion(tr.Question)})
	case "finish":
		a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.sessionID,
			Result: &executor.Result{OK: true, Branch: firstNonEmpty(tr.Branch, branch),
				CommitHash: firstNonEmpty(tr.Commit, commit), SessionID: r.sessionID,
				Summary: tr.Summary, FinalText: turn.FinalText(text)}})
	default:
		// 兜底：模型没守收尾纪律。唯一可信的是 git 实况——但**有新提交不等于
		// 干完了**，只等于「这回合动过代码」。模型没宣布完成，handoff 就不替它
		// 宣布（B74）：发 result{OK:false}，git 实况留在结构化字段，
		// 协调者在 waiting_review 里看一眼再决定 done 还是 continue。
		if hasNew {
			a.log.Warn("回合无收尾协议但有新提交，转失败交协调者裁决",
				"task", r.taskID, "branch", branch, "commit", commit)
			a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.sessionID,
				Result: turn.NoTrailerResult(r.sessionID, branch, commit, text)})
			return
		}
		// 本回合已经通过原生提问工具给过协调者一个问题时，兜底闭嘴：兜底的职责是
		// 「别让回合静默结束」，那个诉求已经满足了。真机 47c36ab9 实测，此处补的
		// 第二张工单内容是「已调用一次提问工具；本回合结束。」——不是问题，回答它
		// 等于把废话灌回模型。
		if askedViaTool {
			a.log.Info("回合无收尾协议，但本回合已走工具提问，兜底不再补工单",
				"task", r.taskID)
			return
		}
		// 空文本守卫：文本为空时 question 产出的是一张空工单，协调者收到一个
		// 没有内容的问题。零文本是故障报告，不是问题（与 opencode mapIdle
		// 的空回合处置对称）
		if strings.TrimSpace(text) == "" {
			a.log.Warn("回合零文本且无新提交，转失败结果交协调者", "task", r.taskID)
			a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.sessionID,
				Result: &executor.Result{OK: false, SessionID: r.sessionID,
					FailReason: "回合结束但零文本产出（可能是供应商流中断）；executor 仍在线，可 continue 续接重试",
					// 与上一行的 FailReason 保持一致：executor 还活着（spec §3.3）
					VoidReason: executor.VoidReasonTurnDiscipline}})
			return
		}
		a.emit(r, executor.AdapterEvent{Type: "question", Text: turn.ClampQuestion(text)})
	}
}

// onClosed 是 ACP 连接终止的唯一处置入口。
//
// 先判主动停止：Stop 置位 stopping 后才关连接，读循环随之退出并回调本函数，
// 此时必须**不**产出失败结果——协调者看到的失败原因是假的（真实是用户主动停）。
//
// 为什么挂起表非空就直接终结、不再尝试重连：实测重连后 grok 不会重发未决的
// 权限请求，那次工具调用已永久卡死。重连成功反而更危险——adapter 会以为一切
// 正常，而任务再也不会前进。宁可立刻转 failed 让协调者 continue 重开一轮。
func (a *Adapter) onClosed(r *runState, cause error) {
	r.emitMu.Lock()
	stopping := r.stopping
	r.emitMu.Unlock()
	if stopping {
		// 主动停止：Stop 自己会 drop，这里不插手
		a.log.Info("ACP 连接已主动关闭，跳过失败处置", "task", r.taskID)
		return
	}
	// 连接断了这条运行态就永远不可用了（事件通道随 emitFatal 一起关掉），
	// 必须摘掉它——否则它以「陈运行态」的身份继续占着 runs 表：Send 会 lookup
	// 到它、拿一条死连接去发指令；Resume 的冷恢复互斥以「runs 表里有条目」为
	// 判据，会把这具僵尸当成「恢复进行中」而拒绝恢复。两条路都被挡死，
	// continue 直接 500（2026-08-09 grok 端到端实测）
	defer a.dropIf(r.taskID, r)
	if n := r.voidAllPending(); n > 0 {
		a.log.Error("ACP 连接断开且有未决权限，任务无法继续",
			"task", r.taskID, "voided", n, "cause", cause)
		a.emitFatal(r, fmt.Sprintf("权限应答通道中断（%d 个未决请求作废），需重新发起一轮", n))
		return
	}
	a.log.Warn("ACP 连接断开，无未决权限", "task", r.taskID, "cause", cause)
	var logTail string
	if r.proc != nil {
		logTail = r.proc.LogTail()
	}
	a.emitFatal(r, fmt.Sprintf("ACP 连接断开: %v；serve 日志尾部: %s", cause, logTail))
}

// updateKind 是 ACP sessionUpdate 的帧归类。
type updateKind int

const (
	updateNone      updateKind = iota // 不产帧
	updateText                        // 产 text 帧
	updateReasoning                   // 产 reasoning 帧
)

// updateFrameKind 把 ACP 的 sessionUpdate 类型归类成帧类型。
//
// 为什么 tool_call / tool_call_update 归 updateNone：它们不产**正文/思维链**帧。
// 工具帧走另一条路（updateToolFields → onSessionUpdate 的工具分支），
// 那条路拿的是 rawInput 原文而不是 toolLine 的 200 字摘要，所以当初
// 「拿人读摘要当帧 input 会丢掉命令尾部」的反对理由在那里不成立。
//
// 未知类型一律 updateNone。
func updateFrameKind(sessionUpdate string) updateKind {
	switch sessionUpdate {
	case "agent_message_chunk":
		return updateText
	case "agent_thought_chunk":
		return updateReasoning
	default:
		return updateNone
	}
}

// turnAccumulator 是单回合的文本累积器：把 session/update 分流成
// 「回合正文」与「render.log 可见性文本」两股。
//
// 为什么要分两股：ParseTrailer 取最后一个 { 开头的行，推理流里模型复述协议
// 样例会污染判定；但推理与工具动作对旁观者有价值，故只进 render.log。
type turnAccumulator struct {
	bodyBuf   strings.Builder // 回合正文（仅 agent_message_chunk）
	renderBuf strings.Builder // 可见性文本（正文 + 推理 + 工具动作）
}

func newTurnAccumulator() *turnAccumulator { return &turnAccumulator{} }

func (t *turnAccumulator) turnText() string { return t.bodyBuf.String() }

func (t *turnAccumulator) reset() {
	t.bodyBuf.Reset()
	t.renderBuf.Reset()
}

// takeRender 取走并清空待落盘的可见性增量。
func (t *turnAccumulator) takeRender() string {
	s := t.renderBuf.String()
	t.renderBuf.Reset()
	return s
}

// feedRaw 消费一条原始 ACP 消息，按 sessionUpdate 类型分流。
//
// 宽容语义：无法解析或未知类型一律跳过，绝不 panic——executor 侧输出不可信。
func (t *turnAccumulator) feedRaw(raw []byte) {
	var msg struct {
		Method string `json:"method"`
		Params struct {
			Update struct {
				Kind    string `json:"sessionUpdate"`
				Content struct {
					Text string `json:"text"`
				} `json:"content"`
				Title    string          `json:"title"`
				Status   string          `json:"status"`
				RawInput json.RawMessage `json:"rawInput"`
			} `json:"update"`
		} `json:"params"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}
	if msg.Method != "session/update" {
		return // _x.ai/* 等私有通知一概忽略
	}
	u := msg.Params.Update
	switch u.Kind {
	case "agent_message_chunk":
		t.bodyBuf.WriteString(u.Content.Text)
		t.renderBuf.WriteString(u.Content.Text)
	case "agent_thought_chunk":
		t.renderBuf.WriteString(u.Content.Text)
	case "tool_call":
		t.renderBuf.WriteString("\n▸ " + toolLine(u.Title, u.RawInput) + "\n")
	case "tool_call_update":
		if u.Status != "" {
			t.renderBuf.WriteString("  └ " + u.Status + "\n")
		}
	}
}

// updateFrameFields 从一条原始 session/update 消息里取 sessionUpdate 类型与
// content.text，供调用方在 feedRaw 之外把帧分流出去。
//
// 解析形状与 feedRaw 一致（同一份消息、同一套字段名）；解析失败或不是
// session/update 时返回空串（调用方据此跳过分流，绝不 panic）。
func updateFrameFields(raw []byte) (kind, text string) {
	var msg struct {
		Method string `json:"method"`
		Params struct {
			Update struct {
				Kind    string `json:"sessionUpdate"`
				Content struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"update"`
		} `json:"params"`
	}
	if json.Unmarshal(raw, &msg) != nil || msg.Method != "session/update" {
		return "", ""
	}
	return msg.Params.Update.Kind, msg.Params.Update.Content.Text
}

// toolUpdate 是一条 session/update 里的工具动作字段。
//
// 与 feedRaw / updateFrameFields 同款做法：累积器是纯累积器，工具分流在它的
// 调用方，故这里再解析一次同一份消息（三处解析同一套字段名，改字段要一起改）。
type toolUpdate struct {
	Kind     string          // "tool_call" | "tool_call_update"
	ID       string          // toolCallId，回合内的配对键
	Title    string          // tool_call 时是工具名；tool_call_update 时是人读句子
	Status   string          // 仅 tool_call_update 携带
	RawInput json.RawMessage // 完整入参（不截断）
	Output   string          // 工具输出；ACP 的 content 数组拼出来，可能为空
}

// updateToolFields 从一条原始 session/update 消息里取工具动作字段。
//
// 返回 ok=false 表示这条不是工具动作（含解析失败、非 session/update、
// 缺 toolCallId）——调用方据此跳过，绝不 panic。
//
// 注意 Output：ACP 的 tool_call_update 可以带 content 数组，但 grok 实际发不发、
// 发什么形状**尚未真机确认**（本仓 testdata/updates.jsonl 是手写夹具）。
// 解析不到就留空串，帧上的 output 为空——诚实的空好过编一个值。
func updateToolFields(raw []byte) (toolUpdate, bool) {
	var msg struct {
		Method string `json:"method"`
		Params struct {
			Update struct {
				Kind     string          `json:"sessionUpdate"`
				ID       string          `json:"toolCallId"`
				Title    string          `json:"title"`
				Status   string          `json:"status"`
				RawInput json.RawMessage `json:"rawInput"`
				Content  []struct {
					Content struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"content"`
			} `json:"update"`
		} `json:"params"`
	}
	if json.Unmarshal(raw, &msg) != nil || msg.Method != "session/update" {
		return toolUpdate{}, false
	}
	u := msg.Params.Update
	if u.Kind != "tool_call" && u.Kind != "tool_call_update" {
		return toolUpdate{}, false
	}
	if u.ID == "" {
		return toolUpdate{}, false // 没有配对键就没法配对，跳过
	}
	var sb strings.Builder
	for _, c := range u.Content {
		sb.WriteString(c.Content.Text)
	}
	return toolUpdate{Kind: u.Kind, ID: u.ID, Title: u.Title,
		Status: u.Status, RawInput: u.RawInput, Output: sb.String()}, true
}

// toolResultStatus 把 ACP 的工具状态映射成帧上的 status。
//
// 返回 ok=false 表示**不是终态**：不产 tool_result 帧、不收工具段。
// 不认识的状态一律按非终态处置——猜错方向的代价不对称：把非终态当终态会
// 提前收段并留下一个永远配不上的 start；把终态当非终态只是少一条条目，
// 由聚合层的 Partial 如实标出。grok 的真实状态取值集合见真机清单。
func toolResultStatus(status string) (string, bool) {
	switch status {
	case "completed":
		return "ok", true
	case "failed", "error":
		return "error", true
	default:
		return "", false
	}
}

// toolLine 把工具调用渲染成一行人类可读摘要：优先用 rawInput.command，
// 否则退回 title。
func toolLine(title string, rawInput json.RawMessage) string {
	if cmd := rawCommand(rawInput); cmd != "" {
		return turn.TruncateMarked(cmd, 200)
	}
	return title
}

// rawCommand 提取工具调用的完整命令（不截断）。
//
// 权限描述里必须用它而不是 toolLine：toolLine 的 200 截断是 render.log 行摘要的
// 设计（旁观者扫一眼即可），但权限描述是黑名单扫描/工单全文的真相源，命令尾部
// 可能正藏着危险片段（B6 根因），截断即静默放行。
func rawCommand(rawInput json.RawMessage) string {
	var in struct {
		Command string `json:"command"`
	}
	if len(rawInput) > 0 && json.Unmarshal(rawInput, &in) == nil {
		return in.Command
	}
	return ""
}

// permToolCall 是 ACP session/request_permission 里 toolCall 的可用子集。
//
// 字段取舍全部来自 Task 1 的真机取样（testdata/perm_*.json），逐条理由：
//   - Kind 是 toolCall.kind：**不能**用来分辨工具，真机实测 Write 与 Edit
//     都是 "edit"。留着只是为了给命令类（"execute")做兜底归一化。
//   - RawInput.Variant 才是可分辨的工具名（"Write" / "SearchReplace"）。
//   - Meta 是 rawInput 缺 variant 时的回落来源（_meta["x.ai/tool"].kind
//     给 "write" / "edit"）。
type permToolCall struct {
	ToolCallID string          `json:"toolCallId"`
	Kind       string          `json:"kind"`
	Title      string          `json:"title"`
	RawInput   json.RawMessage `json:"rawInput"`
	Meta       struct {
		XAI struct {
			Kind string `json:"kind"`
		} `json:"x.ai/tool"`
	} `json:"_meta"`
}

// permRequestFromToolCall 从 ACP toolCall 提取结构化权限载荷。
//
// 参数：
//   - tc: 一次 session/request_permission 的 toolCall 本体
//
// 返回：结构化载荷；关键字段缺失时返回 nil（不伪造空壳，manager 会
// fail-closed 升级人工）
//
// 注意：
//   - 命令取 rawInput.command 的完整原文，不取 toolCall.title——title 是
//     render.log 的行摘要、带 200 截断，命令尾部可能正藏着危险片段。
//   - 路径**原样透传**，不在这里展开相对路径。真机样本里 Edit 给过相对
//     路径 "probe.md"，展开成绝对路径是 permgate 的 InScope 的职责（它
//     知道任务工作目录，adapter 不知道）。
func permRequestFromToolCall(tc permToolCall) *executor.PermRequest {
	// 命令类：取 command 全文
	if cmd := rawCommand(tc.RawInput); cmd != "" {
		tool := executor.NormalizePermTool(tc.Kind)
		if tool == executor.PermToolOther {
			tool = executor.PermToolBash
		}
		return &executor.PermRequest{Tool: tool, Command: cmd}
	}
	// 文件类：先定工具名，再取路径
	if paths := rawPaths(tc.RawInput); len(paths) > 0 {
		tool := fileToolOf(tc)
		return &executor.PermRequest{Tool: tool, Paths: paths}
	}
	return nil
}

// fileToolOf 判定文件类工具究竟是 write 还是 edit。
//
// 为什么不能读 toolCall.kind：2026-08-09 真机实测，Write 与 Edit 的
// toolCall.kind 都是 "edit"（见 testdata/perm_write.json）。用它做判据会把
// 每一次整文件覆写误报成局部编辑。可分辨的来源只有两处，按可靠性排序：
//  1. rawInput.variant —— "Write" / "SearchReplace"
//  2. _meta["x.ai/tool"].kind —— "write" / "edit"
//
// 两处都缺时保守取 write：write 的破坏面更大，宁可按更严的那个判。
func fileToolOf(tc permToolCall) string {
	var in struct {
		Variant string `json:"variant"`
	}
	if len(tc.RawInput) > 0 && json.Unmarshal(tc.RawInput, &in) == nil {
		switch in.Variant {
		case "Write":
			return executor.PermToolWrite
		case "SearchReplace", "Edit":
			return executor.PermToolEdit
		}
	}
	if t := executor.NormalizePermTool(tc.Meta.XAI.Kind); t != executor.PermToolOther {
		return t
	}
	return executor.PermToolWrite
}

// rawPaths 从 rawInput 提取文件类工具的目标路径。
//
// 字段名 file_path 来自 Task 1 的真机探针（testdata/perm_write.json 与
// perm_edit_*.json 三份样本一致），**不是推断**。真机每次只带一个路径，
// 这里仍返回切片是为了对齐 executor.PermRequest.Paths 的形状。
func rawPaths(rawInput json.RawMessage) []string {
	var in struct {
		FilePath string `json:"file_path"`
	}
	if len(rawInput) == 0 || json.Unmarshal(rawInput, &in) != nil {
		return nil
	}
	if in.FilePath == "" {
		return nil
	}
	return []string{in.FilePath}
}

// acpHandler 把 ACP 回调接到 adapter 上。
//
// 纪律：回调运行在 ACP 读循环 goroutine 上，**不得阻塞**——所有耗时动作
// （落盘、git）要么很快，要么另起 goroutine，否则会卡住整条连接的消息消费。
type acpHandler struct {
	a *Adapter
	r *runState
}

// OnNotify 分流对方通知。
//
// 三类：session/update（正文与工具调用，原有链路）、_x.ai/session_notification
// （用量）、_x.ai/models/update（模型名与窗口）。其余私有通知继续忽略。
//
// 为什么这里要认 _x.ai/*：grok 把用量放在私有通知上，标准的 session/update
// 变体一个都不带计数。此前这个函数的第一行是 `if method != "session/update"
// { return }`，私有通知在那里就没了——它们压根到不了 feedRaw。
func (h *acpHandler) OnNotify(method string, params json.RawMessage) {
	switch method {
	case "session/update":
		h.onSessionUpdate(params)
	case "_x.ai/session_notification":
		h.onUsageNotification(params)
	case "_x.ai/models/update":
		h.onModelsUpdate(params)
	}
}

// onSessionUpdate 是原 OnNotify 的正文链路，一字未改，只是从早返回后的直线
// 变成 switch 的一个分支。
func (h *acpHandler) onSessionUpdate(params json.RawMessage) {
	h.r.turnMu.Lock()
	raw := append([]byte(`{"method":"session/update","params":`), append(params, '}')...)
	h.r.acc.feedRaw(raw)
	// W4a：turnAccumulator 是纯累积器，不该带 I/O——帧在它的调用方分流。
	// bodyBuf / renderBuf 的两股走向一字不改，这里只是多一路输出
	if kind, text := updateFrameFields(raw); text != "" {
		switch updateFrameKind(kind) {
		case updateText:
			if err := h.r.frames.Text(h.r.textPart, text); err != nil {
				h.a.log.Warn("写 text 帧失败，不影响回合", "task", h.r.taskID, "cause", err)
			}
		case updateReasoning:
			if err := h.r.frames.Reasoning(h.r.textPart, text); err != nil {
				h.a.log.Warn("写 reasoning 帧失败，不影响回合", "task", h.r.taskID, "cause", err)
			}
		}
	}
	if tu, ok := updateToolFields(raw); ok {
		h.a.mapToolUpdate(h.r, tu)
	}
	h.r.turnMu.Unlock()
	h.a.flushRender(h.r)
}

// mapToolUpdate 把一条工具动作落成帧 + 打点。调用方必须持有 r.turnMu。
//
// 两端各一次：tool_call 产 tool_call 帧并开工具段；终态的 tool_call_update
// 产 tool_result 帧并收工具段。中间态（in_progress 等）只更新不了什么，跳过。
//
// 打点必须在写帧**之前**：写帧要过一次头尾截断与文件 IO，把那段时间算进
// 工具耗时是在给工具记别人的账。
func (a *Adapter) mapToolUpdate(r *runState, tu toolUpdate) {
	if tu.Kind == "tool_call" {
		a.reportTiming(r, r.seg.ToolStart(tu.ID, tu.Title, rawCommand(tu.RawInput)))
		// 帧里存 rawInput 全文（只受头尾截断约束），不是 toolLine 的 200 字摘要
		if err := r.frames.ToolCall(tu.ID, tu.Title, string(tu.RawInput)); err != nil {
			a.log.Warn("写 tool_call 帧失败，不影响回合", "task", r.taskID, "cause", err)
		}
		return
	}
	status, terminal := toolResultStatus(tu.Status)
	if !terminal {
		return // 中间态：没有可落的结果，也不能收段
	}
	// 先取耗时再写帧：dur 来自 Segmenter 里记的 tool_call 时刻，
	// 没配上时是 -1（不知道），帧上就不带 dur_ms
	dur, entries := r.seg.ToolEnd(tu.ID)
	if err := r.frames.ToolResult(tu.ID, status, tu.Output, dur); err != nil {
		a.log.Warn("写 tool_result 帧失败，不影响回合", "task", r.taskID, "cause", err)
	}
	a.reportTiming(r, entries)
}

// onUsageNotification 处理 _x.ai/session_notification：只有 response_completed
// 会产出用量，其余（turn_completed 等）一律忽略——理由见 parseResponseCompleted。
func (h *acpHandler) onUsageNotification(params json.RawMessage) {
	u, ok := parseResponseCompleted(params)
	if !ok {
		return // 不是 response_completed，或没有有效数字：静默跳过，不是错误
	}
	h.r.turnMu.Lock()
	if h.r.ctxWindow > 0 {
		w := h.r.ctxWindow
		u.ContextWindow = &w
	}
	model := h.r.actualModel
	h.r.turnMu.Unlock()
	h.a.emit(h.r, executor.AdapterEvent{Type: "usage", ActualModel: model, Usage: u})
}

// onModelsUpdate 处理 _x.ai/models/update：记下模型名与窗口，供后续用量帧带上。
func (h *acpHandler) onModelsUpdate(params json.RawMessage) {
	model, window, ok := parseModelsUpdate(params)
	if !ok {
		h.a.log.Debug("grok 模型通知解析失败，跳过", "task", h.r.taskID)
		return
	}
	h.r.turnMu.Lock()
	changed := h.r.actualModel != model || h.r.ctxWindow != window
	h.r.actualModel, h.r.ctxWindow = model, window
	h.r.turnMu.Unlock()
	if changed {
		h.a.log.Info("grok 实际模型", "task", h.r.taskID, "model", model, "window", window)
	}
	// 模型名先单发一次：回合还没开始就能显示，不必等第一次模型调用完成
	h.a.emit(h.r, executor.AdapterEvent{Type: "usage", ActualModel: model})
}

func (h *acpHandler) OnPermission(reqID, params json.RawMessage) {
	var p struct {
		ToolCall permToolCall `json:"toolCall"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.ToolCall.ToolCallID == "" {
		h.a.log.Error("权限请求解析失败，按拒绝处理（fail-closed）",
			"task", h.r.taskID, "cause", err)
		_ = h.r.cli.Reply(reqID, map[string]any{
			"outcome": map[string]any{"outcome": "selected", "optionId": "reject-once"}})
		return
	}
	// 权限描述是唯一真相源：全文进工单与黑名单扫描（见 permTextHardLimit 的注释），
	// 展示截断由 manager 的 permEventText 负责。命令必须取完整原文（rawCommand 而非
	// toolLine）——toolLine 的 200 截断是 render.log 行摘要，命令尾部可能正藏着
	// 危险片段。
	text := p.ToolCall.Title
	if cmd := rawCommand(p.ToolCall.RawInput); cmd != "" {
		text += " | " + cmd
	}
	text = turn.TruncateMarked(text, permTextHardLimit)
	// 挂起登记同时存 desc：RespondPermission 拒绝时把它记入被拒清单，而不是让
	// 协调者看到一串 toolCallId（被拒清单的意义是「模型刚才想干什么、被挡了」）
	h.r.notePending(p.ToolCall.ToolCallID, reqID, text)
	req := permRequestFromToolCall(p.ToolCall)
	if req == nil {
		h.a.log.Warn("grok 权限请求提取不出结构化载荷，将由 manager 升级人工",
			"task", h.r.taskID, "perm", p.ToolCall.ToolCallID, "kind", p.ToolCall.Kind)
	} else {
		h.a.log.Info("grok 权限请求已结构化", "task", h.r.taskID,
			"perm", p.ToolCall.ToolCallID, "tool", req.Tool, "paths", len(req.Paths))
	}
	h.a.log.Info("grok 权限等待开始", "task", h.r.taskID,
		"perm", p.ToolCall.ToolCallID, "request_id", string(reqID))
	entries := h.r.seg.PauseWaiting(p.ToolCall.ToolCallID)
	if len(entries) == 0 {
		h.a.log.Warn("grok 权限请求未找到对应工具等待窗口",
			"task", h.r.taskID, "perm", p.ToolCall.ToolCallID)
	} else {
		h.a.reportTiming(h.r, entries)
	}
	h.a.emit(h.r, executor.AdapterEvent{Type: "permission",
		PermissionID: p.ToolCall.ToolCallID, SessionID: h.r.sessionID,
		Text: text, Perm: req})
}

// OnAskQuestion 处置 grok 的交互提问（spec §4.2.3）。
//
// 纪律：**先解阻塞再做别的**。不应答会让 session/prompt 永不返回、serve 进程健在、
// 看门狗探活通过——任务表面在跑实则永久静止，是最坏的一种故障形态（§5.3(c) 实测）。
// 因此即使参数解析失败也必须回包。
//
// 应答形态见 askQuestionReply：形状错了不会挂死，但会被 grok 判为工具执行失败报回
// 模型（2026-08-09 真机两轮实测），模型要么重问一遍、要么把「工具报错了」写进回合
// 文本——两种都会脏掉协调者看到的工单。
func (h *acpHandler) OnAskQuestion(reqID, params json.RawMessage) {
	// 先解阻塞：任何解析失败都不能挡住这一步
	if err := h.r.cli.Reply(reqID, askQuestionReply()); err != nil {
		h.a.log.Error("提问请求应答失败，该回合可能已挂死",
			"task", h.r.taskID, "cause", err)
	}

	text := askQuestionText(params)
	if text == "" {
		h.a.log.Warn("提问请求解析不出内容，已解阻塞但无法上报协调者",
			"task", h.r.taskID)
		return
	}
	h.a.log.Info("grok 走了交互提问工具（绕开回合协议），已转交协调者",
		"task", h.r.taskID)
	// 记在回合上：收尾兜底据此不再把回合叙述补成第二张工单
	h.r.noteAskedViaTool()
	if err := turn.AppendRender(filepath.Join(h.r.taskDir, renderLogName),
		"\n【模型提问】"+text+"\n"); err != nil {
		h.a.log.Warn("提问文本写 render.log 失败，不影响上报", "task", h.r.taskID, "cause", err)
	}
	h.a.emit(h.r, executor.AdapterEvent{Type: "question",
		SessionID: h.r.sessionID, Text: turn.ClampQuestion(text)})
}

// askQuestionReply 是 _x.ai/ask_user_question 的应答体。
//
// 形态不是猜的，是 grok 自己的 serve.log 给的（2026-08-09 真机，第二轮）：
//
//	tool_error: execution_failure tool_name="ask_user_question"
//	error_message=Client returned an invalid response to user question:
//	  invalid type: map, expected variant identifier at line 1 column 11
//
// 这是 serde 对内部标签枚举 AskUserQuestionExtResponse 的报错：它按名字取到了标签
// 字段 `outcome`，却发现值是 map 而不是变体名——所以 outcome 必须是**字符串**。
// 合法变体从 grok 二进制符号表读出，只有三个：accepted（需带 answers）、
// skip_interview、chat_about_this。
//
// 为什么选 skip_interview：它对应「用户跳过这轮问答」，grok 收到后给模型的提示是
// 「用户没有作答，按你自己的判断继续，或换个问题问」——正是 handoff 想要的语义。
// handoff 的提问真相源是回合 trailer 的 {"ask":…}，本通道只负责**立刻解阻塞**，
// 不承担作答（作答走协调者工单，答案在下一回合以 prompt 送进来）。选 accepted 就
// 得在这里同步憋出一份 answers，会让 ACP 请求阻塞到人回话为止——那正是 §4.2.3
// 要避免的挂死形态。
//
// 踩过的两个坑，都留在这当反面教材：回裸 `{}`（缺 outcome 字段），以及照抄
// session/request_permission 的内嵌形态 {"outcome":{"outcome":"cancelled"}}
// ——两者都被 grok 判为工具执行失败报回模型。两个协议长得像，但不是同一个枚举。
func askQuestionReply() map[string]any {
	return map[string]any{"outcome": "skip_interview"}
}

// askQuestionText 把 _x.ai/ask_user_question 的 params 渲染成人读的一段文本。
// 解析失败返回空串（调用方据此跳过上报，但阻塞已在调用方解除）。
func askQuestionText(params json.RawMessage) string {
	var p struct {
		Questions []struct {
			Question string `json:"question"`
			Options  []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(params, &p); err != nil || len(p.Questions) == 0 {
		return ""
	}
	var b strings.Builder
	for _, q := range p.Questions {
		b.WriteString(q.Question)
		b.WriteString("\n")
		for i, o := range q.Options {
			b.WriteString(fmt.Sprintf("  %d) %s", i+1, o.Label))
			if o.Description != "" {
				b.WriteString(" —— " + o.Description)
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (h *acpHandler) OnClosed(err error) { h.a.onClosed(h.r, err) }
