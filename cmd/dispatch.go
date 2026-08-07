// 本文件实现 handoff dispatch 子命令：把本地 plan 文件派发到 agentd 执行。
//
// 职责：
//   - 读取本地 plan 文件并 base64 编码，连同仓库路径/计划名/target 一并 POST
//     给 agentd（body {repo, plan_b64, plan_name, target}）
//   - 成功时单行输出任务 JSON（state=running，供上层脚本解析任务 id）
//
// 边界：
//   - 只做文件读取与上传，不校验计划内容语义（解析与执行由 executor 负责）
package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
)

var dispatchRepo string

// dispatchCmd 派发一个计划任务到 agentd 执行。
//
// 使用方式：handoff dispatch --repo <仓库路径> [--target <name>] <plan 文件>
var dispatchCmd = &cobra.Command{
	Use:   "dispatch <plan 文件>",
	Short: "派发一个计划任务到 agentd 执行",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if dispatchRepo == "" {
			return fmt.Errorf("--repo 必须指定任务仓库路径")
		}
		content, err := os.ReadFile(args[0])
		if err != nil {
			return fmt.Errorf("读取计划文件 %s: %w", args[0], err)
		}
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		task, err := client.New(addr, token).Dispatch(cmd.Context(),
			dispatchRepo, base64.StdEncoding.EncodeToString(content), filepath.Base(args[0]), targetName)
		if err != nil {
			return err
		}
		b, err := json.Marshal(task)
		if err != nil {
			return fmt.Errorf("序列化任务: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(b))
		return nil
	},
}

func init() {
	dispatchCmd.Flags().StringVar(&dispatchRepo, "repo", "", "任务仓库路径（executor 工作区，必须）")
	rootCmd.AddCommand(dispatchCmd)
}
