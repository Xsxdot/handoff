// desktopwake_opener_test.go —— B434 G2：生产 opener 与装配点（契约 §3.7、r12 第 3 条；拆解稿
// §3.10、r5）。
//
// 职责：锁 PlatformDesktopOpener 两条平台分支与 darwin opener 的 continue 前缀闸；锁「NewServer
// 不装配 opener（测试进程永远拿不到真实 opener）、SetDesktopOpener 是唯一注入入口、装配在
// cmd/agentd.go 组装点」——后者以源码守卫断言（沿 TestB23314SetSchedulingBeforeStartAutomation
// 形状），因为漏装配在运行期只表现为「信已落盘、continue 未发出」，任何单测都不会因此变红。
// 边界：真实 `open claude://…` 拉起桌面端属真机清单（I 卡），单测从不以合法 continue 深链调
// darwin opener（那会在开发机上真的起子进程）。
package agentd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// TestB434PlatformDesktopOpenerUnsupported 锁非 darwin 分支：错误文本逐字为契约口径。
func TestB434PlatformDesktopOpenerUnsupported(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		err := PlatformDesktopOpener(goos)(proto.ClaudeContinueURLPrefix + "local_x")
		if !errors.Is(err, errDesktopDeepLinkUnsupported) || err.Error() != "本平台不支持 Claude 桌面端深链" {
			t.Fatalf("%s：应报「本平台不支持 Claude 桌面端深链」，实得 %v", goos, err)
		}
	}
}

// TestB434DarwinOpenerRejectsNonContinue 锁 darwin opener 的前缀闸：只放行 continue 深链，
// 其它 URL（含其它 claude:// 路径）在起子进程之前被拒。
func TestB434DarwinOpenerRejectsNonContinue(t *testing.T) {
	open := PlatformDesktopOpener("darwin")
	for _, link := range []string{"https://example.com/x", "claude://code/new?folder=/tmp", ""} {
		if err := open(link); err == nil || !strings.Contains(err.Error(), "拒绝打开非 continue 深链") {
			t.Fatalf("%q 应被前缀闸拒绝，实得 %v", link, err)
		}
	}
}

// TestB434NewServerLeavesDesktopOpenerNil 锁 r12 第 3 条：NewServer 不装配 opener（V 的 nil 语义），
// 几十处测试夹具经 NewServer 拿到的 Server 永远不会在开发机上真的发深链。
func TestB434NewServerLeavesDesktopOpenerNil(t *testing.T) {
	backend, err := store.Open(t.TempDir() + "/handoff.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	fresh := NewServer(&config.Config{Token: testToken, DataDir: t.TempDir()}, backend,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if fresh.desktopOpen != nil {
		t.Fatal("NewServer 不得装配 desktopOpen（装配只在 cmd/agentd.go 组装点经 SetDesktopOpener）")
	}
}

// TestB434SetDesktopOpenerWiresWake 锁注入入口：经 SetDesktopOpener 装上的平台 opener 被
// 写信路径（现行唯一调用方 = O2 驻场者门铃）使用——非 darwin opener 下信照常落盘、按「continue
// 未发出」处置且原因为平台不支持；夹具 spy 已被替换，不再被调。
func TestB434SetDesktopOpenerWiresWake(t *testing.T) {
	env, _, sessionID, _, home, spy := o2ResidentEnv(t)
	env.srv.SetDesktopOpener(PlatformDesktopOpener("linux"))
	logs := o2CaptureLog(env.srv)
	sendSessionMessage(t, env.srv.rooms, sessionID, "user:tester", "经注入入口的 opener", nil, 0)
	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
		t.Fatalf("门铃未打开不挡消费轮: %v", err)
	}
	out := logs.String()
	if !strings.Contains(out, "continue 未发出") || !strings.Contains(out, errDesktopDeepLinkUnsupported.Error()) {
		t.Fatalf("应按未打开处置且原因为平台不支持: %s", out)
	}
	if n := len(o2Letters(t, home)); n != 1 || len(spy.calls()) != 0 {
		t.Fatalf("信应照常落盘、夹具 spy 已被替换不应再被调：信 %d 封、spy %v", n, spy.calls())
	}
}

// TestB434AgentdAssemblesDesktopOpener 是装配点源码守卫（r12 第 3 条）：cmd/agentd.go 组装点在
// agentd.NewServer 之后、setupLedger（其内 StartAutomation 起消费循环）之前恰一次调用
// srv.SetDesktopOpener(agentd.PlatformDesktopOpener(runtime.GOOS))。
// 为什么必须源码断言：漏这一行时 desktopOpen 为 nil，生产唤醒只表现为「信已落盘、continue
// 未发出」+ 一条 needs_human，编译与全部单测照绿。
func TestB434AgentdAssemblesDesktopOpener(t *testing.T) {
	b, err := os.ReadFile("../../cmd/agentd.go")
	if err != nil {
		t.Fatalf("读组装点源码: %v", err)
	}
	src := string(b)
	const wire = "srv.SetDesktopOpener(agentd.PlatformDesktopOpener(runtime.GOOS))"
	if n := strings.Count(src, wire); n != 1 {
		t.Fatalf("cmd/agentd.go 应恰一次 %s，实得 %d 次", wire, n)
	}
	newIdx := strings.Index(src, "srv := agentd.NewServer(")
	wireIdx := strings.Index(src, wire)
	ledgerIdx := strings.Index(src, ":= setupLedger(cfg, srv,")
	if newIdx < 0 || ledgerIdx < 0 {
		t.Fatalf("组装点锚点缺失：NewServer=%d setupLedger=%d", newIdx, ledgerIdx)
	}
	if !(newIdx < wireIdx && wireIdx < ledgerIdx) {
		t.Fatalf("装配顺序错误：应 NewServer(%d) < SetDesktopOpener(%d) < setupLedger(%d)", newIdx, wireIdx, ledgerIdx)
	}
}
