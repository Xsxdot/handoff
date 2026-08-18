// redirect.go —— shell 输出重定向落点的提取。
//
// 职责：
//   - 从命令原文里摘出「输出会落到哪个文件」，供 judgeBash 做范围判定
//   - 展开 ~ 前缀；识别 /dev/* 这类不构成文件写入的丢弃落点
//
// 边界：
//   - 不做完整 shell 解析：不展开变量、不处理 heredoc、不解析子 shell、不跟 cd
//   - 不判断路径是否越界：范围判定在 path.go，本文件只负责「落点是什么」
//
// 为什么不能先过 StripQuoted：落点常常被引号包住（echo x > "/etc/foo"），
// 剥完引号落点就没了。这里要的恰恰是引号里的内容，所以扫描的是命令原文，
// 自己带引号状态机。
//
// 已知不覆盖（spec §4.3 明列的残余）：相对路径逃逸（> ../../x）能摘出来并交
// InScope 判，但 `cd /etc && echo x > passwd` 这种先换目录再相对写的形态摘到的
// 是 "passwd"，InScope 会按 workdir 拼接判成范围内——本轮不跟 cd。
package permgate

import (
	"os"
	"path/filepath"
	"strings"
)

// discardTargets 是不构成文件写入的重定向落点：写它们不改变文件系统。
//
// 不豁免的代价是实打实的：`go test ./... > /dev/null` 是高频写法，
// 每次都判越界就等于每次都升级人工。
var discardTargets = map[string]bool{
	"/dev/null":   true,
	"/dev/stdout": true,
	"/dev/stderr": true,
	"/dev/tty":    true,
}

// IsDiscardTarget 判断落点是否为 /dev 下的丢弃或终端设备。
//
// 参数：p 为落点路径（已展开 ~）
// 返回：是则 true，调用方应跳过对它的范围判定
func IsDiscardTarget(p string) bool {
	if discardTargets[p] {
		return true
	}
	return strings.HasPrefix(p, "/dev/fd/")
}

// RedirectTargets 从命令串里摘出全部输出重定向的落点。
//
// 参数：cmd 为命令原文（**不要**先过 StripQuoted，见文件头 why）
// 返回：落点路径切片，按出现顺序；没有则返回 nil
//
// 识别的形态：`>`、`>>`、`>|`、`n>`、`n>>`、`&>`、`&>>`
// 明确排除的形态：fd 复制与关闭（`2>&1`、`>&2`、`>&-`）——它们不写文件
//
// 注意：
//   - 引号内的 `>` 不算重定向（`echo "a > b"`、`grep "x->y"` 都不命中）
//   - 落点带引号时取引号内的内容，可以含空格
//   - `~` 与 `~/` 前缀展开为当前用户 home（why 见 expandTilde）
func RedirectTargets(cmd string) []string {
	var out []string
	rs := []rune(cmd)
	var quote rune
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote == 0 && (r == '\'' || r == '"'):
			quote = r
			continue
		case quote != 0 && r == quote:
			quote = 0
			continue
		case quote != 0:
			continue
		}
		if r != '>' {
			continue
		}
		j := i + 1
		// `>>` 与 `>|` 的第二个字符只是操作符的一部分，不是落点
		for j < len(rs) && (rs[j] == '>' || rs[j] == '|') {
			j++
		}
		// `>&` 后面跟数字或 `-` 是 fd 复制/关闭，不写文件；跟其他内容才是落点
		if j < len(rs) && rs[j] == '&' {
			k := j + 1
			if k < len(rs) && (rs[k] == '-' || (rs[k] >= '0' && rs[k] <= '9')) {
				i = k
				continue
			}
			j = k
		}
		for j < len(rs) && (rs[j] == ' ' || rs[j] == '\t') {
			j++
		}
		tgt, next := readTarget(rs, j)
		// 扫描位置直接跳到落点之后：落点内部的字符不该再被当成操作符看
		i = next - 1
		if tgt != "" {
			out = append(out, expandTilde(tgt))
		}
	}
	return out
}

// readTarget 从 rs[i] 起读一个落点词元。
//
// 参数：rs 为命令的 rune 切片，i 为起始下标（调用方已跳过空白）
// 返回：词元内容（去掉包裹引号）与下一个待扫描下标
func readTarget(rs []rune, i int) (string, int) {
	if i >= len(rs) {
		return "", i
	}
	if q := rs[i]; q == '\'' || q == '"' {
		var b strings.Builder
		j := i + 1
		for ; j < len(rs) && rs[j] != q; j++ {
			b.WriteRune(rs[j])
		}
		if j < len(rs) {
			j++ // 吃掉右引号
		}
		return b.String(), j
	}
	var b strings.Builder
	j := i
	for ; j < len(rs); j++ {
		switch rs[j] {
		case ' ', '\t', '\n', '|', ';', '&', '(', ')', '<', '>':
			return b.String(), j
		}
		b.WriteRune(rs[j])
	}
	return b.String(), j
}

// expandTilde 把 `~` 与 `~/xxx` 展开为当前用户 home。
//
// 参数：p 为原始落点
// 返回：展开后的路径；不以 ~ 开头、或取不到 home 时原样返回
//
// 为什么必须展开：filepath.Abs 不认识 `~`，`> ~/.zshrc` 会被 InScope 拼成
// <workdir>/~/.zshrc，判成范围内直接放行——那正是本条要拦的写入。
// 取不到 home 时原样返回是安全的：`~/.zshrc` 作为相对路径拼到 workdir 下，
// InScope 判在范围内，与展开前的行为一致，不会比现状更松。
func expandTilde(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}
