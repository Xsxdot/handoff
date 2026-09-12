// 会话（群）域的账本存储（B358）。会话是工作单元：人 / 主 agent 开的一场
// 会话，卡是会话里的工作项（一卡同时只挂一个会话）。会话生命周期显式归档；
// 卡的终态不等于会话结束。成员库不落表——成员由「显式人与主 agent + 会话内
// 各卡的当前席位」派生，本文件只存会话本体与卡↔会话归属。
//
// 权威位置裁决（B358 契约拍板）：会话本体与归属在账本域；会话内席位仍权威在
// cards.driver_session（B312/B307 不动），本域只读席位投影。
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Session 是一场会话（群）的账本投影。
type Session struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Owner    string `json:"owner"` // 群主：人或主 agent 会话身份（升级终点）
	Archived bool   `json:"archived"`
	// Members 显式成员身份（人与主 agent 的外部会话身份）。席位成员不落此列
	// ——由会话内各卡的当前席位在投影期派生（不新增成员表）。
	Members   []string  `json:"members,omitempty"`
	Cards     []string  `json:"cards,omitempty"` // 会话内卡号（一卡同时只挂一个会话）
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// sessionCounterKey 是会话号分配器的 ledger_meta 键。跨方言同形，避免依赖
// SUBSTR/CAST 的方言差异。
const sessionCounterKey = "session_seq"

// CreateSession 建一场会话并落 EvSessionCreated（无卡事件）。返回带分配 id
// 的会话。id 形如 session:<n>，由账本单调分配、不复用。
func (s *Store) CreateSession(title, owner, actor string) (Session, error) {
	if owner == "" {
		return Session{}, fmt.Errorf("会话群主不能为空")
	}
	var session Session
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		next, err := s.nextSessionSeqTx(tx)
		if err != nil {
			return err
		}
		now := s.timeNow()
		session = Session{
			ID: "session:" + strconv.FormatInt(next, 10), Title: title, Owner: owner,
			Members:   []string{owner},
			CreatedAt: now, UpdatedAt: now,
		}
		members, err := encodeMembers(session.Members)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(s.q(`INSERT INTO sessions (id, title, owner, archived, members, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`),
			session.ID, session.Title, session.Owner, false, members,
			s.tval(session.CreatedAt), s.tval(session.UpdatedAt)); err != nil {
			return fmt.Errorf("写会话 %s: %w", session.ID, err)
		}
		payload := map[string]any{"id": session.ID, "title": session.Title, "owner": session.Owner}
		if _, err := s.appendEvent(tx, sink, "", EvSessionCreated, actor, payload); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return Session{}, err
	}
	return session, nil
}

// nextSessionSeqTx 事务内分配下一个会话号（读-增-写 ledger_meta 计数器）。
func (s *Store) nextSessionSeqTx(tx *sql.Tx) (int64, error) {
	var raw string
	err := tx.QueryRow(s.q(`SELECT value FROM ledger_meta WHERE key = ?`), sessionCounterKey).Scan(&raw)
	current := int64(0)
	if err == nil {
		current, _ = strconv.ParseInt(raw, 10, 64)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("读会话号水位: %w", err)
	}
	next := current + 1
	if _, err := tx.Exec(s.q(`INSERT INTO ledger_meta (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`), sessionCounterKey, strconv.FormatInt(next, 10)); err != nil {
		return 0, fmt.Errorf("写会话号水位: %w", err)
	}
	return next, nil
}

// GetSession 读单会话；不存在返回 ErrNotFound。
func (s *Store) GetSession(id string) (Session, error) {
	session, err := scanSession(s.db.QueryRow(s.q(sessionSelect+` WHERE id = ?`), id))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, fmt.Errorf("会话 %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return Session{}, err
	}
	if err := s.attachSessionCards(s.db, &session); err != nil {
		return Session{}, err
	}
	return session, nil
}

