// store control_events.go：全局控制事件（control_revision）存储。
//
// 职责：
//   - ControlEventsAfter：按 revision 升序拉取控制事件（control stream 重放用）
//
// 边界：
//   - 控制事件的写入只在 ApplyMachineEvent 的单事务内发生（见 machine_events.go）
//   - 本文件只提供读取侧；写入不开放给 handler 层
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/xushixin/handoff/internal/controlplane"
)

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
