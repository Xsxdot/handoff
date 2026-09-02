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

	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
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

func setStepFirstStateTestWindow(t *testing.T) {
	t.Helper()
	oldTimeout := stepFirstStateTimeout
	oldPoll := stepFirstStatePollInterval
	stepFirstStateTimeout = 40 * time.Millisecond
	stepFirstStatePollInterval = time.Millisecond
	t.Cleanup(func() {
		stepFirstStateTimeout = oldTimeout
		stepFirstStatePollInterval = oldPoll
	})
}

func createStepTestCard(t *testing.T, dir, title string) string {
	t.Helper()
	out, _, err := runLedgerCLI(t, dir, "card", "add", title, "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatalf("建 card step 测试卡: %v", err)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatalf("解码 card step 测试卡: %v", err)
	}
	return card.ID
}

func appendStepDispatchForTest(t *testing.T, dir, cardID string, snap ledger.DispatchSnapshot) {
	t.Helper()
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("打开 card step 测试账本: %v", err)
	}
	defer st.Close()
	snap.Actor = "node:test"
	if err := st.RecordDispatch(cardID, snap); err != nil {
		t.Fatalf("写 dispatched 测试事件: %v", err)
	}
}

func appendStepDispatchFailureForTest(t *testing.T, dir, cardID, body string) {
	t.Helper()
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("打开 card step 失败账本: %v", err)
	}
	defer st.Close()
	if _, err := st.AddComment(cardID, body, "普通", "node:进行中"); err != nil {
		t.Fatalf("写派发失败 comment: %v", err)
	}
	if err := st.MarkNeedsHuman(cardID, "派发失败", "node:进行中"); err != nil {
		t.Fatalf("写派发失败 needs_human: %v", err)
	}
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
	restore := swapDispatchTransport(func(prompt, branch, target, project string) (string, string, error) {
		gotPrompt, gotProject = prompt, project
		return "T-fake-1", "", nil
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

	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	card, err := st.GetCard(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if card.Status != ledger.StatusTodo {
		t.Fatalf("裸 dispatch 不得挪列，实际 %q", card.Status)
	}
	if card.DriverSession == "" || strings.Contains(card.DriverSession, "#") ||
		!strings.HasPrefix(card.DriverSession, "cli:") {
		t.Fatalf("归属应为人尺度身份: %q", card.DriverSession)
	}
	st.Close()
	show, _, err := runLedgerCLI(t, dir, "card", "show", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show, "dispatched") || !strings.Contains(show, "discipline_name") {
		t.Fatalf("快照事件缺失: %q", show)
	}
}

// TestCardDispatchGuardFollowsOwnership 断言裸 dispatch 的守卫只看归属锁。
func TestCardDispatchGuardFollowsOwnership(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "守卫卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ClaimCard(c.ID, "cli:other@remote-host"); err != nil {
		t.Fatalf("预占: %v", err)
	}
	st.Close()
	restore := swapDispatchTransport(func(prompt, branch, target, project string) (string, string, error) {
		t.Fatal("他主持有时不应走到派发")
		return "", "", nil
	})
	defer restore()
	_, _, err = runLedgerCLI(t, dir, "card", "dispatch", c.ID,
		"--template", "feature-impl", "--target", "mac-02")
	if err == nil || !strings.Contains(err.Error(), "cli:other@remote-host") {
		t.Fatalf("他主持有应拒且点名持有者: %v", err)
	}
}

// TestCardDispatchSameOwnerReentryIdempotent 断言同一人换进程重入幂等。
func TestCardDispatchSameOwnerReentryIdempotent(t *testing.T) {
	dir := t.TempDir()
	// 合并 B229 后派发要先过拒发闸（目标机报能力位 + 账本有点名正文）。被测属性
	// 仍是「同一归属者重复派发不被认领拦下」，闸只是它的前置条件，不是被测对象。
	ct := newCaptureTarget(t, `{"disciplines_supported":true}`)
	writeCardDispatchConfig(t, dir, strings.TrimPrefix(ct.ts.URL, "http://"))
	out, _, err := runLedgerCLI(t, dir, "card", "add", "重入卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
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
	if _, err := st.PutDiscipline("implement", "实现角色正文"); err != nil {
		t.Fatalf("种纪律块: %v", err)
	}
	_ = st.Close()
	n := 0
	for i := 0; i < 2; i++ {
		restore := swapDispatchTransport(func(prompt, branch, target, project string) (string, string, error) {
			n++
			return fmt.Sprintf("T-reentry-%d", n), "", nil
		})
		if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", c.ID,
			"--template", "feature-impl", "--target", "fake-01"); err != nil {
			t.Fatalf("同人第 %d 次派发应放行: %v", i+1, err)
		}
		restore()
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
	restore := swapDispatchTransportWithOpts(func(req dispatchRequest) (string, string, error) {
		got = req
		return "T-cli-executor-model", "", nil
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
	setStepFirstStateTestWindow(t)
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

// 派发失败必须退归属；裸 dispatch 从不改变状态列。
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

	restore := swapDispatchTransport(func(prompt, branch, target, project string) (string, string, error) {
		return "", "", errors.New("起点在任务仓库中不存在")
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
	if !strings.Contains(show, `"status":"待办"`) && !strings.Contains(show, `"Status":"待办"`) {
		t.Fatalf("派发失败回滚不得动状态列: %q", show)
	}

	// 真正的判据：换一个会话（新进程即新会话）能立刻重派
	restore = swapDispatchTransport(func(prompt, branch, target, project string) (string, string, error) {
		return "T-retry-1", "", nil
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
	restore := swapDispatchTransportWithOpts(func(req dispatchRequest) (string, string, error) {
		got = req
		return "T-extra-1", "", nil
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
	setStepFirstStateTestWindow(t)
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
	setStepFirstStateTestWindow(t)
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

// TestCardDispatchStepReturnsImmediately verifies the 202 short-wait stdout contract instead of a runner outcome.
func TestCardDispatchStepReturnsImmediately(t *testing.T) {
	setStepFirstStateTestWindow(t)
	dir := t.TempDir()
	newCardStepCLIEndpoint(t, dir, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = cardStepBody(t, r)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	cardID := createStepTestCard(t, dir, "即时返回卡")
	out, _, err := runLedgerCLI(t, dir, "card", "dispatch", cardID, "--step", "进行中")
	if err != nil {
		t.Fatalf("card dispatch --step: %v", err)
	}
	for _, want := range []string{cardID, "进行中", "已受理", "handoff card wait " + cardID} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout = %q, want %q", out, want)
		}
	}
	if !strings.Contains(out, "首态未到") {
		t.Fatalf("无新首态时 stdout 必须说明短等结束: %q", out)
	}
	if strings.Contains(out, "Outcome") || strings.Contains(out, "T-") {
		t.Fatalf("stdout 不应包含旧 Outcome/task id：%q", out)
	}
}

func TestCardDispatchStepReportsNewDispatchFailure(t *testing.T) {
	setStepFirstStateTestWindow(t)
	dir := t.TempDir()
	cardID := createStepTestCard(t, dir, "新派发失败卡")
	appendStepDispatchForTest(t, dir, cardID, ledger.DispatchSnapshot{
		TaskID: "old-task", Branch: "cards/old", Base: "old-base",
		BaseCommit: "oldcommit123456789012345678901234567890", DisciplineName: "old-discipline",
	})
	const comment = "本节点派发失败：\n工作分支跨机：cause-42"
	newCardStepCLIEndpoint(t, dir, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = cardStepBody(t, r)
		appendStepDispatchFailureForTest(t, dir, cardID, comment)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	out, stderr, err := runLedgerCLI(t, dir, "card", "dispatch", cardID, "--step", "进行中")
	if err == nil {
		t.Fatal("水位之后的派发失败首态必须使 CLI 非零退出")
	}
	if !strings.Contains(stderr, comment) {
		t.Fatalf("stderr 必须包含 haltForHuman comment 正文 %q，实际 %q", comment, stderr)
	}
	if strings.Contains(out+stderr, "oldcomm") {
		t.Fatalf("不得把水位之前旧 dispatched 的短号打印成这次结果: out=%q stderr=%q", out, stderr)
	}
}

func TestCardDispatchStepReportsNewDispatchSnapshot(t *testing.T) {
	setStepFirstStateTestWindow(t)
	dir := t.TempDir()
	cardID := createStepTestCard(t, dir, "新派发成功卡")
	appendStepDispatchForTest(t, dir, cardID, ledger.DispatchSnapshot{
		TaskID: "old-task", Branch: "cards/old", Base: "old-base",
		BaseCommit: "oldcommit123456789012345678901234567890", DisciplineName: "old-discipline",
	})
	newCardStepCLIEndpoint(t, dir, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = cardStepBody(t, r)
		appendStepDispatchForTest(t, dir, cardID, ledger.DispatchSnapshot{
			Target: "", TaskID: "new-task", Branch: "cards/new", Base: "main",
			BaseCommit: "1234567890abcdef1234567890abcdef12345678", DisciplineName: "charter-implement",
		})
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	out, stderr, err := runLedgerCLI(t, dir, "card", "dispatch", cardID, "--step", "进行中")
	if err != nil {
		t.Fatalf("card dispatch --step: %v stderr=%q", err, stderr)
	}
	for _, want := range []string{cardID, "进行中", "本机", "cards/new", "main", "1234567", "charter-implement"} {
		if !strings.Contains(out, want) {
			t.Fatalf("成功首态 stdout = %q，缺少 %q", out, want)
		}
	}
	for _, forbidden := range []string{"oldcomm", "目标机未定", "本地 ref", "origin"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("成功首态 stdout 不应包含 %q: %q", forbidden, out)
		}
	}
}

func TestCardDispatchStepFormatsEmptyBaseCommit(t *testing.T) {
	setStepFirstStateTestWindow(t)
	dir := t.TempDir()
	cardID := createStepTestCard(t, dir, "空基线首态卡")
	newCardStepCLIEndpoint(t, dir, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = cardStepBody(t, r)
		appendStepDispatchForTest(t, dir, cardID, ledger.DispatchSnapshot{
			Target: "", TaskID: "empty-base-task", Branch: "cards/empty", Base: "",
			BaseCommit: "", DisciplineName: "charter-review",
		})
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	out, _, err := runLedgerCLI(t, dir, "card", "dispatch", cardID, "--step", "进行中")
	if err != nil {
		t.Fatalf("card dispatch --step: %v", err)
	}
	for _, want := range []string{"无起点分支", "无 sha", "cards/empty", "charter-review"} {
		if !strings.Contains(out, want) {
			t.Fatalf("空基线首态 stdout = %q，缺少 %q", out, want)
		}
	}
}

func TestCardDispatchStepExecutorWithoutTargetUsesLocalFirstState(t *testing.T) {
	setStepFirstStateTestWindow(t)
	dir := t.TempDir()
	var got map[string]json.RawMessage
	cardID := createStepTestCard(t, dir, "只覆盖执行器卡")
	newCardStepCLIEndpoint(t, dir, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = cardStepBody(t, r)
		appendStepDispatchForTest(t, dir, cardID, ledger.DispatchSnapshot{
			Target: "", TaskID: "executor-only-task", Branch: "cards/executor-only", Base: "main",
			BaseCommit: "abcdef0123456789abcdef0123456789abcdef01", DisciplineName: "charter-implement",
		})
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	out, _, err := runLedgerCLI(t, dir, "card", "dispatch", cardID, "--step", "进行中", "--executor", "codex")
	if err != nil {
		t.Fatalf("只覆盖 executor 的 card dispatch --step: %v", err)
	}
	if _, present := got["target"]; present {
		t.Fatalf("空 target 应保持缺席语义，wire 不应凭空写目标机：%v", got)
	}
	if gotExecutor := cardStepString(t, got, "executor"); gotExecutor != "codex" {
		t.Fatalf("executor = %q, want codex", gotExecutor)
	}
	if !strings.Contains(out, "本机") || strings.Contains(out, "目标机未定") {
		t.Fatalf("只覆盖 executor 仍应显示本机而非版本错文案: %q", out)
	}
}

// TestCardDispatchStepCarriesOverrides locks all four CLI override values at the local agentd wire.
func TestCardDispatchStepCarriesOverrides(t *testing.T) {
	setStepFirstStateTestWindow(t)
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

// TestCardDispatchStepUsesActorIdentity verifies step requests carry human-scale identity.
func TestCardDispatchStepUsesActorIdentity(t *testing.T) {
	setStepFirstStateTestWindow(t)
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
	if actor := cardStepString(t, got, "actor"); actor != ledgerActor() || strings.Contains(actor, "#") {
		t.Fatalf("wire actor = %q, want human-scale ledgerActor %q", actor, ledgerActor())
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

// TestCardReleaseRejectsNonHolderAndSucceedsForOwner 断言 release 的可见失败与闭环。
func TestCardReleaseRejectsNonHolderAndSucceedsForOwner(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "释放卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ClaimCard(c.ID, "cli:someone-else@far-away"); err != nil {
		t.Fatalf("预占: %v", err)
	}
	st.Close()
	_, stderr, err := runLedgerCLI(t, dir, "card", "release", c.ID)
	if err == nil {
		t.Fatal("非持有者 release 必须失败")
	}
	if !strings.Contains(stderr+err.Error(), "cli:someone-else@far-away") {
		t.Fatalf("失败报文必须点名持有者: stderr=%q err=%v", stderr, err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "takeover", c.ID); err != nil {
		t.Fatalf("takeover: %v", err)
	}
	stdout, _, err := runLedgerCLI(t, dir, "card", "release", c.ID)
	if err != nil || !strings.Contains(stdout, `{"ok":true}`) {
		t.Fatalf("持有者 release 应成功: %q %v", stdout, err)
	}
}

// TestCardTakeoverAssignsHumanIdentity 断言 takeover 的 from/to payload 仍可审计。
func TestCardTakeoverAssignsHumanIdentity(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "接管卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.ClaimCard(c.ID, "cli:prev@h1"); err != nil {
		t.Fatalf("预占: %v", err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "takeover", c.ID); err != nil {
		t.Fatalf("takeover: %v", err)
	}
	card, err := st.GetCard(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if card.DriverSession != ledgerActor() {
		t.Fatalf("takeover 后归属应是本 CLI 人尺度身份: %q want %q", card.DriverSession, ledgerActor())
	}
	events, err := st.EventsFromAsc([]string{c.ID}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.Type != ledger.EvDriverTakeover {
			continue
		}
		var payload struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatalf("payload 解码: %v", err)
		}
		if payload.From != "cli:prev@h1" || payload.To != ledgerActor() {
			t.Fatalf("takeover payload from/to = %q/%q", payload.From, payload.To)
		}
		found = true
	}
	if !found {
		t.Fatal("takeover 必须落 driver_takeover 事件")
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
	restore := swapDispatchTransportWithOpts(func(req dispatchRequest) (string, string, error) {
		called = true
		return "T-local-fallback", "", nil
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
	restore := swapDispatchTransportWithOpts(func(req dispatchRequest) (string, string, error) {
		got = req
		return "T-noextra-1", "", nil
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
	restore := swapDispatchTransportWithOpts(func(req dispatchRequest) (string, string, error) {
		got = req
		return "T-cli-discipline", "", nil
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

	restore := swapDispatchTransportWithOpts(func(req dispatchRequest) (string, string, error) {
		t.Fatal("拒发时不应发出任何派发请求")
		return "", "", nil
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

func TestCardDispatchWritesPointerLine(t *testing.T) {
	dir := t.TempDir()
	setupDisciplineGateFixture(t, dir, `{"disciplines_supported":true}`)
	out, _, err := runLedgerCLI(t, dir, "card", "add", "指针卡", "--project", "demo", "--workflow", "bug")
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
	restore := swapDispatchTransport(func(prompt, branch, target, project string) (string, string, error) {
		return "T-pointer-1", "", nil
	})
	defer restore()
	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", c.ID,
		"--template", "feature-impl", "--target", "mac-02"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	events, err := st.EventsFromAsc([]string{c.ID}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for i := range events {
		if events[i].Type != ledger.EvRoomMessage {
			continue
		}
		var msg proto.RoomMessage
		if err := json.Unmarshal(events[i].Payload, &msg); err != nil {
			continue
		}
		if msg.Kind != proto.RoomMsgPointer || !msg.BySystem {
			continue
		}
		if !strings.Contains(msg.Body, c.ID) || !strings.Contains(msg.Body, "feature-impl") {
			t.Fatalf("指针行正文应含卡号与模板名: %q", msg.Body)
		}
		found = true
	}
	if !found {
		t.Fatalf("账本里没有 kind=pointer ∧ by_system=true 的派发指针行: %+v", events)
	}
}

// TestCardDispatchPointerFailureDoesNotInterrupt 判据二（失败路径）：Pointer
// 返回错误时 dispatch 主流程不中断（错误仅日志）。与判据一分开写——只写这条
// 的话，一个「根本不调 Pointer」的实现也是绿的（判据一的牙齿在
// TestCardDispatchWritesPointerLine，两条合起来才拔不掉）。
func TestCardDispatchPointerFailureDoesNotInterrupt(t *testing.T) {
	dir := t.TempDir()
	setupDisciplineGateFixture(t, dir, `{"disciplines_supported":true}`)
	out, _, err := runLedgerCLI(t, dir, "card", "add", "指针失败卡", "--project", "demo", "--workflow", "bug")
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
	restoreTransport := swapDispatchTransport(func(prompt, branch, target, project string) (string, string, error) {
		return "T-pointer-fail", "", nil
	})
	defer restoreTransport()
	restorePointer := swapRoomPointer(func(_ *collab.Service, roomID, body string) error {
		return errors.New("指针写失败（测试注入）")
	})
	defer restorePointer()
	out, _, err = runLedgerCLI(t, dir, "card", "dispatch", c.ID,
		"--template", "feature-impl", "--target", "mac-02")
	if err != nil {
		t.Fatalf("Pointer 失败不应打断派发主流程: %v", err)
	}
	if !strings.Contains(out, "T-pointer-fail") {
		t.Fatalf("stdout 应含 task id: %q", out)
	}
}

type probeErrorCardTarget struct {
	ts         *httptest.Server
	dispatches int32
}

func newProbeErrorCardTarget(t *testing.T) *probeErrorCardTarget {
	t.Helper()
	target := &probeErrorCardTarget{}
	target.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("not-json"))
			return
		}
		if r.URL.Path == "/api/tasks" && r.Method == http.MethodPost {
			atomic.AddInt32(&target.dispatches, 1)
			http.Error(w, "probe failure test must not dispatch", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(target.ts.Close)
	return target
}

func setupCardDispatchProbeErrorFixture(t *testing.T, dir string, target *probeErrorCardTarget) {
	t.Helper()
	c := &config.Config{
		Listen: "127.0.0.1:0", Token: testToken, DataDir: dir, StallTimeout: 2 * time.Hour,
		Ledger: config.LedgerConfig{Enabled: true},
		Targets: map[string]config.Target{
			"mac-02": {Addr: strings.TrimPrefix(target.ts.URL, "http://"), Token: testToken},
		},
	}
	if err := config.Save(filepath.Join(dir, "config.yaml"), c); err != nil {
		t.Fatalf("写探活错误测试配置: %v", err)
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

func TestCardDispatchProbeFailureDoesNotClaimUnsupported(t *testing.T) {
	dir := t.TempDir()
	target := newProbeErrorCardTarget(t)
	setupCardDispatchProbeErrorFixture(t, dir, target)
	out, _, err := runLedgerCLI(t, dir, "card", "add", "探活错误卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &created); err != nil {
		t.Fatalf("解码建卡: %v", err)
	}
	_, errOut, err := runLedgerCLI(t, dir, "card", "dispatch", created.ID,
		"--template", "feature-impl", "--target", "mac-02")
	if err == nil {
		t.Fatal("Status 失败时模板卡派发必须返回错误")
	}
	joined := err.Error() + errOut
	if !strings.Contains(joined, "探活失败") || !strings.Contains(joined, "invalid character") {
		t.Fatalf("错误必须含探活语义和 cause：%s", joined)
	}
	if strings.Contains(joined, "升级到同批版本") {
		t.Fatalf("探活失败不得归因成版本升级：%s", joined)
	}
	if got := atomic.LoadInt32(&target.dispatches); got != 0 {
		t.Fatalf("探活失败不得发送任务，实际 %d 次", got)
	}
	show, _, err := runLedgerCLI(t, dir, "card", "show", created.ID)
	if err != nil {
		t.Fatalf("读回卡: %v", err)
	}
	var card struct {
		DriverSession string `json:"driver_session"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(show)), &card); err != nil {
		t.Fatalf("解码卡: %v", err)
	}
	if card.DriverSession != "" {
		t.Fatalf("探活失败不得认领卡，driver_session=%q", card.DriverSession)
	}
}
