// pullstate_test.go —— PullTracker 的并发锁与状态流转测试（B233.26 随 pullstate.go
// 自 gateway 归域编排包；原在 agentd/update_test.go 包内直造，现走同包未导出面）。
package orchestration

import (
	"errors"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

// 并发锁：一个自拉在跑时，第二个请求 409 + pull_in_progress。
// 没有这道锁，两个 goroutine 会往同一个临时文件路径写，互相截断出一个坏二进制。
func TestPullTrackerRejectsConcurrent(t *testing.T) {
	p := NewPullTracker()
	if !p.Begin("v1.0.0") {
		t.Fatal("首次 Begin 应成功")
	}
	if p.Begin("v1.0.1") {
		t.Fatal("已有自拉在跑时 Begin 应失败")
	}
	p.Fail(errors.New("boom"))
	if !p.Begin("v1.0.2") {
		t.Fatal("失败释放后 Begin 应能再次成功")
	}
}

// 没跑过自拉时 Snapshot 返回 nil：status 不该显示一个编出来的空状态。
func TestPullTrackerSnapshotNilWhenIdle(t *testing.T) {
	if got := NewPullTracker().Snapshot(); got != nil {
		t.Fatalf("空闲时应返回 nil，实得 %+v", got)
	}
}

// 失败后 Snapshot 必须留住阶段与错误原文——进程不重启，这正是要查它的场合。
func TestPullTrackerKeepsFailure(t *testing.T) {
	p := NewPullTracker()
	p.Begin("v1.0.0")
	p.Stage(proto.PullStageDownloading)
	p.Fail(errors.New("proxyconnect tcp: connection refused"))
	got := p.Snapshot()
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
