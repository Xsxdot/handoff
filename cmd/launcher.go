// launcher.go —— `handoff launcher` 命令族与 `handoff status` 的「拉起器」段（B434 S3）。
//
// 职责：
//   - register / show / unregister：把当前 Claude 桌面端会话登记为某项目的拉起器、
//     查看登记（stdout JSON）、注销（契约 §3.4、§3.10、条 139–143）。拉起器是人开过的
//     一场普通桌面端会话；人不在时 agentd 给它写信 + continue，由它代开协调者会话。
//   - probeLaunchers / renderLauncherSection（T8）：status 段逐项目列 project /
//     desktop_session / machine / 可达（条 159–161、§11.2）。
//
// 边界：只经 ledger.Store 的四个拉起器方法读写账本；登记覆盖式（条 140）、不落卡事件
// （条 143）。登记身份只取当前会话出示（不接受 --cli/--session 手填）：登记的 CLI 会话
// id 是信箱文件名的键，手填错一个字信就没人收。探活只读各 agentd 的 /api/status，不调
// 任何拉起器端点；本机判定不复用 scheduling.IsLocalMachine（按架构法依赖方向，
// d_cli 无到 d_scheduling 的契约方向）。
package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

var (
	launcherRegProject, launcherRegSession, launcherRegMachine string
	launcherShowProject                                        string
	launcherUnregProject                                       string
)

var launcherCmd = &cobra.Command{
	Use:   "launcher",
	Short: "项目拉起器登记（人不在时由它代开 Claude 桌面端协调者会话）",
}

// launcherWire 是 launcher register / show 的 stdout 线格式。键名与 desktop_launchers
// 列名同为 snake_case（cli_session / desktop_session 与 proto.DesktopDeliverReq 同名）；
// registered_at 为 RFC3339Nano UTC。ledger.DesktopLauncher 无 json tag，故在此显式投影。
type launcherWire struct {
	Project        string    `json:"project"`
	DesktopSession string    `json:"desktop_session"`
	CLISession     string    `json:"cli_session"`
	Machine        string    `json:"machine"`
	Actor          string    `json:"actor"`
	RegisteredAt   time.Time `json:"registered_at"`
}

// toLauncherWire 把账本登记投影成 stdout 线格式（逐字段，无省略）。
func toLauncherWire(l ledger.DesktopLauncher) launcherWire {
	return launcherWire{
		Project: l.Project, DesktopSession: l.DesktopSession, CLISession: l.CLISession,
		Machine: l.Machine, Actor: l.Actor, RegisteredAt: l.RegisteredAt.UTC(),
	}
}

// launcherRegisterCmd 把当前 claude 会话登记为项目拉起器（覆盖式）。
// 注意：只认当前会话出示的身份，cli 段必须是 claude（契约条 141）；actor 记出示的席位
// 身份 cli:claude#<CLI 会话 id>，登记来源由它追溯。
var launcherRegisterCmd = &cobra.Command{
	Use:   "register --project <名> --desktop-session <local_…>",
	Short: "把当前 claude 桌面端会话登记为项目的拉起器（每项目至多一个，再登记即覆盖）",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		slog.Default().Info("CLI 登记拉起器入口", "project", launcherRegProject, "desktop_session", launcherRegSession)
		identity, err := currentSeatIdentity("", "")
		if err != nil {
			slog.Default().Warn("CLI 登记拉起器身份出示失败", "project", launcherRegProject, "cause", err)
			return fmt.Errorf("登记拉起器要在 claude 桌面端会话里执行: %w", err)
		}
		cli, cliSession, err := proto.ParseSeatIdentity(identity)
		if err != nil {
			slog.Default().Warn("CLI 登记拉起器解析会话身份失败", "project", launcherRegProject, "cause", err)
			return fmt.Errorf("解析当前会话身份: %w", err)
		}
		if cli != desktopSeatCLI {
			slog.Default().Warn("CLI 登记拉起器拒绝非 claude 会话", "project", launcherRegProject, "cli", cli)
			return fmt.Errorf("登记拉起器要在 claude 桌面端会话里执行，当前会话物种是 %q", cli)
		}
		machine, hint, err := resolveDesktopMachine(launcherRegMachine, cmd.Flags().Changed("machine"))
		if err != nil {
			slog.Default().Warn("CLI 登记拉起器取机器名失败", "project", launcherRegProject, "cause", err)
			return err
		}
		if hint != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), hint)
		}
		st, err := openLedger()
		if err != nil {
			slog.Default().Warn("CLI 登记拉起器打开账本失败", "project", launcherRegProject, "cause", err)
			return err
		}
		defer st.Close()
		if err := st.RegisterDesktopLauncher(ledger.DesktopLauncher{
			Project: launcherRegProject, DesktopSession: launcherRegSession,
			CLISession: cliSession, Machine: machine, Actor: identity,
		}); err != nil {
			slog.Default().Warn("CLI 登记拉起器失败", "project", launcherRegProject, "cause", err)
			return fmt.Errorf("登记项目 %s 的拉起器: %w", launcherRegProject, err)
		}
		l, ok, err := st.DesktopLauncherOf(launcherRegProject)
		if err != nil {
			slog.Default().Warn("CLI 登记拉起器回读失败", "project", launcherRegProject, "cause", err)
			return fmt.Errorf("回读项目 %s 的拉起器登记: %w", launcherRegProject, err)
		}
		if !ok {
			slog.Default().Warn("CLI 登记拉起器后回读不到登记", "project", launcherRegProject)
			return fmt.Errorf("登记项目 %s 的拉起器后回读不到登记", launcherRegProject)
		}
		slog.Default().Info("CLI 登记拉起器完成", "project", l.Project, "desktop_session", l.DesktopSession, "machine", l.Machine)
		return json.NewEncoder(cmd.OutOrStdout()).Encode(toLauncherWire(l))
	},
}

