// B439 镜像命名路由竖切：自名 != 挂账 target 时走池（远端源），自名 ==
// 挂账 target 时走本机源，存量 ” 仍走本机源；订不到目标机时 WARN + 断链亮灯。
package ledgermirror

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

func openEventStore(t *testing.T, name string) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("打开事件库 %s: %v", name, err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestB439MirrorNamedHolderMirrorsRemoteDispatch 持有者 macbook-pro 订阅挂账
// target=linux-01（自名不同 → 走池/远端源），产生 task_mirrored 且 SourceTarget
// 是派发机名字。持有者自己的本机源是空库，若误路由到本机源则读不到事件。
func TestB439MirrorNamedHolderMirrorsRemoteDispatch(t *testing.T) {
	s := testLedger(t)
	cardID := linkedCard(t, s, "linux-01", "T-remote")
	dispatcherStore := openEventStore(t, "linux-01.db")
	if _, err := dispatcherStore.AppendEvent("T-remote", proto.EventType("message"),
		map[string]string{"text": "hi"}); err != nil {
		t.Fatalf("追加远端事件: %v", err)
	}
	holderStore := openEventStore(t, "macbook-pro.db") // 空：持有者本机没有该 task

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcherLocal := NewLocalSource(dispatcherStore, log)
	m := New(s, machinesWith(t, "linux-01"), Options{
		Holder: "macbook-pro", SelfName: "macbook-pro",
		Tick: 20 * time.Millisecond, LeaseTTL: time.Second,
		LocalSource: NewLocalSource(holderStore, log),
		// 远端源（池给出的 linux-01 事件流）在机内用派发机的本地源模拟。
		Source: func(ctx context.Context, c client.EventStreamClient, taskID string, fromSeq int64,
			onEvent func(proto.Event) error) error {
			return dispatcherLocal(ctx, c, taskID, fromSeq, onEvent)
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)
	t.Cleanup(func() { cancel(); m.Stop() })

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		events, err := s.EventsFromAsc([]string{cardID}, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range events {
			if e.Type == ledger.EvTaskMirrored && e.SourceTask == "T-remote" {
				if e.SourceTarget != "linux-01" {
					t.Fatalf("镜像 SourceTarget = %q，期望派发机名 linux-01", e.SourceTarget)
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("持有者 macbook-pro 未镜像挂账 target=linux-01")
}

// TestB439MirrorLegacyEmptyTargetStillMirrors 存量 ” 挂账仍走本机源（S7 兼容，
// WU1 保行为）：持有者恰是派发机时可镜像。
func TestB439MirrorLegacyEmptyTargetStillMirrors(t *testing.T) {
	s := testLedger(t)
	cardID := linkedCard(t, s, "", "T-legacy")
	holderStore := openEventStore(t, "macbook-pro.db")
	if _, err := holderStore.AppendEvent("T-legacy", proto.EventType("message"),
		map[string]string{"text": "hi"}); err != nil {
		t.Fatalf("追加本机事件: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := New(s, newFakeMachines(), Options{
		Holder: "macbook-pro", SelfName: "macbook-pro",
		LocalSource: NewLocalSource(holderStore, log),
	})
	m.reconcile(context.Background())

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		events, err := s.EventsFromAsc([]string{cardID}, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range events {
			if e.Type == ledger.EvTaskMirrored && e.SourceTask == "T-legacy" {
				if e.SourceTarget != "" {
					t.Fatalf("存量 '' 镜像 SourceTarget = %q，期望空串", e.SourceTarget)
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("存量 '' 挂账未走本机源镜像")
}

// TestB439MirrorUnreachableNamedTargetWarnsAndStaysLit 挂账目标机不在本机机器
// 清单时：WARN 带 target/task，健康行不刷新（断链亮灯），不静默。
func TestB439MirrorUnreachableNamedTargetWarnsAndStaysLit(t *testing.T) {
	s := testLedger(t)
	linkedCard(t, s, "linux-01", "T-remote")
	if err := s.TouchMirrorHealth("linux-01", 7); err != nil {
		t.Fatalf("预置健康行: %v", err)
	}
	before, err := s.MirrorHealth()
	if err != nil {
		t.Fatal(err)
	}
	var beforeAt time.Time
	for _, row := range before {
		if row.Target == "linux-01" {
			beforeAt = row.UpdatedAt
		}
	}
	if beforeAt.IsZero() {
		t.Fatal("预置健康行缺失")
	}

	var logs safeBuf
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	m := New(s, newFakeMachines(), Options{Holder: "macbook-pro", SelfName: "macbook-pro"})
	m.reconcile(context.Background())

	out := logs.String()
	if !strings.Contains(out, "target=linux-01") || !strings.Contains(out, "T-remote") {
		t.Fatalf("断链应打 WARN 带 target/task，实得日志:\n%s", out)
	}
	after, err := s.MirrorHealth()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range after {
		if row.Target == "linux-01" && !row.UpdatedAt.Equal(beforeAt) {
			t.Fatalf("断链时健康行被刷新了：before=%v after=%v", beforeAt, row.UpdatedAt)
		}
	}
}
