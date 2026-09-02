// TargetEndpoint 的本机/远程端点换算测试：覆盖无 --target 时用本地配置 token
// 认证（服务端无条件 Bearer，无 token 的本机调用必然 401）、--agentd 显式优先、
// token 缺失报错、--target 的 targets 表解析与未定义报错。
package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Xsxdot/handoff/internal/agentd"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

const testToken = "test-token"

// writeTestConfig 写一份测试配置文件并返回路径。
func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("写测试配置: %v", err)
	}
	return p
}

// resetFlags 在用例结束时把**整棵命令树**的 flag 复位，保证用例之间互不污染。
//
// 为什么是整棵树而不是列举几个变量（B132）：cobra 的 flag 值绑定在包级变量上
// （如 cmd/dispatch.go 顶部那 13 个 dispatchXxx），一次 rootCmd.Execute 把值写进去
// 之后就一直留着。本函数原先只还原 agentdURL/targetName/configPath 三个，于是
// 跑过 `dispatch --project proj1` 的用例会把 dispatchProject 泄漏给后续所有用例——
// 表现为整包 `go test ./cmd/ -count=2` 稳定失败（第二遍开局就带着上一遍的残留），
// 而 -count=1 与单跑该用例都正常，极难归因。
//
// 按名字列举治不住这个：新增一个 flag 就要记得回来加一行，而**忘了不会报错**，
// 只会在几个月后变成一条偶发的红。所以改成按构造复位——遍历 rootCmd 及其全部
// 子命令，把每个 flag 设回 DefValue 并清 Changed。本包无 slice 类型 flag，
// Set(DefValue) 是幂等替换而非追加；将来若引入 slice flag，这里要另行处理。
//
// 顺序上先整树复位、再还原调用方保存的三个值：调用方是直接改变量（不走 flag
// 解析）来构造场景的，它们期望的是「我改之前的样子」，而不是 flag 默认值。
func resetFlags(t *testing.T) {
	t.Helper()
	oldAgentd, oldTarget, oldConfig := agentdURL, targetName, configPath
	t.Cleanup(func() {
		resetCommandTree(rootCmd)
		agentdURL, targetName, configPath = oldAgentd, oldTarget, oldConfig
	})
}

// resetCommandTree 把 cmd 及其全部子命令的 flag 值设回 DefValue 并清 Changed。
//
// 边界：只动 flag 状态，不碰命令的 args/输出流（那些由各用例自己的 t.Cleanup
// 还原）。Set 的错误被忽略——DefValue 是 pflag 自己生成的合法字面量，设不回去
// 说明 flag 定义本身有问题，不该由测试 helper 兜底。
func resetCommandTree(c *cobra.Command) {
	reset := func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	}
	c.Flags().VisitAll(reset)
	c.PersistentFlags().VisitAll(reset)
	for _, sub := range c.Commands() {
		resetCommandTree(sub)
	}
}

// TestTargetEndpointLocalAuth 覆盖本机模式（无 --target）：
// token 必须来自本地配置，地址取配置 Listen（未显式 --agentd）或显式 --agentd。
func TestTargetEndpointLocalAuth(t *testing.T) {
	cfgPath := writeTestConfig(t, `listen: "127.0.0.1:9999"
token: "local-tok"
targets:
  devbox:
    addr: "10.0.0.1:7777"
    token: "remote-tok"
`)
	resetFlags(t)
	targetName = ""
	configPath = cfgPath
	agentdURL = "http://127.0.0.1:7777"

	t.Run("无 target 用本地配置 token 与 listen", func(t *testing.T) {
		rootCmd.PersistentFlags().Lookup("agentd").Changed = false
		addr, token, err := TargetEndpoint()
		if err != nil {
			t.Fatalf("TargetEndpoint: %v", err)
		}
		if addr != "http://127.0.0.1:9999" {
			t.Fatalf("addr=%q, want http://127.0.0.1:9999（配置 Listen 补 scheme）", addr)
		}
		if token != "local-tok" {
			t.Fatalf("token=%q, want local-tok（本机认证必须带配置 token）", token)
		}
	})

	t.Run("显式 --agentd 优先于配置 listen", func(t *testing.T) {
		agentdURL = "http://192.168.1.10:7777"
		if err := rootCmd.PersistentFlags().Set("agentd", agentdURL); err != nil {
			t.Fatalf("Set agentd flag: %v", err)
		}
		addr, token, err := TargetEndpoint()
		if err != nil {
			t.Fatalf("TargetEndpoint: %v", err)
		}
		if addr != "http://192.168.1.10:7777" {
			t.Fatalf("addr=%q, want 显式 --agentd 优先", addr)
		}
		if token != "local-tok" {
			t.Fatalf("token=%q, want local-tok", token)
		}
	})

	t.Run("配置 token 为空返回错误", func(t *testing.T) {
		emptyCfg := writeTestConfig(t, "listen: \"127.0.0.1:7777\"\ntoken: \"\"\n")
		configPath = emptyCfg
		_, _, err := TargetEndpoint()
		if err == nil || !strings.Contains(err.Error(), "token") {
			t.Fatalf("token 为空应报错, got %v", err)
		}
	})
}

