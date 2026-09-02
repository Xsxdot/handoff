package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/testhttp"
)

// TestForwardProjectAddToNamedMachine 断言：带 ?machine= 的登记请求被原样搬到
// 那台机器，响应状态码与报文原样透传。
func TestForwardProjectAddToNamedMachine(t *testing.T) {
	remote := newTestAgentdEnv(t) // 远程那台：manager 未注入，登记必 503
	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: remote.ts.URL, Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req, _ := http.NewRequest(http.MethodPost,
		local.ts.URL+"/api/projects?machine=devbox",
		bytes.NewReader([]byte(`{"origin_url":"git@github.com:x/h.git"}`)))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	// 远端答什么就透什么：状态码与中文报错原文一律不改写
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d，期望原样透传远端的 503；体=%s", resp.StatusCode, body)
	}
	if !bytes.Contains(body, []byte("manager 未就绪")) {
		t.Errorf("远端报错原文必须原样透传，实得 %s", body)
	}
}

// TestForwardPreservesHandoffHeaders 断言：流式端点依赖的 X-Handoff-* 响应头
// 穿过 agentd 反代后仍可见；Content-Length 等本地重编码相关头不在此契约内。
func TestForwardPreservesHandoffHeaders(t *testing.T) {
	remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("X-Handoff-Frames-Size", "1725")
		w.Header().Set("X-Handoff-Render-Size", "2048")
		_, _ = w.Write([]byte("ok\n"))
	}))

	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: remote.URL, Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req, _ := http.NewRequest(http.MethodPost,
		local.ts.URL+"/api/projects?machine=devbox", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Handoff-Frames-Size"); got != "1725" {
		t.Fatalf("X-Handoff-Frames-Size = %q，期望原样透传 1725", got)
	}
	if got := resp.Header.Get("X-Handoff-Render-Size"); got != "2048" {
		t.Fatalf("X-Handoff-Render-Size = %q，期望原样透传 2048", got)
	}
}

// TestForwardStreamsChunks 断言：上游分两段写并在中间等待时，第一段会先穿过
// agentd 到达客户端，而不是被 forwardTo 等到响应结束后才一起回送。
func TestForwardStreamsChunks(t *testing.T) {
	allowSecond := make(chan struct{})
	secondWritten := make(chan struct{})
	var allowSecondOnce sync.Once
	defer allowSecondOnce.Do(func() { close(allowSecond) })

	remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("上游 ResponseWriter 不支持 Flush")
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("first"))
		flusher.Flush()

		<-allowSecond
		_, _ = w.Write([]byte("second"))
		flusher.Flush()
		close(secondWritten)
	}))

	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: remote.URL, Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req, _ := http.NewRequest(http.MethodPost,
		local.ts.URL+"/api/projects?machine=devbox", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+testToken)

	respCh := make(chan *http.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()

	var resp *http.Response
	select {
	case err := <-errCh:
		t.Fatalf("请求失败: %v", err)
	case resp = <-respCh:
	case <-time.After(2 * time.Second):
		t.Fatal("上游首段写出后，转发端仍未回送响应头")
	}
	defer resp.Body.Close()

	first := make([]byte, len("first"))
	readFirstErr := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(resp.Body, first)
		readFirstErr <- err
	}()
	select {
	case err := <-readFirstErr:
		if err != nil {
			t.Fatalf("读取首段失败: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("首段未在上游写出第二段前到达客户端")
	}
	if got := string(first); got != "first" {
		t.Fatalf("首段 = %q，期望 %q", got, "first")
	}
	select {
	case <-secondWritten:
		t.Fatal("读取首段时上游已经写出了第二段")
	default:
	}

	allowSecondOnce.Do(func() { close(allowSecond) })
	rest, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取尾段失败: %v", err)
	}
	if got := string(rest); got != "second" {
		t.Fatalf("尾段 = %q，期望 %q", got, "second")
	}
}

// TestForwardUnknownMachineRejected 断言：机器名不在 targets 里 → 400 且点名它。
func TestForwardUnknownMachineRejected(t *testing.T) {
	local := newTestAgentdEnv(t)
	req, _ := http.NewRequest(http.MethodPost,
		local.ts.URL+"/api/projects?machine=ghost", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400", resp.StatusCode)
	}
	if !bytes.Contains(body, []byte("ghost")) {
		t.Errorf("报文必须点名那个机器名，实得 %s", body)
	}
}

