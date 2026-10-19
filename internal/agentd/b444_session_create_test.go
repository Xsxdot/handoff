// b444_session_create_test.go —— B444 P2 建会话编排与改名的竖切与反例（plan §5 A2.1–A2.6；协调者裁决
// R9 / Q3 / Q4 / Q6 / Q7；缺陷族 R6/R7/R9/R10）。
//
// 观察点：真 HTTP mux（ledgerPost/ledgerGet 穿 httptest）+ 真账本 + 真 collab + 真 scheduling 名额 + 真
// keystone，只把 keystone 出站 Runner 换成 b444CreateRunner（记录 Launch 的 spec 与简报，可注入失败）。
// 判据钉行为：响应 JSON 键与值、账本里的会话/卡归属/驻场行、会话里的系统指针行、HTTP 状态码、
// 建会话后人发言能否真的续到驻场者（竖切穿过 P1 唤醒循环——R9 机器名若写错，本机不会认领）。
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// b444CreateRunner 记录 Launch 的 spec/简报与 Resume 的 ref；launchErr 非 nil 时 Launch 返回它。
// onLaunch 非 nil 时在 Launch 内（首轮「正在跑」期间、返回会话 id 之前）以简报调用——模拟载体在首轮里
// 执行 handoff 命令（B444 P2 返工 F1 的竖切观察点）。onLaunch 在锁外调用，可再入本 runner 的读面。
type b444CreateRunner struct {
	mu        sync.Mutex
	launches  []keysclient.SessionSpec
	prompts   []string
	resumes   []keysclient.SessionRef
	launchErr error
	nextID    int
	onLaunch  func(prompt string)
}

func (r *b444CreateRunner) Launch(spec keysclient.SessionSpec, prompt string) (keysclient.TurnResult, error) {
	r.mu.Lock()
	r.launches = append(r.launches, spec)
	r.prompts = append(r.prompts, prompt)
	hook := r.onLaunch
	r.mu.Unlock()
	if hook != nil {
		hook(prompt)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.launchErr != nil {
		return keysclient.TurnResult{}, r.launchErr
	}
	r.nextID++
	return keysclient.TurnResult{SessionID: fmt.Sprintf("ses_b444_create_%d", r.nextID)}, nil
}

func (r *b444CreateRunner) Resume(ref keysclient.SessionRef, _ string) (keysclient.TurnResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resumes = append(r.resumes, ref)
	return keysclient.TurnResult{SessionID: ref.SessionID}, nil
}

func (r *b444CreateRunner) setLaunchErr(err error) {
	r.mu.Lock()
	r.launchErr = err
	r.mu.Unlock()
}

func (r *b444CreateRunner) snapshot() ([]keysclient.SessionSpec, []string, []keysclient.SessionRef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]keysclient.SessionSpec(nil), r.launches...), append([]string(nil), r.prompts...),
		append([]keysclient.SessionRef(nil), r.resumes...)
}

// newB444CreateEnv：控制台身份 + 自动化 + 协调小队 coord（载体 coord-carrier，machine=local 别名）+ 项目
// handoff 的本机位置 + b444CreateRunner 出站。withSquad=false 时不登记任何小队（A2.3）。
func newB444CreateEnv(t *testing.T, withSquad bool) (*ledgerEnv, *b444CreateRunner) {
	t.Helper()
	env := newRoomsEnv(t)
	if withSquad {
		seedQueueCoordinator(t, env)
	}
	if err := env.st.CreateProjectLocation(&proto.ProjectLocation{Name: "handoff", Path: "/repo/handoff"}); err != nil {
		t.Fatalf("登记项目位置: %v", err)
	}
	runner := &b444CreateRunner{}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	return env, runner
}

func b444Card(t *testing.T, env *ledgerEnv, title, project string) string {
	t.Helper()
	card, err := env.ledger.CreateCard(ledger.NewCard{Title: title, Project: project, Workflow: "attachment-gates", Actor: "test"})
	if err != nil {
		t.Fatalf("建卡 %s: %v", title, err)
	}
	return card.ID
}

func b444Create(t *testing.T, env *ledgerEnv, body string) (int, proto.SessionCreateResp, map[string]json.RawMessage) {
	t.Helper()
	code, raw := ledgerPost(t, env.testAgentdEnv, "/api/sessions", body)
	var resp proto.SessionCreateResp
	var keys map[string]json.RawMessage
	if code == http.StatusOK {
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			t.Fatalf("解码建会话响应: %v body=%s", err, raw)
		}
		if err := json.Unmarshal([]byte(raw), &keys); err != nil {
			t.Fatal(err)
		}
	} else {
		keys = map[string]json.RawMessage{"error": json.RawMessage(fmt.Sprintf("%q", raw))}
	}
	return code, resp, keys
}

// b444WantMachine 是本机写进驻场行的真实机器名（R9）：配置名优先，其次 hostname。
func b444WantMachine(t *testing.T, env *ledgerEnv) string {
	t.Helper()
	if cfg := env.srv.conf(); cfg != nil && cfg.Name != "" {
		return cfg.Name
	}
	return o2Hostname(t)
}

func b444SessionCount(t *testing.T, env *ledgerEnv) int {
	t.Helper()
	sessions, err := env.ledger.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	return len(sessions)
}

// A2.1 竖切：项目 + 两张卡 + 小队、标题空 → 所属项目、「首张 等 2 张」、两卡进群、push 驻场者 agent:coord
// 坐下（R9 真实机器名）、首轮简报带 --agent；随后人无 @ 发言 → 同一 push 会话续轮（穿过 P1 唤醒循环）。
func TestB444SessionCreateWithCardsSeatsPushResident(t *testing.T) {
	env, runner := newB444CreateEnv(t, true)
	c1 := b444Card(t, env, "登录页白屏", "handoff")
	c2 := b444Card(t, env, "接口超时", "handoff")
	code, resp, keys := b444Create(t, env, fmt.Sprintf(`{"project":"handoff","cards":[%q,%q],"squad":"coord"}`, c1, c2))
	if code != http.StatusOK {
		t.Fatalf("建会话应 200，得 %d %s", code, keys["error"])
	}
	if resp.HomeProject != "handoff" || resp.Title != "登录页白屏 等 2 张" || resp.Owner != consoleMember {
		t.Fatalf("会话本体不符: %+v", resp.Session)
	}
	if len(resp.Cards) != 2 {
		t.Fatalf("两卡都应进群，得 %v", resp.Cards)
	}
	if _, ok := keys["rejected_cards"]; ok {
		t.Fatalf("无拒卡时 rejected_cards 应缺键: %s", keys["rejected_cards"])
	}
	if resp.Resident == nil || resp.Resident.Identity != b444Identity || resp.Resident.Error != "" {
		t.Fatalf("驻场者应坐下 agent:coord 且无错: %+v", resp.Resident)
	}
	r, ok, err := env.ledger.SessionResident(resp.ID)
	if err != nil || !ok {
		t.Fatalf("驻场行应存在: ok=%v err=%v", ok, err)
	}
	if r.DriverMode != ledger.ResidentDriverPush || r.PushSession != "ses_b444_create_1" || r.Carrier != "coord-carrier" ||
		r.PushCLI != "opencode" || r.Workdir != "/repo/handoff" || r.HomeDir == "" {
		t.Fatalf("驻场行形状不符: %+v", r)
	}
	// 协调者裁决 R9：push 驻场行的载体机器必须是真实机器名，禁空、禁 local/本机 别名。
	if r.CarrierMachine == "" || r.CarrierMachine == "local" || r.CarrierMachine == "本机" ||
		r.CarrierMachine != b444WantMachine(t, env) {
		t.Fatalf("驻场行载体机器应为真实机器名 %q，得 %q", b444WantMachine(t, env), r.CarrierMachine)
	}
	if resp.Resident.CarrierMachine != r.CarrierMachine {
		t.Fatalf("响应载体机器应与驻场行一致: %q vs %q", resp.Resident.CarrierMachine, r.CarrierMachine)
	}
	launches, prompts, _ := runner.snapshot()
	if len(launches) != 1 || launches[0].Workdir != "/repo/handoff" {
		t.Fatalf("应恰一次首轮拉起且 workdir 取所属项目位置: %+v", launches)
	}
	if !strings.Contains(prompts[0], "--agent coord") || !strings.Contains(prompts[0], resp.ID) ||
		strings.Contains(prompts[0], "--expect-title") {
		t.Fatalf("有卡会话的首轮简报应带发言命令、不带改名指令: %q", prompts[0])
	}
	code, raw := ledgerGet(t, env.testAgentdEnv, "/api/sessions/"+resp.ID)
	if code != http.StatusOK || !strings.Contains(raw, `"kind":"resident"`) || !strings.Contains(raw, `"driver_mode":"push"`) {
		t.Fatalf("详情应可见 push 驻场者成员: %d %s", code, raw)
	}

	drainAutomation(t, env)
	sendSessionMessage(t, env.srv.rooms, resp.ID, consoleMember, "先看登录页", nil, 0)
	drainAutomation(t, env)
	_, _, resumes := runner.snapshot()
	if len(resumes) != 1 || resumes[0].SessionID != "ses_b444_create_1" {
		t.Fatalf("建会话后人无 @ 发言应续到驻场者同一会话，得 %+v", resumes)
	}
}

