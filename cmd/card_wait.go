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

func init() {
	cardWaitCmd.Flags().BoolVar(&cardWaitSubtree, "subtree", false, "扩展到子树（后代 + 并入成员，动态）")
	cardWaitCmd.Flags().DurationVar(&cardWaitTimeout, "timeout", 0, "总时限（如 2h）；到点以 124 退出")
}
