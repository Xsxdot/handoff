// session.go 实现 handoff session 命令族（B358，P7 拍板：新词立新族）。
//
// 本卡（B358.3）只落 wait 子命令——外部会话（人/主 agent）的 R1 订阅通道
// （契约 §3.8/§4.6 条 39–46）：全流升序轮询读 + 复用冻结寻址唯一入口
// collab.Service.MessageWakeTargets 过滤，只收「寻址命中我」的会话消息；
// 一次性原语，命中输出恰一行 SessionWake JSON 后退出 0，超时 124。
// S5 续写 list/detail/create/archive/join/leave/send（交界申报见 b358.3-plan §0）。
//
// 组装走既有 CLI 组装点 openRoomService/roomServiceFor（不新增组装点、不新增
// 跨域边，契约 §3.8）；游标文件介质由 openRoomService 统一挂载（条 44 的
// unread 同一投影依赖它）。
// B358.5 已续写六子命令（create/list/detail/archive/join/leave/send）——交界
// 兑现。
// B365 wait 双形态化：缺省一次性原语不变，--follow 常驻订阅（每命中一行、
// SIGINT/SIGTERM 退 0、--timeout 变空闲上限，同 card_wait follow 语义）；
// send 增 --reply-to 发送半边（透传 proto.RoomMessage.ReplyTo，接收半边
// ResolveDelivery 隐式寻址原作者已冻结）。
// 2026-09-24 r2：CLI 正文有效 @ 自动寻址；默认 wait 使用共享账本交付水位，
// 重挂时积压合成一行摘要；显式 --since 仍是只读手工回放。
// B435.2：wait 改为 `session wait <session>`——收件地址是会话驻场者
// （resident:<session>/<identity>，启动时一次定址，驻场者变更即非 0 退出），
// 命中判定唯一入口由 MessageWakeTargets 换成 collab.Service.Matches（C-137/C-145）；
// 驻场者由 session sit/stand（session_resident.go）写入。
package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/collab/room"
	"github.com/Xsxdot/handoff/internal/diag"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/logx"
	"github.com/Xsxdot/handoff/internal/proto"
)

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "会话（群）命令族：订阅寻址、列表、详情、发言（B358）",
}

var (
	sessionWaitSince   int64
	sessionWaitTimeout time.Duration
	// sessionWaitFollow 开启常驻订阅（B365）：未开启时保持一次性原语——首个
	// 命中输出后退出 0（条 46）；开启后每个命中各输出一行并继续等下一条。
	sessionWaitFollow bool
)

// sessionWaitPollInterval 全流轮询节奏；与 card wait 的 Follow tick 同量级。
// var 而非 const：cmd 测试缝——follow 下「--timeout 是空闲上限而非总时长」的
// 时序断言要求把轮询调到亚秒级（2s 轮询下两种语义不可分辨），测试收尾还原。
var sessionWaitPollInterval = 2 * time.Second

var sessionWaitCmd = &cobra.Command{
	Use:   "wait <session>",
	Short: "作为会话驻场者收件（先 handoff session sit）：续收积压摘要或实时 SessionWake（缺省一条即退 0；--follow 常驻）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if sessionWaitTimeout < 0 {
			return fmt.Errorf("--timeout 必须为正时长（当前 %s）", sessionWaitTimeout)
		}
		return runSessionWait(cmd, args[0], sessionWaitSince, cmd.Flags().Changed("since"), sessionWaitTimeout, sessionWaitFollow)
	},
}

// —— B409 U5 有界候选读的扫描器（冻结语义见
// docs/superpowers/specs/2026-09-25-session-bounded-candidate-read-contract.md）——
//
// 进程内扫描水位与持久交付游标严格分离（契约第 31/32 条）：扫描水位可跨过
// 未命中候选与无候选的已查范围，只表示「哪些 seq 已在某个已确认的席位状态下
// 完成过候选枚举与判定」；持久 session_delivery_cursors.last_seq 只在定向命中
// 成功写入 stdout 后推进到该输出覆盖的最大命中 seq（第 33/34/35 条）。两者
// 分离是恢复语义的根基：重挂补收读持久游标，页内推进读扫描水位——混用任何
// 一方都会造成「无命中也推进交付水位」（丢消息）或「重复扫描」（无害但退化
// 成全流读）。

// sessionWaitPageLimit 单页候选读条数；与旧全流分页 500 同量级。
const sessionWaitPageLimit = 500

// sessionWaitSeatRetries 席位状态版本不稳定时的有界重试上限；超过即返回可见
// 错误（契约第 17 条），绝不带着未确认的页输出或推进。
const sessionWaitSeatRetries = 8

// sessionWaitBeforeQuery / sessionWaitBeforeJudge / sessionWaitBeforeAdvance
// 是确定性竞态与失败注入的测试缝（生产恒 nil）：分别 fired 于「席位版本读取
// 之后、候选查询之前」「候选查询之后、最终判定之前」「stdout 成功之后、持久
// 游标推进之前」。有界候选读的回归护栏（单向换绑、空页换绑、A→member→A 的
// ABA、游标写失败）依赖这些同步点构造确定性交错。
var (
	sessionWaitBeforeQuery   func(from, to int64)
	sessionWaitBeforeJudge   func(from, to int64)
	sessionWaitBeforeAdvance func(cursorKey string, seq int64)
)

// sessionHit 一条已判定命中的候选：原事件与解码后的消息。
type sessionHit struct {
	ev     ledger.Event
	msg    proto.RoomMessage
	notice *proto.TaskTicketNotice
}

