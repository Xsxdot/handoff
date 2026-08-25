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
	"os"
	"path/filepath"
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
}

// A2 缝级红绿：带 SessionID 续接必须走 resume 分支——argv 出现 `-s <id>`
// 且回显会话 id 原样透传。
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
	lines := readLines(t, capture)
	found := false
	for i, l := range lines {
		if l == "-s" && i+1 < len(lines) && lines[i+1] == "ses_known" {
			found = true
		}
	}
	if !found {
		t.Fatalf("argv 未出现 -s ses_known（resume 分支缺失）: %v", lines)
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
