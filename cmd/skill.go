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
	"io/fs"
	"log/slog"
	"os"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/agy"
	"github.com/Xsxdot/handoff/internal/executor/claudecode"
	"github.com/Xsxdot/handoff/internal/executor/codex"
	"github.com/Xsxdot/handoff/internal/executor/grok"
	"github.com/Xsxdot/handoff/internal/executor/opencode"
	"github.com/Xsxdot/handoff/internal/skill"
	"github.com/spf13/cobra"
)

// skillContent 是 main 包用 go:embed 注入的 SKILL.md 全文。
//
// 为什么用注入而不是在本包 embed：go:embed 不能引用父目录，而 skills/
// 在仓库根。为什么不在本包放一份拷贝：那份拷贝会和二进制一样漂移。
var skillContent string

// pluginContent 是 main 包注入的 OpenCode monitor 插件源码。
var pluginContent string

// SetSkillContent 由 main 在启动时注入内嵌的 skill 全文。
func SetSkillContent(s string) { skillContent = s }

// SetPluginContent 由 main 注入 OpenCode monitor 插件全文。
func SetPluginContent(s string) { pluginContent = s }

// claudeModFS 是 main 包注入的 Claude Code 唤醒 mod 文件树（根即插件目录，B434）。
//
// 为什么是 fs.FS 而不是字符串：mod 是三件一目录，安装要保住相对路径；
// 为什么注入：go:embed 不能引用父目录，与 skillContent 同理。
var claudeModFS fs.FS

// SetClaudeModFS 由 main 注入内嵌的 Claude Code 唤醒 mod（契约 §3.13）。nil 表示构建漏了注入，
// install 会如实报 skipped，不会装出半截 mod。
func SetClaudeModFS(f fs.FS) { claudeModFS = f }

func defaultSkillProviders(log *slog.Logger) []executor.Skills {
	return []executor.Skills{
		claudecode.New(log),
		codex.New(log),
		opencode.New(log),
		grok.New(log),
		agy.New(log),
	}
}

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "查看或安装给 AI 协调者的 handoff skill",
	Long: "不带参数报告各落点是否与当前二进制内嵌的 skill 一致。\n" +
		"skill install 把内嵌版本装到本机各家 agent（Claude Code / codex / opencode / grok），\n" +
		"并在已装 OpenCode 时写入 monitor 插件；已装 Claude Code 时写入桌面端唤醒 mod\n" +
		"（~/.handoff/claude-mods/handoff-desktop-wake，并登记到 ~/.claude/settings.json 的 CLAUDE_CODE_PLUGIN_DIRS）。\n" +
		"安装与升级会自动调用它，正常不需要手工跑。",
	RunE: func(cmd *cobra.Command, _ []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("取 home 目录: %w", err)
		}
		// Status 的 err 恒为 nil：单点读取失败已落到该 Site 的 Note 上，
		// 报告里如实点名，不让一处坏掉的落点吃掉整份报告
		sites, _ := skill.Status(skillContent, home, defaultSkillProviders(slog.Default()))
		sites = append(sites, skill.PluginStatus(pluginContent, home))
		sites = append(sites, skill.ClaudeModStatus(claudeModFS, home))
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
		sites, err := skill.Install(skillContent, home, defaultSkillProviders(slog.Default()))
		if err != nil {
			return err
		}
		sites = append(sites, skill.InstallPlugin(pluginContent, home))
		sites = append(sites, skill.InstallClaudeMod(claudeModFS, home))
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
