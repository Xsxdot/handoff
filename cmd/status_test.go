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
	"time"

	"github.com/xushixin/handoff/internal/proto"
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

// 对端是 release 构建时，「版本」行要显示版本号而不是光秃秃的 revision。
func TestStatusPrefersReleaseVersion(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"listen":"0.0.0.0:7777","data_dir":"/data",
			"started_at":"2026-08-10T00:00:00Z",
			"version":{"version":"v0.1.0","revision":"8353ef68d711eaf63eeb1287f342f3238204aec8","go":"go1.26.1"},
			"executors":["opencode"],"default_executor":"opencode",
			"task_counts":{},"active":[]}`))
	}))
	t.Cleanup(ts.Close)

	out, err := runStatus(t, writeStatusConfig(t), ts.URL)
	if err != nil {
		t.Fatalf("status 不应报错: %v", err)
	}
	if !strings.Contains(out, "v0.1.0") {
		t.Fatalf("release 构建的版本行应含 v0.1.0:\n%s", out)
	}
	if !strings.Contains(out, "8353ef68d711") {
		t.Fatalf("版本行仍应带 revision（排障要用）:\n%s", out)
	}
}

// 对端不是 release 构建（Version 为空）时，展示必须原样退回 revision 逻辑。
//
// why 单独钉一例：这是「新字段不许破坏既有形态」的回归闸。本机 go build 出来的
// agentd 常年是这个形态，退化成显示空版本会让 status 变得毫无信息。
func TestStatusFallsBackToRevisionWhenNoVersion(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"listen":"0.0.0.0:7777","data_dir":"/data",
			"started_at":"2026-08-10T00:00:00Z",
			"version":{"revision":"8353ef68d711eaf63eeb1287f342f3238204aec8","go":"go1.26.1"},
			"executors":["opencode"],"default_executor":"opencode",
			"task_counts":{},"active":[]}`))
	}))
	t.Cleanup(ts.Close)

	out, err := runStatus(t, writeStatusConfig(t), ts.URL)
	if err != nil {
		t.Fatalf("status 不应报错: %v", err)
	}
	if !strings.Contains(out, "8353ef68d711") {
		t.Fatalf("无版本号时应退回 revision 展示:\n%s", out)
	}
}

// compareBuild 的四种组合：两边都有版本号时比版本号，否则退回 revision 比较。
func TestCompareBuildPrefersVersion(t *testing.T) {
	cases := []struct {
		name       string
		cli, agent proto.BuildInfo
		want       string // 期望出现在结果里的子串
	}{
		{
			name:  "两边同版本",
			cli:   proto.BuildInfo{Version: "v0.1.0", Revision: "aaaaaaaaaaaa1111"},
			agent: proto.BuildInfo{Version: "v0.1.0", Revision: "bbbbbbbbbbbb2222"},
			want:  "一致",
		},
		{
			name:  "两边不同版本，要报出对端版本",
			cli:   proto.BuildInfo{Version: "v0.1.0", Revision: "aaaaaaaaaaaa1111"},
			agent: proto.BuildInfo{Version: "v0.2.0", Revision: "aaaaaaaaaaaa1111"},
			want:  "v0.2.0",
		},
		{
			name:  "对端无版本号，退回 revision 比较",
			cli:   proto.BuildInfo{Version: "v0.1.0", Revision: "aaaaaaaaaaaa1111"},
			agent: proto.BuildInfo{Revision: "aaaaaaaaaaaa1111"},
			want:  "一致",
		},
		{
			name:  "本地无版本号，退回 revision 比较且不一致",
			cli:   proto.BuildInfo{Revision: "aaaaaaaaaaaa1111"},
			agent: proto.BuildInfo{Version: "v0.2.0", Revision: "bbbbbbbbbbbb2222"},
			want:  "不一致",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := compareBuild(c.cli, c.agent); !strings.Contains(got, c.want) {
				t.Fatalf("compareBuild=%q，期望含 %q", got, c.want)
			}
		})
	}
}

// 有待命更新时 status 要多打一行，且说清楚为什么还没换。
//
// why：spec §4.7 要求「长期有活跃任务，一直不空闲」时 status 同步显示。
// 没有这一行，用户只会看到 agentd 版本一直不变，无从知道更新其实已经下好了。
func TestRenderStatusShowsPendingUpdate(t *testing.T) {
	var buf bytes.Buffer
	st := &proto.StatusResp{
		Listen: "127.0.0.1:7777", DataDir: "/d", StartedAt: time.Now().Add(-time.Hour),
		Executors: []string{"opencode"}, DefaultExecutor: "opencode",
		TaskCounts: map[string]int{},
		Update: &proto.UpdateStatus{
			Pending: "v0.3.0", DownloadedAt: time.Now().Add(-2 * time.Hour), Managed: true,
		},
	}
	renderStatus(&buf, "http://x", proto.BuildInfo{}, st)
	out := buf.String()
	if !strings.Contains(out, "v0.3.0") {
		t.Fatalf("应显示待命版本:\n%s", out)
	}
	if !strings.Contains(out, "待命") {
		t.Fatalf("应说明它在等窗口:\n%s", out)
	}
}

// 非托管时那一行要把真因说出来，否则用户永远等不到自动换版还不知道为什么。
func TestRenderStatusShowsUnmanagedReason(t *testing.T) {
	var buf bytes.Buffer
	st := &proto.StatusResp{
		Listen: "127.0.0.1:7777", DataDir: "/d", StartedAt: time.Now(),
		Executors: []string{"opencode"}, DefaultExecutor: "opencode",
		TaskCounts: map[string]int{},
		Update:     &proto.UpdateStatus{Pending: "v0.3.0", Managed: false},
	}
	renderStatus(&buf, "http://x", proto.BuildInfo{}, st)
	if !strings.Contains(buf.String(), "非托管") {
		t.Fatalf("非托管必须说明白:\n%s", buf.String())
	}
}

// 没有待命更新时不多打任何一行——绝大多数时候都是这种情况。
func TestRenderStatusNoUpdateLine(t *testing.T) {
	var buf bytes.Buffer
	st := &proto.StatusResp{
		Listen: "127.0.0.1:7777", DataDir: "/d", StartedAt: time.Now(),
		Executors: []string{"opencode"}, DefaultExecutor: "opencode",
		TaskCounts: map[string]int{},
	}
	renderStatus(&buf, "http://x", proto.BuildInfo{}, st)
	if strings.Contains(buf.String(), "待命") {
		t.Fatalf("无待命更新时不该多打行:\n%s", buf.String())
	}
}
