package codex_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor/codex"
)

func TestWSURLHasNoSecret(t *testing.T) {
	p := &codex.Proc{Session: "handoff-abc12345", TaskDir: t.TempDir(), Port: 47777}
	if got := p.WSURL(); got != "ws://127.0.0.1:47777" {
		t.Fatalf("WSURL = %s", got)
	}
}

func TestServeInfoRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := &codex.Proc{Session: "handoff-abc12345", TaskDir: dir, Port: 47777}
	if err := codex.WriteServeInfoForTest(p); err != nil {
		t.Fatalf("write serve info: %v", err)
	}
	fi, err := os.Stat(filepath.Join(dir, "serve.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("serve.json 权限 = %v，应为 0600", fi.Mode().Perm())
	}
	got, err := codex.ReadServeInfo(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Session != p.Session || got.Port != p.Port || got.TaskDir != dir {
		t.Fatalf("回读不一致: %+v", got)
	}
}

func TestLogTailReadsServeLog(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "serve.log"),
		[]byte("boot line\nlisten failed: address already in use\n"), 0o600); err != nil {
		t.Fatalf("seed log: %v", err)
	}
	p := &codex.Proc{Session: "s", TaskDir: dir, Port: 1}
	if !strings.Contains(p.LogTail(), "address already in use") {
		t.Fatalf("LogTail 没带上可行动真因: %q", p.LogTail())
	}
}

func TestKillTreatsMissingSessionAsClean(t *testing.T) {
	restore := codex.SwapTmuxKillForTest(func(session string) error {
		return os.ErrNotExist // 模拟 tmux kill-session 失败
	})
	defer restore()
	p := &codex.Proc{Session: "handoff-nonexistent", TaskDir: t.TempDir(), Port: 1}
	// 会话本就不存在时，Kill 视为已清理，不报错（B20：回收要幂等）
	if err := p.Kill(); err != nil {
		t.Fatalf("Kill 应把「会话不存在」视为已清理，实得: %v", err)
	}
}
