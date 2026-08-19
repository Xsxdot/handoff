// 本文件只做一件事：让本包 spawn 的子进程不要在用户屏幕上弹控制台窗口。
//
// 边界：
//   - 只管窗口可见性，不改 stdout/stderr 的接法（调用方仍拿得到 CombinedOutput）
//   - 与 noconsole_other.go 成对存在，非 Windows 上是空实现
package service

import (
	"os/exec"
	"syscall"
)

// createNoWindow 是 Win32 的 CREATE_NO_WINDOW。
//
// 为什么不能只靠 SysProcAttr.HideWindow：HideWindow 走的是 STARTUPINFO 的
// wShowWindow，对「父进程本身没有控制台」这种情形不生效——GUI 子系统进程
// 拉起控制台程序时，Windows 会给子进程**新分配**一个控制台，那个新控制台
// 不受 wShowWindow 约束。CREATE_NO_WINDOW 才是「根本不要分配控制台」。
const createNoWindow = 0x08000000

// hideConsole 让 c 启动时不分配控制台窗口。
//
// 参数：c 为尚未 Start 的命令；已 Start 的命令改 SysProcAttr 无效。
// 注意：会覆盖 c.SysProcAttr，调用方不要在此之后再自行赋值。
func hideConsole(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
