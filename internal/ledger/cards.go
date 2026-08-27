// 任务卡的建/读/改/终止/复活与带前缀号段分配。状态转移在 move.go，
// 关系与合并在 relations.go / merge.go——本文件不碰关系表。
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// NewCard 建卡请求。Workflow 为空时由账本按现有工作流数量解析；Parent 非空则建子卡
// （点号 id，继承基线由查询期解析）。
type NewCard struct {
	Title, Project, Priority, Parent, Workflow, BaseBranch, Actor string
}

var topIDPat = regexp.MustCompile(`^([A-Z]{1,4})(\d+)$`)

// maxWorkflowNesting 父链上同名工作流的嵌套上限（含新卡自身）。
//
// why 存在：子卡可绑任意工作流模板——包括「分域开发」这类会在拆解节点
// 再生子卡的模板自身，递归是刻意保留的组合性质（spec §8.3）；这个常量
// 挡的是失控递归（拆解把活原样再拆给自己）。why 是 3：两层分域已覆盖
// 「域内再分小领域」，第三层留给极端大活；再深就该先竖切域了。
// 配置化（项目实例化清单覆盖）等真实需求出现再做。
const maxWorkflowNesting = 3

// EnsureMinB 垫高 B 前缀水位（迁移前防与 markdown 总账撞号；只升不降）。
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

