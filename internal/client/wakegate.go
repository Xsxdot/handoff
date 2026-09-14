// 本文件承载 task_mirrored 的镜像身份闸判定正文（B370）。
//
// 职责：把「一条 task_mirrored 该不该叫醒消费者」从两处消费点（cmd/card_wait.go
// 与 internal/agentd/wakeconsumer.go）收成一处共享实现；判定输入 = 事件投影 +
// 该事件所属卡的派发快照读取口，输出 = 交付/不交付 + reason。两份近重复的快照
// 扫描（cmd 侧 cardWaitCurrentWorkflowAttempt / agentd 侧 Server.currentWorkflowAttempt）
// 退役，取数归本文件的 CurrentWorkflowAttempt。
// 边界：不碰账本 schema、不改 envelope、不删账本行、不吞错误；调用点仍负责自身
// 的交付动作（card wait Encode / wakeconsumer 记 seen 与推进游标）与 envelope 解码。
//
// 归属：本包（d_transport）已是 WaitDeliveryPolicy 的宿主，两处消费点都依赖它；
// 身份闸与类型表是同一条「是否叫醒」规则的两半，住一处。代价是新增契约方向
// d_transport→d_ledger（读 ledger.Store 与 ledger.DispatchSnapshot），已在
// codegraph/target.json 显式声明。
//
// B370 Ticket 0（骨架期）：签名、类型与 reason 枚举按契约冻结；降级三分支中
// 「快照无工作流身份」「找不到快照」两条放行语义留待 implement 激活，本提交对
// 「身份缺失或为空」维持与旧行为等价的保守结果（Deliver=false），因此两处消费点
// 的既有测试保持绿。详见 docs/superpowers/specs/b370-contract.md。
package client

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// WakeGateReason 是一次镜像身份闸判定的结论枚举。
// 放行与拦截都必须携带 reason，供两个消费点按分支落日志。
type WakeGateReason string

const (
	// WakeGateEmptyEnvelopeIdentity 表示 envelope 缺 node/attempt 键，或键值对为空串。
	WakeGateEmptyEnvelopeIdentity WakeGateReason = "empty_workflow_identity"
	// WakeGateSnapshotNoWorkflowIdentity 表示能定位该 task 的派发快照，但快照无工作流身份。
	WakeGateSnapshotNoWorkflowIdentity WakeGateReason = "snapshot_without_workflow_identity"
	// WakeGateSnapshotNotFound 表示找不到该 task 的派发快照。
	WakeGateSnapshotNotFound WakeGateReason = "snapshot_not_found"
	// WakeGateCurrentAttempt 表示身份非空且等于当前派发（放行）。
	WakeGateCurrentAttempt WakeGateReason = "current_attempt"
	// WakeGateStaleAttempt 表示能证明是旧 attempt（拦截，防迟到）。
	WakeGateStaleAttempt WakeGateReason = "stale_attempt"
	// WakeGateMissingSourceTask 表示身份非空但缺 source_task（闭集拒绝）。
	WakeGateMissingSourceTask WakeGateReason = "missing_source_task"
)

// WakeGateDecision 是一次判定的结果：是否放行 + 判定依据。
type WakeGateDecision struct {
	// Deliver 为 true 表示该 task_mirrored 可以进入类型表判断（叫醒面）。
	Deliver bool
	Reason  WakeGateReason
}

// WakeGateEvent 是两处消费点喂给身份闸的事件最小投影。
// 消费点的既有 wire 类型不同（cmd 用 ledger.Event、agentd 用 proto.LedgerEvent），
// 各自解码 envelope 后填本结构；Node/Attempt 用指针保留「键缺失」与「空串」之别。
type WakeGateEvent struct {
	CardID       string
	Seq          int64
	Node         *string
	Attempt      *string
	TaskType     string
	SourceTask   string
	SourceTarget string
	SourceSeq    int64
}

