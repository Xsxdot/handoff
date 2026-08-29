package agentd

// coordapi_test.go —— 协调者生命周期三端点的缝级测试（B156.3 K4）。
// 全部断言穿过 HTTP 层（ledgerPost/ledgerGet），组装链穿过真实编制域
// （SquadRows→LaunchAdmit→Carrier），keystone 侧注入 fake 端口记录 spec。

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// fakeCoordRunner 记录 Launch 收到的 SessionSpec 与次数（防「spec 回潮」的 agentd
// 侧观测点）；failLaunch 置真时 Launch 报错——source 只进审计与错误包装，承载失败
// 的错误原文是「source 进 LaunchForCard」的唯一可观测面。
type fakeCoordRunner struct {
	launches   []keysclient.SessionSpec
	resumes    []string
	failLaunch bool
}

func (r *fakeCoordRunner) Launch(spec keysclient.SessionSpec, prompt string) (keysclient.TurnResult, error) {
	r.launches = append(r.launches, spec)
	if r.failLaunch {
		return keysclient.TurnResult{}, errors.New("承载不可用")
	}
	return keysclient.TurnResult{SessionID: "sess-coord", Output: "ok"}, nil
}

func (r *fakeCoordRunner) Resume(ref keysclient.SessionRef, prompt string) (keysclient.TurnResult, error) {
	r.resumes = append(r.resumes, prompt)
	return keysclient.TurnResult{SessionID: ref.SessionID, Output: "ok"}, nil
}

// fakeCoordNarrator 记录叙事文本（keystone 出站缝③ 替身）。
type fakeCoordNarrator struct{ lines []string }

func (n *fakeCoordNarrator) Say(cardID, text string) error {
	n.lines = append(n.lines, text)
	return nil
}

// newCoordEnv 装配真实编制域（SetupAutomation 全真接线：facadeAsRegistry +
// scheduling.Service）+ 注入 fake 端口的真实 keystone（fakeCoordRunner 记录 spec）。
// launch 的 SessionSpec 组装必须真实穿过编制域解析（SquadRows→LaunchAdmit→Carrier）。
func newCoordEnv(t *testing.T) (*ledgerEnv, *fakeCoordRunner) {
	t.Helper()
	env := newLedgerEnv(t)
	env.srv.SetupAutomation(env.ledger)
	runner := &fakeCoordRunner{}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	return env, runner
}

func newNoPTYCoordEnv(t *testing.T) (*ledgerEnv, *fakeCoordRunner) {
	t.Helper()
	env := newNoPTYLedgerEnv(t)
	env.srv.SetupAutomation(env.ledger)
	runner := &fakeCoordRunner{}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	return env, runner
}

// putOnlineCarrier 迁移旧 Healthy=true 夹具：PutCarrier 新建仍按契约落 pending，
// 测试若要模拟旧夹具的已上线事实，必须再经过真实检测写回缝。
func putOnlineCarrier(t *testing.T, svc *scheduling.Service, carrier scheduling.Carrier) {
	t.Helper()
	carrier.Status = scheduling.StatusOnline
	if err := svc.PutCarrier(carrier, 0); err != nil {
		t.Fatalf("登记载体 %s: %v", carrier.Name, err)
	}
	if _, err := svc.ApplyDetect(carrier.Name, scheduling.DetectEvidence{Reachable: true}, ""); err != nil {
		t.Fatalf("设置载体 %s online: %v", carrier.Name, err)
	}
}

func setCoordForwardTarget(t *testing.T, env *ledgerEnv, name, addr string) {
	t.Helper()
	env.srv.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	if err := env.srv.swapConf(func(cfg *config.Config) error {
		cfg.Targets = map[string]config.Target{
			name: {Addr: addr, Token: testToken},
		}
		return nil
	}); err != nil {
		t.Fatalf("登记转发 target: %v", err)
	}
}

