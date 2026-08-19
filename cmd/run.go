// 本文件实现 handoff run 子命令：在任务仓库远程执行审阅命令（跑测试/lint）。
//
// 职责：
//   - 按参数个数分档拼接命令行（单参数=shell 原文透传，多参数=逐个转义），
//     交给 agentd 执行（sh -c，10min 超时），合并输出原文打印；
//     非零退出码以错误返回（cobra 打印到 stderr），输出已先行打印
//
// 边界：
//   - 只透传命令，不解释输出语义；命令执行于任务仓库，由 agentd 限时回收
package cmd

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

// shellSafe 是无需引号即可安全交给 sh 的字符集：字母数字与一组不参与 shell
// 解释的标点。取值与 POSIX shell 的元字符集互补——不在这里的一律引住。
var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellQuote 把一个参数转成可安全交给 sh 的形态：安全字符原样返回，
// 其余用单引号包住，内嵌单引号按 POSIX 惯例拆成三段转义序列。
func shellQuote(s string) string {
	if s != "" && shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellJoin 把 argv 拼成一条可安全交给远端 sh -c 的命令行。
//
// 两档行为：
//   - **只有一个参数**：原样返回。用户给的就是一条 shell 命令原文
//     （`handoff run T1 "cd sub && go test ./..."`），本来就指望 sh -c 解析它。
//     对它转义会把整条命令当成一个带空格的命令名，报 command not found——
//     这是一条**回归防线**，改动前请先读 cmd/run_test.go 的第一个用例。
//   - **多个参数**：逐个 shellQuote 后以空格连接。用户给的是 argv，本地 shell
//     已完成分词与去引号。
//
// 为什么需要它（B66）：命令要穿两层 shell。本地 shell 已消费掉一层引号，
// 直接 strings.Join 重拼后远端 sh -c 会按新的词边界再解析一次——
// `grep -rn 'foo bar' .` 到远端就成了三个参数。静默失真，不报错，
// 而审阅取证正是「读了就信」的场景。
func shellJoin(args []string) string {
	if len(args) == 1 {
		return args[0]
	}
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}

// runCmd 在任务仓库执行一条审阅命令并输出合并结果。
//
// 使用方式：handoff run <task> <命令...>（如 handoff run T1 go test ./...）
var runCmd = &cobra.Command{
	Use:   "run <task> <命令...>",
	Short: "在任务仓库执行审阅命令（跑测试/lint）",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, cleanup, err := newTargetClient()
		if err != nil {
			return err
		}
		defer cleanup()
		cmdline := shellJoin(args[1:])
		fmt.Fprintf(cmd.ErrOrStderr(), "远端将执行: %s\n", cmdline)
		stdout, code, err := c.Run(cmd.Context(), args[0], cmdline)
		if err != nil {
			return err
		}
		fmt.Fprint(cmd.OutOrStdout(), stdout)
		if code != 0 {
			return fmt.Errorf("命令退出码 %d（输出已打印）", code)
		}
		return nil
	},
}

func init() {
	// 关闭 flag 穿插解析（P1-13）：`handoff run T1 go test -v ./...` 中任务名后的
	// -v/-race/-run 是审阅命令自身的参数，必须原样进入 args[1:]；cobra 默认的
	// Interspersed 会把它们当 handoff 的未知 flag 直接报错，协调者最主要的验证
	// 动作不可用。SetInterspersed(false) 让解析在首个位置参数（任务名）处停止，
	// 之后全部按位置参数透传；--agentd/--target 等 handoff 自有 flag 需写在任务名之前
	runCmd.Flags().SetInterspersed(false)
	rootCmd.AddCommand(runCmd)
}