// TestTargetEndpointLocalRewrite 覆盖 B85 的确定性改写：本机模式下 host 非
// loopback（单网卡 IP / 通配）一律改拨 127.0.0.1 同端口；显式 --agentd 不改写。
func TestTargetEndpointLocalRewrite(t *testing.T) {
	resetFlags(t)
	targetName = ""

	t.Run("单网卡 IP 改拨 loopback", func(t *testing.T) {
		configPath = writeTestConfig(t, "listen: \"100.64.0.5:9999\"\ntoken: \"tok\"\n")
		rootCmd.PersistentFlags().Lookup("agentd").Changed = false
		addr, _, err := TargetEndpoint()
		if err != nil {
			t.Fatalf("TargetEndpoint: %v", err)
		}
		if addr != "http://127.0.0.1:9999" {
			t.Fatalf("addr=%q, want http://127.0.0.1:9999（单网卡档靠辅助监听兜底）", addr)
		}
	})

	t.Run("通配也改拨 loopback", func(t *testing.T) {
		configPath = writeTestConfig(t, "listen: \"0.0.0.0:9999\"\ntoken: \"tok\"\n")
		rootCmd.PersistentFlags().Lookup("agentd").Changed = false
		addr, _, err := TargetEndpoint()
		if err != nil {
			t.Fatalf("TargetEndpoint: %v", err)
		}
		if addr != "http://127.0.0.1:9999" {
			t.Fatalf("addr=%q, want http://127.0.0.1:9999（拨 0.0.0.0 能通只是协议栈宽容）", addr)
		}
	})

	t.Run("显式 --agentd 不改写", func(t *testing.T) {
		configPath = writeTestConfig(t, "listen: \"100.64.0.5:9999\"\ntoken: \"tok\"\n")
		if err := rootCmd.PersistentFlags().Set("agentd", "http://100.64.0.5:9999"); err != nil {
			t.Fatalf("Set agentd flag: %v", err)
		}
		addr, _, err := TargetEndpoint()
		if err != nil {
			t.Fatalf("TargetEndpoint: %v", err)
		}
		if addr != "http://100.64.0.5:9999" {
			t.Fatalf("addr=%q, 显式 --agentd 指明了端点就该照拨", addr)
		}
	})

	t.Run("Endpoints 本机行同样改写", func(t *testing.T) {
		configPath = writeTestConfig(t, "listen: \"100.64.0.5:9999\"\ntoken: \"tok\"\n")
		rootCmd.PersistentFlags().Lookup("agentd").Changed = false
		eps, err := Endpoints("")
		if err != nil {
			t.Fatalf("Endpoints: %v", err)
		}
		if eps[0].Addr != "http://127.0.0.1:9999" {
			t.Fatalf("本机行 addr=%q, want http://127.0.0.1:9999（与 TargetEndpoint 同口径）", eps[0].Addr)
		}
	})
}

// TestTargetEndpointRemote 覆盖远程模式（--target）：
// 从配置 Targets 表换算 addr/token；未定义的 target 报错。
func TestTargetEndpointRemote(t *testing.T) {
	cfgPath := writeTestConfig(t, `listen: "127.0.0.1:9999"
token: "local-tok"
targets:
  devbox:
    addr: "10.0.0.1:7777"
    token: "remote-tok"
`)
	resetFlags(t)
	configPath = cfgPath

	t.Run("target 已定义换算成功", func(t *testing.T) {
		targetName = "devbox"
		addr, token, err := TargetEndpoint()
		if err != nil {
			t.Fatalf("TargetEndpoint: %v", err)
		}
		if addr != "http://10.0.0.1:7777" {
			t.Fatalf("addr=%q, want http://10.0.0.1:7777", addr)
		}
		if token != "remote-tok" {
			t.Fatalf("token=%q, want remote-tok", token)
		}
	})

	t.Run("target 未定义报错", func(t *testing.T) {
		targetName = "ghost"
		_, _, err := TargetEndpoint()
		if err == nil || !strings.Contains(err.Error(), "ghost") {
			t.Fatalf("未定义 target 应报错, got %v", err)
		}
	})
}

