// handoff service 三个子命令的 CLI 行为测试。
//
// 经 newServiceManager 缝注入 fake，测试不会真的装任何服务。
package cmd

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/service"
)

// fakeManager 是可编排的 service.Manager。
type fakeManager struct {
	installErr error
	uninstErr  error
	status     service.Status
	statusErr  error
	installed  *service.Spec
}

func (f *fakeManager) Install(s service.Spec) error {
	f.installed = &s
	return f.installErr
}
func (f *fakeManager) Uninstall() error                { return f.uninstErr }
func (f *fakeManager) Status() (service.Status, error) { return f.status, f.statusErr }
func (f *fakeManager) Kind() string                    { return "fake" }
func (f *fakeManager) UnitPath() (string, error)       { return "/tmp/fake.unit", nil }

// withFakeManager 替换 newServiceManager 缝。
func withFakeManager(t *testing.T, f *fakeManager) {
	t.Helper()
	old := newServiceManager
	newServiceManager = func(*slog.Logger) (service.Manager, error) { return f, nil }
	t.Cleanup(func() { newServiceManager = old })
}

// runService 跑一次 service 子命令，返回合并输出与错误。
func runService(t *testing.T, cfgPath string, args ...string) (string, error) {
	t.Helper()
	resetFlags(t)
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(append([]string{"service"}, append(args, "--config", cfgPath)...))
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	err := rootCmd.ExecuteContext(context.Background())
	return buf.String(), err
}

// install 成功时要把实际用到的路径打出来——用户下一步排障全靠这几行。
func TestServiceInstallReportsPaths(t *testing.T) {
	f := &fakeManager{}
	withFakeManager(t, f)
	cfg := writeStatusConfig(t)
	out, err := runService(t, cfg, "install")
	if err != nil {
		t.Fatalf("install 不应报错: %v", err)
	}
	if f.installed == nil {
		t.Fatal("Install 未被调用")
	}
	if f.installed.BinPath == "" {
		t.Error("Spec.BinPath 不应为空")
	}
	if f.installed.ConfigPath != cfg {
		t.Errorf("Spec.ConfigPath=%q，应等于 --config 给的 %q", f.installed.ConfigPath, cfg)
	}
	if !strings.Contains(out, "/tmp/fake.unit") {
		t.Errorf("输出应含单元路径:\n%s", out)
	}
}

// install 失败要把真因带到用户面前，不能吞掉。
func TestServiceInstallSurfacesCause(t *testing.T) {
	withFakeManager(t, &fakeManager{installErr: errors.New("Load failed: 5: Input/output error")})
	_, err := runService(t, writeStatusConfig(t), "install")
	if err == nil {
		t.Fatal("install 失败时应返回错误")
	}
	if !strings.Contains(err.Error(), "Input/output error") {
		t.Fatalf("错误应带真因，得到: %v", err)
	}
}

// status 的三种形态都要有各自可读的一行，不能都打成同一句。
func TestServiceStatusStates(t *testing.T) {
	cases := []struct {
		name string
		st   service.Status
		want string
	}{
		{"装了在跑", service.Status{Installed: true, Running: true}, "已托管"},
		{"装了没跑", service.Status{Installed: true, Running: false}, "已安装但未运行"},
		{"没装", service.Status{}, "未托管"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withFakeManager(t, &fakeManager{status: c.st})
			out, err := runService(t, writeStatusConfig(t), "status")
			if err != nil {
				t.Fatalf("status 不应报错: %v", err)
			}
			if !strings.Contains(out, c.want) {
				t.Fatalf("输出应含 %q:\n%s", c.want, out)
			}
		})
	}
}

// uninstall 幂等：没装时也应成功。
func TestServiceUninstallIsIdempotent(t *testing.T) {
	withFakeManager(t, &fakeManager{})
	if _, err := runService(t, writeStatusConfig(t), "uninstall"); err != nil {
		t.Fatalf("uninstall 不应报错: %v", err)
	}
}
