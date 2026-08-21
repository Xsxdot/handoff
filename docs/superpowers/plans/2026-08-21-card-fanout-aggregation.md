# 子卡扇出聚合闸与递归护栏 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让父卡能「等全部子卡完结」再过节点（聚合闸），子卡状态在父卡与账本上可见，并给递归拆解加嵌套深度护栏——spec `docs/superpowers/specs/2026-08-21-domain-partitioned-dev-protocol-design.md` §8.2/§8.3 的引擎半边。

**Architecture:** 全部沿现有形状扩展，不引入新表、新事件类型、新端点：聚合闸是 `Gate` 的第三个字段（与 RequireAttachment/RequireAcceptance 并列，在 `moveCardTx` 同一处判）；子卡计数是 `CardView` 的查询期派生标记（derived.go 现算，不落列）；父子边入账本复用 `EvComment`+refs（AddBlocks 同款）；递归护栏是 `CreateCard` 事务内沿父链数同名工作流的前置校验。已存在且不动：子卡建卡（`NewCard.Parent`、点号 ID）、父链（`ancestorsTx`）、基线继承（`EffectiveBaseBranch`）、抽屉子任务区（detail 端点已返回 `children`）。

**Tech Stack:** Go（database/sql + 自带 `s.q`/`s.tval`/`mutate` 事务模式、slog 经 `log()`）、React+TS（vitest + @testing-library/react）。

## Global Constraints

- SQL 一律 `s.q(...)` 占位符转换、时间 `s.tval(...)`、写操作进 `s.mutate(func(tx, sink))`——照抄包内现有写法。
- 日志用包内 `log()`（slog），拒绝路径 Warn 且带卡号上下文；**禁止 `fmt.Printf`**。
- 哨兵错误：闸不过 wrap `ErrGateBlocked`、状态类拒绝 wrap `ErrBadState`（HTTP 层靠 `errors.Is` 翻译，裸 error 会变 500）。
- 注释中文、解释「为什么」；新导出符号必须有 doc 注释。
- 每个 task 完成后 `gofmt -l internal web 2>/dev/null`（应无输出）——executor 的 ledger 有漏 gofmt 前科，这条是硬门。
- 前端契约唯一定义点是 `web/src/api/ledger.ts`；组件测试对齐同目录既有 `*.test.tsx` 的基建（imports 以邻近测试文件实际用法为准）。
- 测试命令：Go `go test ./internal/ledger/`；前端 `cd web && npx vitest run <文件>`。

## 明确不在本 plan 范围内

- 「分域开发」工作流模板本体与三个 prompt 模板（spec §8.1，下一个 plan）。
- (工作流模板, 目标域) 组合的环检测——卡还没有「域」标签，等分域模板落地带上域元数据后再细化；本 plan 的同名工作流嵌套上限已挡住自递归失控。
- 嵌套上限的配置化（项目实例化清单覆盖）——先钉常量，配置需求真出现再做。
- CLI 呈现改动——`card show` 走 detail 端点，`children` 已在响应里。
- 子卡失败时的自动 OnFail 路由（spec §8.2「重派该子卡/整组回拆解」）——卡模型没有「失败」终态（只有 已完成/终止），本轮语义定为：终止也算完结、闸的错误清单点名未完结子卡、整组怎么处置留人裁决。等真实使用出现「需要自动整组回拆解」的案例再立项，避免给还没验证过的流程预置自动化。

---

### Task 1: 聚合闸 `Gate.RequireChildrenDone`

**Files:**
- Modify: `internal/ledger/types.go:112-115`（Gate 结构）
- Modify: `internal/ledger/move.go:55-73`（moveCardTx 的 gate 判定块）
- Test: `internal/ledger/move_children_gate_test.go`（新建）

**Interfaces:**
- Consumes: 既有 `Gate`、`moveCardTx`、`ErrGateBlocked`、`seedStore`/`mustChild` 测试帮手、`PutWorkflow(name, def)`。
- Produces: `Gate.RequireChildrenDone bool`（json `require_children_done`）——Task 5 的 TS 契约、NodeEditor 开关依赖此字段名。

- [ ] **Step 1: 写失败测试**

