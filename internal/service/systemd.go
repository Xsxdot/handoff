// systemd.go -- Linux 侧的服务托管实现。
//
// 边界：
//   - 写 /etc/systemd/system 需要 root。**无权限时必须明确提示「需要 sudo」**
//     而不是把 permission denied 扁平抛出（B45 的教训：真因只落在日志里等于没有）
//   - unit 里 KillMode=process 与 Restart=always 是硬要求，理由见各自注释
//
// 未真机验证：本仓库暂无 Linux 机器（spec §10）。本文件的正确性目前完全由
// systemd_test.go 的内容断言守着，改动时务必同步维护那些断言。
package service

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

// systemdManager 是 Linux 实现。四个字段是测试缝。
type systemdManager struct {
	log       *slog.Logger
	unitDir   string
	user      string
	run       func(name string, args ...string) ([]byte, error)
	writeFile func(path string, data []byte, perm uint32) error
	remove    func(path string) error
}

// newSystemd 构造生产用的 systemd manager。
func newSystemd(log *slog.Logger) *systemdManager {
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	return &systemdManager{
		log:     log,
		unitDir: "/etc/systemd/system",
		user:    name,
		run: func(n string, args ...string) ([]byte, error) {
			return exec.Command(n, args...).CombinedOutput()
		},
		writeFile: func(p string, b []byte, perm uint32) error { return os.WriteFile(p, b, os.FileMode(perm)) },
		remove:    os.Remove,
	}
}

func (m *systemdManager) Kind() string { return "systemd" }

// UnitPath 返回 unit 文件落点。
func (m *systemdManager) UnitPath() (string, error) {
	return filepath.Join(m.unitDir, SystemdUnit), nil
}

// unitBody 渲染 unit 内容。
//
// 两条硬要求，改之前先读懂为什么：
//
//   - KillMode=process：执行者由 agentd 经 shim 以 setsid 拉起，setsid 脱离了
//     会话与进程组但**改不了 cgroup 归属**（cgroup 由 fork 继承）。systemd 默认的
//     KillMode=control-group 会在 stop/restart 时向整个 cgroup 发信号，执行者
//     一并被杀，正在跑的任务全部中断（B36）
//   - Restart=always：自更新换版靠「agentd 自己 exit 0 -> 管理器拉起新版」交接。
//     on-failure 在 exit 0 时**不重启**——换完版服务就此消失，而且没有任何信号
//     告诉任何人。这是 D9 的直接结论
//
// User 必须写字面用户名。写 %i 会被当成模板 unit 的实例名占位符，在非模板
// unit 里解析为空串，而 `User=` 空值会被 systemd 重置为 root——服务会以 root
// 静默跑起来，不报任何错。
func (m *systemdManager) unitBody(spec Spec) string {
	exec := spec.BinPath + " agentd"
	if spec.ConfigPath != "" {
		exec += " --config " + spec.ConfigPath
	}
	var b strings.Builder
	b.WriteString("# 由 handoff service install 生成，勿手改——重装会覆盖。\n")
	b.WriteString("[Unit]\n")
	b.WriteString("Description=handoff agentd (executor host)\n")
	b.WriteString("After=network-online.target\n")
	b.WriteString("Wants=network-online.target\n\n")
	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	b.WriteString("User=" + m.user + "\n")
	b.WriteString("ExecStart=" + exec + "\n")
	// exit 0 也要拉起：自更新换版的交接点就是退出码（D9）
	b.WriteString("Restart=always\n")
	b.WriteString("RestartSec=3\n\n")
	b.WriteString("# KillMode=process 是硬要求：setsid 改不了 cgroup 归属，\n")
	b.WriteString("# 默认的 control-group 会在重启时把执行者一并杀掉（B36）。\n")
	b.WriteString("KillMode=process\n\n")
	b.WriteString("[Install]\n")
	b.WriteString("WantedBy=multi-user.target\n")
	return b.String()
}

