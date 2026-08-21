package codegraph

import (
	"path/filepath"
	"testing"
)

func TestMergeBaselineOnly(t *testing.T) {
	v := Merge(loadFixture(t), nil)
	if v.Name != "baseline" || len(v.Nodes) != 7 || len(v.Edges) != 4 {
		t.Fatalf("基准视图形状: %s %d %d", v.Name, len(v.Nodes), len(v.Edges))
	}
	if v.Nodes["n_do"].Status != "" {
		t.Fatal("基准视图不应有 status")
	}
}

func TestMergeWithDiff(t *testing.T) {
	g := loadFixture(t)
	d, _ := LoadDiff(filepath.Join("testdata", "repo"), "branch-x")
	v := Merge(g, d)
	if v.Name != "branch:x" {
		t.Fatalf("视图名取 diff.view: %s", v.Name)
	}
	if v.Nodes["n_audit"].Status != "added" || v.Nodes["n_do"].Status != "modified" ||
		v.Nodes["n_save"].Status != "deleted" {
		t.Fatalf("节点状态: %+v", v.Nodes)
	}
	// 修改后的节点内容替换为 diff 里的版本，且带 signatureOld
	if v.Nodes["n_do"].SignatureOld == "" || v.Nodes["n_do"].Signature ==
		g.Nodes["n_do"].Signature {
		t.Fatal("modified 节点应替换为新签名并携带旧签名")
	}
	// 删除的节点保留在视图里（status=deleted），供渲染红虚线，不是直接消失
	st := map[string]string{}
	for _, e := range v.Edges {
		st[e.From+"→"+e.To] = e.Status
	}
	if st["n_do→n_audit"] != "added" || st["n_do→n_save"] != "deleted" {
		t.Fatalf("边状态: %v", st)
	}
}

func TestMergeSkipsInvalidAddedEdges(t *testing.T) {
	g := loadFixture(t)
	v := Merge(g, &Diff{EdgesAdded: []Edge{{"n_do", "n_ghost"}}})
	if len(v.Edges) != len(g.Edges) {
		t.Fatalf("非法新增边不应进入视图: %+v", v.Edges)
	}
}

// implements 边必须穿过 LoadGraph→LoadDiff→Merge 全链出现在视图里。
// 只测内存构造会漏掉 json tag 拼写错这类 wire 缺陷（ChildrenTotal 前科）。
func TestMergeImplementsThroughWire(t *testing.T) {
	g, err := LoadGraph("testdata/repo")
	if err != nil {
		t.Fatalf("加载基线: %v", err)
	}
	if len(g.Implements) == 0 {
		t.Fatal("夹具基线应含 implements 边")
	}
	d, err := LoadDiff("testdata/repo", "branch-x")
	if err != nil {
		t.Fatalf("加载 diff: %v", err)
	}
	v := Merge(g, d)
	var added, kept int
	for _, e := range v.Implements {
		switch e.Status {
		case "added":
			added++
		case "":
			kept++
		}
	}
	if kept == 0 || added == 0 {
		t.Fatalf("视图 implements 合并不对: kept=%d added=%d", kept, added)
	}
}
