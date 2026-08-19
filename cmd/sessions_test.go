// handoff sessions 测试：渲染、吊销、以及设备名的终端注入防护。
package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// TestRenderSessionsStripsControlChars 钉死 spec §6：设备名里的 ANSI 转义序列
// 绝不能原样打到终端上——服务端已经净化过一道，但 CLI 不能假设对端是新版 agentd。
func TestRenderSessionsStripsControlChars(t *testing.T) {
	now := time.Now()
	var buf bytes.Buffer
	renderSessions(&buf, []proto.SessionInfo{{
		ID: "sess-1", DeviceName: "设备\x1b[31m名\x07",
		CreatedAt: now, ExpiresAt: now.Add(time.Hour), LastSeenAt: now,
	}})
	out := buf.String()
	if strings.ContainsRune(out, '\x1b') || strings.ContainsRune(out, '\x07') {
		t.Fatalf("输出里残留控制字符: %q", out)
	}
	if !strings.Contains(out, "sess-1") {
		t.Errorf("输出缺少会话 id: %q", out)
	}
}

// TestRenderSessionsMarksRevoked 钉死：已吊销的会话要显式标出，而不是看起来正常。
func TestRenderSessionsMarksRevoked(t *testing.T) {
	now := time.Now()
	revoked := now.Add(-time.Minute)
	var buf bytes.Buffer
	renderSessions(&buf, []proto.SessionInfo{{
		ID: "sess-1", DeviceName: "手机", CreatedAt: now,
		ExpiresAt: now.Add(time.Hour), LastSeenAt: now, RevokedAt: &revoked,
	}})
	if !strings.Contains(buf.String(), "已吊销") {
		t.Fatalf("已吊销的会话未标出: %q", buf.String())
	}
}

// TestRenderSessionsEmpty 钉死：空列表要说人话，不是打一片空白。
func TestRenderSessionsEmpty(t *testing.T) {
	var buf bytes.Buffer
	renderSessions(&buf, nil)
	if !strings.Contains(buf.String(), "没有") {
		t.Fatalf("空列表输出不友好: %q", buf.String())
	}
}

// TestSessionsEndToEnd 验证 sessions 列出与 revoke 发出的实际 HTTP 请求。
func TestSessionsEndToEnd(t *testing.T) {
	var gotMethod, gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `[{"id":"sess-1","device_name":"手机","created_at":"2026-08-11T00:00:00Z",`+
				`"expires_at":"2036-08-11T00:00:00Z","last_seen_at":"2026-08-11T00:00:00Z","revoked_at":null}]`)
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer ts.Close()

	var out bytes.Buffer
	if err := runSubcommandForTest(t, &out, ts.URL, testToken, []string{"sessions"}); err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/auth/sessions" {
		t.Fatalf("列出请求 = %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(out.String(), "sess-1") || !strings.Contains(out.String(), "手机") {
		t.Fatalf("列表输出不含会话: %q", out.String())
	}

	out.Reset()
	if err := runSubcommandForTest(t, &out, ts.URL, testToken, []string{"sessions", "revoke", "sess-1"}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/auth/sessions/sess-1" {
		t.Fatalf("吊销请求 = %s %s，期望 DELETE /api/auth/sessions/sess-1", gotMethod, gotPath)
	}
	if !strings.Contains(out.String(), "已吊销会话 sess-1") {
		t.Errorf("吊销成功未回显: %q", out.String())
	}
}
