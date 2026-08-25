// Package room 是协作房间域的执法内核（B156.2 C4/C5 沉放结构）：房间解析、
// 只读判定与书写者执法的唯一实现处。根包 collab 只保留门面 Service 与错误
// 哨兵的再导出，规则一律沉在本子包（协调者护栏：根包只留门面）。
//
// 门面禁令与根包同款：本包生产代码只经 client.LedgerClient 与 proto 与外界
// 交互，零 import internal/ledger——执法所需的「已并入」状态（Following）与
// 终态判定全部走列表投影（ListAllCards），不与账本内部类型耦合。
//
// C5（消费与注意力读模型）与本包共用沉放结构：Pending/Consume/ListRooms/
// MarkRead/Unread 所需的房间判定与读侧过滤助手（SameRoom/MentionsMember/
// UnmarshalMessage/RoomEventType）都在本包。
package room

import (
	"encoding/json"
	"log/slog"

	"github.com/Xsxdot/handoff/internal/collab/client"
	"github.com/Xsxdot/handoff/internal/proto"
)

// log 结构化 logger（同 ledger 包 log() 先例，slog.Default()）。
func log() *slog.Logger { return slog.Default() }

// 房间三类（proto.RoomSummary.Kind 词表；C5 的列表投影共用，等值由
// service_test.go 的字面量测试钉住）。
const (
	KindCard    = "card"
	KindProject = "project"
	KindGlobal  = "global"
)

// Room 是房间解析的完整结果：识别房间形态并携带执法所需全部上下文。
// Card 为 nil 表示群房间（无卡可比）；ParentSession 只在卡房间且卡有直接父
// 时可能非空（直接父卡当前绑定者，供 relay/user 的一级父判定，拍板 5.5）。
type Room struct {
	ID            string
	CardID        string
	Kind          string
	Project       string
	ReadOnly      bool
	Card          *proto.Card
	ParentSession string
}

// Resolve 把房间标识解析为执法上下文。
//
// 卡房间（卡号）：存在性、终态、并入态与绑定来自一次 ListAllCards 全量投影
// ——GetCard 不派生 Following（契约 §11.4），并入只读判定只有列表方法送得来，
// 且终态卡也在列表内（IncludeTerminal:true）。群房间恒可写（本期群房间无
// 终态、无绑定可比），不查卡。
//
// 返回哨兵：房间不存在 → ErrNoRoom（直接返回不包裹，调用方 == / errors.Is
// 皆可）。账本读失败（ListAllCards 报错）按原样向上传播，不伪装成 ErrNoRoom。
func Resolve(lc client.LedgerClient, roomID string) (*Room, error) {
	if isGroupRoom(roomID) {
		kind, name := groupRoomKind(roomID)
		log().Info("房间解析为群房间", "room", roomID, "kind", kind, "project", name)
		return &Room{ID: roomID, Kind: kind, Project: name}, nil
	}
	cards, err := lc.ListAllCards("")
	if err != nil {
		return nil, err
	}
	byID := make(map[string]proto.Card, len(cards))
	for _, c := range cards {
		byID[c.ID] = c
	}
	c, ok := byID[roomID]
	if !ok {
		log().Warn("房间解析失败：卡不存在", "room", roomID)
		return nil, ErrNoRoom
	}
	r := &Room{
		ID:       roomID,
		CardID:   c.ID,
		Kind:     KindCard,
		Project:  c.Project,
		ReadOnly: IsTerminalStatus(c.Status) || c.Following != "",
		Card:     &c,
	}
	if c.ParentID != "" {
		if p, ok := byID[c.ParentID]; ok {
			r.ParentSession = p.DriverSession
		}
	}
	log().Info("房间解析完成", "room", roomID, "card", c.ID, "read_only", r.ReadOnly,
		"bound", c.DriverSession, "parent_session", r.ParentSession)
	return r, nil
}

