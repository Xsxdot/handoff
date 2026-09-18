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
	"path/filepath"
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
	// B376：复合只读拆段静默。白名单仍只认单段——这一段只在「整条仍是
	// Consult」时把 `a && b` / `a | b` / `a; b` 拆开逐段证明只读；任一段
	// 证明不了就整条 Consult，绝不半段放行。
	if id, segs, ok := compoundSilent(req.Command); ok {
		g.log.Info("复合只读命令静默", "command", req.Command, "id", id,
			"segments", segs, "targets", len(targets))
		return Verdict{Action: AutoAllow, Rule: RuleSafeCommand,
			Reason: "复合只读命令静默: " + id}
	}
	g.log.Debug("安全命令白名单未命中", "command", req.Command)
	return verdict
}

// SafeCommandIDs 返回当前白名单的稳定 id 集合（排序后），供政策快照 Version 哈希。
func SafeCommandIDs() []string {
	return []string{
		"git-ledger-amend", "go-build", "go-test", "go-vet", "gofmt",
		"npm-test", "npm-run", "make",
		"ls", "cat", "grep", "rg", "which", "pwd", "head", "tail", "wc",
		"echo", "printf", "cd", "sed-n", "codegraph",
		"git-status", "git-diff", "git-log", "git-grep", "git-show",
		"git-blame", "git-cat-file", "git-rev-parse", "git-ls-files",
		"git-branch", "git-merge-base", "git-ls-tree", "git-rev-list",
	}
}

// codegraphReadSubcommands 是 codegraph 的只读子命令闭集（B376）。
//
// 闭集之外的任何子命令都判 Consult：codegraph absorb 这类会改写视图目录，
// 不在静默面里。
var codegraphReadSubcommands = map[string]bool{
	"sym": true, "who-calls": true, "chain": true, "flow": true,
	"tree": true, "context": true, "entity": true, "domains": true,
	"check": true, "validate": true, "summary": true,
}

// codegraphValueFlags 是 codegraph 全局旗标里「吃一个值」的那些。
//
// why 需要它：子命令是「第一个不以 - 开头」的词元，而 --repo/--view/--base
// 的**值**（如 `.`）同样不以 - 开头。不跳过值就会把 `.` 当成子命令。
var codegraphValueFlags = map[string]bool{
	"--repo": true, "--view": true, "--base": true,
}

// codegraphReadSubcommand 从子命令之前的参数里找出 codegraph 子命令。
//
// 返回 (子命令名, ok)：ok=false 表示子命令不在只读闭集（或未知），不得静默；
// 命中 --help/--version 且无子命令时返回 ("", true)。
//
// 边界：全局旗标可出现在子命令之前（`codegraph --repo . sym X`），判定按
// 「第一个非旗标词元」而不是固定 fields[1]；值旗标的下一词元按值跳过。
func codegraphReadSubcommand(args []string) (string, bool) {
	if len(args) == 0 {
		return "", true // 裸 codegraph 只打印帮助
	}
	sawHelp := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "--") {
			name := a
			if eq := strings.IndexByte(a, '='); eq >= 0 {
				name = a[:eq]
			} else if codegraphValueFlags[name] && i+1 < len(args) {
				i++ // 跳过值
			}
			if name == "--help" || name == "--version" {
				sawHelp = true
			}
			continue
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			continue
		}
		if codegraphReadSubcommands[a] {
			return a, true
		}
		return "", false
	}
	if sawHelp {
		return "", true
	}
	return "", false
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
	return matchSilentFields(segments[0], command)
}

