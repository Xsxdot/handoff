// approver.go —— B437「代他机裁决」跨进程 wire 词表与 DTO。
//
// 职责：集中定义 agentd↔agentd 代他机裁决端点（POST /api/approver/decide）的
// 请求/应答形状，供发起方 client 与对端 handler 共用。
//
// 边界：本文件只有常量与类型，无行为。跨二进制字段名是冻结 F1/F2/F4：
// 只增不改名不删不改性，新增字段 omitempty，双侧宽松解码（未知键忽略）。
// 请求体**不存在 prompt 类字段**——对端不接受任意提示词（方案甲第三点）。
package proto

// ApproverDecideReq 是发起方 → 对端的代他机裁决请求（冻结 F1）。
//
//   - Permission: 权限请求原文（必填非空）
//   - Executor: 请求点名的执行器（必填非空）
//   - TaskSummary: 任务背景摘要（可空）
//   - Model: 模型名（可空；空 = 对端用该执行器默认）
//
// 没有 prompt 字段：提示词由对端机内拼接（审批语义唯一实现在 orchestration）。
type ApproverDecideReq struct {
	Permission  string `json:"permission"`
	Executor    string `json:"executor"`
	TaskSummary string `json:"task_summary,omitempty"`
	Model       string `json:"model,omitempty"`
}

// ApproverDecideResp 是对端 → 发起方的裁决应答（冻结 F2）。
//
//   - Decision: approve / escalate / error；error 恒表示**该环失败**，发起方转下一环
//   - Reason: 裁决理由或失败明细
//   - Model: 对端单环裁决的模型标注（未指定 = 「默认」占位）
//   - ElapsedMS: 对端侧该环耗时
//   - ErrorKind: 仅 decision=error 时出现，∈ {no_executor, invalid_output, internal}
type ApproverDecideResp struct {
	Decision  string `json:"decision"`
	Reason    string `json:"reason"`
	Model     string `json:"model"`
	ElapsedMS int64  `json:"elapsed_ms"`
	ErrorKind string `json:"error_kind,omitempty"`
}

// 端点决策取值（冻结 F2）。
const (
	ApproverDecisionApprove  = "approve"
	ApproverDecisionEscalate = "escalate"
	ApproverDecisionError    = "error"
)

// 端点 error_kind 取值（冻结 F2）。未知值发起方按 internal 处理。
const (
	ApproverErrorNoExecutor    = "no_executor"
	ApproverErrorInvalidOutput = "invalid_output"
	ApproverErrorInternal      = "internal"
)
