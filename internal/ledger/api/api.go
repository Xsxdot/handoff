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
	return cardWire(card), nil
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
		out = append(out, cardWire(v.Card))
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
	// expect 语义与 RebindDriver 的 CAS 前值一致；空 expect=要求当前无绑定，
	// 该分派随实现节点落地（欠账 #4），现阶段直通镜像只接显式换绑路径。
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
// 投影同形；新增字段两处同步。
func cardWire(c ledger.Card) proto.Card {
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
