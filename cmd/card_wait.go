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
	"sort"
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

// cardSnapshotLine 是 card wait 建连时输出的只读卡状态快照。
//
// 边界：它只描述建连时的卡状态，不是 ledger.Event，不落 card_events，
// 不推进 seq；Follow 进入后仍只输出 ledger.Event。needs_reason 始终出线，
// 让消费方能区分「没有当前原因」与「生产端漏了字段」。
type cardSnapshotLine struct {
	Type        string `json:"type"`
	CardID      string `json:"card_id"`
	Status      string `json:"status"`
	NeedsHuman  bool   `json:"needs_human"`
	NeedsReason string `json:"needs_reason"`
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

// runCardWait 账本单流多路 wait：先输出建连时每个成员的卡快照，再从当前
// seq 起跟子树事件（每行一个 JSON 对象到 stdout），全部成员达骨架终态
// （已完成/终止）即退出 0。快照不是事件，不改变 seq 或 Follow 游标；成员集
// 只有 Follow 期间继续按原语义动态重算。timeout 是总时长（0=不限），超时
// 退出码 124 与单 task wait 一致。
func runCardWait(cmd *cobra.Command, cardID string, subtree bool, timeout time.Duration) error {
	slog.SetDefault(logx.Setup("cli", ""))
	slog.Info("card wait 开始", "card", cardID, "subtree", subtree, "timeout", timeout.String())

	st, err := openLedger()
	if err != nil {
		slog.Error("card wait 打开账本失败", "card", cardID, "cause", err)
		return err
	}
	defer func() {
		if closeErr := st.Close(); closeErr != nil {
			slog.Warn("card wait 关闭账本失败", "card", cardID, "cause", closeErr)
		}
	}()
	slog.Debug("card wait 账本已打开", "card", cardID)
	slog.Debug("card wait 读取根卡", "card", cardID)
	if card, getErr := st.GetCard(cardID); getErr != nil {
		slog.Error("card wait 读取根卡失败", "card", cardID, "cause", getErr)
		return getErr
	} else {
		slog.Debug("card wait 根卡已确认", "card", cardID, "status", card.Status)
	}
	members := func() ([]string, error) {
		if subtree {
			return st.Subtree(cardID)
		}
		return []string{cardID}, nil
	}
	slog.Debug("card wait 读取起始 seq", "card", cardID)
	start, err := st.MaxSeq()
	if err != nil {
		slog.Error("card wait 读取起始 seq 失败", "card", cardID, "cause", err)
		return err
	}
	slog.Debug("card wait 起始 seq 已确定", "card", cardID, "from_seq", start)
	ctx := cmd.Context()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	enc := json.NewEncoder(cmd.OutOrStdout())

	// 快照必须在首次 checkDone 前写出：已完成卡仍保留现有的提前退出与 stderr
	// 语义，但建连消费者先得到一次可解析的当前状态。
	slog.Debug("card wait 解析快照成员", "card", cardID, "subtree", subtree)
	snapshotIDs, err := members()
	if err != nil {
		slog.Error("card wait 解析快照成员失败", "card", cardID, "subtree", subtree, "cause", err)
		return fmt.Errorf("card wait 快照成员: %w", err)
	}
	sort.Strings(snapshotIDs)
	slog.Debug("card wait 快照成员已解析", "card", cardID, "members", snapshotIDs)
	for _, id := range snapshotIDs {
		slog.Debug("card wait 读取快照卡", "root", cardID, "card", id)
		card, getErr := st.GetCard(id)
		if getErr != nil {
			slog.Error("card wait 读取快照卡失败", "root", cardID, "card", id, "cause", getErr)
			return fmt.Errorf("card wait 快照卡 %s: %w", id, getErr)
		}
		slog.Debug("card wait 读取快照 needs", "root", cardID, "card", id)
		needsReason, needsErr := st.NeedsOf(id)
		if needsErr != nil {
			slog.Error("card wait 读取快照 needs 失败", "root", cardID, "card", id, "cause", needsErr)
			return fmt.Errorf("card wait 快照 needs %s: %w", id, needsErr)
		}
		line := cardSnapshotLine{
			Type:        cardSnapshotType,
			CardID:      card.ID,
			Status:      card.Status,
			NeedsHuman:  needsReason != "",
			NeedsReason: needsReason,
		}
		// needs_human 由当前生效原因投影而来；空串是「当前没有标记」，
		// 不是省略字段，避免脚本把缺字段误判为正常无阻塞。
		if encodeErr := enc.Encode(line); encodeErr != nil {
			slog.Error("card wait 写出快照失败", "root", cardID, "card", id, "cause", encodeErr)
			return fmt.Errorf("card wait 写出快照 %s: %w", id, encodeErr)
		}
		slog.Debug("card wait 快照行已输出", "root", cardID, "card", id,
			"status", card.Status, "needs_human", line.NeedsHuman)
	}
	slog.Info("card wait 建连快照已输出", "card", cardID, "members", len(snapshotIDs), "from_seq", start)

	allDone := errors.New("all-done")
	checkDone := func() (bool, error) {
		ids, err := members()
		if err != nil {
			slog.Error("card wait 重算成员失败", "card", cardID, "subtree", subtree, "cause", err)
			return false, err
		}
		for _, id := range ids {
			card, err := st.GetCard(id)
			if err != nil {
				slog.Error("card wait 检查终态读取卡失败", "root", cardID, "card", id, "cause", err)
				return false, err
			}
			if card.Status != ledger.StatusDone && card.Status != ledger.StatusClosed {
				return false, nil
			}
		}
		return true, nil
	}
	if done, checkErr := checkDone(); checkErr != nil {
		slog.Error("card wait 首次终态检查失败", "card", cardID, "cause", checkErr)
		return checkErr
	} else if done {
		slog.Info("card wait 建连时成员已全部完成", "card", cardID, "members", len(snapshotIDs))
		fmt.Fprintln(cmd.ErrOrStderr(), "子树已全部完成")
		return nil
	}

	slog.Debug("card wait 开始跟随", "card", cardID, "from_seq", start, "poll", (2 * time.Second).String())
	slog.Debug("card wait 调用 Follow", "card", cardID, "from_seq", start)
	err = st.Follow(ctx, members, start, 2*time.Second, func(e ledger.Event) error {
		if encodeErr := enc.Encode(e); encodeErr != nil {
			slog.Error("card wait 写出事件失败", "root", cardID, "card", e.CardID,
				"seq", e.Seq, "type", e.Type, "cause", encodeErr)
			return encodeErr
		}
		slog.Debug("card wait 事件已输出", "root", cardID, "card", e.CardID,
			"seq", e.Seq, "type", e.Type)
		if e.Type != ledger.EvStatusMoved {
			return nil
		}
		if done, checkErr := checkDone(); checkErr != nil {
			slog.Error("card wait 事件后终态检查失败", "card", cardID, "seq", e.Seq, "cause", checkErr)
			return checkErr
		} else if done {
			return allDone
		}
		return nil
	})
	slog.Debug("card wait 跟随返回", "card", cardID, "from_seq", start, "cause", err)
	switch {
	case errors.Is(err, allDone):
		slog.Info("card wait 全部成员完成", "card", cardID)
		fmt.Fprintln(cmd.ErrOrStderr(), "子树全部完成，wait 退出")
		return nil
	case errors.Is(err, context.DeadlineExceeded):
		slog.Warn("card wait 超时", "card", cardID, "timeout", timeout.String())
		return &exitCodeError{code: ExitTimeout, err: fmt.Errorf("wait --card 超时")}
	case err != nil:
		slog.Error("card wait 跟随失败", "card", cardID, "from_seq", start, "cause", err)
		return err
	default:
		slog.Info("card wait 正常结束", "card", cardID)
		return nil
	}
}

func init() {
	cardWaitCmd.Flags().BoolVar(&cardWaitSubtree, "subtree", false, "扩展到子树（后代 + 并入成员，动态）")
	cardWaitCmd.Flags().DurationVar(&cardWaitTimeout, "timeout", 0, "总时限（如 2h）；到点以 124 退出")
}
