package agentd

// b448_card_test.go —— B448 S1/S3/S5/S6：卡「叫机器人」/ 换绑的首轮转交竖切与反例（plan §5 V1/V3/V4/V5/V6）。
//
// 观察点：两台真实 agentd 共享账本（b448_pair_test.go），一律打受理机 A 的真实入口端点；执行机 B 上的
// b448Runner 是唯一被允许拉起的 Runner，A 上的 Runner 断言零调用。判据钉行为：HTTP 状态与体、账本席位与
// 承载、两级名额计数、needs_human 事件条数、执行机是否发出第二跳。

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/testhttp"
)

func b448SeatIdentity(t *testing.T, session string) string {
	t.Helper()
	id, err := proto.EncodeSeatIdentity("opencode", session)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func b448NeedsHumanCount(t *testing.T, st *ledger.Store, card string) int {
	t.Helper()
	events, err := st.EventsFromAsc([]string{card}, 0, 1000)
	if err != nil {
		t.Fatalf("读卡事件: %v", err)
	}
	n := 0
	for _, ev := range events {
		if ev.Type == ledger.EvNeedsHuman {
			n++
		}
	}
	return n
}

func b448AssertEmptySeat(t *testing.T, p *b448Pair, card string) {
	t.Helper()
	c, err := p.a.ledger.GetCard(card)
	if err != nil {
		t.Fatal(err)
	}
	if c.DriverSession != "" || c.DriverSource != "" || c.DriverMode != "" {
		t.Fatalf("卡不得落席位: session=%q source=%q mode=%q", c.DriverSession, c.DriverSource, c.DriverMode)
	}
}

// b448Proxy 挂在 B 前面：记录每个请求的路径、头与原始体，可在转发前执行 before（模拟途中竞态）。
type b448Proxy struct {
	mu       sync.Mutex
	paths    []string
	bodies   []map[string]json.RawMessage
	forwards []string
	before   func(path string)
	srv      *httptest.Server
}

func newB448Proxy(t *testing.T, target *b448Host) *b448Proxy {
	t.Helper()
	u, err := url.Parse(target.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	rp := httputil.NewSingleHostReverseProxy(u)
	p := &b448Proxy{}
	p.srv = testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var keys map[string]json.RawMessage
		_ = json.Unmarshal(raw, &keys)
		p.mu.Lock()
		p.paths = append(p.paths, r.URL.Path)
		p.bodies = append(p.bodies, keys)
		p.forwards = append(p.forwards, r.Header.Get(forwardedHeader))
		before := p.before
		p.mu.Unlock()
		if before != nil {
			before(r.URL.Path)
		}
		r.Body = io.NopCloser(strings.NewReader(string(raw)))
		r.ContentLength = int64(len(raw))
		rp.ServeHTTP(w, r)
	}))
	return p
}

func (p *b448Proxy) addr() string { return strings.TrimPrefix(p.srv.URL, "http://") }

