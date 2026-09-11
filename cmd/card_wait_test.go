package cmd

import (
	"encoding/json"
	"path/filepath"
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

func firstJSONLine(t *testing.T, out string) map[string]any {
	t.Helper()
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	if line == "" {
		t.Fatalf("stdout 空: %q", out)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(line), &obj); err != nil {
		t.Fatalf("第一行不是 JSON: %q err=%v", line, err)
	}
	return obj
}

func TestCardWaitSubtreeSnapshotIncludesChildTicket(t *testing.T) {
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

	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	if _, err := st.AppendMirroredEvent(child.ID, ledger.MirroredEvent{
		Target: "linux-01", Task: "task-child", SourceSeq: 1, Type: "permission_request",
		Payload: []byte(`{"ticket_id":"tk-child","permission":"git push"}`), CreatedAt: time.Now(),
	}); err != nil {
		st.Close()
		t.Fatalf("镜像子卡工单: %v", err)
	}
	if _, err := st.AppendMirroredEvent(child.ID, ledger.MirroredEvent{
		Target: "linux-01", Task: "task-child", SourceSeq: 2, Type: "permission_request",
		Payload: []byte(`{"ticket_id":"tk-done"}`), CreatedAt: time.Now(),
	}); err != nil {
		st.Close()
		t.Fatalf("镜像已答工单: %v", err)
	}
	if _, err := st.AppendMirroredEvent(child.ID, ledger.MirroredEvent{
		Target: "linux-01", Task: "task-child", SourceSeq: 3, Type: "ticket_answered",
		Payload: []byte(`{"ticket_id":"tk-done"}`), CreatedAt: time.Now(),
	}); err != nil {
		st.Close()
		t.Fatalf("镜像答复: %v", err)
	}
	st.Close()

	waitOut, _, waitErr := runLedgerCLI(t, dir, "card", "wait", root.ID, "--subtree", "--timeout", "2s")
	if waitErr == nil {
		t.Fatal("子树未完成时应超时，实际成功退出")
	}
	snap := firstJSONLine(t, waitOut)
	if snap["type"] != "card_snapshot" {
		t.Fatalf("第一行 type=%v want card_snapshot out=%q", snap["type"], waitOut)
	}
	if snap["card_id"] != root.ID {
		t.Fatalf("快照 card_id=%v want %s", snap["card_id"], root.ID)
	}
	if snap["subtree"] != true {
		t.Fatalf("subtree 字段应为 true: %+v", snap)
	}
	actionable, _ := snap["actionable"].([]any)
	if actionable == nil {
		t.Fatalf("actionable 缺席（应为空数组或列表）: %+v", snap)
	}
	ids := map[string]map[string]any{}
	for _, item := range actionable {
		row, _ := item.(map[string]any)
		tid, _ := row["ticket_id"].(string)
		ids[tid] = row
	}
	childRow := ids["tk-child"]
	if childRow == nil {
		t.Fatalf("快照缺子卡未决工单 tk-child: %+v out=%q", snap, waitOut)
	}
	if childRow["card_id"] != child.ID {
		t.Fatalf("工单 card_id=%v want 子卡 %s", childRow["card_id"], child.ID)
	}
	if childRow["source_task"] != "task-child" || childRow["source_target"] != "linux-01" {
		t.Fatalf("工单来源三元组不完整: %+v", childRow)
	}
	if _, ok := ids["tk-done"]; ok {
		t.Fatalf("已答复工单不应出现: %+v", snap)
	}
}

func TestCardWaitSnapshotEmittedWhenNothingOwed(t *testing.T) {
	dir := t.TempDir()
	out, _, _ := runLedgerCLI(t, dir, "card", "add", "空欠单", "--project", "demo", "--workflow", "bug")
	var card struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &card)
	waitOut, _, waitErr := runLedgerCLI(t, dir, "card", "wait", card.ID, "--timeout", "2s")
	if waitErr == nil {
		t.Fatal("未完成卡 wait 应超时")
	}
	snap := firstJSONLine(t, waitOut)
	if snap["type"] != "card_snapshot" {
		t.Fatalf("无欠单也要出快照行，type=%v out=%q", snap["type"], waitOut)
	}
	actionable, _ := snap["actionable"].([]any)
	if len(actionable) != 0 {
		t.Fatalf("无欠单时 actionable 应为空数组: %+v", snap)
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
