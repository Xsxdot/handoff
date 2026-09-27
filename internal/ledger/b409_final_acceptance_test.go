// B409.7（U7）隔离验收专用测试：跨方言逻辑事件金样与独立重放 oracle、
// pre-projection 旧库经真实 Open/ensureSchema 升级、投影 rebuild 两次幂等、
// 重建中途确定性失败与恢复、镜像追加在投影失败下的原子回滚。
//
// 数据边界（红线）：
//   - SQLite 腿只写 t.TempDir() 文件；PG 腿只写协调者确认的专用可丢弃库
//     （newB409PGStore 的 handoff_b409_test 库名守卫，本文件不绕过它），
//     金样腿与旧库升级腿使用同库内独立 schema 命名空间并在用后 DROP
//     （绝对量断言与库内既有数据解耦，任何库状态下可复跑）。
//   - 禁止把本文件的任何写入指向生产/共享账本、本机 ledger.db 或其它主机。
//   - 中途失败注入只用数据库触发器（测试内建、测试内拆），生产代码与
//     schema 无任何开关。
//
// oracle 纪律：预期模型一律从权威 card_events 原始行独立重放推导，不经由
// 任何生产投影/读路径代码；生产读与 oracle 的差异即测试红。
package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// —— 通用夹具工具 ——

// execB409FixtureDDL 逐语句执行 testdata 夹具 DDL（先剥 -- 注释行再按分号
// 切分；夹具为受控 DDL，语句内无分号字符串字面量，触发器类多语句体不经此
// 路径）。
func execB409FixtureDDL(t *testing.T, db *sql.DB, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取夹具 DDL %s: %v", path, err)
	}
	var kept []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		kept = append(kept, line)
	}
	for _, stmt := range strings.Split(strings.Join(kept, "\n"), ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("执行夹具 DDL %s 片段 %q: %v", filepath.Base(path), firstLine(stmt), err)
		}
	}
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}

// requireB409IsolatedPGName 复核 DSN 指向专用可丢弃库，拒绝其它任何目标。
func requireB409IsolatedPGName(t *testing.T, dsn string) {
	t.Helper()
	probe, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("连接 B409 隔离 PG: %v", err)
	}
	defer probe.Close()
	var name string
	if err := probe.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatalf("读取 B409 隔离 PG 库名: %v", err)
	}
	if name != "handoff_b409_test" {
		t.Fatalf("拒绝向非专用 PostgreSQL 数据库写入合成数据: current_database()=%q", name)
	}
}