// V1（S1 成功）：载体只在 B。A 入口 200、session_id 来自 B 的 Runner；B 恰一次 Launch、A 零次；席位
// coordinate/push、承载记 B；回合中计数恰为 1（A 已在转交前归还，F32）；回合后两级归零；回执不外泄
// handled_by（F18）；workdir 在 B 解析（B 的项目位置）。
func TestB448CardLaunchTransfersToCarrierMachine(t *testing.T) {
	p := newB448Pair(t, b448PairOpts{})
	card := b448Card(t, p)
	var midSquad, midCarrier int
	p.b.runner.onLaunch = func(keysclient.SessionSpec) { midSquad, midCarrier = p.counts(t) }

	code, body := ledgerPost(t, p.a.testAgentdEnv, "/api/cards/"+card+"/coordinator/launch", `{}`)
	if code != http.StatusOK {
		t.Fatalf("跨机叫机器人应 200，得 %d %s", code, body)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &keys); err != nil {
		t.Fatal(err)
	}
	if _, ok := keys["handled_by"]; ok {
		t.Fatalf("入口回执不得外泄 handled_by（F18）: %s", body)
	}
	var resp proto.CoordinatorLaunchResp
	_ = json.Unmarshal([]byte(body), &resp)
	if !resp.Woke || resp.SessionID != "ses_t448-linux_1" {
		t.Fatalf("回执应 woke=true 且 session_id 来自执行机 Runner: %s", body)
	}
	if got := len(p.a.runner.launchSpecs()); got != 0 {
		t.Fatalf("受理机不得拉起，A Launch=%d", got)
	}
	specs := p.b.runner.launchSpecs()
	if len(specs) != 1 || specs[0].Workdir != "/repo/t448-linux" || specs[0].HomeDir != "/home/t448" {
		t.Fatalf("执行机应恰一次拉起且 workdir 在执行机解析: %+v", specs)
	}
	if midSquad != 1 || midCarrier != 1 {
		t.Fatalf("回合中计数应恰为执行机那一次准入（1/1），得 squad=%d carrier=%d", midSquad, midCarrier)
	}
	c, err := p.a.ledger.GetCard(card)
	if err != nil {
		t.Fatal(err)
	}
	if c.DriverSession != b448SeatIdentity(t, "ses_t448-linux_1") ||
		c.DriverSource != string(proto.SeatSourceCoordinate) || c.DriverMode != string(proto.SeatModePush) {
		t.Fatalf("席位应为 coordinate/push 本回合会话: %+v", c)
	}
	bearing, ok, err := p.a.ledger.SeatBearingOf(card)
	if err != nil || !ok || bearing.Machine != b448MachineB || bearing.Carrier != b448Carrier ||
		bearing.Workdir != "/repo/t448-linux" {
		t.Fatalf("承载应记执行机: %+v ok=%v err=%v", bearing, ok, err)
	}
	p.assertZeroCounts(t)
}

// V4a + Q1 甲（F3/F6/F7/F12/F13/F14）：截获 A 发出的原始转交请求——带防环头、squad/carrier 键恒在、
// accepted_by 为受理机名；首轮不带 expect；换绑带 expect=旧席位身份且执行机以 RebindSeatMode 落座。
func TestB448TransferWireShapeAndRebind(t *testing.T) {
	p := newB448Pair(t, b448PairOpts{})
	proxy := newB448Proxy(t, p.b)
	p.a.setTargets(t, map[string]string{b448MachineB: proxy.addr()})
	card := b448Card(t, p)

	if code, body := ledgerPost(t, p.a.testAgentdEnv, "/api/cards/"+card+"/coordinator/launch", `{}`); code != http.StatusOK {
		t.Fatalf("首轮应 200，得 %d %s", code, body)
	}
	if code, body := ledgerPost(t, p.a.testAgentdEnv, "/api/cards/"+card+"/coordinator/rebind", `{"mode":"launch"}`); code != http.StatusOK {
		t.Fatalf("换绑应 200，得 %d %s", code, body)
	}
	proxy.mu.Lock()
	paths, bodies, fwd := proxy.paths, proxy.bodies, proxy.forwards
	proxy.mu.Unlock()
	if len(paths) != 2 || paths[0] != "/api/cards/"+card+"/coordinator/first-round" || paths[1] != paths[0] {
		t.Fatalf("应恰两次卡首轮转交（每次拉起只发一次，F36）: %v", paths)
	}
	for i, keys := range bodies {
		if fwd[i] != "1" {
			t.Fatalf("第 %d 次转交缺防环头（F3）", i)
		}
		if string(keys["squad"]) != `"`+b448Squad+`"` || string(keys["carrier"]) != `"`+b448Carrier+`"` {
			t.Fatalf("第 %d 次转交 squad/carrier 不符: %v", i, keys)
		}
		if string(keys["accepted_by"]) != `"`+b448MachineA+`"` {
			t.Fatalf("第 %d 次转交 accepted_by 应为受理机名: %v", i, keys)
		}
	}
	if _, ok := bodies[0]["expect"]; ok {
		t.Fatalf("首轮转交不得带 expect（F13）: %v", bodies[0])
	}
	if string(bodies[1]["expect"]) != `"`+b448SeatIdentity(t, "ses_t448-linux_1")+`"` {
		t.Fatalf("换绑转交 expect 应为旧席位身份: %v", bodies[1])
	}
	c, err := p.a.ledger.GetCard(card)
	if err != nil {
		t.Fatal(err)
	}
	if c.DriverSession != b448SeatIdentity(t, "ses_t448-linux_2") || c.DriverMode != string(proto.SeatModePush) {
		t.Fatalf("换绑后席位应为第二回合会话: %+v", c)
	}
	if len(p.b.runner.launchSpecs()) != 2 || len(p.a.runner.launchSpecs()) != 0 {
		t.Fatalf("两回合都应在执行机拉起")
	}
	p.assertZeroCounts(t)
}

