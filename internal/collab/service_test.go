// 协作房间域入站门面的直通竖切与执法矩阵测试（B156.2 契约 §3.3/§4）。
//
// 竖切（重档法定步骤）：一次真实调用 Send(user, 卡房间) 穿过 Service →
// client.LedgerClient 接口 → internal/ledger/api.Facade → 真 SQLite
// ledger.Store，落 card_events 后由 History 读回。测试钉在主缝上（库缝
// 形态 = 夹具直调），这是 Ticket 0「越过空壳的可观测行为须有能变红的测试」
// 的正当出口。
//
// 执法矩阵（欠账 #1）：Send 的协调者类/relay/user 书写者校验、并入只读、
// 换绑剥权，与 Pointer 实现，全部从入站门面（Service.Send/Service.Pointer）
// 断言——缝#1 是唯一法定入口，规则实现（room 子包）不设独立单测。
//
// 本文件的 import internal/ledger(/api) 仅存在于 _test.go：图边采集排除
// 测试文件（charter/graph edgegate.go:190），不构成 d_collab→d_ledger 生产边。
package collab

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/collab/room"
	"github.com/Xsxdot/handoff/internal/ledger"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/proto"
)

// newFixture 起真 SQLite 账本并按组装点同形绑定 Facade。
func newFixture(t *testing.T) (*Service, *ledger.Store) {
	t.Helper()
	st, err := ledger.Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.PutWorkflow("bug", ledger.WorkflowDef{Nodes: []ledger.NodeDef{
		{Name: ledger.StatusTodo, Next: ledger.StatusDoing},
		{Name: ledger.StatusDoing, Next: ledger.StatusDone},
		{Name: ledger.StatusDone},
	}}); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	return New(ledgerapi.New(st)), st
}

