// fence.go —— 进程围栏的策略层：算围栏值、判读余量、翻译 EAGAIN。
//
// 职责：
//   - fenceLimit：由系统上限与保留额算出围栏值 L
//   - CheckAdmission：报告当前余量，供准入闸与高水位告警判读
//   - ExplainForkFailure：把 EAGAIN 翻译成「配额耗尽」还是「原因未知」
//
// 边界：
//   - 不安装围栏（那是 setNprocLimit + shim），不决定拒不拒发（那是 agentd 的策略）
//   - 不修改任何任务状态、不发事件
//   - **一律不 fork**：本文件所有读数走 sysctl / /proc。这套代码要在机器已经
//     fork 不动的时候仍然可用——2026-08-12 devbox 瘫痪时连 `ps | wc -l` 都起
//     不来，当时所有基于 exec 的诊断手段同时失效，正是这条约束的由来
package prochost

import (
	"errors"
	"fmt"
	"syscall"
)

// errFenceNotSupported 表示本平台没有进程围栏实现。
//
// 为什么要与 errNotSupported（进程枚举）分开：两者可以独立缺失，混用会让
// 「数得出但围不住」这种真实存在的状态没法表达。
var errFenceNotSupported = errors.New("本平台不支持进程围栏")

// 围栏策略。包级 var 而非常量：agentd 启动时由 config 经 SetFencePolicy 注入一次。
var (
	fenceDisabled     bool
	fenceReserveRatio = 0.1
)

// fenceReserveFloor 是保留额的下限。
//
// 为什么要有下限：保留额的用途是「救护车道」——保证 agentd、sshd、登录 shell、
// 一次 ps 永远起得来。比例在小机器上会算出个位数，那样的车道等于没有。
const fenceReserveFloor = 200

// fenceWatermarkRatio 是「贴着上限」的判定线：达到参考上限的九成即为高水位。
//
// 为什么是九成而不是满：满了才告警等于没告警——那时已经在撞墙了。九成留出
// 的余量足够审核者收到事件、看一眼、决定要不要收敛。
const fenceWatermarkRatio = 0.9

// procLimitFn 是读系统上限的测试缝（与 enumProcsFn 同款路数）。
// **生产路径恒为 procLimit**，非测试代码不得赋值。
var procLimitFn = procLimit

// getNprocLimitFn 是读「本进程当前实际生效的 RLIMIT_NPROC 软限」的测试缝。
// **生产路径恒为 getNprocLimit**，非测试代码不得赋值。
//
// 为什么归因要用它而不是 procLimitFn：procLimitFn 读的是系统上限
// （kern.maxprocperuid），而围栏把**当前进程**的软限压到 spec.NprocLimit——
// shim 是独立进程、从不调用 SetFencePolicy，策略层默认 L 与它实际装上的围栏
// 可能完全不同（2026-08-12 烟测实证：装了 100，归因却按 2400 算，把确定的
// 配额耗尽判成「不像配额问题」）。fork 失败正是内核拿「调用者自己的软限」比
// 「uid 进程总数」得出的，归因必须读软限本身，而不是按 ratio 重算。
var getNprocLimitFn = getNprocLimit

// SetFencePolicy 注入围栏策略，由 agentd 启动时按 config 调用一次。
//
// 参数：
//   - disabled: true 时完全不装围栏（逃生开关）
//   - reserveRatio: 保留额占系统上限的比例；不在 (0,1) 区间时保留默认值 0.1
//
// 注意：本函数只改包级策略，不会影响已经拉起的 shim——它们的围栏在 fork 那
// 一刻就定死了，改策略只对之后启动的任务生效。
func SetFencePolicy(disabled bool, reserveRatio float64) {
	fenceDisabled = disabled
	if reserveRatio > 0 && reserveRatio < 1 {
		fenceReserveRatio = reserveRatio
	}
	log().Info("进程围栏策略已设定", "disabled", fenceDisabled,
		"reserve_ratio", fenceReserveRatio)
}

// fenceLimit 算出应安装的围栏值 L。
//
// 返回：
//   - L > 0: 应安装的围栏值
//   - L == 0 且 err == nil: 策略关闭，或系统上限小到留不出保留额——两种都是
//     「本次不设围栏」的正常结论，**不是错误**
//   - err != nil: 读不到系统上限
//
// 取法是「贴天花板留救护车道」，不是「给 executor 节流」：保留额只要够
// agentd/sshd/登录 shell 活着即可。压得更低不增加安全性，只会让 executor 更
// 早撞墙、让审核者更容易把配额问题误判成代码问题——一个会误导的防护比没有
// 防护更糟。
func fenceLimit() (int, error) {
	if fenceDisabled {
		return 0, nil
	}
	limit, err := procLimitFn()
	if err != nil {
		log().Warn("读不到系统进程上限，本次不设围栏", "cause", err)
		return 0, err
	}
	reserve := int(float64(limit) * fenceReserveRatio)
	if reserve < fenceReserveFloor {
		reserve = fenceReserveFloor
	}
	if reserve >= limit {
		log().Warn("系统进程上限过小，留不出保留额，本次不设围栏",
			"limit", limit, "reserve", reserve)
		return 0, nil
	}
	return limit - reserve, nil
}

// fenceReference 返回余量判读的参考上限：围栏已启用时为 L，否则退回系统上限。
//
// 为什么参考上限不能恒用系统上限：装了围栏之后，executor 的实际天花板是 L，
// 拿 2666 去算水位会让「已经贴着围栏」显示成「才用了九成的九成」，高水位
// 告警永远不触发。
func fenceReference() (int, error) {
	l, err := fenceLimit()
	if err != nil {
		return 0, err
	}
	if l > 0 {
		return l, nil
	}
	return procLimitFn()
}

