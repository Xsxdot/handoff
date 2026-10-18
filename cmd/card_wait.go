// card wait：账本单流多路 wait。
//
// 职责：跟一张卡（或其动态重算的子树）的账本事件流，逐事件输出，全部成员
// 达骨架终态即退出；本卡席位为 pull 时，另开房间命中段（B435 ①），收寻址命中
// 本卡席位地址的会话房间消息（判定只走 collab.Service.Matches），积压先于实时，
// 输出成功后推进持久交付游标。
// 边界：房间命中段只看本卡席位地址，不随 --subtree 扩展；不判 push 席位（push
// 由 agentd 唤醒）、不写驻场者地址。
// 边界：不碰执行域的 task wait（那是 cmd/wait.go 的 handoff wait <task>）；
// 两者是分层关系——外层用本命令管卡的调度，醒来后处置具体 task 事件仍用
// 执行域动词（reply/approve/continue）。
package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/logx"
	"github.com/Xsxdot/handoff/internal/proto"
)

// cardWaitSubtree 扩展到子树（后代 + 并入成员，每轮动态重算）。
var cardWaitSubtree bool

// cardWaitFollow 开启持续输出；未开启时首个可动作事件输出成功即退出。
var cardWaitFollow bool

// cardWaitSince 是 --since N 的值；是否设置以 Flags().Changed 判（0 是合法起点）。
var cardWaitSince int64

// cardWaitTimeout 总时长，0 = 不限；超时以 ExitTimeout(124) 退出，与执行域
// wait 的超时码一致，脚本侧可用同一套判断。
var cardWaitTimeout time.Duration

const cardSnapshotType = "card_snapshot"

// cardSnapshotLine 是 card wait 建连时输出的只读快照。不回放历史事件：
// 子树成员集会漂，欠单用派生视图（与看板 OpenTicketCounts 同一把尺）。
type cardSnapshotLine struct {
	Type       string               `json:"type"`
	CardID     string               `json:"card_id"`
	Subtree    bool                 `json:"subtree"`
	FromSeq    int64                `json:"from_seq"`
	Members    []string             `json:"members"`
	Actionable []cardSnapshotTicket `json:"actionable"`
	Needs      []cardSnapshotNeed   `json:"needs"`
}

type cardSnapshotTicket struct {
	CardID   string          `json:"card_id"`
	Target   string          `json:"source_target"`
	TaskID   string          `json:"source_task"`
	TicketID string          `json:"ticket_id"`
	TaskType string          `json:"task_type"`
	Payload  json.RawMessage `json:"payload"`
}

type cardSnapshotNeed struct {
	CardID string `json:"card_id"`
	Reason string `json:"reason"`
}

// cardWaitCmd 阻塞跟随一张卡（或整棵子树）的账本事件流，并收寻址命中本卡 pull 席位的
// 会话房间消息。
var cardWaitCmd = &cobra.Command{
	Use:   "wait <id>",
	Short: "跟随卡的账本事件流（--subtree 跟整棵子树）与寻址命中本卡 pull 席位的会话消息，可动作事件一行一唤醒",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if cardWaitTimeout < 0 {
			return fmt.Errorf("--timeout 必须为正时长（当前 %s）；不设上限请省略该参数", cardWaitTimeout)
		}
		return runCardWait(cmd, args[0], cardWaitSubtree, cardWaitFollow, cardWaitTimeout,
			cardWaitSince, cmd.Flags().Changed("since"))
	},
}

// cardWaitPollInterval 是实时段的轮询节拍（与 B353 起的 Follow 节拍同值）。
const cardWaitPollInterval = 2 * time.Second

// cardWaitEventPage 卡事件段单页读取条数（与 Store.Follow 同值）。
const cardWaitEventPage = 500

// cardWaitBeforeJudge 是房间命中段候选页「候选查询之后、匹配判定之前」的确定性竞态测试缝
// （生产恒 nil；同 session wait 的 sessionWaitBeforeJudge）。C-117 的 revision 护栏回归
// 测试在此注入席位变化。
var cardWaitBeforeJudge func(from, to int64)

