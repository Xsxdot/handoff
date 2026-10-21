// cdbase.go —— cd 相对写基准解析（B383 S2b①）。
//
// 职责：
//   - 按命令顺序行走各段，跟踪「已生效且可静态证明」的 cd 目录（基准）
//   - 把重定向落点与写命令参数位落点按当前基准绝对化，交 InScope 判范围
//   - cd 目标可证明落在三基准（Workdir/TaskDir/TaskTmpDir）之一内才更新基准；
//     越出基准、含变量/替换/glob 等不可证明形态时基准破坏（sticky）——其后
//     相对写落点无法解释，整条升级
//
// 边界：
//   - 不做通用 shell 解释器：只认段首裸 cd 两词元形态；解析不了的一律按
//     「基准破坏」处置（fail-closed），绝不冒充已证明基准
//   - 与 rmtargets.go proveDirInTmp 同哲学但独立实现：rm 放行面的基准集只有
//     TaskTmpDir，本文件是三基准（取舍记台账）；字符拒绝共用 rmPathBanChars
//   - 无 cd 的命令行为零变化：基准为空时相对落点原样交 InScope（InScope 内部
//     按 Workdir 拼接，与既有行为逐字一致）
//
// 已知残余（记台账，均落 Consult 不落 AutoAllow）：
//   - env 前缀 cd（`X=1 cd /etc`）与子 shell 内 cd（`(cd /etc; …)`）不被识别为
//     cd 段——基准不更新，相对落点仍按旧基准解释；这类命令的相对写段进不了
//     任何静默面，最坏落 Consult
//   - 单独 & 后台化的 cd（`cd /etc & echo x > f`）实际不改变主 shell 目录，
//     本文件按基准破坏处置——over-reject，安全侧
package permgate

import (
	"fmt"
	"path/filepath"
	"strings"
)

// cdResolvedTargets 按 cd 基准解析命令的全部重定向/参数位写落点。
//
// 参数：cmd 为待判命令（heredoc 闭集命中时已剥离正文）；scope 为任务范围
//
// 返回：
//   - targets: 解析后的落点列表（保持段序与来源序）。无 cd 或 cd 未生效时，
//     相对落点原样返回（交 InScope 按 Workdir 拼，与现状逐字一致）；有已证明
//     基准时，相对落点改按基准绝对化
//   - unres: 非空表示存在「cd 后相对落点无法解释」的形态（基准破坏 + 相对
//     落点），调用方必须整条 Escalate
//
// 落点提取按段进行（splitWriteSegments 引号感知切段），与整串提取等价：
// 重定向操作符与引号状态都不跨段生效。cd 段自身的重定向作用于 cd **前**的
// 工作目录（shell 语义：重定向先于简单命令执行生效），因此先提取后更新基准。
//
// cwd 是执行器在权限载荷里明示的命令工作目录（B460 Q8，Request.Cwd）：非空时作为
// 起始基准，相对落点按它解释而不是按 Workdir；空串维持现状。
func cdResolvedTargets(cmd string, scope Scope, cwd string) (targets []string, unres string) {
	base := cwd     // 已证明的 cd 落点或载荷明示的 cwd（空 = 尚在起点 Workdir）
	broken := false // 基准破坏（sticky）：其后相对落点不可解释
	rebindDone, rebind := false, false
	// 白名单变量（$TMPDIR / $HANDOFF_ARTIFACTS_DIR）重绑定嫌疑是整条命令的属性
	// （先序段的赋值污染后续段），懒判定一次并缓存——没有变量前缀目标的命令不付这份成本。
	rebindSuspect := func() bool {
		if !rebindDone {
			rebindDone = true
			rebind = scopeVarRebindSuspect(cmd)
		}
		return rebind
	}
	resolve := func(t string) (string, bool) {
		if filepath.IsAbs(t) {
			return t, true // 绝对落点与 cd 无关
		}
		if broken {
			return "", false // 基准已破坏：相对落点无法解释
		}
		if base == "" {
			return t, true // 无 cd 生效：原样交 InScope（现状行为）
		}
		// 白名单变量前缀（B460 Q7）按任务注入值绝对化，与 cd 无关；重绑定嫌疑或
		// 残留展开字符时不可解释（与下面的字符拒绝同一结论）。
		if joined, isVar, ok := scopeVarPath(t, scope, rebindSuspect()); isVar {
			return joined, ok
		}
		// 基准已生效：相对落点必须可静态证明。展开/转义/花括号/glob 字符的
		// 展开结果无法枚举（批次 B 评审 12 绕过的同族教训），一律不可解释。
		if strings.ContainsAny(t, "$\\~{},*?[") || strings.ContainsRune(t, '\'') {
			return "", false
		}
		return filepath.Join(base, t), true
	}
	for _, seg := range splitWriteSegments(cmd) {
		// 先提取本段写落点（cd 段的重定向作用于 cd 前目录），再更新基准
		for _, src := range []func(string) []string{RedirectTargets, WriteArgTargets} {
			for _, t := range src(seg) {
				r, ok := resolve(t)
				if !ok {
					return nil, fmt.Sprintf("cd 落点不可静态证明，其后相对写落点 %q 无法解释", t)
				}
				targets = append(targets, r)
			}
		}
		toks, ok := rmTokens(seg)
		if !ok {
			// 未闭合引号：词元边界判不死，cd 状态不可证明（sticky）
			broken = true
			continue
		}
		if len(toks) > 0 && toks[0] == "cd" {
			tgt, ok := cdTarget(toks)
			if !ok {
				// 裸 cd（回家目录）、带旗标、`cd -`/`cd --`/`cd +1`、多词元：落点不可证明
				broken = true
				continue
			}
			dir, ok := proveCDInBases(tgt, base, scope, rebindSuspect())
			if !ok {
				broken = true
				continue
			}
			base = dir
		}
	}
	return targets, ""
}

