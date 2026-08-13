// spend.go —— grok 的**累计消耗**账目解析。
//
// 职责：把 session/prompt 响应的 result._meta 解析成一条 proto.SpendEntry。
// 边界：纯函数，不碰 runState、不发事件、不写日志——接线在 adapter.go。
//
// 与同目录 usage.go 的关系（**grok 是四家里最容易搞错的一家**）：同一条 ACP 线
// 上有两套命名，且缓存的算法**相反**——
//   - usage.go 取 _x.ai/session_notification 的 response_completed，字段是
//     snake_case（input_tokens / cache_read_input_tokens），**不含**缓存要相加，
//     解的是「当前 context 占用」；
//   - 本文件取 session/prompt 响应的 _meta，字段是 camelCase
//     （inputTokens / cachedReadTokens），**已含**缓存要相减，
//     解的是「整个回合消耗了多少」。
//
// 按字段名模糊匹配必错，grok 官方文档已确认这是它有意为之的两套投影。
package grok

import (
	"encoding/json"

	"github.com/xushixin/handoff/internal/proto"
)

// parseTurnMetaSpend 从 session/prompt 响应的 _meta 取本回合的消耗。
//
// 参数：
//   - meta: result._meta 原文
//
// 返回：
//   - e: 账目；Key 取 promptId
//   - ok: promptId 非空时为 true
//
// 三条规则：
//   - **inputTokens 含缓存，要减**（见文件头）。
//   - **reasoningTokens 是 outputTokens 的子集，不再相加**：实抓
//     `totalTokens 34558 = inputTokens 34502 + outputTokens 56`，
//     而 reasoningTokens 是 51——加上就超过 outputTokens 了。
//   - **花费缺席记 CostUnknown，绝不是 0**：grok 只对 API-key 流量打花费戳，
//     pool/OAuth 路径经常整块没有；cost_is_partial 为真时它还会**主动**把所有
//     花费字段一并省略，就是为了不让消费者把分项加成一份假的完整账单。
//     照抄这个语义：拿不到就说不知道。
func parseTurnMetaSpend(meta json.RawMessage) (proto.SpendEntry, bool) {
	var m struct {
		PromptID string `json:"promptId"`
		Usage    struct {
			InputTokens         int   `json:"inputTokens"`
			OutputTokens        int   `json:"outputTokens"`
			CachedReadTokens    int   `json:"cachedReadTokens"`
			CacheCreationTokens int   `json:"cacheCreationTokens"`
			CostUsdTicks        int64 `json:"costUsdTicks"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(meta, &m); err != nil || m.PromptID == "" {
		return proto.SpendEntry{}, false
	}
	cached := m.Usage.CachedReadTokens + m.Usage.CacheCreationTokens
	in := m.Usage.InputTokens - cached
	if in < 0 {
		in = 0
	}
	e := proto.SpendEntry{
		Key:          m.PromptID,
		InputTokens:  in,
		CachedTokens: cached,
		OutputTokens: m.Usage.OutputTokens,
		CostState:    proto.CostUnknown,
	}
	if m.Usage.CostUsdTicks > 0 {
		e.CostTicks = m.Usage.CostUsdTicks
		e.CostState = proto.CostReported
	}
	return e, true
}
