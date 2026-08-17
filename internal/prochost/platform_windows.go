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
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

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

// jobHandle 是 shim 持有的 Job Object 句柄。
//
// 为什么是包级 var 而不是返回给调用方：这个句柄**必须活到 shim 进程结束**，
// KILL_ON_JOB_CLOSE 的语义正是「最后一个句柄关闭时收掉全部成员」。交给调用方
// 持有就会出现「有人 defer Close 了它」的可能，而那等于当场杀掉执行者。
var jobHandle windows.Handle

// installProcessContainer 建 Job Object、设限制、把 shim 自己放进去。
//
// 参数：nprocLimit 为围栏值（执行者树的进程数上限）；<=0 表示不设进程数上限。
//
// 返回：任何一步失败都返回 error——见 shim.go 调用点的注释，Windows 上容器建不
// 起来意味着没有任何回收能力。
//
// 三处关键取舍：
//   - **job 无条件建**，即便 nprocLimit<=0。job 的首要用途是 KILL_ON_JOB_CLOSE
//     连坐回收，围栏只是搭车；照搬 unix 那个 `if limit > 0` 的闸门会把回收能力
//     一起跳过（spec 4.4.1）
//   - **job 必须归 shim 自己持有**。若由 agentd 建并持句柄，agentd 一重启句柄就
//     关，KILL_ON_JOB_CLOSE 当场收掉执行者，B36 的招牌属性「执行者活过 agentd
//     重启」当场失效（spec 4.2）
//   - **ActiveProcessLimit 要 +1**。它计的是 job 内进程数，而 shim 自己也在 job
//     里；策略层算出的值语义是「执行者树的进程数」（spec 4.4.3）
func installProcessContainer(nprocLimit int) error {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		log().Error("创建 Job Object 失败", "cause", err)
		return fmt.Errorf("创建 Job Object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if nprocLimit > 0 {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS
		info.BasicLimitInformation.ActiveProcessLimit = uint32(nprocLimit + 1)
	}
	if _, err := windows.SetInformationJobObject(h,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(h)
		log().Error("设置 Job Object 限制失败", "limit", nprocLimit, "cause", err)
		return fmt.Errorf("设置 Job Object 限制: %w", err)
	}
	self, err := windows.GetCurrentProcess()
	if err != nil {
		windows.CloseHandle(h)
		log().Error("取当前进程句柄失败", "cause", err)
		return fmt.Errorf("取当前进程句柄: %w", err)
	}
	if err := windows.AssignProcessToJobObject(h, self); err != nil {
		windows.CloseHandle(h)
		log().Error("把 shim 放进 Job Object 失败", "cause", err)
		return fmt.Errorf("assign shim 进 Job Object: %w", err)
	}
	jobHandle = h // 故意不 Close：见 jobHandle 的注释
	if nprocLimit > 0 {
		log().Info("进程容器已安装", "kind", "job_object",
			"active_process_limit", nprocLimit+1, "fence", nprocLimit)
	} else {
		log().Info("进程容器已安装", "kind", "job_object", "active_process_limit", "未设",
			"reason", "spec 未下发围栏值")
	}
	return nil
}

// spawnDetached 以脱离本进程的方式拉起 shim，返回其 pid。
//
// 参数：
//   - argv: 完整命令行，argv[0] 必须是绝对路径（本函数不做 PATH 查找）
//   - dir: shim 的工作目录
//   - shimLog: shim 自身 stdout/stderr 的落盘文件
//
// 返回：shim 的 pid；error 非 nil 时没有进程被拉起。
//
// **CREATE_BREAKAWAY_FROM_JOB 是招牌属性在 Windows 上的承重点。** agentd 常常
// 就跑在别人的 job 里（计划任务会放，Windows OpenSSH 的会话也会放——后者已实测：
// 经 ssh 起的 agentd 在会话结束时被连坐杀掉）。若外层 job 带 KILL_ON_JOB_CLOSE，
// agentd 一停，shim 就跟着被外层 job 收掉，「执行者活过 agentd 重启」当场失效。
// 所以先尝试脱离父 job；被拒时回落并**大声说明**降级了什么。
func spawnDetached(argv []string, dir string, shimLog *os.File) (int, error) {
	const baseFlags = windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP
	start := func(flags uint32) (*exec.Cmd, error) {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Dir = dir
		cmd.Stdout = shimLog
		cmd.Stderr = shimLog
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
		return cmd, cmd.Start()
	}
	cmd, err := start(baseFlags | windows.CREATE_BREAKAWAY_FROM_JOB)
	if err != nil {
		// 父 job 不允许 breakaway 时 CreateProcess 返回 ERROR_ACCESS_DENIED。
		// 这不是致命错误，但降级的后果必须让人看见。
		log().Warn("脱离父 job 失败，回落为不脱离；本机上执行者不保证活过 agentd 重启",
			"bin", argv[0], "cause", err)
		cmd, err = start(baseFlags)
		if err != nil {
			log().Error("拉起 shim 失败", "bin", argv[0], "dir", dir, "cause", err)
			return 0, fmt.Errorf("拉起 shim %s: %w", argv[0], err)
		}
		log().Info("shim 已拉起（未脱离父 job）", "pid", cmd.Process.Pid, "bin", argv[0])
		return cmd.Process.Pid, nil
	}
	log().Info("shim 已拉起（已脱离父 job）", "pid", cmd.Process.Pid, "bin", argv[0])
	return cmd.Process.Pid, nil
}

// killGroup 回收 shim 及其全部后代。
//
// 参数：pid 为 shim 的 pid。
//
// 实现上只杀 shim 一个进程——shim 的 job 句柄随其进程终止而关闭，
// KILL_ON_JOB_CLOSE 让内核收掉 job 内剩下的全部成员。所以一个裸 pid 就够，
// 不需要 OpenJobObject（它恰好是 x/sys/windows 唯一缺的那个函数）。
//
// 与 unix 的一处刻意差异：unix 上 shim 死后执行者被 init 收养继续跑（存活锁已释放
// → Alive() 报 false，模型与现实不符，是现存的一处 wart）；Windows 上整棵树跟着死，
// **现实与模型反而对得上**。这是变好不是变差，别当 bug 修回去。
func killGroup(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		log().Error("打开 shim 进程句柄失败", "pid", pid, "cause", err)
		return fmt.Errorf("打开进程 %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		log().Error("终止 shim 失败", "pid", pid, "cause", err)
		return fmt.Errorf("终止进程 %d: %w", pid, err)
	}
	log().Info("已终止 shim，job 将连坐收掉整棵树", "pid", pid)
	return nil
}

// killProc 终止单个进程（名册点名清扫用）。
//
// 与 killGroup 的区别只在语义：这里的 pid 是一个具体后代而非 shim，不期待连坐。
func killProc(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		log().Error("打开单个进程句柄失败", "pid", pid, "cause", err)
		return fmt.Errorf("打开进程 %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		log().Error("终止单个进程失败", "pid", pid, "cause", err)
		return fmt.Errorf("终止进程 %d: %w", pid, err)
	}
	log().Info("已终止单个进程", "pid", pid)
	return nil
}

// createInputChannel / waitInputReader 见文件头：只在 claude 路径上，本轮不做。
func createInputChannel(path string) error {
	log().Error("Windows 输入通道尚未实现", "path", path)
	return errNotImplemented
}

func waitInputReader(path string, timeout time.Duration) (time.Duration, error) {
	log().Error("Windows 输入通道尚未实现", "path", path, "timeout", timeout)
	return 0, errNotImplemented
}
