package opencode

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/prochost"
)

// TestReapMissingProcInfoErrors proc.json 缺失时 Reap 必须报错交审核者——
// 锁+pid 无法从 taskID 推导，不猜（旧实现的确定性会话名兜底随 tmux 一起拆除）。
func TestReapMissingProcInfoErrors(t *testing.T) {
	a := New(quietLogger())
	err := a.Reap("abcdef12-3456", t.TempDir()) // 空 taskDir，无 proc.json
	if err == nil {
		t.Fatal("proc.json 缺失时 Reap 必须报错（无据可查，不猜）")
	}
	if !strings.Contains(err.Error(), "恢复凭据") {
		t.Fatalf("错误应指向恢复凭据读取失败，实得 %q", err.Error())
	}
}

// TestReapNoOpWhenLockFree 防误杀纪律：proc.json 存在但锁已空闲（进程本就已退），
// Reap 直接成功且绝不对该 pid 发信号。
func TestReapNoOpWhenLockFree(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "proc.lock")
	victim := newSleepProc(t)

	if err := writeProcInfo(dir, &procInfo{
		Handle: prochost.Handle{PID: victim.Pid, LockPath: lock}, Port: 1, Password: "p",
	}); err != nil {
		t.Fatal(err)
	}

	a := New(quietLogger())
	if err := a.Reap("abcdef12-3456", dir); err != nil {
		t.Fatalf("锁空闲时 Reap 应直接成功，实得 %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if !opencodeAlive(victim.Pid) {
		t.Fatal("锁空闲时 Reap 误杀了无关进程——防误杀纪律失效")
	}
}

// TestReapKillsWhenLockHeld 锁仍被持有（进程仍在跑）时按进程组回收。
func TestReapKillsWhenLockHeld(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "proc.lock")
	held, err := prochost.AcquireLock(lock)
	if err != nil {
		t.Fatalf("预占锁失败: %v", err)
	}
	defer held.Release()
	victim := newSleepProc(t)

	if err := writeProcInfo(dir, &procInfo{
		Handle: prochost.Handle{PID: victim.Pid, LockPath: lock}, Port: 1, Password: "p",
	}); err != nil {
		t.Fatal(err)
	}

	a := New(quietLogger())
	if err := a.Reap("abcdef12-3456", dir); err != nil {
		t.Fatalf("Reap: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for opencodeAlive(victim.Pid) {
		if time.Now().After(deadline) {
			t.Fatal("锁被持有时 Reap 应回收进程组")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newSleepProc(t *testing.T) *os.Process {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "sleep 10")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动 victim 失败: %v", err)
	}
	reaped := make(chan struct{})
	go func() { _ = cmd.Wait(); close(reaped) }() // 收割 zombie，让 alive 探测可靠
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-reaped })
	return cmd.Process
}

// opencodeAlive 判断 pid 是否还存在（信号 0 探测，不实际发信号）。
func opencodeAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }
