package agentd

// 编制域 HTTP 面的 wire 往返回归（B156.3 K3 Task C，契约 §9 测试义务③ CRUD 半边）。
//
// 全部断言穿过真实 Handler() 的 JSON 边界（httptest 起 Server，Bearer 走 testToken），
// 不是内存直调：序列化结果里的键名、omitempty 缺席、空数组非 null，只有在真实
// 序列化产物上才验得到。重复键双判据落在 TC2/TC4：既点数关键键出现次数，又解码
// 回读逐字段比对（补充纪律 #9）。
//
// 409 依赖声明：registry 层冲突包装 ledger.ErrCASConflict、组装点适配器把它翻成
// schedclient.ErrCASConflict 的翻译归 K2（已在本树），本卡不改写；TC2 的 409 断言
// 即其消费侧回归。

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// schedEnv 是本文件的专用夹具：真实 Handler + 已装配的编制域 + 干净账本。
type schedEnv struct {
	*testAgentdEnv
	svc *scheduling.Service
}

func newSchedEnv(t *testing.T) *schedEnv {
	t.Helper()
	st, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	env := newTestAgentdEnvWithCfg(t, &config.Config{
		Token: testToken,
		Targets: map[string]config.Target{
			"m1": {Addr: "127.0.0.1:1", Token: testToken},
			"m":  {Addr: "127.0.0.1:1", Token: testToken},
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	env.srv.SetLedger(st)
	env.srv.SetupAutomation(st)
	return &schedEnv{testAgentdEnv: env, svc: env.srv.Scheduling()}
}

// newSchedNoPTYEnv 复用 no-PTY 真实账本夹具，保持 Handler/Bearer/JSON 链路不变；
// 只绕开受限环境无法创建 PTY 测试根目录的无关门禁。
func newSchedNoPTYEnv(t *testing.T) *schedEnv {
	t.Helper()
	env := newNoPTYLedgerEnv(t)
	configPath := t.TempDir() + "/config.yaml"
	if err := config.Save(configPath, env.srv.conf()); err != nil {
		t.Fatalf("准备配置: %v", err)
	}
	env.srv.SetConfigPath(configPath)
	env.srv.SetupAutomation(env.ledger)
	if err := env.srv.swapConf(func(c *config.Config) error {
		c.Targets["m1"] = config.Target{Addr: "127.0.0.1:1", Token: testToken}
		c.Targets["m"] = config.Target{Addr: "127.0.0.1:1", Token: testToken}
		return nil
	}); err != nil {
		t.Fatalf("测试 targets: %v", err)
	}
	return &schedEnv{testAgentdEnv: env.testAgentdEnv, svc: env.srv.Scheduling()}
}

// schedReq 发一个带 Bearer 的任意方法请求，回 (状态码, 响应体)。
func schedReq(t *testing.T, env *schedEnv, method, path, body string) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, env.ts.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(data)
}

// TC1：空库 GET 是合法态——200 + 两个空数组（非 null），这是 breakdown K3
// 门禁判据的正向主判据。它的强制正控是 TC2（登记之后同一请求返回非空），
// 两支读数都要落台账。
func TestSquadsGetEmptyIsLegalState(t *testing.T) {
	env := newSchedEnv(t)
	code, body := schedReq(t, env, http.MethodGet, "/api/squads", "")
	if code != http.StatusOK {
		t.Fatalf("空库 GET 应 200，得 %d：%s", code, body)
	}
	if !strings.Contains(body, `"carriers":[]`) || !strings.Contains(body, `"squads":[]`) {
		t.Fatalf("空集合必须是 [] 而非 null：%s", body)
	}
	var resp proto.SquadsResp
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("解码: %v", err)
	}
	if resp.Carriers == nil || resp.Squads == nil {
		t.Fatalf("解码后切片不得为 nil（null 会把前端引向报错分支）：%s", body)
	}
}

// TC2：载体 PUT→GET 往返穿过真实 JSON 边界；重复键双判据（version 恰一次 +
// 解码回读相等）；CAS 409 与缺 expect 400 各一支。409 能到达的前提是组装点
// 适配器的 CAS 翻译在场（K2 已落地）——它翻红时先查 translateRegistryErr。
func TestCarrierPutRoundtripThroughWire(t *testing.T) {
	env := newSchedEnv(t)
	body := `{"machine":"m1","cli":"opencode","home_dir":"/h","credential":"standalone","max_concurrency":2}`
	code, respBody := schedReq(t, env, http.MethodPut, "/api/squads/carriers/c1?expect=0", body)
	if code != http.StatusOK {
		t.Fatalf("新建应 200，得 %d：%s", code, respBody)
	}
	if !strings.Contains(respBody, `"version":1`) {
		t.Fatalf("新建应回 version=1：%s", respBody)
	}
	code, respBody = schedReq(t, env, http.MethodGet, "/api/squads", "")
	if code != http.StatusOK {
		t.Fatalf("GET 应 200，得 %d", code)
	}
	// 重复键点数：单载体响应里 version 键恰好一次（多一处即双写缺陷）。
	if n := strings.Count(respBody, `"version":`); n != 1 {
		t.Fatalf("version 键出现 %d 次，want 恰 1：%s", n, respBody)
	}
	var view proto.SquadsResp
	if err := json.Unmarshal([]byte(respBody), &view); err != nil {
		t.Fatalf("解码: %v", err)
	}
	if len(view.Carriers) != 1 {
		t.Fatalf("载体行数 %d ≠ 1", len(view.Carriers))
	}
	c := view.Carriers[0]
	want := proto.CarrierView{Name: "c1", Machine: "m1", CLI: "opencode",
		HomeDir: "/h", Credential: "standalone", MaxConcurrency: 2,
		Status: "pending", Version: 1}
	if c != want {
		t.Fatalf("往返不等:\n got=%+v\nwant=%+v", c, want)
	}
}

// B293：运行命令由服务端按已存 HOME×CLI 生成，客户端不得拼接。
func TestCarrierRunCommandThroughWire(t *testing.T) {
	env := newSchedEnv(t)
	body := `{"machine":"m1","cli":"codex","home_dir":"~/.handoff/home/c1","credential":"standalone"}`
	code, respBody := schedReq(t, env, http.MethodPut, "/api/squads/carriers/c1?expect=0", body)
	if code != http.StatusOK {
		t.Fatalf("新建应 200，得 %d：%s", code, respBody)
	}
	code, respBody = schedReq(t, env, http.MethodGet, "/api/squads/carriers/c1/run-command", "")
	if code != http.StatusOK {
		t.Fatalf("run-command 应 200，得 %d：%s", code, respBody)
	}
	var got proto.CarrierRunCommandResp
	if err := json.Unmarshal([]byte(respBody), &got); err != nil {
		t.Fatalf("解码: %v", err)
	}
	if got.Command != "HOME=~/.handoff/home/c1 codex" {
		t.Fatalf("command = %q", got.Command)
	}
	code, respBody = schedReq(t, env, http.MethodGet, "/api/squads/carriers/missing/run-command", "")
	if code != http.StatusNotFound {
		t.Fatalf("缺失载体应 404，得 %d：%s", code, respBody)
	}
	// 更新：expect=1 → version=2。
	code, _ = schedReq(t, env, http.MethodPut, "/api/squads/carriers/c1?expect=1",
		`{"machine":"m1","cli":"opencode","home_dir":"/h2","credential":"standalone"}`)
	if code != http.StatusOK {
		t.Fatalf("expect=1 更新应 200，得 %d", code)
	}
	// 过期 expect → 409（正文来自 schedclient 哨兵经组装点翻译后的文案）。
	code, rb := schedReq(t, env, http.MethodPut, "/api/squads/carriers/c1?expect=99",
		`{"machine":"m1","cli":"opencode","credential":"standalone"}`)
	if code != http.StatusConflict ||
		(!strings.Contains(rb, "版本冲突") && !strings.Contains(rb, "冲突")) {
		t.Fatalf("过期 expect 应 409 带冲突文案，得 %d：%s", code, rb)
	}
	// 缺 expect → 400 且点名参数（不许「没带就当 0」的静默语义）。
	code, rb = schedReq(t, env, http.MethodPut, "/api/squads/carriers/c1",
		`{"machine":"m1","cli":"opencode","credential":"standalone"}`)
	if code != http.StatusBadRequest || !strings.Contains(rb, "expect") {
		t.Fatalf("缺 expect 应 400 点名参数，得 %d：%s", code, rb)
	}
	// 非法凭据来源：400 且文案点名两个合法词（400 报文可行动的门禁半边）。
	// 报文必须带「登记校验未过」前缀——证明拒因来自域内 PutCarrier 的
	// ErrInvalid 臂，而不是网关的词表预检（Major-2 收敛后网关无词表校验）。
	code, rb = schedReq(t, env, http.MethodPut, "/api/squads/carriers/c2?expect=0",
		`{"machine":"m","cli":"cli","credential":"boss"}`)
	if code != http.StatusBadRequest ||
		!strings.Contains(rb, "登记校验未过") ||
		!strings.Contains(rb, "standalone") || !strings.Contains(rb, "main_home_sync") {
		t.Fatalf("非法 credential 应 400 点名词表且源自域校验，得 %d：%s", code, rb)
	}
}

// TC3：小队校验（成员必须存在、角色词表）走 400；合法创建后 GET 可见成员集。
// 本测试 400 断言的正控 = 同一 handler 在 TC2 的成功路径（能 200 才谈得上拒得对）。
func TestSquadPutValidatesMembersAndRole(t *testing.T) {
	env := newSchedEnv(t)
	code, rb := schedReq(t, env, http.MethodPut, "/api/squads/squads/sq?expect=0",
		`{"role":"executor","members":[{"carrier":"ghost"}]}`)
	if code != http.StatusBadRequest || !strings.Contains(rb, "ghost") {
		t.Fatalf("幽灵成员应 400 点名成员，得 %d：%s", code, rb)
	}
	code, rb = schedReq(t, env, http.MethodPut, "/api/squads/squads/sq?expect=0",
		`{"role":"boss","members":[]}`)
	if code != http.StatusBadRequest || !strings.Contains(rb, "登记校验未过") ||
		!strings.Contains(rb, "executor") {
		t.Fatalf("非法角色应 400 点名词表且源自域校验，得 %d：%s", code, rb)
	}
	code, _ = schedReq(t, env, http.MethodPut, "/api/squads/carriers/c1?expect=0",
		`{"machine":"m1","cli":"opencode","credential":"standalone"}`)
	if code != http.StatusOK {
		t.Fatalf("前置载体登记失败 %d", code)
	}
	code, _ = schedReq(t, env, http.MethodPut, "/api/squads/squads/sq?expect=0",
		`{"role":"coordinator","members":[{"carrier":"c1"}]}`)
	if code != http.StatusOK {
		t.Fatalf("合法创建应 200，得 %d", code)
	}
	_, rb = schedReq(t, env, http.MethodGet, "/api/squads", "")
	var view proto.SquadsResp
	if err := json.Unmarshal([]byte(rb), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Squads) != 1 || view.Squads[0].Name != "sq" ||
		view.Squads[0].Role != "coordinator" || len(view.Squads[0].Members) != 1 ||
		view.Squads[0].Members[0].Carrier != "c1" || view.Squads[0].Version != 1 {
		t.Fatalf("小队行不符: %+v", view.Squads)
	}
}

// TestSquadPutMemberPolicyRoundtripThroughWire 穿真实 Handler 验收成员政策对象：
// 有值保留、缺席/零值不造键、旧队级 max_concurrency 被忽略，空成员数组不是 null。
// 日志断言同时锁住成功出口的政策计数；本测试不直调 scheduling 投影 helper。
func TestSquadPutMemberPolicyRoundtripThroughWire(t *testing.T) {
	env := newSchedNoPTYEnv(t)
	var logs bytes.Buffer
	env.srv.log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	for _, name := range []string{"c1", "c2"} {
		code, body := schedReq(t, env, http.MethodPut, "/api/squads/carriers/"+name+"?expect=0",
			`{"machine":"m1","cli":"opencode","credential":"standalone"}`)
		if code != http.StatusOK {
			t.Fatalf("登记载体 %s 失败 %d: %s", name, code, body)
		}
	}
	code, body := schedReq(t, env, http.MethodPut, "/api/squads/squads/sq?expect=0",
		`{"role":"executor","max_concurrency":9,"members":[{"carrier":"c1","max_concurrency":2},{"carrier":"c2"}]}`)
	if code != http.StatusOK {
		t.Fatalf("登记成员政策小队失败 %d: %s", code, body)
	}
	if !strings.Contains(logs.String(), "msg=小队登记成功") ||
		!strings.Contains(logs.String(), "member_policy_count=1") || !strings.Contains(logs.String(), "empty_members=false") {
		t.Fatalf("成功日志缺少成员政策上下文: %q", logs.String())
	}

	code, body = schedReq(t, env, http.MethodGet, "/api/squads", "")
	if code != http.StatusOK {
		t.Fatalf("读取小队失败 %d: %s", code, body)
	}
	var resp proto.SquadsResp
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("解码小队响应: %v", err)
	}
	if len(resp.Squads) != 1 || len(resp.Squads[0].Members) != 2 ||
		resp.Squads[0].Members[0] != (proto.SquadMember{Carrier: "c1", MaxConcurrency: 2}) ||
		resp.Squads[0].Members[1] != (proto.SquadMember{Carrier: "c2"}) {
		t.Fatalf("成员政策往返不等: %+v", resp.Squads)
	}
	if strings.Contains(body, `"max_concurrency":9`) {
		t.Fatalf("旧队级总帽不应出现在响应: %s", body)
	}
	if strings.Count(body, `"max_concurrency":2`) != 1 || strings.Count(body, `"max_concurrency"`) != 1 {
		t.Fatalf("成员政策键出现次数不符: %s", body)
	}

	code, body = schedReq(t, env, http.MethodPut, "/api/squads/squads/empty?expect=0",
		`{"role":"executor","members":[]}`)
	if code != http.StatusOK {
		t.Fatalf("空队登记失败 %d: %s", code, body)
	}
	code, body = schedReq(t, env, http.MethodGet, "/api/squads", "")
	if code != http.StatusOK || !strings.Contains(body, `"members":[]`) || strings.Contains(body, `"members":null`) {
		t.Fatalf("空队 wire 不符 %d: %s", code, body)
	}

	code, body = schedReq(t, env, http.MethodPut, "/api/squads/squads/ghost?expect=0",
		`{"role":"executor","members":[{"carrier":"missing"}]}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "missing") {
		t.Fatalf("幽灵成员仍应 400: %d %s", code, body)
	}
}

// TC4：queue 读面。种子两队列三行（含 Ready=false 行），断言：launch 整队在前、
// 位次连续、重复键双判据（"\"ready\":" 恰 3 次 + 解码回读逐项相等）。
func TestQueueGetOrdersByDrainSequence(t *testing.T) {
	env := newSchedEnv(t)
	seeds := []struct {
		kind string
		req  scheduling.IgnitionRequest
	}{
		{scheduling.KindIgnitionQueue, scheduling.IgnitionRequest{Card: "B1",
			Squad: "s", Node: "impl", Priority: "高", Ready: false, Actor: "a"}},
		{scheduling.KindLaunchQueue, scheduling.IgnitionRequest{Card: "B2",
			Squad: "coord", Priority: "低", Ready: false, Actor: "b"}},
		{scheduling.KindIgnitionQueue, scheduling.IgnitionRequest{Card: "B3",
			Squad: "s", Priority: "低", Ready: true, Actor: "c"}},
	}
	for _, s := range seeds {
		if _, err := env.svc.Enqueue(s.req, s.kind); err != nil {
			t.Fatalf("入队 %s: %v", s.req.Card, err)
		}
	}
	code, rb := schedReq(t, env, http.MethodGet, "/api/queue", "")
	if code != http.StatusOK {
		t.Fatalf("GET 应 200，得 %d：%s", code, rb)
	}
	if n := strings.Count(rb, `"ready":`); n != 3 {
		t.Fatalf(`"ready" 键出现 %d 次，want 恰 3（每行一次，缺失会被静默当 false）：%s`, n, rb)
	}
	var view proto.QueueResp
	if err := json.Unmarshal([]byte(rb), &view); err != nil {
		t.Fatalf("解码: %v", err)
	}
	type wantRow struct {
		kind, card string
		pos        int
		ready      bool
		priority   string
	}
	wants := []wantRow{
		{scheduling.KindLaunchQueue, "B2", 1, false, "低"},
		{scheduling.KindIgnitionQueue, "B3", 2, true, "低"},
		{scheduling.KindIgnitionQueue, "B1", 3, false, "高"},
	}
	if len(view.Queue) != len(wants) {
		t.Fatalf("行数 %d ≠ %d：%s", len(view.Queue), len(wants), rb)
	}
	for i, w := range wants {
		g := view.Queue[i]
		if g.Kind != w.kind || g.Card != w.card || g.Position != w.pos ||
			g.Ready != w.ready || g.Priority != w.priority {
			t.Fatalf("第 %d 行不符:\n got=%+v\nwant=%+v", i+1, g, w)
		}
		if g.ID == "" || g.Squad == "" || g.Actor == "" {
			t.Fatalf("元数据字段不得为空: %+v", g)
		}
	}
}

// TC5：鉴权门禁（breakdown K3：端点过既有鉴权中间件）。否定断言（无 token 401）
// 的强制正控 = 同一路径带 token 必须 200，两个读数都落台账。
func TestSchedulingEndpointsRequireAuth(t *testing.T) {
	env := newSchedEnv(t)
	req, err := http.NewRequest(http.MethodGet, env.ts.URL+"/api/squads", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无 token 应 401，得 %d（正控读数见下一行）", resp.StatusCode)
	}
	code, _ := schedReq(t, env, http.MethodGet, "/api/squads", "")
	if code != http.StatusOK {
		t.Fatalf("正控：带 token 同路径应 200，得 %d", code)
	}
}

// TC7：空白名（用户可修错误）必须 400 而非 500——Major-3 判据的 wire 半臂。
// 名来自路径 {name}，网关不预检它，得靠域内 PutCarrier/PutSquad 的校验行
// 以 %w 包 ErrInvalid 后经 schedPutErr 落到 400；此前裸 fmt.Errorf 上浮
// default→500。成员引用缺失的 400 分类由 TC3 锁（ErrNotFound 臂）。
func TestCarrierPutBlankNameIs400(t *testing.T) {
	env := newSchedEnv(t)
	code, rb := schedReq(t, env, http.MethodPut, "/api/squads/carriers/%20?expect=0",
		`{"machine":"m1","cli":"opencode","credential":"standalone"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("空白载体名应 400，得 %d：%s", code, rb)
	}
}

func TestSquadPutBlankNameIs400(t *testing.T) {
	env := newSchedEnv(t)
	code, rb := schedReq(t, env, http.MethodPut, "/api/squads/squads/%20?expect=0",
		`{"role":"executor","members":[]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("空白小队名应 400，得 %d：%s", code, rb)
	}
}

// TC8：machine/cli 必填的 400 分类随 Major-2 收敛到域内——网关不再预检，
// 报文带「登记校验未过」前缀证明拒因来自 PutCarrier 的 ErrInvalid 臂。
// 这是被删的三块 wire 预检里「必填字段」那块在 wire 侧的收敛证明。
func TestCarrierPutMissingRequiredFieldsIs400(t *testing.T) {
	env := newSchedEnv(t)
	for name, body := range map[string]string{
		"缺 machine": `{"cli":"opencode","credential":"standalone"}`,
		"缺 cli":     `{"machine":"m1","credential":"standalone"}`,
	} {
		code, rb := schedReq(t, env, http.MethodPut, "/api/squads/carriers/cx?expect=0", body)
		if code != http.StatusBadRequest || !strings.Contains(rb, "登记校验未过") {
			t.Fatalf("%s 应 400 且源自域校验，得 %d：%s", name, code, rb)
		}
	}
}

func TestCarrierPutUnknownMachineIs400(t *testing.T) {
	env := newSchedEnv(t)
	code, rb := schedReq(t, env, http.MethodPut, "/api/squads/carriers/ghost?expect=0",
		`{"machine":"ghost-box","cli":"opencode","credential":"standalone"}`)
	if code != http.StatusBadRequest || !strings.Contains(rb, "登记校验未过") ||
		!strings.Contains(rb, "ghost-box") || !strings.Contains(rb, "targets") {
		t.Fatalf("未知机器应 400 点名机器与 targets，得 %d：%s", code, rb)
	}
}

func TestCarrierPutEmptyCredentialDefaultsStandalone(t *testing.T) {
	env := newSchedEnv(t)
	code, rb := schedReq(t, env, http.MethodPut, "/api/squads/carriers/plain?expect=0",
		`{"machine":"本机","cli":"grok"}`)
	if code != http.StatusOK {
		t.Fatalf("空凭据本机载体应 200，得 %d：%s", code, rb)
	}
	_, body := schedReq(t, env, http.MethodGet, "/api/squads", "")
	if !strings.Contains(body, `"name":"plain"`) || !strings.Contains(body, `"credential":"standalone"`) {
		t.Fatalf("空凭据应落 standalone：%s", body)
	}
}

// TC6：未装配降级——SetLedger 有、SetupAutomation 没跑 → 503 可行动文案。
func TestSchedulingEndpointsDegradedWithoutSetup(t *testing.T) {
	st, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	env := newTestAgentdEnv(t)
	env.srv.SetLedger(st) // 刻意不 SetupAutomation
	only := &schedEnv{testAgentdEnv: env}
	code, rb := schedReq(t, only, http.MethodGet, "/api/squads", "")
	if code != http.StatusServiceUnavailable || !strings.Contains(rb, "编制域未装配") {
		t.Fatalf("未装配应 503 带可行动文案，得 %d：%s", code, rb)
	}
}

func TestCarrierDeleteSeam(t *testing.T) {
	env := newSchedEnv(t)

	// 先建载体 c1 和小队 s1（c1 属于 s1）
	code, _ := schedReq(t, env, http.MethodPut, "/api/squads/carriers/c1?expect=0",
		`{"machine":"m1","cli":"opencode","credential":"standalone"}`)
	if code != http.StatusOK {
		t.Fatalf("登记 c1 失败")
	}
	code, _ = schedReq(t, env, http.MethodPut, "/api/squads/squads/s1?expect=0",
		`{"role":"executor","members":[{"carrier":"c1"}]}`)
	if code != http.StatusOK {
		t.Fatalf("登记 s1 失败")
	}

	// 建不在任何小队的未入队载体 c2
	code, _ = schedReq(t, env, http.MethodPut, "/api/squads/carriers/c2?expect=0",
		`{"machine":"m1","cli":"opencode","credential":"standalone"}`)
	if code != http.StatusOK {
		t.Fatalf("登记 c2 失败")
	}

	t.Run("缺少 expect 参数 400", func(t *testing.T) {
		code, rb := schedReq(t, env, http.MethodDelete, "/api/squads/carriers/c2", "")
		if code != http.StatusBadRequest || !strings.Contains(rb, "缺少 expect") {
			t.Fatalf("缺 expect 应 400，得 %d：%s", code, rb)
		}
	})

	t.Run("非法 expect 参数 400", func(t *testing.T) {
		code, rb := schedReq(t, env, http.MethodDelete, "/api/squads/carriers/c2?expect=invalid", "")
		if code != http.StatusBadRequest {
			t.Fatalf("非法 expect 应 400，得 %d：%s", code, rb)
		}
	})

	t.Run("载体不存在 404", func(t *testing.T) {
		code, rb := schedReq(t, env, http.MethodDelete, "/api/squads/carriers/nonexistent?expect=1", "")
		if code != http.StatusNotFound {
			t.Fatalf("载体不存在应 404，得 %d：%s", code, rb)
		}
	})

	t.Run("仍在小队中的载体拒绝删除 400 且文案带小队名", func(t *testing.T) {
		code, rb := schedReq(t, env, http.MethodDelete, "/api/squads/carriers/c1?expect=1", "")
		if code != http.StatusBadRequest || !strings.Contains(rb, "s1") {
			t.Fatalf("在队载体删除应 400 且含 s1，得 %d：%s", code, rb)
		}
		// 校验 c1 仍在
		_, getBody := schedReq(t, env, http.MethodGet, "/api/squads", "")
		if !strings.Contains(getBody, `"name":"c1"`) {
			t.Fatalf("c1 仍应存在：%s", getBody)
		}
	})

	t.Run("CAS 版本冲突 409", func(t *testing.T) {
		code, rb := schedReq(t, env, http.MethodDelete, "/api/squads/carriers/c2?expect=99", "")
		if code != http.StatusConflict {
			t.Fatalf("版本冲突应 409，得 %d：%s", code, rb)
		}
		// 校验 c2 仍在
		_, getBody := schedReq(t, env, http.MethodGet, "/api/squads", "")
		if !strings.Contains(getBody, `"name":"c2"`) {
			t.Fatalf("c2 仍应存在：%s", getBody)
		}
	})

	t.Run("未入队载体成功删除 200 且 GET /api/squads 不再包含", func(t *testing.T) {
		code, rb := schedReq(t, env, http.MethodDelete, "/api/squads/carriers/c2?expect=1", "")
		if code != http.StatusOK {
			t.Fatalf("未入队删除应 200，得 %d：%s", code, rb)
		}
		var resp proto.SquadPutResp
		if err := json.Unmarshal([]byte(rb), &resp); err != nil {
			t.Fatalf("解码响应失败: %v", err)
		}
		if resp.Name != "c2" || resp.Version != 1 {
			t.Fatalf("返回应为 name: c2, version: 1，实得 %+v", resp)
		}
		// GET /api/squads 不再包含 c2
		_, getBody := schedReq(t, env, http.MethodGet, "/api/squads", "")
		if strings.Contains(getBody, `"name":"c2"`) {
			t.Fatalf("删除后 GET /api/squads 不应再含 c2：%s", getBody)
		}
	})
}
