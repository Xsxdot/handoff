// admission.go —— 开工前的进程余量准入闸。
//
// 职责：
//   - checkProcHeadroom：余量已耗尽时拒绝开工，并给出带数字的理由
//
// 边界：
//   - 不安装围栏、不回收进程：只做一次只读判读
//   - **不承担拦截职责**。真正拦住事故的是围栏（进程起不来），本闸换来的是
//     一句人能看懂的话，以及「A 任务吃满时 B 任务得到解释而不是莫名撞墙」。
//     2026-08-12 两个任务开工时余量都是好的，这个闸拦不住它们——别高估它
package agentd

import (
	"errors"
	"fmt"

	"github.com/Xsxdot/handoff/internal/prochost"
)

// ErrNoProcHeadroom 表示进程余量已耗尽，本次开工被拒。
//
// 路由层靠 errors.Is 认它并返回 400——这是环境问题不是请求格式问题，
// 但 4xx 能让协调者立刻知道「不用重试，先腾地方」。
var ErrNoProcHeadroom = errors.New("进程余量不足")

// admissionFn 是余量判读的测试缝。**生产路径恒为 prochost.CheckAdmission**。
var admissionFn = prochost.CheckAdmission

// checkProcHeadroom 在开工前判读一次进程余量。
//
// 参数：op 为动作名（"dispatch" / "run"），只用于日志与错误文案
//
// 返回：余量耗尽时返回包装 ErrNoProcHeadroom 的错误（文案带 used/limit
// 真实数字）；其余情况一律 nil。
//
// 注意：
//   - 高水位（九成）**放行**并打 Warn：拦在这里等于把「快满了」当「满了」，
//     会把还能正常完成的任务无谓挡掉
//   - 读数不可信时**放行**（fail-open）：为「量不出来」而拒绝派发，会让
//     handoff 在不支持的平台上彻底不能用，代价远大于收益
//
// 与 spec §3.4 的一处有意偏离：spec 写「`used ≥ 0.9L`：放行，但 stderr 警告
// + 事件记录」。这里只打服务端 Warn 日志，**不在准入闸里发事件**——高水位事件
// 由 Task 6 的看门狗统一按「越线沿一次」发射。若准入闸也发，一轮密集派发会
// 产生 N 条重复事件，把协调者的会话刷爆，反而淹掉真正要处置的工单。协调者
// 得到的信息量不减（同一条 `resource_pressure` 事件），噪声大减。
func checkProcHeadroom(op string) error {
	a := admissionFn()
	if !a.Known {
		log().Debug("进程余量未知，放行", "op", op)
		return nil
	}
	if a.Full() {
		log().Error("进程余量耗尽，拒绝开工", "op", op, "used", a.Used, "limit", a.Limit)
		return fmt.Errorf("%w：当前 %d/%d，请等待在跑的任务结束或先回收残留",
			ErrNoProcHeadroom, a.Used, a.Limit)
	}
	if a.NearFull() {
		log().Warn("进程余量已达高水位，仍放行", "op", op, "used", a.Used, "limit", a.Limit)
		return nil
	}
	log().Debug("进程余量充足", "op", op, "used", a.Used, "limit", a.Limit)
	return nil
}
