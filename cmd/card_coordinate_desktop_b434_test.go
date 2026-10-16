// card_coordinate_desktop_b434_test.go —— B434.2：handoff card coordinate <id> --desktop
// （契约 §3.10、§4.16 条 252–260，用户裁决 P7=d）。
//
// 职责：httptest 假 agentd（按契约 §3.9 形状作答，G3 端点未落地期的对侧）记录每个请求，
// 锁：只调代开端点一次、零调 coordinator/launch、200 原样一行 JSON、非 2xx stderr 逐字
// 打印服务端 error 且无 JSON 外壳、CLI 不打 needs（既不发请求也不写本地账本）。
// 边界：端点自身行为归 G3。必须 env -u CLAUDE_CODE_SESSION_ID 下跑。
package cmd

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

// b434Req 是假 agentd 收到的一次请求。
type b434Req struct{ Method, Path, Body string }

// b434Recorder 收集假 agentd 收到的请求（handler 在服务端 goroutine 写、测试 goroutine 读）。
type b434Recorder struct {
	mu   sync.Mutex
	reqs []b434Req
}

func (r *b434Recorder) all() []b434Req {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]b434Req(nil), r.reqs...)
}

func (r *b434Recorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = nil
}

// b434RecordAgentd 起假 agentd（覆写 dir/config.yaml 指向它），按给定状态码/体作答并记录请求。
func b434RecordAgentd(t *testing.T, dir string, status int, body string) *b434Recorder {
	t.Helper()
	rec := &b434Recorder{}
	stubSquadAgentd(t, dir, status, body, func(r *http.Request, b string) {
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, b434Req{Method: r.Method, Path: r.URL.Path, Body: b})
		rec.mu.Unlock()
	})
	return rec
}

// 条 252、255：--desktop 是缺省 false 的 bool flag；不引入 --cli/--session。
func TestB434CardCoordinateDesktopFlag(t *testing.T) {
	f := cardCoordinateCmd.Flags().Lookup("desktop")
	if f == nil || f.Value.Type() != "bool" || f.DefValue != "false" {
		t.Fatalf("card coordinate 应注册缺省 false 的 bool --desktop，得 %+v", f)
	}
	if cardCoordinateCmd.Flags().Lookup("cli") != nil || cardCoordinateCmd.Flags().Lookup("session") != nil {
		t.Fatal("--desktop 不得引入 --cli/--session")
	}
	clearSeatSourceEnv(t)
	dir := t.TempDir()
	if _, _, err := runLedgerCLI(t, dir, "card", "coordinate", "B42", "--desktop", "--cli", "grok", "--session", "s"); err == nil ||
		!strings.Contains(err.Error(), "unknown flag: --cli") {
		t.Fatalf("--desktop 搭 --cli 应在 Cobra 层被拒: %v", err)
	}
}

// 条 253、254：恰一次 POST desktop-launch，体 {}；零调 coordinator/launch。
func TestB434CardCoordinateDesktopCallsLauncherEndpoint(t *testing.T) {
	clearSeatSourceEnv(t)
	dir := t.TempDir()
	rec := b434RecordAgentd(t, dir, http.StatusOK, `{"launcher":"local_l1","machine":"mac-02","handled_by":"mac-02"}`)
	if _, errOut, err := runLedgerCLI(t, dir, "card", "coordinate", "B42", "--desktop"); err != nil {
		t.Fatalf("coordinate --desktop: %v stderr=%s", err, errOut)
	}
	reqs := rec.all()
	if len(reqs) != 1 {
		t.Fatalf("假 agentd 应恰收到 1 次请求，得 %+v", reqs)
	}
	if r := reqs[0]; r.Method != http.MethodPost || r.Path != "/api/cards/B42/coordinator/desktop-launch" || r.Body != "{}" {
		t.Fatalf("请求 = %+v, want POST /api/cards/B42/coordinator/desktop-launch 体 {}", r)
	}
	for _, r := range reqs {
		if strings.HasSuffix(r.Path, "/coordinator/launch") {
			t.Fatalf("--desktop 不得调 coordinator/launch: %+v", r)
		}
	}
}

// 条 256：不带 --desktop 恰调 coordinator/launch、零调 desktop-launch。
func TestB434CardCoordinateWithoutDesktopUnchanged(t *testing.T) {
	clearSeatSourceEnv(t)
	dir := t.TempDir()
	rec := b434RecordAgentd(t, dir, http.StatusOK,
		`{"woke":true,"session_id":"sess-01","rebuilt":false,"escalated":false,"output":"ok"}`)
	if _, errOut, err := runLedgerCLI(t, dir, "card", "coordinate", "B42"); err != nil {
		t.Fatalf("coordinate: %v stderr=%s", err, errOut)
	}
	reqs := rec.all()
	if len(reqs) != 1 || reqs[0].Path != "/api/cards/B42/coordinator/launch" {
		t.Fatalf("不带 --desktop 应恰调 coordinator/launch 一次，得 %+v", reqs)
	}
	for _, r := range reqs {
		if strings.Contains(r.Path, "desktop-launch") {
			t.Fatalf("不带 --desktop 不得调 desktop-launch: %+v", r)
		}
	}
}

