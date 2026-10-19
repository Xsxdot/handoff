// PG 账本打开的有界性与连接上限（2026-10-19 mac-mini 重启事故）。
// 默认 skip，设 LEDGER_TEST_PG_DSN 后启用。
package ledger

import (
	"database/sql"
	"os"
	"sync"
	"testing"
	"time"
)

// 事故形状：ensureSchema 的 ALTER TABLE cards 要 AccessExclusiveLock，任一
// 会话持 cards 上的锁时 Open 无限等待、零日志。修复后 Open 必须在锁等待上限
// 内返回，且错误判为瞬时（交给 agentd 退避重试，而不是 fail 快或挂死）。
// schema 版本闸之后只有标记过期（升级当次）才会跑 ALTER，故先删标记模拟旧库
// （spec ledger-schema-version S2）；放锁后 Open 补跑并写回标记。
func TestPGOpenBoundedWhenCardsLocked(t *testing.T) {
	dsn := os.Getenv("LEDGER_TEST_PG_DSN")
	s0 := newPGStore(t) // 先建 schema，保证 cards 表存在
	if _, err := s0.db.Exec(`DELETE FROM ledger_meta WHERE key = $1`, schemaVersionKey); err != nil {
		t.Fatalf("删 schema 版本标记: %v", err)
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

	done := make(chan error, 1)
	go func() {
		s, err := OpenShared(dsn)
		if err == nil {
			s.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cards 被锁时 Open 不应成功（ALTER 需要排他锁）")
		}
		if !IsTransientOpenError(err) {
			t.Fatalf("锁等待超时应判为瞬时错误，得 %v", err)
		}
	case <-time.After(schemaLockTimeout + 20*time.Second):
		t.Fatalf("cards 被锁时 Open 超过 %v 仍未返回（无界等待）", schemaLockTimeout+20*time.Second)
	}
	if got := readSchemaMarker(t, s0.db); got != -1 {
		t.Fatalf("建库失败时不应写标记，得 %d", got)
	}

	if err := tx.Rollback(); err != nil {
		t.Fatalf("放 cards 锁: %v", err)
	}
	s, err := OpenShared(dsn)
	if err != nil {
		t.Fatalf("放锁后 Open: %v", err)
	}
	s.Close()
	if got := readSchemaMarker(t, s0.db); got != ledgerSchemaVersion {
		t.Fatalf("放锁后 Open 应写回标记 %d，得 %d", ledgerSchemaVersion, got)
	}
}

// 事故形状：database/sql 默认 MaxOpenConns=0（无上限），镜像持有者 517 个
// goroutine 并发读库即开 517 条连接，打穿共享库 max_connections=90。
func TestPGPoolCapped(t *testing.T) {
	s := newPGStore(t)
	if got := s.db.Stats().MaxOpenConnections; got != PGMaxOpenConns {
		t.Fatalf("MaxOpenConnections=%d，期望 %d", got, PGMaxOpenConns)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4*PGMaxOpenConns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.db.Exec(`SELECT pg_sleep(0.2)`); err != nil {
				t.Errorf("并发查询: %v", err)
			}
		}()
	}
	peak := 0
	stop := make(chan struct{})
	go func() { wg.Wait(); close(stop) }()
	for {
		select {
		case <-stop:
			if peak > PGMaxOpenConns {
				t.Fatalf("并发 %d 查询时连接峰值 %d 超过上限 %d", 4*PGMaxOpenConns, peak, PGMaxOpenConns)
			}
			return
		case <-time.After(10 * time.Millisecond):
			if n := s.db.Stats().OpenConnections; n > peak {
				peak = n
			}
		}
	}
}
