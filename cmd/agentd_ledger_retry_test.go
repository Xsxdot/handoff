// 账本挂载的退避重试（2026-10-19 mac-mini 事故）：瞬时错误（53300 连接数超限）
// 不得让 agentd 放弃账本；配置性错误照旧 fail 快；停机时放弃重试。
package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/agentd"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/jackc/pgx/v5/pgconn"
)

func newRetryFixture(t *testing.T) (*config.Config, *agentd.Server, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{Listen: "127.0.0.1:0", Token: testToken, DataDir: dir, StallTimeout: 2 * time.Hour}
	taskStore, err := store.Open(filepath.Join(dir, "tasks.db"))
	if err != nil {
		t.Fatalf("开任务库: %v", err)
	}
	t.Cleanup(func() { taskStore.Close() })
	return cfg, agentd.NewServer(cfg, taskStore, discardLogger()), taskStore
}

func tooManyClients() error {
	return fmt.Errorf("连接账本库: %w", &pgconn.PgError{Code: "53300", Message: "sorry, too many clients already"})
}

func TestSetupLedgerRetriesTransientOpenError(t *testing.T) {
	cfg, srv, taskStore := newRetryFixture(t)
	attempts := 0
	var phases []string
	var slept []time.Duration
	boot := ledgerBoot{
		open: func(dsn string, shared bool) (*ledger.Store, error) {
			attempts++
			if attempts <= 2 {
				return nil, tooManyClients()
			}
			return ledger.Open(dsn)
		},
		report: func(phase string) { phases = append(phases, phase) },
		sleep:  func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stop, err := setupLedger(cfg, srv, taskStore, ctx, discardLogger(), boot)
	if err != nil {
		t.Fatalf("瞬时错误应重试到成功，得 %v", err)
	}
	t.Cleanup(stop)
	if attempts != 3 {
		t.Fatalf("attempts=%d，期望 3（两次 53300 后成功）", attempts)
	}
	if !srv.LedgerMounted() {
		t.Fatal("重试成功后账本必须挂载")
	}
	if len(slept) != 2 || slept[1] <= slept[0]/2 {
		t.Fatalf("应退避两次且第二次不短于首次的抖动下界，得 %v", slept)
	}
	joined := strings.Join(phases, "\n")
	if !strings.Contains(joined, "账本未挂载") || !strings.Contains(joined, "53300") {
		t.Fatalf("重试期间启动闸应播报「账本未挂载」与原因，得 %q", joined)
	}
}

func TestSetupLedgerFailsFastOnConfigError(t *testing.T) {
	cfg, srv, taskStore := newRetryFixture(t)
	attempts := 0
	boot := ledgerBoot{
		open: func(string, bool) (*ledger.Store, error) {
			attempts++
			return nil, fmt.Errorf("连接账本库: %w", &pgconn.PgError{Code: "28P01", Message: "password authentication failed"})
		},
		sleep: func(context.Context, time.Duration) error { return nil },
	}
	_, err := setupLedger(cfg, srv, taskStore, context.Background(), discardLogger(), boot)
	if err == nil || attempts != 1 {
		t.Fatalf("鉴权失败应一次即 fail 快：err=%v attempts=%d", err, attempts)
	}
}

func TestSetupLedgerGivesUpRetryOnShutdown(t *testing.T) {
	cfg, srv, taskStore := newRetryFixture(t)
	bootCtx, cancel := context.WithCancel(context.Background())
	attempts := 0
	boot := ledgerBoot{
		ctx: bootCtx,
		open: func(string, bool) (*ledger.Store, error) {
			attempts++
			cancel() // 第一次失败后停机信号到达
			return nil, tooManyClients()
		},
	}
	done := make(chan error, 1)
	go func() {
		_, err := setupLedger(cfg, srv, taskStore, context.Background(), discardLogger(), boot)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("停机时应放弃重试并返回 context.Canceled，得 %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("停机后重试循环未退出")
	}
	if attempts != 1 {
		t.Fatalf("attempts=%d，停机后不应再试", attempts)
	}
}

// ---- B462 S4：自登记 / 本地 targets 导入的瞬时错误退避重试 ----
//
// 2026-10-19 linux-01 事故形状：账本已打开，自登记撞 53300 即放弃挂载、agentd 照常 Ready，
// 卡账本直到人工重启都不可用。shared=true 配 SQLite 句柄（boot.open 注入），让自登记分支可达。

// newSharedRetryFixture 返回一份「有 DSN 且身份齐」的配置；boot.open 注入时用 sqlitePath 打开真实账本。
func newSharedRetryFixture(t *testing.T) (*config.Config, *agentd.Server, *store.Store, string) {
	t.Helper()
	cfg, srv, taskStore := newRetryFixture(t)
	cfg.Ledger.DSN = "postgres://b462-fixture.invalid/ledger"
	cfg.Name = "b462-self"
	cfg.Advertise = "127.0.0.1:1"
	return cfg, srv, taskStore, filepath.Join(cfg.DataDir, "ledger.db")
}

// tooManyClientsDeep 是自登记真实返回的多层 %w 包裹形态（RegisterDevice → mutate → 事务）。
func tooManyClientsDeep() error {
	return fmt.Errorf("自登记机器 b462-self: %w",
		fmt.Errorf("开账本事务: %w", &pgconn.PgError{Code: "53300", Message: "sorry, too many clients already"}))
}

func TestSetupLedgerRetriesTransientSelfRegister(t *testing.T) {
	cfg, srv, taskStore, dbPath := newSharedRetryFixture(t)
	opens, registers := 0, 0
	var phases []string
	var slept []time.Duration
	boot := ledgerBoot{
		open: func(string, bool) (*ledger.Store, error) { opens++; return ledger.Open(dbPath) },
		register: func(st *ledger.Store, c *config.Config, l *slog.Logger) error {
			registers++
			if registers <= 2 {
				return tooManyClientsDeep()
			}
			return agentd.SelfRegister(st, c, l)
		},
		report: func(phase string) { phases = append(phases, phase) },
		sleep:  func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stop, err := setupLedger(cfg, srv, taskStore, ctx, discardLogger(), boot)
	if err != nil {
		t.Fatalf("自登记瞬时错误应重试到成功，得 %v", err)
	}
	t.Cleanup(stop)
	if !srv.LedgerMounted() {
		t.Fatal("自登记重试成功后账本必须挂载")
	}
	if registers != 3 || opens != 1 {
		t.Fatalf("registers=%d opens=%d，期望 3 次自登记、1 次打开（句柄保留不重开）", registers, opens)
	}
	if len(slept) != 2 {
		t.Fatalf("应退避 2 次，得 %v", slept)
	}
	joined := strings.Join(phases, "\n")
	if !strings.Contains(joined, "账本未挂载") || !strings.Contains(joined, "自登记") || !strings.Contains(joined, "53300") {
		t.Fatalf("重试期间启动闸应播报「账本未挂载：自登记…53300」，得 %q", joined)
	}
}

func TestSetupLedgerRetriesTransientImportTargets(t *testing.T) {
	cfg, srv, taskStore, dbPath := newSharedRetryFixture(t)
	registers, imports := 0, 0
	var phases []string
	boot := ledgerBoot{
		open:     func(string, bool) (*ledger.Store, error) { return ledger.Open(dbPath) },
		register: func(*ledger.Store, *config.Config, *slog.Logger) error { registers++; return nil },
		importTargets: func(*ledger.Store, *config.Config, *slog.Logger) error {
			imports++
			if imports == 1 {
				return tooManyClientsDeep()
			}
			return nil
		},
		report: func(phase string) { phases = append(phases, phase) },
		sleep:  func(context.Context, time.Duration) error { return nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stop, err := setupLedger(cfg, srv, taskStore, ctx, discardLogger(), boot)
	if err != nil {
		t.Fatalf("导入瞬时错误应重试到成功，得 %v", err)
	}
	t.Cleanup(stop)
	if !srv.LedgerMounted() || registers != 1 || imports != 2 {
		t.Fatalf("mounted=%v registers=%d imports=%d，期望挂载、自登记 1 次、导入 2 次",
			srv.LedgerMounted(), registers, imports)
	}
	if !strings.Contains(strings.Join(phases, "\n"), "导入本地 targets") {
		t.Fatalf("导入重试应播报「导入本地 targets」，得 %q", phases)
	}
}

func TestSetupLedgerSelfRegisterRetryGivesUpOnShutdownAndClosesHandle(t *testing.T) {
	cfg, srv, taskStore, dbPath := newSharedRetryFixture(t)
	bootCtx, cancel := context.WithCancel(context.Background())
	var held *ledger.Store
	registers := 0
	boot := ledgerBoot{
		ctx:  bootCtx,
		open: func(string, bool) (*ledger.Store, error) { return ledger.Open(dbPath) },
		register: func(st *ledger.Store, _ *config.Config, _ *slog.Logger) error {
			registers++
			held = st
			cancel() // 第一次瞬时失败后停机信号到达
			return tooManyClientsDeep()
		},
	}
	done := make(chan error, 1)
	go func() {
		_, err := setupLedger(cfg, srv, taskStore, context.Background(), discardLogger(), boot)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("停机时应放弃自登记重试并返回 context.Canceled，得 %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("停机后自登记重试循环未退出")
	}
	if registers != 1 || srv.LedgerMounted() {
		t.Fatalf("registers=%d mounted=%v，停机后不应再试、不应挂载", registers, srv.LedgerMounted())
	}
	if _, err := held.ListDisciplineNames(); err == nil {
		t.Fatal("停机放弃重试时必须关闭已打开的账本句柄（关闭后读应报错）")
	}
}

func TestSetupLedgerSelfRegisterConflictStaysFailClosed(t *testing.T) {
	cfg, srv, taskStore, dbPath := newSharedRetryFixture(t)
	registers := 0
	slept := 0
	boot := ledgerBoot{
		open: func(string, bool) (*ledger.Store, error) { return ledger.Open(dbPath) },
		register: func(*ledger.Store, *config.Config, *slog.Logger) error {
			registers++
			return fmt.Errorf("自登记机器 b462-self: %w", ledger.ErrDeviceConflict)
		},
		sleep: func(context.Context, time.Duration) error { slept++; return nil },
	}
	stop, err := setupLedger(cfg, srv, taskStore, context.Background(), discardLogger(), boot)
	if err != nil || stop == nil {
		t.Fatalf("撞名应 fail-closed 返回 (stop, nil)，得 err=%v", err)
	}
	stop()
	if registers != 1 || slept != 0 || srv.LedgerMounted() {
		t.Fatalf("registers=%d slept=%d mounted=%v，撞名不得重试、不得挂载", registers, slept, srv.LedgerMounted())
	}
}
