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

// 换版模式，走 query 参数 mode。
//
// 为什么要显式 mode 而不靠「tag 有没有」隐式判别：现有判别已经压在
// 「body 空不空」这一个维度上，再叠一层「tag 有没有」，三种模式的判据就散在
// 两个维度上，加第四种时必然出错。显式 mode 还让新旧 agentd 的分歧点变成
// 一个可测的单点。
const (
	// UpdateModePull: 只下发 tag + sha256，由 agentd 自己去下载（body 必须为空）
	UpdateModePull = "pull"
	// UpdateModePush: 协调者推 tar.gz 原文（body 必须非空）。省略 mode 且 body
	// 非空等价于本模式，这是为了让老 CLI 的请求继续被正确处理
	UpdateModePush = "push"
)

// UpdateResp 是换版成功的响应。
//
// Restarted 恒为 true——接口返回 200 就意味着 agentd 随后会触发优雅关停。
// 保留这个字段是为了让消费方读代码时不必去猜「返回之后还会发生什么」。
type UpdateResp struct {
	OK      bool   `json:"ok"`
	Version string `json:"version,omitempty"` // 换上的版本；纯重启模式为空
	Prev    string `json:"prev,omitempty"`    // 旧二进制留存路径，回滚要用

	// Accepted 表示这次请求只是被**受理**，换版还没发生（自拉模式，202）。
	// 与 Restarted 的区别是时态：Restarted 说"我这就重启"，Accepted 说
	// "我开始下载了，结果去 status 里看"。调用方据此决定是直接等版本号变，
	// 还是要一路盯着 pull_state
	Accepted bool `json:"accepted,omitempty"`

	Restarted bool `json:"restarted"`
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

	// UpdateReasonPullInProgress: 已有一个自拉在跑。force 不越过——两个自拉
	// 会往同一个临时文件路径（release.TempName(tag) 是确定性的）写，
	// 互相截断出一个坏二进制，而 Activate 会把它装上去
	UpdateReasonPullInProgress = "pull_in_progress"
)

// UpdateError 是换版被拒的响应体。
//
// Reason 为空表示这次失败不属于上面两道闸（参数错、校验不过、自检不过等），
// 此时消费方**不该编处置建议**，只报原始错误原文。
type UpdateError struct {
	Error  string `json:"error"`
	Reason string `json:"reason,omitempty"`
}
