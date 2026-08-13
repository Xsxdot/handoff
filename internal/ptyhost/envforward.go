// 会话级环境变量转发：把 SSH_AUTH_SOCK 这类「由会话注入、不来自 dotfile」的
// 变量解析出来，注入单个终端会话的环境。
//
// 职责：
//   - 按三级顺序解析每个变量：继承 → 平台查询 → 探不到
//   - 逐个变量记录三态结论，让「终端里 git push 失败」变成一行可搜的日志
//
// 边界：
//   - 只产出**这个会话的** cmd.Env，**绝不写回 agentd 自身环境**。这与
//     internal/pathenv 相反：PATH 是进程级恒定事实，socket 路径是会话级易变
//     事实，写回会让后续所有 fork 拿到一个可能已经失效的路径。
//   - 探不到就是探不到，不编造默认值（spec §4.2）
//   - 解析失败一律降级为 unavailable，不阻断会话创建
package ptyhost

import (
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// defaultEnvForward 是内置默认转发清单。配置里没写（nil）时用它。
//
// 只有 SSH_AUTH_SOCK 一个：它是**实测确认**会因托管形态丢失、且丢失后
// 直接让 git push / ssh 失效的那一个。其余变量按需由用户在配置里显式追加，
// 不预先塞一堆猜的。
var defaultEnvForward = []string{"SSH_AUTH_SOCK"}

// DefaultEnvForward 返回内置默认清单的副本。
//
// 返回副本而不是切片本身：调用方（config 解析、测试）拿到后可能就地排序或改写，
// 那会污染进程内所有后续会话。
func DefaultEnvForward() []string {
	out := make([]string, len(defaultEnvForward))
	copy(out, defaultEnvForward)
	return out
}

// launchctlGetenv 是平台级变量查询，测试可整体替换。
var launchctlGetenv = launchctlGetenvReal

// launchctlGetenvReal 在 macOS 上用 `launchctl getenv <name>` 查会话级变量。
//
// 关于这次 fork：B73 要求「防线全链路零 fork」，那条约束的对象是**进程耗尽时
// 仍需工作的诊断路径**。会话创建本身就要 fork 一个 shell，此处多一次 fork
// 不改变可用性边界。
//
// Linux 不猜：systemd 用户会话下没有等价的稳定查询口径，直接返回 false，
// 由「继承 + 用户显式配置」两条路兜底。
func launchctlGetenvReal(name string) (string, bool) {
	if runtime.GOOS != "darwin" {
		return "", false
	}
	out, err := exec.Command("launchctl", "getenv", name).Output()
	if err != nil {
		return "", false
	}
	v := strings.TrimSpace(string(out))
	if v == "" {
		return "", false
	}
	return v, true
}

// ResolveEnvForward 把 names 里每个变量按三级顺序解析后追加到 base，返回新环境。
//
// 参数：
//   - names: 要转发的变量名清单（调用方已按 nil→默认清单 归一化）
//   - base:  会话的基础环境（PATH / TERM 等），原样保留
//   - log:   逐个变量记录三态结论，不得为 nil
//
// 返回：base + 解析成功的 `NAME=VALUE`。探不到的变量**不出现**在结果里。
//
// 注意：日志只记变量名与结论来源，**不记变量值**——今天转发的是 socket 路径，
// 但这份清单是用户可配的，明天可能就有人往里加一个带凭据的变量。
func ResolveEnvForward(names []string, base []string, log *slog.Logger) []string {
	out := make([]string, 0, len(base)+len(names))
	out = append(out, base...)
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if v := os.Getenv(name); v != "" {
			out = append(out, name+"="+v)
			log.Info("终端环境变量已转发", "name", name, "source", "inherited")
			continue
		}
		if v, ok := launchctlGetenv(name); ok {
			out = append(out, name+"="+v)
			log.Info("终端环境变量已转发", "name", name, "source", "resolved", "via", "launchctl")
			continue
		}
		// 成功路径与失败路径都有声：不然无法区分「解析失败」与「这段代码没跑」。
		log.Warn("终端环境变量无法解析，该会话里将没有它",
			"name", name, "source", "unavailable", "goos", runtime.GOOS)
	}
	return out
}
