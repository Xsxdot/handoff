// credential.go —— 凭据位置与密钥类环境变量守卫（B460 S10，spec r1.3 用户裁决「收紧」）。
//
// 职责：
//   - credentialLocations：凭据位置清单的**单一来源**——规则层据此判定，审批提示词
//     （orchestration 只读政策段）经 CredentialLocations 渲染同一份清单
//   - credentialExposure：判定一次请求是否触及凭据位置、或打印密钥类环境变量
//
// 接入点：Gate.Judge 出口唯一一处——任何 AutoAllow 裁决（safe-command、复合只读、范围内
// 写入、heredoc、rm 出口、写文件工具）返回前过本守卫，命中改判 Consult（交审批链，审批政策
// 要求把凭据读取升级给人）。网页抓取路由不过本守卫（spec S10：路由不变）。
//
// 边界：
//   - 只把 AutoAllow 降成 Consult，从不硬拒（Escalate 与 Consult 原样）——凭据读取是否正当
//     由审批链与人裁决，规则层只负责「不替人放行」
//   - 不做 shell 解释：宽松切词后按路径组件匹配，宁多判（over-reject 只是多问一次）
//   - 只读文件系统（解软链），不读文件内容
//
// 判定面（任一命中即触及）：
//   - 路径组件匹配：命令里任何词元（引号/反斜杠剥掉、按 `=` `:` 与重定向符再切）的路径组件里
//     **任意位置**出现清单项（`.ssh/…`、`x/.aws/…`、`/root/.ssh/…` 都算）；glob 按 shell
//     语义匹配（`*` 不匹配点开头的名字）；花括号先展开
//   - 家目录锚定：`~`、`$HOME`、家目录下的绝对路径、相对路径按 cwd 解析后落在家目录下、
//     `$XDG_DATA_HOME` 等映射变量——这些形态额外判「祖先」（`grep -r x ~`、`~/.config`
//     会递归读到清单项）与残留变量（`~/.s$X` 视为可展开成任何名字）
//   - 软链：词元解析到已存在路径时解软链再判（任务目录里指向凭据的软链）
//   - `.env` 与 `.env.*` 文件名（任何目录）；`/proc/<pid>/environ`
//   - 回家目录的 cd：裸 `cd`、`cd --`
//   - 密钥类环境变量：`env`/`printenv`（任何参数）、无参 `set`、无参或 `-p` 的 `export`、
//     `declare`/`typeset` 无参或带 p/x 旗标；`$NAME`/`${NAME}` 展开且 NAME 含 KEY、TOKEN、
//     SECRET、PASSWORD、PASSWD、CREDENTIAL、AUTH（不区分大小写）
//
// 已知残余（记台账）：`cd ../.. && cat config.yaml` 这类「cd 到家目录祖先后用不含清单组件的
// 相对路径读」只在 cd 目标本身落在家目录（锚定祖先）时被识别；变量间接（`X=~/.s; cat ${X}sh/k`）
// 不识别。两类都需要专门构造，审批链的政策段仍会兜住被转交的部分。
package permgate

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// CredentialLocation 是一项凭据位置（相对家目录）。
//
//   - Path: `~/` 形态；以 `/` 结尾表示目录（其下任何文件都算）
//   - Note: 可选的同义位置说明，随清单渲染进审批提示词
type CredentialLocation struct {
	Path string
	Note string
}

// credentialLocations 是凭据位置清单（单一来源）。增删条目同时改变规则层判定与审批提示词，
// 属于 spec 承诺面（S10），须回 spec 批准。
var credentialLocations = []CredentialLocation{
	{Path: "~/.ssh/"},
	{Path: "~/.aws/"},
	{Path: "~/.config/gh/"},
	{Path: "~/.netrc"},
	{Path: "~/.git-credentials"},
	{Path: "~/.docker/config.json"},
	{Path: "~/.kube/config"},
	{Path: "~/.codex/auth.json"},
	{Path: "~/.local/share/opencode/auth.json", Note: "或 $XDG_DATA_HOME/opencode/auth.json"},
	{Path: "~/.grok/auth.json"},
	{Path: "~/.claude/.credentials.json"},
	{Path: "~/.claude.json"},
	{Path: "~/.gemini/", Note: "agy 的 antigravity-cli/antigravity-oauth-token 在其下"},
	{Path: "~/.handoff/config.yaml"},
	{Path: "~/.handoff/env/"},
	{Path: "~/.claude/infra/"},
}

