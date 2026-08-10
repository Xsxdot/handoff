// 本文件实现 handoff repo 子命令族：把一个项目显式落到某台执行机上，
// 并维护「执行机 × 仓库」的登记，使日常 dispatch 不必再写仓库路径。
//
// 职责：
//   - repo add：登记执行机上已有的克隆，或让 agentd 克隆一份再登记
//   - repo ls：列出登记，并显示每条的实际状态（登记与磁盘漂移时看得见）
//   - repo rm：注销登记
//
// 边界：
//   - 不自己 ssh、不自己 clone：克隆由执行机上的 agentd 执行，用它自己的 git 凭据
//   - 不删磁盘上的仓库：rm 只删登记
//   - 不做解析：dispatch 时 --repo 怎么解释是 agentd 的事
package cmd

import (
	"fmt"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
)

var (
	repoAddPath  string
	repoAddURL   string
	repoAddClone bool
)

// localOriginURL 读当前目录仓库的 origin 地址；不是 git 仓库或没有 origin 时返回空串。
//
// 与 localHeadCommit 同源同 caveat：取的是 **cwd** 的信息，因此 cwd 必须是
// 你要落地的那个仓库。
func localOriginURL() string {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// validateRepoAddFlags 校验 repo add 的两种形态互斥且齐备。
//
// 返回：
//   - 错误：两种形态都没给时返回可读提示（本地拦下，不浪费一次往返）
func validateRepoAddFlags() error {
	if !repoAddClone && repoAddPath == "" {
		return fmt.Errorf("需要二选一：--path <执行机上已有仓库的路径>，或 --clone（让 agentd 克隆一份）")
	}
	return nil
}

// repoCmd 是 repo 子命令族的父命令。
var repoCmd = &cobra.Command{
	Use:   "repo",
	Short: "管理执行机上的仓库登记（落地项目、列出、注销）",
}

// repoAddCmd 登记一个仓库。
//
// 使用方式：
//
//	handoff repo add [名字] --path /root/work/handoff --target devbox
//	handoff repo add [名字] --clone [--url <URL>] [--path <落点>] --target devbox
var repoAddCmd = &cobra.Command{
	Use:   "add [名字]",
	Short: "把一个仓库登记到执行机（可让 agentd 克隆一份）",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateRepoAddFlags(); err != nil {
			return err
		}
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		name := ""
		if len(args) == 1 {
			name = args[0]
		}
		url := repoAddURL
		if repoAddClone && url == "" {
			// 与 dispatch 取基线同源：默认拿 cwd 的 origin
			url = localOriginURL()
			if url == "" {
				return fmt.Errorf("--clone 需要仓库 URL：当前目录不是 git 仓库（或没有 origin），请用 --url 指定")
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "克隆源取自当前目录的 origin: %s\n", url)
		}
		repo, err := client.New(addr, token).RepoAdd(cmd.Context(), client.RepoAddOpts{
			Name: name, Path: repoAddPath, URL: url, Clone: repoAddClone,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "已登记 %s → %s\n", repo.Name, repo.Path)
		fmt.Fprintf(cmd.ErrOrStderr(), "此后可用 --repo %s 派发，或在该仓库目录里直接省略 --repo\n", repo.Name)
		return nil
	},
}

// repoLsCmd 列出登记。
var repoLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "列出执行机上的仓库登记（含实际状态）",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		repos, err := client.New(addr, token).RepoList(cmd.Context())
		if err != nil {
			return err
		}
		if len(repos) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "（该执行机上还没有任何仓库登记，用 handoff repo add 落地一个）")
			return nil
		}
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "名字\t路径\t状态\torigin")
		for _, r := range repos {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Name, r.Path, r.Status, r.OriginURL)
		}
		return tw.Flush()
	},
}

// repoRmCmd 注销一条登记。
var repoRmCmd = &cobra.Command{
	Use:   "rm <名字>",
	Short: "注销一条仓库登记（只删登记，不删磁盘上的仓库）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		if err := client.New(addr, token).RepoRemove(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "已注销登记 %s（磁盘上的仓库未动）\n", args[0])
		return nil
	},
}

func init() {
	repoAddCmd.Flags().StringVar(&repoAddPath, "path", "",
		"执行机上的仓库路径；--clone 时为落点（省略则用执行机配置的 repo_root/<名字>）")
	repoAddCmd.Flags().StringVar(&repoAddURL, "url", "",
		"克隆源 URL（仅与 --clone 连用；省略则取当前目录的 origin）")
	repoAddCmd.Flags().BoolVar(&repoAddClone, "clone", false,
		"让 agentd 在执行机上克隆一份，而不是登记已有的克隆")
	repoCmd.AddCommand(repoAddCmd, repoLsCmd, repoRmCmd)
	rootCmd.AddCommand(repoCmd)
}