// newB409PGStoreInIsolatedSchema 在专用库的独立 schema 命名空间内打开 Store
// （与升级腿 TestB409U7PreProjectionUpgradePostgres 同一隔离模式：库内建全新
// schema + search_path DSN 指向它，用后 DROP CASCADE）。金样断言含全库绝对量
// （未决工单恰 2 条、oracle 全表重放），schema 隔离使其与库内既有数据（如
// 矩阵夹具残留）解耦，任何库状态下可复跑；库外零写入红线由库名守卫保持。
func newB409PGStoreInIsolatedSchema(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("LEDGER_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未设 LEDGER_TEST_PG_DSN，跳过 B409 PG 金样测试")
	}
	requireB409IsolatedPGName(t, dsn)
	schema := fmt.Sprintf("b409_gold_%d", time.Now().UnixNano())
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("连接隔离 PG: %v", err)
	}
	if _, err := admin.Exec(fmt.Sprintf(`CREATE SCHEMA %s`, schema)); err != nil {
		admin.Close()
		t.Fatalf("建隔离 schema: %v", err)
	}
	t.Cleanup(func() {
		// schema 清理与连接关闭同处一个 cleanup：defer 会先于 t.Cleanup 执行，
		// 不能依赖 defer 的连接存活。
		if _, err := admin.Exec(fmt.Sprintf(`DROP SCHEMA %s CASCADE`, schema)); err != nil {
			t.Errorf("清理隔离 schema %s: %v", schema, err)
		}
		if err := admin.Close(); err != nil {
			t.Errorf("关闭隔离 PG 管理连接: %v", err)
		}
	})
	// DSN 已带 query（sslmode）用 &，否则用 ?；search_path 值为本文件生成的
	// 受控标识符（字母/下划线/数字），无需 URL 编码。
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	isolatedDSN := dsn + sep + "search_path=" + schema
	store, err := Open(isolatedDSN)
	if err != nil {
		t.Fatalf("打开隔离 schema 内的 B409 PG 测试库: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// —— 金样（步骤 2）：同一逻辑事件集，经真实生产写路径分别落入两个方言 ——

type b409U7Golden struct {
	stamp                      string
	workflow                   string
	cardA, cardB               string
	sessionMain, sessionEmpty  string
	memberA, memberB, memberNo string
	target1, target2, target3  string
	task1, task2, task3, taskB string
	targetB                    string
	seqM1, seqM2, seqM3, seqM4 int64
	seqM5, seqM6, seqM7, seqM8 int64
}

// seedB409U7Golden 按固定顺序把同一逻辑事件集写入任一方言：卡经 ImportCard、
// 会话经 CreateSession、房间消息经 RecordRoomMessage、消费经
// RecordMessageConsumed、工单经 AppendMirroredEvent、交付游标经
// AdvanceSessionDeliveryCursor——全部是生产写路径，无直插。
func seedB409U7Golden(t *testing.T, s *Store) *b409U7Golden {
	t.Helper()
	g := &b409U7Golden{stamp: fmt.Sprintf("%d", time.Now().UnixNano())}
	g.workflow = "u7-gold-" + g.stamp
	if _, err := s.PutWorkflow(g.workflow, WorkflowDef{Nodes: []NodeDef{
		{Name: StatusTodo, Next: StatusDoing},
		{Name: StatusDoing},
	}}); err != nil {
		t.Fatalf("金样写工作流: %v", err)
	}
	g.cardA = "B" + g.stamp
	g.cardB = "C" + g.stamp
	if _, err := s.ImportCard(g.cardA, "b409-u7-golden", NewCard{
		Title: "B409 U7 金样卡 A", Project: "u7-gold-" + g.stamp, Workflow: g.workflow, Actor: "b409-u7",
	}); err != nil {
		t.Fatalf("金样建卡 A: %v", err)
	}
	if _, err := s.ImportCard(g.cardB, "b409-u7-golden", NewCard{
		Title: "B409 U7 金样卡 B", Project: "u7-gold-" + g.stamp, Workflow: g.workflow, Actor: "b409-u7",
	}); err != nil {
		t.Fatalf("金样建卡 B: %v", err)
	}
	g.memberA = "agent:gold-a-" + g.stamp
	g.memberB = "agent:gold-b-" + g.stamp
	g.memberNo = "agent:gold-nobody-" + g.stamp
	sessionMain, err := s.CreateSession("B409 U7 金样会话", "user:gold", "b409-u7")
	if err != nil {
		t.Fatalf("金样建主会话: %v", err)
	}
	sessionEmpty, err := s.CreateSession("B409 U7 空会话", "user:gold", "b409-u7")
	if err != nil {
		t.Fatalf("金样建空会话: %v", err)
	}
	g.sessionMain, g.sessionEmpty = sessionMain.ID, sessionEmpty.ID

	send := func(msg proto.RoomMessage, actor string) int64 {
		t.Helper()
		seq, err := s.RecordRoomMessage("", msg, actor)
		if err != nil {
			t.Fatalf("金样写房间消息 %q: %v", msg.Body, err)
		}
		return seq
	}
	// 顺序固定：定向一 → 普通 → 回复（隐式寻址作者 memberA）→ 定向 B →
	// 无寻址系统指针 → memberA 发言 → 回复 memberA（reply 分支正样本）→
	// 带寻址系统指针（by_system 超集行）。
	g.seqM1 = send(proto.RoomMessage{Room: g.sessionMain, Kind: proto.RoomMsgUser,
		Body: "b409-golden 定向消息一", Mentions: []string{g.memberA}}, "user:gold")
	g.seqM2 = send(proto.RoomMessage{Room: g.sessionMain, Kind: proto.RoomMsgUser,
		Body: "b409-golden 普通消息"}, "user:gold")
	g.seqM3 = send(proto.RoomMessage{Room: g.sessionMain, Kind: proto.RoomMsgUser,
		Body: "b409-golden 回复一", ReplyTo: g.seqM1}, g.memberA)
	g.seqM4 = send(proto.RoomMessage{Room: g.sessionMain, Kind: proto.RoomMsgUser,
		Body: "b409-golden 定向消息二", Mentions: []string{g.memberB}}, "user:gold")
	g.seqM5 = send(proto.RoomMessage{Room: g.sessionMain, Kind: proto.RoomMsgPointer,
		Body: "b409-golden 指针行", BySystem: true}, "system:b409-u7")
	g.seqM6 = send(proto.RoomMessage{Room: g.sessionMain, Kind: proto.RoomMsgUser,
		Body: "b409-golden 成员发言"}, g.memberA)
	g.seqM7 = send(proto.RoomMessage{Room: g.sessionMain, Kind: proto.RoomMsgUser,
		Body: "b409-golden 回复成员发言", ReplyTo: g.seqM6}, "user:gold")
	g.seqM8 = send(proto.RoomMessage{Room: g.sessionMain, Kind: proto.RoomMsgPointer,
		Body: "b409-golden 寻址指针行", Mentions: []string{g.memberA}, BySystem: true}, "system:b409-u7")

	if err := s.RecordMessageConsumed("", g.seqM1, g.memberA); err != nil {
		t.Fatalf("金样消费 m1: %v", err)
	}
	if err := s.RecordMessageConsumed("", g.seqM4, g.memberB); err != nil {
		t.Fatalf("金样消费 m4: %v", err)
	}

	g.target1, g.target2, g.target3, g.targetB = "gold-t1-"+g.stamp, "gold-t2-"+g.stamp, "gold-t3-"+g.stamp, "gold-tb-"+g.stamp
	g.task1, g.task2, g.task3, g.taskB = "gold-task-1", "gold-task-2", "gold-task-3", "gold-task-b"
	mirror := func(cardID, target, task string, sourceSeq int64, typ, body string) {
		t.Helper()
		inserted, err := s.AppendMirroredEvent(cardID, MirroredEvent{Target: target, Task: task,
			SourceSeq: sourceSeq, Type: typ, Payload: []byte(body), CreatedAt: time.Now()})
		if err != nil || !inserted {
			t.Fatalf("金样镜像 %s/%s#%d %s: inserted=%v err=%v", target, task, sourceSeq, typ, inserted, err)
		}
	}
	mirror(g.cardA, g.target1, g.task1, 1, evTicketQuestion, `{"ticket_id":"gold-q1"}`)
	mirror(g.cardA, g.target2, g.task2, 1, evTicketCreated, `{"ticket_id":"gold-p1"}`)
	mirror(g.cardA, g.target2, g.task2, 2, evTicketAnswered, `{"ticket_id":"gold-p1"}`)
	mirror(g.cardA, g.target1, g.task1, 2, evTicketsVoided, `{}`)
	mirror(g.cardA, g.target3, g.task3, 1, evTicketQuestion, `{"ticket_id":"gold-open"}`)
	mirror(g.cardB, g.targetB, g.taskB, 1, evTicketQuestion, `{"ticket_id":"gold-b1"}`)
	// 同一来源三元组重放必须幂等跳过，不得重复入账。
	replayed, err := s.AppendMirroredEvent(g.cardA, MirroredEvent{Target: g.target3, Task: g.task3,
		SourceSeq: 1, Type: evTicketQuestion, Payload: []byte(`{"ticket_id":"gold-open"}`), CreatedAt: time.Now()})
	if err != nil || replayed {
		t.Fatalf("金样镜像重放应幂等跳过: inserted=%v err=%v", replayed, err)
	}
	if err := s.AdvanceSessionDeliveryCursor(g.memberA, g.seqM3); err != nil {
		t.Fatalf("金样推进交付游标: %v", err)
	}
	return g
}

// b409U7Oracle 独立重放预期模型：只读权威 card_events 原始行，不经任何生产
// 投影/读代码。
type b409U7Oracle struct {
	openTickets      []OpenTicket
	counts           map[string]int
	roomMessages     []Event
	candidatesA      []int64
	candidatesB      []int64
	consumedA        []int64
	cursorA          int64
	cursorB          int64
	maxMirrorSeq     int64
	mirrorIdentities []string
}

// replayB409U7Oracle 从 card_events 重放金样预期。actorToSeq 记录房间消息作者，
// 供 reply 分支独立推导（与 session_candidates 的 SQL 表达互为对照实现）。
func replayB409U7Oracle(t *testing.T, s *Store, g *b409U7Golden) *b409U7Oracle {
	t.Helper()
	o := &b409U7Oracle{counts: map[string]int{}, cursorB: 0}
	rows, err := s.db.Query(s.q(`SELECT seq, card_id, type, actor, payload, source_target, source_task, source_seq
		FROM card_events ORDER BY seq ASC`))
	if err != nil {
		t.Fatalf("oracle 读权威事件: %v", err)
	}
	defer rows.Close()
	roomMsgActor := map[int64]string{}
	open := map[string]OpenTicket{}
	for rows.Next() {
		var seq int64
		var cardID, typ, actor, payload string
		var nullCardID sql.NullString
		var target, task sql.NullString
		var sourceSeq sql.NullInt64
		if err := rows.Scan(&seq, &nullCardID, &typ, &actor, &payload, &target, &task, &sourceSeq); err != nil {
			t.Fatalf("oracle 扫事件: %v", err)
		}
		cardID = nullCardID.String
		switch typ {
		case EvRoomMessage:
			var msg proto.RoomMessage
			if err := json.Unmarshal([]byte(payload), &msg); err != nil {
				t.Fatalf("oracle 解消息 seq=%d: %v", seq, err)
			}
			if msg.Room != g.sessionMain {
				continue
			}
			roomMsgActor[seq] = actor
			o.roomMessages = append(o.roomMessages, Event{Seq: seq, CardID: cardID, Type: typ, Actor: actor,
				Payload: json.RawMessage(payload)})
			for _, m := range msg.Mentions {
				switch m {
				case g.memberA:
					o.candidatesA = append(o.candidatesA, seq)
				case g.memberB:
					o.candidatesB = append(o.candidatesB, seq)
				}
			}
		case EvMessageConsumed:
			var marker consumedMarker
			if err := json.Unmarshal([]byte(payload), &marker); err != nil {
				t.Fatalf("oracle 解消费标记 seq=%d: %v", seq, err)
			}
			if marker.Consumer == g.memberA {
				o.consumedA = append(o.consumedA, marker.MessageSeq)
			}
		case EvTaskMirrored:
			if cardID == "" || !target.Valid || !task.Valid {
				continue
			}
			o.maxMirrorSeq = seq
			o.mirrorIdentities = append(o.mirrorIdentities, fmt.Sprintf("%s/%s#%d", target.String, task.String, sourceSeq.Int64))
			var event mirroredTaskPayload
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				t.Fatalf("oracle 解镜像 seq=%d: %v", seq, err)
			}
			key := func(ticketID string) string { return cardID + "|" + target.String + "|" + task.String + "|" + ticketID }
			switch event.TaskType {
			case evTicketCreated, evTicketQuestion:
				var body ticketPayload
				if err := json.Unmarshal(event.Payload, &body); err != nil {
					t.Fatalf("oracle 解工单创建 seq=%d: %v", seq, err)
				}
				if body.TicketID != "" {
					open[key(body.TicketID)] = OpenTicket{CardID: cardID, Target: target.String, TaskID: task.String,
						TicketID: body.TicketID, TaskType: event.TaskType, Payload: append(json.RawMessage(nil), event.Payload...)}
				}
			case evTicketAnswered:
				var body ticketPayload
				if err := json.Unmarshal(event.Payload, &body); err != nil {
					t.Fatalf("oracle 解工单答复 seq=%d: %v", seq, err)
				}
				delete(open, key(body.TicketID))
			case evTicketsVoided, "completed", "failed", "archived":
				prefix := cardID + "|" + target.String + "|" + task.String + "|"
				for k := range open {
					if strings.HasPrefix(k, prefix) {
						delete(open, k)
					}
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("oracle 遍历事件结束: %v", err)
	}
	// reply 分支：reply_to 指向 memberA 作者的消息（同房间已由 roomMsgActor 保证——
	// 金样只有一个房间，跨房间 reply 由权威判定拒绝，oracle 与 SQL 分支同为超集放行）。
	for _, ev := range o.roomMessages {
		var msg proto.RoomMessage
		if err := json.Unmarshal(ev.Payload, &msg); err != nil {
			t.Fatalf("oracle 再解消息 seq=%d: %v", ev.Seq, err)
		}
		if msg.ReplyTo > 0 && roomMsgActor[msg.ReplyTo] == g.memberA {
			o.candidatesA = append(o.candidatesA, ev.Seq)
		}
	}
	sort.Slice(o.candidatesA, func(i, j int) bool { return o.candidatesA[i] < o.candidatesA[j] })
	sort.Slice(o.candidatesB, func(i, j int) bool { return o.candidatesB[i] < o.candidatesB[j] })
	for _, ticket := range open {
		o.openTickets = append(o.openTickets, ticket)
		o.counts[ticket.CardID]++
	}
	sortCanonicalOpenTickets(o.openTickets)
	sort.Slice(o.roomMessages, func(i, j int) bool { return o.roomMessages[i].Seq < o.roomMessages[j].Seq })
	o.cursorA = g.seqM3
	return o
}

// assertB409U7Golden 把生产读路径逐项对照 oracle 与冻结金样；返回逐方言读回
// 的 payload bytes 记录（不静默规范化字节差异，只做语义等值断言）。
func assertB409U7Golden(t *testing.T, s *Store, g *b409U7Golden, dialect string) {
	t.Helper()
	o := replayB409U7Oracle(t, s, g)

	// 1) 工单投影：明细与聚合计数与 oracle 逐字段一致。
	tickets, err := s.OpenTickets()
	if err != nil {
		t.Fatalf("[%s] OpenTickets: %v", dialect, err)
	}
	sortCanonicalOpenTickets(tickets)
	if !reflect.DeepEqual(tickets, o.openTickets) {
		t.Fatalf("[%s] OpenTickets 与独立 oracle 不一致:\n got=%+v\nwant=%+v", dialect, tickets, o.openTickets)
	}
	counts, err := s.OpenTicketCounts()
	if err != nil {
		t.Fatalf("[%s] OpenTicketCounts: %v", dialect, err)
	}
	if !reflect.DeepEqual(counts, o.counts) {
		t.Fatalf("[%s] OpenTicketCounts 与 oracle 不一致: got=%v want=%v", dialect, counts, o.counts)
	}
	// 冻结金样：恰 2 张未决工单（gold-open@卡A、gold-b1@卡B），已建/已答/已作废不残留。
	if len(tickets) != 2 {
		t.Fatalf("[%s] 金样未决工单应为 2 条，实得 %d: %+v", dialect, len(tickets), tickets)
	}

	// 2) 房间历史窗口：升序、字段语义与 oracle 一致；排他游标边界。
	events, err := s.RoomMessagesBeforeContext(context.Background(), g.sessionMain, 0, 200)
	if err != nil {
		t.Fatalf("[%s] 房间历史读: %v", dialect, err)
	}
	if len(events) != len(o.roomMessages) {
		t.Fatalf("[%s] 房间历史行数 %d ≠ oracle %d", dialect, len(events), len(o.roomMessages))
	}
	for i, ev := range events {
		want := o.roomMessages[i]
		if ev.Seq != want.Seq || ev.Type != want.Type || ev.Actor != want.Actor || ev.CardID != want.CardID {
			t.Fatalf("[%s] 房间历史第 %d 行身份不一致: got=%+v want=%+v", dialect, i, ev, want)
		}
		var gotMsg, wantMsg proto.RoomMessage
		if err := json.Unmarshal(ev.Payload, &gotMsg); err != nil {
			t.Fatalf("[%s] 解生产消息 seq=%d: %v", dialect, ev.Seq, err)
		}
		if err := json.Unmarshal(want.Payload, &wantMsg); err != nil {
			t.Fatalf("[%s] 解 oracle 消息 seq=%d: %v", dialect, want.Seq, err)
		}
		if !reflect.DeepEqual(gotMsg, wantMsg) {
			t.Fatalf("[%s] 房间消息 seq=%d 语义不一致: got=%+v want=%+v", dialect, ev.Seq, gotMsg, wantMsg)
		}
	}
	// 排他边界：before=m3 时全部行 seq < m3，且是 m3 之前的真实窗口。
	excl, err := s.RoomMessagesBeforeContext(context.Background(), g.sessionMain, g.seqM3, 200)
	if err != nil {
		t.Fatalf("[%s] 排他窗口读: %v", dialect, err)
	}
	for _, ev := range excl {
		if ev.Seq >= g.seqM3 {
			t.Fatalf("[%s] 排他游标语义破坏：before=%d 返回 seq=%d", dialect, g.seqM3, ev.Seq)
		}
	}

	// 3) 会话候选超集：mentions/reply 分支与 oracle 一致；fromSeq 排他、toSeq 包含。
	cands, err := s.SessionMessageCandidates(g.memberA, 0, 0, 10000)
	if err != nil {
		t.Fatalf("[%s] memberA 候选读: %v", dialect, err)
	}
	assertB409U7Seqs(t, dialect, "memberA 候选", b409U7SeqsOf(cands), o.candidatesA)
	// 冻结金样（契约超集逐行钉住）：memberA 恰命中 m1（身份 @）、m7（reply→
	// memberA 作者）、m8（by_system 带 @ 的超集行）；普通发言/无寻址指针/他人
	// @ 不得混入。
	assertB409U7Seqs(t, dialect, "memberA 冻结金样", o.candidatesA, []int64{g.seqM1, g.seqM7, g.seqM8})
	candsB, err := s.SessionMessageCandidates(g.memberB, 0, 0, 10000)
	if err != nil {
		t.Fatalf("[%s] memberB 候选读: %v", dialect, err)
	}
	assertB409U7Seqs(t, dialect, "memberB 候选", b409U7SeqsOf(candsB), o.candidatesB)
	assertB409U7Seqs(t, dialect, "memberB 冻结金样", o.candidatesB, []int64{g.seqM4})
	candsPage, err := s.SessionMessageCandidates(g.memberA, g.seqM3, 0, 100)
	if err != nil {
		t.Fatalf("[%s] memberA 增量候选读: %v", dialect, err)
	}
	for _, ev := range candsPage {
		if ev.Seq <= g.seqM3 {
			t.Fatalf("[%s] 候选 fromSeq 排他破坏：from=%d 返回 seq=%d", dialect, g.seqM3, ev.Seq)
		}
	}
	candsUpper, err := s.SessionMessageCandidates(g.memberA, 0, g.seqM2, 100)
	if err != nil {
		t.Fatalf("[%s] memberA 上界候选读: %v", dialect, err)
	}
	for _, ev := range candsUpper {
		if ev.Seq > g.seqM2 {
			t.Fatalf("[%s] 候选 toSeq 包含破坏：to=%d 返回 seq=%d", dialect, g.seqM2, ev.Seq)
		}
	}

	// 4) 交付游标：已推进 member 与从未交付 member 不得混淆。
	cursorA, err := s.SessionDeliveryCursor(g.memberA)
	if err != nil || cursorA != o.cursorA {
		t.Fatalf("[%s] memberA 交付游标 = %d/%v，期望 %d", dialect, cursorA, err, o.cursorA)
	}
	cursorB, err := s.SessionDeliveryCursor(g.memberB)
	if err != nil || cursorB != 0 {
		t.Fatalf("[%s] memberB 交付游标应缺省为 0: %d/%v", dialect, cursorB, err)
	}

	// 5) 水位：投影状态行的 ledger_seq 与 oracle 最大合格镜像 seq 一致。
	var version int
	var ledgerSeq int64
	if err := s.db.QueryRow(`SELECT version, ledger_seq FROM open_ticket_projection_state WHERE id = 1`).
		Scan(&version, &ledgerSeq); err != nil {
		t.Fatalf("[%s] 读投影水位: %v", dialect, err)
	}
	if version != openTicketProjectionVersion || ledgerSeq != o.maxMirrorSeq {
		t.Fatalf("[%s] 投影水位不一致: version=%d ledger_seq=%d，期望 version=%d seq=%d",
			dialect, version, ledgerSeq, openTicketProjectionVersion, o.maxMirrorSeq)
	}
	wm, err := s.MirrorWatermark(g.target3, g.task3)
	if err != nil || wm != 1 {
		t.Fatalf("[%s] 镜像 watermark(target3,task3) = %d/%v，期望 1", dialect, wm, err)
	}

	// 5b) 投影滞后不冒充最新：直接落一条新未决工单镜像（绕过投影维护，
	// 模拟旧 writer/半途失败遗留），下一次读必须显式追赶补齐——不得把
	// 落后投影当完整最新返回，也不得静默吞掉这条工单。
	lagEnvelope := `{"node":"u7","attempt":"u7","task_type":"permission_request","payload":{"ticket_id":"gold-late-ticket"}}`
	if _, err := s.db.Exec(s.q(`INSERT INTO card_events
		(card_id, type, actor, payload, source_target, source_task, source_seq, created_at)
		VALUES (?, ?, 'mirror', ?, ?, ?, 1, ?)`),
		g.cardB, EvTaskMirrored, lagEnvelope, "gold-lag-"+g.stamp, "gold-lag-task", s.tval(time.Now())); err != nil {
		t.Fatalf("[%s] 种滞后镜像: %v", dialect, err)
	}
	lateTickets, err := s.OpenTickets()
	if err != nil {
		t.Fatalf("[%s] 滞后追赶读: %v", dialect, err)
	}
	if len(lateTickets) != 3 {
		t.Fatalf("[%s] 投影滞后未追赶：应含新落账工单共 3 条，实得 %d: %+v", dialect, len(lateTickets), lateTickets)
	}
	hasLate := false
	for _, ticket := range lateTickets {
		if ticket.TicketID == "gold-late-ticket" && ticket.CardID == g.cardB {
			hasLate = true
		}
	}
	if !hasLate {
		t.Fatalf("[%s] 追赶后缺少 gold-late-ticket: %+v", dialect, lateTickets)
	}

	// 6) 空 / 失败 / 取消三者不混淆。
	empty, err := s.RoomMessagesBeforeContext(context.Background(), g.sessionEmpty, 0, 200)
	if err != nil || len(empty) != 0 {
		t.Fatalf("[%s] 真实空房间应 0 行无错: rows=%d err=%v", dialect, len(empty), err)
	}
	emptyCands, err := s.SessionMessageCandidates(g.memberNo, 0, 0, 100)
	if err != nil || len(emptyCands) != 0 {
		t.Fatalf("[%s] 无提及成员候选应真实空: rows=%d err=%v", dialect, len(emptyCands), err)
	}
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.RoomMessagesBeforeContext(cancelCtx, g.sessionMain, 0, 200); !errors.Is(err, context.Canceled) {
		t.Fatalf("[%s] 取消读应返回 context.Canceled 而非空结果: err=%v", dialect, err)
	}
	if _, err := s.SessionMessageCandidatesContext(cancelCtx, g.memberA, 0, 0, 100); !errors.Is(err, context.Canceled) {
		t.Fatalf("[%s] 候选取消应返回 context.Canceled: err=%v", dialect, err)
	}
	failureStore := b409U7FailureProbeStore(t, s)
	if _, err := failureStore.RoomMessagesBeforeContext(context.Background(), g.sessionMain, 0, 200); err == nil {
		t.Fatalf("[%s] 存储不可用读应报错而非空结果", dialect)
	}

	// 7) 逐方言读回字节留痕（PG JSONB 与 SQLite TEXT 的存储编码差异如实记录）。
	payloadBytes := 0
	for _, ev := range events {
		payloadBytes += len(ev.Payload)
	}
	ticketBytes := 0
	for _, ticket := range tickets {
		ticketBytes += len(ticket.Payload)
	}
	t.Logf("B409_U7_GOLDEN dialect=%s room_rows=%d room_payload_bytes=%d tickets=%d ticket_payload_bytes=%d "+
		"candidates_a=%d candidates_b=%d cursor_a=%d max_mirror_seq=%d",
		dialect, len(events), payloadBytes, len(tickets), ticketBytes,
		len(o.candidatesA), len(o.candidatesB), o.cursorA, o.maxMirrorSeq)
}

func b409U7SeqsOf(events []Event) []int64 {
	out := make([]int64, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Seq)
	}
	return out
}

func assertB409U7Seqs(t *testing.T, dialect, label string, got, want []int64) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("[%s] %s 候选集不一致: got=%v want=%v", dialect, label, got, want)
	}
}

// b409U7FailureProbeStore 打开第二个同位存储并立即关闭，用于「存储不可用 =
// 显式错误」探针；与合法空结果区分。
func b409U7FailureProbeStore(t *testing.T, s *Store) *Store {
	t.Helper()
	var probe *Store
	var err error
	if s.dialect == dialectPG {
		probe, err = Open(s.dsn)
	} else {
		probe, err = Open(s.path)
	}
	if err != nil {
		t.Fatalf("打开失败探针存储: %v", err)
	}
	if err := probe.Close(); err != nil {
		t.Fatalf("关闭失败探针存储: %v", err)
	}
	return probe
}

// cleanupB409U7Golden 按 stamp 主键清理专用 PG 上的金样行（SQLite 文件随
// TempDir 消亡，无需清理）；镜像事件经卡号级联删除后重建投影水位。
func cleanupB409U7Golden(t *testing.T, s *Store, g *b409U7Golden) {
	t.Helper()
	if s.dialect != dialectPG {
		return
	}
	cleanup := func(stmt string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(s.q(stmt), args...); err != nil {
			t.Errorf("金样清理执行 %q: %v", firstLine(stmt), err)
		}
	}
	cleanup(`DELETE FROM card_events WHERE card_id IN (?, ?)`, g.cardA, g.cardB)
	cleanup(`DELETE FROM card_events WHERE type = 'room_message' AND payload->>'room' IN (?, ?)`, g.sessionMain, g.sessionEmpty)
	cleanup(`DELETE FROM card_events WHERE type = 'message_consumed' AND actor IN (?, ?)`, g.memberA, g.memberB)
	cleanup(`DELETE FROM card_events WHERE type = 'session_created' AND payload->>'id' IN (?, ?)`, g.sessionMain, g.sessionEmpty)
	cleanup(`DELETE FROM session_delivery_cursors WHERE member IN (?, ?)`, g.memberA, g.memberB)
	cleanup(`DELETE FROM sessions WHERE id IN (?, ?)`, g.sessionMain, g.sessionEmpty)
	cleanup(`DELETE FROM open_ticket_projection WHERE card_id IN (?, ?)`, g.cardA, g.cardB)
	cleanup(`DELETE FROM cards WHERE id IN (?, ?)`, g.cardA, g.cardB)
	cleanup(`DELETE FROM workflows WHERE name = ?`, g.workflow)
	if err := s.rebuildOpenTicketProjection(); err != nil {
		t.Errorf("金样清理后重建投影水位: %v", err)
	}
}

// TestB409U7CrossDialectGoldenAndOracle 步骤 2 主测试：同一逻辑金样在 SQLite
// 与专用 PG 上经真实写路径建立，逐项对照独立 oracle；SQLite 腿无条件跑，PG 腿
// 设 LEDGER_TEST_PG_DSN 后启用。
func TestB409U7CrossDialectGoldenAndOracle(t *testing.T) {
	if dsn := os.Getenv("LEDGER_TEST_PG_DSN"); dsn != "" {
		t.Run("postgres", func(t *testing.T) {
			s := newB409PGStoreInIsolatedSchema(t)
			g := seedB409U7Golden(t, s)
			t.Cleanup(func() { cleanupB409U7Golden(t, s, g) })
			assertB409U7Golden(t, s, g, "postgres")
		})
	} else {
		t.Log("未设 LEDGER_TEST_PG_DSN，PG 金样腿跳过（SQLite 腿仍执行）")
	}
	t.Run("sqlite", func(t *testing.T) {
		s := seedStore(t)
		g := seedB409U7Golden(t, s)
		assertB409U7Golden(t, s, g, "sqlite")
	})
}

// —— 步骤 3：pre-projection 旧库升级、rebuild 幂等与中途失败恢复 ——

// b409LegacyFixture 一份旧库形态的确定性 canonical 数据（升级前写入）。
type b409LegacyFixture struct {
	CardID     string
	Workflow   string
	RoomID     string
	Member     string
	Target1    string
	Task1      string
	TicketOpen string // 升级后应保持未决的工单
	TicketGone string // 已答复工单（升级后不得出现）
}

// seedB409LegacyRows 直接以旧库时代的持久格式写入 canonical 行（旧写路径
// 产生的就是这些列与 envelope；此时投影表尚不存在）。sqlite 与 pg 的差异只
// 在时间编码与 payload 列类型，SQL 文本两方言同形（占位符经 pg 重写）。
func seedB409LegacyRows(t *testing.T, db *sql.DB, pg bool, fx b409LegacyFixture) {
	t.Helper()
	now := time.Now().UTC()
	tval := func(t time.Time) any {
		if pg {
			return t
		}
		return t.Format(time.RFC3339Nano)
	}
	cardStmt := `INSERT INTO cards (id, title, status, priority, project, workflow_name, workflow_version, attachments, created_at, updated_at)
		VALUES (?, 'B409 U7 旧库卡', '待办', '中', 'u7-legacy', ?, 1, '[]', ?, ?)`
	if pg {
		cardStmt = rewriteB409Placeholders(cardStmt)
	}
	if _, err := db.Exec(cardStmt,
		fx.CardID, fx.Workflow, tval(now), tval(now)); err != nil {
		t.Fatalf("旧库写卡: %v", err)
	}
	mirrorEnvelope := `{"node":"u7-node","attempt":"u7-attempt","task_type":"question","payload":{"ticket_id":"%s"}}`
	rows := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO card_events (card_id, type, actor, payload, created_at) VALUES (NULL, 'room_message', 'user:legacy', ?, ?)`,
			[]any{fmt.Sprintf(`{"room":%q,"kind":"user","body":"b409-legacy 旧库消息一"}`, fx.RoomID), tval(now)}},
		{`INSERT INTO card_events (card_id, type, actor, payload, created_at) VALUES (NULL, 'room_message', 'user:legacy', ?, ?)`,
			[]any{fmt.Sprintf(`{"room":%q,"kind":"user","body":"b409-legacy 旧库消息二","mentions":[%q]}`, fx.RoomID, fx.Member), tval(now)}},
		{`INSERT INTO card_events (card_id, type, actor, payload, source_target, source_task, source_seq, created_at)
			VALUES (?, 'task_mirrored', 'mirror', ?, ?, ?, 1, ?)`,
			[]any{fx.CardID, fmt.Sprintf(mirrorEnvelope, fx.TicketOpen), fx.Target1, fx.Task1, tval(now)}},
		{`INSERT INTO card_events (card_id, type, actor, payload, source_target, source_task, source_seq, created_at)
			VALUES (?, 'task_mirrored', 'mirror', ?, ?, ?, 2, ?)`,
			[]any{fx.CardID, fmt.Sprintf(mirrorEnvelope, fx.TicketGone), fx.Target1, fx.Task1, tval(now)}},
		{`INSERT INTO card_events (card_id, type, actor, payload, source_target, source_task, source_seq, created_at)
			VALUES (?, 'task_mirrored', 'mirror', ?, ?, ?, 3, ?)`,
			[]any{fx.CardID, `{"node":"u7-node","attempt":"u7-attempt","task_type":"ticket_answered","payload":{"ticket_id":"` + fx.TicketGone + `"}}`, fx.Target1, fx.Task1, tval(now)}},
		{`INSERT INTO card_events (card_id, type, actor, payload, created_at) VALUES (NULL, 'message_consumed', ?, ?, ?)`,
			[]any{fx.Member, fmt.Sprintf(`{"message_seq":1,"consumer":%q}`, fx.Member), tval(now)}},
	}
	for i, row := range rows {
		stmt := row.query
		if pg {
			stmt = rewriteB409Placeholders(stmt)
		}
		if _, err := db.Exec(stmt, row.args...); err != nil {
			t.Fatalf("旧库写事件 %d: %v", i+1, err)
		}
	}
	cursorStmt := `INSERT INTO session_delivery_cursors (member, last_seq, updated_at) VALUES (?, 1, ?)`
	if pg {
		cursorStmt = rewriteB409Placeholders(cursorStmt)
	}
	if _, err := db.Exec(cursorStmt,
		fx.Member, tval(now)); err != nil {
		t.Fatalf("旧库写交付游标: %v", err)
	}
}

// rewriteB409Placeholders 为 PG 方言把 ? 重写为 $N（与 Store.q 同规则，供
// 尚无 Store 句柄的旧库夹具写入使用）。
func rewriteB409Placeholders(query string) string {
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// verifyB409UpgradedLedger 升级后统一断言：canonical 事件无损、投影与独立
// oracle 一致、水位正确、旧游标保留、真实空/错误语义可用。
func verifyB409UpgradedLedger(t *testing.T, s *Store, fx b409LegacyFixture, canonicalRows int, expectMirrorSeq int64) {
	t.Helper()
	var gotRows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM card_events`).Scan(&gotRows); err != nil {
		t.Fatalf("升级后读 canonical 事件数: %v", err)
	}
	if gotRows != canonicalRows {
		t.Fatalf("升级不得改写权威事件：canonical 行数 %d ≠ 升级前 %d", gotRows, canonicalRows)
	}
	tickets, err := s.OpenTickets()
	if err != nil {
		t.Fatalf("升级后 OpenTickets: %v", err)
	}
	if len(tickets) != 1 || tickets[0].TicketID != fx.TicketOpen || tickets[0].CardID != fx.CardID ||
		tickets[0].Target != fx.Target1 || tickets[0].TaskID != fx.Task1 {
		t.Fatalf("升级后未决工单与 oracle 不符: %+v", tickets)
	}
	if string(tickets[0].Payload) != fmt.Sprintf(`{"ticket_id":"%s"}`, fx.TicketOpen) && s.dialect == dialectSQLite {
		t.Fatalf("SQLite 升级后工单正文应逐字保留: %s", tickets[0].Payload)
	}
	counts, err := s.OpenTicketCounts()
	if err != nil || counts[fx.CardID] != 1 {
		t.Fatalf("升级后聚合计数应为 1: %v/%v", counts, err)
	}
	var version int
	var ledgerSeq int64
	if err := s.db.QueryRow(`SELECT version, ledger_seq FROM open_ticket_projection_state WHERE id = 1`).
		Scan(&version, &ledgerSeq); err != nil {
		t.Fatalf("读升级后水位: %v", err)
	}
	if version != openTicketProjectionVersion || ledgerSeq != expectMirrorSeq {
		t.Fatalf("升级后水位不符: version=%d ledger_seq=%d，期望 v%d seq=%d", version, ledgerSeq, openTicketProjectionVersion, expectMirrorSeq)
	}
	events, err := s.RoomMessagesBeforeContext(context.Background(), fx.RoomID, 0, 200)
	if err != nil || len(events) != 2 {
		t.Fatalf("升级后房间历史应 2 行: rows=%d err=%v", len(events), err)
	}
	asc := true
	for i := 1; i < len(events); i++ {
		asc = asc && events[i-1].Seq < events[i].Seq
	}
	if !asc {
		t.Fatalf("升级后房间历史应升序: %+v", events)
	}
	cands, err := s.SessionMessageCandidates(fx.Member, 0, 0, 100)
	if err != nil || len(cands) != 1 {
		t.Fatalf("升级后候选读应恰命中提及行 1 条: rows=%d err=%v", len(cands), err)
	}
	cursor, err := s.SessionDeliveryCursor(fx.Member)
	if err != nil || cursor != 1 {
		t.Fatalf("升级不得重置交付游标: cursor=%d err=%v", cursor, err)
	}
	t.Logf("B409_U7_UPGRADE dialect=%s canonical_rows=%d open_tickets=%d ledger_seq=%d", s.dialectLabel(), gotRows, len(tickets), ledgerSeq)
}

