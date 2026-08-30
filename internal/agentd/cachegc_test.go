// cachegc_test.go —— 任务私有缓存叶子路径、短号占用与 tmp 根保护测试。
//
// 职责：
//   - 锁定两处叶子形状、空 ID / `..` 的 tmp 根拒绝、短号占用与字节统计
//   - 从 Done/Stop/compensate 声明缝再锁收口删除（缝 1）
//
// 边界：
//   - 不覆盖 Manager.GC 批处理；那属于 gc_test.go
package agentd

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestCacheID8AndLeaves(t *testing.T) {
	data := "/data"
	id := "137a7dc9-df89-4c1c-891e-ebe106c68b37"
	if got, want := cacheID8(id), "137a7dc9"; got != want {
		t.Fatalf("cacheID8 = %q want %q", got, want)
	}
	if got, want := cacheID8("T1"), "T1"; got != want {
		t.Fatalf("short cacheID8 = %q want %q", got, want)
	}
	if got, want := cacheActiveLeaf(data, id), executor.TaskTmpDir(data, id); got != want {
		t.Fatalf("active = %q want %q", got, want)
	}
	if got, want := cacheLegacyLeaf(data, id), filepath.Join(data, "tasks", id, "tmp"); got != want {
		t.Fatalf("legacy = %q want %q", got, want)
	}
	if got, want := cacheTmpRoot(data), filepath.Join(data, "tmp"); got != want {
		t.Fatalf("root = %q want %q", got, want)
	}
}

func TestCacheTmpRootGuard(t *testing.T) {
	data := "/opt/handoff"
	if !isCacheTmpRoot(data, cacheActiveLeaf(data, "")) {
		t.Fatal("空 taskID 的活动叶子必须判为 tmp 根")
	}
	if !isCacheTmpRoot(data, filepath.Join(data, "tmp", ".")) {
		t.Fatal("Clean 后的 tmp/. 必须判为 tmp 根")
	}
	dotdot := cacheLegacyLeaf(data, "..")
	if !isCacheTmpRoot(data, dotdot) {
		t.Fatalf("taskID=.. 的遗留叶子 %q 必须判为 tmp 根", dotdot)
	}
	id := "abcd1234-xxxx"
	plans := planTaskCacheLeaves(data, "", nil)
	if len(plans) == 0 || !plans[0].Skip || plans[0].Note == "" {
		t.Fatalf("空 ID 必须 skip 并带原因，实得 %+v", plans)
	}
	for _, p := range planTaskCacheLeaves(data, id, nil) {
		if isCacheTmpRoot(data, p.Path) && !p.Skip {
			t.Fatalf("根路径不得进入可删计划：%+v", p)
		}
	}
}

func TestActiveLeafOccupied(t *testing.T) {
	self := "deadbeef-0000-4000-8000-aaaaaaaaaaaa"
	otherRun := proto.Task{ID: "deadbeef-0000-4000-8000-bbbbbbbbbbbb", State: proto.TaskStateRunning}
	otherDone := proto.Task{ID: "deadbeef-0000-4000-8000-cccccccccccc", State: proto.TaskStateCompleted}
	otherReview := proto.Task{ID: "deadbeef-0000-4000-8000-dddddddddddd", State: proto.TaskStateWaitingReview}
	unrelated := proto.Task{ID: "cafebabe-0000-4000-8000-eeeeeeeeeeee", State: proto.TaskStateRunning}
	selfRow := proto.Task{ID: self, State: proto.TaskStateCompleted}

	if activeLeafOccupied([]proto.Task{selfRow, otherDone, unrelated}, self) {
		t.Fatal("终态同号与无关短号不得占用")
	}
	if !activeLeafOccupied([]proto.Task{selfRow, otherRun}, self) {
		t.Fatal("其他 running 同 id8 必须占用")
	}
	if !activeLeafOccupied([]proto.Task{selfRow, otherReview}, self) {
		t.Fatal("其他 waiting_review 同 id8 必须占用（非终态）")
	}
	if activeLeafOccupied([]proto.Task{selfRow}, self) {
		t.Fatal("自己不得算占用者")
	}
}

