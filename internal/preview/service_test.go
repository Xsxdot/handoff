// preview service 测试：owner-side Preview 会话生命周期。
//
// 职责：
//   - 覆盖 ValidatePort 端口边界校验
//   - 覆盖 Create 绑定字段、command_id 幂等与 nonce 不可预测
//   - 覆盖过期会话在 Get/LookupNonce 都返回 ResourceNotFound
//   - 覆盖 Close 把会话置为 closed 并持久化
//
// 边界：
//   - 使用内存 stub repository，不依赖 SQLite
//   - 不启动 HTTP server 或 preview 代理进程
package preview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/store"
	"github.com/xushixin/handoff/internal/workspaceapi"
)

// stubRepo 内存 repository：byID 为主键，byCommand/byNonce 提供幂等与反向解析。
type stubRepo struct {
	byID      map[string]workspaceapi.PreviewSession
	byCommand map[string]string
	byNonce   map[string]string
}

func newStubRepo() *stubRepo {
	return &stubRepo{
		byID:      make(map[string]workspaceapi.PreviewSession),
		byCommand: make(map[string]string),
		byNonce:   make(map[string]string),
	}
}

// nonceFromURL 提取 /v1/preview-proxy/<nonce>/ 中的 nonce。
func nonceFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, part := range parts {
		if part == "preview-proxy" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func (r *stubRepo) CreatePreviewSession(_ context.Context, machineID, commandID string,
	session workspaceapi.PreviewSession) (workspaceapi.PreviewSession, bool, error) {
	if id, ok := r.byCommand[commandID]; ok {
		return r.byID[id], false, nil
	}
	session.MachineID = machineID
	r.byID[session.PreviewSessionID] = session
	r.byCommand[commandID] = session.PreviewSessionID
	if nonce := nonceFromURL(session.URL); nonce != "" {
		r.byNonce[nonce] = session.PreviewSessionID
	}
	return session, true, nil
}

func (r *stubRepo) GetPreviewSession(_ context.Context, id string) (workspaceapi.PreviewSession, error) {
	session, ok := r.byID[id]
	if !ok {
		return workspaceapi.PreviewSession{}, store.ErrNotFound
	}
	return session, nil
}

func (r *stubRepo) GetPreviewSessionByNonce(_ context.Context, nonce string) (workspaceapi.PreviewSession, error) {
	id, ok := r.byNonce[nonce]
	if !ok {
		return workspaceapi.PreviewSession{}, store.ErrNotFound
	}
	return r.byID[id], nil
}

func (r *stubRepo) UpsertPreviewSession(_ context.Context, machineID string, session workspaceapi.PreviewSession) error {
	session.MachineID = machineID
	r.byID[session.PreviewSessionID] = session
	if nonce := nonceFromURL(session.URL); nonce != "" {
		r.byNonce[nonce] = session.PreviewSessionID
	}
	return nil
}

func (r *stubRepo) ExpirePreviewSessions(context.Context) (int, error) {
	count := 0
	for id, session := range r.byID {
		if (session.State == workspaceapi.PreviewStatePending || session.State == workspaceapi.PreviewStateActive) &&
			session.ExpiresAt.Before(time.Now()) {
			session.State = workspaceapi.PreviewStateExpired
			r.byID[id] = session
			count++
		}
	}
	return count, nil
}

func stubIDGen() (func() string, func() string) {
	var id, nonce int
	return func() string { id++; return fmt.Sprintf("preview-%d", id) },
		func() string { nonce++; return fmt.Sprintf("nonce-%d", nonce) }
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestValidatePort(t *testing.T) {
	tests := []struct {
		name    string
		port    int
		wantErr bool
	}{
		{"zero", 0, true},
		{"negative", -42, true},
		{"above-max", 65536, true},
		{"min-ok", 1, false},
		{"max-ok", 65535, false},
		{"typical-ok", 8080, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePort(tt.port)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("ValidatePort(%d) = %v, want nil", tt.port, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidatePort(%d) = nil, want error", tt.port)
			}
			var apiErr *workspaceapi.Error
			if !errors.As(err, &apiErr) {
				t.Fatalf("ValidatePort(%d) = %v, want typed workspaceapi.Error", tt.port, err)
			}
			if apiErr.Code != workspaceapi.ErrorCommandConflict {
				t.Fatalf("ValidatePort(%d) code = %q, want %q", tt.port, apiErr.Code, workspaceapi.ErrorCommandConflict)
			}
		})
	}
}

func TestCreateBindsSession(t *testing.T) {
	repo := newStubRepo()
	newID, newNonce := stubIDGen()
	service := newService(repo, "machine-1", "http://127.0.0.1:8899", discardLogger(), time.Hour, newID, newNonce)

	created, err := service.Create(context.Background(),
		workspaceapi.WorkspaceRef{WorkspaceID: "ws-1", MachineID: "machine-1", RootPath: "/tmp/ws-1"},
		workspaceapi.CreatePreviewCommand{CommandID: "cmd-1", Port: 8080})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.PreviewSessionID == "" {
		t.Fatal("PreviewSessionID 为空")
	}
	if created.WorkspaceID != "ws-1" {
		t.Fatalf("WorkspaceID = %q, want ws-1", created.WorkspaceID)
	}
	if created.MachineID != "machine-1" {
		t.Fatalf("MachineID = %q, want machine-1", created.MachineID)
	}
	if created.State != workspaceapi.PreviewStatePending {
		t.Fatalf("State = %q, want pending", created.State)
	}
	if created.Port != 8080 {
		t.Fatalf("Port = %d, want 8080", created.Port)
	}
	if !created.ExpiresAt.After(time.Now()) {
		t.Fatalf("ExpiresAt = %v, want future", created.ExpiresAt)
	}
	pattern := regexp.MustCompile(`^http://127\.0\.0\.1:\d+/v1/preview-proxy/[^/]+/$`)
	if !pattern.MatchString(created.URL) {
		t.Fatalf("URL = %q, 不匹配 %s", created.URL, pattern)
	}
	if nonce := nonceFromURL(created.URL); nonce == "" {
		t.Fatalf("URL = %q, 缺少 nonce 路径段", created.URL)
	}
}

func TestCreateIdempotentByCommandID(t *testing.T) {
	repo := newStubRepo()
	newID, newNonce := stubIDGen()
	service := newService(repo, "machine-1", "http://127.0.0.1:8899", discardLogger(), time.Hour, newID, newNonce)
	ws := workspaceapi.WorkspaceRef{WorkspaceID: "ws-1", MachineID: "machine-1", RootPath: "/tmp/ws-1"}
	cmd := workspaceapi.CreatePreviewCommand{CommandID: "cmd-1", Port: 8080}

	first, err := service.Create(context.Background(), ws, cmd)
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	second, err := service.Create(context.Background(), ws, cmd)
	if err != nil {
		t.Fatalf("second Create: %v", err)
	}
	if second.PreviewSessionID != first.PreviewSessionID {
		t.Fatalf("idempotent create 改变 id: %q -> %q", first.PreviewSessionID, second.PreviewSessionID)
	}
	if second.URL != first.URL {
		t.Fatalf("idempotent create 改变 url: %q -> %q", first.URL, second.URL)
	}
	stored, err := repo.GetPreviewSession(context.Background(), first.PreviewSessionID)
	if err != nil {
		t.Fatalf("Get stored: %v", err)
	}
	if stored.URL != first.URL {
		t.Fatalf("stored url = %q, want %q", stored.URL, first.URL)
	}
}

func TestCreateNonceUnpredictable(t *testing.T) {
	repo := newStubRepo()
	newID, newNonce := stubIDGen()
	service := newService(repo, "machine-1", "http://127.0.0.1:8899", discardLogger(), time.Hour, newID, newNonce)
	ws := workspaceapi.WorkspaceRef{WorkspaceID: "ws-1", MachineID: "machine-1", RootPath: "/tmp/ws-1"}

	first, err := service.Create(context.Background(), ws, workspaceapi.CreatePreviewCommand{CommandID: "cmd-1", Port: 8080})
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	second, err := service.Create(context.Background(), ws, workspaceapi.CreatePreviewCommand{CommandID: "cmd-2", Port: 9090})
	if err != nil {
		t.Fatalf("second Create: %v", err)
	}
	if first.PreviewSessionID == second.PreviewSessionID {
		t.Fatal("两次创建 preview session id 相同")
	}
	if first.URL == second.URL {
		t.Fatal("两次创建 URL 相同")
	}
	if nonceFromURL(first.URL) == nonceFromURL(second.URL) {
		t.Fatal("两次创建 nonce 相同")
	}
}

func isResourceNotFound(t *testing.T, err error) bool {
	t.Helper()
	if err == nil {
		return false
	}
	var apiErr *workspaceapi.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Code == workspaceapi.ErrorResourceNotFound
}

func TestGetExpiredReturnsResourceNotFound(t *testing.T) {
	repo := newStubRepo()
	newID, newNonce := stubIDGen()
	service := newService(repo, "machine-1", "http://127.0.0.1:8899", discardLogger(), time.Nanosecond, newID, newNonce)

	created, err := service.Create(context.Background(),
		workspaceapi.WorkspaceRef{WorkspaceID: "ws-1", MachineID: "machine-1", RootPath: "/tmp/ws-1"},
		workspaceapi.CreatePreviewCommand{CommandID: "cmd-1", Port: 8080})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	time.Sleep(2 * time.Millisecond)

	if _, err := service.Get(context.Background(), created.PreviewSessionID); !isResourceNotFound(t, err) {
		t.Fatalf("Get = %v, want ErrorResourceNotFound", err)
	}
	if _, err := service.LookupNonce(context.Background(), nonceFromURL(created.URL)); !isResourceNotFound(t, err) {
		t.Fatalf("LookupNonce = %v, want ErrorResourceNotFound", err)
	}
}

func TestCloseSetsClosedState(t *testing.T) {
	repo := newStubRepo()
	newID, newNonce := stubIDGen()
	service := newService(repo, "machine-1", "http://127.0.0.1:8899", discardLogger(), time.Hour, newID, newNonce)

	created, err := service.Create(context.Background(),
		workspaceapi.WorkspaceRef{WorkspaceID: "ws-1", MachineID: "machine-1", RootPath: "/tmp/ws-1"},
		workspaceapi.CreatePreviewCommand{CommandID: "cmd-1", Port: 8080})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	closed, err := service.Close(context.Background(), created.PreviewSessionID)
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if closed.State != workspaceapi.PreviewStateClosed {
		t.Fatalf("Close state = %q, want closed", closed.State)
	}
	stored, err := repo.GetPreviewSession(context.Background(), created.PreviewSessionID)
	if err != nil {
		t.Fatalf("Get stored: %v", err)
	}
	if stored.State != workspaceapi.PreviewStateClosed {
		t.Fatalf("stored state = %q, want closed", stored.State)
	}
	if stored.MachineID != "machine-1" {
		t.Fatalf("stored machine_id = %q, want machine-1", stored.MachineID)
	}
}
