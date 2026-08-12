// handoff init 的 CLI 行为测试。
//
// 交互经 rootCmd.SetIn 喂脚本化答案，tty 判定经 initStdinIsTTY 缝控制，
// 因此测试既能覆盖交互分支也不需要真的终端。
package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/config"
)

// runInit 跑一次 init：answers 是按行喂给 stdin 的答案，tty 控制是否走交互分支。
func runInit(t *testing.T, cfgPath string, tty bool, answers string) (string, error) {
	t.Helper()
	resetFlags(t)
	oldTTY := initStdinIsTTY
	initStdinIsTTY = func() bool { return tty }
	t.Cleanup(func() { initStdinIsTTY = oldTTY })

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetIn(strings.NewReader(answers))
	rootCmd.SetArgs([]string{"init", "--config", cfgPath})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetIn(nil)
	})
	err := rootCmd.ExecuteContext(context.Background())
	return buf.String(), err
}

// loadCfg 读回写盘的配置。
func loadCfg(t *testing.T, p string) *config.Config {
	t.Helper()
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("回读配置: %v", err)
	}
	return cfg
}

// 非 tty 时一问不问，只探测 + 写出厂默认，并明确告诉用户下一步。
//
// why：init 会被 install.sh 经管道调起（curl … | bash），那种场景下 stdin
// 被脚本占着。问了也没人答，卡住比不问糟得多。
func TestInitNonInteractiveWritesDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	out, err := runInit(t, p, false, "")
	if err != nil {
		t.Fatalf("init 不应报错: %v", err)
	}
	if !strings.Contains(out, "未交互配置") {
		t.Errorf("非 tty 时应提示未交互配置:\n%s", out)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("配置应被写出来: %v", err)
	}
	cfg := loadCfg(t, p)
	if cfg.Listen != "127.0.0.1:7777" {
		t.Errorf("listen 应为出厂默认，得到 %q", cfg.Listen)
	}
	if cfg.Token == "" {
		t.Error("token 应被生成")
	}
	if !cfg.Update.Auto {
		t.Error("update.auto 应为出厂默认 true")
	}
}

// 探测表必须打印，且四家都在。
func TestInitPrintsDetectionTable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	out, _ := runInit(t, p, false, "")
	for _, n := range []string{"opencode", "claude", "grok", "codex"} {
		if !strings.Contains(out, n) {
			t.Errorf("探测表缺 %s:\n%s", n, out)
		}
	}
}

// 交互下全部回车（取默认）：应写出一份合法配置，且角色默认能走通。
func TestInitInteractiveAllDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	// 30 个空行足够覆盖所有提问，多余的不会被读
	out, err := runInit(t, p, true, strings.Repeat("\n", 30))
	if err != nil {
		t.Fatalf("init 不应报错: %v\n%s", err, out)
	}
	cfg := loadCfg(t, p)
	if cfg.Listen == "" || cfg.Token == "" {
		t.Fatalf("配置不完整: %+v", cfg)
	}
}

// 幂等：已有配置时，每一问的默认值取当前值，全回车即原样保持。
//
// why 这条是 init 能当「改配置工具」用的前提。若默认值退回出厂值，
// 用户重跑一次 init 就会把 listen、token、targets 全部冲掉。
func TestInitIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	body := "listen: 0.0.0.0:7788\ntoken: keepme\nrepo_root: /srv/repos\nexecutor:\n  default: grok\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runInit(t, p, true, strings.Repeat("\n", 30)); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg := loadCfg(t, p)
	if cfg.Listen != "0.0.0.0:7788" {
		t.Errorf("listen 被冲掉了: %q", cfg.Listen)
	}
	if cfg.Token != "keepme" {
		t.Errorf("token 被冲掉了: %q", cfg.Token)
	}
	if cfg.RepoRoot != "/srv/repos" {
		t.Errorf("repo_root 被冲掉了: %q", cfg.RepoRoot)
	}
	if cfg.Executor.Default != "grok" {
		t.Errorf("executor.default 被冲掉了: %q", cfg.Executor.Default)
	}
}

// 显式回答要被采纳：选审核者机角色 + 给一个 listen。
func TestInitAcceptsAnswers(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	// 角色选 1（执行机），listen 给 0.0.0.0:7799，其余回车
	answers := "1\n\n\n0.0.0.0:7799\n" + strings.Repeat("\n", 26)
	if _, err := runInit(t, p, true, answers); err != nil {
		t.Fatalf("init: %v", err)
	}
	if got := loadCfg(t, p).Listen; got != "0.0.0.0:7799" {
		t.Fatalf("listen=%q，期望采纳输入的 0.0.0.0:7799", got)
	}
}

// 末尾必须打印本机 token 与现成的配对片段——审核者机要靠它配 targets。
func TestInitPrintsPairingSnippet(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	out, err := runInit(t, p, true, "1\n"+strings.Repeat("\n", 29))
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(out, "targets:") {
		t.Errorf("应打印现成的配对 yaml 片段:\n%s", out)
	}
	if !strings.Contains(out, loadCfg(t, p).Token) {
		t.Error("配对片段里应含本机 token")
	}
}
