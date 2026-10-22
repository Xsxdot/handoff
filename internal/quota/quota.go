// Package quota 是额度适配子系统（边界型，B453）：按账号 kind 读本机凭据、调外部
// 额度接口、把响应体解析成统一的窗口读数。
//
// 职责：
//   - 统一读数值类型（Snapshot/Window）与失败类别（Failure/Category）；
//   - 按 kind 注册适配器（Registry），未注册 kind 明确失败而不是静默空读数；
//   - 各家适配器只读凭据文件、绝不刷新或改写；凭据值只存在于本包内存中。
//
// 边界：
//   - 不 import 任何业务包（编制域、agentd、编排），依赖方向只有「使用方 → quota」；
//     读数落账本、状态推导、调度判据都不在这里；
//   - HOME 由调用方展开成绝对路径再传入（~ 展开归 hostapi），本包不猜 HOME；
//   - 任何 error、日志、返回值都不得含凭据值或 HTTP 请求头（spec S2「密钥不离机」）。
package quota

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// 账号 kind（spec F7）。
const (
	KindOpencodeGo = "opencode-go"
	KindCodex      = "codex"
	KindGrok       = "grok"
	KindAgy        = "agy"
	// KindClaude 与 KindAPIKey 只有离线取材（LoginReader），不支持额度探测（plan Q11）。
	KindClaude = "claude"
	KindAPIKey = "apikey"
)

// 窗口键（spec F8 `windows[].key`）。
const (
	WindowSession = "session"
	WindowWeekly  = "weekly"
	WindowMonthly = "monthly"
)

// Window 是一个额度窗口的读数。UsedPercent 已钳制到 0–100；ResetsAt 缺席 = 接口未给。
type Window struct {
	Key         string
	Group       string // 模型组名（agy 用）；单池账号为空
	UsedPercent float64
	ResetsAt    *time.Time
}

// Snapshot 是一次成功探测的读数。Blocked 非空表示接口明示该账号不可用（如无订阅）——
// 它是「探测成功、结论为不可用」，不是探测失败（plan Q4：归无额度）。
type Snapshot struct {
	Windows     []Window
	PlanType    string
	Blocked     *Failure
	Fingerprint string // 身份值短哈希（Fingerprint），取不到为空
	Extra       *Extra // 额外额度（credits/预付/按需），没有为 nil
}

// Extra 是窗口之外的额外额度：余额与/或按需用量。各字段可缺。
type Extra struct {
	Unit      string // credits / usd 等；接口未给为空
	Balance   *float64
	Used      *float64
	Limit     *float64
	Unlimited bool
}

// Category 是探测失败（或不可用）的类别（plan §9 草案，非冻结）。
type Category string

const (
	CategoryCredentialMissing   Category = "credential_missing"
	CategoryCredentialInvalid   Category = "credential_invalid"
	CategoryCredentialExpired   Category = "credential_expired"
	CategoryLimitReached        Category = "limit_reached"
	CategoryVersionTooOld       Category = "version_too_old"
	CategoryCLIMissing          Category = "cli_missing"
	CategoryUnauthorized        Category = "unauthorized"
	CategoryNoSubscription      Category = "no_subscription"
	CategoryNetwork             Category = "network"
	CategoryProxy               Category = "proxy"
	CategoryTimeout             Category = "timeout"
	CategoryRateLimited         Category = "rate_limited"
	CategoryUpstream            Category = "upstream"
	CategoryParse               Category = "parse"
	CategoryUnsupportedKind     Category = "unsupported_kind"
	CategoryUnsupportedPlatform Category = "unsupported_platform"
)

// Failure 是带类别的探测失败。Message 是给人看的可行动短因，保证不含凭据值。
type Failure struct {
	Category Category
	Message  string
}

func (f *Failure) Error() string { return string(f.Category) + ": " + f.Message }

func fail(c Category, format string, args ...any) *Failure {
	return &Failure{Category: c, Message: fmt.Sprintf(format, args...)}
}

// Request 是一次探测的输入：凭据位置 + 出网代理。
//
//   - Home：账号凭据 HOME，调用方已展开的绝对路径；
//   - Entry：凭据条目（如 auth.json 里的 "opencode-go"），空 = 该 kind 缺省；
//   - Proxy：本次生效的出网代理（账号代理优先，否则调用方给的机器回落值）；
//     空串 = 沿用进程环境 HTTPS_PROXY/HTTP_PROXY/NO_PROXY。
type Request struct {
	Kind  string
	Home  string
	Entry string
	Proxy string
}

// Prober 是一种 kind 的额度适配。返回的 error 一律是 *Failure。
type Prober interface {
	Probe(ctx context.Context, req Request) (Snapshot, error)
}

// Registry 按 kind 分派到适配器。由组装点构造一次，并发安全（只读 map）。readers 是只有离线
// 取材、不支持额度探测的 kind（claude，plan Q11），只在组装期经 RegisterLoginReader 登记。
type Registry struct {
	adapters map[string]Prober
	readers  map[string]LoginReader
}

// NewRegistry 用 kind → 适配器表构造注册表。表在构造后不再变化。
func NewRegistry(adapters map[string]Prober) *Registry {
	copied := make(map[string]Prober, len(adapters))
	for k, v := range adapters {
		copied[k] = v
	}
	return &Registry{adapters: copied}
}

// Probe 按 req.Kind 调对应适配器。未注册的 kind 返回 unsupported_kind 失败。
func (r *Registry) Probe(ctx context.Context, req Request) (Snapshot, error) {
	a, ok := r.adapters[req.Kind]
	if !ok {
		log().Warn("额度探测：kind 未注册适配", "kind", req.Kind)
		return Snapshot{}, fail(CategoryUnsupportedKind, "类型 %q 的额度探测尚未支持", req.Kind)
	}
	return a.Probe(ctx, req)
}

func log() *slog.Logger { return slog.Default().With("mod", "quota") }
