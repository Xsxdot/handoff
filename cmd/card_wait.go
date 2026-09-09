// card wait：账本单流多路 wait。
//
// 职责：跟一张卡（或其动态重算的子树）的账本事件流，逐事件输出，全部成员
// 达骨架终态即退出。
// 边界：不碰执行域的 task wait（那是 cmd/wait.go 的 handoff wait <task>）；
// 两者是分层关系——外层用本命令管卡的调度，醒来后处置具体 task 事件仍用
// 执行域动词（reply/approve/continue）。
package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/spf13/cobra"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/logx"
	"github.com/Xsxdot/handoff/internal/proto"
)

// cardWaitSubtree 扩展到子树（后代 + 并入成员，每轮动态重算）。
var cardWaitSubtree bool

// cardWaitFollow 开启持续输出；未开启时首个可动作事件输出成功即退出。
var cardWaitFollow bool

// cardWaitTimeout 总时长，0 = 不限；超时以 ExitTimeout(124) 退出，与执行域
// wait 的超时码一致，脚本侧可用同一套判断。
var cardWaitTimeout time.Duration

// cardWaitCmd 阻塞跟随一张卡（或整棵子树）的账本事件流。
var cardWaitCmd = &cobra.Command{
	Use:   "wait <id>",
	Short: "跟随卡的账本事件流（--subtree 跟整棵子树），可动作事件一行一唤醒",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if cardWaitTimeout < 0 {
			return fmt.Errorf("--timeout 必须为正时长（当前 %s）；不设上限请省略该参数", cardWaitTimeout)
		}
		return runCardWait(cmd, args[0], cardWaitSubtree, cardWaitFollow, cardWaitTimeout)
	},
}

