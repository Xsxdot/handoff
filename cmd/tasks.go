// 本文件实现 handoff tasks 子命令：列出全部任务。
//
// 职责：
//   - 调用 client.ListTasks 拉取任务列表，每行输出一个任务 JSON（供上层脚本逐行解析）
//
// 边界：
//   - 只做列表展示，不做任何状态判断与筛选
package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/spf13/cobra"
)

// tasksCmd 列出全部任务，每行一个任务 JSON（created_at 降序）。
var tasksCmd = &cobra.Command{
	Use:   "tasks",
	Short: "列出全部任务（每行一个任务 JSON）",
	RunE: func(cmd *cobra.Command, _ []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		tasks, err := client.New(addr, token).ListTasks(cmd.Context())
		if err != nil {
			return err
		}
		for _, t := range tasks {
			b, err := json.Marshal(t)
			if err != nil {
				return fmt.Errorf("序列化任务 %s: %w", t.ID, err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(tasksCmd)
}
