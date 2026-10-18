// 本文件锁定 B434 深链 raw message 通道（契约 §3.11、§4.3 条 28–41；B435 X 起 /new 形态退役，
// 条 31 改为被拒——b435-contract.md §7）：
//   - 协议字面量（对侧常量执法：web/src/app/lib/desktopShell.ts#OPEN_DEEPLINK_MESSAGE_PREFIX
//     与本常量逐字相等，两侧各锁一次，同 ExternalBrowserMessagePrefix 的既有形态）；
//   - 白名单放行/拒绝：拒绝路径一律断言 opener 零调用，不只看返回值（返回值对已处理
//     的拒绝也是 true，只看它是假绿温床，breakdown §5.7 第 4 条）；拒绝用例以 /continue 为
//     载体并断言日志里的拒绝理由——否则 /new 退役后它们会因「path 不对」被拒而假绿；
//   - 日志不出现完整目标 URL 与 q（简报全文）；
//   - 三条 raw 通道对来源 frame 的判据一致（breakdown §5.7 第 5 条）；
//   - desktop/main.go RawMessageHandler 的接线顺序与生产 opener（源码守卫）。
//
// 不启动真实 opener、不 import Wails。
package shell_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
)

// deepLinkSource 是外链控制台 frame 的典型来源：带一次性 ticket 的 agentd console URL。
const deepLinkSource = "http://127.0.0.1:7777/console?ticket=source-secret"

// deepLinkBriefMarker 埋在 q 里，用来断言日志不泄露简报正文。
const deepLinkBriefMarker = "BRIEFSECRET434"

// deepLinkNewURL 是已退役的 new-session 深链形态（B434 claudeDeepLink.ts 的产出形状），
// B435 X 起必须被拒。
const deepLinkNewURL = "claude://code/new?folder=%2FUsers%2Fme%2Fproj&q=%5Bhandoff%5D%20" +
	deepLinkBriefMarker + "%20%E4%B8%AD%E6%96%87%0A%22a%26b%23c%22"

const deepLinkContinueURL = "claude://code/continue?session=local_x"

// deepLinkContinueSecretURL 是带哨兵的 continue 深链（query 键值不在白名单内校验），
// 用来断言日志不泄露 query。
const deepLinkContinueSecretURL = "claude://code/continue?session=local_x&note=" + deepLinkBriefMarker

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
		deepLinkContinueURL,     // 裸 URL，没有前缀
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

