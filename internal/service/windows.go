// windows.go —— Windows 侧的服务托管实现（Task Scheduler 计划任务）。
//
// 为什么是计划任务而不是 SCM 服务：executor 的凭据全挂在用户 profile 下，
// SCM 服务默认跑在 Session 0 / SYSTEM，%USERPROFILE% 会变，用户态认证链路也
// 会随之失效。计划任务保留用户身份，并与其它平台的单元托管模型一致。
//
// 边界：
//   - 不加 //go:build windows：靠 New() 的 runtime.GOOS switch 分发，确保 XML
//     内容能在 macOS/Linux 上单测。
//   - 单元走 XML 而不是命令行参数：IgnoreNew 是承重配置，只能用 XML 表达。
//   - 不做日志重定向：Task Scheduler 没有 StandardOutPath 式的能力，agentd
//     自己负责日志落盘。
package service

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"time"
	"unicode/utf16"
)

// WindowsTaskName 是计划任务的名字，同时也是 XML 文件名的主干。
const WindowsTaskName = "handoff-agentd"

// installVerifyInterval / installVerifyAttempts 是 Install 复核「agentd 真的
// 起来了」的轮询节奏，合计 installVerifyWindow。
//
// 为什么轮询而不是睡一个固定值：schtasks /Run 只把启动请求交给计划任务服务，
// 返回时进程往往还没起来。常见机器上百毫秒即可，负载高时要几秒——固定睡值
// 要么白等要么误判。与 prochost 的 killVerifyBackoff 同一条纪律：异步生效的
// 动作必须复核，且复核要给够窗口。
const (
	installVerifyInterval = 500 * time.Millisecond
	installVerifyAttempts = 10
	installVerifyWindow   = installVerifyInterval * installVerifyAttempts
)

// windowsManager 是 Windows 实现。八个字段是测试缝。
type windowsManager struct {
	log          *slog.Logger
	localAppData string
	currentUser  func() (string, error)
	mkdirAll     func(string, os.FileMode) error
	run          func(string, ...string) ([]byte, error)
	writeFile    func(string, []byte, uint32) error
	remove       func(string) error
	// sleep 是复核轮询的等待缝：测试注入空实现，避免为了走完复核窗口
	// 真的睡 5 秒（那会让 service 包的单测从毫秒级变成分钟级）
	sleep func(time.Duration)
}

// newWindows 构造生产用的 Windows manager。
func newWindows(log *slog.Logger) *windowsManager {
	return &windowsManager{
		log:          log,
		localAppData: os.Getenv("LOCALAPPDATA"),
		currentUser: func() (string, error) {
			u, err := user.Current()
			if err != nil {
				return "", err
			}
			return u.Username, nil
		},
		mkdirAll: os.MkdirAll,
		run: func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).CombinedOutput()
		},
		writeFile: func(path string, data []byte, perm uint32) error {
			return os.WriteFile(path, data, os.FileMode(perm))
		},
		remove: os.Remove,
		sleep:  time.Sleep,
	}
}

// toUTF16LE 把字符串编码为带 BOM 的 UTF-16 LE 字节序列。
//
// schtasks /Create /XML 要求 UTF-16LE；喂 UTF-8 会报一个与编码无关的「文件无效」
// 错误，排查时几乎不可能从报错反推出编码问题。
func toUTF16LE(s string) []byte {
	codes := utf16.Encode([]rune(s))
	b := make([]byte, 0, 2+len(codes)*2)
	b = append(b, 0xFF, 0xFE)
	for _, c := range codes {
		b = append(b, byte(c), byte(c>>8))
	}
	return b
}

// esc 按 XML 文本规则转义字符串。
//
// 用户目录和可执行文件路径可以含有 & 或 < 等字符；不转义会让 schtasks 拒绝
// 整个任务定义，而且错误信息不会明确指出是哪个路径字符造成的。
func esc(s string) string {
	var buf bytes.Buffer
	if err := xml.EscapeText(&buf, []byte(s)); err != nil {
		return s
	}
	return buf.String()
}

