// 账本域的数据类型与受控词表。字段与 spec §2.1 DDL 一一对应；
// 状态骨架锚点、关系类型、事件类型的字符串字面量以本文件为唯一定义点。
package ledger

import (
	"encoding/json"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// 状态骨架锚点（workflow 自定义状态插在锚点之间；终止不在 States 序列里，
// 它经 CloseCard 从任意非终态进入，带 reason）。
const (
	StatusTodo   = "待办"
	StatusDoing  = "进行中"
	StatusReview = "待审阅"
	StatusDone   = "已完成"
	StatusClosed = "终止"
)

// 终止 reason 受控词表。
const (
	CloseCancelled = "取消"
	CloseAbandoned = "废弃"
	CloseShelved   = "搁置" // 唯一可复活的终止
)

// 派发用途。审阅轮只读、跑在工作分支上，不新开分支——WorkBranch 靠它
// 把审阅轮排除在「卡的分支」之外。
const (
	PurposeImplement = "implement"
	PurposeReview    = "review"
)

// 关系类型。merged_into 不许经 AddRelation 直建，必须走 MergeCards
// （因为要做基线/链式校验）。
const (
	RelBlocks         = "blocks"
	RelMergedInto     = "merged_into"
	RelDiscoveredFrom = "discovered_from"
	RelSplitFrom      = "split_from"
	RelRelates        = "relates"
)

// 事件类型（card_events.type）。task_mirrored 由 Plan B 镜像子系统写入，
// 这里先占词表位。
const (
	EvCardCreated   = "card_created"
	EvStatusMoved   = "status_moved"
	EvDispatched    = "dispatched"
	EvReviewVerdict = "review_verdict"
	EvMerged        = "merged"
	// EvBranchMerged 合并环节把工作分支合进基线并推 origin。与 EvMerged
	// （卡并入承载卡）是两回事，不可复用。
	EvBranchMerged       = "branch_merged"
	EvUnmerged           = "unmerged"
	EvSplit              = "split"
	EvAcceptanceRecorded = "acceptance_recorded"
	EvComment            = "comment"
	EvNeedsHuman         = "needs_human"
	EvNeedsCleared       = "needs_cleared"
	EvDecisionOpened     = "decision_opened"
	EvDecisionAnswered   = "decision_answered"
	EvTaskMirrored       = "task_mirrored"
	EvWorkflowMigrated   = "workflow_migrated"
	EvDriverTakeover     = "driver_takeover"
	// EvRoomMessage 协作房间域（B156.2）的唯一内容事件；kind 受控词表在
	// proto.RoomMsgKind*。卡会话消息 CardID=卡号；项目群/全员群消息
	// CardID=""（无卡事件——follow.go 现状把项目级事件排除在多路 wait 外，
	// executor 因此零感知，这是刻意的）。载荷 schema 见 proto.RoomMessage。
	EvRoomMessage = "room_message"
	// EvMessageConsumed 房间消息被恰好一次消费的落账标记。消费判定在账本
	// 同一 mutate 事务内查后写（照 ClearNeedsHumanFrom 同形），权威在事件
	// 存在性；会话侧游标只是缓存。
	EvMessageConsumed = "message_consumed"
)

// WorkflowTarget 是跨流迁移的显式目标。Version==0 表示在迁移事务内取目标流最新版。
// Status 必须显式提供，迁移不根据同名列自动映射。
type WorkflowTarget struct {
	Name    string
	Version int
	Status  string
}

// WorkflowLocation 是卡在某个工作流版本和状态列上的位置。
type WorkflowLocation struct {
	Workflow string
	Version  int
	Status   string
}

// WorkflowMigration 描述一次跨流迁移及其审计事件。
type WorkflowMigration struct {
	CardID string
	From   WorkflowLocation
	To     WorkflowLocation
	Event  Event
}

// Attachment 卡的附件引用。Path 是相对 docs/superpowers/ 的规范形 git 路径。
type Attachment struct {
	Kind string `json:"kind"` // spec|plan|doc
	Path string `json:"path"`
}

// Card 任务卡。字段语义见 spec §2；零值时间用 IsZero 判空。
type Card struct {
	ID                 string       `json:"id"`
	Title              string       `json:"title"`
	Status             string       `json:"status"`
	TerminateReason    string       `json:"terminate_reason,omitempty"` // 仅 Status==终止 时非空
	Priority           string       `json:"priority"`                   // 高|中|低
	Project            string       `json:"project"`
	ParentID           string       `json:"parent"`
	WorkflowName       string       `json:"workflow"`
	WorkflowVersion    int          `json:"workflow_version"`
	Attachments        []Attachment `json:"attachments,omitempty"`
	AcceptanceCriteria string       `json:"acceptance_criteria,omitempty"`
	BaseBranch         string       `json:"base_branch,omitempty"` // 空 = 继承祖先/项目主线（EffectiveBaseBranch 解析）
	DriverSession      string       `json:"driver_session,omitempty"`
	DriverHeartbeatAt  time.Time    `json:"driver_heartbeat_at,omitempty"` // 兼容列名；语义是认领时刻，不是续租心跳
	CreatedAt          time.Time    `json:"created_at"`
	UpdatedAt          time.Time    `json:"updated_at"`
}

// Relation 类型化关系边。JSON tag 服务直接编码账本结构的 CLI；HTTP 详情使用
// proto 投影并刻意保留 PascalCase 线格式。
type Relation struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
}

