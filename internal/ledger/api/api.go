// Package api 是账本域对协作房间域的薄门面（B156.2 还债路径）：包住既有
// *ledger.Store，逐方法转调并做 Event→proto.LedgerEvent 映射（先例
// ledgerEventWire，internal/agentd/ledgerapi.go:106），不含任何业务判断。
// 由组装点构造并注入 collab.New；本包之外不得引用。
//
// 本文件属直通镜像接线：转调体照抄既有同形方法的接线形态。
package api

import (
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
		return proto.Card{}, err
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
	events, err := f.st.EventsFromAsc(cardIDs, fromSeq, limit)
	if err != nil {
		return nil, err
	}
	out := make([]proto.LedgerEvent, 0, len(events))
	for _, ev := range events {
		out = append(out, eventWire(ev))
	}
	return out, nil
}

func (f *Facade) BindDriver(id, session, carrier, expect string) error {
	// expect 语义在账本侧 RebindDriver 落地（B156.2 欠账 #4）：expect=当前
	// 绑定前值 CAS；空 expect=要求当前无绑定。本方法保持直通镜像零业务判断。
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
		Seq:       ev.Seq,
		CardID:    ev.CardID,
		Type:      ev.Type,
		Actor:     ev.Actor,
		Payload:   ev.Payload,
		CreatedAt: ev.CreatedAt,
	}
}
