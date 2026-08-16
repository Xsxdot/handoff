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
}

func (f *fakeManager) Install(s service.Spec) error    { f.installed = true; f.gotSpec = s; return f.installErr }
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
	if f.gotSpec != spec {
		t.Fatalf("传给 Install 的 Spec = %+v, want %+v", f.gotSpec, spec)
	}
}

// 已安装但没在运行：必须 Install。Installed 不等于 Running——launchd/systemd
// 的「已装未跑 / 崩溃循环 / 手动 stop」都是真实且常见的状态，此时 EnsureRunning
// 必须重新托管拉起，不能因为单元文件存在就跳过。
func TestEnsureRunningInstallsWhenInstalledButStopped(t *testing.T) {
	f := &fakeManager{status: service.Status{Installed: true}}
	withManager(t, f, nil)
	spec := service.Spec{BinPath: "/usr/local/bin/handoff", ConfigPath: "/c.yaml", LogPath: "/l.log"}
	if err := EnsureRunning(slog.Default(), spec); err != nil {
		t.Fatalf("EnsureRunning 报错: %v", err)
	}
	if !f.installed {
		t.Fatal("agentd 已装未跑却没有重新 Install 拉起")
	}
	if f.gotSpec != spec {
		t.Fatalf("传给 Install 的 Spec = %+v, want %+v", f.gotSpec, spec)
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
