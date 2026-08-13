// adapter.go —— opencode 语义到 executor.Adapter 契约的翻译层。
//
// 职责：
//   - 把 StartServe/CreateSession/PromptAsync/SubscribeEvents 编排成 Adapter 的
//     五动作：Start（环境物料 → serve → 会话 → 初始 prompt → 订阅映射）、
//     Send（同一会话续接）、RespondPermission（权限应答转发）、
//     Stop（kill serve + 关事件流）、Events（事件通道）
//   - SSE 事件 → AdapterEvent 映射：permission.asked → permission 事件；模型
//     文本（message.part.updated / message.part.delta）增量累积 →
//     render.log 追加 + 节流 progress；session.status idle → turn.ParseTrailer
//     分类（ask/finish/none 兜底 git 实况裁决）；serve 死亡 → failed result
//   - 可见性：回合文本增量追加到 <taskDir>/render.log，供 handoff attach
//     （render 流式 endpoint）旁观模型执行
//
// 边界：
//   - 不写 store、不做审批判断（见 executor.go 包级边界）：会话 id 等一切持久化
//     诉求经事件（progress「会话就绪」/ Result.SessionID）或返回值交给 manager 落库
//   - 不做任务状态机迁移：6 状态迁移完全由 manager 负责，本层只产事件、收指令
//   - 不重试、不决策：SSE 解析宽容（未知事件 Debug 跳过、绝不 panic）；
//     trailer 缺失时兜底只做「是否有新提交」的事实裁决，没有新提交就交协调者
//
// 事件映射以真实 SSE 样本为准（spike3/spike5，opencode 1.18.15 serve 模式）：
//   - 文本载体：模型文本走 message.part.updated（properties.part.type=text，
//     带该 part 全量文本快照）与 message.part.delta（properties.field=text，
//     properties.delta 增量），reasoning/tool 等非 text part 的增量隔离不进
//     回合；message.updated 只有 properties.info（role/messageID），不带文本，
//     仅用于探测新回合开始（role=user 首次见到该消息 id 时清空累积，同 id
//     重发忽略——session.diff 广播后服务端会重发同一 user 消息）
//   - 回合结束主信号：session.status 的 properties.status.type=idle；同现的
//     session.idle 与顶层 idle/busy 事件全部忽略，防重复触发分类
//   - 权限：permission.asked（properties.id 即 PermissionID，properties.permission/
//     patterns/metadata 拼描述）；permission.replied 是应答回显，必须忽略
//   - 其余类型（server.connected/heartbeat、session.updated/diff、catalog.updated、
//     integration.updated、reference.updated、plugin.added、step-start/step-finish、
//     tool/reasoning 等）宽容 Debug 跳过
package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/turn"
)

// 看门狗与节流参数：
//   - watchdogFastInterval/watchdogSlowInterval/watchdogFastProbes：serve 存活
//     探活节奏。活跃期（有新事件）200ms 高频；连续 fastProbes 次成功探活且无
//     新事件（任务进 waiting_review 挂过夜）后降频到 2s——200ms 高频探活每天
//     每任务约 43 万次 HTTP 请求（P1-17），降频后约 4.3 万次；
//     任一失败立即回到高频，保证死亡判定不被降频拖慢太多（慢档最坏 ~6s）
//   - watchdogFailThreshold：连续 3 次死亡（快档约 600ms 窗口）才判定死亡，
//     吸收探活抖动，不误杀正常任务
//   - progressThrottle：progress 事件节流，每任务至多每 30s 一条，防止高频
//     文本增量刷爆事件库（按回合粒度细化是 Task 12 e2e 的遗留项）
const (
	watchdogFastInterval  = 200 * time.Millisecond
	watchdogSlowInterval  = 2 * time.Second
	watchdogFastProbes    = 10
	watchdogFailThreshold = 3
	progressThrottle      = 30 * time.Second
	idleGraceDefault      = 1500 * time.Millisecond
)

// 保留态回收与文本累积的边界参数：
//   - reapIntervalDefault/reapMaxAttemptsDefault：Stop 时 kill 失败保留下来的
//     运行态的后台重试节奏与放弃上限（A-10）。保留是为了「还有机会回收孤儿
//     serve」，不是为了永久驻留——不重试就只剩内存与 lookup 阴影，不设上限则
//     runs 表只增不减。放弃时打 Error 交人工清理
//   - permTextHardLimit：权限描述的**防失控**硬上限（不是给协调者看的上限）。
//     全文经工单交给协调者，事件 payload 由 manager 侧另行截断——两者是不同的
//     关注点：工单要「看得全」，事件要「唤醒消息短」。64KB 只防失控输出。
//   - pendingDeltaLimit：类型未知的 part 增量暂存上限（见 mapPartDelta）。
//     超限即丢弃并 Warn，防止服务端只发 delta 不发 part.updated 时无界增长
const (
	reapIntervalDefault    = 30 * time.Second
	reapMaxAttemptsDefault = 20
	permTextHardLimit      = 64 << 10
	pendingDeltaLimit      = 64 << 10
)

// serveHandle 抽象 serve 进程的存活/销毁/诊断：真实实现是 *Proc（procHandle），
// 测试注入假探活，绕开真实进程依赖。
type serveHandle interface {
	Alive() bool
	Kill() error
	LogTail() string
}

// procHandle 把 *Proc 适配成 serveHandle。
//
// LogTail 读 serve.log 尾部而非 capture-pane：serve 死亡后它所在的窗格随命令
// 退出而关闭，capture-pane 读不到已关闭窗格（P1-8）。注意会话本身此时**仍在**
// ——第二窗口的 tail -f render.log 还吊着它，回收由 subscribeLoop 显式 Kill 完成。
type procHandle struct{ p *Proc }

func (h procHandle) Alive() bool { return h.p.Alive() }
func (h procHandle) Kill() error { return h.p.Kill() }

// LogTail 返回脱敏后的 serve.log 尾部。
//
// 为什么必须脱敏（A-12）：这段尾部会进 Result.FailReason（落事件库）和
// agentd.log，而它的内容完全由 opencode 决定——启动横幅、panic 时的环境转储、
// 带认证的 URL 都可能回显 OPENCODE_SERVER_PASSWORD。密码就在手边，抹掉的成本
// 为零，赌 opencode 每个版本都不打印才是不该冒的险。
func (h procHandle) LogTail() string {
	tail := serveLogTail(h.p.ServeLogPath)
	if h.p.Password == "" {
		return tail
	}
	return strings.ReplaceAll(tail, h.p.Password, "***")
}

// Adapter 是 opencode 的 executor.Adapter 实现（语义翻译层）。
//
// 并发安全：runs 表由 mu 保护；每个任务的运行态（回合累积、事件通道）只被
// 该任务自己的订阅 goroutine 访问，不做跨任务共享。
type Adapter struct {
	log  *slog.Logger
	mu   sync.Mutex
	runs map[string]*runState // taskID -> 运行态
	// idleGrace 是 idle 去抖宽限期（见 scheduleIdle）。测试注入毫秒级值，
	// 让回合分类的断言不必真等 1.5s。
	idleGrace time.Duration
	// reapInterval/reapMaxAttempts 是 kill 失败保留态的后台重试节奏与放弃上限
	// （见 reapRetained）。测试注入毫秒级值，避免真等 30s。
	reapInterval    time.Duration
	reapMaxAttempts int
}

// New 创建 opencode adapter。
//
// 参数：
//   - log: 本模块日志入口（nil 时退回 slog.Default()）
func New(log *slog.Logger) *Adapter {
	if log == nil {
		log = slog.Default()
	}
	return &Adapter{
		log:             log,
		runs:            make(map[string]*runState),
		idleGrace:       idleGraceDefault,
		reapInterval:    reapIntervalDefault,
		reapMaxAttempts: reapMaxAttemptsDefault,
	}
}

// runState 是单任务运行的完整状态。
//
// turnOrder/partSeen/partSnap/partTypes/pendingDelta/userMsgs/lastProgress/
// startCommit/idleGen 由 turnMu 保护：它们原本只被订阅 goroutine 读写，idle
// 去抖引入定时器 goroutine 后成为共享状态（见 scheduleIdle/resolveIdle）。
// evCh 的写入由 emitMu + evClosed 保护，使定时器 goroutine 也能安全 emit；
// 关闭权仍归 subscribeLoop（见 closeEvents）。lastEventAt 被事件映射写、
// 看门狗 goroutine 读，用 atomic 保证并发安全。
type runState struct {
	taskID      string
	taskDir     string
	repoPath    string
	session     string
	api         *API
	handle      serveHandle
	runCtx      context.Context
	runCancel   context.CancelFunc
	evCh        chan executor.AdapterEvent
	stopCh      chan struct{}
	stopOnce    sync.Once
	closeOnce   sync.Once
	renderPath  string
	emitMu      sync.Mutex // 保护 evCh 的写入与关闭（订阅 goroutine 与 idle 定时器 goroutine 共写）
	evClosed    bool       // evCh 已关闭，emit 必须静默丢弃（防 send on closed channel）
	turnMu      sync.Mutex // 保护以下回合累积状态（订阅 goroutine 与 idle 定时器 goroutine 共访）
	idleGen     uint64     // idle 去抖代次：任何回合推进都自增，使在途的候选 idle 失效
	idleTimer   *time.Timer
	startCommit string // 本回合起点 commit（兜底分类的基线，每回合结束后刷新）
	// 回合文本按 part 分段保存而非拼成一个字符串：服务端会修订同一个 part 的
	// 快照（"Hello world" → "Hi world"），只有按 part 存当前值、按 turnOrder
	// 顺序拼接，修订才能替换而不是叠加（A-6）。turnOrder 记录各 part 首次出现的
	// 先后，保证拼出来的文本顺序与模型输出一致。
	turnOrder []string          // 本回合已产出文本的 part key（首见顺序）
	partSeen  map[string]string // messageID+partID -> 该 part 当前的文本
	partSnap  map[string]bool   // messageID+partID -> 是否收到过非空全量快照
	// partTypes/userMsgs 是「这个 part/消息是什么」的会话级事实，不随回合边界
	// 失效（A-4）：第一回合登记的 reasoning part 若在回合结束时被遗忘，它后续的
	// 增量会被当成模型输出，思维链直接变成面向协调者的提问。
	partTypes map[string]string // messageID+partID -> part 类型（delta 无类型字段，靠它识别非 text 增量）
	userMsgs  map[string]bool   // messageID -> user 消息（其文本 part 不进回合）
	// pendingDelta 暂存「类型尚未揭示」的 part 增量（A-5）：part.updated 先于
	// delta 到达只是抓包里的观测顺序，SSE 跨重连没有顺序保证；未知即当文本会
	// 泄漏 reasoning，未知即丢弃会丢模型输出——暂存到类型揭晓再决定去留。
	pendingDelta map[string]string
	// permText/turnRejected 支撑「被拒权限终止回合」的识别（2026-08-08 实测 P0）：
	// opencode 收到 reject 会直接终结回合，最后一条消息只有 error 状态的 tool
	// part、零文本，idle 时回合文本为空。仅凭「空回合」无法区分它与「会话瞬时
	// 空闲」——前者必须唤醒协调者（否则任务挂死到看门狗），后者必须忽略（否则
	// 每次批准都塞一条无意义提问）。故显式记录本回合发生过的拒绝。
	permText     map[string]string // permID -> 权限描述（会话级：permID 全局唯一，不随回合清空）
	turnRejected []string          // 本回合已回传 reject 的权限描述，mapIdle 消费后清空
	// 提问通路状态（B49）。opencode 原生 question 工具会阻塞等人作答，而它的
	// 应答通道（question.asked → /question/{id}/reply）handoff 此前从没订阅，
	// 于是工具永远等不到应答、回合不结束、任务挂死到 stall 超时。
	//
	// pendingQuestionID / pendingQuestions: 当前挂起的请求及其问题结构。工具
	// 阻塞保证同一任务至多一个挂起请求，故用单值而非 map。Send 据此分流：
	// 有挂起请求就把协调者的答复打到 reply 端点，没有才发新 prompt。
	// seenQuestionIDs: 已上报过的 requestID。question 事件不像 permission 那样
	// 带幂等 id 给 manager 派生 ticket，SSE 重放的去重只能在本层做。
	pendingQuestionID string
	pendingQuestions  []QuestionInfo
	seenQuestionIDs   map[string]bool
	// askedViaTool 是回合级取走式标记（B49 §4.4）：本回合已通过 question 工具
	// 问过协调者。mapIdle 判出 trailer ask 时取走它并抑制那张工单——否则同一
	// 回合会给协调者两张单（grok 那次 askedViaTool 踩过的同一个坑）
	askedViaTool bool
	pendingBytes int       // pendingDelta 的总字节数（上限见 pendingDeltaLimit）
	lastProgress time.Time // 上次发 progress 的时刻（节流）
	// lastAssistantMsgID 是本回合最后一条 assistant 消息的 id（turnMu 保护）。
	// 它是对账水位的来源：mapIdle 正常分类完一个回合后，把它写进 proc.json，
	// 使断连恢复后的对账能判出「这个回合我已经消费过了」。不写就会重复补发。
	lastAssistantMsgID string
	// childSessions / permSession 支撑子会话审批归属（B52）。
	//
	// childSessions: 已认亲成功的子会话 id → 会话标题。认亲一次即缓存——一个
	// 回合里同一子会话会连发多条事件，每条都发 HTTP 会把网络 I/O 塞进事件热路径。
	// permSession: permID → 该权限所属会话 id。应答必须发回请求所在的会话
	// （子会话的权限发给父会话 opencode 不认），而 RespondPermission 的入参只有 permID。
	//
	// why 不复用 turnMu：acceptForeign 在 mapEvent 里刻意排在 turnMu.Lock() 之前，
	// 就是为了不在持锁时做网络 I/O。锁序固定 turnMu → sessMu，不得反向。
	sessMu        sync.RWMutex
	childSessions map[string]string
	permSession   map[string]string
	lastEventAt   atomic.Int64 // 最近一次 SSE 事件到达时刻（unixnano，mapEvent 打点）；看门狗据此判定任务活跃性
}

