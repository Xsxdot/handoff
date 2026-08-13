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

	// Watchers 是当前订阅该任务事件流的连接数（几个审核者在听）。
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
//
// 为什么没有「待命版本」了：B59 取消了「下载完等空闲窗口再换」的自主决策，
// 换版由操作者一条命令触发并当场完成，中间不存在待命态（见 B59 spec D1）。
type UpdateStatus struct {
	Managed bool `json:"managed"`
}

// StatusResp 是 GET /api/status 的响应。
//
// 注意：TaskCounts 的六个状态键恒存在，计数为零也出现——缺键与零值对消费方
// 是两回事。
type StatusResp struct {
	Version         BuildInfo      `json:"version"`
	Listen          string         `json:"listen"`
	DataDir         string         `json:"data_dir"`
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
	// idle 时长的**诊断**降级成一句「我没收到东西」——审核者拿到的信息严格更少。
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

	// PtySessions 是当前活着的终端会话数。指针 + omitempty，与 Proc 同一纪律：
	// nil = 对端没上报，渲染时整行不打印；0 = 确实一个都没有。
	//
	// 为什么 status 只给个数、不给每个会话占多少进程：数进程要枚举全机进程，
	// 而 status 有「不能变成慢命令」的硬纪律。进程数在 /api/footprint 里给。
	PtySessions *int `json:"pty_sessions,omitempty"`
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
