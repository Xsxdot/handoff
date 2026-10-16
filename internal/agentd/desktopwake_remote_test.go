// desktopwake_remote_test.go —— B434 G2：桌面端席位跨机转交（契约 §3.7、拍板 D3/D5；
// 条 95–105、165–171、93(b)，r10 第 10 条「异机分支不阻断清队」）。
//
// 职责：两台 agentd 共享同一账本（发起端 env.srv + 被转交端 desktopPeer），或发起端 + 假对端
// （desktopRequestRecorder），从缝 5 消费循环 / 缝 6 handleCoordWake 进，锁「本机零写信、对端
// 写信开 opener / 一跳封顶 / 转交失败不标 seen 且一段一条 needs_human / letter_unopened 终局」。
// 边界：本机失败路径在 desktopwake_fail_test；夹具来自 desktopwake_slice/_letter/_mailbox/_fail_test。
package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/testhttp"
)

// desktopPeer 是共享账本的第二台 agentd（被转交端），自认 self 为本机目标名。
type desktopPeer struct {
	srv  *Server
	url  string
	spy  *desktopOpenerSpy
	home string
}

// newDesktopPeer 起被转交端：独立 store/配置、共享 st 账本、自动化与假 keystone 就位、opener spy。
func newDesktopPeer(t *testing.T, st *ledger.Store, self string) *desktopPeer {
	t.Helper()
	backend, err := store.Open(t.TempDir() + "/handoff.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	cfg := &config.Config{Token: testToken, DataDir: t.TempDir(), Targets: map[string]config.Target{}}
	srv := NewServer(cfg, backend, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.SetLedger(st)
	newManagerForServer(t, srv, nil)
	srv.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	ts := testhttp.NewServer(t, srv.Handler())
	cfg.Listen = strings.TrimPrefix(ts.URL, "http://")
	cfg.Targets[self] = config.Target{Addr: ts.URL, Token: testToken}
	SetupAutomationForTest(t, srv, st)
	srv.SetKeystone(keystone.New(&queueTraceRunner{}, &fakeCoordNarrator{}, srv.autoLedger, attachLocator{}))
	p := &desktopPeer{srv: srv, url: ts.URL, spy: &desktopOpenerSpy{}, home: t.TempDir()}
	srv.desktopOpen, srv.desktopHome = p.spy.open, p.home
	return p
}

// desktopRequestRecorder 是假对端 agentd：记录每个请求路径，按 status 回包；delay>0 时
// 挂住直到请求被取消或 delay 到期（超时出口用）。
type desktopRequestRecorder struct {
	mu     sync.Mutex
	paths  []string
	status int
	body   string
	delay  time.Duration
}

func (r *desktopRequestRecorder) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.ReadAll(req.Body)
		r.mu.Lock()
		r.paths = append(r.paths, req.URL.Path)
		status, body, delay := r.status, r.body, r.delay
		r.mu.Unlock()
		if delay > 0 {
			select {
			case <-req.Context().Done():
				return
			case <-time.After(delay):
			}
		}
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

func (r *desktopRequestRecorder) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.paths...)
}

// routeDesktopMachine 在 srv 的配置里把 machine 登记（或改指）到 addr。
func routeDesktopMachine(t *testing.T, srv *Server, machine, addr string) {
	t.Helper()
	allowCarrierMachines(t, srv)
	if err := srv.swapConf(func(c *config.Config) error {
		if c.Targets == nil {
			c.Targets = map[string]config.Target{}
		}
		c.Targets[machine] = config.Target{Addr: addr, Token: testToken}
		return nil
	}); err != nil {
		t.Fatalf("登记目标 %s: %v", machine, err)
	}
}