```go
// 聚合闸：RequireChildrenDone 的门条件。父卡进目标列前，全部直接子卡
// 必须已完结（已完成或终止）；无子卡时空洞为真（同一工作流复用给不扇出
// 的卡时不该被闸卡住）。
package ledger

import (
	"errors"
	"strings"
	"testing"
)

// fanoutStore 建一条带聚合闸的两列工作流：进行中 →（闸）集成。
func fanoutStore(t *testing.T) *Store {
	t.Helper()
	s := seedStore(t)
	if _, err := s.PutWorkflow("fanout", WorkflowDef{Nodes: []NodeDef{
		{Name: "进行中", Next: "集成"},
		{Name: "集成", Gate: Gate{RequireChildrenDone: true}},
	}}); err != nil {
		t.Fatalf("PutWorkflow: %v", err)
	}
	return s
}

func mkFanout(t *testing.T, s *Store, title string) Card {
	t.Helper()
	card, err := s.CreateCard(NewCard{Title: title, Project: "p", Workflow: "fanout", Actor: "test"})
	if err != nil {
		t.Fatalf("建 fanout 卡: %v", err)
	}
	return card
}

// 有未完结子卡 → 拒，错误里点名 pending 的子卡；子卡全完结（done 或
// closed 混合）→ 放行。
func TestChildrenGateBlocksUntilChildrenSettled(t *testing.T) {
	s := fanoutStore(t)
	parent := mkFanout(t, s, "母卡")
	childA := mustChild(t, s, parent.ID, "子 A")
	childB := mustChild(t, s, parent.ID, "子 B")

	err := s.MoveCard(parent.ID, "集成", "", "test")
	if !errors.Is(err, ErrGateBlocked) {
		t.Fatalf("应被聚合闸拦下，实得: %v", err)
	}
	if !strings.Contains(err.Error(), childA.ID) {
		t.Fatalf("错误应点名未完结子卡 %s，实得: %v", childA.ID, err)
	}

	if err := s.MoveCard(childA.ID, StatusDone, "", "test"); err != nil {
		t.Fatalf("完结子 A: %v", err)
	}
	if err := s.CloseCard(childB.ID, CloseCancelled, "test"); err != nil {
		t.Fatalf("终止子 B: %v", err)
	}
	if err := s.MoveCard(parent.ID, "集成", "", "test"); err != nil {
		t.Fatalf("子卡全完结后应放行，实得: %v", err)
	}
}

// 无子卡 = 空洞为真：同一工作流给不扇出的卡复用时直接过闸。
func TestChildrenGatePassesWithNoChildren(t *testing.T) {
	s := fanoutStore(t)
	solo := mkFanout(t, s, "独卡")
	if err := s.MoveCard(solo.ID, "集成", "", "test"); err != nil {
		t.Fatalf("无子卡应过闸，实得: %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ledger/ -run TestChildrenGate -v`
Expected: FAIL（`unknown field RequireChildrenDone`——编译错，即为失败形态）

- [ ] **Step 3: types.go 加字段**

`internal/ledger/types.go` 的 Gate 改为：

```go
// Gate workflow 转移进入某状态前的门条件。
type Gate struct {
	RequireAttachment string `json:"require_attachment,omitempty"` // 附件 kind 非空集
	RequireAcceptance bool   `json:"require_acceptance,omitempty"` // 验收判据非空
	// RequireChildrenDone 聚合闸：全部**直接**子卡已完结（已完成或终止）
	// 才许进入本列。无子卡时空洞为真——同一工作流复用给不扇出的卡时，
	// 这张卡不该被自己用不上的闸卡住。终止也算完结是刻意的：被取消的
	// 子卡不该把父卡永远堵死，取舍权在看错误清单的人。
	RequireChildrenDone bool `json:"require_children_done,omitempty"`
}
```

- [ ] **Step 4: move.go 加判定**

在 `moveCardTx` 现有 gate 块内（`gate.RequireAcceptance` 判定之后、CAS 写之前）插入：

```go
			if gate.RequireChildrenDone {
				pending, err := s.pendingChildrenTx(tx, id)
				if err != nil {
					return err
				}
				if len(pending) > 0 {
					log().Warn("转移被拒：聚合闸有未完结子卡", "card", id, "to", to, "pending", pending)
					return fmt.Errorf("进 %q 需全部子卡完结，未完结: %s: %w",
						to, strings.Join(pending, ", "), ErrGateBlocked)
				}
			}
```

