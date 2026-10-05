// heredoc.go —— heredoc 受限闭集（B383 S2b②）。
//
// 职责：
//   - closedHeredoc：识别卡事件 17074 的 `cd $TMPDIR/cardq && cat > main.go
//     <<'EOF' … EOF` 同构形态——引用的字面定界符、单个裸 cat 把正文写入一个
//     文件、整条命令及所有段无管道/命令替换/包装器/执行型后继；命中返回剥离
//     正文后的命令与写落点词元
//   - heredocInScopeVerdict：命中形态的放行判定——cd 段经 cdWalkBase 逐项
//     证明、落点经 cd 基准解析并证明落在任务范围内时，整条 AutoAllow
//
// 安全核心：定界符带引号时 shell 对正文不做任何展开（无变量替换、无命令
// 替换），正文是写给 cat 的纯数据；cat 只把字节写进落点文件。因此命中闭集时
// 正文可作为数据从各判据面剔除（RedirectTargets/黑名单/自指令/人工门都不扫
// 正文）；不命中时正文**绝不剔除**——按现状整条判定，绝不能剔除正文后交既有
// 宽松白名单。
//
// 边界：
//   - 不做通用 shell 解析：解析不了的形态（未引用定界符、未终止正文、终止符
//     后还有命令、多 heredoc、fd 前缀、here-string）一律不命中 → 现状升级
//   - 裸 cat 的消费者信任沿用现有安全命令白名单对裸命令名的信任前提（grep/
//     rg/cat 等裸名已在此前提下静默）：若执行器的 PATH/函数/alias 能改变其
//     解析，须先收窄到可信绝对可执行文件——该环境证明排 acceptance（台账
//     记边界声明），本批不动该前提
//   - 消费者只认裸 cat：bash/sh/python/tee 等任何其他消费者、cat 带额外旗标
//     或参数，一律不命中
package permgate

import (
	"path/filepath"
	"strings"
)

// RuleHeredocInScope 是 heredoc 闭集放行时填进 Verdict.Rule 的固定值，与
// RuleRmInScope 并列——放行的是「正文按数据剔除后的范围内写入」，审计面要
// 能区分它与白名单静默、rm 范围放行。
const RuleHeredocInScope = "heredoc-in-scope"

// closedHeredocInfo 是闭集命中的解析结果。
type closedHeredocInfo struct {
	// stripped 是剥离正文后的命令（命令行区原样保留，含 heredoc 操作符），
	// 供落点提取与命令类判定使用——正文从此不进任何判据面。
	stripped string
	// target 是 cat 的写落点词元（原文，未绝对化；引号已由词元化剥除）。
	target string
}

// closedHeredoc 判定命令是否为受控 heredoc 写入闭集形态。
//
// 返回 nil 表示不在闭集（解析不了、形态不符、一丝不满足），调用方按现状
// 整条判定。命中时正文已剥离且写落点词元已提取。
func closedHeredoc(cmd string) *closedHeredocInfo {
	rs := []rune(cmd)
	opStart, opEnd, delim, ok := findQuotedHeredocOp(rs)
	if !ok || delim == "" {
		return nil
	}
	// 命令行区 = 操作符所在行（heredoc 正文从下一个换行开始）
	cmdLine := cmd
	rest := ""
	if nl := strings.IndexRune(cmd[min(opEnd, len(cmd)):], '\n'); nl >= 0 {
		cmdLine = cmd[:opEnd+nl]
		rest = cmd[opEnd+nl+1:]
	}
	// 同一命令行区出现第二个 heredoc 操作符（含未引用形式）→ 不在闭集
	if hasAnotherHeredocOp([]rune(cmdLine), opEnd) {
		return nil
	}
	// 命令行区不得含命令替换/反引号（`$(`、反引号在引号内也会执行）
	if hasCommandSubstitution(cmdLine) {
		return nil
	}
	// 正文终止符：某一行恰等于定界符；其后只允许空白
	lines := strings.Split(rest, "\n")
	termAt := -1
	for i, ln := range lines {
		if ln == delim {
			termAt = i
			break
		}
	}
	if termAt < 0 {
		return nil // 正文未终止：解析不了，现状判定
	}
	if strings.TrimSpace(strings.Join(lines[termAt+1:], "\n")) != "" {
		return nil // 终止符后还有命令：执行型后继不在闭集
	}
	// 命令行区切段：只允许 cd 段与唯一的 cat 段
	segs, ok := splitHeredocSegments(cmdLine)
	if !ok {
		return nil
	}
	opToken := cmd[opStart:opEnd]
	catIdx := -1
	for i, seg := range segs {
		if strings.Contains(seg, opToken) {
			if catIdx >= 0 {
				return nil
			}
			catIdx = i
		}
	}
	if catIdx < 0 {
		return nil
	}
	target, ok := validateHeredocCat(segs[catIdx], opToken)
	if !ok {
		return nil
	}
	for i, seg := range segs {
		if i == catIdx {
			continue
		}
		// 其余段必须是纯 `cd <目标>` 两词元形态（目标合法性由 cdWalkBase 判）
		toks, ok := rmTokens(seg)
		if !ok || len(toks) != 2 || toks[0] != "cd" {
			return nil
		}
	}
	return &closedHeredocInfo{stripped: cmdLine, target: target}
}

