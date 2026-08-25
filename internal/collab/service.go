// Package collab 是协作房间域（B156.2）的入站门面：房间消息播发、白名单
// 与书写者执法、@定向消费队列与注意力面读模型的唯一入口。外界（gateway、
// CLI）只 import 本包与 proto DTO，不得深入内部；对外部账本的能力需求经
// client.LedgerClient 接口由组装点绑定（双向门面，架构法第四条第 3 档）。
//
// 根包只保留门面：执法规则（房间解析、只读判定、书写者校验）全部沉在
// internal/collab/room 子包；错误哨兵定义在 room、由本包再导出。
//
// 与 d_sessions（终端 PTY 回放域）同名不同物：本包管协作房间，不管 PTY。
package collab

import (
	"log/slog"

	"github.com/Xsxdot/handoff/internal/collab/client"
	"github.com/Xsxdot/handoff/internal/collab/room"
	"github.com/Xsxdot/handoff/internal/proto"
)

// log 结构化 logger（同 ledger 包 log() 先例，slog.Default()）。
func log() *slog.Logger { return slog.Default() }

// 错误哨兵。调用方（CLI/agentd）按哨兵翻译为退出码或 HTTP 状态：
// ErrNoRoom→400/404、ErrKindNotAllowed→400、ErrNotWriter→403、
// ErrReadOnly/ErrNotBound→409。定义在 room 子包（执法内核产生它们），
// 本包再导出以保持 collab.ErrXxx 契约符号不变。
var (
	ErrNoRoom         = room.ErrNoRoom
	ErrKindNotAllowed = room.ErrKindNotAllowed
	ErrReadOnly       = room.ErrReadOnly
	ErrNotWriter      = room.ErrNotWriter
	ErrNotBound       = room.ErrNotBound
)

// historyDefaultLimit History 未给 limit 时的取数上限。
const historyDefaultLimit = 200

// pointerActor Pointer 写指针行时落账的 actor 标识（契约 §3.3 Pointer 无
// actor 参数、调用方身份不可得，固定系统组件标识）。
const pointerActor = "system:pointer"

// Service 协作房间域入站门面。依赖只收 client 接口。
type Service struct {
	lc client.LedgerClient
}

// New 组装点调用；lc 的具体实现是 internal/ledger/api.Facade。
func New(lc client.LedgerClient) *Service {
	return &Service{lc: lc}
}

// Send 发消息：白名单 + 书写者执法的唯一写入口（pointer 除外——它只能走
// Pointer，经本方法一律拒收）。返回落账 seq。
//
// 执法矩阵（契约 §3.3 逐条 + §4 冻结清单）：kind 词表校验（pointer 拒收）
// → 房间解析（room.Resolve：存在性 + 只读判定）→ 只读拒写 → 按 kind 的
// 书写者校验（room.VerifyWriter）→ 经 client 发布。全部规则实现在 room
// 子包，本方法只做编排。
func (s *Service) Send(roomID string, msg proto.RoomMessage, actor string) (int64, error) {
	if !room.KindAllowed(msg.Kind) || msg.Kind == proto.RoomMsgPointer {
		return 0, room.ErrKindNotAllowed
	}
	r, err := room.Resolve(s.lc, roomID)
	if err != nil {
		return 0, err
	}
	if r.ReadOnly {
		return 0, room.ErrReadOnly
	}
	if err := room.VerifyWriter(r, msg.Kind, actor); err != nil {
		return 0, err
	}
	msg.Room = roomID
	seq, err := s.lc.RecordRoomMessage(r.CardID, msg, actor)
	if err != nil {
		return 0, err
	}
	log().Info("房间消息已落账", "room", roomID, "kind", msg.Kind, "actor", actor, "seq", seq)
	return seq, nil
}

// Pointer 系统组件写指针行的专用入口；HTTP/CLI 面不得路由它（冻结清单有
// 对应断言）。kind=pointer 与 BySystem=true 由本方法自置（rooms.go 注释
// 「本键只应由 Service.Pointer 置真」），调用方只递 roomID 与正文；房间
// 解析复用 room.Resolve，并入冻结/终态归档房间拒写（ErrReadOnly）。
func (s *Service) Pointer(roomID string, msg proto.RoomMessage) (int64, error) {
	r, err := room.Resolve(s.lc, roomID)
	if err != nil {
		return 0, err
	}
	if r.ReadOnly {
		return 0, room.ErrReadOnly
	}
	msg.Kind = proto.RoomMsgPointer
	msg.BySystem = true
	msg.Room = roomID
	seq, err := s.lc.RecordRoomMessage(r.CardID, msg, pointerActor)
	if err != nil {
		return 0, err
	}
	log().Info("指针行已落账", "room", roomID, "card", r.CardID, "seq", seq)
	return seq, nil
}

// History 读房间历史：seq 游标分页（beforeSeq 排他、升序截尾），只返回
// type=room_message 的事件。limit<=0 取 historyDefaultLimit。
func (s *Service) History(roomID string, beforeSeq int64, limit int) ([]proto.LedgerEvent, error) {
	if limit <= 0 {
		limit = historyDefaultLimit
	}
	events, err := s.lc.EventsFromAsc([]string{}, beforeSeq, 0)
	if err != nil {
		return nil, err
	}
	out := make([]proto.LedgerEvent, 0, len(events))
	for _, ev := range events {
		if ev.Type != room.RoomEventType || !room.SameRoom(ev, roomID) {
			continue
		}
		out = append(out, ev)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// Pending @定向消费队列（读侧）：mentions∋consumer 且尚无本人消费标记的
// 群级消息 + 其绑定卡房间的未消费用户留言。实现随欠账 #3 补齐游标维度；
// 当前返回空切片占位，调用方不得依赖其完整性。
func (s *Service) Pending(consumer string) ([]proto.LedgerEvent, error) {
	_ = consumer
	return []proto.LedgerEvent{}, nil
}

// Consume 消费落账（恰好一次的权威在账本 message_consumed 事件）。
// Ticket 0 空壳：无可观测行为。
func (s *Service) Consume(seq int64, consumer string) error {
	_ = seq
	_ = consumer
	return nil
}

// Mentions 收件箱源③：@member 的未消费提及。
func (s *Service) Mentions(member string, afterSeq int64, limit int) ([]proto.LedgerEvent, error) {
	if limit <= 0 {
		limit = historyDefaultLimit
	}
	events, err := s.lc.EventsFromAsc([]string{}, afterSeq, 0)
	if err != nil {
		return nil, err
	}
	out := make([]proto.LedgerEvent, 0, len(events))
	for _, ev := range events {
		if ev.Type != room.RoomEventType || !room.MentionsMember(ev, member) {
			continue
		}
		out = append(out, ev)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// ListRooms 会话列表（扁平活动排序 + 项目筛选）。派生成员规则随欠账 #3
// 落地；当前返回空切片占位，调用方不得依赖其完整性。
func (s *Service) ListRooms(project string) ([]proto.RoomSummary, error) {
	_ = project
	return []proto.RoomSummary{}, nil
}

// MarkRead 未读游标置位（打开房间即已读）。Ticket 0 空壳。
func (s *Service) MarkRead(member, roomID string, uptoSeq int64) error {
	_ = member
	_ = roomID
	_ = uptoSeq
	return nil
}

// Unread 未读计数。Ticket 0 空壳：恒返回 0。
func (s *Service) Unread(member, roomID string) (int, error) {
	_ = member
	_ = roomID
	return 0, nil
}
