package grok_test

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor/grok"
)

func TestWriteTaskEnvGeneratesPinnedPermissionConfig(t *testing.T) {
	dir := t.TempDir()
	home, err := grok.WriteTaskEnv(dir, "grok-4.5")
	if err != nil {
		t.Fatalf("WriteTaskEnv 出错: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("读 config.toml 失败: %v", err)
	}
	cfg := string(b)

	// permission_mode 必须钉死为 default：用户真实配置是 always-approve，
	// 不钉死等于审批门全废（spec §3.3）
	if !strings.Contains(cfg, `permission_mode = "default"`) {
		t.Errorf("config.toml 必须钉死 permission_mode=default，实际:\n%s", cfg)
	}
	if !strings.Contains(cfg, `default = "grok-4.5"`) {
		t.Errorf("config.toml 应写入任务级模型，实际:\n%s", cfg)
	}
	// 危险模式表逐条断言——少一条就是静默放行
	for _, rule := range []string{
		`"Write(*)"`, `"Edit(*)"`, `"Bash(rm *)"`, `"Bash(*sudo*)"`, `"Bash(*git push*)"`,
		`"Bash(*git reset --hard*)"`, `"Bash(*--force*)"`,
		`"Bash(curl *)"`, `"Bash(wget *)"`, `"WebFetch(*)"`,
	} {
		if !strings.Contains(cfg, rule) {
			t.Errorf("ask 规则缺 %s，实际:\n%s", rule, cfg)
		}
	}
	// Write/Edit 不得回到 allow 表：留在 allow 里等于写仓库外路径不经任何人（B27）
	for _, rule := range []string{`"Write(*)"`, `"Edit(*)"`} {
		if strings.Contains(allowSection(cfg), rule) {
			t.Errorf("allow 不得再放行 %s——那等于写仓库外路径不经任何人（B27）", rule)
		}
	}
	// auto_update 必须显式钉死为 false：grok 首次启动会用默认 true 把 config.toml
	// 整个重写补全，全新任务级 GROK_HOME 下默认 true 会让 serve 启动时联网查更新
	// 并无限期挂起（2026-08-09 真机实测，见 spec），必须写死不交给 grok 补
	if !strings.Contains(cfg, "auto_update = false") {
		t.Errorf("config.toml 必须钉死 auto_update=false，实际:\n%s", cfg)
	}
	if fi, err := os.Stat(filepath.Join(home, "config.toml")); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o600 {
		t.Errorf("config.toml 权限 = %v，期望 0600", fi.Mode().Perm())
	}
}

func TestWriteTaskEnvOmitsModelSectionWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	home, err := grok.WriteTaskEnv(dir, "")
	if err != nil {
		t.Fatalf("WriteTaskEnv 出错: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(home, "config.toml"))
	if strings.Contains(string(b), "[models]") {
		t.Errorf("model 为空时不应写 [models] 段（用 grok 自身默认），实际:\n%s", b)
	}
}

func TestEnsureAuthLinkIsIdempotentAndRepairs(t *testing.T) {
	home := t.TempDir()
	if err := grok.EnsureAuthLink(home); err != nil {
		t.Fatalf("首次建链出错: %v", err)
	}
	link := filepath.Join(home, "auth.json")
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("软链未建立: %v", err)
	}
	// 幂等：重复调用不报错
	if err := grok.EnsureAuthLink(home); err != nil {
		t.Fatalf("重复调用应幂等，出错: %v", err)
	}
	// 修复：软链被删掉后应重建（spec §3.3 实测 token 刷新会干掉它）
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := grok.EnsureAuthLink(home); err != nil {
		t.Fatalf("修复出错: %v", err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("软链未被重建: %v", err)
	}
}

// TestServeSpecKeepsSecretInEnvAndBindsLoopback 钉住 serveSpec 的安全与命令形态：
// secret 走 env 不进 argv（spec §3.1）、绑定回环端口、argv 是 grok agent serve 原样。
func TestServeSpecKeepsSecretInEnvAndBindsLoopback(t *testing.T) {
	spec := grok.ServeSpecForTest("/repo", "/task", "grok-4", 24199, "s3cr3t", nil)
	for _, a := range spec.Argv {
		if strings.Contains(a, "s3cr3t") {
			t.Fatalf("secret 绝不能进 argv: %v", spec.Argv)
		}
	}
	joined := strings.Join(spec.Argv, " ")
	if !strings.Contains(joined, "--bind 127.0.0.1:24199") {
		t.Fatalf("必须绑定回环端口，实际: %v", spec.Argv)
	}
	var gotSecret, gotHome bool
	for _, kv := range spec.Env {
		switch kv {
		case "GROK_AGENT_SECRET=s3cr3t":
			gotSecret = true
		case "GROK_HOME=/task/grokhome":
			gotHome = true
		}
	}
	if !gotSecret {
		t.Fatalf("secret 必须经 env 传入，实得 %v", spec.Env)
	}
	if !gotHome {
		t.Fatalf("必须注入任务级 GROK_HOME，实得 %v", spec.Env)
	}
	if spec.LockPath == "" || spec.InfoPath == "" || !spec.Sentinel {
		t.Fatalf("LockPath/InfoPath/Sentinel 必填: %+v", spec)
	}
}

// TestServeSpecInjectsEnvBeforeGrokVars 钉住 B19 的 env 注入契约：
// handoff 自身注入的变量必须排在 env 文件之后，才能覆盖同名键。
func TestServeSpecInjectsEnvBeforeGrokVars(t *testing.T) {
	env := []string{
		"HTTPS_PROXY=http://127.0.0.1:7890",
		"GROK_AGENT_SECRET=user_tries_override",
		"GROK_HOME=/user/override",
	}
	spec := grok.ServeSpecForTest("/repo", "/task", "grok-4", 24199, "s3cr3t", env)
	secretIdx, homeIdx := -1, -1
	for i, kv := range spec.Env {
		if kv == "GROK_AGENT_SECRET=s3cr3t" {
			secretIdx = i
		}
		if kv == "GROK_HOME=/task/grokhome" {
			homeIdx = i
		}
	}
	if secretIdx < 0 || homeIdx < 0 {
		t.Fatalf("handoff 注入的 GROK_* 变量缺失: %v", spec.Env)
	}
	// 用户写的覆盖行必须在 handoff 自身行之前，否则覆盖不到
	userSecret := -1
	for i, kv := range spec.Env {
		if kv == "GROK_AGENT_SECRET=user_tries_override" {
			userSecret = i
		}
	}
	if userSecret > secretIdx {
		t.Fatalf("handoff 注入变量必须排在 env 文件之后以取得覆盖优先级，user=%d handoff=%d", userSecret, secretIdx)
	}
}

// allowSection 从 config.toml 提取 allow 数组的文本块（写文件类工具不得回到
// allow 表的断言用；config.toml 不是合法 JSON，按文本截取）。
func allowSection(cfg string) string {
	i := strings.Index(cfg, "allow = [")
	if i < 0 {
		return ""
	}
	rest := cfg[i+len("allow = ["):]
	if j := strings.Index(rest, "]"); j >= 0 {
		return rest[:j]
	}
	return rest
}

// fakeAuthorityConfig 把 HOME 指向临时目录并写出一份权威 config.toml，
// 返回该临时 HOME。body 为空串表示「权威配置不存在」。
//
// 同时设 USERPROFILE：os.UserHomeDir 在 Windows 上读的是它而不是 HOME。
// 本包的测试目前只在 unix runner 上跑，但多设一个变量零成本，省得以后
// CI 扩包时才发现这里是个哑弹。
func fakeAuthorityConfig(t *testing.T, body string) string {
	t.Helper()
	fake := t.TempDir()
	t.Setenv("HOME", fake)
	t.Setenv("USERPROFILE", fake)
	if body == "" {
		return fake
	}
	grokDir := filepath.Join(fake, ".grok")
	if err := os.MkdirAll(grokDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(grokDir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return fake
}

const testAuthorityConfig = `[models]
default = "deepseek-v4-pro"

[model.deepseek-v4-pro]
model = "deepseek-v4-pro"
base_url = "https://example.invalid/v1"
api_key = "sk-SENTINEL-DO-NOT-LOG"
api_backend = "chat_completions"
context_window = 131072

[marketplace]
enabled = true
`

// TestWriteTaskEnvCarriesCustomProvider 验证权威配置里的 provider 定义被搬进
// 任务 config——不搬的话，配了自定义 provider 的机器上 grok 会回落内建
// provider 并报 Authentication required（B135）。
func TestWriteTaskEnvCarriesCustomProvider(t *testing.T) {
	fakeAuthorityConfig(t, testAuthorityConfig)

	home, err := grok.WriteTaskEnv(t.TempDir(), "")
	if err != nil {
		t.Fatalf("WriteTaskEnv 出错: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(b)

	if !strings.Contains(cfg, "[model.deepseek-v4-pro]") {
		t.Errorf("provider 段没被搬进来，实际:\n%s", cfg)
	}
	if !strings.Contains(cfg, `base_url = "https://example.invalid/v1"`) {
		t.Errorf("provider 段字段丢失，实际:\n%s", cfg)
	}
	// 没传 model 时用权威 default 兜底
	if !strings.Contains(cfg, `default = "deepseek-v4-pro"`) {
		t.Errorf("应以权威 default 兜底，实际:\n%s", cfg)
	}
	// 权威的 [marketplace] 不该被搬：只搬 [model.*]
	if strings.Contains(cfg, "[marketplace]") {
		t.Errorf("[marketplace] 不该被搬进任务 config，实际:\n%s", cfg)
	}
	// [models] 只能出现一次——TOML 不允许同名表定义两次，出现两次 grok 直接报错
	if n := strings.Count(cfg, "[models]"); n != 1 {
		t.Errorf("[models] 出现 %d 次，必须恰好 1 次（TOML 不允许重复表定义），实际:\n%s", n, cfg)
	}
}

// TestWriteTaskEnvFlagModelBeatsAuthorityDefault 验证 --model 传入值压过权威
// default——协调者显式指定的模型不能被用户的个人默认值悄悄换掉。
func TestWriteTaskEnvFlagModelBeatsAuthorityDefault(t *testing.T) {
	fakeAuthorityConfig(t, testAuthorityConfig)

	home, err := grok.WriteTaskEnv(t.TempDir(), "deepseek-v4-flash")
	if err != nil {
		t.Fatalf("WriteTaskEnv 出错: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(b)

	if !strings.Contains(cfg, `default = "deepseek-v4-flash"`) {
		t.Errorf("应以 --model 传入值为准，实际:\n%s", cfg)
	}
	if strings.Contains(cfg, `default = "deepseek-v4-pro"`) {
		t.Errorf("权威 default 不该压过传入值，实际:\n%s", cfg)
	}
}

// TestWriteTaskEnvOmitsDefaultWhenNeitherSourceHasOne 验证两个来源都没有模型名
// 时**不写 default 这一行**——写一行空值会让 grok 拿空串当模型名去请求。
func TestWriteTaskEnvOmitsDefaultWhenNeitherSourceHasOne(t *testing.T) {
	fakeAuthorityConfig(t, `[model.x]
model = "x"
base_url = "https://example.invalid/v1"
`)

	home, err := grok.WriteTaskEnv(t.TempDir(), "")
	if err != nil {
		t.Fatalf("WriteTaskEnv 出错: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(b)

	if strings.Contains(cfg, "default =") {
		t.Errorf("两个来源都没模型名时不该写 default 行，实际:\n%s", cfg)
	}
	// 但 provider 段照搬不误——这两件事互不依赖
	if !strings.Contains(cfg, "[model.x]") {
		t.Errorf("provider 段仍应搬运，实际:\n%s", cfg)
	}
}

// TestWriteTaskEnvWithoutAuthorityIsByteIdentical 是**回归保护**：不用自定义
// provider 的用户必须零影响。权威配置不存在时，生成结果要与本条改动之前
// 逐字节相同。
//
// golden 取自改动前 `WriteTaskEnv(dir, "grok-4.5")` 的实际输出。
func TestWriteTaskEnvWithoutAuthorityIsByteIdentical(t *testing.T) {
	fakeAuthorityConfig(t, "") // 权威配置不存在

	home, err := grok.WriteTaskEnv(t.TempDir(), "grok-4.5")
	if err != nil {
		t.Fatalf("WriteTaskEnv 出错: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "taskenv_config_golden.toml"))
	if err != nil {
		t.Fatalf("读 golden 失败（先按计划 Step 2 生成它）: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("无权威配置时生成结果与 golden 不一致——不用自定义 provider 的用户被影响了。\n实际:\n%s\n期望:\n%s", got, want)
	}
}

// TestWriteTaskEnvNeverLogsSecrets 是**承重**用例：日志里出现 api_key 等于
// 把用户密钥写进 agentd.log。
func TestWriteTaskEnvNeverLogsSecrets(t *testing.T) {
	fakeAuthorityConfig(t, testAuthorityConfig)

	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })

	if _, err := grok.WriteTaskEnv(t.TempDir(), ""); err != nil {
		t.Fatalf("WriteTaskEnv 出错: %v", err)
	}
	if strings.Contains(buf.String(), "sk-SENTINEL-DO-NOT-LOG") {
		t.Errorf("日志里出现了 api_key，实际日志:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "example.invalid") {
		t.Errorf("日志里出现了 base_url，段内容一律不许进日志，实际日志:\n%s", buf.String())
	}
	// 段名可以打，且应该打——否则出问题时无从判断到底搬了什么
	if !strings.Contains(buf.String(), "model.deepseek-v4-pro") {
		t.Errorf("日志应含段名以便排障，实际日志:\n%s", buf.String())
	}
}

// TestWriteTaskEnvCarriesAuxiliaryModelKnobs 是 B138 的端到端断言：权威配置里
// [models] 的辅助旋钮必须出现在任务级 config 里，且整份文件只能有一个 [models]
// 段（TOML 不允许同名表定义两次，写重了 grok 直接解析失败）。
//
// 承重点在「只搬 default」曾经的代价：任务级 home 里 web_search / session_summary
// 一律缺失 → grok 回落内建 grok-4.6 → 自定义 provider 400。
func TestWriteTaskEnvCarriesAuxiliaryModelKnobs(t *testing.T) {
	fakeAuthorityConfig(t, `[models]
default = "deepseek-v4-pro"
web_search = "deepseek-v4-flash"
session_summary = "deepseek-v4-flash"

[model.deepseek-v4-pro]
model = "deepseek-v4-pro"
base_url = "https://example.invalid/v1"
`)

	home, err := grok.WriteTaskEnv(t.TempDir(), "")
	if err != nil {
		t.Fatalf("WriteTaskEnv 出错: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(b)

	for _, want := range []string{
		`default = "deepseek-v4-pro"`,
		`web_search = "deepseek-v4-flash"`,
		`session_summary = "deepseek-v4-flash"`,
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("任务级 config 缺 %q，实际:\n%s", want, cfg)
		}
	}
	if n := strings.Count(cfg, "[models]"); n != 1 {
		t.Errorf("[models] 段出现 %d 次（必须恰好 1 次，重复定义会让 grok 解析失败），实际:\n%s", n, cfg)
	}
	// --model 传入时仍以它为准，辅助旋钮照搬不误——两件事互不干扰
	home2, err := grok.WriteTaskEnv(t.TempDir(), "deepseek-v4-flash")
	if err != nil {
		t.Fatalf("WriteTaskEnv 出错: %v", err)
	}
	b2, err := os.ReadFile(filepath.Join(home2, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg2 := string(b2)
	if !strings.Contains(cfg2, `default = "deepseek-v4-flash"`) {
		t.Errorf("--model 应压过权威 default，实际:\n%s", cfg2)
	}
	if !strings.Contains(cfg2, `session_summary = "deepseek-v4-flash"`) {
		t.Errorf("辅助旋钮不该因传了 --model 就丢失，实际:\n%s", cfg2)
	}
}
