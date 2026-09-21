// report.go —— fake Provider 能力报告（B233.2 Bundle）。
//
// 职责：fake 仅声明 execution=true，其余能力全为 false。
// 边界：仅供测试与演示执行者使用。
package fake

import "github.com/Xsxdot/handoff/internal/executor"

var _ executor.Provider = (*Fake)(nil)

func (f *Fake) Name() string { return "fake" }

func (f *Fake) Report() executor.CapabilityReport {
	return executor.CapabilityReport{
		Harness: "fake",
		Caps: []executor.CapabilityDecl{
			{Name: executor.CapExecution, Supported: true},
			{Name: executor.CapCoordination, Supported: false},
			{Name: executor.CapOneShot, Supported: false},
			{Name: executor.CapProfile, Supported: false},
			{Name: executor.CapSkills, Supported: false},
		},
	}
}
