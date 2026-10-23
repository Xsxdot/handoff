package accounts_test

// login_state_test.go —— 登录状态与登录代理（B464 段 2，plan Q5/Q16、V5/V6/V8）。
//
// 职责：锁证据型「已过期」（Q5）：经这条登录的探测/检测得到凭据失效且其后凭据未变 → 已过期；
// 离线到期已过且无 refresh token → 已过期；凭据变化（变更时刻晚于失效证据、到期后移、换身份）
// → 回正常；已失效（gone）不被证据改写；登录代理的合并写（打码回填保留原值、空串清除、校验
// 失败拒）；发现器 × 人改代理 × 探测器写失效证据并发 N=50 三类字段各自保留最后一次写（V6）；
// 可探登录 = 正常 + 所在机发现新鲜、按 id 序、换下一条、无可探 → 读面「探测失败 · 没有可用
// 登录」（V8）；kind 规则（na、darwin 上 claude 只认主 HOME）。
// 边界：内存 registry；发现器扫描与探测器调用由 agentd/accountwork 的测试承接。

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

func tp(t time.Time) *time.Time { return &t }

// cxFound 是一份 codex 登录的发现结果（到期、可刷新、凭据变更时刻可调）。
func cxFound(fp string, expires *time.Time, refreshable bool, changed *time.Time) accounts.FoundLogin {
	return accounts.FoundLogin{Kind: accounts.KindCodex, Source: "auth.json", Fingerprint: fp,
		ExpiresAt: expires, Refreshable: refreshable, CredChangedAt: changed}
}

func reconcileAt(t *testing.T, svc *accounts.Service, machine string, when time.Time, homes ...accounts.ScannedHome) accounts.ReconcileResult {
	t.Helper()
	res, err := svc.Reconcile(accounts.Discovery{Machine: machine, At: when, Homes: homes})
	if err != nil {
		t.Fatalf("Reconcile(%s): %v", machine, err)
	}
	return res
}

func onlyLogin(t *testing.T, svc *accounts.Service) accounts.Login {
	t.Helper()
	ls, err := svc.Logins()
	if err != nil || len(ls) != 1 {
		t.Fatalf("应恰一条登录：%+v %v", ls, err)
	}
	return ls[0]
}

