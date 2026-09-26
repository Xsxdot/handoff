// 会话（群）域入站门面（B358）：会话生命周期、成员与状态投影、详情页三块，
// 以及投递寻址的唯一判定入口 WakeTargets。
//
// 锚点翻转：会话 = 工作单元（人 / 主 agent 开的一场会话），卡是会话里的工作项；
// 一卡同时只挂一个会话。成员不落表——由会话的显式成员（人与主 agent 外部会话
// 身份）+ 会话内各卡的当前席位派生。席位权威仍在 cards.driver_session。
//
// 投递寻址化（本卡核心）：一条消息的接收人由发送者写下的寻址决定，与「群里
// 有谁」无关；系统永不按成员集合投递。WakeTargets 是唯一判定入口，纯寻址规则
// 在 room.ResolveDelivery（签名里没有成员集合，扇出形状写不出来）。
package collab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/collab/client"
	"github.com/Xsxdot/handoff/internal/collab/room"
	"github.com/Xsxdot/handoff/internal/proto"
)

// CreateSession 建一场会话（群）。owner = 群主（人或主 agent 会话身份），
// 群主是升级终点。
func (s *Service) CreateSession(title, owner, actor string) (proto.Session, error) {
	return s.lc.CreateSession(title, owner, actor)
}

// ListSessions 列全部会话（含归档），member 非空时投影该成员未读数。
func (s *Service) ListSessions(member string) ([]proto.SessionSummary, error) {
	return s.ListSessionsContext(context.Background(), member)
}

// ListSessionsContext 用于 HTTP 请求，事件流读取随客户端取消而停止。
func (s *Service) ListSessionsContext(ctx context.Context, member string) ([]proto.SessionSummary, error) {
	sessions, err := s.lc.ListSessions()
	if err != nil {
		return nil, err
	}
	cards, err := s.lc.ListAllCards("")
	if err != nil {
		return nil, err
	}
	byCard := make(map[string]proto.Card, len(cards))
	for _, c := range cards {
		byCard[c.ID] = c
	}
	cardIDs := make([]string, 0)
	roomWatermarks := make(map[string]int64, len(sessions))
	for _, session := range sessions {
		cardIDs = append(cardIDs, session.Cards...)
		roomWatermarks[session.ID] = 0
	}
	if member != "" {
		cursors, err := s.cursor.Snapshot(member)
		if err != nil {
			log().Warn("会话列表组装失败：读游标快照", "member", member, "cause", err)
			return nil, err
		}
		for sessionID := range roomWatermarks {
			roomWatermarks[sessionID] = cursors[sessionID]
		}
	}
	needsEvents, err := s.lc.LatestNeedsEventsContext(ctx, cardIDs)
	if err != nil {
		log().Warn("会话列表组装失败：读成员卡 needs 状态", "sessions", len(sessions), "cards", len(cardIDs), "cause", err)
		return nil, err
	}
	needs := latestNeedsByCard(needsEvents)
	roomSnapshots, err := s.lc.RoomMessageSnapshotsContext(ctx, roomWatermarks)
	if err != nil {
		log().Warn("会话列表组装失败：读房间活动摘要", "sessions", len(sessions), "cause", err)
		return nil, err
	}
	roomByID := make(map[string]client.RoomMessageSnapshot, len(roomSnapshots))
	for _, snapshot := range roomSnapshots {
		roomByID[snapshot.RoomID] = snapshot
	}
	out := make([]proto.SessionSummary, 0, len(sessions))
	for _, session := range sessions {
		snapshot, hasSnapshot := roomByID[session.ID]
		var lastMessage *client.RoomMessageSnapshot
		if hasSnapshot {
			lastMessage = &snapshot
		}
		summary := s.summarizeSession(session, byCard, lastMessage, needs)
		if member != "" {
			summary.Unread = int(snapshot.MessagesAfter)
		}
		out = append(out, summary)
	}
	return out, nil
}