func mustCard(t *testing.T, s *Service, st *ledger.Store, title string) ledger.Card {
	t.Helper()
	card, err := st.CreateCard(ledger.NewCard{Title: title, Project: "handoff", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	return card
}

// mustCardWithParent 建一张直接父卡为 parentID 的卡。
func mustCardWithParent(t *testing.T, s *Service, st *ledger.Store, title, parentID string) ledger.Card {
	t.Helper()
	card, err := st.CreateCard(ledger.NewCard{Title: title, Project: "handoff", Workflow: "bug", Parent: parentID, Actor: "test"})
	if err != nil {
		t.Fatalf("建子卡: %v", err)
	}
	return card
}

// mustBind 用规范 coordinate 席位给测试卡绑定协调者会话。
func mustBind(t *testing.T, st *ledger.Store, id, owner string) {
	t.Helper()
	if err := st.BindSeat(id, owner, proto.SeatSourceCoordinate); err != nil {
		t.Fatalf("绑定 %s→%s: %v", id, owner, err)
	}
}

// mustAnyCard 建一张默认标题的卡。
func mustAnyCard(t *testing.T, s *Service, st *ledger.Store) ledger.Card {
	return mustCard(t, s, st, "竖切夹具卡")
}

// TestSendUserMessageVerticalSlice 直通竖切（B358 锚点翻转后：会话房间是唯一
// 活着的发言面）：一次真实调用 Send(session, user) 穿过 Service →
// client.LedgerClient → internal/ledger/api.Facade → 真 SQLite ledger.Store，
// 落无卡事件后由 History 读回。载荷 roundtrip 断言保留（序列化边界既有金样本
// 语义不因迁移丢失）。
func TestSendUserMessageVerticalSlice(t *testing.T) {
	svc, st := newFixture(t)
	sid := mustSession(t, svc, st, "竖切会话")

	msg := proto.RoomMessage{Room: sid, Kind: proto.RoomMsgUser,
		Body: "先停一下，验收判据我想改", Refs: []string{"docs/x.md"}, Mentions: []string{"B156"}}
	seq, err := svc.Send(sid, msg, "user:sy")
	if err != nil {
		t.Fatalf("竖切发送失败: %v", err)
	}
	if seq <= 0 {
		t.Fatalf("seq 必须为正: %d", seq)
	}
	events, err := st.EventsFromAsc([]string{}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var found *ledger.Event
	for i := range events {
		if events[i].Seq == seq {
			found = &events[i]
		}
	}
	if found == nil {
		t.Fatalf("事件流里没有该 seq: %d", seq)
	}
	if found.CardID != "" || found.Actor != "user:sy" {
		t.Fatalf("会话消息必须是无卡事件且 actor 落账: card=%q actor=%q", found.CardID, found.Actor)
	}
	var back proto.RoomMessage
	if err := json.Unmarshal(found.Payload, &back); err != nil {
		t.Fatal(err)
	}
	if back.Body != msg.Body || back.Kind != proto.RoomMsgUser || len(back.Refs) != 1 {
		t.Fatalf("载荷往返不一致: %+v", back)
	}
	history, err := svc.History(sid, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Seq != seq {
		t.Fatalf("History 读回与发送不符: %+v", history)
	}
}

// TestSendGroupMessageLandsAsCardlessEvent 会话消息走无卡事件（contract 条 33；
// 原 project 房间夹具随旧房间归档迁移到会话房间）。
func TestSendGroupMessageLandsAsCardlessEvent(t *testing.T) {
	svc, st := newFixture(t)
	sid := mustSession(t, svc, st, "无卡事件会话")
	seq, err := svc.Send(sid, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "全局通知"}, "user:sy")
	if err != nil {
		t.Fatalf("会话房间发送失败: %v", err)
	}
	events, err := st.EventsFromAsc([]string{}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Seq == seq && ev.CardID != "" {
			t.Fatalf("会话级消息必须是无卡事件: %+v", ev)
		}
	}
}

func TestSendRejectsPointerViaSend(t *testing.T) {
	svc, _ := newFixture(t)
	if _, err := svc.Send("global", proto.RoomMessage{Kind: proto.RoomMsgPointer, Body: "指针"}, "system:pointer"); err != ErrKindNotAllowed {
		t.Fatalf("pointer 经 Send 必须拒收，got %v", err)
	}
}

func TestSendRejectsUnknownRoom(t *testing.T) {
	svc, _ := newFixture(t)
	if _, err := svc.Send("B99999", proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "x"}, "user:sy"); err != ErrNoRoom {
		t.Fatalf("不存在房间必须返回 ErrNoRoom，got %v", err)
	}
}

// TestSendRejectsEmptyActor 空 actor 在会话房间被拒（原卡房间夹具随旧房间
// 归档迁移；空 actor 是书写者执法的第一道，Task 3 终形同样覆盖）。
func TestSendRejectsEmptyActor(t *testing.T) {
	svc, st := newFixture(t)
	sid := mustSession(t, svc, st, "空 actor 会话")
	if _, err := svc.Send(sid, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "x"}, ""); err != ErrNotWriter {
		t.Fatalf("空 actor 必须返回 ErrNotWriter，got %v", err)
	}
}

func TestSendRejectsTerminalCardRoom(t *testing.T) {
	svc, st := newFixture(t)
	card := mustAnyCard(t, svc, st)
	if err := st.CloseCard(card.ID, ledger.CloseCancelled, "test"); err != nil {
		t.Fatalf("置终态: %v", err)
	}
	if _, err := svc.Send(card.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "x"}, "user:sy"); err != ErrReadOnly {
		t.Fatalf("终态卡房间必须只读，got %v", err)
	}
}

// TestSendRejectsMergedCardRoom 并入承载卡的房间（merged_into 非空）Send →
// ErrReadOnly。
func TestSendRejectsMergedCardRoom(t *testing.T) {
	svc, st := newFixture(t)
	carrier := mustCard(t, svc, st, "承载卡")
	member := mustCard(t, svc, st, "并入卡")
	if err := st.MergeCards([]string{member.ID}, carrier.ID, "test"); err != nil {
		t.Fatalf("合并: %v", err)
	}
	if _, err := svc.Send(member.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "x"}, "user:sy"); err != ErrReadOnly {
		t.Fatalf("并入房间必须 ErrReadOnly，got %v", err)
	}
}

