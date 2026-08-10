// agentd preview_server 测试：Preview 创建/读取/关闭与无鉴权代理路由。
//
// 职责：
//   - 创建返回指向本机 loopback 的 nonce URL、command_id 幂等
//   - GET/DELETE /v1/previews/{id} 读取与关闭会话
//   - /v1/preview-proxy/{nonce}/... 无 Bearer 头直达上游、机器不可用 503、未知 nonce 404
//   - 远端机器会话按 machine_id 转发给 previewPeerConnector
//
// 边界：
//   - 使用真实 SQLite store + 真实 machineauthority/preview service + httptest
//   - 上游用真实 httptest.Server 回放，避免依赖 mock 代理细节
package agentd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/desktopapi"
	"github.com/xushixin/handoff/internal/machineauthority"
	"github.com/xushixin/handoff/internal/peer"
	"github.com/xushixin/handoff/internal/preview"
	"github.com/xushixin/handoff/internal/resourcegateway"
	"github.com/xushixin/handoff/internal/store"
	"github.com/xushixin/handoff/internal/workspaceapi"
)

// previewTestEnv 聚合 Preview 路由测试依赖：真实 store + 真实本机 owner
// authority/preview service + stub peer connector + httptest server。
type previewTestEnv struct {
	srv            *Server
	ts             *httptest.Server
	st             *store.Store
	token          string
	localMachineID string
	workspaceID    string
	peers          *stubPreviewPeers
}

// newPreviewTestEnv 组装 Preview 测试环境：注册本机 Machine 与 detached
// Workspace（available），接线 router/owner/peer connector。
func newPreviewTestEnv(t *testing.T) *previewTestEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	local, err := st.EnsureLocalMachine(context.Background(), "本机")
	if err != nil {
		t.Fatalf("EnsureLocalMachine: %v", err)
	}
	ws, err := st.ResolveWorkspaceForPath(context.Background(), local.ID, "/repo/preview-ws", "/repo/preview-ws")
	if err != nil {
		t.Fatalf("ResolveWorkspaceForPath: %v", err)
	}
	cfg := &config.Config{Token: testToken, Listen: "127.0.0.1:7777"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(cfg, st, logger)
	localAuthority := machineauthority.NewResourceAuthority(logger)
	ps, err := preview.NewService(st, local.ID, "http://127.0.0.1:7777", logger)
	if err != nil {
		t.Fatalf("preview.NewService: %v", err)
	}
	localAuthority.SetPreviewService(ps)
	peers := &stubPreviewPeers{}
	router := resourcegateway.NewRouter(st, localAuthority, peer.NewAuthorityRegistry(nil, nil), logger)
	srv.SetResourceRouter(router)
	srv.SetPreviewService(ps)
	srv.SetPreviewPeerConnector(peers)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &previewTestEnv{srv: srv, ts: ts, st: st, token: cfg.Token,
		localMachineID: local.ID, workspaceID: ws.ID, peers: peers}
}

// do 发起请求；auth=true 时携带 Bearer token。
func (e *previewTestEnv) do(t *testing.T, method, path, body string, auth bool) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, reader)
	if err != nil {
		t.Fatalf("NewRequest %s %s: %v", method, path, err)
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+e.token)
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// createPreview 以给定 command_id/port 创建 Preview 会话并解码 DTO。
func (e *previewTestEnv) createPreview(t *testing.T, commandID string, port int) desktopapi.PreviewSessionDTO {
	t.Helper()
	resp := e.do(t, http.MethodPost, "/v1/workspaces/"+e.workspaceID+"/previews",
		fmt.Sprintf(`{"command_id":%q,"port":%d}`, commandID, port), true)
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("create preview status = %d, want 201: %s", resp.StatusCode, b)
	}
	return decodePreviewDTO(t, resp)
}

func decodePreviewDTO(t *testing.T, resp *http.Response) desktopapi.PreviewSessionDTO {
	t.Helper()
	defer resp.Body.Close()
	var dto desktopapi.PreviewSessionDTO
	if err := json.NewDecoder(resp.Body).Decode(&dto); err != nil {
		t.Fatalf("decode preview session: %v", err)
	}
	return dto
}

// previewNonce 从 owner-loopback URL 提取代理 nonce。
func previewNonce(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse preview url %q: %v", rawURL, err)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if p == "preview-proxy" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	t.Fatalf("url %q 缺少 nonce 路径段", rawURL)
	return ""
}

// decodeProblem 解码 Problem 线格式并返回 code。
func decodeProblem(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	return problem.Code
}

