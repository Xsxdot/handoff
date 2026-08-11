// Package toolchain 探测本机装了哪些 executor、各自处于什么状态。
//
// 职责：
//   - 对 opencode / claude / grok / codex 四家，各查「可执行文件在不在 PATH」
//     与「凭证文件在不在」，归一成一个三态（claude 两态）
//
// 边界：
//   - **只探测，不决策**：不排序偏好、不写配置、不阻止任何选择。那是 cmd/init.go 的事
//   - **绝不发起真实模型调用**。README 里「claude -p "hi" 能出结果即视为就绪」是给人
//     看的验证方法，不是这里能用的判据——那是一次真实付费调用，几秒到几十秒且要联网
//   - 不打日志：本包是一组纯取值函数，无 I/O 副作用（只读文件是否存在），
//     在这里打日志只会给 init 的输出制造噪音；探测结果由调用方成表打印
package toolchain

import (
	"os"
	"os/exec"
	"path/filepath"
)

// 三个探测缝，生产实现即标准库；测试替换它们，从而不依赖跑测机器的真实环境。
var (
	lookPath    = exec.LookPath
	statFile    = func(p string) error { _, err := os.Stat(p); return err }
	userHomeDir = os.UserHomeDir
)

// State 是一家 executor 的可用状态。
type State int

const (
	// StateMissing：可执行文件不在 PATH 里。
	StateMissing State = iota
	// StateNoCreds：装了，但找不到凭证文件。
	StateNoCreds
	// StateReady：装了且凭证文件在。
	StateReady
	// StateAuthUnknown：装了，但本机没有可靠的轻量凭证判据——只有 claude 会是这个状态。
	//
	// 为什么单独一态而不是并进 StateNoCreds：两者要让用户做的事完全相反。
	// NoCreds 是「去登录」，AuthUnknown 是「大概率能用，自己心里有数」。
	// 合并等于把「不知道」说成「没登录」，那是编造。
	StateAuthUnknown
)

// String 返回给人看的中文短语（init 的表格直接打印它）。
func (s State) String() string {
	switch s {
	case StateMissing:
		return "没装"
	case StateNoCreds:
		return "已安装，未登录"
	case StateReady:
		return "就绪"
	case StateAuthUnknown:
		return "已安装，登录态未知"
	}
	return "未知"
}

// Result 是一家 executor 的探测结果。
type Result struct {
	// Name 是 executor 名，与 dispatch --executor 用的名字一致。
	Name string
	// Path 是可执行文件路径；StateMissing 时为空。
	Path string
	// State 是探测出的状态。
	State State
}

// Ready 表示「可以放心把它设成缺省执行者」。
//
// 注意：StateAuthUnknown 返回 **false**。它的语义是「不知道」，
// 把不知道当成就绪，就是替用户做了一个没有依据的判断。
func (r Result) Ready() bool { return r.State == StateReady }

// credRelPath 是各家凭证文件相对 HOME 的路径。
//
// 这三条在 devbox 上逐一查实过（2026-08-11），不是猜的。claude 不在表里——
// 它的 OAuth 凭据存在 macOS Keychain 里，没有可靠的文件判据（~/.claude.json
// 存在但那是配置不是凭证，拿它当登录判据会把没登录的机器报成就绪）。
var credRelPath = map[string]string{
	"opencode": ".local/share/opencode/auth.json",
	"grok":     ".grok/auth.json",
	"codex":    ".codex/auth.json",
}

// order 固定探测与返回顺序，让 init 的表格每次长得一样。
var order = []string{"opencode", "claude", "grok", "codex"}

// Detect 探测四家 executor 的状态。
//
// 返回：
//   - 固定四项，顺序恒为 opencode / claude / grok / codex
//
// 注意：
//   - 取不到 HOME 时，装了的执行者一律报 StateNoCreds 而不是 StateMissing——
//     「凭证查不到」和「没装」是两件事，混为一谈会让用户去重装一个已经装好的东西
func Detect() []Result {
	home, homeErr := userHomeDir()
	out := make([]Result, 0, len(order))
	for _, name := range order {
		r := Result{Name: name}
		p, err := lookPath(name)
		if err != nil {
			r.State = StateMissing
			out = append(out, r)
			continue
		}
		r.Path = p
		if name == "claude" {
			// claude 没有可靠的轻量判据，如实报「不知道」
			r.State = StateAuthUnknown
			out = append(out, r)
			continue
		}
		rel, ok := credRelPath[name]
		if !ok || homeErr != nil {
			// 没有凭证判据（工具不在表里）或连 HOME 都取不到——都属于「查不了」，
			// 不是「没登录」。如实报未知，别猜。与 claude 那条同一个道理
			r.State = StateAuthUnknown
			out = append(out, r)
			continue
		}
		if statFile(filepath.Join(home, rel)) == nil {
			r.State = StateReady
		} else {
			r.State = StateNoCreds
		}
		out = append(out, r)
	}
	return out
}

// FirstReady 返回第一个就绪的 executor 名；一个都没有时返回空串。
//
// 供 init 挑 executor.default 的默认值用。**不把 StateAuthUnknown 算进来**，
// 理由同 Result.Ready。
func FirstReady(rs []Result) string {
	for _, r := range rs {
		if r.Ready() {
			return r.Name
		}
	}
	return ""
}
