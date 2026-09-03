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

// TestSendUserMessageVerticalSlice 直通竖切：一次真实调用穿全链。
func TestSendUserMessageVerticalSlice(t *testing.T) {
	svc, st := newFixture(t)
	card := mustAnyCard(t, svc, st)

	msg := proto.RoomMessage{Room: card.ID, Kind: proto.RoomMsgUser,
		Body: "先停一下，验收判据我想改", Refs: []string{"docs/x.md"}, Mentions: []string{"B156"}}
	seq, err := svc.Send(card.ID, msg, "user:sy")
	if err != nil {
		t.Fatalf("竖切发送失败: %v", err)
	}
	if seq <= 0 {
		t.Fatalf("seq 必须为正: %d", seq)
	}

	events, err := st.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var found *ledger.Event
	for i := range events {
		if events[i].Type == "room_message" {
			found = &events[i]
		}
	}
	if found == nil {
		t.Fatalf("card_events 里没有 room_message 行: %+v", events)
	}
	if found.CardID != card.ID || found.Actor != "user:sy" {
		t.Fatalf("落账行字段漂移: card=%q actor=%q", found.CardID, found.Actor)
	}
	var back proto.RoomMessage
	if err := json.Unmarshal(found.Payload, &back); err != nil {
		t.Fatal(err)
	}
	if back.Body != msg.Body || back.Kind != proto.RoomMsgUser || len(back.Refs) != 1 {
		t.Fatalf("载荷往返不一致: %+v", back)
	}

	history, err := svc.History(card.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Seq != seq {
		t.Fatalf("History 读回与发送不符: %+v", history)
	}
}

// TestSendGroupMessageLandsAsCardlessEvent 群级消息走无卡事件。
func TestSendGroupMessageLandsAsCardlessEvent(t *testing.T) {
	svc, st := newFixture(t)
	const room = "project:handoff"
	seq, err := svc.Send(room, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "全局通知"}, "user:sy")
	if err != nil {
		t.Fatalf("群房间发送失败: %v", err)
	}
	events, err := st.EventsFromAsc([]string{}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Seq == seq && ev.CardID != "" {
			t.Fatalf("群级消息必须是无卡事件: %+v", ev)
		}
	}
}

