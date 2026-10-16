// seat_desktop.go —— B434 桌面端席位记录与项目拉起器登记。
//
// 职责：bind 席位的「Claude 桌面端会话 id + 所在机器 + 拉起来源」（seat_desktops）
// 与每项目至多一个的拉起器登记（desktop_launchers）。两者都是账本行，与
// cards.driver_session 的席位真源分开存放：席位真源不变（bind / cli:claude#<CLI
// 会话 id>），本记录只回答「怎样把这场会话叫醒」。不复用 seat_bearings：那张表
// 的存在即意味着 coordinate 席位（B389 规则 4，见 binding.go#validateSeatBearing），
// 见契约拍板 D1。
//
// 边界：
//   - SetSeatDesktop 是 seat_desktops 唯一写面（CAS：expect = 卡上席位、来源必须是
//     bind），与 BindSeat 两步提交（拍板 D2）；删行只经 deleteSeatDesktopTx，由
//     RebindSeat 与 clearSeatTx 在各自事务内调用（生命周期联动）。BindSeat 不写也不删。
//   - 拉起器登记不落任何卡事件（无卡归属），来源由 actor 列追溯；没有生命周期联动
//     （会话被删后登记仍在），由 handoff status 的可达探测与 launcher unregister 兜底。
//   - 时间列 bound_at / registered_at 由 Store 取账本时钟写入，入参里的
//     BoundAt / RegisteredAt 被忽略。
//   - 本文件不校验席位 cli 段是否为 claude（契约条 60/141 在 CLI 一侧执法）。
//
// 契约见 docs/superpowers/specs/b434-contract.md §3.3–§3.4、§8 D1/D2/D4。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// SeatDesktop 是 bind 席位的桌面端记录：这场 claude 对话在 Claude 桌面端里的
// 会话 id 与所在机器，以及它是怎么被开出来的。
type SeatDesktop struct {
	Session string // 桌面端会话 id（local_…），proto.ValidateDesktopSessionID 校验
	Machine string // 所在机器：与 SeatBearing.Machine 同一值域（空串/本机名/登记的 target 名）
	Via     string // proto.SeatDesktopVia*
	BoundAt time.Time
}

// SetSeatDesktop 为卡上已有的 bind 席位写入桌面端记录（upsert，覆盖旧值）。
// T2 最薄路径版：只有正路径；写闸（id/via 校验、来源判定、CAS）由 T3 补齐。
func (s *Store) SetSeatDesktop(id, expect string, desk SeatDesktop) error {
	log().Info("开始写桌面端席位记录", "card", id, "has_expect", expect != "",
		"desktop_session", desk.Session, "machine", desk.Machine, "via", desk.Via)
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		card, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("写桌面端记录读卡 %s: %w", id, err)
		}
		if _, err := tx.Exec(s.q(`INSERT INTO seat_desktops
				(card_id, identity, desktop_session, machine, via, bound_at)
				VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(card_id) DO UPDATE SET identity = excluded.identity,
					desktop_session = excluded.desktop_session, machine = excluded.machine,
					via = excluded.via, bound_at = excluded.bound_at`),
			id, card.DriverSession, desk.Session, desk.Machine, desk.Via, s.tval(s.timeNow())); err != nil {
			return fmt.Errorf("写桌面端记录 %s: %w", id, err)
		}
		if _, err := s.appendEvent(tx, sink, id, EvSeatDesktopSet, card.DriverSession, map[string]string{
			"identity":        card.DriverSession,
			"desktop_session": desk.Session,
			"machine":         desk.Machine,
			"via":             desk.Via,
		}); err != nil {
			return fmt.Errorf("桌面端记录落事件 %s: %w", id, err)
		}
		return nil
	})
	if err != nil {
		log().Warn("写桌面端席位记录失败", "card", id, "cause", err)
		return err
	}
	log().Info("写桌面端席位记录完成", "card", id, "desktop_session", desk.Session, "machine", desk.Machine, "via", desk.Via)
	return nil
}

// SeatDesktopOf 读该卡的桌面端记录；无记录返回 ok=false 且不报错。
// 它不参与「是不是空座」的判定——空座只由 cards 两列判。
func (s *Store) SeatDesktopOf(id string) (SeatDesktop, bool, error) {
	var d SeatDesktop
	var boundAt any
	err := s.db.QueryRow(s.q(`SELECT desktop_session, machine, via, bound_at
		FROM seat_desktops WHERE card_id = ?`), id).Scan(&d.Session, &d.Machine, &d.Via, &boundAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SeatDesktop{}, false, nil
	}
	if err != nil {
		log().Warn("读桌面端席位记录失败", "card", id, "cause", err)
		return SeatDesktop{}, false, fmt.Errorf("读桌面端记录 %s: %w", id, err)
	}
	d.BoundAt = toTime(boundAt)
	return d, true, nil
}

// DesktopLauncher 是某项目的拉起器登记：一场由人开过的普通桌面端会话，
// 人不在时由控制面写信 + continue 唤醒它代开协调者会话。
type DesktopLauncher struct {
	Project        string // 卡的 project 名（cards.project 同值域）
	DesktopSession string // 桌面端会话 id（local_…）
	CLISession     string // 该会话的 CLAUDE_CODE_SESSION_ID（信箱文件名用它）
	Machine        string // 所在机器，值域同 SeatDesktop.Machine
	RegisteredAt   time.Time
	Actor          string
}

// RegisterDesktopLauncher 登记（覆盖）项目的拉起器；每项目至多一条。
func (s *Store) RegisterDesktopLauncher(l DesktopLauncher) error {
	_ = l
	return proto.ErrDesktopNotImplemented // TODO(B434)
}

// DesktopLauncherOf 读项目的拉起器；无登记返回 ok=false 且不报错。
func (s *Store) DesktopLauncherOf(project string) (DesktopLauncher, bool, error) {
	_ = project
	return DesktopLauncher{}, false, proto.ErrDesktopNotImplemented // TODO(B434)
}

// UnregisterDesktopLauncher 删除项目的拉起器登记（幂等）。
func (s *Store) UnregisterDesktopLauncher(project string) error {
	_ = project
	return proto.ErrDesktopNotImplemented // TODO(B434)
}

// ListDesktopLaunchers 列出全部拉起器登记，按 project 升序。
func (s *Store) ListDesktopLaunchers() ([]DesktopLauncher, error) {
	return nil, proto.ErrDesktopNotImplemented // TODO(B434)
}
