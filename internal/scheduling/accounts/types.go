// types.go —— 账号域的实体与值类型（B453 起；B464 改为「账号 = 身份、登录 = 凭据落点」）。
//
// 职责：四类 registry 行的 body 形状、状态词表、缺省值——
//   - 账号（kind=account）：一个厂商侧身份；id 由 kind（+ provider）+ 身份指纹确定性导出
//     （plan L1/Q2），只由发现建，人可改名/阈值/间隔；
//   - 登录（kind=account_login）：账号在某台机器某个 HOME 下的一份凭据（plan L5–L7）；
//     id 是「机器 × HOME × kind × provider × 来源」槽位的确定性哈希，写者是所在机发现器
//     （归属/来源/指纹/状态）与人（代理，段 2），两者都走读-合并-CAS；
//   - 读数（kind=account_reading）：探测器机写，B453 同款，增 login（plan L8）；
//   - 发现结果（kind=discovery_run）：每机一行，id = 机器名（plan L9）。
//
// 边界：值类型刻意不复用 internal/quota 的类型——编制域不依赖额度适配子系统，映射在
// agentd 侧（accountwork）完成。账本 body 里不出现任何凭据值与原始身份材料（spec F10、
// B464 「密钥不离机」）：身份只以 sha256 短指纹出现。
package accounts

import "time"

// registry kind（B453 F18 + B464 L5/L9，冻结）。
const (
	KindAccount      = "account"
	KindReading      = "account_reading"
	KindLogin        = "account_login"
	KindDiscoveryRun = "discovery_run"
)

// 账号类型（B453 F7 + B464 A3，冻结）。claude/apikey 的取材在段 2。
const (
	KindOpencodeGo = "opencode-go"
	KindCodex      = "codex"
	KindGrok       = "grok"
	KindAgy        = "agy"
	KindClaude     = "claude"
	KindAPIKey     = "apikey"
)

// knownKinds 是账号 kind 词表（发现写入与人编辑共用）。
var knownKinds = []string{KindOpencodeGo, KindCodex, KindGrok, KindAgy, KindClaude, KindAPIKey}

// 缺省值（spec「实现决定」：间隔 15 分钟 / 预警 60% / 低额度 80% / 过期 30 分钟）。
const (
	DefaultIntervalMinutes = 15
	DefaultWarnPercent     = 60
	DefaultLowPercent      = 80
	StaleAfter             = 30 * time.Minute
)

// AllowedIntervals 是可选探测间隔（分钟），spec S1。
var AllowedIntervals = []int{5, 10, 15, 30, 60}

// Account 是账号定义（registry kind=account 的 body，plan L3）。ID 是 registry 主键，不进 body。
// Kind/Provider/Fingerprint/CreatedBy/CreatedAt 由发现写入后不再变；Name 与三项阈值归人。
type Account struct {
	ID              string    `json:"-"`
	Kind            string    `json:"kind"`
	Provider        string    `json:"provider,omitempty"`
	Name            string    `json:"name"`
	Label           string    `json:"label,omitempty"`
	Fingerprint     string    `json:"fingerprint"`
	IntervalMinutes int       `json:"interval_minutes,omitempty"`
	WarnPercent     int       `json:"warn_percent,omitempty"`
	LowPercent      int       `json:"low_percent,omitempty"`
	CreatedBy       string    `json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
}

// LoginState 是登录状态（plan L7，冻结）。段 1 只产出 ok/gone；expired 的证据型判据在段 2。
type LoginState string

const (
	LoginOK      LoginState = "ok"      // 正常
	LoginExpired LoginState = "expired" // 已过期（段 2）
	LoginGone    LoginState = "gone"    // 已失效：上次在、本次发现扫不到
)

// Label 返回登录状态的用户可见中文名。
func (s LoginState) Label() string {
	switch s {
	case LoginOK:
		return "正常"
	case LoginExpired:
		return "已过期"
	case LoginGone:
		return "已失效"
	default:
		return string(s)
	}
}

// MainHome 是登录行里主 HOME 的写法（plan L6：`~` 或 `~/.handoff/home/<dir>`）。
const MainHome = "~"

// Login 是一条登录（registry kind=account_login 的 body，plan L6）。ID 不进 body。
// Home 是机器无关的写法（`~` 或 `~/.handoff/home/<dir>`），HomeAbs 是所在机展开后的绝对路径。
type Login struct {
	ID           string     `json:"-"`
	Account      string     `json:"account"`
	Machine      string     `json:"machine"`
	Home         string     `json:"home"`
	HomeAbs      string     `json:"home_abs"`
	Kind         string     `json:"kind"`
	Provider     string     `json:"provider,omitempty"`
	Source       string     `json:"source"`
	Fingerprint  string     `json:"fingerprint"`
	State        LoginState `json:"state"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	ChangedAt    time.Time  `json:"changed_at"`
	AuthFailedAt *time.Time `json:"auth_failed_at,omitempty"`
	Proxy        string     `json:"proxy,omitempty"`
}