// Admission 是一次余量判读的结果。
//
// 字段说明：
//   - Used: 当前 uid 的进程数
//   - Limit: 参考上限（围栏值或系统上限）
//   - Known: 读数是否可信；false 时 Used/Limit 无意义，不得据此做任何判断
type Admission struct {
	Used  int  `json:"used"`
	Limit int  `json:"limit"`
	Known bool `json:"known"`
}

// Full 报告余量是否已经耗尽。读数不可信时恒为 false（fail-open）。
func (a Admission) Full() bool { return a.Known && a.Used >= a.Limit }

// NearFull 报告是否已达高水位（参考上限的九成）。读数不可信时恒为 false。
func (a Admission) NearFull() bool {
	return a.Known && float64(a.Used) >= float64(a.Limit)*fenceWatermarkRatio
}

// CheckAdmission 零 fork 读一次当前余量。
//
// 返回：Admission；任何一步读不到数都返回零值（Known=false）。
//
// 注意：读不到数时**不报错也不猜 0**，而是让调用方 fail-open 照常放行。
// 为「量不出来」而拒绝派发，会让 handoff 在不支持的平台上彻底不能用，
// 代价远大于收益——防护装置故障不该变成拒绝服务。
func CheckAdmission() Admission {
	procs, err := enumProcsFn()
	if err != nil {
		log().Debug("余量判读失败（枚举进程），按未知处理", "cause", err)
		return Admission{}
	}
	ref, err := fenceReference()
	if err != nil || ref <= 0 {
		log().Debug("余量判读失败（参考上限不可用），按未知处理", "cause", err, "ref", ref)
		return Admission{}
	}
	return Admission{Used: len(procs), Limit: ref, Known: true}
}

// ExplainForkFailure 判读一个进程创建失败是否为配额耗尽，并给出可读归因。
//
// 参数：err 为任意一次 fork/exec 返回的错误（nil 安全）
//
// 返回：
//   - note: 面向人的归因文案；空串表示这个错误与配额无关，调用方不必改写它
//   - quota: 是否**确定**为配额耗尽
//
// 参考上限是**本进程当前实际生效的 RLIMIT_NPROC 软限**（getNprocLimit），不是
// 按 reserve_ratio 重算的策略默认值：fork 失败正是内核拿「调用者自己的软限」
// 比「uid 进程总数」得出的，归因必须引用它。在 shim 里它就是刚装上的围栏
// （spec.NprocLimit）；在 agentd 里它是系统默认上限。两个都不用记账，读现成的。
//
// 三条分支，对应 2026-08-12 事故的教训——当时的
// `fork/exec /bin/sh: resource temporarily unavailable` 埋在测试输出里，长得
// 像 flaky 测试，把排障方向带偏了整整 43 分钟：
//   - 非 EAGAIN：不认领，返回空串
//   - EAGAIN 且占用 ≥ 实际软限：**确定**归因，quota=true，文案带真实数字——
//     内核正是在 used ≥ 软限这个阈值上拒绝 fork，占用不低于它就不可能别的原因
//   - EAGAIN 但占用低于实际软限、或读不出数：**如实说不知道**，quota=false。
//     宁可说「原因未知」也不能猜一个像模像样的结论
func ExplainForkFailure(err error) (note string, quota bool) {
	if err == nil || !errors.Is(err, syscall.EAGAIN) {
		return "", false
	}
	procs, perr := enumProcsFn()
	if perr != nil {
		log().Warn("进程创建失败（EAGAIN），但读不到当前占用，无法归因")
		return "进程创建失败（EAGAIN），且读不到当前进程占用，原因未知", false
	}
	limit, lerr := getNprocLimitFn()
	if lerr != nil || limit <= 0 {
		log().Warn("进程创建失败（EAGAIN），但读不到本进程实际生效的进程数上限，无法归因",
			"cause", lerr, "limit", limit)
		return "进程创建失败（EAGAIN），且读不到本进程实际生效的进程数上限，原因未知", false
	}
	used := len(procs)
	if used >= limit {
		log().Error("进程配额耗尽", "used", used, "limit", limit)
		return fmt.Sprintf("进程配额耗尽（当前 uid %d/%d），命令未执行；"+
			"这不是代码问题，请降低并发后重试", used, limit), true
	}
	log().Warn("进程创建失败（EAGAIN），但占用低于实际上限，原因未知",
		"used", used, "limit", limit)
	return fmt.Sprintf("进程创建失败（EAGAIN），但当前占用仅 %d/%d，"+
		"不像配额问题，原因未知", used, limit), false
}

// applyFencePolicy 按当前策略把围栏值写进 spec。
//
// 参数：spec 为待下发的进程规格，本函数**就地修改**其 NprocLimit 字段
//
// 为什么由 prochost 自己填而不是让 adapter 填：四个 adapter 各自构造 Spec，
// 交给它们填等于四处都可能漏，而漏掉的后果是这个任务完全没有围栏保护、
// 且日志里看不出任何异常——一个静默失效的防护装置。
//
// 注意：算不出围栏值时字段置 0（不设围栏）并打 Warn，**绝不阻断拉起**。
func applyFencePolicy(spec *Spec) {
	l, err := fenceLimit()
	if err != nil {
		log().Warn("算不出进程围栏值，本次不设围栏", "cause", err)
		spec.NprocLimit = 0
		return
	}
	spec.NprocLimit = l
}
