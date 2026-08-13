// usage.go —— grok 的 token 用量与实际模型名解析。
//
// 职责：把两条 _x.ai/* 私有通知解析成 proto.Usage 与模型名/窗口。
// 边界：纯函数，不碰 runState、不发事件、不写日志——接线在 adapter.go。
//
// 为什么用量在私有通知上：grok 的 ACP 线把用量放在 _x.ai/session_notification
// 与 _x.ai/models/update 上，标准的 session/update 变体一个都不带计数。
package grok

import (
	"encoding/json"

	"github.com/xushixin/handoff/internal/proto"
)

// parseResponseCompleted 从 _x.ai/session_notification 的 params 里取当前
// context 占用；只认 sessionUpdate == "response_completed"。
//
// 参数：
//   - params: 通知的 params 原文
//
// 返回：
//   - 是 response_completed 且占用 > 0 时返回快照与 true；否则 nil 与 false
//
// 注意（两条规则，改错了不会报错、只会显示错的数）：
//   - **只认 response_completed，绝不认 turn_completed**：后者的 usage 是
//     整回合**跨模型调用的累加**。实测 modelCalls=4 的回合里
//     `inputTokens 138637` 恰等于四次调用的 input_tokens 之和加 cache_read 之和，
//     而真实占用只有 34752——差 4 倍，长回合会超过 100%（探针笔记 §4.2）。
//     turn_completed 是将来做「累计消耗」的正确来源，不是这里的。
//   - **snake_case 的 input_tokens 不含缓存，必须相加**；而 turn_completed 的
//     camelCase inputTokens 已含缓存。同一条线两套约定，**按字段名模糊匹配必错**。
func parseResponseCompleted(params json.RawMessage) (*proto.Usage, bool) {
	var p struct {
		Update struct {
			Kind  string `json:"sessionUpdate"`
			Usage struct {
				InputTokens         int `json:"input_tokens"`
				CacheReadTokens     int `json:"cache_read_input_tokens"`
				CacheCreationTokens int `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		} `json:"update"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, false
	}
	if p.Update.Kind != "response_completed" {
		return nil, false
	}
	tokens := p.Update.Usage.InputTokens + p.Update.Usage.CacheReadTokens +
		p.Update.Usage.CacheCreationTokens
	if tokens <= 0 {
		return nil, false
	}
	return &proto.Usage{ContextTokens: tokens}, true
}

// parseModelsUpdate 从 _x.ai/models/update 的 params 里取当前模型名与窗口上限。
//
// 返回：
//   - model: currentModelId；window: 该模型的 _meta.totalContextTokens（取不到为 0）
//   - ok: currentModelId 非空时为 true
//
// 注意：availableModels 是**数组且可能含多个模型**，必须按 currentModelId 匹配，
// 取第 0 个会在多模型场景下拿到别的模型的窗口——又一个静默错误。
func parseModelsUpdate(params json.RawMessage) (string, int, bool) {
	var p struct {
		CurrentModelID  string `json:"currentModelId"`
		AvailableModels []struct {
			ModelID string `json:"modelId"`
			Meta    struct {
				TotalContextTokens int `json:"totalContextTokens"`
			} `json:"_meta"`
		} `json:"availableModels"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.CurrentModelID == "" {
		return "", 0, false
	}
	for _, m := range p.AvailableModels {
		if m.ModelID == p.CurrentModelID {
			return p.CurrentModelID, m.Meta.TotalContextTokens, true
		}
	}
	return p.CurrentModelID, 0, true // 有模型名没窗口，也是有效信息
}
