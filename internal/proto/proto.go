// Package proto 是 handoff 协议类型的唯一定义处。
//
// 职责：
//   - 定义任务状态（TaskState）、事件类型（EventType）及任务/事件/工单（Ticket）数据结构
//   - 提供任务状态机迁移合法性校验（CanTransit）
//
// 边界：
//   - 纯类型包：无 I/O、无业务逻辑、无外部依赖
//   - 不负责持久化、事件派发、状态变更执行等行为
package proto

import (
	"encoding/json"
	"time"
)

// TaskState 表示任务所处状态。
type TaskState string

const (
	TaskStatePending       TaskState = "pending"
	TaskStateRunning       TaskState = "running"
	TaskStateWaitingAnswer TaskState = "waiting_answer"
	TaskStateWaitingReview TaskState = "waiting_review"
	TaskStateCompleted     TaskState = "completed"
	TaskStateFailed        TaskState = "failed"
)

// TerminalStates 是任务的两个终态：到此不再有 executor 持有工作区。
// 存储层按它生成「非终态」查询条件，避免与状态机定义漂移。
var TerminalStates = []TaskState{TaskStateCompleted, TaskStateFailed}

// IsTerminal 报告该状态是否为终态（completed / failed）。
func (s TaskState) IsTerminal() bool {
	return s == TaskStateCompleted || s == TaskStateFailed
}

// EventType 表示任务产生的事件类型。
type EventType string

const (
	EventTypePermissionRequest EventType = "permission_request"
	EventTypeQuestion          EventType = "question"
	EventTypeProgress          EventType = "progress"
	EventTypeCompleted         EventType = "completed"
	EventTypeFailed            EventType = "failed"
	EventTypeStalled           EventType = "stalled"
	// EventTypeDeliveryFailed 表示审核者的应答已落库但没能送达 executor。
	//
	// 为什么必须是一类事件而不只是日志：应答未送达时 executor 仍原地阻塞，
	// 而工单已被消耗、不再出现在挂起项里——若只写日志，审核者这边完全无感，
	// 任务会一直挂到看门狗超时。作为事件产出才能唤醒审核者去执行 handoff resume。
	EventTypeDeliveryFailed EventType = "delivery_failed"
	// EventTypeApproverDecision 表示分级审批链中廉价模型审批者对权限请求的裁决
	// 结果（approve/escalate/error）。只入库做审计（show 可见），不唤醒审核者——
	// approve 路径已自动放行、escalate 路径由紧随其后的 permission_request 唤醒。
	EventTypeApproverDecision EventType = "approver_decision"
	// EventTypeApproverDisabled 表示本任务连续多次裁决失败（fail-closed），审批链
	// 已停用，后续权限请求一律直接升级人工审核者，不再浪费一次注定失败的裁决调用。
	EventTypeApproverDisabled EventType = "approver_disabled"
	// EventTypePermissionReuse 表示一次权限请求命中了本任务内**同一权限描述**的
	// 既有人工批准，被自动放行而没有再次叫醒审核者（B57②）。
	// 复用必须留痕，否则「我明明没批过这个」将无从对质。
	EventTypePermissionReuse EventType = "permission_reuse"
	// EventTypeDenyGuidanceRelayed 表示审核者拒绝时给出的原因已作为一条消息
	// 下发给 executor（B50）。
	EventTypeDenyGuidanceRelayed EventType = "deny_guidance_relayed"
	// EventTypeDenyGuidanceDropped 表示拒绝原因没能下发——回合在下一条提问到达前
	// 就终结了。审核者据此知道要用 continue 自己把话带上。
	EventTypeDenyGuidanceDropped EventType = "deny_guidance_dropped"
	// EventTypeTicketsVoided 表示任务终结时把剩余挂起工单一并作废了（B63）。
	//
	// 为什么必须留痕：pending_tickets 是审核者接管陌生会话时「我还欠哪些没答」
	// 的权威清单，工单凭空消失与工单凭空挂着一样难排查——show 里要能回答
	// 「那张单是何时、因为什么被作废的」。
	//
	// **只入库不 Publish**，且在客户端不可交付（见 client.isDeliverable）：它与
	// completed/failed 同时刻产生，可交付就会抢走一次性 wait 的收手权。
	EventTypeTicketsVoided EventType = "tickets_voided"
	// EventTypeArchived 是任务被 done 归档时追加的终态事件，payload 为 ArchivedPayload。
	//
	// 为什么归档需要一条自己的事件：在此之前 Done 只做状态迁移、不追加任何事件，
	// 跟随中的 wait --follow 只能从「订阅被关掉了」间接推断任务结束。等待方要判断
	// 「这个任务做完没有」时，事件流里根本没有可等的东西（B68）。
	//
	// 注意：本事件**唤醒 wait**（与 progress / approver_decision 那类只入库的事件不同）,
	// README 与 handoff skill 的事件表必须同步列出它。
	EventTypeArchived EventType = "archived"
)

