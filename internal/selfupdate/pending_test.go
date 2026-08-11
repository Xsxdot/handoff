// pending 持久化的测试（托管判据已随 IsManaged 迁到 managed_test.go）。
package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 往返：存进去什么读出来还是什么。
func TestPendingRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := &Pending{Version: "v0.2.0", Path: "/opt/bin/.handoff.new-v0.2.0", DownloadedAt: time.Unix(1760000000, 0).UTC()}
	if err := SavePending(dir, want); err != nil {
		t.Fatalf("SavePending: %v", err)
	}
	got, err := LoadPending(dir)
	if err != nil {
		t.Fatalf("LoadPending: %v", err)
	}
	if got == nil || got.Version != want.Version || got.Path != want.Path || !got.DownloadedAt.Equal(want.DownloadedAt) {
		t.Fatalf("往返不一致: %+v vs %+v", got, want)
	}
}

// 文件不存在是**正常状态**（绝大多数时候都没有待命更新），必须返回 (nil, nil)。
//
// why：把它当错误，更新循环每轮都会打一条 Error，日志里全是噪音，
// 真正的错误反而被淹掉。
func TestLoadPendingMissingIsNotError(t *testing.T) {
	got, err := LoadPending(t.TempDir())
	if err != nil {
		t.Fatalf("缺文件不该报错: %v", err)
	}
	if got != nil {
		t.Fatalf("缺文件应返回 nil，得到 %+v", got)
	}
}

// 坏 JSON 要报错而不是静默当成没有——静默会让「更新卡住」永远查不出原因。
func TestLoadPendingCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(PendingPath(dir)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(PendingPath(dir), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPending(dir); err == nil {
		t.Fatal("坏 JSON 应报错")
	}
}

// ClearPending 幂等：没有文件时也返回 nil。
func TestClearPendingIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := ClearPending(dir); err != nil {
		t.Fatalf("无文件时 Clear 应成功: %v", err)
	}
	if err := SavePending(dir, &Pending{Version: "v1"}); err != nil {
		t.Fatal(err)
	}
	if err := ClearPending(dir); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	got, _ := LoadPending(dir)
	if got != nil {
		t.Fatal("Clear 之后应读不到")
	}
}
