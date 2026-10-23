// accountwork_s2_test.go —— 发现器的 kind 规则与证据映射、探测器的 na 跳过、失效证据写入与
// 换登录（B464 段 2，plan Q5/Q6 分支 b/Q10/Q11、V5/V8）。
//
// 边界：账本是内存 registry；额度适配用记录请求的假实现或 quota 夹具（临时 HOME），不读本机
// 真实凭据、不出网。
package accountwork

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/quota"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

// kindReader 是只报一种 kind 的假取材：以 HOME 路径为身份，记下被问到的 HOME。
type kindReader struct {
	kind  string
	mu    sync.Mutex
	homes []string
}

func (r *kindReader) Logins(home string) ([]quota.LoginFinding, []error) {
	r.mu.Lock()
	r.homes = append(r.homes, home)
	r.mu.Unlock()
	return []quota.LoginFinding{{Kind: r.kind, Source: r.kind + "-src", Fingerprint: quota.Fingerprint(r.kind + home)}}, nil
}

func (r *kindReader) Probe(context.Context, quota.Request) (quota.Snapshot, error) {
	return quota.Snapshot{}, errors.New("不该被探测")
}

// TestDiscovererClaudeMainHomeOnlyByGOOS 锁 Q6 分支 b：darwin 上 claude 登录只认主 HOME（非主
// HOME 连取材都不做），linux 每个 HOME 独立；其他 kind 不受影响。GOOS 注入，不看真实平台。
func TestDiscovererClaudeMainHomeOnlyByGOOS(t *testing.T) {
	for _, tc := range []struct {
		goos       string
		wantClaude []string // 认出 claude 登录的 HOME
	}{
		{"darwin", []string{"~"}},
		{"linux", []string{"~", "~/.handoff/home/iso"}},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			main := t.TempDir()
			if err := os.MkdirAll(filepath.Join(main, ".handoff", "home", "iso"), 0o700); err != nil {
				t.Fatal(err)
			}
			claude, codex := &kindReader{kind: quota.KindClaude}, &kindReader{kind: quota.KindCodex}
			reg := quota.NewRegistry(map[string]quota.Prober{quota.KindCodex: codex})
			reg.RegisterLoginReader(quota.KindClaude, claude)
			d := NewDiscoverer(DiscovererConfig{Accounts: accounts.New(newMem()), Quota: reg, Machine: "m1", GOOS: tc.goos,
				MainHome: func() (string, error) { return main, nil }})
			res, err := d.RunOnce(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			got := map[string][]string{}
			for _, l := range res.Logins {
				got[l.Kind] = append(got[l.Kind], l.Home)
			}
			sort.Strings(got[accounts.KindClaude])
			if fmt.Sprint(got[accounts.KindClaude]) != fmt.Sprint(tc.wantClaude) {
				t.Fatalf("claude 登录 HOME = %v，want %v", got[accounts.KindClaude], tc.wantClaude)
			}
			if len(got[accounts.KindCodex]) != 2 {
				t.Fatalf("codex 不受 claude 规则影响：%v", got[accounts.KindCodex])
			}
			if len(claude.homes) != len(tc.wantClaude) {
				t.Fatalf("被规则排除的 HOME 不得取材：问了 %v", claude.homes)
			}
		})
	}
}

// TestDiscoverFreshWindowIsThreeRounds 锁 Q10 的新鲜度窗口与发现节拍的倍数关系（3 个周期）。
func TestDiscoverFreshWindowIsThreeRounds(t *testing.T) {
	if accounts.DiscoveryFreshFor != 3*DefaultDiscoverEvery {
		t.Fatalf("新鲜度窗口 %v 应为 3 × 发现节拍 %v", accounts.DiscoveryFreshFor, DefaultDiscoverEvery)
	}
}

