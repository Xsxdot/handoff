package agentd

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/executor/turn"
	"github.com/xushixin/handoff/internal/proto"
)

func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestEventFrameHookWritesRefFrame(t *testing.T) {
	dataDir := t.TempDir()
	taskID := "3704f368-8109-4943-b6c2-97e7943f577e"
	if err := os.MkdirAll(filepath.Join(dataDir, "tasks", taskID), 0o755); err != nil {
		t.Fatalf("建任务目录: %v", err)
	}

	hook := eventFrameHook(dataDir, testLogger(t))
	hook(proto.Event{Seq: 88, TaskID: taskID, Type: proto.EventTypePermissionRequest})

	f, err := os.Open(filepath.Join(dataDir, "tasks", taskID, turn.FramesFileName))
	if err != nil {
		t.Fatalf("打开 frames.jsonl: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		t.Fatal("应至少写出一帧")
	}
	var fr proto.Frame
	if err := json.Unmarshal(sc.Bytes(), &fr); err != nil {
		t.Fatalf("解析帧: %v", err)
	}
	if fr.Type != proto.FrameEvent {
		t.Errorf("类型应为 event，实得 %s", fr.Type)
	}
	if fr.RefSeq != 88 {
		t.Errorf("ref_seq 应为 88，实得 %d", fr.RefSeq)
	}
	if string(fr.Event) != string(proto.EventTypePermissionRequest) {
		t.Errorf("event 名应为 permission_request，实得 %q", fr.Event)
	}
}

// 任务目录不存在（事件属于一个已清理的任务）不该 panic 也不该报错——
// 钩子是尽力而为的可见性副作用。
func TestEventFrameHookToleratesMissingTaskDir(t *testing.T) {
	hook := eventFrameHook(t.TempDir(), testLogger(t))
	hook(proto.Event{Seq: 1, TaskID: "no-such-task", Type: proto.EventTypeProgress})
	// 不 panic 即通过
}
