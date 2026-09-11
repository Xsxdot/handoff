// card wait：账本单流多路 wait。
//
// 职责：跟一张卡（或其动态重算的子树）的账本事件流，逐事件输出，全部成员
// 达骨架终态即退出。
// 边界：不碰执行域的 task wait（那是 cmd/wait.go 的 handoff wait <task>）；
// 两者是分层关系——外层用本命令管卡的调度，醒来后处置具体 task 事件仍用
// 执行域动词（reply/approve/continue）。
package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/spf13/cobra"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/logx"
)

// cardWaitSubtree 扩展到子树（后代 + 并入成员，每轮动态重算）。
var cardWaitSubtree bool

// cardWaitTimeout 总时长，0 = 不限；超时以 ExitTimeout(124) 退出，与执行域
// wait 的超时码一致，脚本侧可用同一套判断。
var cardWaitTimeout time.Duration

const cardSnapshotType = "card_snapshot"

// cardSnapshotLine 是 card wait 建连时输出的只读快照。不回放历史事件：
// 子树成员集会漂，欠单用派生视图（与看板 OpenTicketCounts 同一把尺）。
type cardSnapshotLine struct {
	Type       string               `json:"type"`
	CardID     string               `json:"card_id"`
	Subtree    bool                 `json:"subtree"`
	FromSeq    int64                `json:"from_seq"`
	Members    []string             `json:"members"`
	Actionable []cardSnapshotTicket `json:"actionable"`
	Needs      []cardSnapshotNeed   `json:"needs"`
}

type cardSnapshotTicket struct {
	CardID   string          `json:"card_id"`
	Target   string          `json:"source_target"`
	TaskID   string          `json:"source_task"`
	TicketID string          `json:"ticket_id"`
	TaskType string          `json:"task_type"`
	Payload  json.RawMessage `json:"payload"`
}

type cardSnapshotNeed struct {
	CardID string `json:"card_id"`
	Reason string `json:"reason"`
}

// cardWaitCmd 阻塞跟随一张卡（或整棵子树）的账本事件流。
var cardWaitCmd = &cobra.Command{
	Use:   "wait <id>",
	Short: "跟随卡的账本事件流（--subtree 跟整棵子树），全部达终态退出",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if cardWaitTimeout < 0 {
			return fmt.Errorf("--timeout 必须为正时长（当前 %s）；不设上限请省略该参数", cardWaitTimeout)
		}
		return runCardWait(cmd, args[0], cardWaitSubtree, cardWaitTimeout)
	},
}

// runCardWait 账本单流多路 wait：从当前 seq 起跟子树事件（每行一个
// JSON 事件到 stdout），全部成员达骨架终态（已完成/终止）即退出 0。
// 成员集每轮重算——wait 挂起期间新拆/新并入的卡天然进流。timeout 是
// 总时长（0=不限），超时退出码 124 与单 task wait 一致。
func runCardWait(cmd *cobra.Command, cardID string, subtree bool, timeout time.Duration) error {
	st, err := openLedger()
	if err != nil {
		return err
	}
	defer st.Close()
	if _, err := st.GetCard(cardID); err != nil {
		return err
	}
	members := func() ([]string, error) {
		if subtree {
			return st.Subtree(cardID)
		}
		return []string{cardID}, nil
	}
	start, err := st.MaxSeq()
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	slog.SetDefault(logx.Setup("cli", ""))
	enc := json.NewEncoder(cmd.OutOrStdout())
	snapshotIDs, err := members()
	if err != nil {
		slog.Error("card wait 解析快照成员失败", "card", cardID, "subtree", subtree, "cause", err)
		return fmt.Errorf("解析成员集: %w", err)
	}
	if err := encodeCardWaitSnapshot(enc, st, cardID, subtree, start, snapshotIDs); err != nil {
		return err
	}
	allDone := errors.New("all-done")
	checkDone := func() (bool, error) {
		ids, err := members()
		if err != nil {
			return false, err
		}
		for _, id := range ids {
			card, err := st.GetCard(id)
			if err != nil {
				return false, err
			}
			if card.Status != ledger.StatusDone && card.Status != ledger.StatusClosed {
				return false, nil
			}
		}
		return true, nil
	}
	if done, err := checkDone(); err != nil {
		return err
	} else if done {
		fmt.Fprintln(cmd.ErrOrStderr(), "子树已全部完成")
		return nil
	}
	err = st.Follow(ctx, members, start, 2*time.Second, func(e ledger.Event) error {
		if err := enc.Encode(e); err != nil {
			return err
		}
		if e.Type != ledger.EvStatusMoved {
			return nil
		}
		if done, err := checkDone(); err != nil {
			return err
		} else if done {
			return allDone
		}
		return nil
	})
	switch {
	case errors.Is(err, allDone):
		fmt.Fprintln(cmd.ErrOrStderr(), "子树全部完成，wait 退出")
		return nil
	case errors.Is(err, context.DeadlineExceeded):
		return &exitCodeError{code: ExitTimeout, err: fmt.Errorf("wait --card 超时")}
	default:
		return err
	}
}

func encodeCardWaitSnapshot(enc *json.Encoder, st *ledger.Store, cardID string, subtree bool, fromSeq int64, members []string) error {
	want := make(map[string]bool, len(members))
	for _, id := range members {
		want[id] = true
	}
	tickets, err := st.OpenTickets()
	if err != nil {
		slog.Error("card wait 读未决工单失败", "card", cardID, "cause", err)
		return fmt.Errorf("读未决工单: %w", err)
	}
	actionable := make([]cardSnapshotTicket, 0)
	for _, ticket := range tickets {
		if !want[ticket.CardID] {
			continue
		}
		actionable = append(actionable, cardSnapshotTicket{
			CardID: ticket.CardID, Target: ticket.Target, TaskID: ticket.TaskID,
			TicketID: ticket.TicketID, TaskType: ticket.TaskType, Payload: ticket.Payload,
		})
	}
	reasons, err := st.NeedsReasons()
	if err != nil {
		slog.Error("card wait 读等人标记失败", "card", cardID, "cause", err)
		return fmt.Errorf("读等人标记: %w", err)
	}
	needs := make([]cardSnapshotNeed, 0)
	for _, id := range members {
		if reason := reasons[id]; reason != "" {
			needs = append(needs, cardSnapshotNeed{CardID: id, Reason: reason})
		}
	}
	line := cardSnapshotLine{
		Type: cardSnapshotType, CardID: cardID, Subtree: subtree, FromSeq: fromSeq,
		Members: members, Actionable: actionable, Needs: needs,
	}
	if err := enc.Encode(line); err != nil {
		return fmt.Errorf("写 card_snapshot: %w", err)
	}
	slog.Info("card wait 建连快照已输出", "card", cardID, "subtree", subtree,
		"members", len(members), "actionable", len(actionable), "needs", len(needs),
		"from_seq", fromSeq)
	return nil
}

func init() {
	cardWaitCmd.Flags().BoolVar(&cardWaitSubtree, "subtree", false, "扩展到子树（后代 + 并入成员，动态）")
	cardWaitCmd.Flags().DurationVar(&cardWaitTimeout, "timeout", 0, "总时限（如 2h）；到点以 124 退出")
}
