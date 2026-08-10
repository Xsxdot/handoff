// handoff status 的 CLI 行为测试：正常渲染、老 agentd 降级且退 0、401 退 1。
package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeStatusConfig 写一份最小可用配置，返回路径。
// 字段名按 yaml.v3 对无 tag 结构体的默认规则（全小写字段名）。
func writeStatusConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	body := "listen: 127.0.0.1:7777\ntoken: " + testToken + "\ndatadir: " + dir + "\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// runStatus 执行一次 status 命令，返回 stdout 与错误。
func runStatus(t *testing.T, cfgPath, agentdURL string, extra ...string) (string, error) {
	t.Helper()
	resetFlags(t)
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	args := append([]string{"status", "--config", cfgPath, "--agentd", agentdURL}, extra...)
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		statusJSONOut = false
	})
	err := rootCmd.ExecuteContext(context.Background())
	return buf.String(), err
}

// 正常 200：关键字段要出现在文本里，且不报错（退出码 0）。
func TestStatusRendersText(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"listen":"0.0.0.0:7777","data_dir":"/data",
			"started_at":"2026-08-10T00:00:00Z",
			"version":{"revision":"8353ef68d711eaf63eeb1287f342f3238204aec8","go":"go1.26.1"},
			"executors":["claude","opencode"],"default_executor":"opencode",
			"task_counts":{"running":1,"pending":0,"completed":2},
			"active":[{"id":"1c28505a-1111-2222-3333-444455556666","name":"B19 env 注入",
				"state":"running","executor":"opencode","live":"dead",
				"note":"tmux 会话 handoff-1c28505a 不存在"}]}`))
	}))
	t.Cleanup(ts.Close)

	out, err := runStatus(t, writeStatusConfig(t), ts.URL)
	if err != nil {
		t.Fatalf("status 应成功，得到错误: %v", err)
	}
	for _, want := range []string{"可用", "8353ef68d711", "/data", "opencode", "running 1", "1c28505a"} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %q：\n%s", want, out)
		}
	}
	// 计数为零的状态不该出现在文本里（JSON 侧才恒有六个键）
	if strings.Contains(out, "pending 0") {
		t.Fatalf("文本渲染应省略零值计数：\n%s", out)
	}
}

// 老 agentd 返回 404：输出降级结论，**且不报错**（退出码 0）。
func TestStatusOldAgentdIsSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)

	out, err := runStatus(t, writeStatusConfig(t), ts.URL)
	if err != nil {
		t.Fatalf("老 agentd 照样能派发能审阅，必须退 0，得到错误: %v", err)
	}
	for _, want := range []string{"版本过旧", "Bearer 鉴权通过", "升级远端 agentd"} {
		if !strings.Contains(out, want) {
			t.Fatalf("降级输出缺少 %q：\n%s", want, out)
		}
	}
}

// 401：必须报错（退出码 1）。
func TestStatusUnauthorizedFails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(ts.Close)

	if _, err := runStatus(t, writeStatusConfig(t), ts.URL); err == nil {
		t.Fatal("401 是真失败，必须返回错误")
	}
}

// --json：顶层 reachable 与退出码同源。
func TestStatusJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"listen":"l","data_dir":"d","task_counts":{},"active":[]}`))
	}))
	t.Cleanup(ts.Close)

	out, err := runStatus(t, writeStatusConfig(t), ts.URL, "--json")
	if err != nil {
		t.Fatalf("status --json: %v", err)
	}
	if !strings.Contains(out, `"reachable":true`) {
		t.Fatalf("JSON 输出缺少 reachable:\n%s", out)
	}
}

// --json 遇上老 agentd：reachable=true 且 degraded=true。
func TestStatusJSONDegraded(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)

	out, err := runStatus(t, writeStatusConfig(t), ts.URL, "--json")
	if err != nil {
		t.Fatalf("status --json: %v", err)
	}
	if !strings.Contains(out, `"degraded":true`) || !strings.Contains(out, `"reachable":true`) {
		t.Fatalf("降级 JSON 应 reachable=true 且 degraded=true:\n%s", out)
	}
}
