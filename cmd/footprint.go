// footprint.go —— `handoff footprint` 命令：体检全部任务的进程足迹。
//
// 职责：
//   - 拉取对端全部任务（含已归档）的进程占用与判定结论并渲染
//
// 边界：
//   - **只数不杀**：本命令不回收任何进程。清扫由 agentd 在 executor 判死时
//     自动完成（见 spec §3.4），本命令只负责让人看见
//   - 不改任何任务状态、不发事件
package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/prochost"
	"github.com/xushixin/handoff/internal/proto"
)

var (
	footprintJSONOut bool
	footprintAll     bool
)

// footprintCmd 体检全部任务的进程足迹。
//
// 使用方式：handoff footprint [--target <名字>] [--all] [--json]
var footprintCmd = &cobra.Command{
	Use:   "footprint",
	Short: "查看各任务占用的进程数与本机进程余量（只数不杀）",
	RunE: func(cmd *cobra.Command, _ []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()

		fp, err := client.New(addr, token).Footprint(cmd.Context())
		switch {
		case errors.Is(err, client.ErrFootprintUnsupported):
			// 与 status 同款：404 是一条成功的诊断结论，不是失败
			fmt.Fprintf(out, "agentd   %s   可用（版本过旧）\n", addr)
			fmt.Fprintln(out, "限制     该 agentd 不支持 /api/footprint，足迹不可得")
			fmt.Fprintln(out, "处置     升级远端 agentd 后重试")
			return nil
		case err != nil:
			return err
		}
		if footprintJSONOut {
			return json.NewEncoder(out).Encode(fp)
		}
		renderFootprint(out, fp)
		return nil
	},
}

func init() {
	footprintCmd.Flags().BoolVar(&footprintJSONOut, "json", false, "以 JSON 输出")
	footprintCmd.Flags().BoolVar(&footprintAll, "all", false, "显示全部任务（默认只显示有残留或判不出结论的）")
	rootCmd.AddCommand(footprintCmd)
}

// renderFootprint 渲染体检结果。
//
// 默认过滤规则：只显示 Procs > 0（确有残留）或 Verdict != ok（我们不敢下结论）
// 的行。**后者必须显示，哪怕 Procs 是 0**——「没有残留」与「判不出」是两回事，
// 把判不出的行按 0 过滤掉，等于用一个假结论把该看的东西藏起来。
func renderFootprint(w io.Writer, fp *proto.FootprintResp) {
	if fp.Usage != nil {
		fmt.Fprintf(w, "进程     %d/%d（本机 uid 已用/上限）\n", fp.Usage.Used, fp.Usage.Limit)
	} else {
		// nil 不能渲染成 0/0：见 proto.ProcUsage 的 why
		fmt.Fprintln(w, "进程     未知（对端未提供）")
	}
	shown := 0
	for _, r := range fp.Rows {
		if !footprintAll && r.Procs == 0 && r.Verdict == string(prochost.VerdictOK) {
			continue
		}
		shown++
		line := fmt.Sprintf("  %s  %s  %s  %d 进程", short8(r.TaskID), r.Name, r.State, r.Procs)
		if r.Verdict != string(prochost.VerdictOK) {
			line += "  ⚠ " + r.Verdict
		}
		fmt.Fprintln(w, line)
	}
	if shown == 0 {
		fmt.Fprintln(w, "足迹     无残留（共体检 "+strconv.Itoa(len(fp.Rows))+" 个任务）")
	}
}
