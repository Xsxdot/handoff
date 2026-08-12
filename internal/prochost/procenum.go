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
//   - 非 darwin/linux 一律返回 errNotSupported，调用方据此降级，不猜值
package prochost

import "errors"

// errNotSupported 表示本平台没有进程枚举实现。
//
// 为什么要显式区分而不是返回空集：空集意味着「确实一个进程都没有」，
// 与「这个平台我们看不了」是两回事——后者必须让调用方降级为「未知」，
// 而不是渲染出一个 0 让人以为足迹是空的。
var errNotSupported = errors.New("本平台不支持进程枚举")

// procEntry 是一个进程的足迹相关属性。
//
// StartedAt 为 unix 纳秒，两个平台都归一到这个单位——身份校验要把成员的启动
// 时刻与 shim 的启动时刻直接比较，单位不统一这条判据就是错的。
type procEntry struct {
	PID       int
	PGID      int
	StartedAt int64
}
