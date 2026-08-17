//go:build windows

// platform_windows.go —— prochost 的 Windows 平台原语。
//
// 职责：detached spawn、Job Object 进程容器、按 job 回收、LockFileEx 存活锁。
//
// 边界：
//   - 只提供系统调用级能力，不含任何 handoff 业务语义
//   - 用 golang.org/x/sys/windows，不引 go-winio。为什么与 platform_unix.go
//     「不引 x/sys」的结论相反：unix 上 stdlib syscall 就够（Flock/Mkfifo/Kill
//     都在），不引是零成本；而 Windows 的 stdlib syscall 里 CreateNamedPipe /
//     LockFileEx / CreateJobObject 一个都没有，「只用 stdlib」的实际含义是在本仓库
//     里重写一份 x/sys。同一条原则（用最小够用的东西）在两个平台导出相反结论
//   - 输入通道（命名管道）不在本轮范围：它只在 claude 路径上，而 claude 在
//     Windows 上根本不注册（见 cmd/agentd.go 的 defaultAdapters）
package prochost

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// errNotImplemented 是本平台尚未实现的原语的统一返回。
//
// 本轮之后它只剩输入通道两个原语在用，且**实际不可达**：调用它们的唯一路径是
// claude adapter，而 Windows 上 claude 不进注册表，dispatch 在门口就被拒了。
// 这个不可达是被注册层挡出来的，不是碰巧——改注册表时要连带想到这里。
var errNotImplemented = errors.New("prochost: 本平台的进程承载尚未实现")

// lockSupported 标记本平台是否真的能加锁。
//
// Windows 的字节区间锁随句柄关闭而释放，而进程终止时句柄由系统关闭，因此
// 「内核在进程死亡时无条件释放」这条不变量与 unix 的 flock 一致——prochost
// 那套「不写 PID、不做进程探活、不提供 --force」的设计前提在本平台同样成立。
const lockSupported = true

// defaultFenceHardLimitMode 标记本平台的围栏值取自 TaskHardLimit 而非 reserve_ratio。
//
// 为什么 Windows 走另一套：reserve_ratio 的前提是「存在一个每用户进程数上限，
// 我们保留其中一部分」，而 Windows 没有 RLIMIT_NPROC 式的每用户硬上限（进程数
// 受内存与句柄约束）。详见 spec 11.6。
const defaultFenceHardLimitMode = true

// flockExclusiveNB 对一个已打开的文件取非阻塞独占锁。
//
// 锁 1 个字节即可：本包只用它做「有没有人持有」的存在性判据，不做区间互斥。
func flockExclusiveNB(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, new(windows.Overlapped))
}

// isLockContended 判定错误是否为「锁已被他人持有」。
//
// LOCKFILE_FAIL_IMMEDIATELY 下撞锁返回 ERROR_LOCK_VIOLATION(33)。必须与真正的
// IO 故障分开：撞锁是正常语义（对方还活着），IO 故障说明存活判据本身不可信。
func isLockContended(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}

func spawnDetached(argv []string, dir string, shimLog *os.File) (int, error) {
	return 0, errNotImplemented
}

func killGroup(pid int) error { return errNotImplemented }

func killProc(pid int) error { return errNotImplemented }

// createInputChannel / waitInputReader 见文件头：只在 claude 路径上，本轮不做。
func createInputChannel(path string) error { return errNotImplemented }

func waitInputReader(path string, timeout time.Duration) (time.Duration, error) {
	return 0, errNotImplemented
}
