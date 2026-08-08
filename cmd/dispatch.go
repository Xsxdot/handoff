// 本文件实现 handoff dispatch 子命令：把本地 plan 文件（或 --prompt 直接指令）
// 派发到 agentd 执行。
//
// 职责：
//   - 读取本地 plan 文件并 base64 编码，连同仓库路径/计划名/target/执行者/模型/
//     分支/worktree 等参数一并 POST 给 agentd（body {repo, plan_b64, prompt, ...}）
//   - 成功时单行输出任务 JSON（state=running，供上层脚本解析任务 id）
//
// 边界：
//   - 只做文件读取与上传，不校验计划内容语义（解析与执行由 executor 负责）
//   - --no-terminal 在本文件只注册 flag 并参与「是否弹终端」的判定骨架；
//     实际弹窗行为由 Task 12 的弹终端块驱动
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

var (
	dispatchRepo        string
	dispatchPrompt      string
	dispatchName        string
	dispatchExecutor    string
	dispatchModel       string
	dispatchBranch      string
	dispatchNewBranch   string
	dispatchBase        string
	dispatchWorktree    string
	dispatchNewWorktree bool
	dispatchNoTerminal  bool
)

// dispatchCmd 派发一个计划任务到 agentd 执行。
//
// 使用方式：handoff dispatch [--repo <仓库>] [--prompt ...] [--executor x] [--model m]
// [--branch b | --new-branch b [--base t]] [--worktree w | --new-worktree]
// [--no-terminal] [plan 文件]
var dispatchCmd = &cobra.Command{
	Use:   "dispatch [plan 文件]",
	Short: "派发一个计划任务到 agentd 执行",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if dispatchRepo == "" {
			return fmt.Errorf("--repo 必须指定任务仓库路径")
		}
		var planB64, planName string
		if len(args) == 1 {
			content, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("读取计划文件 %s: %w", args[0], err)
			}
			planB64 = base64.StdEncoding.EncodeToString(content)
			planName = filepath.Base(args[0])
		}
		// plan 文件与 --prompt 都缺：本地先报错，省一次网络往返（服务端也会拒）
		if planB64 == "" && dispatchPrompt == "" {
			return fmt.Errorf("必须提供 plan 文件或 --prompt（至少其一）")
		}
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		task, err := client.New(addr, token).Dispatch(cmd.Context(), client.DispatchOpts{
			Repo: dispatchRepo, PlanB64: planB64, PlanName: planName, Target: targetName,
			Prompt: dispatchPrompt, Name: dispatchName,
			Executor: dispatchExecutor, Model: dispatchModel,
			Branch: dispatchBranch, NewBranch: dispatchNewBranch, Base: dispatchBase,
			Worktree: dispatchWorktree, NewWorktree: dispatchNewWorktree,
		})
		if err != nil {
			return err
		}
		b, err := json.Marshal(task)
		if err != nil {
			return fmt.Errorf("序列化任务: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(b))
		// 弹终端块的入口（Task 12 实现）：--no-terminal 在此使能判定
		dispatchAfterTerminal(cmd, task.ID)
		return nil
	},
}

func init() {
	dispatchCmd.Flags().StringVar(&dispatchRepo, "repo", "", "任务仓库路径（executor 工作区，必须）")
	dispatchCmd.Flags().StringVar(&dispatchPrompt, "prompt", "", "直接指令（prompt-only 派发；与 plan 文件至少其一）")
	dispatchCmd.Flags().StringVar(&dispatchName, "name", "", "任务展示名（默认从 plan 文件名或 prompt 派生）")
	dispatchCmd.Flags().StringVar(&dispatchExecutor, "executor", "", "执行者名（如 opencode/fake；空=agentd 缺省执行者）")
	dispatchCmd.Flags().StringVar(&dispatchModel, "model", "", "任务级模型覆盖（空=执行者自身默认）")
	dispatchCmd.Flags().StringVar(&dispatchBranch, "branch", "", "切到已存在分支（与 --new-branch 互斥）")
	dispatchCmd.Flags().StringVar(&dispatchNewBranch, "new-branch", "", "新建分支名（空且 --branch 空=自动 handoff/<id8>）")
	dispatchCmd.Flags().StringVar(&dispatchBase, "base", "", "新分支起点 commit/分支（仅与 --new-branch 连用；空=HEAD）")
	dispatchCmd.Flags().StringVar(&dispatchWorktree, "worktree", "", "用户自带 worktree 路径（与 --new-worktree 互斥）")
	dispatchCmd.Flags().BoolVar(&dispatchNewWorktree, "new-worktree", false, "在 DataDir/worktrees 下新建 managed worktree（任务完成时自动删除）")
	dispatchCmd.Flags().BoolVar(&dispatchNoTerminal, "no-terminal", false, "派发成功后不弹终端实况（默认弹，受配置 terminal.auto 控制）")
	dispatchCmd.MarkFlagsMutuallyExclusive("branch", "new-branch")
	dispatchCmd.MarkFlagsMutuallyExclusive("worktree", "new-worktree")
	rootCmd.AddCommand(dispatchCmd)
}

// dispatchAfterTerminal 是 dispatch 成功后「弹终端实况」的钩子。
//
// Task 12 会替换其实现（osascript 弹 Terminal.app 或降级提示行）；本任务先以
// 无操作占位，保证 --no-terminal flag 注册与命令结构就绪。
func dispatchAfterTerminal(cmd *cobra.Command, taskID string) {
	_ = cmd
	_ = taskID
}
