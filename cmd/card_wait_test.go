package cmd

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

func createCardWaitFixture(t *testing.T, dir string) string {
	t.Helper()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "B353 wait", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatalf("card add: %v", err)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatalf("解析 card add: %v; output=%q", err, out)
	}
	if card.ID == "" {
		t.Fatalf("card add 未返回 id: %q", out)
	}
	return card.ID
}

func moveCardWaitFixtureToDone(st *ledger.Store, cardID string) error {
	for _, status := range []string{ledger.StatusDoing, ledger.StatusReview, ledger.StatusDone} {
		if err := st.MoveCard(cardID, status, "", "test"); err != nil {
			return err
		}
	}
	return nil
}

func TestB353CardWaitHelpHasFollow(t *testing.T) {
	dir := t.TempDir()
	out, stderr, err := runLedgerCLI(t, dir, "card", "wait", "--help")
	if err != nil {
		t.Fatalf("card wait --help: %v", err)
	}
	help := out + stderr
	if !strings.Contains(help, "--follow") || !strings.Contains(help, "--subtree") {
		t.Fatalf("card wait help 缺少 --follow/--subtree: %q", help)
	}
}

func TestB353CardWaitDefaultEmitsFirstActionAndExits(t *testing.T) {
	dir := t.TempDir()
	cardID := createCardWaitFixture(t, dir)
	writerErr := make(chan error, 1)
	go func() {
		var writeErr error
		defer func() { writerErr <- writeErr }()
		time.Sleep(250 * time.Millisecond)
		st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
		if err != nil {
			writeErr = err
			return
		}
		defer st.Close()
		if _, err := st.AddComment(cardID, "audit only", "普通", "test"); err != nil {
			writeErr = err
			return
		}
		if err := st.MarkNeedsHuman(cardID, "需要人工", "test"); err != nil {
			writeErr = err
			return
		}
		time.Sleep(250 * time.Millisecond)
		if err := st.ClearNeedsHuman(cardID, "test"); err != nil {
			writeErr = err
			return
		}
		writeErr = moveCardWaitFixtureToDone(st, cardID)
	}()

	out, _, err := runLedgerCLI(t, dir, "card", "wait", cardID, "--timeout", "5s")
	if err != nil {
		t.Fatalf("card wait 默认模式: %v", err)
	}
	if err := <-writerErr; err != nil {
		t.Fatalf("写入 card wait 事件: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("默认模式应只输出一条可动作事件，实际 %d 行: %q", len(lines), out)
	}
	var event ledger.Event
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatalf("stdout 不是 ledger.Event JSON: %v; line=%q", err, lines[0])
	}
	if event.CardID != cardID || event.Type != ledger.EvNeedsHuman {
		t.Fatalf("默认模式事件=%+v, want card=%s type=%s", event, cardID, ledger.EvNeedsHuman)
	}
}