// matchSilentFields 在单段词元上匹配静默白名单。command 是承载这些词元的
// 原文（复合拆段时是段文本，单段路径时是整条命令），供 gofmt/git diff 的
// 写落点与 echo/printf 的重定向守卫使用。
//
// 为什么单独拆出来：单段白名单被两处复用——safeCommandID 的原路径与 B376
// 复合拆段的每一段。两份实现迟早漂移，白名单表必须只有一处。
func matchSilentFields(fields []string, command string) (id string, ok bool) {
	if len(fields) == 0 {
		return "", false
	}
	// 命令替换（`$(...)` / 反引号）不得静默：它在引号内也会被执行——词元匹配
	// 只看得到 `echo`，而 shell 会跑括号里的任意命令。splitSafeCommand 的引号
	// 状态机把引号内内容当字面量，不会报警，所以这道守卫必须独立存在。
	// 不拦纯变量展开（`$VAR`/`${VAR}`）：它不执行命令，且 `echo "$HOME"` 是
	// 高频无副作用写法，拦它会重新制造噪音。`${VAR:-$(cmd)}` 里的 `$(` 仍被拦。
	if hasCommandSubstitution(command) {
		return "", false
	}
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
		case "--prefix":
			// B376：--prefix 插在 npm 与 test/run 之间也认；`npm --prefix d ci`
			// 的 ci 仍非静默。
			if len(fields) >= 4 {
				switch fields[3] {
				case "test":
					return "npm-test", true
				case "run":
					if len(fields) >= 5 && fields[4] != "" && !strings.HasPrefix(fields[4], "-") {
						return "npm-run", true
					}
				}
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
	if fields[0] == "cd" && len(fields) <= 2 {
		return "cd", true
	}
	if fields[0] == "codegraph" {
		// B376 复审：codegraph 的全局旗标（--repo/--view/--base/--stale）可出现在
		// 子命令之前，白名单要按「子命令闭集」判，而不是把 fields[1] 当子命令。
		// 未知子命令仍 Consult（absorb 会改写视图目录）。
		sub, ok := codegraphReadSubcommand(fields[1:])
		if !ok {
			return "", false
		}
		if sub == "" {
			return "codegraph", true
		}
		return "codegraph-" + sub, true
	}
	if fields[0] == "sed" {
		// B376：sed 只有 -n 只读形态才静默；-i/--in-place 会改源文件。
		// -i 的短路后缀（-i.bak、-i'.bak'、--in-place=.bak）也是原地改写，
		// 必须用前缀匹配而不是整词匹配，否则守卫被后缀绕过。
		//
		// 复审 2：-i 不是唯一的写/执行面。sed 脚本里的 w/W（写文件）、e（执行
		// 命令）以及 s///w file 的 w 标志同样在静默面外；-f/--file 从文件读脚本，
		// 内容不可见同样不得静默。这三类与 -i 一样是「静态不可证明只读」。
		if hasSedQuiet(fields[1:]) &&
			!hasSedInPlace(fields[1:]) && !hasSedWriteOrExec(fields[1:]) {
			return "sed-n", true
		}
		return "", false
	}
	if fields[0] == "rg" {
		// B376 复审：rg 的执行型标志会拉起任意程序，不得静默。
		// --pre <cmd> / --pre=<cmd> 让 rg 对每个文件执行该命令；--pre-glob 不
		// 是执行型，但与本守卫无关。其它 rg 形态仍与 grep 同档只读。
		if hasRGExecuteFlag(fields[1:]) {
			return "", false
		}
		return fields[0], true
	}
	if fields[0] == "echo" || fields[0] == "printf" {
		// B376：echo/printf 只在无写重定向时静默；`echo x > file` 是写。
		if hasNonDiscardWriteRedirect(command) {
			return "", false
		}
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
		case "merge-base":
			return "git-merge-base", true
		case "ls-tree":
			return "git-ls-tree", true
		case "rev-list":
			return "git-rev-list", true
		case "branch":
			// B376：git branch 的删除/改名/复制/强制形态都非只读。
			// -m/-M/-c/-C 是短选项：`git branch -M topic`、`git branch -Mnew`
			// 与 `git branch -mM` 都是强制改名，必须前缀匹配；--move/--copy/
			// --delete/--force 同理，`--delete=x` 也命中。
			if hasGitBranchMutator(fields[1:]) {
				return "", false
			}
			return "git-branch", true
		}
	}
	return "", false
}

