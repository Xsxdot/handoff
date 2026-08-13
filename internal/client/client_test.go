// client 包测试：对着 httptest 起的真 agentd Server 验证 ListTasks/Attach/Reply/WaitEvent。
//
// 关键约定：
//   - 全部测试用 t.Setenv("HOME", t.TempDir()) 重定向用户主目录，cursor 文件落在
//     $HOME/.handoff/cursors/<agentd>/<task>，断言与清理都在测试沙箱内完成，不污染真实主目录
//   - WaitEvent 的断线重连测试需要「同地址重启」：用 net.Listen("tcp","127.0.0.1:0")
//     先占一个固定端口，关闭后用同一地址重新 Listen
package client_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/agentd"
	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/fake"
	"github.com/xushixin/handoff/internal/permgate"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

const testToken = "test-token"

// newTestEnv 构造测试环境：真实 SQLite store + httptest 起的真 agentd Server，
// 并把 HOME 指向临时目录（cursor 落盘位置的依赖）。
type newTestEnv struct {
	srv   *agentd.Server
	ts    *httptest.Server
	st    *store.Store
	home  string
	token string
}

// newTestClientEnv 组装完整测试环境并注册清理。
func newTestClientEnv(t *testing.T) *newTestEnv {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	st, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := agentd.NewServer(&config.Config{Token: testToken}, st, logger)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &newTestEnv{srv: srv, ts: ts, st: st, home: home, token: testToken}
}

