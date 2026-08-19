package cmd

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func TestWaitCardSubtreeExitsWhenAllDone(t *testing.T) {
	dir := t.TempDir()
	out, _, _ := runLedgerCLI(t, dir, "card", "add", "根卡", "--project", "demo", "--workflow", "bug")
	var root struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &root)
	out, _, _ = runLedgerCLI(t, dir, "card", "split", root.ID, "子卡")
	var child struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &child)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(500 * time.Millisecond)
		st, err := ledger.Open(dir + "/ledger.db")
		if err != nil {
			t.Error(err)
			return
		}
		defer st.Close()
		for _, id := range []string{child.ID, root.ID} {
			_ = st.MoveCard(id, "进行中", "", "test")
			_ = st.MoveCard(id, "待审阅", "", "test")
			_ = st.MoveCard(id, "已完成", "", "test")
		}
	}()
	waitOut, _, err := runLedgerCLI(t, dir, "wait", "--card", root.ID, "--subtree", "--timeout", "15s")
	wg.Wait()
	if err != nil {
		t.Fatalf("wait 应正常退出: %v", err)
	}
	if !strings.Contains(waitOut, child.ID) || !strings.Contains(waitOut, root.ID) {
		t.Fatalf("事件缺失: %q", waitOut)
	}
}

func TestWaitCardConflictsWithTaskArg(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runLedgerCLI(t, dir, "wait", "T123", "--card", "B1"); err == nil {
		t.Fatal("task 参数与 --card 互斥应报错")
	}
}
