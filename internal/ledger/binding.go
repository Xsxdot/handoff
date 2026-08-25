// 绑定席位与驱动活性租约（B156.2）。权威在账本域卡上（拍板记录 5.1）：
// 绑定 = cards.driver_session + 新列 driver_carrier，CAS 语义与派发即认领
// （ClaimCard）同源；心跳 = 新表 driver_leases 按 session 全局一行，租期
// 模式照抄运行锁但互不代写（runlock.go 文件头边界）。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// DriverLease 驱动活性租约一行：哪个协调者会话、活到几点。
type DriverLease struct {
	Session   string
	ExpiresAt time.Time
}

const (
	// DriverLeaseTTL 驱动租约默认租期（照抄 RunLockTTL 数量级）。
	DriverLeaseTTL = 5 * time.Minute
	// DriverLeaseRenewInterval 生产续租 goroutine 的默认节拍。
	DriverLeaseRenewInterval = 2 * time.Minute
)

// ClaimCardAs 认领归属并登记载体标识（B156.2 契约 §3.2）。语义与 ClaimCard
// 完全一致：归属判定与归属写入同一事务（move.go 文件头警告的两写窗口）、
// 不改状态列、不落事件、终态拒绝 ErrBadState、他主持有拒绝 ErrCASConflict、
// 同 owner 重入幂等；另写 driver_carrier 列。
//
// carrier 是不透明载体标识（breakdown 澄清一，2026-08-26 定稿）：本期只存
// 不解释——不解析、不校验、不假设格式；空串含义是「未登记载体」。既有
// ClaimCard 保持签名不变、内部转调本方法传空串：老调用方零改动，显式认领
// 同时把历史载体归零为未登记（权威在本次认领动作本身）。
func (s *Store) ClaimCardAs(id, owner, carrier string) error {
	log().Info("开始认领归属", "card", id, "owner", owner)
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if owner == "" {
			log().Warn("认领被拒：owner 为空", "card", id)
			return fmt.Errorf("认领被拒：owner 为空")
		}
		card, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("认领: 卡 %s: %w", id, err)
		}
		if card.Status == StatusDone || card.Status == StatusClosed {
			log().Warn("认领被拒：终态卡", "card", id, "status", card.Status)
			return fmt.Errorf("卡 %s 已处于终态 %s: %w", id, card.Status, ErrBadState)
		}
		if card.DriverSession != "" && card.DriverSession != owner {
			log().Warn("认领被拒：他主持有", "card", id,
				"holder", card.DriverSession, "claimer", owner)
			return fmt.Errorf("卡 %s 已由 %s 认领: %w", id, card.DriverSession, ErrCASConflict)
		}
		if _, err := tx.Exec(s.q(`UPDATE cards SET driver_session = ?, driver_carrier = ?,
			driver_heartbeat_at = ? WHERE id = ?`),
			owner, carrier, s.tval(s.timeNow()), id); err != nil {
			return fmt.Errorf("认领写归属: %w", err)
		}
		return nil
	})
	if err != nil {
		log().Warn("认领归属失败", "card", id, "owner", owner, "cause", err)
		return err
	}
	log().Info("归属已认领", "card", id, "owner", owner)
	return nil
}

// RebindDriver 换绑（契约 §3.2）：expect=当前绑定前值（CAS，不符返回
// ErrCASConflict）；expect="" 与「前值为空」统一比较——即要求当前无绑定。
// 成功=同事务覆写 driver_session+driver_carrier（并按 TakeoverCard 先例
// 刷新 driver_heartbeat_at＝新认领时刻）且落恰一条 EvDriverTakeover
// payload{from,to}、actor=新会话（运行锁抢占同位先例 runlock.go:89）。
// 复用活跃事件类型，不新增绑定事件类型。写权检查读当前值：旧会话对该
// 房间的一切后续判定随覆写自然剥权（C4 Send 执法依赖这一点）。
// 终态卡不在拒绝之列：冻结语义无此条（房间面只读由 ReadOnly 执法）。
func (s *Store) RebindDriver(id, toSession, carrier, expect string) error {
	log().Info("开始换绑驱动", "card", id, "to", toSession, "expect", expect)
	prev := ""
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if toSession == "" {
			log().Warn("换绑被拒：目标会话为空", "card", id)
			return fmt.Errorf("换绑被拒：目标会话为空")
		}
		card, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("换绑: 卡 %s: %w", id, err)
		}
		if card.DriverSession != expect {
			log().Warn("换绑被拒：CAS 冲突", "card", id, "expect", expect, "actual", card.DriverSession)
			return fmt.Errorf("卡 %s 当前绑定 %q 非 %q: %w", id, card.DriverSession, expect, ErrCASConflict)
		}
		if _, err := tx.Exec(s.q(`UPDATE cards SET driver_session = ?, driver_carrier = ?,
			driver_heartbeat_at = ? WHERE id = ?`),
			toSession, carrier, s.tval(s.timeNow()), id); err != nil {
			return fmt.Errorf("换绑写归属: %w", err)
		}
		if _, err := s.appendEvent(tx, sink, id, EvDriverTakeover, toSession,
			map[string]string{"from": card.DriverSession, "to": toSession}); err != nil {
			return fmt.Errorf("换绑落事件: %w", err)
		}
		prev = card.DriverSession
		return nil
	})
	if err != nil {
		log().Warn("换绑驱动失败", "card", id, "to", toSession, "cause", err)
		return err
	}
	log().Info("驱动已换绑", "card", id, "from", prev, "to", toSession)
	return nil
}

