// headtail.go —— 帧字段的头尾截断。
//
// 职责：把超长的工具入参/输出压成「头 + 省略标记 + 尾」，并报告原始长度
// 边界：纯函数，不打日志、不做 I/O；不认识帧结构，只处理字符串
//
// 为什么头尾都留而不是只留头：报错信息与 stack trace 几乎总在输出**尾部**，
// 纯头部截断会刚好切掉最有用的那一段——那正是审核者要看的东西。
package turn

import (
	"strings"
	"unicode/utf8"

	"github.com/xushixin/handoff/internal/executor"
)

const (
	// FrameFieldHead 是帧字段保留的头部字节预算。
	FrameFieldHead = 4 << 10
	// FrameFieldTail 是帧字段保留的尾部字节预算。
	FrameFieldTail = 4 << 10
)

// HeadTail 把 s 压成「头 head 字节 + 截断标记 + 尾 tail 字节」。
//
// 参数：
//   - s:    原始字符串
//   - head: 头部保留的字节预算（按 rune 边界向内收缩，不会切出半个字符）
//   - tail: 尾部保留的字节预算（同上）
//
// 返回：
//   - out:       结果字符串；未截断时与 s 相同
//   - truncated: 是否确实发生了截断
//   - orig:      s 的原始字节数（无论是否截断都返回真实值）
//
// 注意：head+tail 已能覆盖整串时原样返回——否则会出现「截断后比原文还长」
// （多了一个标记）这种荒唐结果。
func HeadTail(s string, head, tail int) (out string, truncated bool, orig int64) {
	orig = int64(len(s))
	if len(s) <= head+tail {
		return s, false, orig
	}
	h := s[:sliceToRuneBoundary(s, head)]
	t := s[len(s)-tailToRuneBoundary(s, tail):]
	var b strings.Builder
	b.WriteString(h)
	b.WriteString(executor.TruncationMarker)
	b.WriteString(t)
	return b.String(), true, orig
}

// sliceToRuneBoundary 返回 <= n 的最大下标，且该下标是一个 rune 的起点。
//
// 为什么要向内收缩而不是直接切：切在 UTF-8 码点中间会产出 U+FFFD 替换字符，
// 前端渲染出来是一串乱码方块，而且 JSON 编码后还会把这个损坏悄悄固化下来。
func sliceToRuneBoundary(s string, n int) int {
	if n >= len(s) {
		return len(s)
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return n
}

// tailToRuneBoundary 返回 <= n 的最大尾部长度，且切点是一个 rune 的起点。
func tailToRuneBoundary(s string, n int) int {
	if n >= len(s) {
		return len(s)
	}
	for n > 0 && !utf8.RuneStart(s[len(s)-n]) {
		n--
	}
	return n
}
