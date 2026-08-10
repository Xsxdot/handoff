// reconcile.go —— 断连窗口的会话对账（B38）。
//
// 职责：
//   - Reconcile：查会话尾部与持久化水位比对，把断连期间错过的回合终态补回事件流
//
// 边界：
//   - 不写 store、不改任务状态：补发的事件经既有 evCh 交给 manager，状态迁移归它
//   - 不发明事件语义：取回的文本交给既有的 turn.ParseTrailer 分类，产出与实时
//     路径同形的 question / result
//   - **不捧回权限请求**：opencode 的消息流里 tool part 只有 callID 没有权限 id，
//     而 RespondPermission 要求真实 id、伪造即 404（更早的 spike 结论，见
//     adapter.go 的 onReconnect 降级告警）。建一张批了也送不回去的工单比不建更糟，
//     故 ReconcileOutcome.Pending 在本 adapter 恒为 0
package opencode

import (
	"context"
	"fmt"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/turn"
)

// Reconcile 把断连期间错过的回合终态补回事件流。
//
// 参数：
//   - ctx: 控制查询超时
//   - taskID: 目标任务；运行态不在（未 Start / 已 Stop）时返回「无运行态」结论而非错误
//
// 返回：
//   - ReconcileOutcome: 结论。Emitted 只可能是 0 或 1（spec §2.2 的不变量）；
//     Pending 恒为 0（见文件头注释）
//   - err: 查会话失败。**调用方收到错误时只记 WARN、不改任何状态**——一次网络
//     抖动不该把能恢复的任务判成不可恢复
//
// 注意：
//   - 首次对账（水位为空串）**不补发**，只把当前尾部认作已消费。否则升级到本
//     版本的存量任务会在第一次恢复时集体重放最后一个回合
//   - 「补发 → 前进水位」在 turnMu 下串行完成，与实时路径的 mapIdle 互斥；
//     拆成两步会让两条路同时判「未消费」而补出两条终态
func (a *Adapter) Reconcile(ctx context.Context, taskID string) (executor.ReconcileOutcome, error) {
	a.log.Info("adapter 开始对账", "task", taskID)

	a.mu.Lock()
	r := a.runs[taskID]
	a.mu.Unlock()
	if r == nil || r.api == nil {
		a.log.Info("对账跳过：该任务无运行态", "task", taskID)
		return executor.ReconcileOutcome{Note: "该任务当前无运行态，无需对账"}, nil
	}

	msg, err := r.api.LastAssistantMessage(ctx, r.session)
	if err != nil {
		a.log.Warn("对账查会话尾部失败，不改任何状态", "task", taskID, "cause", err)
		return executor.ReconcileOutcome{Note: "查会话尾部失败"},
			fmt.Errorf("对账查会话尾部: %w", err)
	}
	if msg == nil {
		a.log.Info("对账结论：会话尚无 assistant 消息", "task", taskID)
		return executor.ReconcileOutcome{Note: "会话里还没有模型消息，无需对账"}, nil
	}
	if msg.CompletedMS == 0 {
		a.log.Info("对账结论：回合仍在进行", "task", taskID, "msg", msg.ID)
		return executor.ReconcileOutcome{Note: "executor 的回合仍在进行中，没有丢失的终态"}, nil
	}

	// 补发与水位前进必须在同一把锁下完成：实时路径的 mapIdle 也走这条判据，
	// 拆开就是 check-then-act，两条路会同时判「未消费」而补出两条终态
	r.turnMu.Lock()
	defer r.turnMu.Unlock()

	pi, err := readProcInfo(r.taskDir)
	if err != nil {
		a.log.Warn("对账读凭据失败，不改任何状态", "task", taskID, "cause", err)
		return executor.ReconcileOutcome{Note: "读恢复凭据失败"},
			fmt.Errorf("对账读恢复凭据: %w", err)
	}
	if pi.LastTurnMsgID == msg.ID {
		a.log.Info("对账结论：终态已送达过，无需补发", "task", taskID, "msg", msg.ID)
		return executor.ReconcileOutcome{TurnEnded: true,
			Note: "回合已完结，且终态此前已送达，无需补发"}, nil
	}
	if pi.LastTurnMsgID == "" {
		// 首次对账：把当前尾部认作已消费。存量任务升级到本版本后水位是空的，
		// 不做这条保护会让它们在第一次恢复时集体重放最后一个回合
		pi.LastTurnMsgID = msg.ID
		if werr := writeProcInfo(r.taskDir, pi); werr != nil {
			a.log.Warn("首次对账写水位失败", "task", taskID, "cause", werr)
		}
		a.log.Info("对账结论：首次对账，已把当前会话尾部认作基线，不补发",
			"task", taskID, "msg", msg.ID)
		return executor.ReconcileOutcome{TurnEnded: true,
			Note: "首次对账，已记录当前进度为基线（不回溯此前的回合）"}, nil
	}

	ev, note := a.classifyReconciled(r, msg)
	if !a.emit(r, ev) {
		a.log.Warn("对账补发失败：事件通道已关闭", "task", taskID, "msg", msg.ID)
		return executor.ReconcileOutcome{TurnEnded: true,
			Note: "回合已完结但事件通道已关闭，未能补发"}, nil
	}
	pi.LastTurnMsgID = msg.ID
	if werr := writeProcInfo(r.taskDir, pi); werr != nil {
		a.log.Warn("对账后写水位失败，下次对账可能重复补发",
			"task", taskID, "msg", msg.ID, "cause", werr)
	}
	a.log.Info("对账完成：已补发断连期间丢失的终态",
		"task", taskID, "msg", msg.ID, "event", ev.Type, "note", note)
	return executor.ReconcileOutcome{TurnEnded: true, Emitted: 1, Note: note}, nil
}

