// env_test.go —— env 配置端点的测试（白盒包：要直接看 manager 的 resolver）。
package agentd

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/envfile"
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

func TestEnvKeysHidesValues(t *testing.T) {
	env, envDir := newEnvEnv(t, nil, "opencode")
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "# 注释\nexport TOKEN=zzz-secret-zzz\nGOPROXY=https://proxy.example\nGOPROXY=https://mirror.example\nEMPTY_ONE=\n"
	if err := os.WriteFile(filepath.Join(envDir, "a.env"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	raw, code := env.getRaw(t, "/api/env/file/keys?name=a.env")
	if code != 200 {
		t.Fatalf("code = %d, want 200: %s", code, raw)
	}
	// spec §6 的机器判据：响应体里不得出现任何值
	assertNoSecret(t, raw, "zzz-secret-zzz")
	assertNoSecret(t, raw, "proxy.example")

	var resp proto.EnvKeysResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("解码: %v", err)
	}
	if len(resp.Keys) != 3 {
		t.Fatalf("keys = %+v，想要 TOKEN/GOPROXY/EMPTY_ONE 三条（重复键只出现一次）", resp.Keys)
	}
	byKey := map[string]proto.EnvKey{}
	for _, k := range resp.Keys {
		byKey[k.Key] = k
	}
	if byKey["TOKEN"].ValueBytes != len("zzz-secret-zzz") {
		t.Fatalf("TOKEN.value_bytes = %d，想要 %d", byKey["TOKEN"].ValueBytes, len("zzz-secret-zzz"))
	}
	if !byKey["GOPROXY"].Duplicate {
		t.Fatalf("GOPROXY 应标记 duplicate（后者覆盖，位置留在首次出现处）")
	}
	if byKey["EMPTY_ONE"].ValueBytes != 0 {
		t.Fatalf("EMPTY_ONE.value_bytes = %d，想要 0", byKey["EMPTY_ONE"].ValueBytes)
	}
}

func TestEnvKeysDoesNotConsultProcessEnv(t *testing.T) {
	// lookup 传 nil：展开时不查 agentd 自己的环境，否则同一个文件在不同机器上
	// 会显示出不同的值长度，既误导又多泄露一层信息
	t.Setenv("B158_OUTER", "0123456789")
	env, envDir := newEnvEnv(t, nil, "opencode")
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "a.env"), []byte("X=$B158_OUTER\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var resp proto.EnvKeysResp
	if code := env.getJSON(t, "/api/env/file/keys?name=a.env", &resp); code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}
	if len(resp.Keys) != 1 || resp.Keys[0].ValueBytes != 0 {
		t.Fatalf("keys = %+v，想要 X 且 value_bytes=0（外部变量不查）", resp.Keys)
	}
}

func TestEnvKeysErrors(t *testing.T) {
	env, envDir := newEnvEnv(t, nil, "opencode")
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "bad.env"), []byte("1BAD=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var body map[string]string
	if code := env.getJSON(t, "/api/env/file/keys?name=../x", &body); code != 400 {
		t.Fatalf("穿越名 code = %d, want 400", code)
	}
	if code := env.getJSON(t, "/api/env/file/keys?name=gone.env", &body); code != 404 {
		t.Fatalf("不存在 code = %d, want 404", code)
	}
	if code := env.getJSON(t, "/api/env/file/keys?name=bad.env", &body); code != 400 {
		t.Fatalf("语法错 code = %d, want 400", code)
	}
	// Parse 的错误自带行号，必须原样透传——它是用户改对的唯一线索
	if !strings.Contains(body["error"], "第 1 行") && !strings.Contains(body["error"], "1BAD") {
		t.Fatalf("error = %q，想要带行号或原行", body["error"])
	}
}

// getRaw 发起带 token 的 GET，返回原始响应体与状态码；默认视图的凭据边界必须对原文检查。
func (e *testAgentdEnv) getRaw(t *testing.T, path string) ([]byte, int) {
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
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("GET %s 读取: %v", path, err)
	}
	return raw, resp.StatusCode
}

// assertNoSecret 断言响应原文没有出现被测 env 值。
func assertNoSecret(t *testing.T, raw []byte, secret string) {
	t.Helper()
	if strings.Contains(string(raw), secret) {
		t.Fatalf("响应体里出现了 env 的值 %q：%s", secret, raw)
	}
}

