// probe_test.go —— ProbeHome/WakeHome 声明缝的本机文件系统与进程边界测试。
//
// 职责：锁定目标 HOME 展开、只读探测、main_home_sync 供给与有时限无 prompt
// 唤起。测试只使用临时目录和受控 shell CLI，不代表真实 CLI/Windows 真机验收。
// 边界：跨机 HTTP 编排由 agentd 测试覆盖，真实 executor 行为留给协调者真机清单。
package hostapi

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func swapUserHomeDir(t *testing.T, home string) {
	t.Helper()
	old := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = old })
}

func newProbeHost() *Host {
	return NewWithCredentialPathFor(func(name string) (string, bool) {
		if name != "opencode" {
			return "", false
		}
		return ".local/share/opencode/auth.json", true
	})
}

func TestProbeHomeClassifiesFilesystemWithoutWriting(t *testing.T) {
	mainHome := t.TempDir()
	swapUserHomeDir(t, mainHome)
	h := newProbeHost()

	missing := filepath.Join(t.TempDir(), "missing")
	got, err := h.ProbeHome(context.Background(), ProbeRequest{Path: missing, CLI: "opencode"})
	if err != nil || got.Kind != ProbeEmpty {
		t.Fatalf("不存在路径 = %+v/%v，want empty/nil", got, err)
	}
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.Mkdir(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err = h.ProbeHome(context.Background(), ProbeRequest{Path: empty, CLI: "opencode"})
	if err != nil || got.Kind != ProbeEmpty {
		t.Fatalf("空目录 = %+v/%v，want empty/nil", got, err)
	}
	occupied := filepath.Join(t.TempDir(), "occupied")
	if err := os.Mkdir(occupied, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(occupied, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = h.ProbeHome(context.Background(), ProbeRequest{Path: occupied, CLI: "opencode"})
	if err != nil || got.Kind != ProbeOccupied {
		t.Fatalf("非空无凭据 = %+v/%v，want occupied/nil", got, err)
	}
	auth := filepath.Join(occupied, ".local/share/opencode/auth.json")
	if err := os.MkdirAll(filepath.Dir(auth), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(auth, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = h.ProbeHome(context.Background(), ProbeRequest{Path: occupied, CLI: "opencode"})
	if err != nil || got.Kind != ProbeLoggedIn {
		t.Fatalf("命中凭据 = %+v/%v，want logged_in/nil", got, err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatalf("ProbeHome 不得改写已有文件: %q/%v", data, err)
	}
}

func TestProbeHomeMainHomeSyncReadsButDoesNotCopy(t *testing.T) {
	mainHome := t.TempDir()
	swapUserHomeDir(t, mainHome)
	auth := filepath.Join(mainHome, ".local/share/opencode/auth.json")
	if err := os.MkdirAll(filepath.Dir(auth), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(auth, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "carrier-home")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := newProbeHost().ProbeHome(context.Background(), ProbeRequest{
		Path: target, CLI: "opencode", Credential: "main_home_sync",
	})
	if err != nil || got.Kind != ProbeLoggedIn {
		t.Fatalf("主 HOME 同步探测 = %+v/%v，want logged_in/nil", got, err)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("ProbeHome 不得复制文件，目录条目 = %v", entries)
	}
}

func TestProbeHomeExpandsTildeOnTargetHomeAndNoFile判据IsOccupied(t *testing.T) {
	mainHome := t.TempDir()
	swapUserHomeDir(t, mainHome)
	target := filepath.Join(mainHome, ".handoff/home/c1")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "note"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := NewWithCredentialPathFor(func(string) (string, bool) { return "", false })
	got, err := h.ProbeHome(context.Background(), ProbeRequest{Path: "~/.handoff/home/c1", CLI: "claude"})
	if err != nil || got.Kind != ProbeOccupied {
		t.Fatalf("无文件判据/波浪号 = %+v/%v，want occupied/nil", got, err)
	}
}

func replaceWakeCommandContext(t *testing.T, mode, recordPath string) {
	t.Helper()
	old := commandContext
	commandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if filepath.Base(name) != "opencode" {
			t.Errorf("WakeHome 假 CLI 名称 = %q，want opencode", name)
		}
		fakeArgs := []string{"-test.run=^TestWakeHomeFakeProcess$", "--", "--wake-fake=" + mode}
		if recordPath != "" {
			fakeArgs = append(fakeArgs, "--wake-record="+recordPath)
		}
		fakeArgs = append(fakeArgs, args...)
		// 使用测试二进制绝对路径，避免 PATH 上的真实 CLI 介入；runWake 随后
		// 覆盖 cmd.Env，假进程仍可从 HOME 观察到 buildEnv 的最终结果。
		return exec.CommandContext(ctx, os.Args[0], fakeArgs...)
	}
	t.Cleanup(func() { commandContext = old })
}

func TestWakeHomeFakeProcess(t *testing.T) {
	marker := "--wake-fake="
	mode := ""
	recordPath := ""
	var cliArgs []string
	for i, arg := range os.Args {
		if arg != "--" {
			continue
		}
		for _, childArg := range os.Args[i+1:] {
			switch {
			case strings.HasPrefix(childArg, marker):
				mode = strings.TrimPrefix(childArg, marker)
			case strings.HasPrefix(childArg, "--wake-record="):
				recordPath = strings.TrimPrefix(childArg, "--wake-record=")
			default:
				cliArgs = append(cliArgs, childArg)
			}
		}
		break
	}
	if mode == "" {
		return
	}
	if mode == "block" {
		// 保留一个计时器让测试二进制保持运行；纯 select{} 会被 Go
		// runtime 立即判定为死锁，无法验证父进程的 context 终止。
		<-time.After(time.Hour)
	}
	if recordPath != "" {
		payload := strings.Join(append(cliArgs, "HOME="+os.Getenv("HOME")), "\n") + "\n"
		if err := os.WriteFile(recordPath, []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWakeHomeSuppliesMainCredentialBeforeNoPromptCLI(t *testing.T) {
	mainHome := t.TempDir()
	swapUserHomeDir(t, mainHome)
	auth := filepath.Join(mainHome, ".local/share/opencode/auth.json")
	if err := os.MkdirAll(filepath.Dir(auth), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(auth, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	argvFile := filepath.Join(t.TempDir(), "argv")
	replaceWakeCommandContext(t, "record", argvFile)
	target := filepath.Join(t.TempDir(), "carrier-home")
	h := newProbeHost()
	got, err := h.WakeHome(context.Background(), WakeRequest{
		CLI: "opencode", HomeDir: target, Credential: "main_home_sync", Timeout: time.Second,
	})
	if err != nil || got.Outcome != WakeReady {
		t.Fatalf("WakeHome = %+v/%v，want ready/nil", got, err)
	}
	copied, err := os.ReadFile(filepath.Join(target, ".local/share/opencode/auth.json"))
	if err != nil || string(copied) != "token" {
		t.Fatalf("凭据未按原文供给: %q/%v", copied, err)
	}
	if _, err := os.Stat(filepath.Join(target, ".config/skills")); !os.IsNotExist(err) {
		t.Fatalf("供给不得搬技能树，stat err=%v", err)
	}
	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(argv)), "\n")
	if len(lines) < 2 || lines[0] != "--version" || lines[len(lines)-1] != "HOME="+target {
		t.Fatalf("唤起 argv/env = %q，want --version 且 HOME=目标", string(argv))
	}
}

func TestWakeHomeOccupiedNeverOverwrites(t *testing.T) {
	mainHome := t.TempDir()
	swapUserHomeDir(t, mainHome)
	auth := filepath.Join(mainHome, ".local/share/opencode/auth.json")
	if err := os.MkdirAll(filepath.Dir(auth), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(auth, []byte("main-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	replaceWakeCommandContext(t, "record", "")
	target := filepath.Join(t.TempDir(), "carrier-home")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(target, "keep")
	if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newProbeHost().WakeHome(context.Background(), WakeRequest{
		CLI: "opencode", HomeDir: target, Credential: "main_home_sync", Timeout: time.Second,
	}); err != nil {
		t.Fatalf("occupied 唤起不应因供给失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".local/share/opencode/auth.json")); !os.IsNotExist(err) {
		t.Fatalf("occupied 不得复制主凭据，stat err=%v", err)
	}
	data, err := os.ReadFile(keep)
	if err != nil || string(data) != "keep" {
		t.Fatalf("occupied 文件被覆盖: %q/%v", data, err)
	}
}

func TestWakeHomeHonorsTimeoutWithoutRunTurn(t *testing.T) {
	swapUserHomeDir(t, t.TempDir())
	replaceWakeCommandContext(t, "block", "")
	ctx := context.Background()
	started := time.Now()
	_, err := newProbeHost().WakeHome(ctx, WakeRequest{CLI: "opencode", HomeDir: t.TempDir(), Timeout: 40 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "超时") || time.Since(started) > time.Second {
		t.Fatalf("短超时错误/耗时异常: err=%v elapsed=%v", err, time.Since(started))
	}
}

var _ = exec.CommandContext
