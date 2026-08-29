package agentd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/targetclient"
)

type failingPreviewOpener struct{}

func (failingPreviewOpener) OpenPreview(context.Context, string, string) (*proto.PreviewOpenResp, error) {
	return &proto.PreviewOpenResp{}, errors.New("opener stop failed")
}
func (failingPreviewOpener) Stop(context.Context) error { return errors.New("opener stop failed") }

func TestPreviewCloseEventReclaimsLocalBrowserResources(t *testing.T) {
	_, owner := newPreviewOwnerEnv(t)
	session, err := owner.Create(context.Background(), protoPreviewPortReq())
	if err != nil {
		t.Fatalf("create preview: %v", err)
	}
	launcher := &previewLauncherStub{done: make(chan error)}
	service := NewPreviewOpenService(owner, nil, nil, launcher, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := service.OpenPreview(context.Background(), session.ID, ""); err != nil {
		t.Fatalf("open preview: %v", err)
	}
	if _, err := owner.Close(context.Background(), session.ID); err != nil {
		t.Fatalf("close preview: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		service.mu.Lock()
		remaining := len(service.processes)
		service.mu.Unlock()
		if remaining == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	service.mu.Lock()
	remaining := len(service.processes)
	service.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("processes after owner close=%d", remaining)
	}
	resp, err := service.OpenPreview(context.Background(), session.ID, "")
	if !errors.Is(err, store.ErrNotFound) || resp == nil || resp.Opened {
		t.Fatalf("reopen closed preview resp=%+v err=%v", resp, err)
	}
	_ = service.Stop(context.Background())
}

func TestStopPreviewServicesContinuesOwnerCleanupAfterOpenerError(t *testing.T) {
	env, owner := newPreviewOwnerEnv(t)
	events, cancel := owner.hub.Subscribe()
	defer cancel()
	env.srv.SetPreviewOpener(failingPreviewOpener{})
	if err := env.srv.StopPreviewServices(context.Background()); err == nil {
		t.Fatal("stop should return opener error")
	}
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("owner hub must close after opener error")
		}
	case <-time.After(time.Second):
		t.Fatal("owner cleanup did not continue")
	}
}

