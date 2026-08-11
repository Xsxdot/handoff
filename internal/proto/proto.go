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
)

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

// Repo 是一条「执行机 × 仓库」登记：把该执行机上一个已落地的 git 仓库
// 与一个短名字绑定，使 dispatch 不必再写完整路径。
//
// 字段：
//   - Name: 登记名（每台执行机内唯一），dispatch 时可用作 --repo 的取值
//   - Path: 该执行机上仓库的绝对路径
//   - OriginURL: 仓库的 origin 地址，dispatch 省略 --repo 时据此自动匹配
//   - CreatedAt: 登记时间
//   - Status: repo ls 时**现场探得**的实际状态（"有效"/"路径不存在"/"不是 git 仓库"），
//     不落库，仅列表响应携带——它是登记与文件系统漂移的可见化手段
type Repo struct {
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	OriginURL string    `json:"origin_url"`
	CreatedAt time.Time `json:"created_at"`
	Status    string    `json:"status,omitempty"`
}