// SessionDetail 读会话详情页投影：成员与成员状态、协调者派发的任务节点、
// 会话 timeline。结构信息不进群聊流。
func (s *Service) SessionDetail(id string) (proto.SessionDetail, error) {
	return s.SessionDetailContext(context.Background(), id)
}

// SessionDetailContext 用于 HTTP 请求，取消详情投影的长事件流扫描。
func (s *Service) SessionDetailContext(ctx context.Context, id string) (proto.SessionDetail, error) {
	session, err := s.lc.GetSession(id)
	if err != nil {
		return proto.SessionDetail{}, mapSessionError(err)
	}
	byCard, err := s.sessionCardsByID(ctx, session.Cards)
	if err != nil {
		return proto.SessionDetail{}, err
	}
	events, err := s.lc.SessionProjectionEventsContext(ctx, session.ID, session.Cards)
	if err != nil {
		log().Warn("会话详情组装失败：读限域投影事件", "session", session.ID, "cards", len(session.Cards), "cause", err)
		return proto.SessionDetail{}, err
	}
	needs := needsHumanByCard(events)
	roomSnapshots, err := s.lc.RoomMessageSnapshotsContext(ctx, map[string]int64{session.ID: 0})
	if err != nil {
		log().Warn("会话详情组装失败：读房间活动摘要", "session", session.ID, "cause", err)
		return proto.SessionDetail{}, err
	}
	var latestMessage *client.RoomMessageSnapshot
	if len(roomSnapshots) > 0 {
		latestMessage = &roomSnapshots[0]
	}
	detail := proto.SessionDetail{
		Summary:  s.summarizeSession(session, byCard, latestMessage, needs),
		Nodes:    sessionNodes(session, byCard, events),
		Timeline: sessionTimeline(events, session),
	}
	return detail, nil
}

// ArchiveSession 显式归档会话；归档后只读。卡的终态不等于会话结束。
func (s *Service) ArchiveSession(id, actor string) error {
	return mapSessionError(s.lc.ArchiveSession(id, actor))
}

// JoinCard 把卡拉进会话（只建讨论面；进群 ≠ 配人，配人仍走 B307 三按钮）。
func (s *Service) JoinCard(sessionID, cardID, actor string) error {
	return mapSessionError(s.lc.JoinCardToSession(sessionID, cardID, actor))
}

// LeaveCard 把卡移出会话；幂等。
func (s *Service) LeaveCard(sessionID, cardID, actor string) error {
	return mapSessionError(s.lc.LeaveCardToSession(sessionID, cardID, actor))
}

// WakeTargets 解析一条 room_message 的寻址，返回被唤醒成员身份集；空集表示
// 不唤醒任何人。规则：
//   - @卡号 解析为该卡当前席位（换绑后指向新席位）；解析不到卡时按外部会话
//     身份原样使用；
//   - reply_to 指向被回复消息的作者（隐式寻址原作者，一人）；作者若是某卡当前
//     席位则保持该席位身份（换绑后再次 @ 该卡仍命中新席位）；
//   - 系统结构行（by_system / pointer）恒不唤醒。
func (s *Service) WakeTargets(ev proto.LedgerEvent) ([]string, error) {
	var msg proto.RoomMessage
	if err := room.UnmarshalMessage(ev.Payload, &msg); err != nil {
		return nil, err
	}
	return s.MessageWakeTargets(msg)
}

// MessageWakeTargets 是 WakeTargets 的消息级半边（已在手解析好 replyAuthor 的
// 调用方用它）。唤醒路径只认它——签名里没有成员集合，扇出形状写不出来。
func (s *Service) MessageWakeTargets(msg proto.RoomMessage) ([]string, error) {
	if msg.BySystem || msg.Kind == proto.RoomMsgPointer {
		return nil, nil
	}
	replyAuthor, err := s.replyAuthorOf(msg)
	if err != nil {
		return nil, err
	}
	return s.resolveMessageTargets(msg, replyAuthor)
}

