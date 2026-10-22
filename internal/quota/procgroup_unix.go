//go:build unix

// procgroup_unix.go —— agy 子进程的进程组隔离与整组终止（unix）。
// 与 internal/hostapi/group_unix.go 同一做法：Setpgid 自成一组，超时/取消按负 pid
// 对整组 SIGKILL——只杀直接子进程会留下 agy 自起的孙进程。
package quota

import (
	"os/exec"
	"syscall"
)

const procGroupSupported = true

// isolateGroup 让子进程自成进程组，并把 ctx 取消的处置改成杀整组（必须在 Start 前设置）。
func isolateGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
