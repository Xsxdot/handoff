// 任务卡的建/读/改/终止/复活与 B 号分配。状态转移在 move.go，
// 关系与合并在 relations.go / merge.go——本文件不碰关系表。
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// NewCard 建卡请求。Workflow 为空时由账本解析为 triage；Parent 非空则建子卡
// （点号 id，继承基线由查询期解析）。
type NewCard struct {
	Title, Project, Priority, Parent, Workflow, BaseBranch, Actor string
}

var topIDPat = regexp.MustCompile(`^B(\d+)$`)

// maxWorkflowNesting 父链上同名工作流的嵌套上限（含新卡自身）。
//
// why 存在：子卡可绑任意工作流模板——包括「分域开发」这类会在拆解节点
// 再生子卡的模板自身，递归是刻意保留的组合性质（spec §8.3）；这个常量
// 挡的是失控递归（拆解把活原样再拆给自己）。why 是 3：两层分域已覆盖
// 「域内再分小领域」，第三层留给极端大活；再深就该先竖切域了。
// 配置化（项目实例化清单覆盖）等真实需求出现再做。
const maxWorkflowNesting = 3

// EnsureMinB 垫高 B 号水位（迁移前防与 markdown 总账撞号；只升不降）。
func (s *Store) EnsureMinB(n int) error {
	return s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		current := 0
		var value string
		err := tx.QueryRow(s.q(`SELECT value FROM ledger_meta WHERE key = 'min_b'`)).Scan(&value)
		if err == nil {
			current, _ = strconv.Atoi(value)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("读 min_b: %w", err)
		}
		if n <= current {
			return nil
		}
		// upsert：两方言都支持 INSERT ... ON CONFLICT
		_, err = tx.Exec(s.q(`INSERT INTO ledger_meta (key, value) VALUES ('min_b', ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value`), strconv.Itoa(n))
		if err != nil {
			return fmt.Errorf("写 min_b: %w", err)
		}
		return nil
	})
}