// bindDesktopSeatAt 在卡上 bind 席位并写桌面端记录，Machine 取给定机器名。
func bindDesktopSeatAt(t *testing.T, env *ledgerEnv, cardID, cliID, desktopSession, machine string) string {
	t.Helper()
	identity, err := proto.EncodeSeatIdentity("claude", cliID)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.ledger.BindSeat(cardID, identity, proto.SeatSourceBind, ledger.SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	if err := env.ledger.SetSeatDesktop(cardID, identity, ledger.SeatDesktop{
		Session: desktopSession, Machine: machine, Via: proto.SeatDesktopViaPage}); err != nil {
		t.Fatal(err)
	}
	return identity
}

// postForwardedWake 以转交身份（X-Handoff-Forwarded: 1）直打 baseURL 的 /coordinator/wake。
func postForwardedWake(t *testing.T, baseURL, cardID string, req proto.CoordinatorWakeReq) (int, string) {
	t.Helper()
	body, _ := json.Marshal(req)
	hr, err := http.NewRequest(http.MethodPost, baseURL+"/api/cards/"+cardID+"/coordinator/wake", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	hr.Header.Set("Authorization", "Bearer "+testToken)
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("X-Handoff-Forwarded", "1")
	resp, err := http.DefaultClient.Do(hr)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

// wakeRoundFailRows 数卡上 wake_round 的 fail 行。
func wakeRoundFailRows(t *testing.T, env *ledgerEnv, cardID string) int {
	t.Helper()
	n := 0
	for _, row := range readWakeRoundCommentRows(t, env, cardID) {
		if strings.Contains(row.Key, ":fail") || row.Round.Phase == "fail" {
			n++
		}
	}
	return n
}

// TestB434DesktopRemoteTransferDelivers 锁条 95–97：席位机器为对端 → 发起端零写信零 opener、
// 恰一次转交；被转交端在自己的信箱写信并发 continue；该批标 seen、零 needs_human。
func TestB434DesktopRemoteTransferDelivers(t *testing.T) {
	env, _, spy, home := newDesktopSliceEnv(t)
	peer := newDesktopPeer(t, env.ledger, "peer-mac")
	routeDesktopMachine(t, env.srv, "peer-mac", peer.url)
	cardID := createCoordCard(t, env)
	bindDesktopSeatAt(t, env, cardID, "remote-ok", "local_remote-ok", "peer-mac")
	drainAutomation(t, env)
	seq := openDecisionSeq(t, env, cardID, "远端裁决")
	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
		t.Fatalf("转交应成功，实得 %v", err)
	}
	if files := mdFilesUnder(t, home); len(files) != 0 || len(spy.calls()) != 0 {
		t.Fatalf("条 95：发起端零写信零 opener：files=%v opener=%v", files, spy.calls())
	}
	letters := desktopLettersIn(t, peer.home)
	if len(letters) != 1 || !strings.Contains(letters[0], fmt.Sprintf(" seq=%d kind=", seq)) {
		t.Fatalf("条 97：被转交端应写恰一封含 seq %d 的信：%q", seq, letters)
	}
	if got := peer.spy.calls(); len(got) != 1 || got[0] != "claude://code/continue?session=local_remote-ok" {
		t.Fatalf("条 97：被转交端应发一次 continue：%v", got)
	}
	if !automationSeenHas(env, seq) {
		t.Fatal("转交成功该批应标 seen")
	}
	if marks := desktopNeedsHumanOn(t, env, cardID); len(marks) != 0 {
		t.Fatalf("成功转交零 needs_human：%v", marks)
	}
}

// TestB434DesktopForwardedOneHopCap 锁条 96/98/99/171：被转交端收到的唤醒对其也非本机的
// 席位 → 零出站（第三台假对端零请求）、回 502 且 error 含「一跳封顶」、不落 needs_human。
func TestB434DesktopForwardedOneHopCap(t *testing.T) {
	env, _, _, _ := newDesktopSliceEnv(t)
	peer := newDesktopPeer(t, env.ledger, "peer-mac")
	third := &desktopRequestRecorder{}
	thirdTS := testhttp.NewServer(t, third.handler())
	routeDesktopMachine(t, peer.srv, "third-mac", thirdTS.URL)
	cardID := createCoordCard(t, env)
	seat := bindDesktopSeatAt(t, env, cardID, "hop-cap", "local_hop-cap", "third-mac")
	seq := openDecisionSeq(t, env, cardID, "转两跳")
	code, body := postForwardedWake(t, peer.url, cardID, proto.CoordinatorWakeReq{
		Seat: seat, Events: []proto.LedgerEvent{rawEventAt(t, env, seq)}, Holder: "x#1"})
	if code != http.StatusBadGateway || !strings.Contains(body, "一跳封顶") {
		t.Fatalf("条 99：一跳封顶应回 502 且含原因，实得 %d %s", code, body)
	}
	if got := third.calls(); len(got) != 0 {
		t.Fatalf("条 98：被转交端不得二次转交：%v", got)
	}
	if marks := desktopNeedsHumanOn(t, env, cardID); len(marks) != 0 {
		t.Fatalf("条 99/171：被转交端不落 needs_human：%v", marks)
	}
}

// TestB434DesktopRemoteFailureKinds 锁条 100–102：四种转交失败（非 2xx、不可达、无目标、超时）
// 各自 → 未投递、不标 seen、游标不越过该批、恰一条 needs_human（前缀同本机）。
func TestB434DesktopRemoteFailureKinds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		route func(t *testing.T, env *ledgerEnv) // 把 peer-mac 指向失败形态
	}{
		{"非2xx", func(t *testing.T, env *ledgerEnv) {
			rec := &desktopRequestRecorder{status: http.StatusBadGateway, body: `{"error":"注入"}`}
			routeDesktopMachine(t, env.srv, "peer-mac", testhttp.NewServer(t, rec.handler()).URL)
		}},
		{"不可达", func(t *testing.T, env *ledgerEnv) {
			routeDesktopMachine(t, env.srv, "peer-mac", "http://127.0.0.1:1")
		}},
		{"无目标", func(t *testing.T, env *ledgerEnv) { allowCarrierMachines(t, env.srv) }},
		{"超时", func(t *testing.T, env *ledgerEnv) {
			old := desktopTransferTimeout
			desktopTransferTimeout = 100 * time.Millisecond
			t.Cleanup(func() { desktopTransferTimeout = old })
			rec := &desktopRequestRecorder{delay: 5 * time.Second}
			routeDesktopMachine(t, env.srv, "peer-mac", testhttp.NewServer(t, rec.handler()).URL)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, _, spy, home := newDesktopSliceEnv(t)
			tc.route(t, env)
			cardID := createCoordCard(t, env)
			bindDesktopSeatAt(t, env, cardID, "remote-fail", "local_remote-fail", "peer-mac")
			drainAutomation(t, env)
			seq := openDecisionSeq(t, env, cardID, "对端坏了")
			start := time.Now()
			_, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
			if !desktopUndelivered(err) {
				t.Fatalf("条 100：转交失败应报未投递，实得 %v", err)
			}
			if elapsed := time.Since(start); elapsed > 3*time.Second {
				t.Fatalf("转交失败不得长时间挡消费循环：%v", elapsed)
			}
			if automationSeenHas(env, seq) {
				t.Fatal("条 101：转交失败该批不得标 seen")
			}
			if automationCursorOf(env) >= seq {
				t.Fatalf("条 102：游标不得越过 seq %d（cursor=%d）", seq, automationCursorOf(env))
			}
			assertOneDesktopFailMark(t, env, cardID, 1)
			if len(mdFilesUnder(t, home)) != 0 || len(spy.calls()) != 0 {
				t.Fatal("转交失败发起端不得自己写信")
			}
		})
	}
}

