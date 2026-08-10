// store control_events.go：全局控制事件（control_revision）存储。
//
// 职责：
//   - ControlEventsAfter：按 revision 升序拉取控制事件（control stream 重放用）
//   - appendControlEventTx：资源写事务内追加完整 upsert 事件
//
// 边界：
//   - 写入只开放给 store 内部事务，不开放给 handler 层
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/xushixin/handoff/internal/controlplane"
)

// appendControlEventTx 在调用方事务中分配下一 revision 并写入完整 upsert payload。
// 所有资源写与事件写必须共用同一事务，避免桌面看到不存在的资源或漏掉已提交资源。
func appendControlEventTx(ctx context.Context, tx *sql.Tx, kind controlplane.ControlEventKind,
	resourceID string, value any) (controlplane.ControlEvent, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return controlplane.ControlEvent{}, fmt.Errorf("序列化 %s control payload: %w", kind, err)
	}
	var revision int64
	if err := tx.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(control_revision), 0) + 1 FROM control_events").Scan(&revision); err != nil {
		return controlplane.ControlEvent{}, fmt.Errorf("读取下一控制面 revision: %w", err)
	}
	createdAt := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO control_events (control_revision, kind, resource_id, payload, created_at)
VALUES (?, ?, ?, ?, ?)`, revision, string(kind), resourceID, string(payload), fmtTime(createdAt)); err != nil {
		return controlplane.ControlEvent{}, fmt.Errorf("追加 %s control event: %w", kind, err)
	}
	return controlplane.ControlEvent{
		ControlRevision: revision, Kind: kind, ResourceID: resourceID,
		Payload: payload, CreatedAt: createdAt,
	}, nil
}

// ControlEventsAfter 返回 revision 之后的 control events，升序，最多 limit 条。
func (s *Store) ControlEventsAfter(_ context.Context, afterRevision int64, limit int) ([]controlplane.ControlEvent, error) {
	rows, err := s.db.QueryContext(context.Background(), `
SELECT control_revision, kind, resource_id, payload, created_at
FROM control_events WHERE control_revision > ? ORDER BY control_revision ASC LIMIT ?`,
		afterRevision, limit)
	if err != nil {
		return nil, fmt.Errorf("查询控制事件: %w", err)
	}
	defer rows.Close()
	var out []controlplane.ControlEvent
	for rows.Next() {
		var (
			ev        controlplane.ControlEvent
			kind      string
			payload   string
			createdAt string
		)
		if err := rows.Scan(&ev.ControlRevision, &kind, &ev.ResourceID, &payload, &createdAt); err != nil {
			return nil, fmt.Errorf("读取控制事件行: %w", err)
		}
		ev.Kind = controlplane.ControlEventKind(kind)
		ev.Payload = json.RawMessage(payload)
		ev.CreatedAt = parseTime(createdAt)
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历控制事件: %w", err)
	}
	return out, nil
}

// LatestControlRevision 返回当前最大 control_revision（0=尚无事件）。
func (s *Store) LatestControlRevision(ctx context.Context) (int64, error) {
	var rev int64
	if err := s.db.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(control_revision), 0) FROM control_events").Scan(&rev); err != nil {
		return 0, fmt.Errorf("读取控制面 revision: %w", err)
	}
	return rev, nil
}

// CurrentCursor 返回机器已消费到的 last_machine_seq。
func (s *Store) CurrentCursor(ctx context.Context, machineID string) (int64, error) {
	var seq sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		"SELECT last_machine_seq FROM machine_cursors WHERE machine_id = ?", machineID).Scan(&seq); err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, fmt.Errorf("读取机器 %s cursor: %w", machineID, err)
	}
	if !seq.Valid {
		return 0, nil
	}
	return seq.Int64, nil
}
