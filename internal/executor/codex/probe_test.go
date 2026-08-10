// codex 只读探活测试：判据是 app-server 端口 TCP 可连。
package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
)

// serve.json 缺失 → 返回错误（调用方判 unknown）。
func TestProbeUnknownWhenServeInfoMissing(t *testing.T) {
	a := New(nil)
	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: t.TempDir(), SessionID: "th-1"})
	if err == nil {
		t.Fatal("serve.json 缺失必须返回错误，让调用方判 unknown")
	}
	if got.Alive {
		t.Fatal("出错时 Alive 必须为 false")
	}
}

// 端口没人听 → 判死。
func TestProbeDeadWhenPortClosed(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	b, err := json.Marshal(map[string]any{"port": 1, "session": "handoff-abcdef01"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "serve.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: dir, SessionID: "th-1"})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got.Alive {
		t.Fatal("端口无人监听应判死")
	}
}