// TestLoginStateEvidenceTransitions 是 V5 的表：每步一轮发现或一次失效证据，断言迁移后的状态。
func TestLoginStateEvidenceTransitions(t *testing.T) {
	future := tp(at(3600))
	type step struct {
		name      string
		discover  *accounts.FoundLogin // 非空 = 跑一轮发现（At = when）
		scanEmpty bool                 // 跑一轮扫不到的发现
		evidence  bool                 // 写一次失效证据（at = when）
		when      int
		want      accounts.LoginState
		wantProof bool // 是否仍留着失效证据
	}
	f := func(fl accounts.FoundLogin) *accounts.FoundLogin { return &fl }
	cases := []struct {
		name  string
		steps []step
	}{
		{"探测失效 → 已过期，凭据未变保持，变更时刻晚于证据 → 正常", []step{
			{name: "发现", discover: f(cxFound(fpA, future, true, tp(at(0)))), when: 10, want: accounts.LoginOK},
			{name: "失效证据", evidence: true, when: 20, want: accounts.LoginExpired, wantProof: true},
			{name: "凭据未变的下一轮", discover: f(cxFound(fpA, future, true, tp(at(0)))), when: 30, want: accounts.LoginExpired, wantProof: true},
			{name: "凭据变更时刻缺席的下一轮", discover: f(cxFound(fpA, future, true, nil)), when: 35, want: accounts.LoginExpired, wantProof: true},
			{name: "重新登录（mtime 晚于证据）", discover: f(cxFound(fpA, future, true, tp(at(40)))), when: 50, want: accounts.LoginOK},
		}},
		{"到期时刻后移 = 已刷新 → 正常", []step{
			{name: "发现", discover: f(cxFound(fpA, tp(at(100)), true, nil)), when: 10, want: accounts.LoginOK},
			{name: "失效证据", evidence: true, when: 20, want: accounts.LoginExpired, wantProof: true},
			{name: "到期后移", discover: f(cxFound(fpA, tp(at(5000)), true, nil)), when: 30, want: accounts.LoginOK},
		}},
		{"同槽位换身份 = 重新登录 → 正常", []step{
			{name: "发现", discover: f(cxFound(fpA, future, true, tp(at(0)))), when: 10, want: accounts.LoginOK},
			{name: "失效证据", evidence: true, when: 20, want: accounts.LoginExpired, wantProof: true},
			{name: "换身份", discover: f(cxFound(fpB, future, true, tp(at(0)))), when: 30, want: accounts.LoginOK},
		}},
		{"离线到期已过且无 refresh → 已过期；有 refresh 不算", []step{
			{name: "到期已过 + 可刷新", discover: f(cxFound(fpA, tp(at(5)), true, nil)), when: 10, want: accounts.LoginOK},
			{name: "到期已过 + 不可刷新", discover: f(cxFound(fpA, tp(at(5)), false, nil)), when: 20, want: accounts.LoginExpired},
			{name: "到期后移", discover: f(cxFound(fpA, tp(at(9000)), false, nil)), when: 30, want: accounts.LoginOK},
		}},
		{"已失效不被证据改写；再现时凭据新于证据 → 正常", []step{
			{name: "发现", discover: f(cxFound(fpA, future, true, tp(at(0)))), when: 10, want: accounts.LoginOK},
			{name: "失效证据", evidence: true, when: 20, want: accounts.LoginExpired, wantProof: true},
			{name: "扫不到", scanEmpty: true, when: 30, want: accounts.LoginGone, wantProof: true},
			{name: "已失效时再来证据", evidence: true, when: 40, want: accounts.LoginGone, wantProof: true},
			{name: "再现（新写的凭据）", discover: f(cxFound(fpA, future, true, tp(at(45)))), when: 50, want: accounts.LoginOK},
		}},
		{"再现时凭据仍旧于证据 → 已过期", []step{
			{name: "发现", discover: f(cxFound(fpA, future, true, tp(at(0)))), when: 10, want: accounts.LoginOK},
			{name: "失效证据", evidence: true, when: 20, want: accounts.LoginExpired, wantProof: true},
			{name: "扫不到", scanEmpty: true, when: 30, want: accounts.LoginGone, wantProof: true},
			{name: "再现（同一份旧凭据）", discover: f(cxFound(fpA, future, true, tp(at(0)))), when: 50, want: accounts.LoginExpired, wantProof: true},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := accounts.New(newMem())
			for _, st := range tc.steps {
				switch {
				case st.discover != nil:
					reconcileAt(t, svc, "m1", at(st.when), mainHome(*st.discover))
				case st.scanEmpty:
					reconcileAt(t, svc, "m1", at(st.when), mainHome())
				case st.evidence:
					l := onlyLogin(t, svc)
					if _, err := svc.MarkLoginAuthFailed(l.ID, at(st.when), "m1", "credential_expired"); err != nil {
						t.Fatalf("%s: %v", st.name, err)
					}
				}
				l := onlyLogin(t, svc)
				if l.State != st.want || (l.AuthFailedAt != nil) != st.wantProof {
					t.Fatalf("%s：状态 %q 证据 %v，want %q 证据=%v", st.name, l.State, l.AuthFailedAt, st.want, st.wantProof)
				}
			}
		})
	}
}

func TestMarkLoginAuthFailedKeepsLatestAndRejectsUnknown(t *testing.T) {
	svc := accounts.New(newMem())
	reconcileAt(t, svc, "m1", at(0), mainHome(cxFound(fpA, nil, true, tp(at(0)))))
	l := onlyLogin(t, svc)
	if wrote, err := svc.MarkLoginAuthFailed(l.ID, at(20), "m1", "unauthorized"); err != nil || !wrote {
		t.Fatalf("首次证据应写：%v %v", wrote, err)
	}
	if wrote, err := svc.MarkLoginAuthFailed(l.ID, at(10), "m1", "unauthorized"); err != nil || wrote {
		t.Fatalf("更早的证据不覆盖更晚的：%v %v", wrote, err)
	}
	if got := onlyLogin(t, svc); got.AuthFailedAt == nil || !got.AuthFailedAt.Equal(at(20)) {
		t.Fatalf("应保留最晚证据：%+v", got)
	}
	if _, err := svc.MarkLoginAuthFailed("login-nope", at(30), "m1", "unauthorized"); !errors.Is(err, accounts.ErrLoginNotFound) {
		t.Fatalf("不存在的登录应 ErrLoginNotFound：%v", err)
	}
}

