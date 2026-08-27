// status.go —— handoff status 的响应线格式。
//
// 职责：
//   - 定义 BuildInfo / ActiveTask / StatusResp 三个结构与 Live* 取值常量
//
// 边界：
//   - 只有数据，无行为、无 I/O（与本包其余部分同规格）
//   - 不定义「怎么展示」：文本渲染归 cmd/status.go
package proto

import "time"

// BuildInfo 是一个 handoff 二进制的构建标识。
//
// 字段说明：
//   - Version: release 版本号（形如 v0.1.0），构建时由 ldflags 注入；
//     **空串表示不是 release 构建**（本地 go build / go run / 测试二进制），
//     此时调用方应退回 Revision 展示
//   - Revision: vcs.revision；**空串表示不是 go build 产物**（go run / 测试
//     二进制没有 vcs 戳），调用方应显示「版本未知」而不是空
//   - Time: vcs.time
//   - Modified: vcs.modified——true 表示这个二进制是带未提交改动编出来的，
//     它对不上任何一个提交，排障时这是关键信息
//   - Go: 编译所用 Go 版本
//
// 为什么 Version 与 Revision 并存而不是二选一：它们回答不同的问题。
// Version 回答「该不该更新」（自动更新比的是它），Revision 回答「出问题的
// 是哪个提交」（排障比的是它）。release 构建两者都有。
type BuildInfo struct {
	Version  string `json:"version,omitempty"`
	Revision string `json:"revision"`
	Time     string `json:"time"`
	Modified bool   `json:"modified"`
	Go       string `json:"go"`

	// Platform 是构建目标平台，形如 "linux/amd64"，在 buildinfo.Read() 里用
	// runtime.GOOS + "/" + runtime.GOARCH 现算填入（CLI 与 agentd 同一条路径，
	// 不会出现只有一端填的情况）。
	//
	// **空串表示对端没给这个字段**（老 agentd）。此时远程升级必须明确拒绝而不是
	// 猜一个默认值——猜错就是给一台 linux 机器推一个 darwin 二进制，自检会拦下，
	// 但那是白跑一次 15MB 上传换来的一条晦涩错误。
	Platform string `json:"platform,omitempty"`
}

// ActiveTask.Live 的三个取值。
//
// 为什么必须有 unknown：探不出结论时猜一个值就是在制造假阳性，而一条会说谎的
// 诊断命令比没有更糟——因为你会信它。
const (
	LiveAlive   = "alive"
	LiveDead    = "dead"
	LiveUnknown = "unknown"
)

// ActiveTask 是一个非终结任务及其 executor 存活结论。
//
// 注意：ID 始终是完整 UUID。文本渲染可以只显示前 8 位（与执行者进程的短 id 展示
// handoff-<id8> 一致，便于人肉对照），但任何拿去当参数的地方都必须用完整 UUID
// ——store.GetTask 是精确匹配，不做前缀查找。
type ActiveTask struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	State    string `json:"state"`
	Executor string `json:"executor"`
	RepoPath string `json:"repo_path"`
	Live     string `json:"live"` // LiveAlive / LiveDead / LiveUnknown
	Note     string `json:"note"` // 判死或判不出的一句话理由；alive 时为空

	// Watchers 是当前订阅该任务事件流的连接数（几个协调者在听）。
	//
	// 为什么是指针：nil 表示**对端没给这个字段**（老 agentd），与「确实是 0」
	// 是两回事。猜一个 0 就是在制造假阳性——与 Live 三态用 unknown 而不猜死
	// 是同一条纪律：一条会说谎的诊断命令比没有更糟，因为你会信它。
	Watchers *int `json:"watchers,omitempty"`

	// Procs 是该任务当前占用的进程数。
	//
	// 为什么是指针：nil 表示**取不到这个信息**（老 agentd、adapter 不支持、
	// 平台不支持、或 pgid 判定为复用/凭据不全），与「确实是 0 个进程」是两回事。
	// 猜一个 0 就是制造假阳性——与 Watchers、Live 三态同一条纪律。
	Procs *int `json:"procs,omitempty"`
}

