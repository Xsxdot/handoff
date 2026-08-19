//go:build !windows

// 本文件是 noconsole_windows.go 的非 Windows 对应物：这些平台上不存在
// 「子进程弹控制台窗口」这回事，因此是空实现。
package main

import "os/exec"

// hideConsole 在非 Windows 平台上什么都不做。
func hideConsole(*exec.Cmd) {}
