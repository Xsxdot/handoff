//go:build darwin || linux

// fence_unix.go —— 进程围栏的类 Unix 实现（setrlimit RLIMIT_NPROC）。
//
// 职责：
//   - setNprocLimit：把当前进程的 RLIMIT_NPROC 软硬限一起压到给定值
//   - getNprocLimit：读回当前软限（自检与测试用）
//
// 边界：
//   - 只动**调用者自己**的 rlimit，不影响同 uid 的其它进程；子孙靠继承拿到
//   - 不决定围栏值取多少（那是 fence.go 的策略层），不判断该不该装
//   - 不 fork：setrlimit 是纯系统调用，这是本包零 fork 约束的一部分
package prochost

import (
	"fmt"
	"math"

	"golang.org/x/sys/unix"
)

// setNprocLimit 把当前进程的 RLIMIT_NPROC 软硬限一起压到 n。
//
// 参数：n 为围栏值（该 uid 可同时存在的进程数上限），必须为正数
//
// 返回：n 非正数、或 setrlimit 失败时返回错误。调用方应降级为「本次无围栏」
// 并继续，绝不因为防护装置装不上就中断业务。
//
// 注意：
//   - **软硬限必须同设**。只压软限的话，被围住的进程一句 setrlimit 就能把
//     软限抬回硬限，围栏形同虚设；硬限只能降不能升（升需特权），两者同设
//     即构成一扇单向门，executor 及其全部后代都拆不掉。
//   - 限值随 fork/exec 继承，且 **setsid 不重置它**——这正是围栏能覆盖
//     「逃逸出进程组的后代」的原因（2026-08-12 真机验证：进程 setsid 到
//     pid==sid==pgid 的完全独立会话后，rlimit 原样保留）。按进程组回收
//     收不到的那些树，在这里一个也跑不掉。
//   - 计数口径是**整个 uid**而不是本进程树：内核 fork 时拿「调用者自己的
//     限值」比「该 uid 当前活着的进程总数」。这个语义正是围栏能自动跨任务
//     合成的原因——所有 executor 树设同一个 L，uid 总数就不会因它们的
//     fork 越过 L，不需要任何任务间协调。
//   - linux 的 RLIMIT_NPROC 把线程也算进去，而本包 enumProcs 只数进程，
//     两边口径不同，高水位判定在 linux 上会偏乐观。darwin（当前唯一的真实
//     部署平台）只数进程，无此偏差。差异是已知的，靠保留额的余量吸收。
//   - 本函数刻意不打日志：调用方（shim）在安装边界上统一记录围栏值与结果，
//     这里再记一遍等于同一件事写两次。
func setNprocLimit(n int) error {
	if n <= 0 {
		return fmt.Errorf("围栏值必须为正数，得到 %d", n)
	}
	rl := unix.Rlimit{Cur: uint64(n), Max: uint64(n)}
	if err := unix.Setrlimit(unix.RLIMIT_NPROC, &rl); err != nil {
		return fmt.Errorf("setrlimit RLIMIT_NPROC=%d: %w", n, err)
	}
	return nil
}

// getNprocLimit 读当前进程的 RLIMIT_NPROC 软限。
//
// 返回：软限值；getrlimit 失败时返回错误。
//
// 注意：无限大（RLIM_INFINITY）会被钳到 math.MaxInt32——直接 int() 转换会
// 得到 -1，那个负数一路流下去会把「无上限」变成「上限为负」，比钳值危险得多。
func getNprocLimit() (int, error) {
	var rl unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NPROC, &rl); err != nil {
		return 0, fmt.Errorf("getrlimit RLIMIT_NPROC: %w", err)
	}
	if rl.Cur > uint64(math.MaxInt32) {
		return math.MaxInt32, nil
	}
	return int(rl.Cur), nil
}
