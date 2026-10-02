// rmtargets.go —— rm 删除目标提取与「范围内删除」放行判定（B383 S2a）。
//
// 职责：
//   - rmDeletionTargets：从 rm 的参数列表提取删除目标。**独立实现**，不与
//     RedirectTargets/WriteArgTargets 合并——「删除目标」与「重定向/参数位写
//     落点」语义不同源（写是把新内容落到某处，删是让既有路径消失；B383 spec
//     明示两者分开判定），且 rm 的旗标面（-rf 连写、-- 分隔符）是它独有的
//   - rmInScopeVerdict：小型正例闭集的放行判定——整条命令由「可证明进入
//     TaskTmpDir 的 cd 段」与「全部目标可证明落在 TaskTmpDir 的 rm 段」构成、
//     且至少有一个 rm 段时，黑名单 rm 命中解除，整条按 AutoAllow 放行；
//     任何一丝不满足整条不放宽（逐段逐目标一票否决）
//
// 边界：
//   - 不做通用 shell 解释器：只认清楚命令形态（spec §S2），解析不了的形态
//     一律维持现状（黑名单命中 → Escalate，未命中 → Consult）
//   - rm 放行面**只认 TaskTmpDir**——不是 Workdir/TaskDir；cd 进 Workdir 后
//     的相对删除不放宽
//   - cd 本批只读解析（确认 cd 目标可证明进入 TaskTmpDir 或其子目录）；通用
//     「cd 后相对写基准」是批次 C 的活，不在这里实现写路径的基准重写
//   - 反斜杠与展开字符不做解释、直接拒绝：本文件不解释反斜杠转义（\.
//     转义剥掉后会重组成 ..，Clean 不认 \ 分隔，静态路径与真实解析路径
//     会分叉——批次 B 评审用 $TMPDIR/\../etc 实测证伪过「误差只落安全侧」
//     的早先假设），凡词元残留 \ 与展开字符（见 rmPathBanChars）一律
//     整条不放宽，不存在「切得更碎也能对齐真实语义」的假设
package permgate

import (
	"path/filepath"
	"strings"
)

// RuleRmInScope 是「范围内删除」放行时填进 Verdict.Rule 的固定值，与
// RuleSafeCommand 并列——manager 的 permission_auto_allow 结构化审计按 Rule
// 落载荷，审计面要能区分「白名单静默」与「黑名单命中被 scope 解除」。
const RuleRmInScope = "rm-in-scope"

// rmFlagLetters 是允许跳过的 rm 短旗标字母闭集（POSIX/GNU/BSD rm 的无参短
// 旗标，簇旗标 -rf/-fr/-rv 逐字母展开判）。簇里出现任何其他字母（-e、-c、
// -z 之类）都可能是未知旗标——不能证明它不吃参数，就把后续词元的身份
// （旗标值还是删除目标）判死了，整条不放宽。
const rmFlagLetters = "rRfIidv"

// rmLongFlags 是允许跳过的 rm 长旗标闭集。rm 的长旗标都不吃**独立**参数
// （--interactive 的可选值只能写成 --interactive=WHEN，见独立分支），跳过
// 它们不会把「旗标的值」误当成删除目标；闭集之外的未知长旗标一律不放宽。
var rmLongFlags = map[string]bool{
	"--recursive":        true,
	"--force":            true,
	"--interactive":      true,
	"--one-file-system":  true,
	"--preserve-root":    true,
	"--no-preserve-root": true,
	"--dir":              true,
	"--verbose":          true,
}

// rmPathBanChars 是删除目标词元的展开/转义/花括号字符拒绝集（评审 NEEDS-FIX
// 根因修复）：$ 变量展开、\ 转义（Clean 不认反斜杠分隔，$TMPDIR/\../etc 的
// \. 剥掉后露出 ..）、~ tilde 展开、{ } , 花括号展开一变多。$TMPDIR/${TMPDIR}
// 白名单前缀由 proveDirInTmp 先行剥除，仅对残留部分做本拒绝。
const rmPathBanChars = "$\\~{},"

