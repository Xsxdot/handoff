package proto

import (
	"encoding/json"
	"time"
)

// Attachment 是账本卡片附件的 wire DTO。
type Attachment struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

// Card 是账本卡片的 wire DTO；字段名与现有账本 HTTP 响应保持一致。
type Card struct {
	ID                 string       `json:"id"`
	Title              string       `json:"title"`
	Status             string       `json:"status"`
	TerminateReason    string       `json:"terminate_reason,omitempty"`
	Priority           string       `json:"priority"`
	Project            string       `json:"project"`
	ParentID           string       `json:"parent"`
	WorkflowName       string       `json:"workflow"`
	WorkflowVersion    int          `json:"workflow_version"`
	Attachments        []Attachment `json:"attachments,omitempty"`
	AcceptanceCriteria string       `json:"acceptance_criteria,omitempty"`
	BaseBranch         string       `json:"base_branch,omitempty"`
	DriverSession      string       `json:"driver_session,omitempty"`
	DriverHeartbeatAt  time.Time    `json:"driver_heartbeat_at,omitempty"`
	CreatedAt          time.Time    `json:"created_at"`
	UpdatedAt          time.Time    `json:"updated_at"`
}

// Relation 是账本关系边的 wire DTO。
// PascalCase 键是现有线格式，刻意不加 JSON tag 以保持兼容。
type Relation struct {
	From, To, Type string
	CreatedAt      time.Time
}

// TaskStateRow 是卡上挂账任务的镜像状态摘要 wire DTO。
// PascalCase 键是现有线格式，刻意不加 JSON tag 以保持兼容。
type TaskStateRow struct {
	Target, TaskID, Purpose, LastType string
	LastSeq                           int64
}

// Gate 是工作流节点进入条件的 wire DTO。
type Gate struct {
	RequireAttachment   string `json:"require_attachment,omitempty"`
	RequireAcceptance   bool   `json:"require_acceptance,omitempty"`
	RequireChildrenDone bool   `json:"require_children_done,omitempty"`
}

// NodeOverride 是工作流节点模板覆盖的 wire DTO。
type NodeOverride struct {
	Executor   string `json:"executor,omitempty"`
	Discipline string `json:"discipline,omitempty"`
	Target     string `json:"target,omitempty"`
	Model      string `json:"model,omitempty"`
}

// NodeOutput 是工作流节点声明的单一附件 kind/path wire DTO。
// 指针由 NodeDef.Produces 持有，以区分旧 JSON 的字段缺失和显式对象。
type NodeOutput struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

// NodeDef 是工作流节点的 wire DTO。
type NodeDef struct {
	Name             string       `json:"name"`
	Template         string       `json:"template,omitempty"`
	Override         NodeOverride `json:"override,omitempty"`
	Dispatch         bool         `json:"dispatch,omitempty"`
	Verdict          bool         `json:"verdict,omitempty"`
	CarryCardContext bool         `json:"carry_card_context,omitempty"`
	MaxRounds        int          `json:"max_rounds,omitempty"`
	OmitAcceptance   bool         `json:"omit_acceptance,omitempty"`
	Next             string       `json:"next,omitempty"`
	OnFail           string       `json:"on_fail,omitempty"`
	Gate             Gate         `json:"gate,omitempty"`
	HumanBases       []string     `json:"human_bases,omitempty"`
	Produces         *NodeOutput  `json:"produces,omitempty"`
}

// CardBrief 是详情中直接子卡的最小摘要 wire DTO。
type CardBrief struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// Decision 是卡上裁决项的 wire DTO。
type Decision struct {
	ID         int64     `json:"id"`
	CardID     string    `json:"card_id,omitempty"`
	Body       string    `json:"body"`
	Options    []string  `json:"options,omitempty"`
	Status     string    `json:"status"`
	CreatedBy  string    `json:"created_by"`
	Answer     string    `json:"answer,omitempty"`
	AnsweredBy string    `json:"answered_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	AnsweredAt time.Time `json:"answered_at,omitempty"`
}

// LedgerEvent 是账本事件的 wire DTO。
type LedgerEvent struct {
	Seq       int64           `json:"seq"`
	CardID    string          `json:"card_id"`
	Type      string          `json:"type"`
	Actor     string          `json:"actor"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

// CardView 是列表卡片及查询期派生标记的 wire DTO。
type CardView struct {
	ID            string       `json:"id"`
	Title         string       `json:"title"`
	Status        string       `json:"status"`
	Priority      string       `json:"priority"`
	Project       string       `json:"project"`
	Workflow      string       `json:"workflow"`
	Parent        string       `json:"parent"`
	BaseBranch    string       `json:"base_branch"`
	BaseFrozen    bool         `json:"base_frozen"`
	Attachments   []Attachment `json:"attachments"`
	Following     string       `json:"following"`
	Blocked       bool         `json:"blocked"`
	BlockedBy     []string     `json:"blocked_by"`
	MergedCount   int          `json:"merged_count"`
	Needs         string       `json:"needs"`
	OpenDecisions int          `json:"open_decisions"`
	ChildrenTotal int          `json:"children_total"`
	ChildrenDone  int          `json:"children_done"`
	Conflict      bool         `json:"conflict"`
	OpenTickets   int          `json:"open_tickets"`
}

// CardDetail 是卡片详情的 wire DTO。
type CardDetail struct {
	Card                Card           `json:"card"`
	Relations           []Relation     `json:"relations"`
	Events              []LedgerEvent  `json:"events"`
	TaskStates          []TaskStateRow `json:"task_states"`
	EffectiveBaseBranch string         `json:"effective_base_branch"`
	Decisions           []Decision     `json:"decisions"`
	Needs               string         `json:"needs"`
	Children            []CardBrief    `json:"children"`
}

// FlowDetail 是工作流详情的 wire DTO。
type FlowDetail struct {
	Name    string    `json:"name"`
	Version int       `json:"version"`
	Nodes   []NodeDef `json:"nodes"`
	States  []string  `json:"states"`
}

// NewCardReq 是建卡请求。workflow 缺席或为空表示尚未定性，由账本解析为 triage。
type NewCardReq struct {
	Title      string `json:"title"`
	Project    string `json:"project"`
	Workflow   string `json:"workflow,omitempty"`
	Priority   string `json:"priority,omitempty"`
	Parent     string `json:"parent,omitempty"`
	BaseBranch string `json:"base_branch,omitempty"`
}

// MigrateCardReq 是显式目标工作流、落点列和可选版本的迁移请求。
type MigrateCardReq struct {
	Workflow string `json:"workflow"`
	Status   string `json:"status"`
	Version  int    `json:"version,omitempty"`
}

// CardWorkflowLocation 是迁移响应中的卡位置。
type CardWorkflowLocation struct {
	ID              string `json:"id"`
	Workflow        string `json:"workflow"`
	WorkflowVersion int    `json:"workflow_version"`
	Status          string `json:"status"`
}

// MigrateCardResp 是跨流迁移响应。
type MigrateCardResp struct {
	OK    bool                 `json:"ok"`
	ID    string               `json:"id"`
	From  CardWorkflowLocation `json:"from"`
	To    CardWorkflowLocation `json:"to"`
	Event LedgerEvent          `json:"event"`
}

// CardCreateResp 是建卡响应。
type CardCreateResp struct {
	ID string `json:"id"`
}
