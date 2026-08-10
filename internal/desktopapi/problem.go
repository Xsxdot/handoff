// desktopapi 错误线格式：Problem 与 WriteProblem。
//
// 职责：
//   - 定义稳定的 Problem code（spec §12.3）
//   - 提供 WriteProblem 统一写回，用户可修复错误保留具体 message，
//     内部错误只返回安全摘要
//
// 边界：
//   - 不泄露内部错误细节：details 不含 secret、env value 或完整敏感输出
//   - 不直接返回 Go error 文本，调用方负责把内部错误记入日志并映射 code
package desktopapi

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// ProblemCode 是稳定的错误码，桌面端据此做分支处理而非解析 message 文本。
type ProblemCode string

// 稳定 Problem codes（spec §12.3）。
const (
	ProblemLocalAgentdUnavailable ProblemCode = "LOCAL_AGENTD_UNAVAILABLE"
	ProblemMachineOffline         ProblemCode = "MACHINE_OFFLINE"
	ProblemCapabilityUnsupported  ProblemCode = "CAPABILITY_UNSUPPORTED"
	ProblemResourceNotFound       ProblemCode = "RESOURCE_NOT_FOUND"
	ProblemPathOutsideWorkspace   ProblemCode = "PATH_OUTSIDE_WORKSPACE"
	ProblemVersionConflict        ProblemCode = "VERSION_CONFLICT"
	ProblemCommandConflict        ProblemCode = "COMMAND_CONFLICT"
	ProblemCursorExpired          ProblemCode = "CURSOR_EXPIRED"
	ProblemOperationInProgress    ProblemCode = "OPERATION_IN_PROGRESS"
	ProblemAuthFailed             ProblemCode = "AUTH_FAILED"
)

// Problem 是统一错误线格式。
//
// 约束：
//   - Message 供用户阅读，可为可行动提示；不包含敏感信息
//   - Details 可选，仅放上下文标识（id/seq 等），不泄露 secret/env/回答全文
//   - Retryable 提示客户端是否值得退避重试
type Problem struct {
	Code        ProblemCode `json:"code"`
	Message     string      `json:"message"`
	Retryable   bool        `json:"retryable"`
	MachineID   string      `json:"machine_id,omitempty"`
	WorkspaceID string      `json:"workspace_id,omitempty"`
	TaskID      string      `json:"task_id,omitempty"`
	OperationID string      `json:"operation_id,omitempty"`
	Details     string      `json:"details,omitempty"`
}

// ProblemError 在 application/adapter 层携带稳定 HTTP 状态与公开 Problem，
// 同时通过 Cause 保留仅供日志与 errors.Is/As 使用的内部原因。
type ProblemError struct {
	Status  int
	Problem Problem
	Cause   error
}

// Error 返回安全错误摘要，不拼接内部 Cause，避免调用方误把敏感底层错误写回 HTTP。
func (e *ProblemError) Error() string {
	return fmt.Sprintf("%s: %s", e.Problem.Code, e.Problem.Message)
}

// Unwrap 暴露内部原因给服务端日志与 errors.Is/As；公开响应仍只编码 Problem。
func (e *ProblemError) Unwrap() error { return e.Cause }

// WriteProblem 把 Problem 写回响应，统一 Content-Type 与 JSON 编码。
//
// 内部错误调用方在日志里记录 cause 原文，此处只输出安全摘要。
func WriteProblem(w http.ResponseWriter, status int, p Problem) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}
