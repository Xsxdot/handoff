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
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/coder/websocket"
)

// pushEvents 起一个把给定事件依次推给客户端的 WS 端点，推完按 after 收尾。
func pushEvents(t *testing.T, evs []proto.Event, after func(*websocket.Conn)) *httptest.Server {
	t.Helper()
	// cursor 落在 $HOME/.handoff/cursors/<agentd>/<task>：不重定向就会污染真实主目录，
	// 且上一轮遗留的 cursor 会让本轮的 from_seq 起点不确定
	t.Setenv("HOME", t.TempDir())
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
		}, nil)
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
			func(ev *proto.Event) error { seen <- ev.Seq; return nil }, nil)
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
		300*time.Millisecond, func(*proto.Event) error { return nil }, nil)
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
		300*time.Millisecond, func(*proto.Event) error { return nil }, nil)
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
		func(*proto.Event) error { n++; return nil }, nil)
	if err != nil {
		t.Fatalf("FollowEvents = %v, want nil（归档是正常终结）", err)
	}
	if n != 1 {
		t.Fatalf("交付 %d 条, want 1", n)
	}
}

// TestFollowArchiveWithIdleSetIsNormal 验证 idle>0 时归档关闭仍被识别为正常终结
// （nil），而不是被 cancelRead() 污染成 ErrIdleTimeout。
//
// 缺陷形态（真机验收实测）：streamOnce 的读循环在错误分诊之前先 cancelRead()，
// 于是 readCtx.Err() 恒为 context.Canceled；判据又只问「非 nil」——两个叠加后
// idle>0 时任何读错误都被判成 ErrIdleTimeout。归档的 StatusNormalClosure、普通
// 断线、对端重启全被一锅端，errArchived 与重连分支永远到不了。而 124 的语义是
// 「到点了，正常」，无人值守时没人会去查，B56 要根除的无人订阅真空就此复活。
func TestFollowArchiveWithIdleSetIsNormal(t *testing.T) {
	evs := []proto.Event{{Seq: 1, TaskID: "t1", Type: proto.EventTypeQuestion}}
	ts := pushEvents(t, evs, func(c *websocket.Conn) {
		_ = c.Close(websocket.StatusNormalClosure, "task archived")
	})
	n := 0
	err := client.New(ts.URL, "").FollowEvents(t.Context(), "t1", false, time.Hour,
		func(*proto.Event) error { n++; return nil }, nil)
	if err != nil {
		t.Fatalf("FollowEvents = %v, want nil（归档是正常终结）", err)
	}
	if n != 1 {
		t.Fatalf("交付 %d 条, want 1", n)
	}
}

// TestFollowAbruptCloseReconnectsNotIdleTimeout 验证 idle>0 且连接被粗暴断开
// （不发关闭帧，模拟对端重启/网络切断）时 follow 走重连而不是误判空闲超时。
func TestFollowAbruptCloseReconnectsNotIdleTimeout(t *testing.T) {
	var mu sync.Mutex
	conns := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		conns++
		first := conns == 1
		mu.Unlock()
		if first {
			// 第一次连接：不打招呼直接掐断（模拟对端重启/网络切断）
			c.CloseNow()
			return
		}
		// 第二次连接：持续推 progress，验证 follow 确实重连上了
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

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err := client.NewWithWSTiming(ts.URL, "", 10*time.Millisecond, 50*time.Millisecond, 5*time.Second).
		FollowEvents(ctx, "t1", false, time.Hour, func(*proto.Event) error { return nil }, nil)
	if errors.Is(err, client.ErrIdleTimeout) {
		t.Fatal("断线被误判成空闲超时：重连分支到不了")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("FollowEvents = %v, want context.DeadlineExceeded（测试自己收尾，证明 follow 一直在重连）", err)
	}
	mu.Lock()
	got := conns
	mu.Unlock()
	if got < 2 {
		t.Fatalf("服务端收到连接 %d 次, want >= 2（follow 未重连）", got)
	}
}

