// 回合末四分法的两个写入口：card accept（完成项的验收结果）与
// card needs（阻断需人工的等人标记）。
//
// 职责：把 ledger.Store 上已有的 RecordAcceptance / MarkNeedsHuman /
// ClearNeedsHuman 三个方法接出 CLI 门面。
// 边界：只落事件，不改卡状态——状态流转一律走 card move（由工作流 gate
// 校验）；验收判据文本归 card update --accept，本文件只管「验的结果」。
package cmd

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"
)

// cardAcceptEvidence 验收证据原文（命令 + 结果）。
var cardAcceptEvidence string

// cardAcceptUnverified 落「未验」而非「已验」。
var cardAcceptUnverified bool

// cardNeedsClear 清除等人标记而不是打标。
var cardNeedsClear bool

// cardAcceptCmd 记录验收结果。
//
// 参数：<id> 卡 id；--evidence 证据原文（已验时必填）；--unverified 落未验。
// 注意：本命令只落 acceptance_recorded 事件，不推状态。是否「验过了才能进
// 下一态」由工作流的 RequireAcceptance gate 决定，是政策不是本命令的事。
var cardAcceptCmd = &cobra.Command{
	Use:   "accept <id>",
	Short: "记验收结果（缺省已验，需 --evidence；--unverified 落未验）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		verified := !cardAcceptUnverified
		evidence := strings.TrimSpace(cardAcceptEvidence)
		// 「已验」是一个断言，无证据的断言不许落账——这是本项目取证文化的
		// 硬约束，不是可选的输入校验。未验则允许空证据（就是「还没验」）。
		if verified && evidence == "" {
			return fmt.Errorf("已验必须带证据：加 --evidence <命令与结果>，或用 --unverified 记为未验")
		}
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		actor := ledgerActor()
		slog.Info("记验收结果", "card", args[0], "verified", verified, "evidence_bytes", len(evidence))
		if err := st.RecordAcceptance(args[0], verified, evidence, actor); err != nil {
			slog.Error("记验收结果失败", "card", args[0], "verified", verified, "err", err)
			return err
		}
		slog.Info("验收结果已落账", "card", args[0], "verified", verified)
		fmt.Fprintf(cmd.OutOrStdout(), "已记录：%s %s\n", args[0], map[bool]string{true: "已验", false: "未验"}[verified])
		return nil
	},
}

// cardNeedsCmd 打/清等人标记。
//
// 参数：<id> 卡 id；<reason...> 原因（打标时必填，多词自动拼接）；
// --clear 清除标记（此时不需要原因）。
// 注意：本命令与节点执行器自动打的标记同源同显——审阅超轮、合并冲突那些
// 由 internal/ledgerstep 落的标记走的是同一个 MarkNeedsHuman。
var cardNeedsCmd = &cobra.Command{
	Use:   "needs <id> [reason...]",
	Short: "打等人标记（原因必填）；--clear 清除",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		reason := strings.TrimSpace(strings.Join(args[1:], " "))
		// 「等人」是注意力平面上的一个红点，没有原因的红点等于噪音——
		// 看的人无从判断该做什么，只能再去翻事件流
		if !cardNeedsClear && reason == "" {
			return fmt.Errorf("等人标记必须带原因：handoff card needs %s <原因>", id)
		}
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		actor := ledgerActor()
		if cardNeedsClear {
			slog.Info("清除等人标记", "card", id)
			if err := st.ClearNeedsHuman(id, actor); err != nil {
				slog.Error("清除等人标记失败", "card", id, "err", err)
				return err
			}
			slog.Info("等人标记已清除", "card", id)
			fmt.Fprintf(cmd.OutOrStdout(), "已清除等人标记：%s\n", id)
			return nil
		}
		slog.Info("打等人标记", "card", id, "reason", reason)
		if err := st.MarkNeedsHuman(id, reason, actor); err != nil {
			slog.Error("打等人标记失败", "card", id, "reason", reason, "err", err)
			return err
		}
		slog.Info("等人标记已落账", "card", id, "reason", reason)
		fmt.Fprintf(cmd.OutOrStdout(), "已标记等人：%s（%s）\n", id, reason)
		return nil
	},
}

func init() {
	cardAcceptCmd.Flags().StringVar(&cardAcceptEvidence, "evidence", "", "证据原文（命令 + 结果）；已验时必填")
	cardAcceptCmd.Flags().BoolVar(&cardAcceptUnverified, "unverified", false, "记为未验（证据可空）")
	cardNeedsCmd.Flags().BoolVar(&cardNeedsClear, "clear", false, "清除等人标记（不需要原因）")
}
