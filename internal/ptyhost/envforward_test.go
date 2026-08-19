package ptyhost

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func hasKV(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

// ① 继承：agentd 自身环境里就有 → 直接带过去，不去问 launchctl。
func TestResolveEnvForwardInherited(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "/tmp/inherited.sock")
	launchctlGetenv = func(string) (string, bool) {
		t.Fatal("自身环境已有该变量时不该再调 launchctl")
		return "", false
	}
	t.Cleanup(func() { launchctlGetenv = launchctlGetenvReal })

	var buf bytes.Buffer
	out := ResolveEnvForward([]string{"SSH_AUTH_SOCK"}, []string{"PATH=/bin"}, slog.New(slog.NewTextHandler(&buf, nil)))
	if !hasKV(out, "SSH_AUTH_SOCK=/tmp/inherited.sock") {
		t.Fatalf("环境里应含继承来的值，实得 %v", out)
	}
	if !strings.Contains(buf.String(), "inherited") {
		t.Errorf("成功路径必须有声（source=inherited），实得日志:\n%s", buf.String())
	}
}

// ② 解析：**这是本轮修复的那个缺陷**——变量不在 os.Environ() 里（托管形态），
// 必须走平台解析。没有这条用例，缺陷会在开发者手起 agentd 的机器上永远绿。
func TestResolveEnvForwardResolved(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "") // 显式清空，模拟 launchd/systemd 托管形态
	launchctlGetenv = func(name string) (string, bool) {
		if name == "SSH_AUTH_SOCK" {
			return "/var/run/launchd/Listeners", true
		}
		return "", false
	}
	t.Cleanup(func() { launchctlGetenv = launchctlGetenvReal })

	var buf bytes.Buffer
	out := ResolveEnvForward([]string{"SSH_AUTH_SOCK"}, nil, slog.New(slog.NewTextHandler(&buf, nil)))
	if !hasKV(out, "SSH_AUTH_SOCK=/var/run/launchd/Listeners") {
		t.Fatalf("应带上解析出的值，实得 %v", out)
	}
	if !strings.Contains(buf.String(), "resolved") {
		t.Errorf("解析成功必须记 source=resolved，实得日志:\n%s", buf.String())
	}
}

// ③ 探不到：如实记 unavailable，**不编造、不设默认值、不阻断会话创建**。
func TestResolveEnvForwardUnavailable(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	launchctlGetenv = func(string) (string, bool) { return "", false }
	t.Cleanup(func() { launchctlGetenv = launchctlGetenvReal })

	var buf bytes.Buffer
	out := ResolveEnvForward([]string{"SSH_AUTH_SOCK"}, []string{"PATH=/bin"}, slog.New(slog.NewTextHandler(&buf, nil)))
	for _, e := range out {
		if strings.HasPrefix(e, "SSH_AUTH_SOCK=") {
			t.Fatalf("探不到时绝不能凭空造一个值，实得 %q", e)
		}
	}
	if !hasKV(out, "PATH=/bin") {
		t.Error("base 环境必须原样保留")
	}
	if !strings.Contains(buf.String(), "unavailable") {
		t.Errorf("必须留下可搜的 unavailable 结论，实得日志:\n%s", buf.String())
	}
}

// 默认清单是内置常量，调用方拿到的是副本——改它不该影响下一次调用。
func TestDefaultEnvForwardIsCopy(t *testing.T) {
	a := DefaultEnvForward()
	if len(a) != 1 || a[0] != "SSH_AUTH_SOCK" {
		t.Fatalf("默认清单 = %v，期望 [SSH_AUTH_SOCK]", a)
	}
	a[0] = "TAMPERED"
	if DefaultEnvForward()[0] != "SSH_AUTH_SOCK" {
		t.Fatal("DefaultEnvForward 必须返回副本，不能把内置清单暴露出去")
	}
}