// launcherShowCmd 查看拉起器登记：给 --project 出单个对象（未登记 → 非零退出 + 指路），
// 否则出全部登记的数组（按 project 字节序，空为 []）。
var launcherShowCmd = &cobra.Command{
	Use:   "show [--project <名>]",
	Short: "查看拉起器登记（stdout JSON：给 --project 出单个对象，否则出全部的数组）",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		st, err := openLedger()
		if err != nil {
			slog.Default().Warn("CLI 查看拉起器打开账本失败", "cause", err)
			return err
		}
		defer st.Close()
		enc := json.NewEncoder(cmd.OutOrStdout())
		if launcherShowProject != "" {
			l, ok, err := st.DesktopLauncherOf(launcherShowProject)
			if err != nil {
				slog.Default().Warn("CLI 查看拉起器读登记失败", "project", launcherShowProject, "cause", err)
				return fmt.Errorf("读项目 %s 的拉起器登记: %w", launcherShowProject, err)
			}
			if !ok {
				slog.Default().Info("CLI 查看拉起器：项目未登记", "project", launcherShowProject)
				return fmt.Errorf("项目 %s 未登记拉起器：在该项目的一场 claude 桌面端会话里执行 handoff launcher register --project %s --desktop-session <local_…>",
					launcherShowProject, launcherShowProject)
			}
			slog.Default().Info("CLI 查看拉起器", "project", l.Project)
			return enc.Encode(toLauncherWire(l))
		}
		ls, err := st.ListDesktopLaunchers()
		if err != nil {
			slog.Default().Warn("CLI 列拉起器登记失败", "cause", err)
			return fmt.Errorf("列拉起器登记: %w", err)
		}
		rows := make([]launcherWire, 0, len(ls))
		for _, l := range ls {
			rows = append(rows, toLauncherWire(l))
		}
		slog.Default().Info("CLI 列出拉起器", "count", len(rows))
		return enc.Encode(rows)
	},
}