// rmInScopeVerdict 是 B383 S2a 的「范围内删除」放行判定（judgeBash 专用）。
//
// 返回 ok=true 时 verdict 为 AutoAllow（Rule=RuleRmInScope）；ok=false 表示
// 有一丝不满足，调用方沿既有链路（judgeCommand → 黑名单）维持现状。
//
// 放行条件（spec §S2 逐条，全部满足才放行）：
//
//	a. 每个 rm 删除目标都可静态解析——已知旗标与 -- 分隔符跳过；未知旗标、
//	   glob、引号混排、命令替换等解析不了的形态一律不放宽；
//	b. 绝对目标解析后必须落在 Scope.TaskTmpDir 内（不是 Workdir/TaskDir）；
//	   $TMPDIR 字面量按任务注入值解读，但仅当本命令中 TMPDIR 未被重绑定：
//	   同名赋值（含 env 前缀形态）、export、unset、命令替换、间接展开
//	   （${!…}）、子 shell、包装器等任何无法静态证明绑定值的形态 → 整条
//	   不放宽；
//	c. 相对目标必须有先序段的可证明 cd（cd 目标本身要落进 TaskTmpDir 子树）
//	   作为解析基准；cd 出基准、或无 cd 的裸相对目标不放宽；
//	d. 逐段逐目标全过，不局部放行——`rm -rf $TMPDIR/a; rm -rf /etc` 任何一
//	   目标不满足 → 整条维持现状；
//	e. 包装器形态（sh -c / bash -c 等，复用 execWrapperRx）维持硬拦；
//	f. 解析后逃逸（../../、软链指外）按解析后路径判，与 path.go InScope 的
//	   软链/前缀防护同哲学。
func rmInScopeVerdict(cmd string, scope Scope) (Verdict, bool) {
	// 快速短路：任务没有 TaskTmpDir 时闭集不可能成立（TestJudgeFailClosedTable
	// 的无 TaskTmpDir 范围因此原样 Escalate）；没有 rm 词元的命令没有 rm 段。
	// 词元判定用小写精确匹配：`RM -rf …` 大小写形态不在闭集（黑名单 (?i) 命中
	// 后维持现状升级，方向安全）。
	if scope.TaskTmpDir == "" || !strings.Contains(cmd, "rm") {
		return Verdict{}, false
	}
	// 全局守卫：以下任一形态出现，整条命令的 TMPDIR 绑定值或最终执行内容
	// 都无法静态证明，逐段分析之前就整条否决。
	if hasCommandSubstitution(cmd) {
		// `$(…)`/反引号在引号内也会执行（hasCommandSubstitution 扫原文），
		// 目标展开值不可静态证明。
		return Verdict{}, false
	}
	if HasExecWrapper(cmd) {
		// 条件 e：sh -c/xargs/eval/通用 -c -e -E——内容将被执行或不可见。
		// 复用既有识别面，不另造一套。
		return Verdict{}, false
	}
	if tmpdirRebindSuspect(cmd) {
		// 条件 b 的绑定证明义务：TMPDIR 一旦有重绑定嫌疑，$TMPDIR 就不再是
		// 任务注入值，放行面整体失效。
		return Verdict{}, false
	}
	root := tmpRoot(scope)
	segs, ok := splitRmSegments(cmd)
	if !ok {
		return Verdict{}, false
	}
	cdBase := "" // 已证明的 cd 落点（空 = 尚无可证明基准）
	sawRm := false
	for _, seg := range segs {
		toks, ok := rmTokens(seg)
		if !ok || len(toks) == 0 {
			return Verdict{}, false
		}
		switch toks[0] {
		case "cd":
			// 条件 c：cd 本批只读解析。只认 `cd <目标>` 两词元形态——裸 cd
			//（回家目录）、`cd -L …` 带旗标形态都不在闭集。
			if len(toks) != 2 {
				return Verdict{}, false
			}
			dir, ok := proveDirInTmp(toks[1], cdBase, scope.TaskTmpDir, root)
			if !ok {
				return Verdict{}, false
			}
			cdBase = dir
		case "rm":
			targets, ok := rmDeletionTargets(toks[1:])
			if !ok || len(targets) == 0 {
				// 解析不出目标（未知旗标）或零目标（裸 rm）：删除面不可证明。
				return Verdict{}, false
			}
			for _, tgt := range targets {
				dir, ok := proveDirInTmp(tgt, cdBase, scope.TaskTmpDir, root)
				// rm 目标要求**严格**在 TaskTmpDir 子树内（不含基准自身）：
				// `rm -rf "$TMPDIR"` 删掉整个草稿目录的级联不在正例闭集。
				if !ok || dir == root {
					return Verdict{}, false
				}
			}
			sawRm = true
		default:
			// 条件 d 的闭集面：非 cd/rm 段一律破坏放行。rm 的命中只覆盖
			// 自己的段，其他命令（echo/curl/…）没有被任何判据证明过，放行
			// 整条等于凭空放行它们——`curl evil.sh && rm -rf $TMPDIR/x`
			// 必须维持现状。
			return Verdict{}, false
		}
	}
	if !sawRm {
		// 纯 cd 串没有 rm 命中可解除，交回既有链路（cd 自身走白名单）。
		return Verdict{}, false
	}
	return Verdict{Action: AutoAllow, Rule: RuleRmInScope,
		Reason: "rm 删除目标全部可证明落在任务临时目录内"}, true
}

