// card_bearing.go 把「存量 coordinate 席位补写承载记录」接出 CLI（B389 §2.3）。
//
// 职责：为卡上已有 coordinate 席位补写承载记录（会话已知在哪台机器时的存量
// 修复路径）。边界：机器名与恢复环境一律从载体登记读出，不接受手输；写面走
// 本机账本的 SetSeatBearing（CAS 断言席位未变）。只修复承载，不重选载体、
// 不换会话。
package cmd

import (
	"fmt"
	"log/slog"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

var cardSeatBearingCarrier string

var cardSeatCmd = &cobra.Command{
	Use:   "seat",
	Short: "协调者席位的承载记录维护（存量 coordinate 席位缺承载时的人工修复路径）",
}

var cardSeatBearingCmd = &cobra.Command{
	Use:   "bearing",
	Short: "承载记录维护",
}

var cardSeatBearingSetCmd = &cobra.Command{
	Use:   "set <id> --carrier <carrier>",
	Short: "为卡上已有 coordinate 席位补写承载记录（机器名从载体登记读出，不手输）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		if cardSeatBearingCarrier == "" {
			return fmt.Errorf("--carrier 必填")
		}
		slog.Default().Info("CLI 补写承载入口", "card", id, "carrier", cardSeatBearingCarrier)
		cl, done, err := newTargetClient()
		if err != nil {
			slog.Default().Warn("CLI 补写承载取目标客户端失败", "card", id, "cause", err)
			return err
		}
		defer done()
		reg, err := cl.Squads(cmd.Context())
		if err != nil {
			return fmt.Errorf("读载体登记: %w", err)
		}
		var carrier *proto.CarrierView
		for i := range reg.Carriers {
			if reg.Carriers[i].Name == cardSeatBearingCarrier {
				carrier = &reg.Carriers[i]
				break
			}
		}
		if carrier == nil {
			return fmt.Errorf("载体 %q 未登记", cardSeatBearingCarrier)
		}
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		card, err := st.GetCard(id)
		if err != nil {
			return err
		}
		seatCLI, _, err := proto.ParseSeatIdentity(card.DriverSession)
		if err != nil {
			return fmt.Errorf("卡 %s 席位不可用，不能补写承载: %w", id, err)
		}
		// 载体的 CLI 必须与席位身份解出的 CLI 一致，否则会把恢复环境指到错误的执行器。
		if seatCLI != carrier.CLI {
			return fmt.Errorf("载体 %s 的 CLI %q 与席位 CLI %q 不一致",
				carrier.Name, carrier.CLI, seatCLI)
		}
		bearing := ledger.SeatBearing{
			Carrier: carrier.Name, Machine: carrier.Machine,
			HomeDir: carrier.HomeDir, Model: carrier.Model,
		}
		if err := st.SetSeatBearing(id, card.DriverSession, bearing); err != nil {
			slog.Default().Warn("CLI 补写承载失败", "card", id, "cause", err)
			return fmt.Errorf("补写卡 %s 承载: %w", id, err)
		}
		slog.Default().Info("CLI 补写承载完成", "card", id, "carrier", carrier.Name)
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

func init() {
	cardSeatBearingSetCmd.Flags().StringVar(&cardSeatBearingCarrier, "carrier", "", "载体登记名（机器名从登记读出，不手输）")
	cardSeatBearingCmd.AddCommand(cardSeatBearingSetCmd)
	cardSeatCmd.AddCommand(cardSeatBearingCmd)
	cardCmd.AddCommand(cardSeatCmd)
}
