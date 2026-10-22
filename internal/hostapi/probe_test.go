// probe_test.go —— WakeHome 声明缝的本机文件系统与进程边界测试。
//
// 职责：锁定目标 HOME 展开、检测永不复制凭据（B464），以及 WakeHome 经
// RunTurn 发 DetectPrompt（B295）。测试只使用临时目录和受控假 CLI，
// 不代表真实 CLI/Windows 真机验收。
// 边界：跨机 HTTP 编排由 agentd 测试覆盖，真实回合留给协调者真机清单。
package hostapi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testWakeBudget 是递交给 WakeHome 的探测预算。
//
// 用例断言的是**判定结果**（ready / need_login / …），不是延迟；假 CLI 秒回，
// 1s 预算在「全量 go test ./...」的并行负载下会翻车（2026-09-19 实测：
// TestWakeHomeReadyRequiresTurnOutputNotCredFile 全量跑红、单跑绿）。放到 10s：
// 只有真挂死才会走到超时分支。
// 断言超时语义的用例（TestWakeHomeHonorsTimeoutViaRunTurn）仍用自己的短预算，不受影响。
const testWakeBudget = 10 * time.Second

func swapUserHomeDir(t *testing.T, home string) {
	t.Helper()
	old := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = old })
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

// TestWakeHomeNeverCopiesCredential 锁 B464 V15 的检测半边：检测在载体（登录）HOME 原样跑，
// 前后主 HOME 与载体 HOME 的凭据文件集合与 mtime 不变；载体 HOME 不存在时不 mkdir 造空 HOME，
// 直接报需要登录并点名路径（不把「登录已失效」伪装成别的）。
func TestWakeHomeNeverCopiesCredential(t *testing.T) {
	mainHome := t.TempDir()
	swapUserHomeDir(t, mainHome)
	writeCred := func(home, content string) string {
		t.Helper()
		p := filepath.Join(home, ".local/share/opencode/auth.json")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-time.Hour)
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
		return p
	}
	mainCred := writeCred(mainHome, "main-token")
	installFakeCLI(t)
	capture := withArgvCapture(t)

	missing := filepath.Join(t.TempDir(), "gone-home")
	got, err := New().WakeHome(context.Background(), WakeRequest{CLI: "opencode", HomeDir: missing, Timeout: testWakeBudget})
	if err != nil || got.Outcome != WakeNeedLogin || !strings.Contains(got.Detail, missing) {
		t.Fatalf("HOME 不存在应报需要登录并点名路径：%+v/%v", got, err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("检测不得创建载体 HOME，stat err=%v", err)
	}

	login := t.TempDir()
	loginCred := writeCred(login, "login-token")
	before := snapshotTree(t, mainHome, login)
	got, err = New().WakeHome(context.Background(), WakeRequest{CLI: "opencode", HomeDir: login, Timeout: testWakeBudget})
	if err != nil || got.Outcome != WakeReady {
		t.Fatalf("WakeHome = %+v/%v，want ready/nil", got, err)
	}
	if after := snapshotTree(t, mainHome, login); after[mainCred] != before[mainCred] || after[loginCred] != before[loginCred] {
		t.Fatalf("检测前后凭据 mtime 变了：before=%v after=%v", before, after)
	}
	for path := range snapshotTree(t, login) {
		if strings.HasSuffix(path, "auth.json") && path != loginCred {
			t.Fatalf("检测在登录 HOME 里多出凭据文件 %s", path)
		}
	}
	foundHome := false
	for _, l := range readLines(t, capture) {
		if l == "env:HOME="+login {
			foundHome = true
		}
	}
	if !foundHome {
		t.Fatalf("检测回合 HOME 应为登录 HOME，got %v", readLines(t, capture))
	}
}

// snapshotTree 记录若干根下全部普通文件的 mtime（凭据不再复制的文件集合 + mtime 判据）。
func snapshotTree(t *testing.T, roots ...string) map[string]time.Time {
	t.Helper()
	out := map[string]time.Time{}
	for _, root := range roots {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err == nil && info.Mode().IsRegular() && !strings.Contains(path, "argv") {
				out[path] = info.ModTime()
			}
			return nil
		})
	}
	return out
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
	if _, err := New().WakeHome(context.Background(), WakeRequest{
		CLI: "opencode", HomeDir: target, Timeout: testWakeBudget,
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
	got, err := New().WakeHome(context.Background(), WakeRequest{
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

func isolateEmptyPATH(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

func rejectDetectTurn(t *testing.T) {
	t.Helper()
	old := detectTurn
	detectTurn = func(_ *Host, _ context.Context, req TurnRequest) (TurnReply, error) {
		t.Errorf("名单外 CLI 不得走检测回合 cli=%s", req.CLI)
		return TurnReply{}, errors.New("detectTurn should not run for unsupported CLI")
	}
	t.Cleanup(func() { detectTurn = old })
}

func TestWakeHomeUnsupportedCLIWritesUnreachableNotReady(t *testing.T) {
	swapUserHomeDir(t, t.TempDir())
	isolateEmptyPATH(t)
	rejectDetectTurn(t)
	got, err := New().WakeHome(context.Background(), WakeRequest{
		CLI: "grok", HomeDir: t.TempDir(), Timeout: testWakeBudget,
	})
	if err != nil {
		t.Fatalf("找不到命令应映射为 WakeReply 而非 error: %v", err)
	}
	if got.Outcome != WakeUnreachable {
		t.Fatalf("找不到命令 Outcome = %q，want unreachable", got.Outcome)
	}
	if !strings.Contains(got.Detail, "找不到") {
		t.Fatalf("Detail 应含找不到: %q", got.Detail)
	}
	if strings.Contains(got.Detail, "未实装") {
		t.Fatalf("找不到命令不得再报未实装: %q", got.Detail)
	}
}

func TestWakeHomeUnsupportedCLIPathReadySkipsTurn(t *testing.T) {
	swapUserHomeDir(t, t.TempDir())
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "grok"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	rejectDetectTurn(t)
	got, err := New().WakeHome(context.Background(), WakeRequest{
		CLI: "grok", HomeDir: t.TempDir(), Timeout: testWakeBudget,
	})
	if err != nil || got.Outcome != WakeReady {
		t.Fatalf("PATH 命中 = %+v/%v，want ready/nil", got, err)
	}
}

func TestWakeHomeUnsupportedCLIFallbackDirReadySkipsTurn(t *testing.T) {
	home := t.TempDir()
	swapUserHomeDir(t, home)
	isolateEmptyPATH(t)
	dir := filepath.Join(home, ".grok", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "grok"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rejectDetectTurn(t)
	got, err := New().WakeHome(context.Background(), WakeRequest{
		CLI: "grok", HomeDir: t.TempDir(), Timeout: testWakeBudget,
	})
	if err != nil || got.Outcome != WakeReady {
		t.Fatalf("安装位命中 = %+v/%v，want ready/nil", got, err)
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
	got, err := New().WakeHome(context.Background(), WakeRequest{
		CLI: "opencode", HomeDir: target, Timeout: testWakeBudget,
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
	if err := os.MkdirAll(target, 0o700); err != nil { // B464：检测不再 mkdir 载体 HOME
		t.Fatal(err)
	}
	var captured TurnRequest
	old := detectTurn
	detectTurn = func(h *Host, ctx context.Context, req TurnRequest) (TurnReply, error) {
		captured = req
		return old(h, ctx, req)
	}
	t.Cleanup(func() { detectTurn = old })

	got, err := New().WakeHome(context.Background(), WakeRequest{
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
