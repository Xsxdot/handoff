package agentd

// b453_coord_proxy_test.go —— B453 S4「该载体的任务回合」含协调者回合（段 3 裁决并入段 4）：
// keystone 经 coordinatorRunner 拉起/续接的协调者回合，载体所选账号填了代理时子进程环境的
// 六个代理键被账号代理覆盖（NO_PROXY 不覆盖）；未选账号/未填代理时环境不变。
import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

type b453RecCoord struct {
	launch executor.CoordSessionSpec
	resume executor.CoordSessionRef
}

func (c *b453RecCoord) Launch(_ context.Context, spec executor.CoordSessionSpec, _ string) (executor.CoordTurnResult, error) {
	c.launch = spec
	return executor.CoordTurnResult{SessionID: "s1", Output: "ok"}, nil
}
func (c *b453RecCoord) Resume(_ context.Context, ref executor.CoordSessionRef, _ string) (executor.CoordTurnResult, error) {
	c.resume = ref
	return executor.CoordTurnResult{SessionID: ref.SessionID, Output: "ok"}, nil
}
func (c *b453RecCoord) CancelTurn(context.Context, executor.CoordSessionRef) error { return nil }

func envValue(env []string, key string) (string, bool) {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v, true
		}
	}
	return "", false
}

func TestB453CoordinatorRoundsUseAccountProxy(t *testing.T) {
	const proxy = "http://b453u:b453p@127.0.0.1:1080"
	rec := &b453RecCoord{}
	r := coordinatorRunner{coord: rec,
		isPushResidentSession: func(string) (bool, error) { return false, nil },
		accountProxy: func(carrier, cli, home string) string {
			if carrier == "coord-c" && cli == "opencode" && home == "/h/coord" {
				return proxy
			}
			return ""
		}}
	if _, err := r.Launch(keysclient.SessionSpec{CLI: "opencode", HomeDir: "/h/coord", Workdir: "/w", Carrier: "coord-c"}, "brief"); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	for _, k := range []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "https_proxy", "http_proxy", "all_proxy"} {
		if v, ok := envValue(rec.launch.Env, k); !ok || v != proxy {
			t.Fatalf("Launch 回合 %s 应为账号代理，得 %q（env=%d 项）", k, v, len(rec.launch.Env))
		}
	}
	if _, err := r.Resume(keysclient.SessionRef{CLI: "opencode", SessionID: "s1", HomeDir: "/h/coord", Workdir: "/w", Carrier: "coord-c"}, "wake"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if v, _ := envValue(rec.resume.Env, "HTTPS_PROXY"); v != proxy {
		t.Fatalf("Resume 回合应套账号代理，得 %q", v)
	}
	if v, _ := envValue(rec.resume.Env, "HANDOFF_SESSION_ID"); v != "s1" {
		t.Fatalf("Resume 回合席位对不得被代理覆盖丢掉，得 %q", v)
	}
	// 未选账号 / 未填代理：环境不加代理键
	rec2 := &b453RecCoord{}
	r2 := coordinatorRunner{coord: rec2, accountProxy: func(string, string, string) string { return "" }}
	if _, err := r2.Launch(keysclient.SessionSpec{CLI: "opencode", HomeDir: "/h/other", Workdir: "/w"}, "brief"); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if _, ok := envValue(rec2.launch.Env, "HTTPS_PROXY"); ok {
		t.Fatalf("无账号代理时不得加代理键: %v", rec2.launch.Env)
	}
}

// 门面解析：按（CLI, 展开后的 HOME）找本机协调者载体，取其所选登录的代理；登录不在本机不套用。
func TestB453CoordinatorAccountProxyResolves(t *testing.T) {
	env := newSchedEnv(t)
	acc := wireAccounts(t, env)
	self := env.srv.conf().Name
	if self == "" {
		self = "local"
	}
	home := t.TempDir()
	// 登录 HOME 就是协调者载体的 HOME（载体 home_dir 由服务端镜像为登录 home）。
	login := putOpencodeLogin(t, acc, self, home)
	setLoginProxy(t, env.srv.SchedulingRegistry(), login, "http://u:p@127.0.0.1:1080")
	putOnlineCarrier(t, env.svc, scheduling.Carrier{Name: "coord-c", Machine: self, CLI: "opencode",
		MaxConcurrency: 1, Login: login})
	if got := env.srv.coordinatorAccountProxy("coord-c", "opencode", home); got != "http://u:p@127.0.0.1:1080" {
		t.Fatalf("应按载体名解析到账号代理，得 %q", got)
	}
	if got := env.srv.coordinatorAccountProxy("", "opencode", home); got != "http://u:p@127.0.0.1:1080" {
		t.Fatalf("旧引用按 CLI+HOME 反查应解析到账号代理，得 %q", got)
	}
	if got := env.srv.coordinatorAccountProxy("coord-c", "opencode", t.TempDir()); got != "http://u:p@127.0.0.1:1080" {
		t.Fatalf("有载体名时 HOME 不匹配也应取该载体登录，得 %q", got)
	}
	if got := env.srv.coordinatorAccountProxy("", "opencode", t.TempDir()); got != "" {
		t.Fatalf("旧引用 HOME 不匹配的不得套用，得 %q", got)
	}
	_ = accounts.KindCodex
}

// TestB464CoordinatorProxyByCarrierDisambiguatesSameHome 锁 I2：同一 HOME 两条登录、两个载体
// 各选其一且代理不同——协调者回合按席位载体取对应登录的代理，不再靠（CLI, HOME）反查。
func TestB464CoordinatorProxyByCarrierDisambiguatesSameHome(t *testing.T) {
	env := newSchedEnv(t)
	acc := wireAccounts(t, env)
	self := env.srv.conf().Name
	if self == "" {
		self = "local"
	}
	home := "~/.handoff/home/shared"
	res, err := acc.Reconcile(accounts.Discovery{Machine: self, At: time.Now(), Homes: []accounts.ScannedHome{{
		Home: home, HomeAbs: "/home/shared", Logins: []accounts.FoundLogin{
			{Kind: accounts.KindOpencodeGo, Source: "auth.json", Fingerprint: "sha256:aaaa11112222"},
			{Kind: accounts.KindOpencodeGo, Source: "opencode.db", Fingerprint: "sha256:bbbb33334444"},
		},
	}}})
	if err != nil || len(res.Logins) != 2 {
		t.Fatalf("同 HOME 应建两条登录：%+v %v", res, err)
	}
	const proxyA, proxyB = "http://u:p@127.0.0.1:1081", "http://u:p@127.0.0.1:1082"
	setLoginProxy(t, env.srv.SchedulingRegistry(), res.Logins[0].ID, proxyA)
	setLoginProxy(t, env.srv.SchedulingRegistry(), res.Logins[1].ID, proxyB)
	for _, c := range []struct{ name, login string }{{"coord-a", res.Logins[0].ID}, {"coord-b", res.Logins[1].ID}} {
		putOnlineCarrier(t, env.svc, scheduling.Carrier{Name: c.name, Machine: self, CLI: "opencode",
			MaxConcurrency: 1, Login: c.login})
	}
	for _, tc := range []struct{ carrier, want string }{{"coord-a", proxyA}, {"coord-b", proxyB}} {
		if got := env.srv.coordinatorAccountProxy(tc.carrier, "opencode", ""); got != tc.want {
			t.Fatalf("载体 %s 的协调者回合应取所选登录代理 %q，得 %q", tc.carrier, tc.want, got)
		}
	}
}
