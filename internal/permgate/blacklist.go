// blacklist.go —— 黑名单硬规则与命令类判据。
//
// 职责：
//   - 持有内置黑名单模式表（自 internal/agentd/approver.go 迁入）
//   - 引号字面量剥离与执行包装器识别
//   - judgeCommand：命令类请求（bash 及其余工具）的判定
//
// 边界：
//   - 不认识工具名、不碰路径：路由在 permgate.go，路径判定在 path.go
package permgate

import (
	"fmt"
	"regexp"
	"strings"
)

// builtinBlacklist 是内置黑名单（(?i) 不区分大小写编译）。
//
// 拦截意图（逐条）：
//   - rm 破坏性删除：短连写（-rf/-fr）与分写（-r ... -f）之外，还要拦长选项
//     （--recursive/--force）与长短混写——`rm --recursive --force /`、`rm -r --force`
//     都是脚本常规写法，只认短连写会被绕过。两段式匹配：rm 后同时含「递归标志」
//     与「强制标志」即命中（两种顺序各一条；RE2 不支持 lookahead）
//   - git push 强推：`git\b.*\bpush\b` 而非 `git\s+push`——`git -C /repo push
//     --force origin main` 的 -C 会插在中间；--force\b 同时覆盖 --force-with-lease
//   - sudo：提权命令影响整个主机，绝不能由廉价模型自动放行
//   - git reset --hard：丢弃未提交改动，不可逆
//   - drop table/database：删库删表不可恢复
//
// 曾经有过第 9 条 `\bproduction\b|\bprod\b`，2026-08-09 删除（spec §4.2）：
// 它想拦「操作生产环境」，实现成的却是「文本里出现 prod 字样」——实测 9 条
// 误命中里有 4 条出自它（`go test ./internal/prod/...`、`npm run build:prod`、
// `Write: /repo/docs/production.md`、`cat docs/production-checklist.md`）。
// 而 agent 跑在 worktree 里，接触生产的途径是 `kubectl -n prod` 这类命令形态，
// 不是关键词。正则分不出这两者，廉价模型分得出——该语义已移入
// approverPromptTemplate。用户仍可经 config 的 approver.blacklist 自定义补回。
var builtinBlacklist = []string{
	`rm\s+-[a-z]*[rf][a-z]*[rf]`,                                  // rm -rf / -fr 常见连写
	`rm\s+-[rf]\b.*\s-[rf]\b`,                                     // rm -r ... -f 分写
	`rm\b[^;&\n]*(--recursive|-r\b|-R\b)[^;&\n]*(--force\b|-f\b)`, // 递归段在前
	`rm\b[^;&\n]*(--force\b|-f\b)[^;&\n]*(--recursive|-r\b|-R\b)`, // 强制段在前
	`git\b.*\bpush\b.*(--force\b|-f\b)`,                           // force push
	`\bsudo\b`,
	`git\s+reset\s+--hard`,
	`drop\s+(table|database)`,
}

// execWrapperRx 识别「把命令藏进字符串/变量再执行」的包装器。
//
// 为什么需要它：黑名单改为对剥离引号后的文本匹配之后，`sh -c "rm -rf /"`
// 剥完就干净了。命中这条即恢复硬拦，不给绕过留口子。
//
// 三种形态（大小写不敏感）：
//   - 解释器直调：sh/bash/zsh/dash/ksh/env 的 `-c` 形态——`env ... -c` 的 -c
//     由第二段 alternation 的通用执行标志兜住（spec §4.1 包装器清单）
//   - eval：内容是不可见的变量/构造串，判据无从判定（TestQuoteBypass 锁死）
//   - xargs：把管道输入当作参数喂给后续命令
//   - 通用执行标志：任意工具的 `-c` / `-e` / `-E`（psql -c / mysql -e /
//     python -c / ruby -e 等「执行字符串参数」形态）
//
// 用 \b 边界而非 strings.Contains：`git push` 的 "sh"、`ssh host` 的 "sh"
// 前后都是词字符，不会误伤（TestHasExecWrapper 锁死）。
//
// 通用执行标志为什么敢用这么宽的 `-c/-e/-E`：包装器只在「黑名单已命中」时
// 参与升级（见 judgeCommand），`git log -c` / `gcc -c` 这类无害 -c 用法不命中
// 黑名单，不会因此被误升级。
var execWrapperRx = regexp.MustCompile(
	`(?i)\b(sh|bash|zsh|dash|ksh|env)\b[^|;&]*\s-c\b` +
		`|\beval\b|\bxargs\b` +
		`|\b[a-z][a-z0-9_-]*\b[^|;&\n]*\s-[ceE]\b`)

// evalRx 单独识别 eval：它是唯一「未命中黑名单也要硬升级」的包装器——eval 的
// 参数是变量/运行时构造的串（`eval "$DANGER"`），判据看不到它最终执行什么。
// 其余包装器（sh -c / xargs）未命中黑名单时按 Consult 放给廉价模型，避免
// `find . | xargs grep TODO` 这类无害用法被连环升级（B23 的噪音教训）。
var evalRx = regexp.MustCompile(`(?i)\beval\b`)

// HasExecWrapper 判断命令是否含执行包装器（sh -c / bash -c / eval / xargs /
// 通用 -c/-e/-E 执行标志等）。
func HasExecWrapper(s string) bool { return execWrapperRx.MatchString(s) }

