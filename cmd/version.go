// 本文件实现 handoff version 子命令：打印本二进制的版本标识。
//
// 职责：
//   - 首行输出纯版本字符串，供机器精确比对
//   - 其后输出 revision / Go 版本 / 平台三行，供人排障
//
// 边界：
//   - 不联网、不读配置文件：这条命令只回答「我是谁」。它必须能在一台刚装完、
//     还没有 ~/.handoff/config.yaml 的机器上跑通
//   - **首行格式是对外契约**：B54.3 的自更新自检会拉起新下载的二进制跑本命令，
//     把首行与期望 tag 精确比对（见 spec §4.6 步骤 ⑤）。改这一行的格式等于改
//     协议，必须同步改自检侧
package cmd

import (
	"fmt"
	"runtime"

	"github.com/Xsxdot/handoff/internal/buildinfo"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

// versionUnknown 是非 release 构建（本地 go build / go run / 测试二进制）的首行取值。
//
// why（不留空串）：空首行与「命令没有任何输出」无法区分，自检侧会把两种情况
// 都判为失败，丢掉「二进制能跑但不是 release 构建」这个有用结论。
const versionUnknown = "unknown"

// versionCmd 打印本二进制的版本标识。
//
// 使用方式：handoff version
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "打印本二进制的版本标识",
	RunE: func(cmd *cobra.Command, _ []string) error {
		b, ok := buildinfo.Read()
		out := cmd.OutOrStdout()

		// 首行永远存在且只有版本号本身——这是给机器读的那一行
		if b.Version != "" {
			fmt.Fprintln(out, b.Version)
		} else {
			fmt.Fprintln(out, versionUnknown)
		}

		if !ok {
			// 极少见：非 go 工具链链接的二进制。如实说明而不是打三行空值
			fmt.Fprintln(out, "构建信息不可读（非 go 工具链链接的二进制）")
			return nil
		}
		fmt.Fprintf(out, "revision  %s\n", revisionText(b))
		fmt.Fprintf(out, "go        %s\n", b.Go)
		fmt.Fprintf(out, "platform  %s/%s\n", runtime.GOOS, runtime.GOARCH)
		return nil
	},
}

func init() { rootCmd.AddCommand(versionCmd) }

// revisionText 渲染 revision 行。
//
// 参数：
//   - b: 构建标识
//
// 返回：
//   - 一行文本；Revision 为空时如实说明「非 go build 产物」而不是留空
func revisionText(b proto.BuildInfo) string {
	if b.Revision == "" {
		return "未知（非 go build 产物）"
	}
	s := b.Revision
	if b.Time != "" {
		s += "  " + b.Time
	}
	if b.Modified {
		// 带未提交改动意味着这个二进制对不上任何一个提交，排障时是关键信息
		s += "  带未提交改动"
	}
	return s
}
