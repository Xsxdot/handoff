package cmd

import (
	"testing"

	"github.com/Xsxdot/charter/graph/codegraph"
)

// 本测试是 handoff 仓库自身的契约闸：真实 baseline 套真实 target。
// 它转红的含义不是「测试坏了」，是「出现了未声明的跨域依赖」——
// 处置是改走契约面或走契约变更调 target，不是改测试（spec §8）。
func TestRepoContractGate(t *testing.T) {
	tg, err := codegraph.LoadTarget("..")
	if err != nil {
		t.Fatalf("仓库 target.json 不可用: %v", err)
	}
	if issues := codegraph.ValidateTarget(tg); len(issues) > 0 {
		t.Fatalf("target 不合法: %v", issues)
	}
	g, err := codegraph.LoadGraph("..")
	if err != nil {
		t.Fatalf("加载仓库基线: %v", err)
	}
	rep := codegraph.Check(tg, codegraph.Merge(g, nil))
	for _, f := range rep.Fails {
		t.Errorf("契约违规 [%s] %s", f.Kind, f.Detail)
	}
	t.Logf("legacy 命中: %v，warn %d 条", rep.LegacyHits, len(rep.Warns))
}
