// Windows 实现的测试：Task Scheduler XML 内容与 schtasks 调用序列都在这里钉住。
//
// 全部经缝注入，不真的调 schtasks、不真的写 %LOCALAPPDATA%——测试跑完机器上
// 不会多出任何计划任务。本文件不带 build tag：windows.go 全平台编译
// （同 launchd.go / systemd.go），测试因此在 mac/Linux 上也能跑。
package service

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

// fromUTF16LE 把 toUTF16LE 的产物解回字符串，供断言 XML 内容用。
func fromUTF16LE(b []byte) string {
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		b = b[2:]
	}
	codes := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		codes = append(codes, uint16(b[i])|uint16(b[i+1])<<8)
	}
	return string(utf16.Decode(codes))
}

// XML 的内容是这条路上最容易写错又最难发现的东西，逐项钉住。
// 四项承重配置任一缺失都会静默失效，测试是唯一的防线。
func TestWindowsTaskXMLContent(t *testing.T) {
	m := &windowsManager{log: testLogger()}
	body := m.taskXML(Spec{
		BinPath:    `C:\Users\u\.local\bin\handoff.exe`,
		ConfigPath: `C:\Users\u\.handoff\config.yaml`,
		LogPath:    `C:\Users\u\.handoff\agentd.log`,
	}, `WIN-B37\Administrator`)

	for _, want := range []string{
		"<Interval>PT1M</Interval>",
		"<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>",
		"<BootTrigger>",
		"<DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>",
		"<StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>",
		`<Command>C:\Users\u\.local\bin\handoff.exe</Command>`,
		"<Arguments>agentd --config C:\\Users\\u\\.handoff\\config.yaml</Arguments>",
		"<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>",
		"<UserId>WIN-B37\\Administrator</UserId>",
		"<LogonType>S4U</LogonType>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("XML 缺少 %q:\n%s", want, body)
		}
	}

	if strings.Contains(strings.ToLower(body), "cmd.exe") {
		t.Error("XML 不得套 cmd.exe：D8 实测它让 /end 只杀外层，agentd 孙进程原样活着，管理器视图与现实分叉")
	}
}

// schtasks /Create /XML 要求 UTF-16LE；这条断言钉住编码与 BOM。
func TestWindowsXMLIsUTF16LEWithBOM(t *testing.T) {
	got := toUTF16LE("<Task>")
	if len(got) < 2 || got[0] != 0xFF || got[1] != 0xFE {
		t.Fatalf("缺少 UTF-16LE BOM，前两字节=%v", got[:min(2, len(got))])
	}
	if s := fromUTF16LE(got); s != "<Task>" {
		t.Fatalf("往返不一致：%q", s)
	}
	if len(got) != 14 {
		t.Fatalf("UTF-16LE 长度应为 14，实际 %d", len(got))
	}
}

// XML 特殊字符必须转义，否则路径里一个 & 就让 schtasks 拒绝整个文件。
func TestWindowsTaskXMLEscapes(t *testing.T) {
	m := &windowsManager{log: testLogger()}
	body := m.taskXML(Spec{BinPath: `C:\a&b\handoff.exe`}, "u")
	if strings.Contains(body, `C:\a&b\handoff.exe`) {
		t.Error("路径里的 & 未转义，schtasks 会拒绝整个 XML")
	}
	if !strings.Contains(body, "&amp;") {
		t.Errorf("未见转义后的 &amp;:\n%s", body)
	}
}

// newTestWindows 造一个全缝替换的 windows manager，并返回记录调用的切片指针。
func newTestWindows(t *testing.T, runOut string, runErr error) (*windowsManager, *[]string, *map[string][]byte) {
	t.Helper()
	calls := []string{}
	written := map[string][]byte{}
	m := &windowsManager{
		log:          testLogger(),
		localAppData: `C:\Users\u\AppData\Local`,
		currentUser:  func() (string, error) { return `WIN-B37\Administrator`, nil },
		mkdirAll:     func(string, os.FileMode) error { return nil },
		run: func(name string, args ...string) ([]byte, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			// Install 末尾的复核会走 Status()，它要问两个东西：任务的 PID
			// （schtasks /V /FO CSV）与该 PID 是否存活（tasklist）。测试默认
			// 让这两问都答“在跑”，否则每个 Install 用例都要等满复核窗口才失败
			joined := strings.Join(args, " ")
			if name == "schtasks" && strings.Contains(joined, "/FO CSV") {
				return []byte("\"TaskName\",\"Status\",\"PID\"\n\"\\handoff-agentd\",\"Running\",\"4242\"\n"), runErr
			}
			if name == "tasklist" {
				return []byte("handoff.exe 4242 Console 1 40,000 K\n"), nil
			}
			return []byte(runOut), runErr
		},
		writeFile: func(p string, b []byte, _ uint32) error { written[p] = b; return nil },
		remove:    func(p string) error { delete(written, p); return nil },
		// 不真的睡：复核窗口是 5 秒，真睡会让本包单测从毫秒级变成分钟级
		sleep: func(time.Duration) {},
	}
	return m, &calls, &written
}

