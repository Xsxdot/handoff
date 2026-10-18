// B435 R：驻场者地址的候选读（C-149 身份 @ ∪ reply→驻场者作者 ∪ 同会话无 @ 的无卡消息；C-150 新支
// 不按 reply_to 过滤；C-151 有界、两方言同集同序）。权威裁决在 collab.Service.Matches。
package ledger

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

const b435ResidentIdentity = "user:r"

// b435ResidentCandidateFixture 在 f 上落一组覆盖 C-149 三支与排除项的消息，返回应入候选的 seq（升序）。
func b435ResidentCandidateFixture(t *testing.T, f *u5Fixture, actor func(string) string) []int64 {
	t.Helper()
	const sess = "session:1"
	// 应入：@驻场身份。
	at := f.u5Send(t, actor("user:a"), sess, proto.RoomMsgUser, []string{b435ResidentIdentity}, 0, false)
	// 不应入：驻场者自己 @别人（mentions 非空，非回复）。
	base := f.u5Send(t, actor(b435ResidentIdentity), sess, proto.RoomMsgUser, []string{"user:z"}, 0, false)
	// 应入：reply→驻场者作者（mentions 非空，只靠 reply 支）。
	reply := f.u5Send(t, actor("user:a"), sess, proto.RoomMsgUser, []string{"user:q"}, base, false)
	// 应入：同会话无 @（缺键）、无效 reply_to 且无 @。
	missing := f.u5Send(t, actor("user:a"), sess, proto.RoomMsgUser, nil, 0, false)
	badReply := f.u5Send(t, actor("user:a"), sess, proto.RoomMsgUser, nil, 999999999, false)
	// 不应入：他会话无 @、挂卡历史行、@别人。
	f.u5Send(t, actor("user:a"), "session:2", proto.RoomMsgUser, nil, 0, false)
	f.u5SendCardRoom(t, f.card1, nil)
	f.u5Send(t, actor("user:a"), sess, proto.RoomMsgUser, []string{"user:other"}, 0, false)
	return []int64{at, reply, missing, badReply}
}

func TestB435ResidentCandidatesSuperset(t *testing.T) {
	f := newU5Fixture(t)
	s := f.s
	const sess = "session:1"
	want := b435ResidentCandidateFixture(t, f, func(a string) string { return a })
	emptyArr := b435InsertCardless(t, s, "user:a", `{"room":"session:1","kind":"user","body":"x","mentions":[]}`)
	want = append(want, emptyArr)
	ctx := context.Background()
	got, err := s.ResidentMessageCandidatesContext(ctx, b435ResidentIdentity, sess, 0, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(seqsOf(got), want) {
		t.Fatalf("驻场者候选不符:\n got=%v\nwant=%v", seqsOf(got), want)
	}
	page, err := s.ResidentMessageCandidatesContext(ctx, b435ResidentIdentity, sess, 0, 0, 2)
	if err != nil || !reflect.DeepEqual(seqsOf(page), want[:2]) {
		t.Fatalf("limit=2 应返回前两条且升序: %v %v", seqsOf(page), err)
	}
	window, err := s.ResidentMessageCandidatesContext(ctx, b435ResidentIdentity, sess, want[0], want[2], 100)
	if err != nil || !reflect.DeepEqual(seqsOf(window), want[1:3]) {
		t.Fatalf("(from,to] 窗口不符: %v %v", seqsOf(window), err)
	}
	for _, bad := range []struct {
		identity, session string
		limit             int
	}{{"", sess, 10}, {b435ResidentIdentity, "", 10}, {b435ResidentIdentity, sess, 0}} {
		if _, err := s.ResidentMessageCandidatesContext(ctx, bad.identity, bad.session, 0, 0, bad.limit); err == nil {
			t.Fatalf("非法入参应报错: %+v", bad)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.ResidentMessageCandidatesContext(canceled, b435ResidentIdentity, sess, 0, 0, 10); err == nil {
		t.Fatal("已取消 ctx 应报错")
	}
	// 与席位候选同一 SQL：对同一身份两读面同集同序（「同体」的可观察证据）。
	seat, err := s.SeatMessageCandidatesContext(ctx, b435ResidentIdentity, sess, 0, 0, 100)
	if err != nil || !reflect.DeepEqual(seqsOf(seat), seqsOf(got)) {
		t.Fatalf("驻场者候选应与同身份席位候选同集同序: seat=%v resident=%v err=%v", seqsOf(seat), seqsOf(got), err)
	}
	// 执行计划：三支扫描域仍在无卡 room_message 局部索引上（无 card_events 全表 SCAN）。
	q, args := s.addressCandidatesSQL(b435ResidentIdentity, sess, 0, 0)
	plan := explainQueryPlan(t, s, s.q(q), append(args, 100)...)
	t.Logf("B435_RESIDENT_CANDIDATES_SQLITE_EQP %s", plan)
	if strings.Contains(plan, "SCAN card_events") || !strings.Contains(plan, "idx_room_messages_cardless_seq") {
		t.Fatalf("驻场者候选执行计划未走无卡 room_message 局部索引：\n%s", plan)
	}
}

// C-151 PG 孪生（隔离 schema，门控；真机 R-PG）。
func TestB435ResidentCandidatesPG(t *testing.T) {
	f := newU5FixturePG(t)
	want := b435ResidentCandidateFixture(t, f, func(string) string { return f.run })
	// PG 夹具以 run 标记为全部 actor（清理按 actor 定位），reply→驻场者作者支无法用 run 作者构造，
	// 故 PG 腿只核「@驻场身份 + 同会话无 @」两支与排除项，reply 支由 SQLite 腿锁。
	got, err := f.s.ResidentMessageCandidatesContext(context.Background(), b435ResidentIdentity, "session:1", 0, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	wantPG := []int64{want[0], want[2], want[3]}
	if !reflect.DeepEqual(seqsOf(got), wantPG) {
		t.Fatalf("PG 驻场者候选不符: got=%v want=%v", seqsOf(got), wantPG)
	}
}
