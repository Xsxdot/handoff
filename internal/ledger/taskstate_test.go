package ledger

import (
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
