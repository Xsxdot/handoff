// b393_timeout_test.go —— B393 挂死截断的红色回路。
//
// 职责：钉住 coordinatorRunner.Resume（挂死的发生地）在遇到不返回的 CLI 时，
// 必须在协调者唤醒回合上界内判失败返回，而不是钉死到 hostapi 的 30m 缺省。
// 缝：生产 Runner 实现 coordinatorRunner.Resume（keysclient.Runner 接口冻结、无
// ctx 参数，返回 `-s` 续接回合）。这里能同时满足「现在红（无界等待）→ T1 后绿
// （秒级返回）」——放 keystone 层拿不到 ctx 会是个假回路（plan T0.2 论证）。
//
// 边界：只经导出构造 NewCoordinatorRunner 与 PATH 注入假 CLI；不碰真账本/网络。
package agentd

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/hostapi"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/toolchain"
)

// installSleepyCoordinatorCLI 装一个「长睡」的假 opencode：模拟 CLI 收尾挂死
// （不返回、不输出、进程不退出）。走 PATH 注入（仓内先例 coordrunner_test.go
// #installCoordinatorFakeCLI / hostapi/runturn_test.go#installFakeCLI）——真二进
// 制无法在单测里被命令化地造出挂死态，PATH 前插是唯一不改生产代码的注入面。
func installSleepyCoordinatorCLI(t *testing.T, sleepSec int) {
	t.Helper()
	binDir := t.TempDir()
	script := "#!/bin/sh\nsleep " + strconv.Itoa(sleepSec) + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "opencode"), []byte(script), 0o755); err != nil {
		t.Fatalf("写 sleepy fake CLI: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestB393ResumeHangsBoundedByTimeout 锁 R3.1：挂死的 resume 必须在
// coordWakeTurnTimeout 附近返回错误，而不是钉死到 hostapi 的 30m 缺省。
//
// 红（当前 HEAD）：coordinatorRunner.Resume 用 context.WithCancel(Background())
// 无期限，hostapi 走 30m 缺省 → 本测试在 10s 窗内收不到返回，超时红。
// 绿（T1.2）：readyCtx 给 1s 上界，秒级返回错误。
// 变异自验：把 readyCtx 的 WithTimeout 改回 WithCancel → 复红。
func TestB393ResumeHangsBoundedByTimeout(t *testing.T) {
	prev := coordWakeTurnTimeout
	coordWakeTurnTimeout = 1 * time.Second
	defer func() { coordWakeTurnTimeout = prev }()

	installSleepyCoordinatorCLI(t, 30)
	h := hostapi.NewWithCredentialPathFor(toolchain.CredRelPathFor)
	runner := testCoordRunner(h, nil) // coordrunner_test.go 既有夹具

	done := make(chan error, 1)
	go func() {
		_, err := runner.Resume(keysclient.SessionRef{
			CLI: "opencode", SessionID: "ses_x",
			HomeDir: t.TempDir(), Workdir: t.TempDir(),
		}, "唤醒简报")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("挂死回合必须判失败并返回错误")
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("resume 在 1s 上界后仍未返回（无界等待，R3.1）")
	}
}
