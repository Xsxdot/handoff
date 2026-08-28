// spend.go —— agy 的用量消耗账目解析。
//
// 职责：把 result 行的 usage（会话累计值）解析成一条 proto.SpendEntry。
// 使用会话级稳定 Key 覆盖既有条目，防止多回合累计值在 ledger 中被错误求和。
package agy

import (
	"fmt"

	"github.com/Xsxdot/handoff/internal/proto"
)

// parseSpend 从 AgyUsageRaw 解析出 SpendEntry。
func parseSpend(raw *AgyUsageRaw, conversationID string) (proto.SpendEntry, bool) {
	if raw == nil || conversationID == "" {
		return proto.SpendEntry{}, false
	}
	if raw.InputTokens == 0 && raw.OutputTokens == 0 && raw.CacheReadTokens == 0 {
		return proto.SpendEntry{}, false
	}

	return proto.SpendEntry{
		Key:          fmt.Sprintf("%s-spend", conversationID),
		InputTokens:  raw.InputTokens,
		CachedTokens: raw.CacheReadTokens,
		OutputTokens: raw.OutputTokens + raw.ThinkingTokens,
		CostTicks:    0,
		CostState:    proto.CostUnknown,
	}, true
}
