// proxyenv.go —— 账号代理覆盖子进程环境（B453 spec S4「账号代理随载体生效」，plan Q6）。
//
// 职责：任务回合（编排三处 env 组装）与检测回合（hostapi 唤起）共用一份「覆盖代理键」的
// 规则，优先级 = 账号代理 > 执行器 env 文件 > 机器进程环境（spec「实现决定」）。
// 边界：只改传给子进程的 env 切片；不读写配置文件、不打日志（代理串可能带密码，调用方
// 用 Mask 记日志）。
package proxycfg

import (
	"os"
	"strings"
)

// LocalNoProxy 是账号代理生效而下层没有 NO_PROXY 时补的回环地址：opencode 执行器经
// localhost 与自己起的 `opencode serve` 通信，socks 代理吃掉 localhost 会让任务整段失联。
const LocalNoProxy = "localhost,127.0.0.1,::1"

// proxyKeys 是被账号代理覆盖的六个键（大小写各一）。NO_PROXY 刻意不在内。
var proxyKeys = []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"}

// WithProxyEnv 返回以 proxy 覆盖六个代理键后的 env：先删 env 里全部同名行再追加，覆盖优先
// 级固定为账号代理（env 文件允许重复键，只追加会依赖重复键的处理顺序）；追加的行同时压过
// 子进程从机器进程环境继承的同名键。NO_PROXY 不覆盖；env 与进程环境都没有 NO_PROXY/no_proxy
// 时补 LocalNoProxy（大小写各一）。proxy 为空返回 env 本身——未选账号或账号未填代理时与
// 改动前逐字节一致。
func WithProxyEnv(env []string, proxy string) []string {
	proxy = strings.TrimSpace(proxy)
	if proxy == "" {
		return env
	}
	out := make([]string, 0, len(env)+len(proxyKeys)+2)
	hasNoProxy := false
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if isProxyKey(key) {
			continue
		}
		if key == "NO_PROXY" || key == "no_proxy" {
			hasNoProxy = true
		}
		out = append(out, kv)
	}
	for _, k := range proxyKeys {
		out = append(out, k+"="+proxy)
	}
	if !hasNoProxy && os.Getenv("NO_PROXY") == "" && os.Getenv("no_proxy") == "" {
		out = append(out, "NO_PROXY="+LocalNoProxy, "no_proxy="+LocalNoProxy)
	}
	return out
}

func isProxyKey(key string) bool {
	for _, k := range proxyKeys {
		if key == k {
			return true
		}
	}
	return false
}
