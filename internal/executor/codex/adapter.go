// adapter.go —— codex 的 executor.Adapter 实现：五动作与事件翻译。
//
// 职责：
//   - Start/Events/Send/RespondPermission/Stop 五动作
//   - 把 codex 的 ServerNotification / ServerRequest 翻译成 AdapterEvent 四类事件
//   - 回合边界判定与收尾分类（复用 internal/executor/turn 的 trailer 与 git 取证）
//   - 把事件流渲染进 render.log，供 handoff attach 的第二窗口实况显示
//
// 边界：
//   - 不写 store、不做审批判断、不做状态机迁移（executor 契约的硬边界）
//   - 不碰 codex 的配置文件：安全档位全部协议级下发且每回合重钉
package codex

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

// progressThrottle 与 opencode/grok 同值：防高频增量刷爆事件库。
const progressThrottle = 30 * time.Second

// 协议方法名。集中成常量，避免散落的字面量拼错后静默不生效。
const (
	methodInitialize    = "initialize"
	methodInitialized   = "initialized"
	methodThreadStart   = "thread/start"
	methodThreadResume  = "thread/resume"
	methodTurnStart     = "turn/start"
	methodTurnInterrupt = "turn/interrupt"

	ntfTurnCompleted     = "turn/completed"
	ntfItemStarted       = "item/started"
	ntfItemCompleted     = "item/completed"
	ntfThreadStatus      = "thread/status/changed"
	ntfRateLimits        = "account/rateLimits/updated"
	ntfServerReqResolved = "serverRequest/resolved"
	ntfTokenUsage        = "thread/tokenUsage/updated"

	reqCommandApproval     = "item/commandExecution/requestApproval"
	reqFileChangeApproval  = "item/fileChange/requestApproval"
	reqPermissionsApproval = "item/permissions/requestApproval"
	reqUserInput           = "item/tool/requestUserInput"
	reqAuthRefresh         = "account/chatgptAuthTokens/refresh"
)

// deltaNotifications 是只喂 render.log、不产 handoff 事件的高频通知。
//
// 为什么不产事件：这些通知每秒可达数十条，逐条产事件会把事件库刷爆，
// 且 wait 的游标语义（B22）会被无意义的增量淹没。
var deltaNotifications = map[string]bool{
	"item/agentMessage/delta":           true,
	"item/reasoning/textDelta":          true,
	"item/reasoning/summaryTextDelta":   true,
	"item/commandExecution/outputDelta": true,
}

// deltaKind 是增量通知的帧归类。
//
// 常量带 Kind 中缀是被迫的：本包已有一个 deltaText **函数**
// （adapter.go:727，从 params 里取增量文本），Go 不允许同名。
type deltaKind int

const (
	deltaKindNone      deltaKind = iota // 不产帧
	deltaKindText                       // 产 text 帧
	deltaKindReasoning                  // 产 reasoning 帧
)

// deltaFrameKind 把增量通知的方法名归类成帧类型。
//
// 为什么 commandExecution/outputDelta 归 deltaNone：它是命令的流式输出，
// 属于工具结果；完整结果由 commandExecution item 的 completed 通知以一条
// tool_result 帧上报，在这里再产一路会把同一段输出写两遍。
//
// 未知方法一律 deltaNone：codex 上游加了新的 delta 通知时，宁可少一种帧，
// 也不要把它猜成正文。
func deltaFrameKind(method string) deltaKind {
	switch method {
	case "item/agentMessage/delta":
		return deltaKindText
	case "item/reasoning/textDelta", "item/reasoning/summaryTextDelta":
		return deltaKindReasoning
	default:
		return deltaKindNone
	}
}

// sandboxPolicy 是每回合显式钉死的沙箱策略（spec §2 / §2.2）。
//
// 为什么每回合都传而不是只在 thread/start 传一次：thread/start 钉过的值会被
// thread/resume 或任何一次带覆盖的 turn/start 改掉，而恢复路径正是最容易漏钉的
// 地方（B18 的教训）。每回合重钉是一次固定成本的幂等操作，换来「任何一个回合
// 都不可能跑在开发机 config 的档位上」。
//
// networkAccess 为 true 是 2026-08-09 用户的明确决定（spec §2.2）：executor 跑在
// 专用开发机上，网络面本来就敞着；反方向的代价是实的——关掉后装依赖会失败，
// 且实证拒网**不产工单**，属于协调者不知情的哑失败。

// taskTmpDir 返回任务专属临时目录（<DataDir>/tmp/<id8>）。
//
// TaskDir 位于 <DataDir>/tasks/<id>（见 executor.StartReq.TaskDir 的契约注释），
// 因此向上两级取 DataDir，任务 ID（= filepath.Base(taskDir)）取前 8 位作短号，
// 不足 8 位时按原样用（不 panic、不补位）。
//
// 新目录仍在 DataDir 之内、仍在仓库工作区之外：把 TMPDIR 指进仓库会让
// 「非 git 目录应报错」用例的临时目录落入仓库，git 命令正常成功而假红。
//
// 为什么必须是这个短形状（61→27 的字节账，别只当成风格偏好）：
// Go 的 t.TempDir() 在 TMPDIR 下再套一层 <用例名><随机数>/001，claudecode 测试
// 还要在里面建 unix socket perm.sock，而 AF_UNIX sun_path 上限是 108 字节（含
// 结尾 NUL），internal/executor/claudecode/perm.go 的 sunPathMax = 107。旧形状
// <DataDir>/tasks/<36 位 UUID>/tmp 在默认 DataDir /root/.handoff（14 字节）下
// 长 61 字节：61 + 测试子路径预算 51（<用例名><随机数>/001 41 + /perm.sock 10）
// = 112 > 107，派出去的每一轮 go test ./... 必红。新形状 <DataDir>/tmp/<id8>
// 长 27 字节（14 + /tmp/ 5 + 8 位短号），27 + 51 = 78，留足余量。改回去=复现
// 必红的整轮测试，所以不要动。
func taskTmpDir(taskDir string) string {
	dataDir := filepath.Dir(filepath.Dir(taskDir))
	return filepath.Join(dataDir, "tmp", shortTaskID(filepath.Base(taskDir)))
}