// TestDiscovererMapsRefreshAndChangeForExpiredState 是 V5 的发现路径：quota 交出的到期/刷新
// 信息穿过发现器映射进登录状态——离线到期已过且无 refresh → 已过期，有 refresh → 正常。
func TestDiscovererMapsRefreshAndChangeForExpiredState(t *testing.T) {
	main := t.TempDir()
	expired := fakeAccessJWT(time.Now().Add(-time.Hour))
	write := func(home, extra string) {
		p := filepath.Join(home, ".codex", "auth.json")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		body := `{"tokens":{"access_token":"` + expired + `","account_id":"acct-` + filepath.Base(home) + `"` + extra + `}}`
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	iso := filepath.Join(main, ".handoff", "home", "refresh")
	write(main, "")
	write(iso, `,"refresh_token":"`+sentinelKey+`"`)
	reg := quota.NewRegistry(map[string]quota.Prober{quota.KindCodex: &quota.Codex{CredRelPath: ".codex/auth.json"}})
	d := NewDiscoverer(DiscovererConfig{Accounts: accounts.New(newMem()), Quota: reg, Machine: "m1", GOOS: "linux",
		MainHome: func() (string, error) { return main, nil }})
	res, err := d.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]accounts.LoginState{}
	for _, l := range res.Logins {
		states[l.Home] = l.State
	}
	if states["~"] != accounts.LoginExpired || states["~/.handoff/home/refresh"] != accounts.LoginOK {
		t.Fatalf("到期已过：无 refresh → 已过期、有 refresh → 正常；得 %v", states)
	}
}

// fakeAccessJWT 造一个只有 exp 的 JWT（codex 适配只读 exp，不验签）。
func fakeAccessJWT(exp time.Time) string {
	return "eyJhbGciOiJub25lIn0." + b64(fmt.Sprintf(`{"exp":%d}`, exp.Unix())) + ".sig"
}

func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// recordingProber 按 HOME 决定探测结果，并记下每次请求。
type recordingProber struct {
	mu   sync.Mutex
	reqs []quota.Request
	fail map[string]error // HOME → 失败；缺席 = 成功
}

func (p *recordingProber) Probe(_ context.Context, req quota.Request) (quota.Snapshot, error) {
	p.mu.Lock()
	p.reqs = append(p.reqs, req)
	err := p.fail[req.Home]
	p.mu.Unlock()
	if err != nil {
		return quota.Snapshot{}, err
	}
	return quota.Snapshot{Windows: []quota.Window{{Key: quota.WindowWeekly, UsedPercent: 12}}}, nil
}

