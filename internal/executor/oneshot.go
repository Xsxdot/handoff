// oneshot.go：执行者名字 → 一次性 CLI argv 的唯一映射点。
//
// 职责：
//   - 把「执行者名 + 模型 + prompt」翻译为一次性命令行调用（prompt 作末位参数）
//   - 审批者的 CLI 裁决与未来的降级/补充调用共用这一个映射，保证各执行者的
//     调用形态只在一点登记，不会出现多处各自拼 argv 的漂移
//
// 边界：
//   - 不执行命令、不读配置、不做任何 I/O
//   - 新执行者（如 grok）在此登记：加一个 switch 分支即接入
package executor

import "fmt"

// OneShotArgs 返回执行者的一次性调用 argv（prompt 作为末位参数）。
//
// 参数：
//   - executorName: 执行者名，目前支持 opencode / claude / grok / agy；未知名字返回错误
//   - model: 模型名；空表示让执行者用自身默认模型（省略对应参数）
//   - prompt: 一次性 prompt 原文，作为命令的最后一个参数
//
// 返回：
//   - 可直接交给 exec.CommandContext 的 argv；映射失败时返回错误，
//     错误文本列出支持的执行者名
func OneShotArgs(executorName, model, prompt string) ([]string, error) {
	switch executorName {
	case "opencode":
		if model != "" {
			return []string{"opencode", "run", "-m", model, prompt}, nil
		}
		return []string{"opencode", "run", prompt}, nil
	case "claude":
		if model != "" {
			return []string{"claude", "-p", "--model", model, prompt}, nil
		}
		return []string{"claude", "-p", prompt}, nil
	case "grok":
		// why --effort low：实测同一条裁决 prompt，默认 high effort 32.4s、
		// low 7.5s，而审批者默认超时 60s——high 档等于把预算烧掉一半以上。
		// 本函数的职责就是「一次性调用形态的唯一登记点」，把「一次性 = 廉价
		// 快速」编码在这里符合定位。
		//
		// why 参数顺序不能动：-p <PROMPT> 是取值参数而非开关，--effort 必须
		// 排在 -p 之前，否则 grok 报 "a value is required for '--single'"。
		// prompt 仍是末位参数，本函数的契约不变。
		if model != "" {
			return []string{"grok", "--effort", "low", "-m", model, "-p", prompt}, nil
		}
		return []string{"grok", "--effort", "low", "-p", prompt}, nil
	case "agy":
		if model != "" {
			return []string{"agy", "-p", "--model", model, prompt}, nil
		}
		return []string{"agy", "-p", prompt}, nil
	default:
		return nil, fmt.Errorf("未知执行者 %q（one-shot 支持 opencode/claude/grok/agy）", executorName)
	}
}
