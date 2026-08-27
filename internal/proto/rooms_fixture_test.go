// 房间 wire 形状的金样本冻结（B156.2 契约 §6）。本文件钉住三类编码：
// escalation 全字段、user 最小字段（omitempty 生效）、inbox 三 origin。
// Go 与 web/src/api/rooms.test.ts 的 TS 孪生金样本逐键一致；改形状先回
// contract 节点。事件类型字面量与 ledger.EvRoomMessage 的等式在
// internal/collab/service_test.go 钉住（测试文件不计图边，可同时看见两侧）。
package proto

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRoomMessageGoldenEscalation(t *testing.T) {
	msg := RoomMessage{
		Room:       "B156",
		Kind:       RoomMsgEscalation,
		Body:       "一句话：验收判据不成立，挂 B156",
		Refs:       []string{"docs/superpowers/specs/b156.2-contract.md", "timeline#note-3"},
		Mentions:   []string{"user:sy"},
		DecisionID: 7,
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"room", "kind", "body", "refs", "mentions", "decision_id"}
	if len(got) != len(wantKeys) {
		t.Fatalf("escalation 金样本键集漂移: got %v", got)
	}
	for _, k := range wantKeys {
		if _, ok := got[k]; !ok {
			t.Fatalf("缺键 %q: %s", k, raw)
		}
	}
	if _, ok := got["by_system"]; ok {
		t.Fatalf("by_system 零值必须省略: %s", raw)
	}
	if got["decision_id"].(float64) != 7 {
		t.Fatalf("decision_id 编码错误: %s", raw)
	}
}

func TestRoomMessageGoldenUserMinimal(t *testing.T) {
	msg := RoomMessage{Room: "project:handoff", Kind: RoomMsgUser, Body: "先停一下"}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"refs", "mentions", "decision_id", "by_system"} {
		if strings.Contains(string(raw), `"`) && strings.Contains(string(raw), banned) {
			t.Fatalf("可选键 %q 不得出现在最小金样本里: %s", banned, raw)
		}
	}
	var back RoomMessage
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Room != msg.Room || back.Kind != msg.Kind || back.Body != msg.Body {
		t.Fatalf("往返解码不一致: %+v", back)
	}
}

func TestInboxItemGoldenThreeOrigins(t *testing.T) {
	items := []InboxItem{
		{Origin: InboxOriginDecision, Title: "推翻级简报：契约语义冲突", CardID: "B156",
			RefID: "7", Payload: json.RawMessage(`{"id":7,"status":"open"}`)},
		{Origin: InboxOriginTicket, Title: "权限工单待答复", RefID: "tkt-42"},
		{Origin: InboxOriginMention, Title: "@你：改动影响 B145", RefID: "918"},
	}
	seen := map[string]bool{}
	for _, item := range items {
		raw, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]any{}
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if !seen[item.Origin] {
			seen[item.Origin] = true
		} else {
			t.Fatalf("origin 重复: %s", raw)
		}
		if got["origin"] != item.Origin || got["ref_id"] != item.RefID {
			t.Fatalf("origin/ref_id 编码漂移: %s", raw)
		}
		switch item.Origin {
		case InboxOriginDecision:
			if _, ok := got["card_id"]; !ok {
				t.Fatalf("decision 条目应带 card_id: %s", raw)
			}
			if _, ok := got["payload"]; !ok {
				t.Fatalf("decision 条目应带 payload: %s", raw)
			}
		default:
			if _, ok := got["card_id"]; ok {
				t.Fatalf("空 card_id 必须省略: %s", raw)
			}
		}
	}
	if len(seen) != 3 {
		t.Fatalf("三源词表漂移: %v", seen)
	}
}

func TestRoomSummaryGoldenProjection(t *testing.T) {
	card := RoomSummary{
		ID: "B1", Kind: "card", Title: "卡会话", Live: true,
		ReadOnly: false, LastActivity: time.Unix(0, 0).UTC(), Unread: 0,
		Attach: &RoomAttach{Target: "devbox", TaskID: "T1", WorkDir: "/w/B1", Command: "handoff attach T1"},
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["unread"] != float64(0) {
		t.Fatalf("unread 0 必须在线: %s", raw)
	}
	attach, ok := got["attach"].(map[string]any)
	if !ok {
		t.Fatalf("attach 应为对象: %s", raw)
	}
	for key, want := range map[string]string{
		"target": "devbox", "task_id": "T1", "work_dir": "/w/B1", "command": "handoff attach T1",
	} {
		if attach[key] != want {
			t.Fatalf("attach.%s 编码错误: got %v want %q", key, attach[key], want)
		}
	}

	globalRaw, err := json.Marshal(RoomSummary{ID: "global", Kind: "global", Title: "全员", Unread: 0})
	if err != nil {
		t.Fatal(err)
	}
	var global map[string]any
	if err := json.Unmarshal(globalRaw, &global); err != nil {
		t.Fatal(err)
	}
	if _, ok := global["attach"]; ok {
		t.Fatalf("无 attach 的 global 不得出 attach 键: %s", globalRaw)
	}
}
