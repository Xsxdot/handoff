// 本文件覆盖打开控制台前的同步编排：承重调用顺序、D8 失败不阻断与繁忙闸门。
//
// 边界：只替换 OpenSyncDeps 的外部动作，不连接真实 agentd、文件系统或窗口。
package shell_test

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
)

// okDeps 造一份「该同步且一切顺利」的依赖，并把调用顺序记进 seq。
func okDeps(seq *[]string) shell.OpenSyncDeps {
	return shell.OpenSyncDeps{
		EnsureRunning:    func() error { *seq = append(*seq, "ensure"); return nil },
		InstalledPath:    func() (string, error) { return "/tmp/handoff", nil },
		InstalledVersion: func(string) string { return "v0.3.0" },
		Busy: func(context.Context) (int, error) {
			*seq = append(*seq, "busy")
			return 0, nil
		},
		EmbedVersion:   "v0.4.0",
		EmbedAvailable: true,
		Sync: shell.SyncDeps{
			OpenEmbedded: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("B")), nil },
			Activate: func(_, target string) (string, error) {
				*seq = append(*seq, "activate")
				return target + ".prev", nil
			},
			SkillInstall: func(context.Context, string) ([]byte, error) {
				*seq = append(*seq, "skill")
				return nil, nil
			},
			RestartAgentd: func(context.Context, bool) error {
				*seq = append(*seq, "restart")
				return nil
			},
		},
		Wait: shell.WaitDeps{
			Version: func(context.Context) (string, error) {
				*seq = append(*seq, "waitver")
				return "v0.4.0", nil
			},
			Sleep: func(time.Duration) {},
		},
	}
}

// TestSyncOnOpenOrderIsLoadBearing 钉住 spec §5 的三条承重顺序。
//
// ① EnsureRunning 必须在探 Busy 之前——闸一判据要从 agentd 的 /api/status
//
//	探，agentd 不在跑就探不出
//
// ② 探 Busy（闸一）必须在 activate 之前——与 cmd/upgrade.go:500 同序。反过来
//
//	会留下「磁盘是新的、跑着的是旧的」这种持续不一致
//
// ③ waitver 必须在 restart 之后——它等的就是重启的结果
func TestSyncOnOpenOrderIsLoadBearing(t *testing.T) {
	var seq []string
	out := shell.SyncOnOpen(context.Background(), okDeps(&seq))
	if out.Err != nil {
		t.Fatalf("一切顺利时不该有错误：%v", out.Err)
	}
	if out.Plan != shell.SyncDo {
		t.Fatalf("Plan = %v，想要 SyncDo", out.Plan)
	}
	want := []string{"ensure", "busy", "activate", "skill", "restart", "waitver"}
	if !slices.Equal(seq, want) {
		t.Errorf("调用序列 = %v，想要 %v", seq, want)
	}
}

// TestSyncOnOpenNeverBlocksOnFailure 是 D8 的唯一守卫。
//
// 同步路径上的任何失败都绝不能阻断打开控制台。本测试对每一个失败点各注入
// 一次，断言 SyncOnOpen 都会返回（而不是 panic、不是挂住），并把错误如实
// 带在 Err 里交给调用方。
//
// 这条测试的价值不在于覆盖率：它是「用户双击应用打不开」这个最严重后果的
// 唯一自动化防线。
func TestSyncOnOpenNeverBlocksOnFailure(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name     string
		mutate   func(*shell.OpenSyncDeps)
		wantErr  bool
		wantPlan shell.SyncPlan
	}{
		{"agentd 起不来", func(d *shell.OpenSyncDeps) {
			d.EnsureRunning = func() error { return boom }
		}, true, shell.SyncSkip},
		{"定位不到已装二进制", func(d *shell.OpenSyncDeps) {
			d.InstalledPath = func() (string, error) { return "", boom }
		}, true, shell.SyncSkip},
		{"读不出已装版本", func(d *shell.OpenSyncDeps) {
			d.InstalledVersion = func(string) string { return "" }
		}, false, shell.SyncSkip}, // 判不出 → SyncSkip，不是错误
		{"探不出活跃任务数", func(d *shell.OpenSyncDeps) {
			d.Busy = func(context.Context) (int, error) { return 0, boom }
		}, false, shell.SyncBlocked}, // 探不出 → 按繁忙处置 → SyncBlocked，不是错误
		{"换版失败", func(d *shell.OpenSyncDeps) {
			d.Sync.Activate = func(string, string) (string, error) { return "", boom }
		}, true, shell.SyncDo},
		{"触发重启失败", func(d *shell.OpenSyncDeps) {
			d.Sync.RestartAgentd = func(context.Context, bool) error { return boom }
		}, true, shell.SyncDo},
		{"agentd 没回来", func(d *shell.OpenSyncDeps) {
			d.Wait.Version = func(context.Context) (string, error) { return "v0.3.0", nil }
		}, true, shell.SyncDo},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var seq []string
			d := okDeps(&seq)
			c.mutate(&d)
			done := make(chan shell.SyncOutcome, 1)
			ctx, cancel := context.WithCancel(context.Background())
			// 「agentd 没回来」那条会走满超时循环；用可取消的 Sleep 把它压短，
			// 同时验证取消路径也返回而不是挂住
			d.Wait.Sleep = func(time.Duration) { cancel() }
			go func() { done <- shell.SyncOnOpen(ctx, d) }()
			select {
			case out := <-done:
				if out.Plan != c.wantPlan {
					t.Errorf("Plan = %v，想要 %v", out.Plan, c.wantPlan)
				}
				if c.wantErr && out.Err == nil {
					t.Errorf("想要一个错误，却拿到 nil（Plan=%v）", out.Plan)
				}
				if !c.wantErr && out.Err != nil {
					t.Errorf("不该有错误，却拿到 %v", out.Err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("SyncOnOpen 挂住了——D8 被破，用户双击应用会打不开")
			}
		})
	}
}

// TestSyncOnOpenBlockedWhenBusy 钉住有活跃任务时不换，且把任务数带出来给 UI。
func TestSyncOnOpenBlockedWhenBusy(t *testing.T) {
	var seq []string
	d := okDeps(&seq)
	d.Busy = func(context.Context) (int, error) { return 3, nil }
	out := shell.SyncOnOpen(context.Background(), d)
	if out.Plan != shell.SyncBlocked {
		t.Errorf("Plan = %v，想要 SyncBlocked", out.Plan)
	}
	if out.Busy != 3 {
		t.Errorf("Busy = %d，想要 3——托盘要用它显示「N 个任务进行中」", out.Busy)
	}
	if slices.Contains(seq, "activate") {
		t.Error("有活跃任务时仍然换了版")
	}
}