func (s *Store) dialectLabel() string {
	if s.dialect == dialectPG {
		return "postgres"
	}
	return "sqlite"
}

// TestB409U7PreProjectionUpgradeSQLite 用 a7983e9c 冻结 schema 夹具在临时
// SQLite 文件上重建旧库，载入 canonical 事件后经真实 Open 升级。
func TestB409U7PreProjectionUpgradeSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-ledger.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("打开旧库文件: %v", err)
	}
	execB409FixtureDDL(t, db, filepath.Join("testdata", "b409-preprojection-schema-sqlite.sql"))
	fx := b409LegacyFixture{CardID: "B777001", Workflow: "bug", RoomID: "session:legacy-1",
		Member: "agent:legacy-u7", Target1: "legacy-target-u7", Task1: "legacy-task-u7",
		TicketOpen: "legacy-open", TicketGone: "legacy-gone"}
	seedB409LegacyRows(t, db, false, fx)
	if err := db.Close(); err != nil {
		t.Fatalf("关闭旧库句柄: %v", err)
	}
	// 升级前 oracle：旧库无投影表，预期从已写入的 canonical 行独立推导。
	upgraded, err := Open(path)
	if err != nil {
		t.Fatalf("真实 Open 升级旧库: %v", err)
	}
	t.Cleanup(func() { upgraded.Close() })
	// 源 seq 1/2/3 均合格（card 非空、镜像身份齐备），水位=3（消息/消费行不计）。
	verifyB409UpgradedLedger(t, upgraded, fx, 6, 5)
}

