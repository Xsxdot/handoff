// quota.go —— B453 S6/S7：回合失败分类为额度耗尽后的编排动作。
//
// 职责：
//   - 回合失败文本按「载体执行器 + 账号 kind」分类（唯一判据 quota.ClassifyTurnFailure）
//   - 只读任务撞额度：回调门面标账号、在同小队另挑成员重跑；先写 rerun_by 链接、再落
//     failed 终态（终态与链接同时可见，`--wait` 才能跟链，plan Q9），终态经 TerminalNotifier
//     归还名额
//   - 分支任务撞额度：照常 waiting_review（工作树保留、不换人），标账号并开一张 notice 工单
//
// 边界：不判名额、不选人、不碰账号域——标记与重跑由门面实现 QuotaHandler（架构法第九条：
// 接口定义在使用方）。限速与分类不确定的失败按普通失败处理（Q10）。
package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/turn"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/quota"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/google/uuid"
)

// QuotaFailure 是一次被判为额度耗尽的回合失败：命中的分类签名 + 失败原文。
type QuotaFailure struct {
	Signature string
	Text      string
}

// QuotaHit 是标记账号后的告知素材（notice 工单与失败原因用）。
type QuotaHit struct {
	// Account 是被标无额度的账号；空 = 载体未选账号（或账号已删），只在失败原因里写明。
	Account string
	// Windows 是撞到的窗口描述（如「5h 已用 100%」），取自账号最近读数中越过低额度线的窗口；
	// 无读数或读数未越线时为空（读数滞后于撞额度，如实写「未知」）。
	Windows []string
	// RecoverAt 是预计恢复时刻（越线窗口中最晚的重置时刻，同 S5）；未知为 nil。
	RecoverAt *time.Time
}

// RerunOutcome 是重跑回调派出的新任务。
type RerunOutcome struct {
	Ref     proto.TaskRef
	Carrier string // 接替的载体名（时间线文案用）
}

// QuotaHandler 由组装点注入（实现在控制门面 agentd.Server）：
//   - MarkTaskQuota 把任务所用账号标无额度（未选账号的什么也不标），返回告知素材；
//     标记失败只由实现方记日志，不阻断编排（账号状态最迟由下一次探测纠正）
//   - RerunReadonly 用原任务的原始派发输入在同小队另挑成员（排除重跑链上已用账号）派新任务；
//     err 为人读原因（无人可换时即 S5 文案），原任务据此以额度耗尽失败
type QuotaHandler interface {
	MarkTaskQuota(task *proto.Task, f QuotaFailure) QuotaHit
	RerunReadonly(ctx context.Context, task *proto.Task, f QuotaFailure) (RerunOutcome, error)
}

// quotaHandlerBox 让 atomic.Value 存放接口值时类型恒定。
type quotaHandlerBox struct{ h QuotaHandler }

// quotaRerunTimeout 是重跑回调（选人 + 可能的跨机转发派发）的上限：派发本身含建树与拉起执行器，
// 冷机可达数十秒；超时按「没有可换的成员」失败，不让原任务无限停在 running。
const quotaRerunTimeout = 3 * time.Minute

// quotaFailureTextLimit 是失败原文进原因/告知的字符上限。
const quotaFailureTextLimit = 600

// SetQuotaHandler 注入撞额度回调；nil 表示未装配（只读撞额度退回普通失败路径，分支任务照开
// notice 但不标账号）。
func (m *Manager) SetQuotaHandler(h QuotaHandler) {
	m.quotaHandler.Store(quotaHandlerBox{h: h})
	m.log.Info("撞额度回调已注入", "present", h != nil)
}

func (m *Manager) quotaHandlerOf() QuotaHandler {
	box, _ := m.quotaHandler.Load().(quotaHandlerBox)
	return box.h
}

