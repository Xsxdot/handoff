package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
)

func TestResolveCardDispatchTemplateThreeStates(t *testing.T) {
	old := cardDispatchTemplate
	t.Cleanup(func() { cardDispatchTemplate = old })

	t.Run("zero templates points to template put", func(t *testing.T) {
		st, err := ledger.Open(t.TempDir() + "/ledger.db")
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		cardDispatchTemplate = ""
		_, err = resolveCardDispatchTemplate(st)
		if err == nil || !strings.Contains(err.Error(), "先用 template put") {
			t.Fatalf("零模板应指引先建模板: %v", err)
		}
	})

	t.Run("one template is selected", func(t *testing.T) {
		st, err := ledger.Open(t.TempDir() + "/ledger.db")
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if _, err := st.PutTemplate("only", ledger.TemplateDef{Executor: "codex", Purpose: ledger.PurposeImplement, Prompt: "x"}); err != nil {
			t.Fatal(err)
		}
		cardDispatchTemplate = ""
		got, err := resolveCardDispatchTemplate(st)
		if err != nil || got != "only" {
			t.Fatalf("唯一模板未自动采用: got=%q err=%v", got, err)
		}
	})

	t.Run("many templates require explicit name", func(t *testing.T) {
		st, err := ledger.Open(t.TempDir() + "/ledger.db")
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		for _, name := range []string{"alpha", "beta"} {
			if _, err := st.PutTemplate(name, ledger.TemplateDef{Executor: "codex", Purpose: ledger.PurposeImplement, Prompt: "x"}); err != nil {
				t.Fatal(err)
			}
		}
		cardDispatchTemplate = ""
		_, err = resolveCardDispatchTemplate(st)
		if err == nil || !strings.Contains(err.Error(), "显式指定 --template") ||
			!strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), "beta") {
			t.Fatalf("多模板应列选项并要求显式指定: %v", err)
		}
		cardDispatchTemplate = "beta"
		got, err := resolveCardDispatchTemplate(st)
		if err != nil || got != "beta" {
			t.Fatalf("显式模板应优先: got=%q err=%v", got, err)
		}
	})
}

func newCardStepCLIEndpoint(t *testing.T, dir string, handler http.Handler) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(handler)
	cfg := &config.Config{
		Listen: strings.TrimPrefix(ts.URL, "http://"), Token: testToken, DataDir: dir,
		StallTimeout: 2 * time.Hour, Ledger: config.LedgerConfig{Enabled: true},
	}
	if err := config.Save(filepath.Join(dir, "config.yaml"), cfg); err != nil {
		ts.Close()
		t.Fatalf("写 card step 测试配置: %v", err)
	}
	t.Cleanup(ts.Close)
	return ts
}

func cardStepBody(t *testing.T, r *http.Request) map[string]json.RawMessage {
	t.Helper()
	if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/api/cards/") || !strings.HasSuffix(r.URL.Path, "/step") {
		t.Fatalf("请求 = %s %s，want POST /api/cards/{id}/step", r.Method, r.URL.Path)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
		t.Fatalf("Authorization = %q, want Bearer %s", got, testToken)
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("解析 card step body: %v", err)
	}
	return body
}

func cardStepString(t *testing.T, body map[string]json.RawMessage, key string) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(body[key], &value); err != nil {
		t.Fatalf("字段 %s 不是字符串：%v", key, err)
	}
	return value
}

// setupDisciplineGateFixture 预写 B229 拒发闸前提：假目标机 mac-02 按 statusBody
// 应答 /api/status，模板点名的角色正文已入账本。裸卡派发自接线起在认领前过闸，
// 既有用例钉的是各自关注面，闸的前提在此统一满足。
func setupDisciplineGateFixture(t *testing.T, dir, statusBody string) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, statusBody)
	}))
	t.Cleanup(ts.Close)
	c := &config.Config{
		Listen: "127.0.0.1:0", Token: testToken, DataDir: dir, StallTimeout: 2 * time.Hour,
		Ledger: config.LedgerConfig{Enabled: true},
		Targets: map[string]config.Target{
			"mac-02": {Addr: strings.TrimPrefix(ts.URL, "http://"), Token: testToken},
		},
	}
	if err := config.Save(filepath.Join(dir, "config.yaml"), c); err != nil {
		t.Fatalf("写测试配置: %v", err)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.PutDiscipline("implement", "测试角色正文"); err != nil {
		t.Fatalf("种纪律块: %v", err)
	}
}

