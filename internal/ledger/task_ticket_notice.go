// Package ledger persists addressed notices for original executor tickets.
// It records routing state only; ticket answers remain owned by the executor store.
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// TaskTicketNoticeState is the current routing projection for one executor ticket.
type TaskTicketNoticeState struct {
	SourceMachine    string                 `json:"source_machine"`
	TaskID           string                 `json:"task_id"`
	TicketID         string                 `json:"ticket_id"`
	TicketKind       string                 `json:"ticket_kind"`
	SourceSeq        int64                  `json:"source_seq"`
	OwnershipVersion int64                  `json:"ownership_version"`
	Target           *proto.DeliveryAddress `json:"target,omitempty"`
	Status           string                 `json:"status"`
	Reason           string                 `json:"reason,omitempty"`
	LastError        string                 `json:"last_error,omitempty"`
	RetryAfter       *time.Time             `json:"retry_after,omitempty"`
	UpdatedAt        time.Time              `json:"updated_at"`
}

// PutTaskTicketNotice atomically upserts current routing state and emits a
// separately addressed event when a target exists. A changed address advances
// ownership_version; replaying the same address is idempotent.
func (s *Store) PutTaskTicketNotice(in TaskTicketNoticeState, actor string) (TaskTicketNoticeState, bool, error) {
	if in.SourceMachine == "" || in.TaskID == "" || in.TicketID == "" || in.SourceSeq <= 0 {
		return TaskTicketNoticeState{}, false, errors.New("task ticket notice source identity is incomplete")
	}
	if in.TicketKind != "ask" && in.TicketKind != "gate" {
		return TaskTicketNoticeState{}, false, fmt.Errorf("unsupported task ticket notice kind %q", in.TicketKind)
	}
	if in.Target != nil {
		if in.Target.Kind == proto.DeliverySeat && in.Target.Card == "" || in.Target.Kind == proto.DeliveryResident && in.Target.Session == "" {
			return TaskTicketNoticeState{}, false, errors.New("task ticket notice target location is incomplete")
		}
		if err := proto.ValidateTaskTicketNotice(proto.TaskTicketNotice{SourceMachine: in.SourceMachine, TaskID: in.TaskID,
			TicketID: in.TicketID, SourceSeq: in.SourceSeq, OwnershipVersion: 1, TicketKind: in.TicketKind, Target: *in.Target}); err != nil {
			return TaskTicketNoticeState{}, false, err
		}
	}
	if actor == "" {
		actor = "agentd"
	}
	var out TaskTicketNoticeState
	changed := false
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		var oldTargetRaw sql.NullString
		var oldVersion int64
		var oldSourceSeq int64
		var oldStatus, oldKind string
		err := tx.QueryRow(s.q(`SELECT target, ownership_version, source_seq, status, ticket_kind FROM task_ticket_notices
			WHERE source_machine=? AND task_id=? AND ticket_id=?`), in.SourceMachine, in.TaskID, in.TicketID).
			Scan(&oldTargetRaw, &oldVersion, &oldSourceSeq, &oldStatus, &oldKind)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read current ticket notice routing: %w", err)
		}
		newTarget := ""
		if in.Target != nil {
			b, marshalErr := json.Marshal(in.Target)
			if marshalErr != nil {
				return fmt.Errorf("encode ticket notice target: %w", marshalErr)
			}
			newTarget = string(b)
		}
		oldTargetMatches := false
		if !errors.Is(err, sql.ErrNoRows) {
			if !oldTargetRaw.Valid {
				oldTargetMatches = in.Target == nil
			} else if in.Target != nil {
				var oldTarget proto.DeliveryAddress
				if decodeErr := json.Unmarshal([]byte(oldTargetRaw.String), &oldTarget); decodeErr != nil {
					return fmt.Errorf("decode current ticket notice target: %w", decodeErr)
				}
				oldTargetMatches = oldTarget == *in.Target
			}
		}
		if errors.Is(err, sql.ErrNoRows) {
			in.OwnershipVersion = 1
			changed = true
		} else {
			if oldStatus == "answered" || oldStatus == "superseded" {
				in.OwnershipVersion = oldVersion
				in.SourceSeq = oldSourceSeq
				in.Status = oldStatus
				in.TicketKind = oldKind
				if oldTargetRaw.Valid {
					var oldTarget proto.DeliveryAddress
					if err := json.Unmarshal([]byte(oldTargetRaw.String), &oldTarget); err != nil {
						return fmt.Errorf("decode closed ticket notice target: %w", err)
					}
					in.Target = &oldTarget
				}
				in.UpdatedAt = s.timeNow().UTC()
				out = in
				return nil
			}
			if in.SourceSeq < oldSourceSeq {
				return fmt.Errorf("stale task ticket notice source sequence %d < %d", in.SourceSeq, oldSourceSeq)
			}
			in.OwnershipVersion = oldVersion
			if !oldTargetMatches {
				in.OwnershipVersion++
				changed = true
			}
		}
		if in.Target == nil {
			if errors.Is(err, sql.ErrNoRows) || changed || oldStatus == "" {
				in.Status = "needs_human"
				if in.Reason == "" {
					in.Reason = "origin_unknown"
				}
			} else {
				in.Status = oldStatus
				if in.Reason == "" {
					in.Reason = "resident_missing"
				}
			}
		} else if errors.Is(err, sql.ErrNoRows) || changed {
			in.Status = "pending"
			in.Reason = ""
		} else {
			in.Status = oldStatus
			in.Reason = ""
		}
		in.UpdatedAt = s.timeNow().UTC()
		var targetValue any
		if newTarget != "" {
			targetValue = newTarget
		}
		var retryValue any
		if in.RetryAfter != nil {
			retryValue = s.tval(*in.RetryAfter)
		}
		_, err = tx.Exec(s.q(`INSERT INTO task_ticket_notices
			(source_machine,task_id,ticket_id,source_seq,ownership_version,ticket_kind,target,status,reason,last_error,retry_after,updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT (source_machine,task_id,ticket_id) DO UPDATE SET
			source_seq=excluded.source_seq, ownership_version=excluded.ownership_version, ticket_kind=excluded.ticket_kind, target=excluded.target,
			status=excluded.status, reason=excluded.reason, last_error=excluded.last_error,
			retry_after=excluded.retry_after, updated_at=excluded.updated_at`),
			in.SourceMachine, in.TaskID, in.TicketID, in.SourceSeq, in.OwnershipVersion, in.TicketKind,
			targetValue, in.Status, in.Reason, in.LastError, retryValue, s.tval(in.UpdatedAt))
		if err != nil {
			return fmt.Errorf("upsert ticket notice routing: %w", err)
		}
		if in.Target != nil && changed {
			wire := proto.TaskTicketNotice{SourceMachine: in.SourceMachine, TaskID: in.TaskID,
				TicketID: in.TicketID, SourceSeq: in.SourceSeq, OwnershipVersion: in.OwnershipVersion,
				TicketKind: in.TicketKind, Target: *in.Target}
			if err := json.Unmarshal([]byte(newTarget), &wire.Target); err != nil {
				return fmt.Errorf("decode committed ticket notice target: %w", err)
			}
			eventCard := ""
			if wire.Target.Kind == proto.DeliverySeat {
				eventCard = wire.Target.Card
			}
			if _, err := s.appendEvent(tx, sink, eventCard, EvTaskTicketNotice, actor, wire); err != nil {
				return err
			}
		}
		out = in
		return nil
	})
	if err != nil {
		log().Warn("task ticket notice routing update failed", "machine", in.SourceMachine, "task", in.TaskID,
			"ticket", in.TicketID, "source_seq", in.SourceSeq, "cause", err)
		return TaskTicketNoticeState{}, false, err
	}
	log().Info("task ticket notice routing persisted", "machine", out.SourceMachine, "task", out.TaskID,
		"ticket", out.TicketID, "ownership_version", out.OwnershipVersion, "status", out.Status,
		"target_set", out.Target != nil, "event_emitted", out.Target != nil && changed)
	return out, changed, nil
}

