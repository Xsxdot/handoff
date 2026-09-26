// 会话消息候选读与收件箱限域读的金样（B409 U5，有界候选读契约第 1–5/21–25/45 条）。
//
// 候选读只按地址键枚举「可能被 MessageWakeTargets 定向到 member」的超集：
//   - mentions 精确含 member（身份）或含 member 当前席位的卡号；
//   - reply_to 指向 member 是作者的消息；
// 唯一裁决仍在 collab.Service.MessageWakeTargets（契约第 19/20 条），因此
// by_system/pointer/非会话房间等「枚举进来但判定拒绝」的行必须出现在候选里
// ——把寻址规则的任何一条复制进 SQL 都会让这些金样变红。
package ledger

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

const (
	u5Member     = "user:sy"
	u5Other      = "user:other"
	u5MemberSeat = "cli:member#m1"
	u5OtherSeat  = "cli:other#o1"
	// u5NoListMax 是「无上界」金样用的远界：任何夹具 seq 都小于它。
	u5NoListMax = int64(1) << 62
)

// u5Fixture 建两卡两席位（C1→member 席位、C2→other 席位）。PG 上夹具行以
// run 标记为 actor 落账，测试收尾按标记清理。
type u5Fixture struct {
	s     *Store
	card1 string
	card2 string
	run   string // PG 夹具 actor 标记（SQLite 为空）
}

func newU5Fixture(t *testing.T) *u5Fixture {
	t.Helper()
	s, err := Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return u5FinishFixture(t, s)
}

func newU5FixturePG(t *testing.T) *u5Fixture {
	t.Helper()
	s := newB409PGStore(t)
	run := fmt.Sprintf("u5-cand-%d", time.Now().UnixNano())
	f := u5FinishFixture(t, s)
	cleanupB409U5Fixture(t, s, run, f.card1, f.card2)
	// PG 夹具统一以 run 为 actor 落事件，清理按 actor 定位。
	f.run = run
	return f
}

