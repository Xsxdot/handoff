// wakeconsumer_test.go —— K5 账本事件消费者的缝级测试。
//
// 职责：通过真实 ledger Facade 读取事件，锁住 task/room 映射、过滤、同卡合并、
// attach 延迟和 cursor 回退幂等。
// 边界：不复制 keystone briefing/重建规则；只观察消费者到 Wake 的真实入口。
package agentd

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

func newNoPTYAutomationEnv(t *testing.T) (*ledgerEnv, *queueTraceRunner) {
	t.Helper()
	env := newNoPTYLedgerEnv(t)
	SetupAutomationForTest(t, env.srv, env.ledger)
	return env, seedQueueCoordinator(t, env)
}

func appendMirroredForConsumer(t *testing.T, st *ledger.Store, cardID, task, typ string, seq int64, payload string) int64 {
	t.Helper()
	node := "consumer-" + task
	if err := st.RecordDispatch(cardID, ledger.DispatchSnapshot{
		Target: "consumer-target", TaskID: task, Node: node, Attempt: task,
		Branch: "cards/" + cardID + "-" + task, Purpose: ledger.PurposeReview, Actor: "test",
	}); err != nil {
		t.Fatalf("写消费者派发快照 %s: %v", task, err)
	}
	if _, err := st.AppendMirroredEvent(cardID, ledger.MirroredEvent{
		Target: "consumer-target", Task: task, Node: node, Attempt: task, SourceSeq: seq, Type: typ,
		Payload: []byte(payload), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("写镜像事件 %s: %v", typ, err)
	}
	events, err := st.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		t.Fatalf("读镜像事件 %s: %v", typ, err)
	}
	return events[len(events)-1].Seq
}

func appendWorkflowDispatchForConsumer(t *testing.T, st *ledger.Store, cardID, task, node, attempt string) {
	t.Helper()
	appendWorkflowDispatchForConsumerWithTarget(t, st, cardID, "consumer-target", task, node, attempt)
}

func appendWorkflowDispatchForConsumerWithTarget(t *testing.T, st *ledger.Store, cardID, target, task, node, attempt string) {
	t.Helper()
	if err := st.RecordDispatch(cardID, ledger.DispatchSnapshot{
		Target: target, TaskID: task, Node: node, Attempt: attempt,
		Branch: "cards/" + cardID + "-" + task, Purpose: ledger.PurposeReview, Actor: "test",
	}); err != nil {
		t.Fatalf("写工作流派发快照 %s: %v", task, err)
	}
}

func appendWorkflowMirroredForConsumer(t *testing.T, st *ledger.Store, cardID, task, node, attempt, typ string, seq int64, payload string) int64 {
	t.Helper()
	return appendWorkflowMirroredForConsumerWithTarget(t, st, cardID, "consumer-target", task, node, attempt, typ, seq, payload)
}

func appendWorkflowMirroredForConsumerWithTarget(t *testing.T, st *ledger.Store, cardID, target, task, node, attempt, typ string, seq int64, payload string) int64 {
	t.Helper()
	if _, err := st.AppendMirroredEvent(cardID, ledger.MirroredEvent{
		Target: target, Task: task, Node: node, Attempt: attempt,
		SourceSeq: seq, Type: typ, Payload: []byte(payload), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("写工作流镜像事件 %s: %v", typ, err)
	}
	events, err := st.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		t.Fatalf("读工作流镜像事件 %s: %v", typ, err)
	}
	return events[len(events)-1].Seq
}