// hasNonDiscardWriteRedirect 判断命令是否含会真正写文件的输出重定向。
//
// 复用 RedirectTargets 的引号感知扫描，只是把 /dev/null 这类丢弃设备排除。
func hasNonDiscardWriteRedirect(cmd string) bool {
	for _, t := range RedirectTargets(cmd) {
		if !IsDiscardTarget(t) {
			return true
		}
	}
	return false
}

// hasCommandSubstitution 判断命令是否含命令替换或变量展开（含引号内）。
//
// 为什么必须独立扫原文：splitSafeCommand 的引号状态机把引号内内容当字面量，
// `echo "$(rm -rf x)"` 会被切成干净的 [echo, $(rm -rf x)]。但 shell 在引号内
// **照样执行** 命令替换。判据是「静态可证明」，内容不可见就不在静默面。
func hasCommandSubstitution(cmd string) bool {
	for i := 0; i < len(cmd); i++ {
		switch cmd[i] {
		case '`':
			return true
		case '$':
			if i+1 < len(cmd) && cmd[i+1] == '(' {
				return true
			}
		}
	}
	return false
}

// compoundSilent 是 B376 的复合只读判定入口：把 `a && b` / `a | b` / `a; b`
// 拆开后逐段证明只读，全部可静默才静默。
//
// 边界（与 spec 第 1 条逐字对齐）：
//   - 白名单仍只认单段——本函数不把整串写进白名单表
//   - 任一段证明不了（未知命令、写重定向、管道进写工具）→ 整条不静默
//   - 绝不半段放行：`grep a && rm x` 整条 Consult
//
// 返回：命中 id（"compound"）、段数、是否整条可静默。
func compoundSilent(command string) (id string, segments int, ok bool) {
	segs, ok := splitSilentSegments(command)
	if !ok {
		return "", 0, false
	}
	cdActive := false
	for _, seg := range segs {
		// cd 改变后续段的实际工作目录，而参数位写落点按 Workdir 解析——相对
		// 落点会被误判成范围内（`cd /etc && gofmt -w passwd` 实际写 /etc/passwd）。
		// 重定向落点有 hasNonDiscardWriteRedirect 全拦；这里只补参数位写命令。
		if isCDSegment(seg) {
			cdActive = true
		} else if cdActive && segmentHasRelativeWriteTarget(seg) {
			return "", len(segs), false
		}
		if _, ok := matchSilentSegment(seg); !ok {
			return "", len(segs), false
		}
	}
	return "compound", len(segs), true
}

// isCDSegment 判断一段的首词元是否为 cd。
func isCDSegment(seg string) bool {
	fields, ok := silentFields(seg)
	return ok && len(fields) > 0 && fields[0] == "cd"
}

// segmentHasRelativeWriteTarget 判断一段是否存在相对路径的参数位写落点。
//
// 只认相对路径：绝对落点的归属不随 cd 变化，且 judgeBash 的范围判定已覆盖。
// 丢弃设备跳过（`tee /dev/null` 之类不构成写）。
func segmentHasRelativeWriteTarget(seg string) bool {
	for _, t := range WriteArgTargets(seg) {
		if IsDiscardTarget(t) {
			continue
		}
		if !filepath.IsAbs(t) {
			return true
		}
	}
	return false
}

// splitSilentSegments 把命令按引号外的 `&&` / `||` / `;` 切开。
//
// 返回 ok=false 的形态（任一出现即整条不静默）：
//   - 未闭合引号、换行、`(` `)`、反引号、`$(`、`${`
//   - 单独的 `&`（后台执行，不在静默面）
//   - 空段（连接符前后缺命令）
//
// 注意：`|` 不在这里切——管道在 matchSilentSegment 内按 `每一级可静默`
// 单独判定（语义不同：管道级的可静默条件是逐级只读）。
func splitSilentSegments(cmd string) ([]string, bool) {
	var out []string
	var token strings.Builder
	var quote rune
	flush := func() bool {
		seg := strings.TrimSpace(token.String())
		token.Reset()
		if seg == "" {
			return false
		}
		out = append(out, seg)
		return true
	}
	rs := []rune(cmd)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
			token.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
			token.WriteRune(r)
		case r == '&':
			if i+1 >= len(rs) || rs[i+1] != '&' {
				return nil, false
			}
			if !flush() {
				return nil, false
			}
			i++
		case r == '|':
			if i+1 < len(rs) && rs[i+1] == '|' {
				if !flush() {
					return nil, false
				}
				i++
				continue
			}
			token.WriteRune(r) // 单管道留给 splitPipeStages
		case r == ';':
			if !flush() {
				return nil, false
			}
		case r == '\n', r == '(', r == ')', r == '`':
			return nil, false
		case r == '$' && i+1 < len(rs) && (rs[i+1] == '(' || rs[i+1] == '{'):
			return nil, false
		default:
			token.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, false
	}
	if !flush() {
		return nil, false
	}
	return out, true
}

