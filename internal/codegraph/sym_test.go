package codegraph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadFixtureView(t *testing.T) (*View, string) {
	t.Helper()
	repo := filepath.Join("testdata", "repo")
	g, err := LoadGraph(repo)
	if err != nil {
		t.Fatal(err)
	}
	return Merge(g, nil), repo
}

func TestSymLookupTailMatch(t *testing.T) {
	v, repo := loadFixtureView(t)
	r, err := SymLookup(v, repo, "Do")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Matches) != 1 {
		t.Fatalf("matches=%d, want 1", len(r.Matches))
	}
	m := r.Matches[0]
	if m.ID != "n_do" || m.Anchor != "ok" || m.Line != 4 || m.Domain != "d_svc/api" || m.Signature == "" {
		t.Fatalf("match=%+v", m)
	}
}

func TestSymLookupExactName(t *testing.T) {
	v, repo := loadFixtureView(t)
	r, err := SymLookup(v, repo, "Server.Do")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Matches) != 1 || r.Matches[0].ID != "n_do" {
		t.Fatalf("matches=%+v", r.Matches)
	}
}

func TestSymLookupByID(t *testing.T) {
	v, repo := loadFixtureView(t)
	r, err := SymLookup(v, repo, "n_do")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Matches) != 1 || r.Matches[0].ID != "n_do" {
		t.Fatalf("matches=%+v", r.Matches)
	}
}

func TestSymLookupAmbiguousTail(t *testing.T) {
	v := &View{
		Name: "baseline",
		Nodes: map[string]ViewNode{
			"z_close": {Node: Node{Name: "A.Close"}},
			"a_close": {Node: Node{Name: "B.Close"}},
		},
	}
	r, err := SymLookup(v, t.TempDir(), "Close")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Matches) != 2 || r.Matches[0].ID != "a_close" || r.Matches[1].ID != "z_close" {
		t.Fatalf("matches=%+v", r.Matches)
	}
}

func TestSymLookupMiss(t *testing.T) {
	v, repo := loadFixtureView(t)
	_, err := SymLookup(v, repo, "Nope")
	if err == nil || !strings.Contains(err.Error(), "图未覆盖") || !strings.Contains(err.Error(), "近似候选") {
		t.Fatalf("err=%v", err)
	}
}

func TestSymLookupUnscanned(t *testing.T) {
	v, repo := loadFixtureView(t)
	r, err := SymLookup(v, repo, "demo skip")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Matches) != 1 || r.Matches[0].Anchor != "unscanned" {
		t.Fatalf("matches=%+v", r.Matches)
	}
}

func writeAnchorFile(t *testing.T, lines ...string) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "anchor.go"), []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestReAnchorOK(t *testing.T) {
	repo := writeAnchorFile(t, "one", "two", "func Token() {}", "four", "five")
	line, status := ReAnchor(repo, Node{Kind: "func", Name: "Token", File: "anchor.go", Line: 3})
	if line != 3 || status != "ok" {
		t.Fatalf("got (%d, %q)", line, status)
	}
}

func TestReAnchorMoved(t *testing.T) {
	repo := writeAnchorFile(t, "one", "two", "func Token() {}", "four", "five")
	line, status := ReAnchor(repo, Node{Kind: "func", Name: "Token", File: "anchor.go", Line: 1})
	if line != 3 || status != "moved" {
		t.Fatalf("got (%d, %q)", line, status)
	}
}

func TestReAnchorMovedPrefersDefinition(t *testing.T) {
	repo := writeAnchorFile(t, "// Token is mentioned", "one", "Token appears here", "func Token() {}", "five", "six")
	line, status := ReAnchor(repo, Node{Kind: "func", Name: "Token", File: "anchor.go", Line: 6})
	if line != 4 || status != "moved" {
		t.Fatalf("got (%d, %q)", line, status)
	}
}

func TestReAnchorVanished(t *testing.T) {
	repo := writeAnchorFile(t, "one", "two", "three")
	line, status := ReAnchor(repo, Node{Kind: "func", Name: "Token", File: "anchor.go", Line: 2})
	if line != 2 || status != "vanished" {
		t.Fatalf("got (%d, %q)", line, status)
	}
}

func TestReAnchorFileMissing(t *testing.T) {
	line, status := ReAnchor(t.TempDir(), Node{Kind: "func", Name: "Token", File: "missing.go", Line: 7})
	if line != 7 || status != "file_missing" {
		t.Fatalf("got (%d, %q)", line, status)
	}
}

func TestReAnchorWordBoundary(t *testing.T) {
	repo := writeAnchorFile(t, "Done")
	line, status := ReAnchor(repo, Node{Kind: "func", Name: "Do", File: "anchor.go", Line: 1})
	if line != 1 || status != "vanished" {
		t.Fatalf("got (%d, %q)", line, status)
	}
}
