// b460_waitfinal_test.go —— B460 S1/S2/S3 V1：WaitFinal 以快照 pending_tickets 为「有待答工单」
// 的权威判据（F4/F5），question / permission_request 事件只作唤醒、立即重取快照。
package client

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/coder/websocket"
)

func b460Pending(snap *AttachInfo, ids ...string) *AttachInfo {
	for _, id := range ids {
		snap.PendingTickets = append(snap.PendingTickets, proto.Ticket{ID: id, TaskID: "t1", Kind: "gate",
			Request: []byte(`{"kind":"gate","permission":"Bash: stat README.md"}`)})
	}
	return snap
}

// b460Ctx 给每条用例 5s 上限：判据回归时快速变红而不是挂到 go test 总超时。
func b460Ctx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// 退避初值 2s：唤醒后重取快照若走了退避，(b) 的时延断言必红。
func b460Client(url string) *Client {
	return NewWithWSTiming(url, "", 2*time.Second, 4*time.Second, time.Second)
}

// (a) S3/F5：进入时已有待答工单 → 首轮快照即返回待答结论，零 WS 拨号。
func TestB460WaitFinalPendingOnEntryNoDial(t *testing.T) {
	s := newB452FinalServer(t, &b452FinalServer{
		attach: func(int32) *AttachInfo {
			return b460Pending(snapshot("t1", proto.TaskStateWaitingAnswer, b452Ev(5, proto.EventTypePermissionRequest)), "t1:p1")
		},
	})
	out, err := b460Client(s.ts.URL).WaitFinal(b460Ctx(t), "t1", "")
	if err != nil {
		t.Fatalf("WaitFinal: %v", err)
	}
	if len(out.Pending) != 1 || out.Pending[0].ID != "t1:p1" || out.Completed {
		t.Fatalf("应返回待答结论，得 %+v", out)
	}
	if n := s.wsN.Load(); n != 0 {
		t.Fatalf("进入即有待答工单时不得拨事件流，拨了 %d 次", n)
	}
}

// (b) S1/S2：首轮无待答；流里推 permission_request → 立即（不退避）重取快照，带工单 → 待答结论。
func TestB460WaitFinalWakeOnPermissionRequest(t *testing.T) {
	var mu sync.Mutex
	var at []time.Time
	s := newB452FinalServer(t, &b452FinalServer{
		attach: func(n int32) *AttachInfo {
			mu.Lock()
			at = append(at, time.Now())
			mu.Unlock()
			if n == 1 {
				return snapshot("t1", proto.TaskStateRunning, b452Ev(1, proto.EventTypeProgress))
			}
			return b460Pending(snapshot("t1", proto.TaskStateWaitingAnswer,
				b452Ev(1, proto.EventTypeProgress), b452Ev(2, proto.EventTypePermissionRequest)), "t1:p2")
		},
		stream: func(_ int32, conn *websocket.Conn, r *http.Request) {
			writeWSEvent(t, conn, r.Context(), b452Ev(2, proto.EventTypePermissionRequest))
			<-r.Context().Done()
		},
	})
	start := time.Now()
	out, err := b460Client(s.ts.URL).WaitFinal(b460Ctx(t), "t1", "")
	if err != nil {
		t.Fatalf("WaitFinal: %v", err)
	}
	if len(out.Pending) != 1 || out.Pending[0].ID != "t1:p2" {
		t.Fatalf("应返回待答结论，得 %+v", out)
	}
	if el := time.Since(start); el > time.Second {
		t.Fatalf("唤醒后应立即重取快照（不退避），实际耗时 %s", el)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(at) != 2 {
		t.Fatalf("应恰取两次快照，得 %d", len(at))
	}
}

// (c) 唤醒后快照无待答（已被答复）→ 从新水位继续跟流 → completed。
func TestB460WaitFinalWakeButAnsweredContinues(t *testing.T) {
	s := newB452FinalServer(t, &b452FinalServer{
		attach: func(n int32) *AttachInfo {
			if n == 1 {
				return snapshot("t1", proto.TaskStateRunning, b452Ev(1, proto.EventTypeProgress))
			}
			return snapshot("t1", proto.TaskStateRunning, b452Ev(1, proto.EventTypeProgress),
				b452Ev(2, proto.EventTypePermissionRequest), b452Ev(3, proto.EventTypeTicketAnswered))
		},
		stream: func(n int32, conn *websocket.Conn, r *http.Request) {
			if n == 1 {
				writeWSEvent(t, conn, r.Context(), b452Ev(2, proto.EventTypePermissionRequest))
			} else {
				writeWSEvent(t, conn, r.Context(), b452Completed(4, "回文"))
			}
			<-r.Context().Done()
		},
	})
	out, err := b460Client(s.ts.URL).WaitFinal(b460Ctx(t), "t1", "")
	if err != nil {
		t.Fatalf("WaitFinal: %v", err)
	}
	if !out.Completed || out.FinalText != "回文" || len(out.Pending) != 0 {
		t.Fatalf("应完成结论，得 %+v", out)
	}
	if q := s.wsQ[1]; !strings.Contains(q, "from_seq=3") {
		t.Fatalf("续跟应从新水位起，得 %q", q)
	}
}

// (d) F4 优先级：waiting_review 且同时带待答工单 → 走回合结论，不返回待答。
func TestB460WaitFinalTurnEndedBeatsPending(t *testing.T) {
	t0 := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	s := newB452FinalServer(t, &b452FinalServer{
		attach: func(int32) *AttachInfo {
			return b460Pending(b452Snap(proto.TaskStateWaitingReview, t0,
				b452At(b452Completed(2, "回文"), t0.Add(time.Millisecond))), "t1:p9")
		},
	})
	out, err := b460Client(s.ts.URL).WaitFinal(b460Ctx(t), "t1", "")
	if err != nil {
		t.Fatalf("WaitFinal: %v", err)
	}
	if !out.Completed || len(out.Pending) != 0 {
		t.Fatalf("回合已结束应优先给回合结论，得 %+v", out)
	}
}

// (f) machine 非空：两次快照与流都带 machine=。
func TestB460WaitFinalWakeByMachine(t *testing.T) {
	s := newB452FinalServer(t, &b452FinalServer{
		attach: func(n int32) *AttachInfo {
			if n == 1 {
				return snapshot("t1", proto.TaskStateRunning)
			}
			return b460Pending(snapshot("t1", proto.TaskStateWaitingAnswer, b452Ev(1, proto.EventTypeQuestion)), "t1:q")
		},
		stream: func(_ int32, conn *websocket.Conn, r *http.Request) {
			writeWSEvent(t, conn, r.Context(), b452Ev(1, proto.EventTypeQuestion))
			<-r.Context().Done()
		},
	})
	out, err := b460Client(s.ts.URL).WaitFinal(b460Ctx(t), "t1", "linux-01")
	if err != nil || len(out.Pending) != 1 {
		t.Fatalf("应待答结论：%+v %v", out, err)
	}
	for _, q := range append(append([]string(nil), s.attachQ...), s.wsQ...) {
		if !strings.Contains(q, "machine=linux-01") {
			t.Fatalf("快照与流都应带 machine，得 %q（attach %v ws %v）", q, s.attachQ, s.wsQ)
		}
	}
	if len(s.attachQ) != 2 || len(s.wsQ) != 1 {
		t.Fatalf("应两次快照一次流，得 attach %v ws %v", s.attachQ, s.wsQ)
	}
}
