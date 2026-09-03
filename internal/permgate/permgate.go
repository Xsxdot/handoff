// Package permgate 提供权限请求的结构化判据。
//
// 职责：
//   - 把一次权限请求判成三个出口之一：AutoAllow（立即放行）、Consult
//     （交廉价模型审批者）、Escalate（直接升级人工协调者）
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
	"strings"

	"github.com/Xsxdot/handoff/internal/executor"
)

// Action 是一次权限裁决的出口。
type Action int

const (
	// AutoAllow 立即放行：不建工单、不发事件、不唤醒任何人。
	// 只可能出自 write/edit 路由（目标路径全部落在任务范围内）。
	AutoAllow Action = iota
	// Consult 交廉价模型审批者裁决（今天未命中黑名单时的默认路径）。
	Consult
	// Escalate 直接升级人工协调者（今天黑名单命中时的路径）。
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
// 三处都是 handoff 分配给该任务的空间：Workdir 是它要改的仓库/worktree，
// TaskDir 是 agentd 给它的 0700 私有目录，TaskTmpDir 是执行器的任务草稿/构建
// 目录。写这三处不该叫醒任何人；共享系统 /tmp 不在范围内。
type Scope struct {
	Workdir    string
	TaskDir    string
	TaskTmpDir string
}

// RuleSafeCommand marks a positive command-whitelist match. It is distinct
// from the empty rule used by in-scope file writes so audit callers can record
// only static command decisions.
const RuleSafeCommand = "safe-command"

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

// Judge 判定一次权限请求（spec §3.4 的路由 + §7 的 fail-closed 表）。
//
// 参数：
//   - req: 结构化后的权限请求
//   - scope: 本任务的合法作用范围
//
// 返回：Verdict，调用方据 Action 决定后续动作
//
// 路由：
//   - write / edit → 路径归属判定，**并且**对 Text 跑一次黑名单（路径本身
//     可能命中，如 Write: /etc/sudoers）；两项是与关系
//   - bash        → 先判全部落点的范围归属，落点干净才做命令类判定
//   - 其余        → 对 Text 做命令类判定
//
// 注意：
//   - Truncated 一律直接 Escalate 且不再往下判：看到的是不完整的描述，
//     危险片段可能落在截断之外，黑名单与模型都不可信
//   - 本方法**永不因失败而返回 AutoAllow**（spec §7）
func (g *Gate) Judge(req Request, scope Scope) Verdict {
	if req.Truncated {
		return Verdict{Action: Escalate,
			Reason: "权限描述含截断标记，危险片段可能落在截断之外"}
	}
	switch req.Tool {
	case executor.PermToolWrite, executor.PermToolEdit:
		return g.judgeFileWrite(req, scope)
	case executor.PermToolBash:
		return g.judgeBash(req, scope)
	default:
		return g.judgeCommand(req.Text)
	}
}

// judgeFileWrite 判定写文件类请求：黑名单与路径归属都通过才自动放行。
//
// 返回：AutoAllow 或 Escalate；本函数**永不返回 Consult**——写文件的危险
// 判定是确定性的（路径在不在范围内），不需要模型介入。
func (g *Gate) judgeFileWrite(req Request, scope Scope) Verdict {
	if len(req.Paths) == 0 {
		return Verdict{Action: Escalate,
			Reason: "写文件请求未能提取出目标路径，无法判定范围"}
	}
	if hit, rule := g.match(req.Text); hit {
		return Verdict{Action: Escalate,
			Reason: "写文件描述命中黑名单", Rule: rule}
	}
	for _, p := range req.Paths {
		in, base, err := InScope(p, scope)
		if err != nil {
			return Verdict{Action: Escalate,
				Reason: fmt.Sprintf("目标路径归一化失败 %q: %v", p, err)}
		}
		if !in {
			return Verdict{Action: Escalate,
				Reason: fmt.Sprintf("目标路径越出任务范围: %s", p)}
		}
		g.log.Debug("写入路径在任务范围内", "path", p, "base", base)
	}
	return Verdict{Action: AutoAllow, Reason: "全部目标路径落在任务范围内"}
}

// judgeBash 判定命令类请求：先判落点范围，落点干净才落回命令判据。
//
// 参数：req 为结构化请求（用 Command 与 Paths），scope 为任务范围
// 返回：Escalate（任一落点越界或归一化失败），否则 judgeCommand 的结果
//
// 落点有三个来源，同等对待：
//   - req.Paths —— executor 自己检出的越界目录。opencode 的 external_directory
//     bash 形态把它填在这里（opencode/adapter_test.go 有同名断言钉住），
//     但**此前被本函数的前身整个丢弃**，越界因此从「人来判」降级成「廉价模型判」
//   - RedirectTargets(req.Command) —— handoff 自己从命令原文摘的重定向落点。
//     不能只靠前者：2026-08-18 真机探针实测 opencode 只解析「作为参数出现的
//     路径」，`echo x > /tmp/f` 零权限请求直接写成功（spec §2.2.1）
//   - WriteArgTargets(req.Command) —— handoff 从写命令参数位摘的落点（B151）。
//     claude 的 bash 请求 Paths 恒为空，opencode 对管道后的 tee 也检不出，这一路
//     是唯一兜底
//
// 为什么路径判据前置于命令判据：越界是确定性事实，而 judgeCommand 最好的结果
// 也只是 Consult。放在后面会让「越界」被稀释成廉价模型的一次裁决。
//
// 为什么落点为空不 fail-closed（与 judgeFileWrite 的 len(Paths)==0 → Escalate 不同）：
// 纯 bash 门类本来就不带路径，`go build ./...` 是绝大多数情形；在这里 fail-closed
// 等于把每条命令都升级人工，那是 spec §3 明确排除的反转。
func (g *Gate) judgeBash(req Request, scope Scope) Verdict {
	// 落点有三个来源，判定规则完全一致，合并后走同一个循环：
	//   - req.Paths：executor 自己检出的（opencode 的 external_directory）
	//   - RedirectTargets：handoff 从重定向语法里摘的（B134）
	//   - WriteArgTargets：handoff 从写命令参数位摘的（B151）——claude 的 bash
	//     请求 Paths 恒为空，opencode 对管道后的 tee 也检不出，这一路是唯一兜底
	redirects := RedirectTargets(req.Command)
	writeArgs := WriteArgTargets(req.Command)
	targets := append([]string(nil), req.Paths...)
	targets = append(targets, redirects...)
	targets = append(targets, writeArgs...)
	if n := len(targets); n > 0 {
		g.log.Debug("命令落点已汇总", "count", n,
			"from_executor", len(req.Paths),
			"from_redirect", len(redirects),
			"from_write_args", len(writeArgs))
	}
	for _, p := range targets {
		if IsDiscardTarget(p) {
			g.log.Debug("命令落点是丢弃设备，跳过范围判定", "path", p)
			continue
		}
		in, base, err := InScope(p, scope)
		if err != nil {
			g.log.Debug("命令落点归一化失败，按越界处置", "path", p, "cause", err)
			return Verdict{Action: Escalate,
				Reason: fmt.Sprintf("命令落点归一化失败 %q: %v", p, err)}
		}
		if !in {
			// 越界只返回给 manager 打带 task/perm 的权威 WARN；这里不重复打日志。
			// 文案逐字复用 judgeFileWrite：B27 的真机验收记录按这句话 grep 日志
			return Verdict{Action: Escalate,
				Reason: fmt.Sprintf("目标路径越出任务范围: %s", p)}
		}
		g.log.Debug("命令落点在任务范围内", "path", p, "base", base)
	}
	// Existing self-command, blacklist, and wrapper rules remain authoritative;
	// the positive whitelist only handles their ordinary Consult result.
	verdict := g.judgeCommand(req.Command)
	if verdict.Action != Consult {
		return verdict
	}
	if id, ok := safeCommandID(req.Command); ok {
		g.log.Info("安全命令白名单命中", "command", req.Command, "id", id,
			"targets", len(targets))
		return Verdict{Action: AutoAllow, Rule: RuleSafeCommand,
			Reason: "安全命令白名单命中: " + id}
	}
	g.log.Debug("安全命令白名单未命中", "command", req.Command)
	return verdict
}

// safeCommandID matches a complete, positive command shape and returns its
// stable audit identifier. It tokenizes shell quoting for the command body;
// it is not a shell executor and rejects connectors except the one ledger
// amend form explicitly required by charter.
func safeCommandID(command string) (id string, ok bool) {
	segments, ok := splitSafeCommand(command)
	if !ok {
		return "", false
	}
	if len(segments) == 2 {
		if isLedgerAmend(segments[0], segments[1]) && len(WriteArgTargets(command)) > 0 {
			return "git-ledger-amend", true
		}
		return "", false
	}
	if len(segments) != 1 || len(segments[0]) == 0 {
		return "", false
	}
	fields := segments[0]
	if len(fields) >= 2 && fields[0] == "go" {
		switch fields[1] {
		case "build":
			return "go-build", true
		case "test":
			return "go-test", true
		case "vet":
			return "go-vet", true
		}
	}
	if fields[0] == "gofmt" {
		if hasToken(fields[1:], "-w") && len(WriteArgTargets(command)) == 0 {
			return "", false
		}
		return "gofmt", true
	}
	if len(fields) >= 2 && fields[0] == "npm" {
		switch fields[1] {
		case "test":
			return "npm-test", true
		case "run":
			if len(fields) >= 3 && fields[2] != "" && !strings.HasPrefix(fields[2], "-") {
				return "npm-run", true
			}
		}
	}
	if fields[0] == "make" {
		return "make", true
	}
	switch fields[0] {
	case "ls", "cat", "grep", "which", "pwd", "head", "tail", "wc":
		return fields[0], true
	}
	if len(fields) >= 2 && fields[0] == "git" {
		switch fields[1] {
		case "status":
			return "git-status", true
		case "diff":
			if hasGitDiffOutput(fields) && len(WriteArgTargets(command)) == 0 {
				return "", false
			}
			return "git-diff", true
		case "log":
			return "git-log", true
		case "grep":
			return "git-grep", true
		case "show":
			if hasGitDiffOutput(fields) {
				return "", false
			}
			return "git-show", true
		case "blame":
			return "git-blame", true
		case "cat-file":
			return "git-cat-file", true
		case "rev-parse":
			return "git-rev-parse", true
		case "ls-files":
			return "git-ls-files", true
		}
	}
	return "", false
}

func hasToken(fields []string, want string) bool {
	for _, field := range fields {
		if field == want {
			return true
		}
	}
	return false
}

func hasGitDiffOutput(fields []string) bool {
	for _, field := range fields[2:] {
		if field == "--output" || strings.HasPrefix(field, "--output=") {
			return true
		}
	}
	return false
}

func isLedgerAmend(left, right []string) bool {
	if len(left) < 3 || left[0] != "git" || left[1] != "add" {
		return false
	}
	pathspec := false
	for _, field := range left[2:] {
		if field == "--" {
			continue
		}
		if !strings.HasPrefix(field, "-") {
			pathspec = true
		}
	}
	return pathspec && len(right) == 4 && right[0] == "git" && right[1] == "commit" &&
		right[2] == "--amend" && right[3] == "--no-edit"
}

// splitSafeCommand tokenizes one command or the two segments of the ledger
// amend form. Any unapproved shell connector or unterminated quote fails.
func splitSafeCommand(command string) ([][]string, bool) {
	var segments [][]string
	var fields []string
	var token strings.Builder
	var quote rune
	active := false
	flush := func() {
		if active {
			fields = append(fields, token.String())
			token.Reset()
			active = false
		}
	}
	for i, rs := 0, []rune(command); i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				token.WriteRune(r)
			}
			active = true
		case r == '\'' || r == '"':
			quote = r
			active = true
		case r == ' ' || r == '\t' || r == '\r':
			flush()
		case r == '|', r == ';', r == '\n', r == '(', r == ')', r == '`':
			return nil, false
		case r == '&':
			if i > 0 && rs[i-1] == '>' && i+1 < len(rs) &&
				((rs[i+1] >= '0' && rs[i+1] <= '9') || rs[i+1] == '-') {
				// File-descriptor duplication/close (2>&1, >&2, >&-) is
				// a redirection token, not a command connector.
				token.WriteRune(r)
				active = true
				continue
			}
			if i+1 >= len(rs) || rs[i+1] != '&' {
				return nil, false
			}
			flush()
			if len(fields) == 0 {
				return nil, false
			}
			segments = append(segments, fields)
			fields = nil
			i++
		case r == '$' && i+1 < len(rs) && (rs[i+1] == '(' || rs[i+1] == '{'):
			return nil, false
		default:
			token.WriteRune(r)
			active = true
		}
	}
	if quote != 0 {
		return nil, false
	}
	flush()
	if len(fields) == 0 {
		return nil, false
	}
	segments = append(segments, fields)
	return segments, true
}
