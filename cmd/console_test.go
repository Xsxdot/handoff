// handoff console 测试：--print-url 的输出契约（桌面壳靠它接线）与 agentd 未运行时的报错。
package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