// shortTaskID 取任务 ID 前 8 位（不足 8 位则原样返回）。
func shortTaskID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// tmpEnvKVs 把 Go 工具链的临时目录与构建缓存指向任务专属 tmp。
//
// writableRoots 是开门，环境变量是走门；两者缺一不可。GOCACHE 单独放子目录，
// 让清理临时文件时不会误删构建缓存，同时消除跨任务缓存污染。
func tmpEnvKVs(taskTmp string) []string {
	return []string{
		"TMPDIR=" + taskTmp,
		"GOTMPDIR=" + taskTmp,
		"GOCACHE=" + filepath.Join(taskTmp, "gocache"),
	}
}

// sandboxPolicy 返回每回合显式下发的沙箱策略。
//
// taskTmp 是任务隔离的工具链临时目录；gitCommonDir 是 linked worktree 所需的
// 共享 git 公共目录。两者按固定顺序进入 writableRoots，空值不占位；其余安全姿态
// 保持历史值。真正的 gitCommonDir 只在 newRunState 中取一次并缓存，避免每回合重跑 git。
func sandboxPolicy(taskTmp, gitCommonDir string) map[string]any {
	roots := []any{}
	for _, root := range []string{taskTmp, gitCommonDir} {
		if root != "" {
			roots = append(roots, root)
		}
	}
	return map[string]any{
		"type":                "workspaceWrite",
		"networkAccess":       true,
		"excludeSlashTmp":     true,
		"excludeTmpdirEnvVar": true,
		"writableRoots":       roots,
	}
}

// Adapter 是 codex 的 executor.Adapter 实现。
//
// 并发安全：runs 表由 mu 保护；每个任务的运行态只被该任务自己的回调路径访问。
type Adapter struct {
	log  *slog.Logger
	mu   sync.Mutex
	runs map[string]*runState
}

// New 创建 codex adapter。
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
	threadID    string // == sessionId，落 task.ExecutorSession
	startCommit string
	// gitCommonDir 是构造运行态时一次性取证的共享 git 公共目录。
	// 空串表示工作目录非 git 仓库或取证失败；sandboxPolicy 据此不追加该根。
	gitCommonDir string

	proc *Proc
	cli  *Client

	*permTable
	items *itemIndex

	frames   *turn.FrameWriter // 结构化回合帧；构造失败时为 nil，方法对 nil 安全
	seg      *turn.Segmenter   // 耗时段切分器；与 frames 同款 nil 安全约定
	textPart string            // 本回合正文/思维链的 part 标识

	// stopping 是主动停止标记：Stop 先置位再关连接，onClosed 与回合收尾据此
	// 知道这是用户主动停止而非执行失败，不产出假的失败结果
	stopping bool

	evCh     chan executor.AdapterEvent
	emitMu   sync.Mutex
	evClosed bool

	turnMu       sync.Mutex
	turnInFlight bool
	turnID       string // 仅供 turn/interrupt 使用，不参与回合边界判定
	bodyBuf      strings.Builder
	renderBuf    strings.Builder
	lastProgress time.Time
	askedViaTool bool

	// spendBase 是累计消耗的回合基线（B83）。与 usage 的当前占用是两个口径，
	// 共用同一条 thread/tokenUsage/updated 通知但取不同字段，别混。
	spendBase spendBase
	// pricingWarned 是「模型不在牌价表」的 Warn 已打标记：同模型只打一次，
	// 否则每回合刷一条。
	pricingWarned bool
}

// newRunState 建一条运行态；git 公共目录在此唯一构造点取证并缓存，Start 与 Resume 共用。
func (a *Adapter) newRunState(taskID, taskDir, repoPath string) *runState {
	gitCommonDir := ""
	if repoPath != "" {
		a.log.Debug("读取 git 公共目录", "task", taskID, "repo", repoPath)
		var err error
		gitCommonDir, err = turn.GitCommonDir(repoPath)
		if err != nil {
			a.log.Debug("git 公共目录不可用，跳过追加可写根", "task", taskID,
				"repo", repoPath, "cause", err)
			gitCommonDir = ""
		} else {
			a.log.Info("git 公共目录已准备为沙箱可写根", "task", taskID,
				"repo", repoPath, "common_dir", gitCommonDir)
		}
	}
	r := &runState{
		taskID: taskID, taskDir: taskDir, repoPath: repoPath, gitCommonDir: gitCommonDir,
		evCh:      make(chan executor.AdapterEvent, 64),
		permTable: newPermTable(),
		items:     newItemIndex(itemIndexCap),
		// lastProgress 必须从创建时刻起算：零值 time.Time 的 time.Since 是一个巨大值，
		// 会让**第一次** flushRender 恒判定「节流到期」而多产一条 progress 事件——
		// 权限工单旁边凭空多一张进度单（计划原稿漏了初始化）。
		lastProgress: time.Now(),
	}
	// 构造失败不挡任务：FrameWriter 的方法对 nil 接收者是空操作
	fw, err := turn.WriterFor(taskDir, a.log)
	if err != nil {
		a.log.Warn("创建帧写入器失败，本任务无结构化帧", "task", taskID, "cause", err)
	}
	r.frames = fw
	r.seg = turn.NewSegmenter(nil)
	return r
}

func renderStartPrompt(taskID, planContent, disciplineBlock string) (string, error) {
	return turn.RenderPrompt(taskID, planContent, disciplineBlock)
}

