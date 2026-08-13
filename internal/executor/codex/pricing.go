// pricing.go —— codex 的 API 牌价表与花费估算。
//
// 职责：把 token 数按公开牌价折成花费（ticks）。
// 边界：只服务 codex——四家 executor 里只有它一个字都不报花费，
// 其余三家自报，绝不拿这张表去覆盖它们的自报值。
//
// 纪律：**表里没有的模型一律 CostUnknown，不猜、不用默认价、不拿邻近型号顶。**
// 猜错是静默错误——数字照常显示，只是错的。缺失只是不显示，不会撒谎。
//
// 这张表算出来的是「等价 API 成本」，不一定是账单：codex 走 ChatGPT 订阅时
// 不按 token 另行计费。协议里看不出走的是哪条计费路径，所以本实现不区分，
// 也不隐藏这个数——它作为「烧了多少资源」的量度仍然成立，
// `CostEstimated` 的「估算」标记已经在提示别把它当账单。
package codex

import (
	"math"

	"github.com/xushixin/handoff/internal/proto"
)

// modelPrice 是单个模型的三档单价，单位：美元 / 百万 token。
type modelPrice struct {
	Input       float64 // 未命中缓存的输入
	CachedInput float64 // 命中缓存的输入
	Output      float64 // 产出（含 reasoning）
}

// modelPrices 是 OpenAI 公开 API 牌价。
//
// 数据来源：developers.openai.com/api/docs/pricing，**取价日期 2026-08-13**。
// 价格会变，表里的值只对写下它的那天负责；过期的后果是数字偏差，
// 而缺失的后果只是不显示——两种失效模式都不撒谎，所以宁缺毋滥。
//
// 刻意不收的两类：
//   - `-pro` 系列：官方页对它们的 cached input 是「—」（不适用），
//     三档缺一档就估不准；
//   - `gpt-5-codex` / `gpt-5.1-codex` / `gpt-5.2-codex`：官方 API 定价页当天
//     只列了 gpt-5.3-codex 一个 codex 型号，其余没有可引的公开单价。
var modelPrices = map[string]modelPrice{
	"gpt-5.6-sol":   {Input: 5.00, CachedInput: 0.50, Output: 30.00},
	"gpt-5.6-terra": {Input: 2.00, CachedInput: 0.20, Output: 12.00},
	"gpt-5.6-luna":  {Input: 0.20, CachedInput: 0.02, Output: 1.20},
	"gpt-5.5":       {Input: 5.00, CachedInput: 0.50, Output: 30.00},
	"gpt-5.4":       {Input: 2.50, CachedInput: 0.25, Output: 15.00},
	"gpt-5.4-mini":  {Input: 0.75, CachedInput: 0.075, Output: 4.50},
	"gpt-5.4-nano":  {Input: 0.20, CachedInput: 0.02, Output: 1.25},
	"gpt-5.3-codex": {Input: 1.75, CachedInput: 0.175, Output: 14.00},
	"gpt-5.2":       {Input: 1.75, CachedInput: 0.175, Output: 14.00},
	"gpt-5.1":       {Input: 1.25, CachedInput: 0.125, Output: 10.00},
	"gpt-5":         {Input: 1.25, CachedInput: 0.125, Output: 10.00},
	"gpt-5-mini":    {Input: 0.25, CachedInput: 0.025, Output: 2.00},
	"gpt-5-nano":    {Input: 0.05, CachedInput: 0.005, Output: 0.40},
}

// estimateTicks 按牌价估算这些 token 的花费。
//
// 参数：
//   - model: 实际模型名（空串或表里没有 → CostUnknown）
//   - input: 未命中缓存的输入；cached: 命中缓存的输入；output: 产出
//
// 返回：
//   - ticks: 花费（1 USD = 10^10 ticks）；CostUnknown 时恒为 0
//   - state: CostEstimated（表里有）或 CostUnknown（表里没有）
//
// 注意：先按美元算完再一次性转 ticks，不要三档各自取整——三次整除的误差会累积。
func estimateTicks(model string, input, cached, output int) (int64, proto.CostState) {
	p, ok := modelPrices[model]
	if !ok {
		return 0, proto.CostUnknown
	}
	usd := (float64(input)*p.Input + float64(cached)*p.CachedInput +
		float64(output)*p.Output) / 1e6
	if usd <= 0 {
		return 0, proto.CostEstimated // 量为 0 时花费确实是 0，这不是「不知道」
	}
	return int64(math.Round(usd * 1e10)), proto.CostEstimated
}
