package ledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

type ticketOracleKey struct {
	cardID, target, taskID, ticketID string
}

// replayOpenTicketsOracle 独立从权威 card_events 重放工单状态，不调用生产投影代码。
func replayOpenTicketsOracle(t *testing.T, s *Store) []OpenTicket {
	t.Helper()
	rows, err := s.db.Query(s.q(`SELECT card_id, source_target, source_task, payload
		FROM card_events WHERE type = ? AND source_target IS NOT NULL AND source_task IS NOT NULL ORDER BY seq ASC`), EvTaskMirrored)
	if err != nil {
		t.Fatalf("读取 oracle 镜像事件: %v", err)
	}
	defer rows.Close()
	open := map[ticketOracleKey]OpenTicket{}
	for rows.Next() {
		var cardID, target, taskID, raw string
		if err := rows.Scan(&cardID, &target, &taskID, &raw); err != nil {
			t.Fatalf("扫描 oracle 镜像事件: %v", err)
		}
		var event mirroredTaskPayload
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			t.Fatalf("解码 oracle 镜像事件: %v", err)
		}
		key := func(ticketID string) ticketOracleKey {
			return ticketOracleKey{cardID: cardID, target: target, taskID: taskID, ticketID: ticketID}
		}
		switch event.TaskType {
		case evTicketCreated, evTicketQuestion:
			var payload ticketPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("解码 oracle 创建事件: %v", err)
			}
			if payload.TicketID != "" {
				open[key(payload.TicketID)] = OpenTicket{CardID: cardID, Target: target, TaskID: taskID,
					TicketID: payload.TicketID, TaskType: event.TaskType, Payload: append(json.RawMessage(nil), event.Payload...)}
			}
		case evTicketAnswered:
			var payload ticketPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("解码 oracle 答复事件: %v", err)
			}
			delete(open, key(payload.TicketID))
		case evTicketsVoided, "completed", "failed", "archived":
			for candidate := range open {
				if candidate.cardID == cardID && candidate.target == target && candidate.taskID == taskID {
					delete(open, candidate)
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("读取 oracle 镜像事件结束: %v", err)
	}
	out := make([]OpenTicket, 0, len(open))
	for _, ticket := range open {
		out = append(out, ticket)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CardID != out[j].CardID {
			return out[i].CardID < out[j].CardID
		}
		return out[i].TicketID < out[j].TicketID
	})
	return out
}