同文件底部加帮手（import 补 `strings`）：

```go
// pendingChildrenTx 事务内取未完结（非 已完成/终止）的直接子卡 id 列表。
// 与转移同事务读：闸判定和状态写之间不留「子卡刚好在窗口里完结/复活」的缝。
func (s *Store) pendingChildrenTx(tx *sql.Tx, id string) ([]string, error) {
	rows, err := tx.Query(s.q(`SELECT id, status FROM cards WHERE parent_id = ?`), id)
	if err != nil {
		return nil, fmt.Errorf("聚合闸读子卡: %w", err)
	}
	defer rows.Close()
	var pending []string
	for rows.Next() {
		var childID, status string
		if err := rows.Scan(&childID, &status); err != nil {
			return nil, err
		}
		if status != StatusDone && status != StatusClosed {
			pending = append(pending, childID)
		}
	}
	return pending, rows.Err()
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/ledger/ -run TestChildrenGate -v`
Expected: PASS（两个用例）

- [ ] **Step 6: 日志与注释自检**

- 拒绝路径已 Warn 带 card/to/pending（Step 4）✓；帮手 doc 注释解释「为什么同事务」✓；Gate 字段注释解释「为什么终止也算完结」✓。放行路径不加日志——与既有 gate 判定一致（放行的可观测点是随后的 `EvStatusMoved` 事件）。

- [ ] **Step 7: 回归 + 提交**

Run: `go test ./internal/ledger/ && gofmt -l internal`
Expected: 全绿、gofmt 无输出

```bash
git add internal/ledger/types.go internal/ledger/move.go internal/ledger/move_children_gate_test.go
git commit -m "feat(ledger): 聚合闸 RequireChildrenDone——父卡等全部子卡完结再过节点"
```

---

### Task 2: `CardView` 子卡计数派生（children_total / children_done）

**Files:**
- Modify: `internal/ledger/types.go:202-210`（CardView）
- Modify: `internal/ledger/derived.go`（ListCards 组装 + 新帮手 allParents）
- Test: `internal/ledger/derived_children_test.go`（新建）

**Interfaces:**
- Consumes: `ListCards`、`allStatuses`（已返回 map[id]status）、Task 1 的完结语义（done|closed）。
- Produces: `CardView.ChildrenTotal/ChildrenDone int`（json `children_total`/`children_done`）——Task 5 TS 契约、Task 6 看板徽标依赖此字段名；`children_done` 的语义 = **已完结**（含终止），与聚合闸一致。

- [ ] **Step 1: 写失败测试**

```go
// CardView 子卡计数派生：children_total/children_done 查询期现算。
// done 语义 = 已完结（已完成或终止），与聚合闸（move_children_gate_test）
// 保持同一把尺——徽标显示 2/3 而闸放行，是两处语义漂移的经典形态。
package ledger

import "testing"

func TestListCardsDerivesChildrenCounts(t *testing.T) {
	s := seedStore(t)
	parent := mk(t, s, "母卡")
	childA := mustChild(t, s, parent.ID, "子 A")
	childB := mustChild(t, s, parent.ID, "子 B")
	mustChild(t, s, parent.ID, "子 C")
	// 孙卡不计入母卡（只数直接子卡）
	mustChild(t, s, childA.ID, "孙卡")

	if err := s.MoveCard(childA.ID, StatusDone, "", "test"); err != nil {
		t.Fatalf("完结子 A: %v", err)
	}
	if err := s.CloseCard(childB.ID, CloseShelved, "test"); err != nil {
		t.Fatalf("搁置子 B: %v", err)
	}

	views, err := s.ListCards(CardFilter{})
	if err != nil {
		t.Fatalf("ListCards: %v", err)
	}
	var got *CardView
	for i := range views {
		if views[i].ID == parent.ID {
			got = &views[i]
		}
	}
	if got == nil {
		t.Fatalf("列表里找不到母卡 %s", parent.ID)
	}
	if got.ChildrenTotal != 3 || got.ChildrenDone != 2 {
		t.Fatalf("应 total=3 done=2（done 含终止），实得 total=%d done=%d",
			got.ChildrenTotal, got.ChildrenDone)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ledger/ -run TestListCardsDerivesChildrenCounts -v`
