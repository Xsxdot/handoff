// launchd 实现的测试：plist 内容与 launchctl 调用序列都在这里钉住。
//
// 全部经缝注入，不真的调 launchctl、不真的写 ~/Library——测试跑完机器上
// 不会多出任何服务。
package service

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
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
	}
	return m, &calls, &written
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
