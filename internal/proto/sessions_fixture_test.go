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
	"reflect"
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

func TestSessionTimelineKindVocabulary(t *testing.T) {
	// timeline kind 词表冻结（B358 补签轮）：逐值断言字面量，防手抖改串；
	// 消费方（sessionTimeline）的 switch 必须与这里逐值对齐，未知 ledger
	// 事件类型不得被默认分支收进 timeline。
	want := map[string]string{
		SessionEventCardJoined:  "card_joined",
		SessionEventCardLeft:    "card_left",
		SessionEventSeatBound:   "seat_bound",
		SessionEventSeatRebound: "seat_rebound",
		SessionEventNeedsHuman:  "needs_human",
		SessionEventCardClosed:  "card_closed",
		SessionEventArchived:    "archived",
		SessionEventCreated:     "created",
	}
	for constant, literal := range want {
		if constant != literal {
			t.Fatalf("timeline kind 常量 %q 与冻结字面量 %q 不一致", constant, literal)
		}
	}
}

// TestSessionWakeFixture 锁会话订阅通道唤醒载荷的 wire 形状（契约 §3.8/条 43）：
// 顶层键集恰 {session,hit,referenced,unread}；referenced 为指针 + omitempty——
// 无引用锚时省键（缺键 ≠ 零值，序列化边界纪律）；hit/referenced 键集恰
// {seq,room,actor,body}。R1 通道（S3）stdout 形状以本测试为 Go 侧金样本；
// TS 孪生金样本归 S6（契约 §8.9）。改形状先回 contract 节点。
func TestSessionWakeFixture(t *testing.T) {
	hit := SessionCite{Seq: 42, Room: "session:1", Actor: "cli:opencode#ab12", Body: "@B1 这个方案跑不通"}
	wake := SessionWake{
		Session:    "session:1",
		Hit:        hit,
		Referenced: &SessionCite{Seq: 41, Room: "session:1", Actor: "user:sy", Body: "商定的是走分支 B"},
		Unread:     3,
	}
	raw, err := json.Marshal(wake)
	if err != nil {
		t.Fatalf("编码 SessionWake: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("解 JSON: %v", err)
	}
	for _, key := range []string{"session", "hit", "referenced", "unread"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("顶层缺键 %s: %s", key, raw)
		}
	}
	if len(fields) != 4 {
		t.Fatalf("顶层键集漂移: %s", raw)
	}
	var hitFields map[string]json.RawMessage
	if err := json.Unmarshal(fields["hit"], &hitFields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"seq", "room", "actor", "body"} {
		if _, ok := hitFields[key]; !ok {
			t.Fatalf("hit 缺键 %s: %s", key, fields["hit"])
		}
	}
	if len(hitFields) != 4 {
		t.Fatalf("hit 键集漂移: %s", fields["hit"])
	}
	// referenced omitempty：nil 时省键。
	rawNoRef, err := json.Marshal(SessionWake{Session: "session:1", Hit: hit, Unread: 1})
	if err != nil {
		t.Fatal(err)
	}
	var noRefFields map[string]json.RawMessage
	if err := json.Unmarshal(rawNoRef, &noRefFields); err != nil {
		t.Fatal(err)
	}
	if _, ok := noRefFields["referenced"]; ok {
		t.Fatalf("无引用锚时 referenced 必须省键: %s", rawNoRef)
	}
	// 往返恒等（roundtrip 属性：一条属性顶一族手写用例）。
	var back SessionWake
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(wake, back) {
		t.Fatalf("roundtrip 漂移: want=%+v got=%+v", wake, back)
	}
}
