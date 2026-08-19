package codegraph

import (
	"path/filepath"
	"strings"
	"testing"
)

func loadFixture(t *testing.T) *Graph {
	t.Helper()
	g, err := LoadGraph(filepath.Join("testdata", "repo"))
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestValidateCleanFixture(t *testing.T) {
	if issues := Validate(loadFixture(t)); len(issues) != 0 {
		t.Fatalf("夹具应当干净: %v", issues)
	}
}

func TestValidateCatchesBrokenRefs(t *testing.T) {
	g := loadFixture(t)
	n := g.Nodes["n_do"]
	n.Container = "k_ghost"
	g.Nodes["n_do"] = n
	g.Edges = append(g.Edges, Edge{"n_do", "n_ghost"})
	issues := Validate(g)
	if len(issues) != 2 {
		t.Fatalf("应报 2 条: %v", issues)
	}
	// 报文必须带引用者 id，否则修数据要靠猜
	if !strings.Contains(issues[0], "n_do") || !strings.Contains(issues[1], "n_ghost") {
		t.Fatalf("报文缺上下文: %v", issues)
	}
}

func TestValidateDiff(t *testing.T) {
	g := loadFixture(t)
	d, _ := LoadDiff(filepath.Join("testdata", "repo"), "branch-x")
	if issues := ValidateDiff(g, d); len(issues) != 0 {
		t.Fatalf("夹具 diff 应当干净: %v", issues)
	}
	d.NodesDeleted = append(d.NodesDeleted, "n_ghost") // 删除不存在的节点
	d.EdgesAdded = append(d.EdgesAdded, Edge{"n_audit", "n_ghost"})
	if issues := ValidateDiff(g, d); len(issues) != 2 {
		t.Fatalf("应报 2 条: %v", issues)
	}
}
