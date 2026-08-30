package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/targetclient"
)

type previewListStub struct {
	sessions []proto.PreviewSession
	err      error
}

func (s previewListStub) ListPreviews(context.Context) (*proto.PreviewListResp, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &proto.PreviewListResp{Sessions: s.sessions}, nil
}

func TestPreviewMirrorListAndEvents(t *testing.T) {
	remote, _ := newPreviewOwnerEnv(t)
	resp, body := previewPost(t, remote, "/api/previews", `{"port":5173}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remote create status=%d body=%s", resp.StatusCode, body)
	}
	var remoteSession struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &remoteSession); err != nil {
		t.Fatalf("decode remote session: %v", err)
	}

	localDir := t.TempDir()
	localStore, err := store.Open(localDir + "/handoff.db")
	if err != nil {
		t.Fatalf("open local store: %v", err)
	}
	t.Cleanup(func() { _ = localStore.Close() })
	localHub := NewPreviewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	localOwner := NewPreviewOwner(localStore, localHub, PreviewOwnerDeps{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cfg := &config.Config{Targets: map[string]config.Target{"devbox": {Addr: remote.ts.URL, Token: testToken}}}
	pool := targetclient.NewPool(func() *config.Config { return cfg }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer pool.Close()
	mirror := NewPreviewMirror(pool, localOwner, localHub, func(name string) bool { return name == "local" }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list, err := mirror.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(list.Sessions) != 1 || list.Sessions[0].ID != remoteSession.ID || list.Sessions[0].Machine != "devbox" {
		t.Fatalf("sessions=%+v", list.Sessions)
	}
	if len(list.Machines) != 2 || list.Machines[1].Name != "devbox" || !list.Machines[1].Ok {
		t.Fatalf("machines=%+v", list.Machines)
	}
	if _, ok := mirror.Resolve(remoteSession.ID, "devbox"); !ok {
		t.Fatal("remote session must resolve by machine and id")
	}
	if _, ok := mirror.Resolve(remoteSession.ID, ""); ok {
		t.Fatal("remote session must not be treated as local")
	}

	events, cancelEvents := localHub.Subscribe()
	defer cancelEvents()
	go mirror.Run(ctx)
	time.Sleep(100 * time.Millisecond)
	resp, body = previewRequest(t, remote, http.MethodDelete, "/api/previews/"+remoteSession.ID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remote close status=%d body=%s", resp.StatusCode, body)
	}
	select {
	case event := <-events:
		if event.Type != "preview.closed" || event.Session.ID != remoteSession.ID || event.Session.Machine != "devbox" {
			t.Fatalf("event=%+v", event)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for mirrored close")
	}
	mirror.Stop()
}

func TestPreviewMirrorStopExitsRunAndPreventsRestart(t *testing.T) {
	mirror := NewPreviewMirror(nil, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	parent := context.Background()
	runDone := make(chan struct{})
	go func() {
		mirror.Run(parent)
		close(runDone)
	}()

	deadline := time.Now().Add(time.Second)
	for {
		mirror.mu.RLock()
		started := mirror.started
		mirror.mu.RUnlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("mirror Run did not start")
		}
		time.Sleep(time.Millisecond)
	}

	mirror.Stop()
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("mirror Run must exit after Stop while parent context remains active")
	}

	select {
	case <-mirror.stopDone:
	default:
		t.Fatal("mirror stop signal must be closed after Stop")
	}

	// A stopped mirror must not register a new target loop when a caller reaches
	// the ticker path concurrently with the shutdown sequence.
	remote := NewPreviewMirror(targetclient.NewPool(func() *config.Config {
		return &config.Config{Targets: map[string]config.Target{"remote": {Addr: "http://127.0.0.1:1"}}}
	}, slog.New(slog.NewTextHandler(io.Discard, nil))), nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer remote.pool.Close()
	remote.Stop()
	remote.ensureLoops(context.Background())
	remote.mu.RLock()
	loopCount := len(remote.cancels)
	remote.mu.RUnlock()
	if loopCount != 0 {
		t.Fatal("stopped mirror must not restart target loops")
	}
}

func TestPreviewMirrorListConvergencePublishesClosedForDroppedSession(t *testing.T) {
	hub := NewPreviewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	mirror := NewPreviewMirror(nil, nil, hub, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	session := proto.PreviewSession{ID: "preview-dropped", EntryURL: "http://localhost:5173", Machine: "devbox"}
	mirror.mu.Lock()
	mirror.sessions[previewSessionKey("devbox", session.ID)] = session
	mirror.mu.Unlock()
	done := make(chan error)
	launcher := &previewLauncherStub{done: done}
	service := NewPreviewOpenService(nil, mirror, nil, launcher, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer func() {
		select {
		case <-done:
		default:
			close(done)
		}
		_ = service.Stop(context.Background())
	}()
	if resp, err := service.OpenPreview(context.Background(), session.ID, "devbox"); err != nil || resp == nil || !resp.Opened {
		t.Fatalf("open remote preview resp=%+v err=%v", resp, err)
	}
	launcher.mu.Lock()
	profile := launcher.starts[0].UserDataDir
	launcher.mu.Unlock()
	events, cancel := hub.Subscribe()
	defer cancel()

	if err := mirror.refreshTarget(context.Background(), "devbox", previewListStub{}); err != nil {
		t.Fatalf("refresh target: %v", err)
	}
	select {
	case event := <-events:
		if event.Type != proto.PreviewEventClosed || !reflect.DeepEqual(event.Session, session) || event.Machine != "devbox" {
			t.Fatalf("closed event=%+v, want session=%+v", event, session)
		}
	case <-time.After(time.Second):
		t.Fatal("mirror did not publish closed event for dropped session")
	}
	waitForPreviewCondition(t, func() bool {
		launcher.mu.Lock()
		defer launcher.mu.Unlock()
		return len(launcher.stopPIDs) == 1 && launcher.stopPIDs[0] == 42
	})
	if _, err := os.Stat(profile); err != nil {
		t.Fatalf("profile removed before dropped browser exit: %v", err)
	}
	close(done)
	waitForPreviewCondition(t, func() bool {
		_, err := os.Stat(profile)
		return errors.Is(err, os.ErrNotExist)
	})
	if _, ok := mirror.Resolve(session.ID, "devbox"); ok {
		t.Fatal("dropped session remained in mirror")
	}
}
