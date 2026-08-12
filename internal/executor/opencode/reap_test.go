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
	// 锁必须由 victim 自己持有（见 newLockVictim 的 why）：测试进程代持会让
	// 「进程已死」与「锁已释放」脱钩，Kill 复核必然误报 ErrStillAlive
	victim := newLockVictim(t, lock)

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

// lockVictimEnv 是持锁 victim helper 的开关环境变量（值为锁文件路径）。
const lockVictimEnv = "OPENCODE_REAP_LOCK_VICTIM"

// TestHelperLockVictim 不是测试：被 newLockVictim 以子进程方式调用。
// 它在子进程里抢下 lockVictimEnv 指向的锁并常驻 30s，模拟生产里
// 「shim 自己持锁」的形态。
func TestHelperLockVictim(t *testing.T) {
	if os.Getenv(lockVictimEnv) == "" {
		t.Skip("非 helper 调用")
	}
	held, err := prochost.AcquireLock(os.Getenv(lockVictimEnv))
	if err != nil {
		os.Exit(2)
	}
	defer held.Release()
	os.Stdout.WriteString("locked")
	os.Stdout.Close()
	time.Sleep(30 * time.Second)
}

// newLockVictim 拉起一个「自己持锁」的 victim 进程，等它就绪后返回。
//
// 为什么 victim 必须自己持锁：prochost.Kill 的死亡判据是**锁被释放**
// （Alive 探锁），不是「pid 消失」。若由测试进程代持锁，victim 被杀干净后
// 锁还握在测试手里，Kill 会报 ErrStillAlive——「进程已死」与「锁已释放」
// 脱钩，制造出生产中不存在的形态（生产里 shim 持锁、shim 一死内核即释放锁）。
func newLockVictim(t *testing.T, lock string) *os.Process {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", "^TestHelperLockVictim$", "-test.v=false")
	cmd.Env = append(os.Environ(), lockVictimEnv+"="+lock)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("建 stdout 管道失败: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动持锁 victim 失败: %v", err)
	}
	reaped := make(chan struct{})
	go func() { _ = cmd.Wait(); close(reaped) }() // 收割 zombie，让 alive 探测可靠
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-reaped })
	buf := make([]byte, len("locked"))
	if _, err := stdout.Read(buf); err != nil {
		t.Fatalf("等持锁 victim 就绪失败: %v", err)
	}
	return cmd.Process
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

// TestProcHandleReadsProcInfo 验证 adapter 能把 proc.json 里的进程句柄交出来。
//
// agentd 侧的清扫与计数都靠它取 Handle——取不到就只能降级为「无凭据」，
// 整个足迹功能对该 adapter 静默失效。
func TestProcHandleReadsProcInfo(t *testing.T) {
	dir := t.TempDir()
	want := prochost.Handle{PID: 4242, LockPath: filepath.Join(dir, "shim.lock"), StartedAt: 999}
	if err := writeProcInfo(dir, &procInfo{Handle: want, Port: 1234, Password: "p"}); err != nil {
		t.Fatalf("写 proc.json 失败: %v", err)
	}
	a := New(quietLogger())
	got, err := a.ProcHandle("task-1", dir)
	if err != nil {
		t.Fatalf("ProcHandle 失败: %v", err)
	}
	if got != want {
		t.Fatalf("Handle 不符：got %+v, want %+v", got, want)
	}
}

// TestProcHandleMissingProcInfo proc.json 不存在时必须报错，不能返回零值 Handle。
//
// 零值 Handle 的 PID=0，classify 会判 no_credential 降级——语义上没错，但
// 调用方拿不到「读失败」这个事实，日志里就少了一条能解释「为什么这个任务
// 从来没被清扫过」的线索。
func TestProcHandleMissingProcInfo(t *testing.T) {
	a := New(quietLogger())
	if _, err := a.ProcHandle("task-1", t.TempDir()); err == nil {
		t.Fatal("proc.json 缺失时应返回错误")
	}
}