// newRun 创建并登记一个任务的运行态。
func (a *Adapter) newRun(taskID, taskDir, repoPath string) *runState {
	r := &runState{
		taskID:          taskID,
		taskDir:         taskDir,
		repoPath:        repoPath,
		evCh:            make(chan executor.AdapterEvent, 16),
		stopCh:          make(chan struct{}),
		renderPath:      filepath.Join(taskDir, renderLogFileName),
		partSeen:        make(map[string]string),
		partSnap:        make(map[string]bool),
		partTypes:       make(map[string]string),
		pendingDelta:    make(map[string]string),
		userMsgs:        make(map[string]bool),
		permText:        make(map[string]string),
		childSessions:   map[string]string{},
		permSession:     map[string]string{},
		seenQuestionIDs: map[string]bool{},
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

// Start 按「WriteTaskEnv → StartServe → 建会话 → 初始 prompt → 订阅映射」
// 流程启动任务执行并立即返回。
//
// 参数：
//   - ctx: 控制启动阶段的超时/取消（CreateSession/PromptAsync 受其约束）；
//     不代表执行生命周期（执行延续到 Stop）
//   - req: 任务快照、计划原文与任务工作目录
//
// 返回：
//   - 任一启动阶段失败（环境物料生成/serve 启动/建会话/发 prompt）返回错误，
//     调用方（manager）应把任务标记 failed
//
// 注意：
//   - serve 已拉起但后续阶段失败时自动 Kill 清理进程残留，避免半启动进程占端口
func (a *Adapter) Start(ctx context.Context, req executor.StartReq) (err error) {
	a.log.Info("adapter 开始启动任务", "task", req.Task.ID,
		"task_dir", req.TaskDir, "workdir", req.Task.Workdir())
	defer func() {
		if err != nil {
			a.log.Error("adapter 启动任务失败", "task", req.Task.ID, "cause", err)
		}
	}()

	configPath, _, err := WriteTaskEnv(req.TaskDir, req.Task.ID, req.Task.Model, req.PlanContent)
	if err != nil {
		return err
	}
	// serve 的工作目录（cwd）取 task.Workdir()：worktree 任务的 executor 必须在
	// worktree 里跑（分支 HEAD 在那里），主仓库 HEAD 停在派发前位置；原地模式
	// Workdir() 回退 RepoPath，行为与一期一致
	proc, err := startServe(ctx, req.Task.Workdir(), req.Task.ID, req.TaskDir, configPath, req.Env, a.log)
	if err != nil {
		return err
	}
	a.log.Info("opencode serve 已启动", "task", req.Task.ID,
		"port", proc.Port, "shim_pid", proc.Handle.PID)
	// serve 连接凭据落盘（proc.json）：agentd 重启后 RecoverOnStartup 凭它探活
	// 与重建订阅；写失败不阻断启动（缺失时重启恢复按「执行器已不在」处理）
	if err := writeProcInfo(req.TaskDir, &procInfo{
		Handle: proc.Handle, Port: proc.Port, Password: proc.Password,
	}); err != nil {
		a.log.Warn("写 serve 连接凭据失败，重启恢复将不可用", "task", req.Task.ID, "cause", err)
	}
	api := NewAPI(fmt.Sprintf("http://127.0.0.1:%d", proc.Port), proc.Password)
	if _, err := a.startRun(ctx, req, api, procHandle{p: proc}); err != nil {
		if kerr := proc.Kill(); kerr != nil {
			a.log.Warn("清理 serve 残留失败", "task", req.Task.ID, "cause", kerr)
		}
		return err
	}
	// 会话已由 startRun 新建成功：此刻起水位可信（空水位 = 尚无回合结束，B38）。
	// armed=true 只在这里置——它语义是「会话是本版本 agentd 亲手新建的」，不是
	// 「serve 进程起来了」（冷恢复也会拉 serve，但那是老会话，armed 必须保持原值）
	if werr := writeProcInfo(req.TaskDir, &procInfo{
		Handle: proc.Handle, Port: proc.Port, Password: proc.Password,
		WatermarkArmed: true,
	}); werr != nil {
		a.log.Warn("新建会话后写 armed 标记失败，对账可能误判水位可信度",
			"task", req.Task.ID, "cause", werr)
	}
	return nil
}

// startRun 完成「建会话 → 发初始 prompt → 记录起点 commit → 启动订阅与看门狗」。
//
// 这是 Start 的内部骨架，单独成函数以便测试注入 httptest server 与假探活
// （免真实 opencode 二进制）。
//
// 返回：
//   - sessionID: 新建的 opencode 会话 id（经 Result.SessionID 随结果事件上报，
//     供 manager 落 task.ExecutorSession）
//   - err: 建会话/发 prompt 失败；失败时运行态已注销
func (a *Adapter) startRun(ctx context.Context, req executor.StartReq, api *API, handle serveHandle) (sessionID string, err error) {
	r := a.newRun(req.Task.ID, req.TaskDir, req.Task.Workdir())
	r.api = api
	r.handle = handle
	a.log.Info("adapter 启动运行", "task", r.taskID, "task_dir", r.taskDir, "workdir", r.repoPath)
	defer func() {
		if err != nil {
			a.log.Error("adapter 启动运行失败", "task", r.taskID, "cause", err)
			r.runCancel()
			a.drop(r.taskID)
		} else {
			a.log.Info("adapter 运行已启动", "task", r.taskID, "session", sessionID)
		}
	}()

	sessionID, err = api.CreateSession(ctx)
	if err != nil {
		return "", err
	}
	r.session = sessionID
	a.log.Info("opencode 会话已建", "task", r.taskID, "session", sessionID)

	// 「会话就绪」信号：建会话成功立即带 SessionID 发一条 progress——manager 据此
	// 落 task.ExecutorSession。为什么不用 result 做唯一通道：审核主路径常以
	// question 收尾、result 永不出现；Task 12 重启恢复要拿会话 id 重建 SSE，
	// 必须让它在首个事件就到 manager。progress 只入库不阻塞，零风险。
	a.emit(r, executor.AdapterEvent{Type: "progress", SessionID: sessionID, Text: "会话就绪"})

	// 初始 prompt 取 WriteTaskEnv 生成的 prompt.md（回合制纪律模板渲染产物）
	promptPath := filepath.Join(r.taskDir, promptFileName)
	promptContent, err := os.ReadFile(promptPath)
	if err != nil {
		return "", fmt.Errorf("读取任务 prompt %s: %w", promptPath, err)
	}
	if err := api.PromptAsync(ctx, sessionID, string(promptContent)); err != nil {
		return "", err
	}
	a.log.Info("opencode 初始 prompt 已发", "task", r.taskID,
		"session", sessionID, "prompt_len", len(promptContent))

	// 记录任务起点 commit：兜底分类用 git 实况裁决「是否有新提交」的基线；
	// 非 git 仓库或查询失败时留空，兜底一律按无新提交处理（转提问，不卡死）
	r.captureStartCommit(a)

	go r.subscribeLoop(a)
	go a.watchdog(r)
	return sessionID, nil
}

// captureStartCommit 记录任务起点 commit：git 兜底分类（fallbackClassify）用
// 「是否有新提交」裁决的基线；非 git 仓库或查询失败时留空，兜底一律按无新提交处理。
//
// 由 startRun 与 Resume 复用：两者都需在订阅启动前定下本回合的 git 基线。
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
// 注意（P1-11）：任务不在运行（未启动/已 Stop/运行态已随终结注销）时返回
// **已关闭的通道**而非 nil——契约是「通道关闭 = 执行终结」，消费方（manager
// 中介循环）靠 range 在关闭时退出；nil 通道会让 for-range 永久阻塞。Dispatch →
// go mediate 的调度窗口内 serve 若死亡，运行态已注销而中介循环尚未开始：返回
// 已关闭通道让中介循环立即退出、不泄漏 goroutine；该窗口内已产出的 failed
// 结果随运行态注销而丢失，是「尚未开始消费」的必然缝隙，任务状态由看门狗与
// 重启恢复兜底。
func (a *Adapter) Events(taskID string) <-chan executor.AdapterEvent {
	if r := a.lookup(taskID); r != nil {
		return r.evCh
	}
	ch := make(chan executor.AdapterEvent)
	close(ch)
	return ch
}

// Send 向同一会话续发指令（原生续接：上下文完整保留）。
//
// 参数：
//   - text: 协调者的回答/修改指令，原样透传，不得加工
//
// 注意：
//   - stopCh 已关（Stop 已介入，运行态可能因 kill 失败被保留）时拒绝发送：
//     订阅已退出，prompt 发出也没有事件回程，任务会静默挂死——宁可让协调者
//     看到「任务不在运行」的明确错误
//   - 有挂起的 question 请求时不发 prompt，改把答复回填给该请求（B49）：
//     question 工具阻塞时回合并未结束，发 prompt 会开出第二个回合
func (a *Adapter) Send(ctx context.Context, taskID, text string) (err error) {
	r := a.lookup(taskID)
	if r == nil {
		return fmt.Errorf("任务 %s: %w", taskID, executor.ErrTaskNotRunning)
	}
	select {
	case <-r.stopCh:
		return fmt.Errorf("任务 %s 已停止（运行态保留待回收），不能续接", taskID)
	default:
	}
	a.log.Info("adapter 收到续接指令", "task", taskID, "text", turn.TruncateRunes(text, 80))
	defer func() {
		if err != nil {
			a.log.Error("adapter 续接指令发送失败", "task", taskID, "cause", err)
		} else {
			a.log.Info("adapter 续接指令已发送", "task", taskID, "session", r.session)
		}
	}()
	// 提问分流（B49）：有挂起的 question 请求时，协调者的答复必须打到 reply
	// 端点而不是发新 prompt——question 工具阻塞时回合还在跑，再发 prompt 会
	// 开出第二个回合，而阻塞的工具依然等不到应答
	if reqID, qs := r.takePendingQuestionSnapshot(); reqID != "" {
		return a.replyPendingQuestion(ctx, r, reqID, qs, text)
	}
	return r.api.PromptAsync(ctx, r.session, text)
}

// takePendingQuestionSnapshot 读一份挂起提问的快照（不清除）。
//
// 返回：请求 id 与问题结构；无挂起请求时 id 为空串
//
// 为什么只读不清：答复可能折算失败或被服务端拒绝，那两条路都要重发工单并
// **保留**挂起请求——提前清掉，协调者的下一次答复就无处可投，任务重新死锁。
// 清除只发生在应答成功之后（见 replyPendingQuestion）。
func (r *runState) takePendingQuestionSnapshot() (string, []QuestionInfo) {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	return r.pendingQuestionID, r.pendingQuestions
}

// clearPendingQuestion 清除挂起提问（应答成功后调用）。
func (r *runState) clearPendingQuestion() {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	r.pendingQuestionID = ""
	r.pendingQuestions = nil
}

// replyPendingQuestion 把协调者的答复折算并回填给 opencode 的 question 工具。
//
// 参数：
//   - reqID/qs: 挂起请求的 id 与问题结构
//   - text: 协调者答复原文
//
// 返回：
//   - nil: 应答成功，或答复折算不了/被服务端拒绝而已重发工单（两者对协调者
//     都不是错误，是「再答一次」）
//   - 非 nil: 网络/服务端故障等真错误，冒泡给协调者终端
//
// 注意：
//   - 折算失败**不触达服务端**：猜一个最接近的选项会让模型按错误前提继续干活
func (a *Adapter) replyPendingQuestion(ctx context.Context, r *runState,
	reqID string, qs []QuestionInfo, text string) error {

	answers, perr := parseQuestionAnswers(qs, text)
	if perr != nil {
		a.log.Warn("答复无法折算成选项，重发工单请协调者再答",
			"task", r.taskID, "request", reqID, "cause", perr)
		// 重发也填同一个 reqID：由 manager 统一判定「这是重放还是重发」，
		// adapter 侧留空等于把判断拆到两个地方，manager 那份判据永不触发
		a.emit(r, executor.AdapterEvent{Type: "question", QuestionID: reqID,
			Text: turn.ClampQuestion("上一次答复没能对上选项（" + perr.Error() + "）。\n\n" +
				renderQuestionTicket(qs))})
		return nil
	}
	a.log.Info("答复已折算，回填 executor 提问", "task", r.taskID,
		"request", reqID, "answers", answers)
	if err := r.api.ReplyQuestion(ctx, reqID, answers); err != nil {
		if errors.Is(err, ErrCustomAnswerRejected) {
			a.log.Warn("opencode 不接受该自定义答案，重发工单请协调者改填选项",
				"task", r.taskID, "request", reqID, "cause", err)
			a.emit(r, executor.AdapterEvent{Type: "question", QuestionID: reqID,
				Text: turn.ClampQuestion("opencode 不接受自定义答案，请改填编号或选项原文。\n\n" +
					renderQuestionTicket(qs))})
			return nil
		}
		return err
	}
	r.clearPendingQuestion()
	a.log.Info("提问已应答，回合继续", "task", r.taskID, "request", reqID)
	return nil
}

// RespondPermission 把协调者的权限裁决转发给 opencode server。
//
// 参数：
//   - permID: 与 permission 事件中的 PermissionID 一致（manager 的 ticket id
//     经 taskID:permID 命名空间化，此处为裸 permID，由 manager 还原后传入）
//   - decision: "once"（批准本次）或 "reject"（拒绝）
//
// 注意：
//   - stopCh 已关时拒绝转发：与 Send 同因（见 Send 注释），保留态不接新裁决
func (a *Adapter) RespondPermission(ctx context.Context, taskID, permID, decision string) (err error) {
	r := a.lookup(taskID)
	if r == nil {
		return fmt.Errorf("任务 %s: %w", taskID, executor.ErrTaskNotRunning)
	}
	select {
	case <-r.stopCh:
		return fmt.Errorf("任务 %s 已停止（运行态保留待回收），不能应答权限", taskID)
	default:
	}
	a.log.Info("adapter 收到权限应答", "task", taskID, "perm", permID, "decision", decision)
	defer func() {
		if err != nil {
			a.log.Error("adapter 权限应答转发失败", "task", taskID, "perm", permID, "cause", err)
		} else {
			a.log.Info("adapter 权限应答已转发", "task", taskID, "perm", permID)
		}
	}()
	// 应答必须发回权限请求所在的会话：子会话的权限发给父会话 opencode 不认，
	// 协调者的批准落不了地，任务照样挂死（B52）
	sess := r.session
	r.sessMu.RLock()
	mapped, known := r.permSession[permID]
	r.sessMu.RUnlock()
	if known {
		sess = mapped
	} else {
		// 进程内表，agentd 重启后为空；此时只能退回父会话。若该权限来自子
		// agent，这次应答会被 opencode 拒（4xx），错误会经 httpError 一路回到
		// 协调者终端——是响的，不是静默的
		a.log.Warn("权限应答未在会话映射表里找到该 permID，退回父会话应答",
			"task", taskID, "perm", permID, "session", sess)
	}
	a.log.Info("权限应答选定会话", "task", taskID, "perm", permID,
		"session", sess, "from_map", known)
	if err := r.api.RespondPermission(ctx, sess, permID, decision); err != nil {
		return err
	}
	// 拒绝登记必须在转发成功之后：没送达的拒绝不会终止 executor 的回合，
	// 提前登记会让下一次空回合 idle 谎报「因权限被拒终止」。
	// 不在 turnMu 下调 api（网络 I/O），故登记单独短暂加锁。
	if decision == "reject" {
		r.noteRejected(permID)
	}
	return nil
}

// noteRejected 登记一次已送达的权限拒绝，供 mapIdle 识别「被拒终止的空回合」。
//
// 参数：
//   - permID: 被拒的权限 id（描述从 permText 反查，查不到时退化为 id 本身）
func (r *runState) noteRejected(permID string) {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	desc := r.permText[permID]
	if strings.TrimSpace(desc) == "" {
		desc = "权限 " + permID
	}
	r.turnRejected = append(r.turnRejected, desc)
}

// takeTurnRejected 取出并清空本回合的拒绝记录（调用方须已持 turnMu）。
func (r *runState) takeTurnRejected() []string {
	rejected := r.turnRejected
	r.turnRejected = nil
	return rejected
}

// takeAskedViaTool 取走「本回合已通过 question 工具提问」的标记（读后即清）。
//
// 返回：本回合是否已通过工具问过协调者
//
// 为什么必须取走式：标记的生命周期是一个回合。常驻会让下一回合的真 trailer
// 提问被误抑制，任务停在 running 无人知晓——那正是 B49 要消灭的形态。
//
// 注意：调用方须已持 turnMu（mapIdle 的既有契约）
func (r *runState) takeAskedViaTool() bool {
	asked := r.askedViaTool
	r.askedViaTool = false
	return asked
}

// rejectedTurnQuestion 组装「回合因权限被拒而终止」交给协调者的提问文本。
//
// why（必须是 question 而不是 result/failed）：任务没失败也没完成，它只是停在
// 半路等人指路。question 让任务进 waiting_answer，协调者可直接续发指令（换个
// 方式做/跳过这步/收尾提交），会话上下文完整保留——这与 fallbackClassify
// 「流程不卡死」的取向一致。
func rejectedTurnQuestion(rejected []string) string {
	return "上一步操作因权限被拒而终止了本回合（executor 未产出任何文本）：\n  - " +
		strings.Join(rejected, "\n  - ") +
		"\n\n请给出下一步指令：换用其他方式完成该步骤 / 跳过该步骤继续 / 直接收尾提交。"
}

// Stop 终止任务执行：取消订阅 → kill serve（执行者进程组）→ 事件通道关闭 → 注销运行态。
//
// 注意：
//   - 幂等：重复 Stop 不 panic；事件通道只关闭一次（由订阅 goroutine 持有关闭权）
//   - kill 失败但 serve 仍存活时**保留运行态**（P1-9）：serve 占着端口与模型
//     会话，drop 掉就没有任何途径回收；保留期间运行态是惰性的（订阅与看门狗
//     都已退出、事件通道已关），Send/RespondPermission 经 stopCh 守卫拒绝继续执行
//   - 保留态由 reapRetained 后台重试回收（A-10），重试有上限、放弃时打 Error
//     交人工（handoff stop 回收）：**agentd 重启不会接走它**——RecoverOnStartup
//     只探测 running/waiting_answer 任务（watchdog.go），而 Stop 只由 Done 在
//     归档时调用（manager.go），进程内保留态只可能属于已归档任务
//   - 运行态注销（drop）与 subscribeLoop 退出时的 drop 是幂等的 map 删除，
//     mu 保护下不会重复释放——runs 表因此不随任务累积无界增长
func (a *Adapter) Stop(taskID string) (err error) {
	r := a.lookup(taskID)
	if r == nil {
		return fmt.Errorf("任务 %s: %w", taskID, executor.ErrTaskNotRunning)
	}
	a.log.Info("adapter 停止任务", "task", taskID)
	defer func() {
		if err != nil {
			a.log.Error("adapter 停止任务失败", "task", taskID, "cause", err)
		} else {
			a.log.Info("adapter 任务已停止", "task", taskID)
		}
	}()
	// 先关 stopCh（让 emit 让路）再取消运行 ctx（打断 SSE 订阅），最后 kill 进程
	r.stopOnce.Do(func() {
		close(r.stopCh)
		r.runCancel()
	})
	// 挂起的提问先解阻塞再杀进程（B49）：opencode 的 question 工具在等应答，
	// 直接杀掉会留下一条永远挂起的请求。失败只 Warn 不阻断——进程马上就没了，
	// 为一次解阻塞失败挡住 Stop 是本末倒置
	if reqID, _ := r.takePendingQuestionSnapshot(); reqID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), unaryTimeout)
		if rerr := r.api.RejectQuestion(ctx, reqID); rerr != nil {
			a.log.Warn("停止前拒绝挂起提问失败，该请求可能仍挂在 opencode 侧",
				"task", taskID, "request", reqID, "cause", rerr)
		} else {
			a.log.Info("停止前已拒绝挂起提问", "task", taskID, "request", reqID)
		}
		cancel()
		r.clearPendingQuestion()
	}
	if r.handle != nil {
		if kerr := r.handle.Kill(); kerr != nil {
			if r.handle.Alive() {
				// kill 失败但 serve 仍存活：保留运行态（P1-9）。为什么保留——
				// 订阅 goroutine 已随 stopCh 退出（其 defer 见 stopCh 已关、
				// 不争 drop），若此处也 drop，孤儿 serve 无人能再回收；
				// 保留后由 reapRetained 后台重试完成清理
				a.log.Error("kill serve 失败，保留运行态并转入后台重试", "task", taskID, "cause", kerr)
				go a.reapRetained(r)
				return fmt.Errorf("kill serve: %w", kerr)
			}
			// kill 失败但 serve 已自灭（进程死）：无孤儿资源
			// 可留，保留反而是无法回收的僵尸条目，照常注销
			a.log.Warn("kill serve 失败但 serve 已死，照常注销运行态", "task", taskID, "cause", kerr)
		}
	}
	// 清理完成后注销运行态：此后 lookup/Events 返回 nil（runs 表不残留已停任务）
	a.drop(taskID)
	return nil
}