// cardWaitRoom 是房间命中段的启动时状态（契约 C-113）：地址在启动时按本卡当前席位
// 构造一次，之后不随换绑漂移（换绑后旧地址自然不再命中）。
type cardWaitRoom struct {
	addr proto.DeliveryAddress
	key  string // 持久游标键 = DeliveryCursorKey(addr) = 席位身份原样（C-112）
	scan int64  // 进程内扫描水位：(scan, fence] 已在确认的席位状态下完成候选枚举与判定
}

// cardWaitItem 是合并输出序列的一项：卡事件段的事件，或房间命中段已判定命中的事件。
type cardWaitItem struct {
	ev   ledger.Event
	room bool
}

// runCardWait 跟随卡或动态子树的账本单流，并合并房间命中段（B435 ①）。
//
// 两段同一个按 fence 推进的轮询循环（契约 §13 第 3 条）：每拍读一次 MaxSeq 作 fence，
// 卡事件段读成员卡 (cardFrom, fence]，房间命中段读本卡席位地址候选 (scan, fence] 并逐条
// 经 collab.Service.Matches 判定，两段按 seq 合并输出、进程内去重（C-125）。房间段起点
// 早于启动时 MaxSeq 的部分即积压，按 seq 天然先于任何实时行（C-118/C-121）。
//
// 默认模式输出首个可动作行（卡事件或房间命中）后退出 0；follow 模式持续输出，直到当前
// 成员全部进入已完成/终止。房间命中行写 stdout 成功后才推进持久游标（C-122）；stdout 写
// 失败或游标写失败非 0 退出（C-123/C-124）。--since N：两段都从 seq>N 读，不读写持久游标
// （C-102–C-104），快照 from_seq=N（plan Q3 甲）。默认 timeout 是等待总时长，follow timeout
// 是任意账本事件之间的空闲上限；0 表示不设限，超时为 124。
func runCardWait(cmd *cobra.Command, cardID string, subtree, follow bool, timeout time.Duration, since int64, sinceSet bool) error {
	slog.SetDefault(logx.Setup("cli", ""))
	slog.Info("card wait 入口", "card", cardID, "subtree", subtree, "follow", follow,
		"timeout", timeout.String(), "since", since, "since_set", sinceSet)
	if sinceSet && since < 0 {
		return fmt.Errorf("--since 必须为非负 seq（当前 %d）", since)
	}
	st, err := openLedger()
	if err != nil {
		slog.Error("card wait 打开账本失败", "card", cardID, "subtree", subtree,
			"follow", follow, "timeout", timeout.String(), "cause", err)
		return fmt.Errorf("card %s wait 打开账本: %w", cardID, err)
	}
	defer st.Close()
	card, err := st.GetCard(cardID)
	if err != nil {
		slog.Error("card wait 卡不存在或读取失败", "card", cardID, "subtree", subtree,
			"follow", follow, "timeout", timeout.String(), "cause", err)
		return fmt.Errorf("card %s wait 读取卡: %w", cardID, err)
	}
	members := func() ([]string, error) {
		if subtree {
			return st.Subtree(cardID)
		}
		return []string{cardID}, nil
	}
	start, err := st.MaxSeq()
	if err != nil {
		slog.Error("card wait 读取起点失败", "card", cardID, "subtree", subtree,
			"follow", follow, "timeout", timeout.String(), "cause", err)
		return fmt.Errorf("card %s wait 读取起点: %w", cardID, err)
	}
	cardFrom := start
	if sinceSet {
		cardFrom = since
	}
	ctx := cmd.Context()
	noteActivity := func() {}
	stopIdle := func() {}
	timedOut := func() bool { return false }
	if follow && timeout > 0 {
		ctx, noteActivity, stopIdle, timedOut = startCardWaitIdle(ctx, timeout)
		defer stopIdle()
	} else if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	snapshotIDs, err := members()
	if err != nil {
		slog.Error("card wait 解析快照成员失败", "card", cardID, "subtree", subtree, "cause", err)
		return fmt.Errorf("解析成员集: %w", err)
	}
	if err := encodeCardWaitSnapshot(enc, st, cardID, subtree, cardFrom, snapshotIDs); err != nil {
		return err
	}
	room, err := cardWaitRoomSegment(ctx, st, card, start, since, sinceSet)
	if err != nil {
		return err
	}
	checkDone := func() (bool, error) {
		ids, err := members()
		if err != nil {
			return false, fmt.Errorf("card %s wait 解析成员: %w", cardID, err)
		}
		for _, id := range ids {
			card, err := st.GetCard(id)
			if err != nil {
				return false, fmt.Errorf("card %s wait 读取成员 %s: %w", cardID, id, err)
			}
			if card.Status != ledger.StatusDone && card.Status != ledger.StatusClosed {
				return false, nil
			}
		}
		return true, nil
	}
	if done, err := checkDone(); err != nil {
		slog.Error("card wait 初始终态检查失败", "card", cardID, "subtree", subtree,
			"follow", follow, "timeout", timeout.String(), "cause", err)
		return err
	} else if done {
		slog.Info("card wait 初始即全部终态", "card", cardID, "subtree", subtree,
			"follow", follow)
		fmt.Fprintln(cmd.ErrOrStderr(), "子树已全部完成")
		return nil
	}
	var svc *collab.Service
	if room != nil {
		svc = roomServiceFor(st)
	}
	allDone := errors.New("all-done")
	emitted := map[int64]bool{}
	// handle 处理合并序列的一项；返回 allDone 表示正常退出（一次性首个输出 / 全部终态）。
	handle := func(item cardWaitItem) error {
		e := item.ev
		if emitted[e.Seq] {
			slog.Debug("card wait 同 seq 已输出，跳过", "card", cardID, "seq", e.Seq)
			return nil
		}
		noteActivity()
		if item.room {
			if err := enc.Encode(e); err != nil {
				slog.Error("card wait 输出房间命中失败（游标不推进）", "card", cardID, "seq", e.Seq, "cause", err)
				return fmt.Errorf("card %s wait 输出房间命中 seq=%d: %w", cardID, e.Seq, err)
			}
			emitted[e.Seq] = true
			slog.Info("card wait 已输出房间命中", "card", cardID, "seq", e.Seq, "follow", follow)
			if !sinceSet {
				if err := st.AdvanceSessionDeliveryCursor(room.key, e.Seq); err != nil {
					slog.Error("card wait 推进交付游标失败（该行下次允许重投）", "card", cardID,
						"seq", e.Seq, "cause", err)
					return fmt.Errorf("card %s wait 推进交付游标到 %d: %w", cardID, e.Seq, err)
				}
			}
			if !follow {
				return allDone
			}
			return nil
		}
		slog.Debug("card wait 收到账本事件", "card", e.CardID, "seq", e.Seq,
			"type", e.Type, "follow", follow)
		if e.Type == ledger.EvStatusMoved {
			done, err := checkDone()
			if err != nil {
				slog.Error("card wait 终态检查失败", "card", cardID, "seq", e.Seq,
					"type", e.Type, "cause", err)
				return err
			}
			if done {
				slog.Info("card wait 成员全部终态", "card", cardID, "seq", e.Seq,
					"type", e.Type, "follow", follow)
				return allDone
			}
			return nil
		}
		actionable, err := cardWaitEventActionable(st, e)
		if err != nil {
			slog.Error("card wait 事件分类失败", "card", e.CardID, "seq", e.Seq,
				"type", e.Type, "cause", err)
			return err
		}
		if !actionable {
			slog.Debug("card wait 过滤审计事件", "card", e.CardID, "seq", e.Seq,
				"type", e.Type)
			return nil
		}
		if err := enc.Encode(e); err != nil {
			slog.Error("card wait 输出事件失败", "card", e.CardID, "seq", e.Seq,
				"type", e.Type, "cause", err)
			return err
		}
		emitted[e.Seq] = true
		slog.Info("card wait 已输出可动作事件", "card", e.CardID, "seq", e.Seq,
			"type", e.Type, "follow", follow)
		if !follow {
			return allDone
		}
		return nil
	}
	err = cardWaitLoop(ctx, st, svc, room, members, cardFrom, handle)
	switch {
	case errors.Is(err, allDone):
		if follow {
			slog.Info("card wait 成员全部终态，follow 退出", "card", cardID)
		} else {
			slog.Info("card wait 首个可动作行已输出，一次性退出", "card", cardID)
		}
		return nil
	case follow && errors.Is(err, context.Canceled) && timedOut():
		slog.Error("card wait follow 空闲超时", "card", cardID, "subtree", subtree,
			"follow", follow, "timeout", timeout.String(), "cause", err)
		return &exitCodeError{code: ExitTimeout, err: fmt.Errorf("wait --card 空闲超时")}
	case errors.Is(err, context.DeadlineExceeded):
		slog.Error("card wait 总时长超时", "card", cardID, "subtree", subtree,
			"follow", follow, "timeout", timeout.String(), "cause", err)
		return &exitCodeError{code: ExitTimeout, err: fmt.Errorf("wait --card 超时")}
	default:
		if err != nil {
			slog.Error("card wait 异常退出", "card", cardID, "subtree", subtree,
				"follow", follow, "timeout", timeout.String(), "cause", err)
		}
		return err
	}
}

