// 房间 HTTP 面与收件箱测试（B156.2 C6）：六端点、错误映射逐哨兵、收件箱三源、
// Watchers 翻转正控、破坏性不受限、集成冒烟。全部从 HTTP 进入（spec 测试接缝
// 清单 #1 的调用方侧 = gateway 控制面），调用链穿过 collab 入站 api 门面。
package agentd

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// webUserMember 是测试环境里控制台已认证主体的成员标识：httptest.NewServer 固定
// 监听 127.0.0.1，hostOnly(r.RemoteAddr) 恒为 "127.0.0.1"，故 roomUserActor 恒为
// "web:127.0.0.1"（hostOnly 见 hostguard.go:236）。消息 @ 该标识才进收件箱 mention 源。
const webUserMember = "web:127.0.0.1"

// newRoomsEnv 组装房间 HTTP 测试环境：真 SQLite 账本 + bug 工作流 + SetupAutomation
// （装配 collab.Service 与换绑端口）+ httptest 全链。
func newRoomsEnv(t *testing.T) *ledgerEnv {
	t.Helper()
	env := newNoPTYLedgerEnv(t)
	seedAgentdLedger(t, env.ledger, "bug")
	env.srv.SetupAutomation(env.ledger)
	return env
}

// inboxItems 拉取收件箱并解码 items。
func inboxItems(t *testing.T, env *ledgerEnv) []proto.InboxItem {
	t.Helper()
	var out struct {
		Items []proto.InboxItem `json:"items"`
	}
	code := env.getJSON(t, "/api/inbox", &out)
	if code != 200 {
		t.Fatalf("GET /api/inbox: %d", code)
	}
	return out.Items
}

func hasOrigin(items []proto.InboxItem, origin string) bool {
	for _, it := range items {
		if it.Origin == origin {
			return true
		}
	}
	return false
}

// itemKeys 序列化一条收件箱条目并返回 JSON 键集（金样本键集断言用）。
func itemKeys(t *testing.T, it proto.InboxItem) map[string]bool {
	t.Helper()
	raw, err := json.Marshal(it)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for k := range m {
		keys[k] = true
	}
	return keys
}

func TestRoomsListEndpoint(t *testing.T) {
	env := newRoomsEnv(t)
	card := seedCard(t, env, "卡A")
	if _, err := env.srv.rooms.Send(card.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "hi"}, "user:sy"); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Rooms []proto.RoomSummary `json:"rooms"`
	}
	code := env.getJSON(t, "/api/rooms?project=p", &out)
	if code != 200 {
		t.Fatalf("GET /api/rooms: %d", code)
	}
	found := false
	for _, r := range out.Rooms {
		if r.ID == card.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("卡房间应出现在列表: %+v", out.Rooms)
	}
}

