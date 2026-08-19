package codegraph

import (
	"path/filepath"
	"testing"
)

func TestMergeBaselineOnly(t *testing.T) {
	v := Merge(loadFixture(t), nil)
	if v.Name != "baseline" || len(v.Nodes) != 6 || len(v.Edges) != 4 {
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
