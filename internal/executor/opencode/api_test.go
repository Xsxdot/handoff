// opencode 客户端测试：用 httptest 起 fake opencode server 驱动验证，
// 不依赖真实 opencode 二进制。
//
// 覆盖：路径/方法/basic auth 头/请求体契约、SSE 解析与断流重连、垃圾行容错。
package opencode_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/executor/opencode"
)

const testPassword = "test-password-123"

// quietLog 把测试期间的 slog.Default 换成丢弃 logger，保证测试输出干净；
// 实现通过 slog.Default() 取 logger（运行时求值），测试可借此隔离日志噪音。
func quietLog(t *testing.T) {
	t.Helper()
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
}

// wantAuth 是 fake server 期望收到的 basic auth 头（用户名固定 opencode）。
func wantAuth() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("opencode:"+testPassword))
}

// TestCreateSessionAndPrompt 验证 POST /session 与 POST /session/{id}/prompt_async 的
// 路径/方法/basic auth 头/请求体，并验证 CreateSession 正确解析返回的 session id。
func TestCreateSessionAndPrompt(t *testing.T) {
	quietLog(t)
	var mu sync.Mutex
	var gotSessionMethod, gotSessionPath, gotSessionAuth, gotSessionBody string
	var gotPromptMethod, gotPromptPath, gotPromptAuth, gotPromptBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			gotSessionMethod, gotSessionPath, gotSessionAuth, gotSessionBody = r.Method, r.URL.Path, r.Header.Get("Authorization"), string(body)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":"sess-abc123"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/session/sess-abc123/prompt_async":
			gotPromptMethod, gotPromptPath, gotPromptAuth, gotPromptBody = r.Method, r.URL.Path, r.Header.Get("Authorization"), string(body)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	api := opencode.NewAPI(ts.URL, testPassword)
	ctx := context.Background()

	sessionID, err := api.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sessionID != "sess-abc123" {
		t.Errorf("CreateSession 返回 %q，期望 sess-abc123", sessionID)
	}

	const wantPrompt = "执行 plan 并完成任务"
	if err := api.PromptAsync(ctx, sessionID, wantPrompt); err != nil {
		t.Fatalf("PromptAsync: %v", err)
	}

	auth := wantAuth()
	if gotSessionMethod != http.MethodPost {
		t.Errorf("POST /session 方法 %s，期望 POST", gotSessionMethod)
	}
	if gotSessionPath != "/session" {
		t.Errorf("POST /session 路径 %s，期望 /session", gotSessionPath)
	}
	if gotSessionAuth != auth {
		t.Errorf("POST /session Authorization %q，期望 %q", gotSessionAuth, auth)
	}
	if gotSessionBody != `{"title":"handoff"}` {
		t.Errorf("POST /session body %q，期望 {\"title\":\"handoff\"}", gotSessionBody)
	}

	if gotPromptMethod != http.MethodPost {
		t.Errorf("prompt_async 方法 %s，期望 POST", gotPromptMethod)
	}
	if gotPromptPath != "/session/sess-abc123/prompt_async" {
		t.Errorf("prompt_async 路径 %s，期望 /session/sess-abc123/prompt_async", gotPromptPath)
	}
	if gotPromptAuth != auth {
		t.Errorf("prompt_async Authorization %q，期望 %q", gotPromptAuth, auth)
	}
	var pb struct {
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	if err := json.Unmarshal([]byte(gotPromptBody), &pb); err != nil {
		t.Fatalf("prompt_async body 不是合法 JSON: %v\nbody=%s", err, gotPromptBody)
	}
	if len(pb.Parts) != 1 || pb.Parts[0].Type != "text" || pb.Parts[0].Text != wantPrompt {
		t.Errorf("prompt_async body 应含 1 个 text part 且文本为 %q，实际 %+v", wantPrompt, pb.Parts)
	}
}

// TestCreateSessionAccepts2xx 验证建会话接受整个 2xx 区间：真实 opencode server
// 对 POST /session 可能回 201/202 而非 200，只认 200 会把合法的创建成功误判为失败。
func TestCreateSessionAccepts2xx(t *testing.T) {
	quietLog(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/session" {
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id":"sess-201"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	api := opencode.NewAPI(ts.URL, testPassword)
	sessionID, err := api.CreateSession(context.Background())
	if err != nil {
		t.Fatalf("CreateSession(201) 应成功: %v", err)
	}
	if sessionID != "sess-201" {
		t.Fatalf("sessionID=%q, want sess-201", sessionID)
	}
}

// TestRespondPermissionBody 验证 POST /session/{id}/permissions/{permID} 的
// 路径与请求体 {"response":"once"}，并拒绝非法 response 值。
func TestRespondPermissionBody(t *testing.T) {
	quietLog(t)
	var mu sync.Mutex
	var gotPath, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != http.MethodPost {
			t.Errorf("permissions 方法 %s，期望 POST", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		gotPath, gotBody = r.URL.Path, string(body)
		w.Write([]byte("true"))
	}))
	defer ts.Close()

	api := opencode.NewAPI(ts.URL, testPassword)
	ctx := context.Background()

	if err := api.RespondPermission(ctx, "s1", "perm-1", "once"); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	if gotPath != "/session/s1/permissions/perm-1" {
		t.Errorf("permissions 路径 %s，期望 /session/s1/permissions/perm-1", gotPath)
	}
	if !strings.Contains(gotBody, `"response":"once"`) {
		t.Errorf("permissions body %q 应包含 \"response\":\"once\"", gotBody)
	}
	var req struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal([]byte(gotBody), &req); err != nil || req.Response != "once" {
		t.Errorf("permissions body 解析后 response=%q, err=%v", req.Response, err)
	}

	if err := api.RespondPermission(ctx, "s1", "perm-2", "bogus"); err == nil {
		t.Error("RespondPermission 应拒绝非法 response 值 bogus")
	}
}

// TestSubscribeReconnect 验证 SSE 断流后指数退避重连：
// 连接 1 发 2 条事件后断开，客户端重连（连接 2）收到第 3 条，onEvent 共 3 次；
// ctx 取消后 SubscribeEvents 立即返回。
func TestSubscribeReconnect(t *testing.T) {
	quietLog(t)
	var mu sync.Mutex
	conns := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		conns++
		n := conns
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		if n == 1 {
			fmt.Fprint(w, "event: message\ndata: {\"type\":\"e1\",\"n\":1}\n\n")
			fl.Flush()
			fmt.Fprint(w, "data: {\"type\":\"e2\",\"n\":2}\n\n")
			fl.Flush()
			return // 结束响应体 -> 客户端断流，触发重连
		}
		fmt.Fprint(w, "data: {\"type\":\"e3\",\"n\":3}\n\n")
		fl.Flush()
		<-r.Context().Done() // 保持连接，直到客户端取消
	}))
	defer ts.Close()

	api := opencode.NewAPI(ts.URL, testPassword)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu2 sync.Mutex
	var events []json.RawMessage
	done := make(chan error, 1)
	go func() {
		done <- api.SubscribeEvents(ctx, func(ev json.RawMessage) {
			mu2.Lock()
			events = append(events, ev)
			mu2.Unlock()
		}, nil)
	}()

	deadline := time.After(15 * time.Second)
	for {
		mu2.Lock()
		got := len(events)
		mu2.Unlock()
		if got >= 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("超时未收满 3 条事件，当前 %d 条", got)
		case <-time.After(20 * time.Millisecond):
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("ctx 取消后 SubscribeEvents 返回错误: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ctx 取消后 SubscribeEvents 未在 3s 内返回")
	}

	mu.Lock()
	defer mu.Unlock()
	if conns != 2 {
		t.Errorf("应恰好重连 1 次（共 2 个 HTTP 连接），实际 %d", conns)
	}
}