// reapRetained 回收 Stop 时因 kill 失败而保留的运行态（A-10）。
//
// 周期重试 Kill：成功（或 serve 已自灭）即注销运行态；连续失败达
// reapMaxAttempts 后放弃并注销，打 Error 交人工清理执行者进程。
//
// why（保留必须配重试与上限）：保留态是惰性的——没有 goroutine、事件通道已关，
// 它唯一的价值就是「还留着 handle，还有机会回收孤儿 serve」。不重试，这个价值
// 从不兑现（Stop 只由归档调用一次，重启也不接管），条目只是 runs 表里的内存与
// lookup 阴影；不设上限，runs 表就只增不减。
func (a *Adapter) reapRetained(r *runState) {
	interval := a.reapInterval
	if interval <= 0 {
		interval = reapIntervalDefault
	}
	maxAttempts := a.reapMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = reapMaxAttemptsDefault
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		<-ticker.C
		kerr := r.handle.Kill()
		if kerr == nil || !r.handle.Alive() {
			a.log.Info("保留态回收成功", "task", r.taskID, "attempt", attempt, "cause", kerr)
			a.drop(r.taskID)
			return
		}
		a.log.Warn("保留态回收重试失败", "task", r.taskID,
			"attempt", attempt, "max_attempts", maxAttempts, "cause", kerr)
	}
	a.log.Error("保留态回收重试耗尽，注销条目并交人工清理（请 handoff stop 回收）",
		"task", r.taskID, "attempts", maxAttempts)
	a.drop(r.taskID)
}

