// lock.go —— 基于文件锁的进程存活凭据。
//
// 职责：
//   - AcquireLock：抢占一个路径上的排他锁，返回持锁句柄（持有到 Release 或进程退出）
//   - IsLocked：探测某个路径上的锁是否被人持有（prochost.Alive 的判据）
//   - LockSupported：报告本平台是否真的能加锁
//
// 边界：
//   - 平台原语在 platform_unix.go / platform_other.go，本文件只做逻辑与错误语义
//   - 不写 PID、不做进程探活、不提供 --force：flock 由内核在进程终止时释放
//     （正常退出/panic/SIGKILL/掉电皆然），不存在陈旧锁
//   - 不跨机器：flock 是本机语义
//
// 本文件由 internal/agentd/flock_unix.go / flock_other.go（B34）上移而来，
// 因为拆 tmux 后「文件锁」同时是 agentd 单实例保护与 executor 存活判定的基础，
// 两处各写一份是重复造轮子。agentd 侧的 AcquireDataDirLock 现在是本 API 的调用方。
package prochost

import (
	"errors"
	"fmt"
	"os"
)

// ErrLockHeld 表示锁已被其他进程持有（与真正的 IO 故障区分开：两者该给用户的
// 信息完全不同，调用方靠 errors.Is 判别，禁止按错误文本判）。
var ErrLockHeld = errors.New("锁已被其他进程持有")

// Lock 是一个已持有的文件锁，直到 Release 或进程退出。
type Lock struct{ f *os.File }

// LockSupported 报告本平台是否真的能加锁。
//
// 为什么要暴露：非 unix 平台上加锁是空操作，调用方需要据此打 Warn 明说
// 「保护未生效」，而不是让人误以为锁住了。
func LockSupported() bool { return lockSupported }

// AcquireLock 对 path 取非阻塞排他锁（文件不存在则以 0600 创建）。
//
// 返回：
//   - 持锁句柄；调用方须持有到不再需要为止
//   - 锁已被他人持有时返回包装了 ErrLockHeld 的错误
//   - 其他失败（打不开、文件系统不支持 flock）返回普通错误
//
// 注意：锁挂在「打开的文件描述」上，不在路径上——`rm` 掉锁文件不能解锁；
// 锁 fd 也不会被子进程继承（Go 的 exec 只传 ExtraFiles），因此锁精确代表本进程。
func AcquireLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("打开锁文件 %s: %w", path, err)
	}
	if err := flockExclusiveNB(f); err != nil {
		f.Close()
		if isLockContended(err) {
			return nil, fmt.Errorf("抢占锁 %s: %w", path, ErrLockHeld)
		}
		return nil, fmt.Errorf("给 %s 加锁: %w", path, err)
	}
	return &Lock{f: f}, nil
}

// Release 释放锁。重复调用安全（第二次直接返回 nil）。
//
// 生产侧可有可无——进程退出内核即释放；保留它是为了 defer 的习惯写法，
// 以及让测试能验证「释放后可重新获取」。
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	path := l.f.Name()
	err := l.f.Close() // 关闭 fd 即释放 flock，无需显式 LOCK_UN
	l.f = nil
	if err != nil {
		return fmt.Errorf("释放锁 %s: %w", path, err)
	}
	return nil
}

// IsLocked 报告 path 上的排他锁当前是否被某个进程持有。
//
// 实现：试着非阻塞抢锁——抢到说明没人持有（随即释放），撞锁说明有人持有。
// 文件不存在视为无人持有（返回 false，且**不建文件**：探测不应有副作用）。
func IsLocked(path string) (bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("打开锁文件 %s: %w", path, err)
	}
	defer f.Close()
	if err := flockExclusiveNB(f); err != nil {
		if isLockContended(err) {
			return true, nil
		}
		return false, fmt.Errorf("试锁 %s: %w", path, err)
	}
	return false, nil // 抢到了说明本来没人持有；defer 的 Close 会解锁
}