// V1 反例（B446 兜底在执行机生效）：B 的首轮里协调者自坐 bind/pull → 收尾改写为 coordinate/push，A 得 200。
func TestB448CardSelfSeatAdoptedOnCarrierMachine(t *testing.T) {
	p := newB448Pair(t, b448PairOpts{})
	card := b448Card(t, p)
	p.b.runner.onLaunch = func(keysclient.SessionSpec) {
		if err := p.b.ledger.BindSeatMode(card, b448SeatIdentity(t, "ses_t448-linux_1"), proto.SeatSourceBind,
			proto.SeatModePull, ledger.SeatBearing{}); err != nil {
			t.Errorf("首轮自坐: %v", err)
		}
	}
	code, body := ledgerPost(t, p.a.testAgentdEnv, "/api/cards/"+card+"/coordinator/launch", `{}`)
	if code != http.StatusOK {
		t.Fatalf("执行机自坐兜底后应 200，得 %d %s", code, body)
	}
	c, _ := p.a.ledger.GetCard(card)
	if c.DriverSource != string(proto.SeatSourceCoordinate) || c.DriverMode != string(proto.SeatModePush) {
		t.Fatalf("自坐席位应改写为 coordinate/push: %+v", c)
	}
	if n := b448NeedsHumanCount(t, p.a.ledger, card); n != 0 {
		t.Fatalf("自坐兜底不应标 needs_human，得 %d 条", n)
	}
	p.assertZeroCounts(t)
}

// V1 反例（F25）：B 的回合里席位被他人抢占 → A 入口 409 带 session_id；needs_human 恰一条（B 写、A 不追加）。
func TestB448CardSeatStolenOnCarrierMachine(t *testing.T) {
	p := newB448Pair(t, b448PairOpts{})
	card := b448Card(t, p)
	p.b.runner.onLaunch = func(keysclient.SessionSpec) {
		if err := p.b.ledger.BindSeat(card, "cli:opencode#sess-intruder", proto.SeatSourceCoordinate,
			ledger.SeatBearing{Carrier: "x", Machine: "local"}); err != nil {
			t.Errorf("抢座: %v", err)
		}
	}
	code, body := ledgerPost(t, p.a.testAgentdEnv, "/api/cards/"+card+"/coordinator/launch", `{}`)
	if code != http.StatusConflict {
		t.Fatalf("席位被抢应 409，得 %d %s", code, body)
	}
	var resp struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil || resp.SessionID != "ses_t448-linux_1" {
		t.Fatalf("409 应带执行机回合的 session_id: %s", body)
	}
	if n := b448NeedsHumanCount(t, p.a.ledger, card); n != 1 {
		t.Fatalf("needs_human 应恰一条，得 %d", n)
	}
	p.assertZeroCounts(t)
}

