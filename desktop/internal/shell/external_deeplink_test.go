// 本文件锁定 B434 深链 raw message 通道（契约 §3.11、§4.3 条 28–41）：
//   - 协议字面量（对侧常量执法：web/src/app/lib/desktopShell.ts#OPEN_DEEPLINK_MESSAGE_PREFIX
//     与本常量逐字相等，两侧各锁一次，同 ExternalBrowserMessagePrefix 的既有形态）；
//   - 白名单放行/拒绝：拒绝路径一律断言 opener 零调用，不只看返回值（返回值对已处理
//     的拒绝也是 true，只看它是假绿温床，breakdown §5.7 第 4 条）；
//   - 日志不出现完整目标 URL 与 q（简报全文）；
//   - 三条 raw 通道对来源 frame 的判据一致（breakdown §5.7 第 5 条）。
//
// 不启动真实 opener、不 import Wails。
package shell_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
)

// deepLinkSource 是外链控制台 frame 的典型来源：带一次性 ticket 的 agentd console URL。
const deepLinkSource = "http://127.0.0.1:7777/console?ticket=source-secret"

// deepLinkBriefMarker 埋在 q 里，用来断言日志不泄露简报正文。
const deepLinkBriefMarker = "BRIEFSECRET434"

// deepLinkNewURL 形如 claudeDeepLink.ts#buildClaudeNewSessionURL 的产出：
// folder/q 经 encodeURIComponent，含中文、换行（%0A）、& 与 #（条 19 的字符集）。
const deepLinkNewURL = "claude://code/new?folder=%2FUsers%2Fme%2Fproj&q=%5Bhandoff%5D%20" +
	deepLinkBriefMarker + "%20%E4%B8%AD%E6%96%87%0A%22a%26b%23c%22"

const deepLinkContinueURL = "claude://code/continue?session=local_x"

// openSpy 记录 opener 的每次调用参数。
type openSpy struct {
	calls []string
	err   error
}

func (s *openSpy) open(target string) error {
	s.calls = append(s.calls, target)
	return s.err
}

func TestExternalDeepLinkMessagePrefixLiteral(t *testing.T) {
	if shell.ExternalDeepLinkMessagePrefix != "handoff:open-deeplink:" {
		t.Fatalf("prefix = %q", shell.ExternalDeepLinkMessagePrefix)
	}
	if shell.ExternalDeepLinkMessagePrefix == shell.ExternalBrowserMessagePrefix {
		t.Fatalf("深链前缀不得与系统浏览器前缀相同")
	}
}

// 条 30：非本前缀消息不消费、不调 opener。
func TestHandleExternalDeepLinkMessageIgnoresOtherPrefixes(t *testing.T) {
	for _, message := range []string{
		"",
		"other:message",
		shell.ExternalBrowserMessagePrefix + "http://127.0.0.1:7777/cards",
		shell.ExternalClipboardMessagePrefix + "r1:aGk=",
		deepLinkNewURL,          // 裸 URL，没有前缀
		"handoff:open-deeplink", // 少一个冒号
	} {
		spy := &openSpy{}
		if shell.HandleExternalDeepLinkMessage(nil, message, deepLinkSource, spy.open) {
			t.Fatalf("message %q 不应被消费", message)
		}
		if len(spy.calls) != 0 {
			t.Fatalf("message %q 不应调用 opener，got %q", message, spy.calls)
		}
	}
}

// 条 31/32：两条白名单深链放行，opener 恰一次且参数逐字等于原 URL。
func TestHandleExternalDeepLinkMessageOpensWhitelisted(t *testing.T) {
	for _, target := range []string{
		deepLinkNewURL,
		deepLinkContinueURL,
		"claude://code/new?folder=%2Ftmp&q=x",
		"claude://code/continue?session=local_0123abcd-ef",
	} {
		spy := &openSpy{}
		consumed := shell.HandleExternalDeepLinkMessage(
			nil, shell.ExternalDeepLinkMessagePrefix+target, deepLinkSource, spy.open)
		if !consumed {
			t.Fatalf("target %q 必须被消费", target)
		}
		if len(spy.calls) != 1 || spy.calls[0] != target {
			t.Fatalf("target %q opener 调用 = %q，want 恰一次且逐字相等", target, spy.calls)
		}
	}
}

