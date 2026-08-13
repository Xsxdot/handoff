// handoff 程序入口：把命令执行交给 cmd 包。
package main

import (
	_ "embed"
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

func main() {
	cmd.SetSkillContent(skillContent)
	// Execute 出错必须带非零退出码退出：cobra 已把错误打到 stderr，
	// 上层脚本（e2e 验收、CI）依赖退出码判断命令成败，静默吞错会让
	// 「所有 CLI 失败都表现为成功」——错误信息还在但退出码恒 0。
	// 退出码经 cmd.ExitCode 换算：无人值守场景要能区分「等满了时限」（124）
	// 与「配置/鉴权失败」（1），两者的处置完全不同
	if err := cmd.Execute(); err != nil {
		os.Exit(cmd.ExitCode(err))
	}
}