// StripQuoted 把成对引号内的内容清空，保留引号本身。
//
// 参数：s 为原始命令串
// 返回：剥离后的串，如 `git commit -m "去掉 rm -rf 分支"` → `git commit -m ""`
//
// 注意：
//   - 不做完整 shell 解析。反斜杠转义（`"it\"s"`）会让本函数提前认为引号闭合，
//     结果是**剥得更少**——剥得少意味着更可能仍然命中黑名单、更可能 Escalate，
//     方向是安全的，因此接受
//   - 未闭合引号：引号之后的内容全部丢弃
func StripQuoted(s string) string {
	var b strings.Builder
	var quote rune // 0 = 不在引号内
	for _, r := range s {
		switch {
		case quote == 0 && (r == '\'' || r == '"'):
			quote = r
			b.WriteRune(r)
		case quote != 0 && r == quote:
			quote = 0
			b.WriteRune(r)
		case quote != 0:
			// 引号内字面量：丢弃
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// judgeCommand 判定命令类请求（spec §4.1 的规则 + 两条实证补强）。
//
// 参数：s 为待判文本（bash 路由传 Command，其余路由传 Text）
// 返回：Consult 或 Escalate；本函数**永不返回 AutoAllow**
//
// 规则（逐条判定）：
//   - 剥离引号后仍是 handoff 自指令 → Escalate（身份越权，与危险性无关）
//   - 原文是自指令且含执行包装器   → Escalate（`sh -c "handoff dispatch"`）
//   - 剥离引号字面量后仍命中黑名单 → Escalate（真危险，无论引号与否）
//   - 原文命中且含执行包装器       → Escalate（引号内内容将被执行，不给
//     降级口子——`sh -c "rm -rf /"` 剥完虽干净，但内容正是要执行的）
//   - 原文命中但无执行包装器       → Consult（仅引号内字面量命中，如
//     `git commit -m "去掉 rm -rf 分支"`，降级交模型看一眼）
//   - 原文未命中但含 eval          → Escalate（eval 参数是变量/构造串，
//     内容不可见，无法判定）
//   - 原文未命中且无 eval          → Consult（与改动前一致）
//
// 核心取舍：误判的修法不是「直接放行」，而是「从硬拦降级为让模型看一眼」——
// 未枚举到的引号绕过形态最坏也只落到廉价模型手上；而执行包装器与 eval 两条
// 把「内容将被执行/不可见」的形态恢复硬拦，不给绕过留口子。
func (g *Gate) judgeCommand(s string) Verdict {
	// 自指令判据前置于黑名单：这是身份越权，与命令本身危不危险无关。
	// 引号处理与下方黑名单逐条同构——剥完引号还在的是真调用，只在引号内
	// 出现的（commit message）降级落回黑名单链
	if hit, sub := IsSelfCommand(StripQuoted(s)); hit {
		return g.selfCmdVerdict(sub, "剥离引号字面量后仍是自指令调用")
	}
	// 包装器把 handoff 放在引号边界内时，strings.Fields 会得到 `"handoff`；
	// 这里只把引号替换为空格以恢复词元边界，不在纯判据里引入引号语义。
	if hit, sub := IsSelfCommand(selfCmdTokenText(s)); hit && HasExecWrapper(s) {
		return g.selfCmdVerdict(sub, "自指令藏在执行包装器的引号里，内容将被执行")
	}

	hit, rule := g.match(s)
	if h2, r2 := g.match(StripQuoted(s)); h2 {
		return Verdict{Action: Escalate, Reason: "剥离引号字面量后仍命中黑名单", Rule: r2}
	}
	if HasExecWrapper(s) {
		if hit {
			return Verdict{Action: Escalate,
				Reason: "命中黑名单且含执行包装器，引号内内容将被执行", Rule: rule}
		}
		if evalRx.MatchString(s) {
			return Verdict{Action: Escalate,
				Reason: "命令含 eval 且内容不可见，无法判定是否危险"}
		}
	}
	if hit {
		return Verdict{Action: Consult,
			Reason: "仅引号内字面量命中黑名单，降级交审批者裁决", Rule: rule}
	}
	return Verdict{Action: Consult, Reason: "黑名单未命中"}
}

// selfCmdVerdict 构造自指令裁决，并在 permgate 侧留一条 Debug 现场。
//
// 参数：sub 为命中的子命令名，why 为命中路径（真调用 / 藏在包装器里）
//
// 注意：这里只打 Debug，权威的 Warn 一条落在 agentd 的 judgePermission——
// 那里才拿得到 task id 与 permission id，能把这条判定关联到具体任务。
// 两处都打 Warn 会让同一件事在日志里出现两遍。
func (g *Gate) selfCmdVerdict(sub, why string) Verdict {
	g.log.Debug("命中 handoff 自指令判据", "subcommand", sub, "why", why)
	return Verdict{
		Action: Escalate,
		Reason: fmt.Sprintf("executor 试图调用 handoff 变更命令 %s（%s）", sub, why),
		Rule:   RuleSelfCommand,
	}
}

// selfCmdTokenText 为包装器路径恢复被引号粘连的词元边界。
func selfCmdTokenText(s string) string {
	return strings.NewReplacer(`"`, " ", `'`, " ").Replace(s)
}
