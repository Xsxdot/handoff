// 本文件实现浏览器鉴权所需的两张表：会话（sessions）与一次性 ticket（auth_tickets）。
//
// 职责：
//   - 凭据哈希：两张表都只存 SHA-256，明文永不落库
//   - ticket 的创建与**原子消费**（一次性由一条条件 UPDATE 的原子性保证）
//   - 会话的创建、按哈希/按 id 查询、列出、吊销、活跃与续期写入
//
// 边界：
//   - 不判断会话/ticket 是否「有效」：过期与吊销的判定属业务规则，由 agentd 侧做
//     （store 是叶子层；且 spec §11 要求把「会话过期」与「会话已吊销」作为不同
//     原因分别记日志，合并判定就拿不出这个区分）
//   - 不生成凭据明文、不设置 cookie、不做任何 HTTP 相关处理
//   - 叶子层纪律：本层方法错误 return 前不打日志（避免双份），由调用方带上下文记录。
//     签发、消费、建立、吊销四个节点的 Info 日志由 agentd 侧（Task 4/6 的调用方）打，
//     不在本文件任何方法里加日志——这条责任转移是刻意的，不是漏了
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// Session 是一个浏览器会话记录。
//
// 注意：
//   - TokenHash 是 cookie 明文的 SHA-256；明文不落库，本结构体永远拿不到它
//   - RevokedAt 为 nil 表示未吊销；是否「有效」还要看 ExpiresAt，由调用方判定
type Session struct {
	ID         string
	TokenHash  string
	DeviceName string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
	RevokedAt  *time.Time
}

