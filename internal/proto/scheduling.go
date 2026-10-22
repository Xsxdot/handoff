// 编制域 wire 面（B156.3 K3）：squads/queue 端点与协调者拉起响应的请求/响应
// DTO。字段名与 web/src/api/scheduling.ts 镜像一字不差；线格式由
// TestContractFixtures 逐字节钉住（改字段先看红，再 -update 显式刷新）。
//
// 边界：proto 不 import 编制域——View 类型是独立投影，scheduling.Carrier/Squad
// → View 的手写投影在 agentd（schedapi.go），该边界由其 wire 往返测试锁。
package proto

import "time"

// CoordinatorRebindReq 是协调者换绑请求。HTTP 控制面只接受 mode=launch；
// mode=self 由 CLI 本机账本路径执行，identity 不属于 HTTP 可信输入。
type CoordinatorRebindReq struct {
	Mode     string `json:"mode"`
	Identity string `json:"identity,omitempty"`
}

// CarrierView 是载体登记行（GET /api/squads 返回元素）。Version 是 registry
// 行版本，CAS 编辑回路（GET 取版 → PUT ?expect=）的唯一权威来源。
type CarrierView struct {
	Name           string `json:"name"`
	Machine        string `json:"machine"`
	CLI            string `json:"cli"`
	HomeDir        string `json:"home_dir"`
	Model          string `json:"model,omitempty"`
	Credential     string `json:"credential"`
	MaxConcurrency int    `json:"max_concurrency,omitempty"`
	Status         string `json:"status,omitempty"`     // B293：状态英文键（B453 起无 quota）；空=存量行未回填
	LastError      string `json:"last_error,omitempty"` // 最近一次检测说明；不参与准入
	Version        int    `json:"version"`
	// Account 是所选账号名（B453 F13，omitempty：不选即缺席）。
	Account string `json:"account,omitempty"`
	// AccountMismatch 仅在载体 HOME 的凭据指纹与账号最近读数的指纹都在且不一致时为 true
	// （键出现）；任一侧缺指纹不比对（键缺席）。只标红，不拦派发（spec S4）。
	AccountMismatch bool `json:"account_mismatch,omitempty"`
}

// SquadMember 是小队成员的 wire 形状。MaxConcurrency=0（不限）以键缺席表达。
type SquadMember struct {
	Carrier        string `json:"carrier"`
	MaxConcurrency int    `json:"max_concurrency,omitempty"`
}

// SquadView 是小队登记行；政策位落在每个成员对象上。
type SquadView struct {
	Name    string        `json:"name"`
	Role    string        `json:"role"`
	Members []SquadMember `json:"members"`
	Version int           `json:"version"`
}

// SquadsResp 是 GET /api/squads 的响应体。空库时两个字段是空数组而非 null
// ——「什么都没配」是合法态，前端据此渲染引导文案而不是报错。
type SquadsResp struct {
	Carriers []CarrierView `json:"carriers"`
	Squads   []SquadView   `json:"squads"`
	// Running 是 sched_running 占用行清单（B390 名额键残留观察面）：每行一个
	// 键+计数，操作者据此看见「谁占着、多少」。空库 = []，与既有空数组纪律一致。
	Running []RegistryRunningView `json:"running"`
}

// RegistryRunningView 是一条 sched_running 占用行（key + 当前计数）。
// key 形如 squad/<squad>/<carrier> 或 carrier/<carrier>（见 scheduling.OccupancyKeys）。
type RegistryRunningView struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// CarrierInput 是 PUT 载体的请求体。刻意不含 status / last_error / healthy：
// 状态只由创建、home_dir 变更与检测写入，登记请求不得设置（B293 废止 PutCarrier 翻真）。
type CarrierInput struct {
	Name           string `json:"name,omitempty"`
	Machine        string `json:"machine"`
	CLI            string `json:"cli"`
	HomeDir        string `json:"home_dir"`
	Model          string `json:"model,omitempty"`
	Credential     string `json:"credential"`
	MaxConcurrency int    `json:"max_concurrency,omitempty"`
	// Account 是所选账号名（B453 F13）：空/缺席 = 不选。只能选载体同机的已登记账号。
	Account string `json:"account,omitempty"`
}

// SquadInput 是 PUT 小队的请求体。Members 为空合法（岔口四：先建空队再补成员）。
type SquadInput struct {
	Name    string        `json:"name,omitempty"`
	Role    string        `json:"role"`
	Members []SquadMember `json:"members"`
}

// SquadPutResp 是 PUT 成功响应：version 是本次写入产生的版本（expect+1）；
// 他人的并发写会再推进，编辑回路每次 GET 重取。
type SquadPutResp struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
}