func TestOpenTicketProjectionMatchesCanonicalReplay(t *testing.T) {
	s := seedStore(t)
	cardA := mk(t, s, "投影 A")
	cardB := mk(t, s, "投影 B")
	sequences := map[string]int64{}
	appendMirror := func(cardID, target, taskID, typ, raw string) (bool, int64) {
		t.Helper()
		key := target + "/" + taskID
		sequences[key]++
		seq := sequences[key]
		inserted, err := s.AppendMirroredEvent(cardID, MirroredEvent{Target: target, Task: taskID,
			SourceSeq: seq, Type: typ, Payload: []byte(raw), CreatedAt: time.Now()})
		if err != nil {
			t.Fatalf("追加 %s@%s 的 %s 镜像: %v", taskID, target, typ, err)
		}
		return inserted, seq
	}

	createdRaw := "{ \"ticket_id\":\"same-id\", \"question\":\"keep spacing\" }"
	inserted, seq := appendMirror(cardA.ID, "mac-01", "task-1", evTicketQuestion, createdRaw)
	if !inserted {
		t.Fatal("首次镜像应插入")
	}
	// 同一来源事件重放不得重复影响投影。
	inserted, err := s.AppendMirroredEvent(cardA.ID, MirroredEvent{Target: "mac-01", Task: "task-1",
		SourceSeq: seq, Type: evTicketQuestion, Payload: []byte(createdRaw), CreatedAt: time.Now()})
	if err != nil || inserted {
		t.Fatalf("重复镜像应幂等跳过: inserted=%v err=%v", inserted, err)
	}
	appendMirror(cardA.ID, "mac-01", "task-1", evTicketCreated, `{"ticket_id":"answered"}`)
	appendMirror(cardB.ID, "mac-01", "task-1", evTicketQuestion, `{"ticket_id":"same-id"}`)
	appendMirror(cardA.ID, "mac-02", "task-1", evTicketQuestion, `{"ticket_id":"same-id"}`)
	appendMirror(cardA.ID, "mac-01", "task-2", evTicketQuestion, `{"ticket_id":"same-id"}`)
	appendMirror(cardA.ID, "mac-01", "task-1", evTicketAnswered, `{"ticket_id":"answered"}`)
	appendMirror(cardB.ID, "mac-01", "task-1", "failed", `{}`)
	appendMirror(cardA.ID, "mac-01", "task-1", evTicketsVoided, `{}`)
	appendMirror(cardA.ID, "mac-01", "task-1", evTicketQuestion, `{"ticket_id":"late-open"}`)
	appendMirror(cardA.ID, "mac-01", "task-3", evTicketQuestion, createdRaw)

	want := replayOpenTicketsOracle(t, s)
	previousLogger := slog.Default()
	logBuffer := new(bytes.Buffer)
	slog.SetDefault(slog.New(slog.NewJSONHandler(logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previousLogger)
	got, err := s.OpenTickets()
	if err != nil {
		t.Fatalf("OpenTickets: %v", err)
	}
	canonicalOrder := func(tickets []OpenTicket) {
		sort.Slice(tickets, func(i, j int) bool {
			if tickets[i].CardID != tickets[j].CardID {
				return tickets[i].CardID < tickets[j].CardID
			}
			if tickets[i].TicketID != tickets[j].TicketID {
				return tickets[i].TicketID < tickets[j].TicketID
			}
			if tickets[i].Target != tickets[j].Target {
				return tickets[i].Target < tickets[j].Target
			}
			return tickets[i].TaskID < tickets[j].TaskID
		})
	}
	canonicalOrder(got)
	canonicalOrder(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OpenTickets 与 card_events oracle 不一致\n got: %#v\nwant: %#v", got, want)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].CardID > got[i].CardID || (got[i-1].CardID == got[i].CardID && got[i-1].TicketID > got[i].TicketID) {
			t.Fatalf("OpenTickets 未按卡号和 ticket ID 排序: %+v", got)
		}
	}
	wantCounts := map[string]int{}
	for _, ticket := range want {
		wantCounts[ticket.CardID]++
	}
	gotCounts, err := s.OpenTicketCounts()
	if err != nil {
		t.Fatalf("OpenTicketCounts: %v", err)
	}
	if !reflect.DeepEqual(gotCounts, wantCounts) {
		t.Fatalf("OpenTicketCounts 与 oracle 不一致: got=%v want=%v", gotCounts, wantCounts)
	}
	logLines := bytes.Split(bytes.TrimSpace(logBuffer.Bytes()), []byte{'\n'})
	if len(logLines) != 2 {
		t.Fatalf("明细与计数读应各记录一次搬运观测，日志=%s", logBuffer.String())
	}
	var detailObservation, countObservation struct {
		Message      string `json:"msg"`
		RowsRead     int64  `json:"rows_read"`
		PayloadBytes int64  `json:"payload_bytes"`
	}
	if err := json.Unmarshal(logLines[0], &detailObservation); err != nil {
		t.Fatalf("解码明细读观测: %v", err)
	}
	if err := json.Unmarshal(logLines[1], &countObservation); err != nil {
		t.Fatalf("解码计数读观测: %v", err)
	}
	wantPayloadBytes := int64(0)
	for _, ticket := range want {
		wantPayloadBytes += int64(len(ticket.Payload))
	}
	if detailObservation.RowsRead != int64(len(want)) || detailObservation.PayloadBytes != wantPayloadBytes {
		t.Fatalf("明细热读传输边界 rows=%d/%d payload_bytes=%d/%d",
			detailObservation.RowsRead, len(want), detailObservation.PayloadBytes, wantPayloadBytes)
	}
	if countObservation.RowsRead != 1 || countObservation.PayloadBytes != 0 {
		t.Fatalf("计数热读必须只返回聚合行且不带正文: rows=%d payload_bytes=%d",
			countObservation.RowsRead, countObservation.PayloadBytes)
	}

	// 热读使用的投影只保留当前未决正文，且只返回有限的有效票据。
	var rows, payloadBytes int
	if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(length(payload)), 0) FROM open_ticket_projection`).Scan(&rows, &payloadBytes); err != nil {
		t.Fatalf("读取未决工单投影统计: %v", err)
	}
	wantPayloadBytesInt := 0
	for _, ticket := range want {
		wantPayloadBytesInt += len(ticket.Payload)
	}
	if rows != len(want) || payloadBytes != wantPayloadBytesInt {
		t.Fatalf("投影只应保留未决工单: rows=%d/%d payload_bytes=%d/%d", rows, len(want), payloadBytes, wantPayloadBytesInt)
	}
	var stateRows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM open_ticket_projection_state WHERE id = 1`).Scan(&stateRows); err != nil {
		t.Fatalf("读取投影水位: %v", err)
	}
	if stateRows != 1 {
		t.Fatalf("应有唯一投影水位记录，实得 %d", stateRows)
	}
	preserved := false
	for _, ticket := range want {
		if ticket.TaskID == "task-3" && ticket.TicketID == "same-id" && string(ticket.Payload) == createdRaw {
			preserved = true
		}
	}
	if len(want) != 4 || !preserved {
		t.Fatalf("oracle 样本没有覆盖原始 payload 与跨 key 隔离: %s", fmt.Sprint(want))
	}
}

