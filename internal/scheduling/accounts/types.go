// types.go —— 账号域的实体与值类型（B453，编制域子包）。
//
// 职责：账号定义（人写，kind=account）与探测读数行（机写，kind=account_reading）
// 的 body 形状、状态词表、缺省值。两者同 id（账号名），物理上不同行：人写定义走
// 整行 CAS，机写读数走「读-合并-CAS 重试」，互不顶版本（plan 拍板 Q1）。
//
// 边界：值类型刻意不复用 internal/quota 的读数类型——编制域不依赖额度适配子系统，
// 映射在 agentd 探测器（组装侧）完成。账本 body 里不出现任何凭据值（spec F10）。
package accounts

import "time"

// registry kind（spec F18，冻结）。id = 账号名。
const (
	KindAccount = "account"
	KindReading = "account_reading"
)

// 账号类型（spec F7，冻结）。
const (
	KindOpencodeGo = "opencode-go"
	KindCodex      = "codex"
	KindGrok       = "grok"
	KindAgy        = "agy"
)

// 缺省值（spec「实现决定」：间隔 15 分钟 / 预警 60% / 低额度 80% / 过期 30 分钟）。
const (
	DefaultIntervalMinutes = 15
	DefaultWarnPercent     = 60
	DefaultLowPercent      = 80
	StaleAfter             = 30 * time.Minute
)

// AllowedIntervals 是可选探测间隔（分钟），spec S1。
var AllowedIntervals = []int{5, 10, 15, 30, 60}

// DefaultEntry 返回该 kind 的缺省凭据条目（spec S1）；agy 由 CLI 自管登录态，条目留空。
func DefaultEntry(kind string) string {
	switch kind {
	case KindOpencodeGo:
		return "opencode-go"
	case KindCodex:
		return "tokens"
	case KindGrok:
		return "https://auth.x.ai"
	default:
		return ""
	}
}

// Account 是人写的账号定义（registry kind=account 的 body）。名称即 id，登记后不可改。
type Account struct {
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	Machine         string `json:"machine"`
	HomeDir         string `json:"home_dir"`
	Entry           string `json:"entry,omitempty"`
	Proxy           string `json:"proxy,omitempty"`
	IntervalMinutes int    `json:"interval_minutes"`
	WarnPercent     int    `json:"warn_percent"`
	LowPercent      int    `json:"low_percent"`
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

// ReadingRecord 是机写读数行（registry kind=account_reading 的 body，plan §9 草案）。
type ReadingRecord struct {
	Reading              *Reading   `json:"reading,omitempty"` // 最近一次成功读数
	LastAttemptStartedAt *time.Time `json:"last_attempt_started_at,omitempty"`
	LastAttemptAt        *time.Time `json:"last_attempt_at,omitempty"`
	LastError            *Problem   `json:"last_error,omitempty"` // 最近一次尝试失败；成功即清
	FailureStreak        int        `json:"failure_streak,omitempty"`
	QuotaMark            *QuotaMark `json:"quota_mark,omitempty"`
	Writer               string     `json:"writer,omitempty"` // 最后一次写入者（机器名/来源），审计用
	WrittenAt            time.Time  `json:"written_at"`
}

// ProbeOutcome 是一次探测的结果（所在机探测器写入用）：Reading 与 Failure 二选一。
type ProbeOutcome struct {
	StartedAt  time.Time
	FinishedAt time.Time
	Reading    *Reading
	Failure    *Problem
	Writer     string
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

// View 是账号的完整读面：定义 + 定义行版本 + 读数行 + 推导出的状态。
type View struct {
	Account Account
	Version int
	State   State
	Stale   bool
	// RecoverAt 是与 State 同一时刻推导的预计恢复（RecoverAt 函数；nil = 不阻挡或未知）。
	RecoverAt *time.Time
	Record    ReadingRecord
	// Carriers 是引用该账号的载体名（排序；组装点未注入引用查询时为空）。
	Carriers []string
}
