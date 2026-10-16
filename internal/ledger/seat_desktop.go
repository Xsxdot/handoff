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
	"slices"
	"strings"
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

// validSeatDesktopVia 报告 via 是否在 proto.SeatDesktopVia* 词表内。写闸处校验：
// 词表外取值一旦进了 seat_desktop_set 载荷，timeline 读者无从解释。
func validSeatDesktopVia(via string) bool {
	switch via {
	case proto.SeatDesktopViaPage, proto.SeatDesktopViaLauncher, proto.SeatDesktopViaManual:
		return true
	}
	return false
}

// SetSeatDesktop 为卡上已有的 bind 席位写入桌面端记录（upsert，覆盖旧值）。
//
// 参数：id 卡号；expect 调用方读到的当前席位身份（CAS 期望值，原始字节比较）；
// desk 桌面端记录——Session 必须过 proto.ValidateDesktopSessionID，Via 必须是
// proto.SeatDesktopVia* 之一，Machine 允许空串（= 本机），BoundAt 被忽略（由账本
// 时钟写入）。
//
// 返回：nil = 同一事务内恰写（或覆盖）一行 seat_desktops 并落恰一条
// EvSeatDesktopSet（actor = 席位身份，载荷 {identity, desktop_session, machine, via}）。
// 非法 id / 词表外 via / 空座 / 非 bind 席位 → 包 ErrBadState；expect 与卡上席位
// 不符 → 包 ErrCASConflict；卡不存在 → 包 ErrNotFound。任一错误下不写行、不落事件。
func (s *Store) SetSeatDesktop(id, expect string, desk SeatDesktop) error {
	log().Info("开始写桌面端席位记录", "card", id, "has_expect", expect != "",
		"desktop_session", desk.Session, "machine", desk.Machine, "via", desk.Via)
	if err := proto.ValidateDesktopSessionID(desk.Session); err != nil {
		wrapped := fmt.Errorf("桌面端会话 id 非法（%v）: %w", err, ErrBadState)
		log().Warn("写桌面端席位记录被拒：会话 id 非法", "card", id, "cause", wrapped)
		return wrapped
	}
	if !validSeatDesktopVia(desk.Via) {
		err := fmt.Errorf("桌面端席位来源 %q 不在词表 {page,launcher,manual}: %w", desk.Via, ErrBadState)
		log().Warn("写桌面端席位记录被拒：来源非法", "card", id, "cause", err)
		return err
	}
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		card, err := getCardTx(s, tx, id)
		if err != nil {
			err = fmt.Errorf("写桌面端记录读卡 %s: %w", id, err)
			log().Warn("写桌面端席位记录失败：读卡", "card", id, "cause", err)
			return err
		}
		// 先判来源再判 CAS：空座的 driver_session 是空串，expect="" 会让纯 CAS 漏放。
		if card.DriverSource != string(proto.SeatSourceBind) || card.DriverSession == "" {
			err := fmt.Errorf("卡 %s 不是 bind 席位（source=%q），不能写桌面端记录: %w", id, card.DriverSource, ErrBadState)
			log().Warn("写桌面端席位记录被拒：非 bind 席位或空座", "card", id, "source", card.DriverSource, "cause", err)
			return err
		}
		if card.DriverSession != expect {
			err := fmt.Errorf("卡 %s 当前席位与期望不符，请重读后再写: %w", id, ErrCASConflict)
			log().Warn("写桌面端席位记录被拒：CAS 冲突", "card", id, "cause", err)
			return err
		}
		if _, err := tx.Exec(s.q(`INSERT INTO seat_desktops
				(card_id, identity, desktop_session, machine, via, bound_at)
				VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(card_id) DO UPDATE SET identity = excluded.identity,
					desktop_session = excluded.desktop_session, machine = excluded.machine,
					via = excluded.via, bound_at = excluded.bound_at`),
			id, card.DriverSession, desk.Session, desk.Machine, desk.Via, s.tval(s.timeNow())); err != nil {
			err = fmt.Errorf("写桌面端记录 %s: %w", id, err)
			log().Warn("写桌面端席位记录失败：写行", "card", id, "cause", err)
			return err
		}
		if _, err := s.appendEvent(tx, sink, id, EvSeatDesktopSet, card.DriverSession, map[string]string{
			"identity":        card.DriverSession,
			"desktop_session": desk.Session,
			"machine":         desk.Machine,
			"via":             desk.Via,
		}); err != nil {
			err = fmt.Errorf("桌面端记录落事件 %s: %w", id, err)
			log().Warn("写桌面端席位记录失败：落事件", "card", id, "cause", err)
			return err
		}
		return nil
	})
	if err != nil {
		// 事务内每条错误分支（写闸拒绝、读卡、写行、落事件）已各打一条带原因的 Warn；
		// 这里只补 Debug，避免同一失败两条 Warn。
		log().Debug("写桌面端席位记录失败", "card", id, "cause", err)
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

// deleteSeatDesktopTx 在调用方事务内删除该卡的桌面端记录（幂等）。只由 RebindSeat
// 与 clearSeatTx 调用：旧席位的桌面端 id 必须随席位一起失效——分成两步就会留下
// 「旧会话 id 指着新席位」的孤儿行，信被投到旧会话。
func (s *Store) deleteSeatDesktopTx(tx *sql.Tx, card string) error {
	if _, err := tx.Exec(s.q(`DELETE FROM seat_desktops WHERE card_id = ?`), card); err != nil {
		return fmt.Errorf("删桌面端记录 %s: %w", card, err)
	}
	return nil
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

// validateDesktopLauncher 执法登记形态：项目名非空且无首尾空白；桌面端会话 id 过
// proto.ValidateDesktopSessionID；CLI 会话 id 必须能编进 cli:claude#<id> 席位语法，
// 且不含路径分隔符、不以点开头（它是信箱文件名 ~/.handoff/desktop-wake/<CLI id>.<ns>.md 的一段）；
// actor 非空。全部失败包 ErrBadState。
func validateDesktopLauncher(l DesktopLauncher) error {
	if l.Project == "" || strings.TrimSpace(l.Project) != l.Project {
		return fmt.Errorf("拉起器登记项目名非法 %q: %w", l.Project, ErrBadState)
	}
	if err := proto.ValidateDesktopSessionID(l.DesktopSession); err != nil {
		return fmt.Errorf("拉起器桌面端会话 id 非法（%v）: %w", err, ErrBadState)
	}
	if _, err := proto.EncodeSeatIdentity("claude", l.CLISession); err != nil {
		return fmt.Errorf("拉起器 CLI 会话 id 非法（%v）: %w", err, ErrBadState)
	}
	if strings.ContainsAny(l.CLISession, `/\`) {
		return fmt.Errorf("拉起器 CLI 会话 id 含路径分隔符 %q: %w", l.CLISession, ErrBadState)
	}
	if strings.HasPrefix(l.CLISession, ".") {
		// 以点开头的信箱文件名会被 mod 与 writeDesktopLetter 同时跳过（点开头 = 写信中的
		// 临时文件），这样的拉起器永远收不到信；账本是该规则唯一执法点（契约 r11 第 3 条）。
		return fmt.Errorf("拉起器 CLI 会话 id 不得以点开头 %q: %w", l.CLISession, ErrBadState)
	}
	if strings.TrimSpace(l.Actor) == "" {
		return fmt.Errorf("拉起器登记缺 actor: %w", ErrBadState)
	}
	return nil
}

// RegisterDesktopLauncher 登记（覆盖）项目的拉起器；每项目至多一条。
//
// 参数：l 登记内容——Project/DesktopSession/CLISession/Actor 必填且合形（见
// validateDesktopLauncher），Machine 允许空串（= 本机），RegisteredAt 被忽略（由账本
// 时钟写入）。返回：形态不合法包 ErrBadState 且不写；写库失败原样包装返回。
// 注意：不落任何卡事件；覆盖式（同项目再登记直接替换前一条）。
func (s *Store) RegisterDesktopLauncher(l DesktopLauncher) error {
	log().Info("开始登记拉起器", "project", l.Project, "desktop_session", l.DesktopSession,
		"machine", l.Machine, "actor", l.Actor)
	if err := validateDesktopLauncher(l); err != nil {
		log().Warn("登记拉起器被拒", "project", l.Project, "cause", err)
		return err
	}
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		if _, err := tx.Exec(s.q(`INSERT INTO desktop_launchers
				(project, desktop_session, cli_session, machine, actor, registered_at)
				VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(project) DO UPDATE SET desktop_session = excluded.desktop_session,
					cli_session = excluded.cli_session, machine = excluded.machine,
					actor = excluded.actor, registered_at = excluded.registered_at`),
			l.Project, l.DesktopSession, l.CLISession, l.Machine, l.Actor, s.tval(s.timeNow())); err != nil {
			return fmt.Errorf("写拉起器登记 %s: %w", l.Project, err)
		}
		return nil
	})
	if err != nil {
		log().Warn("登记拉起器失败", "project", l.Project, "cause", err)
		return err
	}
	log().Info("拉起器登记完成", "project", l.Project, "desktop_session", l.DesktopSession, "machine", l.Machine)
	return nil
}

// DesktopLauncherOf 读项目的拉起器；无登记返回 ok=false 且不报错。
func (s *Store) DesktopLauncherOf(project string) (DesktopLauncher, bool, error) {
	var l DesktopLauncher
	var registeredAt any
	err := s.db.QueryRow(s.q(`SELECT project, desktop_session, cli_session, machine, actor, registered_at
		FROM desktop_launchers WHERE project = ?`), project).
		Scan(&l.Project, &l.DesktopSession, &l.CLISession, &l.Machine, &l.Actor, &registeredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return DesktopLauncher{}, false, nil
	}
	if err != nil {
		log().Warn("读拉起器登记失败", "project", project, "cause", err)
		return DesktopLauncher{}, false, fmt.Errorf("读拉起器登记 %s: %w", project, err)
	}
	l.RegisteredAt = toTime(registeredAt)
	return l, true, nil
}

// UnregisterDesktopLauncher 删除项目的拉起器登记（幂等：无登记仍返回 nil）。
func (s *Store) UnregisterDesktopLauncher(project string) error {
	removed := false
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		res, err := tx.Exec(s.q(`DELETE FROM desktop_launchers WHERE project = ?`), project)
		if err != nil {
			return fmt.Errorf("删拉起器登记 %s: %w", project, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("读删除影响行数: %w", err)
		}
		removed = n > 0
		return nil
	})
	if err != nil {
		log().Warn("注销拉起器失败", "project", project, "cause", err)
		return err
	}
	log().Info("拉起器登记已注销", "project", project, "had_row", removed)
	return nil
}

// ListDesktopLaunchers 列出全部拉起器登记，按 project 字节序升序；无登记返回空切片。
// 排序在 Go 侧做而非 ORDER BY：PG 的默认 collation 可能非 C（大小写、标点不按字节
// 序），两方言结果会分叉；字节序在两方言一致。
func (s *Store) ListDesktopLaunchers() ([]DesktopLauncher, error) {
	rows, err := s.db.Query(`SELECT project, desktop_session, cli_session, machine, actor, registered_at
		FROM desktop_launchers`)
	if err != nil {
		log().Warn("列拉起器登记失败", "cause", err)
		return nil, fmt.Errorf("列拉起器登记: %w", err)
	}
	defer rows.Close()
	out := []DesktopLauncher{}
	for rows.Next() {
		var l DesktopLauncher
		var registeredAt any
		if err := rows.Scan(&l.Project, &l.DesktopSession, &l.CLISession, &l.Machine, &l.Actor, &registeredAt); err != nil {
			log().Warn("扫拉起器登记行失败", "scanned", len(out), "cause", err)
			return nil, fmt.Errorf("扫拉起器登记: %w", err)
		}
		l.RegisteredAt = toTime(registeredAt)
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		log().Warn("遍历拉起器登记失败", "scanned", len(out), "cause", err)
		return nil, fmt.Errorf("列拉起器登记: %w", err)
	}
	slices.SortFunc(out, func(a, b DesktopLauncher) int { return strings.Compare(a.Project, b.Project) })
	return out, nil
}
