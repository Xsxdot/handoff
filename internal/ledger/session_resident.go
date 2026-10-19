// session_resident.go —— B435 房间驻场者登记的读写面（契约 b435-contract.md ②④；plan P2 甲 / P3 甲；
// R plan Q6–Q8）。
//
// 职责：按会话主键读、登记、请下 session_residents 一行。驻场者是会话房间无席位的默认收件人，
// 一房间至多一名（主键即约束，C-414）。
// 边界：读面 K0 落地；写面（SitResident/StandResident）是 R 的 `session sit/stand` 账本半边，
// 只管会话存在/归档与唯一性，身份记法校验在 collab.Service.SitResident（本包不调 proto 包级
// 函数，避免推高 d_ledger→d_protocol 预算）。本文件返回账本本地类型，wire 映射在
// internal/ledger/api。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// 驻场者驱动形态词表（B444 P1，plan Q2 甲）：空串 = desktop（旧行零回填即此义，门铃信驱动）；
// push = 由 handoff 拉起的载体担任，唤醒走 keystone 续轮。与 proto.ResidentDriver* 同值（本包不
// import proto 的包级符号以守 d_ledger→d_protocol 预算，wire 映射在 internal/ledger/api）。
const (
	ResidentDriverDesktop = ""
	ResidentDriverPush    = "push"
)

// SessionResident 是驻场者登记一行。桌面端三列同空 = 无桌面端地址（只靠 session wait 收件）。
// push 驻场者（DriverMode=push）的载体落点存在独立的 bearing 列里，**不复用** cli_session/machine
// （plan Q2 甲：门铃只靠 cli_session 判可按，复用会造成门铃与 keystone 双重唤醒）。
type SessionResident struct {
	Session        string // 会话房间 id（session:<n>），主键
	Identity       string // 驻场者成员身份（user:/agent: 统一记法）
	DesktopSession string // Claude 桌面端会话 id；空 = 无
	CLISession     string // 桌面端会话的 CLI 会话 id（信箱文件名键）；空 = 无
	Machine        string // 桌面端所在机器；空 = 登记时的本机
	SatAt          time.Time
	// B444 push 驱动形态与载体落点（与 seat_bearings 同形）；desktop 行全空。
	DriverMode     string // ResidentDriverDesktop | ResidentDriverPush
	PushCLI        string // push 载体 CLI（opencode/claude…）
	PushSession    string // push 载体的 CLI 会话 id（续轮 resume 的目标）
	Carrier        string // 载体名（scheduling 登记名）
	CarrierMachine string // 载体所在机器；空 = 本机
	HomeDir        string // 载体隔离 HOME
	Workdir        string // 回合工作目录
	Model          string // 载体模型
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
	err := s.db.QueryRow(s.q(`SELECT session, identity, desktop_session, cli_session, machine, sat_at,
		driver_mode, push_cli, push_session, carrier, carrier_machine, home_dir, workdir, model
		FROM session_residents WHERE session = ?`), session).
		Scan(&r.Session, &r.Identity, &r.DesktopSession, &r.CLISession, &r.Machine, &satAt,
			&r.DriverMode, &r.PushCLI, &r.PushSession, &r.Carrier, &r.CarrierMachine, &r.HomeDir, &r.Workdir, &r.Model)
	if errors.Is(err, sql.ErrNoRows) {
		log().Debug("会话无驻场者", "session", session)
		return SessionResident{}, false, nil
	}
	if err != nil {
		log().Warn("读驻场者失败", "session", session, "cause", err)
		return SessionResident{}, false, fmt.Errorf("session_residents 查询 session=%s: %w", session, err)
	}
	r.SatAt = toTime(satAt)
	log().Debug("读驻场者完成", "session", session, "identity", r.Identity, "has_desktop", r.DesktopSession != "",
		"driver_mode", r.DriverMode)
	return r, true, nil
}

