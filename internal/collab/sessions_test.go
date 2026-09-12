// B358 会话（群）域直通竖切与投递寻址测试。重档法定步骤：一次真实调用穿过
// 全部空壳——collab.Service → client.LedgerClient → internal/ledger/api.Facade
// → 真 SQLite ledger.Store。测试钉在主缝上（库缝形态 = 夹具直调）。
//
// 投递寻址（本卡核心契约）的断言全在 room.ResolveDelivery 的纯函数外形上：
// 无寻址不唤醒、@ 命中才唤醒、reply_to 命中原作者、@卡号解析当前席位、系统
// 结构行不唤醒；扇出形状写不出来（签名里没有成员集合）。
package collab

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/collab/client"
	"github.com/Xsxdot/handoff/internal/collab/room"
	"github.com/Xsxdot/handoff/internal/ledger"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/proto"
)

// newSessionFixture 起真 SQLite 账本并挂 collab.Service；同时返回出站接口
// （Facade）以断言接口直接能力。
func newSessionFixture(t *testing.T) (*Service, *ledger.Store, client.LedgerClient) {
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
	lc := ledgerapi.New(st)
	return New(lc), st, lc
}

func sessionCard(t *testing.T, st *ledger.Store, title string) ledger.Card {
	t.Helper()
	card, err := st.CreateCard(ledger.NewCard{Title: title, Project: "handoff", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	return card
}

// TestSessionVerticalSlice 直通竖切：开会话 → 拉卡进群 → 群里发言（无卡事件）
// → History 读回 → 会话列表与详情页投影。一次真实调用穿全链。
func TestSessionVerticalSlice(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	card := sessionCard(t, st, "竖切会话卡")

	session, err := svc.CreateSession("架构物理化", "user:sy", "user:sy")
	if err != nil {
		t.Fatalf("建会话: %v", err)
	}
	if session.ID == "" || session.Owner != "user:sy" {
		t.Fatalf("会话回执不完整: %+v", session)
	}
	if err := svc.JoinCard(session.ID, card.ID, "user:sy"); err != nil {
		t.Fatalf("拉卡进群: %v", err)
	}
	// 进群 ≠ 配人：卡在群里但席位为空座。
	if err := st.BindSeat(card.ID, "cli:opencode#seat-1", proto.SeatSourceCoordinate); err != nil {
		t.Fatalf("配人: %v", err)
	}

	// 群消息：无卡事件（CardID 为空），带显式 @卡号。
	seq, err := svc.Send(session.ID, proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "@B 看这里", Mentions: []string{card.ID},
	}, "user:sy")
	if err != nil {
		t.Fatalf("群发言: %v", err)
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
	if found == nil || found.CardID != "" {
		t.Fatalf("会话消息必须是无卡事件: %+v", found)
	}

	history, err := svc.History(session.ID, 0, 0)
	if err != nil || len(history) != 1 || history[0].Seq != seq {
		t.Fatalf("History 读回不符: %+v err=%v", history, err)
	}

	summaries, err := svc.ListSessions("user:sy")
	if err != nil {
		t.Fatalf("列会话: %v", err)
	}
	if len(summaries) != 1 || summaries[0].ID != session.ID {
		t.Fatalf("会话列表条目错误: %+v", summaries)
	}
	if len(summaries[0].Cards) != 1 || summaries[0].Cards[0].Seat != "cli:opencode#seat-1" {
		t.Fatalf("会话卡投影错误: %+v", summaries[0].Cards)
	}
	if summaries[0].Unread != 1 {
		t.Fatalf("成员未读应为 1: %+v", summaries[0].Unread)
	}

	detail, err := svc.SessionDetail(session.ID)
	if err != nil {
		t.Fatalf("会话详情: %v", err)
	}
	if detail.Summary.ID != session.ID || len(detail.Timeline) == 0 {
		t.Fatalf("会话详情投影不完整: %+v", detail)
	}
}

// TestWakeTargetsAddressing 投递寻址：唯一唤醒判定入口。
func TestWakeTargetsAddressing(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	card := sessionCard(t, st, "寻址卡")
	if err := st.BindSeat(card.ID, "cli:opencode#seat-1", proto.SeatSourceCoordinate); err != nil {
		t.Fatalf("配人: %v", err)
	}
	session, err := svc.CreateSession("寻址会话", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.JoinCard(session.ID, card.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}

	// 无寻址 → 不唤醒任何人。
	noAddr := ledgerEvent(t, proto.RoomMessage{Room: session.ID, Kind: proto.RoomMsgUser, Body: "没人要办的话"})
	if targets, err := svc.WakeTargets(noAddr); err != nil || len(targets) != 0 {
		t.Fatalf("无寻址发言不得唤醒: %v err=%v", targets, err)
	}

	// @卡号 → 命中该卡当前席位。
	mentioned := ledgerEvent(t, proto.RoomMessage{Room: session.ID, Kind: proto.RoomMsgUser, Body: "@卡", Mentions: []string{card.ID}})
	targets, err := svc.WakeTargets(mentioned)
	if err != nil || len(targets) != 1 || targets[0] != "cli:opencode#seat-1" {
		t.Fatalf("@卡号应命中当前席位: %v err=%v", targets, err)
	}

	// 换绑后 @同一卡号 → 命中新席位。
	if err := st.RebindSeat(card.ID, "cli:codex#seat-2", proto.SeatSourceCoordinate, "cli:opencode#seat-1"); err != nil {
		t.Fatalf("换绑: %v", err)
	}
	targets, err = svc.WakeTargets(mentioned)
	if err != nil || len(targets) != 1 || targets[0] != "cli:codex#seat-2" {
		t.Fatalf("换绑后 @卡号应命中新席位: %v err=%v", targets, err)
	}

	// 系统结构行 → 不唤醒。
	system := ledgerEvent(t, proto.RoomMessage{Room: session.ID, Kind: proto.RoomMsgPointer, Body: "指针", BySystem: true})
	if targets, err := svc.WakeTargets(system); err != nil || len(targets) != 0 {
		t.Fatalf("系统结构行不得唤醒: %v err=%v", targets, err)
	}
}

// TestResolveDeliveryPurity 纯函数寻址：reply_to 隐式寻址原作者、@ 与回复
// 去重、空寻址空集。
func TestResolveDeliveryPurity(t *testing.T) {
	resolve := func(cardID string) (string, bool) {
		if cardID == "B1" {
			return "cli:x#new", true
		}
		return "", false
	}

	// 有 @ 有 reply：去重后按首次出现顺序。
	got := room.ResolveDelivery(proto.RoomMessage{
		Kind: proto.RoomMsgUser, Mentions: []string{"B1", "user:sy"}, ReplyTo: 9,
	}, "cli:x#new", resolve)
	if len(got) != 2 || got[0] != "cli:x#new" || got[1] != "user:sy" {
		t.Fatalf("寻址去重/顺序错误: %v", got)
	}

	// 只 reply：命中 replyAuthor。
	got = room.ResolveDelivery(proto.RoomMessage{Kind: proto.RoomMsgUser, ReplyTo: 9}, "user:sy", resolve)
	if len(got) != 1 || got[0] != "user:sy" {
		t.Fatalf("reply 应隐式寻址原作者: %v", got)
	}

	// 无寻址：空集。
	if got = room.ResolveDelivery(proto.RoomMessage{Kind: proto.RoomMsgUser}, "", resolve); len(got) != 0 {
		t.Fatalf("无寻址应空集: %v", got)
	}

	// 空座 @：resolveSeat 返回 isCard=true 但 seat 空时该 @ 落空。
	emptySeat := func(string) (string, bool) { return "", true }
	got = room.ResolveDelivery(proto.RoomMessage{Kind: proto.RoomMsgUser, Mentions: []string{"B-empty"}}, "", emptySeat)
	if len(got) != 0 {
		t.Fatalf("空座 @ 不应命中: %v", got)
	}

	// 非卡号 @（外部会话身份）：resolveSeat isCard=false，原样使用。
	got = room.ResolveDelivery(proto.RoomMessage{Kind: proto.RoomMsgUser, Mentions: []string{"user:sy"}}, "", resolve)
	if len(got) != 1 || got[0] != "user:sy" {
		t.Fatalf("外部会话身份 @ 应原样命中: %v", got)
	}
}

// TestSessionArchiveReadOnly 归档后只读：归档会话不得再拉卡进群。
func TestSessionArchiveReadOnly(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	card := sessionCard(t, st, "归档会话卡")
	session, err := svc.CreateSession("归档会话", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ArchiveSession(session.ID, "user:sy"); err != nil {
		t.Fatalf("归档: %v", err)
	}
	if err := svc.JoinCard(session.ID, card.ID, "user:sy"); err == nil {
		t.Fatal("归档会话不得再拉卡进群")
	}
}

// TestSessionExplicitMembers 显式成员（人 / 主 agent）落会话成员列，幂等；
// 席位成员另由卡席位派生，不入 Members。
func TestSessionExplicitMembers(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	session, err := svc.CreateSession("成员会话", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	// CreateSession 初值含群主。
	if len(session.Members) != 1 || session.Members[0] != "user:sy" {
		t.Fatalf("会话初值应含群主: %+v", session.Members)
	}
	if err := st.AddSessionMember(session.ID, "cli:claude#desktop", "user:sy"); err != nil {
		t.Fatalf("加显式成员: %v", err)
	}
	if err := st.AddSessionMember(session.ID, "cli:claude#desktop", "user:sy"); err != nil {
		t.Fatalf("重复加成员应幂等: %v", err)
	}
	got, err := st.GetSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Members) != 2 {
		t.Fatalf("显式成员应恰好两条（幂等）: %+v", got.Members)
	}
}

// TestLedgerClientSessionPassthrough 断言出站接口（Facade 实现侧）的会话语义：
// SessionOfCard 归属读写与 AddSessionMember 幂等。这些是编译期接口方法，运行时
// 行为必须穿真 Facade 落真 SQLite——不留「已实现但零测试」的方法。
func TestLedgerClientSessionPassthrough(t *testing.T) {
	_, st, lc := newSessionFixture(t)
	card := sessionCard(t, st, "接口缝卡")
	session, err := lc.CreateSession("接口缝会话", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	if err := lc.JoinCardToSession(session.ID, card.ID, "user:sy"); err != nil {
		t.Fatalf("接口缝拉卡进群: %v", err)
	}
	if owner, err := lc.SessionOfCard(card.ID); err != nil || owner != session.ID {
		t.Fatalf("SessionOfCard 应命中会话 %s: %q err=%v", session.ID, owner, err)
	}
	if err := lc.AddSessionMember(session.ID, "cli:claude#desktop", "user:sy"); err != nil {
		t.Fatalf("接口缝加成员: %v", err)
	}
	if err := lc.AddSessionMember(session.ID, "cli:claude#desktop", "user:sy"); err != nil {
		t.Fatalf("接口缝加成员应幂等: %v", err)
	}
	got, err := lc.GetSession(session.ID)
	if err != nil || len(got.Members) != 2 {
		t.Fatalf("显式成员应两条: %+v err=%v", got.Members, err)
	}
	// 不存在的会话 → 使用方哨兵 client.ErrNotFound（门面翻 ErrNoRoom）。
	if _, err := lc.GetSession("session:999"); !errors.Is(err, client.ErrNotFound) {
		t.Fatalf("不存在会话应返回 client.ErrNotFound: %v", err)
	}
	if _, err := svcDetail(t, New(lc), "session:999"); !errors.Is(err, ErrNoRoom) {
		t.Fatalf("门面应把哨兵翻成 ErrNoRoom: %v", err)
	}
}

// svcDetail 调 Service.SessionDetail 并返回错误（测试辅助）。
func svcDetail(t *testing.T, svc *Service, id string) (proto.SessionDetail, error) {
	t.Helper()
	return svc.SessionDetail(id)
}

// TestIsAddressed 判断消息是否携带任何寻址（显式 @ 或有效 reply）。
func TestIsAddressed(t *testing.T) {
	cases := []struct {
		name   string
		msg    proto.RoomMessage
		author string
		want   bool
	}{
		{"none", proto.RoomMessage{Kind: proto.RoomMsgUser}, "", false},
		{"mention", proto.RoomMessage{Kind: proto.RoomMsgUser, Mentions: []string{"B1"}}, "", true},
		{"blank mention only", proto.RoomMessage{Kind: proto.RoomMsgUser, Mentions: []string{"  "}}, "", false},
		{"reply with author", proto.RoomMessage{Kind: proto.RoomMsgUser, ReplyTo: 5}, "user:sy", true},
		{"reply without author", proto.RoomMessage{Kind: proto.RoomMsgUser, ReplyTo: 5}, "", false},
		{"system row", proto.RoomMessage{Kind: proto.RoomMsgUser, Mentions: []string{"B1"}, BySystem: true}, "", false},
	}
	for _, tc := range cases {
		if got := room.IsAddressed(tc.msg, tc.author); got != tc.want {
			t.Errorf("%s: IsAddressed=%v want %v", tc.name, got, tc.want)
		}
	}
}

// ledgerEvent 把一个 RoomMessage 编成账本事件外形（纯函数测试用）。
func ledgerEvent(t *testing.T, msg proto.RoomMessage) proto.LedgerEvent {
	t.Helper()
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	return proto.LedgerEvent{Seq: 1, Type: "room_message", Actor: "user:sy", Payload: raw}
}
