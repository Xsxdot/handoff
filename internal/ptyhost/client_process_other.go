//go:build !unix && !windows

// client_process_other.go —— 未实现平台上的 ptyhost 进程适配占位。
//
// 职责：保持客户端在其它 GOOS 上可编译。
// 边界：不承诺 detached 或 PTY 能力；Host.Supported 会在调用前返回 false。
package ptyhost

import "os/exec"

func configureDetached(*exec.Cmd) {}

func killDetached(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
