// handoff console 测试：--print-url 的输出契约（桌面壳靠它接线）与 agentd 未运行时的报错。
package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
	"rsc.io/qr"
)

// TestConsolePrintURLOutputContract 钉死断言 16 与桌面壳的接线契约：
// stdout 恰好是一行可用 URL，没有任何其他噪音——壳会直接把它交给 loadURL。
func TestConsolePrintURLOutputContract(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/tickets" {
			t.Errorf("非预期路径: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer 测试令牌" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		// 用 r.Host 拼，和真 agentd 的 consoleURL 一致
		json.NewEncoder(w).Encode(map[string]any{
			"url":        "http://" + r.Host + "/console?ticket=abc",
			"expires_at": "2026-08-11T00:00:00Z",
		})
	}))
	defer ts.Close()

	var stdout bytes.Buffer
	runSubcommandForTest(t, &stdout, ts.URL, "测试令牌", []string{"console", "--print-url"})

	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout 行数 = %d，期望恰好 1 行（壳直接消费这一行）: %q", len(lines), stdout.String())
	}
	if !strings.HasPrefix(lines[0], "http") || !strings.Contains(lines[0], "/console?ticket=") {
		t.Fatalf("stdout 不是可用 URL: %q", lines[0])
	}
}

// TestConsoleAgentdNotRunning 钉死断言 18：agentd 未运行时明确报错，不退化成超时。
func TestConsoleAgentdNotRunning(t *testing.T) {
	// 先起一个 httptest 再立刻关掉，拿到一个确定没人监听的地址
	ts := httptest.NewServer(http.NotFoundHandler())
	dead := ts.URL
	ts.Close()

	var stdout bytes.Buffer
	err := runSubcommandForTest(t, &stdout, dead, "测试令牌", []string{"console", "--print-url"})
	if err == nil {
		t.Fatal("agentd 未运行时应报错")
	}
	if !strings.Contains(err.Error(), "连接 agentd") {
		t.Fatalf("报错文案未点明连不上 agentd: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("失败时 stdout 应为空，实际: %q", stdout.String())
	}
}

// TestConsoleRejectsPositionalArgs 钉死：console 不接受位置参数——多余参数说明用法
// 错误，静默忽略会让拼错的命令「看似成功」，尤其桌面壳依赖 stdout 恰好一行契约，
// 多喂一个参数被吞掉会直接破坏那条契约。
//
// 与 TestUsagePrintedOnlyForArgErrors 的约定一致：参数/flag 错误会打印 usage（根因
// 就是用法），stderr 保留 cobra 的 "Error:" 行——断言的是「报错且报了用法」，而非
// 参数错误后的任何输出都不该有。
func TestConsoleRejectsPositionalArgs(t *testing.T) {
	// 配置不可达即可：Args 校验发生在 RunE 之前，多余参数会在触达任何网络调用前被拒
	var stdout bytes.Buffer
	err := runSubcommandForTest(t, &stdout, "http://127.0.0.1:1", "测试令牌",
		[]string{"console", "--print-url", "多余的参数"})
	if err == nil {
		t.Fatal("多余位置参数应报错，实际为 nil")
	}
	if !strings.Contains(err.Error(), "unknown command") && !strings.Contains(err.Error(), "accepts 0 arg") {
		t.Errorf("报错应点明不接受位置参数，实际: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage:") {
		t.Errorf("参数错误应打印 usage 段（根因就是用法）, got %q", stdout.String())
	}
}

// runSubcommandForTest 以给定 flags 执行一个子命令，把 stdout 写进 stdout 参数。
//
// args 的**第一个元素是子命令名**（如 "console"），其余是传给它的 flag/参数。
// addr 是 fake agentd 的 http:// 地址；token 写进临时配置，让 TargetEndpoint 取到它。
func runSubcommandForTest(t *testing.T, stdout *bytes.Buffer, addr, token string, args []string) error {
	t.Helper()
	cfgPath := writeTestConfig(t,
		"listen: \""+strings.TrimPrefix(addr, "http://")+"\"\ntoken: \""+token+"\"\n")
	resetFlags(t)
	targetName = ""
	configPath = cfgPath
	// 与 runDispatch 一致：清掉 --agentd 的 Changed 标记，让地址取自配置的 listen
	rootCmd.PersistentFlags().Lookup("agentd").Changed = false
	// resetFlags 不覆盖 console 的包级 flag，跨用例会残留（尤其 --print-url 会让
	// 本应失败的用例看似通过）；每次运行显式复位
	consolePrintURL, consoleDevice, consoleNoOpen = false, "", false

	rootCmd.SetArgs(args)
	rootCmd.SetOut(stdout)
	var errBuf bytes.Buffer
	rootCmd.SetErr(&errBuf)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	return Execute()
}

// --- B369.5 配对载体测试 ---

// pairBundleFakeAgentd 起一个假 agentd：POST /api/auth/tickets 回一张指向自身的 ticket。
func pairBundleFakeAgentd(t *testing.T, token string) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/tickets" {
			t.Errorf("非预期路径: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"url":        "http://" + r.Host + "/console?ticket=abc",
			"expires_at": "2026-09-14T00:01:00Z",
		})
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

// runConsoleForTest 以 args 执行 console，捕获 stdout/stderr（只经 rootCmd.SetOut，
// 绝不调 consoleCmd.SetOut——cobra 的 outWriter 一旦设置会跨用例残留）。
func runConsoleForTest(t *testing.T, cfgPath string, args ...string) (string, string, error) {
	t.Helper()
	resetFlags(t)
	targetName = ""
	configPath = cfgPath
	rootCmd.PersistentFlags().Lookup("agentd").Changed = false
	consoleQR, consoleBundle, consolePrintURL, consoleNoOpen = false, false, false, false
	rootCmd.SetArgs(args)
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	err := Execute()
	return stdout.String(), stderr.String(), err
}

// TestConsoleQRBundleRoundTrip 钉死 S5 主缝：console --bundle 对多 target 配置产出的
// stdout（恰好一行 JSON）必须能被 proto.DecodePairBundle 解回，含全部可达机器（每机
// token/形态/ticket）。
func TestConsoleQRBundleRoundTrip(t *testing.T) {
	ts1 := pairBundleFakeAgentd(t, "token-aaa")
	ts2 := pairBundleFakeAgentd(t, "token-bbb")
	cfgPath := writeTestConfig(t, "listen: \"127.0.0.1:1\"\ntoken: \"local-tok\"\n"+
		"targets:\n  devbox:\n    addr: \""+strings.TrimPrefix(ts1, "http://")+"\"\n    token: \"token-aaa\"\n"+
		"  nas:\n    addr: \""+strings.TrimPrefix(ts2, "http://")+"\"\n    token: \"token-bbb\"\n")
	stdout, _, err := runConsoleForTest(t, cfgPath, "console", "--bundle")
	if err != nil {
		t.Fatalf("console --bundle: %v", err)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("--bundle stdout 应恰好一行载荷，实得 %d：%q", len(lines), stdout)
	}
	b, err := proto.DecodePairBundle(lines[0])
	if err != nil {
		t.Fatalf("CLI 产出的载荷必须能被 proto.DecodePairBundle 解回: %v\nraw=%s", err, lines[0])
	}
	if len(b.Machines) != 2 {
		t.Fatalf("应含全部可达机器 2 台: %+v", b.Machines)
	}
	byName := map[string]proto.PairMachine{}
	for _, m := range b.Machines {
		byName[m.Name] = m
		if m.Ticket == nil {
			t.Fatalf("可达机 %q 必须带 ticket", m.Name)
		}
	}
	if byName["devbox"].Token != "token-aaa" || byName["devbox"].Addr == "" {
		t.Fatalf("devbox 登记不对: %+v", byName["devbox"])
	}
	if byName["nas"].Token != "token-bbb" || byName["nas"].Addr == "" {
		t.Fatalf("nas 登记不对: %+v", byName["nas"])
	}
}

// TestConsoleBundlePartialOnOfflineTarget 离线机（连不上）只标为无 ticket，不整单失败。
func TestConsoleBundlePartialOnOfflineTarget(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	deadAddr := strings.TrimPrefix(dead.URL, "http://")
	dead.Close()
	live := pairBundleFakeAgentd(t, "token-live")
	cfgPath := writeTestConfig(t, "listen: \"127.0.0.1:1\"\ntoken: \"local-tok\"\n"+
		"targets:\n  live:\n    addr: \""+strings.TrimPrefix(live, "http://")+"\"\n    token: \"token-live\"\n"+
		"  offline:\n    addr: \""+deadAddr+"\"\n    token: \"token-off\"\n")
	stdout, _, err := runConsoleForTest(t, cfgPath, "console", "--bundle")
	if err != nil {
		t.Fatalf("离线机不整单失败: %v", err)
	}
	b, err := proto.DecodePairBundle(strings.TrimSpace(stdout))
	if err != nil {
		t.Fatalf("部分 bundle 应可解回: %v (stdout=%q)", err, stdout)
	}
	byName := map[string]proto.PairMachine{}
	for _, m := range b.Machines {
		byName[m.Name] = m
	}
	if byName["live"].Ticket == nil {
		t.Fatal("在线机必须带 ticket")
	}
	if byName["offline"].Ticket != nil {
		t.Fatal("离线机必须无 ticket（部分 bundle）")
	}
}

// TestConsoleBundleNoTargetsErrors 无任何 target 时必须明确报错，不产出空 bundle。
func TestConsoleBundleNoTargetsErrors(t *testing.T) {
	cfgPath := writeTestConfig(t, "listen: \"127.0.0.1:1\"\ntoken: \"local-tok\"\n")
	stdout, _, err := runConsoleForTest(t, cfgPath, "console", "--bundle")
	if err == nil {
		t.Fatal("无 target 应报错")
	}
	if !strings.Contains(err.Error(), "没有任何 target") {
		t.Fatalf("报错应点明无 target: %v", err)
	}
	if stdout != "" {
		t.Errorf("失败时 stdout 应为空: %q", stdout)
	}
}

// TestConsoleOutputModesMutuallyExclusive 输出模式互斥：--qr 与 --bundle 是同一载荷
// 的两种编码（cobra flag 互斥）；--qr/--bundle 与 --print-url/--no-open 是配对面与
// 桌面壳单 URL 面（RunE 前置拒绝）。三种组合都必须报错且不触达网络。
func TestConsoleOutputModesMutuallyExclusive(t *testing.T) {
	// 每例一个子测试：flag 的 Changed 标记跨 Execute 累积，只有子测试的
	// t.Cleanup（resetFlags 注册）跑完才会清，故不能在同一测试体里循环 Execute。
	//
	// 夹具必须带一个**可达 target**：否则 RunE 前置拒绝缺失时，runConsolePair 仍会
	// 因「无 target」报错，两条 RunE 互斥子例就成了假绿（变异验证实测：去掉 RunE
	// 守卫后无 target 夹具下本测试仍绿）。带可达 target 后，守卫缺失会真产出载荷。
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"qr 与 bundle 互斥", []string{"console", "--qr", "--bundle"}},
		{"qr 与 print-url 互斥", []string{"console", "--qr", "--print-url"}},
		{"bundle 与 no-open 互斥", []string{"console", "--bundle", "--no-open"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := pairBundleFakeAgentd(t, "token-mut")
			cfgPath := writeTestConfig(t, "listen: \"127.0.0.1:1\"\ntoken: \"local-tok\"\n"+
				"targets:\n  dev:\n    addr: \""+strings.TrimPrefix(ts, "http://")+"\"\n    token: \"token-mut\"\n")
			stdout, _, err := runConsoleForTest(t, cfgPath, tc.args...)
			if err == nil {
				t.Fatalf("%v 同给应报错", tc.args)
			}
			if stdout != "" {
				t.Fatalf("%v 同给不得产出载荷: %q", tc.args, stdout)
			}
		})
	}
	// --print-url 与 --no-open 是历史同义词，不得互斥（既有调用方两种都用）。
	t.Run("print-url 与 no-open 同义不互斥", func(t *testing.T) {
		ts := pairBundleFakeAgentd(t, "测试令牌")
		cfgPath := writeTestConfig(t, "listen: \""+strings.TrimPrefix(ts, "http://")+"\"\ntoken: \"测试令牌\"\n")
		stdout, _, err := runConsoleForTest(t, cfgPath, "console", "--print-url", "--no-open")
		if err != nil {
			t.Fatalf("--print-url 与 --no-open 同义，不得报错: %v", err)
		}
		if strings.Count(strings.TrimRight(stdout, "\n"), "\n") != 0 {
			t.Fatalf("同义词组合仍须恰好一行 URL: %q", stdout)
		}
	})
}

