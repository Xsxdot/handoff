// agentd 换版接口测试（package agentd：复用 watchdog_test.go 的 newTestStore）。
//
// 覆盖 B59 Task 3 的 7 个用例：两道闸（非托管硬拒绝且 force 不越过 / 活跃任务
// 默认拒绝可 force 越过）、waiting_review 不计入活跃、body 为空只重启、缺 tag/sha256
// 拒绝、校验/自检失败不许走到 Activate。全部经注入的 UpdateDeps 桩，不碰真实
// 二进制、真实进程管理器、真实 GitHub。
package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/google/uuid"
)

// seedTask 以指定状态落一条任务。CreateTask 原样入库不经状态机，
// 直接给最终状态即可（watchdog_test 的 seedRunningTask 等是「创建后再迁移」
// 的另一条路，这里不需要中间态）。
func seedTask(t *testing.T, st *store.Store, state proto.TaskState) {
	t.Helper()
	now := time.Now().UTC()
	if err := st.CreateTask(&proto.Task{ID: uuid.NewString(), Target: "local", State: state, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
}

// newUpdateServer 起一个只用于换版接口的 Server：注入全部外部依赖，
// 一次也不碰真实二进制、真实进程管理器、真实 GitHub。
func newUpdateServer(t *testing.T, st *store.Store, managed bool) (*Server, *[]string) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(&config.Config{Token: "tk", DataDir: t.TempDir()}, st, log)
	acts := &[]string{}
	target := filepath.Join(t.TempDir(), "handoff")
	os.WriteFile(target, []byte("old"), 0o755)
	srv.SetUpdateDeps(UpdateDeps{
		Getenv: func(k string) string {
			if managed && k == "INVOCATION_ID" {
				return "test"
			}
			return ""
		},
		Executable: func() (string, error) { return target, nil },
		Install: func(_ []byte, _, tag, destDir string) (string, error) {
			p := filepath.Join(destDir, ".handoff.new-"+tag)
			os.WriteFile(p, []byte("new"), 0o755)
			return p, nil
		},
		Activate: func(newPath, tgt string) (string, error) {
			*acts = append(*acts, newPath+"->"+tgt)
			return tgt + ".prev", nil
		},
	})
	srv.SetRestart(func(reason string) bool { *acts = append(*acts, "restart:"+reason); return true })
	return srv, acts
}

// post 发一次换版请求，返回状态码与解出的错误体（200 时 UpdateError 为零值）。
func post(t *testing.T, srv *Server, query string, body []byte) (int, proto.UpdateError, proto.UpdateResp) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/update?"+query, bytes.NewReader(body))
	// httptest.NewRequest 的默认 Host 是 example.com，会被 hostGuard 在鉴权前
	// 403 掉（W3 的 Host 白名单）。本组用例测的是换版的两道闸，不是白名单，
	// 因此显式给一个回环 Host 让请求走到 handler。
	req.Host = "127.0.0.1:7777"
	req.Header.Set("Authorization", "Bearer tk")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	var e proto.UpdateError
	var ok proto.UpdateResp
	json.Unmarshal(w.Body.Bytes(), &e)
	json.Unmarshal(w.Body.Bytes(), &ok)
	return w.Code, e, ok
}

// TestUpdateRejectsUnmanagedEvenWithForce 是闸二的核心断言，也是整个接口
// 最不能出错的一条：换完 exit(0) 之后没人拉起，这台机器上就此没有 agentd
// 在跑，且没有任何信号告诉任何人。force 不是逃生口。
func TestUpdateRejectsUnmanagedEvenWithForce(t *testing.T) {
	st := newTestStore(t)
	srv, acts := newUpdateServer(t, st, false /*managed*/)
	code, e, _ := post(t, srv, "tag=v9.9.9&sha256="+strings.Repeat("a", 64)+"&force=1", []byte("tgz"))
	if code != http.StatusConflict {
		t.Fatalf("状态码 = %d，期望 409", code)
	}
	if e.Reason != proto.UpdateReasonUnmanaged {
		t.Fatalf("reason = %q，期望 %q", e.Reason, proto.UpdateReasonUnmanaged)
	}
	if len(*acts) != 0 {
		t.Fatalf("被拒后不该有任何换版或重启动作，实得 %v", *acts)
	}
}

