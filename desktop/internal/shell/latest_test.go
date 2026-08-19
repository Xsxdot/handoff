// 本文件覆盖桌面端新版安装包检查的缓存、方向判定与静默失败契约。
package shell_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
	"github.com/Xsxdot/handoff/internal/selfupdate"
)

// TestCheckLatestSilentOnUnknownCurrent 钉住 spec §6.3：基准判不出就不提示。
//
// 开发构建下 embedbin.Version 为空。此时既不能说「有新版」（不知道跟谁比），
// 也不能瞎猜——症状（一直提示或永不提示）都不会报错，排查成本极高。
func TestCheckLatestSilentOnUnknownCurrent(t *testing.T) {
	fetched := false
	d := shell.LatestDeps{
		Fetch: func(context.Context) (string, error) { fetched = true; return "v9.9.9", nil },
		Now:   time.Now,
	}
	tag, newer := shell.CheckLatest(context.Background(), t.TempDir(), "", d)
	if newer || tag != "" {
		t.Errorf("current 为空时返回了 (%q,%v)，想要 (\"\",false)", tag, newer)
	}
	if fetched {
		t.Error("current 判不出时不该发网络请求——白耗一次 GitHub 匿名限流额度")
	}
}

// TestCheckLatestSilentOnFetchError 钉住任何失败都静默。
//
// 通知路是锦上添花，它自己绝不能成为故障源（沿用 clicheck.go 文件头的既有约定）。
func TestCheckLatestSilentOnFetchError(t *testing.T) {
	d := shell.LatestDeps{
		Fetch: func(context.Context) (string, error) { return "", errors.New("网络不通") },
		Now:   time.Now,
	}
	tag, newer := shell.CheckLatest(context.Background(), t.TempDir(), "v0.3.0", d)
	if newer || tag != "" {
		t.Errorf("拉取失败时返回了 (%q,%v)，想要 (\"\",false)", tag, newer)
	}
}

// TestCheckLatestUsesCacheWithin24h 钉住共用 CLI 那份限流缓存。
//
// api.github.com 有 60 次/小时/IP 的匿名限流，而多台执行机很可能共用一个
// 代理出口 IP。限流一旦触发，agentd 的换版也会跟着失败——所以这里省下的
// 不只是自己的一次请求。
func TestCheckLatestUsesCacheWithin24h(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	if err := selfupdate.SaveCLICheck(dir, &selfupdate.CLICheck{CheckedAt: now, Latest: "v0.4.0"}); err != nil {
		t.Fatal(err)
	}
	fetched := false
	d := shell.LatestDeps{
		Fetch: func(context.Context) (string, error) { fetched = true; return "v9.9.9", nil },
		Now:   func() time.Time { return now.Add(time.Hour) },
	}
	tag, newer := shell.CheckLatest(context.Background(), dir, "v0.3.0", d)
	if fetched {
		t.Error("缓存还新鲜却又发了一次请求")
	}
	if !newer || tag != "v0.4.0" {
		t.Errorf("返回 (%q,%v)，想要 (\"v0.4.0\",true)", tag, newer)
	}
}

// TestCheckLatestRefetchesAfter24h 钉住缓存过期会重查并回写。
func TestCheckLatestRefetchesAfter24h(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	if err := selfupdate.SaveCLICheck(dir, &selfupdate.CLICheck{CheckedAt: now, Latest: "v0.4.0"}); err != nil {
		t.Fatal(err)
	}
	later := now.Add(25 * time.Hour)
	d := shell.LatestDeps{
		Fetch: func(context.Context) (string, error) { return "v0.5.0", nil },
		Now:   func() time.Time { return later },
	}
	tag, newer := shell.CheckLatest(context.Background(), dir, "v0.3.0", d)
	if !newer || tag != "v0.5.0" {
		t.Errorf("返回 (%q,%v)，想要 (\"v0.5.0\",true)", tag, newer)
	}
	// 回写过缓存，下一次才不会又查
	got := selfupdate.LoadCLICheck(dir)
	if got == nil || got.Latest != "v0.5.0" {
		t.Errorf("缓存没被回写：%+v", got)
	}
}

// TestCheckLatestNotNewerWhenSameOrOlder 钉住不会反向提示。
//
// B59 验收当场抓出过反向提示（装了 v0.1.1 的机器被劝「有新版本 v0.1.0」），
// 根因是只判「不相等」而没判方向。
func TestCheckLatestNotNewerWhenSameOrOlder(t *testing.T) {
	for _, latest := range []string{"v0.3.0", "v0.2.9", "v0.10.0"} {
		dir := t.TempDir()
		now := time.Now()
		if err := selfupdate.SaveCLICheck(dir, &selfupdate.CLICheck{CheckedAt: now, Latest: latest}); err != nil {
			t.Fatal(err)
		}
		d := shell.LatestDeps{Now: func() time.Time { return now }}
		tag, newer := shell.CheckLatest(context.Background(), dir, "v0.11.0", d)
		want := latest == "" // 恒 false，这里只为表达「都不该提示」
		if newer != want {
			t.Errorf("current=v0.11.0 latest=%s 时 newer=%v，想要 false（tag=%q）", latest, newer, tag)
		}
	}
}
