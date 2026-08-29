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
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

type previewLauncherStub struct {
	mu          sync.Mutex
	starts      []PreviewLaunchSpec
	focuses     []int
	stopPIDs    []int
	startPIDs   []int
	findErr     error
	startErr    error
	focusErr    error
	stopPIDErr  error
	stopCalls   int
	done        chan error
	startHook   func()
	stopPIDHook func(int)
}

func (f *previewLauncherStub) FindExecutable(context.Context) (string, error) {
	return "/usr/bin/chromium", f.findErr
}
func (f *previewLauncherStub) Start(_ context.Context, _ string, spec PreviewLaunchSpec) (PreviewBrowserHandle, error) {
	f.mu.Lock()
	f.starts = append(f.starts, spec)
	pid := 42
	if index := len(f.starts) - 1; index < len(f.startPIDs) {
		pid = f.startPIDs[index]
	}
	startHook := f.startHook
	f.mu.Unlock()
	if f.startErr != nil {
		return PreviewBrowserHandle{}, f.startErr
	}
	if startHook != nil {
		startHook()
	}
	return PreviewBrowserHandle{PID: pid, Done: f.done}, nil
}
func (f *previewLauncherStub) Focus(_ context.Context, pid int) error {
	f.mu.Lock()
	f.focuses = append(f.focuses, pid)
	f.mu.Unlock()
	return f.focusErr
}
func (f *previewLauncherStub) StopPID(_ context.Context, pid int) error {
	f.mu.Lock()
	f.stopPIDs = append(f.stopPIDs, pid)
	stopPIDHook := f.stopPIDHook
	f.mu.Unlock()
	if stopPIDHook != nil {
		stopPIDHook(pid)
	}
	return f.stopPIDErr
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
	if spec.SessionID != session.ID || spec.EntryURL != session.EntryURL || spec.ProxyBypassList != "<-loopback>" || spec.PACPath == "" || spec.UserDataDir == "" || spec.ProxyNonce == "" || !strings.HasPrefix(spec.ProxyServer, "127.0.0.1:") {
		t.Fatalf("launch spec=%+v", spec)
	}
	args := previewLaunchArgs(spec)
	wantProxyArg := "--proxy-server=socks5://handoff-preview:" + spec.ProxyNonce + "@" + spec.ProxyServer
	foundProxyArg := false
	for _, arg := range args {
		if arg == wantProxyArg {
			foundProxyArg = true
			break
		}
	}
	if !foundProxyArg {
		t.Fatalf("launch args missing nonce auth: %v", args)
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
	close(launcher.done)
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

func TestPreviewOpenServiceTouchFailureStopsPIDBeforeCleanup(t *testing.T) {
	_, owner := newPreviewOwnerEnv(t)
	session, err := owner.Create(context.Background(), protoPreviewPortReq())
	if err != nil {
		t.Fatalf("create owner session: %v", err)
	}
	done := make(chan error)
	stopPIDCalled := make(chan struct{})
	launcher := &previewLauncherStub{done: done}
	launcher.startHook = func() {
		if _, _, err := owner.st.ClosePreview(session.ID, owner.deps.Now()); err != nil {
			t.Errorf("close owner session in start hook: %v", err)
		}
	}
	launcher.stopPIDHook = func(pid int) {
		if pid != 42 {
			t.Errorf("stopped pid=%d, want 42", pid)
		}
		close(stopPIDCalled)
	}
	service := NewPreviewOpenService(owner, nil, nil, launcher, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer func() {
		select {
		case <-done:
		default:
			close(done)
		}
		_ = service.Stop(context.Background())
	}()

	result := make(chan error, 1)
	go func() {
		resp, err := service.OpenPreview(context.Background(), session.ID, "")
		if resp == nil || resp.Opened {
			result <- errors.New("touch failure reported opened")
			return
		}
		result <- err
	}()
	select {
	case <-stopPIDCalled:
	case <-time.After(time.Second):
		t.Fatal("Touch failure did not stop browser PID")
	}
	launcher.mu.Lock()
	profile := launcher.starts[0].UserDataDir
	launcher.mu.Unlock()
	if _, err := os.Stat(profile); err != nil {
		t.Fatalf("profile removed before browser exit: %v", err)
	}
	close(done)
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "续命 preview 会话") {
			t.Fatalf("open error=%v, want Touch failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("open did not return after browser exit")
	}
	waitForPreviewCondition(t, func() bool {
		_, err := os.Stat(profile)
		return errors.Is(err, os.ErrNotExist)
	})
}

func TestPreviewOpenServiceStopWaitsForBrowserExit(t *testing.T) {
	_, owner := newPreviewOwnerEnv(t)
	session, err := owner.Create(context.Background(), protoPreviewPortReq())
	if err != nil {
		t.Fatalf("create owner session: %v", err)
	}
	done := make(chan error)
	launcher := &previewLauncherStub{done: done}
	service := NewPreviewOpenService(owner, nil, nil, launcher, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if resp, err := service.OpenPreview(context.Background(), session.ID, ""); err != nil || resp == nil || !resp.Opened {
		t.Fatalf("open preview resp=%+v err=%v", resp, err)
	}
	finished := make(chan error, 1)
	go func() { finished <- service.Stop(context.Background()) }()
	select {
	case err := <-finished:
		t.Fatalf("stop returned before browser exit: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(done)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("stop after browser exit: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stop did not wait for browser exit")
	}
}

func TestPreviewOpenServiceOwnerCloseStopsPIDBeforeCleanup(t *testing.T) {
	_, owner := newPreviewOwnerEnv(t)
	session, err := owner.Create(context.Background(), protoPreviewPortReq())
	if err != nil {
		t.Fatalf("create owner session: %v", err)
	}
	done := make(chan error)
	launcher := &previewLauncherStub{done: done}
	service := NewPreviewOpenService(owner, nil, nil, launcher, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer func() {
		select {
		case <-done:
		default:
			close(done)
		}
		_ = service.Stop(context.Background())
	}()
	if resp, err := service.OpenPreview(context.Background(), session.ID, ""); err != nil || resp == nil || !resp.Opened {
		t.Fatalf("open preview resp=%+v err=%v", resp, err)
	}
	launcher.mu.Lock()
	profile := launcher.starts[0].UserDataDir
	launcher.mu.Unlock()
	if _, err := owner.Close(context.Background(), session.ID); err != nil {
		t.Fatalf("close owner session: %v", err)
	}
	waitForPreviewCondition(t, func() bool {
		launcher.mu.Lock()
		defer launcher.mu.Unlock()
		return len(launcher.stopPIDs) == 1 && launcher.stopPIDs[0] == 42
	})
	if _, err := os.Stat(profile); err != nil {
		t.Fatalf("profile removed before browser exit: %v", err)
	}
	close(done)
	waitForPreviewCondition(t, func() bool {
		_, err := os.Stat(profile)
		return errors.Is(err, os.ErrNotExist)
	})
}

func TestPreviewOpenServiceOwnerExpireStopsPIDBeforeCleanup(t *testing.T) {
	_, owner := newPreviewOwnerEnv(t)
	session, err := owner.Create(context.Background(), protoPreviewPortReq())
	if err != nil {
		t.Fatalf("create owner session: %v", err)
	}
	done := make(chan error)
	launcher := &previewLauncherStub{done: done}
	service := NewPreviewOpenService(owner, nil, nil, launcher, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer func() {
		select {
		case <-done:
		default:
			close(done)
		}
		_ = service.Stop(context.Background())
	}()
	if resp, err := service.OpenPreview(context.Background(), session.ID, ""); err != nil || resp == nil || !resp.Opened {
		t.Fatalf("open preview resp=%+v err=%v", resp, err)
	}
	launcher.mu.Lock()
	profile := launcher.starts[0].UserDataDir
	launcher.mu.Unlock()
	if _, err := owner.st.TouchPreview(session.ID, owner.deps.Now().Add(-time.Duration(previewTTLSeconds)*time.Second)); err != nil {
		t.Fatalf("age preview session: %v", err)
	}
	if err := owner.Expire(context.Background()); err != nil {
		t.Fatalf("expire owner session: %v", err)
	}
	waitForPreviewCondition(t, func() bool {
		launcher.mu.Lock()
		defer launcher.mu.Unlock()
		return len(launcher.stopPIDs) == 1 && launcher.stopPIDs[0] == 42
	})
	if _, err := os.Stat(profile); err != nil {
		t.Fatalf("profile removed before browser exit: %v", err)
	}
	close(done)
	waitForPreviewCondition(t, func() bool {
		_, err := os.Stat(profile)
		return errors.Is(err, os.ErrNotExist)
	})
}

func TestPreviewOpenServiceUsesMirrorHubForRemoteClose(t *testing.T) {
	_, owner := newPreviewOwnerEnv(t)
	mirrorHub := NewPreviewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	mirror := NewPreviewMirror(nil, owner, mirrorHub, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	session := proto.PreviewSession{ID: "preview-remote", EntryURL: "http://localhost:5173"}
	mirror.mu.Lock()
	mirror.sessions[previewSessionKey("devbox", session.ID)] = session
	mirror.mu.Unlock()
	done := make(chan error)
	launcher := &previewLauncherStub{done: done}
	service := NewPreviewOpenService(owner, mirror, nil, launcher, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	mirror.applyRemoteEvent("devbox", proto.PreviewEvent{Type: proto.PreviewEventClosed, Session: session})
	waitForPreviewCondition(t, func() bool {
		launcher.mu.Lock()
		defer launcher.mu.Unlock()
		return len(launcher.stopPIDs) == 1 && launcher.stopPIDs[0] == 42
	})
	if _, err := os.Stat(profile); err != nil {
		t.Fatalf("profile removed before remote browser exit: %v", err)
	}
	close(done)
	waitForPreviewCondition(t, func() bool {
		_, err := os.Stat(profile)
		return errors.Is(err, os.ErrNotExist)
	})
}

func TestPreviewOpenServiceClosedEventsDoNotBlockOnFirstBrowser(t *testing.T) {
	_, owner := newPreviewOwnerEnv(t)
	first, err := owner.Create(context.Background(), protoPreviewPortReq())
	if err != nil {
		t.Fatalf("create first owner session: %v", err)
	}
	owner.deps.NewID = func() string { return "preview-second" }
	second, err := owner.Create(context.Background(), protoPreviewPortReq())
	if err != nil {
		t.Fatalf("create second owner session: %v", err)
	}
	done := make(chan error)
	launcher := &previewLauncherStub{done: done, startPIDs: []int{101, 102}}
	service := NewPreviewOpenService(owner, nil, nil, launcher, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer func() {
		select {
		case <-done:
		default:
			close(done)
		}
		_ = service.Stop(context.Background())
	}()
	for _, session := range []*proto.PreviewSession{first, second} {
		if resp, err := service.OpenPreview(context.Background(), session.ID, ""); err != nil || resp == nil || !resp.Opened {
			t.Fatalf("open session=%s resp=%+v err=%v", session.ID, resp, err)
		}
	}
	if _, err := owner.Close(context.Background(), first.ID); err != nil {
		t.Fatalf("close first owner session: %v", err)
	}
	waitForPreviewCondition(t, func() bool {
		launcher.mu.Lock()
		defer launcher.mu.Unlock()
		return len(launcher.stopPIDs) == 1 && launcher.stopPIDs[0] == 101
	})
	if _, err := owner.Close(context.Background(), second.ID); err != nil {
		t.Fatalf("close second owner session: %v", err)
	}
	waitForPreviewCondition(t, func() bool {
		launcher.mu.Lock()
		defer launcher.mu.Unlock()
		if len(launcher.stopPIDs) != 2 {
			return false
		}
		return (launcher.stopPIDs[0] == 101 && launcher.stopPIDs[1] == 102) ||
			(launcher.stopPIDs[0] == 102 && launcher.stopPIDs[1] == 101)
	})
}

func waitForPreviewCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("preview condition did not become true")
}

func protoPreviewPortReq() proto.PreviewOpenReq { return proto.PreviewOpenReq{Port: 5173} }
