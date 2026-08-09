// reconcile.go —— 任务运行态与 executor 实际存活性的对账。
//
// 职责：
//   - reconcileExecutorGone：「executor 已不在」这一事实的唯一收尾实现，
//     三个到达口（启动探活 / 事件通道关闭 / 审核者动作撞上失配）共用
//
// 边界：
//   - 不探活：本文件只负责「已经知道 executor 没了之后怎么办」，
//     「怎么知道的」属各到达口自己（spec §2.2 明确不加周期性探活）
//   - 不碰 adapter：收尾只动 store 与 hub
package agentd

import (
	"log/slog"

	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// reconcileExecutorGone 收尾一个 executor 已不在的任务：
// 作废挂起工单 → 追加 failed 事件 → 迁 waiting_review → 广播。
//
// 参数：
//   - st/hub: 存储与实时路由
//   - taskID: 待收尾任务
//   - reason: 失配来源的人话说明，直接进 failed 事件的 fail_reason。审核者据此
//     区分「agentd 重启后 executor 已不在」「executor 事件流终结」「恢复操作发现
//     executor 已不在」三种现场——三者的后续处置不同，混成一句话等于丢信息
//   - log: 日志入口
//
// 返回：
//   - 收尾后的任务状态（调用方要回给 CLI 时用）；读任务失败时返回空串
//
// 注意：
//   - 对 waiting_review / completed / failed 是**空操作**：前者本就是待审核终态
//     （追加事件只是噪音），后两者已终结。三个到达口可能对同一任务先后触发，
//     幂等由这条保证
//   - 作废工单排在事件之前，且作废失败只记日志不中断：事件是审核者的主要信息
//     来源，必须落
//   - 追加事件失败则不迁移状态：迁了却没事件 = 审核者看到状态变化却不知原因
func reconcileExecutorGone(st *store.Store, hub *Hub, taskID, reason string, log *slog.Logger) proto.TaskState {
	cur, err := st.GetTask(taskID)
	if err != nil {
		log.Error("对账读取任务失败", "task", taskID, "reason", reason, "cause", err)
		return ""
	}
	log.Info("executor 已不在，开始对账", "task", taskID, "state", cur.State, "reason", reason)
	if cur.State != proto.TaskStateRunning && cur.State != proto.TaskStateWaitingAnswer {
		// 空操作：待审核终态与已终结态都不需要收尾（why 见 doc 注意）
		log.Info("任务无需对账，跳过", "task", taskID, "state", cur.State)
		return cur.State
	}

	if voided, verr := st.VoidPendingTickets(taskID); verr != nil {
		log.Error("对账作废挂起工单失败，继续追加事件", "task", taskID, "cause", verr)
	} else if voided > 0 {
		log.Warn("对账作废挂起工单", "task", taskID, "voided", voided)
	}
	evt, err := st.AppendEvent(taskID, proto.EventTypeFailed, failedPayload{FailReason: reason})
	if err != nil {
		log.Error("对账追加 failed 事件失败，不迁移状态", "task", taskID, "cause", err)
		return cur.State
	}
	if err := recoverTransit(st, taskID, cur.State); err != nil {
		log.Error("对账迁移 waiting_review 失败", "task", taskID, "cause", err)
		return cur.State
	}
	hub.Publish(evt)
	log.Info("对账完成", "task", taskID, "from", cur.State, "to", proto.TaskStateWaitingReview)
	return proto.TaskStateWaitingReview
}
