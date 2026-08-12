package rawtap

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.NewFile(0, os.DevNull), nil))
}

func TestOpenReturnsNilWhenDisabled(t *testing.T) {
	t.Setenv(EnvDir, "")
	if tap := Open("opencode", "task-1", discard()); tap != nil {
		t.Fatal("未设置环境变量时必须完全关闭，返回 nil")
	}
}

func TestNilTapIsSafe(t *testing.T) {
	var tap *Tap
	tap.Write([]byte("x")) // 不得 panic
	tap.Close()
}

func TestWriteAppendsOneLinePerCall(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)
	tap := Open("grok", "task-2", discard())
	if tap == nil {
		t.Fatal("设置了环境变量却没开启")
	}
	tap.Write([]byte(`{"a":1}`))
	tap.Write([]byte("second line\nwith embedded newline"))
	tap.Close()

	b, err := os.ReadFile(filepath.Join(dir, "grok-task-2.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("应为 2 行，实际 %d 行: %q", len(lines), string(b))
	}
	if lines[0] != `{"a":1}` {
		t.Fatalf("第一行被改写: %q", lines[0])
	}
	// 内嵌换行必须被转义，否则一条上游消息会在样本里裂成两条，回放时对不上
	if strings.Contains(lines[1], "\nwith") {
		t.Fatalf("内嵌换行未转义: %q", lines[1])
	}
	if !strings.Contains(lines[1], `\n`) {
		t.Fatalf("内嵌换行应转义成 \\n: %q", lines[1])
	}
}

func TestWriteIsConcurrencySafe(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)
	tap := Open("codex", "task-3", discard())
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tap.Write([]byte("line"))
		}()
	}
	wg.Wait()
	tap.Close()

	b, err := os.ReadFile(filepath.Join(dir, "codex-task-3.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(b), "\n"); got != 50 {
		t.Fatalf("并发写丢行或串行：应 50 行，实际 %d", got)
	}
}

func TestOpenFailureDegradesToNil(t *testing.T) {
	// 指到一个「已被文件占位」的路径：MkdirAll 必失败
	dir := t.TempDir()
	occupied := filepath.Join(dir, "occupied")
	if err := os.WriteFile(occupied, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvDir, occupied)
	if tap := Open("opencode", "task-4", discard()); tap != nil {
		t.Fatal("目录不可用时必须降级为 nil，不得让诊断开关拖垮执行")
	}
}
