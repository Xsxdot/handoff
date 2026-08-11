// 本文件实现 handoff upgrade：人工触发的版本检查、升级与回滚。
//
// 职责：
//   - --check（默认）：查最新版本并与本进程比对，只报告
//   - --now：下载、校验、自检、替换当前二进制
//   - --rollback：把 <目标>.prev 换回去
//
// 边界：
//   - **不重启 agentd**。替换的是磁盘上的二进制，正在跑的 agentd 仍是旧进程；
//     本命令只负责把这件事说清楚，重启由用户或自动更新循环完成
//   - 「非托管则拒绝」那条闸**不适用于本命令**：它约束的是自动更新（换完没人
//     拉起就没了），而人工敲命令的人知道自己要不要手动把 agentd 起回来。
//     把人工出口也堵上，非托管机器就彻底没法升级了
package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/buildinfo"
	"github.com/xushixin/handoff/internal/release"
)

// releaseChecker / releaseFetcher 与 internal/selfupdate 里的同名接口形状一致，
// 在这里重新声明是为了让 cmd 不必 import selfupdate 只为拿两个接口。
type releaseChecker interface {
	Latest(ctx context.Context) (release.Release, error)
}

type releaseFetcher interface {
	Fetch(ctx context.Context, rel release.Release, destDir string) (string, error)
}

// 四个缝，测试替换它们以避免联网与动真实二进制。
var (
	newReleaseChecker = func() releaseChecker { return release.NewClient() }
	newReleaseFetcher = func() releaseFetcher { return release.NewInstaller(slog.Default()) }
	activateBinary    = release.Activate
	rollbackBinary    = release.Rollback
)

var (
	upgradeCheck    bool
	upgradeNow      bool
	upgradeRollback bool
)

// currentBinary 返回当前二进制的真实路径。
//
// 必须 EvalSymlinks：装在 ~/.local/bin 的二进制常常是个 symlink，
// 替换 symlink 本身只会把链接换成一个普通文件，链接目标仍是旧版。
func currentBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("取当前可执行文件路径: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return exe, nil
}

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "检查、安装或回滚 handoff 版本",
	Long: "不带参数等同 --check，只报告有没有新版。\n" +
		"--now 立即下载并替换当前二进制；--rollback 换回上一版。\n" +
		"注意：替换的是磁盘上的二进制，正在运行的 agentd 仍是旧进程，需要重启才生效。",
	RunE: func(cmd *cobra.Command, _ []string) error {
		if upgradeNow && upgradeRollback {
			return fmt.Errorf("--now 与 --rollback 不能同时使用")
		}
		out := cmd.OutOrStdout()

		if upgradeRollback {
			target, err := currentBinary()
			if err != nil {
				return err
			}
			if err := rollbackBinary(target); err != nil {
				return fmt.Errorf("回滚失败: %w", err)
			}
			fmt.Fprintf(out, "已回滚   %s\n", target)
			fmt.Fprintf(out, "注意     正在运行的 agentd 仍是回滚前的进程，需要重启才生效。\n")
			return nil
		}

		// 丢掉 ok：读不到构建信息时 Version 仍然有效（它是 ldflags 注入的编译期
		// 常量，与能否读到 vcs 戳无关），与 cmd/status.go 的用法一致
		bi, _ := buildinfo.Read()
		cur := bi.Version
		rel, err := newReleaseChecker().Latest(cmd.Context())
		if err != nil {
			return fmt.Errorf("检查最新版本失败: %w", err)
		}

		curText := cur
		if curText == "" {
			// 如实说「不是 release 构建」而不是显示空——空会被读成「没查到」
			curText = "unknown（非 release 构建）"
		}
		fmt.Fprintf(out, "当前     %s\n", curText)
		fmt.Fprintf(out, "最新     %s\n", rel.Tag)

		if !upgradeNow {
			switch {
			case cur == "":
				fmt.Fprintf(out, "处置     本地构建比不出新旧；要装官方版本请跑 handoff upgrade --now\n")
			case cur == rel.Tag:
				fmt.Fprintf(out, "处置     已是最新，无需升级\n")
			default:
				fmt.Fprintf(out, "处置     handoff upgrade --now 立即升级\n")
			}
			return nil
		}

		target, err := currentBinary()
		if err != nil {
			return err
		}
		// 临时文件必须与目标同目录：os.Rename 的原子性只在同一文件系统内成立
		newPath, err := newReleaseFetcher().Fetch(cmd.Context(), rel, filepath.Dir(target))
		if err != nil {
			return fmt.Errorf("下载或校验失败: %w", err)
		}
		prev, err := activateBinary(newPath, target)
		if err != nil {
			return fmt.Errorf("替换二进制失败: %w", err)
		}
		fmt.Fprintf(out, "已升级   %s → %s\n", curText, rel.Tag)
		fmt.Fprintf(out, "路径     %s\n", target)
		fmt.Fprintf(out, "上一版   %s（handoff upgrade --rollback 可换回）\n", prev)
		// 不说这句，用户会以为升级完就生效了，而正在跑的 agentd 还是旧进程
		fmt.Fprintf(out, "注意     正在运行的 agentd 仍是旧进程，重启它才会用上新版：\n")
		fmt.Fprintf(out, "         托管的用 handoff service install 重装，或等自动更新的空闲窗口。\n")
		return nil
	},
}

func init() {
	upgradeCmd.Flags().BoolVar(&upgradeCheck, "check", false, "只检查有没有新版（默认行为）")
	upgradeCmd.Flags().BoolVar(&upgradeNow, "now", false, "立即下载并替换当前二进制")
	upgradeCmd.Flags().BoolVar(&upgradeRollback, "rollback", false, "换回上一版（<二进制>.prev）")
	rootCmd.AddCommand(upgradeCmd)
}