// nextTopID 事务内分配下一个指定前缀的顶层号：
// max(该前缀现存顶层号, B 前缀的 min_b 水位) + 1。
// 号永不复用（终止/归档的卡仍占号）。在 mutate 的全局写锁内调用，
// 无并发分配竞态。min_b 只对 B 前缀生效，其它前缀从 1 起步。
func (s *Store) nextTopID(tx *sql.Tx, prefix string) (string, error) {
	maxNumber := 0
	rows, err := tx.Query(`SELECT id FROM cards WHERE parent_id IS NULL`)
	if err != nil {
		return "", fmt.Errorf("扫现存 %s 号: %w", prefix, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", fmt.Errorf("扫现存 %s 号时读卡: %w", prefix, err)
		}
		if match := topIDPat.FindStringSubmatch(id); match != nil {
			if match[1] != prefix {
				continue
			}
			if number, _ := strconv.Atoi(match[2]); number > maxNumber {
				maxNumber = number
			}
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("扫现存 %s 号: %w", prefix, err)
	}
	var value string
	if prefix == "B" {
		if err := tx.QueryRow(s.q(`SELECT value FROM ledger_meta WHERE key = 'min_b'`)).Scan(&value); err == nil {
			if number, _ := strconv.Atoi(value); number > maxNumber {
				maxNumber = number
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("读 B 前缀水位 min_b: %w", err)
		}
	}
	return fmt.Sprintf("%s%d", prefix, maxNumber+1), nil
}

// nextChildID 分配 parent 下一个点号子位（C1 → C1.1、C1.2…）。
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

// prepareCard 校验建卡请求并补默认值（优先级缺省「中」、工作流按账本现有流解析），
// 取回要钉住的工作流版本。
//
// 参数：nc 建卡请求（就地补默认，故取指针）。
// 返回：钉定版本的工作流；标题/project 为空、工作流不存在或状态序列为空时报错。
//
// 注意：只做纯校验与取流，不开事务——CreateCard 与 ImportCard 共用它，
// 保证两条通道的入参语义逐字一致（差别只在 id 从哪来）。
func (s *Store) prepareCard(nc *NewCard) (Workflow, error) {
	if strings.TrimSpace(nc.Title) == "" {
		return Workflow{}, fmt.Errorf("建卡: 标题不能为空")
	}
	if nc.Project == "" {
		return Workflow{}, fmt.Errorf("建卡: project 不能为空")
	}
	if nc.Priority == "" {
		nc.Priority = "中"
	}
	if strings.TrimSpace(nc.Workflow) == "" {
		log().Info("建卡解析缺省工作流", "project", nc.Project, "title", nc.Title)
		names, err := s.ListWorkflowNames()
		if err != nil {
			log().Error("建卡列工作流失败", "project", nc.Project, "title", nc.Title, "cause", err)
			return Workflow{}, fmt.Errorf("建卡解析缺省工作流: 列工作流失败: %w", err)
		}
		switch len(names) {
		case 0:
			err := fmt.Errorf("建卡缺少工作流：账本中没有工作流，请先用 workflow put 建立一条工作流")
			log().Warn("建卡被拒：账本没有工作流", "project", nc.Project, "title", nc.Title)
			return Workflow{}, err
		case 1:
			nc.Workflow = names[0]
			log().Info("建卡采用唯一工作流", "project", nc.Project, "title", nc.Title, "workflow", nc.Workflow)
		default:
			err := fmt.Errorf("建卡缺少工作流：账本中有多条工作流，请显式指定 --workflow（可选：%s）", strings.Join(names, "、"))
			log().Warn("建卡被拒：缺省工作流有歧义", "project", nc.Project, "title", nc.Title, "workflows", names)
			return Workflow{}, err
		}
	}
	wf, err := s.GetWorkflow(nc.Workflow, 0)
	if err != nil {
		return Workflow{}, fmt.Errorf("建卡取工作流 %q: %w", nc.Workflow, err)
	}
	if len(wf.Def.States) == 0 {
		return Workflow{}, fmt.Errorf("建卡取工作流 %q: 状态序列不能为空", nc.Workflow)
	}
	return wf, nil
}

// checkParentTx 校验父卡存在且同名工作流嵌套未超限。
//
// 参数：parent 父卡 id（空串直接放行）；workflowName 新卡要绑的工作流名。
// 返回：父卡不存在返回包装 ErrNotFound 的错误，嵌套超限返回包装 ErrBadState 的错误。
func (s *Store) checkParentTx(tx *sql.Tx, parent, workflowName string) error {
	if parent == "" {
		return nil
	}
	if _, err := getCardTx(s, tx, parent); err != nil {
		return fmt.Errorf("父卡 %s: %w", parent, err)
	}
	nesting, err := s.workflowNestingTx(tx, parent, workflowName)
	if err != nil {
		return err
	}
	if nesting+1 > maxWorkflowNesting {
		log().Warn("建卡被拒：工作流嵌套超限",
			"parent", parent, "workflow", workflowName, "nesting", nesting)
		return fmt.Errorf("父链上已有 %d 层 %q 工作流（上限 %d）——先竖切域或给子卡换更细粒度的工作流: %w",
			nesting, workflowName, maxWorkflowNesting, ErrBadState)
	}
	return nil
}

// insertCardTx 事务内落卡：插行 + card_created 事件 + 子卡时父卡 timeline 留痕。
//
// 参数：id 已定好的卡号；nc 已过 prepareCard 的请求；wf 钉定版本的工作流；
// extra 并入 card_created 负载的附加字段（导入通道用来标注来源，普通建卡传 nil）。
// 返回：建成的卡（内存态，字段与刚插入的行一致）。
func (s *Store) insertCardTx(tx *sql.Tx, sink *eventSink, id string, nc NewCard, wf Workflow, extra map[string]any) (Card, error) {
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
		return Card{}, fmt.Errorf("插入卡 %s: %w", id, err)
	}
	payload := map[string]any{"title": nc.Title, "workflow": wf.Name, "workflow_version": wf.Version}
	maps.Copy(payload, extra)
	if _, err := s.appendEvent(tx, sink, id, EvCardCreated, nc.Actor, payload); err != nil {
		return Card{}, err
	}
	// 父卡 timeline 留痕：审计链要能从父卡回答「子卡从哪来」。放在同
	// 一事务里——子卡建了而父卡没痕，或反过来，都是账本自相矛盾。
	if nc.Parent != "" {
		if _, err := s.appendEvent(tx, sink, nc.Parent, EvComment, nc.Actor,
			map[string]any{"kind": "普通",
				"body": fmt.Sprintf("创建子卡 %s：%s", id, nc.Title),
				"refs": []string{id}}); err != nil {
			return Card{}, err
		}
	}
	return Card{ID: id, Title: nc.Title, Status: wf.Def.States[0], Priority: nc.Priority,
		Project: nc.Project, ParentID: nc.Parent, WorkflowName: wf.Name,
		WorkflowVersion: wf.Version, BaseBranch: nc.BaseBranch, CreatedAt: now, UpdatedAt: now}, nil
}

// CreateCard 建卡：按项目分配号段前缀与顶层号、钉工作流最新版本、初始态 = 工作流首态、
// 落 card_created 事件。
func (s *Store) CreateCard(nc NewCard) (Card, error) {
	log().Info("开始建卡", "project", nc.Project, "title", nc.Title, "parent", nc.Parent)
	wf, err := s.prepareCard(&nc)
	if err != nil {
		log().Warn("建卡前校验失败", "project", nc.Project, "title", nc.Title, "cause", err)
		return Card{}, err
	}
	var card Card
	err = s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if err := s.checkParentTx(tx, nc.Parent, wf.Name); err != nil {
			return err
		}
		var id string
		var idErr error
		if nc.Parent != "" {
			id, idErr = s.nextChildID(tx, nc.Parent)
		} else {
			prefix, prefixErr := s.cardPrefixTx(tx, nc.Project)
			if prefixErr != nil {
				return prefixErr
			}
			id, idErr = s.nextTopID(tx, prefix)
		}
		if idErr != nil {
			log().Error("建卡取号失败", "project", nc.Project, "parent", nc.Parent, "cause", idErr)
			return idErr
		}
		card, err = s.insertCardTx(tx, sink, id, nc, wf, nil)
		if err != nil {
			log().Error("建卡落库失败", "card", id, "project", nc.Project, "cause", err)
			return err
		}
		log().Info("建卡完成", "card", card.ID, "project", card.Project, "workflow", card.WorkflowName)
		return err
	})
	if err != nil {
		log().Warn("建卡失败", "project", nc.Project, "title", nc.Title, "parent", nc.Parent, "cause", err)
	}
	return card, err
}

