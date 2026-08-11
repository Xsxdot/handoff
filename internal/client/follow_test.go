// follow_test.go —— FollowEvents 的行为契约测试。
//
// 职责：钉住「持续交付」「空闲以任何帧为准」「终态识别」三条，用自控 WS 端点，
// 不起真 agentd——被测的是客户端这一侧的语义。
//
// 边界：不验重连退避（那由 ws_backoff_test 覆盖），不验 cursor 落盘细节。
package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/proto"
)

// pushEvents 起一个把给定事件依次推给客户端的 WS 端点，推完按 after 收尾。
func pushEvents(t *testing.T, evs []proto.Event, after func(*websocket.Conn)) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		for _, ev := range evs {
			b, err := json.Marshal(ev)
			if err != nil {
				return
			}
			if err := c.Write(r.Context(), websocket.MessageText, b); err != nil {
				return
			}
		}
		after(c)
	}))
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close() })
	return ts
}

// TestFollowDeliversEveryEvent 验证 follow 不在首个事件后返回：
// 三条可动作事件必须逐条交付，且 progress 被过滤掉不交付。
func TestFollowDeliversEveryEvent(t *testing.T) {
	evs := []proto.Event{
		{Seq: 1, TaskID: "t1", Type: proto.EventTypeQuestion},
		{Seq: 2, TaskID: "t1", Type: proto.EventTypeProgress},
		{Seq: 3, TaskID: "t1", Type: proto.EventTypeCompleted},
		{Seq: 4, TaskID: "t1", Type: proto.EventTypeStalled},
		{Seq: 5, TaskID: "t1", Type: proto.EventTypeFailed},
	}
	ts := pushEvents(t, evs, func(c *websocket.Conn) { <-make(chan struct{}) })

	var got []int64
	err := client.New(ts.URL, "").FollowEvents(t.Context(), "t1", false, 0,
		func(ev *proto.Event) error {
			got = append(got, ev.Seq)
			return nil
		})
	if err != nil {
		t.Fatalf("FollowEvents = %v, want nil（failed 事件是正常终结）", err)
	}
	want := []int64{1, 3, 4, 5}
	if len(got) != len(want) {
		t.Fatalf("交付 seq = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("交付 seq = %v, want %v", got, want)
		}
	}
}

// TestFollowDoesNotExitOnCompleted 验证 completed **不**终结跟随：
// 那只是一轮结束，continue 之后还有事件。
//
// 缺陷形态：把 completed 当终态会让 follow 在每轮结束时退出，真空原样回来。
func TestFollowDoesNotExitOnCompleted(t *testing.T) {
	evs := []proto.Event{
		{Seq: 1, TaskID: "t1", Type: proto.EventTypeCompleted},
		{Seq: 2, TaskID: "t1", Type: proto.EventTypeQuestion},
	}
	done := make(chan struct{})
	ts := pushEvents(t, evs, func(c *websocket.Conn) { <-done })
	t.Cleanup(func() { close(done) })

	seen := make(chan int64, 4)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		_ = client.New(ts.URL, "").FollowEvents(ctx, "t1", false, 0,
			func(ev *proto.Event) error { seen <- ev.Seq; return nil })
	}()
	for _, want := range []int64{1, 2} {
		select {
		case got := <-seen:
			if got != want {
				t.Fatalf("交付 seq = %d, want %d", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("未等到 seq %d —— follow 可能在 completed 后就退出了", want)
		}
	}
}

// TestFollowIdleCountsProgressFrames 是 §2.2 的核心断言：
// **只有 progress 帧流入时不得触发空闲超时。**
//
// 缺陷形态：把空闲定义成「距上一次可交付事件」，一个健康的长跑任务（8f7a4f18
// 连跑 15 小时只有 progress）会每隔 --timeout 无故 124 一次，兜底变噪音。
func TestFollowIdleCountsProgressFrames(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		// 每 50ms 推一条 progress，持续到测试取消；空闲阈值设 300ms
		for i := 1; ; i++ {
			b, _ := json.Marshal(proto.Event{
				Seq: int64(i), TaskID: "t1", Type: proto.EventTypeProgress})
			if err := c.Write(r.Context(), websocket.MessageText, b); err != nil {
				return
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}))
	defer func() { ts.CloseClientConnections(); ts.Close() }()

	ctx, cancel := context.WithTimeout(t.Context(), 1500*time.Millisecond)
	defer cancel()
	err := client.New(ts.URL, "").FollowEvents(ctx, "t1", false,
		300*time.Millisecond, func(*proto.Event) error { return nil })
	if errors.Is(err, client.ErrIdleTimeout) {
		t.Fatal("只有 progress 流入时触发了空闲超时：空闲口径把过滤掉的帧漏算了")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("FollowEvents = %v, want context.DeadlineExceeded（测试自己收尾）", err)
	}
}

// TestFollowIdleTimeoutWhenNoFrames 验证完全无帧时按空闲超时退出。
func TestFollowIdleTimeoutWhenNoFrames(t *testing.T) {
	ts := pushEvents(t, nil, func(c *websocket.Conn) { <-make(chan struct{}) })
	err := client.New(ts.URL, "").FollowEvents(t.Context(), "t1", false,
		300*time.Millisecond, func(*proto.Event) error { return nil })
	if !errors.Is(err, client.ErrIdleTimeout) {
		t.Fatalf("FollowEvents = %v, want ErrIdleTimeout", err)
	}
}

// TestFollowExitsOnArchive 验证服务端以正常关闭码收尾时 follow 正常退出（nil），
// 而不是把它当断线无限重连一个已经结束的任务。
func TestFollowExitsOnArchive(t *testing.T) {
	evs := []proto.Event{{Seq: 1, TaskID: "t1", Type: proto.EventTypeQuestion}}
	ts := pushEvents(t, evs, func(c *websocket.Conn) {
		_ = c.Close(websocket.StatusNormalClosure, "task archived")
	})
	n := 0
	err := client.New(ts.URL, "").FollowEvents(t.Context(), "t1", false, 0,
		func(*proto.Event) error { n++; return nil })
	if err != nil {
		t.Fatalf("FollowEvents = %v, want nil（归档是正常终结）", err)
	}
	if n != 1 {
		t.Fatalf("交付 %d 条, want 1", n)
	}
}
