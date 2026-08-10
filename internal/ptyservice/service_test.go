// ptyservice Service 集成测试：真实 owner shell、replay、resize 与稳定身份。
//
// 职责：
//   - shell cwd 等于 Workspace root，输出 seq 单调
//   - 订阅断开不终止 session，重连按 cursor replay
//   - resize/input 双向生效，显式 close 只结束原 session
//   - 同 command_id 永远返回同 session/incarnation
//
// 边界：
//   - Unix 使用真实 PTY；不依赖桌面、HTTP、SSH 或远端 shell 探针
package ptyservice

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/store"
	"github.com/xushixin/handoff/internal/workspaceapi"
)

func TestServiceDisconnectsSlowSubscriberWithoutUnboundingOutput(t *testing.T) {
	service := &Service{machineID: "machine-1", log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		opts: Options{SubscriberBuffer: 1}}
	runtime := &runtimeSession{metadata: workspaceapi.PtySession{TerminalSessionID: "term-1", Incarnation: "inc-1",
		WorkspaceID: "ws-1", State: workspaceapi.PtyStateActive}, ring: NewRing(2, 8),
		subscribers: make(map[string]*ptySubscriber), ended: make(chan struct{})}
	subscriber := &ptySubscriber{events: make(chan workspaceapi.PtyServerFrame, 1), done: make(chan error, 1), closed: make(chan struct{})}
	runtime.subscribers["slow"] = subscriber

	service.publishData(runtime, []byte("1234"))
	service.publishData(runtime, []byte("5678"))
	select {
	case err := <-subscriber.done:
		var resourceErr *workspaceapi.Error
		if !errors.As(err, &resourceErr) || resourceErr.Code != workspaceapi.ErrorSlowConsumer {
			t.Fatalf("slow subscriber error = %T %v", err, err)
		}
	case <-time.After(time.Second):
		t.Fatal("慢订阅未被断开")
	}
	if runtime.ring.FrameCount() > 2 || runtime.ring.ByteCount() > 8 || len(runtime.subscribers) != 0 {
		t.Fatalf("slow subscriber boundaries = frames:%d bytes:%d subscribers:%d",
			runtime.ring.FrameCount(), runtime.ring.ByteCount(), len(runtime.subscribers))
	}
}