// TestConsoleQRArtifactIsTerminalQR --qr 的 stdout 必须是可渲染的二维码文本。
func TestConsoleQRArtifactIsTerminalQR(t *testing.T) {
	live := pairBundleFakeAgentd(t, "token-qr")
	cfgPath := writeTestConfig(t, "listen: \"127.0.0.1:1\"\ntoken: \"local-tok\"\n"+
		"targets:\n  devbox:\n    addr: \""+strings.TrimPrefix(live, "http://")+"\"\n    token: \"token-qr\"\n")
	stdout, _, err := runConsoleForTest(t, cfgPath, "console", "--qr")
	if err != nil {
		t.Fatalf("console --qr: %v", err)
	}
	if !strings.ContainsAny(stdout, "█▀▄") {
		t.Fatalf("--qr 输出应是二维码半块字符画:\n%s", stdout)
	}
	if strings.Count(stdout, "\n") < 10 {
		t.Fatalf("二维码应有多行: %q", stdout)
	}
}

// TestConsolePrintURLStillOneLine 不破坏既有契约：--print-url 仍恰好一行 URL。
func TestConsolePrintURLStillOneLine(t *testing.T) {
	ts := pairBundleFakeAgentd(t, "测试令牌")
	var stdout bytes.Buffer
	runSubcommandForTest(t, &stdout, ts, "测试令牌", []string{"console", "--print-url"})
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "http") {
		t.Fatalf("--print-url 仍须恰好一行 URL: %q", stdout.String())
	}
}