// 条 33–40：拒绝路径返回 true（已处理）且 opener 零调用。
func TestHandleExternalDeepLinkMessageRejects(t *testing.T) {
	cases := []struct {
		name, target, source string
	}{
		{"条33 http", "http://127.0.0.1:7777/cards", deepLinkSource},
		{"条34 https", "https://claude.ai/code/new?q=x", deepLinkSource},
		{"条35 file", "file:///Applications/Claude.app", deepLinkSource},
		{"条36 javascript", "javascript:alert(1)", deepLinkSource},
		{"条37 host 非 code", "claude://chat/new?q=x", deepLinkSource},
		{"条37 host 带端口", "claude://code:80/new?q=x", deepLinkSource},
		{"条37 host 大小写", "claude://CODE/new?q=x", deepLinkSource},
		{"条37 无 host（opaque）", "claude:code/new?q=x", deepLinkSource},
		{"条37 userinfo", "claude://user@code/new?q=x", deepLinkSource},
		{"条38 path 不在白名单", "claude://code/settings", deepLinkSource},
		{"条38 path 空", "claude://code?q=x", deepLinkSource},
		{"条38 path 根", "claude://code/?q=x", deepLinkSource},
		{"条38 path 尾斜杠", "claude://code/new/?q=x", deepLinkSource},
		{"条38 path 子路径", "claude://code/new/x?q=x", deepLinkSource},
		{"条38 path 百分号变体", "claude://code/%6Eew?q=x", deepLinkSource},
		{"条38 fragment", "claude://code/new?q=x#frag", deepLinkSource},
		{"条38 空 fragment", "claude://code/new?q=x#", deepLinkSource},
		{"条39 换行", "claude://code/new?q=a\nb", deepLinkSource},
		{"条39 NUL", "claude://code/new?q=a\x00b", deepLinkSource},
		{"条39 DEL", "claude://code/new?q=a\x7fb", deepLinkSource},
		{"条39 TAB", "claude://code/continue?session=local_x\t", deepLinkSource},
		{"scheme 大写", "CLAUDE://code/new?q=x", deepLinkSource},
		{"scheme 前导空格", " claude://code/new?q=x", deepLinkSource},
		{"空目标", "", deepLinkSource},
		{"畸形 URL", "claude://[code/new?q=x", deepLinkSource},
		{"条40 来源为空", deepLinkContinueURL, ""},
		{"条40 来源 file", deepLinkContinueURL, "file:///tmp/index.html"},
		{"条40 来源 wails", deepLinkContinueURL, "wails://localhost/"},
		{"条40 来源 null", deepLinkContinueURL, "null"},
		{"条40 来源 about", deepLinkContinueURL, "about:blank"},
		{"条40 来源无 host", deepLinkContinueURL, "http:///console"},
		{"条40 来源 userinfo", deepLinkContinueURL, "http://u:p@127.0.0.1:7777/console"},
		{"条40 来源 claude", deepLinkContinueURL, "claude://code/new"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spy := &openSpy{}
			consumed := shell.HandleExternalDeepLinkMessage(
				nil, shell.ExternalDeepLinkMessagePrefix+tc.target, tc.source, spy.open)
			if !consumed {
				t.Fatal("已识别的拒绝消息必须被消费")
			}
			if len(spy.calls) != 0 {
				t.Fatalf("拒绝消息不应调用 opener，got %q", spy.calls)
			}
		})
	}
}

// opener 失败或未配置：仍消费（返回 true），不重试。
func TestHandleExternalDeepLinkMessageOpenerFailure(t *testing.T) {
	spy := &openSpy{err: errors.New("exec: open failed")}
	if !shell.HandleExternalDeepLinkMessage(
		nil, shell.ExternalDeepLinkMessagePrefix+deepLinkContinueURL, deepLinkSource, spy.open) {
		t.Fatal("opener 失败的消息必须被消费")
	}
	if len(spy.calls) != 1 {
		t.Fatalf("opener 失败不重试：calls = %q", spy.calls)
	}
	if !shell.HandleExternalDeepLinkMessage(
		nil, shell.ExternalDeepLinkMessagePrefix+deepLinkContinueURL, deepLinkSource, nil) {
		t.Fatal("opener 未配置的消息必须被消费")
	}
}

