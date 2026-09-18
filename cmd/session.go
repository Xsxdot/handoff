// session.go 实现 handoff session 命令族（B358，P7 拍板：新词立新族）。
//
// 本卡（B358.3）只落 wait 子命令——外部会话（人/主 agent）的 R1 订阅通道
// （契约 §3.8/§4.6 条 39–46）：全流升序轮询读 + 复用冻结寻址唯一入口
// collab.Service.MessageWakeTargets 过滤，只收「寻址命中我」的会话消息；
// 一次性原语，命中输出恰一行 SessionWake JSON 后退出 0，超时 124，订阅只读。
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
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/logx"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/collab/room"
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
	Short: "订阅会话寻址：命中输出一行 SessionWake JSON（缺省首个命中即退 0；--follow 常驻）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if sessionWaitTimeout < 0 {
			return fmt.Errorf("--timeout 必须为正时长（当前 %s）；不设上限请省略该参数", sessionWaitTimeout)
		}
		return runSessionWait(cmd, args[0], sessionWaitSince, cmd.Flags().Changed("since"), sessionWaitTimeout, sessionWaitFollow)
	},
}

// runSessionWait 是 R1 通道本体（契约条 39–46）。member 与寻址 target 逐字
// 相等（不透明串，不做大小写/前后缀归一）；--since 缺省 = 启动时流尾（排他），
// 只等新事件；只对会话房间的 EvRoomMessage 做命中判定，其余事件推进游标、
// 零输出；不落账、不推进未读游标、不经 keystone（订阅只读，条 45）。
//
// 双形态（B365），一次性与 follow 共享同一条读循环（EventsFromAsc 全流轮询），
// 分叉点只在「首个命中后是否退出」：
//   - 一次性（缺省）：首个命中输出一行 SessionWake 后退出 0（条 46）；--timeout
//     是等待总时长，到点 124；
//   - --follow：每个命中各自输出一行后重置空闲计时、继续推进游标等下一条，
//     不主动退出——SIGINT/SIGTERM 退出 0；--timeout 语义变为空闲上限（任意
//     两命中帧之间的最大间隔，0 = 不设限），到点 124（与 runCardWait 的
//     follow 语义同形，card_wait.go:55-56）。
func runSessionWait(cmd *cobra.Command, member string, sinceFlag int64, sinceSet bool, timeout time.Duration, follow bool) error {
	slog.SetDefault(logx.Setup("cli", ""))
	slog.Info("session wait 入口", "member", member, "since", sinceFlag,
		"since_set", sinceSet, "timeout", timeout.String(), "follow", follow)
	if sinceSet && sinceFlag < 0 {
		return fmt.Errorf("--since 必须为非负 seq（当前 %d）", sinceFlag)
	}
	svc, st, err := openRoomService()
	if err != nil {
		slog.Error("session wait 打开会话服务失败", "member", member, "cause", err)
		return fmt.Errorf("session wait 打开会话服务: %w", err)
	}
	defer st.Close()
	cursor := sinceFlag
	if !sinceSet {
		cursor, err = st.MaxSeq()
		if err != nil {
			return fmt.Errorf("session wait 读取起点: %w", err)
		}
	}
	ctx := cmd.Context()
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
	enc := json.NewEncoder(cmd.OutOrStdout())
	ticker := time.NewTicker(sessionWaitPollInterval)
	defer ticker.Stop()
	for {
		events, err := st.EventsFromAsc(nil, cursor, 500)
		if err != nil {
			return fmt.Errorf("session wait 读取事件流: %w", err)
		}
		for _, ev := range events {
			cursor = ev.Seq // 其余事件类型推进游标、零输出（条 41）
			if ev.Type != ledger.EvRoomMessage {
				continue
			}
			var msg proto.RoomMessage
			if err := json.Unmarshal(ev.Payload, &msg); err != nil {
				return fmt.Errorf("session wait 事件 %d 载荷解码失败: %w", ev.Seq, err)
			}
			if !room.IsSessionRoom(msg.Room) {
				continue
			}
			targets, err := svc.MessageWakeTargets(msg)
			if err != nil {
				return fmt.Errorf("session wait 事件 %d 寻址判定失败: %w", ev.Seq, err)
			}
			hit := false
			for _, target := range targets {
				if target == member { // 逐字相等（条 39）
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
			wake, err := buildSessionWake(svc, ev, msg, member)
			if err != nil {
				return err
			}
			if err := enc.Encode(wake); err != nil {
				return fmt.Errorf("session wait 输出唤醒载荷: %w", err)
			}
			slog.Info("session wait 命中并输出", "member", member,
				"session", msg.Room, "seq", ev.Seq, "follow", follow)
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

// buildSessionWake 装配三件套（spec §4.3：命中条 + 引用条 + 未读数）。
// referenced 取 ReplyTo 指向消息的引用条；History 默认窗（200 条）外或解码
// 失败按「无引用锚」省键（plan 裁定 5 的降级声明）。unread 与
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
	if msg.ReplyTo > 0 {
		history, err := svc.History(msg.Room, 0, 0)
		if err != nil {
			return proto.SessionWake{}, fmt.Errorf("session wait 读取引用条: %w", err)
		}
		for _, h := range history {
			if h.Seq != msg.ReplyTo {
				continue
			}
			var ref proto.RoomMessage
			if json.Unmarshal(h.Payload, &ref) == nil {
				wake.Referenced = &proto.SessionCite{
					Seq: h.Seq, Room: msg.Room, Actor: h.Actor, Body: ref.Body,
				}
			}
			break
		}
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
	Short: "会话发言（--agent 出示主 agent 身份；--cli/--session 成对出示协调者席位；无 flag 用人尺度 actor）",
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
			Refs: sessionSendRefs, Mentions: sessionSendMention,
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
	sessionWaitCmd.Flags().Int64Var(&sessionWaitSince, "since", -1, "订阅起点（账本 seq，排他）；缺省 = 启动时流尾，只等新事件")
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
	sessionSendCmd.Flags().StringArrayVar(&sessionSendMention, "mention", nil, "@成员或卡号（可重复；寻址唤醒的来源）")
	sessionSendCmd.Flags().Int64Var(&sessionSendReplyTo, "reply-to", 0, "被回复消息的账本 seq（回复锚；隐式寻址原作者；0 = 无）")
	sessionSendCmd.Flags().StringVar(&sessionSendCLI, "cli", "", "手填当前会话物种名（需与 --session 成对）")
	sessionSendCmd.Flags().StringVar(&sessionSendSession, "session", "", "手填当前会话 id（需与 --cli 成对）")
	sessionSendCmd.Flags().StringVar(&sessionSendAgent, "agent", "", "以主 agent 启动身份发言（编码为 agent:<启动身份>；与 --cli/--session 互斥）")
	sessionCmd.AddCommand(sessionCreateCmd, sessionListCmd, sessionDetailCmd, sessionArchiveCmd, sessionJoinCmd, sessionLeaveCmd, sessionSendCmd)
	rootCmd.AddCommand(sessionCmd)
}
