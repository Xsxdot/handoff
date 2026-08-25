// Package api 的缝级测试（spec 测试接缝清单 #2：client.LedgerClient 出站
// 接口缝的实现侧）。RecordMessageConsumed 的账本行为断言从 client.LedgerClient
// 接口进入——api.go 的 var _ client.LedgerClient = (*Facade)(nil) 是编译期
// 背书，走接口验的才是契约 §3.4 冻结的那条缝；直查库的计数断言（markersOf）
// 另持组装点同形注入的 *ledger.Store，两者不冲突（跨卡审计裁决二）。
//
// 文件名 api_rooms_test.go：本卡四支全是房间消息消费语义；api_test.go 归
// C1 的门面缝通用断言（跨卡审计裁决一）。
package api

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/collab/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// newFixture 起真 SQLite 账本并按组装点同形构造门面（service_test.go 夹具
// 同形）。工作流种子只服务建卡；群级路径不需要任何卡。返回接口形态：
// 被测调用一律经 client.LedgerClient，*ledger.Store 只供直查库断言。
func newFixture(t *testing.T) (client.LedgerClient, *ledger.Store) {
	t.Helper()
	st, err := ledger.Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.PutWorkflow("bug", ledger.WorkflowDef{Nodes: []ledger.NodeDef{
		{Name: ledger.StatusTodo, Next: ledger.StatusDoing},
		{Name: ledger.StatusDoing, Next: ledger.StatusDone},
		{Name: ledger.StatusDone},
	}}); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	return New(st), st
}