// sessionWaitPage 读一页驻场者地址的有界候选，并在席位状态版本护栏内完成该页全部
// Matches 判定（B435 C-137：命中只经匹配函数；驻场者登记/请下与换绑同样递增版本，
// R plan Q6 甲）。返回确认命中的行与推进后的进程内扫描水位 next。
//
// 席位一致性（契约第 6–17 条）的实现选择是 **revision 复读**而非页内锁：
//   - 候选查询前读取席位版本（第 10 条）；
//   - 该页全部判定之后复读版本，空候选页同样复读（第 7/11 条）；
//   - 版本不同说明枚举与判定观察了不同席位状态——丢弃本页结果（第 12 条），
//     从原未提交 seq 下界重读（第 13 条）；版本单调不复用（席位变更路径原子
//     递增，internal/ledger/seat_revision.go），因此 A→member→A 的 ABA 必然
//     改变版本被发现——这正是「只比较席位集合值」做不到的（拍板记录明确
//     拒绝集合值比较）；
//   - 一致性未确认前不输出、不推进扫描水位（第 15/16 条）；有界重试仍不稳定
//     则返回可见错误（第 17 条）。
//
// 不选锁方案的原因：锁需要跨「账本候选读」与「collab 判定读」两段持锁，而
// collab 经接口缝读卡，锁无法横跨两个组件的事务边界；revision 在读多写少的
// 订阅路径上无写侧争用，且把一致性判据收敛为单一单调量。
// sessionWaitPage 读一页候选并在席位状态版本护栏内判定。B409.6：ctx 贯通到
// 候选读与席位版本读（取消时 SQL 真正中断，不只在 select 处感知）；每页判定
// 收口一行（候选数/命中数/重读次数/耗时）。
func sessionWaitPage(ctx context.Context, svc *collab.Service, st *ledger.Store, addr proto.DeliveryAddress, from, to int64) (hits []sessionHit, next int64, err error) {
	started := time.Now()
	member := addr.Identity
	for attempt := 1; ; attempt++ {
		revBefore, err := st.SeatRevisionContext(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("session wait 读席位状态版本: %w", err)
		}
		if sessionWaitBeforeQuery != nil {
			sessionWaitBeforeQuery(from, to)
		}
		candidates, err := st.ResidentMessageCandidatesContext(ctx, addr.Identity, addr.Session, from, to, sessionWaitPageLimit)
		if err != nil {
			return nil, 0, fmt.Errorf("session wait 读候选页: %w", err)
		}
		if sessionWaitBeforeJudge != nil {
			sessionWaitBeforeJudge(from, to)
		}
		pageHits := make([]sessionHit, 0, len(candidates))
		lastSeq := from
		for _, ev := range candidates {
			if ev.Type == ledger.EvTaskTicketNotice {
				notice, hit, err := sessionWaitTaskTicketNotice(st, ev, addr)
				if err != nil {
					return nil, 0, err
				}
				if hit {
					pageHits = append(pageHits, sessionHit{ev: ev, notice: &notice})
				}
				lastSeq = ev.Seq
				continue
			}
			msg, hit, err := sessionWaitMatch(svc, ev, addr)
			if err != nil {
				return nil, 0, err
			}
			if hit {
				pageHits = append(pageHits, sessionHit{ev: ev, msg: msg})
			}
			lastSeq = ev.Seq
		}
		revAfter, err := st.SeatRevisionContext(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("session wait 复读席位状态版本: %w", err)
		}
		if revAfter != revBefore {
			// 枚举与判定观察了不同席位状态：本页整体作废，从原下界重读。
			// 不输出、不推进任何水位。
			slog.Warn("session wait 候选页席位状态发生变化，丢弃重读",
				"member", member, "from_seq", from, "to_seq", to,
				"rev_before", revBefore, "rev_after", revAfter, "attempt", attempt,
				"candidates", len(candidates))
			if attempt >= sessionWaitSeatRetries {
				return nil, 0, fmt.Errorf("session wait 候选页席位状态在 %d 次重读后仍不稳定（seq 范围 %d→%d）",
					sessionWaitSeatRetries, from, to)
			}
			continue
		}
		if len(candidates) < sessionWaitPageLimit && to > 0 {
			// 窗口已穷尽：本页查询覆盖 (from, to] 全部，扫描水位可跨到 to
			//（契约第 31 条——可跨过无候选的已查询范围）。
			slog.Debug("session wait 候选页判定完成", append(diag.Attrs(ctx),
				"member", member, "from_seq", from, "to_seq", to,
				"candidates", len(candidates), "hits", len(pageHits),
				"attempt", attempt, "elapsed_ns", time.Since(started).Nanoseconds())...)
			return pageHits, to, nil
		}
		slog.Debug("session wait 候选页判定完成", append(diag.Attrs(ctx),
			"member", member, "from_seq", from, "to_seq", to,
			"candidates", len(candidates), "hits", len(pageHits),
			"attempt", attempt, "elapsed_ns", time.Since(started).Nanoseconds())...)
		return pageHits, lastSeq, nil
	}
}

// sessionWaitTaskTicketNotice rechecks the latest ownership version before returning
// an addressed event, so stale routes from a prior resident/seat never ring.
func sessionWaitTaskTicketNotice(st *ledger.Store, ev ledger.Event, addr proto.DeliveryAddress) (proto.TaskTicketNotice, bool, error) {
	var notice proto.TaskTicketNotice
	if err := json.Unmarshal(ev.Payload, &notice); err != nil {
		return notice, false, fmt.Errorf("session wait task_ticket_notice seq=%d 解码: %w", ev.Seq, err)
	}
	if err := proto.ValidateTaskTicketNotice(notice); err != nil {
		return notice, false, fmt.Errorf("session wait task_ticket_notice seq=%d 契约校验: %w", ev.Seq, err)
	}
	if notice.Target != addr {
		return notice, false, nil
	}
	current, err := st.GetTaskTicketNotice(notice.SourceMachine, notice.TaskID, notice.TicketID)
	if err != nil {
		return notice, false, fmt.Errorf("session wait 复读工单通知归属 %s/%s: %w", notice.TaskID, notice.TicketID, err)
	}
	if current.OwnershipVersion != notice.OwnershipVersion || current.SourceSeq != notice.SourceSeq || current.TicketKind != notice.TicketKind || current.Target == nil || *current.Target != addr ||
		(current.Status != "pending" && current.Status != "delivered") {
		return notice, false, nil
	}
	if addr.Kind == proto.DeliveryResident {
		resident, ok, err := st.SessionResident(addr.Session)
		if err != nil {
			return notice, false, fmt.Errorf("session wait 复读当前驻场者 %s: %w", addr.Session, err)
		}
		if !ok || resident.Identity != addr.Identity {
			return notice, false, nil
		}
	}
	return notice, true, nil
}

