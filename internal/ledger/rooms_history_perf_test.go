package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
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
			if _, err := s.db.Exec(`DELETE FROM card_events
				WHERE COALESCE(payload->>'b409_run', payload->'payload'->>'b409_run') = $1`, runID); err != nil {
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
	// body 510：镜像 envelope 与真实 AppendMirroredEvent 对齐（去掉顶层
	// b409_run 键）后，尺寸补偿使基准 mirror payload 保持 ~6.31MB 计划量级。
	insertB409MirrorEvents(t, s, runID, "m1", 9337, 510)

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

	insertB409MirrorEvents(t, s, runID, "m2", 9337, 510)
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
	// 镜像行 envelope 与真实 AppendMirroredEvent 对齐后，run 标签在其内层
	// payload；COALESCE 兼容无关事件的顶层标签。
	if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(octet_length(payload::text)), 0)
		FROM card_events WHERE COALESCE(payload->>'b409_run', payload->'payload'->>'b409_run') = $1`, runID).Scan(&stats.events, &stats.payloadBytes); err != nil {
		t.Fatalf("统计 B409 性能夹具: %v", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(octet_length(payload::text)), 0)
		FROM card_events WHERE COALESCE(payload->>'b409_run', payload->'payload'->>'b409_run') = $1 AND type = $2`, runID, EvTaskMirrored).
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