func TestB353CardWaitFollowFiltersAndContinues(t *testing.T) {
	dir := t.TempDir()
	cardID := createCardWaitFixture(t, dir)
	writerErr := make(chan error, 1)
	go func() {
		var writeErr error
		defer func() { writerErr <- writeErr }()
		time.Sleep(250 * time.Millisecond)
		st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
		if err != nil {
			writeErr = err
			return
		}
		defer st.Close()
		if err := st.MarkNeedsHuman(cardID, "need human", "test"); err != nil {
			writeErr = err
			return
		}
		if err := st.ClearNeedsHuman(cardID, "test"); err != nil {
			writeErr = err
			return
		}
		decision, err := st.OpenDecision(cardID, "choose", []string{"a", "b"}, "test")
		if err != nil {
			writeErr = err
			return
		}
		if err := st.AnswerDecision(decision.ID, "a", "test"); err != nil {
			writeErr = err
			return
		}
		if _, err := st.RecordRoomMessage(cardID, proto.RoomMessage{
			Room: cardID, Kind: proto.RoomMsgUser, Body: "真人消息",
		}, "test"); err != nil {
			writeErr = err
			return
		}
		if _, err := st.RecordRoomMessage(cardID, proto.RoomMessage{
			Room: cardID, Kind: proto.RoomMsgUser, Body: "系统消息", BySystem: true,
		}, "test"); err != nil {
			writeErr = err
			return
		}
		if _, err := st.AddComment(cardID, "comment", "普通", "test"); err != nil {
			writeErr = err
			return
		}
		if _, err := st.AppendMirroredEvent(cardID, ledger.MirroredEvent{
			Target: "test", Task: "task-audit", Node: "node", Attempt: "attempt",
			SourceSeq: 1, Type: string(proto.EventTypePermissionAutoAllow),
			Payload: []byte(`{"rule":"safe"}`), CreatedAt: time.Now(),
		}); err != nil {
			writeErr = err
			return
		}
		if _, err := st.AppendMirroredEvent(cardID, ledger.MirroredEvent{
			Target: "test", Task: "task-action", Node: "node", Attempt: "attempt",
			SourceSeq: 2, Type: string(proto.EventTypeDeliveryFailed),
			Payload: []byte(`{"ticket_id":"q1"}`), CreatedAt: time.Now(),
		}); err != nil {
			writeErr = err
			return
		}
		writeErr = moveCardWaitFixtureToDone(st, cardID)
	}()

	out, _, err := runLedgerCLI(t, dir, "card", "wait", cardID, "--follow", "--timeout", "5s")
	if err != nil {
		t.Fatalf("card wait --follow: %v", err)
	}
	if err := <-writerErr; err != nil {
		t.Fatalf("写入 card wait follow 事件: %v", err)
	}
	var got []ledger.Event
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var event ledger.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("stdout 不是 ledger.Event JSON: %v; line=%q", err, line)
		}
		got = append(got, event)
	}
	wantTypes := []string{ledger.EvNeedsHuman, ledger.EvNeedsCleared,
		ledger.EvDecisionOpened, ledger.EvDecisionAnswered, ledger.EvRoomMessage,
		ledger.EvTaskMirrored}
	if len(got) != len(wantTypes) {
		t.Fatalf("follow 输出 %d 行，want %d: %+v", len(got), len(wantTypes), got)
	}
	for i, want := range wantTypes {
		if got[i].Type != want || got[i].CardID != cardID {
			t.Fatalf("follow event[%d]=%+v, want card=%s type=%s", i, got[i], cardID, want)
		}
	}
	if string(got[len(got)-1].Payload) == "" {
		t.Fatal("task_mirrored 原始 payload 不应丢失")
	}
	if !strings.Contains(string(got[len(got)-1].Payload), "ticket_id") {
		t.Fatalf("follow 最后一条 task_mirrored 应是可动作 delivery_failed，payload=%s", got[len(got)-1].Payload)
	}
}

func TestB353CardWaitTimeoutModes(t *testing.T) {
	dir := t.TempDir()
	cardID := createCardWaitFixture(t, dir)
	writerErr := make(chan error, 1)
	go func() {
		var writeErr error
		defer func() { writerErr <- writeErr }()
		time.Sleep(250 * time.Millisecond)
		st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
		if err != nil {
			writeErr = err
			return
		}
		defer st.Close()
		if _, err := st.AddComment(cardID, "audit", "普通", "test"); err != nil {
			writeErr = err
		}
	}()
	_, _, err := runLedgerCLI(t, dir, "card", "wait", cardID, "--timeout", "500ms")
	if writerErrValue := <-writerErr; writerErrValue != nil {
		t.Fatalf("写入默认超时事件: %v", writerErrValue)
	}
	var codeErr *exitCodeError
	if !errors.As(err, &codeErr) || codeErr.code != ExitTimeout {
		t.Fatalf("默认模式超时=%v，want exit code 124", err)
	}

	dir = t.TempDir()
	cardID = createCardWaitFixture(t, dir)
	writerErr = make(chan error, 1)
	go func() {
		var writeErr error
		defer func() { writerErr <- writeErr }()
		time.Sleep(250 * time.Millisecond)
		st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
		if err != nil {
			writeErr = err
			return
		}
		defer st.Close()
		if _, err := st.AddComment(cardID, "audit", "普通", "test"); err != nil {
			writeErr = err
		}
	}()
	_, _, err = runLedgerCLI(t, dir, "card", "wait", cardID, "--follow", "--timeout", "200ms")
	if writerErrValue := <-writerErr; writerErrValue != nil {
		t.Fatalf("写入 follow 超时事件: %v", writerErrValue)
	}
	if !errors.As(err, &codeErr) || codeErr.code != ExitTimeout {
		t.Fatalf("follow 模式超时=%v，want exit code 124", err)
	}
}

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
	if strings.TrimSpace(waitOut) != "" {
		t.Fatalf("status_moved 终态检查不应输出唤醒行: %q", waitOut)
	}
	st, err := ledger.Open(dir + "/ledger.db")
	if err != nil {
		t.Fatalf("打开账本核对终态: %v", err)
	}
	defer st.Close()
	for _, id := range []string{child.ID, root.ID} {
		card, err := st.GetCard(id)
		if err != nil {
			t.Fatalf("读取卡 %s: %v", id, err)
		}
		if card.Status != ledger.StatusDone {
			t.Fatalf("卡 %s status=%s, want %s", id, card.Status, ledger.StatusDone)
		}
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