// resolveMessageTargets 解析消息寻址；@卡号 解析到该卡当前席位。
// 查询错误必须上抛：补拉扫描若误判为未命中，后续命中的持久水位会越过该消息。
func (s *Service) resolveMessageTargets(msg proto.RoomMessage, replyAuthor string) ([]string, error) {
	cards := make(map[string]proto.Card, len(msg.Mentions))
	for _, mention := range msg.Mentions {
		mention = strings.TrimSpace(mention)
		if mention == "" {
			continue
		}
		card, err := s.lc.GetCard(mention)
		if errors.Is(err, client.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("解析会话寻址 %q: %w", mention, err)
		}
		cards[mention] = card
	}
	resolveSeat := func(mention string) (string, bool) {
		card, ok := cards[mention]
		return card.DriverSession, ok
	}
	return room.ResolveDelivery(msg, replyAuthor, resolveSeat), nil
}

// AddressesCard 判断一条消息的寻址是否命中该卡当前席位（唤醒路径的必要条件）。
// 装配缺失（无账本）或卡无席位时返回 false——宁缺毋滥，杜绝广播回潮。
func (s *Service) AddressesCard(msg proto.RoomMessage, cardID string) bool {
	if cardID == "" {
		return false
	}
	card, err := s.lc.GetCard(cardID)
	if err != nil || card.DriverSession == "" {
		return false
	}
	targets, err := s.MessageWakeTargets(msg)
	if err != nil {
		return false
	}
	for _, target := range targets {
		if target == card.DriverSession {
			return true
		}
	}
	return false
}

// replyEventOf 按全局 seq 点读引用条；不存在、跨房间或非消息都不是有效回复。
// 单行升序读避免恢复积压时每条 reply 都重扫完整账本。
func (s *Service) replyEventOf(msg proto.RoomMessage) (*proto.LedgerEvent, error) {
	if msg.ReplyTo <= 0 || msg.Room == "" {
		return nil, nil
	}
	events, err := s.lc.EventsFromAsc(nil, msg.ReplyTo-1, 1)
	if err != nil {
		return nil, fmt.Errorf("读取回复事件 %d: %w", msg.ReplyTo, err)
	}
	if len(events) == 0 || events[0].Seq != msg.ReplyTo || events[0].Type != room.RoomEventType || !room.SameRoom(events[0], msg.Room) {
		return nil, nil
	}
	return &events[0], nil
}

// MessageReference 取回复引用摘要；旧消息不受 History 默认 200 条窗口限制。
func (s *Service) MessageReference(msg proto.RoomMessage) (*proto.SessionCite, error) {
	event, err := s.replyEventOf(msg)
	if err != nil || event == nil {
		return nil, err
	}
	var ref proto.RoomMessage
	if err := room.UnmarshalMessage(event.Payload, &ref); err != nil {
		return nil, fmt.Errorf("解码回复事件 %d: %w", event.Seq, err)
	}
	return &proto.SessionCite{Seq: event.Seq, Room: msg.Room, Actor: event.Actor, Body: ref.Body}, nil
}

// replyAuthorOf 找同会话被回复消息的作者；找不到返回空串。
func (s *Service) replyAuthorOf(msg proto.RoomMessage) (string, error) {
	event, err := s.replyEventOf(msg)
	if err != nil || event == nil {
		return "", err
	}
	return event.Actor, nil
}

// summarizeSession 组装会话列表/详情头部的会话摘要。
func (s *Service) summarizeSession(session proto.Session, byCard map[string]proto.Card,
	latestMessage *client.RoomMessageSnapshot, needs map[string]bool) proto.SessionSummary {
	summary := proto.SessionSummary{
		ID: session.ID, Kind: proto.SessionKind, Title: session.Title,
		Owner: session.Owner, Archived: session.Archived,
		LastActivity: session.UpdatedAt,
		Members:      s.sessionMembers(session, byCard),
		Cards:        sessionCards(session, byCard),
	}
	for _, cardID := range session.Cards {
		if needs[cardID] {
			summary.NeedsHuman = true
			break
		}
	}
	if latestMessage != nil {
		if latestMessage.LastActivity.After(summary.LastActivity) {
			summary.LastActivity = latestMessage.LastActivity
		}
		var msg proto.RoomMessage
		if err := room.UnmarshalMessage(latestMessage.Latest.Payload, &msg); err != nil {
			log().Warn("会话摘要预览投影跳过：消息载荷无效", "session", session.ID,
				"seq", latestMessage.Latest.Seq, "cause", err)
		} else {
			summary.Preview = &proto.RoomPreview{
				Body: truncateRoomPreview(msg.Body), Seq: latestMessage.Latest.Seq, CreatedAt: latestMessage.Latest.CreatedAt,
			}
		}
	}
	return summary
}

