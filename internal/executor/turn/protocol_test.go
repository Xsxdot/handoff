package turn_test

import (
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor/turn"
)

func TestRenderPromptEmbedsTaskIDAndPlan(t *testing.T) {
	got, err := turn.RenderPrompt("T1", "第一步：改 foo.go")
	if err != nil {
		t.Fatalf("RenderPrompt 出错: %v", err)
	}
	for _, want := range []string{"T1", "第一步：改 foo.go", `{"ask":`, `{"branch":`} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt 缺少 %q\n实际:\n%s", want, got)
		}
	}
}

func TestParseTrailer(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		wantKind string
		wantVal  string
	}{
		{"ask 单行", `{"ask":"用哪个库？"}`, "ask", "用哪个库？"},
		{"finish 单行", `{"branch":"handoff/T1","commit":"abc123","summary":"done"}`, "finish", "abc123"},
		{"取最后一个 JSON 行", "{\"ask\":\"旧\"}\n说明文字\n{\"ask\":\"新\"}", "ask", "新"},
		{"末行普通文本时回退更早的 JSON 行", "{\"ask\":\"问题\"}\n收尾说明", "ask", "问题"},
		{"损坏 JSON 按 none", `{"ask":`, "none", ""},
		{"无 JSON 行按 none", "普通输出，没有协议行", "none", ""},
		{"合法 JSON 但无协议字段", `{"foo":1}`, "none", ""},
		{"末行前缀正文 + finish（B48 现场）",
			`g.{"branch":"handoff/T1","commit":"abc123","summary":"done"}`, "finish", "abc123"},
		{"末行后缀正文 + ask", `{"ask":"用哪个库？"} 好的`, "ask", "用哪个库？"},
		{"末行前后都有正文", `前缀 {"ask":"问题"} 后缀`, "ask", "问题"},
		{"末行是正文时回退到更早的以 { 开头的行",
			"{\"ask\":\"更早的问题\"}\n收尾说明没有花括号", "ask", "更早的问题"},
		{"末行含 { 但不是合法 JSON", "见 {} 占位\n真的没有协议行", "none", ""},
		{"末行是合法 JSON 但无协议字段", `说明 {"foo":1}`, "none", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, tr := turn.ParseTrailer(c.text)
			if kind != c.wantKind {
				t.Fatalf("kind = %q，期望 %q", kind, c.wantKind)
			}
			switch c.wantKind {
			case "ask":
				if tr.Question != c.wantVal {
					t.Errorf("Question = %q，期望 %q", tr.Question, c.wantVal)
				}
			case "finish":
				if tr.Commit != c.wantVal {
					t.Errorf("Commit = %q，期望 %q", tr.Commit, c.wantVal)
				}
			}
		})
	}
}