func TestOpenTicketProjectionRebuildRepairsMissingRows(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "投影可重建")
	for i, ticketID := range []string{"one", "two"} {
		if _, err := s.AppendMirroredEvent(card.ID, MirroredEvent{Target: "linux-02", Task: "rebuild-task",
			SourceSeq: int64(i + 1), Type: evTicketQuestion,
			Payload: []byte(fmt.Sprintf(`{"ticket_id":%q}`, ticketID)), CreatedAt: time.Now()}); err != nil {
			t.Fatalf("写入重建样本 %s: %v", ticketID, err)
		}
	}
	want := replayOpenTicketsOracle(t, s)
	if _, err := s.db.Exec(`DELETE FROM open_ticket_projection WHERE ticket_id = 'one'`); err != nil {
		t.Fatalf("模拟投影行缺失: %v", err)
	}
	if _, err := s.OpenTickets(); err == nil {
		t.Fatal("投影行缺失不得伪装为空或返回不完整结果")
	}
	if err := s.rebuildOpenTicketProjection(); err != nil {
		t.Fatalf("显式重建投影: %v", err)
	}
	got, err := s.OpenTickets()
	if err != nil {
		t.Fatalf("重建后 OpenTickets: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("重建后与事件 oracle 不一致: got=%+v want=%+v", got, want)
	}
	if err := s.rebuildOpenTicketProjection(); err != nil {
		t.Fatalf("重复重建投影: %v", err)
	}
	gotAgain, err := s.OpenTickets()
	if err != nil || !reflect.DeepEqual(gotAgain, want) {
		t.Fatalf("重复重建应幂等: got=%+v want=%+v err=%v", gotAgain, want, err)
	}
}

func TestOpenTicketProjectionUpgradeReplaysExistingEvents(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "旧账本投影升级")
	for i, event := range []struct{ typ, body string }{
		{evTicketQuestion, `{"ticket_id":"kept"}`},
		{evTicketQuestion, `{"ticket_id":"closed"}`},
		{evTicketAnswered, `{"ticket_id":"closed"}`},
	} {
		if _, err := s.AppendMirroredEvent(card.ID, MirroredEvent{Target: "upgrade-target", Task: "upgrade-task",
			SourceSeq: int64(i + 1), Type: event.typ, Payload: []byte(event.body), CreatedAt: time.Now()}); err != nil {
			t.Fatalf("建立旧账本镜像事件: %v", err)
		}
	}
	want := replayOpenTicketsOracle(t, s)
	path := s.path
	if _, err := s.db.Exec(`DROP TABLE open_ticket_projection_state`); err != nil {
		t.Fatalf("模拟旧版账本缺少投影状态表: %v", err)
	}
	if _, err := s.db.Exec(`DROP TABLE open_ticket_projection`); err != nil {
		t.Fatalf("模拟旧版账本缺少投影表: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("关闭旧版测试账本: %v", err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatalf("升级旧版 SQLite 账本: %v", err)
	}
	t.Cleanup(func() { upgraded.Close() })
	got, err := upgraded.OpenTickets()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("启动升级应从权威事件重建投影: got=%+v want=%+v err=%v", got, want, err)
	}
}