// launcherUnregisterCmd 注销项目拉起器（幂等：没登记也成功）。
var launcherUnregisterCmd = &cobra.Command{
	Use:   "unregister --project <名>",
	Short: "注销项目的拉起器登记（幂等）",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		slog.Default().Info("CLI 注销拉起器入口", "project", launcherUnregProject)
		st, err := openLedger()
		if err != nil {
			slog.Default().Warn("CLI 注销拉起器打开账本失败", "project", launcherUnregProject, "cause", err)
			return err
		}
		defer st.Close()
		if err := st.UnregisterDesktopLauncher(launcherUnregProject); err != nil {
			slog.Default().Warn("CLI 注销拉起器失败", "project", launcherUnregProject, "cause", err)
			return fmt.Errorf("注销项目 %s 的拉起器: %w", launcherUnregProject, err)
		}
		slog.Default().Info("CLI 注销拉起器完成", "project", launcherUnregProject)
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

func init() {
	launcherRegisterCmd.Flags().StringVar(&launcherRegProject, "project", "", "项目名（与卡的 project 同值域，必填）")
	launcherRegisterCmd.Flags().StringVar(&launcherRegSession, "desktop-session", "", "当前会话的 Claude 桌面端会话 id（local_…，必填）")
	launcherRegisterCmd.Flags().StringVar(&launcherRegMachine, "machine", "", "拉起器所在机器（缺省本机 hostname）")
	_ = launcherRegisterCmd.MarkFlagRequired("project")
	_ = launcherRegisterCmd.MarkFlagRequired("desktop-session")
	launcherShowCmd.Flags().StringVar(&launcherShowProject, "project", "", "只看该项目（缺省列出全部）")
	launcherUnregisterCmd.Flags().StringVar(&launcherUnregProject, "project", "", "项目名（必填）")
	_ = launcherUnregisterCmd.MarkFlagRequired("project")
	launcherCmd.AddCommand(launcherRegisterCmd, launcherShowCmd, launcherUnregisterCmd)
	rootCmd.AddCommand(launcherCmd)
}

// launcherProbeTimeout 是 status 段对每台拉起器机器探活的上限：status 是诊断命令，
// 一台挂掉的机器不该把整条命令拖住。
const launcherProbeTimeout = 3 * time.Second

// launcherProbe 是 status「拉起器」段的一行。
type launcherProbe struct {
	Launcher ledger.DesktopLauncher
	Reach    string // 可达 / 不可达（原因）/ 不可探（非本机）
	Warn     string // 非空时行尾追加「⚠ <Warn>」
}

// probeLaunchers 逐条探活拉起器所在机器，顺序与入参一致（ListDesktopLaunchers 的 project 字节序）。
// 参数：ctx 为命令上下文；ls 为账本登记。返回：每条登记一行结论；ls 为空返回 nil。
func probeLaunchers(ctx context.Context, ls []ledger.DesktopLauncher) []launcherProbe {
	if len(ls) == 0 {
		return nil
	}
	cfg := loadCLIConfig()
	host, err := os.Hostname()
	if err != nil {
		slog.Default().Warn("status 取本机 hostname 失败，拉起器本机判定只认空机器名", "cause", err)
	}
	out := make([]launcherProbe, 0, len(ls))
	for _, l := range ls {
		reach, warn := probeLauncherMachine(ctx, cfg, host, l.Machine)
		slog.Default().Info("status 拉起器探活", "project", l.Project, "machine", l.Machine, "reach", reach)
		out = append(out, launcherProbe{Launcher: l, Reach: reach, Warn: warn})
	}
	return out
}

// probeLauncherMachine 判一台拉起器机器够不够得着。
//
// 参数：cfg 为本机 CLI 配置；host 为本机 hostname（取不到为空串）；machine 为登记值。
// 返回：reach ∈ {可达, 不可达（原因）, 不可探（非本机）}；warn 非空表示机器名不利于异机寻址。
// 判定顺序（契约 §3.10、r10 第 6 条）：空串、local、本机或等于 hostname → 探本机 agentd
// （LocalEndpoint，不受 --target 影响）；本机配置 targets 里有同名项 → 经该 target 探；
// 其它名字 → 不可探（非本机）+ 告警。
// 为什么别名照探本机却仍告警：在本机看它们确实指本机，但异机发起的代开会把 local / 本机 /
// 空串解释成发起机自己——对拉起器登记而言恰是该提醒的错配（契约 §11.2）。本机别名词表与
// scheduling.IsLocalMachine 同义而不复用它：d_cli 无到 d_scheduling 的契约方向。
func probeLauncherMachine(ctx context.Context, cfg *config.Config, host, machine string) (string, string) {
	ctx, cancel := context.WithTimeout(ctx, launcherProbeTimeout)
	defer cancel()
	if machine == "" || machine == "local" || machine == "本机" || (host != "" && machine == host) {
		addr, token, err := LocalEndpoint()
		if err != nil {
			return fmt.Sprintf("不可达（%v）", err), ""
		}
		reach := agentdReach(ctx, client.New(addr, token))
		if machine == host {
			return reach, ""
		}
		return reach, fmt.Sprintf("机器名 %q 是本机别名：异机发起的代开会把它当成发起机自己，请用 --machine <本机 target 名> 重新登记", machine)
	}
	if _, ok := cfg.Targets[machine]; ok {
		c, done, err := newTargetClientNamed(machine)
		if err != nil {
			return fmt.Sprintf("不可达（%v）", err), ""
		}
		defer done()
		return agentdReach(ctx, c), ""
	}
	return "不可探（非本机）", fmt.Sprintf("机器名 %q 既不是本机也不在本机配置 targets 里；异机转交按 target 名寻址，请确认它与各 agentd 的 targets 一致", machine)
}

// agentdReach 用 /api/status 判一台 agentd 够不够得着；老 agentd 的 404（不支持
// /api/status）已证明 TCP、HTTP 与 Bearer 全通，算可达（与 statusCmd 同一判据）。
func agentdReach(ctx context.Context, c *client.Client) string {
	if _, err := c.Status(ctx); err != nil && !errors.Is(err, client.ErrStatusUnsupported) {
		return fmt.Sprintf("不可达（%v）", err)
	}
	return "可达"
}

// renderLauncherSection 渲染 status「拉起器」段。
// 参数：rows 为探活结论；listErr 为读登记的错误。
// 注意：无登记且读成功时整段不打（不改变既有输出）；读登记失败只打一行原因——status 是
// 诊断命令，账本故障不该把它整体打挂。
func renderLauncherSection(w io.Writer, rows []launcherProbe, listErr error) {
	if listErr != nil {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "拉起器   读登记失败：%v\n", listErr)
		return
	}
	if len(rows) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "拉起器")
	for _, r := range rows {
		machine := r.Launcher.Machine
		if machine == "" {
			machine = "（空）"
		}
		line := fmt.Sprintf("  %s  %s  %s  %s", r.Launcher.Project, r.Launcher.DesktopSession, machine, r.Reach)
		if r.Warn != "" {
			line += "  ⚠ " + r.Warn
		}
		fmt.Fprintln(w, line)
	}
}
