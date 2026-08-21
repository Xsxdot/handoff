// Workflow 聚合：不可变版本化的状态机形状。只插新版本、永不 UPDATE
// 旧行——钉版本的卡随时能取回当时的形状，这是审计链的前提。
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/discipline"
)

// withStatesFromNodes 把 Nodes 投影成 States/Gates（写入侧）。
//
// 参数：无（值接收者）。返回：投影后的副本；Nodes 为空时原样返回。
// 注意：会**覆盖**调用方传入的 States/Gates——Nodes 在场时它们是派生物，
// 允许两者不一致等于允许账本自相矛盾。
func (d WorkflowDef) withStatesFromNodes() WorkflowDef {
	if len(d.Nodes) == 0 {
		return d
	}
	states := make([]string, 0, len(d.Nodes))
	gates := make(map[string]Gate, len(d.Nodes))
	for _, node := range d.Nodes {
		states = append(states, node.Name)
		if node.Gate != (Gate{}) {
			gates[node.Name] = node.Gate
		}
	}
	d.States = states
	if len(gates) == 0 {
		gates = nil
	}
	d.Gates = gates
	return d
}

// withNodesFromStates 为只有 States 的老 def 补出等价节点序列（读取侧）。
//
// 参数：无（值接收者）。返回：补齐 Nodes 的副本；Nodes 非空时原样返回。
// 补出的节点全部是纯人工列（所有能力开关关闭），Next 按 States 顺序串起来，
// 末节点无 Next——与老 def 在界面上的行为完全一致。
func (d WorkflowDef) withNodesFromStates() WorkflowDef {
	if len(d.Nodes) > 0 || len(d.States) == 0 {
		return d
	}
	nodes := make([]NodeDef, 0, len(d.States))
	for i, state := range d.States {
		node := NodeDef{Name: state, Gate: d.Gates[state]}
		if i+1 < len(d.States) {
			node.Next = d.States[i+1]
		}
		nodes = append(nodes, node)
	}
	d.Nodes = nodes
	return d
}

// countDispatchNodes 数带派发能力的节点，只用于日志——「这条流有几列会自动跑」
// 是排查「卡为什么不动」时第一个要看的数。
func countDispatchNodes(nodes []NodeDef) int {
	n := 0
	for _, node := range nodes {
		if node.Dispatch {
			n++
		}
	}
	return n
}

// validateNodes 校验节点序列的内部一致性。
//
// 参数：nodes 节点序列（可为空，空 = 老 def 形态，不校验）。
// 返回：第一处违规的错误（包装 ErrBadState，供 HTTP 层翻成 400）；全部合法返回 nil。
//
// 校验范围**刻意只覆盖 Store 看得见的东西**：模板存在性能查（同一个库），
// 纪律块存在性查不了（正文在 agentd 的 DataDir 下，Store 不认识文件系统），
// 那一项留给派发时报错。把校验硬塞进来只会让 Store 依赖 agentd 的目录布局。
func (s *Store) validateNodes(nodes []NodeDef) error {
	if len(nodes) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		if node.Name == "" {
			return fmt.Errorf("节点名不能为空: %w", ErrBadState)
		}
		if seen[node.Name] {
			return fmt.Errorf("节点名 %q 重复: %w", node.Name, ErrBadState)
		}
		seen[node.Name] = true
	}
	for _, node := range nodes {
		// Verdict 蕴含 Dispatch：没有派发就没有报文，也就没有裁决块可解析。
		if node.Verdict && !node.Dispatch {
			return fmt.Errorf("节点 %q 开了 Verdict 却没开 Dispatch（没有派发就没有报文可裁决）: %w",
				node.Name, ErrBadState)
		}
		if node.Dispatch && node.Template == "" {
			return fmt.Errorf("节点 %q 开了 Dispatch 但没写模板: %w", node.Name, ErrBadState)
		}
		if node.Dispatch {
			if _, err := s.GetTemplate(node.Template, 0); err != nil {
				return fmt.Errorf("节点 %q 引用的模板 %q 不可用: %w", node.Name, node.Template, ErrBadState)
			}
		}
		if node.MaxRounds < 0 {
			return fmt.Errorf("节点 %q 的 MaxRounds 不能为负: %w", node.Name, ErrBadState)
		}
		if node.MaxRounds > 0 && !node.Verdict {
			return fmt.Errorf("节点 %q 设了 MaxRounds 却没开 Verdict（不裁决就没有轮次）: %w",
				node.Name, ErrBadState)
		}
		if node.OnFail != "" && !node.Verdict {
			return fmt.Errorf("节点 %q 设了 OnFail 却没开 Verdict（不裁决就没有失败分支）: %w",
				node.Name, ErrBadState)
		}
		// 路由按名字指向，悬空的名字会让卡走到一半停住且看不出原因，写入时就拦掉。
		for label, to := range map[string]string{"Next": node.Next, "OnFail": node.OnFail} {
			if to == "" || seen[to] {
				continue
			}
			return fmt.Errorf("节点 %q 的 %s 指向不存在的节点 %q: %w",
				node.Name, label, to, ErrBadState)
		}
	}
	return nil
}