func appendRawMirroredWithoutSourceTask(t *testing.T, env *ledgerEnv, cardID, target, node, attempt string, sourceSeq int64) int64 {
	t.Helper()
	db, err := sql.Open("sqlite", env.ledgerPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode=WAL&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("打开 raw mirrored ledger: %v", err)
	}
	defer db.Close()
	payload := fmt.Sprintf(`{"node":%q,"attempt":%q,"task_type":"question","payload":{"ticket_id":"missing-source-task"}}`, node, attempt)
	result, err := db.Exec(`INSERT INTO card_events
		(card_id, type, actor, payload, source_target, source_seq, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, cardID, ledger.EvTaskMirrored, "mirror", payload, target, sourceSeq, time.Now())
	if err != nil {
		t.Fatalf("写缺 source_task 的 raw mirrored 事件: %v", err)
	}
	seq, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("读取缺 source_task 事件 seq: %v", err)
	}
	return seq
}

// deleteSeatBearingRow 用 raw sqlite 删承载行（模拟契约 §2.3 的存量态：
// 有 coordinate 席位、无承载记录）。照抄 appendRawMirroredWithoutSourceTask
// 的开库手法。
func deleteSeatBearingRow(t *testing.T, env *ledgerEnv, cardID string) {
	t.Helper()
	db, err := sql.Open("sqlite", env.ledgerPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("打开 raw ledger: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`DELETE FROM seat_bearings WHERE card_id = ?`, cardID); err != nil {
		t.Fatalf("删承载行: %v", err)
	}
}

func TestB349AutomationSourceIdentity(t *testing.T) {
	t.Run("current attempt requires source task and target", func(t *testing.T) {
		env, runner := newNoPTYAutomationEnv(t)
		cardID := createCoordCard(t, env)
		prebindConsumerSession(t, env, cardID)
		appendWorkflowDispatchForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-current", "review", "attempt-current")
		appendRawMirroredWithoutSourceTask(t, env, cardID, "target-current", "review", "attempt-current", 700)
		appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "target-current", "wrong-task", "review", "attempt-current", "question", 1, `{"ticket_id":"wrong-task"}`)
		appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "wrong-target", "attempt-current", "review", "attempt-current", "question", 2, `{"ticket_id":"wrong-target"}`)
		appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-current", "review", "attempt-old", "question", 3, `{"ticket_id":"old-attempt"}`)
		appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-current", "review", "attempt-current", "question", 999, `{"ticket_id":"current"}`)

		processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
		if err != nil || escalated || processed != 1 {
			t.Fatalf("source identity 闸 processed=%d escalated=%v err=%v，want 1/false/nil", processed, escalated, err)
		}
		_, resumes, _ := runner.snapshot()
		if len(resumes) != 1 || !strings.Contains(resumes[0], "current") {
			t.Fatalf("仅当前 source identity 应唤醒一次: %v", resumes)
		}
		for _, unwanted := range []string{"wrong-task", "wrong-target", "old-attempt"} {
			if strings.Contains(resumes[0], unwanted) {
				t.Fatalf("不匹配 source identity %q 不应进入 wake: %s", unwanted, resumes[0])
			}
		}
	})

	t.Run("latest malformed identity snapshot is ignored", func(t *testing.T) {
		env, runner := newNoPTYAutomationEnv(t)
		cardID := createCoordCard(t, env)
		prebindConsumerSession(t, env, cardID)
		appendWorkflowDispatchForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-current", "review", "attempt-current")
		appendWorkflowDispatchForConsumerWithTarget(t, env.ledger, cardID, "target-current", "task-not-attempt", "review", "attempt-new")
		appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-current", "review", "attempt-current", "question", 10, `{"ticket_id":"current-after-invalid"}`)

		processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
		if err != nil || escalated || processed != 1 {
			t.Fatalf("TaskID!=Attempt 的最新快照不应遮蔽合法快照，processed=%d escalated=%v err=%v", processed, escalated, err)
		}
		_, resumes, _ := runner.snapshot()
		if len(resumes) != 1 || !strings.Contains(resumes[0], "current-after-invalid") {
			t.Fatalf("合法快照未唤醒: %v", resumes)
		}
	})

	t.Run("snapshot scan paginates to tail", func(t *testing.T) {
		env, runner := newNoPTYAutomationEnv(t)
		cardID := createCoordCard(t, env)
		prebindConsumerSession(t, env, cardID)
		for i := 0; i < 501; i++ {
			if _, err := env.ledger.AddComment(cardID, fmt.Sprintf("audit-%d", i), "普通", "test"); err != nil {
				t.Fatalf("写分页审计事件 %d: %v", i, err)
			}
		}
		if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
			t.Fatalf("先推进审计事件游标: %v", err)
		}
		appendWorkflowDispatchForConsumerWithTarget(t, env.ledger, cardID, "", "attempt-current", "review", "attempt-current")
		appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "", "attempt-current", "review", "attempt-current", "question", 7777, `{"ticket_id":"paged"}`)

		processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
		if err != nil || escalated || processed != 1 {
			t.Fatalf("分页到尾且双空 target 应唤醒，processed=%d escalated=%v err=%v", processed, escalated, err)
		}
		_, resumes, _ := runner.snapshot()
		if len(resumes) != 1 || !strings.Contains(resumes[0], "paged") {
			t.Fatalf("分页尾部合法事件未唤醒: %v", resumes)
		}
	})
}

func appendEnvelopeForWakeconsumer(t *testing.T, st *ledger.Store, cardID, task, node, attempt string, payload []byte) []byte {
	t.Helper()
	wrote, err := st.AppendMirroredEvent(cardID, ledger.MirroredEvent{
		Target: "consumer-target", Task: task, Node: node, Attempt: attempt,
		SourceSeq: 1, Type: string(proto.EventTypeQuestion), Payload: payload, CreatedAt: time.Unix(0, 0),
	})
	if err != nil || !wrote {
		t.Fatalf("写 envelope 夹具: wrote=%v err=%v", wrote, err)
	}
	events, err := st.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		t.Fatalf("读 envelope 夹具: %v", err)
	}
	for _, event := range events {
		if event.Type == ledger.EvTaskMirrored && event.SourceTask == task && event.SourceSeq == 1 {
			return append([]byte(nil), event.Payload...)
		}
	}
	t.Fatalf("找不到 envelope 夹具: task=%s", task)
	return nil
}

func mutateWakeconsumerEnvelope(t *testing.T, raw []byte, nodePresent, attemptPresent, payloadPresent bool, nullIdentity bool) []byte {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("解 fixture envelope: %v", err)
	}
	if !nodePresent {
		delete(fields, "node")
	} else if nullIdentity {
		fields["node"] = json.RawMessage("null")
	}
	if !attemptPresent {
		delete(fields, "attempt")
	} else if nullIdentity {
		fields["attempt"] = json.RawMessage("null")
	}
	if !payloadPresent {
		delete(fields, "payload")
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("重编码 fixture envelope: %v", err)
	}
	return encoded
}

// TestB2336WakeconsumerEnvelopeJSONBoundaries uses the production decoder after
// Store.AppendMirroredEvent has emitted the real envelope.  The fixture map only
// removes legacy keys or supplies JSON null; it does not duplicate decoder logic.
func TestB2336WakeconsumerEnvelopeJSONBoundaries(t *testing.T) {
	cases := []struct {
		name                                        string
		node, attempt, payload                      string
		nodePresent, attemptPresent, payloadPresent bool
		nullIdentity                                bool
		wantNode, wantAttempt                       *string
		wantPayload                                 string
		wantErr                                     bool
	}{
		{name: "missing identity", payload: `{"ticket_id":"q1"}`, payloadPresent: true, wantPayload: `{"ticket_id":"q1"}`},
		{name: "empty strings and null payload", nodePresent: true, attemptPresent: true, payloadPresent: true, wantNode: stringPtr(""), wantAttempt: stringPtr(""), wantPayload: "null"},
		{name: "nonempty strings and object", node: "review", attempt: "attempt-1", payload: `{"ticket_id":"q1"}`, nodePresent: true, attemptPresent: true, payloadPresent: true, wantNode: stringPtr("review"), wantAttempt: stringPtr("attempt-1"), wantPayload: `{"ticket_id":"q1"}`},
		{name: "json null identity", nodePresent: true, attemptPresent: true, payloadPresent: true, nullIdentity: true, wantPayload: "null"},
		{name: "missing payload is rejected", node: "review", attempt: "attempt-2", nodePresent: true, attemptPresent: true, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newNoPTYLedgerEnv(t)
			cardID := createCoordCard(t, env)
			raw := appendEnvelopeForWakeconsumer(t, env.ledger, cardID, "task-"+tc.name,
				tc.node, tc.attempt, []byte(tc.payload))
			raw = mutateWakeconsumerEnvelope(t, raw, tc.nodePresent, tc.attemptPresent,
				tc.payloadPresent, tc.nullIdentity)
			got, err := decodeMirroredTaskEnvelope(proto.LedgerEvent{
				Seq: 1, CardID: cardID, Type: ledger.EvTaskMirrored, Payload: raw,
			})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("缺失 payload 应由生产 decoder 拒绝: envelope=%s", raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("生产 wakeconsumer decoder: %v; envelope=%s", err, raw)
			}
			if !sameOptionalString(got.Node, tc.wantNode) {
				t.Fatalf("node=%v, want=%v", got.Node, tc.wantNode)
			}
			if !sameOptionalString(got.Attempt, tc.wantAttempt) {
				t.Fatalf("attempt=%v, want=%v", got.Attempt, tc.wantAttempt)
			}
			if string(got.Payload) != tc.wantPayload {
				t.Fatalf("payload=%s, want=%s", got.Payload, tc.wantPayload)
			}
		})
	}
}

func stringPtr(value string) *string { return &value }

func sameOptionalString(got, want *string) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return *got == *want
}

func FuzzB2336WakeconsumerEnvelopeJSONRoundTrip(f *testing.F) {
	f.Add("review", "attempt-1", []byte(`{"ticket_id":"q1"}`), true, true, true)
	f.Add("", "", []byte(`null`), true, true, true)
	f.Add("review", "attempt-2", []byte(`{}`), false, true, true)
	f.Add("review", "attempt-3", []byte{}, true, true, false)

	f.Fuzz(func(t *testing.T, node, attempt string, payload []byte, nodePresent, attemptPresent, payloadPresent bool) {
		if len(payload) > 0 && !json.Valid(payload) {
			t.Skip()
		}
		env := newNoPTYLedgerEnv(t)
		cardID := createCoordCard(t, env)
		raw := appendEnvelopeForWakeconsumer(t, env.ledger, cardID, "fuzz-task", node, attempt, payload)
		raw = mutateWakeconsumerEnvelope(t, raw, nodePresent, attemptPresent, payloadPresent, false)
		got, err := decodeMirroredTaskEnvelope(proto.LedgerEvent{
			Seq: 1, CardID: cardID, Type: ledger.EvTaskMirrored, Payload: raw,
		})
		if !payloadPresent {
			if err == nil {
				t.Fatalf("缺失 payload 应由生产 decoder 拒绝: %s", raw)
			}
			return
		}
		if err != nil {
			t.Fatalf("生产 wakeconsumer decoder: %v; envelope=%s", err, raw)
		}
		if nodePresent {
			if got.Node == nil || *got.Node != node {
				t.Fatalf("node=%v, want present %q", got.Node, node)
			}
		} else if got.Node != nil {
			t.Fatalf("缺失 node 不得解码成零值 %q", *got.Node)
		}
		if attemptPresent {
			if got.Attempt == nil || *got.Attempt != attempt {
				t.Fatalf("attempt=%v, want present %q", got.Attempt, attempt)
			}
		} else if got.Attempt != nil {
			t.Fatalf("缺失 attempt 不得解码成零值 %q", *got.Attempt)
		}
		wantPayload := payload
		if len(wantPayload) == 0 {
			wantPayload = []byte("null")
		}
		if string(got.Payload) != string(wantPayload) {
			t.Fatalf("payload=%s, want=%s", got.Payload, wantPayload)
		}
	})
}

func prebindConsumerSession(t *testing.T, env *ledgerEnv, cardID string) {
	t.Helper()
	result, err := env.srv.keystone.LaunchForCard(context.Background(), cardID, "coordinate", keysclient.SessionSpec{CLI: "opencode"})
	if err != nil {
		t.Fatalf("预绑定协调者会话: %v", err)
	}
	identity, err := proto.EncodeSeatIdentity("opencode", result.SessionID)
	if err != nil {
		t.Fatalf("编码预绑定协调者席位: %v", err)
	}
	if err := env.ledger.BindSeat(cardID, identity, proto.SeatSourceCoordinate, ledger.SeatBearing{Carrier: "coord-carrier", Machine: "local"}); err != nil {
		t.Fatalf("写预绑定协调者席位: %v", err)
	}
}

func appendUserMessage(t *testing.T, st *ledger.Store, cardID, body string, bySystem bool) {
	t.Helper()
	if _, err := st.RecordRoomMessage(cardID, proto.RoomMessage{
		Room: cardID, Kind: proto.RoomMsgUser, Body: body, BySystem: bySystem,
	}, "test"); err != nil {
		t.Fatalf("写用户消息: %v", err)
	}
}

func appendPointerMessage(t *testing.T, st *ledger.Store, cardID string) {
	t.Helper()
	if _, err := st.RecordRoomMessage(cardID, proto.RoomMessage{
		Room: cardID, Kind: proto.RoomMsgPointer, Body: "协调者指针",
	}, "system:pointer"); err != nil {
		t.Fatalf("写指针消息: %v", err)
	}
}

func appendRawRoomEvent(t *testing.T, env *ledgerEnv, cardID, payload string) int64 {
	t.Helper()
	db, err := sql.Open("sqlite", env.ledgerPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode=WAL&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("打开原始 room ledger: %v", err)
	}
	defer db.Close()
	result, err := db.Exec(`INSERT INTO card_events (card_id, type, actor, payload, created_at)
		VALUES (?, ?, ?, ?, ?)`, cardID, ledger.EvRoomMessage, "test", payload, time.Now())
	if err != nil {
		t.Fatalf("写原始 room event: %v", err)
	}
	seq, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("读取原始 room event seq: %v", err)
	}
	return seq
}

func TestAutomationEventMappingThroughConsumer(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)
	for i, typ := range []string{"completed", "failed", "turn_failed", "permission_request", "question", "progress"} {
		appendMirroredForConsumer(t, env.ledger, cardID, "task-"+typ, typ, int64(i+1), `{"text":"`+typ+`"}`)
	}
	// B358.3：广播形状删除（条 30），卡房间消息不再唤醒——「用户留言」与
	// 「系统用户形状」同为无寻址的卡房间 user 消息，只标 seen。
	appendUserMessage(t, env.ledger, cardID, "用户留言", false)
	appendPointerMessage(t, env.ledger, cardID)
	appendUserMessage(t, env.ledger, cardID, "系统用户形状", true)

	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil {
		t.Fatalf("消费事件: %v", err)
	}
	if escalated {
		t.Fatal("正常事件消费不应升级人工")
	}
	if processed != 5 {
		t.Fatalf("处理唤醒事件数=%d，want 5", processed)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("同卡事件应合并为一次 Resume，实得 %d", len(resumes))
	}
	for _, want := range []string{"completed", "failed", "turn_failed", "permission_request", "question"} {
		if !strings.Contains(resumes[0], want) {
			t.Fatalf("briefing 缺 %q: %s", want, resumes[0])
		}
	}
	for _, unwanted := range []string{"progress", "用户留言", "协调者指针", "系统用户形状"} {
		if strings.Contains(resumes[0], unwanted) {
			t.Fatalf("不应唤醒/进入 briefing 的内容 %q 出现: %s", unwanted, resumes[0])
		}
	}
}

func TestB353AutomationUsesWaitDeliveryPolicy(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)
	falseTypes := []string{
		"progress", "approver_decision", "approver_disabled", "tickets_voided",
		"ticket_answered", "permission_auto_allow", "permission_reuse",
	}
	for i, typ := range falseTypes {
		appendMirroredForConsumer(t, env.ledger, cardID, "audit-"+typ, typ, int64(i+1),
			`{"kind":"audit","type":"`+typ+`"}`)
	}
	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil {
		t.Fatalf("消费策略假事件: %v", err)
	}
	if processed != 0 || escalated {
		t.Fatalf("策略假事件不应唤醒，processed=%d escalated=%v", processed, escalated)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 0 {
		t.Fatalf("策略假事件不应 Resume: %v", resumes)
	}
	events, err := env.ledger.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		t.Fatalf("读取审计事件: %v", err)
	}
	for _, typ := range falseTypes {
		found := false
		for _, event := range events {
			if event.Type == ledger.EvTaskMirrored && strings.Contains(string(event.Payload), `"task_type":"`+typ+`"`) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("策略假事件 %s 被消费过滤后不应从账本消失", typ)
		}
	}

	trueTypes := []string{"delivery_failed", "stalled", "approval_dropped", "archived"}
	for i, typ := range trueTypes {
		appendMirroredForConsumer(t, env.ledger, cardID, "action-"+typ, typ, int64(100+i),
			`{"kind":"action","type":"`+typ+`"}`)
	}
	processed, escalated, err = env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || escalated || processed != len(trueTypes) {
		t.Fatalf("策略真事件未完整唤醒，processed=%d escalated=%v err=%v", processed, escalated, err)
	}
	_, resumes, _ = runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("同卡策略真事件应合并一次 Resume，实得 %d", len(resumes))
	}
	for _, typ := range trueTypes {
		if !strings.Contains(resumes[0], typ) {
			t.Fatalf("Resume briefing 缺少策略真事件 %s: %s", typ, resumes[0])
		}
	}
}

func TestB353AutomationMapsCardActionEvents(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)
	if err := env.ledger.MarkNeedsHuman(cardID, "needs human payload", "test"); err != nil {
		t.Fatal(err)
	}
	if err := env.ledger.ClearNeedsHuman(cardID, "test"); err != nil {
		t.Fatal(err)
	}
	decision, err := env.ledger.OpenDecision(cardID, "decision body", []string{"a", "b"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.ledger.AnswerDecision(decision.ID, "a", "test"); err != nil {
		t.Fatal(err)
	}
	appendUserMessage(t, env.ledger, cardID, "真人 room payload", false)
	appendUserMessage(t, env.ledger, cardID, "系统 room payload", true)
	appendPointerMessage(t, env.ledger, cardID)
	if _, err := env.ledger.AddComment(cardID, "comment audit", "普通", "test"); err != nil {
		t.Fatal(err)
	}
	if err := env.ledger.MoveCard(cardID, ledger.StatusTodo, "", "test"); err != nil {
		t.Fatal(err)
	}

	// B389 判据收口（契约 §3.5.1）移出 needs_human；B394（b394-contract 条目 1）再移出
	// needs_cleared——processed 4→3→2，只剩 decision_opened/decision_answered 唤醒。
	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || escalated || processed != 2 {
		t.Fatalf("卡原生动作消费 processed=%d escalated=%v err=%v，want 2/nil/false", processed, escalated, err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("卡原生动作应合并一次 Resume，实得 %d", len(resumes))
	}
	// decision_opened/decision_answered 仍唤醒（B394 不动它们）；needs_cleared 不再唤醒。
	for _, want := range []string{"decision body", "answer"} {
		if !strings.Contains(resumes[0], want) {
			t.Fatalf("卡原生动作 briefing 缺少 %q: %s", want, resumes[0])
		}
	}
	if strings.Contains(resumes[0], "needs human payload") {
		t.Fatalf("needs_human 已收口，不得进入 briefing: %s", resumes[0])
	}
	for _, unwanted := range []string{"真人 room payload", "系统 room payload", "comment audit", "协调者指针", ledger.StatusTodo} {
		if strings.Contains(resumes[0], unwanted) {
			t.Fatalf("非动作事件 %q 不应进入 briefing: %s", unwanted, resumes[0])
		}
	}
}

// TestB353AutomationRoomJSONBoundaries 已整支删除（B358.3）：它直调
// automationWakeEvent 的 room 分支——该分支已移入 roomMessageWakeEvents 寻址
// 分流（plan Task 2 步骤 5.3）。by_system 解码边界由
// TestB353AutomationRoomJSONBoundariesThroughConsumer 穿消费循环覆盖
// （breakdown §3.4 ③「不许只单测映射函数」）。
func TestB353AutomationRoomJSONBoundariesThroughConsumer(t *testing.T) {
	// B358.3：广播形状删除（条 30）——所有可解码 payload（卡房间、无寻址）
	// wantProcessed=0 且零 Resume；by_system invalid 仍 wantErr（解码失败上抛）。
	cases := []struct {
		name    string
		payload string
		wantErr bool
	}{
		{name: "by_system missing", payload: `{"kind":"user","body":"missing"}`},
		{name: "by_system false", payload: `{"kind":"user","body":"false","by_system":false}`},
		{name: "by_system true", payload: `{"kind":"user","body":"true","by_system":true}`},
		{name: "by_system null", payload: `{"kind":"user","body":"null","by_system":null}`},
		{name: "by_system invalid", payload: `{"kind":"user","body":"invalid","by_system":"yes"}`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, runner := newNoPTYAutomationEnv(t)
			cardID := createCoordCard(t, env)
			prebindConsumerSession(t, env, cardID)
			seq := appendRawRoomEvent(t, env, cardID, tc.payload)
			processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
			if tc.wantErr {
				if err == nil || processed != 0 || escalated ||
					!strings.Contains(err.Error(), fmt.Sprint(seq)) {
					t.Fatalf("非法 room payload consumer seam processed=%d escalated=%v err=%v, seq=%d",
						processed, escalated, err, seq)
				}
				return
			}
			if err != nil || escalated {
				t.Fatalf("room payload consumer seam err=%v escalated=%v", err, escalated)
			}
			if processed != 0 {
				t.Fatalf("卡房间消息不唤醒（条 30），processed=%d", processed)
			}
			_, resumes, _ := runner.snapshot()
			if len(resumes) != 0 {
				t.Fatalf("广播形状已删，room payload 不应唤醒，resumes=%v", resumes)
			}
		})
	}
}

func TestB353AutomationMalformedEnvelopeFailsAtConsumerSeam(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)
	seq := appendMirroredForConsumer(t, env.ledger, cardID, "malformed", "", 1, `{"body":"missing type"}`)
	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err == nil || processed != 0 || escalated {
		t.Fatalf("缺失 task_type 应在 consumer seam 失败，processed=%d escalated=%v err=%v", processed, escalated, err)
	}
	for _, want := range []string{cardID, "task_mirrored"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("错误 %q 缺少上下文: %v", want, err)
		}
	}
	if !strings.Contains(err.Error(), fmt.Sprint(seq)) {
		t.Fatalf("错误缺少事件 seq=%d: %v", seq, err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 0 {
		t.Fatalf("坏 envelope 不得 Resume: %v", resumes)
	}
}

func TestB2336StaleAttemptDoesNotWake(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)
	appendWorkflowDispatchForConsumer(t, env.ledger, cardID, "attempt-old", "review", "attempt-old")
	appendWorkflowDispatchForConsumer(t, env.ledger, cardID, "attempt-new", "review", "attempt-new")
	appendWorkflowMirroredForConsumer(t, env.ledger, cardID, "attempt-old", "review", "attempt-old",
		"question", 1, `{"ticket_id":"old-ticket"}`)
	appendWorkflowMirroredForConsumer(t, env.ledger, cardID, "attempt-new", "review", "attempt-new",
		"question", 1, `{"ticket_id":"new-ticket"}`)
	appendWorkflowMirroredForConsumer(t, env.ledger, cardID, "task-empty", "", "",
		"question", 1, `{"ticket_id":"empty-ticket"}`)

	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil {
		t.Fatalf("消费当前/旧 attempt: %v", err)
	}
	if escalated || processed != 1 {
		t.Fatalf("仅当前 attempt 应唤醒一次，processed=%d escalated=%v", processed, escalated)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 || !strings.Contains(resumes[0], "new-ticket") {
		t.Fatalf("当前 attempt 未唤醒或内容错误: resumes=%v", resumes)
	}
	if strings.Contains(resumes[0], "old-ticket") || strings.Contains(resumes[0], "empty-ticket") {
		t.Fatalf("旧/空 attempt 不得进入 wake: %s", resumes[0])
	}
	events, err := env.ledger.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		t.Fatalf("读消费者账本: %v", err)
	}
	for _, event := range events {
		if event.Type == ledger.EvReviewVerdict || event.Type == ledger.EvAcceptanceRecorded {
			t.Fatalf("执行事件不应生成业务裁决/验收: %+v", event)
		}
	}
}

// TestB2336TerminalWakeDoesNotWriteBusinessVerdict keeps execution terminal
// facts separate from the workflow's review/acceptance facts.  The consumer
// may wake the coordinator, but it does not own either business write.
func TestB2336TerminalWakeDoesNotWriteBusinessVerdict(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)
	for i, typ := range []string{"completed", "turn_failed", "failed"} {
		appendMirroredForConsumer(t, env.ledger, cardID, "terminal-"+typ, typ, int64(i+1),
			`{"text":"`+typ+`"}`)
	}

	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil {
		t.Fatalf("消费终态事件: %v", err)
	}
	if escalated || processed != 3 {
		t.Fatalf("终态事件应各唤醒一次，processed=%d escalated=%v", processed, escalated)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("同卡终态事件应合并为一次 Resume，实得 %d", len(resumes))
	}
	for _, typ := range []string{"completed", "turn_failed", "failed"} {
		if !strings.Contains(resumes[0], typ) {
			t.Fatalf("终态 wake briefing 缺少 %q: %s", typ, resumes[0])
		}
	}
	events, err := env.ledger.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		t.Fatalf("读终态消费账本: %v", err)
	}
	for _, event := range events {
		if event.Type == ledger.EvReviewVerdict || event.Type == ledger.EvAcceptanceRecorded {
			t.Fatalf("执行终态不应伪造业务裁决/验收: %+v", event)
		}
	}
}

func TestAutomationApproverDisabledFallsBackToWake(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)
	appendMirroredForConsumer(t, env.ledger, cardID, "disabled", "approver_disabled", 1, `{"reason":"disabled"}`)
	appendMirroredForConsumer(t, env.ledger, cardID, "permission", "permission_request", 1, `{"permission":"go test"}`)

	processed, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil {
		t.Fatalf("消费 disabled 后权限事件: %v", err)
	}
	if processed != 1 {
		t.Fatalf("permission_request 应处理 1 条，实得 %d", processed)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 || !strings.Contains(resumes[0], "permission_request") {
		t.Fatalf("disabled 后 permission 未唤醒一次: resumes=%v", resumes)
	}
}

func TestAutomationCursorRewindIsIdempotent(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)
	appendMirroredForConsumer(t, env.ledger, cardID, "terminal", "completed", 1, `{"text":"done"}`)

	processed, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || processed != 1 {
		t.Fatalf("首次消费 processed=%d err=%v，want 1/nil", processed, err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("首次应 Resume 一次，实得 %d", len(resumes))
	}
	env.srv.automationMu.Lock()
	env.srv.automationCursor = 0
	env.srv.automationMu.Unlock()
	processed, _, err = env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || processed != 0 {
		t.Fatalf("游标回退后 processed=%d err=%v，want 0/nil", processed, err)
	}
	_, resumes, _ = runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("游标回退不应二次 Resume，实得 %d", len(resumes))
	}
}

func TestAutomationAttachDefersAndThenWakes(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)
	cursorPath := filepath.Join(env.srv.conf().DataDir, "automation-cursor.json")
	appendMirroredForConsumer(t, env.ledger, cardID, "terminal", "completed", 1, `{"text":"done"}`)
	events, err := env.ledger.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	var terminalSeq int64
	for _, ev := range events {
		if ev.Type == ledger.EvTaskMirrored {
			terminalSeq = ev.Seq
		}
	}
	env.srv.keystone.SetAttach(cardID, true)
	processed, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || processed != 0 {
		t.Fatalf("attach 中 processed=%d err=%v，want 0/nil", processed, err)
	}
	env.srv.automationMu.Lock()
	cursor := env.srv.automationCursor
	env.srv.automationMu.Unlock()
	if cursor >= terminalSeq {
		t.Fatalf("attach 暂缓却推进 cursor=%d 到事件=%d", cursor, terminalSeq)
	}
	if _, err := os.Stat(cursorPath); !os.IsNotExist(err) {
		t.Fatalf("attach 暂缓不应写 cursor 文件，stat err=%v", err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 0 {
		t.Fatalf("attach 中不应 Resume，实得 %d", len(resumes))
	}
	env.srv.keystone.SetAttach(cardID, false)
	processed, _, err = env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || processed != 1 {
		t.Fatalf("解除 attach 后 processed=%d err=%v，want 1/nil", processed, err)
	}
	_, resumes, _ = runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("解除 attach 后应 Resume 一次，实得 %d", len(resumes))
	}
	if _, err := os.Stat(cursorPath); err != nil {
		t.Fatalf("解除 attach 后应持久化 cursor: %v", err)
	}
}

type fallbackConsumerRunner struct {
	mu         sync.Mutex
	launches   int
	resumes    int
	failLaunch bool
	failResume bool
}

func (r *fallbackConsumerRunner) Launch(keysclient.SessionSpec, string) (keysclient.TurnResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.launches++
	if r.failLaunch {
		return keysclient.TurnResult{}, errors.New("launch failed")
	}
	return keysclient.TurnResult{SessionID: "fallback-session"}, nil
}

func (r *fallbackConsumerRunner) Resume(keysclient.SessionRef, string) (keysclient.TurnResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resumes++
	if r.failResume {
		return keysclient.TurnResult{}, errors.New("resume failed")
	}
	return keysclient.TurnResult{SessionID: "fallback-session"}, nil
}

func TestAutomationFallbackResumeRebuildFailure(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	SetupAutomationForTest(t, env.srv, env.ledger)
	allowCarrierMachines(t, env.srv, "ftm")
	putOnlineCarrier(t, mustScheduling(t, env.srv), scheduling.Carrier{
		Name: "coord-carrier", Machine: "ftm", CLI: "opencode",
		HomeDir: "/tmp/coord-home", Credential: scheduling.CredentialStandalone,
		Status: scheduling.StatusOnline,
	})
	if err := mustScheduling(t, env.srv).PutSquad(scheduling.Squad{
		Name: "coord", Role: scheduling.RoleCoordinator, Members: []scheduling.SquadMember{{Carrier: "coord-carrier", MaxConcurrency: 1}},
	}, 0); err != nil {
		t.Fatal(err)
	}
	runner := &fallbackConsumerRunner{}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	cardID := createCoordCard(t, env)
	result, err := env.srv.keystone.LaunchForCard(context.Background(), cardID, "coordinate", keysclient.SessionSpec{CLI: "opencode"})
	if err != nil {
		t.Fatalf("建立既有会话: %v", err)
	}
	identity, err := proto.EncodeSeatIdentity("opencode", result.SessionID)
	if err != nil {
		t.Fatalf("编码既有会话席位: %v", err)
	}
	if err := env.ledger.BindSeat(cardID, identity, proto.SeatSourceCoordinate, ledger.SeatBearing{Carrier: "coord-carrier", Machine: "local"}); err != nil {
		t.Fatalf("写既有会话席位: %v", err)
	}
	runner.failResume = true
	runner.failLaunch = true
	appendMirroredForConsumer(t, env.ledger, cardID, "terminal", "completed", 1, `{"text":"done"}`)

	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err == nil || processed != 0 || !escalated {
		t.Fatalf("resume/rebuild 双失败 processed=%d escalated=%v err=%v", processed, escalated, err)
	}
	if runner.resumes != 1 || runner.launches != 2 {
		t.Fatalf("兜底调用次数 resume=%d launch=%d，want 1/2", runner.resumes, runner.launches)
	}
	events, err := env.ledger.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	foundNeeds := false
	for _, ev := range events {
		if ev.Type == ledger.EvNeedsHuman && strings.Contains(string(ev.Payload), "resume") && strings.Contains(string(ev.Payload), "重建") {
			foundNeeds = true
		}
	}
	if !foundNeeds {
		t.Fatalf("缺少含 resume/重建 原因的 needs_human 事件")
	}
}

// TestAutomationWakeFailureAdvancesCursor 唤醒失败必须推进 cursor，不得对
// 同一条用户消息无限 Launch（B274：空 spec + 失败不推 cursor + kick = 指针洪流）。
func TestAutomationWakeFailureAdvancesCursor(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	SetupAutomationForTest(t, env.srv, env.ledger)
	allowCarrierMachines(t, env.srv, "ftm")
	putOnlineCarrier(t, mustScheduling(t, env.srv), scheduling.Carrier{
		Name: "coord-carrier", Machine: "ftm", CLI: "opencode",
		HomeDir: "/tmp/coord-home", Credential: scheduling.CredentialStandalone,
		Status: scheduling.StatusOnline,
	})
	if err := mustScheduling(t, env.srv).PutSquad(scheduling.Squad{
		Name: "coord", Role: scheduling.RoleCoordinator, Members: []scheduling.SquadMember{{Carrier: "coord-carrier", MaxConcurrency: 1}},
	}, 0); err != nil {
		t.Fatal(err)
	}
	runner := &fallbackConsumerRunner{failLaunch: true}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	cardID := createCoordCard(t, env)
	runner.failLaunch = false
	// B358.3：广播形状删除（条 30），失败升级路径需要一次真实寻址命中——
	// 夹具从卡房间 appendUserMessage 改为会话发言 @卡号（mustWakeSessionFixture
	// 内含预绑定席位，坐席 Launch 需 failLaunch=false，故在置回失败位之前）。
	sessionID, svc := mustWakeSessionFixture(t, env, cardID)
	runner.failLaunch = true
	runner.failResume = true
	sendSessionMessage(t, svc, sessionID, "user:tester", "用户留言", []string{cardID}, 0)

	_, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err == nil {
		t.Fatal("无绑定且 Launch 失败时期望 err")
	}
	first := runner.launches
	if first < 1 {
		t.Fatal("至少应尝试一次 Launch")
	}
	env.srv.automationMu.Lock()
	cursor := env.srv.automationCursor
	env.srv.automationMu.Unlock()
	if cursor == 0 {
		t.Fatal("唤醒失败后 cursor 仍为 0，同一条消息会被再消费")
	}

	_, _, _ = env.srv.consumeAutomationEventsOnce(context.Background())
	if runner.launches != first {
		t.Fatalf("失败后不得对同一条再 Launch：%d → %d", first, runner.launches)
	}
}

func TestB349AutomationCursorPersistence(t *testing.T) {
	t.Run("persists and resumes from the same DataDir", func(t *testing.T) {
		env, runner := newNoPTYAutomationEnv(t)
		cardID := createCoordCard(t, env)
		prebindConsumerSession(t, env, cardID)
		cursorPath := filepath.Join(env.srv.conf().DataDir, "automation-cursor.json")
		roomPath := filepath.Join(env.srv.conf().DataDir, "room-cursors.json")
		roomBefore, roomBeforeErr := os.ReadFile(roomPath)
		if _, err := os.Stat(cursorPath); !os.IsNotExist(err) {
			t.Fatalf("首次装配前 cursor 应不存在，stat err=%v", err)
		}
		appendMirroredForConsumer(t, env.ledger, cardID, "terminal", "completed", 1, `{"text":"first"}`)
		processed, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
		if err != nil || processed != 1 {
			t.Fatalf("首次消费 processed=%d err=%v，want 1/nil", processed, err)
		}
		raw, err := os.ReadFile(cursorPath)
		if err != nil {
			t.Fatalf("消费后应存在 cursor 文件: %v", err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatalf("cursor JSON: %v", err)
		}
		if len(fields) != 1 {
			t.Fatalf("cursor 只能包含 seq，fields=%s", raw)
		}
		if _, ok := fields["seq"]; !ok {
			t.Fatalf("cursor 缺少 seq: %s", raw)
		}
		info, err := os.Stat(cursorPath)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("cursor 权限=%v err=%v，want 0600", info.Mode().Perm(), err)
		}
		if roomAfter, roomAfterErr := os.ReadFile(roomPath); errors.Is(roomAfterErr, os.ErrNotExist) != errors.Is(roomBeforeErr, os.ErrNotExist) || string(roomAfter) != string(roomBefore) {
			t.Fatalf("room cursor 不应被自动化 cursor 改写，before=(%v,%q) after=(%v,%q)", roomBeforeErr, roomBefore, roomAfterErr, roomAfter)
		}

		resumed := NewServer(env.srv.conf(), env.st, discardLogger())
		resumed.SetLedger(env.ledger)
		SetupAutomationForTest(t, resumed, env.ledger)
		resumed.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, resumed.autoLedger, attachLocator{}))
		processed, _, err = resumed.consumeAutomationEventsOnce(context.Background())
		if err != nil || processed != 0 {
			t.Fatalf("新 Server 读回已保存 cursor 后不应重放，processed=%d err=%v", processed, err)
		}
		appendMirroredForConsumer(t, env.ledger, cardID, "terminal-next", "completed", 2, `{"text":"second"}`)
		processed, _, err = resumed.consumeAutomationEventsOnce(context.Background())
		if err != nil || processed != 1 {
			t.Fatalf("cursor 后新事件应续拉，processed=%d err=%v", processed, err)
		}
		_, resumes, _ := runner.snapshot()
		if len(resumes) != 2 {
			t.Fatalf("首次事件与 cursor 后新事件各应唤醒一次，resumes=%v", resumes)
		}
	})

	t.Run("corrupt or unreadable cursor starts at zero", func(t *testing.T) {
		t.Run("corrupt json", func(t *testing.T) {
			env, runner := newNoPTYAutomationEnv(t)
			cardID := createCoordCard(t, env)
			prebindConsumerSession(t, env, cardID)
			appendMirroredForConsumer(t, env.ledger, cardID, "terminal-corrupt", "completed", 1, `{"text":"corrupt-cursor"}`)
			cursorPath := filepath.Join(env.srv.conf().DataDir, "automation-cursor.json")
			if err := os.WriteFile(cursorPath, []byte(`{"seq":`), 0o600); err != nil {
				t.Fatalf("写损坏 cursor: %v", err)
			}

			resumed := NewServer(env.srv.conf(), env.st, discardLogger())
			resumed.SetLedger(env.ledger)
			SetupAutomationForTest(t, resumed, env.ledger)
			resumed.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, resumed.autoLedger, attachLocator{}))
			if resumed.automationCursor != 0 {
				t.Fatalf("损坏 cursor 不应装配为已保存水位: %d", resumed.automationCursor)
			}
			processed, _, err := resumed.consumeAutomationEventsOnce(context.Background())
			if err != nil || processed != 1 {
				t.Fatalf("损坏 cursor 以 0 启动后应重试事件，processed=%d err=%v", processed, err)
			}
			_, resumes, _ := runner.snapshot()
			if len(resumes) != 1 {
				t.Fatalf("损坏 cursor 不应跳过未消费事件，resumes=%v", resumes)
			}
		})

		t.Run("unreadable directory", func(t *testing.T) {
			env, runner := newNoPTYAutomationEnv(t)
			cardID := createCoordCard(t, env)
			prebindConsumerSession(t, env, cardID)
			appendMirroredForConsumer(t, env.ledger, cardID, "terminal-unreadable", "completed", 1, `{"text":"unreadable-cursor"}`)
			cursorPath := filepath.Join(env.srv.conf().DataDir, "automation-cursor.json")
			if err := os.Mkdir(cursorPath, 0o700); err != nil {
				t.Fatalf("制造不可读 cursor 夹具: %v", err)
			}

			resumed := NewServer(env.srv.conf(), env.st, discardLogger())
			resumed.SetLedger(env.ledger)
			SetupAutomationForTest(t, resumed, env.ledger)
			resumed.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, resumed.autoLedger, attachLocator{}))
			if resumed.automationCursor != 0 {
				t.Fatalf("不可读 cursor 不应装配为已保存水位: %d", resumed.automationCursor)
			}
			processed, _, err := resumed.consumeAutomationEventsOnce(context.Background())
			if err == nil || processed != 1 {
				t.Fatalf("不可读 cursor 仍应先重试事件并暴露保存错误，processed=%d err=%v", processed, err)
			}
			_, resumes, _ := runner.snapshot()
			if len(resumes) != 1 {
				t.Fatalf("不可读 cursor 不应跳过未消费事件，resumes=%v", resumes)
			}
		})
	})

	t.Run("save failure keeps memory ahead without faking disk waterline", func(t *testing.T) {
		env, runner := newNoPTYAutomationEnv(t)
		cardID := createCoordCard(t, env)
		prebindConsumerSession(t, env, cardID)
		cursorPath := filepath.Join(env.srv.conf().DataDir, "automation-cursor.json")
		if err := os.Mkdir(cursorPath, 0o700); err != nil {
			t.Fatalf("制造 cursor rename 失败夹具: %v", err)
		}
		appendMirroredForConsumer(t, env.ledger, cardID, "terminal", "completed", 1, `{"text":"save-failure"}`)
		processed, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
		if err == nil || processed != 1 {
			t.Fatalf("Save 失败应保留消费结果并返回错误，processed=%d err=%v", processed, err)
		}
		env.srv.automationMu.Lock()
		memoryCursor := env.srv.automationCursor
		env.srv.automationMu.Unlock()
		if memoryCursor == 0 {
			t.Fatal("Save 失败后内存 cursor 不应回滚")
		}
		info, statErr := os.Stat(cursorPath)
		if statErr != nil || !info.IsDir() {
			t.Fatalf("Save 失败不应伪造主 cursor 文件，stat=%v err=%v", info, statErr)
		}
		if _, err := os.Stat(cursorPath + ".tmp"); !os.IsNotExist(err) {
			t.Fatalf("Save 失败后临时文件应清理，stat err=%v", err)
		}
		_, resumes, _ := runner.snapshot()
		if len(resumes) != 1 {
			t.Fatalf("Save 失败前仍应只唤醒一次，resumes=%v", resumes)
		}

		if err := os.Remove(cursorPath); err != nil {
			t.Fatalf("移除 Save 失败夹具目录: %v", err)
		}
		resumed := NewServer(env.srv.conf(), env.st, discardLogger())
		resumed.SetLedger(env.ledger)
		SetupAutomationForTest(t, resumed, env.ledger)
		resumed.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, resumed.autoLedger, attachLocator{}))
		processed, _, err = resumed.consumeAutomationEventsOnce(context.Background())
		// B389 §3.1：事件已在首轮认领并 CompleteWake（done_at 非空），即使 cursor
		// 未落盘也不得重放——认领表是 exactly-once 的权威，重试会重复唤醒。
		if err != nil || processed != 0 {
			t.Fatalf("已完成认领的事件不得重放，processed=%d err=%v，want 0/nil", processed, err)
		}
		if _, err := os.Stat(cursorPath); err != nil {
			t.Fatalf("新 Server 读回后应保存收窄后的水位: %v", err)
		}
		_, resumes, _ = runner.snapshot()
		if len(resumes) != 1 {
			t.Fatalf("已完成认领的事件不得再次唤醒，resumes=%v", resumes)
		}
	})
}

func appendRawMirroredWithSource(t *testing.T, env *ledgerEnv, cardID, target, task, payload string, sourceSeq int64) int64 {
	t.Helper()
	db, err := sql.Open("sqlite", env.ledgerPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("打开 raw mirrored ledger: %v", err)
	}
	defer db.Close()
	result, err := db.Exec(`INSERT INTO card_events
		(card_id, type, actor, payload, source_target, source_task, source_seq, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		cardID, ledger.EvTaskMirrored, "mirror", payload, target, task, sourceSeq, time.Now())
	if err != nil {
		t.Fatalf("写 raw mirrored 事件: %v", err)
	}
	seq, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("读取 raw mirrored seq: %v", err)
	}
	return seq
}

func TestB370AutomationDegradedIdentity(t *testing.T) {
	cases := []struct {
		name          string
		setup         func(t *testing.T, env *ledgerEnv, cardID string)
		wantProcessed int
		wantWake      string // 非空表示 expects 一次 Resume 且 briefing 含该子串
	}{
		{
			name: "missing keys with source equal current dispatch wakes",
			setup: func(t *testing.T, env *ledgerEnv, cardID string) {
				appendWorkflowDispatchForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-current", "review", "attempt-current")
				appendRawMirroredWithSource(t, env, cardID, "target-current", "attempt-current",
					`{"task_type":"question","payload":{"ticket_id":"legacy-missing-keys"}}`, 1)
			},
			wantProcessed: 1,
			wantWake:      "legacy-missing-keys",
		},
		{
			name: "empty string keys with source equal current dispatch wakes",
			setup: func(t *testing.T, env *ledgerEnv, cardID string) {
				appendWorkflowDispatchForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-current", "review", "attempt-current")
				appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-current", "", "", "question", 2, `{"ticket_id":"empty-keys"}`)
			},
			wantProcessed: 1,
			wantWake:      "empty-keys",
		},
		{
			name: "snapshot without workflow identity wakes",
			setup: func(t *testing.T, env *ledgerEnv, cardID string) {
				appendWorkflowDispatchForConsumerWithTarget(t, env.ledger, cardID, "target-current", "task-no-identity", "", "")
				appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "target-current", "task-no-identity", "", "", "question", 3, `{"ticket_id":"no-identity-snapshot"}`)
			},
			wantProcessed: 1,
			wantWake:      "no-identity-snapshot",
		},
		{
			name: "no dispatch snapshot wakes",
			setup: func(t *testing.T, env *ledgerEnv, cardID string) {
				appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-current", "", "", "question", 4, `{"ticket_id":"no-dispatch"}`)
			},
			wantProcessed: 1,
			wantWake:      "no-dispatch",
		},
		{
			name: "empty identity source mismatch is blocked",
			setup: func(t *testing.T, env *ledgerEnv, cardID string) {
				appendWorkflowDispatchForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-new", "review", "attempt-new")
				appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-old", "", "", "question", 5, `{"ticket_id":"stale-empty"}`)
			},
			wantProcessed: 0,
		},
		{
			name: "nonempty identity equal current dispatch wakes",
			setup: func(t *testing.T, env *ledgerEnv, cardID string) {
				appendWorkflowDispatchForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-current", "review", "attempt-current")
				appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-current", "review", "attempt-current", "question", 6, `{"ticket_id":"current-identity"}`)
			},
			wantProcessed: 1,
			wantWake:      "current-identity",
		},
		{
			name: "nonempty old attempt is blocked",
			setup: func(t *testing.T, env *ledgerEnv, cardID string) {
				appendWorkflowDispatchForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-new", "review", "attempt-new")
				appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-new", "review", "attempt-old", "question", 7, `{"ticket_id":"stale-identity"}`)
			},
			wantProcessed: 0,
		},
		{
			name: "delivery policy false is blocked after identity passes",
			setup: func(t *testing.T, env *ledgerEnv, cardID string) {
				appendWorkflowDispatchForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-current", "review", "attempt-current")
				appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-current", "review", "attempt-current", "permission_auto_allow", 7, `{"rule":"safe"}`)
			},
			wantProcessed: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, runner := newNoPTYAutomationEnv(t)
			cardID := createCoordCard(t, env)
			prebindConsumerSession(t, env, cardID)
			tc.setup(t, env, cardID)
			processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
			if err != nil || escalated || processed != tc.wantProcessed {
				t.Fatalf("processed=%d escalated=%v err=%v，want %d/false/nil", processed, escalated, err, tc.wantProcessed)
			}
			_, resumes, _ := runner.snapshot()
			if tc.wantWake == "" {
				if len(resumes) != 0 {
					t.Fatalf("不应唤醒，resumes=%v", resumes)
				}
				return
			}
			if len(resumes) != 1 || !strings.Contains(resumes[0], tc.wantWake) {
				t.Fatalf("应唤醒一次且含 %q，resumes=%v", tc.wantWake, resumes)
			}
		})
	}
}