// IsMainHome 报告登录是否落在所在机主 HOME。
func (l Login) IsMainHome() bool { return l.Home == MainHome }

// DiscoveryRun 是一台机器最近一轮发现的结果（registry kind=discovery_run 的 body，plan L9）。
// Errors 只含位置与原因类别，不含凭据值。
type DiscoveryRun struct {
	Machine string    `json:"-"`
	At      time.Time `json:"at"`
	Homes   int       `json:"homes"`
	Logins  int       `json:"logins"`
	Errors  []string  `json:"errors,omitempty"`
}

// Window 是一个额度窗口读数。UsedPercent 0–100；ResetsAt 缺席 = 接口未给。
type Window struct {
	Key         string     `json:"key"`
	Group       string     `json:"group,omitempty"`
	UsedPercent float64    `json:"used_percent"`
	ResetsAt    *time.Time `json:"resets_at,omitempty"`
}

// Problem 是探测失败或「接口明示不可用」的原因：类别 + 人话短因（不含凭据）。
type Problem struct {
	Category string `json:"category"`
	Message  string `json:"message"`
}

// Reading 是一次成功探测的读数。Blocked 非空 = 接口明示不可用（如无订阅），按无额度推导。
type Reading struct {
	Windows     []Window    `json:"windows"`
	PlanType    string      `json:"plan_type,omitempty"`
	ExtraUsage  *ExtraUsage `json:"extra_usage,omitempty"`
	Fingerprint string      `json:"fingerprint,omitempty"` // 凭据身份短指纹（sha256 前缀），取不到为空
	Blocked     *Problem    `json:"blocked,omitempty"`
	ProbedAt    time.Time   `json:"probed_at"`
}

// ExtraUsage 是窗口之外的额外额度（codex credits、grok 预付/按需）。各字段可缺。
type ExtraUsage struct {
	Unit      string   `json:"unit,omitempty"`
	Balance   *float64 `json:"balance,omitempty"`
	Used      *float64 `json:"used,omitempty"`
	Limit     *float64 `json:"limit,omitempty"`
	Unlimited bool     `json:"unlimited,omitempty"`
}

// QuotaMark 是撞额度标记：由任务/检测撞额度时写入，持续到开始时刻晚于它的成功探测。
type QuotaMark struct {
	At       time.Time `json:"at"`
	Task     string    `json:"task,omitempty"`
	Carrier  string    `json:"carrier,omitempty"`
	Machine  string    `json:"machine,omitempty"`
	Category string    `json:"category,omitempty"`
	Message  string    `json:"message,omitempty"`
}

// ReadingRecord 是机写读数行（registry kind=account_reading 的 body）。Login 是最近一次探测
// 所用登录 id（plan L8，可缺席）。
type ReadingRecord struct {
	Reading              *Reading   `json:"reading,omitempty"` // 最近一次成功读数
	LastAttemptStartedAt *time.Time `json:"last_attempt_started_at,omitempty"`
	LastAttemptAt        *time.Time `json:"last_attempt_at,omitempty"`
	LastError            *Problem   `json:"last_error,omitempty"` // 最近一次尝试失败；成功即清
	FailureStreak        int        `json:"failure_streak,omitempty"`
	QuotaMark            *QuotaMark `json:"quota_mark,omitempty"`
	Login                string     `json:"login,omitempty"`
	Writer               string     `json:"writer,omitempty"` // 最后一次写入者（机器名/来源），审计用
	WrittenAt            time.Time  `json:"written_at"`
}

// ProbeOutcome 是一次探测的结果（所在机探测器写入用）：Reading 与 Failure 二选一。
// Login 是本次所用登录 id（写进读数行，plan L8）。
type ProbeOutcome struct {
	StartedAt  time.Time
	FinishedAt time.Time
	Reading    *Reading
	Failure    *Problem
	Writer     string
	Login      string
}

// State 是账号状态键（spec F6，冻结）。
type State string

const (
	StateOK   State = "ok"   // 正常
	StateLow  State = "low"  // 低额度
	StateOut  State = "out"  // 无额度
	StateFail State = "fail" // 探测失败
	StateNone State = "none" // 未探测
)

// LoginView 是一条登录的读面：登录行 + 行版本 + 引用它的载体名（排序）。
type LoginView struct {
	Login    Login
	Version  int
	Carriers []string
}

// View 是账号的完整读面：定义 + 定义行版本 + 读数行 + 推导出的状态 + 名下登录。
type View struct {
	Account Account
	Version int
	State   State
	Stale   bool
	// RecoverAt 是与 State 同一时刻推导的预计恢复（RecoverAt 函数；nil = 不阻挡或未知）。
	RecoverAt *time.Time
	Record    ReadingRecord
	// Logins 是名下登录（按 id 排序）。
	Logins []LoginView
	// Carriers 是引用名下任一登录的载体名（排序去重；组装点未注入引用查询时为空）。
	Carriers []string
	// ProbeLogin 是当前「可探登录」的 id（plan Q10 段 1 最小形：第一条正常登录）；空 = 没有。
	ProbeLogin string
}
