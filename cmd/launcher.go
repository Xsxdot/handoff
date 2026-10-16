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
// 任何拉起器端点；本机判定不复用 scheduling.IsLocalMachine（d_cli 无到 d_scheduling 的
// 契约方向，codegraph/target.json）。
package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

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
			return fmt.Errorf("解析当前会话身份: %w", err)
		}
		if cli != desktopSeatCLI {
			slog.Default().Warn("CLI 登记拉起器拒绝非 claude 会话", "project", launcherRegProject, "cli", cli)
			return fmt.Errorf("登记拉起器要在 claude 桌面端会话里执行，当前会话物种是 %q", cli)
		}
		machine, hint, err := resolveDesktopMachine(launcherRegMachine, cmd.Flags().Changed("machine"))
		if err != nil {
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
			return fmt.Errorf("回读项目 %s 的拉起器登记: %w", launcherRegProject, err)
		}
		if !ok {
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
