// b439_s7_test.go —— B439 S7 账本侧派生读：无归属（target=”）在飞挂账计数与
// 非终态挂账键集合。终态 ” 挂账不计；非空 target 不计入无归属。
package ledger

import (
	"path/filepath"
	"testing"
)

func TestB439UnattributedInflightCount(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "u.db"))
	if err != nil {
		t.Fatalf("打开账本: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.PutWorkflow("bug", WorkflowDef{States: []string{StatusTodo, StatusDoing, StatusDone}}); err != nil {
		t.Fatalf("PutWorkflow: %v", err)
	}
	card, err := s.CreateCard(NewCard{Title: "无归属", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatalf("CreateCard: %v", err)
	}
	if err := s.LinkTask(card.ID, "", "T-inflight", "implement", "t"); err != nil {
		t.Fatalf("挂非终态 '' 账: %v", err)
	}
	if err := s.LinkTask(card.ID, "", "T-done", "implement", "t"); err != nil {
		t.Fatalf("挂终态 '' 账: %v", err)
	}
	if _, err := s.AppendMirroredEvent(card.ID, MirroredEvent{
		Target: "", Task: "T-done", SourceSeq: 1, Type: "archived", Payload: []byte(`{}`),
	}); err != nil {
		t.Fatalf("预置终态 '' 镜像: %v", err)
	}
	if err := s.LinkTask(card.ID, "linux-01", "T-named", "implement", "t"); err != nil {
		t.Fatalf("挂命名账: %v", err)
	}

	count, err := s.UnattributedInflightCount()
	if err != nil {
		t.Fatalf("UnattributedInflightCount: %v", err)
	}
	if count != 1 {
		t.Fatalf("无归属在飞挂账 = %d, want 1（终态 '' 与命名挂账不计）", count)
	}

	set, err := s.NonTerminalLinks()
	if err != nil {
		t.Fatalf("NonTerminalLinks: %v", err)
	}
	if !set["/T-inflight"] {
		t.Fatalf("NonTerminalLinks 应含非终态 '/T-inflight': %v", set)
	}
	if set["/T-done"] {
		t.Fatalf("终态 '' 不应在 NonTerminalLinks: %v", set)
	}
	if !set["linux-01/T-named"] {
		t.Fatalf("命名非终态挂账应在 NonTerminalLinks: %v", set)
	}
}