// splitPipeStages 把一段命令按引号外的 `|` 切成管道各级。
func splitPipeStages(seg string) ([]string, bool) {
	var out []string
	var token strings.Builder
	var quote rune
	flush := func() bool {
		stage := strings.TrimSpace(token.String())
		token.Reset()
		if stage == "" {
			return false
		}
		out = append(out, stage)
		return true
	}
	rs := []rune(seg)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
			token.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
			token.WriteRune(r)
		case r == '|':
			if i+1 < len(rs) && rs[i+1] == '|' {
				return nil, false // `||` 应在段层已被切开，出现即形态异常
			}
			if !flush() {
				return nil, false
			}
		default:
			token.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, false
	}
	if !flush() {
		return nil, false
	}
	return out, true
}

// silentFields 把一段命令切成词元（引号感知）。段内不应再含连接符；
// 出现连接符或未闭合引号即返回 ok=false。
func silentFields(seg string) ([]string, bool) {
	segments, ok := splitSafeCommand(seg)
	if !ok || len(segments) != 1 {
		return nil, false
	}
	return segments[0], true
}

// matchSilentSegment 判定单段（可含管道）能否静默，返回命中的 id。
//
// 判定顺序：
//  1. 段内含非丢弃写重定向 → 否（`echo x > file` 不得静默）
//  2. 无管道：整段作为单段白名单匹配
//  3. 有管道：每一级都要单段可静默，且末级二进制不是 `tee`
func matchSilentSegment(seg string) (string, bool) {
	if hasNonDiscardWriteRedirect(seg) {
		return "", false
	}
	stages, ok := splitPipeStages(seg)
	if !ok {
		return "", false
	}
	if len(stages) == 1 {
		fields, ok := silentFields(stages[0])
		if !ok {
			return "", false
		}
		return matchSilentFields(fields, seg)
	}
	firstID := ""
	for i, stage := range stages {
		fields, ok := silentFields(stage)
		if !ok {
			return "", false
		}
		if i == 0 {
			firstID = fields[0]
		}
		if i == len(stages)-1 && fields[0] == "tee" {
			return "", false
		}
		if _, ok := matchSilentFields(fields, stage); !ok {
			return "", false
		}
	}
	if firstID == "" {
		firstID = "pipe"
	}
	return firstID, true
}

func hasToken(fields []string, want string) bool {
	for _, field := range fields {
		if field == want {
			return true
		}
	}
	return false
}

// hasSedInPlace 判断 sed 参数里是否含原地改写选项。
//
// 为什么不能用 hasToken("-i")：sed 允许选项与实参粘连，也允许短选项聚簇——
// `-i.bak`、`-i'.bak'`（splitSafeCommand 剥引号后是 `-i.bak`）、
// `--in-place=.bak`、`-ni.bak`。整词匹配会漏掉全部非独立词形态，而它们都
// 真的改写源文件。sed 的短选项表里只有 -i 含字母 i，故短选项簇按字母扫。
func hasSedInPlace(fields []string) bool {
	for _, f := range fields {
		if f == "" || f == "-" || !strings.HasPrefix(f, "-") {
			continue
		}
		if strings.HasPrefix(f, "--") {
			if strings.HasPrefix(f, "--in-place") {
				return true
			}
			continue
		}
		body := f[1:]
		if i := strings.IndexByte(body, '.'); i >= 0 {
			body = body[:i]
		}
		if strings.ContainsRune(body, 'i') {
			return true
		}
	}
	return false
}

