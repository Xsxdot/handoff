// contracts.go —— 编排域的契约面：出站 client DTO、事件 payload、派发/回收哨兵
// 与 executor 失联对账（B233.26 反转 D1 后自 gateway 的 b23313_retained.go 归域）。
//
// 职责：承载 gateway 包与编排包共享的声明——D1 期它们钉在
// gateway 侧、编排经 agentd.* 引用；反转后契约随提供方落本包，gateway 经
// orchestration.* 反向引用（方向合法）。别名哨兵（ErrRepoUnusable /
// ErrBadDispatchRequest，workspace 正身的旧别名）与转发垫片（RecoverTransit /
// TruncateRunes / Git 三兄弟等）在反转中退役，不再随迁。
//
// 边界：不放 HTTP 面；ReconcileExecutorGone 是本包对「executor 已不在」的
// 唯一收口实现，gateway 的 Server.RecoverOnStartup 包装与 watchdog 复用它。
package orchestration

import (
	"errors"
	"log/slog"

	"github.com/Xsxdot/handoff/internal/prochost"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// —— 派发/登记 DTO（B233.26：随契约家族归域编排包）——

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
	// Receiver 是统一接收者名（载体或小队）。空=使用已确认的默认载体。
	// Ticket 0 只保留字段；解析/准入接线归实现节点。
	Receiver string
	// Carrier 是网关已经绑定的载体名。空=旧调用/未接线；只在 CreateTask 写入，
	// 不得把 cfg.Executor.Default 或 Receiver 名称解析放到 Manager。
	Carrier string
	// Squad 是网关已经绑定的小队名快照。空=载体直派或旧调用；只在 CreateTask 写入，
	// 供任务终态释放成员政策位，不参与 Manager 的接收者解析。
	Squad string
	// HomeDir 是小队派发载体 HOME 的可空透传值；nil=字段缺席，指向空串=显式空值。
	// Ticket 0 只保留字段，执行机覆写行为归实现票 U5。
	HomeDir *string
	// Executor 是任务选择的执行者名；空=缺省（cfg.Executor.Default）。
	Executor string
	// Discipline 是本次派发点名的纪律块**角色名**（如 review）；空=未点名。
	//
	// B229 起：名字只作记录与回显，本机**不再按名字解析任何东西**——正文必须
	// 由协调者侧缝 1 解析组装后经 DisciplineText 下发；点名而正文缺席 = 拒派。
	Discipline string
	// DisciplineText 是协调者侧缝 1 组装好的纪律正文，本机收文即用、落盘随任务
	// 走；执行机不再读本地纪律目录。DisciplineVersion 是命中的账本版本（未点名/
	// 临时正文为 0）。三者关系（B229 契约 §2.5）：正文非空→逐字节注入并落盘；
	// 正文与名字都空→零注入；名字非空而正文空→拒派。
	DisciplineText    string
	DisciplineVersion int
	// Model 是任务级模型覆盖；空=配置 executor.model，再空=executor 自身默认。
	Model string
	// Branch / NewBranch 分支二选一（与 PrepareWorkspace 的 WorkspaceReq 一致）：
	// Branch=切到已存在分支；NewBranch=新建分支（空且 Branch 空=自动 handoff/<id8>）。
	Branch    string
	NewBranch string
	// Base 是新分支起点（仅与 NewBranch/自动分支连用；空=HEAD）。
	Base string
	// ResolveDefaultBase 仅用于卡派发的空基线：在目标项目仓库路径已解析后，
	// 先取 origin/HEAD 指向的默认分支名，再交给既有 D2 补拉到远端尖端。
	// false 时 Base 为空仍保持普通 CLI/--no-sync-check 的 HEAD 语义。
	ResolveDefaultBase bool
	// LocalBaseBranch 表示 Base 是目标机本地工作分支；解析时不得补拉。
	// 与 ResolveDefaultBase 互斥。
	LocalBaseBranch bool
	// Worktree / NewWorktree worktree 二选一：Worktree=用户自带 worktree；
	// NewWorktree=在 DataDir/worktrees 下新建 managed worktree（done 时删除）。
	Worktree    string
	NewWorktree bool
	// BaseCommit 是协调者本地 HEAD 的提交号（40 位十六进制），用于校验任务仓库
	// 不落后于本地；空=不校验（本地派发或调用方 cwd 不是 git 仓库）。
	BaseCommit string
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

// —— 事件 payload（gateway 生产直接发出/消费）——

// TicketAnsweredPayload 是 ticket_answered 事件的 payload。
type TicketAnsweredPayload struct {
	TicketID string `json:"ticket_id"`
	Answer   string `json:"answer"`
}

// ProgressPayload 是 progress 事件的 payload。
type ProgressPayload struct {
	Text string `json:"text"`
}

// CompletedPayload 是 completed 事件的 payload。
type CompletedPayload struct {
	Branch     string `json:"branch"`
	CommitHash string `json:"commit"`
	Summary    string `json:"summary"`
	// FinalText is optional for rolling upgrades: old adapters omit it and old
	// consumers ignore it. A pointer distinguishes an absent field from an
	// explicitly empty final text at the JSON boundary.
	FinalText *string `json:"final_text,omitempty"`
}

// FailedPayload 是 failed 事件的 payload。
type FailedPayload struct {
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

// NewFailedPayload 构造带占用快照的失败载荷。
//
// 参数：reason 为失败原因，原样保留不做改写；branch/commit 为可选的 git 实况，
// 绝大多数失败路径没有（进程退出、对账），传空串即可
func NewFailedPayload(reason, branch, commit string) FailedPayload {
	p := FailedPayload{FailReason: reason, Branch: branch, CommitHash: commit}
	if a := admissionFn(); a.Known {
		p.ProcUsage = &a
	}
	return p
}

// ApproverDecisionPayload 是 approver_decision 事件的 payload：审批者对一次权限
// 请求的裁决结果。Decision 取 approve/escalate/error（error=裁决本身失败）。
//
// B376：Executor 记录最终生效的候选名（failover 后停下的那个），空=未配置/
// 旧路径。additive 字段，omitempty 保证旧事件形态不变。
//
// B405：Model 记录该候选实际使用的模型名；未指定时 = "默认"（ApproverModelDefaultLabel）。
// addit 字段，omitempty 保证旧事件形态不变。
type ApproverDecisionPayload struct {
	TicketID   string `json:"ticket_id"`
	Permission string `json:"permission"`
	Decision   string `json:"decision"`
	Reason     string `json:"reason"`
	ElapsedMS  int64  `json:"elapsed_ms"`
	Executor   string `json:"executor,omitempty"`
	Model      string `json:"model,omitempty"`
}

// —— Dispatch 哨兵（gateway server 层映射为 400/500）——

// ErrWorkdirBusy 表示目标工作目录已被活跃任务占用（编排侧占用守卫，不属于工作区能力）。
var ErrWorkdirBusy = errors.New("目标工作目录已被活跃任务占用")

// ErrExecutorStartFailed 是 Dispatch 启动 executor 失败（adapter.Start 返回错误）
// 的哨兵（gateway server 层映射见 writeDispatchError 的对应分支）。
var ErrExecutorStartFailed = errors.New("启动 executor 失败")

// ErrEnvResolveFailed 表示 env 文件解析失败（文件缺失/语法错/文件名非法）。
var ErrEnvResolveFailed = errors.New("解析 env 文件失败")

// ErrDisciplineResolveFailed 表示纪律块文件不可用（文件名非法/读不到/超限）。
var ErrDisciplineResolveFailed = errors.New("纪律块解析失败")

// —— reclaim 哨兵（gateway server 层映射为 409/500）——

var (
	// ErrReclaimNotTerminal 表示任务还没到终态，不予回收——删运行中任务的
	// 工作树等于抽它脚下。
	ErrReclaimNotTerminal = errors.New("任务非终态，不回收工作树")
	// ErrReclaimRepoUnreachable 表示仓库不可达或不是 git 仓库，判不出。
	// 单任务回收必须据此拒绝，绝不能降级成「无残留」静默成功。
	ErrReclaimRepoUnreachable = errors.New("仓库不可达，工作树状态判不出")
	// ErrReclaimNotManaged 表示该任务用的是协调者自带的工作树，agentd 无权删。
	ErrReclaimNotManaged = errors.New("工作区不是 agentd 管理的 worktree")
)

// ReconcileExecutorGone 收尾一个 executor 已不在的任务。
//
// 参数：
//   - sweep: 残留进程清扫回调；**无条件调用**，与任务状态无关（why 见下）
//
// 为什么清扫是无条件后置动作：本函数对非 running/waiting_answer 的状态提前返回，
// 那条分支的理由是「待审核终态与已终结态不需要状态收尾」——它说的是状态，不是
// 资源。executor 已经不在了，残留进程该不该收与任务停在哪个状态无关；已经 done
// 过的任务同样可能有残留（那次 Kill 正因锁已释放而空转）。2026-08-12 事故里两个
// 任务最终都停在 waiting_review，恰好是提前返回会跳过的形态。
//
// 顺序：状态收尾在前、清扫在后。协调者的工作流（任务进 waiting_review）不受
// 清扫成败影响——清扫失败只上报，绝不回头改状态。
//
// B233.26：实现体随契约家族归域本包（依赖的 VoidTicketsWithAudit/recoverTransit/
// NewFailedPayload/Hub 同包直调，gateway 侧经 orchestration.* 反向引用）。
func ReconcileExecutorGone(st *store.Store, hub *Hub, taskID, reason string,
	log *slog.Logger, sweep func(taskID string)) proto.TaskState {

	cur, err := st.GetTask(taskID)
	if err != nil {
		log.Error("对账读取任务失败", "task", taskID, "reason", reason, "cause", err)
		return ""
	}
	log.Info("executor 已不在，开始对账", "task", taskID, "state", cur.State, "reason", reason)
	if cur.State != proto.TaskStateRunning && cur.State != proto.TaskStateWaitingAnswer {
		log.Info("任务无需状态对账，仅清扫残留", "task", taskID, "state", cur.State)
		sweep(taskID) // 无条件：见上方 why
		return cur.State
	}

	// 复用终态收口的同一个助手（B63）：这条路径迁的是 waiting_review（非终态），
	// 走不到 transit 的终态分支，但「executor 已死 ⇒ 挂起工单不可能再被回答」的
	// 语义与终态一致，审计痕迹也该一致
	VoidTicketsWithAudit(st, taskID, reason, log)
	// 先迁状态、后追加事件（B97）：turn_failed 事件一落库就可被 WS 重放读到，状态
	// 必须先就位，否则协调者看到 turn_failed 后立刻 continue/done 会被状态机 409
	// 拒。反转的代价是失败形态从「状态错」变成「状态对、事件缺」：迁失败就不追加
	// 事件，任务停在旧状态可重试；崩在两步之间留下的是「waiting_review 但缺一条
	// turn_failed」，协调者 show 出来仍可裁决——旧形态「事件说回合失败、状态还是
	// running」只会让操作被拒、干等到 2h 看门狗（handleResult / transitFailedWithEvent
	// 函数头的同一条理由）。
	if err := recoverTransit(st, taskID, cur.State); err != nil {
		log.Error("对账迁移 waiting_review 失败，不追加 turn_failed 事件", "task", taskID, "cause", err)
		sweep(taskID)
		return cur.State
	}
	// 对账路径没有 git 实况可带（executor 已不在，查不了回合起点）。
	//
	// 类型是 turn_failed 而不是 failed（B100 补漏）：本函数迁的是
	// **waiting_review**（上面的 recoverTransit），任务**没有终结**——executor 死了
	// 但代码还在，值得让协调者 diff 完再决定 continue 还是 done。落 failed 会让
	// wait --follow 收流、打「任务已终结」并以 0 退出，把一个正等着裁决的任务
	// 报成死的。B100 首轮漏了这条：它的 spec 把这一行误记成「任务落 failed」，
	// 没去看 recoverTransit 的实际迁移目标（见 watchdog.go 里 transitFailedWithEvent
	// 的注释，那里明写着「reconcileExecutorGone 收的是 waiting_review」）。
	evt, err := st.AppendEvent(taskID, proto.EventTypeTurnFailed, NewFailedPayload(reason, "", ""))
	if err != nil {
		log.Error("对账追加 turn_failed 事件失败（状态已迁 waiting_review）", "task", taskID, "cause", err)
		sweep(taskID) // 事件没发成不代表 executor 还活着，残留照收
		return cur.State
	}
	hub.Publish(evt)
	log.Info("对账完成", "task", taskID, "from", cur.State, "to", proto.TaskStateWaitingReview)
	sweep(taskID)
	return proto.TaskStateWaitingReview
}