func (s *Service) sessionCardsByID(ctx context.Context, cardIDs []string) (map[string]proto.Card, error) {
	byCard := make(map[string]proto.Card, len(cardIDs))
	for _, cardID := range cardIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		card, err := s.lc.GetCard(cardID)
		if errors.Is(err, client.ErrNotFound) {
			continue
		}
		if err != nil {
			log().Warn("会话详情组装失败：读成员卡", "card", cardID, "cause", err)
			return nil, err
		}
		byCard[card.ID] = card
	}
	return byCard, nil
}

func latestNeedsByCard(events []proto.LedgerEvent) map[string]bool {
	needs := make(map[string]bool, len(events))
	for _, event := range events {
		needs[event.CardID] = event.Type == "needs_human"
	}
	return needs
}

// sessionMembers 派生会话成员：显式成员（人/主 agent）+ 会话内各卡的当前席位。
// 状态只报可证实的——有未过期租约报 working/listening，否则报 last_active；
// 空座报 empty。不报「在线」（外部会话随时可能被关掉）。
//
// 显式成员 kind 按统一记法前缀判定（B358.9 契约 §5 H 组条 39/40）：user:→human、
// agent:→agent。历史脏行（旧 web:/cli: 脸）不崩：kind 按最保守的 human 兜底并留痕。
func (s *Service) sessionMembers(session proto.Session, byCard map[string]proto.Card) []proto.SessionMember {
	members := make([]proto.SessionMember, 0, len(session.Members)+len(session.Cards))
	for _, identity := range session.Members {
		kind := proto.SessionMemberHuman
		parsedKind, _, err := proto.ParseMemberIdentity(identity)
		switch {
		case err != nil:
			log().Warn("会话成员身份非统一记法，kind 按 human 兜底",
				"session", session.ID, "identity", identity, "cause", err)
		case parsedKind == proto.IdentityKindAgent:
			kind = proto.SessionMemberAgent
		}
		status, lastActive := s.memberStatus(identity)
		members = append(members, proto.SessionMember{
			Identity: identity, Kind: kind, Status: status, LastActive: lastActive,
		})
	}
	for _, cardID := range session.Cards {
		card, ok := byCard[cardID]
		if !ok {
			continue
		}
		member := proto.SessionMember{
			Kind: proto.SessionMemberSeat, CardID: cardID, CardTitle: card.Title,
		}
		if card.DriverSession == "" {
			member.Status = proto.SessionMemberEmpty
		} else {
			member.Identity = card.DriverSession
			member.Status, member.LastActive = s.memberStatus(card.DriverSession)
		}
		members = append(members, member)
	}
	return members
}

// memberStatus 判定成员状态：有未过期租约报 working（活跃租约视为可续租），
// 否则按可证实性报 last_active。绝不报「在线」。
func (s *Service) memberStatus(identity string) (string, time.Time) {
	expiresAt, exists, err := s.lc.DriverLease(identity)
	if err != nil || !exists {
		return proto.SessionMemberLastActive, time.Time{}
	}
	if expiresAt.After(nowFn()) {
		return proto.SessionMemberWorking, expiresAt
	}
	return proto.SessionMemberLastActive, expiresAt
}