// TestPreviewRegressionOwnerMirrorProjection walks the frozen preview contract from
// owner HTTP/WS text through client and mirror projection to the local open handler.
// It does not prove Chromium, OS focus, cross-machine DNS, or a restarted agentd.
func TestPreviewRegressionOwnerMirrorProjection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	remoteEnv, remoteOwner := newPreviewOwnerEnv(t)
	remoteClient := client.New(remoteEnv.ts.URL, testToken)

	streamCtx, cancelStream := context.WithCancel(ctx)
	events := make(chan proto.PreviewEvent, 4)
	streamErr := make(chan error, 1)
	go func() {
		streamErr <- remoteClient.StreamPreviewEventsOnce(streamCtx, func(event proto.PreviewEvent) error {
			events <- event
			return nil
		})
	}()
	waitPreviewHubSubscribers(t, remoteOwner.hub, 1)

	created, err := remoteClient.CreatePreview(ctx, protoPreviewPortReq())
	if err != nil {
		t.Fatalf("owner HTTP create: %v", err)
	}
	createdEvent := readPreviewRegressionEvent(t, ctx, events)
	if createdEvent.Type != proto.PreviewEventCreated || createdEvent.Session.ID != created.ID || createdEvent.Session.OriginURL == "" || createdEvent.Session.Branch == "" || createdEvent.Session.TTLSeconds != 7200 {
		t.Fatalf("created event=%+v session=%+v", createdEvent, created)
	}
	if _, err := remoteClient.ClosePreview(ctx, created.ID); err != nil {
		t.Fatalf("owner HTTP close: %v", err)
	}
	closedEvent := readPreviewRegressionEvent(t, ctx, events)
	if closedEvent.Type != proto.PreviewEventClosed || !reflect.DeepEqual(closedEvent.Session, createdEvent.Session) {
		t.Fatalf("closed event=%+v created=%+v", closedEvent, createdEvent)
	}
	cancelStream()
	select {
	case <-streamErr:
	case <-ctx.Done():
		t.Fatalf("owner WS did not close: %v", ctx.Err())
	}

	remoteOwner.deps.NewID = func() string { return "preview-active" }
	active, err := remoteClient.CreatePreview(ctx, protoPreviewPortReq())
	if err != nil {
		t.Fatalf("owner HTTP create for mirror: %v", err)
	}
	ownerList, err := remoteClient.ListPreviews(ctx)
	if err != nil || len(ownerList.Sessions) != 1 || ownerList.Sessions[0].Machine != "" {
		t.Fatalf("owner list=%+v err=%v", ownerList, err)
	}

	localEnv, localOwner := newPreviewOwnerEnv(t)
	pool := targetclient.NewPool(func() *config.Config {
		return &config.Config{Targets: map[string]config.Target{"devbox": {Addr: remoteEnv.ts.URL, Token: testToken}}}
	}, previewTestLogger(t))
	defer pool.Close()
	mirror := NewPreviewMirror(pool, localOwner, localOwner.hub, nil, previewTestLogger(t))
	localEnv.srv.SetPreviewMirror(mirror)
	launcher := &previewLauncherStub{}
	opener := NewPreviewOpenService(localOwner, mirror, pool, launcher, previewTestLogger(t))
	localEnv.srv.SetPreviewOpener(opener)
	defer func() { _ = opener.Stop(context.Background()) }()
	localClient := client.New(localEnv.ts.URL, testToken)

	all, err := localClient.ListPreviewsAll(ctx)
	if err != nil {
		t.Fatalf("coordinator scope=all: %v", err)
	}
	if len(all.Sessions) != 1 || all.Sessions[0].ID != active.ID || all.Sessions[0].Machine != "devbox" {
		t.Fatalf("coordinator sessions=%+v", all.Sessions)
	}
	if len(all.Machines) != 2 || all.Machines[0].Name != "" || all.Machines[1].Name != "devbox" || !all.Machines[1].Ok {
		t.Fatalf("coordinator machines=%+v", all.Machines)
	}
	if _, err := localOwner.st.GetPreview(active.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("remote owner session leaked into local store: err=%v", err)
	}
	if _, err := localClient.ClosePreview(ctx, active.ID); err == nil {
		t.Fatal("coordinator close must not close remote owner truth")
	}
	if _, err := localClient.OpenPreview(ctx, active.ID, "devbox"); err != nil {
		t.Fatalf("coordinator open query: %v", err)
	}
	launcher.mu.Lock()
	if len(launcher.starts) != 1 || launcher.starts[0].SessionID != active.ID {
		launcher.mu.Unlock()
		t.Fatalf("launcher starts=%+v", launcher.starts)
	}
	launcher.mu.Unlock()

	projectedEvents, cancelEvents := localOwner.hub.Subscribe()
	defer cancelEvents()
	go mirror.Run(ctx)
	waitPreviewHubSubscribers(t, remoteOwner.hub, 1)
	if _, err := remoteClient.ClosePreview(ctx, active.ID); err != nil {
		t.Fatalf("remote close for mirror: %v", err)
	}
	select {
	case event := <-projectedEvents:
		if event.Type != proto.PreviewEventClosed || event.Session.ID != active.ID || event.Session.Machine != "devbox" {
			t.Fatalf("projected close event=%+v", event)
		}
	case <-ctx.Done():
		t.Fatalf("mirror did not project close: %v", ctx.Err())
	}
	if _, ok := mirror.Resolve(active.ID, "devbox"); ok {
		t.Fatal("mirror retained closed session")
	}
	mirror.Stop()
}

func readPreviewRegressionEvent(t *testing.T, ctx context.Context, events <-chan proto.PreviewEvent) proto.PreviewEvent {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-ctx.Done():
		t.Fatalf("preview event timeout: %v", ctx.Err())
		return proto.PreviewEvent{}
	}
}

func waitPreviewHubSubscribers(t *testing.T, hub *PreviewHub, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		hub.mu.Lock()
		got := len(hub.subscribers)
		hub.mu.Unlock()
		if got >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("preview WS subscriber did not connect")
}
