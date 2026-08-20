// procenum.go —— 进程枚举与上限的平台无关契约。
//
// 职责：
//   - 定义 procEntry：一个进程的 pid / pgid / 启动时刻（unix 纳秒）
//   - 声明两个平台原语的契约：enumProcs（当前 uid 的全部进程）、procLimit（每 uid 上限）
//
// 边界：
//   - 只负责「读」，不发任何信号、不做任何判断：谁属于哪个任务是 footprint.go 的事
//   - **实现一律不得 fork**（禁止 ps/lsof）：这套代码要在机器已经 fork 不动的时候
//     仍然可用，否则它会在最需要它的那一刻恰好失灵——2026-08-12 devbox 整机 fork
//     瘫痪时，所有基于 exec 的诊断手段全部失效，正是这条约束的由来
//   - 非 darwin/linux 一律返回 ErrNotSupported，调用方据此降级，不猜值
package prochost

import (
	"errors"
	"sync"
)

// ProcessCredential 是一个进程的可验证身份凭据。
//
// PID 单独不能证明身份：进程退出后操作系统会复用它。StartedAt 与 PID 一起构成
// 本包已有的 pid 复用防线；0 表示没有拿到内核启动时刻，调用方必须把它当成未知。
type ProcessCredential struct {
	PID       int
	StartedAt int64
}

// PtyhostCredentialProvider 返回当前 agentd 已登记的 ptyhost 身份凭据。
//
// 参数：无。
// 返回：已由会话目录锁与 meta.json 认证过的 ptyhost 进程凭据；未知身份不得返回。
// 注意：该回调只服务机器级进程压力统计，不改变任务级进程名册与 RLIMIT_NPROC。
type PtyhostCredentialProvider func() []ProcessCredential

var (
	ptyhostCredentialMu       sync.RWMutex
	ptyhostCredentialProvider PtyhostCredentialProvider
)

// SetPtyhostCredentialProvider 设置机器级进程压力统计的 ptyhost 凭据来源。
//
// 参数：provider 是当前 agentd 的凭据提供者；传 nil 表示不排除任何进程。
// 返回：无。
// 注意：agentd 启动时设置一次；调用方必须保证 provider 返回的是仍然持有会话锁的
// ptyhost。未知或过期凭据不能用于扣减统计，否则会把 PID 复用误判成 ptyhost。
func SetPtyhostCredentialProvider(provider PtyhostCredentialProvider) {
	ptyhostCredentialMu.Lock()
	ptyhostCredentialProvider = provider
	ptyhostCredentialMu.Unlock()
}

// PtyhostCredentials 返回当前已登记的 ptyhost 凭据快照。
//
// 返回：提供者给出的凭据切片；未设置提供者时为 nil。
// 注意：这是只读视图，供调用方核对「某个真实会话是否已成为可验证凭据」。
// **不要拿它自己做扣减** —— 扣减判据（PID 与启动时刻必须同时匹配）在
// chargeableProcessCount 里，绕过它会把 PID 复用误判成 ptyhost。
func PtyhostCredentials() []ProcessCredential {
	provider := currentPtyhostCredentialProvider()
	if provider == nil {
		return nil
	}
	return provider()
}

func currentPtyhostCredentialProvider() PtyhostCredentialProvider {
	ptyhostCredentialMu.RLock()
	provider := ptyhostCredentialProvider
	ptyhostCredentialMu.RUnlock()
	return provider
}

// ProcessCredentialForPID 读取一个当前 uid 进程的内核启动时刻。
//
// 参数：pid 是要查的进程号。
// 返回：找到时返回 PID 与启动时刻及 true；进程不存在、枚举失败或启动时刻缺失时
// 返回零值与 false。
// 注意：调用方应把 false 当成“无法证明身份”，不能因此排除该进程。
func ProcessCredentialForPID(pid int) (ProcessCredential, bool) {
	if pid <= 0 {
		return ProcessCredential{}, false
	}
	procs, err := enumProcsFn()
	if err != nil {
		return ProcessCredential{}, false
	}
	for _, proc := range procs {
		if proc.PID == pid && proc.StartedAt > 0 {
			return ProcessCredential{PID: proc.PID, StartedAt: proc.StartedAt}, true
		}
	}
	return ProcessCredential{}, false
}

// ErrNotSupported 表示本平台没有进程枚举实现。
//
// 为什么要显式区分而不是返回空集：空集意味着「确实一个进程都没有」，
// 与「这个平台我们看不了」是两回事——后者必须让调用方降级为「未知」，
// 而不是渲染出一个 0 让人以为足迹是空的。
//
// **为什么导出**：跨包的调用方（agentd 的终态清扫）必须能把它与真正的清扫
// 失败区分开。不导出时那边只能把 err 原文塞进用户可见的告警里，于是 Windows
// 上每个任务收尾都报一次「残留进程清扫失败，请人工处理」——而那台机器的回收
// 由 Job Object 连坐承担，根本没有残留（B148）。与 ptyhost.ErrNotSupported、
// 本包 ErrExecutorAlive 同一形态：平台能力缺失是结论，不是故障。
var ErrNotSupported = errors.New("本平台不支持进程枚举")

// procEntry 是一个进程的足迹相关属性。
//
// StartedAt 为 unix 纳秒，两个平台都归一到这个单位——身份校验要把成员的启动
// 时刻与 shim 的启动时刻直接比较，单位不统一这条判据就是错的。
//
// PPID 是出生登记（roster）唯一的链接字段：setsid 改得了 pgid/sid，改不了
// ppid。进程树活着时沿它能闭包出全部后代；树一死 ppid 就断（后代被 reparent
// 给 init/launchd），所以它只在**记账时**可用，不能在清扫时才去追——这正是
// 「出生登记」要在活着的时候落盘的原因。
type procEntry struct {
	PID       int
	PPID      int
	PGID      int
	StartedAt int64
}
