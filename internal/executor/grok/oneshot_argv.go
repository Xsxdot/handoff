// oneshot_argv.go —— grok 一次性调用的 argv 编码（B233.2）。
//
// 职责：按调用方 Limits 编码 grok CLI argv。Effort 空 = 不写 --effort。
//
// 边界：
//   - 不执行命令
//   - 不把 low effort 当成 grok 一次性的默认；那是审批调用方的策略
//   - --effort 必须排在 -p 之前（-p 是取值参数）
package grok

import (
	"fmt"

	"github.com/Xsxdot/handoff/internal/executor"
)

// OneShotArgv 把 model / prompt / limits 编成 grok 一次性 argv。
//
// 非空 Effort 目前只允许 executor.EffortLow；其它值 fail-closed，避免把未知
// 档位静默喂给 CLI。
func OneShotArgv(model, prompt string, limits executor.OneShotLimits) ([]string, error) {
	if limits.Effort != "" && limits.Effort != executor.EffortLow {
		return nil, fmt.Errorf("grok oneshot 不认识 effort %q（仅支持 %q 或空）",
			limits.Effort, executor.EffortLow)
	}
	args := []string{"grok"}
	if limits.Effort == executor.EffortLow {
		args = append(args, "--effort", executor.EffortLow)
	}
	if model != "" {
		args = append(args, "-m", model)
	}
	return append(args, "-p", prompt), nil
}
