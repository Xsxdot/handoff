// b439_s6_test.go —— B439 S6：CLI 侧两个入口（card dispatch 的目标解析、拉起器
// 探活）在配了自名的机器上拒收本机旧别名，未配自名保持今天行为（S5）。
package cmd

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

func TestB439S6CLITargetLegacyAliasGate(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("取 hostname: %v", err)
	}
	named := writeTestConfig(t, "name: linux-01\ntoken: t\nlisten: \"127.0.0.1:0\"\n")
	old := configPath
	configPath = named
	t.Cleanup(func() { configPath = old })

	for _, alias := range []string{"local", "本机", host} {
		if _, _, err := resolveCLITarget(alias); err == nil {
			t.Fatalf("--target %q 应被拒", alias)
		} else if !strings.Contains(err.Error(), "linux-01") {
			t.Fatalf("拒收文案应点名自名 linux-01，实得: %v", err)
		}
	}
	if _, isLocal, err := resolveCLITarget(""); err != nil || !isLocal {
		t.Fatalf("省略应视作本机: isLocal=%v err=%v", isLocal, err)
	}
	if _, isLocal, err := resolveCLITarget("linux-01"); err != nil || !isLocal {
		t.Fatalf("自名应视作本机: isLocal=%v err=%v", isLocal, err)
	}

	// 无自名：local 不在 targets 里 → 今天就是「未登记」错误（S5 保持），
	// 不能被误报成本机旧别名。
	plain := writeTestConfig(t, "token: t\nlisten: \"127.0.0.1:0\"\n")
	configPath = plain
	if _, _, err := resolveCLITarget("local"); err == nil || strings.Contains(err.Error(), "旧别名") {
		t.Fatalf("无自名 local 应保持今天「未登记」行为，实得: %v", err)
	}
}

func TestB439S6LauncherProbeLegacyAliasGate(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("取 hostname: %v", err)
	}
	cfg := &config.Config{Name: "linux-01", Listen: "127.0.0.1:0", Token: "t"}
	for _, alias := range []string{"local", "本机", host} {
		reach, warn := probeLauncherMachine(context.Background(), cfg, host, alias)
		if reach != "不可探（本机旧别名）" {
			t.Fatalf("--machine %q 应拒探，实得 reach=%q warn=%q", alias, reach, warn)
		}
		if !strings.Contains(warn, "linux-01") {
			t.Fatalf("告警应点名自名 linux-01，实得: %q", warn)
		}
	}

	// 无自名保持今天行为：local 照探本机 + 告警「本机别名」。
	plainPath := writeTestConfig(t, "token: t\nlisten: \"127.0.0.1:0\"\n")
	old := configPath
	configPath = plainPath
	t.Cleanup(func() { configPath = old })
	plain := &config.Config{Listen: "127.0.0.1:0", Token: "t"}
	reach, warn := probeLauncherMachine(context.Background(), plain, host, "local")
	if reach == "不可探（本机旧别名）" {
		t.Fatal("无自名不应按旧别名拒探")
	}
	if !strings.Contains(warn, "本机别名") {
		t.Fatalf("无自名应保持今天告警，实得 reach=%q warn=%q", reach, warn)
	}
}
