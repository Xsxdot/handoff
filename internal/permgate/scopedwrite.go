// scopedwrite.go —— 任务范围内建目录与写入的可证明放行（B460 S6）。
//
// 职责：
//   - 认一小类「写」形态：`mkdir`（全部目标）与 `echo`/`printf` 带写重定向（全部
//     落点），每个落点都可静态证明落在任务范围（Workdir/TaskDir/TaskTmpDir）内时，
//     整条命令按白名单同档 AutoAllow，审计 id 为 mkdir / echo-write / printf-write
//   - 允许它们与 cd、既有只读静默段用 `&&` `||` `;` 串成复合命令
//
// 边界（plan §3 硬边界，改动须回主会话）：
//   - B1 门禁不扩大：落点只认绝对字面路径、已证明基准 + 不含展开字符的相对路径、
//     白名单变量前缀（$TMPDIR、$HANDOFF_ARTIFACTS_DIR）；`$HOME/x`、`~/x`、glob、
//     花括号、反斜杠、单引号字面量一律不进。**不用 InScope 的宽松解释**——它会把
//     `$HOME/x` 拼成 `<workdir>/$HOME/x` 判在范围内
//   - B2 不扩大存量 cwd 假设：既无命令内可证明 cd、请求又无 Cwd 的相对落点不进
//     （claude 的 Bash 工具跨调用保留 cwd，Workdir 只是假设）
//   - B3：只在 judgeBash 的落点范围循环、judgeCommand（自指令/黑名单/包装器/eval）
//     与人工清单之后才会被问到；本文件另行拒绝命令替换与执行包装器
//   - 纯计算 + 文件系统只读探测（provePathInBases 解软链），不做 shell 解释
package permgate

import (
	"strings"
)

// scopedWriteID 判定整条命令是否属于「范围内可证明写入」闭集。
//
// 参数：
//   - cmd: 待判命令（heredoc 闭集命中时已剥离正文，与白名单同输入）
//   - cwd: 载荷明示的工作目录（Request.Cwd）；空 = 没有起始基准（B2）
//   - scope: 任务范围（含 ArtifactsDir 作 $HANDOFF_ARTIFACTS_DIR 的展开值）
//
// 返回：命中时第一个写段的审计 id（mkdir / echo-write / printf-write）与 true；
// 任一段、任一落点证明不了即 ("", false)，调用方维持原裁决（通常 Consult）。
//
// 闭集（逐段一票否决，绝不半段放行）：
//   - cd 段：`cd <目标>` 两词元，目标可证明在范围内；相对目标要求已有基准
//   - mkdir 段：已知旗标（-p -v -m MODE 及其聚簇/长形态），全部目标可证明
//   - echo/printf 段：无管道，全部非丢弃写重定向落点可证明；无写重定向的
//     echo/printf 段按只读段处理
//   - 其余段：既有只读静默段（matchSilentSegment），且不得有任何写落点
//   - 至少一个写段（纯只读命令归既有白名单，不在这里重复放行）
//   - cd 链可信（cdChainTrusted：首个 cd 之后全 `&&`，cd 前不接 `||`）
func scopedWriteID(cmd, cwd string, scope Scope) (string, bool) {
	if hasCommandSubstitution(cmd) || HasExecWrapper(cmd) {
		return "", false
	}
	segs, ops, ok := splitSilentSegmentsOps(cmd)
	if !ok {
		return "", false
	}
	// cd 的生效要可证明（与 heredoc / rm 出口共用同一判据）。
	if !cdChainTrusted(segs, ops) {
		return "", false
	}
	rebind := scopeVarRebindSuspect(cmd)
	base := cwd
	firstID := ""
	for _, seg := range segs {
		toks, ok := rmTokens(seg)
		if !ok || len(toks) == 0 {
			return "", false
		}
		switch toks[0] {
		case "cd":
			if len(toks) != 2 || hasUnquotedShellOp(seg) {
				return "", false
			}
			dir, ok := proveScopedToken(toks[1], base, scope, rebind)
			if !ok {
				return "", false
			}
			base = dir
		case "mkdir":
			if hasUnquotedShellOp(seg) {
				return "", false
			}
			targets, ok := mkdirProvableArgs(toks[1:])
			if !ok {
				return "", false
			}
			for _, t := range targets {
				if _, ok := proveScopedToken(t, base, scope, rebind); !ok {
					return "", false
				}
			}
			if firstID == "" {
				firstID = "mkdir"
			}
		case "echo", "printf":
			writes, ok := echoWriteTargets(seg)
			if !ok {
				return "", false
			}
			if len(writes) == 0 {
				// 无写重定向：只读 echo/printf，按只读段判（下方 default 同逻辑）
				if _, ok := matchSilentSegment(seg); !ok {
					return "", false
				}
				continue
			}
			for _, t := range writes {
				if _, ok := proveScopedToken(t, base, scope, rebind); !ok {
					return "", false
				}
			}
			if firstID == "" {
				firstID = toks[0] + "-write"
			}
		default:
			if _, ok := matchSilentSegment(seg); !ok {
				return "", false
			}
			if len(WriteArgTargets(seg)) > 0 {
				// gofmt -w / git add 之类白名单写形态不在本闭集：它们的落点
				// 按 Workdir 解释，cd 之后不可证明
				return "", false
			}
		}
	}
	return firstID, firstID != ""
}

