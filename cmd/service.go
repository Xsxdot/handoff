// 本文件实现 handoff service 子命令：把本机 agentd 交给进程管理器托管。
//
// 职责：
//   - install：解析当前二进制与配置路径，生成并安装服务单元，复核起来了
//   - uninstall：停止并移除单元
//   - status：报告托管状态
//
// 边界：
//   - 不启动/停止 agentd 进程本身：那是管理器的事，本命令只管单元
//   - 不改 handoff 的配置文件：托管与配置是两件事，配置走 handoff init
//   - 托管之后 agentd 的形态会变：手动 Ctrl-C 会被管理器拉回，停服务要用
//     systemctl stop / launchctl bootout。install 成功时会把这句打给用户
package cmd

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/service"
)

// newServiceManager 是构造平台 Manager 的缝，测试替换它注入 fake。
var newServiceManager = service.New

// serviceCmd 是 handoff service 的父命令。
var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "把 agentd 交给本机进程管理器托管（launchd / systemd）",
}

// resolveSpec 组装 Spec：二进制绝对路径 + 配置路径 + 日志路径。
//
// 返回：
//   - 填好的 Spec
//   - 错误：取不到可执行文件路径或加载配置失败
//
// 注意：
//   - BinPath 必须经 EvalSymlinks 解析。装在 ~/.local/bin/handoff 的二进制
//     常常是个 symlink；单元里写 symlink，换版换掉链接目标后单元还指着旧的
func resolveSpec(cfgPath string) (service.Spec, error) {
	exe, err := os.Executable()
	if err != nil {
		return service.Spec{}, fmt.Errorf("取当前可执行文件路径: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return service.Spec{}, fmt.Errorf("加载配置 %s: %w", cfgPath, err)
	}
	return service.Spec{
		BinPath:    exe,
		ConfigPath: cfgPath,
		LogPath:    filepath.Join(cfg.DataDir, "agentd.log"),
	}, nil
}

// effectiveConfigPath 返回本次命令实际使用的配置路径。
func effectiveConfigPath() string {
	if configPath != "" {
		return configPath
	}
	return config.DefaultPath()
}

// installService 安装并启动服务单元，把结果打给用户。
//
// 参数：
//   - out: 面向用户的输出（不是日志）
//   - cfgPath: 传给 agentd 的配置路径
//
// 返回：
//   - 错误：构造管理器、解析 Spec、安装任一步失败
//
// 注意：
//   - 抽成函数是为了让 handoff init 能走**同一条**代码路径追问并代跑（B71）。
//     init 复制一份逻辑的话，两处的托管行为会各自演化
func installService(out io.Writer, cfgPath string) error {
	log := slog.Default()
	m, err := newServiceManager(log)
	if err != nil {
		return err
	}
	spec, err := resolveSpec(cfgPath)
	if err != nil {
		return err
	}
	if err := m.Install(spec); err != nil {
		return fmt.Errorf("安装服务失败: %w", err)
	}
	unit, _ := m.UnitPath()
	fmt.Fprintf(out, "已托管   %s\n", m.Kind())
	fmt.Fprintf(out, "单元     %s\n", unit)
	fmt.Fprintf(out, "二进制   %s\n", spec.BinPath)
	fmt.Fprintf(out, "配置     %s\n", spec.ConfigPath)
	fmt.Fprintf(out, "日志     %s\n", spec.LogPath)
	// 形态变化必须说清楚：托管之后手动 Ctrl-C 会被拉回来，这是最容易
	// 让人以为「服务停不掉」的一点
	fmt.Fprintf(out, "\n注意     agentd 现在由 %s 托管，崩溃或退出都会被自动拉起。\n", m.Kind())
	fmt.Fprintf(out, "         想真正停掉它请用 handoff service uninstall，Ctrl-C 只会让它被重新拉起。\n")
	return nil
}

var serviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "安装并启动服务单元",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return installService(cmd.OutOrStdout(), effectiveConfigPath())
	},
}

var serviceUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "停止并移除服务单元",
	RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := newServiceManager(slog.Default())
		if err != nil {
			return err
		}
		if err := m.Uninstall(); err != nil {
			return fmt.Errorf("卸载服务失败: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "已卸载   %s 单元；agentd 不再被自动拉起\n", m.Kind())
		return nil
	},
}

var serviceStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "查看托管状态",
	RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := newServiceManager(slog.Default())
		if err != nil {
			return err
		}
		st, err := m.Status()
		if err != nil {
			return fmt.Errorf("查询服务状态失败: %w", err)
		}
		unit, _ := m.UnitPath()
		out := cmd.OutOrStdout()
		switch {
		case st.Installed && st.Running:
			fmt.Fprintf(out, "已托管        %s   %s\n", m.Kind(), unit)
		case st.Installed:
			// 装了没跑是一个真实且常见的状态（崩溃循环、被手动 stop），
			// 必须与「没装」分开报，否则用户会去重装一个已经装了的东西
			fmt.Fprintf(out, "已安装但未运行  %s   %s\n", m.Kind(), unit)
			fmt.Fprintf(out, "处置          看日志找原因，或 handoff service install 重装\n")
		default:
			fmt.Fprintf(out, "未托管        %s 上没有 handoff 的服务单元\n", m.Kind())
			fmt.Fprintf(out, "处置          handoff service install\n")
		}
		if st.Detail != "" {
			fmt.Fprintf(out, "管理器原文    %s\n", st.Detail)
		}
		return nil
	},
}

func init() {
	serviceCmd.AddCommand(serviceInstallCmd, serviceUninstallCmd, serviceStatusCmd)
	rootCmd.AddCommand(serviceCmd)
}
