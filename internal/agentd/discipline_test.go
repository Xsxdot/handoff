// discipline_test.go —— 纪律配置端点的测试。
//
// B229 后的形态：本地纪律目录退役，GET /api/discipline 的内置与文件清单恒为
// 空数组（类型保留防 TS 断裂）；GET/PUT /api/discipline/file 拒服务并指路
// handoff discipline put（P4 裁决 a）；机器级映射 PUT 语义不动（③层 Out of Scope）。
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
		nil, nil, newTestGate(t), discardLogger())
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

// TestDisciplineGetReturnsEmptyCatalog 目录退役后：GET 恒 200 且 Builtins/Files
// 都是空数组（wire 层是 [] 不是 null，TS 类型不破）；磁盘上残留文件也不再列出。
func TestDisciplineGetReturnsEmptyCatalog(t *testing.T) {
	env, dir := newDisciplineEnv(t,
		map[string]string{"codex": "codex-strict.md", "ghost": ""}, "opencode", "codex")
	// 故意留一份残留文件：它属于退役目录，任何清单都不再出现
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "codex-strict.md"), []byte("自定义\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	raw, code := env.getRaw(t, "/api/discipline")
	if code != http.StatusOK {
		t.Fatalf("code = %d, want 200: %s", code, raw)
	}
	body := string(raw)
	if !strings.Contains(body, `"builtins":[]`) || !strings.Contains(body, `"files":[]`) {
		t.Fatalf("Builtins/Files 必须是空数组而不是 null 或残留数据：%s", body)
	}
	var got proto.DisciplineResp
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("解码: %v", err)
	}
	want := map[string]bool{"codex": true, "ghost": true, "opencode": true}
	if len(got.Bindings) != len(want) {
		t.Fatalf("Bindings = %+v, want %v 三条", got.Bindings, want)
	}
	for _, b := range got.Bindings {
		if !want[b.Executor] {
			t.Errorf("意外 binding %+v", b)
		}
		if b.DefaultTier != "" {
			t.Errorf("内置档位已删，DefaultTier 应为空串：%+v", b)
		}
	}
}

// TestDisciplineFileEndpointsRetired（P4 裁决 a）：file 端点拒服务，错误可行动
// （点名 handoff discipline put）；PUT 之后磁盘目录不出现新文件——
// 「编辑成功但永不生效」的静默失败通道被显式关死。
func TestDisciplineFileEndpointsRetired(t *testing.T) {
	env, dir := newDisciplineEnv(t, nil, "codex")

	var readBody map[string]string
	if code := env.getJSON(t, "/api/discipline/file?name=mine.md", &readBody); code != http.StatusGone {
		t.Errorf("GET code = %d, want 410", code)
	}
	if !strings.Contains(readBody["error"], "handoff discipline put") {
		t.Errorf("GET 错误应可行动（含 handoff discipline put）：%q", readBody["error"])
	}

	var writeBody map[string]string
	code := env.putJSON(t, "/api/discipline/file?name=mine.md",
		proto.FileWriteReq{Content: "纪律正文\n"}, &writeBody)
	if code != http.StatusGone {
		t.Errorf("PUT code = %d, want 410", code)
	}
	if !strings.Contains(writeBody["error"], "handoff discipline put") {
		t.Errorf("PUT 错误应可行动（含 handoff discipline put）：%q", writeBody["error"])
	}
	if entries, rerr := os.ReadDir(dir); rerr == nil && len(entries) > 0 {
		t.Fatalf("拒服务的 PUT 不得在磁盘留下文件：%v", entries)
	}
}

func TestDisciplineMappingSavesThreeModes(t *testing.T) {
	env, dir := newDisciplineEnv(t, nil, "codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mine.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	var got proto.DisciplineResp
	code := env.putJSON(t, "/api/discipline/mapping", proto.DisciplineMappingReq{
		Bindings: []proto.DisciplineBinding{
			{Executor: "codex", Mode: "file", File: "mine.md"},
			{Executor: "grok", Mode: "off"},
			{Executor: "opencode", Mode: "default"},
		},
	}, &got)
	if code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	m := env.srv.DisciplineMapping()
	if m["codex"] != "mine.md" {
		t.Errorf("file 档 = %q", m["codex"])
	}
	if v, ok := m["grok"]; !ok || v != "" {
		t.Errorf("off 档应是空串且键存在，得到 %q/%v", v, ok)
	}
	if _, ok := m["opencode"]; ok {
		t.Errorf("default 档必须是**键不存在**，现在键还在")
	}
}

func TestDisciplineMappingRejectsMissingFile(t *testing.T) {
	env, _ := newDisciplineEnv(t, nil, "codex")
	var body map[string]string
	code := env.putJSON(t, "/api/discipline/mapping", proto.DisciplineMappingReq{
		Bindings: []proto.DisciplineBinding{{Executor: "codex", Mode: "file", File: "nope.md"}},
	}, &body)
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400（语义不动：配一个不存在的文件仍然拒绝保存）", code)
	}
	if _, ok := env.srv.DisciplineMapping()["codex"]; ok {
		t.Error("校验失败时不得落盘任何改动")
	}
}

func TestDisciplineMappingRejectsBadMode(t *testing.T) {
	env, _ := newDisciplineEnv(t, nil, "codex")
	var body map[string]string
	if code := env.putJSON(t, "/api/discipline/mapping", proto.DisciplineMappingReq{
		Bindings: []proto.DisciplineBinding{{Executor: "codex", Mode: "sometimes"}},
	}, &body); code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", code)
	}
}
