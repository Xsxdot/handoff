// Package shellq 提供单引号 shell 字面量包装，供把参数安全拼进 shell 命令串
// （osascript do script、tmux 命令串等）。
//
// 边界：
//   - 纯函数，无 I/O；不执行命令
//   - 与 opencode/proc.go 的 shellQuote 同实现——两处引用避免复制漂移
package shellq

import "strings"

// Quote 把字符串包成单引号 shell 字面量（内含单引号转义为 '\”）。
//
// 参数/密码/路径可能含引号或空白，不转义会改变脚本语义或让命令被拆错；
// 转义后整个串作为 shell 的一个单词参与拼接。
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
