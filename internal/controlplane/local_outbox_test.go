// controlplane 本机 outbox 投影泵集成测试。
//
// 职责：
//   - 证明本机 owner 已持久化的 machine event 会被投影成 control event
//   - 证明重复 drain 不重复广播 revision
//
// 边界：
//   - 使用真实 SQLite store；不启动 HTTP server 或文件 watcher
package controlplane_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/store"
)

func TestLocalOutboxPumpProjectsAndBroadcasts(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	machine, err := st.EnsureLocalMachine(context.Background(), "本机")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertWorkspaceWithMachineEvent(context.Background(), controlplane.Workspace{
		ID: "ws", MachineID: machine.ID, Kind: controlplane.WorkspaceKindMain,
		Path: "/repo", CanonicalPath: "/repo",
	}, controlplane.MachineEventWorkspaceUpsert); err != nil {
		t.Fatal(err)
	}
	projector := controlplane.NewProjector(st)
	var published []controlplane.ControlEvent
	projector.OnApplied = func(event controlplane.ControlEvent) { published = append(published, event) }
	pump := controlplane.NewLocalOutboxPump(st, machine.ID, projector,
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	count, err := pump.Drain(context.Background())
	if err != nil || count != 1 || len(published) != 1 {
		t.Fatalf("first drain count=%d published=%+v err=%v", count, published, err)
	}
	if published[0].Kind != controlplane.ControlEventKindWorkspaceUpsert || published[0].ControlRevision != 1 {
		t.Fatalf("published = %+v", published[0])
	}
	count, err = pump.Drain(context.Background())
	if err != nil || count != 0 || len(published) != 1 {
		t.Fatalf("second drain count=%d published=%+v err=%v", count, published, err)
	}
}