// createPendingTask 预置一个 pending 任务并返回其 ID。
func (e *newTestEnv) createPendingTask(t *testing.T) string {
	t.Helper()
	now := time.Now().UTC()
	id := "task-1"
	if err := e.st.CreateTask(&proto.Task{ID: id, Target: "opencode", RepoPath: "/repo", State: proto.TaskStatePending, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return id
}

// cursorPath 返回测试期望的 cursor 文件路径（与 client 实现同规则：
// $HOME/.handoff/cursors/<agentd>/<task>，<agentd> 是 host:port 折成的路径段）。
func (e *newTestEnv) cursorPath(taskID string) string {
	return filepath.Join(e.home, ".handoff", "cursors", cursorNS(e.ts.URL), taskID)
}

// cursorNS 把 agentd 地址折成路径段（与 client 内部 cursorNamespace 同规则，
// 外部测试包无法访问未导出函数，故在此复刻一份）。
func cursorNS(addr string) string {
	u := strings.TrimPrefix(strings.TrimPrefix(addr, "http://"), "https://")
	var b strings.Builder
	for _, r := range u {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// readCursor 读取 cursor 文件内容；不存在时返回空串。
func (e *newTestEnv) readCursor(t *testing.T, taskID string) string {
	t.Helper()
	b, err := os.ReadFile(e.cursorPath(taskID))
	if err != nil {
		t.Fatalf("读取 cursor 文件: %v", err)
	}
	return string(b)
}

// TestListTasksAndAttach 覆盖 ListTasks 与 Attach：创建任务与未答工单后，
// tasks 列表能查到、attach 能取回 pending_tickets（恢复现场的数据源）。
func TestListTasksAndAttach(t *testing.T) {
	env := newTestClientEnv(t)
	taskID := env.createPendingTask(t)
	now := time.Now().UTC()
	if _, err := env.st.CreateTicket(&proto.Ticket{ID: "tk-1", TaskID: taskID, Kind: "gate", Request: json.RawMessage(`{"kind":"bash","command":"go test ./..."}`), CreatedAt: now}); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	cl := client.New(env.ts.URL, env.token)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tasks, err := cl.ListTasks(ctx)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != taskID {
		t.Fatalf("ListTasks 返回 %+v, want 1 条 %s", tasks, taskID)
	}

	info, err := cl.Attach(ctx, taskID)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if info.Task.ID != taskID {
		t.Fatalf("Attach.Task.ID = %s, want %s", info.Task.ID, taskID)
	}
	if len(info.PendingTickets) != 1 || info.PendingTickets[0].ID != "tk-1" {
		t.Fatalf("PendingTickets = %+v, want [tk-1]", info.PendingTickets)
	}
}

// TestWaitEventSkipsProgress 覆盖 cursor 语义与 progress 不唤醒：
// 预置 progress + question 两条事件，WaitEvent 应跳过 progress、返回 question，
// 且 cursor 落盘为 question 的 seq（下次 wait 从该 seq 之后继续，不重不丢）。
func TestWaitEventSkipsProgress(t *testing.T) {
	env := newTestClientEnv(t)
	taskID := "t1"
	// 任务行必须先存在：P0-2 后服务端对不存在的任务直接 PolicyViolation 关闭
	// WS（打错 task-id 的永久错误），本测试要覆盖的是 cursor 语义而非该分支
	if err := env.st.CreateTask(&proto.Task{ID: taskID, Target: "opencode", RepoPath: "/repo",
		State: proto.TaskStatePending, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	pv, err := env.st.AppendEvent(taskID, proto.EventTypeProgress, map[string]any{"n": 1})
	if err != nil {
		t.Fatalf("AppendEvent progress: %v", err)
	}
	qv, err := env.st.AppendEvent(taskID, proto.EventTypeQuestion, map[string]any{"text": "用哪个方案?"})
	if err != nil {
		t.Fatalf("AppendEvent question: %v", err)
	}
	if pv.Seq >= qv.Seq {
		t.Fatalf("事件 seq 异常: progress=%d question=%d", pv.Seq, qv.Seq)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ev, err := client.New(env.ts.URL, env.token).WaitEvent(ctx, taskID, false)
	if err != nil {
		t.Fatalf("WaitEvent: %v", err)
	}
	if ev.Seq != qv.Seq {
		t.Fatalf("WaitEvent 返回 seq=%d, want %d（question 的 seq）", ev.Seq, qv.Seq)
	}
	if ev.Type != proto.EventTypeQuestion {
		t.Fatalf("WaitEvent 返回 type=%s, want question", ev.Type)
	}

	// cursor 落盘为 question 的 seq
	if got := env.readCursor(t, taskID); got != strconv.FormatInt(qv.Seq, 10) {
		t.Fatalf("cursor 内容 = %q, want %d", got, qv.Seq)
	}
}

// trackListener 包装 net.Listener，记录每次 Accept 得到的连接，提供 closeAll 强杀全部连接。
//
// 为什么需要它：http.Server.Close 不关闭已 hijack 的 WS 连接（net/http 对 hijack 后的
// 连接不再跟踪），而测试要模拟「agentd 崩溃」——必须从 TCP 层把已建立的连接全部断开，
// 客户端才会感知断线并进入退避重连。
type trackListener struct {
	net.Listener
	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

func newTrackListener(t *testing.T) *trackListener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	return &trackListener{Listener: ln, conns: make(map[net.Conn]struct{})}
}

func (l *trackListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.conns[c] = struct{}{}
	l.mu.Unlock()
	return c, nil
}

// closeAll 强制关闭本 listener 接受过的全部连接（含已被 http.Server hijack 的 WS 连接）。
func (l *trackListener) closeAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for c := range l.conns {
		c.Close()
	}
}

// TestWaitEventReconnect 覆盖断线指数退避重连：
// 先在固定地址起 server（store 预置 progress 事件），WaitEvent 连上并消费 progress 后，
// 从 TCP 层强杀连接模拟 agentd 崩溃；同地址重启 server 并落库 + Publish 一条 question，
// WaitEvent 应在退避重连后拿到它。
//
// 为什么 question 同时落库与广播：客户端可能仍在退避沉睡，实时广播会错过，
// 但 store 落库保证重连后的 WS 补发（from_seq=cursor）一定把它送达——测试因此确定。
func TestWaitEventReconnect(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	st, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	taskID := "t1"
	// 任务行必须先存在（P0-2 后服务端对不存在的任务直接关闭 WS），
	// 本测试要覆盖的是断线重连而非任务不存在分支
	if err := st.CreateTask(&proto.Task{ID: taskID, Target: "opencode", RepoPath: "/repo",
		State: proto.TaskStatePending, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := st.AppendEvent(taskID, proto.EventTypeProgress, map[string]any{"n": 1}); err != nil {
		t.Fatalf("AppendEvent progress: %v", err)
	}

	// 先占一个固定地址，起第一台 server
	ln := newTrackListener(t)
	addr := "http://" + ln.Addr().String()
	srv1 := agentd.NewServer(&config.Config{Token: testToken}, st, logger)
	hs1 := &http.Server{Handler: srv1.Handler()}
	done1 := make(chan struct{})
	go func() {
		hs1.Serve(ln)
		close(done1)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	type result struct {
		ev  *proto.Event
		err error
	}
	gotCh := make(chan result, 1)
	go func() {
		ev, err := client.New(addr, testToken).WaitEvent(ctx, taskID, false)
		gotCh <- result{ev: ev, err: err}
	}()

	// 等首次连接建立并消费掉 progress（progress 被跳过，WaitEvent 保持阻塞等待可动作事件）
	time.Sleep(300 * time.Millisecond)
	// 模拟 agentd 崩溃：停 accept + 强杀全部已建立连接
	hs1.Close()
	ln.closeAll()
	<-done1

	// 同地址重启（客户端 1s 首退避，足够完成重启）
	ln2, err := net.Listen("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("同地址重启 Listen: %v", err)
	}
	srv2 := agentd.NewServer(&config.Config{Token: testToken}, st, logger)
	hs2 := &http.Server{Handler: srv2.Handler()}
	go hs2.Serve(ln2)
	t.Cleanup(func() { hs2.Close() })

	// 落库 + 广播 question：无论客户端重连后走补发还是实时路径都能拿到
	qev, err := st.AppendEvent(taskID, proto.EventTypeQuestion, map[string]any{"text": "重连后的问题"})
	if err != nil {
		t.Fatalf("AppendEvent question: %v", err)
	}
	srv2.Hub().Publish(qev)

	select {
	case g := <-gotCh:
		if g.err != nil {
			t.Fatalf("WaitEvent 重连后报错: %v", g.err)
		}
		if g.ev.Type != proto.EventTypeQuestion {
			t.Fatalf("重连后拿到 type=%s, want question", g.ev.Type)
		}
		if g.ev.Seq != qev.Seq {
			t.Fatalf("重连后拿到 seq=%d, want %d", g.ev.Seq, qev.Seq)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("断线重连后未拿到 question 事件")
	}
}

// TestReplyRoundTrip 覆盖 reply → attach 闭环：
// 回答工单后，attach 的 pending_tickets 应清空（恢复现场时不再出现已处理的工单）。
func TestReplyRoundTrip(t *testing.T) {
	env := newTestClientEnv(t)
	taskID := env.createPendingTask(t)
	now := time.Now().UTC()
	if _, err := env.st.CreateTicket(&proto.Ticket{ID: "tk-1", TaskID: taskID, Kind: "gate", Request: json.RawMessage(`{"kind":"gate","permission":"Bash: go test ./..."}`), CreatedAt: now}); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	// 注入 manager（fake 中继必成功）：本测试要验证 reply 成功路径的闭环
	// （pending_tickets 清空）；无 manager 时 reply 会按 P0-5 语义返回 502
	// （回答落库但无中继落点），那是 TestReplyRelayFailureSurfacesReason 的覆盖面
	gate, gerr := permgate.New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if gerr != nil {
		t.Fatalf("permgate.New: %v", gerr)
	}
	mgr := agentd.NewManager(env.st, env.srv.Hub(), map[string]executor.Adapter{"fake": fake.New(nil)},
		&config.Config{Token: env.token, DataDir: t.TempDir(), Executor: config.ExecutorConfig{Default: "fake"}},
		nil,
		gate,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	env.srv.SetManager(mgr)
	cl := client.New(env.ts.URL, env.token)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := cl.Reply(ctx, taskID, "tk-1", "allow"); err != nil {
		t.Fatalf("Reply: %v", err)
	}

	info, err := cl.Attach(ctx, taskID)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if len(info.PendingTickets) != 0 {
		t.Fatalf("reply 后 pending_tickets 仍残留 %d 条", len(info.PendingTickets))
	}
}

// TestReplyRelayFailureSurfacesReason 覆盖 P0-5 的客户端可见性：回答已落库但
// executor 侧递送失败（无等待者且 manager 未注入，应答未回传 executor）时
// agentd 返回 502，client.Reply 的错误信息必须携带状态码与 reason——协调者在
// CLI 立即看到「executor 没收到」及原因，而不是只有远端 agentd.log 一行。
func TestReplyRelayFailureSurfacesReason(t *testing.T) {
	env := newTestClientEnv(t)
	taskID := env.createPendingTask(t)
	now := time.Now().UTC()
	if err := env.st.UpdateTaskState(taskID, proto.TaskStateRunning); err != nil {
		t.Fatalf("→running: %v", err)
	}
	if err := env.st.UpdateTaskState(taskID, proto.TaskStateWaitingAnswer); err != nil {
		t.Fatalf("→waiting_answer: %v", err)
	}
	if _, err := env.st.CreateTicket(&proto.Ticket{ID: "tk-1", TaskID: taskID, Kind: "gate", Request: json.RawMessage(`{"kind":"gate","permission":"Bash: rm -rf node_modules"}`), CreatedAt: now}); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	// 未注入 manager（agentd bootstrap 窗口语义）：回答落库后无等待者、
	// 无中继落点 → 502 + reason
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := client.New(env.ts.URL, env.token).Reply(ctx, taskID, "tk-1", "allow")
	if err == nil {
		t.Fatal("Reply 对中继失败应返回错误（非 2xx）")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Fatalf("错误应含 502 状态码（非 2xx 才让 CLI 非零退出）, got: %v", err)
	}
	if !strings.Contains(err.Error(), "manager 未注入") {
		t.Fatalf("错误应含中继失败原因（响应体并入错误信息）, got: %v", err)
	}
}

// TestWaitEventWrongTokenFailsFast 覆盖 P0-2 的握手 401 快速失败：
// token 未同步是文档写明的手工配对步骤（最常见的配置错误），旧实现会无限退避重连、
// 与「还没有事件」无法区分；新实现握手 401 属永久性失败，WaitEvent 立即返回错误。
//
// 判定：错误必须在首轮退避（1s）内返回——若旧实现仍把 401 当瞬时错误，
// 至少会先睡 1s 才第二次拨号，此断言即失败。
func TestWaitEventWrongTokenFailsFast(t *testing.T) {
	env := newTestClientEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err := client.New(env.ts.URL, "wrong-token").WaitEvent(ctx, "t1", false)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("错 token 的 WaitEvent 应返回错误")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("错误应含 401 状态码, got %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("错 token 应在退避前立即返回, 实际耗时 %v（疑似仍在退避重连）", elapsed)
	}
}

// TestWaitEventTaskNotFoundFailsFast 覆盖 P0-2 的「打错 task-id 永久阻塞」：
// 服务端对不存在的任务以 PolicyViolation（1008）close 码关闭连接，客户端应
// 把它识别为永久性失败立即返回（错误信息含 close reason），而非无限重连。
//
// 判定：错误必须含 1008 与 reason「task not found」，且在首轮退避（1s）内返回。
func TestWaitEventTaskNotFoundFailsFast(t *testing.T) {
	env := newTestClientEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err := client.New(env.ts.URL, env.token).WaitEvent(ctx, "no-such-task", false)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("不存在的任务 WaitEvent 应返回错误")
	}
	// CloseError 的 Error() 用枚举名而非数字（StatusPolicyViolation），
	// reason 带服务端写的「task not found」——两者都要在错误信息里
	if !strings.Contains(err.Error(), "PolicyViolation") || !strings.Contains(err.Error(), "task not found") {
		t.Fatalf("错误应含 PolicyViolation 与 reason, got %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("任务不存在应在退避前立即返回, 实际耗时 %v（疑似仍在退避重连）", elapsed)
	}
}

// TestWaitEventTimeout 覆盖 --timeout 的底层语义：ctx 带 deadline 且任务无事件时，
// WaitEvent 返回 ctx.Err()（DeadlineExceeded）——cmd 层据此转成非 0 退出，
// 与「事件到达退出 0」可区分（区别于旧的无限挂起）。
func TestWaitEventTimeout(t *testing.T) {
	env := newTestClientEnv(t)
	taskID := env.createPendingTask(t) // 任务存在但没有任何事件

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := client.New(env.ts.URL, env.token).WaitEvent(ctx, taskID, false)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("无事件 + deadline 应返回 DeadlineExceeded, got %v", err)
	}
}

// TestHTTPErrorLevelsByStatus 验证 4xx 打 Warn、5xx 打 Error。
// why：任务不存在（404）是预期内的客户端错误，打 ERROR 会在 attach 无参列表等
// 常规路径上刷出假告警，把真正的服务端故障淹掉。
func TestHTTPErrorLevelsByStatus(t *testing.T) {
	cases := []struct {
		status    int
		wantLevel string
	}{{http.StatusNotFound, "WARN"}, {http.StatusInternalServerError, "ERROR"}}
	for _, tc := range cases {
		var buf bytes.Buffer
		old := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"error":"x"}`))
		}))
		_, err := client.New(srv.URL, "tok").Attach(context.Background(), "T1")
		srv.Close()
		slog.SetDefault(old)
		if err == nil {
			t.Fatalf("状态码 %d 必须返回错误", tc.status)
		}
		if !strings.Contains(buf.String(), "level="+tc.wantLevel) {
			t.Errorf("状态码 %d 期望日志级别 %s，实得:\n%s", tc.status, tc.wantLevel, buf.String())
		}
	}
}

// TestStopParsesWorktreeRemoved 验证 Stop 的返回值如实来自响应体 worktree_removed：
// true=本次删了 managed worktree，false=用户自带 worktree/原地模式没删。CLI 据此
// 打印与行为一致的提示，不在客户端猜。响应体缺字段（旧版 agentd）时按 false 处理。
func TestStopParsesWorktreeRemoved(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{
		{`{"status":"stopped","worktree_removed":true}`, true},
		{`{"status":"stopped","worktree_removed":false}`, false},
		{`{"status":"stopped"}`, false},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(tc.body))
		}))
		removed, err := client.New(srv.URL, "tok").Stop(context.Background(), "T1")
		srv.Close()
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
		if removed != tc.want {
			t.Fatalf("body %q: worktree_removed=%v, want %v", tc.body, removed, tc.want)
		}
	}
}

// TestRenderStreamPassesParamsAndReturnsSize 钉死请求参数与响应头解析。
func TestRenderStreamPassesParamsAndReturnsSize(t *testing.T) {
	var gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("X-Handoff-Render-Size", "4096")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("live"))
	}))
	defer ts.Close()

	rc, size, err := client.New(ts.Listener.Addr().String(), "tok").
		RenderStream(context.Background(), "T1", 0, 512, true)
	if err != nil {
		t.Fatalf("RenderStream 失败: %v", err)
	}
	defer rc.Close()
	if size != 4096 {
		t.Fatalf("size = %d, want 4096", size)
	}
	if !strings.Contains(gotQuery, "follow=1") || !strings.Contains(gotQuery, "tail=512") {
		t.Fatalf("查询参数缺失: %q", gotQuery)
	}
	b, _ := io.ReadAll(rc)
	if string(b) != "live" {
		t.Fatalf("流内容 = %q", b)
	}
}

// TestRenderStreamSurfacesHTTPError 钉死错误路径：404 必须变成明确错误而不是空流。
func TestRenderStreamSurfacesHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "任务不存在", http.StatusNotFound)
	}))
	defer ts.Close()
	_, _, err := client.New(ts.Listener.Addr().String(), "tok").
		RenderStream(context.Background(), "nope", 0, 0, false)
	if err == nil {
		t.Fatal("404 时必须返回错误")
	}
}

// TestDispatchErrorBodyNotTruncated 是错误体读取上限（4096）的守门人（B42）：
// 服务端返回一个 >256 字节的中文错误体（长路径 + UUID + 中文出路提示），
// 断言 Dispatch 返回的 error 文本包含**体尾部**的标记串。为什么尾部：B42 的
// 409 报文把「点名占用者 + 两条出路」放在后半句，旧上限 256 字节（~85 个汉字）
// 恰好把它们截掉——这个功能的价值就在那最后几行。把上限调回去，本测试立刻转红。
func TestDispatchErrorBodyNotTruncated(t *testing.T) {
	trailer := "或改用 --new-worktree 在独立工作树上开工"
	// 路径加长：保证 trailer 起点落在 256 之后——旧上限 256 字节会把它整体截没，
	// 新上限 4096 才放得下。仅 len(body)>256 不够（trailer 可能恰好嵌在前 256 内）。
	repo := "/Users/very/long/path/to/repo/with/a/considerably/longer/prefix/so/the/trailing/message/tail/gets/cut/by/the/old/256/byte/limit"
	body := `{"error":"目标工作目录已被活跃任务占用: ` + repo + ` 正被任务 8fd0b7d8-86d2-46f7-97c8-421cae47a954（重构登录, waiting_review）占用；先 handoff done/stop 它，` + trailer + `"}`
	if i := strings.Index(body, trailer); i <= 256 {
		t.Fatalf("trailer 起点 %d 必须在 256 之后才能测截断（body 共 %d 字节）", i, len(body))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	_, err := client.New(srv.URL, "tok").Dispatch(context.Background(), client.DispatchOpts{
		ProjectID: "deadbeefdeadbeef", PlanB64: "eGluZw==", PlanName: "plan.md",
	})
	if err == nil {
		t.Fatal("409 必须返回错误")
	}
	if !strings.Contains(err.Error(), "409") {
		t.Fatalf("错误应含 409 状态码, got: %v", err)
	}
	if !strings.Contains(err.Error(), trailer) {
		t.Fatalf("错误文本必须包含报文尾部标记串（被 256 上限截断）: %v", err)
	}
}

// TestDoneNoteSavedTrue 断言新 agentd 回传 true 时如实返回。
func TestDoneNoteSavedTrue(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true,"note_saved":true}`))
	}))
	defer ts.Close()
	saved, err := client.New(ts.URL, testToken).Done(context.Background(), "t1", "改完了")
	if err != nil || !saved {
		t.Fatalf("期望 saved=true err=nil，得到 saved=%v err=%v", saved, err)
	}
}

// TestDoneOldAgentdReportsNotSaved 是旧 agentd 兼容的钉子：响应里**没有**
// note_saved 字段时必须按 false 处理，否则「说明丢了」会变成哑失败——
// 协调者以为留了话，其实没留（与 stop 的 worktree_removed 同一模式）。
func TestDoneOldAgentdReportsNotSaved(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()
	saved, err := client.New(ts.URL, testToken).Done(context.Background(), "t1", "改完了")
	if err != nil {
		t.Fatal(err)
	}
	if saved {
		t.Fatal("响应无 note_saved 字段时必须按 false 处理")
	}
}

// TestDoneSendsNoteInBody 断言 note 真的进了请求体——签名对了但没发出去
// 是最容易漏的一种「测试全绿但功能没有」。
func TestDoneSendsNoteInBody(t *testing.T) {
	var gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Write([]byte(`{"ok":true,"note_saved":true}`))
	}))
	defer ts.Close()
	if _, err := client.New(ts.URL, testToken).Done(context.Background(), "t1", "改完了"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotBody, `"note":"改完了"`) {
		t.Fatalf("note 没进请求体: %s", gotBody)
	}
}

// 老 agentd：两个端点都 404 → 判定为不支持，调用方据此降级退 0。
func TestReclaimOnOldAgentdReportsUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r) // 老 agentd：两条路由都不存在
	}))
	defer srv.Close()

	_, err := client.New(srv.URL, "tok").Reclaim(context.Background(), "abc", false)
	if !errors.Is(err, client.ErrReclaimUnsupported) {
		t.Fatalf("两端点皆 404 应判为不支持，实得 %v", err)
	}
}