// 条 257 + 序列化边界（HTTP 体 → client 解码 → stdout 编码）：stdout 恰一行 JSON，解码为
// DesktopLauncherOpenResp 与服务端体相等；键集与值逐字相等（machine 空串必须出键，区分
// 「缺失」与「零值」），退出 0。proto↔TS 一侧由 V 卡 fixture 锁。
func TestB434CardCoordinateDesktopOK(t *testing.T) {
	clearSeatSourceEnv(t)
	const body = `{"launcher":"local_l1","machine":"","handled_by":"mac-01"}`
	dir := t.TempDir()
	b434RecordAgentd(t, dir, http.StatusOK, body)
	out, errOut, err := runLedgerCLI(t, dir, "card", "coordinate", "B42", "--desktop")
	if err != nil || ExitCode(err) != 0 {
		t.Fatalf("200 应退出 0: %v stderr=%s", err, errOut)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 1 {
		t.Fatalf("stdout 应恰一行，得 %q", out)
	}
	var got, want proto.DesktopLauncherOpenResp
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("stdout 不是 DesktopLauncherOpenResp JSON: %q: %v", lines[0], err)
	}
	if err := json.Unmarshal([]byte(body), &want); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("回执 = %+v, want %+v", got, want)
	}
	var gotRaw, wantRaw map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &gotRaw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(body), &wantRaw); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotRaw, wantRaw) {
		t.Fatalf("stdout 键值 = %v, want 与服务端体逐字相等 %v", gotRaw, wantRaw)
	}
}

// 条 258、260：409 无拉起器 → stderr 含 error 原文逐字、不含 {"error" 外壳、退出非零；
// 假 agentd 只收到这一次请求；本地账本零新事件（CLI 不打 needs）。
func TestB434CardCoordinateDesktop409NoLauncher(t *testing.T) {
	clearSeatSourceEnv(t)
	const msg = "无拉起器，请在桌面端开一场并登记"
	dir := t.TempDir()
	rec := b434RecordAgentd(t, dir, http.StatusConflict, `{"error":"`+msg+`"}`)
	id := mustAddCard(t, dir, "无拉起器卡")
	rec.reset()
	before := b434MaxSeq(t, dir)
	out, errOut, err := runLedgerCLI(t, dir, "card", "coordinate", id, "--desktop")
	if err == nil || ExitCode(err) == 0 {
		t.Fatalf("409 应非零退出: %v", err)
	}
	if !strings.Contains(errOut, msg) || strings.Contains(errOut, `{"error"`) {
		t.Fatalf("stderr 应含原文且无 JSON 外壳: %q", errOut)
	}
	// 契约 r10 第 6 条：断言面是 cobra 的错误行——stderr 末行逐字等于「Error: <服务端 error>」。
	if errLines := nonEmptyLines(errOut); len(errLines) == 0 || errLines[len(errLines)-1] != "Error: "+msg {
		t.Fatalf("stderr 末行应逐字为 %q，得 %q", "Error: "+msg, errOut)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("失败时 stdout 应为空: %q", out)
	}
	reqs := rec.all()
	if len(reqs) != 1 || !strings.HasSuffix(reqs[0].Path, "/coordinator/desktop-launch") {
		t.Fatalf("CLI 只该发这一次代开请求，得 %+v", reqs)
	}
	if after := b434MaxSeq(t, dir); after != before {
		t.Fatalf("CLI 不得在本地账本落任何事件（含 needs_human）: seq %d → %d", before, after)
	}
}

// 条 259、260：409 无本机位置 / 409 席位有人 / 502 投递失败 / 502 非 JSON → stderr 含服务端
// 原文、退出非零、只一次请求、零 needs 请求。
func TestB434CardCoordinateDesktopErrorPassthrough(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"409 无本机位置", http.StatusConflict, `{"error":"项目在本机没有登记位置：demo（handoff project add 登记后可用）"}`, "项目在本机没有登记位置：demo（handoff project add 登记后可用）"},
		{"409 席位有人", http.StatusConflict, `{"error":"卡 B42 已有人坐，换人用 handoff card rebind B42 --self 或 --launch"}`, "卡 B42 已有人坐，换人用 handoff card rebind B42 --self 或 --launch"},
		{"502 投递失败", http.StatusBadGateway, `{"error":"桌面端协调者唤醒失败：对端不可达"}`, "桌面端协调者唤醒失败：对端不可达"},
		{"502 非 JSON", http.StatusBadGateway, "upstream down", "upstream down"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearSeatSourceEnv(t)
			dir := t.TempDir()
			rec := b434RecordAgentd(t, dir, tc.status, tc.body)
			_, errOut, err := runLedgerCLI(t, dir, "card", "coordinate", "B42", "--desktop")
			if err == nil || ExitCode(err) == 0 {
				t.Fatalf("非 2xx 应非零退出: %v", err)
			}
			if !strings.Contains(errOut, tc.want) || strings.Contains(errOut, `{"error"`) {
				t.Fatalf("stderr 应含服务端原文 %q 且无 JSON 外壳: %q", tc.want, errOut)
			}
			reqs := rec.all()
			if len(reqs) != 1 {
				t.Fatalf("应恰一次请求，得 %+v", reqs)
			}
			for _, r := range reqs {
				if strings.Contains(r.Path, "needs") {
					t.Fatalf("CLI 不得发 needs 相关请求: %+v", r)
				}
			}
		})
	}
}
