//go:build unix

// platform_unix.go —— prochost 的 unix 平台原语。
//
// 职责：detached spawn（新会话 + 新进程组）、flock 加锁与撞锁判定、进程组回收、
// FIFO 输入通道。
//
// 边界：
//   - 只提供系统调用级能力，不含任何 handoff 业务语义；被 prochost.go / lock.go /
//     shim.go 调用
//   - 只用 stdlib syscall，不引 golang.org/x/sys——本仓库既有的三处平台切分
//     （opennonblock_*、workspace_procgroup_*、原 agentd/flock_*）都是这个套路，
//     且实测所需常量与函数在 darwin/linux 的 syscall 里齐备，多一个直接依赖不划算
package prochost

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// lockSupported 标记本平台是否真的能加锁。
const lockSupported = true

// defaultFenceHardLimitMode 见 fence.go：本平台走 reserve_ratio，不用 TaskHardLimit。
const defaultFenceHardLimitMode = false

// flockExclusiveNB 对一个已打开的文件取非阻塞独占锁。
//
// 注意：锁挂在「打开的文件描述」上而不是路径上。两个后果——同一进程内两次
// OpenFile 同一路径同样互斥（测试据此免起子进程）；`rm` 掉锁文件并不能解锁。
// （本函数由 internal/agentd/flock_unix.go 原样上移，行为不变。）
func flockExclusiveNB(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// isLockContended 判定错误是否为「锁已被他人持有」（LOCK_NB 下的 EWOULDBLOCK），
// 用于把撞锁与真正的 IO 故障分开——两者该给的错误信息完全不同。
func isLockContended(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK)
}

// spawnDetached 拉起 argv 并让它脱离当前进程的会话与进程组，返回其 pid。
//
// 参数：
//   - argv: 完整命令行，[0] 必须是绝对路径
//   - dir: 子进程工作目录
//   - shimLog: 非 nil 时把子进程的 **stderr** 接到这个文件（shim 的日志落点，
//     生产里是 <taskDir>/shim.log）。nil 时 stderr 照旧接 /dev/null。stdin/stdout
//     恒为 /dev/null——它们与「日志落点」是两回事
//
// 为什么 stderr 单独开一个通道而不是沿用「全 nil 即 detach」：shim 用 slog 记
// 「围栏已安装 / 撞墙归因」等关键行，slog 默认写 stderr；全 nil 会把它们一并丢进
// /dev/null，撞墙时协调者在任务目录里什么都读不到。
//
// 为什么用 Setsid 而不是让子进程自己调 setsid(2)：子进程被 fork 出来时若已是
// 进程组组长，setsid(2) 会返回 EPERM。由父进程在 SysProcAttr 里声明最干净，
// 且一次系统调用同时拿到「新会话 + 新进程组（pgid == pid）」——后者是 Kill
// 能按组连坐全部后代的前提。
//
// 为什么 stdio 默认全部置 nil：Go 会把它们接到 /dev/null。子进程不能持有本进程的
// 任何 fd，否则 agentd 退出时管道破裂会波及它，detach 就名存实亡。
//
// 为什么起一个 goroutine 收尸而不是 Process.Release：Release 只释放 Go 侧的
// 进程句柄，**不改变内核里的父子关系**——被拉起的进程仍是本进程的亲儿子，死后
// 没人 waitpid 就变成僵尸，占着 pid 槽位直到本进程退出。agentd 是常驻服务，
// 每停一个执行者漏一个僵尸，等于按任务数缓慢泄漏 pid（08-10 真机实测：stop 之后
// `ps` 里留下 `[handoff] <defunct>`，父进程正是 agentd）。收尸不影响 detach：
// 会话与进程组早在 Setsid 时就分开了，agentd 若先死，内核把它交给 init 接管。
//
// 边界：本函数不脱离 cgroup——cgroup 归属由 fork 继承，setsid 改不了它。
// systemd 托管场景必须在 unit 里设 KillMode=process，否则 systemctl restart
// 仍会连坐（见 spec §3.3 与 Task 10 的 unit 模板）。
func spawnDetached(argv []string, dir string, shimLog *os.File) (int, error) {
	if len(argv) == 0 {
		return 0, errors.New("argv 为空")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, shimLog
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		if note, _ := ExplainForkFailure(err); note != "" {
			log().Error("拉起 shim 失败（进程配额）", "note", note, "cause", err)
			return 0, fmt.Errorf("%s: %w", note, err)
		}
		return 0, fmt.Errorf("拉起 %s: %w", argv[0], err)
	}
	pid := cmd.Process.Pid
	go func() {
		// 只为收尸，退出码在这里没有意义（真正的退出语义由 shim 的哨兵承载）。
		// 阻塞时长 = 执行者寿命，一个任务一个 goroutine，量级与任务数同阶。
		if err := cmd.Wait(); err != nil {
			log().Debug("shim 已退出", "pid", pid, "cause", err)
			return
		}
		log().Debug("shim 已退出", "pid", pid, "cause", nil)
	}()
	return pid, nil
}

