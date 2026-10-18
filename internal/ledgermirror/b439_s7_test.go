// b439_s7_test.go —— B439 S7：存量无归属 ” 挂账不迁移但不静默。非终态 ” 挂账
// 每轮对账打一次 WARN（带 task），终态 ” 静默；仍按持有者本机尽力订阅。
package ledgermirror

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestB439S7LegacyEmptyTargetInflightWarns(t *testing.T) {
	s := testLedger(t)
	linkedCard(t, s, "", "T-legacy-inflight")

	var logs safeBuf
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	m := New(s, newFakeMachines(), Options{Holder: "macbook-pro", SelfName: "macbook-pro"})
	m.reconcile(context.Background())

	out := logs.String()
	if n := strings.Count(out, "历史无归属挂账"); n != 1 {
		t.Fatalf("非终态 '' 挂账应每轮打一次 WARN，实得 %d 次:\n%s", n, out)
	}
	if !strings.Contains(out, "T-legacy-inflight") {
		t.Fatalf("WARN 应带 task，实得:\n%s", out)
	}
}

func TestB439S7LegacyEmptyTargetTerminalSilent(t *testing.T) {
	s := testLedger(t)
	cardID := linkedCard(t, s, "", "T-legacy-done")
	if _, err := s.AppendMirroredEvent(cardID, ledger.MirroredEvent{
		Target: "", Task: "T-legacy-done", SourceSeq: 1,
		Type: string(proto.EventTypeArchived), Payload: []byte(`{}`),
	}); err != nil {
		t.Fatalf("预置终态 '' 镜像事件: %v", err)
	}

	var logs safeBuf
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	m := New(s, newFakeMachines(), Options{Holder: "macbook-pro", SelfName: "macbook-pro"})
	m.reconcile(context.Background())

	if out := logs.String(); strings.Contains(out, "历史无归属挂账") {
		t.Fatalf("终态 '' 挂账不应打 WARN，实得:\n%s", out)
	}
}

// TestB439S7LegacyEmptyTargetStillSubscribes 非终态 ” 挂账仍走本机源尽力订阅
// （持有者恰是派发机时可用），WARN 不改变订阅行为。
func TestB439S7LegacyEmptyTargetStillSubscribes(t *testing.T) {
	s := testLedger(t)
	cardID := linkedCard(t, s, "", "T-legacy")
	holderStore := openEventStore(t, "macbook-pro.db")
	if _, err := holderStore.AppendEvent("T-legacy", proto.EventType("message"),
		map[string]string{"text": "hi"}); err != nil {
		t.Fatalf("追加本机事件: %v", err)
	}
	m := New(s, newFakeMachines(), Options{
		Holder: "macbook-pro", SelfName: "macbook-pro",
		LocalSource: NewLocalSource(holderStore, slog.New(slog.NewTextHandler(io.Discard, nil))),
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
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("非终态 '' 挂账应仍走本机源镜像")
}
