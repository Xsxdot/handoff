//go:build linux

// taskmark_linux.go —— linux 的任务标记实现：按注入的环境变量归属。
//
// 为什么是环境变量而不是 cwd：/proc/<pid>/environ 对同 uid 可读，macOS 那条
// 针对平台二进制的屏蔽在 linux 不存在（spec §4.3 非 root 实测）。环境变量比
// cwd 强在两处：进程 cd 走了也跟得住（构建脚本 cd 到别处再编译是常态），
// 并发下不依赖目录独占（两个任务指到同一个 --worktree 也不会串）。
// 因此 linux 上**所有任务形态**都能准确归属，不像 macOS 只限托管 worktree。
//
// 边界：只读 environ，不发信号、不判存活；不得 fork。
package prochost

import (
	"bytes"
	"fmt"
	"os"
)

// attributes 判定 pid 是否属于 cred 所描述的任务（linux：按 environ）。
//
// 返回：
//   - true: 该进程的 environ 里 TaskMarkEnvKey 的值等于 cred.TaskID
//   - 错误: 该 pid 的 environ 读不到（进程已退出，或不是本 uid 的进程）
//
// 注意：
//   - cred.TaskID 为空即一律不命中——否则「根本没有该变量的进程」会被
//     空值匹配成命中，把整台机器的进程都归给任务
//   - environ 反映的是进程**启动时**的环境块，正是我们要的：它由 execve 传递，
//     不受 setsid / reparent 影响，也不随进程后续行为改变
//   - 本判据依赖执行者不清洗环境变量。opencode 实测透传；其余三家未逐一验证
//     （spec §12 已记账）
//   - 本文件刻意不打日志：attributes 会被 markMembers 对每个 pid 调用一次，
//     全表数百次且 handoff status 高频触发；失败由 markMembers 统一记 Debug，
//     汇总由 Footprint 在边界上记录，避免淹没 agentd.log。
func attributes(pid int, cred TaskCred) (bool, error) {
	if cred.TaskID == "" {
		return false, nil
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return false, fmt.Errorf("读 /proc/%d/environ: %w", pid, err)
	}
	want := []byte(TaskMarkEnvKey + "=" + cred.TaskID)
	for _, kv := range bytes.Split(raw, []byte{0}) {
		if bytes.Equal(kv, want) {
			return true, nil
		}
	}
	return false, nil
}
