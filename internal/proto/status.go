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

// BuildInfo 是一个 handoff 二进制的构建标识，取自 runtime/debug.ReadBuildInfo。
//
// 字段说明：
//   - Revision: vcs.revision；**空串表示不是 go build 产物**（go run / 测试
//     二进制没有 vcs 戳），调用方应显示「版本未知」而不是空
//   - Time: vcs.time
//   - Modified: vcs.modified——true 表示这个二进制是带未提交改动编出来的，
//     它对不上任何一个提交，排障时这是关键信息
//   - Go: 编译所用 Go 版本
type BuildInfo struct {
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
}