// killGroup 向 pid 所在进程组发送 SIGKILL（负 pid 表示按组发送）。
//
// 幂等：组已不存在时内核返回 ESRCH，视为已回收成功。
// 调用方（prochost.Kill）必须先确认存活锁仍被持有才可调用本函数——
// 对已回收的 pid 发信号有误杀被复用 pid 的风险。
func killGroup(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return fmt.Errorf("kill -9 -%d: %w", pid, err)
	}
	return nil
}

// killProc 对**单个 pid** 发 SIGKILL（不是进程组）。
//
// 为什么不能复用 killGroup：第二段清扫的对象是 setsid 逃逸出去的进程，它们
// 各自成组，组里往往还有它们自己的无关兄弟；按组发信号会把没经过身份校验的
// 进程一起带走——那正是 B47 误杀的形态。名册逐条校验、逐条发信号，一条一条来。
func killProc(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("非法 pid %d", pid)
	}
	return syscall.Kill(pid, syscall.SIGKILL)
}

// createInputChannel 幂等创建 0600 命名管道（见 CreateInputChannel 的文档）。
func createInputChannel(path string) error {
	err := syscall.Mkfifo(path, 0o600)
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.EEXIST) {
		return fmt.Errorf("mkfifo %s: %w", path, err)
	}
	fi, serr := os.Stat(path)
	if serr != nil {
		return fmt.Errorf("stat %s: %w", path, serr)
	}
	// 残留的普通文件会让 shim 的 O_RDWR 打开变成普通文件读写，子进程 stdin
	// 立刻 EOF——症状是「executor 起来了但一句话都不回」，极难排查，必须显式失败
	if fi.Mode()&os.ModeNamedPipe == 0 {
		return fmt.Errorf("%s 已存在但不是命名管道", path)
	}
	return nil
}

// waitInputReader 轮询探测 FIFO 读端（见 WaitInputReader 的文档）。
func waitInputReader(path string, timeout time.Duration) (time.Duration, error) {
	deadline := time.Now().Add(timeout)
	start := time.Now()
	for {
		f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			f.Close()
			return time.Since(start), nil
		}
		// ENXIO 之外的错误（管道缺失、权限）重试无意义，立即失败
		if !errors.Is(err, syscall.ENXIO) {
			return time.Since(start), fmt.Errorf("探测 %s 读端: %w", path, err)
		}
		if time.Now().After(deadline) {
			return time.Since(start), fmt.Errorf("%s 在 %s 内未出现读端", path, timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// writeInputChannel 往 FIFO 投递字节（见 WriteInputChannel 的文档）。
//
// O_NONBLOCK 不是性能选择而是语义选择：没有它，打开写端会一直阻塞到出现读端，
// 「执行者已不在」就变成「永远等下去」。
func writeInputChannel(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("打开输入通道 %s（读端可能已不在）: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("写输入通道 %s: %w", path, err)
	}
	return nil
}

// installProcessContainer 在 spawn 执行者之前，把当前进程（shim）放进本平台的
// 「进程容器」里，使执行者全树继承其约束。
//
// 参数：nprocLimit 为围栏值（执行者树的进程数上限）；<=0 表示不设围栏。
//
// 返回：error 非 nil 时 shim 必须放弃拉起执行者。
//
// unix 的容器就是 RLIMIT_NPROC——rlimit 随 fork 继承，所以装在 shim 上等于装在
// 整棵树上。**装不上不阻断**：防护装置故障不该变成拒绝服务，这是 B73 定的语义，
// 本次泛化不改变它（与 Windows 侧相反，见 platform_windows.go 的同名函数）。
func installProcessContainer(nprocLimit int) error {
	if nprocLimit <= 0 {
		log().Info("本任务未设进程围栏", "reason", "spec 未下发围栏值")
		return nil
	}
	if err := setNprocLimit(nprocLimit); err != nil {
		log().Warn("安装进程围栏失败，本任务无围栏保护", "limit", nprocLimit, "cause", err)
		return nil
	}
	log().Info("进程围栏已安装", "limit", nprocLimit)
	return nil
}
