package ledger

import (
	"strings"
	"testing"
)

func TestTemplateVersioningAndDefaults(t *testing.T) {
	s := newTestStore(t)
	if err := s.EnsureDefaultTemplates(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tp, err := s.GetTemplate("feature-impl", 0)
	if err != nil || tp.Version != 1 {
		t.Fatalf("feature-impl: %v %+v", err, tp)
	}
	if tp.Def.Executor != "opencode" || tp.Def.DisciplinePath == "" {
		t.Fatalf("默认模板字段: %+v", tp.Def)
	}
	rv, err := s.GetTemplate("review-generic", 0)
	if err != nil {
		t.Fatalf("review-generic: %v", err)
	}
	if !strings.Contains(rv.Def.Prompt, "handoff-verdict") {
		t.Fatalf("审阅模板缺输出契约: %q", rv.Def.Prompt)
	}
	if err := s.EnsureDefaultTemplates(); err != nil {
		t.Fatal(err)
	}
	if tp2, _ := s.GetTemplate("feature-impl", 0); tp2.Version != 1 {
		t.Fatalf("seed 不幂等: v%d", tp2.Version)
	}
	def := tp.Def
	def.Executor = "codex"
	v, err := s.PutTemplate("feature-impl", def)
	if err != nil || v != 2 {
		t.Fatalf("put v2: %d %v", v, err)
	}
	if old, _ := s.GetTemplate("feature-impl", 1); old.Def.Executor != "opencode" {
		t.Fatalf("v1 被改: %+v", old.Def)
	}
	def.ModelByTarget = map[string]string{"mac-02": "gpt-5.6-luna", "win-b37": "deepseek-v4-pro"}
	if _, err := s.PutTemplate("feature-impl", def); err != nil {
		t.Fatalf("model override: %v", err)
	}
	tp3, _ := s.GetTemplate("feature-impl", 0)
	if tp3.Def.ModelByTarget["mac-02"] != "gpt-5.6-luna" {
		t.Fatalf("覆盖丢失: %+v", tp3.Def)
	}
}
