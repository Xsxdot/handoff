// session_resident.go —— B435 房间驻场者登记的只读面（契约 b435-contract.md ②④；plan P2 甲 / P3 甲）。
//
// 职责：按会话主键读 session_residents 一行。驻场者是会话房间无席位的默认收件人，
// 一房间至多一名（主键即约束，C-414）。
// 边界：K0 只有读面，没有任何生产写入口——表恒空即真实的「无驻场者」；写面
// （session sit/stand、身份校验）归后续子卡 R。本文件返回账本本地类型，wire 映射在
// internal/ledger/api（避免新增 d_ledger→d_protocol 调用边）。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SessionResident 是驻场者登记一行。桌面端三列同空 = 无桌面端地址（只靠 session wait 收件）。
type SessionResident struct {
	Session        string // 会话房间 id（session:<n>），主键
	Identity       string // 驻场者成员身份（user:/agent: 统一记法）
	DesktopSession string // Claude 桌面端会话 id；空 = 无
	CLISession     string // 桌面端会话的 CLI 会话 id（信箱文件名键）；空 = 无
	Machine        string // 桌面端所在机器；空 = 登记时的本机
	SatAt          time.Time
}

// SessionResident 读会话的驻场者。参数 session 为会话房间 id。返回：无登记 → ok=false、
// err=nil（「无驻场者」是合法读数）；读失败 → 带会话上下文的错误（调用方不得当作无驻场者）。
func (s *Store) SessionResident(session string) (SessionResident, bool, error) {
	if session == "" {
		err := fmt.Errorf("读驻场者：会话为空: %w", ErrBadState)
		log().Warn("读驻场者被拒：会话为空", "cause", err)
		return SessionResident{}, false, err
	}
	var r SessionResident
	var satAt any
	err := s.db.QueryRow(s.q(`SELECT session, identity, desktop_session, cli_session, machine, sat_at
		FROM session_residents WHERE session = ?`), session).
		Scan(&r.Session, &r.Identity, &r.DesktopSession, &r.CLISession, &r.Machine, &satAt)
	if errors.Is(err, sql.ErrNoRows) {
		log().Debug("会话无驻场者", "session", session)
		return SessionResident{}, false, nil
	}
	if err != nil {
		log().Warn("读驻场者失败", "session", session, "cause", err)
		return SessionResident{}, false, fmt.Errorf("读会话 %s 驻场者: %w", session, err)
	}
	r.SatAt = toTime(satAt)
	log().Debug("读驻场者完成", "session", session, "identity", r.Identity, "has_desktop", r.DesktopSession != "")
	return r, true, nil
}