// HashCredential 计算凭据明文的 SHA-256 十六进制串。
//
// 参数：
//   - plain: 凭据明文（ticket 或 cookie 值）
//
// 返回：
//   - 64 字符的小写十六进制哈希
//
// 注意：
//   - 为什么不加盐、不用 bcrypt：输入是 256 位高熵随机串（不是人选的口令），
//     没有字典攻击面；而查表必须是 O(1) 精确匹配，加盐会退化成逐行比对
func HashCredential(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// CreateAuthTicket 落库一张一次性 ticket。
//
// 参数：
//   - hash: ticket 明文的 SHA-256（由调用方算好；本层不接触明文）
//   - deviceName: 签发时登记的设备名，纯展示
//   - createdAt / expiresAt: 签发与过期时刻
//
// 返回：
//   - 数据库错误；重复 hash 会因主键冲突报错（概率上不可能，视为真故障）
func (s *Store) CreateAuthTicket(hash, deviceName string, createdAt, expiresAt time.Time) error {
	if _, err := s.db.ExecContext(context.Background(), `
INSERT INTO auth_tickets (id, device_name, created_at, expires_at, consumed_at)
VALUES (?, ?, ?, ?, NULL)`,
		hash, deviceName, fmtTime(createdAt), fmtTime(expiresAt)); err != nil {
		return fmt.Errorf("写入 ticket: %w", err)
	}
	return nil
}

// ConsumeAuthTicket 原子认领一张未消费的 ticket，并返回它的设备名与过期时刻。
//
// 参数：
//   - hash: ticket 明文的 SHA-256
//   - now: 认领时刻，写入 consumed_at
//
// 返回：
//   - deviceName / expiresAt: 该 ticket 的登记信息
//   - ErrNotFound: 不存在或已被消费（对调用方是同一个结论：这张票不能用）
//
// 注意：
//   - 一次性**由 SQL 条件 UPDATE 的原子性保证**，不靠「先查后改」——后者在并发下
//     会让同一张 ticket 换出两个会话
//   - **过期不在这里判**：返回 expiresAt 交给调用方比较。原因是本表的时间以
//     RFC3339Nano 文本存储，Go 的格式化会裁掉尾部零，导致「整秒」与「带小数秒」
//     的两个时刻在 SQLite 里按字典序比较时次序反转（`.` 的码位小于 `Z`）。
//     把比较放到 Go 侧用 time.Time 做，才是无坑的
func (s *Store) ConsumeAuthTicket(hash string, now time.Time) (string, time.Time, error) {
	res, err := s.db.ExecContext(context.Background(), `
UPDATE auth_tickets SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL`,
		fmtTime(now), hash)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("消费 ticket: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("读取 ticket 影响行数: %w", err)
	}
	if n != 1 {
		return "", time.Time{}, ErrNotFound
	}
	var deviceName, expiresAt string
	if err := s.db.QueryRowContext(context.Background(),
		"SELECT device_name, expires_at FROM auth_tickets WHERE id = ?", hash).
		Scan(&deviceName, &expiresAt); err != nil {
		return "", time.Time{}, fmt.Errorf("读取 ticket 登记信息: %w", err)
	}
	return deviceName, parseTime(expiresAt), nil
}

// CreateSession 落库一个新会话。
//
// 参数：
//   - sess: 会话数据；ID 与 TokenHash 必须非空，RevokedAt 被忽略（新会话必未吊销）
//
// 返回：
//   - 数据库错误
func (s *Store) CreateSession(sess *Session) error {
	if _, err := s.db.ExecContext(context.Background(), `
INSERT INTO sessions (id, token_hash, device_name, created_at, expires_at, last_seen_at, revoked_at)
VALUES (?, ?, ?, ?, ?, ?, NULL)`,
		sess.ID, sess.TokenHash, sess.DeviceName,
		fmtTime(sess.CreatedAt), fmtTime(sess.ExpiresAt), fmtTime(sess.LastSeenAt)); err != nil {
		return fmt.Errorf("写入会话 %s: %w", sess.ID, err)
	}
	return nil
}

// sessionColumns 是会话查询的固定列序，与 scanSession 一一对应。
const sessionColumns = "id, token_hash, device_name, created_at, expires_at, last_seen_at, revoked_at"

// scanSession 按 sessionColumns 的列序把一行扫成 Session。
func scanSession(sc interface{ Scan(...any) error }) (*Session, error) {
	var (
		sess      Session
		created   string
		expires   string
		lastSeen  string
		revokedAt sql.NullString
	)
	if err := sc.Scan(&sess.ID, &sess.TokenHash, &sess.DeviceName,
		&created, &expires, &lastSeen, &revokedAt); err != nil {
		return nil, err
	}
	sess.CreatedAt = parseTime(created)
	sess.ExpiresAt = parseTime(expires)
	sess.LastSeenAt = parseTime(lastSeen)
	if revokedAt.Valid {
		t := parseTime(revokedAt.String)
		sess.RevokedAt = &t
	}
	return &sess, nil
}

// SessionByTokenHash 按 cookie 哈希查会话；不存在返回 ErrNotFound。
//
// 注意：已过期或已吊销的会话**照样返回**——有效性由调用方判定，这样才能把
// 「不存在 / 已过期 / 已吊销」记成三种不同的鉴权失败原因
func (s *Store) SessionByTokenHash(hash string) (*Session, error) {
	row := s.db.QueryRowContext(context.Background(),
		"SELECT "+sessionColumns+" FROM sessions WHERE token_hash = ?", hash)
	sess, err := scanSession(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("按哈希查询会话: %w", err)
	}
	return sess, nil
}

// SessionByID 按会话 id 查会话；不存在返回 ErrNotFound。
func (s *Store) SessionByID(id string) (*Session, error) {
	row := s.db.QueryRowContext(context.Background(),
		"SELECT "+sessionColumns+" FROM sessions WHERE id = ?", id)
	sess, err := scanSession(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("按 id 查询会话 %s: %w", id, err)
	}
	return sess, nil
}

// ListSessions 列出全部会话（含已吊销与已过期），按创建时刻降序。
//
// 注意：已吊销的会话也返回——`handoff sessions` 要能看到「这台设备已经被吊销了」，
// 直接消失反而让人怀疑是不是漏看了
func (s *Store) ListSessions() ([]Session, error) {
	rows, err := s.db.QueryContext(context.Background(),
		"SELECT "+sessionColumns+" FROM sessions ORDER BY created_at DESC")
	if err != nil {
		return nil, fmt.Errorf("列出会话: %w", err)
	}
	defer rows.Close()
	out := make([]Session, 0, 8)
	for rows.Next() {
		sess, serr := scanSession(rows)
		if serr != nil {
			return nil, fmt.Errorf("扫描会话行: %w", serr)
		}
		out = append(out, *sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历会话: %w", err)
	}
	return out, nil
}

// RevokeSession 吊销一个尚未吊销的会话。
//
// 返回：
//   - ErrNotFound: 会话不存在**或**已被吊销（两者对调用方是同一个结论：这次吊销没改变什么）
func (s *Store) RevokeSession(id string, at time.Time) error {
	res, err := s.db.ExecContext(context.Background(),
		"UPDATE sessions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL",
		fmtTime(at), id)
	if err != nil {
		return fmt.Errorf("吊销会话 %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取吊销影响行数: %w", err)
	}
	if n != 1 {
		return ErrNotFound
	}
	return nil
}

// TouchSession 写回会话的最后活跃时刻与过期时刻（滑动续期）。
//
// 注意：调用方负责节流——本方法每次调用都真写库
func (s *Store) TouchSession(id string, lastSeen, expiresAt time.Time) error {
	if _, err := s.db.ExecContext(context.Background(),
		"UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id = ?",
		fmtTime(lastSeen), fmtTime(expiresAt), id); err != nil {
		return fmt.Errorf("更新会话 %s 活跃时刻: %w", id, err)
	}
	return nil
}
