package ledger

import (
	"encoding/json"
	"testing"
	"time"
)

func TestLiveMirrorTargets(t *testing.T) {
	s := seedStore(t)
	if live, err := s.LiveMirrorTargets(); err != nil || len(live) != 0 {
		t.Fatalf("空账本应无在飞 target: %v %+v", err, live)
	}
	archived := mk(t, s, "已归档")
	if err := s.LinkTask(archived.ID, "mac-02", "T-arch", "implement", "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMirroredEvent(archived.ID, MirroredEvent{Target: "mac-02", Task: "T-arch",
		SourceSeq: 1, Type: "completed", Payload: []byte(`{}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMirroredEvent(archived.ID, MirroredEvent{Target: "mac-02", Task: "T-arch",
		SourceSeq: 2, Type: "archived", Payload: []byte(`{}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	failed := mk(t, s, "已失败")
	if err := s.LinkTask(failed.ID, "mac-02", "T-fail", "implement", "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMirroredEvent(failed.ID, MirroredEvent{Target: "mac-02", Task: "T-fail",
		SourceSeq: 1, Type: "failed", Payload: []byte(`{}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if live, err := s.LiveMirrorTargets(); err != nil || live["mac-02"] {
		t.Fatalf("全终态不应算在飞: %v %+v", err, live)
	}
	waiting := mk(t, s, "待审")
	if err := s.LinkTask(waiting.ID, "linux-01", "T-wait", "implement", "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMirroredEvent(waiting.ID, MirroredEvent{Target: "linux-01", Task: "T-wait",
		SourceSeq: 1, Type: "completed", Payload: []byte(`{}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	unknown := mk(t, s, "未镜像")
	if err := s.LinkTask(unknown.ID, "mac-02", "T-new", "implement", "t"); err != nil {
		t.Fatal(err)
	}
	live, err := s.LiveMirrorTargets()
	if err != nil {
		t.Fatal(err)
	}
	if !live["linux-01"] {
		t.Fatalf("waiting_review 仍会再来事件，应算在飞: %+v", live)
	}
	if !live["mac-02"] {
		t.Fatalf("从未镜像过的挂账应算在飞: %+v", live)
	}
}

func TestAppendMirroredEventWorkflowEnvelopeGolden(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "镜像工作流事件")
	if _, err := s.AppendMirroredEvent(c.ID, MirroredEvent{
		Target: "mac-02", Task: "task-attempt-1", Node: "review", Attempt: "task-attempt-1",
		SourceSeq: 7, Type: "question", Payload: []byte(`{"ticket_id":"task-attempt-1:q1"}`), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	events, err := s.EventsFromAsc([]string{c.ID}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("镜像事件未落账")
	}
	want := `{"node":"review","attempt":"task-attempt-1","task_type":"question","payload":{"ticket_id":"task-attempt-1:q1"}}`
	if got := string(events[len(events)-1].Payload); got != want {
		t.Fatalf("镜像工作流 envelope = %s, want %s", got, want)
	}
}

func TestAppendMirroredEventWorkflowEnvelopeJSONBoundaries(t *testing.T) {
	tests := []struct {
		name                        string
		node, attempt, payload      string
		nodePresent, attemptPresent bool
		payloadPresent              bool
	}{
		{name: "missing fields", nodePresent: false, attemptPresent: false, payloadPresent: false},
		{name: "empty strings and null", nodePresent: true, attemptPresent: true, payloadPresent: true},
		{name: "nonempty strings and object", node: "review", attempt: "attempt-1", payload: `{"ticket_id":"q1"}`, nodePresent: true, attemptPresent: true, payloadPresent: true},
		{name: "missing node with nonempty attempt", attempt: "attempt-2", payload: `{}`, nodePresent: false, attemptPresent: true, payloadPresent: true},
		{name: "nonempty identity with missing payload", node: "review", attempt: "attempt-3", nodePresent: true, attemptPresent: true, payloadPresent: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := seedStore(t)
			c := mk(t, s, "镜像 JSON 边界")
			raw := appendWorkflowEnvelopePayload(t, s, c.ID, MirroredEvent{
				Target: "mac-02", Task: "task-" + tc.name, Node: tc.node, Attempt: tc.attempt,
				SourceSeq: 1, Type: "question", Payload: []byte(tc.payload), CreatedAt: time.Unix(0, 0),
			})
			raw = omitWorkflowEnvelopeFields(t, raw, tc.nodePresent, tc.attemptPresent, tc.payloadPresent)
			got, err := decodeWorkflowEnvelopeJSON(raw)
			if err != nil {
				t.Fatalf("真实 AppendMirroredEvent envelope 解码: %v; raw=%s", err, raw)
			}

			if tc.nodePresent {
				if got.Node == nil || *got.Node != tc.node {
					t.Fatalf("node = %v, want present value %q", got.Node, tc.node)
				}
			} else if got.Node != nil {
				t.Fatalf("缺失 node 不得解码成零值 %q", *got.Node)
			}
			if tc.attemptPresent {
				if got.Attempt == nil || *got.Attempt != tc.attempt {
					t.Fatalf("attempt = %v, want present value %q", got.Attempt, tc.attempt)
				}
			} else if got.Attempt != nil {
				t.Fatalf("缺失 attempt 不得解码成零值 %q", *got.Attempt)
			}
			if tc.payloadPresent {
				want := tc.payload
				if want == "" {
					want = "null"
				}
				if string(got.Payload) != want {
					t.Fatalf("payload = %s, want %s", got.Payload, want)
				}
			} else if got.Payload != nil {
				t.Fatalf("缺失 payload 不得解码成 JSON null: %s", got.Payload)
			}
		})
	}
}

type workflowEnvelopeJSON struct {
	Node     *string         `json:"node"`
	Attempt  *string         `json:"attempt"`
	TaskType string          `json:"task_type"`
	Payload  json.RawMessage `json:"payload"`
}

func appendWorkflowEnvelopePayload(t *testing.T, s *Store, cardID string, ev MirroredEvent) []byte {
	t.Helper()
	wrote, err := s.AppendMirroredEvent(cardID, ev)
	if err != nil || !wrote {
		t.Fatalf("真实 AppendMirroredEvent: wrote=%v err=%v", wrote, err)
	}
	events, err := s.EventsFromAsc([]string{cardID}, 0, 100)
	if err != nil {
		t.Fatalf("读取真实镜像 envelope: %v", err)
	}
	for _, event := range events {
		if event.Type == EvTaskMirrored && event.SourceTarget == ev.Target &&
			event.SourceTask == ev.Task && event.SourceSeq == ev.SourceSeq {
			return append([]byte(nil), event.Payload...)
		}
	}
	t.Fatalf("未找到真实镜像 envelope: target=%s task=%s seq=%d", ev.Target, ev.Task, ev.SourceSeq)
	return nil
}

func omitWorkflowEnvelopeFields(t *testing.T, raw []byte, nodePresent, attemptPresent, payloadPresent bool) []byte {
	t.Helper()
	// AppendMirroredEvent always emits the new envelope keys; deleting keys only
	// here models a legacy persisted envelope so missing and explicit zero values
	// can be asserted against the same real append/decode path.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("解码 envelope fixture: %v", err)
	}
	if !nodePresent {
		delete(fields, "node")
	}
	if !attemptPresent {
		delete(fields, "attempt")
	}
	if !payloadPresent {
		delete(fields, "payload")
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("重新编码 envelope fixture: %v", err)
	}
	return encoded
}

func decodeWorkflowEnvelopeJSON(raw []byte) (workflowEnvelopeJSON, error) {
	var envelope workflowEnvelopeJSON
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return workflowEnvelopeJSON{}, err
	}
	return envelope, nil
}

func FuzzAppendMirroredEventWorkflowEnvelopeJSONRoundTrip(f *testing.F) {
	f.Add("review", "attempt-1", []byte(`{"ticket_id":"q1"}`), true, true, true)
	f.Add("", "", []byte(`null`), true, true, true)
	f.Add("review", "attempt-2", []byte(`{}`), false, true, true)
	f.Add("review", "attempt-3", []byte{}, true, true, false)

	f.Fuzz(func(t *testing.T, node, attempt string, payload []byte, nodePresent, attemptPresent, payloadPresent bool) {
		if len(payload) > 0 && !json.Valid(payload) {
			t.Skip()
		}
		s := seedStore(t)
		c := mk(t, s, "镜像 JSON fuzz")
		raw := appendWorkflowEnvelopePayload(t, s, c.ID, MirroredEvent{
			Target: "fuzz-target", Task: "fuzz-task", Node: node, Attempt: attempt,
			SourceSeq: 1, Type: "question", Payload: payload, CreatedAt: time.Unix(0, 0),
		})
		raw = omitWorkflowEnvelopeFields(t, raw, nodePresent, attemptPresent, payloadPresent)
		got, err := decodeWorkflowEnvelopeJSON(raw)
		if err != nil {
			t.Fatalf("真实 AppendMirroredEvent envelope 解码: %v; raw=%s", err, raw)
		}

		if nodePresent {
			if got.Node == nil || *got.Node != node {
				t.Fatalf("node = %v, want present value %q", got.Node, node)
			}
		} else if got.Node != nil {
			t.Fatalf("缺失 node 不得解码成零值 %q", *got.Node)
		}
		if attemptPresent {
			if got.Attempt == nil || *got.Attempt != attempt {
				t.Fatalf("attempt = %v, want present value %q", got.Attempt, attempt)
			}
		} else if got.Attempt != nil {
			t.Fatalf("缺失 attempt 不得解码成零值 %q", *got.Attempt)
		}
		if payloadPresent {
			want := payload
			if len(want) == 0 {
				want = []byte("null")
			}
			if string(got.Payload) != string(want) {
				t.Fatalf("payload = %s, want %s", got.Payload, want)
			}
		} else if got.Payload != nil {
			t.Fatalf("缺失 payload 不得解码成 JSON null: %s", got.Payload)
		}
	})
}

func TestLatestTaskStates(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "卡")
	if err := s.LinkTask(c.ID, "mac-02", "T1", "implement", "t"); err != nil {
		t.Fatal(err)
	}
	for i, typ := range []string{"message", "state", "failed"} {
		if _, err := s.AppendMirroredEvent(c.ID, MirroredEvent{Target: "mac-02", Task: "T1",
			SourceSeq: int64(i + 1), Type: typ, Payload: []byte(`{}`), CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	states, err := s.LatestTaskStates(c.ID)
	if err != nil || len(states) != 1 {
		t.Fatalf("states: %v %+v", err, states)
	}
	if states[0].LastType != "failed" {
		t.Fatalf("实况应取最后一条: %+v", states[0])
	}
	// 无镜像事件的挂账 task：LastType 空（未知，不编）
	if err := s.LinkTask(c.ID, "mac-02", "T2", "review", "t"); err != nil {
		t.Fatal(err)
	}
	states, err = s.LatestTaskStates(c.ID)
	if err != nil || len(states) != 2 {
		t.Fatalf("应两行: %v %+v", err, states)
	}
	if states[1].LastType != "" {
		t.Fatalf("无镜像 task 应为未知: %+v", states[1])
	}
}

func TestOpenTicketCounts(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "卡")
	if err := s.LinkTask(c.ID, "mac-02", "T1", "implement", "t"); err != nil {
		t.Fatal(err)
	}
	seq := int64(0)
	put := func(typ, payload string) {
		seq++
		if _, err := s.AppendMirroredEvent(c.ID, MirroredEvent{Target: "mac-02", Task: "T1",
			SourceSeq: seq, Type: typ, Payload: []byte(payload), CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	put(evTicketCreated, `{"ticket_id":"q1"}`)
	put(evTicketCreated, `{"ticket_id":"q2"}`)
	counts, err := s.OpenTicketCounts()
	if err != nil || counts[c.ID] != 2 {
		t.Fatalf("两单未决: %v %+v", err, counts)
	}
	put(evTicketAnswered, `{"ticket_id":"q1"}`)
	counts, err = s.OpenTicketCounts()
	if err != nil || counts[c.ID] != 1 {
		t.Fatalf("答一单剩一单: %v %+v", err, counts)
	}
	put(evTicketsVoided, `{}`) // 回合结束作废全部未决单
	counts, err = s.OpenTicketCounts()
	if err != nil || counts[c.ID] != 0 {
		t.Fatalf("作废后应清零: %v %+v", err, counts)
	}
}

func TestCardStepInFlightNoEvents(t *testing.T) {
	s := seedStore(t)
	inFlight, err := s.CardStepInFlight("B167")
	if err != nil {
		t.Fatalf("CardStepInFlight: %v", err)
	}
	if inFlight {
		t.Fatal("没有镜像事件不应报告在飞")
	}
}

func recordDispatch(t *testing.T, s *Store, cardID, target, taskID string) {
	t.Helper()
	if err := s.RecordDispatch(cardID, DispatchSnapshot{
		Target: target, TaskID: taskID, Branch: "cards/" + cardID + "-" + taskID,
		Purpose: PurposeImplement, Template: "feature-impl", Actor: "test",
	}); err != nil {
		t.Fatalf("写派发事件: %v", err)
	}
}

func mirrorTaskEvent(t *testing.T, s *Store, cardID, target, taskID, typ string) {
	t.Helper()
	seq, err := s.MirrorWatermark(target, taskID)
	if err != nil {
		t.Fatalf("取镜像 watermark: %v", err)
	}
	if _, err := s.AppendMirroredEvent(cardID, MirroredEvent{
		Target: target, Task: taskID, SourceSeq: seq + 1, Type: typ,
		Payload: nil, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("写镜像任务事件: %v", err)
	}
}

// TestCardStepInFlightReplaysTaskLifecycle 在飞判定按任务生命周期回放：
// 派发后未见终态=在飞；archived/failed 才收口。completed/turn_failed 对应
// waiting_review，基准语义把「等裁决」算在飞，不许当终态。
func TestCardStepInFlightInFlightUntilTerminal(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "在飞判定")
	if got, err := s.CardStepInFlight(c.ID); err != nil || got {
		t.Fatalf("没派过任务应不在飞，实得 %v err=%v", got, err)
	}
	// 真实派发是卡账本原生 EvDispatched；此时还没有任何镜像事件。
	recordDispatch(t, s, c.ID, "acc", "T-1")
	if got, _ := s.CardStepInFlight(c.ID); !got {
		t.Fatal("只派发、零镜像事件时必须判在飞")
	}
	mirrorTaskEvent(t, s, c.ID, "acc", "T-1", "completed")
	if got, _ := s.CardStepInFlight(c.ID); !got {
		t.Fatal("completed 对应 waiting_review，仍算在飞")
	}
	mirrorTaskEvent(t, s, c.ID, "acc", "T-1", "turn_failed")
	if got, _ := s.CardStepInFlight(c.ID); !got {
		t.Fatal("turn_failed 对应 waiting_review，仍算在飞")
	}
	mirrorTaskEvent(t, s, c.ID, "acc", "T-1", "archived")
	if got, _ := s.CardStepInFlight(c.ID); got {
		t.Fatal("archived 是终态，应收口")
	}
}

// TestCardStepInFlightPerTask 多任务各自回放，一个收口不影响另一个。
func TestCardStepInFlightPerTask(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "多任务在飞")
	recordDispatch(t, s, c.ID, "acc", "T-1")
	recordDispatch(t, s, c.ID, "acc", "T-2")
	mirrorTaskEvent(t, s, c.ID, "acc", "T-1", "archived")
	if got, _ := s.CardStepInFlight(c.ID); !got {
		t.Fatal("T-2 未收口，整卡仍应判在飞")
	}
	mirrorTaskEvent(t, s, c.ID, "acc", "T-2", "failed")
	if got, _ := s.CardStepInFlight(c.ID); got {
		t.Fatal("两个任务都收口了，应判不在飞")
	}
}
