// W3a 各任务白盒测试（package agentd）共享的测试环境辅助。
//
// 为什么另备一份而不复用 server_test.go 的 newTestEnv：那是 agentd_test 黑盒
// 包的辅助，白盒测试无法引用它。本文件按同款风格各备一份，签名与语义对齐
// （token 常量、真实 SQLite + httptest、t.Cleanup 收尾）。
package agentd

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/store"
)

// testToken 是白盒测试的固定访问令牌（与 server_test.go 的同名常量同值不同包）。
const testToken = "test-token"

// testAgentdEnv 聚合白盒测试依赖：真实 SQLite store + httptest server。
type testAgentdEnv struct {
	srv   *Server
	ts    *httptest.Server
	st    *store.Store
	mgr   *Manager // 由用例注入（SetManager 装配后填充）
	token string
}

// newTestAgentdEnv 构造完整测试环境（token=testToken，日志丢弃）。
func newTestAgentdEnv(t *testing.T) *testAgentdEnv {
	t.Helper()
	return newTestAgentdEnvWithCfg(t, &config.Config{Token: testToken},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// newTestAgentdEnvWithCfg 同 newTestAgentdEnv，但注入自定义配置与日志器。
func newTestAgentdEnvWithCfg(t *testing.T, cfg *config.Config, logger *slog.Logger) *testAgentdEnv {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	// 先落一份真实配置再注入路径：handler 层写操作（如新增开发机）经 swapConf
	// 落盘时要求 cfgPath 非空，且该路径上已存在一份可覆盖的配置
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("准备配置失败: %v", err)
	}
	st, err := store.Open(filepath.Join(dir, "handoff.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	srv := NewServer(cfg, st, logger)
	srv.SetConfigPath(cfgPath)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &testAgentdEnv{srv: srv, ts: ts, st: st, token: cfg.Token}
}

// getJSON 发起带 token 的 GET，返回状态码并把响应体解码到 out（out 为 nil 时只取状态码）。
func (e *testAgentdEnv) getJSON(t *testing.T, path string, out any) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, e.ts.URL+path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("GET %s 解码: %v", path, err)
		}
	}
	return resp.StatusCode
}

// getJSONCode 同 getJSON：语义更贴近「先看状态码」的断言场景，行为完全一致。
func (e *testAgentdEnv) getJSONCode(t *testing.T, path string, out any) int {
	t.Helper()
	return e.getJSON(t, path, out)
}