// 条 32：continue 深链放行，opener 恰一次且参数逐字等于原 URL。
func TestHandleExternalDeepLinkMessageOpensWhitelisted(t *testing.T) {
	for _, target := range []string{
		deepLinkContinueURL,
		deepLinkContinueSecretURL,
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

// 拒绝理由（validateClaudeDeepLink / validateDeepLinkSource 的固定文案，经日志 cause 字段可见）。
const (
	whyScheme    = "深链 scheme 必须是 claude://"
	whyHost      = "深链 host 必须是 code"
	whyUserinfo  = "深链不允许 userinfo"
	whyPath      = "深链 path 必须是 /continue"
	whyFragment  = "深链不允许 fragment"
	whyControl   = "深链含控制字符"
	whyEmpty     = "深链为空"
	whyMalformed = "深链无法解析"
	whySrcEmpty  = "来源 frame URL 为空"
	whySrcHTTP   = "来源 frame URL 必须是带 host 的 http(s) 地址"
	whySrcUser   = "来源 frame URL 不允许 userinfo"
)

// rejectAndReason 发一条深链消息，断言已消费、opener 零调用，返回日志全文（含拒绝理由）。
func rejectAndReason(t *testing.T, target, source string) string {
	t.Helper()
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	spy := &openSpy{}
	if !shell.HandleExternalDeepLinkMessage(log, shell.ExternalDeepLinkMessagePrefix+target, source, spy.open) {
		t.Fatal("已识别的拒绝消息必须被消费")
	}
	if len(spy.calls) != 0 {
		t.Fatalf("拒绝消息不应调用 opener，got %q", spy.calls)
	}
	return logs.String()
}

// 条 31（B435 X 改）：new-session 深链形态退役——被拒且 opener 零调用，理由是 path。
func TestHandleExternalDeepLinkMessageRejectsRetiredNew(t *testing.T) {
	for _, target := range []string{deepLinkNewURL, "claude://code/new?folder=%2Ftmp&q=x"} {
		if out := rejectAndReason(t, target, deepLinkSource); !strings.Contains(out, whyPath) {
			t.Fatalf("/new 应以 path 理由被拒：%q", out)
		}
	}
}

// 条 33–40：拒绝路径返回 true（已处理）、opener 零调用，且因声明的理由被拒（以 /continue 为载体）。
func TestHandleExternalDeepLinkMessageRejects(t *testing.T) {
	cases := []struct {
		name, target, source, why string
	}{
		{"条33 http", "http://127.0.0.1:7777/cards", deepLinkSource, whyScheme},
		{"条34 https", "https://claude.ai/code/continue?session=local_x", deepLinkSource, whyScheme},
		{"条35 file", "file:///Applications/Claude.app", deepLinkSource, whyScheme},
		{"条36 javascript", "javascript:alert(1)", deepLinkSource, whyScheme},
		{"条37 host 非 code", "claude://chat/continue?session=local_x", deepLinkSource, whyHost},
		{"条37 host 带端口", "claude://code:80/continue?session=local_x", deepLinkSource, whyHost},
		{"条37 host 大小写", "claude://CODE/continue?session=local_x", deepLinkSource, whyHost},
		{"条37 无 host（opaque）", "claude:code/continue?session=local_x", deepLinkSource, whyScheme},
		{"条37 userinfo", "claude://user@code/continue?session=local_x", deepLinkSource, whyUserinfo},
		{"条38 path 不在白名单", "claude://code/settings", deepLinkSource, whyPath},
		{"条38 path 空", "claude://code?session=local_x", deepLinkSource, whyPath},
		{"条38 path 根", "claude://code/?session=local_x", deepLinkSource, whyPath},
		{"条38 path 尾斜杠", "claude://code/continue/?session=local_x", deepLinkSource, whyPath},
		{"条38 path 子路径", "claude://code/continue/x?session=local_x", deepLinkSource, whyPath},
		{"条38 path 百分号变体", "claude://code/%63ontinue?session=local_x", deepLinkSource, whyPath},
		{"条38 fragment", "claude://code/continue?session=local_x#frag", deepLinkSource, whyFragment},
		{"条38 空 fragment", "claude://code/continue?session=local_x#", deepLinkSource, whyFragment},
		{"条39 换行", "claude://code/continue?session=a\nb", deepLinkSource, whyControl},
		{"条39 NUL", "claude://code/continue?session=a\x00b", deepLinkSource, whyControl},
		{"条39 DEL", "claude://code/continue?session=a\x7fb", deepLinkSource, whyControl},
		{"条39 TAB", "claude://code/continue?session=local_x\t", deepLinkSource, whyControl},
		{"scheme 大写", "CLAUDE://code/continue?session=local_x", deepLinkSource, whyScheme},
		{"scheme 前导空格", " claude://code/continue?session=local_x", deepLinkSource, whyScheme},
		{"空目标", "", deepLinkSource, whyEmpty},
		{"畸形 URL", "claude://[code/continue?session=local_x", deepLinkSource, whyMalformed},
		{"条40 来源为空", deepLinkContinueURL, "", whySrcEmpty},
		{"条40 来源 file", deepLinkContinueURL, "file:///tmp/index.html", whySrcHTTP},
		{"条40 来源 wails", deepLinkContinueURL, "wails://localhost/", whySrcHTTP},
		{"条40 来源 null", deepLinkContinueURL, "null", whySrcHTTP},
		{"条40 来源 about", deepLinkContinueURL, "about:blank", whySrcHTTP},
		{"条40 来源无 host", deepLinkContinueURL, "http:///console", whySrcHTTP},
		{"条40 来源 userinfo", deepLinkContinueURL, "http://u:p@127.0.0.1:7777/console", whySrcUser},
		{"条40 来源 claude", deepLinkContinueURL, "claude://code/continue?session=local_y", whySrcHTTP},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if out := rejectAndReason(t, tc.target, tc.source); !strings.Contains(out, tc.why) {
				t.Fatalf("应因「%s」被拒，日志：%q", tc.why, out)
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
	malformed := "claude://[code/continue?session=local_x&note=" + deepLinkBriefMarker
	// source 为空串时取 deepLinkSource；nilOpen 为 true 时不注入 opener。
	cases := []struct {
		name, target, source string
		openErr              error
		nilOpen              bool
		wantResult           string
	}{
		{"成功", deepLinkContinueSecretURL, "", nil, false, "opened"},
		{"host 拒绝", strings.Replace(deepLinkContinueSecretURL, "//code/", "//chat/", 1), "", nil, false, "invalid_target"},
		{"畸形", malformed, "", nil, false, "invalid_target"},
		{"opener 失败", deepLinkContinueSecretURL, "", errors.New("open " + deepLinkContinueSecretURL + " failed"), false, "open_failed"},
		{"来源拒绝", deepLinkContinueSecretURL, "file:///x?ticket=source-secret", nil, false, "invalid_source"},
		{"opener 未配置", deepLinkContinueSecretURL, "", nil, true, "opener_unconfigured"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			source := tc.source
			if source == "" {
				source = deepLinkSource
			}
			spy := &openSpy{err: tc.openErr}
			open := spy.open
			if tc.nilOpen {
				open = nil
			}
			if !shell.HandleExternalDeepLinkMessage(
				log, shell.ExternalDeepLinkMessagePrefix+tc.target, source, open) {
				t.Fatal("本协议消息必须被消费")
			}
			out := logs.String()
			if !strings.Contains(out, "result="+tc.wantResult) {
				t.Fatalf("日志缺 result=%s：%q", tc.wantResult, out)
			}
			for _, leak := range []string{deepLinkBriefMarker, tc.target, source, "source-secret"} {
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

// 源码守卫（契约 §3.11）：desktop/main.go 的 RawMessageHandler 依次尝试剪贴板、系统浏览器、
// 深链三个处理器，深链的 opener 生产注入为 app.Browser.OpenURL。package main 因
// go:embed all:frontend/dist 与 Wails 应用构造无法在 go test 下实例化，故读源码断言。
func TestRawMessageHandlerWiresDeepLinkThird(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "../../main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	var handler *ast.FuncLit
	ast.Inspect(file, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "RawMessageHandler" {
			handler, _ = kv.Value.(*ast.FuncLit)
			return false
		}
		return true
	})
	if handler == nil {
		t.Fatal("main.go 未找到 RawMessageHandler: func(...) {...}")
	}
	var order []string
	var deepLinkOpener string
	ast.Inspect(handler.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "shell" || !strings.HasPrefix(sel.Sel.Name, "HandleExternal") {
			return true
		}
		order = append(order, sel.Sel.Name)
		if sel.Sel.Name == "HandleExternalDeepLinkMessage" && len(call.Args) == 4 {
			deepLinkOpener = exprString(call.Args[3])
		}
		return true
	})
	want := []string{
		"HandleExternalClipboardMessage",
		"HandleExternalBrowserMessage",
		"HandleExternalDeepLinkMessage",
	}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("RawMessageHandler 处理器顺序 = %v，want %v", order, want)
	}
	if deepLinkOpener != "app.Browser.OpenURL" {
		t.Fatalf("深链 opener = %q，want app.Browser.OpenURL", deepLinkOpener)
	}
}

// exprString 把 a.b.c 形式的选择子表达式还原成点分字符串；其它形状返回空串。
func exprString(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		if left := exprString(v.X); left != "" {
			return left + "." + v.Sel.Name
		}
	}
	return ""
}
