// agentd server 测试：HTTP API 鉴权、reply 回程闭环（WaitAnswer 解除 + 状态回 running）、WS 补发+实时流。
package agentd_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/xushixin/handoff/internal/agentd"
	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/executor/fake"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

const testToken = "test-token"

// taskDetail 是对 GET /api/tasks/{id} 响应线格式的独立断言（与实现内 struct 分处两处，保证契约一致）。
type taskDetail struct {
	Task           proto.Task     `json:"task"`
	PendingTickets []proto.Ticket `json:"pending_tickets"`
	RecentEvents   []proto.Event  `json:"recent_events"`
}

// testEnv 聚合 server 测试依赖：真实 SQLite store + httptest server，token 固定为 test-token。
type testEnv struct {
	srv   *agentd.Server
	ts    *httptest.Server
	st    *store.Store
	token string
}

// newTestEnv 构造完整测试环境，并注册 t.Cleanup 关闭 store 与 server。
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	return newTestEnvWithLogger(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// newTestEnvWithLogger 同 newTestEnv，但注入自定义 logger（供测试捕获服务端关键日志做
// 确定性同步信号）。
func newTestEnvWithLogger(t *testing.T, logger *slog.Logger) *testEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	srv := agentd.NewServer(&config.Config{Token: testToken}, st, logger)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &testEnv{srv: srv, ts: ts, st: st, token: testToken}
}

// signalHandler 是测试专用 slog.Handler：全量放行（含 Debug），每条日志先触发 on 回调
// 再转发给内部 handler（通常为 Discard）。用于把服务端日志变成测试的确定性信号。
type signalHandler struct {
	h  slog.Handler
	on func(slog.Record)
}

func (s *signalHandler) Enabled(ctx context.Context, level slog.Level) bool { return true }

func (s *signalHandler) Handle(ctx context.Context, r slog.Record) error {
	if s.on != nil {
		s.on(r)
	}
	return s.h.Handle(ctx, r)
}

func (s *signalHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &signalHandler{h: s.h.WithAttrs(attrs), on: s.on}
}

func (s *signalHandler) WithGroup(name string) slog.Handler {
	return &signalHandler{h: s.h.WithGroup(name), on: s.on}
}

