//go:build linux

package prochost

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestAttributesByEnviron 验真实进程：带标记的（含 setsid 出去的）命中，
// 不带标记的不命中。
//
// 对照组必须先命中，再断言其余——先证明读取此刻没失灵（B37 spec §12.5）。
func TestAttributesByEnviron(t *testing.T) {
	cred := TaskCred{TaskID: "task-abc"}

	start := func(marked, setsid bool) *exec.Cmd {
		c := exec.Command("/bin/sleep", "30")
		c.Env = []string{"PATH=/usr/bin:/bin"}
		if marked {
			c.Env = append(c.Env, TaskMarkEnvKey+"=task-abc")
		}
		if setsid {
			c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		}
		if err := c.Start(); err != nil {
			t.Fatalf("起子进程失败: %v", err)
		}
		t.Cleanup(func() { _ = c.Process.Kill(); _, _ = c.Process.Wait() })
		return c
	}

	marked := start(true, false)
	markedSetsid := start(true, true)
	plain := start(false, false)
	time.Sleep(300 * time.Millisecond)

	if ok, err := attributes(marked.Process.Pid, cred); err != nil || !ok {
		t.Fatalf("对照组未命中，本轮测量无效：ok=%v err=%v", ok, err)
	}
	if ok, err := attributes(markedSetsid.Process.Pid, cred); err != nil || !ok {
		t.Fatalf("setsid 出去的子进程应命中：ok=%v err=%v", ok, err)
	}
	if ok, err := attributes(plain.Process.Pid, cred); err != nil || ok {
		t.Fatalf("无标记的进程不应命中：ok=%v err=%v", ok, err)
	}
}

// TestAttributesRejectsDifferentTaskID 钉住并发隔离：另一个任务的标记不得命中。
func TestAttributesRejectsDifferentTaskID(t *testing.T) {
	c := exec.Command("/bin/sleep", "30")
	c.Env = []string{"PATH=/usr/bin:/bin", TaskMarkEnvKey + "=task-other"}
	if err := c.Start(); err != nil {
		t.Fatalf("起子进程失败: %v", err)
	}
	t.Cleanup(func() { _ = c.Process.Kill(); _, _ = c.Process.Wait() })
	time.Sleep(300 * time.Millisecond)

	ok, err := attributes(c.Process.Pid, TaskCred{TaskID: "task-abc"})
	if err != nil {
		t.Fatalf("不应报错：%v", err)
	}
	if ok {
		t.Fatalf("另一个任务的标记不得命中")
	}
}

// TestAttributesEmptyTaskIDNeverMatches 钉住 TaskID 为空时一律不命中——
// 否则「没有该变量的进程」会被空串前缀匹配成命中。
func TestAttributesEmptyTaskIDNeverMatches(t *testing.T) {
	ok, err := attributes(os.Getpid(), TaskCred{})
	if err != nil {
		t.Fatalf("不应报错：%v", err)
	}
	if ok {
		t.Fatalf("TaskID 为空时不得命中")
	}
}
