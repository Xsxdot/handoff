// classify.go —— 回合失败的撞额度三类分类：唯一判据（B453 plan Q10/Q19，spec「撞额度分类」）。
//
// 职责：把执行器回合失败的文本分成「额度耗尽 / 限速 / 其他」三类。检测链
// （hostapi.classifyTurnError）与任务链（段 4 Manager 回合失败）都委托这里，不另写一份。
// 限速不算额度耗尽（Q10：429 只说明这一刻太快，账号额度还在）；分类不确定的一律「其他」，
// 不静默触发换人重跑或标无额度。
//
// 边界：签名表是边界型事实——本版签名来自 orca 字段说明与公开错误码（合成，见测试注），
// 段 4 以 R6a 真样本校正（plan U7）；放宽到「不确定也算额度」须回主会话（plan §6）。
package quota

import (
	"regexp"
	"slices"
	"strings"
)

// TurnFailureClass 是回合失败的三类结论。
type TurnFailureClass string

const (
	TurnQuota       TurnFailureClass = "quota"        // 额度耗尽：标账号无额度、触发 S6/S7
	TurnRateLimited TurnFailureClass = "rate_limited" // 限速：视为可达，不标账号
	TurnOther       TurnFailureClass = "other"        // 其他：按普通失败处理
)

// TurnFailure 是一次分类的结论与依据。Signature 是命中的签名（小写），供日志与撞额度标记
// 说明依据；TurnOther 时为空。
type TurnFailure struct {
	Class     TurnFailureClass
	Signature string
}

// turnSignature 是一条签名：kinds 为空 = 任何账号 kind 都适用。
type turnSignature struct {
	kinds   []string
	pattern string // 小写子串
	class   TurnFailureClass
}

// quotaSignatures 先于限速判：OpenAI 的 insufficient_quota 本身走 HTTP 429，先判限速会把
// 真额度耗尽漏成限速。单独的「quota」字样不在表里——它太常见（进度提示、配额说明），不确定
// 就不算（spec S6「分类不确定的不算撞额度」）。
//
// 来源标注（R6a，2026-10-22）：下表**全部合成、未经真机**——opencode 403 ENTITLEMENT 真机未能复现
// （所需 provider 已不存在），codex/grok/agy 的额度错误也无真样本。真样本只有 opencode API key
// 过期（401 authentication_error），属凭据无效类，表里没有任何签名命中它（反例测试锁定）。
var quotaSignatures = []turnSignature{
	{kinds: []string{KindCodex}, pattern: "insufficient_quota"},
	{kinds: []string{KindCodex}, pattern: "usage_limit_reached"},
	{kinds: []string{KindCodex}, pattern: "you've hit your usage limit"},
	{kinds: []string{KindOpencodeGo}, pattern: "entitlementerror"},  // 合成，未经真机
	{kinds: []string{KindOpencodeGo}, pattern: "entitlement_error"}, // 合成，未经真机
	{kinds: []string{KindOpencodeGo}, pattern: "usage limit"},
	{kinds: []string{KindGrok}, pattern: "out of credits"},
	{kinds: []string{KindGrok}, pattern: "credits exhausted"},
	{kinds: []string{KindGrok}, pattern: "insufficient credits"},
	{pattern: "quota exceeded"},
	{pattern: "exceeded your current quota"},
}

// rateLimitSignatures 任何 kind 都适用。
var rateLimitSignatures = []string{"too many requests", "rate limit", "rate_limit", "ratelimit"}

// status429 只认独立的 429（不认端口、id 里的 1429 之类）。
var status429 = regexp.MustCompile(`(^|[^0-9])429([^0-9]|$)`)

// cliKinds 是执行器 CLI 可承载的账号 kind：无账号时按执行器外壳缩小签名集合（Q10）。
// 未知 CLI 用全部签名。
var cliKinds = map[string][]string{
	"codex":    {KindCodex},
	"grok":     {KindGrok},
	"agy":      {KindAgy},
	"opencode": {KindOpencodeGo, KindCodex, KindGrok},
}

// ClassifyTurnFailure 按「载体执行器 + 账号 kind」把回合失败文本分三类（plan Q10）。
// accountKind 非空时只用该 kind 的签名与通用签名；为空时用该执行器可承载的全部签名。
func ClassifyTurnFailure(cli, accountKind, text string) TurnFailure {
	lower := strings.ToLower(text)
	if strings.TrimSpace(lower) == "" {
		return TurnFailure{Class: TurnOther}
	}
	allowed := kindsFor(cli, accountKind)
	for _, sig := range quotaSignatures {
		if len(sig.kinds) > 0 && allowed != nil && !overlaps(sig.kinds, allowed) {
			continue
		}
		if strings.Contains(lower, sig.pattern) {
			return TurnFailure{Class: TurnQuota, Signature: sig.pattern}
		}
	}
	for _, sig := range rateLimitSignatures {
		if strings.Contains(lower, sig) {
			return TurnFailure{Class: TurnRateLimited, Signature: sig}
		}
	}
	if status429.MatchString(lower) {
		return TurnFailure{Class: TurnRateLimited, Signature: "429"}
	}
	return TurnFailure{Class: TurnOther}
}

// kindsFor 返回参与匹配的 kind 集合；nil = 不限。
func kindsFor(cli, accountKind string) []string {
	if accountKind != "" {
		return []string{accountKind}
	}
	base := cli
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	return cliKinds[base]
}

func overlaps(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}