func TestEnvFileReadReturnsFullText(t *testing.T) {
	// 与 keys 端点相反：这条**就是**为编辑而存在的，全文含值必须原样交出
	env, envDir := newEnvEnv(t, nil, "opencode")
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "a.env"), []byte("TOKEN=zzz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var got proto.FileRead
	if code := env.getJSON(t, "/api/env/file?name=a.env", &got); code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}
	if got.Content != "TOKEN=zzz\n" || got.Size != 10 || got.SHA256 == "" {
		t.Fatalf("got = %+v", got)
	}
}

func TestEnvFileWriteRejectsBadSyntax(t *testing.T) {
	// 差异二：env 比纪律块多一道解析门。写坏的文件不该进磁盘——写进去了才发现，
	// 症状会拖到下一次派发（「代理配了但连不上」离根因十万八千里）
	env, envDir := newEnvEnv(t, nil, "opencode")
	var body map[string]string
	code := env.putJSON(t, "/api/env/file?name=a.env",
		proto.FileWriteReq{Content: "1BAD=x\n", BaseSHA256: ""}, &body)
	if code != 400 {
		t.Fatalf("code = %d, want 400", code)
	}
	if _, err := os.Stat(filepath.Join(envDir, "a.env")); !os.IsNotExist(err) {
		t.Fatalf("语法错的文件不该落盘")
	}
	if body["error"] == "" {
		t.Fatalf("必须透传 Parse 的错误原文")
	}
}

func TestEnvFileWriteCreateAndOverwrite(t *testing.T) {
	env, envDir := newEnvEnv(t, nil, "opencode")
	var created proto.FileWriteResp
	if code := env.putJSON(t, "/api/env/file?name=a.env",
		proto.FileWriteReq{Content: "A=1\n", BaseSHA256: ""}, &created); code != 200 {
		t.Fatalf("新建 code = %d, want 200", code)
	}
	if created.SHA256 == "" || created.Size != 4 {
		t.Fatalf("created = %+v", created)
	}
	// 撞名新建 409
	var conflict map[string]string
	if code := env.putJSON(t, "/api/env/file?name=a.env",
		proto.FileWriteReq{Content: "B=2\n", BaseSHA256: ""}, &conflict); code != 409 {
		t.Fatalf("撞名 code = %d, want 409", code)
	}
	// 陈旧 base 409 且带磁盘现状
	var stale proto.FileConflictResp
	if code := env.putJSON(t, "/api/env/file?name=a.env",
		proto.FileWriteReq{Content: "B=2\n", BaseSHA256: "deadbeef"}, &stale); code != 409 {
		t.Fatalf("陈旧 base code = %d, want 409", code)
	}
	if stale.Current.Content != "A=1\n" {
		t.Fatalf("409 体必须带磁盘现状，got = %+v", stale.Current)
	}
	// 正确 base 覆盖成功
	var ok proto.FileWriteResp
	if code := env.putJSON(t, "/api/env/file?name=a.env",
		proto.FileWriteReq{Content: "B=2\n", BaseSHA256: created.SHA256}, &ok); code != 200 {
		t.Fatalf("覆盖 code = %d, want 200", code)
	}
	data, err := os.ReadFile(filepath.Join(envDir, "a.env"))
	if err != nil || string(data) != "B=2\n" {
		t.Fatalf("落盘 = %q, err = %v", data, err)
	}
}

func TestEnvFileWriteAllowsDuplicateKeys(t *testing.T) {
	// 重复键不拦：Resolver 既有行为是 WARN + 后者覆盖。拦它等于在控制台里
	// 发明一条 agentd 不认的规则
	env, _ := newEnvEnv(t, nil, "opencode")
	var resp proto.FileWriteResp
	if code := env.putJSON(t, "/api/env/file?name=a.env",
		proto.FileWriteReq{Content: "A=1\nA=2\n", BaseSHA256: ""}, &resp); code != 200 {
		t.Fatalf("code = %d, want 200（重复键只标注不拦）", code)
	}
}

func TestEnvFileWriteRejectsBadNameAndTooLarge(t *testing.T) {
	env, _ := newEnvEnv(t, nil, "opencode")
	var body map[string]string
	if code := env.putJSON(t, "/api/env/file?name=../x",
		proto.FileWriteReq{Content: "A=1\n"}, &body); code != 400 {
		t.Fatalf("穿越名 code = %d, want 400", code)
	}
	big := strings.Repeat("A=1\n", envfile.MaxFileSize/4+1)
	if code := env.putJSON(t, "/api/env/file?name=big.env",
		proto.FileWriteReq{Content: big}, &body); code != 400 {
		t.Fatalf("超限 code = %d, want 400", code)
	}
}