Expected: FAIL（编译错：无 ChildrenTotal 字段）

- [ ] **Step 3: types.go CardView 加字段**

在 `OpenDecisions int` 之后：

```go
	ChildrenTotal int      `json:"children_total"`         // 直接子卡数
	ChildrenDone  int      `json:"children_done"`          // 已完结（已完成或终止）的直接子卡数——语义与聚合闸同一把尺
```

- [ ] **Step 4: derived.go 组装计数**

`derived.go` 底部加帮手：

```go
// allParents 全量 child→parent 映射（只含有父的卡）。与 allStatuses 同为
// 「全量小表 + 内存组装」——卡量级数百张，正确性优先（见文件头）。
func (s *Store) allParents() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT id, parent_id FROM cards WHERE parent_id IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("读父子表: %w", err)
	}
	defer rows.Close()
	parents := map[string]string{}
	for rows.Next() {
		var id, parent string
		if err := rows.Scan(&id, &parent); err != nil {
			return nil, err
		}
		parents[id] = parent
	}
	return parents, rows.Err()
}
```

`ListCards` 里 `openDecisionCount` 取完之后、组装循环之前加：

```go
	parents, err := s.allParents()
	if err != nil {
		return nil, err
	}
	type childStat struct{ total, done int }
	childStats := map[string]*childStat{}
	for child, parent := range parents {
		stat := childStats[parent]
		if stat == nil {
			stat = &childStat{}
			childStats[parent] = stat
		}
		stat.total++
		// 完结 = 已完成或终止，与聚合闸同一把尺（types.go Gate 注释）
		if status := statusOf[child]; status == StatusDone || status == StatusClosed {
			stat.done++
		}
	}
```

组装循环里 `view := CardView{...}` 之后加：

```go
		if stat := childStats[card.ID]; stat != nil {
			view.ChildrenTotal, view.ChildrenDone = stat.total, stat.done
		}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/ledger/ -run TestListCardsDerivesChildrenCounts -v`
Expected: PASS

- [ ] **Step 6: 日志与注释自检**

- 派生查询失败路径带上下文（"读父子表"）✓；帮手与字段注释解释「为什么与聚合闸同一把尺」✓。纯查询路径无状态变更，按 derived.go 既有约定不加成功日志。

- [ ] **Step 7: 回归 + 提交**

Run: `go test ./internal/ledger/ && gofmt -l internal`
Expected: 全绿、gofmt 无输出

```bash
git add internal/ledger/types.go internal/ledger/derived.go internal/ledger/derived_children_test.go
git commit -m "feat(ledger): CardView 派生子卡计数 children_total/children_done"
```

---

### Task 3: 建子卡落父卡时间线事件（父子边入账本）

**Files:**
- Modify: `internal/ledger/cards.go:161-168`（CreateCard 事务内）
- Test: `internal/ledger/cards_test.go`（追加用例）

**Interfaces:**
- Consumes: `appendEvent(tx, sink, cardID, type, actor, payload)`、`EvComment`、`EventsFromAsc`。
- Produces: 父卡 timeline 上一条 `EvComment`（payload: kind=普通、body=`创建子卡 <id>：<title>`、refs=[子卡 id]）——抽屉时间线自动渲染，无前端改动。

- [ ] **Step 1: 写失败测试（追加到 cards_test.go）**

```go
// 建子卡要在父卡 timeline 留痕：审计链能回答「这张子卡为什么存在、
// 什么时候从谁身上拆出来的」。复用 EvComment+refs（AddBlocks 同款），
// 不新增事件类型。
func TestCreateChildLeavesParentTimelineEvent(t *testing.T) {
	s := seedStore(t)
	parent := mk(t, s, "母卡")
	child := mustChild(t, s, parent.ID, "拆出的子卡")

	events, err := s.EventsFromAsc([]string{parent.ID}, 0, 100)
	if err != nil {
		t.Fatalf("读父卡事件: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Type == EvComment && strings.Contains(string(event.Payload), child.ID) {
			found = true
		}
	}
	if !found {
		t.Fatalf("父卡 timeline 应有指向 %s 的建子卡事件，实得 %d 条事件", child.ID, len(events))
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ledger/ -run TestCreateChildLeavesParentTimelineEvent -v`
Expected: FAIL（found=false）

- [ ] **Step 3: CreateCard 事务内补父卡事件**

