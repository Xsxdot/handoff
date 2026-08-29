package agentd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

type previewLauncherStub struct {
	mu        sync.Mutex
	starts    []PreviewLaunchSpec
	focuses   []int
	findErr   error
	startErr  error
	focusErr  error
	stopCalls int
	done      chan error
}

func (f *previewLauncherStub) FindExecutable(context.Context) (string, error) {
	return "/usr/bin/chromium", f.findErr
}
func (f *previewLauncherStub) Start(_ context.Context, _ string, spec PreviewLaunchSpec) (PreviewBrowserHandle, error) {
	f.mu.Lock()
	f.starts = append(f.starts, spec)
	f.mu.Unlock()
	if f.startErr != nil {
		return PreviewBrowserHandle{}, f.startErr
	}
	return PreviewBrowserHandle{PID: 42, Done: f.done}, nil
}
func (f *previewLauncherStub) Focus(_ context.Context, pid int) error {
	f.mu.Lock()
	f.focuses = append(f.focuses, pid)
	f.mu.Unlock()
	return f.focusErr
}
func (f *previewLauncherStub) Stop(context.Context) error {
	f.mu.Lock()
	f.stopCalls++
	f.mu.Unlock()
	return nil
}

func TestPreviewOpenServiceLaunchesIsolatedChromiumAndFocusesExisting(t *testing.T) {
	env, owner := newPreviewOwnerEnv(t)
	session, err := owner.Create(context.Background(), protoPreviewPortReq())
	if err != nil {
		t.Fatalf("create owner session: %v", err)
	}
	launcher := &previewLauncherStub{done: make(chan error)}
	service := NewPreviewOpenService(owner, nil, nil, launcher, slog.New(slog.NewTextHandler(io.Discard, nil)))
	resp, err := service.OpenPreview(context.Background(), session.ID, "")
	if err != nil || resp == nil || !resp.Opened {
		t.Fatalf("first open resp=%+v err=%v", resp, err)
	}
	launcher.mu.Lock()
	if len(launcher.starts) != 1 {
		t.Fatalf("starts=%d", len(launcher.starts))
	}
	spec := launcher.starts[0]
	launcher.mu.Unlock()
	if spec.SessionID != session.ID || spec.EntryURL != session.EntryURL || spec.ProxyBypassList != "<-loopback>" || spec.PACPath == "" || spec.UserDataDir == "" || !strings.HasPrefix(spec.ProxyServer, "127.0.0.1:") {
		t.Fatalf("launch spec=%+v", spec)
	}
	resp, err = service.OpenPreview(context.Background(), session.ID, "")
	if err != nil || resp == nil || !resp.Opened {
		t.Fatalf("second open resp=%+v err=%v", resp, err)
	}
	launcher.mu.Lock()
	starts, focuses := len(launcher.starts), len(launcher.focuses)
	launcher.mu.Unlock()
	if starts != 1 || focuses != 1 {
		t.Fatalf("starts=%d focuses=%d", starts, focuses)
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	launcher.mu.Lock()
	stopCalls := launcher.stopCalls
	launcher.mu.Unlock()
	if stopCalls != 1 {
		t.Fatalf("stop calls=%d", stopCalls)
	}
	_ = env
}

func TestPreviewOpenServiceFailureDoesNotReportOpened(t *testing.T) {
	_, owner := newPreviewOwnerEnv(t)
	session, err := owner.Create(context.Background(), protoPreviewPortReq())
	if err != nil {
		t.Fatalf("create owner session: %v", err)
	}
	launcher := &previewLauncherStub{findErr: context.Canceled, done: make(chan error)}
	service := NewPreviewOpenService(owner, nil, nil, launcher, slog.New(slog.NewTextHandler(io.Discard, nil)))
	resp, err := service.OpenPreview(context.Background(), session.ID, "")
	if err == nil || resp == nil || resp.Opened {
		t.Fatalf("failure resp=%+v err=%v", resp, err)
	}
	launcher.mu.Lock()
	stopCalls := launcher.stopCalls
	launcher.mu.Unlock()
	if stopCalls != 0 {
		t.Fatalf("launcher stop calls=%d before browser start", stopCalls)
	}
}

func TestPreviewOpenServiceStartFailureCleansProfile(t *testing.T) {
	_, owner := newPreviewOwnerEnv(t)
	session, err := owner.Create(context.Background(), protoPreviewPortReq())
	if err != nil {
		t.Fatalf("create owner session: %v", err)
	}
	launcher := &previewLauncherStub{startErr: errors.New("browser refused"), done: make(chan error)}
	service := NewPreviewOpenService(owner, nil, nil, launcher, slog.New(slog.NewTextHandler(io.Discard, nil)))
	resp, err := service.OpenPreview(context.Background(), session.ID, "")
	if err == nil || resp == nil || resp.Opened {
		t.Fatalf("failure resp=%+v err=%v", resp, err)
	}
	launcher.mu.Lock()
	if len(launcher.starts) != 1 {
		launcher.mu.Unlock()
		t.Fatalf("starts=%d", len(launcher.starts))
	}
	profile := launcher.starts[0].UserDataDir
	launcher.mu.Unlock()
	if _, statErr := os.Stat(profile); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("profile stat err=%v, want not exist", statErr)
	}
	_ = service.Stop(context.Background())
}

func protoPreviewPortReq() proto.PreviewOpenReq { return proto.PreviewOpenReq{Port: 5173} }
