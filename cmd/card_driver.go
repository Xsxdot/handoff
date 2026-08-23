// card_driver.go 把账本里的驱动归属生命周期接出 CLI。
// 边界：只调用 ledger.Store 的原子操作，不改变卡状态、不探测会话存活、不经 agentd。
package cmd

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
)

var cardReleaseCmd = &cobra.Command{
	Use:   "release <id>",
	Short: "主动交还卡的驱动归属",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, session := args[0], ledgerSession()
		slog.Default().Info("CLI 释放驱动入口", "card", id, "session", session)
		st, err := openLedger()
		if err != nil {
			slog.Default().Warn("CLI 释放驱动打开账本失败", "card", id, "session", session, "cause", err)
			return err
		}
		defer st.Close()
		slog.Default().Info("CLI 释放驱动调用账本", "card", id, "session", session)
		if err := st.ReleaseCard(id, session); err != nil {
			slog.Default().Warn("CLI 释放驱动失败", "card", id, "session", session, "cause", err)
			return fmt.Errorf("释放卡 %s 的驱动: %w", id, err)
		}
		slog.Default().Info("CLI 释放驱动完成", "card", id, "session", session)
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

var cardTakeoverCmd = &cobra.Command{
	Use:   "takeover <id>",
	Short: "显式接管卡的驱动归属",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, session, actor := args[0], ledgerSession(), ledgerActor()
		slog.Default().Info("CLI 接管驱动入口", "card", id, "session", session, "actor", actor)
		st, err := openLedger()
		if err != nil {
			slog.Default().Warn("CLI 接管驱动打开账本失败", "card", id, "session", session, "actor", actor, "cause", err)
			return err
		}
		defer st.Close()
		slog.Default().Info("CLI 接管驱动调用账本", "card", id, "session", session, "actor", actor)
		if err := st.TakeoverCard(id, session, actor); err != nil {
			slog.Default().Warn("CLI 接管驱动失败", "card", id, "session", session, "actor", actor, "cause", err)
			return fmt.Errorf("接管卡 %s 的驱动: %w", id, err)
		}
		slog.Default().Info("CLI 接管驱动完成", "card", id, "session", session, "actor", actor)
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

func init() {
	cardCmd.AddCommand(cardReleaseCmd, cardTakeoverCmd)
}