// runCardWait 跟随卡或动态子树的账本单流。
//
// 默认模式只输出首个成功编码的可动作原始 ledger.Event 后退出 0；follow
// 模式持续输出可动作事件，直到当前成员全部进入已完成/终止。审计事件
// 始终保留在账本，只在本消费点不写 stdout。默认 timeout 是等待总时长，
// follow timeout 是任意账本事件之间的空闲上限；0 表示不设限，超时为 124。
func runCardWait(cmd *cobra.Command, cardID string, subtree, follow bool, timeout time.Duration) error {
	slog.SetDefault(logx.Setup("cli", ""))
	slog.Info("card wait 入口", "card", cardID, "subtree", subtree, "follow", follow,
		"timeout", timeout.String())
	st, err := openLedger()
	if err != nil {
		slog.Error("card wait 打开账本失败", "card", cardID, "subtree", subtree,
			"follow", follow, "timeout", timeout.String(), "cause", err)
		return fmt.Errorf("card %s wait 打开账本: %w", cardID, err)
	}
	defer st.Close()
	if _, err := st.GetCard(cardID); err != nil {
		slog.Error("card wait 卡不存在或读取失败", "card", cardID, "subtree", subtree,
			"follow", follow, "timeout", timeout.String(), "cause", err)
		return fmt.Errorf("card %s wait 读取卡: %w", cardID, err)
	}
	members := func() ([]string, error) {
		if subtree {
			return st.Subtree(cardID)
		}
		return []string{cardID}, nil
	}
	start, err := st.MaxSeq()
	if err != nil {
		slog.Error("card wait 读取起点失败", "card", cardID, "subtree", subtree,
			"follow", follow, "timeout", timeout.String(), "cause", err)
		return fmt.Errorf("card %s wait 读取起点: %w", cardID, err)
	}
	ctx := cmd.Context()
	noteActivity := func() {}
	stopIdle := func() {}
	timedOut := func() bool { return false }
	if follow && timeout > 0 {
		ctx, noteActivity, stopIdle, timedOut = startCardWaitIdle(ctx, timeout)
		defer stopIdle()
	} else if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	allDone := errors.New("all-done")
	checkDone := func() (bool, error) {
		ids, err := members()
		if err != nil {
			return false, fmt.Errorf("card %s wait 解析成员: %w", cardID, err)
		}
		for _, id := range ids {
			card, err := st.GetCard(id)
			if err != nil {
				return false, fmt.Errorf("card %s wait 读取成员 %s: %w", cardID, id, err)
			}
			if card.Status != ledger.StatusDone && card.Status != ledger.StatusClosed {
				return false, nil
			}
		}
		return true, nil
	}
	if done, err := checkDone(); err != nil {
		slog.Error("card wait 初始终态检查失败", "card", cardID, "subtree", subtree,
			"follow", follow, "timeout", timeout.String(), "cause", err)
		return err
	} else if done {
		slog.Info("card wait 初始即全部终态", "card", cardID, "subtree", subtree,
			"follow", follow)
		fmt.Fprintln(cmd.ErrOrStderr(), "子树已全部完成")
		return nil
	}
	err = st.Follow(ctx, members, start, 2*time.Second, func(e ledger.Event) error {
		noteActivity()
		slog.Debug("card wait 收到账本事件", "card", e.CardID, "seq", e.Seq,
			"type", e.Type, "follow", follow)

		if e.Type == ledger.EvStatusMoved {
			done, err := checkDone()
			if err != nil {
				slog.Error("card wait 终态检查失败", "card", cardID, "seq", e.Seq,
					"type", e.Type, "cause", err)
				return err
			}
			if done {
				slog.Info("card wait 成员全部终态", "card", cardID, "seq", e.Seq,
					"type", e.Type, "follow", follow)
				return allDone
			}
			return nil
		}

		actionable, err := cardWaitEventActionable(st, e)
		if err != nil {
			slog.Error("card wait 事件分类失败", "card", e.CardID, "seq", e.Seq,
				"type", e.Type, "cause", err)
			return err
		}
		if !actionable {
			slog.Debug("card wait 过滤审计事件", "card", e.CardID, "seq", e.Seq,
				"type", e.Type)
			return nil
		}
		if err := enc.Encode(e); err != nil {
			slog.Error("card wait 输出事件失败", "card", e.CardID, "seq", e.Seq,
				"type", e.Type, "cause", err)
			return err
		}
		slog.Info("card wait 已输出可动作事件", "card", e.CardID, "seq", e.Seq,
			"type", e.Type, "follow", follow)
		if !follow {
			return allDone
		}
		return nil
	})
	switch {
	case errors.Is(err, allDone):
		if follow {
			slog.Info("card wait 成员全部终态，follow 退出", "card", cardID)
		} else {
			slog.Info("card wait 首个可动作事件已输出，一次性退出", "card", cardID)
		}
		return nil
	case follow && errors.Is(err, context.Canceled) && timedOut():
		slog.Error("card wait follow 空闲超时", "card", cardID, "subtree", subtree,
			"follow", follow, "timeout", timeout.String(), "cause", err)
		return &exitCodeError{code: ExitTimeout, err: fmt.Errorf("wait --card 空闲超时")}
	case errors.Is(err, context.DeadlineExceeded):
		slog.Error("card wait 总时长超时", "card", cardID, "subtree", subtree,
			"follow", follow, "timeout", timeout.String(), "cause", err)
		return &exitCodeError{code: ExitTimeout, err: fmt.Errorf("wait --card 超时")}
	default:
		if err != nil {
			slog.Error("card wait 异常退出", "card", cardID, "subtree", subtree,
				"follow", follow, "timeout", timeout.String(), "cause", err)
		}
		return err
	}
}