// subscribeLoop 是单任务的事件流主循环（唯一持有 evCh 关闭权的 goroutine）：
// 订阅 SSE → mapEvent 逐条映射产出 AdapterEvent；订阅退出后按退出原因
// （serve 死亡 / 流不可恢复）产出 failed 结果，随后关闭事件通道。
func (r *runState) subscribeLoop(a *Adapter) {
	defer func() {
		// 停掉在途的 idle 去抖定时器：订阅已结束，再触发一次回合分类只会产出
		// 归属不明的事件（通道也即将关闭）
		r.cancelPendingIdle()
		r.closeOnce.Do(r.closeEvents)
		// 注销运行态（P1-9）：仅当 Stop 未介入（stopCh 未关）时由本 defer 注销——
		// 此时退出只可能是 serve 死亡/流异常，进程已死或已在上文 Kill 回收，无
		// 孤儿可留。Stop 已介入时（stopCh 已关），drop 与否由 Stop 裁决：kill
		// 成功才 drop、kill 失败保留运行态供重试回收；本 defer 不争抢，否则会
		// 把「kill 失败保留」的承诺毁掉。Stop 的 stopOnce 先关 stopCh 再取消
		// runCtx，订阅只在 runCancel 后退出，此处的时序判断无竞态
		select {
		case <-r.stopCh:
		default:
			a.drop(r.taskID)
		}
		a.log.Info("opencode 事件流订阅退出", "task", r.taskID)
	}()
	err := r.api.SubscribeEvents(r.runCtx, func(raw json.RawMessage) {
		a.mapEvent(r, raw)
	}, func() {
		// P1-10b：/event 无重放语义，断连间隙服务端产出的事件永久丢失。
		// B38 起，回合终态那半边由对账补回；权限请求那半边补不回来——消息流的
		// tool part 只有 callID 没有权限 id，应答端点要求真实 id、伪造即 404，
		// 故仍保留本告警，它是协调者知道「可能需要 attach 人工兜底」的唯一信号
		a.log.Warn("SSE 断连已恢复：断连间隙的权限请求可能丢失（/event 无重放语义），"+
			"若任务卡在等待决策请 handoff attach 查看或 handoff resume --force 收口",
			"task", r.taskID, "session", r.session)
		go a.reconcileAfterRecovery(context.Background(), r.taskID, "reconnect")
	})
	select {
	case <-r.stopCh:
		return // 正常 Stop 关停：静默退出，不产事件
	default:
	}
	if !r.handle.Alive() {
		// 看门狗判定 serve 死亡后已取消 runCtx：订阅随连接断开而退出，
		// 此处产出 failed 结果，让协调者看到死亡现场（serve.log 尾部——
		// serve 所在窗格已随命令退出关闭，capture-pane 读不到，P1-8）
		tail := turn.TailRunes(r.handle.LogTail(), 200)
		a.log.Error("opencode serve 已退出", "task", r.taskID, "stderr_tail", tail)
		a.emit(r, executor.AdapterEvent{Type: "result", Result: &executor.Result{
			OK: false, SessionID: r.session,
			FailReason: "opencode serve 已退出: " + tail,
		}})
		// 回收残留会话：serve 死后第二窗口（tail -f render.log）会一直吊着
		// 执行者进程不回收就成孤儿（shim 的锁与子进程占资源）；
		// Kill 幂等，后续 Stop 再 kill 也安全。证据不丢——serve.log/render.log
		// 在磁盘上，协调者照常读文件
		if kerr := r.handle.Kill(); kerr != nil {
			a.log.Warn("serve 死亡后回收执行者进程失败", "task", r.taskID, "cause", kerr)
		}
		return
	}
	a.log.Warn("opencode 事件流意外中断，按失败结束回合", "task", r.taskID, "cause", err)
	a.emit(r, executor.AdapterEvent{Type: "result", Result: &executor.Result{
		OK: false, SessionID: r.session,
		FailReason: "opencode 事件流意外中断: " + turn.TailRunes(fmt.Sprint(err), 200),
	}})
}

// watchdog 是 serve 存活看门狗：周期探活，连续 3 次失败判定 serve 死亡，
// 取消运行 ctx 让订阅退出——failed 结果由 subscribeLoop 统一产出（保持
// 「事件通道只有一个写入者」的关闭权约定）。探活间隔自适应用默认配置
// （见 watchdogConfig）。
func (a *Adapter) watchdog(r *runState) {
	a.watchdogWithConfig(r, watchdogConfig{
		fastInterval: watchdogFastInterval,
		slowInterval: watchdogSlowInterval,
		fastProbes:   watchdogFastProbes,
	})
}

// watchdogConfig 是探活节奏的可注入配置（测试注入毫秒级快慢间隔，
// 让「降频/复位」的时间敏感断言不依赖真实 200ms/2s 节奏）。
type watchdogConfig struct {
	fastInterval time.Duration // 活跃期探活间隔
	slowInterval time.Duration // 任务稳定（无事件且探活连续成功）后的降频间隔
	fastProbes   int           // 连续 fastProbes 次成功探活且无新事件后降频
}

// watchdogWithConfig 是 watchdog 的配置注入骨架（探活节奏自适应逻辑）：
//
//   - 收到新事件（emit 打点 lastEventAt 有变化）→ 回到高频：任务活跃（模型在
//     产出/权限在等裁决），serve 死亡要能被快速发现
//   - 高频下连续 fastProbes 次成功探活且无新事件 → 降频到 slow：任务静默
//     （waiting_review 挂过夜），200ms 高频探活是纯浪费——每天每任务约 43 万次
//     HTTP 请求（P1-17）
//   - 任一失败 → 立即回到高频：死亡判定是看门狗唯一职责，不能被降频拖慢
//     （慢档最坏 3 次 × 2s ≈ 6s 才判死，MVP 可接受）
func (a *Adapter) watchdogWithConfig(r *runState, cfg watchdogConfig) {
	failures := 0
	successes := 0
	prevEvent := r.lastEventAt.Load()
	interval := cfg.fastInterval
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-r.runCtx.Done():
			return
		case <-ticker.C:
			evTs := r.lastEventAt.Load()
			active := evTs != prevEvent
			prevEvent = evTs
			if active && interval != cfg.fastInterval {
				a.log.Debug("探活回到高频：收到新事件，任务活跃", "task", r.taskID)
				successes = 0
				interval = cfg.fastInterval
				ticker.Stop()
				ticker = time.NewTicker(interval)
			}
			if r.handle.Alive() {
				failures = 0
				successes++
				if interval == cfg.fastInterval && !active && successes >= cfg.fastProbes {
					// 高频下连续成功且无事件：任务进入静默期（如 waiting_review），
					// 降频省 HTTP 请求（P1-17）。Debug 而非 Info（修复 6）：
					// 与「回高频」对称，两档来回切时不刷 Info 噪音——任务正常干活时
					// 每 ~4 秒就在两档间切一次，Info 级别的「降频」配 Debug 的
					// 「回高频」会误导成「任务卡住」
					a.log.Debug("探活降频：任务静默，探活间隔升到慢档", "task", r.taskID,
						"fast", cfg.fastInterval, "slow", cfg.slowInterval)
					successes = 0
					interval = cfg.slowInterval
					ticker.Stop()
					ticker = time.NewTicker(interval)
				}
				continue
			}
			failures++
			successes = 0
			if interval != cfg.fastInterval {
				a.log.Debug("探活回到高频：出现失败，需快速判定死亡", "task", r.taskID)
				interval = cfg.fastInterval
				ticker.Stop()
				ticker = time.NewTicker(interval)
			}
			if failures >= watchdogFailThreshold {
				a.log.Error("opencode serve 探活失败达阈值，判定死亡",
					"task", r.taskID, "failures", failures)
				r.runCancel()
				return
			}
		}
	}
}