// TestSubscribeTolerantGarbage 验证 SSE 解析的宽容性：
// 注释行、event: 行、非 JSON data、无前缀垃圾行、残缺 JSON 都不中断订阅，
// 只有合法 JSON 事件被回调，且顺序保持。
func TestSubscribeTolerantGarbage(t *testing.T) {
	quietLog(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprint(w, ": comment line\n")
		fmt.Fprint(w, "event: message\n")
		fmt.Fprint(w, "data: this-is-not-json\n")
		fmt.Fprint(w, "\n")
		fmt.Fprint(w, "garbage-line-without-prefix\n")
		fmt.Fprint(w, "data: {\"type\":\"a\",\"n\":1}\n")
		fmt.Fprint(w, "\n")
		fmt.Fprint(w, "data: {broken json\n")
		fmt.Fprint(w, "\n")
		fmt.Fprint(w, "data: {\"type\":\"b\",\"n\":2}\n")
		fmt.Fprint(w, "\n")
		fmt.Fprint(w, "data:\n")
		fmt.Fprint(w, "\n")
		fmt.Fprint(w, "data: {\"type\":\"c\",\"n\":3}\n")
		fmt.Fprint(w, "\n")
		fl.Flush()
		<-r.Context().Done()
	}))
	defer ts.Close()

	api := opencode.NewAPI(ts.URL, testPassword)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var events []json.RawMessage
	done := make(chan error, 1)
	go func() {
		done <- api.SubscribeEvents(ctx, func(ev json.RawMessage) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		}, nil)
	}()

	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		got := len(events)
		mu.Unlock()
		if got >= 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("超时未收满 3 条合法事件，当前 %d 条", got)
		case <-time.After(20 * time.Millisecond):
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("ctx 取消后 SubscribeEvents 返回错误: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ctx 取消后 SubscribeEvents 未在 3s 内返回")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 3 {
		t.Fatalf("应恰好收到 3 条合法事件，实际 %d 条", len(events))
	}
	wantTypes := []string{"a", "b", "c"}
	for i, raw := range events {
		var ev struct {
			Type string `json:"type"`
			N    int    `json:"n"`
		}
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatalf("第 %d 条事件解析失败: %v", i, err)
		}
		if ev.Type != wantTypes[i] || ev.N != i+1 {
			t.Errorf("第 %d 条事件 %+v，期望 type=%s n=%d", i, ev, wantTypes[i], i+1)
		}
	}
}

