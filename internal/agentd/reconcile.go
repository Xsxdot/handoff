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
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/prochost"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// noteStopping 标记「接下来这次事件通道关闭是我们自己发起的」。
//
// 必须在 ad.Stop() **之前**调用。
//
// why（为什么需要这个标记）：Manager.Stop 先调 ad.Stop() 再落 failed，两步之间
// adapter 已经关掉了事件通道，mediate 随之退出并对账——此时任务状态还是 running，
// 于是补一条它不该有的 failed 事件，并造成 running→waiting_review→failed 的
// 状态抖动（末跳合法，所以不硬失败，但事件是噪音）。
//
// why（为什么不改 Stop 的顺序）：先落 failed 再 ad.Stop() 会让 executor 在状态
// 已定型后仍可能产出事件，各 handler 的「已终结则丢弃」判断要散在更多路径上，
// 风险大于收益。显式标记说的正是「这次关闭是我们自己关的」，诚实且局部。
func (m *Manager) noteStopping(taskID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopping[taskID] = struct{}{}
}

// takeStopping 取走并清空标记，返回本次关闭是否为主动停止。
//
// why（取走式而非常驻）：标记的生命周期就是一次主动停止。若它长期驻留，下一次
// executor 猝死会被上一次的主动停止误抑制——真出事时反而没人对账。与 grok
// adapter 的 takeAskedViaTool、opencode 的 takeTurnRejected 同源。
func (m *Manager) takeStopping(taskID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.stopping[taskID]
	delete(m.stopping, taskID)
	return ok
}

// reconcileExecutorGone 是包级同名函数的方法薄包装（省去调用点重复传 st/hub/log）。
func (m *Manager) reconcileExecutorGone(taskID, reason string) proto.TaskState {
	return reconcileExecutorGone(m.st, m.hub, taskID, reason, m.log)
}

// stopExecutor 停 executor，并在「没有内存运行态」时按恢复凭据兜底回收。
//
// 参数：
//   - taskID: 目标任务
//   - ad: 已解析的 adapter（调用方已做 adapterFor）
//
// 注意：
//   - 调用前必须已 noteStopping（本函数会关掉事件通道，mediate 随之退出）
//   - 任何失败都不中断调用方（归档/中止本身已经达成）；回收不掉时留 progress
//     事件提示人工——与 worktree 清理失败的信号对称。B20 现场的孤儿存活 11.5
//     小时，正是因为完全静默、没人知道它在
func (m *Manager) stopExecutor(taskID string, ad executor.Adapter) {
	m.noteStopping(taskID)
	err := ad.Stop(taskID)
	if err == nil {
		return
	}
	if !errors.Is(err, executor.ErrTaskNotRunning) {
		// executor 还在，只是这次没停掉：兜底回收对它无意义——
		// 真去 kill 进程反而可能杀掉正在收尾的进程
		m.log.Error("停止 executor 失败", "task", taskID, "cause", err)
		// 唯独「已发 SIGKILL 但复核仍存活」要惊动人：这是唯一一种不提示就会
		// 留下长期孤儿的失败（B20 现场存活了 11.5 小时，正是因为完全静默）。
		// 其余 Stop 失败五花八门（ctx 取消、内部状态不一致），全发事件等于
		// 把审核者淹了，那样这条提示就没人看了。
		if errors.Is(err, prochost.ErrStillAlive) {
			m.notifyOrphanRisk(taskID, fmt.Sprintf(
				"executor 进程可能残留（已发 SIGKILL 但复核仍存活），"+
					"请先 handoff status 确认，再 handoff stop %s 回收（原因：%v）", taskID, err))
		}
		return
	}
	rp, ok := ad.(reaper)
	if !ok {
		m.log.Warn("executor 无内存运行态且 adapter 不支持兜底回收", "task", taskID, "cause", err)
		return
	}
	taskDir := filepath.Join(m.cfg.DataDir, "tasks", taskID)
	m.log.Info("executor 无内存运行态，按恢复凭据兜底回收", "task", taskID)
	if rerr := rp.Reap(taskID, taskDir); rerr != nil {
		m.log.Error("兜底回收失败，留事件提示人工", "task", taskID, "cause", rerr)
		// 给审核者的是「下一步做什么」，不是「出了什么错」——旧文案让人去
		// tmux kill-session，那个命令现在不存在了，照做只会更困惑
		m.notifyOrphanRisk(taskID, fmt.Sprintf("executor 进程可能残留，请先 handoff status 确认，"+
			"再 handoff stop %s 回收（原因：%v）", taskID, rerr))
		return
	}
	m.log.Info("按恢复凭据兜底回收成功", "task", taskID)
}

// notifyOrphanRisk 追加一条「executor 可能残留」的 progress 事件并广播。
//
// 参数：
//   - taskID: 目标任务
//   - text: 面向审核者的正文；给的必须是「下一步做什么」而不是「出了什么错」
//
// 注意：
//   - 追加失败只记日志、不返回错误：调用方全都处在归档/中止的收尾路径上，
//     那件事本身已经达成，不该因为发不出提示而中断
func (m *Manager) notifyOrphanRisk(taskID, text string) {
	evt, err := m.st.AppendEvent(taskID, proto.EventTypeProgress, progressPayload{Text: text})
	if err != nil {
		m.log.Error("追加 executor 残留提示事件失败", "task", taskID, "cause", err)
		return
	}
	m.hub.Publish(evt)
	m.log.Info("已向审核者发出 executor 残留提示", "task", taskID)
}

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
//
// 「executor 已不在 ⇒ failed」与「会话被 abort ⇒ question」的异同（B38 Task9 订正）：
//   - 本函数把「executor 已不在」一律落 failed——serve 意外退出（崩溃/OOM/被杀）
//     是异常，判 failed 是对的：那是执行器确实死了，任务无法继续，交审核者裁决
//   - 对比：opencode 会话被**人工 abort** 解开时（error.name=MessageAbortedError），
//     adapter 的会话对账把它补发成 **question** 而非 failed——abort 在真实使用里
//     几乎总是救援动作（解开冻结/卡死会话），释放出来的是完整内容，不是任务失败。
//     两者本质区别：serve 崩溃是「执行器无征兆死亡」（异常，failed 正确）；abort
//     是「有人主动打断了回合」（救援，question 正确）。别把两者合并成一种落点
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
