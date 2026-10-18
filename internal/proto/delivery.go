// delivery.go —— B435 收件地址与匹配结果的 wire 词表（契约 docs/superpowers/specs/b435-contract.md ②）。
//
// 职责：定义「一条会话房间消息送到谁」的地址形状、持久游标键编码、无收件人原因
// 词表与人类作者判据。判定本体（匹配函数、默认收件人）住在 internal/collab，
// 本文件只放两侧（collab 生产、agentd/CLI 消费）共用的类型与字面量。
//
// 边界：地址只有两种——卡席位与房间驻场者；外部身份（未驻场的 user:/agent:）
// 不是地址。本文件不读账本、不解析 mentions。
package proto

import (
	"strings"
	"unicode"
)

// DeliveryAddressKind 是收件地址种类。
type DeliveryAddressKind string

const (
	// DeliverySeat 卡席位地址：Card 必填，Identity = 该卡当前 driver_session（cli:<cli>#<sid>）。
	DeliverySeat DeliveryAddressKind = "seat"
	// DeliveryResident 房间驻场者地址：Card 为空，Identity = 驻场者成员身份（user:/agent:）。
	DeliveryResident DeliveryAddressKind = "resident"
)

// DeliveryAddress 是匹配函数的地址：一张卡的席位，或一个房间的驻场者。
type DeliveryAddress struct {
	Kind     DeliveryAddressKind `json:"kind"`
	Session  string              `json:"session"`
	Card     string              `json:"card,omitempty"`
	Identity string              `json:"identity"`
}

// cursorKeyResidentPrefix 是驻场者地址游标键的前缀（session_delivery_cursors.member 列值）。
const cursorKeyResidentPrefix = "resident:"

// DeliveryCursorKey 返回该地址在持久交付游标（session_delivery_cursors.member 列）里的键：
//   - 席位：Identity 原样（cli:<cli>#<sid>）——与 B435 前 `session wait <席位身份>` 写下的
//     旧行同键，语义同为「该席位已成功输出的最大命中 seq」，升级后可直接续用；
//   - 驻场者："resident:" + Session + "/" + Identity——会话 id（session:<n>）不含 "/"，
//     首个 "/" 即分界；与 B435 前按 user:/agent: 成员身份写下的旧行不同键（旧行作废不迁移）。
//
// 种类未知或必填段为空返回空串（调用方必须拒绝空键，不得写库）。
// 包级函数而非方法：proto 词表包的地址类型不另立方法容器。
func DeliveryCursorKey(a DeliveryAddress) string {
	switch a.Kind {
	case DeliverySeat:
		if a.Card == "" || a.Identity == "" {
			return ""
		}
		return a.Identity
	case DeliveryResident:
		if a.Session == "" || a.Identity == "" {
			return ""
		}
		return cursorKeyResidentPrefix + a.Session + "/" + a.Identity
	default:
		return ""
	}
}

// NoRecipientReason 是消息收件地址集为空时的原因词表（详情页文案的数据源）。
type NoRecipientReason string

const (
	// NoRecipientStructural 系统结构行（by_system / kind=pointer / by_system:null）：恒不唤醒。
	NoRecipientStructural NoRecipientReason = "structural"
	// NoRecipientNotSessionRoom 非会话房间（旧卡房间 / project: / global）：只读归档，不投递。
	NoRecipientNotSessionRoom NoRecipientReason = "not_session_room"
	// NoRecipientNonHumanUnaddressed 非人类作者且无寻址：不唤醒（反扇出）。
	NoRecipientNonHumanUnaddressed NoRecipientReason = "non_human_unaddressed"
	// NoRecipientAddressedUnresolved 有寻址但解析不出任何地址（空座卡、非本会话卡、未驻场身份）。
	// 详情页标「无收件人（@ 指定）」。
	NoRecipientAddressedUnresolved NoRecipientReason = "addressed_unresolved"
	// NoRecipientRootNoSeat 人类无寻址、无驻场者、会话根卡恰一张但该卡空座。详情页标「无收件人（@ 指定）」。
	NoRecipientRootNoSeat NoRecipientReason = "root_no_seat"
	// NoRecipientNeedsMention 人类无寻址、无驻场者、会话根卡零张或多张。详情页标「需 @」。
	NoRecipientNeedsMention NoRecipientReason = "needs_mention"
)

// Delivery 是匹配函数对一条消息的解析结果。
//
// Default=false：Addresses 来自显式寻址（@ / reply_to），人数 = 发送者写下的人数（B358 条 20–25）。
// Default=true：Addresses 恰一项，来自默认收件人分支（人类作者、无寻址）。
// Addresses 为空时 NoRecipient 非空。
type Delivery struct {
	Addresses   []DeliveryAddress
	Default     bool
	NoRecipient NoRecipientReason
}

// IsHumanAuthor 报告 actor 是否为人类作者（B435 spec r3 R3 裁决）：
//   - 统一记法 user:<name>（ParseMemberIdentity 成功且种类为 user）；或
//   - CLI 人形 actor cli:<user>@<host>（cmd/ledgercli.go#ledgerActor 的形状）：
//     cli: 前缀、余部恰一个 '@' 且两侧非空、不含 '#' 与空白。
//
// '#' 是席位分隔符（seat.go:39-40），故席位 cli:<cli>#<sid> 恒不是人类；
// agent:*（含系统发信身份 agent:handoff）、web:<host> 也不是。
func IsHumanAuthor(actor string) bool {
	if kind, _, err := ParseMemberIdentity(actor); err == nil {
		return kind == IdentityKindUser
	}
	return isCLIHumanActor(actor)
}

// isCLIHumanActor 判 cli:<user>@<host> 形状；与 ParseSeatIdentity 互斥（含 '#' 即拒）。
func isCLIHumanActor(actor string) bool {
	rest, ok := strings.CutPrefix(actor, "cli:")
	if !ok || strings.ContainsRune(rest, '#') || strings.IndexFunc(rest, unicode.IsSpace) >= 0 {
		return false
	}
	user, host, ok := strings.Cut(rest, "@")
	return ok && user != "" && host != "" && !strings.ContainsRune(host, '@')
}

// RoomHistoryItem 是房间历史视图（GET /api/rooms/{id}/messages 的 messages[]）的一行
// （B435 ⑤ 投递投影）：账本事件原样平铺 + 可省略的 no_recipient。仅当该行是会话房间
// room_message 且经 collab.Service.Recipients 解析为空集时出键，取值 = 该次的
// NoRecipientReason；有收件人、非 room_message 或判定读失败时不出键。
type RoomHistoryItem struct {
	LedgerEvent
	NoRecipient NoRecipientReason `json:"no_recipient,omitempty"`
}

// SystemNotifierIdentity 是 agentd 向项目房间发通知时的成员身份（B435 D8）：
// 以它为作者 Send 普通寻址消息（by_system=false，Mentions=[驻场者身份]）。
const SystemNotifierIdentity = "agent:handoff"