// ListSessions 列全部会话（含归档），按 id 升序（=创建序）。
func (s *Store) ListSessions() ([]Session, error) {
	rows, err := s.db.Query(s.q(sessionSelect + ` ORDER BY id ASC`))
	if err != nil {
		return nil, fmt.Errorf("列会话: %w", err)
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("扫会话行: %w", err)
		}
		out = append(out, session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.attachSessionCards(s.db, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ArchiveSession 显式归档会话；归档后只读，幂等。
func (s *Store) ArchiveSession(id, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getSessionTx(s, tx, id); err != nil {
			return err
		}
		if _, err := tx.Exec(s.q(`UPDATE sessions SET archived = ?, updated_at = ? WHERE id = ?`),
			true, s.tval(s.timeNow()), id); err != nil {
			return fmt.Errorf("归档会话 %s: %w", id, err)
		}
		if _, err := s.appendEvent(tx, sink, "", EvSessionArchived, actor,
			map[string]string{"session": id}); err != nil {
			return err
		}
		return nil
	})
}

// JoinCardToSession 把卡拉进会话：卡必须存在、会话必须存在且未归档、卡必须
// 当前不属于任何会话（一卡同时只挂一个会话），全部判定与写入在同一 mutate
// 事务内完成（唯一索引 uq_session_cards_card 是最后一道兜底）。落
// EvSessionCardJoined（无卡事件）。进群 ≠ 配人——本方法不碰卡席位。
func (s *Store) JoinCardToSession(sessionID, cardID, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		session, err := getSessionTx(s, tx, sessionID)
		if err != nil {
			return err
		}
		if session.Archived {
			return fmt.Errorf("会话 %s 已归档: %w", sessionID, ErrBadState)
		}
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("拉卡进群: %w", err)
		}
		if owner, err := sessionOfCardTx(s, tx, cardID); err != nil {
			return err
		} else if owner != "" && owner != sessionID {
			return fmt.Errorf("卡 %s 已属会话 %s: %w", cardID, owner, ErrBadState)
		}
		if _, err := tx.Exec(s.q(`INSERT INTO session_cards (session_id, card_id, created_at)
			VALUES (?, ?, ?) ON CONFLICT (session_id, card_id) DO NOTHING`),
			sessionID, cardID, s.tval(s.timeNow())); err != nil {
			return fmt.Errorf("写会话卡归属 %s/%s: %w", sessionID, cardID, err)
		}
		if _, err := tx.Exec(s.q(`UPDATE sessions SET updated_at = ? WHERE id = ?`),
			s.tval(s.timeNow()), sessionID); err != nil {
			return fmt.Errorf("触会话 %s: %w", sessionID, err)
		}
		if _, err := s.appendEvent(tx, sink, "", EvSessionCardJoined, actor,
			map[string]string{"session": sessionID, "card": cardID}); err != nil {
			return err
		}
		return nil
	})
}

// LeaveCardToSession 把卡移出会话；幂等（不在会话内返回 nil）。落
// EvSessionCardLeft。
func (s *Store) LeaveCardToSession(sessionID, cardID, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getSessionTx(s, tx, sessionID); err != nil {
			return err
		}
		res, err := tx.Exec(s.q(`DELETE FROM session_cards WHERE session_id = ? AND card_id = ?`),
			sessionID, cardID)
		if err != nil {
			return fmt.Errorf("移卡出群 %s/%s: %w", sessionID, cardID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("读移卡影响行数: %w", err)
		}
		if n == 0 {
			return nil
		}
		if _, err := tx.Exec(s.q(`UPDATE sessions SET updated_at = ? WHERE id = ?`),
			s.tval(s.timeNow()), sessionID); err != nil {
			return fmt.Errorf("触会话 %s: %w", sessionID, err)
		}
		if _, err := s.appendEvent(tx, sink, "", EvSessionCardLeft, actor,
			map[string]string{"session": sessionID, "card": cardID}); err != nil {
			return err
		}
		return nil
	})
}

