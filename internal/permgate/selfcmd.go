// selfcmd.go —— handoff 自指令判据（身份越权）。
//
// 职责：
//   - 识别「executor 在 shell 里调用 handoff 自身 CLI 的变更类子命令」
//   - 只读子命令按白名单放行，其余一律判为自指令
//
// 边界：
//   - 不判危险性：rm -rf / sudo 这类由 blacklist.go 管。两者威胁轴不同——
//     那边问「这条命令会不会破坏东西」，这边问「这个角色该不该做这件事」
//   - 不做引号剥离：那是 judgeCommand 的编排职责，本文件只提供一次「这段
//     文本里有没有自指令」的纯判定，无 I/O、无状态
//   - 执行包装器识别复用同包的 HasExecWrapper，不另造一套解析（见 judgeSegment）
package permgate

import "strings"

// RuleSelfCommand 是自指令命中时填进 Verdict.Rule 的固定值。
//
// agentd 侧据它把「升级人工」这条日志的级别提到 Warn（见 manager.go 的
// escalateLogLevel）：自指令在本次改动前会被廉价模型静默放行，属于「本该
// 漏过、现在被拦下」那一类，必须在日志里一眼可见。
const RuleSelfCommand = "self-command"

// selfCmdReadOnly 是允许 executor 调用的只读子命令白名单。
//
// 判据是「只读且无外部副作用」。attach 与 pull **不在**其中：两者都要 ssh 到
// 别的机器、用的是协调者的 ssh 身份，副作用越出本机；attach 还开交互会话，
// 而 executor 无 tty，拦它零损失。
var selfCmdReadOnly = map[string]bool{
	"tasks": true, "show": true, "diff": true, "fetch": true, "status": true,
	"frames": true, "sessions": true, "footprint": true, "ls": true,
}

// selfCmdMutating 是明确的变更类子命令名单。
//
// 它**不是**拦截面的全集——未列入的未知子命令同样会被拦（见 judgeSegment
// 第 3 级）。这份名单只有两个作用：让 Verdict.Reason 能报出具体子命令名；
// 让「变更词优先于白名单词」的顺序可判，堵住 `handoff run T1 handoff show`
// 这种把白名单词塞进变更命令参数里的形态。
var selfCmdMutating = map[string]bool{
	"dispatch": true, "continue": true, "done": true, "stop": true,
	"reply": true, "resume": true, "run": true, "reclaim": true,
	"attach": true, "pull": true, "agentd": true, "init": true,
	"service": true, "skill": true, "upgrade": true, "update-check": true,
	"project": true, "machines": true, "revoke": true, "console": true,
}

// IsSelfCommand 判断命令文本里是否存在 handoff 的变更类自指令调用。
//
// 参数：s 为待判文本（bash 路由传 Command，其余路由传 Text）
//
// 返回：
//   - hit: 是否判为自指令
//   - sub: 命中的子命令名；未知子命令返回该词元原文；未命中返回 ""
//
// 判定分三步（spec §3.3 + B383 第 3 项之 (c)）：
//  1. 按 | ; & 换行切段，逐段独立判定
//  2. 段内定位处于**命令位置**的 basename 为 handoff/handoff.exe 的词元（见
//     judgeSegment）；普通参数位上的 handoff 不定位。其后不以 - 开头的词元即候选
//  3. 三级判定，顺序不可换：含变更词 → 命中；否则含白名单词 → 放行；
//     否则候选非空 → 命中
//
// 注意：本函数不处理引号——调用方（judgeCommand）负责按原文与 StripQuoted
// 结果各跑一遍。执行包装器只参与「命令位置」判定（见 judgeSegment）。
func IsSelfCommand(s string) (hit bool, sub string) {
	for _, seg := range splitSegments(s) {
		if h, name := judgeSegment(seg); h {
			return true, name
		}
	}
	return false, ""
}

// splitSegments 按 shell 的命令分隔符把文本切成独立命令段。
//
// 为什么必须先切段：判定问的是「handoff 之后跟了什么」，而 | ; & 之后的词元
// 属于另一条命令。不切的话 `handoff tasks | grep done` 里的 done 会被算成
// handoff 的候选子命令，把一次只读调用误判成变更调用。
func splitSegments(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == '|' || r == ';' || r == '&' || r == '\n'
	})
}

