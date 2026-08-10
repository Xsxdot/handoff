//go:build unix

// flock_unix.go —— 单实例锁的平台原语（unix 实现）。
//
// 职责：把 flock(2) 的「非阻塞独占锁」与「锁已被占用」的 errno 判定，包成两个
// 平台无关的小函数供 lock.go 使用。
//
// 边界：不碰文件的打开与关闭，也不打日志——那些归 lock.go。
package agentd

import (
	"errors"
	"os"
	"syscall"
)

// flockSupported 标记本平台是否真的能加锁。
const flockSupported = true

// flockExclusiveNB 对一个已打开的文件取非阻塞独占锁。
//
// 注意：锁挂在「打开的文件描述」上而不是路径上。两个后果——同一进程内两次
// OpenFile 同一路径同样互斥（lock_test.go 据此免起子进程）；`rm` 掉锁文件
// 并不能解锁。
func flockExclusiveNB(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// isLockContended 判定错误是否为「锁已被他人持有」（LOCK_NB 下的 EWOULDBLOCK），
// 用于把撞锁与真正的 IO 故障分开——两者该给的错误信息完全不同。
func isLockContended(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK)
}
