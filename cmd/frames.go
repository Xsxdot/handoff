// 本文件实现 handoff frames 子命令：读任务的结构化回合帧。
//
// 职责：
//   - 调 GET /api/tasks/{id}/frames，把 ndjson 流每行原样打到 stdout
//
// 边界：
//   - **不做人类友好格式化**：本命令是 handoff tui（W4e）与脚本的数据源，
//     人要看好看的有 Web 控制台。与 handoff tasks 的「一行一个 JSON」同风格
//   - 不解析帧语义：只做行搬运与心跳过滤
//   - 任务 id 是完整 UUID 精确匹配，没有前缀补全（与全部子命令一致）
package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
)

var (
	framesFollow bool
	framesOffset int64
	framesTail   int64
)

// framesCmd 读任务的结构化回合帧。
var framesCmd = &cobra.Command{
	Use:   "frames <task>",
	Short: "读任务的结构化回合帧（每行一个 JSON 帧）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		return runFrames(cmd.Context(), addr, token, args[0],
			framesOffset, framesTail, framesFollow, cmd.OutOrStdout())
	},
}

// runFrames 打开帧流并把每一行原样写到 out。
//
// 参数：
//   - addr/token: agentd 端点与令牌（token 只进请求头，绝不进日志或输出）
//   - taskID:     完整 UUID
//   - offset/tail/follow: 与 GET /api/tasks/{id}/frames 的同名参数一致（字节）
//
// 注意：
//   - 空行是服务端的心跳保活，跳过不输出——它不是帧，喷给消费方会让
//     按行解析的脚本收到一条解析失败
//   - follow 模式下本函数直到 ctx 取消（Ctrl+C）或服务端断流才返回
func runFrames(ctx context.Context, addr, token, taskID string,
	offset, tail int64, follow bool, out io.Writer) error {
	rc, size, err := client.New(addr, token).FramesStream(ctx, taskID, offset, tail, follow)
	if err != nil {
		return err
	}
	defer rc.Close()

	sc := bufio.NewScanner(rc)
	// 单帧上限 16KB，给到 1MB 足够宽裕，同时挡住异常巨行把内存吃穿
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue // 心跳空行（见函数注释）
		}
		if _, err := fmt.Fprintf(out, "%s\n", line); err != nil {
			return fmt.Errorf("输出帧: %w", err)
		}
	}
	if err := sc.Err(); err != nil {
		// ctx 取消（Ctrl+C）走到这里是正常收尾，交给调用方按 ctx 判定
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("读帧流（文件当前 %d 字节）: %w", size, err)
	}
	return nil
}

func init() {
	framesCmd.Flags().BoolVar(&framesFollow, "follow", false, "到达文件尾后继续等待增量")
	framesCmd.Flags().Int64Var(&framesOffset, "offset", 0, "起始字节偏移（优先于 --tail）")
	framesCmd.Flags().Int64Var(&framesTail, "tail", 0, "从尾部回溯的字节数")
	rootCmd.AddCommand(framesCmd)
}