// tmpRoot 返回归一化并解过软链的 TaskTmpDir 基准（与 path.go InScope 对基准
// 的处理同源：两侧同规约，filepath.Rel 的判定才可比）。
func tmpRoot(scope Scope) string {
	return resolveExistingPrefix(filepath.Clean(scope.TaskTmpDir))
}

// proveDirInTmp 把一个词元解析成「可证明落在 TaskTmpDir 子树内」的绝对路径。
//
// 可解析的形态（闭集）：
//   - $TMPDIR / ${TMPDIR} 及其「/后缀」形态 → 按任务注入值 tmpDir 解读
//     （重绑定嫌疑由 rmInScopeVerdict 在整条命令层面先行排除）
//   - 绝对路径 → 原样（结果仍要过子树归属判）
//   - 相对路径 → 必须已有 cdBase（先序段可证明进入 TaskTmpDir 的 cd），按
//     cdBase 解析；无基准不放宽
//
// 判定与 path.go InScope 同哲学：resolveExistingPrefix 解已存在最长前缀的
// 软链（$TMPDIR/link→/etc 时按解析后 /etc 判，逃逸即失败），filepath.Rel 判
// 子树归属（不用字符串前缀，HasPrefix("/repo-evil/x","/repo") 的教训）。
//
// 不可证明的形态（一律 fail）：
//   - glob 元字符（*?[）：展开结果无法静态枚举，`$TMPDIR/.*` 这类形态里还
//     可能藏进 `..`
//   - 残留单引号：shell 对单引号内容不做变量展开（'$TMPDIR' 是字面文件名），
//     与双引号/裸词元的展开语义不同源，混排后无法静态对应真实参数
//   - 展开/转义/花括号字符（$ 白名单外残留、\、~、{、}、,）：见下方字符拒绝
func proveDirInTmp(tok, cdBase, tmpDir, root string) (string, bool) {
	if tmpDir == "" || strings.ContainsAny(tok, "*?[") || strings.ContainsRune(tok, '\'') {
		return "", false
	}
	// 展开字符拒绝（批次 B 评审 NEEDS-FIX 的根因修复）：剥掉 $TMPDIR/${TMPDIR}
	// 白名单前缀后，词元残留部分不得再含 $ \ ~ { } , 任何一个。四形态解析
	// **之前**先做这道否决——否则：
	//   - 相对分支把「不像白名单形式」的展开词元当字面量拼进 cdBase 判内，
	//     而 shell 运行时把它们展开成绝对路径或空串（$HOME/x、$TMPDIRX/sub、
	//     ${TMPDIR:-/etc}/x、~/.ssh），真实删除面完全不可证明；
	//   - Clean 不认反斜杠分隔，$TMPDIR/\../etc 的 \. 转义剥掉后露出 ..，
	//     静态看到的路径与真实解析路径分叉（\.. 被当成组件名而非父目录）；
	//   - 花括号展开一变多（$TMPDIR/{a,/etc/passwd}、{/etc/passwd,/etc/hosts}），
	//     单词元证明不了多目标；${TMPDIR}x / "$TMPDIR"x 的引号-花括号邻接
	//     拼接展开成基准的**兄弟路径**，词元已不是白名单形式，残留 $ { } 即拒。
	var rest string
	switch {
	case tok == "$TMPDIR" || tok == "${TMPDIR}":
		rest = ""
	case strings.HasPrefix(tok, "$TMPDIR/"):
		rest = tok[len("$TMPDIR/"):]
	case strings.HasPrefix(tok, "${TMPDIR}/"):
		rest = tok[len("${TMPDIR}/"):]
	default:
		rest = tok
	}
	if strings.ContainsAny(rest, rmPathBanChars) {
		return "", false
	}
	var joined string
	switch {
	case tok == "$TMPDIR" || tok == "${TMPDIR}":
		joined = tmpDir
	case strings.HasPrefix(tok, "$TMPDIR/"):
		joined = tmpDir + tok[len("$TMPDIR"):]
	case strings.HasPrefix(tok, "${TMPDIR}/"):
		joined = tmpDir + tok[len("${TMPDIR}"):]
	case filepath.IsAbs(tok):
		joined = tok
	default:
		if cdBase == "" {
			// 无 cd 的裸相对目标：没有任何已证明的解析基准（Workdir 不是
			// rm 放行面的基准），不放宽。
			return "", false
		}
		joined = filepath.Join(cdBase, tok)
	}
	p := resolveExistingPrefix(filepath.Clean(joined))
	rel, err := filepath.Rel(root, p)
	if err != nil {
		// 跨卷等无法求相对路径的情形：视作不在基准内。
		return "", false
	}
	if rel != "." &&
		(rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		// 解析后逃逸（$TMPDIR/../../etc、软链指外）：按解析后路径判，不放宽。
		return "", false
	}
	return p, true
}

