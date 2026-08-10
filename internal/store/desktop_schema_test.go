// store 包桌面控制面迁移测试：验证 desktop 域表/索引创建与迁移幂等。
//
// 职责：
//   - 打开全新库后断言控制面所需的全部表与唯一索引存在
//   - 重复 Open 两次验证迁移幂等（不重复建表、不报错）
//
// 边界：
//   - 不覆盖各表的读写行为（由 machines/workspaces/machine_events 等专项测试负责）
//   - 不校验迁移失败的具体错误文本（Open 返回错误即视为迁移失败）
//
// 为什么用 package store（内部测试）而非 store_test：需要直接访问 Store 的
// 私有 db 字段做 sqlite_master/pragma 元数据断言，外部测试包拿不到。
package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// desktopTables 是桌面控制面迁移后必须存在的表清单。
var desktopTables = []string{
	"control_metadata",
	"machines",
	"projects",
	"project_locations",
	"workspaces",
	"git_refs",
	"task_summaries",
	"operations",
	"machine_events",
	"machine_cursors",
	"control_events",
	"pty_sessions",
}

// TestDesktopSchemaTablesExist 打开全新库，断言控制面全部表存在。
func TestDesktopSchemaTablesExist(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	for _, table := range desktopTables {
		if !tableExists(s.db, table) {
			t.Errorf("表 %s 不存在", table)
		}
	}
}

// TestDesktopSchemaMigrationIdempotent 重复 Open 多次验证迁移幂等。
//
// 为什么必须幂等：agentd 重启必然重新 Open 同一数据库文件，迁移若报错或重复
// 建表，控制面就无法在旧库上启动。重复 Open 均须成功且不产生重复表。
func TestDesktopSchemaMigrationIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "handoff.db")
	for i := 0; i < 3; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatalf("第 %d 次 Open 失败: %v", i+1, err)
		}
		for _, table := range desktopTables {
			if !tableExists(s.db, table) {
				t.Errorf("第 %d 次 Open 后表 %s 不存在", i+1, table)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatalf("第 %d 次 Close 失败: %v", i+1, err)
		}
	}
}

// TestDesktopSchemaUniqueIndexes 断言控制面关键唯一约束。
//
// 这些约束是「稳定 ID + 幂等」的数据库层兜底：重复 upsert 或重复 machine event
// 都要能被 UNIQUE 索引拦截或幂等吸收，不能出现重复行。
func TestDesktopSchemaUniqueIndexes(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	assertUniqueIndex(t, s.db, "machines", "idx_machines_kind_local", "kind")
	assertUniqueIndex(t, s.db, "project_locations", "idx_project_locations_project_role", "project_id,role")
	assertUniqueIndex(t, s.db, "workspaces", "idx_workspaces_machine_canonical", "machine_id,canonical_path")
	assertUniqueIndex(t, s.db, "machine_events", "idx_machine_events_machine_seq", "machine_id,machine_seq")
	assertUniqueIndex(t, s.db, "control_events", "idx_control_events_revision", "control_revision")
	assertUniqueIndex(t, s.db, "pty_sessions", "idx_pty_sessions_command", "command_id")
}

// tableExists 检查 SQLite 元数据表 sqlite_master 中是否存在指定表。
func tableExists(db *sql.DB, table string) bool {
	var n int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// assertUniqueIndex 断言指定表存在一个覆盖给定列的唯一索引。
func assertUniqueIndex(t *testing.T, db *sql.DB, table, indexName, cols string) {
	t.Helper()
	var sqlText string
	err := db.QueryRow(
		"SELECT sql FROM sqlite_master WHERE type='index' AND tbl_name=? AND name=?",
		table, indexName).Scan(&sqlText)
	if err != nil {
		t.Fatalf("索引 %s 不存在（table %s）: %v", indexName, table, err)
	}
	if !strings.Contains(strings.ToUpper(sqlText), "UNIQUE") {
		t.Errorf("索引 %s 不是 UNIQUE: %s", indexName, sqlText)
	}
	for _, col := range strings.Split(cols, ",") {
		col = strings.TrimSpace(col)
		var ok int
		err := db.QueryRow(
			"SELECT COUNT(*) FROM pragma_index_info(?) WHERE name=?", indexName, col).Scan(&ok)
		if err != nil {
			t.Fatalf("读取索引 %s 列失败: %v", indexName, err)
		}
		if ok == 0 {
			t.Errorf("索引 %s 缺少列 %s", indexName, col)
		}
	}
}
