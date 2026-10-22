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
//
// B464 A13：删 credential/account/account_mismatch；login/no_account 是载体的账号登录选择
// （no_account 只在 true 时出现；两者都缺席 = 存量「未选账号登录」）；account_id/account_name/
// login_state 是派生只读字段（所选登录所属账号与登录状态，读不到即缺席）；home_dir 是服务端
// 按登录镜像的派生值。
type CarrierView struct {
	Name           string `json:"name"`
	Machine        string `json:"machine"`
	CLI            string `json:"cli"`
	HomeDir        string `json:"home_dir"`
	Model          string `json:"model,omitempty"`
	MaxConcurrency int    `json:"max_concurrency,omitempty"`
	Status         string `json:"status,omitempty"`     // B293：状态英文键（B453 起无 quota）；空=存量行未回填
	LastError      string `json:"last_error,omitempty"` // 最近一次检测说明；不参与准入
	Version        int    `json:"version"`
	Login          string `json:"login,omitempty"`
	NoAccount      bool   `json:"no_account,omitempty"`
	AccountID      string `json:"account_id,omitempty"`
	AccountName    string `json:"account_name,omitempty"`
	LoginState     string `json:"login_state,omitempty"`
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
// 状态只由创建、HOME 变更与检测写入，登记请求不得设置（B293 废止 PutCarrier 翻真）。
//
// B464 A14：删 home_dir/credential/account（请求体里出现也被忽略）；login 与 no_account 恰给
// 一个，否则 400。HOME 由服务端按所选登录镜像。
type CarrierInput struct {
	Name           string `json:"name,omitempty"`
	Machine        string `json:"machine"`
	CLI            string `json:"cli"`
	Model          string `json:"model,omitempty"`
	MaxConcurrency int    `json:"max_concurrency,omitempty"`
	Login          string `json:"login,omitempty"`
	NoAccount      bool   `json:"no_account,omitempty"`
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
//
// B464 W1：删 credential/fingerprint_kind/fingerprint_entry/account；login 是载体所选登录 id
// （可缺席）——目标机在本机账号域查这条登录的 kind 与代理，只认所在机是本机的登录；代理串
// （可能带密码）不过机间 wire。旧版协调机发来的已删键一律忽略。
type HomeWakeReq struct {
	CLI     string `json:"cli"`
	HomeDir string `json:"home_dir"`
	Model   string `json:"model,omitempty"`
	Login   string `json:"login,omitempty"`
}

// HomeWakeResp 是本机唤起结局。outcome 只允许 ready / need_login / quota / unreachable；
// quota 自 B453 起语义是「可达 + 撞额度」（F16），协调侧不再把载体写成限额中。B464 W1 删
// fingerprint。
type HomeWakeResp struct {
	Outcome string `json:"outcome"`
	Detail  string `json:"detail,omitempty"`
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

// —— 账号与额度（B453 F1–F11；B464 A1–A9 账号 = 身份、登录 = 凭据落点）——
//
// 账号 API 的 wire 形状。producer = agentd（GET/PUT/DELETE /api/accounts、
// POST /api/accounts/{ref}/probe），consumer = 控制台、`handoff account` CLI、他机
// agentd（立即探测转发）。`handoff account list --json` 每行即一个 AccountView（C1）。
// 任何位置都不出现凭据值（F10）与原始身份材料：身份只以 sha256 短指纹出现。

// AccountsResp 是 GET /api/accounts 的响应体（A1）。空库两者为 [] 不是 null。
type AccountsResp struct {
	Accounts  []AccountView   `json:"accounts"`
	Discovery []DiscoveryView `json:"discovery"`
}

// AccountView 是账号读面（A2）。id 由 kind（+ provider）+ 身份短指纹确定性导出；name 可改、全局
// 唯一。可缺席键：provider（仅 apikey）、label、interval_minutes/warn_percent/low_percent
// （不支持额度探测的 kind 缺席）、reading（从无成功）、last_attempt_at、last_error、recover_at、
// probe_login（没有可探登录）。state ∈ ok|low|out|fail|none|na；kind ∈
// opencode-go|codex|grok|agy|claude|apikey（A3）。logins 是名下登录，carriers 是引用名下任一登录
// 的载体名，均无则为 []。
type AccountView struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	Kind            string              `json:"kind"`
	Provider        string              `json:"provider,omitempty"`
	Label           string              `json:"label,omitempty"`
	Fingerprint     string              `json:"fingerprint"`
	IntervalMinutes int                 `json:"interval_minutes,omitempty"`
	WarnPercent     int                 `json:"warn_percent,omitempty"`
	LowPercent      int                 `json:"low_percent,omitempty"`
	Version         int                 `json:"version"`
	State           string              `json:"state"`
	Stale           bool                `json:"stale"`
	Reading         *AccountReadingView `json:"reading,omitempty"`
	LastAttemptAt   *time.Time          `json:"last_attempt_at,omitempty"`
	LastError       *AccountProblemView `json:"last_error,omitempty"`
	// RecoverAt 是预计离开低额度/无额度的时刻（可选）：服务端按 accounts.RecoverAt 推导——越线
	// 窗口全部重置即最晚那个；不阻挡、撞额度标记/接口明示不可用、或任一越线窗口缺重置时刻 →
	// 缺席（恢复时刻未知）。界面直接读它，不另算。
	RecoverAt  *time.Time  `json:"recover_at,omitempty"`
	ProbeLogin string      `json:"probe_login,omitempty"`
	Logins     []LoginView `json:"logins"`
	Carriers   []string    `json:"carriers"`
}

// LoginView 是一条登录（A4）：机器、HOME（`~` 或 `~/.handoff/home/<dir>`）、凭据来源名、身份短
// 指纹、状态（ok|expired|gone）、最近变更时刻、使用它的载体（无则 []）。可缺席：expires_at、
// proxy（密码段打码）、relogin（B464 段 3）。
type LoginView struct {
	ID          string     `json:"id"`
	Machine     string     `json:"machine"`
	Home        string     `json:"home"`
	Source      string     `json:"source"`
	Fingerprint string     `json:"fingerprint"`
	State       string     `json:"state"`
	ChangedAt   time.Time  `json:"changed_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Proxy       string     `json:"proxy,omitempty"`
	Carriers    []string   `json:"carriers"`
}

// DiscoveryView 是一台机器的「上次发现」（A5）。self = 是否应答本请求的机器；at 缺席 = 从未
// 发现；error 是最近一轮的问题摘要（位置与类别，不含凭据）。
type DiscoveryView struct {
	Machine string     `json:"machine"`
	Self    bool       `json:"self"`
	At      *time.Time `json:"at,omitempty"`
	Error   string     `json:"error,omitempty"`
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

// AccountDeleteRefusal 是 DELETE /api/accounts/{ref} 因名下登录被载体引用拒删时的 409 体（A7）。
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

// AccountInput 是 PUT /api/accounts/{ref}?expect= 的请求体（A6）。账号只由发现产生：expect=0
// 一律 400。三项数值用指针区分「缺席 = 保留现值」与「给了 0」（交校验）；name 改名在 B464 段 2
// 开放。
type AccountInput struct {
	Name            string `json:"name,omitempty"`
	IntervalMinutes *int   `json:"interval_minutes,omitempty"`
	WarnPercent     *int   `json:"warn_percent,omitempty"`
	LowPercent      *int   `json:"low_percent,omitempty"`
}
