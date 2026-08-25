//go:build unix

package codex_test

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/codex"
	"github.com/Xsxdot/handoff/internal/prochost"
)

// 没有 threadId 就没法 resume——判不可恢复，且这不是错误
func TestResumeWithoutSessionIDIsNotAlive(t *testing.T) {
	a := codex.New(nil)
	out, err := a.Resume(executor.ResumeReq{TaskID: "T1", TaskDir: t.TempDir()})
	if err != nil {
		t.Fatalf("判不可恢复不应报错: %v", err)
	}
	if out.Alive {
		t.Fatal("无 threadId 时不应判活")
	}
}

// 没有 proc.json 同理
func TestResumeWithoutServeInfoIsNotAlive(t *testing.T) {
	a := codex.New(nil)
	out, err := a.Resume(executor.ResumeReq{
		TaskID: "T2", TaskDir: t.TempDir(), SessionID: "thread-1"})
	if err != nil {
		t.Fatalf("判不可恢复不应报错: %v", err)
	}
	if out.Alive {
		t.Fatal("无 proc.json 时不应判活")
	}
}

// 进程已死（锁空闲、端口连不上）且不允许冷恢复 → 保持不可恢复。
// 旧实现用 tmux kill 测试缝断言「先回收旧会话」；换成 prochost 后回收走
// prochost.Kill（锁空闲直接成功、不发信号），这里只断言结局：不判活。
func TestResumeColdDisallowedStaysDead(t *testing.T) {
	dir := t.TempDir()
	writeDeadServeInfo(t, dir)

	a := codex.New(nil)
	out, err := a.Resume(executor.ResumeReq{
		TaskID: "T3", TaskDir: dir, RepoPath: dir, SessionID: "thread-1", Cold: false})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.Alive {
		t.Fatal("不允许冷恢复时不应判活")
	}
	if out.Note == "" {
		t.Fatal("必须给出判死原因，协调者要能看懂为什么任务没恢复")
	}
}

// TestResumeHotReattachDoesNotCreateTaskTmp exercises the Resume hot path through
// the live proc seam. A broken implementation that creates the cold-only directory
// before checking Alive returns that filesystem error instead of reaching reattach.
func TestResumeHotReattachDoesNotCreateTaskTmp(t *testing.T) {
	root := t.TempDir()
	taskDir := filepath.Join(root, "tasks", "hot-task")
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tmp"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	lockPath := filepath.Join(taskDir, "proc.lock")
	held, err := prochost.AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("预占热恢复锁失败: %v", err)
	}
	defer held.Release()
	if err := codex.WriteServeInfoForTest(&codex.Proc{
		Handle:  prochost.Handle{PID: os.Getpid(), LockPath: lockPath},
		TaskDir: taskDir,
		Port:    ln.Addr().(*net.TCPAddr).Port,
	}); err != nil {
		t.Fatal(err)
	}

	a := codex.New(nil)
	out, err := a.Resume(executor.ResumeReq{
		TaskID: "hot-task", TaskDir: taskDir, RepoPath: taskDir,
		SessionID: "thread-1", Cold: true,
	})
	if err != nil {
		t.Fatalf("热重连不应被冷路径 tmp 创建失败拦截: %v", err)
	}
	if out.Alive {
		t.Fatalf("测试 listener 非 Codex WS，热重连应在拨号后判不可恢复: %+v", out)
	}
}

// 冷恢复时任务目录已被归档清理 → 判不可恢复，不越界重建
func TestResumeColdRefusesWhenTaskDirGone(t *testing.T) {
	dir := t.TempDir()
	writeDeadServeInfo(t, dir)
	gone := filepath.Join(dir, "not-exist")

	a := codex.New(nil)
	out, _ := a.Resume(executor.ResumeReq{
		TaskID: "T4", TaskDir: dir, RepoPath: gone, SessionID: "thread-1", Cold: true})
	if out.Alive {
		t.Fatal("工作区已不存在时不应判活（重建是 Dispatch 的职责）")
	}
}

// Reap：proc.json 缺失时如实报错交协调者，不猜（旧确定性会话名兜底已拆除）
func TestReapMissingProcInfoErrors(t *testing.T) {
	a := codex.New(nil)
	err := a.Reap("abcdef1234", t.TempDir())
	if err == nil {
		t.Fatal("proc.json 缺失时 Reap 必须报错（无据可查，不猜）")
	}
	if !strings.Contains(err.Error(), "恢复凭据") {
		t.Fatalf("错误应指向恢复凭据读取失败，实得 %q", err.Error())
	}
}

// Reap：锁空闲（进程本就已退）时直接成功，绝不对 pid 发信号（防误杀纪律）
func TestReapNoOpWhenLockFree(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "proc.lock")
	victim := newSleepProc(t)
	codex.WriteServeInfoForTest(&codex.Proc{
		Handle: prochost.Handle{PID: victim.Pid, LockPath: lock}, TaskDir: dir, Port: 1,
	})

	a := codex.New(nil)
	if err := a.Reap("abcdef1234", dir); err != nil {
		t.Fatalf("锁空闲时 Reap 应直接成功，实得 %v", err)
	}
	deadline := deadlineLater()
	for alivePID(victim.Pid) {
		if timeNowAfter(deadline) {
			break
		}
		sleepTiny()
	}
	if !alivePID(victim.Pid) {
		t.Fatal("锁空闲时 Reap 误杀了无关进程——防误杀纪律失效")
	}
}

func writeDeadServeInfo(t *testing.T, dir string) {
	t.Helper()
	// 端口指向一个必然连不上的地址、锁无人持有，让 Alive() 判死
	codex.WriteServeInfoForTest(&codex.Proc{
		Handle:  prochost.Handle{PID: 4242, LockPath: filepath.Join(dir, "proc.lock")},
		TaskDir: dir, Port: 1,
	})
}

// newSleepProc 拉一个独立进程组的常驻 sleep（Reap 防误杀测试的 victim）。
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

func alivePID(pid int) bool         { return syscall.Kill(pid, 0) == nil }
func deadlineLater() time.Time      { return time.Now().Add(2 * time.Second) }
func timeNowAfter(t time.Time) bool { return time.Now().After(t) }
func sleepTiny()                    { time.Sleep(10 * time.Millisecond) }
