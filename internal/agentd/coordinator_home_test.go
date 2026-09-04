// coordinator_home_test.go —— 协调者隔离 HOME 的 Launch/Resume 供给缝测试。
//
// 职责：从 coordinatorRunner 入口验证配置、规则、缺失凭据与子进程 HOME 的真实
// 文件/进程边界；不测试 WakeHome 的检测供给路径。
// 边界：只使用临时主 HOME、隔离 HOME 和 fake opencode，不代表真实 CLI 登录行为。
package agentd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/hostapi"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/toolchain"
)

func installCoordinatorFakeCLI(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	capture := filepath.Join(t.TempDir(), "coordinator-cli.txt")
	script := `#!/bin/sh
: "${COORD_CAPTURE:?COORD_CAPTURE 必须设置}"
for arg in "$@"; do printf 'arg:%s\n' "$arg" >>"$COORD_CAPTURE"; done
printf 'env:HOME=%s\n' "$HOME" >>"$COORD_CAPTURE"
printf '%s\n' '{"type":"step_start","sessionID":"runner-sess","part":{"type":"step-start"}}'
printf '%s\n' '{"type":"text","sessionID":"runner-sess","part":{"type":"text","text":"runner-ok"}}'
printf '%s\n' '{"type":"step_finish","sessionID":"runner-sess","part":{"type":"step-finish","reason":"stop"}}'
`
	fake := filepath.Join(binDir, "opencode")
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatalf("写 coordinator fake CLI: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("COORD_CAPTURE", capture)
	return capture
}

func requireFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s 内容=%q，want %q", path, got, want)
	}
}

