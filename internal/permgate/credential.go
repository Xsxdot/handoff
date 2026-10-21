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
// 判定面（任一命中即触及；组件比较一律大小写不敏感，路径按 Clean 语义化简 `..`）：
//   - 路径组件匹配：命令里任何词元（引号/反斜杠剥掉、按 `=` `:` 与重定向符再切）的路径组件里
//     **任意位置**出现清单项（`.ssh/…`、`x/.aws/…`、`/root/.ssh/…` 都算）；glob 按 shell
//     语义匹配（`*` 不匹配点开头的名字）；花括号先展开
//   - 解析成绝对路径后再判：`~`、`~user`、`$HOME` 与 `$XDG_DATA_HOME` 等映射变量、`$TMPDIR` /
//     `$HANDOFF_ARTIFACTS_DIR`（任务注入值）、绝对路径、相对路径（按 cwd 与命令内 cd 跟踪出的
//     每个基准）——
//     · 是任一家目录（本用户、/root、/Users/<x>、/home/<x>）本身或其祖先（`/`、`/Users`）：
//     递归读会读到其下全部凭据；
//     · 落在家目录之下：清单项本身或其祖先（`~/.config` 会递归读到 `~/.config/gh/`），
//     残留变量（`~/.s$X`）视为可展开成任何名字
//   - 软链：解析出的路径再做真实路径解析（已存在最长前缀解软链、悬空软链跟随），结果再判
//   - `.env` 与 `.env.*` 文件名（任何目录）；`/proc/<pid>/environ`
//   - 回家目录的 cd：裸 `cd`、`cd --`
//   - 密钥类环境变量：`env`/`printenv`（任何参数）、无参 `set`、无参或 `-p` 的 `export`、
//     `declare`/`typeset` 无参或带 p/x 旗标；`$NAME`/`${NAME}` 展开且 secretEnvName(NAME)
//
// 取舍（B460 修复轮 2）：「家目录及其祖先」不区分是否递归——`ls /`、`ls /Users` 也 Consult。
// 递归与否要逐命令解析旗标（grep -r/-R/--recursive、ls -R、rg 默认递归、tree…），漏一种就是
// 绕过；非递归列根目录在任务里罕见，多问一次的代价可接受。
//
// 已知残余（记台账）：变量整值即凭据路径（`cat $P`，P 由环境给出）不识别；cd 目标含未知变量
// 时其后相对路径只按其余基准判。审批链的政策段仍会兜住被转交的部分。
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
	{Path: "~/.kube/"},
	{Path: "~/.codex/auth.json"},
	{Path: "~/.local/share/opencode/auth.json", Note: "或 $XDG_DATA_HOME/opencode/auth.json"},
	{Path: "~/.grok/auth.json"},
	{Path: "~/.claude/.credentials.json"},
	{Path: "~/.claude.json"},
	{Path: "~/.gemini/", Note: "agy 的 antigravity-cli/antigravity-oauth-token 在其下"},
	{Path: "~/.handoff/config.yaml"},
	{Path: "~/.handoff/env/"},
	{Path: "~/.claude/infra/"},
	// B460 修复轮 2 F5（spec S10「等」，主会话补）
	{Path: "~/.npmrc"},
	{Path: "~/.pypirc"},
	{Path: "~/.config/gcloud/"},
	{Path: "~/.cargo/credentials"},
	{Path: "~/.cargo/credentials.toml"},
	{Path: "~/.gnupg/"},
	{Path: "~/.bash_history"},
	{Path: "~/.zsh_history"},
	{Path: "~/.history"},
	{Path: "~/.local/share/fish/fish_history"},
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
	secretEnvNameRx = regexp.MustCompile(`(?i)KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|AUTH|DATABASE_URL`)
	envExpandRx     = regexp.MustCompile(`\$\{?[#!]?([A-Za-z_][A-Za-z0-9_]*)`)
	leadingVarRx    = regexp.MustCompile(`^\$(\{([A-Za-z_][A-Za-z0-9_]*)\}|([A-Za-z_][A-Za-z0-9_]*))`)
)

// secretEnvName 判定变量名是否是密钥类：含 KEY、TOKEN、SECRET、PASSWORD、PASSWD、CREDENTIAL、
// AUTH、DATABASE_URL（子串，不区分大小写），或按 `_` 切开后有一段恰为 PAT、DSN（B460 修复轮 2
// F5：PAT/DSN 按词段认，`$PATH`、`$PATTERN` 不连坐）。
func secretEnvName(name string) bool {
	if secretEnvNameRx.MatchString(name) {
		return true
	}
	for _, part := range strings.Split(strings.ToUpper(name), "_") {
		if part == "PAT" || part == "DSN" {
			return true
		}
	}
	return false
}

