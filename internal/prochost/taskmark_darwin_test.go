//go:build darwin

package prochost

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestCwdOffsetSelfCheckPasses 钉住偏移量自检本身是有效的：
// 它必须能读出本进程真实的 cwd。自检失效时整条判据会降级，
// 那时下面的归属测试会全部「正确地」返回 not supported——
// 所以先单独把自检钉住，否则后面的绿是假绿。
func TestCwdOffsetSelfCheckPasses(t *testing.T) {
	if !cwdReadable() {
		t.Fatalf("cwd 偏移量自检未通过：本机上 proc_pidinfo 读不出自己的 cwd")
	}
	got, err := cwdOf(os.Getpid())
	if err != nil {
		t.Fatalf("读自身 cwd 失败: %v", err)
	}
	want, _ := os.Getwd()
	wantResolved, _ := filepath.EvalSymlinks(want)
	if got != wantResolved {
		t.Fatalf("自身 cwd 不符：实得 %q 期望 %q", got, wantResolved)
	}
}

// TestAttributesByCwd 验真实进程：worktree 内的（含 setsid 出去的）命中，
// worktree 外的不命中。
//
// 对照组必须先命中，再断言目标——先证明扫描此刻没失灵，否则「没捞到」
// 与「读取机制坏了」在输出上完全一样（B37 spec §12.5 的教训）。
func TestAttributesByCwd(t *testing.T) {
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("解析临时目录失败: %v", err)
	}
	cred := TaskCred{TaskID: "task-1", MarkRoot: resolved}

	start := func(dir string, setsid bool) *exec.Cmd {
		c := exec.Command("/bin/sleep", "30")
		c.Dir = dir
		if setsid {
			c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		}
		if err := c.Start(); err != nil {
			t.Fatalf("起子进程失败: %v", err)
		}
		t.Cleanup(func() { _ = c.Process.Kill(); _, _ = c.Process.Wait() })
		return c
	}

	inside := start(resolved, false)
	insideSetsid := start(resolved, true)
	outside := start(os.TempDir(), false)
	time.Sleep(300 * time.Millisecond)

	// 前置断言：对照组（确知在 worktree 内）必须命中
	if ok, err := attributes(inside.Process.Pid, cred); err != nil || !ok {
		t.Fatalf("对照组未命中，本轮测量无效：ok=%v err=%v", ok, err)
	}
	if ok, err := attributes(insideSetsid.Process.Pid, cred); err != nil || !ok {
		t.Fatalf("setsid 出去的子进程应命中：ok=%v err=%v", ok, err)
	}
	if ok, err := attributes(outside.Process.Pid, cred); err != nil || ok {
		t.Fatalf("worktree 外的进程不应命中：ok=%v err=%v", ok, err)
	}
}

// TestAttributesEmptyMarkRootNeverMatches 钉住 MarkRoot 为空时一律不命中——
// 这是「仅托管 worktree 可杀」在平台层的最后一道防线。
func TestAttributesEmptyMarkRootNeverMatches(t *testing.T) {
	ok, err := attributes(os.Getpid(), TaskCred{TaskID: "task-1"})
	if err != nil {
		t.Fatalf("不应报错：%v", err)
	}
	if ok {
		t.Fatalf("MarkRoot 为空时不得命中任何进程")
	}
}