// isWaitCancel 报告 err 是否为 wait ctx 的取消/到期（区别于真实读错误）。
// ctx 贯通查询后，取消会以查询错误形态冒出；这里统一识别，交给调用方按
// 运行模式映射退出语义（follow 退 0 / 一次性 124）。
func isWaitCancel(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// runSessionWait 作为会话驻场者收件（B435 R，契约 §4.2 C-131–C-144）。参数 session 是位置参数
// 原值：须为会话房间号（C-132，旧 `session wait <成员身份>` 形态落此支拒绝）；会话须已有驻场者
// （C-133，无驻场者立即非 0、不阻塞）。地址 = 启动时读到的驻场者（C-134），持久游标键 =
// DeliveryCursorKey(驻场者地址)（C-135/C-136）。无 --since 时从共享交付水位补收；显式 --since
// 保持手工排他回放，不改变共享水位。命中只经 collab.Service.Matches（C-137）。
//
// 双形态（B365）共用有界候选读循环；默认先扫描积压，单行摘要成功写出后
// 推进共享交付水位。之后分叉点是「实时首个命中后是否退出」：
//   - 一次性（缺省）：首个命中输出一行 SessionWake 后退出 0（条 46）；--timeout
//     是等待总时长，到点 124；
//   - --follow：每个命中各自输出一行后重置空闲计时、继续推进游标等下一条，
//     不主动退出——SIGINT/SIGTERM 退出 0；--timeout 语义变为空闲上限（任意
//     两命中帧之间的最大间隔，0 = 不设限），到点 124（与 runCardWait 的
//     follow 语义同形，card_wait.go:55-56）。
//
// countingWriter 统计经 stdout 写出的字节数（B409.6 诊断用；原样透传，
// 不改写内容——stdout 的每行 JSON 语义不动）。
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// stockDefaultLogger 记录包加载时的 slog 默认 logger：生产 CLI 进程用它判断
// 「默认出口还是原始 slog」，从而装配 cli 组件 logger；已被组装方替换（测试
// 捕获 / 嵌入宿主自备 logger）时不覆盖，诊断日志走既有出口。
var stockDefaultLogger = slog.Default()

func runSessionWait(cmd *cobra.Command, session string, sinceFlag int64, sinceSet bool, timeout time.Duration, follow bool) error {
	started := time.Now()
	opID := diag.NewOperationID()
	baseCtx := cmd.Context()
	if baseCtx == nil {
		// 直调（测试/嵌入）未 SetContext 时 cobra 返回 nil；诊断 ctx 仍需落底。
		baseCtx = context.Background()
	}
	ctx := diag.With(baseCtx, diag.Info{OperationID: opID})
	if slog.Default() == stockDefaultLogger {
		slog.SetDefault(logx.Setup("cli", ""))
	}
	slog.Info("session wait 入口", append(diag.Attrs(ctx), "session", session, "since", sinceFlag,
		"since_set", sinceSet, "timeout", timeout.String(), "follow", follow)...)
	if sinceSet && sinceFlag < 0 {
		return fmt.Errorf("--since 必须为非负 seq（当前 %d）", sinceFlag)
	}
	if !room.IsSessionRoom(session) {
		// C-132：旧 `session wait <成员身份>` 形态（及任何非会话号参数）在开账本前拒绝。
		slog.Warn("session wait 参数不是会话号", append(diag.Attrs(ctx), "arg", session)...)
		return fmt.Errorf("session wait 的参数须是会话号（形如 session:<n>），得 %q；"+
			"旧 `session wait <成员身份>` 形态已删除——现在按会话收件，收件人是该会话的驻场者", session)
	}
	openStarted := time.Now()
	svc, st, err := openRoomService()
	openNs := time.Since(openStarted).Nanoseconds()
	if err != nil {
		slog.Error("session wait 打开会话服务失败", append(diag.Attrs(ctx),
			"session", session, "open_ns", openNs, "cause", err)...)
		slog.Warn("session wait 完成", append(diag.Attrs(ctx), "command", "session_wait",
			"session", session, "outcome", "error", "error_class", "open_failed",
			"stdout_bytes", int64(0), "elapsed_ns", time.Since(started).Nanoseconds())...)
		return fmt.Errorf("session wait 打开会话服务: %w", err)
	}
	defer st.Close()
	resident, seated, err := st.SessionResident(session)
	if err != nil {
		slog.Error("session wait 读驻场者失败", append(diag.Attrs(ctx), "session", session, "cause", err)...)
		return fmt.Errorf("session wait 读会话 %s 驻场者: %w", session, err)
	}
	if !seated {
		// C-133：无驻场者立即失败（≠124），不挂到超时。
		slog.Warn("session wait 会话无驻场者", append(diag.Attrs(ctx), "session", session)...)
		return fmt.Errorf("会话 %s 无驻场者，session wait 无收件地址：先 handoff session sit %s --as <身份>", session, session)
	}
	addr := proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: session, Identity: resident.Identity}
	slog.Info("session wait 驻场者地址", append(diag.Attrs(ctx), "session", session,
		"resident", resident.Identity, "cursor_key", proto.DeliveryCursorKey(addr))...)
	counter := &countingWriter{w: cmd.OutOrStdout()}
	runErr := sessionWaitRun(ctx, svc, st, counter, addr, sinceFlag, sinceSet, timeout, follow)

	// B409.6 收口：结果分类 + stdout 字节 + 总耗时；诊断只写 stderr（slog
	// 出口），stdout 的 JSON 行与退出码语义不变。
	attrs := append(diag.Attrs(ctx), "command", "session_wait", "session", session, "member", addr.Identity,
		"follow", follow, "open_ns", openNs, "stdout_bytes", counter.n,
		"elapsed_ns", time.Since(started).Nanoseconds())
	var codeErr *exitCodeError
	outcome := ""
	switch {
	case runErr == nil && counter.n > 0:
		outcome = "success_nonempty"
	case runErr == nil:
		outcome = "canceled"
		attrs = append(attrs, "cancel_reason", "context_canceled")
	case errors.As(runErr, &codeErr) && codeErr.code == ExitTimeout:
		outcome = "canceled"
		attrs = append(attrs, "cancel_reason", "deadline_exceeded")
	case isWaitCancel(runErr):
		outcome = "canceled"
		attrs = append(attrs, "cancel_reason", "context_canceled")
	default:
		outcome = "error"
		attrs = append(attrs, "error_class", "cli_error")
	}
	attrs = append(attrs, "outcome", outcome)
	level := slog.Info
	if outcome == "error" {
		level = slog.Warn
	}
	level("session wait 完成", attrs...)
	return runErr
}

