// 绑定席位与驱动活性租约（B312）。权威在账本域卡上：席位身份和来源是
// cards.driver_session/driver_source，所有新占用与换绑都经本文件的两个原子写面；
// driver_carrier 与 driver_leases 仅保留兼容语义，不能成为席位的第二真源。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
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

// BindSeat 只把空座原子地占为规范 identity/source，不写 driver_carrier；
// 同一事务落恰一条 EvDriverSeatBound（B358：协调者入群的 timeline 事实源，
// 与换绑的 EvDriverTakeover 配对）。读、判空、写入、落事件在同一个账本
// 事务内完成。B389：叫机器人席位必须同时落承载记录（§2.2 规则 1-3），
// 人尺度坐下必须为零承载（规则 4）。
func (s *Store) BindSeat(id, identity string, source proto.SeatSource, bearing SeatBearing) error {
	log().Info("开始坐下", "card", id, "source", source, "carrier", bearing.Carrier)
	if identity == "" {
		err := fmt.Errorf("坐下需要当前会话席位身份: %w", ErrBadState)
		log().Warn("坐下被拒：身份为空", "card", id, "source", source, "cause", err)
		return err
	}
	if err := proto.ValidateSeat(identity, source); err != nil {
		wrapped := fmt.Errorf("坐下席位无效: %w", err)
		log().Warn("坐下被拒：席位无效", "card", id, "source", source, "cause", wrapped)
		return wrapped
	}
	if err := validateSeatBearing(source, bearing); err != nil {
		log().Warn("坐下被拒：承载记录不合法", "card", id, "source", source, "cause", err)
		return err
	}
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		card, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("坐下读卡 %s: %w", id, err)
		}
		if card.DriverSession != "" || card.DriverSource != "" {
			err := fmt.Errorf("卡 %s 已有席位，请使用 rebind: %w", id, ErrCASConflict)
			log().Warn("坐下被拒：席位非空", "card", id, "source", source, "cause", err)
			return err
		}
		if _, err := tx.Exec(s.q(`UPDATE cards SET driver_session = ?, driver_source = ?, driver_heartbeat_at = ? WHERE id = ?`),
			identity, source, s.tval(s.timeNow()), id); err != nil {
			return fmt.Errorf("坐下写席位 %s: %w", id, err)
		}
		if err := s.writeSeatBearing(tx, id, identity, bearing); err != nil {
			return err
		}
		if _, err := s.appendEvent(tx, sink, id, EvDriverSeatBound, identity,
			map[string]string{"to": identity}); err != nil {
			return fmt.Errorf("坐下落事件 %s: %w", id, err)
		}
		return nil
	})
	if err != nil {
		log().Warn("坐下失败", "card", id, "source", source, "cause", err)
		return err
	}
	log().Info("坐下成功", "card", id, "source", source)
	return nil
}

