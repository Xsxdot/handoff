package orchestration

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/opencode"
	"github.com/Xsxdot/handoff/internal/hostapi"
	"github.com/Xsxdot/handoff/internal/keysclient"
)

func testLoadRules(mainHome, cli string) ([]executor.ProfileFile, []executor.ProfileFile, error) {
	var rules []executor.ProfileFile
	rulePath := filepath.Join(mainHome, ".config", "opencode", "AGENTS.md")
	if b, err := os.ReadFile(rulePath); err == nil {
		rules = append(rules, executor.ProfileFile{Name: "AGENTS.md", Content: string(b)})
	}
	var skills []executor.ProfileFile
	skillsDir := filepath.Join(mainHome, ".config", "opencode", "skills")
	_ = filepath.Walk(skillsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(skillsDir, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		skills = append(skills, executor.ProfileFile{Name: rel, Content: string(b)})
		return nil
	})
	return rules, skills, nil
}

// TestCoordinatorHomeSupplyOnLaunchAndResume 锁协调者隔离 HOME 的供给行为：
// Launch/Resume 前按白名单写入 config.yaml（活配置投影）、AGENTS.md、skills；B464 起
// 不再供给缺失凭据（S8）。B233.26 归域后本包直驱 supplier.Prepare 断言供给结果；runner 把
// prepareHome 返回值作为子进程 HOME 传递的集成断言留 gateway（coordrunner_test.go）。
func TestCoordinatorHomeSupplyOnLaunchAndResume(t *testing.T) {
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
	mainAuth := filepath.Join(mainHome, ".local", "share", "opencode", "auth.json")
	if err := os.WriteFile(mainAuth, []byte("main-auth-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	mainAuthMtime := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(mainAuth, mainAuthMtime, mainAuthMtime); err != nil {
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
		currentConfig: func() *config.Config { return liveCfg },
		userHomeDir:   func() (string, error) { return mainHome, nil },
		expandHomeDir: expandHome,
		profileFor: func(cli string) (executor.Profile, error) {
			return opencode.New(nil).Profile(), nil
		},
		loadRules: testLoadRules,
	}

	spec := keysclient.SessionSpec{
		CLI:     "opencode",
		HomeDir: "~/.handoff/home/muse",
		Workdir: t.TempDir(),
	}

	prepared, err := supplier.Prepare(spec)
	if err != nil {
		t.Fatalf("supplier.Prepare: %v", err)
	}
	if prepared != targetDir {
		t.Fatalf("Prepare 返回 %q, want 展开后的隔离 HOME %q", prepared, targetDir)
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
	// B464 S8：供给绝不复制凭据——载体 HOME 里不出现主 HOME 的 auth.json，主 HOME 凭据 mtime 不变。
	if _, err := os.Stat(filepath.Join(targetDir, ".local", "share", "opencode", "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("协调者供给不得复制凭据，stat err=%v", err)
	}
	if info, err := os.Stat(mainAuth); err != nil || !info.ModTime().Equal(mainAuthMtime) {
		t.Fatalf("主 HOME 凭据 mtime 变了：%v/%v", info, err)
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
			profileFor: func(cli string) (executor.Profile, error) {
				return opencode.New(nil).Profile(), nil
			},
			loadRules: testLoadRules,
		}
		occSpec := keysclient.SessionSpec{
			CLI:     "opencode",
			HomeDir: "~/.handoff/home/occupied",
			Workdir: t.TempDir(),
		}
		if _, err := occupiedSupplier.Prepare(occSpec); err != nil {
			t.Fatalf("occupiedSupplier.Prepare: %v", err)
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
			profileFor: func(cli string) (executor.Profile, error) {
				return opencode.New(nil).Profile(), nil
			},
			loadRules: testLoadRules,
		}
		resSpec := keysclient.SessionSpec{
			CLI:     "opencode",
			HomeDir: "~/.handoff/home/resume",
			Workdir: t.TempDir(),
		}

		if _, err := resumeSupplier.Prepare(resSpec); err != nil {
			t.Fatalf("resumeSupplier.Prepare: %v", err)
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
	})
}

// snapshotHomeFiles 记录 HOME 下普通文件的正文与 mtime，用于证明「零写入」。
func snapshotHomeFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[rel] = string(data) + "|" + info.ModTime().UTC().Format(time.RFC3339Nano)
		return nil
	})
	if err != nil {
		t.Fatalf("快照 %s: %v", root, err)
	}
	return out
}

