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
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// WindowsTaskName 是计划任务的名字，同时也是 XML 文件名的主干。
const WindowsTaskName = "handoff-agentd"

// windowsManager 是 Windows 实现。七个字段是测试缝。
type windowsManager struct {
	log          *slog.Logger
	localAppData string
	currentUser  func() (string, error)
	mkdirAll     func(string, os.FileMode) error
	run          func(string, ...string) ([]byte, error)
	writeFile    func(string, []byte, uint32) error
	remove       func(string) error
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
// 注意：重复触发与 IgnoreNew 一起模拟 KeepAlive，LogonTrigger 对标 RunAtLoad，
// 电池设置避免任务静默不启动，ExecutionTimeLimit 为零避免长跑 agentd 被掐掉，
// Command 直接指向 handoff.exe 以确保 schtasks 能跟踪真正的 agentd。
func (m *windowsManager) taskXML(spec Spec, user string) string {
	args := "agentd"
	if spec.ConfigPath != "" {
		args += " --config " + spec.ConfigPath
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-16"?>` + "\n")
	b.WriteString(`<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">` + "\n")
	b.WriteString("  <RegistrationInfo>\n    <Description>handoff agentd</Description>\n  </RegistrationInfo>\n")
	b.WriteString("  <Triggers>\n    <LogonTrigger>\n      <Enabled>true</Enabled>\n")
	b.WriteString("      <Repetition>\n        <Interval>PT1M</Interval>\n")
	b.WriteString("        <Duration>P365D</Duration>\n        <StopAtDurationEnd>false</StopAtDurationEnd>\n")
	b.WriteString("      </Repetition>\n    </LogonTrigger>\n  </Triggers>\n")
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
	return filepath.Join(m.localAppData, "handoff", WindowsTaskName+".xml"), nil
}

// Kind 返回管理器种类。
func (m *windowsManager) Kind() string { return "schtasks" }
