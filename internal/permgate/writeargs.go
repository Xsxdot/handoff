// writeargs.go —— 「落点在参数位」的写命令目的地提取。
//
// 职责：
//   - 从命令原文里摘出 tee/cp/mv/ln/install/dd 会写到哪个路径，供 judgeBash 做范围判定
//   - 展开 ~ 前缀（与 redirect.go 同一套语义）
//
// 边界：
//   - 不做完整 shell 解析：不展开变量、不解析子 shell、不跟 cd
//   - 不判断路径是否越界：范围判定在 path.go
//   - 不管重定向：那是 redirect.go 的事，两者由 judgeBash 分别调用后合并
//
// 为什么需要它：重定向落点由 RedirectTargets 覆盖，但同样是写仓库外，
// `echo x | tee /tmp/y`、`cp secret /outside`、`dd of=/tmp/x` 的落点在**参数位**，
// 摘不出来就只能落 Consult 交廉价模型（2026-08-18 真机实测：claude 上这两条都是
// `交审批者 黑名单未命中`）。
//
// 为什么只看每一段的**首个词元**：这样 `git commit -m "cp a /etc/x"` 天然不误伤——
// 段首是 git，引号里的 cp 不是命令。误伤一次的代价是平白叫醒协调者，比漏判更常发生。
package permgate

import "strings"

// argTargetKind 描述某个写命令的落点在哪几个参数位。
type argTargetKind int

const (
	// targetAllArgs：所有非标志参数都是落点（tee 会写到它列出的每一个文件）
	targetAllArgs argTargetKind = iota
	// targetLastArg：最后一个非标志参数是目的地，其余是源（cp/mv/ln/install）
	targetLastArg
	// targetOfPrefix：落点写在 of=PATH 里（dd）
	targetOfPrefix
)

// writeCommands 是「落点在参数位」的写命令表。
//
// 只收**确定会写文件**的命令。刻意不收 `sed`：它的 -i 形态落点是源文件本身，
// 语义与本表三种都不同，单独处理才不会把 `sed s/x/y/ file` 这类只读用法误判成写。
// 每次修改本表必须同步 writeargs_test 的逐条断言。
var writeCommands = map[string]argTargetKind{
	"tee":     targetAllArgs,
	"cp":      targetLastArg,
	"mv":      targetLastArg,
	"ln":      targetLastArg,
	"install": targetLastArg,
	"dd":      targetOfPrefix,
}

// WriteArgTargets 从命令串里摘出全部「参数位写落点」。
//
// 参数：cmd 为命令原文（**不要**先过 StripQuoted：落点常被引号包住，剥完就没了）
// 返回：落点路径切片，按出现顺序；没有则返回 nil
//
// 注意：
//   - 按 `|`、`&`、`;` 分段，逐段判首个词元是不是写命令——只有是，才摘该段的落点
//   - `--` 之后的词元一律不当标志看
//   - `~` 与 `~/xxx` 展开为当前用户 home（why 见 expandTilde）
//   - 丢弃落点（/dev/null 等）**不在这里过滤**：由 judgeBash 统一用 IsDiscardTarget 跳过，
//     两个落点来源共用同一处豁免，不会两边写两套
func WriteArgTargets(cmd string) []string {
	var out []string
	for _, seg := range splitWriteSegments(cmd) {
		toks := tokenize(seg)
		if len(toks) == 0 {
			continue
		}
		name := baseName(toks[0])
		kind, ok := writeCommands[name]
		if !ok {
			continue
		}
		for _, t := range targetsOf(kind, toks[1:]) {
			out = append(out, expandTilde(t))
		}
	}
	return out
}

// targetsOf 按落点种类从参数列表里挑出目的地。
//
// 参数：kind 为落点种类，args 为命令名之后的全部词元
// 返回：落点切片；判不出（如 cp 只给了一个参数）时返回 nil
func targetsOf(kind argTargetKind, args []string) []string {
	if kind == targetOfPrefix {
		var out []string
		for _, a := range args {
			if v, ok := strings.CutPrefix(a, "of="); ok && v != "" {
				out = append(out, v)
			}
		}
		return out
	}

	var plain []string // 非标志参数
	var byFlag []string
	endOfFlags := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !endOfFlags && a == "--" {
			endOfFlags = true
			continue
		}
		if !endOfFlags && strings.HasPrefix(a, "-") && a != "-" {
			// -t DIR / --target-directory=DIR：目的地在标志上，其余参数全是源
			if v, ok := strings.CutPrefix(a, "--target-directory="); ok && v != "" {
				byFlag = append(byFlag, v)
				continue
			}
			if a == "-t" || a == "--target-directory" {
				if i+1 < len(args) {
					byFlag = append(byFlag, args[i+1])
					i++
				}
				continue
			}
			continue
		}
		plain = append(plain, a)
	}
	if len(byFlag) > 0 {
		return byFlag
	}
	switch kind {
	case targetAllArgs:
		return plain
	case targetLastArg:
		// 只有一个参数时判不出源与目的地（cp a.txt 本身就不是完整命令），不猜
		if len(plain) < 2 {
			return nil
		}
		return plain[len(plain)-1:]
	}
	return nil
}

// splitWriteSegments 按 shell 的命令分隔符把命令串切成段，引号内的分隔符不算。
//
// 参数：cmd 为命令原文
// 返回：各段原文（可能含前导空白），空段丢弃
//
// 为什么不复用 selfcmd.go 里的 splitSegments：那个用 strings.FieldsFunc，
// **不认引号**——`git commit -m "a | tee /etc/x"` 会被它切出一段 `tee /etc/x`，
// 在本文件里就成了一次凭空的越界升级。那边不认引号是刻意的（judgeCommand 对
// 原文与 StripQuoted 结果各跑一遍来覆盖包装器），改它会动到自指令判据，不碰。
func splitWriteSegments(cmd string) []string {
	var out []string
	var b strings.Builder
	var quote rune
	flush := func() {
		if s := strings.TrimSpace(b.String()); s != "" {
			out = append(out, s)
		}
		b.Reset()
	}
	for _, r := range cmd {
		switch {
		case quote == 0 && (r == '\'' || r == '"'):
			quote = r
			b.WriteRune(r)
		case quote != 0 && r == quote:
			quote = 0
			b.WriteRune(r)
		case quote == 0 && (r == '|' || r == '&' || r == ';' || r == '\n'):
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return out
}

// tokenize 把一段命令切成词元，引号内的空白不分词、包裹引号被去掉。
//
// 参数：seg 为单段命令原文
// 返回：词元切片
//
// 复用 readTarget 而不是另写一个扫描器：落点词元与命令词元的边界规则是同一套，
// 两份实现迟早会漂移。
func tokenize(seg string) []string {
	var out []string
	rs := []rune(seg)
	for i := 0; i < len(rs); {
		switch rs[i] {
		case ' ', '\t', '\n':
			i++
			continue
		case '>', '<', '(', ')':
			// 重定向与子 shell 不归本文件管，遇到就停：后面的词元不再是本命令的参数
			return out
		}
		tok, next := readTarget(rs, i)
		if next == i { // 防御：readTarget 未推进则强制前进，避免死循环
			i++
			continue
		}
		i = next
		if tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

// baseName 取命令名的最后一段，让 /usr/bin/tee 与 tee 判成同一个命令。
func baseName(s string) string {
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		return s[i+1:]
	}
	return s
}
