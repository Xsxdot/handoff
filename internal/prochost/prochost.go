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
	"errors"
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
//   - InputCh: 可选。非空时 shim 经 openInputChannel 准备子进程 stdin（平台各自实现）
//   - LockPath: shim 的存活锁路径
//   - InfoPath: adapter 的 proc.json 路径。shim **不写它**（那是 adapter 的独占
//     文件），只拿它的所在目录来放 spec.json 与 child.pid——见 shim.go recordChildPID
//   - Sentinel: true 时子进程退出后向 Stdout 追加 handoff_exit 哨兵行
//   - NprocLimit: 执行者树的进程数围栏（0 = 不设）；由 Start 按策略算出，调用方不填
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

	// NprocLimit 是这棵执行者进程树的围栏值（RLIMIT_NPROC 软硬限）。
	//
	// 0 = 不设围栏。为什么用零值而不是指针：这个字段没有「对端没发」与
	// 「对端发了 0」的区分需求——两者都表示不装，语义完全一致。
	//
	// omitempty + 零值语义同时保证了滚动升级安全：新版 agentd 写出的
	// spec.json 被旧版 shim 读到时该字段被忽略（旧 shim 不认识），新版 shim
	// 读到升级前的 spec.json 得到 0 则跳过安装——两个方向都不会出事。
	NprocLimit int `json:"nproc_limit,omitempty"`

	// TaskID 是本任务的 UUID，Start 据它注入 HANDOFF_TASK_ID 环境变量，
	// 并回填进 Handle 供归属判定使用。
	//
	// omitempty + 零值语义：为空则不注入、不参与归属判定。旧版 shim 读到新版
	// spec.json 会忽略该字段；新版 shim 读到旧 spec.json 得到空串则判据不参与——
	// 两个方向都不会出事，与 NprocLimit 同款滚动升级纪律。
	TaskID string `json:"task_id,omitempty"`

	// MarkRoot 是 cwd 归属判据的比对根（已解析符号链接的绝对路径）。
	//
	// **只在托管 worktree 形态下由调用方填写**：空串即本任务不启用 cwd 归属。
	// 把「仅托管 worktree 可杀」编码进数据而不是运行时再判一次，是为了让这条
	// 边界没有「某处忘了检查」的可能。
	MarkRoot string `json:"mark_root,omitempty"`
}

// Handle 是一个已拉起的 shim 的句柄，可直接序列化进 adapter 的 proc.json。
//
// 字段说明：
//   - PID: shim 的进程 id，同时是它的进程组 id（Kill 按组发信号靠它）
//   - LockPath: 存活锁路径；Alive 只看它，不看 PID（见包注释的 why）
type Handle struct {
	PID      int    `json:"pid"`
	LockPath string `json:"lock_path"`

	// StartedAt 是 shim 进程的启动时刻（unix 纳秒），足迹身份校验的时间下界。
	//
	// 为什么读内核而不是记墙钟：规则三要把**成员**的启动时刻与它直接比较，而成员
	// 的时刻来自内核（darwin p_starttime / linux /proc starttime）。两边取自同一个
	// 时钟源才可比——记 time.Now() 会引入毫秒级偏差，linux 的 jiffies 精度（10ms）
	// 下足以让紧随其后 fork 的子进程「看起来比父进程还早」，从而被规则三误排除。
	//
	// omitempty + 零值语义：升级前写下的 proc.json 没有这个字段，读出 0 即判
	// VerdictNoCredential 降级为只上报不清扫。老任务不会因为升级就被动手。
	StartedAt int64 `json:"started_at,omitempty"`

	// RosterPath 是后代名册（roster.json）的路径，第二段清扫的入口。
	//
	// 为什么要记在 Handle 里而不是让 Sweep 自己推：Sweep 跑在 agentd 进程里，
	// 手上只有 proc.json 反序列化出来的 Handle——它没有 spec，也就没有
	// InfoPath，推不出任务目录。这个字段是两个进程之间唯一的交接点。
	//
	// omitempty + 零值语义：升级前写下的 proc.json 没有这个字段，读出空串即
	// 跳过第二段清扫（只做 pgid 那段），与 StartedAt 缺失时降级为只上报是
	// 同一条纪律——老任务不会因为升级就被动手。
	RosterPath string `json:"roster_path,omitempty"`

	// MembersPath 是进程容器成员快照（members.json）的路径。
	//
	// 只有具备进程容器的平台（Windows 的 Job Object）会填它并落盘；unix 上恒为空串，
	// Footprint 因此自然落回 pgid + roster + 标记三段判据。升级前写下的 proc.json
	// 没有此字段，读出空串即跳过容器来源，不会改变老任务的清扫语义。
	MembersPath string `json:"members_path,omitempty"`

	// TaskID / MarkRoot 是归属判定的凭据，由 Start 从 Spec 原样带过来。
	//
	// omitempty + 零值语义：升级前写下的 proc.json 没有这两个字段，读出空串即
	// 跳过标记判据、只走 pgid + roster——与 StartedAt / RosterPath 缺失时同一条
	// 纪律，老任务不会因为升级就被动手。
	TaskID   string `json:"task_id,omitempty"`
	MarkRoot string `json:"mark_root,omitempty"`
}