// taskXML 渲染 Task Scheduler 的任务定义。
//
// 参数：spec 是要托管的 agentd 描述；user 是运行身份。
// 返回：XML 全文（落盘前由 toUTF16LE 转码）。
// 注意：重复触发与 IgnoreNew 一起模拟 KeepAlive，电池设置避免任务静默不启动，
// ExecutionTimeLimit 为零避免长跑 agentd 被掐掉，Command 直接指向 handoff.exe
// 以确保 schtasks 能跟踪真正的 agentd。
//
// **触发器用 BootTrigger 而不是 LogonTrigger**（08-18 真机对照后改正）。
// 执行机是无人值守的服务器，平时没有任何交互登录会话——LogonTrigger 意味着
// 机器重启后 agentd 要等到有人 RDP 登录才会起来，而那可能永远不发生。
// win-b37 上手搓的那份托管用的正是 BootTrigger + S4U，已长期实证可行。
// S4U 拿到的是无网络凭据的令牌，对 agentd 够用（它只需本地资源与出站连接）。
func (m *windowsManager) taskXML(spec Spec, user string) string {
	args := "agentd"
	if spec.ConfigPath != "" {
		args += " --config " + spec.ConfigPath
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-16"?>` + "\n")
	b.WriteString(`<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">` + "\n")
	b.WriteString("  <RegistrationInfo>\n    <Description>handoff agentd</Description>\n  </RegistrationInfo>\n")
	// 两个触发器分工明确，缺一不可：
	//   BootTrigger —— 开机即起，不必等第一次重复到点
	//   TimeTrigger —— 每分钟重复，配合 IgnoreNew 等价于 systemd 的 Restart=always
	//
	// 重复触发**必须**挂在 TimeTrigger 上，不能挂 BootTrigger（2026-08-18 实测）：
	// BootTrigger 的重复序列锚定在开机那一刻，任务若在开机之后才注册，这条序列
	// 在本次开机会话里从未激活过，`schtasks /Query /V` 的 Next Run Time 恒为 N/A，
	// 杀掉 agentd 后 150 秒也没有被拉回。而 agentd 换版时是**正常退出 0**，
	// RestartOnFailure 那类「失败才重启」的设定同样接不住——升级链的交接点就断在这里。
	//
	// StartBoundary 取一个固定的过去时刻（而非当前时间）：调度器会从它按间隔推算出
	// 下一次运行，写死可以让 XML 对同一份 Spec 稳定可复现，测试才能钉住它。
	// <Duration> 整个省掉即「无限重复」——写成有限值意味着到期后静默失去自愈能力。
	b.WriteString("  <Triggers>\n    <BootTrigger>\n      <Enabled>true</Enabled>\n    </BootTrigger>\n")
	b.WriteString("    <TimeTrigger>\n")
	b.WriteString("      <Repetition>\n        <Interval>PT1M</Interval>\n")
	b.WriteString("        <StopAtDurationEnd>false</StopAtDurationEnd>\n      </Repetition>\n")
	b.WriteString("      <StartBoundary>2020-01-01T00:00:00</StartBoundary>\n")
	b.WriteString("      <Enabled>true</Enabled>\n    </TimeTrigger>\n  </Triggers>\n")
	b.WriteString("  <Principals>\n    <Principal id=\"Author\">\n")
	b.WriteString("      <UserId>" + esc(user) + "</UserId>\n")
	b.WriteString("      <LogonType>S4U</LogonType>\n      <RunLevel>HighestAvailable</RunLevel>\n")
	b.WriteString("    </Principal>\n  </Principals>\n")
	b.WriteString("  <Settings>\n")
	b.WriteString("    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>\n")
	b.WriteString("    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>\n")
	b.WriteString("    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>\n")
	b.WriteString("    <StartWhenAvailable>true</StartWhenAvailable>\n")
	b.WriteString("    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>\n")
	b.WriteString("    <Enabled>true</Enabled>\n")
	b.WriteString("  </Settings>\n")
	b.WriteString("  <Actions Context=\"Author\">\n    <Exec>\n")
	b.WriteString("      <Command>" + esc(spec.BinPath) + "</Command>\n")
	b.WriteString("      <Arguments>" + esc(args) + "</Arguments>\n")
	b.WriteString("    </Exec>\n  </Actions>\n</Task>\n")
	return b.String()
}

// UnitPath 返回 XML 的落点。
func (m *windowsManager) UnitPath() (string, error) {
	if m.localAppData == "" {
		if m.log != nil {
			m.log.Error("取不到 LOCALAPPDATA，无法定位计划任务 XML 落点", "task", WindowsTaskName)
		}
		return "", fmt.Errorf("取不到 %%LOCALAPPDATA%%，无法定位计划任务 XML 的落点")
	}
	return windowsPathJoin(m.localAppData, "handoff", WindowsTaskName+".xml"), nil
}

// Kind 返回管理器种类。
func (m *windowsManager) Kind() string { return "schtasks" }

// windowsPathJoin 在所有宿主平台上都使用 Windows 分隔符。
//
// windows.go 不带 build tag，macOS/Linux 上的单测也会构造 Windows 路径；使用
// 宿主机 filepath.Join 会在测试中生成混合分隔符，掩盖实际交给 schtasks 的路径。
func windowsPathJoin(parts ...string) string {
	if len(parts) == 0 {
		return ""
	}
	out := strings.TrimRight(parts[0], `\/`)
	for _, part := range parts[1:] {
		part = strings.Trim(part, `\/`)
		if part == "" {
			continue
		}
		if out == "" {
			out = part
		} else {
			out += `\` + part
		}
	}
	return out
}

// windowsPathDir 返回 Windows 路径的父目录，供全平台测试调用注入的 mkdirAll。
func windowsPathDir(path string) string {
	i := strings.LastIndexAny(path, `/\`)
	if i < 0 {
		return "."
	}
	if i == 0 {
		return path[:1]
	}
	return path[:i]
}

// Install 写 XML 并建任务，最后复核任务真的注册上了。
//
// 次序与其它平台一致：先清旧 → 写盘 → 建任务 → 复核。建任务失败时回滚删 XML，
// 避免留下让人工误判安装状态的孤儿文件。
func (m *windowsManager) Install(spec Spec) error {
	path, err := m.UnitPath()
	if err != nil {
		return err
	}
	usr, err := m.currentUser()
	if err != nil {
		m.log.Error("取当前用户失败，无法确定计划任务的运行身份", "cause", err)
		return fmt.Errorf("取当前用户: %w", err)
	}
	m.log.Info("安装 Windows 计划任务", "task", WindowsTaskName, "xml", path,
		"bin", spec.BinPath, "user", usr)

	if out, derr := m.run("schtasks", "/Delete", "/TN", WindowsTaskName, "/F"); derr != nil {
		m.log.Debug("删除旧任务（未装时报错属正常）", "output", strings.TrimSpace(string(out)))
	}

	if err := m.mkdirAll(windowsPathDir(path), 0o755); err != nil {
		m.log.Error("创建 XML 目录失败", "dir", windowsPathDir(path), "cause", err)
		return fmt.Errorf("创建 %s: %w", windowsPathDir(path), err)
	}
	if err := m.writeFile(path, toUTF16LE(m.taskXML(spec, usr)), 0o644); err != nil {
		m.log.Error("写计划任务 XML 失败", "path", path, "cause", err)
		return fmt.Errorf("写 XML %s: %w", path, err)
	}

	if out, cerr := m.run("schtasks", "/Create", "/TN", WindowsTaskName, "/XML", path, "/F"); cerr != nil {
		if rmErr := m.remove(path); rmErr != nil {
			m.log.Error("回滚删除 XML 失败", "path", path, "cause", rmErr)
		}
		m.log.Error("建计划任务失败，已回滚", "cause", cerr, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("建计划任务失败: %s（%w）", strings.TrimSpace(string(out)), cerr)
	}

	if out, qerr := m.run("schtasks", "/Query", "/TN", WindowsTaskName); qerr != nil {
		m.log.Error("任务已建但复核失败", "cause", qerr, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("任务已建但复核不到（检查 %s）: %w", spec.LogPath, qerr)
	}

	// 建完任务必须**立刻启动它**。Manager 接口对 Install 的约定是「生成单元、
	// 写盘、加载、启动，并复核真的起来了」——launchd 靠 plist 里的 RunAtLoad
	// 在 bootstrap 时自动拉起，systemd 由 install 顺带 start。
	// 而 Windows 的 BootTrigger 要等下一次开机、LogonTrigger 要等下一次登录，
	// 两者都不会在 install 当下启动任何东西。少了这一步，install 会「成功」
	// 返回而 agentd 从未运行——那正是 Install 契约里最不能省的一半。
	if out, rerr := m.run("schtasks", "/Run", "/TN", WindowsTaskName); rerr != nil {
		m.log.Error("任务已建但启动失败", "cause", rerr, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("任务已建但启动失败: %s（%w）", strings.TrimSpace(string(out)), rerr)
	}

	// 复核「真的在跑」，而不是「注册上了」。/Run 只是把请求交给计划任务服务，
	// 二进制路径错、端口被占、配置读不出来都会让它起来即死——那种情况下
	// 报「安装成功」，操作者会去查一个并不存在的服务。
	// 轮询而不是睡一个固定值：进程拉起通常在百毫秒内，慢的机器上要几秒。
	var last Status
	for i := 0; i < installVerifyAttempts; i++ {
		m.sleep(installVerifyInterval)
		st, serr := m.Status()
		if serr == nil && st.Running {
			m.log.Info("Windows 计划任务安装完成并已运行",
				"task", WindowsTaskName, "probe", i+1)
			return nil
		}
		last = st
	}
	m.log.Error("任务已启动但复核窗口内未见进程存活",
		"task", WindowsTaskName, "window", installVerifyWindow,
		"installed", last.Installed, "detail", last.Detail)
	return fmt.Errorf("任务已建并已触发，但 %s 内未复核到 agentd 进程存活（检查 %s）",
		installVerifyWindow, spec.LogPath)
}

// Uninstall 删任务并删 XML。本来就没装时返回 nil。
//
// 不使用 schtasks /End：它只杀外层 cmd.exe，无法保证 agentd 孙进程退出。任务
// 删除先于进程回收，避免重复触发在一分钟内把已杀进程重新拉起。
func (m *windowsManager) Uninstall() error {
	path, err := m.UnitPath()
	if err != nil {
		return err
	}
	m.log.Info("卸载 Windows 计划任务", "task", WindowsTaskName)
	// 先停再删，顺序不能倒：先删任务的话，调度器就不再认识那个进程，
	// agentd 会活下来变成一个「没人托管、也没人知道它还在」的孤儿进程
	// （2026-08-18 win-b37 实测：只删不停，agentd 活过 75 秒仍在，
	// 而 Uninstall 报的是「agentd 不再被自动拉起」，读起来像已经停了；
	// 随后的 install 又因为它占着 DataDir 锁而失败）。
	//
	// 为什么这里可以用 /End，而 spec §2.5 当初拒绝它：D8 那次 /End 只杀掉外层
	// 是因为手搓任务套了 `cmd.exe /c`，调度器跟踪的是 cmd、agentd 是它的孙进程。
	// 本实现不套 cmd（§2.2 第 4 条），任务的动作进程直接就是 handoff.exe，
	// /End 精确命中。这也是唯一能精确命中的办法：agentd 与操作者正在敲的
	// handoff CLI 同一个镜像名，按名字杀会连自己一起杀掉。
	//
	// agentd 若是手工起的（不由本任务拉起），/End 杀不到它——那是诚实的结果：
	// 卸载摘掉的是托管，手工起的进程不归管理器处置。
	if out, eerr := m.run("schtasks", "/End", "/TN", WindowsTaskName); eerr != nil {
		m.log.Debug("停止任务报错（未装或本来就没在跑时属正常）",
			"output", strings.TrimSpace(string(out)))
	}
	if out, derr := m.run("schtasks", "/Delete", "/TN", WindowsTaskName, "/F"); derr != nil {
		m.log.Debug("删除任务报错（未装时属正常）", "output", strings.TrimSpace(string(out)))
	}
	if err := m.remove(path); err != nil && !os.IsNotExist(err) {
		m.log.Error("删除计划任务 XML 失败", "path", path, "cause", err)
		return fmt.Errorf("删除 XML %s: %w", path, err)
	}
	m.log.Info("Windows 计划任务已卸载", "task", WindowsTaskName)
	return nil
}

// Status 查询任务是否注册且在跑。
//
// **Running 判据是 schtasks 的「上次结果」等于 SCHED_S_TASK_RUNNING(267009)**，
// 不是 PID、也不是 Status 列的文本。三条理由，都是 2026-08-18 真机实测得来的：
//
//   - **schtasks 根本不给 PID。** `/Query /V /FO CSV` 的 28 个字段里没有任何一列
//     是进程号（起草时以为有，那是事实错误）。按 PID 复核在这里不可能实现
//   - **列名与 Status 列的取值都会本地化。** 英文机器是 `Status: Running`，
//     中文机器是「正在运行」；按列名或按文本匹配都会在换一台机器时静默失效。
//     而 267009 是个数值常量，跨语言不变
//   - **进程侧也区分不开。** tasklist 不给命令行，而 agentd 与操作者正在敲的
//     handoff CLI 是同一个镜像名——按镜像名判定在 Status 被 CLI 调用时必然
//     假阳性（调用者自己就是一个 handoff.exe）
//
// 由此接受一个明确的局限：本判据信任 schtasks 的运行态记录。D8 记录过它会与
// 现实分叉，但那个分叉的根因是手搓托管套了 cmd.exe（schtasks 只跟踪外层）；
// 本实现的 Command 直接指向 handoff.exe，同日实测 schtasks 报 Running 时
// 进程确实在、agentd 的 HTTP 面也确实在应答。
func (m *windowsManager) Status() (Status, error) {
	out, err := m.run("schtasks", "/Query", "/TN", WindowsTaskName, "/V", "/FO", "LIST")
	if err != nil {
		m.log.Debug("查询计划任务未命中（未装时属正常）", "output", strings.TrimSpace(string(out)))
		return Status{}, nil
	}
	s := Status{Installed: true, Detail: firstLine(string(out))}
	s.Running = taskIsRunning(string(out))
	m.log.Debug("计划任务状态", "task", WindowsTaskName,
		"installed", s.Installed, "running", s.Running)
	return s, nil
}

// schedTaskRunning 是 Win32 的 SCHED_S_TASK_RUNNING（0x41301），schtasks 在
// 「上次结果」一栏用它表示任务此刻正在运行。
//
// 用十进制字面量而不是 0x41301：schtasks 的输出就是十进制。
const schedTaskRunning = "267009"

// taskIsRunning 判断 schtasks 的详细输出是否表示任务正在运行。
//
// 参数：out 为 `schtasks /Query /V /FO LIST` 的原文
//
// 为什么是子串匹配而不是解析字段：字段名会随系统语言变化（英文
// `Last Result`、中文「上次结果」），按名字取值等于把判据绑死在一种语言上。
// 而 267009 这个数值只会出现在「上次结果」里——它既不是时间戳也不是路径，
// 误命中其它字段的可能性可以忽略。
func taskIsRunning(out string) bool {
	return strings.Contains(out, schedTaskRunning)
}

// UnitReferences 报告已注册的计划任务是否指向 exePath 这个二进制。
//
// 参数：
//   - log: 日志入口
//   - exePath: 调用方自己的可执行文件绝对路径（须先 EvalSymlinks）
//
// 返回：
//   - true 表示「本进程退出后，计划任务会把同一个二进制重新拉起」
//   - 第二个返回值是判否的理由原文，供调用方打日志；判是时为空
//
// 为什么问的是「任务指不指向我」而不是「谁把我拉起来的」：换版闸二真正
// 要的保证是「我 exit(0) 之后还有人把我拉回来」。schtasks 不像 systemd /
// launchd 那样给被拉起的进程注入任何环境变量，「谁拉起我」在 Windows 上
// 根本问不出来；而「任务在不在、指的是不是我」既问得出，又恰好是那个保证。
//
// 顺带挡住一个真实的坑：从别的目录跑一个 agentd（如临时工作树里的构建），
// 此时任务指向的是另一个二进制——换版会换掉没人运行的那个文件，upgrade
// 报成功而机器上跑的还是旧版。路径对不上就判否，正是闸二该拦的情形。
func UnitReferences(log *slog.Logger, exePath string) (bool, string) {
	return newWindows(log).unitReferences(exePath)
}

func (m *windowsManager) unitReferences(exePath string) (bool, string) {
	if strings.TrimSpace(exePath) == "" {
		return false, "拿不到本进程的可执行文件路径"
	}
	out, err := m.run("schtasks", "/Query", "/TN", WindowsTaskName, "/V", "/FO", "LIST")
	if err != nil {
		return false, fmt.Sprintf("计划任务 %s 未注册（handoff service install 可装）", WindowsTaskName)
	}
	// 子串匹配而不是解析「Task To Run」字段：字段名随系统语言变化，按名字取值
	// 等于把判据绑死在一种语言上（与 taskIsRunning 同一条纪律）。
	// 大小写无关：Windows 路径本就大小写不敏感，注册时与运行时的写法可能不同。
	if !strings.Contains(strings.ToLower(string(out)), strings.ToLower(exePath)) {
		return false, fmt.Sprintf("计划任务 %s 登记的不是本进程的二进制（本进程 %s）", WindowsTaskName, exePath)
	}
	return true, ""
}