// Start 异步启动执行并立即返回。
//
// 步骤：StartServe → Dial → initialize + initialized → thread/start →
// emit progress{SessionID}（会话就绪信号）→ turn/start（不等待）。
//
// 注意：turn/start 是异步的（立即返回 inProgress），回合终态在 turn/completed
// 通知里，因此回合边界由通知驱动而非响应驱动。
func (a *Adapter) Start(ctx context.Context, req executor.StartReq) (err error) {
	taskID := req.Task.ID
	start := time.Now()
	a.log.Info("codex 启动任务", "task", taskID, "repo", req.Task.Workdir(),
		"task_dir", req.TaskDir, "model", req.Task.Model)
	defer func() {
		if err != nil {
			a.log.Error("codex 启动任务失败", "task", taskID, "cause", err)
		}
	}()

	taskTmp := taskTmpDir(req.TaskDir)
	if err := os.MkdirAll(filepath.Join(taskTmp, "gocache"), 0o755); err != nil {
		a.log.Error("创建任务专属 tmp 失败", "task", taskID, "dir", taskTmp, "cause", err)
		return fmt.Errorf("创建任务专属 tmp %s: %w", taskTmp, err)
	}
	a.log.Info("任务专属 tmp 就绪", "task", taskID, "dir", taskTmp)
	env := append(append([]string{}, req.Env...), tmpEnvKVs(taskTmp)...)

	proc, err := startServe(ctx, req.Task.Workdir(), taskID,
		prochost.ResolveMarkRoot(req.Task.Workdir(), req.Task.WorktreeManaged),
		req.TaskDir, env, a.log)
	if err != nil {
		return err
	}
	// 之后任一步失败都要回收进程，否则留下一个没人管的执行者进程
	defer func() {
		if err != nil {
			_ = proc.Kill()
		}
	}()

	r := a.newRunState(taskID, req.TaskDir, req.Task.Workdir())
	r.proc = proc
	// 回合起点 commit：兜底分类要靠「是否有新提交」这个事实裁决
	if _, c, _, gerr := turn.GitTurnStatus(req.Task.Workdir(), ""); gerr == nil {
		r.startCommit = c
	} else {
		a.log.Warn("读取回合起点 commit 失败，兜底裁决将退化", "task", taskID, "cause", gerr)
	}

	cli, err := Dial(ctx, proc.WSURL(), &handler{a: a, r: r}, a.log)
	if err != nil {
		return err
	}
	r.cli = cli
	defer func() {
		if err != nil {
			_ = cli.Close()
		}
	}()

	if err := a.openThread(ctx, r, req.Task.Workdir(), req.Task.Model,
		developerInstructionsFor(req.Discipline)); err != nil {
		return err
	}

	prompt, err := renderStartPrompt(taskID, req.PlanContent, req.Discipline)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.Discipline) == "" {
		a.log.Info("codex 未注入纪律块", "task", taskID)
	} else {
		a.log.Info("codex 纪律块已注入 prompt", "task", taskID, "bytes", len(req.Discipline))
	}

	a.mu.Lock()
	a.runs[taskID] = r
	a.mu.Unlock()

	// 「会话就绪」信号：审核主路径常以 question 收尾、result 永不出现，
	// progress 是会话 id 到达 manager 的可靠通道。**必须排在 turn/start 之前**：
	// 回合可能在几秒内就产出权限工单，manager 那时必须已经知道 threadId。
	a.emit(r, executor.AdapterEvent{Type: "progress", SessionID: r.threadID,
		Text: "codex 会话已就绪"})

	if err := r.frames.BeginTurn("dispatch", ""); err != nil {
		a.log.Warn("写 turn_start 帧失败，不影响回合", "task", req.Task.ID, "cause", err)
	}
	a.reportTiming(r, r.seg.BeginTurn(r.frames.Turn()))
	r.textPart = r.frames.NextPart()

	if err := a.startTurn(r, prompt); err != nil {
		return err
	}
	go a.watchdog(r)

	a.log.Info("codex 任务已启动", "task", taskID, "thread", r.threadID,
		"port", proc.Port, "elapsed_ms", time.Since(start).Milliseconds())
	return nil
}

// buildThreadStartParams 构造 thread/start 的入参。
//
// 抽成函数是为了让测试与生产代码用同一份字面量；下次增加安全参数时，
// 两处各写一份必然漏改一处。developerInstructions 是 codex 协议直收的持久
// 指令通道，协议铁律与执行纪律放在这里才能跨回合常驻。
func buildThreadStartParams(cwd, model, developerInstructions string) map[string]any {
	params := map[string]any{
		"cwd":               cwd,
		"sandbox":           "workspace-write",
		"approvalPolicy":    "on-request",
		"approvalsReviewer": "user",
	}
	if model != "" {
		params["model"] = model
	}
	if developerInstructions != "" {
		params["developerInstructions"] = developerInstructions
	}
	return params
}

// buildThreadResumeParams 构造 thread/resume 的入参。恢复路径也必须重传
// developerInstructions，否则恢复后常驻纪律会消失。
func buildThreadResumeParams(threadID, repoPath, developerInstructions string) map[string]any {
	params := map[string]any{
		"threadId":          threadID,
		"cwd":               repoPath,
		"approvalPolicy":    "on-request",
		"approvalsReviewer": "user",
	}
	if developerInstructions != "" {
		params["developerInstructions"] = developerInstructions
	}
	return params
}

// developerInstructionsFor 拼出常驻指令：协议铁律在前，执行纪律在后。
func developerInstructionsFor(disciplineBlock string) string {
	if strings.TrimSpace(disciplineBlock) == "" {
		return turn.ProtocolRules
	}
	return turn.ProtocolRules + "\n\n" + strings.TrimSpace(disciplineBlock)
}

// openThread 完成握手与会话建立：initialize → initialized → thread/start。
//
// 单独抽出：登录态失效那条路径要能在不起进程的情况下被测到。
func (a *Adapter) openThread(ctx context.Context, r *runState, cwd, model, developerInstructions string) error {
	a.log.Info("codex 会话建立中", "task", r.taskID, "cwd", cwd, "model", model,
		"dev_instr_bytes", len(developerInstructions))
	if _, err := r.cli.Call(ctx, methodInitialize, map[string]any{
		"clientInfo":   map[string]any{"name": "handoff", "version": "1"},
		"capabilities": map[string]any{"experimentalApi": true},
	}); err != nil {
		return fmt.Errorf("codex initialize: %w", err)
	}
	if err := r.cli.Notify(methodInitialized, nil); err != nil {
		return fmt.Errorf("codex initialized 通知: %w", err)
	}

	params := buildThreadStartParams(cwd, model, developerInstructions)
	res, err := r.cli.Call(ctx, methodThreadStart, params)
	if err != nil {
		// 凭据问题重试一万次也不会好，给可操作指引（spec §8）
		if strings.Contains(strings.ToLower(err.Error()), "unauthorized") {
			return fmt.Errorf("codex 登录态失效，请在 executor 机重新 `codex login`: %w", err)
		}
		return fmt.Errorf("codex thread/start: %w", err)
	}
	var out struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		// Model 是本 thread **实际**使用的模型（如 "gpt-5.6-sol"）。
		// 它就在我们已经在读的这一帧的顶层，此前被整块丢弃。
		Model string `json:"model"`
	}
	if err := json.Unmarshal(res, &out); err != nil || out.Thread.ID == "" {
		return fmt.Errorf("codex thread/start 未返回 threadId: %s", res)
	}
	r.threadID = out.Thread.ID
	a.log.Info("codex 会话已建立", "task", r.taskID, "thread", r.threadID)
	// 在会话就绪之后补发实际模型名：init 帧里没带，thread/start 的顶层才有
	if out.Model != "" {
		// 牌价估算要用实际模型名，而 emit 之后这个值就没别处留存了。
		r.spendBase.Model = out.Model
		a.log.Info("codex 实际模型", "task", r.taskID, "model", out.Model)
		a.emit(r, executor.AdapterEvent{Type: "usage", ActualModel: out.Model})
	}
	return nil
}