// hasSedQuiet 判断 sed 参数里是否含「静默」旗标（-n / --quiet / --silent）。
//
// 为什么不能用 hasToken("-n")：sed 允许短选项聚簇，`-ne` 与 `-n -e` 是同一个
// 只读形态（本机 GNU sed 4.9 实测输出一致，原文在台账）。判定必须按 GNU sed
// 选项表扫每一段短选项簇，而不是整词相等。
//
// 扫簇时遇带参选项字母即停：GNU sed 里 e/f/l 必带实参（可粘连也可取下一词元），
// i 可选粘连后缀，其后字符属于该选项实参，不再是旗标——所以 `-en` 里的 n 是
// e 的脚本实参，不算静默；`-ln`/`-nl 2` 里的 n 是 l 的实参（-nl 2 的 2 才是
// l 的行宽），也不算。旗标段（簇首直到首个带参字母前）里出现 n 才是静默。
//
// 返回 true 只表示「有静默旗标」；`-i` 与写/执行面由调用方另行否决。
func hasSedQuiet(fields []string) bool {
	for _, f := range fields {
		if f == "--quiet" || f == "--silent" {
			return true
		}
		if f == "" || f == "-" || !strings.HasPrefix(f, "-") || strings.HasPrefix(f, "--") {
			continue
		}
		for _, c := range f[1:] {
			switch c {
			case 'n':
				return true
			case 'e', 'f', 'l', 'i':
				// 带参字母之后的字符是实参，旗标段到此为止。
				goto next
			}
		}
	next:
	}
	return false
}

// hasRGExecuteFlag 判断 rg 参数里是否含执行型标志。
//
// --pre <cmd> / --pre=<cmd> 让 rg 对每个被检索文件先跑一次该命令，等于把
// 任意程序带过门；--pre-glob 只是 glob 过滤，不在此列。
func hasRGExecuteFlag(fields []string) bool {
	for _, f := range fields {
		if f == "--pre" || strings.HasPrefix(f, "--pre=") {
			return true
		}
	}
	return false
}

