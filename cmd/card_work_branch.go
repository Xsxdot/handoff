// card work-branch 命令：人工登记/覆盖/清除某卡的工作分支来源（B382）。
//
// 职责：把「本机人工实现、没有 dispatched 快照」的卡的工作分支写进账本事件流，
// 让 review 等后续节点能从该分支起。用户可见名：handoff card work-branch <卡号> <分支>。
// 边界：只写账本事件；有非审阅快照时登记留痕但不生效（快照是权威来源），此处只提示不报错。
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var cardWorkBranchCmd = &cobra.Command{
	Use:   "work-branch <卡号> <分支>",
	Short: "登记/覆盖/清除人工工作分支（本机人工实现的卡；分支传空串清除）",
	Long: "登记某卡的工作分支来源，供 review 等后续节点从该分支起。\n" +
		"卡上已有非审阅 dispatched 快照时，快照是权威来源，本次登记只留痕不生效。\n" +
		"分支传空串表示清除登记。",
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		applied, err := st.RegisterWorkBranch(args[0], args[1], ledgerActor())
		if err != nil {
			return err
		}
		if !applied {
			if _, err := fmt.Fprintln(cmd.ErrOrStderr(),
				"提示：卡上已有非审阅 dispatched 快照，登记已留痕但不生效（快照是工作分支的权威来源）"); err != nil {
				return fmt.Errorf("输出登记提示: %w", err)
			}
		}
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

func init() {
	cardCmd.AddCommand(cardWorkBranchCmd)
}