func TestOpenTicketProjectionPostgresReplayAndRebuild(t *testing.T) {
	s := newB409PGStore(t)
	cardID := fmt.Sprintf("B409-PROJECTION-%d", time.Now().UnixNano())
	now := time.Now()
	if _, err := s.db.Exec(s.q(`INSERT INTO cards
		(id, title, status, priority, project, workflow_name, workflow_version, attachments, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`), cardID, "B409 工单投影 PG 夹具", StatusTodo, "中",
		"b409-projection-test", "bug", 1, "[]", s.tval(now), s.tval(now)); err != nil {
		t.Fatalf("建立专用 PG 测试卡: %v", err)
	}
	t.Cleanup(func() {
		if _, err := s.db.Exec(s.q(`DELETE FROM open_ticket_projection WHERE card_id = ?`), cardID); err != nil {
			t.Errorf("清理 PG 投影夹具: %v", err)
		}
		if _, err := s.db.Exec(s.q(`DELETE FROM card_events WHERE card_id = ?`), cardID); err != nil {
			t.Errorf("清理 PG 事件夹具: %v", err)
		}
		if err := s.rebuildOpenTicketProjection(); err != nil {
			t.Errorf("清理后重建 PG 投影水位: %v", err)
		}
		if _, err := s.db.Exec(s.q(`DELETE FROM cards WHERE id = ?`), cardID); err != nil {
			t.Errorf("清理 PG 测试卡: %v", err)
		}
	})
	for i, event := range []struct{ typ, body string }{
		{evTicketQuestion, `{ "ticket_id": "pg-one", "question": "keep" }`},
		{evTicketQuestion, `{"ticket_id":"pg-two"}`},
		{evTicketAnswered, `{"ticket_id":"pg-one"}`},
	} {
		if _, err := s.AppendMirroredEvent(cardID, MirroredEvent{Target: "b409-pg-target", Task: "projection-task",
			SourceSeq: int64(i + 1), Type: event.typ, Payload: []byte(event.body), CreatedAt: time.Now()}); err != nil {
			t.Fatalf("追加 PG 镜像事件 %d: %v", i+1, err)
		}
	}
	want := replayOpenTicketsOracle(t, s)
	got, err := s.OpenTickets()
	if err != nil {
		t.Fatalf("PG OpenTickets: %v", err)
	}
	if !reflect.DeepEqual(filterTicketsByCard(got, cardID), filterTicketsByCard(want, cardID)) {
		t.Fatalf("PG 投影与权威事件重放不一致: got=%+v want=%+v", filterTicketsByCard(got, cardID), filterTicketsByCard(want, cardID))
	}
	counts, err := s.OpenTicketCounts()
	if err != nil || counts[cardID] != 1 {
		t.Fatalf("PG 聚合计数应为一单: counts=%v err=%v", counts, err)
	}
	if _, err := s.db.Exec(s.q(`DELETE FROM open_ticket_projection WHERE card_id = ?`), cardID); err != nil {
		t.Fatalf("模拟 PG 投影缺行: %v", err)
	}
	if _, err := s.OpenTickets(); err == nil {
		t.Fatal("PG 投影缺行不得静默返回空结果")
	}
	if err := s.rebuildOpenTicketProjection(); err != nil {
		t.Fatalf("PG 显式重建投影: %v", err)
	}
	got, err = s.OpenTickets()
	if err != nil || !reflect.DeepEqual(filterTicketsByCard(got, cardID), filterTicketsByCard(want, cardID)) {
		t.Fatalf("PG 重建后未恢复 canonical 结果: got=%+v want=%+v err=%v",
			filterTicketsByCard(got, cardID), filterTicketsByCard(want, cardID), err)
	}
}

func filterTicketsByCard(tickets []OpenTicket, cardID string) []OpenTicket {
	filtered := make([]OpenTicket, 0)
	for _, ticket := range tickets {
		if ticket.CardID == cardID {
			filtered = append(filtered, ticket)
		}
	}
	return filtered
}

