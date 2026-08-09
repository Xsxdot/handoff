// agentd desktop_server 测试：桌面 /v1 路由的鉴权、线格式与 Operation 语义。
//
// 职责：
//   - GET /v1/bootstrap 返回快照与 revision
//   - POST /v1/projects/operations 项目创建 202 + Operation 返回
//   - 同 operation ID 返回现有权威 Operation，不重复执行
//   - GET /v1/operations/{id} 查询
//   - Problem 线格式
//
// 边界：
//   - 使用真实 store + httptest
//   - control stream 单独测试（control_stream_test.go）
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/store"
)

// testToken 是桌面测试用 token。
const testToken = "test-token"

// testEnv 是桌面路由测试的轻量环境（与 agentd_test 包的 testEnv 并列，
// 此处为 package agentd 内部测试，无法复用外部测试包的未导出类型）。
type testEnv struct {
	srv   *Server
	ts    *httptest.Server
	st    *store.Store
	token string
}

// newDesktopTestEnv 组装带 desktop 路由的测试环境。
func newDesktopTestEnv(t *testing.T) *testEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	// 确保本机 machine 存在（ProjectService.validateForm 查机器注册表）
	if _, err := st.EnsureLocalMachine(context.Background(), "本机"); err != nil {
		t.Fatalf("EnsureLocalMachine: %v", err)
	}
	cfg := &config.Config{Token: testToken}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(cfg, st, logger)
	// 注入 ProjectService（用真实 MachineCommander 会失败，这里用空 commander
	// 只会让 operation 进入 failed；bootstrap/operation 查询不需要 commander）
	srv.SetProjectService(controlplane.NewProjectService(st, nilCommander{}, logger))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &testEnv{srv: srv, ts: ts, st: st, token: cfg.Token}
}

// get 发起带 token 的 GET 请求。
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

// post 发起带 token 的 POST 请求。
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

// nilCommander 是返回错误的 MachineCommander（让 operation 落 failed 而非 panic）。
type nilCommander struct{}

func (nilCommander) InspectPath(context.Context, controlplane.InspectPathCommand) (controlplane.PathInspection, error) {
	return controlplane.PathInspection{}, errors.New("commander 不可用")
}

func (nilCommander) Clone(context.Context, controlplane.CloneLocationCommand) (controlplane.PathInspection, error) {
	return controlplane.PathInspection{}, errors.New("commander 不可用")
}

// TestDesktopBootstrap 验证 GET /v1/bootstrap 返回快照与 revision。
func TestDesktopBootstrap(t *testing.T) {
	env := newDesktopTestEnv(t)
	resp := env.get(t, "/v1/bootstrap")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	defer resp.Body.Close()
	var body struct {
		Machines        []json.RawMessage `json:"machines"`
		Projects        []json.RawMessage `json:"projects"`
		ControlRevision int64             `json:"control_revision"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	if body.Machines == nil {
		t.Fatal("machines 应为数组而非 null")
	}
	if body.ControlRevision < 0 {
		t.Fatalf("control_revision = %d, want >= 0", body.ControlRevision)
	}
}

// TestDesktopBootstrapRequiresAuth 验证 bootstrap 需鉴权。
func TestDesktopBootstrapRequiresAuth(t *testing.T) {
	env := newDesktopTestEnv(t)
	resp, err := http.Get(env.ts.URL + "/v1/bootstrap")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// TestDesktopCreateOperation202 验证 POST /v1/projects/operations 返回 202 + Operation。
func TestDesktopCreateOperation202(t *testing.T) {
	env := newDesktopTestEnv(t)
	localID := localMachineIDFromEnv(t, env)
	body := `{"operation_id":"op-uuid-1","name":"super-debug","locations":[
	  {"machine_id":"` + localID + `","role":"local","source":"existing_path","path":"/repo"}]}`
	resp := env.post(t, "/v1/projects/operations", body)
	if resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 202: %s", resp.StatusCode, b)
	}
	defer resp.Body.Close()
	var op struct {
		OperationID string `json:"operation_id"`
		State       string `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&op); err != nil {
		t.Fatalf("decode operation: %v", err)
	}
	if op.OperationID != "op-uuid-1" {
		t.Fatalf("operation_id = %q", op.OperationID)
	}
	// 空 commander 会让目标失败 → failed
	if op.State == "" {
		t.Fatal("state 不应为空")
	}
}