// cardWaitRoomSegment 决定房间命中段是否开启并给出起点（C-113/C-115/C-119）。
// 关闭条件：本卡空座、席位 mode≠pull、卡不属任何会话——返回 nil（卡事件段照常）。
// 起点：--since → N；否则持久游标行存在 → 行值；无行 → 本卡上该身份最近一条坐下/换绑
// 事件 seq（Q2 甲）；连该事件也查不到（B358 前的存量席位）→ 启动时 MaxSeq（不补发历史）。
func cardWaitRoomSegment(ctx context.Context, st *ledger.Store, card ledger.Card, start, since int64, sinceSet bool) (*cardWaitRoom, error) {
	if card.DriverSession == "" || card.DriverMode != string(proto.SeatModePull) {
		slog.Info("card wait 房间命中段关闭：本卡不是 pull 席位", "card", card.ID,
			"has_seat", card.DriverSession != "", "mode", card.DriverMode)
		return nil, nil
	}
	session, err := st.SessionOfCard(card.ID)
	if err != nil {
		slog.Error("card wait 读卡所属会话失败", "card", card.ID, "cause", err)
		return nil, fmt.Errorf("card %s wait 读所属会话: %w", card.ID, err)
	}
	if session == "" {
		slog.Info("card wait 房间命中段关闭：卡不属任何会话", "card", card.ID)
		return nil, nil
	}
	room := &cardWaitRoom{addr: proto.DeliveryAddress{
		Kind: proto.DeliverySeat, Session: session, Card: card.ID, Identity: card.DriverSession,
	}}
	room.key = proto.DeliveryCursorKey(room.addr)
	if room.key == "" {
		err := fmt.Errorf("card %s wait 席位地址游标键为空", card.ID)
		slog.Error("card wait 席位地址非法", "card", card.ID, "cause", err)
		return nil, err
	}
	origin := "since"
	switch {
	case sinceSet:
		room.scan = since
	default:
		seq, ok, err := st.SessionDeliveryCursorRowContext(ctx, room.key)
		if err != nil {
			slog.Error("card wait 读交付游标失败", "card", card.ID, "cause", err)
			return nil, fmt.Errorf("card %s wait 读交付游标: %w", card.ID, err)
		}
		if ok {
			room.scan, origin = seq, "cursor"
			break
		}
		lower, ok, err := st.SeatBoundSeqContext(ctx, card.ID, card.DriverSession)
		if err != nil {
			slog.Error("card wait 读席位积压下界失败", "card", card.ID, "cause", err)
			return nil, fmt.Errorf("card %s wait 读席位积压下界: %w", card.ID, err)
		}
		if ok {
			room.scan, origin = lower, "seat_bound"
		} else {
			room.scan, origin = start, "no_bound_event"
		}
	}
	slog.Info("card wait 房间命中段开启", "card", card.ID, "session", session,
		"identity", card.DriverSession, "from_seq", room.scan, "origin", origin, "start", start)
	return room, nil
}

