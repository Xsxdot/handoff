package agentd

// coordapi_test.go —— 协调者生命周期三端点的缝级测试（B156.3 K4）。
// 全部断言穿过 HTTP 层（ledgerPost/ledgerGet），组装链穿过真实编制域
// （SquadRows→LaunchAdmit→Carrier），keystone 侧注入 fake 端口记录 spec。

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/hostapi"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/testhttp"
)

// fakeCoordRunner 记录 Launch 收到的 SessionSpec 与次数（防「spec 回潮」的 agentd
// 侧观测点）；failLaunch 置真时 Launch 报错——source 只进审计与错误包装，承载失败
// 的错误原文是「source 进 LaunchForCard」的唯一可观测面。
type fakeCoordRunner struct {
	launches   []keysclient.SessionSpec
	resumes    []string
	failLaunch bool
	launchID   string
}

func (r *fakeCoordRunner) Launch(spec keysclient.SessionSpec, prompt string) (keysclient.TurnResult, error) {
	r.launches = append(r.launches, spec)
	if r.failLaunch {
		return keysclient.TurnResult{}, errors.New("承载不可用")
	}
	if r.launchID == "" {
		r.launchID = "sess-coord"
	}
	return keysclient.TurnResult{SessionID: r.launchID, Output: "ok"}, nil
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
// allowCarrierMachines 把测试用机器名写进活配置 targets，让 PutCarrier 的
// B334 闸放过夹具载体。本机别名跳过。cfgPath 空时先落一份临时配置。
func allowCarrierMachines(t *testing.T, s *Server, names ...string) {
	t.Helper()
	if s.cfgPath == "" {
		p := t.TempDir() + "/config.yaml"
		if err := config.Save(p, s.conf()); err != nil {
			t.Fatalf("准备配置: %v", err)
		}
		s.SetConfigPath(p)
	}
	if err := s.swapConf(func(c *config.Config) error {
		for _, name := range names {
			if scheduling.IsLocalMachine(name) {
				continue
			}
			if _, ok := c.Targets[name]; ok {
				continue
			}
			c.Targets[name] = config.Target{Addr: "127.0.0.1:1", Token: testToken}
		}
		return nil
	}); err != nil {
		t.Fatalf("测试 targets %v: %v", names, err)
	}
}

func newCoordEnv(t *testing.T) (*ledgerEnv, *fakeCoordRunner) {
	t.Helper()
	env := newLedgerEnv(t)
	SetupAutomationForTest(t, env.srv, env.ledger)
	allowCarrierMachines(t, env.srv, "linux-01", "m1", "m2")
	// print 一次性回合：假 runner 默认自报 "sess-coord"，无需再注入 TUI 缝。
	runner := &fakeCoordRunner{}
	ks := keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{expandHome: hostapi.ExpandHomePath})
	ks.SetSessionRefResolver(coordinatorSessionRefResolver{server: env.srv, expandHomeDir: hostapi.ExpandHomePath})
	env.srv.SetKeystone(ks)
	return env, runner
}

func newNoPTYCoordEnv(t *testing.T) (*ledgerEnv, *fakeCoordRunner) {
	t.Helper()
	env := newNoPTYLedgerEnv(t)
	SetupAutomationForTest(t, env.srv, env.ledger)
	allowCarrierMachines(t, env.srv, "linux-01", "m1", "m2")
	// print 一次性回合：假 runner 默认自报 "sess-coord"，无需再注入 TUI 缝。
	runner := &fakeCoordRunner{}
	ks := keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{expandHome: hostapi.ExpandHomePath})
	ks.SetSessionRefResolver(coordinatorSessionRefResolver{server: env.srv, expandHomeDir: hostapi.ExpandHomePath})
	env.srv.SetKeystone(ks)
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
	svc := mustScheduling(t, env.srv)
	putOnlineCarrier(t, svc, scheduling.Carrier{Name: "c1", Machine: "local",
		CLI: "opencode", HomeDir: "/home/coordinator",
		Credential: scheduling.CredentialStandalone, Status: scheduling.StatusOnline})
	if err := svc.PutSquad(scheduling.Squad{Name: "coord", Role: scheduling.RoleCoordinator,
		Members: []scheduling.SquadMember{{Carrier: "c1", MaxConcurrency: 1}}}, 0); err != nil {
		t.Fatalf("登记协调者小队: %v", err)
	}
}