// Event 账本单流事件。镜像事件三个 Source 字段非空，其余事件为空。
type Event struct {
	Seq          int64           `json:"seq"`
	CardID       string          `json:"card_id"` // 空 = 项目级事件（如项目级裁决）
	Type         string          `json:"type"`
	Actor        string          `json:"actor"`
	Payload      json.RawMessage `json:"payload"`
	SourceTarget string          `json:"source_target,omitempty"`
	SourceTask   string          `json:"source_task,omitempty"`
	SourceSeq    int64           `json:"source_seq,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

// Gate workflow 转移进入某状态前的门条件。
type Gate struct {
	RequireAttachment string `json:"require_attachment,omitempty"` // 附件 kind 非空集
	// RequireAttachmentAny 择一门：卡带其中**任意一种** kind 的附件即放行。
	//
	// 存在的理由是一条列序服务多条路径时，单值门必然顾此失彼（B226）：
	// charter 流的 implement 列，L2 走 spec→plan→implement（有 plan 无
	// breakdown），L3 轻档走 contract→breakdown→implement（有 breakdown
	// 无 plan）。门真正要保证的是「有一份可执行的工作单」，而不是「那份
	// 工作单叫什么名字」。
	//
	// 与 RequireAttachment 是 **AND** 关系：两个都设就两个都要过。
	// 空 slice 等同未设。
	RequireAttachmentAny []string `json:"require_attachment_any,omitempty"`
	RequireAcceptance    bool     `json:"require_acceptance,omitempty"` // 验收判据非空
	// RequireChildrenDone 聚合闸：全部**直接**子卡已完结（已完成或终止）
	// 才许进入本列。无子卡时空洞为真——同一工作流复用给不扇出的卡时，
	// 这张卡不该被自己用不上的闸卡住。终止也算完结是刻意的：被取消的
	// 子卡不该把父卡永远堵死，取舍权在看错误清单的人。
	RequireChildrenDone bool `json:"require_children_done,omitempty"`
}

// NodeOverride 节点对所引模板的单字段覆盖；零值字段 = 沿用模板的值。
//
// why 要「引模板 + 覆盖」而不是节点内联全部字段：executor / target / model
// 这几样在同一条流里高度重复，内联会让「换一台执行机」变成挨个节点改；
// 而只引模板又满足不了「审阅这一列想换个执行者」这种单点微调。
type NodeOverride struct {
	Executor   string `json:"executor,omitempty"`
	Discipline string `json:"discipline,omitempty"` // 具名纪律块名，如 review / finishing
	Target     string `json:"target,omitempty"`
	Model      string `json:"model,omitempty"`
	// Squad 是本节点绑定的执行者小队名（B156.3）。绑定后目标机/执行者/模型
	// 由编制域按小队成员载体解析，显式 Target/Executor/Model 仍可一次性覆盖。
	// 存量直绑节点不填此字段，语义不变（契约 §5：小队是解析层，不逼迁移）。
	Squad string `json:"squad,omitempty"`
	// Purpose 覆盖模板的派发用途（implement / review / ...）。
	//
	// why 用途必须能按节点覆盖：模板是**复用物**（一条流的十个节点常引同一份
	// 模板），而用途是**这一列要干什么**。派发期有四处行为按用途裁决——分支
	// 命名、审阅轮的基线取工作分支、卡的工作分支归属（WorkBranch 跳过审阅
	// 轮）、重跑轮次挂号——节点拿不到自己的用途时，这四处会一起判错。
	// 2026-08-22 真机实测：charter 流 review 节点引的是 purpose=charter 的
	// 通用模板，于是审阅轮从卡基线开了条新分支，执行者在空分支上把实现又写
	// 了一遍，等于从未审阅过工作分支（B183）。
	Purpose string `json:"purpose,omitempty"`
}

// NodeOutput 是节点在本轮必须写出的单一附件声明。
// Kind 复用附件白名单；Path 是仓内相对路径模板，由派发前渲染。
// nil 表示该节点不声明产出，保持旧工作流行为。
type NodeOutput struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

// NodeDef 工作流的一个节点：看板的一列 + 卡走到这列时的执行规矩。
//
// 设计要点（用户 2026-08-21 定死，改动前先回看 spec）：
//   - **没有预设节点类型**。「审阅」「合并」不是内置语义，而是下面几个能力
//     开关的组合结果，用户可以随意重组。
//   - **节点只配「怎么干」（纪律、执行者、开关），不配「干什么」**。合并目标
//     这类每张卡都不同的值来自卡本身（有效基线分支），不写死在节点上。
//   - **路由用节点名指向而不是数组下标**，为将来的 DAG 分叉预留形状；本轮
//     只实现线性消费。
type NodeDef struct {
	Name     string       `json:"name"`
	Template string       `json:"template,omitempty"` // Dispatch=true 时必填
	Override NodeOverride `json:"override,omitempty"`

	// 能力开关——语义由组合得出，不要在这里新增「节点类型」字段。
	Dispatch         bool `json:"dispatch,omitempty"`           // 进入本列时派发一个任务
	Verdict          bool `json:"verdict,omitempty"`            // 等回合终态、解析裁决块并按结果路由（蕴含 Dispatch）
	CarryCardContext bool `json:"carry_card_context,omitempty"` // prompt 里拼入卡上下文段
	MaxRounds        int  `json:"max_rounds,omitempty"`         // Verdict 的轮次封顶；0 = 用包内默认
	// OmitAcceptance 为真时，本节点的 prompt 不注入整卡的验收判据。
	//
	// why 需要这个开关：验收判据通常是**实现级**的（测试全绿、真机跑通），
	// 而计划/拆解类节点的法定产出是文档。两者同时在场时，「pass 的依据是你
	// 真实跑到的结果」这条裁决契约在计划节点上无解，执行者化解矛盾的方式是
	// 直接把实现做掉——2026-08-22 真机实测过一次（B182）；对照组是同一条流上
	// 判据字段为空的卡，同一个执行者没有越轨。
	OmitAcceptance bool `json:"omit_acceptance,omitempty"`
	// Produces 为真时，节点裁决 pass 后由协调者按声明路径检查本轮 diff 并挂附件。
	// 注意：这里只保存声明，不在账本层读取文件系统或验证文档内容。
	Produces *NodeOutput `json:"produces,omitempty"`

	Next   string `json:"next,omitempty"`    // 裁决通过后移到哪一列；空 = 停在本列
	OnFail string `json:"on_fail,omitempty"` // 裁决未过退到哪一列；空 = 停在本列
	Gate   Gate   `json:"gate,omitempty"`    // 进入本列的门槛

	// HumanBases 列出「卡的有效基线落在其中时，本节点不自动执行、直接打
	// 等人标记」的分支名。
	//
	// why 需要它：合并退役成普通派发节点后，原 MergeStep 里那条
	// 「基线是主线就永远人工」的保护随之消失，而往 main 合并是外部可见且
	// 不易撤回的动作。做成节点上的一个列表（而不是代码里的常量）既保住了
	// 保护，又符合「只提供能力、语义由配置组合」——用户想让某条流自动合
	// main，把这个列表清空即可。
	HumanBases []string `json:"human_bases,omitempty"`
}

// WorkflowDef 工作流形状。
//
// **Nodes 是权威，States/Gates 是写入时从 Nodes 派生的只读投影。**
// why 保留派生投影而不是让消费者全改成读 Nodes：MoveCard 的状态校验、
// 看板列渲染、MigrateCardWorkflow 的防悬空校验都在读 States，派生投影让
// 它们一行不改地继续工作，把本次改动的爆炸半径压在读写两端。
//
// 反方向也成立：只有 States 的老行（存量卡钉的就是它）读出时补出等价的
// 纯人工节点序列，所以调用方永远可以只看 Nodes。
type WorkflowDef struct {
	States []string           `json:"states"`
	Gates  map[string]Gate    `json:"gates,omitempty"` // key = 目标状态
	Nodes  []NodeDef          `json:"nodes,omitempty"`
	Board  *proto.BoardLayout `json:"board,omitempty"`
}

// Workflow 不可变版本化聚合：同 name 只增版本，不改旧行。
type Workflow struct {
	Name      string
	Version   int
	Def       WorkflowDef
	CreatedAt time.Time
}

// Decision 裁决项。CardID 空 = 项目级请示。
type Decision struct {
	ID         int64     `json:"id"`
	CardID     string    `json:"card_id,omitempty"`
	Body       string    `json:"body"`
	Options    []string  `json:"options,omitempty"`
	Status     string    `json:"status"` // open|answered
	CreatedBy  string    `json:"created_by"`
	Answer     string    `json:"answer,omitempty"`
	AnsweredBy string    `json:"answered_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	AnsweredAt time.Time `json:"answered_at,omitempty"`
}

// CardView = Card + 查询期计算的派生标记（不落列，spec §2）。
type CardView struct {
	Card
	Blocked       bool     `json:"blocked"`
	BaseFrozen    bool     `json:"base_frozen"`          // true = 至少有一条 dispatched 事件，基线已冻结
	BlockedBy     []string `json:"blocked_by,omitempty"` // 未完成的 blocker
	Following     string   `json:"following,omitempty"`  // 非空 = merged_into 的承载卡 id（跟随态）
	MergedCount   int      `json:"merged_count"`         // 承载的成员数
	NeedsReason   string   `json:"needs,omitempty"`      // 非空 = 等人，值为 reason
	OpenDecisions int      `json:"open_decisions"`
	ChildrenTotal int      `json:"children_total"` // 直接子卡数
	ChildrenDone  int      `json:"children_done"`  // 已完结（已完成或终止）的直接子卡数——语义与聚合闸同一把尺
}

// CardFilter ListCards 的过滤条件；零值 = 不过滤该维度。
type CardFilter struct {
	Project         string
	Status          string
	BaseBranch      string
	Blocked         bool // true = 只要 blocked
	Needs           bool // true = 只要 等人/有 open 裁决
	IncludeTerminal bool // false = 排除 已完成/终止
}