// rmDeletionTargets 从 rm 的参数列表提取删除目标。
//
// 参数：args 为 rm 命令名之后的全部词元（引号已由 rmTokens 剥离）
// 返回：目标切片与 ok；ok=false 表示出现了闭集之外的旗标形态——不能证明
// 「旗标与目标的边界」，后续词元是旗标值还是删除目标判不死，整条不放宽。
//
// 规则：
//   - 已知短旗标（rmFlagLetters 的任意簇）与已知长旗标跳过；
//   - `--` 之后一律是目标——`rm -- -rf-looking-name` 删的是形似旗标的文件名；
//   - `-` 单独成词按目标处理（POSIX rm 的普通操作数）；
//   - GNU getopt 的换位语义：旗标可出现在目标之后（`rm a -rf`），解析不因
//     先见目标而停止。
func rmDeletionTargets(args []string) ([]string, bool) {
	var targets []string
	endOfFlags := false
	for _, a := range args {
		if !endOfFlags && a == "--" {
			endOfFlags = true
			continue
		}
		if !endOfFlags && strings.HasPrefix(a, "-") && a != "-" {
			if strings.HasPrefix(a, "--") {
				if rmLongFlags[a] || strings.HasPrefix(a, "--interactive=") {
					continue
				}
				// 未知长旗标：不能证明它不吃独立参数。
				return nil, false
			}
			for _, c := range a[1:] {
				if !strings.ContainsRune(rmFlagLetters, c) {
					// 未知短旗标字母（含 -e/-c 这类会与包装器语义纠缠的）。
					return nil, false
				}
			}
			continue
		}
		targets = append(targets, a)
	}
	return targets, true
}