// RenewDriverLease 续租/首建该 session 的租约行（upsert，session 全局一行：
// 兼任 N 卡的协调者一条租约覆盖全部席位，「它活着吗」问的就是会话本身）。
// 返回是否生效：true=行已写入（首建或续期）。负 ttl 合法——产已过期行，
// 供消费侧与测试在注入时钟下演练活性翻转；活性判定不在本方法内做
// （RunLockOf 同一约定：读面不过滤过期）。
func (s *Store) RenewDriverLease(session string, ttl time.Duration) (bool, error) {
	if session == "" {
		log().Warn("租约续期被拒：session 为空")
		return false, fmt.Errorf("租约续期被拒：session 为空")
	}
	renewed := false
	var expiresAt time.Time
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		expiresAt = s.timeNow().Add(ttl)
		// 两方言同形的 upsert：excluded.expires_at 引用插入候选行
		// （SQLite≥3.24 / PG≥9.5；modernc.org/sqlite v1.56.0 最小程序实测通过）。
		if _, err := tx.Exec(s.q(`INSERT INTO driver_leases (session, expires_at) VALUES (?, ?)
			ON CONFLICT(session) DO UPDATE SET expires_at = excluded.expires_at`),
			session, s.tval(expiresAt)); err != nil {
			return fmt.Errorf("续驱动租约: %w", err)
		}
		renewed = true
		return nil
	})
	if err != nil {
		log().Warn("驱动租约续期失败", "session", session, "cause", err)
		return false, err
	}
	log().Info("驱动租约已续", "session", session,
		"expires_at", expiresAt.Format(time.RFC3339))
	return renewed, nil
}

// DropDriverLease 删除该 session 的租约行（幂等：行不存在仍是成功 no-op）。
func (s *Store) DropDriverLease(session string) error {
	dropped := false
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		res, err := tx.Exec(s.q(`DELETE FROM driver_leases WHERE session = ?`), session)
		if err != nil {
			return fmt.Errorf("删驱动租约: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("读删除影响行数: %w", err)
		}
		dropped = n > 0
		return nil
	})
	if err != nil {
		log().Warn("驱动租约删除失败", "session", session, "cause", err)
		return err
	}
	if dropped {
		log().Info("驱动租约已删除", "session", session)
	}
	return nil
}

// DriverLeaseOf 读单 session 租约；第二个返回值=行是否存在。
// 不过滤过期：「是否活着」由消费侧按 ExpiresAt 与同一注入时钟判定
// （RunLockOf 同一约定，runlock.go:143-144）。
func (s *Store) DriverLeaseOf(session string) (DriverLease, bool, error) {
	var lease DriverLease
	var expiresAt any
	err := s.db.QueryRow(s.q(`SELECT session, expires_at FROM driver_leases WHERE session = ?`),
		session).Scan(&lease.Session, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return DriverLease{}, false, nil
	}
	if err != nil {
		return DriverLease{}, false, fmt.Errorf("读驱动租约 %s: %w", session, err)
	}
	lease.ExpiresAt = toTime(expiresAt)
	return lease, true, nil
}

// AllDriverLeases 全部租约行（看板批量判活用）。同样不过滤过期，
// 按 session 排序保证读形确定。
func (s *Store) AllDriverLeases() ([]DriverLease, error) {
	rows, err := s.db.Query(`SELECT session, expires_at FROM driver_leases ORDER BY session`)
	if err != nil {
		return nil, fmt.Errorf("读全部驱动租约: %w", err)
	}
	defer rows.Close()
	var out []DriverLease
	for rows.Next() {
		var lease DriverLease
		var expiresAt any
		if err := rows.Scan(&lease.Session, &expiresAt); err != nil {
			return nil, err
		}
		lease.ExpiresAt = toTime(expiresAt)
		out = append(out, lease)
	}
	return out, rows.Err()
}
