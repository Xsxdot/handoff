package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCursorNamespaceFoldsAddressForms(t *testing.T) {
	cases := []struct{ in, want string }{
		{"http://127.0.0.1:7777", "127.0.0.1_7777"},
		{"http://100.73.238.21:7777", "100.73.238.21_7777"},
		{"https://box.example.com:8443", "box.example.com_8443"},
		{"127.0.0.1:7777", "127.0.0.1_7777"}, // 无 scheme 也要折到同一个篓
		{"", "unknown"},
	}
	for _, c := range cases {
		if got := cursorNamespace(c.in); got != c.want {
			t.Fatalf("cursorNamespace(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCursorRootPrefersHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c := New("http://127.0.0.1:7777", "")
	got, err := c.cursorRootDir()
	if err != nil {
		t.Fatalf("cursorRootDir: %v", err)
	}
	want := filepath.Join(home, ".handoff", "cursors")
	if got != want {
		t.Fatalf("根 = %q, want %q", got, want)
	}
}

func TestCursorRootFallsBackToCwdWhenHomeUnwritable(t *testing.T) {
	home := t.TempDir()
	// 造一个不可写的 ~/.handoff：先建目录再摘掉写权限，
	// 这样 MkdirAll 成功而 CreateTemp 失败——正是沙箱里的形状
	if err := os.MkdirAll(filepath.Join(home, ".handoff"), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	cwd := t.TempDir()
	t.Chdir(cwd)

	c := New("http://127.0.0.1:7777", "")
	got, err := c.cursorRootDir()
	if err != nil {
		t.Fatalf("cursorRootDir: %v", err)
	}
	want := filepath.Join(cwd, ".handoff", "cursors")
	if got != want {
		t.Fatalf("根 = %q, want %q（应降级到 cwd）", got, want)
	}
}

func TestCursorRootResolvesOnlyOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c := New("http://127.0.0.1:7777", "")
	first, err := c.cursorRootDir()
	if err != nil {
		t.Fatal(err)
	}
	// 解析后把 HOME 换掉：缓存生效的话第二次必须仍返回第一次的值
	t.Setenv("HOME", t.TempDir())
	second, err := c.cursorRootDir()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("游标根被重复解析：first=%q second=%q", first, second)
	}
}

func TestCursorRootErrorNamesBothPaths(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".handoff"), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".handoff"), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	c := New("http://127.0.0.1:7777", "")
	_, err := c.cursorRootDir()
	if err == nil {
		t.Fatal("两处都不可写时必须报错，不得静默")
	}
	msg := err.Error()
	if !strings.Contains(msg, home) || !strings.Contains(msg, cwd) {
		t.Fatalf("错误必须同时点名两个候选路径，实际: %s", msg)
	}
}