// sessionCards 投影会话里的工作项卡（空座卡 Seat 为空串）。
func sessionCards(session proto.Session, byCard map[string]proto.Card) []proto.SessionCard {
	out := make([]proto.SessionCard, 0, len(session.Cards))
	for _, cardID := range session.Cards {
		card, ok := byCard[cardID]
		if !ok {
			out = append(out, proto.SessionCard{CardID: cardID})
			continue
		}
		out = append(out, proto.SessionCard{
			CardID: card.ID, Title: card.Title, Status: card.Status, Seat: card.DriverSession,
		})
	}
	return out
}

// sessionNodes 从卡挂账/镜像投影协调者派发的任务节点（不新增执行域依赖）。
func sessionNodes(session proto.Session, byCard map[string]proto.Card, events []proto.LedgerEvent) []proto.SessionNode {
	out := []proto.SessionNode{}
	for _, cardID := range session.Cards {
		for _, ev := range events {
			if ev.CardID != cardID || ev.Type != "task_mirrored" {
				continue
			}
			var envelope struct {
				Node     *string `json:"node"`
				TaskType string  `json:"task_type"`
			}
			if err := json.Unmarshal(ev.Payload, &envelope); err != nil {
				log().Warn("task_mirrored 载荷不可解析，节点投影跳过该行",
					"seq", ev.Seq, "card", ev.CardID, "cause", err)
				continue
			}
			if envelope.Node == nil {
				log().Warn("task_mirrored 载荷缺 node 字段，节点投影跳过该行",
					"seq", ev.Seq, "card", ev.CardID)
				continue
			}
			out = append(out, proto.SessionNode{
				CardID: cardID, Node: *envelope.Node, State: envelope.TaskType,
			})
		}
	}
	return out
}

// structEventFields 解析结构事件载荷为键值表；解析失败留日志并返回 nil，
// 调用方跳过该行（缺陷族 2：静默跳过只许丢一行，不吞整段 timeline）。
func structEventFields(ev proto.LedgerEvent) map[string]any {
	var fields map[string]any
	if err := json.Unmarshal(ev.Payload, &fields); err != nil {
		log().Warn("会话结构事件载荷不可解析，timeline 跳过该行",
			"seq", ev.Seq, "type", ev.Type, "cause", err)
		return nil
	}
	return fields
}

