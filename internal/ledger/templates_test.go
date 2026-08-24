package ledger

import (
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/discipline"
)

func TestTemplateVersioningAndDefaults(t *testing.T) {
	s := newTestStore(t)
	if err := seedTestTemplates(t, s); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tp, err := s.GetTemplate("feature-impl", 0)
	if err != nil || tp.Version != 1 {
		t.Fatalf("feature-impl: %v %+v", err, tp)
	}
	if tp.Def.Executor != "opencode" || tp.Def.Discipline != discipline.NameImplement || tp.Def.DisciplinePath != "" {
		t.Fatalf("默认模板字段: %+v", tp.Def)
	}
	rv, err := s.GetTemplate("review-generic", 0)
	if err != nil {
		t.Fatalf("review-generic: %v", err)
	}
	if !strings.Contains(rv.Def.Prompt, "{{TITLE}}") || !strings.Contains(rv.Def.Prompt, "{{ACCEPT}}") {
		t.Fatalf("审阅测试模板缺少最小变量: %q", rv.Def.Prompt)
	}
	if err := seedTestTemplates(t, s); err != nil {
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

// TestTemplateLegacyDisciplinePathMaps 老模板行用的是 discipline_path，
// 宽松 JSON 解码会把它静默丢掉——必须映射成名字，否则审阅模板会悄悄
// 退回 executor 兜底的实现块（正是本轮要修的缺陷换个方式复活）。
func TestTemplateLegacyDisciplinePathMaps(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{"docs/superpowers/discipline/block-review.md", "review"},
		{"docs/superpowers/discipline/block-a.md", "implement"},
		{"docs/superpowers/discipline/block-b.md", "implement"},
	} {
		got := disciplineNameFromLegacyPath(tc.path)
		if got != tc.want {
			t.Fatalf("路径 %s 应映射为 %q，实得 %q", tc.path, tc.want, got)
		}
	}
}

// TestTemplateLegacyUnknownPathMapsEmpty 认不出来的旧值映射为空（退回兜底），
// 但调用方必须打 Warn——猜不出来可以退，不能不出声。
func TestTemplateLegacyUnknownPathMapsEmpty(t *testing.T) {
	if got := disciplineNameFromLegacyPath("some/custom/block.md"); got != "" {
		t.Fatalf("未知路径应映射为空，实得 %q", got)
	}
}

// TestGetTemplateMapsLegacyRow 存了老字段的行读出来要带上名字。
func TestGetTemplateMapsLegacyRow(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.PutTemplate("legacy-review", TemplateDef{
		Executor: "grok", Purpose: PurposeReview, BranchPrefix: "cards",
		DisciplinePath: "docs/superpowers/discipline/block-review.md",
		Prompt:         "审阅",
	}); err != nil {
		t.Fatalf("PutTemplate: %v", err)
	}
	tpl, err := st.GetTemplate("legacy-review", 0)
	if err != nil {
		t.Fatalf("GetTemplate: %v", err)
	}
	if tpl.Def.Discipline != "review" {
		t.Fatalf("老行应映射出 review，实得 %q", tpl.Def.Discipline)
	}
}

// TestGetTemplateNewFieldWins 新字段非空时不看旧字段。
func TestGetTemplateNewFieldWins(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.PutTemplate("both", TemplateDef{
		Executor: "grok", Purpose: PurposeReview, BranchPrefix: "cards",
		Discipline: "review", DisciplinePath: "docs/superpowers/discipline/block-a.md",
		Prompt: "审阅",
	}); err != nil {
		t.Fatalf("PutTemplate: %v", err)
	}
	tpl, err := st.GetTemplate("both", 0)
	if err != nil {
		t.Fatalf("GetTemplate: %v", err)
	}
	if tpl.Def.Discipline != "review" {
		t.Fatalf("新字段应胜出，实得 %q", tpl.Def.Discipline)
	}
}

// TestTemplateFixturesUseNames 测试夹具使用纪律块名字，不再指向路径。
func TestTemplateFixturesUseNames(t *testing.T) {
	st := newTestStore(t)
	if err := seedTestTemplates(t, st); err != nil {
		t.Fatalf("写测试模板: %v", err)
	}
	for name, want := range map[string]string{
		"feature-impl":   discipline.NameImplement,
		"review-generic": discipline.NameReview,
	} {
		tpl, err := st.GetTemplate(name, 0)
		if err != nil {
			t.Fatalf("GetTemplate(%s): %v", name, err)
		}
		if tpl.Def.Discipline != want {
			t.Fatalf("%s 的纪律块名字应为 %q，实得 %q", name, want, tpl.Def.Discipline)
		}
		if tpl.Def.DisciplinePath != "" {
			t.Fatalf("%s 不该再带旧路径字段，实得 %q", name, tpl.Def.DisciplinePath)
		}
	}
}

// TestDomainTemplateFixtures 分域三模板的夹具形状。purpose 必须互异——
// 分支名由 purpose 拼出，相同会在同一张卡上撞名。变量白名单断言防静默失败：
// prompt 里写了不受支持的 {{X}} 不会报错，会原样送到执行者面前。
func TestDomainTemplateFixtures(t *testing.T) {
	s := newTestStore(t)
	if err := seedTestTemplates(t, s); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct{ discipline, purpose string }{
		"domain-breakdown":   {"spec-draft", "breakdown"},
		"domain-ticket0":     {"implement", "ticket0"},
		"domain-integration": {"implement", "integration"},
	}
	for name, want := range cases {
		tpl, err := s.GetTemplate(name, 0)
		if err != nil {
			t.Fatalf("取 %s: %v", name, err)
		}
		if tpl.Def.Executor != "codex" || tpl.Def.BranchPrefix != "cards" {
			t.Fatalf("%s 执行者/分支前缀不对: %+v", name, tpl.Def)
		}
		if tpl.Def.Discipline != want.discipline || tpl.Def.Purpose != want.purpose {
			t.Fatalf("%s 角色/purpose 不对: 想要 %+v 实得 %s/%s",
				name, want, tpl.Def.Discipline, tpl.Def.Purpose)
		}
		stripped := strings.NewReplacer(
			"{{TITLE}}", "", "{{CARD}}", "", "{{ACCEPT}}", "").Replace(tpl.Def.Prompt)
		if strings.Contains(stripped, "{{") {
			t.Fatalf("%s prompt 含不受支持的模板变量（会原样送出）:\n%s", name, tpl.Def.Prompt)
		}
	}
}
