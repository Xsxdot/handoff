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
	"log/slog"
	"strconv"
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
	squadExpect  int
	squadJSON    bool
)

var squadCreateCmd = &cobra.Command{
	Use:   "create --name <名> --role <executor|coordinator>",
	Short: "登记小队（成员须指向已登记载体；空成员合法——先立队再补成员，岔口四）",
	RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(squadName) == "" {
			slog.Default().Warn("squad.create rejected", "reason", "missing_name")
			return fmt.Errorf("--name 必填")
		}
		if squadRole != "executor" && squadRole != "coordinator" {
			slog.Default().Warn("squad.create rejected", "reason", "invalid_role", "role", squadRole)
			return fmt.Errorf("--role 只能是 executor 或 coordinator")
		}
		members, err := squadMemberInputs(squadMembers)
		if err != nil {
			slog.Default().Warn("squad.create rejected", "reason", "invalid_member", "cause", err)
			return err
		}
		slog.Default().Info("squad.create", "name", squadName, "role", squadRole,
			"member_count", len(members), "policy_count", countSquadPolicies(members), "dialed", false)
		cl, done, err := newTargetClient()
		if err != nil {
			slog.Default().Error("squad.create dial failed", "name", squadName, "cause", err)
			return err
		}
		defer done()
		resp, err := cl.PutSquad(cmd.Context(), squadName, 0, proto.SquadInput{
			Role: squadRole, Members: members})
		if err != nil {
			slog.Default().Error("squad.create failed", "name", squadName,
				"member_count", len(members), "cause", err)
			return fmt.Errorf("登记小队被拒: %w", err)
		}
		slog.Default().Info("squad.create succeeded", "name", resp.Name,
			"member_count", len(members), "policy_count", countSquadPolicies(members), "dialed", true)
		fmt.Fprintf(cmd.ErrOrStderr(), "已登记小队 %s v%d\n", resp.Name, resp.Version)
		return json.NewEncoder(cmd.OutOrStdout()).Encode(resp)
	},
}

var squadSetCmd = &cobra.Command{
	Use:   "set --name <名> [--role ...] [--member ...] [--expect V]",
	Short: "修改小队（未给的字段保持现状；--member 给出即整体替换成员集）",
	RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(squadName) == "" {
			slog.Default().Warn("squad.set rejected", "reason", "missing_name")
			return fmt.Errorf("--name 必填")
		}
		var members []proto.SquadMember
		var err error
		if cmd.Flags().Changed("member") {
			members, err = squadMemberInputs(squadMembers)
			if err != nil {
				slog.Default().Warn("squad.set rejected", "name", squadName,
					"reason", "invalid_member", "cause", err)
				return err
			}
		}
		slog.Default().Info("squad.set", "name", squadName,
			"member_changed", cmd.Flags().Changed("member"),
			"member_count", len(members), "policy_count", countSquadPolicies(members), "dialed", false)
		cl, done, err := newTargetClient()
		if err != nil {
			slog.Default().Error("squad.set dial failed", "name", squadName, "cause", err)
			return err
		}
		defer done()
		cur, err := cl.Squads(cmd.Context())
		if err != nil {
			slog.Default().Error("squad.set read failed", "name", squadName, "cause", err)
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
			slog.Default().Warn("squad.set rejected", "name", squadName, "reason", "not_found")
			return fmt.Errorf("小队 %s 不存在（handoff squad list 查看）", squadName)
		}
		in := proto.SquadInput{Role: found.Role, Members: found.Members}
		if cmd.Flags().Changed("role") {
			in.Role = squadRole
		}
		if cmd.Flags().Changed("member") {
			in.Members = members
		}
		expect := found.Version
		if cmd.Flags().Changed("expect") {
			expect = squadExpect
		}
		resp, err := cl.PutSquad(cmd.Context(), squadName, expect, in)
		if err != nil {
			slog.Default().Error("squad.set failed", "name", squadName,
				"member_count", len(in.Members), "cause", err)
			return fmt.Errorf("修改小队被拒: %w", err)
		}
		slog.Default().Info("squad.set succeeded", "name", resp.Name,
			"member_count", len(in.Members), "policy_count", countSquadPolicies(in.Members), "dialed", true)
		fmt.Fprintf(cmd.ErrOrStderr(), "已更新小队 %s v%d\n", resp.Name, resp.Version)
		return json.NewEncoder(cmd.OutOrStdout()).Encode(resp)
	},
}

