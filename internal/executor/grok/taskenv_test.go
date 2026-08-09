package grok_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor/grok"
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
		`"Bash(rm *)"`, `"Bash(*sudo*)"`, `"Bash(*git push*)"`,
		`"Bash(*git reset --hard*)"`, `"Bash(*--force*)"`,
		`"Bash(curl *)"`, `"Bash(wget *)"`, `"WebFetch(*)"`,
	} {
		if !strings.Contains(cfg, rule) {
			t.Errorf("ask 规则缺 %s，实际:\n%s", rule, cfg)
		}
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

func TestWriteServeScriptKeepsSecretOutOfArgv(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "grokhome")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := grok.WriteServeScript(dir, home, 24199, "s3cr3t", nil)
	if err != nil {
		t.Fatalf("WriteServeScript 出错: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)

	// secret 必须走环境变量，绝不能出现在 grok 的命令行参数里
	// （tmux 客户端 argv 本机全局可读，spec §3.1）
	if !strings.Contains(s, "export GROK_AGENT_SECRET=") {
		t.Errorf("secret 必须经环境变量注入，实际:\n%s", s)
	}
	if strings.Contains(s, "--secret") {
		t.Errorf("secret 绝不能进 argv，实际:\n%s", s)
	}
	if !strings.Contains(s, "export GROK_HOME=") {
		t.Errorf("必须注入任务级 GROK_HOME，实际:\n%s", s)
	}
	if !strings.Contains(s, "--bind 127.0.0.1:24199") {
		t.Errorf("必须绑定回环端口，实际:\n%s", s)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("启动脚本权限 = %v，期望 0600（含 secret）", fi.Mode().Perm())
	}
}

// TestWriteServeScriptInjectsEnvBeforeGrokVars 钉住 B19 的 env 注入契约：
// 注入行必须排在 handoff 自身的 GROK_* 之前，值必须单引号包裹。
// 与 opencode 的 TestServeScriptInjectsEnvBeforeOpencodeVars 同构。
func TestWriteServeScriptInjectsEnvBeforeGrokVars(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "grokhome")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"HTTPS_PROXY=http://127.0.0.1:7890",
		"LITERAL=$NOT_EXPANDED",
		"WITHSPACE=a b",
	}
	p, err := grok.WriteServeScript(dir, home, 24199, "s3cr3t", env)
	if err != nil {
		t.Fatalf("WriteServeScript 出错: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)

	proxyIdx := strings.Index(s, "export HTTPS_PROXY='http://127.0.0.1:7890'")
	if proxyIdx < 0 {
		t.Fatalf("脚本缺少注入的 HTTPS_PROXY export 行:\n%s", s)
	}
	// 值必须单引号包裹：Go 侧已展开过一次，不加引号 shell 会再展开一次
	if !strings.Contains(s, "export LITERAL='$NOT_EXPANDED'") {
		t.Errorf("含 $ 的值必须单引号包裹防二次展开:\n%s", s)
	}
	if !strings.Contains(s, "export WITHSPACE='a b'") {
		t.Errorf("含空格的值必须单引号包裹:\n%s", s)
	}
	// 顺序是硬要求：handoff 自身注入的变量必须排在后面才能覆盖 env 文件的同名键
	homeIdx := strings.Index(s, "export GROK_HOME=")
	if homeIdx < 0 || proxyIdx > homeIdx {
		t.Errorf("注入的 env 行必须排在 GROK_HOME 之前（proxy=%d home=%d）:\n%s",
			proxyIdx, homeIdx, s)
	}
}

// TestWriteServeScriptWithoutEnvIsUnchangedInShape 保证 env 为空时脚本形态不变，
// 免得 B19 之前生成的脚本与之后的产生无谓差异。
func TestWriteServeScriptWithoutEnvIsUnchangedInShape(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "grokhome")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := grok.WriteServeScript(dir, home, 24199, "s3cr3t", nil)
	if err != nil {
		t.Fatalf("WriteServeScript 出错: %v", err)
	}
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), "\n\nexport GROK_HOME=") {
		t.Errorf("env 为空时不应留下空行:\n%s", b)
	}
}