func (p *recordingProber) requests() []quota.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]quota.Request(nil), p.reqs...)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("5s 内未等到：%s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestProberWritesEvidenceAndSwitchesLogin 是 V8 的探测器半边：首选登录探测 401 → 该登录记失效
// 证据（已过期）→ 换下一条登录立即再探（不等失败退避）→ 读数行记下新登录；探测请求按登录
// 来源取条目、带该登录的代理（未设则回落机器代理）；网络类失败不记证据。
func TestProberWritesEvidenceAndSwitchesLogin(t *testing.T) {
	acc := accounts.New(newMem())
	homes := map[string]string{"~/.handoff/home/a": "/h/a", "~/.handoff/home/b": "/h/b", "~/.handoff/home/c": "/h/c"}
	var scanned []accounts.ScannedHome
	for h, abs := range homes {
		scanned = append(scanned, accounts.ScannedHome{Home: h, HomeAbs: abs, Logins: []accounts.FoundLogin{
			{Kind: accounts.KindCodex, Source: "auth.json", Fingerprint: "sha256:cccc11112222", Refreshable: true}}})
	}
	if _, err := acc.Reconcile(accounts.Discovery{Machine: "self", At: time.Now(), Homes: scanned}); err != nil {
		t.Fatal(err)
	}
	id := accounts.AccountID(accounts.KindCodex, "", "sha256:cccc11112222")
	v, _ := acc.Get(id)
	order := []string{}
	for _, lv := range v.Logins {
		order = append(order, lv.Login.ID)
	}
	// 按 id 序：第一条 401、第二条网络错、第三条成功且设了登录代理
	byID := map[string]accounts.Login{}
	for _, lv := range v.Logins {
		byID[lv.Login.ID] = lv.Login
	}
	first, second, third := byID[order[0]], byID[order[1]], byID[order[2]]
	if _, err := acc.SetLoginProxy(third.ID, "http://127.0.0.1:7999"); err != nil {
		t.Fatal(err)
	}
	qp := &recordingProber{fail: map[string]error{
		first.HomeAbs:  &quota.Failure{Category: quota.CategoryUnauthorized, Message: "401"},
		second.HomeAbs: &quota.Failure{Category: quota.CategoryNetwork, Message: "断网"},
	}}
	p := NewProber(ProberConfig{Accounts: acc, Quota: qp, IsSelf: func(m string) bool { return m == "self" }, Writer: "self",
		ExpandHome: func(h string) (string, error) { return homes[h], nil }, Proxy: func() string { return "http://fallback:1" }})
	p.scanEvery = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	waitFor(t, "首选登录记失效证据", func() bool {
		l, _ := acc.Login(first.ID)
		return l.State == accounts.LoginExpired && l.AuthFailedAt != nil
	})
	waitFor(t, "第二条探测（网络错）", func() bool { return len(qp.requests()) >= 2 })
	// 网络类失败不是凭据失效：不记证据、不换登录，按失败退避（30s）等，本测试内不会再探第二条
	if l, _ := acc.Login(second.ID); l.State != accounts.LoginOK || l.AuthFailedAt != nil {
		t.Fatalf("网络错不得记失效证据：%+v", l)
	}
	if _, err := acc.MarkLoginAuthFailed(second.ID, time.Now(), "test", "unauthorized"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "换到第三条并成功写读数", func() bool {
		v, _ := acc.Get(id)
		return v.Record.Reading != nil && v.Record.Login == third.ID
	})
	p.Stop()
	reqs := qp.requests()
	if len(reqs) != 3 {
		t.Fatalf("应恰探三次（换登录立即再探、不重复）：%+v", reqs)
	}
	for i, want := range []struct{ home, proxy string }{
		{first.HomeAbs, "http://fallback:1"}, {second.HomeAbs, "http://fallback:1"}, {third.HomeAbs, "http://127.0.0.1:7999"},
	} {
		if reqs[i].Home != want.home || reqs[i].Proxy != want.proxy || reqs[i].Entry != "tokens" || reqs[i].Kind != accounts.KindCodex {
			t.Fatalf("第 %d 次请求 = %+v，want home=%s proxy=%s entry=tokens", i+1, reqs[i], want.home, want.proxy)
		}
	}
}

// TestProberSkipsNAAccounts 锁 Q11：claude/第三方 key 账号不探测（调 quota 之前就跳过），立即
// 探测回「不支持额度探测」。
func TestProberSkipsNAAccounts(t *testing.T) {
	acc := accounts.New(newMem())
	if _, err := acc.Reconcile(accounts.Discovery{Machine: "self", At: time.Now(), Homes: []accounts.ScannedHome{{Home: "~", HomeAbs: "/h",
		Logins: []accounts.FoundLogin{{Kind: accounts.KindClaude, Source: "claude-auth", Fingerprint: "sha256:dddd11112222"},
			{Kind: accounts.KindAPIKey, Provider: "deepseek", Source: "auth.json", Fingerprint: "sha256:eeee11112222"}}}}}); err != nil {
		t.Fatal(err)
	}
	qp := &recordingProber{}
	p := NewProber(ProberConfig{Accounts: acc, Quota: qp, IsSelf: func(string) bool { return true }, Writer: "self",
		ExpandHome: func(string) (string, error) { return "/h", nil }})
	p.scanEvery = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	time.Sleep(80 * time.Millisecond)
	if _, err := p.ProbeNow(context.Background(), "claude:dddd11112222"); !errors.Is(err, ErrProbeUnsupported) {
		t.Fatalf("立即探测 na 账号应 ErrProbeUnsupported：%v", err)
	}
	p.Stop()
	if n := len(qp.requests()); n != 0 {
		t.Fatalf("na 账号不得调额度适配，调了 %d 次", n)
	}
	list, _ := acc.List()
	for _, v := range list {
		if v.Record.LastAttemptAt != nil || v.State != accounts.StateNA {
			t.Fatalf("na 账号不写读数行：%+v", v)
		}
	}
}

// TestDiscovererSecretsNeverLeakAllKinds 是 V4 扩到段 2 的全部取材路径：一个 HOME 里放 codex、
// grok（`issuer::<uuid>` 条目）、agy token、opencode 第三方 key 与 claude（假 CLI）的哨兵凭据，
// 另一个 HOME 放同样 kind 的坏形状文件；跑一轮生产发现器 → 日志捕获、账本全部行体、归并结果里
// 零出现凭据值与原始身份材料（account_id、user_id、sub、accountUuid），只有短指纹与邮箱 label。
func TestDiscovererSecretsNeverLeakAllKinds(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	const (
		acct = "acct-SENTINEL-raw-identity"
		user = "user-SENTINEL-raw-identity"
		sub  = "sub-SENTINEL-raw-identity"
		uuid = "uuid-SENTINEL-raw-identity"
	)
	put := func(home, rel, body string) {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	main := t.TempDir()
	idTok := "eyJhbGciOiJub25lIn0." + b64(`{"email":"x@example.com","sub":"`+sub+`"}`) + "." + sentinelKey
	put(main, ".codex/auth.json", `{"tokens":{"access_token":"`+fakeAccessJWT(time.Now().Add(time.Hour))+`","account_id":"`+acct+
		`","id_token":"`+idTok+`","refresh_token":"`+sentinelKey+`"}}`)
	put(main, ".grok/auth.json", `{"https://auth.x.ai::0b8e-SENTINEL":{"key":"`+sentinelKey+`","user_id":"`+user+
		`","email":"g@example.com","expires_at":"2099-01-01T00:00:00Z","refresh_token":"`+sentinelKey+`"}}`)
	put(main, ".gemini/antigravity-cli/antigravity-oauth-token", `{"auth_method":"consumer","id_token":"`+idTok+
		`","token":{"access_token":"`+sentinelKey+`","refresh_token":"`+sentinelKey+`","expiry":"2099-01-01T00:00:00Z"}}`)
	put(main, credRel, `{"deepseek":{"type":"api","key":"`+sentinelKey+`"}}`)
	put(main, ".claude.json", `{"oauthAccount":{"accountUuid":"`+uuid+`","emailAddress":"c@example.com"}}`)
	bad := filepath.Join(main, ".handoff", "home", "bad")
	for _, rel := range []string{".codex/auth.json", ".grok/auth.json", ".gemini/antigravity-cli/antigravity-oauth-token", credRel} {
		put(bad, rel, `{"x":"`+sentinelKey)
	}
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho '{\"loggedIn\":true}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	reg := quota.NewRegistry(map[string]quota.Prober{
		quota.KindOpencodeGo: &quota.OpencodeGo{CredRelPath: credRel},
		quota.KindCodex:      &quota.Codex{CredRelPath: ".codex/auth.json"},
		quota.KindGrok:       &quota.Grok{CredRelPath: ".grok/auth.json"},
		quota.KindAgy:        &quota.Agy{},
	})
	reg.RegisterLoginReader(quota.KindClaude, &quota.Claude{Binary: bin, Timeout: 5 * time.Second})
	mem := newMem()
	acc := accounts.New(mem)
	d := NewDiscoverer(DiscovererConfig{Accounts: acc, Quota: reg, Machine: "m1", GOOS: "linux",
		MainHome: func() (string, error) { return main, nil },
		Logger:   slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	res, err := d.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, l := range res.Logins {
		kinds[l.Kind] = true
	}
	for _, k := range []string{accounts.KindCodex, accounts.KindGrok, accounts.KindAgy, accounts.KindAPIKey, accounts.KindClaude} {
		if !kinds[k] {
			t.Fatalf("主 HOME 应认出 %s 登录：%+v", k, res.Logins)
		}
	}
	runs, _ := acc.DiscoveryRuns()
	if len(runs) != 1 || len(runs[0].Errors) < 4 {
		t.Fatalf("坏形状 HOME 的各 kind 问题应记进 discovery_run：%+v", runs)
	}
	views, _ := acc.List()
	raw, _ := json.Marshal(struct {
		R any
		V any
		D any
	}{res, views, runs})
	all := logs.String() + mem.dump() + string(raw)
	for _, secret := range []string{sentinelKey, acct, user, sub, uuid, "0b8e-SENTINEL"} {
		if strings.Contains(all, secret) {
			t.Fatalf("发现路径泄露了 %q", secret)
		}
	}
}
