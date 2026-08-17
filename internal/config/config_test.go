// config 包测试：验证默认值生成、token 落盘持久化、targets 解析与严格键校验。
package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"gopkg.in/yaml.v3"
)

func TestLoadGeneratesDefaultsAndToken(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:7777" {
		t.Fatalf("listen=%s", cfg.Listen)
	}
	if len(cfg.Token) < 16 {
		t.Fatalf("token 未生成: %q", cfg.Token)
	}
	// 二次加载读回同一 token（说明已落盘）
	cfg2, err := config.Load(p)
	if err != nil || cfg2.Token != cfg.Token {
		t.Fatalf("token 未持久化")
	}
}

// TestLoadParsesTargets 验证合法配置（已知键）正常解析：token 与 targets 表。
// 此 fixture 全部为已知键——严格解析（L-1）下必须保持可加载，是回归基线。
func TestLoadParsesTargets(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte("token: abc123abc123abc1\ntargets:\n  devbox:\n    addr: \"100.1.2.3:7777\"\n    token: \"tk\"\n"), 0o600)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Targets["devbox"].Addr != "100.1.2.3:7777" {
		t.Fatalf("targets 解析失败")
	}
}

// TestLoadRejectsUnknownKeys 覆盖 L-1 严格解析：未知顶层键（旧版配置的
// access_key 等已废弃标量）必须报错，且错误信息带未知键名与迁移提示——
// 旧实现 yaml.Unmarshal 静默忽略未知键，鉴权等行为会在用户不知情时静默改变
// （安全静默降级）。
func TestLoadRejectsUnknownKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte("token: abc123abc123abc1\naccess_key: \"AKIA...\"\n"), 0o600)
	_, err := config.Load(p)
	if err == nil {
		t.Fatal("未知键配置应报错，实际成功——严格解析未生效")
	}
	if !strings.Contains(err.Error(), "access_key") {
		t.Fatalf("错误信息应含未知键名 access_key, got %v", err)
	}
	if !strings.Contains(err.Error(), "请删除未知键或升级配置") {
		t.Fatalf("错误信息应含迁移提示, got %v", err)
	}
}

// TestLoadRejectsUnknownTargetKeys 验证 KnownFields 对 targets map value
// （Target 结构）同样生效：目标条目内的未知键同样报错。
func TestLoadRejectsUnknownTargetKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte("token: abc123abc123abc1\ntargets:\n  devbox:\n    addr: \"1.2.3.4:5\"\n    secret_key: \"x\"\n"), 0o600)
	_, err := config.Load(p)
	if err == nil {
		t.Fatal("target 条目内未知键应报错，实际成功")
	}
	if !strings.Contains(err.Error(), "secret_key") {
		t.Fatalf("错误信息应含未知键名 secret_key, got %v", err)
	}
}

// TestLoadParsesTargetUser 验证 target 可配置 ssh 用户名（user 键）：
// 解析后 Target.User 就位，供 attach/pull 换算 user@host。
func TestLoadParsesTargetUser(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte("token: abc123abc123abc1\ntargets:\n  devbox:\n    addr: \"100.1.2.3:7777\"\n    user: \"sycm\"\n    token: \"tk\"\n"), 0o600)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Targets["devbox"].User != "sycm" {
		t.Fatalf("target 的 user 字段未解析: %+v", cfg.Targets["devbox"])
	}
}

// TestLoadEmptyFileKeepsDefaults 验证空配置文件按「无内容」处理：
// 保持默认值且不报错（与 yaml.Unmarshal 对空输入的 no-op 语义一致），
// 不能把空文件的 io.EOF 误当解析错误。
func TestLoadEmptyFileKeepsDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, nil, 0o600)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("空文件应正常加载, got %v", err)
	}
	if cfg.Listen != "127.0.0.1:7777" {
		t.Fatalf("listen=%s, want 默认 127.0.0.1:7777", cfg.Listen)
	}
}

