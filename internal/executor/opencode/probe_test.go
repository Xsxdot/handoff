// opencode 只读探活测试：判据是 tmux 会话 + HTTP 应答，两者缺一即死。
package opencode

import (
	"testing"

	"github.com/xushixin/handoff/internal/executor"
)

// tmux 会话没了 → 判死，且不得回收会话。
func TestProbeSessionGoneAndNeverKills(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	// 注意 writeServeInfo 收的是 *Proc（不是 serveInfo）——它内部只取
	// Port/Password/TmuxSession 三个字段序列化
	if err := writeServeInfo(dir, &Proc{Port: 45999, Password: "pw", TmuxSession: "handoff-abcdef01"}); err != nil {
		t.Fatal(err)
	}
	killed := 0
	oldKill, oldHas := tmuxKill, tmuxHasSession
	tmuxKill = func(string) error { killed++; return nil }
	tmuxHasSession = func(string) bool { return false }
	t.Cleanup(func() { tmuxKill, tmuxHasSession = oldKill, oldHas })

	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: dir, SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got.Alive {
		t.Fatal("tmux 会话已不存在，serve 一定没了")
	}
	if got.Note == "" {
		t.Fatal("判死必须带一句话理由")
	}
	if killed != 0 {
		t.Fatalf("探活是只读的，不得回收会话，实际 Kill 了 %d 次", killed)
	}
}

// serve.json 缺失 → 返回错误（调用方判 unknown）。
func TestProbeUnknownWhenServeInfoMissing(t *testing.T) {
	a := New(nil)
	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: t.TempDir(), SessionID: "sess-1"})
	if err == nil {
		t.Fatal("serve.json 缺失必须返回错误，让调用方判 unknown 而不是 dead")
	}
	if got.Alive {
		t.Fatal("出错时 Alive 必须为 false")
	}
}
