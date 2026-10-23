package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Xsxdot/handoff/internal/diag"
)

const sessionResidentRoundReadLimit = 100

type residentRoundPayload struct {
	Session  string `json:"session"`
	Identity string `json:"identity"`
	RoundID  string `json:"round_id"`
	Phase    string `json:"phase"`
	Reason   string `json:"reason,omitempty"`
}

// RecordSessionResidentRound appends one correlated observation to the existing
// session event stream. It does not infer liveness from a claim or lease.
func (s *Store) RecordSessionResidentRound(sessionID, identity, roundID, phase, reason string) (int64, error) {
	sessionID, identity, roundID = strings.TrimSpace(sessionID), strings.TrimSpace(identity), strings.TrimSpace(roundID)
	if sessionID == "" || identity == "" || roundID == "" {
		return 0, fmt.Errorf("resident round requires session, identity and round_id: %w", ErrBadState)
	}
	switch phase {
	case "start", "end", "fail":
	default:
		return 0, fmt.Errorf("resident round phase %q is invalid: %w", phase, ErrBadState)
	}
	if !utf8.ValidString(reason) {
		return 0, fmt.Errorf("resident round reason is not valid UTF-8: %w", ErrBadState)
	}
	if reason != "" {
		runes := []rune(strings.TrimSpace(reason))
		if len(runes) > 200 {
			reason = string(runes[:200]) + "…"
		} else {
			reason = string(runes)
		}
	}
	if _, err := s.GetSession(sessionID); err != nil {
		return 0, fmt.Errorf("resident round session %s: %w", sessionID, err)
	}
	var seq int64
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getSessionTx(s, tx, sessionID); err != nil {
			return err
		}
		sessionExpr, identityExpr, roundExpr, phaseExpr := `payload->>'session'`, `payload->>'identity'`, `payload->>'round_id'`, `payload->>'phase'`
		if s.dialect == dialectSQLite {
			sessionExpr, identityExpr = `json_extract(payload, '$.session')`, `json_extract(payload, '$.identity')`
			roundExpr, phaseExpr = `json_extract(payload, '$.round_id')`, `json_extract(payload, '$.phase')`
		}
		var exists int
		startQuery := `SELECT 1 FROM card_events WHERE card_id IS NULL AND type='resident_round' AND ` + sessionExpr + `=? AND ` + identityExpr + `=? AND ` + roundExpr + `=? AND ` + phaseExpr + `='start' LIMIT 1`
		startErr := tx.QueryRow(s.q(startQuery), sessionID, identity, roundID).Scan(&exists)
		if phase == "start" && startErr == nil {
			return fmt.Errorf("resident round %s already started: %w", roundID, ErrBadState)
		}
		if startErr != nil && !errors.Is(startErr, sql.ErrNoRows) {
			return fmt.Errorf("lookup resident round start: %w", startErr)
		}
		if phase != "start" && errors.Is(startErr, sql.ErrNoRows) {
			return fmt.Errorf("resident round %s has no correlated start: %w", roundID, ErrBadState)
		}
		if phase != "start" {
			terminalQuery := `SELECT 1 FROM card_events WHERE card_id IS NULL AND type='resident_round' AND ` + sessionExpr + `=? AND ` + identityExpr + `=? AND ` + roundExpr + `=? AND ` + phaseExpr + ` IN ('end','fail') LIMIT 1`
			err := tx.QueryRow(s.q(terminalQuery), sessionID, identity, roundID).Scan(&exists)
			if err == nil {
				return fmt.Errorf("resident round %s already has a terminal outcome: %w", roundID, ErrBadState)
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("lookup resident round terminal: %w", err)
			}
		}
		var err error
		seq, err = s.appendEvent(tx, sink, "", EvResidentRound, identity, residentRoundPayload{
			Session: sessionID, Identity: identity, RoundID: roundID, Phase: phase, Reason: reason,
		})
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("record resident round event: %w", err)
	}
	log().Info("驻场者回合事实已落账", "session", sessionID, "identity", identity, "round_id", roundID, "phase", phase, "seq", seq)
	return seq, nil
}

// SessionResidentRoundEventsContext reads only the latest fixed-size window for
// one session. The caller filters the active resident identity and reports truncation.
func (s *Store) SessionResidentRoundEventsContext(ctx context.Context, sessionID, identity string, limit int) ([]Event, bool, error) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(identity) == "" || limit < 1 || limit > sessionResidentRoundReadLimit {
		return nil, false, fmt.Errorf("resident round query requires session, identity and limit 1..%d", sessionResidentRoundReadLimit)
	}
	sessionExpr := `payload->>'session'`
	identityExpr := `payload->>'identity'`
	if s.dialect == dialectSQLite {
		sessionExpr = `json_extract(payload, '$.session')`
		identityExpr = `json_extract(payload, '$.identity')`
	}
	query := `SELECT seq, card_id, type, actor, payload, source_target, source_task, source_seq, created_at
		FROM card_events WHERE card_id IS NULL AND type='resident_round' AND ` + sessionExpr + `=? AND ` + identityExpr + `=? ORDER BY seq DESC LIMIT ?`
	started := time.Now()
	var events []Event
	stage, err := s.timedReadQuery(ctx, query, []any{sessionID, identity, limit + 1}, func(rows *sql.Rows) (int, int64, error) {
		for rows.Next() {
			var ev Event
			var cardID, sourceTarget, sourceTask sql.NullString
			var sourceSeq sql.NullInt64
			var raw string
			var createdAt any
			if err := rows.Scan(&ev.Seq, &cardID, &ev.Type, &ev.Actor, &raw, &sourceTarget, &sourceTask, &sourceSeq, &createdAt); err != nil {
				return len(events), 0, err
			}
			ev.CardID, ev.SourceTarget, ev.SourceTask, ev.SourceSeq = cardID.String, sourceTarget.String, sourceTask.String, sourceSeq.Int64
			ev.Payload, ev.CreatedAt = []byte(raw), toTime(createdAt)
			events = append(events, ev)
		}
		return len(events), 0, rows.Err()
	})
	if err != nil {
		log().Error("读取驻场者回合事实失败", append(diag.Attrs(ctx), "session", sessionID, "identity", identity, "limit", limit,
			"failed_stage", stage.failedStage, "error_class", readErrorClass(err, stage.failedStage), "cause", err)...)
		return nil, false, fmt.Errorf("read resident round facts for %s: %w", sessionID, err)
	}
	truncated := len(events) > limit
	if truncated {
		events = events[:limit]
	}
	for left, right := 0, len(events)-1; left < right; left, right = left+1, right-1 {
		events[left], events[right] = events[right], events[left]
	}
	log().Debug("读取驻场者回合事实完成", append(append(diag.Attrs(ctx), "session", sessionID, "identity", identity, "rows", len(events), "truncated", truncated,
		"elapsed_ns", time.Since(started).Nanoseconds()), stage.logAttrs()...)...)
	return events, truncated, nil
}