func TestB370AutomationBlockedEventSeenAndContinues(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)
	appendWorkflowDispatchForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-new", "review", "attempt-new")
	blockedSeq := appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-new", "review", "attempt-old", "question", 1, `{"ticket_id":"stale"}`)

	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || escalated || processed != 0 {
		t.Fatalf("被拦事件 processed=%d escalated=%v err=%v，want 0/false/nil", processed, escalated, err)
	}
	env.srv.automationMu.Lock()
	_, seen := env.srv.automationSeen[blockedSeq]
	cursor := env.srv.automationCursor
	env.srv.automationMu.Unlock()
	if !seen {
		t.Fatalf("被拦事件 seq=%d 应记 seen", blockedSeq)
	}
	if cursor < blockedSeq {
		t.Fatalf("被拦事件应推进游标：cursor=%d seq=%d", cursor, blockedSeq)
	}

	appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-new", "review", "attempt-new", "question", 2, `{"ticket_id":"current-after-block"}`)
	processed, escalated, err = env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || escalated || processed != 1 {
		t.Fatalf("被拦后消费循环应继续，processed=%d escalated=%v err=%v", processed, escalated, err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 || !strings.Contains(resumes[0], "current-after-block") {
		t.Fatalf("后续合法事件未唤醒: %v", resumes)
	}
}

func splitCoordChild(t *testing.T, env *ledgerEnv, parentID string) string {
	t.Helper()
	child, err := env.ledger.SplitCard(parentID, "子卡工单", "test")
	if err != nil {
		t.Fatalf("拆子卡: %v", err)
	}
	return child.ID
}

func TestAutomationWakeBubblesChildTicketToParentCoordinate(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	parentID := createCoordCard(t, env)
	prebindConsumerSession(t, env, parentID)
	childID := splitCoordChild(t, env, parentID)
	appendMirroredForConsumer(t, env.ledger, childID, "child-task", "permission_request", 1, `{"ticket_id":"tk-child"}`)

	processed, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil {
		t.Fatalf("消费子卡工单: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed=%d want 1", processed)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("应 Resume 父卡一次，实得 %d %v", len(resumes), resumes)
	}
	if !strings.Contains(resumes[0], "\n- 卡号："+parentID+"\n") {
		t.Fatalf("briefing 应是父卡 %s: %s", parentID, resumes[0])
	}
	if strings.Contains(resumes[0], "\n- 卡号："+childID+"\n") {
		t.Fatalf("不应把子卡当唤醒目标: %s", resumes[0])
	}
	if !strings.Contains(resumes[0], "permission_request") {
		t.Fatalf("briefing 缺 permission_request: %s", resumes[0])
	}
}

func TestAutomationWakeDoesNotBubbleWhenChildHasCoordinateSeat(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	parentID := createCoordCard(t, env)
	prebindConsumerSession(t, env, parentID)
	childID := splitCoordChild(t, env, parentID)
	prebindConsumerSession(t, env, childID)
	appendMirroredForConsumer(t, env.ledger, childID, "child-task", "permission_request", 1, `{"ticket_id":"tk-child"}`)

	processed, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil {
		t.Fatalf("消费: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed=%d want 1", processed)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("只应叫醒子卡一次，实得 %d %v", len(resumes), resumes)
	}
	if !strings.Contains(resumes[0], "\n- 卡号："+childID+"\n") {
		t.Fatalf("应叫醒子卡 %s: %s", childID, resumes[0])
	}
	if strings.Contains(resumes[0], "\n- 卡号："+parentID+"\n") {
		t.Fatalf("子卡有席位时不应叫醒父卡: %s", resumes[0])
	}
}

func TestAutomationWakeDoesNotWakeBindParent(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	parentID := createCoordCard(t, env)
	if err := env.ledger.BindSeat(parentID, "cli:grok#bind-parent", proto.SeatSourceBind, ledger.SeatBearing{}); err != nil {
		t.Fatalf("父卡 bind: %v", err)
	}
	childID := splitCoordChild(t, env, parentID)
	appendMirroredForConsumer(t, env.ledger, childID, "child-task", "permission_request", 1, `{"ticket_id":"tk-child"}`)

	_, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil {
		t.Fatalf("消费: %v", err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 0 {
		t.Fatalf("父卡 bind 不应自动 Resume，实得 %v", resumes)
	}
}

// TestB389NeedsHumanDoesNotWake 锁 §4-18：needs_human 不再触发协调者唤醒
// （resumes/launches 为 0，processed 为 0）。
func TestB389NeedsHumanDoesNotWake(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)
	if err := env.ledger.MarkNeedsHuman(cardID, "需要人处理", "test"); err != nil {
		t.Fatal(err)
	}
	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || escalated {
		t.Fatalf("消费失败: err=%v escalated=%v", err, escalated)
	}
	if processed != 0 {
		t.Fatalf("needs_human 不应被消费为唤醒，processed=%d", processed)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 0 {
		t.Fatalf("needs_human 不得唤醒协调者，resumes=%v", resumes)
	}
}

// TestB389TerminalCardDoesNotWake 锁 §4-32：MoveCard 到终态（不清席位，D2 边界）
// 后收到任意事件都不再唤醒，且不占名额。
func TestB389TerminalCardDoesNotWake(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	seedAgentdLedger(t, env.ledger, "bug")
	card, err := env.ledger.CreateCard(ledger.NewCard{
		Title: "终态卡", Project: "handoff", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	prebindConsumerSession(t, env, card.ID) // 席位 + 承载 coord-carrier/local
	for _, to := range []string{ledger.StatusDoing, ledger.StatusReview, ledger.StatusDone} {
		if err := env.ledger.MoveCard(card.ID, to, "", "test"); err != nil {
			t.Fatalf("移列到 %s: %v", to, err)
		}
	}
	if got, _ := env.ledger.GetCard(card.ID); got.Status != ledger.StatusDone || got.DriverSession == "" {
		t.Fatalf("前置条件：终态卡仍带席位，status=%q session=%q", got.Status, got.DriverSession)
	}
	appendMirroredForConsumer(t, env.ledger, card.ID, "terminal", "question", 1, `{"ticket_id":"tk"}`)
	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
		t.Fatalf("消费: %v", err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 0 {
		t.Fatalf("终态卡不得唤醒，resumes=%v", resumes)
	}
	for _, key := range []string{"squad/coord/coord-carrier", "carrier/coord-carrier"} {
		if got := runningCountIn(t, env.srv.autoLedger, key); got != 0 {
			t.Fatalf("终态卡不得占名额 %s=%d", key, got)
		}
	}
}

// TestB389ClaimExcludesOtherMachine 锁 §3.1/§4-23 核心：事件已被他机认领时，
// 本机跳过——不占名额、不发起回合，游标不越过该 seq（在飞认领挡住水位）。
func TestB389ClaimExcludesOtherMachine(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)
	seq := appendMirroredForConsumer(t, env.ledger, cardID, "claim", "completed", 1, `{"text":"done"}`)
	if got, err := env.ledger.ClaimWake(seq, cardID, "other#1", time.Minute); err != nil || !got {
		t.Fatalf("预认领: got=%v err=%v", got, err)
	}
	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
		t.Fatalf("消费: %v", err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 0 {
		t.Fatalf("他机持有认领时本机不得发起回合: %v", resumes)
	}
	inFlight, err := env.ledger.WakeClaimsBefore(seq + 1)
	if err != nil {
		t.Fatalf("读在飞认领: %v", err)
	}
	if len(inFlight) != 1 || inFlight[0] != seq {
		t.Fatalf("他机在飞认领应仍在: %v", inFlight)
	}
	env.srv.automationMu.Lock()
	cursor := env.srv.automationCursor
	env.srv.automationMu.Unlock()
	if cursor >= seq {
		t.Fatalf("在飞认领应挡住游标推进: cursor=%d seq=%d", cursor, seq)
	}
}

// TestB389LocalWakeCompletesClaim 锁 §3.1.5：本机认领并处理成功后收尾（done_at
// 非空），不再挡住宿主游标。
func TestB389LocalWakeCompletesClaim(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)
	seq := appendMirroredForConsumer(t, env.ledger, cardID, "claim-ok", "completed", 1, `{"text":"done"}`)
	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
		t.Fatalf("消费: %v", err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("本机应唤醒一次，实得 %d", len(resumes))
	}
	inFlight, err := env.ledger.WakeClaimsBefore(seq + 1)
	if err != nil {
		t.Fatalf("读在飞认领: %v", err)
	}
	if len(inFlight) != 0 {
		t.Fatalf("本机认领应已收尾，在飞=%v", inFlight)
	}
	if !wakeClaimRowExists(t, env, seq) {
		t.Fatal("本机处理必须留下认领行（收尾而非不认领）")
	}
}

// wakeClaimRowExists 直读认领行存在性，区分「认领后收尾」与「根本没认领」。
func wakeClaimRowExists(t *testing.T, env *ledgerEnv, seq int64) bool {
	t.Helper()
	db, err := sql.Open("sqlite", env.ledgerPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("打开 raw ledger: %v", err)
	}
	defer db.Close()
	var done any
	err = db.QueryRow(`SELECT done_at FROM wake_claims WHERE seq = ?`, seq).Scan(&done)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		t.Fatalf("读认领行: %v", err)
	}
	return done != nil
}

// TestB389MissingBearingSkipsWithoutSlots 锁 §4-21/22：缺承载时本机不发起回合、
// 不占名额，但事件照常消费一次（否则该 seq 一直重试），认领已收尾。
func TestB389MissingBearingSkipsWithoutSlots(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)
	deleteSeatBearingRow(t, env, cardID)
	appendMirroredForConsumer(t, env.ledger, cardID, "missing", "completed", 1, `{"text":"done"}`)
	processed, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil {
		t.Fatalf("消费: %v", err)
	}
	if processed != 1 {
		t.Fatalf("缺承载事件应被消费一次（无回合），processed=%d", processed)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 0 {
		t.Fatalf("缺承载不应发起回合: %v", resumes)
	}
	for _, key := range []string{"squad/coord/coord-carrier", "carrier/coord-carrier"} {
		if got := runningCountIn(t, env.srv.autoLedger, key); got != 0 {
			t.Fatalf("缺承载不应占名额 %s=%d", key, got)
		}
	}
	inFlight, err := env.ledger.WakeClaimsBefore(math.MaxInt64)
	if err != nil {
		t.Fatalf("读在飞认领: %v", err)
	}
	if len(inFlight) != 0 {
		t.Fatalf("缺承载事件应已收尾，在飞=%v", inFlight)
	}
}

// TestB389SeatBearingMissingIdempotent 锁 §4-22：同席位重复调用只落一条事件，
// 且「需要人」展示亮起。
func TestB389SeatBearingMissingIdempotent(t *testing.T) {
	env, _ := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)
	deleteSeatBearingRow(t, env, cardID)
	card, err := env.ledger.GetCard(cardID)
	if err != nil {
		t.Fatalf("读卡: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := env.srv.reportSeatBearingMissing(cardID, card.DriverSession); err != nil {
			t.Fatalf("第 %d 次报告缺承载: %v", i+1, err)
		}
	}
	events, err := env.ledger.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		t.Fatalf("读事件: %v", err)
	}
	missing := 0
	for _, ev := range events {
		if ev.Type == ledger.EvSeatBearingMissing {
			missing++
		}
	}
	if missing != 1 {
		t.Fatalf("EvSeatBearingMissing 应恰一条，实得 %d", missing)
	}
	if needs, err := env.ledger.NeedsOf(cardID); err != nil || needs == "" {
		t.Fatalf("缺承载应亮起需要人展示: needs=%q err=%v", needs, err)
	}
}

// siblingWakeRunner 按会话 id 决定 Resume 成败；Launch 恒失败（失败卡的兜底重建
// 也失败，从而触发错误；成功卡只 Resume 不重建）。用来构造「A 卡失败、B 卡成功」。
type siblingWakeRunner struct {
	mu             sync.Mutex
	failResumeSeat string
	resumes        []string
	launches       int
}

func (r *siblingWakeRunner) Launch(keysclient.SessionSpec, string) (keysclient.TurnResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.launches++
	return keysclient.TurnResult{}, errors.New("launch failed")
}

func (r *siblingWakeRunner) Resume(ref keysclient.SessionRef, _ string) (keysclient.TurnResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resumes = append(r.resumes, ref.SessionID)
	if ref.SessionID == r.failResumeSeat {
		return keysclient.TurnResult{}, errors.New("resume failed")
	}
	return keysclient.TurnResult{SessionID: ref.SessionID}, nil
}

func (r *siblingWakeRunner) snapshot() ([]string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.resumes...), r.launches
}

// bindCoordSeedForTest 直接落一个 coordinate 席位 + 承载（不经 Launch），
// 会话 id 由调用方给定，便于按会话指定失败。
func bindCoordSeedForTest(t *testing.T, env *ledgerEnv, cardID, sessionID string) string {
	t.Helper()
	seat, err := proto.EncodeSeatIdentity("opencode", sessionID)
	if err != nil {
		t.Fatalf("编码席位: %v", err)
	}
	if err := env.ledger.BindSeat(cardID, seat, proto.SeatSourceCoordinate,
		ledger.SeatBearing{Carrier: "coord-carrier", Machine: "local"}); err != nil {
		t.Fatalf("落席位: %v", err)
	}
	return seat
}

// TestB389WakeFailureDoesNotBlockSiblingCard 锁 §4-30：单卡唤醒失败不得阻断同批
// 其他卡（B 卡仍被唤醒，processed==1），失败卡进入退避。
func TestB389WakeFailureDoesNotBlockSiblingCard(t *testing.T) {
	env, _ := newNoPTYAutomationEnv(t)
	runner := &siblingWakeRunner{failResumeSeat: "sess-a"}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	cardA := createCoordCard(t, env)
	cardB := createCoordCard(t, env)
	bindCoordSeedForTest(t, env, cardA, "sess-a")
	bindCoordSeedForTest(t, env, cardB, "sess-b")
	appendMirroredForConsumer(t, env.ledger, cardA, "a", "completed", 1, `{"text":"a"}`)
	appendMirroredForConsumer(t, env.ledger, cardB, "b", "completed", 1, `{"text":"b"}`)

	processed, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err == nil {
		t.Fatal("A 卡失败应返回错误")
	}
	if processed != 1 {
		t.Fatalf("B 卡仍应被唤醒，processed=%d", processed)
	}
	resumes, _ := runner.snapshot()
	if len(resumes) != 2 {
		t.Fatalf("两张卡各应尝试一次 Resume，实得 %v", resumes)
	}
}

// TestB389WakeBackoffSkipsSameSeqNextRound 锁 §4-31：同卡同 seq 失败后进入退避，
// 相邻两轮不重复试跑同一 seq（把认领行清掉模拟他机释放/重放，仍不得再跑）。
func TestB389WakeBackoffSkipsSameSeqNextRound(t *testing.T) {
	env, _ := newNoPTYAutomationEnv(t)
	runner := &siblingWakeRunner{failResumeSeat: "sess-a"}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	cardA := createCoordCard(t, env)
	bindCoordSeedForTest(t, env, cardA, "sess-a")
	seq := appendMirroredForConsumer(t, env.ledger, cardA, "a", "completed", 1, `{"text":"a"}`)

	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err == nil {
		t.Fatal("首轮应失败")
	}
	resumes, _ := runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("首轮应尝试一次，实得 %v", resumes)
	}
	// 模拟他机释放/重放：删认领行、回退游标、清 seen。
	releaseWakeClaimForTest(t, env, seq)
	env.srv.automationMu.Lock()
	env.srv.automationCursor = seq - 1
	delete(env.srv.automationSeen, seq)
	env.srv.automationMu.Unlock()

	if _, _, _ = env.srv.consumeAutomationEventsOnce(context.Background()); false {
	}
	resumes, _ = runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("退避窗内不得重复试跑同 seq，resumes=%v", resumes)
	}
}

