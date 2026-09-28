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
package cmd

import (
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
	Use:   "wait <member>",
	Short: "订阅会话寻址：续收积压摘要或实时 SessionWake（缺省一条即退 0；--follow 常驻）",
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
	sessionWaitBeforeAdvance func(member string, seq int64)
)

// sessionHit 一条已判定命中的候选：原事件与解码后的消息。
type sessionHit struct {
	ev  ledger.Event
	msg proto.RoomMessage
}

// sessionWaitPage 读一页有界候选，并在席位状态版本护栏内完成该页全部
// MessageWakeTargets 判定。返回确认命中的行与推进后的进程内扫描水位 next。
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
func sessionWaitPage(ctx context.Context, svc *collab.Service, st *ledger.Store, member string, from, to int64) (hits []sessionHit, next int64, err error) {
	started := time.Now()
	for attempt := 1; ; attempt++ {
		revBefore, err := st.SeatRevisionContext(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("session wait 读席位状态版本: %w", err)
		}
		if sessionWaitBeforeQuery != nil {
			sessionWaitBeforeQuery(from, to)
		}
		candidates, err := st.SessionMessageCandidatesContext(ctx, member, from, to, sessionWaitPageLimit)
		if err != nil {
			return nil, 0, fmt.Errorf("session wait 读候选页: %w", err)
		}
		if sessionWaitBeforeJudge != nil {
			sessionWaitBeforeJudge(from, to)
		}
		pageHits := make([]sessionHit, 0, len(candidates))
		lastSeq := from
		for _, ev := range candidates {
			msg, hit, err := sessionWaitMatch(svc, ev, member)
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

// isWaitCancel 报告 err 是否为 wait ctx 的取消/到期（区别于真实读错误）。
// ctx 贯通查询后，取消会以查询错误形态冒出；这里统一识别，交给调用方按
// 运行模式映射退出语义（follow 退 0 / 一次性 124）。
func isWaitCancel(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// runSessionWait 订阅会话定向消息。无 --since 时从共享交付水位补收；显式
// --since 保持手工排他回放，不改变共享水位。寻址只经 MessageWakeTargets。
//
// 双形态（B365）共用有界候选读循环；默认先扫描积压，单行摘要成功写出后
// 推进共享交付水位。之后分叉点是「实时首个命中后是否退出」：
//   - 一次性（缺省）：首个命中输出一行 SessionWake 后退出 0（条 46）；--timeout
//     是等待总时长，到点 124；
//   - --follow：每个命中各自输出一行后重置空闲计时、继续推进游标等下一条，
//     不主动退出——SIGINT/SIGTERM 退出 0；--timeout 语义变为空闲上限（任意
//     两命中帧之间的最大间隔，0 = 不设限），到点 124（与 runCardWait 的
//     follow 语义同形，card_wait.go:55-56）。
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

func runSessionWait(cmd *cobra.Command, member string, sinceFlag int64, sinceSet bool, timeout time.Duration, follow bool) error {
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
	slog.Info("session wait 入口", append(diag.Attrs(ctx), "member", member, "since", sinceFlag,
		"since_set", sinceSet, "timeout", timeout.String(), "follow", follow)...)
	if sinceSet && sinceFlag < 0 {
		return fmt.Errorf("--since 必须为非负 seq（当前 %d）", sinceFlag)
	}
	openStarted := time.Now()
	svc, st, err := openRoomService()
	openNs := time.Since(openStarted).Nanoseconds()
	if err != nil {
		slog.Error("session wait 打开会话服务失败", append(diag.Attrs(ctx),
			"member", member, "open_ns", openNs, "cause", err)...)
		slog.Warn("session wait 完成", append(diag.Attrs(ctx), "command", "session_wait",
			"member", member, "outcome", "error", "error_class", "open_failed",
			"stdout_bytes", int64(0), "elapsed_ns", time.Since(started).Nanoseconds())...)
		return fmt.Errorf("session wait 打开会话服务: %w", err)
	}
	defer st.Close()
	counter := &countingWriter{w: cmd.OutOrStdout()}
	runErr := sessionWaitRun(ctx, svc, st, counter, member, sinceFlag, sinceSet, timeout, follow)

	// B409.6 收口：结果分类 + stdout 字节 + 总耗时；诊断只写 stderr（slog
	// 出口），stdout 的 JSON 行与退出码语义不变。
	attrs := append(diag.Attrs(ctx), "command", "session_wait", "member", member,
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
// 用它注入假 writer / 已关闭账本 / 竞态钩子）。ctx 只用于退出信号；查询自身
// 按页有界，取消语义沿用既有 select 轮询（两方言一致，契约第 55 条）。
func sessionWaitRun(ctx context.Context, svc *collab.Service, st *ledger.Store, out io.Writer, member string, sinceFlag int64, sinceSet bool, timeout time.Duration, follow bool) error {
	runStarted := time.Now()
	if !sinceSet {
		// 默认：读共享交付水位。读错必须显式失败——把读错当 0 会把停听期间
		// 已交付的窗口当作未交付全量重放，反向会把未交付窗口当已交付丢掉
		//（契约第 10/43/44 条的反向）。B409.6：点读随 ctx 取消。
		var err error
		sinceFlag, err = st.SessionDeliveryCursorContext(ctx, member)
		if err != nil {
			slog.Error("session wait 读取交付水位失败", append(diag.Attrs(ctx),
				"member", member, "cause", err)...)
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
			hits, next, err := sessionWaitPage(ctx, svc, st, member, scan, fence)
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
		if len(backlog.Hits) > 0 {
			// stdout 成功才算交付（契约第 58 条）；随后推进到最高命中 seq。
			if err := enc.Encode(backlog); err != nil {
				return fmt.Errorf("session wait 输出积压摘要: %w", err)
			}
			if sessionWaitBeforeAdvance != nil {
				sessionWaitBeforeAdvance(member, backlog.ToSeq)
			}
			if err := st.AdvanceSessionDeliveryCursor(member, backlog.ToSeq); err != nil {
				slog.Warn("session wait 积压已输出但进度写入失败", "member", member, "seq", backlog.ToSeq, "cause", err)
				return fmt.Errorf("session wait 写交付水位: %w", err)
			}
			slog.Info("session wait 积压已交付", "member", member, "from_seq", from,
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
			hits, next, err := sessionWaitPage(ctx, svc, st, member, cursor, fence)
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
						sessionWaitBeforeAdvance(member, h.ev.Seq)
					}
					if err := st.AdvanceSessionDeliveryCursor(member, h.ev.Seq); err != nil {
						slog.Warn("session wait 唤醒已输出但进度写入失败", "member", member, "seq", h.ev.Seq, "cause", err)
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

// sessionWaitMatch 仅在会话房间里调用协作域唯一寻址判据，防止 CLI 自己
// 根据 mentions/成员列表发明第二套唤醒规则。
func sessionWaitMatch(svc *collab.Service, ev ledger.Event, member string) (proto.RoomMessage, bool, error) {
	if ev.Type != ledger.EvRoomMessage {
		return proto.RoomMessage{}, false, nil
	}
	var msg proto.RoomMessage
	if err := json.Unmarshal(ev.Payload, &msg); err != nil {
		return msg, false, fmt.Errorf("session wait 事件 %d 载荷解码失败: %w", ev.Seq, err)
	}
	if !room.IsSessionRoom(msg.Room) {
		return msg, false, nil
	}
	targets, err := svc.MessageWakeTargets(msg)
	if err != nil {
		return msg, false, fmt.Errorf("session wait 事件 %d 寻址判定失败: %w", ev.Seq, err)
	}
	for _, target := range targets {
		if target == member {
			return msg, true, nil
		}
	}
	return msg, false, nil
}

// sessionWaitReference 只投影回复引用条；积压摘要不需要逐条重算未读列表。
func sessionWaitReference(svc *collab.Service, msg proto.RoomMessage) (*proto.SessionCite, error) {
	return svc.MessageReference(msg)
}

// buildSessionWake 装配三件套（spec §4.3：命中条 + 引用条 + 未读数）。
// referenced 取 ReplyTo 指向的同会话消息；无有效引用锚时省键。unread 与
// ListSessions(member) 的 Unread 同一投影（条 44——同一游标介质，含命中条）。
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

var sessionCreateOwner string

var sessionCreateCmd = &cobra.Command{
	Use:   "create <title>",
	Short: "建一场会话（群）：--owner 指定群主（统一记法 user:<name>/agent:<name>）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		title := strings.TrimSpace(args[0])
		if title == "" {
			return fmt.Errorf("会话标题不能为空")
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
	sessionCreateCmd.Flags().StringVar(&sessionCreateOwner, "owner", "", "群主身份（统一记法 user:<name>/agent:<name>，必填）")
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
