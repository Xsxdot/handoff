// 会话订阅交付水位独立于 UI 已读和 message_consumed，按外部成员跨机器共享。
package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Xsxdot/handoff/internal/diag"
)

// SessionDeliveryCursor 读已成功输出给监听器的最大命中 seq；无记录为 0。
func (s *Store) SessionDeliveryCursor(member string) (int64, error) {
	return s.SessionDeliveryCursorContext(context.Background(), member)
}

// SessionDeliveryCursorContext 是 SessionDeliveryCursor 的可取消入口
//（B409.6：补收起点读随 CLI context 取消）。点读不单独量池等待。
func (s *Store) SessionDeliveryCursorContext(ctx context.Context, member string) (int64, error) {
	if member == "" {
		return 0, fmt.Errorf("交付游标成员不能为空")
	}
	started := time.Now()
	var seq int64
	err := s.db.QueryRowContext(ctx, s.q(`SELECT last_seq FROM session_delivery_cursors WHERE member=?`), member).Scan(&seq)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		log().Warn("读取会话交付游标失败", append(diag.Attrs(ctx), "error_class", readErrorClass(err, ""), "cause", err)...)
		return 0, fmt.Errorf("读取会话交付游标: %w", err)
	}
	log().Debug("读取会话交付游标完成", append(diag.Attrs(ctx), "last_seq", seq,
		"elapsed_ns", time.Since(started).Nanoseconds())...)
	return seq, nil
}

// AdvanceSessionDeliveryCursor 只升不降；延迟写入不能覆盖另一实例的新水位。
func (s *Store) AdvanceSessionDeliveryCursor(member string, seq int64) error {
	if member == "" || seq < 0 {
		return fmt.Errorf("会话交付游标参数非法")
	}
	_, err := s.db.Exec(s.q(`INSERT INTO session_delivery_cursors (member, last_seq, updated_at)
		VALUES (?,?,?) ON CONFLICT (member) DO UPDATE SET
		last_seq=excluded.last_seq, updated_at=excluded.updated_at
		WHERE session_delivery_cursors.last_seq < excluded.last_seq`), member, seq, s.tval(s.timeNow()))
	if err != nil {
		return fmt.Errorf("推进会话交付游标: %w", err)
	}
	return nil
}