// RebindSeat 以 expect 原始字节值 CAS 覆写非空席位，并在同一事务落恰一条
// EvDriverTakeover。expect 由服务层从账本读出，不来自用户可控 flag。
// B389：承载记录随席位同事务整体覆写——换绑可能同时换机器、换载体、换环境。
func (s *Store) RebindSeat(id, identity string, source proto.SeatSource, expect string, bearing SeatBearing) error {
	log().Info("开始换绑席位", "card", id, "source", source, "has_expect", expect != "", "carrier", bearing.Carrier)
	if identity == "" {
		err := fmt.Errorf("换绑需要目标会话席位身份: %w", ErrBadState)
		log().Warn("换绑被拒：身份为空", "card", id, "source", source, "cause", err)
		return err
	}
	if err := proto.ValidateSeat(identity, source); err != nil {
		wrapped := fmt.Errorf("换绑席位无效: %w", err)
		log().Warn("换绑被拒：席位无效", "card", id, "source", source, "cause", wrapped)
		return wrapped
	}
	if err := validateSeatBearing(source, bearing); err != nil {
		log().Warn("换绑被拒：承载记录不合法", "card", id, "source", source, "cause", err)
		return err
	}
	var old string
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		card, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("换绑读卡 %s: %w", id, err)
		}
		if card.DriverSession == "" && card.DriverSource == "" {
			err := fmt.Errorf("卡 %s 为空座，请使用 bind 或 coordinate: %w", id, ErrCASConflict)
			log().Warn("换绑被拒：空座", "card", id, "source", source, "cause", err)
			return err
		}
		if card.DriverSession == "" {
			err := fmt.Errorf("卡 %s 的旧席位缺少身份，不能直接换绑: %w", id, ErrBadState)
			log().Warn("换绑被拒：存量席位身份为空", "card", id, "source", source, "cause", err)
			return err
		}
		if card.DriverSession != expect {
			err := fmt.Errorf("卡 %s 当前席位与期望不符，请重读后换绑: %w", id, ErrCASConflict)
			log().Warn("换绑被拒：CAS 冲突", "card", id, "source", source, "cause", err)
			return err
		}
		if _, err := tx.Exec(s.q(`UPDATE cards SET driver_session = ?, driver_source = ?, driver_heartbeat_at = ? WHERE id = ?`),
			identity, source, s.tval(s.timeNow()), id); err != nil {
			return fmt.Errorf("换绑写席位 %s: %w", id, err)
		}
		if err := s.writeSeatBearing(tx, id, identity, bearing); err != nil {
			return err
		}
		if _, err := s.appendEvent(tx, sink, id, EvDriverTakeover, identity,
			map[string]string{"from": card.DriverSession, "to": identity}); err != nil {
			return fmt.Errorf("换绑落事件 %s: %w", id, err)
		}
		old = card.DriverSession
		return nil
	})
	if err != nil {
		log().Warn("换绑席位失败", "card", id, "source", source, "cause", err)
		return err
	}
	log().Info("换绑席位成功", "card", id, "source", source, "had_old", old != "")
	return nil
}

// SeatBearing 是协调者席位的承载记录（B389）：这一次会话实际生在哪个载体、
// 哪台机器，以及恢复它所需的运行环境。它只随 coordinate 席位存在——人尺度
// 坐下（bind）没有载体归属，唤醒本就是 no-op。
//
// 席位真源仍是 cards.driver_session/driver_source；本记录的 identity 列只是
// 同事务写入的一致性见证，读取方不得拿它当席位来源。
type SeatBearing struct {
	Carrier string // 载体登记名（scheduling.Carrier.Name）
	Machine string // 载体所在机器名
	HomeDir string // 恢复环境：Resume 用的 HOME（该 CLI 用默认环境时为空）
	Workdir string
	Model   string
}

// IsZero 报告承载记录是否为空值（人尺度坐下的唯一合法形态）。
func (b SeatBearing) IsZero() bool {
	return b.Carrier == "" && b.Machine == "" && b.HomeDir == "" && b.Workdir == "" && b.Model == ""
}

// validateSeatBearing 执法 §2.2 的两条形态规则：叫机器人必有载体与机器归属，
// 人尺度坐下必须无归属。宁可显式失败，不静默忽略半个记录。
func validateSeatBearing(source proto.SeatSource, bearing SeatBearing) error {
	if source == proto.SeatSourceCoordinate {
		if strings.TrimSpace(bearing.Carrier) == "" || strings.TrimSpace(bearing.Machine) == "" {
			return fmt.Errorf("叫机器人席位必须带载体与机器归属（carrier=%q machine=%q）: %w",
				bearing.Carrier, bearing.Machine, ErrBadState)
		}
		return nil
	}
	if !bearing.IsZero() {
		return fmt.Errorf("人尺度坐下不应带载体归属（carrier=%q）: %w", bearing.Carrier, ErrBadState)
	}
	return nil
}

