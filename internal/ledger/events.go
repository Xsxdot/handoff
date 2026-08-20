// 账本事件单流：同事务 append、PG 事务内 pg_notify 推送、游标升序读。
// 追加语义与 internal/store 的 events 表同源：append-only、seq 全局
// 自增（单卡内稀疏）、升序读截断尾部保证游标只越过真正收到的事件。
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// appendEvent 在事务内落一条账本事件并（PG）推送通知。cardID 可空 =
// 项目级事件。返回 seq。所有领域操作共用此入口，禁止绕过它裸 INSERT。
func (s *Store) appendEvent(tx *sql.Tx, sink *eventSink, cardID, typ, actor string, payload any) (int64, error) {
	return s.appendEventAt(tx, sink, cardID, typ, actor, payload, time.Now())
}

// appendEventAt 同 appendEvent，但由调用方给定事件时间——调用方需要把
// 同一个时间戳回填进返回给上层的 Event，否则返回值的 CreatedAt 是零值，
// 机器消费方（CLI stdout、Plan D 的 HTTP API）会读到 0001-01-01。
func (s *Store) appendEventAt(tx *sql.Tx, sink *eventSink, cardID, typ, actor string, payload any, at time.Time) (int64, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("编码事件 payload: %w", err)
	}
	var cid any
	if cardID != "" {
		cid = cardID
	}
	var seq int64
	if s.dialect == dialectPG {
		// PG 用 RETURNING 拿 seq，并在同事务内 NOTIFY——提交即送达 LISTEN 端
		err = tx.QueryRow(s.q(`INSERT INTO card_events (card_id, type, actor, payload, created_at)
			VALUES (?,?,?,?,?) RETURNING seq`),
			cid, typ, actor, string(raw), s.tval(at)).Scan(&seq)
		if err == nil {
			_, err = tx.Exec(`SELECT pg_notify('card_events', $1)`, fmt.Sprint(seq))
		}
	} else {
		var result sql.Result
		result, err = tx.Exec(s.q(`INSERT INTO card_events (card_id, type, actor, payload, created_at)
			VALUES (?,?,?,?,?)`),
			cid, typ, actor, string(raw), s.tval(at))
		if err == nil {
			seq, err = result.LastInsertId()
		}
	}
	if err != nil {
		return 0, fmt.Errorf("落账本事件 %s: %w", typ, err)
	}
	sink.seqs = append(sink.seqs, seq)
	return seq, nil
}

// EventsFromAsc 按 seq 升序读事件。cardIDs 空 = 全流（含项目级）；fromSeq
// 排他；limit<=0 取 1000。升序 + LIMIT 截尾，游标语义与 store.EventsFromAsc
// 一致——绝不能改成降序截头，那会让游标永久跨过缺口。
func (s *Store) EventsFromAsc(cardIDs []string, fromSeq int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 1000
	}
	q := `SELECT seq, card_id, type, actor, payload, source_target, source_task, source_seq, created_at
		FROM card_events WHERE seq > ?`
	args := []any{fromSeq}
	if len(cardIDs) > 0 {
		q += ` AND card_id IN (?` + strings.Repeat(",?", len(cardIDs)-1) + `)`
		for _, id := range cardIDs {
			args = append(args, id)
		}
	}
	q += ` ORDER BY seq ASC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(s.q(q), args...)
	if err != nil {
		return nil, fmt.Errorf("读账本事件流: %w", err)
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var event Event
		var cardID, sourceTarget, sourceTask sql.NullString
		var sourceSeq sql.NullInt64
		var raw string
		var createdAt any
		if err := rows.Scan(&event.Seq, &cardID, &event.Type, &event.Actor, &raw,
			&sourceTarget, &sourceTask, &sourceSeq, &createdAt); err != nil {
			return nil, fmt.Errorf("扫描事件行: %w", err)
		}
		event.CardID = cardID.String
		event.SourceTarget = sourceTarget.String
		event.SourceTask = sourceTask.String
		event.SourceSeq = sourceSeq.Int64
		event.Payload = json.RawMessage(raw)
		event.CreatedAt = toTime(createdAt)
		out = append(out, event)
	}
	return out, rows.Err()
}

// ---- 以下为建立在事件流上的领域操作 ----

var cardRefPat = regexp.MustCompile(`#(B\d+(?:\.\d+)*)`)

