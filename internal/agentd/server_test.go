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
	st, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := agentd.NewServer(&config.Config{Token: testToken}, st, logger)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &testEnv{srv: srv, ts: ts, st: st, token: testToken}
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
