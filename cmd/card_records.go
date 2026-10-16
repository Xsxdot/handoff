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
		markActor := needsMarkActor(cmd, id, actor)
		slog.Info("打等人标记", "card", id, "reason", reason, "actor", markActor)
		if err := st.MarkNeedsHuman(id, reason, markActor); err != nil {
			slog.Error("打等人标记失败", "card", id, "reason", reason, "actor", markActor, "err", err)
			return err
		}
		slog.Info("等人标记已落账", "card", id, "reason", reason)
		fmt.Fprintf(cmd.OutOrStdout(), "已标记等人：%s（%s）\n", id, reason)
		return nil
	},
}

// needsMarkActor 决定打等人标记的 actor（契约 §3.8.1「席位自打的前提」、条 189/190）。
//
// 参数：cmd 用于 stderr 提示；card 卡号（只进日志）；fallback 是出示失败时的回落值
// （ledgerActor()）。
// 返回：当前会话能出示席位身份就返回席位身份，否则返回 fallback。
// 为什么：桌面端唤醒规则 4 按「actor == 目标卡 driver_session」排除协调者自打的标；
// 若仍记 cli:<USER>@<host>，协调者在自己会话里打的标与人在终端打的标无从区分，会被
// 自己的标叫醒（回声环）。出示失败（普通终端）不是错误：回落、照常落标、命令成功，
// 只在 stderr 留一行提示（拆解稿 §5.2：回落若成常态，规则 4 会静默失效，得让人看见）。
func needsMarkActor(cmd *cobra.Command, card, fallback string) string {
	identity, err := currentSeatIdentity("", "")
	if err != nil {
		slog.Warn("等人标记未出示席位身份，actor 回落终端身份", "card", card, "actor", fallback, "cause", err)
		fmt.Fprintf(cmd.ErrOrStderr(), "提示：当前会话未出示席位身份，等人标记记在 %s 名下\n", fallback)
		return fallback
	}
	return identity
}

func init() {
	cardAcceptCmd.Flags().StringVar(&cardAcceptEvidence, "evidence", "", "证据原文（命令 + 结果）；已验时必填")
	cardAcceptCmd.Flags().BoolVar(&cardAcceptUnverified, "unverified", false, "记为未验（证据可空）")
	cardNeedsCmd.Flags().BoolVar(&cardNeedsClear, "clear", false, "清除等人标记（不需要原因）")
}
