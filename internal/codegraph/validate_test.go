package codegraph

import (
	"path/filepath"
	"reflect"
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

func TestValidateDomains(t *testing.T) {
	g := &Graph{
		Domains: map[string]Domain{
			"d_svc":     {Label: "svc", Kind: "服务端", Summary: "服务"},
			"d_svc/api": {Label: "api", Kind: "接口层", Summary: "路由", Parent: "d_svc"},
			"d_ghosted": {Label: "孤儿", Kind: "x", Parent: "d_nope"},
		},
		Containers: map[string]Container{
			"k_api":  {Label: "svc.Server", Kind: "服务端", Domain: "d_svc/api"},
			"k_core": {Label: "svc.Manager", Kind: "核心", Domain: "d_svc"},
			"k_lost": {Label: "svc.Store", Kind: "存储", Domain: "d_ghost"},
			"k_none": {Label: "svc.Util", Kind: "工具"},
		},
		Nodes: map[string]Node{},
		Edges: []Edge{},
	}
	want := []string{
		"容器 k_core 挂在非叶子领域 d_svc（容器只能挂叶子领域）",
		"容器 k_lost 引用不存在的领域 d_ghost",
		"容器 k_none 未归属领域（domains 非空时每个容器都必须有 domain）",
		"领域 d_ghosted 的 parent d_nope 不存在",
	}
	if got := Validate(g); !reflect.DeepEqual(got, want) {
		t.Fatalf("领域校验:\n got=%q\nwant=%q", got, want)
	}
}

func TestValidateDomainParentCycle(t *testing.T) {
	g := &Graph{
		Domains: map[string]Domain{
			"d_a": {Label: "a", Kind: "x", Parent: "d_b"},
			"d_b": {Label: "b", Kind: "x", Parent: "d_a"},
		},
		Containers: map[string]Container{},
		Nodes:      map[string]Node{},
	}
	got := Validate(g)
	if len(got) != 2 || !strings.Contains(got[0], "父链存在环") {
		t.Fatalf("父链成环应逐个领域报出: %q", got)
	}
}

func TestValidateNoDomainsSectionIsClean(t *testing.T) {
	// 旧扫描数据没有 domains 段：整段校验跳过，不得因此报问题
	g := &Graph{
		Containers: map[string]Container{"k_svc": {Label: "svc", Kind: "服务端"}},
		Nodes:      map[string]Node{},
	}
	if got := Validate(g); len(got) != 0 {
		t.Fatalf("无领域段应零问题: %q", got)
	}
}