// importIDPat 导入卡号的合法形态：顶层 <前缀><数字>，或点号子卡
// <前缀><数字>(.<数字>)+；前缀为 1~4 个大写 ASCII 字母。
var importIDPat = regexp.MustCompile(`^[A-Z]{1,4}\d+(\.\d+)*$`)

// ImportCard 显式 ID 导入建卡：携带既有带前缀卡号（markdown 存量总账的行、
// 搁置条目复活）原号迁入账本。这是永久能力，不是一次性迁移脚本。
//
// 参数：id 目标卡号（顶层 "B153"/"C1" 或点号子卡 "C1.1"，父卡 id 由点号前缀
// 推导，nc.Parent 无需也不应自行填写）；source 导入来源标注（落进
// card_created 事件负载，空串记为「手工导入」）；nc 卡字段（标题、project
// 必填；优先级缺省「中」；工作流为空时按账本唯一流规则解析）。
// 返回：建成的卡；目标号已存在返回包装 ErrBadState 的错误。
//
// 注意：
//   - **导入不受 min_b 水位约束**——min_b 只约束自动取号（nextTopID），
//     导入一律按原号落位；导入号高于水位时，后续自动取号会自然跳过它
//     （nextTopID 取「现存最大号」与 min_b 的较大者 +1）。
//   - 点号子卡要求父卡**已存在**，缺父直接拒，不自动补建——补建等于
//     替用户猜父卡的标题与状态，猜错比报错难发现。
//   - 除 card_created 事件多带 imported/import_source 两个字段外，导入卡
//     与 CreateCard 建的卡零行为差别：同样钉工作流最新版本、初始态 =
//     工作流首态、子卡同样在父卡 timeline 留痕。
func (s *Store) ImportCard(id, source string, nc NewCard) (Card, error) {
	id = strings.TrimSpace(id)
	if !importIDPat.MatchString(id) {
		return Card{}, fmt.Errorf("导入卡: id %q 形如 B153、C1 或 C1.1", id)
	}
	if strings.TrimSpace(source) == "" {
		source = "手工导入"
	}
	// 父卡由点号前缀推导：id 自己就带着归属，再让调用方传一遍 Parent
	// 只会多出「两者不一致时听谁的」这种没有正确答案的问题。
	if idx := strings.LastIndex(id, "."); idx >= 0 {
		nc.Parent = id[:idx]
	} else {
		nc.Parent = ""
	}
	wf, err := s.prepareCard(&nc)
	if err != nil {
		return Card{}, err
	}
	var card Card
	err = s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, id); err == nil {
			log().Warn("导入被拒：目标卡号已存在", "card", id)
			return fmt.Errorf("卡 %s 已存在，导入拒绝覆盖: %w", id, ErrBadState)
		} else if !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("导入查重 %s: %w", id, err)
		}
		if err := s.checkParentTx(tx, nc.Parent, wf.Name); err != nil {
			return err
		}
		card, err = s.insertCardTx(tx, sink, id, nc, wf,
			map[string]any{"imported": true, "import_source": source})
		if err != nil {
			return err
		}
		log().Info("导入卡完成", "card", id, "workflow", wf.Name, "source", source)
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

