//go:build unix

package ptyhost

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// fmtSscan 是 fmt.Sscan 的别名，让调用点一眼看出它在解析 pid，不引入新依赖。
var fmtSscan = func(s string, a ...any) (int, error) { return fmt.Sscan(s, a...) }

// startDrain 在后台持续读 PTY 主端，把输出累积进 out。
//
// 为什么必须有它（macOS 实测）：bash 从 PTY 退出时会阻塞在「往主端写收尾输出」
// 的路径上，主端没人读它就不肯死，cmd.Wait 永不返回。生产代码里 ptyhost.Host
// 的 pump 循环总是在读，这是纯测试层面的需求。
//
// 另外 SetReadDeadline 在这里**不可用**：creack/pty 返回的主端是阻塞 fd，Go 的
// deadline 机制只对注册进 poller 的 fd 生效，实测挂在阻塞 Read 上不超时。
func startDrain(f *os.File, out *strings.Builder) chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		b := make([]byte, 4096)
		for {
			n, err := f.Read(b)
			if n > 0 {
				out.Write(b[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	return done
}

// readUntil 读 PTY 直到输出里出现 want 或超时，返回累积输出。
//
// 超时后读 goroutine 可能仍阻塞在 Read 上；测试的 defer killPty + f.Close()
// 会把它解开，不构成泄漏。
func readUntil(t *testing.T, f *os.File, want string, timeout time.Duration) string {
	t.Helper()
	var sb strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		b := make([]byte, 4096)
		for {
			n, err := f.Read(b)
			if n > 0 {
				sb.Write(b[:n])
				if strings.Contains(sb.String(), want) {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	select {
	case <-done:
		return sb.String()
	case <-time.After(timeout):
		return sb.String()
	}
}

// startPty 必须能起一个真 shell，回显可读，尺寸按传入值生效。
func TestStartPtyEchoAndSize(t *testing.T) {
	f, cmd, err := startPty("/bin/sh", t.TempDir(), []string{"TERM=xterm-256color", "PATH=/usr/bin:/bin"}, 120, 40)
	if err != nil {
		t.Fatalf("startPty: %v", err)
	}
	defer func() { _ = killPty(cmd); _ = f.Close() }()

	if _, err := f.WriteString("stty size; exit\n"); err != nil {
		t.Fatalf("写入 PTY: %v", err)
	}
	got := readUntil(t, f, "40 120", 10*time.Second)
	if !strings.Contains(got, "40 120") {
		t.Fatalf("PTY 输出里没读到 `40 120`（stty size 的 行 列 顺序），实得:\n%q", got)
	}
}

// resizePty 改尺寸后，shell 里 stty size 必须读到新值。
func TestResizePty(t *testing.T) {
	f, cmd, err := startPty("/bin/sh", t.TempDir(), []string{"PATH=/usr/bin:/bin"}, 80, 24)
	if err != nil {
		t.Fatalf("startPty: %v", err)
	}
	defer func() { _ = killPty(cmd); _ = f.Close() }()

	if err := resizePty(f, 100, 30); err != nil {
		t.Fatalf("resizePty: %v", err)
	}
	if _, err := f.WriteString("stty size; exit\n"); err != nil {
		t.Fatalf("写入 PTY: %v", err)
	}
	got := readUntil(t, f, "30 100", 10*time.Second)
	if !strings.Contains(got, "30 100") {
		t.Fatalf("resize 之后 stty size 没读到 `30 100`，实得:\n%q", got)
	}
}

// shell 正常退出时，waitExitCode 返回它的退出码。
func TestWaitExitCodeNormal(t *testing.T) {
	f, cmd, err := startPty("/bin/sh", t.TempDir(), []string{"PATH=/usr/bin:/bin"}, 80, 24)
	if err != nil {
		t.Fatalf("startPty: %v", err)
	}
	defer func() { _ = killPty(cmd); _ = f.Close() }()
	// 主端必须持续排空，否则 shell 的退出路径会卡在写收尾输出上（见 startDrain 注释）
	var out strings.Builder
	_ = startDrain(f, &out)
	if _, err := f.WriteString("exit 7\n"); err != nil {
		t.Fatalf("写入 PTY: %v", err)
	}
	done := make(chan int, 1)
	go func() { done <- waitExitCode(cmd) }()
	select {
	case code := <-done:
		if code != 7 {
			t.Fatalf("exit code = %d，期望 7", code)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("waitExitCode 超时，输出:\n%q", out.String())
	}
}

// 被信号杀掉时，退出码换算为 128+signo（SIGKILL=9 → 137）。
// 这是 shell 的通行约定，前端直接展示这个数字，不能是 -1。
func TestWaitExitCodeSignal(t *testing.T) {
	_, cmd, err := startPty("/bin/sh", t.TempDir(), []string{"PATH=/usr/bin:/bin"}, 80, 24)
	if err != nil {
		t.Fatalf("startPty: %v", err)
	}
	if err := killPty(cmd); err != nil {
		t.Fatalf("killPty: %v", err)
	}
	if code := waitExitCode(cmd); code != 137 {
		t.Fatalf("exit code = %d，期望 137（128+SIGKILL）", code)
	}
}

// terminatePty 打的是进程组：shell 的子进程也要一起走，否则关会话会留孤儿。
func TestTerminatePtyKillsProcessGroup(t *testing.T) {
	f, cmd, err := startPty("/bin/sh", t.TempDir(), []string{"PATH=/usr/bin:/bin"}, 80, 24)
	if err != nil {
		t.Fatalf("startPty: %v", err)
	}
	defer func() { _ = killPty(cmd); _ = f.Close() }()
	// 起一个后台子进程并打印它的 pid
	if _, err := f.WriteString("sleep 300 & echo CHILD=$!\n"); err != nil {
		t.Fatalf("写入 PTY: %v", err)
	}
	got := readUntil(t, f, "\r\nCHILD=", 10*time.Second)
	child := 0
	// want 用行首的 "\r\nCHILD=" 而不是裸的 "CHILD="：shell 会把命令原文回显出来，
	// 里面带着字面的 `echo CHILD=$!`，按裸串匹配会停在回显上而不是真实的输出行。
	// 同理取**最后一个** CHILD= 解析 pid，防止命中回显段。
	if i := strings.LastIndex(got, "CHILD="); i >= 0 {
		rest := strings.TrimSpace(got[i+len("CHILD="):])
		rest = strings.SplitN(rest, "\r", 2)[0]
		rest = strings.SplitN(rest, "\n", 2)[0]
		_, _ = fmtSscan(rest, &child)
	}
	if child == 0 {
		t.Fatalf("没读到子进程 pid，输出:\n%q", got)
	}
	if err := terminatePty(cmd); err != nil {
		t.Fatalf("terminatePty: %v", err)
	}
	// 交互式 bash 会把 SIGTERM 推迟到读到下一个命令才处理（bash 文档明确），
	// 因此不能阻塞等它退出；喂一个回车让挂起的信号被处理掉。真正的断言在下面：
	// 子进程 sleep 300 与 shell 同组、不拖信号，必须当场死掉。
	_, _ = f.WriteString("\n")
	// 给内核一点时间收割
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if p, err := os.FindProcess(child); err != nil || p.Signal(nil) != nil {
			return // 子进程已不在，符合预期
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("终止会话后子进程 %d 仍存活，进程组没打中", child)
}

// TestForegroundPgidIdleShellIsItself 断言：shell 空闲时前台进程组就是它自己，
// 判据据此得出「没有前台进程」。
func TestForegroundPgidIdleShellIsItself(t *testing.T) {
	f, cmd, err := startPty("/bin/sh", t.TempDir(), []string{"PATH=/usr/bin:/bin"}, 80, 24)
	if err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	defer func() { _ = killPty(cmd); _ = f.Close() }()

	fgp, ok := foregroundPgid(f)
	if !ok {
		t.Fatalf("拿不到前台进程组")
	}
	if fgp != cmd.Process.Pid {
		t.Fatalf("空闲 shell 的前台组应当是它自己：fg=%d pid=%d", fgp, cmd.Process.Pid)
	}
}

// TestForegroundPgidRunsChild 断言：shell 里跑一个前台命令时，前台进程组换成
// 那个命令的组——这正是「别把用户的 build 静默杀掉」所需要的判据。
//
// 已实测：macOS 交互式 /bin/sh（bash）开启作业控制，前台命令会自成进程组。
func TestForegroundPgidRunsChild(t *testing.T) {
	f, cmd, err := startPty("/bin/sh", t.TempDir(), []string{"PATH=/usr/bin:/bin"}, 80, 24)
	if err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	defer func() { _ = killPty(cmd); _ = f.Close() }()
	// 排空主端：提示符与回显若没人读，shell 可能在写路径上阻塞，前台命令起不来
	var sb strings.Builder
	startDrain(f, &sb)

	if _, err := f.Write([]byte("sleep 5\n")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	// 轮询而不是 sleep 一个定值：shell 起子进程的耗时在不同机器上差一个数量级，
	// 定值要么慢要么偶发失败
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fgp, ok := foregroundPgid(f); ok && fgp != cmd.Process.Pid {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("3 秒内前台进程组始终等于 shell 自己，前台命令没被识别出来")
}
