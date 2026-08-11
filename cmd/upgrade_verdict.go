// 本文件是 handoff upgrade 的**唯一判据来源**（B64）。
//
// 职责：
//   - 把一台机器的探测结果收敛成单一结论（verdict）
//   - 结论之间的优先级在此定义一次，供只读巡检与 --now 两个消费方共用
//
// 边界：
//   - 纯函数：不做 I/O、不打日志、不产出面向操作者的文案
//   - 不判 busy：活跃任务是「要不要现在换」的闸，不是「这台机器是什么状态」的
//     结论；它只在 verdictNeedsUpgrade 之后由 process 施加（spec §4.3）
//
// 为什么必须只有一处：B64 的病根是 renderCheckRow 与 process 各维护一套分支表，
// 两套的分支集合与优先级不一致，于是同一台机器有两套说法。
package cmd

// verdict 是一台机器的唯一结论。
type verdict int

const (
	// verdictUnreachable 远端够不着：版本、平台、托管状态一概无从得知。
	verdictUnreachable verdict = iota
	// verdictAgentdDown 本机 agentd 没在跑。不是失败——敲命令的人知道自己
	// 要不要把它起回来。
	verdictAgentdDown
	// verdictTooOld 远端过旧，连平台都不上报。
	verdictTooOld
	// verdictLatest 已是最新，无事可做。
	verdictLatest
	// verdictUnmanaged 对端**明确上报**非托管：换完 exit(0) 没人拉起。
	verdictUnmanaged
	// verdictManagedUnknown 对端上报了平台却没上报托管状态：不知道就是不知道。
	verdictManagedUnknown
	// verdictNeedsUpgrade 该升级，且没有已知障碍。
	verdictNeedsUpgrade
)

// String 让 verdict 在日志与测试失败信息里可读。
func (v verdict) String() string {
	switch v {
	case verdictUnreachable:
		return "unreachable"
	case verdictAgentdDown:
		return "agentd_down"
	case verdictTooOld:
		return "too_old"
	case verdictLatest:
		return "latest"
	case verdictUnmanaged:
		return "unmanaged"
	case verdictManagedUnknown:
		return "managed_unknown"
	case verdictNeedsUpgrade:
		return "needs_upgrade"
	}
	return "unknown"
}

// classify 把探测结果收敛成单一结论。
//
// 参数：
//   - ms: 一台机器的探测结果（probeMachine 的产物）
//   - latest: 最新发布的 tag
//
// 返回：
//   - 该机器的唯一结论
//
// 优先级（顺序即判据，改动前先读完这段）：
//  1. 够不着 / 本机 agentd 没跑——探测本身没拿到东西，但两者含义不同：远端够不着
//     连版本都不知道；本机 agentd 没跑时二进制版本仍然已知，所以下一条还能生效
//  2. 远端过旧（未上报平台）——排在托管判定之前：连平台都不上报的对端，它的托管
//     状态同样不可信，报「非托管」会把人引去装一个救不了它的 service（B64 原症状）
//  3. 已是最新——排在托管判定之前：没事可做时不该催人装 service，也不该重下重换
//  4. 明确非托管 → 不知道是否托管 → 该升级
func classify(ms *machineState, latest string) verdict {
	if ms.Err != nil && !ms.Ep.Local {
		return verdictUnreachable
	}
	if !ms.Ep.Local && ms.Platform == "" {
		return verdictTooOld
	}
	if ms.isLatest(latest) {
		return verdictLatest
	}
	// 排在 isLatest 之后：本机 agentd 没跑时 Agentd 为空、isLatest 只比二进制，
	// 已经最新就没事可做——不必为了「把它起回来」先重下一遍同版本再换一次文件
	if ms.Err != nil {
		return verdictAgentdDown
	}
	if ms.Managed != nil && !*ms.Managed {
		return verdictUnmanaged
	}
	if ms.Managed == nil {
		return verdictManagedUnknown
	}
	return verdictNeedsUpgrade
}