// A2.1 命名分支：只给一张卡 → 标题 = 卡标题；零卡 → 「新会话」，首轮简报叫驻场者等人开口再起名；显式标题原样。
func TestB444SessionCreateAutoTitle(t *testing.T) {
	env, runner := newB444CreateEnv(t, true)
	c1 := b444Card(t, env, "单卡标题", "handoff")
	if code, resp, keys := b444Create(t, env, fmt.Sprintf(`{"project":"handoff","cards":[%q],"squad":"coord"}`, c1)); code != 200 || resp.Title != "单卡标题" {
		t.Fatalf("单卡会话名应为卡标题: %d %+v %s", code, resp.Session, keys["error"])
	}
	code, resp, keys := b444Create(t, env, `{"project":"handoff","squad":"coord"}`)
	if code != 200 || resp.Title != proto.SessionUntitledTitle || resp.HomeProject != "handoff" || len(resp.Cards) != 0 {
		t.Fatalf("零卡会话名应为「新会话」且属项目: %d %+v %s", code, resp.Session, keys["error"])
	}
	_, prompts, _ := runner.snapshot()
	last := prompts[len(prompts)-1]
	// 真机 R5（协调者批准改断言，同 f6113731）：首轮人还没开口，简报叫驻场者等、不下发改名；CAS 改名指令
	// 只在有人话的续轮出现（keystone TestB444ResidentBriefing 锁续轮侧）。
	if strings.Contains(last, "session rename "+resp.ID) || !strings.Contains(last, "本轮不要改名") {
		t.Fatalf("零卡未命名会话的首轮简报应叫驻场者等人开口、不带改名指令: %q", last)
	}
	if code, resp, _ := b444Create(t, env, `{"title":"  人起的名  ","project":"handoff","squad":"coord"}`); code != 200 || resp.Title != "人起的名" {
		t.Fatalf("显式标题应裁剪后原样: %d %q", code, resp.Title)
	}
}

// A2.2 反例（Q7）：已属他会话、他项目、终态、不存在的卡各被拒并点名原因；其余照拉，会话照建；
// 自动命名只按已接受的卡算。
func TestB444SessionCreateRejectsCardsWithReason(t *testing.T) {
	env, _ := newB444CreateEnv(t, true)
	ok1 := b444Card(t, env, "可拉的卡", "handoff")
	taken := b444Card(t, env, "已在别处", "handoff")
	foreign := b444Card(t, env, "别的项目", "erp")
	closed := b444Card(t, env, "已终止", "handoff")
	if err := env.ledger.CloseCard(closed, ledger.CloseShelved, "test"); err != nil {
		t.Fatalf("终止卡: %v", err)
	}
	s0, err := env.srv.rooms.CreateSession("先来的会话", "user:tester", "user:tester")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.srv.rooms.JoinCard(s0.ID, taken, "user:tester"); err != nil {
		t.Fatal(err)
	}
	code, resp, keys := b444Create(t, env, fmt.Sprintf(`{"project":"handoff","cards":[%q,%q,%q,%q,"B99999"],"squad":"coord"}`,
		taken, ok1, foreign, closed))
	if code != http.StatusOK {
		t.Fatalf("部分拒卡仍应 200 建会话，得 %d %s", code, keys["error"])
	}
	if len(resp.Cards) != 1 || resp.Cards[0] != ok1 || resp.Title != "可拉的卡" {
		t.Fatalf("只应拉入可拉的卡并按它命名: cards=%v title=%q", resp.Cards, resp.Title)
	}
	reasons := map[string]string{}
	for _, rc := range resp.RejectedCards {
		reasons[rc.Card] = rc.Reason
	}
	if !strings.Contains(reasons[taken], s0.ID) {
		t.Fatalf("已属他会话的卡原因应点名 %s，得 %q", s0.ID, reasons[taken])
	}
	if !strings.Contains(reasons[foreign], "erp") {
		t.Fatalf("他项目卡原因应点名其项目 erp，得 %q", reasons[foreign])
	}
	if reasons[closed] == "" || reasons["B99999"] == "" || len(reasons) != 4 {
		t.Fatalf("终态卡与不存在卡也应被拒并给原因: %v", reasons)
	}
	if owner, _ := env.ledger.SessionOfCard(taken); owner != s0.ID {
		t.Fatalf("被拒的卡归属不得变: %q", owner)
	}
}