// TestB434DesktopRemoteRecovers 锁条 103/105 与 plan §11.3「一段一轮」：对端 502 → 退避窗内
// 不重发；窗后重发仍失败不再落标、wake_round fail 行仍一行；对端恢复后保住的 seq 进对端信、
// 标 seen、清段。
func TestB434DesktopRemoteRecovers(t *testing.T) {
	env, _, _, _ := newDesktopSliceEnv(t)
	clock := fixDesktopClock(env)
	peer := newDesktopPeer(t, env.ledger, "peer-mac")
	broken := &desktopRequestRecorder{status: http.StatusBadGateway, body: `{"error":"注入"}`}
	routeDesktopMachine(t, env.srv, "peer-mac", testhttp.NewServer(t, broken.handler()).URL)
	cardID := createCoordCard(t, env)
	bindDesktopSeatAt(t, env, cardID, "remote-heal", "local_remote-heal", "peer-mac")
	drainAutomation(t, env)
	seq := openDecisionSeq(t, env, cardID, "等对端恢复")
	consume := func() error {
		_, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
		return err
	}
	if err := consume(); !desktopUndelivered(err) {
		t.Fatalf("首轮应未投递：%v", err)
	}
	if err := consume(); err != nil || len(broken.calls()) != 1 {
		t.Fatalf("退避窗内不得重发：err=%v calls=%v", err, broken.calls())
	}
	clock.Advance(desktopWakeRetryMax)
	if err := consume(); !desktopUndelivered(err) || len(broken.calls()) != 2 {
		t.Fatalf("窗后应重发一次仍失败：err=%v calls=%v", err, broken.calls())
	}
	assertOneDesktopFailMark(t, env, cardID, 1) // 条 103
	if n := wakeRoundFailRows(t, env, cardID); n != 1 {
		t.Fatalf("同一失败段 wake_round fail 行应一行（段内复用轮次），实得 %d", n)
	}

	routeDesktopMachine(t, env.srv, "peer-mac", peer.url)
	clock.Advance(desktopWakeRetryMax)
	if err := consume(); err != nil {
		t.Fatalf("对端恢复后应转交成功：%v", err)
	}
	letters := desktopLettersIn(t, peer.home)
	if len(letters) != 1 || !strings.Contains(letters[0], fmt.Sprintf(" seq=%d kind=", seq)) || !automationSeenHas(env, seq) {
		t.Fatalf("条 105：保住的 seq %d 应进对端信并标 seen：%q", seq, letters)
	}
	env.srv.automationMu.Lock()
	_, inSegment := env.srv.desktopWakeStall[cardID]
	env.srv.automationMu.Unlock()
	if inSegment {
		t.Fatal("条 104：转交成功后失败段应清零")
	}
}

