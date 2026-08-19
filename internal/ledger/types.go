// 账本域的数据类型与受控词表。字段与 spec §2.1 DDL 一一对应；
// 状态骨架锚点、关系类型、事件类型的字符串字面量以本文件为唯一定义点。
package ledger

import (
	"encoding/json"
	"time"
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
	EvCardCreated        = "card_created"
	EvStatusMoved        = "status_moved"
	EvDispatched         = "dispatched"
	EvReviewVerdict      = "review_verdict"
	EvMerged             = "merged"
	EvUnmerged           = "unmerged"
	EvSplit              = "split"
	EvAcceptanceRecorded = "acceptance_recorded"
	EvComment            = "comment"
	EvNeedsHuman         = "needs_human"
	EvNeedsCleared       = "needs_cleared"
	EvDecisionOpened     = "decision_opened"
	EvDecisionAnswered   = "decision_answered"
	EvTaskMirrored       = "task_mirrored"
)

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
	DriverHeartbeatAt  time.Time    `json:"driver_heartbeat_at,omitempty"`
	CreatedAt          time.Time    `json:"created_at"`
	UpdatedAt          time.Time    `json:"updated_at"`
}

// Relation 类型化关系边。
type Relation struct {
	From, To, Type string
	CreatedAt      time.Time
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
	RequireAcceptance bool   `json:"require_acceptance,omitempty"` // 验收判据非空
}

// WorkflowDef 状态机形状。States 是含插入状态的全序列（不含「终止」）；
// 一期不限制转移方向（人工回退是真实需求），只校验目标在 States 内 + gate。
type WorkflowDef struct {
	States []string        `json:"states"`
	Gates  map[string]Gate `json:"gates,omitempty"` // key = 目标状态
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
	BlockedBy     []string `json:"blocked_by,omitempty"` // 未完成的 blocker
	Following     string   `json:"following,omitempty"`  // 非空 = merged_into 的承载卡 id（跟随态）
	NeedsReason   string   `json:"needs,omitempty"`      // 非空 = 等人，值为 reason
	OpenDecisions int      `json:"open_decisions"`
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
