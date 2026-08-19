// 本文件只做一件事：让薄壳 spawn 的子进程不要在用户屏幕上弹控制台窗口。
//
// 边界：
//   - 只管窗口可见性，不改 stdout/stderr 的接法（调用方仍拿得到 Output）
//   - 与 noconsole_other.go 成对存在，非 Windows 上是空实现
package main

import (
	"os/exec"
	"syscall"
)

// createNoWindow 是 Win32 的 CREATE_NO_WINDOW。
//
// 薄壳是 GUI 子系统进程、自身没有控制台，Windows 会给它拉起的控制台程序
// **新分配**一个控制台窗口——那个窗口不受 STARTUPINFO 的 wShowWindow 约束，
// 所以只设 HideWindow 不够，必须让它根本不分配。
const createNoWindow = 0x08000000

// hideConsole 让 c 启动时不分配控制台窗口。
//
// 参数：c 为尚未 Start 的命令；已 Start 的命令改 SysProcAttr 无效。
func hideConsole(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
