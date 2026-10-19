// 账本挂载的退避重试（2026-10-19 mac-mini 事故）：瞬时错误（53300 连接数超限）
// 不得让 agentd 放弃账本；配置性错误照旧 fail 快；停机时放弃重试。
package cmd

import (
	"context"
	"errors"
	"fmt"
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
