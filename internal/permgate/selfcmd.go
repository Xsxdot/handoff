// selfcmd.go —— handoff 自指令判据（身份越权）。
//
// 职责：
//   - 识别「executor 在 shell 里调用 handoff 自身 CLI 的变更类子命令」
//   - 只读子命令按白名单放行，其余一律判为自指令
//
// 边界：
//   - 不判危险性：rm -rf / sudo 这类由 blacklist.go 管。两者威胁轴不同——
//     那边问「这条命令会不会破坏东西」，这边问「这个角色该不该做这件事」
//   - 不做引号剥离与执行包装器识别：那是 judgeCommand 的编排职责，本文件
//     只提供一次「这段文本里有没有自指令」的纯判定，无 I/O、无状态
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
// 判定分三步（spec §3.3）：
//  1. 按 | ; & 换行切段，逐段独立判定
//  2. 段内找首个 basename 为 handoff/handoff.exe 的词元，其后不以 - 开头的
//     词元即候选
//  3. 三级判定，顺序不可换：含变更词 → 命中；否则含白名单词 → 放行；
//     否则候选非空 → 命中
//
// 注意：本函数不处理引号与执行包装器，调用方（judgeCommand）负责按原文与
// StripQuoted 结果各跑一遍。
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
func judgeSegment(seg string) (bool, string) {
	fields := strings.Fields(seg)
	idx := -1
	for i, f := range fields {
		if isHandoffBinary(f) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false, ""
	}
	// 跳过 flag：`handoff --agentd http://x:1 tasks` 里 --agentd 与它的值都不
	// 该当成子命令。flag 的值跳不掉（它不以 - 开头），但无妨——它落进候选后
	// 两个名单都不认识，会被后面的白名单词或变更词覆盖判定
	var cand []string
	for _, f := range fields[idx+1:] {
		if strings.HasPrefix(f, "-") {
			continue
		}
		cand = append(cand, f)
	}
	if len(cand) == 0 {
		// `handoff --help`、裸 `handoff`、`cd ~/handoff`、`rm -rf handoff`
		// 都落在这里：没有子命令就没有变更行为
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

// isHandoffBinary 判断词元是否为 handoff 可执行文件的调用形态。
//
// 取 basename 而非整串比较，是为了覆盖 ./handoff 与 /usr/local/bin/handoff；
// 认 .exe 后缀是为了 Windows 执行机（B37）。反过来，cat handoff.log 的
// basename 是 handoff.log，不命中——同名文件不会被误当成调用。
func isHandoffBinary(tok string) bool {
	base := tok
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	base = strings.ToLower(base)
	return base == "handoff" || base == "handoff.exe"
}