// TestSubscribeCtxCancel 验证订阅在连接无事件时也能随 ctx 取消及时返回（不泄漏 goroutine）。
func TestSubscribeCtxCancel(t *testing.T) {
	quietLog(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fl.Flush()
		<-r.Context().Done()
	}))
	defer ts.Close()

	api := opencode.NewAPI(ts.URL, testPassword)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- api.SubscribeEvents(ctx, func(json.RawMessage) {}, nil)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("ctx 取消后 SubscribeEvents 返回错误: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ctx 取消后 SubscribeEvents 未在 3s 内返回")
	}
}

// TestUnaryTimeoutHang 验证一元调用对「挂起不响应」的 server 在超时内返回错误：
// 模拟半死的 opencode（TCP 通但不响应）——没有客户端超时的话 handoff reply 回程
// 会在审核者终端永久挂起。超时经 NewAPIWithUnaryTimeout 注入 200ms，避免测试耗时。
func TestUnaryTimeoutHang(t *testing.T) {
	quietLog(t)
	// unblock 放行 handler 供 ts.Close 收尾：半死 handler 挂起期间请求 ctx 不随
	// 客户端断开而取消（带 body 的 POST 实测不会触发），必须显式放行，否则 Close 永久阻塞
	unblock := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-unblock // 吞掉请求不响应：半死 server 形态
	}))
	defer ts.Close()
	defer close(unblock)

	api := opencode.NewAPIWithUnaryTimeout(ts.URL, testPassword, 200*time.Millisecond)
	start := time.Now()
	_, err := api.CreateSession(context.Background())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("半死 server 上 CreateSession 应在超时后返回错误")
	}
	if !strings.Contains(err.Error(), "timeout") && !strings.Contains(err.Error(), "deadline exceeded") {
		t.Errorf("错误应含 timeout/deadline exceeded 语义，实际: %v", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("超时返回过慢（应约 200ms），实际 %v", elapsed)
	}
}

