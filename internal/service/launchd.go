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
	"time"
)

// launchdVerifyInterval / launchdVerifyAttempts 是复核状态变化的轮询节奏。
//
// 为什么轮询而不是查一次：launchctl 的 bootstrap / bootout / kill 都是异步的
// ——命令返回只说明请求被受理，进程到位或退出还要几十到几百毫秒。查一次会
// 把正常的启动误报成失败，也会把还没死透的旧进程误报成「已停止」。
const (
	launchdVerifyInterval = 200 * time.Millisecond
	launchdVerifyAttempts = 25
	launchdVerifyWindow   = launchdVerifyInterval * launchdVerifyAttempts
)

// launchdManager 是 macOS 实现。七个基础字段与 stat、sleep 是测试缝。
type launchdManager struct {
	log       *slog.Logger
	homeDir   func() (string, error)
	plistDir  string
	mkdirAll  func(path string, perm os.FileMode) error
	run       func(name string, args ...string) ([]byte, error)
	writeFile func(path string, data []byte, perm uint32) error
	remove    func(path string) error
	// stat 用来判断 plist 在不在，即「装没装」。必须是缝：Status 现在按
	// plist 存在与否判 Installed，测试要能构造「装了但没加载」这一状态
	stat func(path string) (os.FileInfo, error)
	// sleep 是复核轮询的等待缝：测试注入空实现，避免为了走完复核窗口
	// 真的睡几秒（那会让 service 包的单测从毫秒级变成秒级）
	sleep func(time.Duration)
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
		stat:      os.Stat,
		sleep:     time.Sleep,
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

	// 解除可能残留的停用状态。
	//
	// 承重：Stop 用 launchctl disable 把 target 写进了 launchd 的停用清单，
	// 而那份清单独立于 plist——删掉 plist 再重装也不会清掉它。对停用的 target
	// bootstrap 会直接拒，Install 随后回滚删掉刚写的 plist：用户 stop 过一次
	// 之后跑 install，看到的是「装不上」而且 plist 也没了。
	// 忽略错误：从未被 disable 过的 target，enable 报错是正常的
	if out, err := m.run("launchctl", "enable", m.target()); err != nil {
		m.log.Debug("enable（从未停用过时报错属正常）", "output", strings.TrimSpace(string(out)))
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

// ensureInstalled 在做任何变更前确认 plist 在。
//
// 返回：plist 路径；不在时返回包装了 ErrNotInstalled 的错误
//
// 注意：三个生命周期方法共用它，且必须在发出任何 launchctl 变更命令之前调用
// ——「没装」的正确处置是去 install，不是让 start 悄悄替 install 干活
func (m *launchdManager) ensureInstalled() (string, error) {
	path, err := m.UnitPath()
	if err != nil {
		return "", err
	}
	if _, statErr := m.stat(path); statErr != nil {
		m.log.Error("单元未安装", "label", LaunchdLabel, "plist", path, "cause", statErr)
		return "", errNotInstalled(path)
	}
	return path, nil
}

// waitRunning 轮询到服务真的在跑为止。超时返回 false。
//
// 这里不调用 Status，因为它还会查询 print-disabled，而 start 的 25 轮复核只
// 关心运行态。
func (m *launchdManager) waitRunning() bool {
	for i := 0; i < launchdVerifyAttempts; i++ {
		if m.currentPid() != 0 {
			return true
		}
		m.sleep(launchdVerifyInterval)
	}
	return false
}

// waitStopped 轮询到服务真的不在跑为止。超时返回 false。
//
// 这里不调用 Status，因为它还会查询 print-disabled，而 stop 的 25 轮复核只关
// 心运行态。
func (m *launchdManager) waitStopped() bool {
	for i := 0; i < launchdVerifyAttempts; i++ {
		if m.currentPid() == 0 {
			return true
		}
		m.sleep(launchdVerifyInterval)
	}
	return false
}

// currentPid 取当前实例的 pid，取不到（未加载 / 没在跑）返回 0。
//
// 它也是 start/stop 复核使用的轻量运行态探针：launchctl print 查不到 job，或
// 输出里没有 pid 行，这两种情况都返回 0；非零 pid 就代表当前有 agentd 实例在跑。
// 这正是轮询可以用 pid 代替完整 Status 的等价关系。
func (m *launchdManager) currentPid() int {
	out, err := m.run("launchctl", "print", m.target())
	if err != nil {
		return 0
	}
	return parsePrintPid(string(out))
}

// Start 启动一个已安装的服务，并解除可能存在的停用状态。
//
// 返回：错误——plist 不在（ErrNotInstalled）、enable 失败、加载失败、
// 或复核窗口内没见它跑起来
//
// 注意：
//   - **enable 必须在 bootstrap 之前。** 被 launchctl disable 过的 target，
//     bootstrap 会直接拒（Service is disabled），而 Stop 正是靠 disable 才让
//     「停到显式 start」跨得过重启——这条路是 stop→start 的必经之路
//   - 用 bootstrap 而不是 kickstart：Stop 做过 bootout，job 已从 launchd
//     卸载，kickstart 找不到目标。已经加载着时 bootstrap 会报
//     "service already loaded"，那不是失败（目标状态本就是「加载着」），
//     降级为 kickstart 把它踢起来
//   - 不代为 Install
func (m *launchdManager) Start() error {
	path, err := m.ensureInstalled()
	if err != nil {
		return err
	}
	m.log.Info("启动 launchd 服务", "label", LaunchdLabel, "plist", path)
	if out, eerr := m.run("launchctl", "enable", m.target()); eerr != nil {
		m.log.Error("解除停用失败", "label", LaunchdLabel,
			"cause", eerr, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("解除停用失败: %s（%w）", strings.TrimSpace(string(out)), eerr)
	}
	if out, berr := m.run("launchctl", "bootstrap", m.domain(), path); berr != nil {
		bootstrapOutput := strings.TrimSpace(string(out))
		m.log.Debug("bootstrap 报错，改用 kickstart（已加载时属正常）",
			"output", bootstrapOutput)
		if kout, kerr := m.run("launchctl", "kickstart", m.target()); kerr != nil {
			kickstartOutput := strings.TrimSpace(string(kout))
			m.log.Error("启动 launchd 服务失败：先试 bootstrap 再试 kickstart，两个都失败",
				"label", LaunchdLabel,
				"bootstrap_cause", berr, "bootstrap_output", bootstrapOutput,
				"kickstart_cause", kerr, "kickstart_output", kickstartOutput)
			return fmt.Errorf("启动 launchd 服务失败：先试 bootstrap 再试 kickstart，两个都失败；bootstrap 原文: %s（%v）；kickstart 原文: %s（%w）",
				bootstrapOutput, berr, kickstartOutput, kerr)
		}
	}
	if !m.waitRunning() {
		m.log.Error("服务已触发但复核窗口内未见运行",
			"label", LaunchdLabel, "window", launchdVerifyWindow)
		return fmt.Errorf("服务已触发，但 %s 内未复核到运行（可能起来即退出）", launchdVerifyWindow)
	}
	m.log.Info("launchd 服务已启动", "label", LaunchdLabel)
	return nil
}

// Stop 停止服务并关掉自动拉起，直到显式 Start。
//
// 返回：错误——plist 不在（ErrNotInstalled）、disable 失败、
// 或复核窗口内它仍在跑
//
// 注意：
//   - **disable 在前、bootout 在后，顺序是承重的。** disable 成功而 bootout
//     失败，留下的是「还在跑但已停用」，重启后自己下去；反过来 bootout 成功
//     而 disable 失败，留下的是「停了但仍启用」，下次登录 launchd 自动把它
//     bootstrap 回来，用户的 stop 被无声撤销。选前一种失败形态
//   - **只 bootout 不 disable 是不够的**：plist 还躺在 ~/Library/LaunchAgents
//     里，RunAtLoad 会在下次登录时把它拉回来
func (m *launchdManager) Stop() error {
	path, err := m.ensureInstalled()
	if err != nil {
		return err
	}
	m.log.Info("停止并停用 launchd 服务", "label", LaunchdLabel, "plist", path)
	if out, derr := m.run("launchctl", "disable", m.target()); derr != nil {
		m.log.Error("停用 launchd 服务失败", "label", LaunchdLabel,
			"cause", derr, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("停用失败: %s（%w）", strings.TrimSpace(string(out)), derr)
	}
	if out, berr := m.run("launchctl", "bootout", m.target()); berr != nil {
		// 本来就没加载时 bootout 必然报错，这不是失败
		m.log.Debug("bootout 报错（本来就没加载时属正常）", "output", strings.TrimSpace(string(out)))
	}
	if !m.waitStopped() {
		m.log.Error("已请求停止但复核窗口内仍在运行",
			"label", LaunchdLabel, "window", launchdVerifyWindow)
		return fmt.Errorf("已请求停止，但 %s 内它仍在运行", launchdVerifyWindow)
	}
	m.log.Info("launchd 服务已停止并停用", "label", LaunchdLabel)
	return nil
}

// Restart 就地重启服务，不改动 plist。
//
// 返回：错误——plist 不在（ErrNotInstalled）、发信号失败、
// 或复核窗口内 pid 没换
//
// 注意：
//   - **发 SIGTERM 而不是 kickstart -k。** 后者是 SIGKILL，会把在途任务砍在
//     半路；SIGTERM 走的是 agentd 自己的优雅关停（停收新连接 → 等在途请求
//     → 按序收尾），而 plist 里的 KeepAlive=true 保证它随后被拉回来
//   - **复核判据是 pid 变了且在跑，不是「还在跑」。** launchd 的重启是异步的，
//     kill 返回时旧进程可能还没死；只查「在不在跑」的话，「什么都没发生」和
//     「重启成功」长得一模一样
//   - 没在跑（含被 Stop 停住）时等价于 Start，语义与 systemctl restart 对齐：
//     用户在 agentd 崩着的时候敲 restart，要的是它起来
func (m *launchdManager) Restart() error {
	if _, err := m.ensureInstalled(); err != nil {
		return err
	}
	before := m.currentPid()
	if before == 0 {
		m.log.Info("重启时发现服务未在运行，改为启动", "label", LaunchdLabel)
		return m.Start()
	}
	m.log.Info("重启 launchd 服务", "label", LaunchdLabel, "pid_before", before)
	if out, kerr := m.run("launchctl", "kill", "SIGTERM", m.target()); kerr != nil {
		m.log.Error("发送 SIGTERM 失败", "label", LaunchdLabel,
			"cause", kerr, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("发送 SIGTERM 失败: %s（%w）", strings.TrimSpace(string(out)), kerr)
	}
	for i := 0; i < launchdVerifyAttempts; i++ {
		m.sleep(launchdVerifyInterval)
		if now := m.currentPid(); now != 0 && now != before {
			m.log.Info("launchd 服务已重启", "label", LaunchdLabel,
				"pid_before", before, "pid_after", now)
			return nil
		}
	}
	m.log.Error("已发 SIGTERM 但复核窗口内 pid 未换",
		"label", LaunchdLabel, "pid_before", before, "window", launchdVerifyWindow)
	return fmt.Errorf("已发 SIGTERM，但 %s 内 pid 仍是 %d（可能没被拉起来，检查日志）",
		launchdVerifyWindow, before)
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

// Status 查询单元的安装、运行与停用状态。
//
// 返回：
//   - Status：Installed 看 plist 在不在，Running 看 job 加载且不是
//     not running，Disabled 查 launchd 的停用清单
//   - 错误：只有取不到 plist 路径（主目录读不出来）才算查询失败；
//     「没装」「没跑」「停用了」都是正常答案，不是错误
//
// 注意：
//   - **Installed 的判据是 plist 存在，不是 launchctl print 能查到。**
//     Stop 会 bootout（把 job 从 launchd 卸载）但保留 plist；若按 print 判，
//     stop 之后 start 会被「没装」硬拒，「停到显式 start」当场自相矛盾
func (m *launchdManager) Status() (Status, error) {
	path, err := m.UnitPath()
	if err != nil {
		return Status{}, err
	}
	s := Status{}
	if _, statErr := m.stat(path); statErr == nil {
		s.Installed = true
	}
	if out, printErr := m.run("launchctl", "print", m.target()); printErr == nil {
		// 加载着就一定装着：plist 可能刚被手工删掉而 job 还留在内存里
		s.Installed = true
		s.Detail = firstLine(string(out))
		// print 输出里带 "state = not running" 才算没跑；只注册没跑是常见状态
		s.Running = !strings.Contains(string(out), "state = not running")
	}
	s.Disabled = m.isDisabled()
	m.log.Debug("launchd 服务状态", "label", LaunchdLabel,
		"installed", s.Installed, "running", s.Running, "disabled", s.Disabled)
	return s, nil
}

// isDisabled 查 launchd 的停用覆写数据库，判断本 job 是否被显式停用。
//
// 返回：被 launchctl disable 过则 true；查不到、查询失败、从未出现在清单里
// 都按 false（未停用）处理
//
// 注意：
//   - 两种输出格式都要认。macOS 26 打的是 "<label>" => disabled/enabled，
//     更早的系统打的是 => true/false。只认一种，会在另一种系统上把「已停用」
//     读成「启用」，status 于是给出错误的处置建议
//   - 从未被 enable/disable 过的 label 根本不出现在这份清单里——不出现即未停用
func (m *launchdManager) isDisabled() bool {
	out, err := m.run("launchctl", "print-disabled", m.domain())
	if err != nil {
		m.log.Debug("查 launchd 停用清单失败，按未停用处理",
			"cause", err, "output", strings.TrimSpace(string(out)))
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, `"`+LaunchdLabel+`"`) {
			continue
		}
		i := strings.Index(line, "=>")
		if i < 0 {
			continue
		}
		v := strings.TrimSpace(line[i+2:])
		return v == "disabled" || v == "true"
	}
	return false
}

// parsePrintPid 从 launchctl print 的输出里取当前实例的 pid。
//
// 返回：取不到（未加载、或那一刻没有进程）返回 0
//
// 注意：pid 是 Restart 唯一可信的复核判据——「还在跑」区分不了新旧实例
func parsePrintPid(out string) int {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "pid = ") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "pid = ")))
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

// firstLine 取多行输出的第一行，用作 Detail 摘要。
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
