package agentd

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/opencode"
	"github.com/Xsxdot/handoff/internal/hostapi"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/toolchain"
)

// TestResumeTurnRequestCarriesIsolatedHome 锁 B299：续接 TurnRequest 必须带
// 隔离 HOME/Workdir/Model，不能只剩 CLI+SessionID。
func TestResumeTurnRequestCarriesIsolatedHome(t *testing.T) {
	ref := keysclient.SessionRef{
		CLI: "opencode", SessionID: "ses_x",
		HomeDir: "/home/coord", Workdir: "/repo", Model: "fast",
	}
	req := resumeTurnRequest(ref, "ping")
	if req.CLI != ref.CLI || req.SessionID != ref.SessionID || req.Prompt != "ping" {
		t.Fatalf("身份/prompt 映射错误: %+v", req)
	}
	if req.HomeDir != ref.HomeDir || req.Workdir != ref.Workdir || req.Model != ref.Model {
		t.Fatalf("续接丢了隔离环境: %+v", req)
	}
	if len(req.Env) != 2 || req.Env[0] != "HANDOFF_SESSION_CLI="+ref.CLI ||
		req.Env[1] != "HANDOFF_SESSION_ID="+ref.SessionID {
		t.Fatalf("续接缺少当前会话出示环境: %+v", req.Env)
	}
}

// installCoordinatorFakeCLI 往 PATH 装一个记录 argv 与 HOME 的假 opencode，返回捕获文件路径。
// （原随 coordinator_home_test.go 定义；B233.26 供给族归域编排包后，runner 侧消费面留下。）
func installCoordinatorFakeCLI(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	capture := filepath.Join(t.TempDir(), "coordinator-cli.txt")
	script := `#!/bin/sh
: "${COORD_CAPTURE:?COORD_CAPTURE 必须设置}"
for arg in "$@"; do printf 'arg:%s\n' "$arg" >>"$COORD_CAPTURE"; done
printf 'env:HOME=%s\n' "$HOME" >>"$COORD_CAPTURE"
printf 'env:HANDOFF_SESSION_CLI=%s\n' "${HANDOFF_SESSION_CLI-<unset>}" >>"$COORD_CAPTURE"
printf 'env:HANDOFF_SESSION_ID=%s\n' "${HANDOFF_SESSION_ID-<unset>}" >>"$COORD_CAPTURE"
printf '%s\n' '{"type":"step_start","sessionID":"runner-sess","part":{"type":"step-start"}}'
printf '%s\n' '{"type":"text","sessionID":"runner-sess","part":{"type":"text","text":"runner-ok"}}'
printf '%s\n' '{"type":"step_finish","sessionID":"runner-sess","part":{"type":"step-finish","reason":"stop"}}'
`
	fakeCLI := filepath.Join(binDir, "opencode")
	if err := os.WriteFile(fakeCLI, []byte(script), 0o755); err != nil {
		t.Fatalf("写 coordinator fake CLI: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("COORD_CAPTURE", capture)
	return capture
}

// testCoordRunner 构造挂在假 CLI 上的协调者承载（走导出构造 NewCoordinatorRunner）。
func testCoordRunner(h *hostapi.Host, prepareHome func(keysclient.SessionSpec) (string, error)) keysclient.Runner {
	ocRep, _ := executor.BaselineReport(executor.HarnessOpenCode)
	coordReg := executor.NewRegistry(executor.StaticProvider{HarnessName: executor.HarnessOpenCode, Rep: ocRep})
	coord := opencode.NewCoordinator(h, slog.Default())
	return NewCoordinatorRunner(coord, coordReg, prepareHome)
}

// TestCoordinatorRunnerPassesPreparedHomeToSubprocess 锁 runner 与 prepareHome 缝的
// 集成行为（原 TestCoordinatorHomeSupplyOnLaunchAndResume 的 runner 断言段；B233.26
// 供给族归域编排包后，其包内测试拿不到 gateway 未导出 runner、本包拿不到编排未导出
// supplier，故按缝切分：供给内容断言随 supplier 归编排包，本组只锁「prepareHome 的
// 返回值成为子进程 HOME，且字面 ~ 绝不泄漏」与 Resume 的 -s argv 传递）。
func TestCoordinatorRunnerPassesPreparedHomeToSubprocess(t *testing.T) {
	capture := installCoordinatorFakeCLI(t)
	targetDir := t.TempDir()
	prepareHome := func(spec keysclient.SessionSpec) (string, error) {
		if strings.HasPrefix(spec.HomeDir, "~") {
			t.Errorf("prepareHome 收到未展开的字面 HOME %q", spec.HomeDir)
		}
		return targetDir, nil
	}
	h := hostapi.NewWithCredentialPathFor(toolchain.CredRelPathFor)
	runner := testCoordRunner(h, prepareHome)

	res, err := runner.Launch(keysclient.SessionSpec{
		CLI: "opencode", HomeDir: targetDir, Workdir: t.TempDir(),
	}, "测试启动")
	if err != nil {
		t.Fatalf("runner.Launch: %v", err)
	}
	if res.SessionID != "runner-sess" {
		t.Fatalf("SessionID=%q, want runner-sess", res.SessionID)
	}

	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("读 capture: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
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

	ref := keysclient.SessionRef{
		CLI: "opencode", SessionID: "runner-sess",
		HomeDir: targetDir, Workdir: t.TempDir(),
	}
	_ = os.Remove(capture)
	resResult, err := runner.Resume(ref, "resume prompt")
	if err != nil {
		t.Fatalf("runner.Resume: %v", err)
	}
	if resResult.SessionID != "runner-sess" {
		t.Fatalf("Resume SessionID=%q, want runner-sess", resResult.SessionID)
	}
	capData, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("读 capture: %v", err)
	}
	capLines := strings.Split(strings.TrimSpace(string(capData)), "\n")
	hasResumeArg, hasSessArg := false, false
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
	wantResumeHome := "env:HOME=" + targetDir
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
}

// b435UnsetSeatEnv 清掉测试进程自身可能带的 HANDOFF 席位键（宿主会话环境），
// 让「子进程读到什么」只取决于 runner 注入，结束后还原。
func b435UnsetSeatEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"HANDOFF_SESSION_CLI", "HANDOFF_SESSION_ID"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}

// b435CapturedEnv 读假 CLI 捕获文件里 env:<key>= 行的值；缺行 → ok=false。
func b435CapturedEnv(t *testing.T, capture, key string) (string, bool) {
	t.Helper()
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("读 capture: %v", err)
	}
	prefix := "env:" + key + "="
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix), true
		}
	}
	return "", false
}

