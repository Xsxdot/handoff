// 本文件实现 preview_sessions 表的持久化边界。
//
// 职责：
//   - 以 SQLite 保存 owner preview session 的线下事实和生命周期时间
//   - 以条件更新提供 close/touch/expire 的幂等并发语义
//
// 边界：
//   - 仅持久化 preview_sessions，不拥有业务规则、事件发布或 HTTP 处理
//   - 不把 PreviewRecord 的内部 Source/LastActiveAt/ClosedAt 编码进 wire DTO
//   - 时间由调用方传入；本层只统一为 UTC RFC3339Nano 文本保存
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// PreviewSource identifies the owner-side source behind a preview session.
// Kind is "port" or "path"; path sources also carry the workspace root and
// workspace-relative path. Source is internal persistence data and never wire data.
type PreviewSource struct {
	Kind          string
	Port          int
	WorkspaceRoot string
	RelativePath  string
}

// PreviewRecord is the complete owner-side preview row.
// ClosedAt is nil for an active row. LastActiveAt is the idle-TTL clock and is
// deliberately separate from the session's CreatedAt wire field.
type PreviewRecord struct {
	Session      proto.PreviewSession
	Source       PreviewSource
	LastActiveAt time.Time
	ClosedAt     *time.Time
}

const previewColumns = `id, entry_url, via_json, cwd, origin_url, branch, created_at,
  ttl_seconds, source_kind, source_port, workspace_root, relative_path, last_active_at, closed_at`

func scanPreviewRow(sc rowScanner) (PreviewRecord, error) {
	var (
		row        PreviewRecord
		viaJSON    string
		createdAt  string
		lastActive string
		closedAt   sql.NullString
	)
	if err := sc.Scan(
		&row.Session.ID, &row.Session.EntryURL, &viaJSON, &row.Session.CWD,
		&row.Session.OriginURL, &row.Session.Branch, &createdAt, &row.Session.TTLSeconds,
		&row.Source.Kind, &row.Source.Port, &row.Source.WorkspaceRoot,
		&row.Source.RelativePath, &lastActive, &closedAt,
	); err != nil {
		return PreviewRecord{}, err
	}
	if viaJSON != "" {
		if err := json.Unmarshal([]byte(viaJSON), &row.Session.Via); err != nil {
			return PreviewRecord{}, fmt.Errorf("解析预览会话 %s allowlist: %w", row.Session.ID, err)
		}
	}
	row.Session.CreatedAt = parseTime(createdAt)
	row.LastActiveAt = parseTime(lastActive)
	if closedAt.Valid {
		closed := parseTime(closedAt.String)
		row.ClosedAt = &closed
	}
	return row, nil
}

