// gc_test.go —— B298 gc 预览/执行的 Manager 层测试（HTTP 接缝用例在
// gateway_http_test.go，B233.26 因测试变体类型约束转外部测试包）。
//
// 职责：
//   - 锁定终态扫描、叶子去重、快照重读、脏树 skip、缺失幂等与删除失败入 JSON
//
// 边界：
//   - 复用 cachegc_test 与 reclaim_test 的夹具，不另造 git init
package orchestration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/orchestration/internal/cacheplan"
	"github.com/Xsxdot/handoff/internal/proto"
)

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
	want, err := cacheplan.SumRegularFileBytes(active)
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
