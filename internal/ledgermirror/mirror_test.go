// 镜像子系统测试：事件源用 fake 注入，验证 lease 独占、幂等重放、
// 挂账对账、终态退订。真网络路径归真机判据，不在单测里装。
package ledgermirror

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
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

// TestB2336MirrorSourceEventCoverage keeps the source-to-card seam explicit:
// each deliverable task event is mirrored once, while progress and approver
// bookkeeping remain source-only audit events.
func TestB2336MirrorSourceEventCoverage(t *testing.T) {
	s := testLedger(t)
	cardID := linkedCard(t, s, "mac-02", "T-source-events")
	input := []proto.Event{
		{Seq: 1, Type: proto.EventTypeQuestion, Payload: []byte(`{"ticket_id":"q1"}`)},
		{Seq: 2, Type: proto.EventTypePermissionRequest, Payload: []byte(`{"ticket_id":"p1"}`)},
		{Seq: 3, Type: proto.EventTypeCompleted, Payload: []byte(`{"summary":"done"}`)},
		{Seq: 4, Type: proto.EventTypeTurnFailed, Payload: []byte(`{"fail_reason":"turn"}`)},
		{Seq: 5, Type: proto.EventTypeFailed, Payload: []byte(`{"fail_reason":"failed"}`)},
		{Seq: 6, Type: proto.EventTypeArchived, Payload: []byte(`{}`)},
		{Seq: 7, Type: proto.EventTypeProgress, Payload: []byte(`{"text":"progress"}`)},
		{Seq: 8, Type: proto.EventTypeApproverDecision, Payload: []byte(`{"decision":"allow"}`)},
		{Seq: 9, Type: proto.EventTypeApproverDisabled, Payload: []byte(`{"reason":"disabled"}`)},
	}
	source := func(ctx context.Context, _ *client.Client, taskID string, fromSeq int64,
		onEvent func(proto.Event) error) error {
		for _, event := range input {
			if event.Seq <= fromSeq {
				continue
			}
			event.TaskID = taskID
			if err := onEvent(event); err != nil {
				return err
			}
		}
		return nil
	}
	m := runMirror(t, s, machinesWith(t, "mac-02"), source)
	defer m.Stop()

	deadline := time.Now().Add(5 * time.Second)
	var events []ledger.Event
	for time.Now().Before(deadline) {
		var err error
		events, err = s.EventsFromAsc([]string{cardID}, 0, 100)
		if err != nil {
			t.Fatalf("读镜像事件: %v", err)
		}
		count := 0
		for _, event := range events {
			if event.Type == ledger.EvTaskMirrored {
				count++
			}
		}
		if count == 6 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	gotTypes := map[string]int{}
	for _, event := range events {
		if event.Type != ledger.EvTaskMirrored {
			if event.Type == ledger.EvReviewVerdict || event.Type == ledger.EvAcceptanceRecorded {
				t.Fatalf("终态镜像不应直接产生业务裁决/验收: %+v", event)
			}
			continue
		}
		var envelope struct {
			TaskType string `json:"task_type"`
		}
		if err := json.Unmarshal(event.Payload, &envelope); err != nil {
			t.Fatalf("解镜像 envelope: %v", err)
		}
		gotTypes[envelope.TaskType]++
	}
	for _, want := range []string{"question", "permission_request", "completed", "turn_failed", "failed", "archived"} {
		if gotTypes[want] != 1 {
			t.Fatalf("源事件 %s 镜像次数=%d，want 1；types=%v", want, gotTypes[want], gotTypes)
		}
	}
	for _, skipped := range []string{"progress", "approver_decision", "approver_disabled"} {
		if gotTypes[skipped] != 0 {
			t.Fatalf("过滤源事件 %s 不应写入镜像: types=%v", skipped, gotTypes)
		}
	}
}

func TestB2336ProjectionChangeResubscribesWithNewAttempt(t *testing.T) {
	s := testLedger(t)
	c, _ := s.CreateCard(ledger.NewCard{Title: "节点重试", Project: "p", Workflow: "bug", Actor: "t"})
	if err := s.LinkTask(c.ID, "mac-02", "T-retry", ledger.PurposeReview, "t"); err != nil {
		t.Fatalf("LinkTask: %v", err)
	}
	if err := s.RecordDispatch(c.ID, ledger.DispatchSnapshot{
		Target: "mac-02", TaskID: "T-retry", Node: "review", Attempt: "attempt-old",
		Branch: "cards/" + c.ID + "-review", Purpose: ledger.PurposeReview, Actor: "t",
	}); err != nil {
		t.Fatalf("RecordDispatch old: %v", err)
	}
	mach := machinesWith(t, "mac-02")
	calls := make(chan srcCall, 4)
	var sourceCalls atomic.Int64
	source := func(ctx context.Context, c *client.Client, taskID string, fromSeq int64,
		onEvent func(proto.Event) error) error {
		seq := sourceCalls.Add(1)
		calls <- srcCall{client: c, from: fromSeq, ctx: ctx}
		if seq > fromSeq {
			if err := onEvent(proto.Event{Seq: seq, TaskID: taskID, Type: "message",
				Payload: []byte(`{"text":"retry"}`)}); err != nil {
				return err
			}
		}
		<-ctx.Done()
		return ctx.Err()
	}
	m := New(s, mach, Options{Holder: "test", Source: source})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.reconcile(ctx)
	first := waitCall(t, calls, "旧 attempt 首次订阅")
	if first.from != 0 {
		t.Fatalf("首次订阅 from=%d, want 0", first.from)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		wm, err := s.MirrorWatermark("mac-02", "T-retry")
		if err != nil {
			t.Fatalf("MirrorWatermark old: %v", err)
		}
		if wm == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if wm, _ := s.MirrorWatermark("mac-02", "T-retry"); wm != 1 {
		t.Fatalf("旧 attempt 事件未落账，watermark=%d", wm)
	}
	if err := s.RecordDispatch(c.ID, ledger.DispatchSnapshot{
		Target: "mac-02", TaskID: "T-retry", Node: "review", Attempt: "attempt-new",
		Branch: "cards/" + c.ID + "-review-2", Purpose: ledger.PurposeReview, Actor: "t",
	}); err != nil {
		t.Fatalf("RecordDispatch new: %v", err)
	}
	m.reconcile(ctx)
	second := waitCall(t, calls, "投影变化后应取消旧订阅并重订阅")
	if second.from != 1 {
		t.Fatalf("新 attempt 应从旧 watermark 1 续拉，实得 %d", second.from)
	}
	select {
	case <-first.ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("投影变化后旧订阅未取消")
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		wm, err := s.MirrorWatermark("mac-02", "T-retry")
		if err != nil {
			t.Fatalf("MirrorWatermark new: %v", err)
		}
		if wm == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if wm, _ := s.MirrorWatermark("mac-02", "T-retry"); wm != 2 {
		t.Fatalf("新 attempt 事件未落账，watermark=%d", wm)
	}
	events, err := s.EventsFromAsc([]string{c.ID}, 0, 100)
	if err != nil {
		t.Fatalf("读镜像事件: %v", err)
	}
	for _, event := range events {
		if event.Type != ledger.EvTaskMirrored || event.SourceSeq != 2 {
			continue
		}
		var envelope struct {
			Node    string `json:"node"`
			Attempt string `json:"attempt"`
		}
		if err := json.Unmarshal(event.Payload, &envelope); err != nil {
			t.Fatalf("解新 attempt envelope: %v", err)
		}
		if envelope.Node != "review" || envelope.Attempt != "attempt-new" {
			t.Fatalf("新 attempt 镜像身份=%+v，want review/attempt-new", envelope)
		}
		m.Stop()
		return
	}
	t.Fatal("缺少新 attempt 的 source_seq=2 镜像事件")
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

func TestMirrorLocalTargetUsesLocalSource(t *testing.T) {
	ledgerStore := testLedger(t)
	cardID := linkedCard(t, ledgerStore, "", "local-task")
	localStore, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("打开本机 store: %v", err)
	}
	defer localStore.Close()
	for i := 0; i < 32; i++ {
		if _, err := localStore.AppendEvent("local-task", proto.EventTypeProgress,
			map[string]int{"index": i}); err != nil {
			t.Fatalf("追加 progress %d: %v", i, err)
		}
	}
	permissionPayload := json.RawMessage(`{"permission_id":"perm-1","command":"git status"}`)
	permission, err := localStore.AppendEvent("local-task", proto.EventTypePermissionRequest, permissionPayload)
	if err != nil {
		t.Fatalf("追加 permission_request: %v", err)
	}
	if _, err := localStore.AppendEvent("local-task", proto.EventTypeApproverDecision,
		map[string]string{"decision": "allow"}); err != nil {
		t.Fatalf("追加 approver_decision: %v", err)
	}

	machines := newFakeMachines()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := New(ledgerStore, machines, Options{
		Holder:   "test-local",
		Tick:     20 * time.Millisecond,
		LeaseTTL: time.Second,
		Source: func(context.Context, *client.Client, string, int64, func(proto.Event) error) error {
			return fmt.Errorf("本机 link 不应调用远端 source")
		},
		LocalSource: NewLocalSource(localStore, log),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.reconcile(ctx)

	deadline := time.Now().Add(time.Second)
	var mirrored ledger.Event
	for time.Now().Before(deadline) {
		events, err := ledgerStore.EventsFromAsc([]string{cardID}, 0, 100)
		if err != nil {
			t.Fatalf("读取 card_events: %v", err)
		}
		for _, event := range events {
			if event.Type == ledger.EvTaskMirrored && event.SourceTask == "local-task" &&
				event.SourceSeq == permission.Seq {
				mirrored = event
			}
		}
		if mirrored.Seq != 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if mirrored.Seq == 0 {
		t.Fatal("一秒内没有从本机 Store 镜像 permission_request")
	}
	if mirrored.SourceTarget != "" || mirrored.SourceTask != "local-task" || mirrored.SourceSeq != permission.Seq {
		t.Fatalf("本机镜像来源 = (%q, %q, %d)，want (%q, local-task, %d)",
			mirrored.SourceTarget, mirrored.SourceTask, mirrored.SourceSeq, "", permission.Seq)
	}
	var mirroredPayload struct {
		TaskType string          `json:"task_type"`
		Payload  json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(mirrored.Payload, &mirroredPayload); err != nil {
		t.Fatalf("解码镜像 payload: %v", err)
	}
	if mirroredPayload.TaskType != string(proto.EventTypePermissionRequest) ||
		string(mirroredPayload.Payload) != string(permissionPayload) {
		t.Fatalf("镜像 payload = %s, want permission_request 原 payload", mirrored.Payload)
	}

	events, err := ledgerStore.EventsFromAsc([]string{cardID}, 0, 100)
	if err != nil {
		t.Fatalf("再次读取 card_events: %v", err)
	}
	var mirroredCount int
	for _, event := range events {
		if event.Type == ledger.EvTaskMirrored {
			mirroredCount++
			if event.SourceSeq != permission.Seq {
				t.Fatalf("progress 或 approver_decision 被镜像: %+v", event)
			}
		}
	}
	if mirroredCount != 1 {
		t.Fatalf("本机应只镜像一条 permission_request，实得 %d", mirroredCount)
	}
	if calls := machines.forCalls(); len(calls) != 0 {
		t.Fatalf("本机 link 不应调用 Machines.For，实得 %v", calls)
	}

	wrote, err := ledgerStore.AppendMirroredEvent(cardID, ledger.MirroredEvent{
		Target: "", Task: "local-task", SourceSeq: permission.Seq,
		Type: string(permission.Type), Payload: permission.Payload, CreatedAt: permission.CreatedAt,
	})
	if err != nil {
		t.Fatalf("重复镜像同 seq: %v", err)
	}
	if wrote {
		t.Fatal("同 seq 重放不应再次写入 task_mirrored")
	}
	m.Stop()
}
