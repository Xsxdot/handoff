// runturn_test.go —— RunTurn 缝级测试。缝 = target.json 已声明的
// d_gateway→d_execution 方向上的 hostapi.Host.RunTurn 门面（消费方：组装点
// coordinatorRunner）。各支测试入口全部落在该缝上，无内部锁。
//
// 假 CLI 桩沿 PATH 注入（仓内先例 internal/executor/claudecode/timing_test.go
// #installPersistentFakeClaude）；桩回放的事件形状对齐上游实测抓取
// （b156.3.1-plan §三 F1/F2），不是凭记忆编的。
package hostapi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// installFakeCLI 把受控 shell 脚本伪装成载体 CLI 放进 PATH 最前。脚本名取
// "opencode"：请求值必须是门禁名单内的 CLI（岔口一裁决 A），PATH 前插保证
// 真正执行的是本桩而非真二进制。桩行为：
// argv 逐行记入 $FAKECLI_ARGV_FILE（断言证据），随后追加一行 env:HOME=…
// （隔离 HOME 断言证据）；$FAKECLI_SLEEP 非空时先睡够秒数（超时用例）；
// 最后按 run --format json 形态回放三行 JSONL（step_start/text/step_finish），
// 会话 id 取 `-s` 的值，缺省 ses_fake_new。
func installFakeCLI(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	script := `#!/bin/sh
: "${FAKECLI_ARGV_FILE:?FAKECLI_ARGV_FILE 必须设置}"
for a in "$@"; do printf '%s\n' "$a" >>"$FAKECLI_ARGV_FILE"; done
printf 'env:HOME=%s\n' "$HOME" >>"$FAKECLI_ARGV_FILE"
if [ -n "$FAKECLI_SLEEP" ]; then sleep "$FAKECLI_SLEEP"; fi
SID=""; prev=""
for a in "$@"; do
  if [ "$prev" = "-s" ]; then SID="$a"; fi
  prev="$a"
done
if [ -z "$SID" ]; then SID="ses_fake_new"; fi
printf '%s\n' "{\"type\":\"step_start\",\"sessionID\":\"$SID\",\"part\":{\"type\":\"step-start\"}}"
printf '%s\n' "{\"type\":\"text\",\"sessionID\":\"$SID\",\"part\":{\"type\":\"text\",\"text\":\"ok-$SID\"}}"
printf '%s\n' "{\"type\":\"step_finish\",\"sessionID\":\"$SID\",\"part\":{\"type\":\"step-finish\",\"reason\":\"stop\"}}"
`
	fake := filepath.Join(binDir, "opencode")
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// withArgvCapture 准备桩的证据文件并把路径交给桩环境。
func withArgvCapture(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "argv.txt")
	t.Setenv("FAKECLI_ARGV_FILE", p)
	return p
}

