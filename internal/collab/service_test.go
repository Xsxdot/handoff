// 协作房间域入站门面的直通竖切与执法分支测试（B156.2 契约 §3.3/§7）。
//
// 竖切（重档法定步骤）：一次真实调用 Send(user, 卡房间) 穿过 Service →
// client.LedgerClient 接口 → internal/ledger/api.Facade → 真 SQLite
// ledger.Store，落 card_events 后由 History 读回。测试钉在主缝上（库缝
// 形态 = 夹具直调），这是 Ticket 0「越过空壳的可观测行为须有能变红的测试」
// 的正当出口。
//
// 本文件的 import internal/ledger(/api) 仅存在于 _test.go：图边采集排除
// 测试文件（charter/graph edgegate.go:190），不构成 d_collab→d_ledger 生产边。
package collab

import (
	"encoding/json"
	"path/filepath"
	"testing"

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

// TestCoordinatorKindsNotForgedBySkeleton 竖切阶段协调者类 kind 不得被
// 空壳放行——放行即伪造书写者执法通过。
func TestCoordinatorKindsNotForgedBySkeleton(t *testing.T) {
	svc, st := newFixture(t)
	card := mustAnyCard(t, svc, st)
	for _, kind := range []string{proto.RoomMsgEscalation, proto.RoomMsgDeviation,
		proto.RoomMsgClosing, proto.RoomMsgRelay, proto.RoomMsgReply} {
		if _, err := svc.Send(card.ID, proto.RoomMessage{Kind: kind, Body: "x"}, "cli:a@h"); err == nil {
			t.Fatalf("kind %s 在欠账 #1 落地前不得放行", kind)
		}
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

// TestRoomEventTypeLiteralMatchesLedger 钉住 collab 侧字面量与账本词表的
// 等式；测试文件不计图边，可同时看见两侧（门面禁令只约束生产代码）。
func TestRoomEventTypeLiteralMatchesLedger(t *testing.T) {
	if protoRoomEventType != ledger.EvRoomMessage {
		t.Fatalf("房间事件类型字面量漂移: %q != %q", protoRoomEventType, ledger.EvRoomMessage)
	}
}