// TestConsoleProductionDoesNotLogToken 源码级守卫：console.go 不得把 token 写进日志。
func TestConsoleProductionDoesNotLogToken(t *testing.T) {
	body, err := os.ReadFile("console.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, bad := range []string{"\"token\", m.Token", "\"token\", t.Token", "\"token\", cfg.Token"} {
		if strings.Contains(text, bad) {
			t.Fatalf("console.go 不得记录 token: %q", bad)
		}
	}
}

// TestConsoleBundleCapacityBudget 实测 N 机 bundle 字节数（拆解 §3.6 ③ 容量预算；
// contract §9 附区条 3：PairMachineBudgetBytes=200 是声明值，实测上界写入断言）。
//
// 用真实 ticket 长度（64 hex）而非短桩：否则测出的字节数偏小，预算失真。
// 单码容量按 rsc.io/qr v0.2.0 实测：L 级 2953 字节、M 级 2331 字节、Q 级 1663、
// H 级 1273（byte 模式二分实测）。渲染固定用 L 级（renderBundleQR），故上界以 L 为准。
func TestConsoleBundleCapacityBudget(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"url":        "http://" + r.Host + "/console?ticket=" + strings.Repeat("c", 64),
			"expires_at": "2026-09-14T00:01:00Z",
		})
	}))
	t.Cleanup(ts.Close)
	addr := strings.TrimPrefix(ts.URL, "http://")
	var b strings.Builder
	b.WriteString("listen: \"127.0.0.1:1\"\ntoken: \"local-tok\"\ntargets:\n")
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&b, "  machine-%02d:\n    addr: \"%s\"\n    token: \"token-%02d\"\n", i, fmt.Sprintf("%s", addr), i)
	}
	cfgPath := writeTestConfig(t, b.String())
	stdout, _, err := runConsoleForTest(t, cfgPath, "console", "--bundle")
	if err != nil {
		t.Fatalf("console --bundle: %v", err)
	}
	payload := strings.TrimSpace(stdout)
	// 实测字节数写入断言（改 wire 形状/字段名会当场红，提醒重估 N 上界）。
	t.Logf("实测 8 机直连 bundle 字节数=%d（avg=%.1f/机）", len(payload), float64(len(payload))/8)
	// 声明值 PairMachineBudgetBytes=200 是 contract 的声明预算（不含 ticket URL 的
	// 保险上限），实测每机 ~238B 已超声明值——本条只把**实测**上界钉住：8 机 <= 2600
	// 字节，仍在 L 级单码容量（2953）内；超界退路 = --bundle 粘贴串（contract §9 附区条 3）。
	if len(payload) > 2600 {
		t.Fatalf("8 机 bundle 超实测上界 2600：%d", len(payload))
	}
	// 单码可编码（L 级）：容量上界的机器判据
	if _, err := qr.Encode(payload, qr.L); err != nil {
		t.Fatalf("8 机 bundle 应能在 L 级单码内编码: %v (len=%d)", err, len(payload))
	}
}