// sessionWaitRun 是订阅通道的核心循环（runSessionWait 的依赖注入形态，测试
// 用它注入假 writer / 已关闭账本 / 竞态钩子）。addr 是启动时解析出的驻场者地址：
// 游标键、积压 member、命中判定、未读投影全部从它取。ctx 只用于退出信号；查询自身
// 按页有界，取消语义沿用既有 select 轮询（两方言一致，契约第 55 条）。
func sessionWaitRun(ctx context.Context, svc *collab.Service, st *ledger.Store, out io.Writer, addr proto.DeliveryAddress, sinceFlag int64, sinceSet bool, timeout time.Duration, follow bool) error {
	runStarted := time.Now()
	member := addr.Identity
	key := proto.DeliveryCursorKey(addr)
	if key == "" {
		return fmt.Errorf("session wait 地址残缺（kind=%s session=%q identity=%q），拒绝读写交付游标", addr.Kind, addr.Session, addr.Identity)
	}
	if !sinceSet {
		// 默认：读共享交付水位。读错必须显式失败——把读错当 0 会把停听期间
		// 已交付的窗口当作未交付全量重放，反向会把未交付窗口当已交付丢掉
		//（契约第 10/43/44 条的反向）。B409.6：点读随 ctx 取消。地址键无行 = 0
		//（契约 §6：B409r2 条 28 对驻场者地址不变——首次 wait 从房间头补收）。
		var err error
		sinceFlag, err = st.SessionDeliveryCursorContext(ctx, key)
		if err != nil {
			slog.Error("session wait 读取交付水位失败", append(diag.Attrs(ctx),
				"member", member, "cursor_key", key, "cause", err)...)
			return fmt.Errorf("session wait 读取交付水位: %w", err)
		}
	}
	if !follow && timeout > 0 {
		// 一次性：--timeout 是等待总时长（现状不变）。
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	var idleC <-chan time.Time
	var idleTimer *time.Timer
	if follow {
		// 常驻：SIGINT/SIGTERM 汇入 ctx 取消（主动退出 0）；--timeout 是空闲
		// 上限——计时只在命中帧重置，与总时长解耦，故不用 WithTimeout。
		var stopSignals context.CancelFunc
		ctx, stopSignals = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stopSignals()
		if timeout > 0 {
			idleTimer = time.NewTimer(timeout)
			defer idleTimer.Stop()
			idleC = idleTimer.C
		}
	}
	enc := json.NewEncoder(out)
	cursor := sinceFlag
	if !sinceSet {
		// 积压扫描：启动时一次 MaxSeq 快照为冻结上界（契约第 26/27 条），
		// 扫描 (cursor, MaxSeq] 的有界候选页；扫描期新增事件留给实时循环。
		fence, err := st.MaxSeq()
		if err != nil {
			return fmt.Errorf("session wait 读取积压边界: %w", err)
		}
		from := cursor
		scan := cursor
		backlog := proto.SessionBacklog{Type: "session_backlog", Member: member, FromSeq: from, Hits: []proto.SessionBacklogHit{}}
		for scan < fence {
			hits, next, err := sessionWaitPage(ctx, svc, st, addr, scan, fence)
			if err != nil {
				if isWaitCancel(err) {
					// ctx 贯通后取消以查询错误冒出：follow 主动退 0，一次性按
					// 超时 124——与 select 分支同语义，不把取消记成读错误。
					slog.Info("session wait 积压扫描被取消", append(diag.Attrs(ctx),
						"member", member, "from_seq", from, "through_seq", scan,
						"elapsed_ns", time.Since(runStarted).Nanoseconds())...)
					if follow {
						return nil
					}
					return &exitCodeError{code: ExitTimeout, err: fmt.Errorf("session wait 超时")}
				}
				return err
			}
			if next <= scan {
				return fmt.Errorf("session wait 积压扫描水位未推进（scan=%d next=%d）", scan, next)
			}
			for _, h := range hits {
				if h.notice != nil {
					backlog.TaskTicketNotices = append(backlog.TaskTicketNotices, proto.SessionTicketNoticeWake{
						Type: "task_ticket_notice", Notice: *h.notice, Status: "pending"})
					backlog.ToSeq = h.ev.Seq
					continue
				}
				ref, err := sessionWaitReference(svc, h.msg)
				if err != nil {
					return err
				}
				backlog.Hits = append(backlog.Hits, proto.SessionBacklogHit{
					Hit:        proto.SessionCite{Seq: h.ev.Seq, Room: h.msg.Room, Actor: h.ev.Actor, Body: h.msg.Body},
					Referenced: ref,
				})
				backlog.ToSeq = h.ev.Seq
			}
			scan = next
		}
		slog.Info("session wait 积压扫描完成", append(diag.Attrs(ctx), "member", member,
			"from_seq", from, "through_seq", scan, "hits", len(backlog.Hits),
			"elapsed_ns", time.Since(runStarted).Nanoseconds())...)
		if len(backlog.Hits) > 0 || len(backlog.TaskTicketNotices) > 0 {
			// stdout 成功才算交付（契约第 58 条）；随后推进到最高命中 seq。
			if err := enc.Encode(backlog); err != nil {
				return fmt.Errorf("session wait 输出积压摘要: %w", err)
			}
			for _, notice := range backlog.TaskTicketNotices {
				if err := st.SetTaskTicketNoticeStatus(notice.Notice.SourceMachine, notice.Notice.TaskID,
					notice.Notice.TicketID, notice.Notice.OwnershipVersion, "delivered", "", ""); err != nil {
					return fmt.Errorf("session wait 确认工单通知送达: %w", err)
				}
			}
			if sessionWaitBeforeAdvance != nil {
				sessionWaitBeforeAdvance(key, backlog.ToSeq)
			}
			if err := st.AdvanceSessionDeliveryCursor(key, backlog.ToSeq); err != nil {
				slog.Warn("session wait 积压已输出但进度写入失败", "member", member, "cursor_key", key, "seq", backlog.ToSeq, "cause", err)
				return fmt.Errorf("session wait 写交付水位: %w", err)
			}
			slog.Info("session wait 积压已交付", "member", member, "cursor_key", key, "from_seq", from,
				"to_seq", backlog.ToSeq, "hits", len(backlog.Hits))
			if !follow {
				return nil
			}
			if idleTimer != nil {
				if !idleTimer.Stop() {
					select {
					case <-idleTimer.C:
					default:
					}
				}
				idleTimer.Reset(timeout)
			}
		}
		// 扫描到的非命中事件无须再次判定；只持久化已输出的命中水位（契约
		// 第 32/35 条——进程内水位与持久游标分离）。
		cursor = scan
	}
	ticker := time.NewTicker(sessionWaitPollInterval)
	defer ticker.Stop()
	for {
		// R plan Q10 乙：每拍复读驻场者，与启动地址不同即可行动报错退出（非 0、≠124，与 C-133
		// 同档）——启动地址已不再是任何人的收件地址，挂到超时就是静默失败。不输出、不推进游标。
		if err := sessionWaitResidentUnchanged(st, addr); err != nil {
			return err
		}
		fence, err := st.MaxSeqContext(ctx)
		if err != nil {
			// ctx 到期/取消不会自愈：与页查询同款退出映射（follow 退 0 /
			// 一次性 124）。这里若 continue，到期的 ctx 会让循环原地打转。
			if isWaitCancel(err) {
				slog.Info("session wait 读取流边界被取消", append(diag.Attrs(ctx),
					"member", member, "elapsed_ns", time.Since(runStarted).Nanoseconds())...)
				if follow {
					return nil
				}
				return &exitCodeError{code: ExitTimeout, err: fmt.Errorf("session wait 超时")}
			}
			return fmt.Errorf("session wait 读取流边界: %w", err)
		}
		if fence > cursor {
			hits, next, err := sessionWaitPage(ctx, svc, st, addr, cursor, fence)
			if err != nil {
				if isWaitCancel(err) {
					slog.Info("session wait 实时扫描被取消", append(diag.Attrs(ctx),
						"member", member, "through_seq", cursor,
						"elapsed_ns", time.Since(runStarted).Nanoseconds())...)
					if follow {
						return nil
					}
					return &exitCodeError{code: ExitTimeout, err: fmt.Errorf("session wait 超时")}
				}
				return err
			}
			for _, h := range hits {
				if h.notice != nil {
					wake := proto.SessionTicketNoticeWake{Type: "task_ticket_notice", Notice: *h.notice, Status: "delivered"}
					if err := enc.Encode(wake); err != nil {
						return fmt.Errorf("session wait 输出原工单通知: %w", err)
					}
					if err := st.SetTaskTicketNoticeStatus(h.notice.SourceMachine, h.notice.TaskID,
						h.notice.TicketID, h.notice.OwnershipVersion, "delivered", "", ""); err != nil {
						return fmt.Errorf("session wait 确认工单通知送达: %w", err)
					}
					if !sinceSet {
						if err := st.AdvanceSessionDeliveryCursor(key, h.ev.Seq); err != nil {
							return fmt.Errorf("session wait 推进工单通知水位: %w", err)
						}
					}
					if !follow {
						return nil
					}
					continue
				}
				wake, err := buildSessionWake(svc, h.ev, h.msg, member)
				if err != nil {
					return err
				}
				if err := enc.Encode(wake); err != nil {
					// stdout 失败不得推进持久水位（契约第 59 条）。
					return fmt.Errorf("session wait 输出唤醒载荷: %w", err)
				}
				if !sinceSet {
					if sessionWaitBeforeAdvance != nil {
						sessionWaitBeforeAdvance(key, h.ev.Seq)
					}
					if err := st.AdvanceSessionDeliveryCursor(key, h.ev.Seq); err != nil {
						slog.Warn("session wait 唤醒已输出但进度写入失败", "member", member, "cursor_key", key, "seq", h.ev.Seq, "cause", err)
						return fmt.Errorf("session wait 写交付水位: %w", err)
					}
				}
				slog.Info("session wait 命中并输出", append(diag.Attrs(ctx), "member", member,
					"session", h.msg.Room, "seq", h.ev.Seq, "follow", follow,
					"elapsed_ns", time.Since(runStarted).Nanoseconds())...)
				if !follow {
					return nil // 一次性原语：首个命中输出后退出 0（条 46）
				}
				// follow：本命中帧起再等一个完整空闲窗；排空可能已到点的旧火药
				//（card_wait startCardWaitIdle 同款排空-重置）。
				if idleTimer != nil {
					if !idleTimer.Stop() {
						select {
						case <-idleTimer.C:
						default:
						}
					}
					idleTimer.Reset(timeout)
				}
			}
			// 进程内扫描水位推进到已确认页的边界；持久游标只随输出推进。
			cursor = next
		}
		select {
		case <-ctx.Done():
			if follow {
				// SIGINT/SIGTERM（或父 ctx 取消）——主动退出 0（B365 双形态）。
				slog.Info("session wait follow 主动退出", "member", member)
				return nil
			}
			slog.Info("session wait 超时退出", "member", member, "timeout", timeout.String())
			return &exitCodeError{code: ExitTimeout, err: fmt.Errorf("session wait 超时")}
		case <-idleC:
			slog.Info("session wait follow 空闲超时退出", "member", member, "timeout", timeout.String())
			return &exitCodeError{code: ExitTimeout, err: fmt.Errorf("session wait 空闲超时")}
		case <-ticker.C:
		}
	}
}

// sessionWaitResidentUnchanged 复读会话驻场者并与启动地址比较（R plan Q10 乙）。返回 nil = 仍是启动
// 时的驻场者；否则返回可行动错误（读失败也返回错误——不得当「未变」继续挂着）。
func sessionWaitResidentUnchanged(st *ledger.Store, addr proto.DeliveryAddress) error {
	current, seated, err := st.SessionResident(addr.Session)
	if err != nil {
		slog.Error("session wait 复读驻场者失败", "session", addr.Session, "member", addr.Identity, "cause", err)
		return fmt.Errorf("session wait 复读会话 %s 驻场者: %w", addr.Session, err)
	}
	if seated && current.Identity == addr.Identity {
		return nil
	}
	now := "无驻场者"
	if seated {
		now = "现驻场者 " + current.Identity
	}
	slog.Warn("session wait 驻场者已变更，退出", "session", addr.Session, "started_as", addr.Identity, "now", now)
	return fmt.Errorf("会话 %s 的驻场者已不是本进程启动时的 %s（%s），本进程不再收件；"+
		"要继续收件先 handoff session sit %s --as <身份>，再重新 handoff session wait %s", addr.Session, addr.Identity, now, addr.Session, addr.Session)
}

// sessionWaitMatch 解码候选并经匹配函数判定是否命中驻场者地址（B435 C-137；与
// card_wait.go#cardWaitRoomMatch 同构）：载荷 by_system:null 的旧边界按结构行跳过（Recipients
// 收已解码消息，看不到 null）；解码失败或匹配读失败上抛（C-228：不得当未命中，否则持久水位会
// 越过该消息）。CLI 不自己解析 mentions/作者——判定只在 collab。
func sessionWaitMatch(svc *collab.Service, ev ledger.Event, addr proto.DeliveryAddress) (proto.RoomMessage, bool, error) {
	if ev.Type != ledger.EvRoomMessage {
		return proto.RoomMessage{}, false, nil
	}
	var msg proto.RoomMessage
	if err := json.Unmarshal(ev.Payload, &msg); err != nil {
		return msg, false, fmt.Errorf("session wait 事件 %d 载荷解码失败: %w", ev.Seq, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(ev.Payload, &fields); err != nil {
		return msg, false, fmt.Errorf("session wait 事件 %d 字段解码失败: %w", ev.Seq, err)
	}
	if raw, present := fields["by_system"]; present && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return msg, false, nil
	}
	hit, err := svc.Matches(msg, ev.Actor, addr)
	if err != nil {
		slog.Error("session wait 匹配函数判定失败", "session", addr.Session, "member", addr.Identity, "seq", ev.Seq, "cause", err)
		return msg, false, fmt.Errorf("session wait 事件 %d 寻址判定失败: %w", ev.Seq, err)
	}
	return msg, hit, nil
}

// sessionWaitReference 只投影回复引用条；积压摘要不需要逐条重算未读列表。
func sessionWaitReference(svc *collab.Service, msg proto.RoomMessage) (*proto.SessionCite, error) {
	return svc.MessageReference(msg)
}

// buildSessionWake 装配三件套（spec §4.3：命中条 + 引用条 + 未读数）。
// referenced 取 ReplyTo 指向的同会话消息；无有效引用锚时省键。unread 与
// ListSessions(驻场身份) 的 Unread 同一投影（C-141 / 条 44——同一游标介质，含命中条）。
// ev 用 ledger.Event：CLI 读侧 Store.EventsFromAsc 的既有返回类型
// （与 proto.LedgerEvent 同形；card_wait 同款）。
func buildSessionWake(svc *collab.Service, ev ledger.Event, msg proto.RoomMessage, member string) (proto.SessionWake, error) {
	wake := proto.SessionWake{
		Session: msg.Room,
		Hit: proto.SessionCite{
			Seq: ev.Seq, Room: msg.Room, Actor: ev.Actor, Body: msg.Body,
		},
	}
	var err error
	wake.Referenced, err = sessionWaitReference(svc, msg)
	if err != nil {
		return proto.SessionWake{}, err
	}
	summaries, err := svc.ListSessions(member)
	if err != nil {
		return proto.SessionWake{}, fmt.Errorf("session wait 读未读投影: %w", err)
	}
	for _, summary := range summaries {
		if summary.ID == msg.Room {
			wake.Unread = summary.Unread
			break
		}
	}
	return wake, nil
}

// —— B358.5 续写：list/detail/create/archive/join/leave/send 六子命令 ——
//
// 身份两字段（B358.4 拍板① 的 CLI 半边，docs/superpowers/plans/b358.5-plan.md §2.2）：
//   - 成员身份（--owner、--member、席位出示值）用统一记法 user:<name>/agent:<name>
//     并校验前缀——owner 是会话初始成员（契约条 3），web:<host> 是机器位不配当成员；
//   - 审计 actor 沿 CLI 既有注入面 ledgerActor()（cli:<user>@<host>），两字段不混用。
// session send 两形态：无席位 flag=人（ledgerActor）；--cli/--session 成对=协调者
// 席位（currentSeatIdentity 只消费 flag 对，环境席位键不参与——身份显式性与会话
// wait 的显式 member 对称）；单只 flag 用法错。kind 恒 user（发言自由，spec §4.2）。

// validSessionMemberIdentity 校验会话成员统一记法（B358.9 §8.3 CLI 半边）：
// 收敛到 proto 唯一定义处，退役与 gateway 侧同规则的重复实现（契约 §5 H 组条 42）。
func validSessionMemberIdentity(identity string) bool {
	return proto.ValidateMemberIdentity(identity)
}

var (
	sessionCreateOwner string
	// B444：项目内建会话三 flag。任一给出即走 agentd HTTP 编排（建会话 + 拉卡 + 同步拉起驻场者首轮）；
	// 全缺保留旧形态（本地账本直写，标题与 --owner 必填）。
	sessionCreateProject string
	sessionCreateCards   []string
	sessionCreateSquad   string
)

var sessionCreateCmd = &cobra.Command{
	Use:   "create [title]",
	Short: "建一场会话（群）：--project/--card/--squad 经 agentd 在项目内建会话并拉起驻场者（标题可空自动命名）；否则 --owner 必填、本地直写",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		title := ""
		if len(args) == 1 {
			title = strings.TrimSpace(args[0])
		}
		if cmd.Flags().Changed("project") || cmd.Flags().Changed("card") || cmd.Flags().Changed("squad") {
			return runSessionCreateInProject(cmd, title)
		}
		if title == "" {
			return fmt.Errorf("会话标题不能为空（项目内建会话可留空自动命名：handoff session create --project <项目> --squad <小队> [--card <卡号>]...）")
		}
		owner := strings.TrimSpace(sessionCreateOwner)
		if !validSessionMemberIdentity(owner) {
			return fmt.Errorf("--owner 必须是统一记法 user:<name> 或 agent:<name>（当前 %q）", sessionCreateOwner)
		}
		svc, st, err := openRoomService()
		if err != nil {
			return err
		}
		defer st.Close()
		actor := ledgerActor()
		session, err := svc.CreateSession(title, owner, actor)
		if err != nil {
			slog.Default().Warn("CLI 建会话失败", "title", title, "owner", owner, "actor", actor, "cause", err)
			return fmt.Errorf("建会话: %w", err)
		}
		slog.Default().Info("CLI 会话已创建", "session", session.ID, "title", title, "owner", owner, "actor", actor)
		return json.NewEncoder(cmd.OutOrStdout()).Encode(session)
	},
}

// runSessionCreateInProject 是 `session create --project ...` 的 HTTP 半边（B444）：组 proto.SessionCreateReq
// 发给 agentd（群主缺省由服务端取控制台身份；--owner 给了就带上，服务端校验记法）；stdout 一行
// SessionCreateResp JSON。驻场者首轮失败时服务端仍 200（会话与卡已建），stderr 额外提示重试命令。
func runSessionCreateInProject(cmd *cobra.Command, title string) error {
	project := strings.TrimSpace(sessionCreateProject)
	if project == "" {
		return fmt.Errorf("--card/--squad 须配 --project <项目>（会话属项目，项目建后不可改）")
	}
	cl, done, err := newTargetClient()
	if err != nil {
		return err
	}
	defer done()
	req := proto.SessionCreateReq{
		Title: title, Owner: strings.TrimSpace(sessionCreateOwner), Project: project,
		Cards: sessionCreateCards, Squad: strings.TrimSpace(sessionCreateSquad),
	}
	resp, err := cl.SessionCreate(cmd.Context(), req)
	if err != nil {
		slog.Default().Warn("CLI 项目内建会话失败", "project", project, "cards", len(req.Cards), "squad", req.Squad, "cause", err)
		return fmt.Errorf("项目内建会话: %w", err)
	}
	slog.Default().Info("CLI 项目内会话已创建", "session", resp.ID, "project", project, "title", resp.Title,
		"rejected", len(resp.RejectedCards), "resident_ok", resp.Resident != nil && resp.Resident.Error == "")
	if resp.Resident != nil && resp.Resident.Error != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "驻场者未拉起：%s\n重试：handoff session resident launch %s --squad <小队>\n",
			resp.Resident.Error, resp.ID)
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(resp)
}

