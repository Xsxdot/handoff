// systemd 实现的测试。全部经缝注入，不真的调 systemctl、不真的写 /etc。
//
// 这些断言在 Linux 侧是唯一的防线：本仓库暂无 Linux 机器可做真机验证
// （spec §10），unit 内容写错在 macOS 上不会有任何症状。
package service

import (
	"errors"
	"strings"
	"testing"
)

func newTestSystemd(t *testing.T, runErr error) (*systemdManager, *[]string, *map[string][]byte) {
	t.Helper()
	calls := []string{}
	written := map[string][]byte{}
	m := &systemdManager{
		log:     testLogger(),
		unitDir: "/etc/systemd/system",
		user:    "alice",
		run: func(name string, args ...string) ([]byte, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			return []byte("ok"), runErr
		},
		writeFile: func(p string, b []byte, _ uint32) error { written[p] = b; return nil },
		remove:    func(p string) error { delete(written, p); return nil },
	}
	return m, &calls, &written
}

// unit 内容逐项钉住。两条硬要求各有各的理由，写错都不会在编译期暴露。
func TestSystemdUnitContent(t *testing.T) {
	m, _, written := newTestSystemd(t, nil)
	if err := m.Install(Spec{BinPath: "/usr/local/bin/handoff", ConfigPath: "/home/alice/.handoff/config.yaml"}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	body := string((*written)["/etc/systemd/system/handoff-agentd.service"])
	if body == "" {
		t.Fatal("unit 没被写出来")
	}
	if !strings.Contains(body, "KillMode=process") {
		t.Error("unit 必须含 KillMode=process：setsid 脱离了会话与进程组但改不了 cgroup 归属，默认的 control-group 会在重启时把执行者一并杀掉（B36 硬要求）")
	}
	if !strings.Contains(body, "Restart=always") {
		t.Error("unit 必须是 Restart=always 而非 on-failure：自更新换版靠 exit 0 交接，on-failure 在 exit 0 时不重启，服务会在换版后无声消失")
	}
	if strings.Contains(body, "Restart=on-failure") {
		t.Error("unit 不得含 Restart=on-failure")
	}
	if strings.Contains(body, "--executor") {
		t.Error("ExecStart 不得带 --executor（spec D5）")
	}
	if !strings.Contains(body, "User=alice") {
		t.Errorf("unit 应写实际用户名而不是占位符:\n%s", body)
	}
	if strings.Contains(body, "CHANGEME") || strings.Contains(body, "%i") {
		t.Error("unit 不得残留占位符：User= 空值会被 systemd 重置为 root，服务会以 root 静默跑起来")
	}
	if !strings.Contains(body, "/usr/local/bin/handoff agentd") {
		t.Errorf("ExecStart 路径不对:\n%s", body)
	}
}

// 安装序列：写盘 → daemon-reload → enable --now → is-active（复核）。
func TestSystemdInstallSequence(t *testing.T) {
	m, calls, _ := newTestSystemd(t, nil)
	if err := m.Install(Spec{BinPath: "/usr/local/bin/handoff", ConfigPath: "/c.yaml"}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	joined := strings.Join(*calls, " | ")
	for _, want := range []string{"daemon-reload", "enable", "is-active"} {
		if !strings.Contains(joined, want) {
			t.Errorf("调用序列缺 %q: %s", want, joined)
		}
	}
}

// 写 /etc 没权限时必须明确说「需要 sudo」，而不是把 permission denied 扁平抛出。
//
// why（B45 的教训）：真因只落在日志里等于没有。用户看到的是一句
// "open /etc/systemd/system/...: permission denied"，他不知道该 sudo 重跑。
func TestSystemdInstallSaysSudoOnPermissionError(t *testing.T) {
	m, _, _ := newTestSystemd(t, nil)
	m.writeFile = func(string, []byte, uint32) error { return errors.New("permission denied") }
	err := m.Install(Spec{BinPath: "/usr/local/bin/handoff", ConfigPath: "/c.yaml"})
	if err == nil {
		t.Fatal("写盘失败时应报错")
	}
	if !strings.Contains(err.Error(), "sudo") {
		t.Fatalf("报错必须提示需要 sudo，得到: %v", err)
	}
}

// enable 失败要回滚删掉 unit，并带出 systemctl 原文。
func TestSystemdInstallRollsBackOnFailure(t *testing.T) {
	calls := []string{}
	written := map[string][]byte{}
	m := &systemdManager{
		log:     testLogger(),
		unitDir: "/etc/systemd/system",
		user:    "alice",
		run: func(name string, args ...string) ([]byte, error) {
			calls = append(calls, strings.Join(args, " "))
			if len(args) > 0 && args[0] == "enable" {
				return []byte("Failed to enable unit: Unit file is masked."), errors.New("exit status 1")
			}
			return []byte("ok"), nil
		},
		writeFile: func(p string, b []byte, _ uint32) error { written[p] = b; return nil },
		remove:    func(p string) error { delete(written, p); return nil },
	}
	err := m.Install(Spec{BinPath: "/usr/local/bin/handoff", ConfigPath: "/c.yaml"})
	if err == nil {
		t.Fatal("enable 失败时应报错")
	}
	if !strings.Contains(err.Error(), "masked") {
		t.Errorf("报错应带 systemctl 原文，得到: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("失败后应回滚，却还剩 %d 个文件", len(written))
	}
}

// is-active 成功 = 在跑；失败 = 装了但没跑，且不算查询错误。
func TestSystemdStatus(t *testing.T) {
	m, _, _ := newTestSystemd(t, nil)
	st, err := m.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Installed || !st.Running {
		t.Fatalf("is-active 成功时应报在跑，得到 %+v", st)
	}

	m2, _, _ := newTestSystemd(t, errors.New("exit status 3"))
	st2, err := m2.Status()
	if err != nil {
		t.Fatalf("未激活不该当成查询失败: %v", err)
	}
	if st2.Running {
		t.Fatalf("is-active 失败时不该报在跑，得到 %+v", st2)
	}
}

func TestSystemdKind(t *testing.T) {
	m, _, _ := newTestSystemd(t, nil)
	if m.Kind() != "systemd" {
		t.Fatalf("Kind()=%q", m.Kind())
	}
}