// TestB434DesktopRemoteLetterUnopened 锁条 165–171：被转交端信已落盘、opener 失败 → 直打回
// 200 + letter_unopened=true + woke=false；经发起端消费 → 标 seen、complete、不产生第二封、
// 恰一条 needs_human（由发起端落，actor agentd）。
func TestB434DesktopRemoteLetterUnopened(t *testing.T) {
	env, _, _, _ := newDesktopSliceEnv(t)
	peer := newDesktopPeer(t, env.ledger, "peer-mac")
	peer.spy.err = errors.New("注入：对端 open 失败")
	routeDesktopMachine(t, env.srv, "peer-mac", peer.url)
	probeCard := createCoordCard(t, env) // 直打用卡，与消费循环用卡分开，免得排水时再转交一次
	seat := bindDesktopSeatAt(t, env, probeCard, "remote-unopened", "local_remote-unopened", "peer-mac")
	probeSeq := openDecisionSeq(t, env, probeCard, "直打")
	code, body := postForwardedWake(t, peer.url, probeCard, proto.CoordinatorWakeReq{
		Seat: seat, Events: []proto.LedgerEvent{rawEventAt(t, env, probeSeq)}, Holder: "x#1"})
	var resp proto.CoordinatorWakeResp
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("回包不是 CoordinatorWakeResp：%v %s", err, body)
	}
	if code != http.StatusOK || !resp.LetterUnopened || resp.Woke {
		t.Fatalf("条 165–167：应 200 + letter_unopened=true + woke=false，实得 %d %+v", code, resp)
	}
	if n := len(desktopLettersIn(t, peer.home)); n != 1 {
		t.Fatalf("条 165：被转交端信应已落盘，实得 %d 封", n)
	}
	if marks := desktopNeedsHumanOn(t, env, probeCard); len(marks) != 0 {
		t.Fatalf("条 168/171：被转交端不落 needs_human：%v", marks)
	}
	peer.spy.err = nil
	drainAutomation(t, env) // 直打卡的 seq 由消费循环正常转交一次（opener 已恢复），水位归零
	peer.spy.err = errors.New("注入：对端 open 又失败")

	cardID := createCoordCard(t, env)
	bindDesktopSeatAt(t, env, cardID, "remote-unopened-2", "local_remote-unopened-2", "peer-mac")
	drainAutomation(t, env)
	peerLettersBefore := len(desktopLettersIn(t, peer.home))
	seq := openDecisionSeq(t, env, cardID, "经消费循环")
	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); !desktopUnopened(err) {
		t.Fatalf("发起端应报未打开，实得 %v", err)
	}
	if !automationSeenHas(env, seq) || automationCursorOf(env) < seq {
		t.Fatalf("条 169：该批应标 seen 且游标越过 seq %d", seq)
	}
	assertOneDesktopFailMark(t, env, cardID, 1) // 条 170
	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
		t.Fatalf("回声轮: %v", err)
	}
	if n := len(desktopLettersIn(t, peer.home)) - peerLettersBefore; n != 1 {
		t.Fatalf("条 169：不重试、不产生第二封同批信，实得新增 %d 封", n)
	}
}

