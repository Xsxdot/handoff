// launchd 实现的测试：plist 内容与 launchctl 调用序列都在这里钉住。
//
// 全部经缝注入，不真的调 launchctl、不真的写 ~/Library——测试跑完机器上
// 不会多出任何服务。
package service

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// newTestLaunchd 造一个全缝替换的 launchd manager，并返回记录调用的切片指针。
func newTestLaunchd(t *testing.T, runErr error) (*launchdManager, *[]string, *map[string][]byte) {
	t.Helper()
	calls := []string{}
	written := map[string][]byte{}
	m := &launchdManager{
		log:      testLogger(),
		homeDir:  func() (string, error) { return "/home/u", nil },
		plistDir: "/home/u/Library/LaunchAgents",
		mkdirAll: func(string, os.FileMode) error { return nil },
		run: func(name string, args ...string) ([]byte, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			return []byte("ok"), runErr
		},
		writeFile: func(p string, b []byte, _ uint32) error { written[p] = b; return nil },
		remove:    func(p string) error { delete(written, p); return nil },
		// 默认「plist 在」：多数用例测的是已装场景。测未装的用例自己覆盖它
		stat: func(string) (os.FileInfo, error) { return nil, nil },
		// 测试里不真的睡：复核轮询最多 25 次，真睡会把包的单测从毫秒拖到秒级
		sleep: func(time.Duration) {},
	}
	return m, &calls, &written
}

// Stop 必须 disable 在前、bootout 在后。
//
// why 顺序承重：disable 成功而 bootout 失败，留下的是「还在跑但已停用」，
// 重启后自己下去；反过来 bootout 成功而 disable 失败，留下的是「停了但仍
// 启用」，下次登录 launchd 自动 bootstrap 回来，把用户的 stop 无声撤销。
// 选前一种失败形态。
func TestLaunchdStopDisablesBeforeBootout(t *testing.T) {
	m, calls, _ := newTestLaunchd(t, nil)
	// print 失败 => Status.Running 为 false，复核轮询第一次就通过
	m.run = func(name string, args ...string) ([]byte, error) {
		*calls = append(*calls, name+" "+strings.Join(args, " "))
		if len(args) > 0 && args[0] == "print" {
			return []byte(""), errors.New("exit status 113")
		}
		return []byte("ok"), nil
	}
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	joined := strings.Join(*calls, " | ")
	di, bi := strings.Index(joined, "disable"), strings.Index(joined, "bootout")
	if di < 0 || bi < 0 {
		t.Fatalf("Stop 必须同时发出 disable 与 bootout: %s", joined)
	}
	if di > bi {
		t.Errorf("disable 必须先于 bootout（否则 bootout 成功而 disable 失败时，下次登录它会自己回来）: %s", joined)
	}
}

// 只 bootout 不 disable 撑不过一次登录，所以 disable 失败必须让 Stop 失败。
func TestLaunchdStopFailsWhenDisableFails(t *testing.T) {
	m, _, _ := newTestLaunchd(t, nil)
	m.run = func(name string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "disable" {
			return []byte("Could not disable service"), errors.New("exit status 1")
		}
		return []byte("ok"), nil
	}
	err := m.Stop()
	if err == nil {
		t.Fatal("disable 失败时 Stop 必须报错——只 bootout 的话它下次登录就回来了")
	}
	if !strings.Contains(err.Error(), "Could not disable") {
		t.Errorf("报错要带 launchctl 原文（真因）: %v", err)
	}
}

// 停不下来时不许报成功：bootout 返回 0 只说明请求被受理。
func TestLaunchdStopFailsWhenStillRunning(t *testing.T) {
	m, _, _ := newTestLaunchd(t, nil)
	m.run = func(name string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "print" {
			return []byte("state = running\n\tpid = 42"), nil
		}
		return []byte("ok"), nil
	}
	if err := m.Stop(); err == nil {
		t.Fatal("复核到仍在运行时 Stop 必须报错，不能报「已停止」")
	}
}

// plist 不在 => 未安装，三个命令一律硬拒，且错误可被 errors.Is 判别。
func TestLaunchdLifecycleRefusesWhenNotInstalled(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*launchdManager) error
	}{
		{"Start", (*launchdManager).Start},
		{"Stop", (*launchdManager).Stop},
		{"Restart", (*launchdManager).Restart},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, calls, _ := newTestLaunchd(t, nil)
			m.stat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
			err := tc.call(m)
			if !errors.Is(err, ErrNotInstalled) {
				t.Fatalf("未安装时应返回 ErrNotInstalled，得到: %v", err)
			}
			for _, c := range *calls {
				for _, mutating := range []string{"bootout", "bootstrap", "kill", "disable", "enable"} {
					if strings.Contains(c, mutating) {
						t.Errorf("未安装时不得发出任何变更类命令，却发了: %s", c)
					}
				}
			}
		})
	}
}

