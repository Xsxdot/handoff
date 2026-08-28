// agentd 命令测试：HTTP server 超时配置（P1-3）。
//
// 覆盖：newAgentdHTTPServer 的四个超时字段全部非零——这是「防 slowloris / 防
// 半死连接挂起」的配置级守卫；另断言 WriteTimeout ≥ agentd.RunCmdTimeout——
// handleTaskRun 同步执行 RunCmd，写超时小于命令执行上限会把长审阅命令掐断
// （退出码 124 契约无法兑现，见 cmd/agentd.go newAgentdHTTPServer 注释）。
// http.Server 超时行为本身由 net/http 保证，httptest 用自己的 server 无法覆盖，
// 故只做配置存在性断言（why 见 P1-3 修法）。
package cmd

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/agentd"
	"github.com/Xsxdot/handoff/internal/executor/grok"
	"github.com/Xsxdot/handoff/internal/prochost"
	"github.com/Xsxdot/handoff/internal/toolchain"
)

// 注册表必须认识始终可用的执行者名：dispatch --executor <name> 的路由前提。
//
// 为什么每个始终可用名字都要断言而不是只断言数量：漏掉任一行都不会编译报错，
// 症状要拖到「派发时报未注册」才暴露。grok 是否存在由符号链接能力决定，
// 由 TestAdaptersForSkipsGrokWhenSymlinkUnavailable 单独覆盖。
func TestAdapterRegistryHasAlwaysAvailableExecutors(t *testing.T) {
	ads := defaultAdapters(slog.Default())
	for _, want := range []string{"opencode", "claude", "codex", "agy", "fake"} {
		if _, ok := ads[want]; !ok {
			names := make([]string, 0, len(ads))
			for n := range ads {
				names = append(names, n)
			}
			t.Fatalf("adapter 注册表缺 %s，实际注册: %v", want, names)
		}
	}
}

// TestAdaptersForRegistersClaudeOnAllPlatforms 钉住 B128 的核心结论：
// claude 不再按平台拒绝。
func TestAdaptersForRegistersClaudeOnAllPlatforms(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		ads := adaptersForWithProbe(goos, slog.New(slog.NewTextHandler(io.Discard, nil)), t.TempDir())
		if _, ok := ads["claude"]; !ok {
			t.Fatalf("goos=%s 时 claude 未注册", goos)
		}
	}
}

// TestAdaptersForRegistersAgyOnAllPlatforms 确保 agy 在所有平台注册。
func TestAdaptersForRegistersAgyOnAllPlatforms(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		ads := adaptersForWithProbe(goos, slog.New(slog.NewTextHandler(io.Discard, nil)), t.TempDir())
		if _, ok := ads["agy"]; !ok {
			t.Fatalf("goos=%s 时 agy 未注册", goos)
		}
	}
}

// TestAdaptersForSkipsGrokWhenSymlinkUnavailable 钉住 grok 走能力探测：
// 探测目录不可用时必须不注册，而不是注册了等运行期炸。
func TestAdaptersForSkipsGrokWhenSymlinkUnavailable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	ads := adaptersForWithProbe("windows", slog.New(slog.NewTextHandler(io.Discard, nil)), missing)
	if _, ok := ads["grok"]; ok {
		t.Fatalf("符号链接不可用时 grok 仍被注册")
	}
	// 其余执行器不受影响：一个执行器不可用不该拖垮整张注册表
	for _, name := range []string{"opencode", "codex", "claude", "agy", "fake"} {
		if _, ok := ads[name]; !ok {
			t.Fatalf("%s 被误伤，未注册", name)
		}
	}
}

// TestAdaptersForRegistersGrokWhenSymlinkAvailable 钉住 grok 注册表的正向接线：
// 防止实现行被删掉而现有测试仍全绿的静默回归，问题会拖到派发时才暴露。
// Windows 上没有符号链接权限时跳过；这不是 grok 注册逻辑失败。
func TestAdaptersForRegistersGrokWhenSymlinkAvailable(t *testing.T) {
	probeDir := t.TempDir()
	if supported, reason := grok.SymlinkCapability(probeDir); !supported {
		t.Skipf("本机不具备符号链接能力，跳过 grok 正向注册测试: %s", reason)
	}
	ads := adaptersForWithProbe("windows", slog.New(slog.NewTextHandler(io.Discard, nil)), probeDir)
	if _, ok := ads["grok"]; !ok {
		t.Fatalf("符号链接能力可用时 grok 未注册；实现行被删掉会造成静默回归")
	}
}