// insertB409MirrorEvents 批量写入镜像事件。envelope 与真实 AppendMirroredEvent
// 持久格式逐键对齐（顶层恰 node/attempt/task_type/payload 四键，run 标签只在
// 内层 payload；矩阵测试另有真实 writer 样本对照断言钉住这一点）。
func insertB409MirrorEvents(t *testing.T, s *Store, runID, batch string, count, bodyBytes int) {
	t.Helper()
	_, err := s.db.Exec(`INSERT INTO card_events(card_id, type, actor, payload, source_target, source_task, source_seq, created_at)
		SELECT NULL, $4::text, 'b409-perf',
			jsonb_build_object(
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
	// U5 引入 idx_room_messages_cardless_seq 后，优化器在两个等价的
	// room_message 局部索引间按数据分布自行选择；断言放宽为「二者之一且
	// 不退化全表扫」，仍钉住有界读路径。
	usesRoomSeq := strings.Contains(planText, "idx_room_messages_room_seq")
	usesCardlessSeq := strings.Contains(planText, "idx_room_messages_cardless_seq")
	if !usesRoomSeq && !usesCardlessSeq {
		t.Fatalf("PostgreSQL 没有选择房间消息局部索引（room_seq/cardless_seq 之一）: %s", planText)
	}
	if strings.Contains(planText, "Seq Scan on card_events") {
		t.Fatalf("PostgreSQL 房间历史查询退化为全表扫描: %s", planText)
	}
	t.Logf("B409_PG_EXPLAIN %s", planText)
}

// —— B409.7（U7）三阶段增长矩阵 ——
//
// 隔离边界：只经 newB409PGStore（专用 handoff_b409_test 库名守卫）写入；阶段①
// 要求相关范围为空，阶段②③在其上做加法。金样目标经真实生产写路径（ImportCard/
// CreateSession/AddSessionMember/RecordRoomMessage/AppendMirroredEvent）建立，
// 批量种子用 runID 标签的 SQL 批插；镜像批插 envelope 与真实 AppendMirroredEvent
// 持久格式有逐键对照断言钉住。每档以数据库实测计数/字节作证，禁止以配置数字
// 冒充实测。seed-phase 入口供 agentd HTTP/CLI 验收 harness 分阶段复用。
const (
	b409U7BulkMirrors   = 9337  // 阶段①镜像事件（对齐 r2 基准量级）
	b409U7UnrelatedGrow = 20000 // 阶段②无关非镜像事件增量
	b409U7RoomMessages  = 250   // 目标房间消息（首屏 200 的数据源）
)

// b409U7Fixture 矩阵的固定业务目标标识（真实写路径产物 + 确定性派生键）。
type b409U7Fixture struct {
	RunID        string
	Workflow     string
	CardA, CardB string
	Project      string
	SessionMain  string
	SessionEmpty string
	WaitMember   string
	IdleMember   string
	MirrorTarget string
}

// b409U7Stamp 从 runID 提取确定性数字段（runID 形如 b409-u7-<纳秒>）。
func b409U7Stamp(runID string) string {
	idx := strings.LastIndexByte(runID, '-')
	if idx < 0 {
		return runID
	}
	return runID[idx+1:]
}

// deriveB409U7Fixture 由 runID 确定性派生全部夹具标识（三阶段一致；阶段②③
// 据此与库内实际行对账）。
func deriveB409U7Fixture(runID string) *b409U7Fixture {
	stamp := b409U7Stamp(runID)
	return &b409U7Fixture{
		RunID:        runID,
		Workflow:     "u7-matrix-" + stamp,
		CardA:        "B91" + stamp,
		CardB:        "C91" + stamp,
		Project:      "u7-matrix-" + stamp,
		SessionMain:  "", // CreateSession 分配，阶段①落库后经 loadB409U7Fixture 读回
		SessionEmpty: "",
		WaitMember:   "agent:u7-wait-" + stamp,
		IdleMember:   "agent:u7-idle-" + stamp,
		MirrorTarget: "u7-mirror-" + stamp,
	}
}

// seedB409U7Targets 阶段①目标建立：全部经真实生产写路径。返回落库后的夹具
// （含 CreateSession 分配的会话 id）。
func seedB409U7Targets(t *testing.T, s *Store, runID string) *b409U7Fixture {
	t.Helper()
	fx := deriveB409U7Fixture(runID)
	if _, err := s.PutWorkflow(fx.Workflow, WorkflowDef{Nodes: []NodeDef{
		{Name: StatusTodo, Next: StatusDoing},
		{Name: StatusDoing},
	}}); err != nil {
		t.Fatalf("矩阵写工作流: %v", err)
	}
	for _, card := range []struct{ id, title string }{{fx.CardA, "B409 U7 矩阵列卡 A"}, {fx.CardB, "B409 U7 矩阵列卡 B"}} {
		if _, err := s.ImportCard(card.id, "b409-u7-matrix", NewCard{
			Title: card.title, Project: fx.Project, Workflow: fx.Workflow, Actor: "b409-u7",
		}); err != nil {
			t.Fatalf("矩阵建卡 %s: %v", card.id, err)
		}
	}
	sessionMain, err := s.CreateSession("B409 U7 主会话 "+runID, "user:sy", "b409-u7")
	if err != nil {
		t.Fatalf("矩阵建主会话: %v", err)
	}
	sessionEmpty, err := s.CreateSession("B409 U7 空会话 "+runID, "user:sy", "b409-u7")
	if err != nil {
		t.Fatalf("矩阵建空会话: %v", err)
	}
	fx.SessionMain, fx.SessionEmpty = sessionMain.ID, sessionEmpty.ID
	for _, member := range []string{fx.WaitMember, fx.IdleMember} {
		if err := s.AddSessionMember(sessionMain.ID, member, "b409-u7"); err != nil {
			t.Fatalf("矩阵加成员 %s: %v", member, err)
		}
	}
	for i := 1; i <= b409U7RoomMessages; i++ {
		if _, err := s.RecordRoomMessage("", proto.RoomMessage{
			Room: sessionMain.ID, Kind: proto.RoomMsgUser,
			Body: fmt.Sprintf("u7 基准消息 %s #%d", runID, i),
		}, "user:sy"); err != nil {
			t.Fatalf("矩阵写房间消息 %d: %v", i, err)
		}
	}
	// wait 成员的两条定向消息：S3 收件箱/会话等待的固定目标结果；再 @ 一次
	// 控制台身份 user:sy，使 /api/inbox 的 mention 源有真实命中（区别于合法空）。
	for i := 1; i <= 3; i++ {
		mention := fx.WaitMember
		if i == 3 {
			mention = "user:sy"
		}
		if _, err := s.RecordRoomMessage("", proto.RoomMessage{
			Room: sessionMain.ID, Kind: proto.RoomMsgUser,
			Body: fmt.Sprintf("u7 定向消息 %s #%d", runID, i), Mentions: []string{mention},
		}, "user:sy"); err != nil {
			t.Fatalf("矩阵写定向消息 %d: %v", i, err)
		}
	}
	// 工单生命周期：卡A 开→答（终态）→再开 = 1 未决；卡B 开 = 1 未决。
	// 卡B 的 task 名独立于卡A——镜像幂等键是 (target, task, source_seq) 三元组。
	mirror := func(cardID, task string, sourceSeq int64, typ, body string) {
		t.Helper()
		if inserted, err := s.AppendMirroredEvent(cardID, MirroredEvent{Target: fx.MirrorTarget, Task: task,
			SourceSeq: sourceSeq, Type: typ, Payload: []byte(body), CreatedAt: time.Now()}); err != nil || !inserted {
			t.Fatalf("矩阵镜像 %s/%s#%d: inserted=%v err=%v", task, typ, sourceSeq, inserted, err)
		}
	}
	mirror(fx.CardA, "u7-task-lifecycle", 1, evTicketQuestion, `{"ticket_id":"u7-matrix-l1"}`)
	mirror(fx.CardA, "u7-task-lifecycle", 2, evTicketAnswered, `{"ticket_id":"u7-matrix-l1"}`)
	mirror(fx.CardA, "u7-task-open", 1, evTicketQuestion, `{"ticket_id":"u7-matrix-open-a"}`)
	mirror(fx.CardB, "u7-task-open-b", 1, evTicketQuestion, `{"ticket_id":"u7-matrix-open-b"}`)
	return fx
}

// loadB409U7Fixture 阶段②③从库内读回夹具标识（标题/id 均含 runID 派生键）。
func loadB409U7Fixture(t *testing.T, s *Store, runID string) *b409U7Fixture {
	t.Helper()
	fx := deriveB409U7Fixture(runID)
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE title = $1`, "B409 U7 主会话 "+runID).Scan(&fx.SessionMain); err != nil {
		t.Fatalf("读回矩阵主会话: %v", err)
	}
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE title = $1`, "B409 U7 空会话 "+runID).Scan(&fx.SessionEmpty); err != nil {
		t.Fatalf("读回矩阵空会话: %v", err)
	}
	return fx
}

// b409U7StageStats 单阶段数据库实测统计（不以配置数字作证）。
type b409U7StageStats struct {
	TotalEvents        int64
	TypeCounts         map[string]int64
	MirrorRows         int64
	MirrorPayloadBytes int64
	TotalPayloadBytes  int64
}

// collectB409U7StageStats 全库实测：总行数、按 type 分组、精确 task_mirrored
// 行数与 payload bytes（PG JSONB 渲染字节 octet_length(payload::text)）。
func collectB409U7StageStats(t *testing.T, s *Store) b409U7StageStats {
	t.Helper()
	var stats b409U7StageStats
	stats.TypeCounts = map[string]int64{}
	rows, err := s.db.Query(`SELECT type, COUNT(*), COALESCE(SUM(octet_length(payload::text)), 0) FROM card_events GROUP BY type`)
	if err != nil {
		t.Fatalf("按类型统计 card_events: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var typ string
		var n, bytes int64
		if err := rows.Scan(&typ, &n, &bytes); err != nil {
			t.Fatalf("扫描类型统计: %v", err)
		}
		stats.TypeCounts[typ] = n
		stats.TotalEvents += n
		stats.TotalPayloadBytes += bytes
		if typ == EvTaskMirrored {
			stats.MirrorRows = n
			stats.MirrorPayloadBytes = bytes
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历类型统计: %v", err)
	}
	// 禁混淆检查：不得存在 mirror_event 等易混 type 冒充 task_mirrored。
	for _, confusing := range []string{"mirror_event", "task_mirrored_event"} {
		if n := stats.TypeCounts[confusing]; n != 0 {
			t.Fatalf("存在易混类型 %s × %d，禁止混计入镜像分母", confusing, n)
		}
	}
	return stats
}

// logB409U7Stage 输出阶段实测行（台账与 harness 解析此行）。
func logB409U7Stage(t *testing.T, phase string, stats b409U7StageStats) {
	t.Helper()
	types := make([]string, 0, len(stats.TypeCounts))
	for typ, n := range stats.TypeCounts {
		types = append(types, fmt.Sprintf("%s=%d", typ, n))
	}
	sort.Strings(types)
	t.Logf("B409_U7_STAGE phase=%s total_events=%d mirror_rows=%d mirror_payload_bytes=%d total_payload_bytes=%d types=%s",
		phase, stats.TotalEvents, stats.MirrorRows, stats.MirrorPayloadBytes, stats.TotalPayloadBytes, strings.Join(types, ","))
}

// b409U7ProbeReads 限域读探针：全部走真实 Store 读路径，返回 rows/bytes 作
// 「DB 返回规模不随无关事件增长」的断言数据。
type b409U7ProbeReads struct {
	RoomRows       int
	RoomPayload    int
	TicketRows     int
	TicketPayload  int
	CountCards     int
	CandidateRows  int
	CandidateBytes int
}

func b409U7Probes(t *testing.T, s *Store, fx *b409U7Fixture) b409U7ProbeReads {
	t.Helper()
	var probes b409U7ProbeReads
	events, err := s.RoomMessagesBeforeContext(context.Background(), fx.SessionMain, 0, 200)
	if err != nil {
		t.Fatalf("探针读房间历史: %v", err)
	}
	probes.RoomRows = len(events)
	for _, ev := range events {
		probes.RoomPayload += len(ev.Payload)
	}
	tickets, err := s.OpenTickets()
	if err != nil {
		t.Fatalf("探针读未决工单: %v", err)
	}
	probes.TicketRows = len(tickets)
	for _, ticket := range tickets {
		probes.TicketPayload += len(ticket.Payload)
	}
	counts, err := s.OpenTicketCounts()
	if err != nil {
		t.Fatalf("探针读工单计数: %v", err)
	}
	probes.CountCards = len(counts)
	cands, err := s.SessionMessageCandidates(fx.WaitMember, 0, 0, 10000)
	if err != nil {
		t.Fatalf("探针读会话候选: %v", err)
	}
	probes.CandidateRows = len(cands)
	for _, ev := range cands {
		probes.CandidateBytes += len(ev.Payload)
	}
	return probes
}

// assertB409U7ProbesStable 断言限域读在无关/镜像增长前后返回规模不变。
func assertB409U7ProbesStable(t *testing.T, phase string, before, after b409U7ProbeReads) {
	t.Helper()
	if before != after {
		t.Fatalf("%s 限域读规模漂移（不应随无关/镜像事件增长）:\n前=%+v\n后=%+v", phase, before, after)
	}
}

// cleanupB409U7Matrix 按夹具标识清理矩阵数据并重建投影水位。
func cleanupB409U7Matrix(t *testing.T, s *Store, fx *b409U7Fixture) {
	t.Helper()
	cleanup := func(stmt string, args ...any) {
		if _, err := s.db.Exec(stmt, args...); err != nil {
			t.Errorf("矩阵清理 %q: %v", firstLine(stmt), err)
		}
	}
	cleanup(`DELETE FROM card_events WHERE COALESCE(payload->>'b409_run', payload->'payload'->>'b409_run') = $1`, fx.RunID)
	cleanup(`DELETE FROM card_events WHERE payload->>'room' IN ($1, $2)`, fx.SessionMain, fx.SessionEmpty)
	cleanup(`DELETE FROM card_events WHERE card_id IN ($1, $2)`, fx.CardA, fx.CardB)
	cleanup(`DELETE FROM card_events WHERE type = 'message_consumed' AND actor IN ($1, $2)`, fx.WaitMember, fx.IdleMember)
	cleanup(`DELETE FROM card_events WHERE type = 'session_created' AND payload->>'id' IN ($1, $2)`, fx.SessionMain, fx.SessionEmpty)
	cleanup(`DELETE FROM session_delivery_cursors WHERE member IN ($1, $2)`, fx.WaitMember, fx.IdleMember)
	cleanup(`DELETE FROM sessions WHERE id IN ($1, $2)`, fx.SessionMain, fx.SessionEmpty)
	cleanup(`DELETE FROM open_ticket_projection WHERE card_id IN ($1, $2)`, fx.CardA, fx.CardB)
	cleanup(`DELETE FROM cards WHERE id IN ($1, $2)`, fx.CardA, fx.CardB)
	cleanup(`DELETE FROM workflows WHERE name = $1`, fx.Workflow)
	if err := s.rebuildOpenTicketProjection(); err != nil {
		t.Errorf("矩阵清理后重建投影水位: %v", err)
	}
}

// assertB409U7EnvelopeParity 用真实 AppendMirroredEvent 写一条样本，对照批量
// 种子行的顶层键集与包裹形态（PG JSONB 归一化后逐键比对，不做字节比较）。
func assertB409U7EnvelopeParity(t *testing.T, s *Store, fx *b409U7Fixture, runID string) {
	t.Helper()
	realPayload := []byte(fmt.Sprintf(`{"b409_run":%q,"batch":"envelope-probe"}`, runID))
	inserted, err := s.AppendMirroredEvent(fx.CardA, MirroredEvent{Target: fx.MirrorTarget, Task: "u7-envelope-probe",
		SourceSeq: 1, Type: "message", Payload: realPayload, CreatedAt: time.Now()})
	if err != nil || !inserted {
		t.Fatalf("写真实 writer 对照样本: inserted=%v err=%v", inserted, err)
	}
	scanPayload := func(where string, args ...any) string {
		t.Helper()
		var raw string
		if err := s.db.QueryRow(`SELECT payload::text FROM card_events WHERE `+where, args...).Scan(&raw); err != nil {
			t.Fatalf("读对照样本 payload: %v", err)
		}
		return raw
	}
	real := scanPayload(`source_target = $1 AND source_task = 'u7-envelope-probe' AND source_seq = 1`, fx.MirrorTarget)
	var bulkRow string
	if err := s.db.QueryRow(`SELECT payload::text FROM card_events
		WHERE payload->'payload'->>'b409_run' = $1 AND type = $2 AND source_task <> 'u7-envelope-probe' LIMIT 1`, runID, EvTaskMirrored).Scan(&bulkRow); err != nil {
		t.Fatalf("读批量镜像样本: %v", err)
	}
	topKeys := func(raw string) map[string]json.RawMessage {
		t.Helper()
		m := map[string]json.RawMessage{}
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("解镜像 envelope: %v", err)
		}
		return m
	}
	realKeys, bulkKeys := topKeys(real), topKeys(bulkRow)
	want := []string{"attempt", "node", "payload", "task_type"}
	if len(realKeys) != len(want) || len(bulkKeys) != len(want) {
		t.Fatalf("镜像 envelope 顶层键数不符: real=%v bulk=%v", realKeys, bulkKeys)
	}
	for _, key := range want {
		if _, ok := realKeys[key]; !ok {
			t.Fatalf("真实 writer envelope 缺键 %q: %s", key, real)
		}
		if _, ok := bulkKeys[key]; !ok {
			t.Fatalf("批量种子 envelope 缺键 %q（与真实 AppendMirroredEvent 格式不等同）: %s", key, bulkRow)
		}
	}
	var bulkTaskType, realTaskType string
	_ = json.Unmarshal(bulkKeys["task_type"], &bulkTaskType)
	_ = json.Unmarshal(realKeys["task_type"], &realTaskType)
	if bulkTaskType == "" || realTaskType == "" {
		t.Fatalf("task_type 应为字符串: real=%q bulk=%q", realTaskType, bulkTaskType)
	}
	t.Logf("B409_U7_ENVELOPE real_top_keys=%v bulk_top_keys=%v", want, want)
}

// assertB409U7EmptyScope 阶段①前置：矩阵要求专用库相关范围为空。
func assertB409U7EmptyScope(t *testing.T, s *Store) {
	t.Helper()
	for _, check := range []struct{ table string }{{"card_events"}, {"sessions"}, {"cards"}, {"session_delivery_cursors"}} {
		var n int64
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + check.table).Scan(&n); err != nil {
			t.Fatalf("检查隔离库 %s: %v", check.table, err)
		}
		if n != 0 {
			t.Fatalf("矩阵要求 %s 开始为空，当前 %d 行（请先重置专用可丢弃库）", check.table, n)
		}
	}
}

// TestB409U7PGFinalGrowthMatrix 步骤 4 主测试：三阶段隔离 PG 增长矩阵。
// 阶段①当前量级（21,270 量级 / 9,337 镜像）；②+20,000 无关非镜像；③镜像
// 行数与 payload bytes 各自至少翻倍。每阶段数据库实测统计 + 限域读探针 +
// 房间历史 100 样本时延（沿 Wave 0 断言）。LEDGER_TEST_PG_PERF=1 启用。
func TestB409U7PGFinalGrowthMatrix(t *testing.T) {
	if os.Getenv("LEDGER_TEST_PG_PERF") != "1" {
		t.Skip("set LEDGER_TEST_PG_PERF=1 to run the B409 U7 final growth matrix on isolated PostgreSQL")
	}
	s := newB409PGStore(t)
	assertB409U7EmptyScope(t, s)
	runID := os.Getenv("LEDGER_TEST_PG_PERF_RUN_ID")
	if runID == "" {
		runID = fmt.Sprintf("b409-u7-%d", time.Now().UnixNano())
	}
	keep := os.Getenv("LEDGER_TEST_PG_PERF_KEEP") == "1"
	fx := seedB409U7Targets(t, s, runID)
	if keep {
		t.Logf("B409_U7_FIXTURE run_id=%s card_a=%s card_b=%s session_main=%s session_empty=%s wait_member=%s idle_member=%s mirror_target=%s project=%s room_messages=%d",
			fx.RunID, fx.CardA, fx.CardB, fx.SessionMain, fx.SessionEmpty, fx.WaitMember, fx.IdleMember, fx.MirrorTarget, fx.Project, b409U7RoomMessages)
	} else {
		t.Cleanup(func() { cleanupB409U7Matrix(t, s, fx) })
	}

	// 阶段①：批量无关 + 批量镜像（envelope 对照真实 writer）。
	seeded := collectB409U7StageStats(t, s)
	base := b409U7UnrelatedBaseCount(seeded.TotalEvents)
	insertB409UnrelatedEvents(t, s, runID, "base", base)
	insertB409MirrorEvents(t, s, runID, "m1", b409U7BulkMirrors, 600)
	assertB409U7EnvelopeParity(t, s, fx, runID)
	stage1 := collectB409U7StageStats(t, s)
	logB409U7Stage(t, "1-baseline", stage1)
	probe1 := b409U7Probes(t, s, fx)
	measureB409RoomHistory(t, s, "u7-stage-1-baseline", fx.SessionMain, b409U7StatsForMeasure(stage1))

	// 阶段②：+20,000 无关非镜像事件；目标结果集与镜像量不变。
	insertB409UnrelatedEvents(t, s, runID, "growth-20k", b409U7UnrelatedGrow)
	stage2 := collectB409U7StageStats(t, s)
	logB409U7Stage(t, "2-plus-unrelated", stage2)
	if stage2.MirrorRows != stage1.MirrorRows || stage2.MirrorPayloadBytes != stage1.MirrorPayloadBytes {
		t.Fatalf("阶段②镜像量不得变化: rows %d→%d bytes %d→%d",
			stage1.MirrorRows, stage2.MirrorRows, stage1.MirrorPayloadBytes, stage2.MirrorPayloadBytes)
	}
	if want := stage1.TotalEvents + b409U7UnrelatedGrow; stage2.TotalEvents != want {
		t.Fatalf("阶段②总事件 = %d，期望 %d", stage2.TotalEvents, want)
	}
	if stage2.TypeCounts["unrelated_event"] != stage1.TypeCounts["unrelated_event"]+b409U7UnrelatedGrow {
		t.Fatalf("阶段②无关事件计数不符: %d→%d", stage1.TypeCounts["unrelated_event"], stage2.TypeCounts["unrelated_event"])
	}
	probe2 := b409U7Probes(t, s, fx)
	assertB409U7ProbesStable(t, "阶段②", probe1, probe2)
	measureB409RoomHistory(t, s, "u7-stage-2-plus-unrelated", fx.SessionMain, b409U7StatsForMeasure(stage2))

	// 阶段③：镜像行数与 payload bytes 各自至少翻倍（对阶段①分母）。金样
	// 镜像也计入分母，翻倍增量须把这部分一并补上。
	goldenMirrors := stage1.MirrorRows - b409U7BulkMirrors
	insertB409MirrorEvents(t, s, runID, "m2", b409U7BulkMirrors+int(goldenMirrors), 600)
	stage3 := collectB409U7StageStats(t, s)
	logB409U7Stage(t, "3-doubled-mirrors", stage3)
	if stage3.MirrorRows < 2*stage1.MirrorRows {
		t.Fatalf("阶段③镜像行数 %d 未达阶段① %d 的两倍", stage3.MirrorRows, stage1.MirrorRows)
	}
	if stage3.MirrorPayloadBytes < 2*stage1.MirrorPayloadBytes {
		t.Fatalf("阶段③镜像 payload bytes %d 未达阶段① %d 的两倍", stage3.MirrorPayloadBytes, stage1.MirrorPayloadBytes)
	}
	probe3 := b409U7Probes(t, s, fx)
	assertB409U7ProbesStable(t, "阶段③", probe1, probe3)
	measureB409RoomHistory(t, s, "u7-stage-3-doubled-mirrors", fx.SessionMain, b409U7StatsForMeasure(stage3))
	t.Logf("B409_U7_PROBES stage1=%+v stage2=%+v stage3=%+v", probe1, probe2, probe3)
}

// b409U7UnrelatedBaseCount 按实测金样事件数推导阶段①批量无关事件数，使总
// 规模落在 21,270 基准量级。
func b409U7UnrelatedBaseCount(goldenEvents int64) int {
	base := 21270 - int(goldenEvents) - b409U7RoomMessages - b409U7BulkMirrors
	if base < 0 {
		panic(fmt.Sprintf("金样事件数 %d 超出 21,270 基准预算，矩阵常量需复核", goldenEvents))
	}
	return base
}

// b409U7StatsForMeasure 把阶段实测统计转换为 Wave 0 房间历史测量入参。
func b409U7StatsForMeasure(stats b409U7StageStats) b409PerfStats {
	return b409PerfStats{
		events:             int(stats.TotalEvents),
		payloadBytes:       stats.TotalPayloadBytes,
		mirrorRows:         int(stats.MirrorRows),
		mirrorPayloadBytes: stats.MirrorPayloadBytes,
	}
}

// TestB409U7PGSeedPhase 是 HTTP/CLI 验收 harness 的分阶段 seed 入口（同一
// 隔离库上增量推进，agentd 无需重启即可测到各档）：
//
//	LEDGER_TEST_PG_SEED_PHASE=1  从空库建阶段①（要求相关范围为空）
//	LEDGER_TEST_PG_SEED_PHASE=2  校验阶段①状态后 +20,000 无关非镜像事件
//	LEDGER_TEST_PG_SEED_PHASE=3  校验阶段②状态后镜像翻倍（行数与 bytes 各自≥2×）
//
// 每次运行都输出 B409_U7_STAGE（实测统计）与 B409_U7_FIXTURE（夹具标识供
// harness 定位）。LEDGER_TEST_PG_PERF_RUN_ID 必须三阶段一致。
func TestB409U7PGSeedPhase(t *testing.T) {
	if os.Getenv("LEDGER_TEST_PG_PERF") != "1" {
		t.Skip("set LEDGER_TEST_PG_PERF=1 to seed the B409 U7 acceptance matrix")
	}
	phase := os.Getenv("LEDGER_TEST_PG_SEED_PHASE")
	switch phase {
	case "1", "2", "3":
	default:
		t.Fatalf("LEDGER_TEST_PG_SEED_PHASE 必须为 1/2/3，当前 %q", phase)
	}
	runID := os.Getenv("LEDGER_TEST_PG_PERF_RUN_ID")
	if runID == "" {
		t.Fatal("分阶段 seed 必须显式设置 LEDGER_TEST_PG_PERF_RUN_ID（三阶段一致）")
	}
	s := newB409PGStore(t)
	var existing int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM card_events`).Scan(&existing); err != nil {
		t.Fatalf("读隔离库事件数: %v", err)
	}
	if phase == "1" {
		assertB409U7EmptyScope(t, s)
		fx := seedB409U7Targets(t, s, runID)
		seeded := collectB409U7StageStats(t, s)
		base := b409U7UnrelatedBaseCount(seeded.TotalEvents)
		insertB409UnrelatedEvents(t, s, runID, "base", base)
		insertB409MirrorEvents(t, s, runID, "m1", b409U7BulkMirrors, 600)
		assertB409U7EnvelopeParity(t, s, fx, runID)
		logB409U7Stage(t, "1-baseline", collectB409U7StageStats(t, s))
		t.Logf("B409_U7_FIXTURE run_id=%s card_a=%s card_b=%s session_main=%s session_empty=%s wait_member=%s idle_member=%s mirror_target=%s project=%s room_messages=%d",
			fx.RunID, fx.CardA, fx.CardB, fx.SessionMain, fx.SessionEmpty, fx.WaitMember, fx.IdleMember, fx.MirrorTarget, fx.Project, b409U7RoomMessages)
		return
	}
	if existing == 0 {
		t.Fatalf("阶段 %s 要求库内已有前一阶段夹具，当前 card_events 为空", phase)
	}
	fx := loadB409U7Fixture(t, s, runID)
	if phase == "2" {
		stage1 := collectB409U7StageStats(t, s)
		insertB409UnrelatedEvents(t, s, runID, "growth-20k", b409U7UnrelatedGrow)
		stage2 := collectB409U7StageStats(t, s)
		if stage2.MirrorRows != stage1.MirrorRows || stage2.MirrorPayloadBytes != stage1.MirrorPayloadBytes {
			t.Fatalf("阶段②镜像量不得变化: rows %d→%d bytes %d→%d",
				stage1.MirrorRows, stage2.MirrorRows, stage1.MirrorPayloadBytes, stage2.MirrorPayloadBytes)
		}
		if want := stage1.TotalEvents + b409U7UnrelatedGrow; stage2.TotalEvents != want {
			t.Fatalf("阶段②总事件 = %d，期望 %d", stage2.TotalEvents, want)
		}
		logB409U7Stage(t, "2-plus-unrelated", stage2)
	} else {
		stage2 := collectB409U7StageStats(t, s)
		goldenMirrors := stage2.MirrorRows - b409U7BulkMirrors
		insertB409MirrorEvents(t, s, runID, "m2", b409U7BulkMirrors+int(goldenMirrors), 600)
		stage3 := collectB409U7StageStats(t, s)
		if stage3.MirrorRows < 2*stage2.MirrorRows {
			t.Fatalf("阶段③镜像行数 %d 未达阶段② %d 的两倍", stage3.MirrorRows, stage2.MirrorRows)
		}
		if stage3.MirrorPayloadBytes < 2*stage2.MirrorPayloadBytes {
			t.Fatalf("阶段③镜像 bytes %d 未达阶段② %d 的两倍", stage3.MirrorPayloadBytes, stage2.MirrorPayloadBytes)
		}
		logB409U7Stage(t, "3-doubled-mirrors", stage3)
	}
	// 夹具标识与阶段①一致（确定性派生 + 库内核对）。
	t.Logf("B409_U7_FIXTURE run_id=%s card_a=%s card_b=%s session_main=%s session_empty=%s wait_member=%s idle_member=%s mirror_target=%s project=%s room_messages=%d",
		fx.RunID, fx.CardA, fx.CardB, fx.SessionMain, fx.SessionEmpty, fx.WaitMember, fx.IdleMember, fx.MirrorTarget, fx.Project, b409U7RoomMessages)
}
