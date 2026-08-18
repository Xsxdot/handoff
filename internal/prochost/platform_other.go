//go:build !unix && !windows

// platform_other.go —— prochost 的既非 unix 也非 windows 平台原语（骨架）。
//
// 文件名用 _other 而非 _windows：Go 会把 _windows 后缀当成隐式 GOOS 约束，
// 与 //go:build !unix && !windows 相与后覆盖 plan9/js 等其它平台。
// 仓库既有的 flock_other.go / opennonblock_other.go 也是这个命名。
//
// 职责：让 prochost 在没有平台承载实现的其它平台上保持可编译，并明确返回退化语义。
//
// 边界与两类退化（**两类语义不同，别混为一谈**）：
//   - **进程类原语**（spawnDetached / killGroup / createInputChannel /
//     waitInputReader）返回 errNotImplemented：拉不起来就是拉不起来，
//     绝不能静默假装成功
//   - **锁原语**（flockExclusiveNB / isLockContended / lockSupported）退化为
//     「加锁恒成功、永不撞锁、lockSupported=false」：这是从
//     internal/agentd/flock_other.go 原样上移的既有决定（B34），调用方据
//     LockSupported() 打 Warn 明说保护未生效，而不是假装锁住了。改成报错会让
//     agentd 的 DataDir 单实例锁在 Windows 上直接启动失败，那是行为退化不是改进
//
// Windows 的实现已移到 platform_windows.go（B37）。本文件现在只覆盖
// plan9/js 等既非 unix 也非 windows 的平台，那里没有任何实现计划。
//
// 为什么这里只留骨架：这些平台没有本轮的进程承载实现计划，静默假装成功会比明确
// 报未实现更危险。
package prochost

import (
	"errors"
	"os"
	"time"
)

// errNotImplemented 是其它平台进程类原语的统一返回。
var errNotImplemented = errors.New("prochost: 本平台的进程承载尚未实现")

// lockSupported 标记本平台是否真的能加锁。
const lockSupported = false

// defaultFenceHardLimitMode 见 fence.go：本平台走 reserve_ratio，不用 TaskHardLimit。
const defaultFenceHardLimitMode = false

// flockExclusiveNB 非 unix 平台无 flock，空操作（见文件头「两类退化」）。
func flockExclusiveNB(*os.File) error { return nil }

// isLockContended 非 unix 平台永远撞不上锁——因为根本没加锁。
func isLockContended(error) bool { return false }

func spawnDetached(argv []string, dir string, shimLog *os.File) (int, error) {
	return 0, errNotImplemented
}

func killGroup(pid int) error { return errNotImplemented }

// killProc 非 unix 平台无 syscall.Kill（Windows 上它不存在），直接报未实现。
// 第二段清扫（rosterKill）拿到该错误只记一条日志并跳过这一条，不影响第一段。
func killProc(pid int) error { return errNotImplemented }

func createInputChannel(path string) error { return errNotImplemented }

func waitInputReader(path string, timeout time.Duration) (time.Duration, error) {
	return 0, errNotImplemented
}

// installProcessContainer 本平台无实现。
//
// 实际不可达：本平台的 spawnDetached 同样返回未实现，shim 根本起不来。
func installProcessContainer(int) error { return errNotImplemented }