在子卡自身 `EvCardCreated` 的 `appendEvent` 成功之后、`card = Card{...}` 之前插入：

```go
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
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/ledger/ -run TestCreateChildLeavesParentTimelineEvent -v`
Expected: PASS

- [ ] **Step 5: 日志与注释自检**

- 事件本身就是可观测轨迹（账本单流），错误分支沿事务上抛带上下文 ✓；插入块注释解释「为什么同事务」✓。

- [ ] **Step 6: 回归 + 提交**

Run: `go test ./internal/ledger/ && gofmt -l internal`
Expected: 全绿、gofmt 无输出

```bash
git add internal/ledger/cards.go internal/ledger/cards_test.go
git commit -m "feat(ledger): 建子卡时在父卡 timeline 落事件，父子边入账本"
```

---

### Task 4: 递归护栏——同名工作流嵌套深度上限

**Files:**
- Modify: `internal/ledger/cards.go`（常量 + 帮手 + CreateCard 前置校验）
- Test: `internal/ledger/cards_test.go`（追加用例）

**Interfaces:**
- Consumes: `CreateCard` 事务、`ErrBadState`（HTTP 层翻 400 的哨兵，validateNodes 同款）。
- Produces: `maxWorkflowNesting = 3` 常量与 `workflowNestingTx` 帮手；超限建卡报错并给出处置指引。

- [ ] **Step 1: 写失败测试（追加到 cards_test.go）**

```go
// 递归护栏：父链上同名工作流的嵌套上限（spec §8.3）。子卡可绑任意工作流
// ——包括父卡自己那个，递归是组合性质；护栏挡的是失控（拆解节点把活原样
// 再拆给自己直到永远）。异名工作流不受此限。
func TestCreateCardRejectsDeepWorkflowNesting(t *testing.T) {
	s := seedStore(t)
	newBug := func(parent string) (Card, error) {
		return s.CreateCard(NewCard{Title: "层", Project: "p", Workflow: "bug", Parent: parent, Actor: "test"})
	}
	level1, err := newBug("")
	if err != nil {
		t.Fatalf("第 1 层: %v", err)
	}
	level2, err := newBug(level1.ID)
	if err != nil {
		t.Fatalf("第 2 层: %v", err)
	}
	level3, err := newBug(level2.ID)
	if err != nil {
		t.Fatalf("第 3 层（达上限，应放行）: %v", err)
	}
	if _, err := newBug(level3.ID); !errors.Is(err, ErrBadState) {
		t.Fatalf("第 4 层应被护栏拒（wrap ErrBadState），实得: %v", err)
	}
	// 异名工作流不占同一个计数：feature 卡挂在三层 bug 之下照常放行
	if _, err := s.CreateCard(NewCard{Title: "异名", Project: "p", Workflow: "feature",
		Parent: level3.ID, Actor: "test"}); err != nil {
		t.Fatalf("异名工作流不该被拒: %v", err)
	}
}
```

