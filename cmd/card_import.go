// card import：显式 B 号导入建卡的命令面薄壳。存量 markdown 总账迁入、
// 冻结 md 里的搁置条目复活都走它——是永久能力，不是一次性迁移脚本。
// 薄壳只做参数拼装与转发，撞号/缺父的判定全在账本域 Store.ImportCard。
package cmd

import (
	"fmt"
	"strings"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/spf13/cobra"
)

var (
	cardImportProject, cardImportPriority string
	cardImportWorkflow, cardImportSource  string
	cardImportBase                        string
)

var cardImportCmd = &cobra.Command{
	Use:   "import <id> <标题>",
	Short: "按既有 B 号导入建卡（存量迁入/搁置复活；撞号即拒）",
	Long: `按既有 B 号导入建卡。

id 形如 B153（顶层）或 B153.1（点号子卡，父卡须已存在）。
目标号已存在时拒绝，不覆盖。导入不受 min_b 水位约束——
水位只管自动取号，导入一律按原号落位。`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		card, err := st.ImportCard(args[0], cardImportSource, ledger.NewCard{
			Title: strings.Join(args[1:], " "), Project: cardImportProject,
			Priority: cardImportPriority, Workflow: cardImportWorkflow,
			BaseBranch: cardImportBase, Actor: ledgerActor(),
		})
		if err != nil {
			return fmt.Errorf("导入卡: %w", err)
		}
		return printCardJSON(cmd, card)
	},
}

func init() {
	cardImportCmd.Flags().StringVar(&cardImportProject, "project", "", "项目名（必填）")
	cardImportCmd.Flags().StringVar(&cardImportPriority, "priority", "中", "高|中|低")
	cardImportCmd.Flags().StringVar(&cardImportWorkflow, "workflow", "", "工作流名（空=triage）")
	cardImportCmd.Flags().StringVar(&cardImportSource, "source", "", "导入来源标注（落 card_created 事件；空=手工导入）")
	cardImportCmd.Flags().StringVar(&cardImportBase, "base-branch", "", "基线分支（空=继承/主线）")
	_ = cardImportCmd.MarkFlagRequired("project")

	cardCmd.AddCommand(cardImportCmd)
}
