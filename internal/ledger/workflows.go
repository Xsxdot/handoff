// Workflow 聚合：不可变版本化的状态机形状。只插新版本、永不 UPDATE
// 旧行——钉版本的卡随时能取回当时的形状，这是审计链的前提。
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

var defaultBoardColumns = []string{"代办", "沟通中", "进行中", "审核中", "结束"}

// DefaultBoardLayout 返回给定状态集合的默认五列看板映射。
// 未登记状态落入「进行中」，使旧工作流和新状态都能被诚实呈现。
func DefaultBoardLayout(states []string) proto.BoardLayout {
	mapping := map[string]string{
		"待办": "代办", "已出spec": "沟通中", "已出 spec": "沟通中",
		"进行中": "进行中", "待审阅": "审核中", "待合并": "审核中",
		"已完成": "结束", "终止": "结束",
	}
	for _, state := range states {
		if _, ok := mapping[state]; !ok {
			mapping[state] = "进行中"
		}
	}
	return proto.BoardLayout{
		Columns:       append([]string(nil), defaultBoardColumns...),
		StateToColumn: mapping, Fallback: "进行中",
	}
}

func validateBoardLayout(layout *proto.BoardLayout) error {
	if layout == nil {
		return nil
	}
	if len(layout.Columns) != 5 {
		return fmt.Errorf("看板列必须恰好五列: %w", ErrBadState)
	}
	seen := make(map[string]bool, len(layout.Columns))
	for _, column := range layout.Columns {
		if strings.TrimSpace(column) == "" || seen[column] {
			return fmt.Errorf("看板列名必须非空且唯一: %q: %w", column, ErrBadState)
		}
		seen[column] = true
	}
	if !seen[layout.Fallback] {
		return fmt.Errorf("看板兜底列 %q 不在列序中: %w", layout.Fallback, ErrBadState)
	}
	for state, column := range layout.StateToColumn {
		if !seen[column] {
			return fmt.Errorf("状态 %q 映射到不存在的看板列 %q: %w", state, column, ErrBadState)
		}
	}
	return nil
}

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
		// 用 DeepEqual 而不是 != 或手写 isEmpty()：Gate 自带了 slice 字段
		//（RequireAttachmentAny）已不可比较，而手写的空判定有个静默失败模式
		// ——将来给 Gate 加字段却忘了更新它，只设了新字段的 gate 会被判成空、
		// 悄悄不登记进 Gates，门就此无声失效。DeepEqual 对加字段免疫。
		// 本函数每次载入工作流只跑一次、节点是个位数，反射开销无关紧要。
		if !reflect.DeepEqual(node.Gate, Gate{}) {
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

// countProducesNodes 数带单一附件产出声明的节点，只用于写入成功日志。
func countProducesNodes(nodes []NodeDef) int {
	n := 0
	for _, node := range nodes {
		if node.Produces != nil {
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
		if node.Produces != nil {
			if strings.TrimSpace(node.Produces.Kind) == "" || strings.TrimSpace(node.Produces.Path) == "" {
				return fmt.Errorf("节点 %q 的 produces 必须同时填写 kind 和 path: %w",
					node.Name, ErrBadState)
			}
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
	if err := validateBoardLayout(def.Board); err != nil {
		log().Warn("工作流看板布局校验未过", "name", name, "cause", err)
		return 0, err
	}
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
			"nodes", len(def.Nodes), "dispatch_nodes", countDispatchNodes(def.Nodes),
			"produces_nodes", countProducesNodes(def.Nodes), "board_columns", boardColumnCount(def.Board))
		return nil
	})
	return version, err
}

func boardColumnCount(board *proto.BoardLayout) int {
	if board == nil {
		return 0
	}
	return len(board.Columns)
}

// GetWorkflow 取指定版本；version==0 取最新版。找不到返回 ErrNotFound。
func (s *Store) GetWorkflow(name string, version int) (Workflow, error) {
	workflow, err := s.getWorkflowStored(name, version)
	if err != nil {
		return Workflow{}, err
	}
	workflow.Def = workflow.Def.withNodesFromStates()
	if len(workflow.Def.Nodes) > 0 && len(workflow.Def.Nodes) == len(workflow.Def.States) {
		log().Debug("读出工作流", "name", workflow.Name, "version", workflow.Version, "nodes", len(workflow.Def.Nodes))
	}
	return workflow, nil
}

// getWorkflowStored 读取工作流存储的原始定义，不做老 def 的 Nodes 投影。
//
// 参数：name 工作流名；version==0 取最新版。返回：数据库中的 Workflow，
// 找不到返回 ErrNotFound。注意：仅供 Store 内部判定存储形态；对外读取必须
// 使用 GetWorkflow，保持老 def 也能被消费方当作节点形态使用的兼容语义。
func (s *Store) getWorkflowStored(name string, version int) (Workflow, error) {
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
	workflow.CreatedAt = toTime(createdAt)
	return workflow, nil
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

// MigrateCardWorkflow 把卡显式迁到目标工作流、版本和状态列，并返回迁移审计投影。
// Version==0 取事务内目标流最新版；目标流和状态列都必须由调用方显式提供。
func (s *Store) MigrateCardWorkflow(cardID, toWorkflow string, toVersion int, toStatus, actor string) (WorkflowMigration, error) {
	var migration WorkflowMigration
	if strings.TrimSpace(toWorkflow) == "" || strings.TrimSpace(toStatus) == "" {
		return migration, fmt.Errorf("迁移目标工作流和状态列不能为空: %w", ErrBadState)
	}
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
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
		// 迁移也是进入目标列，必须在同一事务内复用目标列 gate；否则「先迁到
		// 无闸流再迁回」会绕过门禁（拆解 §4.4）。
		if err := s.checkWorkflowGateTx(tx, card, target, toStatus, "迁移"); err != nil {
			return err
		}
		// 原位置必须在 UPDATE 之前从事务内读出；写完后 cards 只剩目标值，
		// 再读会丢失审计事件需要的 from_*。
		from := WorkflowLocation{Workflow: card.WorkflowName, Version: card.WorkflowVersion, Status: card.Status}
		to := WorkflowLocation{Workflow: toWorkflow, Version: toVersion, Status: toStatus}
		if _, err := tx.Exec(s.q(`UPDATE cards SET workflow_name = ?, workflow_version = ?, status = ?, updated_at = ? WHERE id = ?`),
			toWorkflow, toVersion, toStatus, s.tval(time.Now()), cardID); err != nil {
			return fmt.Errorf("写迁移: %w", err)
		}
		payload := map[string]any{
			"from_workflow": from.Workflow,
			"from_version":  from.Version,
			"from_status":   from.Status,
			"to_workflow":   to.Workflow,
			"to_version":    to.Version,
			"to_status":     to.Status,
		}
		eventAt := time.Now().UTC()
		seq, err := s.appendEventAt(tx, sink, cardID, EvWorkflowMigrated, actor, payload, eventAt)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("编码迁移事件投影: %w", err)
		}
		migration = WorkflowMigration{
			CardID: cardID,
			From:   from,
			To:     to,
			Event: Event{
				Seq: seq, CardID: cardID, Type: EvWorkflowMigrated, Actor: actor,
				Payload: raw, CreatedAt: eventAt,
			},
		}
		log().Info("工作流迁移完成", "card", cardID,
			"from_workflow", from.Workflow, "from_version", from.Version, "from_status", from.Status,
			"to_workflow", to.Workflow, "to_version", to.Version, "to_status", to.Status)
		return nil
	})
	return migration, err
}

// jsonUnmarshal 统一 JSON 解码错误措辞。
func jsonUnmarshal(raw string, v any) error {
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return fmt.Errorf("解码 JSON 定义: %w", err)
	}
	return nil
}