// cred 把 Handle 投影成一次归属判定所需的凭据。
//
// 为什么要单独一层而不是让判据直接吃 Handle：判据只需要这两个字段，
// 传整个 Handle 会让 taskmark.go 依赖 PID/LockPath 这些与它无关的东西，
// 单测也得构造无关字段。
func (h Handle) cred() TaskCred {
	return TaskCred{TaskID: h.TaskID, MarkRoot: h.MarkRoot}
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

// ErrStillAlive 表示已发出 SIGKILL 且复核窗口走完，进程组仍然存活。
//
// 与「信号发送失败」区分开：后者是系统调用出错（可能只是权限或参数问题），
// 前者是进程**真的没死**——只有后一种意味着会留下长期孤儿，值得惊动人。
// agentd 侧靠 errors.Is 认这个哨兵来决定要不要给协调者发提示事件。
var ErrStillAlive = errors.New("进程组仍然存活")

// killVerifyWindow 是复核存活的总时长上限，killVerifyBackoff 的各项之和。
//
// 为什么是 1s 而不是更久：Kill 处在归档/中止的同步路径上，它变慢等于
// handoff done / handoff stop 变慢。1s 足以覆盖 SIGKILL 的正常生效窗口；
// 超过 1s 还活着的本来就该交给人和后台重试，而不是让协调者对着终端干等。
const killVerifyWindow = time.Second

// killVerifyBackoff 是 killGroup 之后逐次复核的等待序列（累计 = killVerifyWindow）。
//
// 为什么要退避而不是固定间隔：SIGKILL 异步生效，绝大多数进程在头几十毫秒内
// 就没了——前密后疏能让常见情况几乎不增加延迟，又不放弃慢死场景的覆盖。
//
// 是变量而非常量：测试要把它换成微秒级，否则每条复核用例都真等 1s。
var killVerifyBackoff = []time.Duration{
	10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond,
	80 * time.Millisecond, 160 * time.Millisecond, 320 * time.Millisecond,
	370 * time.Millisecond,
}

// aliveFn / killGroupFn / killProcFn 是包内测试接缝：SIGKILL 在类 Unix 上不可拦截，
// 真进程做不出「持锁但杀不死」或「出生时刻不符」的形态，只能靠替换这些函数驱动
// 复核失败与点名安全路径。**生产路径恒为下面这些默认值**，任何非测试代码都不得
// 赋值给它们。
var (
	aliveFn     = Alive
	killGroupFn = killGroup
	// killProcFn 是单 pid 发信号的测试接缝：名册点名的安全用例要断言
	// 「哪些 pid 被发了信号」，真进程做不出「出生时刻不符」这种形态
	killProcFn = killProc
)

// Kill 终止 shim 及其全部后代（按进程组发送 SIGKILL），并**复核它是否真的死了**。
//
// 幂等：锁已空闲说明 shim 已死，直接返回 nil——**绝不对该 pid 发任何信号**，
// 因为它可能已被操作系统复用给毫不相干的进程（workspace.go 的历史教训：
// 旧实现 300 条成功命令误杀 114 次）。
//
// 参数：
//   - h: 目标 shim 的 Handle；PID <= 0 视为无进程可杀，直接 nil
//
// 返回：
//   - nil: 已确认进程组退出（或本来就已经死了）
//   - 包装 ErrStillAlive 的错误: 信号发出去了，但复核窗口（killVerifyWindow）
//     走完进程仍存活——调用方应保留运行态、上抛给 agentd 提示人工
//   - 其它错误: 信号发送本身失败
//
// 注意：
//   - 复核判据用 Alive（文件锁）而非 kill(pid, 0)：锁由内核在进程死亡时释放，
//     不存在 pid 复用误判，而 kill(pid,0) 会把「pid 被复用」误报成「还活着」
//   - 本函数在确认死亡前不返回，因此调用方紧随其后的资源清理
//     （如 RemoveManagedWorktree）天然排在进程真死之后，不需要额外同步
func Kill(h Handle) error {
	if h.PID <= 0 {
		return nil
	}
	if !aliveFn(h) {
		log().Info("存活锁已释放，无需回收", "pid", h.PID, "lock", h.LockPath)
		return nil
	}
	log().Info("回收执行者进程组", "pid", h.PID)
	if err := killGroupFn(h.PID); err != nil {
		log().Error("回收执行者进程组失败", "pid", h.PID, "cause", err)
		return fmt.Errorf("回收进程组 %d: %w", h.PID, err)
	}
	for i, d := range killVerifyBackoff {
		time.Sleep(d)
		if !aliveFn(h) {
			log().Info("回收完成，已确认进程组退出", "pid", h.PID, "probe", i+1)
			return nil
		}
	}
	log().Error("已发 SIGKILL 但复核窗口走完仍存活，可能有逃逸出进程组的后代",
		"pid", h.PID, "lock", h.LockPath, "window", killVerifyWindow)
	return fmt.Errorf("%w: pid=%d，已发 SIGKILL 并复核 %s", ErrStillAlive, h.PID, killVerifyWindow)
}

// CreateInputChannel 幂等创建输入通道（unix 为 0600 命名管道，Windows 由平台实现）。
//
// 参数：path 为通道路径（通常是 <taskDir>/in.fifo）
//
// 返回：
//   - 已存在且确实是命名管道 → nil（复用）
//   - 已存在但是普通文件/目录 → 错误（残留物会让平台输入通道语义完全改变，必须
//     显式失败而不是静默当通道用）
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

// WriteInputChannel 往输入通道投递一段字节。
//
// 参数：
//   - path: 通道路径（与 Spec.InputCh 同一个值）
//   - data: 原样投递的字节，本函数不加工、不追加换行
//
// 返回：读端不在、通道不存在、写失败时返回错误。
//
// 注意：
//   - **「打不开即读端不在」是承重语义**：unix 上以 O_WRONLY|O_NONBLOCK 打开，
//     读端未就绪时 POSIX 规定直接失败（ENXIO）；Windows 上 CreateFile 打不开
//     管道名报 ERROR_FILE_NOT_FOUND。两边都是调用方判定「执行者已不在」的依据，
//     实现不得改成阻塞等待或静默成功
//   - 本函数不做 JSON 序列化：那是 adapter 的协议知识，prochost 只搬字节
func WriteInputChannel(path string, data []byte) error {
	return writeInputChannel(path, data)
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
	applyFencePolicy(&spec)
	applyTaskMark(&spec)
	log().Debug("任务标记凭据已就位", "task_id", spec.TaskID,
		"mark_root", spec.MarkRoot, "env_injected", spec.TaskID != "")
	b, err := json.Marshal(spec)
	if err != nil {
		return Handle{}, fmt.Errorf("序列化 spec: %w", err)
	}
	if err := os.WriteFile(specPath, b, 0o600); err != nil {
		return Handle{}, fmt.Errorf("写 spec %s: %w", specPath, err)
	}
	argv := append([]string{selfExe}, extraArgs...)
	argv = append(argv, "_shim", "--spec", specPath)

	// shim 的 stderr 接进任务目录的 shim.log：shim 是独立进程，日志走 slog→stderr，
	// 不接的话会落进 /dev/null（spawnDetached 的 stdio=nil），撞墙归因那一行谁
	// 都看不到——2026-08-12 烟测实证「装了围栏却报 363/2400」的归因行确实被丢掉。
	// 打开失败只降级（shim 输出继续落 /dev/null），绝不阻断拉起。
	var shimLog *os.File
	if spec.InfoPath != "" {
		shimLogPath := filepath.Join(filepath.Dir(spec.InfoPath), "shim.log")
		shimLog, err = os.OpenFile(shimLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			log().Warn("打开 shim 日志文件失败，shim 输出将落入 /dev/null",
				"path", shimLogPath, "cause", err)
		} else {
			defer shimLog.Close()
		}
	}
	pid, err := spawnDetached(argv, spec.Dir, shimLog)
	if err != nil {
		log().Error("拉起 shim 失败", "spec", specPath, "cause", err)
		return Handle{}, err
	}
	// 读回内核记录的启动时刻作为身份校验的时间下界（why 见 Handle.StartedAt）。
	// 读不到不阻断拉起：shim 已经在跑了，为一个诊断字段把它杀掉是本末倒置——
	// 代价是这个任务此后只能上报、不能自动清扫，如实降级即可
	startedAt := lookupStartedAt(pid)
	if startedAt <= 0 {
		log().Warn("读不到 shim 启动时刻，该任务将只能上报残留、无法自动清扫",
			"pid", pid, "spec", specPath)
	}
	roster := rosterPath(spec.InfoPath)
	members := ""
	if containerSampleFn != nil {
		members = membersPath(spec.InfoPath)
	}
	log().Info("shim 已拉起", "pid", pid, "bin", spec.Argv[0], "spec", specPath,
		"started_at", startedAt, "roster", roster, "members", members)
	return Handle{
		PID:         pid,
		LockPath:    spec.LockPath,
		StartedAt:   startedAt,
		RosterPath:  roster,
		MembersPath: members,
		TaskID:      spec.TaskID,
		MarkRoot:    spec.MarkRoot,
	}, nil
}