// QueueEntry 是 GET /api/queue 的元素。Kind/ID/Seq/Position 来自队列元数据，
// 其余九个字段逐一来自入队时刻的 IgnitionRequest 快照（拍板记录②：出队前
// 不重读卡）。Position 是清队顺序下的全局 1 基位次。
type QueueEntry struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Card     string `json:"card"`
	Node     string `json:"node,omitempty"`
	Squad    string `json:"squad"`
	Target   string `json:"target,omitempty"`
	Executor string `json:"executor,omitempty"`
	Model    string `json:"model,omitempty"`
	Priority string `json:"priority,omitempty"`
	Ready    bool   `json:"ready"`
	Actor    string `json:"actor"`
	Seq      int64  `json:"seq"`
	Position int    `json:"position"`
}

// QueueResp 是 GET /api/queue 的响应体；空队列 = "queue":[]。
type QueueResp struct {
	Queue []QueueEntry `json:"queue"`
}

// CoordinatorLaunchResp 是 POST /api/cards/{id}/coordinator/launch 的成功响应
// （keystone.RoundResult 的 wire 投影，K4 的 handler 产出、K3 的 CLI 消费——
// 形状以此处为准，K4 不得另行发明字段）。
type CoordinatorLaunchResp struct {
	Woke      bool   `json:"woke"`
	SessionID string `json:"session_id,omitempty"`
	Rebuilt   bool   `json:"rebuilt"`
	Escalated bool   `json:"escalated"`
	Output    string `json:"output,omitempty"`
}

// CoordinatorAttachInfo 是协调者会话的定位三元组；Machine 允许为空串表示本机。
// Dir 与 Command 均由服务端定位器产生，客户端不得自行拼接或改写。
type CoordinatorAttachInfo struct {
	Machine string `json:"machine"`
	Dir     string `json:"dir"`
	Command string `json:"command"`
}

// CoordinatorStatus 是 GET coordinator 的状态；Attach=nil 序列化为 null 表示未绑定。
// attach_active 是进程内的人工接管态，与 Attach 三元组是否存在是两件事。
type CoordinatorStatus struct {
	Bound        bool                   `json:"bound"`
	AttachActive bool                   `json:"attach_active"`
	Attach       *CoordinatorAttachInfo `json:"attach"`
}

// CoordinatorAttachReleaseResp 是 active=false 交回无头后的成功回执。
type CoordinatorAttachReleaseResp struct {
	OK bool `json:"ok"`
}

// HomeProbeReq 是 POST /api/host/probe 的请求体。machine 走 query（?machine=），
// 不进 body——转发基座靠 query 选路。Credential 空 = standalone。
type HomeProbeReq struct {
	CLI        string `json:"cli"`
	Path       string `json:"path"`
	Credential string `json:"credential,omitempty"`
}

// HomeProbeResp 是路径探测的三类结果。kind 只允许 empty / logged_in / occupied。
type HomeProbeResp struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
}

// HomeWakeReq 是 POST /api/host/wake 的请求体（本机唤起，供检测编排转发）。
type HomeWakeReq struct {
	CLI        string `json:"cli"`
	HomeDir    string `json:"home_dir"`
	Credential string `json:"credential,omitempty"`
	Model      string `json:"model,omitempty"`
	// FingerprintKind / FingerprintEntry 是载体所选账号的 kind 与凭据条目（B453 F15，可选）：
	// 目标机据此在载体 HOME 里算凭据短指纹、缩小撞额度签名集合；缺席 = 不比对。
	FingerprintKind  string `json:"fingerprint_kind,omitempty"`
	FingerprintEntry string `json:"fingerprint_entry,omitempty"`
	// Account 是所选账号名（B453，可选）：目标机在本机账号域里查它的代理，用于检测回合
	// 出网——代理串（可能带密码）不过机间 wire。缺席 = 沿用目标机今天的环境。
	Account string `json:"account,omitempty"`
}

// HomeWakeResp 是本机唤起结局。outcome 只允许 ready / need_login / quota / unreachable；
// quota 自 B453 起语义是「可达 + 撞额度」（F16），协调侧不再把载体写成限额中。
type HomeWakeResp struct {
	Outcome string `json:"outcome"`
	Detail  string `json:"detail,omitempty"`
	// Fingerprint 是载体 HOME 凭据短指纹（B453 F15，可选）；缺席 = 未算或算不出（不比对）。
	Fingerprint string `json:"fingerprint,omitempty"`
}

// CarrierDetectResp 是 POST /api/squads/carriers/{name}/detect 成功后的投影。
type CarrierDetectResp struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	LastError string `json:"last_error,omitempty"`
	Version   int    `json:"version"`
}

// CarrierRunCommandResp 是 GET /api/squads/carriers/{name}/run-command 的回执。
// command 由服务端生成，客户端只复制。
type CarrierRunCommandResp struct {
	Command string `json:"command"`
}