// hasSedWriteOrExec 判断 sed 参数里是否含脚本层面的写文件或执行命令面。
//
// sed 的 `w file` / `W file` 命令把模式空间写进任意文件，`e` 命令执行任意
// shell；`s/.../.../w file` 的 w 标志同理。这些都可以藏进 sed 脚本（位置参数
// 或 -e/--expression 的下一词元，也可能直接粘连成 `s/a/b/w /tmp/x`）。
//
// 判定是文本级白名单之外的黑名单：扫描**每一个**脚本源。旧实现遇首个位置
// 参数即 return，`sed -n 'p' -e 'w /tmp/out' f` 这种「位置脚本在前、-e 写/
// 执行在后」的形态会被静默放行——真机 GNU sed 里位置参数按输入文件读并报错，
// 但 -e 的 w/e 照样执行（review-4 真机复现绕过）。因此这里不再短路：
//
//   - -f/--file（含 --file=）从任意文件读脚本，内容不可见 → 否决
//   - 每个 -e/--expression（含 --expression=与粘连）都扫脚本正文
//   - 位置参数按 GNU 语义只有「没有显式脚本源」时才是脚本，此时扫首个位置
//     参数；有 -e/-f 时位置参数是输入文件，不执行，不当脚本扫（否则
//     `sed -n -e 'p' /etc/passwd` 会把路径正则误当写命令）
//
// 保守但正确——判据是「静态可证明只读」，可见写/执行面即否决。
func hasSedWriteOrExec(fields []string) bool {
	var positionals []string
	explicitScript := false
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if f == "-f" || f == "--file" || strings.HasPrefix(f, "--file=") {
			return true
		}
		if strings.HasPrefix(f, "--expression=") {
			explicitScript = true
			if sedScriptWritesOrExecs(strings.TrimPrefix(f, "--expression=")) {
				return true
			}
			continue
		}
		switch f {
		case "-e", "--expression":
			explicitScript = true
			if i+1 < len(fields) {
				i++
				if sedScriptWritesOrExecs(fields[i]) {
					return true
				}
			}
			continue
		case "-n", "--quiet", "--silent", "-s", "-E", "-r", "-z", "-u", "--posix",
			"--debug", "--sandbox", "-l":
			continue
		}
		if strings.HasPrefix(f, "--") {
			// 未知长选项（含 --debug=... 的变体）保守否决：判据要静态可证明。
			return true
		}
		if strings.HasPrefix(f, "-") && f != "-" {
			// B377：短选项簇按 GNU sed 选项表展开。旗标段（簇首到首个带参
			// 字母前）逐个字母判；遇 e/f/l/i 即停——其后属该选项的实参。
			//   - e：实参是 sed 脚本（粘连在簇上，或取下一词元），交给
			//     sedScriptWritesOrExecs 扫写/执行面；簇里出现过 e 即等于
			//     有了显式脚本源，必须置 explicitScript，否则其后位置参数
			//     （输入文件名）会被当脚本扫——`sed -ne '1,20p' web.log`
			//     的 web.log 会被扫出 w 而误否决
			//   - f：脚本来自文件、内容不可见 → 否决
			//   - l/i：无写/执行语义，跳过实参
			//   - 未知带参字母：不能证明的形态保守否决
			body := f[1:]
			i2 := 0
			for i2 < len(body) {
				switch body[i2] {
				case 'n', 's', 'E', 'r', 'z', 'u':
					i2++
					continue
				case 'e':
					explicitScript = true
					arg := body[i2+1:]
					if arg == "" && i+1 < len(fields) {
						i++
						arg = fields[i]
					}
					if sedScriptWritesOrExecs(arg) {
						return true
					}
					i2 = len(body)
				case 'f':
					return true
				case 'l', 'i':
					i2 = len(body)
				default:
					return true
				}
			}
			continue
		}
		positionals = append(positionals, f)
	}
	// 有显式脚本源（-e/--expression/-f）时，位置参数是输入文件而非脚本；
	// 没有时，首个位置参数才是脚本。
	if !explicitScript && len(positionals) > 0 {
		return sedScriptWritesOrExecs(positionals[0])
	}
	return false
}

// sedScriptWritesOrExecs 判断一段 sed 脚本文本是否含写文件或执行命令面。
//
// 为什么不能先按 `;` 切分再逐条判：review-3 的 `s/a;b/c/e` 证明分号既可能
// 是命令分隔符，也可能是 s/// 的 pattern/replacement 正文。先切会把 `s/a;b/c/e`
// 切成 `s/a`（解析失败，判否）与 `b/c/e`（首字母 b，判否），于是执行标志 e 漏掉。
// 现在改成对整段脚本做一次左到右扫描，分隔符只在自己该在的位置生效。
//
// 单条命令的扫描顺序（与 GNU sed 语法一致）：
//  1. 地址/取反前缀：行号（含 `~step`）、`$`、正则 `/re/`（可带 `I`/`M` 标志）、
//     `\cre`、`,` 范围端点（含 `,+N` / `,~N`）、`!`
//  2. 命令字母：
//     - w/W/e 直接否决
//     - s 解析 pattern/replacement/flags，flags 含 w/W/e 否决
//     - y 跳过三段分隔文本（转写，非写/执行）
//     - a/i/c/r/R 吃掉本行剩余（GNU 语义：它们把 `;` 后文本当参数，不执行）
//     - b/t/T/: 标签吃到 `;`/换行（标签在 `;` 处结束，不能吞掉后续命令）
//     - 其余单字母命令跳过
//
// 对无法确定或未闭合的形态以「是」作答——误伤只多一次 Consult，漏判则是
// 静默写文件或静默执行命令。
func sedScriptWritesOrExecs(script string) bool {
	i := 0
	for i < len(script) {
		switch script[i] {
		case ' ', '\t', '\r', ';', '\n':
			i++
			continue
		}
		next, ok := skipSedAddress(script, i)
		if !ok {
			return true // 地址形态不可证明为只读
		}
		i = next
		if i >= len(script) {
			return false // 只有地址没有命令
		}
		switch c := script[i]; c {
		case 'w', 'W', 'e':
			return true
		case 's':
			n, flags := parseSedSubstitute(script, i)
			if strings.ContainsAny(flags, "wWe") {
				return true
			}
			i = n
		case 'y':
			n, ok := parseSedY(script, i)
			if !ok {
				return true
			}
			i = n
		case 'a', 'i', 'c', 'r', 'R':
			i = skipSedToLineEnd(script, i+1)
		case 'b', 't', 'T', ':':
			i = skipSedToSeparator(script, i+1)
		case '#':
			i = skipSedToLineEnd(script, i)
		default:
			i++
		}
	}
	return false
}