// TestLoadRejectsNonPositiveStallTimeout 验证 stalltimeout 显式写成 0 或负值时
// 立即报错，而不是带着一个必然误判的值启动。
//
// 缺陷形态：无校验时 stalltimeout=0 会让看门狗在**每个** running 任务的首个
// tick 上判定 stalled——协调者会被一批凭空的 stalled 事件叫醒，而任务其实好好的。
func TestLoadRejectsNonPositiveStallTimeout(t *testing.T) {
	for _, v := range []string{"0s", "-5m"} {
		p := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(p, []byte("listen: 127.0.0.1:7777\nstalltimeout: "+v+"\n"), 0o600); err != nil {
			t.Fatalf("写配置: %v", err)
		}

		_, err := config.Load(p)

		if err == nil {
			t.Fatalf("stalltimeout=%s 应被拒绝，实际加载成功", v)
		}
		if !strings.Contains(err.Error(), "stalltimeout") {
			t.Errorf("stalltimeout=%s 的错误未点名该配置项: %v", v, err)
		}
	}
}

// TestLoadPhase2Sections 验证二期新增三节配置（approver/executor/terminal）的解析。
func TestLoadPhase2Sections(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte(`
token: abc
approver:
  executor: claude
  model: haiku
  blacklist:
    - "kubectl .*delete"
executor:
  default: opencode
  model: cheap/model
terminal:
  auto: false
`), 0o600)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Approver.Executor != "claude" || cfg.Approver.Model != "haiku" {
		t.Fatalf("approver 解析错误: %+v", cfg.Approver)
	}
	if cfg.Approver.Timeout != 60*time.Second {
		t.Fatalf("approver.timeout 缺省应为 60s，得到 %s", cfg.Approver.Timeout)
	}
	if len(cfg.Approver.Blacklist) != 1 {
		t.Fatalf("blacklist 解析错误")
	}
	if cfg.Executor.Default != "opencode" || cfg.Executor.Model != "cheap/model" {
		t.Fatalf("executor 解析错误: %+v", cfg.Executor)
	}
	if cfg.Terminal.Auto {
		t.Fatalf("terminal.auto=false 未生效")
	}
}

// TestLoadPhase2Defaults 验证二期各节缺省值：approver 默认关闭（Executor 空）、
// timeout 默认 60s、executor 缺省 opencode、terminal.auto 默认 false（不弹）。
func TestLoadPhase2Defaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	cfg, err := config.Load(p) // 首次运行生成默认配置
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Approver.Executor != "" || cfg.Approver.Timeout != 60*time.Second {
		t.Fatalf("approver 默认值错误: %+v", cfg.Approver)
	}
	if cfg.Executor.Default != "opencode" || cfg.Terminal.Auto {
		t.Fatalf("executor/terminal 默认值错误（terminal.auto 默认应为 false）")
	}
}

// TestValidateRejectsBadApproverTimeout 验证 approver 已启用时 timeout 非正被拒绝。
func TestValidateRejectsBadApproverTimeout(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte("token: a\napprover:\n  executor: claude\n  timeout: -1s\n"), 0o600)
	if _, err := config.Load(p); err == nil {
		t.Fatalf("approver.timeout 非正值应被拒绝")
	}
}

// TestValidateRejectsBadBlacklistRegex 验证黑名单正则表达式在启动期预检，
// 非法正则立即报错而不是运行期 panic。
func TestValidateRejectsBadBlacklistRegex(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte("token: a\napprover:\n  executor: claude\n  blacklist:\n    - \"([\"\n"), 0o600)
	if _, err := config.Load(p); err == nil {
		t.Fatalf("非法正则应在启动期被拒绝，而不是运行期 panic")
	}
}

// TestSyncAutoDefaultsTrue 验证省略 sync 键时默认开启自动同步。
func TestSyncAutoDefaultsTrue(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("listen: 127.0.0.1:7777\ntoken: t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Sync.Auto {
		t.Error("sync.auto 省略时应默认 true")
	}
}