// emit 向事件通道投递一条 AdapterEvent 并打产出日志（所有事件统一的出口）。
//
// 同时为看门狗打活跃点（lastEventAt）：探活降频的「收到新事件回高频」信号
// （P1-17，见 watchdogWithConfig）。
//
// 并发：订阅 goroutine 与 idle 去抖定时器 goroutine 都会 emit，故写入与关闭
// 统一由 emitMu 串行化，evClosed 使关闭后的迟到投递静默丢弃而非 panic。
//
// 返回 false 表示 Stop 已关闭 stopCh 或通道已关闭，事件未被投递。
func (a *Adapter) emit(r *runState, ev executor.AdapterEvent) bool {
	r.lastEventAt.Store(time.Now().UnixNano())
	switch ev.Type {
	case "permission":
		a.log.Info("adapter 产出权限事件", "task", r.taskID, "type", ev.Type,
			"perm", ev.PermissionID, "text", turn.TruncateRunes(ev.Text, 80))
	case "question":
		a.log.Info("adapter 产出提问事件", "task", r.taskID, "type", ev.Type,
			"text", turn.TruncateRunes(ev.Text, 80))
	case "progress":
		a.log.Info("adapter 产出进度事件", "task", r.taskID, "type", ev.Type,
			"text", turn.TruncateRunes(ev.Text, 80))
	case "result":
		ok := ev.Result != nil && ev.Result.OK
		a.log.Info("adapter 产出结果事件", "task", r.taskID, "type", ev.Type, "ok", ok)
	default:
		a.log.Info("adapter 产出未知事件", "task", r.taskID, "type", ev.Type)
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

// closeEvents 关闭事件通道（只由 subscribeLoop 的 defer 调用，关闭权唯一）。
// 与 emit 共用 emitMu：置位 evClosed 后任何在途的 emit 都会静默丢弃，
// 保证 idle 定时器 goroutine 的迟到投递不会写已关闭通道。
func (r *runState) closeEvents() {
	r.emitMu.Lock()
	defer r.emitMu.Unlock()
	if r.evClosed {
		return
	}
	r.evClosed = true
	close(r.evCh)
}

// sseEvent 是 SSE 事件的通用外壳：type 区分事件类别，properties 是各类载荷，
// sessionID 用于多任务并发时的会话隔离（真实 /event 是全服务器广播流）。
type sseEvent struct {
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
	SessionID  string          `json:"sessionID"`
}

// mapEvent 把一条 SSE 事件映射为 0~N 条 AdapterEvent（宽容解析：未知事件
// Debug 跳过，绝不 panic、绝不中断订阅）。
func (a *Adapter) mapEvent(r *runState, raw json.RawMessage) {
	// 活跃打点挂在「收到 SSE 事件」上而非「产出 AdapterEvent」上（A-11）：
	// progress 有 30s 节流，挂在产出上会让正在流式输出的任务绝大部分时间
	// 被看门狗当成静默期而降频探活
	r.lastEventAt.Store(time.Now().UnixNano())
	var ev sseEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		a.log.Debug("SSE 事件解析失败，跳过", "task", r.taskID, "cause", err)
		return
	}
	// 会话隔离：真实 /event 广播全服务器事件，只处理本任务会话的事件。
	// 注意：真实事件里 sessionID 位于 properties 而非顶层（spike 实测），
	// 顶层字段是历史遗留，过滤必须从 properties 提取。
	sessionID := ev.SessionID
	if sessionID == "" {
		var prop struct {
			SessionID string `json:"sessionID"`
		}
		if json.Unmarshal(ev.Properties, &prop) == nil {
			sessionID = prop.SessionID
		}
	}
	if sessionID != r.session && !a.acceptForeign(r, ev, sessionID) {
		return
	}
	// 回合累积状态自 idle 去抖起被订阅 goroutine 与定时器 goroutine 共访：
	// 整个 switch 在 turnMu 下串行执行，与 resolveIdle 互斥。emit 只取 emitMu，
	// 不回取 turnMu，故持锁 emit 不会死锁（背压表现与去抖前一致）。
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	switch {
	case ev.Type == "permission.asked":
		a.mapPermissionAsked(r, ev.Properties)
	case ev.Type == "permission.replied":
		// 应答回显：approve/reject 后服务端回发 replied；若把它当新权限，
		// 协调者的 respond 会被当成再次询问，权限流程死循环——必须忽略
		a.log.Debug("permission.replied 应答回显，忽略", "task", r.taskID, "type", ev.Type)
	case ev.Type == "question.asked":
		a.mapQuestionAsked(r, ev.Properties)
	case ev.Type == "question.replied", ev.Type == "question.rejected":
		// 应答回显：与 permission.replied 同因——把回显当成新提问，协调者的
		// 答复会被当作再次提问，流程死循环
		a.log.Debug("question 应答回显，忽略", "task", r.taskID, "type", ev.Type)
	case ev.Type == "message.updated":
		a.mapMessageUpdated(r, ev.Properties)
	case ev.Type == "message.part.updated":
		a.mapPartUpdated(r, ev.Properties)
	case ev.Type == "message.part.delta":
		a.mapPartDelta(r, ev.Properties)
	case ev.Type == "session.status":
		a.mapSessionStatus(r, ev.Properties)
	case ev.Type == "session.idle":
		// idle 双信号去重：真实流在 session.status idle 后还会补发一条
		// session.idle，两条都触发会重复分类（重复 question/result）——
		// 主信号只有 session.status，这里 Debug 跳过
		a.log.Debug("session.idle 与 session.status idle 同现，忽略防重复触发",
			"task", r.taskID, "type", ev.Type)
	case ev.Type == "session.error":
		a.log.Warn("opencode 会话报错", "task", r.taskID,
			"properties", turn.TruncateRunes(string(ev.Properties), 200))
	default:
		a.log.Debug("未知 SSE 事件，跳过", "task", r.taskID, "type", ev.Type)
	}
}

// taskScopedEvents 是「必须归属到某个会话才能处理」的事件类型集合：它们都会
// 改变某个任务的回合状态或直接产出面向协调者的工单。其余类型（server.connected、
// heartbeat、catalog.updated、plugin.added 等）是服务器级广播，本就不带 sessionID。
var taskScopedEvents = map[string]bool{
	"permission.asked":     true,
	"permission.replied":   true,
	"question.asked":       true,
	"question.replied":     true,
	"question.rejected":    true,
	"message.updated":      true,
	"message.part.updated": true,
	"message.part.delta":   true,
	"session.status":       true,
	"session.idle":         true,
	"session.error":        true,
}

// acceptForeign 裁决一条「会话 id 与本任务不符」的事件是否仍要处理，
// 并保证两个方向都不静默。
//
// 参数：
//   - sessionID: 从顶层或 properties 提取到的会话 id（"" 表示事件没带）
//
// 返回：
//   - true 表示继续按本任务的事件处理，false 表示丢弃
//
// why（三条实测结论，改这里之前先读）：
//   - opencode 会为 subagent/task 工具派生子会话，其权限请求带子会话 id。
//     GET /session/{id} 返回 parentID，**父子关系端点是存在的**——本函数早期
//     版本的注释断言「本层无法把子会话映射回任务」，那是错的，B52 已实测推翻：
//     子会话的 parentID 就等于本任务的会话 id
//   - 子会话被 opencode 自己下了 permission:[{task,*,deny}]，不能再派生 subagent，
//     所以嵌套深度恒为 1，比一次 parentID 就够，不需要向上遍历
//   - 每个任务起自己的 opencode serve（见 proc.go 的 freePort），所以 /event 的
//     「全服务器广播」实际是「本任务这一个 serve 的广播」，流上出现的陌生会话
//     本就只可能是本任务自己派生的子会话
//
// why 仍然保留校验（不能改成一律放行）：校验存在的理由是防止跨任务串台。
// 缺 sessionID 的任务级事件更不能 fail-open——一条无归属的 permission.asked
// 会被当成本任务的审批门，协调者的批准动作会发到错误的会话。
func (a *Adapter) acceptForeign(r *runState, ev sseEvent, sessionID string) bool {
	if !taskScopedEvents[ev.Type] {
		return true // 服务器级广播事件：本就不带会话，交给下游的 default 分支跳过
	}
	if ev.Type == "permission.asked" {
		if sessionID == "" {
			a.log.Warn("收到不带会话 id 的审批请求，无法归属，未产出工单",
				"task", r.taskID, "own_session", r.session,
				"properties", turn.TruncateRunes(string(ev.Properties), 200))
			a.emitOwnershipFailure(r, sessionID, "事件未携带会话 id")
			return false
		}
		if _, ok := a.resolveChildSession(r, sessionID); ok {
			return true
		}
		a.emitOwnershipFailure(r, sessionID, "认亲失败")
		return false
	}
	// 非审批事件即使来自已认亲的子会话也照旧丢弃：回合记账（lastAssistantMsgID
	// 水位、pendingDelta、mapIdle 分类）整套围绕单一会话的消息序列构建，交错
	// 第二个会话的消息会直接污染水位与空闲判定。子 agent 干了什么仍看得到——
	// 主 agent 会在自己的收尾正文里转述（四家 executor 探针均已实测）。
	a.log.Debug("收到其他会话事件，跳过", "task", r.taskID,
		"type", ev.Type, "session", sessionID)
	return false
}

// resolveChildSession 判定一个陌生会话是否是本任务派生的子会话。
//
// 参数：
//   - sessionID: 事件携带的会话 id（调用方保证非空）
//
// 返回：
//   - title: 子会话标题（供工单标注）；认亲失败时为空
//   - ok:    是否认亲成功
//
// 注意：
//   - 只缓存正结果。一次网络抖动导致的判定失败若被缓存，这个子会话就被永久
//     拉黑，它后续每一条审批都会被丢弃——那正是本次要修的故障形态
//   - 不持 sessMu 跨 HTTP 调用：查缓存与写缓存各自短暂加锁，网络 I/O 不在锁下。
//     同一个新子会话的两条事件并发到达会各发一次 GET /session，幂等且无害，
//     不值得为它加 singleflight
//   - 不向上递归找祖先：opencode 自己给 subagent 下了 permission:[{task,*,deny}]
//     （真机实测），嵌套深度恒为 1。真出现二级说明这条不变量被打破了，那是断言
//     失败，该报警而不是在事件热路径上做无界遍历
func (a *Adapter) resolveChildSession(r *runState, sessionID string) (title string, ok bool) {
	r.sessMu.RLock()
	title, cached := r.childSessions[sessionID]
	r.sessMu.RUnlock()
	if cached {
		a.log.Debug("子会话认亲命中缓存", "task", r.taskID, "child", sessionID)
		return title, true
	}

	a.log.Info("子会话认亲开始", "task", r.taskID, "event_session", sessionID, "own_session", r.session)
	start := time.Now()
	// ctx 不挂 run 的生命周期：超时已被 ownershipTimeout 收在 5s 内，
	// 而 Stop 会关掉 SSE 流，最坏情况只是多等这一次往返
	ctx, cancel := context.WithTimeout(context.Background(), ownershipTimeout)
	defer cancel()
	d, err := r.api.GetSession(ctx, sessionID)
	if err != nil {
		a.log.Warn("子会话认亲失败：查询会话详情出错，本条审批请求丢弃",
			"task", r.taskID, "event_session", sessionID, "cause", err)
		return "", false
	}
	switch {
	case d.ParentID == r.session:
		r.sessMu.Lock()
		r.childSessions[sessionID] = d.Title
		r.sessMu.Unlock()
		a.log.Info("子会话认亲成功", "task", r.taskID, "child", sessionID,
			"parent", d.ParentID, "title", d.Title, "elapsed_ms", time.Since(start).Milliseconds())
		return d.Title, true
	case d.ParentID == "":
		a.log.Warn("子会话认亲失败：该会话没有父会话，是与本任务无关的顶层会话，本条审批请求丢弃",
			"task", r.taskID, "event_session", sessionID, "own_session", r.session)
	default:
		a.log.Warn("子会话认亲失败：父会话不是本任务会话，opencode「subagent 不可再派生 subagent」"+
			"（task:deny）的不变量可能已被打破，本条审批请求丢弃",
			"task", r.taskID, "event_session", sessionID, "event_parent", d.ParentID, "own_session", r.session)
	}
	return "", false
}

// emitOwnershipFailure 把一次归属判定失败播报给协调者。
//
// 参数：
//   - sessionID: 判定失败的会话 id（可能为空串）
//   - reason:    失败原因短语，进 progress 文本
//
// why：丢弃一条审批请求意味着 opencode 在等一个永远不会到来的决策，而 serve
// 活着、看门狗不触发——只写日志的话协调者在 handoff 里完全看不见。progress
// 只入库不阻塞，是把这件事送到 handoff show 事件历史的最轻通道。
func (a *Adapter) emitOwnershipFailure(r *runState, sessionID, reason string) {
	if sessionID == "" {
		sessionID = "(未携带)"
	}
	a.emit(r, executor.AdapterEvent{
		Type: "progress",
		Text: "丢弃了一条无法归属的审批请求（" + reason + "，会话 " + sessionID +
			"）：opencode 可能在等一个看不见的决策，任务若卡住请 handoff attach 查看现场",
	})
}

// mapPermissionAsked 处理 permission.asked（真实事件）：properties.id 即
// PermissionID（manager 按其派生命名空间化的 ticket id，稳定幂等：SSE 重连
// 重放同一权限请求时复用同一 id，CreateTicket 按派生 id 去重）。
//
// 描述组合：permission 字段（如 bash） + metadata.command（如 echo spike-hi）；
// 无 command 时退回 patterns 拼接——尽量贴近工具将要执行的表述。
func (a *Adapter) mapPermissionAsked(r *runState, props json.RawMessage) {
	var pa struct {
		ID         string   `json:"id"`
		SessionID  string   `json:"sessionID"`
		Permission string   `json:"permission"`
		Patterns   []string `json:"patterns"`
		Metadata   struct {
			Command string `json:"command"`
			// filepath 是小写 p——真机样本如此（testdata/perm_edit.json）。
			// 写成 filePath 会静默取到空串，然后整条请求退化成「提取不出
			// 结构」被 fail-closed 升级，表现为每次编辑都唤醒人，很难查。
			FilePath string `json:"filepath"`
			// external_directory 的 bash 形态没有 filepath，越界目录在这里
			ParentDir   string   `json:"parentDir"`
			Directories []string `json:"directories"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(props, &pa); err != nil {
		a.log.Debug("permission.asked 载荷解析失败，跳过", "task", r.taskID, "cause", err)
		return
	}
	if pa.ID == "" {
		a.log.Debug("permission.asked 事件缺 id，跳过", "task", r.taskID)
		return
	}
	text := pa.Permission
	if cmd := pa.Metadata.Command; cmd != "" {
		text += ": " + cmd
	} else if len(pa.Patterns) > 0 {
		text += ": " + strings.Join(pa.Patterns, " ")
	}
	// 描述下限（A-2）：三种真实形态（缺 permission / 缺 metadata.command /
	// 缺 patterns）都会拼出空串。空描述意味着协调者被要求批准一个空白行——
	// 宁可给出「未提供描述 + 权限 id」，让他知道要去 handoff attach 里看现场
	if strings.TrimSpace(text) == "" {
		a.log.Warn("permission.asked 无可读描述，按未说明权限交协调者",
			"task", r.taskID, "perm", pa.ID)
		text = "opencode 未提供权限描述（id " + pa.ID + "），请 handoff attach 查看现场"
	}
	// 子会话归属与标注（B52）。permSession 决定应答发往哪个会话；前缀让协调者
	// 一眼看出这条审批来自子 agent——子 agent 的越权与主 agent 的越权含义不同。
	//
	// why 前缀必须加在空描述兜底之后：前缀本身非空，先加前缀会让上面那段
	// 「描述为空就给兜底文本」的判空永远为假，真正空描述的请求就变成一条只有
	// 前缀的工单，协调者仍然看不到要批什么。
	permSess := pa.SessionID
	if permSess == "" {
		permSess = r.session
	}
	if permSess != r.session {
		r.sessMu.RLock()
		title := r.childSessions[permSess]
		r.sessMu.RUnlock()
		if title != "" {
			text = "[子 agent: " + title + "] " + text
		} else {
			// 认亲成功但标题为空（opencode 未给 title）：仍要标出来源，
			// 只是标不出是哪个子 agent
			text = "[子 agent] " + text
		}
	}
	r.sessMu.Lock()
	r.permSession[pa.ID] = permSess
	r.sessMu.Unlock()
	a.log.Info("权限请求已归属会话", "task", r.taskID, "perm", pa.ID,
		"session", permSess, "is_child", permSess != r.session)
	// 记下描述供「被拒终止回合」的诊断文本引用（本函数在 turnMu 下执行，见
	// mapEvent 的 switch 契约）；permID 全局唯一，表不随回合清空
	r.permText[pa.ID] = text

	// 结构化载荷（B23/B27）：permission 字段就是 opencode 的工具类别原文，
	// 真机实测取值有 bash / edit / external_directory，直接作归一化来源。
	//
	// 为什么路径不取 patterns：patterns 里是相对路径与通配摘要（真机样本里
	// edit 的 patterns 是 ["probe.md"]、external_directory 的是 ["/tmp/*"]），
	// 拿它判归属会把通配符当成路径。绝对路径只在 metadata 里。
	var req *executor.PermRequest
	tool := executor.NormalizePermTool(pa.Permission)
	paths := pa.Metadata.Directories
	if pa.Metadata.FilePath != "" {
		paths = append([]string{pa.Metadata.FilePath}, paths...)
	}
	switch {
	case pa.Metadata.Command != "":
		if tool == executor.PermToolOther {
			tool = executor.PermToolBash
		}
		// bash 形态的 external_directory 同时带命令与越界目录，两个都要给
		// permgate——命令走黑名单判据，目录走归属判据
		req = &executor.PermRequest{Tool: tool, Command: pa.Metadata.Command, Paths: paths}
	case len(paths) > 0:
		if tool == executor.PermToolOther {
			tool = executor.PermToolWrite
		}
		req = &executor.PermRequest{Tool: tool, Paths: paths}
	}
	if req == nil {
		// 提取不出结构 → manager 会 fail-closed 升级人工。记一条，否则
		// 「为什么这个请求没走审批者」在日志里无从查起
		a.log.Warn("opencode 权限请求提取不出结构化载荷，将由 manager 升级人工",
			"task", r.taskID, "perm", pa.ID, "permission", pa.Permission)
	}
	a.emit(r, executor.AdapterEvent{
		Type: "permission", PermissionID: pa.ID,
		Text: turn.TruncateMarked(text, permTextHardLimit),
		Perm: req,
	})
}

// mapQuestionAsked 处理 question.asked：opencode 原生 question 工具的提问请求。
//
// 与 permission.asked 同构（同一条 SSE 流上的并排事件），但有一处根本差别：
// **提问不结束回合**。question 工具阻塞等应答，session 不会转 idle，答复之后
// 是同一个回合继续——因此本函数**不得** clearTurn / advanceWatermark，
// 清缓冲会丢掉该回合已累积的文本，推进水位会让后续对账错位。
//
// 参数：props 为事件的 properties 原文（{id, sessionID, questions[], tool{}}）
//
// 注意：
//   - 本函数在 turnMu 下执行（见 mapEvent 的 switch 契约），可安全读写回合状态；
//     不得在此做网络 I/O
//   - 按 requestID 去重：seenQuestionIDs 只负责**进程内**的 SSE 重放去重；
//     跨进程（agentd 重启）的幂等由 manager 按事件上的 QuestionID 承担
func (a *Adapter) mapQuestionAsked(r *runState, props json.RawMessage) {
	var qa struct {
		ID        string         `json:"id"`
		SessionID string         `json:"sessionID"`
		Questions []QuestionInfo `json:"questions"`
	}
	if err := json.Unmarshal(props, &qa); err != nil {
		a.log.Debug("question.asked 载荷解析失败，跳过", "task", r.taskID, "cause", err)
		return
	}
	if qa.ID == "" {
		a.log.Debug("question.asked 事件缺 id，跳过", "task", r.taskID)
		return
	}
	if r.seenQuestionIDs[qa.ID] {
		a.log.Debug("question.asked 重复到达（SSE 重放），已忽略",
			"task", r.taskID, "request", qa.ID)
		return
	}
	r.seenQuestionIDs[qa.ID] = true

	text := renderQuestionTicket(qa.Questions)
	// 描述下限：questions 为空或全无正文时，渲染结果对协调者没有信息量。
	// 与 mapPermissionAsked 的空描述兜底同理——宁可给出「未提供内容 + 请求 id」，
	// 让他知道要去 handoff attach 里看现场，也不能静默丢弃（丢了就是死锁）
	if len(qa.Questions) == 0 {
		a.log.Warn("question.asked 无问题内容，按未说明提问交协调者",
			"task", r.taskID, "request", qa.ID)
		text = "opencode 提出了一个空提问（id " + qa.ID + "），请 handoff attach 查看现场后作答"
	}
	// 子会话标注（B52 同款）：子 agent 的提问与主 agent 的提问含义不同
	qSess := qa.SessionID
	if qSess == "" {
		qSess = r.session
	}
	if qSess != r.session {
		r.sessMu.RLock()
		title := r.childSessions[qSess]
		r.sessMu.RUnlock()
		if title != "" {
			text = "[子 agent: " + title + "] " + text
		} else {
			text = "[子 agent] " + text
		}
	}

	r.pendingQuestionID = qa.ID
	r.pendingQuestions = qa.Questions
	// 回合级去重标记（B49 §4.4）：本回合已通过工具问过，回合末的 trailer ask
	// 不再重复出单。Task 5 消费它
	r.askedViaTool = true

	a.log.Info("收到 executor 提问，转工单交协调者", "task", r.taskID,
		"request", qa.ID, "session", qSess, "is_child", qSess != r.session,
		"question_count", len(qa.Questions))
	a.emit(r, executor.AdapterEvent{
		Type: "question", QuestionID: qa.ID, Text: turn.ClampQuestion(text),
	})
	// 注意：此处刻意不调 clearTurn / advanceWatermark / captureStartCommit——
	// 回合还在跑（见函数头注释）
}

// mapMessageUpdated 处理 message.updated：真实事件只携带 properties.info
// （role/messageID），不带文本——文本载体是 message.part.updated/delta。本层
// 只用它登记「哪些 messageID 属于 user」，这些消息的 text part 不进回合累积
// （回合只算模型输出）。
//
// why（不拿它清空回合）：user 消息曾被当作「新回合开始」的清空信号，但服务端
// 会重播同一条 user 消息（spike5 实测同一 msg id 出现 3 次，每次 session.diff
// 后一次）。进程内的 userMsgs 只在本次运行有效——agentd 重启后 Resume 出来的
// 运行态拿到的是空表，重播的老消息会被当「首见」而清空整个回合，恢复后累积的
// 文本全部丢弃、idle 走空回合分支永不分类，任务静默挂死（A-3）。回合缓冲由
// mapIdle 在分类后清空即可，不需要第二个清空信号。
func (a *Adapter) mapMessageUpdated(r *runState, props json.RawMessage) {
	var msg struct {
		Info struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"info"`
	}
	if err := json.Unmarshal(props, &msg); err != nil {
		a.log.Debug("message.updated 载荷解析失败，跳过", "task", r.taskID, "cause", err)
		return
	}
	if msg.Info.Role != "user" || msg.Info.ID == "" {
		return // assistant 消息不携带文本，无事可做
	}
	r.userMsgs[msg.Info.ID] = true
}

// mapPartUpdated 处理 message.part.updated：text 类型 part 携带该 part 的
// 全量文本快照（真实流：文本首帧可能是空串的「part 创建」事件，随后走
// part.delta 增量，收尾再回发完整快照）。
//
// why（类型登记先于过滤 + 按 part 存当前值 + 与 delta 对账）：
//   - part 类型必须在 text 过滤前登记进 partTypes：message.part.delta 只有
//     field 没有 part 类型字段（spike5 实测 reasoning 增量也是 field=text），
//     part.updated 是唯一携带类型的事件——错过登记，reasoning/tool 增量会被
//     当模型输出累积进回合与 render.log。类型揭晓时还要处置该 part 在
//     pendingDelta 里的暂存增量（见 mapPartDelta）
//   - 服务端对同一 part 可能多次回发快照，且增量流结束后会补发全量快照——
//     按 messageID+partID 保存该 part 的当前文本，快照即覆盖，天然去重
//   - 快照不是已见文本的延续时（服务端修订了这一段），覆盖而非叠加：回合文本
//     与 render.log 都是给人读的，"Hello world" 改成 "Hi world" 不该读到
//     "Hello worldHi world"
//   - 空文本快照是 part 创建事件而非有效全量，不置 partSnap、也不清空已累积：
//     后续 delta 仍要照常累积（spike5 实测顺序：空快照 → delta 流 → 全量快照）
func (a *Adapter) mapPartUpdated(r *runState, props json.RawMessage) {
	var pu struct {
		Part struct {
			ID        string `json:"id"`
			MessageID string `json:"messageID"`
			Type      string `json:"type"`
			Text      string `json:"text"`
		} `json:"part"`
	}
	if err := json.Unmarshal(props, &pu); err != nil {
		a.log.Debug("message.part.updated 载荷解析失败，跳过", "task", r.taskID, "cause", err)
		return
	}
	p := pu.Part
	if p.ID == "" || p.MessageID == "" {
		return // 缺 id 的事件无法对账，跳过
	}
	key := partKey(p.MessageID, p.ID)
	// 类型登记先于 text 过滤（why 见函数注释）：delta 无类型字段，只有这里能
	// 建立「part -> 非 text」的事实，mapPartDelta 据此跳过 reasoning/tool 增量
	r.partTypes[key] = p.Type
	isText := p.Type == "text" && !r.userMsgs[p.MessageID]
	if isText {
		r.lastAssistantMsgID = p.MessageID
	}
	// 类型揭晓：把该 part 暂存的增量按真实类型落地或丢弃（A-5）
	a.flushPending(r, key, isText)
	if !isText {
		return // reasoning/tool/step-start 等非文本 part 与 user 消息文本不参与累积
	}
	if p.Text == "" {
		return // part 创建事件：无有效全量，等 delta
	}
	r.partSnap[key] = true
	seen := r.partSeen[key]
	if p.Text == seen {
		return // 快照与已累积一致：去重
	}
	if seen != "" && !strings.HasPrefix(p.Text, seen) {
		a.log.Debug("part 快照被服务端修订，按新快照覆盖",
			"task", r.taskID, "msg", p.MessageID, "part", p.ID)
	}
	a.setPartText(r, key, p.Text)
}

// flushPending 在 part 类型揭晓时处置它的暂存增量：是文本就落地进回合，
// 不是就整段丢弃（reasoning/tool 的增量绝不能进回合与 render.log）。
func (a *Adapter) flushPending(r *runState, key string, isText bool) {
	buf, ok := r.pendingDelta[key]
	if !ok {
		return
	}
	delete(r.pendingDelta, key)
	r.pendingBytes -= len(buf)
	if !isText {
		a.log.Debug("暂存增量所属 part 非文本，整段丢弃", "task", r.taskID, "bytes", len(buf))
		return
	}
	a.setPartText(r, key, r.partSeen[key]+buf)
}

// mapPartDelta 处理 message.part.delta：field=text 的流式增量。
//
// why（已知类型才落地 + 未知类型暂存 + 与快照对账）：
//   - 只有**已登记为 text** 的 part 增量才进回合：partTypes 由 part.updated
//     登记（delta 本身只有 field 无类型，spike5 实测 reasoning 增量同样是
//     field=text），不加这道闸，reasoning/tool 增量会被当模型输出
//   - 类型未知时不猜（A-5）：「part.updated 总是先于 delta 到达」只是抓包里的
//     观测顺序，SSE 跨重连无顺序保证。猜 text 会泄漏思维链，直接丢弃会丢模型
//     输出——暂存进 pendingDelta，等 part.updated 揭示类型再落地或丢弃
//   - 若该 part 已收到非空全量快照（part.updated），增量已被快照覆盖，
//     跳过防重复；否则增量直接追加——真实流里「part 创建（空文本）→
//     逐条 delta 增长」期间并无有效快照可对账（spike5 实测）
func (a *Adapter) mapPartDelta(r *runState, props json.RawMessage) {
	var pd struct {
		MessageID string `json:"messageID"`
		PartID    string `json:"partID"`
		Field     string `json:"field"`
		Delta     string `json:"delta"`
	}
	if err := json.Unmarshal(props, &pd); err != nil {
		a.log.Debug("message.part.delta 载荷解析失败，跳过", "task", r.taskID, "cause", err)
		return
	}
	if pd.Field != "text" || pd.Delta == "" || pd.MessageID == "" || pd.PartID == "" {
		return
	}
	if r.userMsgs[pd.MessageID] {
		return
	}
	r.lastAssistantMsgID = pd.MessageID
	key := partKey(pd.MessageID, pd.PartID)
	switch r.partTypes[key] {
	case "text":
		if r.partSnap[key] {
			return // 全量快照已含该文本：增量冗余
		}
		a.setPartText(r, key, r.partSeen[key]+pd.Delta)
	case "":
		// 类型未揭晓：暂存等待 part.updated 裁决（why 见函数注释）
		if r.pendingBytes+len(pd.Delta) > pendingDeltaLimit {
			a.log.Warn("类型未知的 part 增量超出暂存上限，丢弃",
				"task", r.taskID, "msg", pd.MessageID, "part", pd.PartID,
				"limit_bytes", pendingDeltaLimit)
			return
		}
		r.pendingDelta[key] += pd.Delta
		r.pendingBytes += len(pd.Delta)
	default:
		// 已知非 text part（reasoning/tool）的增量：不累积、不进 render.log
	}
}

// mapSessionStatus 处理 session.status：status.type=idle 是回合结束的主信号
// （真实样本：{"type":"session.status","properties":{"status":{"type":"idle"}}}；
// busy 状态同结构，不触发）。同现的 session.idle 由 mapEvent 忽略防重复。
func (a *Adapter) mapSessionStatus(r *runState, props json.RawMessage) {
	var st struct {
		Status struct {
			Type string `json:"type"`
		} `json:"status"`
	}
	if err := json.Unmarshal(props, &st); err != nil {
		a.log.Debug("session.status 载荷解析失败，跳过", "task", r.taskID, "cause", err)
		return
	}
	if st.Status.Type == "idle" {
		a.scheduleIdle(r, props)
		return
	}
	// 非 idle（busy 等）说明回合仍在推进：撤销在途的候选回合结束
	r.idleGen++
}

// scheduleIdle 把一次 idle 登记为「候选回合结束」，静默满 idleGrace 后才真正分类。
// 调用方须持有 turnMu。
//
// why（去抖而非见 idle 即分类）：idle 是 opencode 的会话状态信号，不等于「模型
// 这一轮说完了」——工具调用间隙、权限等待期间都可能出现瞬时 idle。见 idle 即
// 分类会把半截回合当成完整回合：命中 git 兜底时更会因「仓库里已有新提交」谎报
// completed，协调者据此执行 done，Stop 就在 opencode 仍在干活时杀掉了执行者进程。
//
// 宽限期内任何回合推进（新增文本、非 idle 状态、下一条 idle）都会自增 idleGen，
// 使在途的候选失效——真正的回合结束后不会再有事件，宽限期自然走完。代价是回合
// 分类延迟 idleGrace，相对协调者分钟级的往返可忽略。
func (a *Adapter) scheduleIdle(r *runState, raw json.RawMessage) {
	r.idleGen++
	gen := r.idleGen
	if r.idleTimer != nil {
		r.idleTimer.Stop()
	}
	grace := a.idleGrace
	if grace <= 0 {
		grace = idleGraceDefault
	}
	// raw 由 SSE 解析缓冲复用，定时器在回调返回后才读，必须拷贝
	snapshot := append(json.RawMessage(nil), raw...)
	a.log.Debug("登记候选回合结束，等待去抖", "task", r.taskID, "grace", grace)
	r.idleTimer = time.AfterFunc(grace, func() { a.resolveIdle(r, gen, snapshot) })
}

// resolveIdle 是 idle 去抖到期后的回合分类入口（定时器 goroutine 执行）。
// 代次不匹配说明宽限期内回合又推进了，本次候选作废。
func (a *Adapter) resolveIdle(r *runState, gen uint64, raw json.RawMessage) {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	if r.idleGen != gen {
		a.log.Debug("候选回合结束已被新活动撤销", "task", r.taskID,
			"scheduled_gen", gen, "current_gen", r.idleGen)
		return
	}
	a.mapIdle(r, raw)
}

// cancelPendingIdle 停掉在途的 idle 去抖定时器并作废候选（自行加锁，
// 供订阅结束等 turnMu 未持有的路径调用）。
func (r *runState) cancelPendingIdle() {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	r.idleGen++
	if r.idleTimer != nil {
		r.idleTimer.Stop()
		r.idleTimer = nil
	}
}

// mapIdle 回合结束（session.status idle 主信号）时分类收尾：turn.ParseTrailer 判定
// ask/finish/none，none 走 git 实况兜底（why 见 fallbackClassify）。分类后清空
// 回合缓冲。
//
// 空回合（无累积文本）不再静默跳过：零文本转失败结果交协调者（B21）——idle 但
// 无文本说明文本流没被本层接住（事件结构变化/增量对账失败）或供应商流中断，
// 这是「任务可能静默挂死」的观测点，必须产事件而非 Debug 静默。被拒终止的
// 空回合例外，它走 question（有内容可问，见下文）。props 为触发 idle 的
// session.status 载荷，仅用于日志上下文。
func (a *Adapter) mapIdle(r *runState, raw json.RawMessage) {
	text := r.turnText()
	if strings.TrimSpace(text) == "" {
		// 被拒终止的回合：opencode 收到 reject 直接终结回合，只留 error 状态的
		// tool part、零文本。旧实现在此静默 return，任务停在 running 直到 2h
		// 看门狗（2026-08-08 真实派发实测的 P0）——必须转 question 唤醒协调者
		if rejected := r.takeTurnRejected(); len(rejected) > 0 {
			a.log.Warn("回合因权限被拒终止且无文本产出，转提问交协调者裁决",
				"task", r.taskID, "rejected", rejected)
			a.emit(r, executor.AdapterEvent{
				Type: "question", Text: turn.ClampQuestion(rejectedTurnQuestion(rejected)),
			})
			a.advanceWatermark(r)
			r.clearTurn()
			r.takeAskedViaTool() // 回合结束（被拒终止空回合），标记必须随回合清掉，否则漏到下一回合
			r.captureStartCommit(a)
			return
		}
		// 零文本回合转失败结果交协调者（B21）：旧实现在此静默 return，任务停在
		// running 直到 2h 看门狗。
		//
		// 为什么是 result{OK:false} 而不是 question：上面「被拒终止」那条走
		// question，因为那个现场有内容可问；零文本回合没有任何东西可问，它是一份
		// 故障报告——result{OK:false} 的语义才对得上，且 FailReason 能把现场
		// 写清楚。manager 的 handleResult 对 OK=false 的既有处置（作废挂起工单 →
		// failed 事件 → 落 waiting_review）正是我们要的，continue 立刻可用
		a.log.Warn("idle 但回合无文本，转失败结果交协调者", "task", r.taskID,
			"event", turn.TailRunes(string(raw), 120))
		a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.session, Result: &executor.Result{
			OK: false,
			FailReason: "回合结束但零文本产出（可能是供应商流中断）；executor 仍在线，" +
				"可 continue 续接重试",
		}})
		a.advanceWatermark(r)
		r.clearTurn()
		r.takeAskedViaTool() // 回合结束（零文本回合），标记必须随回合清掉，否则漏到下一回合
		r.captureStartCommit(a)
		return
	}
	// 回合级去重标记必须在回合终结时恰好取走一次，且与走哪条分支无关：
	// 漏在 finish/none 分支上不清，标记就漏到下一回合，把那一回合的真提问
	// 误抑制掉——任务停在 running 无人知晓，正是 B49 要消灭的形态
	askedViaTool := r.takeAskedViaTool()
	kind, t := turn.ParseTrailer(text)
	switch kind {
	case "ask":
		// 回合级去重（B49 §4.4）：本回合已通过 question 工具问过协调者时，
		// 回合末的 trailer ask 多半是同一个问题的复述——出第二张单会让协调者
		// 面对两份措辞不同的同一件事（grok 那次 askedViaTool 踩过的同一个坑）。
		// 兜底通道存在的目的是「保证回合不静默结束」，工具已经问过时该诉求
		// 已经满足
		if askedViaTool {
			a.log.Debug("本回合已通过 question 工具提问，抑制 trailer 提问工单",
				"task", r.taskID, "trailer_tail", turn.TailRunes(t.Question, 80))
			break
		}
		a.emit(r, executor.AdapterEvent{Type: "question", Text: turn.ClampQuestion(t.Question)})
	case "finish":
		a.emit(r, executor.AdapterEvent{Type: "result", Result: &executor.Result{
			OK: true, Branch: t.Branch, CommitHash: t.Commit,
			Summary: t.Summary, SessionID: r.session,
		}})
	case "none":
		a.fallbackClassify(r, text)
	}
	a.advanceWatermark(r)
	r.clearTurn()
	// 兜底分类的 git 基线按回合刷新（C-1）：基线若固定在 run 起点，第一回合
	// 提交之后的每个无 trailer 回合都会「相对起点有新提交」，于是带着上一回合的
	// commit hash 谎报 completed。多回合（提问 → reply → continue）是 handoff
	// 的主路径，基线必须跟着回合走。
	r.captureStartCommit(a)
}