// cdWalkBase 只跟踪 cd 基准（不提取落点），供 heredoc 闭集校验 cd 段。
//
// 返回最终基准与破坏标记；破坏为 sticky——出现过一次不可证明的 cd，其后
// 即使出现可证明的绝对 cd 也不再恢复（over-reject，安全侧）。
func cdWalkBase(cmd string, scope Scope) (base string, broken bool) {
	rebindDone, rebind := false, false
	rebindSuspect := func() bool {
		if !rebindDone {
			rebindDone = true
			rebind = scopeVarRebindSuspect(cmd)
		}
		return rebind
	}
	for _, seg := range splitWriteSegments(cmd) {
		toks, ok := rmTokens(seg)
		if !ok {
			broken = true
			continue
		}
		if len(toks) > 0 && toks[0] == "cd" {
			tgt, ok := cdTarget(toks)
			if !ok {
				broken = true
				continue
			}
			dir, ok := proveCDInBases(tgt, base, scope, rebindSuspect())
			if !ok {
				broken = true
				continue
			}
			base = dir
		}
	}
	return base, broken
}

// proveCDInBases 把一个 cd 目标词元解析成「可证明落在三基准之一内」的绝对路径。
//
// 可解析形态与字符拒绝和 rmtargets.go proveDirInTmp 同哲学（独立实现，基准集
// 不同——那边只认 TaskTmpDir，这里认 Workdir/TaskDir/TaskTmpDir 三基准）：
//   - $TMPDIR / ${TMPDIR}、$HANDOFF_ARTIFACTS_DIR / ${HANDOFF_ARTIFACTS_DIR}（B460 Q7）
//     及其「/后缀」形态按任务注入值解读（重绑定嫌疑由 rebindSuspect 先行排除）；
//   - 绝对路径原样；
//   - 相对路径按当前基准（起点 Workdir）拼接。
//
// 不可证明（fail）：glob 元字符、单引号残留、白名单前缀之外残留 $ \ ~ { } ,
// （rmPathBanChars，over-reject 是刻意安全侧取舍）、变量重绑定、解析后
// 逃逸（../../、软链指外）。
func proveCDInBases(tok, cur string, scope Scope, rebindSuspect bool) (string, bool) {
	if cur == "" {
		cur = scope.Workdir
	}
	return proveScopedToken(tok, cur, scope, rebindSuspect)
}

// proveScopedToken 把一个路径词元解析成「可证明落在三基准之一内」的绝对路径。
//
// 与 proveCDInBases 的唯一差别：相对词元**必须**有已证明的基准 base，不默认
// 回落 Workdir——B460 新放行形态的硬边界 B2（无基准的相对落点不可证明：claude
// 的 Bash 工具跨调用保留 cwd，Workdir 只是假设）。
func proveScopedToken(tok, base string, scope Scope, rebindSuspect bool) (string, bool) {
	if strings.ContainsAny(tok, "*?[") || strings.ContainsRune(tok, '\'') {
		return "", false
	}
	if joined, isVar, ok := scopeVarPath(tok, scope, rebindSuspect); isVar {
		if !ok {
			return "", false
		}
		return provePathInBases(joined, scope)
	}
	if strings.ContainsAny(tok, rmPathBanChars) {
		return "", false
	}
	var joined string
	switch {
	case filepath.IsAbs(tok):
		joined = tok
	case base == "":
		return "", false
	default:
		joined = filepath.Join(base, tok)
	}
	return provePathInBases(joined, scope)
}

