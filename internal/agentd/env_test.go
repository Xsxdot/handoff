// env_test.go —— env 配置端点的测试（白盒包：要直接看 manager 的 resolver）。
package agentd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

// newEnvEnv 构造带 DataDir、env 映射与若干已注册 executor 的白盒环境，
// 返回环境与该机的 env 目录路径（目录本身不预先创建——「还没建」是必测的一档）。
func newEnvEnv(t *testing.T, mapping map[string]string, execs ...string) (*testAgentdEnv, string) {
	t.Helper()
	dataDir := t.TempDir()
	env := newTestAgentdEnvWithCfg(t, &config.Config{
		Token: testToken, DataDir: dataDir, Env: mapping,
	}, discardLogger())
	ads := map[string]executor.Adapter{}
	for _, n := range execs {
		ads[n] = &failStartAdapter{} // 只需要名字进注册表，本组用例不启动任何 executor
	}
	mgr := NewManager(env.st, env.srv.Hub(), ads, env.srv.conf(),
		env.srv.DisciplineMapping, env.srv.EnvMapping, nil, newTestGate(t), discardLogger())
	env.srv.SetManager(mgr)
	env.mgr = mgr
	return env, filepath.Join(dataDir, "env")
}

func TestEnvGetListsFilesAndBindings(t *testing.T) {
	// 配置里放一个当前没注册的 executor 名（ghost）：它必须仍然出现在 bindings 里，
	// 否则界面看不见它、而它还在配置里生效
	env, envDir := newEnvEnv(t,
		map[string]string{"codex": "proxy.env", "ghost": "ghost.env"}, "opencode", "codex")
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "proxy.env"), []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var resp proto.EnvResp
	if code := env.getJSON(t, "/api/env", &resp); code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}
	if resp.Dir != envDir {
		t.Fatalf("dir = %q, want %q", resp.Dir, envDir)
	}
	if len(resp.Files) != 1 || resp.Files[0].Name != "proxy.env" || resp.Files[0].Size != 4 {
		t.Fatalf("files = %+v", resp.Files)
	}
	got := map[string]proto.EnvBinding{}
	for _, b := range resp.Bindings {
		got[b.Executor] = b
	}
	if len(got) != 3 {
		t.Fatalf("bindings = %+v，想要 codex/ghost/opencode 三条（注册 ∪ 配置的并集）", resp.Bindings)
	}
	if got["codex"].Mode != proto.EnvModeFile || got["codex"].File != "proxy.env" {
		t.Fatalf("codex = %+v，想要 file/proxy.env", got["codex"])
	}
	if got["opencode"].Mode != proto.EnvModeOff || got["opencode"].File != "" {
		t.Fatalf("opencode = %+v，想要 off 且不带 file（配置里没这个键）", got["opencode"])
	}
	if got["ghost"].Mode != proto.EnvModeFile {
		t.Fatalf("ghost = %+v，想要 file（配置里有键，虽然 adapter 没注册）", got["ghost"])
	}
	// 排序稳定：界面每次刷新不该跳行
	names := []string{}
	for _, b := range resp.Bindings {
		names = append(names, b.Executor)
	}
	if strings.Join(names, ",") != "codex,ghost,opencode" {
		t.Fatalf("顺序 = %v，想要按名字升序", names)
	}
}

func TestEnvGetWhenDirMissing(t *testing.T) {
	// 目录还没建是常态，不是错误：必须 200 + 空列表
	env, envDir := newEnvEnv(t, nil, "opencode")
	var resp proto.EnvResp
	if code := env.getJSON(t, "/api/env", &resp); code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}
	if resp.Dir != envDir || len(resp.Files) != 0 {
		t.Fatalf("resp = %+v，想要 dir 有值、files 空", resp)
	}
	if len(resp.Bindings) != 1 || resp.Bindings[0].Mode != proto.EnvModeOff {
		t.Fatalf("bindings = %+v，想要 opencode/off", resp.Bindings)
	}
}

func TestEnvGetWithoutManagerIs503(t *testing.T) {
	// executor 名单来自 manager；manager 未就绪时不能装作「一个 executor 都没有」
	env := newTestAgentdEnvWithCfg(t, &config.Config{
		Token: testToken, DataDir: t.TempDir(),
	}, discardLogger())
	var body map[string]string
	if code := env.getJSON(t, "/api/env", &body); code != 503 {
		t.Fatalf("code = %d, want 503", code)
	}
	if !strings.Contains(body["error"], "manager") {
		t.Fatalf("error = %q，想要提到 manager", body["error"])
	}
}
