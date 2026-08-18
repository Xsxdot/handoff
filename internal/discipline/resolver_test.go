package discipline

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestForUnconfiguredUsesBuiltinByTier(t *testing.T) {
	r := NewResolver(t.TempDir(), nil, quietLog())
	for _, c := range []struct{ exec, wantSource, wantSubstr string }{
		{"opencode", "内置:" + TierSubagent, "用你自己的 subagent 机制"},
		{"claude", "内置:" + TierSubagent, "用你自己的 subagent 机制"},
		{"codex", "内置:" + TierSingleContext, "在本会话内自己逐 task 实现"},
		{"grok", "内置:" + TierSingleContext, "在本会话内自己逐 task 实现"},
	} {
		b, err := r.For(c.exec)
		if err != nil {
			t.Fatalf("%s: 意外错误 %v", c.exec, err)
		}
		if b.Source != c.wantSource {
			t.Errorf("%s: Source = %q, want %q", c.exec, b.Source, c.wantSource)
		}
		if !strings.Contains(b.Text, c.wantSubstr) {
			t.Errorf("%s: 正文未含 %q", c.exec, c.wantSubstr)
		}
	}
}

func TestForUnknownExecutorFallsBackToSingleContext(t *testing.T) {
	r := NewResolver(t.TempDir(), nil, quietLog())
	b, err := r.For("某个还没写适配器的执行器")
	if err != nil {
		t.Fatalf("意外错误 %v", err)
	}
	if b.Source != "内置:"+TierSingleContext {
		t.Fatalf("Source = %q，未登记的必须落单上下文版", b.Source)
	}
}

func TestForConfiguredReadsFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mine.md"), []byte("我自己的纪律"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewResolver(dir, map[string]string{"codex": "mine.md"}, quietLog())
	b, err := r.For("codex")
	if err != nil {
		t.Fatalf("意外错误 %v", err)
	}
	if b.Text != "我自己的纪律" {
		t.Errorf("Text = %q", b.Text)
	}
	if b.Source != "配置:mine.md" {
		t.Errorf("Source = %q, want 配置:mine.md", b.Source)
	}
}

func TestForEmptyValueDisablesInjection(t *testing.T) {
	r := NewResolver(t.TempDir(), map[string]string{"codex": "  "}, quietLog())
	b, err := r.For("codex")
	if err != nil {
		t.Fatalf("意外错误 %v", err)
	}
	if b.Text != "" || b.Source != "" {
		t.Fatalf("显式关闭却拿到了纪律块：%+v", b)
	}
}

func TestForRejectsPathSeparator(t *testing.T) {
	for _, bad := range []string{"../etc/passwd", "sub/dir.md", "."} {
		r := NewResolver(t.TempDir(), map[string]string{"codex": bad}, quietLog())
		if _, err := r.For("codex"); err == nil {
			t.Errorf("%q 应被拒", bad)
		}
	}
}

func TestForMissingFileErrors(t *testing.T) {
	r := NewResolver(t.TempDir(), map[string]string{"codex": "nope.md"}, quietLog())
	if _, err := r.For("codex"); err == nil {
		t.Fatal("文件缺失应报错")
	}
}

func TestPreflightDoesNotPanicOnBadConfig(t *testing.T) {
	r := NewResolver(t.TempDir(), map[string]string{"codex": "nope.md", "grok": "../x"}, quietLog())
	r.Preflight()
}
