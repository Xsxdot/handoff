// spend.go —— agy 的用量消耗账目解析。
//
// 职责：把 result 行的 usage 解析成一条 proto.SpendEntry。
package agy

import (
	"fmt"

	"github.com/Xsxdot/handoff/internal/proto"
)

// parseSpend 从 AgyUsageRaw 解析出 SpendEntry。
func parseSpend(raw *AgyUsageRaw, conversationID string, numTurns int) (proto.SpendEntry, bool) {
	if raw == nil || conversationID == "" {
		return proto.SpendEntry{}, false
	}
	if raw.InputTokens == 0 && raw.OutputTokens == 0 && raw.CacheReadTokens == 0 {
		return proto.SpendEntry{}, false
	}

	key := fmt.Sprintf("%s-turn-%d", conversationID, numTurns)
	if numTurns <= 0 {
		key = fmt.Sprintf("%s-spend", conversationID)
	}

	return proto.SpendEntry{
		Key:          key,
		InputTokens:  raw.InputTokens,
		CachedTokens: raw.CacheReadTokens,
		OutputTokens: raw.OutputTokens + raw.ThinkingTokens,
		CostTicks:    0,
		CostState:    proto.CostUnknown,
	}, true
}