func TestCoordinatorHomeLaunchAndResumeSupplyFullHome(t *testing.T) {
	capture := installCoordinatorFakeCLI(t)
	mainHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mainHome, ".config", "opencode", "skills", "plan"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mainHome, ".config", "opencode", "AGENTS.md"), []byte("agents"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mainHome, ".config", "opencode", "skills", "plan", "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	auth := filepath.Join(mainHome, ".local", "share", "opencode", "auth.json")
	if err := os.MkdirAll(filepath.Dir(auth), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(auth, []byte("auth-live"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mainHome, ".local", "share", "opencode", "other.db"), []byte("do-not-copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "muse")
	dataDir := filepath.Join(t.TempDir(), "agentd-data")
	cfg := &config.Config{
		Token:        "agentd-live",
		DataDir:      dataDir,
		StallTimeout: time.Hour,
		Ledger:       config.LedgerConfig{DSN: "ledger.sqlite"},
	}
	supplier := coordinatorHomeSupplier{
		currentConfig:  func() *config.Config { return cfg },
		userHomeDir:    func() (string, error) { return mainHome, nil },
		expandHomeDir:  func(string) (string, error) { return target, nil },
		credentialPath: toolchain.CredRelPathFor,
	}
	runner := coordinatorRunner{h: hostapi.NewWithCredentialPathFor(toolchain.CredRelPathFor), prepareHome: supplier.Prepare}

	spec := keysclient.SessionSpec{CLI: "opencode", HomeDir: "~/.handoff/home/muse", Workdir: t.TempDir()}
	launch, err := runner.Launch(spec, "first")
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if launch.SessionID != "runner-sess" || launch.Output != "runner-ok" {
		t.Fatalf("Launch result=%+v", launch)
	}
	loaded, err := config.Load(filepath.Join(target, ".handoff", "config.yaml"))
	if err != nil {
		t.Fatalf("回读隔离配置: %v", err)
	}
	absDSN, err := filepath.Abs("ledger.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Token != "agentd-live" || loaded.DataDir != dataDir || loaded.Ledger.DSN != absDSN {
		t.Fatalf("隔离配置投影错误: token=%q data=%q dsn=%q", loaded.Token, loaded.DataDir, loaded.Ledger.DSN)
	}
	requireFileContent(t, filepath.Join(target, ".config", "opencode", "AGENTS.md"), "agents")
	requireFileContent(t, filepath.Join(target, ".config", "opencode", "skills", "plan", "SKILL.md"), "skill")
	requireFileContent(t, filepath.Join(target, ".local", "share", "opencode", "auth.json"), "auth-live")
	if _, err := os.Stat(filepath.Join(target, ".local", "share", "opencode", "other.db")); !os.IsNotExist(err) {
		t.Fatalf("白名单外的 opencode 数据不得同步: stat err=%v", err)
	}
	if lines, readErr := os.ReadFile(capture); readErr != nil || !strings.Contains(string(lines), "env:HOME="+target) || strings.Contains(string(lines), "env:HOME=~") {
		t.Fatalf("fake CLI HOME 证据错误: %q/%v", lines, readErr)
	}

	occupied := filepath.Join(t.TempDir(), "occupied")
	if err := os.MkdirAll(filepath.Join(occupied, ".local", "share", "opencode"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(filepath.Join(occupied, ".handoff", "config.yaml"), &config.Config{Token: "first-run", DataDir: dataDir, StallTimeout: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(occupied, ".local", "share", "opencode", "sessions.db"), []byte("session-sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(occupied, ".local", "share", "opencode", "auth.json"), []byte("old-auth"), 0o600); err != nil {
		t.Fatal(err)
	}
	occupiedSupplier := supplier
	occupiedSupplier.expandHomeDir = func(string) (string, error) { return occupied, nil }
	occupiedRunner := coordinatorRunner{h: hostapi.NewWithCredentialPathFor(toolchain.CredRelPathFor), prepareHome: occupiedSupplier.Prepare}
	if _, err := occupiedRunner.Launch(spec, "occupied"); err != nil {
		t.Fatalf("occupied Launch: %v", err)
	}
	occupiedConfig, err := config.Load(filepath.Join(occupied, ".handoff", "config.yaml"))
	if err != nil {
		t.Fatalf("回读 occupied 配置: %v", err)
	}
	if occupiedConfig.Token != "agentd-live" {
		t.Fatalf("occupied first-run token 未覆盖: %q", occupiedConfig.Token)
	}
	requireFileContent(t, filepath.Join(occupied, ".local", "share", "opencode", "sessions.db"), "session-sentinel")
	requireFileContent(t, filepath.Join(occupied, ".local", "share", "opencode", "auth.json"), "old-auth")

	resume, err := runner.Resume(keysclient.SessionRef{CLI: "opencode", SessionID: "runner-sess", HomeDir: "~/.handoff/home/muse", Workdir: t.TempDir()}, "resume")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resume.SessionID != "runner-sess" {
		t.Fatalf("Resume result=%+v", resume)
	}
	resumeEvidence, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(resumeEvidence), "arg:-s\narg:runner-sess") {
		t.Fatalf("Resume 未携带 session argv: %q", resumeEvidence)
	}
}

func TestCoordinatorHomeSupplierRejectsExpansionFailure(t *testing.T) {
	p := coordinatorHomeSupplier{
		currentConfig: func() *config.Config { return &config.Config{Token: "x", DataDir: t.TempDir()} },
		userHomeDir:   func() (string, error) { return "", errors.New("home unavailable") },
		expandHomeDir: func(string) (string, error) { return "", errors.New("expand unavailable") },
	}
	_, err := p.Prepare(keysclient.SessionSpec{CLI: "opencode", HomeDir: "~/muse"})
	if err == nil || !strings.Contains(err.Error(), "展开协调者供给 HOME") || !strings.Contains(err.Error(), "~/muse") {
		t.Fatalf("供给展开失败错误缺上下文: %v", err)
	}
}

func TestNormalizeCoordinatorSpecRequiresAbsoluteHome(t *testing.T) {
	if _, err := normalizeCoordinatorSpec(keysclient.SessionSpec{CLI: "opencode"}); err == nil ||
		!strings.Contains(err.Error(), "HomeDir") {
		t.Fatalf("空 HomeDir 必须被拒绝: %v", err)
	}
	if _, err := normalizeCoordinatorSpec(keysclient.SessionSpec{CLI: "opencode", HomeDir: "relative-home"}); err == nil ||
		!strings.Contains(err.Error(), "绝对路径") {
		t.Fatalf("相对 HomeDir 必须被拒绝: %v", err)
	}
}

func TestCoordinatorHomeSupplierRejectsSymlinkedRuleParent(t *testing.T) {
	mainHome := t.TempDir()
	source := filepath.Join(t.TempDir(), "opencode-source")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "AGENTS.md"), []byte("agents"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(mainHome, ".config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, filepath.Join(mainHome, ".config", "opencode")); err != nil {
		t.Skipf("创建 symlink 不可用: %v", err)
	}
	target := filepath.Join(t.TempDir(), "muse")
	p := coordinatorHomeSupplier{
		currentConfig: func() *config.Config {
			return &config.Config{Token: "x", DataDir: t.TempDir(), StallTimeout: time.Hour}
		},
		userHomeDir:    func() (string, error) { return mainHome, nil },
		expandHomeDir:  func(string) (string, error) { return target, nil },
		credentialPath: toolchain.CredRelPathFor,
	}
	if _, err := p.Prepare(keysclient.SessionSpec{CLI: "opencode", HomeDir: "~/muse"}); err == nil ||
		!strings.Contains(err.Error(), "symlink") {
		t.Fatalf("规则父路径 symlink 必须被拒绝: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".config", "opencode", "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("拒绝 symlink 后不得在目标写规则: stat err=%v", err)
	}
}
