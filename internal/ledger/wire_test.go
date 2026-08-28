// 线格式契约：卡/事件/裁决序列化出去的字段名是 CLI stdout 与 Web API
// 共用的词汇表，改名即破坏机器消费方。这里把它钉死——PascalCase 泄漏
// （Go 结构体默认行为）曾在 card note / decision 的输出上真实发生过。
package ledger

import (
	"encoding/json"
	"testing"
	"time"
)

func keysOf(t *testing.T, v any) map[string]bool {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

func TestWireFieldNames(t *testing.T) {
	cardKeys := keysOf(t, Card{ID: "B1", BaseBranch: "x", DriverSession: "s"})
	for _, want := range []string{"id", "title", "status", "project", "parent",
		"workflow", "workflow_version", "base_branch", "driver_session", "created_at"} {
		if !cardKeys[want] {
			t.Errorf("Card 缺字段 %q（实际 %v）", want, cardKeys)
		}
	}
	viewKeys := keysOf(t, CardView{Card: Card{ID: "B1"}, Blocked: true,
		BlockedBy: []string{"B2"}, Following: "B3", MergedCount: 2, NeedsReason: "等人", OpenDecisions: 1})
	for _, want := range []string{"blocked", "blocked_by", "following", "merged_count", "needs", "open_decisions"} {
		if !viewKeys[want] {
			t.Errorf("CardView 缺字段 %q（实际 %v）", want, viewKeys)
		}
	}
	evKeys := keysOf(t, Event{Seq: 1, CardID: "B1", Type: EvComment,
		Payload: json.RawMessage(`{}`), CreatedAt: time.Now()})
	for _, want := range []string{"seq", "card_id", "type", "actor", "payload", "created_at"} {
		if !evKeys[want] {
			t.Errorf("Event 缺字段 %q（实际 %v）", want, evKeys)
		}
	}
	decKeys := keysOf(t, Decision{ID: 1, Body: "b", Status: "open"})
	for _, want := range []string{"id", "body", "status", "created_by", "created_at"} {
		if !decKeys[want] {
			t.Errorf("Decision 缺字段 %q（实际 %v）", want, decKeys)
		}
	}
}

func TestLedgerLinkAndRelationZeroValuesKeepSnakeKeys(t *testing.T) {
	cases := []struct {
		name  string
		value any
		keys  []string
	}{
		{name: "task link", value: TaskLink{}, keys: []string{"card_id", "target", "task_id", "purpose", "created_at"}},
		{name: "relation", value: Relation{}, keys: []string{"from", "to", "type", "created_at"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			for _, key := range tc.keys {
				value, ok := fields[key]
				if !ok {
					t.Fatalf("零值线格式缺键 %q: %s", key, raw)
				}
				if key == "created_at" {
					var timestamp time.Time
					if err := json.Unmarshal(value, &timestamp); err != nil {
						t.Fatalf("%s 不是时间值: %v", key, err)
					}
					if !timestamp.IsZero() {
						t.Fatalf("零值 %s 应保持零时间: %v", key, timestamp)
					}
					continue
				}
				var text string
				if err := json.Unmarshal(value, &text); err != nil {
					t.Fatalf("%s 不是字符串值: %v", key, err)
				}
				if text != "" {
					t.Fatalf("零值 %s 应为空字符串: %q", key, text)
				}
			}
		})
	}
}

// 评论事件的返回值必须带真实时间戳——零值会让机器消费方读到 0001-01-01。
func TestCommentEventCarriesTimestamp(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "带时间戳")
	ev, err := s.AddComment(c.ID, "hi", "普通", "test")
	if err != nil {
		t.Fatalf("comment: %v", err)
	}
	if ev.CreatedAt.IsZero() {
		t.Fatal("评论事件返回值的 CreatedAt 是零值")
	}
	if d := time.Since(ev.CreatedAt); d < 0 || d > time.Minute {
		t.Fatalf("时间戳不合理: %v", ev.CreatedAt)
	}
}
