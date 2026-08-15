// 镜像测试：发现式订阅与终态收手。
package agentd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestMirrorDiscoverOnceSubscribesActiveTasks 断言一轮发现即：
// 活跃任务进 mirror_tasks（带 target），其事件被复制进 mirror_events。
func TestMirrorDiscoverOnceSubscribesActiveTasks(t *testing.T) {
	remote := newTestAgentdEnv(t)
	now := time.Now().UTC()
	taskID := uuid.NewString()
	mustCreateTask(t, remote.st, &proto.Task{ID: taskID, Name: "远端活",
		State: proto.TaskStateRunning, RepoPath: "/remote/handoff",
		CreatedAt: now, UpdatedAt: now})
	if _, err := remote.st.AppendEvent(taskID, proto.EventTypeQuestion,
		json.RawMessage(`{"text":"继续吗"}`)); err != nil {
		t.Fatalf("远端落事件: %v", err)
	}

	localSt := newTestStore(t)
	hub := NewHub()
	cfg := &config.Config{Token: testToken,
		Targets: map[string]config.Target{"devbox": {Addr: remote.ts.URL, Token: testToken}}}
	m := NewMirror(cfg, localSt, hub, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m.discoverOnce(ctx)

	// 快照：一轮发现之后就该有
	list, err := localSt.ListMirrorTasks()
	if err != nil || len(list) != 1 || list[0].Target != "devbox" {
		t.Fatalf("镜像任务不对：%+v err=%v", list, err)
	}
	// 事件：订阅是异步的，等到水位推上去为止（最长 5s）
	deadline := time.Now().Add(5 * time.Second)
	for {
		wm, _ := localSt.MirrorWatermark(taskID)
		if wm > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("5s 内没有镜像到任何事件")
		}
		time.Sleep(50 * time.Millisecond)
	}
	evs, err := localSt.MirrorEventsFrom(taskID, 0, 10)
	if err != nil || len(evs) == 0 || evs[0].Type != proto.EventTypeQuestion {
		t.Fatalf("镜像事件不对：%+v err=%v", evs, err)
	}
	m.Stop() // 收掉全部订阅，别把 goroutine 漏给下一个测试
}

// TestMirrorDropsTerminalTasks 断言：终态任务不再订阅（快照仍在，供审阅历史）。
func TestMirrorDropsTerminalTasks(t *testing.T) {
	remote := newTestAgentdEnv(t)
	now := time.Now().UTC()
	taskID := uuid.NewString()
	mustCreateTask(t, remote.st, &proto.Task{ID: taskID, Name: "已完结",
		State: proto.TaskStateCompleted, RepoPath: "/remote/handoff",
		CreatedAt: now, UpdatedAt: now})

	localSt := newTestStore(t)
	hub := NewHub()
	cfg := &config.Config{Token: testToken,
		Targets: map[string]config.Target{"devbox": {Addr: remote.ts.URL, Token: testToken}}}
	m := NewMirror(cfg, localSt, hub, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m.discoverOnce(ctx)

	// 快照仍要留（历史审阅），但不订阅
	list, err := localSt.ListMirrorTasks()
	if err != nil || len(list) != 1 {
		t.Fatalf("镜像任务不对：%+v err=%v", list, err)
	}
	if m.isSubscribed(taskID) {
		t.Error("终态任务不该被订阅")
	}
	m.Stop()
}