func TestCardDispatchClaimAndSnapshot(t *testing.T) {
	dir := t.TempDir()
	setupDisciplineGateFixture(t, dir, `{"disciplines_supported":true}`)

	out, _, err := runLedgerCLI(t, dir, "card", "add", "要派的卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "update", c.ID, "--accept", "测试全绿"); err != nil {
		t.Fatal(err)
	}

	var gotPrompt, gotProject string
	restore := swapDispatchTransport(func(prompt, branch, target, project string) (string, error) {
		gotPrompt, gotProject = prompt, project
		return "T-fake-1", nil
	})
	defer restore()

	out, _, err = runLedgerCLI(t, dir, "card", "dispatch", c.ID,
		"--template", "feature-impl", "--target", "mac-02", "--discipline-override", "implement")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !strings.Contains(out, "T-fake-1") {
		t.Fatalf("输出应含 task id: %q", out)
	}
	if strings.Contains(gotPrompt, "# 执行纪律") || !strings.Contains(gotPrompt, "要派的卡") ||
		!strings.Contains(gotPrompt, "测试全绿") {
		t.Fatalf("prompt 拼装: %q", gotPrompt)
	}
	if gotProject != "demo" {
		t.Fatalf("派发未带 project: %q", gotProject)
	}

	show, _, err := runLedgerCLI(t, dir, "card", "show", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show, `"Status":"进行中"`) && !strings.Contains(show, `"status":"进行中"`) {
		t.Fatalf("未认领: %q", show)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", c.ID, "--template", "feature-impl",
		"--target", "mac-02", "--discipline-override", "implement"); err == nil ||
		!strings.Contains(err.Error(), "认领") {
		t.Fatalf("重复派发应报已认领: %v", err)
	}
	if !strings.Contains(show, "dispatched") || !strings.Contains(show, "discipline_name") {
		t.Fatalf("快照事件缺失: %q", show)
	}
}

// TestCardDispatchExecutorModelFlags 穿过 card dispatch 无 step 路径的真实 CLI 接线，
// 并从账本 JSON 事件核对一次性覆盖确实落账。
func TestCardDispatchExecutorModelFlags(t *testing.T) {
	dir := t.TempDir()
	setupDisciplineGateFixture(t, dir, `{"disciplines_supported":true}`)
	out, _, err := runLedgerCLI(t, dir, "card", "add", "覆盖执行器的卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatal(err)
	}
	var got dispatchRequest
	restore := swapDispatchTransportWithOpts(func(req dispatchRequest) (string, error) {
		got = req
		return "T-cli-executor-model", nil
	})
	defer restore()
	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", card.ID,
		"--template", "feature-impl", "--target", "mac-02", "--executor", "grok", "--model", "grok-model"); err != nil {
		t.Fatalf("card dispatch: %v", err)
	}
	if got.executor != "grok" || got.model != "grok-model" {
		t.Fatalf("CLI 请求 executor/model = %q/%q, want %q/%q", got.executor, got.model, "grok", "grok-model")
	}
	show, _, err := runLedgerCLI(t, dir, "card", "show", card.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show, `"executor":"grok"`) || !strings.Contains(show, `"model":"grok-model"`) {
		t.Fatalf("dispatched 快照缺 executor/model: %q", show)
	}
}