// nextTopID 事务内分配下一个顶层 B 号：max(现存顶层号, min_b) + 1。
// 号永不复用（终止/归档的卡仍占号）。在 mutate 的全局写锁内调用，
// 无并发分配竞态。
func (s *Store) nextTopID(tx *sql.Tx) (string, error) {
	maxNumber := 0
	rows, err := tx.Query(`SELECT id FROM cards WHERE parent_id IS NULL`)
	if err != nil {
		return "", fmt.Errorf("扫现存 B 号: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		if match := topIDPat.FindStringSubmatch(id); match != nil {
			if number, _ := strconv.Atoi(match[1]); number > maxNumber {
				maxNumber = number
			}
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	var value string
	if err := tx.QueryRow(s.q(`SELECT value FROM ledger_meta WHERE key = 'min_b'`)).Scan(&value); err == nil {
		if number, _ := strconv.Atoi(value); number > maxNumber {
			maxNumber = number
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return fmt.Sprintf("B%d", maxNumber+1), nil
}

// nextChildID 分配 parent 下一个点号子位（B157 → B157.1、B157.2…）。
func (s *Store) nextChildID(tx *sql.Tx, parent string) (string, error) {
	rows, err := tx.Query(s.q(`SELECT id FROM cards WHERE parent_id = ?`), parent)
	if err != nil {
		return "", fmt.Errorf("扫子卡号: %w", err)
	}
	defer rows.Close()
	maxNumber := 0
	prefix := parent + "."
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		if rest, ok := strings.CutPrefix(id, prefix); ok {
			if number, err := strconv.Atoi(rest); err == nil && number > maxNumber {
				maxNumber = number
			}
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s.%d", parent, maxNumber+1), nil
}

// CreateCard 建卡：分配 B 号、钉工作流最新版本、初始态 = 工作流首态、
// 落 card_created 事件。
func (s *Store) CreateCard(nc NewCard) (Card, error) {
	if strings.TrimSpace(nc.Title) == "" {
		return Card{}, fmt.Errorf("建卡: 标题不能为空")
	}
	if nc.Project == "" {
		return Card{}, fmt.Errorf("建卡: project 不能为空")
	}
	if nc.Priority == "" {
		nc.Priority = "中"
	}
	if strings.TrimSpace(nc.Workflow) == "" {
		nc.Workflow = "triage"
	}
	wf, err := s.GetWorkflow(nc.Workflow, 0)
	if err != nil {
		return Card{}, fmt.Errorf("建卡取工作流 %q: %w", nc.Workflow, err)
	}
	if len(wf.Def.States) == 0 {
		return Card{}, fmt.Errorf("建卡取工作流 %q: 状态序列不能为空", nc.Workflow)
	}
	var card Card
	err = s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		var id string
		var idErr error
		if nc.Parent != "" {
			if _, parentErr := getCardTx(s, tx, nc.Parent); parentErr != nil {
				return fmt.Errorf("父卡 %s: %w", nc.Parent, parentErr)
			}
			nesting, err := s.workflowNestingTx(tx, nc.Parent, wf.Name)
			if err != nil {
				return err
			}
			if nesting+1 > maxWorkflowNesting {
				log().Warn("建卡被拒：工作流嵌套超限",
					"parent", nc.Parent, "workflow", wf.Name, "nesting", nesting)
				return fmt.Errorf("父链上已有 %d 层 %q 工作流（上限 %d）——先竖切域或给子卡换更细粒度的工作流: %w",
					nesting, wf.Name, maxWorkflowNesting, ErrBadState)
			}
			id, idErr = s.nextChildID(tx, nc.Parent)
		} else {
			id, idErr = s.nextTopID(tx)
		}
		if idErr != nil {
			return idErr
		}
		now := time.Now()
		var parent any
		if nc.Parent != "" {
			parent = nc.Parent
		}
		var base any
		if nc.BaseBranch != "" {
			base = nc.BaseBranch
		}
		_, err := tx.Exec(s.q(`INSERT INTO cards
			(id, title, status, priority, project, parent_id, workflow_name, workflow_version,
			 attachments, acceptance_criteria, base_branch, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,'[]','',?,?,?)`),
			id, nc.Title, wf.Def.States[0], nc.Priority, nc.Project, parent,
			wf.Name, wf.Version, base, s.tval(now), s.tval(now))
		if err != nil {
			return fmt.Errorf("插入卡 %s: %w", id, err)
		}
		if _, err := s.appendEvent(tx, sink, id, EvCardCreated, nc.Actor,
			map[string]any{"title": nc.Title, "workflow": wf.Name, "workflow_version": wf.Version}); err != nil {
			return err
		}
		// 父卡 timeline 留痕：审计链要能从父卡回答「子卡从哪来」。放在同
		// 一事务里——子卡建了而父卡没痕，或反过来，都是账本自相矛盾。
		if nc.Parent != "" {
			if _, err := s.appendEvent(tx, sink, nc.Parent, EvComment, nc.Actor,
				map[string]any{"kind": "普通",
					"body": fmt.Sprintf("创建子卡 %s：%s", id, nc.Title),
					"refs": []string{id}}); err != nil {
				return err
			}
		}
		card = Card{ID: id, Title: nc.Title, Status: wf.Def.States[0], Priority: nc.Priority,
			Project: nc.Project, ParentID: nc.Parent, WorkflowName: wf.Name,
			WorkflowVersion: wf.Version, BaseBranch: nc.BaseBranch, CreatedAt: now, UpdatedAt: now}
		return nil
	})
	return card, err
}

// cardColumns 与 scanCard 配对——加列只改这两处（照抄 store 的 taskColumns 模式）。
const cardColumns = `id, title, status, terminate_reason, priority, project, parent_id,
	workflow_name, workflow_version, attachments, acceptance_criteria, base_branch,
	driver_session, driver_heartbeat_at, created_at, updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanCard(row rowScanner) (Card, error) {
	var card Card
	var terminateReason, parentID, acceptanceCriteria, baseBranch, driverSession sql.NullString
	var attachments string
	var heartbeatAt, createdAt, updatedAt any
	if err := row.Scan(&card.ID, &card.Title, &card.Status, &terminateReason, &card.Priority,
		&card.Project, &parentID, &card.WorkflowName, &card.WorkflowVersion, &attachments,
		&acceptanceCriteria, &baseBranch, &driverSession, &heartbeatAt, &createdAt, &updatedAt); err != nil {
		return Card{}, err
	}
	card.TerminateReason, card.ParentID, card.AcceptanceCriteria = terminateReason.String, parentID.String, acceptanceCriteria.String
	card.BaseBranch, card.DriverSession = baseBranch.String, driverSession.String
	if err := json.Unmarshal([]byte(attachments), &card.Attachments); err != nil {
		return Card{}, fmt.Errorf("解码附件: %w", err)
	}
	card.DriverHeartbeatAt, card.CreatedAt, card.UpdatedAt = toTime(heartbeatAt), toTime(createdAt), toTime(updatedAt)
	return card, nil
}

func getCardTx(s *Store, tx *sql.Tx, id string) (Card, error) {
	card, err := scanCard(tx.QueryRow(s.q(`SELECT `+cardColumns+` FROM cards WHERE id = ?`), id))
	if errors.Is(err, sql.ErrNoRows) {
		return Card{}, ErrNotFound
	}
	return card, err
}

// workflowNestingTx 数父链（从 parent 起向上、含 parent 自身）里钉了
// 同名工作流的卡数。64 层上限与 ancestorsTx 同源：防坏数据成环死循环。
func (s *Store) workflowNestingTx(tx *sql.Tx, parent, workflowName string) (int, error) {
	count := 0
	current := parent
	for i := 0; i < 64; i++ {
		var name string
		var up sql.NullString
		err := tx.QueryRow(s.q(`SELECT workflow_name, parent_id FROM cards WHERE id = ?`), current).
			Scan(&name, &up)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return count, nil
			}
			return 0, fmt.Errorf("数工作流嵌套: 读卡 %s: %w", current, err)
		}
		if name == workflowName {
			count++
		}
		if !up.Valid || up.String == "" {
			return count, nil
		}
		current = up.String
	}
	return 0, fmt.Errorf("父链深度超限（数据疑似成环）: %s", parent)
}

// GetCard 读单卡。不存在返回 ErrNotFound。
func (s *Store) GetCard(id string) (Card, error) {
	card, err := scanCard(s.db.QueryRow(s.q(`SELECT `+cardColumns+` FROM cards WHERE id = ?`), id))
	if errors.Is(err, sql.ErrNoRows) {
		return Card{}, fmt.Errorf("卡 %s: %w", id, ErrNotFound)
	}
	return card, err
}

// CardBrief 是卡的最小展示三元组：抽屉「子任务」区一行需要的全部。
//
// 为什么不直接返回 Card：子任务区只渲染 id + 标题 + 状态徽标，把整张卡
// （含判据、附件、驱动租约）塞进详情响应，是给一个只读列表付整卡的序列化代价。
type CardBrief struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// ChildrenOf 返回该卡的直接子卡（parent_id = cardID），按 id 升序。
//
// 参数：cardID 父卡 id。
// 返回：直接子卡的最小三元组；卡不存在时返回错误（上层映射 404）。
//
// 注意：只一层，不递归。要全后代请看 Subtree——但那个语义不一样
// （含 merged_into 的并入成员），不是「子任务」。
//
// 为什么卡不存在要报错而不是返回空：空切片是「这张卡没有子卡」的合法答案，
// 与「你给的 id 根本不存在」混成同一个响应，前端就只能靠猜。
func (s *Store) ChildrenOf(cardID string) ([]CardBrief, error) {
	if _, err := s.GetCard(cardID); err != nil {
		return nil, fmt.Errorf("查子卡父卡 %s: %w", cardID, err)
	}
	rows, err := s.db.Query(s.q(
		`SELECT id, title, status FROM cards WHERE parent_id = ? ORDER BY id`), cardID)
	if err != nil {
		return nil, fmt.Errorf("读子卡 %s: %w", cardID, err)
	}
	defer rows.Close()
	children := make([]CardBrief, 0, 4)
	for rows.Next() {
		var brief CardBrief
		if err := rows.Scan(&brief.ID, &brief.Title, &brief.Status); err != nil {
			return nil, fmt.Errorf("扫描子卡 %s: %w", cardID, err)
		}
		children = append(children, brief)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读子卡 %s: %w", cardID, err)
	}
	return children, nil
}

// AttachFile 挂附件（同 path 幂等）；落 comment 事件记录动作，附件本体
// 是卡字段不是事件。
func (s *Store) AttachFile(id, kind, path, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		card, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("挂附件: 卡 %s: %w", id, err)
		}
		for _, attachment := range card.Attachments {
			if attachment.Path == path {
				return nil // 幂等
			}
		}
		card.Attachments = append(card.Attachments, Attachment{Kind: kind, Path: path})
		raw, _ := json.Marshal(card.Attachments)
		if _, err := tx.Exec(s.q(`UPDATE cards SET attachments = ?, updated_at = ? WHERE id = ?`),
			string(raw), s.tval(time.Now()), id); err != nil {
			return fmt.Errorf("写附件: %w", err)
		}
		_, err = s.appendEvent(tx, sink, id, EvComment, actor,
			map[string]any{"kind": "普通", "body": fmt.Sprintf("挂附件 %s:%s", kind, path)})
		return err
	})
}

// DetachFile 摘附件（按 path 匹配）。
func (s *Store) DetachFile(id, path, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		card, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("摘附件: 卡 %s: %w", id, err)
		}
		kept := card.Attachments[:0]
		for _, attachment := range card.Attachments {
			if attachment.Path != path {
				kept = append(kept, attachment)
			}
		}
		raw, _ := json.Marshal(kept)
		if _, err := tx.Exec(s.q(`UPDATE cards SET attachments = ?, updated_at = ? WHERE id = ?`),
			string(raw), s.tval(time.Now()), id); err != nil {
			return fmt.Errorf("写附件: %w", err)
		}
		_, err = s.appendEvent(tx, sink, id, EvComment, actor,
			map[string]any{"kind": "普通", "body": "摘附件 " + path})
		return err
	})
}

// SetAcceptance 写验收判据文本（判据是卡字段；验收**结果**走
// RecordAcceptance 落事件，Task 8）。
func (s *Store) SetAcceptance(id, criteria, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		result, err := tx.Exec(s.q(`UPDATE cards SET acceptance_criteria = ?, updated_at = ? WHERE id = ?`),
			criteria, s.tval(time.Now()), id)
		if err != nil {
			return fmt.Errorf("写判据: %w", err)
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return fmt.Errorf("卡 %s: %w", id, ErrNotFound)
		}
		_, err = s.appendEvent(tx, sink, id, EvComment, actor,
			map[string]any{"kind": "普通", "body": "更新验收判据"})
		return err
	})
}

// UpdateCardMeta 改标题/优先级（空串 = 不改该项）。落 comment 事件。
func (s *Store) UpdateCardMeta(id, title, priority, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		card, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("改卡: 卡 %s: %w", id, err)
		}
		if title == "" {
			title = card.Title
		}
		if priority == "" {
			priority = card.Priority
		}
		if _, err := tx.Exec(s.q(`UPDATE cards SET title = ?, priority = ?, updated_at = ? WHERE id = ?`),
			title, priority, s.tval(time.Now()), id); err != nil {
			return fmt.Errorf("写改卡: %w", err)
		}
		_, err = s.appendEvent(tx, sink, id, EvComment, actor,
			map[string]any{"kind": "普通", "body": fmt.Sprintf("改卡：标题=%q 优先级=%s", title, priority)})
		return err
	})
}

// CloseCard 终止（从任意非终态；reason 受控词表）。终止不是删除：
// 号仍占用、事件仍在流里、搁置可复活。
func (s *Store) CloseCard(id, reason, actor string) error {
	if reason != CloseCancelled && reason != CloseAbandoned && reason != CloseShelved {
		return fmt.Errorf("终止 reason %q 不在受控词表 {取消,废弃,搁置}", reason)
	}
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		card, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("终止: 卡 %s: %w", id, err)
		}
		if card.Status == StatusClosed || card.Status == StatusDone {
			log().Warn("终止被拒", "card", id, "status", card.Status)
			return fmt.Errorf("卡 %s 已处于 %s: %w", id, card.Status, ErrBadState)
		}
		if _, err := tx.Exec(s.q(`UPDATE cards SET status = ?, terminate_reason = ?, updated_at = ? WHERE id = ?`),
			StatusClosed, reason, s.tval(time.Now()), id); err != nil {
			return fmt.Errorf("写终止: %w", err)
		}
		_, err = s.appendEvent(tx, sink, id, EvStatusMoved, actor,
			map[string]any{"from": card.Status, "to": StatusClosed, "reason": reason})
		return err
	})
}

// ReviveCard 复活：仅 终止(搁置) → 待办。
func (s *Store) ReviveCard(id, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		card, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("复活: 卡 %s: %w", id, err)
		}
		if card.Status != StatusClosed || card.TerminateReason != CloseShelved {
			log().Warn("复活被拒", "card", id, "status", card.Status, "reason", card.TerminateReason)
			return fmt.Errorf("卡 %s 非 终止(搁置)，不可复活: %w", id, ErrBadState)
		}
		if _, err := tx.Exec(s.q(`UPDATE cards SET status = ?, terminate_reason = NULL, updated_at = ? WHERE id = ?`),
			StatusTodo, s.tval(time.Now()), id); err != nil {
			return fmt.Errorf("写复活: %w", err)
		}
		_, err = s.appendEvent(tx, sink, id, EvStatusMoved, actor,
			map[string]any{"from": StatusClosed, "to": StatusTodo, "reason": "复活"})
		return err
	})
}
