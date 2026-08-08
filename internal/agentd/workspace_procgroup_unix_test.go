//go:build unix

// RunCmd 进程组回收测试（仅 unix）：超时后 sh 拉起的后台孙进程必须随进程组
// 一并被杀，不留孤儿。旧实现只杀 sh 本身，孙进程存活——本用例即回归红线。
package agentd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// TestRunCmdKillsProcessGroupOnTimeout 验证超时按进程组回收：
// sh -c 拉起的后台 sleep（孙进程）在 RunCmd 超时后必须被一并杀掉。
// 同时用计数包装断言 killProcGroup 恰好调用 1 次——既不能漏杀（孙进程
// 存活），也不能多杀（对已回收 pid 重复发信号即 P0-3 的误杀形态）。
func TestRunCmdKillsProcessGroupOnTimeout(t *testing.T) {
	orig := runCmdTimeout
	runCmdTimeout = 300 * time.Millisecond
	defer func() { runCmdTimeout = orig }()

	var kills atomic.Int32
	origKill := killProcGroup
	killProcGroup = func(pid int) { kills.Add(1); origKill(pid) }
	defer func() { killProcGroup = origKill }()

	repo := initGitRepo(t)
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	// 子 shell 后台拉起 sleep 并把其 pid 写盘，随后 wait 阻塞——超时被杀时
	// sleep 成为 sh 的孙进程，只有按进程组回收才能被清理
	cmdline := fmt.Sprintf("(sleep 60 & echo $! > %s; wait)", pidFile)

	_, code, err := RunCmd(context.Background(), repo, cmdline)
	if err == nil || code != 124 {
		t.Fatalf("超时命令应返回错误且 code=124, got code=%d err=%v", code, err)
	}
	if got := kills.Load(); got != 1 {
		t.Fatalf("超时路径 killProcGroup 应恰好调用 1 次, got %d", got)
	}
	b, rerr := os.ReadFile(pidFile)
	if rerr != nil {
		t.Fatalf("读取孙进程 pid 文件: %v", rerr)
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(string(b)))
	if perr != nil {
		t.Fatalf("解析孙进程 pid %q: %v", b, perr)
	}
	// 组回收是异步的（ctx.Done → 回收协程 kill），轮询等待孙进程死亡
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return // 孙进程已死：进程组回收生效
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("超时后孙进程 %d 仍存活（进程组未被回收，旧实现即此处红）", pid)
}

// TestRunCmdNoKillOnNormalExit 是 P0-3 的回归红线：正常退出的命令，回收协程
// 绝不允许再向已回收的 pid 发 SIGKILL。实测旧实现 300 条成功命令误杀 114 次
// （回收协程滞后于 Wait 返回 + defer cancel()，select 两条分支同时就绪随机择路
// 命中 ctx.Done 分支，无条件杀已回收的 pid——一旦 pid 被 OS 复用为进程组组长，
// 就会 SIGKILL 掉 executor 机器上毫不相干的进程组）。
//
// 原理：GOMAXPROCS=1 让回收协程必然滞后于主流程（close(cmdDone) 后直到
// cancel() 触发才有机会被调度），此时 ctx.Done 与 cmdDone 同时就绪，旧实现
// 每次约 50% 概率误杀；修复后两条分支在进程已回收时都不再杀，次数恒为 0。
func TestRunCmdNoKillOnNormalExit(t *testing.T) {
	origMax := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(origMax)

	var kills atomic.Int32
	origKill := killProcGroup
	killProcGroup = func(pid int) { kills.Add(1); origKill(pid) }
	defer func() { killProcGroup = origKill }()

	repo := initGitRepo(t)
	for i := 0; i < 20; i++ {
		_, code, err := RunCmd(context.Background(), repo, "echo ok")
		if err != nil || code != 0 {
			t.Fatalf("第 %d 条命令应正常退出: code=%d err=%v", i, code, err)
		}
	}
	// 让滞后的回收协程全部跑完，再断言误杀次数
	runtime.Gosched()
	time.Sleep(200 * time.Millisecond)
	if got := kills.Load(); got != 0 {
		t.Fatalf("正常退出命令不应触发 killProcGroup（对已回收 pid 发 SIGKILL）, 调用 %d 次", got)
	}
}
