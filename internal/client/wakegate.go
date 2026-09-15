// 本文件承载 task_mirrored 的镜像身份闸判定正文（B370）。
//
// 职责：把「一条 task_mirrored 该不该叫醒消费者」从两处消费点（cmd/card_wait.go
// 与 internal/agentd/wakeconsumer.go）收成一处共享实现；判定输入 = 事件投影 +
// 该事件所属卡的派发快照读取口，输出 = 交付/不交付 + reason。
// 边界：不碰账本 schema、不改 envelope、不删账本行、不吞错误；调用点仍负责自身
// 的交付动作（card wait Encode / wakeconsumer 记 seen 与推进游标）与 envelope 解码。
//
// 降级方向（B370「能判定才拦」）：身份缺失或为空时不再一律拒绝，改用 source 列
// 与当前派发快照比对，只在能证明是旧 attempt 时才拦。判定正文只此一处，
// 两份近重复的快照扫描（cmd 侧 cardWaitCurrentWorkflowAttempt / agentd 侧
// Server.currentWorkflowAttempt）已退役，取数归本文件的 scanCardDispatchSnapshots。
//
// 归属：本包（d_transport）已是 WaitDeliveryPolicy 的宿主，两处消费点都依赖它；
// 身份闸与类型表是同一条「是否叫醒」规则的两半，住一处。代价是新增契约方向
// d_transport→d_ledger（读 ledger.Store 与 ledger.DispatchSnapshot），已在
// codegraph/target.json 显式声明。
//
// 详见 docs/superpowers/specs/b370-contract.md 与 docs/superpowers/plans/b370-plan.md。
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
	// WakeGateEmptyEnvelopeIdentity 是身份缺失/为空这一输入形态的分类标签
	// （用于降级判定入口日志的 identity_state），不再是判定结论。
	WakeGateEmptyEnvelopeIdentity WakeGateReason = "empty_workflow_identity"
	// WakeGateSnapshotNoWorkflowIdentity 表示有派发但快照无工作流身份 = 无从判迟到 = 放行。
	WakeGateSnapshotNoWorkflowIdentity WakeGateReason = "snapshot_without_workflow_identity"
	// WakeGateSnapshotNotFound 表示找不到该 task 的派发快照 = 无从判迟到 = 放行。
	WakeGateSnapshotNotFound WakeGateReason = "snapshot_not_found"
	// WakeGateCurrentAttempt 表示身份（envelope 或 source 列）等于当前派发（放行）。
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
// node 为空不是有效的身份查询键，恒返回 found=false（B349 契约保留）。
func CurrentWorkflowAttempt(st *ledger.Store, cardID, node string) (snapshot ledger.DispatchSnapshot, found bool, err error) {
	if node == "" {
		return ledger.DispatchSnapshot{}, false, nil
	}
	snapshot, found, _, err = scanCardDispatchSnapshots(st, cardID, node)
	return snapshot, found, err
}

// scanCardDispatchSnapshots 是身份闸唯一的快照取数口（B370 F2）。
//
// 参数：cardID 是事件所属卡；node 非空时只接受 Node == node 的快照，
// node 为空表示不过滤节点（F1 空身份定位用同一张卡上的最新合格快照）。
//
// 返回：
//   - snapshot/qualified：最新（最大事件 seq）合格快照与「存在合格快照」；
//   - anyDispatch：该卡是否存在任何 EvDispatched（区分降级分支 2「有派发无身份」
//     与分支 3「无派发」）；
//   - err：读账或 payload 解码失败时带 card/seq/type 上下文返回，不吞。
func scanCardDispatchSnapshots(st *ledger.Store, cardID, node string) (
	snapshot ledger.DispatchSnapshot, qualified bool, anyDispatch bool, err error,
) {
	const pageSize = 500
	from := int64(0)
	for {
		events, readErr := st.EventsFromAsc([]string{cardID}, from, pageSize)
		if readErr != nil {
			slog.Error("读取当前 workflow attempt 失败", "card", cardID,
				"node", node, "from_seq", from, "cause", readErr)
			return ledger.DispatchSnapshot{}, false, false,
				fmt.Errorf("卡 %s 当前派发快照读取: %w", cardID, readErr)
		}
		for _, event := range events {
			if event.Seq > from {
				from = event.Seq
			}
			if event.Type != ledger.EvDispatched {
				continue
			}
			anyDispatch = true
			var candidate ledger.DispatchSnapshot
			if decodeErr := json.Unmarshal(event.Payload, &candidate); decodeErr != nil {
				slog.Error("派发快照解码失败", "card", cardID,
					"seq", event.Seq, "type", event.Type, "node", node, "cause", decodeErr)
				return ledger.DispatchSnapshot{}, false, false,
					fmt.Errorf("卡 %s 派发快照 seq=%d type=%s 解码: %w", cardID, event.Seq, event.Type, decodeErr)
			}
			if node != "" && candidate.Node != node {
				continue
			}
			if candidate.Node == "" || candidate.Attempt == "" || candidate.TaskID != candidate.Attempt {
				continue
			}
			snapshot = candidate
			qualified = true
		}
		if len(events) < pageSize {
			return snapshot, qualified, anyDispatch, nil
		}
	}
}

