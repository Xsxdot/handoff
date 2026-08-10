// store PTY session 测试：元数据、幂等 command 与 owner outbox 原子性。
//
// 职责：
//   - session create/update/exit 与 machine event 同事务
//   - command_id 重复只返回原 session，不创建第二个 shell 身份
//   - agentd 重启把遗留 active/starting session 标 ended
//
// 边界：
//   - 只验证 SQLite 事实，不启动 PTY，也不持久化 terminal bytes
package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/workspaceapi"
)

func TestPtySessionLifecycleWritesMachineEventsAtomically(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "pty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	session := workspaceapi.PtySession{
		TerminalSessionID: "term-1", Incarnation: "inc-1", WorkspaceID: "ws-1",
		State: workspaceapi.PtyStateStarting, Shell: "/bin/sh",
	}
	created, inserted, event, err := s.CreatePtySessionWithMachineEvent(ctx, "machine-1", "command-1", session)
	if err != nil {
		t.Fatal(err)
	}
	if !inserted || created.TerminalSessionID != session.TerminalSessionID ||
		event.Kind != controlplane.MachineEventPtyUpsert || event.MachineSeq != 1 {
		t.Fatalf("created=%+v inserted=%v event=%+v", created, inserted, event)
	}

	duplicate := session
	duplicate.TerminalSessionID = "must-not-exist"
	got, inserted, duplicateEvent, err := s.CreatePtySessionWithMachineEvent(ctx, "machine-1", "command-1", duplicate)
	if err != nil {
		t.Fatal(err)
	}
	if inserted || duplicateEvent.MachineSeq != 0 || got.TerminalSessionID != "term-1" {
		t.Fatalf("duplicate got=%+v inserted=%v event=%+v", got, inserted, duplicateEvent)
	}

	created.State = workspaceapi.PtyStateActive
	if _, err := s.UpdatePtySessionWithMachineEvent(ctx, "machine-1", created, controlplane.MachineEventPtyUpsert); err != nil {
		t.Fatal(err)
	}
	exitCode := 0
	created.State = workspaceapi.PtyStateEnded
	created.ThroughSeq = 7
	created.ExitCode = &exitCode
	exitEvent, err := s.UpdatePtySessionWithMachineEvent(ctx, "machine-1", created, controlplane.MachineEventPtyExit)
	if err != nil {
		t.Fatal(err)
	}
	if exitEvent.MachineSeq != 3 || exitEvent.Kind != controlplane.MachineEventPtyExit {
		t.Fatalf("exit event=%+v", exitEvent)
	}
	persisted, err := s.GetPtySession(ctx, "term-1")
	if err != nil || persisted.State != workspaceapi.PtyStateEnded || persisted.ThroughSeq != 7 ||
		persisted.ExitCode == nil || *persisted.ExitCode != 0 {
		t.Fatalf("persisted=%+v err=%v", persisted, err)
	}
	events, err := s.MachineEventsAfter(ctx, "machine-1", 0, 10)
	if err != nil || len(events) != 3 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}

func TestEndActivePtySessionsAfterRestartKeepsIdentity(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "pty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	session := workspaceapi.PtySession{
		TerminalSessionID: "old-term", Incarnation: "old-inc", WorkspaceID: "ws-1",
		State: workspaceapi.PtyStateActive, Shell: "/bin/sh", ThroughSeq: 9,
	}
	if _, _, _, err := s.CreatePtySessionWithMachineEvent(ctx, "machine-1", "old-command", session); err != nil {
		t.Fatal(err)
	}
	ended, err := s.EndActivePtySessionsWithMachineEvents(ctx, "machine-1")
	if err != nil || ended != 1 {
		t.Fatalf("ended=%d err=%v", ended, err)
	}
	got, err := s.GetPtySessionByCommandID(ctx, "old-command")
	if err != nil || got.TerminalSessionID != "old-term" || got.Incarnation != "old-inc" ||
		got.State != workspaceapi.PtyStateEnded {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}
