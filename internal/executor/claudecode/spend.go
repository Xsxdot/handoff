// spend.go —— claudecode 的**累计消耗**账目解析。
//
// 职责：把 result 行解析成一条 proto.SpendEntry（本轮新增的 token 与花费）。
// 边界：纯函数，不碰 runState、不发事件、不写日志——接线在 adapter.go。
//
// 与同目录 usage.go 的关系：usage.go 解「当前 context 占用」（assistant 消息，
// 最后一次调用的输入侧），本文件解「一共烧了多少」（result 行，逐轮累加）。
// 两个口径的公式不同且都容易写错，刻意分文件，不要合并。
package claudecode

import (
	"encoding/json"
	"math"

	"github.com/xushixin/handoff/internal/proto"
)

// usdToTicks 把浮点美元换成整数 ticks（1 USD = 10^10）。
//
// 为什么统一到整数：花费要跨回合求和，浮点累加的误差对不上服务端的账
// （grok 官方文档的原话）。转换在**入账时**做一次，之后全程整数。
func usdToTicks(usd float64) int64 {
	if usd <= 0 {
		return 0
	}
	return int64(math.Round(usd * 1e10))
}

// parseResultSpend 从 result 行取本轮新增的消耗。
//
// 参数：
//   - m: result 行
//   - prevCostUSD: **同一个进程内**上一次 result 行的 total_cost_usd（首个回合传 0）
//
// 返回：
//   - e: 账目；Key 取 result 行的 uuid
//   - nextPrev: 本次的 total_cost_usd，调用方存回 runState 作下次的基线
//   - ok: uuid 非空且 usage 可解析时为 true
//
// 两条 claudecode 独有的规则（**改错了不会报错，只会显示错的数**）：
//   - **取 `usage.*` 不取 `modelUsage.*`**：前者是本轮，后者是**进程累计**。
//     进程累计在 --resume 后会归零（实测前一进程收尾 in=3095，新进程首轮 in=98），
//     所以它既不是会话累计也不能直接用。
//   - **`input_tokens` 不含缓存，缓存两项要相加**——与 codex/grok 相反。
//     实测轮 3 的 input_tokens 只有 54 而 cache_read 有 32768。
//
// 花费只有进程内累计值 `total_cost_usd`，所以本轮花费 = 本次 − 上次。
// runState 天生是进程级的，新进程基线自然从 0 起，首个回合的差分就是它自己——正确。
// 差分为负说明基线陈旧（不该发生），退回取当前值本身，绝不写负数进账本。
func parseResultSpend(m streamMsg, prevCostUSD float64) (proto.SpendEntry, float64, bool) {
	if m.UUID == "" {
		return proto.SpendEntry{}, prevCostUSD, false // 没有键就没有幂等，宁可不记
	}
	var u struct {
		InputTokens         int `json:"input_tokens"`
		CacheReadTokens     int `json:"cache_read_input_tokens"`
		CacheCreationTokens int `json:"cache_creation_input_tokens"`
		OutputTokens        int `json:"output_tokens"`
	}
	if len(m.Usage) == 0 {
		return proto.SpendEntry{}, prevCostUSD, false
	}
	if err := json.Unmarshal(m.Usage, &u); err != nil {
		return proto.SpendEntry{}, prevCostUSD, false
	}

	delta := m.TotalCostUSD - prevCostUSD
	if delta < 0 {
		delta = m.TotalCostUSD // 基线陈旧：退回当前值（调用方会打 Warn）
	}
	return proto.SpendEntry{
		Key:          m.UUID,
		InputTokens:  u.InputTokens,
		CachedTokens: u.CacheReadTokens + u.CacheCreationTokens,
		OutputTokens: u.OutputTokens,
		CostTicks:    usdToTicks(delta),
		CostState:    proto.CostReported,
	}, m.TotalCostUSD, true
}
