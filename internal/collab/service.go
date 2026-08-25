// Package collab 是协作房间域（B156.2）的入站门面：房间消息播发、白名单
// 与书写者执法、@定向消费队列与注意力面读模型的唯一入口。外界（gateway、
// CLI）只 import 本包与 proto DTO，不得深入内部；对外部账本的能力需求经
// client.LedgerClient 接口由组装点绑定（双向门面，架构法第四条第 3 档）。
//
// 与 d_sessions（终端 PTY 回放域）同名不同物：本包管协作房间，不管 PTY。
package collab

import (
	"encoding/json"
	"errors"

	"github.com/Xsxdot/handoff/internal/collab/client"
	"github.com/Xsxdot/handoff/internal/proto"
)

// 错误哨兵。调用方（CLI/agentd）按哨兵翻译为退出码或 HTTP 状态：
// ErrNoRoom→400/404、ErrKindNotAllowed→400、ErrNotWriter→403、
// ErrReadOnly/ErrNotBound→409。
var (
	ErrNoRoom         = errors.New("collab: 房间不存在")
	ErrKindNotAllowed = errors.New("collab: 消息形态不在白名单")
	ErrReadOnly       = errors.New("collab: 房间已只读（并入冻结或终态归档）")
	ErrNotWriter      = errors.New("collab: 书写者与房间身份不符")
	ErrNotBound       = errors.New("collab: 卡无绑定席位")
)

// roomKind 房间三类（RoomSummary.Kind 词表）。
const (
	roomCard    = "card"
	roomProject = "project"
	roomGlobal  = "global"
)

// historyDefaultLimit History 未给 limit 时的取数上限。
const historyDefaultLimit = 200

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
// 执法矩阵（契约 §4 冻结清单逐条对应）：kind 词表校验 → 房间解析 → 只读
// 拒写 → 按 kind 分派的书写者校验 → 经 client 发布。user 类发送在本提交已
// 随直通竖切真实接线；其余分支的实现归实现节点（交棒欠账 #1），当前对未
// 接线分支直接落 ErrKindNotAllowed 占位，不得被当作执法通过。
func (s *Service) Send(roomID string, msg proto.RoomMessage, actor string) (int64, error) {
	if !kindAllowed(msg.Kind) || msg.Kind == proto.RoomMsgPointer {
		return 0, ErrKindNotAllowed
	}
	cardID, readOnly, err := s.resolveRoom(roomID)
	if err != nil {
		return 0, err
	}
	if readOnly {
		return 0, ErrReadOnly
	}
	switch msg.Kind {
	case proto.RoomMsgUser:
		// 用户发言：actor 必须非空（用户不是协调者；与绑定值的比对随欠账 #1 落地）。
		if actor == "" {
			return 0, ErrNotWriter
		}
	default:
		// 协调者类/relay 的绑定比对矩阵随欠账 #1 落地；竖切阶段不伪造放行。
		return 0, ErrKindNotAllowed
	}
	msg.Room = roomID
	return s.lc.RecordRoomMessage(cardID, msg, actor)
}

// Pointer 系统组件写指针行的专用入口；HTTP/CLI 面不得路由它（冻结清单有
// 对应断言）。Ticket 0 空壳：无可观测行为。
func (s *Service) Pointer(roomID string, msg proto.RoomMessage) (int64, error) {
	_ = roomID
	_ = msg
	return 0, nil
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
		if ev.Type != protoRoomEventType || !sameRoom(ev, roomID) {
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
		if ev.Type != protoRoomEventType || !mentionsMember(ev, member) {
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

// resolveRoom 把房间标识解析为 (cardID, readOnly)。卡房间要求卡存在；
// 群房间恒可写（本期群房间无终态）。终态/并入判定随欠账 #1 补全，
// 竖切阶段只做存在性。
func (s *Service) resolveRoom(roomID string) (cardID string, readOnly bool, err error) {
	if isGroupRoom(roomID) {
		return "", false, nil
	}
	card, err := s.lc.GetCard(roomID)
	if err != nil {
		return "", false, ErrNoRoom
	}
	readOnly = card.Status == "已完成" || card.Status == "终止"
	return card.ID, readOnly, nil
}

// kindAllowed kind 是否在受控词表内。
func kindAllowed(kind string) bool {
	switch kind {
	case proto.RoomMsgEscalation, proto.RoomMsgDeviation, proto.RoomMsgClosing,
		proto.RoomMsgRelay, proto.RoomMsgReply, proto.RoomMsgUser, proto.RoomMsgPointer:
		return true
	}
	return false
}

// isGroupRoom 群房间标识形如 project:<name> 或 global。
func isGroupRoom(roomID string) bool {
	const prefix = "project:"
	return roomID == "global" || len(roomID) > len(prefix) && roomID[:len(prefix)] == prefix
}

// protoRoomEventType 是账本流上房间消息的事件类型字面量。取值必须等于
// ledger.EvRoomMessage；因门面禁令本包不能 import ledger，等式由金样本测试
// （internal/proto/rooms_fixture_test.go）钉住。
const protoRoomEventType = "room_message"

// sameRoom 判断事件是否属于该房间：卡房间的 card_id 即房间号；群级事件看
// 载荷 Room 字段。
func sameRoom(ev proto.LedgerEvent, roomID string) bool {
	if isGroupRoom(roomID) {
		var msg proto.RoomMessage
		if err := unmarshalRoomMessage(ev.Payload, &msg); err != nil {
			return false
		}
		return msg.Room == roomID
	}
	return ev.CardID == roomID
}

// mentionsMember 判断事件的 mentions 是否含该成员。
func mentionsMember(ev proto.LedgerEvent, member string) bool {
	var msg proto.RoomMessage
	if err := unmarshalRoomMessage(ev.Payload, &msg); err != nil {
		return false
	}
	for _, m := range msg.Mentions {
		if m == member {
			return true
		}
	}
	return false
}

// unmarshalRoomMessage 解码房间消息载荷；未知键不报错（encoding/json 默认）。
func unmarshalRoomMessage(raw json.RawMessage, msg *proto.RoomMessage) error {
	return json.Unmarshal(raw, msg)
}
