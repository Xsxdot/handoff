package ledgernode

import (
	"encoding/json"
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
