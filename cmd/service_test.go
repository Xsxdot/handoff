// handoff service 三个子命令的 CLI 行为测试。
//
// 经 newServiceManager 缝注入 fake，测试不会真的装任何服务。
package cmd

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/service"
)

// fakeManager 是可编排的 service.Manager。
type fakeManager struct {
	installErr error
	uninstErr  error
	status     service.Status
	statusErr  error
	installed  *service.Spec
	started    bool
	startErr   error
	stopped    bool
	stopErr    error
	restarted  bool
	restartErr error
}

func (f *fakeManager) Install(s service.Spec) error {
	f.installed = &s
	return f.installErr
}
func (f *fakeManager) Start() error                    { f.started = true; return f.startErr }
func (f *fakeManager) Stop() error                     { f.stopped = true; return f.stopErr }
func (f *fakeManager) Restart() error                  { f.restarted = true; return f.restartErr }
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
	// go test 自己的二进制在编译缓存里，resolveSpec 会当临时文件拒掉。
	// 这里换成一个看起来像安装产物的路径，只测 CLI 行为，不碰真文件。
	oldExe := osExecutable
	osExecutable = func() (string, error) { return "/usr/local/bin/handoff", nil }
	t.Cleanup(func() { osExecutable = oldExe })
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

// start 调 Manager.Start 并报出结果。
func TestServiceStart(t *testing.T) {
	f := &fakeManager{}
	withFakeManager(t, f)
	out, err := runService(t, writeStatusConfig(t), "start")
	if err != nil {
		t.Fatalf("start: %v\n%s", err, out)
	}
	if !f.started {
		t.Error("start 必须调 Manager.Start")
	}
	if !strings.Contains(out, "已启动") {
		t.Errorf("输出应报「已启动」，得到:\n%s", out)
	}
}

// stop 必须把「它不会自己回来」这句打出来。
//
// why：这是形态变化。不说的话，用户下次发现本机派不了活时不会想到
// 是自己停的——那正是 install 打「Ctrl-C 停不掉它」要避免的同一类坑。
func TestServiceStopWarnsItWontComeBack(t *testing.T) {
	f := &fakeManager{}
	withFakeManager(t, f)
	out, err := runService(t, writeStatusConfig(t), "stop")
	if err != nil {
		t.Fatalf("stop: %v\n%s", err, out)
	}
	if !f.stopped {
		t.Error("stop 必须调 Manager.Stop")
	}
	for _, want := range []string{"不会自己回来", "重启机器", "handoff service start"} {
		if !strings.Contains(out, want) {
			t.Errorf("stop 的输出必须含 %q（形态变化要说清楚），得到:\n%s", want, out)
		}
	}
}

// restart 调 Manager.Restart 并报出结果。
func TestServiceRestart(t *testing.T) {
	f := &fakeManager{}
	withFakeManager(t, f)
	out, err := runService(t, writeStatusConfig(t), "restart")
	if err != nil {
		t.Fatalf("restart: %v\n%s", err, out)
	}
	if !f.restarted {
		t.Error("restart 必须调 Manager.Restart")
	}
	if !strings.Contains(out, "已重启") {
		t.Errorf("输出应报「已重启」，得到:\n%s", out)
	}
}

// 未安装时报错必须直接给出 install，而不是把底层原文原样抛给用户。
func TestServiceLifecycleNotInstalledPointsAtInstall(t *testing.T) {
	for _, sub := range []string{"start", "stop", "restart"} {
		t.Run(sub, func(t *testing.T) {
			f := &fakeManager{
				startErr:   service.ErrNotInstalled,
				stopErr:    service.ErrNotInstalled,
				restartErr: service.ErrNotInstalled,
			}
			withFakeManager(t, f)
			out, err := runService(t, writeStatusConfig(t), sub)
			if err == nil {
				t.Fatal("未安装时应报错")
			}
			combined := out + err.Error()
			if !strings.Contains(combined, "handoff service install") {
				t.Errorf("未安装的处置必须是 install，得到:\n%s", combined)
			}
		})
	}
}

// status 要把「被停用」单独报出来。
//
// why：现状「已安装但未运行」的处置是「看日志找原因，或 install 重装」。
// 被 stop 停住时那条是错的——会把用户支去重装一个本来好好的单元。
func TestServiceStatusReportsDisabled(t *testing.T) {
	f := &fakeManager{status: service.Status{Installed: true, Disabled: true}}
	withFakeManager(t, f)
	out, err := runService(t, writeStatusConfig(t), "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if !strings.Contains(out, "已停止") {
		t.Errorf("被停用时应报「已停止」，得到:\n%s", out)
	}
	if !strings.Contains(out, "handoff service start") {
		t.Errorf("被停用的处置是 start，得到:\n%s", out)
	}
	if strings.Contains(out, "重装") {
		t.Errorf("被停用时不得建议重装（那是崩溃场景的处置），得到:\n%s", out)
	}
}

// 「在跑但已停用」是 stop 半途失败留下的真实状态，不能被报成一切正常。
func TestServiceStatusReportsRunningButDisabled(t *testing.T) {
	f := &fakeManager{status: service.Status{Installed: true, Running: true, Disabled: true}}
	withFakeManager(t, f)
	out, err := runService(t, writeStatusConfig(t), "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if !strings.Contains(out, "已停用") {
		t.Errorf("在跑但已停用时必须点出「已停用」，否则重启机器后它不会回来而用户毫不知情，得到:\n%s", out)
	}
}

