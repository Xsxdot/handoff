package ledgernode

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestFinalMessageFromEventsUsesProtocolFields(t *testing.T) {
	events := []proto.Event{
		{Seq: 1, Type: proto.EventTypeProgress, Payload: json.RawMessage(`{"text":"working"}`)},
		{Seq: 2, Type: proto.EventTypeCompleted, Payload: json.RawMessage(`{"branch":"cards/B1-implement","commit":"abc","summary":"最终审阅报文"}`)},
	}
	message, err := finalMessageFromEvents(events)
	if err != nil || message != "最终审阅报文" {
		t.Fatalf("completed summary: %v %q", err, message)
	}

	events = []proto.Event{{Seq: 3, Type: proto.EventTypeTurnFailed,
		Payload: json.RawMessage(`{"fail_reason":"回合失败原文"}`)}}
	message, err = finalMessageFromEvents(events)
	if err != nil || message != "回合失败原文" {
		t.Fatalf("turn_failed reason: %v %q", err, message)
	}

	if _, err := finalMessageFromEvents([]proto.Event{{Type: proto.EventTypeCompleted,
		Payload: json.RawMessage(`{"branch":"x"}`)}}); err == nil {
		t.Fatal("缺最终文本应报错")
	}
}

func TestTaskBranchReadsLatestDispatchSnapshot(t *testing.T) {
	s, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.EnsureDefaultWorkflows(); err != nil {
		t.Fatal(err)
	}
	card, err := s.CreateCard(ledger.NewCard{Title: "task branch", Project: "p", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordDispatch(card.ID, ledger.DispatchSnapshot{Branch: "cards/old", Actor: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordDispatch(card.ID, ledger.DispatchSnapshot{Branch: "cards/latest", Actor: "test"}); err != nil {
		t.Fatal(err)
	}
	branch, err := taskBranch(s, card)
	if err != nil || branch != "cards/latest" {
		t.Fatalf("latest branch: %v %q", err, branch)
	}
}

// codex 收尾会在 completed 之后再补一条 turn_failed（app-server 断连），
// 那是传输层假警报。取报文必须认 completed——否则节点拿到的是断连文案，
// 裁决必然解析失败、每次审阅都白白转人工。2026-08-19 真机实测踩中。
func TestFinalMessagePrefersCompletedOverTrailingTurnFailed(t *testing.T) {
	events := []proto.Event{
		{Type: proto.EventTypeProgress, Payload: []byte(`{"text":"审阅中"}`)},
		{Type: proto.EventTypeCompleted, Payload: []byte(`{"summary":"审阅结论\n` + "```" + `handoff-verdict\n{\"verdict\":\"pass\"}\n` + "```" + `"}`)},
		{Type: proto.EventTypeTurnFailed, Payload: []byte(`{"fail_reason":"codex 连接断开: EOF"}`)},
	}
	message, err := finalMessageFromEvents(events)
	if err != nil {
		t.Fatalf("取报文: %v", err)
	}
	if !strings.Contains(message, "handoff-verdict") {
		t.Fatalf("应取 completed 的报文，实得: %q", message)
	}
}

// 真失败（没有 completed）仍要拿到失败原文，交上游转人工。
func TestFinalMessageFallsBackToFailure(t *testing.T) {
	events := []proto.Event{
		{Type: proto.EventTypeProgress, Payload: []byte(`{"text":"起手"}`)},
		{Type: proto.EventTypeTurnFailed, Payload: []byte(`{"fail_reason":"模型 400"}`)},
	}
	message, err := finalMessageFromEvents(events)
	if err != nil || message != "模型 400" {
		t.Fatalf("失败回退: %v %q", err, message)
	}
}

// 节点等的必须是「回合终态」而不是「首个可动作事件」：审阅同样要过权限
// 门、也可能发工单，醒在这些事件上就去取报文必然报「没有最终报文」。
func TestWaitForTurnEndSkipsNonTerminalEvents(t *testing.T) {
	seq := []proto.EventType{
		proto.EventTypePermissionRequest,
		proto.EventTypeQuestion,
		proto.EventTypePermissionRequest,
		proto.EventTypeCompleted,
	}
	calls := 0
	err := waitForTurnEnd(context.Background(), func(context.Context) (*proto.Event, error) {
		ev := &proto.Event{Type: seq[calls]}
		calls++
		return ev, nil
	})
	if err != nil {
		t.Fatalf("等终态: %v", err)
	}
	if calls != len(seq) {
		t.Fatalf("应一直等到 completed（%d 次），实际 %d 次", len(seq), calls)
	}
}

// turn_failed 也是回合终态：executor 还活着，但这一回合结束了，报文在
// fail_reason 里——不能继续等下去把节点挂死。
func TestWaitForTurnEndAcceptsTurnFailed(t *testing.T) {
	calls := 0
	err := waitForTurnEnd(context.Background(), func(context.Context) (*proto.Event, error) {
		calls++
		return &proto.Event{Type: proto.EventTypeTurnFailed}, nil
	})
	if err != nil || calls != 1 {
		t.Fatalf("turn_failed 应立即收口: err=%v calls=%d", err, calls)
	}
}
