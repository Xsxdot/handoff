package ledger

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestRoomHistoryPostgresPerformance is an opt-in local acceptance harness for B409 Wave 0.
// It refuses non-dedicated databases and deletes only rows tagged with its unique run id.
func TestRoomHistoryPostgresPerformance(t *testing.T) {
	if os.Getenv("LEDGER_TEST_PG_PERF") != "1" {
		t.Skip("set LEDGER_TEST_PG_PERF=1 to run isolated PostgreSQL history performance acceptance")
	}
	s := newB409PGStore(t)
	var existing int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM card_events`).Scan(&existing); err != nil {
		t.Fatalf("检查隔离性能库是否为空: %v", err)
	}
	if existing != 0 {
		t.Fatalf("性能测试库要求开始时没有账本事件，当前有 %d 行", existing)
	}
	var existingSessions int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&existingSessions); err != nil {
		t.Fatalf("检查隔离性能库会话表: %v", err)
	}
	if existingSessions != 0 {
		t.Fatalf("性能测试库要求开始时没有会话，当前有 %d 行", existingSessions)
	}

	runID := os.Getenv("LEDGER_TEST_PG_PERF_RUN_ID")
	if runID == "" {
		runID = fmt.Sprintf("b409-wave0-%d", time.Now().UnixNano())
	}
	const roomID = "session:1"
	if os.Getenv("LEDGER_TEST_PG_PERF_KEEP") == "1" {
		t.Logf("B409_PG_PERF_FIXTURE run_id=%s room_id=%s", runID, roomID)
	} else {
		t.Cleanup(func() {
			if _, err := s.db.Exec(`DELETE FROM card_events WHERE payload->>'b409_run' = $1`, runID); err != nil {
				t.Errorf("清理 B409 性能夹具: %v", err)
			}
			if _, err := s.db.Exec(`DELETE FROM sessions WHERE id = $1`, roomID); err != nil {
				t.Errorf("清理 B409 性能会话夹具: %v", err)
			}
		})
	}
	if _, err := s.db.Exec(`INSERT INTO sessions(id, title, owner, archived, members, created_at, updated_at)
		VALUES ($1, 'B409 性能验收会话', 'user:sy', false, jsonb_build_array('user:sy'), now(), now())`, roomID); err != nil {
		t.Fatalf("建立隔离 UI 会话夹具: %v", err)
	}
	insertB409RoomMessages(t, s, runID, roomID, 250)
	insertB409UnrelatedEvents(t, s, runID, "base", 11683)
	insertB409MirrorEvents(t, s, runID, "m1", 9337, 470)

	baseline := b409PayloadStats(t, s, runID)
	if baseline.events != 21270 || baseline.mirrorRows != 9337 ||
		math.Abs(float64(baseline.mirrorPayloadBytes-6310000)) > 250000 {
		t.Fatalf("基准数据未对齐计划规模: %+v，期望 21270 events / 9337 mirror rows / mirror payload 约 6.31 MB", baseline)
	}
	measureB409RoomHistory(t, s, "baseline", roomID, baseline)
	assertB409RoomIndex(t, s, roomID)

	insertB409UnrelatedEvents(t, s, runID, "growth-20k", 20000)
	withUnrelated := b409PayloadStats(t, s, runID)
	if withUnrelated.events != 41270 {
		t.Fatalf("增加 20k 无关事件后的总量 = %d，期望 41270", withUnrelated.events)
	}
	measureB409RoomHistory(t, s, "plus-20k-unrelated", roomID, withUnrelated)

	insertB409MirrorEvents(t, s, runID, "m2", 9337, 470)
	doubledMirrors := b409PayloadStats(t, s, runID)
	if doubledMirrors.events != 50607 || doubledMirrors.mirrorRows != 18674 ||
		math.Abs(float64(doubledMirrors.mirrorPayloadBytes-2*baseline.mirrorPayloadBytes)) > float64(baseline.mirrorPayloadBytes)/100 {
		t.Fatalf("镜像行与 payload 翻倍后的规模不符: baseline=%+v doubled=%+v", baseline, doubledMirrors)
	}
	measureB409RoomHistory(t, s, "plus-doubled-mirrors", roomID, doubledMirrors)
}

type b409PerfStats struct {
	events             int
	payloadBytes       int64
	mirrorRows         int
	mirrorPayloadBytes int64
}

func b409PayloadStats(t *testing.T, s *Store, runID string) b409PerfStats {
	t.Helper()
	var stats b409PerfStats
	if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(octet_length(payload::text)), 0)
		FROM card_events WHERE payload->>'b409_run' = $1`, runID).Scan(&stats.events, &stats.payloadBytes); err != nil {
		t.Fatalf("统计 B409 性能夹具: %v", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(octet_length(payload::text)), 0)
		FROM card_events WHERE payload->>'b409_run' = $1 AND type = $2`, runID, EvTaskMirrored).
		Scan(&stats.mirrorRows, &stats.mirrorPayloadBytes); err != nil {
		t.Fatalf("统计 B409 镜像夹具: %v", err)
	}
	return stats
}

func insertB409RoomMessages(t *testing.T, s *Store, runID, roomID string, count int) {
	t.Helper()
	_, err := s.db.Exec(`INSERT INTO card_events(card_id, type, actor, payload, created_at)
		SELECT NULL, 'room_message', 'b409-perf',
			jsonb_build_object('b409_run', $1::text, 'room', $2::text, 'kind', 'user', 'body', repeat('x', 200), 'i', n), now()
		FROM generate_series(1, $3) AS series(n)`, runID, roomID, count)
	if err != nil {
		t.Fatalf("写入 B409 目标房间消息: %v", err)
	}
}

func insertB409UnrelatedEvents(t *testing.T, s *Store, runID, batch string, count int) {
	t.Helper()
	_, err := s.db.Exec(`INSERT INTO card_events(card_id, type, actor, payload, created_at)
		SELECT NULL, 'unrelated_event', 'b409-perf',
			jsonb_build_object('b409_run', $1::text, 'batch', $2::text, 'i', n), now()
		FROM generate_series(1, $3) AS series(n)`, runID, batch, count)
	if err != nil {
		t.Fatalf("写入 B409 无关事件: %v", err)
	}
}

func insertB409MirrorEvents(t *testing.T, s *Store, runID, batch string, count, bodyBytes int) {
	t.Helper()
	_, err := s.db.Exec(`INSERT INTO card_events(card_id, type, actor, payload, source_target, source_task, source_seq, created_at)
		SELECT NULL, $4::text, 'b409-perf',
			jsonb_build_object(
				'b409_run', $1::text,
				'node', 'b409-node', 'attempt', 'b409-attempt', 'task_type', 'event',
				'payload', jsonb_build_object('b409_run', $1::text, 'batch', $2::text, 'i', n,
					'body', repeat('x', $5))),
			'b409-perf', $1::text || ':' || $2::text, n, now()
		FROM generate_series(1, $3) AS series(n)`, runID, batch, count, EvTaskMirrored, bodyBytes)
	if err != nil {
		t.Fatalf("写入 B409 镜像事件: %v", err)
	}
}

func measureB409RoomHistory(t *testing.T, s *Store, phase, roomID string, stats b409PerfStats) {
	t.Helper()
	firstStarted := time.Now()
	firstEvents, err := s.RoomMessagesBeforeContext(t.Context(), roomID, 0, 200)
	firstElapsed := time.Since(firstStarted)
	firstBytes := roomHistoryPayloadBytes(firstEvents)
	if err != nil || len(firstEvents) != 200 {
		t.Fatalf("%s 首次读取: messages=%d err=%v", phase, len(firstEvents), err)
	}
	t.Logf("B409_PG_FIRST phase=%s target_messages=%d payload_bytes=%d elapsed_ns=%d errors=0",
		phase, len(firstEvents), firstBytes, firstElapsed.Nanoseconds())
	for i := 0; i < 5; i++ {
		if events, err := s.RoomMessagesBeforeContext(t.Context(), roomID, 0, 200); err != nil || len(events) != 200 {
			t.Fatalf("%s 预热 %d: messages=%d err=%v", phase, i, len(events), err)
		}
	}
	samples := make([]int64, 0, 100)
	for i := 0; i < 100; i++ {
		started := time.Now()
		events, err := s.RoomMessagesBeforeContext(t.Context(), roomID, 0, 200)
		samples = append(samples, time.Since(started).Nanoseconds())
		if err != nil || len(events) != 200 {
			t.Fatalf("%s 样本 %d: messages=%d err=%v", phase, i, len(events), err)
		}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	if samples[94] > int64(2*time.Second) {
		t.Fatalf("%s p95 超过 2 秒: %d ns", phase, samples[94])
	}
	t.Logf("B409_PG_PERF phase=%s events=%d payload_bytes=%d mirror_rows=%d mirror_payload_bytes=%d target_messages=200 errors=0 p50_ns=%d p95_ns=%d max_ns=%d samples_ns=%v",
		phase, stats.events, stats.payloadBytes, stats.mirrorRows, stats.mirrorPayloadBytes,
		samples[49], samples[94], samples[99], samples)
}

func roomHistoryPayloadBytes(events []Event) int {
	total := 0
	for _, event := range events {
		total += len(event.Payload)
	}
	return total
}

func assertB409RoomIndex(t *testing.T, s *Store, roomID string) {
	t.Helper()
	rows, err := s.db.Query(`EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)
		SELECT seq FROM card_events
		WHERE type = 'room_message' AND card_id IS NULL AND payload->>'room' = $1
		ORDER BY seq DESC LIMIT 200`, roomID)
	if err != nil {
		t.Fatalf("读取 PostgreSQL 房间历史查询计划: %v", err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("扫描 PostgreSQL 查询计划: %v", err)
		}
		plan = append(plan, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历 PostgreSQL 查询计划: %v", err)
	}
	planText := fmt.Sprint(plan)
	if !strings.Contains(planText, "idx_room_messages_room_seq") {
		t.Fatalf("PostgreSQL 没有选择房间表达式索引: %s", planText)
	}
	t.Logf("B409_PG_EXPLAIN %s", planText)
}
