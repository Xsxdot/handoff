//go:build !unix

// platform_other.go —— prochost 的非 unix 平台原语（A 期骨架）。
//
// 文件名用 _other 而非 _windows：Go 会把 _windows 后缀当成隐式 GOOS 约束，
// 与 //go:build !unix 相与后只覆盖 windows，plan9/js 等非 unix 平台会编译失败。
// 仓库既有的 flock_other.go / opennonblock_other.go 也是这个命名。
//
// 职责：让 prochost 在 GOOS=windows 下编译通过。
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
// B 期（独立立项）补齐：
//   - spawnDetached → CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS + Job Object
//   - killGroup → Job Object 的 TerminateJobObject
//   - createInputChannel → \\.\pipe\ 命名管道（go-winio）
//   - 锁原语 → LockFileEx（语义与 flock 一致：进程死亡内核释放）
//
// 为什么 A 期只留骨架：Windows 上四个 executor CLI 的可用性尚未验证，
// 进程层写完也无法端到端验收，违背本项目「每个 adapter 都真机端到端」的纪律。
package prochost

import (
	"errors"
	"os"
	"time"
)

// errNotImplemented 是 A 期非 unix 平台进程类原语的统一返回。
var errNotImplemented = errors.New("prochost: 本平台的进程承载尚未实现（A 期只提供骨架，见 B 期计划）")

// lockSupported 标记本平台是否真的能加锁。
const lockSupported = false

// flockExclusiveNB 非 unix 平台无 flock，空操作（见文件头「两类退化」）。
func flockExclusiveNB(*os.File) error { return nil }

// isLockContended 非 unix 平台永远撞不上锁——因为根本没加锁。
func isLockContended(error) bool { return false }

func spawnDetached(argv []string, dir string, shimLog *os.File) (int, error) {
	return 0, errNotImplemented
}

func killGroup(pid int) error { return errNotImplemented }

func createInputChannel(path string) error { return errNotImplemented }

func waitInputReader(path string, timeout time.Duration) (time.Duration, error) {
	return 0, errNotImplemented
}
