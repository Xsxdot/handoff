// sessionsapi_test.go —— B358.4 会话 HTTP 端点缝级测试。
//
// 职责：六端点全部从真实 mux 注册进 httptest（ledgerGet/ledgerPost/ledgerDelete
// 穿 httptest.NewServer——「空壳冒充接线」的形状禁令，breakdown S4 缺陷族 4），
// 调用链穿过 collab 入站门面（spec §6 缝 #1 的调用方侧）。断言：§2.1 路由
// 形状、§2.2 错误映射表逐格、proto.SessionDetail 键集金样本、缺陷族 5 反例
// （请求体塞 actor 被忽略；伪造 member 查询参数不影响本人未读）。
// 边界：不测 collab 门面内部语义（S1 已收口）；不测 TS 半边（S6 孪生金样本）。
package agentd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// sessionFixtureOwner 是会话夹具的群主身份（P4 统一记法，拍板①：owner 取
// 请求体、校验 user:/agent: 前缀；先例 wakeconsumer_b358_test.go#mustWakeSessionFixture
// 的 user:tester）。审计 actor 与它是两个字段：前者恒为服务端注入的
// webUserMember，不因 owner 改变。
const sessionFixtureOwner = "user:tester"

// mustConsoleSession 经 HTTP 建一场会话（owner=sessionFixtureOwner，请求体
// 统一记法），返回会话投影。拍板①后群主不再是 webUserMember，而 HTTP 发言
// 路径的 actor 由服务端注入恒为 web:127.0.0.1、书写者执法要求它是会话成员
// ——夹具以 Store.AddSessionMember（幂等、零事件副作用；成员扩张的 HTTP
// 端点显式不做，plan §8.5）把控制台成员坐进会话，控制台发言/已读/收 @ 全链可用。
func mustConsoleSession(t *testing.T, env *ledgerEnv, title string) proto.Session {
	t.Helper()
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/sessions",
		fmt.Sprintf(`{"title":%q,"owner":%q}`, title, sessionFixtureOwner))
	if code != 200 {
		t.Fatalf("POST /api/sessions: %d %s", code, body)
	}
	var session proto.Session
	if err := json.Unmarshal([]byte(body), &session); err != nil {
		t.Fatalf("解码建会话响应: %v", err)
	}
	if !strings.HasPrefix(session.ID, "session:") {
		t.Fatalf("会话 id 应形如 session:<n>: %q", session.ID)
	}
	if err := env.ledger.AddSessionMember(session.ID, webUserMember, webUserMember); err != nil {
		t.Fatalf("坐进控制台成员: %v", err)
	}
	return session
}

// TestSessionsCreateEndpoint 锁：建会话 200 + session:<n> id + 群主取请求体
// 统一记法（拍板①，owner 前缀校验）。两字段不混用反例（同支内）：body 塞
// actor 字段被忽略（审计 actor 恒服务端注入 roomUserActor）；owner 缺统一
// 记法前缀（含机器位 web: 与裸名）或缺失一律 400；空标题 400。
func TestSessionsCreateEndpoint(t *testing.T) {
	env := newRoomsEnv(t)
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/sessions",
		`{"title":"需求对齐","owner":"user:mallory","actor":"user:mallory"}`)
	if code != 200 {
		t.Fatalf("POST /api/sessions: %d %s", code, body)
	}
	var session proto.Session
	if err := json.Unmarshal([]byte(body), &session); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(session.ID, "session:") {
		t.Fatalf("响应应含 session:<n> 形 id: %q", session.ID)
	}
	if session.Owner != "user:mallory" {
		t.Fatalf("群主必须取请求体统一记法（拍板①）: %q", session.Owner)
	}
	// 两字段不混用反例落账核对：EvSessionCreated 的 actor 是服务端注入值
	// （roomUserActor），不是 body 塞的 actor 串、也不是 owner。
	events, err := env.ledger.EventsFromAsc(nil, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	created := false
	for _, ev := range events {
		if ev.Type != ledger.EvSessionCreated {
			continue
		}
		created = true
		if ev.Actor != webUserMember {
			t.Fatalf("建会话审计 actor 应服务端注入 %q，实得 %q", webUserMember, ev.Actor)
		}
	}
	if !created {
		t.Fatal("EvSessionCreated 应已落账")
	}
	// owner 前缀校验反例：机器位 web: 不是成员统一记法（拍板①「两字段不得混用」）。
	if code, _ = ledgerPost(t, env.testAgentdEnv, "/api/sessions",
		`{"title":"x","owner":"web:127.0.0.1"}`); code != 400 {
		t.Fatalf("owner 用机器位 web: 应 400: %d", code)
	}
	if code, _ = ledgerPost(t, env.testAgentdEnv, "/api/sessions",
		`{"title":"x","owner":"mallory"}`); code != 400 {
		t.Fatalf("owner 裸名无前缀应 400: %d", code)
	}
	// owner 缺失 → 400（群主是初始成员，契约条 3，不可缺）。
	if code, _ = ledgerPost(t, env.testAgentdEnv, "/api/sessions", `{"title":"x"}`); code != 400 {
		t.Fatalf("owner 缺失应 400: %d", code)
	}
	// 空标题拒绝（handler 卫生检查，与 handleCardCreate 同款）。
	if code, _ = ledgerPost(t, env.testAgentdEnv, "/api/sessions",
		`{"title":"  ","owner":"user:mallory"}`); code != 400 {
		t.Fatalf("空标题应 400: %d", code)
	}
}

