// 本文件覆盖桌面 raw message 到系统浏览器的接缝：协议字面量、来源同源校验、
// 非 http(s) 拒绝与 opener 失败消费。它不启动真实浏览器。
package shell_test

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
)

func TestHandleExternalBrowserMessage(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	const sourceFrameURL = "http://127.0.0.1:7777/console?ticket=secret"
	const targetURL = "http://127.0.0.1:7777/cards?project=handoff"

	if shell.ExternalBrowserMessagePrefix != "handoff:open-browser:" {
		t.Fatalf("ExternalBrowserMessagePrefix = %q", shell.ExternalBrowserMessagePrefix)
	}

	t.Run("同源 cards query 通过 wire 串交给 opener", func(t *testing.T) {
		var opened string
		consumed := shell.HandleExternalBrowserMessage(
			log,
			"handoff:open-browser:http://127.0.0.1:7777/cards?project=handoff",
			sourceFrameURL,
			func(url string) error { opened = url; return nil },
		)
		if !consumed {
			t.Fatal("同协议消息必须被消费")
		}
		if opened != targetURL {
			t.Fatalf("opener URL = %q, want %q", opened, targetURL)
		}
	})

	t.Run("未知协议不消费", func(t *testing.T) {
		called := false
		consumed := shell.HandleExternalBrowserMessage(log, "other:message", sourceFrameURL, func(string) error {
			called = true
			return nil
		})
		if consumed {
			t.Fatal("未知协议不应被消费")
		}
		if called {
			t.Fatal("未知协议不应调用 opener")
		}
	})

	invalid := []struct {
		name   string
		target string
		source string
	}{
		{name: "javascript scheme", target: "javascript:alert(1)", source: sourceFrameURL},
		{name: "file scheme", target: "file:///tmp/cards", source: sourceFrameURL},
		{name: "ftp scheme", target: "ftp://127.0.0.1:7777/cards", source: sourceFrameURL},
		{name: "different host", target: "http://evil.example/cards", source: sourceFrameURL},
		{name: "different port", target: "http://127.0.0.1:7778/cards", source: sourceFrameURL},
		{name: "different scheme", target: "https://127.0.0.1:7777/cards", source: sourceFrameURL},
		{name: "userinfo", target: "http://user@127.0.0.1:7777/cards", source: sourceFrameURL},
		{name: "missing source", target: targetURL, source: ""},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			consumed := shell.HandleExternalBrowserMessage(
				log,
				shell.ExternalBrowserMessagePrefix+tc.target,
				tc.source,
				func(string) error { called = true; return nil },
			)
			if !consumed {
				t.Fatal("已识别协议的拒绝消息仍必须被消费")
			}
			if called {
				t.Fatal("被拒绝 URL 不得交给 opener")
			}
		})
	}

	t.Run("opener 错误仍被消费", func(t *testing.T) {
		called := false
		consumed := shell.HandleExternalBrowserMessage(
			log,
			shell.ExternalBrowserMessagePrefix+targetURL,
			sourceFrameURL,
			func(string) error { called = true; return errors.New("browser unavailable") },
		)
		if !consumed {
			t.Fatal("opener 失败的协议消息仍必须被消费")
		}
		if !called {
			t.Fatal("有效 URL 必须尝试调用 opener")
		}
	})
}
