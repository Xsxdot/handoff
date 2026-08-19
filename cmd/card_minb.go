// card min-b：切换期一次性命令——把 B 号水位垫到历史总账 max B，
// 此后新建卡号严格大于历史号，markdown 旧账与账本新账永不撞号。
// Hidden：日常工作流用不到它，藏起来防误用。
package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
)

var cardMinBCmd = &cobra.Command{
	Use:    "min-b <n>",
	Short:  "垫 B 号水位（切换期一次性；只升不降）",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 0 {
			return fmt.Errorf("水位必须是非负整数，收到 %q", args[0])
		}
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		// EnsureMinB 语义即「只升不降 + 幂等」，回垫是无操作不是报错
		if err := st.EnsureMinB(n); err != nil {
			return fmt.Errorf("垫号: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), `{"ok":true,"min_b":%d}`+"\n", n)
		return nil
	},
}

func init() {
	cardCmd.AddCommand(cardMinBCmd)
}
