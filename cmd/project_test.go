package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// TestProjectAddRejectsNonRepo 验证 cwd 不是 git 仓库时本地就拒，报文说明原因。
// 为什么在本地拦：项目身份只依赖本机信息，多跑一次网络毫无意义。
func TestProjectAddRejectsNonRepo(t *testing.T) {
	t.Chdir(t.TempDir()) // 临时目录不是 git 仓库
	var out bytes.Buffer
	projectAddCmd.SetOut(&out)
	projectAddCmd.SetErr(&out)
	err := projectAddCmd.RunE(projectAddCmd, nil)
	if err == nil {
		t.Fatal("非 git 目录应被拒")
	}
	if !strings.Contains(err.Error(), "origin") {
		t.Errorf("报文应说明身份由 origin 派生，got %q", err.Error())
	}
}

// TestLocalOriginURL 验证从 cwd 读 origin；不是 git 仓库时返回空串而不是报错。
//
// 原属 cmd/repo_test.go（B62 cutover 后 localOriginURL 随 project.go 存活），
// 保留以防覆盖回归。
func TestLocalOriginURL(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if got := localOriginURL(); got != "" {
		t.Fatalf("非 git 目录应返回空串，got %q", got)
	}
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	want := filepath.Join(dir, "fake-origin.git")
	if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", want).CombinedOutput(); err != nil {
		t.Fatalf("remote add: %v: %s", err, out)
	}
	if got := localOriginURL(); got != want {
		t.Fatalf("localOriginURL() = %q, want %q", got, want)
	}
}

// TestProjectEditHitsCorrectEndpoint 断言 project edit 把 PATCH 打到
// /api/projects/<name>，body 只带给出的字段，成功响应被正确呈现。
func TestProjectEditHitsCorrectEndpoint(t *testing.T) {
	resetProjectEditFlags(t)
	var gotMethod, gotPath string
	var gotBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		// 响应线格式对齐 proto.ProjectLocation
		io.WriteString(w, `{"project_id":"pid1","name":"x","path":"/y",`+
			`"origin_url":"git@example.com:x/handoff.git","created_at":"2026-08-15T00:00:00Z"}`)
	}))
	defer ts.Close()

	var stdout bytes.Buffer
	err := runSubcommandForTest(t, &stdout, ts.URL, "测试令牌",
		[]string{"project", "edit", "handoff", "--name", "x", "--path", "/y"})
	if err != nil {
		t.Fatalf("project edit 应成功: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", gotMethod)
	}
	if gotPath != "/api/projects/handoff" {
		t.Errorf("path = %q, want /api/projects/handoff", gotPath)
	}
	var body map[string]string
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatalf("解析请求体: %v", err)
	}
	if body["new_name"] != "x" || body["path"] != "/y" {
		t.Errorf("请求体 = %v, want new_name=x path=/y", body)
	}
	if !strings.Contains(stdout.String(), "已更新 handoff") ||
		!strings.Contains(stdout.String(), "新名字 x") ||
		!strings.Contains(stdout.String(), "新路径 /y") {
		t.Errorf("成功输出应点明改了哪些字段, got %q", stdout.String())
	}
}

// TestProjectEditRequiresAtLeastOneField 断言 --name 与 --path 都空时在发请求前就
// 报错，并提示至少给一个——本地可判的错误，不该打一趟网络。
func TestProjectEditRequiresAtLeastOneField(t *testing.T) {
	resetProjectEditFlags(t)
	// 配置指向必然连不上的端口：若请求真的发出去了，err 会是连接失败而不是
	// 字段校验错误，测试立刻暴露「本地拦截失效」
	var stdout bytes.Buffer
	err := runSubcommandForTest(t, &stdout, "http://127.0.0.1:1", "测试令牌",
		[]string{"project", "edit", "handoff"})
	if err == nil {
		t.Fatal("--name 与 --path 都空应报错，实际为 nil")
	}
	if !strings.Contains(err.Error(), "--name") || !strings.Contains(err.Error(), "--path") {
		t.Errorf("报文应提示至少给 --name 或 --path 之一, got %q", err.Error())
	}
}

// TestProjectEditTargetResolvesRemote 断言带 --target devbox 时请求打到
// targets 表里 devbox 的地址，而不是本机。
func TestProjectEditTargetResolvesRemote(t *testing.T) {
	resetProjectEditFlags(t)
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/api/projects/handoff" || r.Method != http.MethodPatch {
			t.Errorf("非预期请求 %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"project_id":"pid1","name":"x","path":"/y",`+
			`"origin_url":"git@example.com:x/handoff.git"}`)
	}))
	defer ts.Close()

	cfgPath := writeTestConfig(t, "listen: \"127.0.0.1:1\"\ntoken: \"local-tok\"\n"+
		"targets:\n  devbox:\n    addr: \""+strings.TrimPrefix(ts.URL, "http://")+"\"\n    token: \"remote-tok\"\n")
	resetFlags(t)
	targetName = "devbox"
	configPath = cfgPath
	rootCmd.PersistentFlags().Lookup("agentd").Changed = false

	rootCmd.SetArgs([]string{"project", "edit", "handoff", "--name", "x"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	t.Cleanup(func() { rootCmd.SetOut(nil) })
	if err := Execute(); err != nil {
		t.Fatalf("--target devbox 应成功: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("请求应打到 targets 表中的 devbox 地址，实得 %d 次", got)
	}
}

// resetProjectEditFlags 复位 project edit 的包级 flag，防止跨用例残留
// （runSubcommandForTest 的 resetFlags 不覆盖它们）。
func resetProjectEditFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		projectEditName, projectEditPath = "", ""
	})
}
