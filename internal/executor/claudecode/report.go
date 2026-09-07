// report.go —— 本家 Provider 能力报告（B233.2 Bundle）。
//
// 职责：让生产 Adapter 可查询五类能力；矩阵与 BaselineReport 对齐。
// 边界：无 I/O；报告「支持」不是生产入口已挂上。
package claudecode

import "github.com/Xsxdot/handoff/internal/executor"

var _ executor.Provider = (*Adapter)(nil)

// Name 返回登记名。必须与 cmd/agentd.go 组装表键相同。
func (a *Adapter) Name() string { return executor.HarnessClaude }

// Report 返回本家能力矩阵。查询面与 BaselineReport 对齐；「支持」不等于已接线。
func (a *Adapter) Report() executor.CapabilityReport {
	rep, err := executor.BaselineReport(executor.HarnessClaude)
	if err != nil {
		if a != nil && a.log != nil {
			a.log.Error("BaselineReport 失败，返回空能力报告", "harness", executor.HarnessClaude, "cause", err)
		}
		return executor.CapabilityReport{Harness: executor.HarnessClaude}
	}
	return rep
}
