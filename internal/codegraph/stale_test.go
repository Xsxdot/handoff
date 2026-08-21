package codegraph

import (
	"path/filepath"
	"testing"
)

func TestCheckStaleCleanFixture(t *testing.T) {
	g := loadFixture(t)
	stale := CheckStale(filepath.Join("testdata", "repo"), g)
	// unscanned 的 e_skip 没有源文件但必须被跳过——夹具整体干净
	if len(stale) != 0 {
		t.Fatalf("夹具应当不 stale: %+v", stale)
	}
}

func TestCheckStaleDetects(t *testing.T) {
	g := loadFixture(t)
	// 场景 1：行号越界
	n := g.Nodes["n_do"]
	n.Line = 999
	g.Nodes["n_do"] = n
	// 场景 2：文件不存在
	n2 := g.Nodes["n_save"]
	n2.File = "svc/gone.go"
	g.Nodes["n_save"] = n2
	// 场景 3：行内容对不上（把 runE 指到注释行）
	n3 := g.Nodes["n_runE"]
	n3.Line = 2
	g.Nodes["n_runE"] = n3
	stale := CheckStale(filepath.Join("testdata", "repo"), g)
	if len(stale) != 3 {
		t.Fatalf("应报 3 条: %+v", stale)
	}
	reasons := map[string]string{}
	for _, s := range stale {
		reasons[s.ID] = s.Reason
	}
	if reasons["n_do"] == "" || reasons["n_save"] == "" || reasons["n_runE"] == "" {
		t.Fatalf("每条都要带原因: %v", reasons)
	}
}
