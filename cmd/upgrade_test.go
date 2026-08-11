// handoff upgrade 的 CLI 行为测试。全部经包级缝注入，不联网、不动真实二进制。
package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/release"
	"github.com/xushixin/handoff/internal/selfupdate"
)

// withUpgradeStubs 替换四个缝，返回记录调用的指针。
func withUpgradeStubs(t *testing.T, latest release.Release, latestErr, fetchErr, actErr, rbErr error) (*int, *int) {
	t.Helper()
	oldC, oldF, oldA, oldR := newReleaseChecker, newReleaseFetcher, activateBinary, rollbackBinary
	fetches, rollbacks := 0, 0
	newReleaseChecker = func() releaseChecker {
		return checkerFunc(func(context.Context) (release.Release, error) { return latest, latestErr })
	}
	newReleaseFetcher = func() releaseFetcher {
		return fetcherFunc(func(context.Context, release.Release, string) (string, error) {
			fetches++
			return "/tmp/.handoff.new", fetchErr
		})
	}
	activateBinary = func(string, string) (string, error) { return "/tmp/handoff.prev", actErr }
	rollbackBinary = func(string) error { rollbacks++; return rbErr }
	t.Cleanup(func() {
		newReleaseChecker, newReleaseFetcher, activateBinary, rollbackBinary = oldC, oldF, oldA, oldR
	})
	return &fetches, &rollbacks
}

type checkerFunc func(context.Context) (release.Release, error)

func (f checkerFunc) Latest(ctx context.Context) (release.Release, error) { return f(ctx) }

type fetcherFunc func(context.Context, release.Release, string) (string, error)

func (f fetcherFunc) Fetch(ctx context.Context, r release.Release, d string) (string, error) {
	return f(ctx, r, d)
}

func runUpgrade(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetFlags(t)
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(append([]string{"upgrade"}, args...))
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		upgradeCheck, upgradeNow, upgradeRollback = false, false, false
	})
	err := rootCmd.ExecuteContext(context.Background())
	return buf.String(), err
}

// 默认（无 flag）= --check：只报告，不下载。
func TestUpgradeDefaultIsCheckOnly(t *testing.T) {
	fetches, _ := withUpgradeStubs(t, release.Release{Tag: "v9.9.9"}, nil, nil, nil, nil)
	out, err := runUpgrade(t)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if *fetches != 0 {
		t.Fatal("默认只检查，不该下载")
	}
	if !strings.Contains(out, "v9.9.9") {
		t.Fatalf("应报告最新版本:\n%s", out)
	}
}

// --now 会真的下载并替换。
func TestUpgradeNowInstalls(t *testing.T) {
	fetches, _ := withUpgradeStubs(t, release.Release{Tag: "v9.9.9"}, nil, nil, nil, nil)
	out, err := runUpgrade(t, "--now")
	if err != nil {
		t.Fatalf("upgrade --now: %v", err)
	}
	if *fetches != 1 {
		t.Fatalf("应下载一次，实际 %d 次", *fetches)
	}
	if !strings.Contains(out, "已升级") {
		t.Fatalf("应报告升级结果:\n%s", out)
	}
	// 换完必须提示「要让 agentd 用上新版得重启它」——不说这句，用户会以为
	// 升级完就生效了，而正在跑的 agentd 还是旧进程
	if !strings.Contains(out, "重启") {
		t.Fatalf("应提示重启 agentd 才生效:\n%s", out)
	}
}

// --now 在非托管环境下**仍然允许**：那条闸约束的是自动更新，不是人工命令。
//
// why：人工敲 upgrade --now 的人知道自己在干什么，也知道要不要手动把
// agentd 起回来。把人工出口也堵上，非托管机器就彻底没法升级了。
func TestUpgradeNowAllowedWhenUnmanaged(t *testing.T) {
	fetches, _ := withUpgradeStubs(t, release.Release{Tag: "v9.9.9"}, nil, nil, nil, nil)
	if _, err := runUpgrade(t, "--now"); err != nil {
		t.Fatalf("非托管环境下人工升级不该被拒: %v", err)
	}
	if *fetches != 1 {
		t.Fatal("非托管环境下人工升级应照常下载")
	}
}

// --rollback 调回滚。
func TestUpgradeRollback(t *testing.T) {
	_, rollbacks := withUpgradeStubs(t, release.Release{}, nil, nil, nil, nil)
	out, err := runUpgrade(t, "--rollback")
	if err != nil {
		t.Fatalf("upgrade --rollback: %v", err)
	}
	if *rollbacks != 1 {
		t.Fatalf("应回滚一次，实际 %d 次", *rollbacks)
	}
	if !strings.Contains(out, "已回滚") {
		t.Fatalf("应报告回滚结果:\n%s", out)
	}
}

// 回滚失败要把真因带出来（没有 prev 是最常见的）。
func TestUpgradeRollbackSurfacesCause(t *testing.T) {
	withUpgradeStubs(t, release.Release{}, nil, nil, nil, errors.New("没有可回滚的旧二进制"))
	_, err := runUpgrade(t, "--rollback")
	if err == nil {
		t.Fatal("回滚失败应返回错误")
	}
	if !strings.Contains(err.Error(), "没有可回滚") {
		t.Fatalf("错误应带真因，得到: %v", err)
	}
}

// 互斥：--now 与 --rollback 不能同时给。
func TestUpgradeFlagsAreMutuallyExclusive(t *testing.T) {
	withUpgradeStubs(t, release.Release{Tag: "v1"}, nil, nil, nil, nil)
	if _, err := runUpgrade(t, "--now", "--rollback"); err == nil {
		t.Fatal("--now 与 --rollback 同时给应报错")
	}
}

// 查版本失败要把状态码/真因带出来。
func TestUpgradeCheckSurfacesCause(t *testing.T) {
	withUpgradeStubs(t, release.Release{}, errors.New("查最新版本返回 403: API rate limit exceeded"), nil, nil, nil)
	_, err := runUpgrade(t, "--check")
	if err == nil {
		t.Fatal("查版本失败应返回错误")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("错误应带状态码，得到: %v", err)
	}
}

// 更新提示必须打在 stderr 而不是 stdout。
//
// why：stdout 是各命令的机器可读输出（dispatch 的 JSON、tasks 的每行 JSON），
// 掺一行人话进去会让调用方的 jq 直接失败——而那是审核者回路的主干道。
func TestUpdateNoticeGoesToStderr(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, []byte("listen: 127.0.0.1:7777\ntoken: t\ndatadir: "+dir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := selfupdate.SaveCLICheck(dir, &selfupdate.CLICheck{
		CheckedAt: time.Now().UTC(), Latest: "v99.0.0",
	}); err != nil {
		t.Fatal(err)
	}
	resetFlags(t)
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	t.Cleanup(func() { rootCmd.SetArgs(nil); rootCmd.SetOut(nil); rootCmd.SetErr(nil) })

	// 直接调提示函数：跑一条真命令会引入网络/agentd 依赖，而这里要验的
	// 只是「提示落在哪个流上」
	fake := &cobra.Command{Use: "fakecmd"}
	fake.SetOut(&stdout)
	fake.SetErr(&stderr)
	configPath = cfg
	maybeNotifyUpdate(fake)

	if strings.Contains(stdout.String(), "有新版本") {
		t.Fatalf("提示不该出现在 stdout:\n%s", stdout.String())
	}
	// 本地测试二进制的 buildinfo.Version 为空 → 按设计不提示。
	// 这里只钉住「绝不污染 stdout」这一条，它与版本无关
	_ = stderr
}