// skipSedBlanks 跳过空格与制表符，返回下一个非空白下标。
func skipSedBlanks(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

// skipSedAddress 跳过一条命令前的完整地址前缀（含范围、GNU 端点、取反）。
//
// 返回：(新下标, ok)。ok=false 表示地址形态畸形或未闭合——调用方按不可证明
// 只读否决。没有地址时原地下标返回，ok=true。
func skipSedAddress(s string, i int) (int, bool) {
	n, ok := skipSedAddressItem(s, i)
	if !ok {
		return i, false
	}
	if n == i {
		return i, true // 无地址
	}
	i = n
	for {
		j := skipSedBlanks(s, i)
		if j >= len(s) || s[j] != ',' {
			break
		}
		j = skipSedBlanks(s, j+1)
		switch {
		case j < len(s) && s[j] == '+':
			// GNU `addr,+N`：相对行数端点。
			j++
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
		case j+1 < len(s) && s[j] == '~' && s[j+1] >= '0' && s[j+1] <= '9':
			// GNU `addr,~N`：相对步长端点。
			j++
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
		default:
			n2, ok := skipSedAddressItem(s, j)
			if !ok || n2 == j {
				return j, false // 逗号后无端点，畸形
			}
			j = n2
		}
		i = j
	}
	// 取反 `!` 可出现在完整地址之后（含空格）。
	for {
		j := skipSedBlanks(s, i)
		if j < len(s) && s[j] == '!' {
			i = j + 1
			continue
		}
		break
	}
	return i, true
}

// skipSedAddressItem 跳过一个地址项：行号（可带 ~step）、$、/re/ 或 \cre。
//
// 正则地址闭合后可带 GNU 的 `I`/`M` 标志（`/re/Iw file`），必须一并消费，
// 否则 w 会被当成地址的一部分读掉。
func skipSedAddressItem(s string, i int) (int, bool) {
	if i >= len(s) {
		return i, true
	}
	switch c := s[i]; {
	case c >= '0' && c <= '9':
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i < len(s) && s[i] == '~' {
			i++
			for i < len(s) && s[i] >= '0' && s[i] <= '9' {
				i++
			}
		}
		return i, true
	case c == '$':
		return i + 1, true
	case c == '/':
		n, ok := skipSedDelimited(s, i, '/')
		if !ok {
			return i, false
		}
		return skipSedAddressFlags(s, n), true
	case c == '\\':
		if i+1 >= len(s) || s[i+1] == '\n' {
			return i, false
		}
		n, ok := skipSedDelimited(s, i+1, s[i+1])
		if !ok {
			return i, false
		}
		return skipSedAddressFlags(s, n), true
	}
	return i, true // 无地址项
}

// skipSedAddressFlags 消费正则地址闭合后的 GNU 标志 I / M。
func skipSedAddressFlags(s string, i int) int {
	for i < len(s) && (s[i] == 'I' || s[i] == 'M') {
		i++
	}
	return i
}

// parseSedSubstitute 从 `s` 命令字母起解析 `s<d>pat<d>rep<d>flags`。
//
// 参数：s 为脚本，start 指向 `s`
// 返回：命令之后的下一扫描下标；flags 段文本（到 `;`/换行为止，可能为空）。
// 未闭合分隔符时按读到脚本末尾处理、flags 为空（不构成写；由调用方决定）。
func parseSedSubstitute(s string, start int) (int, string) {
	if start+1 >= len(s) {
		return len(s), ""
	}
	delim := s[start+1]
	// pattern：起始分隔符在 start+1。
	if n, ok := skipSedDelimited(s, start+1, delim); ok {
		// replacement：上一段闭合分隔符即下一段的起始分隔符。
		if n2, ok := skipSedDelimited(s, n-1, delim); ok {
			j := n2
			for j < len(s) && s[j] != ';' && s[j] != '\n' {
				j++
			}
			return j, s[n2:j]
		}
	}
	return len(s), ""
}

// parseSedY 从 `y` 命令字母起解析 `y<d>src<d>dst<d>`（两段分隔文本）。
func parseSedY(s string, start int) (int, bool) {
	if start+1 >= len(s) {
		return len(s), true
	}
	delim := s[start+1]
	n, ok := skipSedDelimited(s, start+1, delim)
	if !ok {
		return len(s), false
	}
	// 第一段闭合分隔符同时是第二段的起始分隔符，回退一位再进入下一轮。
	n2, ok := skipSedDelimited(s, n-1, delim)
	if !ok {
		return len(s), false
	}
	return n2, true
}

// skipSedToLineEnd 跳到本行行尾（不含换行）。
func skipSedToLineEnd(s string, i int) int {
	for i < len(s) && s[i] != '\n' {
		i++
	}
	return i
}

// skipSedToSeparator 跳到下一个 `;` 或换行（标签类命令的参数边界）。
func skipSedToSeparator(s string, i int) int {
	for i < len(s) && s[i] != ';' && s[i] != '\n' {
		i++
	}
	return i
}

// skipSedDelimited 跳过以 delim 包裹的一段 sed 文本（pattern 或 replacement）。
//
// 参数：s 为脚本，start 指向起始 delim，delim 为分隔字符（`/` 或 `\c` 的 c）
// 返回：闭合 delim 之后的下标；(0, false) 表示未闭合——调用方必须按「不可
// 证明只读」否决。反斜杠转义（`\/`）不提前闭合。
func skipSedDelimited(s string, start int, delim byte) (int, bool) {
	for i := start + 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++ // 跳过被转义的下一个字符
		case delim:
			return i + 1, true
		}
	}
	return 0, false
}

