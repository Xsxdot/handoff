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
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"strconv"
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
	b.WriteString("  <Triggers>\n    <BootTrigger>\n      <Enabled>true</Enabled>\n")
	b.WriteString("      <Repetition>\n        <Interval>PT1M</Interval>\n")
	b.WriteString("        <Duration>P365D</Duration>\n        <StopAtDurationEnd>false</StopAtDurationEnd>\n")
	b.WriteString("      </Repetition>\n    </BootTrigger>\n  </Triggers>\n")
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
// Running 必须按 PID 复核，而不能按镜像名；否则操作者正在运行的 handoff CLI
// 会被误计为 agentd。没注册时返回零值状态和 nil，因为「没装」是正常答案。
func (m *windowsManager) Status() (Status, error) {
	out, err := m.run("schtasks", "/Query", "/TN", WindowsTaskName, "/V", "/FO", "CSV")
	if err != nil {
		m.log.Debug("查询计划任务未命中（未装时属正常）", "output", strings.TrimSpace(string(out)))
		return Status{}, nil
	}
	s := Status{Installed: true, Detail: firstLine(string(out))}
	pid := pidFromQueryCSV(string(out))
	if pid <= 0 {
		m.log.Warn("计划任务已注册但读不到 PID，Running 判为 false", "task", WindowsTaskName)
		return s, nil
	}
	tout, terr := m.run("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH")
	if terr != nil {
		m.log.Warn("按 PID 复核进程失败，Running 判为 false", "pid", pid, "cause", terr)
		return s, nil
	}
	s.Running = strings.Contains(string(tout), strconv.Itoa(pid))
	m.log.Debug("计划任务状态", "task", WindowsTaskName,
		"installed", s.Installed, "running", s.Running, "pid", pid)
	return s, nil
}

// pidFromQueryCSV 从 schtasks /Query /V /FO CSV 输出里取 PID 列。
//
// 按列名而不是固定列号查找，因为列数会随 Windows 版本变化；返回 0 表示没有
// 读到可用 PID，调用方据此把 Running 判为 false 而不是猜值。
func pidFromQueryCSV(out string) int {
	r := csv.NewReader(strings.NewReader(out))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil || len(records) < 2 {
		return 0
	}
	idx := -1
	for i, h := range records[0] {
		h = strings.TrimPrefix(h, "\ufeff")
		if strings.EqualFold(strings.TrimSpace(h), "PID") {
			idx = i
			break
		}
	}
	if idx < 0 || idx >= len(records[1]) {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(records[1][idx]))
	if err != nil {
		return 0
	}
	return pid
}