// findQuotedHeredocOp 在命令文本里找第一个「引用字面定界符」的 heredoc 操作符
// （<<'D' 或 <<"D"）。
//
// 不算数的形态（任一出现即整体不命中）：未引用定界符（正文会做展开）、
// here-string（<<<）、fd 前缀（2<<）、定界符引号未闭合或为空。
func findQuotedHeredocOp(rs []rune) (opStart, opEnd int, delim string, ok bool) {
	var quote rune
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		switch {
		case r == '\'' || r == '"':
			quote = r
		case r == '<' && i+1 < len(rs) && rs[i+1] == '<':
			if i > 0 && rs[i-1] >= '0' && rs[i-1] <= '9' {
				return 0, 0, "", false // fd 前缀 heredoc
			}
			j := i + 2
			if j >= len(rs) || (rs[j] != '\'' && rs[j] != '"') {
				return 0, 0, "", false // 未引用定界符 / here-string
			}
			q := rs[j]
			k := j + 1
			for k < len(rs) && rs[k] != q && rs[k] != '\n' {
				k++
			}
			if k >= len(rs) || rs[k] != q || k == j+1 {
				return 0, 0, "", false // 引号未闭合或定界符为空
			}
			return i, k + 1, string(rs[j+1 : k]), true
		}
	}
	return 0, 0, "", false
}

// hasAnotherHeredocOp 判断 from 之后（引号外）是否还有任何 `<<` 操作符。
func hasAnotherHeredocOp(rs []rune, from int) bool {
	var quote rune
	for i := from; i < len(rs); i++ {
		r := rs[i]
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		switch {
		case r == '\'' || r == '"':
			quote = r
		case r == '<' && i+1 < len(rs) && rs[i+1] == '<':
			return true
		}
	}
	return false
}

// splitHeredocSegments 把命令行区按 `&&` / `||` / `;` / 换行切段（引号感知）。
//
// 出现管道（单独 |）、单 &（后台）、括号（子 shell / 进程替换）、空段即整体
// 不命中——闭集不允许执行型后继。
func splitHeredocSegments(cmd string) ([]string, bool) {
	var segs []string
	var b strings.Builder
	var quote rune
	flush := func() bool {
		s := strings.TrimSpace(b.String())
		b.Reset()
		if s == "" {
			return false
		}
		segs = append(segs, s)
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
			b.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
			b.WriteRune(r)
		case r == '\n':
			if !flush() {
				return nil, false
			}
		case r == '(', r == ')':
			return nil, false
		case r == '&':
			if i+1 >= len(rs) || rs[i+1] != '&' {
				return nil, false // 单 &：后台执行
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
			return nil, false // 管道
		case r == ';':
			if !flush() {
				return nil, false
			}
		default:
			b.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, false
	}
	if !flush() {
		return nil, false
	}
	return segs, true
}

// validateHeredocCat 校验 cat 段形态：裸 cat + 恰一个 `>`/`>>` 重定向 + 目标
// 词元，操作符两侧（合并后）不得再有任何其他词元；额外旗标/参数一律不命中。
// 重定向与操作符的先后次序都认（`cat > f <<'EOF'` 与 `cat <<'EOF' > f` 同构）。
//
// 返回写落点词元与 ok。
func validateHeredocCat(seg, opToken string) (string, bool) {
	opPos := strings.Index(seg, opToken)
	if opPos < 0 {
		return "", false
	}
	// 摘掉操作符后合并两侧：重定向+目标必须恰好占满，别无他物
	segNoOp := seg[:opPos] + " " + seg[opPos+len(opToken):]
	toks, ok := rmTokens(segNoOp)
	if !ok || len(toks) == 0 || toks[0] != "cat" {
		return "", false // 裸 cat 之外的头（bash/tee/…）不在闭集
	}
	redirects := 0
	target := ""
	for i := 1; i < len(toks); i++ {
		t := toks[i]
		switch {
		case t == ">" || t == ">>":
			if i+1 >= len(toks) {
				return "", false
			}
			redirects++
			target = toks[i+1]
			i++
		case strings.HasPrefix(t, ">>"):
			redirects++
			target = t[2:]
		case strings.HasPrefix(t, ">"):
			redirects++
			target = t[1:]
		default:
			return "", false // 额外旗标/参数不在闭集
		}
	}
	if redirects != 1 || target == "" {
		return "", false
	}
	// 目标词元拒绝展开/转义/花括号/glob/引号字符（与批次 B 字符拒绝同哲学，
	// over-reject 是刻意安全侧取舍）；& | < 开头是 fd 复制/双重重定向形态
	if strings.ContainsAny(target, "$\\~{},*?[]'\"") ||
		strings.ContainsAny(target[:1], "&|<") {
		return "", false
	}
	return target, true
}

// heredocInScopeVerdict 是 B383 S2b② 的放行判定（judgeBash 专用）。
//
// 前提：closedHeredoc 已确认整条命令符合闭集形态（正文是数据）。放行还须：
//   - cd 段逐项可证明（cdWalkBase 破坏即不放宽——正文外的 cd 仍逐项判定）；
//   - 写落点按 cd 基准解析后可证明落在任务范围内。
//
// 返回 ok=false 时调用方沿既有链路维持现状（落点越界的情形已由落点范围循环
// 先行升级，不会走到这里）。
func heredocInScopeVerdict(cmd string, scope Scope) (Verdict, bool) {
	hd := closedHeredoc(cmd)
	if hd == nil {
		return Verdict{}, false
	}
	base, broken := cdWalkBase(hd.stripped, scope)
	if broken {
		return Verdict{}, false
	}
	joined := hd.target
	switch {
	case filepath.IsAbs(joined):
	case base != "":
		joined = filepath.Join(base, joined)
	case scope.Workdir != "":
		joined = filepath.Join(scope.Workdir, joined)
	default:
		return Verdict{}, false
	}
	abs, ok := provePathInBases(joined, scope)
	if !ok {
		return Verdict{}, false // 越界：交回既有链路（落点循环已升级）
	}
	return Verdict{Action: AutoAllow, Rule: RuleHeredocInScope,
		Reason: "heredoc 正文按数据剔除，写入落点可证明落在任务范围内: " + abs}, true
}
