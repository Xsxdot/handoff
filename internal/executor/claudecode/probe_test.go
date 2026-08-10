// claudecode 只读探活测试。
//
// 覆盖三态与那条最容易误判的路径：tmux 会话还在（窗口 1 的 tail -f 吊着）
// 但 claude 已退——必须判死。
package claudecode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
)

// tmux 会话在且无死亡哨兵 → 存活。
func TestProbeAlive(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	if err := writeProcInfo(dir, &procInfo{TmuxSession: "handoff-abcdef01", SessionID: "sess-1"}); err != nil {
		t.Fatal(err)
	}
	old := tmuxHasSession
	tmuxHasSession = func(string) bool { return true }
	t.Cleanup(func() { tmuxHasSession = old })

	out, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: dir, SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !out.Alive {
		t.Fatal("tmux 会话在且无哨兵，应判存活")
	}
}

// 关键路径：tmux 会话还在但 out.jsonl 已有 handoff_exit 哨兵 → 必须判死。
// 这正是「manager 层统一 tmux has-session」会给出假阳性的那个反例。
func TestProbeSessionAliveButProcessExited(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	if err := writeProcInfo(dir, &procInfo{TmuxSession: "handoff-abcdef01", SessionID: "sess-1"}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, outFileName)
	if err := os.WriteFile(out, []byte(`{"type":"handoff_exit","code":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := tmuxHasSession
	tmuxHasSession = func(string) bool { return true }
	t.Cleanup(func() { tmuxHasSession = old })

	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: dir, SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got.Alive {
		t.Fatal("哨兵已出现，即使 tmux 会话还在也必须判死")
	}
	if got.Note == "" {
		t.Fatal("判死必须带一句话理由给审核者看")
	}
}

// tmux 会话没了 → 判死。
func TestProbeSessionGone(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	if err := writeProcInfo(dir, &procInfo{TmuxSession: "handoff-abcdef01", SessionID: "sess-1"}); err != nil {
		t.Fatal(err)
	}
	old := tmuxHasSession
	tmuxHasSession = func(string) bool { return false }
	t.Cleanup(func() { tmuxHasSession = old })

	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: dir, SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got.Alive {
		t.Fatal("tmux 会话已不存在，进程一定没了")
	}
}

// 恢复凭据缺失 → 返回错误（调用方按 unknown 处理，不得当成 dead）。
func TestProbeUnknownWhenCredentialsMissing(t *testing.T) {
	a := New(nil)
	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: t.TempDir(), SessionID: "sess-1"})
	if err == nil {
		t.Fatal("凭据缺失必须返回错误，让调用方判 unknown 而不是 dead")
	}
	if got.Alive {
		t.Fatal("出错时 Alive 必须为 false")
	}
}

// 只读铁律：探活不得回收 tmux 会话。判死路径上 Kill 一次都不能有。
func TestProbeNeverKills(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	if err := writeProcInfo(dir, &procInfo{TmuxSession: "handoff-abcdef01", SessionID: "sess-1"}); err != nil {
		t.Fatal(err)
	}
	killed := 0
	oldKill, oldHas := tmuxKill, tmuxHasSession
	tmuxKill = func(string) error { killed++; return nil }
	tmuxHasSession = func(string) bool { return false } // 判死路径
	t.Cleanup(func() { tmuxKill, tmuxHasSession = oldKill, oldHas })

	if _, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: dir, SessionID: "sess-1"}); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if killed != 0 {
		t.Fatalf("探活是只读的，不得回收会话，实际 Kill 了 %d 次", killed)
	}
}
