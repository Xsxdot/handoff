// Package service 把 agentd 交给本平台的进程管理器托管。
//
// 职责：
//   - 生成服务单元（macOS 的 launchd plist / Linux 的 systemd unit）
//   - 安装、卸载、查询状态；安装后**复核服务真的起来了**
//
// 边界：
//   - 不下载、不判断版本、不读 handoff 的配置文件：要什么路径由调用方在 Spec 里给全
//   - 不负责重启策略之外的进程管理：拉起、崩溃重启都是管理器的事
//   - 三个平台各一个实现：launchd（macOS）/ systemd（Linux）/ schtasks（Windows）。
//     Windows 走计划任务而非 SCM 服务，理由见 windows.go 的文件头
package service

import (
	"fmt"
	"log/slog"
	"runtime"
)

// LaunchdLabel 是 macOS 上的 job 标签，同时也是 plist 的文件名主干。
const LaunchdLabel = "dev.gosuper.handoff.agentd"

// SystemdUnit 是 Linux 上的 unit 文件名。
const SystemdUnit = "handoff-agentd.service"

// Spec 描述「要托管的是哪个 agentd」。
//
// 字段说明：
//   - BinPath: handoff 可执行文件的**绝对路径**，调用方须先做 EvalSymlinks，
//     否则服务会指向一个 symlink，升级换掉链接目标后单元还指着旧的
//   - ConfigPath: 传给 agentd 的 --config
//   - LogPath: 管理器把 stdout/stderr 重定向到哪
type Spec struct {
	BinPath    string
	ConfigPath string
	LogPath    string
}

// Status 是服务的当前状态。
//
// Installed 与 Running 是两件事：单元装了但没跑（崩溃循环、被手动 stop）
// 是一个真实且常见的状态，合并成一个布尔会让用户看不出区别。
type Status struct {
	Installed bool
	Running   bool
	// Detail 是管理器原文的摘要，供排障。查不到时为空
	Detail string
}

// Manager 是平台无关的服务托管接口。
type Manager interface {
	// Install 生成单元、写盘、加载、启动，并复核真的起来了。失败时回滚。
	Install(spec Spec) error
	// Uninstall 停止并移除单元。单元本来就不在时返回 nil（幂等）。
	Uninstall() error
	// Status 查询状态。「没装」是正常答案，不是错误。
	Status() (Status, error)
	// Kind 返回管理器种类："launchd" / "systemd"。
	Kind() string
	// UnitPath 返回单元文件的落点路径。
	UnitPath() (string, error)
}

// New 按当前平台返回对应的 Manager。
//
// 参数：
//   - log: 日志入口
//
// 返回：
//   - 平台对应的 Manager
//   - 不支持的平台返回错误，报文里说清为什么不支持而不是只说「不支持」
func New(log *slog.Logger) (Manager, error) {
	switch runtime.GOOS {
	case "darwin":
		return newLaunchd(log), nil
	case "linux":
		return newSystemd(log), nil
	case "windows":
		return newWindows(log), nil
	default:
		return nil, fmt.Errorf("不支持的平台 %s（仅 darwin/linux/windows）", runtime.GOOS)
	}
}
