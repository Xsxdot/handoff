// dispatch 默认弹终端测试：darwin 下 openTerminal 被调、--no-terminal 抑制、
// 弹窗失败不影响退出码。
package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

// runDispatch 以给定 flags 执行 dispatch（指向 fake agentd），返回 stdout 与错误。
func runDispatch(t *testing.T, extraArgs ...string) (string, error) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tasks" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"task-abc123","state":"running"}`)
	}))
	t.Cleanup(ts.Close)
	addr := strings.TrimPrefix(ts.URL, "http://")
	cfgPath := writeTestConfig(t, "listen: \""+addr+"\"\ntoken: \""+testToken+"\"\n")
	resetFlags(t)
	targetName = ""
	configPath = cfgPath
	agentdURL = "http://127.0.0.1:7777"
	rootCmd.PersistentFlags().Lookup("agentd").Changed = false
	t.Cleanup(func() { dispatchNoTerminal = false })

	args := append([]string{"dispatch", "--repo", t.TempDir(), "--prompt", "x"}, extraArgs...)
	rootCmd.SetArgs(args)
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	var out, errBuf bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errBuf)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	err := Execute()
	_ = errBuf
	return out.String(), err
}

// TestAppleScriptQuoteEscapes 钉死 osascript 命令串的引号转义。
//
// 这是 shellq 删除后 cmd 包唯一还需要的引号能力：do script 的参数是
// AppleScript 字符串字面量，attach 命令里若含空格或引号，不转义会让整条
// do script 语法错误、终端窗口弹不出来。
func TestAppleScriptQuoteEscapes(t *testing.T) {
	cases := []struct{ in, want string }{
		{`handoff attach T1`, `'handoff attach T1'`},
		{`a'b`, `'a'\''b'`},
	}
	for _, c := range cases {
		if got := appleScriptQuote(c.in); got != c.want {
			t.Fatalf("appleScriptQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDispatchOpensTerminalByDefault 验证 darwin 下派发成功后默认弹终端：
// openTerminal 被调且 argv 含 attach 与任务 id。
func TestDispatchOpensTerminalByDefault(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("弹终端仅 darwin")
	}
	var called [][]string
	old := openTerminal
	openTerminal = func(argv []string) error { called = append(called, argv); return nil }
	t.Cleanup(func() { openTerminal = old })

	if _, err := runDispatch(t); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(called) != 1 {
		t.Fatalf("openTerminal 应被调 1 次，得到 %d 次", len(called))
	}
	joined := strings.Join(called[0], " ")
	if !strings.Contains(joined, "attach") || !strings.Contains(joined, "handoff-task-abc") {
		t.Fatalf("attach argv 应含 attach 与任务 id8 会话名: %v", called[0])
	}
}

// TestDispatchNoTerminalFlagSuppresses 验证 --no-terminal 抑制弹窗：
// openTerminal 不被调，stdout 打印提示行。
func TestDispatchNoTerminalFlagSuppresses(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("弹终端仅 darwin")
	}
	called := 0
	old := openTerminal
	openTerminal = func(argv []string) error { called++; return nil }
	t.Cleanup(func() { openTerminal = old })

	out, err := runDispatch(t, "--no-terminal")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if called != 0 {
		t.Fatalf("--no-terminal 时 openTerminal 不应被调")
	}
	if !strings.Contains(out, "handoff attach task-abc123") {
		t.Fatalf("stdout 应含提示行，得到 %q", out)
	}
}

// TestDispatchTerminalFailureDoesNotFailCommand 验证弹窗失败降级：
// openTerminal 返回错误时命令仍退出 0，任务 JSON 正常输出。
func TestDispatchTerminalFailureDoesNotFailCommand(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("弹终端仅 darwin")
	}
	old := openTerminal
	openTerminal = func(argv []string) error { return fmt.Errorf("Terminal 未安装") }
	t.Cleanup(func() { openTerminal = old })

	out, err := runDispatch(t)
	if err != nil {
		t.Fatalf("弹窗失败不应让命令失败，得到 err=%v", err)
	}
	if !strings.Contains(out, `"task-abc123"`) {
		t.Fatalf("任务 JSON 应正常输出，得到 %q", out)
	}
}