func TestRoomsListUnreadAndAttachProjection(t *testing.T) {
	env := newRoomsEnv(t)
	card := seedCard(t, env, "可挂账卡")
	if _, err := env.srv.rooms.Send(card.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "一"}, "user:sy"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.srv.rooms.Send(card.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "二"}, "user:sy"); err != nil {
		t.Fatal(err)
	}
	if err := env.st.CreateTask(&proto.Task{ID: "T1", RepoPath: "/repo", WorkDir: "/work/B1"}); err != nil {
		t.Fatal(err)
	}
	if err := env.ledger.LinkTask(card.ID, "", "T1", ledger.PurposeImplement, "test"); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Rooms []proto.RoomSummary `json:"rooms"`
	}
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms")
	if code != 200 {
		t.Fatalf("GET /api/rooms: %d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	var found *proto.RoomSummary
	for i := range out.Rooms {
		if out.Rooms[i].ID == card.ID {
			found = &out.Rooms[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("卡房间应出现在列表: %+v", out.Rooms)
	}
	if found.Unread != 2 {
		t.Fatalf("两条未读消息应投影 unread=2: %+v", *found)
	}
	if found.Attach == nil || found.Attach.Target != "" || found.Attach.TaskID != "T1" ||
		found.Attach.WorkDir != "/work/B1" || found.Attach.Command != "handoff attach T1" {
		t.Fatalf("本机挂账 attach 投影错误: %+v", found.Attach)
	}
	globalFound := false
	for _, room := range out.Rooms {
		if room.Kind == "global" {
			globalFound = true
			if room.Unread < 0 {
				t.Fatalf("global unread 必须在线: %+v", room)
			}
		}
	}
	if !globalFound {
		t.Fatal("global 房间应出现在列表")
	}

	seq, err := env.srv.rooms.Send(card.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "三"}, "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	code, body = ledgerPost(t, env.testAgentdEnv, "/api/rooms/"+card.ID+"/read", fmt.Sprintf(`{"upto_seq":%d}`, seq))
	if code != 200 {
		t.Fatalf("POST /read: %d %s", code, body)
	}
	code, body = ledgerGet(t, env.testAgentdEnv, "/api/rooms")
	if code != 200 {
		t.Fatalf("GET /api/rooms after read: %d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	for _, room := range out.Rooms {
		if room.ID == card.ID && room.Unread != 0 {
			t.Fatalf("已读后 unread 应为 0: %+v", room)
		}
	}
}

func TestRoomsListWithoutAttachmentKeepsAttachMissing(t *testing.T) {
	env := newRoomsEnv(t)
	card := seedCard(t, env, "无挂账卡")
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms")
	if code != 200 {
		t.Fatalf("GET /api/rooms: %d %s", code, body)
	}
	var out struct {
		Rooms []json.RawMessage `json:"rooms"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	for _, raw := range out.Rooms {
		var room struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &room); err != nil {
			t.Fatal(err)
		}
		if room.ID == card.ID && strings.Contains(string(raw), `"attach"`) {
			t.Fatalf("无挂账卡不应出现 attach: %s", raw)
		}
	}
}

func TestRoomMessagesEndpoint(t *testing.T) {
	// 写侧受守、读侧宽容的正面锚（协调者裁决①）：存在的房间、已落过 N 条
	// room_message → GET 返回 200 且恰好那 N 条（条数与内容都断言，不只断言
	// 非空）。同一支测试里的这条正面断言让「History 恒空」的假实现当场翻红。
	env := newRoomsEnv(t)
	card := seedCard(t, env, "卡B")
	first, err := env.srv.rooms.Send(card.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "第一条"}, "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	second, err := env.srv.rooms.Send(card.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "第二条"}, "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Messages []proto.LedgerEvent `json:"messages"`
	}
	code := env.getJSON(t, "/api/rooms/"+card.ID+"/messages?limit=10", &out)
	if code != 200 {
		t.Fatalf("GET messages: %d", code)
	}
	if len(out.Messages) != 2 {
		t.Fatalf("历史应恰好两条: %+v", out.Messages)
	}
	seqs := map[int64]bool{}
	bodies := map[string]bool{}
	for _, ev := range out.Messages {
		seqs[ev.Seq] = true
		var msg proto.RoomMessage
		if err := json.Unmarshal(ev.Payload, &msg); err != nil {
			t.Fatal(err)
		}
		bodies[msg.Body] = true
	}
	if !seqs[first] || !seqs[second] {
		t.Fatalf("两条消息的 seq 都应返回: %d %d (%v)", first, second, seqs)
	}
	if !bodies["第一条"] || !bodies["第二条"] {
		t.Fatalf("两条消息正文都应返回: %v", bodies)
	}
	// 读侧宽容的否定锚（同一支测试、同一夹具）：不存在的房间 → 200 且零条。
	var empty struct {
		Messages []proto.LedgerEvent `json:"messages"`
	}
	code = env.getJSON(t, "/api/rooms/NO-SUCH/messages", &empty)
	if code != 200 {
		t.Fatalf("GET 不存在房间应 200: %d", code)
	}
	if len(empty.Messages) != 0 {
		t.Fatalf("不存在房间历史应零条: %+v", empty.Messages)
	}
}

func TestRoomSendEndpoint(t *testing.T) {
	env := newRoomsEnv(t)
	card := seedCard(t, env, "卡C")
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/rooms/"+card.ID+"/messages", `{"body":"你好"}`)
	if code != 200 {
		t.Fatalf("POST messages: %d %s", code, body)
	}
	events, err := env.ledger.EventsFromAsc([]string{card.ID}, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range events {
		if ev.Type != ledger.EvRoomMessage {
			continue
		}
		var msg proto.RoomMessage
		if err := json.Unmarshal(ev.Payload, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Body == "你好" {
			found = true
			if !strings.HasPrefix(ev.Actor, "web:") {
				t.Fatalf("actor 应服务端注入（不经请求体）: %q", ev.Actor)
			}
		}
	}
	if !found {
		t.Fatalf("消息未落账: %+v", events)
	}
}

func TestRoomReadEndpoint(t *testing.T) {
	env := newRoomsEnv(t)
	card := seedCard(t, env, "卡D")
	seq, err := env.srv.rooms.Send(card.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "x"}, "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/rooms/"+card.ID+"/read",
		fmt.Sprintf(`{"upto_seq":%d}`, seq))
	if code != 200 {
		t.Fatalf("POST read: %d %s", code, body)
	}
	// 读尽后未读应 0（MarkRead 到当前最大 seq → 水位语义清空）
	if n, err := env.srv.rooms.Unread(webUserMember, card.ID); err != nil || n != 0 {
		t.Fatalf("读尽后未读应 0: %v %d", err, n)
	}
}

func TestRoomSendErrMapping(t *testing.T) {
	env := newRoomsEnv(t)
	// 不存在的房间 → 400（Send 的 ErrNoRoom 契约义务，写侧受守）
	if code, _ := ledgerPost(t, env.testAgentdEnv, "/api/rooms/NO-SUCH/messages", `{"body":"x"}`); code != 400 {
		t.Fatalf("不存在房间应 400: %d", code)
	}
	// 绑定者本人以 user 发言 → 403（ErrNotWriter）
	card := seedCard(t, env, "卡E")
	if err := env.ledger.ClaimCardAs(card.ID, webUserMember, ""); err != nil {
		t.Fatal(err)
	}
	if code, _ := ledgerPost(t, env.testAgentdEnv, "/api/rooms/"+card.ID+"/messages", `{"body":"x"}`); code != 403 {
		t.Fatalf("绑定者发言应 403: %d", code)
	}
	// 终态卡房间 → 409（ErrReadOnly）
	parent := seedCard(t, env, "父")
	child := seedChildCard(t, env, parent.ID, "子")
	if err := env.ledger.MoveCard(child.ID, ledger.StatusDone, "", "test"); err != nil {
		t.Fatal(err)
	}
	if code, _ := ledgerPost(t, env.testAgentdEnv, "/api/rooms/"+child.ID+"/messages", `{"body":"x"}`); code != 409 {
		t.Fatalf("终态房间应 409: %d", code)
	}
	// 空正文 → 400（handler 卫生检查）
	if code, _ := ledgerPost(t, env.testAgentdEnv, "/api/rooms/"+parent.ID+"/messages", `{"body":"  "}`); code != 400 {
		t.Fatalf("空正文应 400: %d", code)
	}
}

func TestCardRebindCASConflict(t *testing.T) {
	env := newRoomsEnv(t)
	card := seedCard(t, env, "卡F")
	if err := env.ledger.ClaimCardAs(card.ID, "cli:a@h", "desktop"); err != nil {
		t.Fatal(err)
	}
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/rebind",
		`{"to_session":"cli:b@h","carrier":"cli","expect":"WRONG"}`)
	if code != 409 {
		t.Fatalf("expect 不符应 409: %d %s", code, body)
	}
	// expect 正确 → 200 且换绑生效
	code, body = ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/rebind",
		`{"to_session":"cli:b@h","carrier":"cli","expect":"cli:a@h"}`)
	if code != 200 {
		t.Fatalf("换绑应 200: %d %s", code, body)
	}
	card2, err := env.ledger.GetCard(card.ID)
	if err != nil {
		t.Fatal(err)
	}
	if card2.DriverSession != "cli:b@h" {
		t.Fatalf("换绑未生效: %+v", card2)
	}
}

func TestRoomsEndpoints503WithoutLedger(t *testing.T) {
	env := newTestAgentdEnv(t)
	code, _ := ledgerGet(t, env, "/api/rooms")
	if code != 503 {
		t.Fatalf("未挂账本应 503: %d", code)
	}
}

func TestInboxThreeSources(t *testing.T) {
	env := newRoomsEnv(t)
	// decision 源：卡级 + 项目级 open 裁决
	card := seedCard(t, env, "卡G")
	if _, err := env.ledger.OpenDecision(card.ID, "一句话：契约语义冲突", []string{"a", "b"}, "coord"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.ledger.OpenDecision("", "项目级裁决", nil, "coord"); err != nil {
		t.Fatal(err)
	}
	// mention 源：群房间 @ 用户
	if _, err := env.srv.rooms.Send("project:p", proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "改动影响 B145", Mentions: []string{webUserMember}}, "user:sy"); err != nil {
		t.Fatal(err)
	}
	// ticket 源：等待人工任务上的未答复工单
	mustCreateTask(t, env.st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake", State: proto.TaskStateWaitingAnswer})
	if _, err := env.st.CreateTicket(&proto.Ticket{
		ID: "t1:p1", TaskID: "t1", Kind: "gate",
		Request: json.RawMessage(`{"kind":"gate","permission":"x"}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	items := inboxItems(t, env)
	for _, origin := range []string{proto.InboxOriginDecision, proto.InboxOriginTicket, proto.InboxOriginMention} {
		if !hasOrigin(items, origin) {
			t.Fatalf("收件箱应含 %s 源: %+v", origin, items)
		}
	}
}

func TestInboxDecisionOpenAllIncludingProjectLevel(t *testing.T) {
	env := newRoomsEnv(t)
	card := seedCard(t, env, "卡H")
	if _, err := env.ledger.OpenDecision(card.ID, "卡级裁决", nil, "coord"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.ledger.OpenDecision("", "项目级裁决", nil, "coord"); err != nil {
		t.Fatal(err)
	}
	items := inboxItems(t, env)
	decCount := 0
	hasProjectLevel := false
	for _, it := range items {
		if it.Origin != proto.InboxOriginDecision {
			continue
		}
		decCount++
		if it.CardID == "" {
			hasProjectLevel = true
		}
	}
	if decCount != 2 || !hasProjectLevel {
		t.Fatalf("decision 源应含 open 全量（含项目级）: %+v", items)
	}
}

func TestInboxTicketExcludesDrivenTask(t *testing.T) {
	env := newRoomsEnv(t)
	mustCreateTask(t, env.st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake", State: proto.TaskStateWaitingAnswer})
	if _, err := env.st.CreateTicket(&proto.Ticket{
		ID: "t1:p1", TaskID: "t1", Kind: "gate",
		Request: json.RawMessage(`{"kind":"gate"}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// 无人驱动：上浮
	if items := inboxItems(t, env); !hasOrigin(items, proto.InboxOriginTicket) {
		t.Fatalf("无人驱动应上浮工单: %+v", items)
	}
	// 订阅翻转 Watchers=1：排除（内建正控——真实 hub 订阅，非 mock 返回值）
	ch, unsub := env.srv.hub.Subscribe("t1")
	defer unsub()
	_ = ch
	if items := inboxItems(t, env); hasOrigin(items, proto.InboxOriginTicket) {
		t.Fatalf("Watchers>0 应排除工单: %+v", items)
	}
	// 退订回 0：重新上浮
	unsub()
	if items := inboxItems(t, env); !hasOrigin(items, proto.InboxOriginTicket) {
		t.Fatalf("退订后应重新上浮工单: %+v", items)
	}
}

func TestInboxDestructiveTicketFloatsWithWatchers(t *testing.T) {
	env := newRoomsEnv(t)
	mustCreateTask(t, env.st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake", State: proto.TaskStateWaitingAnswer})
	if _, err := env.st.CreateTicket(&proto.Ticket{
		ID: "t1:p1", TaskID: "t1", Kind: "gate",
		Request: json.RawMessage(`{"kind":"gate"}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// 审批者升级信号（D-destructive，台账 L10）：approver_decision escalate
	if _, err := env.st.AppendEvent("t1", proto.EventTypeApproverDecision, approverDecisionPayload{
		TicketID: "t1:p1", Decision: "escalate"}); err != nil {
		t.Fatal(err)
	}
	ch, unsub := env.srv.hub.Subscribe("t1")
	defer unsub()
	_ = ch
	if items := inboxItems(t, env); !hasOrigin(items, proto.InboxOriginTicket) {
		t.Fatalf("破坏性工单不受 Watchers 限制应上浮: %+v", items)
	}
}

func TestInboxMentionSource(t *testing.T) {
	env := newRoomsEnv(t)
	seq, err := env.srv.rooms.Send("project:p", proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "改动影响 B145", Mentions: []string{webUserMember}}, "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	items := inboxItems(t, env)
	var mention *proto.InboxItem
	for i := range items {
		if items[i].Origin == proto.InboxOriginMention {
			mention = &items[i]
		}
	}
	if mention == nil {
		t.Fatal("mention 源应含未消费提及")
	}
	if mention.RefID != strconv.FormatInt(seq, 10) {
		t.Fatalf("mention RefID 应为消息 seq 十进制串: %q", mention.RefID)
	}
	// 消费后消失
	if err := env.srv.rooms.Consume(seq, webUserMember); err != nil {
		t.Fatal(err)
	}
	if items := inboxItems(t, env); hasOrigin(items, proto.InboxOriginMention) {
		t.Fatalf("消费后 mention 应消失: %+v", items)
	}
}

func TestInboxRefIDShapes(t *testing.T) {
	env := newRoomsEnv(t)
	card := seedCard(t, env, "卡I")
	dec, err := env.ledger.OpenDecision(card.ID, "一句话：X", nil, "coord")
	if err != nil {
		t.Fatal(err)
	}
	seq, err := env.srv.rooms.Send("project:p", proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "b", Mentions: []string{webUserMember}}, "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	mustCreateTask(t, env.st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake", State: proto.TaskStateWaitingAnswer})
	if _, err := env.st.CreateTicket(&proto.Ticket{
		ID: "tkt-42", TaskID: "t1", Kind: "ask",
		Request: json.RawMessage(`{"kind":"ask","question":"q"}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	items := inboxItems(t, env)
	for _, it := range items {
		switch it.Origin {
		case proto.InboxOriginDecision:
			if it.RefID != strconv.FormatInt(dec.ID, 10) {
				t.Fatalf("decision RefID 应为十进制 id 串: %q", it.RefID)
			}
		case proto.InboxOriginTicket:
			if it.RefID != "tkt-42" {
				t.Fatalf("ticket RefID 应为 ticket id 原文: %q", it.RefID)
			}
		case proto.InboxOriginMention:
			if it.RefID != strconv.FormatInt(seq, 10) {
				t.Fatalf("mention RefID 应为消息 seq 十进制串: %q", it.RefID)
			}
		}
	}
}

func TestInboxGoldenKeyShapes(t *testing.T) {
	env := newRoomsEnv(t)
	card := seedCard(t, env, "卡J")
	if _, err := env.ledger.OpenDecision(card.ID, "一句话：X", nil, "coord"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.srv.rooms.Send("project:p", proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "b", Mentions: []string{webUserMember}}, "user:sy"); err != nil {
		t.Fatal(err)
	}
	mustCreateTask(t, env.st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake", State: proto.TaskStateWaitingAnswer})
	if _, err := env.st.CreateTicket(&proto.Ticket{
		ID: "tkt-42", TaskID: "t1", Kind: "gate",
		Request: json.RawMessage(`{"kind":"gate"}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// 金样本键集断言（contract_fixture_test.go 同款）：decision 带 card_id+payload；
	// ticket/mention omitempty 省略 card_id。
	for _, it := range inboxItems(t, env) {
		keys := itemKeys(t, it)
		if it.Origin == proto.InboxOriginDecision {
			if !keys["card_id"] || !keys["payload"] {
				t.Fatalf("decision 条目应带 card_id+payload: %v", keys)
			}
		} else if keys["card_id"] {
			t.Fatalf("空 card_id 必须省略: %v %+v", keys, it)
		}
	}
}

func TestInboxIntegrationSmoke(t *testing.T) {
	// 澄清三：httptest 一发穿 handler→真 SQLite→响应解码回 InboxItem/RoomSummary，
	// 与 testdata/RoomsFixture.json 孪生一致（键集形状，非逐字节值）。
	env := newRoomsEnv(t)
	card := seedCard(t, env, "卡K")
	if _, err := env.ledger.OpenDecision(card.ID, "一句话：契约语义冲突", []string{"a", "b"}, "coord"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.srv.rooms.Send("project:p", proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "b", Mentions: []string{webUserMember}}, "user:sy"); err != nil {
		t.Fatal(err)
	}
	mustCreateTask(t, env.st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake", State: proto.TaskStateWaitingAnswer})
	if _, err := env.st.CreateTicket(&proto.Ticket{
		ID: "tkt-42", TaskID: "t1", Kind: "gate",
		Request: json.RawMessage(`{"kind":"gate"}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	items := inboxItems(t, env)
	if len(items) < 3 {
		t.Fatalf("集成冒烟三源应齐: %+v", items)
	}
	for _, it := range items {
		keys := itemKeys(t, it)
		if !keys["origin"] || !keys["title"] || !keys["ref_id"] {
			t.Fatalf("InboxItem 基础键缺一: %v", keys)
		}
	}
	var rooms struct {
		Rooms []proto.RoomSummary `json:"rooms"`
	}
	if code := env.getJSON(t, "/api/rooms", &rooms); code != 200 {
		t.Fatalf("GET /api/rooms: %d", code)
	}
	if len(rooms.Rooms) == 0 {
		t.Fatal("RoomSummary 列表应非空")
	}
}