// classifyQuota 对回合失败文本分类；只有「额度耗尽」返回 ok。限速与其他按普通失败，记 Info 供排障。
func (m *Manager) classifyQuota(cur *proto.Task, text string) (QuotaFailure, bool) {
	cls := quota.ClassifyTurnFailure(cur.Executor, cur.AccountKind, text)
	switch cls.Class {
	case quota.TurnQuota:
		m.log.Info("回合失败判为额度耗尽", "task", cur.ID, "executor", cur.Executor,
			"carrier", cur.Carrier, "account", cur.Account, "account_kind", cur.AccountKind,
			"mode", cur.Mode, "signature", cls.Signature)
		return QuotaFailure{Signature: cls.Signature, Text: text}, true
	case quota.TurnRateLimited:
		m.log.Info("回合失败判为限速（不算额度耗尽，按普通失败）", "task", cur.ID,
			"executor", cur.Executor, "account", cur.Account, "signature", cls.Signature)
	}
	return QuotaFailure{}, false
}

// quotaSourceText 是撞额度分类与重写原因的原文：执行器给了错误原文（ErrorDetail）就用它，
// 否则用 FailReason。为什么不直接用 FailReason：它可能拼着适配层的通用处置句（「零文本产出…
// 可 continue 续接重试」），判成额度耗尽后只读原任务已终止、分支任务不续接原会话，那句是错的
// 指引（真机 R6b/R7 瑕疵 1）。普通失败路径不经这里，FailReason 原样进 turn_failed。
func quotaSourceText(r *executor.Result) string {
	if strings.TrimSpace(r.ErrorDetail) != "" {
		return r.ErrorDetail
	}
	return r.FailReason
}

// beginReadonlyQuota 认领只读任务的撞额度处置并另起 goroutine 执行。返回 false = 未接管
// （未注入回调），调用方走普通失败路径。
//
// 为什么另起 goroutine：处置末尾要停 executor、关事件通道（同 autoArchiveReadonly），在中介
// 循环里同步调用等于停自己；且重跑回调可能跨机派发，不能堵住中介循环。
func (m *Manager) beginReadonlyQuota(cur *proto.Task, f QuotaFailure) bool {
	if m.quotaHandlerOf() == nil {
		m.log.Warn("只读任务撞额度但未注入撞额度回调，退回普通失败路径（停 waiting_review 交人）",
			"task", cur.ID, "account", cur.Account)
		return false
	}
	if _, loaded := m.quotaInFlight.LoadOrStore(cur.ID, true); loaded {
		m.log.Info("只读任务撞额度处置已在进行，忽略重复失败结果", "task", cur.ID)
		return true
	}
	go m.rerunAfterQuota(cur.ID, f)
	return true
}

// quotaInFlightFor 报告任务是否正在撞额度处置中（期间到达的结果一律丢弃，防二次重跑）。
func (m *Manager) quotaInFlightFor(taskID string) bool {
	_, ok := m.quotaInFlight.Load(taskID)
	return ok
}

