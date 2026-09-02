// 本文件验证原生剪贴板 raw message 的协议边界与结果回传，不触碰真实系统剪贴板。
package shell_test

import (
	"encoding/base64"
	"testing"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
)

func TestHandleExternalClipboardMessage(t *testing.T) {
	const source = "http://127.0.0.1:7788/console?ticket=secret"
	encoded := base64.StdEncoding.EncodeToString([]byte("你好\nhello"))
	var gotText, gotID string
	var gotOK bool

	consumed := shell.HandleExternalClipboardMessage(
		nil,
		shell.ExternalClipboardMessagePrefix+"17:"+encoded,
		source,
		func(text string) bool {
			gotText = text
			return true
		},
		func(requestID string, ok bool) {
			gotID, gotOK = requestID, ok
		},
	)
	if !consumed {
		t.Fatal("原生剪贴板协议消息必须被消费")
	}
	if gotText != "你好\nhello" || gotID != "17" || !gotOK {
		t.Fatalf("回传结果 text=%q id=%q ok=%v", gotText, gotID, gotOK)
	}
}

func TestHandleExternalClipboardMessageRejectsInvalidBoundary(t *testing.T) {
	const source = "http://127.0.0.1:7788/console"
	tests := []struct {
		name    string
		message string
		source  string
	}{
		{name: "unknown protocol", message: "other:message", source: source},
		{name: "invalid id", message: shell.ExternalClipboardMessagePrefix + "bad.id:YQ==", source: source},
		{name: "invalid encoding", message: shell.ExternalClipboardMessagePrefix + "1:not-base64", source: source},
		{name: "invalid source", message: shell.ExternalClipboardMessagePrefix + "1:YQ==", source: "file:///tmp/console"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			consumed := shell.HandleExternalClipboardMessage(
				nil, tt.message, tt.source,
				func(string) bool {
					called = true
					return true
				},
				func(string, bool) {},
			)
			if tt.name == "unknown protocol" {
				if consumed || called {
					t.Fatalf("未知协议结果 consumed=%v called=%v", consumed, called)
				}
				return
			}
			if !consumed || called {
				t.Fatalf("非法请求结果 consumed=%v called=%v", consumed, called)
			}
		})
	}
}