// V3（S6/F22）：转交途中执行机名额被占满 → A 入口 409「协调者并发已满」（与本机同文案）；launch_queue 出队
// 同条件下得到 ErrNoSlot 并回填队列。
func TestB448CardNoSlotOnCarrierMachine(t *testing.T) {
	p := newB448Pair(t, b448PairOpts{})
	proxy := newB448Proxy(t, p.b)
	p.a.setTargets(t, map[string]string{b448MachineB: proxy.addr()})
	var held []scheduling.Binding
	proxy.before = func(string) {
		// A 已归还自己的名额后、B 准入前，别处占满了该载体（跨机竞态）。
		b, err := p.b.sched.AdmitSeatCarrier(b448Squad, b448Carrier)
		if err != nil {
			t.Errorf("预占名额: %v", err)
			return
		}
		held = append(held, b)
	}
	card := b448Card(t, p)
	code, body := ledgerPost(t, p.a.testAgentdEnv, "/api/cards/"+card+"/coordinator/launch", `{}`)
	if code != http.StatusConflict || !strings.Contains(body, "协调者并发已满（小队 "+b448Squad+"）") {
		t.Fatalf("执行机名额满应 409 同文案，得 %d %s", code, body)
	}
	b448AssertEmptySeat(t, p, card)
	if len(p.b.runner.launchSpecs()) != 0 {
		t.Fatal("名额满不得拉起")
	}
	for _, b := range held {
		_ = p.b.sched.Release(b.Squad, b.Carrier)
	}
	held = nil
	p.assertZeroCounts(t)

	if _, err := p.a.sched.Enqueue(scheduling.IgnitionRequest{Card: card, Squad: b448Squad, Actor: "test", Ready: true},
		scheduling.KindLaunchQueue); err != nil {
		t.Fatalf("入队: %v", err)
	}
	processed, err := p.a.srv.drainQueuesOnce(context.Background())
	if err != nil || processed != 1 {
		t.Fatalf("清队应处理 1 条且不报错，得 processed=%d err=%v", processed, err)
	}
	rows, err := p.a.sched.QueueSnapshot()
	if err != nil || len(rows) != 1 || rows[0].Kind != scheduling.KindLaunchQueue || rows[0].Req.Card != card {
		t.Fatalf("执行机无位应回填 launch_queue: rows=%+v err=%v", rows, err)
	}
	for _, b := range held {
		_ = p.b.sched.Release(b.Squad, b.Carrier)
	}
	p.assertZeroCounts(t)

	// F22 直证：同一入口函数的错误满足 ErrNoSlot 与 *coordinatorAdmissionError。
	_, launchErr := p.a.srv.launchCoordinatorRound(context.Background(), card, "coordinate")
	var admission *coordinatorAdmissionError
	if !errors.Is(launchErr, scheduling.ErrNoSlot) || !errors.As(launchErr, &admission) {
		t.Fatalf("转交 no_slot 应还原为 ErrNoSlot + *coordinatorAdmissionError，得 %v", launchErr)
	}
	for _, b := range held {
		_ = p.b.sched.Release(b.Squad, b.Carrier)
	}
}