func TestServiceExitMarksFullSubscriberSlowInsteadOfSilentlyDroppingExit(t *testing.T) {
	repo, err := store.Open(filepath.Join(t.TempDir(), "pty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	service, err := NewService(repo, "machine-1", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	session := workspaceapi.PtySession{TerminalSessionID: "term-full", Incarnation: "inc-full",
		WorkspaceID: "ws-1", State: workspaceapi.PtyStateActive, Shell: "/bin/sh", ThroughSeq: 1}
	if _, _, _, err := repo.CreatePtySessionWithMachineEvent(context.Background(), "machine-1", "command-full", session); err != nil {
		t.Fatal(err)
	}
	subscriber := &ptySubscriber{events: make(chan workspaceapi.PtyServerFrame, 1), done: make(chan error, 1), closed: make(chan struct{})}
	subscriber.events <- workspaceapi.PtyServerFrame{Kind: workspaceapi.PtyFrameData, Seq: 1}
	runtime := &runtimeSession{metadata: session, ring: NewRing(2, 8),
		subscribers: map[string]*ptySubscriber{"full": subscriber}, ended: make(chan struct{})}
	service.sessions[session.TerminalSessionID] = runtime
	service.finishRuntime(runtime, 0)
	err = <-subscriber.done
	var resourceErr *workspaceapi.Error
	if !errors.As(err, &resourceErr) || resourceErr.Code != workspaceapi.ErrorSlowConsumer {
		t.Fatalf("full exit subscriber error = %T %v", err, err)
	}
}

func TestServiceConnectReturnsCursorExpiredSnapshot(t *testing.T) {
	service := &Service{machineID: "machine-1", log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		opts: Options{SubscriberBuffer: 1}, sessions: make(map[string]*runtimeSession)}
	runtime := &runtimeSession{metadata: workspaceapi.PtySession{TerminalSessionID: "term-1", Incarnation: "inc-1",
		WorkspaceID: "ws-1", State: workspaceapi.PtyStateActive}, ring: NewRing(2, 7),
		subscribers: make(map[string]*ptySubscriber), ended: make(chan struct{})}
	service.sessions["term-1"] = runtime
	service.publishData(runtime, []byte("old"))
	service.publishData(runtime, []byte("new"))
	service.publishData(runtime, []byte("tail"))

	subscription, err := service.Connect(context.Background(), "term-1", "inc-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	if !subscription.CursorExpired || subscription.Snapshot == nil ||
		subscription.Snapshot.ThroughSeq != 3 || len(subscription.Replay) != 0 {
		t.Fatalf("cursor recovery = expired:%v snapshot:%+v replay:%+v",
			subscription.CursorExpired, subscription.Snapshot, subscription.Replay)
	}
	data, err := base64.StdEncoding.DecodeString(subscription.Snapshot.DataBase64)
	if err != nil || string(data) != "newtail" {
		t.Fatalf("snapshot data = %q, %v", data, err)
	}
}

func TestServiceRejectsCursorAheadOfOwner(t *testing.T) {
	service := &Service{machineID: "machine-1", log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		opts: Options{SubscriberBuffer: 1}, sessions: make(map[string]*runtimeSession)}
	runtime := &runtimeSession{metadata: workspaceapi.PtySession{TerminalSessionID: "term-1", Incarnation: "inc-1",
		WorkspaceID: "ws-1", State: workspaceapi.PtyStateActive, ThroughSeq: 2}, ring: NewRing(2, 8),
		subscribers: make(map[string]*ptySubscriber), ended: make(chan struct{})}
	service.sessions["term-1"] = runtime
	_, err := service.Connect(context.Background(), "term-1", "inc-1", 3)
	var resourceErr *workspaceapi.Error
	if !errors.As(err, &resourceErr) || resourceErr.Code != workspaceapi.ErrorCommandConflict {
		t.Fatalf("future cursor error = %T %v", err, err)
	}
}

func TestServiceCreatesWorkspaceShellAndReconnects(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows PTY 本阶段明确返回 capability unsupported")
	}
	repo, err := store.Open(filepath.Join(t.TempDir(), "pty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	service, err := NewService(repo, "machine-1", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	root := t.TempDir()
	ctx := context.Background()
	session, err := service.Create(ctx, workspaceapi.WorkspaceRef{
		WorkspaceID: "ws-1", MachineID: "machine-1", RootPath: root,
	}, workspaceapi.CreateTerminalCommand{CommandID: "command-1", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	if session.State != workspaceapi.PtyStateActive || session.TerminalSessionID == "" || session.Incarnation == "" {
		t.Fatalf("session=%+v", session)
	}
	duplicate, err := service.Create(ctx, workspaceapi.WorkspaceRef{
		WorkspaceID: "ws-1", MachineID: "machine-1", RootPath: root,
	}, workspaceapi.CreateTerminalCommand{CommandID: "command-1", Cols: 120, Rows: 40})
	if err != nil || duplicate.TerminalSessionID != session.TerminalSessionID || duplicate.Incarnation != session.Incarnation {
		t.Fatalf("duplicate=%+v err=%v", duplicate, err)
	}

	first, err := service.Connect(ctx, session.TerminalSessionID, session.Incarnation, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Send(ctx, inputFrame(session, "printf '__PWD__%s\\n' \"$PWD\"\n")); err != nil {
		t.Fatal(err)
	}
	pwdOutput, lastSeq := waitForOutput(t, first, root)
	if !strings.Contains(pwdOutput, root) {
		t.Fatalf("shell cwd output=%q, want root %q", pwdOutput, root)
	}
	first.Cancel()

	if err := service.Input(ctx, session.TerminalSessionID, session.Incarnation, []byte("printf '__REPLAY__ok\\n'\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	second, err := service.Connect(ctx, session.TerminalSessionID, session.Incarnation, lastSeq)
	if err != nil {
		t.Fatal(err)
	}
	replayed, replaySeq := waitForOutput(t, second, "__REPLAY__ok")
	if !strings.Contains(replayed, "__REPLAY__ok") || replaySeq <= lastSeq {
		t.Fatalf("replay=%q seq=%d last=%d", replayed, replaySeq, lastSeq)
	}
	if err := second.Send(ctx, workspaceapi.PtyClientFrame{
		Version: 1, Kind: workspaceapi.PtyClientFrameResize,
		TerminalSessionID: session.TerminalSessionID, Incarnation: session.Incarnation,
		Cols: 91, Rows: 33,
	}); err != nil {
		t.Fatal(err)
	}
	if err := second.Send(ctx, inputFrame(session, "printf '__SIZE__'; stty size\n")); err != nil {
		t.Fatal(err)
	}
	sizeOutput, _ := waitForOutput(t, second, "33 91")
	if !strings.Contains(sizeOutput, "__SIZE__") {
		t.Fatalf("resize output=%q", sizeOutput)
	}
	second.Cancel()

	ended, err := service.CloseTerminal(ctx, session.TerminalSessionID, session.Incarnation)
	if err != nil {
		t.Fatal(err)
	}
	if ended.State != workspaceapi.PtyStateEnded {
		t.Fatalf("ended=%+v", ended)
	}
	got, err := service.Get(ctx, session.TerminalSessionID)
	if err != nil || got.State != workspaceapi.PtyStateEnded || got.Incarnation != session.Incarnation {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	endedSubscription, err := service.Connect(ctx, session.TerminalSessionID, session.Incarnation, replaySeq)
	if err != nil {
		t.Fatal(err)
	}
	if len(endedSubscription.Replay) == 0 ||
		endedSubscription.Replay[len(endedSubscription.Replay)-1].Kind != workspaceapi.PtyFrameExit {
		t.Fatalf("ended reconnect replay = %+v", endedSubscription.Replay)
	}
	select {
	case _, ok := <-endedSubscription.Events:
		if ok {
			t.Fatal("ended reconnect events 必须关闭")
		}
	case <-time.After(time.Second):
		t.Fatal("ended reconnect events 未关闭")
	}
	select {
	case streamErr := <-endedSubscription.Done:
		if streamErr != nil {
			t.Fatalf("ended reconnect done = %v", streamErr)
		}
	case <-time.After(time.Second):
		t.Fatal("ended reconnect done 未关闭")
	}
}

func TestServiceRestartEndsOldIdentityWithoutStartingReplacement(t *testing.T) {
	repo, err := store.Open(filepath.Join(t.TempDir(), "pty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	old := workspaceapi.PtySession{TerminalSessionID: "old-term", Incarnation: "old-inc",
		WorkspaceID: "ws-1", State: workspaceapi.PtyStateActive, Shell: "/bin/sh", ThroughSeq: 12}
	if _, _, _, err := repo.CreatePtySessionWithMachineEvent(context.Background(), "machine-1", "old-command", old); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repo, "machine-1", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	result, err := service.Create(context.Background(), workspaceapi.WorkspaceRef{
		WorkspaceID: "ws-1", MachineID: "machine-1", RootPath: t.TempDir(),
	}, workspaceapi.CreateTerminalCommand{CommandID: "old-command", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	if result.TerminalSessionID != "old-term" || result.Incarnation != "old-inc" ||
		result.State != workspaceapi.PtyStateEnded || len(service.sessions) != 0 {
		t.Fatalf("restart identity = %+v runtimes=%d", result, len(service.sessions))
	}
	duplicate, err := service.Create(context.Background(), workspaceapi.WorkspaceRef{
		WorkspaceID: "ws-1", MachineID: "machine-1", RootPath: t.TempDir(),
	}, workspaceapi.CreateTerminalCommand{CommandID: "old-command", Cols: 100, Rows: 30})
	if err != nil || duplicate.TerminalSessionID != "old-term" || duplicate.State != workspaceapi.PtyStateEnded {
		t.Fatalf("cached durable command = %+v err=%v", duplicate, err)
	}
	historyLost, err := service.Connect(context.Background(), "old-term", "old-inc", 3)
	if err != nil {
		t.Fatalf("crash recovery history loss: %v", err)
	}
	if !historyLost.CursorExpired || historyLost.Snapshot == nil ||
		historyLost.Snapshot.ThroughSeq != 12 || historyLost.Snapshot.DataBase64 != "" {
		t.Fatalf("crash recovery history signal = expired:%t snapshot:%+v",
			historyLost.CursorExpired, historyLost.Snapshot)
	}
	select {
	case frame := <-historyLost.Events:
		if frame.Kind != workspaceapi.PtyFrameExit || frame.Seq != 12 {
			t.Fatalf("history loss exit = %+v", frame)
		}
	case <-time.After(time.Second):
		t.Fatal("history loss reconnect 未返回 exit")
	}
	reconnected, err := service.Connect(context.Background(), "old-term", "old-inc", 99)
	if err != nil {
		t.Fatalf("crash recovery reconnect: %v", err)
	}
	if reconnected.Session.ThroughSeq != 99 || len(reconnected.Replay) != 1 ||
		reconnected.Replay[0].Kind != workspaceapi.PtyFrameExit || reconnected.Replay[0].Seq != 99 {
		t.Fatalf("crash recovery cursor reset = session:%+v replay:%+v", reconnected.Session, reconnected.Replay)
	}
}

func TestServiceIdempotentCommandDoesNotRequireWorkspacePathAfterCreation(t *testing.T) {
	repo, err := store.Open(filepath.Join(t.TempDir(), "pty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ended := workspaceapi.PtySession{TerminalSessionID: "ended-term", Incarnation: "ended-inc",
		WorkspaceID: "ws-1", State: workspaceapi.PtyStateEnded, Shell: "/bin/sh"}
	if _, _, _, err := repo.CreatePtySessionWithMachineEvent(context.Background(), "machine-1", "ended-command", ended); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repo, "machine-1", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	missingRoot := filepath.Join(t.TempDir(), "removed")
	if err := os.RemoveAll(missingRoot); err != nil {
		t.Fatal(err)
	}
	result, err := service.Create(context.Background(), workspaceapi.WorkspaceRef{
		WorkspaceID: "ws-1", MachineID: "machine-1", RootPath: missingRoot,
	}, workspaceapi.CreateTerminalCommand{CommandID: "ended-command", Cols: 80, Rows: 24})
	if err != nil || result.TerminalSessionID != ended.TerminalSessionID || result.State != workspaceapi.PtyStateEnded {
		t.Fatalf("idempotent command with missing root = %+v err=%v", result, err)
	}
	_, err = service.Create(context.Background(), workspaceapi.WorkspaceRef{
		WorkspaceID: "ws-other", MachineID: "machine-1", RootPath: missingRoot,
	}, workspaceapi.CreateTerminalCommand{CommandID: "ended-command", Cols: 80, Rows: 24})
	var resourceErr *workspaceapi.Error
	if !errors.As(err, &resourceErr) || resourceErr.Code != workspaceapi.ErrorCommandConflict {
		t.Fatalf("cross-workspace cached command error = %T %v", err, err)
	}
}

func TestServiceExitAndIdempotentCreateDoNotDeadlockAcrossNotifier(t *testing.T) {
	base, err := store.Open(filepath.Join(t.TempDir(), "pty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	repo := &blockingExitRepository{Store: base, entered: make(chan struct{}), release: make(chan struct{})}
	service, err := NewService(repo, "machine-1", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	session := workspaceapi.PtySession{TerminalSessionID: "term-lock", Incarnation: "inc-lock",
		WorkspaceID: "ws-lock", State: workspaceapi.PtyStateActive, Shell: "/bin/sh"}
	if _, _, _, err := base.CreatePtySessionWithMachineEvent(context.Background(), "machine-1", "command-lock", session); err != nil {
		t.Fatal(err)
	}
	runtime := &runtimeSession{metadata: session, ring: NewRing(2, 8),
		subscribers: make(map[string]*ptySubscriber), ended: make(chan struct{})}
	service.sessions[session.TerminalSessionID] = runtime
	service.commands["command-lock"] = session.TerminalSessionID
	notified := make(chan struct{}, 1)
	service.SetOutboxNotifier(func() { notified <- struct{}{} })

	finished := make(chan struct{})
	go func() {
		service.finishRuntime(runtime, 0)
		close(finished)
	}()
	select {
	case <-repo.entered:
	case <-time.After(time.Second):
		t.Fatal("exit persistence 未进入阻塞点")
	}
	created := make(chan error, 1)
	go func() {
		_, createErr := service.Create(context.Background(), workspaceapi.WorkspaceRef{
			WorkspaceID: "ws-lock", MachineID: "machine-1", RootPath: t.TempDir(),
		}, workspaceapi.CreateTerminalCommand{CommandID: "command-lock"})
		created <- createErr
	}()
	close(repo.release)
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("finishRuntime 与幂等 Create 发生死锁")
	}
	select {
	case createErr := <-created:
		if createErr != nil {
			t.Fatalf("idempotent Create: %v", createErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("幂等 Create 未在 exit 持久化后完成")
	}
	select {
	case <-notified:
	case <-time.After(time.Second):
		t.Fatal("exit 持久化后 notifier 未执行")
	}
}

func TestServiceRetriesExitPersistence(t *testing.T) {
	base, err := store.Open(filepath.Join(t.TempDir(), "pty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	repo := &flakyExitRepository{Store: base, remaining: 2}
	service, err := NewService(repo, "machine-1", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	session := workspaceapi.PtySession{TerminalSessionID: "term-retry", Incarnation: "inc-retry",
		WorkspaceID: "ws-1", State: workspaceapi.PtyStateActive, Shell: "/bin/sh"}
	if _, _, _, err := base.CreatePtySessionWithMachineEvent(context.Background(), "machine-1", "command-retry", session); err != nil {
		t.Fatal(err)
	}
	runtime := &runtimeSession{metadata: session, ring: NewRing(2, 8), subscribers: make(map[string]*ptySubscriber), ended: make(chan struct{})}
	service.sessions[session.TerminalSessionID] = runtime
	service.finishRuntime(runtime, 0)
	if repo.callCount() != 3 {
		t.Fatalf("exit persistence calls = %d, want 3", repo.callCount())
	}
	persisted, err := base.GetPtySession(context.Background(), session.TerminalSessionID)
	if err != nil || persisted.State != workspaceapi.PtyStateEnded {
		t.Fatalf("persisted session = %+v err=%v", persisted, err)
	}
}

func TestServiceSurfacesPermanentExitPersistenceFailure(t *testing.T) {
	base, err := store.Open(filepath.Join(t.TempDir(), "pty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	repo := &flakyExitRepository{Store: base, remaining: 100}
	service, err := NewService(repo, "machine-1", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	session := workspaceapi.PtySession{TerminalSessionID: "term-fail", Incarnation: "inc-fail",
		WorkspaceID: "ws-1", State: workspaceapi.PtyStateActive, Shell: "/bin/sh"}
	if _, _, _, err := base.CreatePtySessionWithMachineEvent(context.Background(), "machine-1", "command-fail", session); err != nil {
		t.Fatal(err)
	}
	subscriber := &ptySubscriber{events: make(chan workspaceapi.PtyServerFrame, 1), done: make(chan error, 1), closed: make(chan struct{})}
	runtime := &runtimeSession{metadata: session, ring: NewRing(2, 8),
		subscribers: map[string]*ptySubscriber{"watcher": subscriber}, ended: make(chan struct{})}
	service.sessions[session.TerminalSessionID] = runtime
	service.finishRuntime(runtime, 0)
	if frame, ok := <-subscriber.events; ok {
		t.Fatalf("durable exit failure leaked terminal exit frame: %+v", frame)
	}
	streamErr := <-subscriber.done
	var resourceErr *workspaceapi.Error
	if !errors.As(streamErr, &resourceErr) || resourceErr.Code != workspaceapi.ErrorUnavailable {
		t.Fatalf("persistence stream error = %T %v", streamErr, streamErr)
	}
	if repo.callCount() != 3 {
		t.Fatalf("exit persistence calls = %d, want 3", repo.callCount())
	}
	if _, closeErr := service.CloseTerminal(context.Background(), session.TerminalSessionID, session.Incarnation); closeErr == nil {
		t.Fatal("explicit close must surface durable exit failure")
	} else if !errors.As(closeErr, &resourceErr) || resourceErr.Code != workspaceapi.ErrorUnavailable {
		t.Fatalf("explicit close persistence error = %T %v", closeErr, closeErr)
	}
	if _, connectErr := service.Connect(context.Background(), session.TerminalSessionID, session.Incarnation, 0); connectErr == nil {
		t.Fatal("reconnect must surface durable exit failure")
	}
	repo.allowPersistence()
	reconciled, reconcileErr := service.Get(context.Background(), session.TerminalSessionID)
	if reconcileErr != nil || reconciled.State != workspaceapi.PtyStateEnded {
		t.Fatalf("follow-up reconciliation = %+v err=%v", reconciled, reconcileErr)
	}
	persisted, persistErr := base.GetPtySession(context.Background(), session.TerminalSessionID)
	if persistErr != nil || persisted.State != workspaceapi.PtyStateEnded {
		t.Fatalf("reconciled durable session = %+v err=%v", persisted, persistErr)
	}
	if _, connectErr := service.Connect(context.Background(), session.TerminalSessionID, session.Incarnation, 0); connectErr != nil {
		t.Fatalf("reconnect after reconciliation: %v", connectErr)
	}
}

func TestServiceBoundsConcurrentSubscribersPerSession(t *testing.T) {
	service := &Service{machineID: "machine-1", log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		opts: Options{SubscriberBuffer: 1, MaxSubscribers: 2}, sessions: make(map[string]*runtimeSession)}
	runtime := &runtimeSession{metadata: workspaceapi.PtySession{TerminalSessionID: "term-bounded", Incarnation: "inc-bounded",
		WorkspaceID: "ws-bounded", State: workspaceapi.PtyStateActive}, ring: NewRing(2, 8),
		subscribers: make(map[string]*ptySubscriber), ended: make(chan struct{})}
	service.sessions[runtime.metadata.TerminalSessionID] = runtime
	first, err := service.Connect(context.Background(), "term-bounded", "inc-bounded", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Cancel()
	second, err := service.Connect(context.Background(), "term-bounded", "inc-bounded", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Cancel()
	_, err = service.Connect(context.Background(), "term-bounded", "inc-bounded", 0)
	var resourceErr *workspaceapi.Error
	if !errors.As(err, &resourceErr) || resourceErr.Code != workspaceapi.ErrorUnavailable {
		t.Fatalf("subscriber limit error = %T %v", err, err)
	}
}

func TestServiceSpawnFailureReportsExitPersistenceFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows PTY 本阶段明确返回 capability unsupported")
	}
	base, err := store.Open(filepath.Join(t.TempDir(), "pty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	repo := &flakyExitRepository{Store: base, remaining: 100}
	service, err := NewServiceWithOptions(repo, "machine-1", slog.New(slog.NewTextHandler(io.Discard, nil)),
		Options{Shell: filepath.Join(t.TempDir(), "missing-shell")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(context.Background(), workspaceapi.WorkspaceRef{
		WorkspaceID: "ws-1", MachineID: "machine-1", RootPath: t.TempDir(),
	}, workspaceapi.CreateTerminalCommand{CommandID: "spawn-failure", Cols: 80, Rows: 24})
	var resourceErr *workspaceapi.Error
	if !errors.As(err, &resourceErr) || resourceErr.Code != workspaceapi.ErrorUnavailable {
		t.Fatalf("spawn persistence error = %T %v", err, err)
	}
	repo.allowPersistence()
	retry, retryErr := service.Create(context.Background(), workspaceapi.WorkspaceRef{
		WorkspaceID: "ws-1", MachineID: "machine-1", RootPath: t.TempDir(),
	}, workspaceapi.CreateTerminalCommand{CommandID: "spawn-failure", Cols: 100, Rows: 30})
	if retryErr != nil || retry.State != workspaceapi.PtyStateEnded || retry.TerminalSessionID == "" {
		t.Fatalf("spawn failure command reconciliation = %+v err=%v", retry, retryErr)
	}
}

func TestServiceActivePersistenceFailureEndsStableIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows PTY 本阶段明确返回 capability unsupported")
	}
	base, err := store.Open(filepath.Join(t.TempDir(), "pty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	repo := &flakyExitRepository{Store: base, activeRemaining: 1}
	service, err := NewService(repo, "machine-1", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	_, err = service.Create(context.Background(), workspaceapi.WorkspaceRef{
		WorkspaceID: "ws-1", MachineID: "machine-1", RootPath: root,
	}, workspaceapi.CreateTerminalCommand{CommandID: "active-failure", Cols: 80, Rows: 24})
	if err == nil {
		t.Fatal("active persistence failure must fail create")
	}
	persisted, err := base.GetPtySessionByCommandID(context.Background(), "active-failure")
	if err != nil || persisted.State != workspaceapi.PtyStateEnded {
		t.Fatalf("active failure durable identity = %+v err=%v", persisted, err)
	}
	retry, err := service.Create(context.Background(), workspaceapi.WorkspaceRef{
		WorkspaceID: "ws-1", MachineID: "machine-1", RootPath: root,
	}, workspaceapi.CreateTerminalCommand{CommandID: "active-failure", Cols: 100, Rows: 30})
	if err != nil || retry.TerminalSessionID != persisted.TerminalSessionID || retry.State != workspaceapi.PtyStateEnded {
		t.Fatalf("active failure retry = %+v err=%v", retry, err)
	}
}

type flakyExitRepository struct {
	*store.Store
	mu              sync.Mutex
	remaining       int
	activeRemaining int
	calls           int
}

type blockingExitRepository struct {
	*store.Store
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *blockingExitRepository) UpdatePtySessionWithMachineEvent(ctx context.Context, machineID string,
	session workspaceapi.PtySession, kind controlplane.MachineEventKind) (controlplane.MachineEvent, error) {
	if kind == controlplane.MachineEventPtyExit {
		r.once.Do(func() { close(r.entered) })
		select {
		case <-r.release:
		case <-ctx.Done():
			return controlplane.MachineEvent{}, ctx.Err()
		}
	}
	return r.Store.UpdatePtySessionWithMachineEvent(ctx, machineID, session, kind)
}

func (r *flakyExitRepository) UpdatePtySessionWithMachineEvent(ctx context.Context, machineID string,
	session workspaceapi.PtySession, kind controlplane.MachineEventKind) (controlplane.MachineEvent, error) {
	if kind == controlplane.MachineEventPtyUpsert && session.State == workspaceapi.PtyStateActive {
		r.mu.Lock()
		if r.activeRemaining > 0 {
			r.activeRemaining--
			r.mu.Unlock()
			return controlplane.MachineEvent{}, fmt.Errorf("injected PTY active persistence failure")
		}
		r.mu.Unlock()
	}
	if kind == controlplane.MachineEventPtyExit {
		r.mu.Lock()
		r.calls++
		if r.remaining > 0 {
			r.remaining--
			r.mu.Unlock()
			return controlplane.MachineEvent{}, fmt.Errorf("injected PTY exit persistence failure")
		}
		r.mu.Unlock()
	}
	return r.Store.UpdatePtySessionWithMachineEvent(ctx, machineID, session, kind)
}

func (r *flakyExitRepository) allowPersistence() {
	r.mu.Lock()
	r.remaining = 0
	r.activeRemaining = 0
	r.mu.Unlock()
}

func (r *flakyExitRepository) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func inputFrame(session workspaceapi.PtySession, value string) workspaceapi.PtyClientFrame {
	return workspaceapi.PtyClientFrame{
		Version: 1, Kind: workspaceapi.PtyClientFrameInput,
		TerminalSessionID: session.TerminalSessionID, Incarnation: session.Incarnation,
		DataBase64: base64.StdEncoding.EncodeToString([]byte(value)),
	}
}

func waitForOutput(t *testing.T, subscription *workspaceapi.PtySubscription, needle string) (string, int64) {
	t.Helper()
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	var output strings.Builder
	lastSeq := int64(0)
	consume := func(frame workspaceapi.PtyServerFrame) bool {
		if frame.Kind == workspaceapi.PtyFrameData {
			data, err := base64.StdEncoding.DecodeString(frame.DataBase64)
			if err != nil {
				t.Fatalf("decode frame: %v", err)
			}
			output.Write(data)
			if frame.Seq <= lastSeq {
				t.Fatalf("seq 不单调: previous=%d current=%d", lastSeq, frame.Seq)
			}
			lastSeq = frame.Seq
		}
		return strings.Contains(output.String(), needle)
	}
	for _, frame := range subscription.Replay {
		if consume(frame) {
			return output.String(), lastSeq
		}
	}
	for {
		select {
		case frame := <-subscription.Events:
			if consume(frame) {
				return output.String(), lastSeq
			}
		case err := <-subscription.Done:
			t.Fatalf("subscription ended before %q: %v output=%q", needle, err, output.String())
		case <-timer.C:
			t.Fatalf("timeout waiting %q output=%q", needle, output.String())
		}
	}
}