// CurrentWorkflowAttempt 从事件所属卡的全量事件流取指定节点当前的有效派发快照。
// 只有 Node、Attempt 非空且 TaskID == Attempt 的快照才是身份事实；事件按 seq 升序
// 分页读到尾，最后一个合格快照是当前身份。旧快照与不自洽快照保留为审计数据。
//
// 本函数是两处消费点退役的近重复快照扫描的唯一取数口，返回完整快照供 source 列比较。
func CurrentWorkflowAttempt(st *ledger.Store, cardID, node string) (snapshot ledger.DispatchSnapshot, found bool, err error) {
	const pageSize = 500
	from := int64(0)
	for {
		events, readErr := st.EventsFromAsc([]string{cardID}, from, pageSize)
		if readErr != nil {
			slog.Error("读取当前 workflow attempt 失败", "card", cardID,
				"node", node, "from_seq", from, "cause", readErr)
			return ledger.DispatchSnapshot{}, false,
				fmt.Errorf("卡 %s 当前派发快照读取: %w", cardID, readErr)
		}
		for _, event := range events {
			if event.Seq > from {
				from = event.Seq
			}
			if event.Type != ledger.EvDispatched {
				continue
			}
			var candidate ledger.DispatchSnapshot
			if decodeErr := json.Unmarshal(event.Payload, &candidate); decodeErr != nil {
				slog.Error("派发快照解码失败", "card", cardID,
					"seq", event.Seq, "type", event.Type, "node", node, "cause", decodeErr)
				return ledger.DispatchSnapshot{}, false,
					fmt.Errorf("卡 %s 派发快照 seq=%d type=%s 解码: %w", cardID, event.Seq, event.Type, decodeErr)
			}
			if candidate.Node == node && candidate.Node != "" && candidate.Attempt != "" &&
				candidate.TaskID == candidate.Attempt {
				snapshot = candidate
				found = true
			}
		}
		if len(events) < pageSize {
			return snapshot, found, nil
		}
	}
}

// JudgeMirroredWake 是 task_mirrored 的身份闸判定正文（B370）。
//
// B370 冻结的判定方向是「能判定才拦」：身份缺失或为空（缺 node/attempt 键，
// 或键值对为空串）时的降级三分支为
//  1. 能定位快照且快照带工作流身份：与当前快照比对，相等放行，不等拦截；
//  2. 能定位快照但快照无工作流身份：无从判迟到 = 放行；
//  3. 找不到快照：无从判迟到 = 放行。
//
// 身份非空时按 B349 正文保留：attempt 等于当前 snapshot.Attempt、
// snapshot.TaskID == snapshot.Attempt、source_task 等于该 Attempt、
// source_target 与当前 Target 相等（两边都空算匹配）；缺 source_task 闭集拒绝；
// !found 不叫醒。本函数只做判定，副作用（记 seen、推进游标、日志等级）归消费点。
//
// Ticket 0 骨架期：本函数对「身份缺失或为空」暂返回与旧行为等价的保守结果
// （Deliver=false），三分支放行语义由 implement 激活；非空身份路径已按 B349 正文
// 完整实现，并在两处消费点接线，使本包到账本的调用边成为活跃边。
func JudgeMirroredWake(st *ledger.Store, ev WakeGateEvent) (WakeGateDecision, error) {
	if ev.Node == nil || *ev.Node == "" || ev.Attempt == nil || *ev.Attempt == "" {
		slog.Info("task_mirrored 因缺失或空 workflow 身份跳过", "card", ev.CardID,
			"seq", ev.Seq, "type", ledger.EvTaskMirrored, "task_type", ev.TaskType,
			"reason", string(WakeGateEmptyEnvelopeIdentity))
		return WakeGateDecision{Deliver: false, Reason: WakeGateEmptyEnvelopeIdentity}, nil
	}
	snapshot, found, err := CurrentWorkflowAttempt(st, ev.CardID, *ev.Node)
	if err != nil {
		return WakeGateDecision{}, err
	}
	if !found || snapshot.TaskID != snapshot.Attempt || snapshot.Attempt != *ev.Attempt ||
		ev.SourceTask != snapshot.Attempt || ev.SourceTarget != snapshot.Target {
		slog.Info("task_mirrored 因 source identity 不匹配跳过", "card", ev.CardID,
			"seq", ev.Seq, "type", ledger.EvTaskMirrored, "node", *ev.Node,
			"attempt", *ev.Attempt, "current_attempt", snapshot.Attempt,
			"source_task", ev.SourceTask, "source_target", ev.SourceTarget,
			"current_target", snapshot.Target, "source_seq", ev.SourceSeq,
			"task_type", ev.TaskType, "reason", string(WakeGateStaleAttempt))
		return WakeGateDecision{Deliver: false, Reason: WakeGateStaleAttempt}, nil
	}
	slog.Debug("task_mirrored 通过 source identity 闸", "card", ev.CardID,
		"seq", ev.Seq, "type", ledger.EvTaskMirrored, "node", *ev.Node,
		"attempt", *ev.Attempt, "source_task", ev.SourceTask,
		"source_target", ev.SourceTarget, "current_target", snapshot.Target,
		"source_seq", ev.SourceSeq, "task_type", ev.TaskType)
	return WakeGateDecision{Deliver: true, Reason: WakeGateCurrentAttempt}, nil
}
