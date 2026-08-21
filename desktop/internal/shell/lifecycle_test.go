package shell

import (
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/service"
)

// fakeManager 是 service.Manager 的测试替身：Install 只记录被调用，不碰真系统。
// 必须有它——真 Install 会往 launchd/systemd 写单元文件，测试绝不允许。
type fakeManager struct {
	status     service.Status
	statusErr  error
	installErr error
	installed  bool
	gotSpec    service.Spec
	started    bool
	startErr   error
	stopped    bool
	restarted  bool
}

func (f *fakeManager) Install(s service.Spec) error {
	f.installed = true
	f.gotSpec = s
	return f.installErr
}
func (f *fakeManager) Start() error                    { f.started = true; return f.startErr }
func (f *fakeManager) Stop() error                     { f.stopped = true; return nil }
func (f *fakeManager) Restart() error                  { f.restarted = true; return nil }
func (f *fakeManager) Uninstall() error                { return nil }
func (f *fakeManager) Status() (service.Status, error) { return f.status, f.statusErr }
func (f *fakeManager) Kind() string                    { return "fake" }
func (f *fakeManager) UnitPath() (string, error)       { return "/fake/unit", nil }

func withManager(t *testing.T, m service.Manager, err error) {
	t.Helper()
	prev := newManager
	newManager = func(*slog.Logger) (service.Manager, error) { return m, err }
	t.Cleanup(func() { newManager = prev })
}

// 已经在跑：绝不能再 Install 一次——那会重装单元、打断正在跑的任务。
func TestEnsureRunningDoesNothingWhenAlreadyRunning(t *testing.T) {
	f := &fakeManager{status: service.Status{Installed: true, Running: true}}
	withManager(t, f, nil)
	if err := EnsureRunning(slog.Default(), service.Spec{BinPath: "/bin/handoff"}); err != nil {
		t.Fatalf("EnsureRunning 报错: %v", err)
	}
	if f.installed {
		t.Fatal("agentd 已在运行却又执行了 Install——会打断正在跑的任务")
	}
}

// 没装：装上。并且 Spec 要原样传下去。
func TestEnsureRunningInstallsWhenAbsent(t *testing.T) {
	f := &fakeManager{status: service.Status{Installed: false}}
	withManager(t, f, nil)
	spec := service.Spec{BinPath: "/usr/local/bin/handoff", ConfigPath: "/c.yaml", LogPath: "/l.log"}
	if err := EnsureRunning(slog.Default(), spec); err != nil {
		t.Fatalf("EnsureRunning 报错: %v", err)
	}
	if !f.installed {
		t.Fatal("agentd 没装却没有执行 Install")
	}
	if f.started {
		t.Fatal("单元没装却先调了 Start——Start 的契约是「只启动已安装的单元」，不该拿它探路")
	}
	if f.gotSpec != spec {
		t.Fatalf("传给 Install 的 Spec = %+v, want %+v", f.gotSpec, spec)
	}
}

// 已安装但没在运行：只 Start，**不许** Install。Installed 不等于 Running
// （「已装未跑 / 崩溃循环 / 手动 stop」都是真实状态），此时必须把它拉起来——
// 但拉起来不等于重装。
//
// 这条断言换过一次方向：此前这里要求的是 Install。改的理由在 Windows——
// 那边的 Install 会先 `schtasks /Delete /F` 再重建任务，而本函数在每次换版时
// 都会被 WaitAgentdBack 催一次，于是升级一次就把用户改过的任务定义抹一次。
func TestEnsureRunningStartsWithoutReinstallWhenInstalledButStopped(t *testing.T) {
	f := &fakeManager{status: service.Status{Installed: true}}
	withManager(t, f, nil)
	spec := service.Spec{BinPath: "/usr/local/bin/handoff", ConfigPath: "/c.yaml", LogPath: "/l.log"}
	if err := EnsureRunning(slog.Default(), spec); err != nil {
		t.Fatalf("EnsureRunning 报错: %v", err)
	}
	if !f.started {
		t.Fatal("agentd 已装未跑却没有 Start")
	}
	if f.installed {
		t.Fatal("已装的单元被重新 Install 了——Windows 上这会删掉任务再重建，抹掉用户的修改")
	}
}

// 已安装、但 Start 失败：回落到 Install。单元可能真的坏了（指向已被删除的
// 二进制、定义被改残），此时重装是对的自愈动作——「少重写一次」绝不能凌驾于
// 「agentd 起不来」之上。
func TestEnsureRunningFallsBackToInstallWhenStartFails(t *testing.T) {
	f := &fakeManager{
		status:   service.Status{Installed: true},
		startErr: errors.New("计划任务查询不到"),
	}
	withManager(t, f, nil)
	spec := service.Spec{BinPath: "/usr/local/bin/handoff", ConfigPath: "/c.yaml", LogPath: "/l.log"}
	if err := EnsureRunning(slog.Default(), spec); err != nil {
		t.Fatalf("EnsureRunning 报错: %v", err)
	}
	if !f.started {
		t.Fatal("没有先尝试 Start")
	}
	if !f.installed {
		t.Fatal("Start 失败后没有回落到 Install——agentd 会起不来")
	}
	if f.gotSpec != spec {
		t.Fatalf("传给 Install 的 Spec = %+v, want %+v", f.gotSpec, spec)
	}
}

// 被 handoff service stop 显式停用时，绝不自愈。
//
// why 承重：EnsureRunning 的既有逻辑是「没在跑 → Start，Start 失败 →
// Install 自愈」。launchd 上 stop 做过 bootout，Start 会失败，于是回落
// Install 把用户刚显式停掉的 agentd 装回来跑起来——stop 这个动作在装了
// 桌面壳的机器上当场失效。
func TestEnsureRunningRespectsDisabled(t *testing.T) {
	f := &fakeManager{status: service.Status{Installed: true, Running: false, Disabled: true}}
	withManager(t, f, nil)
	if err := EnsureRunning(slog.Default(), service.Spec{BinPath: "/opt/bin/handoff"}); err != nil {
		t.Fatalf("被停用不是错误，EnsureRunning 应正常返回: %v", err)
	}
	if f.started {
		t.Error("被停用时不得调 Start")
	}
	if f.installed {
		t.Error("被停用时不得调 Install——那会把用户显式停掉的 agentd 装回来")
	}
}

// 平台不支持（Windows）：把原因原样带出来，不许吞、不许 panic。
func TestEnsureRunningSurfacesUnsupportedPlatform(t *testing.T) {
	withManager(t, nil, errors.New("暂不支持 Windows：agentd 依赖的进程承载层 Windows 实现尚未完成"))
	err := EnsureRunning(slog.Default(), service.Spec{})
	if err == nil {
		t.Fatal("平台不支持却没报错")
	}
	if !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("错误信息丢掉了平台原因，实际 = %q", err)
	}
}