// A2.3 反例：无协调小队 / 小队不存在 / 小队角色不是协调者 / 缺项目 → 400 带指路文案，零写入。
func TestB444SessionCreateSquadValidationZeroWrites(t *testing.T) {
	env, runner := newB444CreateEnv(t, false)
	card := b444Card(t, env, "卡", "handoff")
	before := b444SessionCount(t, env)
	code, raw := ledgerPost(t, env.testAgentdEnv, "/api/sessions", fmt.Sprintf(`{"project":"handoff","cards":[%q]}`, card))
	if code != http.StatusBadRequest || !strings.Contains(raw, "squad create") {
		t.Fatalf("无协调小队应 400 并指路 squad create: %d %s", code, raw)
	}
	svc := mustScheduling(t, env.srv)
	putOnlineCarrier(t, svc, scheduling.Carrier{Name: "exec-carrier", Machine: "local", CLI: "opencode",
		HomeDir: "/tmp/exec-home", Credential: scheduling.CredentialStandalone, MaxConcurrency: 1})
	if err := svc.PutSquad(scheduling.Squad{Name: "exec", Role: scheduling.RoleExecutor,
		Members: []scheduling.SquadMember{{Carrier: "exec-carrier", MaxConcurrency: 1}}}, 0); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"project":"handoff","squad":"exec"}`,
		`{"project":"handoff","squad":"nope"}`,
		`{"squad":"exec"}`,
		fmt.Sprintf(`{"cards":[%q]}`, card),
		`{"project":"has space","squad":"exec"}`,
	} {
		if code, raw := ledgerPost(t, env.testAgentdEnv, "/api/sessions", body); code != http.StatusBadRequest {
			t.Fatalf("%s 应 400，得 %d %s", body, code, raw)
		}
	}
	if after := b444SessionCount(t, env); after != before {
		t.Fatalf("校验失败不得写会话：%d → %d", before, after)
	}
	if owner, _ := env.ledger.SessionOfCard(card); owner != "" {
		t.Fatalf("校验失败不得拉卡，卡已属 %q", owner)
	}
	if launches, _, _ := runner.snapshot(); len(launches) != 0 {
		t.Fatalf("校验失败不得拉起载体: %+v", launches)
	}
	// 旧形态 {title} 不受影响（版本化增量）。
	if code, raw := ledgerPost(t, env.testAgentdEnv, "/api/sessions", `{"title":"旧形态"}`); code != 200 || strings.Contains(raw, "resident") {
		t.Fatalf("旧形态建会话应照旧 200 且无 resident 键: %d %s", code, raw)
	}
}

// A2.4 首轮失败（Q3）：Launch 报错 → 200、resident.error、会话与卡保留、驻场位空、会话里有系统指针行；
// 随后 resident/launch 重试成功（Q4，同一编排函数）；再叫一次 → 409 已有驻场者。
func TestB444SessionCreateResidentFailureThenRetry(t *testing.T) {
	env, runner := newB444CreateEnv(t, true)
	card := b444Card(t, env, "失败场的卡", "handoff")
	runner.setLaunchErr(errors.New("载体登录失效"))
	code, resp, keys := b444Create(t, env, fmt.Sprintf(`{"project":"handoff","cards":[%q],"squad":"coord"}`, card))
	if code != http.StatusOK {
		t.Fatalf("首轮失败仍应 200，得 %d %s", code, keys["error"])
	}
	if resp.Resident == nil || resp.Resident.Identity != "" || !strings.Contains(resp.Resident.Error, "载体登录失效") {
		t.Fatalf("响应应只带 resident.error: %+v", resp.Resident)
	}
	if owner, _ := env.ledger.SessionOfCard(card); owner != resp.ID {
		t.Fatalf("首轮失败卡仍应在会话里，得 %q", owner)
	}
	if _, ok, err := env.ledger.SessionResident(resp.ID); err != nil || ok {
		t.Fatalf("首轮失败驻场位应为空: ok=%v err=%v", ok, err)
	}
	pointers := b444Pointers(t, env, resp.ID)
	if len(pointers) != 1 || !strings.Contains(pointers[0], "resident launch") {
		t.Fatalf("会话里应恰一条指路重试的系统指针行，得 %q", pointers)
	}

	runner.setLaunchErr(nil)
	code, raw := ledgerPost(t, env.testAgentdEnv, "/api/sessions/"+resp.ID+"/resident/launch", `{"squad":"coord"}`)
	if code != http.StatusOK {
		t.Fatalf("重试叫驻场者应 200，得 %d %s", code, raw)
	}
	var outcome proto.SessionResidentOutcome
	if err := json.Unmarshal([]byte(raw), &outcome); err != nil || outcome.Identity != b444Identity {
		t.Fatalf("重试响应应带 identity: %s err=%v", raw, err)
	}
	if r, ok, _ := env.ledger.SessionResident(resp.ID); !ok || r.DriverMode != ledger.ResidentDriverPush ||
		r.CarrierMachine != b444WantMachine(t, env) {
		t.Fatalf("重试后驻场行应 push 且机器名真实: ok=%v %+v", ok, r)
	}
	if code, raw := ledgerPost(t, env.testAgentdEnv, "/api/sessions/"+resp.ID+"/resident/launch", `{}`); code != http.StatusConflict {
		t.Fatalf("已有驻场者再叫应 409，得 %d %s", code, raw)
	}
	if code, _ := ledgerPost(t, env.testAgentdEnv, "/api/sessions/session:999/resident/launch", `{}`); code != http.StatusNotFound {
		t.Fatalf("不存在会话叫驻场者应 404，得 %d", code)
	}
	if launches, _, _ := runner.snapshot(); len(launches) != 2 {
		t.Fatalf("409/404 不得拉起载体：Launch 应恰 2 次，得 %d", len(launches))
	}
}

// A2.5 HTTP 改名（Q6）：群主改名 200 + timeline renamed；expect_title 不符 409 且标题不变；非群主非驻场者
// 403 且标题不变；空标题 400；不存在 404。
func TestB444SessionRenameEndpoint(t *testing.T) {
	env, _ := newB444CreateEnv(t, true)
	code, resp, keys := b444Create(t, env, `{"project":"handoff","squad":"coord"}`)
	if code != 200 {
		t.Fatalf("建会话: %d %s", code, keys["error"])
	}
	path := "/api/sessions/" + resp.ID + "/rename"
	if code, raw := ledgerPost(t, env.testAgentdEnv, path, `{"title":"人先起名","expect_title":"新会话"}`); code != 200 {
		t.Fatalf("群主 CAS 改名应 200: %d %s", code, raw)
	}
	if code, raw := ledgerPost(t, env.testAgentdEnv, path, `{"title":"驻场者起名","expect_title":"新会话"}`); code != http.StatusConflict {
		t.Fatalf("expect_title 不符应 409: %d %s", code, raw)
	}
	if code, _ := ledgerPost(t, env.testAgentdEnv, path, `{"title":"  "}`); code != http.StatusBadRequest {
		t.Fatalf("空标题应 400: %d", code)
	}
	if code, _ := ledgerPost(t, env.testAgentdEnv, "/api/sessions/session:999/rename", `{"title":"x"}`); code != http.StatusNotFound {
		t.Fatalf("不存在会话改名应 404: %d", code)
	}
	code, raw := ledgerGet(t, env.testAgentdEnv, "/api/sessions/"+resp.ID)
	if code != 200 || !strings.Contains(raw, `"title":"人先起名"`) || !strings.Contains(raw, `"kind":"renamed"`) {
		t.Fatalf("详情应显示新标题与 renamed timeline 行: %d %s", code, raw)
	}

	other, err := env.srv.rooms.CreateSession("别人的会话", "user:other", "user:other")
	if err != nil {
		t.Fatal(err)
	}
	if code, raw := ledgerPost(t, env.testAgentdEnv, "/api/sessions/"+other.ID+"/rename", `{"title":"抢名"}`); code != http.StatusForbidden {
		t.Fatalf("非群主非驻场者改名应 403: %d %s", code, raw)
	}
	if got, _ := env.ledger.GetSession(other.ID); got.Title != "别人的会话" {
		t.Fatalf("403 后标题不得变: %q", got.Title)
	}
}

// A2.6 序列化边界（S8）：GET /api/sessions 里旧会话行**无** home_project 键，新会话行有。
func TestB444SessionsListHomeProjectKeyPresence(t *testing.T) {
	env, _ := newB444CreateEnv(t, true)
	legacy, err := env.srv.rooms.CreateSession("旧会话", consoleMember, consoleMember)
	if err != nil {
		t.Fatal(err)
	}
	_, homed, keys := b444Create(t, env, `{"title":"项目会话","project":"handoff","squad":"coord"}`)
	if homed.ID == "" {
		t.Fatalf("建项目会话失败: %s", keys["error"])
	}
	code, raw := ledgerGet(t, env.testAgentdEnv, "/api/sessions")
	if code != 200 {
		t.Fatalf("GET /api/sessions: %d %s", code, raw)
	}
	var list struct {
		Sessions []map[string]json.RawMessage `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, row := range list.Sessions {
		var id string
		_ = json.Unmarshal(row["id"], &id)
		_, has := row["home_project"]
		switch id {
		case legacy.ID:
			seen++
			if has {
				t.Fatalf("旧会话行不得有 home_project 键: %s", row["home_project"])
			}
		case homed.ID:
			seen++
			if string(row["home_project"]) != `"handoff"` {
				t.Fatalf("新会话行 home_project 应为 handoff，得 %s", row["home_project"])
			}
		}
	}
	if seen != 2 {
		t.Fatalf("列表应含两场会话，命中 %d", seen)
	}
}

