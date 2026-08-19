// handoff decision 命令族：裁决项。主会话回合末 open、用户 answer、
// 会话唤醒后 list 读答复——「推不推等你一句话」的闭环三件套。
package cmd

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

var decisionCmd = &cobra.Command{Use: "decision", Short: "裁决项（结构化请示：开/列/答）"}

var (
	decOpenCard    string
	decOpenOptions []string
	decListAll     bool
)

var decOpenCmd = &cobra.Command{
	Use: "open <正文...>", Short: "开裁决（--card 挂卡，缺省项目级；--option 可多次）", Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		decision, err := st.OpenDecision(decOpenCard, strings.Join(args, " "), decOpenOptions, ledgerActor())
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(decision)
	},
}

var decListCmd = &cobra.Command{
	Use: "list", Short: "列裁决（缺省只列未答复=全局裁决收件箱；--all 全量）", Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		decisions, err := st.ListDecisions(!decListAll)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		for _, decision := range decisions {
			if err := enc.Encode(decision); err != nil {
				return err
			}
		}
		return nil
	},
}

var decAnswerCmd = &cobra.Command{
	Use: "answer <id> <答复...>", Short: "答复裁决（答案落账 + 事件流，已答复的不许改）", Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("裁决 id 应为数字（D-3 写 3）: %w", err)
		}
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.AnswerDecision(id, strings.Join(args[1:], " "), ledgerActor()); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

func init() {
	decOpenCmd.Flags().StringVar(&decOpenCard, "card", "", "挂到卡（缺省项目级）")
	decOpenCmd.Flags().StringArrayVar(&decOpenOptions, "option", nil, "候选项（可多次）")
	decListCmd.Flags().BoolVar(&decListAll, "all", false, "含已答复")
	decisionCmd.AddCommand(decOpenCmd, decListCmd, decAnswerCmd)
	rootCmd.AddCommand(decisionCmd)
}
