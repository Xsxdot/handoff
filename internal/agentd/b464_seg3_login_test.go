// b464_seg3_login_test.go —— 段 3 的三条新 HTTP 面（B464 plan A10–A12、V13）：
//   - POST /api/logins/home：只在 ~/.handoff/home/ 下建 0700 新目录、目录名字符集、已存在 409、
//     登录命令表（R0-8）与 HOME 前缀、darwin 上 claude 只允许主 HOME、他机转发后由入口机补 ssh 行；
//   - POST /api/logins/check：单 HOME 发现写账本、尚未识别 → []、新账号/已有账号两种结果、
//     HOME 形状白名单、他机一跳转发、转发到达非目标机 409；
//   - POST /api/accounts/discover：全队扇出（本机 + 设备表）、离线机器单独报错整体 200、
//     ?machine= 只跑一台、转发不二次扇出。
//
// 边界：一切 HOME 都是 t.TempDir 夹具（测试注入 loginHomeDir 与发现器 MainHome，不改进程 HOME）；
// 凭据取材用假 LoginReader，只回短指纹与 label，不读本机真实凭据、不出网（远端是本进程 httptest）。
package agentd

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/agentd/accountwork"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/quota"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
	"github.com/Xsxdot/handoff/internal/testhttp"
)

// fakeLoginReader 是按 HOME 绝对路径回固定发现的假取材（只回短指纹与 label）。
type fakeLoginReader struct {
	mu    sync.Mutex
	byAbs map[string]quota.LoginFinding
}

func (r *fakeLoginReader) Logins(home string) ([]quota.LoginFinding, []error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if f, ok := r.byAbs[home]; ok {
		return []quota.LoginFinding{f}, nil
	}
	return nil, nil
}

// newSeg3Env 组装带自名与设备表的编制/账号环境；targets 里的远端由测试给。
func newSeg3Env(t *testing.T, name string, targets map[string]config.Target) (*schedEnv, *accounts.Service) {
	t.Helper()
	st, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	env := newTestAgentdEnvWithCfg(t, &config.Config{
		Name: name, Token: testToken, Targets: targets,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	env.srv.SetLedger(st)
	SetupAutomationForTest(t, env.srv, st)
	acc := accounts.New(env.srv.SchedulingRegistry())
	env.srv.SetAccounts(acc)
	return &schedEnv{testAgentdEnv: env, svc: mustScheduling(t, env.srv)}, acc
}

func seg3Req(t *testing.T, env *schedEnv, method, path, body string, headers map[string]string) (int, string) {
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
	for k, v := range headers {
		req.Header.Set(k, v)
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

// withLoginHome 把新增登录/单 HOME 检查展开 `~` 的入口钉到夹具主 HOME（不改进程 HOME）。
func withLoginHome(t *testing.T, main string) {
	t.Helper()
	prev := loginHomeDir
	loginHomeDir = func() (string, error) { return main, nil }
	t.Cleanup(func() { loginHomeDir = prev })
}

func TestLoginHomeCreatesIsolatedHomeAndCommandTable(t *testing.T) {
	prev := loginGOOS
	t.Cleanup(func() { loginGOOS = prev })
	loginGOOS = "linux" // 这条锁命令表与建目录，不锁平台分支；darwin 上的 claude 拒绝另测。
	main := t.TempDir()
	withLoginHome(t, main)
	env, _ := newSeg3Env(t, "self", nil)
	abs := func(dir string) string { return filepath.Join(main, ".handoff", "home", dir) }

	for _, tc := range []struct {
		agent, dir, run, note string
	}{
		{"opencode", "oc-1234", "opencode console login", "opencode auth login -p"},
		{"codex", "cx-1234", "codex login", "codex login --device-auth"},
		{"grok", "gk-1234", "grok login", ""},
		{"agy", "agy-1234", "agy", "交互运行"},
		{"claude", "cl-1234", "claude auth login", ""},
	} {
		code, body := seg3Req(t, env, http.MethodPost, "/api/logins/home",
			`{"agent":"`+tc.agent+`","dir":"`+tc.dir+`"}`, nil)
		if code != http.StatusOK {
			t.Fatalf("%s 新增登录应 200：%d %s", tc.agent, code, body)
		}
		var resp proto.LoginHomeResp
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatalf("%s 响应不是 LoginHomeResp：%v %s", tc.agent, err, body)
		}
		wantHome := "~/.handoff/home/" + tc.dir
		if resp.Home != wantHome {
			t.Fatalf("%s home = %q，want %q", tc.agent, resp.Home, wantHome)
		}
		if !strings.HasPrefix(resp.Command.Run, "HOME="+abs(tc.dir)+" ") || !strings.Contains(resp.Command.Run, tc.run) {
			t.Fatalf("%s 登录命令应带 HOME 前缀：%q", tc.agent, resp.Command.Run)
		}
		if tc.note != "" && !strings.Contains(resp.Command.Note, tc.note) {
			t.Fatalf("%s note 应含 %q：%q", tc.agent, tc.note, resp.Command.Note)
		}
		if resp.Command.SSH != "" {
			t.Fatalf("本机新增登录不应给 ssh 行：%q", resp.Command.SSH)
		}
		info, err := os.Stat(abs(tc.dir))
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s 应建 0700 目录：%v %v", tc.agent, info, err)
		}
	}

	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"非法目录名", `{"agent":"codex","dir":"../evil"}`, http.StatusBadRequest},
		{"含大写", `{"agent":"codex","dir":"CX-1"}`, http.StatusBadRequest},
		{"空目录名", `{"agent":"codex","dir":""}`, http.StatusBadRequest},
		{"未知 agent", `{"agent":"vim","dir":"vim-1"}`, http.StatusBadRequest},
		{"目录已存在", `{"agent":"codex","dir":"cx-1234"}`, http.StatusConflict},
	} {
		if code, body := seg3Req(t, env, http.MethodPost, "/api/logins/home", tc.body, nil); code != tc.want {
			t.Fatalf("%s → %d %s，want %d", tc.name, code, body, tc.want)
		}
	}
	if _, err := os.Stat(filepath.Join(main, "evil")); !os.IsNotExist(err) {
		t.Fatalf("非法目录名不得越出 ~/.handoff/home：%v", err)
	}
}

