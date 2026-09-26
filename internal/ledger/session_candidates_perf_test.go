// 会话候选读的性能证据（B409 U5；有界候选读契约第 46–48 条 + B409 r2 增长矩阵）。
//
// 固定有效候选集，分两档灌入无关事件（镜像历史带大载荷 / 结构无关类型 / 无关
// 房间的群消息），断言：
//   1. 候选查询实际返回 rows / payload bytes 恒定（不随全流增长）；
//   2. 执行计划用地址候选键 + seq 范围：SQLite EXPLAIN QUERY PLAN 不出现
//      card_events 全表 SCAN；PostgreSQL EXPLAIN (ANALYZE, BUFFERS) 走
//      room_message 局部索引，不出现 Seq Scan on card_events。
// 原始计划文本经 t.Logf（前缀 B409_U5_*）落测试输出，作为交付证据原文。
package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// candidatePerfFixture 建固定候选集：member 席位卡 C1、other 席位卡 C2，
// session:1 内 3 条 member 侧命中候选（user:sy：身份 @、reply→user:sy 作者；
// cli:member#m1：席位卡 @）与 3 条非命中行（other 席位卡 @、无寻址、other 的
// reply 目标）。返回 (卡号, 候选 rows, 候选 payload bytes)。
func candidatePerfFixture(t *testing.T, s *Store, actor string) (memberCard string, rows, bytes int) {
	t.Helper()
	if _, err := s.PutWorkflow("charter", WorkflowDef{States: []string{"待办", "完成"}}); err != nil {
		t.Fatal(err)
	}
	c1, err := s.CreateCard(NewCard{Project: "u5", Title: actor + " C1", Actor: actor, Workflow: "charter"})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := s.CreateCard(NewCard{Project: "u5", Title: actor + " C2", Actor: actor, Workflow: "charter"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BindSeat(c1.ID, "cli:member#m1", proto.SeatSourceBind, SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	if err := s.BindSeat(c2.ID, "cli:other#o1", proto.SeatSourceBind, SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	sendAs := func(msgActor, roomID, kind string, mentions []string, replyTo int64) int64 {
		t.Helper()
		msg := proto.RoomMessage{Room: roomID, Kind: kind, Body: strings.Repeat("u", 200), Mentions: mentions, ReplyTo: replyTo}
		seq, err := s.RecordRoomMessage("", msg, msgActor)
		if err != nil {
			t.Fatalf("候选夹具发言: %v", err)
		}
		return seq
	}
	send := func(roomID, kind string, mentions []string, replyTo int64) int64 {
		return sendAs(actor, roomID, kind, mentions, replyTo)
	}
	send("session:1", proto.RoomMsgUser, []string{"user:sy"}, 0)              // 身份 @：user:sy 命中
	memberMsg := sendAs("user:sy", "session:1", proto.RoomMsgUser, nil, 0)    // user:sy 作者（reply 目标）
	send("session:1", proto.RoomMsgUser, nil, memberMsg)                      // reply→user:sy：命中
	send("session:1", proto.RoomMsgUser, []string{c1.ID}, 0)                  // member 席位卡 @：命中
	send("session:1", proto.RoomMsgUser, []string{c2.ID}, 0)                  // other 席位卡 @：非命中
	send("session:1", proto.RoomMsgUser, nil, 0)                              // 无寻址：非命中

	gotRows, gotBytes := mustCandidateStats(t, s)
	if gotRows != 3 {
		t.Fatalf("候选夹具命中数=%d，期望 3（user:sy 身份+reply、member 席位卡）", gotRows)
	}
	return c1.ID, gotRows, gotBytes
}

// mustCandidateStats 统计两个订阅 member 的候选 rows/bytes（增长断言的读数）。
func mustCandidateStats(t *testing.T, s *Store) (rows, bytes int) {
	t.Helper()
	for _, member := range []string{"user:sy", "cli:member#m1"} {
		events, err := s.SessionMessageCandidates(member, 0, 0, 100)
		if err != nil {
			t.Fatalf("候选读 %s: %v", member, err)
		}
		for _, ev := range events {
			rows++
			bytes += len(ev.Payload)
		}
	}
	return rows, bytes
}

// insertUnrelatedEventsSQLite：count 条无关事件（镜像历史带 470B 载荷、结构
// 无关类型、无关房间群消息各占约三分之一），单事务写入。
func insertUnrelatedEventsSQLite(t *testing.T, s *Store, actor string, count int) {
	t.Helper()
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		mirror := count / 3
		structural := count / 3
		otherRoom := count - mirror - structural
		for i := 0; i < mirror; i++ {
			payload := fmt.Sprintf(`{"node":"n","attempt":"a","task_type":"event","payload":{"i":%d,"body":"%s"}}`,
				i, strings.Repeat("x", 400))
			if _, err := tx.Exec(`INSERT INTO card_events (card_id, type, actor, payload, source_target, source_task, source_seq, created_at)
				VALUES (NULL, 'task_mirrored', ?, ?, ?, ?, ?, datetime('now'))`,
				actor, payload, actor, fmt.Sprintf("t-%d", i), i); err != nil {
				return err
			}
		}
		for i := 0; i < structural; i++ {
			if _, err := tx.Exec(`INSERT INTO card_events (card_id, type, actor, payload, created_at)
				VALUES (NULL, 'unrelated_event', ?, ?, datetime('now'))`,
				actor, fmt.Sprintf(`{"i":%d,"body":"%s"}`, i, strings.Repeat("y", 300))); err != nil {
				return err
			}
		}
		for i := 0; i < otherRoom; i++ {
			payload := fmt.Sprintf(`{"room":"session:999","kind":"user","body":"%s","mentions":["user:other%d"]}`,
				strings.Repeat("z", 200), i)
			if _, err := tx.Exec(`INSERT INTO card_events (card_id, type, actor, payload, created_at)
				VALUES (NULL, 'room_message', ?, ?, datetime('now'))`,
				actor, payload); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("灌无关事件: %v", err)
	}
}

// SQLite 增长断言 + EXPLAIN QUERY PLAN 证据（默认跑；20k 无关事件单事务灌入）。
func TestSessionCandidatesGrowthSQLite(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过 20k 增长夹具")
	}
	s, err := Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	const actor = "u5-perf"
	_, baseRows, baseBytes := candidatePerfFixture(t, s, actor)

	insertUnrelatedEventsSQLite(t, s, actor, 20000)
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM card_events`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	growthRows, growthBytes := mustCandidateStats(t, s)
	t.Logf("B409_U5_SQLITE_GROWTH baseline_rows=%d baseline_bytes=%d growth_rows=%d growth_bytes=%d total_events=%d elapsed_ns=%d",
		baseRows, baseBytes, growthRows, growthBytes, total, time.Since(started).Nanoseconds())
	if growthRows != baseRows || growthBytes != baseBytes {
		t.Fatalf("候选返回随无关事件增长：rows %d→%d bytes %d→%d", baseRows, growthRows, baseBytes, growthBytes)
	}
	if growthRows >= total {
		t.Fatalf("候选返回=%d 不应随全流总量 %d 增长", growthRows, total)
	}

	// 执行计划：两分支分别取证（UNION 顶层只回合并行，分支计划各自可断言）。
	for _, branch := range []string{"mentions", "reply"} {
		bQuery, bArgs := s.sessionCandidatesBranchSQL("user:sy", 0, 0, branch)
		bQuery += " ORDER BY seq ASC LIMIT ?"
		bArgs = append(bArgs, 100)
		plan := explainQueryPlan(t, s, s.q(bQuery), bArgs...)
		t.Logf("B409_U5_SQLITE_EQP branch=%s %s", branch, plan)
		if strings.Contains(plan, "SCAN card_events") {
			t.Fatalf("SQLite %s 分支执行计划出现 card_events 全表扫描：\n%s", branch, plan)
		}
		if !strings.Contains(plan, "SEARCH") {
			t.Fatalf("SQLite %s 分支执行计划未使用索引搜索：\n%s", branch, plan)
		}
	}
}

func explainQueryPlan(t *testing.T, s *Store, query string, args ...any) string {
	t.Helper()
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN `+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id, parent int
		var notUsed, detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}

// PostgreSQL 增长断言 + EXPLAIN (ANALYZE, BUFFERS) 证据（隔离专用库）。
// 夹具与无关事件按 seq 快照清理：进入前记录 MaxSeq，收尾删除其后的全部行
// （专用库内测试串行执行，快照之后只有本测试写入）。
func TestPGSessionCandidatesGrowth(t *testing.T) {
	s := newB409PGStore(t)
	var before int64
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM card_events`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	run := fmt.Sprintf("u5-perf-%d", time.Now().UnixNano())
	actor := run
	c1, baseRows, baseBytes := candidatePerfFixture(t, s, actor)
	t.Cleanup(func() {
		cleanupB409U5Fixture(t, s, run, c1)
		if _, err := s.db.Exec(`DELETE FROM card_events WHERE seq > $1 AND card_id IS NULL`, before); err != nil {
			t.Errorf("清理 PG 无关事件: %v", err)
		}
	})

	// PG：generate_series 单语句灌 20k 无关（镜像 470B / 结构 / 无关房间）。
	if _, err := s.db.Exec(`INSERT INTO card_events (card_id, type, actor, payload, source_target, source_task, source_seq, created_at)
		SELECT NULL, 'task_mirrored', $1,
			jsonb_build_object('node','n','attempt','a','task_type','event','payload',
				jsonb_build_object('i', n, 'body', repeat('x', 400))),
			$1, $1 || ':t-' || n, n, now()
		FROM generate_series(1, 6667) AS series(n)`, actor); err != nil {
		t.Fatalf("灌 PG 镜像无关事件: %v", err)
	}
	if _, err := s.db.Exec(`INSERT INTO card_events (card_id, type, actor, payload, created_at)
		SELECT NULL, 'unrelated_event', $1, jsonb_build_object('i', n, 'body', repeat('y', 300)), now()
		FROM generate_series(1, 6667) AS series(n)`, actor); err != nil {
		t.Fatalf("灌 PG 结构无关事件: %v", err)
	}
	if _, err := s.db.Exec(`INSERT INTO card_events (card_id, type, actor, payload, created_at)
		SELECT NULL, 'room_message', $1,
			jsonb_build_object('room','session:999','kind','user','body', repeat('z', 200),
				'mentions', jsonb_build_array('user:other' || n::text)), now()
		FROM generate_series(1, 6666) AS series(n)`, actor); err != nil {
		t.Fatalf("灌 PG 无关房间消息: %v", err)
	}
	started := time.Now()
	growthRows, growthBytes := mustCandidateStats(t, s)
	t.Logf("B409_U5_PG_GROWTH baseline_rows=%d baseline_bytes=%d growth_rows=%d growth_bytes=%d elapsed_ns=%d",
		baseRows, baseBytes, growthRows, growthBytes, time.Since(started).Nanoseconds())
	if growthRows != baseRows || growthBytes != baseBytes {
		t.Fatalf("PG 候选返回随无关事件增长：rows %d→%d bytes %d→%d", baseRows, growthRows, baseBytes, growthBytes)
	}

	// 执行计划（ANALYZE + BUFFERS，实际参数执行）：两分支分别断言走局部索引。
	for _, branch := range []string{"mentions", "reply"} {
		bQuery, bArgs := s.sessionCandidatesBranchSQL("user:sy", 0, 0, branch)
		bQuery += " ORDER BY seq ASC LIMIT ?"
		bArgs = append(bArgs, 100)
		plan := pgExplain(t, s, s.q(bQuery), bArgs...)
		t.Logf("B409_U5_PG_EXPLAIN branch=%s %s", branch, plan)
		if strings.Contains(plan, "Seq Scan on card_events") {
			t.Fatalf("PostgreSQL %s 分支执行计划出现 card_events 顺序扫描：\n%s", branch, plan)
		}
		if !strings.Contains(plan, "idx_room_messages_cardless_seq") &&
			!strings.Contains(plan, "idx_room_messages_seq") &&
			!strings.Contains(plan, "idx_room_reply_to_seq") &&
			!strings.Contains(plan, "idx_room_messages_actor_seq") &&
			!strings.Contains(plan, "card_events_pkey") {
			t.Fatalf("PostgreSQL %s 分支执行计划未使用候选地址索引：\n%s", branch, plan)
		}
	}
}

func pgExplain(t *testing.T, s *Store, query string, args ...any) string {
	t.Helper()
	rows, err := s.db.Query(`EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) `+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN ANALYZE: %v", err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}

// 收件箱限域读（collab client seam）的增长证据：mentions 候选 / 绑定卡用户
// 消息 / consumed 批量在 +20k 无关事件下 rows/bytes 恒定，且执行计划不含
// card_events 全表 SCAN。
func TestInboxCandidatesGrowthSQLite(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过 20k 增长夹具")
	}
	s, err := Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	_, _, _ = candidatePerfFixture(t, s, "u5-perf-inbox")
	// 消费一条消息，让 consumed 批量读有内容。
	var msgSeq int64
	if err := s.db.QueryRow(`SELECT seq FROM card_events WHERE type = 'room_message' AND payload->>'room' = 'session:1' ORDER BY seq LIMIT 1`).Scan(&msgSeq); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordMessageConsumed("", msgSeq, "user:sy"); err != nil {
		t.Fatal(err)
	}
	inboxStats := func(t *testing.T) (rows, bytes int) {
		t.Helper()
		events, err := s.MentionCandidatesContext(context.Background(), "user:sy", "", true, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range events {
			rows++
			bytes += len(ev.Payload)
		}
		return rows, bytes
	}
	baseRows, baseBytes := inboxStats(t)
	if baseRows != 1 {
		t.Fatalf("群级提及候选应恰 1 条（@user:sy），实得 %d", baseRows)
	}

	insertUnrelatedEventsSQLite(t, s, "u5-perf-inbox", 20000)
	growthRows, growthBytes := inboxStats(t)
	cardMsgs, err := s.CardRoomUserMessagesContext(context.Background(), nil, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	consumed, err := s.ConsumedMessageSeqsContext(context.Background(), "user:sy", 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("B409_U5_SQLITE_INBOX_GROWTH base_rows=%d base_bytes=%d growth_rows=%d growth_bytes=%d card_user_rows=%d consumed_seqs=%d",
		baseRows, baseBytes, growthRows, growthBytes, len(cardMsgs), len(consumed))
	if growthRows != baseRows || growthBytes != baseBytes {
		t.Fatalf("收件箱候选返回随无关事件增长：rows %d→%d bytes %d→%d", baseRows, growthRows, baseBytes, growthBytes)
	}
	if len(consumed) != 1 || consumed[0] != msgSeq {
		t.Fatalf("consumed 批量读漂移：%v want [%d]", consumed, msgSeq)
	}

	// 执行计划：提及候选读（无卡限定）不得全表 SCAN。
	query, args := s.mentionCandidatesSQL("user:sy", "", true, 0)
	query += " ORDER BY seq ASC LIMIT ?"
	args = append(args, 100)
	plan := explainQueryPlan(t, s, s.q(query), args...)
	t.Logf("B409_U5_SQLITE_INBOX_EQP %s", plan)
	if strings.Contains(plan, "SCAN card_events") {
		t.Fatalf("SQLite 收件箱候选执行计划出现全表扫描：\n%s", plan)
	}
}
