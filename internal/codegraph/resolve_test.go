package codegraph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveAnchorInGraph(t *testing.T) {
	v, repo := loadFixtureView(t)
	r, err := ResolveAnchor(v, repo, "svc/server.go#Do")
	if err != nil {
		t.Fatal(err)
	}
	if r.NodeID != "n_do" || r.Anchor != "ok" || r.Line != 4 || r.File != "svc/server.go" {
		t.Fatalf("图内锚定: %+v", r)
	}
}

func TestResolveAnchorOutOfGraph(t *testing.T) {
	repo := writeAnchorFile(t, "package sample", "", "func Moved() {}", "func Other() {}")
	v := &View{Nodes: map[string]ViewNode{}}
	r, err := ResolveAnchor(v, repo, "anchor.go#Moved")
	if err != nil {
		t.Fatal(err)
	}
	if r.Anchor != "moved" || r.Line != 3 || r.NodeID != "" {
		t.Fatalf("图外锚定: %+v", r)
	}
	r, err = ResolveAnchor(v, repo, "anchor.go#Gone")
	if err != nil {
		t.Fatal(err)
	}
	if r.Anchor != "vanished" || r.Line != 0 {
		t.Fatalf("消失锚定: %+v", r)
	}
}

func TestCheckDocAnchors(t *testing.T) {
	repo := writeAnchorFile(t, "package sample", "func Good() {}", "func AlsoGood() {}")
	doc := "doc.md"
	text := "`anchor.go#Good` `anchor.go#AlsoGood` `anchor.go#Gone`"
	if err := os.WriteFile(filepath.Join(repo, doc), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	results, err := CheckDocAnchors(&View{Nodes: map[string]ViewNode{}}, repo, doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("锚点数量: %d (%+v)", len(results), results)
	}
	if results[0].Ref != "anchor.go#Good" || results[1].Anchor != "moved" || results[2].Anchor != "vanished" {
		t.Fatalf("锚点结果: %+v", results)
	}
}

func TestCheckDocAnchorsDeduplicates(t *testing.T) {
	repo := writeAnchorFile(t, "package sample", "func Good() {}")
	if err := os.WriteFile(filepath.Join(repo, "doc.md"), []byte("`anchor.go#Good` `anchor.go#Good`"), 0o644); err != nil {
		t.Fatal(err)
	}
	results, err := CheckDocAnchors(&View{Nodes: map[string]ViewNode{}}, repo, "doc.md")
	if err != nil || len(results) != 1 {
		t.Fatalf("锚点去重: results=%+v err=%v", results, err)
	}
}

func TestResolveAnchorMissingFile(t *testing.T) {
	r, err := ResolveAnchor(&View{Nodes: map[string]ViewNode{}}, t.TempDir(), "missing.go#Gone")
	if err != nil {
		t.Fatal(err)
	}
	if r.Anchor != "file_missing" || !strings.Contains(r.Ref, "missing.go#Gone") {
		t.Fatalf("缺文件锚定: %+v", r)
	}
}
