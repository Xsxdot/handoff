// B385：会话成员「最后活跃」的账本事件推导（缝 1）与旧传输脸读侧归一（缝 2）。
//
// 边界：只读展示面——权力面（写权限、成员名单、@ 路由）仍按 proto/identity.go
// fail-closed，旧脸回落禁令不受影响。数据源是会话范围账本事件，不再读
// driver_leases（B189 后恒空死表）；working/listening 租约语义不在此产出。
package collab

import (
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/collab/room"
	"github.com/Xsxdot/handoff/internal/proto"
)

// deriveMemberLastActive 单遍扫描会话范围账本事件，产出成员/席位身份→最后
// 活跃时刻（同身份取最大 CreatedAt；缺失身份不建键，查表回零值）。
//
// 范围（沿用 timeline 归属判据 §9 同族）：会话房间消息（载荷 room == 本会话
// id）∪ 本会话各卡的账本事件（ev.CardID ∈ 本会话当前 Cards）。动作词表：一切
// 范围内事件都算可证实动作，除系统组件（agentd/mirror/system:*）——系统不是
// 成员。身份匹配：actor 与目标身份精确相等为主；否则旧脸归一后再比。
func deriveMemberLastActive(events []proto.LedgerEvent, session proto.Session,
	byCard map[string]proto.Card) map[string]time.Time {
	targets := make(map[string]bool, len(session.Members)+len(session.Cards))
	userNames := make(map[string]bool, len(session.Members))
	for _, identity := range session.Members {
		targets[identity] = true
		if name, ok := strings.CutPrefix(identity, "user:"); ok {
			userNames[name] = true
		}
	}
	inSessionCard := make(map[string]bool, len(session.Cards))
	for _, cardID := range session.Cards {
		inSessionCard[cardID] = true
		if card, ok := byCard[cardID]; ok && card.DriverSession != "" {
			targets[card.DriverSession] = true
		}
	}
	out := make(map[string]time.Time, len(targets))
	for _, ev := range events {
		if isSystemActor(ev.Actor) {
			continue
		}
		if !eventInSessionScope(ev, session.ID, inSessionCard) {
			continue
		}
		key := ev.Actor
		if !targets[key] {
			key = normalizeLegacyFace(ev.Actor, userNames)
			if !targets[key] {
				continue
			}
		}
		if at, ok := out[key]; !ok || ev.CreatedAt.After(at) {
			out[key] = ev.CreatedAt
		}
	}
	return out
}

// normalizeLegacyFace 读侧旧传输脸归一：cli:<名>@<主机> / web:<名>@<主机> 的
// 本地段与某 user:<名> 成员名精确相等时归一为 user:<名>；其余形态一律原样
// （宁窄勿宽——席位脸 cli:<cli>#<id> 无 @，自然不进归一）。
func normalizeLegacyFace(actor string, userNames map[string]bool) string {
	var rest string
	switch {
	case strings.HasPrefix(actor, "cli:"):
		rest = actor[len("cli:"):]
	case strings.HasPrefix(actor, "web:"):
		rest = actor[len("web:"):]
	default:
		return actor
	}
	name, host, ok := strings.Cut(rest, "@")
	if !ok || name == "" || host == "" {
		return actor
	}
	if userNames[name] {
		return "user:" + name
	}
	return actor
}

// isSystemActor 系统组件落账标识：agentd 本体、镜像管道、system:* 指针行等。
// 系统不是成员，其事件不算任何人的「最后活跃」。
func isSystemActor(actor string) bool {
	switch actor {
	case "", "agentd", "mirror":
		return true
	}
	return strings.HasPrefix(actor, "system:")
}

// eventInSessionScope 判定事件是否计入本会话读数：本会话卡的任意账本事件，
// 或会话房间消息（载荷 room == 本会话 id）。
func eventInSessionScope(ev proto.LedgerEvent, sessionID string, inSessionCard map[string]bool) bool {
	if ev.CardID != "" && inSessionCard[ev.CardID] {
		return true
	}
	return ev.Type == room.RoomEventType && room.SameRoom(ev, sessionID)
}