// startTurn 发起一个回合。
//
// 参数：
//   - text: 回合输入原文（首轮是渲染后的 plan 提示词，续接时是协调者原话）
//
// 注意：**四个安全参数每回合重钉一遍**（spec §5.1 步骤 6）——安全姿态因此与
// thread 的历史状态和恢复路径完全无关。
func (a *Adapter) startTurn(r *runState, text string) error {
	r.turnMu.Lock()
	r.turnInFlight = true
	r.turnID = ""
	r.turnMu.Unlock()

	a.log.Info("codex 发起回合", "task", r.taskID, "thread", r.threadID, "input_len", len(text))
	ch, err := r.cli.CallAsync(methodTurnStart, map[string]any{
		"threadId":          r.threadID,
		"cwd":               r.repoPath,
		"sandboxPolicy":     sandboxPolicy(taskTmpDir(r.taskDir), r.gitCommonDir),
		"approvalPolicy":    "on-request",
		"approvalsReviewer": "user",
		"input":             []any{map[string]any{"type": "text", "text": text}},
	})
	if err != nil {
		r.turnMu.Lock()
		r.turnInFlight = false
		r.turnMu.Unlock()
		a.log.Error("codex 发起回合失败", "task", r.taskID, "cause", err)
		// CallAsync 只会因「连接已关闭 / 写失败」失败，两者都等于指令送不进
		// executor；必须带哨兵，否则 manager 的四级恢复阶梯整个不启动（B18/grok 教训）
		return fmt.Errorf("任务 %s 的 codex 连接不可用（%v）: %w", r.taskID, err, executor.ErrTaskNotRunning)
	}
	// turn/start 的响应只带 turnId 与 inProgress；回合终态在 turn/completed 通知里。
	// 这里只等它记 turnId（供 turn/interrupt），绝不把它当回合边界。
	go a.noteTurnID(r, ch)
	return nil
}

// noteTurnID 等 turn/start 的响应并记下 turnId；响应本身携带错误时判回合失败。
func (a *Adapter) noteTurnID(r *runState, ch <-chan Result) {
	res := <-ch
	if res.Err != nil {
		a.log.Error("codex turn/start 返回错误", "task", r.taskID, "cause", res.Err)
		a.finishTurn(r, "failed", res.Err.Error(), r.takeTurnText())
		return
	}
	var out struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(res.Result, &out); err == nil && out.Turn.ID != "" {
		r.turnMu.Lock()
		r.turnID = out.Turn.ID
		r.turnMu.Unlock()
		a.log.Debug("codex 回合已受理", "task", r.taskID, "turn", out.Turn.ID)
	}
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
	// 结果：turn/start 发得出去、模型真的会跑，但产出的一切事件都会在 emit 里
	// 被 evClosed 短路丢弃，任务停在 running 直到 2h 看门狗（B92 在 grok 上实测）。
	//
	// 加在 Send 而不是 startTurn：startTurn 也被首轮启动路径调用，那时通道当然
	// 没关，加在那里是给热路径平白多一把锁。
	//
	// 返回 ErrTaskNotRunning 而不是自定义错误：manager 的四级恢复阶梯以
	// errors.Is(err, ErrTaskNotRunning) 为触发条件，会尝试冷恢复重建运行态。
	r.emitMu.Lock()
	closed := r.evClosed
	r.emitMu.Unlock()
	if closed {
		return fmt.Errorf("任务 %s 的事件通道已关闭，运行态已终结: %w", taskID, executor.ErrTaskNotRunning)
	}
	a.log.Info("codex 续接回合", "task", taskID, "thread", r.threadID)
	if err := r.frames.BeginTurn("send", text); err != nil {
		a.log.Warn("写 turn_start 帧失败，不影响回合", "task", taskID, "cause", err)
	}
	a.reportTiming(r, r.seg.BeginTurn(r.frames.Turn()))
	r.textPart = r.frames.NextPart()
	return a.startTurn(r, text)
}