// JudgeMirroredWake 是 task_mirrored 的身份闸判定正文（B370）。
//
// B370 冻结的判定方向是「能判定才拦」：身份非空走 A 路径（B349 正文保留），
// 身份缺失或为空走降级三分支（见 judgeEmptyEnvelopeIdentity）。本函数只做判定，
// 副作用（记 seen、推进游标、日志等级）归消费点。
//
// 参数：st 为账本；st == nil 时显式返回带上下文错误（F4），不 panic。
func JudgeMirroredWake(st *ledger.Store, ev WakeGateEvent) (WakeGateDecision, error) {
	if st == nil {
		slog.Error("镜像身份闸无法判定：账本未装配", "card", ev.CardID,
			"seq", ev.Seq, "type", ledger.EvTaskMirrored, "task_type", ev.TaskType)
		return WakeGateDecision{}, fmt.Errorf("卡 %s task_mirrored seq=%d: 身份闸账本未装配", ev.CardID, ev.Seq)
	}
	if ev.Node == nil || *ev.Node == "" || ev.Attempt == nil || *ev.Attempt == "" {
		return judgeEmptyEnvelopeIdentity(st, ev)
	}
	// A. 身份非空：按 B349 正文保留。
	if ev.SourceTask == "" {
		slog.Info("task_mirrored 因缺 source_task 跳过", "card", ev.CardID,
			"seq", ev.Seq, "type", ledger.EvTaskMirrored, "node", *ev.Node,
			"attempt", *ev.Attempt, "task_type", ev.TaskType,
			"reason", string(WakeGateMissingSourceTask))
		return WakeGateDecision{Deliver: false, Reason: WakeGateMissingSourceTask}, nil
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
		"source_seq", ev.SourceSeq, "task_type", ev.TaskType,
		"reason", string(WakeGateCurrentAttempt))
	return WakeGateDecision{Deliver: true, Reason: WakeGateCurrentAttempt}, nil
}

// judgeEmptyEnvelopeIdentity 是身份缺失或为空时的降级判定（B370 三分支）。
//
//  1. 能定位快照且快照带工作流身份：与当前快照比对 source 列，相等放行，不等拦截；
//  2. 能定位快照但快照无工作流身份：无从判迟到 = 放行；
//  3. 找不到快照：无从判迟到 = 放行。
//
// 定位规则（F1=A）：node 非空按 (cardID, node) 用最新合格快照定位；node 缺失或空
// 时以同一张卡上的最新合格快照为当前身份。source 三列是权威身份，不读 envelope。
func judgeEmptyEnvelopeIdentity(st *ledger.Store, ev WakeGateEvent) (WakeGateDecision, error) {
	var node string
	if ev.Node != nil {
		node = *ev.Node
	}
	slog.Info("task_mirrored 身份缺失或为空，进入降级判定", "card", ev.CardID,
		"seq", ev.Seq, "type", ledger.EvTaskMirrored, "task_type", ev.TaskType,
		"node_present", ev.Node != nil, "attempt_present", ev.Attempt != nil,
		"node", node, "identity_state", string(WakeGateEmptyEnvelopeIdentity))
	snapshot, qualified, anyDispatch, err := scanCardDispatchSnapshots(st, ev.CardID, node)
	if err != nil {
		return WakeGateDecision{}, err
	}
	if !anyDispatch {
		slog.Info("task_mirrored 身份为空且该卡无派发快照，降级放行", "card", ev.CardID,
			"seq", ev.Seq, "type", ledger.EvTaskMirrored, "task_type", ev.TaskType,
			"source_task", ev.SourceTask, "source_target", ev.SourceTarget,
			"reason", string(WakeGateSnapshotNotFound))
		return WakeGateDecision{Deliver: true, Reason: WakeGateSnapshotNotFound}, nil
	}
	if !qualified {
		slog.Info("task_mirrored 身份为空且派发快照无工作流身份，降级放行", "card", ev.CardID,
			"seq", ev.Seq, "type", ledger.EvTaskMirrored, "task_type", ev.TaskType,
			"source_task", ev.SourceTask, "source_target", ev.SourceTarget,
			"reason", string(WakeGateSnapshotNoWorkflowIdentity))
		return WakeGateDecision{Deliver: true, Reason: WakeGateSnapshotNoWorkflowIdentity}, nil
	}
	if ev.SourceTask == snapshot.Attempt && ev.SourceTarget == snapshot.Target {
		slog.Debug("task_mirrored 空身份按 source 列匹配当前派发放行", "card", ev.CardID,
			"seq", ev.Seq, "type", ledger.EvTaskMirrored, "task_type", ev.TaskType,
			"source_task", ev.SourceTask, "source_target", ev.SourceTarget,
			"current_attempt", snapshot.Attempt, "current_target", snapshot.Target,
			"reason", string(WakeGateCurrentAttempt))
		return WakeGateDecision{Deliver: true, Reason: WakeGateCurrentAttempt}, nil
	}
	slog.Info("task_mirrored 空身份且 source 列不等于当前派发，拦截", "card", ev.CardID,
		"seq", ev.Seq, "type", ledger.EvTaskMirrored, "task_type", ev.TaskType,
		"source_task", ev.SourceTask, "source_target", ev.SourceTarget,
		"current_attempt", snapshot.Attempt, "current_target", snapshot.Target,
		"reason", string(WakeGateStaleAttempt))
	return WakeGateDecision{Deliver: false, Reason: WakeGateStaleAttempt}, nil
}