// GetTaskTicketNotice reads the latest notice routing projection.
func (s *Store) GetTaskTicketNotice(machine, taskID, ticketID string) (TaskTicketNoticeState, error) {
	var out TaskTicketNoticeState
	var rawTarget, retryAfter sql.NullString
	var updatedAt any
	err := s.db.QueryRow(s.q(`SELECT source_machine,task_id,ticket_id,source_seq,ownership_version,target,
		ticket_kind,status,reason,last_error,retry_after,updated_at FROM task_ticket_notices
		WHERE source_machine=? AND task_id=? AND ticket_id=?`), machine, taskID, ticketID).Scan(
		&out.SourceMachine, &out.TaskID, &out.TicketID, &out.SourceSeq, &out.OwnershipVersion,
		&rawTarget, &out.TicketKind, &out.Status, &out.Reason, &out.LastError, &retryAfter, &updatedAt)
	if err != nil {
		return TaskTicketNoticeState{}, err
	}
	if rawTarget.Valid {
		var target proto.DeliveryAddress
		if err := json.Unmarshal([]byte(rawTarget.String), &target); err != nil {
			return TaskTicketNoticeState{}, fmt.Errorf("decode ticket notice target: %w", err)
		}
		out.Target = &target
	}
	if retryAfter.Valid {
		parsed := toTime(retryAfter.String)
		out.RetryAfter = &parsed
	}
	out.UpdatedAt = toTime(updatedAt)
	return out, nil
}

