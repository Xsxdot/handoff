// config 包测试：验证默认值生成、token 落盘持久化、targets 解析与严格键校验。
package config_test

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/config"
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
// tick 上判定 stalled——审核者会被一批凭空的 stalled 事件叫醒，而任务其实好好的。
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

// 没写 update 段时必须落在出厂默认上。
//
// why 单独钉一例：Load 用的是「字面量预置默认 + yaml 覆盖式解码」，
// 新加的段一旦忘了写进那个字面量，表现就是 Auto=false、Interval=0——
// 自动更新静默不工作，且没有任何报错。
func TestUpdateDefaults(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("listen: 127.0.0.1:7777\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Update.Auto {
		t.Error("update.auto 默认应为 true")
	}
	if cfg.Update.Interval != 6*time.Hour {
		t.Errorf("update.interval 默认应为 6h，得到 %s", cfg.Update.Interval)
	}
}

// 显式写了就以写的为准。
func TestUpdateExplicit(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	body := "listen: 127.0.0.1:7777\nupdate:\n  auto: false\n  interval: 30m\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Update.Auto {
		t.Error("显式 auto: false 未生效")
	}
	if cfg.Update.Interval != 30*time.Minute {
		t.Errorf("interval=%s，期望 30m", cfg.Update.Interval)
	}
}

// 启用自动更新却给了非正 interval：必须在启动期拦下。
//
// why：0 会让更新循环退化成忙轮询，每个 tick 都立刻到期，把 GitHub API
// 的匿名限流（60 次/小时）几秒钟打满，然后所有版本检查一起失败。
// 这和 stalltimeout 必须为正是同一类问题，处置也保持一致：显式写错才拦，
// 省略该键走默认值是正常用法。
func TestUpdateIntervalMustBePositiveWhenAuto(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	body := "listen: 127.0.0.1:7777\nupdate:\n  auto: true\n  interval: 0s\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(p); err == nil {
		t.Fatal("interval=0 且 auto=true 时应报错")
	} else if !strings.Contains(err.Error(), "update.interval") {
		t.Fatalf("报错应点名 update.interval，得到: %v", err)
	}
}

// 关掉自动更新时不校验 interval——没启用的东西写错不该拦启动。
func TestUpdateIntervalNotCheckedWhenAutoOff(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	body := "listen: 127.0.0.1:7777\nupdate:\n  auto: false\n  interval: 0s\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(p); err != nil {
		t.Fatalf("auto=false 时不应校验 interval，却报错: %v", err)
	}
}

// 未知字段的报错必须把 update 列进已知键清单。
//
// why：那条消息是用户唯一能看到的「支持哪些键」的清单。漏了 update，
// 用户配了正确的键、看到「不支持」的报错，会去删掉本来对的配置。
func TestUnknownFieldMessageListsUpdate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("nonsense_key: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(p)
	if err == nil {
		t.Fatal("未知键应报错")
	}
	if !strings.Contains(err.Error(), "update{auto,interval}") {
		t.Fatalf("已知键清单里缺 update{auto,interval}: %v", err)
	}
}

// TestLoadAcceptsDeprecatedUpdateKeys 是这次删除里唯一不能出错的一条。
//
// why：配置是 KnownFields(true) 严格解析的——未知键让 agentd **启动失败**。
// v0.1.0 首次运行会把 update.auto / update.interval 写进 config.yaml，
// 直接删字段等于让所有装过 v0.1.0 的机器升级后起不来，正是这个设计要
// 消灭的那类失配的最狠形态。
func TestLoadAcceptsDeprecatedUpdateKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte("token: tk\nupdate:\n  auto: false\n  interval: 12h\n"), 0o600)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("含 update 键的旧配置必须能正常加载: %v", err)
	}
	if cfg.Update.Auto {
		t.Fatal("字段值仍应被解出来（只是不再有效果）")
	}
}

// TestWarnDeprecatedFiresOnNonDefault：取值非默认时必须 Warn。
// 用户把 auto 设成 false 是有意图的，悄悄让它失效等于骗人。
func TestWarnDeprecatedFiresOnNonDefault(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	(&config.Config{Update: config.UpdateConfig{Auto: false, Interval: 6 * time.Hour}}).WarnDeprecated(log)
	if !strings.Contains(buf.String(), "update.auto") {
		t.Fatalf("非默认值必须 Warn:\n%s", buf.String())
	}
}

// TestWarnDeprecatedSilentOnDefault：默认值不打——绝大多数机器都是默认值，
// 每次启动打一条无从处置的 Warn，只会让人学会忽略日志。
func TestWarnDeprecatedSilentOnDefault(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	(&config.Config{Update: config.UpdateConfig{Auto: true, Interval: 6 * time.Hour}}).WarnDeprecated(log)
	if buf.Len() != 0 {
		t.Fatalf("默认值不该打 Warn:\n%s", buf.String())
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