// TestUnaryTimeoutNotAffectingSSE 验证一元超时只作用于一元调用，SSE 长连接不受影响：
// 客户端注入 200ms 一元超时，订阅持续 300ms 以上仍能收到全部事件——若两个 client
// 被错误复用，长连接会在 200ms 时被 Timeout 掐断、第二条事件丢失。
func TestUnaryTimeoutNotAffectingSSE(t *testing.T) {
	quietLog(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/event" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"n\":1}\n\n")
		fl.Flush()
		time.Sleep(300 * time.Millisecond) // 超过一元超时 200ms 仍保持连接
		fmt.Fprint(w, "data: {\"n\":2}\n\n")
		fl.Flush()
		<-r.Context().Done()
	}))
	defer ts.Close()

	api := opencode.NewAPIWithUnaryTimeout(ts.URL, testPassword, 200*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var events []json.RawMessage
	done := make(chan error, 1)
	go func() {
		done <- api.SubscribeEvents(ctx, func(ev json.RawMessage) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		}, nil)
	}()

	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		n := len(events)
		mu.Unlock()
		if n >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("SSE 未收满 2 条事件（一元超时不应影响长连接），当前 %d 条", n)
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("ctx 取消后 SubscribeEvents 返回错误: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ctx 取消后 SubscribeEvents 未在 3s 内返回")
	}
}

// TestSubscribeBackoffResetAfterSuccess 验证连接**活够健康门槛**后退避复位
// （P1-10a，判据按 A-8 修正）：连续两次连接失败把退避抬到 200ms，随后一次
// 活过门槛的连接必须把退避复位回初始 100ms——否则退避指数累积到封顶后终身不降，
// 断连间隙越拉越长。同时验证 onReconnect 仅在「断连后的成功重连」时触发一次。
//
// 为什么判据是「活够时长」而不是「拿到 200」：半死的 opencode 会接受连接、
// 回 200、立刻关流。按 200 复位等于对它永不退避（见
// TestSSEBackoffGrowsWhenServerAcceptsThenCloses）。
//
// 时间线（注入退避 100ms 起、1s 封顶，健康门槛 150ms）：
//   - conn1 失败 → 等 100ms；conn2 失败 → 等 200ms
//   - conn3 成功且流持续 250ms（> 门槛）→ 退避复位 100ms
//   - conn4 失败 → 应约等 100ms（未复位则等 400ms）；conn5 失败 → 约 200ms
//     （未复位则 800ms）——两组间隔阈值都留了 2~3 倍余量，无需精确计时
func TestSubscribeBackoffResetAfterSuccess(t *testing.T) {
	quietLog(t)
	var mu sync.Mutex
	conns := 0
	var connTimes []time.Time
	reconnects := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		conns++
		n := conns
		connTimes = append(connTimes, time.Now())
		mu.Unlock()
		switch n {
		case 1, 2, 4:
			w.WriteHeader(http.StatusInternalServerError) // 连接失败：退避加倍
		case 5:
			// 末次连接挂住不返回：测试随后 cancel，走的是正常关停路径，
			// SubscribeEvents 应返回 nil 而不是把「取消」当失败原因
			<-r.Context().Done()
		case 3:
			// 成功连接：先送一条事件，再把流保持到健康门槛之上才结束
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"e1\"}\n\n")
			w.(http.Flusher).Flush()
			time.Sleep(250 * time.Millisecond)
		}
	}))
	defer ts.Close()

	api := opencode.NewAPIWithSSETiming(ts.URL, testPassword,
		100*time.Millisecond, time.Second, 150*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- api.SubscribeEvents(ctx, func(json.RawMessage) {}, func() {
			mu.Lock()
			reconnects++
			mu.Unlock()
		})
	}()

	deadline := time.After(10 * time.Second)
	for {
		mu.Lock()
		n := conns
		mu.Unlock()
		if n >= 5 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("超时未等到 5 次连接，当前 %d 次", n)
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("ctx 取消后 SubscribeEvents 返回错误: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ctx 取消后 SubscribeEvents 未在 3s 内返回")
	}

	mu.Lock()
	defer mu.Unlock()
	// conn3 起到 conn4 起的间隔 = conn3 的 250ms 寿命 + 退避：
	// 复位后约 350ms；未复位则 ≥650ms
	if gap := connTimes[3].Sub(connTimes[2]); gap > 500*time.Millisecond {
		t.Errorf("成功连接后退避未复位：conn3→conn4 间隔 %v，期望约 350ms（250ms 流 + 100ms 退避）", gap)
	}
	// conn4 失败后按复位后的递增：约 200ms；未复位则 ≥800ms
	if gap := connTimes[4].Sub(connTimes[3]); gap > 600*time.Millisecond {
		t.Errorf("conn4→conn5 间隔 %v 异常（复位后应为 200ms 级）", gap)
	}
	if reconnects != 1 {
		t.Errorf("onReconnect 应恰触发 1 次（仅 conn3 成功重连），实际 %d", reconnects)
	}
}

