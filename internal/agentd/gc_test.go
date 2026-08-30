// gc_test.go —— B298 agentd gc 预览/执行与 HTTP 接缝测试。
//
// 职责：
//   - 锁定终态扫描、叶子去重、快照重读、脏树 skip、缺失幂等与删除失败入 JSON
//   - 验证 GET 预览 / POST 执行写 200 JSON，未鉴权 401
//
// 边界：
//   - 复用 cachegc_test 与 reclaim_test 的夹具，不另造 git init
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
)

func newGCServer(t *testing.T) (*Server, *Manager) {
	t.Helper()
	m, st, hub, _ := newTestManager(t)
	s := &Server{st: st, hub: hub, log: m.log, mgr: m}
	s.cfg.Store(&config.Config{Token: "test"})
	return s, m
}

func doGC(t *testing.T, s *Server, method, rawURL, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, rawURL, nil)
	} else {
		r = httptest.NewRequest(method, rawURL, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Host = "127.0.0.1:7777"
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, r)
	return rec
}

func TestGCPreviewListsTerminalLeavesWithoutDeleting(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	term := "aaaaaaa1-0000-4000-8000-000000000001"
	live := "bbbbbbbb-0000-4000-8000-000000000002"
	active, legacy, _ := seedTaskWithCache(t, m, term, proto.TaskStateFailed)
	liveActive, _, _ := seedTaskWithCache(t, m, live, proto.TaskStateRunning)
	orphan := filepath.Join(m.cfg.DataDir, "tmp", "orphan99")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "x"), []byte("orphan"), 0o644); err != nil {
		t.Fatal(err)
	}
	resp, err := m.GC(context.Background(), false, false)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !resp.Preview || resp.Force {
		t.Fatalf("preview/force 标记错误：%+v", resp)
	}
	if resp.Scanned != 1 {
		t.Fatalf("scanned=%d want 1（只计终态行）", resp.Scanned)
	}
	if resp.ReleasableBytes == nil || *resp.ReleasableBytes == 0 {
		t.Fatalf("应报告可释放字节，实得 %+v", resp.ReleasableBytes)
	}
	if _, err := os.Lstat(active); err != nil {
		t.Fatalf("预览不得删终态叶子: %v", err)
	}
	if _, err := os.Lstat(liveActive); err != nil {
		t.Fatalf("预览不得碰非终态: %v", err)
	}
	if _, err := os.Lstat(orphan); err != nil {
		t.Fatalf("孤儿目录不得扫删: %v", err)
	}
	foundTerm := false
	for _, row := range resp.CacheRows {
		if row.Path == liveActive {
			t.Fatal("非终态叶子不得出现在 cache_rows")
		}
		if row.Path == orphan {
			t.Fatal("孤儿路径不得入表")
		}
		if row.TaskID == term && row.Status != proto.GCItemPlanned {
			t.Fatalf("终态行应为 planned，实得 %+v", row)
		}
		if row.TaskID == term {
			foundTerm = true
		}
	}
	if !foundTerm {
		t.Fatalf("缺少终态 cache 行: %+v", resp.CacheRows)
	}
	_ = legacy
}

