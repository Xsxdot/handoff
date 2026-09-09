package cmd

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
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
		if err := st.RecordDispatch(cardID, ledger.DispatchSnapshot{
			Target: "test", TaskID: "attempt", Node: "node", Attempt: "attempt",
			Branch: "cards/" + cardID + "-attempt", Purpose: ledger.PurposeReview, Actor: "test",
		}); err != nil {
			writeErr = err
			return
		}
		if _, err := st.AppendMirroredEvent(cardID, ledger.MirroredEvent{
			Target: "test", Task: "attempt", Node: "node", Attempt: "attempt",
			SourceSeq: 1, Type: string(proto.EventTypePermissionAutoAllow),
			Payload: []byte(`{"rule":"safe"}`), CreatedAt: time.Now(),
		}); err != nil {
			writeErr = err
			return
		}
		if _, err := st.AppendMirroredEvent(cardID, ledger.MirroredEvent{
			Target: "test", Task: "attempt", Node: "node", Attempt: "attempt",
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

func TestB353CardWaitFollowIdleRefreshesAuditLedgerEvent(t *testing.T) {
	dir := t.TempDir()
	cardID := createCardWaitFixture(t, dir)
	writerErr := make(chan error, 1)
	go func() {
		time.Sleep(250 * time.Millisecond)
		st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
		if err != nil {
			writerErr <- err
			return
		}
		defer st.Close()
		_, err = st.AddComment(cardID, "idle refresh audit", "普通", "test")
		writerErr <- err
	}()

	started := time.Now()
	out, _, err := runLedgerCLI(t, dir, "card", "wait", cardID, "--follow", "--timeout", "3s")
	elapsed := time.Since(started)
	if writerErrValue := <-writerErr; writerErrValue != nil {
		t.Fatalf("写入 follow idle 审计事件: %v", writerErrValue)
	}
	var codeErr *exitCodeError
	if !errors.As(err, &codeErr) || codeErr.code != ExitTimeout {
		t.Fatalf("仅审计事件后应以 follow idle 超时，err=%v", err)
	}
	if elapsed < 4*time.Second {
		t.Fatalf("账本审计事件未刷新 follow idle：耗时 %s，至少应超过首次 3s 总时长", elapsed)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("审计事件刷新 idle 但不应输出 stdout: %q", out)
	}
}

func insertRawCardWaitEvent(dir, cardID, payload string) (int64, error) {
	db, err := sql.Open("sqlite", filepath.Join(dir, "ledger.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode=WAL&_pragma=foreign_keys(on)")
	if err != nil {
		return 0, err
	}
	defer db.Close()
	result, err := db.Exec(`INSERT INTO card_events (card_id, type, actor, payload, created_at)
		VALUES (?, ?, ?, ?, ?)`, cardID, ledger.EvTaskMirrored, "test", payload, time.Now())
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func TestB353CardWaitStatusMovedNonTerminalDoesNotEmit(t *testing.T) {
	dir := t.TempDir()
	cardID := createCardWaitFixture(t, dir)
	writerErr := make(chan error, 1)
	go func() {
		time.Sleep(250 * time.Millisecond)
		st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
		if err != nil {
			writerErr <- err
			return
		}
		defer st.Close()
		writerErr <- st.MoveCard(cardID, ledger.StatusDoing, "", "test")
	}()

	out, _, err := runLedgerCLI(t, dir, "card", "wait", cardID, "--timeout", "3s")
	if writerErrValue := <-writerErr; writerErrValue != nil {
		t.Fatalf("写入非终态 status_moved: %v", writerErrValue)
	}
	var codeErr *exitCodeError
	if !errors.As(err, &codeErr) || codeErr.code != ExitTimeout {
		t.Fatalf("仅非终态 status_moved 应继续等待并超时，err=%v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("非终态 status_moved 不应输出 stdout: %q", out)
	}
}

func TestB353CardWaitPreservesNullAndRejectsMissingMirroredPayload(t *testing.T) {
	dir := t.TempDir()
	cardID := createCardWaitFixture(t, dir)
	type writeResult struct {
		seq int64
		err error
	}
	writerResult := make(chan writeResult, 1)
	go func() {
		time.Sleep(250 * time.Millisecond)
		st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
		if err != nil {
			writerResult <- writeResult{err: err}
			return
		}
		defer st.Close()
		if err := st.RecordDispatch(cardID, ledger.DispatchSnapshot{
			Target: "test", TaskID: "attempt", Node: "node", Attempt: "attempt",
			Branch: "cards/" + cardID + "-attempt", Purpose: ledger.PurposeReview, Actor: "test",
		}); err != nil {
			writerResult <- writeResult{err: err}
			return
		}
		if _, err := st.AppendMirroredEvent(cardID, ledger.MirroredEvent{
			Target: "test", Task: "attempt", Node: "node", Attempt: "attempt",
			SourceSeq: 1, Type: "delivery_failed", Payload: nil, CreatedAt: time.Now(),
		}); err != nil {
			writerResult <- writeResult{err: err}
			return
		}
		seq, insertErr := insertRawCardWaitEvent(dir, cardID,
			`{"node":"node","attempt":"attempt","task_type":"delivery_failed"}`)
		writerResult <- writeResult{seq: seq, err: insertErr}
	}()

	out, _, err := runLedgerCLI(t, dir, "card", "wait", cardID, "--follow", "--timeout", "5s")
	result := <-writerResult
	if result.err != nil {
		t.Fatalf("写入 null/missing task_mirrored: %v", result.err)
	}
	if err == nil || !strings.Contains(err.Error(), "缺 task_type/payload") ||
		!strings.Contains(err.Error(), cardID) ||
		!strings.Contains(err.Error(), fmt.Sprint(result.seq)) ||
		!strings.Contains(err.Error(), ledger.EvTaskMirrored) {
		t.Fatalf("缺失 mirrored payload 应在 card wait 穿缝报错，err=%v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("null payload 事件应原样输出一行，实际 %d 行: %q", len(lines), out)
	}
	var event ledger.Event
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatalf("stdout 不是 ledger.Event JSON: %v", err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(event.Payload, &envelope); err != nil {
		t.Fatalf("task_mirrored envelope 解码: %v", err)
	}
	if raw, ok := envelope["payload"]; !ok || strings.TrimSpace(string(raw)) != "null" {
		t.Fatalf("原始 null payload 丢失或被改写: %s", event.Payload)
	}
}

func TestB349CardWaitSourceIdentity(t *testing.T) {
	dir := t.TempDir()
	cardID := createCardWaitFixture(t, dir)
	writerErr := make(chan error, 1)
	go func() {
		time.Sleep(250 * time.Millisecond)
		st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
		if err != nil {
			writerErr <- err
			return
		}
		defer st.Close()
		if err := st.RecordDispatch(cardID, ledger.DispatchSnapshot{
			Target: "target-current", TaskID: "attempt-current", Node: "review", Attempt: "attempt-current",
			Branch: "cards/" + cardID + "-attempt-current", Purpose: ledger.PurposeReview, Actor: "test",
		}); err != nil {
			writerErr <- err
			return
		}
		mirrors := []ledger.MirroredEvent{
			{Target: "target-current", Task: "wrong-task", Node: "review", Attempt: "attempt-current", SourceSeq: 1, Type: "question", Payload: []byte(`{"ticket_id":"wrong-task"}`), CreatedAt: time.Now()},
			{Target: "wrong-target", Task: "attempt-current", Node: "review", Attempt: "attempt-current", SourceSeq: 2, Type: "question", Payload: []byte(`{"ticket_id":"wrong-target"}`), CreatedAt: time.Now()},
			{Target: "", Task: "attempt-current", Node: "review", Attempt: "attempt-current", SourceSeq: 7, Type: "question", Payload: []byte(`{"ticket_id":"one-empty"}`), CreatedAt: time.Now()},
			{Target: "target-current", Task: "attempt-current", Node: "review", Attempt: "attempt-old", SourceSeq: 3, Type: "question", Payload: []byte(`{"ticket_id":"old-attempt"}`), CreatedAt: time.Now()},
			{Target: "target-current", Task: "attempt-current", Node: "review", Attempt: "attempt-current", SourceSeq: 999, Type: "delivery_failed", Payload: []byte(`{"ticket_id":"current"}`), CreatedAt: time.Now()},
			{Target: "target-current", Task: "attempt-current", Node: "review", Attempt: "no-snapshot", SourceSeq: 4, Type: "delivery_failed", Payload: []byte(`{"ticket_id":"no-snapshot"}`), CreatedAt: time.Now()},
			{Target: "target-current", Task: "attempt-current", Node: "review", Attempt: "attempt-current", SourceSeq: 5, Type: "permission_auto_allow", Payload: []byte(`{"rule":"safe"}`), CreatedAt: time.Now()},
		}
		for _, event := range mirrors {
			if _, err := st.AppendMirroredEvent(cardID, event); err != nil {
				writerErr <- err
				return
			}
		}
		if _, err := st.AppendMirroredEvent(cardID, ledger.MirroredEvent{
			Target: "target-current", Task: "attempt-current", Node: "", Attempt: "",
			SourceSeq: 6, Type: "question", Payload: []byte(`{"ticket_id":"missing-identity"}`), CreatedAt: time.Now(),
		}); err != nil {
			writerErr <- err
			return
		}
		if err := st.RecordDispatch(cardID, ledger.DispatchSnapshot{
			Target: "", TaskID: "attempt-empty", Node: "empty-node", Attempt: "attempt-empty",
			Branch: "cards/" + cardID + "-attempt-empty", Purpose: ledger.PurposeReview, Actor: "test",
		}); err != nil {
			writerErr <- err
			return
		}
		if _, err := st.AppendMirroredEvent(cardID, ledger.MirroredEvent{
			Target: "", Task: "attempt-empty", Node: "empty-node", Attempt: "attempt-empty",
			SourceSeq: 100, Type: "delivery_failed", Payload: []byte(`{"ticket_id":"double-empty"}`), CreatedAt: time.Now(),
		}); err != nil {
			writerErr <- err
			return
		}
		if err := st.MarkNeedsHuman(cardID, "native action", "test"); err != nil {
			writerErr <- err
			return
		}
		writerErr <- moveCardWaitFixtureToDone(st, cardID)
	}()

	out, _, err := runLedgerCLI(t, dir, "card", "wait", cardID, "--follow", "--timeout", "5s")
	if writeErr := <-writerErr; writeErr != nil {
		t.Fatalf("写入 card wait source identity 事件: %v", writeErr)
	}
	if err != nil {
		t.Fatalf("card wait source identity: %v; output=%q", err, out)
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
	if len(got) != 3 || got[0].Type != ledger.EvTaskMirrored || got[1].Type != ledger.EvTaskMirrored || got[2].Type != ledger.EvNeedsHuman {
		t.Fatalf("仅当前镜像、双空镜像和卡原生事件应输出，got=%+v", got)
	}
	if got[0].SourceTarget != "target-current" || got[0].SourceTask != "attempt-current" || got[0].SourceSeq != 999 {
		t.Fatalf("stdout 丢失当前镜像 source 三列: %+v", got[0])
	}
	if got[1].SourceTarget != "" || got[1].SourceTask != "attempt-empty" || got[1].SourceSeq != 100 {
		t.Fatalf("双空 target 当前镜像 source 三列错误: %+v", got[1])
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.Split(strings.TrimSpace(out), "\n")[2]), &wire); err != nil {
		t.Fatalf("卡原生 stdout JSON: %v", err)
	}
	for _, key := range []string{"source_target", "source_task", "source_seq"} {
		if _, present := wire[key]; present {
			t.Fatalf("卡原生事件不应伪造 source 列 %q: %s", key, out)
		}
	}
}

func TestB349CardWaitSubtreeUsesEventCardIdentity(t *testing.T) {
	dir := t.TempDir()
	rootOut, _, err := runLedgerCLI(t, dir, "card", "add", "根卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatalf("card add root: %v", err)
	}
	var root struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(rootOut)), &root); err != nil || root.ID == "" {
		t.Fatalf("解析根卡: err=%v output=%q", err, rootOut)
	}
	childOut, _, err := runLedgerCLI(t, dir, "card", "split", root.ID, "子卡")
	if err != nil {
		t.Fatalf("card split child: %v", err)
	}
	var child struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(childOut)), &child); err != nil || child.ID == "" {
		t.Fatalf("解析子卡: err=%v output=%q", err, childOut)
	}

	writerErr := make(chan error, 1)
	go func() {
		time.Sleep(250 * time.Millisecond)
		st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
		if err != nil {
			writerErr <- err
			return
		}
		defer st.Close()
		if err := st.RecordDispatch(root.ID, ledger.DispatchSnapshot{
			Target: "root-target", TaskID: "attempt-root", Node: "review", Attempt: "attempt-root",
			Branch: "cards/" + root.ID + "-attempt-root", Purpose: ledger.PurposeReview, Actor: "test",
		}); err != nil {
			writerErr <- err
			return
		}
		if err := st.RecordDispatch(child.ID, ledger.DispatchSnapshot{
			Target: "child-target", TaskID: "attempt-child", Node: "review", Attempt: "attempt-child",
			Branch: "cards/" + child.ID + "-attempt-child", Purpose: ledger.PurposeReview, Actor: "test",
		}); err != nil {
			writerErr <- err
			return
		}
		for _, event := range []ledger.MirroredEvent{
			{
				Target: "child-target", Task: "attempt-child", Node: "review", Attempt: "attempt-child",
				SourceSeq: 11, Type: "question", Payload: []byte(`{"ticket_id":"child-current"}`), CreatedAt: time.Now(),
			},
			{
				Target: "root-target", Task: "attempt-root", Node: "review", Attempt: "attempt-root",
				SourceSeq: 12, Type: "question", Payload: []byte(`{"ticket_id":"child-stale"}`), CreatedAt: time.Now(),
			},
		} {
			if _, err := st.AppendMirroredEvent(child.ID, event); err != nil {
				writerErr <- err
				return
			}
		}
		for _, id := range []string{child.ID, root.ID} {
			if err := moveCardWaitFixtureToDone(st, id); err != nil {
				writerErr <- err
				return
			}
		}
		writerErr <- nil
	}()

	out, _, err := runLedgerCLI(t, dir, "card", "wait", root.ID, "--subtree", "--follow", "--timeout", "5s")
	if writeErr := <-writerErr; writeErr != nil {
		t.Fatalf("写入子卡 source identity 事件: %v", writeErr)
	}
	if err != nil {
		t.Fatalf("card wait --subtree 子卡身份: %v; output=%q", err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("根卡快照不得放行/拒绝子卡事件，期望仅输出当前子卡事件，实际 %d 行: %q", len(lines), out)
	}
	var event ledger.Event
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatalf("stdout 不是 ledger.Event JSON: %v; line=%q", err, lines[0])
	}
	if event.CardID != child.ID || event.SourceTask != "attempt-child" || event.SourceTarget != "child-target" ||
		!strings.Contains(string(event.Payload), "child-current") {
		t.Fatalf("子卡事件未按子卡当前派发快照放行: %+v", event)
	}
}

func TestB353CardWaitFollowIdleRefreshesEachLedgerEvent(t *testing.T) {
	previousProcs := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(previousProcs) })
	ctx, noteActivity, stop, timedOut := startCardWaitIdle(context.Background(), 80*time.Millisecond)
	defer stop()
	time.Sleep(10 * time.Millisecond)
	for i := 0; i < 12; i++ {
		noteActivity()
		deadline := time.Now().Add(15 * time.Millisecond)
		for time.Now().Before(deadline) {
		}
	}
	if timedOut() {
		t.Fatal("连续账本事件后 idle 不应因单槽通知丢刷新而超时")
	}
	select {
	case <-ctx.Done():
		t.Fatalf("连续账本事件后 follow idle context 已取消: %v", ctx.Err())
	default:
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