// TestSubscribeNoReconnectOnFirstConnect 验证首次连接成功不触发 onReconnect：
// 断连恢复语义要求「之前断过」才算重连，首次建连只是订阅起步。
func TestSubscribeNoReconnectOnFirstConnect(t *testing.T) {
	quietLog(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"type\":\"e1\"}\n\n")
		fl.Flush()
		<-r.Context().Done()
	}))
	defer ts.Close()

	api := opencode.NewAPIWithSSEBackoff(ts.URL, testPassword, 100*time.Millisecond, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	reconnects := 0
	done := make(chan error, 1)
	go func() {
		done <- api.SubscribeEvents(ctx, func(json.RawMessage) {}, func() { reconnects++ })
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("ctx 取消后 SubscribeEvents 返回错误: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ctx 取消后 SubscribeEvents 未在 3s 内返回")
	}
	if reconnects != 0 {
		t.Errorf("首次连接成功不应触发 onReconnect，实际 %d 次", reconnects)
	}
}

// TestHasSession 验证会话在场校验：GET /session 列表里含目标 id 返回 true，
// 不含返回 false。
func TestHasSession(t *testing.T) {
	quietLog(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/session" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"id":"sess-a"},{"id":"sess-b"}]`))
	}))
	defer ts.Close()

	api := opencode.NewAPI(ts.URL, testPassword)
	ctx := context.Background()
	if ok, err := api.HasSession(ctx, "sess-b"); err != nil || !ok {
		t.Fatalf("含目标 id 应 ok=true, got ok=%v err=%v", ok, err)
	}
	if ok, err := api.HasSession(ctx, "sess-missing"); err != nil || ok {
		t.Fatalf("不含目标 id 应 ok=false, got ok=%v err=%v", ok, err)
	}
}

// TestGetSessionParsesParentAndTitle 覆盖 B52 核心依据：GET /session/{id} 返回
// parentID（子会话归属的唯一依据）与 title（工单标注素材）。本文件是外部测试
// 包（package opencode_test），sessionDetail 未导出，只断言 GetSession 的可观察
// 行为——用 := 接收即可使用未导出类型的值。
func TestGetSessionParsesParentAndTitle(t *testing.T) {
	quietLog(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/session/ses_child" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, `{"id":"ses_child","parentID":"ses_parent","title":"Run probe curl command (@general subagent)"}`)
	}))
	defer ts.Close()

	api := opencode.NewAPI(ts.URL, testPassword)
	d, err := api.GetSession(context.Background(), "ses_child")
	if err != nil {
		t.Fatalf("GetSession 失败: %v", err)
	}
	if d.ParentID != "ses_parent" {
		t.Fatalf("parentID=%q，期望 ses_parent", d.ParentID)
	}
	if d.Title != "Run probe curl command (@general subagent)" {
		t.Fatalf("title=%q，期望带 subagent 标记的标题", d.Title)
	}
}