// TestB434DesktopRemoteQueueReleaseDoesNotBlockDrain 锁 r10 第 10 条：异机桌面端席位的队列出队
// 唤醒不转交、不写信，清队照常放行（基线即绿，变异 M7 见台账 L6）。
func TestB434DesktopRemoteQueueReleaseDoesNotBlockDrain(t *testing.T) {
	env, _, spy, home := newDesktopSliceEnv(t)
	peer := &desktopRequestRecorder{}
	routeDesktopMachine(t, env.srv, "peer-mac", testhttp.NewServer(t, peer.handler()).URL)
	cardID := seedCard(t, env, "异机桌面端 bind 卡队列出队").ID
	bindDesktopSeatAt(t, env, cardID, "queue-remote", "local_queue-remote", "peer-mac")
	done := make(chan struct{}, 4)
	env.srv.runStepFn = func(_ context.Context, _ *ledgerstep.StepRunner, _, _ string) { done <- struct{}{} }
	req := scheduling.IgnitionRequest{Card: cardID, Node: ledger.StatusDoing, Actor: "test", Ready: true}
	if err := env.srv.drainIgnitionRequest(context.Background(), req); err != nil {
		t.Fatalf("异机桌面端席位队列出队应放行，实得 %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runStepFn 2s 内未被调用")
	}
	waitFor(t, func() bool { return !env.srv.cardStepInFlight(cardID) })
	if got := peer.calls(); len(got) != 0 {
		t.Fatalf("队列出队不得转交：%v", got)
	}
	if len(mdFilesUnder(t, home)) != 0 || len(spy.calls()) != 0 {
		t.Fatal("队列出队不得写信/开 opener")
	}
}

// TestB434BindWithoutDesktopInfo 锁条 93(b)：无桌面端记录的 bind 席位，转交收件与队列出队
// 两个入口都留 Info「bind 席位跳过协调者唤醒」、零信零 opener（基线即绿，变异 M4 见台账 L6）。
func TestB434BindWithoutDesktopInfo(t *testing.T) {
	env, _, spy, home := newDesktopSliceEnv(t)
	var logs bytes.Buffer
	env.srv.log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cardID := createCoordCard(t, env)
	seat := bindDesktopSeat(t, env, cardID, "plain-recv", "")
	seq := openDecisionSeq(t, env, cardID, "转交来的裁决")
	body, _ := json.Marshal(proto.CoordinatorWakeReq{Seat: seat, Events: []proto.LedgerEvent{rawEventAt(t, env, seq)}, Holder: "peer#1"})
	code, resp := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/coordinator/wake", string(body))
	if code != http.StatusOK {
		t.Fatalf("转交收件对无记录 bind 应 200，实得 %d %s", code, resp)
	}
	var out proto.CoordinatorWakeResp
	_ = json.Unmarshal([]byte(resp), &out)
	if out.Woke || out.LetterUnopened {
		t.Fatalf("无记录 bind 不应 woke/letter_unopened：%+v", out)
	}
	const want = `level=INFO msg="bind 席位跳过协调者唤醒"`
	if !strings.Contains(logs.String(), want) {
		t.Fatalf("条 93(b)：转交收件应留 Info，实得：\n%s", logs.String())
	}
	if files := mdFilesUnder(t, home); len(files) != 0 || len(spy.calls()) != 0 {
		t.Fatalf("零信零 opener：files=%v opener=%v", files, spy.calls())
	}
	logs.Reset()
	if _, err := env.srv.wakeCoordinatorRoundID(context.Background(), cardID,
		[]keystone.WakeEvent{{Kind: keystone.WakeQueueRelease, Card: cardID}}, nil, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), want) {
		t.Fatalf("条 93(b)：队列出队应留 Info，实得：\n%s", logs.String())
	}
}

// TestB434DesktopMailboxHomeRule 锁契约 r12 第 10 条「信箱 HOME 规则唯一定义」：
// desktopMailboxHome 在 desktopHome 非空时原样返回，为空时取 os.UserHomeDir()；
// wakeDesktopSeat 与 G3 收件机都只经它取信箱根。
func TestB434DesktopMailboxHomeRule(t *testing.T) {
	env, _, _, home := newDesktopSliceEnv(t)
	if got, err := env.srv.desktopMailboxHome(); err != nil || got != home {
		t.Fatalf("desktopHome=%q 时应原样返回，实得 %q err=%v", home, got, err)
	}
	env.srv.desktopHome = ""
	want, wantErr := os.UserHomeDir()
	if got, err := env.srv.desktopMailboxHome(); got != want || (err == nil) != (wantErr == nil) {
		t.Fatalf("desktopHome 为空时应取 os.UserHomeDir()=%q（err=%v），实得 %q err=%v", want, wantErr, got, err)
	}
}
