package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/testperm"
)

func TestCursorPathUsesNamespacedLayout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c := New("http://100.73.238.21:7777", "")
	p, err := c.cursorPath("task-abc")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".handoff", "cursors", "100.73.238.21_7777", "task-abc")
	if p != want {
		t.Fatalf("游标路径 = %q, want %q", p, want)
	}
}

func TestCursorsOfDifferentAgentdDoNotCollide(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	a := New("http://127.0.0.1:7777", "")
	b := New("http://100.73.238.21:7777", "")

	if err := a.writeCursor("same-id", 11); err != nil {
		t.Fatal(err)
	}
	if err := b.writeCursor("same-id", 22); err != nil {
		t.Fatal(err)
	}
	if got := a.readCursor("same-id"); got != 11 {
		t.Fatalf("本机游标被另一台 agentd 覆盖：got %d want 11", got)
	}
	if got := b.readCursor("same-id"); got != 22 {
		t.Fatalf("远端游标读错：got %d want 22", got)
	}
}

func TestReadCursorMissingFileIsSilentFirstRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c := New("http://127.0.0.1:7777", "")
	if got := c.readCursor("never-seen"); got != 0 {
		t.Fatalf("首次必须从 0 开始，got %d", got)
	}
}

func TestReadCursorCorruptContentIsReported(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c := New("http://127.0.0.1:7777", "")
	p, err := c.cursorPath("corrupt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("not-a-number"), 0o600); err != nil {
		t.Fatal(err)
	}
	seq, reported := c.readCursorWithDiag("corrupt")
	if seq != 0 {
		t.Fatalf("损坏内容必须退回 0，got %d", seq)
	}
	if !reported {
		t.Fatal("内容损坏必须被报告，不得与「文件不存在」一样静默")
	}
}

func TestReadCursorPermissionDeniedIsReported(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c := New("http://127.0.0.1:7777", "")
	p, err := c.cursorPath("denied")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("42"), 0o600); err != nil {
		t.Fatal(err)
	}
	testperm.DenyRead(t, p)
	seq, reported := c.readCursorWithDiag("denied")
	if seq != 0 {
		t.Fatalf("读不了必须退回 0，got %d", seq)
	}
	if !reported {
		t.Fatal("权限被拒必须被报告，不得与「文件不存在」一样静默")
	}
}