// V5（S5/F27/F37）：B 判定载体不在本机（设备表不一致）→ 拒绝且不发第二跳；A 入口 502 防环文案；两机零计数。
func TestB448CardNotLocalRejectsWithoutSecondHop(t *testing.T) {
	p := newB448Pair(t, b448PairOpts{})
	var hops atomic.Int32
	counter := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	if err := p.b.srv.swapConf(func(c *config.Config) error {
		c.Name = "t448-other"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	p.b.setTargets(t, map[string]string{b448MachineB: strings.TrimPrefix(counter.URL, "http://")})
	card := b448Card(t, p)
	code, body := ledgerPost(t, p.a.testAgentdEnv, "/api/cards/"+card+"/coordinator/launch", `{}`)
	if code != http.StatusBadGateway ||
		!strings.Contains(body, "转交到本机的协调者首轮，但载体 "+b448Carrier+" 不在本机（设备表不一致？）") {
		t.Fatalf("防环应 502 带防环文案，得 %d %s", code, body)
	}
	if n := hops.Load(); n != 0 {
		t.Fatalf("执行机不得发第二跳，计数 handler 收到 %d 次", n)
	}
	if len(p.b.runner.launchSpecs())+len(p.a.runner.launchSpecs()) != 0 {
		t.Fatal("防环拒绝不得拉起")
	}
	b448AssertEmptySeat(t, p, card)
	p.assertZeroCounts(t)
}

// V6（F21）：对端不认新路径（404）→ A 入口 502「<机器> 的 agentd 版本过旧，不支持首轮转交，请升级」，零席位零计数。
func TestB448CardOldPeerReportsUpgrade(t *testing.T) {
	p := newB448Pair(t, b448PairOpts{})
	old := testhttp.NewServer(t, http.NotFoundHandler())
	p.a.setTargets(t, map[string]string{b448MachineB: strings.TrimPrefix(old.URL, "http://")})
	card := b448Card(t, p)
	code, body := ledgerPost(t, p.a.testAgentdEnv, "/api/cards/"+card+"/coordinator/launch", `{}`)
	if code != http.StatusBadGateway || !strings.Contains(body, b448MachineB+" 的 agentd 版本过旧，不支持首轮转交，请升级") {
		t.Fatalf("老对端应 502 版本过旧文案，得 %d %s", code, body)
	}
	b448AssertEmptySeat(t, p, card)
	p.assertZeroCounts(t)
}

// V4b/c + F4/F8–F11/F20：直接向 B 发原始请求——缺头、缺键、空串一律 400 kind=invalid，「缺失」与「为空」
// 文案不同；卡不存在 409 not_found（不回 404）；全部零写入、零计数、零回合。两个端点同口径。
func TestB448FirstRoundWireValidation(t *testing.T) {
	p := newB448Pair(t, b448PairOpts{})
	card := b448Card(t, p)
	code, raw := ledgerPost(t, p.a.testAgentdEnv, "/api/sessions", `{"title":"空驻场会话"}`)
	if code != http.StatusOK {
		t.Fatalf("建空会话: %d %s", code, raw)
	}
	var sess proto.Session
	_ = json.Unmarshal([]byte(raw), &sess)
	for _, path := range []string{
		"/api/cards/" + card + "/coordinator/first-round",
		"/api/sessions/" + sess.ID + "/resident/first-round",
	} {
		for _, tc := range []struct {
			name, body string
			forwarded  bool
			want       string
		}{
			{"缺转交头", `{"squad":"t448-coord","carrier":"t448-oc"}`, false, "转交"},
			{"缺 squad", `{"carrier":"t448-oc"}`, true, "squad 缺失"},
			{"空 squad", `{"squad":"","carrier":"t448-oc"}`, true, "squad 为空"},
			{"缺 carrier", `{"squad":"t448-coord"}`, true, "carrier 缺失"},
			{"空 carrier", `{"squad":"t448-coord","carrier":""}`, true, "carrier 为空"},
			{"非 JSON", `{`, true, "解析"},
		} {
			code, body := b448Raw(t, p.b, path, tc.body, tc.forwarded)
			var e proto.FirstRoundTransferError
			_ = json.Unmarshal([]byte(body), &e)
			if code != http.StatusBadRequest || e.Kind != proto.FirstRoundKindInvalid || !strings.Contains(e.Error, tc.want) {
				t.Fatalf("%s %s：应 400 invalid 含 %q，得 %d %s", path, tc.name, tc.want, code, body)
			}
		}
	}
	code, body := b448Raw(t, p.b, "/api/cards/B99999/coordinator/first-round",
		`{"squad":"t448-coord","carrier":"t448-oc"}`, true)
	var e proto.FirstRoundTransferError
	_ = json.Unmarshal([]byte(body), &e)
	if code != http.StatusConflict || e.Kind != proto.FirstRoundKindNotFound {
		t.Fatalf("卡不存在应 409 not_found（F20 不回 404），得 %d %s", code, body)
	}
	// F20 修订 r1：会话不存在 → 409 no_room（即 F26），受理端经 client 还原为 collab.ErrNoRoom（入口 404）。
	code, body = b448Raw(t, p.b, "/api/sessions/S99999/resident/first-round",
		`{"squad":"t448-coord","carrier":"t448-oc"}`, true)
	e = proto.FirstRoundTransferError{}
	_ = json.Unmarshal([]byte(body), &e)
	if code != http.StatusConflict || e.Kind != proto.FirstRoundKindNoRoom {
		t.Fatalf("会话不存在应 409 no_room（F20 r1 不回 404），得 %d %s", code, body)
	}
	cl, err := p.a.srv.clientForTarget(b448MachineB)
	if err != nil {
		t.Fatal(err)
	}
	_, wireErr := cl.MarkForwarded().ResidentFirstRound(context.Background(), "S99999",
		proto.FirstRoundTransferReq{Squad: b448Squad, Carrier: b448Carrier, AcceptedBy: b448MachineA})
	if restored := firstRoundFailure(b448Squad, b448Carrier, b448MachineB, wireErr); !errors.Is(restored, collab.ErrNoRoom) {
		t.Fatalf("会话不存在经 client 还原应满足 errors.Is(collab.ErrNoRoom)，得 wire=%v restored=%v", wireErr, restored)
	}
	b448AssertEmptySeat(t, p, card)
	if _, ok, err := p.a.ledger.SessionResident(sess.ID); err != nil || ok {
		t.Fatalf("不成形请求不得落驻场行: ok=%v err=%v", ok, err)
	}
	if len(p.b.runner.launchSpecs()) != 0 {
		t.Fatal("不成形请求不得拉起")
	}
	p.assertZeroCounts(t)
}
