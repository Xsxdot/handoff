package grok

import "testing"

// sessionUpdate 类型到帧类型的归类。
func TestUpdateFrameKind(t *testing.T) {
	cases := map[string]updateKind{
		"agent_message_chunk": updateText,
		"agent_thought_chunk": updateReasoning,
		"tool_call":           updateNone,
		"tool_call_update":    updateNone,
		"something_new":       updateNone,
	}
	for u, want := range cases {
		if got := updateFrameKind(u); got != want {
			t.Errorf("%s 应归为 %v，实得 %v", u, want, got)
		}
	}
}

// 既有不变式：思维链绝不进 bodyBuf（bodyBuf 是 ParseTrailer 的输入）。
func TestThoughtNeverEntersTurnText(t *testing.T) {
	acc := newTurnAccumulator()
	// feedRaw 接的是完整 ACP 消息（method/params/update 包裹），与真机形状一致
	acc.feedRaw([]byte(`{"method":"session/update","params":{"update":{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"我打算输出 {\"ask\":\"x\"}"}}}}`))
	if got := acc.turnText(); got != "" {
		t.Fatalf("思维链绝不能进回合正文，实得 %q", got)
	}
	// 但它照旧进 render 那一股（grok 的既有行为）
	if got := acc.takeRender(); got == "" {
		t.Fatal("思维链应照旧进 render 股（既有行为不该被改动）")
	}
}