// InsertPreview persists a complete owner preview row.
// The operation is not an upsert: a duplicate session ID is returned as a
// database error so the owner cannot publish an event for an unknown row.
func (s *Store) InsertPreview(row PreviewRecord) error {
	viaJSON, err := json.Marshal(row.Session.Via)
	if err != nil {
		return fmt.Errorf("序列化预览会话 %s allowlist: %w", row.Session.ID, err)
	}
	var closed any
	if row.ClosedAt != nil {
		closed = fmtTime(*row.ClosedAt)
	}
	_, err = s.db.ExecContext(context.Background(), `
INSERT INTO preview_sessions
  (id, entry_url, via_json, cwd, origin_url, branch, created_at, ttl_seconds,
   source_kind, source_port, workspace_root, relative_path, last_active_at, closed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.Session.ID, row.Session.EntryURL, string(viaJSON), row.Session.CWD,
		row.Session.OriginURL, row.Session.Branch, fmtTime(row.Session.CreatedAt),
		row.Session.TTLSeconds, row.Source.Kind, row.Source.Port, row.Source.WorkspaceRoot,
		row.Source.RelativePath, fmtTime(row.LastActiveAt), closed)
	if err != nil {
		return fmt.Errorf("写入预览会话 %s: %w", row.Session.ID, err)
	}
	return nil
}

// GetPreview reads one complete owner preview row.
// ErrNotFound is returned when id does not exist; closed rows are still
// readable because close/expire need to return the complete event payload.
func (s *Store) GetPreview(id string) (PreviewRecord, error) {
	row := s.db.QueryRowContext(context.Background(),
		`SELECT `+previewColumns+` FROM preview_sessions WHERE id = ?`, id)
	preview, err := scanPreviewRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PreviewRecord{}, fmt.Errorf("预览会话 %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return PreviewRecord{}, fmt.Errorf("读取预览会话 %s: %w", id, err)
	}
	return preview, nil
}

// ListActivePreviews returns active rows whose idle TTL has not elapsed.
// Time comparison is intentionally done with time.Time rather than SQLite
// text comparison because RFC3339Nano strings with and without fractional
// seconds do not have a reliable lexical ordering around the decimal point.
func (s *Store) ListActivePreviews(now time.Time) ([]PreviewRecord, error) {
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT `+previewColumns+` FROM preview_sessions WHERE closed_at IS NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("查询活动预览会话: %w", err)
	}
	defer rows.Close()
	active := []PreviewRecord{}
	for rows.Next() {
		row, err := scanPreviewRow(rows)
		if err != nil {
			return nil, fmt.Errorf("读取活动预览会话: %w", err)
		}
		if previewExpired(row, now) {
			continue
		}
		active = append(active, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历活动预览会话: %w", err)
	}
	return active, nil
}

func previewExpired(row PreviewRecord, now time.Time) bool {
	return !row.LastActiveAt.Add(time.Duration(row.Session.TTLSeconds) * time.Second).After(now)
}

// ClosePreview conditionally closes a row and returns the full row plus whether
// this call performed the transition. The read/update is one transaction so
// only the winner may publish preview.closed; repeated close is an idempotent
// no-op with the already-closed row.
func (s *Store) ClosePreview(id string, at time.Time) (PreviewRecord, bool, error) {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return PreviewRecord{}, false, fmt.Errorf("开始关闭预览会话 %s 事务: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(context.Background(),
		`SELECT `+previewColumns+` FROM preview_sessions WHERE id = ?`, id)
	preview, err := scanPreviewRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PreviewRecord{}, false, fmt.Errorf("预览会话 %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return PreviewRecord{}, false, fmt.Errorf("读取待关闭预览会话 %s: %w", id, err)
	}
	if preview.ClosedAt != nil {
		if err := tx.Commit(); err != nil {
			return PreviewRecord{}, false, fmt.Errorf("提交重复关闭预览会话 %s: %w", id, err)
		}
		return preview, false, nil
	}
	closedAt := at.UTC()
	result, err := tx.ExecContext(context.Background(),
		`UPDATE preview_sessions SET closed_at = ? WHERE id = ? AND closed_at IS NULL`, fmtTime(closedAt), id)
	if err != nil {
		return PreviewRecord{}, false, fmt.Errorf("关闭预览会话 %s: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return PreviewRecord{}, false, fmt.Errorf("读取关闭预览会话 %s 影响行数: %w", id, err)
	}
	if affected != 1 {
		if err := tx.Commit(); err != nil {
			return PreviewRecord{}, false, fmt.Errorf("提交并发关闭预览会话 %s: %w", id, err)
		}
		return preview, false, nil
	}
	preview.ClosedAt = &closedAt
	if err := tx.Commit(); err != nil {
		return PreviewRecord{}, false, fmt.Errorf("提交关闭预览会话 %s: %w", id, err)
	}
	return preview, true, nil
}

// TouchPreview renews the idle clock only while the row remains open.
// It returns false for a missing or already-closed row and never changes wire
// fields such as CreatedAt or TTLSeconds.
func (s *Store) TouchPreview(id string, at time.Time) (bool, error) {
	result, err := s.db.ExecContext(context.Background(),
		`UPDATE preview_sessions SET last_active_at = ? WHERE id = ? AND closed_at IS NULL`,
		fmtTime(at.UTC()), id)
	if err != nil {
		return false, fmt.Errorf("续命预览会话 %s: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("读取续命预览会话 %s 影响行数: %w", id, err)
	}
	return affected == 1, nil
}

// ExpirePreviews conditionally closes every idle row and returns only rows
// transitioned by this call. It uses the same transaction/conditional update
// rule as ClosePreview, preventing duplicate close events across sweepers.
func (s *Store) ExpirePreviews(now time.Time) ([]PreviewRecord, error) {
	now = now.UTC()
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, fmt.Errorf("开始过期预览会话事务: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(context.Background(),
		`SELECT `+previewColumns+` FROM preview_sessions WHERE closed_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("查询待过期预览会话: %w", err)
	}
	var candidates []PreviewRecord
	for rows.Next() {
		row, err := scanPreviewRow(rows)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("读取待过期预览会话: %w", err)
		}
		if previewExpired(row, now) {
			candidates = append(candidates, row)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("遍历待过期预览会话: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("关闭待过期预览会话查询: %w", err)
	}
	expired := make([]PreviewRecord, 0, len(candidates))
	for _, candidate := range candidates {
		closedAt := now
		result, err := tx.ExecContext(context.Background(),
			`UPDATE preview_sessions SET closed_at = ? WHERE id = ? AND closed_at IS NULL`,
			fmtTime(closedAt), candidate.Session.ID)
		if err != nil {
			return nil, fmt.Errorf("过期预览会话 %s: %w", candidate.Session.ID, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("读取过期预览会话 %s 影响行数: %w", candidate.Session.ID, err)
		}
		if affected == 1 {
			candidate.ClosedAt = &closedAt
			expired = append(expired, candidate)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交过期预览会话: %w", err)
	}
	return expired, nil
}

// UpdatePreviewEntry updates the owner-generated entry URL after restoring a
// path preview's static server. Closed or missing rows return ErrNotFound.
func (s *Store) UpdatePreviewEntry(id, entryURL string) error {
	result, err := s.db.ExecContext(context.Background(),
		`UPDATE preview_sessions SET entry_url = ? WHERE id = ? AND closed_at IS NULL`, entryURL, id)
	if err != nil {
		return fmt.Errorf("更新预览会话 %s entry_url: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取预览会话 %s entry_url 影响行数: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("预览会话 %s: %w", id, ErrNotFound)
	}
	return nil
}
