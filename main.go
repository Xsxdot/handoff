// handoff 程序入口：把命令执行交给 cmd 包。
package main

import (
	"embed"
	"io/fs"
	"os"

	"github.com/Xsxdot/handoff/cmd"
)

// skillContent 是给 AI 协调者的 skill 全文，编译期嵌进二进制。
//
// 为什么必须在 main 包做：go:embed 不能引用父目录，而 cmd 在子目录里。
//
// 为什么内嵌而不是走 npm/独立文件：skill 版本 == 二进制版本，**结构上不可能
// 漂移**——而漂移正是要解决的病根（旧 skill 会按已经变了的规则主动误导协调者）。
// 用一条会漂移的分发通道去修漂移，自相矛盾（B59 spec D5）。
//
//go:embed skills/handoff/SKILL.md
var skillContent string

// pluginContent 是 OpenCode monitor 插件源码，与 skill 同版本内嵌，避免分发漂移。
//
//go:embed plugins/opencode-monitor/handoff-monitor.ts
var pluginContent string

// claudeModEmbed 是 Claude Code 桌面端唤醒 mod 的三件源码，与 skill 同版本内嵌（B434 契约 §3.13）。
//
// 为什么逐个列文件，而不是 `all:plugins/claude-desktop-wake`：引擎会往它加载的 mod 目录写
// .claude-plugin/types/（几百 KB 的 d.ts），开发者在仓内目录挂过 mod 后，`all:` 会把它们连同
// tests/ 一起卷进二进制——内嵌内容随构建机而变，status 就随构建机漂移。不带 `all:` 又会静默
// 丢掉 .claude-plugin/plugin.json（internal/webui/embed.go 同一个坑）。显式列出正好是 mod 本体。
// 往 mod 加文件须同步这里（TestB434ClaudeModEmbedMatchesRepoDir 会拦下遗漏）。
//
//go:embed plugins/claude-desktop-wake/.claude-plugin/plugin.json plugins/claude-desktop-wake/hooks/hooks.json plugins/claude-desktop-wake/hooks/register.ts
var claudeModEmbed embed.FS

// claudeModDir 是 mod 在仓内（也即内嵌树里）的目录。
const claudeModDir = "plugins/claude-desktop-wake"

// claudeModFS 返回以插件目录为根的内嵌 mod，相对路径与安装目录一一对应。
func claudeModFS() fs.FS {
	sub, err := fs.Sub(claudeModEmbed, claudeModDir)
	if err != nil {
		// fs.Sub 只在路径非法时出错，而 claudeModDir 是常量：走到这里就是代码被改坏了
		panic("内嵌 Claude 唤醒 mod 的根目录非法: " + err.Error())
	}
	return sub
}

func main() {
	cmd.SetSkillContent(skillContent)
	cmd.SetPluginContent(pluginContent)
	cmd.SetClaudeModFS(claudeModFS())
	// Execute 出错必须带非零退出码退出：cobra 已把错误打到 stderr，
	// 上层脚本（e2e 验收、CI）依赖退出码判断命令成败，静默吞错会让
	// 「所有 CLI 失败都表现为成功」——错误信息还在但退出码恒 0。
	// 退出码经 cmd.ExitCode 换算：无人值守场景要能区分「等满了时限」（124）
	// 与「配置/鉴权失败」（1），两者的处置完全不同
	if err := cmd.Execute(); err != nil {
		os.Exit(cmd.ExitCode(err))
	}
}
