// ask.go —— Ask 回程契约与工单命名空间编码（B233.1）。
//
// 职责：
//   - 定义可选 AskResponder：按问题身份回答，而不是走 Adapter.Send
//   - 冻结 taskID:nativeID 工单命名空间的唯一编码/解码
//
// 边界：
//   - AskResponder 不是 Adapter 的第六个方法；未实现的 adapter 不得被
//     OpenCode 路径误调，也不得回退成 Send
//   - 编码函数无 I/O；空 nativeID 的 uuid 工单由调用方生成，不走本函数
package executor

import (
	"context"
	"strings"
)

// RespondAskReq 是一次有身份的 Ask 回答。
//
// TicketID 是尝试/回合身份（工单 id）。QuestionID 是原生问题 id，可空。
// 只给 TaskID「找当前那条问题」不合法。
type RespondAskReq struct {
	TaskID     string
	TicketID   string
	QuestionID string
	Answer     string
}

// AskResponder 是执行契约的 Ask 回程增量。消费方（Manager）做类型断言。
type AskResponder interface {
	RespondAsk(ctx context.Context, req RespondAskReq) error
}

// NamespacedTicketID 把任务 id 与原生请求 id 编成工单 id。
//
// 格式是 taskID + ":" + nativeID。nativeID 自身可含冒号；解码用前缀剥离。
func NamespacedTicketID(taskID, nativeID string) string {
	return taskID + ":" + nativeID
}

// NativeIDFromTicket 从命名空间化工单 id 还原裸原生 id。
//
// 不变式：工单由 NamespacedTicketID(taskID, nativeID) 生成时，本函数精确还原。
func NativeIDFromTicket(taskID, ticketID string) string {
	return strings.TrimPrefix(ticketID, taskID+":")
}
