//go:build !unix

// flock_other.go —— 单实例锁的平台原语（非 unix 退化实现）。
//
// 职责：让 lock.go 在没有 flock(2) 的平台上照常编译。
//
// 边界：**不提供任何保护**——加锁恒成功。调用方 lock.go 据 flockSupported
// 打 Warn 明说保护未生效，而不是假装锁住了。
package agentd

import "os"

// flockSupported 标记本平台是否真的能加锁。
const flockSupported = false

// flockExclusiveNB 非 unix 平台无 flock，空操作。
func flockExclusiveNB(*os.File) error { return nil }

// isLockContended 非 unix 平台永远撞不上锁——因为根本没加锁。
func isLockContended(error) bool { return false }
