//go:build unix

// RunCmd 进程组回收测试（仅 unix）：超时后 sh 拉起的后台孙进程必须随进程组
// 一并被杀，不留孤儿。旧实现只杀 sh 本身，孙进程存活——本用例即回归红线。
package agentd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRunCmdKillsProcessGroupOnTimeout 验证超时按进程组回收：
// sh -c 拉起的后台 sleep（孙进程）在 RunCmd 超时后必须被一并杀掉。
func TestRunCmdKillsProcessGroupOnTimeout(t *testing.T) {
	orig := runCmdTimeout
	runCmdTimeout = 300 * time.Millisecond
	defer func() { runCmdTimeout = orig }()

	repo := initGitRepo(t)
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	// 子 shell 后台拉起 sleep 并把其 pid 写盘，随后 wait 阻塞——超时被杀时
	// sleep 成为 sh 的孙进程，只有按进程组回收才能被清理
	cmdline := fmt.Sprintf("(sleep 60 & echo $! > %s; wait)", pidFile)

	_, code, err := RunCmd(context.Background(), repo, cmdline)
	if err == nil || code != 124 {
		t.Fatalf("超时命令应返回错误且 code=124, got code=%d err=%v", code, err)
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
