// 镜像测试：发现式订阅与终态收手。
package agentd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/targetclient"
	"github.com/google/uuid"
)

type mirrorRequestCounts struct {
	list atomic.Int64
	ws   atomic.Int64
}

func newMirrorHTTPEnv(t *testing.T) *testAgentdEnv {
	t.Helper()
	cfg := &config.Config{Token: testToken, DataDir: t.TempDir()}
	st := newTestStore(t)
	srv := NewServer(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &testAgentdEnv{srv: srv, ts: ts, st: st, token: testToken}
}

func countedMirrorTarget(t *testing.T, env *testAgentdEnv, counts *mirrorRequestCounts) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tasks" {
			counts.list.Add(1)
		}
		if r.URL.Path == "/ws/events" || strings.HasSuffix(r.URL.Path, "/events") {
			counts.ws.Add(1)
		}
		env.srv.Handler().ServeHTTP(w, r)
	}))
}

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
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool := targetclient.NewPool(func() *config.Config { return cfg }, log)
	defer pool.Close()
	m := NewMirror(pool, localSt, hub, nil, log)

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
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool := targetclient.NewPool(func() *config.Config { return cfg }, log)
	defer pool.Close()
	m := NewMirror(pool, localSt, hub, nil, log)

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

func TestMirrorDiscoverOnceSkipsSelfTarget(t *testing.T) {
	local := newMirrorHTTPEnv(t)
	remote := newMirrorHTTPEnv(t)
	now := time.Now().UTC()
	mustCreateTask(t, local.st, &proto.Task{ID: uuid.NewString(), Name: "本机活",
		State: proto.TaskStateRunning, RepoPath: "/local/handoff", CreatedAt: now, UpdatedAt: now})
	remoteTaskID := uuid.NewString()
	mustCreateTask(t, remote.st, &proto.Task{ID: remoteTaskID, Name: "远端活",
		State: proto.TaskStateRunning, RepoPath: "/remote/handoff", CreatedAt: now, UpdatedAt: now})

	var localCounts, remoteCounts mirrorRequestCounts
	localTarget := countedMirrorTarget(t, local, &localCounts)
	remoteTarget := countedMirrorTarget(t, remote, &remoteCounts)
	t.Cleanup(localTarget.Close)
	t.Cleanup(remoteTarget.Close)

	localStore := newTestStore(t)
	hub := NewHub()
	cfg := &config.Config{Token: testToken, Targets: map[string]config.Target{
		"local":  {Addr: localTarget.URL, Token: testToken},
		"devbox": {Addr: remoteTarget.URL, Token: testToken},
	}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool := targetclient.NewPool(func() *config.Config { return cfg }, log)
	defer pool.Close()
	m := NewMirror(pool, localStore, hub, func(name string) bool { return name == "local" }, log)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.discoverOnce(ctx)

	if got := localCounts.list.Load(); got != 0 {
		t.Fatalf("本机 target ListTasks 请求数 = %d, want 0", got)
	}
	if got := localCounts.ws.Load(); got != 0 {
		t.Fatalf("本机 target WS 请求数 = %d, want 0", got)
	}
	if got := remoteCounts.list.Load(); got != 1 {
		t.Fatalf("devbox ListTasks 请求数 = %d, want 1", got)
	}

	deadline := time.Now().Add(time.Second)
	for remoteCounts.ws.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := remoteCounts.ws.Load(); got == 0 {
		t.Fatal("devbox 活跃任务未启动既有 WS 订阅")
	}
	views, err := localStore.ListMirrorTasks()
	if err != nil {
		t.Fatalf("读取 mirror_tasks: %v", err)
	}
	if len(views) != 1 || views[0].Target != "devbox" || views[0].Task.ID != remoteTaskID {
		t.Fatalf("mirror_tasks = %+v, want only devbox remote task", views)
	}
	if names := m.machineNames(); len(names) != 2 || names[0] != "devbox" || names[1] != "local" {
		t.Fatalf("machineNames = %v, want both configured names", names)
	}
	m.Stop()
}