// PutWorkflow 写入 name 的下一个版本并返回版本号。Nodes 在写入前投影为 States/Gates。
func (s *Store) PutWorkflow(name string, def WorkflowDef) (int, error) {
	def = def.withStatesFromNodes()
	if err := s.validateNodes(def.Nodes); err != nil {
		log().Warn("工作流节点校验未过", "name", name, "cause", err)
		return 0, err
	}
	raw, err := json.Marshal(def)
	if err != nil {
		return 0, fmt.Errorf("编码工作流定义: %w", err)
	}
	var version int
	err = s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		row := tx.QueryRow(s.q(`SELECT COALESCE(MAX(version),0) FROM workflows WHERE name = ?`), name)
		if err := row.Scan(&version); err != nil {
			return fmt.Errorf("查最大版本: %w", err)
		}
		version++
		_, err := tx.Exec(s.q(`INSERT INTO workflows (name, version, definition, created_at) VALUES (?,?,?,?)`),
			name, version, string(raw), s.tval(time.Now()))
		if err != nil {
			return fmt.Errorf("写工作流 %s v%d: %w", name, version, err)
		}
		log().Info("写入工作流版本", "name", name, "version", version,
			"nodes", len(def.Nodes), "dispatch_nodes", countDispatchNodes(def.Nodes))
		return nil
	})
	return version, err
}

// GetWorkflow 取指定版本；version==0 取最新版。找不到返回 ErrNotFound。
func (s *Store) GetWorkflow(name string, version int) (Workflow, error) {
	q := `SELECT name, version, definition, created_at FROM workflows WHERE name = ?`
	args := []any{name}
	if version > 0 {
		q += ` AND version = ?`
		args = append(args, version)
	}
	q += ` ORDER BY version DESC LIMIT 1`
	row := s.db.QueryRow(s.q(q), args...)
	var workflow Workflow
	var raw string
	var createdAt any
	if err := row.Scan(&workflow.Name, &workflow.Version, &raw, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Workflow{}, fmt.Errorf("工作流 %s v%d: %w", name, version, ErrNotFound)
		}
		return Workflow{}, fmt.Errorf("读工作流: %w", err)
	}
	if err := jsonUnmarshal(raw, &workflow.Def); err != nil {
		return Workflow{}, fmt.Errorf("解码工作流定义: %w", err)
	}
	workflow.Def = workflow.Def.withNodesFromStates()
	if len(workflow.Def.Nodes) > 0 && len(workflow.Def.Nodes) == len(workflow.Def.States) {
		log().Debug("读出工作流", "name", workflow.Name, "version", workflow.Version, "nodes", len(workflow.Def.Nodes))
	}
	workflow.CreatedAt = toTime(createdAt)
	return workflow, nil
}