// fallbackClassify 是「模型未按纪律输出协议 trailer」的兜底分类。
//
// why（兜底分类规则）：回合结束但 turn.ParseTrailer 判 none。此时拿 git 实况裁决：
//   - 相对回合起点有新 commit → 发 result{OK:false}（B74）。**不替模型宣布完成**：
//     模型没说完成，handoff 就不说。翻转不给协调者加任何一次操作——OK 与 !OK
//     都落 waiting_review，done 与 continue 在那里都合法；变的只是事件从
//     「已完成，摘要如下」变成「有新提交，但模型未按纪律宣布完成」。
//   - 没有新 commit / git 查询失败 → 把回合全文交协调者裁决（question），流程不卡死。
func (a *Adapter) fallbackClassify(r *runState, text string) {
	a.log.Warn("回合未输出协议 trailer，走 git 兜底", "task", r.taskID,
		"turn_tail", turn.TailRunes(text, 120))
	branch, commit, hasNew, err := a.gitTurnStatus(r)
	if err != nil || !hasNew {
		if err != nil {
			a.log.Error("git 兜底查询失败", "task", r.taskID, "cause", err)
		}
		a.log.Info("兜底判定无新提交，转提问交协调者裁决", "task", r.taskID, "has_new", hasNew)
		a.emit(r, executor.AdapterEvent{Type: "question", Text: turn.ClampQuestion(text)})
		return
	}
	a.log.Warn("兜底判定有新提交，但模型未宣布完成，转失败交协调者裁决",
		"task", r.taskID, "branch", branch, "commit", commit)
	a.emit(r, executor.AdapterEvent{Type: "result",
		Result: turn.NoTrailerResult(r.session, branch, commit, text)})
}