// 短选项 -d/-D/-m/-M/-c/-C/-f/-u/-t 既可能独立成词（`git branch -M topic`），
// 也可能与参数粘连（`git branch -Mtopic`），还可能聚簇（`git branch -mM`）；
// 长选项 `--move=x` 亦然。整词匹配会漏掉粘连形态，必须前缀匹配。
//
// 各选项为什么非只读：
//   - d/D 删除、m/M 改名、c/C 复制：直接改 ref
//   - f 强制重置分支指向：改 ref
//   - u / --set-upstream-to / --unset-upstream / -t / --track：写 .git/config
//     的上游追踪
//   - --edit-description：写分支描述到 config
func hasGitBranchMutator(fields []string) bool {
	mutatingLong := []string{
		"--delete", "--move", "--copy", "--force",
		"--set-upstream-to", "--unset-upstream", "--edit-description", "--track",
	}
	for _, f := range fields {
		if !strings.HasPrefix(f, "-") {
			continue
		}
		if strings.HasPrefix(f, "--") {
			for _, long := range mutatingLong {
				if f == long || strings.HasPrefix(f, long+"=") {
					return true
				}
			}
			continue
		}
		// 短选项：跳过前导 '-'，逐个字母检查改写类。
		for _, c := range f[1:] {
			switch c {
			case 'd', 'D', 'm', 'M', 'c', 'C', 'f', 'u', 't':
				return true
			}
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