// ProcUsage 是本机当前 uid 的进程占用与上限。
//
// 为什么两个数必须一起给：只看 Used 不知道离墙还有多远，只看 Limit 没有意义。
// 2026-08-12 devbox 整机 fork 瘫痪时，346/2666 这两个数并排才说明得了问题。
type ProcUsage struct {
	Used  int `json:"used"`
	Limit int `json:"limit"`
}

// UpdateStatus 是这台 agentd 与「换版」有关的状态。
//
// 字段说明：
//   - Managed: 当前 agentd 进程是不是被进程管理器（systemd / launchd）拉起的。
//     **false 时换版被硬拒绝**——换完 exit(0) 之后没人拉起，这台机器上就此
//     没有 agentd 在跑，且没有任何信号告诉任何人。`--force` 也不越过这一条
//   - Pull: 本 agentd 支持「自拉换版」（POST /api/update?mode=pull）
//   - PullState: 最近一次自拉的状态；nil = 本进程还没自拉过，或上一次已成功
//     （成功的终点是重启，状态随进程一起消失）
//
// 为什么没有「待命版本」了：B59 取消了「下载完等空闲窗口再换」的自主决策，
// 换版由操作者一条命令触发并当场完成，中间不存在待命态（见 B59 spec D1）。
type UpdateStatus struct {
	Managed bool `json:"managed"`

	// Pull 表示对端支持自拉换版。
	//
	// 为什么是指针：nil 表示**对端没给这个字段**（老 agentd 不上报），与
	// 「对端说 false」是两回事。这条区分是选路判据——老 agentd 收到
	// mode=pull + 空 body 会掉进「纯重启」分支并回 200，CLI 若据此以为
	// 受理了，就会干等到超时报「已换版但新进程未上线」，一次纯误导。
	// 与同结构族里 BuildInfo.Platform 空串、ActiveTask.Watchers *int 同款纪律。
	Pull *bool `json:"pull,omitempty"`

	// PullState 是最近一次自拉换版的状态，仅存内存、不落盘。
	//
	// 为什么没有 done 态：成功路径的终点是**进程重启**，状态自然消失——
	// 而那时 status 报的版本号已经变了，调用方靠版本号就能确认。一个落盘的
	// done 会在下次启动时变成误导性的陈旧数据。失败时进程不重启，状态留在
	// 内存里可查，这正是需要它的场合。
	PullState *PullState `json:"pull_state,omitempty"`
}

