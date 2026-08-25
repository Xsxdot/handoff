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

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
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

// TestUnattendedJudgement 钉死 §3.3 的异常判据。
//
// 为什么必须写死而不是「watchers==0 就报警」：waiting_review 等协调者裁决，
// 挂几天都正常，把它算进来这条标记就会天天亮，变成没人再看的狼来了。
func TestUnattendedJudgement(t *testing.T) {
	zero, one := 0, 1
	cases := []struct {
		name     string
		state    string
		watchers *int
		want     bool
	}{
		{"running 无人听 = 异常", "running", &zero, true},
		{"pending 无人听 = 异常", "pending", &zero, true},
		{"waiting_answer 无人听 = 异常", "waiting_answer", &zero, true},
		{"waiting_review 无人听 = 正常", "waiting_review", &zero, false},
		{"running 有人听 = 正常", "running", &one, false},
		{"对端没给 watchers = 不下结论", "running", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := unattended(proto.ActiveTask{State: c.state, Watchers: c.watchers})
			if got != c.want {
				t.Errorf("unattended(%s, %v) = %v, want %v", c.state, c.watchers, got, c.want)
			}
		})
	}
}

func TestAttendanceMarksTrueOrphan(t *testing.T) {
	zero := 0
	got := attendance(proto.ActiveTask{State: "running", Watchers: &zero},
		func(string) (string, string, time.Time, bool) {
			return "", "", time.Time{}, false
		})
	if !got.Unattended {
		t.Fatalf("账本查不到卡时应保留无人值守: %+v", got)
	}
}

func TestAttendanceReportsCardDriverInsteadOfOrphan(t *testing.T) {
	zero := 0
	heartbeatAt := time.Now().Add(-12 * time.Minute)
	task := proto.ActiveTask{ID: "task-1", State: "waiting_answer", Watchers: &zero}
	got := attendance(task, func(taskID string) (string, string, time.Time, bool) {
		if taskID != task.ID {
			t.Fatalf("lookup taskID = %q, want %q", taskID, task.ID)
		}
		return "B177", "session-1", heartbeatAt, true
	})
	if got.Unattended {
		t.Fatalf("有卡驱动时不应标无人值守: %+v", got)
	}
	if got.CardID != "B177" || got.Driver != "session-1" {
		t.Fatalf("卡驱动归属未带出: %+v", got)
	}
	if got.HeartbeatAge < 12*time.Minute || got.HeartbeatAge >= 13*time.Minute {
		t.Fatalf("认领时刻年龄应约为 12 分钟: %s", got.HeartbeatAge)
	}

	var buf bytes.Buffer
	renderStatusWithLookup(&buf, "127.0.0.1:7777", proto.BuildInfo{}, &proto.StatusResp{
		TaskCounts: map[string]int{"waiting_answer": 1},
		Active:     []proto.ActiveTask{task},
	}, func(string) (string, string, time.Time, bool) {
		return "B177", "session-1", heartbeatAt, true
	})
	line := buf.String()
	if !strings.Contains(line, "无人订阅") || !strings.Contains(line, "B177") {
		t.Fatalf("渲染应显示无人订阅与卡号:\n%s", line)
	}
	if strings.Contains(line, "无人值守") {
		t.Fatalf("有卡驱动时不应显示无人值守:\n%s", line)
	}
	if !strings.Contains(line, "认领于 12m 前") {
		t.Fatalf("渲染应显示整分钟认领时刻年龄:\n%s", line)
	}
	var unknownHeartbeat bytes.Buffer
	renderStatusWithLookup(&unknownHeartbeat, "127.0.0.1:7777", proto.BuildInfo{}, &proto.StatusResp{
		TaskCounts: map[string]int{"waiting_answer": 1},
		Active:     []proto.ActiveTask{task},
	}, func(string) (string, string, time.Time, bool) {
		return "B177", "session-1", time.Time{}, true
	})
	if !strings.Contains(unknownHeartbeat.String(), "认领于 未知") {
		t.Fatalf("零值认领时刻应显示未知:\n%s", unknownHeartbeat.String())
	}
}

