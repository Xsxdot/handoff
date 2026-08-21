// 账本库的打开、schema、方言吸收与事务包裹。方言差异只允许出现在
// 本文件的四个点：driver 选择、DDL 变体、q() 占位符重写、tval()/toTime()
// 时间编解码、notify()。其余文件写方言无关的 SQL。
package ledger

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // PG driver: "pgx"
	_ "modernc.org/sqlite"             // SQLite driver: "sqlite"
)

type dialect int

const (
	dialectSQLite dialect = iota
	dialectPG
)

// Store 账本库句柄。并发安全；Close 后不可用。
type Store struct {
	db      *sql.DB
	dialect dialect
	path    string // 仅 SQLite：文件路径（测试用）
	dsn     string // 原始 DSN（Follow 的 PG LISTEN 需要开第二条裸连接）

	mu        sync.Mutex
	listeners []func(seq int64) // SQLite 回退模式的进程内事件推送
}

// Open 打开账本库并幂等建 schema。dsn 以 postgres:// 或 postgresql://
// 开头走 PG，否则视为 SQLite 文件路径（单机回退模式）。
func Open(dsn string) (*Store, error) {
	s := &Store{dsn: dsn}
	var err error
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		s.dialect = dialectPG
		s.db, err = sql.Open("pgx", dsn)
	} else {
		s.dialect = dialectSQLite
		s.path = dsn
		s.db, err = sql.Open("sqlite", dsn+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)")
		// database/sql 默认会开多个连接；SQLite 文件锁只保证最终写入互斥，
		// 不能阻止两个事务同时读到同一个 B 号水位。单连接把账本的“读-判-写”
		// 串成真正的单写者，兑现 mutate 的并发模型。
		s.db.SetMaxOpenConns(1)
		s.db.SetMaxIdleConns(1)
	}
	if err != nil {
		return nil, fmt.Errorf("打开账本库: %w", err)
	}
	if err := s.db.Ping(); err != nil {
		s.db.Close()
		return nil, fmt.Errorf("连接账本库: %w", err)
	}
	if err := s.ensureSchema(); err != nil {
		s.db.Close()
		return nil, fmt.Errorf("建账本 schema: %w", err)
	}
	dialectName := "postgres"
	if s.dialect == dialectSQLite {
		dialectName = "sqlite"
	}
	log().Info("账本库已打开", "dialect", dialectName, "target", redactDSN(dsn, s.dialect))
	return s, nil
}

// redactDSN 保留 Open 日志需要的目标定位信息，同时不把 PG URL 中的密码
// 写进日志；SQLite 路径没有凭据，原样保留。
func redactDSN(dsn string, d dialect) string {
	if d != dialectPG {
		return dsn
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return "<postgres dsn>"
	}
	if u.User != nil {
		u.User = url.User(u.User.Username())
	}
	return u.String()
}

// Close 关闭底层连接。
func (s *Store) Close() error { return s.db.Close() }