// ListActiveTaskTicketNotices returns notices that may still need routing work.
func (s *Store) ListActiveTaskTicketNotices() ([]TaskTicketNoticeState, error) {
	rows, err := s.db.Query(`SELECT source_machine,task_id,ticket_id,source_seq,ownership_version,ticket_kind,
		target,status,reason,last_error,retry_after,updated_at FROM task_ticket_notices
		WHERE status IN ('pending','delivered','needs_human') ORDER BY updated_at`)
	if err != nil {
		return nil, fmt.Errorf("list active task ticket notices: %w", err)
	}
	defer rows.Close()
	out := []TaskTicketNoticeState{}
	for rows.Next() {
		var notice TaskTicketNoticeState
		var rawTarget, retryAfter sql.NullString
		var updatedAt any
		if err := rows.Scan(&notice.SourceMachine, &notice.TaskID, &notice.TicketID,
			&notice.SourceSeq, &notice.OwnershipVersion, &notice.TicketKind, &rawTarget,
			&notice.Status, &notice.Reason, &notice.LastError, &retryAfter, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan active task ticket notice: %w", err)
		}
		if rawTarget.Valid {
			var target proto.DeliveryAddress
			if err := json.Unmarshal([]byte(rawTarget.String), &target); err != nil {
				return nil, fmt.Errorf("decode active task ticket target: %w", err)
			}
			notice.Target = &target
		}
		if retryAfter.Valid {
			parsed := toTime(retryAfter.String)
			notice.RetryAfter = &parsed
		}
		notice.UpdatedAt = toTime(updatedAt)
		out = append(out, notice)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active task ticket notices: %w", err)
	}
	return out, nil
}

// SetTaskTicketNoticeStatus changes delivery/lifecycle state only if the caller
// still holds the current ownership version. It cannot change answer authority.
func (s *Store) SetTaskTicketNoticeStatus(machine, taskID, ticketID string, version int64, status, reason, lastError string) error {
	if status != "pending" && status != "delivered" && status != "needs_human" && status != "superseded" && status != "answered" {
		return fmt.Errorf("invalid task ticket notice status %q", status)
	}
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		res, err := tx.Exec(s.q(`UPDATE task_ticket_notices SET status=?,reason=?,last_error=?,updated_at=?
			WHERE source_machine=? AND task_id=? AND ticket_id=? AND ownership_version=?
			AND status IN ('pending','delivered','needs_human')`), status, reason, lastError, s.tval(s.timeNow().UTC()),
			machine, taskID, ticketID, version)
		if err != nil {
			return fmt.Errorf("update task ticket notice status: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("read task ticket notice status update count: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("task ticket notice ownership changed or is closed: %s/%s", taskID, ticketID)
		}
		log().Info("task ticket notice status changed", "machine", machine, "task", taskID,
			"ticket", ticketID, "ownership_version", version, "status", status)
		return nil
	})
	if err != nil {
		log().Warn("task ticket notice status update failed", "machine", machine, "task", taskID,
			"ticket", ticketID, "ownership_version", version, "status", status, "cause", err)
	}
	return err
}

