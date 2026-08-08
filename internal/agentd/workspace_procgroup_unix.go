//go:build unix

// 本文件提供 RunCmd 的进程组原语（unix 实现）：让命令成为独立进程组组长，
// 超时/取消时把组内孙进程一并回收，不留孤儿。
package agentd

import (
	"os/exec"
	"syscall"
)

// setProcGroup 让命令进程成为新进程组组长（pgid = pid）。
//
// 为什么需要：RunCmd 经 sh -c 执行，命令可能拉起孙进程（管道/后台子 shell）；
// CommandContext 超时只杀 sh 本身，孙进程会成孤儿继续占用资源。设为组组长后，
// 超时可按组一次性回收全部后代。
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcGroup 杀掉 pid 所在进程组（组 id 即组长 pid，负数表示按组发送）。
//
// 幂等：组已不存在（全部成员已退出）时返回 ESRCH，调用方忽略即可。
// 包级 var 而非 func：便于测试替换为计数包装，断言回收协程的调用次数
// （P0-3 回归：正常退出路径必须恰好 0 次，超时路径恰好 1 次）。
var killProcGroup = func(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