// TestUpdateRejectsBusyWithoutForce / TestUpdateForceCrossesBusy 是闸一的
// 两半：默认保护，--force 越过。activeTaskCount 的口径是 running +
// waiting_answer，waiting_review 不计入（它可能挂几天）。
func TestUpdateRejectsBusyWithoutForce(t *testing.T) {
	st := newTestStore(t)
	seedTask(t, st, proto.TaskStateRunning)
	srv, acts := newUpdateServer(t, st, true)
	code, e, _ := post(t, srv, "tag=v9.9.9&sha256="+strings.Repeat("a", 64), []byte("tgz"))
	if code != http.StatusConflict || e.Reason != proto.UpdateReasonBusy {
		t.Fatalf("期望 409/busy，实得 %d/%q", code, e.Reason)
	}
	if len(*acts) != 0 {
		t.Fatalf("被拒后不该有动作，实得 %v", *acts)
	}
}

func TestUpdateForceCrossesBusy(t *testing.T) {
	st := newTestStore(t)
	seedTask(t, st, proto.TaskStateRunning)
	srv, acts := newUpdateServer(t, st, true)
	code, _, ok := post(t, srv, "tag=v9.9.9&sha256="+strings.Repeat("a", 64)+"&force=1", []byte("tgz"))
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", code)
	}
	if !ok.OK || ok.Version != "v9.9.9" || ok.Prev == "" {
		t.Fatalf("响应体不完整: %+v", ok)
	}
	if len(*acts) != 2 || !strings.HasPrefix((*acts)[1], "restart:") {
		t.Fatalf("必须先 Activate 再 restart，实得 %v", *acts)
	}
}

// TestUpdateWaitingReviewDoesNotBlock 锁住活跃口径：waiting_review 不计入。
func TestUpdateWaitingReviewDoesNotBlock(t *testing.T) {
	st := newTestStore(t)
	seedTask(t, st, proto.TaskStateWaitingReview)
	srv, _ := newUpdateServer(t, st, true)
	code, e, _ := post(t, srv, "tag=v9.9.9&sha256="+strings.Repeat("a", 64), []byte("tgz"))
	if code != http.StatusOK {
		t.Fatalf("waiting_review 不该拦下换版，实得 %d/%q", code, e.Error)
	}
}

// TestUpdateEmptyBodyRestartsOnly 是 D8：不带 body = 只重启不换版。
// 本机的二进制由 CLI 直接换（文件就在本地），但仍需要 agentd 重启才生效。
func TestUpdateEmptyBodyRestartsOnly(t *testing.T) {
	st := newTestStore(t)
	srv, acts := newUpdateServer(t, st, true)
	code, _, ok := post(t, srv, "", nil)
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", code)
	}
	if ok.Version != "" {
		t.Fatalf("纯重启不该报换上的版本，实得 %q", ok.Version)
	}
	if len(*acts) != 1 || !strings.HasPrefix((*acts)[0], "restart:") {
		t.Fatalf("纯重启只该有 restart 一个动作，实得 %v", *acts)
	}
}

// TestUpdateRequiresTagAndSum：带 body 就必须给 tag 与 sha256，
// 缺任一个都不能放行——少了它们，agentd 侧的完整性校验与自检都无从比对。
func TestUpdateRequiresTagAndSum(t *testing.T) {
	st := newTestStore(t)
	srv, acts := newUpdateServer(t, st, true)
	for _, q := range []string{"", "tag=v9.9.9", "sha256=" + strings.Repeat("a", 64)} {
		code, e, _ := post(t, srv, q, []byte("tgz"))
		if code != http.StatusBadRequest {
			t.Fatalf("query %q 应 400，实得 %d", q, code)
		}
		if e.Reason != "" {
			t.Fatalf("参数错不属于两道闸，reason 必须为空，实得 %q", e.Reason)
		}
	}
	if len(*acts) != 0 {
		t.Fatalf("参数错不该有动作，实得 %v", *acts)
	}
}

