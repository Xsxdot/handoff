// usage.go —— opencode 的 token 用量与实际模型名解析。
//
// 职责：把 message.updated 的 properties.info 解析成模型名与 proto.Usage。
// 边界：纯函数，不碰 runState、不发事件、不写日志——接线在 adapter.go。
package opencode

import (
	"encoding/json"

	"github.com/Xsxdot/handoff/internal/proto"
)

// parseMessageUsage 从 message.updated 的 properties 里取模型名与 context 占用。
//
// 参数：
//   - props: 事件的 properties 原文（info 是完整的 message 对象）
//
// 返回：
//   - model: info.modelID；u: context 占用，零值或非 assistant 时为 nil；
//     ok: 报文可解析时为 true
//
// 注意（两条，都会导致界面显示错的数）：
//   - **不能取 `tokens.total`**：实测 `total 47071 = input 131 + output 182 +
//     reasoning 294 + cache.read 46464`，它含产出侧，不是占用。
//     占用是 `input + cache.read + cache.write`。
//   - **零值帧必须跳过**：同一条消息会被推多次，且**新建的 assistant 消息
//     tokens 全是 0**。不跳过的话，界面会在每条新消息开头闪回 0
//     （探针笔记 §3.1）。
func parseMessageUsage(props json.RawMessage) (string, *proto.Usage, bool) {
	var p struct {
		Info struct {
			Role    string `json:"role"`
			ModelID string `json:"modelID"`
			Tokens  struct {
				Input int `json:"input"`
				Cache struct {
					Read  int `json:"read"`
					Write int `json:"write"`
				} `json:"cache"`
			} `json:"tokens"`
		} `json:"info"`
	}
	if err := json.Unmarshal(props, &p); err != nil {
		return "", nil, false
	}
	if p.Info.Role != "assistant" {
		return "", nil, true // user 消息没有模型侧用量，不是错误
	}
	tokens := p.Info.Tokens.Input + p.Info.Tokens.Cache.Read + p.Info.Tokens.Cache.Write
	if tokens <= 0 {
		return p.Info.ModelID, nil, true // 新建的消息，还没数字
	}
	return p.Info.ModelID, &proto.Usage{ContextTokens: tokens}, true
}