func TestSumRegularFileBytesIgnoresDirSymlinkAndNonRegular(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("abcd"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "a.txt"), filepath.Join(root, "linkfile")); err != nil {
		t.Fatal(err)
	}
	n, err := sumRegularFileBytes(root)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("只计普通文件 a.txt 的 4 字节，不得跟随 symlink，实得 %d", n)
	}
	missing, err := sumRegularFileBytes(filepath.Join(root, "nope"))
	if err != nil || missing != 0 {
		t.Fatalf("缺失目录应 0,nil，实得 %d %v", missing, err)
	}
}

func writeCacheLeaves(t *testing.T, dataDir, id string) (active, legacy, taskDir string) {
	t.Helper()
	active = executor.TaskTmpDir(dataDir, id)
	if err := os.MkdirAll(filepath.Join(active, "gocache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(active, "gocache", "obj"), []byte("cache-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	taskDir = filepath.Join(dataDir, "tasks", id)
	legacy = filepath.Join(taskDir, "tmp")
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
	return active, legacy, taskDir
}

func seedTaskWithCache(t *testing.T, m *Manager, id string, state proto.TaskState) (active, legacy, taskDir string) {
	t.Helper()
	now := time.Now().UTC()
	mustCreateTask(t, m.st, &proto.Task{
		ID: id, Target: "local", Executor: "fake",
		State: state, CreatedAt: now, UpdatedAt: now,
	})
	return writeCacheLeaves(t, m.cfg.DataDir, id)
}

func assertGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s 应已删除: %v", path, err)
	}
}

func assertKeptFile(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s: %v", path, err)
	}
	if string(b) != want {
		t.Fatalf("%s = %q want %q", path, b, want)
	}
}

func TestDonePurgesBothCacheLeavesAndKeepsTaskDir(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	id := "11111111-0000-4000-8000-000000000001"
	active, legacy, taskDir := seedTaskWithCache(t, m, id, proto.TaskStateWaitingReview)
	mustDone(t, m, id, "")
	assertGone(t, active)
	assertGone(t, legacy)
	assertKeptFile(t, filepath.Join(taskDir, "render.log"), "render.log-keep")
	assertKeptFile(t, filepath.Join(taskDir, "frames.jsonl"), "frames.jsonl-keep")
	assertKeptFile(t, filepath.Join(taskDir, "proc.json"), "proc.json-keep")
	if _, err := os.Lstat(taskDir); err != nil {
		t.Fatalf("任务目录必须保留: %v", err)
	}
	cur, err := st.GetTask(id)
	if err != nil {
		t.Fatal(err)
	}
	if cur.State != proto.TaskStateCompleted {
		t.Fatalf("state=%s want completed", cur.State)
	}
}