// TestUpdateInstallFailureDoesNotActivate：校验/解包/自检任一失败，
// 都不许走到 Activate——换上一个跑不起来的二进制，agentd 就再也起不来了。
func TestUpdateInstallFailureDoesNotActivate(t *testing.T) {
	st := newTestStore(t)
	srv, acts := newUpdateServer(t, st, true)
	srv.SetUpdateDeps(UpdateDeps{
		Getenv: func(k string) string {
			if k == "INVOCATION_ID" {
				return "test"
			}
			return ""
		},
		Executable: func() (string, error) { return filepath.Join(t.TempDir(), "handoff"), nil },
		Install:    func([]byte, string, string, string) (string, error) { return "", errors.New("自检失败") },
		Activate:   func(string, string) (string, error) { t.Fatal("不该走到 Activate"); return "", nil },
	})
	code, e, _ := post(t, srv, "tag=v9.9.9&sha256="+strings.Repeat("a", 64), []byte("tgz"))
	if code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400", code)
	}
	if !strings.Contains(e.Error, "自检失败") {
		t.Fatalf("错误原文必须带出根因，实得 %q", e.Error)
	}
	if len(*acts) != 0 {
		t.Fatalf("失败后不该有动作，实得 %v", *acts)
	}
}

// newTestServerManaged 建一个托管 + 无活跃任务的换版 Server（Task 7+ 的通用助手）。
//
// 依赖扩展（Task 8）：
//   - 注入 manager：handleStatus 在 mgr==nil 时返回 503，而 status 测试直接调它
//   - 补 Platform 缝：runPull 会调 s.upd.Platform()，Task 7 的 newUpdateServer 没设
//     它是 nil，不补会 panic
func newTestServerManaged(t *testing.T) *Server {
	t.Helper()
	st := newTestStore(t)
	srv, _ := newUpdateServer(t, st, true)
	srv.SetManager(NewManager(st, srv.Hub(), map[string]executor.Adapter{}, srv.conf(), nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil))))
	srv.upd.Platform = func() (string, string) { return "linux", "amd64" }
	return srv
}

// doUpdate 直接调 s.handleUpdate 发一次换版请求，返回响应记录器。
func doUpdate(t *testing.T, s *Server, query string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/update"+query, bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleUpdate(w, req)
	return w
}

// mode=pull 缺 tag 或缺 sha256 一律 400：缺了它们无从校验完整性，
// 而"下一个来路不明的二进制装上去"是这条链路最不能容忍的失败。
func TestPullRequiresTagAndSum(t *testing.T) {
	for _, q := range []string{
		"?mode=pull",
		"?mode=pull&tag=v1.0.0",
		"?mode=pull&sha256=abc",
	} {
		s := newTestServerManaged(t)
		rr := doUpdate(t, s, q, nil)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s 应 400，实得 %d", q, rr.Code)
		}
	}
}

// mode 非法值不得静默降级成某个默认模式——猜错的代价是装错东西或白重启。
func TestUnknownModeRejected(t *testing.T) {
	s := newTestServerManaged(t)
	rr := doUpdate(t, s, "?mode=sideload&tag=v1.0.0&sha256=abc", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("非法 mode 应 400，实得 %d", rr.Code)
	}
}

// mode=push 带空 body → 400。调用方显式说了"我要推送"却没带字节，这是个 bug；
// 静默当成纯重启会让它以为换版成功了。
func TestPushModeRejectsEmptyBody(t *testing.T) {
	s := newTestServerManaged(t)
	rr := doUpdate(t, s, "?mode=push&tag=v1.0.0&sha256=abc", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("mode=push 空 body 应 400，实得 %d", rr.Code)
	}
}

// mode=pull 带非空 body → 400。两种模式的意图互斥，不做"猜一个"的兼容。
func TestPullModeRejectsBody(t *testing.T) {
	s := newTestServerManaged(t)
	rr := doUpdate(t, s, "?mode=pull&tag=v1.0.0&sha256=abc", []byte("x"))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("mode=pull 带 body 应 400，实得 %d", rr.Code)
	}
}

