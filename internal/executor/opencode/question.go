// Package opencode 的提问通路纯逻辑。
//
// 职责：
//   - renderQuestionTicket：把 opencode 的 question 请求渲染成审核者可读的工单文本
//
// 边界：
//   - 全部是纯函数：不碰 runState、不发 HTTP、不打日志、不读时钟
//   - 不认识 SSE 事件结构（解析在 adapter.go 的 mapQuestionAsked 里做）
//   - 不做截断（由调用方用 turn.ClampQuestion 收口）
package opencode

import (
	"fmt"
	"strings"
)

// renderQuestionTicket 把一组问题渲染成一张工单的文本。
//
// 参数：qs 为 opencode question 请求里的问题数组（顺序即应答顺序）
//
// 返回：多段文本，每问一段，选项按 `<问号>.<选项号>` 编号
//
// 注意：
//   - 编号跨问连续编排（1.1/1.2/2.1），审核者据此作答，parseQuestionAnswers
//     按同一套编号回读——两者是同一契约的两半，改一个必须改另一个
//   - 无选项的问题不编号，提示直接作答：opencode 允许问题只有正文
func renderQuestionTicket(qs []QuestionInfo) string {
	var b strings.Builder
	b.WriteString("executor 需要你决策：\n")
	for i, q := range qs {
		fmt.Fprintf(&b, "\n问题 %d", i+1)
		if h := strings.TrimSpace(q.Header); h != "" {
			fmt.Fprintf(&b, "（%s）", h)
		}
		b.WriteString("：")
		b.WriteString(strings.TrimSpace(q.Question))
		b.WriteString("\n")
		if len(q.Options) == 0 {
			b.WriteString("  （无候选项，直接作答）\n")
			continue
		}
		for j, o := range q.Options {
			fmt.Fprintf(&b, "  %d.%d %s", i+1, j+1, o.Label)
			if d := strings.TrimSpace(o.Description); d != "" {
				b.WriteString(" — " + d)
			}
			b.WriteString("\n")
		}
		var marks []string
		if q.Multiple {
			marks = append(marks, "可多选（逗号分隔）")
		}
		if q.Custom {
			marks = append(marks, "可自定义（直接写答案）")
		}
		if len(marks) > 0 {
			b.WriteString("  [" + strings.Join(marks, "；") + "]\n")
		}
	}
	b.WriteString("\n用 handoff reply --answer 作答，填编号（如 1.2）或选项原文；" +
		"多问按顺序用分号分隔（如 \"1.2; 2.1\"）。")
	return b.String()
}
