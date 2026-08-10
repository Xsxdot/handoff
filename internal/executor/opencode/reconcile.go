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
//   - 是否补发由 WatermarkArmed 决定（见 proc.go 的 armed 语义）：未 armed 的
//     legacy 会话**不补发**，只把当前尾部认作已消费基线——否则升级到本版本的
//     存量任务会在第一次恢复时集体重放最后一个回合；armed 会话空水位即「第一个
//     回合尚未消费」，必须补发（B38 头号场景）
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
	ended, reason := reconcileTurnEnded(msg)
	if !ended {
		a.log.Info("对账结论：回合仍在进行，不补发",
			"task", taskID, "msg", msg.ID, "reason", reason)
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
	if !pi.WatermarkArmed {
		// 未 armed：会话不是本版本 agentd 亲手新建的（legacy 任务），空水位不可信——
		// 它分不清「从没消费过」和「上一回合终态已正常送达」。把当前尾部认作基线
		// 不补发，防止升级后第一次恢复集体重放最后一个回合。
		//
		// 为什么 legacy 任务必须保持 unarmed（而不是这里顺手置 true）：多回合任务
		// 的回合 1 终态已送达、任务进 waiting_review，用户 continue 后任务回 running、
		// 回合 2 刚发 prompt 尚无 assistant 消息——会话尾部仍是回合 1 那条 completed。
		// 若把这样会话判成 armed，就会把已审过的回合 1 终态再补发一遍
		pi.LastTurnMsgID = msg.ID
		if werr := writeProcInfo(r.taskDir, pi); werr != nil {
			a.log.Warn("首次对账写水位失败", "task", taskID, "cause", werr)
		}
		a.log.Info("对账结论：未 armed 认基线，已把当前会话尾部记为基线，不补发",
			"task", taskID, "msg", msg.ID, "armed", false)
		return executor.ReconcileOutcome{TurnEnded: true,
			Note: "会话非本版本新建（legacy），已把当前进度记为基线（不回溯此前的回合）"}, nil
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
		"task", taskID, "msg", msg.ID, "event", ev.Type, "note", note, "armed", true)
	return executor.ReconcileOutcome{TurnEnded: true, Emitted: 1, Note: note}, nil
}

// reconcileTurnEnded 判定取回的会话尾部消息是否真的意味着「回合已结束」。
//
// 为什么不能只靠 CompletedMS：opencode 一个用户回合会产多条 assistant 消息，
// 工具调用各自成条、各自带 completed。只凭「最后一条 assistant 的 CompletedMS
// != 0」会把一条纯工具消息误判成回合终态——executor 死亡/崩溃后会话冻结在
// 工具消息上，补一条假终态会把正常任务主动推进到 waiting_answer，比 B38 原始
// 症状（冻死）更糟。
//
// 判据按行逐条判定、命中即停（真机数据见各行的注释出处）：
//
//	1  CompletedMS == 0                         → 未结束（消息未 finalize：在飞或冻结）
//	2  Finish == "stop"                         → 已结束（自然结束，无 tool part）
//	3  ErrorName == "MessageAbortedError"       → 已结束（会话被 abort 而终）
//	4  ToolStatus == "error"                    → 已结束（工具被拒/报错而终）
//	5  ToolStatus == "completed"                → 未结束（真·回合中途冻结）
//	6  兜底（无 tool、无 error、finish 缺席/其它）→ 已结束（窄兜底，见下）
//
// row2 为什么成立（Finish=="stop" ⇒ 回合结束）：stop 是 opencode 的自然结束标记——
// 模型把话说完、本轮不再有工具调用，消息的 finish 就是 "stop"。实测该形态消息
// 的 parts 为 step-start/text/step-finish（无 tool part），是「回合到此为止」的
// 最直接信号。与实时路径 mapIdle 的判定口径一致（文本完整、无续接意图即分类收尾）。
//
// row4 为什么成立（工具 error ⇒ 回合结束）：本会话 14 条带 state.status=error
// 工具 part 的消息 14/14 后面紧跟的都是 user 消息（或就是会话尾），零反例——
// 「工具报错模型会自己重试、回合不结束」的担心被数据否掉。更重要的代码佐证：
// 实时路径 adapter.go:1236-1241 早就把「回合因权限被拒而终止」当作回合结束并转成
// question 唤醒审核者（rejectedTurnQuestion，adapter.go:482-488）——row4 不是
// 新发明，是让对账口径与实时路径对齐。
//
// row6 为什么是窄兜底且判已结束：真实 payload 里 completed 的消息几乎总带 finish
// （456 条：tool-calls 452 / unknown 2 / 缺席 2，缺席那两条一条是 abort、一条是
// 在飞）。落进 row6 的是 finish="unknown"（本会话 2 条，均为真实回合终态、无 tool
// part）与缺 finish 的经典终态——它们补发是对的。row6 不是主路径，但缺失它会让
// 老版本/异常形态的终态永远补不上，故判已结束。
//
// 返回：回合是否已结束，以及命中的判据行（进日志，供线上判定回溯）。
func reconcileTurnEnded(msg *SessionMessage) (bool, string) {
	if msg.CompletedMS == 0 {
		return false, "unfinalized" // row1：消息未 finalize（在飞或 completed=null 冻结）
	}
	if msg.Finish == "stop" {
		return true, "stop" // row2：自然结束（step-start/text/step-finish，无 tool part）
	}
	if msg.ErrorName == "MessageAbortedError" {
		return true, "aborted" // row3：会话被 abort 而终（finish 缺席）
	}
	if msg.ToolStatus == "error" {
		return true, "tool_error" // row4：工具被拒/报错而终（finish=tool-calls）
	}
	if msg.ToolStatus == "completed" {
		return false, "frozen_tool_tail" // row5：真·回合中途冻结（工具完成但回合未完）
	}
	return true, "fallback_terminal" // row6：窄兜底（finish=unknown 等）
}