// AttachFile 挂附件（同 kind、path 二元组幂等）；返回是否实际新增。
// 落 comment 事件记录动作，附件本体是卡字段不是事件。
//
// 注意：返回 true 只表示事务已提交并新增了条目；重复挂载返回 false、nil。
func (s *Store) AttachFile(id, kind, path, actor string) (bool, error) {
	log().Info("挂附件进入", "card", id, "kind", kind, "path", path, "actor", actor)
	added := false
	if err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		card, err := getCardTx(s, tx, id)
		if err != nil {
			log().Warn("挂附件失败：读取卡", "card", id, "kind", kind, "path", path, "cause", err)
			return fmt.Errorf("挂附件: 卡 %s: %w", id, err)
		}
		for _, attachment := range card.Attachments {
			if attachment.Kind == kind && attachment.Path == path {
				return nil // 幂等
			}
		}
		card.Attachments = append(card.Attachments, Attachment{Kind: kind, Path: path})
		added = true
		raw, _ := json.Marshal(card.Attachments)
		if _, err := tx.Exec(s.q(`UPDATE cards SET attachments = ?, updated_at = ? WHERE id = ?`),
			string(raw), s.tval(time.Now()), id); err != nil {
			log().Error("挂附件失败：写附件", "card", id, "kind", kind, "path", path, "cause", err)
			return fmt.Errorf("写附件: %w", err)
		}
		_, err = s.appendEvent(tx, sink, id, EvComment, actor,
			map[string]any{"kind": "普通", "body": fmt.Sprintf("挂附件 %s:%s", kind, path)})
		if err != nil {
			log().Error("挂附件失败：落事件", "card", id, "kind", kind, "path", path, "cause", err)
			return err
		}
		log().Info("挂附件完成", "card", id, "kind", kind, "path", path, "actor", actor)
		return nil
	}); err != nil {
		return false, err
	}
	return added, nil
}

// DetachFile 摘附件。selector 命中卡上已有的 kind:path 时只摘该二元组；
// 否则把 selector 当作裸 path，摘掉该路径的全部附件。两种形态都落 comment
// 事件，附件本体是卡字段不是事件。返回同一事务内摘掉的条目清单。
func (s *Store) DetachFile(id, selector, actor string) ([]Attachment, error) {
	log().Info("摘附件进入", "card", id, "selector", selector, "actor", actor)
	var removed []Attachment
	if err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		card, err := getCardTx(s, tx, id)
		if err != nil {
			log().Warn("摘附件失败：读取卡", "card", id, "selector", selector, "cause", err)
			return fmt.Errorf("摘附件: 卡 %s: %w", id, err)
		}
		kind, path, exact := detachSelector(card.Attachments, selector)
		kept := card.Attachments[:0]
		removed = make([]Attachment, 0, 1)
		for _, attachment := range card.Attachments {
			match := attachment.Path == path
			if exact {
				match = len(removed) == 0 && attachment.Kind == kind && attachment.Path == path
			}
			if match {
				removed = append(removed, attachment)
				continue
			}
			kept = append(kept, attachment)
		}
		raw, _ := json.Marshal(kept)
		if _, err := tx.Exec(s.q(`UPDATE cards SET attachments = ?, updated_at = ? WHERE id = ?`),
			string(raw), s.tval(time.Now()), id); err != nil {
			log().Error("摘附件失败：写附件", "card", id, "selector", selector, "removed", removed, "cause", err)
			return fmt.Errorf("写附件: %w", err)
		}
		_, err = s.appendEvent(tx, sink, id, EvComment, actor,
			map[string]any{"kind": "普通", "body": "摘附件 " + selector})
		if err != nil {
			log().Error("摘附件失败：落事件", "card", id, "selector", selector, "removed", removed, "cause", err)
			return err
		}
		log().Info("摘附件完成", "card", id, "selector", selector, "exact", exact,
			"count", len(removed), "attachments", removed, "actor", actor)
		return nil
	}); err != nil {
		return nil, err
	}
	return removed, nil
}

// detachSelector 解析 detach 的双形态。只有卡上确实存在 kind:path 精确项时
// 才进入精确模式；否则保留旧语义，把完整 selector 当作 path。
func detachSelector(attachments []Attachment, selector string) (kind, path string, exact bool) {
	kind, path, hasKind := strings.Cut(selector, ":")
	if !hasKind {
		return "", selector, false
	}
	for _, attachment := range attachments {
		if attachment.Kind == kind && attachment.Path == path {
			return kind, path, true
		}
	}
	return "", selector, false
}

// AcceptanceInFlightNotice 是新判据对已启动轮次的明确影响说明。
const AcceptanceInFlightNotice = "本次修改对正在跑的轮次无效，将从下一轮 `card dispatch --step` 生效"

