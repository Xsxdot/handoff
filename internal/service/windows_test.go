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
			// Install 末尾的复核会走 Status()，它读 schtasks 的详细输出并找
			// SCHED_S_TASK_RUNNING(267009)。默认让它答“在跑”，否则每个
			// Install 用例都要等满复核窗口才失败
			if name == "schtasks" && strings.Contains(joined, "/FO LIST") {
				return []byte("TaskName: \\handoff-agentd\r\nLast Result: 267009\r\n"), runErr
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

// Running 判据必须是 SCHED_S_TASK_RUNNING(267009) 这个**数值**，
// 不能是 Status 列的文本、也不能是 PID。
//
// 三条都是 2026-08-18 真机实测钉下来的：schtasks 的 28 个字段里根本没有 PID 列；
// 字段名与 Status 取值都会随系统语言变化（中文机器是「正在运行」）；而进程侧
// 区分不开 agentd 与操作者正在敲的 handoff CLI（同一个镜像名，tasklist 不给命令行）。
func TestWindowsStatusUsesLocaleProofRunningCode(t *testing.T) {
	var queried string
	m, _, _ := newTestWindows(t, "", nil)
	m.run = func(name string, args ...string) ([]byte, error) {
		queried = name + " " + strings.Join(args, " ")
		// 真机原文的形状：字段名英文，值里带 267009
		return []byte("TaskName:      \\handoff-agentd\r\nStatus:        Running\r\nLast Result:   267009\r\n"), nil
	}
	st, err := m.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Installed || !st.Running {
		t.Fatalf("应报已装且在跑，实际 %+v", st)
	}
	if strings.Contains(queried, "tasklist") {
		t.Error("不得再走 tasklist：agentd 与 handoff CLI 同名，按镜像名判定必然假阳性")
	}
}

// 中文系统上字段名与 Status 取值都会本地化，判据仍须成立——
// 这条是「换一台机器就静默失效」的防线。
func TestWindowsStatusWorksOnLocalizedOutput(t *testing.T) {
	m, _, _ := newTestWindows(t, "", nil)
	m.run = func(string, ...string) ([]byte, error) {
		return []byte("任务名:    \\handoff-agentd\r\n状态:      正在运行\r\n上次结果:  267009\r\n"), nil
	}
	st, err := m.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Running {
		t.Fatal("中文输出下 Running 判据必须照样成立（判的是数值 267009，不是文本）")
	}
}

// 任务注册了但没在跑（上次结果是真实退出码）时，Running 必须为 false。
func TestWindowsStatusNotRunningWhenExited(t *testing.T) {
	m, _, _ := newTestWindows(t, "", nil)
	m.run = func(string, ...string) ([]byte, error) {
		return []byte("TaskName:      \\handoff-agentd\r\nStatus:        Ready\r\nLast Result:   0\r\n"), nil
	}
	st, err := m.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Installed {
		t.Fatal("查得到就是已装")
	}
	if st.Running {
		t.Fatal("上次结果不是 267009 时不得报在跑")
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

// 卸载必须先 /End 停掉 agentd，再 /Delete 删任务——顺序是承重的。
//
// 这条测试取代了原来的「不得用 /End」（它编码的是 D8 时代的前提：手搓任务套了
// `cmd.exe /c`，调度器跟踪的是 cmd，/End 只杀外层、agentd 孙进程原样活着）。
// 本实现不套 cmd，任务的动作进程直接就是 handoff.exe，/End 精确命中；而且这是
// 唯一能精确命中的办法——agentd 与操作者正在敲的 handoff CLI 同一个镜像名，
// 按镜像名杀会把发出卸载命令的 CLI 自己一起杀掉。
//
// 2026-08-18 win-b37 实测漏了这一步的后果：任务与 XML 都删干净了，agentd 活过
// 75 秒仍在，而 Uninstall 报的是「agentd 不再被自动拉起」；随后 install 又因为
// 它占着 DataDir 锁而失败，机器停在一个「没托管、装不上、还在跑」的状态。
func TestWindowsUninstallEndsBeforeDelete(t *testing.T) {
	m, calls, _ := newTestWindows(t, "SUCCESS", nil)
	if err := m.Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	endAt, delAt := -1, -1
	for i, c := range *calls {
		if strings.Contains(c, "/End") && endAt < 0 {
			endAt = i
		}
		if strings.Contains(c, "/Delete") && delAt < 0 {
			delAt = i
		}
	}
	if endAt < 0 {
		t.Fatalf("必须先 /End 停掉 agentd，否则它会变成没人托管也没人知道的孤儿进程；实际调用 %v", *calls)
	}
	if delAt < 0 {
		t.Fatalf("必须 /Delete 删掉任务，实际调用 %v", *calls)
	}
	if endAt > delAt {
		t.Errorf("顺序反了：先删任务，调度器就不再认识那个进程，agentd 会活下来。实际 %v", *calls)
	}
}

// /End 失败（本来就没在跑）不得中断卸载：那会让「任务已删、XML 还在」半途而废。
func TestWindowsUninstallIgnoresEndFailure(t *testing.T) {
	calls := []string{}
	m := &windowsManager{
		log:          testLogger(),
		localAppData: `C:\Users\u\AppData\Local`,
		currentUser:  func() (string, error) { return "u", nil },
		run: func(_ string, args ...string) ([]byte, error) {
			joined := strings.Join(args, " ")
			calls = append(calls, joined)
			if strings.Contains(joined, "/End") {
				return []byte("ERROR: The system cannot find the file specified."), errors.New("exit status 1")
			}
			return []byte("SUCCESS"), nil
		},
		remove: func(string) error { return nil },
	}
	if err := m.Uninstall(); err != nil {
		t.Fatalf("/End 失败不该让 Uninstall 失败，实际 %v", err)
	}
	if len(calls) < 2 || !strings.Contains(calls[1], "/Delete") {
		t.Errorf("/End 失败后仍须继续删任务，实际调用 %v", calls)
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
		if name == "schtasks" && strings.Contains(joined, "/FO LIST") {
			// 起来即死：上次结果是真实退出码而不是 SCHED_S_TASK_RUNNING
			return []byte("TaskName: \\handoff-agentd\r\nLast Result: 1\r\n"), nil
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

// 每分钟的重复触发必须挂在 TimeTrigger 上——这是 agentd 换版后被拉起的唯一依靠。
//
// 2026-08-18 win-b37 实测：重复挂 BootTrigger 时，`schtasks /Query /V` 的
// Next Run Time 恒为 N/A，杀掉 agentd 后 150 秒没有被拉回。原因是 BootTrigger 的
// 重复序列锚定开机时刻，任务在开机之后注册就永远不会激活它。
// 这个缺陷在代码里看不出来（XML 完全合法、/Create 也成功），只能靠这条测试
// 把「重复归谁」钉死。
func TestWindowsTaskXMLRepetitionIsOnTimeTriggerNotBoot(t *testing.T) {
	m := &windowsManager{log: testLogger()}
	body := m.taskXML(Spec{BinPath: `C:\bin\handoff.exe`}, "u")

	boot := between(t, body, "<BootTrigger>", "</BootTrigger>")
	if strings.Contains(boot, "<Repetition>") {
		t.Errorf("重复触发不得挂在 BootTrigger 上（锚定开机时刻，注册在开机之后就永不激活）:\n%s", body)
	}

	if !strings.Contains(body, "<TimeTrigger>") {
		t.Fatalf("必须有 TimeTrigger 承载每分钟重复:\n%s", body)
	}
	tt := between(t, body, "<TimeTrigger>", "</TimeTrigger>")
	if !strings.Contains(tt, "<Interval>PT1M</Interval>") {
		t.Errorf("TimeTrigger 缺少 PT1M 重复:\n%s", tt)
	}
	if !strings.Contains(tt, "<StartBoundary>") {
		t.Errorf("TimeTrigger 缺少 StartBoundary，调度器推算不出下一次运行:\n%s", tt)
	}
	// 有 Duration 就是有限重复：到期后任务静默失去自愈能力，且没有任何报错
	if strings.Contains(tt, "<Duration>") {
		t.Errorf("重复不得设 Duration（省略即无限重复），否则到期后静默失去自愈:\n%s", tt)
	}
}

// between 取出 open 与 close 之间的片段；缺任一端直接 Fatal，避免断言落在空串上恒过。
func between(t *testing.T, s, open, close string) string {
	t.Helper()
	i := strings.Index(s, open)
	if i < 0 {
		t.Fatalf("找不到 %q:\n%s", open, s)
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		t.Fatalf("找不到 %q:\n%s", close, s)
	}
	return rest[:j]
}

// 托管判据：任务没注册、或登记的是别的二进制，都必须判否（fail-closed）。
//
// 后一种不是假想：验收期间机器上同时存在 C:\Tools\handoff\handoff.exe（任务
// 登记的）与临时工作树里的构建。跑后者时若判成托管，换版会换掉没人运行的
// 那个文件，upgrade 报成功而机器上跑的还是旧版——静默且难查。
func TestWindowsUnitReferencesJudgesByRegisteredBinary(t *testing.T) {
	const listOut = `Folder: \
HostName:      IZQPFBDZJZ8GQFZ
TaskName:      \handoff-agentd
Task To Run:   C:\Tools\handoff\handoff.exe agentd --config C:\Users\u\.handoff\config.yaml
Last Result:   267009
`
	cases := []struct {
		name    string
		exe     string
		runErr  error
		want    bool
		wantWhy string
	}{
		{name: "登记的就是本进程", exe: `C:\Tools\handoff\handoff.exe`, want: true},
		{name: "大小写不同也算同一个", exe: `c:\tools\HANDOFF\Handoff.EXE`, want: true},
		{name: "登记的是别的二进制", exe: `C:\b142test\handoff.exe`, want: false, wantWhy: "登记的不是本进程的二进制"},
		{name: "任务没注册", exe: `C:\Tools\handoff\handoff.exe`, runErr: errors.New("ERROR: The system cannot find the file specified."), want: false, wantWhy: "未注册"},
		{name: "拿不到自己的路径", exe: "  ", want: false, wantWhy: "拿不到本进程"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &windowsManager{
				log: testLogger(),
				run: func(string, ...string) ([]byte, error) {
					if c.runErr != nil {
						return nil, c.runErr
					}
					return []byte(listOut), nil
				},
			}
			got, why := m.unitReferences(c.exe)
			if got != c.want {
				t.Fatalf("判据错：want=%v got=%v why=%q", c.want, got, why)
			}
			if c.wantWhy != "" && !strings.Contains(why, c.wantWhy) {
				t.Errorf("理由应说明 %q，实际 %q", c.wantWhy, why)
			}
			if c.want && why != "" {
				t.Errorf("判是时理由应为空，实际 %q", why)
			}
		})
	}
}