func TestEnvMappingSaveTranslatesTwoModes(t *testing.T) {
	env, envDir := newEnvEnv(t, map[string]string{"opencode": "old.env"}, "opencode", "codex")
	env.srv.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "proxy.env"), []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var resp proto.EnvResp
	code := env.putJSON(t, "/api/env/mapping", proto.EnvMappingReq{Bindings: []proto.EnvBinding{
		{Executor: "codex", Mode: proto.EnvModeFile, File: "proxy.env"},
		{Executor: "opencode", Mode: proto.EnvModeOff},
	}}, &resp)
	if code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}
	// 差异一：off 落盘必须是**键不存在**，不是空串
	saved := env.srv.EnvMapping()
	if _, ok := saved["opencode"]; ok {
		t.Fatalf("off 档必须删键，实际 = %+v（空串是脏数据，会让 Resolver 读 <dir>/）", saved)
	}
	if saved["codex"] != "proxy.env" || len(saved) != 1 {
		t.Fatalf("saved = %+v，想要只有 codex", saved)
	}
	// 保存后直接回最新状态，界面拿它刷新
	got := map[string]string{}
	for _, b := range resp.Bindings {
		got[b.Executor] = b.Mode
	}
	if got["opencode"] != proto.EnvModeOff || got["codex"] != proto.EnvModeFile {
		t.Fatalf("响应 = %+v", resp.Bindings)
	}
}

func TestEnvMappingRejectsMissingFileAndBadMode(t *testing.T) {
	env, _ := newEnvEnv(t, nil, "opencode")
	env.srv.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	var body map[string]string

	// file 档指向不存在的文件：把错误挡在保存这一刻，好过三天后某次派发才炸
	if code := env.putJSON(t, "/api/env/mapping", proto.EnvMappingReq{Bindings: []proto.EnvBinding{
		{Executor: "opencode", Mode: proto.EnvModeFile, File: "gone.env"},
	}}, &body); code != 400 {
		t.Fatalf("缺文件 code = %d, want 400", code)
	}
	// file 档但文件名为空：绝不能落成空串
	if code := env.putJSON(t, "/api/env/mapping", proto.EnvMappingReq{Bindings: []proto.EnvBinding{
		{Executor: "opencode", Mode: proto.EnvModeFile, File: "  "},
	}}, &body); code != 400 {
		t.Fatalf("空文件名 code = %d, want 400", code)
	}
	// mode 非法
	if code := env.putJSON(t, "/api/env/mapping", proto.EnvMappingReq{Bindings: []proto.EnvBinding{
		{Executor: "opencode", Mode: "default"},
	}}, &body); code != 400 {
		t.Fatalf("非法 mode code = %d, want 400（env 没有 default 档）", code)
	}
	// executor 名为空
	if code := env.putJSON(t, "/api/env/mapping", proto.EnvMappingReq{Bindings: []proto.EnvBinding{
		{Executor: " ", Mode: proto.EnvModeOff},
	}}, &body); code != 400 {
		t.Fatalf("空 executor code = %d, want 400", code)
	}
}

func TestEnvMappingHotReloadsWithoutRebuildingManager(t *testing.T) {
	// 这条是整个 B158 的承重判据：改完映射**不重建 Manager**，
	// manager 侧的 resolver 必须立即反映新值
	env, envDir := newEnvEnv(t, nil, "opencode")
	env.srv.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "proxy.env"), []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	before, err := env.mgr.env.For("opencode")
	if err != nil || len(before) != 0 {
		t.Fatalf("before = %v, err = %v，想要未配置时不注入", before, err)
	}
	var resp proto.EnvResp
	if code := env.putJSON(t, "/api/env/mapping", proto.EnvMappingReq{Bindings: []proto.EnvBinding{
		{Executor: "opencode", Mode: proto.EnvModeFile, File: "proxy.env"},
	}}, &resp); code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}
	after, err := env.mgr.env.For("opencode")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if len(after) != 1 || after[0] != "A=1" {
		t.Fatalf("after = %v，想要 [A=1]（不重启 agentd 就该生效）", after)
	}
}
