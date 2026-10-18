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
// （B409.6：补收起点读随 CLI context 取消）。点读不单独量池等待。
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

// SessionDeliveryCursorRowContext 读持久交付游标行并区分「无行」与「行值」（B435 C-119：
// 席位地址无游标行时积压下界另算，不能当作 0）。key 为 proto.DeliveryCursorKey 的编码值。
// 返回：无行 → (0,false,nil)；有行 → (last_seq,true,nil)；读失败 → 带上下文错误。
// 既有 SessionDeliveryCursorContext（无行返回 0）不变，session wait 仍在用。
func (s *Store) SessionDeliveryCursorRowContext(ctx context.Context, key string) (int64, bool, error) {
	if key == "" {
		return 0, false, fmt.Errorf("交付游标键不能为空")
	}
	var seq int64
	err := s.db.QueryRowContext(ctx, s.q(`SELECT last_seq FROM session_delivery_cursors WHERE member=?`), key).Scan(&seq)
	if err == sql.ErrNoRows {
		log().Debug("交付游标无行", append(diag.Attrs(ctx), "key", key)...)
		return 0, false, nil
	}
	if err != nil {
		log().Warn("读取交付游标行失败", append(diag.Attrs(ctx), "key", key, "cause", err)...)
		return 0, false, fmt.Errorf("读取交付游标 %s: %w", key, err)
	}
	return seq, true, nil
}

// SeatBoundSeqContext 读席位地址的积压下界（B435 C-119，plan Q2 甲：限本卡）：cardID 上
// payload.to=identity 的最近一条坐下（EvDriverSeatBound）或换绑（EvDriverTakeover）事件 seq。
// 从未以该身份坐下/换绑过 → (0,false,nil)；读失败 → 带上下文错误。
func (s *Store) SeatBoundSeqContext(ctx context.Context, cardID, identity string) (int64, bool, error) {
	if cardID == "" || identity == "" {
		return 0, false, fmt.Errorf("读席位下界需要卡号与身份（card=%q identity=%q）", cardID, identity)
	}
	toExpr := `payload->>'to'`
	if s.dialect == dialectSQLite {
		toExpr = `json_extract(payload, '$.to')`
	}
	var seq sql.NullInt64
	err := s.db.QueryRowContext(ctx, s.q(`SELECT MAX(seq) FROM card_events
		WHERE card_id = ? AND type IN (?, ?) AND `+toExpr+` = ?`),
		cardID, EvDriverSeatBound, EvDriverTakeover, identity).Scan(&seq)
	if err != nil {
		log().Warn("读席位积压下界失败", append(diag.Attrs(ctx), "card", cardID, "identity", identity, "cause", err)...)
		return 0, false, fmt.Errorf("读卡 %s 席位 %s 下界: %w", cardID, identity, err)
	}
	if !seq.Valid {
		return 0, false, nil
	}
	return seq.Int64, true, nil
}
