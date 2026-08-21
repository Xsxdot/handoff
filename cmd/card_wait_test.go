package cmd

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func TestCardWaitSubtreeExitsWhenAllDone(t *testing.T) {
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
	waitOut, _, err := runLedgerCLI(t, dir, "card", "wait", root.ID, "--subtree", "--timeout", "15s")
	wg.Wait()
	if err != nil {
		t.Fatalf("wait 应正常退出: %v", err)
	}
	if !strings.Contains(waitOut, child.ID) || !strings.Contains(waitOut, root.ID) {
		t.Fatalf("事件缺失: %q", waitOut)
	}
}

// TestWaitRejectsCardFlag 执行域动词必须对 card 一无所知：--card 应是未知 flag。
// 这条是「分层」这个设计裁决的回归网——有人再把账本分支塞回 wait 就会红。
func TestWaitRejectsCardFlag(t *testing.T) {
	dir := t.TempDir()
	_, _, err := runLedgerCLI(t, dir, "wait", "--card", "B1")
	if err == nil {
		t.Fatalf("wait 不应再认识 --card")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("应报未知 flag，实际: %v", err)
	}
}
