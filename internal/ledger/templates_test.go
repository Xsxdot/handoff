package ledger

import (
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/discipline"
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
	if tp.Def.Executor != "opencode" || tp.Def.Discipline != discipline.NameImplement || tp.Def.DisciplinePath != "" {
		t.Fatalf("默认模板字段: %+v", tp.Def)
	}
	rv, err := s.GetTemplate("review-generic", 0)
	if err != nil {
		t.Fatalf("review-generic: %v", err)
	}
	if !strings.Contains(rv.Def.Prompt, "handoff-verdict") {
		t.Fatalf("审阅模板缺输出契约: %q", rv.Def.Prompt)
	}
	if !strings.Contains(rv.Def.Prompt, "正文中") || !strings.Contains(rv.Def.Prompt, "<简短摘要>") {
		t.Fatalf("审阅模板未同步正文传输契约: %q", rv.Def.Prompt)
	}
	if strings.Contains(rv.Def.Prompt, "裁决块原文") {
		t.Fatalf("审阅模板仍要求把裁决块塞进 summary: %q", rv.Def.Prompt)
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

// TestVerdictTemplateContractUpgradeCreatesNewVersion 保证契约变更会给未改动的
// 出厂模板追加版本，同时保留旧版本；用户改过同名模板时不应被 seed 覆盖。
func TestVerdictTemplateContractUpgradeCreatesNewVersion(t *testing.T) {
	s := newTestStore(t)
	legacy := map[string]TemplateDef{
		"review-generic": {
			Executor: "grok", Purpose: "review", BranchPrefix: "cards",
			Discipline: discipline.NameReview,
			Prompt: "审阅卡 {{CARD}}（{{TITLE}}）对应分支的完整 diff：spec 符合性（要求全实现、没有多做）+ 代码质量双裁决。\n" +
				"验收判据：{{ACCEPT}}\n" + legacyReviewVerdictContract,
		},
		"domain-ticket0": {
			Executor: "codex", Purpose: "ticket0", BranchPrefix: "cards",
			Discipline: discipline.NameImplement,
			Prompt:     domainTicket0Prompt + legacyImplVerdictContract,
		},
		"domain-integration": {
			Executor: "codex", Purpose: "integration", BranchPrefix: "cards",
			Discipline: discipline.NameImplement,
			Prompt:     domainIntegrationPrompt + legacyImplVerdictContract,
		},
	}
	for name, def := range legacy {
		if version, err := s.PutTemplate(name, def); err != nil || version != 1 {
			t.Fatalf("写入 %s v1: version=%d err=%v", name, version, err)
		}
	}
	if err := s.EnsureDefaultTemplates(); err != nil {
		t.Fatalf("升级默认模板: %v", err)
	}
	for name := range legacy {
		latest, err := s.GetTemplate(name, 0)
		if err != nil {
			t.Fatalf("读取 %s 最新版本: %v", name, err)
		}
		if latest.Version != 2 {
			t.Fatalf("%s 应追加 v2，实得 v%d", name, latest.Version)
		}
		if latest.Def.Prompt == legacy[name].Prompt {
			t.Fatalf("%s v2 仍是旧契约", name)
		}
		old, err := s.GetTemplate(name, 1)
		if err != nil || old.Def.Prompt != legacy[name].Prompt {
			t.Fatalf("%s v1 不应被改写: %+v err=%v", name, old.Def, err)
		}
	}
	if err := s.EnsureDefaultTemplates(); err != nil {
		t.Fatalf("重复 seed: %v", err)
	}
	for name := range legacy {
		latest, _ := s.GetTemplate(name, 0)
		if latest.Version != 2 {
			t.Fatalf("重复 seed 不应追加 %s v%d", name, latest.Version)
		}
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

// TestDefaultTemplatesUseNames 出厂模板用名字，不再指路径。
func TestDefaultTemplatesUseNames(t *testing.T) {
	st := newTestStore(t)
	if err := st.EnsureDefaultTemplates(); err != nil {
		t.Fatalf("EnsureDefaultTemplates: %v", err)
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

// TestDefaultDomainTemplates 分域三模板的 seed 形状。purpose 必须互异——
// 分支名由 purpose 拼出，相同会在同一张卡上撞名。变量白名单断言防静默失败：
// prompt 里写了不受支持的 {{X}} 不会报错，会原样送到执行者面前。
func TestDefaultDomainTemplates(t *testing.T) {
	s := newTestStore(t)
	if err := s.EnsureDefaultTemplates(); err != nil {
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
	// 带 Verdict 节点引用的两个模板必须携带裁决输出契约，否则报文里没有
	// handoff-verdict block，节点永远解析失败转等人。
	for _, name := range []string{"domain-ticket0", "domain-integration"} {
		tpl, _ := s.GetTemplate(name, 0)
		if !strings.Contains(tpl.Def.Prompt, "handoff-verdict") {
			t.Fatalf("%s 缺裁决输出契约", name)
		}
		if !strings.Contains(tpl.Def.Prompt, "之前或之后") {
			t.Fatalf("%s 未同步裁决块位置契约", name)
		}
	}
	for name, want := range map[string]string{
		"domain-breakdown":   "graph check --view",
		"domain-ticket0":     "契约冻结即提交该文件",
		"domain-integration": "棘轮只减不增",
	} {
		tpl, _ := s.GetTemplate(name, 0)
		if !strings.Contains(tpl.Def.Prompt, want) {
			t.Fatalf("%s 缺 graph check 契约片段 %q", name, want)
		}
	}
	// 拆解节点不裁决，prompt 里出现契约会诱导 spec-draft 角色多输出一个假裁决块。
	tpl, _ := s.GetTemplate("domain-breakdown", 0)
	if strings.Contains(tpl.Def.Prompt, "handoff-verdict") {
		t.Fatal("domain-breakdown 不该带裁决契约")
	}
}
