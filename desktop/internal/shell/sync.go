// 本文件负责「已配置的机器上要不要把 agentd/CLI 换成内嵌的那份」的判据与执行
// （spec §4.1 / §5）。
//
// 职责：
//   - PlanSync 是纯函数：吃 DecideRelease 的结论 + 活跃任务数 + 内嵌可用性，
//     吐四态之一。不碰文件系统、不发网络请求，因此四态可以穷举测试。
//   - DoSync 才动这台机器：换二进制、同步 skill、触发 agentd 重启。
//
// 边界（承重）：
//   - 本文件**不做版本比较**。判据全部来自 DecideRelease，它背后是全仓唯一的
//     selfupdate.CompareVersion。这里再写一份比较就是第四份。
//   - 本文件**不决定何时被调用**。调用时机与相对顺序是 main.go 的 openConsole
//     的责任（spec §5 的三条承重顺序），放在这里会让顺序无法被单独测试。
//   - 同步失败**绝不阻断打开控制台**（spec D8）：所有函数只返回错误，绝不
//     os.Exit、绝不 panic、绝不阻塞等待用户输入。
package shell

import "strconv"

// SyncPlan 是同步决策的四态。
type SyncPlan int

const (
	// SyncSkip 表示不需要同步（已有的不旧，或版本判不出，或压根没有既有安装）。
	SyncSkip SyncPlan = iota
	// SyncDo 表示该换，且此刻换是安全的。
	SyncDo
	// SyncBlocked 表示该换，但有活跃任务，闸一拦下。
	SyncBlocked
	// SyncNoEmbed 表示该换但本次构建没内嵌二进制（开发构建未带 -tags embedbin）。
	SyncNoEmbed
)

// String 返回四态的可读名，供日志用。
func (p SyncPlan) String() string {
	switch p {
	case SyncSkip:
		return "skip"
	case SyncDo:
		return "do"
	case SyncBlocked:
		return "blocked"
	case SyncNoEmbed:
		return "no-embed"
	default:
		return "SyncPlan(" + strconv.Itoa(int(p)) + ")"
	}
}

// PlanSync 决定要不要把已装的 handoff 换成内嵌的那份，是纯函数。
//
// 参数：
//   - d: DecideRelease 的结论。只有 DecisionEmbeddedNewer 才可能走到换版
//   - busy: 活跃任务数（running/waiting_answer）。**负数表示调用方探测失败**
//   - embedAvailable: 本次构建有没有内嵌二进制（embedbin.Available()）
//
// 返回四态之一，语义见各常量注释。
//
// 注意：
//   - busy 为负一律按 SyncBlocked 处置。猜错的代价不对称：误判空闲会在用户
//     有活跃任务时重启 agentd，误判繁忙只是这次不升级
//   - 本函数不写日志（纯函数约定）。四态决策的日志由调用方拿到返回值后打
func PlanSync(d ReleaseDecision, busy int, embedAvailable bool) SyncPlan {
	if d != DecisionEmbeddedNewer {
		return SyncSkip
	}
	if !embedAvailable {
		return SyncNoEmbed
	}
	// busy != 0 涵盖了负数（探测失败），见 doc comment 的不对称代价说明
	if busy != 0 {
		return SyncBlocked
	}
	return SyncDo
}