// SetAcceptance 写验收判据文本（判据是卡字段；验收**结果**走
// RecordAcceptance 落事件，Task 8）。写入成功后查询挂账 task 的镜像实况；
// 这是唯一能同时覆盖 CLI 与 HTTP 的写点，也让新判据不会静默影响已经启动的轮次。
// 查询到在飞 task 时，先落原有更新评论，再以既有评论事件记录影响提示。
func (s *Store) SetAcceptance(id, criteria, actor string) error {
	log().Info("更新验收判据进入", "card", id, "actor", actor, "criteria_bytes", len(criteria))
	if err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
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
	}); err != nil {
		log().Error("更新验收判据失败", "card", id, "actor", actor, "cause", err)
		return err
	}

	states, err := s.LatestTaskStates(id)
	if err != nil {
		log().Error("判据已写入但读取在飞 task 失败", "card", id, "actor", actor, "cause", err)
		return nil
	}
	liveIDs := make([]string, 0, len(states))
	for _, state := range states {
		if state.LastType != "archived" && state.LastType != "failed" {
			liveIDs = append(liveIDs, state.Target+"/"+state.TaskID)
		}
	}
	if len(liveIDs) == 0 {
		log().Info("验收判据更新完成，无在飞 task", "card", id, "actor", actor)
		return nil
	}

	body := AcceptanceInFlightNotice + "：" + strings.Join(liveIDs, "、")
	if _, err := s.AddComment(id, body, "普通", actor); err != nil {
		log().Error("判据已写入但在飞提示落账失败", "card", id, "actor", actor,
			"tasks", liveIDs, "cause", err)
		return fmt.Errorf("写在飞判据提示: %w", err)
	}
	log().Warn("更新验收判据影响在飞轮次", "card", id, "actor", actor, "tasks", liveIDs)
	return nil
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

// SetCardBaseBranch 为尚未出现任何 dispatched 事件的卡设置或清除显式基线。
// id 是卡号，branch 非空为显式分支、空串清除自身覆盖值，actor 是审计主体。
// 首次派发判定、cards 更新和 EvComment 必须在同一个 mutate 事务内完成。
func (s *Store) SetCardBaseBranch(id, branch, actor string) error {
	log().Info("设置卡基线进入", "card", id, "branch", branch, "actor", actor)
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, id); err != nil {
			log().Warn("设置卡基线失败：卡不存在或读取失败", "card", id, "cause", err)
			return fmt.Errorf("设置卡基线：卡 %s: %w", id, err)
		}
		var raw string
		var createdAt any
		err := tx.QueryRow(s.q(`SELECT payload, created_at FROM card_events
			WHERE card_id = ? AND type = ? ORDER BY seq ASC LIMIT 1`), id, EvDispatched).Scan(&raw, &createdAt)
		switch {
		case err == nil:
			var snapshot DispatchSnapshot
			if decodeErr := json.Unmarshal([]byte(raw), &snapshot); decodeErr != nil {
				log().Error("首条 dispatched payload 损坏", "card", id, "cause", decodeErr)
				return fmt.Errorf("卡 %s 的首次派发快照损坏: %w", id, ErrBadState)
			}
			at := toTime(createdAt).Format(time.RFC3339Nano)
			log().Warn("设置卡基线被拒：基线已冻结", "card", id,
				"first_branch", snapshot.Branch, "first_dispatched_at", at, "actor", actor)
			return fmt.Errorf("卡 %s 已在分支 %q 于 %s 首次派发，基线已冻结: %w",
				id, snapshot.Branch, at, ErrBadState)
		case !errors.Is(err, sql.ErrNoRows):
			log().Error("查询 dispatched 事件失败", "card", id, "cause", err)
			return fmt.Errorf("查询卡 %s 的 dispatched 事件: %w", id, err)
		}
		now := s.timeNow()
		if _, err := tx.Exec(s.q(`UPDATE cards SET base_branch = ?, updated_at = ? WHERE id = ?`),
			branch, s.tval(now), id); err != nil {
			log().Error("写 cards 基线失败", "card", id, "branch", branch, "cause", err)
			return fmt.Errorf("写卡 %s 基线: %w", id, err)
		}
		payload := map[string]any{"kind": "普通", "base_branch": branch,
			"body": fmt.Sprintf("更新卡基线：%q", branch)}
		if _, err := s.appendEventAt(tx, sink, id, EvComment, actor, payload, now); err != nil {
			log().Error("落基线 comment 失败", "card", id, "branch", branch, "cause", err)
			return fmt.Errorf("记录卡 %s 基线变更: %w", id, err)
		}
		log().Info("设置卡基线完成", "card", id, "branch", branch, "actor", actor)
		return nil
	})
	if err != nil {
		log().Warn("设置卡基线未提交", "card", id, "branch", branch, "cause", err)
	}
	return err
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
