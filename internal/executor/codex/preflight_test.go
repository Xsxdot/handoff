package codex_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor/codex"
)

// swapCodexPathForTest 让 exec.LookPath 视为「codex 在 PATH 上」。
// 测试机上的真实 codex 二进制在 ~/.local/bin（不在默认 PATH），单测不依赖它。
func swapCodexPathForTest(t *testing.T) {
	t.Helper()
	restore := codex.SwapLookPathForTest(func(string) (string, error) {
		return "/fake/codex", nil
	})
	t.Cleanup(restore)
}

func TestPreflightFailsWithoutAuth(t *testing.T) {
	swapCodexPathForTest(t)
	home := t.TempDir() // 空目录：没有 auth.json
	err := codex.Preflight(home, nil)
	if err == nil {
		t.Fatal("未登录必须报错——否则失败点会拖到回合中途")
	}
	if !strings.Contains(err.Error(), "codex login") {
		t.Fatalf("错误必须给出可行动指引，实得: %v", err)
	}
}

func TestPreflightPassesWithAuth(t *testing.T) {
	swapCodexPathForTest(t)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// 污染源只 WARN 不阻断
	if err := os.WriteFile(filepath.Join(home, "AGENTS.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "hooks.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"),
		[]byte("model = \"gpt-5.6-sol\"\n[mcp_servers.superdev]\ncommand = \"x\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := codex.Preflight(home, nil); err != nil {
		t.Fatalf("污染源只应 WARN 不应阻断，实得: %v", err)
	}
}
