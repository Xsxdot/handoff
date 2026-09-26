// Package ledger 的房间历史读取契约测试：限域到目标房间、只取最近窗口，
// 并保持与 collab.room.RoomIDOf 相同的卡房间优先级。
package ledger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

func TestRoomHistoryReturnsNewestWindowInAscendingOrder(t *testing.T) {
	s := newTestStore(t)
	const roomID = "session:history-target"
	const otherRoomID = "session:history-other"
	targetSeqs := make([]int64, 0, 205)
	for i := 0; i < 205; i++ {
		if i%4 == 0 {
			if _, err := s.RecordRoomMessage("", proto.RoomMessage{
				Room: otherRoomID, Kind: proto.RoomMsgUser, Body: "unrelated",
			}, "test"); err != nil {
				t.Fatalf("写入无关房间消息: %v", err)
			}
		}
		seq, err := s.RecordRoomMessage("", proto.RoomMessage{
			Room: roomID, Kind: proto.RoomMsgUser, Body: fmt.Sprintf("target-%03d", i),
		}, "test")
		if err != nil {
			t.Fatalf("写入目标房间消息 %d: %v", i, err)
		}
		targetSeqs = append(targetSeqs, seq)
	}

	got, err := s.RoomMessagesBeforeContext(context.Background(), roomID, 0, 200)
	if err != nil {
		t.Fatalf("读房间历史: %v", err)
	}
	if len(got) != 200 {
		t.Fatalf("最近窗口应有 200 条，得到 %d", len(got))
	}
	for i, event := range got {
		wantSeq := targetSeqs[i+5]
		if event.Seq != wantSeq {
			t.Fatalf("升序窗口第 %d 条 seq = %d，期望 %d", i, event.Seq, wantSeq)
		}
	}

	before := targetSeqs[204]
	page, err := s.RoomMessagesBeforeContext(context.Background(), roomID, before, 20)
	if err != nil {
		t.Fatalf("按 beforeSeq 读房间历史: %v", err)
	}
	if len(page) != 20 {
		t.Fatalf("beforeSeq 前最近窗口应有 20 条，得到 %d", len(page))
	}
	for i, event := range page {
		wantSeq := targetSeqs[184+i]
		if event.Seq != wantSeq || event.Seq >= before {
			t.Fatalf("beforeSeq 窗口第 %d 条 seq = %d，期望排他 seq %d 之前的 %d", i, event.Seq, before, wantSeq)
		}
	}
}

func TestRoomHistoryUsesCardIDBeforePayloadRoom(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "房间身份优先级")
	if _, err := s.RecordRoomMessage(card.ID, proto.RoomMessage{
		Room: "session:payload-must-not-win", Kind: proto.RoomMsgUser, Body: "card room",
	}, "test"); err != nil {
		t.Fatalf("写入卡房间消息: %v", err)
	}
	if _, err := s.RecordRoomMessage("", proto.RoomMessage{
		Room: card.ID, Kind: proto.RoomMsgUser, Body: "legacy payload room",
	}, "test"); err != nil {
		t.Fatalf("写入旧式载荷房间消息: %v", err)
	}

	got, err := s.RoomMessagesBeforeContext(context.Background(), card.ID, 0, 200)
	if err != nil {
		t.Fatalf("读卡房间历史: %v", err)
	}
	if len(got) != 2 || got[0].CardID != card.ID || got[1].CardID != "" {
		t.Fatalf("卡房间 id 应优先于载荷 Room，且保留旧载荷房间：%+v", got)
	}

	wrongRoom, err := s.RoomMessagesBeforeContext(context.Background(), "session:payload-must-not-win", 0, 200)
	if err != nil {
		t.Fatalf("读载荷中的伪房间: %v", err)
	}
	if len(wrongRoom) != 0 {
		t.Fatalf("非空 card_id 的事件不得因 payload Room 被串入其它房间：%+v", wrongRoom)
	}
}