func u5FinishFixture(t *testing.T, s *Store) *u5Fixture {
	t.Helper()
	if _, err := s.PutWorkflow("charter", WorkflowDef{States: []string{"待办", "完成"}}); err != nil {
		t.Fatal(err)
	}
	c1, err := s.CreateCard(NewCard{Project: "p", Title: "U5 候选夹具 C1", Actor: "u5-tester"})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := s.CreateCard(NewCard{Project: "p", Title: "U5 候选夹具 C2", Actor: "u5-tester"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BindSeat(c1.ID, u5MemberSeat, proto.SeatSourceBind, SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	if err := s.BindSeat(c2.ID, u5OtherSeat, proto.SeatSourceBind, SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	return &u5Fixture{s: s, card1: c1.ID, card2: c2.ID}
}

// u5Send 以指定作者身份落一条房间消息（cardID 空 = 群级无卡事件），返回 seq。
func (f *u5Fixture) u5Send(t *testing.T, actor, roomID, kind string, mentions []string, replyTo int64, bySystem bool) int64 {
	t.Helper()
	msg := proto.RoomMessage{
		Room: roomID, Kind: kind, Body: "u5 body", Mentions: mentions,
		ReplyTo: replyTo, BySystem: bySystem,
	}
	seq, err := f.s.RecordRoomMessage("", msg, actor)
	if err != nil {
		t.Fatalf("夹具发言: %v", err)
	}
	return seq
}

// u5SendCardRoom 落一条挂卡的历史卡房间消息（旧形态，仅用于验证候选边界）。
func (f *u5Fixture) u5SendCardRoom(t *testing.T, cardID string, mentions []string) int64 {
	t.Helper()
	msg := proto.RoomMessage{Room: cardID, Kind: proto.RoomMsgUser, Body: "u5 legacy", Mentions: mentions}
	seq, err := f.s.RecordRoomMessage(cardID, msg, "u5-tester")
	if err != nil {
		t.Fatalf("夹具卡房间发言: %v", err)
	}
	return seq
}

func mustSessionCandidates(t *testing.T, s *Store, member string, from, to int64, limit int) []Event {
	t.Helper()
	events, err := s.SessionMessageCandidatesContext(context.Background(), member, from, to, limit)
	if err != nil {
		t.Fatalf("候选读: %v", err)
	}
	return events
}

func seqsOf(events []Event) []int64 {
	out := make([]int64, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Seq)
	}
	return out
}

// 覆盖金样：身份 @、当前席位卡 @、reply→member 作者、非会话房间与系统行
// （超集、由权威判定拒绝）都在候选；other 的席位卡、other 的 reply、无寻址、
// 卡房间行不在候选。顺序严格升序、无重复。
func TestSessionMessageCandidatesCoversWakeableSuperset(t *testing.T) {
	f := newU5Fixture(t)
	s := f.s

	seqIdentity := f.u5Send(t, "u5-tester", "session:1", proto.RoomMsgUser, []string{u5Member}, 0, false)
	seqMemberCard := f.u5Send(t, "u5-tester", "session:1", proto.RoomMsgUser, []string{f.card1}, 0, false)
	seqOtherCard := f.u5Send(t, "u5-tester", "session:1", proto.RoomMsgUser, []string{f.card2}, 0, false)
	memberMsg := f.u5Send(t, u5Member, "session:1", proto.RoomMsgUser, nil, 0, false)
	otherMsg := f.u5Send(t, u5Other, "session:1", proto.RoomMsgUser, nil, 0, false)
	seqReplyMember := f.u5Send(t, "u5-tester", "session:1", proto.RoomMsgUser, nil, memberMsg, false)
	seqReplyOther := f.u5Send(t, "u5-tester", "session:1", proto.RoomMsgUser, nil, otherMsg, false)
	seqOtherMention := f.u5Send(t, "u5-tester", "session:1", proto.RoomMsgUser, []string{u5Other}, 0, false)
	seqNoAddress := f.u5Send(t, "u5-tester", "session:1", proto.RoomMsgUser, nil, 0, false)
	seqSystem := f.u5Send(t, "u5-tester", "session:1", proto.RoomMsgUser, []string{u5Member}, 0, true)
	seqPointer := f.u5Send(t, "u5-tester", "session:1", proto.RoomMsgPointer, []string{u5Member}, 0, false)
	seqProjectRoom := f.u5Send(t, "u5-tester", "project:x", proto.RoomMsgUser, []string{u5Member}, 0, false)
	f.u5SendCardRoom(t, f.card1, []string{u5Member}) // 卡房间行：永远进不了会话订阅通道

	events := mustSessionCandidates(t, s, u5Member, 0, u5NoListMax, 100)
	got := seqsOf(events)
	// user:sy 的候选：身份 @、reply→user:sy 作者、系统行与非会话房间行
	// （超集，由权威判定拒绝）。卡号 @ 不命中外部身份——席位解算属于
	// 席位身份 member（下面单独金样）。
	want := []int64{seqIdentity, seqReplyMember, seqSystem, seqPointer, seqProjectRoom}
	if len(got) != len(want) {
		t.Fatalf("候选集不符：got=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("候选序不符 @%d：got=%v want=%v（升序、无重复）", i, got, want)
		}
	}
	// 席位身份 member 的候选：@C1（该卡 driver_session=cli:member#m1）。
	// @C2（other 席位）不得进入。
	seatGot := seqsOf(mustSessionCandidates(t, s, u5MemberSeat, 0, u5NoListMax, 100))
	if len(seatGot) != 1 || seatGot[0] != seqMemberCard {
		t.Fatalf("席位身份候选错：%v want=[%d]", seatGot, seqMemberCard)
	}
	// other 的席位身份只看到 @C2，看不到 @C1。
	otherSeatGot := seqsOf(mustSessionCandidates(t, s, u5OtherSeat, 0, u5NoListMax, 100))
	if len(otherSeatGot) != 1 || otherSeatGot[0] != seqOtherCard {
		t.Fatalf("other 席位候选错：%v want=[%d]", otherSeatGot, seqOtherCard)
	}
	// 非命中行明确不在 user:sy 候选（失败时给出定位）。
	for _, excluded := range []struct {
		name string
		seq  int64
	}{{"other 的席位卡", seqOtherCard}, {"other 的 reply", seqReplyOther}, {"other 的 mention", seqOtherMention}, {"无寻址", seqNoAddress}} {
		for _, seq := range got {
			if seq == excluded.seq {
				t.Fatalf("%s（seq=%d）不得进入 member=%s 的候选", excluded.name, excluded.seq, u5Member)
			}
		}
	}
}

// 当前席位语义：候选随绑定状态变化（契约第 4 条「member 当前席位」）。
// 订阅 member 用席位身份 cli:member#m1——@卡号 的席位解算只对它生效。
func TestSessionMessageCandidatesFollowsCurrentSeat(t *testing.T) {
	f := newU5Fixture(t)
	s := f.s
	// C1 先绑给 other：@C1 不该进 member 席位的候选。
	if err := s.RebindSeat(f.card1, u5OtherSeat, proto.SeatSourceBind, u5MemberSeat, SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	seqCard := f.u5Send(t, "u5-tester", "session:1", proto.RoomMsgUser, []string{f.card1}, 0, false)
	if got := mustSessionCandidates(t, s, u5MemberSeat, 0, u5NoListMax, 100); len(got) != 0 {
		t.Fatalf("席位属 other 时 @C1 不得成为 member 候选：%v", seqsOf(got))
	}
	// 单向换绑给 member：同一条消息进入候选。
	if err := s.RebindSeat(f.card1, u5MemberSeat, proto.SeatSourceBind, u5OtherSeat, SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	if got := mustSessionCandidates(t, s, u5MemberSeat, 0, u5NoListMax, 100); len(got) != 1 || got[0].Seq != seqCard {
		t.Fatalf("席位回到 member 后 @C1 必须成为候选：%v", seqsOf(got))
	}
	// ABA（member→other→member）：集合值回到旧状态，但候选按当前状态仍含 @C1。
	if err := s.RebindSeat(f.card1, u5OtherSeat, proto.SeatSourceBind, u5MemberSeat, SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	if err := s.RebindSeat(f.card1, u5MemberSeat, proto.SeatSourceBind, u5OtherSeat, SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	if got := mustSessionCandidates(t, s, u5MemberSeat, 0, u5NoListMax, 100); len(got) != 1 {
		t.Fatalf("ABA 回到 member 席位后候选必须恢复：%v", seqsOf(got))
	}
}

// 分页边界：from 排他、to 包含、limit 截尾升序；续页从上一页末条 seq 起，
// 不跳不重（契约第 22–25 条）。
func TestSessionMessageCandidatesPagingBounds(t *testing.T) {
	f := newU5Fixture(t)
	s := f.s
	var sent []int64
	for i := 0; i < 5; i++ {
		sent = append(sent, f.u5Send(t, "u5-tester", "session:1", proto.RoomMsgUser, []string{u5Member}, 0, false))
	}
	fence := sent[len(sent)-1]

	page1 := mustSessionCandidates(t, s, u5Member, 0, fence, 3)
	if len(page1) != 3 {
		t.Fatalf("首页应截到 3 条：%v", seqsOf(page1))
	}
	page2 := mustSessionCandidates(t, s, u5Member, page1[2].Seq, fence, 3)
	merged := append(seqsOf(page1), seqsOf(page2)...)
	if len(merged) != 5 {
		t.Fatalf("两页合并必须恰 5 条（不跳不重）：%v", merged)
	}
	for i, seq := range merged {
		if seq != sent[i] {
			t.Fatalf("合并序漂移 @%d：%v want %v", i, merged, sent)
		}
	}
	// to 为包含上界：从 fence-1 起读只含最后一条。
	tail := mustSessionCandidates(t, s, u5Member, fence-1, fence, 10)
	if len(tail) != 1 || tail[0].Seq != fence {
		t.Fatalf("排他下界/包含上界错：%v", seqsOf(tail))
	}
	// to=0 表示无上界（follow 模式）。
	unbounded := mustSessionCandidates(t, s, u5Member, fence-1, 0, 10)
	if len(unbounded) != 1 || unbounded[0].Seq != fence {
		t.Fatalf("to=0 必须无上界：%v", seqsOf(unbounded))
	}
	// 空窗口：无候选无错误。
	empty := mustSessionCandidates(t, s, u5Member, fence, fence, 10)
	if len(empty) != 0 {
		t.Fatalf("空窗口必须返回空：%v", seqsOf(empty))
	}
	if _, err := s.SessionMessageCandidatesContext(context.Background(), u5Member, 0, 10, 0); err == nil {
		t.Fatal("limit<=0 必须报参数错误（账本层不复制调用方默认值）")
	}
}

// member 隔离：A 的候选永远不含只 @B / 回复 B 的行（反例：任一 member 读到
// 另一 member 的定向消息）。
func TestSessionMessageCandidatesMemberIsolation(t *testing.T) {
	f := newU5Fixture(t)
	otherMsg := f.u5Send(t, "u5-tester", "session:1", proto.RoomMsgUser, []string{u5Other}, 0, false)
	replyToOther := f.u5Send(t, "u5-tester", "session:1", proto.RoomMsgUser, nil, otherMsg, false)
	got := mustSessionCandidates(t, f.s, u5Member, 0, u5NoListMax, 100)
	for _, ev := range got {
		if ev.Seq == otherMsg || ev.Seq == replyToOther {
			t.Fatalf("member 隔离破口：seq=%d 出现在 %s 的候选", ev.Seq, u5Member)
		}
	}
}

// PG 方言同构：同一覆盖金样（契约第 51–55 条）。
func TestPGSessionMessageCandidatesSameGolden(t *testing.T) {
	f := newU5FixturePG(t)
	s := f.s

	seqIdentity := f.u5Send(t, f.run, "session:1", proto.RoomMsgUser, []string{u5Member}, 0, false)
	seqMemberCard := f.u5Send(t, f.run, "session:1", proto.RoomMsgUser, []string{f.card1}, 0, false)
	memberMsg := f.u5Send(t, u5Member, "session:1", proto.RoomMsgUser, nil, 0, false)
	f.u5Send(t, f.run, "session:1", proto.RoomMsgUser, []string{f.card2}, 0, false)
	seqReplyMember := f.u5Send(t, f.run, "session:1", proto.RoomMsgUser, nil, memberMsg, false)
	f.u5Send(t, f.run, "session:1", proto.RoomMsgUser, nil, 0, false)

	gotSy := seqsOf(mustSessionCandidates(t, s, u5Member, 0, u5NoListMax, 100))
	wantSy := []int64{seqIdentity, seqReplyMember}
	if len(gotSy) != len(wantSy) {
		t.Fatalf("PG user 候选集不符：got=%v want=%v", gotSy, wantSy)
	}
	for i := range wantSy {
		if gotSy[i] != wantSy[i] {
			t.Fatalf("PG user 候选序不符 @%d：got=%v want=%v", i, gotSy, wantSy)
		}
	}
	gotSeat := seqsOf(mustSessionCandidates(t, s, u5MemberSeat, 0, u5NoListMax, 100))
	if len(gotSeat) != 1 || gotSeat[0] != seqMemberCard {
		t.Fatalf("PG 席位身份候选错：%v want=[%d]", gotSeat, seqMemberCard)
	}
}

// —— 收件箱限域读金样（B156.2 语义的限域取数面；裁决/归并仍在 collab）——

func mustMentionCandidates(t *testing.T, s *Store, member, roomID string, cardlessOnly bool, afterSeq int64, limit int) []Event {
	t.Helper()
	events, err := s.MentionCandidatesContext(context.Background(), member, roomID, cardlessOnly, afterSeq, limit)
	if err != nil {
		t.Fatalf("提及候选读: %v", err)
	}
	return events
}

// MentionCandidates：mentions∋member 的 room_message 超集；roomID 限房间时
// 返回 SameRoom 的 SQL 超集（卡行按 card_id、无卡行按 payload.room）；afterSeq
// 排他；limit 截尾升序。kind/by_system 不在 SQL 过滤——那是 collab 的裁决。
func TestMentionCandidatesBoundedRead(t *testing.T) {
	f := newU5Fixture(t)
	s := f.s

	seqHit := f.u5Send(t, "u5-tester", "session:1", proto.RoomMsgUser, []string{u5Member}, 0, false)
	f.u5Send(t, "u5-tester", "session:1", proto.RoomMsgUser, []string{u5Other}, 0, false)
	seqCardRoom := f.u5SendCardRoom(t, f.card1, []string{u5Member})

	// 全房间（Mentions 源）：卡房间行 + 无卡行都在。
	all := mustMentionCandidates(t, s, u5Member, "", false, 0, 10)
	if len(all) != 2 || all[0].Seq != seqHit || all[1].Seq != seqCardRoom {
		t.Fatalf("全房间提及候选错：%v", seqsOf(all))
	}
	// 仅无卡（Pending 的群级源）：只剩会话/群房间行。
	cardless := mustMentionCandidates(t, s, u5Member, "", true, 0, 10)
	if len(cardless) != 1 || cardless[0].Seq != seqHit {
		t.Fatalf("无卡提及候选错：%v", seqsOf(cardless))
	}
	// 房间限定（consumeRoomMentions 源）：SameRoom 超集 = 卡行 + 同号无卡行。
	inRoom := mustMentionCandidates(t, s, u5Member, f.card1, false, 0, 10)
	if len(inRoom) != 1 || inRoom[0].Seq != seqCardRoom {
		t.Fatalf("房间限定提及候选错：%v", seqsOf(inRoom))
	}
	// afterSeq 排他。
	after := mustMentionCandidates(t, s, u5Member, "", false, seqHit, 10)
	if len(after) != 1 || after[0].Seq != seqCardRoom {
		t.Fatalf("afterSeq 排他错：%v", seqsOf(after))
	}
	// limit 截尾 + 参数执法。
	limited := mustMentionCandidates(t, s, u5Member, "", false, 0, 1)
	if len(limited) != 1 {
		t.Fatalf("limit 截尾错：%v", seqsOf(limited))
	}
	if _, err := s.MentionCandidatesContext(context.Background(), u5Member, "", false, 0, 0); err == nil {
		t.Fatal("limit<=0 必须报参数错误")
	}
}

// CardRoomUserMessages：绑定卡房间内 kind=user 的行（Pending 第二源）；
// 非 user kind（escalation/pointer/reply）不在列。
func TestCardRoomUserMessagesBoundedRead(t *testing.T) {
	f := newU5Fixture(t)
	s := f.s

	seqUser := f.u5SendCardRoom(t, f.card1, nil)
	f.u5Send(t, "u5-tester", "session:1", proto.RoomMsgUser, nil, 0, false) // 无卡房间：不属于任何卡
	if _, err := s.RecordRoomMessage(f.card1, proto.RoomMessage{Room: f.card1, Kind: proto.RoomMsgEscalation, Body: "简报"}, "u5-tester"); err != nil {
		t.Fatal(err)
	}
	// 只查 C1：恰好一条 user 行；C2 无消息。
	got, err := s.CardRoomUserMessagesContext(context.Background(), []string{f.card1}, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Seq != seqUser {
		t.Fatalf("绑定卡用户消息错：%v", seqsOf(got))
	}
	got2, err := s.CardRoomUserMessagesContext(context.Background(), []string{f.card1, f.card2}, 0, 10)
	if err != nil || len(got2) != 1 {
		t.Fatalf("多卡合读错：%v err=%v", seqsOf(got2), err)
	}
	got3, err := s.CardRoomUserMessagesContext(context.Background(), nil, 0, 10)
	if err != nil || len(got3) != 0 {
		t.Fatalf("空卡集必须返回空：%v err=%v", seqsOf(got3), err)
	}
}

// ConsumedMessageSeqs：按 consumer 批量取已消费 message_seq；afterSeq 是安全
// 上界（标记事件 seq 必大于被标记消息 seq）；载荷才是权威（actor 粗筛 + 载荷
// 精确匹配，ledger/rooms.go 同款）。
func TestConsumedMessageSeqsBatchRead(t *testing.T) {
	f := newU5Fixture(t)
	s := f.s

	msgA := f.u5Send(t, "u5-tester", "session:1", proto.RoomMsgUser, []string{u5Member}, 0, false)
	msgB := f.u5Send(t, "u5-tester", "session:1", proto.RoomMsgUser, []string{u5Member}, 0, false)
	if err := s.RecordMessageConsumed("", msgA, u5Member); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordMessageConsumed("", msgB, u5Other); err != nil {
		t.Fatal(err)
	}
	seqs, err := s.ConsumedMessageSeqsContext(context.Background(), u5Member, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(seqs) != 1 || seqs[0] != msgA {
		t.Fatalf("consumed 批量读错：%v", seqs)
	}
	// afterSeq 过滤：标记事件 seq 必大于被标记消息 seq，故 afterSeq=msgA 仍取得到它。
	after, err := s.ConsumedMessageSeqsContext(context.Background(), u5Member, msgA)
	if err != nil || len(after) != 1 || after[0] != msgA {
		t.Fatalf("consumed afterSeq 过滤错：%v err=%v", after, err)
	}
}

// EventBySeq：seq 点读（Consume 定位卡号的限域替身——不再全流扫描）。
func TestEventBySeqPointRead(t *testing.T) {
	f := newU5Fixture(t)
	s := f.s

	msg := f.u5Send(t, "u5-tester", "session:1", proto.RoomMsgUser, []string{u5Member}, 0, false)
	ev, ok, err := s.EventBySeq(msg)
	if err != nil || !ok {
		t.Fatalf("点读已存在 seq: ok=%v err=%v", ok, err)
	}
	if ev.Seq != msg || ev.Type != "room_message" {
		t.Fatalf("点读内容错：%+v", ev)
	}
	if _, ok, err := s.EventBySeq(msg + 1000); err != nil || ok {
		t.Fatalf("点读不存在 seq: ok=%v err=%v", ok, err)
	}
}
