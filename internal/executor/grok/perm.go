// perm.go —— ACP 权限门：挂起表、裁决回发与连接断开时的处置。
//
// 职责：
//   - 暂存 toolCallId → ACP 请求 id，等 manager 的裁决回来后经 Reply 回发
//   - 把 handoff 的 once/reject 翻译为 ACP 的 allow-once/reject-once
//   - 连接断开时作废全部挂起项并告知调用方
//
// 边界：
//   - 不做审批判断：批不批由 manager 依协调者/审批者的应答决定，本层只转发
//   - 不碰 store：工单、黑名单、升级链全在 manager
//
// 为什么需要挂起表（opencode 没有）：ACP 的权限是 agent→client 的**阻塞式
// JSON-RPC 请求**，应答必须带原请求 id 回发；而 opencode 的权限应答是一次
// 独立的 HTTP POST，无需保留连接级状态。
//
// 为什么断开即作废且不再尝试救回：spike 实测 WS 断开后重连 + session/load
// 成功，但未决的权限请求**不会被重发**，grok 侧那次工具调用永久卡在等应答。
// 此时假装恢复成功比直接失败更危险——任务会静止而无人知晓。
package grok

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Xsxdot/handoff/internal/executor"
)

// optionIDFor 把 handoff 的裁决翻译为 ACP 的 optionId。
//
// fail-closed：未知裁决一律按拒绝，绝不误放行——误拒的代价是协调者再来一轮，
// 误放的代价可能是不可逆的破坏性操作。
func optionIDFor(decision string) string {
	if decision == "once" {
		return "allow-once"
	}
	return "reject-once"
}

// notePending 登记一个待裁决的权限请求。
//
// 参数：
//   - toolCallID: ACP 的 toolCallId，manager 经它应答（PermissionID 与之同名）
//   - reqID: ACP 请求 id，应答回发必需
//   - desc: 人类可读的权限描述；拒绝时记入被拒清单，不用 toolCallId
func (r *runState) notePending(toolCallID string, reqID json.RawMessage, desc string) {
	r.pendMu.Lock()
	defer r.pendMu.Unlock()
	r.pending[toolCallID] = pendingPerm{reqID: reqID, desc: desc}
}

// takePending 取出并移除挂起项。
func (r *runState) takePending(toolCallID string) (pendingPerm, bool) {
	r.pendMu.Lock()
	defer r.pendMu.Unlock()
	pp, ok := r.pending[toolCallID]
	delete(r.pending, toolCallID)
	return pp, ok
}

// restorePending keeps a failed-to-send permission request available for retry.
// The ACP peer is still waiting when Reply fails, so its waiting window must
// remain open until a later successful response.
func (r *runState) restorePending(toolCallID string, pp pendingPerm) {
	r.pendMu.Lock()
	defer r.pendMu.Unlock()
	if _, exists := r.pending[toolCallID]; !exists {
		r.pending[toolCallID] = pp
	}
}

// voidAllPending 作废全部挂起项，返回作废数量。
func (r *runState) voidAllPending() int {
	r.pendMu.Lock()
	defer r.pendMu.Unlock()
	n := len(r.pending)
	r.pending = map[string]pendingPerm{}
	return n
}

// noteRejected 记下本回合被拒的权限描述，回合收尾时一并交代给协调者。
func (r *runState) noteRejected(desc string) {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	r.rejected = append(r.rejected, desc)
}

// takeRejected 取走并清空本回合的被拒记录。
func (r *runState) takeRejected() []string {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	out := r.rejected
	r.rejected = nil
	return out
}

// rejectedTurnQuestion 把被拒清单拼成交给协调者的问题。
func rejectedTurnQuestion(rejected []string) string {
	var b strings.Builder
	b.WriteString("本回合有权限请求被拒，模型可能改用其它做法或停下。被拒清单：\n")
	for _, d := range rejected {
		b.WriteString("  - " + d + "\n")
	}
	b.WriteString("请确认下一步该怎么做。")
	return b.String()
}

// PermissionsVolatile 表明本 adapter 的权限请求随连接消亡。
//
// manager 据此在 agentd 重启后拒绝恢复「尚有未决权限工单」的任务——实测
// session/load 只恢复会话历史，不恢复未决授权请求（见 spec §5.2）。
func (a *Adapter) PermissionsVolatile() bool { return true }

// RespondPermission 应答 grok 的权限请求。
//
// 参数：
//   - taskID: 目标任务
//   - permID: 权限请求 id（即 ACP 的 toolCallId，裸值不带命名空间前缀）
//   - decision: "once"（批准本次）或 "reject"（拒绝）
//   - reason: ACP 的 outcome 只有 optionId，带不了消息，本 adapter 忽略（spec §2.5）
//
// 返回：
//   - 任务不在运行中、或挂起表查不到该 permID 时，包装 executor.ErrTaskNotRunning
//     ——两者都意味着「executor 侧那次请求已经不在了」，调用方据此转失败交协调者，
//     而不是当作可重试的瞬时错误
func (a *Adapter) RespondPermission(ctx context.Context, taskID, permID, decision, _ string) (err error) {
	r := a.lookup(taskID)
	if r == nil {
		a.log.Warn("权限应答时任务不在运行中", "task", taskID, "perm", permID)
		return fmt.Errorf("任务 %s 无运行态: %w", taskID, executor.ErrTaskNotRunning)
	}
	pp, ok := r.takePending(permID)
	if !ok {
		a.log.Warn("权限应答找不到挂起请求（连接已重建或已作废）",
			"task", taskID, "perm", permID)
		return fmt.Errorf("权限请求 %s 已不在挂起表: %w", permID, executor.ErrTaskNotRunning)
	}

	opt := optionIDFor(decision)
	a.log.Info("回发权限裁决", "task", taskID, "perm", permID, "decision", decision, "option", opt)
	if err := r.cli.Reply(pp.reqID, map[string]any{
		"outcome": map[string]any{"outcome": "selected", "optionId": opt},
	}); err != nil {
		r.restorePending(permID, pp)
		a.log.Error("回发权限裁决失败", "task", taskID, "perm", permID, "cause", err)
		return fmt.Errorf("回发权限裁决: %w", err)
	}
	if opt == "reject-once" {
		// 记入被拒清单的是权限描述而非 permID：被拒清单存在的意义是让协调者知道
		// 「模型刚才想干什么、被挡了」，一串不透明 toolCallId 等于没说（见
		// rejectedTurnQuestion）。desc 用完整文本，长度收口由回合收尾处的
		// turn.ClampQuestion 负责，不在本处截短。
		r.noteRejected(pp.desc)
	}
	entries := r.seg.Resume(permID)
	if len(entries) == 0 {
		a.log.Warn("grok 权限裁决已送达但未找到等待窗口",
			"task", taskID, "perm", permID)
	} else {
		a.reportTiming(r, entries)
	}
	a.log.Info("权限裁决已送达 executor", "task", taskID, "perm", permID)
	return nil
}
