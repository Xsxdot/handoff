// killmode.go —— systemd KillMode 的启动期自检。
//
// 职责：
//   - 判断 agentd 是否运行在 systemd unit 下；若是，读取其 KillMode
//   - KillMode 非 process 时打 WARN，提示执行者会随 agentd 重启一并被杀
//
// 边界：
//   - 只提示不阻断：用户可能有意用 control-group（例如希望重启即清场）
//   - 不修改任何配置：改 unit 是部署侧的事，agentd 无权也不应代劳
//   - 非 Linux / 非 systemd 环境一律静默：macOS 与 docker 下报这个警告是纯噪声
//
// 为什么这件事必须提示：拆掉 tmux 后，「执行者活过 agentd 重启」依赖 shim 脱离
// agentd 的进程树。setsid 做到了会话与进程组的脱离，但**改不了 cgroup 归属**
// ——cgroup 由 fork 继承。systemd 默认 KillMode=control-group 会在 restart 时
// 向整个 cgroup 发信号，shim 与执行者一并被杀，目标①直接落空。
// 这不是本次改动引入的退化：tmux 时代同样如此（tmux server 若由 agentd 首次
// 拉起也在同一 cgroup 里），只是从没被显式说明过。
package agentd

import (
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// selfCgroupPath 是当前进程的 cgroup 描述文件（cgroup v2 统一层级）。
const selfCgroupPath = "/proc/self/cgroup"

// killModeFromCgroup 解析当前进程所属的 systemd unit 及其 KillMode。
//
// 参数（两个函数参数是测试缝，生产分别传 os.ReadFile 与 systemctlKillMode）：
//   - readFile: 读 /proc/self/cgroup
//   - unitLookup: 按 unit 名查 KillMode
//
// 返回：
//   - unit: unit 名；mode: KillMode 值；ok: 是否确实在 systemd unit 下
//   - 非 Linux、非 systemd、cgroup 路径不含 .service 时 ok=false（静默）
func killModeFromCgroup(readFile func(string) ([]byte, error),
	unitLookup func(string) (string, error)) (unit, mode string, ok bool) {
	b, err := readFile(selfCgroupPath)
	if err != nil {
		return "", "", false // 非 Linux 或读不到：静默
	}
	// cgroup v2 行形如 "0::/system.slice/handoff-agentd.service"
	for _, line := range strings.Split(string(b), "\n") {
		idx := strings.LastIndex(line, "/")
		if idx < 0 {
			continue
		}
		name := strings.TrimSpace(line[idx+1:])
		if !strings.HasSuffix(name, ".service") {
			continue
		}
		unit = name
		break
	}
	if unit == "" {
		return "", "", false
	}
	if unitLookup == nil {
		return unit, "", true
	}
	mode, err = unitLookup(unit)
	if err != nil {
		return unit, "", true // unit 认出来了但查不到 mode：仍算 systemd 场景
	}
	return unit, mode, true
}

// systemctlKillMode 用 systemctl show 查某个 unit 的 KillMode。
func systemctlKillMode(unit string) (string, error) {
	out, err := exec.Command("systemctl", "show", "-p", "KillMode", "--value", unit).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// WarnIfKillModeUnsafe 在 agentd 启动期检查 systemd KillMode 并按需告警。
//
// 注意：
//   - 只打日志，绝不阻断启动（见文件头边界）
//   - 非 systemd 环境完全静默——macOS 开发机是主要使用场景，不能有噪声
func WarnIfKillModeUnsafe(log *slog.Logger) {
	unit, mode, ok := killModeFromCgroup(os.ReadFile, systemctlKillMode)
	if !ok {
		log.Debug("未在 systemd unit 下运行，跳过 KillMode 自检")
		return
	}
	if mode == "process" {
		log.Info("systemd KillMode 配置正确，agentd 重启不会连坐执行者", "unit", unit, "kill_mode", mode)
		return
	}
	log.Warn("systemd KillMode 非 process：agentd 重启会连同执行者一起杀掉，"+
		"正在跑的任务会中断。请在 unit 里设 KillMode=process（模板见 deploy/handoff-agentd.service）",
		"unit", unit, "kill_mode", mode)
}
