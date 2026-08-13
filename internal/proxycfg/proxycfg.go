// Package proxycfg 把 handoff 配置里的 proxy 字符串翻译成各消费方要的形态。
//
// 职责：
//   - Validate：取值域校验，供配置加载在启动期硬拒坏值
//   - Transport：给 net/http 用的 *http.Transport
//   - GitArgs：给 git 子进程用的 `-c http.proxy=<url>` 前缀参数
//   - Redact：日志用的脱敏文本
//
// 边界：
//   - 不读配置文件：调用方把字符串给它，它只做翻译
//   - 不碰网络，不判断代理是否可达（那是消费方真发请求时才知道的事）
//   - 不决定「谁该走代理」：协调者↔agentd 那条链路永不走代理，这条纪律由
//     调用方（只有 release 与 agentd 的出网 git 接线）保证，不在本包
//   - 本包不打日志，日志由调用方在接线点打并须经 Redact（本包是纯翻译函数，
//     无 I/O、无外部调用、无状态变更）
package proxycfg

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// SupportedSchemes 是 proxy 允许的 scheme。
//
// 这四种是 net/http 的 Transport 与 git 的 http.proxy **都**原生支持的交集。
// socks4 不在其中：Go 从来没支持过它，配上去的表现是运行期报一句
// "unsupported protocol scheme"，而那时早已过了任何人会看的时刻。
var SupportedSchemes = []string{"http", "https", "socks5", "socks5h"}

// Validate 校验 proxy 取值域。空串合法（= 不配代理）。
//
// 参数：
//   - proxy: 代理地址，形如 socks5://127.0.0.1:1080
//
// 返回：
//   - 错误：URL 畸形、scheme 不在 SupportedSchemes、或缺 host。
//     错误文本一律列出支持的 scheme——只说"不支持"而不说"支持什么"，
//     等于让用户去猜
func Validate(proxy string) error {
	if proxy == "" {
		return nil
	}
	u, err := url.Parse(proxy)
	if err != nil {
		return fmt.Errorf("proxy %q 不是合法 URL: %w（支持 %s）",
			proxy, err, strings.Join(SupportedSchemes, "/"))
	}
	if !schemeSupported(u.Scheme) {
		return fmt.Errorf("proxy %q 的 scheme 为 %q，只支持 %s（裸 host:port 也不行，必须带 scheme）",
			proxy, u.Scheme, strings.Join(SupportedSchemes, "/"))
	}
	if u.Host == "" {
		return fmt.Errorf("proxy %q 缺少主机地址（支持 %s）",
			proxy, strings.Join(SupportedSchemes, "/"))
	}
	return nil
}

func schemeSupported(s string) bool {
	for _, want := range SupportedSchemes {
		if s == want {
			return true
		}
	}
	return false
}

// Transport 按 proxy 造一个 *http.Transport。
//
// 参数：
//   - proxy: 代理地址；**空串 = 不配**，返回的 Transport 沿用
//     http.ProxyFromEnvironment（即 HTTPS_PROXY/HTTP_PROXY/NO_PROXY），
//     与本功能上线前的行为一字不差
//
// 返回：
//   - 可直接塞进 http.Client 的 Transport
//   - 错误：proxy 未通过 Validate
//
// 注意：
//   - 非空 proxy 时**固定返回**该地址，不再看 NO_PROXY。显式配置就是显式意图，
//     而 handoff 自己走代理的出网只有 GitHub 一个域，"某些域不走代理"这个需求
//     在这里不存在
//   - 基于 http.DefaultTransport 克隆，因此连接池、HTTP/2、超时等默认值全部保留；
//     从零 new 一个 &http.Transport{} 会静默丢掉这些，症状是并发下载变慢而无人知晓
func Transport(proxy string) (*http.Transport, error) {
	if err := Validate(proxy); err != nil {
		return nil, err
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		// 正常不可能：标准库的 DefaultTransport 就是 *http.Transport。
		// 真发生了说明有人在进程里换掉了它，此时静默用零值 Transport 会丢掉
		// 那个人的意图，如实报错更好
		return nil, fmt.Errorf("http.DefaultTransport 不是 *http.Transport（被第三方替换？）")
	}
	tr := base.Clone()
	if proxy == "" {
		return tr, nil // Clone 已带 ProxyFromEnvironment
	}
	u, err := url.Parse(proxy)
	if err != nil {
		return nil, fmt.Errorf("解析 proxy %q: %w", proxy, err) // Validate 已过，这里防御性兜底
	}
	tr.Proxy = func(*http.Request) (*url.URL, error) { return u, nil }
	return tr, nil
}

// GitArgs 返回给 git 子进程的代理参数，须插在子命令**之前**。
//
// 参数：
//   - proxy: 代理地址；空串返回 nil
//
// 返回：
//   - 形如 []string{"-c", "http.proxy=socks5://127.0.0.1:1080"}
//
// 注意：
//   - git 的 http.proxy 只对 http(s):// 的 remote 生效，**对 ssh:// 与
//     git@host:path 无效**。SSH remote 要走代理得配 ssh 的 ProxyCommand，
//     那会动到用户的 ssh 配置面，不在 handoff 的职责内（见 README）
//   - 用 -c 而不是注入 HTTPS_PROXY 环境变量：不污染子进程环境，也不会让
//     本地 git 操作平白多一个配置
func GitArgs(proxy string) []string {
	if proxy == "" {
		return nil
	}
	return []string{"-c", "http.proxy=" + proxy}
}

// Redact 返回可安全打进日志的代理文本。
//
// 参数：
//   - proxy: 代理地址；空串返回空串
//
// 返回：
//   - 含 user:pass@ 时凭据部分替换为 ***，其余原样；解析不了时**只返回 scheme
//     加省略号**，绝不返回原文——解析失败恰恰是最可能把整串密码原样打出去的场合
//
// 注意：
//   - 这不是可选的美化。代理 URL 常含凭据，本仓纪律见 internal/envfile/resolver.go:64
func Redact(proxy string) string {
	if proxy == "" {
		return ""
	}
	u, err := url.Parse(proxy)
	if err != nil || u.Host == "" {
		return "<无法解析的 proxy 值，已隐藏>"
	}
	if u.User != nil {
		u.User = url.User("***")
	}
	return u.String()
}
