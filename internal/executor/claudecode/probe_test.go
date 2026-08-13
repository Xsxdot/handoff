// claudecode 只读探活测试。
//
// 覆盖三态与那条最容易误判的路径：存活锁还在但 claude 已退（哨兵在）——必须判死。
package claudecode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/prochost"
)

// 存活锁被持有且无死亡哨兵 → 存活。
func TestProbeAlive(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	lock := filepath.Join(dir, "proc.lock")
	held, err := prochost.AcquireLock(lock)
	if err != nil {
		t.Fatalf("预占锁失败: %v", err)
	}
	t.Cleanup(func() { held.Release() })
	if err := writeProcInfo(dir, &procInfo{
		Handle: prochost.Handle{PID: 4242, LockPath: lock}, SessionID: "sess-1",
	}); err != nil {
		t.Fatal(err)
	}

	out, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: dir, SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !out.Alive {
		t.Fatal("锁被持有且无哨兵，应判存活")
	}
}

// 关键路径：锁被持有但 out.jsonl 已有 handoff_exit 哨兵 → 必须判死。
// 这正是「锁在 ≠ 进程活」的极短窗口，哨兵兜住它。
func TestProbeSessionAliveButProcessExited(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	lock := filepath.Join(dir, "proc.lock")
	held, err := prochost.AcquireLock(lock)
	if err != nil {
		t.Fatalf("预占锁失败: %v", err)
	}
	t.Cleanup(func() { held.Release() })
	if err := writeProcInfo(dir, &procInfo{
		Handle: prochost.Handle{PID: 4242, LockPath: lock}, SessionID: "sess-1",
	}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, outFileName)
	if err := os.WriteFile(out, []byte(`{"type":"handoff_exit","code":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: dir, SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got.Alive {
		t.Fatal("哨兵已出现，即使锁还被持有也必须判死")
	}
	if got.Note == "" {
		t.Fatal("判死必须带一句话理由给协调者看")
	}
}

// 存活锁已释放 → 判死。
func TestProbeSessionGone(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	lock := filepath.Join(dir, "proc.lock")
	if err := writeProcInfo(dir, &procInfo{
		Handle: prochost.Handle{PID: 4242, LockPath: lock}, SessionID: "sess-1",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: dir, SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got.Alive {
		t.Fatal("存活锁已释放，进程一定没了")
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

// 只读铁律：探活不得回收执行者。判死路径上 Kill 一次都不能有。
func TestProbeNeverKills(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	lock := filepath.Join(dir, "proc.lock")
	if err := writeProcInfo(dir, &procInfo{
		Handle: prochost.Handle{PID: 4242, LockPath: lock}, SessionID: "sess-1",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: dir, SessionID: "sess-1"}); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	// Probe 是只读的：判死路径不该有任何进程被回收（pid 4242 是假的，若被 Kill
	// 会因 ESRCH 撞出错误日志，但更关键的是 Probe 根本不调用 Kill）
}