// —— 账号与额度（B453，spec §8 F1–F11）——
//
// 账号 API 的 wire 形状。producer = agentd（GET/PUT /api/accounts、
// POST /api/accounts/{name}/probe），consumer = 控制台、`handoff account` CLI、他机
// agentd（立即探测转发）。`handoff account list --json` 每行即一个 AccountView（F11）。
// 任何位置都不出现凭据值（F10）。

// AccountsResp 是 GET /api/accounts 的响应体（F1）。空库为 [] 不是 null。
type AccountsResp struct {
	Accounts []AccountView `json:"accounts"`
}

// AccountView 是账号读面（F5）。proxy 若含 userinfo 密码，密码段打码为 ***（段 1 裁决 7；
// PUT 收到与打码形式相同的值时服务端保留原值）。可缺席键：entry（agy）、proxy、reading（从无成功）、
// last_attempt_at、last_error。state ∈ ok|low|out|fail|none（F6）；kind ∈
// opencode-go|codex|grok|agy（F7）。carriers 是引用该账号的载体名，无引用为 []。
type AccountView struct {
	Name            string              `json:"name"`
	Kind            string              `json:"kind"`
	Machine         string              `json:"machine"`
	HomeDir         string              `json:"home_dir"`
	Entry           string              `json:"entry,omitempty"`
	Proxy           string              `json:"proxy,omitempty"`
	IntervalMinutes int                 `json:"interval_minutes"`
	WarnPercent     int                 `json:"warn_percent"`
	LowPercent      int                 `json:"low_percent"`
	Version         int                 `json:"version"`
	State           string              `json:"state"`
	Stale           bool                `json:"stale"`
	Reading         *AccountReadingView `json:"reading,omitempty"`
	LastAttemptAt   *time.Time          `json:"last_attempt_at,omitempty"`
	LastError       *AccountProblemView `json:"last_error,omitempty"`
	// RecoverAt 是预计离开低额度/无额度的时刻（审查第 1 轮 I2 增补，可选）：服务端按
	// accounts.RecoverAt 推导——越线窗口全部重置即最晚那个；不阻挡、撞额度标记/接口明示
	// 不可用、或任一越线窗口缺重置时刻 → 缺席（恢复时刻未知）。界面直接读它，不另算。
	RecoverAt *time.Time `json:"recover_at,omitempty"`
	Carriers  []string   `json:"carriers"`
}

// AccountReadingView 是最近一次成功读数（F8）。windows 无窗口时为 []。
type AccountReadingView struct {
	Windows     []AccountWindowView    `json:"windows"`
	PlanType    string                 `json:"plan_type,omitempty"`
	ExtraUsage  *AccountExtraUsageView `json:"extra_usage,omitempty"`
	Fingerprint string                 `json:"fingerprint,omitempty"` // sha256:<12 位十六进制>，凭据身份短指纹
	ProbedAt    time.Time              `json:"probed_at"`
}

// AccountExtraUsageView 是窗口之外的额外额度（codex credits、grok 预付/按需）。各键可缺席：
// balance 余额、used/limit 按需已用与上限、unit 计量单位（接口未给则缺席）、unlimited 不限量。
type AccountExtraUsageView struct {
	Unit      string   `json:"unit,omitempty"`
	Balance   *float64 `json:"balance,omitempty"`
	Used      *float64 `json:"used,omitempty"`
	Limit     *float64 `json:"limit,omitempty"`
	Unlimited bool     `json:"unlimited,omitempty"`
}

// AccountDeleteRefusal 是 DELETE /api/accounts/{name} 因被载体引用拒删时的 409 体（F3）。
type AccountDeleteRefusal struct {
	Error    string   `json:"error"`
	Carriers []string `json:"carriers"`
}

// AccountWindowView 是一个额度窗口（F8）：key ∈ session|weekly|monthly；group 仅
// agy 模型组；used_percent 0–100；resets_at 可缺席。
type AccountWindowView struct {
	Key         string     `json:"key"`
	Group       string     `json:"group,omitempty"`
	UsedPercent float64    `json:"used_percent"`
	ResetsAt    *time.Time `json:"resets_at,omitempty"`
}

// AccountProblemView 是 last_error 的形状：失败（或接口明示不可用）的类别与短因。
type AccountProblemView struct {
	Category string `json:"category"`
	Message  string `json:"message"`
}

// AccountInput 是 PUT /api/accounts/{name}?expect= 的请求体（F9）。名称取路径；
// 不含 state/reading/version。三项数值用指针区分「缺席 = 取缺省」与「给了 0」。
type AccountInput struct {
	Kind            string `json:"kind"`
	Machine         string `json:"machine"`
	HomeDir         string `json:"home_dir"`
	Entry           string `json:"entry,omitempty"`
	Proxy           string `json:"proxy,omitempty"`
	IntervalMinutes *int   `json:"interval_minutes,omitempty"`
	WarnPercent     *int   `json:"warn_percent,omitempty"`
	LowPercent      *int   `json:"low_percent,omitempty"`
}
