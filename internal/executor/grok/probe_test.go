// grok 只读探活测试：判据是存活锁 + 端口 HTTP 应答（收到任何响应即算活）。
package grok

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/prochost"
)

// writeTestServeInfo 写一份最小可读的恢复凭据（proc.json），供探活测试构造现场。
// 键名取自 procInfo 的 json tag（handle / port / secret）。
func writeTestServeInfo(t *testing.T, dir string, port int) {
	t.Helper()
	b, err := json.Marshal(&procInfo{
		Handle: prochost.Handle{PID: 4242, LockPath: filepath.Join(dir, lockFileName)},
		Port:   port, Secret: "s",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, procInfoFileName), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// proc.json 缺失 → 返回错误（调用方判 unknown，不得当 dead）。
func TestProbeUnknownWhenServeInfoMissing(t *testing.T) {
	a := New(nil)
	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: t.TempDir(), SessionID: "sess-1"})
	if err == nil {
		t.Fatal("proc.json 缺失必须返回错误，让调用方判 unknown")
	}
	if got.Alive {
		t.Fatal("出错时 Alive 必须为 false")
	}
}

// 锁无人持有 → 判死，且带理由。
// 写一份指向无人持锁 + 必然无人监听端口的凭据。
func TestProbeDeadWhenPortClosed(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	writeTestServeInfo(t, dir, 1) // 端口 1：特权端口，本机必然连不上
	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: dir, SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got.Alive {
		t.Fatal("锁无人持有应判死")
	}
	if got.Note == "" {
		t.Fatal("判死必须带一句话理由")
	}
}