// R9 另一分支：载体登记的是真实机器名（hostname）→ 驻场行原样记它。
func TestB444ResidentCarrierMachineKeepsRegisteredName(t *testing.T) {
	env, _ := newB444CreateEnv(t, false)
	host, err := os.Hostname()
	if err != nil || host == "" {
		t.Skip("本机无 hostname")
	}
	allowCarrierMachines(t, env.srv, host)
	svc := mustScheduling(t, env.srv)
	putOnlineCarrier(t, svc, scheduling.Carrier{Name: "host-carrier", Machine: host, CLI: "opencode",
		HomeDir: "/tmp/host-home", Credential: scheduling.CredentialStandalone, MaxConcurrency: 1})
	if err := svc.PutSquad(scheduling.Squad{Name: "hostq", Role: scheduling.RoleCoordinator,
		Members: []scheduling.SquadMember{{Carrier: "host-carrier", MaxConcurrency: 1}}}, 0); err != nil {
		t.Fatal(err)
	}
	code, resp, keys := b444Create(t, env, `{"project":"handoff","squad":"hostq"}`)
	if code != 200 || resp.Resident == nil || resp.Resident.Identity != "agent:hostq" {
		t.Fatalf("建会话: %d %+v %s", code, resp.Resident, keys["error"])
	}
	if r, _, _ := env.ledger.SessionResident(resp.ID); r.CarrierMachine != host {
		t.Fatalf("登记名为真实机器名时应原样记 %q，得 %q", host, r.CarrierMachine)
	}
}

