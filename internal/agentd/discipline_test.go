// discipline_test.go —— 纪律配置端点的测试（白盒包：要直接看 manager 的 resolver）。
package agentd

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

// newDisciplineEnv 构造带 DataDir、纪律映射与若干已注册 executor 的白盒环境，
// 返回环境与该机的纪律块目录路径（目录本身不预先创建——「还没建」是必测的一档）。
func newDisciplineEnv(t *testing.T, mapping map[string]string, execs ...string) (*testAgentdEnv, string) {
	t.Helper()
	dataDir := t.TempDir()
	env := newTestAgentdEnvWithCfg(t, &config.Config{
		Token: testToken, DataDir: dataDir, Discipline: mapping,
	}, discardLogger())
	ads := map[string]executor.Adapter{}
	for _, n := range execs {
		ads[n] = &failStartAdapter{} // 只需要名字进注册表，本组用例不启动任何 executor
	}
	mgr := NewManager(env.st, env.srv.Hub(), ads, env.srv.conf(),
		env.srv.DisciplineMapping, nil, newTestGate(t), discardLogger())
	env.srv.SetManager(mgr)
	env.mgr = mgr
	return env, filepath.Join(dataDir, "discipline")
}

// putJSON 发起带 token 的 PUT（JSON body），返回状态码并把响应体解码到 out。
func (e *testAgentdEnv) putJSON(t *testing.T, path string, body any, out any) int {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPut, e.ts.URL+path, strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("PUT %s 解码: %v", path, err)
		}
	}
	return resp.StatusCode
}

func TestDisciplineGetListsBuiltinsFilesAndBindings(t *testing.T) {
	// 配置里放一个当前没注册的 executor 名（ghost）：它必须仍然出现在 bindings 里，
	// 否则界面看不见它、而它还在配置里生效
	env, discDir := newDisciplineEnv(t,
		map[string]string{"codex": "codex-strict.md", "ghost": ""}, "opencode", "codex")
	if err := os.MkdirAll(discDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(discDir, "codex-strict.md"), []byte("自定义\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var got proto.DisciplineResp
	if code := env.getJSON(t, "/api/discipline", &got); code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	if got.Dir != discDir {
		t.Errorf("Dir = %q, want %q", got.Dir, discDir)
	}
	if len(got.Builtins) != 2 || got.Builtins[0].Tier != "subagent" {
		t.Errorf("Builtins = %+v", got.Builtins)
	}
	if len(got.Files) != 1 || got.Files[0].Name != "codex-strict.md" {
		t.Errorf("Files = %+v", got.Files)
	}
	want := map[string]proto.DisciplineBinding{
		"codex":    {Executor: "codex", Mode: "file", File: "codex-strict.md", DefaultTier: "single-context"},
		"ghost":    {Executor: "ghost", Mode: "off", DefaultTier: "single-context"},
		"opencode": {Executor: "opencode", Mode: "default", DefaultTier: "subagent"},
	}
	if len(got.Bindings) != 3 {
		t.Fatalf("Bindings = %+v，want 3 条（注册的 ∪ 配置里的）", got.Bindings)
	}
	for _, b := range got.Bindings {
		if want[b.Executor] != b {
			t.Errorf("binding %s = %+v, want %+v", b.Executor, b, want[b.Executor])
		}
	}
}

func TestDisciplineGetOnMissingDirReturnsEmptyFiles(t *testing.T) {
	env, _ := newDisciplineEnv(t, nil, "opencode")

	var got proto.DisciplineResp
	if code := env.getJSON(t, "/api/discipline", &got); code != http.StatusOK {
		t.Fatalf("code = %d，目录不存在应当是 200 空列表而不是错误", code)
	}
	if len(got.Files) != 0 {
		t.Fatalf("Files = %+v, want 空", got.Files)
	}
}
