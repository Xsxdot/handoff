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
	runConsoleForTest(t, &stdout, ts.URL, "测试令牌", []string{"--print-url"})

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
	err := runConsoleForTest(t, &stdout, dead, "测试令牌", []string{"--print-url"})
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

// runConsoleForTest 以给定 flags 执行 console 子命令，把 stdout 写进 stdout 参数。
//
// addr 是 fake agentd 的 http:// 地址；token 写进临时配置，让 TargetEndpoint 取到它。
func runConsoleForTest(t *testing.T, stdout *bytes.Buffer, addr, token string, extraArgs []string) error {
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

	rootCmd.SetArgs(append([]string{"console"}, extraArgs...))
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