// get 发起带正确 token 的 GET 请求。
func (e *testEnv) get(t *testing.T, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, e.ts.URL+path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

// post 发起带正确 token 的 POST 请求，body 为 JSON 文本。
func (e *testEnv) post(t *testing.T, path, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// TestAuthRequired 验证 Bearer 鉴权：无 token 401、错 token 401、对 token 200；WS 路由同样被拦截。
func TestAuthRequired(t *testing.T) {
	env := newTestEnv(t)

	resp, err := http.Get(env.ts.URL + "/api/tasks")
	if err != nil {
		t.Fatalf("GET 无 token: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无 token 返回 %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodGet, env.ts.URL+"/api/tasks", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET 错 token: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错 token 返回 %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// 对 token 应 200 且返回合法 JSON 数组（空列表）
	resp = env.get(t, "/api/tasks")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("对 token 返回 %d, want 200", resp.StatusCode)
	}
	defer resp.Body.Close()
	var tasks []proto.Task
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		t.Fatalf("解码任务列表: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("空库返回 %d 条任务, want 0", len(tasks))
	}

	// WS 路由同样要求 Bearer token：无 token 拨号应 401
	wsURL := "ws" + strings.TrimPrefix(env.ts.URL, "http") + "/ws/events?task=t1&from_seq=0"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, resp, err = websocket.Dial(ctx, wsURL, nil)
	if err == nil {
		t.Fatal("无 token 的 WS 拨号未报错")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无 token WS 返回 %v, want 401", resp)
	}
}

// TestReplyAnswersTicketAndNotifies 覆盖 reply 唤醒闭环：
// 预置 waiting_answer 任务 + 未答 ticket，goroutine 阻塞在 hub.WaitAnswer；
// POST reply 后 WaitAnswer 解除并拿到原文、任务回到 running、pending_tickets 清空、应答持久化。
func TestReplyAnswersTicketAndNotifies(t *testing.T) {
	env := newTestEnv(t)
	now := time.Now().UTC()
	taskID := "task-1"
	if err := env.st.CreateTask(&proto.Task{ID: taskID, Target: "opencode", RepoPath: "/repo", State: proto.TaskStatePending, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// 走合法状态链 pending → running → waiting_answer
	if err := env.st.UpdateTaskState(taskID, proto.TaskStateRunning); err != nil {
		t.Fatalf("→running: %v", err)
	}
	if err := env.st.UpdateTaskState(taskID, proto.TaskStateWaitingAnswer); err != nil {
		t.Fatalf("→waiting_answer: %v", err)
	}
	if _, err := env.st.CreateTicket(&proto.Ticket{ID: "tk-1", TaskID: taskID, Kind: "gate", Request: json.RawMessage(`{"kind":"bash","command":"rm -rf node_modules"}`), CreatedAt: now}); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	// 模拟 executor 侧阻塞等待该 ticket 的应答
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ansCh := make(chan string, 1)
	go func() {
		ans, err := env.srv.Hub().WaitAnswer(ctx, "tk-1")
		if err != nil {
			ansCh <- "ERR:" + err.Error()
			return
		}
		ansCh <- ans
	}()
	// 等 WaitAnswer 完成注册，保证「先 Wait 后 Notify」的时序
	time.Sleep(50 * time.Millisecond)

	resp := env.post(t, "/api/tasks/"+taskID+"/reply", `{"ticket_id":"tk-1","answer":"用 pgx 不用 gorm"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reply 返回 %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	// WaitAnswer 解除并拿到原文
	select {
	case ans := <-ansCh:
		if ans != "用 pgx 不用 gorm" {
			t.Fatalf("WaitAnswer 拿到 %q, want %q", ans, "用 pgx 不用 gorm")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitAnswer 未解除阻塞")
	}

	// 任务状态回到 running，attach 数据源的 pending_tickets 清空
	detailResp := env.get(t, "/api/tasks/"+taskID)
	if detailResp.StatusCode != http.StatusOK {
		t.Fatalf("GET detail 返回 %d, want 200", detailResp.StatusCode)
	}
	defer detailResp.Body.Close()
	var detail taskDetail
	if err := json.NewDecoder(detailResp.Body).Decode(&detail); err != nil {
		t.Fatalf("解码 detail: %v", err)
	}
	if detail.Task.State != proto.TaskStateRunning {
		t.Fatalf("state = %s, want running", detail.Task.State)
	}
	if len(detail.PendingTickets) != 0 {
		t.Fatalf("pending_tickets 仍残留 %d 条", len(detail.PendingTickets))
	}

	// 应答已持久化，可被后续 attach/继续会话读取
	tk, err := env.st.GetTicket("tk-1")
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if tk.Answer == nil || *tk.Answer != "用 pgx 不用 gorm" {
		t.Fatalf("ticket 应答未持久化: %+v", tk)
	}
}

// TestReplyUnknownTicket404 覆盖 reply 对未知/跨任务 ticket 的 404。
func TestReplyUnknownTicket404(t *testing.T) {
	env := newTestEnv(t)
	now := time.Now().UTC()
	taskID := "task-1"
	if err := env.st.CreateTask(&proto.Task{ID: taskID, Target: "opencode", RepoPath: "/repo", State: proto.TaskStatePending, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// 未知 ticket → 404
	resp := env.post(t, "/api/tasks/"+taskID+"/reply", `{"ticket_id":"ghost","answer":"x"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("未知 ticket 返回 %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()

	// 属于其他任务的 ticket 通过本任务 URL 回答 → 404（不跨任务操作）
	if _, err := env.st.CreateTicket(&proto.Ticket{ID: "tk-other", TaskID: "task-other", Kind: "gate", Request: json.RawMessage(`{}`), CreatedAt: now}); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	resp = env.post(t, "/api/tasks/"+taskID+"/reply", `{"ticket_id":"tk-other","answer":"x"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("跨任务 ticket 返回 %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestReplySelfHealsWithoutWaiter 覆盖 reply 自愈中继（agentd 重启后等待 goroutine
// 已消亡、/event 不重放历史的场景）：hub 无等待者时 handleReply 必须经
// manager.RelayAnswer 把应答直接回传 executor——gate 收到 once、ask 收到原文，
// 而不是把回答静默丢弃让 executor 永远阻塞。
func TestReplySelfHealsWithoutWaiter(t *testing.T) {
	env := newTestEnv(t)
	now := time.Now().UTC()
	taskID := "task-1"
	if err := env.st.CreateTask(&proto.Task{ID: taskID, Target: "opencode", RepoPath: "/repo", State: proto.TaskStatePending, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// 走合法状态链 pending → running → waiting_answer
	if err := env.st.UpdateTaskState(taskID, proto.TaskStateRunning); err != nil {
		t.Fatalf("→running: %v", err)
	}
	if err := env.st.UpdateTaskState(taskID, proto.TaskStateWaitingAnswer); err != nil {
		t.Fatalf("→waiting_answer: %v", err)
	}
	if _, err := env.st.CreateTicket(&proto.Ticket{ID: "tk-gate", TaskID: taskID, Kind: "gate",
		Request: json.RawMessage(`{"kind":"gate","permission":"Bash: go test ./..."}`), CreatedAt: now}); err != nil {
		t.Fatalf("CreateTicket gate: %v", err)
	}
	if _, err := env.st.CreateTicket(&proto.Ticket{ID: "tk-ask", TaskID: taskID, Kind: "ask",
		Request: json.RawMessage(`{"kind":"ask","question":"用哪个方案?"}`), CreatedAt: now}); err != nil {
		t.Fatalf("CreateTicket ask: %v", err)
	}

	// 注入 manager（真实 hub + fake executor）：reply 路由的自愈中继落点
	f := fake.New(nil)
	mgr := agentd.NewManager(env.st, env.srv.Hub(), f, &config.Config{Token: testToken, DataDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	env.srv.SetManager(mgr)

	// 全程无任何 WaitAnswer 等待者（模拟重启后等待 goroutine 已消亡）
	resp := env.post(t, "/api/tasks/"+taskID+"/reply", `{"ticket_id":"tk-gate","answer":"allow"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reply gate 返回 %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()
	eventually(t, 2*time.Second, "executor 收到 RespondPermission(once)", func() bool {
		perms := f.Perms()
		return len(perms) == 1 && perms[0].PermID == "tk-gate" && perms[0].Decision == "once"
	})

	resp = env.post(t, "/api/tasks/"+taskID+"/reply", `{"ticket_id":"tk-ask","answer":"单数"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reply ask 返回 %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()
	eventually(t, 2*time.Second, "executor 收到 Send(原文)", func() bool {
		sends := f.Sends()
		return len(sends) == 1 && sends[0].Text == "单数"
	})
}

// TestWSReplayWindowLiveNoLoss 是 P0-1 的回归测试：重放写循环进行期间（客户端不读、
// TCP 背压把重放写阻塞住）Publish 的事件必须被捕获并最终送达，不得丢失。
//
// 覆盖场景（旧实现必失败）：旧代码「先重放后订阅」，重放被背压阻塞期间 Publish 的
// question 无订阅者、被 hub 直接丢弃——任务随即进入 waiting_answer 不再产出事件，
// 客户端连接健康不会重连，审核者永远不被唤醒。新实现「先订阅后重放」，重放期间的
// 实时事件由排空器收集、按 seq 归并补出。
//
// 判定逻辑（确定性优先，不依赖机器 TCP 缓冲大小与调度速度）：
//   - 确定性信号：服务端在开始写重放前打 Debug 日志「WS 重放开始」（发生在订阅与
//     store 读之后、重放写之前）。测试通过 signalHandler 捕获该日志后立即 Publish，
//     此时必然处于「已订阅 + 重放写进行中」，question 必走归并路径——旧代码没有
//     该日志（且重放后才订阅），测试在信号等待处即失败，不依赖背压时序
//   - 积压量 10000 条（约 1.5MB）：取 eventReplayLimit 上限——超过会被补发截断、
//     破坏「seq 严格连续」断言；1.5MB 超过常见内核回环发送缓冲（Linux/macOS 默认
//     均在 MB 级以下），客户端不读时重放写会被背压阻塞，忠实复现 P0-1 的「重放长
//     时间阻塞」场景；即便内核缓冲异常超大（重放写不被阻塞），信号也保证 question
//     落在重放阶段内，测试仍真实覆盖归并路径
//   - 断言 seq 1..10001 严格连续到达（无丢失、无重复、无乱序），question 必达
func TestWSReplayWindowLiveNoLoss(t *testing.T) {
	// 捕获服务端「WS 重放开始」Debug 日志作为确定性信号
	replayStarted := make(chan struct{}, 1)
	env := newTestEnvWithLogger(t, slog.New(&signalHandler{
		h: slog.NewTextHandler(io.Discard, nil),
		on: func(r slog.Record) {
			if r.Message == "WS 重放开始" {
				select {
				case replayStarted <- struct{}{}:
				default:
				}
			}
		},
	}))
	taskID := "t1"
	// 10000 条积压历史（~1.5MB，= eventReplayLimit 上限）：让重放写循环「需时明显」——
	// 客户端不读时 TCP 缓冲填满、服务端阻塞在重放写，为「重放期间 Publish」制造
	// 确定性的时间窗口
	const backlog = 10000
	for i := 1; i <= backlog; i++ {
		if _, err := env.st.AppendEvent(taskID, proto.EventTypeProgress, map[string]any{"n": i}); err != nil {
			t.Fatalf("AppendEvent %d: %v", i, err)
		}
	}

	wsURL := "ws" + strings.TrimPrefix(env.ts.URL, "http") + "/ws/events?task=" + taskID + "&from_seq=0"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + env.token}},
	})
	if err != nil {
		if resp != nil {
			t.Fatalf("WS 拨号失败 status=%d: %v", resp.StatusCode, err)
		}
		t.Fatalf("WS 拨号失败: %v", err)
	}
	defer conn.CloseNow()

	// 等确定性信号：收到「WS 重放开始」= 订阅已完成 + 重放写循环已开始，此刻
	// Publish 必落在重放/归并阶段（取代固定 sleep，不依赖机器速度）
	select {
	case <-replayStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("服务端未进入重放阶段（未收到 WS 重放开始日志）——重放路径未被覆盖")
	}

	// 重放期间 Publish 一条 question（seq 10001，未落库——只验证实时通道侧不丢）
	env.srv.Hub().Publish(proto.Event{Seq: backlog + 1, TaskID: taskID,
		Type: proto.EventTypeQuestion, Payload: json.RawMessage(`{"q":"重放期间的问题"}`)})

	// 恢复读取：重放事件 + question 必须按 seq 1..10001 连续到达
	var (
		last   int64
		count  int
		qGot   bool
		maxSeq = int64(backlog + 1)
	)
	for count < backlog+1 {
		_, b, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("读到 %d 条后 Read 失败: %v", count, err)
		}
		var ev proto.Event
		if err := json.Unmarshal(b, &ev); err != nil {
			t.Fatalf("解码事件失败: %v", err)
		}
		// seq 连续性：与预期严格递增一致，无丢失、无重复、无乱序
		if ev.Seq != last+1 {
			t.Fatalf("seq 不连续: 收到 %d（第 %d 条）, want %d——重放/实时归并存在丢失或乱序", ev.Seq, count+1, last+1)
		}
		if ev.Seq == maxSeq {
			if ev.Type != proto.EventTypeQuestion {
				t.Fatalf("最后一条 seq=%d 类型=%s, want question", ev.Seq, ev.Type)
			}
			if string(ev.Payload) != `{"q":"重放期间的问题"}` {
				t.Fatalf("question payload=%s, want {\"q\":\"重放期间的问题\"}", ev.Payload)
			}
			qGot = true
		}
		last = ev.Seq
		count++
	}
	if !qGot {
		t.Fatal("重放期间 Publish 的 question 未收到——窗口期事件被丢失")
	}
	t.Logf("全部 %d 条事件按 seq 1..%d 连续到达（含重放期间的 question）", count, maxSeq)
}

// TestWSReplayThenLive 覆盖 WS 事件流：from_seq=0 先补发 store 中全部历史事件，随后 Publish 实时到达。
func TestWSReplayThenLive(t *testing.T) {
	env := newTestEnv(t)
	taskID := "t1"
	// 预置两条历史事件
	for i := 1; i <= 2; i++ {
		if _, err := env.st.AppendEvent(taskID, proto.EventTypeProgress, map[string]any{"n": i}); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}

	wsURL := "ws" + strings.TrimPrefix(env.ts.URL, "http") + "/ws/events?task=" + taskID + "&from_seq=0"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + env.token}},
	})
	if err != nil {
		if resp != nil {
			t.Fatalf("WS 拨号失败 status=%d: %v", resp.StatusCode, err)
		}
		t.Fatalf("WS 拨号失败: %v", err)
	}
	defer conn.CloseNow()

	// 读 goroutine：把收到的每条事件（JSON 文本帧）解码后投递到通道
	type got struct {
		seq     int64
		payload string
	}
	gotCh := make(chan got, 8)
	go func() {
		for {
			_, b, err := conn.Read(ctx)
			if err != nil {
				close(gotCh)
				return
			}
			var ev proto.Event
			if err := json.Unmarshal(b, &ev); err != nil {
				gotCh <- got{seq: -1, payload: "unmarshal: " + err.Error()}
				continue
			}
			gotCh <- got{seq: ev.Seq, payload: string(ev.Payload)}
		}
	}()

	// 补发阶段：两条历史事件按 seq 升序到达
	for i := int64(1); i <= 2; i++ {
		select {
		case g := <-gotCh:
			if g.seq != i {
				t.Fatalf("补发第 %d 条 seq=%d, want %d", i, g.seq, i)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("补发超时，未收到 seq=%d", i)
		}
	}

	// 实时阶段：hub 广播一条新事件应被推送
	env.srv.Hub().Publish(proto.Event{Seq: 3, TaskID: taskID, Type: proto.EventTypeProgress, Payload: json.RawMessage(`{"live":true}`)})
	select {
	case g := <-gotCh:
		if g.seq != 3 {
			t.Fatalf("实时事件 seq=%d, want 3", g.seq)
		}
		if g.payload != `{"live":true}` {
			t.Fatalf("实时事件 payload=%s, want {\"live\":true}", g.payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未收到实时事件")
	}
}
