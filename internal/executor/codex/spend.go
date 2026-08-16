// spend.go —— codex 的**累计消耗**账目解析。
//
// 职责：把 thread/tokenUsage/updated 的 total 差分成「本回合新增」的账目。
// 边界：纯函数 + 一个值类型的基线，不碰 runState、不发事件、不写日志——
// 接线在 adapter.go。
//
// 与同目录 usage.go 的关系：usage.go 取 `last.inputTokens` 解「当前 context 占用」，
// 本文件取 `total` 的差分解「一共烧了多少」。**同一帧、两个口径、不同字段**，
// 是本仓库最容易混的一处，刻意分文件。
package codex

import (
	"encoding/json"

	"github.com/Xsxdot/handoff/internal/proto"
)

// spendBase 是「上一个回合结束时的 total」，用来把 thread 累计差分成回合增量。
//
// Model 是实际模型名（牌价估算要用）。三个 token 字段是**已归一化**的口径
// （输入不含缓存）。零值 = 本进程还没有已结束的回合。
type spendBase struct {
	Model  string
	Input  int
	Cached int
	Output int
	// pending 是本回合最近一次看到的 total（尚未推进为基线）。
	pending struct {
		Input  int
		Cached int
		Output int
	}
}

// commit 把本回合最近一次看到的 total 推进为下一个回合的基线。
// 调用时机：收到 turn/completed 时（回合边界）。
func (b spendBase) commit() spendBase {
	b.Input, b.Cached, b.Output = b.pending.Input, b.pending.Cached, b.pending.Output
	return b
}

// parseTurnSpend 从 thread/tokenUsage/updated 取本回合新增的消耗。
//
// 参数：
//   - params: 通知的 params 原文（含 turnId 与 tokenUsage）
//   - base: 上一个回合结束时的基线
//
// 返回：
//   - e: 账目；Key 取 params.turnId
//   - next: 更新了 pending 的基线，调用方存回 runState
//   - ok: turnId 非空且 tokenUsage 可解析时为 true
//
// 三条 codex 独有的规则（**改错了不会报错，只会显示错的数**）：
//   - **取 `total` 不取 `last`**：这里要的是「一共烧了多少」，`last` 只是最后
//     一次调用。（usage.go 里的当前占用恰好相反，取 `last`。）
//   - **`cachedInputTokens` 是 `inputTokens` 的子集，要减不要加**——与
//     claudecode/opencode 相反。实抓佐证 `totalTokens 24673 = inputTokens 24668
//   - outputTokens 5`，缓存若是加项等式不成立。
//   - **`reasoningOutputTokens` 是 `outputTokens` 的子集，不再相加**（同一条等式）。
//
// 为什么一个回合内可以反复调用本函数：同一个 turnId 会来多条通知，但账本按键
// **覆盖**，最后一条即最终值。所以不需要判断「哪条是最后一条」。
//
// 差分为负说明 total 归零了（thread/resume 的形态，未实测），此时按当前值
// 全量入账并由调用方打 Warn——宁可某个回合多算一点，也不写负数进账本。
func parseTurnSpend(params json.RawMessage, base spendBase) (proto.SpendEntry, spendBase, bool) {
	var p struct {
		TurnID     string `json:"turnId"`
		TokenUsage struct {
			Total struct {
				InputTokens       int `json:"inputTokens"`
				CachedInputTokens int `json:"cachedInputTokens"`
				OutputTokens      int `json:"outputTokens"`
			} `json:"total"`
		} `json:"tokenUsage"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.TurnID == "" {
		return proto.SpendEntry{}, base, false
	}
	t := p.TokenUsage.Total
	// 归一化：输入减掉缓存这一子集
	curIn := t.InputTokens - t.CachedInputTokens
	if curIn < 0 {
		curIn = 0
	}
	curCached, curOut := t.CachedInputTokens, t.OutputTokens

	next := base
	next.pending.Input, next.pending.Cached, next.pending.Output = curIn, curCached, curOut

	in := nonNegDelta(curIn, base.Input)
	cached := nonNegDelta(curCached, base.Cached)
	out := nonNegDelta(curOut, base.Output)

	ticks, state := estimateTicks(base.Model, in, cached, out)
	return proto.SpendEntry{
		Key:          p.TurnID,
		InputTokens:  in,
		CachedTokens: cached,
		OutputTokens: out,
		CostTicks:    ticks,
		CostState:    state,
	}, next, true
}

// nonNegDelta 求 cur−base，base 大于 cur 时（计数器归零）退回 cur 本身。
func nonNegDelta(cur, base int) int {
	if cur < base {
		return cur
	}
	return cur - base
}
