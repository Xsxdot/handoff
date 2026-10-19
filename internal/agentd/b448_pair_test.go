package agentd

// b448_pair_test.go —— B448 双 agentd 测试夹具（plan §4 第 1 步承重假设）。
//
// 职责：在一个测试进程里起两台 agentd（受理机 A = t448-mac、执行机 B = t448-linux），共用同一个
// *ledger.Store（共享账本），各自独立的 store / DataDir / HTTP 监听 / 日志；A 的 targets 把 t448-linux
// 指向 B 的真实监听地址，转交经真实 HTTP 编解码往返。两台各装一个 b448Runner（keystone 出站替身），
// 用来断言「只有执行机拉起」。
// 边界：只供 B448 测试；不 mock 对端，不改生产装配。

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/discipline"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/testhttp"
)

const (
	b448MachineA = "t448-mac"
	b448MachineB = "t448-linux"
	b448Squad    = "t448-coord"
	b448Carrier  = "t448-oc"
)

// b448Runner 是 keystone 出站替身：记录 Launch 次数与 spec；onLaunch 在回合「正在跑」期间调用（锁外），
// 用来在回合中读计数或模拟载体自坐/抢座；launchErr 非 nil 时 Launch 失败；delay 让回合变慢（V7）。
type b448Runner struct {
	mu        sync.Mutex
	name      string
	launches  []keysclient.SessionSpec
	launchErr error
	delay     time.Duration
	onLaunch  func(spec keysclient.SessionSpec)
	nextID    int
}

func (r *b448Runner) Launch(spec keysclient.SessionSpec, _ string) (keysclient.TurnResult, error) {
	r.mu.Lock()
	r.launches = append(r.launches, spec)
	hook, delay := r.onLaunch, r.delay
	r.mu.Unlock()
	if hook != nil {
		hook(spec)
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.launchErr != nil {
		return keysclient.TurnResult{}, r.launchErr
	}
	r.nextID++
	return keysclient.TurnResult{SessionID: fmt.Sprintf("ses_%s_%d", r.name, r.nextID), Output: "ok"}, nil
}

func (r *b448Runner) Resume(ref keysclient.SessionRef, _ string) (keysclient.TurnResult, error) {
	return keysclient.TurnResult{SessionID: ref.SessionID, Output: "ok"}, nil
}

func (r *b448Runner) launchSpecs() []keysclient.SessionSpec {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]keysclient.SessionSpec(nil), r.launches...)
}

// b448LogCapture 收集 slog 记录（消息 + 扁平属性），供 V7 断言两端转交日志。
type b448LogCapture struct {
	mu      sync.Mutex
	records []b448LogRecord
}

type b448LogRecord struct {
	level slog.Level
	msg   string
	attrs map[string]string
}

func (c *b448LogCapture) handler() slog.Handler { return &b448LogHandler{c: c} }

func (c *b448LogCapture) find(msgPart string) []b448LogRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []b448LogRecord
	for _, r := range c.records {
		if strings.Contains(r.msg, msgPart) {
			out = append(out, r)
		}
	}
	return out
}

type b448LogHandler struct {
	c     *b448LogCapture
	attrs []slog.Attr
}

func (h *b448LogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *b448LogHandler) Handle(_ context.Context, r slog.Record) error {
	rec := b448LogRecord{level: r.Level, msg: r.Message, attrs: map[string]string{}}
	for _, a := range h.attrs {
		rec.attrs[a.Key] = a.Value.String()
	}
	r.Attrs(func(a slog.Attr) bool {
		rec.attrs[a.Key] = a.Value.String()
		return true
	})
	h.c.mu.Lock()
	h.c.records = append(h.c.records, rec)
	h.c.mu.Unlock()
	return nil
}

func (h *b448LogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &b448LogHandler{c: h.c, attrs: append(append([]slog.Attr(nil), h.attrs...), attrs...)}
}

func (h *b448LogHandler) WithGroup(string) slog.Handler { return h }

