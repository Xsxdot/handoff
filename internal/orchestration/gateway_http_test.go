// gateway_http_test.go —— 编排实现挂在真 gateway Server 后面的 HTTP 接缝测试
// （reclaim/gc 两组端点的路由、鉴权与状态码）。
//
// 为什么是外部测试包（package orchestration_test）：B233.26 反转 D1 后，
// package orchestration 的包内测试拿到的是 orchestration[test] 测试变体类型，
// 而 agentd.Server.SetManager 收的是 plain 变体的 OrchestrationClient——
// 同源不同包实例，测试变体 *Manager 不满足接口。只有外部测试包与 agentd
// 引用的是同一个 plain orchestration，才能把真 Manager 挂上真 Server。
//
// 边界：只用两包的导出面与公开测试缝（NewManager/NewHub/NewServer/SetManager）；
// git 夹具与任务种子在本文件内自足，不依赖包内测试助手。
package orchestration_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/agentd"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/orchestration"
	"github.com/Xsxdot/handoff/internal/permgate"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/workspace"
)

// httpEnv 是一个挂了真 Manager 的 gateway Server 测试环境。
type httpEnv struct {
	srv     *agentd.Server
	mgr     *orchestration.Manager
	st      *store.Store
	dataDir string
}

func newHTTPEnv(t *testing.T) *httpEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	dataDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{Token: "test", DataDir: dataDir,
		Executor: config.ExecutorConfig{Default: "fake"}}
	g, err := permgate.New(nil, logger)
	if err != nil {
		t.Fatalf("permgate.New: %v", err)
	}
	m := orchestration.NewManager(st, orchestration.NewHub(),
		map[string]executor.Adapter{"fake": fake.New(nil)}, cfg, nil, nil, g, logger)
	m.SetWorkspace(workspace.NewCapability())
	srv := agentd.NewServer(&config.Config{Token: "test"}, st, logger)
	srv.SetManager(m)
	return &httpEnv{srv: srv, mgr: m, st: st, dataDir: dataDir}
}

// doReq 把请求打进真 Server 的 Handler，返回 recorder（与原 doGC/doReclaim 同款）。
func doReq(t *testing.T, s *agentd.Server, method, rawURL, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, rawURL, nil)
	} else {
		r = httptest.NewRequest(method, rawURL, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	// httptest.NewRequest 的默认 Host 是 example.com，会被 hostGuard 在鉴权前
	// 403 掉（W3 的 Host 白名单）。本组用例测的是端点行为，不是白名单，
	// 因此显式给一个回环 Host 让请求走到 handler（与 update_test.go 同款处理）。
	r.Host = "127.0.0.1:7777"
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, r)
	return rec
}

// ---- gc 端点（自 gc_test.go 迁入：同受「真 Server」约束）----