// Start 必须 enable 在前、bootstrap 在后。
//
// why：被 launchctl disable 过的 target，bootstrap 会直接拒（Service is
// disabled）。而 Stop 正是靠 disable 生效的，所以这是 stop→start 的必经之路。
func TestLaunchdStartEnablesBeforeBootstrap(t *testing.T) {
	m, calls, _ := newTestLaunchd(t, nil)
	m.run = func(name string, args ...string) ([]byte, error) {
		*calls = append(*calls, name+" "+strings.Join(args, " "))
		if len(args) > 0 && args[0] == "print" {
			return []byte("state = running\n\tpid = 42"), nil
		}
		return []byte("ok"), nil
	}
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	joined := strings.Join(*calls, " | ")
	ei, bi := strings.Index(joined, "enable"), strings.Index(joined, "bootstrap")
	if ei < 0 || bi < 0 {
		t.Fatalf("Start 必须同时发出 enable 与 bootstrap: %s", joined)
	}
	if ei > bi {
		t.Errorf("enable 必须先于 bootstrap（被 disable 过的 target，bootstrap 会直接拒）: %s", joined)
	}
}

// bootstrap 与 kickstart 都失败时，两个命令的原文都必须保留。
//
// why：bootstrap 的失败通常才是真因；只报 kickstart 的「找不到服务」会把
// 用户误导到「没装」，但 ensureInstalled 刚确认过 plist 确实存在。
func TestLaunchdStartReportsBootstrapAndKickstartFailures(t *testing.T) {
	m, _, _ := newTestLaunchd(t, nil)
	m.run = func(name string, args ...string) ([]byte, error) {
		if len(args) == 0 {
			return nil, nil
		}
		switch args[0] {
		case "bootstrap":
			return []byte("Load failed: 5: Input/output error"), errors.New("exit status 5")
		case "kickstart":
			return []byte("Could not find service in domain"), errors.New("exit status 113")
		default:
			return []byte("ok"), nil
		}
	}
	err := m.Start()
	if err == nil {
		t.Fatal("bootstrap 与 kickstart 都失败时 Start 必须报错")
	}
	for _, want := range []string{
		"Load failed: 5: Input/output error",
		"Could not find service in domain",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误必须保留两次尝试的原文 %q，得到: %v", want, err)
		}
	}
}

// Restart 发 SIGTERM，不发 kickstart -k。
//
// why：kickstart -k 是 SIGKILL，会把在途任务砍在半路；SIGTERM 走的是
// agentd 自己那条优雅关停（停收新连接→等在途请求→按序收尾），
// 而 KeepAlive=true 保证它随后被拉回来。
func TestLaunchdRestartSendsSigtermNotKickstartK(t *testing.T) {
	m, calls, _ := newTestLaunchd(t, nil)
	pid := 100
	m.run = func(name string, args ...string) ([]byte, error) {
		*calls = append(*calls, name+" "+strings.Join(args, " "))
		if len(args) > 0 && args[0] == "print" {
			return []byte(fmt.Sprintf("state = running\n\tpid = %d", pid)), nil
		}
		if len(args) > 0 && args[0] == "kill" {
			pid = 200 // 模拟被 KeepAlive 拉起的新实例
		}
		return []byte("ok"), nil
	}
	if err := m.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	joined := strings.Join(*calls, " | ")
	if !strings.Contains(joined, "kill SIGTERM") {
		t.Errorf("Restart 必须发 kill SIGTERM: %s", joined)
	}
	if strings.Contains(joined, "kickstart -k") {
		t.Error("Restart 不得用 kickstart -k：那是 SIGKILL，会把在途任务砍在半路")
	}
}

// 复核判据是 pid 变了，不是「还在跑」。
//
// why：launchd 的重启是异步的，kill 返回时旧进程可能还没死。只查
// 「在不在跑」的话，「什么都没发生」和「重启成功」长得一模一样。
func TestLaunchdRestartFailsWhenPidUnchanged(t *testing.T) {
	m, _, _ := newTestLaunchd(t, nil)
	m.run = func(name string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "print" {
			return []byte("state = running\n\tpid = 100"), nil
		}
		return []byte("ok"), nil
	}
	err := m.Restart()
	if err == nil {
		t.Fatal("pid 没变时 Restart 必须报错——那说明它根本没被重启")
	}
	if !strings.Contains(err.Error(), "100") {
		t.Errorf("报错应带上没变的那个 pid，便于排障: %v", err)
	}
}