// mustCardFor 建一张卡并返回。
func mustCardFor(t *testing.T, st *ledger.Store, title string) ledger.Card {
	t.Helper()
	card, err := st.CreateCard(ledger.NewCard{Title: title, Project: "handoff", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	return card
}

// markersOf 数全流上的 message_consumed 事件并解出载荷键集。直查库：
// 经 *ledger.Store.EventsFromAsc 读原始账本事件，不经 wire 投影。
func markersOf(t *testing.T, st *ledger.Store) map[int64]struct {
	CardID  string
	Keys    map[string]json.RawMessage
	Payload map[string]any
} {
	t.Helper()
	events, err := st.EventsFromAsc([]string{}, 0, 1000)
	if err != nil {
		t.Fatalf("读事件流: %v", err)
	}
	out := map[int64]struct {
		CardID  string
		Keys    map[string]json.RawMessage
		Payload map[string]any
	}{}
	for _, ev := range events {
		if ev.Type != "message_consumed" {
			continue
		}
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(ev.Payload, &keys); err != nil {
			t.Fatalf("解消费标记载荷键集: %v", err)
		}
		var p map[string]any
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatalf("解消费标记载荷: %v", err)
		}
		out[ev.Seq] = struct {
			CardID  string
			Keys    map[string]json.RawMessage
			Payload map[string]any
		}{ev.CardID, keys, p}
	}
	return out
}

// TestRecordMessageConsumedExactlyOnceThroughSeam 恰好一次的三条账本面：
// 首次消费落恰一条标记（载荷金样键集=message_seq+consumer 两键、不多不少）、
// 同参重试 no-op、他人消费同一条各落一条互不顶替。
func TestRecordMessageConsumedExactlyOnceThroughSeam(t *testing.T) {
	f, st := newFixture(t)
	card := mustCardFor(t, st, "消费标记卡")
	seq, err := f.RecordRoomMessage(card.ID, proto.RoomMessage{Room: card.ID,
		Kind: proto.RoomMsgUser, Body: "留言"}, "user:sy")
	if err != nil {
		t.Fatalf("发消息: %v", err)
	}

	if err := f.RecordMessageConsumed(card.ID, seq, "user:sy"); err != nil {
		t.Fatalf("首次消费: %v", err)
	}
	got := markersOf(t, st)
	if len(got) != 1 {
		t.Fatalf("首次消费后应恰 1 条标记，实得 %d", len(got))
	}
	for _, m := range got {
		if m.CardID != card.ID {
			t.Fatalf("卡房间消费标记应挂卡流: card=%q", m.CardID)
		}
		if len(m.Keys) != 2 || m.Keys["message_seq"] == nil || m.Keys["consumer"] == nil {
			t.Fatalf("载荷键集漂移（冻结=message_seq+consumer 恰两键）: %v", m.Keys)
		}
		if ms, _ := m.Payload["message_seq"].(float64); int64(ms) != seq {
			t.Fatalf("message_seq 应为 %d，实得 %v", seq, m.Payload["message_seq"])
		}
		if c, _ := m.Payload["consumer"].(string); c != "user:sy" {
			t.Fatalf("consumer 应为 user:sy，实得 %v", m.Payload["consumer"])
		}
	}

	if err := f.RecordMessageConsumed(card.ID, seq, "user:sy"); err != nil {
		t.Fatalf("同参重试应幂等 nil: %v", err)
	}
	if n := len(markersOf(t, st)); n != 1 {
		t.Fatalf("同参重试后仍应恰 1 条标记，实得 %d", n)
	}

	if err := f.RecordMessageConsumed(card.ID, seq, "cli:b@h"); err != nil {
		t.Fatalf("他人消费同一消息应各落一条: %v", err)
	}
	if n := len(markersOf(t, st)); n != 2 {
		t.Fatalf("两个消费者共应 2 条标记，实得 %d", n)
	}
}

// TestRecordMessageConsumedGroupMarkerIsCardless 群级消息的消费标记同为
// 无卡事件（项目级），且同样恰好一次。
func TestRecordMessageConsumedGroupMarkerIsCardless(t *testing.T) {
	f, st := newFixture(t)
	seq, err := f.RecordRoomMessage("", proto.RoomMessage{Room: "global",
		Kind: proto.RoomMsgUser, Body: "群消息"}, "user:sy")
	if err != nil {
		t.Fatalf("群级发送: %v", err)
	}
	if err := f.RecordMessageConsumed("", seq, "user:sy"); err != nil {
		t.Fatalf("群级消费: %v", err)
	}
	got := markersOf(t, st)
	if len(got) != 1 {
		t.Fatalf("群级消费标记应恰一条，实得 %d", len(got))
	}
	for _, m := range got {
		if m.CardID != "" {
			t.Fatalf("群级消费标记应为无卡事件: card=%q", m.CardID)
		}
	}
	if err := f.RecordMessageConsumed("", seq, "user:sy"); err != nil {
		t.Fatalf("群级重试: %v", err)
	}
	if n := len(markersOf(t, st)); n != 1 {
		t.Fatalf("群级重试后仍应恰一条，实得 %d", n)
	}
}

// TestRecordMessageConsumedUnknownSeqIsIdempotentNil 锁死岔口六方案甲的
// 选择：对不存在 seq 的消费是幂等 nil 而非报错。若未来契约加 ErrNoMessage，
// 本测试随该契约修订一起改。
func TestRecordMessageConsumedUnknownSeqIsIdempotentNil(t *testing.T) {
	f, st := newFixture(t)
	if err := f.RecordMessageConsumed("", 987654321, "user:x"); err != nil {
		t.Fatalf("对不存在 seq 消费应幂等 nil（岔口六方案甲），got %v", err)
	}
	// 正断言（跨卡审计裁决六）：不校验 msgSeq 存在性 ⇒ 未知 seq 也真落一条
	// 标记——「谁消费了哪个 seq」是事实记录，存在性校验是调用方的事。对着
	// return nil 空壳只断言「没报错」时，「没实现」和「实现了」不可区分。
	if n := len(markersOf(t, st)); n != 1 {
		t.Fatalf("未知 seq 的消费也应真落恰好一条标记，实得 %d", n)
	}
}

// TestRecordMessageConsumedRejectsMissingCardAndEmptyConsumer 反面：卡房间
// 的标记要求卡存在（ErrNotFound）；空 consumer 是调用方 bug，直接报错。
func TestRecordMessageConsumedRejectsMissingCardAndEmptyConsumer(t *testing.T) {
	f, _ := newFixture(t)
	if err := f.RecordMessageConsumed("B99999", 1, "user:x"); !errors.Is(err, ledger.ErrNotFound) {
		t.Fatalf("不存在的卡必须报 ErrNotFound，got %v", err)
	}
	if err := f.RecordMessageConsumed("", 1, ""); err == nil {
		t.Fatal("空 consumer 必须报错：没有「谁」的消费标记无意义")
	}
}
