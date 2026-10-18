// B435 席位地址的候选读、游标行读与积压下界（契约 C-112、C-119、C-148、C-150、C-151）。
//
// 候选新支 = 既有两支（身份 @ / 席位卡 @ / reply→席位作者）∪「该会话房间内 mentions 缺键或
// 空数组的无卡 room_message」。新支不按 reply_to 过滤（无效回复按无寻址，须进候选）；
// 权威裁决在 collab.Service.Matches。
package ledger

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

func b435InsertCardless(t *testing.T, s *Store, actor, payload string) int64 {
	t.Helper()
	res, err := s.db.Exec(`INSERT INTO card_events (card_id, type, actor, payload, created_at)
		VALUES (NULL, ?, ?, ?, ?)`, EvRoomMessage, actor, payload, s.tval(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	seq, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return seq
}

func TestB435SeatCandidatesUnaddressedBranch(t *testing.T) {
	f := newU5Fixture(t)
	s := f.s
	const sess = "session:1"
	// 新支应入：mentions 缺键、空数组、无效 reply_to（缺 mentions）。
	missing := f.u5Send(t, "user:a", sess, proto.RoomMsgUser, nil, 0, false)
	emptyArr := b435InsertCardless(t, s, "user:a", `{"room":"session:1","kind":"user","body":"x","mentions":[]}`)
	badReply := f.u5Send(t, "user:a", sess, proto.RoomMsgUser, nil, 999999, false)
	// 既有支应入：@席位卡、@席位身份。
	atCard := f.u5Send(t, "user:a", sess, proto.RoomMsgUser, []string{f.card1}, 0, false)
	atSeat := f.u5Send(t, "user:a", sess, proto.RoomMsgUser, []string{u5MemberSeat}, 0, false)
	// 不应入：他会话房间无 @、挂卡历史行、@别人。
	f.u5Send(t, "user:a", "session:2", proto.RoomMsgUser, nil, 0, false)
	f.u5SendCardRoom(t, f.card1, nil)
	f.u5Send(t, "user:a", sess, proto.RoomMsgUser, []string{"user:other"}, 0, false)

	got, err := s.SeatMessageCandidatesContext(context.Background(), u5MemberSeat, sess, 0, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{missing, emptyArr, badReply, atCard, atSeat}
	if !reflect.DeepEqual(seqsOf(got), want) {
		t.Fatalf("席位候选不符:\n got=%v\nwant=%v", seqsOf(got), want)
	}
	// 有界：limit 截断且升序；上界包含。
	page, err := s.SeatMessageCandidatesContext(context.Background(), u5MemberSeat, sess, 0, 0, 2)
	if err != nil || !reflect.DeepEqual(seqsOf(page), want[:2]) {
		t.Fatalf("limit=2 应返回前两条: %v %v", seqsOf(page), err)
	}
	window, err := s.SeatMessageCandidatesContext(context.Background(), u5MemberSeat, sess, missing, badReply, 100)
	if err != nil || !reflect.DeepEqual(seqsOf(window), []int64{emptyArr, badReply}) {
		t.Fatalf("(from,to] 窗口不符: %v %v", seqsOf(window), err)
	}
	for _, bad := range []struct {
		identity, session string
		limit             int
	}{{"", sess, 10}, {u5MemberSeat, "", 10}, {u5MemberSeat, sess, 0}} {
		if _, err := s.SeatMessageCandidatesContext(context.Background(), bad.identity, bad.session, 0, 0, bad.limit); err == nil {
			t.Fatalf("非法入参应报错: %+v", bad)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.SeatMessageCandidatesContext(ctx, u5MemberSeat, sess, 0, 0, 10); err == nil {
		t.Fatal("已取消 ctx 应报错")
	}

	// 执行计划：新支扫描域仍在无卡 room_message 局部索引上（无 card_events 全表 SCAN）。
	q, args := s.seatCandidatesUnaddressedBranchSQL(sess, 0, 0)
	plan := explainQueryPlan(t, s, s.q(q+" ORDER BY seq ASC LIMIT ?"), append(args, 100)...)
	t.Logf("B435_SEAT_CANDIDATES_SQLITE_EQP %s", plan)
	if strings.Contains(plan, "SCAN card_events") || !strings.Contains(plan, "idx_room_messages_cardless_seq") {
		t.Fatalf("新支执行计划未走无卡 room_message 局部索引：\n%s", plan)
	}
}

// C-119 前提：游标读区分「无行」与「行值 0」。
func TestB435DeliveryCursorRowDistinguishesMissing(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if seq, ok, err := s.SessionDeliveryCursorRowContext(ctx, "cli:codex#x"); err != nil || ok || seq != 0 {
		t.Fatalf("无行应 ok=false: %d %v %v", seq, ok, err)
	}
	if err := s.AdvanceSessionDeliveryCursor("cli:codex#x", 0); err != nil {
		t.Fatal(err)
	}
	if seq, ok, err := s.SessionDeliveryCursorRowContext(ctx, "cli:codex#x"); err != nil || !ok || seq != 0 {
		t.Fatalf("行值 0 应 ok=true: %d %v %v", seq, ok, err)
	}
	if err := s.AdvanceSessionDeliveryCursor("cli:codex#x", 42); err != nil {
		t.Fatal(err)
	}
	if seq, ok, err := s.SessionDeliveryCursorRowContext(ctx, "cli:codex#x"); err != nil || !ok || seq != 42 {
		t.Fatalf("行值应读回 42: %d %v %v", seq, ok, err)
	}
	if _, _, err := s.SessionDeliveryCursorRowContext(ctx, ""); err == nil {
		t.Fatal("空键应报错")
	}
}

// C-119（Q2 甲：限本卡）：下界 = 本卡上 to=身份 的最近一条坐下/换绑事件 seq。
func TestB435SeatBoundSeq(t *testing.T) {
	s := seedStore(t)
	ctx := context.Background()
	a, b := b435Card(t, s, "a"), b435Card(t, s, "b")
	const me, other = "cli:codex#me", "cli:codex#other"
	if _, ok, err := s.SeatBoundSeqContext(ctx, a.ID, me); err != nil || ok {
		t.Fatalf("从未坐下应 ok=false: %v %v", ok, err)
	}
	if err := s.BindSeatMode(a.ID, me, proto.SeatSourceBind, proto.SeatModePull, SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	first, ok, err := s.SeatBoundSeqContext(ctx, a.ID, me)
	if err != nil || !ok || first <= 0 {
		t.Fatalf("坐下后应读到下界: %d %v %v", first, ok, err)
	}
	if err := s.RebindSeatMode(a.ID, other, proto.SeatSourceBind, proto.SeatModePull, me, SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	if err := s.RebindSeatMode(a.ID, me, proto.SeatSourceBind, proto.SeatModePull, other, SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	second, ok, err := s.SeatBoundSeqContext(ctx, a.ID, me)
	if err != nil || !ok || second <= first {
		t.Fatalf("换绑回来应取最近一条: first=%d second=%d %v %v", first, second, ok, err)
	}
	// 同身份坐到别的卡不影响本卡下界（Q2 甲）。
	if err := s.BindSeatMode(b.ID, me, proto.SeatSourceBind, proto.SeatModePull, SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	if again, _, err := s.SeatBoundSeqContext(ctx, a.ID, me); err != nil || again != second {
		t.Fatalf("他卡坐下不得抬高本卡下界: %d want %d %v", again, second, err)
	}
}