// mkdirProvableArgs 严格解析 mkdir 参数，返回全部目录目标。
//
// 只认 BSD 与 GNU 共有的旗标：-p、-v、-m MODE（含 -mMODE、聚簇 -pv、-pm MODE），
// 以及 GNU 长形态 --parents、--verbose、--mode=MODE / --mode MODE；`--` 之后全是目标。
// 其他旗标（-Z、--context 等）不能证明不吃参数，整条不放行；零目标不放行。
func mkdirProvableArgs(args []string) ([]string, bool) {
	var targets []string
	endOfFlags := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if endOfFlags || a == "-" || !strings.HasPrefix(a, "-") {
			targets = append(targets, a)
			continue
		}
		switch {
		case a == "--":
			endOfFlags = true
		case a == "--parents", a == "--verbose", strings.HasPrefix(a, "--mode="):
		case a == "--mode":
			if i+1 >= len(args) {
				return nil, false
			}
			i++
		case strings.HasPrefix(a, "--"):
			return nil, false
		default:
			body := a[1:]
			for j := 0; j < len(body); j++ {
				switch body[j] {
				case 'p', 'v':
					continue
				case 'm':
					if j == len(body)-1 { // 值在下一个词元
						if i+1 >= len(args) {
							return nil, false
						}
						i++
					}
					j = len(body) // 其后字符是权限位
				default:
					return nil, false
				}
			}
		}
	}
	return targets, len(targets) > 0
}

// echoWriteTargets 取 echo/printf 段的非丢弃写重定向落点（按 shell 词元语义重解）。
//
// 落点按原文经 rmTokens 重新切词：双引号剥离、单引号保留为「不可证明」标记——
// `> '$TMPDIR/x'` 写的是字面名文件，与展开语义不同源；`"$TMPDIR"/../x` 这类
// 引号后紧跟字符的拼接词元（adjoined）直接拒绝。返回 ok=false 的形态：段内有
// 管道（写面不止重定向）、拼接词元、切不出恰好一个词元。
func echoWriteTargets(seg string) ([]string, bool) {
	stages, ok := splitPipeStages(seg)
	if !ok || len(stages) != 1 {
		return nil, false
	}
	var out []string
	for _, t := range redirectTargetsQuoted(seg) {
		if IsDiscardTarget(t.path) {
			continue
		}
		if t.adjoined {
			return nil, false
		}
		toks, ok := rmTokens(t.raw)
		if !ok || len(toks) != 1 || toks[0] == "" {
			return nil, false
		}
		out = append(out, toks[0])
	}
	return out, true
}

// hasUnquotedShellOp 判断段内引号外是否有管道或重定向符（cd / mkdir 段不允许带）。
func hasUnquotedShellOp(seg string) bool {
	var quote rune
	for _, r := range seg {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == '|' || r == '<' || r == '>' || r == '&':
			return true
		}
	}
	return false
}
