package cmd

// squad 命令族回归（B156.3 K3 Task D）：命令级测试走既有 harness
// （newCardStepCLIEndpoint + runLedgerCLI，card_dispatch_test.go:77 /
// ledgercli_test.go:24），stub 服务端断言请求形状与服务端 400 报文的可行动渲染。
// 真实 gateway 链路的另一端由 Task C 的 wire 测试锁，两端拼起来即穿过完整
// JSON 边界。

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
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
		"--name", "coord", "--role", "coordinator", "--member", "c1")
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
	members, ok := in["members"].([]any)
	if in["role"] != "coordinator" || !ok || len(members) != 1 ||
		members[0].(map[string]any)["carrier"] != "c1" || in["max_concurrency"] != nil {
		t.Fatalf("请求体字段不符: %s", gotBody)
	}
	if !strings.Contains(out, `"version":1`) {
		t.Fatalf("stdout 应出机器 JSON: %s", out)
	}
	if !strings.Contains(errOut, "已登记小队 coord") {
		t.Fatalf("stderr 应有人话回执: %s", errOut)
	}
}

// TestSquadCreateParsesMemberPoliciesKeepsNamesExact 穿真实 Cobra create 接缝验收
// P1：重复 --member 保留完整成员名，冒号后的政策位进入成员对象，且不生成队级总帽。
func TestSquadCreateParsesMemberPoliciesKeepsNamesExact(t *testing.T) {
	dir := t.TempDir()
	var gotBody string
	stubSquadAgentd(t, dir, http.StatusOK, `{"name":"sq","version":1}`,
		func(_ *http.Request, body string) { gotBody = body })
	if _, _, err := runLedgerCLI(t, dir, "squad", "create",
		"--name", "sq", "--role", "executor", "--member", "c1:2",
		"--member", "c2", "--member", "A B/中文"); err != nil {
		t.Fatalf("合法成员政策应创建成功: %v", err)
	}
	var input struct {
		Role    string           `json:"role"`
		Members []map[string]any `json:"members"`
	}
	if err := json.Unmarshal([]byte(gotBody), &input); err != nil {
		t.Fatalf("请求体非 JSON: %s", gotBody)
	}
	if input.Role != "executor" || len(input.Members) != 3 ||
		input.Members[0]["carrier"] != "c1" || input.Members[0]["max_concurrency"] != float64(2) ||
		input.Members[1]["carrier"] != "c2" || input.Members[1]["max_concurrency"] != nil ||
		input.Members[2]["carrier"] != "A B/中文" || strings.Contains(gotBody, `"max_concurrency":0`) {
		t.Fatalf("成员对象/名字/队级总帽不符: %s", gotBody)
	}
}

// TestSquadCreateRejectsInvalidMemberPolicyBeforeDialing 锁本地 parser 的拒绝臂：
// 非空政策必须是正整数，且任何失败都发生在 newTargetClient 之前。
func TestSquadCreateRejectsInvalidMemberPolicyBeforeDialing(t *testing.T) {
	for _, raw := range []string{"c1:0", "c1:-1", "c1:1.5", "c1:abc", "c1:", ":2", "c1:2:3"} {
		t.Run(raw, func(t *testing.T) {
			dir := t.TempDir()
			dialed := false
			stubSquadAgentd(t, dir, http.StatusOK, `{}`, func(*http.Request, string) { dialed = true })
			_, _, err := runLedgerCLI(t, dir, "squad", "create", "--name", "sq",
				"--role", "executor", "--member", raw)
			if err == nil || !strings.Contains(err.Error(), "合法示例") || !strings.Contains(err.Error(), "--member") {
				t.Fatalf("非法政策 %q 应给合法示例，得 %v", raw, err)
			}
			if dialed {
				t.Fatal("非法成员政策不得拨号")
			}
		})
	}
}

// TestSquadCreateRejectsRemovedMaxConcurrency 锁旧队级 flag 不再注册，避免 CLI
// 继续把队级总帽发送到服务端。
func TestSquadCreateRejectsRemovedMaxConcurrency(t *testing.T) {
	dir := t.TempDir()
	dialed := false
	stubSquadAgentd(t, dir, http.StatusOK, `{}`, func(*http.Request, string) { dialed = true })
	_, _, err := runLedgerCLI(t, dir, "squad", "create", "--name", "sq", "--role", "executor", "--max-concurrency", "2")
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("旧队级 flag 应被 Cobra 拒绝，得 %v", err)
	}
	if dialed {
		t.Fatal("旧队级 flag 被拒后不得拨号")
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
	fixture := `{"carriers":[{"name":"c1","machine":"m1","cli":"opencode","home_dir":"/h","credential":"standalone","max_concurrency":2,"healthy":true,"version":1}],"squads":[{"name":"sq","role":"executor","members":[{"carrier":"c1"}],"version":1}]}`
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

// TestSquadListLogsShape 锁住 list 外部调用前后的结构化上下文：列表不修改数据，
// 但必须能说明成员/政策规模以及请求是否已拨号。
func TestSquadListLogsShape(t *testing.T) {
	dir := t.TempDir()
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	stubSquadAgentd(t, dir, http.StatusOK,
		`{"carriers":[{"name":"c1","machine":"m1","cli":"opencode","home_dir":"/h","credential":"standalone","max_concurrency":2,"healthy":true,"version":1}],"squads":[{"name":"sq","role":"executor","members":[{"carrier":"c1","max_concurrency":2}],"version":1}]}`,
		nil)
	if _, _, err := runLedgerCLI(t, dir, "squad", "list"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"squad.list", "carrier_count=1", "squad_count=1", "member_count=1", "policy_count=1", "dialed=true"} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("list 日志缺少 %q: %s", want, logs.String())
		}
	}
}

