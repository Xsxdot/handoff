// B358 会话（群）域 wire 形状的 Go 侧金样本。锁两类编码：
//   - RoomMessage 的 B358 新键 reply_to（omitempty：零值不出键、非零在线）；
//   - 会话 DTO 的关键键集（Session/SessionSummary/SessionMember/SessionDetail）。
//
// TS 孪生金样本（web/src/api/rooms.ts + testdata/RoomsFixture.json）由实现节点
// 随控制台内容面补齐（本卡其属越过空壳的可观测行为），届时两侧逐键一致。
// 改形状先回 contract 节点。
package proto

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRoomMessageReplyToGolden(t *testing.T) {
	// 零值：reply_to 必须不出键（既有最小金样本不受扰）。
	minimal, err := json.Marshal(RoomMessage{Room: "session:1", Kind: RoomMsgUser, Body: "无回复"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(minimal), "reply_to") {
		t.Fatalf("reply_to 零值必须省略: %s", minimal)
	}
	// 非零：reply_to 以数字键在线。
	replied, err := json.Marshal(RoomMessage{Room: "session:1", Kind: RoomMsgUser, Body: "回复", ReplyTo: 42})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(replied, &got); err != nil {
		t.Fatal(err)
	}
	if got["reply_to"] != float64(42) {
		t.Fatalf("reply_to 应编码为 42: %s", replied)
	}
	// 往返一致。
	var back RoomMessage
	if err := json.Unmarshal(replied, &back); err != nil {
		t.Fatal(err)
	}
	if back.ReplyTo != 42 || back.Body != "回复" {
		t.Fatalf("reply_to 往返不一致: %+v", back)
	}
}

func TestSessionSummaryGoldenProjection(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	summary := SessionSummary{
		ID: "session:7", Kind: SessionKind, Title: "架构物理化", Owner: "user:sy",
		Unread: 2, NeedsHuman: true, LastActivity: now,
		Members: []SessionMember{
			{Identity: "user:sy", Kind: SessionMemberHuman, Status: SessionMemberLastActive},
			{Kind: SessionMemberSeat, CardID: "B1", CardTitle: "竖切卡", Status: SessionMemberWorking},
		},
		Cards: []SessionCard{{CardID: "B1", Title: "竖切卡", Status: "进行中", Seat: "cli:opencode#s1"}},
	}
	raw, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "kind", "title", "owner", "unread", "needs_human", "last_activity", "members", "cards"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("SessionSummary 缺键 %q: %s", key, raw)
		}
	}
	if got["kind"] != SessionKind {
		t.Fatalf("SessionSummary.kind 应为 %q: %s", SessionKind, raw)
	}
	members, ok := got["members"].([]any)
	if !ok || len(members) != 2 {
		t.Fatalf("members 应两条: %s", raw)
	}
	seat := members[1].(map[string]any)
	if seat["kind"] != SessionMemberSeat || seat["card_id"] != "B1" {
		t.Fatalf("席位成员投影错误: %s", raw)
	}
}

func TestSessionDetailGoldenProjection(t *testing.T) {
	detail := SessionDetail{
		Summary: SessionSummary{ID: "session:7", Kind: SessionKind, Title: "会话"},
		Nodes:   []SessionNode{{CardID: "B1", Node: "implement", State: "running"}},
		Timeline: []SessionTimelineEvent{
			{Seq: 1, Kind: SessionEventCreated, Detail: ""},
			{Seq: 2, Kind: SessionEventCardJoined, CardID: "B1"},
		},
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["summary"]; !ok {
		t.Fatalf("SessionDetail 缺 summary: %s", raw)
	}
	nodes, ok := got["nodes"].([]any)
	if !ok || len(nodes) != 1 {
		t.Fatalf("SessionDetail.nodes 应一条: %s", raw)
	}
	timeline, ok := got["timeline"].([]any)
	if !ok || len(timeline) != 2 {
		t.Fatalf("SessionDetail.timeline 应两条: %s", raw)
	}
}

func TestSessionMemberStatusVocabulary(t *testing.T) {
	// 状态词表冻结：不得报告「在线」这类无法证实的取值。
	for _, status := range []string{SessionMemberWorking, SessionMemberListening, SessionMemberLastActive, SessionMemberEmpty} {
		if status == "online" || status == "在线" {
			t.Fatalf("成员状态不得包含在线: %q", status)
		}
	}
}
