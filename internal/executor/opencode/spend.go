// spend.go —— opencode 的**累计消耗**账目解析。
//
// 职责：把 message.updated 的 properties.info 解析成一条 proto.SpendEntry。
// 边界：纯函数，不碰 runState、不发事件、不写日志——接线在 adapter.go。
//
// 与同目录 usage.go 的关系：usage.go 取 `input + cache.read + cache.write` 解
// 「当前 context 占用」（只算输入侧），本文件还要算上产出侧，解「一共烧了多少」。
//
// opencode 是四家里唯一**消息级**而非回合级的，幂等键取 info.id。
// 服务端对同一条消息会随生成推很多次、id 相同而 tokens 在涨——账本按键**覆盖**，
// 最后一帧即最终值，所以这里不需要判断「哪一帧是最后一帧」。
package opencode

import (
	"encoding/json"
	"math"

	"github.com/Xsxdot/handoff/internal/proto"
)

// parseMessageSpend 从 message.updated 的 properties 取这条消息的消耗。
//
// 参数：
//   - props: 事件的 properties 原文（info 是完整的 message 对象）
//
// 返回：
//   - e: 账目；Key 取 info.id
//   - ok: 是 assistant 消息、有 id、且至少有一个非零计数时为 true
//
// 两条 opencode 独有的规则（**改错了不会报错，只会显示错的数**）：
//   - **`tokens.reasoning` 与 `tokens.output` 平行，要相加**——与 codex/grok
//     相反（那两家的 reasoning 是 output 的子集）。实抓等式：
//     `total 47071 = input 131 + output 182 + reasoning 294 + cache.read 46464`。
//     不加就少算，而且这里 reasoning 比 output 还大，少算得很显眼。
//   - **全零帧必须跳过**：新建的 assistant 消息 tokens 全是 0，入账会产生一行
//     空账目（虽然随后会被同 id 覆盖，但中间态会让界面闪一下 0）。
func parseMessageSpend(props json.RawMessage) (proto.SpendEntry, bool) {
	var p struct {
		Info struct {
			ID     string  `json:"id"`
			Role   string  `json:"role"`
			Cost   float64 `json:"cost"`
			Tokens struct {
				Input     int `json:"input"`
				Output    int `json:"output"`
				Reasoning int `json:"reasoning"`
				Cache     struct {
					Read  int `json:"read"`
					Write int `json:"write"`
				} `json:"cache"`
			} `json:"tokens"`
		} `json:"info"`
	}
	if err := json.Unmarshal(props, &p); err != nil {
		return proto.SpendEntry{}, false
	}
	if p.Info.Role != "assistant" || p.Info.ID == "" {
		return proto.SpendEntry{}, false
	}
	tk := p.Info.Tokens
	cached := tk.Cache.Read + tk.Cache.Write
	out := tk.Output + tk.Reasoning // 平行相加，见函数注释
	if tk.Input == 0 && cached == 0 && out == 0 && p.Info.Cost == 0 {
		return proto.SpendEntry{}, false // 新建的空消息，还没有数
	}
	e := proto.SpendEntry{
		Key:          p.Info.ID,
		InputTokens:  tk.Input,
		CachedTokens: cached,
		OutputTokens: out,
		CostState:    proto.CostUnknown,
	}
	if p.Info.Cost > 0 {
		e.CostTicks = int64(math.Round(p.Info.Cost * 1e10))
		e.CostState = proto.CostReported
	}
	return e, true
}
