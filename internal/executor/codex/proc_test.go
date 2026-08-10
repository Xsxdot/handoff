package codex_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor/codex"
	"github.com/xushixin/handoff/internal/prochost"
)

// TestCodexSpecArgvIsListenForm 钉死 codex 的启动形态：
// `codex app-server --listen ws://127.0.0.1:<port>` 是协议契约的一部分，
// 端口拼错或少了 --listen 会让 WS JSON-RPC 完全连不上。
func TestCodexSpecArgvIsListenForm(t *testing.T) {
	spec := codex.ServeSpecForTest("/repo", "/task", 34567, []string{"HTTPS_PROXY=http://p:1"})
	joined := strings.Join(spec.Argv, " ")
	if !strings.Contains(joined, "app-server") || !strings.Contains(joined, "--listen") {
		t.Fatalf("argv 必须是 codex app-server --listen 形态，实得 %v", spec.Argv)
	}
	if !strings.Contains(joined, "34567") {
		t.Fatalf("argv 必须带上分配到的端口，实得 %v", spec.Argv)
	}
	// 代理必须透传：codex 从非交互上下文启动，继承不到 shell 里的代理变量，
	// 漏配的症状极具迷惑性（会话建得起来、show 显示 running，但一个 token 都不产）
	var gotProxy bool
	for _, kv := range spec.Env {
		if kv == "HTTPS_PROXY=http://p:1" {
			gotProxy = true
		}
	}
	if !gotProxy {
		t.Fatalf("env 文件的代理变量必须透传，实得 %v", spec.Env)
	}
	if spec.LockPath == "" || spec.InfoPath == "" {
		t.Fatal("LockPath/InfoPath 必填")
	}
}

// TestCodexAliveNeedsLockFirst 钉死锁优先的两层判定。
func TestCodexAliveNeedsLockFirst(t *testing.T) {
	p := &codex.Proc{Handle: prochost.Handle{PID: os.Getpid(),
		LockPath: filepath.Join(t.TempDir(), "proc.lock")}, Port: 1}
	if p.Alive() {
		t.Fatal("锁无人持有时必须直接判死")
	}
}

func TestWSURLHasNoSecret(t *testing.T) {
	p := &codex.Proc{Handle: prochost.Handle{PID: 42, LockPath: filepath.Join(t.TempDir(), "proc.lock")},
		TaskDir: t.TempDir(), Port: 47777}
	if got := p.WSURL(); got != "ws://127.0.0.1:47777" {
		t.Fatalf("WSURL = %s", got)
	}
}

func TestServeInfoRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := &codex.Proc{Handle: prochost.Handle{PID: 42, LockPath: filepath.Join(dir, "proc.lock")},
		TaskDir: dir, Port: 47777}
	if err := codex.WriteServeInfoForTest(p); err != nil {
		t.Fatalf("write serve info: %v", err)
	}
	fi, err := os.Stat(filepath.Join(dir, "proc.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("proc.json 权限 = %v，应为 0600", fi.Mode().Perm())
	}
	got, err := codex.ReadServeInfo(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Port != p.Port || got.TaskDir != dir {
		t.Fatalf("回读不一致: %+v", got)
	}
}

func TestLogTailReadsServeLog(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "serve.log"),
		[]byte("boot line\nlisten failed: address already in use\n"), 0o600); err != nil {
		t.Fatalf("seed log: %v", err)
	}
	p := &codex.Proc{TaskDir: dir, Port: 1}
	if !strings.Contains(p.LogTail(), "address already in use") {
		t.Fatalf("LogTail 没带上可行动真因: %q", p.LogTail())
	}
}
