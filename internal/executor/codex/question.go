// question.go —— codex 原生提问通道 item/tool/requestUserInput 的翻译。
//
// 职责：
//   - 解析提问报文，渲染成交给审核者的问题全文
//   - 构造必须立即回发的应答体
//
// 边界：
//   - 不决定「回合要不要结束」：那是 adapter 回合收尾的事
//   - **不代传机密**：isSecret 的问题正文不进事件库
//
// 为什么必须立即应答而不是等审核者：回调跑在读循环 goroutine 上，等审核者会卡死
// 整条连接；而不应答会让 codex 侧的回合永久挂起。grok 那边这条通道翻过两次车
// （应答形态错被判工具失败、兜底重复上报导致一次提问两张工单），此处逐条对症。
package codex

import (
	"encoding/json"
	"strings"
)

// handoffAnswerText 是回发给 codex 的固定答案。
//
// 内容必须是**对模型有效的指令**而不是占位符：告诉它问题已转交人类、按收尾协议
// 结束本回合。空答案或无意义答案会被 codex 判成工具失败（grok 的教训）。
const handoffAnswerText = "该问题已转交给人类审核者。请立即按 handoff 收尾协议结束本回合" +
	"（输出 HANDOFF_STATUS: ask 及问题正文），不要自行猜测答案继续执行。"

// userInputOption 是一个候选答案。
type userInputOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// userInputQuestion 是一条待答问题。
type userInputQuestion struct {
	ID       string            `json:"id"`
	Header   string            `json:"header"`
	Question string            `json:"question"`
	Options  []userInputOption `json:"options"`
	IsOther  bool              `json:"isOther"`
	IsSecret bool              `json:"isSecret"`
}

// parseUserInput 解析提问报文。
//
// 返回：itemId、问题列表与 true；报文非法或没有任何问题时返回 false
// （没有问题就没什么可转交的，但调用方仍须应答，见 adapter 的分支）。
func parseUserInput(params json.RawMessage) (string, []userInputQuestion, bool) {
	var p struct {
		ItemID    string              `json:"itemId"`
		Questions []userInputQuestion `json:"questions"`
	}
	if err := json.Unmarshal(params, &p); err != nil || len(p.Questions) == 0 {
		return "", nil, false
	}
	return p.ItemID, p.Questions, true
}

// userInputText 把问题列表渲染成交给审核者的正文。
//
// 注意：isSecret 的问题**只给标题不给正文**——凭据不经 handoff 的事件库中转，
// 事件是要落盘的。
func userInputText(qs []userInputQuestion) string {
	var b strings.Builder
	b.WriteString("【模型提问】\n")
	for _, q := range qs {
		if q.Header != "" {
			b.WriteString("■ " + q.Header + "\n")
		}
		if q.IsSecret {
			b.WriteString("  （codex 索要一个机密值，handoff 不代传机密；" +
				"若确需提供，请由人直接在 executor 机处理）\n")
			continue
		}
		if q.Question != "" {
			b.WriteString("  " + q.Question + "\n")
		}
		for _, o := range q.Options {
			line := "    - " + o.Label
			if o.Description != "" {
				line += "：" + o.Description
			}
			b.WriteString(line + "\n")
		}
		if q.IsOther {
			b.WriteString("    - （也可给出选项之外的答案）\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// userInputReply 构造应答体：{"answers":{"<qid>":{"answers":["…"]}}}。
//
// 注意：**每个问题都要有非空答案**——漏一个或给空串会被 codex 判成工具失败，
// 模型随后可能反复重试同一次提问。
func userInputReply(qs []userInputQuestion) map[string]any {
	answers := make(map[string]any, len(qs))
	for i, q := range qs {
		id := q.ID
		if id == "" {
			// 没有 id 的问题也要占一个键，否则应答与问题数量对不上
			id = "q" + itoaSmall(i)
		}
		answers[id] = map[string]any{"answers": []string{handoffAnswerText}}
	}
	return map[string]any{"answers": answers}
}

// itoaSmall 是小整数转字符串（避免为一个下标引入 strconv 的心智负担）。
func itoaSmall(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}
