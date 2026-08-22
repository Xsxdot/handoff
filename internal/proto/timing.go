// timing.go —— 执行耗时的线格式与账目类型（2026-08-22 需求 A 契约）。
//
// 职责：
//   - TimingEntry：adapter 产出、store 消费的一条耗时账目（与 SpendEntry 同构）
//   - TaskTiming / TimingBucket：对账目求和后的聚合结果，挂在 Task 上
//
// 边界：
//   - 纯类型定义：不做 I/O、不认识任何具体 executor
//   - 不含「未归类」这一档的采集面——other 只在聚合层由差额产生（见 TaskTiming）
//
// 为什么与 SpendEntry 分成两套而不是合并：Spend 描述 token 与花费，Timing 描述
// 时间，两者的幂等键粒度不同（Spend 按上游消息 id，Timing 按回合内的段）。
// 合并会逼出一个「本条只有时间没有 token」的半空结构，而那正是 B83 的账本
// 明确避开的形态。
package proto

// TimingKind 是一条耗时账目的种类。
//
// **turn 不是「段」**：spec §A.3 的封闭段集合是 {api, tool, other}，turn 承载的
// 是回合墙钟——它是 other 的分母，不参与三分。把 turn 当段加进汇总会让总时长
// 被计两遍，且不报错（契约文档 §6.1 的拍板记录就是为拦这个而写的）。
type TimingKind string

const (
	// TimingKindAPI 是模型段：从回合开始（或上一批工具结果全部提交）到本轮模型输出结束。
	TimingKindAPI TimingKind = "api"
	// TimingKindTool 是工具段：单次工具调用从发出到结果返回。
	TimingKindTool TimingKind = "tool"
	// TimingKindTurn 是回合墙钟。不是段，见 TimingKind 的说明。
	TimingKindTurn TimingKind = "turn"
)

// TimingEntry 是一条待入账的耗时（adapter 产出，store 消费）。
//
// Key 必须在同一个任务内**稳定且唯一**——它是幂等的全部依据，同 Key 重复上报
// 按**覆盖**处理（不是累加）。这与 SpendEntry 是同一条语义，刻意不另立一套。
//
// 键的构造一律**从内容派生**，不用进程内计数器：
//   - tool: "tool/<turn>/<part>"（part 在回合内唯一）
//   - api:  "api/<turn>/<n>"，n = 本段之前已完成的工具批次数，首段为 0
//   - turn: "turn/<turn>"
//
// 用计数器的后果：agentd 重启或上游流重放后计数器归零，第二段会写成第一段的
// 键并覆盖掉真数据——而且账面上看不出任何异常。
type TimingEntry struct {
	Key   string
	Kind  TimingKind
	Turn  int
	DurMS int64
	// Label 是 Kind=tool 时的工具名（如 "Bash"）；其余种类为空。
	Label string
	// Detail 是 Kind=tool 时的命令/入参摘要，写入前按 200 rune 头尾截断；
	// 其余种类为空。**不得改存全文**（契约文档 §3.4 的凭据边界）。
	Detail string
	// OffsetMS 是 Kind=tool 时相对本回合起点的毫秒偏移；api / turn 恒为 0。
	//
	// 它存在的唯一理由是让聚合层算得出「工具占用的墙钟跨度」：只有 DurMS 算
	// 不出区间并集。删掉它之后，并发工具（claude 一次发多个 tool_use）的任务
	// OtherMS 会系统性地静默变成 0。
	OffsetMS int64
}

// TimingBucketCap 是聚合结果里每层分桶的条数上限。
//
// 有上限不是性能优化：没有上限时，一个跑了几百次不同命令的任务会把整份
// 排行怼进响应，而排行第 40 名之后的信息对「哪条命令慢」这个问题毫无价值。
const TimingBucketCap = 20

// TimingBucket 是按标签聚合的一格耗时。
//
// Sub 只下钻**一层**（工具名 → 命令首词）：再深一层要引入命令解析规则，
// 那是一条会随 shell 用法漂移的规则，不值得钉进契约。
type TimingBucket struct {
	Label string         `json:"label"`
	DurMS int64          `json:"dur_ms"`
	Count int            `json:"count"`
	Sub   []TimingBucket `json:"sub,omitempty"`
}

// TaskTiming 是一个任务的耗时聚合快照。
//
// 边界：本结构由 Store.TaskTiming 对 task_timing_ledger 求和产出，
// 一条账目都没有时返回 nil ——**绝不返回零值结构**。理由与 Cumulative 逐字
// 相同：0 会被读成「一共花了 0」，而真相是「还不知道」。
type TaskTiming struct {
	// TotalMS 是各回合墙钟之和（Σ kind=turn 的条目）。
	TotalMS int64 `json:"total_ms"`
	// APIMS 是模型段之和。
	APIMS int64 `json:"api_ms"`
	// ToolMS 是各工具段**时长之和**；并发工具时它可以大于 ToolSpanMS。
	ToolMS int64 `json:"tool_ms"`
	// ToolSpanMS 是工具占用的**墙钟跨度**（同回合内工具段区间的并集之和）。
	// 它与 ToolMS 同时给出，互不冒充——取其一当另一个用，就是在撒谎。
	ToolSpanMS int64 `json:"tool_span_ms"`
	// OtherMS 是未归类时间：max(0, TotalMS − APIMS − ToolSpanMS)。
	// 承载排队、等审批、框架开销。**绝不摊进 APIMS**（spec §A.3 规则 1）。
	OtherMS int64 `json:"other_ms"`
	// Partial 为真表示至少有一个回合缺 api 或 tool 条目，OtherMS 因此偏大。
	// 界面必须能读出这一点，不得把 Partial 的 OtherMS 当真实空档展示。
	Partial bool `json:"partial"`
	// Buckets 是按工具名聚合的排行，降序，最多 TimingBucketCap 条。
	Buckets []TimingBucket `json:"buckets,omitempty"`
}