// 新 agentd + 不存在的任务：动作 404 但列表 200 → 任务是真不存在。
// 这两条走同一个 HTTP 码，用例分不开就等于没修。
func TestReclaimUnknownTaskIsNotMistakenForUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/reclaim" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"rows":[],"scanned":0}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"任务 abc 不存在"}`))
	}))
	defer srv.Close()

	_, err := client.New(srv.URL, "tok").Reclaim(context.Background(), "abc", false)
	if err == nil {
		t.Fatalf("应报错")
	}
	if errors.Is(err, client.ErrReclaimUnsupported) {
		t.Fatalf("列表可用时不得判成「不支持」，实得 %v", err)
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("错误应透传服务端真因，实得 %v", err)
	}
}

func TestReclaimDirtyRejectionCarriesStructuredList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"工作树有 2 项未提交改动或未跟踪文件",
"reason":"dirty","dirty":[{"status":" M","path":"a.go"},{"status":"??","path":"b.log"}]}`))
	}))
	defer srv.Close()

	_, err := client.New(srv.URL, "tok").Reclaim(context.Background(), "abc", false)
	var rej *client.ReclaimRejected
	if !errors.As(err, &rej) {
		t.Fatalf("409 应解成 ReclaimRejected，实得 %v", err)
	}
	if rej.Reason != proto.ReasonDirty || len(rej.Dirty) != 2 {
		t.Fatalf("拒绝详情解析错：%+v", rej)
	}
}

func TestReclaimListUnsupportedOn404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	if _, err := client.New(srv.URL, "tok").ReclaimList(context.Background()); !errors.Is(err, client.ErrReclaimUnsupported) {
		t.Fatalf("列表 404 应判为不支持，实得 %v", err)
	}
}

// 真机烟测照出的缺陷回归：Reclaim 的 force 必须真实进入请求体。修复前实现把
// 预编码的 bytes.NewReader 传给 c.do，而 c.do 会对 body 再 json.Marshal 一次——
// bytes.Reader 无导出字段，序列化成 {}，force 悄悄变 false，CLI 的 --force 永远被拒。
func TestReclaimForceCarriesIntoRequestBody(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"removed":true,"action":"removed"}`))
	}))
	defer srv.Close()

	if _, err := client.New(srv.URL, "tok").Reclaim(context.Background(), "abc", true); err != nil {
		t.Fatalf("回收：%v", err)
	}
	if !strings.Contains(gotBody, `"force":true`) {
		t.Fatalf("请求体必须带 force=true，实得：%s", gotBody)
	}
}