// judgeSegment 判定单个命令段，返回值语义同 IsSelfCommand。
//
// 判据（B383 第 3 项之 (c)）：handoff 必须处于**命令位置**才算自指令候选。
// 命令位置 = 段首词元，或执行包装器之后的词元；**普通参数位**上的 handoff
// 一律放行。旧判据是「出现 handoff 即认」，会把下面三条普通参数位调用误拦
// （评审实测的三条误报）：
//
//	rg handoff internal/                 handoff 是 rg 的搜索词
//	grep -rn handoff docs/               handoff 是 grep 的搜索词
//	go build -o ./handoff ./cmd/handoff  handoff 是 go 的输出名与源路径
//
// 收紧识别面只在「没有执行包装器」时生效：段首是 handoff、或段内含执行包装器
// 时，handoff 可能正处命令位置，仍按旧判据整段定位首个 handoff 词元——包装器
// 形态维持现状、不额外放宽（fail-closed）。所以本函数只会**减少**命中，不会
// 新增：段首命中是旧命中的子集，包装器命中与旧判据逐字相同。
func judgeSegment(seg string) (bool, string) {
	fields := strings.Fields(seg)
	if len(fields) == 0 {
		return false, ""
	}
	idx := -1
	switch {
	case isHandoffBinary(fields[0]):
		// 段首即 handoff 调用：命令位置。
		idx = 0
	case hasExecWrapper(seg, fields):
		// 段内含执行包装器：无法排除 handoff 处于命令位置，按旧判据整段定位。
		// 这一路是 `sh -c "handoff dispatch"`、`xargs handoff dispatch` 的落点。
		for i, f := range fields {
			if isHandoffBinary(f) {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		// 普通参数位（rg/grep/find 的检索词、go build -o 的输出名等）→ 放行。
		return false, ""
	}
	return judgeHandoffCall(fields[idx+1:])
}

// judgeHandoffCall 对一次「命令位置的 handoff 调用」做三级判定。
//
// 参数：args 为 handoff 之后的词元（不含 handoff 本身）
//
// 顺序不可换：变更词优先于白名单词，堵住 `handoff run T1 handoff show` 这种
// 把白名单词塞进变更命令参数里的形态。
func judgeHandoffCall(args []string) (bool, string) {
	// 跳过 flag：`handoff --agentd http://x:1 tasks` 里 --agentd 与它的值都不
	// 该当成子命令。flag 的值跳不掉（它不以 - 开头），但无妨——它落进候选后
	// 两个名单都不认识，会被后面的白名单词或变更词覆盖判定
	var cand []string
	for _, f := range args {
		if strings.HasPrefix(f, "-") {
			continue
		}
		cand = append(cand, f)
	}
	if len(cand) == 0 {
		// `handoff --help`、裸 `handoff` 都落在这里：没有子命令就没有变更行为
		return false, ""
	}
	// 顺序不可换：变更词优先于白名单词
	for _, c := range cand {
		if selfCmdMutating[c] {
			return true, c
		}
	}
	for _, c := range cand {
		if selfCmdReadOnly[c] {
			return false, ""
		}
	}
	// 安全默认：两个名单都不认识的子命令一律拦。B115 的成因正是黑名单形态下
	// 「新出现的东西默认是敞的」，这条把默认反过来
	return true, cand[0]
}

// selfCmdWrappers 是「首个非 flag 实参即所运行命令」的裸包装器命令名。
//
// 为什么需要它：判据问的是「handoff 是不是被执行的命令」。这些词把紧随其后的
// 词元抬到命令位置，但都**没有 -c 旗标**，blacklist.go 的 execWrapperRx 认不出
// ——它要 `-c` 才匹配（sh/bash -c）或直接列了 xargs/eval。这里补上其余几种，使
// `env handoff dispatch`、`sudo handoff dispatch` 之类仍按现状拦。
//
// 只查段首（fields[0]）：`rg handoff time` 这种把包装器名当普通参数的形态
// 不该被抬成命令位置。
var selfCmdWrappers = map[string]bool{
	"env": true, "sudo": true, "doas": true, "nohup": true,
	"command": true, "exec": true, "nice": true, "ionice": true,
	"stdbuf": true, "setsid": true, "timeout": true, "time": true,
	"xargs": true, "eval": true,
}

// findExecFlags 是 find 的「下一词元即被执行的命令」标志。它不是独立的包装器
// 命令（段首是 find），但同样把后面的词元抬到命令位置，漏了它 `find … -exec
// handoff dispatch \;` 会从旧判据的拦变成放行。
var findExecFlags = map[string]bool{
	"-exec": true, "-execdir": true, "-ok": true, "-okdir": true,
}

// hasExecWrapper 判断段内是否含执行包装器（命令位置抬升器）。
//
// 复用 blacklist.go 的 HasExecWrapper（sh -c / bash -c / eval / xargs / 通用
// -c -e -E 形态），并补上它认不出的裸包装器命令（env/sudo/…）与 find 的
// -exec 标志。三条判据任一成立即算「无法排除 handoff 处于命令位置」。
func hasExecWrapper(seg string, fields []string) bool {
	if HasExecWrapper(seg) {
		return true
	}
	if selfCmdWrappers[tokenBase(fields[0])] {
		return true
	}
	for _, f := range fields {
		if findExecFlags[f] {
			return true
		}
	}
	return false
}

// isHandoffBinary 判断词元是否为 handoff 可执行文件的调用形态。
//
// 取 basename 而非整串比较，是为了覆盖 ./handoff 与 /usr/local/bin/handoff；
// 认 .exe 后缀是为了 Windows 执行机（B37）。反过来，cat handoff.log 的
// basename 是 handoff.log，不命中——同名文件不会被误当成调用。
func isHandoffBinary(tok string) bool {
	base := tokenBase(tok)
	return base == "handoff" || base == "handoff.exe"
}

// tokenBase 取词元的 basename（去目录、转小写），用于可执行文件名比对。
func tokenBase(tok string) string {
	base := tok
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	return strings.ToLower(base)
}