func TestAppendMirroredEventProjectionFailureRollsBackEvent(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "投影事务原子性")
	if _, err := s.db.Exec(`CREATE TRIGGER fail_ticket_projection BEFORE INSERT ON open_ticket_projection
		BEGIN SELECT RAISE(ABORT, 'forced ticket projection failure'); END`); err != nil {
		t.Fatalf("安装投影失败触发器: %v", err)
	}
	inserted, err := s.AppendMirroredEvent(card.ID, MirroredEvent{Target: "linux-03", Task: "atomic-task",
		SourceSeq: 1, Type: evTicketQuestion, Payload: []byte(`{"ticket_id":"atomic"}`), CreatedAt: time.Now()})
	if err == nil || inserted {
		t.Fatalf("投影失败时追加必须失败并报告未提交: inserted=%v err=%v", inserted, err)
	}
	var canonicalRows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM card_events
		WHERE source_target = 'linux-03' AND source_task = 'atomic-task' AND source_seq = 1`).Scan(&canonicalRows); err != nil {
		t.Fatalf("检查权威事件回滚: %v", err)
	}
	if canonicalRows != 0 {
		t.Fatalf("投影失败后权威事件仍提交了 %d 行", canonicalRows)
	}
}

func TestOpenTicketProjectionGrowthKeepsRowsAndPayloadBounded(t *testing.T) {
	s := seedStore(t)
	cards := make([]Card, 4)
	for i := range cards {
		cards[i] = mk(t, s, fmt.Sprintf("投影增长卡 %d", i+1))
		for ticket := 1; ticket <= 25; ticket++ {
			if _, err := s.AppendMirroredEvent(cards[i].ID, MirroredEvent{Target: "growth-card", Task: fmt.Sprintf("task-%d", i+1),
				SourceSeq: int64(ticket), Type: evTicketQuestion,
				Payload:   []byte(fmt.Sprintf(`{"ticket_id":"%s-%02d"}`, cards[i].ID, ticket)),
				CreatedAt: time.Now()}); err != nil {
				t.Fatalf("种 100 个固定未决工单: card=%s ticket=%d err=%v", cards[i].ID, ticket, err)
			}
		}
	}

	const baselineMirrors = 9337
	const unrelatedEvents = 20000
	const bodySize = 700
	mirrorPayload := `{"task_type":"message","payload":{"padding":"` + strings.Repeat("x", bodySize-len(`{"task_type":"message","payload":{"padding":"`)-len(`"}}`)) + `"}}`
	if len(mirrorPayload) != bodySize {
		t.Fatalf("增长镜像 payload 长度=%d，期望=%d", len(mirrorPayload), bodySize)
	}
	insertGrowthMirrors(t, s, cards[0].ID, 1, baselineMirrors-len(cards)*25, mirrorPayload)
	stages := []struct {
		name string
		seed func()
	}{
		{name: "baseline"},
		{name: "unrelated-ledger", seed: func() { insertGrowthEvents(t, s, cards[0].ID, unrelatedEvents) }},
		{name: "doubled-mirrors", seed: func() {
			insertGrowthMirrors(t, s, cards[0].ID, int64(baselineMirrors-len(cards)*25+1),
				baselineMirrors, mirrorPayload)
		}},
	}

	previousLogger := slog.Default()
	logBuffer := new(bytes.Buffer)
	slog.SetDefault(slog.New(slog.NewJSONHandler(logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previousLogger)
	var baselineMirrorCount, baselineMirrorBytes int64
	for _, stage := range stages {
		if stage.seed != nil {
			stage.seed()
		}
		mirrorRows, mirrorBytes := projectionGrowthMirrorStats(t, s)
		if stage.name == "baseline" {
			baselineMirrorCount, baselineMirrorBytes = mirrorRows, mirrorBytes
		} else if stage.name == "doubled-mirrors" && (mirrorRows < baselineMirrorCount*2 || mirrorBytes < baselineMirrorBytes*2) {
			t.Fatalf("第三阶段镜像数据未达到双倍: rows=%d baseline=%d bytes=%d baseline=%d",
				mirrorRows, baselineMirrorCount, mirrorBytes, baselineMirrorBytes)
		}
		allTickets := replayOpenTicketsOracle(t, s)
		if len(allTickets) != 100 {
			t.Fatalf("%s oracle 应保留同一组 100 条未决工单，得到 %d", stage.name, len(allTickets))
		}

		logBuffer.Reset()
		tickets, err := s.OpenTickets()
		if err != nil {
			t.Fatalf("%s OpenTickets: %v", stage.name, err)
		}
		if !reflect.DeepEqual(tickets, allTickets) {
			t.Fatalf("%s 明细与事件 oracle 不同", stage.name)
		}
		detailRead := decodeTicketReadObservation(t, logBuffer.Bytes(), "读取未决工单投影完成")

		logBuffer.Reset()
		counts, err := s.OpenTicketCounts()
		if err != nil {
			t.Fatalf("%s OpenTicketCounts: %v", stage.name, err)
		}
		if len(counts) != len(cards) {
			t.Fatalf("%s 应返回 4 张卡的聚合计数: %v", stage.name, counts)
		}
		for _, card := range cards {
			if counts[card.ID] != 25 {
				t.Fatalf("%s 卡 %s 应有 25 单: %v", stage.name, card.ID, counts)
			}
		}
		countRead := decodeTicketReadObservation(t, logBuffer.Bytes(), "聚合未决工单投影完成")
		if detailRead.RowsRead != 100 || detailRead.PayloadBytes != int64(sumTicketPayloadBytes(tickets)) {
			t.Fatalf("%s 明细读不应随历史增长: rows=%d payload_bytes=%d", stage.name, detailRead.RowsRead, detailRead.PayloadBytes)
		}
		if countRead.RowsRead != 4 || countRead.PayloadBytes != 0 {
			t.Fatalf("%s 计数读只应回传 4 行聚合且无正文: rows=%d payload_bytes=%d",
				stage.name, countRead.RowsRead, countRead.PayloadBytes)
		}
		t.Logf("stage=%s events=%d mirrors=%d mirror_payload_bytes=%d detail_rows=%d detail_payload_bytes=%d count_rows=%d count_payload_bytes=%d",
			stage.name, projectionGrowthEventCount(t, s), mirrorRows, mirrorBytes,
			detailRead.RowsRead, detailRead.PayloadBytes, countRead.RowsRead, countRead.PayloadBytes)
	}
}

func insertGrowthMirrors(t *testing.T, s *Store, cardID string, firstSourceSeq int64, count int, payload string) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatalf("开启增长镜像种子事务: %v", err)
	}
	defer tx.Rollback()
	for i := 0; i < count; i++ {
		if _, err := tx.Exec(s.q(`INSERT INTO card_events
			(card_id, type, actor, payload, source_target, source_task, source_seq, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`), cardID, EvTaskMirrored, "growth-test", payload,
			"growth-noise", "unrelated-task", firstSourceSeq+int64(i), s.tval(time.Now())); err != nil {
			t.Fatalf("种增长镜像 %d/%d: %v", i+1, count, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("提交增长镜像种子: %v", err)
	}
}

func insertGrowthEvents(t *testing.T, s *Store, cardID string, count int) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatalf("开启无关事件种子事务: %v", err)
	}
	defer tx.Rollback()
	for i := 0; i < count; i++ {
		if _, err := tx.Exec(s.q(`INSERT INTO card_events (card_id, type, actor, payload, created_at)
			VALUES (?, ?, ?, ?, ?)`), cardID, "growth_noise", "growth-test", `{}`, s.tval(time.Now())); err != nil {
			t.Fatalf("种无关事件 %d/%d: %v", i+1, count, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("提交无关事件种子: %v", err)
	}
}

func projectionGrowthMirrorStats(t *testing.T, s *Store) (rows, payloadBytes int64) {
	t.Helper()
	if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(length(payload)), 0)
		FROM card_events WHERE type = 'task_mirrored'`).Scan(&rows, &payloadBytes); err != nil {
		t.Fatalf("读取增长镜像统计: %v", err)
	}
	return rows, payloadBytes
}

