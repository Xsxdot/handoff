// probe.go —— 本机隔离 HOME 的路径探测与一次性唤起（B293）。
//
// 职责：问本机某路径相对某 CLI 是空 / 已登录 / 非空无凭据；以及用该 HOME 有时限
// 地唤起对应执行者一次（空白 HOME 落执行者自己的文件）。只暴露本机能力。
//
// 边界：不写编制域状态（那是 scheduling.ApplyDetect）；不发模型 prompt（不是
// RunTurn）；不进入交互登录。跨机由 gateway 经 ?machine= 转发到对端同名端点。
package hostapi

import (
	"context"
	"time"
)

// DefaultDetectTimeout 是 WakeRequest.Timeout 为 0 时的缺省上界。
// 长于各 CLI serve 就绪（opencode 10s / grok 15s / codex 20s），短于
// DefaultTurnTimeout（30 分钟），避免把控制台卡在登录交互或一次付费回合里。
const DefaultDetectTimeout = 30 * time.Second

// ProbeKind 是路径探测的三类结果（与设置页提示一一对应）。
type ProbeKind string

const (
	ProbeEmpty    ProbeKind = "empty"     // 不存在或目录无任何条目
	ProbeLoggedIn ProbeKind = "logged_in" // 可见该 CLI 凭据
	ProbeOccupied ProbeKind = "occupied"  // 非空且未见该 CLI 凭据
)

// ProbeRequest 描述一次只读探测。Credential 取载体词表（standalone /
// main_home_sync）；空 = standalone。值不得当凭据明文使用。
type ProbeRequest struct {
	Path       string
	CLI        string
	Credential string
}

// ProbeReply 是探测结果。Kind 是冻结三值；Detail 给人看，不参与准入。
type ProbeReply struct {
	Kind   ProbeKind
	Detail string
}

// WakeOutcome 是一次唤起在本机观察到的结局，供编制域套四态表。
type WakeOutcome string

const (
	WakeReady       WakeOutcome = "ready"
	WakeNeedLogin   WakeOutcome = "need_login"
	WakeQuota       WakeOutcome = "quota"
	WakeUnreachable WakeOutcome = "unreachable"
)

// WakeRequest 描述一次有时限的本机唤起。Credential 取载体词表（standalone /
// main_home_sync）；空 = standalone。Timeout=0 使用 DefaultDetectTimeout。
type WakeRequest struct {
	CLI        string
	HomeDir    string
	Credential string
	Model      string
	Timeout    time.Duration
}

// WakeReply 是唤起结局。Detail 给人看，原样交给 ApplyDetect 的 last_error。
type WakeReply struct {
	Outcome WakeOutcome
	Detail  string
}

// ProbeHome 只读探测本机路径。Ticket 0 空壳：恒返回 ErrUnavailable。
func (h *Host) ProbeHome(ctx context.Context, req ProbeRequest) (ProbeReply, error) {
	_ = ctx
	log().Warn("ProbeHome 尚未接线", "cli", req.CLI, "credential", req.Credential)
	return ProbeReply{}, ErrUnavailable
}

// WakeHome 用隔离 HOME 唤起对应 CLI 一次。Ticket 0 空壳：恒返回 ErrUnavailable。
func (h *Host) WakeHome(ctx context.Context, req WakeRequest) (WakeReply, error) {
	_ = ctx
	log().Warn("WakeHome 尚未接线", "cli", req.CLI, "timeout", req.Timeout.String())
	return WakeReply{}, ErrUnavailable
}