// TestFollowFiltersAuditEvents 钉住可交付口径：approver_decision / approver_disabled
// 与 progress 一样不唤醒协调者。
//
// 为什么这条会退化：这两类在服务端只入库不 Publish，实时流本就见不到，
// 于是「客户端不过滤」长期没有症状——直到 WS 重放从 store 读出它们。
func TestFollowFiltersAuditEvents(t *testing.T) {
	evs := []proto.Event{
		{Seq: 1, TaskID: "t-audit", Type: proto.EventTypeApproverDecision},
		{Seq: 2, TaskID: "t-audit", Type: proto.EventTypeProgress},
		{Seq: 3, TaskID: "t-audit", Type: proto.EventTypeApproverDisabled},
		{Seq: 4, TaskID: "t-audit", Type: proto.EventTypeQuestion},
		{Seq: 5, TaskID: "t-audit", Type: proto.EventTypeFailed},
	}
	ts := pushEvents(t, evs, func(c *websocket.Conn) { <-make(chan struct{}) })

	var got []int64
	err := client.New(ts.URL, "").FollowEvents(t.Context(), "t-audit", false, 0,
		func(ev *proto.Event) error { got = append(got, ev.Seq); return nil }, nil)
	if err != nil {
		t.Fatalf("FollowEvents = %v, want nil", err)
	}
	want := []int64{4, 5}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("交付 seq = %v, want %v（审计类事件不该唤醒协调者）", got, want)
	}
}

// TestFollowAllDeliversAuditEvents 验证 all=true 不做任何过滤：排障时要看得到审计事件。
func TestFollowAllDeliversAuditEvents(t *testing.T) {
	evs := []proto.Event{
		{Seq: 1, TaskID: "t-all", Type: proto.EventTypeApproverDecision},
		{Seq: 2, TaskID: "t-all", Type: proto.EventTypeProgress},
		{Seq: 3, TaskID: "t-all", Type: proto.EventTypeFailed},
	}
	ts := pushEvents(t, evs, func(c *websocket.Conn) { <-make(chan struct{}) })

	n := 0
	err := client.New(ts.URL, "").FollowEvents(t.Context(), "t-all", true, 0,
		func(*proto.Event) error { n++; return nil }, nil)
	if err != nil {
		t.Fatalf("FollowEvents = %v, want nil", err)
	}
	if n != 3 {
		t.Fatalf("all=true 交付 %d 条, want 3（不过滤）", n)
	}
}

// snapAndPushServer 起一个既服务 GET /api/tasks/{id} 快照、又服务 /ws/events 的
// 假 agentd。每次 WS 连接会把握手带的 from_seq 记进 gotFromSeq。
//
// snaps 按连接次序取用：第 n 次 HTTP 快照请求取 snaps[min(n, len-1)]，
// 用于「第一次连接后积压、第二次连接前再对账」这类多阶段场景。
func snapAndPushServer(t *testing.T, snaps []*client.AttachInfo,
	evs []proto.Event, after func(*websocket.Conn)) (*httptest.Server, *[]string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	var mu sync.Mutex
	var gotFromSeq []string
	nth := 0
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		i := nth
		nth++
		mu.Unlock()
		if i >= len(snaps) {
			i = len(snaps) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snaps[i])
	})
	mux.HandleFunc("/ws/events", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotFromSeq = append(gotFromSeq, r.URL.Query().Get("from_seq"))
		mu.Unlock()
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		for _, ev := range evs {
			b, merr := json.Marshal(ev)
			if merr != nil {
				return
			}
			if werr := c.Write(r.Context(), websocket.MessageText, b); werr != nil {
				return
			}
		}
		after(c)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close() })
	return ts, &gotFromSeq
}

// TestFollowReconcilesBeforeConnect 验证首连前对账：吐一行摘要，且 WS 握手带的
// from_seq 是水位而不是 cursor——积压事件根本不被拉取。
func TestFollowReconcilesBeforeConnect(t *testing.T) {
	snap := &client.AttachInfo{
		Task:           proto.TaskView{Task: proto.Task{State: proto.TaskStateWaitingAnswer}},
		PendingTickets: []proto.Ticket{{ID: "new1", Kind: "gate"}},
		RecentEvents: []proto.Event{
			{Seq: 104, TaskID: "tk", Type: proto.EventTypePermissionRequest},
			{Seq: 109, TaskID: "tk", Type: proto.EventTypePermissionRequest},
		},
	}
	live := []proto.Event{{Seq: 200, TaskID: "tk", Type: proto.EventTypeFailed}}
	ts, fromSeqs := snapAndPushServer(t, []*client.AttachInfo{snap}, live,
		func(c *websocket.Conn) { <-make(chan struct{}) })

	var sums []*client.BacklogSummary
	var evSeqs []int64
	err := client.New(ts.URL, "").FollowEvents(t.Context(), "tk", false, 0,
		func(ev *proto.Event) error { evSeqs = append(evSeqs, ev.Seq); return nil },
		func(s *client.BacklogSummary) error { sums = append(sums, s); return nil })
	if err != nil {
		t.Fatalf("FollowEvents = %v, want nil", err)
	}
	if len(sums) != 1 {
		t.Fatalf("摘要 %d 行, want 1", len(sums))
	}
	if sums[0].ToSeq != 109 || sums[0].Missed != 2 {
		t.Fatalf("摘要 = %+v, want to_seq=109 missed=2", sums[0])
	}
	if len(*fromSeqs) != 1 || (*fromSeqs)[0] != "109" {
		t.Fatalf("WS from_seq = %v, want [109]（积压不该被拉取）", *fromSeqs)
	}
	if len(evSeqs) != 1 || evSeqs[0] != 200 {
		t.Fatalf("交付事件 = %v, want [200]（只有实时那条）", evSeqs)
	}
}