// createCoordCard 建一张协调者测试卡（project=handoff，attachment-gates 流）。
func createCoordCard(t *testing.T, env *ledgerEnv) string {
	t.Helper()
	card, err := env.ledger.CreateCard(ledger.NewCard{
		Title: "协调者生命周期卡", Project: "handoff", Workflow: "attachment-gates", Actor: "test"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	return card.ID
}

// seedCoordinatorSquad 登记协调者载体 + 小队（物理位/政策位各 1）。
func seedCoordinatorSquad(t *testing.T, env *ledgerEnv) {
	t.Helper()
	svc := env.srv.Scheduling()
	putOnlineCarrier(t, svc, scheduling.Carrier{Name: "c1", Machine: "linux-01",
		CLI: "opencode", HomeDir: "/home/coordinator",
		Credential: scheduling.CredentialStandalone, Status: scheduling.StatusOnline})
	if err := svc.PutSquad(scheduling.Squad{Name: "coord", Role: scheduling.RoleCoordinator,
		Members: []string{"c1"}, MaxConcurrency: 1}, 0); err != nil {
		t.Fatalf("登记协调者小队: %v", err)
	}
}

// TestCoordLaunchEndpointSuccess 缝⑤×缝①×缝②：一键拉起（source=manual）走真实
// 编制域解析出非空 SessionSpec（CLI/HomeDir/Workdir 逐一断言——钉澄清 2，防空
// spec 回潮）；拉起恰一次 Launch；两级计数各 +1；全程不产生 task。
func TestCoordLaunchEndpointSuccess(t *testing.T) {
	env, runner := newCoordEnv(t)
	seedCoordinatorSquad(t, env)
	if err := env.st.CreateProjectLocation(&proto.ProjectLocation{
		Name: "handoff", Path: "/repo/handoff"}); err != nil {
		t.Fatalf("登记项目位置: %v", err)
	}
	cardID := createCoordCard(t, env)

	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/launch",
		`{"source":"manual"}`)
	if code != http.StatusOK {
		t.Fatalf("launch 状态=%d body=%s", code, body)
	}
	var resp struct {
		Woke      bool   `json:"woke"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil || !resp.Woke || resp.SessionID != "sess-coord" {
		t.Fatalf("launch 响应异常: %s err=%v", body, err)
	}
	if len(runner.launches) != 1 {
		t.Fatalf("Launch 次数=%d，want 恰 1", len(runner.launches))
	}
	got := runner.launches[0]
	if got.CLI != "opencode" || got.HomeDir != "/home/coordinator" {
		t.Fatalf("spec 回潮：CLI/HomeDir 必须由编制域解析出来，got=%+v", got)
	}
	if got.Workdir != "/repo/handoff" {
		t.Fatalf("工作目录应解析为项目位置根，got=%q", got.Workdir)
	}
	facade := env.srv.autoLedger
	for key, want := range map[string]int{"squad/coord": 0, "carrier/c1": 0} {
		if n := runningCountIn(t, facade, key); n != want {
			t.Fatalf("计数 %s=%d，want %d", key, n, want)
		}
	}
	// 铁律：拉起路径全程不产生 task（竖切断言延伸到 gateway 层）。
	links, err := env.ledger.TasksOf(cardID)
	if err != nil || len(links) != 0 {
		t.Fatalf("拉起不应产生 task：links=%v err=%v", links, err)
	}
}

// TestCoordLaunchSourceFlowsIntoKeystone 缝⑤×缝②：source 参数原样进 LaunchForCard
// （来源只进审计与错误包装——承载失败时错误原文含「来源 <source>」）。两值各验一遍，
// 每次拉起恰触发一次 Launch。
func TestCoordLaunchSourceFlowsIntoKeystone(t *testing.T) {
	env, runner := newCoordEnv(t)
	seedCoordinatorSquad(t, env)
	runner.failLaunch = true
	cardID := createCoordCard(t, env)

	for _, tc := range []struct{ src, want string }{
		{"manual", "来源 manual"},
		{"card_create", "来源 card_create"},
	} {
		code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/launch",
			`{"source":"`+tc.src+`"}`)
		if code != http.StatusBadGateway {
			t.Fatalf("[%s] 承载失败应 502，状态=%d body=%s", tc.src, code, body)
		}
		if !strings.Contains(body, tc.want) {
			t.Fatalf("[%s] 来源未进 LaunchForCard：body=%s", tc.src, body)
		}
	}
	if len(runner.launches) != 2 {
		t.Fatalf("两次拉起应各触发恰一次 Launch，实际 %d", len(runner.launches))
	}
}

// TestCoordLaunchNoSquadActionableError 缝⑤×缝①：未登记协调者小队时 400 且报文
// 含指路文案与最小参数示例（岔口四 B 附加约束）。执行者小队在场证明过滤按 role 生效。
func TestCoordLaunchNoSquadActionableError(t *testing.T) {
	env, _ := newCoordEnv(t)
	svc := env.srv.Scheduling()
	putOnlineCarrier(t, svc, scheduling.Carrier{Name: "e1", Machine: "linux-01",
		CLI: "opencode", Credential: scheduling.CredentialStandalone,
		Status: scheduling.StatusOnline})
	if err := svc.PutSquad(scheduling.Squad{Name: "exec", Role: scheduling.RoleExecutor,
		Members: []string{"e1"}}, 0); err != nil {
		t.Fatal(err)
	}
	cardID := createCoordCard(t, env)
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/launch", `{}`)
	if code != http.StatusBadRequest {
		t.Fatalf("无协调者小队应 400，状态=%d body=%s", code, body)
	}
	for _, want := range []string{"handoff squad create", "--role coordinator", "--member"} {
		if !strings.Contains(body, want) {
			t.Fatalf("指路文案缺 %q：%s", want, body)
		}
	}
}

// TestCoordLaunchAmbiguousSquadConflict 缝⑤×缝①：≥2 个协调者小队 → 409，歧义
// 错误逐一点名候选。
func TestCoordLaunchAmbiguousSquadConflict(t *testing.T) {
	env, _ := newCoordEnv(t)
	svc := env.srv.Scheduling()
	for _, c := range []scheduling.Carrier{
		{Name: "c1", Machine: "m1", CLI: "opencode", Credential: scheduling.CredentialStandalone, Status: scheduling.StatusOnline},
		{Name: "c2", Machine: "m2", CLI: "opencode", Credential: scheduling.CredentialStandalone, Status: scheduling.StatusOnline},
	} {
		putOnlineCarrier(t, svc, c)
	}
	for _, q := range []scheduling.Squad{
		{Name: "coord-a", Role: scheduling.RoleCoordinator, Members: []string{"c1"}},
		{Name: "coord-b", Role: scheduling.RoleCoordinator, Members: []string{"c2"}},
	} {
		if err := svc.PutSquad(q, 0); err != nil {
			t.Fatal(err)
		}
	}
	cardID := createCoordCard(t, env)
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/launch", `{}`)
	if code != http.StatusConflict {
		t.Fatalf("多协调者小队应 409，状态=%d body=%s", code, body)
	}
	for _, want := range []string{"coord-a", "coord-b"} {
		if !strings.Contains(body, want) {
			t.Fatalf("歧义错误应点名候选，缺 %q：%s", want, body)
		}
	}
}

// TestCoordAttachTakeoverMutesWake 缝⑤×缝②：attach 端点置 SetAttach(card,true) 后
// Decide 对一切种类恒判不醒（竖切已有域内断言，这里穿过 HTTP 层的集成断言），
// 交回后恢复。定位三元组经真实 attachLocator 产出。
func TestCoordAttachTakeoverMutesWake(t *testing.T) {
	env, _ := newCoordEnv(t)
	seedCoordinatorSquad(t, env)
	cardID := createCoordCard(t, env)
	if code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/launch", `{}`); code != http.StatusOK {
		t.Fatalf("预拉起失败：%d %s", code, body)
	}
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/attach",
		`{"active":true,"workdir":"/repo/handoff"}`)
	if code != http.StatusOK {
		t.Fatalf("attach 接管应 200，状态=%d body=%s", code, body)
	}
	var info struct {
		Machine string `json:"machine"`
		Dir     string `json:"dir"`
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(body), &info); err != nil {
		t.Fatalf("attach 响应解析失败: %v body=%s", err, body)
	}
	if info.Dir != "/repo/handoff" || !strings.Contains(info.Command, "sess-coord") {
		t.Fatalf("定位三元组异常: %+v", info)
	}
	if d := env.srv.keystone.Decide(keystone.WakeEvent{Kind: keystone.WakeTaskTerminal, Card: cardID}); d.Wake {
		t.Fatal("attach 接管中自动唤醒不应发生（穿过 HTTP 的互斥断言）")
	}
	if code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/attach", `{"active":false}`); code != http.StatusOK {
		t.Fatalf("attach 交回应 200，状态=%d body=%s", code, body)
	}
	if d := env.srv.keystone.Decide(keystone.WakeEvent{Kind: keystone.WakeTaskTerminal, Card: cardID}); !d.Wake {
		t.Fatal("交回后自动唤醒应恢复")
	}
}

// TestCoordStatusEndpoint 缝⑤×缝②：GET coordinator 的绑定/接管态三态推进
// （未拉 → bound=false；拉起 → bound=true；接管 → attach_active=true）。
func TestCoordStatusEndpoint(t *testing.T) {
	env, _ := newNoPTYCoordEnv(t)
	seedCoordinatorSquad(t, env)
	if err := env.st.CreateProjectLocation(&proto.ProjectLocation{
		Name: "handoff", Path: "/repo/handoff"}); err != nil {
		t.Fatalf("登记项目位置: %v", err)
	}
	cardID := createCoordCard(t, env)

	code, body := ledgerGet(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator")
	if code != http.StatusOK {
		t.Fatalf("GET coordinator 状态=%d body=%s", code, body)
	}
	for _, want := range []string{`"bound":false`, `"attach_active":false`} {
		if !strings.Contains(body, want) {
			t.Fatalf("未绑定态缺 %s：%s", want, body)
		}
	}
	var status proto.CoordinatorStatus
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Fatalf("未绑定态解析失败: %v body=%s", err, body)
	}
	if status.Bound || status.AttachActive || status.Attach != nil {
		t.Fatalf("未绑定态异常: %+v", status)
	}
	if !strings.Contains(body, `"attach":null`) {
		t.Fatalf("未绑定态必须显式返回 attach:null: %s", body)
	}
	if code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/launch", `{}`); code != http.StatusOK {
		t.Fatalf("拉起失败：%d %s", code, body)
	}
	code, body = ledgerGet(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator")
	if code != http.StatusOK || !strings.Contains(body, `"bound":true`) || strings.Contains(body, `"attach_active":true`) {
		t.Fatalf("绑定态异常：%d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Fatalf("绑定态解析失败: %v body=%s", err, body)
	}
	if status.Attach == nil || status.Attach.Dir != "/repo/handoff" || status.Attach.Machine != "" {
		t.Fatalf("绑定态定位三元组异常: %+v", status.Attach)
	}
	if !strings.Contains(body, `"machine":""`) {
		t.Fatalf("绑定态必须保留 machine 空串: %s", body)
	}
	if code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/attach", `{"active":true}`); code != http.StatusOK {
		t.Fatalf("attach 失败：%d %s", code, body)
	}
	code, body = ledgerGet(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator")
	if code != http.StatusOK || !strings.Contains(body, `"attach_active":true`) {
		t.Fatalf("接管态未反映：%d %s", code, body)
	}
}

func TestCoordAttachMissingActive(t *testing.T) {
	env, _ := newNoPTYCoordEnv(t)
	cardID := createCoordCard(t, env)

	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/attach", `{}`)
	if code != http.StatusBadRequest {
		t.Fatalf("缺 active 应 400，状态=%d body=%s", code, body)
	}
	if !strings.Contains(body, "active 必填") {
		t.Fatalf("缺 active 的错误应保留原文: %s", body)
	}
	if env.srv.keystone.AttachState(cardID) {
		t.Fatal("缺 active 不得改变接管态")
	}
}

func TestCoordAttachLocateFailureRollsBack(t *testing.T) {
	env, _ := newNoPTYCoordEnv(t)
	cardID := createCoordCard(t, env)

	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/attach",
		`{"active":true,"workdir":"/repo/handoff"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("未绑定 attach 应 400，状态=%d body=%s", code, body)
	}
	if !strings.Contains(body, "没有绑定") {
		t.Fatalf("定位错误应保留没有绑定原文: %s", body)
	}
	if env.srv.keystone.AttachState(cardID) {
		t.Fatal("定位失败后接管态必须回滚")
	}
}

func TestCoordAttachForwardsMachineQuery(t *testing.T) {
	type received struct {
		method string
		path   string
		body   string
		header string
	}
	got := make(chan received, 1)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusInternalServerError)
			return
		}
		got <- received{method: r.Method, path: r.URL.RequestURI(), body: string(data), header: r.Header.Get(forwardedHeader)}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"remote":"ok"}`))
	}))
	t.Cleanup(remote.Close)

	env, _ := newNoPTYCoordEnv(t)
	cardID := createCoordCard(t, env)
	env.srv.keystone.SetAttach(cardID, true)
	setCoordForwardTarget(t, env, "devbox", remote.URL)

	code, body := ledgerPost(t, env.testAgentdEnv,
		"/api/cards/"+cardID+"/attach?machine=devbox", `{"active":false}`)
	if code != http.StatusOK || body != `{"remote":"ok"}` {
		t.Fatalf("转发响应应原样返回，状态=%d body=%q", code, body)
	}
	select {
	case request := <-got:
		if request.method != http.MethodPost || request.path != "/api/cards/"+cardID+"/attach" ||
			request.body != `{"active":false}` || request.header != "1" {
			t.Fatalf("远端请求异常: %+v", request)
		}
	default:
		t.Fatal("远端没有收到 attach 转发")
	}
	if !env.srv.keystone.AttachState(cardID) {
		t.Fatal("转发请求不得在本地执行 release")
	}
}

func TestCoordLaunchFailureReleasesCapacityAndKeeps502(t *testing.T) {
	env, runner := newNoPTYCoordEnv(t)
	seedCoordinatorSquad(t, env)
	cardID := createCoordCard(t, env)
	runner.failLaunch = true

	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/launch", `{}`)
	if code != http.StatusBadGateway {
		t.Fatalf("承载失败应 502，状态=%d body=%s", code, body)
	}
	if !strings.Contains(body, "拉起协调者失败") || strings.Contains(body, "并发已满") {
		t.Fatalf("LaunchForCard 失败的错误投影错误：%s", body)
	}
	for _, key := range []string{"squad/coord", "carrier/c1"} {
		if got := runningCountIn(t, env.srv.autoLedger, key); got != 0 {
			t.Fatalf("失败回合后计数 %s=%d，want 0", key, got)
		}
	}

	runner.failLaunch = false
	code, body = ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/launch", `{}`)
	if code != http.StatusOK {
		t.Fatalf("归还名额后第二次拉起应成功，状态=%d body=%s", code, body)
	}
	for _, key := range []string{"squad/coord", "carrier/c1"} {
		if got := runningCountIn(t, env.srv.autoLedger, key); got != 0 {
			t.Fatalf("成功回合后计数 %s=%d，want 0", key, got)
		}
	}
}
