package agentd

// b453_coord_proxy_test.go —— B453 S4「该载体的任务回合」含协调者回合（段 3 裁决并入段 4）：
// keystone 经 coordinatorRunner 拉起/续接的协调者回合，载体所选账号填了代理时子进程环境的
// 六个代理键被账号代理覆盖（NO_PROXY 不覆盖）；未选账号/未填代理时环境不变。
import (
	"context"
	"strings"
	"testing"

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
		accountProxy: func(cli, home string) string {
			if cli == "opencode" && home == "/h/coord" {
				return proxy
			}
			return ""
		}}
	if _, err := r.Launch(keysclient.SessionSpec{CLI: "opencode", HomeDir: "/h/coord", Workdir: "/w"}, "brief"); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	for _, k := range []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "https_proxy", "http_proxy", "all_proxy"} {
		if v, ok := envValue(rec.launch.Env, k); !ok || v != proxy {
			t.Fatalf("Launch 回合 %s 应为账号代理，得 %q（env=%d 项）", k, v, len(rec.launch.Env))
		}
	}
	if _, err := r.Resume(keysclient.SessionRef{CLI: "opencode", SessionID: "s1", HomeDir: "/h/coord", Workdir: "/w"}, "wake"); err != nil {
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
	r2 := coordinatorRunner{coord: rec2, accountProxy: func(string, string) string { return "" }}
	if _, err := r2.Launch(keysclient.SessionSpec{CLI: "opencode", HomeDir: "/h/other", Workdir: "/w"}, "brief"); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if _, ok := envValue(rec2.launch.Env, "HTTPS_PROXY"); ok {
		t.Fatalf("无账号代理时不得加代理键: %v", rec2.launch.Env)
	}
}

// 门面解析：按（CLI, 展开后的 HOME）找本机协调者载体，取其账号代理；账号不在本机不套用。
func TestB453CoordinatorAccountProxyResolves(t *testing.T) {
	env := newSchedEnv(t)
	acc := wireAccounts(t, env)
	self := env.srv.conf().Name
	if self == "" {
		self = "local"
	}
	putTestAccount(t, acc, "cx-self", self, "http://u:p@127.0.0.1:1080")
	home := t.TempDir()
	putOnlineCarrier(t, env.svc, scheduling.Carrier{Name: "coord-c", Machine: self, CLI: "opencode", HomeDir: home,
		Credential: scheduling.CredentialStandalone, MaxConcurrency: 1, Account: "cx-self"})
	if got := env.srv.coordinatorAccountProxy("opencode", home); got != "http://u:p@127.0.0.1:1080" {
		t.Fatalf("应解析到载体账号代理，得 %q", got)
	}
	if got := env.srv.coordinatorAccountProxy("opencode", t.TempDir()); got != "" {
		t.Fatalf("HOME 不匹配的不得套用，得 %q", got)
	}
	_ = accounts.KindCodex
}
