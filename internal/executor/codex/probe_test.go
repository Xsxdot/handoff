// codex 只读探活测试：判据是存活锁 + app-server 端口 TCP 可连。
package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/prochost"
)

// proc.json 缺失 → 返回错误（调用方判 unknown）。
func TestProbeUnknownWhenServeInfoMissing(t *testing.T) {
	a := New(nil)
	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: t.TempDir(), SessionID: "th-1"})
	if err == nil {
		t.Fatal("proc.json 缺失必须返回错误，让调用方判 unknown")
	}
	if got.Alive {
		t.Fatal("出错时 Alive 必须为 false")
	}
}

// 锁无人持有 → 判死。
func TestProbeDeadWhenPortClosed(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	b, err := json.Marshal(&procInfo{
		Handle: prochost.Handle{PID: 4242, LockPath: filepath.Join(dir, lockFileName)},
		Port:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, procInfoFileName), b, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: dir, SessionID: "th-1"})
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