var b444SessionIDPattern = regexp.MustCompile(`session:\d+`)

// 启动清扫（协调者裁决：agentd 崩溃留下的首轮在途行）：本机在途行 → 请下 + 一条「被重启打断」指针行（带重试
// 命令）；别机在途行（R9）与已补写的本机 push 行 → 原样不动、零指针行。
func TestB444StartupSweepStandsInterruptedInflightResidents(t *testing.T) {
	env, _ := newB444CreateEnv(t, true)
	mk := func(title string) string {
		t.Helper()
		s, err := env.srv.rooms.CreateSessionInProject(title, consoleMember, "handoff", consoleMember)
		if err != nil {
			t.Fatal(err)
		}
		return s.ID
	}
	self, other, filled := mk("本机在途"), mk("别机在途"), mk("本机已补写")
	sit := func(session, machine, push string) {
		t.Helper()
		if _, err := env.srv.rooms.SitResident(proto.SessionResident{Session: session, Identity: b444Identity,
			DriverMode: proto.ResidentDriverPush, PushCLI: "opencode", PushSession: push,
			Carrier: "coord-carrier", CarrierMachine: machine}); err != nil {
			t.Fatalf("坐驻场行 %s: %v", session, err)
		}
	}
	sit(self, b444WantMachine(t, env), "")
	sit(other, "linux-99-elsewhere", "")
	sit(filled, b444WantMachine(t, env), "ses_live")

	env.srv.sweepInterruptedResidents()

	if _, ok, err := env.ledger.SessionResident(self); err != nil || ok {
		t.Fatalf("本机在途行应被请下: ok=%v err=%v", ok, err)
	}
	pointers := b444Pointers(t, env, self)
	if len(pointers) != 1 || !strings.Contains(pointers[0], "重启") ||
		!strings.Contains(pointers[0], "handoff session resident launch "+self+" --squad coord") {
		t.Fatalf("本机在途会话应恰一条「被重启打断」指针行并给重试命令，得 %q", pointers)
	}
	if r, ok, _ := env.ledger.SessionResident(other); !ok || r.PushSession != "" || len(b444Pointers(t, env, other)) != 0 {
		t.Fatalf("别机在途行不得动（R9）: ok=%v %+v", ok, r)
	}
	if r, ok, _ := env.ledger.SessionResident(filled); !ok || r.PushSession != "ses_live" || len(b444Pointers(t, env, filled)) != 0 {
		t.Fatalf("已补写的 push 行不得动: ok=%v %+v", ok, r)
	}
	// 幂等：再扫一次不重复落指针行。
	env.srv.sweepInterruptedResidents()
	if n := len(b444Pointers(t, env, self)); n != 1 {
		t.Fatalf("再扫不得重复落指针行，得 %d", n)
	}
}