func TestRoomHistoryEmptyInvalidLimitAndCanceledContext(t *testing.T) {
	s := newTestStore(t)
	empty, err := s.RoomMessagesBeforeContext(context.Background(), "session:absent", 0, 200)
	if err != nil {
		t.Fatalf("空房间应正常返回: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("空房间应返回空切片，得到 %#v", empty)
	}
	if _, err := s.RoomMessagesBeforeContext(context.Background(), "session:absent", 0, 0); err == nil {
		t.Fatal("非正 limit 应返回错误，调用层负责应用默认值")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.RoomMessagesBeforeContext(ctx, "session:absent", 0, 200); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消必须传播到账本查询，得到 %v", err)
	}
}

func TestRoomHistoryPayloadPathUsesRoomIndex(t *testing.T) {
	s := newTestStore(t)
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN
		SELECT seq FROM card_events
		WHERE type = 'room_message' AND card_id IS NULL AND json_extract(payload, '$.room') = ?
		ORDER BY seq DESC LIMIT ?`, "session:index-probe", 200)
	if err != nil {
		t.Fatalf("读取房间历史查询计划: %v", err)
	}
	defer rows.Close()
	usedIndex := false
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("扫描查询计划: %v", err)
		}
		if strings.Contains(detail, "idx_room_messages_room_seq") {
			usedIndex = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历查询计划: %v", err)
	}
	if !usedIndex {
		t.Fatal("SQLite 未选择房间 payload 表达式索引，历史读取仍可能扫描无关消息")
	}
}

func TestB409ProjectionQueriesUseScopedIndexes(t *testing.T) {
	s := newTestStore(t)
	roomQuery, roomArgs := roomMessageSnapshotsQuery(`json_extract(payload, '$.room')`, []string{"B10001", "global"}, map[string]int64{"B10001": 2, "global": 3}, false)
	plans := []struct {
		name, query, index string
		args               []any
	}{
		{name: "room snapshot", query: roomQuery, args: roomArgs, index: "idx_room_messages_card_seq"},
		{name: "member card projection", query: `SELECT seq FROM card_events WHERE card_id IN (?)
			AND type IN ('task_mirrored','needs_human','needs_cleared','driver_takeover','driver_seat_bound','status_moved') ORDER BY seq ASC`,
			args: []any{"B10001"}, index: "idx_session_projection_card_seq"},
		{name: "latest needs", query: `SELECT seq FROM card_events WHERE card_id IN (?)
			AND type IN ('needs_human','needs_cleared') ORDER BY seq DESC`, args: []any{"B10001"}, index: "idx_needs_events_card_seq"},
		{name: "session created", query: `SELECT seq FROM card_events WHERE card_id IS NULL AND type = 'session_created'
			AND json_extract(payload, '$.id') = ?`, args: []any{"session:100"}, index: "idx_session_created_id_seq"},
		{name: "session structure", query: `SELECT seq FROM card_events WHERE card_id IS NULL
			AND type IN ('session_archived','session_card_joined','session_card_left')
			AND json_extract(payload, '$.session') = ?`, args: []any{"session:100"}, index: "idx_session_structure_session_seq"},
	}
	for _, probe := range plans {
		t.Run(probe.name, func(t *testing.T) {
			rows, err := s.db.Query("EXPLAIN QUERY PLAN "+probe.query, probe.args...)
			if err != nil {
				t.Fatalf("读取查询计划: %v", err)
			}
			defer rows.Close()
			used := false
			for rows.Next() {
				var id, parent, notUsed int
				var detail string
				if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
					t.Fatalf("扫描查询计划: %v", err)
				}
				used = used || strings.Contains(detail, probe.index)
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("遍历查询计划: %v", err)
			}
			if !used {
				t.Fatalf("SQLite 范围查询未选择 %s: query=%s", probe.index, probe.query)
			}
		})
	}
}
