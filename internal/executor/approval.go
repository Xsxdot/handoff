// approval.go —— 审批 client 契约（B233.1）。
//
// 职责：
//   - 定义执行实例侧持有的 ApprovalClient：PolicySnapshot / Request / Await / Acknowledge
//   - 定义快照、请求、引用、决定与投递确认的类型与状态字面值
//
// 边界：
//   - 接口定义在使用方（本包）。具体实现由组装点注入 StartReq.Approval，
//     adapter 不得 import permgate 或 agentd.Approver
//   - 本文件无 I/O、无实现：不写 store、不调模型、不发原生权限应答
package executor

import "context"

// ApprovalStatus 是审批 client 对外可见的决定状态。Consult 是服务内部步骤，
// 不得作为 client 返回值。
type ApprovalStatus string

const (
	ApprovalAllow   ApprovalStatus = "allow"
	ApprovalDeny    ApprovalStatus = "deny"
	ApprovalPending ApprovalStatus = "pending"
)

// AckStage 区分「决定已形成 / 已送达原生请求 / 动作已执行」。
type AckStage string

const (
	AckFormed    AckStage = "formed"
	AckDelivered AckStage = "delivered"
	AckExecuted  AckStage = "executed"
)

// ApprovalScope 是一次执行实例的合法作用范围，与 permgate.Scope 三根同形。
type ApprovalScope struct {
	Workdir    string
	TaskDir    string
	TaskTmpDir string
}

// PolicySnapshot 是绑定到当前回合的版本化政策。
//
// Version 非空；同一政策内容必须得到同一 Version，内容变化必须换 Version。
// 具体哈希算法由实现节点给出，本节点只冻结相等性。
type PolicySnapshot struct {
	Version string
	TaskID  string
	Scope   ApprovalScope
}

// ApprovalRequest 是一次原生权限请求的结构化输入。
type ApprovalRequest struct {
	NativeID  string
	Text      string
	Perm      *PermRequest
	Truncated bool
}

// ApprovalRef 是审批服务给出的稳定引用，供 Await / Acknowledge 使用。
type ApprovalRef struct {
	ID string
}

// ApprovalDecision 是最终或当前决定。
type ApprovalDecision struct {
	Status ApprovalStatus
	Reason string
	Rule   string
}

// ApprovalResult 是 Request 的立即返回：可能已经是终态，也可能 pending。
type ApprovalResult struct {
	Ref      ApprovalRef
	Decision ApprovalDecision
}

// ApprovalAck 确认决定生命周期中的一个阶段。
type ApprovalAck struct {
	Ref      ApprovalRef
	NativeID string
	Stage    AckStage
}

// ApprovalClient 是限定于单个执行实例的审批 client。
//
// 实现必须拒绝把范围扩大到其它任务。失败、超时、不可用不得返回 allow。
type ApprovalClient interface {
	PolicySnapshot(ctx context.Context) (PolicySnapshot, error)
	Request(ctx context.Context, req ApprovalRequest) (ApprovalResult, error)
	Await(ctx context.Context, ref ApprovalRef) (ApprovalDecision, error)
	Acknowledge(ctx context.Context, ack ApprovalAck) error
}

// ApprovalBinder 是可选能力：允许运行态任务重绑 ApprovalClient（如 Continue 投影新快照后）。
type ApprovalBinder interface {
	BindApproval(taskID string, client ApprovalClient) error
}