// 安装要按删旧 → 写盘 → 建任务 → 复核的次序走。
func TestWindowsInstallSequence(t *testing.T) {
	m, calls, written := newTestWindows(t, "SUCCESS", nil)
	if err := m.Install(Spec{BinPath: `C:\bin\handoff.exe`, ConfigPath: `C:\c.yaml`}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	// 前四条的顺序是承重的：先删旧（同名任务在时 /Create 会失败）、再建、
	// 再复核注册、最后**启动**。启动那一条曾经整个缺失，见下面的专项测试
	want := []string{"/Delete", "/Create", "/Query", "/Run"}
	if len(*calls) < len(want) {
		t.Fatalf("调用条数不足 %d，实际 %d: %v", len(want), len(*calls), *calls)
	}
	for i, w := range want {
		if !strings.Contains((*calls)[i], w) {
			t.Errorf("第 %d 条调用应含 %q，实际 %q", i+1, w, (*calls)[i])
		}
	}
	xmlPath := `C:\Users\u\AppData\Local\handoff\handoff-agentd.xml`
	if _, ok := (*written)[xmlPath]; !ok {
		t.Fatalf("XML 没写到 %s，实际写了 %v", xmlPath, keysOf(*written))
	}
	if b := (*written)[xmlPath]; len(b) < 2 || b[0] != 0xFF || b[1] != 0xFE {
		t.Error("落盘的 XML 不是 UTF-16LE BOM 开头，schtasks 会拒绝它")
	}
}

// 建任务失败必须回滚，避免留下让人工误判安装状态的孤儿 XML。
func TestWindowsInstallRollsBackOnCreateFailure(t *testing.T) {
	calls := []string{}
	written := map[string][]byte{}
	m := &windowsManager{
		log:          testLogger(),
		localAppData: `C:\Users\u\AppData\Local`,
		currentUser:  func() (string, error) { return "u", nil },
		mkdirAll:     func(string, os.FileMode) error { return nil },
		run: func(name string, args ...string) ([]byte, error) {
			joined := strings.Join(args, " ")
			calls = append(calls, joined)
			if strings.Contains(joined, "/Create") {
				return []byte("ERROR: Access is denied."), errors.New("exit status 1")
			}
			return []byte("SUCCESS"), nil
		},
		writeFile: func(p string, b []byte, _ uint32) error { written[p] = b; return nil },
		remove:    func(p string) error { delete(written, p); return nil },
	}
	err := m.Install(Spec{BinPath: `C:\bin\handoff.exe`})
	if err == nil {
		t.Fatal("建任务失败时 Install 应该报错")
	}
	if !strings.Contains(err.Error(), "Access is denied") {
		t.Errorf("报文必须带 schtasks 原文，实际: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("失败后必须回滚删除 XML，实际残留 %v", keysOf(written))
	}
}

// Status 的 Running 必须按 PID 复核，不能按镜像名。
func TestWindowsStatusVerifiesByPID(t *testing.T) {
	var tasklistFilter string
	m, _, _ := newTestWindows(t, "", nil)
	m.run = func(name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if name == "schtasks" {
			return []byte("\"TaskName\",\"Status\",\"PID\"\n\"\\handoff-agentd\",\"Running\",\"4242\"\n"), nil
		}
		tasklistFilter = joined
		return []byte("handoff.exe                   4242 Console      1     40,000 K\n"), nil
	}
	st, err := m.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Installed || !st.Running {
		t.Fatalf("应报已装且在跑，实际 %+v", st)
	}
	if !strings.Contains(tasklistFilter, "PID eq 4242") {
		t.Errorf("复核判据必须是 PID，实际 tasklist 参数: %q", tasklistFilter)
	}
	if strings.Contains(tasklistFilter, "IMAGENAME") {
		t.Error("不得按镜像名复核：会把操作者正在敲的 handoff CLI 也数进去")
	}
}

// 没装是正常答案不是错误。
func TestWindowsStatusNotInstalledIsNotError(t *testing.T) {
	m, _, _ := newTestWindows(t, "ERROR: The system cannot find the file specified.", errors.New("exit status 1"))
	st, err := m.Status()
	if err != nil {
		t.Fatalf("没装时 Status 不该报错: %v", err)
	}
	if st.Installed || st.Running {
		t.Fatalf("没装时两个字段都该是 false，实际 %+v", st)
	}
}

// 本来就没装时 Uninstall 返回 nil（幂等）。
func TestWindowsUninstallIsIdempotent(t *testing.T) {
	m, _, _ := newTestWindows(t, "ERROR: The system cannot find the file specified.", errors.New("exit status 1"))
	m.remove = func(string) error { return os.ErrNotExist }
	if err := m.Uninstall(); err != nil {
		t.Fatalf("没装时 Uninstall 应返回 nil，实际 %v", err)
	}
}

// 卸载不得依赖 schtasks /End，它只杀外层 cmd.exe。
func TestWindowsUninstallDoesNotUseEnd(t *testing.T) {
	m, calls, _ := newTestWindows(t, "SUCCESS", nil)
	if err := m.Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	for _, c := range *calls {
		if strings.Contains(c, "/End") {
			t.Errorf("不得用 schtasks /End，实际调用: %q", c)
		}
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Install 必须真的把服务启动起来，而不是建完任务就返回。
//
// Manager 接口对 Install 的约定是「生成单元、写盘、加载、启动，并复核真的起来了」。
// launchd 靠 plist 的 RunAtLoad 在 bootstrap 时自动拉起；而 Windows 的 BootTrigger
// 要等下次开机、LogonTrigger 要等下次登录——少了 /Run，install 会「成功」返回而
// agentd 从未运行。2026-08-18 真机对照时发现的实缺陷，这条测试是它的防线。
func TestWindowsInstallActuallyStartsService(t *testing.T) {
	m, calls, _ := newTestWindows(t, "SUCCESS", nil)
	if err := m.Install(Spec{BinPath: `C:\bin\handoff.exe`}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	var ran bool
	for _, c := range *calls {
		if strings.Contains(c, "/Run") {
			ran = true
		}
	}
	if !ran {
		t.Fatalf("Install 必须调 schtasks /Run 启动任务，否则 agentd 要等下次开机才起来。实际调用: %v", *calls)
	}
}

// 启动了但进程没起来，Install 必须失败——报「安装成功」会让操作者
// 去查一个并不存在的服务。
func TestWindowsInstallFailsWhenProcessNeverAppears(t *testing.T) {
	m, _, _ := newTestWindows(t, "SUCCESS", nil)
	// tasklist 查不到那个 pid：模拟起来即死（二进制路径错、端口被占、配置读不出）
	m.run = func(name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if name == "schtasks" && strings.Contains(joined, "/FO CSV") {
			return []byte("\"TaskName\",\"Status\",\"PID\"\n\"\\handoff-agentd\",\"Ready\",\"0\"\n"), nil
		}
		if name == "tasklist" {
			return []byte("信息: 没有运行的任务匹配指定标准。\n"), nil
		}
		return []byte("SUCCESS"), nil
	}
	err := m.Install(Spec{BinPath: `C:\bin\handoff.exe`, LogPath: `C:\logs\agentd.log`})
	if err == nil {
		t.Fatal("进程从未出现时 Install 必须报错，不能报安装成功")
	}
	// 报文要把日志路径给出来——那是操作者下一步唯一能看的东西
	if !strings.Contains(err.Error(), `C:\logs\agentd.log`) {
		t.Errorf("报文应指向日志路径，实际: %v", err)
	}
}

// 触发器必须是 BootTrigger：执行机是无人值守服务器，LogonTrigger 意味着
// 重启后要等有人 RDP 登录 agentd 才起来，而那可能永远不发生。
func TestWindowsTaskXMLUsesBootTriggerNotLogon(t *testing.T) {
	m := &windowsManager{log: testLogger()}
	body := m.taskXML(Spec{BinPath: `C:\bin\handoff.exe`}, "u")
	if strings.Contains(body, "LogonTrigger") {
		t.Error("不得用 LogonTrigger：无人值守服务器上没有交互登录，agentd 将永不自启")
	}
	if !strings.Contains(body, "<BootTrigger>") {
		t.Errorf("必须用 BootTrigger:\n%s", body)
	}
}
