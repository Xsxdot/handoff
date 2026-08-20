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
//   - 不认识 cobra、不认识 Endpoint：本包要能被 agentd 直接调
//
// 为什么必须只有一处：B64 的病根是 renderCheckRow 与 process 各维护一套分支表，
// 两套的分支集合与优先级不一致，于是同一台机器有两套说法。
package upgrade

// Machine 是一台机器的探测结果。
//
// Name / Local / Agentd / Revision / Platform / Managed / Pull / Busy / Err 的语义
// 与 CLI 侧 machineState 一致。Bin 是 CLI 本机路径的二进制版本；远端不使用。
type Machine struct {
	Name     string
	Local    bool
	Bin      string // 仅本机 CLI 路径使用；远端不填
	Agentd   string // 对端上报的 release 版本号
	Revision string // 仅 Agentd 为空时用于渲染
	Platform string // "goos/goarch"；空 = 对端过旧未上报
	// Managed / Pull 是**三态指针**：nil = 对端没上报，与「对端说 false」是两回事。
	// 用 bool 零值把前者塌成后者，就会把「我不知道」讲成「它非托管」——B64 的病根。
	Managed *bool
	Pull    *bool
	Busy    int
	Err     error
}

// Verdict 是一台机器的唯一结论。
type Verdict int

const (
	// VerdictUnreachable 远端够不着：版本、平台、托管状态一概无从得知。
	VerdictUnreachable Verdict = iota
	// VerdictAgentdDown 本机 agentd 没在跑。不是失败——敲命令的人知道自己
	// 要不要把它起回来。
	VerdictAgentdDown
	// VerdictTooOld 远端过旧，连平台都不上报。
	VerdictTooOld
	// VerdictLatest 已是最新，无事可做。
	VerdictLatest
	// VerdictUnmanaged 对端**明确上报**非托管：换完 exit(0) 没人拉起。
	VerdictUnmanaged
	// VerdictManagedUnknown 对端上报了平台却没上报托管状态：不知道就是不知道。
	VerdictManagedUnknown
	// VerdictNeedsUpgrade 该升级，且没有已知障碍。
	VerdictNeedsUpgrade
)

// String 让 Verdict 在日志与测试失败信息里可读。
func (v Verdict) String() string {
	switch v {
	case VerdictUnreachable:
		return "unreachable"
	case VerdictAgentdDown:
		return "agentd_down"
	case VerdictTooOld:
		return "too_old"
	case VerdictLatest:
		return "latest"
	case VerdictUnmanaged:
		return "unmanaged"
	case VerdictManagedUnknown:
		return "managed_unknown"
	case VerdictNeedsUpgrade:
		return "needs_upgrade"
	}
	return "unknown"
}

// Classify 把探测结果收敛成单一结论。
//
// 参数：
//   - m: 一台机器的探测结果（调用方探测的产物）
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
func Classify(m Machine, latest string) Verdict {
	if m.Err != nil && !m.Local {
		return VerdictUnreachable
	}
	if !m.Local && m.Platform == "" {
		return VerdictTooOld
	}
	if m.IsLatest(latest) {
		return VerdictLatest
	}
	// 排在 IsLatest 之后：本机 agentd 没跑时 Agentd 为空、IsLatest 只比二进制，
	// 已经最新就没事可做——不必为了「把它起回来」先重下一遍同版本再换一次文件
	if m.Err != nil {
		return VerdictAgentdDown
	}
	if m.Managed != nil && !*m.Managed {
		return VerdictUnmanaged
	}
	if m.Managed == nil {
		return VerdictManagedUnknown
	}
	return VerdictNeedsUpgrade
}

// IsLatest 判断一台机器是否已是最新版本。
//
// 本机 agentd 未运行时只比二进制版本；运行时两者都要对齐（二进制已最新但
// agentd 还是旧进程 = 中间态，仍需要重启这一步）。远端只比较 agentd 版本。
func (m Machine) IsLatest(latest string) bool {
	if m.Local {
		if m.Agentd == "" {
			return m.Bin == latest
		}
		return m.Bin == latest && m.Agentd == latest
	}
	return m.Agentd == latest
}