// SetResidentPushSession 给首轮在途的 push 驻场行补写 push_session（B444 P2 返工 F1：先坐下、再拉起首轮、
// 首轮返回会话 id 后补写——首轮里驻场者已是书写者，能发言、能改名）。
// 参数：identity 作 CAS（须等于现驻场者）；pushSession 非空。只写「driver_mode=push 且 push_session 仍空」
// 的行——单条条件 UPDATE，两方言原子。返回：入参为空 → ErrBadState；无行或身份不符 → ErrResidentNotSeated；
// 行在但已补写过 / 不是 push → ErrCASConflict（原值不变）。不落账本事件、不递增席位版本（驻场身份不变）。
func (s *Store) SetResidentPushSession(session, identity, pushSession string) error {
	if session == "" || identity == "" || pushSession == "" {
		err := fmt.Errorf("补写驻场者 push_session：会话、身份、会话 id 必填（session=%q identity=%q）: %w",
			session, identity, ErrBadState)
		log().Warn("补写驻场者 push_session 被拒：入参为空", "cause", err)
		return err
	}
	res, err := s.db.Exec(s.q(`UPDATE session_residents SET push_session = ?
		WHERE session = ? AND identity = ? AND driver_mode = ? AND push_session = ''`),
		pushSession, session, identity, ResidentDriverPush)
	if err != nil {
		log().Warn("补写驻场者 push_session 失败", "session", session, "identity", identity, "cause", err)
		return fmt.Errorf("补写驻场者 push_session session=%s: %w", session, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("读补写 push_session 影响行数: %w", err)
	}
	if n == 1 {
		log().Info("驻场者 push_session 已补写", "session", session, "identity", identity, "push_session", pushSession)
		return nil
	}
	current, ok, err := s.SessionResident(session)
	if err != nil {
		return err
	}
	if !ok || current.Identity != identity {
		log().Info("补写 push_session 被拒：身份不是现驻场者", "session", session, "identity", identity, "resident_present", ok)
		return fmt.Errorf("会话 %s 的现驻场者不是 %s: %w", session, identity, ErrResidentNotSeated)
	}
	log().Info("补写 push_session 被拒：行已补写或不是 push", "session", session, "identity", identity,
		"driver_mode", current.DriverMode, "has_push_session", current.PushSession != "")
	return fmt.Errorf("会话 %s 驻场行不是首轮在途的 push 行（driver_mode=%q，push_session 已有=%v）: %w",
		session, current.DriverMode, current.PushSession != "", ErrCASConflict)
}

// ListInflightPushResidents 列首轮在途的 push 驻场行（driver_mode=push 且 push_session 空），按会话 id 升序
// （B444 P2：agentd 启动清扫的读面——首轮回合由拉起它的 agentd 进程执行，进程重启后在途行不会再被补写）。
// 不按机器过滤：哪台机器的行归谁处置由调用方按 carrier_machine 判（R9）。读失败带上下文上抛。
func (s *Store) ListInflightPushResidents() ([]SessionResident, error) {
	rows, err := s.db.Query(s.q(`SELECT session, identity, desktop_session, cli_session, machine, sat_at,
		driver_mode, push_cli, push_session, carrier, carrier_machine, home_dir, workdir, model
		FROM session_residents WHERE driver_mode = ? AND push_session = '' ORDER BY session ASC`), ResidentDriverPush)
	if err != nil {
		log().Warn("列首轮在途驻场行失败", "cause", err)
		return nil, fmt.Errorf("session_residents 查询首轮在途行: %w", err)
	}
	defer rows.Close()
	var out []SessionResident
	for rows.Next() {
		var r SessionResident
		var satAt any
		if err := rows.Scan(&r.Session, &r.Identity, &r.DesktopSession, &r.CLISession, &r.Machine, &satAt,
			&r.DriverMode, &r.PushCLI, &r.PushSession, &r.Carrier, &r.CarrierMachine, &r.HomeDir, &r.Workdir, &r.Model); err != nil {
			return nil, fmt.Errorf("扫首轮在途驻场行: %w", err)
		}
		r.SatAt = toTime(satAt)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历首轮在途驻场行: %w", err)
	}
	log().Debug("列首轮在途驻场行完成", "rows", len(out))
	return out, nil
}

// ErrResidentOccupied 表示会话已有驻场者（C-414：一房间至多一名，第二次 sit 一律拒绝）。
var ErrResidentOccupied = errors.New("ledger: 会话已有驻场者")

// ErrResidentNotSeated 表示要请下的身份不是该会话的现驻场者（含无人在座）。
var ErrResidentNotSeated = errors.New("ledger: 该身份不是会话驻场者")

// SitResident 登记会话驻场者（B435 R，`session sit` 的账本半边）。参数 r：Session、Identity
// 必填；桌面端三列可空（空 = 无桌面端地址）；B444 驱动形态与 bearing 列原样落库（形状校验在
// collab.Service.SitResident）；SatAt 由账本按本库时钟填写，入参值忽略。
// 返回：写下的一行（含 SatAt）。错误：入参为空 → ErrBadState；会话不存在 → ErrNotFound；
// 已归档 → ErrBadState；已有驻场者（不论身份，C-414 / Q8 甲）→ ErrResidentOccupied，原登记不变。
//
// 一房间至多一名由主键兑现：`INSERT … ON CONFLICT (session) DO NOTHING`，影响 0 行即被占。
// INSERT 是事务的第一条语句——SQLite 下让事务一开始就取写锁（跨连接排队走 busy_timeout），
// 而不是先读后升级写锁撞 SQLITE_BUSY；会话存在/归档判定在同一事务内随后执行，失败即回滚
// 撤掉刚插的行（无 TOCTOU）。同事务递增席位状态版本（Q6 甲：驻场者是判定表输入，与换绑同样
// 让在途候选页整页重读）。不落账本事件（`AddSessionMember` 先例）。注意：身份记法校验不在
// 本层（d_ledger 不调 proto 包级函数），由 collab.Service.SitResident 唯一入口负责。
func (s *Store) SitResident(r SessionResident) (SessionResident, error) {
	if r.Session == "" || r.Identity == "" {
		err := fmt.Errorf("登记驻场者：会话与身份必填（session=%q identity=%q）: %w", r.Session, r.Identity, ErrBadState)
		log().Warn("登记驻场者被拒：入参为空", "cause", err)
		return SessionResident{}, err
	}
	r.SatAt = s.timeNow()
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		res, err := tx.Exec(s.q(`INSERT INTO session_residents
			(session, identity, desktop_session, cli_session, machine, sat_at,
			 driver_mode, push_cli, push_session, carrier, carrier_machine, home_dir, workdir, model)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (session) DO NOTHING`),
			r.Session, r.Identity, r.DesktopSession, r.CLISession, r.Machine, s.tval(r.SatAt),
			r.DriverMode, r.PushCLI, r.PushSession, r.Carrier, r.CarrierMachine, r.HomeDir, r.Workdir, r.Model)
		if err != nil {
			return fmt.Errorf("写驻场者 session=%s: %w", r.Session, err)
		}
		session, err := getSessionTx(s, tx, r.Session)
		if err != nil {
			return err
		}
		if session.Archived {
			return fmt.Errorf("会话 %s 已归档，不能登记驻场者: %w", r.Session, ErrBadState)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("读驻场者写入影响行数: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("会话 %s: %w", r.Session, ErrResidentOccupied)
		}
		return s.bumpSeatRevisionTx(tx)
	})
	if err != nil {
		if errors.Is(err, ErrResidentOccupied) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrBadState) {
			log().Info("登记驻场者被拒", "session", r.Session, "identity", r.Identity, "cause", err)
		} else {
			log().Warn("登记驻场者失败", "session", r.Session, "identity", r.Identity, "cause", err)
		}
		return SessionResident{}, err
	}
	log().Info("驻场者已登记", "session", r.Session, "identity", r.Identity, "has_desktop", r.DesktopSession != "",
		"driver_mode", r.DriverMode, "carrier", r.Carrier, "carrier_machine", r.CarrierMachine)
	return r, nil
}

