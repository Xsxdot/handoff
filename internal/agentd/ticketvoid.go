// 本文件实现「工单作废 + 留痕」这一个动作（B63）。
//
// 职责：
//   - 把一个任务的全部未回答工单作废，并按需产出一条 tickets_voided 审计事件
//   - 作为 transit 终态分支与 reconcileExecutorGone 的唯一共用实现
//
// 边界：
//   - 不判断「该不该作废」——时机由调用方决定（终态 / executor 已死）
//   - 不 Publish：tickets_voided 是纯审计事件，实时流上不出现（见 proto 常量注释）
//   - 不因作废或写事件失败而中断调用方：状态迁移已经发生，为一条审计写失败回滚
//     终态得不偿失
package agentd

import (
	"log/slog"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// ticketsVoidedPayload 是 tickets_voided 事件的 payload。
//
// Reason 直接沿用调用方的迁移原因（"done" / "stop" / 对账的那句人话），
// 让 show 里能回答「这批单是因为什么被作废的」。
type ticketsVoidedPayload struct {
	Voided int    `json:"voided"`
	Reason string `json:"reason"`
}

// voidTicketsWithAudit 作废任务的全部挂起工单，并在确实作废了东西时留一条审计事件。
//
// 参数：
//   - st: 存储
//   - taskID: 任务 ID
//   - reason: 作废原因，进事件 payload 与日志
//   - log: 日志入口
//
// 返回：
//   - 本次被作废的工单数；出错或无单可作废时为 0
//
// 注意：
//   - voided == 0 时**不产出事件**：绝大多数任务终结时本就没有挂起工单，
//     无条件写事件等于给每条正常事件流添噪音
//   - 依赖 VoidPendingTickets 的幂等（第二次起返回 0）来天然去重，本函数不另做判重
//   - 失败一律只记日志：见文件头「不中断调用方」
func voidTicketsWithAudit(st *store.Store, taskID, reason string, log *slog.Logger) int {
	voided, err := st.VoidPendingTickets(taskID)
	if err != nil {
		log.Error("作废挂起工单失败", "task", taskID, "reason", reason, "cause", err)
		return 0
	}
	if voided == 0 {
		return 0
	}
	log.Warn("挂起工单已作废", "task", taskID, "reason", reason, "voided", voided)
	if _, err := st.AppendEvent(taskID, proto.EventTypeTicketsVoided,
		ticketsVoidedPayload{Voided: voided, Reason: reason}); err != nil {
		log.Error("追加工单作废审计事件失败", "task", taskID, "voided", voided, "cause", err)
	}
	return voided
}
