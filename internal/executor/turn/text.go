// text.go —— 回合文本的截断工具。
//
// 职责：按 rune 截断，并在确实发生截断时追加显式标记
// 边界：纯函数，不打日志、不做 I/O；截断标记的语义契约在 executor 包

package turn

import "github.com/Xsxdot/handoff/internal/executor"

// QuestionTextLimit 是交给协调者的回合文本上限。兜底分类会把整个回合原文当
// question 发出，一个失控的长回合会直接灌进工单行与协调者终端；全文始终在
// 任务目录的 render.log 里，截断不丢证据。
//
// 为什么导出：opencode 的 regression_group_a_test.go 直接断言这个上限，
// 搬包后它得能从 turn 引到同一个值——两处各写一个 8000 就会悄悄漂移。
const QuestionTextLimit = 8000

// FinalTextLimit 是回合末正文送入 completed payload 的尾部窗口（按 rune 计）。
// 裁决块按模板契约位于正文尾部，保留尾部而不是头部，才能在长回合受限时仍
// 把裁决交给下游解析器；正文完整证据仍保留在 render.log。
const FinalTextLimit = 16 << 10

// FinalText 返回用于终态 Result 的正文窗口。
//
// 参数：text 是 adapter 收集到的完整回合正文。
// 返回：不超过 FinalTextLimit 个 rune 的正文尾部；短正文原样返回。
// 注意：这是传输边界的有界投影，不应替代 render.log 作为完整取证来源。
func FinalText(text string) string {
	return TailRunes(text, FinalTextLimit)
}

// TruncateMarked 按 rune 截断到 n，确实截断时追加 executor.TruncationMarker。
//
// 为什么必须带标记：上层据此 fail-closed——权限文本含标记说明裁决者看到的是
// 不完整命令，危险片段可能落在截断之外，黑名单与廉价模型都不可信。
func TruncateMarked(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + executor.TruncationMarker
}

// TruncateRunes 按 rune 截断到 n，不加任何标记。
func TruncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// TailRunes 返回末尾 n 个 rune；不足 n 时原样返回。
func TailRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

// ClampQuestion 把兜底分类产出的整段回合文本收敛到 QuestionTextLimit，
// 超出时追加尾缀指明全文去处。
//
// 为什么**不能**复用 TruncateMarked：两者的「全文在哪」不同，尾缀因此必须不同。
//   - TruncateMarked 用于 permission 文本，全文在工单里（B6 契约：工单存全文、
//     事件截断），协调者 `handoff show` 就能拿到，`…（已截断）` 足够；
//   - 本函数用于 question 文本，全文**不在工单里**，只在任务目录的 render.log。
//     不指路 = 协调者拿到半截文本且不知道去哪找全文，证据链断掉。
//
// 这段尾缀是逐字从 opencode 现有实现搬来的，opencode 的
// regression_group_a_test.go 断言 `strings.Contains(ev.Text, "render.log")`，
// 改字面量即回归。
func ClampQuestion(text string) string {
	if len([]rune(text)) <= QuestionTextLimit {
		return text
	}
	return TruncateRunes(text, QuestionTextLimit) +
		"\n\n…（回合文本过长已截断，完整内容见任务目录 render.log）"
}
