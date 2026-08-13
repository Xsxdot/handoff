// usage.go —— codex 的 token 用量解析。
//
// 职责：把 thread/tokenUsage/updated 通知的 params 解析成 proto.Usage。
// 边界：纯函数，不碰 runState、不发事件、不写日志——接线在 adapter.go。
package codex

import (
	"encoding/json"

	"github.com/xushixin/handoff/internal/proto"
)

// parseTokenUsage 从 thread/tokenUsage/updated 的 params 里取当前 context 占用。
//
// 参数：
//   - params: 通知的 params 原文
//
// 返回：
//   - 解析成功且占用 > 0 时返回快照与 true；否则返回 nil 与 false
//
// 注意（两条 codex 独有的规则，改错了不会报错、只会显示错的数）：
//   - **取 `last` 不取 `total`**：`total` 是整个 thread 的累加，随回合单调增长，
//     不是「当前占用」。
//   - **`cachedInputTokens` 是 `inputTokens` 的子集，不再相加**：实抓佐证
//     `last.totalTokens 24673 = inputTokens 24668 + outputTokens 5`，
//     再加缓存就是重复计数。这一点与 grok/claudecode/opencode **相反**。
func parseTokenUsage(params json.RawMessage) (*proto.Usage, bool) {
	var p struct {
		TokenUsage struct {
			Last struct {
				InputTokens int `json:"inputTokens"`
			} `json:"last"`
			ModelContextWindow int `json:"modelContextWindow"`
		} `json:"tokenUsage"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, false
	}
	tokens := p.TokenUsage.Last.InputTokens
	if tokens <= 0 {
		return nil, false // 0 不是「占用为零」，是「还没有数」——不编造
	}
	u := &proto.Usage{ContextTokens: tokens}
	if w := p.TokenUsage.ModelContextWindow; w > 0 {
		u.ContextWindow = &w
	}
	return u, true
}
