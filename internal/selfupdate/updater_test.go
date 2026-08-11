// 更新循环的行为测试。全部经注入的假 Checker/Fetcher/闭包驱动，
// 不联网、不动真实二进制、不真的关停任何东西。
package selfupdate

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/release"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// recordLog 返回一个把输出记进 buffer 的 logger，便于断言"只 Warn 一次"。
func recordLog(buf *strings.Builder) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

type fakeChecker struct {
	rel  release.Release
	err  error
	hits int
}

func (f *fakeChecker) Latest(context.Context) (release.Release, error) {
	f.hits++
	return f.rel, f.err
}

type fakeFetcher struct {
	path string
	err  error
	hits int
}

func (f *fakeFetcher) Fetch(context.Context, release.Release, string) (string, error) {
	f.hits++
	return f.path, f.err
}

// baseOpts 造一组「一切正常、托管、空闲」的默认参数。
func baseOpts(t *testing.T, cur string, rel release.Release) (Options, *fakeChecker, *fakeFetcher, *[]string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "handoff")
	if err := os.WriteFile(bin, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	ck := &fakeChecker{rel: rel}
	ft := &fakeFetcher{path: filepath.Join(dir, ".handoff.new-"+rel.Tag)}
	reasons := []string{}
	opts := Options{
		DataDir:        dir,
		CurrentVersion: cur,
		Interval:       time.Hour,
		BusyCount:      func() (int, error) { return 0, nil },
		Shutdown:       func(r string) bool { reasons = append(reasons, r); return true },
		Checker:        ck,
		Fetcher:        ft,
		Getenv: func(k string) string {
			if k == "INVOCATION_ID" {
				return "unit-1"
			}
			return ""
		},
		Executable: func() (string, error) { return bin, nil },
		Activate:   func(newPath, target string) (string, error) { return target + ".prev", nil },
		Log:        quietLog(),
	}
	return opts, ck, ft, &reasons
}

func relV(tag string) release.Release { return release.Release{Tag: tag} }

// 已是最新：不下载、不关停。
func TestTickAlreadyLatest(t *testing.T) {
	opts, _, ft, reasons := baseOpts(t, "v0.2.0", relV("v0.2.0"))
	New(opts).Tick(context.Background())
	if ft.hits != 0 {
		t.Error("已是最新不该下载")
	}
	if len(*reasons) != 0 {
		t.Error("已是最新不该触发关停")
	}
}

// 有新版 + 托管 + 空闲：下载 → 换版 → 触发关停，关停原因带上新版本号。
func TestTickUpgradesWhenIdle(t *testing.T) {
	opts, _, ft, reasons := baseOpts(t, "v0.1.0", relV("v0.2.0"))
	New(opts).Tick(context.Background())
	if ft.hits != 1 {
		t.Fatalf("应下载一次，实际 %d 次", ft.hits)
	}
	if len(*reasons) != 1 || !strings.Contains((*reasons)[0], "v0.2.0") {
		t.Fatalf("应触发一次带新版本号的关停，得到 %v", *reasons)
	}
	// 换完版 pending 应清掉——留着会让新进程以为还有待命更新
	if p, _ := LoadPending(opts.DataDir); p != nil {
		t.Fatalf("换版后应清掉 pending，还剩 %+v", p)
	}
}

// 有活跃任务：下载待命，但**不换版、不关停**。这是 D3 的核心。
func TestTickWaitsWhenBusy(t *testing.T) {
	opts, _, ft, reasons := baseOpts(t, "v0.1.0", relV("v0.2.0"))
	opts.BusyCount = func() (int, error) { return 2, nil }
	New(opts).Tick(context.Background())
	if ft.hits != 1 {
		t.Error("忙时也应先把新版下好待命")
	}
	if len(*reasons) != 0 {
		t.Fatalf("有活跃任务时绝不能关停，却触发了 %v", *reasons)
	}
	p, err := LoadPending(opts.DataDir)
	if err != nil || p == nil || p.Version != "v0.2.0" {
		t.Fatalf("应把待命版本持久化，得到 %+v err=%v", p, err)
	}
}

// 已有 pending 时不重复下载，直接判空闲。
func TestTickReusesPending(t *testing.T) {
	opts, ck, ft, reasons := baseOpts(t, "v0.1.0", relV("v0.2.0"))
	if err := SavePending(opts.DataDir, &Pending{Version: "v0.2.0", Path: ft.path}); err != nil {
		t.Fatal(err)
	}
	New(opts).Tick(context.Background())
	if ft.hits != 0 {
		t.Error("已有 pending 不该重复下载")
	}
	if ck.hits != 0 {
		t.Error("已有 pending 不该再查版本（省一次限流额度）")
	}
	if len(*reasons) != 1 {
		t.Fatalf("空闲时应换版，得到 %v", *reasons)
	}
}