// gitTurnStatus 查询仓库当前分支与 HEAD commit，并对比任务起点判定是否有新提交。
//
// 薄封装：实现在 turn 共享包（B2/B3 两个 adapter 同构逻辑），本方法保留
// 调用点已有的 r 上下文（repoPath/startCommit）。
func (a *Adapter) gitTurnStatus(r *runState) (branch, commit string, hasNew bool, err error) {
	return turn.GitTurnStatus(r.repoPath, r.startCommit)
}

// maybeProgress 节流发 progress：每任务至多每 progressThrottle 一条
// （Task 12 e2e 遗留：按回合粒度细化）。
func (a *Adapter) maybeProgress(r *runState) {
	now := time.Now()
	if now.Sub(r.lastProgress) < progressThrottle {
		return
	}
	r.lastProgress = now
	a.emit(r, executor.AdapterEvent{Type: "progress", Text: turn.TailRunes(r.turnText(), 200)})
}

// setPartText 把某个 part 的当前文本置为 text，并把变化反映到 render.log
// 与 progress——part.updated 与 part.delta 两个入口共用。
//
// 追加（新文本是已见文本的延续）时 render.log 只写增量，保持 tail -f 的
// 流式观感；服务端修订该段时写一条带标记的整段，让旁观者知道前面那段已作废
// （render.log 是 append-only 文件，改不了历史）。
func (a *Adapter) setPartText(r *runState, key, text string) {
	old, existed := r.partSeen[key]
	if !existed {
		r.turnOrder = append(r.turnOrder, key)
	}
	if text == old {
		return
	}
	// 新增/修订文本即回合仍在推进：撤销在途的候选回合结束（见 scheduleIdle）
	r.idleGen++
	r.partSeen[key] = text
	render := strings.TrimPrefix(text, old)
	if old != "" && !strings.HasPrefix(text, old) {
		render = "\n[以上一段已被服务端修订为]\n" + text
	}
	if err := r.appendRender(render); err != nil {
		a.log.Warn("追加 render.log 失败", "task", r.taskID, "cause", err)
	}
	a.maybeProgress(r)
}

