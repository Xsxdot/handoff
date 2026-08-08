// handoff 程序入口：把命令执行交给 cmd 包。
package main

import (
	"os"

	"github.com/xushixin/handoff/cmd"
)

func main() {
	// Execute 出错必须带非零退出码退出：cobra 已把错误打到 stderr，
	// 上层脚本（e2e 验收、CI）依赖退出码判断命令成败，静默吞错会让
	// 「所有 CLI 失败都表现为成功」——错误信息还在但退出码恒 0。
	// 退出码经 cmd.ExitCode 换算：无人值守场景要能区分「等满了时限」（124）
	// 与「配置/鉴权失败」（1），两者的处置完全不同
	if err := cmd.Execute(); err != nil {
		os.Exit(cmd.ExitCode(err))
	}
}