// TestB409U7PreProjectionUpgradePostgres 同一夹具在专用 PG 的独立 schema
// 命名空间内重建旧库并经真实 Open 升级；库名守卫先于任何写入。
func TestB409U7PreProjectionUpgradePostgres(t *testing.T) {
	dsn := os.Getenv("LEDGER_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未设 LEDGER_TEST_PG_DSN，跳过 PG 旧库升级验收")
	}
	requireB409IsolatedPGName(t, dsn)
	schema := fmt.Sprintf("b409_preproj_%d", time.Now().UnixNano())
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("连接隔离 PG: %v", err)
	}
	if _, err := admin.Exec(fmt.Sprintf(`CREATE SCHEMA %s`, schema)); err != nil {
		admin.Close()
		t.Fatalf("建隔离 schema: %v", err)
	}
	t.Cleanup(func() {
		// schema 清理与连接关闭同处一个 cleanup：defer 会先于 t.Cleanup 执行，
		// 不能依赖 defer 的连接存活。
		if _, err := admin.Exec(fmt.Sprintf(`DROP SCHEMA %s CASCADE`, schema)); err != nil {
			t.Errorf("清理隔离 schema %s: %v", schema, err)
		}
		if err := admin.Close(); err != nil {
			t.Errorf("关闭隔离 PG 管理连接: %v", err)
		}
	})
	legacyDSN := dsn + "&search_path=" + schema
	legacy, err := sql.Open("pgx", legacyDSN)
	if err != nil {
		t.Fatalf("按 schema 打开旧库连接: %v", err)
	}
	var verifiedSchema string
	if err := legacy.QueryRow(`SELECT current_schema()`).Scan(&verifiedSchema); err != nil || verifiedSchema != schema {
		legacy.Close()
		t.Fatalf("search_path 未生效: current_schema=%q err=%v", verifiedSchema, err)
	}
	execB409FixtureDDL(t, legacy, filepath.Join("testdata", "b409-preprojection-schema-pg.sql"))
	fx := b409LegacyFixture{CardID: "B777002", Workflow: "bug", RoomID: "session:legacy-pg-1",
		Member: "agent:legacy-u7-pg", Target1: "legacy-target-u7-pg", Task1: "legacy-task-u7-pg",
		TicketOpen: "legacy-open-pg", TicketGone: "legacy-gone-pg"}
	seedB409LegacyRows(t, legacy, true, fx)
	if err := legacy.Close(); err != nil {
		t.Fatalf("关闭旧库连接: %v", err)
	}
	upgraded, err := Open(legacyDSN)
	if err != nil {
		t.Fatalf("真实 Open 升级 PG 旧库: %v", err)
	}
	t.Cleanup(func() { upgraded.Close() })
	if upgraded.dialect != dialectPG {
		t.Fatalf("升级实例应为 PG 方言")
	}
	verifyB409UpgradedLedger(t, upgraded, fx, 6, 5)
}