// TestCardDispatchStepExecutorModelFlags 验证 --step 与模板路径共用同一对 CLI flag。
func TestCardDispatchStepExecutorModelFlags(t *testing.T) {
	dir := t.TempDir()
	var got map[string]json.RawMessage
	newCardStepCLIEndpoint(t, dir, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = cardStepBody(t, r)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	out, _, err := runLedgerCLI(t, dir, "card", "add", "step 覆盖执行器的卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", card.ID, "--step", "进行中",
		"--target", "mac-02", "--executor", "grok", "--model", "grok-model"); err != nil {
		t.Fatalf("card dispatch --step: %v", err)
	}
	if gotTarget := cardStepString(t, got, "target"); gotTarget != "mac-02" {
		t.Fatalf("--step 请求 target = %q, want mac-02", gotTarget)
	}
	if gotExecutor := cardStepString(t, got, "executor"); gotExecutor != "grok" {
		t.Fatalf("--step 请求 executor = %q, want grok", gotExecutor)
	}
	if gotModel := cardStepString(t, got, "model"); gotModel != "grok-model" {
		t.Fatalf("--step 请求 model = %q, want grok-model", gotModel)
	}
}

// 派发失败必须连驱动租约一起退：只退状态会让卡停在「待办但有主」，
// 而驱动身份带 pid——同一个人换个进程重试会被自己挡住 5 分钟。
func TestCardDispatchFailureReleasesLease(t *testing.T) {
	dir := t.TempDir()
	setupDisciplineGateFixture(t, dir, `{"disciplines_supported":true}`)
	out, _, err := runLedgerCLI(t, dir, "card", "add", "会派失败的卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}

	restore := swapDispatchTransport(func(prompt, branch, target, project string) (string, error) {
		return "", errors.New("起点在任务仓库中不存在")
	})
	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", c.ID,
		"--template", "feature-impl", "--target", "mac-02", "--discipline-override", "implement"); err == nil {
		t.Fatal("传输失败时派发应报错")
	}
	restore()

	show, _, err := runLedgerCLI(t, dir, "card", "show", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show, `"driver_session":""`) && strings.Contains(show, `"driver_session":"`) {
		t.Fatalf("派发失败后租约未释放: %q", show)
	}

	// 真正的判据：换一个会话（新进程即新会话）能立刻重派
	restore = swapDispatchTransport(func(prompt, branch, target, project string) (string, error) {
		return "T-retry-1", nil
	})
	defer restore()
	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", c.ID,
		"--template", "feature-impl", "--target", "mac-02", "--discipline-override", "implement"); err != nil {
		t.Fatalf("失败后重派应放行: %v", err)
	}
}

// TestCardDispatchExtraReachesPrompt 钉死 --extra 的正文真的落进 prompt。
// 这是协调者对**某一轮**说话的唯一通道：没有它就只能往工作分支塞提交，
// 而那条路会撞上 WorkBranch 取「最近一条非审阅 dispatched 快照」的三个坑。
func TestCardDispatchExtraReachesPrompt(t *testing.T) {
	dir := t.TempDir()
	setupDisciplineGateFixture(t, dir, `{"disciplines_supported":true}`)
	out, _, err := runLedgerCLI(t, dir, "card", "add", "带补充说明的卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatal(err)
	}
	var got dispatchRequest
	restore := swapDispatchTransportWithOpts(func(req dispatchRequest) (string, error) {
		got = req
		return "T-extra-1", nil
	})
	defer restore()
	const extra = "本轮只修 F1，不要重跑整卡"
	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", card.ID,
		"--template", "feature-impl", "--target", "mac-02", "--extra", extra); err != nil {
		t.Fatalf("card dispatch --extra: %v", err)
	}
	if !strings.Contains(got.prompt, extra) {
		t.Fatalf("prompt 里没有 --extra 正文:\n%s", got.prompt)
	}
	if !strings.Contains(got.prompt, "本次补充") {
		t.Fatalf("prompt 缺「本次补充」小节，执行者无从判断这段的身份:\n%s", got.prompt)
	}
}

