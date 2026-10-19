// B435.5（X）B434 两表退役迁移的 PG 孪生（plan b435.5 §4 段 0、§7 X-PG）。默认 skip；
// 设 LEDGER_TEST_PG_DSN 后启用。每支在本测试新建的隔离 schema 里跑（search_path
// 指向它，用后 DROP CASCADE）——绝不在共享 schema 上 DROP 两表（会删掉开发库真表）。
package ledger

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// newB435RetireEnvPG 建隔离 schema 并返回指向它的环境；DSN 未设则 skip。
func newB435RetireEnvPG(t *testing.T) *b435RetireEnv {
	t.Helper()
	dsn := os.Getenv("LEDGER_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未设 LEDGER_TEST_PG_DSN，跳过 B435 退役迁移 PG 孪生")
	}
	schema := fmt.Sprintf("b435_retire_%d", time.Now().UnixNano())
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
	e := &b435RetireEnv{t: t, dsn: dsn + sep + "search_path=" + schema, pg: true}
	var cur string
	if err := e.raw().QueryRow(`SELECT current_schema()`).Scan(&cur); err != nil || cur != schema {
		t.Fatalf("search_path 未生效，拒绝继续（防误删共享 schema 的表）: current_schema=%q err=%v", cur, err)
	}
	return e
}

func TestB435RetireMigrationClearsAndRecordsPG(t *testing.T) {
	e := newB435RetireEnvPG(t)
	fx := newB435RetireFixture(e)
	if err := e.upgrade(); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	checkB435RetireCleared(t, e, fx)
}

func TestB435RetireMigrationIdempotentPG(t *testing.T) {
	e := newB435RetireEnvPG(t)
	fx := newB435RetireFixture(e)
	if err := e.upgrade(); err != nil {
		t.Fatalf("首次迁移: %v", err)
	}
	db := e.raw()
	first, ok := e.record(db)
	if !ok {
		t.Fatal("首次迁移后留痕键必须存在")
	}
	if err := e.upgrade(); err != nil {
		t.Fatalf("二次迁移: %v", err)
	}
	if second, _ := e.record(db); first != second {
		t.Fatalf("二次迁移留痕必须字节相等:\n first=%s\nsecond=%s", first, second)
	}
	checkB435RetireCleared(t, e, fx)
}

func TestB435RetireMigrationRecreatedEmptyTablesPG(t *testing.T) {
	e := newB435RetireEnvPG(t)
	fx := newB435RetireFixture(e)
	if err := e.upgrade(); err != nil {
		t.Fatalf("首次迁移: %v", err)
	}
	db := e.raw()
	first, _ := e.record(db)
	e.createLegacyTables(db)
	if err := e.upgrade(); err != nil {
		t.Fatalf("空表再迁移: %v", err)
	}
	if e.tableExists(db, "seat_desktops") || e.tableExists(db, "desktop_launchers") {
		t.Fatal("重建的空表必须再被删除")
	}
	if v, _ := e.record(db); v != first {
		t.Fatalf("空表再迁移留痕必须不变:\n before=%s\n after=%s", first, v)
	}
	checkB435RetireCleared(t, e, fx)
}

// H2 的 PG 半边（X-PG）：两连接并发升级同一隔离 schema → 都不报错、恰一份留痕。
func TestB435RetireMigrationConcurrentOpenPG(t *testing.T) {
	e := newB435RetireEnvPG(t)
	fx := newB435RetireFixture(e)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = e.upgrade()
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("并发升级 #%d 报错: %v", i, err)
		}
	}
	checkB435RetireCleared(t, e, fx)
}