// TestGetSessionEmptyIDDoesNotHitServer 空会话 id 直接报错、不触达服务端：
// 拿空 id 拼出的 "/session/" 只会换来 404，白白占掉一次超时预算。
func TestGetSessionEmptyIDDoesNotHitServer(t *testing.T) {
	quietLog(t)
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer ts.Close()

	api := opencode.NewAPI(ts.URL, testPassword)
	if _, err := api.GetSession(context.Background(), ""); err == nil {
		t.Fatal("空会话 id 应当直接报错")
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("空会话 id 不应触达服务端，实际请求 %d 次", n)
	}
}

// TestGetSessionNon2xxReturnsError 非 2xx 响应必须返回错误（fail-closed：
// 认亲拿不到可靠数据就当失败处理，不能当「没这个会话」静默放行）。
func TestGetSessionNon2xxReturnsError(t *testing.T) {
	quietLog(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "boom")
	}))
	defer ts.Close()

	api := opencode.NewAPI(ts.URL, testPassword)
	if _, err := api.GetSession(context.Background(), "ses_child"); err == nil {
		t.Fatal("非 2xx 应当返回错误")
	}
}

func TestReplyQuestionPostsAnswers(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	api := opencode.NewAPI(srv.URL, "pw")
	if err := api.ReplyQuestion(context.Background(), "req_1", [][]string{{"照此实现"}}); err != nil {
		t.Fatalf("ReplyQuestion 返回错误: %v", err)
	}
	if gotPath != "/question/req_1/reply" {
		t.Errorf("path = %q，期望 /question/req_1/reply", gotPath)
	}
	if !strings.Contains(gotBody, `"answers":[["照此实现"]]`) {
		t.Errorf("body = %q，期望含 \"answers\":[[\"照此实现\"]]", gotBody)
	}
}

func TestReplyQuestion4xxMapsToCustomRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid answer"}`))
	}))
	defer srv.Close()

	api := opencode.NewAPI(srv.URL, "pw")
	err := api.ReplyQuestion(context.Background(), "req_1", [][]string{{"我自己写的答案"}})
	if !errors.Is(err, opencode.ErrCustomAnswerRejected) {
		t.Fatalf("err = %v，期望可 errors.Is 命中 ErrCustomAnswerRejected", err)
	}
}

func TestReplyQuestion5xxIsNotCustomRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	api := opencode.NewAPI(srv.URL, "pw")
	err := api.ReplyQuestion(context.Background(), "req_1", [][]string{{"x"}})
	if err == nil {
		t.Fatal("5xx 应当返回错误")
	}
	if errors.Is(err, opencode.ErrCustomAnswerRejected) {
		t.Fatal("5xx 是服务端故障，不能被当成「自定义答案不被接受」")
	}
}

func TestRejectQuestionPostsToRejectPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	api := opencode.NewAPI(srv.URL, "pw")
	if err := api.RejectQuestion(context.Background(), "req_9"); err != nil {
		t.Fatalf("RejectQuestion 返回错误: %v", err)
	}
	if gotPath != "/question/req_9/reject" {
		t.Errorf("path = %q，期望 /question/req_9/reject", gotPath)
	}
}

func TestListPendingQuestionsDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"req_1","sessionID":"ses_a","questions":[
			{"question":"选哪个？","header":"选型","multiple":true,"custom":true,
			 "options":[{"label":"A","description":"甲"},{"label":"B","description":"乙"}]}]}]`))
	}))
	defer srv.Close()

	api := opencode.NewAPI(srv.URL, "pw")
	got, err := api.ListPendingQuestions(context.Background())
	if err != nil {
		t.Fatalf("ListPendingQuestions 返回错误: %v", err)
	}
	if len(got) != 1 || got[0].ID != "req_1" || got[0].SessionID != "ses_a" {
		t.Fatalf("got = %+v，期望一条 req_1/ses_a", got)
	}
	q := got[0].Questions[0]
	if q.Header != "选型" || !q.Multiple || !q.Custom || len(q.Options) != 2 || q.Options[1].Label != "B" {
		t.Errorf("question 解码不完整: %+v", q)
	}
}
