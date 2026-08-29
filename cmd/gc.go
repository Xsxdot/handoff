// gc.go —— handoff gc 子命令的 CLI 接缝空壳。
//
// 职责：
//   - 固定无参 `handoff gc` 的 preview/execute flag 形状与 target client 接线
//   - 把目标 agentd 的报告转成人读或 JSON 输出
//
// 边界：
//   - 不在 CLI 遍历任务目录、不逐个 POST reclaim、不决定服务端删除集合
//   - `--force` 没有 `--yes` 时保持只读；实际资源动作由 agentd 决定
package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

var (
	gcForce bool
	gcYes   bool
	gcJSON  bool
)

// gcCmd 预览或执行目标 agentd 上的终态缓存与 managed worktree 清理。
var gcCmd = &cobra.Command{
	Use:   "gc",
	Short: "预览或清理目标 agentd 的终态缓存与残留工作树",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cl, cleanup, err := newTargetClient()
		if err != nil {
			return err
		}
		defer cleanup()
		addr := "http://relay"
		if targetName == "" {
			addr, _, _ = TargetEndpoint()
		}
		return runGC(cmd, cl, addr)
	},
}

func init() {
	gcCmd.Flags().BoolVar(&gcForce, "force", false,
		"预览/执行时允许按 reclaim 语义强删脏 managed worktree")
	gcCmd.Flags().BoolVar(&gcYes, "yes", false, "确认执行删除；不带此项只预览")
	gcCmd.Flags().BoolVar(&gcJSON, "json", false, "以 JSON 输出")
	rootCmd.AddCommand(gcCmd)
}

// runGC 调用目标 agentd 的单次预览或执行入口。
func runGC(cmd *cobra.Command, cl *client.Client, addr string) error {
	slog.Default().Info("CLI gc 进入", "target", addr, "force", gcForce, "execute", gcYes,
		"json", gcJSON)
	var (
		resp *proto.GCResp
		err  error
	)
	if gcYes {
		resp, err = cl.GC(cmd.Context(), gcForce)
	} else {
		resp, err = cl.GCPreview(cmd.Context(), gcForce)
	}
	if errors.Is(err, client.ErrGCUnsupported) {
		slog.Default().Info("CLI gc 对端过旧", "target", addr)
		_, printErr := fmt.Fprintf(cmd.OutOrStdout(), "agentd %s 过旧，升级后再跑 gc\n", addr)
		return printErr
	}
	if err != nil {
		slog.Default().Error("CLI gc 请求失败", "target", addr, "cause", err)
		return err
	}
	if gcJSON {
		if err := json.NewEncoder(cmd.OutOrStdout()).Encode(resp); err != nil {
			slog.Default().Error("CLI gc JSON 输出失败", "target", addr, "cause", err)
			return err
		}
		slog.Default().Info("CLI gc 完成", "target", addr, "preview", resp.Preview,
			"failures", resp.Failures)
		return nil
	}
	renderGC(cmd.OutOrStdout(), resp)
	slog.Default().Info("CLI gc 完成", "target", addr, "preview", resp.Preview,
		"failures", resp.Failures)
	return nil
}

// renderGC 输出 Ticket 0 报告形状；字节量的详细人读文案由实现节点补齐。
func renderGC(w io.Writer, resp *proto.GCResp) {
	mode := "预览"
	if !resp.Preview {
		mode = "已执行"
	}
	if resp.ReleasableBytes == nil {
		fmt.Fprintf(w, "%s     将释放字节：未计算\n", mode)
	} else {
		fmt.Fprintf(w, "%s     将释放字节：%d\n", mode, *resp.ReleasableBytes)
	}
	fmt.Fprintf(w, "缓存     %d 行；工作树 %d 行；失败 %d\n",
		len(resp.CacheRows), len(resp.WorktreeRows), resp.Failures)
}
