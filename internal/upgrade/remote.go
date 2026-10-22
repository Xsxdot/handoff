// 本文件实现一台远端执行机的完整升级编排。
//
// 职责：把远端机器的七种结论、活跃任务闸、非托管闸、pull/push 择路与新版本上线
// 确认收敛成结构化 Result，供 CLI 与 agentd 共用。
//
// 边界：
//   - **只处理远端。** 本机路径（换文件后自己重启、skill 同步）留在 CLI；两者的
//     失败语义完全不同，合并会让本已复杂的分支表再翻一倍
//   - 不排版、不认识 cobra、不写操作者输出；调用方负责把 Result 翻译成界面
//   - progress 只是可选的阶段性人话回调，不是进度流，也不改变升级结论
package upgrade

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/release"
)

// Status 是一台机器处理完的三态。
type Status int

const (
	StatusOK Status = iota
	StatusSkip
	StatusFail
)

// String 让 Status 在日志与线格式里是可读的词，而不是 0/1/2。
// 真机实测过这个代价：一条 WARN 打出 status=2，读日志的人得回来翻常量顺序。
func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusSkip:
		return "skip"
	case StatusFail:
		return "fail"
	default:
		return "unknown"
	}
}

// Result 是结构化结论，不含任何排版：机器名、列宽、缩进都由调用方决定。
type Result struct {
	Verdict Verdict
	Status  Status
	// Reason 是一句人话的结论（例如「2 个活跃任务」「对端 agentd 过旧，未上报平台…」）。
	Reason string
	// Remedy 是处置建议；空串表示不给建议。够不着时必须为空，避免编造处置。
	Remedy string
	// Forcible 表示这个 Skip 能不能被 Force 越过。非托管与已有自拉永远为 false。
	Forcible bool
	From, To string // Status==StatusOK 时的版本迁移
}

// Peer 是远端 agentd 的升级能力子集。
type Peer interface {
	PushUpdate(ctx context.Context, tag, sum string, tgz []byte, force bool) (*proto.UpdateResp, error)
	PullUpdate(ctx context.Context, tag, sum string, force bool) (*proto.UpdateResp, error)
	WaitVersion(ctx context.Context, want string, timeout, interval time.Duration, checkPull bool) error
	LedgerProber
}

// LedgerProber 是账本可读探针（B462 S3）：(true,nil) 可读；(false,nil) 未挂载（503）；
// 错误 = 读不出结论。生产实现是 *client.Client 的 GET /api/disciplines。
type LedgerProber interface {
	LedgerReadable(ctx context.Context) (bool, error)
}

// ledgerProbeAttempts 是换版后探针出错时的总尝试次数（Q4：出错重试至多 3 次）。
const ledgerProbeAttempts = 3

