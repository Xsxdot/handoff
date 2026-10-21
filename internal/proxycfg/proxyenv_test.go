package proxycfg

// proxyenv_test.go —— 账号代理覆盖子进程环境的回归（B453 段 3，plan Q6/V9 的 env 半边）。

import (
	"reflect"
	"slices"
	"testing"
)

func TestProxyEnvOverridesSixKeysKeepsNoProxy(t *testing.T) {
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	in := []string{"PATH=/bin", "HTTPS_PROXY=http://file:1", "https_proxy=http://file:2", "ALL_PROXY=socks5://x:3",
		"NO_PROXY=corp.local", "FOO=bar"}
	got := WithProxyEnv(in, "socks5://u:p@127.0.0.1:7897")
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		vals := valuesOf(got, k)
		if len(vals) != 1 || vals[0] != "socks5://u:p@127.0.0.1:7897" {
			t.Fatalf("%s = %v，want 唯一一行账号代理", k, vals)
		}
	}
	if vals := valuesOf(got, "NO_PROXY"); len(vals) != 1 || vals[0] != "corp.local" {
		t.Fatalf("NO_PROXY = %v，want 原样保留（不覆盖）", vals)
	}
	if !slices.Contains(got, "PATH=/bin") || !slices.Contains(got, "FOO=bar") {
		t.Fatalf("非代理键必须原样保留：%v", got)
	}
}

func TestProxyEnvAddsLocalhostNoProxyWhenMissing(t *testing.T) {
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	got := WithProxyEnv([]string{"PATH=/bin"}, "http://127.0.0.1:1080")
	for _, k := range []string{"NO_PROXY", "no_proxy"} {
		if vals := valuesOf(got, k); len(vals) != 1 || vals[0] != LocalNoProxy {
			t.Fatalf("%s = %v，want %q（opencode serve 走 localhost 不能被代理吃掉）", k, vals, LocalNoProxy)
		}
	}
}

func TestProxyEnvRespectsProcessNoProxy(t *testing.T) {
	t.Setenv("NO_PROXY", "localhost,10.0.0.0/8")
	got := WithProxyEnv([]string{"PATH=/bin"}, "http://127.0.0.1:1080")
	if vals := valuesOf(got, "NO_PROXY"); len(vals) != 0 {
		t.Fatalf("进程环境已有 NO_PROXY 时不补（子进程继承它），得 %v", vals)
	}
}

func TestProxyEnvEmptyProxyIsByteIdentical(t *testing.T) {
	in := []string{"PATH=/bin", "HTTPS_PROXY=http://file:1"}
	got := WithProxyEnv(in, "")
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("账号无代理时 env 必须与改动前一致：%v → %v", in, got)
	}
}

func valuesOf(env []string, key string) []string {
	var out []string
	for _, kv := range env {
		if len(kv) > len(key) && kv[:len(key)+1] == key+"=" {
			out = append(out, kv[len(key)+1:])
		}
	}
	return out
}
