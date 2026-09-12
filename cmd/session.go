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
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
)

// sessionWaitPollInterval 全流轮询节奏；与 card wait 的 Follow tick 同量级。
const sessionWaitPollInterval = 2 * time.Second

var sessionWaitCmd = &cobra.Command{
	Use:   "wait <member>",
	Short: "阻塞订阅会话寻址：首个命中我的消息输出一行 SessionWake JSON 后退出 0",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if sessionWaitTimeout < 0 {
			return fmt.Errorf("--timeout 必须为正时长（当前 %s）；不设上限请省略该参数", sessionWaitTimeout)
		}
		return runSessionWait(cmd, args[0], sessionWaitSince, cmd.Flags().Changed("since"), sessionWaitTimeout)
	},
}

// runSessionWait 是 R1 通道本体（契约条 39–46）。member 与寻址 target 逐字
// 相等（不透明串，不做大小写/前后缀归一）；--since 缺省 = 启动时流尾（排他），
// 只等新事件；只对会话房间的 EvRoomMessage 做命中判定，其余事件推进游标、
// 零输出；不落账、不推进未读游标、不经 keystone（订阅只读，条 45）。
func runSessionWait(cmd *cobra.Command, member string, sinceFlag int64, sinceSet bool, timeout time.Duration) error {
	slog.SetDefault(logx.Setup("cli", ""))
	slog.Info("session wait 入口", "member", member, "since", sinceFlag,
		"since_set", sinceSet, "timeout", timeout.String())
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
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
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
				"session", msg.Room, "seq", ev.Seq)
			return nil // 一次性原语：首个命中输出后退出 0（条 46）
		}
		select {
		case <-ctx.Done():
			slog.Info("session wait 超时退出", "member", member, "timeout", timeout.String())
			return &exitCodeError{code: ExitTimeout, err: fmt.Errorf("session wait 超时")}
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

func init() {
	sessionWaitCmd.Flags().Int64Var(&sessionWaitSince, "since", -1, "订阅起点（账本 seq，排他）；缺省 = 启动时流尾，只等新事件")
	sessionWaitCmd.Flags().DurationVar(&sessionWaitTimeout, "timeout", 0, "等待总时长；到点以 124 退出；0 = 不限")
	sessionCmd.AddCommand(sessionWaitCmd)
	rootCmd.AddCommand(sessionCmd)
}