// rerunAfterQuota 是只读任务撞额度的处置主体：标账号 → 回调重跑 → 先写链接 → 进度事件 →
// 以额度耗尽落 failed → 认领者回调终态（归还原载体名额）。
func (m *Manager) rerunAfterQuota(taskID string, f QuotaFailure) {
	defer m.quotaInFlight.Delete(taskID)
	cur, err := m.st.GetTask(taskID)
	if err != nil {
		m.log.Error("撞额度处置：读任务失败，任务留原状态待看门狗或人工", "task", taskID, "cause", err)
		return
	}
	h := m.quotaHandlerOf()
	hit := h.MarkTaskQuota(cur, f)
	ctx, cancel := context.WithTimeout(context.Background(), quotaRerunTimeout)
	out, rerr := h.RerunReadonly(ctx, cur, f)
	cancel()
	var reason string
	if rerr != nil {
		m.log.Warn("只读任务撞额度且没有可换的成员，原任务以额度耗尽失败", "task", taskID,
			"squad", cur.Squad, "carrier", cur.Carrier, "account", cur.Account, "cause", rerr)
		reason = fmt.Sprintf("额度耗尽（%s）：%s；没有可换的成员重跑：%v",
			quotaAccountPhrase(cur, hit), clip(f.Text), rerr)
	} else {
		// 先写链接、再落终态（Q9）：客户端只要看见 failed 而没有链接就会以 3 结束
		if lerr := m.st.SetTaskRerunBy(taskID, out.Ref); lerr != nil {
			m.log.Error("撞额度重跑：回写 rerun_by 失败，新任务已在跑但原任务无链接（--wait 将以 3 结束）",
				"task", taskID, "new_task", out.Ref.Task, "new_machine", out.Ref.Machine, "cause", lerr)
		}
		text := fmt.Sprintf("因额度耗尽改由 %s 重跑（新任务 %s%s）", out.Carrier, out.Ref.Task, atMachine(out.Ref.Machine))
		if evt, aerr := m.st.AppendEvent(taskID, proto.EventTypeProgress, ProgressPayload{Text: text}); aerr != nil {
			m.log.Warn("撞额度重跑：追加进度事件失败", "task", taskID, "cause", aerr)
		} else {
			m.hub.Publish(evt)
		}
		m.log.Info("只读任务撞额度已换人重跑", "task", taskID, "new_task", out.Ref.Task,
			"new_machine", out.Ref.Machine, "new_carrier", out.Carrier, "account", cur.Account)
		reason = fmt.Sprintf("额度耗尽（%s）：%s；已改由 %s 重跑，新任务 %s%s",
			quotaAccountPhrase(cur, hit), clip(f.Text), out.Carrier, out.Ref.Task, atMachine(out.Ref.Machine))
	}
	outcome, err := m.stopWithReason(context.Background(), taskID, reason, "quota")
	if err != nil {
		m.log.Error("撞额度处置：落 failed 失败", "task", taskID, "cause", err)
		return
	}
	if !outcome.Claimed {
		return
	}
	snap, err := m.st.GetTask(taskID)
	if err != nil {
		m.log.Error("撞额度处置后读快照失败，用迁移前快照回调", "task", taskID, "cause", err)
		snap = cur
	}
	if n := m.terminalNotifier(); n != nil {
		n.TaskTerminal(snap)
	} else {
		m.log.Warn("撞额度处置完成但未注入终态回调，名额待启动对账兜底", "task", taskID)
	}
}