// TestLoginHomeClaudeDarwinMainHomeOnly 锁 Q6 分支 b：darwin 上的 claude 凭据在钥匙串、同机
// 所有 HOME 共用，新增登录不能建隔离 HOME；linux 不受影响（cmd/agentd 组装点注入 runtime.GOOS）。
func TestLoginHomeClaudeDarwinMainHomeOnly(t *testing.T) {
	main := t.TempDir()
	withLoginHome(t, main)
	env, _ := newSeg3Env(t, "self", nil)
	prev := loginGOOS
	t.Cleanup(func() { loginGOOS = prev })

	loginGOOS = "darwin"
	code, body := seg3Req(t, env, http.MethodPost, "/api/logins/home", `{"agent":"claude","dir":"cl-darwin"}`, nil)
	if code != http.StatusBadRequest || !strings.Contains(body, "主 HOME") {
		t.Fatalf("darwin 上 claude 新增登录应 400 并说明只允许主 HOME：%d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(main, ".handoff", "home", "cl-darwin")); !os.IsNotExist(err) {
		t.Fatalf("被拒的 claude 登录不得建目录：%v", err)
	}

	loginGOOS = "linux"
	if code, body := seg3Req(t, env, http.MethodPost, "/api/logins/home", `{"agent":"claude","dir":"cl-linux"}`, nil); code != http.StatusOK {
		t.Fatalf("linux 上 claude 新增登录应 200：%d %s", code, body)
	}
}

// TestLoginHomeClaudeDarwinRejectsWithKeychainReason 另钉一支：darwin 上 claude 新增登录被拒
// 400，文案必须说明凭据在系统钥匙串（Q6 分支 b）；不是只写「只允许主 HOME」。
func TestLoginHomeClaudeDarwinRejectsWithKeychainReason(t *testing.T) {
	main := t.TempDir()
	withLoginHome(t, main)
	env, _ := newSeg3Env(t, "self", nil)
	prev := loginGOOS
	t.Cleanup(func() { loginGOOS = prev })
	loginGOOS = "darwin"
	code, body := seg3Req(t, env, http.MethodPost, "/api/logins/home", `{"agent":"claude","dir":"cl-kc"}`, nil)
	if code != http.StatusBadRequest || !strings.Contains(body, "钥匙串") {
		t.Fatalf("darwin 上 claude 新增登录应 400 并说明钥匙串：%d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(main, ".handoff", "home", "cl-kc")); !os.IsNotExist(err) {
		t.Fatalf("被拒的 claude 登录不得建目录：%v", err)
	}
}

// TestLoginHomeForwardKeepsQueryAndAddsSSH：他机建 HOME 由入口机按设备表 User@host 补 ssh 行；
// 转发带 X-Handoff-Forwarded 与目标 token；relay 形态与本机不给 ssh 行。
func TestLoginHomeForwardKeepsQueryAndAddsSSH(t *testing.T) {
	var mu sync.Mutex
	var gotPath, gotQuery, gotAuth, gotForwarded string
	remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		gotAuth, gotForwarded = r.Header.Get("Authorization"), r.Header.Get("X-Handoff-Forwarded")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"home":"~/.handoff/home/cx-r","command":{"run":"HOME=/remote/home/.handoff/home/cx-r codex login"}}`)
	}))
	t.Cleanup(remote.Close)

	env, _ := newSeg3Env(t, "self", map[string]config.Target{
		"remote": {Addr: remote.URL, Token: "remote-token", User: "dev"},
	})
	code, body := seg3Req(t, env, http.MethodPost, "/api/logins/home?machine=remote",
		`{"agent":"codex","dir":"cx-r"}`, nil)
	if code != http.StatusOK {
		t.Fatalf("他机新增登录应 200：%d %s", code, body)
	}
	var resp proto.LoginHomeResp
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Home != "~/.handoff/home/cx-r" || !strings.Contains(resp.Command.Run, "codex login") {
		t.Fatalf("应原样交回目标机结果：%+v", resp)
	}
	if resp.Command.SSH != "ssh dev@127.0.0.1" {
		t.Fatalf("入口机应补 ssh 行（设备表 User@host）：%q", resp.Command.SSH)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/api/logins/home" || gotQuery != "machine=remote" {
		t.Fatalf("转发应保留路径/查询串：%s?%s", gotPath, gotQuery)
	}
	if gotForwarded == "" {
		t.Fatal("转发应带 X-Handoff-Forwarded（一跳封顶）")
	}
	if gotAuth != "Bearer remote-token" {
		t.Fatalf("转发应带目标 token，得 %q", gotAuth)
	}
}

func TestLoginSSHLineOnlyForDirectRemoteTarget(t *testing.T) {
	env, _ := newSeg3Env(t, "self", map[string]config.Target{
		"direct": {Addr: "10.0.0.2:7777", Token: "t", User: "dev"},
		"relay":  {Relay: "wss://relay.example", Node: "n1", Credential: "c", Token: "t"},
		"bare":   {Addr: "10.0.0.3:7777", Token: "t"},
	})
	for _, tc := range []struct{ machine, want string }{
		{"self", ""},
		{"", ""},
		{"direct", "ssh dev@10.0.0.2"},
		{"bare", "ssh 10.0.0.3"},
		{"relay", ""},
	} {
		if got := env.srv.loginSSHLine(tc.machine); got != tc.want {
			t.Fatalf("machine %q ssh = %q，want %q", tc.machine, got, tc.want)
		}
	}
}

func TestLoginCheckSingleHomeReportsNewAndExistingAccount(t *testing.T) {
	main := t.TempDir()
	withLoginHome(t, main)
	env, acc := newSeg3Env(t, "self", nil)
	homeAbs := filepath.Join(main, ".handoff", "home", "cx")
	if err := os.MkdirAll(homeAbs, 0o700); err != nil {
		t.Fatal(err)
	}
	reader := &fakeLoginReader{byAbs: map[string]quota.LoginFinding{
		homeAbs: {Kind: quota.KindCodex, Source: "auth.json", Fingerprint: "sha256:abababababab", Label: "cx@example.com"},
	}}
	reg := quota.NewRegistry(map[string]quota.Prober{})
	reg.RegisterLoginReader(quota.KindCodex, reader)
	d := accountwork.NewDiscoverer(accountwork.DiscovererConfig{Accounts: acc, Quota: reg, Machine: "self",
		GOOS: "linux", MainHome: func() (string, error) { return main, nil },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	env.srv.SetAccountDiscoverer(d)

	check := func(home string) proto.LoginCheckResp {
		t.Helper()
		code, body := seg3Req(t, env, http.MethodPost, "/api/logins/check", `{"home":"`+home+`"}`, nil)
		if code != http.StatusOK {
			t.Fatalf("检查 %s 应 200：%d %s", home, code, body)
		}
		var resp proto.LoginCheckResp
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatalf("响应不是 LoginCheckResp：%v %s", err, body)
		}
		return resp
	}

	first := check("~/.handoff/home/cx")
	if len(first.Logins) != 1 {
		t.Fatalf("应识别一条登录：%+v", first)
	}
	got := first.Logins[0]
	if got.Login == "" || got.Account != "codex:abababababab" || got.AccountName != "cx@example.com" ||
		!got.NewAccount || got.AccountLogins != 1 {
		t.Fatalf("首次检查应报新账号：%+v", got)
	}
	second := check("~/.handoff/home/cx")
	if len(second.Logins) != 1 || second.Logins[0].Login != got.Login ||
		second.Logins[0].NewAccount || second.Logins[0].AccountLogins != 1 {
		t.Fatalf("再次检查应挂到已有账号：%+v", second)
	}
	if resp := check("~"); len(resp.Logins) != 0 {
		t.Fatalf("主 HOME 未识别到登录应回 []：%+v", resp)
	}

	for _, home := range []string{"/tmp/other", "~/.handoff/home/a/b", "~/.handoff/home/../x", "~/.handoff/other/x"} {
		if code, body := seg3Req(t, env, http.MethodPost, "/api/logins/check", `{"home":"`+home+`"}`, nil); code != http.StatusBadRequest {
			t.Fatalf("非法 HOME %q 应 400：%d %s", home, code, body)
		}
	}
}

func TestLoginCheckForwardAndNonTargetReject(t *testing.T) {
	remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Handoff-Forwarded") == "" {
			t.Errorf("转发应带 X-Handoff-Forwarded")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"logins":[{"login":"login-aa","account":"codex:abababababab","account_name":"cx","new_account":false,"account_logins":2}]}`)
	}))
	t.Cleanup(remote.Close)
	env, _ := newSeg3Env(t, "self", map[string]config.Target{"remote": {Addr: remote.URL, Token: "t"}})
	code, body := seg3Req(t, env, http.MethodPost, "/api/logins/check?machine=remote", `{"home":"~"}`, nil)
	if code != http.StatusOK || !strings.Contains(body, `"login":"login-aa"`) {
		t.Fatalf("他机检查应原样回目标结果：%d %s", code, body)
	}
	code, body = seg3Req(t, env, http.MethodPost, "/api/logins/check?machine=other", `{"home":"~"}`,
		map[string]string{"X-Handoff-Forwarded": "1"})
	if code != http.StatusConflict || !strings.Contains(body, "非目标机") {
		t.Fatalf("转发到达非目标机应 409：%d %s", code, body)
	}
}