// TestCoordLaunchEndpointSuccess 缝⑤×缝①×缝②：一键拉起（空对象默认 coordinate）走真实
// 编制域解析出非空 SessionSpec（CLI/HomeDir/Workdir 逐一断言——钉澄清 2，防空
// spec 回潮）；拉起恰一次 Launch；席位落定（身份=载体自报的 session id）；
// 名额回合结束即归还；全程不产生 task。
func TestCoordLaunchEndpointSuccess(t *testing.T) {
	env, runner := newCoordEnv(t)
	seedCoordinatorSquad(t, env)
	if err := env.st.CreateProjectLocation(&proto.ProjectLocation{
		Name: "handoff", Path: "/repo/handoff"}); err != nil {
		t.Fatalf("登记项目位置: %v", err)
	}
	cardID := createCoordCard(t, env)

	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/launch",
		`{}`)
	if code != http.StatusOK {
		t.Fatalf("launch 状态=%d body=%s", code, body)
	}
	var resp struct {
		Woke      bool   `json:"woke"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil || !resp.Woke {
		t.Fatalf("launch 响应异常: %s err=%v", body, err)
	}
	// print 一次性回合：session_id 是真的（载体自报），不再是窗口 id。
	if resp.SessionID != "sess-coord" {
		t.Fatalf("launch 应回传载体自报 session id，got %q body=%s", resp.SessionID, body)
	}
	wantIdentity, err := proto.EncodeSeatIdentity("opencode", "sess-coord")
	if err != nil {
		t.Fatalf("编码期望席位身份: %v", err)
	}
	// 缝级观测点：print 回合的 SessionSpec 必须真实穿过编制域解析（防「空 spec 回潮」）。
	if len(runner.launches) != 1 {
		t.Fatalf("拉起应恰触发一次 Launch，实际 %d", len(runner.launches))
	}
	if got := runner.launches[0]; got.CLI != "opencode" || got.HomeDir != "/home/coordinator" || got.Workdir != "/repo/handoff" {
		t.Fatalf("SessionSpec 组装错误: cli=%q home=%q workdir=%q", got.CLI, got.HomeDir, got.Workdir)
	}
	card, err := env.ledger.GetCard(cardID)
	if err != nil {
		t.Fatalf("读回协调者席位: %v", err)
	}
	// 席位落定：拉起回合成功后，席位身份 = 载体自报的会话，来源 = coordinate。
	if card.DriverSession != wantIdentity || card.DriverSource != string(proto.SeatSourceCoordinate) {
		t.Fatalf("拉起后席位应落定 coordinate 身份，got session=%q source=%q want=%q",
			card.DriverSession, card.DriverSource, wantIdentity)
	}
	// 承重属性：print 形态下名额在回合结束即归还（不再靠关 tab 释放）。
	facade := env.srv.autoLedger
	for key, want := range map[string]int{"squad/coord/c1": 0, "carrier/c1": 0} {
		if n := runningCountIn(t, facade, key); n != want {
			t.Fatalf("回合结束后名额应归零 %s=%d，want %d", key, n, want)
		}
	}
	statusCode, statusBody := ledgerGet(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator")
	if statusCode != http.StatusOK {
		t.Fatalf("GET /coordinator 状态=%d body=%s", statusCode, statusBody)
	}
	var coordStatus proto.CoordinatorStatus
	if err := json.Unmarshal([]byte(statusBody), &coordStatus); err != nil {
		t.Fatalf("解析 /coordinator 响应: %v", err)
	}
	if !coordStatus.Bound {
		t.Fatalf("席位落定后 Bound 应为 true: %s", statusBody)
	}

	// 铁律：拉起路径全程不产生 task（竖切断言延伸到 gateway 层）。
	links, err := env.ledger.TasksOf(cardID)
	if err != nil || len(links) != 0 {
		t.Fatalf("拉起不应产生 task：links=%v err=%v", links, err)
	}
}

// TestCoordLaunchSourceFlowsIntoKeystone 锁住来源执法：manual/card_create 等旧值和
// 未知值均在 Launch 前 400，不能借来源字段绕过 coordinate 席位规则。
func TestCoordLaunchSourceFlowsIntoKeystone(t *testing.T) {
	env, runner := newCoordEnv(t)
	seedCoordinatorSquad(t, env)
	cardID := createCoordCard(t, env)

	for _, src := range []string{"manual", "card_create", "unknown"} {
		code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/launch",
			`{"source":"`+src+`"}`)
		if code != http.StatusBadRequest {
			t.Fatalf("[%s] 退役来源应 400，状态=%d body=%s", src, code, body)
		}
		if !strings.Contains(body, "coordinate") {
			t.Fatalf("[%s] 错误应指向 coordinate：body=%s", src, body)
		}
	}
	if len(runner.launches) != 0 {
		t.Fatalf("退役来源不得触发 Launch，实际 %d", len(runner.launches))
	}
	if code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/launch", `{}`); code != http.StatusOK {
		t.Fatalf("coordinate 来源应成功，状态=%d body=%s", code, body)
	}
	// print 形态下没有 tab 可作判据：席位已被占用即阻挡二次 Launch。
	if code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/launch", `{}`); code != http.StatusConflict {
		t.Fatalf("已有席位不得二次 Launch，状态=%d body=%s", code, body)
	}
	card, err := env.ledger.GetCard(cardID)
	if err != nil {
		t.Fatalf("读回席位: %v", err)
	}
	if card.DriverSession == "" || card.DriverSource != string(proto.SeatSourceCoordinate) {
		t.Fatalf("coordinate 成功后席位应已落定，got session=%q source=%q",
			card.DriverSession, card.DriverSource)
	}
}

func TestCoordRebindLaunchHTTPContract(t *testing.T) {
	env, runner := newNoPTYCoordEnv(t)
	seedCoordinatorSquad(t, env)
	cardID := createCoordCard(t, env)
	if code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/launch", `{}`); code != http.StatusOK {
		t.Fatalf("初次拉起失败：%d %s", code, body)
	}
	// 换绑走新的 print 回合：让载体报一个不同的会话 id，证明席位身份真的换了。
	runner.launchID = "sess-rebound"
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/rebind", `{"mode":"launch"}`)
	if code != http.StatusOK {
		t.Fatalf("launch rebind 应 200：%d %s", code, body)
	}
	var resp proto.CoordinatorLaunchResp
	if err := json.Unmarshal([]byte(body), &resp); err != nil || !resp.Woke {
		t.Fatalf("launch rebind 响应异常：%s err=%v", body, err)
	}
	if resp.SessionID != "sess-rebound" {
		t.Fatalf("换绑应回传新会话 id，got %q body=%s", resp.SessionID, body)
	}
	wantIdentity, err := proto.EncodeSeatIdentity("opencode", "sess-rebound")
	if err != nil {
		t.Fatalf("编码期望席位身份: %v", err)
	}
	card, err := env.ledger.GetCard(cardID)
	if err != nil {
		t.Fatalf("读回换绑后席位: %v", err)
	}
	if card.DriverSession != wantIdentity || card.DriverSource != string(proto.SeatSourceCoordinate) {
		t.Fatalf("换绑后席位身份应更新为新会话，got session=%q source=%q want=%q",
			card.DriverSession, card.DriverSource, wantIdentity)
	}
	statusCode, statusBody := ledgerGet(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator")
	if statusCode != http.StatusOK {
		t.Fatalf("GET /coordinator 状态=%d body=%s", statusCode, statusBody)
	}
	var status proto.CoordinatorStatus
	if err := json.Unmarshal([]byte(statusBody), &status); err != nil {
		t.Fatalf("解析 /coordinator: %v", err)
	}
	if !status.Bound {
		t.Fatalf("换绑后 bound 应为 true: %s", statusBody)
	}
	for _, payload := range []string{`{"mode":"self"}`, `{"mode":"launch","identity":"cli:forged#id"}`} {
		code, body = ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/rebind", payload)
		if code != http.StatusBadRequest {
			t.Fatalf("非法 rebind body=%s 应 400：%d %s", payload, code, body)
		}
	}
}

func TestCoordRebindEmptySeatConflicts(t *testing.T) {
	env, runner := newNoPTYCoordEnv(t)
	seedCoordinatorSquad(t, env)
	cardID := createCoordCard(t, env)
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/rebind", `{"mode":"launch"}`)
	if code != http.StatusConflict || !strings.Contains(body, "空座") {
		t.Fatalf("空座 launch rebind 应 409 指向坐下/叫机器人：%d %s", code, body)
	}
	if len(runner.launches) != 0 {
		t.Fatalf("空座 rebind 不得启动机器人，Launch=%d", len(runner.launches))
	}
}

func TestCoordRebindNoSquadIsActionableBadRequest(t *testing.T) {
	env, runner := newNoPTYCoordEnv(t)
	cardID := createCoordCard(t, env)
	if err := env.ledger.BindSeat(cardID, "cli:opencode#sess-old", proto.SeatSourceCoordinate); err != nil {
		t.Fatalf("预置协调者席位: %v", err)
	}
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/rebind", `{"mode":"launch"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("无协调者小队的 rebind 应 400，状态=%d body=%s", code, body)
	}
	if !strings.Contains(body, "handoff squad create") {
		t.Fatalf("rebind 错误应包含登记小队指引：%s", body)
	}
	if len(runner.launches) != 0 {
		t.Fatalf("无协调者小队不得启动机器人，Launch=%d", len(runner.launches))
	}
}

