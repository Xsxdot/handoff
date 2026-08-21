package ledger

import (
	"testing"
	"time"
)

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