// DispatchSnapshot 派发事件快照：模板版本 + 纪律块角色名 + 落点。
// 「B107 那次派发用的哪版纪律块」从这里答（蓝图 §3.3 取证文化）。
//
// 原字段是正文指纹；纪律块改为具名资源后正文不再经过 CLI，指纹无从算起，
// 同一个问题的答案换成名字。老事件里的旧指纹键留在已落盘的 payload 里，
// 只是不再写新的——事件是追加式的，不做回填。
type DispatchSnapshot struct {
	Template        string `json:"template"`
	TemplateVersion int    `json:"template_version"`
	DisciplineName  string `json:"discipline_name"`
	Target          string `json:"target"`
	TaskID          string `json:"task_id"`
	Branch          string `json:"branch"`
	Purpose         string `json:"purpose,omitempty"` // implement|review|…：审阅轮不新开分支，靠它区分
	PlanPath        string `json:"plan_path,omitempty"`
	Actor           string `json:"-"`
}

// RecordDispatch 落派发事件。
func (s *Store) RecordDispatch(cardID string, snap DispatchSnapshot) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("派发落账: 卡 %s: %w", cardID, err)
		}
		_, err := s.appendEvent(tx, sink, cardID, EvDispatched, snap.Actor, snap)
		return err
	})
}

// RecordReviewVerdict 落审阅裁决事件（node 是回合计数分组键，raw 是
// verdict block 原文取证）。
func (s *Store) RecordReviewVerdict(cardID, node string, pass bool, raw, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("裁决落账: 卡 %s: %w", cardID, err)
		}
		_, err := s.appendEvent(tx, sink, cardID, EvReviewVerdict, actor,
			map[string]any{"node": node, "pass": pass, "raw": raw})
		return err
	})
}

// AddComment 发评论。body 里的 #B 号引用解析出来：存在的卡自动建
// relates 边（幂等），不存在的只留在 refs 里（评论是记录不是校验）。
// kind ∈ {普通, 更正}——「更正」承接 markdown 总账的变更痕迹文化。
func (s *Store) AddComment(cardID, body, kind, actor string) (Event, error) {
	return s.addComment(cardID, body, kind, actor, "")
}

// AddCommentReset 同 AddComment，但 payload 附 human_reset_node=<节点>。
// 这是「人工介入重置回合计数」（spec §5）的唯一落账入口：节点执行器
// （Plan C 的 CountRounds）读到该字段即把对应节点的裁决轮次清零。
func (s *Store) AddCommentReset(cardID, body, kind, actor, resetNode string) (Event, error) {
	if resetNode == "" {
		return Event{}, fmt.Errorf("重置评论: 节点名不能为空")
	}
	return s.addComment(cardID, body, kind, actor, resetNode)
}

func (s *Store) addComment(cardID, body, kind, actor, resetNode string) (Event, error) {
	if kind != "普通" && kind != "更正" {
		return Event{}, fmt.Errorf("评论 kind %q 不在 {普通,更正}", kind)
	}
	var event Event
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("评论: 卡 %s: %w", cardID, err)
		}
		var refs []string
		for _, match := range cardRefPat.FindAllStringSubmatch(body, -1) {
			ref := match[1]
			if ref == cardID {
				continue
			}
			refs = append(refs, ref)
			if _, err := getCardTx(s, tx, ref); err != nil {
				if errors.Is(err, ErrNotFound) {
					continue // 幽灵引用：不建边不报错
				}
				return fmt.Errorf("评论引用查卡 %s: %w", ref, err)
			}
			// 幂等建边：已存在同主键则忽略（两方言都支持 ON CONFLICT DO NOTHING）
			if _, err := tx.Exec(s.q(`INSERT INTO card_relations (from_id, to_id, type, created_at)
				VALUES (?,?,?,?) ON CONFLICT (from_id, to_id, type) DO NOTHING`),
				cardID, ref, RelRelates, s.tval(time.Now())); err != nil {
				return fmt.Errorf("评论引用建边 %s: %w", ref, err)
			}
		}
		payload := map[string]any{"kind": kind, "body": body, "refs": refs}
		if resetNode != "" {
			payload["human_reset_node"] = resetNode
		}
		now := time.Now()
		seq, err := s.appendEventAt(tx, sink, cardID, EvComment, actor, payload, now)
		if err != nil {
			return err
		}
		event = Event{Seq: seq, CardID: cardID, Type: EvComment, Actor: actor,
			CreatedAt: now.UTC()}
		raw, _ := json.Marshal(payload)
		event.Payload = raw
		return nil
	})
	return event, err
}