func TestGCScannedCountsAllTerminalRows(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	now := time.Now().UTC()
	mustCreateTask(t, m.st, &proto.Task{ID: "s1", State: proto.TaskStateCompleted, CreatedAt: now, UpdatedAt: now, Executor: "fake"})
	mustCreateTask(t, m.st, &proto.Task{ID: "s2", State: proto.TaskStateFailed, CreatedAt: now, UpdatedAt: now, Executor: "fake"})
	mustCreateTask(t, m.st, &proto.Task{ID: "s3", State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now, Executor: "fake"})
	mustCreateTask(t, m.st, &proto.Task{ID: "s4", State: proto.TaskStateWaitingReview, CreatedAt: now, UpdatedAt: now, Executor: "fake"})
	resp, err := m.GC(context.Background(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Scanned != 2 {
		t.Fatalf("scanned=%d want 2（completed+failed，含无叶子行；waiting_review/running 不计）", resp.Scanned)
	}
	if resp.ReleasableBytes == nil || *resp.ReleasableBytes != 0 {
		t.Fatalf("无叶子时应显式 0，实得 %+v", resp.ReleasableBytes)
	}
}

func TestGCDedupesSharedActiveLeafBytesAndDelete(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	a := "abcdabcd-0000-4000-8000-00000000000a"
	b := "abcdabcd-0000-4000-8000-00000000000b"
	active, _, _ := seedTaskWithCache(t, m, a, proto.TaskStateFailed)
	now := time.Now().UTC()
	mustCreateTask(t, m.st, &proto.Task{ID: b, Target: "local", Executor: "fake", State: proto.TaskStateCompleted, CreatedAt: now, UpdatedAt: now})
	resp, err := m.GC(context.Background(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, row := range resp.CacheRows {
		if row.Path == active {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("共用活动叶子应只报告一次，实得 %d 行 %+v", n, resp.CacheRows)
	}
	want, err := sumRegularFileBytes(active)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ReleasableBytes == nil || *resp.ReleasableBytes < want {
		t.Fatalf("去重字节应含这一份叶子 %d，实得 %+v", want, resp.ReleasableBytes)
	}
	if _, err := m.GC(context.Background(), false, true); err != nil {
		t.Fatal(err)
	}
	assertGone(t, active)
}

func TestGCExecuteRereadsSnapshot(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	id := "re-read00-0000-4000-8000-000000000001"
	active, _, _ := seedTaskWithCache(t, m, id, proto.TaskStateFailed)
	preview, err := m.GC(context.Background(), false, false)
	if err != nil || preview.Scanned != 1 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	if err := st.UpdateTaskState(id, proto.TaskStateRunning); err != nil {
		t.Fatal(err)
	}
	execResp, err := m.GC(context.Background(), false, true)
	if err != nil {
		t.Fatal(err)
	}
	if execResp.Preview {
		t.Fatal("execute 的 preview 必须 false")
	}
	if execResp.Scanned != 0 {
		t.Fatalf("变成 running 后 scanned=%d want 0", execResp.Scanned)
	}
	if _, err := os.Lstat(active); err != nil {
		t.Fatalf("执行必须重读快照，不得删已 running 的叶子: %v", err)
	}
}

func TestGCExecuteDeletesNewTerminal(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	id := "new-term0-0000-4000-8000-000000000001"
	active, _, _ := seedTaskWithCache(t, m, id, proto.TaskStateRunning)
	preview, _ := m.GC(context.Background(), false, false)
	if preview.Scanned != 0 {
		t.Fatalf("running 预览 scanned=%d", preview.Scanned)
	}
	if err := st.UpdateTaskState(id, proto.TaskStateFailed); err != nil {
		t.Fatal(err)
	}
	if _, err := m.GC(context.Background(), false, true); err != nil {
		t.Fatal(err)
	}
	assertGone(t, active)
}

func TestGCForcePreviewDoesNotDeleteDirtyTree(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-gc-dirty", "f-gc-dirty")
	if err := os.WriteFile(filepath.Join(wt, "probe.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	id := seedTerminalTask(t, m, repo, wt, "f-gc-dirty", proto.TaskStateFailed, true)
	resp, err := m.GC(context.Background(), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Force || !resp.Preview {
		t.Fatalf("force 预览标记错误 %+v", resp)
	}
	if _, err := os.Lstat(wt); err != nil {
		t.Fatalf("force 预览不得删脏树: %v", err)
	}
	_ = id
}

func TestGCExecuteSkipsDirtyWithoutForceAndContinues(t *testing.T) {
	m, repo := newReclaimManager(t)
	dirtyWT := newWorktree(t, repo, "wt-gc-d", "f-gc-d")
	if err := os.WriteFile(filepath.Join(dirtyWT, "probe.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cleanWT := newWorktree(t, repo, "wt-gc-c", "f-gc-c")
	dirtyID := seedTerminalTask(t, m, repo, dirtyWT, "f-gc-d", proto.TaskStateFailed, true)
	cleanID := seedTerminalTask(t, m, repo, cleanWT, "f-gc-c", proto.TaskStateFailed, true)
	active, _, _ := writeCacheLeaves(t, m.cfg.DataDir, cleanID)
	resp, err := m.GC(context.Background(), false, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dirtyWT); err != nil {
		t.Fatalf("无 force 脏树必须留: %v", err)
	}
	if _, err := os.Lstat(cleanWT); !os.IsNotExist(err) {
		t.Fatalf("净树应被 reclaim 掉: %v", err)
	}
	assertGone(t, active)
	if resp.Failures != 0 {
		t.Fatalf("skip 不得计入 Failures，实得 %d", resp.Failures)
	}
	var sawSkip, sawDeleted bool
	for _, row := range resp.WorktreeRows {
		if row.TaskID == dirtyID && row.Status == proto.GCItemSkipped {
			sawSkip = true
		}
		if row.TaskID == cleanID && row.Status == proto.GCItemDeleted {
			sawDeleted = true
		}
	}
	if !sawSkip || !sawDeleted {
		t.Fatalf("应同时有脏 skip 与净 deleted：%+v", resp.WorktreeRows)
	}
}

func TestGCExecuteForceRemovesDirty(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-gc-force", "f-gc-force")
	if err := os.WriteFile(filepath.Join(wt, "probe.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedTerminalTask(t, m, repo, wt, "f-gc-force", proto.TaskStateFailed, true)
	if _, err := m.GC(context.Background(), true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(wt); !os.IsNotExist(err) {
		t.Fatalf("force 执行应删脏树: %v", err)
	}
}

func TestGCMissingLeafIsIdempotentNotFailed(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	id := "missing0-0000-4000-8000-000000000001"
	now := time.Now().UTC()
	mustCreateTask(t, m.st, &proto.Task{ID: id, Executor: "fake", State: proto.TaskStateFailed, CreatedAt: now, UpdatedAt: now})
	resp, err := m.GC(context.Background(), false, true)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Failures != 0 {
		t.Fatalf("缺失叶子不得 failed，Failures=%d rows=%+v", resp.Failures, resp.CacheRows)
	}
	if resp.ReleasableBytes == nil || *resp.ReleasableBytes != 0 {
		t.Fatalf("缺失不得虚增字节：%+v", resp.ReleasableBytes)
	}
}

func TestGCRemoveAllFailureIsFailedRowAndContinues(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	id := "failrm00-0000-4000-8000-000000000001"
	other := "okrm0000-0000-4000-8000-000000000002"
	active, _, _ := seedTaskWithCache(t, m, id, proto.TaskStateFailed)
	otherActive, _, _ := seedTaskWithCache(t, m, other, proto.TaskStateFailed)
	m.removeCacheLeafFn = func(path string) error {
		if path == active {
			return errors.New("cache-remove-injected")
		}
		return os.RemoveAll(path)
	}
	resp, err := m.GC(context.Background(), false, true)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Failures < 1 {
		t.Fatalf("注入失败必须计入 Failures，实得 %+v", resp)
	}
	var sawFail bool
	for _, row := range resp.CacheRows {
		if row.Path == active && row.Status == proto.GCItemFailed && strings.Contains(row.Error, "cache-remove-injected") {
			sawFail = true
		}
	}
	if !sawFail {
		t.Fatalf("失败必须进 JSON 行：%+v", resp.CacheRows)
	}
	assertGone(t, otherActive)
}

func TestHandleGCGetPreviewPostExecuteAndAuth(t *testing.T) {
	s, m := newGCServer(t)
	id := "httpgc00-0000-4000-8000-000000000001"
	seedTaskWithCache(t, m, id, proto.TaskStateFailed)

	unauth := doGC(t, s, http.MethodGet, "/api/gc", "", "")
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("未鉴权 GET /api/gc 应 401，实得 %d %s", unauth.Code, unauth.Body.String())
	}
	unauthP := doGC(t, s, http.MethodPost, "/api/gc", `{"force":false}`, "")
	if unauthP.Code != http.StatusUnauthorized {
		t.Fatalf("未鉴权 POST /api/gc 应 401，实得 %d", unauthP.Code)
	}

	get := doGC(t, s, http.MethodGet, "/api/gc", "", "test")
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

	forceGet := doGC(t, s, http.MethodGet, "/api/gc?force=true", "", "test")
	var fg proto.GCResp
	if err := json.Unmarshal(forceGet.Body.Bytes(), &fg); err != nil {
		t.Fatal(err)
	}
	if !fg.Preview || !fg.Force {
		t.Fatalf("GET ?force=true 仍是预览且 force=true，实得 %+v", fg)
	}

	post := doGC(t, s, http.MethodPost, "/api/gc", `{"force":false}`, "test")
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
	s, _ := newGCServer(t)
	rec := doGC(t, s, http.MethodGet, "/api/gc", "", "test")
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