// openQuotaNotice 是分支任务撞额度的处置（S7）：标账号、开 notice 工单，返回 turn_failed 的原因。
// 调用方（handleResult）已迁 waiting_review 并作废了旧挂起工单；本函数在追加 turn_failed 之前调用，
// 保证客户端被 turn_failed 唤醒时工单已在（Q8：waiting_review + 待答工单 → 先交回工单）。
func (m *Manager) openQuotaNotice(cur *proto.Task, f QuotaFailure) string {
	var hit QuotaHit
	if h := m.quotaHandlerOf(); h != nil {
		hit = h.MarkTaskQuota(cur, f)
	} else {
		m.log.Warn("分支任务撞额度但未注入撞额度回调，账号未标记，仍开告知工单", "task", cur.ID, "account", cur.Account)
	}
	now := time.Now()
	req := proto.NoticeRequest{
		Kind: proto.TicketKindNotice, Reason: "quota_exhausted",
		Notice:  quotaNoticeText(cur, hit, f, now),
		Carrier: cur.Carrier, Machine: cur.Target, Account: hit.Account,
		Windows: hit.Windows, RecoverAt: hit.RecoverAt,
		Branch: cur.Branch, Worktree: cur.Workdir(),
	}
	body, err := json.Marshal(req)
	if err != nil {
		m.log.Error("撞额度告知工单编码失败", "task", cur.ID, "cause", err)
		return "额度耗尽：" + f.Text
	}
	tk := &proto.Ticket{ID: uuid.NewString(), TaskID: cur.ID, Kind: proto.TicketKindNotice,
		Request: body, CreatedAt: now.UTC()}
	if _, err := m.st.CreateTicket(tk); err != nil {
		m.log.Error("撞额度告知工单落库失败（任务仍 waiting_review，失败原因里写明额度耗尽）",
			"task", cur.ID, "cause", err)
		return fmt.Sprintf("额度耗尽（%s）：%s", quotaAccountPhrase(cur, hit), f.Text)
	}
	m.log.Info("分支任务撞额度已开告知工单", "task", cur.ID, "ticket", tk.ID, "carrier", cur.Carrier,
		"account", hit.Account, "branch", cur.Branch, "worktree", cur.Workdir())
	text := "撞额度：" + quotaAccountPhrase(cur, hit) + "，任务停下（不换人、工作树保留），已开告知工单 " + tk.ID
	if evt, aerr := m.st.AppendEvent(cur.ID, proto.EventTypeProgress, ProgressPayload{Text: text}); aerr != nil {
		m.log.Warn("撞额度告知：追加进度事件失败", "task", cur.ID, "cause", aerr)
	} else {
		m.hub.Publish(evt)
	}
	return fmt.Sprintf("额度耗尽（%s，见告知工单 %s）：%s", quotaAccountPhrase(cur, hit), tk.ID, f.Text)
}

// quotaNoticeText 是 notice 工单全文（F-B460-1：CLI 退出 5 的块原样打印它）。
func quotaNoticeText(cur *proto.Task, hit QuotaHit, f QuotaFailure, now time.Time) string {
	var b strings.Builder
	b.WriteString("分支任务撞额度，已停下：不自动换人，工作树保留。\n")
	fmt.Fprintf(&b, "载体：%s%s\n", orUnknown(cur.Carrier), atMachine(cur.Target))
	if hit.Account != "" {
		fmt.Fprintf(&b, "账号：%s（已标无额度，持续到下一次成功探测）\n", hit.Account)
	} else {
		b.WriteString("账号：载体未选账号（额度原因只记在本任务失败原因里）\n")
	}
	if len(hit.Windows) > 0 {
		fmt.Fprintf(&b, "撞到的窗口：%s\n", strings.Join(hit.Windows, "、"))
	} else {
		b.WriteString("撞到的窗口：未知（账号最近读数未越线或无读数）\n")
	}
	if hit.RecoverAt != nil {
		fmt.Fprintf(&b, "预计恢复：%s（约 %s 后）\n", hit.RecoverAt.Local().Format("2006-01-02 15:04"),
			scheduling.HumanDuration(hit.RecoverAt.Sub(now)))
	} else {
		b.WriteString("预计恢复：未知\n")
	}
	fmt.Fprintf(&b, "分支：%s\n", orUnknown(cur.Branch))
	fmt.Fprintf(&b, "工作树：%s\n", orUnknown(cur.Workdir()))
	fmt.Fprintf(&b, "失败原文：%s\n", clip(f.Text))
	b.WriteString("接管见 B459；本工单答「已知悉」即关单，不会在原会话续跑。")
	return b.String()
}

// quotaAccountPhrase 是失败原因里的账号短语。
func quotaAccountPhrase(cur *proto.Task, hit QuotaHit) string {
	if hit.Account != "" {
		return "账号 " + hit.Account + " 已标无额度"
	}
	if cur.Account != "" {
		return "账号 " + cur.Account + " 未能标记"
	}
	return "载体 " + orUnknown(cur.Carrier) + " 未选账号"
}

func atMachine(machine string) string {
	if machine == "" {
		return ""
	}
	return "@" + machine
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "（未知）"
	}
	return s
}

func clip(s string) string { return turn.TruncateRunes(strings.TrimSpace(s), quotaFailureTextLimit) }
