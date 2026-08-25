package cmd

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func TestCardWaitSubtreeExitsWhenAllDone(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "根卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatalf("建根卡: %v", err)
	}
	var root struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &root); err != nil {
		t.Fatalf("解根卡: %v", err)
	}
	out, _, err = runLedgerCLI(t, dir, "card", "split", root.ID, "子卡")
	if err != nil {
		t.Fatalf("拆子卡: %v", err)
	}
	var child struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &child); err != nil {
		t.Fatalf("解子卡: %v", err)
	}

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
			if err := st.MoveCard(id, "进行中", "", "test"); err != nil {
				t.Error(err)
			}
			if err := st.MoveCard(id, "待审阅", "", "test"); err != nil {
				t.Error(err)
			}
			if err := st.MoveCard(id, "已完成", "", "test"); err != nil {
				t.Error(err)
			}
		}
	}()
	waitOut, _, waitErr := runLedgerCLI(t, dir, "card", "wait", root.ID, "--subtree", "--timeout", "15s")
	wg.Wait()
	if waitErr != nil {
		t.Fatalf("wait 应正常退出: %v", waitErr)
	}

	lines := cardWaitJSONLines(t, waitOut)
	if len(lines) < 3 {
		t.Fatalf("快照后至少应有事件行: %q", waitOut)
	}
	wantIDs := []string{root.ID, child.ID}
	sort.Strings(wantIDs)
	for i, id := range wantIDs {
		snapshot := readCardWaitSnapshot(t, lines[i])
		if snapshot.CardID != id {
			t.Fatalf("第 %d 条快照 card_id=%q, want %q", i, snapshot.CardID, id)
		}
		if snapshot.Status != "待办" {
			t.Fatalf("第 %d 条快照 status=%q, want 待办", i, snapshot.Status)
		}
		if snapshot.NeedsHuman || snapshot.NeedsReason != "" {
			t.Fatalf("无 needs 的快照=%+v, want false/空串", snapshot)
		}
	}
	for i, line := range lines[2:] {
		if _, ok := line["seq"]; !ok {
			t.Fatalf("快照之后第 %d 条不是事件行（缺 seq）: %v", i, line)
		}
		var typ string
		if err := json.Unmarshal(line["type"], &typ); err != nil {
			t.Fatalf("事件 type 解码: %v", err)
		}
		if typ == "card_snapshot" {
			t.Fatalf("事件段出现第二次快照: %v", line)
		}
	}
	if !strings.Contains(waitOut, child.ID) || !strings.Contains(waitOut, root.ID) {
		t.Fatalf("事件缺失: %q", waitOut)
	}
}

func cardWaitJSONLines(t *testing.T, out string) []map[string]json.RawMessage {
	t.Helper()
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return nil
	}
	rows := strings.Split(trimmed, "\n")
	lines := make([]map[string]json.RawMessage, 0, len(rows))
	for i, row := range rows {
		if strings.TrimSpace(row) == "" {
			t.Fatalf("第 %d 行为空，stdout 必须一行一个 JSON", i)
		}
		var line map[string]json.RawMessage
		if err := json.Unmarshal([]byte(row), &line); err != nil {
			t.Fatalf("第 %d 行不是合法 JSON: %v；原文=%q", i, err, row)
		}
		lines = append(lines, line)
	}
	return lines
}

type cardWaitSnapshot struct {
	Type        string
	CardID      string
	Status      string
	NeedsHuman  bool
	NeedsReason string
}

func readCardWaitSnapshot(t *testing.T, line map[string]json.RawMessage) cardWaitSnapshot {
	t.Helper()
	for _, key := range []string{"type", "card_id", "status", "needs_human", "needs_reason"} {
		if _, ok := line[key]; !ok {
			t.Fatalf("快照缺字段 %q: %v", key, line)
		}
	}
	var got cardWaitSnapshot
	if err := json.Unmarshal(line["type"], &got.Type); err != nil {
		t.Fatalf("快照 type 解码: %v", err)
	}
	if err := json.Unmarshal(line["card_id"], &got.CardID); err != nil {
		t.Fatalf("快照 card_id 解码: %v", err)
	}
	if err := json.Unmarshal(line["status"], &got.Status); err != nil {
		t.Fatalf("快照 status 解码: %v", err)
	}
	if err := json.Unmarshal(line["needs_human"], &got.NeedsHuman); err != nil {
		t.Fatalf("快照 needs_human 解码: %v", err)
	}
	if err := json.Unmarshal(line["needs_reason"], &got.NeedsReason); err != nil {
		t.Fatalf("快照 needs_reason 解码: %v", err)
	}
	if got.Type != "card_snapshot" {
		t.Fatalf("快照 type=%q, want card_snapshot", got.Type)
	}
	return got
}

func TestCardWaitEmitsSnapshotBeforeTimeout(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "瞬时失败卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatalf("解卡: %v", err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "needs", card.ID, "派发瞬时失败"); err != nil {
		t.Fatalf("打 needs: %v", err)
	}

	waitOut, _, waitErr := runLedgerCLI(t, dir, "card", "wait", card.ID, "--timeout", "20ms")
	if waitErr == nil || !strings.Contains(waitErr.Error(), "wait --card 超时") {
		t.Fatalf("wait 应按既有超时语义退出，err=%v", waitErr)
	}
	lines := cardWaitJSONLines(t, waitOut)
	if len(lines) != 1 {
		t.Fatalf("无实时事件时应只输出一条建连快照，实得 %d 行: %q", len(lines), waitOut)
	}
	snapshot := readCardWaitSnapshot(t, lines[0])
	if snapshot.CardID != card.ID || snapshot.Status != "待办" {
		t.Fatalf("卡快照身份/状态=%+v, want card=%s status=待办", snapshot, card.ID)
	}
	if !snapshot.NeedsHuman || snapshot.NeedsReason != "派发瞬时失败" {
		t.Fatalf("卡快照 needs=%+v, want true/派发瞬时失败", snapshot)
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
