// cmdword.go —— 段命令词的唯一归一化（B460 修复轮 2 F1）。
//
// 职责：从一段命令里取出命令词，做 shell 的引号去除（单引号、双引号、反斜杠），并报告
// 原文是否「朴素」（去引号前后一致、只含 [A-Za-z0-9._/+-]）。
//
// 为什么必须只有一处：修复轮 1 之前，「这一段是不是 cd」用保留单引号的 rmTokens 判，
// 只读匹配用剥引号的 splitSafeCommand 判——`'cd' /etc` 在前者眼里不是 cd（基准不更新），
// 在后者眼里是可静默的 cd，`cd $TMPDIR && 'cd' /etc && echo x > passwd` 于是被当成在
// $TMPDIR 里写而放行，实际写 /etc/passwd。两个判据各自分词迟早再分歧，所以：
//   - cd 判定（cdChainTrusted、cdResolvedTargets、cdWalkBase、isCDSegment、heredoc/rm/
//     范围内写入出口）一律用 commandWord 的归一结果认 cd；
//   - 只读匹配（matchSilentFields）要求命令词朴素且与其词元一致——非朴素命令词永不静默；
//   - 依赖 cd 基准的证明出口（范围内写入、heredoc、rm）任一段命令词非朴素即整条不可证明。
package permgate

// commandWord 返回段的命令词（去引号后）、原文是否朴素、以及是否解析成功。
//
// 命令词 = 段首（跳过前导空白）到第一个引号外空白为止的 shell 词。去引号规则：
// 单引号内原样；双引号内 `\` 只转义 `$` “ ` “ `"` `\`；引号外 `\x` → x。
// ok=false：空段或命令词内引号未闭合。
func commandWord(seg string) (word string, plain bool, ok bool) {
	i := 0
	for i < len(seg) && (seg[i] == ' ' || seg[i] == '\t' || seg[i] == '\r' || seg[i] == '\n') {
		i++
	}
	start := i
	var out []byte
	for i < len(seg) {
		c := seg[i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			break
		}
		switch c {
		case '\'':
			j := i + 1
			for j < len(seg) && seg[j] != '\'' {
				j++
			}
			if j >= len(seg) {
				return "", false, false
			}
			out = append(out, seg[i+1:j]...)
			i = j + 1
		case '"':
			j := i + 1
			for j < len(seg) && seg[j] != '"' {
				if seg[j] == '\\' && j+1 < len(seg) && stringsContainsByte("$`\"\\", seg[j+1]) {
					out = append(out, seg[j+1])
					j += 2
					continue
				}
				out = append(out, seg[j])
				j++
			}
			if j >= len(seg) {
				return "", false, false
			}
			i = j + 1
		case '\\':
			if i+1 < len(seg) {
				out = append(out, seg[i+1])
				i += 2
			} else {
				i++
			}
		default:
			out = append(out, c)
			i++
		}
	}
	raw := seg[start:i]
	if raw == "" {
		return "", false, false
	}
	word = string(out)
	return word, raw == word && plainWord(word), true
}

// plainWord：非空且只含 [A-Za-z0-9._/+-]——没有引号、转义、展开、通配、赋值、重定向字符。
func plainWord(w string) bool {
	if w == "" {
		return false
	}
	for i := 0; i < len(w); i++ {
		c := w[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '_', c == '/', c == '+', c == '-':
		default:
			return false
		}
	}
	return true
}

func stringsContainsByte(s string, b byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return true
		}
	}
	return false
}

// isCDWord 判定段的命令词（归一后）是否为 cd；plain 报告其原文是否朴素。
func isCDWord(seg string) (isCD, plain bool) {
	w, p, ok := commandWord(seg)
	return ok && w == "cd", p
}

// commandWordsPlain 判定各段命令词是否都朴素（证明出口的纵深防御：任一段命令词带引号、
// 转义、`$`、反引号等，命令实际执行什么就不可静态证明）。
func commandWordsPlain(segs []string) bool {
	for _, s := range segs {
		if _, plain, ok := commandWord(s); !ok || !plain {
			return false
		}
	}
	return true
}
