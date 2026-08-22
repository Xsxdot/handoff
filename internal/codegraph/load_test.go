// codegraph 加载层测试：夹具仓库读取、diffs 目录发现、坏 JSON 报错带路径。
package codegraph

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadGraph(t *testing.T) {
	g, err := LoadGraph(filepath.Join("testdata", "repo"))
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	if g.Meta.Project != "demo" || len(g.Nodes) != 8 || len(g.Edges) != 4 {
		t.Fatalf("解析形状不对: meta=%+v nodes=%d edges=%d", g.Meta, len(g.Nodes), len(g.Edges))
	}
	if !g.Nodes["e_skip"].Unscanned {
		t.Fatal("unscanned 标丢失")
	}
}

func TestLoadGraphMissing(t *testing.T) {
	if _, err := LoadGraph(t.TempDir()); err == nil {
		t.Fatal("无 codegraph/baseline.json 应当报错")
	}
}

func TestListViewsAndLoadDiff(t *testing.T) {
	views, err := ListViews(filepath.Join("testdata", "repo"))
	if err != nil || len(views) != 1 || views[0] != "branch-x" {
		t.Fatalf("ListViews: %v %v", views, err)
	}
	d, err := LoadDiff(filepath.Join("testdata", "repo"), "branch-x")
	if err != nil {
		t.Fatalf("LoadDiff: %v", err)
	}
	if d.View != "branch:x" || len(d.NodesAdded) != 1 || len(d.NodesDeleted) != 1 {
		t.Fatalf("diff 形状不对: %+v", d)
	}
	if d.NodesModified["n_do"].SignatureOld == "" {
		t.Fatal("signatureOld 丢失")
	}
}

func TestListViewsEmptyDir(t *testing.T) {
	// 没有 diffs 目录不是错误：返回空列表（大多数仓库只有基线）
	dir := t.TempDir()
	writeFixtureBaseline(t, dir)
	views, err := ListViews(dir)
	if err != nil || len(views) != 0 {
		t.Fatalf("空 diffs 应返回空列表: %v %v", views, err)
	}
}

func writeFixtureBaseline(t *testing.T, dir string) {
	t.Helper()
	graphDir := filepath.Join(dir, "codegraph")
	if err := os.MkdirAll(graphDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(graphDir, "baseline.json"), []byte(`{"meta":{},"containers":{},"nodes":{},"edges":[],"diffs":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
}