// CredentialLocations 返回凭据位置清单的副本，供审批提示词渲染（与规则层同源）。
func CredentialLocations() []CredentialLocation {
	return append([]CredentialLocation(nil), credentialLocations...)
}

// SecretEnvCommandsText 是「打印环境变量」类命令的文字说明，供审批提示词渲染（与规则层同源）。
const SecretEnvCommandsText = "env、printenv、无参 set、export -p、declare -p/-x 等打印环境变量的命令"

// credPattern 是一项匹配用的组件序列；dir 为真表示其下任何路径都算。
type credPattern struct {
	comps []string
	dir   bool
}

// credPatterns 由清单派生（去掉 `~/`），另加两项只用于匹配、不单列进提示词的形态：
// 任务目录里 grok 的凭据副本（提示词以「任务目录里指向上述文件的副本或软链」概括），
// 以及 /proc/<pid>/environ。
var credPatterns = func() []credPattern {
	var out []credPattern
	add := func(rel string) {
		dir := strings.HasSuffix(rel, "/")
		out = append(out, credPattern{comps: strings.Split(strings.Trim(rel, "/"), "/"), dir: dir})
	}
	for _, l := range credentialLocations {
		add(strings.TrimPrefix(l.Path, "~/"))
	}
	add("grokhome/auth.json")
	add("proc/*/environ")
	return out
}()

// credHomeVars 把变量映射到家目录内的相对位置（空串 = 家目录本身）。
var credHomeVars = map[string]string{
	"HOME":              "",
	"XDG_DATA_HOME":     ".local/share",
	"XDG_CONFIG_HOME":   ".config",
	"GROK_HOME":         ".grok",
	"CODEX_HOME":        ".codex",
	"CLAUDE_CONFIG_DIR": ".claude",
}

var (
	secretEnvNameRx = regexp.MustCompile(`(?i)KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|AUTH`)
	envExpandRx     = regexp.MustCompile(`\$\{?[#!]?([A-Za-z_][A-Za-z0-9_]*)`)
	leadingVarRx    = regexp.MustCompile(`^\$(\{([A-Za-z_][A-Za-z0-9_]*)\}|([A-Za-z_][A-Za-z0-9_]*))`)
)

// credentialExposure 判定请求是否触及凭据位置或密钥类环境变量。
//
// 参数：cmd 为命令（heredoc 闭集命中时传剥离正文后的形态——正文是写进文件的数据，不是读取）；
// paths 为执行器检出 / 写文件工具的落点；cwd 为相对路径的解析基准（空 = scope.Workdir）。
//
// 返回：命中时的说明（进 Verdict.Reason 与日志）与 true。
func credentialExposure(cmd string, paths []string, cwd string, scope Scope) (string, bool) {
	home, _ := os.UserHomeDir()
	if cwd == "" {
		cwd = scope.Workdir
	}
	if m := envExpandRx.FindAllStringSubmatch(cmd, -1); m != nil {
		for _, x := range m {
			if secretEnvNameRx.MatchString(x[1]) {
				return "展开密钥类环境变量 $" + x[1], true
			}
		}
	}
	for _, seg := range credSegments(cmd) {
		words := credWords(seg)
		w := credCommandWord(words)
		if len(w) == 0 {
			continue
		}
		if hit, ok := credEnvDump(w); ok {
			return hit, true
		}
		switch w[0] {
		case "echo", "printf":
			continue // 只输出参数字面，不读文件（展开类已在上面判过）
		case "cd":
			if len(w) == 1 || (len(w) == 2 && w[1] == "--") {
				return "cd 回到家目录", true
			}
		}
		for _, tok := range words {
			if hit, ok := credTokenHit(tok, home, cwd); ok {
				return hit, true
			}
		}
	}
	for _, p := range paths {
		if hit, ok := credTokenHit(p, home, cwd); ok {
			return hit, true
		}
	}
	return "", false
}

// credSegments 宽松切段：引号与反斜杠先剥掉，再按 ; & | ( ) 反引号 换行切。
// 引号内的分隔符也会被切开——多切只会多判，方向安全。
func credSegments(cmd string) []string {
	s := strings.NewReplacer(`"`, "", "'", "", `\`, "").Replace(cmd)
	return strings.FieldsFunc(s, func(r rune) bool {
		return strings.ContainsRune(";&|()`\n", r)
	})
}

// credWords 把段切成词元：空白与 < > = : 都是分隔（`--file=~/.ssh/k`、`<~/.netrc`、
// `X=~/.aws/c` 的路径部分各自成为词元）。
func credWords(seg string) []string {
	return strings.FieldsFunc(seg, func(r rune) bool {
		return r == ' ' || r == '\t' || strings.ContainsRune("<>=:", r)
	})
}

