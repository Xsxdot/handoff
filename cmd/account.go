// handoff account 命令族：账号额度的只读面与立即探测（B453 S11，spec F11/F12）。
//
// 为什么走 agentd HTTP：账号读面（状态推导）与探测执行权都在服务端——探测只能在
// 账号所在机执行，入口机负责转发；CLI 直连账本既拿不到推导口径的唯一实现，也触发
// 不了他机探测。登记与编辑走控制台（spec Out of Scope：本期无 add/edit/rm）。
//
// stdout：list 缺省表格（账号行 + 其下登录行，B464）、--json 一行一个 AccountView（与 GET
// /api/accounts 元素同形，含 logins）；
// probe 打表头 + 新读数行。人话与诊断走 stderr / 结构化日志。
package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

var accountJSON bool

// accountDiscoverMachine 是 account discover 的 --machine（空 = 全队扇出）。
var accountDiscoverMachine string

var accountCmd = &cobra.Command{Use: "account",
	Short: "账号与额度：只读列表与立即探测（登记/编辑走控制台「◔ 账号」页）"}

var accountListCmd = &cobra.Command{
	Use:   "list",
	Short: "列出账号与额度读数（缺省表格，--json 一行一个账号对象）",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		slog.Default().Info("account.list", "json", accountJSON)
		cl, done, err := newTargetClient()
		if err != nil {
			slog.Default().Error("account.list dial failed", "cause", err)
			return err
		}
		defer done()
		resp, err := cl.Accounts(cmd.Context())
		if err != nil {
			slog.Default().Error("account.list failed", "cause", err)
			return fmt.Errorf("读取账号列表: %w", err)
		}
		slog.Default().Info("account.list succeeded", "accounts", len(resp.Accounts))
		if accountJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			for _, v := range resp.Accounts {
				if err := enc.Encode(v); err != nil {
					slog.Default().Error("account.list render failed", "name", v.Name, "cause", err)
					return err
				}
			}
			return nil
		}
		return renderAccountTable(cmd.OutOrStdout(), resp.Accounts, time.Now())
	},
}

var accountDiscoverCmd = &cobra.Command{
	Use:   "discover",
	Short: "让全部机器（或 --machine 指定的一台）立即发现已登录账号",
	Long: "向入口机发起一次立即发现（POST /api/accounts/discover）：不带 --machine 时全部已登记机器" +
		"（含本机）各扫一次；单台失败只写在那一行。全部成功退出 0，任一失败退出 1。",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		machine := strings.TrimSpace(accountDiscoverMachine)
		slog.Default().Info("account.discover", "machine", machine)
		cl, done, err := newTargetClient()
		if err != nil {
			slog.Default().Error("account.discover dial failed", "cause", err)
			return err
		}
		defer done()
		resp, err := cl.DiscoverAccounts(cmd.Context(), machine)
		if err != nil {
			slog.Default().Error("account.discover failed", "machine", machine, "cause", err)
			return fmt.Errorf("立即发现: %w", err)
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
		failed := 0
		for _, m := range resp.Machines {
			if !m.OK {
				failed++
				fmt.Fprintf(w, "%s\t失败\t%s\n", m.Machine, m.Error)
				continue
			}
			detail := "成功"
			if m.At != nil {
				detail = "成功\t" + m.At.Local().Format("2006-01-02 15:04:05")
			}
			fmt.Fprintf(w, "%s\t%s\n", m.Machine, detail)
		}
		if err := w.Flush(); err != nil {
			slog.Default().Error("account.discover render failed", "cause", err)
			return err
		}
		slog.Default().Info("account.discover succeeded", "machines", len(resp.Machines), "failed", failed)
		if failed > 0 {
			return fmt.Errorf("有 %d 台机器发现失败（见 stdout 各行原因）", failed)
		}
		return nil
	},
}

var accountProbeCmd = &cobra.Command{
	Use:   "probe <名称或 id>",
	Short: "让账号所在机立即探测一次并打印新读数；参数可为账号名称或账号 id（探测失败退出码 1）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		slog.Default().Info("account.probe", "name", name)
		cl, done, err := newTargetClient()
		if err != nil {
			slog.Default().Error("account.probe dial failed", "name", name, "cause", err)
			return err
		}
		defer done()
		v, err := cl.ProbeAccount(cmd.Context(), name)
		if err != nil {
			slog.Default().Error("account.probe failed", "name", name, "cause", err)
			return fmt.Errorf("立即探测账号 %s: %w", name, err)
		}
		if err := renderAccountTable(cmd.OutOrStdout(), []proto.AccountView{*v}, time.Now()); err != nil {
			return err
		}
		if accountProbeFailed(*v) {
			msg := "原因未知"
			if v.LastError != nil {
				msg = v.LastError.Category + "：" + v.LastError.Message
			}
			slog.Default().Warn("account.probe concluded failure", "name", name, "state", v.State, "cause", msg)
			return fmt.Errorf("账号 %s 探测失败：%s", name, msg)
		}
		slog.Default().Info("account.probe succeeded", "name", name, "state", v.State)
		return nil
	},
}

