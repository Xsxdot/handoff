// 本文件实现 handoff 的状态机中枢与 adapter 事件中介（系统的心脏）。
//
// 职责：
//   - 任务生命周期的唯一状态写入者：dispatch（pending→running）、permission/question
//     中介（→waiting_answer→running）、result（→waiting_review）、continue/done（→running/completed）
//   - 把 executor 的 AdapterEvent 中介为「ticket + 事件 + 状态迁移」三件套：
//     permission/question 落 ticket 并挂到 hub.WaitAnswer 等协调者应答，
//     progress 只入库，result 落 completed/failed 事件进 waiting_review
//   - 协调者应答经 reply 回程（server）→ NotifyAnswer 唤醒等待 goroutine → 回传 executor
//   - stop：协调者主动中止任务（停 executor、作废工单（随终态迁移收口完成）、落 failed）
//
// 中介时序与工单幂等的不变量（P1-2/P1-6/P1-7）：
//   - 权限工单 id = taskID+":"+permID（按任务命名空间隔离，跨任务 permID 碰撞不吞工单）；
//     question 工单 id 为 uuid。reply 路由整链（事件 payload / hub 等待键 / 应答回程）
//     都用工单 id，只有回传 adapter 时还原裸 permID
//   - 中介顺序为「落库 → 置 waiting_answer → 启动 waiter goroutine → Publish」：
//     状态先就位（reply 回程 resumeIfIdle 的回迁判定依赖它）；waiter 的 hub 注册
//     是异步的，reply 先于注册到达时退化为「无等待者 → 自愈中继」路径兜底
//     （详见 handlePermission 的顺序契约）
//   - CreateTicket 返回 created=false（重放）时跳过全部后续动作，幂等是完整的
//
// 边界：
//   - 不做审批判断：「allow 之外一律 reject」「回答原样透传」是仅有的两条翻译规则，
//     批不批、答什么由协调者（人/上层）决定
//   - 不直接接触 executor 进程/会话细节，一切经由 executor.Adapter 契约
//   - 不负责看门狗（Task 12）等横向能力；git 工作区操作委托 workspace 包（Task 9）
//   - dispatch 前经 workspace.PrepareWorkspace 准备任务工作区（分支×worktree，
//     脏工作区/非法参数拒绝），其余 git 操作（diff/fetch/run）由 server 路由直接调用 workspace 包
//
// 「失败也进 waiting_review」的 why：
//
//	失败的 result 同样进 waiting_review 而非自动重试或直接 failed——让协调者看到
//	失败现场（failed 事件携带原因）决定重试话术；自动重试会在同一坑里反复烧
//	token 且无人知情，failed 终态又让任务失去续接入口。人工裁决是唯一解。
//
// 状态迁移并发安全：
//   - 全部迁移经 store.UpdateTaskState 的 CAS（WHERE state=旧值）落库，双写者
//     只有一个赢家；reply 回程的 resumeIfIdle 与应答 goroutine 的回迁 race 由
//     CAS + transitBestEffort（容忍 ErrBadTransit）吸收
package agentd

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/discipline"
	"github.com/Xsxdot/handoff/internal/envfile"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/turn"
	"github.com/Xsxdot/handoff/internal/permgate"
	"github.com/Xsxdot/handoff/internal/prochost"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/google/uuid"
)

// eventType 常量：AdapterEvent.Type 的合法取值（与 proto.EventType 的映射见各 handler）。
const (
	adapterEventPermission = "permission"
	adapterEventQuestion   = "question"
	adapterEventProgress   = "progress"
	adapterEventResult     = "result"
	adapterEventUsage      = "usage"
)

// errBadDispatchRequest 是 Dispatch 入参错误的哨兵（server 层映射为 400）。
var errBadDispatchRequest = errors.New("dispatch 请求参数非法")

// errExecutorStartFailed 是 Dispatch 启动 executor 失败（adapter.Start 返回错误）
// 的哨兵（server 层映射见 writeDispatchError 的对应分支）。
//
// 为什么单独成类：executor 依赖缺失（如执行者二进制不在 PATH）是
// **环境问题**而非 agentd 内部故障——协调者需要看到真因（exec: "opencode":
// executable file not found）才能动手装依赖，扁平化的「派发任务失败」只会让
// 协调者去 agentd.log 里翻一行 exec 错误，完全没有可行动信息。
var errExecutorStartFailed = errors.New("启动 executor 失败")

// errEnvResolveFailed 表示 env 文件解析失败（文件缺失/语法错/文件名非法）。
//
// server 层据此回 500 并回显真因：落到 writeDispatchError 的 default 分支只会回
// 扁平的「派发任务失败」，真因被吞——这正是 B16 的根因，不能再犯一次。
var errEnvResolveFailed = errors.New("解析 env 文件失败")

// errDisciplineResolveFailed 表示纪律块文件不可用（文件名非法/读不到/超限）。
// 与 errEnvResolveFailed 并列，让 server 层把真因回显给协调者而不是扁平成 500。
var errDisciplineResolveFailed = errors.New("纪律块解析失败")

// Manager 是任务状态机中枢与 adapter 事件中介。
//
// 并发安全：无共享可变字段（st/hub/ads/cfg/log 构造后只读），
// 每个任务的中介 goroutine 与应答 goroutine 通过 store CAS + hub 路由协作。
// approver 相关的 in-flight/失败计数/停用表由 apMu 保护。
type Manager struct {
	st  *store.Store
	hub *Hub
	// ads 是 executor 注册表（name → Adapter），构造后只读 map，并发安全依旧。
	// 任务经 task.Executor（adapterFor）或缺省名（resolveExecutor）路由到对应实现。
	ads map[string]executor.Adapter
	cfg *config.Config
	log *slog.Logger
	// env 是 env 文件解析器（B19）：Dispatch 时按 task.Executor 解析出要注入
	// executor 进程的环境变量。构造后只读，每次 For 都重新读盘（支持热更新）。
	env *envfile.Resolver
	// discipline 按 executor 名裁出该次派发要注入的纪律块（B129）。
	discipline *discipline.Resolver
	// approver 是分级审批链的廉价模型裁决器；nil=不启用（二期前行为：
	// 权限请求直接升级人工协调者）。构造后只读。
	approver *Approver
	// gate 是权限判据网关（B23/B27）：把权限请求判成 AutoAllow/Consult/Escalate。
	// **与 approver 解耦**——approver 为 nil 时 gate 依然工作，否则未配置审批者的
	// 部署会被工作区内的每一次写入淹没（spec §5.3）。构造后只读。
	gate *permgate.Gate
	// aaMu 保护 aaCount：每任务累计的自动放行次数，回合终结时汇总打一条 Info。
	// 不能完全静默——出问题时要有第一现场。
	aaMu    sync.Mutex
	aaCount map[string]int
	// 审批链运行时状态（apMu 保护）：
	//   - apInflight：正在裁决中的 ticket id 集合（防 SSE 重放双呼审批者）
	//   - apFails：每任务连续裁决失败计数（Err 非 nil 才累计）
	//   - apDisabled：已停用审批链的任务集合（连续失败 3 次）
	//   - denyGuidance：协调者拒绝时给出的原因，等下一条 question 到达时下发
	//     （取走式，见 takeDenyGuidance 的 why）
	apMu         sync.Mutex
	apInflight   map[string]bool
	apFails      map[string]int
	apDisabled   map[string]bool
	denyGuidance map[string]string
	// stopping 是「接下来这次事件通道关闭是我们自己发起的」的意图标记
	// （apMu 之外单独用 mu 保护）。why 见 reconcile.go 的 noteStopping。
	mu       sync.Mutex
	stopping map[string]struct{}
	// sweepOwned 是「本任务的终态清扫已被 Done/Stop 认领」的标记（与 stopping 共用 mu）。
	//
	// 为什么需要它：终态清扫有两个都成立、且位置不能互换的挂点——
	//   - Done/Stop 就地扫（B103）：**必须早于 worktree 删除**，还活着的进程把
	//     cwd 钉在工作树里会让 git worktree remove 失败；这条还带有界重试，
	//     等存活锁释放
	//   - transit 终态分支扫（B119）：挂在唯一的终态收口，才能覆盖**将来新增的**
	//     终态路径；B93 只加在 handleResult，Stop 那条就漏了
	// 两条并存会让 Done/Stop 扫两遍。本标记在 Done/Stop **入口**置位、由 transit
	// 的终态分支检查并清位，于是「顺序保证」与「兜底覆盖」都留住，每条路径只扫一次。
	//
	// 为什么置位在入口而不是清扫之后：两条路径的先后是相反的——Done 是
	// transit→sweepAfterStop，Stop 是 sweepAfterStop→transit。只有入口这个点
	// 对两者都早于各自的 transit。
	sweepOwned map[string]struct{}
	// sweepProcs 是「清扫某任务残留进程」的测试缝。**生产路径恒为 nil**，
	// 由 sweep 方法退回 m.sweepTaskProcsOnce；非测试代码不得赋值。
	//
	// 为什么返回 error（B103）：Done/Stop 要对 ErrExecutorAlive 做有界重试，
	// 而 ErrExecutorAlive 恰恰是最容易让这条修复静默失效的竞态——存活锁的释放
	// 依赖 shim 真正退出，它落后于 stopExecutor 返回。吞掉它就是 B93 犯过的错：
	// 宣称「终态即清扫」，实际每次都被拒，直到 B103 排查才发现。
	sweepProcs func(taskID string) error
	// usageMu 保护 lastUsage：usage 事件的去重指纹（Task 2 通路）。
	usageMu   sync.Mutex
	lastUsage map[string]string // taskID → 上一次上报的用量指纹，去重用
}

// sweep 调用清扫，走测试缝或真实实现。
//
// 返回：prochost.Sweep 的错误；ErrExecutorAlive 表示执行者仍活着（调用方可重试）
func (m *Manager) sweep(taskID string) error {
	if m.sweepProcs != nil {
		return m.sweepProcs(taskID)
	}
	return m.sweepTaskProcsOnce(taskID)
}

// sweepRetryAttempts / sweepRetryGap 是终态清扫对 ErrExecutorAlive 的重试参数。
//
// 是变量而非常量：测试要把间隔调到毫秒级，否则每条用例都真等 600ms。
var (
	sweepRetryAttempts = 3
	sweepRetryGap      = 200 * time.Millisecond
)

// sweepAfterStop 在停完 executor 之后清扫这个任务留下的逃逸后代。
//
// 参数：taskID 为已停 executor 的任务
//
// 为什么必须在 stopExecutor 之后：prochost.Sweep 在存活锁仍被持有时直接拒绝
// （ErrExecutorAlive）——杀活着的执行者是 Kill 的职责，两者风险模型不同。
//
// 为什么必须重试而不是一次拒绝就算了：存活锁的释放依赖 shim 进程真正退出，
// 它落后于 stopExecutor 返回，中间有一个真实窗口。一次被拒就放弃，这条修复
// 在生产上会静默失效——B93 就是这么错的（宣称「终态即清扫」，实测每次都被
// ErrExecutorAlive 拒掉，直到 B103 排查才发现，中间隔了一整轮验收）。
//
// 注意：重试用尽打 Warn 而不是 Info。它意味着「executor 该死没死、逃逸后代
// 大概率残留」，是需要人看见的事。
func (m *Manager) sweepAfterStop(taskID string) {
	for i := 0; i < sweepRetryAttempts; i++ {
		if err := m.sweep(taskID); !errors.Is(err, prochost.ErrExecutorAlive) {
			return
		}
		if i < sweepRetryAttempts-1 {
			time.Sleep(sweepRetryGap)
		}
	}
	m.log.Warn("终态清扫放弃：存活锁始终未释放，逃逸后代可能残留",
		"task", taskID, "attempts", sweepRetryAttempts, "gap", sweepRetryGap)
}

// noteSweepOwned 认领本任务的终态清扫：调用方保证自己会调 sweepAfterStop，
// transit 的终态分支据此跳过兜底清扫。必须在调用方自己的 transit 之前调用。
//
// 参数：taskID 为目标任务
func (m *Manager) noteSweepOwned(taskID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepOwned[taskID] = struct{}{}
}

// consumeSweepOwned 检查并清除认领标记，返回是否命中（命中即本轮不必兜底清扫）。
//
// 为什么检查即清除：标记只在 Done/Stop 的一次调用内有意义。用完就删让 map 不随
// 任务数无界增长；万一 transit 没走到终态（迁移失败提前返回），残留的那一条会在
// 该任务下次落终态时被消费掉。
func (m *Manager) consumeSweepOwned(taskID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.sweepOwned[taskID]
	delete(m.sweepOwned, taskID)
	return ok
}

// NewManager 创建任务管理器。
//
// 参数：
//   - st: 持久化存储（任务/事件/工单的唯一落库点）
//   - hub: 进程内实时路由（事件广播 + ticket 应答等待）
//   - ads: executor 注册表（name → Adapter，如 {"opencode": ..., "fake": ...}）；
//     任务按 executor 名路由，缺省名取 cfg.Executor.Default
//   - cfg: 配置（DataDir 用于派生任务目录、Executor.Default 为缺省执行者名）
//   - discMapping: 取当前纪律块映射的函数（生产上传 (*Server).DisciplineMapping）；
//     nil 时全部 executor 走内置默认
//   - approver: 审批链裁决器；nil=不启用
//   - gate: 权限判据网关；**不得为 nil**，它与 approver 是否启用无关
//   - log: 本模块日志入口
//
// 注意：
//   - 调用方须保证 log 为统一配置后的 logger；st/hub 必须已就绪
func NewManager(st *store.Store, hub *Hub, ads map[string]executor.Adapter, cfg *config.Config,
	discMapping func() map[string]string, approver *Approver, gate *permgate.Gate, log *slog.Logger) *Manager {
	disc := discipline.NewResolver(discipline.Dir(cfg.DataDir), discMapping, log)
	disc.Preflight()
	return &Manager{
		st: st, hub: hub, ads: ads, cfg: cfg, approver: approver, gate: gate, log: log,
		env:          envfile.NewResolver(envfile.Dir(cfg.DataDir), cfg.Env, log),
		discipline:   disc,
		apInflight:   map[string]bool{},
		apFails:      map[string]int{},
		apDisabled:   map[string]bool{},
		denyGuidance: map[string]string{},
		aaCount:      map[string]int{},
		stopping:     map[string]struct{}{},
		sweepOwned:   map[string]struct{}{},
	}
}

// adapterFor 按任务记录的执行者名解析其 adapter。
//
// 规则：task.Executor 非空时按该名查注册表；为空（老任务兼容）回退缺省执行者
// （cfg.Executor.Default）。查不到时报错（错误带已注册名列表）。
func (m *Manager) adapterFor(taskID string) (executor.Adapter, error) {
	task, err := m.st.GetTask(taskID)
	if err != nil {
		return nil, err
	}
	name := task.Executor
	if name == "" {
		name = m.cfg.Executor.Default
	}
	ad, ok := m.ads[name]
	if !ok {
		m.log.Error("任务执行者未注册，无法路由", "task", taskID, "executor", name,
			"registered", registeredNames(m.ads))
		return nil, fmt.Errorf("任务 %s 执行者 %q 未注册", taskID, name)
	}
	return ad, nil
}

// resolveExecutor 在 dispatch 期把请求的执行者名解析为 adapter。
//
// 规则：name 空回退缺省（cfg.Executor.Default）；未注册返回 errBadDispatchRequest
// 包装的错误（server 层映射 400）并列出已注册名。
func (m *Manager) resolveExecutor(name string) (string, executor.Adapter, error) {
	if name == "" {
		name = m.cfg.Executor.Default
	}
	ad, ok := m.ads[name]
	if !ok {
		m.log.Warn("dispatch 指定未注册执行者", "executor", name, "registered", registeredNames(m.ads))
		return "", nil, fmt.Errorf("%w: 执行者 %q 未注册（可用: %s）", errBadDispatchRequest, name, strings.Join(registeredNames(m.ads), ", "))
	}
	return name, ad, nil
}

// resolveModel 决定任务下发时用哪个模型名。空串表示「不指定，由执行者自身默认接管」。
//
// 优先级：任务级 req.Model > 缺省执行者的配置模型 > 空（执行者自身默认）。
//
// why 要判 execName == Default：cfg.Executor.Model 的语义是**缺省执行者**的默认模型，
// 不是全局默认。以前不分执行者一律套上，于是配了 opencode 模型名的机器派 codex 时
// 第一回合就被 provider 顶回 400。
//
// 边界：显式传 --executor 且它恰好等于 cfg.Executor.Default 时，**照样套配置模型**——
// 语义与调用方有没有把名字显式写出来无关。（execName 来自 resolveExecutor，
// 空的 req.Executor 在那里已被归一化成 Default，故这一个判断覆盖两条路径。）
//
// why 不做按执行者的模型映射表：那需要新配置键，会撞上「配了新键的机器跨版本回滚
// 被严格解析拒启动」的老坑（B88）；而 --model 已经能覆盖非缺省执行者的场景。
func (m *Manager) resolveModel(reqModel, execName string) string {
	if reqModel != "" {
		return reqModel
	}
	if execName == m.cfg.Executor.Default {
		return m.cfg.Executor.Model
	}
	return ""
}