func TestEnvSectionParsed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	body := "listen: \"127.0.0.1:7777\"\ntoken: \"t\"\nenv:\n  opencode: dev.env\n  claude: work.env\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Env["opencode"] != "dev.env" || cfg.Env["claude"] != "work.env" {
		t.Fatalf("env 段解析错误: %#v", cfg.Env)
	}
}

func TestEnvDefaultsToEmptyMap(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("token: \"t\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Env == nil {
		t.Fatal("未配置 env 时应为空 map 而非 nil，避免调用方各自判空")
	}
}

func TestUnknownKeyErrorMentionsEnv(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("bogus_key: 1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := config.Load(p)
	if err == nil {
		t.Fatal("未知键应报错")
	}
	if !strings.Contains(err.Error(), "env{") {
		t.Errorf("已知键清单应含 env 段，实际 %q", err.Error())
	}
}

// 未知字段的报错不得再把 update 列进已知键清单：字段已删除，
// 清单再写 update{auto,interval} 会让人以为还能配。
func TestUnknownFieldMessageOmitsUpdate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("nonsense_key: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(p)
	if err == nil {
		t.Fatal("未知键应报错")
	}
	if strings.Contains(err.Error(), "update{auto,interval}") {
		t.Fatalf("已知键清单不得再列 update{auto,interval}: %v", err)
	}
}

// TestLoadParsesWebAllowedHosts 验证 web.allowed_hosts 在严格解码下按 tag 正确解析：
// allowed_hosts（snake_case）能解出一个元素，而不是 yaml.v3 默认映射的 allowedhosts。
func TestLoadParsesWebAllowedHosts(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("web:\n  allowed_hosts:\n    - foo.example.com\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Web.AllowedHosts) != 1 || cfg.Web.AllowedHosts[0] != "foo.example.com" {
		t.Fatalf("web.allowed_hosts 解析错误: %#v", cfg.Web.AllowedHosts)
	}
}

// TestLoadAcceptsDeprecatedUpdateKeys 是这次删除里唯一不能出错的一条。
//
// why：配置是 KnownFields(true) 严格解析的——未知键让 agentd **启动失败**。
// v0.1.x 首次运行会把 update.auto / update.interval 写进 config.yaml，
// 直接删字段再严格解码等于让所有旧机器升级后起不来。必须先剥顶层
// update 再解码，并回写把死键从磁盘清掉。
func TestLoadAcceptsDeprecatedUpdateKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("listen: 127.0.0.1:7777\ntoken: tk\nupdate:\n  auto: false\n  interval: 12h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("含 update 键的旧配置必须能加载: %v", err)
	}
	if cfg.Token != "tk" {
		t.Fatalf("剥键不得伤 token，得到 %q", cfg.Token)
	}
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "update") {
		t.Fatalf("Load 必须回写并丢掉 update 段:\n%s", body)
	}
}

func TestLoadFreshFileHasNoUpdateKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if _, err := config.Load(p); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "update") {
		t.Fatalf("首次生成的配置不得含 update:\n%s", body)
	}
}

func TestLoadStripUpdateDoesNotBlockOnSaveFailure(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("listen: 127.0.0.1:7777\ntoken: tk\nupdate:\n  auto: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// chmod 文件本身，不是目录：macOS 上 WriteFile 仍能截断已有 0600 文件，
	// 目录 0500 挡不住回写，用例会假绿。0444 才让 Save 真正失败。
	if err := os.Chmod(p, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o600) })
	if _, err := config.Load(p); err != nil {
		t.Fatalf("回写失败不得阻断启动: %v", err)
	}
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "update") {
		t.Fatalf("回写应失败，磁盘上仍须留着 update 段:\n%s", body)
	}
}