// TestUsagePrintedOnlyForArgErrors 覆盖 L-5（SilenceUsage）的两个方向：
// 运行期错误（配置加载失败、连不上、任务不存在）只打错误本身，不向 stdout
// 打印整页 flag 帮助——旧实现任何错误都先打 usage，把真正的问题淹没在帮助
// 文本里；而参数/flag 错误必须照常打 usage，因为那类失败的根因就是用法。
// 两种情况下 stderr 都保留 cobra 的 "Error:" 行（SilenceErrors=false，
// 错误不被吞、Execute 仍返回 err）。
func TestUsagePrintedOnlyForArgErrors(t *testing.T) {
	runErr := func(args ...string) (stdout, stderr string, err error) {
		t.Helper()
		rootCmd.SetArgs(args)
		t.Cleanup(func() { rootCmd.SetArgs(nil) })
		var out, errBuf bytes.Buffer
		rootCmd.SetOut(&out)
		rootCmd.SetErr(&errBuf)
		t.Cleanup(func() {
			rootCmd.SetOut(nil)
			rootCmd.SetErr(nil)
		})
		// 走真实入口 Execute（而非 rootCmd.Execute）：单次执行的残留状态清理
		// 在那里，绕过它测的就不是 main 实际跑的东西
		err = Execute()
		return out.String(), errBuf.String(), err
	}

	// 参数错误是唯一该打 usage 的一类失败：根因就是用法。少了它，
	// `handoff done` 缺参只得到一句 "accepts 1 arg(s), received 0"，
	// 既不说该给什么参数，也不说有哪些 flag。
	t.Run("参数错误打印 usage", func(t *testing.T) {
		out, errText, err := runErr("done")
		if err == nil {
			t.Fatal("done 缺任务参数应报错，实际为 nil")
		}
		if !strings.Contains(errText, "Error:") {
			t.Fatalf("stderr 应含 cobra 错误行, got %q", errText)
		}
		if !strings.Contains(out, "Usage:") {
			t.Fatalf("参数错误应打印 usage 段（错误本身说的就是用法）, got %q", out)
		}
	})

	t.Run("RunE 运行错误不打印 usage", func(t *testing.T) {
		// 配置指向不可达端口：TargetEndpoint 正常解析，client.Done 连接失败 → RunE 错误
		cfgPath := writeTestConfig(t, "listen: \"127.0.0.1:1\"\ntoken: \"local-tok\"\n")
		resetFlags(t)
		targetName = ""
		configPath = cfgPath
		rootCmd.PersistentFlags().Lookup("agentd").Changed = false
		out, errText, err := runErr("done", "task-ghost")
		if err == nil {
			t.Fatal("连接不可达 agentd 应报错，实际为 nil")
		}
		if !strings.Contains(errText, "Error:") {
			t.Fatalf("stderr 应含 cobra 错误行, got %q", errText)
		}
		if strings.Contains(out, "Usage:") {
			t.Fatalf("运行错误不应打印 usage 段, got %q", out)
		}
	})
}

func TestRootRejectsDeletedGraphCommand(t *testing.T) {
	resetAllFlags(rootCmd)
	rootCmd.SetArgs([]string{"graph", "--help"})
	var out, errBuf bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errBuf)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})

	err := Execute()
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "unknown command") {
		t.Fatalf("删除 graph 后应拒绝未知命令，err=%v stdout=%q stderr=%q", err, out.String(), errBuf.String())
	}
}