// accountProbeFailed 报告「最近一次尝试」是否失败：成功时读数时刻 = 尝试时刻，失败时
// 尝试时刻晚于最近成功读数（或从无读数）。不看 state——撞额度标记优先于探测失败，
// state 可能是 out 而本次探测其实失败了。
func accountProbeFailed(v proto.AccountView) bool {
	if v.LastAttemptAt == nil {
		return true // 探测后仍无尝试记录：服务端没写成，按失败报
	}
	return v.Reading == nil || v.Reading.ProbedAt.Before(*v.LastAttemptAt)
}

var accountStateLabel = map[string]string{
	"ok": "正常", "low": "低额度", "out": "无额度", "fail": "探测失败", "none": "未探测", "na": "不支持额度探测",
}

var accountWindowLabel = map[string]string{"session": "5h", "weekly": "周", "monthly": "月"}

// loginStateLabel 是登录状态的人话（L7；expired 是证据型，plan Q5）。
var loginStateLabel = map[string]string{"ok": "正常", "expired": "已过期", "gone": "已失效"}

// renderAccountTable 打表格：每个账号一行（名称/类型/状态/窗口已用/最近重置/上次探测/说明），
// 其下每条登录缩进一行（B464 C1 表格形）：所在机:HOME、来源、登录状态、代理（打码）、所用载体。
// 可探登录（读数来源）以「*」标注。
func renderAccountTable(out io.Writer, views []proto.AccountView, now time.Time) error {
	w := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "名称\t类型\t状态\t窗口已用\t最近重置\t上次探测\t说明")
	for _, v := range views {
		state := accountStateLabel[v.State]
		if v.Ignored != nil {
			// B467 C4：已忽略不是新的 state 值（词表不变），只是表格里更醒目的展示。
			state = "已忽略"
		} else if state == "" {
			state = v.State
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", v.Name, v.Kind, state,
			accountWindows(v), accountNextReset(v, now), accountLastProbe(v, now), accountNote(v))
		for _, l := range v.Logins {
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\t\n", accountLoginMark(v, l), l.Machine+":"+l.Home, l.Source,
				accountLoginState(l), accountLoginProxy(l), accountLoginCarriers(l))
		}
	}
	if err := w.Flush(); err != nil {
		slog.Default().Error("account render failed", "cause", err)
		return err
	}
	return nil
}

func accountLoginMark(v proto.AccountView, l proto.LoginView) string {
	if l.ID == v.ProbeLogin {
		return "└*登录"
	}
	return "└ 登录"
}

func accountLoginState(l proto.LoginView) string {
	if s := loginStateLabel[l.State]; s != "" {
		return s
	}
	return l.State
}

func accountLoginProxy(l proto.LoginView) string {
	if l.Proxy == "" {
		return "代理:-"
	}
	return "代理:" + l.Proxy // 服务端已打码
}

func accountLoginCarriers(l proto.LoginView) string {
	if len(l.Carriers) == 0 {
		return "载体:-"
	}
	return "载体:" + strings.Join(l.Carriers, ",")
}

func accountWindows(v proto.AccountView) string {
	if v.Reading == nil || len(v.Reading.Windows) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(v.Reading.Windows))
	for _, win := range v.Reading.Windows {
		label := accountWindowLabel[win.Key]
		if label == "" {
			label = win.Key
		}
		if win.Group != "" {
			label = win.Group + " " + label
		}
		// 显示取整（读数本身保留原值）；组名原样，缩写归界面层。
		parts = append(parts, label+" "+strconv.FormatFloat(math.Round(win.UsedPercent), 'f', 0, 64)+"%")
	}
	return strings.Join(parts, "  ")
}

// accountNextReset 取尚未到来的最早重置时刻，打成「2h41m后」。
func accountNextReset(v proto.AccountView, now time.Time) string {
	if v.Reading == nil {
		return "-"
	}
	var earliest *time.Time
	for _, win := range v.Reading.Windows {
		if win.ResetsAt == nil || !win.ResetsAt.After(now) {
			continue
		}
		if earliest == nil || win.ResetsAt.Before(*earliest) {
			earliest = win.ResetsAt
		}
	}
	if earliest == nil {
		return "-"
	}
	return humanDuration(earliest.Sub(now)) + "后"
}

func accountLastProbe(v proto.AccountView, now time.Time) string {
	if v.Reading == nil {
		if v.LastAttemptAt != nil {
			return "从未成功"
		}
		return "-"
	}
	s := humanDuration(now.Sub(v.Reading.ProbedAt)) + "前"
	if v.Stale {
		s += "（已过期）"
	}
	return s
}

func accountNote(v proto.AccountView) string {
	if v.LastError == nil {
		return "-"
	}
	return "△ " + v.LastError.Message
}

// humanDuration 打成「1d3h」「2h41m」「3m」「<1m」。
func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return "<1m"
	}
	d = d.Round(time.Minute)
	days, hours, mins := int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour), int(d%time.Hour/time.Minute)
	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

func init() {
	accountListCmd.Flags().BoolVar(&accountJSON, "json", false, "一行一个账号对象（与 GET /api/accounts 元素同形）")
	accountDiscoverCmd.Flags().StringVar(&accountDiscoverMachine, "machine", "", "只让这台机器立即发现（缺省 = 全部已登记机器）")
	accountCmd.AddCommand(accountListCmd, accountDiscoverCmd, accountProbeCmd)
	rootCmd.AddCommand(accountCmd)
}
