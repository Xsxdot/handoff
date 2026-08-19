// executor_default_test.go —— 缺省执行者配置端点的测试（白盒包：要看 manager 的解析结果）。
package agentd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

// newExecDefaultEnv 构造带 executor 配置与若干已注册 adapter 的白盒环境。
func newExecDefaultEnv(t *testing.T, def, model string, execs ...string) *testAgentdEnv {
	t.Helper()
	env := newTestAgentdEnvWithCfg(t, &config.Config{
		Token: testToken, DataDir: t.TempDir(),
		Executor: config.ExecutorConfig{Default: def, Model: model},
	}, discardLogger())
	env.srv.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	ads := map[string]executor.Adapter{}
	for _, n := range execs {
		ads[n] = &failStartAdapter{}
	}
	mgr := NewManager(env.st, env.srv.Hub(), ads, env.srv.conf(),
		env.srv.DisciplineMapping, env.srv.EnvMapping, nil, newTestGate(t), discardLogger())
	env.srv.SetManager(mgr)
	env.mgr = mgr
	return env
}

func TestExecutorDefaultGet(t *testing.T) {
	env := newExecDefaultEnv(t, "opencode", "m-oc", "opencode", "codex", "fake")
	var resp proto.ExecutorDefaultResp
	if code := env.getJSON(t, "/api/executor/default", &resp); code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}
	if resp.Default != "opencode" || resp.Model != "m-oc" {
		t.Fatalf("resp = %+v", resp)
	}
	if strings.Join(resp.Available, ",") != "codex,fake,opencode" {
		t.Fatalf("available = %v，想要按名字升序", resp.Available)
	}
}

func TestExecutorDefaultGetWithoutManagerIs503(t *testing.T) {
	// 名单来自 manager；未就绪时不能装作「一个 executor 都没有」，
	// 那会让界面画出一个空下拉框，用户选无可选还以为是配置丢了
	env := newTestAgentdEnvWithCfg(t, &config.Config{
		Token: testToken, DataDir: t.TempDir(),
	}, discardLogger())
	var body map[string]string
	if code := env.getJSON(t, "/api/executor/default", &body); code != 503 {
		t.Fatalf("code = %d, want 503", code)
	}
}

func TestExecutorDefaultPutSaves(t *testing.T) {
	env := newExecDefaultEnv(t, "opencode", "m-oc", "opencode", "codex")
	var resp proto.ExecutorDefaultResp
	code := env.putJSON(t, "/api/executor/default",
		proto.ExecutorDefaultReq{Default: "codex", Model: " gpt-5.6-luna "}, &resp)
	if code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}
	// 前后空白必须 TrimSpace：粘贴模型名时带上空格是常事，
	// 而带空格的模型名会被 provider 当成另一个名字直接 400
	if resp.Default != "codex" || resp.Model != "gpt-5.6-luna" {
		t.Fatalf("resp = %+v，想要 codex / gpt-5.6-luna（已去空白）", resp)
	}
	saved := env.srv.conf().Executor
	if saved.Default != "codex" || saved.Model != "gpt-5.6-luna" {
		t.Fatalf("落盘 = %+v", saved)
	}
	// 承重：不重建 Manager，派发路径立即用新值
	if name, _, err := env.mgr.resolveExecutor(""); err != nil || name != "codex" {
		t.Fatalf("resolveExecutor = %q, err = %v，想要 codex", name, err)
	}
	if got := env.mgr.resolveModel("", "codex"); got != "gpt-5.6-luna" {
		t.Fatalf("resolveModel = %q，想要 gpt-5.6-luna", got)
	}
}

func TestExecutorDefaultPutClearsModel(t *testing.T) {
	// 空串是有意义的取值（不设默认模型），不是「不改」
	env := newExecDefaultEnv(t, "opencode", "m-oc", "opencode")
	var resp proto.ExecutorDefaultResp
	if code := env.putJSON(t, "/api/executor/default",
		proto.ExecutorDefaultReq{Default: "opencode", Model: ""}, &resp); code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}
	if resp.Model != "" {
		t.Fatalf("model = %q，想要空串", resp.Model)
	}
	if got := env.mgr.resolveModel("", "opencode"); got != "" {
		t.Fatalf("resolveModel = %q，想要空串（清空后由执行器自身默认接管）", got)
	}
}

func TestExecutorDefaultPutRejects(t *testing.T) {
	env := newExecDefaultEnv(t, "opencode", "", "opencode", "codex")
	var body map[string]string

	// 未注册的名字：错误里必须列出可选名单，否则用户只知道错了不知道该填什么
	if code := env.putJSON(t, "/api/executor/default",
		proto.ExecutorDefaultReq{Default: "opencde"}, &body); code != 400 {
		t.Fatalf("未注册 code = %d, want 400", code)
	}
	if !strings.Contains(body["error"], "codex") || !strings.Contains(body["error"], "opencode") {
		t.Fatalf("error = %q，想要列出可选名单", body["error"])
	}
	// 空串：缺省执行者不能没有——为空时每一次不带 --executor 的派发都会失败
	if code := env.putJSON(t, "/api/executor/default",
		proto.ExecutorDefaultReq{Default: "  "}, &body); code != 400 {
		t.Fatalf("空 default code = %d, want 400", code)
	}
	// 拒绝后配置必须没动
	if got := env.srv.conf().Executor.Default; got != "opencode" {
		t.Fatalf("被拒后配置被改了：%q", got)
	}
}

func TestExecutorDefaultPutDoesNotValidateModel(t *testing.T) {
	// agentd 不认识任何执行器的模型名单，没有可判据——校验它只能是瞎猜。
	// 这条用例存在的意义是**钉住「不校验」这个决定**，防止有人日后加一个白名单
	env := newExecDefaultEnv(t, "opencode", "", "opencode")
	var resp proto.ExecutorDefaultResp
	if code := env.putJSON(t, "/api/executor/default",
		proto.ExecutorDefaultReq{Default: "opencode", Model: "完全不存在的模型名"}, &resp); code != 200 {
		t.Fatalf("code = %d, want 200（model 不校验）", code)
	}
}
