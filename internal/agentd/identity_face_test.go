// identity_face_test.go —— B358.9 T1 会话/房间面换脸缝级测试。
//
// 职责：锁 gateway 各端点消费 resolveConsoleIdentity 的核心行为——配名后
// actor=user:<console_user> 且登录会话设备名进 RoomMessage.Device（只落款）、
// 未配名/非法名 fail-closed（403 + 文案含 console_user，禁旧脸回落）、
// 建会话 owner 缺省为解析人名、多端已读按人合并；并以源码守卫禁止 `"web:"` 回落。
//
// 边界：全部从真实 HTTP 进入（httptest 全链），不测 collab 门面内部语义；
// cookie 路径才有端戳（主令牌身份端戳恒空）。
package agentd

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// setConsoleUser 原地换活配置里的 console_user（白盒夹具）：newRoomsEnv 默认配
// "sycm"，fail-closed 探针需要空串/非法值。Store 一份副本，避免改共享指针。
func setConsoleUser(t *testing.T, env *ledgerEnv, value string) {
	t.Helper()
	next := *env.srv.conf()
	next.ConsoleUser = value
	env.srv.cfg.Store(&next)
}

// mustNamedSession 造一个带指定设备名的登录会话，返回 cookie 明文。
func mustNamedSession(t *testing.T, st *store.Store, id, device string) string {
	t.Helper()
	plain := "cookie-" + id
	now := time.Now()
	if err := st.CreateSession(&store.Session{
		ID: id, TokenHash: store.HashCredential(plain), DeviceName: device,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour), LastSeenAt: now,
	}); err != nil {
		t.Fatalf("CreateSession %s: %v", id, err)
	}
	return plain
}

