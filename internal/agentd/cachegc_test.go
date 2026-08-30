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
	"os"
	"path/filepath"
	"testing"

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