// Stop 终止执行并回收资源：置 stopping → turn/interrupt → 关连接 → kill 执行者进程组 → 关事件通道。
func (a *Adapter) Stop(taskID string) error {
	r := a.lookup(taskID)
	if r == nil {
		return fmt.Errorf("任务 %s 无运行态: %w", taskID, executor.ErrTaskNotRunning)
	}
	a.log.Info("codex 停止任务", "task", taskID)
	// 先置 stopping 再动连接：让后续的 interrupted / onClosed 知道这是主动停止
	r.emitMu.Lock()
	r.stopping = true
	r.emitMu.Unlock()

	r.turnMu.Lock()
	turnID := r.turnID
	r.turnMu.Unlock()
	if r.cli != nil && turnID != "" {
		// 尽力而为：中断失败不阻断回收，反正连接和进程马上都要没了
		if err := r.cli.Notify(methodTurnInterrupt, map[string]any{
			"threadId": r.threadID, "turnId": turnID,
		}); err != nil {
			a.log.Warn("codex turn/interrupt 发送失败，继续回收", "task", taskID, "cause", err)
		}
	}
	if r.cli != nil {
		_ = r.cli.Close()
	}
	if r.proc != nil {
		if err := r.proc.Kill(); err != nil {
			// 回收失败要上抛而不是就地发事件（B47 修正 B20 的做法）：
			// stopExecutor 在调用本方法**之前**已经 noteStopping，事件通道随时会关，
			// 这里 a.emit 能不能落库是个竞态；而 stopExecutor 侧的
			// AppendEvent + Publish 是确定落库的。用可靠的那条替换不可靠的那条。
			// 提前返回也意味着不 drop 运行态——保留才有机会再回收（与 claudecode 同形）。
			a.log.Error("codex 进程回收失败", "task", taskID, "cause", err)
			return fmt.Errorf("kill codex: %w", err)
		}
	}
	r.closeEvents()
	a.drop(taskID)
	a.log.Info("codex 任务已停止", "task", taskID)
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
// 为什么不能用 drop：冷恢复会把新运行态换进 runs 表，而旧连接的 OnClosed 回调
// 可能在那之后才到（读循环退出有延迟）。无条件删会把刚恢复好的运行态删掉，
// 任务凭空失去运行态——比不摘更坏。
func (a *Adapter) dropIf(taskID string, r *runState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if cur, ok := a.runs[taskID]; ok && cur == r {
		delete(a.runs, taskID)
	}
}

// emit 向事件通道投递一个事件；通道已关闭或已满时丢弃并返回 false。
//
// 为什么要 emitMu + evClosed 而不是裸 send：事件可能来自读循环、看门狗、回合
// 终局三个 goroutine，而关闭权只有一处——没有这把锁会 send on closed channel。
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
			a.log.Debug("codex 耗时条目未能上报（事件通道已终止或已满）",
				"task", r.taskID, "key", e.Key, "remaining", len(entries)-i)
			return
		}
	}
}

// emitTurnFailed 产出一个**回合级**失败终局，**不关闭事件通道**。
//
// 参数：reason 为给协调者看的失败原因原文
//
// 为什么不关通道：回合失败 ≠ codex app-server 完了，进程还活着，协调者一个
// continue 就能接着干——那正是 continue 的用途。以前这里一律 closeEvents，
// 于是 Send→startTurn 在同一个 runstate 上开新回合，新回合的一切事件在 emit
// 里被 evClosed 短路静默丢弃，manager 的 mediate 循环也早已随通道关闭退出，
// 任务停在 running 直到 2h 看门狗落 stalled（而 stalled 只唤醒不修复）。
// 这是 grok 上实测到并已修复的 B92，codex 结构相同。
//
// 一次性语义不受影响：跨回合的去重不需要（finishTurn 每回合只调一次，
// 各 case 互斥），与 fatal 路径之间的去重仍由 evClosed 承担。
func (a *Adapter) emitTurnFailed(r *runState, reason string) {
	a.log.Error("codex 回合失败", "task", r.taskID, "reason", reason)
	a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.threadID,
		Result: &executor.Result{OK: false, SessionID: r.threadID, FailReason: reason}})
}

// emitFatal 产出**执行级**失败终局并关闭事件通道。
//
// 参数：reason 为给协调者看的失败原因原文
//
// 用于连接已断、进程已死、登录态失效——这条运行态真的不可用了，必须关通道让
// manager 的 mediate 循环退出走对账。登录态失效也归这里：判回合级的话 continue
// 会开一个立刻又失败的新回合，变成人肉重试循环（见 reqAuthRefresh 处那句
// 「登录态失效重试一万次也不会好」）。
//
// 一次性语义：断开处置与进程判死两条路径可能同时到达，closeEvents 的幂等保证
// 只有先到者生效，后到者的 emit 被 evClosed 丢弃，不会双重终结。
func (a *Adapter) emitFatal(r *runState, reason string) {
	a.log.Error("codex 执行终结", "task", r.taskID, "reason", reason)
	a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.threadID,
		Result: &executor.Result{OK: false, SessionID: r.threadID, FailReason: reason}})
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

// takeTurnInFlight 取走并清空「回合进行中」标志。
//
// 为什么用标志而不是 turnId 匹配：turn/start 是异步的，turn/completed **可能先于**
// turn/start 的响应到达——那一刻 r.turnID 还是空的，用 turnId 匹配会把回合终局
// 丢掉，任务从此永久静止。标志天然不受这个竞态影响。
func (r *runState) takeTurnInFlight() bool {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	in := r.turnInFlight
	r.turnInFlight = false
	return in
}

// appendBody 累积回合正文（只由 agentMessage 的 item/completed 调用）。
func (r *runState) appendBody(s string) {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	if r.bodyBuf.Len() > 0 {
		r.bodyBuf.WriteString("\n")
	}
	r.bodyBuf.WriteString(s)
}

// takeTurnText 取走本回合正文并清空，为下一回合做准备。
func (r *runState) takeTurnText() string {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	s := r.bodyBuf.String()
	r.bodyBuf.Reset()
	return s
}

// appendRenderDelta 累积 render.log 增量。
func (r *runState) appendRenderDelta(s string) {
	if s == "" {
		return
	}
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	r.renderBuf.WriteString(s)
	if !strings.HasSuffix(s, "\n") {
		r.renderBuf.WriteString("\n")
	}
}

// noteAskedViaTool 标记本回合已通过原生提问工具向协调者递过问题。
func (r *runState) noteAskedViaTool() {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	r.askedViaTool = true
}

// takeAskedViaTool 取走并清空「已走工具提问」标记。
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

