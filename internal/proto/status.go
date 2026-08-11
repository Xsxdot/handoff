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
}

// UpdateStatus 是自动更新的当前状态。
//
// 字段说明：
//   - Pending: 已下载待命的版本；空串表示没有待命更新
//   - DownloadedAt: 下载完成时刻，用于展示「等了多久」
//   - Managed: 当前 agentd 进程是不是被进程管理器拉起的。**false 时自动换版
//     被拒绝**，这是用户唯一能看出「为什么更新一直不生效」的地方
type UpdateStatus struct {
	Pending      string    `json:"pending,omitempty"`
	DownloadedAt time.Time `json:"downloaded_at,omitempty"`
	Managed      bool      `json:"managed"`
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
}
