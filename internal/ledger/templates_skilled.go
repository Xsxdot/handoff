// templates_skilled.go —— 熟练工定义（B452 S1）：派发模板上的接收者与执行形态。
//
// 职责：TemplateDef 新字段的取值常量、put 校验（Validate）与「熟练工定义」判定（IsSkilled）。
// 边界：只做定义层的静态判据；派发时的组装在 CLI（handoff dispatch --template），
// 卡派发（ledgerstep.ViaTemplate）用 IsSkilled 拒发熟练工定义。
package ledger

import (
	"errors"
	"fmt"
)

// 熟练工定义的形态与目标取值。
const (
	TemplateModeBranch   = "branch"
	TemplateModeReadonly = "readonly"
	// TemplateTargetHere 表示「落在调用方所在机」，只对 handoff dispatch --template 有意义；
	// 卡派发见到它即拒发，防止它流进 ResolveTarget 被当成一台叫 here 的远端机。
	TemplateTargetHere = "here"
)

// Validate 是 template put 的定义校验（plan Q10）。
//
// 规则：
//   - prompt 必填；
//   - mode ∈ {"", branch, readonly}，其他值拒绝并点名合法值；
//   - discipline 必填，只读形态（mode=readonly）例外——只读调查不跑实现/审阅纪律；
//   - executor 必填，receiver 非空时例外——执行器由载体决定，填了就当一致性断言；
//   - target 可为机器名或 here，不做取值校验（机器名在派发时由 agentd 判）。
//
// 返回：第一条不满足的规则；全部满足返回 nil。
func (d TemplateDef) Validate() error {
	if d.Prompt == "" {
		return errors.New("prompt 必填")
	}
	switch d.Mode {
	case "", TemplateModeBranch, TemplateModeReadonly:
	default:
		return fmt.Errorf("mode=%q 不合法，只能是 \"\"（缺省=分支形态）、%q 或 %q", d.Mode, TemplateModeBranch, TemplateModeReadonly)
	}
	if d.Discipline == "" && d.Mode != TemplateModeReadonly {
		return errors.New("discipline 必填（只读形态 mode=readonly 可空）")
	}
	if d.Executor == "" && d.Receiver == "" {
		return errors.New("executor 必填（定义了 receiver 时可空，由载体决定执行器）")
	}
	return nil
}

// IsSkilled 判定是否熟练工定义：receiver 或 mode 非空，或 target=here。
// 熟练工定义只能经 handoff dispatch --template 调用；卡派发据此拒发。
func (d TemplateDef) IsSkilled() bool {
	return d.Receiver != "" || d.Mode != "" || d.Target == TemplateTargetHere
}
