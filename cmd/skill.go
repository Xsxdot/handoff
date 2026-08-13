// 本文件实现 handoff skill：报告与同步内嵌 skill 的安装状态。
//
// 职责：
//   - handoff skill：逐落点报告是否与当前二进制一致
//   - handoff skill install：把内嵌内容装到本机各家 agent
//
// 边界：
//   - 不含安装逻辑本身（在 internal/skill）：本层只做参数、打印与退出码
//   - 不装到远端：skill 服务于协调者，协调者在本机
package cmd

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/Xsxdot/handoff/internal/skill"
	"github.com/spf13/cobra"
)

// skillContent 是 main 包用 go:embed 注入的 SKILL.md 全文。
//
// 为什么用注入而不是在本包 embed：go:embed 不能引用父目录，而 skills/
// 在仓库根。为什么不在本包放一份拷贝：那份拷贝会和二进制一样漂移。
var skillContent string

// SetSkillContent 由 main 在启动时注入内嵌的 skill 全文。
func SetSkillContent(s string) { skillContent = s }

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "查看或安装给 AI 协调者的 handoff skill",
	Long: "不带参数报告各落点是否与当前二进制内嵌的 skill 一致。\n" +
		"skill install 把内嵌版本装到本机各家 agent（Claude Code / codex / opencode / grok）。\n" +
		"安装与升级会自动调用它，正常不需要手工跑。",
	RunE: func(cmd *cobra.Command, _ []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("取 home 目录: %w", err)
		}
		// Status 的 err 恒为 nil：单点读取失败已落到该 Site 的 Note 上，
		// 报告里如实点名，不让一处坏掉的落点吃掉整份报告
		sites, _ := skill.Status(skillContent, home)
		out := cmd.OutOrStdout()
		for _, s := range sites {
			fmt.Fprintf(out, "%-8s %s%s\n", skillStateText(s.State), s.Path, noteSuffix(s))
		}
		if !skill.InSync(sites) {
			fmt.Fprintf(out, "处置     handoff skill install 重新同步\n")
		}
		return nil
	},
}

var skillInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "把内嵌的 skill 装到本机各家 agent",
	RunE: func(cmd *cobra.Command, _ []string) error {
		if skillContent == "" {
			// 不拦下就会静静装出一份空 SKILL.md，症状是「装成功了但 skill 是空的」
			return fmt.Errorf("内嵌 skill 为空：这个二进制的构建漏了 go:embed 注入，拒绝安装")
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("取 home 目录: %w", err)
		}
		sites, err := skill.Install(skillContent, home)
		if err != nil {
			return err
		}
		// 逐个落点数出结论：安装是个「部分成功」的操作，只把表打给人看，
		// 事后排查（比如「为什么这台机器的 codex 没有 skill」）就没有任何痕迹
		var installed, skipped int
		for _, s := range sites {
			if s.State == skill.StateSkipped {
				skipped++
				slog.Warn("skill 落点跳过", "path", s.Path, "reason", s.Note)
				continue
			}
			installed++
		}
		slog.Info("skill 安装完成", "home", home, "installed", installed, "skipped", skipped)
		out := cmd.OutOrStdout()
		for _, s := range sites {
			fmt.Fprintf(out, "%-8s %s%s\n", skillStateText(s.State), s.Path, noteSuffix(s))
		}
		return nil
	},
}

// skillStateText 把落点状态渲染成一个定宽中文标签。
func skillStateText(state string) string {
	switch state {
	case skill.StateInstalled:
		return "已安装"
	case skill.StateSkipped:
		return "已跳过"
	case skill.StateInSync:
		return "一致"
	case skill.StateStale:
		return "旧"
	default:
		return "未安装"
	}
}

// noteSuffix 把理由缀在落点后面。理由为空时不留一个空括号。
func noteSuffix(s skill.Site) string {
	if s.Note == "" {
		return ""
	}
	return "（" + s.Note + "）"
}

func init() {
	skillCmd.AddCommand(skillInstallCmd)
	rootCmd.AddCommand(skillCmd)
}
