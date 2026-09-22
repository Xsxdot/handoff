// b399_classify_runner_err_test.go —— B399 review-1：classifyRunnerErr 翻译缝
// 的端到端断言（生产引用 4、测试引用 0 的零测试缝补齐）。
//
// 职责：经真实 coordinatorRunner.Resume 路径（PATH 假 CLI → hostapi.RunTurn →
// classifyRunnerErr），注入「回合超时」与「Session not found」两类失败，断言上抛
// 错误 errors.Is 各自命中 keysclient 哨兵、且不串类——keystone 据此分流（超时保留
// 会话，只有会话不存在才重建，B399 spec r2 §5）。
// 缝：coordinatorRunner.Resume（生产 Runner 实现，keysclient.Runner 接口冻结）。
// 边界：夹具照 b393_timeout_test.go（installSleepyCoordinatorCLI / testCoordRunner）；
// 不碰真账本/网络。
package agentd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/hostapi"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/toolchain"
)

// installFailingCoordinatorCLI 装一个向 stderr 打印指定文本并 exit 1 的假 opencode：
// 模拟载体 CLI 报「会话不存在」等失败（hostapi driver 的 waitErr 分支读 stderr 尾部）。
// PATH 前插是唯一不改生产代码的注入面（同 b393 installSleepyCoordinatorCLI 先例）。
func installFailingCoordinatorCLI(t *testing.T, stderr string) {
	t.Helper()
	binDir := t.TempDir()
	script := "#!/bin/sh\necho '" + stderr + "' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(binDir, "opencode"), []byte(script), 0o755); err != nil {
		t.Fatalf("写 failing fake CLI: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestB399ClassifyRunnerErrTimeoutSentinel 锁 T1.5 翻译缝·超时侧：挂死 Resume
// 撞 coordWakeTurnTimeout 上界后，上抛错误必须 errors.Is 命中
// keysclient.ErrTurnTimeout，且不命中 keysclient.ErrSessionNotFound。
//
// 变异（把 classifyRunnerErr 改成 identity / 两哨兵对调）→ 复红。
func TestB399ClassifyRunnerErrTimeoutSentinel(t *testing.T) {
	prev := coordWakeTurnTimeout
	coordWakeTurnTimeout = 200 * time.Millisecond
	defer func() { coordWakeTurnTimeout = prev }()

	installSleepyCoordinatorCLI(t, 30)
	h := hostapi.NewWithCredentialPathFor(toolchain.CredRelPathFor)
	runner := testCoordRunner(h, nil)

	_, err := runner.Resume(keysclient.SessionRef{
		CLI: "opencode", SessionID: "ses_x",
		HomeDir: t.TempDir(), Workdir: t.TempDir(),
	}, "唤醒简报")
	if err == nil {
		t.Fatalf("挂死回合应失败")
	}
	if !errors.Is(err, keysclient.ErrTurnTimeout) {
		t.Fatalf("超时错误未翻译成 keysclient.ErrTurnTimeout: %v", err)
	}
	if errors.Is(err, keysclient.ErrSessionNotFound) {
		t.Fatalf("超时不得被判为会话不存在: %v", err)
	}
}

// TestB399ClassifyRunnerErrSessionNotFoundSentinel 锁 T1.5 翻译缝·会话不存在侧：
// 假 CLI 向 stderr 打印 "Session not found" 并 exit 1 后，上抛错误必须
// errors.Is 命中 keysclient.ErrSessionNotFound，且不命中 keysclient.ErrTurnTimeout。
//
// 变异（把 classifyRunnerErr 改成 identity / 两哨兵对调）→ 复红。
func TestB399ClassifyRunnerErrSessionNotFoundSentinel(t *testing.T) {
	installFailingCoordinatorCLI(t, "Session not found")
	h := hostapi.NewWithCredentialPathFor(toolchain.CredRelPathFor)
	runner := testCoordRunner(h, nil)

	_, err := runner.Resume(keysclient.SessionRef{
		CLI: "opencode", SessionID: "ses_gone",
		HomeDir: t.TempDir(), Workdir: t.TempDir(),
	}, "续接")
	if err == nil {
		t.Fatalf("会话不存在应失败")
	}
	if !errors.Is(err, keysclient.ErrSessionNotFound) {
		t.Fatalf("错误未翻译成 keysclient.ErrSessionNotFound: %v", err)
	}
	if errors.Is(err, keysclient.ErrTurnTimeout) {
		t.Fatalf("会话不存在不得被判为超时: %v", err)
	}
}
