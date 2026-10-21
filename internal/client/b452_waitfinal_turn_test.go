// b452_waitfinal_turn_test.go —— 修复轮第 3 条：WaitFinal 在 waiting_review 下不得凭「上一回合」或「还没落库」
// 的快照下结论。handleResult 先迁 waiting_review、再 AppendEvent 回合结果，快照可能落在两步之间。
package client

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/coder/websocket"
)

func b452At(ev proto.Event, at time.Time) proto.Event { ev.CreatedAt = at; return ev }

func b452Snap(state proto.TaskState, updated time.Time, evs ...proto.Event) *AttachInfo {
	s := snapshot("t1", state, evs...)
	s.Task.UpdatedAt = updated
	return s
}

// 首回合：快照落在「已迁 waiting_review、completed 尚未落库」之间 → 不得判「未完成就归档」，退避重取快照。
func TestB452WaitFinalFirstTurnBetweenTransitAndAppend(t *testing.T) {
	t0 := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	s := newB452FinalServer(t, &b452FinalServer{
		attach: func(n int32) *AttachInfo {
			if n == 1 {
				return b452Snap(proto.TaskStateWaitingReview, t0, b452At(b452Ev(1, proto.EventTypeProgress), t0.Add(-time.Second)))
			}
			return b452Snap(proto.TaskStateWaitingReview, t0, b452At(b452Ev(1, proto.EventTypeProgress), t0.Add(-time.Second)),
				b452At(b452Completed(2, "首回合回文"), t0.Add(time.Millisecond)))
		},
		stream: func(_ int32, conn *websocket.Conn, r *http.Request) {
			// 回放只会给出同一批历史，然后正常关闭
			writeWSEvent(t, conn, r.Context(), b452Ev(1, proto.EventTypeProgress))
			_ = conn.Close(websocket.StatusNormalClosure, "")
		},
	})
	out, err := b452Client(s.ts.URL).WaitFinal(t.Context(), "t1", "", nil)
	if err != nil || !out.Completed || out.FinalText != "首回合回文" {
		t.Fatalf("应退避重取快照拿到首回合结果，得 outcome=%+v err=%v", out, err)
	}
	if s.attachN.Load() < 2 {
		t.Fatalf("应至少重取一次快照，得 %d", s.attachN.Load())
	}
}

// 续接过的任务：快照在 waiting_review，但最后一条回合结果早于本次迁入 waiting_review（上一回合的），
// 新回合结果尚未落库 → 不得返回上一回合的 final_text。
func TestB452WaitFinalContinuedTaskNeverReturnsPreviousTurn(t *testing.T) {
	t0 := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	transit := t0.Add(10 * time.Second) // 续接后的回合结束、迁入 waiting_review 的时刻
	prev := b452At(b452Completed(3, "上一回合"), t0)
	s := newB452FinalServer(t, &b452FinalServer{
		attach: func(n int32) *AttachInfo {
			if n == 1 {
				return b452Snap(proto.TaskStateWaitingReview, transit, prev, b452At(b452Ev(4, proto.EventTypeProgress), t0.Add(5*time.Second)))
			}
			return b452Snap(proto.TaskStateWaitingReview, transit, prev, b452At(b452Ev(4, proto.EventTypeProgress), t0.Add(5*time.Second)),
				b452At(b452Completed(5, "本回合"), transit.Add(time.Millisecond)))
		},
	})
	out, err := b452Client(s.ts.URL).WaitFinal(t.Context(), "t1", "", nil)
	if err != nil || out.FinalText != "本回合" || out.Event.Seq != 5 {
		t.Fatalf("续接任务不得返回上一回合，得 outcome=%+v err=%v", out, err)
	}
}

// 亲见过「进行中」水位的等待：之后快照的结果若不晚于该水位，一律是上一回合的，绝不采信（确定性判据）。
func TestB452WaitFinalIgnoresResultsAtOrBelowObservedProgressWatermark(t *testing.T) {
	t0 := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	prev := b452At(b452Completed(3, "上一回合"), t0)
	var n atomic.Int32
	s := newB452FinalServer(t, &b452FinalServer{
		attach: func(int32) *AttachInfo {
			switch n.Add(1) {
			case 1:
				return b452Snap(proto.TaskStateRunning, t0.Add(time.Second), prev)
			case 2, 3:
				// 时间判据被后续写入顶高也好、没顶高也好：seq 3 ≤ 亲见水位 3，只能是上一回合
				return b452Snap(proto.TaskStateWaitingReview, t0, prev)
			default:
				return b452Snap(proto.TaskStateWaitingReview, t0, prev, b452At(b452Completed(6, "本回合"), t0.Add(2*time.Second)))
			}
		},
		stream: func(_ int32, conn *websocket.Conn, _ *http.Request) { conn.CloseNow() },
	})
	out, err := b452Client(s.ts.URL).WaitFinal(t.Context(), "t1", "", nil)
	if err != nil || out.FinalText != "本回合" {
		t.Fatalf("应跳过水位以内的上一回合结果，得 outcome=%+v err=%v", out, err)
	}
}

// 回合结果落库后又有写入顶高 updated_at（如清扫作废工单）：连续两次快照同一结果 → 采信，不得无限等。
func TestB452WaitFinalAcceptsStableResultAfterLaterTaskWrite(t *testing.T) {
	t0 := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	s := newB452FinalServer(t, &b452FinalServer{
		attach: func(int32) *AttachInfo {
			return b452Snap(proto.TaskStateWaitingReview, t0.Add(time.Second), b452At(b452Completed(4, "回文"), t0))
		},
	})
	out, err := b452Client(s.ts.URL).WaitFinal(t.Context(), "t1", "", nil)
	if err != nil || out.FinalText != "回文" {
		t.Fatalf("稳定的结果应被采信，得 outcome=%+v err=%v", out, err)
	}
	if got := s.attachN.Load(); got != 2 {
		t.Fatalf("应恰两次快照确认，得 %d", got)
	}
}