// credEnv 是一次判定的环境：家目录与任务范围（白名单变量的展开值）。
type credEnv struct {
	home  string
	scope Scope
}

// credentialExposure 判定请求是否触及凭据位置或密钥类环境变量。
//
// 参数：cmd 为命令（heredoc 闭集命中时传剥离正文后的形态——正文是写进文件的数据，不是读取）；
// paths 为执行器检出 / 写文件工具的落点；cwd 为相对路径的起始基准（空 = scope.Workdir）。
//
// 相对路径按**命令内跟踪的 cd 基准**解析（B460 修复轮 2 F2）：起点是 cwd，每个 cd 段把可解析的
// 目标加入基准集合（不删旧基准——cd 可能失败，两处都按可能的 cwd 判，宁多判），目标不可解析
// 时不加。相对词元对基准集合里每个基准都判一次。
//
// 返回：命中时的说明（进 Verdict.Reason 与日志）与 true。
func credentialExposure(cmd string, paths []string, cwd string, scope Scope) (string, bool) {
	home, _ := os.UserHomeDir()
	env := credEnv{home: home, scope: scope}
	if cwd == "" {
		cwd = scope.Workdir
	}
	var bases []string
	if cwd != "" {
		bases = append(bases, filepath.Clean(cwd))
	}
	if m := envExpandRx.FindAllStringSubmatch(cmd, -1); m != nil {
		for _, x := range m {
			if secretEnvName(x[1]) {
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
			if hit, ok := credTokenHit(tok, env, bases); ok {
				return hit, true
			}
		}
		if w[0] == "cd" && len(w) == 2 && !strings.HasPrefix(w[1], "-") && !strings.HasPrefix(w[1], "+") {
			bases = append(bases, credResolve(w[1], env, bases)...)
		}
	}
	for _, p := range paths {
		if hit, ok := credTokenHit(p, env, bases); ok {
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

// credCommandWord 返回段的命令词起始的词元序列：跳过 command/builtin/exec/nohup/time/sudo
// 等前缀词。
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
func credTokenHit(tok string, env credEnv, bases []string) (string, bool) {
	for _, t := range expandBraces(tok) {
		if hit, ok := credPathHit(t, env, bases); ok {
			return hit, true
		}
	}
	return "", false
}

// credPathHit 对单个（已展开花括号的）词元做判定：
//  1. 词元自身的组件（去掉开头的未知变量、按 Clean 化简）任意位置匹配清单项或 .env；
//  2. 解析成绝对路径（~、$HOME 等映射变量、$TMPDIR / $HANDOFF_ARTIFACTS_DIR 按任务注入值、
//     绝对路径原样、相对路径按每个 cd 基准）后按 credAbsHit 判；
//  3. 对解析结果做真实路径解析（resolveTargetPath：已存在最长前缀解软链 + 悬空软链跟随），
//     与原路径不同则再按 credAbsHit 判（任务目录里指向凭据的软链）。
func credPathHit(t string, env credEnv, bases []string) (string, bool) {
	if t == "" {
		return "", false
	}
	comps := credComps(leadingVarRx.ReplaceAllString(t, ""), true)
	if credSlidingHit(comps) || credEnvFile(comps) {
		return "触及凭据位置 " + t, true
	}
	for _, abs := range credResolve(t, env, bases) {
		if hit, ok := credAbsHit(abs, env); ok {
			return hit + "（" + t + "）", true
		}
		if real, err := resolveTargetPath(abs); err == nil && real != abs {
			if hit, ok := credAbsHit(real, env); ok {
				return "软链 " + t + " → " + real + "：" + hit, true
			}
		}
	}
	return "", false
}

// credResolve 把词元解析成绝对路径（已 Clean）；解析不了（未知变量开头、无基准的相对路径）返回空。
func credResolve(t string, env credEnv, bases []string) []string {
	switch {
	case t == "~" || strings.HasPrefix(t, "~/"):
		if env.home == "" {
			return nil
		}
		return []string{filepath.Join(env.home, strings.TrimPrefix(strings.TrimPrefix(t, "~"), "/"))}
	case strings.HasPrefix(t, "~"):
		// ~user/x：别的用户的家目录，按与本用户家目录同级解释
		if env.home == "" {
			return nil
		}
		return []string{filepath.Join(filepath.Dir(env.home), t[1:])}
	}
	if m := leadingVarRx.FindStringSubmatch(t); m != nil {
		name := m[2] + m[3]
		rest := strings.TrimPrefix(t[len(m[0]):], "/")
		if base, ok := credHomeVars[name]; ok && env.home != "" {
			return []string{filepath.Join(env.home, base, rest)}
		}
		for _, v := range scopeVars {
			if v.name == name {
				if val := v.val(env.scope); val != "" {
					return []string{filepath.Join(val, rest)}
				}
			}
		}
		return nil
	}
	if filepath.IsAbs(t) {
		return []string{filepath.Clean(t)}
	}
	var out []string
	for _, b := range bases {
		if b != "" {
			out = append(out, filepath.Join(b, t))
		}
	}
	return out
}

// credAbsHit 判定一个绝对路径（已 Clean）是否触及凭据位置：
//   - 任意位置组件匹配清单项，或 .env 文件；
//   - 是某个家目录的祖先或家目录本身（`/`、`/Users`、`/home`、`/root`、`/Users/<x>`、
//     `/home/<x>`、本用户家目录及其祖先）——递归读会读到其下全部凭据（B460 修复轮 2 F2）；
//   - 落在某个家目录之下：按家目录锚定判（清单项本身或其祖先、残留变量视为任意名字）。
//
// 一律大小写不敏感（F3：darwin 默认文件系统不区分大小写）。
func credAbsHit(abs string, env credEnv) (string, bool) {
	comps := credComps(abs, false)
	if credSlidingHit(comps) || credEnvFile(comps) {
		return "触及凭据位置 " + abs, true
	}
	for _, h := range credHomes(comps, env) {
		hc := credComps(h, false)
		if len(comps) <= len(hc) && credCompsPrefix(hc, comps) {
			return "触及家目录或其祖先 " + abs, true
		}
		if credCompsPrefix(comps, hc) && credAnchoredHit(comps[len(hc):]) {
			return "触及家目录凭据位置 " + abs, true
		}
	}
	return "", false
}

// credHomes 返回判定用的家目录集合：本用户家目录、/root，以及路径本身若形如
// /users/<x>/… 或 /home/<x>/…，则 /users/<x> 或 /home/<x>（别的用户的家目录）。
// 根下的 /users、/home 本身由「家目录的祖先」覆盖（/users/<x> 的祖先）。
func credHomes(comps []string, env credEnv) []string {
	homes := []string{"/root", "/users/*", "/home/*"}
	if env.home != "" {
		homes = append(homes, env.home)
	}
	var out []string
	for _, h := range homes {
		if strings.HasSuffix(h, "/*") {
			parent := strings.TrimSuffix(h, "/*")
			if len(comps) >= 2 && "/"+comps[0] == parent {
				out = append(out, parent+"/"+comps[1])
			} else {
				out = append(out, parent+"/*") // 祖先判定用：/users 是 /users/* 的祖先
			}
			continue
		}
		out = append(out, h)
	}
	return out
}

// credCompsPrefix：pre 是 comps 的前缀（逐组件，大小写不敏感；pre 里的 `*` 匹配任意组件）。
func credCompsPrefix(comps, pre []string) bool {
	if len(pre) > len(comps) {
		return false
	}
	for i, p := range pre {
		if p != "*" && p != comps[i] {
			return false
		}
	}
	return true
}

// credComps 把路径切成小写组件。先按 path.Clean 语义化简（`a/../b` → `b`，F5：修前直接丢弃
// `..`，`/proc/self/../self/environ` 漏网）；dropLeadingUp 为真时再去掉化简后仍在开头的 `..`
// （相对词元回退到哪不可知，去掉后任意位置匹配仍能认出 `../../.aws/credentials`）。
func credComps(p string, dropLeadingUp bool) []string {
	if p == "" {
		return nil
	}
	var out []string
	for _, c := range strings.Split(filepath.Clean(p), "/") {
		if c == "" || c == "." {
			continue
		}
		if c == ".." && dropLeadingUp && len(out) == 0 {
			continue
		}
		out = append(out, strings.ToLower(c))
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

// credAnchoredHit：家目录内的相对组件序列命中清单项，或是某清单项的祖先（含家目录本身）。
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

// credCompMatch 比较一个词元组件（已小写，可能含 glob / 变量）与清单组件（小写，可能是 `*`）。
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

// credEnvFile：末组件（已小写）是 `.env` 或 `.env.*`（glob 形态按能否匹配这两种名字判）。
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