// PullState 是一次自拉换版的进度与结局。
type PullState struct {
	Tag   string `json:"tag"`
	Stage string `json:"stage"` // PullStage* 之一
	// Error 是 Stage=failed 时的原文。**必须带原文**：调用方拿到它才能
	// 直接看到 "proxyconnect tcp: dial tcp 127.0.0.1:1080: connection refused"
	// 这种一眼定位的信息，而不是一句「版本仍是 X」
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// 自拉的阶段取值。
//
// 只有三个：没有 "done"（见 UpdateStatus.PullState 的注释——成功的终点是重启），
// 也没有单独的 "verifying"（sha256 比对与解包后自检都发生在 installing 内部）。
// **不要为了让阶段看起来更完整而加一个实现从不产出的取值**——消费方会写死
// 代码去处理它，而那段代码永远不会被执行，也永远不会被测到。
const (
	PullStageDownloading = "downloading"
	PullStageInstalling  = "installing"
	PullStageFailed      = "failed"
)

// StatusResp 是 GET /api/status 的响应。
//
// 注意：TaskCounts 的六个状态键恒存在，计数为零也出现——缺键与零值对消费方
// 是两回事。
type StatusResp struct {
	Version BuildInfo `json:"version"`
	Listen  string    `json:"listen"`

	// ListenAux 是 loopback 辅助监听地址（B85）：Listen 为单网卡 IP 时 agentd
	// 额外监听 "127.0.0.1:<同端口>"，本机 CLI 的确定性改写拨的就是它。
	// 空 = 无辅助监听（Listen 为 loopback/通配，或对端是老 agentd）。
	ListenAux string `json:"listen_aux,omitempty"`

	DataDir string `json:"data_dir"`
	// ScratchRoot 是草稿区的绝对路径（<DataDir>/scratch），控制台浮窗的临时文件
	// 落在这里。**缺席 = 这台机器不支持临时文件**（老 agentd，或目录建不出来）。
	//
	// 与 PtySupported 那种能力位的三态纪律不同：那里 nil 要按「不知道，放行」处理，
	// 而这里缺的是一个**路径**——没有路径就没法发请求，放行只会换来一次必然 400。
	ScratchRoot     string         `json:"scratch_root,omitempty"`
	StartedAt       time.Time      `json:"started_at"`
	Executors       []string       `json:"executors"`
	DefaultExecutor string         `json:"default_executor"`
	TaskCounts      map[string]int `json:"task_counts"`
	Active          []ActiveTask   `json:"active"`

	// StallTimeout 是 agentd 看门狗判定「卡住」的空闲阈值，形如 "2h0m0s"
	//（time.Duration.String()）。空串 = 对端未提供。
	//
	// 为什么要外露：wait --follow 的 --timeout 若不大于它，两个计时器同时到点时
	// 客户端的 124 会抢在 agentd 的 stalled 前面退出进程，把一次带 last_seq 和
	// idle 时长的**诊断**降级成一句「我没收到东西」——协调者拿到的信息严格更少。
	StallTimeout string `json:"stall_timeout,omitempty"`

	// Update 是自动更新状态。**指针 + omitempty**：老版本 agentd 不发这个字段，
	// 消费方拿到 nil 就该什么都不显示，而不是显示一个「未托管、无待命」的假状态
	Update *UpdateStatus `json:"update,omitempty"`

	// Proc 是本机 uid 级的进程占用与上限。指针 + omitempty：老 agentd 不发这个
	// 字段，消费方拿到 nil 应当什么都不显示，而不是显示一个「0/0」的假状态。
	Proc *ProcUsage `json:"proc,omitempty"`

	// PtySupported 报告本机 agentd 是否支持 PTY 终端。
	//
	// 三态，与 Update / Proc 同一纪律：
	//   缺席(nil) = 对端 agentd 太老，没上报这个字段——**不许当成 false**
	//   false     = 平台不支持（Windows：ConPTY 是另一套 API，本轮不假装支持）
	//   true      = 支持
	// 前端据此决定画真终端、画「这台机器不支持」还是画「对端版本过旧，未上报」。
	PtySupported *bool `json:"pty_supported,omitempty"`

	// LaunchersSupported 报告本机 agentd 是否认识启动项（CreatePtySessionReq
	// 的 env_file / init_command 两个字段）。
	//
	// **三态的处置与 PtySupported / RevealSupported 刻意相反，别照抄邻居**：
	//   缺席(nil) = 对端 agentd 太老 → **按不支持处置**（不送这两个字段、
	//               界面不展示该机的启动项）
	//   false     = 不支持
	//   true      = 支持
	//
	// 为什么反着来：那两个能力位缺席时「放行」的代价只是一次必然失败的请求，
	// 用户当场看得见；而这里放行的代价是**静默起一个没有环境变量的终端**
	// （请求 200、终端正常出现、变量悄悄不在）。未知时的保守方向由「失败可不
	// 可见」决定，不由邻居的写法决定。
	//
	// 能力位与实现同生同死：上报 true 就必须真的实现这两个字段，不允许
	// 「先上报、下一版补实现」。
	LaunchersSupported *bool `json:"launchers_supported,omitempty"`

	// DisciplinesSupported 报告本机 agentd 是否认识「接收下发的纪律正文」
	// （/api/tasks 的 discipline_text / discipline_version，B229）。
	//
	// 三态的处置与 PtySupported / RevealSupported 刻意相反、与 LaunchersSupported
	// 同向：缺席(nil) = 对端太老 → 按不支持处置（协调者侧拒发）；false = 不支持；
	// true = 支持。放行的代价是静默降级——请求 200、任务正常创建、纪律块悄悄
	// 变成执行机本地残留（B229 缺陷三的原样复活），所以未知时按不支持拒发。
	//
	// 能力位与实现同生同死：收文即用、正文落盘、continue/resume 消费落盘正文
	// 三件齐了才许上报 true。
	DisciplinesSupported *bool `json:"disciplines_supported,omitempty"`

	// RevealSupported 报告本机 agentd 是否支持「在访达中显示」（B108）。
	//
	// 三态与 PtySupported 逐字相同：
	//   缺席(nil) = 对端 agentd 太老，没上报这个字段——**不许当成 false**
	//   false     = 平台不支持（只有 macOS 有 `open -R` 这个语义）
	//   true      = 支持
	//
	// 注意：这只是**平台**支持度。真能不能揭示还要看调用方是不是从回环来的
	//（远程浏览器点了会在 agentd 那台机器的桌面上弹窗，没人看得见），那一层
	// 由端点自己判，不进能力位——它是每请求的属性，不是机器的属性。
	RevealSupported *bool `json:"reveal_supported,omitempty"`

	// PtySessions 是当前活着的终端会话数。指针 + omitempty，与 Proc 同一纪律：
	// nil = 对端没上报，渲染时整行不打印；0 = 确实一个都没有。
	//
	// 为什么 status 只给个数、不给每个会话占多少进程：数进程要枚举全机进程，
	// 而 status 有「不能变成慢命令」的硬纪律。进程数在 /api/footprint 里给。
	PtySessions *int `json:"pty_sessions,omitempty"`

	// WebEmbedded 报告本机是否编译进 Web 控制台。
	//
	// nil = 对端未上报，false = 当前二进制是 stub，true = 已嵌入。
	// 使用指针保证非 nil 的 false 不被 omitempty 省略。
	WebEmbedded *bool `json:"web_embedded,omitempty"`
}

// PtyFootprintRow 是一个终端会话的足迹体检结果。
//
// Procs 为指针：数不出来（平台不支持枚举）时是 nil，**不是 0**——与 ProcUsage
// 同一条理由，0 看起来像结论。
type PtyFootprintRow struct {
	ID         string `json:"id"`
	BasePath   string `json:"base_path"`
	PID        int    `json:"pid"`
	Procs      *int   `json:"procs,omitempty"`
	Foreground bool   `json:"foreground"`
}

// FootprintRow 是一个任务的进程足迹体检结果。
//
// 注意：Verdict 恒非空。判不出结论时给 leader_reuse / no_credential，而不是
// 把 Procs 抹成 0 了事——「没有残留」与「我们不敢下结论」是两回事，后者需要
// 人工看一眼，前者不需要。
type FootprintRow struct {
	TaskID  string `json:"task_id"`
	Name    string `json:"name"`
	State   string `json:"state"`
	Procs   int    `json:"procs"`
	Verdict string `json:"verdict"`
}

// FootprintResp 是 GET /api/footprint 的响应：全部任务（含已归档）的足迹体检。
type FootprintResp struct {
	Rows  []FootprintRow `json:"rows"`
	Usage *ProcUsage     `json:"usage,omitempty"`

	// Pty 是终端会话的足迹。会话只在内存里，所以这一段与 Rows 不同——
	// 它不含历史，列出的都是此刻活着的会话。
	Pty []PtyFootprintRow `json:"pty,omitempty"`
}