// q 把 ? 占位符重写为 PG 的 $N。仅做占位符转换，不做任何转义——
// SQL 里不许出现字面 '?'（本包 SQL 全部自控，无用户拼接）。
func (s *Store) q(query string) string {
	if s.dialect != dialectPG {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// tval 时间入库编码：PG 直接传 time.Time（timestamptz），SQLite 存
// RFC3339Nano 文本。
func (s *Store) tval(t time.Time) any {
	if s.dialect == dialectPG {
		return t.UTC()
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// toTime 时间出库解码，兼容两方言的扫描产物。
func toTime(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case string:
		parsed, _ := time.Parse(time.RFC3339Nano, t)
		return parsed
	case []byte:
		parsed, _ := time.Parse(time.RFC3339Nano, string(t))
		return parsed
	}
	return time.Time{}
}

// eventSink 收集本事务内 append 的事件 seq，供提交后做 SQLite 进程内
// 推送（PG 的推送在事务内 pg_notify，由 LISTEN 端收）。
type eventSink struct{ seqs []int64 }

// mutate 写事务包裹：PG 先拿全局 advisory lock 串行化全部账本写
// （B 号分配的 max+1、环检测的读全图、合并校验都要求读-判-写原子；
// 写 QPS 极小，一把粗锁换正确性，蓝图 §3.1 记过这笔账）；SQLite 由
// 单写者天然串行。fn 内经 sink append 的事件在提交后触发本地 listeners。
func (s *Store) mutate(fn func(tx *sql.Tx, sink *eventSink) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("开账本事务: %w", err)
	}
	defer tx.Rollback() // 提交后是 no-op
	if s.dialect == dialectPG {
		if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(915601)`); err != nil {
			return fmt.Errorf("取账本写锁: %w", err)
		}
	}
	sink := &eventSink{}
	if err := fn(tx, sink); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交账本事务: %w", err)
	}
	if s.dialect == dialectSQLite && len(sink.seqs) > 0 {
		s.mu.Lock()
		listeners := append([]func(int64){}, s.listeners...)
		s.mu.Unlock()
		for _, listener := range listeners {
			for _, seq := range sink.seqs {
				listener(seq)
			}
		}
	}
	return nil
}

// OnEvent 注册进程内事件回调（仅 SQLite 回退模式有意义；PG 模式下
// 消费者应走 LISTEN card_events）。回调在提交后同步触发，勿做慢活。
func (s *Store) OnEvent(fn func(seq int64)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners = append(s.listeners, fn)
}

// ensureSchema 幂等建表。与 internal/store 相同的策略：CREATE IF NOT
// EXISTS 列表顺序执行，无版本号——幂等即契约；后续加列走「ALTER +
// 容忍 duplicate column」（现在还没有第二版，留到真需要时加）。
func (s *Store) ensureSchema() error {
	var ddl []string
	if s.dialect == dialectPG {
		ddl = []string{
			`CREATE TABLE IF NOT EXISTS cards (
				id TEXT PRIMARY KEY, title TEXT NOT NULL, status TEXT NOT NULL,
				terminate_reason TEXT, priority TEXT NOT NULL DEFAULT '中',
				project TEXT NOT NULL, parent_id TEXT REFERENCES cards(id),
				workflow_name TEXT NOT NULL, workflow_version INT NOT NULL,
				attachments JSONB NOT NULL DEFAULT '[]', acceptance_criteria TEXT,
				base_branch TEXT, driver_session TEXT, driver_heartbeat_at TIMESTAMPTZ,
				created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL)`,
			`CREATE INDEX IF NOT EXISTS idx_cards_board ON cards(project, status)`,
			`CREATE INDEX IF NOT EXISTS idx_cards_parent ON cards(parent_id)`,
			`CREATE TABLE IF NOT EXISTS card_relations (
				from_id TEXT NOT NULL REFERENCES cards(id),
				to_id TEXT NOT NULL REFERENCES cards(id),
				type TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL,
				PRIMARY KEY (from_id, to_id, type))`,
			`CREATE UNIQUE INDEX IF NOT EXISTS uq_rel_merged_into
				ON card_relations(from_id) WHERE type = 'merged_into'`,
			`CREATE INDEX IF NOT EXISTS idx_rel_to ON card_relations(to_id, type)`,
			`CREATE TABLE IF NOT EXISTS card_tasks (
				card_id TEXT NOT NULL REFERENCES cards(id),
				target TEXT NOT NULL, task_id TEXT NOT NULL, purpose TEXT NOT NULL,
				created_at TIMESTAMPTZ NOT NULL, PRIMARY KEY (target, task_id))`,
			`CREATE INDEX IF NOT EXISTS idx_card_tasks_card ON card_tasks(card_id)`,
			`CREATE TABLE IF NOT EXISTS card_events (
				seq BIGSERIAL PRIMARY KEY, card_id TEXT REFERENCES cards(id),
				type TEXT NOT NULL, actor TEXT NOT NULL, payload JSONB NOT NULL,
				source_target TEXT, source_task TEXT, source_seq BIGINT,
				created_at TIMESTAMPTZ NOT NULL)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS uq_events_mirror
				ON card_events(source_target, source_task, source_seq)
				WHERE source_target IS NOT NULL`,
			`CREATE INDEX IF NOT EXISTS idx_events_card ON card_events(card_id, seq)`,
			`CREATE TABLE IF NOT EXISTS workflows (
				name TEXT NOT NULL, version INT NOT NULL, definition JSONB NOT NULL,
				created_at TIMESTAMPTZ NOT NULL, PRIMARY KEY (name, version))`,
			`CREATE TABLE IF NOT EXISTS dispatch_templates (
				name TEXT NOT NULL, version INT NOT NULL, definition JSONB NOT NULL,
				created_at TIMESTAMPTZ NOT NULL, PRIMARY KEY (name, version))`,
			`CREATE TABLE IF NOT EXISTS decisions (
				id BIGSERIAL PRIMARY KEY, card_id TEXT REFERENCES cards(id),
				body TEXT NOT NULL, options JSONB,
				status TEXT NOT NULL DEFAULT 'open', created_by TEXT NOT NULL,
				answer TEXT, answered_by TEXT,
				created_at TIMESTAMPTZ NOT NULL, answered_at TIMESTAMPTZ)`,
			`CREATE INDEX IF NOT EXISTS idx_decisions_open ON decisions(status) WHERE status = 'open'`,
			`CREATE TABLE IF NOT EXISTS mirror_lease (
				id INT PRIMARY KEY CHECK (id = 1),
				holder TEXT NOT NULL, lease_until TIMESTAMPTZ NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS mirror_cursors (
				target TEXT PRIMARY KEY, last_seq BIGINT NOT NULL,
				updated_at TIMESTAMPTZ NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS ledger_meta (
				key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		}
	} else {
		// SQLite 回退映射（spec §2.1 文末注）：BIGSERIAL→AUTOINCREMENT、
		// JSONB→TEXT、TIMESTAMPTZ→TEXT(RFC3339)、partial index→应用层校验
		// （merged_into 至多一条与镜像幂等在写路径校验，见 merge.go/events.go）。
		ddl = []string{
			`CREATE TABLE IF NOT EXISTS cards (
				id TEXT PRIMARY KEY, title TEXT NOT NULL, status TEXT NOT NULL,
				terminate_reason TEXT, priority TEXT NOT NULL DEFAULT '中',
				project TEXT NOT NULL, parent_id TEXT REFERENCES cards(id),
				workflow_name TEXT NOT NULL, workflow_version INTEGER NOT NULL,
				attachments TEXT NOT NULL DEFAULT '[]', acceptance_criteria TEXT,
				base_branch TEXT, driver_session TEXT, driver_heartbeat_at TEXT,
				created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
			`CREATE INDEX IF NOT EXISTS idx_cards_board ON cards(project, status)`,
			`CREATE INDEX IF NOT EXISTS idx_cards_parent ON cards(parent_id)`,
			`CREATE TABLE IF NOT EXISTS card_relations (
				from_id TEXT NOT NULL REFERENCES cards(id),
				to_id TEXT NOT NULL REFERENCES cards(id),
				type TEXT NOT NULL, created_at TEXT NOT NULL,
				PRIMARY KEY (from_id, to_id, type))`,
			`CREATE INDEX IF NOT EXISTS idx_rel_to ON card_relations(to_id, type)`,
			`CREATE TABLE IF NOT EXISTS card_tasks (
				card_id TEXT NOT NULL REFERENCES cards(id),
				target TEXT NOT NULL, task_id TEXT NOT NULL, purpose TEXT NOT NULL,
				created_at TEXT NOT NULL, PRIMARY KEY (target, task_id))`,
			`CREATE INDEX IF NOT EXISTS idx_card_tasks_card ON card_tasks(card_id)`,
			`CREATE TABLE IF NOT EXISTS card_events (
				seq INTEGER PRIMARY KEY AUTOINCREMENT, card_id TEXT REFERENCES cards(id),
				type TEXT NOT NULL, actor TEXT NOT NULL, payload TEXT NOT NULL,
				source_target TEXT, source_task TEXT, source_seq INTEGER,
				created_at TEXT NOT NULL)`,
			// SQLite 的 UNIQUE 索引允许多行含 NULL 的键并存（NULL != NULL），
			// 所以无需 PG partial WHERE 也能只约束非空镜像三元组。
			`CREATE UNIQUE INDEX IF NOT EXISTS uq_events_mirror
				ON card_events(source_target, source_task, source_seq)`,
			`CREATE INDEX IF NOT EXISTS idx_events_card ON card_events(card_id, seq)`,
			`CREATE TABLE IF NOT EXISTS workflows (
				name TEXT NOT NULL, version INTEGER NOT NULL, definition TEXT NOT NULL,
				created_at TEXT NOT NULL, PRIMARY KEY (name, version))`,
			`CREATE TABLE IF NOT EXISTS dispatch_templates (
				name TEXT NOT NULL, version INTEGER NOT NULL, definition TEXT NOT NULL,
				created_at TEXT NOT NULL, PRIMARY KEY (name, version))`,
			`CREATE TABLE IF NOT EXISTS decisions (
				id INTEGER PRIMARY KEY AUTOINCREMENT, card_id TEXT REFERENCES cards(id),
				body TEXT NOT NULL, options TEXT,
				status TEXT NOT NULL DEFAULT 'open', created_by TEXT NOT NULL,
				answer TEXT, answered_by TEXT,
				created_at TEXT NOT NULL, answered_at TEXT)`,
			`CREATE INDEX IF NOT EXISTS idx_decisions_open ON decisions(status)`,
			`CREATE TABLE IF NOT EXISTS mirror_lease (
				id INTEGER PRIMARY KEY CHECK (id = 1),
				holder TEXT NOT NULL, lease_until TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS mirror_cursors (
				target TEXT PRIMARY KEY, last_seq INTEGER NOT NULL,
				updated_at TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS ledger_meta (
				key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		}
	}
	for _, stmt := range ddl {
		if _, err := s.db.Exec(stmt); err != nil {
			short := stmt
			if len(short) > 40 {
				short = short[:40]
			}
			return fmt.Errorf("执行 DDL %q: %w", short, err)
		}
	}
	return nil
}