// turnText 按 part 首见顺序拼出当前回合的完整文本。
//
// 为什么不维护一个累加字符串：服务端会修订同一 part 的快照，累加字符串没法
// 撤销已写入的旧值（A-6）；按 part 存当前值再拼接，修订天然是覆盖。
func (r *runState) turnText() string {
	var b strings.Builder
	for _, key := range r.turnOrder {
		b.WriteString(r.partSeen[key])
	}
	return b.String()
}

// clearTurn 清空回合累积（回合分类终结时调用）。
//
// partTypes/userMsgs 不清空：它们是会话级事实（part 类型、user 消息 id 全会话
// 唯一），保留才能保证回合边界之后到达的 reasoning 增量与重放的 user 文本 part
// 永不混入下一回合（A-4）。pendingDelta 同样保留：类型未揭晓的暂存增量跨回合
// 边界仍可能被 part.updated 认领。
func (r *runState) clearTurn() {
	r.turnOrder = nil
	r.partSeen = make(map[string]string)
	r.partSnap = make(map[string]bool)
}

// partKey 生成 part 累积对账的键：messageID+partID 唯一标识一个 part
// （真实流中 part id 全局唯一，双 id 是为了对账键可读可查）。
func partKey(msgID, partID string) string {
	return msgID + "\x00" + partID
}

// appendRender 把消息文本增量追加进 render.log（handoff attach 实况可见）。
//
// 薄封装：实现在 turn 共享包（B2/B3 两个 adapter 同构逻辑），本方法保留
// 调用点已有的 taskDir 上下文（renderPath 由 newRun 按 taskDir 推导）。
func (r *runState) appendRender(delta string) error {
	return turn.AppendRender(r.renderPath, delta)
}

// advanceWatermark 把本回合最后一条 assistant 消息 id 落进 proc.json，作为对账水位。
//
// why（正常路径也必须写）：对账靠「会话尾部消息 id != 水位」判定「有未消费的
// 已完结回合」。若只在对账成功时写水位，一个正常送达的终态就不会推进水位，
// 下一次对账会把它当成丢失事件**重复补发**一遍。
//
// 失败只 Warn 不中断：水位写不进去的后果是下次可能重复补发一条终态（事件表多
// 一条、状态机 waiting_review→waiting_review 被 ErrBadTransit 挡掉），比中断
// 回合分类轻得多。
func (a *Adapter) advanceWatermark(r *runState) {
	msgID := r.lastAssistantMsgID
	if msgID == "" {
		a.log.Debug("本回合无 assistant 消息 id，跳过水位前进", "task", r.taskID)
		return
	}
	pi, err := readProcInfo(r.taskDir)
	if err != nil {
		a.log.Warn("前进对账水位失败：读凭据出错，下次对账可能重复补发",
			"task", r.taskID, "msg", msgID, "cause", err)
		return
	}
	if pi.LastTurnMsgID == msgID {
		return // 已是当前值，不必写盘
	}
	old := pi.LastTurnMsgID
	pi.LastTurnMsgID = msgID
	if err := writeProcInfo(r.taskDir, pi); err != nil {
		a.log.Warn("前进对账水位失败：写凭据出错，下次对账可能重复补发",
			"task", r.taskID, "msg", msgID, "cause", err)
		return
	}
	a.log.Info("对账水位已前进", "task", r.taskID, "from", old, "to", msgID)
}