（cards_test.go 顶部 import 需补 `errors`。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ledger/ -run TestCreateCardRejectsDeepWorkflowNesting -v`
Expected: FAIL（第 4 层被放行，err=nil）

- [ ] **Step 3: 实现护栏**

`cards.go` 顶部常量区（`topIDPat` 附近）加：

```go
// maxWorkflowNesting 父链上同名工作流的嵌套上限（含新卡自身）。
//
// why 存在：子卡可绑任意工作流模板——包括「分域开发」这类会在拆解节点
// 再生子卡的模板自身，递归是刻意保留的组合性质（spec §8.3）；这个常量
// 挡的是失控递归（拆解把活原样再拆给自己）。why 是 3：两层分域已覆盖
// 「域内再分小领域」，第三层留给极端大活；再深就该先竖切域了。
// 配置化（项目实例化清单覆盖）等真实需求出现再做。
const maxWorkflowNesting = 3
```

`CreateCard` 的 mutate 内、父卡存在性校验之后（`id, idErr = s.nextChildID(...)` 之前）加：

```go
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
```

`cards.go` 底部加帮手：

```go
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
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/ledger/ -run TestCreateCardRejectsDeepWorkflowNesting -v`
Expected: PASS

- [ ] **Step 5: 日志与注释自检**

- 拒绝路径 Warn 带 parent/workflow/nesting ✓；错误文案给出处置指引（先竖切/换工作流）而非只报「不行」✓；常量注释写清 why-3 与配置化取舍 ✓。

- [ ] **Step 6: 回归 + 提交**

Run: `go test ./internal/ledger/ && gofmt -l internal`
Expected: 全绿、gofmt 无输出

```bash
git add internal/ledger/cards.go internal/ledger/cards_test.go
git commit -m "feat(ledger): 递归护栏——父链同名工作流嵌套上限 3，超限建卡 400"
```

---

### Task 5: Web 契约与 NodeEditor 聚合闸开关

**Files:**
- Modify: `web/src/api/ledger.ts:16-20`（CardView）与 `:72`（gate 类型）
- Modify: `web/src/app/flows/NodeEditor.tsx:250` 附近（require_acceptance 复选框之后）
- Test: `web/src/app/flows/NodeEditor.childrengate.test.tsx`（新建；imports 对齐同目录既有测试）

**Interfaces:**
- Consumes: Task 1 的 `require_children_done`、Task 2 的 `children_total`/`children_done` 字段名；NodeEditor 既有 `updateGate(patch)`（清 undefined 的模式，NodeEditor.tsx:53-58）。
- Produces: TS 侧契约字段 + 编辑器开关；Task 6 直接消费 `CardView.children_total/children_done` 类型。

- [ ] **Step 1: ledger.ts 补契约字段**

CardView（`open_decisions: number` 之后）：

```ts
  children_total: number
  children_done: number
```

gate 类型（`:72`）改为：

```ts
  gate?: { require_attachment?: string; require_acceptance?: boolean; require_children_done?: boolean }
```

- [ ] **Step 2: 写失败测试**

```tsx
// NodeEditor 聚合闸开关：勾选写 require_children_done=true，取消勾选把
// 字段从 gate 里清掉（不是写 false）——与 require_acceptance 同款语义，
// 后端 omitempty 才不会存一堆无意义的 false。
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { NodeEditor } from './NodeEditor'

describe('NodeEditor 聚合闸', () => {
  it('勾选写 require_children_done，取消勾选清掉字段', () => {
    const update = vi.fn()
    render(<NodeEditor node={{ name: '集成' }} update={update} />)
    fireEvent.click(screen.getByLabelText('需全部子卡完结'))
    expect(update).toHaveBeenCalledWith({ gate: { require_children_done: true } })

    update.mockClear()
    render(<NodeEditor node={{ name: '集成', gate: { require_children_done: true } }} update={update} />)
    const boxes = screen.getAllByLabelText('需全部子卡完结')
    fireEvent.click(boxes[boxes.length - 1])
    expect(update).toHaveBeenCalledWith({ gate: undefined })
  })
})
```

**注意**：`NodeEditor` 的实际 props 名以文件里的组件签名为准（本测试假设 `node`/`update`；若实际是别的名字，测试对齐组件，不改组件）。

- [ ] **Step 3: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/flows/NodeEditor.childrengate.test.tsx`
Expected: FAIL（找不到「需全部子卡完结」）

- [ ] **Step 4: NodeEditor 加开关**

在 `require_acceptance` 复选框（`:250` 附近）的同级位置、照它的既有 JSX 结构追加：

```tsx
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={node.gate?.require_children_done === true}
              onChange={(event) => updateGate({ require_children_done: event.target.checked || undefined })}
            />
            需全部子卡完结
          </label>
```

（label/包裹结构照抄 require_acceptance 那一块的实际写法，保持视觉一致；`updateGate` 已会清空 undefined 字段并在 gate 全空时整体置 undefined。）

- [ ] **Step 5: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/flows/NodeEditor.childrengate.test.tsx`
Expected: PASS

- [ ] **Step 6: 注释自检**

- 测试文件头注释解释「为什么取消勾选是清字段不是写 false」✓；契约字段跟随 ledger.ts 既有行内注释风格，`children_done` 加一句 `// 已完结（含终止），与聚合闸同一把尺`。

- [ ] **Step 7: 回归 + 提交**

Run: `cd web && npx vitest run src/app/flows/ && npx tsc --noEmit`
Expected: 全绿