var squadListCmd = &cobra.Command{
	Use:   "list",
	Short: "列出载体与小队（缺省表格，--json 一行一对象；各行带 CAS 版本）",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		slog.Default().Info("squad.list", "json", squadJSON, "dialed", false)
		cl, done, err := newTargetClient()
		if err != nil {
			slog.Default().Error("squad.list dial failed", "cause", err)
			return err
		}
		defer done()
		resp, err := cl.Squads(cmd.Context())
		if err != nil {
			slog.Default().Error("squad.list failed", "cause", err)
			return fmt.Errorf("读取登记面: %w", err)
		}
		policyCount, memberCount := 0, 0
		for _, squad := range resp.Squads {
			memberCount += len(squad.Members)
			policyCount += countSquadPolicies(squad.Members)
		}
		slog.Default().Info("squad.list succeeded", "carrier_count", len(resp.Carriers),
			"squad_count", len(resp.Squads), "member_count", memberCount,
			"policy_count", policyCount, "dialed", true)
		if squadJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			for _, c := range resp.Carriers {
				if err := enc.Encode(c); err != nil {
					slog.Default().Error("squad.list render failed", "kind", "carrier", "cause", err)
					return err
				}
			}
			for _, s := range resp.Squads {
				if err := enc.Encode(s); err != nil {
					slog.Default().Error("squad.list render failed", "kind", "squad", "cause", err)
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
			fmt.Fprintf(w, "小队\t%s\t%s\t%s\t-\t%d\n",
				s.Name, s.Role, formatSquadMembers(s.Members), s.Version)
		}
		if err := w.Flush(); err != nil {
			slog.Default().Error("squad.list render failed", "kind", "table", "cause", err)
			return err
		}
		return nil
	},
}

func init() {
	squadCreateCmd.Flags().StringVar(&squadName, "name", "", "小队名（必填）")
	squadCreateCmd.Flags().StringVar(&squadRole, "role", "",
		"executor|coordinator（必填；两种小队不混编）")
	squadCreateCmd.Flags().StringArrayVar(&squadMembers, "member", nil,
		"成员载体及可选政策位（重复使用：carrier 或 carrier:N；须已登记）")
	squadSetCmd.Flags().StringVar(&squadName, "name", "", "小队名（必填）")
	squadSetCmd.Flags().StringVar(&squadRole, "role", "",
		"executor|coordinator（不给则保持现状）")
	squadSetCmd.Flags().StringArrayVar(&squadMembers, "member", nil,
		"成员载体及可选政策位（给出即整体替换；格式 carrier 或 carrier:N）")
	squadSetCmd.Flags().IntVar(&squadExpect, "expect", 0,
		"乐观锁版本（不给则用刚读取的现状版本）")
	squadListCmd.Flags().BoolVar(&squadJSON, "json", false, "以 NDJSON 输出")
	squadCmd.AddCommand(squadCreateCmd, squadSetCmd, squadListCmd)
	rootCmd.AddCommand(squadCmd)
}

// parseSquadMember 解析一个完整 --member 值。StringArray 保留空格、斜杠和中文
// 成员名；只有整个政策值留空（即无冒号）才表示不限，避免把非法 0 静默规范化。
func parseSquadMember(raw string) (proto.SquadMember, error) {
	if strings.TrimSpace(raw) == "" {
		return proto.SquadMember{}, fmt.Errorf("--member 不能为空；合法示例：--member c1 或 --member c1:2")
	}
	if strings.Count(raw, ":") > 1 {
		return proto.SquadMember{}, fmt.Errorf("--member 载体名不能含冒号；合法示例：--member c1 或 --member c1:2")
	}
	carrier := raw
	max := 0
	if i := strings.IndexByte(raw, ':'); i >= 0 {
		carrier = raw[:i]
		rawMax := raw[i+1:]
		if strings.TrimSpace(carrier) == "" {
			return proto.SquadMember{}, fmt.Errorf("--member 必须先给载体名；合法示例：--member c1:2")
		}
		value, err := strconv.Atoi(rawMax)
		if err != nil || value <= 0 {
			return proto.SquadMember{}, fmt.Errorf("--member 政策必须是正整数；留空表示不限；合法示例：--member %s:2", carrier)
		}
		max = value
	}
	return proto.SquadMember{Carrier: carrier, MaxConcurrency: max}, nil
}

// squadMemberInputs 逐个解析重复 --member；遇到首个非法值即停止，调用方在
// 创建 HTTP client 之前调用它，保证用户输入错误不会拨号。
func squadMemberInputs(raw []string) ([]proto.SquadMember, error) {
	members := make([]proto.SquadMember, 0, len(raw))
	for _, value := range raw {
		member, err := parseSquadMember(value)
		if err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, nil
}

func countSquadPolicies(members []proto.SquadMember) int {
	count := 0
	for _, member := range members {
		if member.MaxConcurrency > 0 {
			count++
		}
	}
	return count
}

func formatSquadMembers(members []proto.SquadMember) string {
	labels := make([]string, len(members))
	for i, member := range members {
		labels[i] = member.Carrier
		if member.MaxConcurrency > 0 {
			labels[i] += fmt.Sprintf("/%d", member.MaxConcurrency)
		}
	}
	return strings.Join(labels, ",")
}