// sessionTimeline 投影会话 timeline：结构事实（建群/归档/进群/移出）+
// 席位变更（换绑）。归属判据（契约 §9「仅当事件所属卡在该会话内时归入」的
// 全族应用，B358.2 修复 breakdown 发现 A）：
//   - 无卡结构事件：载荷归属键（created 是 id——与 ledger 写面
//     map{"id",...} 同形；其余是 session）等于本会话 id 才归入；
//   - 卡事件：仅当 ev.CardID ∈ 本会话当前 Cards 才归入。判定按当前成员——
//     卡移出后其历史行不再出现在本会话 timeline（契约 §9 字面）。
//
// Ticket 0 偏差：driver_takeover 曾用全量卡表判归属（任何卡的换绑落进每场
// 会话）、created 曾查错载荷键（session vs id，过滤恒空）、joined/left 曾无
// 判定——三处同族一并修复，反例见 TestSessionTimelineBelongsToOneSession。
func sessionTimeline(events []proto.LedgerEvent, session proto.Session) []proto.SessionTimelineEvent {
	inSession := make(map[string]bool, len(session.Cards))
	for _, cardID := range session.Cards {
		inSession[cardID] = true
	}
	out := []proto.SessionTimelineEvent{}
	for _, ev := range events {
		kind := ""
		cardID := ""
		detail := ""
		switch ev.Type {
		case "session_created":
			fields := structEventFields(ev)
			if fields == nil || fields["id"] != session.ID {
				continue
			}
			kind = proto.SessionEventCreated
		case "session_archived":
			fields := structEventFields(ev)
			if fields == nil || fields["session"] != session.ID {
				continue
			}
			kind = proto.SessionEventArchived
		case "session_card_joined":
			fields := structEventFields(ev)
			if fields == nil || fields["session"] != session.ID {
				continue
			}
			card, _ := fields["card"].(string)
			kind, cardID = proto.SessionEventCardJoined, card
		case "session_card_left":
			fields := structEventFields(ev)
			if fields == nil || fields["session"] != session.ID {
				continue
			}
			card, _ := fields["card"].(string)
			kind, cardID = proto.SessionEventCardLeft, card
		case "driver_takeover":
			// 席位变更：仅当事件所属卡在该会话内时归入本 timeline（契约 §9）。
			// detail 原样透传 {from,to} 载荷——可对质，不做二次解释（缺陷族 6）。
			if !inSession[ev.CardID] {
				continue
			}
			kind, cardID, detail = proto.SessionEventSeatRebound, ev.CardID, string(ev.Payload)
		case "driver_seat_bound":
			// R2（契约 §4.7 条 54）：EvDriverSeatBound 是 seat_bound 行的唯一
			// 载体（唯一定义点 internal/ledger/types.go——collab 非测试零 import
			// ledger，契约条 36，故字面量 + 注释指认）；归属判据沿用 §9「仅当
			// 事件所属卡在该会话内」。载荷 {to}、actor=席位自称，detail 原样
			// 透传（可对质，缺陷族 6）。
			if !inSession[ev.CardID] {
				continue
			}
			kind, cardID, detail = proto.SessionEventSeatBound, ev.CardID, string(ev.Payload)
		case "status_moved":
			// R3（契约 §4.8 条 56）：载体是既有 EvStatusMoved。当且仅当
			// to ∈ {已完成, 终止} 记 card_closed（字面值即 ledger.StatusDone /
			// ledger.StatusClosed；漂移由测试用 ledger 常量生产事件钉住）；
			// 普通列间转移不是结构事实。终止的 reason 随 payload 透传 Detail。
			if !inSession[ev.CardID] {
				continue
			}
			switch payloadString(ev.Payload, "to") {
			case "已完成", "终止":
				kind, cardID, detail = proto.SessionEventCardClosed, ev.CardID, string(ev.Payload)
			default:
				continue
			}
		case "needs_human":
			// 等人标记进 timeline（proto.SessionEventNeedsHuman；spec §4.3 末条
			// 「needs_human 亮起归详情页 timeline」）。归属判据同 §9；载荷
			// {reason} 原样透传 Detail。needs_cleared 不进 timeline——词表 8 值
			// 无它（补签轮逐值冻结），标签熄灭由 needsHumanByCard 承担。
			if !inSession[ev.CardID] {
				continue
			}
			kind, cardID, detail = proto.SessionEventNeedsHuman, ev.CardID, string(ev.Payload)
		default:
			// 未知 ledger 事件类型不进 timeline（缺陷族 7：不得被默认分支收编）。
			// seat_bound / card_closed / needs_human 分支已增补（Task 2/3）。
			continue
		}
		out = append(out, proto.SessionTimelineEvent{
			Seq: ev.Seq, Kind: kind, CardID: cardID, Detail: detail,
			Actor: ev.Actor, CreatedAt: ev.CreatedAt,
		})
	}
	return out
}

// needsHumanByCard 扫描全流，返回当前处于 needs_human 未清除的卡集合。
func needsHumanByCard(events []proto.LedgerEvent) map[string]bool {
	out := map[string]bool{}
	for _, ev := range events {
		switch ev.Type {
		case "needs_human":
			if ev.CardID != "" {
				out[ev.CardID] = true
			}
		case "needs_cleared":
			delete(out, ev.CardID)
		}
	}
	return out
}

// payloadString 从事件载荷取一个字符串键。
func payloadString(raw []byte, key string) string {
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return ""
	}
	value, _ := fields[key].(string)
	return value
}

// mapSessionError 把出站哨兵 client.ErrNotFound 翻译为 collab.ErrNoRoom
// （gateway 据此映射 404）。
func mapSessionError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, client.ErrNotFound) {
		return ErrNoRoom
	}
	return err
}