// 回归：空 body 且无 mode 仍是纯重启（B59 D8 行为一字不变）。
func TestEmptyBodyNoModeStillRestarts(t *testing.T) {
	s := newTestServerManaged(t)
	rr := doUpdate(t, s, "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("纯重启应 200，实得 %d: %s", rr.Code, rr.Body)
	}
	var out proto.UpdateResp
	json.Unmarshal(rr.Body.Bytes(), &out)
	if !out.Restarted || out.Accepted {
		t.Fatalf("纯重启应 restarted=true accepted=false，实得 %+v", out)
	}
}

// 并发锁：一个自拉在跑时，第二个请求 409 + pull_in_progress。
// 没有这道锁，两个 goroutine 会往同一个临时文件路径写，互相截断出一个坏二进制。
func TestPullTrackerRejectsConcurrent(t *testing.T) {
	p := newPullTracker()
	if !p.begin("v1.0.0") {
		t.Fatal("首次 begin 应成功")
	}
	if p.begin("v1.0.1") {
		t.Fatal("已有自拉在跑时 begin 应失败")
	}
	p.fail(errors.New("boom"))
	if !p.begin("v1.0.2") {
		t.Fatal("失败释放后 begin 应能再次成功")
	}
}

// 没跑过自拉时 snapshot 返回 nil：status 不该显示一个编出来的空状态。
func TestPullTrackerSnapshotNilWhenIdle(t *testing.T) {
	if got := newPullTracker().snapshot(); got != nil {
		t.Fatalf("空闲时应返回 nil，实得 %+v", got)
	}
}

// 失败后 snapshot 必须留住阶段与错误原文——进程不重启，这正是要查它的场合。
func TestPullTrackerKeepsFailure(t *testing.T) {
	p := newPullTracker()
	p.begin("v1.0.0")
	p.stage(proto.PullStageDownloading)
	p.fail(errors.New("proxyconnect tcp: connection refused"))
	got := p.snapshot()
	if got == nil || got.Stage != proto.PullStageFailed {
		t.Fatalf("应留下 failed 状态，实得 %+v", got)
	}
	if !strings.Contains(got.Error, "connection refused") {
		t.Errorf("应留下错误原文，实得 %q", got.Error)
	}
	if got.Tag != "v1.0.0" {
		t.Errorf("应留下 tag，实得 %q", got.Tag)
	}
}

// 自拉进行中再来一个 push 必须 409 + pull_in_progress：
// 两个换版会写同一个确定性临时文件（release.TempName(tag)），互相截断出坏二进制。
func TestPushRejectedWhilePullRunning(t *testing.T) {
	s := newTestServerManaged(t)
	if !s.pull.begin("v1.0.0") {
		t.Fatal("begin 应成功")
	}
	rr := doUpdate(t, s, "?mode=push&tag=v1.0.0&sha256=abc", []byte("tgz"))
	if rr.Code != http.StatusConflict {
		t.Fatalf("自拉进行中 push 应 409，实得 %d", rr.Code)
	}
	var e proto.UpdateError
	json.Unmarshal(rr.Body.Bytes(), &e)
	if e.Reason != proto.UpdateReasonPullInProgress {
		t.Fatalf("reason 应 pull_in_progress，实得 %q", e.Reason)
	}
}

