// 本文件实现 handoff machines 子命令：列出本机视角的全部机器与探活结果。
//
// 职责：
//   - 调 GET /api/machines，把「本机 + 配置里的 targets」投影成表格或 JSON
//
// 边界：
//   - 只读投影：不做机器配置写操作（增删改 targets 走改 config.yaml）
//   - 不可达的机器必须带原因——一句干巴巴的「不可达」等于让人去猜
package cmd

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
)

// machinesJSON 是 machines 的 --json 开关。
var machinesJSON bool

// machinesCmd 列出本机视角的全部机器与探活结果。
var machinesCmd = &cobra.Command{
	Use:   "machines",
	Short: "列出机器与探活结果（本机 + 配置里的 targets）",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		resp, err := client.New(addr, token).Machines(cmd.Context())
		if err != nil {
			return err
		}
		if machinesJSON {
			b, err := json.Marshal(resp)
			if err != nil {
				return fmt.Errorf("序列化机器列表: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		}
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "名字\t地址\t状态\t版本\t活跃\t延迟\t缺省执行者")
		for _, m := range resp.Machines {
			name := m.Name
			if name == "" {
				name = "本机" // 空串是线格式，人看的是「本机」
			}
			state := "可达"
			if !m.Reachable {
				// 不可达必须带原因：一句干巴巴的「不可达」等于让人去猜
				state = "不可达（" + firstLineOf(m.Error) + "）"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%dms\t%s\n",
				name, m.Addr, state, m.Version, m.ActiveTasks, m.ProbeMs, m.DefaultExecutor)
		}
		return tw.Flush()
	},
}

func init() {
	machinesCmd.Flags().BoolVar(&machinesJSON, "json", false, "输出单行 JSON（proto.MachinesResp）")
	rootCmd.AddCommand(machinesCmd)
}
