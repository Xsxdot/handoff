package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

func TestB409RoomMessageSnapshotsAreScopedAndStableAcrossUnrelatedGrowth(t *testing.T) {
	st := newTestStore(t)
	seedProjectionWorkflow(t, st)
	card, err := st.CreateCard(NewCard{Title: "目标卡", Project: "handoff", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateCard(NewCard{Title: "无关卡", Project: "other", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	first := appendRoomProjectionEvent(t, st, card.ID, "payload-room-is-ignored", "较早活动", base.Add(time.Minute))
	appendRoomProjectionEvent(t, st, "", "project:handoff", "项目群旧消息", base)
	latestCard := appendRoomProjectionEvent(t, st, card.ID, "payload-room-is-ignored", "按 seq 最新", base.Add(-time.Minute))
	appendRoomProjectionEvent(t, st, other.ID, "global", "无关卡 payload 指向全局，但归属仍是卡房间", base.Add(time.Hour))
	globalSeq := appendRoomProjectionEvent(t, st, "", "global", "全局消息", base.Add(2*time.Minute))
	appendNonMessageEvent(t, st, card.ID, EvNeedsHuman, `{"reason":"not a room message"}`, base.Add(3*time.Minute))

	got, err := st.RoomMessageSnapshotsContext(context.Background(), map[string]int64{
		card.ID:           first,
		"project:handoff": globalSeq,
		"global":          globalSeq - 1,
		"missing-room":    0,
	})
	if err != nil {
		t.Fatalf("RoomMessageSnapshotsContext: %v", err)
	}
	byRoom := snapshotByRoom(got)
	if len(byRoom) != 3 {
		t.Fatalf("仅返回有消息的目标房间，每个房间一行: got %d rows (%+v)", len(byRoom), got)
	}
	cardSnapshot, ok := byRoom[card.ID]
	if !ok || cardSnapshot.Latest.Seq != latestCard || string(cardSnapshot.Latest.Payload) == "" {
		t.Fatalf("卡房间取最大 seq 的消息，忽略 payload.Room 的误导值: %+v", cardSnapshot)
	}
	if cardSnapshot.MessagesAfter != 1 {
		t.Fatalf("卡房间按独立游标计数: got %d want 1", cardSnapshot.MessagesAfter)
	}
	if !cardSnapshot.LastActivity.Equal(base.Add(time.Minute)) {
		t.Fatalf("LastActivity 保持消息创建时间最大值，而非最大 seq 的时间: got %s", cardSnapshot.LastActivity)
	}
	projectSnapshot := byRoom["project:handoff"]
	if projectSnapshot.Latest.Seq == 0 || projectSnapshot.MessagesAfter != 0 {
		t.Fatalf("项目房间 latest/count 错误: %+v", projectSnapshot)
	}
	globalSnapshot := byRoom["global"]
	if globalSnapshot.Latest.Seq != globalSeq || globalSnapshot.MessagesAfter != 1 {
		t.Fatalf("全局房间 latest/count 错误: %+v", globalSnapshot)
	}

	baselineRows, baselinePayloadBytes := snapshotRowsAndPayloadBytes(got)
	insertUnrelatedPayloadRows(t, st, 20_000, 1024)
	afterGrowth, err := st.RoomMessageSnapshotsContext(context.Background(), map[string]int64{
		card.ID: first, "project:handoff": globalSeq, "global": globalSeq - 1, "missing-room": 0,
	})
	if err != nil {
		t.Fatalf("增长后 RoomMessageSnapshotsContext: %v", err)
	}
	growthRows, growthPayloadBytes := snapshotRowsAndPayloadBytes(afterGrowth)
	if growthRows != baselineRows || growthPayloadBytes != baselinePayloadBytes {
		t.Fatalf("+20k 无关行改变了传给 collab 的结果: rows %d→%d payload_bytes %d→%d",
			baselineRows, growthRows, baselinePayloadBytes, growthPayloadBytes)
	}
	if len(snapshotByRoom(afterGrowth)) != 3 || snapshotByRoom(afterGrowth)[card.ID].Latest.Seq != latestCard {
		t.Fatalf("无关历史增长改变了可见快照: %+v", afterGrowth)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := st.RoomMessageSnapshotsContext(ctx, map[string]int64{card.ID: 0}); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消请求必须终止支持 context 的范围查询: %v", err)
	}
}

func TestB409SessionProjectionEventsOnlyReturnCurrentSessionFacts(t *testing.T) {
	st := newTestStore(t)
	seedProjectionWorkflow(t, st)
	first, err := st.CreateSession("第一会话", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateSession("另一会话", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	member, err := st.CreateCard(NewCard{Title: "当前成员", Project: "handoff", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := st.CreateCard(NewCard{Title: "外部成员", Project: "handoff", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.JoinCardToSession(first.ID, member.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	if err := st.JoinCardToSession(second.ID, foreign.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}

	want := []int64{}
	for _, event := range []struct {
		cardID string
		typ    string
		body   string
	}{
		{member.ID, EvTaskMirrored, `{"node":"implement","task_type":"running"}`},
		{member.ID, EvNeedsHuman, `{"reason":"blocked"}`},
		{member.ID, EvNeedsCleared, `{}`},
		{member.ID, EvDriverTakeover, `{"from":"old","to":"new"}`},
		{member.ID, EvDriverSeatBound, `{"to":"new"}`},
		{member.ID, EvStatusMoved, `{"to":"终止"}`},
		{member.ID, "comment", `{"text":"not in session projection"}`},
		{foreign.ID, EvTaskMirrored, `{"node":"foreign","task_type":"running"}`},
	} {
		seq := appendNonMessageEvent(t, st, event.cardID, event.typ, event.body, time.Now().UTC())
		if event.cardID == member.ID && event.typ != "comment" {
			want = append(want, seq)
		}
	}

	got, err := st.SessionProjectionEventsContext(context.Background(), first.ID, []string{member.ID})
	if err != nil {
		t.Fatalf("SessionProjectionEventsContext: %v", err)
	}
	seqs := make([]int64, 0, len(got))
	cardSeqs := make([]int64, 0, len(want))
	createdFound, joinedFound := false, false
	for _, event := range got {
		seqs = append(seqs, event.Seq)
		if event.CardID == member.ID {
			cardSeqs = append(cardSeqs, event.Seq)
		}
		createdFound = createdFound || event.Type == EvSessionCreated
		joinedFound = joinedFound || event.Type == EvSessionCardJoined
	}
	if len(got) != len(want)+2 || fmt.Sprint(cardSeqs) != fmt.Sprint(want) || !createdFound || !joinedFound {
		t.Fatalf("只返回当前卡投影与本会话结构事件: rows=%v card_rows=%v want_card_rows=%v created=%v joined=%v",
			seqs, cardSeqs, want, createdFound, joinedFound)
	}
	for _, event := range got {
		if event.Type == EvSessionCreated {
			var payload proto.SessionCreatedPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.ID != first.ID {
				t.Fatalf("session_created 必须按 id 字段限域: payload=%s err=%v", event.Payload, err)
			}
		}
		if event.Type == EvSessionCardJoined {
			var payload proto.SessionCardPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.Session != first.ID {
				t.Fatalf("session_card_joined 必须按 session 字段限域: payload=%s err=%v", event.Payload, err)
			}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := st.SessionProjectionEventsContext(ctx, first.ID, []string{member.ID}); !errors.Is(err, context.Canceled) {
		t.Fatalf("详情范围读取必须响应取消: %v", err)
	}
}

func TestB409LatestNeedsEventsReturnsOneCurrentStatePerCard(t *testing.T) {
	st := newTestStore(t)
	seedProjectionWorkflow(t, st)
	first, err := st.CreateCard(NewCard{Title: "已清除", Project: "handoff", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateCard(NewCard{Title: "仍需人工", Project: "handoff", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	appendNonMessageEvent(t, st, first.ID, EvNeedsHuman, `{"reason":"old"}`, time.Now().UTC())
	cleared := appendNonMessageEvent(t, st, first.ID, EvNeedsCleared, `{}`, time.Now().UTC())
	active := appendNonMessageEvent(t, st, second.ID, EvNeedsHuman, `{"reason":"active"}`, time.Now().UTC())
	appendNonMessageEvent(t, st, first.ID, "comment", `{"text":"noise"}`, time.Now().UTC())

	got, err := st.LatestNeedsEventsContext(context.Background(), []string{first.ID, second.ID})
	if err != nil {
		t.Fatalf("LatestNeedsEventsContext: %v", err)
	}
	if len(got) != 2 || got[0].Seq != cleared || got[1].Seq != active {
		t.Fatalf("应只返回各卡最新 needs 状态，按 seq 升序: got %+v want seq [%d %d]", got, cleared, active)
	}
}

func seedProjectionWorkflow(t *testing.T, st *Store) {
	t.Helper()
	if _, err := st.PutWorkflow("bug", WorkflowDef{Nodes: []NodeDef{
		{Name: StatusTodo, Next: StatusDoing},
		{Name: StatusDoing, Next: StatusDone},
		{Name: StatusDone},
	}}); err != nil {
		t.Fatal(err)
	}
}

func appendRoomProjectionEvent(t *testing.T, st *Store, cardID, roomID, body string, at time.Time) int64 {
	t.Helper()
	var seq int64
	err := st.mutate(func(tx *sql.Tx, sink *eventSink) error {
		var err error
		seq, err = st.appendEventAt(tx, sink, cardID, EvRoomMessage, "user:sy",
			proto.RoomMessage{Room: roomID, Kind: proto.RoomMsgUser, Body: body}, at)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return seq
}

func appendNonMessageEvent(t *testing.T, st *Store, cardID, typ, payload string, at time.Time) int64 {
	t.Helper()
	var seq int64
	err := st.mutate(func(tx *sql.Tx, sink *eventSink) error {
		var err error
		seq, err = st.appendEventAt(tx, sink, cardID, typ, "user:sy", json.RawMessage(payload), at)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return seq
}

func insertUnrelatedPayloadRows(t *testing.T, st *Store, count, payloadSize int) {
	t.Helper()
	noise := `{"noise":"` + strings.Repeat("x", payloadSize) + `"}`
	tx, err := st.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(st.q(`INSERT INTO card_events (card_id,type,actor,payload,created_at) VALUES (NULL,'comment','noise',?,?)`))
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for i := 0; i < count; i++ {
		if _, err := stmt.Exec(noise, st.tval(time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC))); err != nil {
			t.Fatalf("insert unrelated event %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func snapshotByRoom(snapshots []RoomMessageSnapshot) map[string]RoomMessageSnapshot {
	out := make(map[string]RoomMessageSnapshot, len(snapshots))
	for _, snapshot := range snapshots {
		out[snapshot.RoomID] = snapshot
	}
	return out
}

func snapshotRowsAndPayloadBytes(snapshots []RoomMessageSnapshot) (int, int) {
	bytes := 0
	for _, snapshot := range snapshots {
		bytes += len(snapshot.Latest.Payload)
	}
	return len(snapshots), bytes
}
