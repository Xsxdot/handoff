// project_room.go —— B435 O1 项目房间绑定（spec D6「账本记 project ↔ session」；契约 C-513；
// O1 plan Q1/Q2/Q11/Q12 甲）。
//
// 职责：project_rooms 表的读写——每项目至多一场会话（project 主键）、每场会话至多一个项目
// （session 唯一），两方言库层兑现；EnsureProjectRoom 惰性创建（有绑定即返回，无则同事务建会话 +
// 写绑定）。项目房间本身是普通会话（sessions 一行），本文件只多记一条绑定。
// 边界：项目名形状校验、群主取系统发信身份都在调用方 collab.Service.EnsureProjectRoom（本包不调
// proto 包级函数，d_ledger→d_protocol 预算已满）；绑定一经写下不解绑（归档场照旧绑定，Q11 甲）；
// 不拒绝把卡拉进项目房间（Q12 甲：只保证创建时零卡）。不落新的账本事件类型——建会话照常落
// EvSessionCreated。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
)

// projectRoomTitle 是惰性创建的项目房间会话标题。
func projectRoomTitle(project string) string { return "项目房间 " + project }

// EnsureProjectRoom 取项目的项目房间，不存在则创建（`handoff project room` / `session sit --project`
// 的账本半边）。参数：project 为卡 project 字段原值（比较键，调用方已裁剪与校验形状）；owner 为新建
// 会话的群主（也是首个显式成员；命中既有绑定时忽略）；actor 落 session_created 事件。返回：会话本体、
// 是否本次新建。错误：入参为空 → ErrBadState；其余为读写失败（整事务回滚，不留孤儿会话）。
//
// 并发：单 mutate 事务内「读绑定 → 无则建会话 + 写绑定」；PG 由 mutate 的 advisory lock 串行，
// SQLite 由事务写锁串行。事务首条语句是一条空写（见 claimProjectRoomWriteLockTx），让两个进程的
// 并发创建在 busy_timeout 上排队，而不是先读后升级写锁撞 SQLITE_BUSY（R SitResident 同一教训）。
func (s *Store) EnsureProjectRoom(project, owner, actor string) (Session, bool, error) {
	if project == "" || owner == "" {
		err := fmt.Errorf("确保项目房间：项目与群主必填（project=%q owner=%q）: %w", project, owner, ErrBadState)
		log().Warn("确保项目房间被拒：入参为空", "cause", err)
		return Session{}, false, err
	}
	var session Session
	created := false
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if err := s.claimProjectRoomWriteLockTx(tx, project); err != nil {
			return err
		}
		var bound string
		err := tx.QueryRow(s.q(`SELECT session FROM project_rooms WHERE project = ?`), project).Scan(&bound)
		switch {
		case err == nil:
			session, err = getSessionTx(s, tx, bound)
			if err != nil {
				return fmt.Errorf("读项目 %s 的项目房间 %s: %w", project, bound, err)
			}
			return s.attachSessionCards(tx, &session)
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("读项目房间绑定 project=%s: %w", project, err)
		}
		session, err = s.createSessionTx(tx, sink, projectRoomTitle(project), owner, actor)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(s.q(`INSERT INTO project_rooms (project, session, created_at) VALUES (?, ?, ?)`),
			project, session.ID, s.tval(session.CreatedAt)); err != nil {
			return fmt.Errorf("写项目房间绑定 project=%s session=%s: %w", project, session.ID, err)
		}
		created = true
		return nil
	})
	if err != nil {
		log().Warn("确保项目房间失败", "project", project, "cause", err)
		return Session{}, false, err
	}
	if created {
		log().Info("项目房间已创建", "project", project, "session", session.ID, "owner", owner)
	} else {
		log().Debug("项目房间已存在", "project", project, "session", session.ID, "archived", session.Archived)
	}
	return session, created, nil
}

// claimProjectRoomWriteLockTx 是 EnsureProjectRoom 事务的首条语句：一条不改任何值的 UPDATE。
// 为什么：SQLite 下写语句一开始就取事务写锁，跨连接的并发创建在 busy_timeout 上排队；若首条是
// SELECT（读快照）再升级写锁，后到者在 WAL 下直接得 SQLITE_BUSY，busy_timeout 不覆盖（H1 首跑实测：
// 「写会话号水位: database is locked」）。PG 上 mutate 已取 advisory lock，本句无副作用。
func (s *Store) claimProjectRoomWriteLockTx(tx *sql.Tx, project string) error {
	if _, err := tx.Exec(s.q(`UPDATE project_rooms SET created_at = created_at WHERE project = ?`), project); err != nil {
		return fmt.Errorf("取项目房间写锁 project=%s: %w", project, err)
	}
	return nil
}

// ProjectRoom 读项目的项目房间会话 id。返回：无绑定 → ok=false、err=nil（「项目无项目房间」是合法
// 读数）；读失败 → 带项目上下文的错误（调用方不得当作无项目房间）。
func (s *Store) ProjectRoom(project string) (string, bool, error) {
	var session string
	err := s.db.QueryRow(s.q(`SELECT session FROM project_rooms WHERE project = ?`), project).Scan(&session)
	if errors.Is(err, sql.ErrNoRows) {
		log().Debug("项目无项目房间", "project", project)
		return "", false, nil
	}
	if err != nil {
		log().Warn("读项目房间绑定失败", "project", project, "cause", err)
		return "", false, fmt.Errorf("project_rooms 查询 project=%s: %w", project, err)
	}
	log().Debug("读项目房间绑定完成", "project", project, "session", session)
	return session, true, nil
}

// SessionProject 读会话绑定的项目名（SessionSummary.project 的数据源，C-512）。返回：非项目房间 →
// 空串、err=nil；读失败 → 带会话上下文的错误（调用方不得当作非项目房间）。
func (s *Store) SessionProject(session string) (string, error) {
	var project string
	err := s.db.QueryRow(s.q(`SELECT project FROM project_rooms WHERE session = ?`), session).Scan(&project)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		log().Warn("读会话项目绑定失败", "session", session, "cause", err)
		return "", fmt.Errorf("project_rooms 查询 session=%s: %w", session, err)
	}
	return project, nil
}
