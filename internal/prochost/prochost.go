// Package prochost 提供跨平台的「detached 执行者进程」承载能力。
//
// 职责：
//   - Start：以脱离本进程的方式拉起 shim，由 shim 承载真正的 executor 进程
//   - Alive / Kill：基于文件锁的存活判定与按进程组的回收
//   - CreateInputChannel / WaitInputReader：executor 的输入通道（unix = FIFO）
//   - RunShim：shim 自身的主体逻辑（见 shim.go）
//
// 边界：
//   - 不认识 executor 的协议：只管进程起没起来、活没活着、怎么杀干净；
//     「协议层能不能用」由各 adapter 自己探活
//   - 不写任务状态、不碰 store：Handle 的持久化由调用方（adapter）负责
//   - 不解释 Spec.Argv：argv 由调用方组好，本包原样交给操作系统，不经任何 shell
//
// 为什么存活判定用文件锁而不是 pid：pid 会被操作系统复用，「进程存在」不等于
// 「我的那个进程存在」——历史上 workspace.go 就因此误杀过无关进程组。shim 全生命
// 周期持有 LockPath 的排他锁，内核在进程死亡时无条件释放，试锁失败即证明它还活着，
// 完全没有复用窗口。pid 只用于发信号，不参与存活语义。
package prochost

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Spec 是一次执行者进程的启动描述，序列化后交给 shim。
//
// 字段说明：
//   - Argv: 完整命令行，[0] 必须是 exec.LookPath 解析后的绝对路径（shim 不做 PATH 查找）
//   - Dir: 子进程工作目录（任务仓库）
//   - Env: 完整环境变量（KEY=VALUE），由调用方合并完毕，shim 原样使用不再追加
//   - Stdout/Stderr: 子进程输出的追加落盘路径；两者可指向同一文件
//   - InputCh: 可选。非空时 shim 以 O_RDWR 持有该 FIFO 并作为子进程 stdin
//   - LockPath: shim 的存活锁路径
//   - InfoPath: shim 补写 child_pid 的 proc.json 路径
//   - Sentinel: true 时子进程退出后向 Stdout 追加 handoff_exit 哨兵行
//
// 注意：
//   - Env 的值可能含凭据，本结构会被序列化到 0600 的 spec.json；日志里只打 key 名
type Spec struct {
	Argv     []string `json:"argv"`
	Dir      string   `json:"dir"`
	Env      []string `json:"env"`
	Stdout   string   `json:"stdout"`
	Stderr   string   `json:"stderr"`
	InputCh  string   `json:"input_ch,omitempty"`
	LockPath string   `json:"lock_path"`
	InfoPath string   `json:"info_path"`
	Sentinel bool     `json:"sentinel"`
}

// Handle 是一个已拉起的 shim 的句柄，可直接序列化进 adapter 的 proc.json。
//
// 字段说明：
//   - PID: shim 的进程 id，同时是它的进程组 id（Kill 按组发信号靠它）
//   - LockPath: 存活锁路径；Alive 只看它，不看 PID（见包注释的 why）
type Handle struct {
	PID      int    `json:"pid"`
	LockPath string `json:"lock_path"`
}

// log 返回包日志入口（运行时取 slog.Default()，跟随 agentd 的 logx 配置）。
func log() *slog.Logger { return slog.Default().With("mod", "prochost") }

// Alive 报告 Handle 对应的 shim 是否仍然活着。
//
// 判据：LockPath 上的排他锁被占用。锁由内核在进程死亡时释放，因此本判定
// 不存在 pid 复用误判，也不需要任何清理代码配合。
//
// 返回：LockPath 为空、文件不存在或探测出错时一律返回 false（保守：宁可判死
// 后走恢复流程，也不要把死进程当活的导致任务永远卡住）。
func Alive(h Handle) bool {
	if h.LockPath == "" {
		return false
	}
	locked, err := IsLocked(h.LockPath)
	if err != nil {
		log().Debug("探测存活锁失败，按已死处理", "lock", h.LockPath, "cause", err)
		return false
	}
	return locked
}

