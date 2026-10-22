// heredoc.go —— heredoc 受限闭集（B383 S2b②）。
//
// 职责：
//   - closedHeredoc：识别卡事件 17074 的 `cd $TMPDIR/cardq && cat > main.go
//     <<'EOF' … EOF` 同构形态——引用的字面定界符、单个裸 cat 把正文写入一个
//     文件、整条命令及所有段无管道/命令替换/包装器/执行型后继；命中返回剥离
//     正文后的命令与写落点词元
//   - heredocInScopeVerdict：命中形态的放行判定——cd 段经 cdWalkBase 逐项
//     证明、落点经 cd 基准解析并证明落在任务范围内时，整条 AutoAllow
//   - stripDataHeredocBodies（B466）：闭集之外的「数据型」heredoc（cat/tee/python/node
//     消费、引用定界符、不接管道）只为落点解析剥离正文——正文不是 shell，其中的 `>`、
//     `{` 不是写落点；其余判据面仍读原文
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
	// cdTrusted 为假表示 cd 段后有非 `&&` 连接符（B460 修复）：cd 可能失败而
	// cat 照样执行，写落点不可证明，放行出口必须拒绝（正文剥离不受影响）。
	cdTrusted bool
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
	segs, ops, ok := splitHeredocSegments(cmdLine)
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
	// B460 修复轮 2 F1：各段命令词必须朴素（`"cat"`、`'cd'` 不在闭集）。
	if !commandWordsPlain(segs) {
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
	return &closedHeredocInfo{stripped: cmdLine, target: target,
		cdTrusted: cdChainTrusted(segs, ops)}
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
func splitHeredocSegments(cmd string) ([]string, []string, bool) {
	var ops []string
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
				return nil, nil, false
			}
			ops = append(ops, "\n")
		case r == '(', r == ')':
			return nil, nil, false
		case r == '&':
			if i+1 >= len(rs) || rs[i+1] != '&' {
				return nil, nil, false // 单 &：后台执行
			}
			if !flush() {
				return nil, nil, false
			}
			ops = append(ops, "&&")
			i++
		case r == '|':
			if i+1 < len(rs) && rs[i+1] == '|' {
				if !flush() {
					return nil, nil, false
				}
				ops = append(ops, "||")
				i++
				continue
			}
			return nil, nil, false // 管道
		case r == ';':
			if !flush() {
				return nil, nil, false
			}
			ops = append(ops, ";")
		default:
			b.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, nil, false
	}
	if !flush() {
		return nil, nil, false
	}
	return segs, ops, true
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
	if !hd.cdTrusted {
		// B460 修复：`cd X; cat > f <<'EOF'`、`cd X || cat > f …` 里 cd 失败时 cat
		// 在未知 cwd 写——不放行，交回既有链路（Consult）。
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

// dataHeredocConsumers 是正文不被 shell 解释的 heredoc 消费者命令词（B466）。正文对它们
// 是数据（cat/tee 写进文件）或另一种语言的源码（python/node），其中的 `>`、`{` 不是
// shell 重定向。bash/sh/env/sudo 等会把正文当 shell 执行或转交执行的命令一律不在此列。
var dataHeredocConsumers = map[string]bool{
	"cat": true, "tee": true, "python": true, "python3": true, "node": true,
}

// pendingHeredoc 是一行命令里登记的 heredoc：正文从该行之后的下一行开始。
type pendingHeredoc struct {
	delim    string
	dash     bool // <<- ：终止符行允许前导制表符
	quoted   bool // 定界符整词被引号包裹（正文不做任何展开）
	fdPrefix bool // `2<<'EOF'` 一类 fd 前缀
	piped    bool // 所在段接管道（`cat <<'EOF' | sh`）
	segStart int  // 所在段在命令里的起点（取消费者命令词）
}

// stripDataHeredocBodies 只供落点解析：剥掉「数据型」heredoc 的正文与终止符行（B466）。
//
// 数据型 = 定界符整词引号包裹 + 无 fd 前缀 + 所在段不接管道 + 所在行引号外无括号、
// 反引号、`$(`、`[[` + 消费者命令词朴素且属 dataHeredocConsumers。其余 heredoc 的正文
// 原样保留（照旧当 shell 文本参与落点解析）。
//
// 返回：剥离后的命令；没有可剥的正文、或任何一处解析不了（未终止正文、半引号或含
// 展开字符的定界符、操作符在末行）时**原样返回整条命令**——现状判定，fail-closed。
//
// 注意：剥离结果只能喂落点提取与 cd 基准跟踪。黑名单、人工清单、自指令、凭据守卫
// 必须继续看原文——python 正文是会执行的代码，只是不是 shell。
func stripDataHeredocBodies(cmd string) string {
	if !strings.Contains(cmd, "<<") {
		return cmd
	}
	var out strings.Builder
	stripped := false
	for i := 0; i < len(cmd); {
		eol, hds, ok := scanHeredocLine(cmd, i)
		if !ok {
			return cmd
		}
		if eol >= len(cmd) {
			if len(hds) > 0 {
				return cmd // 操作符在末行：正文缺失，解析不了
			}
			out.WriteString(cmd[i:])
			break
		}
		out.WriteString(cmd[i : eol+1])
		i = eol + 1
		for _, h := range hds {
			end, ok := heredocBodyEnd(cmd, i, h)
			if !ok {
				return cmd
			}
			if h.quoted && !h.fdPrefix && !h.piped && dataConsumer(cmd[h.segStart:eol]) {
				stripped = true
			} else {
				out.WriteString(cmd[i:end])
			}
			i = end
		}
	}
	if !stripped {
		return cmd
	}
	return out.String()
}

// dataConsumer 判定段的命令词是否为数据型消费者（朴素且在白名单内）。
func dataConsumer(seg string) bool {
	w, plain, ok := commandWord(seg)
	return ok && plain && dataHeredocConsumers[w]
}

// scanHeredocLine 从 start 起扫一个逻辑命令行（到引号外的换行为止，反斜杠续行不算
// 行尾），登记其中的 heredoc 操作符。
//
// 返回：eol 为行尾换行的下标（无换行时为 len(cmd)）；hds 为本行的 heredoc（按出现序，
// 正文按此序紧随其后）；ok=false 表示定界符形态解析不了，调用方整条放弃剥离。
//
// 引号外 `#` 在词首起注释，注释里的 `<<` 不是操作符。行内出现括号、反引号、`$(`、
// `[[` 时本行全部 heredoc 视为非数据型（`<<` 可能是算术移位、正文可能进命令替换）。
func scanHeredocLine(cmd string, start int) (eol int, hds []pendingHeredoc, ok bool) {
	var quote byte
	complexLine := false
	segStart := start
	wordStart := true
	finish := func(at int) (int, []pendingHeredoc, bool) {
		if complexLine {
			for k := range hds {
				hds[k].quoted = false // 保留正文
			}
		}
		return at, hds, true
	}
	for j := start; j < len(cmd); {
		c := cmd[j]
		var next byte
		if j+1 < len(cmd) {
			next = cmd[j+1]
		}
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			}
			j++
			continue
		}
		if quote == '"' {
			switch {
			case c == '\\':
				j += 2
				continue
			case c == '"':
				quote = 0
			case c == '`' || (c == '$' && next == '('):
				complexLine = true
			}
			j++
			continue
		}
		switch {
		case c == '\n':
			return finish(j)
		case c == '\\':
			j += 2 // 转义字符（含续行换行）
			wordStart = false
			continue
		case c == '\'' || c == '"':
			quote = c
		case c == '#' && wordStart:
			k := strings.IndexByte(cmd[j:], '\n')
			if k < 0 {
				return finish(len(cmd))
			}
			j += k
			continue
		case c == '(' || c == ')' || c == '`' || (c == '$' && next == '(') || (c == '[' && next == '['):
			complexLine = true
		case c == '&' && next == '&', c == '|' && next == '|':
			j += 2
			segStart, wordStart = j, true
			continue
		case c == '|':
			for k := range hds {
				if hds[k].segStart == segStart {
					hds[k].piped = true
				}
			}
			segStart = j + 1
		case c == ';':
			segStart = j + 1
		case c == '&':
			// `2>&1`、`&>f`、`>&2` 是重定向的一部分；其余单 & 是后台分隔符
			if !(j > start && (cmd[j-1] == '>' || cmd[j-1] == '<')) && next != '>' {
				segStart = j + 1
			}
		case c == '<' && next == '<':
			if j+2 < len(cmd) && cmd[j+2] == '<' {
				j += 3 // here-string，不是 heredoc
				wordStart = false
				continue
			}
			h, end, good := parseHeredocOp(cmd, j)
			if !good {
				return 0, nil, false
			}
			h.segStart = segStart
			h.fdPrefix = j > start && cmd[j-1] >= '0' && cmd[j-1] <= '9'
			hds = append(hds, h)
			j = end
			wordStart = false
			continue
		}
		wordStart = c == ' ' || c == '\t' || c == ';' || c == '&' || c == '|' || c == '(' || c == ')'
		j++
	}
	return finish(len(cmd))
}

