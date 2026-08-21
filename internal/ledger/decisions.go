// 裁决项（Decision）：主会话回合末落的结构化请示。开/答均落事件流；
// open 裁决是「需要你」面的一等数据源（derived.go 联动）。一期答复消费 =
// 会话唤醒后 ListDecisions 查账；自动唤醒留三期。
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// OpenDecision 开一条裁决。cardID 空 = 项目级请示；options 可空 = 开放问答。
func (s *Store) OpenDecision(cardID, body string, options []string, createdBy string) (Decision, error) {
	if body == "" {
		return Decision{}, fmt.Errorf("裁决 body 不能为空")
	}
	var decision Decision
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if cardID != "" {
			if _, err := getCardTx(s, tx, cardID); err != nil {
				return fmt.Errorf("裁决: 卡 %s: %w", cardID, err)
			}
		}
		var opts any
		if len(options) > 0 {
			raw, _ := json.Marshal(options)
			opts = string(raw)
		}
		var cardIDValue any
		if cardID != "" {
			cardIDValue = cardID
		}
		now := time.Now()
		var id int64
		if s.dialect == dialectPG {
			if err := tx.QueryRow(s.q(`INSERT INTO decisions (card_id, body, options, status, created_by, created_at)
				VALUES (?,?,?,'open',?,?) RETURNING id`), cardIDValue, body, opts, createdBy, s.tval(now)).Scan(&id); err != nil {
				return fmt.Errorf("写裁决: %w", err)
			}
		} else {
			result, err := tx.Exec(s.q(`INSERT INTO decisions (card_id, body, options, status, created_by, created_at)
				VALUES (?,?,?,'open',?,?)`), cardIDValue, body, opts, createdBy, s.tval(now))
			if err != nil {
				return fmt.Errorf("写裁决: %w", err)
			}
			id, err = result.LastInsertId()
			if err != nil {
				return fmt.Errorf("取裁决 id: %w", err)
			}
		}
		if _, err := s.appendEvent(tx, sink, cardID, EvDecisionOpened, createdBy,
			map[string]any{"decision_id": id, "body": body, "options": options}); err != nil {
			return err
		}
		decision = Decision{ID: id, CardID: cardID, Body: body, Options: options,
			Status: "open", CreatedBy: createdBy, CreatedAt: now}
		return nil
	})
	return decision, err
}

// AnswerDecision 答复。已答复的拒绝（ErrBadState）——答案是账，不许改写；
// 要改口径开新裁决。
func (s *Store) AnswerDecision(id int64, answer, answeredBy string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		var status string
		var cardID sql.NullString
		err := tx.QueryRow(s.q(`SELECT status, card_id FROM decisions WHERE id = ?`), id).Scan(&status, &cardID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("裁决 %d: %w", id, ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("读裁决: %w", err)
		}
		if status != "open" {
			log().Warn("答复被拒：已答复", "decision", id)
			return fmt.Errorf("裁决 %d 已答复: %w", id, ErrBadState)
		}
		if _, err := tx.Exec(s.q(`UPDATE decisions SET status = 'answered', answer = ?, answered_by = ?, answered_at = ?
			WHERE id = ?`), answer, answeredBy, s.tval(time.Now()), id); err != nil {
			return fmt.Errorf("写答复: %w", err)
		}
		_, err = s.appendEvent(tx, sink, cardID.String, EvDecisionAnswered, answeredBy,
			map[string]any{"decision_id": id, "answer": answer})
		return err
	})
}

// ListDecisions openOnly=true 只列未答复（全局裁决收件箱）；false 全量按
// 创建时间升序。
func (s *Store) ListDecisions(openOnly bool) ([]Decision, error) {
	query := decisionSelect
	if openOnly {
		query += ` WHERE status = 'open'`
	}
	query += ` ORDER BY id ASC`
	rows, err := s.db.Query(s.q(query))
	if err != nil {
		return nil, fmt.Errorf("列裁决: %w", err)
	}
	return scanDecisions(rows)
}

// decisionSelect 是裁决行的取列清单，两个查询共用，避免列序漂移。
const decisionSelect = `SELECT id, card_id, body, options, status, created_by, answer, answered_by, created_at, answered_at
		FROM decisions`

// DecisionsOf 取一张卡上的全部裁决（含已答复的），按 id 升序。
//
// why 要按卡取而不是让调用方拉全表过滤：详情抽屉要在卡上就地看到请示正文、
// 候选项并直接答复（「卡的一切信息只在抽屉一处看」），而裁决只增不删，全表
// 会一直长。空 cardID 返回空切片而不是项目级裁决——项目级的入口是收件箱。
func (s *Store) DecisionsOf(cardID string) ([]Decision, error) {
	if cardID == "" {
		return nil, nil
	}
	rows, err := s.db.Query(s.q(decisionSelect+` WHERE card_id = ? ORDER BY id ASC`), cardID)
	if err != nil {
		return nil, fmt.Errorf("取卡 %s 的裁决: %w", cardID, err)
	}
	return scanDecisions(rows)
}

// scanDecisions 把结果集扫成 Decision 切片；调用方负责 rows 的来源，本函数负责关闭。
func scanDecisions(rows *sql.Rows) ([]Decision, error) {
	defer rows.Close()
	var out []Decision
	for rows.Next() {
		var decision Decision
		var cardID, options, answer, answeredBy sql.NullString
		var createdAt, answeredAt any
		if err := rows.Scan(&decision.ID, &cardID, &decision.Body, &options, &decision.Status,
			&decision.CreatedBy, &answer, &answeredBy, &createdAt, &answeredAt); err != nil {
			return nil, fmt.Errorf("扫裁决行: %w", err)
		}
		decision.CardID, decision.Answer, decision.AnsweredBy = cardID.String, answer.String, answeredBy.String
		if options.Valid && options.String != "" {
			if err := jsonUnmarshal(options.String, &decision.Options); err != nil {
				return nil, err
			}
		}
		decision.CreatedAt, decision.AnsweredAt = toTime(createdAt), toTime(answeredAt)
		out = append(out, decision)
	}
	return out, rows.Err()
}