var (
	sessionRenameAgent  string
	sessionRenameExpect string
)

// sessionRenameCmd 改会话名（B444 S7）。两种身份出示：
//   - --agent <名>：以 agent:<名> 改名（push 驻场者用；与 session send --agent 同一自报信任面），本地账本经
//     collab.Service.RenameSession 唯一入口写；
//   - 不带 --agent：经 agentd HTTP 以控制台身份（console_user，服务端注入）改名——与 Web 同一身份。
//
// 只有群主或现驻场者能改名；--expect-title 作 CAS 前值（标题已不是它 → 拒，名字不变）。
var sessionRenameCmd = &cobra.Command{
	Use:   "rename <session> <title>",
	Short: "改会话名（群主或驻场者）：--agent 以驻场身份改；--expect-title 当前标题须等于它才改（CAS）",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		session := strings.TrimSpace(args[0])
		title := strings.TrimSpace(strings.Join(args[1:], " "))
		if title == "" {
			return fmt.Errorf("新标题不能为空")
		}
		expect := sessionRenameExpect
		if strings.TrimSpace(sessionRenameAgent) == "" {
			cl, done, err := newTargetClient()
			if err != nil {
				return err
			}
			defer done()
			renamed, err := cl.SessionRename(cmd.Context(), session, proto.SessionRenameReq{Title: title, ExpectTitle: expect})
			if err != nil {
				slog.Default().Warn("CLI 会话改名失败（控制台身份）", "session", session, "cause", err)
				return fmt.Errorf("会话 %s 改名: %w", session, err)
			}
			slog.Default().Info("CLI 会话已改名（控制台身份）", "session", session, "title", renamed.Title)
			return json.NewEncoder(cmd.OutOrStdout()).Encode(renamed)
		}
		actor, err := proto.MemberIdentity(proto.IdentityKindAgent, strings.TrimSpace(sessionRenameAgent))
		if err != nil {
			return fmt.Errorf("--agent 启动身份非法: %w", err)
		}
		svc, st, err := openRoomService()
		if err != nil {
			return err
		}
		defer st.Close()
		renamed, err := svc.RenameSession(session, title, expect, actor)
		if err != nil {
			slog.Default().Warn("CLI 会话改名失败", "session", session, "actor", actor, "cause", err)
			if errors.Is(err, collab.ErrTitleConflict) {
				return fmt.Errorf("会话 %s 改名被拒：标题已不是 %q（多半是人已手动命名，保留即可）: %w", session, expect, err)
			}
			return fmt.Errorf("会话 %s 改名: %w", session, err)
		}
		slog.Default().Info("CLI 会话已改名", "session", session, "title", renamed.Title, "actor", actor)
		return json.NewEncoder(cmd.OutOrStdout()).Encode(renamed)
	},
}