// F1 竖切（真机 R4 回归）：首轮「正在跑」时驻场者已坐下（push_session 空 = 在途），能以 agent:<小队> 经
// 真实 collab 写面在会话里发言、按占位名 CAS 改名；首轮返回后 push_session 被补写。
// 注意：钩子跑在 HTTP handler 的 goroutine 里，只记录不 Fatal。
func TestB444ResidentSpeaksAndRenamesInFirstRound(t *testing.T) {
	env, runner := newB444CreateEnv(t, true)
	var sendErr, renameErr error
	var during ledger.SessionResident
	var seatedDuring bool
	runner.onLaunch = func(prompt string) {
		session := b444SessionIDPattern.FindString(prompt)
		during, seatedDuring, _ = env.ledger.SessionResident(session)
		_, sendErr = env.srv.rooms.Send(session, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "我是驻场者，先看看"}, b444Identity)
		_, renameErr = env.srv.rooms.RenameSession(session, "排查登录", proto.SessionUntitledTitle, b444Identity)
	}
	code, resp, keys := b444Create(t, env, `{"project":"handoff","squad":"coord"}`)
	if code != http.StatusOK || resp.Resident == nil || resp.Resident.Error != "" {
		t.Fatalf("建会话: %d %+v %s", code, resp.Resident, keys["error"])
	}
	if !seatedDuring || during.DriverMode != ledger.ResidentDriverPush || during.PushSession != "" || during.Identity != b444Identity {
		t.Fatalf("首轮在跑时驻场行应已坐下且 push_session 空（在途），得 seated=%v %+v", seatedDuring, during)
	}
	if sendErr != nil {
		t.Fatalf("首轮里驻场者以 %s 发言应通过书写者检查: %v", b444Identity, sendErr)
	}
	if renameErr != nil {
		t.Fatalf("首轮里驻场者 CAS 改名应成功: %v", renameErr)
	}
	got, err := env.ledger.GetSession(resp.ID)
	if err != nil || got.Title != "排查登录" {
		t.Fatalf("首轮改名应落账: %+v err=%v", got, err)
	}
	if r, ok, _ := env.ledger.SessionResident(resp.ID); !ok || r.PushSession != "ses_b444_create_1" {
		t.Fatalf("首轮返回后应补写 push_session，得 ok=%v %+v", ok, r)
	}
}

