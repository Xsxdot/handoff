// machineauthority 文件变化流测试。
//
// 职责：
//   - 用真实 fsnotify 锁定 create/modify/remove 的 relative path 事件
//   - 锁定 per-workspace seq、after 重放与 journal cursor 过期
//   - 锁定慢订阅者有界断开和 Workspace unavailable 立即终止
//
// 边界：
//   - watcher 事件只做 UI invalidation，不读取或传送文件内容
package machineauthority

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/workspaceapi"
)

func TestFileStreamCoalescesExternalChangesAndReplaysAfterSeq(t *testing.T) {
	dir := t.TempDir()
	authority := NewResourceAuthority(slog.New(slog.NewTextHandler(io.Discard, nil)))
	authority.fileStream.coalesceWindow = 20 * time.Millisecond
	ws := workspaceapi.WorkspaceRef{WorkspaceID: "ws-1", MachineID: "m-1", RootPath: dir}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := authority.SubscribeFiles(ctx, ws, 0)
	if err != nil {
		t.Fatalf("SubscribeFiles: %v", err)
	}
	defer sub.Cancel()

	file := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(file, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	created := waitFileEvent(t, sub.Events, "note.txt", workspaceapi.FileEventCreate)
	if created.Kind != workspaceapi.FileEventCreate || created.Seq != 1 {
		t.Fatalf("created = %+v", created)
	}
	if err := os.WriteFile(file, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	modified := waitFileEvent(t, sub.Events, "note.txt", workspaceapi.FileEventModify)
	if modified.Kind != workspaceapi.FileEventModify || modified.Seq <= created.Seq {
		t.Fatalf("modified = %+v", modified)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	removed := waitFileEvent(t, sub.Events, "note.txt", workspaceapi.FileEventRemove)
	if removed.Kind != workspaceapi.FileEventRemove || removed.Seq <= modified.Seq {
		t.Fatalf("removed = %+v", removed)
	}

	replay, err := authority.SubscribeFiles(context.Background(), ws, created.Seq)
	if err != nil {
		t.Fatalf("replay subscribe: %v", err)
	}
	defer replay.Cancel()
	if len(replay.Replay) != 2 || replay.Replay[0].Seq != modified.Seq || replay.Replay[1].Seq != removed.Seq {
		t.Fatalf("replay = %+v", replay.Replay)
	}
}

func TestFileStreamDisconnectsSlowSubscriberAndUnavailableWorkspace(t *testing.T) {
	dir := t.TempDir()
	authority := NewResourceAuthority(slog.New(slog.NewTextHandler(io.Discard, nil)))
	authority.fileStream.subscriberBuffer = 1
	authority.fileStream.journalLimit = 2
	ws := workspaceapi.WorkspaceRef{WorkspaceID: "ws", MachineID: "m", RootPath: dir}
	slow, err := authority.SubscribeFiles(context.Background(), ws, 0)
	if err != nil {
		t.Fatal(err)
	}
	authority.fileStream.publish("ws", workspaceapi.FileEventCreate, "a", time.Now())
	authority.fileStream.publish("ws", workspaceapi.FileEventModify, "a", time.Now())
	select {
	case err := <-slow.Done:
		if !errors.Is(err, ErrFileStreamOverflow) {
			t.Fatalf("slow done = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("慢订阅者未被断开")
	}

	active, err := authority.SubscribeFiles(context.Background(), ws, 1)
	if err != nil {
		t.Fatal(err)
	}
	authority.SetWorkspaceAvailable("ws", false)
	select {
	case err := <-active.Done:
		assertResourceCode(t, err, workspaceapi.ErrorUnavailable)
	case <-time.After(time.Second):
		t.Fatal("workspace unavailable 未终止订阅")
	}
	_, err = authority.SubscribeFiles(context.Background(), ws, 2)
	assertResourceCode(t, err, workspaceapi.ErrorUnavailable)

	// journal 只保留最后两条；比 earliest-1 更旧的 cursor 必须明确过期。
	authority.SetWorkspaceAvailable("ws", true)
	restarted, err := authority.SubscribeFiles(context.Background(), ws, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Cancel()
	if err := os.WriteFile(filepath.Join(dir, "restarted.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = waitFileEvent(t, restarted.Events, "restarted.txt", workspaceapi.FileEventCreate)
	authority.fileStream.publish("ws", workspaceapi.FileEventCreate, "b", time.Now())
	authority.fileStream.publish("ws", workspaceapi.FileEventCreate, "c", time.Now())
	authority.fileStream.publish("ws", workspaceapi.FileEventCreate, "d", time.Now())
	_, err = authority.SubscribeFiles(context.Background(), ws, 1)
	assertResourceCode(t, err, workspaceapi.ErrorCursorExpired)
}

func TestGitStatusInvalidationIsExplicitFileStreamEvent(t *testing.T) {
	dir := t.TempDir()
	authority := NewResourceAuthority(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := workspaceapi.WorkspaceRef{WorkspaceID: "ws", MachineID: "m", RootPath: dir}
	sub, err := authority.SubscribeFiles(context.Background(), ws, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Cancel()
	authority.InvalidateGitStatus("ws")
	event := waitFileEvent(t, sub.Events, "", workspaceapi.FileEventGitStatus)
	if event.Seq == 0 {
		t.Fatalf("git status invalidation = %+v", event)
	}
}

func waitFileEvent(t *testing.T, events <-chan workspaceapi.FileEvent, path string, kind workspaceapi.FileEventKind) workspaceapi.FileEvent {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("文件事件流提前关闭")
			}
			if event.Path == path && event.Kind == kind {
				return event
			}
		case <-deadline:
			t.Fatalf("等待文件事件 %s 超时", path)
		}
	}
}