// readLines 读证据文件为非空行切片。
func readLines(t *testing.T, p string) []string {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读桩证据 %s: %v", p, err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// readArgvLines 返回桩捕获的 argv 逐元素切片（剔除桩自加的 env:HOME 证据行）。
// 位置防线的数据源：shebang 脚本的 $0 恒为脚本自身路径、与 cmd.Args[0] 无关，
// 多余的前导元素只会落在 $@ 第一格——所以「逐元素等值」是唯一能看见它的断言形状。
func readArgvLines(t *testing.T, p string) []string {
	t.Helper()
	var out []string
	for _, l := range readLines(t, p) {
		if strings.HasPrefix(l, "env:") {
			continue
		}
		out = append(out, l)
	}
	return out
}

// A1 缝级红绿：新建会话必须返回非空 SessionID 与回合文本，且隔离 HOME
// 以 req.HomeDir 为准注入子进程环境。
func TestRunTurnNewSessionReturnsSessionID(t *testing.T) {
	installFakeCLI(t)
	capture := withArgvCapture(t)
	home := t.TempDir()
	h := New()
	reply, err := h.RunTurn(context.Background(), TurnRequest{
		CLI: "opencode", HomeDir: home, Workdir: t.TempDir(), Prompt: "开场简报",
	})
	if err != nil {
		t.Fatalf("新建回合失败: %v", err)
	}
	if reply.SessionID == "" {
		t.Fatalf("新建会话 SessionID 为空")
	}
	if reply.SessionID != "ses_fake_new" {
		t.Fatalf("SessionID=%q, want ses_fake_new", reply.SessionID)
	}
	if !strings.Contains(reply.Output, "ok-ses_fake_new") {
		t.Fatalf("Output=%q 缺回合文本", reply.Output)
	}
	wantHome := "env:HOME=" + home
	lines := readLines(t, capture)
	found := false
	for _, l := range lines {
		if l == wantHome {
			found = true
		}
	}
	if !found {
		t.Fatalf("子进程 HOME 未注入为 %s，证据: %v", wantHome, lines)
	}
	// argv 位置防线（2026-08-26 审阅轮）：包含式断言看不见前导元素——
	// argv 组装回归（多出一个 bin）时 $@ 第一格会变成 binpath 而非 "run"，
	// 只有逐元素等值断言拦得住。整切片比较，位置与内容同时锁死。
	wantArgv := []string{"run", "--format", "json", "--", "开场简报"}
	if got := readArgvLines(t, capture); !reflect.DeepEqual(got, wantArgv) {
		t.Fatalf("argv 逐元素不等（首元素必须恰为 \"run\"，位置防线）:\n got=%q\nwant=%q", got, wantArgv)
	}
}

// A2 缝级红绿：带 SessionID 续接必须走 resume 分支——argv 逐元素等值于
// run 形态完整参数表（`-s ses_known` 在其法定位置），回显会话 id 原样透传。
// 判据形状说明（2026-08-26 审阅轮更正）：「argv 出现 `-s ses_known`」这类
// 包含式断言不看位置，多一个前导元素照绿——必须整切片 DeepEqual。
func TestRunTurnResumePassesSessionID(t *testing.T) {
	installFakeCLI(t)
	capture := withArgvCapture(t)
	h := New()
	reply, err := h.RunTurn(context.Background(), TurnRequest{
		CLI: "opencode", SessionID: "ses_known", Prompt: "唤醒简报",
	})
	if err != nil {
		t.Fatalf("续接回合失败: %v", err)
	}
	if reply.SessionID != "ses_known" {
		t.Fatalf("SessionID=%q, want ses_known", reply.SessionID)
	}
	wantArgv := []string{"run", "--format", "json", "-s", "ses_known", "--", "唤醒简报"}
	if got := readArgvLines(t, capture); !reflect.DeepEqual(got, wantArgv) {
		t.Fatalf("argv 逐元素不等（首元素必须恰为 \"run\"，位置防线）:\n got=%q\nwant=%q", got, wantArgv)
	}
}

// A3 缝级红绿：Timeout 到点判失败且不挂死——错误标明超时，总耗时远小于
// 桩睡眠时长（真进程真超时，不做环境探针式 skip）。
func TestRunTurnTimeoutFailsPromptly(t *testing.T) {
	installFakeCLI(t)
	withArgvCapture(t)
	h := New()
	begin := time.Now()
	_, err := h.RunTurn(context.Background(), TurnRequest{
		CLI: "opencode", Prompt: "慢回合",
		Env:     []string{"FAKECLI_SLEEP=30"},
		Timeout: 300 * time.Millisecond,
	})
	if err == nil {
		t.Fatalf("超时回合应失败")
	}
	if !strings.Contains(err.Error(), "超时") {
		t.Fatalf("错误应标明超时: %v", err)
	}
	if el := time.Since(begin); el > 10*time.Second {
		t.Fatalf("超时未及时终止进程树，耗时 %v", el)
	}
}

// A4 反面断言（岔口一裁决 A）：未实装 CLI 的错误必须含该 CLI 名**且含
// 「未实装」标记词**。后者防一种变异假绿：删掉名单门禁后 LookPath 对
// "claude" 的失败信息同样含名字，只查名字的门禁形同虚设。
func TestRunTurnUnsupportedCLINamesTheCLI(t *testing.T) {
	installFakeCLI(t)
	h := New()
	for _, cli := range []string{"claude", "grok"} {
		_, err := h.RunTurn(context.Background(), TurnRequest{CLI: cli, Prompt: "x"})
		if err == nil {
			t.Fatalf("CLI %q 应报未实装错误", cli)
		}
		if !strings.Contains(err.Error(), cli) {
			t.Fatalf("错误应含 CLI 名 %q: %v", cli, err)
		}
		if !strings.Contains(err.Error(), "未实装") {
			t.Fatalf("错误应含「未实装」标记词（防 LookPath 假绿）: %v", err)
		}
	}
}

// A5 后半边缝级红绿：req.Env 携带同名 HOME 行时 HomeDir 字段必须赢——
// 隔离 HOME 是物种边界的环境执法（spec §4.3），env 清单无权偷换。
func TestRunTurnHomeDirWinsOverEnvLine(t *testing.T) {
	installFakeCLI(t)
	capture := withArgvCapture(t)
	home := t.TempDir()
	h := New()
	if _, err := h.RunTurn(context.Background(), TurnRequest{
		CLI: "opencode", HomeDir: home, Workdir: t.TempDir(), Prompt: "开场简报",
		Env: []string{"HOME=/env-side-home"},
	}); err != nil {
		t.Fatalf("回合失败: %v", err)
	}
	lines := readLines(t, capture)
	found := false
	for _, l := range lines {
		switch l {
		case "env:HOME=" + home:
			found = true
		case "env:HOME=/env-side-home":
			t.Fatalf("req.Env 的 HOME 行漏进子进程，HomeDir 字段未赢: %v", lines)
		}
	}
	if !found {
		t.Fatalf("子进程 HOME 未注入为 %s: %v", home, lines)
	}
}

// A7 缝级断言：空 prompt 的新建回合必须响报（keysclient.Runner.Launch 注释
// 承诺「prompt 必须非空、实现方对空 prompt 必须响报」的执法半边；run 形态
// 无「无消息建会话」，plan §三 F5）。带 SessionID 的续接不受此守卫限制。
func TestRunTurnEmptyPromptRejected(t *testing.T) {
	installFakeCLI(t)
	withArgvCapture(t)
	h := New()
	_, err := h.RunTurn(context.Background(), TurnRequest{CLI: "opencode"})
	if err == nil {
		t.Fatalf("空 prompt 新建回合应响报")
	}
	if !strings.Contains(err.Error(), "prompt") {
		t.Fatalf("错误应指明 prompt 问题: %v", err)
	}
}

// TestBuildEnvExpandsTildeHomeDir 是 2026-09-04 字面量 ~/ 事故的回归锁：
// TurnRequest.HomeDir 含 ~ 时，子进程 HOME 必须是目标机展开后的绝对路径，
// 不得透传字面量（否则子进程相对 cwd 建出 ./~/.handoff/... 垃圾目录）。
func TestBuildEnvExpandsTildeHomeDir(t *testing.T) {
	fakeHome := t.TempDir()
	swapUserHomeDir(t, fakeHome)
	env, expandedHome, err := buildEnv(TurnRequest{HomeDir: "~/.handoff/home/c1"})
	if err != nil {
		t.Fatalf("buildEnv: %v", err)
	}
	wantHome := filepath.Join(fakeHome, ".handoff/home/c1")
	if expandedHome != wantHome {
		t.Fatalf("expandedHome = %q, want %q", expandedHome, wantHome)
	}
	want := "HOME=" + wantHome
	found := false
	for _, kv := range env {
		if kv == "HOME=~/.handoff/home/c1" {
			t.Fatalf("HOME 字面量 ~ 被透传给子进程: %v", env)
		}
		if kv == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("展开后的 HOME 缺席，want %q, got %v", want, env)
	}
}

func TestRunTurnExpandsTildeHomeDirAtSeam(t *testing.T) {
	installFakeCLI(t)
	capture := withArgvCapture(t)
	fakeHome := t.TempDir()
	swapUserHomeDir(t, fakeHome)
	workdir := t.TempDir()

	_, err := New().RunTurn(context.Background(), TurnRequest{
		CLI: "opencode", HomeDir: "~/handoff-home-x", Workdir: workdir, Prompt: "x",
	})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	lines := readLines(t, capture)
	want := "env:HOME=" + filepath.Join(fakeHome, "handoff-home-x")
	found := false
	for _, line := range lines {
		if line == "env:HOME=~/handoff-home-x" {
			t.Fatalf("字面 HOME 进入子进程: %v", lines)
		}
		if line == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺展开后的 HOME=%q: %v", want, lines)
	}
	entries, err := os.ReadDir(workdir)
	if err != nil {
		t.Fatalf("读 workdir: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() == "~" {
			t.Fatalf("workdir 下出现字面 ~ 目录")
		}
	}
}

func TestRunTurnHomeExpansionFailureDoesNotLaunch(t *testing.T) {
	installFakeCLI(t)
	capture := withArgvCapture(t)
	previous := userHomeDir
	userHomeDir = func() (string, error) { return "", errors.New("home unavailable") }
	t.Cleanup(func() { userHomeDir = previous })
	workdir := t.TempDir()

	_, err := New().RunTurn(context.Background(), TurnRequest{
		CLI: "opencode", HomeDir: "~/handoff-home-x", Workdir: workdir, Prompt: "x",
	})
	if err == nil || !strings.Contains(err.Error(), "展开目标 HOME") ||
		!strings.Contains(err.Error(), "~/handoff-home-x") {
		t.Fatalf("展开失败必须带原串，err=%v", err)
	}
	if raw, readErr := os.ReadFile(capture); readErr == nil && len(raw) != 0 {
		t.Fatalf("展开失败后不应启动 fake CLI，证据=%q", raw)
	} else if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		t.Fatalf("读 fake CLI 证据: %v", readErr)
	}
	entries, readErr := os.ReadDir(workdir)
	if readErr != nil {
		t.Fatalf("读 workdir: %v", readErr)
	}
	for _, entry := range entries {
		if entry.Name() == "~" {
			t.Fatalf("失败路径仍创建了字面 ~ 目录")
		}
	}
}
