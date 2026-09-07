// capability.go —— harness 五类能力的名字、报告与查询（B233.2）。
//
// 职责：
//   - 冻结 harness 名、能力名、能力报告形状与「不支持」哨兵
//   - 给出 BaselineReport：今天五家必须声明的支持矩阵（查询面，不是实现）
//
// 边界：
//   - 无 I/O、不拉进程、不拼 argv、不写 HOME
//   - 报告「支持」不等于已经接线；生产入口改挂能力面是实现节点的事
package executor

import (
	"errors"
	"fmt"
)

// 现有受支持 harness 的登记名。必须与 cmd/agentd.go 组装表、OneShotArgs
// switch、toolchain.order 使用同一组字面值——名字漂移会让能力查询与派发对不上。
const (
	HarnessOpenCode = "opencode"
	HarnessClaude   = "claude"
	HarnessGrok     = "grok"
	HarnessCodex    = "codex"
	HarnessAGY      = "agy"
)

// CapabilityName 是五类能力的稳定名字。未出现在报告里的能力视为不支持。
type CapabilityName string

const (
	CapExecution    CapabilityName = "execution"
	CapCoordination CapabilityName = "coordination"
	CapOneShot      CapabilityName = "oneshot"
	CapProfile      CapabilityName = "profile"
	CapSkills       CapabilityName = "skills"
)

// Codex 一次性调用的原生限制词表。它们是 Codex CLI 的既成约束，不是调用方策略。
const (
	NativeLimitSandboxReadOnly  = "sandbox=read-only"
	NativeLimitInvokeExec       = "invoke=exec"
	NativeLimitEphemeral        = "ephemeral"
	NativeLimitSkipGitRepoCheck = "skip-git-repo-check"
	NativeLimitColorNever       = "color=never"
	NativeLimitIgnoreUserConfig = "ignore-user-config"
)

// ErrCapabilityUnsupported 表示该 harness 明确没有这项能力。
//
// 调用方必须用 errors.Is 判别，禁止靠错误文本。命中后禁止静默改走另一家。
var ErrCapabilityUnsupported = errors.New("harness 不支持该能力")

// ErrUnknownHarness 表示名字不在受支持名单里。
var ErrUnknownHarness = errors.New("未知 harness")

// CapabilityDecl 是某一类能力的声明。
type CapabilityDecl struct {
	Name         CapabilityName
	Supported    bool
	NativeLimits []string
}

// CapabilityReport 是一家 harness 的能力清单。Caps 必须五类都有显式声明。
type CapabilityReport struct {
	Harness string
	Caps    []CapabilityDecl
}

// Provider 是可查询能力报告的提供方。具体执行/协调/一次性等门面由实现节点
// 按报告再取，本接口不把五类方法揉成一个上帝对象。
type Provider interface {
	Name() string
	Report() CapabilityReport
}

// StaticProvider 用写死的报告充当 Provider。测试与 Ticket 0 金样本用；
// 生产各家包在实现节点换成真实报告。
type StaticProvider struct {
	HarnessName string
	Rep         CapabilityReport
}

// Name 返回登记名。
func (p StaticProvider) Name() string { return p.HarnessName }

// Report 返回写死的能力清单。
func (p StaticProvider) Report() CapabilityReport { return p.Rep }

// Registry 按名字查找 Provider。组装点绑定；本类型不做厂商 argv。
type Registry struct {
	byName map[string]Provider
}

// NewRegistry 登记一组提供方。后写入覆盖同名先写入。
func NewRegistry(ps ...Provider) *Registry {
	r := &Registry{byName: make(map[string]Provider, len(ps))}
	for _, p := range ps {
		if p == nil {
			continue
		}
		r.byName[p.Name()] = p
	}
	return r
}

// Get 按登记名取提供方。未知名字返回 ErrUnknownHarness。
func (r *Registry) Get(name string) (Provider, error) {
	if r == nil {
		return nil, fmt.Errorf("%w %q（支持 opencode/claude/grok/agy/codex）", ErrUnknownHarness, name)
	}
	p, ok := r.byName[name]
	if !ok {
		return nil, fmt.Errorf("%w %q（支持 opencode/claude/grok/agy/codex）", ErrUnknownHarness, name)
	}
	return p, nil
}

// Require 查报告：不支持或未声明则返回 ErrCapabilityUnsupported。
func (r *Registry) Require(name string, cap CapabilityName) error {
	p, err := r.Get(name)
	if err != nil {
		return err
	}
	return RequireCapability(p.Report(), cap)
}

// ReportHas 报告是否显式声明并支持 cap。未出现在 Caps 里视为不支持。
func ReportHas(r CapabilityReport, cap CapabilityName) bool {
	for _, c := range r.Caps {
		if c.Name == cap {
			return c.Supported
		}
	}
	return false
}

// RequireCapability 在报告上执法：不支持则返回可行动的 Unsupported 错误。
func RequireCapability(r CapabilityReport, cap CapabilityName) error {
	if ReportHas(r, cap) {
		return nil
	}
	return UnsupportedError(r.Harness, cap)
}

// UnsupportedError 构造「某家不支持某能力」的可行动错误。
//
// 文本必须含 harness 名、能力名和「禁止静默兜底」，让调用方日志不用再猜。
func UnsupportedError(harness string, cap CapabilityName) error {
	return fmt.Errorf("%w: %s 不支持 %s（禁止静默兜底到另一家）",
		ErrCapabilityUnsupported, harness, cap)
}

// SupportedHarnesses 返回受支持名单，顺序与 toolchain.order 一致。
func SupportedHarnesses() []string {
	return []string{HarnessOpenCode, HarnessClaude, HarnessGrok, HarnessCodex, HarnessAGY}
}

// AllCapabilityNames 返回五类能力名，固定顺序。
func AllCapabilityNames() []CapabilityName {
	return []CapabilityName{CapExecution, CapCoordination, CapOneShot, CapProfile, CapSkills}
}

// BaselineReport 返回本卡冻结的支持矩阵。未知名字返回 ErrUnknownHarness。
func BaselineReport(harness string) (CapabilityReport, error) {
	switch harness {
	case HarnessOpenCode:
		return reportAll(HarnessOpenCode, true, nil), nil
	case HarnessClaude, HarnessGrok, HarnessAGY:
		return reportAll(harness, false, nil), nil
	case HarnessCodex:
		return reportAll(HarnessCodex, false, []string{
			NativeLimitSandboxReadOnly,
			NativeLimitInvokeExec,
			NativeLimitEphemeral,
			NativeLimitSkipGitRepoCheck,
			NativeLimitColorNever,
			NativeLimitIgnoreUserConfig,
		}), nil
	default:
		return CapabilityReport{}, fmt.Errorf("%w %q（支持 opencode/claude/grok/agy/codex）",
			ErrUnknownHarness, harness)
	}
}

func reportAll(harness string, coordination bool, oneshotLimits []string) CapabilityReport {
	return CapabilityReport{
		Harness: harness,
		Caps: []CapabilityDecl{
			{Name: CapExecution, Supported: true},
			{Name: CapCoordination, Supported: coordination},
			{Name: CapOneShot, Supported: true, NativeLimits: oneshotLimits},
			{Name: CapProfile, Supported: true},
			{Name: CapSkills, Supported: true},
		},
	}
}