func TestStopPurgesBothCacheLeaves(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	id := "22222222-0000-4000-8000-000000000002"
	active, legacy, taskDir := seedTaskWithCache(t, m, id, proto.TaskStateRunning)
	if _, err := m.Stop(context.Background(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	assertGone(t, active)
	assertGone(t, legacy)
	assertKeptFile(t, filepath.Join(taskDir, "render.log"), "render.log-keep")
	cur, _ := st.GetTask(id)
	if cur.State != proto.TaskStateFailed {
		t.Fatalf("state=%s want failed", cur.State)
	}
}

func TestDoneKeepsActiveLeafWhenOtherNonTerminalSharesID8(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	self := "deadbeef-0000-4000-8000-aaaaaaaaaaaa"
	other := "deadbeef-0000-4000-8000-bbbbbbbbbbbb"
	active, legacy, _ := seedTaskWithCache(t, m, self, proto.TaskStateWaitingReview)
	now := time.Now().UTC()
	mustCreateTask(t, m.st, &proto.Task{
		ID: other, Target: "local", Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now,
	})
	mustDone(t, m, self, "")
	if _, err := os.Lstat(active); err != nil {
		t.Fatalf("被占用的现役叶子必须保留: %v", err)
	}
	assertGone(t, legacy)
}

func TestDoneLegacyLeafIgnoresShortIDOccupancy(t *testing.T) {
	// 与上一则共用形状：遗留叶子按完整 id，必须删。已在 TestDoneKeepsActiveLeafWhenOtherNonTerminalSharesID8 钉死。
	// 本则再钉：占用者不存在时两条都删（对照）。
	m, _, _, _ := newTestManager(t)
	id := "feedfeed-0000-4000-8000-000000000009"
	active, legacy, _ := seedTaskWithCache(t, m, id, proto.TaskStateWaitingReview)
	mustDone(t, m, id, "")
	assertGone(t, active)
	assertGone(t, legacy)
}

func TestDoneOnRunningDoesNotPurgeCache(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	id := "33333333-0000-4000-8000-000000000003"
	active, legacy, _ := seedTaskWithCache(t, m, id, proto.TaskStateRunning)
	if err := m.Done(context.Background(), id, ""); err == nil {
		t.Fatal("running 走 Done 必须失败")
	}
	if _, err := os.Lstat(active); err != nil {
		t.Fatalf("非终态不得删现役叶子: %v", err)
	}
	if _, err := os.Lstat(legacy); err != nil {
		t.Fatalf("非终态不得删遗留叶子: %v", err)
	}
}

func TestDonePurgeFailureDoesNotBlockArchive(t *testing.T) {
	var buf bytes.Buffer
	m, st, _, _ := newTestManager(t)
	m.log = slog.New(slog.NewTextHandler(&buf, nil))
	id := "44444444-0000-4000-8000-000000000004"
	active, _, _ := seedTaskWithCache(t, m, id, proto.TaskStateWaitingReview)
	m.removeCacheLeafFn = func(path string) error {
		return errors.New("injected-remove-fail")
	}
	mustDone(t, m, id, "")
	cur, _ := st.GetTask(id)
	if cur.State != proto.TaskStateCompleted {
		t.Fatalf("删除失败不得阻断归档，state=%s", cur.State)
	}
	logs := buf.String()
	for _, want := range []string{id, active, "injected-remove-fail"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("失败日志缺少 %q，实得 %s", want, logs)
		}
	}
}

func TestCompensatePurgesCacheWhenWorktreeRemoveFails(t *testing.T) {
	repo := initTestRepo(t)
	gitT(t, repo, "branch", "e2e/stuck-cache")
	tip := gitOut(t, repo, "rev-parse", "refs/heads/e2e/stuck-cache")
	m := compensateOnlyManager(t)
	id := "2c58bbb7-0000-0000-0000-000000000000"
	active, legacy, taskDir := seedTaskWithCache(t, m, id, proto.TaskStateFailed)
	m.compensateWorkspace(context.Background(), id, repo, Workspace{
		Branch: "e2e/stuck-cache", WorkDir: filepath.Join(t.TempDir(), "not-a-worktree"),
		Managed: true, NewBranchTip: tip,
	})
	assertGone(t, active)
	assertGone(t, legacy)
	assertKeptFile(t, filepath.Join(taskDir, "render.log"), "render.log-keep")
	if !branchExists(t, repo, "e2e/stuck-cache") {
		t.Fatal("工作树删除失败时分支必须保留（回归 TestCompensateKeepsBranchWhenWorktreeRemoveFails）")
	}
}

func TestPurgeRefusesTmpRootEvenIfCalledDirectly(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	root := cacheTmpRoot(m.cfg.DataDir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "keep-me")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.purgeTaskCache("")
	if _, err := os.Lstat(marker); err != nil {
		t.Fatalf("tmp 根内文件必须幸存: %v", err)
	}
}
