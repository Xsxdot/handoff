package codegraph

import (
	"strings"
	"testing"
)

func TestLoadTarget(t *testing.T) {
	tg, err := LoadTarget("testdata/repo")
	if err != nil {
		t.Fatalf("加载目标图: %v", err)
	}
	if tg.Meta.Version != 1 || len(tg.Domains) == 0 {
		t.Fatalf("meta/domains 解析不对: %+v", tg.Meta)
	}
}

// 缺失必须是显式错误——check 无基准静默通过是本机制的头号静默失败模式（spec §5）。
func TestLoadTargetMissingIsError(t *testing.T) {
	if _, err := LoadTarget(t.TempDir()); err == nil {
		t.Fatal("target 缺失应报错，不能返回 nil,nil")
	}
}

func TestValidateTarget(t *testing.T) {
	bad := &Target{
		Meta: TargetMeta{Version: 1},
		Domains: []TargetDomain{
			{ID: "d_a", Name: "A", Type: "logic", Paths: []string{"pkg/**"}},
			{ID: "d_a", Name: "重复", Type: "magic", Paths: []string{"[bad"}},
		},
		Assignments: []Assignment{{Path: "x.go", Domain: "d_nope"}},
		Contracts:   []Contract{{From: "d_a", To: "d_nope", LegacyBudget: -1}},
	}
	issues := ValidateTarget(bad)
	for _, want := range []string{"重复", "type", "paths", "d_nope", "legacyBudget"} {
		found := false
		for _, is := range issues {
			if strings.Contains(is, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("缺少对 %q 的校验报告，实际: %v", want, issues)
		}
	}
}

// legacyBudget 缺省与 0 同义 = 硬拦（spec §4 钉死的语义）。
func TestContractBudgetDefaultZero(t *testing.T) {
	var c Contract
	if c.LegacyBudget != 0 {
		t.Fatal("缺省预算必须是 0（硬拦）")
	}
}

func TestDomainOf(t *testing.T) {
	tg := &Target{
		Domains: []TargetDomain{
			{ID: "d_svc", Type: "logic", Paths: []string{"svc/**"}},
			{ID: "d_cmd", Type: "logic", Paths: []string{"cmd/run.go"}},
		},
		Assignments: []Assignment{{Path: "svc/mirror.go", Domain: "d_cmd"}},
	}
	cases := []struct{ file, want string }{
		{"svc/task.go", "d_svc"},   // 前缀规则
		{"svc/mirror.go", "d_cmd"}, // assignments 优先于 paths
		{"cmd/run.go", "d_cmd"},    // 精确规则
		{"web/x.ts", ""},           // 图外
		{"svcx/task.go", ""},       // 前缀必须整段匹配，svcx 不是 svc/
	}
	for _, c := range cases {
		if got := tg.DomainOf(c.file); got != c.want {
			t.Errorf("DomainOf(%q) = %q, want %q", c.file, got, c.want)
		}
	}
}