// path_dirs 能被读进来，且空值绝不落盘。
//
// why 空值不能落盘（硬要求）：配置是 KnownFields(true) 严格解析的。没配过这一项的
// 机器上一旦被写进 path_dirs: []，一台还没换版的旧 agentd 读到这个未知键会**直接
// 启动失败**——B59 spec D7 那个坑的反方向同款。
func TestPathDirsRoundTripAndOmitEmpty(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")

	cfg, err := config.Load(p) // 首次运行：生成默认配置并写盘
	if err != nil {
		t.Fatalf("首次加载: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读回配置: %v", err)
	}
	if strings.Contains(string(b), "path_dirs") {
		t.Errorf("未配置时 path_dirs 不得落盘，实得:\n%s", b)
	}

	cfg.PathDirs = []string{"/opt/tools/bin"}
	if err := config.Save(p, cfg); err != nil {
		t.Fatalf("写盘: %v", err)
	}
	got, err := config.Load(p)
	if err != nil {
		t.Fatalf("回读: %v", err)
	}
	if len(got.PathDirs) != 1 || got.PathDirs[0] != "/opt/tools/bin" {
		t.Errorf("path_dirs = %v，期望 [/opt/tools/bin]", got.PathDirs)
	}
}

// 缺省值：不禁用、保留比 0.1。这两个默认值是安全侧的——不写配置的用户
// 也应该被围栏保护。
func TestProcFenceDefaults(t *testing.T) {
	cfg, err := loadFromString(t, "listen: 127.0.0.1:7777\ntoken: t\n")
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if cfg.ProcFence.Disabled {
		t.Fatalf("默认不应禁用围栏")
	}
	if cfg.ProcFence.ReserveRatio != 0.1 {
		t.Fatalf("默认保留比应为 0.1，得到 %v", cfg.ProcFence.ReserveRatio)
	}
}

// 显式配置生效。
func TestProcFenceExplicit(t *testing.T) {
	cfg, err := loadFromString(t, "listen: 127.0.0.1:7777\ntoken: t\n"+
		"proc_fence:\n  disabled: true\n  reserve_ratio: 0.25\n")
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if !cfg.ProcFence.Disabled || cfg.ProcFence.ReserveRatio != 0.25 {
		t.Fatalf("显式配置未生效: %+v", cfg.ProcFence)
	}
}

func TestProcFenceTaskLimitsDefaults(t *testing.T) {
	cfg, err := loadFromString(t, "listen: 127.0.0.1:7777\ntoken: t\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProcFence.TaskBudget != 400 {
		t.Fatalf("TaskBudget 默认应为 400，实际 %d", cfg.ProcFence.TaskBudget)
	}
	if cfg.ProcFence.TaskHardLimit != 1200 {
		t.Fatalf("TaskHardLimit 默认应为 1200，实际 %d", cfg.ProcFence.TaskHardLimit)
	}
}

func TestProcFenceTaskLimitsSanitized(t *testing.T) {
	// why：0 是「关掉这一档」的合法表达，必须原样保留，不能被兜底改回默认值；
	// 负数是配置写错，归零（= 关掉）而不是取绝对值
	for _, c := range []struct {
		name                 string
		yaml                 string
		wantBudget, wantHard int
	}{
		{"零表示关掉，原样保留", "proc_fence:\n  task_budget: 0\n  task_hard_limit: 0\n", 0, 0},
		{"负数归零", "proc_fence:\n  task_budget: -5\n  task_hard_limit: -1\n", 0, 0},
		{"硬上限小于告警线时抬到告警线", "proc_fence:\n  task_budget: 400\n  task_hard_limit: 100\n", 400, 400},
		{"正常值原样", "proc_fence:\n  task_budget: 200\n  task_hard_limit: 800\n", 200, 800},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := loadFromString(t, "listen: 127.0.0.1:7777\ntoken: t\n"+c.yaml)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ProcFence.TaskBudget != c.wantBudget || cfg.ProcFence.TaskHardLimit != c.wantHard {
				t.Fatalf("got (%d,%d) want (%d,%d)",
					cfg.ProcFence.TaskBudget, cfg.ProcFence.TaskHardLimit, c.wantBudget, c.wantHard)
			}
		})
	}
}