func TestSetLoginProxyMergesMaskedEchoAndClears(t *testing.T) {
	svc := accounts.New(newMem())
	reconcileAt(t, svc, "m1", at(0), mainHome(cxFound(fpA, nil, true, nil)))
	id := onlyLogin(t, svc).ID
	const full = "http://u:secret@127.0.0.1:7890"
	if lv, err := svc.SetLoginProxy(id, full); err != nil || lv.Login.Proxy != full || lv.Version < 2 {
		t.Fatalf("设代理：%+v %v", lv, err)
	}
	if lv, err := svc.SetLoginProxy(id, accounts.MaskProxy(full)); err != nil || lv.Login.Proxy != full {
		t.Fatalf("打码回填应保留原值：%+v %v", lv, err)
	}
	if _, err := svc.SetLoginProxy(id, "ftp://nope"); !errors.Is(err, accounts.ErrInvalid) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("不支持的代理应 ErrInvalid 且不带原密码：%v", err)
	}
	if lv, err := svc.SetLoginProxy(id, ""); err != nil || lv.Login.Proxy != "" {
		t.Fatalf("空串应清除：%+v %v", lv, err)
	}
	if _, err := svc.SetLoginProxy("login-nope", full); !errors.Is(err, accounts.ErrLoginNotFound) {
		t.Fatalf("不存在的登录应 ErrLoginNotFound：%v", err)
	}
	// 发现器的下一轮不得抹掉人写的代理
	if _, err := svc.SetLoginProxy(id, full); err != nil {
		t.Fatal(err)
	}
	reconcileAt(t, svc, "m1", at(60), accounts.ScannedHome{Home: "~", HomeAbs: "/home/u2",
		Logins: []accounts.FoundLogin{cxFound(fpA, nil, true, nil)}})
	if l := onlyLogin(t, svc); l.Proxy != full || l.HomeAbs != "/home/u2" {
		t.Fatalf("发现写与人写代理应各自保留：%+v", l)
	}
}

