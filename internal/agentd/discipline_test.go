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
		env.srv.DisciplineMapping, nil, nil, newTestGate(t), discardLogger())
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
	if len(got.Builtins) != 6 || got.Builtins[0].Tier != "subagent" || got.Builtins[2].Tier != "review" ||
		got.Builtins[5].Tier != "finishing" {
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

func TestDisciplineFileReadWriteRoundTrip(t *testing.T) {
	env, dir := newDisciplineEnv(t, nil, "codex")

	var wrote proto.FileWriteResp
	code := env.putJSON(t, "/api/discipline/file?name=mine.md",
		proto.FileWriteReq{Content: "纪律正文\n"}, &wrote)
	if code != http.StatusOK {
		t.Fatalf("新建 code = %d", code)
	}
	var read proto.FileRead
	if code := env.getJSON(t, "/api/discipline/file?name=mine.md", &read); code != http.StatusOK {
		t.Fatalf("读 code = %d", code)
	}
	if read.Content != "纪律正文\n" || read.SHA256 != wrote.SHA256 {
		t.Fatalf("读回 = %q / %s，写入回的是 %s", read.Content, read.SHA256, wrote.SHA256)
	}
	if _, err := os.Stat(filepath.Join(dir, "mine.md")); err != nil {
		t.Fatalf("落盘: %v", err)
	}
}

func TestDisciplineFileNewOnExistingIs409(t *testing.T) {
	env, dir := newDisciplineEnv(t, nil, "codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mine.md"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	var body map[string]string
	code := env.putJSON(t, "/api/discipline/file?name=mine.md",
		proto.FileWriteReq{Content: "new"}, &body)
	if code != http.StatusConflict {
		t.Fatalf("code = %d, want 409", code)
	}
}

func TestDisciplineFileBaseMismatchIs409WithCurrent(t *testing.T) {
	env, dir := newDisciplineEnv(t, nil, "codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mine.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var conflict proto.FileConflictResp
	code := env.putJSON(t, "/api/discipline/file?name=mine.md",
		proto.FileWriteReq{Content: "new", BaseSHA256: "deadbeef"}, &conflict)
	if code != http.StatusConflict {
		t.Fatalf("code = %d, want 409", code)
	}
	if conflict.Current.Content != "hello\n" {
		t.Fatalf("409 必须带磁盘现状，得到 %q", conflict.Current.Content)
	}
}

func TestDisciplineFileRejectsBadNameAndOversize(t *testing.T) {
	env, _ := newDisciplineEnv(t, nil, "codex")
	var body map[string]string
	if code := env.putJSON(t, "/api/discipline/file?name=sub%2Fx.md",
		proto.FileWriteReq{Content: "x"}, &body); code != http.StatusBadRequest {
		t.Errorf("含分隔符 code = %d, want 400", code)
	}
	big := strings.Repeat("x", 64*1024+1)
	if code := env.putJSON(t, "/api/discipline/file?name=big.md",
		proto.FileWriteReq{Content: big}, &body); code != http.StatusBadRequest {
		t.Errorf("超限 code = %d, want 400", code)
	}
}

func TestDisciplineFileReadMissingIs404(t *testing.T) {
	env, _ := newDisciplineEnv(t, nil, "codex")
	var body map[string]string
	if code := env.getJSON(t, "/api/discipline/file?name=nope.md", &body); code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", code)
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
	_ = got
}

func TestDisciplineMappingRejectsMissingFile(t *testing.T) {
	env, _ := newDisciplineEnv(t, nil, "codex")
	var body map[string]string
	code := env.putJSON(t, "/api/discipline/mapping", proto.DisciplineMappingReq{
		Bindings: []proto.DisciplineBinding{{Executor: "codex", Mode: "file", File: "nope.md"}},
	}, &body)
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400（配一个不存在的文件等于埋一次必然失败的派发）", code)
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

func TestDisciplineMappingTakesEffectWithoutRestart(t *testing.T) {
	// 本条是 Task 2 + Task 3 + 本 task 合起来的唯一判据：
	// 改完映射**不重建 Manager**，下一次纪律解析就该看到新值。
	env, dir := newDisciplineEnv(t, nil, "codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mine.md"), []byte("自定义纪律\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 白盒：直接问 manager 自己的 resolver，绕过任何缓存层
	before, err := env.mgr.discipline.For("codex")
	if err != nil || before.Source != "内置:single-context" {
		t.Fatalf("改前 = %q err=%v", before.Source, err)
	}
	var got proto.DisciplineResp
	if code := env.putJSON(t, "/api/discipline/mapping", proto.DisciplineMappingReq{
		Bindings: []proto.DisciplineBinding{{Executor: "codex", Mode: "file", File: "mine.md"}},
	}, &got); code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	after, err := env.mgr.discipline.For("codex")
	if err != nil {
		t.Fatalf("改后 For: %v", err)
	}
	if after.Source != "配置:mine.md" || after.Text != "自定义纪律\n" {
		t.Fatalf("改后 = %q / %q，热更新失效（要重启才生效等于界面在骗人）", after.Source, after.Text)
	}
}