// RetryTaskTicketNotice explicitly requeues a still-addressed notice after a
// user retry. It keeps ownership_version unchanged because the recipient did
// not change, and emits a fresh addressed event to wake that recipient again.
func (s *Store) RetryTaskTicketNotice(machine, taskID, ticketID string, version int64, actor string) (TaskTicketNoticeState, bool, error) {
	if actor == "" {
		actor = "user"
	}
	var out TaskTicketNoticeState
	var retried bool
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		var targetRaw sql.NullString
		var updatedAt any
		if err := tx.QueryRow(s.q(`SELECT source_machine,task_id,ticket_id,source_seq,ownership_version,target,ticket_kind,status,reason,last_error,retry_after,updated_at
			FROM task_ticket_notices WHERE source_machine=? AND task_id=? AND ticket_id=?`), machine, taskID, ticketID).Scan(
			&out.SourceMachine, &out.TaskID, &out.TicketID, &out.SourceSeq, &out.OwnershipVersion, &targetRaw,
			&out.TicketKind, &out.Status, &out.Reason, &out.LastError, new(sql.NullString), &updatedAt); err != nil {
			return fmt.Errorf("read notice for explicit retry: %w", err)
		}
		if out.OwnershipVersion != version {
			return fmt.Errorf("task ticket notice ownership changed: %s/%s", taskID, ticketID)
		}
		if targetRaw.Valid {
			var target proto.DeliveryAddress
			if err := json.Unmarshal([]byte(targetRaw.String), &target); err != nil {
				return fmt.Errorf("decode notice retry target: %w", err)
			}
			out.Target = &target
		}
		if out.Status == "answered" || out.Status == "superseded" {
			return fmt.Errorf("task ticket notice is closed: %s/%s", taskID, ticketID)
		}
		if out.Status != "needs_human" || out.Target == nil {
			return nil
		}
		now := s.timeNow().UTC()
		res, err := tx.Exec(s.q(`UPDATE task_ticket_notices SET status='pending',reason='',last_error='',retry_after=NULL,updated_at=?
			WHERE source_machine=? AND task_id=? AND ticket_id=? AND ownership_version=? AND status='needs_human'`),
			s.tval(now), machine, taskID, ticketID, version)
		if err != nil {
			return fmt.Errorf("requeue task ticket notice: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil || n != 1 {
			return fmt.Errorf("task ticket notice retry lost compare-and-swap: %s/%s", taskID, ticketID)
		}
		wire := proto.TaskTicketNotice{SourceMachine: machine, TaskID: taskID, TicketID: ticketID,
			TicketKind: out.TicketKind, SourceSeq: out.SourceSeq, OwnershipVersion: version, Target: *out.Target}
		eventCard := ""
		if wire.Target.Kind == proto.DeliverySeat {
			eventCard = wire.Target.Card
		}
		if _, err := s.appendEvent(tx, sink, eventCard, EvTaskTicketNotice, actor, wire); err != nil {
			return err
		}
		out.Status, out.Reason, out.LastError, out.UpdatedAt = "pending", "", "", now
		retried = true
		return nil
	})
	if err != nil {
		log().Warn("explicit task ticket notice retry failed", "machine", machine, "task", taskID, "ticket", ticketID, "cause", err)
		return TaskTicketNoticeState{}, false, err
	}
	if retried {
		log().Info("explicit task ticket notice retry queued", "machine", machine, "task", taskID, "ticket", ticketID,
			"ownership_version", version, "target_kind", out.Target.Kind)
	}
	return out, retried, nil
}
