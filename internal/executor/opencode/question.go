// Package opencode 的提问通路纯逻辑。
//
// 职责：
//   - renderQuestionTicket：把 opencode 的 question 请求渲染成协调者可读的工单文本
//   - parseQuestionAnswers：把协调者的自由文本答复折算回 opencode 要的 answers
//
// 边界：
//   - 全部是纯函数：不碰 runState、不发 HTTP、不打日志、不读时钟
//   - 不认识 SSE 事件结构（解析在 adapter.go 的 mapQuestionAsked 里做）
//   - 不做截断（由调用方用 turn.ClampQuestion 收口）
package opencode

import (
	"fmt"
	"strconv"
	"strings"
)

// renderQuestionTicket 把一组问题渲染成一张工单的文本。
//
// 参数：qs 为 opencode question 请求里的问题数组（顺序即应答顺序）
//
// 返回：多段文本，每问一段，选项按 `<问号>.<选项号>` 编号
//
// 注意：
//   - 编号跨问连续编排（1.1/1.2/2.1），协调者据此作答，parseQuestionAnswers
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

// parseQuestionAnswers 把协调者的自由文本答复折算成 opencode 要的 answers。
//
// 参数：
//   - qs: 本次请求的问题数组（顺序即 answers 的顺序）
//   - reply: 协调者 `handoff reply --answer` 的原文
//
// 返回：
//   - answers: 按问题顺序排列，每项是该问选中的 label 数组
//   - err: 无法折算（答数不匹配 / 某问答不上且不接受自定义）；此时调用方
//     必须重发工单，**不得**猜一个最接近的选项
//
// 注意：
//   - 分级匹配：编号 `问.选`（每段也接受裸选项号——该段的第 N 个选项）→
//     label 原文（TrimSpace + 大小写归一后精确匹配）→ 该问 Custom 时原文透传
//   - 多问用分号分隔，多选用逗号分隔；两级分隔符不重叠，故可先分号后逗号
//   - 猜错一个选项的代价是模型按错误前提继续干活，重问的代价只是协调者多按
//     一次——错误方向必须选后者（与 B6「误升级好过漏放行」同一取舍）
func parseQuestionAnswers(qs []QuestionInfo, reply string) ([][]string, error) {
	if len(qs) == 0 {
		return nil, fmt.Errorf("本次请求没有问题，无法折算答复")
	}
	segs := splitAnswerSegments(reply, len(qs))
	if len(segs) != len(qs) {
		return nil, fmt.Errorf("有 %d 道问题但只解析出 %d 段答复，请按 \"1.2; 2.1\" 的形式按顺序作答",
			len(qs), len(segs))
	}
	answers := make([][]string, len(qs))
	for i, q := range qs {
		seg := strings.TrimSpace(segs[i])
		if seg == "" {
			return nil, fmt.Errorf("问题 %d 没有对应的答复", i+1)
		}
		var picked []string
		tokens := []string{seg}
		if q.Multiple {
			tokens = splitAndTrim(seg, ",")
		}
		for _, tok := range tokens {
			label, ok := matchOption(q, i, tok)
			if !ok {
				if !q.Custom {
					return nil, fmt.Errorf("问题 %d 的答复 %q 既不是编号也不是选项原文，且该问不接受自定义答案；请填编号（如 %d.1）或选项原文",
						i+1, tok, i+1)
				}
				label = tok // custom：原文透传，服务端若拒绝由调用方降级重问
			}
			picked = append(picked, label)
		}
		answers[i] = picked
	}
	return answers, nil
}

// splitAnswerSegments 把答复切成「每问一段」。
//
// 单问时整段就是一段（分号可能是答案本身的一部分，不能切）；多问时按分号切。
func splitAnswerSegments(reply string, questionCount int) []string {
	if questionCount == 1 {
		return []string{strings.TrimSpace(reply)}
	}
	return splitAndTrim(reply, ";")
}

// splitAndTrim 按 sep 切分并去掉每段首尾空白，丢弃空段。
//
// 中文全角分隔符（；、）一并接受：协调者在中文输入法下很容易打出全角，
// 因此而重问是纯粹的摩擦。
func splitAndTrim(s, sep string) []string {
	switch sep {
	case ";":
		s = strings.ReplaceAll(s, "；", ";")
	case ",":
		s = strings.ReplaceAll(s, "，", ",")
	}
	var out []string
	for _, p := range strings.Split(s, sep) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// matchOption 把一个答复 token 折算成选项 label。
//
// 参数：
//   - q: 该问
//   - idx: 该问的下标（0 起），用于校验 `问.选` 里的问号
//   - tok: 已 TrimSpace 的单个 token
//
// 返回：命中的 label 与是否命中；未命中时由调用方按 Custom 决定透传或重问
func matchOption(q QuestionInfo, idx int, tok string) (string, bool) {
	// 一级：编号。`问.选`（1.2）或单问时的裸选项号（2）
	qn, on, ok := parseOptionNumber(tok)
	if ok && (qn == 0 || qn == idx+1) && on >= 1 && on <= len(q.Options) {
		return q.Options[on-1].Label, true
	}
	// 二级：label 原文，归一化后精确匹配。归一化只做 TrimSpace + 大小写折叠，
	// 不做模糊匹配——模糊匹配会在选项相近时静默选错，那正是重问要防的
	norm := strings.ToLower(strings.TrimSpace(tok))
	for _, o := range q.Options {
		if strings.ToLower(strings.TrimSpace(o.Label)) == norm {
			return o.Label, true
		}
	}
	return "", false
}

// parseOptionNumber 解析 "1.2"（问号.选项号）或 "2"（裸选项号，问号返回 0）。
//
// 返回 ok=false 表示 tok 不是编号形态（含小数点但两侧非数字、或整体非数字）。
func parseOptionNumber(tok string) (questionNo, optionNo int, ok bool) {
	if i := strings.Index(tok, "."); i >= 0 {
		q, err1 := strconv.Atoi(strings.TrimSpace(tok[:i]))
		o, err2 := strconv.Atoi(strings.TrimSpace(tok[i+1:]))
		if err1 != nil || err2 != nil {
			return 0, 0, false
		}
		return q, o, true
	}
	o, err := strconv.Atoi(tok)
	if err != nil {
		return 0, 0, false
	}
	return 0, o, true
}