func TestIsEphemeralBin(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/Users/x/Library/Caches/go-build/44/44109cc-d/handoff", true},
		{"/tmp/go-build123/b001/exe/handoff", true},
		{"/Users/x/.cache/go-build/ab/abcd/handoff", true},
		{"/Users/x/.local/bin/handoff", false},
		{"/usr/local/bin/handoff", false},
		{"/opt/homebrew/bin/handoff", false},
	}
	for _, c := range cases {
		if got := isEphemeralBin(c.path); got != c.want {
			t.Errorf("isEphemeralBin(%q)=%v，want %v", c.path, got, c.want)
		}
	}
}

// go run 的缓存路径不能写进服务单元；有已安装的手持二进制时改用它。
func TestResolveServiceBinFallsBackFromGoBuildCache(t *testing.T) {
	dir, cleanup := makeDurableServiceFixture(t)
	defer cleanup()
	durable := filepath.Join(dir, "handoff")
	if err := os.WriteFile(durable, []byte("ordinary service fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !regularFileExists(durable) {
		t.Fatalf("稳定候选不是普通文件：%q", durable)
	}
	if isEphemeralBin(durable) {
		t.Fatalf("稳定候选仍被判为临时文件：%q；不能用它覆盖 /tmp 回退判据", durable)
	}
	got, err := resolveServiceBinFrom("/Users/x/Library/Caches/go-build/44/aa-d/handoff", []string{durable})
	if err != nil {
		t.Fatalf("有已安装二进制时应回退，得到: %v", err)
	}
	want := durable
	if r, err := filepath.EvalSymlinks(durable); err == nil {
		want = r
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// makeDurableServiceFixture 找一个不受 isEphemeralBin 判定的可写根目录。
// 不使用仓源文件、t.TempDir 或 HOME：回退候选必须像安装产物一样是普通文件，
// 才能真实验证 Linux 仓位于 /tmp 时仍会选择稳定候选。
func makeDurableServiceFixture(t *testing.T) (string, func()) {
	t.Helper()
	var roots []string
	switch runtime.GOOS {
	case "linux":
		// /dev/shm is a system-backed, non-temporary filesystem available in
		// restricted Linux test sandboxes where /var/cache is read-only.
		roots = []string{"/var/cache/handoff-b256-fixture", "/dev/shm/handoff-b256-fixture"}
	case "darwin":
		roots = []string{"/Library/Application Support/handoff-b256-fixture", "/var/cache/handoff-b256-fixture"}
	case "windows":
		roots = []string{`C:\ProgramData\handoff-b256-fixture`}
	default:
		roots = []string{"/var/cache/handoff-b256-fixture"}
	}
	if cache := os.Getenv("GOCACHE"); filepath.IsAbs(cache) {
		roots = append(roots, filepath.Join(cache, "handoff-b256-fixture"))
	}
	if parent := filepath.Dir(filepath.Clean(os.TempDir())); parent != "/" {
		roots = append(roots, filepath.Join(parent, "handoff-b256-fixture"))
	}
	for _, root := range roots {
		if err := os.MkdirAll(root, 0o755); err != nil {
			continue
		}
		if isEphemeralBin(root) {
			continue
		}
		dir, err := os.MkdirTemp(root, "case-")
		if err != nil {
			continue
		}
		return dir, func() { _ = os.RemoveAll(dir) }
	}
	t.Fatalf("找不到不在临时目录且可写的服务夹具根；os.TempDir=%q", os.TempDir())
	return "", func() {}
}

func TestResolveServiceBinSkipsTempFallback(t *testing.T) {
	durable := filepath.Join(t.TempDir(), "go-build-b256", "handoff")
	if err := os.MkdirAll(filepath.Dir(durable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(durable, []byte("temporary service fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !regularFileExists(durable) {
		t.Fatalf("临时候选不是普通文件：%q", durable)
	}
	if !isEphemeralBin(durable) {
		t.Fatalf("t.TempDir 候选未被判为临时文件：%q", durable)
	}
	if _, err := resolveServiceBinFrom("/Users/x/Library/Caches/go-build/44/aa-d/handoff", []string{durable}); err == nil {
		t.Fatal("临时目录候选必须被跳过")
	} else if !strings.Contains(err.Error(), "临时") && !strings.Contains(err.Error(), "go run") {
		t.Fatalf("跳过临时候选时错误应说明临时编译产物：%v", err)
	}
}

// 没有任何可托管的稳定二进制时必须硬拒，不能把缓存路径写进 plist。
func TestResolveServiceBinRejectsGoBuildWithoutFallback(t *testing.T) {
	_, err := resolveServiceBinFrom("/Users/x/Library/Caches/go-build/44/aa-d/handoff", nil)
	if err == nil {
		t.Fatal("没有稳定二进制时应拒绝")
	}
	if !strings.Contains(err.Error(), "临时") && !strings.Contains(err.Error(), "go run") {
		t.Fatalf("错误应说明是临时编译产物，得到: %v", err)
	}
}

// 已经是安装目录里的二进制，原样返回。
func TestResolveServiceBinKeepsDurablePath(t *testing.T) {
	got, err := resolveServiceBinFrom("/Users/x/.local/bin/handoff", nil)
	if err != nil {
		t.Fatalf("稳定路径不应报错: %v", err)
	}
	if got != "/Users/x/.local/bin/handoff" {
		t.Fatalf("got %q", got)
	}
}
