// 本文件实现 handoff tasks 子命令：列出全部任务。
//
// 职责：
//   - 调 client.ListTasks 拉取任务列表，每行输出一个任务 JSON（供上层脚本逐行解析）
//   - --all 时走跨机汇总（GET /api/tasks?scope=all），仍是每行一个任务 JSON，
//     机器应答情况走 stderr——stdout 是给脚本按行解析的任务 JSON 流，往里掺人话
//     会破坏既有消费方
//
// 边界：
//   - 只做列表展示，不做任何状态判断与筛选
package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

// tasksAllFlag 是 tasks 的 --all 开关。
var tasksAllFlag bool

// tasksCmd 列出全部任务，每行一个任务 JSON（created_at 降序）。
var tasksCmd = &cobra.Command{
	Use:   "tasks",
	Short: "列出全部任务（每行一个任务 JSON）",
	RunE: func(cmd *cobra.Command, _ []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		c := client.New(addr, token)
		if tasksAllFlag {
			resp, err := c.ListTasksAll(cmd.Context())
			if err != nil {
				return err
			}
			for _, t := range resp.Tasks {
				if werr := writeTaskLine(cmd, &t); werr != nil {
					return werr
				}
			}
			// 机器应答情况走 stderr：stdout 是任务 JSON 流，缺席的机器必须逐台
			// 可见，但绝不能被脚本按行解析时当成任务
			writeMachinesSummary(cmd, resp)
			return nil
		}
		tasks, err := c.ListTasks(cmd.Context())
		if err != nil {
			return err
		}
		for _, t := range tasks {
			if werr := writeTaskLine(cmd, &t); werr != nil {
				return werr
			}
		}
		return nil
	},
}

// writeTaskLine 把一条 TaskView 序列化成单行 JSON 写到 stdout。
func writeTaskLine(cmd *cobra.Command, tv *proto.TaskView) error {
	b, err := json.Marshal(tv)
	if err != nil {
		return fmt.Errorf("序列化任务 %s: %w", tv.ID, err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(b))
	return nil
}

// writeMachinesSummary 把 --all 的机器应答摘要写到 stderr。
//
// 任何一台机器没答上来都必须在摘要里逐台可见——静默少一行是跨机汇总的
// 头号失败模式。
func writeMachinesSummary(cmd *cobra.Command, resp *proto.TasksResp) {
	if len(resp.Machines) == 0 {
		return
	}
	out := cmd.ErrOrStderr()
	for _, m := range resp.Machines {
		name := m.Name
		if name == "" {
			name = "本机"
		}
		if m.Ok {
			fmt.Fprintf(out, "机器 %s：应答正常\n", name)
			continue
		}
		reason := m.Error
		if reason == "" {
			reason = "未知原因"
		}
		fmt.Fprintf(out, "机器 %s：未应答——%s\n", name, firstLineOf(reason))
	}
}

func init() {
	tasksCmd.Flags().BoolVar(&tasksAllFlag, "all", false, "跨机汇总所有机器上的任务（远端任务带 machine 名，机器应答情况在 stderr）")
	rootCmd.AddCommand(tasksCmd)
}