// AddSessionMember 把一个显式成员（人或主 agent 外部会话身份）记入会话；
// 幂等（已在成员列返回 nil）。席位成员不入此列——那是开会话时按卡当前席位
// 派生出来的投影，不落成员表。
func (s *Store) AddSessionMember(sessionID, identity, actor string) error {
	if identity == "" {
		return fmt.Errorf("会话成员身份不能为空")
	}
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		session, err := getSessionTx(s, tx, sessionID)
		if err != nil {
			return err
		}
		if session.Archived {
			return fmt.Errorf("会话 %s 已归档: %w", sessionID, ErrBadState)
		}
		for _, m := range session.Members {
			if m == identity {
				return nil
			}
		}
		session.Members = append(session.Members, identity)
		members, err := encodeMembers(session.Members)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(s.q(`UPDATE sessions SET members = ?, updated_at = ? WHERE id = ?`),
			members, s.tval(s.timeNow()), sessionID); err != nil {
			return fmt.Errorf("写会话 %s 成员: %w", sessionID, err)
		}
		return nil
	})
}

// SessionOfCard 返回该卡当前所属会话 id；不属于任何会话返回空串。
func (s *Store) SessionOfCard(cardID string) (string, error) {
	var sessionID string
	err := s.db.QueryRow(s.q(`SELECT session_id FROM session_cards WHERE card_id = ?`), cardID).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("读卡 %s 的会话: %w", cardID, err)
	}
	return sessionID, nil
}

// sessionOfCardTx 事务内版 SessionOfCard。
func sessionOfCardTx(s *Store, tx *sql.Tx, cardID string) (string, error) {
	var sessionID string
	err := tx.QueryRow(s.q(`SELECT session_id FROM session_cards WHERE card_id = ?`), cardID).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("读卡 %s 的会话: %w", cardID, err)
	}
	return sessionID, nil
}

func getSessionTx(s *Store, tx *sql.Tx, id string) (Session, error) {
	session, err := scanSession(tx.QueryRow(s.q(sessionSelect+` WHERE id = ?`), id))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, fmt.Errorf("会话 %s: %w", id, ErrNotFound)
	}
	return session, err
}

const sessionSelect = `SELECT id, title, owner, archived, members, created_at, updated_at FROM sessions`

func scanSession(row rowScanner) (Session, error) {
	var session Session
	var archived any
	var members string
	var createdAt, updatedAt any
	if err := row.Scan(&session.ID, &session.Title, &session.Owner, &archived, &members, &createdAt, &updatedAt); err != nil {
		return Session{}, err
	}
	session.Archived = parseBool(archived)
	decoded, err := decodeMembers(members)
	if err != nil {
		return Session{}, fmt.Errorf("解码会话 %s 成员: %w", session.ID, err)
	}
	session.Members = decoded
	session.CreatedAt, session.UpdatedAt = toTime(createdAt), toTime(updatedAt)
	return session, nil
}

// encodeMembers 把成员身份列编成 JSONB/TEXT；空切片编成 '[]'。
func encodeMembers(members []string) (string, error) {
	if len(members) == 0 {
		return "[]", nil
	}
	raw, err := json.Marshal(members)
	if err != nil {
		return "", fmt.Errorf("编码会话成员: %w", err)
	}
	return string(raw), nil
}

// decodeMembers 解码成员身份列；空串按空切片（兼容旧行）。
func decodeMembers(raw string) ([]string, error) {
	if raw == "" || raw == "null" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// parseBool 兼容 PG bool 与 SQLite INTEGER 两方言的扫描产物。
func parseBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int64:
		return t != 0
	case int:
		return t != 0
	case string:
		return t == "1" || t == "true"
	case []byte:
		return string(t) == "1" || string(t) == "true"
	}
	return false
}

// attachSessionCards 填充会话的卡号列表，按卡号升序。
func (s *Store) attachSessionCards(db queryer, session *Session) error {
	rows, err := db.Query(s.q(`SELECT card_id FROM session_cards WHERE session_id = ? ORDER BY card_id`), session.ID)
	if err != nil {
		return fmt.Errorf("读会话 %s 卡列表: %w", session.ID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cardID string
		if err := rows.Scan(&cardID); err != nil {
			return fmt.Errorf("扫会话卡行: %w", err)
		}
		session.Cards = append(session.Cards, cardID)
	}
	return rows.Err()
}

// queryer 是 *sql.DB 与 *sql.Tx 的公共读面（attachSessionCards 的查询器）。
type queryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}
