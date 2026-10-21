// account_api_test.go —— 账号 HTTP 面与所在机探测器的竖切回归（B453 段 1 首故事）。
//
// 职责：两台真 Server 共用一个 ledger.Store（模拟共享账本），走真实 Handler/鉴权/JSON
// 与 forwardJSON，锁：入口机登记 → 立即探测转发到所在机 → 所在机探测器读本机凭据、
// 调（httptest）额度接口、写读数行 → 任一机 GET 看到同一份读数；入口机不探测不写读数；
// 所在机离线 502 且读数不变；转发到达非所在机 409；所在机探测器未运行 503；周期探测
// 只探本机且到期账号。
// 边界：额度接口是 httptest，凭据是临时 HOME 下的哨兵 auth.json；真实 opencode.ai 与
// 真机网络归协调者 R1。
package agentd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/hostapi"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/quota"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/testhttp"
)

const (
	acctSentinelKey = "sk-SENTINEL-b453-agentd-0123456789"
	acctCredRel     = ".local/share/opencode/auth.json"
)

// fakeOpencodeGo 是假 opencode-go 额度接口：数调用次数，核 Bearer 后回三窗口。
func fakeOpencodeGo(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	return testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/zen/go/v1/usage" || r.Header.Get("Authorization") != "Bearer "+acctSentinelKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"usage":{"rolling":{"percent":34,"resetsAt":1761300000},"weekly":{"percent":58},"monthly":{"percent":22}}}`))
	}))
}

func acctHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, acctCredRel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"opencode-go":{"type":"api","key":"`+acctSentinelKey+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// newAccountMachine 起一台挂共享账本 st 的 agentd（自名 name）。quotaBase 非空时
// 装配所在机探测器（opencode-go 适配指向假接口）；空 = 本机无探测器。
func newAccountMachine(t *testing.T, name string, st *ledger.Store, targets map[string]config.Target, quotaBase string) *schedEnv {
	t.Helper()
	dataStore, err := store.Open(t.TempDir() + "/handoff.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	cfg := &config.Config{Name: name, Token: testToken, DataDir: t.TempDir(), Targets: targets}
	srv := NewServer(cfg, dataStore, slog.New(slog.NewTextHandler(io.Discard, nil)))
	SetupAutomationForTest(t, srv, st)
	acc := accounts.New(srv.SchedulingRegistry())
	srv.SetAccounts(acc)
	if quotaBase != "" {
		p := NewAccountProber(AccountProberConfig{
			Accounts: acc,
			Quota: quota.NewRegistry(map[string]quota.Prober{
				quota.KindOpencodeGo: &quota.OpencodeGo{BaseURL: quotaBase, CredRelPath: acctCredRel},
			}),
			IsSelf:     srv.IsSelfMachine,
			Writer:     name,
			ExpandHome: hostapi.ExpandHomePath,
			Proxy:      func() string { return "" },
			Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		srv.SetAccountProber(p)
		t.Cleanup(p.Stop)
	}
	ts := testhttp.NewServer(t, srv.Handler())
	return &schedEnv{testAgentdEnv: &testAgentdEnv{srv: srv, ts: ts, st: dataStore, token: cfg.Token}, svc: mustScheduling(t, srv)}
}

// startProber 启动 env 上装配的探测器（扫描节拍保持 30s 缺省：登记前的首扫无账号，
// 测试时长内不会再扫，立即探测是唯一触发）。
func startProber(t *testing.T, env *schedEnv) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	env.srv.accountProber.Start(ctx)
}

func decodeAccountView(t *testing.T, body string) proto.AccountView {
	t.Helper()
	var v proto.AccountView
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("解码 AccountView: %v (%s)", err, body)
	}
	return v
}

func TestAccountProbeForwardToHostMachineWhichWritesReading(t *testing.T) {
	var calls atomic.Int32
	quotaSrv := fakeOpencodeGo(t, &calls)
	st, err := ledger.Open(t.TempDir() + "/shared.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	b := newAccountMachine(t, "b", st, nil, quotaSrv.URL)
	startProber(t, b)
	a := newAccountMachine(t, "a", st, map[string]config.Target{"b": {Addr: b.ts.URL, Token: testToken}}, "")
	home := acctHome(t)

	code, body := schedReq(t, a, http.MethodPut, "/api/accounts/og-b?expect=0",
		`{"kind":"opencode-go","machine":"b","home_dir":"`+home+`"}`)
	if code != http.StatusOK {
		t.Fatalf("入口机登记: %d %s", code, body)
	}
	v := decodeAccountView(t, body)
	if v.Version != 1 || v.State != "none" || v.Entry != "opencode-go" || v.IntervalMinutes != 15 ||
		v.WarnPercent != 60 || v.LowPercent != 80 || v.Reading != nil || v.Carriers == nil {
		t.Fatalf("登记回执缺省值/状态不符: %+v", v)
	}

	code, body = schedReq(t, a, http.MethodPost, "/api/accounts/og-b/probe", "")
	if code != http.StatusOK {
		t.Fatalf("入口机立即探测（转发到 b）: %d %s", code, body)
	}
	v = decodeAccountView(t, body)
	if v.State != "ok" || v.Reading == nil || len(v.Reading.Windows) != 3 ||
		v.Reading.Windows[0].Key != "session" || v.Reading.Windows[0].UsedPercent != 34 ||
		v.Reading.Windows[0].ResetsAt == nil || v.LastAttemptAt == nil || v.LastError != nil {
		t.Fatalf("探测回执读数不符: %s", body)
	}
	if calls.Load() != 1 {
		t.Fatalf("额度接口应只被所在机调用一次，得 %d", calls.Load())
	}

	// 跨机可见：两台机 GET 读到同一份读数（共享账本）。
	for _, env := range []*schedEnv{a, b} {
		code, body = schedReq(t, env, http.MethodGet, "/api/accounts", "")
		if code != http.StatusOK {
			t.Fatalf("GET /api/accounts: %d %s", code, body)
		}
		var resp proto.AccountsResp
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Accounts) != 1 || resp.Accounts[0].Reading == nil || resp.Accounts[0].Reading.Windows[1].UsedPercent != 58 {
			t.Fatalf("跨机读数不一致: %s", body)
		}
		if strings.Contains(body, acctSentinelKey) {
			t.Fatal("API 响应泄露凭据")
		}
	}
	// 读数行由所在机 b 写；定义行未被机写顶版本。
	rec, err := a.srv.SchedulingRegistry().Get(accounts.KindReading, "og-b")
	if err != nil {
		t.Fatal(err)
	}
	var rr accounts.ReadingRecord
	if err := json.Unmarshal(rec.Body, &rr); err != nil {
		t.Fatal(err)
	}
	if rr.Writer != "b" || strings.Contains(string(rec.Body), acctSentinelKey) {
		t.Fatalf("读数行写者应为所在机 b 且不含凭据: writer=%q", rr.Writer)
	}
	def, err := a.srv.SchedulingRegistry().Get(accounts.KindAccount, "og-b")
	if err != nil || def.Version != 1 || strings.Contains(string(def.Body), acctSentinelKey) {
		t.Fatalf("定义行应保持 v1 且不含凭据: v=%d err=%v", def.Version, err)
	}
}

func TestAccountProbeForwardFailureModes(t *testing.T) {
	var calls atomic.Int32
	quotaSrv := fakeOpencodeGo(t, &calls)
	st, err := ledger.Open(t.TempDir() + "/shared.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	dead := testhttp.NewServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	b := newAccountMachine(t, "b", st, nil, quotaSrv.URL)
	startProber(t, b)
	a := newAccountMachine(t, "a", st, map[string]config.Target{
		"b": {Addr: b.ts.URL, Token: testToken},
		"c": {Addr: deadURL, Token: testToken},
	}, "")
	home := acctHome(t)
	for _, m := range []string{"a", "c"} {
		code, body := schedReq(t, a, http.MethodPut, "/api/accounts/og-"+m+"?expect=0",
			`{"kind":"opencode-go","machine":"`+m+`","home_dir":"`+home+`"}`)
		if code != http.StatusOK {
			t.Fatalf("登记 og-%s: %d %s", m, code, body)
		}
	}
	// 所在机离线：502 点名机器，读数不变（仍未探测）。
	code, body := schedReq(t, a, http.MethodPost, "/api/accounts/og-c/probe", "")
	if code != http.StatusBadGateway || !strings.Contains(body, "c") {
		t.Fatalf("所在机离线应 502: %d %s", code, body)
	}
	code, body = schedReq(t, a, http.MethodGet, "/api/accounts", "")
	if code != http.StatusOK || strings.Contains(body, `"reading"`) || strings.Contains(body, `"last_attempt_at"`) {
		t.Fatalf("离线转发失败不得落读数: %s", body)
	}
	// 本机账号但本机探测器未运行：503，不报成功空读数。
	code, body = schedReq(t, a, http.MethodPost, "/api/accounts/og-a/probe", "")
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "探测器未运行") {
		t.Fatalf("本机无探测器应 503: %d %s", code, body)
	}
	// 转发到达的机器不是所在机：409（不再外扇）。
	req, err := http.NewRequest(http.MethodPost, b.ts.URL+"/api/accounts/og-c/probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set(forwardedHeader, "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(data), "所在机") {
		t.Fatalf("转发到达非所在机应 409: %d %s", resp.StatusCode, data)
	}
	// 未知账号 404；校验失败 400；版本冲突 409。
	if code, body = schedReq(t, a, http.MethodPost, "/api/accounts/nope/probe", ""); code != http.StatusNotFound {
		t.Fatalf("未知账号应 404: %d %s", code, body)
	}
	if code, body = schedReq(t, a, http.MethodPut, "/api/accounts/bad?expect=0",
		`{"kind":"claude","machine":"b","home_dir":"/x"}`); code != http.StatusBadRequest || !strings.Contains(body, "claude") {
		t.Fatalf("未知 kind 应 400: %d %s", code, body)
	}
	if code, body = schedReq(t, a, http.MethodPut, "/api/accounts/bad?expect=0",
		`{"kind":"opencode-go","machine":"b","home_dir":"/x","warn_percent":0}`); code != http.StatusBadRequest {
		t.Fatalf("显式给 warn_percent:0 应 400（不得按缺省吞掉）: %d %s", code, body)
	}
	if code, body = schedReq(t, a, http.MethodPut, "/api/accounts/og-c?expect=0",
		`{"kind":"opencode-go","machine":"c","home_dir":"/x"}`); code != http.StatusConflict {
		t.Fatalf("重复新建应 409: %d %s", code, body)
	}
	if code, body = schedReq(t, a, http.MethodPut, "/api/accounts/og-c",
		`{"kind":"opencode-go","machine":"c","home_dir":"/x"}`); code != http.StatusBadRequest {
		t.Fatalf("缺 expect 应 400: %d %s", code, body)
	}
	if calls.Load() != 0 {
		t.Fatalf("以上路径都不应调额度接口，得 %d", calls.Load())
	}
}

func TestAccountProberPeriodicProbesOnlyLocalDueAccounts(t *testing.T) {
	var calls atomic.Int32
	quotaSrv := fakeOpencodeGo(t, &calls)
	st, err := ledger.Open(t.TempDir() + "/shared.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	b := newAccountMachine(t, "b", st, nil, quotaSrv.URL)
	home := acctHome(t)
	for _, m := range []string{"b", "elsewhere"} {
		code, body := schedReq(t, b, http.MethodPut, "/api/accounts/og-"+m+"?expect=0",
			`{"kind":"opencode-go","machine":"`+m+`","home_dir":"`+home+`","interval_minutes":5}`)
		if code != http.StatusOK {
			t.Fatalf("登记 og-%s: %d %s", m, code, body)
		}
	}
	p := b.srv.accountProber
	p.scanEvery = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for {
		v, err := b.srv.accounts.Get("og-b")
		if err != nil {
			t.Fatal(err)
		}
		if v.Record.Reading != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("周期探测未在 5s 内写入本机账号读数")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// 再跑若干扫描周期：未到间隔（5 分钟）不得重探；他机账号永不探。
	time.Sleep(100 * time.Millisecond)
	p.Stop()
	if calls.Load() != 1 {
		t.Fatalf("到期判据失效：额度接口被调 %d 次，want 1", calls.Load())
	}
	other, err := b.srv.accounts.Get("og-elsewhere")
	if err != nil {
		t.Fatal(err)
	}
	if other.Record.Reading != nil || other.Record.LastAttemptAt != nil {
		t.Fatalf("他机账号不得被本机探测: %+v", other.Record)
	}
}