// releaseWakeClaimForTest 用 raw sqlite 删掉某 seq 的认领行（模拟租约到期后他机
// 释放，使第二轮重新可认领——否则认领本身就挡住了重放，测不到退避）。
func releaseWakeClaimForTest(t *testing.T, env *ledgerEnv, seq int64) {
	t.Helper()
	db, err := sql.Open("sqlite", env.ledgerPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("打开 raw ledger: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`DELETE FROM wake_claims WHERE seq = ?`, seq); err != nil {
		t.Fatalf("删认领行: %v", err)
	}
}

// fanoutTwoCardFixture 建一个会话、拉两张各有 coordinate 席位的卡进群，返回
// (会话 id, 群主 svc, 卡1, 卡2)。两张卡的会话 id 不同（单键认领会把第二张吞掉，
// 这正是本组用例要照到的缺陷族）。
func fanoutTwoCardFixture(t *testing.T, env *ledgerEnv) (string, *collab.Service, string, string) {
	t.Helper()
	svc := env.srv.rooms
	session, err := svc.CreateSession("扇出对账场", "user:tester", "user:tester")
	if err != nil {
		t.Fatalf("建会话: %v", err)
	}
	card1 := createCoordCard(t, env)
	card2 := createCoordCard(t, env)
	for _, cardID := range []string{card1, card2} {
		if err := svc.JoinCard(session.ID, cardID, "user:tester"); err != nil {
			t.Fatalf("拉卡 %s 进群: %v", cardID, err)
		}
	}
	bindCoordSeedForTest(t, env, card1, "sess-fan-1")
	bindCoordSeedForTest(t, env, card2, "sess-fan-2")
	drainAutomation(t, env)
	return session.ID, svc, card1, card2
}

// TestB389FanoutWakesBothCardsOnce 锁 §4-33/34：一条 room_message 寻址命中两张
// 各有 coordinate 席位的卡时，两张各被唤醒恰一次（processed==2、resumes==2），
// 游标推进到该 seq 之外且两个 (card,seq) 认领行都已 done_at 收尾。
func TestB389FanoutWakesBothCardsOnce(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	sessionID, svc, card1, card2 := fanoutTwoCardFixture(t, env)
	seq := sendSessionMessage(t, svc, sessionID, "user:tester", "请两张卡都看", []string{card1, card2}, 0)

	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || escalated {
		t.Fatalf("扇出消费失败: err=%v escalated=%v", err, escalated)
	}
	if processed != 2 {
		t.Fatalf("两张卡应各被唤醒一次，processed=%d", processed)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 2 {
		t.Fatalf("扇出应唤醒两张卡（单键会吞掉第二张）: %v", resumes)
	}
	env.srv.automationMu.Lock()
	cursor := env.srv.automationCursor
	env.srv.automationMu.Unlock()
	if cursor < seq {
		t.Fatalf("扇出后游标应越过该 seq: cursor=%d seq=%d", cursor, seq)
	}
	inFlight, err := env.ledger.WakeClaimsBefore(seq + 1)
	if err != nil {
		t.Fatalf("读在飞认领: %v", err)
	}
	if len(inFlight) != 0 {
		t.Fatalf("两张卡的认领行都应 done_at 收尾，在飞=%v", inFlight)
	}
}

// TestB389FanoutSiblingClaimDoesNotBlockOtherCard 锁 §4-35：扇出中某张卡的
// (card,seq) 已被他机持有时只跳过该卡，另一张卡仍被唤醒（排他收窄为按卡）。
func TestB389FanoutSiblingClaimDoesNotBlockOtherCard(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	sessionID, svc, card1, card2 := fanoutTwoCardFixture(t, env)
	seq := sendSessionMessage(t, svc, sessionID, "user:tester", "请两张卡都看", []string{card1, card2}, 0)
	// 他机预持卡1 的 (card1,seq)；卡2 的 (card2,seq) 仍可被本机认领。
	if got, err := env.ledger.ClaimWake(seq, card1, "other#1", time.Minute); err != nil || !got {
		t.Fatalf("他机预认领卡1: got=%v err=%v", got, err)
	}

	processed, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil {
		t.Fatalf("扇出消费: %v", err)
	}
	if processed != 1 {
		t.Fatalf("他机持有的一张卡应被跳过，另一张仍唤醒，processed=%d", processed)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 || !strings.Contains(resumes[0], "卡号：B2") {
		t.Fatalf("卡2 应仍被唤醒且恰一次: %v", resumes)
	}
	// 卡1 的他机认领仍在飞，挡住该水位；卡1 不得被本机收尾。
	inFlight, err := env.ledger.WakeClaimsBefore(seq + 1)
	if err != nil {
		t.Fatalf("读在飞认领: %v", err)
	}
	if len(inFlight) != 1 || inFlight[0] != seq {
		t.Fatalf("卡1 他机认领应仍在飞: %v", inFlight)
	}
}
