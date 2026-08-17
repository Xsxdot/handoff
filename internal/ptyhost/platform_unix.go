//go:build unix

// PTY 的 unix 平台原语：开伪终端、起 login shell、调尺寸、按进程组终止。
//
// 职责：
//   - 把 openpty 与进程组信号这两件平台相关的事收敛在本文件
//   - 向上只暴露与 platform_other.go 完全同签名的五个函数
//
// 边界：
//   - 不认识会话、缓冲、订阅者——那是 ptyhost.go 的事
//   - 不做参数校验（shell 是否存在等），失败原样上抛
//
// 日志：本文件不打日志。所有错误原样上抛，由 ptyhost.go 带着会话 id 统一记录，
// 避免同一个失败在两层各留一条无法关联的记录。
package ptyhost

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// ptySupported 是本平台的能力常量，经 Host.Supported() 上报到 /api/status。
const ptySupported = true

// startPty 在新伪终端里启动一个 login shell。
//
// 参数：
//   - shell: shell 的绝对路径；cwd: 工作目录；env: 完整环境（不追加 os.Environ）
//   - cols/rows: 初始尺寸
//
// 返回：PTY 主端 fd、已启动的 cmd、错误。
//
// 注意：
//   - 用 `-l` 起 login shell，rc 链照读——用户要的是「和我在 iTerm 里一样」
//   - pty.StartWithSize 内部设置了 Setsid+Setctty，因此 shell 的 pid 即 pgid，
//     terminatePty 才能用 -pid 打整个进程组
//   - env 是**完整替换**：调用方必须自己把要继承的变量拼进来（见 envforward.go）
func startPty(shell, cwd string, env []string, cols, rows int) (*os.File, *exec.Cmd, error) {
	cmd := exec.Command(shell, "-l")
	cmd.Dir = cwd
	cmd.Env = env
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, nil, err
	}
	return f, cmd, nil
}

// withFd 在持有 *os.File 引用计数的前提下，把裸 fd 交给 fn 做 ioctl。
//
// 参数：f 为目标文件；fn 收到的 fd 只在回调期间有效，**不得**存下来事后再用。
//
// 返回：fn 自身的错误；文件已关闭（或取不到 SyscallConn）时返回该失败本身。
//
// 为什么不能直接用 f.Fd()：Fd() 不加引用计数地读出 fd 号，与并发的 f.Close()
// 构成数据竞争——而 ptyhost.reap 在 shell 退出的那一刻正是这么关主端的，
// 于是「取快照」「调尺寸」与「会话自然退出」一撞就中。真实危害比竞争本身更
// 隐蔽：拿到的 fd 号可能在 ioctl 发出前已被内核回收并重新分配给别的文件，
// ioctl 就打到了不相干的 fd 上。SyscallConn().Control 会先 incref，文件已关闭
// 时直接返回错误而不执行 fn，正好对上本包「读不到就当读不到」的语义。
func withFd(f *os.File, fn func(fd uintptr) error) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var inner error
	if err := rc.Control(func(fd uintptr) { inner = fn(fd) }); err != nil {
		return err
	}
	return inner
}

// resizePty 调整伪终端尺寸，内核随即向前台进程组发 SIGWINCH。
//
// 这里自己发 TIOCSWINSZ 而不用 pty.Setsize：后者走的是 f.Fd()（creack/pty
// v1.1.24 的 ioctl.go 只用阻塞路径），会话退出时与 reap 的 Close 撞成数据竞争。
func resizePty(f *os.File, cols, rows int) error {
	ws := unix.Winsize{Col: uint16(cols), Row: uint16(rows)}
	return withFd(f, func(fd uintptr) error {
		return unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &ws)
	})
}

// terminatePty 向整个进程组发 SIGTERM。
//
// 为什么是 -pid 而不是 pid：用户在终端里起的 `npm run dev`、`sleep 300 &` 都是
// shell 的子进程，只杀 shell 会留下一堆孤儿。startPty 用 Setsid 保证了 pid==pgid。
//
// 进程已经不在时（ESRCH）视为成功——它本来就是我们想要的终局。
func terminatePty(cmd *exec.Cmd) error { return signalGroup(cmd, syscall.SIGTERM) }

// killPty 是 terminatePty 的强制版，用于宽限期结束后的兜底。
func killPty(cmd *exec.Cmd) error { return signalGroup(cmd, syscall.SIGKILL) }

func signalGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, sig)
	if err != nil && errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// waitExitCode 阻塞等待 shell 退出并换算退出码。
//
// 被信号杀掉时返回 128+signo（SIGKILL → 137），这是 shell 的通行约定，
// 前端直接展示这个数字；返回 -1 会让用户看到一个没有含义的负数。
func waitExitCode(cmd *exec.Cmd) int {
	err := cmd.Wait()
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return -1
}

// foregroundPgid 读出该 PTY 当前的前台进程组 id。
//
// 参数：ptmx 为主设备端文件
//
// 返回：
//   - pgid: 前台进程组 id
//   - ok: 读到了才为 true。读不到的两种情形都归到 false：shell 已退出
//     （fd 已关）、或本平台不认这个 ioctl
//
// 注意：调用方要的通常不是 pgid 本身，而是「它是否 != shell 自己的 pid」——
// 相等意味着 shell 在等提示符（没有前台命令），不等意味着有个命令正跑在前台。
//
// 「shell 已退出」这一支正是靠 withFd 兜住的：fd 已被 reap 关掉时 Control
// 不会执行回调，直接返回错误，于是走到 false，而不是拿一个悬空的 fd 号去
// ioctl（见 withFd 的注释）。
func foregroundPgid(ptmx *os.File) (int, bool) {
	var pgid int
	err := withFd(ptmx, func(fd uintptr) error {
		var e error
		pgid, e = unix.IoctlGetInt(int(fd), unix.TIOCGPGRP)
		return e
	})
	if err != nil || pgid <= 0 {
		return 0, false
	}
	return pgid, true
}