// stubPreviewPeers 记录 ForwardPreviewProxy/ClosePreviewSession 调用并回写固定 body。
type stubPreviewPeers struct {
	mu    sync.Mutex
	calls []previewPeerCall
	body  string
}

type previewPeerCall struct {
	machineID string
	nonce     string
	sessionID string
}

func (s *stubPreviewPeers) ForwardPreviewProxy(w http.ResponseWriter, _ *http.Request, machineID, nonce string) {
	s.mu.Lock()
	s.calls = append(s.calls, previewPeerCall{machineID: machineID, nonce: nonce})
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, s.body)
}

func (s *stubPreviewPeers) ClosePreviewSession(_ context.Context, machineID, previewSessionID string) error {
	s.mu.Lock()
	s.calls = append(s.calls, previewPeerCall{machineID: machineID, sessionID: previewSessionID})
	s.mu.Unlock()
	return nil
}

func (s *stubPreviewPeers) snapshot() []previewPeerCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]previewPeerCall(nil), s.calls...)
}

// TestCreatePreviewReturnsLocalURL 验证创建返回 201，URL 指向本机 loopback 且带 nonce。
func TestCreatePreviewReturnsLocalURL(t *testing.T) {
	env := newPreviewTestEnv(t)
	created := env.createPreview(t, "cmd-url", 8080)
	if created.PreviewSessionID == "" {
		t.Fatal("preview_session_id 为空")
	}
	pattern := regexp.MustCompile(`^http://127\.0\.0\.1:\d+/v1/preview-proxy/[^/]+/$`)
	if !pattern.MatchString(created.URL) {
		t.Fatalf("url = %q, 不匹配 %s", created.URL, pattern)
	}
	if created.State != "pending" {
		t.Fatalf("state = %q, want pending", created.State)
	}
	if created.MachineID != env.localMachineID {
		t.Fatalf("machine_id = %q, want %q", created.MachineID, env.localMachineID)
	}
	if created.Port != 8080 {
		t.Fatalf("port = %d, want 8080", created.Port)
	}
}

// TestCreatePreviewIdempotent 验证相同 command_id 重复创建返回同一会话。
func TestCreatePreviewIdempotent(t *testing.T) {
	env := newPreviewTestEnv(t)
	first := env.createPreview(t, "cmd-same", 8080)
	second := env.createPreview(t, "cmd-same", 8080)
	if second.PreviewSessionID != first.PreviewSessionID {
		t.Fatalf("idempotent create 改变 id: %q -> %q", first.PreviewSessionID, second.PreviewSessionID)
	}
}

// TestGetAndClosePreview 验证 GET 读取会话、DELETE 关闭并把 state 置 closed。
func TestGetAndClosePreview(t *testing.T) {
	env := newPreviewTestEnv(t)
	created := env.createPreview(t, "cmd-gc", 8080)

	resp := env.do(t, http.MethodGet, "/v1/previews/"+created.PreviewSessionID, "", true)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("GET status = %d, want 200: %s", resp.StatusCode, b)
	}
	got := decodePreviewDTO(t, resp)
	if got.PreviewSessionID != created.PreviewSessionID {
		t.Fatalf("GET id = %q, want %q", got.PreviewSessionID, created.PreviewSessionID)
	}

	resp = env.do(t, http.MethodDelete, "/v1/previews/"+created.PreviewSessionID, "", true)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("DELETE status = %d, want 200: %s", resp.StatusCode, b)
	}
	closed := decodePreviewDTO(t, resp)
	if closed.State != "closed" {
		t.Fatalf("DELETE state = %q, want closed", closed.State)
	}
}

// TestPreviewProxyWithoutAuthSucceeds 验证代理路由不带 Authorization 头直达上游，
// 且上游看到正确的 path/query。
func TestPreviewProxyWithoutAuthSucceeds(t *testing.T) {
	env := newPreviewTestEnv(t)
	var (
		sawMu  sync.Mutex
		sawPat string
		sawQry string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawMu.Lock()
		sawPat = r.URL.Path
		sawQry = r.URL.RawQuery
		sawMu.Unlock()
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "upstream-saw-it")
	}))
	defer upstream.Close()
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse upstream port: %v", err)
	}
	created := env.createPreview(t, "cmd-proxy", port)
	nonce := previewNonce(t, created.URL)

	// 关键断言：代理请求不带 Authorization 头
	resp := env.do(t, http.MethodGet, "/v1/preview-proxy/"+nonce+"/some/path?q=1", "", false)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("proxy status = %d, want 200: %s", resp.StatusCode, b)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "upstream-saw-it" {
		t.Fatalf("proxy body = %q, want upstream-saw-it", body)
	}
	sawMu.Lock()
	defer sawMu.Unlock()
	if sawPat != "/some/path" {
		t.Fatalf("upstream path = %q, want /some/path", sawPat)
	}
	if sawQry != "q=1" {
		t.Fatalf("upstream query = %q, want q=1", sawQry)
	}
}