// TestCardDispatchStepExtraReachesPrompt --step 与模板路径必须共用同一个 flag，
// 否则「给某一轮补一句话」在节点派发上仍然无解——而节点派发正是它最需要的地方。
func TestCardDispatchStepExtraReachesPrompt(t *testing.T) {
	dir := t.TempDir()
	var got map[string]json.RawMessage
	newCardStepCLIEndpoint(t, dir, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = cardStepBody(t, r)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	out, _, err := runLedgerCLI(t, dir, "card", "add", "step 带补充说明的卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatal(err)
	}
	const extra = "上一轮把审阅当成了实现，本轮只改 output.go"
	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", card.ID, "--step", "进行中",
		"--target", "mac-02", "--extra", extra); err != nil {
		t.Fatalf("card dispatch --step --extra: %v", err)
	}
	if gotExtra := cardStepString(t, got, "extra"); gotExtra != extra {
		t.Fatalf("--step 请求 extra = %q, want %q", gotExtra, extra)
	}
}

// TestCardDispatchStepSubmitsToLocalAgentd verifies the step request uses the local endpoint and real client wire.
func TestCardDispatchStepSubmitsToLocalAgentd(t *testing.T) {
	dir := t.TempDir()
	var got map[string]json.RawMessage
	newCardStepCLIEndpoint(t, dir, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = cardStepBody(t, r)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	out, _, err := runLedgerCLI(t, dir, "card", "add", "本机 agentd 卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", card.ID, "--step", "进行中"); err != nil {
		t.Fatalf("card dispatch --step: %v", err)
	}
	if gotStep := cardStepString(t, got, "step"); gotStep != "进行中" {
		t.Fatalf("step = %q, want 进行中", gotStep)
	}
}

// TestCardDispatchStepReturnsImmediately verifies the 202 stdout contract instead of a runner outcome.
func TestCardDispatchStepReturnsImmediately(t *testing.T) {
	dir := t.TempDir()
	newCardStepCLIEndpoint(t, dir, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = cardStepBody(t, r)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	out, _, err := runLedgerCLI(t, dir, "card", "add", "即时返回卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatal(err)
	}
	out, _, err = runLedgerCLI(t, dir, "card", "dispatch", card.ID, "--step", "进行中")
	if err != nil {
		t.Fatalf("card dispatch --step: %v", err)
	}
	for _, want := range []string{card.ID, "进行中", "handoff card wait " + card.ID} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout = %q, want %q", out, want)
		}
	}
	if strings.Contains(out, "Outcome") || strings.Contains(out, "T-") {
		t.Fatalf("stdout 不应包含旧 Outcome/task id：%q", out)
	}
}