// 非托管：下载待命，但坚决不换版。这是最重要的一条防线。
//
// why：非托管进程换完版 exit(0) 之后没人拉起，机器上就此没有 handoff 在跑，
// 而且没有任何信号告诉任何人。
func TestTickRefusesWhenUnmanaged(t *testing.T) {
	opts, _, ft, reasons := baseOpts(t, "v0.1.0", relV("v0.2.0"))
	opts.Getenv = func(string) string { return "" } // 裸进程
	var buf strings.Builder
	opts.Log = recordLog(&buf)
	New(opts).Tick(context.Background())
	if ft.hits != 1 {
		t.Error("非托管也应把新版下好待命，供人工 handoff upgrade --now 使用")
	}
	if len(*reasons) != 0 {
		t.Fatalf("非托管时绝不能自动换版，却触发了 %v", *reasons)
	}
	if !strings.Contains(buf.String(), "非托管") {
		t.Errorf("应 Warn 说明非托管，日志:\n%s", buf.String())
	}
}

// 本进程版本未知（非 release 构建）：跳过自动更新，且**只 Warn 一次**。
//
// why 只一次：6 小时一轮的循环在开发机上会跑很久，每轮刷一条同样的 Warn
// 会把日志淹掉，真正的问题反而看不见。
func TestTickWarnsOnceWhenVersionUnknown(t *testing.T) {
	opts, ck, _, _ := baseOpts(t, "", relV("v0.2.0"))
	var buf strings.Builder
	opts.Log = recordLog(&buf)
	u := New(opts)
	u.Tick(context.Background())
	u.Tick(context.Background())
	u.Tick(context.Background())
	if ck.hits != 0 {
		t.Error("版本未知时不该查版本")
	}
	if n := strings.Count(buf.String(), "版本未知"); n != 1 {
		t.Fatalf("应只 Warn 一次，实际 %d 次:\n%s", n, buf.String())
	}
}

// 查版本失败：Warn 一行就结束，不重试不退避（interval 本身就是退避）。
func TestTickCheckFailureIsNotRetried(t *testing.T) {
	opts, ck, ft, _ := baseOpts(t, "v0.1.0", relV("v0.2.0"))
	ck.err = errors.New("查最新版本返回 403: API rate limit exceeded")
	New(opts).Tick(context.Background())
	if ck.hits != 1 {
		t.Fatalf("应只查一次，实际 %d 次", ck.hits)
	}
	if ft.hits != 0 {
		t.Error("查版本失败不该下载")
	}
}

// 同一个 tag 下载失败过，后续轮次不再重试它。
//
// why：自检失败通常是这个 release 的资产本身有问题（架构拿错、构建坏了），
// 重试只会每轮白下一次 20MB，而且永远不会成功。
func TestTickDoesNotRetryFailedTag(t *testing.T) {
	opts, ck, ft, _ := baseOpts(t, "v0.1.0", relV("v0.2.0"))
	ft.err = errors.New("新二进制自检失败：version 首行为 \"unknown\"")
	u := New(opts)
	u.Tick(context.Background())
	u.Tick(context.Background())
	u.Tick(context.Background())
	if ft.hits != 1 {
		t.Fatalf("同 tag 只应尝试一次，实际 %d 次", ft.hits)
	}
	if ck.hits != 3 {
		t.Errorf("查版本仍应每轮进行（新 tag 出来要能发现），实际 %d 次", ck.hits)
	}
}

// 空闲判定本身出错时按「忙」处理——判不出来就不要换版（fail-closed）。
func TestTickTreatsBusyErrorAsBusy(t *testing.T) {
	opts, _, _, reasons := baseOpts(t, "v0.1.0", relV("v0.2.0"))
	opts.BusyCount = func() (int, error) { return 0, errors.New("db closed") }
	New(opts).Tick(context.Background())
	if len(*reasons) != 0 {
		t.Fatalf("空闲判定失败时不该换版（fail-closed），却触发了 %v", *reasons)
	}
}

// Reconcile：pending 的版本就是自己 → 说明换版成功，清掉并 Info。
func TestReconcileClearsAfterSuccessfulUpdate(t *testing.T) {
	opts, _, _, _ := baseOpts(t, "v0.2.0", relV("v0.2.0"))
	if err := SavePending(opts.DataDir, &Pending{Version: "v0.2.0", Path: "/x"}); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	opts.Log = recordLog(&buf)
	New(opts).Reconcile()
	if p, _ := LoadPending(opts.DataDir); p != nil {
		t.Fatalf("换版成功后应清掉 pending，还剩 %+v", p)
	}
	if !strings.Contains(buf.String(), "更新完成") {
		t.Errorf("应 Info 一条更新完成，日志:\n%s", buf.String())
	}
}

// Reconcile：pending 的版本不是自己 → 保留（还在等窗口）。
func TestReconcileKeepsUnappliedPending(t *testing.T) {
	opts, _, _, _ := baseOpts(t, "v0.1.0", relV("v0.2.0"))
	if err := SavePending(opts.DataDir, &Pending{Version: "v0.2.0", Path: "/x"}); err != nil {
		t.Fatal(err)
	}
	New(opts).Reconcile()
	if p, _ := LoadPending(opts.DataDir); p == nil {
		t.Fatal("尚未生效的 pending 不该被清掉")
	}
}

// Run 在 ctx 取消后必须返回，不能挂住让进程关不掉。
func TestRunStopsOnContextCancel(t *testing.T) {
	opts, _, _, _ := baseOpts(t, "v0.2.0", relV("v0.2.0"))
	opts.Interval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); New(opts).Run(ctx) }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run 未在 ctx 取消后 3s 内返回")
	}
}