// Install 写 unit、reload、enable --now，并复核真的活着。
func (m *systemdManager) Install(spec Spec) error {
	path, err := m.UnitPath()
	if err != nil {
		return err
	}
	if m.user == "" {
		// 空 User 会被 systemd 重置为 root，服务以 root 静默跑起来。宁可拦住
		return fmt.Errorf("取不到当前用户名，无法生成 unit（User= 留空会让服务以 root 运行）")
	}
	m.log.Info("安装 systemd 服务", "unit", SystemdUnit, "path", path, "bin", spec.BinPath, "user", m.user)

	if err := m.writeFile(path, []byte(m.unitBody(spec)), 0o644); err != nil {
		// B45 的教训：扁平抛 permission denied，用户不知道该 sudo 重跑
		if os.IsPermission(err) || strings.Contains(err.Error(), "permission denied") {
			return fmt.Errorf("写 %s 需要 root 权限，请用 sudo 重跑：sudo handoff service install（原因: %w）", path, err)
		}
		return fmt.Errorf("写 unit %s: %w", path, err)
	}

	if out, err := m.run("systemctl", "daemon-reload"); err != nil {
		m.rollback(path)
		return fmt.Errorf("systemctl daemon-reload 失败: %s（%w）", strings.TrimSpace(string(out)), err)
	}
	if out, err := m.run("systemctl", "enable", "--now", SystemdUnit); err != nil {
		m.rollback(path)
		m.log.Error("启用 systemd 服务失败，已回滚", "cause", err, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("启用 systemd 服务失败: %s（%w）", strings.TrimSpace(string(out)), err)
	}

	// 复核：enable --now 返回 0 不代表进程还活着（起来即崩同样返回 0）
	if out, err := m.run("systemctl", "is-active", SystemdUnit); err != nil {
		m.log.Error("服务已启用但复核不到活跃状态", "cause", err, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("服务已启用但未处于活跃状态（可能起来即退出，查 journalctl -u %s）: %w", SystemdUnit, err)
	}
	m.log.Info("systemd 服务安装完成", "unit", SystemdUnit)
	return nil
}

// rollback 删掉刚写的 unit 并 reload，避免留下一个装不上又卸不掉的残件。
func (m *systemdManager) rollback(path string) {
	if err := m.remove(path); err != nil && !os.IsNotExist(err) {
		m.log.Error("回滚删除 unit 失败", "path", path, "cause", err)
		return
	}
	if _, err := m.run("systemctl", "daemon-reload"); err != nil {
		m.log.Warn("回滚后 daemon-reload 失败", "cause", err)
	}
}

// Start 启动一个已安装的单元，不重写 unit 文件、不 daemon-reload。
//
// 单元没装时 systemctl start 会失败，调用方据此回落到 Install。
func (m *systemdManager) Start() error {
	if out, err := m.run("systemctl", "start", SystemdUnit); err != nil {
		m.log.Error("启动 systemd 服务失败", "unit", SystemdUnit,
			"cause", err, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("启动 systemd 服务失败: %s（%w）", strings.TrimSpace(string(out)), err)
	}
	// 复核理由同 Install：start 返回 0 只说明请求被受理，起来即退出照样「成功」
	if out, err := m.run("systemctl", "is-active", SystemdUnit); err != nil {
		m.log.Error("服务已触发但未 active", "unit", SystemdUnit,
			"cause", err, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("服务已触发但未 active（可能起来即退出）: %s（%w）",
			strings.TrimSpace(string(out)), err)
	}
	m.log.Info("systemd 服务已启动", "unit", SystemdUnit)
	return nil
}

// Uninstall 停用并删除 unit。本来就没装时返回 nil。
func (m *systemdManager) Uninstall() error {
	path, err := m.UnitPath()
	if err != nil {
		return err
	}
	m.log.Info("卸载 systemd 服务", "unit", SystemdUnit)
	if out, err := m.run("systemctl", "disable", "--now", SystemdUnit); err != nil {
		// 没装时 disable 必然报错，正常
		m.log.Debug("disable 报错（未装时属正常）", "output", strings.TrimSpace(string(out)))
	}
	if err := m.remove(path); err != nil && !os.IsNotExist(err) {
		if os.IsPermission(err) || strings.Contains(err.Error(), "permission denied") {
			return fmt.Errorf("删除 %s 需要 root 权限，请用 sudo 重跑（原因: %w）", path, err)
		}
		return fmt.Errorf("删除 unit %s: %w", path, err)
	}
	if _, err := m.run("systemctl", "daemon-reload"); err != nil {
		m.log.Warn("卸载后 daemon-reload 失败", "cause", err)
	}
	m.log.Info("systemd 服务已卸载", "unit", SystemdUnit)
	return nil
}

// Status 查询 unit 是否装了、是否活跃。
func (m *systemdManager) Status() (Status, error) {
	out, err := m.run("systemctl", "is-active", SystemdUnit)
	detail := firstLine(string(out))
	if err != nil {
		// is-active 对 inactive/failed/not-found 都返回非 0。这些都是正常答案。
		// 用 unit 文件在不在来区分「没装」与「装了没跑」
		path, _ := m.UnitPath()
		if _, statErr := os.Stat(path); statErr == nil {
			return Status{Installed: true, Running: false, Detail: detail}, nil
		}
		return Status{Detail: detail}, nil
	}
	return Status{Installed: true, Running: true, Detail: detail}, nil
}

// Stop 见 Manager.Stop。TODO(handoff): Task 3 换成真实现。
func (m *systemdManager) Stop() error {
	return fmt.Errorf("systemd Stop 尚未实现")
}

// Restart 见 Manager.Restart。TODO(handoff): Task 3 换成真实现。
func (m *systemdManager) Restart() error {
	return fmt.Errorf("systemd Restart 尚未实现")
}
