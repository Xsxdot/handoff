// account_api_test.go —— 账号 HTTP 面与所在机探测器的竖切回归（B453 段 1 首故事；B464 段 1 起
// 账号由发现产生、探测走「可探登录」）。
//
// 职责：两台真 Server 共用一个 ledger.Store（模拟共享账本），走真实 Handler/鉴权/JSON
// 与 forwardJSON，锁：所在机发现器扫夹具 HOME 认出登录 → 入口机立即探测转发到可探登录所在机
// → 所在机探测器读本机凭据、调（httptest）额度接口、写读数行 → 任一机 GET 看到同一份读数
// （含登录与「上次发现」）；入口机不探测不写读数；所在机离线 502 且读数不变；转发到达非所在机
// 409；所在机探测器未运行 503；PUT expect=0（手工登记）400。周期探测「只探本机可探登录且到期」
// 随探测器迁入 accountwork（TestProberProbesOnlyLocalProbeLogin）。
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

	"github.com/Xsxdot/handoff/internal/agentd/accountwork"
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

// acctHome 建一个夹具主 HOME：auth.json 只有 opencode-go API key 条目（缺省为哨兵 key）。
func acctHome(t *testing.T, key ...string) string {
	t.Helper()
	k := acctSentinelKey
	if len(key) > 0 {
		k = key[0]
	}
	home := t.TempDir()
	path := filepath.Join(home, acctCredRel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"opencode-go":{"type":"api","key":"`+k+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// acctQuota 是测试用额度适配注册表（只装 opencode-go，指向 base）。
func acctQuota(base string) *quota.Registry {
	return quota.NewRegistry(map[string]quota.Prober{
		quota.KindOpencodeGo: &quota.OpencodeGo{BaseURL: base, CredRelPath: acctCredRel},
	})
}

// discoverAs 以 machine 的身份对夹具主 HOME 跑一轮生产发现器（accountwork.Discoverer + 真 quota
// 取材），写进 env 的账号域（共享账本）。返回本轮归并结果。
func discoverAs(t *testing.T, env *schedEnv, machine, home string) accounts.ReconcileResult {
	t.Helper()
	d := accountwork.NewDiscoverer(accountwork.DiscovererConfig{
		Accounts: env.srv.accounts.(*accounts.Service), Quota: acctQuota(""), Machine: machine,
		MainHome: func() (string, error) { return home, nil },
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	res, err := d.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("发现（%s）: %v", machine, err)
	}
	return res
}

// onlyAccount 断言本轮发现恰认出一条登录并返回其账号 id。
func onlyAccount(t *testing.T, res accounts.ReconcileResult) string {
	t.Helper()
	if len(res.Logins) != 1 {
		t.Fatalf("发现应恰认出一条登录，得 %+v", res.Logins)
	}
	return res.Logins[0].Account
}

// newAccountMachine 起一台挂共享账本 st 的 agentd（自名 name）。quotaBase 非空时
// 装配所在机探测器（opencode-go 适配指向假接口）；空 = 本机无探测器。
// 进程 HOME 先钉到空临时目录：任何把「~」展开的路径（探测器、检测）都碰不到本机真实凭据；
// 要探测成功的用例再把 HOME 钉到自己的夹具主 HOME。
func newAccountMachine(t *testing.T, name string, st *ledger.Store, targets map[string]config.Target, quotaBase string) *schedEnv {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
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
		p := accountwork.NewProber(accountwork.ProberConfig{
			Accounts:   acc,
			Quota:      acctQuota(quotaBase),
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

// startProber 启动 env 上装配的探测器（扫描节拍保持缺省：发现前的首扫无账号，
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
	// 探测器把登录 HOME「~」按进程 HOME 展开：钉到夹具，测试绝不触碰本机真实主 HOME 的凭据。
	t.Setenv("HOME", home)
	id := onlyAccount(t, discoverAs(t, b, "b", home))

	code, body := schedReq(t, a, http.MethodGet, "/api/accounts", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/accounts: %d %s", code, body)
	}
	var listed proto.AccountsResp
	if err := json.Unmarshal([]byte(body), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Accounts) != 1 {
		t.Fatalf("发现后应有一个账号: %s", body)
	}
	v := listed.Accounts[0]
	if v.ID != id || v.Kind != "opencode-go" || v.Version != 1 || v.State != "none" || v.IntervalMinutes != 15 ||
		v.WarnPercent != 60 || v.LowPercent != 80 || v.Reading != nil || v.Carriers == nil || len(v.Logins) != 1 ||
		v.Logins[0].Machine != "b" || v.Logins[0].Home != "~" || v.Logins[0].Source != "auth.json" ||
		v.Logins[0].State != "ok" || v.ProbeLogin != v.Logins[0].ID || !strings.HasPrefix(v.Name, "opencode-go-") {
		t.Fatalf("发现建出的账号读面不符: %s", body)
	}
	var sawB bool
	for _, d := range listed.Discovery {
		if d.Machine == "b" && d.At != nil && !d.Self {
			sawB = true
		}
	}
	if !sawB {
		t.Fatalf("入口机 a 应看到 b 的上次发现（self=false、at 在场）: %s", body)
	}

	// 按名字立即探测（{ref} 收名字或 id）：转发到可探登录所在机 b。
	code, body = schedReq(t, a, http.MethodPost, "/api/accounts/"+v.Name+"/probe", "")
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
	// 读数行由所在机 b 写、记下所用登录；定义行未被机写顶版本。
	rec, err := a.srv.SchedulingRegistry().Get(accounts.KindReading, id)
	if err != nil {
		t.Fatal(err)
	}
	var rr accounts.ReadingRecord
	if err := json.Unmarshal(rec.Body, &rr); err != nil {
		t.Fatal(err)
	}
	if rr.Writer != "b" || rr.Login != v.ProbeLogin || strings.Contains(string(rec.Body), acctSentinelKey) {
		t.Fatalf("读数行写者应为所在机 b、记可探登录且不含凭据: writer=%q login=%q", rr.Writer, rr.Login)
	}
	def, err := a.srv.SchedulingRegistry().Get(accounts.KindAccount, id)
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
	// 两个不同身份：一个登录在 a（本机无探测器），一个在 c（离线）。
	idA := onlyAccount(t, discoverAs(t, a, "a", acctHome(t, "sk-b464-machine-a")))
	idC := onlyAccount(t, discoverAs(t, a, "c", acctHome(t, "sk-b464-machine-c")))
	if idA == idC {
		t.Fatal("不同 key 不得归并为同一账号")
	}
	// 所在机离线：502 点名机器，读数不变（仍未探测）。
	code, body := schedReq(t, a, http.MethodPost, "/api/accounts/"+idC+"/probe", "")
	if code != http.StatusBadGateway || !strings.Contains(body, "c") {
		t.Fatalf("所在机离线应 502: %d %s", code, body)
	}
	code, body = schedReq(t, a, http.MethodGet, "/api/accounts", "")
	if code != http.StatusOK || strings.Contains(body, `"reading"`) || strings.Contains(body, `"last_attempt_at"`) {
		t.Fatalf("离线转发失败不得落读数: %s", body)
	}
	// 本机账号但本机探测器未运行：503，不报成功空读数。
	code, body = schedReq(t, a, http.MethodPost, "/api/accounts/"+idA+"/probe", "")
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "探测器未运行") {
		t.Fatalf("本机无探测器应 503: %d %s", code, body)
	}
	// 转发到达的机器不是所在机：409（不再外扇）。
	req, err := http.NewRequest(http.MethodPost, b.ts.URL+"/api/accounts/"+idC+"/probe", nil)
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
	// 名下登录全失效 → 没有可探登录 → 409（a 的 HOME 删掉 auth.json 再扫一轮）。
	goneHome := acctHome(t, "sk-b464-machine-gone")
	idGone := onlyAccount(t, discoverAs(t, a, "g", goneHome))
	if err := os.Remove(filepath.Join(goneHome, acctCredRel)); err != nil {
		t.Fatal(err)
	}
	discoverAs(t, a, "g", goneHome)
	if code, body = schedReq(t, a, http.MethodPost, "/api/accounts/"+idGone+"/probe", ""); code != http.StatusConflict ||
		!strings.Contains(body, "没有可用登录") {
		t.Fatalf("无可探登录应 409: %d %s", code, body)
	}
	// 未知账号 404；手工登记（expect=0）400；校验失败 400；版本冲突 409；缺 expect 400。
	if code, body = schedReq(t, a, http.MethodPost, "/api/accounts/nope/probe", ""); code != http.StatusNotFound {
		t.Fatalf("未知账号应 404: %d %s", code, body)
	}
	if code, body = schedReq(t, a, http.MethodPut, "/api/accounts/"+idA+"?expect=0",
		`{"interval_minutes":5}`); code != http.StatusBadRequest || !strings.Contains(body, "发现") {
		t.Fatalf("expect=0（手工登记）应 400: %d %s", code, body)
	}
	if code, body = schedReq(t, a, http.MethodPut, "/api/accounts/"+idA+"?expect=1",
		`{"warn_percent":0}`); code != http.StatusBadRequest {
		t.Fatalf("显式给 warn_percent:0 应 400（不得按缺省吞掉）: %d %s", code, body)
	}
	if code, body = schedReq(t, a, http.MethodPut, "/api/accounts/"+idA+"?expect=7",
		`{"interval_minutes":5}`); code != http.StatusConflict {
		t.Fatalf("版本冲突应 409: %d %s", code, body)
	}
	if code, body = schedReq(t, a, http.MethodPut, "/api/accounts/"+idA,
		`{"interval_minutes":5}`); code != http.StatusBadRequest {
		t.Fatalf("缺 expect 应 400: %d %s", code, body)
	}
	if code, body = schedReq(t, a, http.MethodPut, "/api/accounts/"+idA+"?expect=1",
		`{"interval_minutes":5}`); code != http.StatusOK || decodeAccountView(t, body).IntervalMinutes != 5 {
		t.Fatalf("编辑间隔应 200 并生效: %d %s", code, body)
	}
	if calls.Load() != 0 {
		t.Fatalf("以上路径都不应调额度接口，得 %d", calls.Load())
	}
}