// StandResident 请下会话驻场者（`session stand` 的账本半边，Q7 甲）：条件删除
// `WHERE session=? AND identity=?`，只有现驻场者本人能被请下。参数 session、identity 必填。
// 错误：入参为空 → ErrBadState；无人在座或身份不符 → ErrResidentNotSeated（登记不变）。
// 成功时同事务递增席位状态版本（Q6 甲）。不动 session_delivery_cursors：同身份再 sit 时
// 地址游标行续用（S8）。
func (s *Store) StandResident(session, identity string) error {
	if session == "" || identity == "" {
		err := fmt.Errorf("请下驻场者：会话与身份必填（session=%q identity=%q）: %w", session, identity, ErrBadState)
		log().Warn("请下驻场者被拒：入参为空", "cause", err)
		return err
	}
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		res, err := tx.Exec(s.q(`DELETE FROM session_residents WHERE session = ? AND identity = ?`), session, identity)
		if err != nil {
			return fmt.Errorf("删驻场者 session=%s: %w", session, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("读驻场者删除影响行数: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("会话 %s 的驻场者不是 %s: %w", session, identity, ErrResidentNotSeated)
		}
		return s.bumpSeatRevisionTx(tx)
	})
	if err != nil {
		if errors.Is(err, ErrResidentNotSeated) {
			log().Info("请下驻场者被拒", "session", session, "identity", identity, "cause", err)
		} else {
			log().Warn("请下驻场者失败", "session", session, "identity", identity, "cause", err)
		}
		return err
	}
	log().Info("驻场者已请下", "session", session, "identity", identity)
	return nil
}