// classifyReconciled 把取回的消息翻译成一条与实时路径同形的 AdapterEvent。
//
// 分类**复用** turn.ParseTrailer——与 mapIdle 走同一套判据，于是以提问收尾的
// 回合会正确地还原成 question 工单，而不是一条假的「做完了」。
//
// 返回：事件本身，以及一句给审核者看的结论。
func (a *Adapter) classifyReconciled(r *runState, msg *SessionMessage) (executor.AdapterEvent, string) {
	// 会话被 abort（row3）：走 question 而不是 result{OK:false}。
	//
	// 为什么 abort 不是任务失败：abort 在真实使用里几乎总是**人工救援动作**——
	// 把冻结/卡死的会话解开。解开释放出来的往往是完整且有价值的内容（设计、提问、
	// 判据），而它既不是「任务完成」也不是「任务失败」，只是停在半路等人指路，
	// 和 row4（工具被拒而终）同形。若把它判成 result{OK:false}，一次成功的救援会
	// 被翻译成任务失败，且 FailReason 只带 error 文本、**消息正文会被丢掉**——
	// 比 B38 原始症状还糟。
	//
	// 为什么必须在 ErrorText 分支之前：abort 的消息两个字段都有值（ErrorText 存
	// 原文、ErrorName 存 name），若不先判 ErrorName，会被下面的 ErrorText 分支截住。
	// 全库 8236 条 assistant 消息里 error.name 分布：无 error 8232 / MessageAbortedError 4 /
	// 字符串形态 0——**MessageAbortedError 是本机唯一出现过的 error 形态**，也就是
	// 说 ErrorText 分支实际上只会被 abort 触发。把 abort 摘出来、保留 ErrorText 兜
	// 未知错误（模型报错/provider 失败仍可能有别的形态），两条都站得住。
	if msg.ErrorName == "MessageAbortedError" {
		text := "断连期间该会话被人工 abort 解开（可能是救援动作）。回合停在这里等人指路，"
		if msg.Text != "" {
			text += "回合原文：\n" + turn.TailRunes(msg.Text, 1000)
		}
		a.log.Warn("对账发现回合被 abort，转提问交审核者裁决",
			"task", r.taskID, "msg", msg.ID)
		return executor.AdapterEvent{Type: "question", SessionID: r.session,
				Text: turn.ClampQuestion(text)},
			"补回了一条断连期间被 abort 的回合（需人工裁决，含原文）"
	}
	if msg.ErrorText != "" {
		a.log.Warn("对账发现回合以错误告终", "task", r.taskID, "msg", msg.ID,
			"error", turn.TruncateRunes(msg.ErrorText, 200))
		return executor.AdapterEvent{Type: "result", SessionID: r.session,
				Result: &executor.Result{OK: false,
					FailReason: "回合在 agentd 断连期间以错误告终：" +
						turn.TruncateRunes(msg.ErrorText, 200)}},
			"补回了一条断连期间丢失的失败结果"
	}
	// 工具被拒/报错而终（row4）：实时路径把「回合因权限被拒而终止」转成 question
	// 唤醒审核者（rejectedTurnQuestion，adapter.go:1236-1241），对账补发必须同形——
	// 这里没有实时路径的 turnRejected 清单，但工具 error 本身就是要告诉审核者的结论。
	// 文本取消息文本（可能是空：纯工具消息无 text），有文本则带原文
	if msg.ToolStatus == "error" {
		text := "断连期间该回合以工具被拒或工具报错告终"
		if msg.Text != "" {
			text += "，回合原文：\n" + turn.TailRunes(msg.Text, 1000)
		}
		a.log.Warn("对账发现回合以工具错误告终，转提问交审核者裁决",
			"task", r.taskID, "msg", msg.ID)
		return executor.AdapterEvent{Type: "question", SessionID: r.session,
				Text: turn.ClampQuestion(text)},
			"补回了一条断连期间丢失的回合（工具被拒/报错而终），需人工裁决"
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
