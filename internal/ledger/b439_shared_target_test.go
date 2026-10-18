// B439 F3：共享账本拒收空 target（事务零残留）；本机/单机账本仍接受 ”（S5）。
package ledger

import (
	"path/filepath"
	"testing"
)

func seedB439Card(t *testing.T, s *Store) Card {
	t.Helper()
	if _, err := s.PutWorkflow("bug", WorkflowDef{States: []string{StatusTodo, StatusDoing, StatusDone}}); err != nil {
		t.Fatalf("PutWorkflow: %v", err)
	}
	c, err := s.CreateCard(NewCard{Title: "身份", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatalf("CreateCard: %v", err)
	}
	return c
}

// TestB439SharedLedgerRejectsEmptyTarget 共享账本上新派发 target=” 必须被拒，
// 且不留下任何快照/挂账/留痕（事务零残留）。
func TestB439SharedLedgerRejectsEmptyTarget(t *testing.T) {
	s, err := OpenShared(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("OpenShared: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	card := seedB439Card(t, s)

	before, err := s.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.RecordDispatchAndLinkTask(card.ID, DispatchSnapshot{
		Target: "", TaskID: "T-empty", Branch: "cards/x", Purpose: "implement", Actor: "t",
	})
	if err == nil {
		t.Fatal("共享账本 target='' 必须被拒")
	}
	links, err := s.TasksOf(card.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 0 {
		t.Fatalf("被拒后不得留下挂账: %+v", links)
	}
	after, err := s.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("被拒后不得留下事件：before=%d after=%d", len(before), len(after))
	}
}

// TestB439LocalLedgerAcceptsEmptyTarget 本机/单机账本（未声明共享）照旧接受
// ”：单机语义下它无歧义，S5 逐点不变。
func TestB439LocalLedgerAcceptsEmptyTarget(t *testing.T) {
	s := newTestStore(t)
	card := seedB439Card(t, s)

	if _, _, err := s.RecordDispatchAndLinkTask(card.ID, DispatchSnapshot{
		Target: "", TaskID: "T-local", Branch: "cards/x", Purpose: "implement", Actor: "t",
	}); err != nil {
		t.Fatalf("本机账本接受 '': %v", err)
	}
	links, err := s.TasksOf(card.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].Target != "" {
		t.Fatalf("本机账本挂账 target 应为空串: %+v", links)
	}
}

// TestB439SharedLedgerAcceptsTeamName 共享账本接受全队名（新写入恒非空）。
func TestB439SharedLedgerAcceptsTeamName(t *testing.T) {
	s, err := OpenShared(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("OpenShared: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	card := seedB439Card(t, s)

	if _, _, err := s.RecordDispatchAndLinkTask(card.ID, DispatchSnapshot{
		Target: "linux-01", TaskID: "T-team", Branch: "cards/x", Purpose: "implement", Actor: "t",
	}); err != nil {
		t.Fatalf("共享账本接受全队名: %v", err)
	}
	links, err := s.TasksOf(card.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].Target != "linux-01" {
		t.Fatalf("共享账本挂账 target 应为全队名: %+v", links)
	}
}
