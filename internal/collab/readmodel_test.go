// 读模型测试（B156.2 C5）：Pending/Consume/Mentions 未消费、ListRooms、
// MarkRead/Unread、live 活性翻转。全部从入站门面（Service 方法）进入
// （spec 测试接缝清单 #1）；排序/筛选/翻转等需精确控制时钟与事件时刻的
// 逻辑测试用假 client（接缝 #2 测试替身），真实集成用真 SQLite。
//
// 本文件 import internal/ledger(/api) 仅存在于 _test.go：图边采集排除测试
// 文件，不构成 d_collab→d_ledger 生产边。
package collab

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/collab/room"
	"github.com/Xsxdot/handoff/internal/ledger"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/proto"
)

// fakeLC 是 client.LedgerClient 的测试替身（接缝 #2）：可控卡片、事件与租约，
// 供 ListRooms 排序/筛选/活性翻转等需要精确时刻的读模型测试使用。全部方法
// 保持与接口一一对应；未用到的写方法返回 nil（本文件不测它们）。
type fakeLC struct {
	cards      []proto.Card
	events     []proto.LedgerEvent
	leases     map[string]time.Time // session -> expiresAt
	eventReads int
}

func (f *fakeLC) GetCard(id string) (proto.Card, error) {
	for _, c := range f.cards {
		if c.ID == id {
			return c, nil
		}
	}
	return proto.Card{}, ledger.ErrNotFound
}
func (f *fakeLC) ListActiveCards(project string) ([]proto.Card, error) {
	var out []proto.Card
	for _, c := range f.cards {
		if project != "" && c.Project != project {
			continue
		}
		if room.IsTerminalStatus(c.Status) {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}
func (f *fakeLC) ListAllCards(project string) ([]proto.Card, error) {
	var out []proto.Card
	for _, c := range f.cards {
		if project != "" && c.Project != project {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}
func (f *fakeLC) RecordRoomMessage(cardID string, msg proto.RoomMessage, actor string) (int64, error) {
	return 0, nil
}
func (f *fakeLC) RecordMessageConsumed(cardID string, msgSeq int64, consumer string) error {
	return nil
}

// --- B358 会话（群）域账本能力（本文件不测会话；给出编译期占位实现）---

func (f *fakeLC) CreateSession(title, owner, actor string) (proto.Session, error) {
	return proto.Session{ID: "session:1", Title: title, Owner: owner}, nil
}
func (f *fakeLC) GetSession(id string) (proto.Session, error) {
	return proto.Session{ID: id}, nil
}
func (f *fakeLC) ListSessions() ([]proto.Session, error) { return nil, nil }
func (f *fakeLC) ArchiveSession(id, actor string) error  { return nil }
func (f *fakeLC) JoinCardToSession(sessionID, cardID, actor string) error {
	return nil
}
func (f *fakeLC) LeaveCardToSession(sessionID, cardID, actor string) error { return nil }
func (f *fakeLC) SessionOfCard(cardID string) (string, error)              { return "", nil }
func (f *fakeLC) AddSessionMember(sessionID, identity, actor string) error { return nil }
func (f *fakeLC) EventsFromAsc(cardIDs []string, fromSeq int64, limit int) ([]proto.LedgerEvent, error) {
	f.eventReads++
	if limit <= 0 {
		limit = 1000
	}
	var out []proto.LedgerEvent
	for _, ev := range f.events {
		if ev.Seq <= fromSeq {
			continue
		}
		if len(cardIDs) > 0 {
			hit := false
			for _, id := range cardIDs {
				if ev.CardID == id {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}
		out = append(out, ev)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// TestListRoomsForMemberScansEventsOnceForUnreadAndActivity 锁住列表性能接缝：
// 活动时间与成员未读必须复用同一次事件流扫描，不能退化为每个房间各读一遍。
// 205 张卡刻意超过验收门的 200+ 规模；计数断言比挂钟更稳定。
func TestListRoomsForMemberScansEventsOnceForUnreadAndActivity(t *testing.T) {
	base := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	fake := &fakeLC{leases: map[string]time.Time{}}
	for i := 0; i < 205; i++ {
		id := fmt.Sprintf("B%03d", i)
		fake.cards = append(fake.cards, proto.Card{
			ID: id, Title: id, Status: "进行中", Project: "p1",
			CreatedAt: base, UpdatedAt: base,
		})
		fake.events = append(fake.events, fakeRoomMsg(int64(i+1), id, base.Add(time.Duration(i)*time.Second)))
	}

	svc := New(fake)
	rooms, err := svc.ListRoomsForMember("p1", "web:test")
	if err != nil {
		t.Fatal(err)
	}
	if len(rooms) != 207 { // 205 卡 + 项目群 + 全员群
		t.Fatalf("房间数量: got %d want 207", len(rooms))
	}
	if fake.eventReads != 1 {
		t.Fatalf("列表应只扫描一次事件流，实际读取 %d 次", fake.eventReads)
	}
	for _, r := range rooms {
		if r.Kind == room.KindCard && r.Unread != 1 {
			t.Fatalf("卡房间 unread 应为 1: %+v", r)
		}
		if r.ID == "B000" {
			if r.Preview == nil || r.Preview.Body != "x" || r.Preview.Seq != 1 {
				t.Fatalf("列表应从同一事件扫描投影最后一条 preview: %+v", r.Preview)
			}
		}
	}
}

func TestListRoomsPreviewTruncatesBody(t *testing.T) {
	base := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	body := strings.Repeat("长", roomPreviewMaxRunes+20)
	fake := &fakeLC{
		cards:  []proto.Card{{ID: "B1", Title: "B1", Status: "进行中", Project: "p1", CreatedAt: base, UpdatedAt: base}},
		events: []proto.LedgerEvent{fakeRoomMsgWithBody(1, "B1", base, body)},
		leases: map[string]time.Time{},
	}
	rooms, err := New(fake).ListRooms("p1")
	if err != nil {
		t.Fatal(err)
	}
	if rooms[0].Preview == nil {
		t.Fatal("房间应有 preview 投影")
	}
	want := strings.Repeat("长", roomPreviewMaxRunes-1) + "…"
	if rooms[0].Preview.Body != want {
		t.Fatalf("preview 正文应保留省略号并截断到 %d rune: got %q want %q", roomPreviewMaxRunes, rooms[0].Preview.Body, want)
	}
}

func TestListRoomsPreviewKeepsLatestEventMetadata(t *testing.T) {
	base := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	latest := base.Add(2 * time.Minute)
	fake := &fakeLC{
		cards: []proto.Card{{ID: "B1", Title: "B1", Status: "进行中", Project: "p1", CreatedAt: base, UpdatedAt: base}},
		events: []proto.LedgerEvent{
			fakeRoomMsgWithBody(1, "B1", base.Add(time.Minute), "旧预览"),
			fakeRoomMsgWithBody(2, "B1", latest, "最新预览"),
		},
		leases: map[string]time.Time{},
	}
	rooms, err := New(fake).ListRooms("p1")
	if err != nil {
		t.Fatal(err)
	}
	preview := rooms[0].Preview
	if preview == nil || preview.Body != "最新预览" || preview.Seq != 2 || !preview.CreatedAt.Equal(latest) {
		t.Fatalf("preview 必须保留最后一条正文、seq 与 created_at: %+v", preview)
	}
}

// TestHistoryFiltersAfterReadingTheWholeEventStream 锁住分页顺序：账本的 LIMIT
// 作用在事件流读取上，不能先截掉前 1000 条再做房间过滤，否则第 1001 条目标房间
// 消息会永久从详情页消失。
func TestHistoryFiltersAfterReadingTheWholeEventStream(t *testing.T) {
	base := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	fake := &fakeLC{}
	for i := 1; i <= 1000; i++ {
		fake.events = append(fake.events, fakeRoomMsg(int64(i), "B-noise", base.Add(time.Duration(i)*time.Second)))
	}
	fake.events = append(fake.events, fakeRoomMsg(1001, "B-target", base.Add(1001*time.Second)))

	history, err := New(fake).History("B-target", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Seq != 1001 {
		t.Fatalf("目标房间第 1001 条消息必须可见: %+v", history)
	}
}

func (f *fakeLC) BindDriver(id, session, carrier, expect string) error {
	return nil
}
func (f *fakeLC) DriverLease(session string) (time.Time, bool, error) {
	exp, ok := f.leases[session]
	return exp, ok, nil
}

// fakeRoomMsg 造一条房间消息事件（群房间 roomID 带 project:/global 前缀时
// 为无卡事件，同 ledger.RecordRoomMessage 语义）。
func fakeRoomMsg(seq int64, roomID string, at time.Time) proto.LedgerEvent {
	return fakeRoomMsgWithBody(seq, roomID, at, "x")
}

func fakeRoomMsgWithBody(seq int64, roomID string, at time.Time, body string) proto.LedgerEvent {
	payload, _ := json.Marshal(proto.RoomMessage{Room: roomID, Kind: proto.RoomMsgUser, Body: body})
	cardID := roomID
	if roomID == "global" || len(roomID) > 8 && roomID[:8] == "project:" {
		cardID = ""
	}
	return proto.LedgerEvent{Seq: seq, CardID: cardID, Type: room.RoomEventType, Actor: "user:sy", Payload: payload, CreatedAt: at}
}

// countConsumedFor 数该消费者全流的 message_consumed 标记数。
func countConsumedFor(t *testing.T, st *ledger.Store, consumer string) int {
	t.Helper()
	events, err := st.EventsFromAsc([]string{}, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range events {
		if ev.Type == ledger.EvMessageConsumed && ev.Actor == consumer {
			n++
		}
	}
	return n
}

// TestPendingGroupMentionAndConsume 契约 §4「Pending 返回群级@」：@ 到
// consumer 的未消费群消息进 Pending；消费后消失。夹具迁会话房间（会话消息
// 同为无卡事件，Pending 的 CardID=="" 分支同样覆盖）。
func TestPendingGroupMentionAndConsume(t *testing.T) {
	svc, st := newFixture(t)
	sid := mustSession(t, svc, st, "群提及会话")
	consumer := "cli:codex#a"
	seq, err := svc.Send(sid,
		proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@cli:codex#a 看", Mentions: []string{consumer}}, "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Send(sid,
		proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "无提及"}, "user:sy"); err != nil {
		t.Fatal(err)
	}
	pending, err := svc.Pending(consumer)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Seq != seq {
		t.Fatalf("Pending 应只含 @ 到 consumer 的未消费群消息: %+v", pending)
	}
	if err := svc.Consume(seq, consumer); err != nil {
		t.Fatal(err)
	}
	after, err := svc.Pending(consumer)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 0 {
		t.Fatalf("消费后 Pending 应清空: %+v", after)
	}
}

// TestPendingCardRoomUserMessages Pending 的绑定卡房间读侧过滤（B156.2 语义
// 残余：旧房间归档后不再有新留言，历史行仍应可查——分支不删）。卡房间发言
// 入口已死，历史行用出站接口直接落（测试文件 import，不构成生产边）。
func TestPendingCardRoomUserMessages(t *testing.T) {
	svc, st := newFixture(t)
	lc := ledgerapi.New(st)
	consumer := "cli:codex#a"
	bound := mustCard(t, svc, st, "绑定卡")
	other := mustCard(t, svc, st, "非绑定卡")
	mustBind(t, st, bound.ID, consumer)
	mustBind(t, st, other.ID, "cli:codex#b")

	msgSeq, err := lc.RecordRoomMessage(bound.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "给我留言"}, "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lc.RecordRoomMessage(other.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "别人的卡留言"}, "user:sy"); err != nil {
		t.Fatal(err)
	}
	if _, err := lc.RecordRoomMessage(bound.ID, proto.RoomMessage{Kind: proto.RoomMsgEscalation, Body: "简报"}, consumer); err != nil {
		t.Fatal(err)
	}
	pending, err := svc.Pending(consumer)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Seq != msgSeq {
		t.Fatalf("Pending 应只含绑定卡房间的未消费 user 留言: %+v", pending)
	}
}

// TestConsumeIdempotentSameArgs 契约 §4「同参重试返回 nil 且不产生第二条」
// （夹具迁会话房间）。
func TestConsumeIdempotentSameArgs(t *testing.T) {
	svc, st := newFixture(t)
	sid := mustSession(t, svc, st, "幂等消费会话")
	seq, err := svc.Send(sid, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "留言"}, "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	consumer := "cli:codex#a"
	if err := svc.Consume(seq, consumer); err != nil {
		t.Fatal(err)
	}
	if err := svc.Consume(seq, consumer); err != nil {
		t.Fatalf("同参重试应幂等 nil，got %v", err)
	}
	if n := countConsumedFor(t, st, consumer); n != 1 {
		t.Fatalf("同参重试不得产生第二条标记: %d", n)
	}
}

// TestConsumeSecondConsumerOwnMarker 各消费者一条自己的标记，互不顶替
// （夹具迁会话房间）。
func TestConsumeSecondConsumerOwnMarker(t *testing.T) {
	svc, st := newFixture(t)
	sid := mustSession(t, svc, st, "双消费者会话")
	seq, _ := svc.Send(sid, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "留言"}, "user:sy")
	if err := svc.Consume(seq, "cli:codex#a"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Consume(seq, "cli:codex#b"); err != nil {
		t.Fatalf("他人已消费的同一条对新消费者是首次写入，got %v", err)
	}
	if n := countConsumedFor(t, st, "cli:codex#a"); n != 1 {
		t.Fatalf("A 应恰一条: %d", n)
	}
	if n := countConsumedFor(t, st, "cli:codex#b"); n != 1 {
		t.Fatalf("B 应恰一条: %d", n)
	}
}

// TestConsumeInvalidSeq seq 不存在或非 room_message 一律幂等 nil 且不落标记
// （夹具迁会话房间；评论行仍挂卡上）。
func TestConsumeInvalidSeq(t *testing.T) {
	svc, st := newFixture(t)
	sid := mustSession(t, svc, st, "无效消费会话")
	card := mustAnyCard(t, svc, st)
	if _, err := st.AddComment(card.ID, "普通评论", "普通", "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Send(sid, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "留言"}, "user:sy"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Consume(999999, "cli:codex#a"); err != nil {
		t.Fatalf("不存在的 seq 应幂等 nil，got %v", err)
	}
	events, err := st.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var commentSeq int64
	for _, ev := range events {
		if ev.Type == ledger.EvComment {
			commentSeq = ev.Seq
		}
	}
	if err := svc.Consume(commentSeq, "cli:codex#a"); err != nil {
		t.Fatalf("非 room_message seq 应幂等 nil，got %v", err)
	}
	if n := countConsumedFor(t, st, "cli:codex#a"); n != 0 {
		t.Fatalf("无效 seq 不得落标记: %d", n)
	}
}

// TestConsumePayloadTwoKeys 消费标记 payload 恰 message_seq/consumer 两键
// （夹具迁会话房间）。
func TestConsumePayloadTwoKeys(t *testing.T) {
	svc, st := newFixture(t)
	sid := mustSession(t, svc, st, "消费载荷会话")
	seq, _ := svc.Send(sid, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "留言"}, "user:sy")
	if err := svc.Consume(seq, "cli:codex#a"); err != nil {
		t.Fatal(err)
	}
	events, err := st.EventsFromAsc([]string{}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Type != ledger.EvMessageConsumed {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(ev.Payload, &m); err != nil {
			t.Fatal(err)
		}
		if len(m) != 2 || m["message_seq"] != float64(seq) || m["consumer"] != "cli:codex#a" {
			t.Fatalf("消费标记 payload 应恰 message_seq/consumer 两键: %v", m)
		}
		return
	}
	t.Fatal("没有消费标记事件")
}

// TestMentionsExcludesConsumed 未消费提及：消费后从 Mentions 消失（夹具迁
// 会话房间——会话 mention 源是 B358 的活语义）。
func TestMentionsExcludesConsumed(t *testing.T) {
	svc, st := newFixture(t)
	sid := mustSession(t, svc, st, "提及消费会话")
	seq, err := svc.Send(sid,
		proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@B145 看", Mentions: []string{"B145"}}, "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	hit, err := svc.Mentions("B145", 0, 0)
	if err != nil || len(hit) != 1 {
		t.Fatalf("未消费提及应命中: %v %d", err, len(hit))
	}
	if err := svc.Consume(seq, "B145"); err != nil {
		t.Fatal(err)
	}
	after, err := svc.Mentions("B145", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 0 {
		t.Fatalf("消费后 Mentions 应清空: %+v", after)
	}
}

// TestListRoomsSortsByActivityDescAndProjectFilter 契约 §4「LastActivity 降序、
// project 过滤、global 恒在」。
func TestListRoomsSortsByActivityDescAndProjectFilter(t *testing.T) {
	base := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	fake := &fakeLC{
		cards: []proto.Card{
			{ID: "B1", Title: "卡一", Status: "进行中", Project: "p1", CreatedAt: base, UpdatedAt: base},
			{ID: "B2", Title: "卡二", Status: "进行中", Project: "p1", CreatedAt: base, UpdatedAt: base},
			{ID: "B3", Title: "卡三", Status: "进行中", Project: "p2", CreatedAt: base, UpdatedAt: base},
		},
		events: []proto.LedgerEvent{
			fakeRoomMsg(1, "B1", base.Add(1*time.Hour)),
			fakeRoomMsg(2, "project:p1", base.Add(2*time.Hour)),
			fakeRoomMsg(3, "B2", base.Add(3*time.Hour)),
		},
	}
	svc := New(fake)
	all, err := svc.ListRooms("")
	if err != nil {
		t.Fatal(err)
	}
	wantAll := []string{"B2", "project:p1", "B1", "B3", "project:p2", "global"}
	if len(all) != len(wantAll) {
		t.Fatalf("列表条目数不符: %+v", all)
	}
	for i, id := range wantAll {
		if all[i].ID != id {
			t.Fatalf("第 %d 项应为 %s: %+v", i, id, all)
		}
	}
	p1, err := svc.ListRooms("p1")
	if err != nil {
		t.Fatal(err)
	}
	wantP1 := []string{"B2", "project:p1", "B1", "global"}
	if len(p1) != len(wantP1) {
		t.Fatalf("p1 筛选条目数不符: %+v", p1)
	}
	for i, id := range wantP1 {
		if p1[i].ID != id {
			t.Fatalf("p1 第 %d 项应为 %s: %+v", i, id, p1)
		}
	}
}

// TestListRoomsTerminalSinks 契约 §4「终态卡沉底」：有活动的终态卡房间沉到
// 列表尾部且只读。
func TestListRoomsTerminalSinks(t *testing.T) {
	base := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	fake := &fakeLC{
		cards: []proto.Card{
			{ID: "BA", Title: "活跃卡", Status: "进行中", Project: "p1", CreatedAt: base, UpdatedAt: base},
			{ID: "BT", Title: "终态卡", Status: ledger.StatusDone, Project: "p1", CreatedAt: base, UpdatedAt: base.Add(2 * time.Hour)},
		},
		events: []proto.LedgerEvent{
			fakeRoomMsg(1, "BT", base.Add(3*time.Hour)),
			fakeRoomMsg(2, "BA", base.Add(1*time.Hour)),
		},
	}
	svc := New(fake)
	rooms, err := svc.ListRooms("")
	if err != nil {
		t.Fatal(err)
	}
	if len(rooms) != 4 {
		t.Fatalf("条目数: %+v", rooms)
	}
	if rooms[0].ID != "BA" {
		t.Fatalf("活跃卡应排最前: %+v", rooms)
	}
	last := rooms[len(rooms)-1]
	if last.ID != "BT" || !last.ReadOnly {
		t.Fatalf("终态卡应沉底且只读: %+v", rooms)
	}
}

// TestListRoomsReadOnlyFlags 契约 §4「merged_into 非空 / 终态 → ReadOnly」。
func TestListRoomsReadOnlyFlags(t *testing.T) {
	base := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	fake := &fakeLC{
		cards: []proto.Card{
			{ID: "B1", Title: "普通卡", Status: "进行中", Project: "p1", CreatedAt: base, UpdatedAt: base},
			{ID: "B2", Title: "并入卡", Status: "进行中", Project: "p1", Following: "B9", CreatedAt: base, UpdatedAt: base},
			{ID: "B3", Title: "终态卡", Status: ledger.StatusClosed, Project: "p1", CreatedAt: base, UpdatedAt: base},
		},
	}
	svc := New(fake)
	rooms, err := svc.ListRooms("")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]proto.RoomSummary{}
	for _, r := range rooms {
		byID[r.ID] = r
	}
	if byID["B1"].ReadOnly {
		t.Fatal("普通卡不应只读")
	}
	if !byID["B2"].ReadOnly {
		t.Fatal("并入卡应只读")
	}
	if !byID["B3"].ReadOnly {
		t.Fatal("终态卡应只读")
	}
}

// TestListRoomsLiveFlip 活性翻转：租约未过期 live=true、过期后 live=false。
// 假 client 的租约 expiresAt 与 collab 注入时钟（nowFn）取自同一可拨源 cur，
// 基线选在过去：变异把判据改读 time.Now() 时真实时钟恒在 expiresAt 之后，
// live=true 断言必然翻红——两侧时钟不同源的假绿被结构上排除（协调者 C1
// 同族教训）。变异靶见 plan 变异①。
func TestListRoomsLiveFlip(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cur := base
	oldNow := nowFn
	nowFn = func() time.Time { return cur }
	t.Cleanup(func() { nowFn = oldNow })

	fake := &fakeLC{
		cards: []proto.Card{
			{ID: "B1", Title: "绑定卡", Status: "进行中", Project: "p1", DriverSession: "cli:codex#a", CreatedAt: base, UpdatedAt: base},
		},
		leases: map[string]time.Time{"cli:codex#a": base.Add(5 * time.Minute)},
	}
	svc := New(fake)

	rooms, err := svc.ListRooms("")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]proto.RoomSummary{}
	for _, r := range rooms {
		byID[r.ID] = r
	}
	if !byID["B1"].Live || byID["B1"].BoundSession != "cli:codex#a" {
		t.Fatalf("租约未过期应 live=true: %+v", byID["B1"])
	}
	cur = base.Add(10 * time.Minute) // 同一可拨源前拨
	rooms2, err := svc.ListRooms("")
	if err != nil {
		t.Fatal(err)
	}
	byID2 := map[string]proto.RoomSummary{}
	for _, r := range rooms2 {
		byID2[r.ID] = r
	}
	if byID2["B1"].Live {
		t.Fatalf("租约过期后应 live=false: %+v", byID2["B1"])
	}
}

// TestListRoomsLiveRealStore 真实集成：未续租 live=false；有效租约 live=true；
// 过期租约 live=false（真实时钟下确定性：+5m 缓冲、负 TTL 已过期）。
func TestListRoomsLiveRealStore(t *testing.T) {
	svc, st := newFixture(t)
	card := mustAnyCard(t, svc, st)
	mustBind(t, st, card.ID, "cli:codex#a")

	rooms, err := svc.ListRooms("")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]proto.RoomSummary{}
	for _, r := range rooms {
		byID[r.ID] = r
	}
	if byID[card.ID].Live {
		t.Fatal("未续租应 live=false")
	}
	if ok, err := st.RenewDriverLease("cli:codex#a", 5*time.Minute); err != nil || !ok {
		t.Fatalf("续租: %v %v", ok, err)
	}
	rooms2, err := svc.ListRooms("")
	if err != nil {
		t.Fatal(err)
	}
	byID2 := map[string]proto.RoomSummary{}
	for _, r := range rooms2 {
		byID2[r.ID] = r
	}
	if !byID2[card.ID].Live {
		t.Fatal("有效租约应 live=true")
	}
	if ok, err := st.RenewDriverLease("cli:codex#a", -time.Minute); err != nil || !ok {
		t.Fatalf("负 TTL 造过期行: %v %v", ok, err)
	}
	rooms3, err := svc.ListRooms("")
	if err != nil {
		t.Fatal(err)
	}
	byID3 := map[string]proto.RoomSummary{}
	for _, r := range rooms3 {
		byID3[r.ID] = r
	}
	if byID3[card.ID].Live {
		t.Fatal("过期租约应 live=false")
	}
}

// TestListRoomsUnmergeRestoresAndKeepsHistory 拆回解冻 + 历史不丢（旧房间
// 读面对质：Pointer 夹具造消息；Send 断言翻成「旧房间恒 ErrReadOnly」）。
func TestListRoomsUnmergeRestoresAndKeepsHistory(t *testing.T) {
	svc, st := newFixture(t)
	carrier := mustCard(t, svc, st, "承载卡")
	member := mustCard(t, svc, st, "并入卡")
	seq, err := svc.Pointer(member.ID, proto.RoomMessage{Body: "并入前指针行"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MergeCards([]string{member.ID}, carrier.ID, "test"); err != nil {
		t.Fatal(err)
	}
	rooms, err := svc.ListRooms("")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]proto.RoomSummary{}
	for _, r := range rooms {
		byID[r.ID] = r
	}
	if !byID[member.ID].ReadOnly {
		t.Fatal("并入卡房间应只读")
	}
	if _, err := svc.Send(member.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "x"}, "user:sy"); err != ErrReadOnly {
		t.Fatalf("旧房间发送应 ErrReadOnly（归档语义）: %v", err)
	}
	if err := st.UnmergeCard(member.ID, "test"); err != nil {
		t.Fatal(err)
	}
	rooms2, err := svc.ListRooms("")
	if err != nil {
		t.Fatal(err)
	}
	byID2 := map[string]proto.RoomSummary{}
	for _, r := range rooms2 {
		byID2[r.ID] = r
	}
	if byID2[member.ID].ReadOnly {
		t.Fatal("拆回后房间应解除只读")
	}
	history, err := svc.History(member.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Seq != seq {
		t.Fatalf("拆回后历史消息不丢: %+v", history)
	}
}

// TestMarkReadUnreadWatermark 未读水位（夹具迁会话房间；发送者=群主，
// Task 3 成员执法落地后不需改）。
func TestMarkReadUnreadWatermark(t *testing.T) {
	svc, st := newFixture(t)
	sid := mustSession(t, svc, st, "未读水位会话")
	seqs := []int64{}
	for _, body := range []string{"一", "二", "三"} {
		seq, err := svc.Send(sid, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: body}, "user:sy")
		if err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, seq)
	}
	if n, err := svc.Unread("user:sy", sid); err != nil || n != 3 {
		t.Fatalf("未读应 3: %v %d", err, n)
	}
	if err := svc.MarkRead("user:sy", sid, seqs[1]); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.Unread("user:sy", sid); err != nil || n != 1 {
		t.Fatalf("读到第二条后未读应 1: %v %d", err, n)
	}
	if err := svc.MarkRead("user:sy", sid, seqs[2]); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.Unread("user:sy", sid); err != nil || n != 0 {
		t.Fatalf("读尽后未读应 0: %v %d", err, n)
	}
}

// TestMarkReadPerRoomAndMember 游标按成员按房间独立（夹具迁会话房间）。
func TestMarkReadPerRoomAndMember(t *testing.T) {
	svc, st := newFixture(t)
	sidA := mustSession(t, svc, st, "会话A")
	sidB := mustSession(t, svc, st, "会话B")
	seqA, _ := svc.Send(sidA, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "a1"}, "user:sy")
	if _, err := svc.Send(sidB, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "b1"}, "user:sy"); err != nil {
		t.Fatal(err)
	}
	if err := svc.MarkRead("user:sy", sidA, seqA); err != nil {
		t.Fatal(err)
	}
	if n, _ := svc.Unread("user:sy", sidA); n != 0 {
		t.Fatalf("会话A 已读应 0: %d", n)
	}
	if n, _ := svc.Unread("user:sy", sidB); n != 1 {
		t.Fatalf("会话B 未读应 1: %d", n)
	}
	if n, _ := svc.Unread("cli:codex#a", sidA); n != 1 {
		t.Fatalf("另一成员游标独立，应 1: %d", n)
	}
}