func TestProcFenceTaskLimitsYamlKeys(t *testing.T) {
	// why：不加 yaml tag 时 yaml.v3 会把 TaskBudget 映射成 taskbudget，
	// 与 README 里写的 task_budget 对不上——同一个坑 ReserveRatio 已经踩过一次
	var pf config.ProcFenceConfig
	if err := yaml.Unmarshal([]byte("task_budget: 7\ntask_hard_limit: 9\n"), &pf); err != nil {
		t.Fatal(err)
	}
	if pf.TaskBudget != 7 || pf.TaskHardLimit != 9 {
		t.Fatalf("yaml key 未按 snake_case 映射：%+v", pf)
	}
}

// loadFromString 把 yaml 字符串写进临时 config.yaml 再 Load。
func loadFromString(t *testing.T, body string) (*config.Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("写配置: %v", err)
	}
	return config.Load(p)
}

// env_forward 能被读进来，且**未配置时绝不落盘**。
//
// why：这是 spec §4.2 那条「默认值只能在用的时候取」的钉子。实现者最顺手的写法
// 是在 Load 里把内置默认 ["SSH_AUTH_SOCK"] 填进结构体——那样下一次 Save 就会把
// env_forward 写进每台机器的 config.yaml，一台还没换版的旧 agentd 读到这个未知键
// 会直接**启动失败**，而所有功能测试仍然全绿。
func TestEnvForwardRoundTripAndOmitEmpty(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")

	cfg, err := config.Load(p) // 首次运行：生成默认配置并写盘
	if err != nil {
		t.Fatalf("首次加载: %v", err)
	}
	if cfg.EnvForward != nil {
		t.Errorf("Load 不得把默认值填进结构体，实得 %v", cfg.EnvForward)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读回配置: %v", err)
	}
	if strings.Contains(string(b), "env_forward") {
		t.Errorf("未配置时 env_forward 不得落盘，实得:\n%s", b)
	}

	// 显式空列表要能被区分出来：它表示「一个都不转发」，不是「没配过」。
	cfg.EnvForward = []string{}
	if err := config.Save(p, cfg); err != nil {
		t.Fatalf("写盘: %v", err)
	}

	cfg.EnvForward = []string{"SSH_AUTH_SOCK", "GPG_AGENT_INFO"}
	if err := config.Save(p, cfg); err != nil {
		t.Fatalf("写盘: %v", err)
	}
	got, err := config.Load(p)
	if err != nil {
		t.Fatalf("回读: %v", err)
	}
	if len(got.EnvForward) != 2 || got.EnvForward[0] != "SSH_AUTH_SOCK" {
		t.Errorf("env_forward = %v，期望 [SSH_AUTH_SOCK GPG_AGENT_INFO]", got.EnvForward)
	}
}

// TestLoadFillsRepoRootDefault 验证 repo_root 未配置时补 <DataDir>/repos，
// 且配置里写了的值不被覆盖。
//
// 为什么必须有默认值：自动登记（B62 §6）把 clone 变成首次派发的主路径，
// repo_root 为空时 agentd 直接拒绝 clone，全新开发机上第一次派发必然失败。
func TestLoadFillsRepoRootDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	// 首次运行：文件不存在，生成默认配置并写盘。
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := filepath.Join(cfg.DataDir, "repos")
	if cfg.RepoRoot != want {
		t.Fatalf("RepoRoot = %q, want %q", cfg.RepoRoot, want)
	}
	// 默认值必须**落盘**，让人看得见，而不是藏在使用点。
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读回配置: %v", err)
	}
	if !strings.Contains(string(b), "repo_root:") {
		t.Fatalf("默认 repo_root 应随首次写盘落到 config.yaml，实际内容:\n%s", b)
	}

	// 显式配置不被覆盖。
	explicit := filepath.Join(dir, "explicit.yaml")
	if err := os.WriteFile(explicit,
		[]byte("token: abc\nrepo_root: /srv/code\n"), 0o600); err != nil {
		t.Fatalf("写测试配置: %v", err)
	}
	cfg2, err := config.Load(explicit)
	if err != nil {
		t.Fatalf("Load(explicit): %v", err)
	}
	if cfg2.RepoRoot != "/srv/code" {
		t.Fatalf("显式 repo_root 被覆盖了: %q", cfg2.RepoRoot)
	}
}