// Usage 是任务当前的 context 占用快照。
//
// 「当前占用」= 最后一次模型调用的输入侧（含缓存命中），**不是**回合或会话的
// 累加。两者差别巨大：实测一个 4 次模型调用的 grok 回合，累加值是真实占用的
// 4 倍，且工具调用越多越离谱，长回合会超过 100%（探针笔记 §4.2）。
//
// 边界：本结构只描述「占用」，不描述「消耗」。累计 token 与花费是另一个口径，
// 将来以新增字段的形式加进来，形状不变、不需要重新设计。
type Usage struct {
	// ContextTokens 是当前 context 占用的 token 数。永远 > 0——取不到时整个
	// Usage 为 nil，不用 0 冒充「没用 token」（B69/B70 纪律）。
	ContextTokens int `json:"context_tokens"`
	// ContextWindow 是该模型的上下文窗口上限（百分比的分母）。
	// nil = 该 executor 不在协议里报窗口（claudecode / opencode），此时界面
	// 只显绝对值。**绝不由 handoff 猜**：猜错是静默错误，百分比照常显示只是错的。
	ContextWindow *int `json:"context_window,omitempty"`
}

// CostState 是花费的可信度。
//
// 取值范围**分两级**：单条账目（SpendEntry / ledger 行）只可能是
// CostReported / CostEstimated / CostUnknown；CostPartial 只在**求和之后**
// 产生（部分行有花费、部分行没有），任何 adapter 都不会产出它。
// 别去找「哪个 adapter 报 partial」——没有。
type CostState string

const (
	// CostReported：执行器自报了花费且完整。
	CostReported CostState = "reported"
	// CostEstimated：执行器不报花费，由 handoff 按 API 牌价估算（只有 codex）。
	CostEstimated CostState = "estimated"
	// CostPartial：**仅聚合级**。有已知部分，但有调用没拿到花费——所以它是
	// **下界**，真实值只会更高。展示时必须能读出这一点。
	CostPartial CostState = "partial"
	// CostUnknown：一次都没拿到。展示成「—」，**绝不是 $0.00**：
	// 花费的缺席意味着 "unreported or incomplete, never free"。
	CostUnknown CostState = "unknown"
)

// Cost 是累计花费及其可信度。
//
// 注意：State 为 CostPartial 时，Ticks 只是**已知部分**的和，是下界不是总额。
type Cost struct {
	// Ticks 是花费，单位 1 USD = 10^10 ticks。
	//
	// 为什么用整数 ticks 而不是浮点美元：grok 原生就给 ticks，且它的文档明说
	// 浮点求和对不上服务端的账。统一整数累加，只在展示的最后一步转美元。
	Ticks int64 `json:"ticks"`
	// State 见 CostState 的注释。CostUnknown 时 Ticks 恒为 0。
	State CostState `json:"state"`
}

// Cumulative 是任务的累计消耗快照。
//
// 与 Usage 的区别（**改错了不会报错，只会显示错的数**）：Usage 描述
// 「现在占用多少 context」（最后一次模型调用的输入侧），本结构描述
// 「这个任务一共烧了多少」（跨全部调用累加）。两者数量级差几倍到几十倍，
// 不要因为字段名像就互相赋值。
//
// 边界：本结构由 Store.TaskCumulative 对 task_usage_ledger 求和产出，
// 只在**单任务读取**时填充；列表接口不填（见 Store.ListTasks 的注释）。
type Cumulative struct {
	// InputTokens 是未命中缓存的输入（口径见 Store.UpsertSpend 的注释）。
	InputTokens int `json:"input_tokens"`
	// CachedTokens 是命中缓存的输入（读缓存 + 写缓存）。
	CachedTokens int `json:"cached_tokens"`
	// OutputTokens 是模型产出，含 reasoning。
	OutputTokens int `json:"output_tokens"`
	// TotalTokens 是上面三项之和，由 store 算好，前端不再自己加。
	TotalTokens int `json:"total_tokens"`
	// Cost 是累计花费；nil = 还没有任何一条账目带花费信息。
	Cost *Cost `json:"cost,omitempty"`
}

