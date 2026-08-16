// tasks --all 测试：stdout 仍是每行一个任务 JSON，机器应答摘要走 stderr。
package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// tasksAllFixture 是 fake agentd 对 GET /api/tasks?scope=all 的固定响应。
var tasksAllFixture = proto.TasksResp{
	Machines: []proto.MachineStatus{
		{Name: "", Ok: true, FetchedAt: time.Now()},
		{Name: "devbox", Ok: true, FetchedAt: time.Now()},
		{Name: "nas", Ok: false, Error: "dial tcp 10.0.0.9:7777: connect: connection refused"},
	},
	Tasks: []proto.TaskView{
		{Task: proto.Task{ID: "aaaa", Name: "本机任务", State: proto.TaskStateRunning}},
		{Task: proto.Task{ID: "bbbb", Name: "远端任务", State: proto.TaskStateRunning, Machine: "devbox"}},
	},
}

// fakeTasksAllServer 起一个服务 /api/tasks 的假 agentd：scope=all 返回信封，否则返回裸数组。
func fakeTasksAllServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("scope") == "all" {
			json.NewEncoder(w).Encode(tasksAllFixture)
			return
		}
		json.NewEncoder(w).Encode([]proto.TaskView{tasksAllFixture.Tasks[0]})
	}))
	t.Cleanup(ts.Close)
	return ts
}

// runSubcommandForTestWithErr 同 runSubcommandForTest，但把 stderr 也捕获进 stderr 参数。
func runSubcommandForTestWithErr(t *testing.T, stdout, stderr *bytes.Buffer, addr, token string, args []string) error {
	t.Helper()
	cfgPath := writeTestConfig(t,
		"listen: \""+strings.TrimPrefix(addr, "http://")+"\"\ntoken: \""+token+"\"\n")
	resetFlags(t)
	targetName = ""
	configPath = cfgPath
	rootCmd.PersistentFlags().Lookup("agentd").Changed = false
	rootCmd.SetArgs(args)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	return Execute()
}

// TestTasksAllStdoutJSONStderrSummary 断言：--all 的 stdout 每行一个任务 JSON
// （含 machine 名），机器应答摘要（含缺席机器与原因）在 stderr。
func TestTasksAllStdoutJSONStderrSummary(t *testing.T) {
	resetW3aFlags(t)
	ts := fakeTasksAllServer(t)
	var stdout, stderr bytes.Buffer
	err := runSubcommandForTestWithErr(t, &stdout, &stderr, ts.URL, "测试令牌", []string{"tasks", "--all"})
	if err != nil {
		t.Fatalf("tasks --all: %v", err)
	}
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("stdout 应恰好 2 行任务 JSON，实得 %d：%q", len(lines), stdout.String())
	}
	var first proto.TaskView
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("stdout 行无法解析为任务 JSON: %v", err)
	}
	var second proto.TaskView
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("stdout 行无法解析为任务 JSON: %v", err)
	}
	if second.Machine != "devbox" {
		t.Errorf("远端任务应带 machine 名，实得 %q", second.Machine)
	}
	if !strings.Contains(stderr.String(), "nas") || !strings.Contains(stderr.String(), "connection refused") {
		t.Errorf("未应答的机器必须在 stderr 摘要里逐台可见带原因：\n%s", stderr.String())
	}
}

// TestTasksWithoutAllUnchanged 断言：不带 --all 时输出与既有一致（裸任务列表）。
func TestTasksWithoutAllUnchanged(t *testing.T) {
	resetW3aFlags(t)
	ts := fakeTasksAllServer(t)
	var stdout bytes.Buffer
	err := runSubcommandForTest(t, &stdout, ts.URL, "测试令牌", []string{"tasks"})
	if err != nil {
		t.Fatalf("tasks: %v", err)
	}
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("不带 --all 应只输出本机 1 行任务 JSON，实得 %d：%q", len(lines), stdout.String())
	}
	var tv proto.TaskView
	if err := json.Unmarshal([]byte(lines[0]), &tv); err != nil {
		t.Fatalf("stdout 行无法解析为任务 JSON: %v", err)
	}
}
