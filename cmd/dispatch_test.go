// dispatch 默认弹终端测试：darwin 下 openTerminal 被调、--no-terminal 抑制、
// 弹窗失败不影响退出码。
package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// dispatchTestTaskJSON 是假 agentd 返回的任务 JSON。测试可临时改写它来构造
// 不同的任务形态（如带基线字段），t.Cleanup 里复原。
var dispatchTestTaskJSON = `{"id":"task-abc123","state":"running"}`

// runDispatch 以给定 flags 执行 dispatch（指向 fake agentd），返回 stdout、stderr 与错误。
func runDispatch(t *testing.T, extraArgs ...string) (string, string, error) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tasks" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, dispatchTestTaskJSON)
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

	args := append([]string{"dispatch", "--project", "proj1", "--prompt", "x"}, extraArgs...)
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
	return out.String(), errBuf.String(), err
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

	if _, _, err := runDispatch(t); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(called) != 1 {
		t.Fatalf("openTerminal 应被调 1 次，得到 %d 次", len(called))
	}
	joined := strings.Join(called[0], " ")
	if !strings.Contains(joined, "attach") || !strings.Contains(joined, "task-abc123") {
		t.Fatalf("attach argv 应含 attach 与任务 id: %v", called[0])
	}
	if called[0][0] != "handoff" {
		t.Fatalf("弹窗命令应指向 handoff 自身（attach 走 render 流），实得 %v", called[0])
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

	out, _, err := runDispatch(t, "--no-terminal")
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

	out, _, err := runDispatch(t)
	if err != nil {
		t.Fatalf("弹窗失败不应让命令失败，得到 err=%v", err)
	}
	if !strings.Contains(out, `"task-abc123"`) {
		t.Fatalf("任务 JSON 应正常输出，得到 %q", out)
	}
}

// TestDispatchPrintsBaselineToStderr 验证派发后 stderr 打出基线短号，且 stdout
// 契约不受影响（仍是单行任务 JSON——上层脚本按行解析，多一行就全乱）。
func TestDispatchPrintsBaselineToStderr(t *testing.T) {
	old := dispatchTestTaskJSON
	dispatchTestTaskJSON = `{"id":"task-abc123","state":"running","base_commit":"d64bac4d64bac4d64bac4d64bac4d64bac4d64ba","base_ahead":0}`
	t.Cleanup(func() { dispatchTestTaskJSON = old })

	out, errOut, err := runDispatch(t, "--no-terminal")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !strings.Contains(errOut, "基线 d64bac4") {
		t.Fatalf("stderr 应含基线短号，得到 %q", errOut)
	}
	if strings.Contains(errOut, "领先") {
		t.Fatalf("未分叉时不该提领先提交数，得到 %q", errOut)
	}
	if strings.Contains(out, "基线") {
		t.Fatalf("stdout 必须只有任务 JSON（脚本按行解析），得到 %q", out)
	}
}

// TestDispatchPrintsDivergenceToStderr 验证任务仓库领先基线时把丢掉的提交数
// 说出来——B35 的现场就是这个差异毫无痕迹，审核者甚至反过来怀疑执行者搞错了。
func TestDispatchPrintsDivergenceToStderr(t *testing.T) {
	old := dispatchTestTaskJSON
	dispatchTestTaskJSON = `{"id":"task-abc123","state":"running","base_commit":"d64bac4d64bac4d64bac4d64bac4d64bac4d64ba","base_ahead":3}`
	t.Cleanup(func() { dispatchTestTaskJSON = old })

	_, errOut, err := runDispatch(t, "--no-terminal")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !strings.Contains(errOut, "领先 3 个提交") {
		t.Fatalf("stderr 应说明任务仓库领先几个提交，得到 %q", errOut)
	}
}

// TestDispatchNoBaselineNoLine 验证没有基线（切已存在分支/老 agentd）时不打
// 空洞的一行：宁可不说，也不要打一个「基线 」误导人。
func TestDispatchNoBaselineNoLine(t *testing.T) {
	_, errOut, err := runDispatch(t, "--no-terminal")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if strings.Contains(errOut, "基线") {
		t.Fatalf("无基线时不应打基线行，得到 %q", errOut)
	}
}

// dirtyCwd 在 t.TempDir() 里造一个「已跟踪文件被改过」的仓库并 chdir 进去，
// 返回仓库路径。t.Chdir 在用例结束时自动切回。
func dirtyCwd(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "init")
	// 改一个已跟踪文件且不提交——这就是 B29 会静默丢掉的那类改动
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	return repo
}

// runRemoteDispatch 以远程模式（--target e2e）执行 dispatch，返回 stdout、
// 假 agentd 收到的请求数与错误。
//
// 不复用 runDispatch：那个 helper 恒置 targetName = ""（本机模式），走不进
// 远程派发的门；这里必须自己搭一份带 targets 的配置。
func runRemoteDispatch(t *testing.T, repo string, extraArgs ...string) (string, int32, error) {
	t.Helper()
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, dispatchTestTaskJSON)
	}))
	t.Cleanup(ts.Close)
	addr := strings.TrimPrefix(ts.URL, "http://")
	cfgPath := writeTestConfig(t, "listen: \"127.0.0.1:7777\"\ntoken: \""+testToken+"\"\n"+
		"targets:\n  e2e:\n    addr: \""+addr+"\"\n    token: \""+testToken+"\"\n")

	// resetFlags 已负责在用例结束时复原 agentdURL/targetName/configPath 与
	// --agentd 的 Changed 标记
	resetFlags(t)
	configPath = cfgPath
	targetName = "e2e"
	agentdURL = "http://127.0.0.1:7777"
	rootCmd.PersistentFlags().Lookup("agentd").Changed = false
	t.Cleanup(func() {
		dispatchNoTerminal = false
		dispatchAllowDirty = false
		dispatchNoSyncCheck = false
	})

	args := append([]string{"dispatch", "--project", "proj1", "--prompt", "x", "--no-terminal"}, extraArgs...)
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
	return out.String(), hits.Load(), err
}

