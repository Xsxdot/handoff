// 本文件覆盖桌面 raw message 到认证系统浏览器 URL 的接缝：协议字面量、来源同源校验、
// /cards next 白名单、签票与 opener 失败消费。它不启动真实浏览器。
package shell_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
)

func TestHandleExternalBrowserMessage(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	const source = "http://127.0.0.1:7777/console?ticket=source-secret"
	const target = "http://127.0.0.1:7777/cards?project=handoff"
	const browserURL = "http://127.0.0.1:7777/console?ticket=issued-secret&next=%2Fcards%3Fproject%3Dhandoff"

	if shell.ExternalBrowserMessagePrefix != "handoff:open-browser:" {
		t.Fatalf("prefix = %q", shell.ExternalBrowserMessagePrefix)
	}

	t.Run("同源先签票再打开 console", func(t *testing.T) {
		var gotNext, opened string
		var events []string
		consumed := shell.HandleExternalBrowserMessage(
			log, shell.ExternalBrowserMessagePrefix+target, source,
			func(next string) (string, error) {
				events = append(events, "issue")
				gotNext = next
				return browserURL, nil
			},
			func(url string) error {
				events = append(events, "open")
				opened = url
				return nil
			},
		)
		if !consumed {
			t.Fatal("协议消息必须被消费")
		}
		if gotNext != "/cards?project=handoff" {
			t.Fatalf("issue next = %q", gotNext)
		}
		if opened != browserURL {
			t.Fatalf("opened = %q, want %q", opened, browserURL)
		}
		if strings.Join(events, ",") != "issue,open" {
			t.Fatalf("调用顺序 = %q, want issue,open", strings.Join(events, ","))
		}
	})

	t.Run("未知协议不消费", func(t *testing.T) {
		called := false
		consumed := shell.HandleExternalBrowserMessage(log, "other:message", source,
			func(string) (string, error) {
				called = true
				return browserURL, nil
			},
			func(string) error {
				called = true
				return nil
			})
		if consumed {
			t.Fatal("未知协议不应被消费")
		}
		if called {
			t.Fatal("未知协议不应调用签票或 opener")
		}
	})

	invalid := []struct {
		name, target, source string
	}{
		{"javascript", "javascript:alert(1)", source},
		{"file", "file:///tmp/cards", source},
		{"ftp", "ftp://127.0.0.1:7777/cards", source},
		{"host", "http://evil.example/cards", source},
		{"port", "http://127.0.0.1:7778/cards", source},
		{"scheme", "https://127.0.0.1:7777/cards", source},
		{"userinfo", "http://user@127.0.0.1:7777/cards", source},
		{"source missing", target, ""},
		{"wrong path", "http://127.0.0.1:7777/other", source},
		{"prefix lookalike", "http://127.0.0.1:7777/cardsx", source},
		{"network path", "http://127.0.0.1:7777//evil", source},
		{"backslash", "http://127.0.0.1:7777/cards\\evil", source},
		{"fragment", "http://127.0.0.1:7777/cards#fragment", source},
		{"encoded control", "http://127.0.0.1:7777/cards?x=%01", source},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			issued, opened := false, false
			consumed := shell.HandleExternalBrowserMessage(
				log, shell.ExternalBrowserMessagePrefix+tc.target, tc.source,
				func(string) (string, error) {
					issued = true
					return browserURL, nil
				},
				func(string) error {
					opened = true
					return nil
				},
			)
			if !consumed {
				t.Fatal("已识别的拒绝消息必须被消费")
			}
			if issued || opened {
				t.Fatalf("拒绝消息不应签票/打开：issued=%v opened=%v", issued, opened)
			}
		})
	}

	t.Run("畸形目标 URL 的拒绝日志不泄露 query 或 token", func(t *testing.T) {
		logs.Reset()
		malformed := "http://[::1?token=secret"
		issued, opened := false, false
		consumed := shell.HandleExternalBrowserMessage(
			log, shell.ExternalBrowserMessagePrefix+malformed, source,
			func(string) (string, error) {
				issued = true
				return browserURL, nil
			},
			func(string) error {
				opened = true
				return nil
			},
		)
		if !consumed || issued || opened {
			t.Fatalf("畸形目标结果 consumed=%v issued=%v opened=%v", consumed, issued, opened)
		}
		if strings.Contains(logs.String(), "token=secret") ||
			strings.Contains(logs.String(), malformed) {
			t.Fatalf("拒绝日志泄露畸形目标 URL 或 token: %q", logs.String())
		}
	})

	t.Run("签票失败不回退裸 target 且日志不泄露", func(t *testing.T) {
		logs.Reset()
		opened := false
		consumed := shell.HandleExternalBrowserMessage(
			log, shell.ExternalBrowserMessagePrefix+target, source,
			func(string) (string, error) {
				return "", errors.New("agentd unavailable ticket=issued-secret")
			},
			func(string) error {
				opened = true
				return nil
			},
		)
		if !consumed || opened {
			t.Fatalf("签票失败结果 consumed=%v opened=%v", consumed, opened)
		}
		if strings.Contains(logs.String(), "issued-secret") ||
			strings.Contains(logs.String(), target) ||
			strings.Contains(logs.String(), source) {
			t.Fatalf("日志泄露 URL/ticket：%q", logs.String())
		}
	})

	t.Run("opener 失败仍消费且日志不泄露", func(t *testing.T) {
		logs.Reset()
		consumed := shell.HandleExternalBrowserMessage(
			log, shell.ExternalBrowserMessagePrefix+target, source,
			func(string) (string, error) { return browserURL, nil },
			func(string) error { return errors.New("browser unavailable ticket=issued-secret") },
		)
		if !consumed {
			t.Fatal("opener 失败消息必须被消费")
		}
		if strings.Contains(logs.String(), "issued-secret") ||
			strings.Contains(logs.String(), target) ||
			strings.Contains(logs.String(), source) {
			t.Fatalf("日志泄露 URL/ticket：%q", logs.String())
		}
	})
}