// VerifyLedgerAfterUpgrade 在新版上线后施加 S3 判据：换版前账本可读，换版后必须仍可读。
//
// 参数：name 只进日志；before 是换版前探针三态（nil/false 直接 Skip，**不调探针**）；
// interval 是探针出错时的重试间隔。
// 返回：结论与最后一次探针错误（仅 Unconfirmed 时非空，供调用方写进原因原文）。
// 注意：调用前必须已确认新版上线（WaitVersion 成功），否则启动闸的 503 会被误读成未挂载。
// 远端（RemoteOne）与 CLI 本机路径共用本函数，判据只此一处。
func VerifyLedgerAfterUpgrade(ctx context.Context, log *slog.Logger, name string, before *bool,
	p LedgerProber, interval time.Duration) (LedgerCheck, error) {
	if log == nil {
		log = slog.Default()
	}
	if JudgeLedgerAfter(before, true, nil) == LedgerCheckSkip {
		log.Info("换版前账本不可读或未知，换版后不检查账本", "name", name, "before_known", before != nil)
		return LedgerCheckSkip, nil
	}
	var lastErr error
	for attempt := 1; ; attempt++ {
		readable, err := p.LedgerReadable(ctx)
		check := JudgeLedgerAfter(before, readable, err)
		switch check {
		case LedgerCheckOK:
			log.Info("换版后账本仍可读", "name", name, "attempts", attempt)
			return check, nil
		case LedgerCheckNotMounted:
			log.Error("新版已上线但账本未挂载", "name", name, "attempts", attempt)
			return check, nil
		}
		lastErr = err
		if attempt >= ledgerProbeAttempts {
			log.Error("换版后账本是否挂载未能确认", "name", name, "attempts", attempt, "cause", err)
			return LedgerCheckUnconfirmed, lastErr
		}
		log.Warn("换版后账本探针出错，稍后重试", "name", name, "attempt", attempt, "next_retry", interval, "cause", err)
		select {
		case <-ctx.Done():
			return LedgerCheckUnconfirmed, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// Fetcher 是远端资产与校验和的下载能力子集。
type Fetcher interface {
	FetchArchive(ctx context.Context, rel release.Release, goos, goarch string) ([]byte, string, error)
	FetchChecksum(ctx context.Context, rel release.Release, goos, goarch string) (string, error)
}

// Options 是一次远端升级的策略与等待时限。
type Options struct {
	Force        bool // 越过闸一（活跃任务）；永不越过闸二
	PreferPush   bool // 对应 CLI 的 --push
	WaitPull     time.Duration
	WaitPush     time.Duration
	WaitInterval time.Duration
}

const (
	defaultWaitPull     = 10 * time.Minute
	defaultWaitPush     = 120 * time.Second
	defaultWaitInterval = 2 * time.Second
)

// RemoteOne 把一台**远端**机器升到 rel，返回结构化结论。
//
// 注意：
//   - **只处理远端。** 本机路径（换文件后自己重启、skill 同步）留在 CLI，
//     两者的失败语义完全不同，合并会让本已复杂的分支表再翻一倍
//   - progress 可为 nil；非 nil 时按阶段回调一句人话，供 agentd 落进日志
func RemoteOne(ctx context.Context, log *slog.Logger, m Machine, peer Peer, f Fetcher,
	rel release.Release, o Options, progress func(string)) Result {
	if log == nil {
		log = slog.Default()
	}
	v := Classify(m, rel.Tag)
	log.Info("开始处理远端升级", "name", m.Name, "verdict", v.String(), "platform", m.Platform, "busy", m.Busy)

	// 这些结论在进入资产路径前就结束：尤其是够不着时不能编处置建议。
	switch v {
	case VerdictUnreachable:
		reason := "远端不可达"
		if m.Err != nil {
			reason = m.Err.Error()
		}
		log.Warn("机器够不着", "name", m.Name, "cause", m.Err)
		return Result{Verdict: v, Status: StatusFail, Reason: reason}
	case VerdictLatest:
		log.Info("跳过：已是最新", "name", m.Name)
		return Result{Verdict: v, Status: StatusSkip, Reason: "已是最新"}
	case VerdictTooOld:
		log.Info("跳过：对端未上报平台", "name", m.Name)
		return Result{Verdict: v, Status: StatusSkip,
			Reason: "对端 agentd 过旧，未上报平台，需先手工升级到 ≥v0.1.1"}
	case VerdictUnmanaged:
		log.Info("跳过：agentd 非托管", "name", m.Name)
		return Result{Verdict: v, Status: StatusSkip,
			Reason: "agentd 非托管启动，重启后不会被拉起",
			Remedy: "先在该机器上 handoff service install"}
	case VerdictManagedUnknown:
		log.Info("跳过：对端未上报托管状态", "name", m.Name)
		return Result{Verdict: v, Status: StatusSkip,
			Reason: "对端未上报托管状态，无法确认换版后能否被拉起"}
	case VerdictAgentdDown:
		// RemoteOne 的边界是远端；这个分支只用于防止误把本机状态送进来时
		// 静默落入资产路径。CLI 的本机处置仍由 localSwap/localUpgrade 负责。
		return Result{Verdict: v, Status: StatusFail, Reason: "agentd 未运行"}
	}

	// 闸一放在共享编排里，避免控制台与 CLI 对远端活跃任务各自作出不同决定。
	if m.Busy > 0 && !o.Force {
		log.Info("跳过：有活跃任务", "name", m.Name, "busy", m.Busy)
		return Result{
			Verdict: v, Status: StatusSkip, Forcible: true,
			Reason: fmt.Sprintf("%d 个活跃任务", m.Busy),
			Remedy: fmt.Sprintf("handoff upgrade --now --target %s --force", m.Name),
		}
	}

	parts := strings.SplitN(m.Platform, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		log.Warn("对端上报的平台格式非法", "name", m.Name, "platform", m.Platform)
		return Result{Verdict: v, Status: StatusFail,
			Reason: fmt.Sprintf("对端上报的平台 %q 格式非法", m.Platform)}
	}

	usePull := !o.PreferPush && m.Pull != nil && *m.Pull
	log.Info("选择换版分发模式", "name", m.Name, "platform", m.Platform,
		"pull_capable", m.Pull != nil && *m.Pull, "force_push", o.PreferPush, "use_pull", usePull)
	say := func(message string) {
		if progress != nil {
			progress(message)
		}
	}

	var resp *proto.UpdateResp
	var err error
	if usePull {
		say("正在下发自拉换版请求")
		sum, sumErr := f.FetchChecksum(ctx, rel, parts[0], parts[1])
		if sumErr != nil {
			log.Error("取校验和失败", "name", m.Name, "cause", sumErr)
			return Result{Verdict: v, Status: StatusFail, Reason: sumErr.Error()}
		}
		log.Info("下发自拉换版请求", "name", m.Name, "tag", rel.Tag, "sha256", sum)
		resp, err = peer.PullUpdate(ctx, rel.Tag, sum, o.Force)
	} else {
		say("正在下载远端平台资产")
		log.Info("下载远端平台资产", "name", m.Name, "platform", m.Platform, "tag", rel.Tag)
		tgz, sum, fetchErr := f.FetchArchive(ctx, rel, parts[0], parts[1])
		if fetchErr != nil {
			log.Error("下载远端资产失败", "name", m.Name, "cause", fetchErr)
			return Result{Verdict: v, Status: StatusFail, Reason: fetchErr.Error()}
		}
		say("正在推送换版请求")
		log.Info("下载完成，推送换版请求", "name", m.Name, "tag", rel.Tag, "bytes", len(tgz))
		resp, err = peer.PushUpdate(ctx, rel.Tag, sum, tgz, o.Force)
	}

	if err != nil {
		var rej *client.UpdateRejected
		if errors.As(err, &rej) {
			log.Warn("换版被拒", "name", m.Name, "reason", rej.Reason, "detail", rej.Msg)
			result := Result{Verdict: v, Status: StatusSkip, Reason: rej.Msg}
			// 三种拒绝的出路不同：busy 可 force，非托管和自拉并行永远不可 force。
			switch rej.Reason {
			case proto.UpdateReasonBusy:
				result.Forcible = true
				result.Remedy = fmt.Sprintf("handoff upgrade --now --target %s --force", m.Name)
			case proto.UpdateReasonUnmanaged:
				result.Remedy = "先在该机器上 handoff service install"
			case proto.UpdateReasonPullInProgress:
				// 不给 --force：两个自拉会写坏同一个临时文件。
				result.Remedy = fmt.Sprintf("等它跑完，或 handoff status --target %s 看 pull_state", m.Name)
			}
			return result
		}
		log.Error("发起换版失败", "name", m.Name, "use_pull", usePull, "cause", err)
		return Result{Verdict: v, Status: StatusFail, Reason: err.Error()}
	}
	if resp == nil {
		err = errors.New("换版请求未返回响应")
		log.Error("发起换版失败", "name", m.Name, "cause", err)
		return Result{Verdict: v, Status: StatusFail, Reason: err.Error()}
	}

	waitTimeout := o.WaitPull
	if !usePull {
		waitTimeout = o.WaitPush
	}
	if waitTimeout <= 0 {
		if usePull {
			waitTimeout = defaultWaitPull
		} else {
			waitTimeout = defaultWaitPush
		}
	}
	waitInterval := o.WaitInterval
	if waitInterval <= 0 {
		waitInterval = defaultWaitInterval
	}
	say("正在等待新版本上线")
	log.Info("换版已受理，等待新版本上线", "name", m.Name,
		"version", resp.Version, "prev", resp.Prev, "accepted", resp.Accepted)
	if err := peer.WaitVersion(ctx, rel.Tag, waitTimeout, waitInterval, usePull); err != nil {
		// 自拉与推送的失败措辞必须不同：推送模式下二进制已经在对端了，
		// 提 prev 与回滚是对的；自拉模式下可能连下载都没成，提回滚是误导
		log.Error("等待新版本上线失败", "name", m.Name, "use_pull", usePull, "cause", err)
		result := Result{Verdict: v, Status: StatusFail}
		if usePull {
			result.Reason = err.Error()
			result.Remedy = fmt.Sprintf("handoff status --target %s 看 pull_state 拿完整原因", m.Name)
		} else {
			// Q9：追加 WaitVersion 原文——对端若还在重试账本挂载，原文正是
			// 「agentd 未就绪（账本未挂载：自登记失败，第 N 次重试…）」
			result.Reason = fmt.Sprintf("已换版但新进程未在 %s 内上线：%v", waitTimeout, err)
			result.Remedy = fmt.Sprintf("prev: %s  回滚：handoff upgrade --rollback", resp.Prev)
		}
		return result
	}
	log.Info("新版本已上线", "name", m.Name, "version", rel.Tag)
	// B462 S3：换版前账本可读，则换版后必须仍可读才判成功。控制台路径不填
	// m.LedgerReadable（nil = 未知），判据直接 Skip，行为不变。
	switch check, perr := VerifyLedgerAfterUpgrade(ctx, log, m.Name, m.LedgerReadable, peer, waitInterval); check {
	case LedgerCheckNotMounted:
		return Result{Verdict: v, Status: StatusFail, Reason: "新版已上线但账本未挂载",
			Remedy: fmt.Sprintf("看该机 agentd 日志（自登记/账本挂载失败原因）；prev: %s", resp.Prev)}
	case LedgerCheckUnconfirmed:
		return Result{Verdict: v, Status: StatusFail,
			Reason: fmt.Sprintf("新版已上线，但账本是否挂载未能确认：%v", perr),
			Remedy: fmt.Sprintf("handoff status --target %s 与该机 agentd 日志；prev: %s", m.Name, resp.Prev)}
	}
	return Result{Verdict: v, Status: StatusOK, From: m.Agentd, To: rel.Tag}
}