// ledgerPostCookie 带 cookie 发 POST（cookie 路径才有登录会话设备名端戳）。
func ledgerPostCookie(t *testing.T, ts *httptest.Server, path, body, cookie string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

// ledgerGetCookie 带 cookie 发 GET。
func ledgerGetCookie(t *testing.T, ts *httptest.Server, path, cookie string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

// roomMessageEvents 返回全量账本事件（调用方自行按 type/body 过滤）。
func roomMessageEvents(t *testing.T, env *ledgerEnv) []ledger.Event {
	t.Helper()
	events, err := env.ledger.EventsFromAsc(nil, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

// TestConsoleFaceActorAndDeviceStamp 锁配名后 actor=解析人名、登录会话设备名
// 进 RoomMessage.Device（契约条 13/16/17/23/24、U1）。
func TestConsoleFaceActorAndDeviceStamp(t *testing.T) {
	env := newRoomsEnv(t) // console_user = "sycm"
	session := mustConsoleSession(t, env, "换脸场")
	cookie := mustNamedSession(t, env.st, "face-device", "mbp / Safari")
	code, body := ledgerPostCookie(t, env.ts, "/api/rooms/"+session.ID+"/messages", `{"body":"你好"}`, cookie)
	if code != 200 {
		t.Fatalf("cookie 发言应 200: %d %s", code, body)
	}
	found := false
	for _, ev := range roomMessageEvents(t, env) {
		if ev.Type != ledger.EvRoomMessage {
			continue
		}
		var msg proto.RoomMessage
		if err := json.Unmarshal(ev.Payload, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Body != "你好" {
			continue
		}
		found = true
		if ev.Actor != consoleMember { // consoleMember == "user:sycm"
			t.Fatalf("actor 应换脸为解析人名 %q，实得 %q", consoleMember, ev.Actor)
		}
		if msg.Device != "mbp / Safari" {
			t.Fatalf("端戳应为登录会话设备名，实得 %q", msg.Device)
		}
	}
	if !found {
		t.Fatal("消息未落账")
	}
}

// TestConsoleFaceFailClosedWithoutName 锁未配名时全部需身份端点 403+可行动文案
// 且写面零落账（契约条 14/16、U3、P-1/P-2）。
func TestConsoleFaceFailClosedWithoutName(t *testing.T) {
	env := newRoomsEnv(t)
	session := mustConsoleSession(t, env, "缺名场")
	before := len(roomMessageEvents(t, env))
	setConsoleUser(t, env, "")
	probes := []struct{ method, path, body string }{
		{"POST", "/api/rooms/" + session.ID + "/messages", `{"body":"x"}`},
		{"POST", "/api/rooms/" + session.ID + "/read", `{"upto_seq":1}`},
		{"POST", "/api/sessions", `{"title":"x"}`},
		{"POST", "/api/sessions/" + session.ID + "/members", `{}`},
		{"POST", "/api/sessions/" + session.ID + "/archive", `{}`},
		// join/leave 是写面（会改卡与会话归属），与其余写面同门 fail-closed：
		// 不设门时它们会在未配名身份下带着零值 actor 落账/改归属。
		{"POST", "/api/sessions/" + session.ID + "/cards", `{"card":"B1"}`},
		{"DELETE", "/api/sessions/" + session.ID + "/cards/B1", ""},
		{"GET", "/api/inbox", ""},
		{"GET", "/api/sessions", ""},
		{"GET", "/api/rooms?limit=50", ""},
	}
	for _, p := range probes {
		var code int
		var body string
		switch p.method {
		case http.MethodGet:
			code, body = ledgerGet(t, env.testAgentdEnv, p.path)
		case http.MethodDelete:
			code, body = ledgerDelete(t, env.testAgentdEnv, p.path, p.body)
		default:
			code, body = ledgerPost(t, env.testAgentdEnv, p.path, p.body)
		}
		if code != http.StatusForbidden {
			t.Fatalf("%s %s 未配名必须 403，实得 %d %s", p.method, p.path, code, body)
		}
		if !strings.Contains(body, "console_user") {
			t.Fatalf("%s %s 拒收文案应含 console_user（可行动）: %s", p.method, p.path, body)
		}
	}
	after := len(roomMessageEvents(t, env))
	if after != before {
		t.Fatalf("未配名写面必须零落账: before=%d after=%d", before, after)
	}
}

// TestConsoleFaceRejectsIllegalName 锁非法 console_user 等同未配（契约条 15）。
func TestConsoleFaceRejectsIllegalName(t *testing.T) {
	env := newRoomsEnv(t)
	session := mustConsoleSession(t, env, "非法名场")
	setConsoleUser(t, env, "bad name")
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/rooms/"+session.ID+"/messages", `{"body":"x"}`)
	if code != http.StatusForbidden || !strings.Contains(body, "console_user") {
		t.Fatalf("非法 console_user 必须 fail-closed 403+文案: %d %s", code, body)
	}
}

// TestConsoleFaceIllegalNameWarnsOnce 锁 B358.9 review finding 4：非法
// console_user 的失败只由拒收门 Warn 一条（此前 identity.go 与 roomsapi.go
// 各 Warn 一条，重复噪声）。捕获 handler 不过滤级别，直接数 Warn 记录。
func TestConsoleFaceIllegalNameWarnsOnce(t *testing.T) {
	env := newRoomsEnv(t)
	cap := &roomsLogCapture{}
	env.srv.log = slog.New(cap)
	session := mustConsoleSession(t, env, "非法名 Warn 场")
	setConsoleUser(t, env, "bad name")
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/rooms/"+session.ID+"/messages", `{"body":"x"}`)
	if code != http.StatusForbidden {
		t.Fatalf("非法 console_user 必须 403: %d %s", code, body)
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	warns := 0
	for _, rec := range cap.records {
		if rec.Level == slog.LevelWarn {
			warns++
		}
	}
	if warns != 1 {
		t.Fatalf("非法 console_user 失败应恰一条 Warn（不重复），实得 %d", warns)
	}
}

// TestSessionCreateOwnerDefaultsToConsoleIdentity 锁建会话 owner 缺省为解析人名、
// 显式 owner 保留、显式非法 400（契约条 43–45、§3.8）。
func TestSessionCreateOwnerDefaultsToConsoleIdentity(t *testing.T) {
	env := newRoomsEnv(t)
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/sessions", `{"title":"默认群主"}`)
	if code != 200 {
		t.Fatalf("缺 owner 已配名应 200 且缺省为人名: %d %s", code, body)
	}
	var session proto.Session
	if err := json.Unmarshal([]byte(body), &session); err != nil {
		t.Fatal(err)
	}
	if session.Owner != consoleMember {
		t.Fatalf("缺省 owner 应为解析人名 %q，实得 %q", consoleMember, session.Owner)
	}
	// 显式 owner 仍按统一记法校验并保留。
	code, body = ledgerPost(t, env.testAgentdEnv, "/api/sessions", `{"title":"显式","owner":"agent:opencode"}`)
	if code != 200 {
		t.Fatalf("显式合法 owner: %d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &session); err != nil {
		t.Fatal(err)
	}
	if session.Owner != "agent:opencode" {
		t.Fatalf("显式 owner 应保留: %q", session.Owner)
	}
	if code, _ = ledgerPost(t, env.testAgentdEnv, "/api/sessions", `{"title":"x","owner":"web:1"}`); code != 400 {
		t.Fatalf("显式机器位 owner 应 400: %d", code)
	}
	if code, _ = ledgerPost(t, env.testAgentdEnv, "/api/sessions", `{"title":"x","owner":"mallory"}`); code != 400 {
		t.Fatalf("显式裸名 owner 应 400: %d", code)
	}
}

// TestConsoleReadMergesAcrossDevices 锁多端已读按人合并（U2 机内可行版）：
// 同一人的游标键 => 一端已读各端都清，端不拆红点。
func TestConsoleReadMergesAcrossDevices(t *testing.T) {
	env := newRoomsEnv(t)
	session := mustConsoleSession(t, env, "多端已读场")
	if _, err := env.srv.rooms.Send(session.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "一"}, consoleMember); err != nil {
		t.Fatal(err)
	}
	seq2, err := env.srv.rooms.Send(session.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "二"}, consoleMember)
	if err != nil {
		t.Fatal(err)
	}
	cookieA := mustNamedSession(t, env.st, "dev-a", "设备A")
	cookieB := mustNamedSession(t, env.st, "dev-b", "设备B")
	// A 端全量已读。
	if code, body := ledgerPostCookie(t, env.ts, "/api/rooms/"+session.ID+"/read",
		fmt.Sprintf(`{"upto_seq":%d}`, seq2), cookieA); code != 200 {
		t.Fatalf("A 端已读: %d %s", code, body)
	}
	// B 端拉列表：同一个人的游标键 => 未读 0（端不拆红点）。
	code, body := ledgerGetCookie(t, env.ts, "/api/sessions", cookieB)
	if code != 200 {
		t.Fatalf("B 端列会话: %d %s", code, body)
	}
	var out struct {
		Sessions []proto.SessionSummary `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	for _, s := range out.Sessions {
		if s.ID == session.ID && s.Unread != 0 {
			t.Fatalf("一端已读应各端都清（按人合并），实得 unread=%d", s.Unread)
		}
	}
}

// TestConsoleFaceNoWebFallbackSourceGuard 源码守卫：禁止旧传输层脸 `"web:"` 回落。
func TestConsoleFaceNoWebFallbackSourceGuard(t *testing.T) {
	for _, path := range []string{"roomsapi.go", "sessionsapi.go"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读 %s: %v", path, err)
		}
		if strings.Contains(string(raw), `"web:"`) {
			t.Errorf("%s 仍有旧传输层脸字面量 \"web:\"（应已换脸为 resolveConsoleIdentity）", path)
		}
	}
}