func TestNewAgentdHTTPServerTimeouts(t *testing.T) {
	s := newAgentdHTTPServer("127.0.0.1:0", http.NewServeMux())
	if s.Addr != "127.0.0.1:0" {
		t.Errorf("Addr=%q, want 127.0.0.1:0", s.Addr)
	}
	if s.ReadHeaderTimeout <= 0 {
		t.Errorf("ReadHeaderTimeout 必须非零（防 slowloris），实际 %v", s.ReadHeaderTimeout)
	}
	if s.ReadTimeout <= 0 {
		t.Errorf("ReadTimeout 必须非零（请求体读取上限），实际 %v", s.ReadTimeout)
	}
	if s.WriteTimeout <= 0 {
		t.Errorf("WriteTimeout 必须非零（响应写入上限），实际 %v", s.WriteTimeout)
	}
	if s.WriteTimeout < agentd.RunCmdTimeout {
		t.Errorf("WriteTimeout %v 必须 >= run 路由执行上限 %v（否则长审阅命令被掐断）",
			s.WriteTimeout, agentd.RunCmdTimeout)
	}
	if s.IdleTimeout <= 0 {
		t.Errorf("IdleTimeout 必须非零（keep-alive 空闲回收），实际 %v", s.IdleTimeout)
	}
}

// TestMarkCapabilityReported 钉住启动期必须播报归属能力——
// 静默缺席正是 B37 反复在防的东西：能力没了而日志一个字不说，
// 排障时会一路怀疑到别处去。
func TestMarkCapabilityReported(t *testing.T) {
	supported, reason := prochost.MarkCapability()
	if !supported && reason == "" {
		t.Fatalf("不支持时必须给出理由，否则日志等于没说")
	}
	if supported && reason != "" {
		t.Fatalf("支持时不该带理由：%q", reason)
	}
}

// 缺省执行者没找到时必须 WARN，且处置里要指出 path_dirs 这条出路。
//
// why：B71 之前，「opencode 没装」这件事要等到第一次派发才暴露，报错落在
// 任务事件流里，离「重启后 agentd 起来了」这个时间点最远。启动时报出来，
// 重启完看一眼日志就知道。
func TestLogExecutorDetectionWarnsOnMissingDefault(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	rs := []toolchain.Result{
		{Name: "opencode", State: toolchain.StateMissing},
		{Name: "claude", State: toolchain.StateAuthUnknown, Path: "/usr/local/bin/claude"},
	}

	logExecutorDetection(log, "opencode", rs)

	out := buf.String()
	if !strings.Contains(out, "executor 探测") {
		t.Errorf("四家探测结果必须成表进启动日志，实得:\n%s", out)
	}
	if !strings.Contains(out, "/usr/local/bin/claude") {
		t.Errorf("探测到的绝对路径必须进日志（排障要靠它），实得:\n%s", out)
	}
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("缺省执行者缺失必须 WARN，实得:\n%s", out)
	}
	if !strings.Contains(out, "path_dirs") {
		t.Errorf("WARN 必须给出处置（path_dirs），实得:\n%s", out)
	}
}

// 缺省执行者在位时不 WARN——每次启动打一条无从处置的告警，只会让人学会忽略日志。
func TestLogExecutorDetectionQuietWhenDefaultPresent(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	rs := []toolchain.Result{{Name: "opencode", State: toolchain.StateReady, Path: "/x/opencode"}}

	logExecutorDetection(log, "opencode", rs)

	if strings.Contains(buf.String(), "level=WARN") {
		t.Errorf("缺省执行者就绪时不该 WARN，实得:\n%s", buf.String())
	}
}

// 缺省是 fake 时不 WARN：fake 是脚本演示执行者，本来就没有对应的二进制。
func TestLogExecutorDetectionQuietForFake(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	rs := []toolchain.Result{{Name: "opencode", State: toolchain.StateMissing}}

	logExecutorDetection(log, "fake", rs)

	if strings.Contains(buf.String(), "level=WARN") {
		t.Errorf("缺省是 fake 时不该 WARN，实得:\n%s", buf.String())
	}
}

// TestAdaptersForAlwaysAvailableKeepsAll 钉住平台能力探测不误伤始终可用的执行器。
func TestAdaptersForAlwaysAvailableKeepsAll(t *testing.T) {
	got := adaptersForWithProbe("darwin", slog.New(slog.NewTextHandler(io.Discard, nil)), t.TempDir())
	for _, name := range []string{"opencode", "claude", "codex", "fake"} {
		if _, ok := got[name]; !ok {
			t.Errorf("应注册 %s", name)
		}
	}
}