// 没在跑时 Restart 等价于 Start：用户在 agentd 崩着的时候敲 restart，
// 要的是它起来，而不是一句「它没在跑」。语义与 systemctl restart 对齐。
func TestLaunchdRestartOnStoppedServiceStarts(t *testing.T) {
	m, calls, _ := newTestLaunchd(t, nil)
	loaded := false
	m.run = func(name string, args ...string) ([]byte, error) {
		*calls = append(*calls, name+" "+strings.Join(args, " "))
		if len(args) > 0 && args[0] == "print" {
			if !loaded {
				return []byte(""), errors.New("exit status 113")
			}
			return []byte("state = running\n\tpid = 7"), nil
		}
		if len(args) > 0 && args[0] == "bootstrap" {
			loaded = true
		}
		return []byte("ok"), nil
	}
	if err := m.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	joined := strings.Join(*calls, " | ")
	if !strings.Contains(joined, "bootstrap") {
		t.Errorf("没在跑时 Restart 应走 Start（enable+bootstrap）: %s", joined)
	}
}

// print 查不到但 plist 还在 => 已安装、未运行。
//
// why 承重：Stop 会 bootout（卸载 job）但保留 plist。若 Installed 按
// launchctl print 判，stop 之后 start 会被「没装」硬拒——
// 「停到显式 start」当场自相矛盾。
func TestLaunchdStatusInstalledWhenPlistExistsButNotLoaded(t *testing.T) {
	m, _, _ := newTestLaunchd(t, errors.New("exit status 113"))
	st, err := m.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Installed {
		t.Error("plist 还在就算已安装——bootout 只卸载 job，不删 plist")
	}
	if st.Running {
		t.Error("print 查不到时不该报在跑")
	}
}

// 两种 print-disabled 输出格式都要认。
//
// why：macOS 26 打的是 => disabled/enabled，更早的系统打的是 => true/false。
// 只认一种，会在另一种系统上把「已停用」读成「启用」，
// status 于是给出错误的处置建议。
func TestLaunchdStatusDisabledBothFormats(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		want bool
	}{
		{"新格式-已停用", "\tdisabled services = {\n\t\t\"" + LaunchdLabel + "\" => disabled\n\t}", true},
		{"新格式-已启用", "\tdisabled services = {\n\t\t\"" + LaunchdLabel + "\" => enabled\n\t}", false},
		{"旧格式-已停用", "\tdisabled services = {\n\t\t\"" + LaunchdLabel + "\" => true\n\t}", true},
		{"旧格式-已启用", "\tdisabled services = {\n\t\t\"" + LaunchdLabel + "\" => false\n\t}", false},
		{"从未出现过", "\tdisabled services = {\n\t\t\"com.other\" => disabled\n\t}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _ := newTestLaunchd(t, nil)
			m.run = func(name string, args ...string) ([]byte, error) {
				if len(args) > 0 && args[0] == "print-disabled" {
					return []byte(tc.out), nil
				}
				return []byte("ok"), nil
			}
			st, err := m.Status()
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if st.Disabled != tc.want {
				t.Errorf("Disabled=%v, want %v（输出: %q）", st.Disabled, tc.want, tc.out)
			}
		})
	}
}