```bash
git add web/src/api/ledger.ts web/src/app/flows/NodeEditor.tsx web/src/app/flows/NodeEditor.childrengate.test.tsx
git commit -m "feat(web): 工作流节点聚合闸开关 + CardView 子卡计数契约"
```

---

### Task 6: 看板父卡子卡徽标

**Files:**
- Modify: `web/src/app/cards/CardItem.tsx:63-65`（Chip 行）
- Test: `web/src/app/cards/CardItem.children.test.tsx`（新建）

**Interfaces:**
- Consumes: Task 5 的 `CardView.children_total/children_done` 类型。
- Produces: 父卡上 `⧉ 子卡 done/total` 徽标（无子卡不渲染）。

- [ ] **Step 1: 写失败测试**

```tsx
// 看板父卡徽标：children_total>0 时显示「⧉ 子卡 done/total」，无子卡
// 不渲染（普通卡不为用不上的机制付视觉税）。
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { CardView } from '../../api/ledger'
import { CardItem } from './CardItem'

const base: CardView = {
  id: 'B1', title: '卡', status: '进行中', priority: '中', project: 'p',
  parent: '', workflow: 'feature', workflow_version: 1,
  blocked: false, blocked_by: [], merged_count: 0, open_decisions: 0,
  open_tickets: 0, children_total: 0, children_done: 0,
} as unknown as CardView

describe('CardItem 子卡徽标', () => {
  it('有子卡时显示 done/total', () => {
    render(<CardItem card={{ ...base, children_total: 3, children_done: 2 }} onOpen={vi.fn()} />)
    expect(screen.getByText('⧉ 子卡 2/3')).toBeInTheDocument()
  })
  it('无子卡不渲染徽标', () => {
    render(<CardItem card={base} onOpen={vi.fn()} />)
    expect(screen.queryByText(/子卡/)).toBeNull()
  })
})
```

（`base` 的字段集若与 `CardView` 实际必填集不符，以 `ledger.ts` 为准补齐；用 `as unknown as CardView` 兜住测试夹具与契约的松耦合。）

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/cards/CardItem.children.test.tsx`
Expected: FAIL（找不到「⧉ 子卡 2/3」）

- [ ] **Step 3: CardItem 加徽标**

在「并入」Chip（`:63-65`）之后插入：

```tsx
        {(card.children_total ?? 0) > 0 && (
          <Chip className="text-foreground" title="直接子卡：已完结/总数（完结含终止）">
            ⧉ 子卡 {card.children_done}/{card.children_total}
          </Chip>
        )}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/cards/CardItem.children.test.tsx`
Expected: PASS

- [ ] **Step 5: 回归 + 提交**

Run: `cd web && npx vitest run src/app/cards/ && npx tsc --noEmit`
Expected: 全绿

```bash
git add web/src/app/cards/CardItem.tsx web/src/app/cards/CardItem.children.test.tsx
git commit -m "feat(web): 看板父卡显示子卡完结徽标 done/total"
```

---

### Task 7: 终检

**Files:** 无新改动（只跑检查，发现问题就地修）

- [ ] **Step 1: 全量回归**

Run: `go build ./... && go vet ./... && go test ./internal/ledger/ ./internal/agentd/ && gofmt -l cmd internal`
Expected: 全部通过、gofmt 无输出

- [ ] **Step 2: 前端全量**

Run: `cd web && npx vitest run && npx tsc --noEmit`
Expected: 全绿

- [ ] **Step 3: instrumenting-code 完工清单自检**

逐项过：错误分支带上下文 ✓ / 无 fmt.Printf ✓ / 拒绝路径 Warn ✓ / 新帮手有 doc 注释 ✓ / 复杂取舍有 why 注释 ✓。任一不满足回对应 task 补。

- [ ] **Step 4: 提交收尾（若有修补）**

```bash
git add -A && git commit -m "chore: 终检修补"
```

---

## 协调者本地验收清单（不派发，属审核者）

1. 浏览器走查：工作流页开「需全部子卡完结」开关 → 保存出新版本；建父卡+2 子卡，父卡拖进闸列被拒且错误点名子卡；子卡完结后拖动放行。
2. 看板：父卡显示 `⧉ 子卡 x/y` 徽标；抽屉子任务区与徽标数一致。
3. CLI：`handoff card add --parent` 连建 4 层同工作流子卡，第 4 层被拒且错误可读。
