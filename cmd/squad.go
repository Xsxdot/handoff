// handoff squad 命令族：编制域登记面的命令入口（B156.3 K3，契约 §11 移交区）。
//
// 为什么走 agentd HTTP 而不像 card/workflow 直连账本库：小队登记是编制域规则调用
// （校验/角色词表/成员存在性都在编制域 scheduling 包的入站门面里），冻结依赖方向
// 只有 d_gateway→d_scheduling 一条（codegraph/target.json 没有 d_cli→d_scheduling，
// best.json 里 schedclient 容器也归 d_scheduling）；CLI 直连会在图外开第二条写入口，
// 违反「gateway 端点与 CLI 写命令最终都汇入 scheduling.Service 单一入口」。
// 代价是本机 agentd 必须在线——登记面是控制面配置，该前提成立；够不着时报
// ErrUnreachable 可行动错误。
//
// stdout 只出机器 JSON（一行一对象），人话走 stderr（card 族同款约定）。
package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	// 不 import internal/client：newTargetClient() 的返回类型由推断承接，本文件
	// 没有任何显式 client 标识符——import 了反而是未使用导入的编译错。
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

var squadCmd = &cobra.Command{Use: "squad",
	Short: "编制域小队登记面（自动化层的前置配置；载体经控制台配置页登记）"}

var (
	squadName    string
	squadRole    string
	squadMembers []string
	squadMaxConc int
	squadExpect  int
	squadJSON    bool
)

var squadCreateCmd = &cobra.Command{
	Use:   "create --name <名> --role <executor|coordinator>",
	Short: "登记小队（成员须指向已登记载体；空成员合法——先立队再补成员，岔口四）",
	RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(squadName) == "" {
			return fmt.Errorf("--name 必填")
		}
		if squadRole != "executor" && squadRole != "coordinator" {
			return fmt.Errorf("--role 只能是 executor 或 coordinator")
		}
		cl, done, err := newTargetClient()
		if err != nil {
			return err
		}
		defer done()
		resp, err := cl.PutSquad(cmd.Context(), squadName, 0, proto.SquadInput{
			Role: squadRole, Members: squadMembers, MaxConcurrency: squadMaxConc})
		if err != nil {
			return fmt.Errorf("登记小队被拒: %w", err)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "已登记小队 %s v%d\n", resp.Name, resp.Version)
		return json.NewEncoder(cmd.OutOrStdout()).Encode(resp)
	},
}

var squadSetCmd = &cobra.Command{
	Use:   "set --name <名> [--role ...] [--member ...] [--max-concurrency N] [--expect V]",
	Short: "修改小队（未给的字段保持现状；--member 给出即整体替换成员集）",
	RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(squadName) == "" {
			return fmt.Errorf("--name 必填")
		}
		cl, done, err := newTargetClient()
		if err != nil {
			return err
		}
		defer done()
		cur, err := cl.Squads(cmd.Context())
		if err != nil {
			return fmt.Errorf("读取现状失败（编辑回路需要当前版本）: %w", err)
		}
		var found *proto.SquadView
		for i := range cur.Squads {
			if cur.Squads[i].Name == squadName {
				found = &cur.Squads[i]
				break
			}
		}
		if found == nil {
			return fmt.Errorf("小队 %s 不存在（handoff squad list 查看）", squadName)
		}
		in := proto.SquadInput{Role: found.Role, Members: found.Members,
			MaxConcurrency: found.MaxConcurrency}
		if cmd.Flags().Changed("role") {
			in.Role = squadRole
		}
		if cmd.Flags().Changed("member") {
			in.Members = squadMembers
		}
		if cmd.Flags().Changed("max-concurrency") {
			in.MaxConcurrency = squadMaxConc
		}
		expect := found.Version
		if cmd.Flags().Changed("expect") {
			expect = squadExpect
		}
		resp, err := cl.PutSquad(cmd.Context(), squadName, expect, in)
		if err != nil {
			return fmt.Errorf("修改小队被拒: %w", err)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "已更新小队 %s v%d\n", resp.Name, resp.Version)
		return json.NewEncoder(cmd.OutOrStdout()).Encode(resp)
	},
}

var squadListCmd = &cobra.Command{
	Use:   "list",
	Short: "列出载体与小队（缺省表格，--json 一行一对象；各行带 CAS 版本）",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cl, done, err := newTargetClient()
		if err != nil {
			return err
		}
		defer done()
		resp, err := cl.Squads(cmd.Context())
		if err != nil {
			return fmt.Errorf("读取登记面: %w", err)
		}
		if squadJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			for _, c := range resp.Carriers {
				if err := enc.Encode(c); err != nil {
					return err
				}
			}
			for _, s := range resp.Squads {
				if err := enc.Encode(s); err != nil {
					return err
				}
			}
			return nil
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
		fmt.Fprintln(w, "类别\t名称\t角色/机器\t成员/CLI\t并发上限\t版本")
		for _, c := range resp.Carriers {
			fmt.Fprintf(w, "载体\t%s\t%s\t%s\t%d\t%d\n",
				c.Name, c.Machine, c.CLI, c.MaxConcurrency, c.Version)
		}
		for _, s := range resp.Squads {
			fmt.Fprintf(w, "小队\t%s\t%s\t%s\t%d\t%d\n",
				s.Name, s.Role, strings.Join(s.Members, ","), s.MaxConcurrency, s.Version)
		}
		return w.Flush()
	},
}

func init() {
	squadCreateCmd.Flags().StringVar(&squadName, "name", "", "小队名（必填）")
	squadCreateCmd.Flags().StringVar(&squadRole, "role", "",
		"executor|coordinator（必填；两种小队不混编）")
	squadCreateCmd.Flags().StringSliceVar(&squadMembers, "member", nil,
		"成员载体名（可重复；须已登记）")
	squadCreateCmd.Flags().IntVar(&squadMaxConc, "max-concurrency", 0, "政策位并发上限（0=不限）")
	squadSetCmd.Flags().StringVar(&squadName, "name", "", "小队名（必填）")
	squadSetCmd.Flags().StringVar(&squadRole, "role", "",
		"executor|coordinator（不给则保持现状）")
	squadSetCmd.Flags().StringSliceVar(&squadMembers, "member", nil,
		"成员载体名（给出即整体替换；不给则保持）")
	squadSetCmd.Flags().IntVar(&squadMaxConc, "max-concurrency", 0,
		"并发上限（不给则保持；0=不限）")
	squadSetCmd.Flags().IntVar(&squadExpect, "expect", 0,
		"乐观锁版本（不给则用刚读取的现状版本）")
	squadListCmd.Flags().BoolVar(&squadJSON, "json", false, "以 NDJSON 输出")
	squadCmd.AddCommand(squadCreateCmd, squadSetCmd, squadListCmd)
	rootCmd.AddCommand(squadCmd)
}