// flushRender 把累积的可见性增量落进 render.log，并按节流产出 progress。
//
// 失败只 Warn 不中断：可见性是增强能力，不值得为它挂掉回合。
func (a *Adapter) flushRender(r *runState) {
	r.turnMu.Lock()
	delta := r.renderBuf.String()
	r.renderBuf.Reset()
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
		a.emit(r, executor.AdapterEvent{Type: "progress", SessionID: r.threadID,
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

// finishTurn 处理一个回合的终局：按 turn.status 与 trailer 分类产出事件。
//
// 参数：
//   - status: codex 的 turn.status（completed / interrupted / failed）
//   - errMsg: status=failed 时 codex 给的原因（B16：必须原样带出，不许扁平化）
//   - text: 本回合正文（已取走）
func (a *Adapter) finishTurn(r *runState, status, errMsg, text string) {
	// 回合收尾在最前面：本函数按 status 分支且 failed 分支提前 return，
	// 放开头是唯一能覆盖全部分支的位置。EndTurn 幂等。
	a.reportTiming(r, r.seg.EndTurn())
	a.flushRender(r)

	switch status {
	case "failed":
		a.emitTurnFailed(r, "回合失败: "+firstNonEmpty(errMsg, "codex 未给出原因"))
		return
	case "interrupted":
		r.emitMu.Lock()
		stopping := r.stopping
		r.emitMu.Unlock()
		if stopping {
			a.log.Info("回合被主动中断，跳过失败处置", "task", r.taskID)
			return
		}
		a.emitTurnFailed(r, "回合被中断（非 handoff 发起）: "+errMsg)
		return
	}

	// 本回合有被拒权限时优先交代：模型被拒后可能悄悄绕路，人不知情
	if rej := r.takeRejected(); len(rej) > 0 {
		a.emit(r, executor.AdapterEvent{Type: "question",
			Text: turn.ClampQuestion(rejectedTurnQuestion(rej))})
		return
	}

	askedViaTool := r.takeAskedViaTool()
	kind, tr := turn.ParseTrailer(text)
	branch, commit, hasNew, gerr := turn.GitTurnStatus(r.repoPath, r.startCommit)
	if gerr != nil {
		a.log.Warn("git 回合取证失败，降级只用 trailer", "task", r.taskID, "cause", gerr)
	}
	a.log.Info("codex 回合收尾", "task", r.taskID, "kind", kind,
		"has_new_commit", hasNew, "branch", branch, "asked_via_tool", askedViaTool,
		"final_text_len", len([]rune(text)))

	switch kind {
	case "ask":
		a.emit(r, executor.AdapterEvent{Type: "question", Text: turn.ClampQuestion(tr.Question)})
	case "finish":
		a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.threadID,
			Result: &executor.Result{OK: true, Branch: firstNonEmpty(tr.Branch, branch),
				CommitHash: firstNonEmpty(tr.Commit, commit), SessionID: r.threadID,
				Summary: tr.Summary, FinalText: turn.FinalText(text)}})
	default:
		// 兜底：模型没守收尾纪律。唯一可信的是 git 实况——但**有新提交不等于
		// 干完了**，只等于「这回合动过代码」。模型没宣布完成，handoff 就不替它
		// 宣布（B74）：发 result{OK:false}，git 实况留在结构化字段，
		// 协调者在 waiting_review 里看一眼再决定 done 还是 continue。
		if hasNew {
			a.log.Warn("回合无收尾协议但有新提交，转失败交协调者裁决",
				"task", r.taskID, "branch", branch, "commit", commit)
			a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.threadID,
				Result: turn.NoTrailerResult(r.threadID, branch, commit, text)})
			return
		}
		// 本回合已走原生提问工具时兜底闭嘴：兜底的职责是「别让回合静默结束」，
		// 那个诉求已经满足了；再补一张工单等于把废话灌回模型（grok 真机教训）
		if askedViaTool {
			a.log.Info("回合无收尾协议，但本回合已走工具提问，兜底不再补工单", "task", r.taskID)
			return
		}
		// 空文本守卫：零文本是故障报告，不是问题
		if strings.TrimSpace(text) == "" {
			a.log.Warn("回合零文本且无新提交，转失败结果交协调者", "task", r.taskID)
			a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.threadID,
				Result: &executor.Result{OK: false, SessionID: r.threadID,
					FailReason: "回合结束但零文本产出；executor 仍在线，可 continue 续接重试",
					// 与上一行的 FailReason 保持一致：executor 还活着（spec §3.3）
					VoidReason: executor.VoidReasonTurnDiscipline}})
			return
		}
		a.emit(r, executor.AdapterEvent{Type: "question", Text: turn.ClampQuestion(text)})
	}
}

// onClosed 是连接终止的唯一处置入口。
//
// 先判主动停止：Stop 置位 stopping 后才关连接，读循环随之退出并回调本函数，
// 此时必须**不**产出失败结果——协调者看到的失败原因是假的。
//
// 为什么挂起表非空就直接终结、不再尝试重连：按最保守路径实现（spec §8）——
// 假设未决权限在重连后不会重发。重连成功反而更危险：adapter 会以为一切正常，
// 而任务再也不会前进。
func (a *Adapter) onClosed(r *runState, cause error) {
	r.emitMu.Lock()
	stopping := r.stopping
	r.emitMu.Unlock()
	if stopping {
		a.log.Info("codex 连接已主动关闭，跳过失败处置", "task", r.taskID)
		return
	}
	// 连接断了这条运行态就永远不可用了，必须摘掉它——否则它以「陈运行态」的身份
	// 继续占着 runs 表：Send 会 lookup 到它、拿死连接去发指令；Resume 的冷恢复
	// 互斥以「runs 表里有条目」为判据，会把僵尸当成「恢复进行中」而拒绝恢复。
	defer a.dropIf(r.taskID, r)
	if n := r.voidAll(); n > 0 {
		a.log.Error("codex 连接断开且有未决权限，任务无法继续",
			"task", r.taskID, "voided", n, "cause", cause)
		a.emitFatal(r, fmt.Sprintf("权限应答通道中断（%d 个未决请求作废），需重新发起一轮", n))
		return
	}
	a.log.Warn("codex 连接断开，无未决权限", "task", r.taskID, "cause", cause)
	var logTail string
	if r.proc != nil {
		logTail = r.proc.LogTail()
	}
	a.emitFatal(r, fmt.Sprintf("codex 连接断开: %v；serve 日志尾部: %s", cause, logTail))
}

// handler 把传输层回调翻译成 handoff 语义。
//
// 注意：回调跑在读循环 goroutine 上，**不得阻塞**——所有耗时动作（git 取证、
// 落盘）都只在回合收尾这类低频路径上做。
type handler struct {
	a *Adapter
	r *runState
}