func TestDiscoverFanoutReportsPerMachineAndStaysTwoHundred(t *testing.T) {
	main := t.TempDir()
	withLoginHome(t, main)
	remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Handoff-Forwarded") == "" {
			t.Errorf("扇出应带 X-Handoff-Forwarded")
		}
		at := time.Now().UTC().Format(time.RFC3339)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"machines":[{"machine":"remote","ok":true,"at":"`+at+`"}]}`)
	}))
	t.Cleanup(remote.Close)
	env, acc := newSeg3Env(t, "self", map[string]config.Target{
		"remote":  {Addr: remote.URL, Token: "t"},
		"offline": {Addr: "127.0.0.1:1", Token: "t"},
	})
	reader := &fakeLoginReader{byAbs: map[string]quota.LoginFinding{
		main: {Kind: quota.KindCodex, Source: "auth.json", Fingerprint: "sha256:cdcdcdcdcdcd", Label: "self@example.com"},
	}}
	reg := quota.NewRegistry(map[string]quota.Prober{})
	reg.RegisterLoginReader(quota.KindCodex, reader)
	d := accountwork.NewDiscoverer(accountwork.DiscovererConfig{Accounts: acc, Quota: reg, Machine: "self",
		GOOS: "linux", MainHome: func() (string, error) { return main, nil },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	env.srv.SetAccountDiscoverer(d)

	code, body := seg3Req(t, env, http.MethodPost, "/api/accounts/discover", "", nil)
	if code != http.StatusOK {
		t.Fatalf("立即发现整体应 200：%d %s", code, body)
	}
	var resp proto.AccountDiscoverResp
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("响应不是 AccountDiscoverResp：%v %s", err, body)
	}
	got := map[string]proto.MachineDiscoverResult{}
	for _, m := range resp.Machines {
		got[m.Machine] = m
	}
	if len(got) != 3 || !got["self"].OK || got["self"].At == nil {
		t.Fatalf("本机发现应成功：%+v", resp.Machines)
	}
	if !got["remote"].OK || got["remote"].At == nil {
		t.Fatalf("远端发现应成功：%+v", resp.Machines)
	}
	if got["offline"].OK || got["offline"].Error == "" {
		t.Fatalf("离线机器应单独报错而整体 200：%+v", resp.Machines)
	}

	code, body = seg3Req(t, env, http.MethodPost, "/api/accounts/discover?machine=remote", "", nil)
	var only proto.AccountDiscoverResp
	if code != http.StatusOK || json.Unmarshal([]byte(body), &only) != nil || len(only.Machines) != 1 ||
		only.Machines[0].Machine != "remote" || !only.Machines[0].OK {
		t.Fatalf("?machine= 应只跑指定机：%d %s", code, body)
	}

	// 扇出请求到达本机（带防环头）时不再外扇，只跑本机。
	code, body = seg3Req(t, env, http.MethodPost, "/api/accounts/discover", "",
		map[string]string{"X-Handoff-Forwarded": "1"})
	var loop proto.AccountDiscoverResp
	if code != http.StatusOK || json.Unmarshal([]byte(body), &loop) != nil || len(loop.Machines) != 1 ||
		loop.Machines[0].Machine != "self" {
		t.Fatalf("带防环头应只跑本机：%d %s", code, body)
	}
	code, body = seg3Req(t, env, http.MethodPost, "/api/accounts/discover?machine=other", "",
		map[string]string{"X-Handoff-Forwarded": "1"})
	if code != http.StatusConflict || !strings.Contains(body, "非目标机") {
		t.Fatalf("转发到达非目标机应 409：%d %s", code, body)
	}
}

// TestAccountViewReloginCarriesCommandAndSSH 锁 A4 的 relogin：登录读面带登录命令（HOME 前缀）、
// 远端登录带入口机换算的 ssh 行、本机登录不带；代理/命令里不出现任何凭据值。
func TestAccountViewReloginCarriesCommandAndSSH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ssh/home 断言只对 unix 布局有意义")
	}
	env, acc := newSeg3Env(t, "self", map[string]config.Target{
		"remote": {Addr: "10.0.0.9:7777", Token: "t", User: "dev"},
	})
	home := t.TempDir()
	putOpencodeLogin(t, acc, "self", home)
	res, err := acc.Reconcile(accounts.Discovery{Machine: "remote", At: time.Now(), Homes: []accounts.ScannedHome{{
		Home: "~/.handoff/home/cx", HomeAbs: "/remote/.handoff/home/cx",
		Logins: []accounts.FoundLogin{{Kind: accounts.KindCodex, Source: "auth.json",
			Fingerprint: "sha256:efefefefefef", Label: "cx@example.com"}},
	}}})
	if err != nil || len(res.Logins) != 1 {
		t.Fatalf("建远端登录：%+v %v", res, err)
	}
	code, body := seg3Req(t, env, http.MethodGet, "/api/accounts", "", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/accounts：%d %s", code, body)
	}
	var resp proto.AccountsResp
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	seen := map[string]proto.LoginView{}
	for _, a := range resp.Accounts {
		for _, l := range a.Logins {
			seen[l.Machine] = l
		}
	}
	selfLogin, ok := seen["self"]
	if !ok || selfLogin.Relogin == nil || !strings.Contains(selfLogin.Relogin.Run, "opencode console login") ||
		!strings.Contains(selfLogin.Relogin.Run, "HOME="+home) || selfLogin.Relogin.SSH != "" {
		t.Fatalf("本机登录 relogin 应带 HOME 前缀命令且无 ssh：%+v", selfLogin.Relogin)
	}
	remoteLogin, ok := seen["remote"]
	if !ok || remoteLogin.Relogin == nil || !strings.Contains(remoteLogin.Relogin.Run, "codex login") ||
		remoteLogin.Relogin.SSH != "ssh dev@10.0.0.9" {
		t.Fatalf("远端登录 relogin 应带 ssh 行：%+v", remoteLogin.Relogin)
	}
}
