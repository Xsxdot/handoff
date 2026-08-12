// update.go —— POST /api/update 的线格式。
//
// 职责：
//   - 定义换版接口的成功响应、拒绝响应与可判别的拒绝原因常量
//
// 边界：
//   - 只有数据，无行为、无 I/O（与本包其余部分同规格）
//   - 请求参数不在这里：tag / sha256 / force 走 query，body 是 tar.gz 原文，
//     没有 JSON 请求体可定义
package proto

// UpdateResp 是换版成功的响应。
//
// Restarted 恒为 true——接口返回 200 就意味着 agentd 随后会触发优雅关停。
// 保留这个字段是为了让消费方读代码时不必去猜「返回之后还会发生什么」。
type UpdateResp struct {
	OK        bool   `json:"ok"`
	Version   string `json:"version,omitempty"` // 换上的版本；纯重启模式为空
	Prev      string `json:"prev,omitempty"`    // 旧二进制留存路径，回滚要用
	Restarted bool   `json:"restarted"`
}

// 换版被拒的可判别原因。
//
// 为什么要机器可读而不只给一句人话：两种拒绝的处置**完全不同**——活跃任务
// 可以 --force 越过，非托管不行。CLI 要据此选处置建议，而给一条注定失败的
// 命令比不给更糟（spec §4.6）。
const (
	// UpdateReasonBusy: 有 running / waiting_answer 任务，且未带 force
	UpdateReasonBusy = "busy"
	// UpdateReasonUnmanaged: agentd 非托管启动，换完没人拉起。force 不越过
	UpdateReasonUnmanaged = "unmanaged"
)

// UpdateError 是换版被拒的响应体。
//
// Reason 为空表示这次失败不属于上面两道闸（参数错、校验不过、自检不过等），
// 此时消费方**不该编处置建议**，只报原始错误原文。
type UpdateError struct {
	Error  string `json:"error"`
	Reason string `json:"reason,omitempty"`
}
