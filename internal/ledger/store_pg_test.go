// PG 冒烟：真 PG 上跑 schema + 基本读写。默认 skip，设
// LEDGER_TEST_PG_DSN 后启用（判据⑩落地前审核者本地跑一次）。
package ledger

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

func newPGStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("LEDGER_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未设 LEDGER_TEST_PG_DSN，跳过 PG 冒烟")
	}
	s, err := Open(dsn)
	if err != nil {
		t.Fatalf("open pg: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// newB409PGStore 验证数据库名后才打开 schema，避免把 B409 合成数据写进其它账本。
func newB409PGStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("LEDGER_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未设 LEDGER_TEST_PG_DSN，跳过 B409 PG 房间历史测试")
	}
	probe, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("连接 B409 PG 测试库: %v", err)
	}
	var databaseName string
	if err := probe.QueryRow(`SELECT current_database()`).Scan(&databaseName); err != nil {
		probe.Close()
		t.Fatalf("确认 B409 PG 测试库名称: %v", err)
	}
	if err := probe.Close(); err != nil {
		t.Fatalf("关闭 B409 PG 名称探针: %v", err)
	}
	if databaseName != "handoff_b409_test" {
		t.Fatalf("拒绝向非专用 PostgreSQL 数据库写入合成数据: current_database()=%q", databaseName)
	}
	store, err := Open(dsn)
	if err != nil {
		t.Fatalf("打开 B409 PG 测试库: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestRoomHistoryPostgresMatchesWindowContract(t *testing.T) {
	s := newB409PGStore(t)
	if err := seedTestWorkflows(t, s); err != nil {
		t.Fatalf("准备隔离测试工作流: %v", err)
	}
	roomID := fmt.Sprintf("session:b409-pg-%d", time.Now().UnixNano())
	otherRoomID := roomID + "-other"
	seqs := make([]int64, 0, 260)
	t.Cleanup(func() {
		for _, seq := range seqs {
			if _, err := s.db.Exec(s.q(`DELETE FROM card_events WHERE seq = ?`), seq); err != nil {
				t.Errorf("清理 B409 PG 合成事件 %d: %v", seq, err)
			}
		}
	})
	targetSeqs := make([]int64, 0, 205)
	for i := 0; i < 205; i++ {
		if i%4 == 0 {
			seq, err := s.RecordRoomMessage("", proto.RoomMessage{
				Room: otherRoomID, Kind: proto.RoomMsgUser, Body: "unrelated",
			}, "b409-pg-test")
			if err != nil {
				t.Fatalf("写入无关 PostgreSQL 房间: %v", err)
			}
			seqs = append(seqs, seq)
		}
		seq, err := s.RecordRoomMessage("", proto.RoomMessage{
			Room: roomID, Kind: proto.RoomMsgUser, Body: fmt.Sprintf("target-%03d", i),
		}, "b409-pg-test")
		if err != nil {
			t.Fatalf("写入 PostgreSQL 目标消息 %d: %v", i, err)
		}
		seqs = append(seqs, seq)
		targetSeqs = append(targetSeqs, seq)
	}

	got, err := s.RoomMessagesBeforeContext(t.Context(), roomID, 0, 200)
	if err != nil {
		t.Fatalf("PostgreSQL 最近窗口查询: %v", err)
	}
	if len(got) != 200 {
		t.Fatalf("PostgreSQL 最近窗口应有 200 条，得到 %d", len(got))
	}
	for i, event := range got {
		if want := targetSeqs[i+5]; event.Seq != want {
			t.Fatalf("PostgreSQL 升序窗口第 %d 条 seq=%d，期望 %d", i, event.Seq, want)
		}
	}
	page, err := s.RoomMessagesBeforeContext(t.Context(), roomID, targetSeqs[204], 20)
	if err != nil {
		t.Fatalf("PostgreSQL beforeSeq 查询: %v", err)
	}
	if len(page) != 20 || page[len(page)-1].Seq >= targetSeqs[204] {
		t.Fatalf("PostgreSQL beforeSeq 必须排他并返回最近窗口: count=%d page=%+v", len(page), page)
	}
	const cardProject = "b409-pg-test"
	if err := s.SetCardPrefix(cardProject, "Z"); err != nil {
		t.Fatalf("给隔离夹具设置未占用前缀: %v", err)
	}
	card, err := s.CreateCard(NewCard{Title: "B409 卡房间读取夹具", Project: cardProject, Workflow: "bug", Actor: "b409-pg-test"})
	if err != nil {
		t.Fatalf("创建 B409 卡房间夹具: %v", err)
	}
	t.Cleanup(func() {
		if _, err := s.db.Exec(`DELETE FROM card_events WHERE card_id = $1`, card.ID); err != nil {
			t.Errorf("清理 B409 卡事件: %v", err)
		}
		if _, err := s.db.Exec(`DELETE FROM cards WHERE id = $1`, card.ID); err != nil {
			t.Errorf("清理 B409 卡: %v", err)
		}
	})
	if _, err := s.RecordRoomMessage(card.ID, proto.RoomMessage{
		Room: "session:b409-payload-must-not-win", Kind: proto.RoomMsgUser, Body: "card room",
	}, "b409-pg-test"); err != nil {
		t.Fatalf("写入 PostgreSQL 卡房间消息: %v", err)
	}
	legacySeq, err := s.RecordRoomMessage("", proto.RoomMessage{
		Room: card.ID, Kind: proto.RoomMsgUser, Body: "legacy card room",
	}, "b409-pg-test")
	if err != nil {
		t.Fatalf("写入 PostgreSQL 旧式卡房间消息: %v", err)
	}
	seqs = append(seqs, legacySeq)
	cardRoom, err := s.RoomMessagesBeforeContext(t.Context(), card.ID, 0, 200)
	if err != nil {
		t.Fatalf("PostgreSQL 卡房间身份读取: %v", err)
	}
	if len(cardRoom) != 2 || cardRoom[0].CardID != card.ID || cardRoom[1].CardID != "" {
		t.Fatalf("PostgreSQL 卡片身份优先于载荷 Room，且保留旧事件：%+v", cardRoom)
	}
	wrongRoom, err := s.RoomMessagesBeforeContext(t.Context(), "session:b409-payload-must-not-win", 0, 200)
	if err != nil || len(wrongRoom) != 0 {
		t.Fatalf("PostgreSQL 非空 card_id 不得因载荷 Room 串房间: events=%+v err=%v", wrongRoom, err)
	}
	if _, err := s.RoomMessagesBeforeContext(t.Context(), roomID+"-missing", 0, 200); err != nil {
		t.Fatalf("PostgreSQL 空房间应返回空结果: %v", err)
	}
	if _, err := s.RoomMessagesBeforeContext(t.Context(), roomID, 0, 0); err == nil {
		t.Fatal("PostgreSQL 非正 limit 应报错")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.RoomMessagesBeforeContext(ctx, roomID, 0, 200); err != context.Canceled {
		t.Fatalf("PostgreSQL 取消未传播: %v", err)
	}
}

func TestRoomHistoryPostgresCancelsBlockedRead(t *testing.T) {
	s := newB409PGStore(t)

	// ACCESS EXCLUSIVE makes the real room-history SELECT wait inside PostgreSQL;
	// cancel only after pg_stat_activity confirms it reached the lock wait.
	lockConn, err := s.db.Conn(t.Context())
	if err != nil {
		t.Fatalf("获取 PostgreSQL 锁连接: %v", err)
	}
	defer lockConn.Close()
	lockTx, err := lockConn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("开启 PostgreSQL 锁事务: %v", err)
	}
	defer lockTx.Rollback()
	if _, err := lockTx.ExecContext(t.Context(), `LOCK TABLE card_events IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("锁住隔离账本事件表: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := s.RoomMessagesBeforeContext(ctx, "session:b409-cancel", 0, 200)
		result <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		var waiting int
		if err := s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM pg_stat_activity
			WHERE datname = current_database() AND state = 'active'
			AND wait_event_type = 'Lock' AND query LIKE 'WITH card_room AS (%'`).Scan(&waiting); err != nil {
			t.Fatalf("确认 PostgreSQL 房间历史查询进入锁等待: %v", err)
		}
		if waiting > 0 {
			blocked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		cancel()
		select {
		case <-result:
		case <-time.After(2 * time.Second):
		}
		t.Fatal("房间历史查询未进入 PostgreSQL 锁等待，无法验证在途取消")
	}

	canceledAt := time.Now()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("PostgreSQL 在途查询取消应传播 context.Canceled，得到 %v", err)
		}
		if elapsed := time.Since(canceledAt); elapsed > 2*time.Second {
			t.Fatalf("PostgreSQL 在途查询取消耗时过长: %s", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PostgreSQL 查询在 context 取消后仍未退出")
	}
}

func TestTaskLinkKeysPostgresPayloadDoesNotGrowWithDispatchedHistory(t *testing.T) {
	s := newB409PGStore(t)
	if err := seedTestWorkflows(t, s); err != nil {
		t.Fatalf("准备隔离测试工作流: %v", err)
	}
	project := fmt.Sprintf("b409-key-read-%d", time.Now().UnixNano())
	prefixValue := time.Now().UnixNano()
	prefix := string([]byte{
		byte('A' + prefixValue%26), byte('A' + prefixValue/26%26),
		byte('A' + prefixValue/676%26), byte('A' + prefixValue/17576%26),
	})
	if err := s.SetCardPrefix(project, prefix); err != nil {
		t.Fatalf("给隔离夹具设置未占用前缀: %v", err)
	}
	card, err := s.CreateCard(NewCard{Title: "B409 link key read", Project: project, Workflow: "bug", Actor: "b409-pg-test"})
	if err != nil {
		t.Fatalf("创建 B409 link-key 卡夹具: %v", err)
	}
	t.Cleanup(func() {
		if _, err := s.db.Exec(`DELETE FROM card_events WHERE card_id = $1`, card.ID); err != nil {
			t.Errorf("清理 B409 link-key 事件: %v", err)
		}
		if _, err := s.db.Exec(`DELETE FROM card_tasks WHERE card_id = $1`, card.ID); err != nil {
			t.Errorf("清理 B409 link-key 关联: %v", err)
		}
		if _, err := s.db.Exec(`DELETE FROM cards WHERE id = $1`, card.ID); err != nil {
			t.Errorf("清理 B409 link-key 卡: %v", err)
		}
		if _, err := s.db.Exec(`DELETE FROM card_prefixes WHERE project = $1`, project); err != nil {
			t.Errorf("清理 B409 link-key 卡号前缀: %v", err)
		}
	})
	for _, snapshot := range []DispatchSnapshot{
		{Target: "mac-02", TaskID: "task-a", Node: "implement", Attempt: "task-a", Purpose: PurposeImplement, Actor: "b409-pg-test"},
		{Target: "linux-01", TaskID: "task-b", Node: "review", Attempt: "task-b", Purpose: PurposeReview, Actor: "b409-pg-test"},
	} {
		if err := s.RecordDispatch(card.ID, snapshot); err != nil {
			t.Fatalf("写入派发快照: %v", err)
		}
		if err := s.LinkTask(card.ID, snapshot.Target, snapshot.TaskID, snapshot.Purpose, "b409-pg-test"); err != nil {
			t.Fatalf("写入固定关联键: %v", err)
		}
	}
	beforeKeys, beforeRows, beforeBytes := captureTaskLinkKeyRead(t, s)
	if len(beforeKeys) != 2 || beforeRows != 2 || beforeBytes != int64(len("linux-01task-b")+len("mac-02task-a")) {
		t.Fatalf("基准 Store/PG 扫描边界不符: keys=%+v rows=%d bytes=%d", beforeKeys, beforeRows, beforeBytes)
	}

	const unrelatedDispatches = 10_000
	if _, err := s.db.Exec(`INSERT INTO card_events(card_id, type, actor, payload, created_at)
		SELECT $1, $2, 'b409-pg-noise',
			jsonb_build_object('target', 'noise', 'task_id', 'noise-' || n,
				'node', 'unrelated-node', 'attempt', 'noise-' || n,
				'body', repeat('x', 256)), now()
		FROM generate_series(1, $3) AS series(n)`, card.ID, EvDispatched, unrelatedDispatches); err != nil {
		t.Fatalf("写入 10k 无关 EvDispatched 历史: %v", err)
	}

	afterKeys, afterRows, afterBytes := captureTaskLinkKeyRead(t, s)
	if len(afterKeys) != 2 || afterRows != beforeRows || afterBytes != beforeBytes {
		t.Fatalf("10k 无关 EvDispatched 不得扩大 key-only PG 读取: before=%d/%d after=%d/%d keys=%+v",
			beforeRows, beforeBytes, afterRows, afterBytes, afterKeys)
	}
	t.Logf("B409_KEY_READ rows_before=%d key_bytes_before=%d rows_after_10k=%d key_bytes_after_10k=%d unrelated_events=%d",
		beforeRows, beforeBytes, afterRows, afterBytes, unrelatedDispatches)
	legacy, err := s.AllTaskLinks()
	if err != nil {
		t.Fatalf("旧投影读面语义应保持可用: %v", err)
	}
	if len(legacy) != 2 || legacy[0].Node == "" || legacy[0].Attempt == "" || legacy[1].Node == "" || legacy[1].Attempt == "" {
		t.Fatalf("旧 AllTaskLinks 派发身份投影被改变: %+v", legacy)
	}
}

func TestTaskLinkKeysPostgresCancelsBlockedRead(t *testing.T) {
	s := newB409PGStore(t)
	lockConn, err := s.db.Conn(t.Context())
	if err != nil {
		t.Fatalf("获取 PostgreSQL 锁连接: %v", err)
	}
	defer lockConn.Close()
	lockTx, err := lockConn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("开启 PostgreSQL 锁事务: %v", err)
	}
	defer lockTx.Rollback()
	if _, err := lockTx.ExecContext(t.Context(), `LOCK TABLE card_tasks IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("锁住隔离账本关联表: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := s.AllTaskLinkKeysContext(ctx)
		result <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		var waiting int
		if err := s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM pg_stat_activity
			WHERE datname = current_database() AND state = 'active' AND wait_event_type = 'Lock'
			AND query LIKE 'SELECT target, task_id FROM card_tasks%'`).Scan(&waiting); err != nil {
			t.Fatalf("确认 key-only PostgreSQL 查询进入锁等待: %v", err)
		}
		if waiting > 0 {
			blocked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		cancel()
		select {
		case <-result:
		case <-time.After(2 * time.Second):
		}
		t.Fatal("key-only 查询未进入 PostgreSQL 锁等待，无法验证在途取消")
	}
	canceledAt := time.Now()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("取消 PostgreSQL key-only 查询应返回 context.Canceled，得到 %v", err)
		}
		if elapsed := time.Since(canceledAt); elapsed > 2*time.Second {
			t.Fatalf("取消 PostgreSQL key-only 查询耗时过长: %s", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PostgreSQL key-only 查询在 context 取消后仍未退出")
	}
}

