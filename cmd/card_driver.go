// card_driver.go 把协调者席位与兼容归属命令接出 CLI。
// 边界：bind/rebind --self 只调用 ledger.Store 的席位原子操作；机器人换绑走
// agentd；release/takeover 保留旧命令但不再改变协调者席位。
package cmd

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

var cardBindCLI, cardBindSession string
var cardRebindCLI, cardRebindSession string

var cardBindCmd = &cobra.Command{
	Use:   "bind <id>",
	Short: "让当前会话坐下成为这张卡的协调者",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		slog.Default().Info("CLI 坐下入口", "card", id)
		identity, err := currentSeatIdentity(cardBindCLI, cardBindSession)
		if err != nil {
			slog.Default().Warn("CLI 坐下身份出示失败", "card", id, "cause", err)
			return err
		}
		st, err := openLedger()
		if err != nil {
			slog.Default().Warn("CLI 坐下打开账本失败", "card", id, "cause", err)
			return err
		}
		defer st.Close()
		if err := st.BindSeat(id, identity, proto.SeatSourceBind); err != nil {
			slog.Default().Warn("CLI 坐下失败", "card", id, "cause", err)
			return fmt.Errorf("坐下卡 %s: %w", id, err)
		}
		slog.Default().Info("CLI 坐下完成", "card", id)
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

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

var cardRebindCmd = &cobra.Command{
	Use:   "rebind <id>",
	Short: "换绑协调者（--self 让当前会话接班，--launch 叫机器人接班）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if cardRebindSelf == cardRebindLaunch {
			return fmt.Errorf("--self 与 --launch 必须二选一")
		}
		id := args[0]
		flagsProvided := cmd.Flags().Changed("cli") || cmd.Flags().Changed("session")
		if cardRebindLaunch && flagsProvided {
			err := fmt.Errorf("card rebind --launch 不接受 --cli/--session：机器人会话由 --launch 自己创建")
			slog.Default().Warn("CLI 机器人换绑拒绝身份 flag", "card", id, "mode", "launch", "cause", err)
			return err
		}
		slog.Default().Info("CLI 换绑入口", "card", id, "mode", map[bool]string{true: "self", false: "launch"}[cardRebindSelf])
		if cardRebindSelf {
			identity, err := currentSeatIdentity(cardRebindCLI, cardRebindSession)
			if err != nil {
				slog.Default().Warn("CLI self 换绑身份出示失败", "card", id, "cause", err)
				return err
			}
			st, err := openLedger()
			if err != nil {
				slog.Default().Warn("CLI self 换绑打开账本失败", "card", id, "cause", err)
				return err
			}
			defer st.Close()
			card, err := st.GetCard(id)
			if err != nil {
				return fmt.Errorf("读取换绑卡 %s: %w", id, err)
			}
			if err := st.RebindSeat(id, identity, proto.SeatSourceBind, card.DriverSession); err != nil {
				slog.Default().Warn("CLI self 换绑失败", "card", id, "cause", err)
				return fmt.Errorf("换绑卡 %s: %w", id, err)
			}
			addr, token, err := LocalEndpoint()
			if err != nil {
				slog.Default().Warn("CLI self 换绑读取本机 agentd 端点失败", "card", id, "cause", err)
				return fmt.Errorf("换绑卡 %s 后驱逐 agentd 内存: %w", id, err)
			}
			if err := client.New(addr, token).CoordinatorForget(cmd.Context(), id); err != nil {
				if !errors.Is(err, client.ErrUnreachable) {
					slog.Default().Warn("CLI self 换绑驱逐 agentd 内存失败", "card", id, "cause", err)
					return fmt.Errorf("换绑卡 %s 后驱逐 agentd 内存: %w", id, err)
				}
				slog.Default().Warn("CLI self 换绑后 agentd 不可达，保留本机 CAS 结果", "card", id, "cause", err)
			} else {
				slog.Default().Info("CLI self 换绑已驱逐 agentd 内存", "card", id)
			}
		} else {
			cl, done, err := newTargetClient()
			if err != nil {
				return err
			}
			defer done()
			if _, err := cl.CoordinatorRebind(cmd.Context(), id, proto.CoordinatorRebindReq{Mode: "launch"}); err != nil {
				slog.Default().Warn("CLI 机器人换绑失败", "card", id, "cause", err)
				return fmt.Errorf("叫机器人换绑卡 %s: %w", id, err)
			}
		}
		slog.Default().Info("CLI 换绑完成", "card", id)
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

var cardRebindSelf, cardRebindLaunch bool

func init() {
	cardBindCmd.Flags().StringVar(&cardBindCLI, "cli", "", "手填当前会话物种名（需与 --session 成对）")
	cardBindCmd.Flags().StringVar(&cardBindSession, "session", "", "手填当前会话 id（需与 --cli 成对）")
	cardRebindCmd.Flags().BoolVar(&cardRebindSelf, "self", false, "当前会话接班")
	cardRebindCmd.Flags().BoolVar(&cardRebindLaunch, "launch", false, "叫机器人接班")
	cardRebindCmd.Flags().StringVar(&cardRebindCLI, "cli", "", "手填接班会话物种名（仅 --self，需与 --session 成对）")
	cardRebindCmd.Flags().StringVar(&cardRebindSession, "session", "", "手填接班会话 id（仅 --self，需与 --cli 成对）")
	cardCmd.AddCommand(cardBindCmd, cardReleaseCmd, cardTakeoverCmd, cardRebindCmd)
}
