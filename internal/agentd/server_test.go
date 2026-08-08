// agentd server 测试：HTTP API 鉴权、reply 回程闭环（WaitAnswer 解除 + 状态回 running）、WS 补发+实时流。
package agentd_test

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/xushixin/handoff/internal/executor"
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
	return newTestEnvWithCfg(t, &config.Config{Token: testToken}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// newTestEnvWithLogger 同 newTestEnv，但注入自定义 logger（供测试捕获服务端关键日志做
// 确定性同步信号）。
func newTestEnvWithLogger(t *testing.T, logger *slog.Logger) *testEnv {
	t.Helper()
	return newTestEnvWithCfg(t, &config.Config{Token: testToken}, logger)
}

// newTestEnvWithCfg 同 newTestEnv，但注入自定义配置（覆盖 token 为空等边界场景）。
func newTestEnvWithCfg(t *testing.T, cfg *config.Config, logger *slog.Logger) *testEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	srv := agentd.NewServer(cfg, st, logger)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &testEnv{srv: srv, ts: ts, st: st, token: cfg.Token}
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

// TestAuthEmptyTokenFailsClosed 覆盖 L-2：配置 token 为空时（手写配置漏掉 token），
// 任何请求都必须 401——旧实现 ConstantTimeCompare("","")==1 会让空 token 请求
// 通过鉴权（隐性 fail-open），且服务端必须打 Error 日志「token 未配置」提示
// 运维修复配置，而不是静默放行。
func TestAuthEmptyTokenFailsClosed(t *testing.T) {
	// 捕获服务端 Error 日志：断言空 token 分支打了「token 未配置」错误
	noTokenLogged := make(chan struct{}, 1)
	env := newTestEnvWithCfg(t, &config.Config{Token: ""}, slog.New(&signalHandler{
		h: slog.NewTextHandler(io.Discard, nil),
		on: func(r slog.Record) {
			if r.Message == "token 未配置，拒绝一切请求（fail-closed）：请在配置中设置 token 后重启 agentd" {
				select {
				case noTokenLogged <- struct{}{}:
				default:
				}
			}
		},
	}))

	// 无 token 的请求 → 401
	resp, err := http.Get(env.ts.URL + "/api/tasks")
	if err != nil {
		t.Fatalf("GET 无 token: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("空配置 token 下无 token 请求返回 %d, want 401（fail-closed）", resp.StatusCode)
	}
	resp.Body.Close()

	// 带任意 token（含空串）的请求同样 → 401：空配置 token 下不存在合法请求
	for _, tk := range []string{"anything", ""} {
		req, _ := http.NewRequest(http.MethodGet, env.ts.URL+"/api/tasks", nil)
		req.Header.Set("Authorization", "Bearer "+tk)
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET token=%q: %v", tk, err)
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("空配置 token 下 token=%q 请求返回 %d, want 401", tk, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// 服务端已打 Error 日志：运维有明确线索修复配置
	select {
	case <-noTokenLogged:
	case <-time.After(2 * time.Second):
		t.Fatal("空 token 拒绝时未打「token 未配置」Error 日志")
	}
}

// TestAttachEmptyListsAreArrays 覆盖 L-7：任务无工单无事件时，attach 数据源
// （GET /api/tasks/{id}）的 pending_tickets/recent_events 必须序列化为 [] 而非
// null——旧实现 nil slice 被 Go 序列化成 null，按数组解码迭代的客户端（attach
// 命令）会踩到 nil。断言原始响应文本（契约是线格式，不是 Go 结构体）。
func TestAttachEmptyListsAreArrays(t *testing.T) {
	env := newTestEnv(t)
	now := time.Now().UTC()
	taskID := "task-empty"
	if err := env.st.CreateTask(&proto.Task{ID: taskID, Target: "opencode", RepoPath: "/repo",
		State: proto.TaskStatePending, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	resp := env.get(t, "/api/tasks/"+taskID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET detail 返回 %d, want 200", resp.StatusCode)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读响应体: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, `"pending_tickets":[]`) {
		t.Fatalf("pending_tickets 应为 [], got %s", body)
	}
	if !strings.Contains(body, `"recent_events":[]`) {
		t.Fatalf("recent_events 应为 [], got %s", body)
	}
	if strings.Contains(body, "null") {
		t.Fatalf("响应不得含 null 序列化（空列表契约）, got %s", body)
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
	// 有等待者命中：relayed=true（中继未走，唤醒即成功）
	var rbody struct {
		OK      bool `json:"ok"`
		Relayed bool `json:"relayed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rbody); err != nil {
		t.Fatalf("解码 reply 响应: %v", err)
	}
	resp.Body.Close()
	if !rbody.OK || !rbody.Relayed {
		t.Fatalf("reply 响应 = %+v, want ok=true relayed=true", rbody)
	}

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
	mgr := agentd.NewManager(env.st, env.srv.Hub(), map[string]executor.Adapter{"fake": f},
		&config.Config{Token: testToken, DataDir: t.TempDir(), Executor: config.ExecutorConfig{Default: "fake"}},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	env.srv.SetManager(mgr)

	// 全程无任何 WaitAnswer 等待者（模拟重启后等待 goroutine 已消亡）
	resp := env.post(t, "/api/tasks/"+taskID+"/reply", `{"ticket_id":"tk-gate","answer":"allow"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reply gate 返回 %d, want 200", resp.StatusCode)
	}
	// 中继成功：relayed=true（P0-5 后响应体携带中继结果，200 表示已送达）
	var rbody struct {
		OK      bool `json:"ok"`
		Relayed bool `json:"relayed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rbody); err != nil {
		t.Fatalf("解码 reply 响应: %v", err)
	}
	resp.Body.Close()
	if !rbody.OK || !rbody.Relayed {
		t.Fatalf("reply gate 响应 = %+v, want ok=true relayed=true", rbody)
	}
	eventually(t, 2*time.Second, "executor 收到 RespondPermission(once)", func() bool {
		perms := f.Perms()
		return len(perms) == 1 && perms[0].PermID == "tk-gate" && perms[0].Decision == "once"
	})

	resp = env.post(t, "/api/tasks/"+taskID+"/reply", `{"ticket_id":"tk-ask","answer":"单数"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reply ask 返回 %d, want 200", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&rbody); err != nil {
		t.Fatalf("解码 reply 响应: %v", err)
	}
	resp.Body.Close()
	if !rbody.OK || !rbody.Relayed {
		t.Fatalf("reply ask 响应 = %+v, want ok=true relayed=true", rbody)
	}
	eventually(t, 2*time.Second, "executor 收到 Send(原文)", func() bool {
		sends := f.Sends()
		return len(sends) == 1 && sends[0].Text == "单数"
	})
}

// TestReplyRelayFailureReturns502 覆盖 P0-5：中继失败必须进响应体而非只有日志。
// 场景：无等待者（agentd 重启后等待 goroutine 已消亡）且 adapter 无该任务运行态
// （executor 已不在）——回答落库后中继必失败。断言：
//   - 502 + {"ok":true,"relayed":false,"reason":...}，reason 含失败原因，
//     审核者在 CLI 立即看到 executor 没收到（而非只有 agentd.log 一行）
//   - 任务保持 waiting_answer 不回迁 running：executor 未收到应答、未恢复执行，
//     标 running 是虚假状态；waiting_answer 保留下次 agentd 重启时
//     RecoverOnStartup 的探活恢复路径
//   - 回答已落库不可回滚：二次 reply 404（与「回滚为未应答」方案的区别所在——
//     应答是「审核者裁决过」的持久审计事实）
func TestReplyRelayFailureReturns502(t *testing.T) {
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
		t.Fatalf("CreateTicket: %v", err)
	}

	// 注入 manager：fake 中继必失败（SetPermError 模拟 adapter 无该任务运行态，
	// 与 opencode adapter 的「任务不在运行中」错误同构，见 adapter.go lookup 判空）
	f := fake.New(nil)
	f.SetPermError(fmt.Errorf("任务 %s 不在运行中", taskID))
	mgr := agentd.NewManager(env.st, env.srv.Hub(), map[string]executor.Adapter{"fake": f},
		&config.Config{Token: testToken, DataDir: t.TempDir(), Executor: config.ExecutorConfig{Default: "fake"}},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	env.srv.SetManager(mgr)

	resp := env.post(t, "/api/tasks/"+taskID+"/reply", `{"ticket_id":"tk-gate","answer":"allow"}`)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("中继失败返回 %d, want 502", resp.StatusCode)
	}
	defer resp.Body.Close()
	var body struct {
		OK      bool   `json:"ok"`
		Relayed bool   `json:"relayed"`
		Reason  string `json:"reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("解码 reply 响应: %v", err)
	}
	if !body.OK || body.Relayed {
		t.Fatalf("响应 = %+v, want ok=true relayed=false", body)
	}
	if !strings.Contains(body.Reason, "不在运行中") {
		t.Fatalf("reason 应含中继失败原因, got %q", body.Reason)
	}

	// 任务保持 waiting_answer：executor 未收到应答未恢复执行，不回迁 running
	task, err := env.st.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.State != proto.TaskStateWaitingAnswer {
		t.Fatalf("中继失败后 state = %s, want waiting_answer（保留恢复路径）", task.State)
	}

	// 回答已落库不可回滚：二次 reply 404（answer IS NULL 守卫已消耗）
	resp2 := env.post(t, "/api/tasks/"+taskID+"/reply", `{"ticket_id":"tk-gate","answer":"allow"}`)
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("二次 reply 返回 %d, want 404（回答已落库）", resp2.StatusCode)
	}
	resp2.Body.Close()
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
	// 任务行必须先存在（P0-2 后服务端对不存在的任务直接 PolicyViolation 关闭 WS），
	// 本测试覆盖的是重放+实时归并，任务不存在分支由 TestWSTaskNotFoundClosesPolicyViolation 单独覆盖
	if err := env.st.CreateTask(&proto.Task{ID: taskID, Target: "opencode", RepoPath: "/repo",
		State: proto.TaskStatePending, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
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

// TestWSTaskNotFoundClosesPolicyViolation 覆盖 P0-2 的服务端半边：
// 订阅不存在的任务（打错 task-id）时，服务端必须以 PolicyViolation（1008）
// close 码关闭连接——语义是「请求本身非法」，客户端据此识别为永久性失败
// 立即报错，而不是把「任务不存在」误当成瞬时断网无限退避重连。
func TestWSTaskNotFoundClosesPolicyViolation(t *testing.T) {
	env := newTestEnv(t)

	wsURL := "ws" + strings.TrimPrefix(env.ts.URL, "http") + "/ws/events?task=no-such-task&from_seq=0"
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

	// 服务端应立即以 PolicyViolation close 关闭连接；Read 返回 CloseError
	if _, _, err := conn.Read(ctx); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("任务不存在应收到 PolicyViolation(1008) close, got %v", err)
	}
}

// TestWSReplayThenLive 覆盖 WS 事件流：from_seq=0 先补发 store 中全部历史事件，随后 Publish 实时到达。
func TestWSReplayThenLive(t *testing.T) {
	env := newTestEnv(t)
	taskID := "t1"
	// 任务行必须先存在（P0-2 后服务端对不存在的任务直接关闭 WS，见
	// TestWSTaskNotFoundClosesPolicyViolation——本测试覆盖的是补发+实时路径）
	if err := env.st.CreateTask(&proto.Task{ID: taskID, Target: "opencode", RepoPath: "/repo",
		State: proto.TaskStatePending, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
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
