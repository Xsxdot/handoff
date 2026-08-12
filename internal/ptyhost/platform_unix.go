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

// resizePty 调整伪终端尺寸，内核随即向前台进程组发 SIGWINCH。
func resizePty(f *os.File, cols, rows int) error {
	return pty.Setsize(f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
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