// registeredNames 返回注册表全部执行者名（按字母序，供错误提示与日志）。
func registeredNames(ads map[string]executor.Adapter) []string {
	names := make([]string, 0, len(ads))
	for n := range ads {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ExecutorNames 返回本机已注册的 executor 名，按名字升序。
//
// 用途：纪律配置端点要把「注册的 adapter」与「配置里已出现的键」取并集，
// 后者不能省——一个配了纪律块但当前没注册的名字（改名、临时摘掉）若不列出，
// 界面就看不见它，而它还躺在配置里。
func (m *Manager) ExecutorNames() []string { return registeredNames(m.ads) }

// DispatchReq 是 Dispatch 的入参：任务仓库、base64 计划与二期派发参数。
type DispatchReq struct {
	// ProjectID 是项目身份（sha256(归一化 origin) 前 16 位），由调用方离线算出。
	// 与 ProjectName 二选一，都空时 400；同时给出时以 ProjectID 为准。
	ProjectID string
	// ProjectName 是项目的人可读引用，仅服务 --project <名字> 与 Web 控制台
	//（它没有 cwd，从项目树里选）。
	ProjectName string
	PlanB64     string // plan 内容，base64 编码（路由/CLI 层编码，此处解码）
	PlanName    string // plan 文件名（归档展示用，写入 task 的 PlanPath 目录下）
	Target      string // 目标主机名（归档展示用，记入 task.Target）
	// Prompt 是无 plan 文件时的直接指令（prompt-only 派发）；与 PlanB64 至少其一
	// 非空。plan 非空时作为「附加指令」拼接在计划之后。
	Prompt string
	// Name 是任务展示名（空时从 plan 名/prompt 派生，见 deriveName）。
	Name string
	// Executor 是任务选择的执行者名；空=缺省（cfg.Executor.Default）。
	Executor string
	// Model 是任务级模型覆盖；空=配置 executor.model，再空=executor 自身默认。
	Model string
	// Branch / NewBranch 分支二选一（与 PrepareWorkspace 的 WorkspaceReq 一致）：
	// Branch=切到已存在分支；NewBranch=新建分支（空且 Branch 空=自动 handoff/<id8>）。
	Branch    string
	NewBranch string
	// Base 是新分支起点（仅与 NewBranch/自动分支连用；空=HEAD）。
	Base string
	// Worktree / NewWorktree worktree 二选一：Worktree=用户自带 worktree；
	// NewWorktree=在 DataDir/worktrees 下新建 managed worktree（done 时删除）。
	Worktree    string
	NewWorktree bool
	// BaseCommit 是协调者本地 HEAD 的提交号（40 位十六进制），用于校验任务仓库
	// 不落后于本地；空=不校验（本地派发或调用方 cwd 不是 git 仓库）。
	BaseCommit string
}

// planSummaryLimit 是 plan 摘要的截断上限（按 rune 计）。
//
// 为什么 200：plan_summary 是 attach/tasks 里「这个任务要干什么」的速览字段，
// 完整计划已落 plan_path 文件；上限同时防超长单行 plan（如压缩过的 markdown）
// 撑爆任务行与终端输出。
const planSummaryLimit = 200

// planSummaryFromContent 从 plan 内容生成任务摘要。
//
// 规则：取首个非空行（markdown 计划的标题位，如 `# 修复登录态丢失`），按
// planSummaryLimit 截断；内容为空或全空行时返回空串。
// 为什么用「首个非空行」而非「前 N 字符整体截断」：plan 开头常有空行/分隔线/
// 注释行，前 N 字符的摘要可能全是空白与噪音；首行标题是 markdown 惯例的意图
// 浓缩位，协调者一眼即知任务方向（P1-12）。
func planSummaryFromContent(content []byte) string {
	for _, line := range strings.Split(string(content), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return truncateRunes(trimmed, planSummaryLimit)
		}
	}
	return ""
}

// deriveName 决定任务的展示名，优先级：显式 Name > plan 文件名（去日期前缀与
// .md 后缀）> prompt 前 20 rune（不切断单词）。
//
// 为什么去日期前缀：约定 plan 文件常以 2026-08-08-<主题>.md 命名，日期前缀是
// 归档排序用的噪音，做任务名会让列表里全是「2026-08-08-…」看不出主题。
// 为什么 prompt 截断不切断单词：任务列表一行能展示的长度有限，直接取前 20 rune
// 会把「…改成 br」这类半截英文留在列表里；截到下一个空白处（或末尾）让名字
// 以完整单词收尾，可读性远好于硬切。
func deriveName(name, planName, prompt string) string {
	if name != "" {
		return name
	}
	if planName != "" {
		n := planName
		n = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}-`).ReplaceAllString(n, "")
		n = strings.TrimSuffix(n, ".md")
		if n != "" {
			return n
		}
	}
	r := []rune(prompt)
	const limit = 20
	if len(r) > limit {
		cut := limit
		// 截断处若切断单词（下一字符非空白），顺延过残缺单词到完整词尾；
		// 再吞掉紧随的空白，保证名字以「完整单词 + 分隔空白」收尾而不是
		// 半截英文（词尾恰好落在 limit 内时两循环都不触发，直接按 limit 切）
		for cut < len(r) && !unicode.IsSpace(r[cut]) {
			cut++
		}
		for cut < len(r) && unicode.IsSpace(r[cut]) {
			cut++
		}
		return string(r[:cut])
	}
	return string(r)
}

// ticketRequest 是工单 request 列的通用载体，kind 区分 gate/ask。
type ticketRequest struct {
	Kind       string `json:"kind"`
	Permission string `json:"permission,omitempty"` // gate：权限描述
	Question   string `json:"question,omitempty"`   // ask：问题原文
}

// permissionPayload 是 permission_request 事件的 payload（含 ticket_id 供回复）。
type permissionPayload struct {
	TicketID   string `json:"ticket_id"`
	Permission string `json:"permission"`
	Kind       string `json:"kind"`
}

// questionPayload 是 question 事件的 payload（含 ticket_id 供回复）。
type questionPayload struct {
	TicketID string `json:"ticket_id"`
	Question string `json:"question"`
	Kind     string `json:"kind"`
}

// deliveryFailedPayload 是 delivery_failed 事件的 payload：哪张工单没送到、
// 为什么、以及协调者该做什么。
type deliveryFailedPayload struct {
	TicketID string `json:"ticket_id"`
	Reason   string `json:"reason"`
	Hint     string `json:"hint"`
}

// progressPayload 是 progress 事件的 payload。
type progressPayload struct {
	Text string `json:"text"`
}

// completedPayload 是 completed 事件的 payload。
type completedPayload struct {
	Branch     string `json:"branch"`
	CommitHash string `json:"commit"`
	Summary    string `json:"summary"`
}

// failedPayload 是 failed 事件的 payload。
type failedPayload struct {
	FailReason string `json:"fail_reason"`
	// Branch/CommitHash 为 omitempty：绝大多数 failed（executor 崩溃、看门狗
	// 判死）没有 git 实况，让空字段出现在 payload 里会让下游以为「查过 git 且
	// 分支是空」。只有回合纪律类失败（无 trailer 但有新提交）才带这两个字段——
	// 它们是协调者判断「这回合到底干到哪儿」的唯一结构化依据，降级成 FailReason
	// 里的一段自由文本就没法再结构化取用了。
	Branch     string `json:"branch,omitempty"`
	CommitHash string `json:"commit,omitempty"`
	// ProcUsage 是任务失败时刻的进程占用快照；读不出数时为 nil。
	//
	// 为什么用指针 + omitempty 而不是零值：一个「0/0」的快照会被读成
	// 「死亡时机器很空闲」，那是彻头彻尾的谎话，比没有快照更糟。nil 表示
	// 「没测到」，与「测到了，很空闲」是两件事，必须能区分
	ProcUsage *prochost.Admission `json:"proc_usage,omitempty"`
}

// newFailedPayload 构造带占用快照的失败载荷。
//
// 参数：reason 为失败原因，原样保留不做改写；branch/commit 为可选的 git 实况，
// 绝大多数失败路径没有（进程退出、对账），传空串即可
//
// 为什么所有 failed 事件都要走这里：三个发射点（Stop、executor 死亡对账、
// reconcile）各自构造等于三处都可能漏挂快照，而漏掉的那次恰好可能就是
// 配额事故那次。协调者拿到「死亡时 2390/2400」与「死亡时 300/2400」，
// 一眼就能定性两个完全不同的排查方向。git 实况同样只此一处带上（B74）：
// 另起一条不带 ProcUsage 的构造路径，就等于把 B73 的快照约定废了。
//
// 为什么这里不打日志：它只是构造载荷，失败本身在三个发射点各自已有日志；
// 在这里再记一遍等于同一件事写四次。
func newFailedPayload(reason, branch, commit string) failedPayload {
	p := failedPayload{FailReason: reason, Branch: branch, CommitHash: commit}
	if a := admissionFn(); a.Known {
		p.ProcUsage = &a
	}
	return p
}

// approverDecisionPayload 是 approver_decision 事件的 payload：审批者对一次权限
// 请求的裁决结果。Decision 取 approve/escalate/error（error=裁决本身失败）。
type approverDecisionPayload struct {
	TicketID   string `json:"ticket_id"`
	Permission string `json:"permission"`
	Decision   string `json:"decision"`
	Reason     string `json:"reason"`
	ElapsedMS  int64  `json:"elapsed_ms"`
}

// approvalDroppedPayload 是 approval_dropped 事件的 payload。
type approvalDroppedPayload struct {
	TicketID string `json:"ticket_id"`
	Decision string `json:"decision"` // 审批者原本的裁决：approve / escalate / error
	State    string `json:"state"`    // 裁决回来时任务已处的状态
}

// approverDisabledPayload 是 approver_disabled 事件的 payload：任务级审批链
// 因连续失败被停用的原因。
type approverDisabledPayload struct {
	Reason string `json:"reason"`
}

// permissionReusePayload 是 permission_reuse 事件的 payload。
type permissionReusePayload struct {
	TicketID      string `json:"ticket_id"`
	PriorTicketID string `json:"prior_ticket_id"`
	Fingerprint   string `json:"fingerprint"`
	Permission    string `json:"permission"`
}

// denyGuidancePayload 是 deny_guidance_relayed / deny_guidance_dropped 事件的
// payload：协调者拒绝时给出的原因。Dropped 时 Cause 说明没能下发的缘由
// （回合终结 / adapter 解析失败 / Send 失败），协调者据此知道要用 continue
// 自己把话带上。
type denyGuidancePayload struct {
	Reason string `json:"reason"`
	Cause  string `json:"cause,omitempty"`
}

// maxApproverFails 是同任务审批者连续裁决失败（Err 非 nil）多少次后停用审批链。
//
// 为什么 3：对「已损坏的审批者命令」（如二进制被卸载、模型服务永久报错）的一次
// 重试代价是整条权限请求被升级、协调者被叫醒一次——反复重试只会形成「每次权限都
// 升级 + 每次审批都失败」的重试风暴，烧人工注意力的同时毫无收益；3 次是「确认
// 不是偶发抖动」的合理样本量。
const maxApproverFails = 3

// permEventTextLimit 是 permission_request / approver_decision 事件 payload 里
// 权限描述的展示上限。事件是唤醒消息，短即可；全文在工单里，经 handoff show 取。
const permEventTextLimit = 200

// permEventText 把权限描述压成事件 payload 用的短文本，超限时带显式截断标记——
// 无标记的截断会让协调者以为看到的就是全部（这正是 B6 的根因），有标记才知道
// 要去 handoff show 看工单里的全文。
func permEventText(s string) string {
	if len([]rune(s)) <= permEventTextLimit {
		return s
	}
	return truncateRunes(s, permEventTextLimit) + executor.TruncationMarker
}

// Dispatch 派发一个新任务：准备任务分支 → 建任务 → 建 taskDir 写 plan → Adapter.Start →
// running → 启动中介 goroutine 消费事件流。
//
// 参数：
//   - req: 仓库路径与 base64 计划（字段说明见 DispatchReq）
//
// 返回：
//   - 已入库的任务（state 为 running）；Adapter.Start 失败时返回错误，
//     此时任务已落库并迁移为 failed（供协调者经 tasks 命令查看失败现场）
//
// 注意：
//   - 任务分支（handoff/<id8>）的准备发生在建任务之前：分支准备是纯前置校验
//     （工作区干净/可开分支），失败时不落任何任务记录，协调者修好仓库重新
//     dispatch 即可——不会为每次被拒的派发留下 failed 噪音
//   - 分支名经 store.SetTaskField 白名单字段 "branch" 写入任务（不随 CreateTask
//     带列写入，保持「创建期只写创建时已知的字段」的约定）
func (m *Manager) Dispatch(ctx context.Context, req DispatchReq) (task *proto.Task, err error) {
	m.log.Info("dispatch 进入",
		"project_id", req.ProjectID, "project_name", req.ProjectName,
		"plan_name", req.PlanName, "target", req.Target,
		"executor", req.Executor, "model", req.Model, "name", req.Name,
		"branch", req.Branch, "new_branch", req.NewBranch, "base", req.Base,
		"base_commit", req.BaseCommit, "worktree", req.Worktree, "new_worktree", req.NewWorktree)
	defer func() {
		if err != nil {
			m.log.Error("dispatch 失败",
				"project_id", req.ProjectID, "project_name", req.ProjectName, "cause", err)
		} else {
			m.log.Info("dispatch 完成", "task", task.ID)
		}
	}()

	// B62：项目解析。放在最前面：后面所有前置校验（仓库可用性、工作目录占用、
	// 基线决议）都要拿到本机路径才有意义。它同时是「必须先登记才能派发」这条
	// 不变式的唯一执行点——本机 CLI 收到 ErrProjectNotRegistered 会自动补登记
	// 后重发，服务端这边不做任何降级。
	entries, err := m.st.ListProjectLocations()
	if err != nil {
		m.log.Error("dispatch 前置：读取项目位置失败", "cause", err)
		return nil, err
	}
	loc, err := resolveProject(req.ProjectID, req.ProjectName, entries)
	if err != nil {
		return nil, err
	}
	// repoPath 是本次派发的工作仓库，从此刻起全部前置校验都用它。
	// 它由**本机查表**得出，调用方无从指定——这正是 B62 要立的规矩。
	repoPath := loc.Path
	m.log.Info("dispatch 项目已解析",
		"project_id", loc.ProjectID, "name", loc.Name, "path", repoPath)

	// 校验：repo 必填；plan 与 prompt 至少其一（prompt-only 派发）
	if repoPath == "" || (req.PlanB64 == "" && req.Prompt == "") {
		return nil, fmt.Errorf("%w: repo_path=%q plan_b64 长度=%d prompt 长度=%d",
			errBadDispatchRequest, repoPath, len(req.PlanB64), len(req.Prompt))
	}
	// dispatch 期解析执行者：req.Executor 空回退缺省；未注册按参数错误拒绝（400）
	execName, ad, err := m.resolveExecutor(req.Executor)
	if err != nil {
		return nil, err
	}
	model := m.resolveModel(req.Model, execName)

	// env 注入（B19）：按 executor 名解析 env 文件。位置刻意排在最前段——早于建任务、
	// 早于 ResolveBaseline 与 PrepareWorkspace。解析失败是配置问题，此刻还没有任何
	// 落库/建树副作用，拒发是干净的；若放到 ad.Start 前才解析，任务已落库、worktree
	// 已建，就变成「创建了一个注定 failed 的任务」，与 spec §6「任务不创建」矛盾
	envKVs, err := m.env.For(execName)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errEnvResolveFailed, err)
	}
	// 纪律块裁决与 env 解析同段：失败是配置问题，此刻还没有落库/建树副作用，
	// 拒发是干净的。
	discBlock, err := m.discipline.For(execName)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errDisciplineResolveFailed, err)
	}

	// 内容合成：plan 解码后作为主体；prompt 非空时——
	//   plan 非空：拼接为「附加指令」小节（为什么：prompt 是派发当刻的补充意图，
	//   与 plan 是同一任务的两个信息面，放进同一文件让 executor 一次读全，避免
	//   二次上下文丢失）；plan 空：prompt 即任务内容
	var planContent []byte
	if req.PlanB64 != "" {
		planContent, err = base64.StdEncoding.DecodeString(req.PlanB64)
		if err != nil {
			return nil, fmt.Errorf("%w: 解码 plan_b64: %v", errBadDispatchRequest, err)
		}
		if req.Prompt != "" {
			planContent = append(planContent, []byte("\n\n## 附加指令（派发时提供）\n\n"+req.Prompt)...)
		}
	} else {
		planContent = []byte(req.Prompt)
	}
	planName := req.PlanName
	if planName == "" {
		planName = "prompt.md" // prompt-only 的 PlanName 兜底：无 plan 文件时固定名归档
	}
	summary := planSummaryFromContent(planContent)
	name := deriveName(req.Name, req.PlanName, req.Prompt)

	now := time.Now().UTC()
	taskID := uuid.NewString()

	// 派发前置 1（B45）：仓库有效性。必须排在 ResolveBaseline 之前——对一个非
	// git 路径，ResolveBaseline 会把它误诊成 ErrBaseCommitMissing（「任务仓库落后
	// 于本地，请先 git push」），那是个比沉默更糟的答案；managed 路径上则一路
	// 走到 worktree add 才失败，扁平成 500
	if err := EnsureRepoUsable(ctx, repoPath); err != nil {
		return nil, err
	}

	// 派发前置 2（B42）：工作目录占用守卫。managed 树每任务一棵，天然不冲突，
	// 不必查；另两种模式的目标目录在派发前就已知，Dispatch 自己算得出来。
	// 排在 ResolveBaseline 之前：后者在基线缺失时会做一次 git fetch（网络代价），
	// 一个注定要被拒的派发不该先付这笔钱
	occupied := ""
	if !req.NewWorktree {
		occupied = repoPath
		if req.Worktree != "" {
			occupied = req.Worktree
		}
	}
	if err := m.guardWorkdirBusy(occupied); err != nil {
		return nil, err
	}

	// 基线决议（B4 校验 + B35 起点）：放在工作区准备之前——基准不对时后面建的
	// 分支全是错的，且此刻还没有任何落库/建树副作用，拒发是干净的
	baseline, err := ResolveBaseline(ctx, repoPath, req.BaseCommit)
	if err != nil {
		return nil, err
	}
	// 起点优先级：显式 --base > 决议出的基线 > 空（交给 git 默认）。
	// 为什么 Branch 模式要排除：切一个已存在的分支没有「起点」这回事，把基线
	// 硬塞进去会被 PrepareWorkspace 的 base×branch 互斥直接拒掉。
	// 为什么显式 --base 时不报分叉：用户已经明确指定了起点，再警告是噪音。
	start, ahead := req.Base, 0
	if start == "" && req.Branch == "" {
		start, ahead = baseline.Start, baseline.Ahead
		if ahead > 0 {
			m.log.Warn("任务仓库 HEAD 领先基线，新分支不含这些提交",
				"repo", repoPath, "start", start, "ahead", ahead)
		}
	}
	m.log.Info("基线起点已确定", "repo", repoPath, "start", start, "ahead", ahead,
		"explicit_base", req.Base != "")

	// B76：起点必须以 sha 形态交给 git。给分支名会触发 DWIM——base 只有
	// origin/<name> 时 git 会忽略显式的 -b 并开出 base 名字的分支，退出码还是 0。
	// 解析放在这里（而不是 PrepareWorkspace 内部）是因为 start 同时喂给工作区
	// 准备与任务记录的 BaseCommit，一次解析服务两处，两者不可能再分叉。
	if start != "" {
		resolved, rerr := resolveCommit(ctx, repoPath, start)
		if rerr != nil {
			return nil, rerr
		}
		if resolved != start {
			m.log.Info("起点原文已解析为提交号", "repo", repoPath, "base", start, "sha", resolved)
		}
		start = resolved
	}

	// 准入闸必须排在建任务行、建 worktree 之前：拒发要干干净净，
	// 不能留下一个建了一半的任务等人收
	if err := checkProcHeadroom("dispatch"); err != nil {
		return nil, err
	}

	// 派发前置：按分支×worktree 正交请求准备工作区（脏检查/建分支/建 worktree）。
	// 为什么放在建任务之前：工作区准备是纯前置校验，失败时不落孤儿任务记录，
	// 协调者修好仓库后重新 dispatch 即可（见 Dispatch doc 注意）
	ws, err := PrepareWorkspace(ctx, WorkspaceReq{
		Repo: repoPath, TaskID: taskID,
		Branch: req.Branch, NewBranch: req.NewBranch, Base: start,
		Worktree: req.Worktree, NewWorktree: req.NewWorktree,
		WorktreesDir: filepath.Join(m.cfg.DataDir, "worktrees"),
	})
	if err != nil {
		return nil, fmt.Errorf("git 工作区准备: %w", err)
	}
	// 补偿清理 defer（P2-2 修复）：PrepareWorkspace 成功之后、executor 真正接管
	// 工作区之前（ad.Start 成功）的**任何**错误返回，都要把已建的 managed worktree
	// 清掉。为什么必须覆盖全部错误返回而不能逐个调用点补：落 failed 的任务没有
	// 任何清理路径（done 只认 waiting_review，见 Stop 修复），MkdirAll/WriteFile/
	// CreateTask/SetTaskField/ad.Start 任一失败漏补，该 worktree 就永久残留。
	// 为什么 executor 接管后不再补偿：ad.Start 成功后 executor 已在 worktree 里干活，
	// 此时删工作树会把运行中的任务脚下抽空——泄漏与破坏之间宁可留待看门狗处置。
	executorStarted := false
	defer func() {
		if err != nil && !executorStarted {
			m.compensateWorkspace(ctx, taskID, repoPath, ws)
		}
	}()

	// taskDir 是任务专属工作目录（计划文件与 executor 侧任务物料都放这里）。
	// why 0700：目录内存 serve 启动脚本 run_serve.sh（0600，含随机密码）与
	// serve.json（0600，含密码）——目录对他人可读会让文件名的存在性可被
	// 探知，且任何权限疏漏都直接暴露凭据；与 DataDir（agentd 启动时 0700）
	// 保持一致
	taskDir := filepath.Join(m.cfg.DataDir, "tasks", taskID)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		return nil, fmt.Errorf("创建任务目录 %s: %w", taskDir, err)
	}
	planPath := filepath.Join(taskDir, planName)
	if err := os.WriteFile(planPath, planContent, 0o600); err != nil {
		return nil, fmt.Errorf("写计划文件 %s: %w", planPath, err)
	}

	task = &proto.Task{
		ID:       taskID,
		Target:   req.Target,
		RepoPath: repoPath,
		// PlanPath 不在 SetTaskField 白名单，只能在创建时一并写入
		PlanPath:  planPath,
		State:     proto.TaskStatePending,
		CreatedAt: now,
		UpdatedAt: now,
		// 二期字段创建时即已知，随 CreateTask 写入（WorkDir 三种模式都是满的：
		// 原地=仓库路径、用户树/managed=工作树路径；proto.Task.Workdir() 的
		// 空串回退只服务旧库历史行）
		Name:            name,
		Executor:        execName,
		Model:           model,
		Discipline:      discBlock.Source,
		WorkDir:         ws.WorkDir,
		WorktreeManaged: ws.Managed,
		// 基线随创建期一并入库（此刻已由 ResolveBaseline 决议完毕），
		// 不走 SetTaskField——那个白名单只服务「创建时还不知道」的字段
		BaseCommit: start,
		BaseAhead:  ahead,
		// B43：新树不含主仓这些未提交改动，随任务落库供 CLI 回显（不阻断派发）
		RepoDirtyCount: ws.RepoDirtyCount,
		RepoDirtyFiles: ws.RepoDirtyFiles,
	}
	if err := m.st.CreateTask(task); err != nil {
		return nil, err
	}

	// 分支名经 SetTaskField 白名单写入（见 Dispatch doc 注意）
	if err := m.st.SetTaskField(taskID, "branch", ws.Branch); err != nil {
		m.log.Error("写入任务分支失败", "task", taskID, "branch", ws.Branch, "cause", err)
		// 分支已在仓库建好但任务记录写不上：按派发失败处理，落 failed 供人工清理
		m.transitBestEffort(taskID, proto.TaskStateFailed, "写分支名失败")
		return nil, fmt.Errorf("记录任务分支: %w", err)
	}
	// 内存态同步补上 branch，保证传给 adapter 的 StartReq.Task 完整
	task.Branch = ws.Branch

	// plan 摘要经 SetTaskField 白名单落库（P1-12）：PlanPath 是 agentd 侧文件路径，
	// 协调者读不到——spec §7 要求全新会话能知道「这个任务本来要干什么」，
	// plan_summary 就是 attach/tasks 里那一眼速览。失败与 branch 同款按派发失败处理
	if err := m.st.SetTaskField(taskID, "plan_summary", summary); err != nil {
		m.log.Error("写入任务 plan 摘要失败", "task", taskID, "cause", err)
		m.transitBestEffort(taskID, proto.TaskStateFailed, "写 plan 摘要失败")
		return nil, fmt.Errorf("记录任务 plan 摘要: %w", err)
	}
	task.PlanSummary = summary
	m.log.Info("plan 摘要已生成", "task", taskID, "summary", truncateRunes(summary, 40))
	m.log.Info("工作区就绪", "task", taskID, "workdir", ws.WorkDir, "managed", ws.Managed)

	if err := ad.Start(ctx, executor.StartReq{Task: *task, PlanContent: string(planContent),
		TaskDir: taskDir, Env: envKVs, Discipline: discBlock.Text}); err != nil {
		m.log.Error("adapter 启动失败", "task", taskID, "cause", err)
		// pending→failed 合法；失败现场留在任务里，协调者可见。
		// 注意：本错误返回由上方 defer 补偿清理 managed worktree（executor 尚未接管）；
		// 包 errExecutorStartFailed 哨兵，让 server 层把真因回显给协调者（修复 3）
		m.transitBestEffort(taskID, proto.TaskStateFailed, "adapter start 失败")
		return nil, fmt.Errorf("%w: %v", errExecutorStartFailed, err)
	}
	// executor 已接管工作区：此后的错误（transit 落库失败等 store 级故障）不再补偿
	// 清理——worktree 正被运行中的 executor 使用，删了反而破坏运行中的任务
	executorStarted = true
	if err := m.transit(taskID, proto.TaskStateRunning, "dispatch"); err != nil {
		return nil, err
	}
	// 返回最新快照：transit 已把状态改为 running（含 updated_at 刷新），
	// 不能让调用方拿到创建时的 pending 旧值
	task, err = m.st.GetTask(taskID)
	if err != nil {
		return nil, fmt.Errorf("读取派发后的任务: %w", err)
	}
	task.Discipline = discBlock.Source
	// 派发回显：纪律块配置化之后，写 plan 的人再也看不到它躺在 plan 头部了。
	if discBlock.Source != "" {
		m.appendProgress(taskID, "纪律块: "+discBlock.Source)
	}
	m.log.Info("纪律块已注入", "task", taskID, "executor", execName,
		"source", discBlock.Source, "bytes", len(discBlock.Text))
	go m.mediate(taskID)
	return task, nil
}

// guardWorkdirBusy 拒绝把任务派到已被活跃任务占用的工作目录（B42）。
//
// 参数：
//   - workDir: 目标工作目录；空串=managed 模式（每任务一棵新树），直接放行
//
// 返回：
//   - nil：无人占用，或本次是 managed 模式
//   - ErrWorkdirBusy：已有非终态任务占着这个目录，错误文本点名占用者与两条出路
//     （server 层据此给 409，与「工作区不干净」同为状态冲突）
//   - 其他错误：查询任务表失败
//
// 注意：
//   - 查询失败按拒发处理：放行的代价是两个 executor 抢同一棵工作树、互相切走
//     HEAD 且全程无报错，比多拒一次派发严重得多
//   - 只报第一个占用者：报文是给人看的行动指引，列出全部只会让它更难读；
//     日志里带了 holders 总数
//   - git 自己已经挡住了分支级冲突（worktree add 遇到已被检出的分支会失败），
//     这道守卫补的是唯一的洞：被共享的主工作树
func (m *Manager) guardWorkdirBusy(workDir string) error {
	if workDir == "" {
		m.log.Info("工作目录占用检查跳过（managed 模式，每任务一棵新树）")
		return nil
	}
	busy, err := m.st.ActiveTasksByWorkDir(workDir)
	if err != nil {
		m.log.Error("查询工作目录占用失败，保守拒发", "workdir", workDir, "cause", err)
		return fmt.Errorf("查询工作目录占用: %w", err)
	}
	if len(busy) == 0 {
		m.log.Info("工作目录占用检查通过", "workdir", workDir)
		return nil
	}
	holder := busy[0]
	m.log.Warn("dispatch 被拒：目标工作目录已被活跃任务占用", "workdir", workDir,
		"holder", holder.ID, "holder_state", holder.State, "holders", len(busy))
	return fmt.Errorf("%w: %s 正被任务 %s（%s, %s）占用；先 handoff done/stop 它，或改用 --new-worktree 在独立工作树上开工",
		ErrWorkdirBusy, workDir, holder.ID, holder.Name, holder.State)
}

// compensateWorkspace 在 dispatch 后续步骤失败时复原已准备好的工作区。
//
// why：PrepareWorkspace 成功意味着磁盘上已经有了工作树、且分支已经建好/切好。
// 若随后 executor 接管前的任何步骤失败（MkdirAll/WriteFile/CreateTask/
// SetTaskField/adapter.Start），任务要么没落库、要么落 failed——两者都没有 done
// 清理路径（done 只认 waiting_review），痕迹会永久留在用户的仓库里：managed
// 模式留下孤儿 worktree 与挡路的空分支（同名重试直接撞 already exists，B39），
// 非 managed 模式更直接——用户的工作树就停在那个空分支上。
//
// 参数：
//   - ctx: 控制补偿期间的 git 调用
//   - taskID: 用于在清理失败时给出可重试的 handoff reclaim 命令
//   - repo: 主仓库路径
//   - ws: PrepareWorkspace 的产出
//
// 注意：
//   - 由 Dispatch 的 defer 统一调用，且只在 executor 未接管时（见 executorStarted
//     注释）；executor 接管后删工作树是把运行中的任务脚下抽空
//   - 全程只记日志，**不覆盖也不替换原始派发错误**——协调者要看的是任务为什么
//     没派出去，补偿成败是次要信息
//   - 三条 fail-safe：worktree 删除失败 / 切回原 ref 失败 / 分支尖端对不上，
//     任一命中都保留现场不再往下做。宁可留残留，不可误删
func (m *Manager) compensateWorkspace(ctx context.Context, taskID string, repo string, ws Workspace) {
	// 空值守卫：现有调用点把 defer 注册在 PrepareWorkspace 成功之后，理论上到不了
	// 这里，但补偿函数本身不该依赖调用点的注册位置
	if ws.WorkDir == "" {
		return
	}
	m.log.Warn("dispatch 后续失败，补偿复原工作区", "repo", repo, "workdir", ws.WorkDir,
		"managed", ws.Managed, "branch", ws.Branch, "prev_ref", ws.PrevRef)

	if ws.Managed {
		if err := RemoveManagedWorktree(ctx, repo, ws.WorkDir); err != nil {
			// 工作树还在，分支被它 checkout 着，git 也会拒绝删除；且失败现场要留给人排查
			m.log.Error("补偿清理 managed worktree 失败，保留分支待查",
				"repo", repo, "workdir", ws.WorkDir, "branch", ws.Branch,
				"retry", "handoff reclaim "+shortTaskID(taskID), "cause", err)
			return
		}
	} else {
		if ws.PrevRef == "" {
			m.log.Warn("补偿无法复原：未记录原 ref，工作树仍停在任务分支上",
				"workdir", ws.WorkDir, "branch", ws.Branch,
				"manual", "git -C "+ws.WorkDir+" checkout <你原来的分支>")
			return
		}
		if _, stderr, err := gitRun(ctx, ws.WorkDir, "checkout", ws.PrevRef); err != nil {
			m.log.Error("补偿切回原 ref 失败，工作树仍停在任务分支上",
				"workdir", ws.WorkDir, "prev_ref", ws.PrevRef,
				"stderr", truncateRunes(stderr, 300), "cause", err)
			return
		}
		m.log.Info("补偿已切回原 ref", "workdir", ws.WorkDir, "prev_ref", ws.PrevRef)
	}
	m.deleteCreatedBranch(ctx, repo, ws)
}

// branchAction 是补偿路径对「本次分支」的处置决定。
// 每个取值对应一条独立规则，便于表驱动测试逐条钉住——这正是把判断从
// deleteCreatedBranch 里拆出来的目的。
type branchAction int

const (
	branchDelete         branchAction = iota // 确认是本次新建且自创建以来零提交，可删
	branchKeepNotOurs                        // 不是本次新建的，是用户自己的分支
	branchKeepTipUnknown                     // 尖端取不到，无从复核
	branchKeepTipMoved                       // 尖端与创建时不符，疑似已有提交
)

// decideBranchAction 判定补偿路径是否可以删除该分支。纯函数：不调 git、不打日志、
// 不碰任何状态，故可以被表驱动测试穷举。
//
// 参数：
//   - recordedTip: PrepareWorkspace 建分支时记下的尖端；空串 = 分支不是本次新建的
//   - currentTip:  当前尖端；tipErr 非 nil 时其值无意义
//   - tipErr:      取当前尖端时的错误
//
// 返回：四种处置之一；只有 branchDelete 允许调用方执行删除。
//
// 注意：
//   - 判定顺序是本函数的全部要点。`recordedTip == ""` 必须排在任何拿 currentTip
//     做的比较之前。旧实现里这条规则靠「碰巧写在前面」生效，一旦有人认为两道闸
//     重复而删掉它，悬空 symref 场景下 branchTip 失败塌缩出的空串会与 recordedTip
//     的空串相等，从而放行删除，毁掉用户自己的分支（该场景已实测可达，见
//     docs/superpowers/specs/2026-08-10-compensation-branch-decision-design.md §2.1）
//   - 取不到尖端一律保留而非删除：删分支不可逆，宁可留残留也不能删错
func decideBranchAction(recordedTip, currentTip string, tipErr error) branchAction {
	if recordedTip == "" {
		return branchKeepNotOurs
	}
	if tipErr != nil {
		return branchKeepTipUnknown
	}
	if currentTip != recordedTip {
		return branchKeepTipMoved
	}
	return branchDelete
}

// deleteCreatedBranch 删除本次 dispatch 新建的分支（补偿路径专用）。
//
// why 这件事不放进 RemoveManagedWorktree：那个函数服务的是 Done/Stop 归档场景，
// 「只删工作树不删分支」在那里完全正确——分支上是任务成果。补偿场景的要求正好
// 相反：分支是几秒前刚建的，executorStarted 守卫保证零提交，留着只会挡路。
// 同一个函数满足不了相反的两组要求，所以在补偿侧单独承担。
//
// 参数：ctx 控制 git 调用；repo 主仓库路径；ws 为 PrepareWorkspace 的产出
//
// 注意：
//   - 调用前必须已经确保分支不再被任何工作树 checkout（managed 已删树 /
//     非 managed 已切回原 ref），否则 git 会拒绝
//   - 删不删的规则全部在 decideBranchAction 里（含「NewBranchTip 为空 = 不是
//     本次新建的，一律不动」这一条），本函数只负责取数据、按决定打日志、执行 git
func (m *Manager) deleteCreatedBranch(ctx context.Context, repo string, ws Workspace) {
	cur, tipErr := branchTip(ctx, repo, ws.Branch)
	switch decideBranchAction(ws.NewBranchTip, cur, tipErr) {
	case branchKeepNotOurs:
		// 不是本次新建的，静默保留：每次 --branch <已存在分支> 的派发失败都会
		// 走到这里，是正常出口，打 WARN 只会变成噪音
		return
	case branchKeepTipUnknown:
		m.log.Warn("取分支尖端失败，无从复核，保留待查",
			"repo", repo, "branch", ws.Branch, "expect", ws.NewBranchTip, "cause", tipErr)
		return
	case branchKeepTipMoved:
		m.log.Warn("分支尖端与创建时不符，疑似已有提交，保留待查",
			"repo", repo, "branch", ws.Branch, "expect", ws.NewBranchTip, "actual", cur)
		return
	}
	m.log.Info("补偿删除本次新建的分支", "repo", repo, "branch", ws.Branch, "tip", ws.NewBranchTip)
	// 用 -D 而非 -d：分支起点可能领先仓库当前 HEAD，-d 会因「未合并」误拒；
	// 而「自创建以来零提交」已由上面的尖端复核实证，-D 在这里是确定性而非暴力
	if _, stderr, err := gitRun(ctx, repo, "branch", "-D", ws.Branch); err != nil {
		m.log.Error("补偿删除分支失败", "repo", repo, "branch", ws.Branch,
			"stderr", truncateRunes(stderr, 300), "cause", err)
		return
	}
	m.log.Info("补偿删除分支完成", "repo", repo, "branch", ws.Branch)
}

// Continue 向任务续发修改指令：要求任务处于 waiting_review，先回迁 running 再
// 经 Adapter.Send 原样透传指令（同一会话续接，上下文完整保留）。
//
// 返回：
//   - 任务不存在返回 store.ErrNotFound；状态不允许续接返回 store.ErrBadTransit
//   - Send 失败时任务回迁 waiting_review（协调者可重试指令），并返回该错误
//
// 注意：
//   - Send 撞 ErrTaskNotRunning 时走恢复阶梯（resumeForContinue）：executor
//     进程已死但会话数据在盘上，冷恢复续上原会话再重试 Send 一次；阶梯全走完
//     仍不可恢复则回迁 waiting_review，错误带 Outcome.Note 说明原因
func (m *Manager) Continue(ctx context.Context, taskID, instructions string) (err error) {
	m.log.Info("continue 进入", "task", taskID)
	defer func() {
		if err != nil {
			m.log.Error("continue 失败", "task", taskID, "cause", err)
		} else {
			m.log.Info("continue 完成", "task", taskID)
		}
	}()

	cur, err := m.st.GetTask(taskID)
	if err != nil {
		return err
	}
	if cur.State != proto.TaskStateWaitingReview {
		m.log.Warn("continue 状态不允许", "task", taskID, "state", cur.State)
		return fmt.Errorf("任务 %s 状态 %s 不允许续接（需 waiting_review）: %w", taskID, cur.State, store.ErrBadTransit)
	}
	if err := m.transit(taskID, proto.TaskStateRunning, "continue"); err != nil {
		return err
	}
	// 只有 taskID：经 adapterFor 解析该任务实际使用的 adapter（executor 已不在
	// 按 ErrTaskNotRunning 同语义处置——Send 由 executor 实现方自身包装该哨兵）
	ad, err := m.adapterFor(taskID)
	if err != nil {
		// 执行者无法路由（如任务记录损坏/缺省执行者未注册）：退回 waiting_review，
		// 协调者可见原因后可处置，不让任务死在 running
		m.transitBestEffort(taskID, proto.TaskStateWaitingReview, "continue 路由执行者失败回迁")
		return fmt.Errorf("解析任务 %s 执行者: %w", taskID, err)
	}
	if err := ad.Send(ctx, taskID, instructions); err != nil {
		if !errors.Is(err, executor.ErrTaskNotRunning) {
			// executor 还在，只是这次没打通：保持原语义，回迁让协调者可重试
			m.log.Error("续发指令失败", "task", taskID, "cause", err)
			m.transitBestEffort(taskID, proto.TaskStateWaitingReview, "continue 发送失败回迁")
			return fmt.Errorf("续发指令: %w", err)
		}
		m.log.Warn("续发指令时 executor 已不在，进入恢复阶梯", "task", taskID, "cause", err)
		if rerr := m.resumeForContinue(ctx, taskID, ad); rerr != nil {
			m.transitBestEffort(taskID, proto.TaskStateWaitingReview, "continue 恢复失败回迁")
			return rerr
		}
		// 重试只做一次：重试的前提是「刚刚成功建立了运行态」，这个前提一次就够
		// 验证。循环重试只会在 executor 反复启动失败时放大伤害
		if err := ad.Send(ctx, taskID, instructions); err != nil {
			m.log.Error("恢复后重试续发仍失败", "task", taskID, "cause", err)
			m.transitBestEffort(taskID, proto.TaskStateWaitingReview, "continue 恢复后发送失败回迁")
			return fmt.Errorf("恢复后续发指令: %w", err)
		}
		m.log.Info("恢复后续发指令成功，重启中介循环", "task", taskID)
		go m.mediate(taskID)
	}
	return nil
}

// resumeForContinue 是 continue 撞上「executor 已不在」时的恢复阶梯（spec §5.4）。
//
// 与启动恢复的关键差别是 Cold=true：协调者手里正好有一条指令要送，把会话拉起来
// 立刻有用；而 agentd 启动时冷恢复等于凭空拉起一堆没人跟它说话的 executor。
//
// 返回：
//   - nil: 已拿到可用运行态，调用方可以重试 Send
//   - 非 nil: 不可恢复，错误里带 Outcome.Note（server 映射 409 时回显给协调者）
//
// 注意：
//   - Mode != reattach 时必须产出 progress 事件：冷恢复换了进程、fresh 断了
//     上下文，都是协调者需要知道的事实（fresh 直接决定下一条指令要不要重述背景）
func (m *Manager) resumeForContinue(ctx context.Context, taskID string, ad executor.Adapter) error {
	task, err := m.st.GetTask(taskID)
	if err != nil {
		return err
	}
	r, ok := ad.(restorer)
	if !ok {
		return fmt.Errorf("任务 %s 的执行者不支持恢复，请重新派发: %w", taskID, executor.ErrTaskNotRunning)
	}
	execName := task.Executor
	if execName == "" {
		execName = m.cfg.Executor.Default
	}
	envKVs, eerr := m.env.For(execName)
	if eerr != nil {
		m.log.Warn("恢复解析 env 失败，按空 env 继续", "task", taskID, "cause", eerr)
	}
	discBlock, derr := m.discipline.For(execName)
	if derr != nil {
		m.log.Warn("恢复时纪律块读取失败，本次不注入", "task", taskID, "cause", derr)
	}
	m.log.Info("进入冷恢复", "task", taskID, "executor", execName, "session", task.ExecutorSession)
	out, err := r.Resume(executor.ResumeReq{
		TaskID: taskID, TaskDir: filepath.Join(m.cfg.DataDir, "tasks", taskID),
		RepoPath: task.Workdir(), SessionID: task.ExecutorSession,
		Env: envKVs, Discipline: discBlock.Text, Model: task.Model,
		MarkRoot: prochost.ResolveMarkRoot(task.Workdir(), task.WorktreeManaged), Cold: true,
	})
	if err != nil {
		m.log.Error("恢复失败", "task", taskID, "cause", err)
		return fmt.Errorf("恢复任务 %s 执行: %w", taskID, err)
	}
	m.log.Info("恢复结果", "task", taskID, "alive", out.Alive, "mode", out.Mode,
		"session", out.SessionID, "note", out.Note)
	if !out.Alive {
		note := out.Note
		if note == "" {
			note = "executor 运行态已丢失且无法重建"
		}
		return fmt.Errorf("任务 %s 无法恢复：%s", taskID, note)
	}
	if out.SessionID != "" && out.SessionID != task.ExecutorSession {
		if serr := m.st.SetTaskField(taskID, "executor_session", out.SessionID); serr != nil {
			m.log.Warn("落库新 executor_session 失败", "task", taskID,
				"session", out.SessionID, "cause", serr)
		}
	}
	// 重连（executor 一直活着）对协调者是无感事件，不打扰；换了进程或断了上下文才播报
	if out.Mode == executor.ResumeModeReattach {
		return nil
	}
	text := fmt.Sprintf("executor 进程已不在，已重启并从磁盘载入原会话 %s，上下文完整", out.SessionID)
	if out.Mode == executor.ResumeModeFresh {
		text = fmt.Sprintf("原会话 %s 已不可载入，已新开会话 %s；上下文从本条指令开始，必要时请在指令中重述背景",
			task.ExecutorSession, out.SessionID)
	}
	evt, aerr := m.st.AppendEvent(taskID, proto.EventTypeProgress, progressPayload{Text: text})
	if aerr != nil {
		m.log.Error("追加恢复播报事件失败", "task", taskID, "cause", aerr)
		return nil // 事件没落住不影响续接本身
	}
	m.hub.Publish(evt)
	return nil
}

// Done 归档任务：要求任务处于 waiting_review，迁移 completed 后调用 Adapter.Stop
// 回收 executor 侧资源，并清理 agentd 管理的 worktree。
//
// 返回：
//   - 任务不存在返回 store.ErrNotFound；状态不允许归档返回 store.ErrBadTransit
//
// 参数：
//   - note: 归档说明（handoff done --note）；空串表示未留说明，此时仍照常归档
//     并发布 archived 事件，只不落说明
//
// 注意：
//   - Stop 失败仅打 Error 日志不影响归档：任务已完成，executor 残留交给人工兜底
//   - 顺序是「先落说明、再迁移状态」：写失败时任务仍在 waiting_review，协调者可
//     原样重试；反过来先迁移就会留下「已归档但说明丢了」且不可重试的状态——done
//     对已 completed 的任务返回 409，协调者补不回来
func (m *Manager) Done(ctx context.Context, taskID, note string) (err error) {
	// 认领终态清扫：本路径末尾自己调 sweepAfterStop（带有界重试、且早于 worktree
	// 删除），transit 的兜底清扫据此跳过。见 sweepOwned 字段注释。
	m.noteSweepOwned(taskID)
	m.log.Info("done 进入", "task", taskID)
	defer func() {
		if err != nil {
			m.log.Error("done 失败", "task", taskID, "cause", err)
		} else {
			m.log.Info("done 完成", "task", taskID)
		}
	}()

	cur, err := m.st.GetTask(taskID)
	if err != nil {
		return err
	}
	if cur.State != proto.TaskStateWaitingReview {
		m.log.Warn("done 状态不允许", "task", taskID, "state", cur.State)
		return fmt.Errorf("任务 %s 状态 %s 不允许归档（需 waiting_review）: %w", taskID, cur.State, store.ErrBadTransit)
	}
	// 先落说明再迁移状态：写失败时任务仍在 waiting_review，协调者可原样重试；
	// 反过来先迁移就会留下「已归档但说明丢了」且不可重试的状态——done 对
	// 已 completed 的任务返回 409，协调者补不回来
	if note != "" {
		if err := m.st.SetTaskField(taskID, "done_note", note); err != nil {
			m.log.Error("写入归档说明失败", "task", taskID, "note_bytes", len(note), "cause", err)
			return fmt.Errorf("写入归档说明: %w", err)
		}
		m.log.Info("归档说明已落库", "task", taskID, "note_bytes", len(note))
	}
	if err := m.transit(taskID, proto.TaskStateCompleted, "done"); err != nil {
		return err
	}
	// 归档事件：必须在 hub.CloseTask 之前发布，否则订阅者（wait --follow）一条
	// 都收不到——而事件仍会入库，症状是等待方永远等不到归档，极难归因（B68 §4.2）。
	//
	// 失败只打日志不阻塞：状态已经迁移完了，此时返回错误也回不去，与本函数里
	// worktree 清理失败的处置保持一致
	if evt, aerr := m.st.AppendEvent(taskID, proto.EventTypeArchived,
		proto.ArchivedPayload{Note: note}); aerr != nil {
		m.log.Error("追加归档事件失败", "task", taskID, "cause", aerr)
	} else {
		m.hub.Publish(evt)
		m.log.Info("归档事件已发布", "task", taskID, "seq", evt.Seq, "has_note", note != "")
	}
	// 任务归档：清理审批链运行时状态，防内存 map 随归档任务无界增长（P2-5）
	m.clearApproverState(taskID)
	// 归档对事件流是无声的（transit 只改状态、不追加事件），跟随中的 wait --follow
	// 无从得知「没有下文了」。关掉订阅，让 WS 以正常关闭码收尾
	if n := m.hub.CloseTask(taskID); n > 0 {
		m.log.Info("done 关闭事件订阅", "task", taskID, "closed", n)
	}
	// done 只持有 taskID：经 adapterFor 解析该任务实际使用的 adapter；解析失败
	// 仅 Error 日志不影响归档（任务已完成，executor 残留交给人工兜底，见 doc 注意）
	ad, err := m.adapterFor(taskID)
	if err != nil {
		m.log.Error("解析任务执行者失败", "task", taskID, "cause", err)
	} else {
		m.stopExecutor(taskID, ad)
	}
	// 终态清扫（B103）：Kill 只够着 shim 那个进程组，executor 的 Bash 工具
	// setsid 出去的后代（`cmd &` 这类）不在组内——不在这里扫，它们会在 launchd
	// 名下一直活到自然退出。必须在 worktree 清理之前：还活着的进程把 cwd 钉在
	// 工作树里，会让 git worktree remove 失败
	m.sweepAfterStop(taskID)
	// worktree 清理（Stop 之后、err 已定型不覆盖）：agentd 管理的 worktree 随任务
	// 完成删除，释放磁盘并防止「每个任务一个残留目录」的无界堆积。
	//
	// 为什么只删 managed：用户自带 worktree（Managed=false）是协调者自己的资产，
	// agentd 无权删别人的工作树；为什么失败只降级不阻塞归档：任务已审核通过，
	// 残树是运维问题不是任务问题——留一条带原因的 progress 事件提示人工处理
	if cur.WorktreeManaged && cur.WorkDir != "" {
		m.log.Info("done 清理 managed worktree", "task", taskID, "workdir", cur.WorkDir)
		if werr := RemoveManagedWorktree(ctx, cur.RepoPath, cur.WorkDir); werr != nil {
			m.log.Error("清理 managed worktree 失败", "task", taskID, "workdir", cur.WorkDir, "cause", werr)
			if evt, aerr := m.st.AppendEvent(taskID, proto.EventTypeProgress, progressPayload{
				Text: worktreeCleanupHint(taskID, werr),
			}); aerr != nil {
				m.log.Error("追加 worktree 清理失败事件失败", "task", taskID, "cause", aerr)
			} else {
				m.hub.Publish(evt)
			}
		} else {
			m.log.Info("managed worktree 已清理", "task", taskID, "workdir", cur.WorkDir)
		}
	}
	return nil
}

// Stop 主动中止一个任务：停 executor、落 failed 并唤醒协调者；挂起工单的作废
// 交由终态迁移的收口完成（B63），不在此处单独做。
//
// 参数：
//   - ctx: 上层上下文（HTTP 请求）
//   - taskID: 待中止的任务
//
// 返回：
//   - worktreeRemoved: 本次是否实际删除了 managed worktree。true=agentd 建的
//     worktree 已删除；false=用户自带 worktree / 原地模式（没删），或 managed
//     worktree 清理失败（工作树仍在）。CLI 据此打印与行为一致的提示，不猜
//   - store.ErrNotFound: 任务不存在
//   - store.ErrBadTransit: 任务已是终态（completed/failed），无可中止
//   - 其余：落库失败
//
// 注意：
//   - 复用 failed 终态而不新增 aborted：状态机零改动，且 failed→running 已允许，
//     中止后仍可重新派发。「人为中止」与「真失败」的区分靠 failed 事件的
//     fail_reason 文本，不靠状态
//   - 不删任务分支：那是协调者的工作成果，stop 只让它停下（审阅/回滚仍可切回分支）
//   - 删除 agentd 管理的 worktree（Managed=true）：被 stop 的任务落 failed，没有
//     done 的 waiting_review 清理路径，不删就永久残留；清理失败只降级为警告事件，
//     不阻断 stop（此时 worktreeRemoved=false，提示如实反映工作树仍在）
//   - adapter.Stop 失败只 Warn 不中断：目的是让任务离开活跃态，executor 残留
//     由执行者进程兜底，不能因为「停不掉进程」就让任务永远卡在 running
func (m *Manager) Stop(ctx context.Context, taskID string) (worktreeRemoved bool, err error) {
	// 认领终态清扫，理由同 Done。见 sweepOwned 字段注释。
	m.noteSweepOwned(taskID)
	m.log.Info("stop 进入", "task", taskID)
	defer func() {
		if err != nil {
			m.log.Error("stop 失败", "task", taskID, "cause", err)
		} else {
			m.log.Info("stop 完成", "task", taskID, "worktree_removed", worktreeRemoved)
		}
	}()

	cur, err := m.st.GetTask(taskID)
	if err != nil {
		return false, err
	}
	if cur.State.IsTerminal() {
		m.log.Warn("stop 状态不允许", "task", taskID, "state", cur.State)
		return false, fmt.Errorf("任务 %s 已是终态 %s，无可中止: %w", taskID, cur.State, store.ErrBadTransit)
	}

	ad, aerr := m.adapterFor(taskID)
	if aerr != nil {
		m.log.Error("解析任务执行者失败", "task", taskID, "cause", aerr)
	} else {
		// Stop 是协调者主动敲的：executor 杀不掉也要把任务落 failed 并作废工单
		// ——人已经决定不要这个任务了。与 ForceReclaim 相反（那条是 watchdog
		// 自动触发，没收掉就不能宣布收掉，见 forcereclaim.go）。
		_ = m.stopExecutor(taskID, ad)
	}

	// 终态清扫（B103）：stop 的语义是「别跑了」，那就该包括 Bash 工具 setsid
	// 出去的后代——它们不在 shim 的进程组里，Kill 够不着
	m.sweepAfterStop(taskID)

	// 挂起工单的作废交由 transit 的终态收口统一完成（B63）——在这里再做一遍会
	// 抢在收口之前把单清空，导致 stop 路径永远拿不到 tickets_voided 审计事件。

	// Stop 是协调者主动中止：无 git 实况可带（不是回合收尾）
	//
	// 先迁状态、后追加事件（B97）：failed 事件一落库就可被 WS 重放读到，状态必须
	// 先就位，否则协调者在一个仍 running 的任务上看到 failed 事件会操作它、却被
	// 状态机拒。反转的代价是失败形态从「状态错」变成「状态对、事件缺」：transit
	// 失败时不追加事件，任务停在旧状态可重试；崩在两步之间留下的是「failed 但缺
	// 一条 failed 事件」，show 出来仍可裁决——旧形态「事件说终结了、状态还是
	// running」只会让协调者干等到 2h 看门狗（handleResult 函数头的同一条理由）。
	if err := m.transit(taskID, proto.TaskStateFailed, "stop"); err != nil {
		return false, err
	}
	evt, err := m.st.AppendEvent(taskID, proto.EventTypeFailed, newFailedPayload("协调者主动中止（handoff stop）", "", ""))
	if err != nil {
		return false, fmt.Errorf("追加中止事件: %w", err)
	}
	// 审批链运行时状态随任务终结清理，防内存 map 无界增长（与 Done 同款）
	m.clearApproverState(taskID)
	// worktree 清理（P2-2 修复）：被 stop 的任务落 failed，而 failed 没有 done 的
	// waiting_review 门禁清理路径——不在这里删，managed worktree 就永久残留、
	// 任务永远归档不了。分支必须保留（stop 不丢工作，协调者可切回分支审阅/回滚）。
	//
	// 为什么只删 managed：用户自带 worktree（Managed=false）是协调者自己的资产，
	// agentd 无权删别人的工作树；为什么失败只降级不阻断 stop：中止已经达成
	// （任务落 failed、事件已追加），残树是运维问题不是任务问题——留一条带原因的
	// progress 事件提示人工处理，与 Done 的清理失败降级同款
	if cur.WorktreeManaged && cur.WorkDir != "" {
		m.log.Info("stop 清理 managed worktree", "task", taskID, "workdir", cur.WorkDir)
		if werr := RemoveManagedWorktree(ctx, cur.RepoPath, cur.WorkDir); werr != nil {
			m.log.Error("stop 清理 managed worktree 失败", "task", taskID, "workdir", cur.WorkDir, "cause", werr)
			if evt, aerr := m.st.AppendEvent(taskID, proto.EventTypeProgress, progressPayload{
				Text: worktreeCleanupHint(taskID, werr),
			}); aerr != nil {
				m.log.Error("追加 worktree 清理失败事件失败", "task", taskID, "cause", aerr)
			} else {
				m.hub.Publish(evt)
			}
		} else {
			worktreeRemoved = true
			m.log.Info("stop managed worktree 已清理", "task", taskID, "workdir", cur.WorkDir)
		}
	}
	m.hub.Publish(evt)
	return worktreeRemoved, nil
}

// unaryAPITimeout 是 executor 侧一次一元调用（建会话/发 prompt/权限应答）的等待上限。
//
// 为什么 30s：与 opencode 客户端的一元超时同值（客户端 Timeout 已兜底），这里再
// 包一层 ctx 截止时间是双保险——未来 executor 若无客户端级超时，管理层的截止时间
// 仍保证「协调者 reply 回程不会永久挂起」。与等待阶段（WaitAnswer，人工应答时长
// 无上限）无关，见 unaryCtx。
const unaryAPITimeout = 30 * time.Second

// unaryCtx 为一次 executor 一元调用派生带截止时间的 ctx。
//
// 为什么派生子 ctx 而不直接把 parent 传下去：等待阶段（WaitAnswer 等人工应答）
// 时长无上限，截止时间必须只包住调用本身、不能缩短等待窗口——parent 在等待期
// 结束后可能已接近/超过 30s（协调者思考了 5 分钟才回复），直接复用会把本应成功
// 的调用掐死在截止时间上。
func unaryCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, unaryAPITimeout)
}

// RelayAnswer 在 reply 回程找不到等待者时，把已落库的协调者应答直接回传给
// executor，自愈「agentd 重启后等待 goroutine 消亡 → 回答丢失 → executor 永远阻塞」。
//
// 场景：agentd 重启时 waitPermission/waitQuestion goroutine 随进程消亡，且 /event
// 不重放历史的话，协调者的 reply 在 hub 里找不到等待者；若应答就此丢弃，任务状态
// 被 resumeIfIdle 回迁 running，executor 却永远等不到权限裁决——工单已答、二次
// reply 404、done 409，不可恢复。本方法读取工单把应答按既有翻译规则直接送达 executor。
//
// 参数：
//   - taskID: 工单所属任务 ID（reply 路由的路径参数）
//   - ticketID: 已回答的工单 ID（AnswerTicket 已落库，此处只负责回传）
//   - answer: 协调者应答原文（与落库值一致）
//
// 返回：
//   - 工单不存在/不属于该任务/类型不识别返回错误；adapter 回传失败返回错误
//
// 规则（与 waitPermission/waitQuestion 的翻译规则完全一致）：
//   - kind=gate：answer trim 后为 "allow" → RespondPermission("once")，其余一律 "reject"
//   - kind=ask：answer 原文原样经 Send 透传
func (m *Manager) RelayAnswer(taskID, ticketID, answer string) error {
	tk, err := m.st.GetTicket(ticketID)
	if err != nil {
		return fmt.Errorf("读取工单 %s: %w", ticketID, err)
	}
	if tk.TaskID != taskID {
		return fmt.Errorf("工单 %s 不属于任务 %s", ticketID, taskID)
	}
	var req ticketRequest
	if err := json.Unmarshal(tk.Request, &req); err != nil {
		return fmt.Errorf("解析工单 %s 请求体: %w", ticketID, err)
	}
	switch req.Kind {
	case "gate":
		decision, reason := gateDecision(answer)
		// ticket id 已按任务命名空间化（taskID:permID，见 handlePermission 的 why），
		// 而 adapter 契约要求裸 PermissionID：剥掉 taskID 前缀还原。invariant：
		// gate 工单 id 恒由 handlePermission 以 taskID+":"+permID 生成，
		// TrimPrefix 精确还原（permID 自身含 ":" 也不影响前缀剥离）
		permID := strings.TrimPrefix(ticketID, taskID+":")
		m.log.Info("reply 无等待者，自愈中继权限应答", "task", taskID,
			"ticket", ticketID, "perm", permID, "decision", decision)
		// 不用 context.Background() 直传：一元调用必须有界（unaryCtx 的 why），
		// 半死的 executor 在 unaryAPITimeout 内必然失败，reply 回程不永久挂起
		actx, acancel := unaryCtx(context.Background())
		defer acancel()
		ad, err := m.adapterFor(taskID)
		if err != nil {
			return fmt.Errorf("解析任务 %s 执行者: %w", taskID, err)
		}
		if err := ad.RespondPermission(actx, taskID, permID, decision, reason); err != nil {
			return fmt.Errorf("中继权限应答: %w", err)
		}
		m.noteDenyGuidanceUnlessInBand(taskID, permID, reason)
		m.markDelivered(taskID, ticketID)
		return nil
	case "ask":
		m.log.Info("reply 无等待者，自愈中继提问回答", "task", taskID, "ticket", ticketID)
		actx, acancel := unaryCtx(context.Background())
		defer acancel()
		ad, err := m.adapterFor(taskID)
		if err != nil {
			return fmt.Errorf("解析任务 %s 执行者: %w", taskID, err)
		}
		if err := ad.Send(actx, taskID, answer); err != nil {
			return fmt.Errorf("中继提问回答: %w", err)
		}
		m.markDelivered(taskID, ticketID)
		return nil
	default:
		return fmt.Errorf("工单 %s 类型 %q 不支持中继", ticketID, req.Kind)
	}
}

// mediate 是单任务的事件中介循环（每任务一个 goroutine，由 Dispatch 启动）：
// 消费 Adapter.Events 直到通道关闭（Stop），每类事件交给对应 handler。
//
// 任务级 ctx 与取消时机（P1-2）：taskCtx 供 waitPermission/waitQuestion 的应答
// 等待共用，中介循环退出（adapter 事件通道关闭 = 执行终结：Done/Stop/serve 死亡）
// 时 defer cancel 取消全部在等应答的 waiter——否则「协调者永不回答 + 执行终结」
// 的组合会让 wait goroutine 用 context.Background() 永久挂死。
//
// 为什么不在 result 到达时取消：result → waiting_review 后任务仍活（协调者可
// continue/回答挂起工单），回答晚于 result 到达是合法流程（应答后 executor 被
// 唤醒续跑，见 TestTransitToReviewTwoHopFromWaitingAnswer）——只有「事件通道
// 关闭」这一执行终结信号才是取消时机。
func (m *Manager) mediate(taskID string) {
	m.log.Info("中介循环启动", "task", taskID)
	taskCtx, cancelTask := context.WithCancel(context.Background())
	defer cancelTask()
	ad, err := m.adapterFor(taskID)
	if err != nil {
		// 任务执行者无法路由（缺省执行者未注册/记录损坏）：中介循环无从消费
		// 事件，按 executor 已不在处置——转交协调者裁决，不静默退出
		m.log.Error("中介循环无法路由执行者", "task", taskID, "cause", err)
		return
	}
	events := ad.Events(taskID)
	for ev := range events {
		m.handleEvent(taskCtx, taskID, ev)
	}
	// 事件通道关闭 = executor 终结。这是「executor 已不在」最常见的到达口——
	// 三个 adapter 在进程/连接死亡时都会 closeEvents()。不在这里对账，任务会
	// 一直停在 running 直到 2h 看门狗（B21 实测：静止 1 小时无任何信号）
	if m.takeStopping(taskID) {
		m.log.Info("中介循环结束（主动停止，跳过对账）", "task", taskID)
		return
	}
	m.log.Info("中介循环结束，开始对账", "task", taskID)
	m.reconcileExecutorGone(taskID, "executor 事件流已终结（进程退出或连接断开）")
}

// handleEvent 按事件类型分发中介处理，并打每类事件的入口日志。
func (m *Manager) handleEvent(ctx context.Context, taskID string, ev executor.AdapterEvent) {
	switch ev.Type {
	case adapterEventPermission:
		m.log.Info("权限请求事件", "task", taskID, "perm", ev.PermissionID,
			"text", truncateRunes(ev.Text, 80))
		m.handlePermission(ctx, taskID, ev)
	case adapterEventQuestion:
		m.log.Info("提问事件", "task", taskID, "text", truncateRunes(ev.Text, 80))
		m.handleQuestion(ctx, taskID, ev)
	case adapterEventProgress:
		m.log.Info("进度事件", "task", taskID, "text", truncateRunes(ev.Text, 80))
		m.handleProgress(taskID, ev)
	case adapterEventResult:
		if ev.Result != nil {
			m.log.Info("执行结果事件", "task", taskID, "ok", ev.Result.OK,
				"branch", ev.Result.Branch, "commit", ev.Result.CommitHash)
		} else {
			m.log.Warn("执行结果事件缺 Result", "task", taskID)
		}
		m.handleResult(taskID, ev)
	case adapterEventUsage:
		// 不打 Info：用量事件频率高（claudecode 一个回合几百条），
		// 每条都打入口日志就是刷屏。首次落库的日志在 handleUsage 里打。
		// 两条通道各走各的：Usage 走当前占用，Spend 走累计账本，绝不交叉。
		m.handleUsage(taskID, ev)
		m.handleSpend(taskID, ev)
	default:
		m.log.Warn("未知 adapter 事件", "task", taskID, "type", ev.Type)
	}
}

// handlePermission 中介权限请求：ticket(id=taskID:permID, kind=gate) → 事件 →
// waiting_answer → goroutine 等协调者应答后回传 executor。
//
// 审批链前置的时序说明（二期新增）：approver 启用且黑名单未命中时，请求先交
// consultApprover 做廉价模型裁决——approve 全程不动状态机（任务保持 running，
// executor 收 once 后继续跑），escalate 则完整走下方「落状态→建工单→waiter→
// Publish」的原契约。因此本函数的中介主体被提取为 escalatePermission，供原路径
// 与审批者 escalate 路径共用，保证两路行为完全一致。
//
// 审批链异步化对原契约的修正（P1-1）：一期 handlePermission 在 mediate 循环内
// 同步执行，同任务的 permission/result 天然串行，「escalate 完整走原契约」的
// 前提隐含了这一点；二期起 consultApprover 是独立 goroutine（最长 60s），裁决
// 期间任务可能已被 handleResult/done 推进到终态。因此 escalatePermission 只能
// 在任务仍处 running/waiting_answer 时被调用——consultApprover 在分流前重读
// 任务快照，状态已终态则只留 approver_decision 审计事件。
//
// 顺序契约（P1-2 时序修复）：ticket/事件落库后，**先置 waiting_answer，再启动
// waiter goroutine，最后才 Publish**。严格说「先注册 waiter」不成立：waitPermission
// 的 hub 注册在 goroutine 启动后才异步发生，Publish 后 reply 仍可能先于注册到达——
// 该情形退化为「无等待者 → 自愈中继」（RelayAnswer 直接回传 executor），任务不卡死。
// 真正必须先就位的是**状态**：resumeIfIdle 读到 running 会跳过回迁，应答已交付但
// 任务随后落回 waiting_answer 且无人再答，永久卡死（探针 1/60 复现「waiting_answer
// 但 pending_tickets=0」）。状态先就位后，reply 无论命中 waiter 还是走中继，
// resumeIfIdle 必看到 waiting_answer，回迁判定一致。
func (m *Manager) handlePermission(ctx context.Context, taskID string, ev executor.AdapterEvent) {
	if ev.PermissionID == "" {
		m.log.Error("权限事件缺 PermissionID", "task", taskID)
		return
	}
	// ticket id 按任务命名空间隔离：taskID:permID（P1-6）。opencode 的权限 id
	// 按会话生成、跨任务不保证唯一，裸 permID 作 ticket id 时第二个任务的工单
	// 会被 INSERT OR IGNORE 静默吞掉——attach 显示 0 挂起项且任务永远无法应答。
	// 命名空间化后各任务工单 id 全局唯一；回传 executor 仍用裸 permID
	// （adapter 契约，见 waitPermission/RelayAnswer）
	ticketID := taskID + ":" + ev.PermissionID
	if m.isPermissionReplay(taskID, ev.PermissionID, ticketID) {
		return
	}
	// 判据前置分流（B23/B27）：结构化判据先判，三个出口对应三条既有路径。
	// AutoAllow 不建工单、不发事件、不改状态——工作区内的写入是派发的目的
	// 本身，为它唤醒任何人都是噪音。
	switch m.judgePermission(taskID, ev).Action {
	case permgate.AutoAllow:
		m.autoAllowPermission(taskID, ev)
		return
	case permgate.Consult:
		// 审批者可用且本任务未停用时才咨询；否则退化为升级人工（原行为）。
		// 已在裁决中的重放（markApproverInflight 返回 false）直接吞掉。
		if m.shouldConsultApprover(taskID) {
			if m.markApproverInflight(ticketID) {
				go m.consultApprover(ctx, taskID, ev, ticketID)
			}
			return
		}
	}
	m.escalatePermission(ctx, taskID, ev, ticketID)
}

// escalatePermission 把权限请求升级人工协调者：落状态 → 建工单 → 追加事件 →
// waiter → Publish（一期 handlePermission 的完整既有行为，顺序契约见其 doc）。
//
// 本函数同时是审批者 escalate 路径的出口——两路共用保证行为一致。
//
// 工单存权限描述全文，事件 payload 另行截断——全文是协调者裁决的依据，不能只存
// 唤醒用的摘要。
func (m *Manager) escalatePermission(ctx context.Context, taskID string, ev executor.AdapterEvent, ticketID string) {
	// 复用判定必须早于任何状态迁移（spec §3.2）：先落 waiting_answer 再放行回迁
	// running，会让任务状态凭空抖动一次，resumeIfIdle 的判定面也跟着变复杂。
	if m.reuseDecision(taskID, ev, ticketID) {
		return
	}
	// 先落状态再建工单（U-1）：协调者经 attach 读到挂起工单后会立即 reply，
	// 「工单已可见但状态还没落 waiting_answer」这段窗口里的 reply 会走完中继、
	// resumeIfIdle 读到 running 直接返回，随后 manager 才盖上 waiting_answer——
	// 任务显示「等你回答」却零挂起工单，reply/continue/done 三条路全封死。
	// 反过来「状态已落但工单还没建」是安全的：reply 找不到工单只会 404，
	// 协调者重试即可，且工单随即出现。
	m.transitBestEffort(taskID, proto.TaskStateWaitingAnswer, "permission_request")
	req, _ := json.Marshal(ticketRequest{Kind: "gate", Permission: ev.Text})
	if _, err := m.st.CreateTicket(&proto.Ticket{
		ID: ticketID, TaskID: taskID, Kind: "gate",
		Request: req, CreatedAt: time.Now().UTC(),
		Fingerprint: permFingerprintFor(ev),
	}); err != nil {
		m.log.Error("创建权限工单失败", "task", taskID, "perm", ev.PermissionID, "ticket", ticketID, "cause", err)
		// 工单没建成，waiting_answer 是虚假状态（无任何可答项），回迁 running
		m.transitBestEffort(taskID, proto.TaskStateRunning, "权限工单创建失败回滚")
		return
	}
	evt, err := m.st.AppendEvent(taskID, proto.EventTypePermissionRequest, permissionPayload{
		TicketID: ticketID, Permission: permEventText(ev.Text), Kind: "gate",
	})
	if err != nil {
		// 工单在、事件缺：不回滚工单，留给下一次重放由 isPermissionReplay 的
		// 「有工单无事件」分支自愈补发（N-4）
		m.log.Error("追加 permission_request 事件失败", "task", taskID, "ticket", ticketID, "cause", err)
		return
	}
	m.log.Info("权限升级人工协调者", "task", taskID, "ticket", ticketID,
		"perm_chars", len([]rune(ev.Text)), "event_truncated", len([]rune(ev.Text)) > permEventTextLimit)
	go m.waitPermission(ctx, taskID, ev.PermissionID, ticketID)
	m.hub.Publish(evt)
}

// judgePermission 把一次权限事件交给判据网关，返回裁决。
//
// 参数：
//   - taskID: 任务 id（用于取工作区范围）
//   - ev: 权限事件
//
// 返回：permgate.Verdict；一切无法判定的情形都返回 Escalate（fail-closed）
//
// 注意：
//   - ev.Perm 为 nil 表示 adapter 提取不出结构——看不懂的请求交给人，
//     绝不让廉价模型去猜
//   - 读任务失败时工作区范围不可知，同样升级人工：范围未知时判「路径在不在
//     范围内」是没有意义的
//   - gate 为 nil 是构造契约被违反（NewManager 文档已写明不得为 nil），
//     但这里仍兜一手：不兜的话 Judge 会在权限处理 goroutine 里空指针 panic，
//     把整个 agentd 带走——那比升级人工严重得多，也违背「fail-closed 无例外」
func (m *Manager) judgePermission(taskID string, ev executor.AdapterEvent) permgate.Verdict {
	if m.gate == nil {
		m.log.Error("判据网关未装配，fail-closed 升级人工（NewManager 的 gate 不得为 nil）",
			"task", taskID, "perm", ev.PermissionID)
		return permgate.Verdict{Action: permgate.Escalate, Reason: "判据网关未装配"}
	}
	if ev.Perm == nil {
		m.log.Warn("权限事件缺结构化载荷，fail-closed 升级人工",
			"task", taskID, "perm", ev.PermissionID,
			"text", truncateRunes(ev.Text, 120))
		return permgate.Verdict{Action: permgate.Escalate,
			Reason: "adapter 未提供结构化权限载荷"}
	}
	task, err := m.st.GetTask(taskID)
	if err != nil {
		m.log.Warn("读任务失败，工作区范围不可知，fail-closed 升级人工",
			"task", taskID, "perm", ev.PermissionID, "cause", err)
		return permgate.Verdict{Action: permgate.Escalate,
			Reason: "读任务失败，工作区范围不可知"}
	}
	scope := permgate.Scope{
		Workdir: task.Workdir(),
		TaskDir: filepath.Join(m.cfg.DataDir, "tasks", taskID),
	}
	v := m.gate.Judge(permgate.Request{
		Tool:      ev.Perm.Tool,
		Text:      ev.Text,
		Command:   ev.Perm.Command,
		Paths:     ev.Perm.Paths,
		Truncated: strings.Contains(ev.Text, executor.TruncationMarker),
	}, scope)
	switch v.Action {
	case permgate.AutoAllow:
		m.log.Debug("权限判定：自动放行", "task", taskID, "perm", ev.PermissionID,
			"tool", ev.Perm.Tool, "paths", ev.Perm.Paths, "reason", v.Reason)
	case permgate.Consult:
		m.log.Info("权限判定：交审批者", "task", taskID, "perm", ev.PermissionID,
			"tool", ev.Perm.Tool, "reason", v.Reason, "rule", v.Rule)
	default:
		lvl := escalateLogLevel(v.Rule)
		m.log.Log(context.Background(), lvl, "权限判定：升级人工",
			"task", taskID, "perm", ev.PermissionID, "tool", ev.Perm.Tool,
			"paths", ev.Perm.Paths, "workdir", scope.Workdir, "task_dir", scope.TaskDir,
			"reason", v.Reason, "rule", v.Rule)
	}
	return v
}

// escalateLogLevel 决定「权限判定：升级人工」这条日志的级别。
//
// 参数：rule 为 Verdict.Rule（黑名单命中时是规则原文，自指令时是
// permgate.RuleSelfCommand，其余情形为空）
//
// 为什么不是一律 Info：Warn 这一档留给「本该被静默通过、现在被拦下」的事件，
// 那是每次收口改动的全部价值所在，必须在日志里一眼可见。今天有两类——
// 越界写与结构缺失（Rule 为空，B27 那一批）、自指令（B115）。黑名单命中
// 走 Info，因为它改动前后都会被拦，不是新增的信号。
func escalateLogLevel(rule string) slog.Level {
	if rule == "" || rule == permgate.RuleSelfCommand {
		return slog.LevelWarn
	}
	return slog.LevelInfo
}

// autoAllowPermission 自动放行一次权限请求：不建工单、不发事件、不改状态，
// 直接把 once 回传 executor。
//
// 注意：
//   - 没有工单可失败，因此回传失败**不产 delivery_failed 事件**；最常见的
//     失败成因是订阅重放（同一权限请求被再次投递，而 executor 侧那次请求
//     早已应答完毕），按 Warn 记录即可
//   - adapterFor 失败意味着任务的运行态已经没了，executor 侧那次请求将无人
//     应答——这是 Error 级，但同样无工单可失败
func (m *Manager) autoAllowPermission(taskID string, ev executor.AdapterEvent) {
	ad, err := m.adapterFor(taskID)
	if err != nil {
		m.log.Error("自动放行：解析执行者失败，该权限请求将无人应答",
			"task", taskID, "perm", ev.PermissionID, "cause", err)
		return
	}
	actx, acancel := unaryCtx(context.Background())
	defer acancel()
	if err := ad.RespondPermission(actx, taskID, ev.PermissionID, "once", ""); err != nil {
		m.log.Warn("自动放行回传 executor 失败（多为订阅重放，请求已失效）",
			"task", taskID, "perm", ev.PermissionID, "cause", err)
		return
	}
	m.noteAutoAllowed(taskID)
}

// noteAutoAllowed 累计一次自动放行。
func (m *Manager) noteAutoAllowed(taskID string) {
	m.aaMu.Lock()
	defer m.aaMu.Unlock()
	m.aaCount[taskID]++
}

// takeAutoAllowed 取走并清空某任务的自动放行计数。
//
// 取走式而非只读：计数的意义是「这一段执行里静默放行了多少次」，汇总打完
// 就该归零，否则下一段的汇总会把上一段的数字算进去。
func (m *Manager) takeAutoAllowed(taskID string) int {
	m.aaMu.Lock()
	defer m.aaMu.Unlock()
	n := m.aaCount[taskID]
	delete(m.aaCount, taskID)
	return n
}

// shouldConsultApprover 判断本任务此刻能否走审批者裁决：审批者已启用
// 且该任务的审批链未被连续失败停用。
//
// 权限内容层面的判定（黑名单、截断标记）已迁入 internal/permgate，
// 本函数只管「审批者这条路通不通」。
func (m *Manager) shouldConsultApprover(taskID string) bool {
	if m.approver == nil {
		return false
	}
	m.apMu.Lock()
	disabled := m.apDisabled[taskID]
	m.apMu.Unlock()
	if disabled {
		m.log.Debug("审批链已停用，直接升级协调者", "task", taskID)
		return false
	}
	return true
}

// markApproverInflight 尝试登记 ticket 的审批中状态；返回 false 表示已有同
// ticket 的裁决在途（SSE 重放），本次直接吞掉不重复咨询。
func (m *Manager) markApproverInflight(ticketID string) bool {
	m.apMu.Lock()
	defer m.apMu.Unlock()
	if m.apInflight[ticketID] {
		return false
	}
	m.apInflight[ticketID] = true
	return true
}

// consultApprover 在独立 goroutine 里完成一次权限请求的审批者裁决（不阻塞
// mediate 循环）。流程：Decide → 落 approver_decision 审计事件（不 Publish）→
// 按结果分流：approve 自动放行 / escalate 升级人工协调者 / Err 计数并 fail-closed。
func (m *Manager) consultApprover(ctx context.Context, taskID string, ev executor.AdapterEvent, ticketID string) {
	defer func() {
		m.apMu.Lock()
		delete(m.apInflight, ticketID)
		m.apMu.Unlock()
	}()
	m.log.Info("审批者开始裁决", "task", taskID, "ticket", ticketID,
		"perm", ev.PermissionID, "text", truncateRunes(ev.Text, 80))
	// 任务摘要取 PlanSummary；GetTask 失败用空串——摘要是上下文不是裁决前提，
	// 不因读失败把整个裁决拖下水
	summary := ""
	if task, err := m.st.GetTask(taskID); err == nil {
		summary = task.PlanSummary
	}
	d := m.approver.Decide(ctx, ev.Text, summary)

	decision := "error"
	switch {
	case d.Approve:
		decision = "approve"
	case d.Err == nil:
		decision = "escalate"
	}
	// 只入库不 Publish：approve 路径无人可唤醒（已自动放行），escalate 路径的
	// 唤醒由紧随其后的 permission_request 完成；approver_decision 是审计记录，
	// 协调者经 show 可见
	if _, err := m.st.AppendEvent(taskID, proto.EventTypeApproverDecision, approverDecisionPayload{
		TicketID: ticketID, Permission: permEventText(ev.Text), Decision: decision,
		Reason: d.Reason, ElapsedMS: d.ElapsedMS,
	}); err != nil {
		m.log.Error("追加 approver_decision 事件失败", "task", taskID, "ticket", ticketID, "cause", err)
	}

	// 重读任务状态后分流（P1-1）：审批链异步化打破了「permission 与 mediate 循环
	// 串行」的一期前提——Decide 是最长 60s 的独立 goroutine，窗口内 executor 可能
	// 死亡（handleResult 落 waiting_review）或被 done 归档（completed）。此时若
	// 照旧建工单/唤醒/回传，会重现 U-1/U-3 专门修掉的那类矛盾形态：状态已终态却
	// 带 pending 权限工单，reply 后答案守卫被消耗、RespondPermission 对已死
	// executor 失败。因此只在任务仍在 running/waiting_answer（可继续中介）时才
	// 分流；否则仅留 audit 事件 + Warn，不建工单、不 Publish、不回传。
	cur, gerr := m.st.GetTask(taskID)
	if gerr != nil {
		m.log.Error("审批者裁决后重读任务失败，按已终结处理", "task", taskID, "cause", gerr)
		return
	}
	if cur.State != proto.TaskStateRunning && cur.State != proto.TaskStateWaitingAnswer {
		m.log.Warn("审批者裁决期间任务已离开 running/waiting_answer，回 reject 并留审计事件",
			"task", taskID, "ticket", ticketID, "decision", decision, "state", cur.State)
		// B117：不建工单/不唤醒/不消耗答案守卫这三条 P1-1 原意一字不改，
		// 但**必须应答**——不答等于让 executor 侧那条请求悬着，codex 实测悬 8.5 分钟后
		// 自行 Rejected("approval request aborted")，打断的是下一个回合。
		// 一律回 reject 而不是照裁决放行：任务此刻已在 waiting_review（语义是「等协调者」），
		// 放行等于让 executor 在无人看管时继续改工作区。命令没跑成可以 continue 重跑，
		// 回合被打死不能。
		m.respondLateDecision(taskID, ticketID, ev.PermissionID, decision, string(cur.State))
		return
	}

	if d.Err != nil {
		// fail-closed：裁决失败升级协调者，并按连续失败计数决定是否停用
		//（防对已损坏审批者命令的重试风暴，见 maxApproverFails 的 why）
		m.countApproverFail(taskID)
		m.escalatePermission(ctx, taskID, ev, ticketID)
		return
	}
	// 干净裁决（approve 或 escalate）：失败计数清零
	m.apMu.Lock()
	delete(m.apFails, taskID)
	m.apMu.Unlock()

	if d.Approve {
		m.approvePermission(taskID, ticketID, ev.PermissionID, ev.Text, permFingerprintFor(ev), d.Reason, "approver")
		return
	}
	m.escalatePermission(ctx, taskID, ev, ticketID)
}

// respondLateDecision 处置「裁决晚于回合边界」：回一个干净的 reject，并留一条
// 协调者可见事件说明那条裁决的去向。
//
// 参数：
//   - decision: 审批者原本的裁决（approve/escalate/error），只进事件不影响回传值
//   - state: 重读到的任务状态，进事件供协调者判断当时发生了什么
//
// 注意：
//   - 回传失败不改变任务状态。最常见成因就是 executor 已经不在了，
//     那正是这条路径的常态之一，不该因此把任务推向 failed
//   - 事件 Publish 而不是只落库：协调者需要知道「有一条批准空转了、
//     该 continue 重跑」，这与 deny_guidance_dropped 的可操作性理由相同
func (m *Manager) respondLateDecision(taskID, ticketID, permID, decision, state string) {
	ad, err := m.adapterFor(taskID)
	if err != nil {
		m.log.Warn("裁决晚到：任务运行态已不在，无法回传",
			"task", taskID, "ticket", ticketID, "cause", err)
	} else {
		actx, acancel := unaryCtx(context.Background())
		defer acancel()
		// reason 传空串是有意的：本次 reject 不是对操作本身的否定，只是裁决
		// 晚于回合边界。带理由的下发（turn.DenyGuidanceText）会缀上「不要重复
		// 发起同一请求」，那句话在这里是错的指导——这条恰恰是可以重来的。
		// 空串让 adapter 回退到通用句，与本路径改动前的行为逐字一致。
		if rerr := ad.RespondPermission(actx, taskID, permID, "reject", ""); rerr != nil {
			m.log.Warn("裁决晚到：回传 reject 失败（多为 executor 已退出）",
				"task", taskID, "ticket", ticketID, "cause", rerr)
		} else {
			m.log.Info("裁决晚到：已回传 reject，回合可正常收尾",
				"task", taskID, "ticket", ticketID)
		}
	}
	evt, aerr := m.st.AppendEvent(taskID, proto.EventTypeApprovalDropped,
		approvalDroppedPayload{TicketID: ticketID, Decision: decision, State: state})
	if aerr != nil {
		m.log.Error("追加 approval_dropped 事件失败", "task", taskID, "ticket", ticketID, "cause", aerr)
		return
	}
	m.hub.Publish(evt)
}

// clearApproverState 清理任务级审批链运行时状态（apFails/apDisabled/denyGuidance）。
//
// 调用点：任务终结处——Done 归档（→completed）、stop（→failed）与 handleResult
// 的回合结束（→waiting_review）。为什么必须清理（P2-5）：这两张是进程内内存
// map，任务归档后若不清，条目随任务数无界增长；且任务被续接时旧的禁用标记/
// 失败计数也不该残留（新回合从干净状态重新评估审批链）。
//
// 为什么这里一并处理 denyGuidance：拒绝原因的生命周期是「从这次拒绝到下一条
// 提问」；回合终结意味着那条提问永远不会来了。若删除时原因还挂着，说明协调者
// 说的话没机会送达——必须落一条 deny_guidance_dropped 审计事件说明去向，
// 「协调者说的话去哪了」在任何路径下都有答案（B50）。
func (m *Manager) clearApproverState(taskID string) {
	m.apMu.Lock()
	delete(m.apFails, taskID)
	delete(m.apDisabled, taskID)
	guidance, had := m.denyGuidance[taskID]
	delete(m.denyGuidance, taskID)
	m.apMu.Unlock()
	if had {
		m.log.Warn("拒绝原因未下发：回合已终结，用 continue 自己把话带上",
			"task", taskID, "reason", truncateRunes(guidance, 80))
		// Publish 而不是只落库（B91）：这条事件是可操作唤醒——审核者拿到的
		// reply 返回是 {"ok":true}，不叫醒的话他永远不知道那句 reason 空转了，
		// 唯一的补救动作（把话写进 continue）也就无从发生。progress /
		// approver_decision 不唤醒的先例不适用：那些没有审核者动作可做，这条有。
		evt, err := m.st.AppendEvent(taskID, proto.EventTypeDenyGuidanceDropped,
			denyGuidancePayload{
				Reason: guidance,
				Cause:  "回合在拒绝原因下发前终结（Done/stop/result），未送达 executor",
			})
		if err != nil {
			m.log.Error("追加 deny_guidance_dropped 事件失败", "task", taskID, "cause", err)
		} else {
			m.hub.Publish(evt)
		}
	}
}

// countApproverFail 累计一次任务级裁决失败，达到 maxApproverFails 时停用该任务
// 的审批链并留 approver_disabled 审计事件（一次）。
func (m *Manager) countApproverFail(taskID string) {
	m.apMu.Lock()
	defer m.apMu.Unlock()
	m.apFails[taskID]++
	fails := m.apFails[taskID]
	if fails >= maxApproverFails && !m.apDisabled[taskID] {
		m.apDisabled[taskID] = true
		if _, err := m.st.AppendEvent(taskID, proto.EventTypeApproverDisabled, approverDisabledPayload{
			Reason: fmt.Sprintf("连续 %d 次裁决失败，审批链已停用（后续权限直接升级人工协调者）", maxApproverFails),
		}); err != nil {
			m.log.Error("追加 approver_disabled 事件失败", "task", taskID, "cause", err)
		}
		m.log.Warn("审批者连续失败已停用", "task", taskID, "fails", fails)
	}
}

// approvePermission 审批者批准路径：幂等建工单并自动答题 → RespondPermission(once)
// → 标记送达。全程不动任务状态机——任务保持 running，executor 收 once 后继续跑。
//
// 为什么不动状态：批准不是「任务被挂起等人工」，executor 恢复执行即续跑，
// 状态机不必经过 waiting_answer（那是「有未决人工事项」的语义，此处没有）。
//
// 参数：
//   - fp: 调用方用 permFingerprintFor(ev) 算好的裁决指纹——本函数只收权限描述
//     文本串、拿不到 ev.Perm，不在函数内重新猜域；键从建单就对齐 reuseDecision，
//     审批者路径的工单才进得了复用面（B91 §7）
//   - source: 这次批准的来源，取 "approver"（廉价模型审批者实时裁决）或
//     "reuse"（命中本任务内既有人工批准自动复用，B57②）。日志里必须区分：
//     复用路径若打「审批者自动批准」会把人引向一条根本没发生的裁决链去排查。
func (m *Manager) approvePermission(taskID, ticketID, permID, permission, fp, reason, source string) {
	m.log.Info("权限自动批准", "task", taskID, "ticket", ticketID,
		"perm", permID, "source", source, "reason", truncateRunes(reason, 80))
	req, _ := json.Marshal(ticketRequest{Kind: "gate", Permission: permission})
	if _, err := m.st.CreateTicket(&proto.Ticket{
		ID: ticketID, TaskID: taskID, Kind: "gate",
		Request: req, CreatedAt: time.Now().UTC(),
		Fingerprint: fp,
	}); err != nil {
		// 工单建不起来批准就无法落审计，按裁决失败处理（fail-closed）
		m.log.Error("审批者批准：创建工单失败", "task", taskID, "ticket", ticketID, "source", source, "cause", err)
		m.countApproverFail(taskID)
		return
	}
	// 工单 answer 落**精确 "allow"**（P0-2）：gate 翻译规则是 answer 严格等于
	// "allow" 才放行，塞理由进 answer 会让审批者的批准在 resume 重投
	// （RelayAnswer）时被翻转成 reject；理由已完整落在 approver_decision 事件的
	// Reason 字段，answer 只需表达「批准」这一动作。
	if err := m.st.AnswerTicket(ticketID, "allow"); err != nil {
		m.log.Error("审批者批准：应答失败", "task", taskID, "ticket", ticketID, "source", source, "cause", err)
		m.countApproverFail(taskID)
		return
	}
	ad, err := m.adapterFor(taskID)
	if err != nil {
		// 工单已被 AnswerTicket 消耗（answer IS NULL 守卫失效），executor 仍原地
		// 阻塞等待——必须产出 delivery_failed 事件让协调者知道该 resume（P1-4），
		// 与紧邻的 RespondPermission 失败分支一致；只记 Error 会让协调者毫无感知
		m.log.Error("审批者批准：解析执行者失败", "task", taskID, "source", source, "cause", err)
		m.NoteDeliveryFailed(taskID, ticketID, err)
		return
	}
	actx, acancel := unaryCtx(context.Background())
	defer acancel()
	if err := ad.RespondPermission(actx, taskID, permID, "once", ""); err != nil {
		m.log.Error("审批者批准：回传 executor 失败", "task", taskID, "perm", permID, "source", source, "cause", err)
		m.NoteDeliveryFailed(taskID, ticketID, err)
		return
	}
	m.markDelivered(taskID, ticketID)
	m.log.Info("审批者批准已送达", "task", taskID, "ticket", ticketID, "source", source)
}

// isPermissionReplay 判定一次 permission 事件是否为「已完整中介过」的重放
// （SSE 断线重连 / agentd 重启后订阅重建都会重放同一权限请求）。
//
// 参数：permID 是 executor 侧裸权限 id（仅用于日志），ticketID 是命名空间化的工单 id。
//
// 返回：true 表示应跳过全部中介动作。
//
// 判定规则（why 这里不能只看工单是否存在）：
//   - 工单不存在 → 新请求，正常中介
//   - 工单存在且已应答 → 真重放，跳过：再动一次就是重复交付
//   - 工单存在但通知事件缺失 → **不是**重放，而是崩溃恰好落在「建工单」与
//     「追加事件」之间留下的半截状态。仅凭工单存在就跳过，会让 permission_request
//     永不产生、状态停在 running、无等待者，协调者的 wait 永远不触发（N-4）——
//     此处放行以补发事件，CreateTicket 本身幂等，不会产生第二张工单
//   - 工单存在且事件也在 → 真重放，跳过（P1-7 的幂等承诺）
func (m *Manager) isPermissionReplay(taskID, permID, ticketID string) bool {
	tk, err := m.st.GetTicket(ticketID)
	if errors.Is(err, store.ErrNotFound) {
		return false
	}
	if err != nil {
		m.log.Error("读取权限工单失败", "task", taskID, "perm", permID, "ticket", ticketID, "cause", err)
		return true // 读不到就不动，宁可少一次唤醒也不重复中介
	}
	if tk.Answer != nil {
		m.log.Debug("权限请求重放且已应答，跳过中介", "task", taskID, "perm", permID, "ticket", ticketID)
		return true
	}
	hasEvent, err := m.st.TicketHasEvent(taskID, ticketID)
	if err != nil {
		m.log.Error("查询权限工单通知事件失败", "task", taskID, "ticket", ticketID, "cause", err)
		return true
	}
	if hasEvent {
		m.log.Debug("权限请求重放，跳过中介", "task", taskID, "perm", permID, "ticket", ticketID)
		return true
	}
	m.log.Warn("权限工单存在但通知事件缺失，补发以自愈", "task", taskID, "perm", permID, "ticket", ticketID)
	return false
}

// permFingerprint 计算权限描述全文的裁决指纹（sha256 十六进制串）。
//
// 为什么取哈希而不是原文：权限描述可长达 64KB，原文不适合做索引键；
// 而复用要求的是「一字不差的同一件事」，哈希恰好表达这个语义。
func permFingerprint(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// permFingerprintFor 计算一次权限请求的裁决指纹，是所有建单/查询点的唯一入口。
//
// 域规则（B91）：
//   - Perm.Command 非空 → 命令域：sha256("cmd\x00" + command)。同一条命令被
//     opencode 以 external_directory 与 bash 两种 kind 各发一次时（双胞胎工单，
//     见 B91 spec §1.1），两次算出同一指纹，第二次得以复用首次的人工批准。
//   - Command 为空但 Paths 非空 → 路径域：sha256("paths\x00" + Tool + "\x00" +
//     排序后的 Paths)。治的是「同一个目录被每个子 agent 各问一次」——子 agent
//     前缀只加在 Text 上（opencode adapter.go 的 permission.asked 归一化处），
//     Perm 不带它，所以路径域天然忽略前缀。Tool 进指纹是硬要求，理由见函数体。
//   - 都为空（Perm 为 nil，或提取不出结构的 fail-closed 类）→ 全文域：
//     沿用 B57 的权限描述全文指纹，行为不变。
//
// 三个域各带自己的前缀做隔离，文本相同也永不相撞，杜绝「某段权限描述全文恰好
// 等于另一条命令文本」这类伪命中。
//
// 注意：写入（建单）与查询（reuseDecision）必须都走本函数——两边规则不一致
// 会让复用静默失效，那正是 B91 要修的缺陷形态。
func permFingerprintFor(ev executor.AdapterEvent) string {
	if ev.Perm != nil {
		if ev.Perm.Command != "" {
			return permFingerprint("cmd\x00" + ev.Perm.Command)
		}
		if len(ev.Perm.Paths) > 0 {
			// 拷贝后排序，不原地改 ev.Perm.Paths——该切片来自 adapter，
			// 排序它会让调用方看到的顺序被本函数悄悄改掉
			paths := append([]string(nil), ev.Perm.Paths...)
			sort.Strings(paths)
			// Tool 必须进指纹：edit 与 external_directory 对同一路径含义不同
			// （写这个文件 vs 授权越界访问这个目录），裸路径合并等于把两种
			// 授权当成一件事。NUL 作分隔符——路径不可能含 NUL，杜绝
			// ["a","b/c"] 与 ["a/b","c"] 这类拼接歧义
			return permFingerprint("paths\x00" + ev.Perm.Tool + "\x00" + strings.Join(paths, "\x00"))
		}
	}
	return permFingerprint(ev.Text)
}

// reuseDecision 检查本次权限请求是否命中本任务内既有的人工批准；命中则自动
// 放行并返回 true，调用方不得再走升级人工那套。
//
// 参数：
//   - taskID/ev/ticketID: 与 escalatePermission 同源
//
// 返回：命中并已自动放行为 true；未命中（含查询失败）为 false
//
// 注意：
//   - 查询失败按未命中处理（fail-closed 到「照常问人」）——多问一次是噪音，
//     错误地复用是安全事故，两个方向的代价不对称
//   - 只复用 allow、只在同任务内复用：见 spec §3.3/§3.4
func (m *Manager) reuseDecision(taskID string, ev executor.AdapterEvent, ticketID string) bool {
	fp := permFingerprintFor(ev)
	prior, err := m.st.FindReusableGrant(taskID, fp)
	if err != nil {
		m.log.Warn("查询可复用裁决失败，照常升级人工", "task", taskID,
			"ticket", ticketID, "fingerprint", fp[:8], "cause", err)
		return false
	}
	if prior == nil {
		m.log.Debug("无可复用裁决，升级人工", "task", taskID,
			"ticket", ticketID, "fingerprint", fp[:8])
		return false
	}
	m.log.Info("命中既有人工批准，自动放行不再叫醒协调者", "task", taskID,
		"ticket", ticketID, "prior_ticket", prior.ID, "fingerprint", fp[:8],
		"perm_chars", len([]rune(ev.Text)))
	// 只入库不 Publish：照 approver_decision 的先例——自动放行没有人需要被唤醒，
	// 但协调者经 show 必须能看到「这条是复用工单 X 的裁决放行的」
	if _, err := m.st.AppendEvent(taskID, proto.EventTypePermissionReuse, permissionReusePayload{
		TicketID: ticketID, PriorTicketID: prior.ID,
		Fingerprint: fp[:8], Permission: permEventText(ev.Text),
	}); err != nil {
		m.log.Error("追加 permission_reuse 事件失败", "task", taskID,
			"ticket", ticketID, "cause", err)
		// 审计事件失败不阻断放行：executor 正阻塞等应答，为一条审计把它挂死
		// 是更坏的结果；Error 日志已留痕
	}
	// 复用 fp 而不是重算：同一个 ev 算两遍 sha256 没有意义，且两处一旦不同步
	// 就会写出与查询键不一致的工单——正是 B91 要修的那类缺陷
	m.approvePermission(taskID, ticketID, ev.PermissionID, ev.Text, fp,
		"复用工单 "+prior.ID+" 的人工批准", "reuse")
	return true
}

// gateDecision 把协调者对 gate 工单的应答翻译成回传 executor 的裁决与可选原因。
//
// 参数：answer 为协调者应答原文（CLI 侧 --approve → "allow"、
// --deny [--reason r] → "deny" 或 "deny: r"，见 cmd/reply.go）
//
// 返回：
//   - decision: "once"（严格等于 "allow" 时）或 "reject"（其余一律）
//   - reason: 拒绝原因；无原因或批准时为空串
//
// 注意：
//   - 「非 allow 一律 reject」是安全语义，本函数新增 reason 返回值**不改变**它——
//     原因是给模型看的旁路信息，不参与裁决
func gateDecision(answer string) (decision, reason string) {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "allow" {
		return "once", ""
	}
	rest := strings.TrimPrefix(trimmed, "deny")
	rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), ":"))
	if rest == trimmed {
		// 前缀根本不是 deny（如协调者手工 POST 了任意文本）：照旧 reject，
		// 但那段文本不是「拒绝原因」，不下发给模型
		return "reject", ""
	}
	return "reject", rest
}

// noteDenyGuidance 登记一条待下发的拒绝原因。
//
// 参数：taskID 为任务 id；reason 为协调者给出的原因（空串直接忽略）
//
// 为什么不立刻 Send：executor 收到 reject 会当场终结回合（opencode 实测），
// 此刻发消息会撞上正在终结的回合，而回合终结时 adapter 还会补一条兜底提问——
// 协调者刚说完怎么改，又被问一遍「请给出下一步指令」。挂起到下一条 question
// 到达时再下发，正好用那次机会开新回合。
func (m *Manager) noteDenyGuidance(taskID, reason string) {
	if strings.TrimSpace(reason) == "" {
		return
	}
	m.apMu.Lock()
	m.denyGuidance[taskID] = reason
	m.apMu.Unlock()
	m.log.Info("登记待下发的拒绝原因", "task", taskID,
		"reason", truncateRunes(reason, 80))
}

// takeDenyGuidance 取走任务挂起的拒绝原因（读后即清）。
//
// 返回：挂起的原因；没有则为空串
//
// 为什么必须取走式：原因的生命周期是「从这次拒绝到下一条提问」。常驻会让后续
// 的真提问被永久吞掉，任务停在 running 无人知晓——与 askedViaTool 同一个坑。
func (m *Manager) takeDenyGuidance(taskID string) string {
	m.apMu.Lock()
	defer m.apMu.Unlock()
	r := m.denyGuidance[taskID]
	delete(m.denyGuidance, taskID)
	return r
}

// relayDenyGuidance 把协调者的拒绝原因作为一条普通消息下发给 executor，开新回合。
//
// 正文渲染与 claude 的同帧送达共用 turn.DenyGuidanceText，两条路措辞必须一致。
//
// 注意：
//   - **不得触碰状态机**：本分支不建工单，落 waiting_answer 会造出「等你回答却
//     零挂起工单」的死形态（reply/continue/done 三条路全封死）。任务保持 running
//   - Send 失败只记 Error + 审计事件：executor 此刻没有在等任何应答，
//     发不出去不会让任何东西挂死，协调者可用 continue 自己把话带上
func (m *Manager) relayDenyGuidance(ctx context.Context, taskID, guidance string) {
	text := turn.DenyGuidanceText(guidance)
	ad, err := m.adapterFor(taskID)
	if err != nil {
		m.log.Error("下发拒绝原因：解析执行者失败", "task", taskID, "cause", err)
		m.appendGuidanceDropped(taskID, guidance, err)
		return
	}
	actx, acancel := unaryCtx(ctx)
	defer acancel()
	if err := ad.Send(actx, taskID, text); err != nil {
		m.log.Error("下发拒绝原因失败", "task", taskID, "cause", err)
		m.appendGuidanceDropped(taskID, guidance, err)
		return
	}
	if _, err := m.st.AppendEvent(taskID, proto.EventTypeDenyGuidanceRelayed,
		denyGuidancePayload{Reason: guidance}); err != nil {
		m.log.Error("追加 deny_guidance_relayed 事件失败", "task", taskID, "cause", err)
	}
	m.log.Info("拒绝原因已下发，executor 将据此开新回合", "task", taskID,
		"reason", truncateRunes(guidance, 80))
}

// appendGuidanceDropped 记录拒绝原因没能下发的审计事件与告警：回合在下一条
// 提问到达前就终结了，协调者说的话无处送达，必须留痕让协调者知道用 continue
// 自己把话带上（B50）。
func (m *Manager) appendGuidanceDropped(taskID, guidance string, cause error) {
	evt, err := m.st.AppendEvent(taskID, proto.EventTypeDenyGuidanceDropped,
		denyGuidancePayload{Reason: guidance, Cause: cause.Error()})
	if err != nil {
		m.log.Error("追加 deny_guidance_dropped 事件失败", "task", taskID, "cause", err)
	} else {
		// 同 clearApproverState 处的理由（B91）：可操作唤醒，不是纯审计
		m.hub.Publish(evt)
	}
	m.log.Warn("拒绝原因未下发：回合已终结，用 continue 自己把话带上",
		"task", taskID, "reason", truncateRunes(guidance, 80), "cause", cause)
}

// waitPermission 阻塞等待权限工单的协调者应答，按规则回传 executor 并回迁 running。
//
// 规则：应答 trim 后等于 "allow" → RespondPermission("once")，其余一律 "reject"
// （deny:原因 等拒绝表达全部归入 reject，原因已随应答落库可追溯）。
//
// 为什么先回迁 running 再回传 executor：respond 一发出 executor 立刻继续执行并可能
// 立即产出下一个事件（如 result），若回迁在 respond 之后，result 可能先到而状态还
// 卡在 waiting_answer，导致 waiting_answer→waiting_review 非法迁移被拒。先落状态
// 保证「executor 恢复执行时状态必为 running」。
//
// 参数：permID 是 executor 侧裸权限 id（adapter 契约），ticketID 是命名空间化的
// 工单 id（hub 等待键与 reply 路由都用它）。
func (m *Manager) waitPermission(ctx context.Context, taskID, permID, ticketID string) {
	start := time.Now()
	ans, err := m.hub.WaitAnswer(ctx, ticketID)
	if err != nil {
		m.log.Warn("权限应答等待被取消", "task", taskID, "perm", permID, "ticket", ticketID, "cause", err)
		return
	}
	m.log.Info("权限应答已收到", "task", taskID, "perm", permID, "ticket", ticketID,
		"wait_ms", time.Since(start).Milliseconds(), "answer", truncateRunes(ans, 80))

	// 回迁失败（reply 回程的 resumeIfIdle 已抢先回迁）由 transitBestEffort 容忍
	m.transitBestEffort(taskID, proto.TaskStateRunning, "permission 已应答")
	decision, reason := gateDecision(ans)
	// 派生子 ctx 只约束调用本身（unaryCtx 的 why）：等答案阶段早已结束，此处的
	// parent 是任务级 ctx（取消无截止），不加超时的话半死 executor 会让本调用挂死
	actx, acancel := unaryCtx(ctx)
	defer acancel()
	ad, err := m.adapterFor(taskID)
	if err != nil {
		m.log.Error("解析任务执行者失败", "task", taskID, "cause", err)
		return
	}
	if err := ad.RespondPermission(actx, taskID, permID, decision, reason); err != nil {
		// executor 侧可能已不在（进程被杀）：记录错误并保持现状，交由协调者裁决。
		// 工单未标记送达，协调者可用 handoff resume 重投（见 RecoverStuck）
		m.log.Error("回应权限失败", "task", taskID, "perm", permID, "decision", decision, "cause", err)
		m.NoteDeliveryFailed(taskID, ticketID, err)
		return
	}
	m.noteDenyGuidanceUnlessInBand(taskID, permID, reason)
	m.markDelivered(taskID, ticketID)
}

// handleQuestion 中介提问：ticket(uuid, kind=ask) → 事件 → waiting_answer →
// goroutine 等协调者回答后原样透传 executor。
//
// 顺序契约与 handlePermission 相同（P1-2）：先置 waiting_answer 再 Publish；
// waiter 注册异步，reply 先于注册到达时退化为自愈中继路径兜底。
func (m *Manager) handleQuestion(ctx context.Context, taskID string, ev executor.AdapterEvent) {
	// 拒绝原因优先下发（B50）：协调者刚说完该怎么改，此刻 executor 的任何提问
	// 都应先收到那条指令。收到后若仍要问，它会再发一次 question——那时 guidance
	// 已消费，正常出单。
	//
	// 这里刻意不区分「被拒终止的兜底提问」与「模型真的在问问题」：manager 没有
	// 可靠判据（文本前缀匹配一改文案就失效）。吞错的代价是**模型**的一个回合，
	// 漏抑制的代价是**协调者**的一个回合——后者正是本条要消灭的东西。
	if guidance := m.takeDenyGuidance(taskID); guidance != "" {
		m.relayDenyGuidance(ctx, taskID, guidance)
		return
	}
	// 工单 id 优先用 executor 的原生提问 id 派生（taskID:questionID），与 gate
	// 工单同构、天然幂等：agentd 重启后 executor 重放同一个 request 时，
	// CreateTicket 直接返回 created=false，不会产出第二张永远答不掉的单（B58）。
	// executor 没有原生 id 时退回 uuid——问题没有天然稳定 id，回答一次即终结。
	ticketID := uuid.NewString()
	if ev.QuestionID != "" {
		ticketID = taskID + ":" + ev.QuestionID
	}
	m.transitBestEffort(taskID, proto.TaskStateWaitingAnswer, "question")
	req, _ := json.Marshal(ticketRequest{Kind: "ask", Question: ev.Text})
	created, err := m.st.CreateTicket(&proto.Ticket{
		ID: ticketID, TaskID: taskID, Kind: "ask",
		Request: req, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		m.log.Error("创建提问工单失败", "task", taskID, "ticket", ticketID, "cause", err)
		m.transitBestEffort(taskID, proto.TaskStateRunning, "提问工单创建失败回滚")
		return
	}
	if !created {
		prior, gerr := m.st.GetTicket(ticketID)
		switch {
		case gerr != nil:
			m.log.Error("提问工单已存在但读取失败，按重放跳过", "task", taskID,
				"ticket", ticketID, "cause", gerr)
			return
		case prior.Answer == nil:
			// 重放：agentd 重启后 executor 重发了同一个仍未作答的 request。
			// 不建单、不发第二条事件（协调者已经被叫醒过一次），但必须重挂
			// waiter——新 agentd 实例里没有任何 goroutine 在等这张单
			m.log.Info("提问重放，复用既有工单并重挂等待", "task", taskID, "ticket", ticketID)
			go m.waitQuestion(ctx, taskID, ticketID)
			return
		default:
			// 重发：旧单已答，但 executor 又问了一次（opencode 的「答复没对上
			// 选项」路径用的是同一个 reqID）。此时**必须**新开一张单——复用已答
			// 工单的 id 会让协调者再也答不了，任务停在 waiting_answer 到 stall
			m.log.Info("提问重发（旧单已答），另开新工单", "task", taskID,
				"prior_ticket", ticketID)
			ticketID = uuid.NewString()
			if _, err := m.st.CreateTicket(&proto.Ticket{
				ID: ticketID, TaskID: taskID, Kind: "ask",
				Request: req, CreatedAt: time.Now().UTC(),
			}); err != nil {
				m.log.Error("创建重发提问工单失败", "task", taskID, "ticket", ticketID, "cause", err)
				m.transitBestEffort(taskID, proto.TaskStateRunning, "提问工单创建失败回滚")
				return
			}
		}
	}
	evt, err := m.st.AppendEvent(taskID, proto.EventTypeQuestion, questionPayload{
		TicketID: ticketID, Question: ev.Text, Kind: "ask",
	})
	if err != nil {
		m.log.Error("追加 question 事件失败", "task", taskID, "ticket", ticketID, "cause", err)
		return
	}
	go m.waitQuestion(ctx, taskID, ticketID)
	m.hub.Publish(evt)
}

// waitQuestion 阻塞等待提问工单的回答，把回答原文原样透传给 executor 并回迁 running。
//
// 为什么原文透传：回答语义（技术建议、需求取舍）只有协调者理解，manager 不做
// 任何加工——无损透传是「回答什么由协调者决定」承诺的执行保证。
//
// 为什么先回迁 running 再 Send：同 waitPermission——Send 一发出 executor 立刻恢复
// 执行并可能立即产出 result，先落状态保证 result 到达时状态必为 running。
func (m *Manager) waitQuestion(ctx context.Context, taskID, ticketID string) {
	start := time.Now()
	ans, err := m.hub.WaitAnswer(ctx, ticketID)
	if err != nil {
		m.log.Warn("提问应答等待被取消", "task", taskID, "ticket", ticketID, "cause", err)
		return
	}
	m.log.Info("提问应答已收到", "task", taskID, "ticket", ticketID,
		"wait_ms", time.Since(start).Milliseconds(), "answer", truncateRunes(ans, 80))

	m.transitBestEffort(taskID, proto.TaskStateRunning, "question 已应答")
	actx, acancel := unaryCtx(ctx)
	defer acancel()
	ad, err := m.adapterFor(taskID)
	if err != nil {
		m.log.Error("解析任务执行者失败", "task", taskID, "cause", err)
		return
	}
	if err := ad.Send(actx, taskID, ans); err != nil {
		m.log.Error("回发提问回答失败", "task", taskID, "ticket", ticketID, "cause", err)
		m.NoteDeliveryFailed(taskID, ticketID, err)
		return
	}
	m.markDelivered(taskID, ticketID)
}

// RecoverReport 是显式恢复操作的结果快照，原样作为 HTTP 响应体回给 CLI。
type RecoverReport struct {
	Task string `json:"task"`
	// Redelivered 是本次成功重投给 executor 的应答条数
	Redelivered int `json:"redelivered"`
	// ExecutorGone 为真表示 executor 已不在，任务已被交给协调者裁决
	ExecutorGone bool `json:"executor_gone"`
	// Reconciled 为真表示本次真的执行了会话对账（adapter 支持且任务状态合适）；
	// 为假时 TurnEnded/Emitted 无意义
	Reconciled bool `json:"reconciled"`
	// TurnEnded 表示对账查到的回合是否已完结
	TurnEnded bool `json:"turn_ended"`
	// Emitted 是对账补发的终态事件数（0 或 1）
	Emitted int `json:"emitted"`
	// Forced 为真表示本次走了 --force 强制收口（状态由人工推动，未经 executor 确认）
	Forced bool `json:"forced"`
	// State 是操作完成后的任务状态
	State proto.TaskState `json:"state"`
	// Note 是给协调者看的一句话结论
	Note string `json:"note"`
}

// reconciler 是 adapter 的可选对账能力（B38）。
//
// 为什么定义在 manager 而不是 executor 包：沿用 internal/executor/resume.go
// 明写的既有约定——能力接口由消费方定义并做类型断言，这样「不支持对账的
// adapter」是自然语义，executor.Adapter 的五动作核心契约也不被污染。
// restorer / volatilePermitter 都是这个形状。
type reconciler interface {
	Reconcile(ctx context.Context, taskID string) (executor.ReconcileOutcome, error)
}

// RecoverStuck 是协调者的显式恢复操作（CLI: handoff resume <task>），
// 用来解开两类卡死：
//   - 「应答已落库但没送到 executor」：重投未送达的应答
//   - 「agentd 与 executor 断连期间回合已完结、终态事件丢失」（B38）：
//     会话对账补发丢失的终态
//
// 为什么需要它：reply 的回程里，应答一旦落库就消耗掉了工单的 answer IS NULL
// 守卫；若此时中继失败（executor 半死、调用超时），协调者会拿到 502，而工单
// 已从 pending 里消失、任务停在 waiting_answer——reply 得 404、continue/done
// 得 409，CLI 上再无一条可走的路。此前唯一的出口是运维重启 agentd 让
// RecoverOnStartup 探活，而那条路只在 executor **已死**时有效：executor 还
// 活着并仍阻塞在权限上时，重启探活成功、订阅重建、已答工单从不重放，是彻底的
// 死锁。B38 又补上第二类：断连窗口内完成的回合，终态事件在 /event 上永久丢失，
// 任务冻死在 running。本方法把出口交到协调者自己手里。
//
// 参数：
//   - taskID: 任务 ID
//   - force: 为真时即使对账判不出（executor 不支持对账 / 回合确实还在忙 /
//     查询失败），仍把任务强制收口到 waiting_review，使 continue/done 可用；
//     收口会留下写明「人工强制、未经 executor 确认」的事件
//
// 返回：
//   - 恢复结果快照（即使返回错误也可能非 nil，用于区分「executor 已死」与「这次没成功」）
//   - 任务不存在、已终结，或重投过程中 executor 仍不可用时返回错误
//
// 行为：
//  1. 无未送达应答 → 转入会话对账（adapter 支持且状态合适时）；force 时再收口
//  2. 有未送达应答 → 逐条重投；成功即标记送达，全部成功后任务回 running
//  3. 重投遇到 executor.ErrTaskNotRunning（executor 确实不在）→ 追加 failed
//     事件、作废挂起工单、任务转 waiting_review 交协调者，不再重试
//  4. 重投遇到其他错误（executor 还在，只是这次调用失败）→ 保持 waiting_answer
//     与未送达标记，返回错误；协调者稍后可再执行一次
//
// 注意：
//   - 幂等：已标记送达的应答不会被重投，重复执行是安全的；对账也幂等（水位已
//     过的回合不会二次补发）
//   - 与 ResumeTask 的区别：ResumeTask 是 agentd 重启时的执行器存活探测与订阅
//     重建（进程级），本方法是单任务的应答重投与会话对账（工单级），两者互不替代
func (m *Manager) RecoverStuck(taskID string, force bool) (*RecoverReport, error) {
	task, err := m.st.GetTask(taskID)
	if err != nil {
		return nil, fmt.Errorf("读取任务 %s: %w", taskID, err)
	}
	if task.State == proto.TaskStateCompleted || task.State == proto.TaskStateFailed {
		return nil, fmt.Errorf("任务 %s 已终结（%s），无可恢复项: %w", taskID, task.State, store.ErrBadTransit)
	}
	rep := &RecoverReport{Task: taskID, State: task.State}

	stuck, err := m.st.UndeliveredAnswers(taskID)
	if err != nil {
		return nil, err
	}
	if len(stuck) == 0 {
		m.log.Info("恢复操作：无未送达应答，转入会话对账", "task", taskID, "state", task.State)
		m.reconcileInto(rep, taskID, task.State)
		if force {
			m.forceToReview(rep, taskID, task.State)
		}
		return rep, nil
	}
	m.log.Info("恢复操作：开始重投未送达应答", "task", taskID, "count", len(stuck))

	for _, tk := range stuck {
		answer := ""
		if tk.Answer != nil {
			answer = *tk.Answer
		}
		if err := m.RelayAnswer(taskID, tk.ID, answer); err != nil {
			if errors.Is(err, executor.ErrTaskNotRunning) {
				// executor 确实不在：继续重投没有意义，转交协调者裁决
				rep.ExecutorGone = true
				rep.State = m.abandonToReview(taskID, tk.ID, err)
				rep.Note = "executor 已不在，任务已转交审核（可 continue 重新派发或 done 归档）"
				m.log.Warn("恢复操作：executor 已不在，任务转交审核",
					"task", taskID, "ticket", tk.ID, "cause", err)
				return rep, nil
			}
			// executor 还在，只是这次没打通：保持现状可重试
			rep.Note = "重投失败，executor 仍在但未响应；稍后可再执行一次 resume"
			m.log.Error("恢复操作：重投应答失败", "task", taskID, "ticket", tk.ID, "cause", err)
			return rep, fmt.Errorf("重投应答 %s: %w", tk.ID, err)
		}
		rep.Redelivered++
	}

	// 全部送达：executor 已恢复执行，状态回 running（若已有其他挂起工单，
	// transitBestEffort 的失败是良性的——状态本就该留在 waiting_answer）
	if task.State == proto.TaskStateWaitingAnswer {
		m.transitBestEffort(taskID, proto.TaskStateRunning, "恢复操作重投应答成功")
	}
	if cur, gerr := m.st.GetTask(taskID); gerr == nil {
		rep.State = cur.State
	}
	rep.Note = "应答已重新送达 executor，执行继续"
	m.log.Info("恢复操作完成", "task", taskID, "redelivered", rep.Redelivered, "state", rep.State)
	return rep, nil
}

// reconcileInto 执行一次会话对账并把结论写进报告（B38）。
//
// 参数：
//   - rep: 待填充的报告；本函数只写 Reconciled/TurnEnded/Emitted/State/Note
//   - taskID / state: 目标任务与其当前状态
//
// 注意：
//   - 只对 running / waiting_answer 生效。其余状态如实说明而不是静默成功：
//     pending 尚未启动、waiting_review 本就该走 continue/done、终态已结束
//   - adapter 未实现 reconciler 时不改状态、不伪装成「对账过了」
//   - 对账失败只记 WARN 并把原因写进 Note，**不返回错误**——协调者要的是
//     「现在怎么办」，不是一个让 CLI 退非零的堆栈
func (m *Manager) reconcileInto(rep *RecoverReport, taskID string, state proto.TaskState) {
	if state != proto.TaskStateRunning && state != proto.TaskStateWaitingAnswer {
		rep.Note = fmt.Sprintf("没有卡在半路的应答；任务处于 %s，不在对账范围"+
			"（pending 尚未启动、waiting_review 请用 continue/done）", state)
		m.log.Info("恢复操作：状态不在对账范围", "task", taskID, "state", state)
		return
	}
	ad, err := m.adapterFor(taskID)
	if err != nil {
		rep.Note = "没有卡在半路的应答；解析任务执行者失败（" + err.Error() +
			"），可 handoff attach 查看现场，或 handoff resume --force 强制收口交审核"
		m.log.Warn("恢复操作：解析任务执行者失败", "task", taskID, "cause", err)
		return
	}
	rc, ok := ad.(reconciler)
	if !ok {
		rep.Note = "没有卡在半路的应答；当前 executor 不支持会话对账，" +
			"可 handoff attach 查看现场，或 handoff resume --force 强制收口交审核"
		m.log.Info("恢复操作：adapter 不支持对账", "task", taskID)
		return
	}
	out, err := rc.Reconcile(context.Background(), taskID)
	if err != nil {
		rep.Note = "没有卡在半路的应答；会话对账失败（" + err.Error() +
			"），未改动任何状态，可稍后重试或 --force 强制收口"
		m.log.Warn("恢复操作：会话对账失败", "task", taskID, "cause", err)
		return
	}
	rep.Reconciled = true
	rep.TurnEnded = out.TurnEnded
	rep.Emitted = out.Emitted
	rep.Note = out.Note
	if out.Emitted > 0 {
		// 补发的事件已走 evCh 进中介循环，状态由它推动；此处重读一次让报告
		// 里的 State 是对账之后的真实值而不是进函数时的快照
		if t, gerr := m.st.GetTask(taskID); gerr == nil {
			rep.State = t.State
		}
	}
	m.log.Info("恢复操作：会话对账完成", "task", taskID,
		"turn_ended", out.TurnEnded, "emitted", out.Emitted, "note", out.Note)
}

// forceToReview 把任务强制收口到 waiting_review（handoff resume --force）。
//
// 为什么需要它：对账判不出来（adapter 不支持 / 会话确实还在忙 / 查询失败）时，
// 协调者此前只剩 handoff stop——而 stop 会把一个其实成功了的任务落成 failed，
// 并杀掉 executor。本操作**保住会话**，只把状态推到可 continue/done 的位置。
//
// 风险与护栏：executor 可能真的还在跑，收口后 continue 会往忙碌会话里塞指令。
// 护栏只有事件文本与报告文案——不加更硬的拦截，因为更硬的拦截就是 stop，
// 而这个场景的全部意义恰恰是不杀会话。
func (m *Manager) forceToReview(rep *RecoverReport, taskID string, state proto.TaskState) {
	rep.Forced = true
	text := "协调者人工强制收口（handoff resume --force）：未经 executor 确认。" +
		"对账当时的结论是：" + rep.Note + "。若 executor 其实仍在执行，" +
		"后续 continue 的指令会进入一个忙碌会话，请先 handoff attach 确认现场。"
	m.log.Warn("恢复操作：人工强制收口", "task", taskID, "from", state, "note", rep.Note)
	m.appendProgress(taskID, text)
	if err := recoverTransit(m.st, taskID, state); err != nil {
		rep.Note = "强制收口失败：" + err.Error()
		m.log.Error("恢复操作：强制收口迁移失败", "task", taskID, "cause", err)
		return
	}
	rep.State = proto.TaskStateWaitingReview
	rep.Note = "已人工强制收口到待审核（未经 executor 确认）；" +
		"可 continue 续接或 done 归档。对账当时的结论：" + rep.Note
}

// appendProgress 追加一条 progress 事件并广播（恢复/强制收口等人工提示用）。
// 落库失败只 Error 不回滚——状态迁移已经发生，事件缺失最坏是协调者少看一条提示。
func (m *Manager) appendProgress(taskID, text string) {
	evt, err := m.st.AppendEvent(taskID, proto.EventTypeProgress, progressPayload{Text: text})
	if err != nil {
		m.log.Error("追加 progress 事件失败", "task", taskID, "cause", err)
		return
	}
	m.hub.Publish(evt)
}

// abandonToReview 在确认 executor 已不在时收尾：留下 failed 事件说明原因、
// 作废挂起工单（避免 attach 继续展示不可能被回答的项）、任务转 waiting_review。
// 返回收尾后的任务状态。
//
// 收尾实现已统一到 reconcileExecutorGone，本函数只负责拼这一句 reason。
func (m *Manager) abandonToReview(taskID, ticketID string, cause error) proto.TaskState {
	return reconcileExecutorGone(m.st, m.hub, taskID,
		fmt.Sprintf("恢复操作发现 executor 已不在，应答 %s 无法送达: %v", ticketID, cause), m.log, m.SweepTaskProcs)
}

// markDelivered 记录「应答已送达 executor」。失败仅 Warn：送达本身已经发生，
// 标记丢失最坏只会让 RecoverStuck 多重投一次，不影响正确性。
func (m *Manager) markDelivered(taskID, ticketID string) {
	if err := m.st.MarkTicketDelivered(ticketID); err != nil {
		m.log.Warn("标记应答已送达失败", "task", taskID, "ticket", ticketID, "cause", err)
	}
}

// NoteDeliveryFailed 产出 delivery_failed 事件：应答已落库但没送到 executor。
//
// 参数：
//   - taskID: 任务 ID
//   - ticketID: 未送达的工单 ID
//   - cause: 送达失败的原因（原样进 payload 供协调者诊断）
//
// why（必须是事件而不只是日志）：此时 executor 仍原地阻塞，而工单已被应答消耗、
// 不再出现在 attach 的挂起项里——只写日志的话协调者这边完全无感，任务一路挂到
// 看门狗超时。产出事件才能唤醒协调者（wait 不过滤该类型），提示执行 handoff resume。
// 供 manager 内部的应答等待链路与 server 的 reply 回程共用。
func (m *Manager) NoteDeliveryFailed(taskID, ticketID string, cause error) {
	evt, err := m.st.AppendEvent(taskID, proto.EventTypeDeliveryFailed, deliveryFailedPayload{
		TicketID: ticketID,
		Reason:   truncateRunes(fmt.Sprint(cause), 500),
		Hint:     "应答已落库但未送达 executor，执行 handoff resume <task> 重投",
	})
	if err != nil {
		m.log.Error("追加 delivery_failed 事件失败", "task", taskID, "ticket", ticketID, "cause", err)
		return
	}
	m.hub.Publish(evt)
}

// handleProgress 中介进度事件：只入库广播，不改任务状态。
//
// SessionID 非空时先落 task.ExecutorSession：「会话就绪」信号（adapter 建会话
// 成功后第一条 progress）是会话 id 到达 manager 的可靠通道——审核主路径常以
// question 收尾、result 永不出现。落库失败仅 Warn，不影响主流程（会话 id 属
// 可修复的辅助字段，进度广播照常）。
func (m *Manager) handleProgress(taskID string, ev executor.AdapterEvent) {
	if ev.SessionID != "" {
		if err := m.st.SetTaskField(taskID, "executor_session", ev.SessionID); err != nil {
			m.log.Warn("落库 executor_session 失败", "task", taskID, "session", ev.SessionID, "cause", err)
		}
	}
	evt, err := m.st.AppendEvent(taskID, proto.EventTypeProgress, progressPayload{Text: ev.Text})
	if err != nil {
		m.log.Error("追加 progress 事件失败", "task", taskID, "cause", err)
		return
	}
	m.hub.Publish(evt)
}

// handleUsage 落 executor 报回的实际模型名与 context 占用。
//
// 与 handleProgress 的区别：**只写任务字段，不追加事件行、不广播**。
// 用量刷新频率高（claudecode 一个回合几百条 assistant 消息），进事件日志会淹没
// 审核者真正要看的 permission/question/completed；界面靠详情轮询自然拿到，
// 不需要事件推送。
//
// 去重是写库风暴的唯一防线：与内存里上一次的三元组全等就直接返回。
// agentd 重启后内存态为空，首帧必写一次，这是可接受的代价。
//
// 落库失败仅 Warn：用量属可修复的辅助字段，与 executor_session 同级，
// 不影响主流程。
func (m *Manager) handleUsage(taskID string, ev executor.AdapterEvent) {
	tokens, window := 0, (*int)(nil)
	if ev.Usage != nil {
		tokens = ev.Usage.ContextTokens
		window = ev.Usage.ContextWindow
	}
	if m.takeUsageUnchanged(taskID, ev.ActualModel, tokens, window) {
		return
	}
	if err := m.st.SetTaskUsage(taskID, ev.ActualModel, tokens, window); err != nil {
		m.log.Warn("落库任务用量失败", "task", taskID, "model", ev.ActualModel,
			"tokens", tokens, "cause", err)
		return
	}
	m.log.Info("任务用量已更新", "task", taskID, "model", ev.ActualModel,
		"tokens", tokens, "window", window)
}

// takeUsageUnchanged 判定这次上报与上一次是否完全相同（相同返回 true，
// 调用方据此跳过打库），并在不同的情况下就地记下新值。
//
// 为什么把窗口也纳入比较：不报窗口的执行者每次都传 nil，只比模型名与分子会让
// 「窗口首次到达」这一帧被误判成重复而丢掉。
func (m *Manager) takeUsageUnchanged(taskID, model string, tokens int, window *int) bool {
	key := fmt.Sprintf("%s|%d|%v", model, tokens, derefOrNil(window))
	m.usageMu.Lock()
	defer m.usageMu.Unlock()
	if m.lastUsage == nil {
		m.lastUsage = map[string]string{}
	}
	if m.lastUsage[taskID] == key {
		return true
	}
	m.lastUsage[taskID] = key
	return false
}

// derefOrNil 把 *int 摊平成可比较的展示值：nil 记作 -1（真实窗口必然 > 0，
// 不会与之相撞）。
func derefOrNil(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

// handleSpend 把 executor 报回的「本次新增消耗」记进账本。
//
// 与 handleUsage 的区别（**两条通道必须互不干扰**）：
//   - handleUsage 走 SetTaskUsage，**整体覆盖** (model, tokens, window) 三元组，
//     描述「当前占用」；
//   - 本方法走 UpsertSpend，按幂等键**覆盖单行**后求和，描述「累计消耗」。
//
// 拿任何一条的值去写另一条，都会产生一个不报错、只是数字错的故障。
//
// 与 handleUsage 相同的两点：**只写库，不追加事件行、不广播**（频率同样高，
// 进事件日志会淹没审核者真正要看的 permission/question/completed）；
// 落库失败仅 Warn（用量属可修复的辅助字段，不影响主流程）。
//
// 幂等不做内存去重：内存态在 agentd 重启后为空，首帧必写一次——对「当前占用」
// 无害（覆盖成同值），对「累计」就是重复计数。所以幂等只能落在库里。
func (m *Manager) handleSpend(taskID string, ev executor.AdapterEvent) {
	if ev.Spend == nil {
		return
	}
	if err := m.st.UpsertSpend(taskID, *ev.Spend); err != nil {
		m.log.Warn("记任务消耗失败", "task", taskID, "key", ev.Spend.Key, "cause", err)
		return
	}
	m.log.Debug("任务消耗已入账", "task", taskID, "key", ev.Spend.Key,
		"input", ev.Spend.InputTokens, "cached", ev.Spend.CachedTokens,
		"output", ev.Spend.OutputTokens, "cost_state", ev.Spend.CostState)
}

// handleResult 中介回合结果：OK → completed 事件，!OK → failed 事件；两者都进
// waiting_review（why 见文件头）——失败也交协调者裁决，不自动重试烧 token。
//
// 顺序是「迁移状态 → 追加事件 → 广播」，三步都不能换位：协调者（或脚本）收到
// completed 事件后可能立即执行 continue/done，若状态尚未回迁 waiting_review 会被
// 409 拒绝，所以状态必须先就位。
//
// 为什么迁移必须排在**追加事件**之前，而不只是排在 Publish 之前（2026-08-13 修）：
// 事件有两条送达路径，Publish 只是其中之一。WS 连接建立时的历史重放直接读 store
// （server.go 的 EventsFromAsc），所以事件在 **AppendEvent 落库的那一刻**就已经
// 可被观测——排在迁移之前就等于把「事件到达时状态已就绪」这条保证只留给实时订阅
// 者，任何断线重连、一次性 wait（每轮新建连接）都会撞进窗口。CI 上实测撞到过：
// TestFullLoop 收到第二条 completed 后立刻 Done，得到 409。
//
// 崩溃语义也因此更好：崩在两步之间留下的是「waiting_review 但缺一条 completed」，
// show 出来仍是可裁决的；反过来则是「卡在 running 却已有 completed」，只能等看门狗。
//
// 迁移失败（如并发 done 已抢先归档）则事件也不追加、不广播：任务已终结，既不该
// 唤醒协调者去操作它，也不该给一个已归档的任务再记一条回合结果。
func (m *Manager) handleResult(taskID string, ev executor.AdapterEvent) {
	if ev.Result == nil {
		m.log.Error("result 事件缺 Result", "task", taskID)
		return
	}
	// 自动放行汇总：AutoAllow 路径逐次只打 Debug，这里给出一条 Info 级总量。
	// 完全静默会让「出问题时没有第一现场」，而逐次 Info 会淹没日志。
	if n := m.takeAutoAllowed(taskID); n > 0 {
		m.log.Info("本段执行自动放行工作区内写入", "task", taskID, "n", n)
	}
	// 已归档（done 后）的杂散 result 直接丢弃，不重复追加事件
	cur, err := m.st.GetTask(taskID)
	if err != nil {
		m.log.Error("读取任务失败", "task", taskID, "cause", err)
		return
	}
	if cur.State == proto.TaskStateCompleted || cur.State == proto.TaskStateFailed {
		m.log.Debug("任务已终结，忽略 result 事件", "task", taskID, "state", cur.State)
		return
	}

	r := ev.Result
	// result 落 executor_session 是「会话就绪」progress 的双保险：适配器在
	// result 上也携带 SessionID，即使 progress 通道异常（如乱序丢失），会话 id
	// 仍经此落库。失败仅 Warn，不影响回合结果中介主流程。
	if r.SessionID != "" {
		if err := m.st.SetTaskField(taskID, "executor_session", r.SessionID); err != nil {
			m.log.Warn("落库 executor_session 失败", "task", taskID, "session", r.SessionID, "cause", err)
		}
	}
	// 状态先就位：事件一落库就可被 WS 重放读到，晚于事件迁移等于放任协调者
	// 在 running 上执行 done（详见函数头注释）
	if err := m.transitToReview(taskID); err != nil {
		m.log.Error("回迁 waiting_review 失败，不追加也不广播结果事件", "task", taskID, "cause", err)
		return
	}
	var evt proto.Event
	if r.OK {
		evt, err = m.st.AppendEvent(taskID, proto.EventTypeCompleted, completedPayload{
			Branch: r.Branch, CommitHash: r.CommitHash, Summary: r.Summary,
		})
	} else {
		// 挂起工单一并作废（U-3）：与 RecoverOnStartup 的重启恢复路径同语义。
		// 不作废的话 attach 仍向协调者展示可操作的挂起项，而工单已无人消费——
		// 一旦 reply，工单被消耗、中继失败返回 502，任务落进不可恢复状态。
		//
		// 理由由 result 侧提供而非硬编码：作废行为在两种情形下相同，但审计
		// 必须说真话。executor 真死传 VoidReasonExecutorGone，回合纪律类失败
		// （无 trailer、零文本）传 VoidReasonTurnDiscipline——后者 executor 还活着。
		// 历史上零文本回合的 FailReason 明写「executor 仍在线，可 continue 续接
		// 重试」，却被同一次调用记成「executor 已终结」，审计与事实互相矛盾。
		voidReason := r.VoidReason
		if voidReason == "" {
			voidReason = executor.VoidReasonExecutorGone
		}
		voidTicketsWithAudit(m.st, taskID, voidReason, m.log)
		m.log.Warn("回合以失败收尾", "task", taskID, "reason", r.FailReason,
			"branch", r.Branch, "commit", r.CommitHash, "void_reason", voidReason)
		// 类型是 turn_failed 而不是 failed：上面 transitToReview 已经把任务迁到
		// waiting_review，它**没有终结**。发 failed 会让 wait --follow 打出
		// 「任务已失败」并以 0 退出，而此时任务好端端等着审（B100 两次真机实测）。
		evt, err = m.st.AppendEvent(taskID, proto.EventTypeTurnFailed,
			newFailedPayload(r.FailReason, r.Branch, r.CommitHash))
	}
	if err != nil {
		m.log.Error("追加 result 事件失败", "task", taskID, "cause", err)
		return
	}
	// 回合结束（任务进入 waiting_review）：清理审批链运行时状态，防内存 map
	// 随任务无界增长；任务被续接时从干净状态重新评估（P2-5）
	m.clearApproverState(taskID)
	m.hub.Publish(evt)
	// 回合结束（非终态）也清扫这一回合留下的逃逸后代：不必等到任务终结才收。
	// 与 transit 的终态清扫不重复——那条管终态，这条管回合边界。
	// executor 此时通常仍存活（serve 常驻模型），走 Sweep 的降级路径只做名册
	// 点名（B119 §2.1），executor 本体不会被误杀。
	//
	// 放在 Publish 之后：事件先落库并广播，审核者的 wait 第一时间醒；清扫是
	// best-effort 的善后（Sweep 内部每个失败分支都只记日志或发 orphan_risk），
	// 不该挡在唤醒前面。
	//
	// 成功分支也清扫：executor 正常收尾同样可能留下 setsid 逃逸的后代
	// （opencode 的 Bash 工具把每条命令都 setsid 成新会话）。
	//
	// 与 B92 的关系（B92 的根因在本改动之后被推翻，这里记下修正后的事实）：
	// 曾以为存在「failed 事件落库但状态没迁移」的缺口，若成立则本调用在那条
	// 路径上不会执行。排查用日志与 DB 证伪了它——handleResult 的迁移一直是
	// 正确的，B92 的真因是 grok 在回合失败时关掉了事件通道，导致 continue 的
	// 续接回合事件被静默丢弃（已修，见 internal/executor/grok 的 emitTurnFailed）。
	// 所以本调用在回合失败时**会**正常触发。watchdog 的每任务点名
	// （scanTaskProcs）仍是有价值的冗余，但兜的不是这条路径，而是
	// Manager.Stop 与 reconcileExecutorGone 那两条「先落事件后迁移、迁移失败」
	// 的真实缺口（另见 B97）。
	_ = m.sweep(taskID)
}

// transitToReview 把任务迁入 waiting_review；若当前状态不允许直跳（典型为回答-续跑
// 链路尚未回迁 running 的 waiting_answer 防御场景），按最新快照走兜底路径重试。
//
// 为什么失败后必须重读重试而不是直接报错：本方法一返回错误，handleResult 就连
// result 事件都不再追加（顺序见其函数头），一次可收敛的 CAS 竞态会被误判成「任务
// 已终结」，任务卡在 running 直到看门狗——竞态细节见 transitToReviewRetry。
func (m *Manager) transitToReview(taskID string) error {
	if err := m.transit(taskID, proto.TaskStateWaitingReview, "result"); err == nil {
		return nil
	}
	return m.transitToReviewRetry(taskID)
}

// transitToReviewRetry 在首跳失败后按最新快照重试进入 waiting_review。
//
// 两种可收敛路径：
//   - waiting_answer：回答-续跑链路尚未回迁 running 的防御场景，两跳经 running 进入
//   - running：残留竞态——首跳失败后、重读前应答 goroutine 已把 waiting_answer 回迁
//     running（running→waiting_review 合法），直接重试补跳即可，已追加的结果事件
//     不因该时序丢失（否则任务卡死在 running 直到看门狗）
//
// 注意：
//   - 两跳中的 running 迁移容忍 ErrBadTransit：该迁移与应答 goroutine 的回迁并发竞争
//     同一 CAS，输家（本方法）说明 running 已达成（谁赢都是 running），继续补跳即收敛
//   - 其余状态（如 done 已抢先归档的 completed/failed）返回错误：任务已终结，
//     由 handleResult 决定不广播，避免唤醒协调者去操作一个已终结的任务
func (m *Manager) transitToReviewRetry(taskID string) error {
	cur, err := m.st.GetTask(taskID)
	if err != nil {
		return err
	}
	switch cur.State {
	case proto.TaskStateWaitingAnswer:
		if err := m.transit(taskID, proto.TaskStateRunning, "result 到达前回迁"); err != nil && !errors.Is(err, store.ErrBadTransit) {
			return err
		}
	case proto.TaskStateRunning:
		// 残留竞态窗口：首跳失败后、重读前应答 goroutine 已回迁 running，直接补跳
	default:
		return fmt.Errorf("任务不在 waiting_answer/running，无法进入 waiting_review: %s", cur.State)
	}
	return m.transit(taskID, proto.TaskStateWaitingReview, "result")
}

// transit 迁移任务状态并记录 from→to 迁移日志；已在目标状态时幂等返回 nil。
func (m *Manager) transit(taskID string, to proto.TaskState, reason string) error {
	cur, err := m.st.GetTask(taskID)
	if err != nil {
		return err
	}
	if cur.State == to {
		return nil
	}
	if err := m.st.UpdateTaskState(taskID, to); err != nil {
		return err
	}
	m.log.Info("任务状态迁移", "task", taskID, "from", cur.State, "to", to, "reason", reason)
	// 终态收口（B63）：done / stop / 各处 transitBestEffort 全部经过本函数，作废挂
	// 在这里才能覆盖**将来新增的**终态路径——B63 本身就是「新增一条路径时忘了补」
	// 漏出来的。
	//
	// 必须排在 UpdateTaskState 成功之后：该迁移可能因并发 CAS 失败（ErrBadTransit），
	// 那时任务仍然活着，先作废等于砸掉它的合法挂起工单。
	//
	// 幂等分支（cur.State == to）在上面已经 return，不会重复作废。
	if to.IsTerminal() {
		voidTicketsWithAudit(m.st, taskID, reason, m.log)
		// 终态即清扫（B119）：与上面的工单作废同一个理由——挂在这里才能覆盖
		// **将来新增的**终态路径。B93 把清扫只加在 handleResult 末尾，Stop 这条
		// 终态路径就漏了，标题写的「落终态即清扫」实际只做到了「回合终态」。
		//
		// 排在作废之后：作废是协调者可见的状态语义，清扫是 best-effort 善后
		// （SweepTaskProcs 每个失败分支只记日志或发 orphan_risk，从不返回错误），
		// 不该挡在语义收口前面。
		//
		// 抑制标记（见 sweptForTerminal 的字段注释）：Done/Stop 已经就地扫过——
		// 它们必须早于 worktree 删除、且带有界重试，本处再扫一遍只是白扫。
		// 本分支管的是**其余**终态路径，那才是 B119 要补的缺口。
		if m.consumeSweepOwned(taskID) {
			m.log.Debug("终态清扫已由 Done/Stop 就地完成，跳过", "task", taskID, "to", to)
		} else {
			m.sweep(taskID)
		}
	}
	return nil
}

// transitBestEffort 容忍 ErrBadTransit 的迁移。
//
// 为什么容忍：reply 回程的 resumeIfIdle 与应答 goroutine 的回迁并发竞争同一迁移，
// CAS 保证只有一个赢家；输家拿 ErrBadTransit 说明目标状态已达成（resumeIfIdle
// 已抢先回迁 running），无需重试也无需告警。其余错误照实记录。
func (m *Manager) transitBestEffort(taskID string, to proto.TaskState, reason string) {
	if err := m.transit(taskID, to, reason); err != nil && !errors.Is(err, store.ErrBadTransit) {
		m.log.Error("状态迁移失败", "task", taskID, "to", to, "reason", reason, "cause", err)
	}
}

// MismatchTransit 返回失配对账扫描（watchdog.scanStateMismatch）的迁移回调包装。
//
// 为什么需要导出：watchdog.go 与 manager.go 同包，扫描本可以直接调 m.transit；
// 但看门狗的接线点在 cmd/agentd.go（agentd 包外），transit 未导出无法从包外引用，
// 于是经这个导出方法把「把任务迁到 failed 并做终态收口」的能力交给 cmd 接线。
// 终态收口（挂起工单作废 + 审计留痕，B63）仍挂在 transit 内部，扫描只负责判定。
func (m *Manager) MismatchTransit() func(taskID string, to proto.TaskState, reason string) error {
	return func(taskID string, to proto.TaskState, reason string) error {
		return m.transit(taskID, to, reason)
	}
}

// restorer 是「agentd 重启后重建执行」的可选 adapter 能力（opencode 实现）。
//
// 为什么不用类型断言外的方案：executor.Adapter 是 Task 3 定稿的五动作契约，
// 为恢复能力加方法会污染 fake 等全部实现；且 fake 的运行态随进程消亡、本就不该
// 支持恢复。把恢复作为可选能力（interface 断言）既保住核心契约，又让
// 「不支持恢复的 adapter 重启后一律按不存活走 failed 恢复路径」成为自然语义。
//
// 恢复的数据类型（ResumeReq/ResumeOutcome/Mode 常量）已随本设计挪到
// internal/executor 包，三个 adapter 与 manager 共用同一套契约。
type restorer interface {
	Resume(executor.ResumeReq) (executor.ResumeOutcome, error)
}

// reaper 是「无内存运行态时按确定性命名兜底回收」的可选 adapter 能力
// （三个真实 adapter 均实现，fake 不实现）。
//
// 为什么单开一个方法而不是让 Stop 自己兜底：Stop 只拿得到 taskID，拿不到 taskDir
// （proc 信息文件在里面）；给 Stop 加参数会改动五动作核心契约、波及 fake 等全部实现。
type reaper interface {
	Reap(taskID, taskDir string) error
}

// volatilePermitter 表示该 adapter 的权限请求随连接消亡：连接一断，executor 侧
// 那次授权请求就永久卡死，重连也救不回（ACP 类适配器，见 grok spec §5.2）。
//
// 不实现本接口的 adapter（如 opencode——权限应答是无状态 HTTP POST，permID 由
// serve 端持有）行为不变。
type volatilePermitter interface {
	PermissionsVolatile() bool
}

// denyReasonInBander 表示该 adapter 能把拒绝理由与裁决同帧送达模型
// （claude：理由进 permDecision.Message，作为 tool_result 正文当场回给模型）。
//
// 不实现本接口的 adapter（grok/codex 的原生协议没有消息字段，opencode 走的
// 老端点会丢弃额外字段，见 spec §2.5）行为不变，仍走 B50 的带外挂起注入。
type denyReasonInBander interface {
	DenyReasonInBand() bool
}

// denyReasonDelivered 判断本任务的 executor 是否已把拒绝理由同帧送达。
//
// 参数：taskID 为任务 id
// 返回：true 表示理由已到模型手里
//
// 注意：返回 true 时调用方**不得**再 noteDenyGuidance——两条路都走会让模型
// 被同一条理由说两遍。解析 adapter 失败时保守返回 false：宁可多说一遍，
// 也不能让理由一句都没送到。
func (m *Manager) denyReasonDelivered(taskID string) bool {
	ad, err := m.adapterFor(taskID)
	if err != nil {
		m.log.Warn("判定拒绝理由送达方式时解析执行者失败，按带外注入处置",
			"task", taskID, "cause", err)
		return false
	}
	d, ok := ad.(denyReasonInBander)
	return ok && d.DenyReasonInBand()
}

// noteDenyGuidanceUnlessInBand 按 executor 的送达能力决定要不要挂起拒绝理由。
//
// 参数：taskID / permID 用于日志定位；reason 为协调者给出的原因
//
// executor 已把理由与裁决同帧送达时直接返回：再挂一份会让模型先在 tool_result
// 里读到理由、下一条 question 时又被同一条理由砸一次。
func (m *Manager) noteDenyGuidanceUnlessInBand(taskID, permID, reason string) {
	if m.denyReasonDelivered(taskID) {
		m.log.Debug("拒绝理由已与裁决同帧送达，跳过带外挂起",
			"task", taskID, "perm", permID)
		return
	}
	// 拒绝原因挂起（B50）：executor 收 reject 会当场终结回合，此刻 Send 会撞上
	// 正在终结的回合；挂起到下一条 question 到达时再下发
	m.noteDenyGuidance(taskID, reason)
}

// ResumeTask 恢复 agentd 重启前已在执行的任务：探测执行器存活；存活则经 adapter
// 重建 SSE 订阅并重启本任务的中介循环（spec §8「存活则重连 SSE 继续」）。
//
// 返回：
//   - true：执行器存活且事件流已重建，任务继续执行
//   - false：执行器已不在（或 adapter 无恢复能力），供 RecoverOnStartup 把任务
//     迁移 failed/waiting_review 交协调者裁决
//
// 注意：
//   - 本方法作为 RecoverOnStartup 的探活闭包传入：存活的「重建订阅 + 重启中介
//     循环」动作封装在闭包内部（见 watchdog.go RecoverOnStartup 的 seam 说明）
//   - 重启前已挂起的权限/提问等待不需要在此重建：reply 回程的 resumeIfIdle
//     自带「回答后无未答工单即回迁 running」的兜底（server.go），协调者回答
//     挂起工单后任务自然恢复执行
//   - 失败的恢复（adapter 报错）返回 false 而非错误：探活闭包契约只有 bool，
//     具体原因已由 Error 日志留痕，恢复路径按不存活处理是保守且安全的选择
//     （宁可交协调者裁决，不可静默吞掉仍在执行的任务事件）
func (m *Manager) ResumeTask(taskID string) bool {
	task, err := m.st.GetTask(taskID)
	if err != nil {
		m.log.Error("恢复读取任务失败", "task", taskID, "cause", err)
		return false
	}
	ad, err := m.adapterFor(taskID)
	if err != nil {
		m.log.Error("恢复读取任务执行者失败", "task", taskID, "cause", err)
		return false
	}
	r, ok := ad.(restorer)
	if !ok {
		m.log.Warn("adapter 不支持执行恢复，任务按不存活处理", "task", taskID)
		return false
	}
	// 权限随连接消亡的 adapter：任务若还挂着未决权限工单，恢复了也永远不会前进
	// （executor 侧那次授权请求已随旧连接卡死，session/load 不会重发），直接判
	// 不可恢复交协调者裁决，而不是建立一条永远不会前进的连接。
	if vp, ok := ad.(volatilePermitter); ok && vp.PermissionsVolatile() {
		pending, err := m.st.PendingTickets(taskID)
		if err != nil {
			m.log.Error("读取未决工单失败，保守判定不可恢复", "task", taskID, "cause", err)
			return false
		}
		if len(pending) > 0 {
			m.log.Warn("任务有未决权限工单且执行者权限随连接消亡，不予恢复",
				"task", taskID, "pending", len(pending))
			return false
		}
	}
	taskDir := filepath.Join(m.cfg.DataDir, "tasks", taskID)
	execName := task.Executor
	if execName == "" {
		execName = m.cfg.Executor.Default
	}
	// env 与 Dispatch 同源：冷恢复重起进程要原样注入（B19），解析失败不阻断
	// 恢复——热重连根本用不上它，冷恢复用不上时由 adapter 侧自行报错
	envKVs, eerr := m.env.For(execName)
	if eerr != nil {
		m.log.Warn("恢复解析 env 失败，按空 env 继续", "task", taskID, "executor", execName, "cause", eerr)
	}
	discBlock, derr := m.discipline.For(execName)
	if derr != nil {
		m.log.Warn("恢复时纪律块读取失败，本次不注入", "task", taskID, "cause", derr)
	}
	// 启动恢复一律 Cold=false：agentd 重启时若有 10 个任务的 executor 已死，
	// 急着冷恢复等于凭空拉起 10 个没人跟它说话的 executor（spec §4）
	out, err := r.Resume(executor.ResumeReq{
		TaskID: taskID, TaskDir: taskDir, RepoPath: task.Workdir(),
		SessionID: task.ExecutorSession, Env: envKVs, Discipline: discBlock.Text, Model: task.Model,
		MarkRoot: prochost.ResolveMarkRoot(task.Workdir(), task.WorktreeManaged), Cold: false,
	})
	if err != nil {
		m.log.Error("重建任务执行失败", "task", taskID, "cause", err)
		return false
	}
	if out.Alive {
		m.log.Info("任务执行已重建，重启中介循环", "task", taskID, "mode", out.Mode)
		go m.mediate(taskID)
	}
	return out.Alive
}