// TestConsoleQROverflowSuggestsBundle 超过单码容量的多机 bundle 经 --qr 必须
// 给出可行动错误（指向 --bundle 粘贴串退路），不是静默产出残缺二维码。
// 用 16 台共享同一个 fake agentd 的 target（名字不同即不同机器登记），
// bundle >3.5KB > L 级单码 2953 字节（rsc.io/qr v0.2.0 实测）。
func TestConsoleQROverflowSuggestsBundle(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"url":        "http://" + r.Host + "/console?ticket=" + strings.Repeat("c", 64),
			"expires_at": "2026-09-14T00:01:00Z",
		})
	}))
	t.Cleanup(ts.Close)
	addr := strings.TrimPrefix(ts.URL, "http://")
	var b strings.Builder
	b.WriteString("listen: \"127.0.0.1:1\"\ntoken: \"local-tok\"\ntargets:\n")
	for i := 0; i < 16; i++ {
		fmt.Fprintf(&b, "  machine-%02d:\n    addr: \"%s\"\n    token: \"token-%02d\"\n", i, addr, i)
	}
	cfgPath := writeTestConfig(t, b.String())
	_, _, err := runConsoleForTest(t, cfgPath, "console", "--qr")
	if err == nil {
		t.Fatal("16 机 bundle 超单码容量，--qr 应报错")
	}
	if !strings.Contains(err.Error(), "--bundle") {
		t.Fatalf("错误应指向 --bundle 粘贴串退路: %v", err)
	}
}