// RecordAcceptance 落验收结果事件（verified=true 表示真机已验）。
// 判据文本在卡字段；结果是事件——「已验/待真机验」从最后一条
// acceptance_recorded 推导。
func (s *Store) RecordAcceptance(cardID string, verified bool, evidence, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("验收: 卡 %s: %w", cardID, err)
		}
		_, err := s.appendEvent(tx, sink, cardID, EvAcceptanceRecorded, actor,
			map[string]any{"verified_on_real_machine": verified, "evidence": evidence})
		return err
	})
}

// RecordBranchMerged 落合并环节的外部动作事件。
//
// 参数：workBranch 工作分支名；base 基线分支名；pushedWorkBranch 本次是否
// 真的推了工作分支（本地已有则推，走 fetch 兜底那条腿则为 false）。
//
// 为什么要专门落这条：合并环节会 push 到 origin——外部可见、不易撤回。
// 自动化做的外部动作必须在 timeline 上留痕，否则「这次到底往 origin 推了
// 什么」只能去翻日志。
func (s *Store) RecordBranchMerged(cardID, workBranch, base string, pushedWorkBranch bool, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("合并落账: 卡 %s: %w", cardID, err)
		}
		_, err := s.appendEvent(tx, sink, cardID, EvBranchMerged, actor,
			map[string]any{
				"work_branch":        workBranch,
				"pushed_work_branch": pushedWorkBranch,
				"merged_into":        base,
				"pushed_base":        base,
			})
		return err
	})
}

// MarkNeedsHuman 打等人标记（reason 必填）；ClearNeedsHuman 清除。
// 等人不落列，从最后一条 needs_human/needs_cleared 事件推导（spec §2）。
func (s *Store) MarkNeedsHuman(cardID, reason, actor string) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("等人标记必须带 reason")
	}
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("等人: 卡 %s: %w", cardID, err)
		}
		_, err := s.appendEvent(tx, sink, cardID, EvNeedsHuman, actor, map[string]any{"reason": reason})
		return err
	})
}

// ClearNeedsHuman 清除等人标记。
func (s *Store) ClearNeedsHuman(cardID, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("清等人: 卡 %s: %w", cardID, err)
		}
		_, err := s.appendEvent(tx, sink, cardID, EvNeedsCleared, actor, map[string]any{})
		return err
	})
}

// ClearNeedsHumanFrom 仅当当前生效的等人标记是 actor 自己打的时才清除，
// 返回是否真的清了（没打过、已清过、或标记是别人打的都返回 false, nil）。
//
// why 要「只撤自己的」而不是让环节直接调 ClearNeedsHuman：等人标记有三种
// 来源——人手工打的、审阅环节打的、合并环节打的。环节跑成一轮就无条件清，
// 会把人刚打上的「先别动这张卡」和另一个环节的「合并冲突待处理」一起抹掉，
// 而那两件事谁也没解决。撤回权只能落在打标记的那一方手里；人是例外，人有权
// 撤任何来源的标记，那条走 ClearNeedsHuman。
func (s *Store) ClearNeedsHumanFrom(cardID, actor string) (bool, error) {
	cleared := false
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("撤回等人: 卡 %s: %w", cardID, err)
		}
		var typ, owner string
		row := tx.QueryRow(s.q(`SELECT type, actor FROM card_events
			WHERE card_id = ? AND type IN (?, ?) ORDER BY seq DESC LIMIT 1`),
			cardID, EvNeedsHuman, EvNeedsCleared)
		switch err := row.Scan(&typ, &owner); {
		case errors.Is(err, sql.ErrNoRows):
			return nil // 这张卡从没打过等人标记，无从撤回
		case err != nil:
			return fmt.Errorf("撤回等人: 读卡 %s 的等人事件: %w", cardID, err)
		}
		// 最后一条是 needs_cleared 说明标记当前已不生效；actor 对不上说明
		// 这条标记是别人打的。两种情况都不动，也不算错。
		if typ != EvNeedsHuman || owner != actor {
			return nil
		}
		if _, err := s.appendEvent(tx, sink, cardID, EvNeedsCleared, actor, map[string]any{}); err != nil {
			return err
		}
		cleared = true
		return nil
	})
	return cleared, err
}

