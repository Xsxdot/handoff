// manualgate.go —— 人工清单显式 Escalate 判据（B383 S4）。
//
// 职责：
//   - 把用户先前裁定「必须保持人工」的命令形态从 Consult 面收口到显式
//     Escalate：git checkout/restore --、psql -c、内联 Python、rg --pre
//   - 以越界 Paths 上报的 Go 模块缓存读不在此判据——范围门（judgeBash 落点
//     循环）本来就硬升级，wire 测试断言该语义不回退即可
//
// 为什么必须在判定层显式 Escalate：仅排除 AutoAllow 不够——非白名单默认
// Consult，而 Consult 可由审批模型直接批准，兑现不了「人工」；真正的 handoff
// 自指令与越界路径的硬升级在既有判据里，不受本文件影响。
//
// 边界：
//   - 只认可识别形态（checkout/restore + `--` 分隔、-c/--command、裸 python
//     标准输入脚本、--pre）；形态之外维持现状出口——增加人工工单是 B=a 安全
//     边界的已知成本，但不得把整个命令族一锅端
//   - 只减不增的反向约束不适用于本判据（它是新增的升级面，出自用户裁定），
//     但同样只覆盖清单列出的形态，不做泛化推断（如 git restore 无 -- 不拦）
package permgate

import "strings"

// RuleManualGate 是人工清单命中时填进 Verdict.Rule 的固定值。审计面据它区分
// 「用户裁定的人工门」与黑名单/自指令升级。
const RuleManualGate = "manual-gate"

// manualGateVerdict 判定命令是否命中人工清单（B383 S4）。
//
// 参数：cmd 为待判命令（heredoc 闭集命中时已剥离正文——正文是数据，不得让
// 人工门扫到正文里的字样）
//
// 返回：命中时 (Escalate 裁决, true)；未命中 (零值, false)，调用方沿既有
// 链路维持现状。
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
	escalate := func(reason string) (Verdict, bool) {
		return Verdict{Action: Escalate, Rule: RuleManualGate, Reason: reason}, true
	}
	head := tokenBase(toks[0])
	switch {
	case head == "git" && len(toks) >= 3 &&
		(toks[1] == "checkout" || toks[1] == "restore") && hasToken(toks[2:], "--"):
		// git checkout/restore -- <pathspec> 丢弃/改写工作树文件（用户裁定
		// 保持人工；AutoAllow 或模型直批都不可接受）
		return escalate("人工清单：git checkout/restore -- 会改写工作树文件，保持人工裁决")
	case head == "psql":
		// psql -c 可执行 SQL 或 \! shell 元命令（v4 复审反例，官方文档实证），
		// 不是只读搜索词；-c 粘连形态（-c'sql'）同拦
		for _, f := range toks[1:] {
			if strings.HasPrefix(f, "-c") || f == "--command" || strings.HasPrefix(f, "--command=") {
				return escalate("人工清单：psql -c 可执行 SQL 与 \\! shell 元命令，保持人工裁决")
			}
		}
	case head == "python" || strings.HasPrefix(head, "python3") || strings.HasPrefix(head, "python2"):
		// 内联 Python：-c 与标准输入脚本（裸命令、显式 -、heredoc 喂入、
		// 管道喂入后的裸段）都可执行任意代码
		if hasPythonInlineFlag(toks[1:]) {
			return escalate("人工清单：内联 Python（-c）可执行任意代码，保持人工裁决")
		}
		if len(toks) == 1 || hasToken(toks[1:], "-") || strings.Contains(seg, "<<") {
			return escalate("人工清单：内联 Python（标准输入脚本）可执行任意代码，保持人工裁决")
		}
	case head == "rg":
		// rg --pre 会对每个被检索文件执行任意程序；与 --no-config 无关
		//（--no-config 只关配置文件注入，管不住显式 --pre）
		if hasRGExecuteFlag(toks[1:]) {
			return escalate("人工清单：rg --pre 会对每个检索文件执行任意程序，保持人工裁决")
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
