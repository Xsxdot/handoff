// B229 缝 2：disciplines 聚合——纪律块正文的账本权威副本。
//
// 与 dispatch_templates 同构：不可变版本化、只插新版、(name, version) 主键
// （store.go 两方言 DDL 同位）。正文是数据不是代码：改一句话 = 发新版本，
// 旧版本永不改写，派发取最新版并把版本号记进 DispatchSnapshot。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// maxDisciplineBody 是纪律块正文的字节上限，与执行机磁盘路径的既有上限同源
// （discipline.Resolver 的 maxBlockSize = 64KiB）。入库不设限等于把「误配一个
// 二进制文件塞爆模型上下文」的旧枪换了个地方上膛。
const maxDisciplineBody = 64 << 10

// Discipline 一份版本化的纪律块正文。
type Discipline struct {
	Name      string
	Version   int
	Body      string
	CreatedAt time.Time
}

// validateDisciplineName 拒绝一切非「纯名字」的取值：路径穿越与散落多处的
// 纪律块都从名字开始防（resolver.resolvePath 同款理由）。
func validateDisciplineName(name string) error {
	if strings.TrimSpace(name) == "" || name == "." || name == ".." ||
		strings.ContainsRune(name, filepath.Separator) || strings.ContainsRune(name, '/') {
		return fmt.Errorf("纪律块名字 %q 不能为空或含路径分隔符", name)
	}
	return nil
}

// PutDiscipline 写入下一版本（不改旧行），返回新版本号。
//
// name 非法、body 为空或超限时拒绝——坏数据挡在写入这一刻，好过第一次派发才炸。
func (s *Store) PutDiscipline(name, body string) (int, error) {
	if err := validateDisciplineName(name); err != nil {
		return 0, err
	}
	if strings.TrimSpace(body) == "" {
		return 0, fmt.Errorf("纪律块 %s 正文不能为空", name)
	}
	if len(body) > maxDisciplineBody {
		return 0, fmt.Errorf("纪律块 %s 正文超过 %d 字节上限（实际 %d）", name, maxDisciplineBody, len(body))
	}
	var ver int
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		if err := tx.QueryRow(s.q(`SELECT COALESCE(MAX(version),0) FROM disciplines WHERE name = ?`),
			name).Scan(&ver); err != nil {
			return fmt.Errorf("查纪律块版本: %w", err)
		}
		ver++
		if _, err := tx.Exec(s.q(`INSERT INTO disciplines (name, version, body, created_at)
			VALUES (?,?,?,?)`), name, ver, body, s.tval(time.Now())); err != nil {
			return fmt.Errorf("写纪律块 %s v%d: %w", name, ver, err)
		}
		return nil
	})
	return ver, err
}

// GetDiscipline 取指定版本；version = 0 取最新。
func (s *Store) GetDiscipline(name string, version int) (Discipline, error) {
	var zero Discipline
	q := `SELECT name, version, body, created_at FROM disciplines WHERE name = ?`
	args := []any{name}
	if version > 0 {
		q += ` AND version = ?`
		args = append(args, version)
	}
	q += ` ORDER BY version DESC LIMIT 1`
	var d Discipline
	var ct any
	err := s.db.QueryRow(s.q(q), args...).Scan(&d.Name, &d.Version, &d.Body, &ct)
	if errors.Is(err, sql.ErrNoRows) {
		return zero, fmt.Errorf("纪律块 %s v%d: %w", name, version, ErrNotFound)
	}
	if err != nil {
		return zero, fmt.Errorf("读纪律块: %w", err)
	}
	d.CreatedAt = toTime(ct)
	return d, nil
}

// ListDisciplineNames 全部纪律块名（去重升序）。
func (s *Store) ListDisciplineNames() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT name FROM disciplines ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("列纪律块名: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