// Subtree 返回卡树成员 id 集：root + 全部后代（parent 链）+ 并入成员
// （merged_into 指向集内任一成员的卡）。多路 wait 用。
//
// 注意：它的语义含并入成员，与抽屉「子任务」区不是一回事——
// 那里要的是直接子卡，走 ChildrenOf。
func (s *Store) Subtree(rootID string) ([]string, error) {
	if _, err := s.GetCard(rootID); err != nil {
		return nil, err
	}
	set := map[string]bool{rootID: true}
	frontier := []string{rootID}
	for len(frontier) > 0 {
		q := `SELECT id FROM cards WHERE parent_id IN (?` + strings.Repeat(",?", len(frontier)-1) + `)`
		args := make([]any, len(frontier))
		for i, id := range frontier {
			args[i] = id
		}
		rows, err := s.db.Query(s.q(q), args...)
		if err != nil {
			return nil, fmt.Errorf("读子树: %w", err)
		}
		var next []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			if !set[id] {
				set[id] = true
				next = append(next, id)
			}
		}
		rowErr := rows.Err()
		rows.Close()
		if rowErr != nil {
			return nil, rowErr
		}
		frontier = next
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	q := `SELECT from_id FROM card_relations WHERE type = 'merged_into' AND to_id IN (?` +
		strings.Repeat(",?", len(ids)-1) + `)`
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.Query(s.q(q), args...)
	if err != nil {
		return nil, fmt.Errorf("读并入成员: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		set[id] = true
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, rows.Err()
}

// WorkBranch 卡的工作分支：最近一次**非审阅**派发所用的分支。
// 审阅是只读的、跑在工作分支上，不新开分支——所以「卡的分支」这个问题
// 的答案必须跳过审阅轮，否则合并节点会去合一条审阅分支，而第二轮审阅
// 会撞上第一轮的同名分支（真机实测：fatal: a branch named ... already exists）。
// 老快照没有 purpose 字段时回落到挂账表按 task_id 查用途。
func (s *Store) WorkBranch(cardID string) (string, error) {
	events, err := s.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		return "", fmt.Errorf("读卡 dispatched 事件: %w", err)
	}
	links, err := s.TasksOf(cardID)
	if err != nil {
		return "", err
	}
	purposeOf := map[string]string{}
	for _, link := range links {
		purposeOf[link.TaskID] = link.Purpose
	}
	branch := ""
	for _, event := range events {
		if event.Type != EvDispatched {
			continue
		}
		var snapshot DispatchSnapshot
		if err := json.Unmarshal(event.Payload, &snapshot); err != nil {
			continue
		}
		purpose := snapshot.Purpose
		if purpose == "" {
			purpose = purposeOf[snapshot.TaskID]
		}
		if purpose == PurposeReview {
			continue
		}
		if snapshot.Branch != "" {
			branch = snapshot.Branch
		}
	}
	if branch == "" {
		return "", fmt.Errorf("卡 %s 没有非审阅的 dispatched 快照（还没派过实现轮？）: %w", cardID, ErrNotFound)
	}
	return branch, nil
}

// ReviewRounds 已派出的审阅轮数（用于给每轮审阅分支编号，避免同名撞车）。
// 与 ledgerstep 的 CountRounds 不是一回事：那个数的是「裁决回合」（人工
// 重置会清零，用于封顶），这个数的是「派过几次审阅」（只增不减，用于起名）。
func (s *Store) ReviewRounds(cardID string) (int, error) {
	links, err := s.TasksOf(cardID)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, link := range links {
		if link.Purpose == PurposeReview {
			count++
		}
	}
	return count, nil
}
