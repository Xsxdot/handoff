// b453_waitfinal_test.go —— B453 S6/S7 客户端侧（V13、V14 客户端条）：WaitFinal 遇 failed + rerun_by
// 透明跟到新任务（含跨机 machine、链长 2）；waiting_review 下有 notice 待答工单先交回（Q8 窄改，
// 只对 notice；gate/ask 旧次序由 TestB460WaitFinalTurnEndedBeatsPending 锁住不变）。
package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/coder/websocket"
)

// b453Multi 是多任务桩：/api/tasks/<id> 按 id 取快照，/ws/events?task=<id> 按 id 推事件；记录 query。
type b453Multi struct {
	mu      sync.Mutex
	attachQ []string // "<id>?<query>"
	wsQ     []string
	attach  func(id string) *AttachInfo
	stream  func(id string, conn *websocket.Conn, r *http.Request)
	ts      *httptest.Server
}

func newB453Multi(t *testing.T, m *b453Multi) *b453Multi {
	t.Helper()
	m.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/tasks/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
			m.mu.Lock()
			m.attachQ = append(m.attachQ, id+"?"+r.URL.RawQuery)
			m.mu.Unlock()
			snap := m.attach(id)
			if snap == nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(snap)
		case r.URL.Path == "/ws/events":
			id := r.URL.Query().Get("task")
			m.mu.Lock()
			m.wsQ = append(m.wsQ, r.URL.RawQuery)
			m.mu.Unlock()
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()
			if m.stream != nil {
				m.stream(id, conn, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(func() { m.ts.CloseClientConnections(); m.ts.Close() })
	return m
}

func b453Snap(id string, state proto.TaskState, rerunBy *proto.TaskRef, evs ...proto.Event) *AttachInfo {
	s := snapshot(id, state, evs...)
	s.Task.RerunBy = rerunBy
	return s
}

func b453FailedEv(id string, seq int64, reason string) proto.Event {
	ev := b452Failed(seq, proto.EventTypeFailed, reason)
	ev.TaskID = id
	return ev
}

func b453CompletedEv(id string, seq int64, text string) proto.Event {
	ev := b452Completed(seq, text)
	ev.TaskID = id
	return ev
}

// V13：快照已是 failed + rerun_by（跨机）→ 跟到新任务，新任务 completed → 结论取自新任务。
func TestB453WaitFinalRerunFollowsChainAcrossMachines(t *testing.T) {
	m := newB453Multi(t, &b453Multi{attach: func(id string) *AttachInfo {
		switch id {
		case "t1":
			return b453Snap("t1", proto.TaskStateFailed, &proto.TaskRef{Task: "t2", Machine: "linux-01"},
				b453FailedEv("t1", 3, "额度耗尽；已改由 cb 重跑"))
		case "t2":
			s := b453Snap("t2", proto.TaskStateCompleted, nil, b453CompletedEv("t2", 5, "## 结论\n新任务"))
			s.Task.Target = "linux-01"
			return s
		}
		return nil
	}})
	out, err := b452Client(m.ts.URL).WaitFinal(t.Context(), "t1", "mac-mini")
	if err != nil {
		t.Fatalf("WaitFinal: %v", err)
	}
	if !out.Completed || out.Task.ID != "t2" || out.FinalText != "## 结论\n新任务" {
		t.Fatalf("应跟到 t2 并以其结论结束，得 %+v", out)
	}
	if len(out.FollowedFrom) != 1 || out.FollowedFrom[0] != (proto.TaskRef{Task: "t1", Machine: "mac-mini"}) {
		t.Fatalf("应记下跟链来源 t1@mac-mini，得 %+v", out.FollowedFrom)
	}
	found := false
	for _, q := range m.attachQ {
		if q == "t2?machine=linux-01" {
			found = true
		}
	}
	if !found {
		t.Fatalf("新任务快照应按 rerun_by.machine 路由，得 %v", m.attachQ)
	}
}

// V13：链长 2 跟两跳；事件流里拿到 failed（进入流前的快照尚无链接）→ 重取快照补到 rerun_by。
func TestB453WaitFinalRerunFollowsTwoHopsFromStream(t *testing.T) {
	var mu sync.Mutex
	streamed := map[string]bool{}
	m := newB453Multi(t, &b453Multi{
		attach: func(id string) *AttachInfo {
			mu.Lock()
			defer mu.Unlock()
			switch id {
			case "t1":
				if !streamed["t1"] {
					return b453Snap("t1", proto.TaskStateRunning, nil, b452Ev(1, proto.EventTypeProgress))
				}
				return b453Snap("t1", proto.TaskStateFailed, &proto.TaskRef{Task: "t2"}, b453FailedEv("t1", 3, "额度耗尽"))
			case "t2":
				return b453Snap("t2", proto.TaskStateFailed, &proto.TaskRef{Task: "t3"}, b453FailedEv("t2", 4, "额度耗尽"))
			case "t3":
				return b453Snap("t3", proto.TaskStateFailed, nil, b453FailedEv("t3", 6, "额度耗尽；没有可换的成员"))
			}
			return nil
		},
		stream: func(id string, conn *websocket.Conn, r *http.Request) {
			if id != "t1" {
				t.Errorf("只有 t1 需要跟流，却拨了 %s", id)
				return
			}
			mu.Lock()
			streamed["t1"] = true
			mu.Unlock()
			writeWSEvent(t, conn, r.Context(), b453FailedEv("t1", 3, "额度耗尽"))
			b452HoldUntilHangup(conn)
		},
	})
	out, err := b452Client(m.ts.URL).WaitFinal(t.Context(), "t1", "")
	if err != nil {
		t.Fatalf("WaitFinal: %v", err)
	}
	if out.Completed || out.Task.ID != "t3" || !strings.Contains(out.Reason, "没有可换的成员") || len(out.FollowedFrom) != 2 {
		t.Fatalf("应跟两跳到 t3 并以其失败结束，得 %+v", out)
	}
}

// V13 反例：failed 无 rerun_by → 行为同今天（非完成、原因原文）。
func TestB453WaitFinalFailedWithoutRerunUnchanged(t *testing.T) {
	m := newB453Multi(t, &b453Multi{attach: func(id string) *AttachInfo {
		if id == "t1" {
			return b453Snap("t1", proto.TaskStateFailed, nil, b453FailedEv("t1", 3, "进程退出"))
		}
		return nil
	}})
	out, err := b452Client(m.ts.URL).WaitFinal(t.Context(), "t1", "")
	if err != nil || out.Completed || out.Task.ID != "t1" || out.Reason != "进程退出" || len(out.FollowedFrom) != 0 {
		t.Fatalf("无链接应照旧，得 %+v %v", out, err)
	}
}

func b453Notice(snap *AttachInfo) *AttachInfo {
	snap.PendingTickets = append(snap.PendingTickets, proto.Ticket{ID: "tk-n", TaskID: snap.Task.ID,
		Kind: proto.TicketKindNotice, Request: []byte(`{"kind":"notice","reason":"quota_exhausted","notice":"分支任务撞额度，已停下"}`)})
	return snap
}

// V14 客户端 / Q8：waiting_review + notice 待答 → 先交回工单（快照路径）。
func TestB453WaitFinalNoticeBeatsTurnEnded(t *testing.T) {
	m := newB453Multi(t, &b453Multi{attach: func(id string) *AttachInfo {
		return b453Notice(b453Snap("t1", proto.TaskStateWaitingReview, nil,
			b452Failed(3, proto.EventTypeTurnFailed, "额度耗尽（账号 a 已标无额度）")))
	}})
	out, err := b452Client(m.ts.URL).WaitFinal(t.Context(), "t1", "")
	if err != nil || len(out.Pending) != 1 || out.Pending[0].Kind != proto.TicketKindNotice {
		t.Fatalf("waiting_review + notice 应先交回工单，得 %+v %v", out, err)
	}
}

// V14 客户端 / Q8：跟流时收到 turn_failed → 重取快照见 notice → 交回工单（不以 3 结束）。
func TestB453WaitFinalNoticeAfterStreamedTurnFailed(t *testing.T) {
	var mu sync.Mutex
	done := false
	m := newB453Multi(t, &b453Multi{
		attach: func(id string) *AttachInfo {
			mu.Lock()
			defer mu.Unlock()
			if !done {
				return b453Snap("t1", proto.TaskStateRunning, nil, b452Ev(1, proto.EventTypeProgress))
			}
			return b453Notice(b453Snap("t1", proto.TaskStateWaitingReview, nil,
				b452Failed(3, proto.EventTypeTurnFailed, "额度耗尽")))
		},
		stream: func(id string, conn *websocket.Conn, r *http.Request) {
			mu.Lock()
			done = true
			mu.Unlock()
			writeWSEvent(t, conn, r.Context(), b452Failed(3, proto.EventTypeTurnFailed, "额度耗尽"))
			b452HoldUntilHangup(conn)
		},
	})
	out, err := b452Client(m.ts.URL).WaitFinal(t.Context(), "t1", "")
	if err != nil || len(out.Pending) != 1 || out.Pending[0].ID != "tk-n" {
		t.Fatalf("流里 turn_failed 后应重取快照交回 notice，得 %+v %v", out, err)
	}
}
