//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

// ptyservice Unix PTY process adapter。
//
// 职责：用 creack/pty 启动登录 shell、设置 cwd/尺寸并终止进程组。
// 边界：不包含 session/replay/store 逻辑；Windows 明确走 unsupported adapter。
package ptyservice

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

type ptyProcess interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Resize(uint16, uint16) error
	Terminate() error
	Kill() error
	Wait() (int, error)
}

type unixProcess struct {
	cmd  *exec.Cmd
	file *os.File
}

// Supported 报告当前构建包含真实 PTY adapter。
func Supported() bool { return true }

func defaultShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/sh"
}

func startPtyProcess(shell, cwd string, cols, rows uint16) (ptyProcess, error) {
	cmd := exec.Command(shell, "-l")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	return &unixProcess{cmd: cmd, file: file}, nil
}

func (p *unixProcess) Read(buffer []byte) (int, error) { return p.file.Read(buffer) }

func (p *unixProcess) Write(data []byte) (int, error) { return p.file.Write(data) }

func (p *unixProcess) Resize(cols, rows uint16) error {
	return pty.Setsize(p.file, &pty.Winsize{Cols: cols, Rows: rows})
}

func (p *unixProcess) Terminate() error {
	if p.cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return p.cmd.Process.Signal(syscall.SIGTERM)
	}
	return nil
}

func (p *unixProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return p.cmd.Process.Kill()
	}
	return nil
}

func (p *unixProcess) Wait() (int, error) {
	err := p.cmd.Wait()
	_ = p.file.Close()
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal()), nil
		}
		return exitErr.ExitCode(), nil
	}
	return -1, err
}
