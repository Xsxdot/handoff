// launchd.go —— macOS 侧的服务托管实现。
//
// 边界：
//   - plist 里**不写 AbandonProcessGroup**。P1 探针（spec §7.1）实测：以 setsid
//     拉起的执行者能活过 launchctl kickstart -k 与 bootout，本就不需要它。
//     写上它等于给一条已被实测证伪的假设留下痕迹，下一个人会以为它是必需的
//   - plist 的 ProgramArguments 里**不带 --executor**（spec D5）
package service

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// launchdManager 是 macOS 实现。五个字段是测试缝。
type launchdManager struct {
	log       *slog.Logger
	homeDir   func() (string, error)
	plistDir  string
	mkdirAll  func(path string, perm os.FileMode) error
	run       func(name string, args ...string) ([]byte, error)
	writeFile func(path string, data []byte, perm uint32) error
	remove    func(path string) error
}

// newLaunchd 构造生产用的 launchd manager。
func newLaunchd(log *slog.Logger) *launchdManager {
	m := &launchdManager{
		log:      log,
		homeDir:  os.UserHomeDir,
		mkdirAll: os.MkdirAll,
		run: func(name string, args ...string) ([]byte, error) {
			// CombinedOutput：launchctl 的真因大多写在 stderr 上，只取 stdout
			// 会得到一个空字符串加一个 "exit status 5"，等于没有诊断信息
			return exec.Command(name, args...).CombinedOutput()
		},
		writeFile: func(p string, b []byte, perm uint32) error { return os.WriteFile(p, b, os.FileMode(perm)) },
		remove:    os.Remove,
	}
	if h, err := m.homeDir(); err == nil {
		m.plistDir = filepath.Join(h, "Library", "LaunchAgents")
	}
	return m
}

func (m *launchdManager) Kind() string { return "launchd" }

// UnitPath 返回 plist 的落点。
func (m *launchdManager) UnitPath() (string, error) {
	if m.plistDir == "" {
		return "", fmt.Errorf("取不到用户主目录，无法定位 LaunchAgents 目录")
	}
	return filepath.Join(m.plistDir, LaunchdLabel+".plist"), nil
}

// domain 返回 launchctl 的目标域，形如 gui/501。
func (m *launchdManager) domain() string {
	return "gui/" + strconv.Itoa(os.Getuid())
}

// target 返回 launchctl 的服务目标，形如 gui/501/dev.gosuper.handoff.agentd。
func (m *launchdManager) target() string { return m.domain() + "/" + LaunchdLabel }

// plistBody 渲染 plist 内容。
//
// 参数：
//   - spec: 要托管的 agentd 描述
//
// 返回：
//   - plist 全文
//
// 注意：
//   - KeepAlive=true 对应 systemd 的 Restart=always：**exit 0 也会被重新拉起**，
//     这正是自更新换版所依赖的（P1 实测确认，见 spec §7.1）
//   - launchd 对重生有约 10 秒节流，换版期间会有约 10 秒的服务空窗
func (m *launchdManager) plistBody(spec Spec) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	b.WriteString("  <key>Label</key>\n  <string>" + LaunchdLabel + "</string>\n")
	b.WriteString("  <key>ProgramArguments</key>\n  <array>\n")
	b.WriteString("    <string>" + spec.BinPath + "</string>\n")
	b.WriteString("    <string>agentd</string>\n")
	if spec.ConfigPath != "" {
		b.WriteString("    <string>--config</string>\n")
		b.WriteString("    <string>" + spec.ConfigPath + "</string>\n")
	}
	b.WriteString("  </array>\n")
	b.WriteString("  <key>RunAtLoad</key>\n  <true/>\n")
	b.WriteString("  <key>KeepAlive</key>\n  <true/>\n")
	if spec.LogPath != "" {
		b.WriteString("  <key>StandardOutPath</key>\n  <string>" + spec.LogPath + "</string>\n")
		b.WriteString("  <key>StandardErrorPath</key>\n  <string>" + spec.LogPath + "</string>\n")
	}
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

// Install 写 plist 并加载，最后复核服务真的注册上了。
func (m *launchdManager) Install(spec Spec) error {
	path, err := m.UnitPath()
	if err != nil {
		return err
	}
	m.log.Info("安装 launchd 服务", "label", LaunchdLabel, "plist", path, "bin", spec.BinPath)

	// 先清旧：同名 job 还注册着时 bootstrap 会直接失败（"service already loaded"）。
	// 忽略这一步的错误——绝大多数情况下它本来就没装，报错是正常的
	if out, err := m.run("launchctl", "bootout", m.target()); err != nil {
		m.log.Debug("bootout 旧 job（未装时报错属正常）", "output", strings.TrimSpace(string(out)))
	}

	if err := m.mkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建 %s: %w", filepath.Dir(path), err)
	}
	if err := m.writeFile(path, []byte(m.plistBody(spec)), 0o644); err != nil {
		return fmt.Errorf("写 plist %s: %w", path, err)
	}

	if out, err := m.run("launchctl", "bootstrap", m.domain(), path); err != nil {
		// 回滚：留下一个加载不了的 plist，下次登录 launchd 还会反复尝试加载它，
		// 而用户以为自己从没装过。报文带上 launchctl 原文——那才是真因
		if rmErr := m.remove(path); rmErr != nil {
			m.log.Error("回滚删除 plist 失败", "path", path, "cause", rmErr)
		}
		m.log.Error("加载 launchd 服务失败，已回滚", "cause", err, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("加载 launchd 服务失败: %s（%w）", strings.TrimSpace(string(out)), err)
	}

	// 复核：bootstrap 成功不等于进程起来了（二进制路径错、端口被占都会
	// 起来即死）。不复核就报「安装成功」，用户会去查一个不存在的服务
	if out, err := m.run("launchctl", "print", m.target()); err != nil {
		m.log.Error("服务已加载但复核失败", "cause", err, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("服务已加载但复核不到（可能起来即退出，检查 %s）: %w", spec.LogPath, err)
	}
	m.log.Info("launchd 服务安装完成", "label", LaunchdLabel)
	return nil
}

// Uninstall 卸载并删除 plist。本来就没装时返回 nil。
func (m *launchdManager) Uninstall() error {
	path, err := m.UnitPath()
	if err != nil {
		return err
	}
	m.log.Info("卸载 launchd 服务", "label", LaunchdLabel)
	if out, err := m.run("launchctl", "bootout", m.target()); err != nil {
		// 没装时 bootout 必然报错，这是正常的，不该让 uninstall 失败
		m.log.Debug("bootout 报错（未装时属正常）", "output", strings.TrimSpace(string(out)))
	}
	if err := m.remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除 plist %s: %w", path, err)
	}
	m.log.Info("launchd 服务已卸载", "label", LaunchdLabel)
	return nil
}

// Status 查询 job 是否注册且在跑。
func (m *launchdManager) Status() (Status, error) {
	out, err := m.run("launchctl", "print", m.target())
	if err != nil {
		// 没注册时 launchctl print 退 113。这是一个正常答案，不是查询失败
		return Status{}, nil
	}
	s := Status{Installed: true, Running: true, Detail: firstLine(string(out))}
	// print 输出里带 "state = running" 才算真在跑；只注册没跑也是常见状态
	if strings.Contains(string(out), "state = not running") {
		s.Running = false
	}
	return s, nil
}

// firstLine 取多行输出的第一行，用作 Detail 摘要。
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