// scopeVars 是可证明前缀的白名单变量：变量名 → 从 Scope 取任务注入值。
// 两者都由 agentd 在启动执行器时注入（TMPDIR 五家 adapter；HANDOFF_ARTIFACTS_DIR
// 见 orchestration/readonly.go），值只来自任务记录。
var scopeVars = []struct {
	name string
	val  func(Scope) string
}{
	{"TMPDIR", func(s Scope) string { return s.TaskTmpDir }},
	{"HANDOFF_ARTIFACTS_DIR", func(s Scope) string { return s.ArtifactsDir }},
}

// scopeVarPath 识别 `$NAME`、`${NAME}` 及其「/后缀」形态（NAME 取 scopeVars）。
//
// 返回：
//   - isVar=false：词元不是白名单变量前缀形态（`$TMPDIRX`、`$HOME/x` 都不是）
//   - isVar=true, ok=true：joined 为按任务注入值拼出的绝对路径（未判范围）
//   - isVar=true, ok=false：形态是白名单变量，但不可解释——重绑定嫌疑、Scope 无该值、
//     或后缀残留展开/转义/花括号字符
func scopeVarPath(tok string, scope Scope, rebindSuspect bool) (joined string, isVar, ok bool) {
	for _, v := range scopeVars {
		for _, form := range []string{"$" + v.name, "${" + v.name + "}"} {
			var rest string
			switch {
			case tok == form:
			case strings.HasPrefix(tok, form+"/"):
				rest = tok[len(form)+1:]
			default:
				continue
			}
			val := v.val(scope)
			if rebindSuspect || val == "" || strings.ContainsAny(rest, rmPathBanChars) {
				return "", true, false
			}
			return filepath.Join(val, rest), true, true
		}
	}
	return "", false, false
}

// scopeVarRebindSuspect 是 tmpdirRebindSuspect 的白名单变量全集版：任一白名单变量
// （TMPDIR、HANDOFF_ARTIFACTS_DIR）在本命令里以展开之外的形态出现（scopeVarNameMisused），
// 或出现间接展开，即整条有嫌疑。合并判定是刻意的 over-reject：改了 TMPDIR 的
// 命令里 $HANDOFF_ARTIFACTS_DIR 也不再信，代价只是少放行一次。
func scopeVarRebindSuspect(cmd string) bool {
	if tmpdirRebindSuspect(cmd) {
		return true
	}
	return scopeVarNameMisused(cmd, "HANDOFF_ARTIFACTS_DIR")
}

// scopeVarNameMisused 判定变量名 name 是否在 cmd 里以「纯展开」之外的形态出现（B460 修复轮 1 B2）。
//
// 纯展开只认两种：`$NAME`（紧前一字符是 `$`）与 `${NAME}`（紧前 `${`、紧后 `}`）。其余任何
// 出现——`printf -vNAME`、`printf -v NAME`、`read NAME`、`for NAME in`、`mapfile NAME`、
// `export/declare/typeset/local/readonly NAME=`、`NAME=… cmd`、`unset NAME`、`declare -n x=NAME`、
// `${NAME:=…}`/`${NAME=…}`、`${!NAME}`、`${#NAME}`——都判嫌疑。
//
// 为什么不逐个枚举赋值形态：修前按词元认 `NAME` / `NAME=`，连写的 `-vNAME` 就漏了；
// 能改绑定的内建与语法太多（含 zsh 的），枚举总有漏网。反过来证明「只被展开」只需
// 一条字面规则。代价是 `$MYTMPDIR` 这类含名字子串的无关变量也被连坐（over-reject，
// 只是少放行一次）。按原串扫描、不区分引号：`'TMPDIR'` 字面量同样判嫌疑，安全侧。
func scopeVarNameMisused(cmd, name string) bool {
	for i := 0; i < len(cmd); {
		j := strings.Index(cmd[i:], name)
		if j < 0 {
			return false
		}
		p, end := i+j, i+j+len(name)
		switch {
		case p >= 2 && cmd[p-2:p] == "${":
			if end >= len(cmd) || cmd[end] != '}' {
				return true // ${NAME:=…}、${NAME=…} 等带操作的形态
			}
		case p >= 1 && cmd[p-1] == '$':
		default:
			return true
		}
		i = end
	}
	return false
}

