package agentd

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/envfile"
	"github.com/Xsxdot/handoff/internal/launcher"
	"github.com/Xsxdot/handoff/internal/proto"
)

func newLauncherTestEnv(t *testing.T, logger *slog.Logger) (*testAgentdEnv, string) {
	t.Helper()
	dataDir := t.TempDir()
	env := newTestAgentdEnvWithCfg(t, &config.Config{Token: testToken, DataDir: dataDir}, logger)
	return env, dataDir
}

func launcherPut(t *testing.T, env *testAgentdEnv, req proto.LaunchersReq) (int, []byte) {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("序列化启动项请求: %v", err)
	}
	return doJSON(t, env, http.MethodPut, "/api/launchers", string(b))
}

func launcherGet(t *testing.T, env *testAgentdEnv) proto.LaunchersResp {
	t.Helper()
	code, body := doJSON(t, env, http.MethodGet, "/api/launchers", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/launchers 状态码 = %d；体=%s", code, body)
	}
	var resp proto.LaunchersResp
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("解析启动项响应: %v；体=%s", err, body)
	}
	return resp
}

func TestLaunchersPutRejectsEmptyName(t *testing.T) {
	env, _ := newLauncherTestEnv(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	code, body := launcherPut(t, env, proto.LaunchersReq{Launchers: []proto.Launcher{{Name: " ", Command: "run"}}})
	if code != http.StatusBadRequest || !strings.Contains(string(body), "第 1 条") {
		t.Fatalf("空名字应返回点名条目的 400，得到 %d: %s", code, body)
	}
}

func TestLaunchersPutRejectsDuplicateName(t *testing.T) {
	env, _ := newLauncherTestEnv(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	code, body := launcherPut(t, env, proto.LaunchersReq{Launchers: []proto.Launcher{
		{Name: "重复", Command: "one"}, {Name: "重复", Command: "two"},
	}})
	if code != http.StatusBadRequest || !strings.Contains(string(body), "重复") {
		t.Fatalf("重复名字应返回点名名字的 400，得到 %d: %s", code, body)
	}
}

func TestLaunchersPutRejectsBothEmpty(t *testing.T) {
	env, _ := newLauncherTestEnv(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	code, body := launcherPut(t, env, proto.LaunchersReq{Launchers: []proto.Launcher{{Name: "空启动项"}}})
	if code != http.StatusBadRequest || !strings.Contains(string(body), "空启动项") {
		t.Fatalf("两项为空应返回点名名字的 400，得到 %d: %s", code, body)
	}
}

func TestLaunchersPutRejectsSeparatorInEnvFile(t *testing.T) {
	env, _ := newLauncherTestEnv(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	code, body := launcherPut(t, env, proto.LaunchersReq{Launchers: []proto.Launcher{{Name: "路径", EnvFile: "../x"}}})
	if code != http.StatusBadRequest || !strings.Contains(string(body), "不能含路径分隔符") {
		t.Fatalf("env 路径应被拒绝，得到 %d: %s", code, body)
	}
}

func TestLaunchersPutRejectsMissingEnvFile(t *testing.T) {
	env, _ := newLauncherTestEnv(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	code, body := launcherPut(t, env, proto.LaunchersReq{Launchers: []proto.Launcher{{Name: "缺文件", EnvFile: "gone.env"}}})
	if code != http.StatusBadRequest || !strings.Contains(string(body), "缺文件") || !strings.Contains(string(body), "gone.env") {
		t.Fatalf("缺失 env 文件应点名启动项与文件，得到 %d: %s", code, body)
	}
}

func TestLaunchersPutReturnsLatestList(t *testing.T) {
	env, _ := newLauncherTestEnv(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	code, body := launcherPut(t, env, proto.LaunchersReq{Launchers: []proto.Launcher{{Name: "  构建  ", Command: "  go test  "}}})
	if code != http.StatusOK {
		t.Fatalf("保存应成功，得到 %d: %s", code, body)
	}
	var got proto.LaunchersResp
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("解析保存响应: %v；体=%s", err, body)
	}
	if len(got.Launchers) != 1 || got.Launchers[0].Name != "构建" || got.Launchers[0].Command != "go test" {
		t.Fatalf("保存响应不是最新规整列表：%s", body)
	}
}

func TestLaunchersGetEnvMissingBothWays(t *testing.T) {
	env, dataDir := newLauncherTestEnv(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	envDir := envfile.Dir(dataDir)
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatalf("建 env 目录: %v", err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "real.env"), []byte("A=1\n"), 0o600); err != nil {
		t.Fatalf("写 env 文件: %v", err)
	}
	if err := os.WriteFile(launcher.Dir(dataDir), []byte(
		`[{"name":"在","env_file":"real.env"},{"name":"不在","env_file":"gone.env"}]`), 0o600); err != nil {
		t.Fatalf("写启动项配置: %v", err)
	}

	resp := launcherGet(t, env)
	byName := map[string]proto.Launcher{}
	for _, l := range resp.Launchers {
		byName[l.Name] = l
	}
	if byName["在"].EnvMissing {
		t.Error("指向存在的 env 文件时 env_missing 应为 false")
	}
	if !byName["不在"].EnvMissing {
		t.Error("指向不存在的 env 文件时 env_missing 应为 true")
	}
}

func TestLaunchersPutIgnoresClientEnvMissing(t *testing.T) {
	env, dataDir := newLauncherTestEnv(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	envDir := envfile.Dir(dataDir)
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatalf("建 env 目录: %v", err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "real.env"), []byte("A=1\n"), 0o600); err != nil {
		t.Fatalf("写 env 文件: %v", err)
	}
	code, body := launcherPut(t, env, proto.LaunchersReq{Launchers: []proto.Launcher{{Name: "忽略", EnvFile: "real.env", EnvMissing: true}}})
	if code != http.StatusOK {
		t.Fatalf("保存应成功，得到 %d: %s", code, body)
	}
	got := launcherGet(t, env)
	if len(got.Launchers) != 1 || got.Launchers[0].EnvMissing {
		t.Fatalf("GET 应按真实文件重算 env_missing=false：%+v", got.Launchers)
	}
}

func TestLaunchersGetOnFreshDataDir(t *testing.T) {
	env, _ := newLauncherTestEnv(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	got := launcherGet(t, env)
	if got.Launchers == nil || len(got.Launchers) != 0 {
		t.Fatalf("新数据目录应返回非 nil 空列表：%+v", got.Launchers)
	}
}

func TestLaunchersPutDoesNotLogCommand(t *testing.T) {
	var buf bytes.Buffer
	env, _ := newLauncherTestEnv(t, slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	code, body := launcherPut(t, env, proto.LaunchersReq{Launchers: []proto.Launcher{{Name: "部署", Command: "SECRET_TOKEN=abc deploy.sh"}}})
	if code != http.StatusOK {
		t.Fatalf("保存应成功，得到 %d: %s", code, body)
	}
	logged := buf.String()
	if strings.Contains(logged, "SECRET_TOKEN") || strings.Contains(logged, "deploy.sh") {
		t.Fatal("命令原文不得进日志")
	}
	if !strings.Contains(logged, "启动项已保存") || !strings.Contains(logged, "with_command=1") {
		t.Fatalf("保存日志应含条数与带命令数量，实得：%s", logged)
	}
}
