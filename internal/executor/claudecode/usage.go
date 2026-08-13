// usage.go —— claudecode 的 token 用量与实际模型名解析。
//
// 职责：把 assistant 消息里的 model 与 usage 解析出来。
// 边界：纯函数，不碰 runState、不发事件、不写日志——接线在 adapter.go。
package claudecode

import (
	"encoding/json"

	"github.com/xushixin/handoff/internal/proto"
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
// claudecode 的协议里没有任何字段给窗口上限，所以 ContextWindow 恒为 nil，
// 界面据此只显绝对值——不去猜、不查表。
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
