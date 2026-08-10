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