// credCommandWord 返回段的命令词起始的词元序列：跳过前置赋值（切词后 `X=1` 已拆成 X、1，
// 无法可靠区分，因此按段原样从第一个词元起判）与 command/builtin/exec/nohup/time 等前缀词。
func credCommandWord(words []string) []string {
	for len(words) > 0 {
		switch words[0] {
		case "command", "builtin", "exec", "nohup", "time", "sudo":
			words = words[1:]
			continue
		}
		break
	}
	return words
}

// credEnvDump 判定命令词是否是「打印环境变量」类。
func credEnvDump(w []string) (string, bool) {
	switch w[0] {
	case "env", "printenv":
		return "打印环境变量（" + w[0] + "）", true
	case "set":
		if len(w) == 1 {
			return "打印环境变量（无参 set）", true
		}
	case "export":
		if len(w) == 1 || w[1] == "-p" {
			return "打印环境变量（export）", true
		}
	case "declare", "typeset":
		if len(w) == 1 {
			return "打印环境变量（" + w[0] + "）", true
		}
		for _, a := range w[1:] {
			if strings.HasPrefix(a, "-") && strings.ContainsAny(a, "px") {
				return "打印环境变量（" + w[0] + " " + a + "）", true
			}
		}
	}
	return "", false
}

// credTokenHit 判定一个词元是否触及凭据位置。
func credTokenHit(tok, home, cwd string) (string, bool) {
	for _, t := range expandBraces(tok) {
		if hit, ok := credPathHit(t, home, cwd); ok {
			return hit, true
		}
	}
	return "", false
}