// TestWaitTimeout 覆盖 wait 的 --timeout：到点必须报错（RunE 返回 error，cobra
// 以非 0 退出），与「事件到达退出 0」可区分——这是 P0-2 修复的最后一层防线
// （配置错误/打错 task-id 已改为立即报错，--timeout 兜底剩余的无事件挂起场景）。
//
// 环境：真实 agentd httptest server + 存在但无任何事件的任务 + --timeout 300ms。
// 任务必须存在：若任务不存在，WaitEvent 会因 PolicyViolation 立即报错，测试将
// 覆盖不到 timeout 路径。
func TestWaitTimeout(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	st, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := agentd.NewServer(&config.Config{Token: testToken}, st, logger)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	now := time.Now().UTC()
	if err := st.CreateTask(&proto.Task{ID: "task-1", Target: "opencode", RepoPath: "/repo",
		State: proto.TaskStatePending, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// 配置 listen 指向测试 server 端口（无 --agentd 时 TargetEndpoint 取 cfg.Listen）
	addr := strings.TrimPrefix(ts.URL, "http://")
	cfgPath := writeTestConfig(t, "listen: \""+addr+"\"\ntoken: \""+testToken+"\"\n")
	resetFlags(t)
	targetName = ""
	configPath = cfgPath
	agentdURL = "http://127.0.0.1:7777" // 默认值，但必须显式标记为未 Changed
	rootCmd.PersistentFlags().Lookup("agentd").Changed = false
	t.Cleanup(func() { waitTimeout = 0 })

	// cobra 的 ExecuteC 只认根命令：执行子命令必须经 rootCmd.SetArgs 传完整
	// 路径（"wait <task> --timeout 300ms"），直接对 waitCmd.SetArgs 会被
	// Root().ExecuteC() 忽略并退回根命令（打印 help 返回 nil）
	rootCmd.SetArgs([]string{"wait", "task-1", "--timeout", "300ms"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	var out, errBuf bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errBuf)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	err = ExecuteContext(ctx)
	if err == nil {
		t.Fatal("--timeout 到点应返回错误（cobra 非 0 退出），实际为 nil")
	}
	if !strings.Contains(err.Error(), "超时") {
		t.Fatalf("错误应说明超时, got %v", err)
	}
	// 无人值守场景只看得到退出码：超时必须与鉴权/配置失败区分开
	if got := ExitCode(err); got != ExitTimeout {
		t.Fatalf("超时退出码 = %d, want %d", got, ExitTimeout)
	}
	// 超时不是事件到达：stdout 不得出现事件 JSON（cobra 会在错误时向 stdout
	// 打印 usage，只断言「无事件 JSON」这一语义）
	if strings.Contains(out.String(), `"seq"`) || strings.Contains(out.String(), `"type"`) {
		t.Fatalf("超时退出不应输出事件 JSON, got %q", out.String())
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("--timeout 300ms 应约 300ms 返回, 实际 %v", elapsed)
	}
}

// TestWaitRejectsNegativeTimeout 验证 --timeout 负值被拒绝而不是当「不设上限」。
//
// 缺陷形态：waitTimeout < 0 时 `if waitTimeout > 0` 不成立，超时保护被静默跳过，
// wait 永远等下去——恰恰是在最需要兜底的无人值守场景里把兜底悄悄关掉了。
func TestWaitRejectsNegativeTimeout(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgPath := writeTestConfig(t, "listen: \"127.0.0.1:1\"\ntoken: \"local-tok\"\n")
	resetFlags(t)
	targetName = ""
	configPath = cfgPath
	t.Cleanup(func() { waitTimeout = 0 })

	rootCmd.SetArgs([]string{"wait", "task-1", "--timeout", "-5s"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	var out, errBuf bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errBuf)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := ExecuteContext(ctx)

	if err == nil {
		t.Fatal("--timeout -5s 应报错，实际为 nil（超时保护被静默跳过）")
	}
	if !strings.Contains(err.Error(), "--timeout") {
		t.Errorf("错误应点名 --timeout, got %v", err)
	}
}

// TestExitCodeDistinguishesTimeout 验证退出码能区分「等满了时限」与其他失败。
//
// 无人值守场景（cron/后台脚本）看不到 stderr，只看得到退出码：全是 1 的话，
// 该继续等的超时和该立刻告警的鉴权失败无从区分。
func TestExitCodeDistinguishesTimeout(t *testing.T) {
	if got := ExitCode(nil); got != 0 {
		t.Errorf("成功应退出 0, got %d", got)
	}
	if got := ExitCode(errors.New("连接失败")); got != ExitFailure {
		t.Errorf("普通失败应退出 %d, got %d", ExitFailure, got)
	}
	timeoutErr := &exitCodeError{code: ExitTimeout, err: errors.New("wait 超时（1h）未等到事件")}
	if got := ExitCode(timeoutErr); got != ExitTimeout {
		t.Errorf("超时应退出 %d, got %d", ExitTimeout, got)
	}
	// 包装后错误文本与 errors.Is 链不受影响：cobra 照常把原文打到 stderr
	if !strings.Contains(timeoutErr.Error(), "超时") {
		t.Errorf("包装不应改写错误文本, got %q", timeoutErr.Error())
	}
}
