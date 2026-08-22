package turn_test

import (
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor/turn"
)

func TestRenderPromptEmbedsTaskIDAndPlan(t *testing.T) {
	got, err := turn.RenderPrompt("T1", "第一步：改 foo.go", "")
	if err != nil {
		t.Fatalf("RenderPrompt 出错: %v", err)
	}
	for _, want := range []string{"T1", "第一步：改 foo.go", `{"ask":`, `{"branch":`} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt 缺少 %q\n实际:\n%s", want, got)
		}
	}
}

func TestRenderPromptEmbedsDisciplineBlock(t *testing.T) {
	out, err := turn.RenderPrompt("T1", "计划正文", "# 执行纪律\n自己逐 task 实现")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "自己逐 task 实现") {
		t.Error("纪律块正文未出现")
	}
	if !strings.Contains(out, "--- 执行纪律（先读这段，再读计划）---") {
		t.Error("纪律块小标题未出现")
	}
	iRules := strings.Index(out, "提问纪律")
	iDisc := strings.Index(out, "自己逐 task 实现")
	iPlan := strings.Index(out, "--- 实现计划 ---")
	if !(iRules < iDisc && iDisc < iPlan) {
		t.Errorf("顺序错：铁律=%d 纪律=%d 计划=%d", iRules, iDisc, iPlan)
	}
}

func TestRenderPromptWithoutDisciplineHasNoMarker(t *testing.T) {
	out, err := turn.RenderPrompt("T1", "计划正文", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "执行纪律") {
		t.Error("空纪律块不该留下任何小标题")
	}
	if !strings.Contains(out, "--- 实现计划 ---") || !strings.Contains(out, "计划正文") {
		t.Error("原有结构被破坏")
	}
}

func TestProtocolRulesMatchesTemplate(t *testing.T) {
	out, err := turn.RenderPrompt("T1", "x", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, turn.ProtocolRules) {
		t.Fatal("ProtocolRules 与模板已漂移")
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
		{"裁决块在 trailer 之后", "正文\n" + `{"branch":"handoff/T1","commit":"abc123","summary":"done"}` +
			"\n```handoff-verdict\n{\"verdict\":\"pass\",\"findings\":[{\"summary\":\"x\"}]}\n```", "finish", "abc123"},
		{"裁决块在 trailer 之前", "正文\n```handoff-verdict\n{\"verdict\":\"fail\",\"findings\":[]}\n```\n" +
			`{"branch":"handoff/T1","commit":"def456","summary":"done"}`, "finish", "def456"},
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