// F1 唤醒腿（裁决 c）：首轮在途时人发无 @ 消息 → 本轮不续轮、不落指针行、不越过；首轮返回补写 push_session
// 后下一轮恰一次 Resume，目标就是首轮会话。
func TestB444MessageDuringFirstRoundDeliveredAfterPushSessionSet(t *testing.T) {
	env, runner := newB444CreateEnv(t, true)
	var sendErr, drainErr error
	var resumesDuring, pointersDuring int
	runner.onLaunch = func(prompt string) {
		session := b444SessionIDPattern.FindString(prompt)
		if _, sendErr = env.srv.rooms.Send(session, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "首轮里人先说一句"}, consoleMember); sendErr != nil {
			return
		}
		_, _, drainErr = env.srv.consumeAutomationEventsOnce(context.Background())
		runner.mu.Lock()
		resumesDuring = len(runner.resumes)
		runner.mu.Unlock()
		history, _ := env.srv.rooms.History(session, 0, 500)
		for _, ev := range history {
			var msg proto.RoomMessage
			if json.Unmarshal(ev.Payload, &msg) == nil && msg.Kind == proto.RoomMsgPointer {
				pointersDuring++
			}
		}
	}
	code, resp, keys := b444Create(t, env, `{"project":"handoff","squad":"coord"}`)
	if code != http.StatusOK || resp.Resident == nil || resp.Resident.Error != "" {
		t.Fatalf("建会话: %d %+v %s", code, resp.Resident, keys["error"])
	}
	if sendErr != nil || drainErr != nil {
		t.Fatalf("首轮里人发言/排水不应报错: send=%v drain=%v", sendErr, drainErr)
	}
	if resumesDuring != 0 || pointersDuring != 0 {
		t.Fatalf("首轮在途不得续轮、不得落指针行，得 resumes=%d pointers=%d", resumesDuring, pointersDuring)
	}
	runner.mu.Lock()
	runner.onLaunch = nil
	runner.mu.Unlock()
	drainAutomation(t, env)
	_, _, resumes := runner.snapshot()
	if len(resumes) != 1 || resumes[0].SessionID != "ses_b444_create_1" {
		t.Fatalf("补写 push_session 后应恰一次续到首轮会话，得 %+v", resumes)
	}
	drainAutomation(t, env)
	if _, _, again := runner.snapshot(); len(again) != 1 {
		t.Fatalf("同一消息不得重复续轮，得 %d 次", len(again))
	}
}

// 裁决 d：驻场行在途（push_session 空）时再叫驻场者 → 409，不拉起载体。
func TestB444ResidentLaunchConflictsWhileInflight(t *testing.T) {
	env, runner := newB444CreateEnv(t, true)
	session, err := env.srv.rooms.CreateSessionInProject("在途场", consoleMember, "handoff", consoleMember)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.srv.rooms.SitResident(proto.SessionResident{Session: session.ID, Identity: b444Identity,
		DriverMode: proto.ResidentDriverPush, PushCLI: "opencode", Carrier: "coord-carrier", CarrierMachine: b444WantMachine(t, env)}); err != nil {
		t.Fatalf("坐在途驻场行: %v", err)
	}
	if code, raw := ledgerPost(t, env.testAgentdEnv, "/api/sessions/"+session.ID+"/resident/launch", `{"squad":"coord"}`); code != http.StatusConflict {
		t.Fatalf("在途驻场行再叫应 409，得 %d %s", code, raw)
	}
	if launches, _, _ := runner.snapshot(); len(launches) != 0 {
		t.Fatalf("409 不得拉起载体: %+v", launches)
	}
}
