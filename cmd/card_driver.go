// card_driver.go 把账本里的驱动归属生命周期接出 CLI。
// 边界：只调用 ledger.Store 的原子操作，不改变卡状态、不探测会话存活、不经 agentd。
// B239：归属身份降为人尺度 ledgerActor()（不带 pid）；release 在非持有者时
// 从静默假成功反转为可见失败——CLI 退出码非零、stderr 含当前持有者。
package cmd

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
)

var cardReleaseCmd = &cobra.Command{
	Use:   "release <id>",
	Short: "主动交还卡的驱动归属（非持有者会失败并告知持有者）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, owner := args[0], ledgerActor()
		slog.Default().Info("CLI 释放归属入口", "card", id, "owner", owner)
		st, err := openLedger()
		if err != nil {
			slog.Default().Warn("CLI 释放归属打开账本失败", "card", id, "cause", err)
			return err
		}
		defer st.Close()
		if err := st.ReleaseCard(id, owner); err != nil {
			slog.Default().Warn("CLI 释放归属失败", "card", id, "owner", owner, "cause", err)
			return fmt.Errorf("释放卡 %s 的归属: %w", id, err)
		}
		slog.Default().Info("CLI 释放归属完成", "card", id, "owner", owner)
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

var cardTakeoverCmd = &cobra.Command{
	Use:   "takeover <id>",
	Short: "显式接管卡的驱动归属（归属落到人名下）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, owner, actor := args[0], ledgerActor(), ledgerActor()
		slog.Default().Info("CLI 接管归属入口", "card", id, "owner", owner, "actor", actor)
		st, err := openLedger()
		if err != nil {
			slog.Default().Warn("CLI 接管归属打开账本失败", "card", id, "cause", err)
			return err
		}
		defer st.Close()
		if err := st.TakeoverCard(id, owner, actor); err != nil {
			slog.Default().Warn("CLI 接管归属失败", "card", id, "actor", actor, "cause", err)
			return fmt.Errorf("接管卡 %s 的归属: %w", id, err)
		}
		slog.Default().Info("CLI 接管归属完成", "card", id, "owner", owner)
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

var (
	cardRebindTo      string
	cardRebindCarrier string
	cardRebindExpect  string
)

// cardRebindCmd 换绑驱动会话（契约 §3.2/§3.5）：expect=CAS 前值（空=要求当前
// 无绑定），成功覆写 driver_session+driver_carrier 并落 EvDriverTakeover。
// carrier 是不透明载体标识（breakdown 澄清一）：本期只存不解释，格式定义权归
// B156.3——帮助文本沿用「不透明载体标识」措辞，不做任何格式假设。
var cardRebindCmd = &cobra.Command{
	Use:   "rebind <id>",
	Short: "换绑驱动会话（CAS；--carrier 为不透明载体标识，本期只存不解释）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if cardRebindTo == "" {
			return fmt.Errorf("--to 必填（新驱动会话标识）")
		}
		id := args[0]
		slog.Default().Info("CLI 换绑入口", "card", id, "to", cardRebindTo, "has_expect", cardRebindExpect != "")
		st, err := openLedger()
		if err != nil {
			slog.Default().Warn("CLI 换绑打开账本失败", "card", id, "cause", err)
			return err
		}
		defer st.Close()
		if err := st.RebindDriver(id, cardRebindTo, cardRebindCarrier, cardRebindExpect); err != nil {
			slog.Default().Warn("CLI 换绑失败", "card", id, "to", cardRebindTo, "cause", err)
			return fmt.Errorf("换绑卡 %s: %w", id, err)
		}
		slog.Default().Info("CLI 换绑完成", "card", id, "to", cardRebindTo)
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

func init() {
	cardRebindCmd.Flags().StringVar(&cardRebindTo, "to", "", "新驱动会话标识（必填）")
	cardRebindCmd.Flags().StringVar(&cardRebindCarrier, "carrier", "", "不透明载体标识（本期只存不解释，格式定义权归 B156.3）")
	cardRebindCmd.Flags().StringVar(&cardRebindExpect, "expect", "", "CAS 前值（空=要求当前无绑定）")
	cardCmd.AddCommand(cardReleaseCmd, cardTakeoverCmd, cardRebindCmd)
}