func TestCoordRebindSourceOnlySeatDoesNotLaunch(t *testing.T) {
	env, runner := newNoPTYCoordEnv(t)
	seedCoordinatorSquad(t, env)
	cardID := createCoordCard(t, env)
	raw, err := sql.Open("sqlite", env.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE cards SET driver_source = ? WHERE id = ?`, string(proto.SeatSourceCoordinate), cardID); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/rebind", `{"mode":"launch"}`)
	if code != http.StatusConflict {
		t.Fatalf("source-only 旧席位 rebind 应 409，状态=%d body=%s", code, body)
	}
	if !strings.Contains(body, "身份") {
		t.Fatalf("source-only 错误应说明身份缺失：%s", body)
	}
	if len(runner.launches) != 0 {
		t.Fatalf("source-only 旧席位不得启动机器人，Launch=%d", len(runner.launches))
	}
}

// TestCoordLaunchNoSquadActionableError 缝⑤×缝①：未登记协调者小队时 400 且报文
// 含指路文案与最小参数示例（岔口四 B 附加约束）。执行者小队在场证明过滤按 role 生效。
func TestCoordLaunchNoSquadActionableError(t *testing.T) {
	env, _ := newCoordEnv(t)
	svc := mustScheduling(t, env.srv)
	putOnlineCarrier(t, svc, scheduling.Carrier{Name: "e1", Machine: "local",
		CLI: "opencode", Credential: scheduling.CredentialStandalone,
		Status: scheduling.StatusOnline})
	if err := svc.PutSquad(scheduling.Squad{Name: "exec", Role: scheduling.RoleExecutor,
		Members: []scheduling.SquadMember{{Carrier: "e1"}}}, 0); err != nil {
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
	svc := mustScheduling(t, env.srv)
	for _, c := range []scheduling.Carrier{
		{Name: "c1", Machine: "m1", CLI: "opencode", Credential: scheduling.CredentialStandalone, Status: scheduling.StatusOnline},
		{Name: "c2", Machine: "m2", CLI: "opencode", Credential: scheduling.CredentialStandalone, Status: scheduling.StatusOnline},
	} {
		putOnlineCarrier(t, svc, c)
	}
	for _, q := range []scheduling.Squad{
		{Name: "coord-a", Role: scheduling.RoleCoordinator, Members: []scheduling.SquadMember{{Carrier: "c1"}}},
		{Name: "coord-b", Role: scheduling.RoleCoordinator, Members: []scheduling.SquadMember{{Carrier: "c2"}}},
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
	if code != http.StatusOK && code != http.StatusBadRequest {
		t.Fatalf("attach 应 200 或因无头会话缺失 400，状态=%d body=%s", code, body)
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
	if code != http.StatusOK || !strings.Contains(body, `"bound":true`) {
		t.Fatalf("print 回合后 Bound 应为 true：%d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Fatalf("绑定态解析失败: %v body=%s", err, body)
	}
	if !status.Bound {
		t.Fatalf("席位落定后 Bound 应为 true: %+v", status)
	}
}

// TestCoordForgetAfterSelfRebindClearsStaleSession 锁 CLI self 换绑后的跨进程边界：
// 账本已经改成 bind 后，agentd 必须 Forget 旧 coordinate 内存，否则 status 会继续
// 暴露不存在的机器人 attach 目录。
func TestCoordForgetAfterSelfRebindClearsStaleSession(t *testing.T) {
	env, runner := newNoPTYCoordEnv(t)
	cardID := createCoordCard(t, env)
	runner.launchID = "sess-old"
	if _, err := env.srv.keystone.LaunchForCard(context.Background(), cardID, "coordinate", keysclient.SessionSpec{CLI: "opencode"}); err != nil {
		t.Fatalf("准备旧 coordinate 会话: %v", err)
	}
	if err := env.ledger.BindSeat(cardID, "cli:opencode#sess-old", proto.SeatSourceCoordinate); err != nil {
		t.Fatalf("准备旧 coordinate 席位: %v", err)
	}
	if err := env.ledger.RebindSeat(cardID, "cli:codex#sess-new", proto.SeatSourceBind, "cli:opencode#sess-old"); err != nil {
		t.Fatalf("模拟 self 换绑: %v", err)
	}

	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/forget", `{}`)
	if code != http.StatusOK || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("Forget 应 200/ok，实得 %d %s", code, body)
	}
	code, body = ledgerGet(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator")
	if code != http.StatusOK {
		t.Fatalf("读取 self 换绑后状态失败: %d %s", code, body)
	}
	var status proto.CoordinatorStatus
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Fatalf("状态解码失败: %v body=%s", err, body)
	}
	if !status.Bound || status.Attach != nil {
		t.Fatalf("bind 席位驱逐旧内存后应 bound=true 且 attach=null: %+v", status)
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
	remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusInternalServerError)
			return
		}
		got <- received{method: r.Method, path: r.URL.RequestURI(), body: string(data), header: r.Header.Get(forwardedHeader)}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"remote":"ok"}`))
	}))

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
	// print 形态下承载失败来自 runner.Launch，不再是「开 TUI 报错」。
	runner.failLaunch = true

	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/launch", `{}`)
	if code != http.StatusBadGateway {
		t.Fatalf("承载失败应 502，状态=%d body=%s", code, body)
	}
	if !strings.Contains(body, "拉起协调者失败") || strings.Contains(body, "并发已满") {
		t.Fatalf("承载失败的错误投影错误：%s", body)
	}
	for _, key := range []string{"squad/coord/c1", "carrier/c1"} {
		if got := runningCountIn(t, env.srv.autoLedger, key); got != 0 {
			t.Fatalf("失败回合后计数 %s=%d，want 0", key, got)
		}
	}

	runner.failLaunch = false
	code, body = ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/launch", `{}`)
	if code != http.StatusOK {
		t.Fatalf("归还名额后第二次拉起应成功，状态=%d body=%s", code, body)
	}
	// print 形态：成功回合也在返回时归还名额，不靠关 tab 释放。
	for _, key := range []string{"squad/coord/c1", "carrier/c1"} {
		if got := runningCountIn(t, env.srv.autoLedger, key); got != 0 {
			t.Fatalf("成功回合结束名额应归零 %s=%d，want 0", key, got)
		}
	}
}