// TestForwardedRequestNeverForwardsAgain 是防环的核心断言：带转发头的请求
// 一律本机处理，哪怕它自己也带着 ?machine=。
func TestForwardedRequestNeverForwardsAgain(t *testing.T) {
	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: "http://127.0.0.1:1", Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req, _ := http.NewRequest(http.MethodPost,
		local.ts.URL+"/api/projects?machine=devbox", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set(forwardedHeader, "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	// devbox 是黑洞地址：真转发了就会是 502/超时；本机处理则是 503（manager 未注入）
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("带转发头的请求必须本机处理，实得状态码 %d", resp.StatusCode)
	}
}

func TestForwardWorktreeStripsCardIDsAndAttachesLocally(t *testing.T) {
	type receivedRequest struct {
		Body map[string]json.RawMessage
	}
	received := make(chan receivedRequest, 1)
	remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(body, &object); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		received <- receivedRequest{Body: object}
		_ = json.NewEncoder(w).Encode(proto.Workspace{Path: "/remote/manual/feat-relay", Branch: "feat/relay", Managed: true})
	}))
	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: remote.URL, Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	led, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = led.Close() })
	seedAgentdLedger(t, led, "bug")
	local.srv.SetLedger(led)
	card, err := led.CreateCard(ledger.NewCard{Title: "跨机挂卡", Project: "p", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost,
		local.ts.URL+"/api/projects/demo/worktrees?machine=devbox",
		strings.NewReader(`{"mode":"new_branch","branch":"feat/relay","base":"main","card_ids":["`+card.ID+`"],"future_key":"preserve"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("跨机建树 code=%d body=%s", resp.StatusCode, body)
	}
	var ws proto.Workspace
	if err := json.Unmarshal(body, &ws); err != nil {
		t.Fatal(err)
	}
	if len(ws.CardResults) != 1 || !ws.CardResults[0].OK || ws.CardResults[0].ID != card.ID {
		t.Fatalf("本地挂卡结果错误: %+v body=%s", ws.CardResults, body)
	}
	got, _ := led.GetCard(card.ID)
	if got.BaseBranch != ws.Branch {
		t.Fatalf("本地卡基线=%q，想要=%q", got.BaseBranch, ws.Branch)
	}
	select {
	case receivedReq := <-received:
		if _, ok := receivedReq.Body["card_ids"]; ok {
			t.Fatalf("转发给目标的请求不应含 card_ids: %+v", receivedReq.Body)
		}
		if string(receivedReq.Body["future_key"]) != `"preserve"` ||
			string(receivedReq.Body["mode"]) != `"new_branch"` {
			t.Fatalf("转发请求未保留未知键/已有键: %+v", receivedReq.Body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("目标未收到建树请求")
	}
}

func TestForwardWorktreeErrorAndCancel(t *testing.T) {
	t.Run("target error is unchanged and does not attach", func(t *testing.T) {
		remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"target boom"}`))
		}))
		local := newTestAgentdEnvWithCfg(t, &config.Config{
			Token:   testToken,
			Targets: map[string]config.Target{"devbox": {Addr: remote.URL, Token: testToken}},
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		led, err := ledger.Open(t.TempDir() + "/ledger.db")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = led.Close() })
		seedAgentdLedger(t, led, "bug")
		local.srv.SetLedger(led)
		card, err := led.CreateCard(ledger.NewCard{Title: "错误不挂卡", Project: "p", Workflow: "bug", Actor: "test"})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/projects/demo/worktrees?machine=devbox",
			strings.NewReader(`{"mode":"new_branch","branch":"feat/error","base":"main","card_ids":["`+card.ID+`"]}`))
		req.Host = "127.0.0.1:7777"
		req.Header.Set("Authorization", "Bearer "+testToken)
		rec := httptest.NewRecorder()
		local.srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError || rec.Body.String() != `{"error":"target boom"}` {
			t.Fatalf("目标错误未原样透传: code=%d body=%s", rec.Code, rec.Body.String())
		}
		got, _ := led.GetCard(card.ID)
		if got.BaseBranch != "" {
			t.Fatalf("目标失败后不应写卡: %q", got.BaseBranch)
		}
	})

	t.Run("cancel does not attach", func(t *testing.T) {
		remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		local := newTestAgentdEnvWithCfg(t, &config.Config{
			Token:   testToken,
			Targets: map[string]config.Target{"devbox": {Addr: remote.URL, Token: testToken}},
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		led, err := ledger.Open(t.TempDir() + "/ledger.db")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = led.Close() })
		seedAgentdLedger(t, led, "bug")
		local.srv.SetLedger(led)
		card, err := led.CreateCard(ledger.NewCard{Title: "取消不挂卡", Project: "p", Workflow: "bug", Actor: "test"})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		req := httptest.NewRequest(http.MethodPost, "/api/projects/demo/worktrees?machine=devbox",
			strings.NewReader(`{"mode":"new_branch","branch":"feat/cancel","base":"main","card_ids":["`+card.ID+`"]}`)).WithContext(ctx)
		req.Host = "127.0.0.1:7777"
		req.Header.Set("Authorization", "Bearer "+testToken)
		rec := httptest.NewRecorder()
		local.srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("取消应返回 502，实得 %d body=%s", rec.Code, rec.Body.String())
		}
		got, _ := led.GetCard(card.ID)
		if got.BaseBranch != "" {
			t.Fatalf("取消后不应写卡: %q", got.BaseBranch)
		}
	})
}
