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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
	if err := env.ledger.BindSeat(cardID, identity, proto.SeatSourceCoordinate); err != nil {
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
	if processed != 6 {
		t.Fatalf("处理唤醒事件数=%d，want 6", processed)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("同卡事件应合并为一次 Resume，实得 %d", len(resumes))
	}
	for _, want := range []string{"completed", "failed", "turn_failed", "permission_request", "question", "用户留言"} {
		if !strings.Contains(resumes[0], want) {
			t.Fatalf("briefing 缺 %q: %s", want, resumes[0])
		}
	}
	for _, unwanted := range []string{"progress", "协调者指针", "系统用户形状"} {
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
	if err := env.ledger.CloseCard(cardID, ledger.CloseCancelled, "test"); err != nil {
		t.Fatal(err)
	}

	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || escalated || processed != 5 {
		t.Fatalf("卡原生动作消费 processed=%d escalated=%v err=%v，want 5/nil/false", processed, escalated, err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("卡原生动作应合并一次 Resume，实得 %d", len(resumes))
	}
	for _, want := range []string{"needs human payload", "needs_cleared", "decision body", "answer", "真人 room payload"} {
		if !strings.Contains(resumes[0], want) {
			t.Fatalf("卡原生动作 briefing 缺少 %q: %s", want, resumes[0])
		}
	}
	for _, unwanted := range []string{"系统 room payload", "comment audit", "协调者指针", ledger.StatusTodo, ledger.StatusClosed} {
		if strings.Contains(resumes[0], unwanted) {
			t.Fatalf("非动作事件 %q 不应进入 briefing: %s", unwanted, resumes[0])
		}
	}
}

func TestB353AutomationRoomJSONBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    bool
		wantErr bool
	}{
		{name: "by_system missing", payload: `{"kind":"user","body":"missing"}`, want: true},
		{name: "by_system false", payload: `{"kind":"user","body":"false","by_system":false}`, want: true},
		{name: "by_system true", payload: `{"kind":"user","body":"true","by_system":true}`},
		{name: "by_system null", payload: `{"kind":"user","body":"null","by_system":null}`},
		{name: "by_system invalid", payload: `{"kind":"user","body":"invalid","by_system":"yes"}`, wantErr: true},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := proto.LedgerEvent{Seq: int64(i + 1), CardID: "B353", Type: ledger.EvRoomMessage,
				Payload: json.RawMessage(tc.payload)}
			wake, got, err := automationWakeEvent(ev)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "事件") ||
					!strings.Contains(err.Error(), fmt.Sprint(i+1)) {
					t.Fatalf("非法 room payload error=%v", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("room payload got wake=%+v yes=%v err=%v，want yes=%v", wake, got, err, tc.want)
			}
		})
	}
}

func TestB353AutomationRoomJSONBoundariesThroughConsumer(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    bool
		wantErr bool
	}{
		{name: "by_system missing", payload: `{"kind":"user","body":"missing"}`, want: true},
		{name: "by_system false", payload: `{"kind":"user","body":"false","by_system":false}`, want: true},
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
			wantProcessed := 0
			if tc.want {
				wantProcessed = 1
			}
			if processed != wantProcessed {
				t.Fatalf("room payload consumer processed=%d, want %d", processed, wantProcessed)
			}
			_, resumes, _ := runner.snapshot()
			if tc.want && len(resumes) != 1 {
				t.Fatalf("真人 room payload 应唤醒一次，resumes=%v", resumes)
			}
			if !tc.want && len(resumes) != 0 {
				t.Fatalf("系统/null room payload 不应唤醒，resumes=%v", resumes)
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
	if err := env.ledger.BindSeat(cardID, identity, proto.SeatSourceCoordinate); err != nil {
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
	prebindConsumerSession(t, env, cardID)
	runner.failLaunch = true
	runner.failResume = true
	appendUserMessage(t, env.ledger, cardID, "用户留言", false)

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
		if err != nil || processed != 1 {
			t.Fatalf("Save 失败后新 Server 应从未落盘水位重试，processed=%d err=%v", processed, err)
		}
		if _, err := os.Stat(cursorPath); err != nil {
			t.Fatalf("新 Server 重试成功后应保存 cursor: %v", err)
		}
		_, resumes, _ = runner.snapshot()
		if len(resumes) != 2 {
			t.Fatalf("Save 失败事件应在新 Server 重试并唤醒两次，resumes=%v", resumes)
		}
	})
}
