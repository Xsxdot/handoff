// usage.go —— agy 的 token 用量解析。
//
// 职责：把 step_update 或 result 里的 usage 对象解析为 proto.Usage。
// 边界：纯函数，不碰 runState、不发事件、不写日志。
package agy

import (
	"github.com/Xsxdot/handoff/internal/proto"
)

// AgyUsageRaw 是 agy 输出中的 usage 结构。
type AgyUsageRaw struct {
	InputTokens     int `json:"input_tokens"`
	OutputTokens    int `json:"output_tokens"`
	ThinkingTokens  int `json:"thinking_tokens"`
	CacheReadTokens int `json:"cache_read_tokens"`
	TotalTokens     int `json:"total_tokens"`
}

// ParseUsage 把 agy 的原始用量转换为 proto.Usage。
//
// 上下文占用 = input_tokens + cache_read_tokens。
// 零值时返回 nil（不使用 0 冒充）。
func ParseUsage(raw *AgyUsageRaw) *proto.Usage {
	if raw == nil {
		return nil
	}
	ctxTokens := raw.InputTokens + raw.CacheReadTokens
	if ctxTokens <= 0 {
		return nil
	}
	return &proto.Usage{
		ContextTokens: ctxTokens,
	}
}
