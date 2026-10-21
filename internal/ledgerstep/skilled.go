// skilled.go —— 模板正文渲染（B452 S2）：卡派发与无卡（熟练工）派发共用的占位符替换。
//
// 职责：RenderTemplatePlaceholders 是模板正文三占位符的唯一替换点；RenderCardlessPrompt 是
// handoff dispatch --template 的提示词组装（无卡：占位符替换为空串，再接「## 本次任务」一节）。
// 边界：只做字符串组装，不读账本、不判定义合法性（定义校验在 ledger.TemplateDef.Validate）。
package ledgerstep

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// CardlessTaskHeading 是无卡渲染时任务文本所在小节的标题行。
const CardlessTaskHeading = "## 本次任务"

// RenderTemplatePlaceholders 替换模板正文里的 {{TITLE}}/{{CARD}}/{{ACCEPT}}。
//
// 参数：prompt 模板正文；card 卡 id；title 卡标题；accept 验收判据（调用方已按 omit 规则换算好）。
// 返回：替换后的正文。未出现的占位符不影响结果。
func RenderTemplatePlaceholders(prompt, card, title, accept string) string {
	return strings.NewReplacer(
		"{{TITLE}}", title,
		"{{CARD}}", card,
		"{{ACCEPT}}", accept,
	).Replace(prompt)
}

// RenderCardlessPrompt 组装无卡派发的提示词：正文占位符替换为空串 + 空行 + 「## 本次任务」+ 空行 + 任务文本。
//
// 参数：prompt 模板正文；task 调用方给的本次任务文本（原样放入，不做裁剪）。
func RenderCardlessPrompt(prompt, task string) string {
	return RenderTemplatePlaceholders(prompt, "", "", "") + "\n\n" + CardlessTaskHeading + "\n\n" + task
}

// rejectSkilledForCard 是卡派发对熟练工定义的拒用守卫（B452 Q10），ViaTemplate 与 PreflightDiscipline
// 共用，保证两处报同一句话。
//
// why：熟练工定义（receiver / mode 非空，或 target=here）只能经 handoff dispatch --template 调用。
// 放它进卡派发，here 会流进目标机解析被当成一台叫 here 的远端机，mode 会被静默忽略
// （卡派发的 wire 不带 mode，只读形态退化成分支形态在主仓切分支）。
//
// 参数：tpl 已取出的模板；cardID 仅进日志，可空（前置解析时没有卡上下文）。
func rejectSkilledForCard(tpl ledger.Template, cardID string) error {
	if !tpl.Def.IsSkilled() {
		return nil
	}
	slog.Default().Warn("卡派发拒用熟练工定义", "card", cardID, "template", tpl.Name, "version", tpl.Version,
		"receiver", tpl.Def.Receiver, "mode", tpl.Def.Mode, "target", tpl.Def.Target)
	return fmt.Errorf("模板 %s 是熟练工定义（receiver=%q mode=%q target=%q），只能经 handoff dispatch --template 调用，不能用于卡派发",
		tpl.Name, tpl.Def.Receiver, tpl.Def.Mode, tpl.Def.Target)
}