// TestFollowNoBacklogIsSilent 验证无积压时行为与改动前逐字一致：一行摘要都不吐，
// from_seq 仍是 cursor。
func TestFollowNoBacklogIsSilent(t *testing.T) {
	snap := &client.AttachInfo{RecentEvents: []proto.Event{
		{Seq: 0, TaskID: "tk2", Type: proto.EventTypeProgress},
	}}
	live := []proto.Event{{Seq: 1, TaskID: "tk2", Type: proto.EventTypeFailed}}
	ts, fromSeqs := snapAndPushServer(t, []*client.AttachInfo{snap}, live,
		func(c *websocket.Conn) { <-make(chan struct{}) })

	n := 0
	err := client.New(ts.URL, "").FollowEvents(t.Context(), "tk2", false, 0,
		func(*proto.Event) error { return nil },
		func(*client.BacklogSummary) error { n++; return nil })
	if err != nil {
		t.Fatalf("FollowEvents = %v, want nil", err)
	}
	if n != 0 {
		t.Fatalf("吐了 %d 行摘要, want 0（无积压不该加噪音）", n)
	}
	if len(*fromSeqs) != 1 || (*fromSeqs)[0] != "0" {
		t.Fatalf("WS from_seq = %v, want [0]", *fromSeqs)
	}
}

// TestFollowTerminalOnFailedSnapshot 验证：积压被跳过后，failed 由快照 state 接住，
// follow 不会挂在一个死任务上。
func TestFollowTerminalOnFailedSnapshot(t *testing.T) {
	snap := &client.AttachInfo{
		Task:         proto.TaskView{Task: proto.Task{State: proto.TaskStateFailed}},
		RecentEvents: []proto.Event{{Seq: 104, TaskID: "tk3", Type: proto.EventTypeFailed}},
	}
	ts, fromSeqs := snapAndPushServer(t, []*client.AttachInfo{snap}, nil,
		func(c *websocket.Conn) { <-make(chan struct{}) })

	n := 0
	err := client.New(ts.URL, "").FollowEvents(t.Context(), "tk3", false, 0,
		func(*proto.Event) error { return nil },
		func(*client.BacklogSummary) error { n++; return nil })
	if err != nil {
		t.Fatalf("FollowEvents = %v, want nil（failed 是正常终结）", err)
	}
	if n != 1 {
		t.Fatalf("摘要 %d 行, want 1", n)
	}
	if len(*fromSeqs) != 0 {
		t.Fatalf("快照已是 failed，不该再建 WS 连接，got %v", *fromSeqs)
	}
}

// TestFollowNilOnBacklogSkipsReconcile 验证 onBacklog 为 nil 时**完全跳过对账**。
//
// 为什么必须是「跳过对账」而不是「丢弃摘要」：后者会让积压事件既不被交付、
// 又无人知晓——事件无声消失是本项目最不能接受的失败形态。
func TestFollowNilOnBacklogSkipsReconcile(t *testing.T) {
	snap := &client.AttachInfo{RecentEvents: []proto.Event{
		{Seq: 104, TaskID: "tk4", Type: proto.EventTypePermissionRequest},
	}}
	live := []proto.Event{{Seq: 200, TaskID: "tk4", Type: proto.EventTypeFailed}}
	ts, fromSeqs := snapAndPushServer(t, []*client.AttachInfo{snap}, live,
		func(c *websocket.Conn) { <-make(chan struct{}) })

	err := client.New(ts.URL, "").FollowEvents(t.Context(), "tk4", false, 0,
		func(*proto.Event) error { return nil }, nil)
	if err != nil {
		t.Fatalf("FollowEvents = %v, want nil", err)
	}
	if len(*fromSeqs) != 1 || (*fromSeqs)[0] != "0" {
		t.Fatalf("WS from_seq = %v, want [0]（未对账，起点仍是 cursor）", *fromSeqs)
	}
}
