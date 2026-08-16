package grok_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor/grok"
	"github.com/Xsxdot/handoff/internal/prochost"
)

// TestGrokSpecKeepsSecretOutOfArgv 钉死安全边界（与 opencode 同源）：
// secret 必须走 env——argv 本机全局可读。旧实现同时排除了 tmux -e
// （show-environment 会把它暴露给任何能连上 tmux server 的本机用户），
// 现在 tmux 没了，但「不进 argv」这条依然是硬约束。
func TestGrokSpecKeepsSecretOutOfArgv(t *testing.T) {
	spec := grok.ServeSpecForTest("/repo", "/task", "grok-4", 23456, "t0psecret", nil)
	for _, a := range spec.Argv {
		if strings.Contains(a, "t0psecret") {
			t.Fatalf("secret 绝不能进 argv: %v", spec.Argv)
		}
	}
	var found bool
	for _, kv := range spec.Env {
		if strings.HasSuffix(kv, "=t0psecret") {
			found = true
		}
	}
	if !found {
		t.Fatalf("secret 必须经 env 传入，实得 %v", spec.Env)
	}
	if spec.LockPath == "" || spec.InfoPath == "" {
		t.Fatal("LockPath/InfoPath 必填")
	}
}

// TestGrokAliveNeedsLockFirst 钉死：锁判死后不再做网络探测（省一次超时等待）。
func TestGrokAliveNeedsLockFirst(t *testing.T) {
	p := &grok.Proc{Handle: prochost.Handle{PID: os.Getpid(),
		LockPath: filepath.Join(t.TempDir(), "proc.lock")}, Port: 1}
	if p.Alive() {
		t.Fatal("锁无人持有时必须直接判死")
	}
}

func TestWSURLCarriesSecretAsServerKey(t *testing.T) {
	p := &grok.Proc{Port: 24199, Secret: "abc"}
	got := p.WSURL()
	want := "ws://127.0.0.1:24199/ws?server-key=abc"
	if got != want {
		t.Errorf("WSURL = %q，期望 %q", got, want)
	}
}

func TestServeInfoRoundTripAndSecretNotInLogTail(t *testing.T) {
	dir := t.TempDir()
	// serve.log 里混入 secret，模拟 grok 启动横幅回显（实测它确实会打印）
	logPath := filepath.Join(dir, "serve.log")
	if err := os.WriteFile(logPath, []byte("Secret:   s3cr3t\npanic: boom\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &grok.Proc{Handle: prochost.Handle{PID: 42, LockPath: filepath.Join(dir, "proc.lock")},
		TaskDir: dir, Port: 24199, Secret: "s3cr3t"}
	if err := grok.WriteServeInfoForTest(p); err != nil {
		t.Fatalf("写 serve.json 失败: %v", err)
	}
	got, err := grok.ReadServeInfo(dir)
	if err != nil {
		t.Fatalf("读 serve.json 失败: %v", err)
	}
	if got.Port != 24199 || got.Secret != "s3cr3t" {
		t.Errorf("往返不一致: %+v", got)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "proc.json")); fi.Mode().Perm() != 0o600 {
		t.Errorf("proc.json 权限 = %v，期望 0600（含 secret）", fi.Mode().Perm())
	}
	// ReadServeInfo 内部以 taskDir 实参为准；LogTail 需要 TaskDir，补上以对齐
	// 生产里 Resume 的用法（TaskDir 由 req.TaskDir 显式传入）
	got.TaskDir = dir

	// LogTail 必须脱敏：它会进 FailReason 落事件库，也进 agentd.log
	tail := got.LogTail()
	if strings.Contains(tail, "s3cr3t") {
		t.Errorf("LogTail 必须脱敏 secret，实际: %q", tail)
	}
	if !strings.Contains(tail, "panic: boom") {
		t.Errorf("LogTail 应保留诊断内容，实际: %q", tail)
	}
}