// TestPointerWritesPointerMessage Pointer 写入后读回该消息，断言
// Kind==pointer && BySystem==true、正文与 seq 落账一致（本卡新增判据一）。
func TestPointerWritesPointerMessage(t *testing.T) {
	svc, st := newFixture(t)
	card := mustAnyCard(t, svc, st)
	seq, err := svc.Pointer(card.ID, proto.RoomMessage{Body: "spec 已定稿"})
	if err != nil {
		t.Fatalf("Pointer: %v", err)
	}
	if seq <= 0 {
		t.Fatalf("Pointer seq 必须为正，got %d", seq)
	}
	events, err := st.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var found *ledger.Event
	for i := range events {
		if events[i].Seq == seq {
			found = &events[i]
		}
	}
	if found == nil {
		t.Fatalf("没找到 pointer 落账行")
	}
	if found.CardID != card.ID || found.Actor != "system:pointer" {
		t.Fatalf("pointer 行身份漂移: card=%q actor=%q", found.CardID, found.Actor)
	}
	var back proto.RoomMessage
	if err := json.Unmarshal(found.Payload, &back); err != nil {
		t.Fatal(err)
	}
	if back.Kind != proto.RoomMsgPointer || !back.BySystem {
		t.Fatalf("Pointer 置位失效: kind=%q by_system=%v", back.Kind, back.BySystem)
	}
	if back.Body != "spec 已定稿" || back.Room != card.ID {
		t.Fatalf("pointer 载荷漂移: body=%q room=%q", back.Body, back.Room)
	}
	hist, err := svc.History(card.ID, 0, 0)
	if err != nil || len(hist) != 1 || hist[0].Seq != seq {
		t.Fatalf("History 读回 pointer 失败: %v %d", err, len(hist))
	}
}

// TestPointerOverridesCallerKindAndBySystem 置位在 Pointer 内部：调用方传入
// 的 Kind/BySystem 被覆盖为 pointer/true（本卡新增判据一的反面形）。
func TestPointerOverridesCallerKindAndBySystem(t *testing.T) {
	svc, st := newFixture(t)
	card := mustAnyCard(t, svc, st)
	seq, err := svc.Pointer(card.ID, proto.RoomMessage{Kind: proto.RoomMsgEscalation, Body: "x", BySystem: false})
	if err != nil || seq <= 0 {
		t.Fatalf("Pointer: %v seq=%d", err, seq)
	}
	events, err := st.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var back proto.RoomMessage
	for i := range events {
		if events[i].Seq == seq {
			if err := json.Unmarshal(events[i].Payload, &back); err != nil {
				t.Fatal(err)
			}
		}
	}
	if back.Kind != proto.RoomMsgPointer || !back.BySystem {
		t.Fatalf("Pointer 必须自置 kind/by_system: kind=%q by_system=%v", back.Kind, back.BySystem)
	}
}

// TestPointerRejectsReadOnlyRoom 终态或并入房间调 Pointer → ErrReadOnly
// （本卡新增判据二）。
func TestPointerRejectsReadOnlyRoom(t *testing.T) {
	svc, st := newFixture(t)
	term := mustAnyCard(t, svc, st)
	if err := st.CloseCard(term.ID, ledger.CloseCancelled, "test"); err != nil {
		t.Fatalf("置终态: %v", err)
	}
	if _, err := svc.Pointer(term.ID, proto.RoomMessage{Body: "x"}); err != ErrReadOnly {
		t.Fatalf("终态房间 Pointer 必须 ErrReadOnly，got %v", err)
	}
	carrier := mustCard(t, svc, st, "承载卡")
	member := mustCard(t, svc, st, "并入卡")
	if err := st.MergeCards([]string{member.ID}, carrier.ID, "test"); err != nil {
		t.Fatalf("合并: %v", err)
	}
	if _, err := svc.Pointer(member.ID, proto.RoomMessage{Body: "x"}); err != ErrReadOnly {
		t.Fatalf("并入房间 Pointer 必须 ErrReadOnly，got %v", err)
	}
}

// TestPointerRejectsUnknownRoom Pointer 解析不到房间 → ErrNoRoom。
func TestPointerRejectsUnknownRoom(t *testing.T) {
	svc, _ := newFixture(t)
	if _, err := svc.Pointer("B99999", proto.RoomMessage{Body: "x"}); err != ErrNoRoom {
		t.Fatalf("未知房间 Pointer 必须 ErrNoRoom，got %v", err)
	}
}

