package grok

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAskQuestionTextRendersQuestionsAndOptions(t *testing.T) {
	// 来自本机 grok 1.0.0 实测报文（spec §4.2.3）
	params := json.RawMessage(`{"sessionId":"s","toolCallId":"c9",` +
		`"questions":[{"question":"这个功能用哪种语言实现？","options":[` +
		`{"label":"Go","description":"用 Go 实现该功能"},` +
		`{"label":"Rust","description":"用 Rust 实现该功能"}],"multiSelect":null}],"mode":"default"}`)

	got := askQuestionText(params)
	for _, want := range []string{"这个功能用哪种语言实现？", "1) Go", "2) Rust", "用 Rust 实现该功能"} {
		if !strings.Contains(got, want) {
			t.Errorf("渲染文本缺少 %q，实得:\n%s", want, got)
		}
	}
	if strings.HasSuffix(got, "\n") {
		t.Errorf("尾部换行未清理: %q", got)
	}
}

func TestAskQuestionTextEmptyOnGarbage(t *testing.T) {
	for name, in := range map[string]string{
		"非 JSON":      `not json`,
		"缺 questions": `{"sessionId":"s"}`,
		"空 questions": `{"questions":[]}`,
	} {
		if got := askQuestionText(json.RawMessage(in)); got != "" {
			t.Errorf("%s: 应返回空串，实得 %q", name, got)
		}
	}
}
