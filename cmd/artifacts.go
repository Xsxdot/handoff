// artifacts.go —— handoff artifacts 子命令（B452 S8，F23）：把任务产物变成本机可读路径并打印。
//
// 职责：先按机取任务快照证明任务存在（404 才能判对端过旧），再按同机/跨机解析产物路径。
// 边界：不改任务状态；跨机下载解到本机新建临时目录，解包规则见 client.ExtractArtifacts。
package cmd

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/spf13/cobra"
)

// artifactsMachine 是任务所在机器；空 = 由本机 agentd 按任务路由（本机任务或镜像行）。
var artifactsMachine string

var artifactsCmd = &cobra.Command{
	Use:   "artifacts <task>",
	Short: "取任务产物：同机打印原路径，跨机下载到本机临时目录后打印路径",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		cli, cleanup, err := newTargetClient()
		if err != nil {
			return err
		}
		defer cleanup()
		// 先证明任务存在：产物接口的 404 只有在任务已知存在时才能判为「对端过旧」（F16）
		info, err := cli.AttachOn(cmd.Context(), taskID, artifactsMachine)
		if err != nil {
			return fmt.Errorf("取任务 %s: %w", taskID, err)
		}
		slog.Info("取任务产物", "task", taskID, "machine", artifactsMachine, "artifacts_dir", info.Task.ArtifactsDir)
		path, err := localArtifactsPath(cmd.Context(), cli, taskID, artifactsMachine, info.Task.ArtifactsDir)
		if errors.Is(err, client.ErrArtifactsUnsupported) {
			return fmt.Errorf("任务 %s 所在的 agentd 版本过旧，不支持产物下载；请升级: %w", taskID, err)
		}
		if err != nil {
			return fmt.Errorf("取任务 %s 的产物: %w", taskID, err)
		}
		if path == "" {
			// F23：无产物时 stdout 空、退出 0；说明走 stderr
			fmt.Fprintln(cmd.ErrOrStderr(), "任务无产物")
			return nil
		}
		fmt.Fprintln(cmd.OutOrStdout(), path)
		return nil
	},
}

func init() {
	artifactsCmd.Flags().StringVar(&artifactsMachine, "machine", "",
		"任务所在机器（缺省由本机 agentd 按任务路由）")
	rootCmd.AddCommand(artifactsCmd)
}