// captureTaskLinkKeyRead observes the actual rows and string payload bytes
// scanned by the Store method through its structured completion log.
func captureTaskLinkKeyRead(t *testing.T, s *Store) ([]TaskLinkKey, int, int64) {
	t.Helper()
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(previous)
	keys, err := s.AllTaskLinkKeysContext(t.Context())
	if err != nil {
		t.Fatalf("Store AllTaskLinkKeysContext: %v", err)
	}
	var rows int
	var keyBytes int64
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("解析 key-only 读取探针: %v; line=%s", err, line)
		}
		if record["msg"] == "读取未挂账关联键完成" {
			rows = int(record["rows_read"].(float64))
			keyBytes = int64(record["key_bytes"].(float64))
		}
	}
	return keys, rows, keyBytes
}

// smokeProject 冒烟数据的项目名——同时是清理段的作用域边界。
const smokeProject = "pg-smoke-临时数据"

func TestPGSchema(t *testing.T) {
	s := newPGStore(t)
	if _, err := s.db.Exec("SELECT * FROM cards LIMIT 0"); err != nil {
		t.Fatalf("cards 表: %v", err)
	}
}

// TestPGSmokeEndToEnd 在真 PG 上过一遍核心链路：seed→建卡→gate→CAS→
// 合并→裁决→事件序完整。SQLite 全量测试覆盖逻辑，这里只验方言差异
// （$N 占位、RETURNING、JSONB、partial index、pg_notify 不报错）。
//
// 清理段会删除账本表中的数据，因此 LEDGER_TEST_PG_DSN 必须指向专用测试库。
func TestPGSmokeEndToEnd(t *testing.T) {
	s := newPGStore(t)
	if err := seedTestWorkflows(t, s); err != nil {
		t.Fatalf("写测试工作流: %v", err)
	}
	if err := s.EnsureMinB(9000); err != nil { // 高位垫号，避免与库内已有数据撞
		t.Fatalf("minb: %v", err)
	}
	card, err := s.CreateCard(NewCard{Title: "pg 冒烟", Project: smokeProject, Workflow: "feature", Actor: "pgtest"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.MoveCard(card.ID, "已出spec", "", "pgtest"); err == nil {
		t.Fatal("gate 在 PG 上应同样拒绝")
	}
	if _, err := s.AttachFile(card.ID, "spec", "s.md", "pgtest"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := s.MoveCard(card.ID, "已出spec", "", "pgtest"); err != nil {
		t.Fatalf("gate 放行: %v", err)
	}
	member, err := s.CreateCard(NewCard{Title: "成员", Project: smokeProject, Workflow: "bug", Actor: "pgtest"})
	if err != nil {
		t.Fatalf("member: %v", err)
	}
	if err := s.MergeCards([]string{member.ID}, card.ID, "pgtest"); err != nil {
		t.Fatalf("merge: %v", err)
	}
	decision, err := s.OpenDecision(card.ID, "冒烟请示", []string{"a", "b"}, "pgtest")
	if err != nil {
		t.Fatalf("decision: %v", err)
	}
	if err := s.AnswerDecision(decision.ID, "a", "pgtest"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	events, err := s.EventsFromAsc([]string{card.ID, member.ID}, 0, 100)
	if err != nil || len(events) < 5 {
		t.Fatalf("事件序: %v n=%d", err, len(events))
	}
	// 清理只删本次冒烟自己造的数据（project = 冒烟专用值），不碰同库里的
	// 其它行——原先是 DELETE ... WHERE TRUE 全表清，一旦 DSN 指向带真数据
	// 的开发库就是一次静默误删；测试的清理不该有超出自己造物的杀伤半径。
	for _, stmt := range []string{
		`DELETE FROM card_events WHERE card_id IN (SELECT id FROM cards WHERE project = $1)`,
		`DELETE FROM card_relations WHERE from_id IN (SELECT id FROM cards WHERE project = $1)
			OR to_id IN (SELECT id FROM cards WHERE project = $1)`,
		`DELETE FROM decisions WHERE card_id IN (SELECT id FROM cards WHERE project = $1)`,
		`DELETE FROM cards WHERE project = $1`,
	} {
		if _, err := s.db.Exec(stmt, smokeProject); err != nil {
			t.Logf("清理冒烟数据: %v", err)
		}
	}
}