// 条 41：成功、拒绝、畸形、opener 失败四条路径的日志都不出现完整目标 URL 与 q。
func TestHandleExternalDeepLinkMessageLogsNoURL(t *testing.T) {
	malformed := "claude://[code/new?q=" + deepLinkBriefMarker
	cases := []struct {
		name, target string
		openErr      error
		wantResult   string
	}{
		{"成功", deepLinkNewURL, nil, "opened"},
		{"host 拒绝", strings.Replace(deepLinkNewURL, "//code/", "//chat/", 1), nil, "invalid_target"},
		{"畸形", malformed, nil, "invalid_target"},
		{"opener 失败", deepLinkNewURL, errors.New("open " + deepLinkNewURL + " failed"), "open_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			spy := &openSpy{err: tc.openErr}
			if !shell.HandleExternalDeepLinkMessage(
				log, shell.ExternalDeepLinkMessagePrefix+tc.target, deepLinkSource, spy.open) {
				t.Fatal("本协议消息必须被消费")
			}
			out := logs.String()
			if !strings.Contains(out, "result="+tc.wantResult) {
				t.Fatalf("日志缺 result=%s：%q", tc.wantResult, out)
			}
			for _, leak := range []string{deepLinkBriefMarker, tc.target, "source-secret"} {
				if strings.Contains(out, leak) {
					t.Fatalf("日志泄露 %q：%q", leak, out)
				}
			}
		})
	}
}

// breakdown §5.7 第 5 条：深链、系统浏览器、剪贴板三道门对来源 frame 的判据一致。
// 每行只变来源；目标/载荷对各通道都合法（浏览器目标取与来源同源的 /cards）。
func TestRawMessageSourceFrameCriteriaConsistent(t *testing.T) {
	cases := []struct {
		source string
		want   bool
	}{
		{"http://127.0.0.1:7777/console?ticket=x", true},
		{"https://agentd.example:8443/console", true},
		{"HTTP://127.0.0.1:7777/console", true},
		{"", false},
		{"file:///tmp/index.html", false},
		{"wails://localhost/", false},
		{"null", false},
		{"about:blank", false},
		{"http:///console", false},
		{"http://u:p@127.0.0.1:7777/console", false},
		{"javascript:alert(1)", false},
	}
	for _, tc := range cases {
		deep := &openSpy{}
		shell.HandleExternalDeepLinkMessage(
			nil, shell.ExternalDeepLinkMessagePrefix+deepLinkContinueURL, tc.source, deep.open)

		browserTarget := "http://127.0.0.1:7777/cards"
		if tc.want {
			browserTarget = strings.SplitN(tc.source, "/console", 2)[0] + "/cards"
		}
		browserOpened := false
		shell.HandleExternalBrowserMessage(
			nil, shell.ExternalBrowserMessagePrefix+browserTarget, tc.source,
			func(string) (string, error) { return "http://127.0.0.1:7777/console?ticket=t", nil },
			func(string) error { browserOpened = true; return nil },
		)

		clipboardWritten := false
		shell.HandleExternalClipboardMessage(
			nil, shell.ExternalClipboardMessagePrefix+"r1:"+base64.StdEncoding.EncodeToString([]byte("hi")),
			tc.source,
			func(string) bool { clipboardWritten = true; return true },
			nil,
		)

		got := []bool{len(deep.calls) == 1, browserOpened, clipboardWritten}
		for i, name := range []string{"deeplink", "browser", "clipboard"} {
			if got[i] != tc.want {
				t.Fatalf("source %q：%s 放行=%v，want %v（三通道判据须一致）", tc.source, name, got[i], tc.want)
			}
		}
	}
}