// setStub 是 squad set 的专用 stub：GET /api/squads 按 fixture 应答、PUT 交给
// put 处理——set 的编辑回路必须先 GET 读版本、再 PUT ?expect=写回，单一固定
// 响应体满足不了这两种请求。
func setStub(t *testing.T, dir, getFixture string, put func(w http.ResponseWriter, r *http.Request)) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/squads", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, getFixture)
	})
	mux.HandleFunc("/api/squads/squads/sq", put)
	newCardStepCLIEndpoint(t, dir, mux)
}

// TD-5：set 缺参——--name 必填，本地拒绝、不发起网络请求（否定断言的正控 =
// TD-7 证明同一命令参数合法时会真的 GET+PUT）。
func TestSquadSetRejectsMissingNameBeforeDialing(t *testing.T) {
	dir := t.TempDir()
	dialed := false
	stubSquadAgentd(t, dir, http.StatusOK, `{}`, func(*http.Request, string) { dialed = true })
	if _, _, err := runLedgerCLI(t, dir, "squad", "set"); err == nil ||
		!strings.Contains(err.Error(), "--name") {
		t.Fatalf("set 缺 --name 应本地拒绝并点名参数，得 %v", err)
	}
	if dialed {
		t.Fatal("参数校验未过不得发起请求")
	}
}

// TD-6：坏 expect——用户显式 --expect 原样带出，服务端 409（过期版本）时报文
// 可行动且点名出路。这钉住「expect 不被静默改写、冲突被透传」两条语义。
func TestSquadSetSurfacesStaleExpectConflict(t *testing.T) {
	dir := t.TempDir()
	var putQuery string
	setStub(t, dir,
		`{"carriers":[],"squads":[{"name":"sq","role":"executor","members":[],"version":1}]}`,
		func(w http.ResponseWriter, r *http.Request) {
			putQuery = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w,
				`{"error":"scheduling: 版本冲突：小队 sq 已被他人修改（expect=5），重试前先 handoff squad list"}`)
		})
	_, errOut, err := runLedgerCLI(t, dir, "squad", "set", "--name", "sq", "--expect", "5")
	if err == nil || !strings.Contains(err.Error(), "冲突") ||
		!strings.Contains(err.Error(), "handoff squad list") {
		t.Fatalf("坏 expect 的 409 应透传可行动文案，得 %v\nstderr=%s", err, errOut)
	}
	if putQuery != "expect=5" {
		t.Fatalf("用户显式 --expect 应原样带出，得 %s", putQuery)
	}
}

// TD-7：服务端 400 时可行动且含名字——set 不做本地词表校验（权威在服务端），
// 400 正文随错误透传、点名小队名与合法词表。
func TestSquadSetSurfacesActionableServerReject(t *testing.T) {
	dir := t.TempDir()
	setStub(t, dir,
		`{"carriers":[],"squads":[{"name":"sq","role":"executor","members":[],"version":1}]}`,
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"小队 sq 角色只能是 executor 或 coordinator"}`)
		})
	_, errOut, err := runLedgerCLI(t, dir, "squad", "set", "--name", "sq", "--role", "boss")
	if err == nil || !strings.Contains(err.Error(), "sq") ||
		!strings.Contains(err.Error(), "executor") {
		t.Fatalf("400 正文应随错误透传且含名字，得 %v\nstderr=%s", err, errOut)
	}
}

// TD-8：版本往返——编辑回路 GET 取 version → PUT 以 ?expect=该值写回（至少
// 一支穿过）；未给的字段保持现状、给的字段覆盖。
func TestSquadSetEditLoopUsesReadVersion(t *testing.T) {
	dir := t.TempDir()
	var gotMethod, gotPath, gotQuery, gotBody string
	var putCount int
	setStub(t, dir,
		`{"carriers":[],"squads":[{"name":"sq","role":"executor","members":[{"carrier":"c1"}],"version":3}]}`,
		func(w http.ResponseWriter, r *http.Request) {
			putCount++
			gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"name":"sq","version":4}`)
		})
	out, errOut, err := runLedgerCLI(t, dir, "squad", "set", "--name", "sq")
	if err != nil {
		t.Fatalf("set 失败: %v\nstderr=%s", err, errOut)
	}
	if putCount != 1 {
		t.Fatalf("编辑回路应恰一次 PUT，得 %d", putCount)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/squads/squads/sq" || gotQuery != "expect=3" {
		t.Fatalf("编辑回路应 GET 取版本后以 expect=3 写回，得 %s %s?%s", gotMethod, gotPath, gotQuery)
	}
	var in map[string]any
	if err := json.Unmarshal([]byte(gotBody), &in); err != nil {
		t.Fatalf("请求体非 JSON: %s", gotBody)
	}
	members, ok := in["members"].([]any)
	if in["role"] != "executor" || !ok || len(members) != 1 ||
		members[0].(map[string]any)["carrier"] != "c1" || in["max_concurrency"] != nil {
		t.Fatalf("未给字段应保持现状、给了字段应覆盖：%s", gotBody)
	}
	if !strings.Contains(out, `"version":4`) {
		t.Fatalf("stdout 应出机器 JSON 带新版本: %s", out)
	}
	if !strings.Contains(errOut, "已更新小队 sq") {
		t.Fatalf("stderr 应有人话回执: %s", errOut)
	}
}