var sessionResidentCmd = &cobra.Command{
	Use:   "resident",
	Short: "会话驻场者（handoff 拉起的 push 驻场者）：launch",
}

var sessionResidentLaunchSquad string

// sessionResidentLaunchCmd 为会话叫 push 驻场者（B444 plan Q4 甲）：POST /api/sessions/{id}/resident/launch，
// 与建会话共用服务端编排。首轮失败时会话里已有系统指针行；本命令非 0 退出并透传服务端原因。
var sessionResidentLaunchCmd = &cobra.Command{
	Use:   "launch <session>",
	Short: "为会话拉起 push 驻场者（--squad 协调小队；缺省取唯一协调小队）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		session := strings.TrimSpace(args[0])
		cl, done, err := newTargetClient()
		if err != nil {
			return err
		}
		defer done()
		squad := strings.TrimSpace(sessionResidentLaunchSquad)
		outcome, err := cl.SessionResidentLaunch(cmd.Context(), session, proto.SessionResidentLaunchReq{Squad: squad})
		if err != nil {
			slog.Default().Warn("CLI 叫驻场者失败", "session", session, "squad", squad, "cause", err)
			return fmt.Errorf("为 %s 叫驻场者: %w", session, err)
		}
		slog.Default().Info("CLI 驻场者已拉起", "session", session, "identity", outcome.Identity,
			"carrier", outcome.Carrier, "carrier_machine", outcome.CarrierMachine)
		return json.NewEncoder(cmd.OutOrStdout()).Encode(outcome)
	},
}

var (
	sessionListMember string
	sessionListJSON   bool
)

