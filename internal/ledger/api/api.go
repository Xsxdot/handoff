// Package api 是账本域对协作房间域的薄门面（B156.2 还债路径）：包住既有
// *ledger.Store，逐方法转调并做 Event→proto.LedgerEvent 映射（先例
// ledgerEventWire，internal/agentd/ledgerapi.go:106），不含任何业务判断。
// 由组装点构造并注入 collab.New；组装点之外只认 client.LedgerClient 接口，
// 不要直接引用本包的具体类型。
//
// 本文件属直通镜像接线：转调体照抄既有同形方法的接线形态。
package api

import (
	"context"
	"errors"
	"time"

	"github.com/Xsxdot/handoff/internal/collab/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// Facade 实现 client.LedgerClient；组装点之外请只认接口。
type Facade struct {
	st *ledger.Store
}

// New 组装点调用。
func New(st *ledger.Store) *Facade {
	return &Facade{st: st}
}

var _ client.LedgerClient = (*Facade)(nil)

func (f *Facade) GetCard(id string) (proto.Card, error) {
	card, err := f.st.GetCard(id)
	if err != nil {
		return proto.Card{}, translateNotFound(err)
	}
	// Store.GetCard 返回裸 Card（单卡读不派生跟随态），包一层视图后
	// Following 恒空；并入态的取数源是 ListActiveCards/ListAllCards。
	return cardWire(ledger.CardView{Card: card}), nil
}

func (f *Facade) ListActiveCards(project string) ([]proto.Card, error) {
	views, err := f.st.ListCards(ledger.CardFilter{
		Project:         project,
		IncludeTerminal: false,
	})
	if err != nil {
		return nil, err
	}
	out := make([]proto.Card, 0, len(views))
	for _, v := range views {
		out = append(out, cardWire(v))
	}
	return out, nil
}

// ListAllCards 是 ListCards{IncludeTerminal:true} 的直通镜像（B156.2 岔口一
// 方案甲还债直通）：终态房间「沉底可列」与并入只读判定的唯一枚举源。
// Following 投影随 CardView 直通，本方法不做任何业务判断。
func (f *Facade) ListAllCards(project string) ([]proto.Card, error) {
	views, err := f.st.ListCards(ledger.CardFilter{
		Project:         project,
		IncludeTerminal: true,
	})
	if err != nil {
		return nil, err
	}
	out := make([]proto.Card, 0, len(views))
	for _, v := range views {
		out = append(out, cardWire(v))
	}
	return out, nil
}

func (f *Facade) RecordRoomMessage(cardID string, msg proto.RoomMessage, actor string) (int64, error) {
	return f.st.RecordRoomMessage(cardID, msg, actor)
}

func (f *Facade) RecordMessageConsumed(cardID string, msgSeq int64, consumer string) error {
	return f.st.RecordMessageConsumed(cardID, msgSeq, consumer)
}

func (f *Facade) EventsFromAsc(cardIDs []string, fromSeq int64, limit int) ([]proto.LedgerEvent, error) {
	return f.EventsFromAscContext(context.Background(), cardIDs, fromSeq, limit)
}

func (f *Facade) EventsFromAscContext(ctx context.Context, cardIDs []string, fromSeq int64, limit int) ([]proto.LedgerEvent, error) {
	events, err := f.st.EventsFromAscContext(ctx, cardIDs, fromSeq, limit)
	if err != nil {
		return nil, err
	}
	out := make([]proto.LedgerEvent, 0, len(events))
	for _, ev := range events {
		out = append(out, eventWire(ev))
	}
	return out, nil
}

// RoomMessagesBeforeContext 直通账本房间历史查询并投影 wire 事件。
func (f *Facade) RoomMessagesBeforeContext(ctx context.Context, roomID string, beforeSeq int64, limit int) ([]proto.LedgerEvent, error) {
	events, err := f.st.RoomMessagesBeforeContext(ctx, roomID, beforeSeq, limit)
	if err != nil {
		return nil, err
	}
	return eventWires(events), nil
}

// RoomMessageSnapshotsContext 直通账本批量房间消息摘要，并映射最新事件 wire。
func (f *Facade) RoomMessageSnapshotsContext(ctx context.Context, afterByRoom map[string]int64) ([]client.RoomMessageSnapshot, error) {
	snapshots, err := f.st.RoomMessageSnapshotsContext(ctx, afterByRoom)
	if err != nil {
		return nil, err
	}
	out := make([]client.RoomMessageSnapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		out = append(out, client.RoomMessageSnapshot{
			RoomID: snapshot.RoomID, Latest: eventWire(snapshot.Latest),
			LastActivity: snapshot.LastActivity, MessagesAfter: snapshot.MessagesAfter,
		})
	}
	return out, nil
}

// SessionProjectionEventsContext 直通单会话详情事件范围，不在 facade 解释业务。
func (f *Facade) SessionProjectionEventsContext(ctx context.Context, sessionID string, cardIDs []string) ([]proto.LedgerEvent, error) {
	events, err := f.st.SessionProjectionEventsContext(ctx, sessionID, cardIDs)
	if err != nil {
		return nil, err
	}
	return eventWires(events), nil
}

// LatestNeedsEventsContext 直通各卡最新 needs 状态事件。
func (f *Facade) LatestNeedsEventsContext(ctx context.Context, cardIDs []string) ([]proto.LedgerEvent, error) {
	events, err := f.st.LatestNeedsEventsContext(ctx, cardIDs)
	if err != nil {
		return nil, err
	}
	return eventWires(events), nil
}

// --- B409 U5 收件箱限域读直通镜像：逐方法转调 Store，不含业务判断 ---

// MentionCandidatesContext 直通账本提及候选读。
func (f *Facade) MentionCandidatesContext(ctx context.Context, member, roomID string, cardlessOnly bool, afterSeq int64, limit int) ([]proto.LedgerEvent, error) {
	events, err := f.st.MentionCandidatesContext(ctx, member, roomID, cardlessOnly, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	return eventWires(events), nil
}

// CardRoomUserMessagesContext 直通账本绑定卡用户消息读。
func (f *Facade) CardRoomUserMessagesContext(ctx context.Context, cardIDs []string, afterSeq int64, limit int) ([]proto.LedgerEvent, error) {
	events, err := f.st.CardRoomUserMessagesContext(ctx, cardIDs, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	return eventWires(events), nil
}

// ConsumedMessageSeqsContext 直通账本消费标记批量读。
func (f *Facade) ConsumedMessageSeqsContext(ctx context.Context, consumer string, afterSeq int64) ([]int64, error) {
	return f.st.ConsumedMessageSeqsContext(ctx, consumer, afterSeq)
}

// EventBySeq 直通账本 seq 点读。
func (f *Facade) EventBySeq(seq int64) (proto.LedgerEvent, bool, error) {
	event, ok, err := f.st.EventBySeq(seq)
	if err != nil || !ok {
		return proto.LedgerEvent{}, ok, err
	}
	return eventWire(event), true, nil
}

// --- B358 会话（群）域直通镜像：逐方法转调 Store，不含业务判断 ---

func (f *Facade) CreateSession(title, owner, actor string) (proto.Session, error) {
	session, err := f.st.CreateSession(title, owner, actor)
	if err != nil {
		return proto.Session{}, err
	}
	return sessionWire(session), nil
}

func (f *Facade) GetSession(id string) (proto.Session, error) {
	session, err := f.st.GetSession(id)
	if err != nil {
		return proto.Session{}, translateNotFound(err)
	}
	return sessionWire(session), nil
}

func (f *Facade) ListSessions() ([]proto.Session, error) {
	sessions, err := f.st.ListSessions()
	if err != nil {
		return nil, err
	}
	out := make([]proto.Session, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, sessionWire(s))
	}
	return out, nil
}

func (f *Facade) ArchiveSession(id, actor string) error {
	return translateNotFound(f.st.ArchiveSession(id, actor))
}

func (f *Facade) JoinCardToSession(sessionID, cardID, actor string) error {
	return translateNotFound(f.st.JoinCardToSession(sessionID, cardID, actor))
}

func (f *Facade) LeaveCardToSession(sessionID, cardID, actor string) error {
	return translateNotFound(f.st.LeaveCardToSession(sessionID, cardID, actor))
}

func (f *Facade) SessionOfCard(cardID string) (string, error) {
	return f.st.SessionOfCard(cardID)
}

// AddSessionMember 把一个显式成员记入会话（幂等）。
func (f *Facade) AddSessionMember(sessionID, identity, actor string) error {
	return translateNotFound(f.st.AddSessionMember(sessionID, identity, actor))
}

// translateNotFound 把账本 ErrNotFound 翻成使用方哨兵 client.ErrNotFound；
// 其它错误原样透传。接口归属使用方（架构法第九条），实现侧负责翻译。
func translateNotFound(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ledger.ErrNotFound) {
		return client.ErrNotFound
	}
	return err
}

// sessionWire 账本会话 → wire DTO（逐字段直通，无业务判断）。
func sessionWire(s ledger.Session) proto.Session {
	return proto.Session{
		ID:        s.ID,
		Title:     s.Title,
		Owner:     s.Owner,
		Archived:  s.Archived,
		Members:   s.Members,
		Cards:     s.Cards,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
	}
}

func (f *Facade) BindDriver(id, session, carrier, expect string) error {
	// 保留旧协作接口以兼容编译；协调者席位写入必须走 Store.BindSeat/
	// Store.RebindSeat，本旧入口不再修改席位。
	return f.st.RebindDriver(id, session, carrier, expect)
}

func (f *Facade) DriverLease(session string) (time.Time, bool, error) {
	lease, ok, err := f.st.DriverLeaseOf(session)
	if err != nil || !ok {
		return time.Time{}, ok, err
	}
	return lease.ExpiresAt, true, nil
}

// cardWire 账本卡 → wire DTO。字段与 internal/agentd/ledgerapi.go 的既有
// 投影同形；新增字段两处同步。入参收 CardView：Following 是查询期派生
// 标记、只存在于视图（types.go#CardView），裸 Card 无从投影。
func cardWire(v ledger.CardView) proto.Card {
	c := v.Card
	return proto.Card{
		ID:                 c.ID,
		Title:              c.Title,
		Status:             c.Status,
		TerminateReason:    c.TerminateReason,
		Priority:           c.Priority,
		Project:            c.Project,
		ParentID:           c.ParentID,
		WorkflowName:       c.WorkflowName,
		WorkflowVersion:    c.WorkflowVersion,
		Attachments:        attachmentsWire(c.Attachments),
		AcceptanceCriteria: c.AcceptanceCriteria,
		BaseBranch:         c.BaseBranch,
		Following:          v.Following,
		DriverSession:      c.DriverSession,
		DriverSource:       c.DriverSource,
		DriverHeartbeatAt:  c.DriverHeartbeatAt,
		CreatedAt:          c.CreatedAt,
		UpdatedAt:          c.UpdatedAt,
	}
}

func attachmentsWire(in []ledger.Attachment) []proto.Attachment {
	if len(in) == 0 {
		return nil
	}
	out := make([]proto.Attachment, 0, len(in))
	for _, a := range in {
		out = append(out, proto.Attachment{Kind: a.Kind, Path: a.Path})
	}
	return out
}

// eventWire 账本事件 → wire DTO（Source 三字段是镜像事件专用，房间消息
// 恒为零值，照抄 ledgerEventWire 全字段形状）。
func eventWire(ev ledger.Event) proto.LedgerEvent {
	return proto.LedgerEvent{
		Seq:          ev.Seq,
		CardID:       ev.CardID,
		Type:         ev.Type,
		Actor:        ev.Actor,
		Payload:      ev.Payload,
		SourceTarget: ev.SourceTarget,
		SourceTask:   ev.SourceTask,
		SourceSeq:    ev.SourceSeq,
		CreatedAt:    ev.CreatedAt,
	}
}

func eventWires(events []ledger.Event) []proto.LedgerEvent {
	out := make([]proto.LedgerEvent, 0, len(events))
	for _, event := range events {
		out = append(out, eventWire(event))
	}
	return out
}