func (e *httpEnv) seedTaskWithCache(t *testing.T, id string, state proto.TaskState) {
	t.Helper()
	now := time.Now().UTC()
	if err := e.st.CreateTask(&proto.Task{
		ID: id, Target: "local", Executor: "fake",
		State: state, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// cache 叶子与任务目录（与包内 writeCacheLeaves 同口径）
	active := executor.TaskTmpDir(e.dataDir, id)
	if err := os.MkdirAll(filepath.Join(active, "gocache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(active, "gocache", "obj"), []byte("cache-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	taskDir := filepath.Join(e.dataDir, "tasks", id)
	legacy := filepath.Join(taskDir, "tmp")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "old"), []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"render.log", "frames.jsonl", "proc.json"} {
		if err := os.WriteFile(filepath.Join(taskDir, name), []byte(name+"-keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHandleGCGetPreviewPostExecuteAndAuth(t *testing.T) {
	env := newHTTPEnv(t)
	id := "httpgc00-0000-4000-8000-000000000001"
	env.seedTaskWithCache(t, id, proto.TaskStateFailed)

	unauth := doReq(t, env.srv, http.MethodGet, "/api/gc", "", "")
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("未鉴权 GET /api/gc 应 401，实得 %d %s", unauth.Code, unauth.Body.String())
	}
	unauthP := doReq(t, env.srv, http.MethodPost, "/api/gc", `{"force":false}`, "")
	if unauthP.Code != http.StatusUnauthorized {
		t.Fatalf("未鉴权 POST /api/gc 应 401，实得 %d", unauthP.Code)
	}

	get := doReq(t, env.srv, http.MethodGet, "/api/gc", "", "test")
	if get.Code != http.StatusOK {
		t.Fatalf("GET 应 200 不是 503，实得 %d %s", get.Code, get.Body.String())
	}
	if strings.Contains(get.Body.String(), "gc 尚未接线") {
		t.Fatal("503 空壳不得再达")
	}
	var preview proto.GCResp
	if err := json.Unmarshal(get.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if !preview.Preview {
		t.Fatal("GET 必须 preview=true")
	}

	forceGet := doReq(t, env.srv, http.MethodGet, "/api/gc?force=true", "", "test")
	var fg proto.GCResp
	if err := json.Unmarshal(forceGet.Body.Bytes(), &fg); err != nil {
		t.Fatal(err)
	}
	if !fg.Preview || !fg.Force {
		t.Fatalf("GET ?force=true 仍是预览且 force=true，实得 %+v", fg)
	}

	post := doReq(t, env.srv, http.MethodPost, "/api/gc", `{"force":false}`, "test")
	if post.Code != http.StatusOK {
		t.Fatalf("POST 应 200，实得 %d %s", post.Code, post.Body.String())
	}
	var execResp proto.GCResp
	if err := json.Unmarshal(post.Body.Bytes(), &execResp); err != nil {
		t.Fatal(err)
	}
	if execResp.Preview {
		t.Fatal("POST 必须 preview=false")
	}
}

func TestHandleGCJSONZeroReleasableBytesPresent(t *testing.T) {
	env := newHTTPEnv(t)
	rec := doReq(t, env.srv, http.MethodGet, "/api/gc", "", "test")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	raw, ok := fields["releasable_bytes"]
	if !ok {
		t.Fatal("空快照成功响应必须带 releasable_bytes:0，不得缺席")
	}
	if string(raw) != "0" {
		t.Fatalf("releasable_bytes=%s want 0", raw)
	}
}

// ---- reclaim 端点（原 reclaim_server_test.go 全组）----

// gitAt 在 dir 里执行 git 命令，失败即 Fatal（与包内 gittesthelpers 同款，外部包自足）。
func gitAt(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// initRepo 在 t.TempDir() 里造一个带初始提交的干净仓库。
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitAt(t, dir, "init", "-q")
	gitAt(t, dir, "checkout", "-b", "main")
	gitAt(t, dir, "config", "user.email", "test@handoff.dev")
	gitAt(t, dir, "config", "user.name", "handoff test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, dir, "add", ".")
	gitAt(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// newWorktree 在 repo 下建一个 managed 风格的工作树并返回其路径。
func newWorktree(t *testing.T, repo, name, branch string) string {
	t.Helper()
	dir := filepath.Join(filepath.Dir(repo), name)
	gitAt(t, repo, "worktree", "add", "-q", dir, "-b", branch)
	return dir
}

// seedTask 往库里塞一个指定状态的任务，返回任务 ID（与包内 seedTerminalTask 同口径）。
func (e *httpEnv) seedTask(t *testing.T, repo, workdir, branch string, state proto.TaskState, managed bool) string {
	t.Helper()
	now := time.Now().UTC()
	id := "t-" + strings.ReplaceAll(t.Name(), "/", "-")
	if err := e.st.CreateTask(&proto.Task{
		ID: id, RepoPath: repo, WorkDir: workdir, Branch: branch,
		State: state, WorktreeManaged: managed, Executor: "fake",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return id
}

// newServerWithDirtyWorktree 造一个 server+manager+真 git 仓库，任务为终态 +
// 脏 managed worktree，返回任务 ID。
func newServerWithDirtyWorktree(t *testing.T) (*httpEnv, string) {
	t.Helper()
	env := newHTTPEnv(t)
	repo := initRepo(t)
	wt := newWorktree(t, repo, "wt-srv1", "f-srv1")
	if err := os.WriteFile(filepath.Join(wt, "probe.log"), []byte("x"), 0o644); err != nil {
		t.Fatalf("造脏：%v", err)
	}
	id := env.seedTask(t, repo, wt, "f-srv1", proto.TaskStateFailed, true)
	return env, id
}

// newServerWithRunningTask 造一个任务为非终态（running）的 server。
func newServerWithRunningTask(t *testing.T) (*httpEnv, string) {
	t.Helper()
	env := newHTTPEnv(t)
	repo := initRepo(t)
	wt := newWorktree(t, repo, "wt-srv2", "f-srv2")
	id := env.seedTask(t, repo, wt, "f-srv2", proto.TaskStateRunning, true)
	return env, id
}

func TestHandleReclaimDirtyReturns409WithReason(t *testing.T) {
	env, id := newServerWithDirtyWorktree(t)
	rec := doReq(t, env.srv, http.MethodPost, "/api/tasks/"+id+"/reclaim", `{"force":false}`, "test")

	if rec.Code != http.StatusConflict {
		t.Fatalf("脏树应返 409，实得 %d：%s", rec.Code, rec.Body.String())
	}
	var body proto.ReclaimError
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应体应是 ReclaimError：%v：%s", err, rec.Body.String())
	}
	if body.Reason != proto.ReasonDirty {
		t.Fatalf("reason 应为 dirty，实得 %q", body.Reason)
	}
	if len(body.Dirty) == 0 {
		t.Fatalf("dirty 清单不能为空——CLI 要靠它渲染改动列表")
	}
}

func TestHandleReclaimNonTerminalReturns409NotTerminal(t *testing.T) {
	env, id := newServerWithRunningTask(t)
	rec := doReq(t, env.srv, http.MethodPost, "/api/tasks/"+id+"/reclaim", `{}`, "test")
	if rec.Code != http.StatusConflict {
		t.Fatalf("非终态应返 409，实得 %d", rec.Code)
	}
	var body proto.ReclaimError
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Reason != proto.ReasonNotTerminal {
		t.Fatalf("reason 应为 not_terminal，实得 %q", body.Reason)
	}
}

func TestHandleReclaimUnknownTaskReturns404(t *testing.T) {
	env, _ := newServerWithDirtyWorktree(t)
	rec := doReq(t, env.srv, http.MethodPost, "/api/tasks/no-such/reclaim", `{}`, "test")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不存在的任务应返 404，实得 %d", rec.Code)
	}
}

func TestHandleReclaimListReturnsRows(t *testing.T) {
	env, id := newServerWithDirtyWorktree(t)
	rec := doReq(t, env.srv, http.MethodGet, "/api/reclaim", "", "test")
	if rec.Code != http.StatusOK {
		t.Fatalf("列表应返 200，实得 %d", rec.Code)
	}
	var body proto.ReclaimListResp
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解列表响应：%v", err)
	}
	if len(body.Rows) != 1 || body.Rows[0].TaskID != id {
		t.Fatalf("应含那条脏树任务，实得 %+v", body.Rows)
	}
}