// OnNotify 分发服务端通知。
func (h *handler) OnNotify(method string, params json.RawMessage) {
	a, r := h.a, h.r
	switch {
	case deltaNotifications[method]:
		text := deltaText(params) // 既有函数：从 params 取增量文本
		r.appendRenderDelta(text) // 既有行为：一字不改（codex 的 render.log 本来就含思维链）
		switch deltaFrameKind(method) {
		case deltaKindText:
			if err := r.frames.Text(r.textPart, text); err != nil {
				a.log.Warn("写 text 帧失败，不影响回合", "task", r.taskID, "cause", err)
			}
		case deltaKindReasoning:
			if err := r.frames.Reasoning(r.textPart, text); err != nil {
				a.log.Warn("写 reasoning 帧失败，不影响回合", "task", r.taskID, "cause", err)
			}
		}
		return
	case method == ntfItemStarted || method == ntfItemCompleted:
		it, ok := parseItemNotification(params)
		if !ok {
			// 解析失败会导致后续 fileChange 权限门 fail-closed 升级人工，
			// 协调者需要能查到原因（items.go 的约定）
			a.log.Debug("codex item 通知解析失败，跳过", "method", method, "params_len", len(params))
			return
		}
		r.items.put(it)
		r.appendRenderDelta(it.renderLine())
		// 工具类 item 落一条 tool_call / tool_result 帧。part 取 item id：
		// started 与 completed 两条通知带同一个 id，帧因此天然配对
		a.appendItemFrame(r, method, it)
		// 回合正文只从 agentMessage 的 completed 取：它带的是**完整正文**，
		// 不必从 delta 拼，trailer 解析因此永远拿到完整文本
		if method == ntfItemCompleted && it.Type == "agentMessage" &&
			strings.TrimSpace(it.Text) != "" {
			r.appendBody(strings.TrimSpace(it.Text))
		}
		a.flushRender(r)
	case method == ntfTurnCompleted:
		if !r.takeTurnInFlight() {
			a.log.Debug("codex 收到无对应回合的 turn/completed，忽略", "task", r.taskID)
			return
		}
		// 回合边界：把本回合最后看到的 total 推进为下一个回合的基线。
		r.spendBase = r.spendBase.commit()
		status, errMsg := parseTurnCompleted(params)
		a.finishTurn(r, status, errMsg, r.takeTurnText())
	case method == ntfThreadStatus || method == ntfRateLimits:
		r.appendRenderDelta("【状态】" + method + " " + string(params))
		a.flushRender(r)
	case method == ntfServerReqResolved:
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		if err := json.Unmarshal(params, &p); err == nil {
			if itemID, ok := r.dropByReqID(string(p.RequestID)); ok {
				a.log.Info("codex 权限请求已被别处了结，摘掉挂起项",
					"task", r.taskID, "perm", itemID)
			}
		}
	case method == ntfTokenUsage:
		// 这条通知排在 turn/completed 之前到达，回合结束时数据已在手。
		// turn/completed 的报文里没有任何用量字段，别去那儿找。
		if u, ok := parseTokenUsage(params); ok {
			a.emit(r, executor.AdapterEvent{Type: "usage", Usage: u})
		} else {
			a.log.Debug("codex 用量通知解析失败，跳过", "task", r.taskID)
		}
		// 累计消耗：同一帧的 total 做回合级差分。取 total 不取 last——
		// 上面那行当前占用恰好相反。
		if e, next, ok := parseTurnSpend(params, r.spendBase); ok {
			if next.pending.Input < r.spendBase.Input {
				a.log.Warn("codex 用量计数器疑似归零，本回合按当前值全量入账",
					"task", r.taskID, "base_input", r.spendBase.Input,
					"now_input", next.pending.Input)
			}
			r.spendBase = next
			a.emit(r, executor.AdapterEvent{Type: "usage", Spend: &e})
			// 模型不在牌价表是用户看不到花费的唯一原因，日志里必须能查到；
			// 同一个模型只 Warn 一次，否则每回合刷一条。
			if e.CostState == proto.CostUnknown && !r.pricingWarned {
				r.pricingWarned = true
				a.log.Warn("codex 模型不在牌价表，本任务不显示花费",
					"task", r.taskID, "model", r.spendBase.Model)
			}
		}
	default:
		a.log.Debug("codex 未处理的通知", "method", method)
	}
}