// TestCardDispatchStepCarriesOverrides locks all four CLI override values at the local agentd wire.
func TestCardDispatchStepCarriesOverrides(t *testing.T) {
	dir := t.TempDir()
	var got map[string]json.RawMessage
	newCardStepCLIEndpoint(t, dir, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = cardStepBody(t, r)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	out, _, err := runLedgerCLI(t, dir, "card", "add", "四项覆盖卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", card.ID, "--step", "进行中",
		"--target", "mac-02", "--executor", "grok", "--model", "grok-model", "--extra", "本轮只修 F1"); err != nil {
		t.Fatalf("card dispatch --step: %v", err)
	}
	for key, want := range map[string]string{"target": "mac-02", "executor": "grok", "model": "grok-model", "extra": "本轮只修 F1"} {
		if got := cardStepString(t, got, key); got != want {
			t.Fatalf("wire %s = %q, want %q", key, got, want)
		}
	}
}

// TestCardDispatchStepUsesPIDActor verifies step requests carry the per-process CLI session.
func TestCardDispatchStepUsesPIDActor(t *testing.T) {
	dir := t.TempDir()
	var got map[string]json.RawMessage
	newCardStepCLIEndpoint(t, dir, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = cardStepBody(t, r)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	out, _, err := runLedgerCLI(t, dir, "card", "add", "PID actor 卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", card.ID, "--step", "进行中"); err != nil {
		t.Fatalf("card dispatch --step: %v", err)
	}
	if actor := cardStepString(t, got, "actor"); actor != ledgerSession() {
		t.Fatalf("wire actor = %q, want ledgerSession %q", actor, ledgerSession())
	}
}

// TestCardDispatchStepRejectsPlan rejects local files before endpoint lookup or HTTP.
func TestCardDispatchStepRejectsPlan(t *testing.T) {
	dir := t.TempDir()
	var requests int32
	newCardStepCLIEndpoint(t, dir, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	out, _, err := runLedgerCLI(t, dir, "card", "add", "拒绝 plan 卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", card.ID, "--step", "进行中", "--plan", "local.md"); err == nil || !strings.Contains(err.Error(), "不会被上传") {
		t.Fatalf("--plan 应在发送前拒绝，err=%v", err)
	}
	if got := atomic.LoadInt32(&requests); got != 0 {
		t.Fatalf("--plan 拒绝前不应发 HTTP，请求数=%d", got)
	}
}

// TestCardDispatchStepNoLocalFallback ensures endpoint failure does not invoke the old local transport seam.
func TestCardDispatchStepNoLocalFallback(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Listen: "127.0.0.1:1", Token: testToken, DataDir: dir,
		StallTimeout: 2 * time.Hour, Ledger: config.LedgerConfig{Enabled: true},
		Targets: map[string]config.Target{"mac-02": {Addr: "127.0.0.1:1", Token: "remote-token"}},
	}
	if err := config.Save(filepath.Join(dir, "config.yaml"), cfg); err != nil {
		t.Fatalf("写不可达测试配置: %v", err)
	}
	out, _, err := runLedgerCLI(t, dir, "card", "add", "不回落卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatal(err)
	}
	called := false
	restore := swapDispatchTransportWithOpts(func(req dispatchRequest) (string, error) {
		called = true
		return "T-local-fallback", nil
	})
	defer restore()
	_, _, err = runLedgerCLI(t, dir, "card", "dispatch", card.ID, "--step", "进行中", "--target", "mac-02")
	if err == nil || !strings.Contains(err.Error(), "够不着") {
		t.Fatalf("本机 agentd 不可达应失败，err=%v", err)
	}
	if called {
		t.Fatal("本机 agentd 不可达时不应调用旧本地 transport")
	}
}

// TestCardDispatchWithoutExtraHasNoSupplementSection 空值不得留下空小节：
// 「## 本次补充」后面跟一片空白比没有更让执行者困惑（同 {{ACCEPT}} 的既有取舍）。
func TestCardDispatchWithoutExtraHasNoSupplementSection(t *testing.T) {
	dir := t.TempDir()
	setupDisciplineGateFixture(t, dir, `{"disciplines_supported":true}`)
	out, _, err := runLedgerCLI(t, dir, "card", "add", "不带补充的卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatal(err)
	}
	var got dispatchRequest
	restore := swapDispatchTransportWithOpts(func(req dispatchRequest) (string, error) {
		got = req
		return "T-noextra-1", nil
	})
	defer restore()
	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", card.ID,
		"--template", "feature-impl", "--target", "mac-02"); err != nil {
		t.Fatalf("card dispatch: %v", err)
	}
	if strings.Contains(got.prompt, "本次补充") {
		t.Fatalf("未传 --extra 时不应出现「本次补充」小节:\n%s", got.prompt)
	}
}

// writeCardDispatchConfig 预写一份带假目标机的配置（runLedgerCLI 见文件存在即跳过）。
// B229 起 card dispatch 在认领前过拒发闸：账本要有点名正文、目标机要报支持能力位。
func writeCardDispatchConfig(t *testing.T, dir, addr string) {
	t.Helper()
	c := &config.Config{
		Listen: "127.0.0.1:0", Token: testToken, DataDir: dir, StallTimeout: 2 * time.Hour,
		Ledger: config.LedgerConfig{Enabled: true},
		Targets: map[string]config.Target{
			"fake-01": {Addr: addr, Token: testToken},
		},
	}
	if err := config.Save(filepath.Join(dir, "config.yaml"), c); err != nil {
		t.Fatalf("写测试配置: %v", err)
	}
}

// TestCardDispatchDeliversResolvedDiscipline CLI 模板派发的缝 1 接线：认领之前
// 经 PreflightDiscipline + ResolveDispatch 解析好正文三元组，随请求上 wire，
// 版本号落 dispatched 快照。
func TestCardDispatchDeliversResolvedDiscipline(t *testing.T) {
	dir := t.TempDir()
	ct := newCaptureTarget(t, `{"disciplines_supported":true}`)
	writeCardDispatchConfig(t, dir, strings.TrimPrefix(ct.ts.URL, "http://"))

	out, _, err := runLedgerCLI(t, dir, "card", "add", "缝1接线卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatal(err)
	}
	// 种模板与账本正文（feature-impl 点名 implement）。
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutTemplate("feature-impl", ledger.TemplateDef{
		Executor: "opencode", Purpose: ledger.PurposeImplement, BranchPrefix: "cards",
		Discipline: "implement", Prompt: "实现 {{TITLE}}",
	}); err != nil {
		t.Fatalf("种模板: %v", err)
	}
	if _, err := st.PutDiscipline("implement", "实现角色正文B229MARKER"); err != nil {
		t.Fatalf("种纪律块: %v", err)
	}
	_ = st.Close()

	var got dispatchRequest
	restore := swapDispatchTransportWithOpts(func(req dispatchRequest) (string, error) {
		got = req
		return "T-cli-discipline", nil
	})
	defer restore()
	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", card.ID,
		"--template", "feature-impl", "--target", "fake-01"); err != nil {
		t.Fatalf("card dispatch: %v", err)
	}
	if !strings.Contains(got.disciplineText, "平台不变量") ||
		!strings.Contains(got.disciplineText, "实现角色正文B229MARKER") {
		t.Fatalf("wire 的正文应是平台层+角色层组装产物，实得前 80 字节: %q",
			truncateBytesForTest(got.disciplineText, 80))
	}
	if got.disciplineVersion != 1 {
		t.Fatalf("wire discipline_version = %d, want 1", got.disciplineVersion)
	}
	show, _, err := runLedgerCLI(t, dir, "card", "show", card.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show, `"discipline_version":1`) {
		t.Fatalf("dispatched 快照应含 discipline_version=1: %s", show)
	}
}

