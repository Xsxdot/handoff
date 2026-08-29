// probe_test.go —— ProbeHome/WakeHome 声明缝的本机文件系统与进程边界测试。
//
// 职责：锁定目标 HOME 展开、只读探测、main_home_sync 供给，以及 WakeHome
// 经 RunTurn 发 DetectPrompt（B295）。测试只使用临时目录和受控假 CLI，
// 不代表真实 CLI/Windows 真机验收。
// 边界：跨机 HTTP 编排由 agentd 测试覆盖，真实回合留给协调者真机清单。
package hostapi

import (
	"context"
	"os"
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

func installFakeCLIFail(t *testing.T, stderr string) {
	t.Helper()
	binDir := t.TempDir()
	script := "#!/bin/sh\necho '" + stderr + "' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(binDir, "opencode"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestWakeHomeSuppliesMainCredentialBeforeTurn(t *testing.T) {
	mainHome := t.TempDir()
	swapUserHomeDir(t, mainHome)
	auth := filepath.Join(mainHome, ".local/share/opencode/auth.json")
	if err := os.MkdirAll(filepath.Dir(auth), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(auth, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	installFakeCLI(t)
	capture := withArgvCapture(t)
	target := filepath.Join(t.TempDir(), "carrier-home")
	got, err := newProbeHost().WakeHome(context.Background(), WakeRequest{
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
	joined := strings.Join(readArgvLines(t, capture), "\n")
	if strings.Contains(joined, "--version") {
		t.Fatalf("检测不得跑 --version: %q", joined)
	}
	if !strings.Contains(joined, "run") || !strings.Contains(joined, "--format") ||
		!strings.Contains(joined, "json") || !strings.Contains(joined, "ping") {
		t.Fatalf("检测 argv 应走 RunTurn ping: %q", joined)
	}
	foundHome := false
	for _, l := range readLines(t, capture) {
		if l == "env:HOME="+target {
			foundHome = true
		}
	}
	if !foundHome {
		t.Fatalf("检测回合 HOME 应为目标目录，got %v", readLines(t, capture))
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
	installFakeCLI(t)
	withArgvCapture(t)
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

func TestWakeHomeHonorsTimeoutViaRunTurn(t *testing.T) {
	swapUserHomeDir(t, t.TempDir())
	installFakeCLI(t)
	withArgvCapture(t)
	t.Setenv("FAKECLI_SLEEP", "30")
	started := time.Now()
	got, err := newProbeHost().WakeHome(context.Background(), WakeRequest{
		CLI: "opencode", HomeDir: t.TempDir(), Timeout: 300 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("超时应映射为 WakeReply 而非 error: %v", err)
	}
	if got.Outcome != WakeUnreachable {
		t.Fatalf("超时 Outcome = %q，want unreachable", got.Outcome)
	}
	if time.Since(started) > 10*time.Second {
		t.Fatalf("超时未及时终止，耗时 %v", time.Since(started))
	}
}

func TestWakeHomeUnsupportedCLIWritesUnreachableNotReady(t *testing.T) {
	swapUserHomeDir(t, t.TempDir())
	got, err := newProbeHost().WakeHome(context.Background(), WakeRequest{
		CLI: "grok", HomeDir: t.TempDir(), Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("未实装应映射为 WakeReply 而非 error: %v", err)
	}
	if got.Outcome != WakeUnreachable {
		t.Fatalf("未实装 Outcome = %q，want unreachable", got.Outcome)
	}
	if !strings.Contains(got.Detail, "未实装") {
		t.Fatalf("Detail 应含未实装: %q", got.Detail)
	}
}

func TestWakeHomeReadyRequiresTurnOutputNotCredFile(t *testing.T) {
	swapUserHomeDir(t, t.TempDir())
	installFakeCLIFail(t, "unauthorized")
	target := t.TempDir()
	auth := filepath.Join(target, ".local/share/opencode/auth.json")
	if err := os.MkdirAll(filepath.Dir(auth), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(auth, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := newProbeHost().WakeHome(context.Background(), WakeRequest{
		CLI: "opencode", HomeDir: target, Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("鉴权失败应映射为 WakeReply 而非 error: %v", err)
	}
	if got.Outcome != WakeNeedLogin {
		t.Fatalf("有凭据文件但回合失败 Outcome = %q，want need_login", got.Outcome)
	}
}

func TestWakeHomeZeroTimeoutUsesDetectDefaultAndWorkdirIsHome(t *testing.T) {
	swapUserHomeDir(t, t.TempDir())
	installFakeCLI(t)
	withArgvCapture(t)
	target := filepath.Join(t.TempDir(), "carrier-home")
	var captured TurnRequest
	old := detectTurn
	detectTurn = func(h *Host, ctx context.Context, req TurnRequest) (TurnReply, error) {
		captured = req
		return old(h, ctx, req)
	}
	t.Cleanup(func() { detectTurn = old })

	got, err := newProbeHost().WakeHome(context.Background(), WakeRequest{
		CLI: "opencode", HomeDir: target, Timeout: 0,
	})
	if err != nil || got.Outcome != WakeReady {
		t.Fatalf("WakeHome = %+v/%v，want ready/nil", got, err)
	}
	if captured.Timeout != DefaultDetectTimeout {
		t.Fatalf("Timeout=0 传给 RunTurn 的上界 = %s，want %s", captured.Timeout, DefaultDetectTimeout)
	}
	if captured.Timeout == DefaultTurnTimeout {
		t.Fatal("检测超时不得落入 DefaultTurnTimeout")
	}
	if captured.Workdir != target || captured.HomeDir != target {
		t.Fatalf("Workdir/HomeDir = %q/%q，want 都是隔离 HOME %q", captured.Workdir, captured.HomeDir, target)
	}
	if captured.Prompt != DetectPrompt {
		t.Fatalf("Prompt = %q，want %q", captured.Prompt, DetectPrompt)
	}
}
