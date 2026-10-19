// card_wait_b435_test.go —— B435 契约 ①：`card wait` 既有 stdout 行格式的逐字金样本。
//
// B435 把会话房间消息并入 card wait（寻址命中本卡 pull 席位），承诺「行格式逐字保持」：
// 建连快照行与事件行两种形状，房间消息命中行就是事件行（type=room_message、card_id=""）。
// 本测试在改造前就锁住现状，改造后不得改期望值。
package cmd

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func TestB435CardWaitLineShapes(t *testing.T) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	snap := cardSnapshotLine{
		Type: cardSnapshotType, CardID: "B1", Subtree: false, FromSeq: 41,
		Members: []string{"B1"}, Actionable: []cardSnapshotTicket{}, Needs: []cardSnapshotNeed{},
	}
	if err := enc.Encode(snap); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), `{"type":"card_snapshot","card_id":"B1","subtree":false,"from_seq":41,"members":["B1"],"actionable":[],"needs":[]}`+"\n"; got != want {
		t.Fatalf("card_snapshot 行漂移:\n got %s\nwant %s", got, want)
	}
	buf.Reset()
	roomHit := ledger.Event{
		Seq: 42, CardID: "", Type: ledger.EvRoomMessage, Actor: "user:sy",
		Payload:   json.RawMessage(`{"room":"session:3","kind":"user","body":"看一下"}`),
		CreatedAt: time.Unix(0, 0).UTC(),
	}
	if err := enc.Encode(roomHit); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), `{"seq":42,"card_id":"","type":"room_message","actor":"user:sy","payload":{"room":"session:3","kind":"user","body":"看一下"},"created_at":"1970-01-01T00:00:00Z"}`+"\n"; got != want {
		t.Fatalf("事件行（房间消息命中）漂移:\n got %s\nwant %s", got, want)
	}
	buf.Reset()
	mirrored := ledger.Event{
		Seq: 43, CardID: "B1", Type: ledger.EvTaskMirrored, Actor: "agentd",
		Payload:      json.RawMessage(`{"task_type":"question","payload":{}}`),
		SourceTarget: "linux-01", SourceTask: "t-1", SourceSeq: 7, CreatedAt: time.Unix(0, 0).UTC(),
	}
	if err := enc.Encode(mirrored); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), `{"seq":43,"card_id":"B1","type":"task_mirrored","actor":"agentd","payload":{"task_type":"question","payload":{}},"source_target":"linux-01","source_task":"t-1","source_seq":7,"created_at":"1970-01-01T00:00:00Z"}`+"\n"; got != want {
		t.Fatalf("事件行（卡事件）漂移:\n got %s\nwant %s", got, want)
	}
}