// TestCoordinatorHomeMainHomeSkipsAllWrites 锁 B464 S8/Q9：供给目标展开后就是主 HOME
// （含经软链的同路径）时整段跳过——config.yaml 投影、provider 配置投影、规则/skills
// 全不写，只做展开与校验；主 HOME 的活配置与规则文件字节与 mtime 都不变。
func TestCoordinatorHomeMainHomeSkipsAllWrites(t *testing.T) {
	mainHome := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(mainHome, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-time.Hour).Truncate(time.Second)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(".handoff", "config.yaml"), "token: agentd-live\n")
	write(filepath.Join(".config", "opencode", "AGENTS.md"), "agents-doc")
	write(filepath.Join(".config", "opencode", "skills", "plan", "SKILL.md"), "skill-plan")
	write(filepath.Join(".config", "opencode", "opencode.jsonc"), `{"provider":{"x":{}}}`)

	// 目标是主 HOME 的软链：判据必须 EvalSymlinks 后比较，不能按字面路径。
	linkParent := t.TempDir()
	linkedMain := filepath.Join(linkParent, "main-home-link")
	if err := os.Symlink(mainHome, linkedMain); err != nil {
		t.Skipf("当前平台不支持 symlink: %v", err)
	}

	liveCfg := &config.Config{Token: "agentd-live", DataDir: t.TempDir(), StallTimeout: 2 * time.Hour}
	supplier := coordinatorHomeSupplier{
		currentConfig: func() *config.Config { return liveCfg },
		userHomeDir:   func() (string, error) { return mainHome, nil },
		expandHomeDir: func(string) (string, error) { return linkedMain, nil },
		profileFor: func(cli string) (executor.Profile, error) {
			t.Errorf("目标就是主 HOME 时不得调用 Profile.Prepare（cli=%s）", cli)
			return opencode.New(nil).Profile(), nil
		},
		loadRules: func(string, string) ([]executor.ProfileFile, []executor.ProfileFile, error) {
			t.Error("目标就是主 HOME 时不得装载规则/skills")
			return nil, nil, nil
		},
	}

	before := snapshotHomeFiles(t, mainHome)
	prepared, err := supplier.Prepare(keysclient.SessionSpec{CLI: "opencode", HomeDir: "~", Workdir: t.TempDir()})
	if err != nil {
		t.Fatalf("目标 = 主 HOME 时 Prepare 应成功跳过写入: %v", err)
	}
	if prepared != linkedMain {
		t.Fatalf("Prepare 返回 %q, want %q", prepared, linkedMain)
	}
	after := snapshotHomeFiles(t, mainHome)
	if len(after) != len(before) {
		t.Fatalf("主 HOME 文件集合变了：before=%d after=%d", len(before), len(after))
	}
	for rel, want := range before {
		if got, ok := after[rel]; !ok || got != want {
			t.Fatalf("主 HOME 文件 %s 被写入或改写：before=%q after=%q", rel, want, got)
		}
	}
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
		got, err := NormalizeCoordinatorSpec(spec)
		if err != nil {
			t.Fatalf("NormalizeCoordinatorSpec(HomeDir=%q) 失败: %v", homeDir, err)
		}
		if got.HomeDir != want {
			t.Fatalf("NormalizeCoordinatorSpec(HomeDir=%q) = %q, want %q", homeDir, got.HomeDir, want)
		}
	}
}