// credPathHit 对单个（已展开花括号的）词元做三类判定：任意位置组件匹配、家目录锚定、软链。
func credPathHit(t, home, cwd string) (string, bool) {
	if t == "" {
		return "", false
	}
	rest, anchored := credHomeRel(t, home, cwd)
	if anchored {
		if credAnchoredHit(splitComps(rest)) {
			return "触及家目录凭据位置 " + t, true
		}
	}
	// 去掉开头的变量（$X/…：值未知，按剩余部分判）与 ../ 前缀后，任意位置组件匹配。
	body := leadingVarRx.ReplaceAllString(t, "")
	comps := splitComps(body)
	if credSlidingHit(comps) || credEnvFile(comps) {
		return "触及凭据位置 " + t, true
	}
	// 软链：词元解析到已存在路径时解软链，解出的真实路径再判一次。
	abs := t
	if !filepath.IsAbs(abs) {
		if strings.ContainsAny(t, "$~") || cwd == "" {
			return "", false
		}
		abs = filepath.Join(cwd, t)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil && real != filepath.Clean(abs) {
		rcomps := splitComps(real)
		if credSlidingHit(rcomps) || credEnvFile(rcomps) {
			return "软链指向凭据位置 " + t + " → " + real, true
		}
		if home != "" {
			if r, ok := relUnder(real, home); ok && credAnchoredHit(splitComps(r)) {
				return "软链指向家目录凭据位置 " + t + " → " + real, true
			}
		}
	}
	return "", false
}

// credHomeRel 判断词元是否锚定在家目录，是则返回家目录内的相对部分。
func credHomeRel(t, home, cwd string) (string, bool) {
	switch {
	case t == "~":
		return "", true
	case strings.HasPrefix(t, "~"):
		// ~/x 与 ~user/x 一律按家目录处理（别的用户的家目录同样是凭据位置）
		if i := strings.IndexByte(t, '/'); i >= 0 {
			return t[i+1:], true
		}
		return "", true
	}
	if m := leadingVarRx.FindStringSubmatch(t); m != nil {
		name := m[2] + m[3]
		if base, ok := credHomeVars[name]; ok {
			return filepath.Join(base, strings.TrimPrefix(t[len(m[0]):], "/")), true
		}
		return "", false
	}
	if home == "" {
		return "", false
	}
	abs := t
	if !filepath.IsAbs(abs) {
		if cwd == "" {
			return "", false
		}
		abs = filepath.Join(cwd, t)
	}
	return relUnder(filepath.Clean(abs), home)
}

// relUnder 返回 p 相对 base 的部分（p 在 base 子树内或等于 base 时）。
func relUnder(p, base string) (string, bool) {
	rel, err := filepath.Rel(filepath.Clean(base), p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	if rel == "." {
		rel = ""
	}
	return rel, true
}

// splitComps 把路径切成组件，丢掉空组件、`.` 与 `..`（`..` 回退到哪不可知，丢掉后
// 任意位置匹配仍能认出 `../../.aws/credentials`）。
func splitComps(p string) []string {
	var out []string
	for _, c := range strings.Split(p, "/") {
		if c == "" || c == "." || c == ".." {
			continue
		}
		out = append(out, c)
	}
	return out
}

// credSlidingHit：组件序列在任意偏移处与某清单项吻合（目录项允许更深，文件项要求完整吻合）。
func credSlidingHit(comps []string) bool {
	for i := range comps {
		for _, p := range credPatterns {
			if credPrefixMatch(comps[i:], p, false) {
				return true
			}
		}
	}
	return false
}

// credAnchoredHit：家目录锚定的相对组件序列命中清单项，或是某清单项的祖先（含家目录本身）。
// 锚定形态里残留 `$` 的组件视为可展开成任何名字。
func credAnchoredHit(comps []string) bool {
	if len(comps) == 0 {
		return true // 家目录本身：递归读会读到全部凭据
	}
	for _, p := range credPatterns {
		if credPrefixMatch(comps, p, true) {
			return true
		}
		if len(comps) < len(p.comps) {
			ancestor := true
			for k, c := range comps {
				if !credCompMatch(c, p.comps[k], true) {
					ancestor = false
					break
				}
			}
			if ancestor {
				return true
			}
		}
	}
	return false
}

// credPrefixMatch：comps 以清单项开头（目录项）或恰为清单项（文件项）。
func credPrefixMatch(comps []string, p credPattern, anchored bool) bool {
	if len(comps) < len(p.comps) || (!p.dir && len(comps) != len(p.comps)) {
		return false
	}
	for k, pc := range p.comps {
		if !credCompMatch(comps[k], pc, anchored) {
			return false
		}
	}
	return true
}

// credCompMatch 比较一个词元组件（可能含 glob / 变量）与清单组件（可能是 `*`）。
//
// glob 按 shell 语义：点开头的名字只被点开头的模式匹配（`*` 不匹配 `.ssh`）。
func credCompMatch(tc, pc string, anchored bool) bool {
	if pc == "*" || tc == pc {
		return true
	}
	if anchored && strings.Contains(tc, "$") {
		return true
	}
	if strings.ContainsAny(tc, "*?[") {
		if strings.HasPrefix(pc, ".") && !strings.HasPrefix(tc, ".") {
			return false
		}
		ok, _ := filepath.Match(tc, pc)
		return ok
	}
	return false
}

// credEnvFile：末组件是 `.env` 或 `.env.*`（glob 形态按能否匹配这两种名字判）。
func credEnvFile(comps []string) bool {
	if len(comps) == 0 {
		return false
	}
	b := comps[len(comps)-1]
	if b == ".env" || strings.HasPrefix(b, ".env.") {
		return true
	}
	return strings.ContainsAny(b, "*?[") && (credCompMatch(b, ".env", false) || credCompMatch(b, ".env.local", false))
}

// expandBraces 做 bash 风格花括号展开（`a{b,c}d` → abd、acd，可嵌套）。展开结果超过上限时
// 返回原词元加一个「任意名字」占位，交调用方按通配处理——宁多判。
func expandBraces(tok string) []string {
	const limit = 64
	out := []string{tok}
	for round := 0; round < 8; round++ {
		changed := false
		var next []string
		for _, t := range out {
			open, close, parts := braceSplit(t)
			if parts == nil {
				next = append(next, t)
				continue
			}
			changed = true
			for _, p := range parts {
				next = append(next, t[:open]+p+t[close+1:])
			}
		}
		out = next
		if len(out) > limit {
			return []string{strings.NewReplacer("{", "*", "}", "*", ",", "*").Replace(tok)}
		}
		if !changed {
			break
		}
	}
	return out
}

// braceSplit 找第一个含顶层逗号的 {…}，返回其位置与各分支；没有则 parts=nil。
func braceSplit(t string) (open, close int, parts []string) {
	for i := 0; i < len(t); i++ {
		if t[i] != '{' {
			continue
		}
		depth, last := 0, i+1
		var ps []string
		for j := i; j < len(t); j++ {
			switch t[j] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					if ps == nil {
						goto nextOpen // 无逗号的 {x} 不展开
					}
					return i, j, append(ps, t[last:j])
				}
			case ',':
				if depth == 1 {
					ps = append(ps, t[last:j])
					last = j + 1
				}
			}
		}
	nextOpen:
	}
	return 0, 0, nil
}