// TestCoordLaunchRejectsRemoteCarrier 锁「print 协调者只支持本机载体」：
// 载体登记在远端时，回合必须被显式拒绝（旧 TUI 形态有远端 PTY 分支，print 没有），
// 且不得留下半状态——席位仍空、名额归零。
func TestCoordLaunchRejectsRemoteCarrier(t *testing.T) {
	env, _ := newNoPTYCoordEnv(t)
	svc := mustScheduling(t, env.srv)
	putOnlineCarrier(t, svc, scheduling.Carrier{
		Name: "remote-carrier", Machine: "linux-01", CLI: "opencode",
		HomeDir: "/home/coordinator", Credential: scheduling.CredentialStandalone,
		MaxConcurrency: 1, Status: scheduling.StatusOnline,
	})
	if err := svc.PutSquad(scheduling.Squad{
		Name: "coord", Role: scheduling.RoleCoordinator,
		Members: []scheduling.SquadMember{{Carrier: "remote-carrier", MaxConcurrency: 1}},
	}, 0); err != nil {
		t.Fatalf("登记协调者小队: %v", err)
	}
	cardID := createCoordCard(t, env)

	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/launch", `{}`)
	if code == http.StatusOK {
		t.Fatalf("远端载体不得被拉起，状态=%d body=%s", code, body)
	}
	if !strings.Contains(body, "远端") {
		t.Fatalf("拒绝理由应点明远端载体：%s", body)
	}
	card, err := env.ledger.GetCard(cardID)
	if err != nil {
		t.Fatal(err)
	}
	if card.DriverSession != "" || card.DriverSource != "" {
		t.Fatalf("被拒的拉起不得落席位: session=%q source=%q", card.DriverSession, card.DriverSource)
	}
	for _, key := range []string{"squad/coord/remote-carrier", "carrier/remote-carrier"} {
		if got := runningCountIn(t, env.srv.autoLedger, key); got != 0 {
			t.Fatalf("被拒后名额 %s=%d，want 0", key, got)
		}
	}
}