// Kill 终止 shim 及其全部后代（按进程组发送 SIGKILL）。
//
// 幂等：锁已空闲说明 shim 已死，直接返回 nil——**绝不对该 pid 发任何信号**，
// 因为它可能已被操作系统复用给毫不相干的进程（workspace.go 的历史教训：
// 旧实现 300 条成功命令误杀 114 次）。
//
// 返回：仅当「确认还活着但杀不掉」时返回错误。
func Kill(h Handle) error {
	if h.PID <= 0 {
		return nil
	}
	if !Alive(h) {
		log().Info("存活锁已释放，无需回收", "pid", h.PID, "lock", h.LockPath)
		return nil
	}
	log().Info("回收执行者进程组", "pid", h.PID)
	if err := killGroup(h.PID); err != nil {
		log().Error("回收执行者进程组失败", "pid", h.PID, "cause", err)
		return fmt.Errorf("回收进程组 %d: %w", h.PID, err)
	}
	return nil
}

// CreateInputChannel 幂等创建输入通道（unix 为 0600 命名管道）。
//
// 参数：path 为通道路径（通常是 <taskDir>/in.fifo）
//
// 返回：
//   - 已存在且确实是命名管道 → nil（复用）
//   - 已存在但是普通文件/目录 → 错误（残留物会让 shim 的 O_RDWR 打开语义完全改变，
//     必须显式失败而不是静默当管道用）
//   - Windows → not implemented（A 期）
func CreateInputChannel(path string) error { return createInputChannel(path) }

// WaitInputReader 等待输入通道上出现读端（shim 已执行到持有 FIFO 那一步）。
//
// 为什么必须等：写端以 O_WRONLY|O_NONBLOCK 打开 FIFO 时，POSIX 规定读端未就绪
// 直接失败（ENXIO，macOS 文案 "device not configured"）。Start 返回只代表 shim
// 已被 fork，不代表它已经打开了 FIFO——这个竞态在 tmux 时代真机复现过
// （8fca917 次生缺陷），换成 shim 后窗口更小但依然存在。
//
// 参数：
//   - path: 通道路径
//   - timeout: 等待上限；到点仍无读端返回错误
//
// 返回：等待耗时（调用方记日志）与错误。非「无读者」类错误立即返回，不重试。
func WaitInputReader(path string, timeout time.Duration) (time.Duration, error) {
	return waitInputReader(path, timeout)
}

// Start 以 detached 方式拉起 shim，由 shim 承载 spec 描述的执行者进程。
//
// 参数：
//   - spec: 启动描述；LockPath/InfoPath/Stdout 必填，Argv[0] 必须是绝对路径
//   - selfExe: handoff 自身可执行文件路径（os.Executable() 的结果）
//   - extraArgs: 附加给 selfExe 的参数；生产传空，测试用它指向测试二进制的 shim 入口
//
// 返回：
//   - Handle（PID 为 shim 的 pid，同时是进程组 id）；spec 落盘失败或 fork 失败时返回错误
//
// 注意：
//   - 返回只代表 shim 已被 fork，**不代表它已持锁或已打开 FIFO**。调用方若要
//     投递输入，必须先 WaitInputReader；若要判存活，必须轮询 Alive 而非立即断言
//   - spec.json 以 0600 落在 InfoPath 同目录：它含完整 env（可能有凭据），
//     权限不能放宽
func Start(spec Spec, selfExe string, extraArgs ...string) (Handle, error) {
	specPath := filepath.Join(filepath.Dir(spec.InfoPath), "spec.json")
	b, err := json.Marshal(spec)
	if err != nil {
		return Handle{}, fmt.Errorf("序列化 spec: %w", err)
	}
	if err := os.WriteFile(specPath, b, 0o600); err != nil {
		return Handle{}, fmt.Errorf("写 spec %s: %w", specPath, err)
	}
	argv := append([]string{selfExe}, extraArgs...)
	argv = append(argv, "_shim", "--spec", specPath)
	pid, err := spawnDetached(argv, spec.Dir)
	if err != nil {
		log().Error("拉起 shim 失败", "spec", specPath, "cause", err)
		return Handle{}, err
	}
	log().Info("shim 已拉起", "pid", pid, "bin", spec.Argv[0], "spec", specPath)
	return Handle{PID: pid, LockPath: spec.LockPath}, nil
}