// EnsureDefaultWorkflows 幂等 seed 出厂工作流。已存在同名的不动（不覆盖用户改过的版本）。
//
// 注意：**本方法会先调 EnsureDefaultTemplates**。出厂工作流的节点引用出厂模板，
// 而 PutWorkflow 会校验模板存在性——把顺序要求写进文档等于把它留给每个调用点
// 各自记住，仓库里 11 处调用点原本全是反的。两个 seed 都幂等，合并调用没有代价。
func (s *Store) EnsureDefaultWorkflows() error {
	if err := s.EnsureDefaultTemplates(); err != nil {
		return fmt.Errorf("seed 出厂工作流前置的模板: %w", err)
	}
	// 出厂工作流是**数据不是代码语义**：用户在控制台改它、删它、重排它都行，
	// seed 只保证「装完就有一条能跑通的流」。这里刻意不引入任何「节点类型」
	// 概念——每一列的行为都由下面这些能力开关组合出来。
	defaults := map[string]WorkflowDef{
		"feature": {
			Nodes: []NodeDef{
				{Name: StatusTodo, Next: "已出spec"},
				{Name: "已出spec", Next: StatusDoing, Gate: Gate{RequireAttachment: "spec"}},
				{Name: StatusDoing, Next: StatusReview,
					Dispatch: true, Template: "feature-impl", CarryCardContext: true},
				{Name: StatusReview, Dispatch: true, Verdict: true, Template: "review-generic",
					CarryCardContext: true, MaxRounds: 3,
					Next: "待合并", OnFail: StatusDoing},
				{Name: "待合并", Next: StatusDone, Gate: Gate{RequireAcceptance: true},
					Dispatch: true, Verdict: true, Template: "review-generic",
					Override:         NodeOverride{Discipline: discipline.NameFinishing},
					CarryCardContext: true, MaxRounds: 1,
					// 出厂默认不自动往主线合：往 main 合是外部可见且不易撤回的
					// 动作，留一道人工门。想让它全自动的用户把这个清单清空即可。
					HumanBases: []string{"main"},
				},
				{Name: StatusDone},
			},
		},
		// domain：分域开发协议（docs/superpowers/specs/2026-08-21-domain-
		// partitioned-dev-protocol-design.md §8.1）的执行形态。节点归属遵循
		// 工作台基准 §5：拆解草案与代码执行归执行者，拍板/扇出/合并归人。
		"domain": {
			Nodes: []NodeDef{
				{Name: StatusTodo, Next: "拆解"},
				// 拆解：只派发不裁决。产出（域清单/契约增量/子卡清单）的拍板
				// 归人——人工把卡移进契约冻结这一步就是拍板动作，附上拍板过
				// 的契约（kind=contract）才能过下一列的闸。
				{Name: "拆解", Next: "契约冻结",
					Dispatch: true, Template: "domain-breakdown", CarryCardContext: true},
				// 契约冻结：把拍板过的契约落成可编译骨架 commit。重跑分支已
				// 按 purpose 轮次挂号（Task 1），MaxRounds 2 不会撞分支名。
				{Name: "契约冻结", Next: "域实现",
					Gate:     Gate{RequireAttachment: "contract"},
					Dispatch: true, Verdict: true, Template: "domain-ticket0",
					CarryCardContext: true, MaxRounds: 2},
				// 域实现：纯人工列。扇出子卡是驱动 handoff 自身的操作（纪律块
				// 对执行者禁止），归协调者；子卡各绑自己的工作流并行走。
				{Name: "域实现", Next: "集成"},
				// 集成：聚合闸拦到全部直接子卡完结；裁决未过退回域实现补卡。
				{Name: "集成", Next: "终审", OnFail: "域实现",
					Gate:     Gate{RequireChildrenDone: true},
					Dispatch: true, Verdict: true, Template: "domain-integration",
					CarryCardContext: true, MaxRounds: 2},
				// 终审：整分支审阅 + 收尾合并，与 feature 流「待合并」同形；
				// 基线是 main 时不自动执行——外部可见动作留人工门。
				{Name: "终审", Next: StatusDone,
					Gate:     Gate{RequireAcceptance: true},
					Dispatch: true, Verdict: true, Template: "review-generic",
					Override:         NodeOverride{Discipline: discipline.NameFinishing},
					CarryCardContext: true, MaxRounds: 1,
					HumanBases: []string{"main"}},
				{Name: StatusDone},
			},
		},
		"bug": {
			Nodes: []NodeDef{
				{Name: StatusTodo, Next: StatusDoing},
				{Name: StatusDoing, Next: StatusReview,
					Dispatch: true, Template: "feature-impl", CarryCardContext: true},
				{Name: StatusReview, Dispatch: true, Verdict: true, Template: "review-generic",
					CarryCardContext: true, MaxRounds: 3, Next: StatusDone, OnFail: StatusDoing},
				{Name: StatusDone},
			},
		},
		"triage": {
			Nodes: []NodeDef{
				{Name: StatusTodo, Next: "定性中"},
				{Name: "定性中", Next: "已定性"},
				{Name: "已定性"},
			},
		},
	}
	for name, def := range defaults {
		if _, err := s.GetWorkflow(name, 0); err == nil {
			continue // 已存在，不覆盖
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if _, err := s.PutWorkflow(name, def); err != nil {
			log().Error("seed 默认工作流失败", "name", name, "cause", err)
			return fmt.Errorf("seed 默认工作流 %s: %w", name, err)
		}
		log().Info("seed 默认工作流", "name", name)
	}
	return nil
}

// ListWorkflowNames 全部工作流名（去重升序）。
func (s *Store) ListWorkflowNames() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT name FROM workflows ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("列工作流名: %w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// MigrateCardWorkflow 把卡显式迁到目标工作流、版本和状态列。Version==0
// 取事务内目标流最新版；目标流和状态列都必须由调用方显式提供。
func (s *Store) MigrateCardWorkflow(cardID, toWorkflow string, toVersion int, toStatus, actor string) error {
	if strings.TrimSpace(toWorkflow) == "" || strings.TrimSpace(toStatus) == "" {
		return fmt.Errorf("迁移目标工作流和状态列不能为空: %w", ErrBadState)
	}
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		card, err := getCardTx(s, tx, cardID)
		if err != nil {
			log().Error("迁移读取原位置失败", "card", cardID, "cause", err)
			return fmt.Errorf("迁工作流: 卡 %s: %w", cardID, err)
		}
		// 门禁必须和 UPDATE 共用这个事务：否则 CLI/HTTP 入口之间会有
		// TOCTOU 窗口，且两入口不会共享同一道在飞判定（契约拍板记录④）。
		inFlight, taskID, err := s.cardStepInFlightTx(tx, cardID)
		if err != nil {
			return fmt.Errorf("迁移检查卡 %s 在飞状态: %w", cardID, err)
		}
		if inFlight {
			log().Warn("迁移被拒：卡环节仍在飞", "card", cardID, "task", taskID,
				"to_workflow", toWorkflow, "to_status", toStatus)
			return fmt.Errorf("卡 %s 的任务 %s 仍在运行，不能迁移: %w", cardID, taskID, ErrStepInFlight)
		}
		if toVersion == 0 {
			if err := tx.QueryRow(s.q(`SELECT COALESCE(MAX(version), 0) FROM workflows WHERE name = ?`), toWorkflow).Scan(&toVersion); err != nil {
				return fmt.Errorf("取目标工作流 %q 最新版本: %w", toWorkflow, err)
			}
		}
		target, err := s.getWorkflowTx(tx, toWorkflow, toVersion)
		if err != nil {
			return err
		}
		found := toStatus == StatusClosed // 终止态卡不受 States 约束
		for _, state := range target.Def.States {
			if state == toStatus {
				found = true
				break
			}
		}
		if !found {
			log().Warn("迁移被拒：状态悬空", "card", cardID, "status", toStatus, "to_workflow", toWorkflow, "to_version", toVersion)
			return fmt.Errorf("卡 %s 当前状态 %q 不在 %s v%d 中，先转移状态再迁: %w",
				cardID, toStatus, toWorkflow, toVersion, ErrBadState)
		}
		// 原位置必须在 UPDATE 之前从事务内读出；写完后 cards 只剩目标值，
		// 再读会丢失审计事件需要的 from_*。
		from := WorkflowLocation{Workflow: card.WorkflowName, Version: card.WorkflowVersion, Status: card.Status}
		to := WorkflowLocation{Workflow: toWorkflow, Version: toVersion, Status: toStatus}
		if _, err := tx.Exec(s.q(`UPDATE cards SET workflow_name = ?, workflow_version = ?, status = ?, updated_at = ? WHERE id = ?`),
			toWorkflow, toVersion, toStatus, s.tval(time.Now()), cardID); err != nil {
			return fmt.Errorf("写迁移: %w", err)
		}
		_, err = s.appendEvent(tx, sink, cardID, EvWorkflowMigrated, actor, map[string]any{
			"from_workflow": from.Workflow,
			"from_version":  from.Version,
			"from_status":   from.Status,
			"to_workflow":   to.Workflow,
			"to_version":    to.Version,
			"to_status":     to.Status,
		})
		if err != nil {
			return err
		}
		log().Info("工作流迁移完成", "card", cardID,
			"from_workflow", from.Workflow, "from_version", from.Version, "from_status", from.Status,
			"to_workflow", to.Workflow, "to_version", to.Version, "to_status", to.Status)
		return nil
	})
}

// jsonUnmarshal 统一 JSON 解码错误措辞。
func jsonUnmarshal(raw string, v any) error {
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return fmt.Errorf("解码 JSON 定义: %w", err)
	}
	return nil
}
