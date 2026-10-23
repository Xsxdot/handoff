package agentd

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// RecordTaskTicketNotice derives the current public delivery address for an
// executor permission/question event and persists its notification projection.
// The original ticket and its answer CAS remain on the source agentd.
func (s *Server) RecordTaskTicketNotice(machine, taskID, cardID, originSession string, event proto.Event) error {
	if event.Type == proto.EventTypeTicketAnswered {
		var answered struct {
			TicketID string `json:"ticket_id"`
		}
		if err := json.Unmarshal(event.Payload, &answered); err != nil {
			return fmt.Errorf("decode ticket answered event seq=%d: %w", event.Seq, err)
		}
		current, err := s.ledger.GetTaskTicketNotice(machine, taskID, answered.TicketID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read answered ticket notice %s/%s: %w", taskID, answered.TicketID, err)
		}
		if current.Status == "answered" || current.Status == "superseded" {
			return nil
		}
		return s.ledger.SetTaskTicketNoticeStatus(machine, taskID, answered.TicketID,
			current.OwnershipVersion, "answered", "ticket_answered", "")
	}
	if event.Type == proto.EventTypeTicketsVoided {
		active, err := s.ledger.ListActiveTaskTicketNoticesForTask(machine, taskID)
		if err != nil {
			return fmt.Errorf("list notices for voided task %s: %w", taskID, err)
		}
		for _, current := range active {
			if err := s.ledger.SetTaskTicketNoticeStatus(machine, taskID, current.TicketID,
				current.OwnershipVersion, "answered", "tickets_voided", ""); err != nil {
				return err
			}
		}
		return nil
	}
	var payload struct {
		TicketID string `json:"ticket_id"`
		Kind     string `json:"kind"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("decode source ticket event seq=%d: %w", event.Seq, err)
	}
	kind := payload.Kind
	if event.Type == proto.EventTypePermissionRequest {
		kind = "gate"
	} else if event.Type == proto.EventTypeQuestion {
		kind = "ask"
	}
	if payload.TicketID == "" || (kind != "gate" && kind != "ask") {
		return fmt.Errorf("source ticket event seq=%d lacks supported ticket identity", event.Seq)
	}
	var target *proto.DeliveryAddress
	reason := ""
	if cardID != "" {
		originCard, err := s.ledger.GetCard(cardID)
		if err != nil {
			return fmt.Errorf("read linked card %s for ticket notice: %w", cardID, err)
		}
		routeCard := ""
		if proto.SeatMode(originCard.DriverMode) == proto.SeatModePull && originCard.DriverSession != "" &&
			proto.ValidateSeat(originCard.DriverSession, proto.SeatSource(originCard.DriverSource)) == nil {
			routeCard = cardID
		} else {
			routeCard = s.resolveWakeCard(cardID)
		}
		if routeCard != "" {
			card, err := s.ledger.GetCard(routeCard)
			if err != nil {
				return fmt.Errorf("read notice recipient card %s: %w", routeCard, err)
			}
			session, err := s.ledger.SessionOfCard(routeCard)
			if err != nil && !errors.Is(err, ledger.ErrNotFound) {
				return fmt.Errorf("read notice recipient session for card %s: %w", routeCard, err)
			}
			// A valid card seat can exist before the card joins a session. Keep
			// its card+identity address; card_wait consumes that cursor directly.
			target = &proto.DeliveryAddress{Kind: proto.DeliverySeat, Session: session,
				Card: routeCard, Identity: card.DriverSession}
		}
	}
	if target == nil {
		if originSession == "" {
			reason = "origin_unknown"
		} else {
			resident, seated, err := s.ledger.SessionResident(originSession)
			if err != nil {
				return fmt.Errorf("read current origin resident %s: %w", originSession, err)
			}
			if seated {
				target = &proto.DeliveryAddress{Kind: proto.DeliveryResident,
					Session: originSession, Identity: resident.Identity}
			} else {
				reason = "resident_missing"
			}
		}
	}
	_, _, err := s.ledger.PutTaskTicketNotice(ledger.TaskTicketNoticeState{
		SourceMachine: machine, TaskID: taskID, TicketID: payload.TicketID,
		TicketKind: kind, SourceSeq: event.Seq, Target: target, Reason: reason,
	}, "agentd")
	if err != nil {
		return fmt.Errorf("persist task ticket notice %s/%s: %w", taskID, payload.TicketID, err)
	}
	s.log.Info("原任务工单通知路由已刷新", "source_machine", machine, "task", taskID,
		"ticket", payload.TicketID, "source_seq", event.Seq, "ticket_kind", kind,
		"target_set", target != nil, "reason", reason)
	return nil
}

// RefreshTaskTicketNotice rebuilds an active notice from current task/card/resident facts.
// It runs during mirror reconciliation so owner changes do not wait for another permission event.
func (s *Server) RefreshTaskTicketNotice(machine, taskID, cardID, originSession string, current ledger.TaskTicketNoticeState) error {
	typ := proto.EventTypeQuestion
	if current.TicketKind == "gate" {
		typ = proto.EventTypePermissionRequest
	} else if current.TicketKind != "ask" {
		return fmt.Errorf("unsupported active task ticket kind %q", current.TicketKind)
	}
	payload, err := json.Marshal(map[string]string{"ticket_id": current.TicketID, "kind": current.TicketKind})
	if err != nil {
		return fmt.Errorf("encode active task ticket identity: %w", err)
	}
	return s.RecordTaskTicketNotice(machine, taskID, cardID, originSession, proto.Event{
		Seq: current.SourceSeq, TaskID: taskID, Type: typ, Payload: payload,
	})
}
