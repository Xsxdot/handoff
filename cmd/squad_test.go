package cmd

// squad 命令族回归（B156.3 K3 Task D）：命令级测试走既有 harness
// （newCardStepCLIEndpoint + runLedgerCLI，card_dispatch_test.go:77 /
// ledgercli_test.go:24），stub 服务端断言请求形状与服务端 400 报文的可行动渲染。
// 真实 gateway 链路的另一端由 Task C 的 wire 测试锁，两端拼起来即穿过完整
// JSON 边界。

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// stubSquadAgentd 起一个假 agentd：回收请求并按给定状态码/响应体作答。
func stubSquadAgentd(t *testing.T, dir string, status int, respBody string,
	seen func(r *http.Request, body string)) {
	t.Helper()
	newCardStepCLIEndpoint(t, dir, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if seen != nil {
			seen(r, string(b))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
	}))
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, ln := range strings.Split(strings.TrimSpace(s), "\n") {
		if strings.TrimSpace(ln) != "" {
			out = append(out, ln)
		}
	}
	return out
}

// TD-1：create 的请求形状（PUT 路径 + expect=0 + body 字段）与双侧渲染。
func TestSquadCreatePostsShapeAndRenders(t *testing.T) {
	dir := t.TempDir()
	var gotPath, gotMethod, gotQuery, gotBody string
	stubSquadAgentd(t, dir, http.StatusOK, `{"name":"coord","version":1}`,
		func(r *http.Request, body string) {
			gotPath, gotMethod, gotQuery, gotBody = r.URL.Path, r.Method, r.URL.RawQuery, body
		})
	out, errOut, err := runLedgerCLI(t, dir, "squad", "create",
		"--name", "coord", "--role", "coordinator", "--max-concurrency", "2")
	if err != nil {
		t.Fatalf("create 失败: %v\nstderr=%s", err, errOut)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/squads/squads/coord" ||
		gotQuery != "expect=0" {
		t.Fatalf("请求形状不符: %s %s?%s", gotMethod, gotPath, gotQuery)
	}
	var in map[string]any
	if err := json.Unmarshal([]byte(gotBody), &in); err != nil {
		t.Fatalf("请求体非 JSON: %s", gotBody)
	}
	if in["role"] != "coordinator" || in["max_concurrency"] != float64(2) {
		t.Fatalf("请求体字段不符: %s", gotBody)
	}
	if !strings.Contains(out, `"version":1`) {
		t.Fatalf("stdout 应出机器 JSON: %s", out)
	}
	if !strings.Contains(errOut, "已登记小队 coord") {
		t.Fatalf("stderr 应有人话回执: %s", errOut)
	}
}

// TD-2：参数校验在本地完成，不发起网络请求（否定断言的正控 = TD-1 证明
// 同一命令在参数合法时会真的发请求）。
func TestSquadCreateRejectsBadRoleBeforeDialing(t *testing.T) {
	dir := t.TempDir()
	dialed := false
	stubSquadAgentd(t, dir, http.StatusOK, `{}`, func(*http.Request, string) { dialed = true })
	if _, _, err := runLedgerCLI(t, dir, "squad", "create",
		"--name", "x", "--role", "boss"); err == nil ||
		!strings.Contains(err.Error(), "executor") {
		t.Fatalf("非法 role 应本地拒绝并点名词表，得 %v", err)
	}
	if dialed {
		t.Fatal("参数校验未过不得发起请求")
	}
}

// TD-3：服务端 400 报文可行动——stub 返回域校验文案，CLI 错误输出必须原样携带
// （httpStatusError.Error() 含正文，client.go:96 实证）。
func TestSquadCreateSurfacesActionableServerReject(t *testing.T) {
	dir := t.TempDir()
	stubSquadAgentd(t, dir, http.StatusBadRequest,
		`{"error":"小队 coord 成员 ghost 不存在（先登记载体）"}`, nil)
	_, errOut, err := runLedgerCLI(t, dir, "squad", "create",
		"--name", "coord", "--role", "coordinator", "--member", "ghost")
	if err == nil || !strings.Contains(err.Error(), "ghost") ||
		!strings.Contains(err.Error(), "先登记载体") {
		t.Fatalf("400 正文应随错误透传且可行动，得 %v\nstderr=%s", err, errOut)
	}
}

// TD-4：list 表格/NDJSON 双形态；请求打 GET /api/squads。
func TestSquadListRendersTableAndJSON(t *testing.T) {
	dir := t.TempDir()
	fixture := `{"carriers":[{"name":"c1","machine":"m1","cli":"opencode","home_dir":"/h","credential":"standalone","max_concurrency":2,"healthy":true,"version":1}],"squads":[{"name":"sq","role":"executor","members":["c1"],"version":1}]}`
	stubSquadAgentd(t, dir, http.StatusOK, fixture, func(r *http.Request, _ string) {
		if r.URL.Path != "/api/squads" {
			t.Errorf("list 应打 /api/squads，得 %s", r.URL.Path)
		}
	})
	out, _, err := runLedgerCLI(t, dir, "squad", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"载体", "c1", "小队", "sq", "executor"} {
		if !strings.Contains(out, want) {
			t.Fatalf("表格缺内容 %q：%s", want, out)
		}
	}
	out2, _, err := runLedgerCLI(t, dir, "squad", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	lines := nonEmptyLines(out2)
	if len(lines) != 2 {
		t.Fatalf("--json 应两行（载体+小队），得 %d：%s", len(lines), out2)
	}
	var head map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &head); err != nil {
		t.Fatalf("首行非 JSON: %v", err)
	}
	if head["name"] != "c1" {
		t.Fatalf("首行应是载体 c1：%s", lines[0])
	}
}