// SpendEntry 是一条待入账的消耗（adapter 产出，store 消费）。
//
// Key 必须在同一个任务内**稳定且唯一**——它是幂等的全部依据。同 Key 重复上报
// 按**覆盖**处理（不是累加），所以流式增长的值可以放心重复报：
// opencode 对同一条 message 会随生成推很多次、id 相同而 tokens 在涨，
// 覆盖天然取到最终值；重复推同值则是无操作。
type SpendEntry struct {
	Key          string
	InputTokens  int
	CachedTokens int
	OutputTokens int
	CostTicks    int64
	// CostState 只能是 CostReported / CostEstimated / CostUnknown 三者之一。
	CostState CostState
}

// Task 表示一个 handoff 任务。
//
// JSON 线格式契约（CLI wait/tasks/attach 输出与 server WS/REST 共用此结构，
// key 必须小写——上层脚本按 {"id":..,"state":..,"created_at":..} 解析）。
type Task struct {
	ID              string    `json:"id"`
	Target          string    `json:"target"`
	RepoPath        string    `json:"repo_path"`
	Branch          string    `json:"branch"`
	PlanPath        string    `json:"plan_path"`
	PlanSummary     string    `json:"plan_summary"`
	ExecutorSession string    `json:"executor_session"`
	State           TaskState `json:"state"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	// Name 是任务的展示名（dispatch --name 或从 plan/prompt 派生）。
	Name string `json:"name"`
	// Executor 是任务选择的执行者（dispatch --executor）；空=缺省执行者（老任务兼容）。
	Executor string `json:"executor"`
	// Model 是任务级模型覆盖（dispatch --model）；空=executor 自身默认。
	Model string `json:"model"`
	// WorkDir 是任务工作区目录。空=原地模式（工作区即 RepoPath，由 Workdir() 统一回退）。
	// 审阅命令（diff/fetch/run）与 executor 的 cwd 都从这里取值，不得直接读 RepoPath。
	WorkDir string `json:"work_dir"`
	// WorktreeManaged 表示 WorkDir 是 agentd 创建的 worktree，任务完成（done）时由
	// agentd 负责删除；用户自带 worktree（Worktree=false）或原地模式均不受管理。
	WorktreeManaged bool `json:"worktree_managed"`
	// BaseCommit 是本任务新分支的**实际起点**（40 位 sha）；空=切已存在分支
	// （没有起点这回事）或老任务（该列后加，不回填、不编造）。
	// 它回答的是「这个任务建在哪个提交上」——B35 之前这个问题无处可问。
	BaseCommit string `json:"base_commit"`
	// BaseAhead 是派发当时任务仓库 HEAD 领先 BaseCommit 的提交数：这些提交
	// 不在任务分支里。0 表示起点就是仓库 HEAD，或该数字当时没能算出来。
	BaseAhead int `json:"base_ahead"`
	// RepoDirtyCount 是派发当时任务仓库未提交改动的**总数**（含未跟踪文件）；
	// 0=干净，或本次不是 managed（--new-worktree）模式。这些改动不在新工作树
	// 里，executor 看不到它们。
	RepoDirtyCount int `json:"repo_dirty_count"`
	// RepoDirtyFiles 是上述改动的文件名展示串（逗号分隔，封顶 5 个，超出补
	// 「等 N 处」）；服务端截断后的展示用字段，与 PlanSummary 同形，不供程序消费
	//（要精确条数请读 RepoDirtyCount）。
	RepoDirtyFiles string `json:"repo_dirty_files"`
	// DoneNote 是归档时审核者留下的完成说明（handoff done --note）；空串=未留说明
	// 或该任务归档于本功能上线之前。它回答的是「这次到底做完了什么、为什么放行」——
	// 归档之后除了一个 completed 状态位，此前没有任何地方记录这件事。
	DoneNote string `json:"done_note"`
	// ActualModel 是 executor 报回的**实际**模型名；空=执行者还没报（回合未
	// 开始）或该任务跑在不报模型名的旧版执行者上。
	//
	// 它与 Model 是两件事：Model 是 dispatch --model 发下去的**入参**（常为空，
	// 意思是「用执行者自己的默认」），ActualModel 是执行者实际在用的那个。
	// 二者不一致时以 ActualModel 为准，界面不并列显示。
	ActualModel string `json:"actual_model,omitempty"`
	// Usage 是当前 context 占用；nil=还没有任何一次模型调用完成。
	Usage *Usage `json:"usage,omitempty"`
	// Cumulative 是任务的累计消耗；nil = 没有任何账目（或本次是列表读取，
	// 列表不填充——见 Store.ListTasks）。与 Usage 是两个口径，别混。
	Cumulative *Cumulative `json:"cumulative,omitempty"`
	// Machine 是这条任务所在的机器：""=本机；否则为**本机** cfg.Targets 的键。
	//
	// 线注解，不入库（存储层不读不写这一列）：它由汇总方在响应时盖章，
	// 语义是「我从哪个 target 拉来的」。
	//
	// 为什么不复用 Target：`target` 存的是「当年派发它的那个 CLI 管这台机器叫
	// 什么」——换一台笔记本、换一份配置派发，同一台机器可以叫不同名字，它是
	// 历史记录不是路由键。透明路由与 UI 的机器筛选必须锚在本机配置上。
	Machine string `json:"machine"`
	// ProjectID 是任务的项目归属：读时按 repo_path 与 project_locations.path
	// 等值 join 得到（W3a §1.3），未归属为 ""。
	//
	// 线注解，不入库：tasks 表不加这一列——历史任务或已注销项目的任务应当
	// 诚实显示「未归属」，而不是一列陈旧数据说谎。
	ProjectID string `json:"project_id"`
}

// MaxDoneNoteBytes 是归档说明的字节上限。
//
// 为什么超限要报错而不是截断：B6 的教训正是「静默截断让审核者盲信自己看到的是
// 全文」。审核者写了 6KB 说明、系统悄悄存 4KB，比直接拒绝糟糕得多。
// 取值 4096：比一句话说明宽出两个数量级，同时挡住「把整个 diff 粘进来」的误用。
const MaxDoneNoteBytes = 4096

// ArchivedPayload 是 EventTypeArchived 的事件负载。
//
// 为什么定义在 proto 而不是 agentd：CLI 侧（B67 与任何解析事件流的脚本）要读
// Note，放在 agentd 包里会逼两边各写一份结构体，形态一漂就是解析不出来。
// 这与只在 agentd 内部使用的 progressPayload 情况不同。
type ArchivedPayload struct {
	Note string `json:"note"`
}

// Workdir 返回 executor cwd 与审阅命令的统一取值点：WorkDir 非空返回它
// （worktree 模式），否则返回 RepoPath（原地模式）。
//
// 注意：所有需要「任务在哪个目录工作」的代码必须走本方法，直接读 RepoPath
// 会在 worktree 模式下拿到错误的工作目录。
func (t *Task) Workdir() string {
	if t.WorkDir != "" {
		return t.WorkDir
	}
	return t.RepoPath
}

// TaskView 是 Task 的 API 视图：任务本体 + 不落库的运行态。
//
// 为什么用嵌入而不是给 Task 加字段：Watchers 是 agentd 内 Hub 的瞬时状态，
// 与任务的持久身份无关。加进 Task 会让存储层背一个它不该知道的概念，迟早有人
// 把它写进 SQLite；嵌入则让存储结构保持纯粹，同时 JSON 字段提升后线格式与旧版
// 逐字节兼容——只多一个 watchers 键，老客户端解码不受影响。
//
// 注意：Watchers 是服务端应答那一刻的快照，不做任何时效承诺。
type TaskView struct {
	Task
	// Watchers 是当前订阅该任务事件流的连接数（几个审核者在听）。
	// 0 不一定是异常：waiting_review 与终态本来就不需要有人盯，判据见
	// handoff status 的 unattended。
	Watchers int `json:"watchers"`
}

// Event 表示任务生命周期中产生的一条事件记录。
//
// JSON 线格式契约（wait 命令输出与 WS 推送共用此结构）：{"seq":..,"task_id":..,
// "type":..,"payload":{..},"created_at":..}，key 必须小写（上层脚本按此解析）。
type Event struct {
	Seq       int64           `json:"seq"`
	TaskID    string          `json:"task_id"`
	Type      EventType       `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

// Ticket 表示一次需人工介入的请求，Kind 取 "gate"（许可门）或 "ask"（提问）。
//
// JSON 线格式契约（attach 输出 pending_tickets 与 REST 响应共用此结构，
// key 必须小写）：{"id":..,"task_id":..,"kind":..,"request":{..},"answer":..,..}。
type Ticket struct {
	ID         string          `json:"id"`
	TaskID     string          `json:"task_id"`
	Kind       string          `json:"kind"`
	Request    json.RawMessage `json:"request"`
	Answer     *string         `json:"answer"`
	CreatedAt  time.Time       `json:"created_at"`
	AnsweredAt *time.Time      `json:"answered_at"`
	// DeliveredAt 是应答送达 executor 的时刻；非 nil 才代表 executor 真的收到了。
	// 与 AnsweredAt 分开记录：「审核者已裁决」与「裁决已送达」是两件事实，
	// 合并会让中继失败后无从判断该不该重投（见 Manager.RecoverStuck）。
	DeliveredAt *time.Time `json:"delivered_at"`
	// Fingerprint 是 gate 工单的裁决指纹：权限描述全文的 sha256 十六进制串。
	// 它让「审核者是不是已经就同一件事表过态」成为一次索引查询而不是全表扫文本。
	// ask 工单不参与复用，留空。
	Fingerprint string `json:"fingerprint"`
}

// transitTable 是任务状态机迁移表，key 为来源状态，value 为允许迁移到的状态集合。
var transitTable = map[TaskState][]TaskState{
	TaskStatePending: {TaskStateRunning, TaskStateFailed},
	TaskStateRunning: {TaskStateWaitingAnswer, TaskStateWaitingReview, TaskStateCompleted, TaskStateFailed},
	// failed 允许回到 running：任务失败后可人工重试，状态机应支持重新开始而非死锁。
	TaskStateWaitingAnswer: {TaskStateRunning, TaskStateFailed},
	TaskStateWaitingReview: {TaskStateRunning, TaskStateCompleted, TaskStateFailed},
	TaskStateCompleted:     {},
	TaskStateFailed:        {TaskStateRunning},
}

// CanTransit 校验状态迁移是否合法（如 completed 不可回 running）。
//
// 参数：
//   - from: 来源状态
//   - to: 目标状态
//
// 返回：
//   - true 表示迁移合法，false 表示不允许该迁移
//
// 注意：
//   - 未在迁移表中登记的状态一律视为不可迁移
//   - 本函数仅做静态校验，不实际变更状态
func CanTransit(from, to TaskState) bool {
	for _, allowed := range transitTable[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// ProjectLocation 是一条「项目 × 机器」位置记录：项目在**这一台**机器上的
// 那一个工作副本。
//
// 模型（B62）：
//   - 项目（project）：一份代码的逻辑身份，与机器无关，由 ProjectID 标识
//   - 位置（location）：项目在某一台机器上的工作副本，由 Path 标识
//   - ADR-0008：一台机器上一个项目**最多一个位置**，由 ProjectID 做主键强制
//
// 字段：
//   - ProjectID: sha256(归一化 origin) 前 16 位；**纯函数派生**，每台机器各算
//     各的，同一个 origin 必然得到同一个值——跨机引用因此不需要任何协调
//   - Name: 人可读引用（每台机器内唯一），由 origin 末段派生，冲突时补 -2；
//     只用于 --project <名字> 与 project rm <名字>，**不参与身份判定**
//   - Path: 该机器上的绝对路径（登记时 Abs+Clean，且已归并到主工作树）
//   - OriginURL: agentd 在该机器上**现读**的权威值，不采信调用方上送的字符串
//   - CreatedAt: 登记时间
//   - Status: project ls 时**现场探得**的实际状态（"有效"/"路径不存在"/
//     "不是 git 仓库"），不落库，仅列表响应携带——它是登记与文件系统漂移的
//     可见化手段
type ProjectLocation struct {
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	OriginURL string    `json:"origin_url"`
	CreatedAt time.Time `json:"created_at"`
	Status    string    `json:"status,omitempty"`
}