// provePathInBases 判定一个绝对化的路径是否落在三基准之一的子树内（含基准
// 自身），命中返回归一化（Clean + 已存在前缀解软链）后的路径。
//
// 与 path.go InScope 同判定面（Abs、resolveExistingPrefix、filepath.Rel），
// 两侧同规约 Rel 才可比；但本函数返回命中基准内的新路径，供后续相对落点
// 继续拼接，InScope 无此需求。
func provePathInBases(joined string, scope Scope) (string, bool) {
	if joined == "" {
		return "", false
	}
	p, err := filepath.Abs(joined)
	if err != nil {
		return "", false
	}
	p = resolveExistingPrefix(filepath.Clean(p))
	for _, b := range []string{scope.Workdir, scope.TaskDir, scope.TaskTmpDir} {
		if b == "" {
			continue
		}
		rb, aerr := filepath.Abs(b)
		if aerr != nil {
			continue
		}
		rb = resolveExistingPrefix(filepath.Clean(rb))
		rel, rerr := filepath.Rel(rb, p)
		if rerr != nil {
			continue
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return p, true
		}
	}
	return "", false
}

// cdChainTrusted 判定命令里的 cd 段能否当作「已生效」（B460 修复，三处放行出口共用：
// heredoc-in-scope、rm-in-scope、范围内写入）。
//
// 参数：segs 为切好的段，ops 为段间连接符（len(ops) == len(segs)-1，取值 "&&"、
// "||"、";"、"\n"）。
//
// 规则：无 cd 段 → 真；有 cd 段时，首个 cd 之后的每个连接符都必须是 `&&`，且任一 cd
// 前紧邻的连接符不得是 `||`。理由是 shell 的短路语义：
//   - `cd A; w`、`cd A` + 换行 + `w`：cd 失败 w 照样执行，cwd 未知；
//   - `cd A || w`、`cd A && x || w`：cd 失败恰好走到 w；
//   - `x || cd A && w`：x 成功时 cd 被跳过，w 在旧 cwd 执行。
//
// 只有全 `&&` 链保证「走到写段时，前面每个 cd 都执行且成功了」。
//
// 另外任一 cd 段的目标不可信（cdTarget 判假：`cd -`、`cd --`、`cd -P`、`cd +1`、裸 `cd`、
// `cd ~…`）时整条链不可信——cd 成功了也不知道去了哪里。
func cdChainTrusted(segs, ops []string) bool {
	first := -1
	for i, seg := range segs {
		if toks, ok := rmTokens(seg); ok && len(toks) > 0 && toks[0] == "cd" {
			if _, ok := cdTarget(toks); !ok {
				// B460 修复轮 1 B1：选项形 / 特殊目标 / 无参 cd 的去向是 $OLDPWD、$HOME 或
				// 目录栈，静态不可知——整条 cd 链不可信，三处出口一律回落。
				return false
			}
			if i > 0 && i-1 < len(ops) && ops[i-1] == "||" {
				return false
			}
			if first < 0 {
				first = i
			}
		}
	}
	if first < 0 {
		return true
	}
	for j := first; j < len(ops); j++ {
		if ops[j] != "&&" {
			return false
		}
	}
	return true
}

// cdTarget 从 cd 段词元（rmTokens 结果，toks[0] == "cd"）取可当路径解读的目标（B460 修复轮 1 B1）。
//
// 返回 ok=false 的形态（cd 的去向不是「名为该词元的目录」）：
//   - 词元数不是 2：裸 `cd`（回 $HOME）、`cd -P dir` 等带旗标或多参数形态；
//   - 目标以 `-` 开头：`cd -`（回 $OLDPWD）、`cd --`（等同裸 cd）、`cd -P`/`cd -L`、zsh `cd -1`；
//   - 目标以 `+` 开头：zsh 目录栈 `cd +1`；
//   - 目标以 `~` 开头：家目录展开；
//   - 空目标。
//
// 判前剥掉前导的引号与反斜杠：`cd "-"`、`cd '--'`、`cd \-` 在 shell 里与 `cd -` 同义。
// 选项形的相对目录（真名为 `-x` 的目录）须写成 `cd ./-x`，那仍可信。
func cdTarget(toks []string) (string, bool) {
	if len(toks) != 2 {
		return "", false
	}
	t := strings.TrimLeft(toks[1], "\\\"'")
	if t == "" || strings.HasPrefix(t, "-") || strings.HasPrefix(t, "+") || strings.HasPrefix(t, "~") {
		return "", false
	}
	return toks[1], true
}