// cardWaitCurrentWorkflowAttempt 从事件所属卡的全量事件流取指定节点当前的有效派发快照。
// 事件卡而非 wait 根卡是身份事实的归属；只接受 Node、Attempt 非空且
// TaskID == Attempt 的快照，并按 seq 升序保留最后一个合格快照。
func cardWaitCurrentWorkflowAttempt(st *ledger.Store, cardID, node string) (snapshot ledger.DispatchSnapshot, found bool, err error) {
	const pageSize = 500
	from := int64(0)
	for {
		events, readErr := st.EventsFromAsc([]string{cardID}, from, pageSize)
		if readErr != nil {
			slog.Error("card wait 读取当前 workflow attempt 失败", "card", cardID,
				"node", node, "from_seq", from, "cause", readErr)
			return ledger.DispatchSnapshot{}, false,
				fmt.Errorf("card %s 当前派发快照读取: %w", cardID, readErr)
		}
		slog.Debug("card wait 读取当前 workflow attempt 分页", "card", cardID,
			"node", node, "from_seq", from, "event_count", len(events), "page_size", pageSize)
		for _, event := range events {
			if event.Seq > from {
				from = event.Seq
			}
			if event.Type != ledger.EvDispatched {
				continue
			}
			var candidate ledger.DispatchSnapshot
			if decodeErr := json.Unmarshal(event.Payload, &candidate); decodeErr != nil {
				slog.Error("card wait 派发快照解码失败", "card", cardID,
					"seq", event.Seq, "type", event.Type, "node", node, "cause", decodeErr)
				return ledger.DispatchSnapshot{}, false,
					fmt.Errorf("card %s 派发快照 seq=%d type=%s 解码: %w", cardID, event.Seq, event.Type, decodeErr)
			}
			if candidate.Node == node && candidate.Node != "" && candidate.Attempt != "" &&
				candidate.TaskID == candidate.Attempt {
				snapshot = candidate
				found = true
				slog.Debug("card wait 命中合格派发快照", "card", cardID, "seq", event.Seq,
					"type", event.Type, "node", node, "attempt", candidate.Attempt,
					"target", candidate.Target)
			}
		}
		if len(events) < pageSize {
			return snapshot, found, nil
		}
	}
}

