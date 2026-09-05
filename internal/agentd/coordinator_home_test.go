package agentd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/hostapi"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/scheduling"
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

func TestCoordinatorHomeSupplyOnLaunchAndResume(t *testing.T) {
	capture := installCoordinatorFakeCLI(t)

	mainHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mainHome, ".config", "opencode", "skills", "plan"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mainHome, ".config", "opencode", "AGENTS.md"), []byte("agents-doc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mainHome, ".config", "opencode", "skills", "plan", "SKILL.md"), []byte("skill-plan"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(mainHome, ".local", "share", "opencode"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mainHome, ".local", "share", "opencode", "auth.json"), []byte("main-auth-token"), 0o600); err != nil {
		t.Fatal(err)
	}

	liveDataDir := t.TempDir()
	liveCfg := &config.Config{
		Token:        "agentd-live",
		DataDir:      liveDataDir,
		StallTimeout: 2 * time.Hour,
		Ledger:       config.LedgerConfig{DSN: "ledger.db"},
	}

	targetDir := t.TempDir()
	expandHome := func(p string) (string, error) {
		if p == "~/.handoff/home/muse" {
			return targetDir, nil
		}
		return hostapi.ExpandHomePath(p)
	}

	supplier := coordinatorHomeSupplier{
		currentConfig:  func() *config.Config { return liveCfg },
		userHomeDir:    func() (string, error) { return mainHome, nil },
		expandHomeDir:  expandHome,
		credentialPath: toolchain.CredRelPathFor,
	}

	h := hostapi.NewWithCredentialPathFor(toolchain.CredRelPathFor)
	runner := coordinatorRunner{h: h, prepareHome: supplier.Prepare}

	spec := keysclient.SessionSpec{
		CLI:     "opencode",
		HomeDir: "~/.handoff/home/muse",
		Workdir: t.TempDir(),
	}

	res, err := runner.Launch(spec, "测试启动")
	if err != nil {
		t.Fatalf("runner.Launch: %v", err)
	}
	if res.SessionID != "runner-sess" {
		t.Fatalf("SessionID=%q, want runner-sess", res.SessionID)
	}

	cfgPath := filepath.Join(targetDir, ".handoff", "config.yaml")
	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load(%s): %v", cfgPath, err)
	}
	if loaded.Token != "agentd-live" {
		t.Fatalf("loaded.Token = %q, want agentd-live", loaded.Token)
	}
	absData, _ := filepath.Abs(liveDataDir)
	if loaded.DataDir != absData {
		t.Fatalf("loaded.DataDir = %q, want %q", loaded.DataDir, absData)
	}
	absDSN, _ := filepath.Abs("ledger.db")
	if loaded.Ledger.DSN != absDSN {
		t.Fatalf("loaded.Ledger.DSN = %q, want %q", loaded.Ledger.DSN, absDSN)
	}

	if b, err := os.ReadFile(filepath.Join(targetDir, ".config", "opencode", "AGENTS.md")); err != nil || string(b) != "agents-doc" {
		t.Fatalf("AGENTS.md = %q/%v, want agents-doc", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(targetDir, ".config", "opencode", "skills", "plan", "SKILL.md")); err != nil || string(b) != "skill-plan" {
		t.Fatalf("SKILL.md = %q/%v, want skill-plan", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(targetDir, ".local", "share", "opencode", "auth.json")); err != nil || string(b) != "main-auth-token" {
		t.Fatalf("auth.json = %q/%v, want main-auth-token", b, err)
	}

	rawCapture, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("读 capture: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(rawCapture)), "\n")
	wantHome := "env:HOME=" + targetDir
	found := false
	for _, l := range lines {
		if strings.HasPrefix(l, "env:HOME=~") {
			t.Fatalf("字面 ~ 进入子进程: %v", lines)
		}
		if l == wantHome {
			found = true
		}
	}
	if !found {
		t.Fatalf("未找到子进程绝对 HOME=%q，捕获行: %v", wantHome, lines)
	}

	t.Run("OccupiedTargetOverwritesConfigPreservesSessionAndAuth", func(t *testing.T) {
		occupiedTarget := t.TempDir()
		if err := os.MkdirAll(filepath.Join(occupiedTarget, ".handoff"), 0o700); err != nil {
			t.Fatal(err)
		}
		firstRunCfg := &config.Config{Token: "first-run-stale-token", DataDir: "/stale", StallTimeout: 2 * time.Hour}
		if err := config.Save(filepath.Join(occupiedTarget, ".handoff", "config.yaml"), firstRunCfg); err != nil {
			t.Fatal(err)
		}

		opencodeDir := filepath.Join(occupiedTarget, ".local", "share", "opencode")
		if err := os.MkdirAll(opencodeDir, 0o700); err != nil {
			t.Fatal(err)
		}
		sentinelPath := filepath.Join(opencodeDir, "sessions.db")
		if err := os.WriteFile(sentinelPath, []byte("sentinel-sessions-db"), 0o600); err != nil {
			t.Fatal(err)
		}
		existingAuth := filepath.Join(opencodeDir, "auth.json")
		if err := os.WriteFile(existingAuth, []byte("existing-auth-token"), 0o600); err != nil {
			t.Fatal(err)
		}

		occupiedSupplier := coordinatorHomeSupplier{
			currentConfig: func() *config.Config { return liveCfg },
			userHomeDir:   func() (string, error) { return mainHome, nil },
			expandHomeDir: func(p string) (string, error) {
				if p == "~/.handoff/home/occupied" {
					return occupiedTarget, nil
				}
				return hostapi.ExpandHomePath(p)
			},
			credentialPath: toolchain.CredRelPathFor,
		}
		occRunner := coordinatorRunner{h: h, prepareHome: occupiedSupplier.Prepare}

		occSpec := keysclient.SessionSpec{
			CLI:     "opencode",
			HomeDir: "~/.handoff/home/occupied",
			Workdir: t.TempDir(),
		}
		if _, err := occRunner.Launch(occSpec, "occupied launch"); err != nil {
			t.Fatalf("occRunner.Launch: %v", err)
		}

		occLoaded, err := config.Load(filepath.Join(occupiedTarget, ".handoff", "config.yaml"))
		if err != nil {
			t.Fatalf("config.Load occupied: %v", err)
		}
		if occLoaded.Token != "agentd-live" {
			t.Fatalf("occupied token 未被 live token 覆盖，got %q", occLoaded.Token)
		}

		if sent, err := os.ReadFile(sentinelPath); err != nil || string(sent) != "sentinel-sessions-db" {
			t.Fatalf("sessions.db sentinel 被破坏: %q/%v", sent, err)
		}
		if auth, err := os.ReadFile(existingAuth); err != nil || string(auth) != "existing-auth-token" {
			t.Fatalf("已有 auth.json 被覆盖: %q/%v", auth, err)
		}

		entries, err := os.ReadDir(opencodeDir)
		if err != nil {
			t.Fatal(err)
		}
		entryNames := make(map[string]bool)
		for _, e := range entries {
			entryNames[e.Name()] = true
		}
		if len(entries) != 2 || !entryNames["sessions.db"] || !entryNames["auth.json"] {
			t.Fatalf(".local/share/opencode 下出现额外条目: %v", entries)
		}
	})

	t.Run("ResumeAlsoSuppliesAndPassesArgvSession", func(t *testing.T) {
		resumeTarget := t.TempDir()
		resumeSupplier := coordinatorHomeSupplier{
			currentConfig: func() *config.Config { return liveCfg },
			userHomeDir:   func() (string, error) { return mainHome, nil },
			expandHomeDir: func(p string) (string, error) {
				if p == "~/.handoff/home/resume" {
					return resumeTarget, nil
				}
				return hostapi.ExpandHomePath(p)
			},
			credentialPath: toolchain.CredRelPathFor,
		}
		resRunner := coordinatorRunner{h: h, prepareHome: resumeSupplier.Prepare}

		ref := keysclient.SessionRef{
			CLI:       "opencode",
			SessionID: "runner-sess",
			HomeDir:   "~/.handoff/home/resume",
			Workdir:   t.TempDir(),
		}

		_ = os.Remove(capture)

		resResult, err := resRunner.Resume(ref, "resume prompt")
		if err != nil {
			t.Fatalf("resRunner.Resume: %v", err)
		}
		if resResult.SessionID != "runner-sess" {
			t.Fatalf("SessionID=%q, want runner-sess", resResult.SessionID)
		}

		resLoaded, err := config.Load(filepath.Join(resumeTarget, ".handoff", "config.yaml"))
		if err != nil {
			t.Fatalf("Resume 未供给 config: %v", err)
		}
		if resLoaded.Token != "agentd-live" {
			t.Fatalf("Resume token 不对: %q", resLoaded.Token)
		}
		if b, err := os.ReadFile(filepath.Join(resumeTarget, ".config", "opencode", "AGENTS.md")); err != nil || string(b) != "agents-doc" {
			t.Fatalf("Resume 缺少 AGENTS.md: %q/%v", b, err)
		}

		capData, err := os.ReadFile(capture)
		if err != nil {
			t.Fatalf("读 capture: %v", err)
		}
		capLines := strings.Split(strings.TrimSpace(string(capData)), "\n")
		hasResumeArg := false
		hasSessArg := false
		for i, l := range capLines {
			if l == "arg:-s" {
				hasResumeArg = true
				if i+1 < len(capLines) && capLines[i+1] == "arg:runner-sess" {
					hasSessArg = true
				}
			}
		}
		if !hasResumeArg || !hasSessArg {
			t.Fatalf("fake CLI 未收到 -s runner-sess 参数: %v", capLines)
		}
		wantResumeHome := "env:HOME=" + resumeTarget
		hasResumeHome := false
		for _, line := range capLines {
			if strings.HasPrefix(line, "env:HOME=~") {
				t.Fatalf("Resume 把字面 ~ 传给 fake CLI: %v", capLines)
			}
			if line == wantResumeHome {
				hasResumeHome = true
			}
		}
		if !hasResumeHome {
			t.Fatalf("Resume 未收到展开后的 HOME=%q，捕获行: %v", wantResumeHome, capLines)
		}
	})
}

func TestCoordinatorHomeSupplierRejectsSymlinkedTargetHome(t *testing.T) {
	mainHome := t.TempDir()
	targetOutside := t.TempDir()
	targetHome := filepath.Join(t.TempDir(), "target-home")
	if err := os.Symlink(targetOutside, targetHome); err != nil {
		t.Skipf("当前平台不支持 symlink: %v", err)
	}

	supplier := coordinatorHomeSupplier{
		currentConfig: func() *config.Config {
			return &config.Config{Token: "agentd-live", DataDir: t.TempDir(), StallTimeout: time.Hour}
		},
		userHomeDir:   func() (string, error) { return mainHome, nil },
		expandHomeDir: func(string) (string, error) { return targetHome, nil },
	}

	if _, err := supplier.Prepare(keysclient.SessionSpec{CLI: "opencode", HomeDir: "~/target"}); err == nil {
		t.Fatal("targetHome 是 symlink 时 Prepare 必须失败")
	}
	entries, err := os.ReadDir(targetOutside)
	if err != nil {
		t.Fatalf("读取 targetHome symlink 对面: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("targetHome symlink 对面不应被写入: %v", entries)
	}
}

func TestCoordinatorHomeSupplierRejectsSymlinkedConfigParent(t *testing.T) {
	mainHome := t.TempDir()
	sourceRoot := filepath.Join(mainHome, ".config", "opencode")
	if err := os.MkdirAll(sourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "AGENTS.md"), []byte("agents-doc"), 0o644); err != nil {
		t.Fatal(err)
	}

	targetHome := t.TempDir()
	targetOutside := t.TempDir()
	if err := os.Symlink(targetOutside, filepath.Join(targetHome, ".config")); err != nil {
		t.Skipf("当前平台不支持 symlink: %v", err)
	}

	supplier := coordinatorHomeSupplier{
		currentConfig: func() *config.Config {
			return &config.Config{Token: "agentd-live", DataDir: t.TempDir(), StallTimeout: time.Hour}
		},
		userHomeDir:   func() (string, error) { return mainHome, nil },
		expandHomeDir: func(string) (string, error) { return targetHome, nil },
	}

	if _, err := supplier.Prepare(keysclient.SessionSpec{CLI: "opencode", HomeDir: "~/target"}); err == nil {
		t.Fatal("targetHome/.config 是 symlink 时 Prepare 必须失败")
	}
	entries, err := os.ReadDir(targetOutside)
	if err != nil {
		t.Fatalf("读取 .config symlink 对面: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf(".config symlink 对面不应被写入: %v", entries)
	}
}

func TestProjectCoordinatorConfigPreservesStallTimeout(t *testing.T) {
	got, err := projectCoordinatorConfig(&config.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("projectCoordinatorConfig: %v", err)
	}
	if got.StallTimeout != 0 {
		t.Fatalf("projectCoordinatorConfig 改写了 StallTimeout: got %s, want 0", got.StallTimeout)
	}
}

// TestNormalizeCoordinatorSpecEmptyHome 验证协调者 SessionSpec 空 HOME 展开为主 HOME 绝对路径，
// 不再因空值返回「协调者 SessionSpec 缺少 HomeDir」。
func TestNormalizeCoordinatorSpecEmptyHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("无法读取当前用户主目录: %v", err)
	}
	want := filepath.Clean(home)

	for _, homeDir := range []string{"", "   ", "\t"} {
		spec := keysclient.SessionSpec{
			CLI:     "opencode",
			HomeDir: homeDir,
		}
		got, err := normalizeCoordinatorSpec(spec)
		if err != nil {
			t.Fatalf("normalizeCoordinatorSpec(HomeDir=%q) 失败: %v", homeDir, err)
		}
		if got.HomeDir != want {
			t.Fatalf("normalizeCoordinatorSpec(HomeDir=%q) = %q, want %q", homeDir, got.HomeDir, want)
		}
	}
}

// TestCoordinatorSessionRefResolverEmptyHomeOnlineCarrier 锁住已上线载体空 HOME 恢复接缝：
// 当已上线载体登记 HOME 为空时，作为合法主 HOME 展开为绝对路径；小队中没有 online 载体时依然报错。
func TestCoordinatorSessionRefResolverEmptyHomeOnlineCarrier(t *testing.T) {
	env, _ := newCoordEnv(t)
	svc := env.srv.Scheduling()

	putOnlineCarrier(t, svc, scheduling.Carrier{
		Name:       "c1",
		Machine:    "本机",
		CLI:        "opencode",
		HomeDir:    "",
		Credential: scheduling.CredentialStandalone,
		Status:     scheduling.StatusOnline,
	})
	if err := svc.PutSquad(scheduling.Squad{
		Name:    "coord",
		Role:    scheduling.RoleCoordinator,
		Members: []scheduling.SquadMember{{Carrier: "c1", MaxConcurrency: 1}},
	}, 0); err != nil {
		t.Fatalf("登记协调者小队: %v", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("无法读取当前用户主目录: %v", err)
	}
	wantHome := filepath.Clean(home)

	resolver := coordinatorSessionRefResolver{server: env.srv, expandHomeDir: hostapi.ExpandHomePath}

	// 1. 已上线载体 HomeDir 为空：成功恢复为主 HOME 绝对路径
	ref, err := resolver.ResolveSessionRef("card-test", keysclient.SessionRef{})
	if err != nil {
		t.Fatalf("已上线空 HOME 载体 ResolveSessionRef 失败: %v", err)
	}
	if ref.HomeDir != wantHome {
		t.Fatalf("ResolveSessionRef HomeDir = %q, want %q", ref.HomeDir, wantHome)
	}

	// 2. 小队中无 online 载体：失败
	if _, err := svc.ApplyDetect("c1", scheduling.DetectEvidence{Reachable: false}, "offline"); err != nil {
		t.Fatalf("设置载体 c1 offline: %v", err)
	}
	if _, err := resolver.ResolveSessionRef("card-test-offline", keysclient.SessionRef{}); err == nil {
		t.Fatal("小队无 online 载体时 ResolveSessionRef 应失败，但返回成功")
	}
}

