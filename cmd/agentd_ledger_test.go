// B229.5 账本退休的 agentd 启动路径测试：enabled 开关删除后，
// 显式 disabled 的配置也必须走通「开库→SetLedger→挂镜像」恒开链路。
package cmd

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/agentd"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/store"
)

// discardLogger 静音被测路径的日志输出。
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestSetupLedgerMountsWithRetiredEnabledFlag 钉住 contract §2.6：
// enabled=false 的存量 config 下 agentd 启动账本路径必须可用——
// dsn 空回退 DataDir/ledger.db、health 端点报 enabled:true。
func TestSetupLedgerMountsWithRetiredEnabledFlag(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Listen: "127.0.0.1:0", Token: testToken, DataDir: dir,
		StallTimeout: 2 * time.Hour, Ledger: config.LedgerConfig{Enabled: false},
	}
	taskStore, err := store.Open(filepath.Join(dir, "tasks.db"))
	if err != nil {
		t.Fatalf("开任务库: %v", err)
	}
	t.Cleanup(func() { taskStore.Close() })
	srv := agentd.NewServer(cfg, taskStore, discardLogger())

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stop, err := setupLedger(cfg, srv, taskStore, ctx, discardLogger())
	if err != nil {
		t.Fatalf("enabled 已退休，启动账本路径应恒可用: %v", err)
	}
	if stop == nil {
		t.Fatal("stop 函数不得为 nil（调用方靠它保证 Stop 先于 Close）")
	}
	t.Cleanup(stop)

	if _, statErr := os.Stat(filepath.Join(dir, "ledger.db")); statErr != nil {
		t.Fatalf("dsn 空应回退 DataDir/ledger.db 并落盘: %v", statErr)
	}

	// B233.14：编制域具体服务必须由 cmd 组装点构造并注入。删掉
	// setupLedger 里的 srv.SetScheduling(sched) 会让这里落成 nil（StartAutomation
	// 也会静默 no-op），必须红。
	svc, ok := srv.Scheduling().(*scheduling.Service)
	if !ok || svc == nil {
		t.Fatalf("setupLedger 后必须注入具体 *scheduling.Service，got %#v", srv.Scheduling())
	}

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/ledger/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 health: %v", err)
	}
	defer resp.Body.Close()
	body := new(strings.Builder)
	if _, err := io.Copy(body, resp.Body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `"enabled":true`) {
		t.Fatalf("health 应报 enabled:true，得到 %d %s", resp.StatusCode, body.String())
	}
}