// 成功路径：下载 → 安装 → 换版 → 触发重启，且四步都被真的调到。
func TestPullSuccessInstallsAndRestarts(t *testing.T) {
	s := newTestServerManaged(t)
	var activated, restarted bool
	done := make(chan struct{})
	s.upd.FetchByTag = func(_ context.Context, tag, goos, goarch, sum string) ([]byte, error) {
		return []byte("archive"), nil
	}
	s.upd.Install = func(tgz []byte, wantSum, wantTag, destDir string) (string, error) {
		return filepath.Join(destDir, "new"), nil
	}
	s.upd.Activate = func(newPath, target string) (string, error) {
		activated = true
		return target + ".prev", nil
	}
	s.SetRestart(func(string) bool { restarted = true; close(done); return true })

	rr := doUpdate(t, s, "?mode=pull&tag=v1.0.0&sha256=abc", nil)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("自拉应 202，实得 %d: %s", rr.Code, rr.Body)
	}
	var out proto.UpdateResp
	json.Unmarshal(rr.Body.Bytes(), &out)
	if !out.Accepted || out.Version != "v1.0.0" {
		t.Fatalf("响应应为 accepted + version，实得 %+v", out)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("后台自拉未在 5s 内触发重启")
	}
	if !activated || !restarted {
		t.Fatalf("activated=%v restarted=%v，两者都应为 true", activated, restarted)
	}
}

// 下载失败：不得 Activate、不得重启，状态落 failed 且带错误原文。
// 这条是"失败时协调者能拿到原因"的落点——没有它，一次代理配错要让人
// 干等到超时才看到一句「版本仍是 X」。
func TestPullDownloadFailureRecordsAndDoesNotActivate(t *testing.T) {
	s := newTestServerManaged(t)
	var activated, restarted bool
	s.upd.FetchByTag = func(_ context.Context, _, _, _, _ string) ([]byte, error) {
		return nil, errors.New("proxyconnect tcp: dial tcp 127.0.0.1:1080: connection refused")
	}
	s.upd.Activate = func(string, string) (string, error) { activated = true; return "", nil }
	s.SetRestart(func(string) bool { restarted = true; return true })

	rr := doUpdate(t, s, "?mode=pull&tag=v1.0.0&sha256=abc", nil)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("受理阶段应 202，实得 %d", rr.Code)
	}
	waitPullStage(t, s, proto.PullStageFailed, 5*time.Second)

	if activated || restarted {
		t.Fatalf("下载失败不得换版或重启，activated=%v restarted=%v", activated, restarted)
	}
	got := s.pull.snapshot()
	if !strings.Contains(got.Error, "connection refused") {
		t.Errorf("状态应留下错误原文，实得 %q", got.Error)
	}
}

// 安装失败（sha256 不符、自检不过等）同样不得 Activate。
func TestPullInstallFailureDoesNotActivate(t *testing.T) {
	s := newTestServerManaged(t)
	var activated bool
	s.upd.FetchByTag = func(_ context.Context, _, _, _, _ string) ([]byte, error) {
		return []byte("archive"), nil
	}
	s.upd.Install = func([]byte, string, string, string) (string, error) {
		return "", errors.New("自检失败：版本号对不上")
	}
	s.upd.Activate = func(string, string) (string, error) { activated = true; return "", nil }

	doUpdate(t, s, "?mode=pull&tag=v1.0.0&sha256=abc", nil)
	waitPullStage(t, s, proto.PullStageFailed, 5*time.Second)
	if activated {
		t.Fatal("安装失败不得换版")
	}
}

// status 必须上报能力位与自拉状态：前者是协调者的选路判据，
// 后者是失败时唯一能拿到原因的地方。
func TestStatusReportsPullCapabilityAndState(t *testing.T) {
	s := newTestServerManaged(t)
	rr := httptest.NewRecorder()
	s.handleStatus(rr, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	var st proto.StatusResp
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Update == nil || st.Update.Pull == nil || !*st.Update.Pull {
		t.Fatalf("status 应上报 pull=true，实得 %+v", st.Update)
	}
	if st.Update.PullState != nil {
		t.Errorf("没跑过自拉时 pull_state 应为 nil，实得 %+v", st.Update.PullState)
	}
}

// waitPullStage 轮询等状态到达期望阶段。
// 用轮询而不是 sleep：后台 goroutine 的耗时不确定，固定 sleep 要么慢要么脆。
func waitPullStage(t *testing.T, s *Server, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if st := s.pull.snapshot(); st != nil && st.Stage == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待 pull 阶段 %q 超时，实得 %+v", want, s.pull.snapshot())
}