// cardWaitLoop 是单 fence 轮询循环（见 runCardWait）。handle 返回非 nil 即终止并原样上抛。
func cardWaitLoop(ctx context.Context, st *ledger.Store, svc *collab.Service, room *cardWaitRoom,
	members func() ([]string, error), cardFrom int64, handle func(cardWaitItem) error) error {
	tick := time.NewTicker(cardWaitPollInterval)
	defer tick.Stop()
	for {
		fence, err := st.MaxSeq()
		if err != nil {
			return fmt.Errorf("card wait 读 fence: %w", err)
		}
		items, err := cardWaitCardEvents(st, members, cardFrom, fence)
		if err != nil {
			return err
		}
		if room != nil {
			hits, err := cardWaitRoomHits(ctx, svc, st, room, fence)
			if err != nil {
				return err
			}
			items = append(items, hits...)
			sort.SliceStable(items, func(i, j int) bool { return items[i].ev.Seq < items[j].ev.Seq })
		}
		for _, item := range items {
			if err := handle(item); err != nil {
				return err
			}
		}
		if fence > cardFrom {
			cardFrom = fence
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// cardWaitCardEvents 读成员卡在 (from, fence] 的事件（分页直到穷尽）。成员集每拍重算：
// wait 挂起期间新拆/新派发的卡天然进流（同 Store.Follow）。
func cardWaitCardEvents(st *ledger.Store, members func() ([]string, error), from, fence int64) ([]cardWaitItem, error) {
	ids, err := members()
	if err != nil {
		return nil, fmt.Errorf("解析成员集: %w", err)
	}
	var out []cardWaitItem
	for from < fence {
		evs, err := st.EventsFromAsc(ids, from, cardWaitEventPage)
		if err != nil {
			return nil, err
		}
		for _, e := range evs {
			if e.Seq > fence {
				return out, nil
			}
			out = append(out, cardWaitItem{ev: e})
			from = e.Seq
		}
		if len(evs) < cardWaitEventPage {
			break
		}
	}
	return out, nil
}

// cardWaitRoomHits 在 (room.scan, fence] 内分页读席位地址候选，并在席位状态版本护栏内
// 逐条经 Matches 判定（C-116/C-117；B409r2 条 6–17 同形）：候选查询前读版本、该页判定后
// 复读；版本变了整页作废、从原下界重读；有界重试仍不稳定返回可见错误。确认后推进进程内
// 扫描水位（持久游标只在 stdout 输出成功后由调用方推进）。
func cardWaitRoomHits(ctx context.Context, svc *collab.Service, st *ledger.Store, room *cardWaitRoom, fence int64) ([]cardWaitItem, error) {
	var out []cardWaitItem
	for room.scan < fence {
		page, next, err := cardWaitRoomPage(ctx, svc, st, room, room.scan, fence)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		room.scan = next
	}
	return out, nil
}

func cardWaitRoomPage(ctx context.Context, svc *collab.Service, st *ledger.Store, room *cardWaitRoom, from, to int64) ([]cardWaitItem, int64, error) {
	for attempt := 1; ; attempt++ {
		revBefore, err := st.SeatRevisionContext(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("card wait 读席位状态版本: %w", err)
		}
		candidates, err := st.SeatMessageCandidatesContext(ctx, room.addr.Identity, room.addr.Session, from, to, sessionWaitPageLimit)
		if err != nil {
			return nil, 0, fmt.Errorf("card wait 读房间候选页: %w", err)
		}
		if cardWaitBeforeJudge != nil {
			cardWaitBeforeJudge(from, to)
		}
		var hits []cardWaitItem
		next := to
		for _, ev := range candidates {
			hit, err := cardWaitRoomMatch(svc, ev, room.addr)
			if err != nil {
				return nil, 0, err
			}
			if hit {
				hits = append(hits, cardWaitItem{ev: ev, room: true})
			}
		}
		if len(candidates) >= sessionWaitPageLimit {
			// 本页未穷尽 (from, to]：扫描水位只能推进到本页最后一条候选。
			next = candidates[len(candidates)-1].Seq
		}
		revAfter, err := st.SeatRevisionContext(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("card wait 复读席位状态版本: %w", err)
		}
		if revAfter != revBefore {
			slog.Warn("card wait 房间候选页席位状态发生变化，丢弃重读", "card", room.addr.Card,
				"from_seq", from, "to_seq", to, "rev_before", revBefore, "rev_after", revAfter,
				"attempt", attempt, "candidates", len(candidates))
			if attempt >= sessionWaitSeatRetries {
				return nil, 0, fmt.Errorf("card wait 房间候选页席位状态在 %d 次重读后仍不稳定（seq 范围 %d→%d）",
					sessionWaitSeatRetries, from, to)
			}
			continue
		}
		slog.Debug("card wait 房间候选页判定完成", "card", room.addr.Card, "from_seq", from,
			"to_seq", to, "candidates", len(candidates), "hits", len(hits), "attempt", attempt)
		return hits, next, nil
	}
}

// cardWaitRoomMatch 解码候选并经匹配函数判定是否命中本卡席位地址。载荷 by_system:null 的
// 旧边界按结构行跳过（Recipients 收已解码消息，看不到 null；与 agentd 解码层同判）。
// 解码失败或匹配读失败上抛（C-129/C-228：不得当未命中，否则持久水位会越过该消息）。
func cardWaitRoomMatch(svc *collab.Service, ev ledger.Event, addr proto.DeliveryAddress) (bool, error) {
	var msg proto.RoomMessage
	if err := json.Unmarshal(ev.Payload, &msg); err != nil {
		return false, fmt.Errorf("card wait 房间候选 seq=%d 解码: %w", ev.Seq, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(ev.Payload, &fields); err != nil {
		return false, fmt.Errorf("card wait 房间候选 seq=%d 字段解码: %w", ev.Seq, err)
	}
	if raw, present := fields["by_system"]; present && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false, nil
	}
	hit, err := svc.Matches(msg, ev.Actor, addr)
	if err != nil {
		slog.Error("card wait 匹配函数判定失败", "card", addr.Card, "seq", ev.Seq, "cause", err)
		return false, fmt.Errorf("card wait 房间候选 seq=%d 判定: %w", ev.Seq, err)
	}
	return hit, nil
}

// cardWaitEventActionable 根据 source identity、唯一任务等待策略与卡原生动作集合分类事件。
// 返回错误时表示 payload 已损坏或缺失，调用方必须携带事件上下文返回，不能
// 把无法判断的事件静默降级为审计。task_mirrored 的身份闸正文由
// client.JudgeMirroredWake 一处承载（--subtree 也用事件所属卡 e.CardID），
// 身份闸通过后才进入 client.WaitDeliveryPolicy 策略判断。
func cardWaitEventActionable(st *ledger.Store, ev ledger.Event) (bool, error) {
	switch ev.Type {
	case ledger.EvNeedsHuman, ledger.EvNeedsCleared,
		ledger.EvDecisionOpened, ledger.EvDecisionAnswered:
		return true, nil
	case ledger.EvTaskMirrored:
		var envelope struct {
			Node     *string         `json:"node"`
			Attempt  *string         `json:"attempt"`
			TaskType string          `json:"task_type"`
			Payload  json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(ev.Payload, &envelope); err != nil {
			return false, fmt.Errorf("card %s task_mirrored seq=%d type=%s 解包: %w",
				ev.CardID, ev.Seq, ev.Type, err)
		}
		if envelope.TaskType == "" || len(envelope.Payload) == 0 {
			return false, fmt.Errorf("card %s task_mirrored seq=%d type=%s 缺 task_type/payload",
				ev.CardID, ev.Seq, ev.Type)
		}
		decision, err := client.JudgeMirroredWake(st, client.WakeGateEvent{
			CardID: ev.CardID, Seq: ev.Seq, Node: envelope.Node, Attempt: envelope.Attempt,
			TaskType: envelope.TaskType, SourceTask: ev.SourceTask,
			SourceTarget: ev.SourceTarget, SourceSeq: ev.SourceSeq,
		})
		if err != nil {
			slog.Error("card wait task_mirrored 身份闸判定失败", "card", ev.CardID, "seq", ev.Seq,
				"type", ev.Type, "task_type", envelope.TaskType, "cause", err)
			return false, err
		}
		slog.Info("card wait task_mirrored 身份闸判定", "card", ev.CardID, "seq", ev.Seq,
			"type", ev.Type, "task_type", envelope.TaskType, "deliver", decision.Deliver,
			"reason", string(decision.Reason))
		if !decision.Deliver {
			return false, nil
		}
		return client.WaitDeliveryPolicy(proto.EventType(envelope.TaskType)), nil
	default:
		return false, nil
	}
}

// startCardWaitIdle 建立 follow 的空闲计时控制器。计时以 Store.Follow 收到
// 任意账本事件为准，过滤掉的审计事件也会调用 noteActivity；stop 等待控制
// goroutine 退出，避免一次性 CLI 测试或命令结束后遗留后台生命周期。
func startCardWaitIdle(parent context.Context, idle time.Duration) (
	ctx context.Context, noteActivity func(), stop func(), timedOut func() bool,
) {
	if idle <= 0 {
		return parent, func() {}, func() {}, func() bool { return false }
	}
	ctx, cancel := context.WithCancel(parent)
	// 每条账本事件都必须和 idle 控制器完成一次握手。容量 1 的非阻塞
	// 通知会把同一批回调压成一条，导致计时从批次中较早事件而非最后事件
	// 刷新；无缓冲通道让 Store.Follow 的回调在每次刷新后再继续。
	activity := make(chan struct{})
	expired := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(idle)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-activity:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(idle)
			case <-timer.C:
				close(expired)
				cancel()
				return
			}
		}
	}()
	noteActivity = func() {
		select {
		case activity <- struct{}{}:
		case <-ctx.Done():
			// 父 context 或 idle timer 已停止控制器；不能再等待接收者。
		}
	}
	stop = func() {
		cancel()
		<-done
	}
	timedOut = func() bool {
		select {
		case <-expired:
			return true
		default:
			return false
		}
	}
	return ctx, noteActivity, stop, timedOut
}

func encodeCardWaitSnapshot(enc *json.Encoder, st *ledger.Store, cardID string, subtree bool, fromSeq int64, members []string) error {
	want := make(map[string]bool, len(members))
	for _, id := range members {
		want[id] = true
	}
	tickets, err := st.OpenTickets()
	if err != nil {
		slog.Error("card wait 读未决工单失败", "card", cardID, "cause", err)
		return fmt.Errorf("读未决工单: %w", err)
	}
	actionable := make([]cardSnapshotTicket, 0)
	for _, ticket := range tickets {
		if !want[ticket.CardID] {
			continue
		}
		actionable = append(actionable, cardSnapshotTicket{
			CardID: ticket.CardID, Target: ticket.Target, TaskID: ticket.TaskID,
			TicketID: ticket.TicketID, TaskType: ticket.TaskType, Payload: ticket.Payload,
		})
	}
	reasons, err := st.NeedsReasons()
	if err != nil {
		slog.Error("card wait 读等人标记失败", "card", cardID, "cause", err)
		return fmt.Errorf("读等人标记: %w", err)
	}
	needs := make([]cardSnapshotNeed, 0)
	for _, id := range members {
		if reason := reasons[id]; reason != "" {
			needs = append(needs, cardSnapshotNeed{CardID: id, Reason: reason})
		}
	}
	line := cardSnapshotLine{
		Type: cardSnapshotType, CardID: cardID, Subtree: subtree, FromSeq: fromSeq,
		Members: members, Actionable: actionable, Needs: needs,
	}
	if err := enc.Encode(line); err != nil {
		return fmt.Errorf("写 card_snapshot: %w", err)
	}
	slog.Info("card wait 建连快照已输出", "card", cardID, "subtree", subtree,
		"members", len(members), "actionable", len(actionable), "needs", len(needs),
		"from_seq", fromSeq)
	return nil
}

func init() {
	cardWaitCmd.Flags().BoolVar(&cardWaitSubtree, "subtree", false, "扩展到子树（后代 + 并入成员，动态）")
	cardWaitCmd.Flags().BoolVar(&cardWaitFollow, "follow", false, "持续输出可动作事件，全部成员终态才退出")
	cardWaitCmd.Flags().DurationVar(&cardWaitTimeout, "timeout", 0,
		"超时（如 2h）；默认=等待总时长，--follow=空闲上限，到点以 124 退出")
	cardWaitCmd.Flags().Int64Var(&cardWaitSince, "since", -1,
		"两段都只读 seq>N 的事件；不读不写持久交付游标（回放/排障用）")
}
