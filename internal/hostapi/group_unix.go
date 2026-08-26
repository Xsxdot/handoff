//go:build unix

// group_unix.go —— unix 平台的进程组终止。run 子进程 Setpgid 成独立组，
// 超时按负 pid 对整组 SIGKILL：opencode 可能自起内嵌 server（`--port` flag
// 所示），只杀直接子进程会留下持有管道/端口的孤儿组。
package hostapi

import (
	"os"
	"os/exec"
	"syscall"
)

// configureProcess 让子进程自成进程组（必须在 Start 前设置）。
func configureProcess(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killGroup 对整个进程组 SIGKILL（负 pid 语义）。
func killGroup(p *os.Process) error {
	return syscall.Kill(-p.Pid, syscall.SIGKILL)
}