func projectionGrowthEventCount(t *testing.T, s *Store) int64 {
	t.Helper()
	var count int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM card_events`).Scan(&count); err != nil {
		t.Fatalf("读取增长账本事件数: %v", err)
	}
	return count
}

func sumTicketPayloadBytes(tickets []OpenTicket) int {
	total := 0
	for _, ticket := range tickets {
		total += len(ticket.Payload)
	}
	return total
}

func decodeTicketReadObservation(t *testing.T, raw []byte, message string) struct {
	RowsRead     int64 `json:"rows_read"`
	PayloadBytes int64 `json:"payload_bytes"`
} {
	t.Helper()
	var observation struct {
		Message      string `json:"msg"`
		RowsRead     int64  `json:"rows_read"`
		PayloadBytes int64  `json:"payload_bytes"`
	}
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
		var candidate struct {
			Message      string `json:"msg"`
			RowsRead     int64  `json:"rows_read"`
			PayloadBytes int64  `json:"payload_bytes"`
		}
		if err := json.Unmarshal(line, &candidate); err != nil {
			t.Fatalf("解码搬运观测: %v; log=%s", err, raw)
		}
		if candidate.Message == message {
			if observation.Message != "" {
				t.Fatalf("重复的搬运观测 %q; log=%s", message, raw)
			}
			observation = candidate
		}
	}
	if observation.Message == "" {
		t.Fatalf("缺少搬运观测 %q; log=%s", message, raw)
	}
	return struct {
		RowsRead     int64 `json:"rows_read"`
		PayloadBytes int64 `json:"payload_bytes"`
	}{RowsRead: observation.RowsRead, PayloadBytes: observation.PayloadBytes}
}
