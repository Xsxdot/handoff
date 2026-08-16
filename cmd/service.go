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
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/service"
	"github.com/spf13/cobra"
)

// osExecutable 是取当前二进制路径的缝。测试换成一个稳定路径，避免
// go test 自己的缓存二进制被 isEphemeralBin 拒掉，把 install 用例带崩。
var osExecutable = os.Executable

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
//   - go run 的 os.Executable 落在编译缓存里。launchd 加载那种路径会
//     Bootstrap failed: 5: Input/output error，缓存一清服务也死。必须换成
//     已安装的稳定二进制，找不到就硬拒，不能把临时路径写进单元。
func resolveSpec(cfgPath string) (service.Spec, error) {
	exe, err := osExecutable()
	if err != nil {
		return service.Spec{}, fmt.Errorf("取当前可执行文件路径: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	exe, err = resolveServiceBinFrom(exe, durableBinCandidates())
	if err != nil {
		return service.Spec{}, err
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

// isEphemeralBin 判断路径是不是 go run / go test 的编译缓存，或落在临时目录里。
//
// 这类路径不能写进 launchd/systemd：管理器下次拉起时文件多半已经没了，
// macOS 上 launchctl bootstrap 还会直接报 Input/output error（exit 5）。
func isEphemeralBin(path string) bool {
	if path == "" {
		return true
	}
	slashed := filepath.ToSlash(path)
	// go run / go test：目录名是 go-build 或 go-build<数字>，不能只认 /go-build/。
	for _, part := range strings.Split(slashed, "/") {
		if strings.HasPrefix(part, "go-build") {
			return true
		}
	}
	// macOS 的 TempDir 是 /var/folders/.../T，Linux 常见是 /tmp；两边都要认。
	var tmps []string
	if tmp := os.TempDir(); tmp != "" {
		tmps = append(tmps, tmp)
	}
	tmps = append(tmps, "/tmp", "/var/tmp")
	for _, tmp := range tmps {
		tmpSlash := strings.TrimRight(filepath.ToSlash(tmp), "/")
		if tmpSlash == "" || tmpSlash == "/" {
			continue
		}
		if strings.HasPrefix(slashed, tmpSlash+"/") {
			return true
		}
	}
	return false
}

// resolveServiceBinFrom 把当前可执行文件收成可以写进服务单元的稳定路径。
//
// 参数：
//   - exe: 已经 EvalSymlinks 过的当前进程路径
//   - candidates: 本机可能装过 handoff 的稳定路径，按优先序
//
// 返回：
//   - 可托管的绝对路径
//   - 当前是临时文件且找不到已安装二进制时的错误
//
// 注意：宁可拒绝，也不把 go-build 缓存写进 plist。现场用 go run . init
// 代装服务时，那条路径就是用户日志里的
// Library/Caches/go-build/.../handoff。
func resolveServiceBinFrom(exe string, candidates []string) (string, error) {
	if exe != "" && !isEphemeralBin(exe) {
		slog.Debug("服务二进制使用当前进程", "bin", exe)
		return exe, nil
	}
	slog.Warn("当前二进制是临时编译产物，不能交给进程管理器", "exe", exe)
	for _, c := range candidates {
		if c == "" {
			continue
		}
		resolved := c
		if r, err := filepath.EvalSymlinks(c); err == nil {
			resolved = r
		}
		if resolved == exe || isEphemeralBin(resolved) {
			continue
		}
		if !regularFileExists(resolved) {
			continue
		}
		slog.Info("改用已安装的 handoff 二进制托管", "bin", resolved, "rejected", exe)
		return resolved, nil
	}
	return "", fmt.Errorf("当前二进制是 go run / 编译缓存里的临时文件（%s），不能写进服务单元。请用安装好的 handoff 执行 service install，或先 go build -o ~/.local/bin/handoff .", exe)
}

// regularFileExists 判断 path 是已存在的普通文件。目录或打不开都当没有。
func regularFileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// durableBinCandidates 返回本机可能装过 handoff 的稳定路径，按优先序。
//
// install.sh 默认落点是 ~/.local/bin；HANDOFF_INSTALL_DIR 覆盖那个目录。
// PATH 上的 handoff 放最后：开发机上它有时仍指向一份临时文件。
func durableBinCandidates() []string {
	var out []string
	if dir := os.Getenv("HANDOFF_INSTALL_DIR"); dir != "" {
		out = append(out, filepath.Join(dir, "handoff"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".local", "bin", "handoff"))
	}
	if p, err := exec.LookPath("handoff"); err == nil {
		out = append(out, p)
	}
	return out
}
