// manualgate.go —— 人工清单判据（B383 S4 识别形态；B451 改为交审批链）。
//
// 职责：
//   - 识别用户点名的高风险命令形态：git checkout/restore --、psql -c、内联
//     Python、rg --pre；命中返回 Consult（Rule=manual-gate），交审批链裁决
//   - 以越界 Paths 上报的 Go 模块缓存读不在此判据——范围门（judgeBash 落点
//     循环）本来就硬升级，wire 测试断言该语义不回退即可
//
// 为什么仍要专门识别、而不是删掉判据落回普通链路（B451 spec 弃选乙）：普通
// 链路末端是安全命令白名单与复合只读静默，这些形态与只读段复合时可能被
// AutoAllow；本判据命中即返回 Consult，judgeBash 不再让它进白名单，从而保住
// 「永不 AutoAllow」，并留下 manual-gate 规则名供审计区分。B383 S4 的「显式
// Escalate、不许审批模型直批」已由 B451 撤销（2026-10-21 用户裁定「人工清单
// 改走审批链（硬升级不动）」）：审批链实测极少拒绝，人工往返成本过高。
//
// 边界：
//   - 只认可识别形态（checkout/restore + `--` 分隔、-c/--command、裸 python
//     标准输入脚本、--pre）；形态之外维持现状出口，不把整个命令族一锅端
//   - 本判据不承载任何硬升级：自指令、黑名单加包装器、eval、越界落点、截断
//     都在 judgeBash 里先于本判据判定（顺序承重，见 judgeBash 注释）
//   - 只覆盖清单列出的形态，不做泛化推断（如 git restore 无 -- 不拦）
package permgate

import "strings"

// RuleManualGate 是人工清单命中时填进 Verdict.Rule 的固定值。审计面据它区分
// 「用户点名的人工清单形态（交审批链）」与普通 Consult、黑名单/自指令升级。
const RuleManualGate = "manual-gate"

// manualGateVerdict 判定命令是否命中人工清单（B383 S4）。
//
// 参数：cmd 为待判命令（heredoc 闭集命中时已剥离正文——正文是数据，不得让
// 人工清单判据扫到正文里的字样）
//
// 返回：命中时 (Consult 裁决，Rule=manual-gate, true)；未命中 (零值, false)，
// 调用方沿既有链路维持现状。
//
// 注意：本函数只识别形态、不判硬升级；调用方必须先跑 judgeCommand 并原样
// 返回其 Escalate，命中后也不得再交白名单（否则人工清单形态可被 AutoAllow）。
func manualGateVerdict(cmd string) (Verdict, bool) {
	for _, seg := range splitWriteSegments(cmd) {
		toks, ok := rmTokens(seg)
		if !ok || len(toks) == 0 {
			continue // 词元边界判不死的段不猜，交既有链路
		}
		if v, hit := manualGateSegment(seg, toks); hit {
			return v, true
		}
	}
	return Verdict{}, false
}

// manualGateSegment 判定单段是否命中人工清单形态。
func manualGateSegment(seg string, toks []string) (Verdict, bool) {
	consult := func(reason string) (Verdict, bool) {
		return Verdict{Action: Consult, Rule: RuleManualGate, Reason: reason}, true
	}
	head := tokenBase(toks[0])
	switch {
	case head == "git" && len(toks) >= 3 &&
		(toks[1] == "checkout" || toks[1] == "restore") && hasToken(toks[2:], "--"):
		// git checkout/restore -- <pathspec> 丢弃/改写工作树文件：不可
		// AutoAllow，交审批链判是否只动任务仓库内文件
		return consult("人工清单：git checkout/restore -- 会改写工作树文件，交审批链裁决")
	case head == "psql":
		// psql -c 可执行 SQL 或 \! shell 元命令（v4 复审反例，官方文档实证），
		// 不是只读搜索词；-c 粘连形态（-c'sql'）同拦
		for _, f := range toks[1:] {
			if strings.HasPrefix(f, "-c") || f == "--command" || strings.HasPrefix(f, "--command=") {
				return consult("人工清单：psql -c 可执行 SQL 与 \\! shell 元命令，交审批链裁决")
			}
		}
	case head == "python" || strings.HasPrefix(head, "python3") || strings.HasPrefix(head, "python2"):
		// 内联 Python：-c 与标准输入脚本（裸命令、显式 -、heredoc 喂入、
		// 管道喂入后的裸段）都可执行任意代码
		if hasPythonInlineFlag(toks[1:]) {
			return consult("人工清单：内联 Python（-c）可执行任意代码，交审批链裁决")
		}
		if len(toks) == 1 || hasToken(toks[1:], "-") || strings.Contains(seg, "<<") {
			return consult("人工清单：内联 Python（标准输入脚本）可执行任意代码，交审批链裁决")
		}
	case head == "rg":
		// rg --pre 会对每个被检索文件执行任意程序；与 --no-config 无关
		//（--no-config 只关配置文件注入，管不住显式 --pre）
		if hasRGExecuteFlag(toks[1:]) {
			return consult("人工清单：rg --pre 会对每个检索文件执行任意程序，交审批链裁决")
		}
	}
	return Verdict{}, false
}

// hasPythonInlineFlag 判定 python 参数里是否含 -c 内联旗标，覆盖全部短旗标
// 词元形态（B383 批次 C 评审 NEEDS-FIX）：
//   - 独立与粘连：-c、-c'code'（rmTokens 剥双引号后成 -ccode）；
//   - 短旗标聚簇：-Sc、-ESc——评审真机实证 `python3 -Sc 'code'` 会把下一
//     词元当代码执行，而 HasPrefix("-c") 认不出簇形态，落 Consult 交模型直批
//     （v6 红线）。修法照 hasSedInPlace 先例按字母扫描：python 短选项表中
//     「c」唯一（只属 -c），簇内出现 c 即内联；c 带值，簇内其后字符属它的
//     实参，对检测无影响。长旗标（-- 开头）与显式 stdin 标记（-）不扫——
//     python 没有 --command 类长旗标，误伤面为零；-Xpycache_prefix=x 这类
//     粘连值里恰好含 c 的形态会被 over-reject（升级而非放行，安全侧）。
func hasPythonInlineFlag(args []string) bool {
	for _, f := range args {
		if f == "-" || !strings.HasPrefix(f, "-") || strings.HasPrefix(f, "--") {
			continue
		}
		if strings.ContainsRune(f[1:], 'c') {
			return true
		}
	}
	return false
}