// seatStealingRunner 在回合内抢先占座：模拟「回合已跑完但席位已被别人占」，
// 让 agentd 回合后的 BindSeat 必然 CAS 冲突。用于验证「席位写失败不静默」。
type seatStealingRunner struct {
	env  *ledgerEnv
	card string
}

func (r *seatStealingRunner) Launch(keysclient.SessionSpec, string) (keysclient.TurnResult, error) {
	if err := r.env.ledger.BindSeat(r.card, "cli:opencode#sess-intruder", proto.SeatSourceCoordinate); err != nil {
		return keysclient.TurnResult{}, err
	}
	return keysclient.TurnResult{SessionID: "sess-coord", Output: "ok"}, nil
}

func (r *seatStealingRunner) Resume(ref keysclient.SessionRef, prompt string) (keysclient.TurnResult, error) {
	return keysclient.TurnResult{SessionID: ref.SessionID, Output: "ok"}, nil
}

// TestCoordLaunchSeatWriteFailureLeavesNeedsHuman 锁「席位写失败不静默」：print 回合
// 已经跑完（有新会话在飞），但席位 CAS 写失败时端点必须回 409 带 session_id，
// 并在账本上留 needs_human——这个会话叫不醒，只能人工处置。
func TestCoordLaunchSeatWriteFailureLeavesNeedsHuman(t *testing.T) {
	env, _ := newNoPTYCoordEnv(t)
	seedCoordinatorSquad(t, env)
	cardID := createCoordCard(t, env)
	stealing := &seatStealingRunner{env: env, card: cardID}
	ks := keystone.New(stealing, &fakeCoordNarrator{}, env.srv.autoLedger,
		attachLocator{expandHome: hostapi.ExpandHomePath})
	ks.SetSessionRefResolver(coordinatorSessionRefResolver{server: env.srv, expandHomeDir: hostapi.ExpandHomePath})
	env.srv.SetKeystone(ks)

	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/launch", `{}`)
	if code != http.StatusConflict {
		t.Fatalf("席位写失败应 409，状态=%d body=%s", code, body)
	}
	var resp struct {
		Error     string `json:"error"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("解析 409 body: %v body=%s", err, body)
	}
	if resp.SessionID != "sess-coord" {
		t.Fatalf("409 应带回新会话 id，got %q body=%s", resp.SessionID, body)
	}
	reason, err := env.ledger.NeedsOf(cardID)
	if err != nil {
		t.Fatalf("读等人标记: %v", err)
	}
	if strings.TrimSpace(reason) == "" {
		t.Fatalf("席位写失败应在账本留 needs_human 痕迹：body=%s", body)
	}
	// 席位没落：账本上仍是抢占者身份，不是 agentd 自报的新会话。
	card, err := env.ledger.GetCard(cardID)
	if err != nil {
		t.Fatalf("读回卡: %v", err)
	}
	if card.DriverSession != "cli:opencode#sess-intruder" {
		t.Fatalf("席位应仍为抢占者身份（证明 CAS 冲突真实发生），got %q", card.DriverSession)
	}
}

// TestCoordLaunchRoundReleasesCapacityImmediately 锁 print 形态的承重属性：一次成功
// 拉起后两级名额立即归零，因而同小队的下一张卡能立刻被受理（不再 409「并发已满」）。
func TestCoordLaunchRoundReleasesCapacityImmediately(t *testing.T) {
	env, _ := newNoPTYCoordEnv(t)
	seedCoordinatorSquad(t, env)
	first := createCoordCard(t, env)
	second := createCoordCard(t, env)

	if code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+first+"/coordinator/launch", `{}`); code != http.StatusOK {
		t.Fatalf("首次拉起应 200：%d %s", code, body)
	}
	for _, key := range []string{"squad/coord/c1", "carrier/c1"} {
		if got := runningCountIn(t, env.srv.autoLedger, key); got != 0 {
			t.Fatalf("首次回合结束名额应立即归零 %s=%d，want 0", key, got)
		}
	}
	if code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+second+"/coordinator/launch", `{}`); code != http.StatusOK {
		t.Fatalf("名额已还，第二张卡应立刻受理（不得 409）：%d %s", code, body)
	}
	for _, key := range []string{"squad/coord/c1", "carrier/c1"} {
		if got := runningCountIn(t, env.srv.autoLedger, key); got != 0 {
			t.Fatalf("第二回合结束名额应立即归零 %s=%d，want 0", key, got)
		}
	}
}

func TestCoordStatusColdLocateUsesRegisteredHomeWithoutAdmission(t *testing.T) {
	env, _ := newCoordEnv(t)
	seedCoordinatorSquad(t, env)
	if err := env.st.CreateProjectLocation(&proto.ProjectLocation{
		Name: "handoff", Path: "/repo/handoff"}); err != nil {
		t.Fatalf("登记项目位置: %v", err)
	}
	cardID := createCoordCard(t, env)

	// 直接绑定席位，不种 keystone 内存会话（模拟冷路径）
	if err := env.ledger.BindSeat(cardID, "cli:opencode#sess-cold", proto.SeatSourceCoordinate); err != nil {
		t.Fatalf("绑定席位: %v", err)
	}

	facade := env.srv.autoLedger
	for key, want := range map[string]int{"squad/coord/c1": 0, "carrier/c1": 0} {
		if n := runningCountIn(t, facade, key); n != want {
			t.Fatalf("GET 前计数 %s=%d，want %d", key, n, want)
		}
	}

	code, body := ledgerGet(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator")
	if code != http.StatusOK {
		t.Fatalf("GET 协调者状态返回 %d: %s", code, body)
	}
	var status proto.CoordinatorStatus
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Fatalf("解析协调者状态响应: %v", err)
	}
	if status.Attach == nil {
		t.Fatalf("status.Attach 为空: %s", body)
	}
	if !strings.Contains(status.Attach.Command, "HOME=/home/coordinator") ||
		!strings.Contains(status.Attach.Command, "--session sess-cold") {
		t.Fatalf("attach.command 必须含 HOME=/home/coordinator 与 --session sess-cold，got: %s", status.Attach.Command)
	}

	for key, want := range map[string]int{"squad/coord/c1": 0, "carrier/c1": 0} {
		if n := runningCountIn(t, facade, key); n != want {
			t.Fatalf("GET 后计数 %s=%d，want %d", key, n, want)
		}
	}
}

func TestCoordStatusQuotesHomePathWithSpaces(t *testing.T) {
	env, _ := newCoordEnv(t)
	c1 := scheduling.Carrier{
		Name: "c1", Machine: "local", CLI: "opencode",
		HomeDir:    "/home/coord docs",
		Credential: scheduling.CredentialStandalone, Status: scheduling.StatusOnline,
	}
	putOnlineCarrier(t, mustScheduling(t, env.srv), c1)
	squad := scheduling.Squad{
		Name: "coord", Role: scheduling.RoleCoordinator,
		Members: []scheduling.SquadMember{{Carrier: "c1", MaxConcurrency: 1}},
	}
	if err := mustScheduling(t, env.srv).PutSquad(squad, 0); err != nil {
		t.Fatalf("登记小队: %v", err)
	}
	if err := env.st.CreateProjectLocation(&proto.ProjectLocation{
		Name: "handoff", Path: "/repo/handoff"}); err != nil {
		t.Fatalf("登记项目位置: %v", err)
	}

	cardID := createCoordCard(t, env)
	if err := env.ledger.BindSeat(cardID, "cli:opencode#sess-cold", proto.SeatSourceCoordinate); err != nil {
		t.Fatalf("绑定席位: %v", err)
	}

	code, body := ledgerGet(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator")
	if code != http.StatusOK {
		t.Fatalf("GET 状态返回 %d: %s", code, body)
	}
	var status proto.CoordinatorStatus
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if status.Attach == nil {
		t.Fatalf("status.Attach 为空")
	}
	wantCommand := "HOME='/home/coord docs' opencode --session sess-cold"
	if status.Attach.Command != wantCommand {
		t.Fatalf("command = %q, want %q", status.Attach.Command, wantCommand)
	}
	cmd := exec.Command("sh", "-n", "-c", status.Attach.Command)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("POSIX shell 解析失败: %v, output: %s", err, string(out))
	}
}

func TestCoordAttachHomeExpansionFailureReturns400(t *testing.T) {
	env, _ := newCoordEnv(t)
	seedCoordinatorSquad(t, env)
	cardID := createCoordCard(t, env)

	runner := &fakeCoordRunner{}
	ks := keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{
		expandHome: func(string) (string, error) {
			return "", errors.New("home unavailable")
		},
	})
	ks.SetSessionRefResolver(coordinatorSessionRefResolver{server: env.srv, expandHomeDir: hostapi.ExpandHomePath})
	env.srv.SetKeystone(ks)

	if _, err := ks.LaunchForCard(context.Background(), cardID, "coordinate", keysclient.SessionSpec{
		CLI: "opencode", HomeDir: "/home/coordinator", Workdir: "/repo/handoff",
	}); err != nil {
		t.Fatalf("seed hot session: %v", err)
	}

	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/attach",
		`{"active":true,"workdir":"/repo/handoff"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("attach 展开失败应返回 400，got %d: %s", code, body)
	}
	if !strings.Contains(body, "home unavailable") {
		t.Fatalf("响应体应包含展开错误，got %s", body)
	}
	if env.srv.keystone.AttachState(cardID) != false {
		t.Fatalf("定位失败后 AttachState 应回滚为 false")
	}
}