// writeSeatBearing 在调用方事务内 upsert 承载记录；upsert 让换绑天然整体覆写。
func (s *Store) writeSeatBearing(tx *sql.Tx, card, identity string, bearing SeatBearing) error {
	if bearing.IsZero() {
		// 人尺度坐下：确保没有上一段会话留下的承载行。
		return s.deleteSeatBearing(tx, card)
	}
	if _, err := tx.Exec(s.q(`INSERT INTO seat_bearings
			(card_id, identity, carrier, machine, home_dir, workdir, model, bound_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(card_id) DO UPDATE SET identity = excluded.identity,
				carrier = excluded.carrier, machine = excluded.machine,
				home_dir = excluded.home_dir, workdir = excluded.workdir,
				model = excluded.model, bound_at = excluded.bound_at`),
		card, identity, bearing.Carrier, bearing.Machine, bearing.HomeDir,
		bearing.Workdir, bearing.Model, s.tval(s.timeNow())); err != nil {
		return fmt.Errorf("写承载记录 %s: %w", card, err)
	}
	return nil
}

// deleteSeatBearing 在调用方事务内删除承载记录（幂等）。
func (s *Store) deleteSeatBearing(tx *sql.Tx, card string) error {
	if _, err := tx.Exec(s.q(`DELETE FROM seat_bearings WHERE card_id = ?`), card); err != nil {
		return fmt.Errorf("删承载记录 %s: %w", card, err)
	}
	return nil
}

// SeatBearingOf 读该卡的承载记录；无记录返回 ok=false 且不报错。
// 它不参与"是不是空座"的判定——空座只由 cards 两列判。
func (s *Store) SeatBearingOf(id string) (SeatBearing, bool, error) {
	var b SeatBearing
	err := s.db.QueryRow(s.q(`SELECT carrier, machine, home_dir, workdir, model
		FROM seat_bearings WHERE card_id = ?`), id).
		Scan(&b.Carrier, &b.Machine, &b.HomeDir, &b.Workdir, &b.Model)
	if errors.Is(err, sql.ErrNoRows) {
		return SeatBearing{}, false, nil
	}
	if err != nil {
		return SeatBearing{}, false, fmt.Errorf("读承载记录 %s: %w", id, err)
	}
	return b, true, nil
}

// ClearSeat 显式清空席位与承载记录（同事务，幂等）：终态卡与人工修复路径共用。
// 清席位不落席位事件——它是"席位不再存在"，不是换绑。
func (s *Store) ClearSeat(id string) error {
	cleared := false
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		card, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("清席位读卡 %s: %w", id, err)
		}
		if card.DriverSession != "" || card.DriverSource != "" {
			if _, err := tx.Exec(s.q(`UPDATE cards SET driver_session = '', driver_source = '', driver_heartbeat_at = ? WHERE id = ?`),
				s.tval(time.Time{}), id); err != nil {
				return fmt.Errorf("清席位写卡 %s: %w", id, err)
			}
			cleared = true
		}
		return s.deleteSeatBearing(tx, id)
	})
	if err != nil {
		log().Warn("清席位失败", "card", id, "cause", err)
		return err
	}
	log().Info("清席位完成", "card", id, "had_seat", cleared)
	return nil
}

// ClaimCardAs 保留旧签名以兼容编译，但不再占用协调者席位。
// carrier 只是历史兼容参数；新流程必须使用 BindSeat 或 RebindSeat。
func (s *Store) ClaimCardAs(id, owner, carrier string) error {
	log().Warn("旧认领入口已停用", "card", id, "has_owner", owner != "", "has_carrier", carrier != "")
	return fmt.Errorf("卡 %s 不再通过 dispatch/ClaimCardAs 占座，请使用 bind、coordinate 或 rebind: %w", id, ErrBadState)
}

// RebindDriver 保留旧签名以兼容编译，但已停用；新的协调者换绑必须使用
// RebindSeat，以便显式携带席位来源并在统一写面完成 CAS。
func (s *Store) RebindDriver(id, toSession, carrier, expect string) error {
	log().Warn("旧驱动换绑入口已停用", "card", id, "has_target", toSession != "", "has_expect", expect != "", "has_carrier", carrier != "")
	return fmt.Errorf("卡 %s 不再通过 RebindDriver 占座，请使用 RebindSeat: %w", id, ErrBadState)
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
