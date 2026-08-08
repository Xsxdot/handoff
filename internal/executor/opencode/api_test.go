// opencode 客户端测试：用 httptest 起 fake opencode server 驱动验证，
// 不依赖真实 opencode 二进制。
//
// 覆盖：路径/方法/basic auth 头/请求体契约、SSE 解析与断流重连、垃圾行容错。
package opencode_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
		})
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
		})
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
		done <- api.SubscribeEvents(ctx, func(json.RawMessage) {})
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
		})
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