func TestSendRejectsUnknownKind(t *testing.T) {
	svc, _ := newFixture(t)
	if _, err := svc.Send("project:x", proto.RoomMessage{Kind: "heartbeat", Body: "心跳"}, "user:sy"); err != ErrKindNotAllowed {
		t.Fatalf("白名单外 kind 必须拒收，got %v", err)
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

func TestSendRejectsEmptyActorForUserKind(t *testing.T) {
	svc, st := newFixture(t)
	card := mustAnyCard(t, svc, st)
	if _, err := svc.Send(card.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "x"}, ""); err != ErrNotWriter {
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

// TestSendCoordinatorKindsRequireBinding 契约 §4 条 4：escalation/deviation/
// closing/reply 由非当前绑定者发送 → ErrNotWriter；当前绑定者可发。
func TestSendCoordinatorKindsRequireBinding(t *testing.T) {
	svc, st := newFixture(t)
	for _, kind := range []string{proto.RoomMsgEscalation, proto.RoomMsgDeviation,
		proto.RoomMsgClosing, proto.RoomMsgReply} {
		card := mustCard(t, svc, st, "协调者执法卡")
		if _, err := svc.Send(card.ID, proto.RoomMessage{Kind: kind, Body: "x"}, "cli:codex#a"); err != ErrNotWriter {
			t.Fatalf("kind %s 非绑定者必须 ErrNotWriter，got %v", kind, err)
		}
		mustBind(t, st, card.ID, "cli:codex#a")
		seq, err := svc.Send(card.ID, proto.RoomMessage{Kind: kind, Body: "x"}, "cli:codex#a")
		if err != nil || seq <= 0 {
			t.Fatalf("kind %s 当前绑定者可发，got err=%v seq=%d", kind, err, seq)
		}
	}
}

// TestSendRelayAllowsDirectParentWriter 契约 §4 条 5：relay 可由直接父卡当前
// 绑定者发送；条 6 反例在 TestSendRelayRejectsGrandparentAndUnrelated。
func TestSendRelayAllowsDirectParentWriter(t *testing.T) {
	svc, st := newFixture(t)
	parent := mustCard(t, svc, st, "父卡")
	child := mustCardWithParent(t, svc, st, "子卡", parent.ID)
	mustBind(t, st, parent.ID, "cli:codex#p")
	if _, err := svc.Send(child.ID, proto.RoomMessage{Kind: proto.RoomMsgRelay, Body: "衔接"}, "cli:codex#p"); err != nil {
		t.Fatalf("直接父绑定者 relay 必须可发，got %v", err)
	}
	mustBind(t, st, child.ID, "cli:codex#c")
	if _, err := svc.Send(child.ID, proto.RoomMessage{Kind: proto.RoomMsgRelay, Body: "衔接"}, "cli:codex#c"); err != nil {
		t.Fatalf("本卡绑定者 relay 必须可发，got %v", err)
	}
}

// TestSendRelayRejectsGrandparentAndUnrelated 契约 §4 条 6：祖父卡（只查一级
// 父，拍板 5.5）与无关联会话 → ErrNotWriter。
func TestSendRelayRejectsGrandparentAndUnrelated(t *testing.T) {
	svc, st := newFixture(t)
	gp := mustCard(t, svc, st, "祖父卡")
	parent := mustCardWithParent(t, svc, st, "父卡", gp.ID)
	child := mustCardWithParent(t, svc, st, "子卡", parent.ID)
	mustBind(t, st, gp.ID, "cli:codex#g")
	mustBind(t, st, parent.ID, "cli:codex#p")
	for _, actor := range []string{"cli:codex#g", "cli:codex#unrelated"} {
		if _, err := svc.Send(child.ID, proto.RoomMessage{Kind: proto.RoomMsgRelay, Body: "x"}, actor); err != ErrNotWriter {
			t.Fatalf("relay actor=%s 必须 ErrNotWriter，got %v", actor, err)
		}
	}
}

// TestSendUserRejectsCardBinding 契约 §4 条 7：user 类 actor 等于房间卡当前
// 绑定值 → ErrNotWriter（用户不是协调者）。
func TestSendUserRejectsCardBinding(t *testing.T) {
	svc, st := newFixture(t)
	card := mustCard(t, svc, st, "绑定卡")
	mustBind(t, st, card.ID, "cli:codex#a")
	if _, err := svc.Send(card.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "x"}, "cli:codex#a"); err != ErrNotWriter {
		t.Fatalf("user actor==绑定值必须 ErrNotWriter，got %v", err)
	}
	if _, err := svc.Send(card.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "x"}, "user:sy"); err != nil {
		t.Fatalf("user 非绑定者应可发，got %v", err)
	}
}

// TestSendUserRejectsParentBinding 岔口十最窄读法：子卡房间的 user 类还拒
// 直接父卡绑定者（相关卡={该卡, 直接父}，与拍板 5.5 一级父同构）。
func TestSendUserRejectsParentBinding(t *testing.T) {
	svc, st := newFixture(t)
	parent := mustCard(t, svc, st, "父卡")
	child := mustCardWithParent(t, svc, st, "子卡", parent.ID)
	mustBind(t, st, parent.ID, "cli:codex#p")
	if _, err := svc.Send(child.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "x"}, "cli:codex#p"); err != ErrNotWriter {
		t.Fatalf("user actor==直接父绑定值必须 ErrNotWriter，got %v", err)
	}
	if _, err := svc.Send(child.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "x"}, "user:sy"); err != nil {
		t.Fatalf("user 非任何绑定者应可发，got %v", err)
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

// TestSendRebindRevokesOldSession 换绑剥权合取（依赖 C1）：RebindDriver 成功
// 后旧会话对该房间的协调者类 Send → ErrNotWriter，新会话可发。
func TestSendRebindRevokesOldSession(t *testing.T) {
	svc, st := newFixture(t)
	card := mustCard(t, svc, st, "换绑卡")
	mustBind(t, st, card.ID, "cli:codex#old")
	if _, err := svc.Send(card.ID, proto.RoomMessage{Kind: proto.RoomMsgEscalation, Body: "x"}, "cli:codex#old"); err != nil {
		t.Fatalf("换绑前旧会话可发，got %v", err)
	}
	if err := st.RebindSeat(card.ID, "cli:codex#new", proto.SeatSourceCoordinate, "cli:codex#old"); err != nil {
		t.Fatalf("换绑: %v", err)
	}
	if _, err := svc.Send(card.ID, proto.RoomMessage{Kind: proto.RoomMsgEscalation, Body: "x"}, "cli:codex#old"); err != ErrNotWriter {
		t.Fatalf("换绑后旧会话必须 ErrNotWriter，got %v", err)
	}
	if _, err := svc.Send(card.ID, proto.RoomMessage{Kind: proto.RoomMsgEscalation, Body: "x"}, "cli:codex#new"); err != nil {
		t.Fatalf("换绑后新会话可发，got %v", err)
	}
}

// TestSendUserGroupRoomRequiresNonEmpty 群房间无卡可比：user 仅要求 actor
// 非空（岔口十最窄读法）。
func TestSendUserGroupRoomRequiresNonEmpty(t *testing.T) {
	svc, _ := newFixture(t)
	if _, err := svc.Send("project:handoff", proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "x"}, ""); err != ErrNotWriter {
		t.Fatalf("群房间空 actor 必须 ErrNotWriter，got %v", err)
	}
	if _, err := svc.Send("global", proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "x"}, "user:sy"); err != nil {
		t.Fatalf("群房间非空 actor 可发，got %v", err)
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

func TestHistoryFiltersNonRoomEvents(t *testing.T) {
	svc, st := newFixture(t)
	card := mustAnyCard(t, svc, st)
	if _, err := st.AddComment(card.ID, "普通评论", "普通", "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Send(card.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "房内发言"}, "user:sy"); err != nil {
		t.Fatal(err)
	}
	history, err := svc.History(card.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("History 必须滤掉非 room_message 事件: %d 条", len(history))
	}
}

// TestHistoryReturnsNewestWindow 钉住「升序截尾」：无 before 时取最新 limit 条，
// 不是从最老一侧截断。B274 真机：发送已 200 却永远看不见新消息。
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
		if _, err := svc.Send(card.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "m" + itoa(i)}, "user:sy"); err != nil {
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

func TestMentionsFiltersByMember(t *testing.T) {
	svc, st := newFixture(t)
	mustAnyCard(t, svc, st)
	if _, err := svc.Send("project:handoff",
		proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@B145 看", Mentions: []string{"B145"}}, "user:sy"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Send("project:handoff",
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