// parseHeredocOp 解析 at 处的 `<<`/`<<-` 操作符与定界符词。
//
// 返回 end 为定界符词之后的下标。ok=false：定界符为空、引号未闭合、半引号
// （`'E'OF`）、或含 `\` `$` 反引号——这些形态的终止行难以与 bash 逐字一致，整条放弃剥离。
func parseHeredocOp(cmd string, at int) (h pendingHeredoc, end int, ok bool) {
	k := at + 2
	if k < len(cmd) && cmd[k] == '-' {
		h.dash = true
		k++
	}
	for k < len(cmd) && (cmd[k] == ' ' || cmd[k] == '\t') {
		k++
	}
	if k >= len(cmd) {
		return h, 0, false
	}
	wordEnd := func(p int) bool {
		return p >= len(cmd) || strings.IndexByte(" \t\n;&|<>()", cmd[p]) >= 0
	}
	if q := cmd[k]; q == '\'' || q == '"' {
		close := strings.IndexByte(cmd[k+1:], q)
		if close <= 0 {
			return h, 0, false // 未闭合或空定界符
		}
		h.delim = cmd[k+1 : k+1+close]
		h.quoted = true
		end = k + 1 + close + 1
	} else {
		p := k
		for !wordEnd(p) {
			p++
		}
		h.delim = cmd[k:p]
		end = p
	}
	if h.delim == "" || strings.ContainsAny(h.delim, "\n\\$`'\"") || !wordEnd(end) {
		return h, 0, false
	}
	return h, end, true
}

// heredocBodyEnd 从 start（正文首行）起找终止符行，返回终止符行之后的下标。
// 找不到终止符（正文未终止）返回 ok=false。
func heredocBodyEnd(cmd string, start int, h pendingHeredoc) (int, bool) {
	for p := start; p < len(cmd); {
		line, next := cmd[p:], len(cmd)
		if nl := strings.IndexByte(cmd[p:], '\n'); nl >= 0 {
			line, next = cmd[p:p+nl], p+nl+1
		}
		if h.dash {
			line = strings.TrimLeft(line, "\t")
		}
		if line == h.delim {
			return next, true
		}
		p = next
	}
	return 0, false
}
