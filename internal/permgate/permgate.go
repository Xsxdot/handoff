// Package permgate 提供权限请求的结构化判据。
//
// 职责：
//   - 把一次权限请求判成三个出口之一：AutoAllow（立即放行）、Consult
//     （交廉价模型审批者）、Escalate（直接升级人工审核者）
//   - 承载全部判定规则：黑名单模式匹配、引号剥离、执行包装器识别、
//     写文件目标路径的范围归属
//
// 边界：
//   - 纯计算，无 I/O：不写 store、不碰 adapter、不发网络请求（EvalSymlinks
//     的文件系统只读探测除外，它是路径判定的必需品）
//   - 无 deny 权：出口里没有「拒绝」——拒绝只有人能做，与 approver 同源
//   - 不做状态迁移、不建工单：调用方（manager）据 Verdict 决定后续动作
//
// 为什么判据要独立成包：三个 adapter（claude/grok/opencode）的权限载荷形态
// 完全不同，但判据必须只有一份——判据分散到 adapter 里，就会重演「opencode
// 有 external_directory、claude 和 grok 没有」这种各家一套的漂移。
package permgate

import (
	"fmt"
	"log/slog"
	"regexp"
)

// Action 是一次权限裁决的出口。
type Action int

const (
	// AutoAllow 立即放行：不建工单、不发事件、不唤醒任何人。
	// 只可能出自 write/edit 路由（目标路径全部落在任务范围内）。
	AutoAllow Action = iota
	// Consult 交廉价模型审批者裁决（今天未命中黑名单时的默认路径）。
	Consult
	// Escalate 直接升级人工审核者（今天黑名单命中时的路径）。
	Escalate
)

// String 给出日志可读的短标签。
func (a Action) String() string {
	switch a {
	case AutoAllow:
		return "auto_allow"
	case Consult:
		return "consult"
	default:
		return "escalate"
	}
}

// Request 是结构化后的权限请求。
//
// Text 与 Command 不是重复：Text 是给人看的全文（与工单同源，形如
// "Bash: xxx"），Command 是命令类工具的纯命令串。bash 路由判 Command，
// 其余路由退回判 Text。
type Request struct {
	Tool      string   // 归一化工具名，取 executor.PermTool* 常量
	Text      string   // 权限描述全文（与工单同源）
	Command   string   // Tool=bash 时的完整命令串
	Paths     []string // Tool=write|edit 时的目标路径（可为相对路径）
	Truncated bool     // 描述含 executor.TruncationMarker
}

// Scope 是本任务的合法作用范围。
//
// 两处都是 handoff 分配给该任务的空间：Workdir 是它要改的仓库/worktree，
// TaskDir 是 agentd 给它的 0700 私有目录。写这两处不该叫醒任何人。
type Scope struct {
	Workdir string
	TaskDir string
}

// Verdict 是裁决结果。
//
//   - Reason: 可读理由，进日志与审计
//   - Rule: 因黑名单而 Escalate 时命中的规则原文；其余情形为空
type Verdict struct {
	Action Action
	Reason string
	Rule   string
}

// Gate 持有编译后的黑名单，是判据的唯一入口。
type Gate struct {
	blacklist []*regexp.Regexp
	log       *slog.Logger
}

// New 构造判据网关。
//
// 参数：
//   - patterns: 用户自定义黑名单（config.ApproverConfig.Blacklist）；内置
//     黑名单自动前置，无需调用方传入
//   - log: 包日志入口；nil 时用 slog.Default()
//
// 返回：
//   - 可用的 Gate；任一正则编译失败即返回错误（配置错误应在启动期暴露）
//
// 注意：
//   - **无论审批者是否启用都必须构造**。AutoAllow 是第 0 层静态判据、不是
//     审批者的职权；漏构造会让未配置审批者的部署被工作区内的每次写入淹没
func New(patterns []string, log *slog.Logger) (*Gate, error) {
	if log == nil {
		log = slog.Default()
	}
	all := append([]string(nil), builtinBlacklist...)
	all = append(all, patterns...)
	rx := make([]*regexp.Regexp, 0, len(all))
	for _, p := range all {
		r, err := regexp.Compile("(?i)" + p)
		if err != nil {
			return nil, fmt.Errorf("编译黑名单正则 %q: %w", p, err)
		}
		rx = append(rx, r)
	}
	log.Info("权限判据网关已就绪", "builtin_rules", len(builtinBlacklist),
		"custom_rules", len(patterns))
	return &Gate{blacklist: rx, log: log}, nil
}

// match 返回是否命中黑名单及命中的规则原文。
func (g *Gate) match(s string) (bool, string) {
	for _, r := range g.blacklist {
		if r.MatchString(s) {
			return true, r.String()
		}
	}
	return false, ""
}
