package ledger

// b452_skilled_template_test.go —— B452 S1（V1）：熟练工定义的两个新字段与 put 校验。
// 锁三件事：新字段往返、旧定义 JSON 字节不变（omitempty）、Validate 的规则表。

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestB452TemplateDefSkilledRoundTrip：带 receiver/mode 的定义经账本往返原样回来。
func TestB452TemplateDefSkilledRoundTrip(t *testing.T) {
	s := newTestStore(t)
	def := TemplateDef{Receiver: "mimo", Mode: TemplateModeReadonly, Target: TemplateTargetHere,
		Purpose: "investigate", Prompt: "查 {{CARD}}"}
	if _, err := s.PutTemplate("code-investigate", def); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := s.GetTemplate("code-investigate", 0)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Def.Receiver != "mimo" || got.Def.Mode != TemplateModeReadonly || got.Def.Target != TemplateTargetHere {
		t.Fatalf("往返丢字段: %+v", got.Def)
	}
	if !got.Def.IsSkilled() {
		t.Fatal("带 receiver/mode 的定义应判为熟练工定义")
	}
}

// TestB452LegacyTemplateDefBytesUnchanged：旧定义（无两新键）解出后两字段为空，重新编码的字节与
// 基线结构的编码逐字一致——`template show` 输出旧模板时不多出任何键。
func TestB452LegacyTemplateDefBytesUnchanged(t *testing.T) {
	legacy := `{"executor":"codex","target":"","purpose":"implement","branch_prefix":"cards","prompt":"x {{CARD}}","discipline":"implement"}`
	var def TemplateDef
	if err := json.Unmarshal([]byte(legacy), &def); err != nil {
		t.Fatal(err)
	}
	if def.Mode != "" || def.Receiver != "" || def.IsSkilled() {
		t.Fatalf("旧定义不应带熟练工字段: %+v", def)
	}
	raw, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != legacy {
		t.Fatalf("旧定义重新编码字节变了:\n got %s\nwant %s", raw, legacy)
	}
}

// TestB452TemplateDefValidate：put 校验规则表（plan Q10）。
func TestB452TemplateDefValidate(t *testing.T) {
	cases := []struct {
		name    string
		def     TemplateDef
		wantErr string // 空 = 应通过
	}{
		{"老式分支模板", TemplateDef{Executor: "codex", Prompt: "p", Discipline: "implement"}, ""},
		{"mode 非法值点名合法值", TemplateDef{Executor: "codex", Prompt: "p", Discipline: "implement", Mode: "ro"},
			`"branch"`},
		{"readonly 纪律可空", TemplateDef{Receiver: "mimo", Prompt: "p", Mode: TemplateModeReadonly}, ""},
		{"branch 纪律空仍拒", TemplateDef{Executor: "codex", Prompt: "p", Mode: TemplateModeBranch}, "discipline"},
		{"缺省形态纪律空仍拒", TemplateDef{Executor: "codex", Prompt: "p"}, "discipline"},
		{"有 receiver 时 executor 可空", TemplateDef{Receiver: "mimo", Prompt: "p", Discipline: "implement"}, ""},
		{"无 receiver 时 executor 必填", TemplateDef{Prompt: "p", Discipline: "implement"}, "executor"},
		{"prompt 必填", TemplateDef{Receiver: "mimo", Mode: TemplateModeReadonly}, "prompt"},
		{"target=here 合法", TemplateDef{Receiver: "mimo", Prompt: "p", Mode: TemplateModeReadonly,
			Target: TemplateTargetHere}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.def.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("应通过，得 %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("应拒且含 %q，得 %v", tc.wantErr, err)
			}
		})
	}
	err := TemplateDef{Executor: "codex", Prompt: "p", Discipline: "implement", Mode: "ro"}.Validate()
	if err == nil || !strings.Contains(err.Error(), `"readonly"`) || !strings.Contains(err.Error(), `"ro"`) {
		t.Fatalf("非法 mode 错误应点名非法值与全部合法值，得 %v", err)
	}
}

// TestB452IsSkilledTargetHere：只有 target=here 也算熟练工定义（防 here 流进卡派发 ResolveTarget）。
func TestB452IsSkilledTargetHere(t *testing.T) {
	if !(TemplateDef{Executor: "codex", Prompt: "p", Discipline: "implement", Target: TemplateTargetHere}).IsSkilled() {
		t.Fatal("target=here 应判为熟练工定义")
	}
	if (TemplateDef{Executor: "codex", Prompt: "p", Discipline: "implement", Target: "linux-01"}).IsSkilled() {
		t.Fatal("普通机器名 target 不应判为熟练工定义")
	}
}
