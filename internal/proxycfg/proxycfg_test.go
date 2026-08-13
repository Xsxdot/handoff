package proxycfg_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/proxycfg"
)

// 空串必须保持现行为：Transport 的 Proxy 等价于 http.ProxyFromEnvironment。
// 判据不看函数指针（不可比较），看行为——设了 HTTPS_PROXY 环境变量后，
// 对 https URL 的请求应当被解析到那个代理。
func TestTransportEmptyHonorsEnv(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://env-proxy.example:3128")
	tr, err := proxycfg.Transport("")
	if err != nil {
		t.Fatalf("Transport(\"\"): %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/x", nil)
	u, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy(): %v", err)
	}
	if u == nil || u.Host != "env-proxy.example:3128" {
		t.Fatalf("空 proxy 应沿用环境变量，实得 %v", u)
	}
}

// 非空时固定返回配置的代理，且**不受环境变量影响**——显式配置就是显式意图。
func TestTransportExplicitOverridesEnv(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://env-proxy.example:3128")
	tr, err := proxycfg.Transport("socks5://127.0.0.1:1080")
	if err != nil {
		t.Fatalf("Transport: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/x", nil)
	u, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy(): %v", err)
	}
	if u == nil || u.Scheme != "socks5" || u.Host != "127.0.0.1:1080" {
		t.Fatalf("显式 proxy 应压过环境变量，实得 %v", u)
	}
}

func TestValidateAcceptsAllSupportedSchemes(t *testing.T) {
	for _, p := range []string{
		"http://h:8080", "https://h:8080", "socks5://h:1080", "socks5h://h:1080",
	} {
		if err := proxycfg.Validate(p); err != nil {
			t.Errorf("Validate(%q) 应通过，实得 %v", p, err)
		}
	}
}

func TestValidateRejectsBadValues(t *testing.T) {
	cases := map[string]string{
		"socks4://h:1080": "不支持的 scheme",
		"127.0.0.1:1080":  "裸 host:port 没有 scheme",
		"http://":         "缺 host",
		"://h:1080":       "畸形 URL",
	}
	for in, why := range cases {
		err := proxycfg.Validate(in)
		if err == nil {
			t.Errorf("Validate(%q) 应被拒（%s）", in, why)
			continue
		}
		// 错误文本必须列出支持的 scheme，否则用户只知道错了不知道该写什么
		for _, s := range proxycfg.SupportedSchemes {
			if !strings.Contains(err.Error(), s) {
				t.Errorf("Validate(%q) 的错误文本应列出 %q，实得 %q", in, s, err)
			}
		}
	}
}

// 空串是合法的（= 不配代理），Validate 必须放行。
func TestValidateAcceptsEmpty(t *testing.T) {
	if err := proxycfg.Validate(""); err != nil {
		t.Fatalf("空 proxy 应合法，实得 %v", err)
	}
}

func TestGitArgs(t *testing.T) {
	if got := proxycfg.GitArgs(""); got != nil {
		t.Errorf("空 proxy 应返回 nil，实得 %v", got)
	}
	got := proxycfg.GitArgs("socks5://127.0.0.1:1080")
	want := []string{"-c", "http.proxy=socks5://127.0.0.1:1080"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("GitArgs = %v，期望 %v", got, want)
	}
}

// 凭据纪律的回归测试：代理 URL 里的密码绝不能出现在任何日志文本里。
func TestRedactHidesCredentials(t *testing.T) {
	got := proxycfg.Redact("socks5://alice:s3cr3t@proxy.example:1080")
	if strings.Contains(got, "s3cr3t") {
		t.Fatalf("Redact 泄漏了密码：%q", got)
	}
	if !strings.Contains(got, "proxy.example:1080") {
		t.Errorf("Redact 应保留主机端口（排障要用），实得 %q", got)
	}
	// 无凭据时原样返回，别把好端端的地址也打成星号
	if got := proxycfg.Redact("http://h:8080"); got != "http://h:8080" {
		t.Errorf("无凭据时应原样返回，实得 %q", got)
	}
	if got := proxycfg.Redact(""); got != "" {
		t.Errorf("空串应返回空串，实得 %q", got)
	}
}

// 错误文本同样不能泄漏凭据：Validate 的错误最终会进 agentd 启动日志
// （config.Load 把校验错误记进日志），原文含密码等于凭据落地。
func TestValidateErrorRedactsCredentials(t *testing.T) {
	err := proxycfg.Validate("socks4://alice:s3cr3t@h:1080")
	if err == nil {
		t.Fatal("socks4 应被拒")
	}
	if strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("Validate 错误泄漏了密码：%q", err)
	}
}
