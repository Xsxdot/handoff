// usage.go —— claudecode 的 token 用量与实际模型名解析。
//
// 职责：把 assistant 消息里的 model 与 usage 解析出来。
// 边界：纯函数，不碰 runState、不发事件、不写日志——接线在 adapter.go。
package claudecode

import (
	"encoding/json"

	"github.com/Xsxdot/handoff/internal/proto"
)

// parseAssistantUsage 从一条 assistant 的 **message 对象**里取模型名与 context 占用。
//
// 参数：
//   - msg: streamMsg.Message 的原文（model / content / usage 都在这一层，
//     与 content 同级；**不在 stream 行的顶层**）
//
// 返回：
//   - model: 该条消息的模型名（可能为空）
//   - u: context 占用；零值时为 nil（**不用 0 冒充**）
//   - ok: 报文可解析时为 true
//
// 注意：**缓存两项要相加**（`input_tokens` 不含缓存），与 codex 的规则相反。
// 窗口**不在 assistant 消息里**，而在 result 行的 modelUsage 里
// （见 pickModelUsageWindow），且要等第一个回合结束才有——所以本函数返回的
// ContextWindow 恒为 nil，由 mapAssistant 接线把 runState 暂存的 r.ctxWindow
// 挂进来。
func parseAssistantUsage(msg json.RawMessage) (string, *proto.Usage, bool) {
	var m struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens         int `json:"input_tokens"`
			CacheCreationTokens int `json:"cache_creation_input_tokens"`
			CacheReadTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(msg, &m); err != nil {
		return "", nil, false
	}
	tokens := m.Usage.InputTokens + m.Usage.CacheCreationTokens + m.Usage.CacheReadTokens
	if tokens <= 0 {
		return m.Model, nil, true // 模型名与用量是两件事，前者仍然有效
	}
	return m.Model, &proto.Usage{ContextTokens: tokens}, true
}

// modelUsage 是 result 行 modelUsage map 里一个条目的窗口视图。
//
// 为什么只声明两个字段：modelUsage 里的 inputTokens / costUSD 等是**会话累计**，
// 属于另一个需求（B83）的料；本文件只取 contextWindow。canonicalModel 是多模型
// 场景下选中正确条目的依据（见 pickModelUsageWindow），一并声明。
type modelUsage struct {
	ContextWindow  int    `json:"contextWindow"`
	CanonicalModel string `json:"canonicalModel"`
}

// pickModelUsageWindow 从 result 行的 modelUsage map 里取当前模型的窗口上限。
//
// 参数：
//   - mu: modelUsage 原文（键=模型名）
//   - prefer: runState 上已知的实际模型名（多键时优先匹配它）
//
// 返回：
//   - window: 选中的 contextWindow；空 map 或没有有效数字时为 0
//   - confident: false 表示「多键且按 prefer / canonicalModel 都匹配不上，
//     挑了任意一个」——调用方必须打 Warn，不能静默挑第一个
//
// 为什么空 map 返回 confident=true：没有 modelUsage 是正常形态（结果行不带计数），
// 不是歧义，不需要告警。
func pickModelUsageWindow(mu map[string]modelUsage, prefer string) (window int, confident bool) {
	if len(mu) == 0 {
		return 0, true
	}
	if len(mu) == 1 {
		for _, v := range mu {
			return v.ContextWindow, true
		}
	}
	// 多键：先按 runState 已知模型名匹配键，或匹配条目自身的 canonicalModel
	for key, v := range mu {
		if key == prefer || v.CanonicalModel == prefer {
			return v.ContextWindow, true
		}
	}
	// 都匹配不上：取任意一个，但不自信——调用方据此告警，不静默
	for _, v := range mu {
		return v.ContextWindow, false
	}
	return 0, true
}