// TestDispatchRemoteDirtyDoesNotSendRequest 验证远程派发在本地已跟踪脏时
// **一个 HTTP 请求都不发**就返回错误。
//
// 判别力：请求计数。只断言 err != nil 的话，「先发请求再报错」的实现照样绿，
// 而那会在远端建出分支和 worktree——正是 B39 现场的成因。
func TestDispatchRemoteDirtyDoesNotSendRequest(t *testing.T) {
	repo := dirtyCwd(t)
	_, hits, err := runRemoteDispatch(t, repo)
	if err == nil {
		t.Fatal("本地已跟踪脏时远程派发应被拒")
	}
	if !strings.Contains(err.Error(), "a.txt") {
		t.Fatalf("错误应列出脏文件，得到: %v", err)
	}
	if hits != 0 {
		t.Fatalf("拒发时不该发起任何 HTTP 请求，实际 %d 次", hits)
	}
}

// TestDispatchRemoteDirtyAllowDirty 验证 --allow-dirty 让同一场景放行到底。
func TestDispatchRemoteDirtyAllowDirty(t *testing.T) {
	repo := dirtyCwd(t)
	out, hits, err := runRemoteDispatch(t, repo, "--allow-dirty")
	if err != nil {
		t.Fatalf("--allow-dirty 应放行: %v", err)
	}
	if hits != 1 {
		t.Fatalf("放行后应正常发起一次派发请求，实际 %d 次", hits)
	}
	if !strings.Contains(out, "task-abc123") {
		t.Fatalf("stdout 应仍是单行任务 JSON，得到: %q", out)
	}
}

// TestDispatchNoSyncCheckSkipsDirty 验证 --no-sync-check 把整块基线逻辑
// （含本地工作区校验）一并关掉——它关的是「根本不看 cwd」，语义上必须覆盖新检查。
func TestDispatchNoSyncCheckSkipsDirty(t *testing.T) {
	repo := dirtyCwd(t)
	_, hits, err := runRemoteDispatch(t, repo, "--no-sync-check")
	if err != nil {
		t.Fatalf("--no-sync-check 应跳过本地校验: %v", err)
	}
	if hits != 1 {
		t.Fatalf("应正常发起一次派发请求，实际 %d 次", hits)
	}
}

// TestDispatchLocalDirtyNotChecked 验证**本机派发**（无 --target）完全不查本地工作区。
//
// 为什么本机模式必须豁免：cwd 与目标项目可以是两个毫不相干的仓库，查 cwd 是查错了
// 对象。这也正是既有代码不在本机模式采基线的原因，新检查必须共用同一道门。
func TestDispatchLocalDirtyNotChecked(t *testing.T) {
	dirtyCwd(t)
	out, _, err := runDispatch(t, "--no-terminal")
	if err != nil {
		t.Fatalf("本机派发不该因 cwd 脏而失败: %v", err)
	}
	if !strings.Contains(out, "task-abc123") {
		t.Fatalf("本机派发应正常返回任务 JSON，得到: %q", out)
	}
}

// TestDispatchPrintsDirtySnapshotToStderr 验证 B43 的回显：执行机仓库有未提交
// 改动时 stderr 说出来（远程派发时审核者根本看不到那台机器的工作区），且
// stdout 仍是单行任务 JSON——上层脚本按行解析，多一行就全乱。
func TestDispatchPrintsDirtySnapshotToStderr(t *testing.T) {
	old := dispatchTestTaskJSON
	dispatchTestTaskJSON = `{"id":"task-abc123","state":"running","repo_dirty_count":3,"repo_dirty_files":"a.go, b.go, c.go"}`
	t.Cleanup(func() { dispatchTestTaskJSON = old })

	out, errOut, err := runDispatch(t, "--no-terminal")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !strings.Contains(errOut, "3 处未提交改动") || !strings.Contains(errOut, "a.go, b.go, c.go") {
		t.Fatalf("stderr 应含脏改动条数与文件名，得到 %q", errOut)
	}
	if strings.Contains(out, "未提交改动") {
		t.Fatalf("stdout 必须只有任务 JSON（脚本按行解析），得到 %q", out)
	}
}

// TestDispatchNoDirtySnapshotNoLine 验证干净时不打空洞的一行：
// 「有 0 处未提交改动」比不说更糟。
func TestDispatchNoDirtySnapshotNoLine(t *testing.T) {
	_, errOut, err := runDispatch(t, "--no-terminal")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if strings.Contains(errOut, "未提交改动") {
		t.Fatalf("干净时不应打提示行，得到 %q", errOut)
	}
}
