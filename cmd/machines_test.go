// machines 命令测试：表格契约、--json、不可达必须带原因。
package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

// machinesFixture 是 fake agentd 对 GET /api/machines 的固定响应。
var machinesFixture = proto.MachinesResp{Machines: []proto.Machine{
	{Name: "", Addr: "127.0.0.1:7777", Reachable: true, Version: "v0.2.0",
		Executors: []string{"opencode"}, DefaultExecutor: "opencode",
		ProbeMs: 0, ActiveTasks: 2},
	{Name: "devbox", Addr: "http://10.0.0.1:7777", Reachable: true, Version: "v0.2.0",
		Executors: []string{"opencode"}, DefaultExecutor: "opencode",
		ProbeMs: 12, ActiveTasks: 1},
	{Name: "nas", Addr: "http://10.0.0.9:7777", Reachable: false,
		Executors: []string{}, Error: "dial tcp 10.0.0.9:7777: connect: connection refused"},
}}

// resetW3aFlags 复位 W3a 引入的包级 flag，防止跨用例残留（runSubcommandForTest
// 的 resetFlags 不覆盖它们）。
func resetW3aFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		machinesJSON = false
		projectTree, projectTreeAll, projectTreeJSON = false, false, false
		tasksAllFlag = false
	})
}

// fakeMachinesServer 起一个只服务 GET /api/machines 的假 agentd。
func fakeMachinesServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/machines" {
			t.Errorf("非预期路径: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(machinesFixture)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// TestMachinesTableContract 断言表头与「本机」显示、不可达机器带原因。
func TestMachinesTableContract(t *testing.T) {
	resetW3aFlags(t)
	ts := fakeMachinesServer(t)
	var stdout bytes.Buffer
	err := runSubcommandForTest(t, &stdout, ts.URL, "测试令牌", []string{"machines"})
	if err != nil {
		t.Fatalf("machines: %v", err)
	}
	out := stdout.String()
	for _, hdr := range []string{"名字", "地址", "状态", "版本", "活跃", "延迟", "缺省执行者"} {
		if !strings.Contains(out, hdr) {
			t.Errorf("表头缺 %q：\n%s", hdr, out)
		}
	}
	if !strings.Contains(out, "本机") {
		t.Errorf("本机那行应显示「本机」而非空白：\n%s", out)
	}
	if !strings.Contains(out, "不可达") || !strings.Contains(out, "connection refused") {
		t.Errorf("不可达机器必须带原因：\n%s", out)
	}
}

// TestMachinesJSON 断言 --json 输出可解析为 proto.MachinesResp 的单行 JSON。
func TestMachinesJSON(t *testing.T) {
	resetW3aFlags(t)
	ts := fakeMachinesServer(t)
	var stdout bytes.Buffer
	err := runSubcommandForTest(t, &stdout, ts.URL, "测试令牌", []string{"machines", "--json"})
	if err != nil {
		t.Fatalf("machines --json: %v", err)
	}
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("--json 应恰好输出一行，实得 %d 行：%q", len(lines), stdout.String())
	}
	var got proto.MachinesResp
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("--json 输出无法解析: %v", err)
	}
	if len(got.Machines) != 3 {
		t.Fatalf("机器数 = %d，期望 3", len(got.Machines))
	}
}
