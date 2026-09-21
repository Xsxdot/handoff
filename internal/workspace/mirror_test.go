// 镜像测试：发现式订阅与终态收手。
//
// B233.19 自 agentd/mirror_test.go 随实现迁入：远端 HTTP/WS 栈换成 RemoteTaskSource
// 的内存替身，断言逐条保留（HTTP 路由计数换成替身的调用计数；真实链路由 gateway
// 侧 mirrorws 测试、fanout 集成测试与 cmd 装配覆盖）。
package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/google/uuid"
)

// newMirrorTestStore 打开一个临时目录里的真 store（mirror_tasks/mirror_events 落点）。
func newMirrorTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("open mirror test store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// nopEventPublisher 是 EventPublisher 的空实现：镜像事件不落库外的断言点。
type nopEventPublisher struct{}

func (nopEventPublisher) Publish(proto.Event) {}

// fakeMirrorClient 是单台 target 的内存替身：ListTasks 给快照；
// StreamEventsOnce 发完 events 后挂住直到 ctx 取消（模拟常驻上游流）。
type fakeMirrorClient struct {
	tasks     []proto.TaskView
	events    []proto.Event
	listCalls atomic.Int64
	streamOK  atomic.Bool // StreamEventsOnce 已被调用（含挂住中）
}

func (c *fakeMirrorClient) ListTasks(ctx context.Context) ([]proto.TaskView, error) {
	c.listCalls.Add(1)
	return c.tasks, nil
}

func (c *fakeMirrorClient) StreamEventsOnce(ctx context.Context, taskID string, fromSeq int64,
	onEvent func(proto.Event) error) error {
	c.streamOK.Store(true)
	for _, ev := range c.events {
		if ev.Seq <= fromSeq {
			continue
		}
		if err := onEvent(ev); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

// fakeMirrorSource 是 RemoteTaskSource 的内存替身。
type fakeMirrorSource struct {
	mu      sync.Mutex
	names   []string
	clients map[string]*fakeMirrorClient
}

func (s *fakeMirrorSource) Names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.names...)
}

func (s *fakeMirrorSource) For(name string) (RemoteTaskClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.clients[name]
	if !ok {
		return nil, errors.New("未知机器 " + name)
	}
	return c, nil
}

// TestMirrorDiscoverOnceSubscribesActiveTasks 断言一轮发现即：
// 活跃任务进 mirror_tasks（带 target），其事件被复制进 mirror_events。
func TestMirrorDiscoverOnceSubscribesActiveTasks(t *testing.T) {
	now := time.Now().UTC()
	taskID := uuid.NewString()

	localSt := newMirrorTestStore(t)
	devbox := &fakeMirrorClient{
		tasks: []proto.TaskView{{Task: proto.Task{ID: taskID, Name: "远端活",
			State: proto.TaskStateRunning, RepoPath: "/remote/handoff",
			CreatedAt: now, UpdatedAt: now}}},
		events: []proto.Event{{Seq: 1, TaskID: taskID, Type: proto.EventTypeQuestion,
			Payload: json.RawMessage(`{"text":"继续吗"}`), CreatedAt: now}},
	}
	source := &fakeMirrorSource{names: []string{"devbox"}, clients: map[string]*fakeMirrorClient{"devbox": devbox}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := NewMirror(source, localSt, nopEventPublisher{}, nil, log)

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
	now := time.Now().UTC()
	taskID := uuid.NewString()

	localSt := newMirrorTestStore(t)
	devbox := &fakeMirrorClient{
		tasks: []proto.TaskView{{Task: proto.Task{ID: taskID, Name: "已完结",
			State: proto.TaskStateCompleted, RepoPath: "/remote/handoff",
			CreatedAt: now, UpdatedAt: now}}},
	}
	source := &fakeMirrorSource{names: []string{"devbox"}, clients: map[string]*fakeMirrorClient{"devbox": devbox}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := NewMirror(source, localSt, nopEventPublisher{}, nil, log)

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
	now := time.Now().UTC()
	remoteTaskID := uuid.NewString()

	remote := &fakeMirrorClient{
		tasks: []proto.TaskView{{Task: proto.Task{ID: remoteTaskID, Name: "远端活",
			State: proto.TaskStateRunning, RepoPath: "/remote/handoff",
			CreatedAt: now, UpdatedAt: now}}},
	}
	local := &fakeMirrorClient{}
	source := &fakeMirrorSource{names: []string{"devbox", "local"},
		clients: map[string]*fakeMirrorClient{"devbox": remote, "local": local}}
	localStore := newMirrorTestStore(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := NewMirror(source, localStore, nopEventPublisher{}, func(name string) bool { return name == "local" }, log)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.discoverOnce(ctx)

	if got := local.listCalls.Load(); got != 0 {
		t.Fatalf("本机 target ListTasks 请求数 = %d, want 0", got)
	}
	if local.streamOK.Load() {
		t.Fatal("本机 target WS 请求数 = 0, want 0（不得自订本机）")
	}
	if got := remote.listCalls.Load(); got != 1 {
		t.Fatalf("devbox ListTasks 请求数 = %d, want 1", got)
	}

	deadline := time.Now().Add(time.Second)
	for !remote.streamOK.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !remote.streamOK.Load() {
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