// TestHistoryFiltersNonRoomEvents 旧房间读面对质（B358：旧房间发言入口已死，
// 指针行是卡房间唯一合法的 room_message 生产者——用 Pointer 夹具，同时锁
// 「room list/History 读面对旧房间仍可读」）。History 必须滤掉非 room_message
// 事件（评论）。
func TestHistoryFiltersNonRoomEvents(t *testing.T) {
	svc, st := newFixture(t)
	card := mustAnyCard(t, svc, st)
	if _, err := st.AddComment(card.ID, "普通评论", "普通", "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Pointer(card.ID, proto.RoomMessage{Body: "房内指针行"}); err != nil {
		t.Fatalf("卡房间 Pointer 应可用: %v", err)
	}
	history, err := svc.History(card.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("History 必须滤掉非 room_message 事件: %d 条", len(history))
	}
}

// TestHistoryReturnsNewestWindow 钉住「升序截尾」（B274 真机教训）：无 before
// 时取最新 limit 条。Pointer 夹具（旧房间读面对质）。
func TestHistoryReturnsNewestWindow(t *testing.T) {
	svc, st := newFixture(t)
	card := mustAnyCard(t, svc, st)
	bodyOf := func(ev proto.LedgerEvent) string {
		var msg proto.RoomMessage
		if err := json.Unmarshal(ev.Payload, &msg); err != nil {
			t.Fatalf("解码 History 载荷: %v", err)
		}
		return msg.Body
	}
	for i := 1; i <= 5; i++ {
		if _, err := svc.Pointer(card.ID, proto.RoomMessage{Body: "m" + itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := svc.History(card.ID, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("无 before 应截最新 2 条，got %d", len(got))
	}
	if bodyOf(got[0]) != "m4" || bodyOf(got[1]) != "m5" {
		t.Fatalf("应升序返回最新两条 m4,m5，got %q %q", bodyOf(got[0]), bodyOf(got[1]))
	}
	older, err := svc.History(card.ID, got[0].Seq, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(older) != 2 {
		t.Fatalf("before=最新窗首 seq 应再取 2 条，got %d", len(older))
	}
	if bodyOf(older[0]) != "m2" || bodyOf(older[1]) != "m3" {
		t.Fatalf("before 上界应取更早的 m2,m3，got %q %q", bodyOf(older[0]), bodyOf(older[1]))
	}
}

func itoa(n int) string { return string(rune('0' + n)) }

// TestMentionsFiltersByMember 提及源按成员过滤（夹具迁会话房间——会话是
// 唯一活着的发言面）。
func TestMentionsFiltersByMember(t *testing.T) {
	svc, st := newFixture(t)
	sid := mustSession(t, svc, st, "提及过滤会话")
	if _, err := svc.Send(sid,
		proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@B145 看", Mentions: []string{"B145"}}, "user:sy"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Send(sid,
		proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "无提及"}, "user:sy"); err != nil {
		t.Fatal(err)
	}
	hit, err := svc.Mentions("B145", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hit) != 1 {
		t.Fatalf("@提及过滤失准: %d 条", len(hit))
	}
	other, err := svc.Mentions("B999", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("未提及成员不得命中: %d 条", len(other))
	}
}

// TestRoomEventTypeLiteralMatchesLedger 钉住 room 侧字面量与账本词表的
// 等式；测试文件不计图边，可同时看见两侧（门面禁令只约束生产代码）。
func TestRoomEventTypeLiteralMatchesLedger(t *testing.T) {
	if room.RoomEventType != ledger.EvRoomMessage {
		t.Fatalf("房间事件类型字面量漂移: %q != %q", room.RoomEventType, ledger.EvRoomMessage)
	}
}

// TestRoomStatusLiteralMatchesLedger 钉住 room.IsTerminalStatus 的终态字面量
// 与账本 StatusDone/StatusClosed 的等式（内部锁，理由见 §7）。
func TestRoomStatusLiteralMatchesLedger(t *testing.T) {
	if !room.IsTerminalStatus(ledger.StatusDone) || !room.IsTerminalStatus(ledger.StatusClosed) {
		t.Fatalf("终态字面量漂移: IsTerminalStatus(%q)=%v, IsTerminalStatus(%q)=%v",
			ledger.StatusDone, room.IsTerminalStatus(ledger.StatusDone),
			ledger.StatusClosed, room.IsTerminalStatus(ledger.StatusClosed))
	}
	if room.IsTerminalStatus(ledger.StatusDoing) {
		t.Fatalf("进行中不应判终态")
	}
}

// mustSession 建一场群主为 user:sy 的会话，返回会话房间 id（P4 拍板的统一
// 记法：人=user:<name>。测试夹具从建会话起就用统一记法，成员执法与 mention
// 匹配都按逐字相等）。
func mustSession(t *testing.T, svc *Service, st *ledger.Store, title string) string {
	t.Helper()
	session, err := svc.CreateSession(title, "user:sy", "user:sy")
	if err != nil {
		t.Fatalf("建会话: %v", err)
	}
	return session.ID
}

// TestSessionKindFreedom kind 白名单废止的行为锚（spec §4.2「发言自由——身份
// 仍执法，不再管你说的是哪一类话」）：词表外 kind（heartbeat）由成员发出即
// 落账且 kind 原样保留（账本仍能回答「谁说的、说的哪类」）。发送者用群主
// 身份——Task 3 成员执法落地后本测试不需改（群主恒是成员）。
func TestSessionKindFreedom(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	session, err := svc.CreateSession("自由发言会话", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	seq, err := svc.Send(session.ID, proto.RoomMessage{Kind: "heartbeat", Body: "心跳"}, "user:sy")
	if err != nil {
		t.Fatalf("词表外 kind 应放行（发言自由，spec §4.2）: %v", err)
	}
	events, err := st.EventsFromAsc([]string{}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Seq != seq {
			continue
		}
		var back proto.RoomMessage
		if err := json.Unmarshal(ev.Payload, &back); err != nil {
			t.Fatal(err)
		}
		if back.Kind != "heartbeat" {
			t.Fatalf("kind 应原样落账: %q", back.Kind)
		}
		return
	}
	t.Fatalf("账本流上没有 seq=%d", seq)
}

// TestSessionMembershipEnforcement 会话书写执法（breakdown §3.2 ③ + §2.4
// 澄清 1）：actor ∈ 显式成员 ∪ 会话内各卡当前席位才可发言，在 Service.Send
// 路径执法（HTTP handleRoomSend、CLI room send、未来订阅通道回复全部共享这
// 一道门——缺陷族 5）。全部断言穿 Service.Send + 真账本（缺陷族 4：不许只测
// room 包帮手）。两个承重反例：会话外卡的席位身份不是成员（席位权威在卡 ≠
// 成员权威在会话）；system:pointer 不得借道 Send 写会话（pointer 豁免只服务
// 卡房间的 Service.Pointer，不得扩大成「系统身份可写会话」——缺陷族 8）。
func TestSessionMembershipEnforcement(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	card := sessionCard(t, st, "成员卡")
	outsider := sessionCard(t, st, "会话外卡")
	session, err := svc.CreateSession("执法会话", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.JoinCard(session.ID, card.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	if err := st.BindSeat(card.ID, "cli:opencode#seat-1", proto.SeatSourceCoordinate); err != nil {
		t.Fatalf("配人: %v", err)
	}
	if err := st.BindSeat(outsider.ID, "cli:codex#outside", proto.SeatSourceCoordinate); err != nil {
		t.Fatalf("配外卡: %v", err)
	}

	// 显式成员（群主）发言 → 成功且 actor 落账可查。
	seq1, err := svc.Send(session.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "群主发言"}, "user:sy")
	if err != nil {
		t.Fatalf("显式成员发言应成功: %v", err)
	}
	assertActor(t, st, seq1, "user:sy")
	// 会话内卡的当前席位发言 → 成功。
	seq2, err := svc.Send(session.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "协调者发言"}, "cli:opencode#seat-1")
	if err != nil {
		t.Fatalf("席位成员发言应成功: %v", err)
	}
	assertActor(t, st, seq2, "cli:opencode#seat-1")

	// 非成员 → ErrNotWriter。
	if _, err := svc.Send(session.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "路人"}, "user:outsider"); err != ErrNotWriter {
		t.Fatalf("非成员发言必须 ErrNotWriter，got %v", err)
	}
	// 会话外卡的席位身份 → ErrNotWriter（它只在它自己那张卡的房间是合法书写者）。
	if _, err := svc.Send(session.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "外卡席位"}, "cli:codex#outside"); err != ErrNotWriter {
		t.Fatalf("会话外卡席位发言必须 ErrNotWriter，got %v", err)
	}
	// pointer 系统身份借道 Send → ErrNotWriter（pointer 豁免不扩大，缺陷族 8）。
	if _, err := svc.Send(session.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "系统"}, "system:pointer"); err != ErrNotWriter {
		t.Fatalf("system:pointer 借道 Send 必须 ErrNotWriter，got %v", err)
	}
	// 空 actor → ErrNotWriter。
	if _, err := svc.Send(session.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "匿名"}, ""); err != ErrNotWriter {
		t.Fatalf("空 actor 必须 ErrNotWriter，got %v", err)
	}
}

