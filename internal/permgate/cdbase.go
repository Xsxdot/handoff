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
func cdResolvedTargets(cmd string, scope Scope) (targets []string, unres string) {
	base := ""      // 已证明的 cd 落点（空 = 尚在起点 Workdir）
	broken := false // 基准破坏（sticky）：其后相对落点不可解释
	rebindDone, rebind := false, false
	// $TMPDIR 重绑定嫌疑是整条命令的属性（先序段的赋值污染后续段），懒判定
	// 一次并缓存——没有 $TMPDIR cd 目标的命令不付这份成本。
	rebindSuspect := func() bool {
		if !rebindDone {
			rebindDone = true
			rebind = tmpdirRebindSuspect(cmd)
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
			if len(toks) != 2 {
				// 裸 cd（回家目录）、带旗标、多词元：落点不可证明
				broken = true
				continue
			}
			dir, ok := proveCDInBases(toks[1], base, scope, rebindSuspect())
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
			rebind = tmpdirRebindSuspect(cmd)
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
			if len(toks) != 2 {
				broken = true
				continue
			}
			dir, ok := proveCDInBases(toks[1], base, scope, rebindSuspect())
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
//   - $TMPDIR / ${TMPDIR} 及其「/后缀」形态按任务注入值解读（重绑定嫌疑由
//     rebindSuspect 先行排除）；
//   - 绝对路径原样；
//   - 相对路径按当前基准（起点 Workdir）拼接。
//
// 不可证明（fail）：glob 元字符、单引号残留、白名单前缀之外残留 $ \ ~ { } ,
// （rmPathBanChars，over-reject 是刻意安全侧取舍）、$TMPDIR 重绑定、解析后
// 逃逸（../../、软链指外）。
func proveCDInBases(tok, cur string, scope Scope, rebindSuspect bool) (string, bool) {
	if strings.ContainsAny(tok, "*?[") || strings.ContainsRune(tok, '\'') {
		return "", false
	}
	// $TMPDIR/${TMPDIR} 白名单前缀剥除后，残留部分不得再含展开/转义/花括号字符
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
		if rebindSuspect || scope.TaskTmpDir == "" {
			return "", false
		}
		joined = scope.TaskTmpDir
	case strings.HasPrefix(tok, "$TMPDIR/"):
		if rebindSuspect || scope.TaskTmpDir == "" {
			return "", false
		}
		joined = scope.TaskTmpDir + tok[len("$TMPDIR"):]
	case strings.HasPrefix(tok, "${TMPDIR}/"):
		if rebindSuspect || scope.TaskTmpDir == "" {
			return "", false
		}
		joined = scope.TaskTmpDir + tok[len("${TMPDIR}"):]
	case filepath.IsAbs(tok):
		joined = tok
	default:
		anchor := cur
		if anchor == "" {
			anchor = scope.Workdir
		}
		if anchor == "" {
			return "", false
		}
		joined = filepath.Join(anchor, tok)
	}
	return provePathInBases(joined, scope)
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