// TestConcurrentDiscoverProxyEvidenceKeepLastWrites 是 V6 承重：发现器合并写（HomeAbs 每轮变）、
// 人改代理、探测器写失效证据三类写者对同一登录并发各 50 次，最终三类字段各是自己最后一次写。
// 单线程下整行覆盖的写法也会全绿，只有并发交错能区分「读-合并-CAS」。
func TestConcurrentDiscoverProxyEvidenceKeepLastWrites(t *testing.T) {
	const n = 50
	mem := newMem()
	seed := accounts.New(mem)
	reconcileAt(t, seed, "m1", at(0), mainHome(cxFound(fpA, nil, true, nil)))
	id := onlyLogin(t, seed).ID
	var wg sync.WaitGroup
	errs := make(chan error, 3*n)
	run := func(f func(svc *accounts.Service, i int) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc := accounts.New(mem)
			for i := 1; i <= n; i++ {
				if err := f(svc, i); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	run(func(svc *accounts.Service, i int) error {
		_, err := svc.Reconcile(accounts.Discovery{Machine: "m1", At: at(i), Homes: []accounts.ScannedHome{{
			Home: "~", HomeAbs: fmt.Sprintf("/home/u-%d", i), Logins: []accounts.FoundLogin{cxFound(fpA, nil, true, nil)}}}})
		return err
	})
	run(func(svc *accounts.Service, i int) error {
		_, err := svc.SetLoginProxy(id, fmt.Sprintf("http://127.0.0.1:%d", 7000+i))
		return err
	})
	run(func(svc *accounts.Service, i int) error {
		_, err := svc.MarkLoginAuthFailed(id, at(1000+i), "m1", "unauthorized")
		return err
	})
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	l := onlyLogin(t, seed)
	if l.HomeAbs != fmt.Sprintf("/home/u-%d", n) || l.Proxy != fmt.Sprintf("http://127.0.0.1:%d", 7000+n) ||
		l.AuthFailedAt == nil || !l.AuthFailedAt.Equal(at(1000+n)) || l.State != accounts.LoginExpired {
		t.Fatalf("三类字段应各保留最后一次写：%+v", l)
	}
}

// TestProbeLoginNeedsOKAndFreshMachine 是 V8 的账号域半边：可探登录 = 正常 + 所在机发现新鲜
// （3 个发现周期内）+ id 最小；首选失效/所在机发现过期 → 换下一条；无可探 → 读面
// 「探测失败 · 没有可用登录」（不写读数行）。
func TestProbeLoginNeedsOKAndFreshMachine(t *testing.T) {
	now := time.Now().UTC()
	svc := accounts.New(newMem())
	found := cxFound(fpA, nil, true, tp(now.Add(-time.Hour)))
	reconcileAt(t, svc, "m1", now, mainHome(found))
	reconcileAt(t, svc, "m2", now, mainHome(found))
	acct := accounts.AccountID(accounts.KindCodex, "", fpA)
	ids := map[string]string{"m1": accounts.LoginID("m1", "~", accounts.KindCodex, "", "auth.json"),
		"m2": accounts.LoginID("m2", "~", accounts.KindCodex, "", "auth.json")}
	first, second := "m1", "m2"
	if ids["m2"] < ids["m1"] {
		first, second = "m2", "m1"
	}
	v, err := svc.Get(acct)
	if err != nil || v.ProbeLogin != ids[first] || v.State != accounts.StateNone {
		t.Fatalf("两机都新鲜：应取 id 最小的 %s，得 %q（%v）state=%q", first, v.ProbeLogin, err, v.State)
	}
	// 首选那台的发现过期（超过 3 个周期没写 discovery_run）→ 换下一条
	reconcileAt(t, svc, first, now.Add(-accounts.DiscoveryFreshFor-time.Second), mainHome(found))
	if v, _ := svc.Get(acct); v.ProbeLogin != ids[second] {
		t.Fatalf("首选所在机发现过期应换 %s，得 %q", second, v.ProbeLogin)
	}
	reconcileAt(t, svc, first, now, mainHome(found))
	// 首选登录失效 → 换下一条
	if _, err := svc.MarkLoginAuthFailed(ids[first], now, first, "unauthorized"); err != nil {
		t.Fatal(err)
	}
	if v, _ := svc.Get(acct); v.ProbeLogin != ids[second] {
		t.Fatalf("首选登录已过期应换 %s，得 %q", second, v.ProbeLogin)
	}
	// 都不可探 → 读面探测失败 · 没有可用登录；不写读数行
	if _, err := svc.MarkLoginAuthFailed(ids[second], now, second, "unauthorized"); err != nil {
		t.Fatal(err)
	}
	v, err = svc.Get(acct)
	if err != nil || v.ProbeLogin != "" || v.State != accounts.StateFail || v.Record.LastError == nil ||
		!strings.Contains(v.Record.LastError.Message, "没有可用登录") {
		t.Fatalf("无可探登录应读作探测失败 · 没有可用登录：%+v %v", v, err)
	}
	list, err := svc.List()
	if err != nil || len(list) != 1 || list[0].State != accounts.StateFail {
		t.Fatalf("列表与单读同一推导：%+v %v", list, err)
	}
	if v.Record.WrittenAt != (time.Time{}) {
		t.Fatalf("推导不写读数行：%+v", v.Record)
	}
}

// TestKindRulesNAAndMainHomeOnly 锁 kind 规则表：claude/apikey 不支持额度探测（na，阈值与间隔
// 缺席、不挡准入、没有可探登录也不读作探测失败）；darwin 上 claude 只认主 HOME，linux 每个 HOME
// 独立（plan Q6 分支 b，GOOS 由调用方注入）。
func TestKindRulesNAAndMainHomeOnly(t *testing.T) {
	for kind, want := range map[string]bool{accounts.KindOpencodeGo: true, accounts.KindCodex: true, accounts.KindGrok: true,
		accounts.KindAgy: true, accounts.KindClaude: false, accounts.KindAPIKey: false} {
		if got := accounts.SupportsProbe(kind); got != want {
			t.Fatalf("SupportsProbe(%s) = %v，want %v", kind, got, want)
		}
	}
	rules := []struct {
		kind, goos string
		want       bool
	}{
		{accounts.KindClaude, "darwin", true}, {accounts.KindClaude, "linux", false},
		{accounts.KindCodex, "darwin", false}, {accounts.KindGrok, "darwin", false}, {accounts.KindAgy, "linux", false},
	}
	for _, r := range rules {
		if got := accounts.LoginMainHomeOnly(r.kind, r.goos); got != r.want {
			t.Fatalf("LoginMainHomeOnly(%s, %s) = %v，want %v", r.kind, r.goos, got, r.want)
		}
	}

	svc := accounts.New(newMem())
	reconcileAt(t, svc, "m1", at(0), mainHome(
		accounts.FoundLogin{Kind: accounts.KindClaude, Source: "claude-auth", Fingerprint: fpA, Label: "cl@example.com", Refreshable: true},
		accounts.FoundLogin{Kind: accounts.KindAPIKey, Provider: "deepseek", Source: "auth.json", Fingerprint: fpB},
	))
	list, err := svc.List()
	if err != nil || len(list) != 2 {
		t.Fatalf("应建两个 na 账号：%+v %v", list, err)
	}
	for _, v := range list {
		a := v.Account
		if v.State != accounts.StateNA || a.IntervalMinutes != 0 || a.WarnPercent != 0 || a.LowPercent != 0 || v.ProbeLogin != "" {
			t.Fatalf("na 账号：状态 na、阈值与间隔缺席、无可探登录：%+v", v)
		}
		if accounts.StateNA.Blocks() {
			t.Fatal("na 不挡准入")
		}
		if as, err := svc.Assess(a.ID, time.Now()); err != nil || as.State != accounts.StateNA {
			t.Fatalf("准入读面同一推导：%+v %v", as, err)
		}
		withThreshold := a
		withThreshold.IntervalMinutes, withThreshold.WarnPercent, withThreshold.LowPercent = 15, 60, 80
		if _, err := svc.Put(withThreshold, v.Version); !errors.Is(err, accounts.ErrInvalid) {
			t.Fatalf("na 账号不接受阈值/间隔：%v", err)
		}
	}
	if accounts.StateNA.Label() != "不支持额度探测" {
		t.Fatalf("na 标签 = %q", accounts.StateNA.Label())
	}
}
