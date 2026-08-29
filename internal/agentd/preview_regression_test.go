package agentd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
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
