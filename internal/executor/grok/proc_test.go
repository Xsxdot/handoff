package grok_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor/grok"
)

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
	p := &grok.Proc{Session: "handoff-abcd1234", TaskDir: dir, Port: 24199, Secret: "s3cr3t"}
	if err := grok.WriteServeInfoForTest(p); err != nil {
		t.Fatalf("写 serve.json 失败: %v", err)
	}
	got, err := grok.ReadServeInfo(dir)
	if err != nil {
		t.Fatalf("读 serve.json 失败: %v", err)
	}
	if got.Port != 24199 || got.Secret != "s3cr3t" || got.Session != "handoff-abcd1234" {
		t.Errorf("往返不一致: %+v", got)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "serve.json")); fi.Mode().Perm() != 0o600 {
		t.Errorf("serve.json 权限 = %v，期望 0600（含 secret）", fi.Mode().Perm())
	}

	// LogTail 必须脱敏：它会进 FailReason 落事件库，也进 agentd.log
	tail := got.LogTail()
	if strings.Contains(tail, "s3cr3t") {
		t.Errorf("LogTail 必须脱敏 secret，实际: %q", tail)
	}
	if !strings.Contains(tail, "panic: boom") {
		t.Errorf("LogTail 应保留诊断内容，实际: %q", tail)
	}
}
