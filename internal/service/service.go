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
	"errors"
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
	// Disabled 表示单元被显式停用（handoff service stop），自动拉起已关掉。
	//
	// 与「装了没跑」是两种状态，不能合并：前者的处置是 handoff service start，
	// 后者的处置是查日志找崩溃原因。合成一个布尔，status 就会给出错误的
	// 处置建议——把用户支去重装一个本来好好的单元。
	Disabled bool
	// Detail 是管理器原文的摘要，供排障。查不到时为空
	Detail string
}

// Manager 是平台无关的服务托管接口。
type Manager interface {
	// Install 生成单元、写盘、加载、启动，并复核真的起来了。失败时回滚。
	Install(spec Spec) error
	// Start 启动一个**已安装**的单元，不改动单元定义本身。
	//
	// 与 Install 的分工是承重的：Install 负责「让单元存在并跑起来」，为此会
	// 重写单元定义（Windows 上是删掉任务再重建）；Start 只负责「让已存在的
	// 单元跑起来」。把两者混为一谈的代价在 Windows 上最明显——每次换版都会
	// 把计划任务删了重建，用户对任务定义的任何修改和任务历史一并消失。
	//
	// 单元没装时返回错误，**不代为安装**：调用方据此决定是否回落到 Install，
	// 而不是让 Start 悄悄替 Install 干活——那样调用方就再也分不清这两种情形。
	Start() error
	// Stop 停止一个**已安装**的单元，并关掉自动拉起，直到显式 Start。
	//
	// 「关掉自动拉起」是承重的：三个平台都配了「退出就拉起」（launchd
	// KeepAlive=true / systemd Restart=always / Windows 每分钟重复触发），
	// 只杀进程在任何一个平台上都停不住。且这个「关掉」必须跨重启生效，
	// 否则用户重启机器后会发现自己停掉的东西又回来了。
	//
	// 单元没装时返回包装了 ErrNotInstalled 的错误，**不代为安装**。
	Stop() error

	// Restart 重启一个**已安装**的单元，不改动单元定义本身。
	//
	// 语义与 systemctl restart 对齐：单元当前没在跑（含被 Stop 停住）时，
	// Restart 等价于 Start——用户在 agentd 崩着的时候敲 restart，要的是
	// 它起来，而不是一句「它没在跑」。
	//
	// 单元没装时返回包装了 ErrNotInstalled 的错误，**不代为安装**。
	Restart() error

	// Uninstall 停止并移除单元。单元本来就不在时返回 nil（幂等）。
	Uninstall() error
	// Status 查询状态。「没装」是正常答案，不是错误。
	Status() (Status, error)
	// Kind 返回管理器种类："launchd" / "systemd" / "schtasks"。
	Kind() string
	// UnitPath 返回单元文件的落点路径。
	UnitPath() (string, error)
}

// ErrNotInstalled 是「单元没装」的哨兵错误。
//
// Start / Stop / Restart 都不代为安装，一律用它包装返回。上层（CLI、桌面壳）
// 靠 errors.Is 区分「没装」与「装了但操作失败」：前者的处置是
// handoff service install，后者是去查日志。
var ErrNotInstalled = errors.New("服务单元未安装")

// errNotInstalled 造一个带单元路径的 ErrNotInstalled。
//
// 参数：
//   - unit: 单元文件路径或任务名，用于告诉用户该去看哪个东西
//
// 返回：可被 errors.Is(err, ErrNotInstalled) 认出的错误
func errNotInstalled(unit string) error {
	return fmt.Errorf("%w: %s（先跑 handoff service install）", ErrNotInstalled, unit)
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