// classifyReconciled 把取回的消息翻译成一条与实时路径同形的 AdapterEvent。
//
// 分类**复用** turn.ParseTrailer——与 mapIdle 走同一套判据，于是以提问收尾的
// 回合会正确地还原成 question 工单，而不是一条假的「做完了」。
//
// 返回：事件本身，以及一句给审核者看的结论。
func (a *Adapter) classifyReconciled(r *runState, msg *SessionMessage) (executor.AdapterEvent, string) {
	if msg.ErrorText != "" {
		a.log.Warn("对账发现回合以错误告终", "task", r.taskID, "msg", msg.ID,
			"error", turn.TruncateRunes(msg.ErrorText, 200))
		return executor.AdapterEvent{Type: "result", SessionID: r.session,
				Result: &executor.Result{OK: false,
					FailReason: "回合在 agentd 断连期间以错误告终：" +
						turn.TruncateRunes(msg.ErrorText, 200)}},
			"补回了一条断连期间丢失的失败结果"
	}
	kind, t := turn.ParseTrailer(msg.Text)
	switch kind {
	case "ask":
		return executor.AdapterEvent{Type: "question", SessionID: r.session,
			Text: turn.ClampQuestion(t.Question)}, "补回了一条断连期间丢失的提问"
	case "finish":
		return executor.AdapterEvent{Type: "result", SessionID: r.session,
			Result: &executor.Result{OK: true, Branch: t.Branch, CommitHash: t.Commit,
				Summary: t.Summary, SessionID: r.session}}, "补回了一条断连期间丢失的完成结果"
	}
	// 无协议 trailer：不走 mapIdle 的 git 兜底（那套依赖 startCommit 基线，而
	// 断连期间基线已失去意义）。交审核者裁决，把回合原文给他
	a.log.Warn("对账发现回合无协议 trailer，转提问交审核者裁决",
		"task", r.taskID, "msg", msg.ID)
	return executor.AdapterEvent{Type: "question", SessionID: r.session,
		Text: turn.ClampQuestion("agentd 断连期间该回合已结束，但未输出协议结论。" +
			"回合原文：\n" + turn.TailRunes(msg.Text, 1000))}, "补回了一条断连期间丢失的回合，需人工裁决"
}

// reconcileAfterRecovery 是两个自动触发点共用的对账入口。
//
// 参数：
//   - ctx: 控制查询超时
//   - taskID: 目标任务
//   - trigger: 触发来源，只用于日志（"startup" = agentd 启动恢复，
//     "reconnect" = 连接断开重连）
//
// 注意：
//   - **不返回错误，且绝不 panic**：spec §6.3 的硬要求——对账失败不能阻断恢复。
//     一次网络抖动若能让 Resume 判不可恢复，比 B38 本身还糟
func (a *Adapter) reconcileAfterRecovery(ctx context.Context, taskID, trigger string) {
	out, err := a.Reconcile(ctx, taskID)
	if err != nil {
		a.log.Warn("恢复后对账失败，恢复本身不受影响",
			"task", taskID, "trigger", trigger, "cause", err)
		return
	}
	a.log.Info("恢复后对账完成", "task", taskID, "trigger", trigger,
		"turn_ended", out.TurnEnded, "emitted", out.Emitted, "note", out.Note)
}
