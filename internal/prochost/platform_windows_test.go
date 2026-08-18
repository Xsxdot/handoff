//go:build windows

// Windows 平台原语的测试：真的建 job、真的 spawn 子进程。
//
// 为什么不能全缝注入：这里验的正是 x/sys 缺结构体、手工声明的布局和系统调用
// 是否匹配；注入掉系统调用等于把要验的东西验没了。
package prochost

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// jobProcessIDs 必须能看见同 job 内 spawn 出来的子进程，并在它退出后不再报它。
//
// **这个 job 刻意不设 KILL_ON_JOB_CLOSE，别加回去。** 生产代码
// （installProcessContainer）设它是对的：那里的 job 属于 shim，shim 一死就该
// 连坐收掉整棵执行者树。但本测试把**测试进程自己**加进 job，一旦设了这个 flag，
// `defer CloseHandle` 关掉最后一个句柄时内核会当场杀掉 job 内全部进程——
// 包括测试进程本身。
//
// 后果不是「测试崩了」那么显眼，而是**一个永远不会红的测试**（2026-08-18 实测）：
//   - 测试体跑完 → defer 自杀 → 框架来不及打印 `--- PASS`，同包后续测试
//     （TestJobProcessIDsWithoutJobErrors）一次都没跑，而 `go test` 仍报
//     `ok` + exit 0
//   - 断言失败时更糟：t.Fatalf 走 runtime.Goexit，defer 照常执行、照常自杀，
//     于是 **FAIL 也被吃掉**。无论代码对错，这个测试都报绿
//
// 本测试要验的只是「成员表读得对不对」，与连坐回收无关——那是 B37 验过的事。
func TestJobProcessIDsSeesChildAndForgetsIt(t *testing.T) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatalf("CreateJobObject: %v", err)
	}
	defer windows.CloseHandle(h)
	self, err := windows.GetCurrentProcess()
	if err != nil {
		t.Fatalf("GetCurrentProcess: %v", err)
	}
	if err := windows.AssignProcessToJobObject(h, self); err != nil {
		t.Fatalf("AssignProcessToJobObject(self): %v —— 外层 job 可能不允许嵌套", err)
	}
	saved := jobHandle
	jobHandle = h
	defer func() { jobHandle = saved }()

	pids, err := jobProcessIDs()
	if err != nil {
		t.Fatalf("jobProcessIDs(before spawn): %v", err)
	}
	if !containsInt(pids, os.Getpid()) {
		t.Fatalf("成员表里没有自己 self=%d pids=%v", os.Getpid(), pids)
	}

	child := exec.Command("ping", "-n", "30", "127.0.0.1")
	if err := child.Start(); err != nil {
		t.Fatalf("spawn child: %v", err)
	}
	childPID := child.Process.Pid
	time.Sleep(500 * time.Millisecond)

	pids2, err := jobProcessIDs()
	if err != nil {
		t.Fatalf("jobProcessIDs(after spawn): %v", err)
	}
	if !containsInt(pids2, childPID) {
		t.Fatalf("子进程未出现在成员表 child=%d pids=%v", childPID, pids2)
	}

	_ = child.Process.Kill()
	_, _ = child.Process.Wait()
	time.Sleep(500 * time.Millisecond)

	pids3, err := jobProcessIDs()
	if err != nil {
		t.Fatalf("jobProcessIDs(after kill): %v", err)
	}
	if containsInt(pids3, childPID) {
		t.Fatalf("子进程已退出但仍在成员表 child=%d pids=%v", childPID, pids3)
	}
}

// 没有 job 句柄时必须报错而不是返回空集：空集会被上层读成「一个进程都没有」。
func TestJobProcessIDsWithoutJobErrors(t *testing.T) {
	saved := jobHandle
	jobHandle = 0
	defer func() { jobHandle = saved }()
	if _, err := jobProcessIDs(); err == nil {
		t.Fatal("无 job 句柄时应报错，返回空集会被误读成「没有成员」")
	}
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