var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "列会话（谁需要我：未读、需要你标签；--member 投影未读；--json 机读）",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		svc, st, err := openRoomService()
		if err != nil {
			return err
		}
		defer st.Close()
		summaries, err := svc.ListSessions(sessionListMember)
		if err != nil {
			slog.Default().Warn("CLI 会话列表失败", "member", sessionListMember, "cause", err)
			return fmt.Errorf("列会话: %w", err)
		}
		// 呈现排序（cmd 层）：最近活动降序（谁需要我语义）；门面返回创建序不动
		//（0.2#11）。稳定排序保平手时创建序，输出形状确定（RFC3339Nano 纳秒精度，
		// 0.2#15）。
		sort.SliceStable(summaries, func(i, j int) bool {
			return summaries[i].LastActivity.After(summaries[j].LastActivity)
		})
		if sessionListJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			for _, summary := range summaries {
				if err := enc.Encode(summary); err != nil {
					return err
				}
			}
			slog.Default().Info("CLI 会话列表已输出", "member", sessionListMember, "count", len(summaries), "format", "json")
			return nil
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\t标题\t群主\t归档\t未读\t需要你\t最近活动")
		for _, summary := range summaries {
			needs := ""
			if summary.NeedsHuman {
				needs = "需要你"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%v\t%d\t%s\t%s\n",
				summary.ID, summary.Title, summary.Owner, summary.Archived,
				summary.Unread, needs, summary.LastActivity.Format("2006-01-02 15:04:05"))
		}
		slog.Default().Info("CLI 会话列表已输出", "member", sessionListMember, "count", len(summaries))
		return w.Flush()
	},
}

var sessionDetailJSON bool

var sessionDetailCmd = &cobra.Command{
	Use:   "detail <session>",
	Short: "会话详情三块：成员 / 派发节点 / timeline（--json 机读）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		svc, st, err := openRoomService()
		if err != nil {
			return err
		}
		defer st.Close()
		detail, err := svc.SessionDetail(args[0])
		if err != nil {
			slog.Default().Warn("CLI 会话详情失败", "session", args[0], "cause", err)
			// mapSessionError 已把 ErrNotFound 替换成裸 ErrNoRoom（0.2#4，Store 层
			// 含 id 文案丢失）——边界把 id 包回去（gateway handleSessionDetail 先例）。
			return fmt.Errorf("会话 %s: %w", args[0], err)
		}
		if sessionDetailJSON {
			slog.Default().Info("CLI 会话详情已输出", "session", args[0], "format", "json")
			return json.NewEncoder(cmd.OutOrStdout()).Encode(detail)
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
		fmt.Fprintln(w, "成员:")
		for _, m := range detail.Summary.Members {
			card := m.CardID
			if card == "" {
				card = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", m.Identity, m.Kind, m.Status, card)
		}
		fmt.Fprintln(w, "节点:")
		for _, n := range detail.Nodes {
			card := n.CardID
			if card == "" {
				card = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", card, n.Node, n.State)
		}
		fmt.Fprintln(w, "时间线:")
		for _, ev := range detail.Timeline {
			card := ev.CardID
			if card == "" {
				card = "-"
			}
			fmt.Fprintf(w, "#%d\t%s\t%s\t%s\n", ev.Seq, ev.Kind, card, ev.Detail)
		}
		slog.Default().Info("CLI 会话详情已输出", "session", args[0],
			"members", len(detail.Summary.Members), "nodes", len(detail.Nodes), "timeline", len(detail.Timeline))
		return w.Flush()
	},
}

var sessionArchiveCmd = &cobra.Command{
	Use:   "archive <session>",
	Short: "归档会话（幂等；归档后只读，卡的终态不等于会话结束）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		svc, st, err := openRoomService()
		if err != nil {
			return err
		}
		defer st.Close()
		actor := ledgerActor()
		if err := svc.ArchiveSession(args[0], actor); err != nil {
			slog.Default().Warn("CLI 归档会话失败", "session", args[0], "actor", actor, "cause", err)
			return fmt.Errorf("归档会话 %s: %w", args[0], err)
		}
		slog.Default().Info("CLI 会话已归档", "session", args[0], "actor", actor)
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]bool{"ok": true})
	},
}

var sessionJoinCmd = &cobra.Command{
	Use:   "join <session> <card>",
	Short: "拉卡进会话（进群≠配人，配人仍走卡上三按钮；一卡同时只挂一个会话）",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		svc, st, err := openRoomService()
		if err != nil {
			return err
		}
		defer st.Close()
		actor := ledgerActor()
		if err := svc.JoinCard(args[0], args[1], actor); err != nil {
			slog.Default().Warn("CLI 拉卡进群失败", "session", args[0], "card", args[1], "actor", actor, "cause", err)
			return fmt.Errorf("session join %s %s: %w", args[0], args[1], err)
		}
		slog.Default().Info("CLI 卡已进群", "session", args[0], "card", args[1], "actor", actor)
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]bool{"ok": true})
	},
}

var sessionLeaveCmd = &cobra.Command{
	Use:   "leave <session> <card>",
	Short: "把卡移出会话（幂等：不在会话内亦成功）",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		svc, st, err := openRoomService()
		if err != nil {
			return err
		}
		defer st.Close()
		actor := ledgerActor()
		if err := svc.LeaveCard(args[0], args[1], actor); err != nil {
			slog.Default().Warn("CLI 移卡出群失败", "session", args[0], "card", args[1], "actor", actor, "cause", err)
			return fmt.Errorf("session leave %s %s: %w", args[0], args[1], err)
		}
		slog.Default().Info("CLI 卡已移出会话", "session", args[0], "card", args[1], "actor", actor)
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]bool{"ok": true})
	},
}

var (
	sessionSendRefs    []string
	sessionSendMention []string
	sessionSendCLI     string
	sessionSendSession string
	// sessionSendAgent 是主 agent 的启动身份出示（B358.9 接缝 #5）：外部
	// harness 自建会话/进成员后，用 `--agent <启动身份>` 以 `agent:<启动身份>`
	// 发言（写权是字符串等值，唯一命中路径）。缺则 fail-closed——不带 flag 且
	// 不带席位对时用 ledgerActor（审计 actor），不冒充主 agent。
	sessionSendAgent string
	// sessionSendReplyTo 被回复消息的账本 seq（B365 回复发送半边）：透传进
	// proto.RoomMessage.ReplyTo，接收侧 ResolveDelivery 隐式寻址原作者（冻结
	// 判定，本卡零改动）。0 = 无回复锚，与缺省等价；负值用法错。
	sessionSendReplyTo int64
)