// TestPreviewProxyRequiresAvailableMachine 验证 Machine 断开后代理立即 503。
func TestPreviewProxyRequiresAvailableMachine(t *testing.T) {
	env := newPreviewTestEnv(t)
	created := env.createPreview(t, "cmd-offline", 8080)
	nonce := previewNonce(t, created.URL)

	if _, err := env.st.SetMachineStatusWithControlEvent(context.Background(),
		env.localMachineID, controlplane.MachineStatusUnavailable); err != nil {
		t.Fatalf("SetMachineStatusWithControlEvent: %v", err)
	}
	resp := env.do(t, http.MethodGet, "/v1/preview-proxy/"+nonce+"/", "", false)
	if resp.StatusCode != http.StatusServiceUnavailable {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("proxy status = %d, want 503: %s", resp.StatusCode, b)
	}
	if code := decodeProblem(t, resp); code != "MACHINE_OFFLINE" {
		t.Fatalf("problem.code = %q, want MACHINE_OFFLINE", code)
	}
}

// TestPreviewProxyUnknownNonce 验证随机 nonce 返回 404 RESOURCE_NOT_FOUND。
func TestPreviewProxyUnknownNonce(t *testing.T) {
	env := newPreviewTestEnv(t)
	resp := env.do(t, http.MethodGet, "/v1/preview-proxy/definitely-not-a-real-nonce/x", "", false)
	if resp.StatusCode != http.StatusNotFound {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("proxy status = %d, want 404: %s", resp.StatusCode, b)
	}
	if code := decodeProblem(t, resp); code != "RESOURCE_NOT_FOUND" {
		t.Fatalf("problem.code = %q, want RESOURCE_NOT_FOUND", code)
	}
}

// TestPreviewProxyForwardsRemoteMachine 验证远端会话按 machine_id 转发给
// previewPeerConnector，且带完整 nonce。
func TestPreviewProxyForwardsRemoteMachine(t *testing.T) {
	env := newPreviewTestEnv(t)
	ctx := context.Background()
	remoteID := "m-remote"
	if _, err := env.st.SyncConfiguredMachines(ctx, []controlplane.ConfiguredMachine{{
		ConfigKey: remoteID, DisplayName: "remote", Kind: controlplane.MachineKindRemote,
		Endpoint: "http://127.0.0.1:9999", SecretRef: "config.targets.m-remote.token",
	}}); err != nil {
		t.Fatalf("SyncConfiguredMachines: %v", err)
	}
	if _, err := env.st.SetMachineStatusWithControlEvent(ctx, remoteID, controlplane.MachineStatusConnected); err != nil {
		t.Fatalf("SetMachineStatusWithControlEvent: %v", err)
	}
	ws, err := env.st.ResolveWorkspaceForPath(ctx, remoteID, "/remote/repo", "/remote/repo")
	if err != nil {
		t.Fatalf("ResolveWorkspaceForPath: %v", err)
	}
	const nonce = "remote-nonce-1"
	session := workspaceapi.PreviewSession{
		PreviewSessionID: "preview-remote-1",
		WorkspaceID:      ws.ID,
		MachineID:        remoteID,
		State:            workspaceapi.PreviewStateActive,
		URL:              "http://127.0.0.1:7777/v1/preview-proxy/" + nonce + "/",
		Port:             8080,
		ExpiresAt:        time.Now().Add(time.Hour),
	}
	if err := env.st.UpsertPreviewSession(ctx, remoteID, session); err != nil {
		t.Fatalf("UpsertPreviewSession: %v", err)
	}
	env.peers.body = "stub-peer-body"

	resp := env.do(t, http.MethodGet, "/v1/preview-proxy/"+nonce+"/page?x=1", "", false)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("proxy status = %d, want 200: %s", resp.StatusCode, b)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "stub-peer-body" {
		t.Fatalf("proxy body = %q, want stub-peer-body", body)
	}
	calls := env.peers.snapshot()
	if len(calls) != 1 || calls[0].machineID != remoteID || calls[0].nonce != nonce {
		t.Fatalf("peer connector calls = %+v, want 1 call machine_id=%s nonce=%s", calls, remoteID, nonce)
	}
}
