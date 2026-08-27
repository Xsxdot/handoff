// 镜像子系统测试：事件源用 fake 注入，验证 lease 独占、幂等重放、
// 挂账对账、终态退订。真网络路径归真机判据，不在单测里装。
package ledgermirror

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

func testLedger(t *testing.T) *ledger.Store {
	t.Helper()
	s, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.PutWorkflow("bug", ledger.WorkflowDef{States: []string{ledger.StatusTodo, ledger.StatusDoing, ledger.StatusDone}}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMirrorFlowsLinkedTaskEvents(t *testing.T) {
	s := testLedger(t)
	c, _ := s.CreateCard(ledger.NewCard{Title: "卡", Project: "p", Workflow: "bug", Actor: "t"})
	_ = s.LinkTask(c.ID, "mac-02", "T1", "implement", "t")

	var calls atomic.Int64
	fake := func(ctx context.Context, _ *client.Client, taskID string, fromSeq int64,
		onEvent func(proto.Event) error) error {
		calls.Add(1)
		for _, e := range []proto.Event{
			{Seq: 1, TaskID: taskID, Type: proto.EventTypeProgress, Payload: []byte(`{}`)},
			{Seq: 2, TaskID: taskID, Type: "message", Payload: []byte(`{"text":"hi"}`)},
			{Seq: 3, TaskID: taskID, Type: proto.EventTypePermissionAutoAllow,
				Payload: []byte(`{"permission_id":"perm-1","rule":"safe-command"}`)},
			{Seq: 4, TaskID: taskID, Type: proto.EventTypeCompleted, Payload: []byte(`{}`)},
		} {
			if e.Seq <= fromSeq {
				continue
			}
			if err := onEvent(e); err != nil {
				return err
			}
		}
		<-ctx.Done()
		return ctx.Err()
	}
	m := New(s, machinesWith(t, "mac-02"), Options{Holder: "test-coord", Tick: 50 * time.Millisecond,
		LeaseTTL: time.Second, Source: fake})
	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)
	t.Cleanup(func() { cancel(); m.Stop() })

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		evs, _ := s.EventsFromAsc([]string{c.ID}, 0, 100)
		var mirrored int
		for _, e := range evs {
			if e.Type == ledger.EvTaskMirrored {
				mirrored++
			}
		}
		if mirrored == 3 {
			var found bool
			for _, e := range evs {
				if e.Type != ledger.EvTaskMirrored || string(e.Payload) == "" {
					continue
				}
				var payload struct {
					TaskType string `json:"task_type"`
					Payload  struct {
						PermissionID string `json:"permission_id"`
					} `json:"payload"`
				}
				if err := json.Unmarshal(e.Payload, &payload); err == nil &&
					payload.TaskType == string(proto.EventTypePermissionAutoAllow) &&
					payload.Payload.PermissionID == "perm-1" {
					found = true
				}
			}
			if !found {
				t.Fatalf("镜像缺少 permission_auto_allow 的真实 payload: %#v", evs)
			}
			rows, _ := s.MirrorHealth()
			if len(rows) == 1 && rows[0].Target == "mac-02" {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("镜像未按期落账（应恰 2 条：progress 过滤、幂等不重）")
}

func TestMirrorLeaseExclusive(t *testing.T) {
	s := testLedger(t)
	blockSrc := func(ctx context.Context, _ *client.Client, _ string, _ int64, _ func(proto.Event) error) error {
		<-ctx.Done()
		return ctx.Err()
	}
	machines := newFakeMachines()
	a := New(s, machines, Options{Holder: "A", Tick: 50 * time.Millisecond, LeaseTTL: time.Second, Source: blockSrc})
	b := New(s, machines, Options{Holder: "B", Tick: 50 * time.Millisecond, LeaseTTL: time.Second, Source: blockSrc})
	ctx, cancel := context.WithCancel(context.Background())
	go a.Run(ctx)
	go b.Run(ctx)
	t.Cleanup(func() { cancel(); a.Stop(); b.Stop() })
	time.Sleep(300 * time.Millisecond)
	if a.Holding() == b.Holding() {
		t.Fatalf("lease 应恰一人持有: A=%v B=%v", a.Holding(), b.Holding())
	}
}

func TestMirrorNoTouchWhenDisconnected(t *testing.T) {
	s := testLedger(t)
	c, _ := s.CreateCard(ledger.NewCard{Title: "卡", Project: "p", Workflow: "bug", Actor: "t"})
	_ = s.LinkTask(c.ID, "dead-box", "T9", "implement", "t")
	failSrc := func(ctx context.Context, _ *client.Client, _ string, _ int64, _ func(proto.Event) error) error {
		return fmt.Errorf("dial refused")
	}
	m := New(s, machinesWith(t, "dead-box", "idle-box"), Options{Holder: "test", Tick: 50 * time.Millisecond, LeaseTTL: time.Second, Source: failSrc})
	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)
	t.Cleanup(func() { cancel(); m.Stop() })

	deadAt := func() (time.Time, bool) {
		rows, _ := s.MirrorHealth()
		for _, r := range rows {
			if r.Target == "dead-box" {
				return r.UpdatedAt, true
			}
		}
		return time.Time{}, false
	}
	time.Sleep(300 * time.Millisecond)
	t1, ok1 := deadAt()
	time.Sleep(300 * time.Millisecond)
	t2, ok2 := deadAt()
	if ok2 && (!ok1 || t2.After(t1)) {
		t.Fatalf("断链 target 的健康心跳仍在被刷新: %v -> %v", t1, t2)
	}
	rows, _ := s.MirrorHealth()
	idleOK := false
	for _, r := range rows {
		if r.Target == "idle-box" {
			idleOK = true
		}
	}
	if !idleOK {
		t.Fatal("无挂账 task 的 target 应照常空 touch")
	}
}

func TestMirrorTouchesWhenAllLinkedTasksArchived(t *testing.T) {
	s := testLedger(t)
	c, _ := s.CreateCard(ledger.NewCard{Title: "卡", Project: "p", Workflow: "bug", Actor: "t"})
	_ = s.LinkTask(c.ID, "mac-02", "T1", "implement", "t")
	src := func(ctx context.Context, _ *client.Client, taskID string, fromSeq int64,
		onEvent func(proto.Event) error) error {
		ev := proto.Event{Seq: 1, TaskID: taskID, Type: proto.EventTypeArchived, Payload: []byte(`{}`)}
		if ev.Seq > fromSeq {
			if err := onEvent(ev); err != nil {
				return err
			}
		}
		<-ctx.Done()
		return ctx.Err()
	}
	m := New(s, machinesWith(t, "mac-02"), Options{Holder: "test", Tick: 50 * time.Millisecond,
		LeaseTTL: time.Second, Source: src})
	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)
	t.Cleanup(func() { cancel(); m.Stop() })

	macAt := func() (time.Time, bool) {
		rows, _ := s.MirrorHealth()
		for _, r := range rows {
			if r.Target == "mac-02" {
				return r.UpdatedAt, true
			}
		}
		return time.Time{}, false
	}
	deadline := time.Now().Add(5 * time.Second)
	var t1 time.Time
	for time.Now().Before(deadline) {
		if at, ok := macAt(); ok {
			t1 = at
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if t1.IsZero() {
		t.Fatal("归档后应有健康行")
	}
	time.Sleep(200 * time.Millisecond)
	t2, ok := macAt()
	if !ok || !t2.After(t1) {
		t.Fatalf("全归档后应继续空 touch: %v -> %v", t1, t2)
	}
}

func TestMirrorTouchesLeftoverIdleCursor(t *testing.T) {
	s := testLedger(t)
	if err := s.TouchMirrorHealth("mac-02", 99); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.MirrorHealth()
	var t1 time.Time
	for _, r := range rows {
		if r.Target == "mac-02" {
			t1 = r.UpdatedAt
		}
	}
	if t1.IsZero() {
		t.Fatal("应已有 mac-02 cursor")
	}
	blockSrc := func(ctx context.Context, _ *client.Client, _ string, _ int64, _ func(proto.Event) error) error {
		<-ctx.Done()
		return ctx.Err()
	}
	m := New(s, machinesWith(t, "idle-box"), Options{Holder: "test", Tick: 50 * time.Millisecond,
		LeaseTTL: time.Second, Source: blockSrc})
	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)
	t.Cleanup(func() { cancel(); m.Stop() })

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		cur, _ := s.MirrorHealth()
		for _, r := range cur {
			if r.Target == "mac-02" && r.UpdatedAt.After(t1) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("清单外、无在飞挂账的残留 cursor 应被空 touch")
}

func TestMirrorDoesNotTouchUnregisteredLiveCursor(t *testing.T) {
	s := testLedger(t)
	c, _ := s.CreateCard(ledger.NewCard{Title: "卡", Project: "p", Workflow: "bug", Actor: "t"})
	_ = s.LinkTask(c.ID, "mac-02", "T9", "implement", "t")
	if err := s.TouchMirrorHealth("mac-02", 1); err != nil {
		t.Fatal(err)
	}
	t1 := time.Time{}
	rows, _ := s.MirrorHealth()
	for _, r := range rows {
		if r.Target == "mac-02" {
			t1 = r.UpdatedAt
		}
	}
	blockSrc := func(ctx context.Context, _ *client.Client, _ string, _ int64, _ func(proto.Event) error) error {
		<-ctx.Done()
		return ctx.Err()
	}
	m := New(s, machinesWith(t, "idle-box"), Options{Holder: "test", Tick: 50 * time.Millisecond,
		LeaseTTL: time.Second, Source: blockSrc})
	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)
	t.Cleanup(func() { cancel(); m.Stop() })
	time.Sleep(300 * time.Millisecond)
	cur, _ := s.MirrorHealth()
	for _, r := range cur {
		if r.Target == "mac-02" && r.UpdatedAt.After(t1) {
			t.Fatalf("清单外但仍有在飞挂账的 cursor 不应被空 touch: %v -> %v", t1, r.UpdatedAt)
		}
	}
}

func TestMirrorStopBeforeRun(t *testing.T) {
	s := testLedger(t)
	m := New(s, newFakeMachines(), Options{Holder: "test"})
	m.Stop()
	done := make(chan struct{})
	go func() {
		m.Run(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop 先于 Run 时，Run 未及时退出")
	}
}