// TestCardDispatchRefusesUnsupportedTargetBeforeClaim 目标机不支持时在认领之前
// 拒发：卡留在原状态（无半状态），错误文案可行动。
func TestCardDispatchRefusesUnsupportedTargetBeforeClaim(t *testing.T) {
	dir := t.TempDir()
	ct := newCaptureTarget(t, `{}`)
	writeCardDispatchConfig(t, dir, strings.TrimPrefix(ct.ts.URL, "http://"))

	out, _, err := runLedgerCLI(t, dir, "card", "add", "拒发零残留卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatal(err)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutTemplate("feature-impl", ledger.TemplateDef{
		Executor: "opencode", Purpose: ledger.PurposeImplement, BranchPrefix: "cards",
		Discipline: "implement", Prompt: "实现 {{TITLE}}",
	}); err != nil {
		t.Fatalf("种模板: %v", err)
	}
	if _, err := st.PutDiscipline("implement", "不该被下发"); err != nil {
		t.Fatalf("种纪律块: %v", err)
	}
	_ = st.Close()

	restore := swapDispatchTransportWithOpts(func(req dispatchRequest) (string, error) {
		t.Fatal("拒发时不应发出任何派发请求")
		return "", nil
	})
	defer restore()
	_, _, err = runLedgerCLI(t, dir, "card", "dispatch", card.ID,
		"--template", "feature-impl", "--target", "fake-01")
	if err == nil || !strings.Contains(err.Error(), "升级") {
		t.Fatalf("应在认领前拒发并给升级指引: %v", err)
	}
	if n := ct.tasks(); n != 0 {
		t.Fatalf("目标机不应收到任务请求，实际 %d 次", n)
	}
	show, _, serr := runLedgerCLI(t, dir, "card", "show", card.ID)
	if serr != nil {
		t.Fatal(serr)
	}
	if strings.Contains(show, `"status":"进行中"`) || strings.Contains(show, `"Status":"进行中"`) {
		t.Fatalf("拒发不得留下已认领的半状态: %s", show)
	}
}
