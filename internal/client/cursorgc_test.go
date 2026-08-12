package client

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDropCursorIsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c := New("http://127.0.0.1:7777", "")
	if err := c.writeCursor("t1", 5); err != nil {
		t.Fatal(err)
	}
	c.DropCursor("t1")
	c.DropCursor("t1") // 第二次不得 panic、不得报错
	if got := c.readCursor("t1"); got != 0 {
		t.Fatalf("游标应已删除，got %d", got)
	}
}

func TestSweepCursorsRemovesOnlyExpired(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c := New("http://127.0.0.1:7777", "")
	if err := c.writeCursor("fresh", 1); err != nil {
		t.Fatal(err)
	}
	if err := c.writeCursor("stale", 2); err != nil {
		t.Fatal(err)
	}
	stalePath, err := c.cursorPath("stale")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-cursorTTL - time.Hour)
	if err := os.Chtimes(stalePath, old, old); err != nil {
		t.Fatal(err)
	}

	c.sweepCursors()

	if got := c.readCursor("fresh"); got != 1 {
		t.Fatalf("未超期游标被误删，got %d", got)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("超期游标未被清掉: %v", err)
	}
}

func TestPurgeLegacyFlatCursors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	legacyDir := filepath.Join(home, ".handoff")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(legacyDir, "cursor-old-task")
	if err := os.WriteFile(legacy, []byte("7"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyTmp := filepath.Join(legacyDir, "cursor-old-task-123.tmp")
	if err := os.WriteFile(legacyTmp, []byte("7"), 0o600); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(legacyDir, "config.yaml")
	if err := os.WriteFile(keep, []byte("listen: x"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := New("http://127.0.0.1:7777", "")
	c.purgeLegacyFlatCursors()

	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("旧平铺游标未被清除")
	}
	if _, err := os.Stat(legacyTmp); !os.IsNotExist(err) {
		t.Fatal("旧平铺临时文件未被清除")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("非游标文件被误删: %v", err)
	}
}