// cardWaitEventActionable 根据 source identity、唯一任务等待策略与卡原生动作集合分类事件。
// 返回错误时表示 payload 已损坏或缺失，调用方必须携带事件上下文返回，不能
// 把无法判断的事件静默降级为审计。task_mirrored 只有身份闸通过后才进入策略判断。
func cardWaitEventActionable(st *ledger.Store, ev ledger.Event) (bool, error) {
	switch ev.Type {
	case ledger.EvNeedsHuman, ledger.EvNeedsCleared,
		ledger.EvDecisionOpened, ledger.EvDecisionAnswered:
		return true, nil
	case ledger.EvRoomMessage:
		var msg proto.RoomMessage
		if err := json.Unmarshal(ev.Payload, &msg); err != nil {
			return false, fmt.Errorf("card %s room_message seq=%d type=%s 解码: %w",
				ev.CardID, ev.Seq, ev.Type, err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(ev.Payload, &fields); err != nil {
			return false, fmt.Errorf("card %s room_message seq=%d type=%s 字段解码: %w",
				ev.CardID, ev.Seq, ev.Type, err)
		}
		if raw, present := fields["by_system"]; present &&
			bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return false, nil
		}
		return msg.Kind == proto.RoomMsgUser && !msg.BySystem, nil
	case ledger.EvTaskMirrored:
		var envelope struct {
			Node     *string         `json:"node"`
			Attempt  *string         `json:"attempt"`
			TaskType string          `json:"task_type"`
			Payload  json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(ev.Payload, &envelope); err != nil {
			return false, fmt.Errorf("card %s task_mirrored seq=%d type=%s 解包: %w",
				ev.CardID, ev.Seq, ev.Type, err)
		}
		if envelope.TaskType == "" || len(envelope.Payload) == 0 {
			return false, fmt.Errorf("card %s task_mirrored seq=%d type=%s 缺 task_type/payload",
				ev.CardID, ev.Seq, ev.Type)
		}
		if envelope.Node == nil || *envelope.Node == "" || envelope.Attempt == nil || *envelope.Attempt == "" {
			slog.Info("card wait task_mirrored 因缺失或空 workflow 身份跳过", "card", ev.CardID,
				"seq", ev.Seq, "type", ev.Type, "task_type", envelope.TaskType,
				"reason", "missing_workflow_identity")
			return false, nil
		}
		snapshot, found, err := cardWaitCurrentWorkflowAttempt(st, ev.CardID, *envelope.Node)
		if err != nil {
			return false, err
		}
		if !found || snapshot.TaskID != snapshot.Attempt || snapshot.Attempt != *envelope.Attempt ||
			ev.SourceTask != snapshot.Attempt || ev.SourceTarget != snapshot.Target {
			slog.Info("card wait task_mirrored 因 source identity 不匹配跳过", "card", ev.CardID,
				"seq", ev.Seq, "type", ev.Type, "node", *envelope.Node,
				"attempt", *envelope.Attempt, "current_attempt", snapshot.Attempt,
				"source_task", ev.SourceTask, "source_target", ev.SourceTarget,
				"current_target", snapshot.Target, "source_seq", ev.SourceSeq,
				"task_type", envelope.TaskType, "reason", "source_identity_mismatch")
			return false, nil
		}
		slog.Debug("card wait task_mirrored 通过 source identity 闸", "card", ev.CardID,
			"seq", ev.Seq, "type", ev.Type, "node", *envelope.Node,
			"attempt", *envelope.Attempt, "source_task", ev.SourceTask,
			"source_target", ev.SourceTarget, "current_target", snapshot.Target,
			"source_seq", ev.SourceSeq, "task_type", envelope.TaskType)
		return client.WaitDeliveryPolicy(proto.EventType(envelope.TaskType)), nil
	default:
		return false, nil
	}
}

// startCardWaitIdle 建立 follow 的空闲计时控制器。计时以 Store.Follow 收到
// 任意账本事件为准，过滤掉的审计事件也会调用 noteActivity；stop 等待控制
// goroutine 退出，避免一次性 CLI 测试或命令结束后遗留后台生命周期。
func startCardWaitIdle(parent context.Context, idle time.Duration) (
	ctx context.Context, noteActivity func(), stop func(), timedOut func() bool,
) {
	if idle <= 0 {
		return parent, func() {}, func() {}, func() bool { return false }
	}
	ctx, cancel := context.WithCancel(parent)
	// 每条账本事件都必须和 idle 控制器完成一次握手。容量 1 的非阻塞
	// 通知会把同一批回调压成一条，导致计时从批次中较早事件而非最后事件
	// 刷新；无缓冲通道让 Store.Follow 的回调在每次刷新后再继续。
	activity := make(chan struct{})
	expired := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(idle)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-activity:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(idle)
			case <-timer.C:
				close(expired)
				cancel()
				return
			}
		}
	}()
	noteActivity = func() {
		select {
		case activity <- struct{}{}:
		case <-ctx.Done():
			// 父 context 或 idle timer 已停止控制器；不能再等待接收者。
		}
	}
	stop = func() {
		cancel()
		<-done
	}
	timedOut = func() bool {
		select {
		case <-expired:
			return true
		default:
			return false
		}
	}
	return ctx, noteActivity, stop, timedOut
}

func init() {
	cardWaitCmd.Flags().BoolVar(&cardWaitSubtree, "subtree", false, "扩展到子树（后代 + 并入成员，动态）")
	cardWaitCmd.Flags().BoolVar(&cardWaitFollow, "follow", false, "持续输出可动作事件，全部成员终态才退出")
	cardWaitCmd.Flags().DurationVar(&cardWaitTimeout, "timeout", 0,
		"超时（如 2h）；默认=等待总时长，--follow=空闲上限，到点以 124 退出")
}
