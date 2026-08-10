// store preview session 持久化：command 幂等、nonce 唯一与批量过期。
//
// 职责：
//   - command_id 幂等创建稳定 preview session
//   - 按 preview_session_id / nonce 读取
//   - 状态变更（closed/expired）upsert
//   - 过期清理把 pending/active 且已过 expires_at 的会话标 expired
//
// 边界：
//   - 不启动或终止 preview 代理进程；代理本体由 owner 内存持有
//   - 业务状态机由 previewservice 负责；本层只做原子持久化
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/xushixin/handoff/internal/workspaceapi"
)

// previewNonceFromURL 从 owner-loopback URL 中提取代理 nonce。
// URL 形如 http://127.0.0.1:<agentd-port>/v1/preview-proxy/<nonce>/；
// PreviewSession 不携带 nonce 字段，nonce 是 URL 的路径段，唯一约束以它为准。
func previewNonceFromURL(rawURL string) string {
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

// CreatePreviewSession 幂等创建 preview session；command_id 重复时返回已存在的会话。
func (s *Store) CreatePreviewSession(ctx context.Context, machineID, commandID string,
	session workspaceapi.PreviewSession) (workspaceapi.PreviewSession, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return workspaceapi.PreviewSession{}, false, fmt.Errorf("开启 preview 创建事务: %w", err)
	}
	defer tx.Rollback()
	now := fmtTime(time.Now())
	result, err := tx.ExecContext(ctx, `
INSERT INTO preview_sessions (preview_session_id, command_id, machine_id, workspace_id,
  nonce, port, state, url, expires_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(command_id) DO NOTHING`, session.PreviewSessionID, commandID, machineID,
		session.WorkspaceID, previewNonceFromURL(session.URL), session.Port, string(session.State),
		session.URL, fmtTime(session.ExpiresAt), now, now)
	if err != nil {
		return workspaceapi.PreviewSession{}, false, fmt.Errorf("创建 preview session %s: %w", session.PreviewSessionID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return workspaceapi.PreviewSession{}, false, fmt.Errorf("读取 preview 创建结果: %w", err)
	}
	if rows == 0 {
		existing, err := getPreviewSessionByCommandIDTx(ctx, tx, commandID)
		if err != nil {
			return workspaceapi.PreviewSession{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return workspaceapi.PreviewSession{}, false, fmt.Errorf("提交 preview 幂等事务: %w", err)
		}
		return existing, false, nil
	}
	if err := tx.Commit(); err != nil {
		return workspaceapi.PreviewSession{}, false, fmt.Errorf("提交 preview 创建事务: %w", err)
	}
	return session, true, nil
}

// GetPreviewSession 按稳定 preview session ID 读取。
func (s *Store) GetPreviewSession(ctx context.Context, id string) (workspaceapi.PreviewSession, error) {
	return scanPreviewSession(s.db.QueryRowContext(ctx, previewSelect+" WHERE preview_session_id = ?", id))
}

// GetPreviewSessionByNonce 按 nonce 读取；代理 nonce 唯一，用于反向解析会话。
func (s *Store) GetPreviewSessionByNonce(ctx context.Context, nonce string) (workspaceapi.PreviewSession, error) {
	return scanPreviewSession(s.db.QueryRowContext(ctx, previewSelect+" WHERE nonce = ?", nonce))
}

// UpsertPreviewSession 按 preview_session_id 幂等 upsert 会话元数据。
func (s *Store) UpsertPreviewSession(ctx context.Context, machineID string, session workspaceapi.PreviewSession) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启 preview upsert 事务: %w", err)
	}
	defer tx.Rollback()
	if err := upsertPreviewSessionTx(ctx, tx, machineID, session); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 preview upsert 事务: %w", err)
	}
	return nil
}

// ExpirePreviewSessions 把已过 expires_at 的 pending/active 会话标 expired；返回更新行数。
func (s *Store) ExpirePreviewSessions(ctx context.Context) (int, error) {
	now := fmtTime(time.Now())
	result, err := s.db.ExecContext(ctx, `
UPDATE preview_sessions SET state = 'expired', updated_at = ?
WHERE state IN ('pending','active') AND expires_at < ?`, now, now)
	if err != nil {
		return 0, fmt.Errorf("过期 preview sessions: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("读取 preview 过期结果: %w", err)
	}
	return int(rows), nil
}

const previewSelect = `SELECT preview_session_id, workspace_id, machine_id, state, url,
  port, expires_at FROM preview_sessions`

func scanPreviewSession(row rowScanner) (workspaceapi.PreviewSession, error) {
	var (
		session   workspaceapi.PreviewSession
		state     string
		expiresAt string
	)
	if err := row.Scan(&session.PreviewSessionID, &session.WorkspaceID, &session.MachineID,
		&state, &session.URL, &session.Port, &expiresAt); err != nil {
		if err == sql.ErrNoRows {
			return workspaceapi.PreviewSession{}, ErrNotFound
		}
		return workspaceapi.PreviewSession{}, fmt.Errorf("读取 preview session: %w", err)
	}
	session.State = workspaceapi.PreviewState(state)
	session.ExpiresAt = parseTime(expiresAt)
	return session, nil
}

func getPreviewSessionByCommandIDTx(ctx context.Context, tx *sql.Tx, commandID string) (workspaceapi.PreviewSession, error) {
	return scanPreviewSession(tx.QueryRowContext(ctx, previewSelect+" WHERE command_id = ?", commandID))
}

func upsertPreviewSessionTx(ctx context.Context, tx *sql.Tx, machineID string,
	session workspaceapi.PreviewSession) error {
	now := fmtTime(time.Now())
	if _, err := tx.ExecContext(ctx, `
INSERT INTO preview_sessions (preview_session_id, command_id, machine_id, workspace_id,
  nonce, port, state, url, expires_at, created_at, updated_at)
VALUES (?, NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(preview_session_id) DO UPDATE SET machine_id = excluded.machine_id,
  workspace_id = excluded.workspace_id, nonce = excluded.nonce, port = excluded.port,
  state = excluded.state, url = excluded.url, expires_at = excluded.expires_at,
  updated_at = excluded.updated_at`,
		session.PreviewSessionID, machineID, session.WorkspaceID, previewNonceFromURL(session.URL),
		session.Port, string(session.State), session.URL, fmtTime(session.ExpiresAt), now, now); err != nil {
		return fmt.Errorf("upsert preview session %s: %w", session.PreviewSessionID, err)
	}
	return nil
}
