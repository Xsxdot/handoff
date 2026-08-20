// W3a 各任务白盒测试（package agentd）共享的测试环境辅助。
//
// 为什么另备一份而不复用 server_test.go 的 newTestEnv：那是 agentd_test 黑盒
// 包的辅助，白盒测试无法引用它。本文件按同款风格各备一份，签名与语义对齐
// （token 常量、真实 SQLite + httptest、t.Cleanup 收尾）。
package agentd

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ptyhost"
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
	if runtime.GOOS != "windows" {
		ptyRoot, err := os.MkdirTemp(".", "at-pty-")
		if err != nil {
			t.Fatalf("准备 PTY 根目录失败: %v", err)
		}
		srv.ptyRootPath = ptyRoot
		srv.pty = ptyhost.New(ptyRoot, testHandoffExecutable(t), logger)
		t.Cleanup(func() {
			for _, sess := range srv.pty.List() {
				_ = srv.pty.Close(sess.ID)
			}
			_ = os.RemoveAll(ptyRoot)
		})
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &testAgentdEnv{srv: srv, ts: ts, st: st, token: cfg.Token}
}

var (
	testHandoffOnce sync.Once
	testHandoffPath string
	testHandoffErr  error
)

// testHandoffExecutable 构建真正包含 _ptyhost 子命令的 handoff 二进制。
//
// agentd.test 是 Go 测试运行器，不是 handoff 主程序；直接把 os.Executable 交给客户端
// 会让它把 `_ptyhost` 当成测试参数。二进制放在系统临时目录，避免测试把构建产物留在仓库。
func testHandoffExecutable(t *testing.T) string {
	t.Helper()
	testHandoffOnce.Do(func() {
		var dir string
		dir, testHandoffErr = os.MkdirTemp("", "handoff-test-bin-")
		if testHandoffErr != nil {
			return
		}
		testHandoffPath = filepath.Join(dir, "handoff")
		cmd := exec.Command("go", "build", "-o", testHandoffPath, "../..")
		cmd.Dir = "."
		var output []byte
		output, testHandoffErr = cmd.CombinedOutput()
		if testHandoffErr != nil {
			testHandoffErr = fmt.Errorf("go build handoff: %w: %s", testHandoffErr, output)
			return
		}
		if testHandoffErr = os.Chmod(testHandoffPath, 0o700); testHandoffErr != nil {
			return
		}
		// 预热：ptyhost 客户端只给 socket 的出现留 3s（internal/ptyhost 的 socketWait），
		// 而**首次** exec 一个刚编出来的二进制要付页缓存冷启动、macOS 还要付首次代码
		// 签名校验的代价。并发负载下这笔开销能把 3s 吃光，PTY 用例随之超时变红。
		// 空跑一次把它挪到计时窗口之外。失败忽略：预热不是判据，真有问题会在用例里暴露。
		_ = exec.Command(testHandoffPath, "--version").Run()
	})
	if testHandoffErr != nil {
		t.Fatalf("准备测试 handoff: %v", testHandoffErr)
	}
	return testHandoffPath
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