// Install 也必须 enable，否则 stop 过的机器再也没法装回来。
//
// why：Stop 用 launchctl disable 把 target 写进了停用清单，而 Install 的
// bootstrap 对停用的 target 会直接拒。Install 失败会回滚删掉刚写的 plist
// ——于是 stop 之后跑 install，用户会看到「装不上」而且 plist 也没了。
func TestLaunchdInstallEnablesBeforeBootstrap(t *testing.T) {
	m, calls, _ := newTestLaunchd(t, nil)
	if err := m.Install(Spec{BinPath: "/opt/bin/handoff", ConfigPath: "/c.yaml", LogPath: "/l.log"}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	joined := strings.Join(*calls, " | ")
	ei, bi := strings.Index(joined, "enable"), strings.Index(joined, "bootstrap")
	if ei < 0 {
		t.Fatalf("Install 必须发出 enable（否则 stop 过的 target 装不回来）: %s", joined)
	}
	if ei > bi {
		t.Errorf("enable 必须先于 bootstrap: %s", joined)
	}
}

// plist 的内容是这条路上最容易写错又最难发现的东西，逐项钉住。
func TestLaunchdPlistContent(t *testing.T) {
	m, _, written := newTestLaunchd(t, nil)
	err := m.Install(Spec{BinPath: "/opt/bin/handoff", ConfigPath: "/home/u/.handoff/config.yaml", LogPath: "/home/u/.handoff/agentd.log"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	body := string((*written)["/home/u/Library/LaunchAgents/dev.gosuper.handoff.agentd.plist"])
	if body == "" {
		t.Fatal("plist 没被写出来")
	}
	for _, want := range []string{
		"<string>dev.gosuper.handoff.agentd</string>",
		"<string>/opt/bin/handoff</string>",
		"<string>agentd</string>",
		"<key>KeepAlive</key>",
		"<key>RunAtLoad</key>",
		"/home/u/.handoff/agentd.log",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("plist 缺少 %q:\n%s", want, body)
		}
	}
	// 两条禁止项，各有各的理由，都不能出现
	if strings.Contains(body, "AbandonProcessGroup") {
		t.Error("plist 不得含 AbandonProcessGroup：P1 探针已实测 setsid 的执行者本就活得过重启（spec §7.1），写上它等于给一条被证伪的假设留痕迹")
	}
	if strings.Contains(body, "--executor") {
		t.Error("plist 不得含 --executor：它只覆盖 cfg.Executor.Default，写死在单元里会让「改配置不生效」变成隐蔽坑（spec D5）")
	}
}

// 安装要按 bootout（清旧）→ 写盘 → bootstrap（加载）→ print（复核）的次序走。
//
// why 要复核：写盘 + bootstrap 成功不代表进程真起来了（二进制路径错、
// 端口被占都会让它起来即死）。不复核就报「安装成功」，用户会去查一个
// 根本不存在的服务。
func TestLaunchdInstallSequence(t *testing.T) {
	m, calls, _ := newTestLaunchd(t, nil)
	if err := m.Install(Spec{BinPath: "/opt/bin/handoff", ConfigPath: "/c.yaml", LogPath: "/l.log"}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	joined := strings.Join(*calls, " | ")
	for _, want := range []string{"bootout", "bootstrap", "print"} {
		if !strings.Contains(joined, want) {
			t.Errorf("调用序列缺 %q: %s", want, joined)
		}
	}
	if i, j := strings.Index(joined, "bootstrap"), strings.Index(joined, "print"); i > j {
		t.Errorf("bootstrap 必须先于 print（复核）: %s", joined)
	}
}

// bootstrap 失败必须回滚（删掉刚写的 plist），并把真因带出来。
//
// why 回滚：留下一个加载不了的 plist，下次登录 launchd 还会尝试加载它并
// 反复失败，用户却以为自己从没装过这个服务。
func TestLaunchdInstallRollsBackOnFailure(t *testing.T) {
	calls := []string{}
	written := map[string][]byte{}
	m := &launchdManager{
		log:      testLogger(),
		homeDir:  func() (string, error) { return "/home/u", nil },
		plistDir: "/home/u/Library/LaunchAgents",
		mkdirAll: func(string, os.FileMode) error { return nil },
		run: func(name string, args ...string) ([]byte, error) {
			calls = append(calls, strings.Join(args, " "))
			if len(args) > 0 && args[0] == "bootstrap" {
				return []byte("Load failed: 5: Input/output error"), errors.New("exit status 5")
			}
			return []byte("ok"), nil
		},
		writeFile: func(p string, b []byte, _ uint32) error { written[p] = b; return nil },
		remove:    func(p string) error { delete(written, p); return nil },
	}
	err := m.Install(Spec{BinPath: "/opt/bin/handoff", ConfigPath: "/c.yaml", LogPath: "/l.log"})
	if err == nil {
		t.Fatal("bootstrap 失败时 Install 应报错")
	}
	if !strings.Contains(err.Error(), "Load failed") {
		t.Errorf("报错应带上 launchctl 的原文（真因），得到: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("失败后应回滚删掉 plist，却还剩 %d 个文件", len(written))
	}
}

// Status 在 print 成功时报已安装且在跑。
func TestLaunchdStatusRunning(t *testing.T) {
	m, _, _ := newTestLaunchd(t, nil)
	st, err := m.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Installed || !st.Running {
		t.Fatalf("print 成功时应报已安装且在跑，得到 %+v", st)
	}
}

// print 失败（job 未注册）时报未安装，且**不返回错误**——「没装」是一个
// 正常答案，不是查询失败。
func TestLaunchdStatusNotInstalled(t *testing.T) {
	m, _, _ := newTestLaunchd(t, errors.New("exit status 113"))
	m.stat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	st, err := m.Status()
	if err != nil {
		t.Fatalf("未注册不该当成查询失败: %v", err)
	}
	if st.Installed || st.Running {
		t.Fatalf("应报未安装，得到 %+v", st)
	}
}

func TestLaunchdKind(t *testing.T) {
	m, _, _ := newTestLaunchd(t, nil)
	if m.Kind() != "launchd" {
		t.Fatalf("Kind()=%q", m.Kind())
	}
}
