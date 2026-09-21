// 本文件导出 wait/follow 的可交付策略。
//
// 职责：把「哪些事件唤醒协调者」从传输实现里叫出名字，供应用消费。
// 边界：不碰 WS/HTTP 收发；不改词表（对齐 B233.1）；调用点仍在 client.waitOnce / FollowEvents。
package client

import "github.com/Xsxdot/handoff/internal/proto"

// WaitDeliveryPolicy 判定 wait/follow 默认是否唤醒协调者。
//
// 这是任务事件消费策略，不是传输过滤：WS/HTTP 流必须仍能见到被判 false 的类型
// （FollowEvents all=true 与服务端 store 重放是现成证据）。传输只负责按游标可靠交付。
//
// 可交付 = 全部类型 − {progress, approver_decision, approver_disabled,
// tickets_voided, ticket_answered, permission_auto_allow, permission_reuse}。
//
// 审计类在服务端只入库不 Publish，实时流本就见不到；WS 重放读 store 会把它们
// 一并推来。不过滤就会出现「重连交付比实时流更多」的唤醒风暴。tickets_voided
// 与 completed/failed 同时刻产生，可交付会抢走一次性 wait 的收手权。
//
// 词表与 B233.1 冻结 17–26 对齐；本卡冻结的是归属（应用，不在传输），不是另造一套。
// all=true 时调用方不使用本谓词，全量交付。
func WaitDeliveryPolicy(t proto.EventType) bool {
	switch t {
	case proto.EventTypeProgress,
		proto.EventTypeApproverDecision,
		proto.EventTypeApproverDisabled,
		proto.EventTypeTicketsVoided,
		proto.EventTypeTicketAnswered,
		proto.EventTypePermissionAutoAllow,
		proto.EventTypePermissionReuse:
		return false
	}
	return true
}
