// handoff card prefix 命令：为项目设置卡号前缀。参数解析与机器输出留在 CLI，
// 前缀格式、占用和已有卡保护由账本域 SetCardPrefix 统一判定。
package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var cardPrefixCmd = &cobra.Command{
	Use:   "prefix <project> <prefix>",
	Short: "设置项目卡号前缀（已有卡后不可修改）",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.SetCardPrefix(args[0], args[1]); err != nil {
			return fmt.Errorf("设置卡号前缀: %w", err)
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
			"ok": true, "project": args[0], "prefix": args[1],
		})
	},
}

func init() {
	cardCmd.AddCommand(cardPrefixCmd)
}
