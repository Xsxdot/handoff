// lock.go —— agentd 的 DataDir 单实例锁。
//
// 职责：
//   - AcquireDataDirLock：对 <DataDir>/agentd.lock 取非阻塞独占文件锁，
//     保证一个数据目录同时只被一个 agentd 接管
//   - 撞锁时给出可行动的错误（指向 handoff status），而不是一句「失败」
//
// 边界：
//   - 不做仓库级互斥：agentd 不是 repo-scoped，proto.Task.RepoPath 是每任务
//     字段，启动时没有「仓库」这个键可锁
//   - 不管陈旧锁：flock 由内核在进程终止时释放（正常退出/panic/SIGKILL/掉电
//     皆然），因此不写 PID、不做进程探活、不提供 --force 逃生口
//   - 不跨机器：flock 是本机语义，两台机器各跑各的 agentd 是 handoff 的正常形态
//   - 加锁原语与错误语义在 internal/prochost（B34 的 flock_unix.go / flock_other.go
//     上移而来，全项目只保留这一份实现），本文件只做日志与文案
package agentd

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/Xsxdot/handoff/internal/prochost"
)

// lockFileName 是 DataDir 下单实例锁文件的名字。
const lockFileName = "agentd.lock"

// lockHeldMsg 是撞锁时给用户看的完整文案（%s 填 DataDir）。
//
// 为什么不报「持有者是谁」：那要求往锁文件里写 PID 再读回，而读到的内容随时
// 可能是陈旧的（写入与读取之间对方进程可能已死）——为一条诊断信息重新引入
// 本已被 flock 消除的状态，不划算。指向 handoff status 是更可靠的答案。
//
// 为什么最后两行是重点：只说「被占了」等于把人堵在门口；给出下一步动作才有用。
const lockHeldMsg = `数据目录 %s 已被另一个 agentd 占用（` + lockFileName + `）。
同一个数据目录同时只能有一个 agentd——两个进程会抢同一份 SQLite、
同一批 worktree 与执行者进程，正是状态机最怕的失配。
先看现役那个是谁：handoff status
它能用就直接复用，不要再起一个。`

// DataDirLock 持有一个 DataDir 的独占权，直到 Release 或进程退出。
type DataDirLock struct {
	l   *prochost.Lock
	log *slog.Logger
}

// AcquireDataDirLock 对 <dataDir>/agentd.lock 取非阻塞独占锁。
//
// 参数：
//   - dataDir: 数据目录，调用方须保证它已存在（agentd 侧由 os.MkdirAll 保证）
//   - log: 日志入口，nil 时退回 slog.Default()
//
// 返回：
//   - 持有锁的句柄，调用方须一直持有到进程结束
//   - error: 已被另一个 agentd 占用时，错误文本是一段完整的可行动指引
//     （含 dataDir 与 `handoff status`），调用方直接原样返回即可，不要再包一层
//
// 注意：
//   - **必须在 store.Open 之前调用**。别指望端口冲突挡住第二个 agentd——
//     ListenAndServe 是 agentd 启动流程的最后一条语句，在它之前 RecoverOnStartup
//     已经对在役 agentd 的活执行器重建了订阅并写入状态迁移；SQLite 开了 WAL
//     也不拦多进程打开。破坏发生在撞端口之前
func AcquireDataDirLock(dataDir string, log *slog.Logger) (*DataDirLock, error) {
	if log == nil {
		log = slog.Default()
	}
	path := filepath.Join(dataDir, lockFileName)
	if !prochost.LockSupported() {
		// 明说保护未生效，而不是让人误以为锁住了
		log.Warn("本平台不支持文件锁，agentd 单实例保护未生效", "data_dir", dataDir)
	}
	l, err := prochost.AcquireLock(path)
	if err != nil {
		if errors.Is(err, prochost.ErrLockHeld) {
			log.Error("数据目录已被另一个 agentd 占用，拒绝启动",
				"data_dir", dataDir, "path", path)
			return nil, fmt.Errorf(lockHeldMsg, dataDir)
		}
		// 非撞锁的加锁失败（如打不开文件、文件系统不支持 flock）：这是环境问题，
		// 与「已被占用」是两码事，不能套用那段指引文案误导用户
		log.Error("获取单实例锁失败", "path", path, "cause", err)
		return nil, err
	}
	if prochost.LockSupported() {
		log.Info("已取得数据目录单实例锁", "data_dir", dataDir, "path", path)
	} else {
		log.Info("数据目录锁文件已就位，但本平台不提供互斥保护",
			"data_dir", dataDir, "path", path)
	}
	return &DataDirLock{l: l, log: log}, nil
}

// Release 释放锁。
//
// 生产侧可有可无——进程退出内核即释放；保留它是为了让测试能验证「释放后可重新
// 获取」，以及 defer 的习惯写法。重复调用是安全的（第二次直接返回 nil）。
func (l *DataDirLock) Release() error {
	if l == nil || l.l == nil {
		return nil
	}
	err := l.l.Release()
	if err != nil {
		l.log.Warn("释放数据目录单实例锁失败", "cause", err)
		return err
	}
	l.log.Debug("已释放数据目录单实例锁")
	return nil
}