var sessionSendCmd = &cobra.Command{
	Use:   "send <session> <text...>",
	Short: "会话发言（正文有效 @ 自动寻址；--agent 出示主 agent 身份；--cli/--session 成对出示协调者席位）",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		// 身份判定先于开账本：用法错误不触账本（0 落账）。三形态（B358.9）：
		// --agent=主 agent 启动身份（agent:<名>）；成对 flag=协调者席位
		// （cli:<cli>#<session>，自报身份需成对 flag）；无 flag=人尺度审计 actor
		// （ledgerActor）。三形态互斥。
		cliChanged := cmd.Flags().Changed("cli")
		sessChanged := cmd.Flags().Changed("session")
		if cliChanged != sessChanged {
			return fmt.Errorf("席位身份必须成对出示：--cli 与 --session 需同时给出（自报身份需成对 flag）")
		}
		if sessionSendAgent != "" && cliChanged {
			return fmt.Errorf("--agent 与 --cli/--session 互斥：一条发言只有一个作者")
		}
		if sessionSendReplyTo < 0 {
			return fmt.Errorf("--reply-to 必须为非负 seq（当前 %d）；0 = 无回复锚", sessionSendReplyTo)
		}
		body := strings.TrimSpace(strings.Join(args[1:], " "))
		if body == "" {
			return fmt.Errorf("消息正文不能为空")
		}
		actor, err := sessionSendActor(sessionSendAgent, sessionSendCLI, sessionSendSession, cliChanged)
		if err != nil {
			return err
		}
		svc, st, err := openRoomService()
		if err != nil {
			return err
		}
		defer st.Close()
		msg := proto.RoomMessage{
			Kind: proto.RoomMsgUser, Body: body,
			Refs: sessionSendRefs, Mentions: sessionMessageMentions(body, sessionSendMention),
			ReplyTo: sessionSendReplyTo,
		}
		seq, err := svc.Send(args[0], msg, actor)
		if err != nil {
			slog.Default().Warn("CLI 会话消息发送失败", "session", args[0], "actor", actor, "cause", err)
			return fmt.Errorf("发送到 %s: %w", args[0], err)
		}
		slog.Default().Info("CLI 会话消息已发送", "session", args[0], "kind", proto.RoomMsgUser, "actor", actor, "seq", seq)
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"ok": true, "seq": seq})
	},
}

var sessionCardMention = regexp.MustCompile(`^[A-Z]{1,4}[0-9]+(\.[0-9]+)*$`)

// sessionMessageMentions 把正文中完整的 @身份/@卡号编译为既有 mentions，
// 保留显式 --mention 的先行次序与宽松兼容口径，统一去重以免重复寻址。
func sessionMessageMentions(body string, explicit []string) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(raw string, explicit bool) {
		raw = strings.TrimSpace(raw)
		canonical := strings.TrimSpace(strings.TrimPrefix(raw, "@"))
		if canonical == "" || seen[canonical] {
			return
		}
		seen[canonical] = true
		if explicit {
			// 只在 Service.Send 写入边界剥一次；这里保留原文，避免 @@ 被剥两次。
			out = append(out, raw)
		} else {
			out = append(out, canonical)
		}
	}
	for _, token := range explicit {
		add(token, true)
	}
	for _, field := range strings.Fields(body) {
		if !strings.HasPrefix(field, "@") {
			continue
		}
		token := strings.TrimPrefix(field, "@")
		if proto.ValidateMemberIdentity(token) || sessionCardMention.MatchString(token) {
			add(token, false)
		}
	}
	return out
}

// sessionSendActor 决议 session send 的发言 actor（B358.9 接缝 #5）。
//
// 三形态（互斥，调用方已保证 --agent 与席位对不并用、席位对已完整）：
//   - agentFlag 非空：主 agent 出示启动身份，编码为 agent:<启动身份>；名字不合法
//     即 fail-closed（可行动报错），绝不回落到别的脸。
//   - cliChanged：协调者席位 cli:<cli>#<session>（currentSeatIdentity）。
//   - 否则：ledgerActor()（CLI 审计 actor，人尺度）。
//
// 抽成独立函数是为了让「三形态决议 + fail-closed」可单测，不与账本打开耦合。
func sessionSendActor(agentFlag, cliFlag, sessionFlag string, cliChanged bool) (string, error) {
	switch {
	case agentFlag != "":
		identity, err := proto.MemberIdentity(proto.IdentityKindAgent, agentFlag)
		if err != nil {
			return "", fmt.Errorf("--agent 启动身份非法: %w", err)
		}
		return identity, nil
	case cliChanged:
		return currentSeatIdentity(cliFlag, sessionFlag)
	default:
		return ledgerActor(), nil
	}
}

func init() {
	sessionWaitCmd.Flags().Int64Var(&sessionWaitSince, "since", -1, "手工回放起点（账本 seq，排他）；缺省从共享交付水位续收积压")
	sessionWaitCmd.Flags().DurationVar(&sessionWaitTimeout, "timeout", 0,
		"缺省=等待总时长，--follow=命中间空闲上限；到点以 124 退出；0 = 不限")
	sessionWaitCmd.Flags().BoolVar(&sessionWaitFollow, "follow", false,
		"常驻订阅：每个命中各输出一行后继续（SIGINT/SIGTERM 退出 0）")
	sessionCmd.AddCommand(sessionWaitCmd)
	sessionCreateCmd.Flags().StringVar(&sessionCreateOwner, "owner", "", "群主身份（统一记法 user:<name>/agent:<name>；本地直写形态必填，项目内形态缺省=agentd 控制台身份）")
	sessionCreateCmd.Flags().StringVar(&sessionCreateProject, "project", "", "会话所属项目（给出即经 agentd 项目内建会话）")
	sessionCreateCmd.Flags().StringArrayVar(&sessionCreateCards, "card", nil, "拉进会话的卡号（可重复；只收本项目、未终态、未进会话的卡）")
	sessionCreateCmd.Flags().StringVar(&sessionCreateSquad, "squad", "", "驻场小队（role=coordinator；缺省取唯一协调小队）")
	sessionRenameCmd.Flags().StringVar(&sessionRenameAgent, "agent", "", "以驻场身份 agent:<名> 改名（本地账本）；缺省经 agentd 以控制台身份改名")
	sessionRenameCmd.Flags().StringVar(&sessionRenameExpect, "expect-title", "", "CAS：当前标题须等于它才改（如 "+proto.SessionUntitledTitle+"）")
	sessionResidentLaunchCmd.Flags().StringVar(&sessionResidentLaunchSquad, "squad", "", "驻场小队（role=coordinator；缺省取唯一协调小队）")
	sessionResidentCmd.AddCommand(sessionResidentLaunchCmd)
	sessionCmd.AddCommand(sessionRenameCmd, sessionResidentCmd)
	sessionListCmd.Flags().StringVar(&sessionListMember, "member", "", "投影该成员身份的未读数（缺省不投影）")
	sessionListCmd.Flags().BoolVar(&sessionListJSON, "json", false, "输出 SessionSummary JSON 流（每会话一行）")
	sessionDetailCmd.Flags().BoolVar(&sessionDetailJSON, "json", false, "输出 SessionDetail JSON（一行）")
	sessionSendCmd.Flags().StringArrayVar(&sessionSendRefs, "ref", nil, "引用锚（git 路径/timeline 锚/卡号/附件路径，可重复）")
	sessionSendCmd.Flags().StringArrayVar(&sessionSendMention, "mention", nil, "显式成员或卡号（@ 前缀可选；可重复；与正文有效 @ 合并去重）")
	sessionSendCmd.Flags().Int64Var(&sessionSendReplyTo, "reply-to", 0, "被回复消息的账本 seq（回复锚；隐式寻址原作者；0 = 无）")
	sessionSendCmd.Flags().StringVar(&sessionSendCLI, "cli", "", "手填当前会话物种名（需与 --session 成对）")
	sessionSendCmd.Flags().StringVar(&sessionSendSession, "session", "", "手填当前会话 id（需与 --cli 成对）")
	sessionSendCmd.Flags().StringVar(&sessionSendAgent, "agent", "", "以主 agent 启动身份发言（编码为 agent:<启动身份>；与 --cli/--session 互斥）")
	sessionCmd.AddCommand(sessionCreateCmd, sessionListCmd, sessionDetailCmd, sessionArchiveCmd, sessionJoinCmd, sessionLeaveCmd, sessionSendCmd)
	rootCmd.AddCommand(sessionCmd)
}
