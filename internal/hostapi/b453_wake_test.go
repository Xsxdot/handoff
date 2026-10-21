// b453_wake_test.go —— B453 段 3 检测链：撞额度三类分类委托 quota（Q10/Q19）、检测回合
// 算载体 HOME 凭据指纹（F15）、账号代理覆盖检测回合环境（Q6）。
//
// 边界：detectTurn 换成记录型替身，不起真实 CLI；指纹函数由测试注入（真实取材在 quota 测）。
package hostapi

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestClassifyTurnErrorThreeWayDelegatesToQuota(t *testing.T) {
	cases := []struct {
		name, kind, text string
		want             WakeOutcome
	}{
		{"真额度耗尽 → quota", "codex", `usage_limit_reached: You've hit your usage limit`, WakeQuota},
		{"429 限速 → 视为可达", "", `HTTP 429 Too Many Requests`, WakeReady},
		{"rate limit 字样 → 视为可达", "", `rate limit reached, retry later`, WakeReady},
		{"单独 quota 字样不确定 → 不当额度", "", `quota check failed: connection refused`, WakeUnreachable},
		{"登录类不动", "", `401 unauthorized, please login`, WakeNeedLogin},
		{"未知 → unreachable", "", `boom`, WakeUnreachable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyTurnError("opencode", tc.kind, errors.New(tc.text))
			if got.Outcome != tc.want {
				t.Fatalf("classifyTurnError(%q) = %q，want %q", tc.text, got.Outcome, tc.want)
			}
			if tc.want == WakeReady && !strings.Contains(got.Detail, "限速") {
				t.Fatalf("限速视为可达时 Detail 要说明：%q", got.Detail)
			}
		})
	}
}

// captureDetectTurn 把检测回合换成记录 env 的替身，返回固定输出。
func captureDetectTurn(t *testing.T, env *[]string, err error) {
	t.Helper()
	old := detectTurn
	detectTurn = func(_ *Host, _ context.Context, req TurnRequest) (TurnReply, error) {
		*env = append([]string(nil), req.Env...)
		if err != nil {
			return TurnReply{}, err
		}
		return TurnReply{Output: "ok", SessionID: "s1"}, nil
	}
	t.Cleanup(func() { detectTurn = old })
}

func TestWakeHomeFingerprintAndAccountProxy(t *testing.T) {
	swapUserHomeDir(t, t.TempDir())
	var env []string
	captureDetectTurn(t, &env, errors.New("insufficient_quota"))
	host := newProbeHost()
	var gotKind, gotHome, gotEntry string
	host.SetCredentialFingerprint(func(kind, home, entry string) (string, error) {
		gotKind, gotHome, gotEntry = kind, home, entry
		return "sha256:abcdefabcdef", nil
	})
	home := t.TempDir()
	reply, err := host.WakeHome(context.Background(), WakeRequest{
		CLI: "opencode", HomeDir: home, Timeout: testWakeBudget,
		AccountKind: "codex", AccountEntry: "tokens", Proxy: "socks5://127.0.0.1:7897",
	})
	if err != nil {
		t.Fatalf("WakeHome: %v", err)
	}
	if reply.Fingerprint != "sha256:abcdefabcdef" || gotKind != "codex" || gotEntry != "tokens" || gotHome != home {
		t.Fatalf("指纹 = %q（kind=%q home=%q entry=%q），want 按所选账号 kind/条目在载体 HOME 里算", reply.Fingerprint, gotKind, gotHome, gotEntry)
	}
	if reply.Outcome != WakeQuota {
		t.Fatalf("codex 账号 insufficient_quota 应判撞额度，得 %q", reply.Outcome)
	}
	if !slices.Contains(env, "HTTPS_PROXY=socks5://127.0.0.1:7897") || !slices.Contains(env, "https_proxy=socks5://127.0.0.1:7897") {
		t.Fatalf("检测回合环境应被账号代理覆盖：%v", env)
	}
}

func TestWakeHomeFingerprintFailureMeansNoCompare(t *testing.T) {
	swapUserHomeDir(t, t.TempDir())
	var env []string
	captureDetectTurn(t, &env, nil)
	host := newProbeHost()
	host.SetCredentialFingerprint(func(string, string, string) (string, error) {
		return "", errors.New("凭据文件缺失")
	})
	reply, err := host.WakeHome(context.Background(), WakeRequest{
		CLI: "opencode", HomeDir: t.TempDir(), Timeout: testWakeBudget, AccountKind: "codex",
	})
	if err != nil || reply.Outcome != WakeReady || reply.Fingerprint != "" {
		t.Fatalf("指纹算不出 = %+v/%v，want ready 且指纹空（不比对，不影响检测结局）", reply, err)
	}
}

func TestWakeHomeWithoutAccountSkipsFingerprintAndKeepsEnv(t *testing.T) {
	swapUserHomeDir(t, t.TempDir())
	var env []string
	captureDetectTurn(t, &env, nil)
	host := newProbeHost()
	host.SetCredentialFingerprint(func(string, string, string) (string, error) {
		t.Error("未选账号不得算指纹")
		return "", nil
	})
	if _, err := host.WakeHome(context.Background(), WakeRequest{CLI: "opencode", HomeDir: t.TempDir(), Timeout: testWakeBudget}); err != nil {
		t.Fatalf("WakeHome: %v", err)
	}
	if want := wakeProxyEnv("opencode"); !slices.Equal(env, want) {
		t.Fatalf("未选账号时检测环境必须与改动前一致：%v，want %v", env, want)
	}
}