// VerifyWriter 按 kind 执法书写者身份（契约 §3.3 执法规则 + §4 清单）。失败
// 统一返回 ErrNotWriter（含空 actor）。
//
// 矩阵：
//   - 协调者类（escalation/deviation/closing/reply）：actor==该卡当前绑定值；
//   - relay：actor==该卡当前绑定值，或 actor==直接父卡当前绑定值（只查一级，
//     拍板 5.5）；
//   - user：actor 非空且不等于该卡当前绑定值；卡有直接父时亦不等于直接父卡
//     绑定值（岔口十最窄读法：相关卡={该卡, 直接父}，与拍板 5.5 一级父同构）；
//   - 群房间无卡可比：仅 user 可写且要求 actor 非空。
func VerifyWriter(r *Room, kind, actor string) error {
	if actor == "" {
		log().Warn("书写者被拒：actor 为空", "room", r.ID, "kind", kind)
		return ErrNotWriter
	}
	switch kind {
	case proto.RoomMsgUser:
		if r.Card == nil {
			return nil
		}
		if actor == r.Card.DriverSession {
			log().Warn("user 被拒：actor 是房间卡绑定者", "room", r.ID,
				"card", r.CardID, "actor", actor)
			return ErrNotWriter
		}
		if r.ParentSession != "" && actor == r.ParentSession {
			log().Warn("user 被拒：actor 是直接父卡绑定者", "room", r.ID,
				"card", r.CardID, "parent_actor", actor)
			return ErrNotWriter
		}
	case proto.RoomMsgRelay:
		if r.Card == nil {
			log().Warn("relay 被拒：群房间无卡可比", "room", r.ID)
			return ErrNotWriter
		}
		if actor != r.Card.DriverSession && actor != r.ParentSession {
			log().Warn("relay 被拒：非本卡或直接父绑定者", "room", r.ID,
				"card", r.CardID, "actor", actor, "bound", r.Card.DriverSession,
				"parent_session", r.ParentSession)
			return ErrNotWriter
		}
	default:
		if r.Card == nil {
			log().Warn("协调者类被拒：群房间无绑定席位可比", "room", r.ID, "kind", kind)
			return ErrNotWriter
		}
		if actor != r.Card.DriverSession {
			log().Warn("协调者类被拒：非当前绑定者", "room", r.ID,
				"card", r.CardID, "kind", kind, "actor", actor,
				"bound", r.Card.DriverSession)
			return ErrNotWriter
		}
	}
	return nil
}

// KindAllowed kind 是否在受控词表内。
func KindAllowed(kind string) bool {
	switch kind {
	case proto.RoomMsgEscalation, proto.RoomMsgDeviation, proto.RoomMsgClosing,
		proto.RoomMsgRelay, proto.RoomMsgReply, proto.RoomMsgUser, proto.RoomMsgPointer:
		return true
	}
	return false
}

// IsTerminalStatus 卡终态判据。取值必须等于 ledger.StatusDone/StatusClosed
// （"已完成"/"终止"）；门面禁令禁止 import ledger，等式由 service_test.go
// 的字面量测试钉住。C5 的 ListRooms 只读投影同用它。
func IsTerminalStatus(status string) bool {
	return status == "已完成" || status == "终止"
}

// isGroupRoom 群房间标识形如 project:<name> 或 global。
func isGroupRoom(roomID string) bool {
	const prefix = "project:"
	return roomID == "global" || len(roomID) > len(prefix) && roomID[:len(prefix)] == prefix
}

// groupRoomKind 解析群房间形态：global 返回 (global,"")；project 群返回
// (project, 名字)。
func groupRoomKind(roomID string) (kind, name string) {
	if roomID == "global" {
		return KindGlobal, ""
	}
	const prefix = "project:"
	return KindProject, roomID[len(prefix):]
}

// RoomEventType 是账本流上房间消息的事件类型字面量。取值必须等于
// ledger.EvRoomMessage；门面禁令禁止 import ledger，等式由金样本测试
// （internal/proto/rooms_fixture_test.go / service_test.go）钉住。
const RoomEventType = "room_message"

// SameRoom 判断事件是否属于该房间：卡房间的 card_id 即房间号；群级事件看
// 载荷 Room 字段。
func SameRoom(ev proto.LedgerEvent, roomID string) bool {
	if isGroupRoom(roomID) {
		var msg proto.RoomMessage
		if err := UnmarshalMessage(ev.Payload, &msg); err != nil {
			return false
		}
		return msg.Room == roomID
	}
	return ev.CardID == roomID
}

// MentionsMember 判断事件的 mentions 是否含该成员。
func MentionsMember(ev proto.LedgerEvent, member string) bool {
	var msg proto.RoomMessage
	if err := UnmarshalMessage(ev.Payload, &msg); err != nil {
		return false
	}
	for _, m := range msg.Mentions {
		if m == member {
			return true
		}
	}
	return false
}

// UnmarshalMessage 解码房间消息载荷；未知键不报错（encoding/json 默认）。
func UnmarshalMessage(raw json.RawMessage, msg *proto.RoomMessage) error {
	return json.Unmarshal(raw, msg)
}
