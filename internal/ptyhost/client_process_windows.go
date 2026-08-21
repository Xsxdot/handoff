//go:build windows

// client_process_windows.go —— Windows 上 ptyhost detached 进程的系统适配。
//
// 职责：让 ptyhost 不继承 agentd 的控制台，并在启动失败时终止它。
// 边界：不实现 ConPTY；Windows 上 Host.Supported 仍为 false，业务语义在 client.go。
package ptyhost

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func configureDetached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
}

func killDetached(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