// TestB435ResumeTurnInjectsSeatEnv 是 F-RM1 的机内复现（R plan 段 0a / Q2 甲）：经导出构造
// NewCoordinatorRunner + 真 opencode.Coordinator + PATH 假 opencode 跑一次真实 Resume，
// 子进程必须读到与卡席位身份一致的 HANDOFF_SESSION_CLI/ID 两键（链路：coordinatorRunner
// → executor.CoordSessionRef.Env → opencode.Coordinator → hostapi.buildEnv → 子进程）。
// 同一回合顺带锁隔离 HOME 透传（原 TestResumeTurnRequestCarriesIsolatedHome 的意图，改测真路径）。
func TestB435ResumeTurnInjectsSeatEnv(t *testing.T) {
	b435UnsetSeatEnv(t)
	capture := installCoordinatorFakeCLI(t)
	home := t.TempDir()
	runner := testCoordRunner(hostapi.NewWithCredentialPathFor(toolchain.CredRelPathFor),
		func(keysclient.SessionSpec) (string, error) { return home, nil })
	if _, err := runner.Resume(keysclient.SessionRef{
		CLI: "opencode", SessionID: "ses_x", HomeDir: home, Workdir: t.TempDir(),
	}, "resume prompt"); err != nil {
		t.Fatalf("runner.Resume: %v", err)
	}
	for _, kv := range [][2]string{{"HANDOFF_SESSION_CLI", "opencode"}, {"HANDOFF_SESSION_ID", "ses_x"}} {
		got, ok := b435CapturedEnv(t, capture, kv[0])
		if !ok || got != kv[1] {
			t.Fatalf("resume 回合子进程 %s=%q（捕获行存在=%v），want %q", kv[0], got, ok, kv[1])
		}
	}
	if got, _ := b435CapturedEnv(t, capture, "HOME"); got != home {
		t.Fatalf("resume 回合子进程 HOME=%q，want %q（隔离 HOME 透传）", got, home)
	}
}

// TestB435LaunchTurnHasNoPartialSeatEnv 锁 Q3 甲：launch 回合会话 id 尚未生成、席位回合后
// 才落，两键都不注入——子进程里两键都缺（半注入会把协调者全部出示席位的命令打成硬错误）。
func TestB435LaunchTurnHasNoPartialSeatEnv(t *testing.T) {
	b435UnsetSeatEnv(t)
	capture := installCoordinatorFakeCLI(t)
	home := t.TempDir()
	runner := testCoordRunner(hostapi.NewWithCredentialPathFor(toolchain.CredRelPathFor),
		func(keysclient.SessionSpec) (string, error) { return home, nil })
	if _, err := runner.Launch(keysclient.SessionSpec{
		CLI: "opencode", HomeDir: home, Workdir: t.TempDir(),
	}, "launch prompt"); err != nil {
		t.Fatalf("runner.Launch: %v", err)
	}
	for _, key := range []string{"HANDOFF_SESSION_CLI", "HANDOFF_SESSION_ID"} {
		if got, _ := b435CapturedEnv(t, capture, key); got != "<unset>" {
			t.Fatalf("launch 回合子进程 %s=%q，want <unset>（两键都不注入）", key, got)
		}
	}
}
