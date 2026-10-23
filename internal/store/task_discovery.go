// Package store's task discovery read side provides bounded creation-ordered pages.
// It does not expose task execution details or create mirror links.
package store

import (
	"context"
	"fmt"

	"github.com/Xsxdot/handoff/internal/proto"
)

const taskDiscoveryMaxPage = 100

// DiscoverTasks returns tasks created after afterSeq, in stable creation order.
// limit must be between 1 and 100; NextSeq remains afterSeq for an empty page.
func (s *Store) DiscoverTasks(afterSeq int64, limit int) (proto.TaskDiscoveryPage, error) {
	page := proto.TaskDiscoveryPage{Items: []proto.TaskDiscoveryItem{}, NextSeq: afterSeq}
	if afterSeq < 0 || limit < 1 || limit > taskDiscoveryMaxPage {
		return page, fmt.Errorf("invalid task discovery bounds: after_seq=%d limit=%d", afterSeq, limit)
	}
	rows, err := s.db.QueryContext(context.Background(), `
SELECT d.seq, t.id, t.origin_session, t.state,
       EXISTS(SELECT 1 FROM tickets k WHERE k.task_id=t.id AND k.answer IS NULL)
FROM task_creation_seq d JOIN tasks t ON t.id=d.task_id
WHERE d.seq > ? ORDER BY d.seq LIMIT ?`, afterSeq, limit)
	if err != nil {
		return page, fmt.Errorf("query task discovery page after %d: %w", afterSeq, err)
	}
	defer rows.Close()
	for rows.Next() {
		var item proto.TaskDiscoveryItem
		var pending int
		if err := rows.Scan(&item.Seq, &item.TaskID, &item.OriginSession, &item.State, &pending); err != nil {
			return page, fmt.Errorf("scan task discovery item: %w", err)
		}
		item.HasPendingTickets = pending != 0
		page.Items = append(page.Items, item)
		page.NextSeq = item.Seq
	}
	if err := rows.Err(); err != nil {
		return page, fmt.Errorf("read task discovery page: %w", err)
	}
	if len(page.Items) == limit {
		if err := s.db.QueryRowContext(context.Background(), `SELECT EXISTS(SELECT 1 FROM task_creation_seq WHERE seq > ?)`, page.NextSeq).Scan(&page.HasMore); err != nil {
			return page, fmt.Errorf("check task discovery continuation after %d: %w", page.NextSeq, err)
		}
	}
	return page, nil
}