// tmpdirRebindSuspect 扫全命令词元，回答「本命令中 TMPDIR 是否有重绑定嫌疑」。
//
// 两类嫌疑（任一出现即整条不放宽）：
//   - 词元以 TMPDIR= 开头：同名赋值的所有形态——`TMPDIR=/etc; …`、
//     `TMPDIR=/etc rm …`（env 前缀）、`export TMPDIR=/x`，引号剥掉后都在
//     这一类里；
//   - 裸 TMPDIR 词元（无 $ 前缀）：`unset TMPDIR`、`export TMPDIR`、
//     `env -u TMPDIR`、`read TMPDIR` 等触碰绑定本身的形态。$TMPDIR/${TMPDIR}
//     的**引用**不在嫌疑内（放行面正是要证明引用值未变）。
//
// 另拦 ${! （间接展开）：展开目标由运行时值决定，静态不可证明。
//
// 为什么是全命令扫描而不只看段首：重绑定嫌疑是整条命令的属性——先序段里的
// `export TMPDIR=/x` 污染后续所有段的 $TMPDIR 语义，一票否决到整条。
func tmpdirRebindSuspect(cmd string) bool {
	toks, ok := rmTokens(cmd)
	if !ok {
		// 未闭合引号：词元边界都判不死，按有嫌疑处理。
		return true
	}
	for _, t := range toks {
		if t == "TMPDIR" || strings.HasPrefix(t, "TMPDIR=") {
			return true
		}
		if strings.Contains(t, "${!") {
			return true
		}
	}
	return false
}

// splitRmSegments 把命令按引号外的 `&&` / `||` / `;` 切成段，供闭集逐段判定。
//
// 返回 ok=false 的形态（任一出现即整条不放宽）：
//   - 未闭合引号、换行、`(` `)`（子 shell——里面的绑定值与执行内容不可见）
//   - 单独的 `|`（管道：rm 段不允许带管道）与单独的 `&`（后台执行）
//   - `>` `<`（重定向）：rm 放行面不解析重定向——`rm -rf $TMPDIR/x > out /etc`
//     这类「重定向后面还藏目标」的形态，tokenizer 若在 > 处停会把 /etc 漏成
//     不可见；judgeBash 的落点循环虽会先判重定向落点，但这里必须整段拒绝才
//     能保证「提取到的目标 = 真实删除目标」
//   - 空段（连接符前后缺命令）
func splitRmSegments(cmd string) ([]string, bool) {
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
		case r == '\n', r == '(', r == ')', r == '`':
			return nil, false
		case r == '&':
			if i+1 >= len(rs) || rs[i+1] != '&' {
				return nil, false // 单 &：后台执行，不在闭集
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
			return nil, false // 单管道：不在闭集
		case r == ';':
			if !flush() {
				return nil, false
			}
		case r == '>' || r == '<':
			return nil, false
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

// rmTokens 把一段命令切成词元。引号内空白不分词；双引号剥离（内容按可展开
// 的裸词元对待，"$TMPDIR" 与 $TMPDIR 同义，与 shell 语义一致）；单引号字符
// **保留**在词元里——shell 对单引号内容不做展开，proveDirInTmp 据此把含 '
// 的词元判为不可证明，而不是冒充展开后的路径。
func rmTokens(seg string) ([]string, bool) {
	var toks []string
	var b strings.Builder
	var quote rune // 0 = 不在引号内；'"' 双引号（剥字符）；'\'' 单引号（留字符）
	active := false
	flush := func() {
		if active {
			toks = append(toks, b.String())
			b.Reset()
			active = false
		}
	}
	for _, r := range seg {
		switch {
		case quote == '"' && r != '"':
			b.WriteRune(r)
			active = true
		case quote == '"':
			quote = 0
		case quote == '\'' && r != '\'':
			b.WriteRune(r)
			active = true
		case quote == '\'':
			b.WriteRune(r) // 单引号字符保留：proveDirInTmp 的不可证明标记
			quote = 0
		case r == '\'' || r == '"':
			quote = r
			active = true
			if r == '\'' {
				b.WriteRune(r) // 单引号字符保留：proveDirInTmp 的不可证明标记
			}
		case r == ' ' || r == '\t' || r == '\r':
			flush()
		default:
			b.WriteRune(r)
			active = true
		}
	}
	if quote != 0 {
		return nil, false // 未闭合引号：词元边界不可证明
	}
	flush()
	return toks, true
}