// b448Host 是一台测试 agentd：真实 Server + 真实 HTTP 监听 + 真实编制域/collab/keystone（Runner 替身）。
type b448Host struct {
	*ledgerEnv
	name   string
	runner *b448Runner
	logs   *b448LogCapture
	sched  *scheduling.Service
}

// newB448Host 在共享账本 st 上起一台 agentd。consoleUser 空 = 不配控制台身份（执行机形态，F5）；
// writeTimeout>0 时设 server 级 WriteTimeout（V7 用来证明收件端放宽了写截止）。
func newB448Host(t *testing.T, st *ledger.Store, ledgerPath, name, consoleUser string, writeTimeout time.Duration) *b448Host {
	t.Helper()
	backend, err := store.Open(t.TempDir() + "/handoff.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	cfg := &config.Config{Token: testToken, DataDir: t.TempDir(), Name: name, ConsoleUser: consoleUser}
	logs := &b448LogCapture{}
	srv := NewServer(cfg, backend, slog.New(logs.handler()))
	srv.SetLedger(st)
	newManagerForServer(t, srv, nil)
	srv.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	ts := testhttp.NewUnstartedServer(t, srv.Handler())
	if writeTimeout > 0 {
		ts.Config.WriteTimeout = writeTimeout
	}
	ts.Start()
	testhttp.ConfigureClient(ts.Client())
	cfg.Listen = strings.TrimPrefix(ts.URL, "http://")
	env := &ledgerEnv{testAgentdEnv: &testAgentdEnv{srv: srv, ts: ts, st: backend, token: testToken},
		ledger: st, ledgerPath: ledgerPath}
	sched := SetupAutomationForTest(t, srv, st)
	runner := &b448Runner{name: name}
	ks := keystone.New(runner, &fakeCoordNarrator{}, srv.autoLedger, attachLocator{})
	srv.SetKeystone(ks)
	if err := backend.CreateProjectLocation(&proto.ProjectLocation{Name: "handoff", Path: "/repo/" + name}); err != nil {
		t.Fatalf("登记项目位置 %s: %v", name, err)
	}
	return &b448Host{ledgerEnv: env, name: name, runner: runner, logs: logs, sched: sched}
}

// setTargets 整体替换活配置 targets（name → addr）。
func (h *b448Host) setTargets(t *testing.T, targets map[string]string) {
	t.Helper()
	if err := h.srv.swapConf(func(c *config.Config) error {
		c.Targets = map[string]config.Target{}
		for name, addr := range targets {
			c.Targets[name] = config.Target{Addr: addr, Token: testToken}
		}
		return nil
	}); err != nil {
		t.Fatalf("登记 targets: %v", err)
	}
}

func (h *b448Host) addr() string { return strings.TrimPrefix(h.ts.URL, "http://") }

// b448Pair 是共享账本的一对 agentd：a 受理机（有控制台身份），b 执行机（无控制台身份）。
type b448Pair struct {
	a, b *b448Host
}

type b448PairOpts struct {
	bWriteTimeout time.Duration
	maxConc       int // 载体与成员上限；0 = 1
}

// newB448Pair 起一对 agentd 并登记协调小队 t448-coord（唯一成员 t448-oc，machine=t448-linux）。
func newB448Pair(t *testing.T, opts b448PairOpts) *b448Pair {
	t.Helper()
	ledgerPath := filepath.Join(t.TempDir(), "ledger.db")
	st, err := ledger.Open(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	seedAgentdLedger(t, st, "bug", "attachment-gates")
	for name, body := range map[string]string{
		discipline.NameImplement: "本机测试实现纪律",
		discipline.NameReview:    "本机测试审阅纪律",
	} {
		if _, err := st.PutDiscipline(name, body); err != nil {
			t.Fatalf("准备测试纪律 %s: %v", name, err)
		}
	}
	b := newB448Host(t, st, ledgerPath, b448MachineB, "", opts.bWriteTimeout)
	a := newB448Host(t, st, ledgerPath, b448MachineA, "sycm", 0)
	a.setTargets(t, map[string]string{b448MachineB: b.addr()})
	maxConc := opts.maxConc
	if maxConc == 0 {
		maxConc = 1
	}
	putOnlineCarrier(t, a.sched, scheduling.Carrier{
		Name: b448Carrier, Machine: b448MachineB, CLI: "opencode",
		HomeDir: "/home/t448", Credential: scheduling.CredentialStandalone,
		MaxConcurrency: maxConc, Status: scheduling.StatusOnline,
	})
	if err := a.sched.PutSquad(scheduling.Squad{
		Name: b448Squad, Role: scheduling.RoleCoordinator,
		Members: []scheduling.SquadMember{{Carrier: b448Carrier, MaxConcurrency: maxConc}},
	}, 0); err != nil {
		t.Fatalf("登记协调小队: %v", err)
	}
	return &b448Pair{a: a, b: b}
}

// counts 读两级名额计数（共享账本，两台读到同一份）。
func (p *b448Pair) counts(t *testing.T) (squad, carrier int) {
	t.Helper()
	return runningCountIn(t, p.a.srv.autoLedger, "squad/"+b448Squad+"/"+b448Carrier),
		runningCountIn(t, p.a.srv.autoLedger, "carrier/"+b448Carrier)
}

func (p *b448Pair) assertZeroCounts(t *testing.T) {
	t.Helper()
	sq, ca := p.counts(t)
	if sq != 0 || ca != 0 {
		t.Fatalf("名额计数应两级归零：squad=%d carrier=%d", sq, ca)
	}
}

// b448Card 在共享账本上建一张 handoff 项目卡。
func b448Card(t *testing.T, p *b448Pair) string {
	t.Helper()
	card, err := p.a.ledger.CreateCard(ledger.NewCard{
		Title: "B448 跨机卡", Project: "handoff", Workflow: "attachment-gates", Actor: "test"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	return card.ID
}

// b448Raw 直接向某台 agentd 发原始请求（可控头与体），返回状态码与体。
func b448Raw(t *testing.T, h *b448Host, path, body string, forwarded bool) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	if forwarded {
		req.Header.Set(forwardedHeader, "1")
	}
	resp, err := h.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// TestB448PairSharedLedgerOverRealHTTP 是 plan §4 第 1 步的承重假设：两台 Server 共一个账本、经真实
// HTTP 互通——A 经 clientForTarget(t448-linux) 打到 B；B 认 t448-linux 为本机、A 不认；两台的
// scheduling 计数是同一份。
func TestB448PairSharedLedgerOverRealHTTP(t *testing.T) {
	p := newB448Pair(t, b448PairOpts{})
	cl, err := p.a.srv.clientForTarget(b448MachineB)
	if err != nil {
		t.Fatalf("A 取 t448-linux 客户端: %v", err)
	}
	if got := cl.BaseURL(); got != p.b.ts.URL {
		t.Fatalf("A 的 t448-linux 客户端应指向 B（%s），得 %s", p.b.ts.URL, got)
	}
	req, _ := http.NewRequest(http.MethodGet, cl.BaseURL()+"/api/ledger/health", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := cl.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("A 经池客户端打 B: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("B 健康探针应 200，得 %d", resp.StatusCode)
	}
	if !p.b.srv.IsSelfTarget(b448MachineB) || p.a.srv.IsSelfTarget(b448MachineB) {
		t.Fatalf("IsSelfTarget(t448-linux)：B 应真、A 应假，得 B=%v A=%v",
			p.b.srv.IsSelfTarget(b448MachineB), p.a.srv.IsSelfTarget(b448MachineB))
	}
	binding, err := p.a.sched.LaunchAdmit(b448Squad)
	if err != nil {
		t.Fatalf("A 准入: %v", err)
	}
	if got := runningCountIn(t, p.b.srv.autoLedger, "carrier/"+b448Carrier); got != 1 {
		t.Fatalf("B 应读到 A 占的名额 1，得 %d", got)
	}
	if err := p.b.sched.Release(binding.Squad, binding.Carrier); err != nil {
		t.Fatalf("B 归还: %v", err)
	}
	p.assertZeroCounts(t)
}