// TestDesktopCreateOperationIdempotent 验证同 operation ID 返回现有权威 Operation。
func TestDesktopCreateOperationIdempotent(t *testing.T) {
	env := newDesktopTestEnv(t)
	localID := localMachineIDFromEnv(t, env)
	body := `{"operation_id":"op-uuid-2","name":"p","locations":[
	  {"machine_id":"` + localID + `","role":"local","source":"existing_path","path":"/repo"}]}`
	r1 := env.post(t, "/v1/projects/operations", body)
	var op1 struct {
		OperationID string `json:"operation_id"`
		State       string `json:"state"`
	}
	if err := json.NewDecoder(r1.Body).Decode(&op1); err != nil {
		t.Fatalf("decode op1: %v", err)
	}
	r1.Body.Close()
	r2 := env.post(t, "/v1/projects/operations", body)
	defer r2.Body.Close()
	var op2 struct {
		OperationID string `json:"operation_id"`
		State       string `json:"state"`
	}
	if err := json.NewDecoder(r2.Body).Decode(&op2); err != nil {
		t.Fatalf("decode op2: %v", err)
	}
	if op1.OperationID != "op-uuid-2" || op2.OperationID != "op-uuid-2" {
		t.Fatalf("operation ids = %q / %q, want 同 op-uuid-2", op1.OperationID, op2.OperationID)
	}
}

// TestDesktopGetOperation 验证 GET /v1/operations/{id} 查询。
func TestDesktopGetOperation(t *testing.T) {
	env := newDesktopTestEnv(t)
	localID := localMachineIDFromEnv(t, env)
	// 先创建
	body := `{"operation_id":"op-uuid-3","name":"p","locations":[
	  {"machine_id":"` + localID + `","role":"local","source":"existing_path","path":"/repo"}]}`
	r := env.post(t, "/v1/projects/operations", body)
	io.Copy(io.Discard, r.Body)
	r.Body.Close()

	resp := env.get(t, "/v1/operations/op-uuid-3")
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, b)
	}
	defer resp.Body.Close()
	var op struct {
		OperationID string `json:"operation_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&op); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if op.OperationID != "op-uuid-3" {
		t.Fatalf("operation_id = %q", op.OperationID)
	}
}

// localMachineIDFromEnv 从测试 store 读取本机 Machine ID。
func localMachineIDFromEnv(t *testing.T, env *testEnv) string {
	t.Helper()
	m, err := env.st.EnsureLocalMachine(context.Background(), "本机")
	if err != nil {
		t.Fatalf("EnsureLocalMachine: %v", err)
	}
	return m.ID
}

// TestDesktopGetOperationNotFound 验证不存在的 operation 返回 404 Problem。
func TestDesktopGetOperationNotFound(t *testing.T) {
	env := newDesktopTestEnv(t)
	resp := env.get(t, "/v1/operations/nope")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem.Code == "" {
		t.Fatal("problem.code 不应为空")
	}
}

// TestDesktopEventsRoute 验证 GET /v1/control/events 存在。
func TestDesktopEventsRoute(t *testing.T) {
	env := newDesktopTestEnv(t)
	resp := env.get(t, "/v1/control/events?after=0")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "[]") && len(strings.TrimSpace(string(raw))) > 0 {
		// 允许空数组
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil {
			t.Fatalf("control/events 应为数组: %s", raw)
		}
	}
}
