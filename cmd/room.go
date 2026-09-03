// room.go 实现 handoff room 命令族：会话列表、历史读取、发言与收件箱。
//
// 边界（契约 §3.5）：list/read/send 直调 collab.Service，不经 agentd，与
// card_driver.go 同构（本进程持账本库）；inbox 是唯一例外——三源里的 ticket
// 源判据 Hub.Watchers(taskID)==0 是 agentd 进程内快照，CLI 进程里恒为 0，
// 本地重算会让所有等待工单全量误上浮（breakdown 岔口三裁决），故 inbox 走
// agentd HTTP /api/inbox（console/diff 先例）。actor 注入沿用 ledgercli.go
// 的 cli:<user>@<host> 约定（契约 §1.1，A.4），防伪造署名。
package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"text/tabwriter"

	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/ledger"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

var roomCmd = &cobra.Command{
	Use:   "room",
	Short: "协作房间（卡会话/项目群/全员群的列表、历史、发言与收件箱）",
}

// roomServiceFor 是 CLI 侧 collab 的唯一绑定点（架构法第八条：组装点之外
// 不得 new 他方具体类型）。openRoomService 与派发指针 writeDispatchPointer
// 共用它绑定 collab 服务，不在两处分别构造。
func roomServiceFor(st *ledger.Store) *collab.Service {
	return collab.New(ledgerapi.New(st))
}

// openRoomService 是 CLI 侧房间域组装点（target.json assembly 登记点语义），
// 房间命令族共用本入口；绑定收敛在 roomServiceFor。调用方负责 Close 返回的 Store。
func openRoomService() (*collab.Service, *ledger.Store, error) {
	st, err := openLedger()
	if err != nil {
		return nil, nil, err
	}
	return roomServiceFor(st), st, nil
}

var roomListProject string

var roomListCmd = &cobra.Command{
	Use:   "list",
	Short: "列会话（按最近活动降序；--project 过滤）",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		svc, st, err := openRoomService()
		if err != nil {
			return err
		}
		defer st.Close()
		rooms, err := svc.ListRooms(roomListProject)
		if err != nil {
			slog.Default().Warn("CLI 列会话失败", "project", roomListProject, "cause", err)
			return fmt.Errorf("列会话: %w", err)
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\t类型\t标题\t绑定\t活\t只读\t最近活动")
		for _, r := range rooms {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%v\t%v\t%s\n",
				r.ID, r.Kind, r.Title, r.BoundSession,
				r.Live, r.ReadOnly, r.LastActivity.Format("2006-01-02 15:04:05"))
		}
		slog.Default().Info("CLI 会话列表已输出", "project", roomListProject, "count", len(rooms))
		return w.Flush()
	},
}

var roomReadAfter int64

var roomReadCmd = &cobra.Command{
	Use:   "read <room>",
	Short: "读房间历史（stdout 每行以 #<seq> 开头；--after 排他）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		svc, st, err := openRoomService()
		if err != nil {
			return err
		}
		defer st.Close()
		events, err := svc.History(args[0], roomReadAfter, 0)
		if err != nil {
			slog.Default().Warn("CLI 读房间历史失败", "room", args[0], "after", roomReadAfter, "cause", err)
			return fmt.Errorf("读房间历史: %w", err)
		}
		for _, ev := range events {
			fmt.Fprintf(cmd.OutOrStdout(), "#%d\t%s\t%s\n", ev.Seq, ev.Actor, roomMessageBody(ev))
		}
		slog.Default().Info("CLI 房间历史已输出", "room", args[0], "count", len(events))
		return nil
	},
}

// roomMessageBody 解出事件正文；非 room_message 或解码失败返回空串。
func roomMessageBody(ev proto.LedgerEvent) string {
	var m proto.RoomMessage
	if err := json.Unmarshal(ev.Payload, &m); err != nil {
		return ""
	}
	return m.Body
}

var (
	roomSendKind    string
	roomSendRefs    []string
	roomSendMention []string
)

var roomSendCmd = &cobra.Command{
	Use:   "send <room> <text...>",
	Short: "发消息（--kind 默认 user；--ref/--mention 可重复）",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		svc, st, err := openRoomService()
		if err != nil {
			return err
		}
		defer st.Close()
		actor := ledgerActor()
		msg := proto.RoomMessage{
			Kind: roomSendKind, Body: strings.Join(args[1:], " "),
			Refs: roomSendRefs, Mentions: roomSendMention,
		}
		if msg.Kind != proto.RoomMsgUser {
			var err error
			actor, err = currentSeatIdentity()
			if err != nil {
				slog.Default().Warn("CLI 协调者房间消息身份出示失败", "room", args[0], "kind", msg.Kind, "cause", err)
				return err
			}
		}
		seq, err := svc.Send(args[0], msg, actor)
		if err != nil {
			slog.Default().Warn("CLI 房间消息发送失败", "room", args[0], "kind", msg.Kind, "actor", actor, "cause", err)
			return fmt.Errorf("发送到 %s: %w", args[0], err)
		}
		slog.Default().Info("CLI 房间消息已发送", "room", args[0], "kind", msg.Kind, "actor", actor, "seq", seq)
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"ok": true, "seq": seq})
	},
}

var roomInboxCmd = &cobra.Command{
	Use:   "inbox",
	Short: "待回复收件箱（三源聚合走 agentd HTTP /api/inbox）",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, cleanup, err := newTargetClient()
		if err != nil {
			return err
		}
		defer cleanup()
		items, err := c.Inbox(cmd.Context())
		if err != nil {
			slog.Default().Warn("CLI 收件箱失败", "cause", err)
			return fmt.Errorf("收件箱: %w", err)
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		for _, it := range items {
			if err := enc.Encode(it); err != nil {
				return err
			}
		}
		slog.Default().Info("CLI 收件箱已输出", "count", len(items))
		return nil
	},
}

func init() {
	roomListCmd.Flags().StringVar(&roomListProject, "project", "", "按项目过滤")
	roomReadCmd.Flags().Int64Var(&roomReadAfter, "after", 0, "只读 seq 大于此值的消息（排他）")
	roomSendCmd.Flags().StringVar(&roomSendKind, "kind", proto.RoomMsgUser, "消息 kind（默认 user）")
	roomSendCmd.Flags().StringArrayVar(&roomSendRefs, "ref", nil, "引用锚（git 路径/timeline 锚/卡号/附件路径，可重复）")
	roomSendCmd.Flags().StringArrayVar(&roomSendMention, "mention", nil, "@成员（可重复）")
	roomCmd.AddCommand(roomListCmd, roomReadCmd, roomSendCmd, roomInboxCmd)
	rootCmd.AddCommand(roomCmd)
}