// deltaText 从 delta 通知里取出可读文本（字段名随通知类型不同，逐个试）。
func deltaText(params json.RawMessage) string {
	var p struct {
		Delta string `json:"delta"`
		Text  string `json:"text"`
		Chunk string `json:"chunk"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return ""
	}
	return firstNonEmpty(p.Delta, p.Text, p.Chunk)
}

// parseTurnCompleted 取出回合终态与失败原因。
//
// 注意：解析失败时返回 "failed" 而不是 "completed"——把一个读不懂的终局当成
// 成功，会让 handoff 替模型宣布完成，是最不能接受的一种误判。
func parseTurnCompleted(params json.RawMessage) (status, errMsg string) {
	var p struct {
		Turn struct {
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Turn.Status == "" {
		return "failed", "无法解析 turn/completed 报文: " + string(params)
	}
	if p.Turn.Error != nil {
		errMsg = p.Turn.Error.Message
	}
	return p.Turn.Status, errMsg
}

// OnServerRequest 分发服务端请求。返回 false 时传输层代回 -32601。
func (h *handler) OnServerRequest(reqID json.RawMessage, method string, params json.RawMessage) bool {
	a, r := h.a, h.r
	switch method {
	case reqCommandApproval:
		ap, ok := parseCommandApproval(params)
		if !ok {
			a.log.Warn("codex 命令审批报文非法，回错误", "task", r.taskID)
			_ = r.cli.ReplyError(reqID, -32602, "invalid approval params")
			return true
		}
		desc := commandPermText(ap)
		perm := permRequestFromCommand(ap)
		r.note(ap.ItemID, reqID, desc)
		r.noteReqID(string(reqID), ap.ItemID)
		a.log.Info("codex 权限请求", "task", r.taskID, "perm", ap.ItemID, "tool", executor.PermToolBash)
		r.appendRenderDelta("【权限门】" + desc)
		a.flushRender(r)
		entries := r.seg.PauseWaiting(ap.ItemID)
		if len(entries) == 0 {
			a.log.Warn("codex 命令审批未找到对应工具等待窗口",
				"task", r.taskID, "perm", ap.ItemID, "method", method)
		} else {
			a.reportTiming(r, entries)
		}
		a.emit(r, executor.AdapterEvent{Type: "permission", PermissionID: ap.ItemID,
			SessionID: r.threadID, Text: desc, Perm: perm})
		return true

	case reqFileChangeApproval:
		var p struct {
			ItemID string `json:"itemId"`
		}
		if err := json.Unmarshal(params, &p); err != nil || p.ItemID == "" {
			a.log.Warn("codex 文件变更审批报文非法，回错误", "task", r.taskID)
			_ = r.cli.ReplyError(reqID, -32602, "invalid approval params")
			return true
		}
		it, found := r.items.get(p.ItemID)
		if !found {
			// 报文里没有路径，索引又查不到 → 不伪造结构，交 manager fail-closed
			a.log.Warn("codex fileChange 权限缺变更清单，已 fail-closed 升级人工",
				"task", r.taskID, "perm", p.ItemID)
			it = nil
		}
		desc := fileChangePermText(it)
		perm := permRequestFromFileChange(it)
		r.note(p.ItemID, reqID, desc)
		r.noteReqID(string(reqID), p.ItemID)
		a.log.Info("codex 权限请求", "task", r.taskID, "perm", p.ItemID, "indexed", found)
		r.appendRenderDelta("【权限门】" + desc)
		a.flushRender(r)
		entries := r.seg.PauseWaiting(p.ItemID)
		if len(entries) == 0 {
			a.log.Warn("codex 文件审批未找到对应工具等待窗口",
				"task", r.taskID, "perm", p.ItemID, "method", method)
		} else {
			a.reportTiming(r, entries)
		}
		a.emit(r, executor.AdapterEvent{Type: "permission", PermissionID: p.ItemID,
			SessionID: r.threadID, Text: desc, Perm: perm})
		return true

	case reqPermissionsApproval:
		// 一律 fail-closed（spec §5.4）：这是「模型申请把沙箱放宽一截」，等价于
		// acceptForSession。**绝不做成可批准的权限门**——能被批准的「放宽沙箱」
		// 正是 §2.1 安全论证赖以成立的那道边界。回一份空 profile，只让协调者知情。
		a.log.Warn("codex 申请放宽沙箱，已拒绝（fail-closed）", "task", r.taskID,
			"params", string(params))
		if err := r.cli.Reply(reqID, map[string]any{
			"profile": map[string]any{}, "scope": "turn",
		}); err != nil {
			a.log.Error("回发沙箱放宽拒绝失败", "task", r.taskID, "cause", err)
		}
		r.appendRenderDelta("【沙箱】模型申请放宽沙箱权限，已按最保守策略拒绝")
		a.flushRender(r)
		a.emit(r, executor.AdapterEvent{Type: "progress", SessionID: r.threadID,
			Text: "codex 申请放宽沙箱权限，已按最保守策略拒绝（不授予任何额外权限）"})
		return true

	case reqAuthRefresh:
		// 不实现（spec §4）：回错误让 codex 走它自己的刷新逻辑，同时把任务判失败
		// 并回显真因——登录态失效重试一万次也不会好。
		a.log.Error("codex 请求补令牌，登录态已失效", "task", r.taskID, "params", string(params))
		_ = r.cli.ReplyError(reqID, -32601, "handoff 不代管 codex 登录态")
		a.emitFatal(r, "codex 登录态失效，请在 executor 机重新 `codex login`")
		return true

	case reqUserInput:
		itemID, qs, ok := parseUserInput(params)
		if !ok {
			// 报文读不懂也必须应答——不应答等于让回合永久挂起
			a.log.Warn("codex 提问报文非法，回空应答", "task", r.taskID, "params_len", len(params))
			_ = r.cli.Reply(reqID, map[string]any{"answers": map[string]any{}})
			return true
		}
		text := userInputText(qs)
		a.log.Info("codex 提问已转交协调者", "task", r.taskID, "item", itemID,
			"question_count", len(qs))
		// 立即应答：回调在读循环上，等协调者会卡死整条连接
		if err := r.cli.Reply(reqID, userInputReply(qs)); err != nil {
			a.log.Error("回发提问应答失败", "task", r.taskID, "item", itemID, "cause", err)
		}
		// 置位后回合收尾的兜底会闭嘴，避免一次提问出两张工单
		r.noteAskedViaTool()
		r.appendRenderDelta(text)
		a.flushRender(r)
		a.emit(r, executor.AdapterEvent{Type: "question", SessionID: r.threadID,
			Text: turn.ClampQuestion(text)})
		return true
	}
	a.log.Debug("codex 未识别的服务端请求，交传输层回 -32601", "task", r.taskID, "method", method)
	return false
}

// OnClosed 连接终止。
func (h *handler) OnClosed(err error) { h.a.onClosed(h.r, err) }

// appendItemFrame 把 item 通知落成 tool_call / tool_result 帧。
//
// 归类：
//   - commandExecution / fileChange 的 started → tool_call
//   - 同类 item 的 completed → tool_result（status 由 ExitCode 判定）
//   - agentMessage / reasoning 不在此处产帧（它们走 delta 通知那一路）
//
// 为什么 part 取 it.ID：started 与 completed 是同一个 item 的两次通知，
// id 相同，前端据此把结果挂回调用卡片，不需要本地维护映射表。
func (a *Adapter) appendItemFrame(r *runState, method string, it *threadItem) {
	if it.Type != "commandExecution" && it.Type != "fileChange" {
		return
	}
	if method == ntfItemStarted {
		input := it.Command
		if it.Type == "fileChange" {
			input = it.renderLine() // 文件变更没有命令串，用路径清单当入参
		}
		a.reportTiming(r, r.seg.ToolStart(it.ID, it.Type, input))
		if err := r.frames.ToolCall(it.ID, it.Type, input); err != nil {
			a.log.Warn("写 tool_call 帧失败，不影响回合", "task", r.taskID, "cause", err)
		}
		return
	}
	status := "ok"
	if it.ExitCode != nil && *it.ExitCode != 0 {
		status = "error"
	}
	dur, entries := r.seg.ToolEnd(it.ID)
	if err := r.frames.ToolResult(it.ID, status, it.renderLine(), dur); err != nil {
		a.log.Warn("写 tool_result 帧失败，不影响回合", "task", r.taskID, "cause", err)
	}
	a.reportTiming(r, entries)
}