// TestSessionsListUnreadAndMemberForgery 锁：列表含新行、Kind=session、
// 两条无 @ 消息 → Unread=2（breakdown S4 ③ 判据）；缺陷族 5 反例：伪造
// ?member= 查询参数不影响本人未读投影（member 维度只认服务端注入）。
func TestSessionsListUnreadAndMemberForgery(t *testing.T) {
	env := newRoomsEnv(t)
	session := mustConsoleSession(t, env, "未读对账场")
	for _, body := range []string{"第一条", "第二条"} {
		code, resp := ledgerPost(t, env.testAgentdEnv,
			"/api/rooms/"+session.ID+"/messages", fmt.Sprintf(`{"body":%q}`, body))
		if code != 200 {
			t.Fatalf("会话发言 %q: %d %s", body, code, resp)
		}
	}
	var out struct {
		Sessions []proto.SessionSummary `json:"sessions"`
	}
	// member 查询参数是伪造面：handler 只认 roomUserActor。
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/sessions?member=user:mallory")
	if code != 200 {
		t.Fatalf("GET /api/sessions: %d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	var found *proto.SessionSummary
	for i := range out.Sessions {
		if out.Sessions[i].ID == session.ID {
			found = &out.Sessions[i]
		}
	}
	if found == nil {
		t.Fatalf("列表应含新会话: %+v", out.Sessions)
	}
	if found.Kind != proto.SessionKind {
		t.Fatalf("行 Kind 应为 session: %q", found.Kind)
	}
	if found.Title != "未读对账场" || found.Owner != sessionFixtureOwner {
		t.Fatalf("标题/群主投影错误（群主=请求体统一记法，拍板①）: %+v", *found)
	}
	if found.Unread != 2 {
		t.Fatalf("两条无 @ 消息 → Unread=2（伪造 member 参数不得改写本人未读）: %d", found.Unread)
	}
	// 已读后伪造参数不得把「别人的未读」带回本人投影：MarkRead 的是服务端注入
	// 成员（webUserMember），若 handler 改读 query member（变异抽查①），这里会
	// 按 user:mallory 的空游标算出 2 而翻红。
	if code, body = ledgerPost(t, env.testAgentdEnv, "/api/rooms/"+session.ID+"/read",
		fmt.Sprintf(`{"upto_seq":1000000}`)); code != 200 {
		t.Fatalf("POST /read: %d %s", code, body)
	}
	code, body = ledgerGet(t, env.testAgentdEnv, "/api/sessions?member=user:mallory")
	if code != 200 {
		t.Fatalf("GET /api/sessions after read: %d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	for _, s := range out.Sessions {
		if s.ID == session.ID && s.Unread != 0 {
			t.Fatalf("已读后未读应为 0（伪造 member 参数不得带回他人未读）: %+v", s)
		}
	}
}

// TestSessionDetailEndpointKeyset 锁：详情响应键集与 proto.SessionDetail 金样本
// 逐键一致（handler 不手抖改键——序列化边界断言穿真实 HTTP，不是本地 marshal）；
// omitempty 三态语义（nodes 无 task_mirrored 时**缺键**而非 null）；不存在会话
// → 404 且文案含会话 id（breakdown 缺陷族 2，文案由 handler 包 id 保证——
// translateNotFound/mapSessionError 双重归一会丢 Store 层文案，见台账续跑接手 5）。
func TestSessionDetailEndpointKeyset(t *testing.T) {
	env := newRoomsEnv(t)
	session := mustConsoleSession(t, env, "详情键集场")
	card := seedCard(t, env, "进群卡")
	if code, body := ledgerPost(t, env.testAgentdEnv, "/api/sessions/"+session.ID+"/cards",
		fmt.Sprintf(`{"card":%q}`, card.ID)); code != 200 {
		t.Fatalf("拉卡进群: %d %s", code, body)
	}
	if code, body := ledgerPost(t, env.testAgentdEnv, "/api/rooms/"+session.ID+"/messages",
		`{"body":"键集场的第一条"}`); code != 200 {
		t.Fatalf("会话发言: %d %s", code, body)
	}
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/sessions/"+session.ID)
	if code != 200 {
		t.Fatalf("GET /api/sessions/{id}: %d %s", code, body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatal(err)
	}
	// 顶层键集：summary 恒在；timeline 有结构事件在场；nodes 无 task_mirrored
	// 事件 → omitempty 缺键（缺失≠null 的字面分辨）。
	assertKeyset(t, "SessionDetail 顶层", raw, "summary", "timeline")
	if _, ok := raw["nodes"]; ok {
		t.Fatalf("nodes 无镜像事件应缺键（omitempty），实得: %v", raw["nodes"])
	}
	var summaryRaw map[string]json.RawMessage
	if err := json.Unmarshal(raw["summary"], &summaryRaw); err != nil {
		t.Fatal(err)
	}
	assertKeyset(t, "SessionSummary", summaryRaw,
		"id", "kind", "title", "owner", "archived", "unread", "needs_human",
		"last_activity", "preview", "members", "cards")
	var members []map[string]json.RawMessage
	if err := json.Unmarshal(summaryRaw["members"], &members); err != nil {
		t.Fatal(err)
	}
	if len(members) == 0 {
		t.Fatal("显式成员（群主+控制台成员）应在 members 投影")
	}
	assertKeyset(t, "SessionMember[0]", members[0], "identity", "kind", "status", "last_active")
	// last_active 虽带 omitempty tag，但 encoding/json 对 time.Time struct 的
	// omitempty 无效——零值照序列化。据此钉「无租约成员报零值时刻」的可证实
	// 语义（不伪造 now），plan 原 3 键断言与该序列化事实不符，见台账偏差清单。
	var lastActive string
	if err := json.Unmarshal(members[0]["last_active"], &lastActive); err != nil {
		t.Fatal(err)
	}
	if lastActive != "0001-01-01T00:00:00Z" {
		t.Fatalf("无租约成员 last_active 应为零值时刻: %q", lastActive)
	}
	var cards []map[string]json.RawMessage
	if err := json.Unmarshal(summaryRaw["cards"], &cards); err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Fatalf("详情 cards 应含进群卡恰一张: %v", cards)
	}
	assertKeyset(t, "SessionCard[0]", cards[0], "card_id", "title", "status")
	var cardID string
	if err := json.Unmarshal(cards[0]["card_id"], &cardID); err != nil {
		t.Fatal(err)
	}
	if cardID != card.ID {
		t.Fatalf("详情 cards 应含该卡: %q != %q", cardID, card.ID)
	}
	var seat string
	if err := json.Unmarshal(cards[0]["seat"], &seat); err == nil && seat != "" {
		t.Fatalf("进群≠配人：seat 应缺键或空（空座虚线），实得 %q", seat)
	}
	// breakdown 缺陷族 2：ErrNoRoom 对详情必须 404 且文案含会话 id。
	code, body = ledgerGet(t, env.testAgentdEnv, "/api/sessions/session:999")
	if code != 404 {
		t.Fatalf("不存在会话详情应 404: %d %s", code, body)
	}
	if !strings.Contains(body, "session:999") {
		t.Fatalf("404 文案应含会话 id（可行动错误）: %s", body)
	}
}

// assertKeyset 断言 decoded JSON 对象的键集与期望逐键一致（多键少键都红）。
func assertKeyset(t *testing.T, label string, obj map[string]json.RawMessage, want ...string) {
	t.Helper()
	if len(obj) != len(want) {
		t.Fatalf("%s 键数应 %d 实得 %d: %v", label, len(want), len(obj), obj)
	}
	for _, k := range want {
		if _, ok := obj[k]; !ok {
			t.Fatalf("%s 缺键 %q: %v", label, k, obj)
		}
	}
}

// TestSessionJoinCardEndpoint 锁：拉卡成功 + 详情含卡 + 幂等（条 10）+
// 已属他会话 409（ErrBadState，文案可行动）+ 不存在卡/会话 404（§2.2 映射表）。
func TestSessionJoinCardEndpoint(t *testing.T) {
	env := newRoomsEnv(t)
	session := mustConsoleSession(t, env, "拉卡场")
	card := seedCard(t, env, "进群卡")
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/sessions/"+session.ID+"/cards",
		fmt.Sprintf(`{"card":%q}`, card.ID))
	if code != 200 {
		t.Fatalf("拉卡进群: %d %s", code, body)
	}
	// 同会话重复 join 幂等（契约条 10）。
	if code, _ = ledgerPost(t, env.testAgentdEnv, "/api/sessions/"+session.ID+"/cards",
		fmt.Sprintf(`{"card":%q}`, card.ID)); code != 200 {
		t.Fatalf("同会话重复拉卡应幂等 200: %d", code)
	}
	// 已属其它会话 → 409 且文案含「已属会话」（可行动错误文案）。
	other := mustConsoleSession(t, env, "另一场")
	code, body = ledgerPost(t, env.testAgentdEnv, "/api/sessions/"+other.ID+"/cards",
		fmt.Sprintf(`{"card":%q}`, card.ID))
	if code != 409 {
		t.Fatalf("已属他会话应 409: %d %s", code, body)
	}
	if !strings.Contains(body, "已属会话") {
		t.Fatalf("409 文案应可行动（含「已属会话」）: %s", body)
	}
	// 不存在卡 → 404（mapSessionError 归一后走 §2.2 表）。
	code, _ = ledgerPost(t, env.testAgentdEnv, "/api/sessions/"+session.ID+"/cards", `{"card":"B99999"}`)
	if code != 404 {
		t.Fatalf("不存在卡应 404: %d", code)
	}
	// 不存在会话 → 404。
	code, _ = ledgerPost(t, env.testAgentdEnv, "/api/sessions/session:999/cards",
		fmt.Sprintf(`{"card":%q}`, card.ID))
	if code != 404 {
		t.Fatalf("不存在会话应 404: %d", code)
	}
}

// TestSessionLeaveCardEndpoint 锁：移出成功 + 详情不再含卡 + 幂等（条 11）+
// 不存在会话 404。
func TestSessionLeaveCardEndpoint(t *testing.T) {
	env := newRoomsEnv(t)
	session := mustConsoleSession(t, env, "移出场")
	card := seedCard(t, env, "移出卡")
	if code, body := ledgerPost(t, env.testAgentdEnv, "/api/sessions/"+session.ID+"/cards",
		fmt.Sprintf(`{"card":%q}`, card.ID)); code != 200 {
		t.Fatalf("拉卡进群: %d %s", code, body)
	}
	code, body := ledgerDelete(t, env.testAgentdEnv, "/api/sessions/"+session.ID+"/cards/"+card.ID, "")
	if code != 200 {
		t.Fatalf("移卡出群: %d %s", code, body)
	}
	// 幂等：不在会话内再移 → 200（契约条 11）。
	if code, _ = ledgerDelete(t, env.testAgentdEnv, "/api/sessions/"+session.ID+"/cards/"+card.ID, ""); code != 200 {
		t.Fatalf("重复移卡应幂等 200: %d", code)
	}
	// 不存在会话 → 404。
	if code, _ = ledgerDelete(t, env.testAgentdEnv, "/api/sessions/session:999/cards/"+card.ID, ""); code != 404 {
		t.Fatalf("不存在会话移卡应 404: %d", code)
	}
}

// TestSessionArchiveEndpoint 锁：归档 200 + 幂等（条 6）+ 详情 archived=true +
// 归档后发言 409（S1 语义经既有发言端点呈现）+ 归档后拉卡 409 且文案含「已归档」。
func TestSessionArchiveEndpoint(t *testing.T) {
	env := newRoomsEnv(t)
	session := mustConsoleSession(t, env, "归档场")
	card := seedCard(t, env, "归档卡")
	if code, body := ledgerPost(t, env.testAgentdEnv, "/api/sessions/"+session.ID+"/cards",
		fmt.Sprintf(`{"card":%q}`, card.ID)); code != 200 {
		t.Fatalf("拉卡进群: %d %s", code, body)
	}
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/sessions/"+session.ID+"/archive", `{}`)
	if code != 200 {
		t.Fatalf("归档: %d %s", code, body)
	}
	// 幂等：重复归档 200（Store 级短路，B358.1 拍板①）。
	if code, _ = ledgerPost(t, env.testAgentdEnv, "/api/sessions/"+session.ID+"/archive", `{}`); code != 200 {
		t.Fatalf("重复归档应幂等 200: %d", code)
	}
	// 详情 archived=true。
	var out struct {
		Summary proto.SessionSummary `json:"summary"`
	}
	code, body = ledgerGet(t, env.testAgentdEnv, "/api/sessions/"+session.ID)
	if code != 200 {
		t.Fatalf("归档后详情: %d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Summary.Archived {
		t.Fatalf("归档后 summary.archived 应 true: %+v", out.Summary)
	}
	// 归档后发言 → 409（ErrReadOnly 经既有 collabErr 映射）。
	if code, _ = ledgerPost(t, env.testAgentdEnv, "/api/rooms/"+session.ID+"/messages", `{"body":"x"}`); code != 409 {
		t.Fatalf("归档会话发言应 409: %d", code)
	}
	// 归档后拉卡 → 409 且文案含「已归档」（可行动错误文案，ErrBadState）。
	code, body = ledgerPost(t, env.testAgentdEnv, "/api/sessions/"+session.ID+"/cards",
		fmt.Sprintf(`{"card":%q}`, card.ID))
	if code != 409 {
		t.Fatalf("归档会话拉卡应 409: %d %s", code, body)
	}
	if !strings.Contains(body, "已归档") {
		t.Fatalf("409 文案应可行动（含「已归档」）: %s", body)
	}
}

// TestSessionMemberAddEndpoint 锁 B366 补员端点（POST /api/sessions/{id}/members）：
// ① 空体自加入成功（服务端权威：成员=服务端注入 actor，发言链 403→加入→200 打通
// ——岔口1 用户故事 1 的服务端半边）；② 幂等（重复 200）；③ body 塞 identity/actor
// 被忽略（成员恒服务端注入，user:mallory 不入成员列——roomsapi.go「成员标识服务端
// 注入，不经请求体」门禁反例）；④ body 塞非统一记法 identity 400（机器位 web: 与
// 裸名，validSessionOwner 口径）；⑤ 不存在会话 404 且文案含会话 id（缺陷族 2）；
// ⑥ 已归档 409 且文案含「已归档」。
func TestSessionMemberAddEndpoint(t *testing.T) {
	env := newRoomsEnv(t)
	// 直接经 HTTP 建会话，不经 mustConsoleSession：控制台成员必须不在成员集，
	// 才能先证 403 前态、再证加入后打通。
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/sessions",
		`{"title":"原地答场","owner":"user:tester"}`)
	if code != 200 {
		t.Fatalf("POST /api/sessions: %d %s", code, body)
	}
	var session proto.Session
	if err := json.Unmarshal([]byte(body), &session); err != nil {
		t.Fatal(err)
	}
	// 前态：控制台 actor 非成员，发言 403（ErrNotWriter，B358.6 岔口1 复现）。
	if code, body = ledgerPost(t, env.testAgentdEnv, "/api/rooms/"+session.ID+"/messages",
		`{"body":"加入前"}`); code != 403 {
		t.Fatalf("非成员发言应 403: %d %s", code, body)
	}
	// ① 空体自加入：成员=服务端注入 actor（webUserMember）。
	if code, body = ledgerPost(t, env.testAgentdEnv, "/api/sessions/"+session.ID+"/members", `{}`); code != 200 {
		t.Fatalf("空体自加入应 200: %d %s", code, body)
	}
	// ② 幂等：重复加入 200（Store 级短路）。
	if code, _ = ledgerPost(t, env.testAgentdEnv, "/api/sessions/"+session.ID+"/members", `{}`); code != 200 {
		t.Fatalf("重复自加入应幂等 200: %d", code)
	}
	// 发言链通：加入后同一 actor 发言 200（403→一键→发言成功的链）。
	if code, body = ledgerPost(t, env.testAgentdEnv, "/api/rooms/"+session.ID+"/messages",
		`{"body":"加入后"}`); code != 200 {
		t.Fatalf("加入后发言应 200: %d %s", code, body)
	}
	// ③ 塞 identity+actor 被忽略：有效统一记法 identity 也不入成员列（服务端
	// 权威），body actor 不改审计面；成员仍只有服务端注入的 webUserMember。
	if code, body = ledgerPost(t, env.testAgentdEnv, "/api/sessions/"+session.ID+"/members",
		`{"identity":"user:mallory","actor":"user:mallory"}`); code != 200 {
		t.Fatalf("塞有效 identity 应 200 且被忽略: %d %s", code, body)
	}
	var detail proto.SessionDetail
	code, body = ledgerGet(t, env.testAgentdEnv, "/api/sessions/"+session.ID)
	if code != 200 {
		t.Fatalf("加员后详情: %d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &detail); err != nil {
		t.Fatal(err)
	}
	var hasWeb, hasMallory bool
	for _, m := range detail.Summary.Members {
		switch m.Identity {
		case webUserMember:
			hasWeb = true
		case "user:mallory":
			hasMallory = true
		}
	}
	if !hasWeb {
		t.Fatalf("服务端注入成员 %q 应在成员列: %+v", webUserMember, detail.Summary.Members)
	}
	if hasMallory {
		t.Fatalf("body 塞 identity 被忽略（成员标识服务端注入，不经请求体）: %+v", detail.Summary.Members)
	}
	// ④ 前缀校验 400（拒绝半边）：机器位 web: 与裸名都不是成员统一记法。
	if code, _ = ledgerPost(t, env.testAgentdEnv, "/api/sessions/"+session.ID+"/members",
		`{"identity":"web:127.0.0.1"}`); code != 400 {
		t.Fatalf("塞机器位 web: identity 应 400: %d", code)
	}
	if code, _ = ledgerPost(t, env.testAgentdEnv, "/api/sessions/"+session.ID+"/members",
		`{"identity":"mallory"}`); code != 400 {
		t.Fatalf("塞裸名 identity 应 400: %d", code)
	}
	// ⑤ 不存在会话 → 404 且文案含会话 id（可行动错误，缺陷族 2）。
	code, body = ledgerPost(t, env.testAgentdEnv, "/api/sessions/session:999/members", `{}`)
	if code != 404 {
		t.Fatalf("不存在会话加员应 404: %d %s", code, body)
	}
	if !strings.Contains(body, "session:999") {
		t.Fatalf("404 文案应含会话 id（可行动错误）: %s", body)
	}
	// ⑥ 已归档 → 409 且文案含「已归档」（可行动错误）。
	if code, body = ledgerPost(t, env.testAgentdEnv, "/api/sessions",
		`{"title":"加员归档场","owner":"user:tester"}`); code != 200 {
		t.Fatalf("建归档场: %d %s", code, body)
	}
	var archived proto.Session
	if err := json.Unmarshal([]byte(body), &archived); err != nil {
		t.Fatal(err)
	}
	if code, body = ledgerPost(t, env.testAgentdEnv, "/api/sessions/"+archived.ID+"/archive", `{}`); code != 200 {
		t.Fatalf("归档: %d %s", code, body)
	}
	code, body = ledgerPost(t, env.testAgentdEnv, "/api/sessions/"+archived.ID+"/members", `{}`)
	if code != 409 {
		t.Fatalf("已归档会话加员应 409: %d %s", code, body)
	}
	if !strings.Contains(body, "已归档") {
		t.Fatalf("409 文案应可行动（含「已归档」）: %s", body)
	}
}

// TestSessionsEndpoints503WithoutLedger 锁 withRooms 守卫对会话端点同款降级
// （与 TestRoomsEndpoints503WithoutLedger 同族）。
func TestSessionsEndpoints503WithoutLedger(t *testing.T) {
	env := newTestAgentdEnv(t)
	if code, _ := ledgerGet(t, env, "/api/sessions"); code != 503 {
		t.Fatalf("未挂账本应 503: %d", code)
	}
}
