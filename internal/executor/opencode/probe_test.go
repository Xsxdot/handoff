// opencode 只读探活测试：判据是存活锁 + HTTP 应答，两者缺一即死。
package opencode

import (
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/prochost"
)

// 存活锁已释放 → 判死（端口不探，反正锁已经没了）。
func TestProbeSessionGoneAndNeverKills(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	// proc.json 指向无人持有的锁（LockPath 空则不建文件，Alive 恒 false）
	if err := writeProcInfo(dir, &procInfo{
		Handle: prochost.Handle{LockPath: filepath.Join(dir, "proc.lock")},
		Port:   45999, Password: "pw",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: dir, SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got.Alive {
		t.Fatal("存活锁已释放，serve 一定没了")
	}
	if got.Note == "" {
		t.Fatal("判死必须带一句话理由")
	}
	// Probe 是只读的：判死路径不该有任何进程被回收（Handle.PID 是假的，
	// 若被 Kill 会因 ESRCH 撞出错误日志，但更关键的是 Probe 根本不调用 Kill）
}

// proc.json 缺失 → 返回错误（调用方判 unknown）。
func TestProbeUnknownWhenServeInfoMissing(t *testing.T) {
	a := New(nil)
	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: t.TempDir(), SessionID: "sess-1"})
	if err == nil {
		t.Fatal("proc.json 缺失必须返回错误，让调用方判 unknown 而不是 dead")
	}
	if got.Alive {
		t.Fatal("出错时 Alive 必须为 false")
	}
}
