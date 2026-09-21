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
	"slices"
	"strings"
	"testing"
	"time"

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
	if err := st.BindSeat(card.ID, "cli:opencode#seat-1", proto.SeatSourceCoordinate, ledger.SeatBearing{Carrier: "test-carrier", Machine: "local"}); err != nil {
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
	if err := st.BindSeat(card.ID, "cli:opencode#seat-1", proto.SeatSourceCoordinate, ledger.SeatBearing{Carrier: "test-carrier", Machine: "local"}); err != nil {
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
	if err := st.RebindSeat(card.ID, "cli:codex#seat-2", proto.SeatSourceCoordinate, "cli:opencode#seat-1", ledger.SeatBearing{Carrier: "test-carrier", Machine: "local"}); err != nil {
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

// TestSessionArchiveReadOnly 归档后只读（contract §4.1 条 7）：归档会话不得
// 再拉卡进群，且拒绝路径不落事件（stream 上不得出现 session_card_joined）。
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
	events, err := st.EventsFromAsc([]string{}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Type == ledger.EvSessionCardJoined {
			t.Fatalf("归档后 JoinCard 被拒不得落事件（条 7）: %+v", ev)
		}
	}
}

// TestArchiveSessionLandsExactlyOneEvent contract §4.1 条 6：归档幂等且全生命
// 周期恰落一条 EvSessionArchived——重复归档不落第二条（协调者拍板 ①：Store 级
// 已归档短路修复扩进本卡有界文件集 internal/ledger/sessions.go，强读法生效；
// Store 侧同款断言在 internal/ledger/sessions_test.go，此处锁门面路径）。
func TestArchiveSessionLandsExactlyOneEvent(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	session, err := svc.CreateSession("恰一条归档事件", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ArchiveSession(session.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	// 重复归档：幂等 nil，不落第二条（条 6 强读法，拍板 ①）。
	if err := svc.ArchiveSession(session.ID, "user:sy"); err != nil {
		t.Fatalf("重复归档应幂等 nil: %v", err)
	}
	events, err := st.EventsFromAsc([]string{}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range events {
		if ev.Type == ledger.EvSessionArchived && payloadString(ev.Payload, "session") == session.ID {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("一次归档应恰一条 EvSessionArchived（重复归档不叠加），got %d", n)
	}
}

// TestSessionOutlivesCardTerminal contract §4.1 条 13：卡终态不导致会话归档
// （会话生命周期独立，spec §4.1；讨论面可能还在用）。
func TestSessionOutlivesCardTerminal(t *testing.T) {
	svc, st := newFixture(t)
	card := mustAnyCard(t, svc, st)
	sid := mustSession(t, svc, st, "比卡活得久的会话")
	if err := svc.JoinCard(sid, card.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	if err := st.CloseCard(card.ID, ledger.CloseCancelled, "test"); err != nil {
		t.Fatalf("置终态: %v", err)
	}
	detail, err := svc.SessionDetail(sid)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Summary.Archived {
		t.Fatal("卡终态不得归档会话（条 13）")
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
	if err := st.AddSessionMember(session.ID, "user:claude", "user:sy"); err != nil {
		t.Fatalf("加显式成员: %v", err)
	}
	if err := st.AddSessionMember(session.ID, "user:claude", "user:sy"); err != nil {
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

// TestSessionMemberKindByIdentityPrefix 锁 B358.9 契约 §5 H 组条 39/40：显式成员
// kind 按统一记法前缀判定——agent:<名> 不得因不是 cli: 形状而被误报 human。
func TestSessionMemberKindByIdentityPrefix(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	session, err := svc.CreateSession("kind 场", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddSessionMember(session.ID, "agent:opencode", "user:sy"); err != nil {
		t.Fatalf("加 agent 成员: %v", err)
	}
	detail, err := svc.SessionDetail(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m := memberByIdentity(t, detail, "agent:opencode"); m.Kind != proto.SessionMemberAgent {
		t.Fatalf("agent:<名> 成员 kind 应为 agent，实得 %q", m.Kind)
	}
	if m := memberByIdentity(t, detail, "user:sy"); m.Kind != proto.SessionMemberHuman {
		t.Fatalf("user:<名> 成员 kind 应为 human，实得 %q", m.Kind)
	}
}

// TestSessionSeatMemberKindDerivedFromCard 锁条 41/35：卡派生席位 kind=seat；
// 席位身份不落 session.Members（席位由卡当前 driver_session 派生）。
func TestSessionSeatMemberKindDerivedFromCard(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	card := sessionCard(t, st, "席位 kind 卡")
	session, err := svc.CreateSession("席位 kind 场", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.JoinCard(session.ID, card.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	if err := st.BindSeat(card.ID, "cli:opencode#seat-k", proto.SeatSourceCoordinate, ledger.SeatBearing{Carrier: "test-carrier", Machine: "local"}); err != nil {
		t.Fatal(err)
	}
	detail, err := svc.SessionDetail(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m := memberByCard(t, detail, card.ID); m.Kind != proto.SessionMemberSeat {
		t.Fatalf("卡派生席位 kind 应为 seat，实得 %q", m.Kind)
	}
	// 席位不落成员表：session.Members 不含 cli: 席位串（条 35）。
	if got, err := svc.lc.GetSession(session.ID); err != nil {
		t.Fatal(err)
	} else {
		for _, m := range got.Members {
			if strings.HasPrefix(m, "cli:") {
				t.Fatalf("席位身份不得写进 session.Members: %v", got.Members)
			}
		}
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
	if err := lc.AddSessionMember(session.ID, "user:claude", "user:sy"); err != nil {
		t.Fatalf("接口缝加成员: %v", err)
	}
	if err := lc.AddSessionMember(session.ID, "user:claude", "user:sy"); err != nil {
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

// sessionStructSeqs 扫全流建「事件类型/归属键值 → seq」索引（测试辅助）。
// created 的归属键是 id、joined/left 是 card——与 ledger 写面载荷同形。
func sessionStructSeqs(t *testing.T, st *ledger.Store) map[string]int64 {
	t.Helper()
	events, err := st.EventsFromAsc([]string{}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, ev := range events {
		var fields map[string]any
		if err := json.Unmarshal(ev.Payload, &fields); err != nil {
			continue
		}
		switch ev.Type {
		case ledger.EvSessionCreated:
			if id, _ := fields["id"].(string); id != "" {
				out["created/"+id] = ev.Seq
			}
		case ledger.EvSessionCardJoined:
			if card, _ := fields["card"].(string); card != "" {
				out["joined/"+card] = ev.Seq
			}
		}
	}
	return out
}

// cardSet 把卡号切片转集合（测试辅助）。
func cardSet(ids []string) map[string]bool {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// TestSessionTimelineBelongsToOneSession 跨会话污染反例（breakdown §2.4
// 发现 A）+ 同族反例：timeline 只装本会话的结构事实。卡 A ∈ 会话一、
// 卡 B 不属任何会话、卡 C ∈ 会话二：B 的席位事件不得进任何会话 timeline；
// 会话二的建群/进群事件不得进会话一（Ticket 0 三处归属偏差：takeover 全量
// 卡表、created 查错载荷键、joined/left 无判定）。本会话自己的行必须保留
// （防修复过删）。先红后绿：在修复前的代码上本测试必红。
func TestSessionTimelineBelongsToOneSession(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	cardA := sessionCard(t, st, "会话一成员卡")
	cardB := sessionCard(t, st, "无会话卡")
	cardC := sessionCard(t, st, "会话二成员卡")

	session1, err := svc.CreateSession("会话一", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.JoinCard(session1.ID, cardA.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	session2, err := svc.CreateSession("会话二", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.JoinCard(session2.ID, cardC.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	// 无会话卡 B：坐下 + 换绑，落 seat_bound 与 takeover 两个卡事件。
	if err := st.BindSeat(cardB.ID, "cli:opencode#b-1", proto.SeatSourceBind, ledger.SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	if err := st.RebindSeat(cardB.ID, "cli:codex#b-2", proto.SeatSourceCoordinate, "cli:opencode#b-1", ledger.SeatBearing{Carrier: "test-carrier", Machine: "local"}); err != nil {
		t.Fatal(err)
	}
	// 会话一成员卡 A 坐下再换绑：seat_rebound 行必须留在会话一（防过删）。
	if err := st.BindSeat(cardA.ID, "cli:opencode#a-1", proto.SeatSourceBind, ledger.SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	if err := st.RebindSeat(cardA.ID, "cli:codex#a-2", proto.SeatSourceCoordinate, "cli:opencode#a-1", ledger.SeatBearing{Carrier: "test-carrier", Machine: "local"}); err != nil {
		t.Fatal(err)
	}

	seqs := sessionStructSeqs(t, st)
	d1, err := svc.SessionDetail(session1.ID)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := svc.SessionDetail(session2.ID)
	if err != nil {
		t.Fatal(err)
	}
	s1full, err := st.GetSession(session1.ID)
	if err != nil {
		t.Fatal(err)
	}
	s2full, err := st.GetSession(session2.ID)
	if err != nil {
		t.Fatal(err)
	}

	// 反例主体（发现 A）：每场会话的 timeline 里，带卡号的行只许属于本会话的卡。
	for name, tc := range map[string]struct {
		detail proto.SessionDetail
		own    map[string]bool
	}{
		"会话一": {d1, cardSet(s1full.Cards)},
		"会话二": {d2, cardSet(s2full.Cards)},
	} {
		for _, row := range tc.detail.Timeline {
			if row.CardID != "" && !tc.own[row.CardID] {
				t.Fatalf("%s timeline 串进非本会话卡的事件行（发现 A）: %+v", name, row)
			}
		}
	}
	// 卡 B 不属任何会话：它的任何行不得出现在任何 timeline。
	for _, d := range []proto.SessionDetail{d1, d2} {
		for _, row := range d.Timeline {
			if row.CardID == cardB.ID {
				t.Fatalf("无会话卡 B 的事件串进 timeline: %+v", row)
			}
		}
	}
	// 同族反例：他场会话的结构事件（created/joined）不串场。
	for _, bad := range []struct {
		detail proto.SessionDetail
		seq    int64
		what   string
	}{
		{d1, seqs["created/"+session2.ID], "会话二的 created"},
		{d1, seqs["joined/"+cardC.ID], "卡 C 进会话二的 joined"},
		{d2, seqs["created/"+session1.ID], "会话一的 created"},
		{d2, seqs["joined/"+cardA.ID], "卡 A 进会话一的 joined"},
	} {
		for _, row := range bad.detail.Timeline {
			if row.Seq == bad.seq {
				t.Fatalf("他场结构事件串场（%s）: %+v", bad.what, row)
			}
		}
	}
	// 防过删：会话一自己的 created、卡 A 的 joined 必须在；卡 A 的
	// seat_rebound 行必须在且 Detail 原样透传 {from,to}（缺陷族 6）。
	for _, want := range []struct {
		seq  int64
		kind string
	}{
		{seqs["created/"+session1.ID], proto.SessionEventCreated},
		{seqs["joined/"+cardA.ID], proto.SessionEventCardJoined},
	} {
		found := false
		for _, row := range d1.Timeline {
			if row.Seq == want.seq && row.Kind == want.kind {
				found = true
			}
		}
		if !found {
			t.Fatalf("会话一自己的行被过删: want seq=%d kind=%s", want.seq, want.kind)
		}
	}
	reboundFound := false
	for _, row := range d1.Timeline {
		if row.Kind == proto.SessionEventSeatRebound && row.CardID == cardA.ID {
			reboundFound = true
			if !strings.Contains(row.Detail, `"from":"cli:opencode#a-1"`) ||
				!strings.Contains(row.Detail, `"to":"cli:codex#a-2"`) {
				t.Fatalf("seat_rebound Detail 应原样透传载荷（可对质）: %+v", row)
			}
		}
	}
	if !reboundFound {
		t.Fatalf("会话一成员卡 A 的换绑行被过删: %+v", d1.Timeline)
	}
}

// TestSessionTimelineSeatBoundAndCardClosed R2/R3 的 timeline 消费（契约欠账
// §8.10）：seat_bound 以 EvDriverSeatBound 为唯一载体、归属判据同 §9；
// card_closed 以既有 EvStatusMoved 为载体、当且仅当 to ∈ {已完成,终止} 记行，
// 终止 reason 透传 Detail；普通列间转移与他会话卡不产生行。测试用
// ledger.StatusDone/StatusClosed 常量生产事件——账本词表若变，此处先红
// （字面量漂移哨兵，契约条 36「collab 非测试零 import ledger」的测试侧镜像）。
func TestSessionTimelineSeatBoundAndCardClosed(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	cardA := sessionCard(t, st, "收口观察卡")
	cardE := sessionCard(t, st, "终止观察卡")
	cardB := sessionCard(t, st, "无会话卡")
	session1, err := svc.CreateSession("收口会话", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	session2, err := svc.CreateSession("旁观会话", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.JoinCard(session1.ID, cardA.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	if err := svc.JoinCard(session1.ID, cardE.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}

	// R2：成员卡坐下 → seat_bound 行；无会话卡坐下 → 不进任何 timeline。
	if err := st.BindSeat(cardA.ID, "cli:opencode#a-1", proto.SeatSourceBind, ledger.SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	if err := st.BindSeat(cardB.ID, "cli:opencode#b-1", proto.SeatSourceBind, ledger.SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	d1, err := svc.SessionDetail(session1.ID)
	if err != nil {
		t.Fatal(err)
	}
	var boundRow *proto.SessionTimelineEvent
	for i := range d1.Timeline {
		row := d1.Timeline[i]
		if row.Kind == proto.SessionEventSeatBound && row.CardID == cardB.ID {
			t.Fatalf("无会话卡 B 的 seat_bound 串场: %+v", row)
		}
		if row.Kind == proto.SessionEventSeatBound && row.CardID == cardA.ID {
			boundRow = &d1.Timeline[i]
		}
	}
	if boundRow == nil {
		t.Fatalf("坐下应产生 seat_bound 行: %+v", d1.Timeline)
	}
	if boundRow.Actor != "cli:opencode#a-1" {
		t.Fatalf("seat_bound actor 应为席位自称（账本写面即如此）: %+v", boundRow)
	}
	if !strings.Contains(boundRow.Detail, `"to":"cli:opencode#a-1"`) {
		t.Fatalf("seat_bound Detail 应原样透传载荷 {to}: %+v", boundRow)
	}

	// R3：普通列间转移不记行；to=已完成 记 card_closed；终止（含 reason）透传。
	if err := st.MoveCard(cardA.ID, ledger.StatusDoing, ledger.StatusTodo, "t"); err != nil {
		t.Fatal(err)
	}
	d1, err = svc.SessionDetail(session1.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range d1.Timeline {
		if row.Kind == proto.SessionEventCardClosed && row.CardID == cardA.ID {
			t.Fatalf("普通列间转移不得记 card_closed（契约条 56）: %+v", row)
		}
	}
	if err := st.MoveCard(cardA.ID, ledger.StatusDone, ledger.StatusDoing, "t"); err != nil {
		t.Fatal(err)
	}
	if err := st.CloseCard(cardE.ID, ledger.CloseAbandoned, "t"); err != nil {
		t.Fatal(err)
	}
	// 无会话卡 B 终止 → 不进任何会话 timeline。
	if err := st.CloseCard(cardB.ID, ledger.CloseCancelled, "t"); err != nil {
		t.Fatal(err)
	}
	d1, err = svc.SessionDetail(session1.ID)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := svc.SessionDetail(session2.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []proto.SessionDetail{d1, d2} {
		for _, row := range d.Timeline {
			if row.Kind == proto.SessionEventCardClosed && row.CardID == cardB.ID {
				t.Fatalf("无会话卡 B 的 card_closed 串场: %+v", row)
			}
		}
	}
	closedA, closedE := false, false
	for _, row := range d1.Timeline {
		if row.Kind == proto.SessionEventCardClosed && row.CardID == cardA.ID {
			closedA = true
			if !strings.Contains(row.Detail, `"to":"已完成"`) {
				t.Fatalf("card_closed Detail 应透传 to=已完成: %+v", row)
			}
		}
		if row.Kind == proto.SessionEventCardClosed && row.CardID == cardE.ID {
			closedE = true
			if !strings.Contains(row.Detail, `"to":"终止"`) || !strings.Contains(row.Detail, `"reason":"废弃"`) {
				t.Fatalf("终止 card_closed 应透传 to 与 reason（不做二次解释）: %+v", row)
			}
		}
	}
	if !closedA || !closedE {
		t.Fatalf("to∈{已完成,终止} 应各记一行 card_closed: closedA=%v closedE=%v", closedA, closedE)
	}
	// 词表对齐（缺陷族 7）：timeline 所有行的 kind ∈ proto 冻结词表 8 值。
	allowed := map[string]bool{
		proto.SessionEventCardJoined: true, proto.SessionEventCardLeft: true,
		proto.SessionEventSeatBound: true, proto.SessionEventSeatRebound: true,
		proto.SessionEventNeedsHuman: true, proto.SessionEventCardClosed: true,
		proto.SessionEventArchived: true, proto.SessionEventCreated: true,
	}
	for _, d := range []proto.SessionDetail{d1, d2} {
		for _, row := range d.Timeline {
			if !allowed[row.Kind] {
				t.Fatalf("timeline 出现词表外 kind: %+v", row)
			}
		}
	}
}

// TestSessionTimelineNeedsHuman needs_human 进 timeline（契约 §3.2 词表 +
// breakdown §3.3 ③）：卡 ∈ 会话的 needs_human 事件出恰一条 kind=needs_human
// 行且 Summary.NeedsHuman 亮起；卡 ∉ 会话的不出现、不点亮他会话标签；
// needs_cleared 不产生 timeline 行（词表 8 值无它——补签轮逐值冻结），
// 标签由 needsHumanByCard 翻灭。
func TestSessionTimelineNeedsHuman(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	cardA := sessionCard(t, st, "等人观察卡")
	cardB := sessionCard(t, st, "无会话卡")
	session1, err := svc.CreateSession("等人会话", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	session2, err := svc.CreateSession("隔壁会话", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.JoinCard(session1.ID, cardA.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkNeedsHuman(cardA.ID, "验收不过", "user:sy"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkNeedsHuman(cardB.ID, "与本会话无关", "user:sy"); err != nil {
		t.Fatal(err)
	}
	d1, err := svc.SessionDetail(session1.ID)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := svc.SessionDetail(session2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !d1.Summary.NeedsHuman {
		t.Fatal("会话内卡 needs_human 后列表标签应亮起")
	}
	if d2.Summary.NeedsHuman {
		t.Fatal("他会话卡的 needs_human 不得点亮本会话标签")
	}
	rows := 0
	for _, row := range d1.Timeline {
		if row.Kind == proto.SessionEventNeedsHuman {
			rows++
			if row.CardID != cardA.ID {
				t.Fatalf("needs_human 行归属错误: %+v", row)
			}
		}
	}
	if rows != 1 {
		t.Fatalf("应恰一条 needs_human 行, got %d: %+v", rows, d1.Timeline)
	}
	for _, row := range d2.Timeline {
		if row.Kind == proto.SessionEventNeedsHuman {
			t.Fatalf("无会话卡 B 的 needs_human 串进会话二: %+v", row)
		}
	}
	before := len(d1.Timeline)
	if err := st.ClearNeedsHuman(cardA.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	d1, err = svc.SessionDetail(session1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d1.Summary.NeedsHuman {
		t.Fatal("needs_cleared 后标签应熄灭")
	}
	if len(d1.Timeline) != before {
		t.Fatalf("needs_cleared 不得产生 timeline 行（词表无此 kind）: before=%d after=%d",
			before, len(d1.Timeline))
	}
}

// memberByCard 按卡号取席位成员（测试辅助）。
func memberByCard(t *testing.T, d proto.SessionDetail, cardID string) proto.SessionMember {
	t.Helper()
	for _, m := range d.Summary.Members {
		if m.CardID == cardID {
			return m
		}
	}
	t.Fatalf("找不到卡 %s 的席位成员: %+v", cardID, d.Summary.Members)
	return proto.SessionMember{}
}

// memberByIdentity 按身份取成员（测试辅助）。
func memberByIdentity(t *testing.T, d proto.SessionDetail, identity string) proto.SessionMember {
	t.Helper()
	for _, m := range d.Summary.Members {
		if m.Identity == identity {
			return m
		}
	}
	t.Fatalf("找不到成员 %s: %+v", identity, d.Summary.Members)
	return proto.SessionMember{}
}

// TestSessionMemberStatusHonest 成员状态诚实判据（契约条 17/18 + breakdown
// §2.4 澄清 3）：working 只能由未过期租约推出，判据时钟与租约判定同一注入
// 时钟（拨钟即翻）；无租约报 last_active+零值；空座报 empty；观察到的状态串
// ⊆ {working,last_active,empty}——四值词表减 listening（生产零载体，澄清 3），
// 「online」等词表外串自然被拒。本测试写下去应绿（行为已在）——红即停手上报。
func TestSessionMemberStatusHonest(t *testing.T) {
	svc, st, lc := newSessionFixture(t)
	cardA := sessionCard(t, st, "租约卡")
	cardC := sessionCard(t, st, "空座卡")
	session1, err := svc.CreateSession("状态会话", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.JoinCard(session1.ID, cardA.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	if err := svc.JoinCard(session1.ID, cardC.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	if err := st.BindSeat(cardA.ID, "cli:opencode#a-1", proto.SeatSourceBind, ledger.SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	// 注入时钟与租约判定同源（条 18）：nowFn 拨到可拨源 cur，working →
	// last_active 的翻转只由 cur 前拨触发（同源范式先例
	// readmodel_test.go#TestListRoomsLiveFlip）。
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cur := base
	oldNow := nowFn
	nowFn = func() time.Time { return cur }
	t.Cleanup(func() { nowFn = oldNow })

	// 未过期租约：Store.RenewDriverLease 是本测试专属生产者（生产零调用方，
	// 澄清 3）；expiresAt = 墙钟 + 1h，必在 cur（2026-01-01）之后 → working。
	if _, err := st.RenewDriverLease("cli:opencode#a-1", time.Hour); err != nil {
		t.Fatal(err)
	}
	exp, exists, err := lc.DriverLease("cli:opencode#a-1")
	if err != nil || !exists {
		t.Fatalf("租约应可读: exp=%v exists=%v err=%v", exp, exists, err)
	}
	d1, err := svc.SessionDetail(session1.ID)
	if err != nil {
		t.Fatal(err)
	}
	seat := memberByCard(t, d1, cardA.ID)
	if seat.Status != proto.SessionMemberWorking {
		t.Fatalf("未过期租约应报 working: %+v", seat)
	}
	if !seat.LastActive.Equal(exp) {
		t.Fatalf("working 的 LastActive 应为租约到期时刻 %v（可证实的「活到几点」）: %+v", exp, seat)
	}

	// 拨钟过期（同一 cur 前拨过 expiresAt）→ last_active；LastActive 仍是
	// 到期时刻。拨钟即翻 = 判据用的是注入钟的证明（缺陷族 4）。
	cur = time.Now().Add(2 * time.Hour)
	d1, err = svc.SessionDetail(session1.ID)
	if err != nil {
		t.Fatal(err)
	}
	seat = memberByCard(t, d1, cardA.ID)
	if seat.Status != proto.SessionMemberLastActive {
		t.Fatalf("拨钟过期后应翻 last_active: %+v", seat)
	}
	if !seat.LastActive.Equal(exp) {
		t.Fatalf("过期租约的 LastActive 应为到期时刻 %v: %+v", exp, seat)
	}

	// 无租约的显式成员：last_active + 零值 LastActive（无「最后活跃」可证实读数）。
	owner := memberByIdentity(t, d1, "user:sy")
	if owner.Status != proto.SessionMemberLastActive || !owner.LastActive.IsZero() {
		t.Fatalf("无租约成员应报 last_active+零值: %+v", owner)
	}
	// 空座卡：empty + 空身份 + 零值。
	empty := memberByCard(t, d1, cardC.ID)
	if empty.Status != proto.SessionMemberEmpty || empty.Identity != "" || !empty.LastActive.IsZero() {
		t.Fatalf("空座卡应报 empty: %+v", empty)
	}
	// 词表扫（缺陷族 8 + 澄清 3）：全部观察值 ∈ {working,last_active,empty}。
	for _, m := range d1.Summary.Members {
		switch m.Status {
		case proto.SessionMemberWorking, proto.SessionMemberLastActive, proto.SessionMemberEmpty:
		default:
			t.Fatalf("成员状态词表外取值（listening 无生产载体；online 不可证实）: %+v", m)
		}
	}
}

// TestSessionNodesAggregation 任务节点投影（breakdown §3.3 ③ + 缺陷族 2）：
// 卡 ∈ 会话的 task_mirrored（含 node 字段）聚合进 Nodes；卡 ∉ 会话的不出现；
// envelope 无 round/target/title 字段 → Round/Target/Title 留空（plan 定稿：
// 不为填空造新账本读）；单条载荷解码失败只跳该行、同卡其余节点保留。
// 本测试写下去应绿（行为已在）——红即停手上报。
func TestSessionNodesAggregation(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	cardA := sessionCard(t, st, "节点观察卡")
	cardB := sessionCard(t, st, "无会话卡")
	session1, err := svc.CreateSession("节点会话", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.JoinCard(session1.ID, cardA.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := st.AppendMirroredEvent(cardA.ID, ledger.MirroredEvent{
		Target: "tgt-1", Task: "task-1", Node: "implement", SourceSeq: 1,
		Type: "task_started", Payload: []byte(`{"step":1}`), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	// 解码失败样本：非法 JSON 内层 → 组合载荷整体不可解析 → 跳该行。
	if _, err := st.AppendMirroredEvent(cardA.ID, ledger.MirroredEvent{
		Target: "tgt-1", Task: "task-1", Node: "review", SourceSeq: 2,
		Type: "task_broken", Payload: []byte(`{"broken`), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	// 无会话卡 B 的镜像 → 不进 Nodes。
	if _, err := st.AppendMirroredEvent(cardB.ID, ledger.MirroredEvent{
		Target: "tgt-2", Task: "task-2", Node: "plan", SourceSeq: 3,
		Type: "task_started", Payload: []byte(`{}`), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	d1, err := svc.SessionDetail(session1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(d1.Nodes) != 1 {
		t.Fatalf("应恰一条节点（解码失败跳过、他会话卡不进）: %+v", d1.Nodes)
	}
	node := d1.Nodes[0]
	if node.CardID != cardA.ID || node.Node != "implement" || node.State != "task_started" {
		t.Fatalf("节点投影字段错误: %+v", node)
	}
	if node.Round != 0 || node.Target != "" || node.Title != "" {
		t.Fatalf("Round/Target/Title 应留空（envelope 无对应字段，不为填空造新账本读）: %+v", node)
	}
}

// TestMentionClearScopedToSessionRoom B287 审查移交反例：会话房间「回复即清
// 提及」只清本房间——user:alice 在两场会话各被 @ 一次，仅回复会话一时，会话
// 一的提及被消费（EvMessageConsumed 在），会话二的提及必须仍留在收件箱
// mention 源（不牵连他房间）。预期绿（consumeRoomMentions 已按 SameRoom
// 过滤）；实测红即越界修复面，停手上报。
func TestMentionClearScopedToSessionRoom(t *testing.T) {
	svc, st, lc := newSessionFixture(t)
	session1, err := svc.CreateSession("清提及-会话一", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	session2, err := svc.CreateSession("清提及-会话二", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{session1.ID, session2.ID} {
		if err := st.AddSessionMember(sid, "user:alice", "user:sy"); err != nil {
			t.Fatal(err)
		}
	}
	seq1, err := svc.Send(session1.ID, proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "@alice 看会话一", Mentions: []string{"user:alice"},
	}, "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	seq2, err := svc.Send(session2.ID, proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "@alice 看会话二", Mentions: []string{"user:alice"},
	}, "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	mentions, err := svc.Mentions("user:alice", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(mentions) != 2 {
		t.Fatalf("两条提及都应在收件箱: %+v", mentions)
	}
	// alice 只回复会话一。
	if _, err := svc.Send(session1.ID, proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "收到，会话一处理中",
	}, "user:alice"); err != nil {
		t.Fatal(err)
	}
	mentions, err = svc.Mentions("user:alice", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(mentions) != 1 || mentions[0].Seq != seq2 {
		t.Fatalf("会话二的提及必须保留、会话一的应被清: %+v", mentions)
	}
	// 权威在账本消费标记：seq1 已消费、seq2 未消费。
	// （plan 笔误修正：st.EventsFromAsc 返回 []ledger.Event，ConsumedSeqs 收
	// []proto.LedgerEvent——改走既有 room.ReadAllEvents 转换，语义同全流升序。）
	events, err := room.ReadAllEvents(lc, 0)
	if err != nil {
		t.Fatal(err)
	}
	consumed := room.ConsumedSeqs(events, "user:alice")
	if !consumed[seq1] {
		t.Fatalf("会话一提及应落 EvMessageConsumed: seq=%d", seq1)
	}
	if consumed[seq2] {
		t.Fatalf("会话二提及不得被牵连消费: seq=%d", seq2)
	}
}

// TestSessionDetailReadableAfterArchive 缺陷族 1：会话归档后详情投影仍可读
// （归档只读执法在写路径 Send/JoinCard，不封读面），timeline 含自己的
// created 与 archived 两行。
func TestSessionDetailReadableAfterArchive(t *testing.T) {
	svc, _, _ := newSessionFixture(t)
	session, err := svc.CreateSession("归档后读", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ArchiveSession(session.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	detail, err := svc.SessionDetail(session.ID)
	if err != nil {
		t.Fatalf("归档后详情必须可读: %v", err)
	}
	kinds := map[string]bool{}
	for _, row := range detail.Timeline {
		kinds[row.Kind] = true
	}
	if !kinds[proto.SessionEventCreated] || !kinds[proto.SessionEventArchived] {
		t.Fatalf("归档后 timeline 应含 created 与 archived: %+v", detail.Timeline)
	}
}

// —— B358.4 移交两支（B358.1 review：仅源码成立的两个事实补直测）——

// listAllCardsFailingClient 只把 ListAllCards 换成注入失败，其余能力走真实
// Facade（内嵌接口值）——构造「读账本失败」这个真实账本给不出的形状。
type listAllCardsFailingClient struct{ client.LedgerClient }

func (c listAllCardsFailingClient) ListAllCards(project string) ([]proto.Card, error) {
	return nil, errInjectedLedgerRead
}

var errInjectedLedgerRead = errors.New("注入：读卡列表失败")

// TestSessionWritersReadFailureNotWriterGuard 锁（B358.1 移交①）：sessionWriters
// 组装书写者集时读卡列表失败必须向上传播原错误，不得伪装成 ErrNotWriter——
// 一次账本读错被说成「你不是成员」（403）是缺陷族 2 的静默失败。fake 注入是
// 唯一构造点：gateway 侧 s.rooms 是真实 Facade 组装，从 HTTP 缝造不出读失败。
func TestSessionWritersReadFailureNotWriterGuard(t *testing.T) {
	svc, st, lc := newSessionFixture(t)
	session, err := svc.CreateSession("读失败反例", "user:sy", "user:sy")
	if err != nil {
		t.Fatalf("建会话: %v", err)
	}
	// 会话内有卡才会走 sessionWriters 的 ListAllCards 半边（无卡会话短路返回
	// 显式成员，读失败构造不出来，plan 原夹具即栽在这里——见台账偏差清单）。
	card := sessionCard(t, st, "读失败反例卡")
	if err := svc.JoinCard(session.ID, card.ID, "user:sy"); err != nil {
		t.Fatalf("拉卡进群: %v", err)
	}
	guarded := New(listAllCardsFailingClient{lc})
	_, err = guarded.Send(session.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "x"}, "user:sy")
	if err == nil {
		t.Fatal("读卡列表失败后发言应报错")
	}
	if errors.Is(err, ErrNotWriter) {
		t.Fatalf("读账本失败不得伪装 403（ErrNotWriter）: %v", err)
	}
	if !errors.Is(err, errInjectedLedgerRead) {
		t.Fatalf("读失败应原样向上传播，实得: %v", err)
	}
}

// TestSessionSendToMissingSessionRoomIsErrNoRoom 锁（B358.1 移交②）：会话房间
// 不存在（session:999 无本体）时经 Send 发言 → ErrNoRoom。路径：Resolve 会话
// 分支只解析形态（room.go，不查会话表）→ sendToSession 的 GetSession 失败 →
// mapSessionError（client.ErrNotFound → ErrNoRoom）。此前仅源码成立，无直测。
func TestSessionSendToMissingSessionRoomIsErrNoRoom(t *testing.T) {
	svc, _, _ := newSessionFixture(t)
	_, err := svc.Send("session:999", proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "x"}, "user:sy")
	if !errors.Is(err, ErrNoRoom) {
		t.Fatalf("不存在的会话房间发言必须 ErrNoRoom，实得: %v", err)
	}
	if errors.Is(err, ErrReadOnly) {
		t.Fatalf("不得误报只读（房间不是在而是无）: %v", err)
	}
}

// TestSendNormalizesMentionAtPrefixToSeat 锁 B391（spec §4.1）：写入边界把
// `@卡号` / `@身份` 归一化为契约裸口径，落账的 mentions 是 `B1`，判定命中
// 该卡当前席位。当前 bug：存储 @B1、GetCard 查不到、targets=["@B1"] 不命中。
// 变异复验：去掉 Send 的 normalizeMentions 调用，本测试重新变红。
func TestSendNormalizesMentionAtPrefixToSeat(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	card := sessionCard(t, st, "B391 归一化卡")
	if err := st.BindSeat(card.ID, "cli:opencode#seat-1", proto.SeatSourceCoordinate,
		ledger.SeatBearing{Carrier: "test-carrier", Machine: "local"}); err != nil {
		t.Fatalf("配人: %v", err)
	}
	session, err := svc.CreateSession("B391 归一化场", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.JoinCard(session.ID, card.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	// 三种 @ 形态同发：卡号、user: 身份、agent: 身份。
	seq, err := svc.Send(session.ID, proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "@" + card.ID + " @user:sy @agent:opencode 看这里",
		Mentions: []string{"@" + card.ID, "@user:sy", "@agent:opencode"},
	}, "user:sy")
	if err != nil {
		t.Fatalf("发言: %v", err)
	}

	// 穿真实序列化边界：从 SQLite 读回 payload 原文再解码，断言存储即契约裸口径。
	events, err := st.EventsFromAsc([]string{}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var (
		stored proto.RoomMessage
		found  bool
	)
	for _, ev := range events {
		if ev.Seq != seq || ev.Type != "room_message" {
			continue
		}
		found = true
		if err := json.Unmarshal(ev.Payload, &stored); err != nil {
			t.Fatalf("解码落账载荷: %v", err)
		}
	}
	if !found {
		t.Fatalf("未找到 seq=%d 的 room_message", seq)
	}
	// 存储边界断言：payload 里的 mentions 已是契约裸口径（body 仍保留 @ 前缀，
	// 不参与本断言——逐字相等只针对 Mentions 数组）。
	wantMentions := []string{card.ID, "user:sy", "agent:opencode"}
	if !slices.Equal(stored.Mentions, wantMentions) {
		t.Fatalf("落账 mentions 应为契约裸口径 %v，实得 %q", wantMentions, stored.Mentions)
	}

	// 判定命中该卡当前席位；两个外部身份原样。
	targets, err := svc.MessageWakeTargets(stored)
	if err != nil {
		t.Fatal(err)
	}
	wantTargets := []string{"cli:opencode#seat-1", "user:sy", "agent:opencode"}
	if !slices.Equal(targets, wantTargets) {
		t.Fatalf("寻址结果 %v want %v", targets, wantTargets)
	}

	// 换绑后同一落账消息仍解析到新席位（@卡号 的能力不因换绑丢失）。
	if err := st.RebindSeat(card.ID, "cli:codex#seat-2", proto.SeatSourceCoordinate,
		"cli:opencode#seat-1", ledger.SeatBearing{Carrier: "test-carrier", Machine: "local"}); err != nil {
		t.Fatalf("换绑: %v", err)
	}
	targets, err = svc.MessageWakeTargets(stored)
	if err != nil {
		t.Fatal(err)
	}
	wantAfterRebind := []string{"cli:codex#seat-2", "user:sy", "agent:opencode"}
	if !slices.Equal(targets, wantAfterRebind) {
		t.Fatalf("换绑后寻址结果 %v want %v", targets, wantAfterRebind)
	}
}

// TestSendMentionNormalizationReverseCases 两条反例不回归（spec §4.2/§4.3）：
// 无寻址不唤醒任何人；@空座卡不落空到别人；裸卡号既有行为不变。
func TestSendMentionNormalizationReverseCases(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	card := sessionCard(t, st, "B391 反例卡")
	if err := st.BindSeat(card.ID, "cli:opencode#seat-1", proto.SeatSourceCoordinate,
		ledger.SeatBearing{Carrier: "test-carrier", Machine: "local"}); err != nil {
		t.Fatalf("配人: %v", err)
	}
	empty := sessionCard(t, st, "B391 空座卡")
	session, err := svc.CreateSession("B391 反例场", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.JoinCard(session.ID, card.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	if err := svc.JoinCard(session.ID, empty.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	noAddrSeq, err := svc.Send(session.ID, proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "没人要办的话"}, "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	emptySeatSeq, err := svc.Send(session.ID, proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "@空座", Mentions: []string{"@" + empty.ID}}, "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	bareSeq, err := svc.Send(session.ID, proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "裸卡号", Mentions: []string{card.ID}}, "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	events, err := st.EventsFromAsc([]string{}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	bySeq := map[int64]proto.RoomMessage{}
	for _, ev := range events {
		if ev.Type != "room_message" {
			continue
		}
		var m proto.RoomMessage
		if err := json.Unmarshal(ev.Payload, &m); err != nil {
			t.Fatal(err)
		}
		bySeq[ev.Seq] = m
	}
	for _, tc := range []struct {
		name      string
		seq       int64
		wantEmpty bool
	}{
		{"无寻址不唤醒", noAddrSeq, true},
		{"@空座卡不落空", emptySeatSeq, true},
		{"裸卡号既有行为", bareSeq, false},
	} {
		targets, err := svc.MessageWakeTargets(bySeq[tc.seq])
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if tc.wantEmpty && len(targets) != 0 {
			t.Fatalf("%s: 应空集，实得 %v", tc.name, targets)
		}
		if !tc.wantEmpty && (len(targets) != 1 || targets[0] != "cli:opencode#seat-1") {
			t.Fatalf("%s: 应命中席位，实得 %v", tc.name, targets)
		}
	}
}

// TestB389SessionListNeedsHumanTagKept 锁 §4-20 回归：B389 判据收口只动唤醒映射，
// 会话列表「需要你」待办（SessionDetail.Summary.NeedsHuman）照常亮起，不改码。
func TestB389SessionListNeedsHumanTagKept(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	card := sessionCard(t, st, "B389 待办标签")
	session, err := svc.CreateSession("B389 会话", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.JoinCard(session.ID, card.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkNeedsHuman(card.ID, "需要你处置", "user:sy"); err != nil {
		t.Fatal(err)
	}
	detail, err := svc.SessionDetail(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !detail.Summary.NeedsHuman {
		t.Fatal("needs_human 后会话列表「需要你」待办应亮起")
	}
}