// assertActor 断言账本流上 seq 那条 room_message 的 actor 落账可查，且会话
// 消息是无卡事件（breakdown ③「成功且 actor 落账可查」的机械载体）。
func assertActor(t *testing.T, st *ledger.Store, seq int64, actor string) {
	t.Helper()
	events, err := st.EventsFromAsc([]string{}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Seq != seq {
			continue
		}
		if ev.Actor != actor {
			t.Fatalf("落账 actor 漂移: want %q got %q", actor, ev.Actor)
		}
		if ev.CardID != "" {
			t.Fatalf("会话消息必须是无卡事件: %+v", ev)
		}
		return
	}
	t.Fatalf("账本流上没有 seq=%d", seq)
}

// TestSessionRebindRevokesOldSeatWriter 换绑剥权（breakdown §3.2 ③；原
// TestSendRebindRevokesOldSession 锁的卡房间矩阵已随旧房间归档死亡，本测试
// 是它的会话形态重立）：同一会话内，换绑前旧席位发言成功落账、换绑后旧席位
// 被拒（且不出账）、新席位发言成功——前后两条消息都在账本。反例必须能变红
// （先红后绿，步骤 1 顺序保证）——否则成员执法只在夹具里成立（缺陷族 4）。
func TestSessionRebindRevokesOldSeatWriter(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	card := sessionCard(t, st, "换绑会话卡")
	session, err := svc.CreateSession("换绑会话", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.JoinCard(session.ID, card.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	if err := st.BindSeat(card.ID, "cli:opencode#old", proto.SeatSourceCoordinate); err != nil {
		t.Fatalf("配人: %v", err)
	}
	seqBefore, err := svc.Send(session.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "换绑前"}, "cli:opencode#old")
	if err != nil {
		t.Fatalf("换绑前旧席位发言应成功: %v", err)
	}
	if err := st.RebindSeat(card.ID, "cli:opencode#new", proto.SeatSourceCoordinate, "cli:opencode#old"); err != nil {
		t.Fatalf("换绑: %v", err)
	}
	if _, err := svc.Send(session.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "换绑后旧席位"}, "cli:opencode#old"); err != ErrNotWriter {
		t.Fatalf("换绑后旧席位必须 ErrNotWriter，got %v", err)
	}
	seqAfter, err := svc.Send(session.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "换绑后新席位"}, "cli:opencode#new")
	if err != nil {
		t.Fatalf("换绑后新席位发言应成功: %v", err)
	}
	// 前后两条消息都在账本（同一会话，前后两条消息——breakdown ③ 原文）；
	// 被拒那条不出账。
	assertActor(t, st, seqBefore, "cli:opencode#old")
	assertActor(t, st, seqAfter, "cli:opencode#new")
	history, err := svc.History(session.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range history {
		var msg proto.RoomMessage
		if err := json.Unmarshal(ev.Payload, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Body == "换绑后旧席位" {
			t.Fatalf("被拒发言不得出账: %+v", ev)
		}
	}
}

// TestSendConsumesRoomMentions B287 延伸到会话房间（breakdown §3.2 ③）：会话
// 房间内 user 发言后，该房间 @本人 的未消费提及被消费——EvMessageConsumed
// 存在式断言 + 收件箱 mention 源不再返回该条。发送方用会话内卡当前席位身份
// （Task 3 成员执法落地后本测试不需改）；@ 目标是群主。缝级断言，入口
// Service.Send / Service.Mentions（spec §6 接缝 #1）。
func TestSendConsumesRoomMentions(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	card := sessionCard(t, st, "提及载体卡")
	session, err := svc.CreateSession("提及会话", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.JoinCard(session.ID, card.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	if err := st.BindSeat(card.ID, "cli:opencode#seat-1", proto.SeatSourceCoordinate); err != nil {
		t.Fatalf("配人: %v", err)
	}
	// 席位成员 @群主。
	if _, err := svc.Send(session.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "看一下这个", Mentions: []string{"user:sy"}}, "cli:opencode#seat-1"); err != nil {
		t.Fatal(err)
	}
	pending, err := svc.Mentions("user:sy", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("发送前应有 1 条未消费提及，got %d", len(pending))
	}
	// 群主 user 发言（回复）→ 该房间 @本人 的提及被消费。
	if _, err := svc.Send(session.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "收到"}, "user:sy"); err != nil {
		t.Fatal(err)
	}
	pending, err = svc.Mentions("user:sy", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("回复后收件箱 mention 源应清空，got %d", len(pending))
	}
	// EvMessageConsumed 存在式断言：账本上确实落了消费标记（不是读侧巧合）。
	events, err := st.EventsFromAsc([]string{}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	consumed := false
	for _, ev := range events {
		if ev.Type == ledger.EvMessageConsumed && ev.Actor == "user:sy" {
			consumed = true
		}
	}
	if !consumed {
		t.Fatal("回复后必须落 EvMessageConsumed（恰好一次语义在账本）")
	}
}

// TestSendOldRoomsArchivedReadOnly 旧房间只读归档（breakdown 岔口 P5 方案 A；
// contract 欠账 #7 的实现=写路径拒绝，无数据迁移——spec 实现决定 5「内容不迁移」）。
// 三形态发言一律 ErrReadOnly：房间在、只是归档——不是 ErrNoRoom（缺陷族 2：
// 拒绝语义必须可行动）。同一卡房间 Service.Pointer 仍可写：派发指针
// （cmd/card_dispatch.go roomPointer）与协调者叙事（agentd roomNarrator）两条
// 上游靠它活着，pointer 豁免是硬约束。指针行断言用存在式不数行数——两条上游
// 并存，计数式会把对方变成偶发红（agentd roomNarrator 注释同款纪律）。
func TestSendOldRoomsArchivedReadOnly(t *testing.T) {
	svc, st := newFixture(t)
	card := mustAnyCard(t, svc, st)
	for _, roomID := range []string{card.ID, "project:handoff", "global"} {
		if _, err := svc.Send(roomID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "旧房间不该写进去"}, "user:sy"); err != ErrReadOnly {
			t.Fatalf("旧房间 %s 发言必须 ErrReadOnly，got %v", roomID, err)
		}
	}
	// pointer 豁免：同一卡房间 Pointer 落账成功且事件可查（存在式）。
	seq, err := svc.Pointer(card.ID, proto.RoomMessage{Body: "派发指针仍在"})
	if err != nil {
		t.Fatalf("卡房间 Pointer 必须仍可写（pointer 豁免硬约束）: %v", err)
	}
	events, err := st.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range events {
		if ev.Seq == seq && ev.Type == "room_message" {
			found = true
		}
	}
	if !found {
		t.Fatalf("指针行未落账: seq=%d events=%+v", seq, events)
	}
}

// TestSessionArchiveSendReadOnly 归档只读接进发言路径（breakdown §2.4 澄清 ②：
// 契约 §3.7 的门面归档判定覆盖发言路径，不只 JoinCard）。生命周期序列反例：
// 归档前成员发言成功落账、归档后一律 ErrReadOnly——归档与发言的机内串行反例
// 覆盖状态机半边（账本单写者 Store.mutate 串行化；PG 真库并发归真机清单 #4，
// 缺陷族 1）。发送者用群主身份，Task 3 成员执法落地后不需改。
func TestSessionArchiveSendReadOnly(t *testing.T) {
	svc, st := newFixture(t)
	sid := mustSession(t, svc, st, "归档发言会话")
	if _, err := svc.Send(sid, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "归档前可以说话"}, "user:sy"); err != nil {
		t.Fatalf("归档前群主发言应成功: %v", err)
	}
	if err := svc.ArchiveSession(sid, "user:sy"); err != nil {
		t.Fatalf("归档: %v", err)
	}
	if _, err := svc.Send(sid, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "归档后还想说"}, "user:sy"); err != ErrReadOnly {
		t.Fatalf("归档会话发言必须 ErrReadOnly，got %v", err)
	}
}