func TestProxyParsedAndValidated(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("proxy: socks5://127.0.0.1:1080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("加载: %v", err)
	}
	if cfg.Proxy != "socks5://127.0.0.1:1080" {
		t.Errorf("proxy = %q，期望 socks5://127.0.0.1:1080", cfg.Proxy)
	}
}

// 坏代理必须在**启动期**被拒。运行期容错会让它表现为"后台更新检查什么都不发生"，
// 而那条路径的纪律是失败静默跳过，于是错误配置可以数月无人察觉。
func TestLoadRejectsBadProxy(t *testing.T) {
	for _, bad := range []string{"socks4://h:1080", "127.0.0.1:1080", "http://"} {
		dir := t.TempDir()
		p := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(p, []byte("proxy: \""+bad+"\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.Load(p); err == nil {
			t.Errorf("proxy=%q 应拒绝加载", bad)
		}
	}
}

// 旧版本兼容契约：未配置时 proxy 键不得落盘，否则旧 agentd 的 KnownFields
// 读到未知键就再也起不来（与 path_dirs 同款教训）。
func TestProxyOmitEmptyOnSave(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if _, err := config.Load(p); err != nil { // 首次运行写盘
		t.Fatalf("首次加载: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "proxy") {
		t.Errorf("未配置时 proxy 不得落盘，实得:\n%s", b)
	}
}

// Defaults 必须零磁盘副作用：firstRun 写盘是 Load 的职责，Defaults 是
// 给桌面壳首次引导用的不落盘入口。若它写盘，向导中途 SIGKILL/崩溃后磁盘上
// 会留下 config.yaml，下次启动 Resolve 判「已配置」，用户回不到向导
// （该缺陷已在真机 SIGKILL 复现，回滚法封不死——进程死了就没有回滚）。
func TestDefaultsWritesNothingToDisk(t *testing.T) {
	// 隔离 HOME：Defaults 不应在真实 ~/.handoff 附近留下任何东西
	t.Setenv("HOME", t.TempDir())
	cfg := config.Defaults()
	if cfg.Token == "" {
		t.Fatal("Defaults 应生成随机 token")
	}
	if _, err := os.Stat(config.DefaultPath()); !os.IsNotExist(err) {
		t.Fatalf("Defaults 不得写盘：DefaultPath()=%s 已存在", config.DefaultPath())
	}
	// 二次调用也应幂等、无副作用。校验成功性由 Defaults 自身的契约覆盖：
	// 它内部 validate 失败即 panic，能走到这里说明出厂默认已通过校验。
	cfg2 := config.Defaults()
	if cfg2.Token == "" {
		t.Fatal("二次调用 Defaults 也应生成随机 token")
	}
}

// 未知键的错误提示必须把 proxy 列进"支持的键"，否则用户配对了却被拒时无从判断。
func TestUnknownKeyErrorMentionsProxy(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("nosuchkey: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(p)
	if err == nil {
		t.Fatal("未知键应被拒")
	}
	if !strings.Contains(err.Error(), "proxy") {
		t.Errorf("错误文本应列出 proxy，实得 %q", err)
	}
}
