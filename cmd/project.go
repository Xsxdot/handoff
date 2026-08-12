// 本文件实现 handoff project 子命令族：把一个项目登记到本机与（可选的）一台
// 远程开发机上，并维护「项目 × 机器」的位置表。
//
// 职责：
//   - project add：把 cwd 登记为本机位置；--target 时一并登记到那台机器
//   - project ls：列出位置，并显示每条的实际状态（登记与磁盘漂移时看得见）
//   - project rm：注销位置
//
// 边界：
//   - 不自己 ssh、不自己 clone：clone 由目标机上的 agentd 执行，用它自己的 git 凭据
//   - 不删磁盘上的仓库：rm 只删登记
//   - 不决定「项目在那台机器的哪个目录」：远程落点由那台机器的 repo_root 决定，
//     本机一个远程细节都不需要知道（spec §6.2）
package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/agentd"
	"github.com/xushixin/handoff/internal/client"
)

// projectAddPath 是 --path：目标机上已有的那份代码的路径（省略则让它自己 clone）。
var projectAddPath string

// localOriginURL 读当前目录仓库的 origin 地址；不是 git 仓库或没有 origin 时返回空串。
//
// 注意：取的是 **cwd** 的信息，因此 cwd 必须在你要登记的那个项目里。
func localOriginURL() string {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// localProjectRoot 返回 cwd 所属项目在本机的位置（已归并到主工作树）。
//
// 返回：
//   - 主工作树根目录的绝对路径
//   - 错误：cwd 不是 git 仓库时返回可读提示
//
// 为什么归并：位置表一个项目只允许一行，而本仓库有十几个 linked worktree
// （spec §5）。归并算法与 agentd 侧共用同一个实现，绝不在这里复制一份。
func localProjectRoot(ctx context.Context) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("取当前目录: %w", err)
	}
	return agentd.MainWorktreeRoot(ctx, cwd)
}

// projectCmd 是 project 子命令族的父命令。
var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "管理项目位置（登记到本机与开发机、列出、注销）",
}

// projectAddCmd 登记一个项目。
//
// 使用方式：
//
//	handoff project add [名字]                                       # 把 cwd 登记为本机位置
//	handoff project add [名字] --target devbox                       # 本机与 devbox 一起登记，devbox 自动 clone
//	handoff project add [名字] --target devbox --path /root/work/x   # 同上，但 devbox 上已有一份
var projectAddCmd = &cobra.Command{
	Use:   "add [名字]",
	Short: "把当前项目登记到本机（--target 时一并登记到那台开发机）",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := ""
		if len(args) == 1 {
			name = args[0]
		}
		origin := localOriginURL()
		if origin == "" {
			return fmt.Errorf("当前目录不是 git 仓库（或没有 origin）：项目身份由 origin 派生，请在项目目录内执行")
		}
		root, err := localProjectRoot(cmd.Context())
		if err != nil {
			return err
		}
		return registerProjectBothHops(cmd, origin, name, root, projectAddPath)
	},
}

// registerProjectBothHops 执行「本机 + 可选目标机」两跳登记。
//
// 参数：
//   - cmd: 用于取 context 与输出流
//   - origin: cwd 的 origin（项目身份来源）
//   - name: 人可读引用名（可空，由 agentd 从 origin 末段派生）
//   - localPath: 本机位置（cwd 的主工作树）
//   - remotePath: 目标机上已有的路径（可空，空则让那台机器自己 clone）
//
// 返回：
//   - 错误：任一跳失败即返回；**不回滚另一跳**（登记是幂等的，重跑即可）
//
// 注意：
//   - --target 的语义是「本机与那台机器**一起**登记」，不是「只登记那台机器」：
//     项目身份是从 cwd 算的，本机位置已知且免费，刻意不登它只会让本机项目树缺一行
//   - 本机永远不 clone（它已经有 cwd 这份了）；远程不给 path 时由它自己 clone
//   - 两跳的「成功」状态行走 cmd.ErrOrStderr()：dispatch 的自动登记路径调用本函数，
//     stdout 必须保持「第一行是任务 JSON」的既有契约（上层脚本按行解析），
//     任何额外输出都不能污染 stdout
func registerProjectBothHops(cmd *cobra.Command, origin, name, localPath, remotePath string) error {
	localAddr, localToken, err := LocalEndpoint()
	if err != nil {
		return err
	}
	local, err := client.New(localAddr, localToken).ProjectAdd(cmd.Context(), client.ProjectAddOpts{
		OriginURL: origin, Name: name, Path: localPath,
	})
	if err != nil {
		return fmt.Errorf("登记到本机: %w", err)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "本机 %s → %s\n", local.Name, local.Path)
	if targetName == "" {
		return nil
	}
	addr, token, err := TargetEndpoint()
	if err != nil {
		return err
	}
	if remotePath == "" {
		// 服务端可能 clone 也可能认领已存在的落点（spec §12），CLI 事前无法分辨，
		// 措辞必须两种结局都成立——写成「克隆」会在认领路径下成为假话。
		fmt.Fprintf(cmd.ErrOrStderr(), "正在让 %s 落地项目 %s（首次需要 clone，可能较慢）…\n", targetName, origin)
	}
	remote, err := client.New(addr, token).ProjectAdd(cmd.Context(), client.ProjectAddOpts{
		OriginURL: origin, Name: local.Name, Path: remotePath,
	})
	if err != nil {
		return fmt.Errorf("登记到 %s: %w", targetName, err)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s %s → %s\n", targetName, remote.Name, remote.Path)
	return nil
}

// projectLsCmd 列出位置。
var projectLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "列出机器上的项目位置（含实际状态）",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		locs, err := client.New(addr, token).ProjectList(cmd.Context())
		if err != nil {
			return err
		}
		if len(locs) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "（这台机器上还没有任何项目，在项目目录里执行 handoff project add）")
			return nil
		}
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "名字\t路径\t状态\tproject_id\torigin")
		for _, l := range locs {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", l.Name, l.Path, l.Status, l.ProjectID, l.OriginURL)
		}
		return tw.Flush()
	},
}

// projectRmCmd 注销一条位置。
var projectRmCmd = &cobra.Command{
	Use:   "rm <名字>",
	Short: "注销一条项目位置（只删登记，不删磁盘上的代码）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		if err := client.New(addr, token).ProjectRemove(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "已注销 %s（磁盘上的代码未动）\n", args[0])
		return nil
	},
}

func init() {
	projectAddCmd.Flags().StringVar(&projectAddPath, "path", "",
		"目标机上已有的那份代码的路径（仅与 --target 连用；省略则由那台机器 clone 到它的 repo_root/<名字>）")
	projectCmd.AddCommand(projectAddCmd, projectLsCmd, projectRmCmd)
	rootCmd.AddCommand(projectCmd)
}
