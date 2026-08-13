// backlog.go —— follow 建连前「积压对账」的线格式与计算。
//
// 职责：
//   - 定义摘要行的线格式 BacklogSummary（stdout 每行一条，与事件行共用一条通道）
//   - 从 agentd 的权威快照（AttachInfo）算出摘要：错过多少条、其中多少已失效、
//     当前还欠哪些工单
//
// 边界：
//   - 无 I/O、无网络：快照怎么拿是 reconcileBacklog 的事，本文件只做纯计算
//   - 不决定摘要吐不吐、cursor 推不推——那是 FollowEvents 的编排
//   - 不打日志：本文件是纯计算，可观测性由调用方 reconcileBacklog 承担
//     （它拿同一份结果打一行带全部计数的 Info）
package client

import (
	"encoding/json"

	"github.com/xushixin/handoff/internal/proto"
)

// BacklogSummaryType 是摘要行的 type 取值。
//
// 为什么复用 type 这个 key 而不另起 kind：stdout 的既有契约是「每行一个带 type
// 的 JSON 对象」，上层按行解析。沿用 type 能让既有解析器读到一个不认识的取值
// 就跳过；换个 key 则会让它们撞上一个缺字段的对象。
//
// 注意：这是**客户端合成**的行，agentd 从不存这个事件类型——不要去 proto.EventType
// 里找它。
const BacklogSummaryType = "backlog_summary"

// BacklogSummary 是 follow 建立连接前对账得出的「你错过了什么」。
//
// 线格式（单行 JSON，stdout）：{"type":"backlog_summary","task_id":..,"from_seq":..,
// "to_seq":..,"state":..,"missed":..,"missed_truncated":..,"stale":..,"actionable":[..]}
type BacklogSummary struct {
	Type    string          `json:"type"`
	TaskID  string          `json:"task_id"`
	FromSeq int64           `json:"from_seq"`
	ToSeq   int64           `json:"to_seq"`
	State   proto.TaskState `json:"state"`

	// Missed 是间隙内可交付事件的条数。MissedTruncated 为 true 时语义降级为
	// 「至少 Missed 条」——快照的事件窗口没能覆盖到 cursor，剩下的数不出来。
	Missed          int  `json:"missed"`
	MissedTruncated bool `json:"missed_truncated"`

	// Stale 是间隙内工单已被消费（审批链答掉或被作废）的事件条数——补 reply 会 404。
	Stale int `json:"stale"`

	// Actionable 是当前仍待处置的工单**全量**，每张带完整 Request 原文，协调者
	// 可直接据此 reply --ticket <id>。
	//
	// 注意它**不限于间隙内**：断网前你就看见过、但一直没答的工单也在里面。
	// 那正是最需要知道的一类，也是 Stale 不能用减法算出来的原因。
	Actionable []proto.Ticket `json:"actionable"`
}

// computeBacklog 从权威快照算出积压摘要。
//
// 参数：
//   - taskID: 完整 UUID，原样写进摘要
//   - fromSeq: 本机 cursor 停在哪（0 表示本机从未交付过该任务的事件）
//   - snap: GET /api/tasks/{id} 的快照
//
// 返回：
//   - 摘要；**无积压时返回 nil**（快照事件窗口为空，或水位不超过 fromSeq）
//
// 注意：
//   - 三个计数各自独立算，不做减法（why 见 BacklogSummary.Actionable 的注释）
//   - seq 是全局 AUTOINCREMENT、跨任务共享，单任务 seq 不连续：ToSeq-FromSeq
//     不是条数，只能逐条遍历来数
func computeBacklog(taskID string, fromSeq int64, snap *AttachInfo) *BacklogSummary {
	if snap == nil || len(snap.RecentEvents) == 0 {
		return nil
	}
	// RecentEvents 按 seq 升序（EventsFrom 取最新窗口后翻回升序），末条即当前水位
	toSeq := snap.RecentEvents[len(snap.RecentEvents)-1].Seq
	if toSeq <= fromSeq {
		return nil
	}

	pending := make(map[string]struct{}, len(snap.PendingTickets))
	for _, tk := range snap.PendingTickets {
		pending[tk.ID] = struct{}{}
	}

	missed, stale := 0, 0
	for _, ev := range snap.RecentEvents {
		// 口径与流过滤共用 isDeliverable：数的是「本该唤醒协调者的事件」
		if ev.Seq <= fromSeq || !isDeliverable(ev.Type) {
			continue
		}
		missed++
		id := ticketIDOf(ev)
		if id == "" {
			continue
		}
		if _, still := pending[id]; !still {
			stale++
		}
	}

	// 归一化为空数组而非 nil：JSON 里 null 与 [] 对按行解析的消费方是两回事
	actionable := snap.PendingTickets
	if actionable == nil {
		actionable = []proto.Ticket{}
	}

	return &BacklogSummary{
		Type:    BacklogSummaryType,
		TaskID:  taskID,
		FromSeq: fromSeq,
		ToSeq:   toSeq,
		State:   snap.Task.State,
		Missed:  missed,
		// 判据是「窗口最旧一条仍晚于 cursor」——此时无法证明窗口覆盖了整个间隙。
		// 不用「窗口满 N 条」：客户端不知道 agentd 的 recentEventsLimit，写死会
		// 造成版本耦合，且服务端调小该值时会**漏报**截断（错在危险方向）。
		// 现判据的代价是偶尔虚报，错在安全方向：宁可少声称，不可多声称
		MissedTruncated: snap.RecentEvents[0].Seq > fromSeq,
		Stale:           stale,
		Actionable:      actionable,
	}
}

// ticketIDOf 取事件 payload 里的 ticket_id。
//
// 参数：
//   - ev: 任意事件
//
// 返回：
//   - 工单 ID；非工单类事件、payload 非法或缺该字段时返回空串
//
// 注意：ticket_id 是 permission_request / question 事件 payload 的既有线格式契约
// （服务端定义在 internal/agentd/manager.go 的 permissionPayload / questionPayload）。
// 此处只解这一个字段，不与服务端结构体耦合。
func ticketIDOf(ev proto.Event) string {
	switch ev.Type {
	case proto.EventTypePermissionRequest, proto.EventTypeQuestion:
	default:
		return ""
	}
	var p struct {
		TicketID string `json:"ticket_id"`
	}
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		// payload 解不开不是致命错：摘要少数一条 stale，好过让整次对账失败
		return ""
	}
	return p.TicketID
}