// TestStatusReportsLedgerHealthWithRetiredEnabledFlag 钉住 B229 §2.6：
// enabled=false 的 config 下 status 不再跳过账本——已挂账且有驱动会话的
// 任务由账本 lookup 报出卡号与驱动者（账本健康可见），而不是退回「无人值守」。
func TestStatusReportsLedgerHealthWithRetiredEnabledFlag(t *testing.T) {
	targetName = ""
	resetFlags(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	body := "listen: 127.0.0.1:7777\ntoken: " + testToken + "\ndatadir: " + dir +
		"\nledger:\n  enabled: false\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// 种一份真实账本：最小工作流 + 卡 + 挂账（target 空串 = 本机模式）+ 驱动认领。
	lst, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("开账本: %v", err)
	}
	if _, err := lst.PutWorkflow("bug", ledger.WorkflowDef{
		Nodes: []ledger.NodeDef{{Name: ledger.StatusTodo}}}); err != nil {
		t.Fatalf("种工作流: %v", err)
	}
	card, err := lst.CreateCard(ledger.NewCard{
		Title: "退休钉", Project: "demo", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	if err := lst.LinkTask(card.ID, "", "task-ledger-1", "implement", "t"); err != nil {
		t.Fatalf("挂账: %v", err)
	}
	if err := lst.ClaimDriver(card.ID, "sess-retired"); err != nil {
		t.Fatalf("认领驱动: %v", err)
	}
	// 先关库：status 会以进程内第二次 Open 打开同一路径。
	if err := lst.Close(); err != nil {
		t.Fatalf("关账本: %v", err)
	}

	const statusBody = `{"listen":"0.0.0.0:7777","data_dir":"/data",
		"started_at":"2026-08-10T00:00:00Z","version":{},
		"executors":["opencode"],"default_executor":"opencode",
		"task_counts":{"running":1},
		"active":[{"id":"task-ledger-1","name":"挂着账的任务",
			"state":"running","executor":"opencode","live":"alive","watchers":0}]}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(statusBody))
	}))
	t.Cleanup(ts.Close)

	out, err := runStatus(t, cfgPath, ts.URL)
	if err != nil {
		t.Fatalf("status 应成功: %v", err)
	}
	wantCard := "无人订阅（卡 " + card.ID
	if !strings.Contains(out, wantCard) || !strings.Contains(out, "sess-retired") {
		t.Fatalf("enabled 退休后 status 应从账本报出卡与驱动（want 含 %q 与 %q）:\n%s",
			wantCard, "sess-retired", out)
	}
}

func TestAttendanceIgnoresLedgerWhenLookupNil(t *testing.T) {
	zero := 0
	got := attendance(proto.ActiveTask{State: "running", Watchers: &zero}, nil)
	if !got.Unattended {
		t.Fatalf("lookup 为 nil 时应降级为无人值守: %+v", got)
	}
}

func TestAttendanceKeepsWatchedTaskSilent(t *testing.T) {
	one := 1
	called := false
	got := attendance(proto.ActiveTask{State: "running", Watchers: &one},
		func(string) (string, string, time.Time, bool) {
			called = true
			return "B177", "session-1", time.Now(), true
		})
	if got.Unattended || got.CardID != "" || got.Driver != "" || got.HeartbeatAge != 0 {
		t.Fatalf("有订阅的任务三格都应为空: %+v", got)
	}
	if called {
		t.Fatal("有订阅的任务不应查询账本")
	}
}

// TestRenderStatusMarksUnattended 验证活跃任务行在存活结论之后追加标记，
// 且只对该标记的三个状态出现。
func TestRenderStatusMarksUnattended(t *testing.T) {
	zero := 0
	var buf bytes.Buffer
	renderStatus(&buf, "127.0.0.1:7777", proto.BuildInfo{}, &proto.StatusResp{
		TaskCounts: map[string]int{"running": 1, "waiting_review": 1},
		Active: []proto.ActiveTask{
			{ID: "aaaaaaaa-1", Name: "跑着的", State: "running",
				Executor: "opencode", Live: proto.LiveAlive, Watchers: &zero},
			{ID: "bbbbbbbb-1", Name: "等审的", State: "waiting_review",
				Executor: "opencode", Live: proto.LiveAlive, Watchers: &zero},
		},
	})
	out := buf.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var running, review string
	for _, l := range lines {
		if strings.Contains(l, "跑着的") {
			running = l
		}
		if strings.Contains(l, "等审的") {
			review = l
		}
	}
	if !strings.Contains(running, "⚠ 无人值守") {
		t.Errorf("running + watchers=0 未标记无人值守: %q", running)
	}
	if strings.Contains(review, "⚠ 无人值守") {
		t.Errorf("waiting_review 不该标记无人值守: %q", review)
	}
	if !strings.Contains(running, "executor 存活") {
		t.Errorf("标记不得顶掉既有的存活结论: %q", running)
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

// 托管时不打任何更新行——B59 取消了待命概念，托管本身无需提示。
//
// why：托管是正常态，绝大多数机器都该是这样；为正常态打一行只会让这条
// 命令的常输出变脏，真正要人注意的「非托管」反而被淹掉。
func TestRenderStatusManagedNoNotice(t *testing.T) {
	var buf bytes.Buffer
	st := &proto.StatusResp{
		Listen: "127.0.0.1:7777", DataDir: "/d", StartedAt: time.Now().Add(-time.Hour),
		Executors: []string{"opencode"}, DefaultExecutor: "opencode",
		TaskCounts: map[string]int{},
		Update:     &proto.UpdateStatus{Managed: true},
	}
	renderStatus(&buf, "http://x", proto.BuildInfo{}, st)
	if strings.Contains(buf.String(), "更新") {
		t.Fatalf("托管时不该打更新行:\n%s", buf.String())
	}
}

// 非托管时那一行要把后果说清楚：换版会被硬拒绝，且 --force 也不越过。
//
// why：不说，用户只会看到一条没头没脑的拒绝——handoff upgrade 说拒绝、
// --force 也拒绝，而 status 从未解释过原因。
func TestRenderStatusShowsUnmanagedReason(t *testing.T) {
	var buf bytes.Buffer
	st := &proto.StatusResp{
		Listen: "127.0.0.1:7777", DataDir: "/d", StartedAt: time.Now(),
		Executors: []string{"opencode"}, DefaultExecutor: "opencode",
		TaskCounts: map[string]int{},
		Update:     &proto.UpdateStatus{Managed: false},
	}
	renderStatus(&buf, "http://x", proto.BuildInfo{}, st)
	out := buf.String()
	if !strings.Contains(out, "非托管启动，换版会被拒绝") {
		t.Fatalf("非托管必须说明换版会被拒绝:\n%s", out)
	}
	if !strings.Contains(out, "--force 也不越过") {
		t.Fatalf("必须说明 force 也不越过:\n%s", out)
	}
	if !strings.Contains(out, "handoff service install") {
		t.Fatalf("必须给出处置建议:\n%s", out)
	}
}

// Update 为 nil（老 agentd 不发这个字段）时不打任何更新行。
func TestRenderStatusNoUpdateLine(t *testing.T) {
	var buf bytes.Buffer
	st := &proto.StatusResp{
		Listen: "127.0.0.1:7777", DataDir: "/d", StartedAt: time.Now(),
		Executors: []string{"opencode"}, DefaultExecutor: "opencode",
		TaskCounts: map[string]int{},
	}
	renderStatus(&buf, "http://x", proto.BuildInfo{}, st)
	if strings.Contains(buf.String(), "更新") {
		t.Fatalf("Update=nil 时不该打更新行:\n%s", buf.String())
	}
}

// TestRenderStatusShowsProcUsage 验证 uid 级进程占用以「已用/上限」形式渲染，
// 且对端没给（nil）时整行不打印——打一行「0/0」比不打更糟。
func TestRenderStatusShowsProcUsage(t *testing.T) {
	var buf bytes.Buffer
	st := &proto.StatusResp{
		Listen: "127.0.0.1:7777", DataDir: "/d", StartedAt: time.Now(),
		Executors: []string{"opencode"}, DefaultExecutor: "opencode",
		TaskCounts: map[string]int{},
		Proc:       &proto.ProcUsage{Used: 346, Limit: 2666},
	}
	renderStatus(&buf, "http://x", proto.BuildInfo{}, st)
	if !strings.Contains(buf.String(), "346/2666") {
		t.Fatalf("进程行应含 346/2666:\n%s", buf.String())
	}

	var nilBuf bytes.Buffer
	st.Proc = nil
	renderStatus(&nilBuf, "http://x", proto.BuildInfo{}, st)
	if strings.Contains(nilBuf.String(), "进程") {
		t.Fatalf("Proc=nil 时不该打进程行:\n%s", nilBuf.String())
	}
}

// TestRenderStatusShowsPerTaskProcs 验证活跃任务行追加进程数，
// 且 nil 时不追加（对端没给这个信息，猜 0 就是制造假阳性）。
func TestRenderStatusShowsPerTaskProcs(t *testing.T) {
	five := 5
	var buf bytes.Buffer
	st := &proto.StatusResp{
		Listen: "127.0.0.1:7777", DataDir: "/d", StartedAt: time.Now(),
		Executors: []string{"opencode"}, DefaultExecutor: "opencode",
		TaskCounts: map[string]int{"running": 1},
		Active: []proto.ActiveTask{
			{ID: "cccccccc-1", Name: "带计数的", State: "running",
				Executor: "opencode", Live: proto.LiveAlive, Procs: &five},
		},
	}
	renderStatus(&buf, "http://x", proto.BuildInfo{}, st)
	if !strings.Contains(buf.String(), "5 进程") {
		t.Fatalf("活跃任务行应含 \"5 进程\":\n%s", buf.String())
	}

	var nilBuf bytes.Buffer
	st.Active[0].Procs = nil
	renderStatus(&nilBuf, "http://x", proto.BuildInfo{}, st)
	if strings.Contains(nilBuf.String(), "进程") {
		t.Fatalf("Procs=nil 时不该追加进程数:\n%s", nilBuf.String())
	}
}

// 有辅助监听时 status 文本带「监听」行；没有时不出现——两档常规配置输出不变。
func TestRenderStatusShowsListenAux(t *testing.T) {
	var buf bytes.Buffer
	renderStatus(&buf, "http://127.0.0.1:7777", proto.BuildInfo{}, &proto.StatusResp{
		Listen: "100.64.0.5:7777", ListenAux: "127.0.0.1:7777",
		TaskCounts: map[string]int{},
	})
	if !strings.Contains(buf.String(), "监听     100.64.0.5:7777（辅 127.0.0.1:7777）") {
		t.Fatalf("输出缺监听行：\n%s", buf.String())
	}

	buf.Reset()
	renderStatus(&buf, "http://127.0.0.1:7777", proto.BuildInfo{}, &proto.StatusResp{
		Listen: "127.0.0.1:7777", TaskCounts: map[string]int{},
	})
	if strings.Contains(buf.String(), "监听") {
		t.Fatalf("无辅助监听时不该有监听行：\n%s", buf.String())
	}
}

// TestRenderStatusShowsPtySessions 验证终端会话数出现在 status，
// 且 nil 时整行不打印（对端没上报，编一个 0 就是假结论）。
func TestRenderStatusShowsPtySessions(t *testing.T) {
	two := 2
	st := &proto.StatusResp{
		Listen: "127.0.0.1:7777", DataDir: "/d", StartedAt: time.Now(),
		Executors: []string{"opencode"}, DefaultExecutor: "opencode",
		TaskCounts: map[string]int{}, PtySessions: &two,
	}
	var buf bytes.Buffer
	renderStatus(&buf, "http://x", proto.BuildInfo{}, st)
	if !strings.Contains(buf.String(), "终端") || !strings.Contains(buf.String(), "2 个会话") {
		t.Fatalf("应含终端会话行:\n%s", buf.String())
	}

	var nilBuf bytes.Buffer
	st.PtySessions = nil
	renderStatus(&nilBuf, "http://x", proto.BuildInfo{}, st)
	if strings.Contains(nilBuf.String(), "终端") {
		t.Fatalf("PtySessions=nil 时不该打这一行:\n%s", nilBuf.String())
	}
}