// b409U7Snapshot 投影快照（rebuild 幂等判据）。
type b409U7Snapshot struct {
	rows      []string
	ledgerSeq int64
	openCount int64
}

func snapshotB409Projection(t *testing.T, s *Store) b409U7Snapshot {
	t.Helper()
	rows, err := s.db.Query(`SELECT card_id, source_target, source_task, ticket_id, task_type, payload
		FROM open_ticket_projection ORDER BY card_id, ticket_id, source_target, source_task`)
	if err != nil {
		t.Fatalf("快照投影行: %v", err)
	}
	defer rows.Close()
	var snap b409U7Snapshot
	for rows.Next() {
		var line string
		var cardID, target, task, ticketID, taskType, payload string
		if err := rows.Scan(&cardID, &target, &task, &ticketID, &taskType, &payload); err != nil {
			t.Fatalf("扫描投影快照: %v", err)
		}
		line = fmt.Sprintf("%s|%s|%s|%s|%s|%s", cardID, target, task, ticketID, taskType, payload)
		snap.rows = append(snap.rows, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历投影快照: %v", err)
	}
	if err := s.db.QueryRow(`SELECT ledger_seq, open_count FROM open_ticket_projection_state WHERE id = 1`).
		Scan(&snap.ledgerSeq, &snap.openCount); err != nil {
		t.Fatalf("快照投影水位: %v", err)
	}
	return snap
}

// installB409MidRebuildFault 安装「重建中途确定性失败」触发器：重建回放时对
// 指定 ticket 的投影插入即报错。安装前先拆除同名残留（前次运行中断可能留下
// 触发器），保证幂等；触发器只存在于本测试生命周期内，生产无开关。
func installB409MidRebuildFault(t *testing.T, s *Store, ticketID string) func() {
	t.Helper()
	if s.dialect == dialectSQLite {
		if _, err := s.db.Exec(`DROP TRIGGER IF EXISTS b409_u7_mid_fail`); err != nil {
			t.Fatalf("拆除 SQLite 残留触发器: %v", err)
		}
		if _, err := s.db.Exec(fmt.Sprintf(`CREATE TRIGGER b409_u7_mid_fail BEFORE INSERT ON open_ticket_projection
			WHEN NEW.ticket_id = '%s'
			BEGIN SELECT RAISE(ABORT, 'b409-u7 mid-rebuild fault injection'); END`, ticketID)); err != nil {
			t.Fatalf("安装 SQLite 中途失败触发器: %v", err)
		}
		return func() {
			if _, err := s.db.Exec(`DROP TRIGGER b409_u7_mid_fail`); err != nil {
				t.Errorf("拆除 SQLite 触发器: %v", err)
			}
		}
	}
	if _, err := s.db.Exec(`DROP TRIGGER IF EXISTS b409_u7_mid_fail ON open_ticket_projection`); err != nil {
		t.Fatalf("拆除 PG 残留触发器: %v", err)
	}
	if _, err := s.db.Exec(`DROP FUNCTION IF EXISTS b409_u7_mid_fail()`); err != nil {
		t.Fatalf("拆除 PG 残留函数: %v", err)
	}
	if _, err := s.db.Exec(`CREATE FUNCTION b409_u7_mid_fail() RETURNS trigger AS $body$
		BEGIN
			IF NEW.ticket_id = '` + ticketID + `' THEN
				RAISE EXCEPTION 'b409-u7 mid-rebuild fault injection';
			END IF;
			RETURN NEW;
		END $body$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("安装 PG 中途失败函数: %v", err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER b409_u7_mid_fail BEFORE INSERT OR UPDATE ON open_ticket_projection
		FOR EACH ROW EXECUTE FUNCTION b409_u7_mid_fail()`); err != nil {
		t.Fatalf("安装 PG 中途失败触发器: %v", err)
	}
	return func() {
		if _, err := s.db.Exec(`DROP TRIGGER b409_u7_mid_fail ON open_ticket_projection`); err != nil {
			t.Errorf("拆除 PG 触发器: %v", err)
		}
		if _, err := s.db.Exec(`DROP FUNCTION b409_u7_mid_fail()`); err != nil {
			t.Errorf("拆除 PG 函数: %v", err)
		}
	}
}

// TestB409U7RebuildIdempotentAndMidwayFailureRecovery 步骤 3 主体（两个方言）：
// rebuild 两次幂等；注入中途失败后错误可见、半成品不冒充最新、恢复后从权威
// 事件幂等补齐到预期水位。
func TestB409U7RebuildIdempotentAndMidwayFailureRecovery(t *testing.T) {
	dialects := []string{"sqlite"}
	if os.Getenv("LEDGER_TEST_PG_DSN") != "" {
		dialects = append(dialects, "postgres")
	}
	for _, dialect := range dialects {
		t.Run(dialect, func(t *testing.T) {
			var s *Store
			if dialect == "postgres" {
				s = newB409PGStore(t)
				stamp := fmt.Sprintf("%d", time.Now().UnixNano())
				t.Cleanup(func() {
					for _, stmt := range []string{
						`DELETE FROM open_ticket_projection WHERE card_id LIKE 'B409-U7-RB%'`,
						`DELETE FROM card_events WHERE card_id LIKE 'B409-U7-RB%'`,
						`DELETE FROM cards WHERE id LIKE 'B409-U7-RB%'`,
					} {
						if _, err := s.db.Exec(stmt); err != nil {
							t.Errorf("rebuild 清理执行 %q: %v", stmt, err)
						}
					}
					if err := s.rebuildOpenTicketProjection(); err != nil {
						t.Errorf("rebuild 清理后重建水位: %v", err)
					}
				})
				cardID := "B409-U7-RB-" + stamp
				if _, err := s.db.Exec(s.q(`INSERT INTO cards
					(id, title, status, priority, project, workflow_name, workflow_version, attachments, created_at, updated_at)
					VALUES (?, 'B409 U7 重建卡', ?, '中', 'u7-rebuild', 'bug', 1, '[]', ?, ?)`),
					cardID, StatusTodo, s.tval(time.Now()), s.tval(time.Now())); err != nil {
					t.Fatalf("建重建卡: %v", err)
				}
				seedB409RebuildTickets(t, s, cardID)
			} else {
				s = seedStore(t)
				card := mk(t, s, "B409 U7 重建卡")
				seedB409RebuildTickets(t, s, card.ID)
			}
			// 取本方言全部未决工单（含既有夹具），作为 oracle 基线。
			tickets, err := s.OpenTickets()
			if err != nil {
				t.Fatalf("首次读未决工单: %v", err)
			}
			sortCanonicalOpenTickets(tickets)

			if err := s.rebuildOpenTicketProjection(); err != nil {
				t.Fatalf("第一次显式 rebuild: %v", err)
			}
			snap1 := snapshotB409Projection(t, s)
			if err := s.rebuildOpenTicketProjection(); err != nil {
				t.Fatalf("第二次显式 rebuild: %v", err)
			}
			snap2 := snapshotB409Projection(t, s)
			if !reflect.DeepEqual(snap1, snap2) {
				t.Fatalf("rebuild 不幂等：两次快照不同\n一次=%+v\n两次=%+v", snap1, snap2)
			}
			again, err := s.OpenTickets()
			if err != nil {
				t.Fatalf("二次 rebuild 后 OpenTickets: %v", err)
			}
			sortCanonicalOpenTickets(again)
			if !reflect.DeepEqual(again, tickets) {
				t.Fatalf("二次 rebuild 后结果漂移: got=%+v want=%+v", again, tickets)
			}

			// 中途失败：对任一未决工单注入确定性插入失败。
			failTicket := tickets[0].TicketID
			removeFault := installB409MidRebuildFault(t, s, failTicket)
			if err := s.rebuildOpenTicketProjection(); err == nil {
				removeFault()
				t.Fatal("中途失败注入未生效：rebuild 应报错")
			}
			// 原子回滚：失败后投影与水位保持失败前快照，不暴露半成品。
			snap3 := snapshotB409Projection(t, s)
			if !reflect.DeepEqual(snap1, snap3) {
				t.Fatalf("中途失败后投影被部分改写（应整体回滚）:\n前=%+v\n后=%+v", snap1, snap3)
			}
			// 全新投影（无状态行）遇中途失败：读者必须得到显式错误而非合法空。
			if _, err := s.db.Exec(`DELETE FROM open_ticket_projection_state`); err != nil {
				t.Fatalf("删除投影状态行: %v", err)
			}
			if _, err := s.db.Exec(`DELETE FROM open_ticket_projection`); err != nil {
				t.Fatalf("删除投影行: %v", err)
			}
			if _, err := s.OpenTickets(); err == nil {
				t.Fatal("无状态 + 中途失败不得冒充合法空结果")
			}
			// 故障清除：重开读路径即自动追赶重建，从权威事件补齐到预期水位。
			removeFault()
			recovered, err := s.OpenTickets()
			if err != nil {
				t.Fatalf("故障清除后恢复读: %v", err)
			}
			sortCanonicalOpenTickets(recovered)
			if !reflect.DeepEqual(recovered, tickets) {
				t.Fatalf("恢复后与 oracle 不一致: got=%+v want=%+v", recovered, tickets)
			}
			snap4 := snapshotB409Projection(t, s)
			if !reflect.DeepEqual(snap1, snap4) {
				t.Fatalf("恢复后快照应回到基线:\n基线=%+v\n恢复=%+v", snap1, snap4)
			}
			t.Logf("B409_U7_REBUILD dialect=%s rows=%d ledger_seq=%d open_count=%d mid_fail_ticket=%s",
				dialect, len(snap1.rows), snap1.ledgerSeq, snap1.openCount, failTicket)
		})
	}
}

// seedB409RebuildTickets 以真实 AppendMirroredEvent 写入 ≥5 个未决工单（含可
// 注入失败的样本），并覆盖创建→答复→作废全生命周期。
func seedB409RebuildTickets(t *testing.T, s *Store, cardID string) {
	t.Helper()
	target := "u7-rebuild-target-" + cardID
	for i := 1; i <= 5; i++ {
		inserted, err := s.AppendMirroredEvent(cardID, MirroredEvent{Target: target, Task: fmt.Sprintf("u7-rebuild-task-%d", i),
			SourceSeq: 1, Type: evTicketQuestion, Payload: []byte(fmt.Sprintf(`{"ticket_id":"u7-rebuild-t%d"}`, i)), CreatedAt: time.Now()})
		if err != nil || !inserted {
			t.Fatalf("种重建工单 t%d: inserted=%v err=%v", i, inserted, err)
		}
	}
	// 生命周期对照：t6 创建后随即答复，不得出现在未决集。
	for _, ev := range []struct {
		seq  int64
		typ  string
		body string
	}{
		{1, evTicketCreated, `{"ticket_id":"u7-rebuild-t6"}`},
		{2, evTicketAnswered, `{"ticket_id":"u7-rebuild-t6"}`},
	} {
		if _, err := s.AppendMirroredEvent(cardID, MirroredEvent{Target: target, Task: "u7-rebuild-task-6",
			SourceSeq: ev.seq, Type: ev.typ, Payload: []byte(ev.body), CreatedAt: time.Now()}); err != nil {
			t.Fatalf("种重建生命周期事件 %d: %v", ev.seq, err)
		}
	}
}

// TestB409U7MirrorAppendAtomicOnProjectionFailure 步骤 3 的追加期中断验证
// （PG 腿；SQLite 腿已由 TestAppendMirroredEventProjectionFailureRollsBackEvent
// 覆盖）：投影失败时权威事件与投影同事务回滚，无静默分叉。
func TestB409U7MirrorAppendAtomicOnProjectionFailurePG(t *testing.T) {
	dsn := os.Getenv("LEDGER_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未设 LEDGER_TEST_PG_DSN，跳过 PG 追加原子性验收")
	}
	s := newB409PGStore(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	cardID := "B409-U7-AT-" + stamp
	if _, err := s.db.Exec(s.q(`INSERT INTO cards
		(id, title, status, priority, project, workflow_name, workflow_version, attachments, created_at, updated_at)
		VALUES (?, 'B409 U7 原子性卡', ?, '中', 'u7-atomic', 'bug', 1, '[]', ?, ?)`),
		cardID, StatusTodo, s.tval(time.Now()), s.tval(time.Now())); err != nil {
		t.Fatalf("建原子性卡: %v", err)
	}
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DELETE FROM open_ticket_projection WHERE card_id = ` + quoteB409Literal(cardID),
			`DELETE FROM card_events WHERE card_id = ` + quoteB409Literal(cardID),
			`DELETE FROM cards WHERE id = ` + quoteB409Literal(cardID),
		} {
			if _, err := s.db.Exec(stmt); err != nil {
				t.Errorf("原子性清理执行: %v", err)
			}
		}
		if err := s.rebuildOpenTicketProjection(); err != nil {
			t.Errorf("原子性清理后重建水位: %v", err)
		}
	})
	if err := s.rebuildOpenTicketProjection(); err != nil {
		t.Fatalf("样本前基线重建: %v", err)
	}
	before := snapshotB409Projection(t, s)
	removeFault := installB409MidRebuildFault(t, s, "u7-atomic")
	inserted, err := s.AppendMirroredEvent(cardID, MirroredEvent{Target: "u7-atomic-target-" + stamp, Task: "u7-atomic-task",
		SourceSeq: 1, Type: evTicketQuestion, Payload: []byte(`{"ticket_id":"u7-atomic"}`), CreatedAt: time.Now()})
	removeFault()
	if err == nil || inserted {
		t.Fatalf("投影失败时追加必须失败并报告未提交: inserted=%v err=%v", inserted, err)
	}
	var canonical int
	if err := s.db.QueryRow(s.q(`SELECT COUNT(*) FROM card_events
		WHERE source_target = ? AND source_task = ? AND source_seq = 1`),
		"u7-atomic-target-"+stamp, "u7-atomic-task").Scan(&canonical); err != nil {
		t.Fatalf("检查权威事件回滚: %v", err)
	}
	if canonical != 0 {
		t.Fatalf("投影失败后权威事件仍提交了 %d 行（应同事务回滚）", canonical)
	}
	after := snapshotB409Projection(t, s)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("投影失败后水位/行被改写（应整体回滚）:\n前=%+v\n后=%+v", before, after)
	}
	// 恢复：故障拆除后同参数追加成功且投影同步推进。
	inserted, err = s.AppendMirroredEvent(cardID, MirroredEvent{Target: "u7-atomic-target-" + stamp, Task: "u7-atomic-task",
		SourceSeq: 1, Type: evTicketQuestion, Payload: []byte(`{"ticket_id":"u7-atomic"}`), CreatedAt: time.Now()})
	if err != nil || !inserted {
		t.Fatalf("故障清除后追加应成功: inserted=%v err=%v", inserted, err)
	}
	counts, err := s.OpenTicketCounts()
	if err != nil || counts[cardID] != 1 {
		t.Fatalf("恢复后聚合计数应为 1: %v/%v", counts, err)
	}
	t.Logf("B409_U7_ATOMIC dialect=postgres card=%s", cardID)
}

// quoteB409Literal 测试清理 SQL 的受控字面量引用（仅用于本文件生成的卡号）。
func quoteB409Literal(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}
