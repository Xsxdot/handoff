package claudecode

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

// alive 判断 pid 是否还存在（信号 0 探测，不实际发信号）。
func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// sleepCmd 返回一条会常驻 10s 的 sleep 命令（测试 victim）。
// Setpgid 让它成为独立进程组组长——prochost.Kill 按进程组发信号，
// 组组长身份是「连坐」生效的前提（与生产里 shim 经 Setsid 当组长对齐）。
// 后台 goroutine Wait 负责收割僵尸：SIGKILL 之后进程变 zombie，若不 Wait
// 回收，syscall.Kill(pid, 0) 对 zombie 恒返回 nil，轮询判死会永远等不到。
// Wait 只能有一个调用方（os/exec 的 Wait 非并发安全），所以由 goroutine
// 独占收割权，Cleanup 只 Kill + 等 goroutine 完成，不再二次 Wait。
func sleepCmd(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "sleep 10")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动 victim 失败: %v", err)
	}
	reaped := make(chan struct{})
	go func() { _ = cmd.Wait(); close(reaped) }() // 收割 zombie，让 alive() 探测可靠
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-reaped })
	return cmd
}

// TestReapMissingProcInfoErrors 兜底回收依赖 proc.json——缺失时如实报错交审核者，
// 不猜（旧实现的确定性会话名可由 taskID 推导，锁+pid 不能）。
func TestReapMissingProcInfoErrors(t *testing.T) {
	a := New(nil)
	err := a.Reap("abcdef12-3456", t.TempDir()) // 空 taskDir，无 proc.json
	if err == nil {
		t.Fatal("proc.json 缺失时 Reap 必须报错（无据可查，不猜）")
	}
	if !strings.Contains(err.Error(), "恢复凭据") {
		t.Fatalf("错误应指向恢复凭据读取失败，实得 %q", err.Error())
	}
}

// TestReapNoOpWhenLockFree 回收的防误杀纪律：proc.json 存在但 Handle 的锁已空闲
// （进程本就已退出）时，Reap 直接返回 nil 且绝不对该 pid 发信号。
func TestReapNoOpWhenLockFree(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "proc.lock")
	// 造一个真实存在、与本 Handle 无关的进程，模拟「pid 已被复用」的误杀风险
	victim := newSleepProc(t)

	if err := writeProcInfo(dir, &procInfo{
		Handle:    prochost.Handle{PID: victim.Pid, LockPath: lock},
		SessionID: "sess-1",
	}); err != nil {
		t.Fatal(err)
	}

	a := New(nil)
	if err := a.Reap("abcdef12-3456", dir); err != nil {
		t.Fatalf("锁空闲时 Reap 应直接成功，实得 %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if !alive(victim.Pid) {
		t.Fatal("锁空闲时 Reap 误杀了无关进程——防误杀纪律失效")
	}
}

// TestReapKillsWhenLockHeld Reap 在锁仍被持有（进程仍在跑）时按进程组回收。
func TestReapKillsWhenLockHeld(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "proc.lock")
	// 锁必须由 victim 自己持有（见 newLockVictim 的 why）：测试进程代持会让
	// 「进程已死」与「锁已释放」脱钩，Kill 复核必然误报 ErrStillAlive
	victim := newLockVictim(t, lock)

	if err := writeProcInfo(dir, &procInfo{
		Handle:    prochost.Handle{PID: victim.Pid, LockPath: lock},
		SessionID: "sess-1",
	}); err != nil {
		t.Fatal(err)
	}

	a := New(nil)
	if err := a.Reap("abcdef12-3456", dir); err != nil {
		t.Fatalf("Reap: %v", err)
	}
	// 锁被持有说明 Handle 还「活着」，Reap 应杀进程组连坐 victim
	deadline := time.Now().Add(2 * time.Second)
	for alive(victim.Pid) {
		if time.Now().After(deadline) {
			t.Fatal("锁被持有时 Reap 应回收进程组")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// lockVictimEnv 是持锁 victim helper 的开关环境变量（值为锁文件路径）。
const lockVictimEnv = "CLAUDECODE_REAP_LOCK_VICTIM"

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
	cmd := sleepCmd(t)
	return cmd.Process
}
