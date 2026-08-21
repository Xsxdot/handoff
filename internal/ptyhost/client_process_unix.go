//go:build unix

// client_process_unix.go —— Unix 上 ptyhost detached 进程的最小系统调用适配。
//
// 职责：让 ptyhost 脱离 agentd 的会话/进程组，并在 Open 失败时按组回收。
// 边界：不决定何时启动或回收，不读 spec，不写日志；业务语义在 client.go。
package ptyhost

import (
	"errors"
	"os/exec"
	"syscall"
)

func configureDetached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func killDetached(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
