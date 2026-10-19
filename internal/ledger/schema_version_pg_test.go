// schema 版本闸的 PG 竖切（spec ledger-schema-version S1/S2b）。默认 skip，设
// LEDGER_TEST_PG_DSN 后启用。S2（标记过期时仍有界）见 open_bounded_pg_test.go。
package ledger

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// readSchemaMarker 直读库内 schema 版本标记；无行返回 -1。
func readSchemaMarker(t *testing.T, db *sql.DB) int {
	t.Helper()
	var raw string
	err := db.QueryRow(`SELECT value FROM ledger_meta WHERE key = $1`, schemaVersionKey).Scan(&raw)
	if err == sql.ErrNoRows {
		return -1
	}
	if err != nil {
		t.Fatalf("读 schema 版本标记: %v", err)
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("schema 版本标记不是整数: %q", raw)
	}
	return v
}

// S1：库已是当前版本时，他人持 cards 的 ACCESS SHARE 锁，Open 照样秒回——
// 若 Open 仍排 ALTER（AccessExclusiveLock），它会等满 lock_timeout 后失败。
func TestPGOpenSkipsSchemaWhenVersionCurrent(t *testing.T) {
	dsn := os.Getenv("LEDGER_TEST_PG_DSN")
	first := newPGStore(t) // 首次 Open：跑建库并写标记
	if got := readSchemaMarker(t, first.db); got != ledgerSchemaVersion {
		t.Fatalf("首次 Open 后标记=%d，期望 %d", got, ledgerSchemaVersion)
	}

	holder, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("开持锁会话: %v", err)
	}
	defer holder.Close()
	tx, err := holder.Begin()
	if err != nil {
		t.Fatalf("开持锁事务: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`LOCK TABLE cards IN ACCESS SHARE MODE`); err != nil {
		t.Fatalf("持 cards 锁: %v", err)
	}
	var holderPID int
	if err := tx.QueryRow(`SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatalf("读持锁会话 pid: %v", err)
	}

	start := time.Now()
	s, err := OpenShared(dsn)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("版本一致时 Open 不应被 cards 的 ACCESS SHARE 挡住: %v（耗时 %v）", err, elapsed)
	}
	defer s.Close()
	if elapsed >= time.Second {
		t.Fatalf("版本一致时 Open 耗时 %v，应 < 1s（疑似仍在排 cards 排他锁）", elapsed)
	}

	// pg_locks 旁证：cards 上除持锁会话外无人持有或等待 AccessExclusiveLock。
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM pg_locks
		WHERE relation = 'cards'::regclass AND mode = 'AccessExclusiveLock' AND pid <> $1`, holderPID).Scan(&n); err != nil {
		t.Fatalf("查 pg_locks: %v", err)
	}
	if n != 0 {
		t.Fatalf("Open 后 cards 上仍有 %d 个 AccessExclusiveLock 持有/等待者", n)
	}
	t.Logf("版本一致时持锁下 Open 耗时 %v", elapsed)
}

// S2b：ledger_meta 都不存在的库（新 schema）Open 视为「标记缺失」——跑完整建库并写标记，
// 而不是把 42P01 当成 Open 失败。
func TestPGOpenFreshSchemaWritesMarker(t *testing.T) {
	dsn := os.Getenv("LEDGER_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未设 LEDGER_TEST_PG_DSN，跳过 schema 版本闸 PG 测试")
	}
	schema := fmt.Sprintf("schema_ver_fresh_%d", time.Now().UnixNano())
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("连接 PG: %v", err)
	}
	if _, err := admin.Exec(fmt.Sprintf(`CREATE SCHEMA %s`, schema)); err != nil {
		admin.Close()
		t.Fatalf("建隔离 schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(fmt.Sprintf(`DROP SCHEMA %s CASCADE`, schema)); err != nil {
			t.Errorf("清理隔离 schema %s: %v", schema, err)
		}
		admin.Close()
	})
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	scoped := dsn + sep + "search_path=" + schema

	s, err := OpenShared(scoped)
	if err != nil {
		t.Fatalf("空 schema 首次 Open: %v", err)
	}
	defer s.Close()
	var cur string
	if err := s.db.QueryRow(`SELECT current_schema()`).Scan(&cur); err != nil || cur != schema {
		t.Fatalf("search_path 未生效: current_schema=%q err=%v", cur, err)
	}
	if got := readSchemaMarker(t, s.db); got != ledgerSchemaVersion {
		t.Fatalf("空 schema 首次 Open 后标记=%d，期望 %d", got, ledgerSchemaVersion)
	}
}
