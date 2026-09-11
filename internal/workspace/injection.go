// injection.go —— 工作区包对宿主能力的注入缝（B233.15）。
//
// 职责：让 workspace 不 import proxycfg / prochost / agentd，却保留今天的出网代理、
// fork 失败文案与 run 准入判读。绑定只允许发生在组装点（cmd/agentd.go）。
//
// 边界：默认零值 = 今天的「未配置」行为（无代理参数、无 fork 文案、不判余量）。
// 三个 setter 非并发安全：只在启动期注入一次（与原 SetGitProxy 同款约定）。
package workspace

// GitNetConfig 是出网 git（clone/fetch）的代理注入值。
//
// Argv 由 proxycfg.GitArgs 产出的参数序列（空 = 不带代理参数）；
// Redact 是日志可读的脱敏代理串（仅日志用）。
type GitNetConfig struct {
	Argv   []string
	Redact string
}

// gitNet 是当前出网注入值；零值 = 不带代理。
var gitNet GitNetConfig

// forkFailureNote 把进程创建失败翻译成归因文案的注入缝。
// 默认返回空串：与今天「prochost 未接」时的可观察行为一致。
var forkFailureNote = func(error) (string, bool) { return "", false }

// procHeadroom 是 run 开工前的余量准入注入缝。nil = 不判余量（放行）。
var procHeadroom func(op string) error

// ConfigureGitNet 绑定出网代理参数与日志脱敏串。只在组装点调用一次。
func ConfigureGitNet(c GitNetConfig) { gitNet = c }

// SetForkFailureNote 绑定 fork 失败文案函数（prochost.ExplainForkFailure）。
// fn 为 nil 时恢复默认「无文案」。只在组装点调用一次。
func SetForkFailureNote(fn func(error) (string, bool)) {
	if fn == nil {
		forkFailureNote = func(error) (string, bool) { return "", false }
		return
	}
	forkFailureNote = fn
}

// SetProcHeadroom 绑定 run 的进程余量判读函数（agentd.CheckProcHeadroom）。
// fn 为 nil 时恢复默认「不判余量」。只在组装点调用一次。
func SetProcHeadroom(fn func(op string) error) { procHeadroom = fn }
