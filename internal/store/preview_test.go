// store preview session 测试：command 幂等、按 ID/nonce 读取、upsert 与过期清理。
//
// 职责：
//   - command_id 重复只返回原 session，不创建第二个 preview 身份
//   - 会话可按 preview_session_id 与 nonce 读取，未知返回 ErrNotFound
//   - upsert 持久化状态变更（closed）
//   - ExpirePreviewSessions 只把已过 expires_at 的 pending/active 会话标 expired
//
// 边界：
//   - 只验证 SQLite 事实，不启动 preview 代理进程
package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/workspaceapi"
)

func previewTestURL(nonce string) string {
	return fmt.Sprintf("http://127.0.0.1:8899/v1/preview-proxy/%s/", nonce)
}

func TestPreviewSessionCreateIdempotent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "preview.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	session := workspaceapi.PreviewSession{
		PreviewSessionID: "preview-1", WorkspaceID: "ws-1", MachineID: "machine-1",
		State: workspaceapi.PreviewStatePending, URL: previewTestURL("nonce-1"),
		Port: 3000, ExpiresAt: time.Now().Add(time.Hour),
	}
	created, inserted, err := s.CreatePreviewSession(ctx, "machine-1", "command-1", session)
	if err != nil {
		t.Fatal(err)
	}
	if !inserted || created.PreviewSessionID != session.PreviewSessionID || created.State != workspaceapi.PreviewStatePending {
		t.Fatalf("created=%+v inserted=%v err=%v", created, inserted, err)
	}

	duplicate := session
	duplicate.PreviewSessionID = "must-not-exist"
	duplicate.URL = previewTestURL("must-not-exist-nonce")
	got, inserted, err := s.CreatePreviewSession(ctx, "machine-1", "command-1", duplicate)
	if err != nil {
		t.Fatal(err)
	}
	if inserted || got.PreviewSessionID != "preview-1" {
		t.Fatalf("duplicate got=%+v inserted=%v err=%v", got, inserted, err)
	}
}

func TestPreviewSessionGetByIDAndNonce(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "preview.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	session := workspaceapi.PreviewSession{
		PreviewSessionID: "preview-2", WorkspaceID: "ws-2", MachineID: "machine-2",
		State: workspaceapi.PreviewStateActive, URL: previewTestURL("nonce-2"),
		Port: 3001, ExpiresAt: time.Now().Add(time.Hour),
	}
	if _, _, err := s.CreatePreviewSession(ctx, "machine-2", "command-2", session); err != nil {
		t.Fatal(err)
	}
	byID, err := s.GetPreviewSession(ctx, "preview-2")
	if err != nil || byID.PreviewSessionID != "preview-2" || byID.State != workspaceapi.PreviewStateActive ||
		byID.Port != 3001 || byID.WorkspaceID != "ws-2" || byID.MachineID != "machine-2" {
		t.Fatalf("byID=%+v err=%v", byID, err)
	}
	if byID.URL != session.URL {
		t.Fatalf("byID.URL=%q want %q", byID.URL, session.URL)
	}
	byNonce, err := s.GetPreviewSessionByNonce(ctx, "nonce-2")
	if err != nil || byNonce.PreviewSessionID != "preview-2" {
		t.Fatalf("byNonce=%+v err=%v", byNonce, err)
	}
	if _, err := s.GetPreviewSession(ctx, "nope"); err != ErrNotFound {
		t.Fatalf("GetPreviewSession(nope) err = %v, want ErrNotFound", err)
	}
	if _, err := s.GetPreviewSessionByNonce(ctx, "nope"); err != ErrNotFound {
		t.Fatalf("GetPreviewSessionByNonce(nope) err = %v, want ErrNotFound", err)
	}
}

func TestPreviewSessionUpsert(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "preview.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	session := workspaceapi.PreviewSession{
		PreviewSessionID: "preview-3", WorkspaceID: "ws-3", MachineID: "machine-3",
		State: workspaceapi.PreviewStatePending, URL: previewTestURL("nonce-3"),
		Port: 3002, ExpiresAt: time.Now().Add(time.Hour),
	}
	if _, _, err := s.CreatePreviewSession(ctx, "machine-3", "command-3", session); err != nil {
		t.Fatal(err)
	}
	session.State = workspaceapi.PreviewStateClosed
	if err := s.UpsertPreviewSession(ctx, "machine-3", session); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetPreviewSession(ctx, "preview-3")
	if err != nil || got.State != workspaceapi.PreviewStateClosed {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestPreviewSessionExpire(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "preview.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	past := workspaceapi.PreviewSession{
		PreviewSessionID: "preview-4", WorkspaceID: "ws-4", MachineID: "machine-4",
		State: workspaceapi.PreviewStateActive, URL: previewTestURL("nonce-4"),
		Port: 3003, ExpiresAt: time.Now().Add(-time.Hour),
	}
	if _, _, err := s.CreatePreviewSession(ctx, "machine-4", "command-4", past); err != nil {
		t.Fatal(err)
	}
	future := workspaceapi.PreviewSession{
		PreviewSessionID: "preview-5", WorkspaceID: "ws-5", MachineID: "machine-5",
		State: workspaceapi.PreviewStateActive, URL: previewTestURL("nonce-5"),
		Port: 3004, ExpiresAt: time.Now().Add(time.Hour),
	}
	if _, _, err := s.CreatePreviewSession(ctx, "machine-5", "command-5", future); err != nil {
		t.Fatal(err)
	}
	expired, err := s.ExpirePreviewSessions(ctx)
	if err != nil || expired != 1 {
		t.Fatalf("expired=%d err=%v", expired, err)
	}
	gotPast, err := s.GetPreviewSession(ctx, "preview-4")
	if err != nil || gotPast.State != workspaceapi.PreviewStateExpired {
		t.Fatalf("gotPast=%+v err=%v", gotPast, err)
	}
	gotFuture, err := s.GetPreviewSession(ctx, "preview-5")
	if err != nil || gotFuture.State != workspaceapi.PreviewStateActive {
		t.Fatalf("gotFuture=%+v err=%v", gotFuture, err)
	}
	if again, err := s.ExpirePreviewSessions(ctx); err != nil || again != 0 {
		t.Fatalf("again=%d err=%v", again, err)
	}
}
