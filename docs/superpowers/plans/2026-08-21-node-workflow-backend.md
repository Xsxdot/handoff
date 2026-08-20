# 节点化工作流（后端）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把「工作流」从写死的状态串改成用户可编排的节点序列——每个节点带纪律块、执行者、能力开关，执行器按节点配置通用化执行，不再 switch review/merge。

**Architecture:** `WorkflowDef` 增 `Nodes []NodeDef`，`Nodes` 为权威、`States`/`Gates` 降为写入时派生的只读投影（老 def 读出时反向补出 Nodes），存量卡钉的旧版本因此零改动继续可读。`ledgerstep` 的 `ReviewStep`/`MergeStep` 合并为一个 `NodeStep`，行为全部由 `NodeDef` 的能力开关决定；executor 收到的 prompt 由「模板正文 + 卡上下文 + 本次补充」三段拼装。本地合并执行（`MergeStep`/`NewLocalObjective`/`NewLocalMerge`/`gitscript.go`）退役——合并改为普通派发节点，走 `finishing` 纪律块。

**Tech Stack:** Go 1.22+、SQLite（`internal/ledger`）、`net/http` ServeMux（`internal/agentd`）、cobra（`cmd`）、`go:embed`（`internal/discipline`）。

## Global Constraints

- **不碰 main 分支**；本 plan 全部落在 `feat/b156-workbench-ledger` 上。
- **老 `WorkflowDef`（只有 `States`/`Gates`）必须继续可解码**：卡钉工作流版本，旧行失读等于存量卡全废。每个改动读取路径的 task 都要有老 def 的回归用例。
- **纪律块只传名字，绝不在 `ledgerstep` 拼正文**：正文由 agentd 按 B129 机制注入。两份纪律同场已在 2026-08-19 真机出过事故。
- **日志一律 `log/slog`**（`internal/ledger` 用包内 `log()`，`internal/agentd` 用 `s.log`，`ledgerstep` 用 `slog.Default()`）。**禁止 `fmt.Printf`**。
- **不要调用 `handoff` CLI、不要起 agentd、不要派发子任务**：本 plan 全部判据都是 `go test` / `go build` / `go vet`，真机验收由协调者在本地做。
- 每个 task 结束必须 `gofmt -l .` 无输出后再 commit。

---

### Task 1: NodeDef 数据模型与新旧 def 双向投影

**Files:**
- Modify: `internal/ledger/types.go`（在 `WorkflowDef` 附近加 `NodeDef`/`NodeOverride`，`WorkflowDef` 增 `Nodes` 字段）
- Modify: `internal/ledger/workflows.go`（`PutWorkflow` 写入前投影、`GetWorkflow` 读出后补 Nodes）
- Test: `internal/ledger/workflows_test.go`

**Interfaces:**
- Consumes: 现有 `WorkflowDef{States, Gates}`、`Gate{RequireAttachment, RequireAcceptance}`
- Produces: `ledger.NodeDef`、`ledger.NodeOverride`、`WorkflowDef.Nodes`、`(WorkflowDef).withNodesFromStates()`、`(WorkflowDef).withStatesFromNodes()`

- [ ] **Step 1: 写失败的测试**

在 `internal/ledger/workflows_test.go` 末尾追加：

```go
func TestWorkflowLegacyDefStillDecodes(t *testing.T) {
	s := newTestStore(t)
	// 老 def：只有 States/Gates，没有 Nodes。存量卡钉的就是这种行。
	if _, err := s.PutWorkflow("legacy", WorkflowDef{
		States: []string{"待办", "进行中", "已完成"},
		Gates:  map[string]Gate{"已完成": {RequireAcceptance: true}},
	}); err != nil {
		t.Fatalf("写老 def: %v", err)
	}
	got, err := s.GetWorkflow("legacy", 0)
	if err != nil {
		t.Fatalf("读老 def: %v", err)
	}
	if len(got.Def.States) != 3 {
		t.Fatalf("States 丢了: %v", got.Def.States)
	}
	// 读出时应补出等价的纯人工节点序列，且顺序与 States 一致、按序 Next。
	if len(got.Def.Nodes) != 3 {
		t.Fatalf("Nodes 应补出 3 个，得到 %d", len(got.Def.Nodes))
	}
	if got.Def.Nodes[0].Name != "待办" || got.Def.Nodes[0].Next != "进行中" {
		t.Fatalf("首节点补错: %+v", got.Def.Nodes[0])
	}
	if got.Def.Nodes[2].Next != "" {
		t.Fatalf("末节点不该有 Next: %+v", got.Def.Nodes[2])
	}
	if got.Def.Nodes[0].Dispatch || got.Def.Nodes[0].Verdict {
		t.Fatalf("补出的节点必须是纯人工列: %+v", got.Def.Nodes[0])
	}
	if got.Def.Nodes[2].Gate.RequireAcceptance != true {
		t.Fatalf("Gate 应并入对应节点: %+v", got.Def.Nodes[2].Gate)
	}
}

func TestWorkflowNodesProjectToStates(t *testing.T) {
	s := newTestStore(t)
	// 先 seed 模板：本用例引用了 feature-impl，而 Task 2 会给 PutWorkflow 加上
	// 模板存在性校验。现在补这一行，等 Task 2 落地时这个用例不会回头变红。
	if err := s.EnsureDefaultTemplates(); err != nil {
		t.Fatalf("seed 模板: %v", err)
	}
	// 新 def：只给 Nodes，States/Gates 应由写入侧派生出来，
	// 好让 MoveCard 等既有消费者一行不改地继续工作。
	if _, err := s.PutWorkflow("nodeform", WorkflowDef{
		Nodes: []NodeDef{
			{Name: "待办", Next: "进行中"},
			{Name: "进行中", Next: "待审阅", Dispatch: true, Template: "feature-impl"},
			{Name: "待审阅", Gate: Gate{RequireAcceptance: true}},
		},
	}); err != nil {
		t.Fatalf("写节点形 def: %v", err)
	}
	got, err := s.GetWorkflow("nodeform", 0)
	if err != nil {
		t.Fatalf("读节点形 def: %v", err)
	}
	want := []string{"待办", "进行中", "待审阅"}
	if len(got.Def.States) != len(want) {
		t.Fatalf("States 未派生: %v", got.Def.States)
	}
	for i, state := range want {
		if got.Def.States[i] != state {
			t.Fatalf("States[%d] = %q, want %q", i, got.Def.States[i], state)
		}
	}
	if !got.Def.Gates["待审阅"].RequireAcceptance {
		t.Fatalf("Gates 未派生: %+v", got.Def.Gates)
	}
	if !got.Def.Nodes[1].Dispatch || got.Def.Nodes[1].Template != "feature-impl" {
		t.Fatalf("Nodes 原样保存失败: %+v", got.Def.Nodes[1])
	}
}
```

- [ ] **Step 2: 跑测试确认它红**

Run: `cd /path/to/repo && go test ./internal/ledger/ -run 'TestWorkflowLegacyDefStillDecodes|TestWorkflowNodesProjectToStates' -v`
Expected: 编译失败，`undefined: NodeDef` / `unknown field Nodes`。

- [ ] **Step 3: 加类型定义（含注释）**

在 `internal/ledger/types.go` 的 `WorkflowDef` **之前**插入：

```go
// NodeOverride 节点对所引模板的单字段覆盖；零值字段 = 沿用模板的值。
//
// why 要「引模板 + 覆盖」而不是节点内联全部字段：executor / target / model
// 这几样在同一条流里高度重复，内联会让「换一台执行机」变成挨个节点改；
// 而只引模板又满足不了「审阅这一列想换个执行者」这种单点微调。
type NodeOverride struct {
	Executor   string `json:"executor,omitempty"`
	Discipline string `json:"discipline,omitempty"` // 具名纪律块名，如 review / finishing
	Target     string `json:"target,omitempty"`
	Model      string `json:"model,omitempty"`
}

// NodeDef 工作流的一个节点：看板的一列 + 卡走到这列时的执行规矩。
//
// 设计要点（用户 2026-08-21 定死，改动前先回看 spec）：
//   - **没有预设节点类型**。「审阅」「合并」不是内置语义，而是下面几个能力
//     开关的组合结果，用户可以随意重组。
//   - **节点只配「怎么干」（纪律、执行者、开关），不配「干什么」**。合并目标
//     这类每张卡都不同的值来自卡本身（有效基线分支），不写死在节点上。
//   - **路由用节点名指向而不是数组下标**，为将来的 DAG 分叉预留形状；本轮
//     只实现线性消费。
type NodeDef struct {
	Name     string       `json:"name"`
	Template string       `json:"template,omitempty"` // Dispatch=true 时必填
	Override NodeOverride `json:"override,omitempty"`

	// 能力开关——语义由组合得出，不要在这里新增「节点类型」字段。
	Dispatch         bool `json:"dispatch,omitempty"`           // 进入本列时派发一个任务
	Verdict          bool `json:"verdict,omitempty"`            // 等回合终态、解析裁决块并按结果路由（蕴含 Dispatch）
	CarryCardContext bool `json:"carry_card_context,omitempty"` // prompt 里拼入卡上下文段
	MaxRounds        int  `json:"max_rounds,omitempty"`         // Verdict 的轮次封顶；0 = 用包内默认

	Next   string `json:"next,omitempty"`    // 裁决通过后移到哪一列；空 = 停在本列
	OnFail string `json:"on_fail,omitempty"` // 裁决未过退到哪一列；空 = 停在本列
	Gate   Gate   `json:"gate,omitempty"`    // 进入本列的门槛

	// HumanBases 列出「卡的有效基线落在其中时，本节点不自动执行、直接打
	// 等人标记」的分支名。
	//
	// why 需要它：合并退役成普通派发节点后，原 MergeStep 里那条
	// 「基线是主线就永远人工」的保护随之消失，而往 main 合并是外部可见且
	// 不易撤回的动作。做成节点上的一个列表（而不是代码里的常量）既保住了
	// 保护，又符合「只提供能力、语义由配置组合」——用户想让某条流自动合
	// main，把这个列表清空即可。
	HumanBases []string `json:"human_bases,omitempty"`
}
```

把 `WorkflowDef` 改成：

```go
// WorkflowDef 工作流形状。
//
// **Nodes 是权威，States/Gates 是写入时从 Nodes 派生的只读投影。**
// why 保留派生投影而不是让消费者全改成读 Nodes：MoveCard 的状态校验、
// 看板列渲染、MigrateCardWorkflow 的防悬空校验都在读 States，派生投影让
// 它们一行不改地继续工作，把本次改动的爆炸半径压在读写两端。
//
// 反方向也成立：只有 States 的老行（存量卡钉的就是它）读出时补出等价的
// 纯人工节点序列，所以调用方永远可以只看 Nodes。
type WorkflowDef struct {
	States []string        `json:"states"`
	Gates  map[string]Gate `json:"gates,omitempty"` // key = 目标状态
	Nodes  []NodeDef       `json:"nodes,omitempty"`
}
```

- [ ] **Step 4: 加双向投影函数（含注释）**

在 `internal/ledger/workflows.go` 的 `PutWorkflow` **之前**插入：

```go
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
```

- [ ] **Step 5: 接进 PutWorkflow / GetWorkflow**

`PutWorkflow` 的开头，`json.Marshal` **之前**插入：

```go
	def = def.withStatesFromNodes()
```

`GetWorkflow` 中 `jsonUnmarshal` 成功之后、`workflow.CreatedAt = ...` 之前插入：

```go
	workflow.Def = workflow.Def.withNodesFromStates()
```

- [ ] **Step 6: 加关键节点日志**

`PutWorkflow` 在 `tx.Exec` 成功后（`return nil` 之前）加成功路径日志：

```go
		log().Info("写入工作流版本", "name", name, "version", version,
			"nodes", len(def.Nodes), "dispatch_nodes", countDispatchNodes(def.Nodes))
```

并在 `withNodesFromStates` 下方补辅助函数：

```go
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
```

`GetWorkflow` 在补 Nodes 之后加一行 Debug（读路径高频，不能用 Info）：

```go
	if len(workflow.Def.Nodes) > 0 && len(workflow.Def.Nodes) == len(workflow.Def.States) {
		log().Debug("读出工作流", "name", workflow.Name, "version", workflow.Version, "nodes", len(workflow.Def.Nodes))
	}
```

- [ ] **Step 7: 跑测试确认绿**

Run: `go test ./internal/ledger/ -run 'TestWorkflow' -v`
Expected: 全部 PASS，含既有的工作流用例。

- [ ] **Step 8: 全量回归 + 格式**

Run: `go build ./... && go vet ./... && go test ./... && gofmt -l .`
Expected: 测试全绿，`gofmt -l .` 无输出。

- [ ] **Step 9: Commit**

```bash
git add internal/ledger/types.go internal/ledger/workflows.go internal/ledger/workflows_test.go
git commit -m "feat(ledger): 工作流增 NodeDef，Nodes 权威、States 派生，老 def 双向兼容"
```

---

### Task 2: PutWorkflow 的节点校验

**Files:**
- Modify: `internal/ledger/workflows.go`
- Test: `internal/ledger/workflows_test.go`

**Interfaces:**
- Consumes: Task 1 的 `NodeDef`、既有 `Store.GetTemplate(name string, version int) (Template, error)`、`ErrNotFound`、`ErrBadState`
- Produces: `PutWorkflow` 在节点非法时返回错误（包装 `ErrBadState`），HTTP 层据此答 400

- [ ] **Step 1: 写失败的测试**

追加到 `internal/ledger/workflows_test.go`：

```go
func TestPutWorkflowRejectsBadNodes(t *testing.T) {
	s := newTestStore(t)
	if err := s.EnsureDefaultTemplates(); err != nil {
		t.Fatalf("seed 模板: %v", err)
	}
	cases := []struct {
		name string
		def  WorkflowDef
		want string // 错误里必须出现的关键词
	}{
		{"空节点名", WorkflowDef{Nodes: []NodeDef{{Name: ""}}}, "节点名"},
		{"重名节点", WorkflowDef{Nodes: []NodeDef{{Name: "A"}, {Name: "A"}}}, "重复"},
		{"派发缺模板", WorkflowDef{Nodes: []NodeDef{{Name: "A", Dispatch: true}}}, "模板"},
		{"模板不存在", WorkflowDef{Nodes: []NodeDef{
			{Name: "A", Dispatch: true, Template: "查无此模板"},
		}}, "查无此模板"},
		{"Next 悬空", WorkflowDef{Nodes: []NodeDef{{Name: "A", Next: "B"}}}, "B"},
		{"OnFail 悬空", WorkflowDef{Nodes: []NodeDef{
			{Name: "A", Dispatch: true, Verdict: true, Template: "review-generic", OnFail: "B"},
		}}, "B"},
		{"Verdict 不带 Dispatch", WorkflowDef{Nodes: []NodeDef{
			{Name: "A", Verdict: true, Template: "review-generic"},
		}}, "Dispatch"},
		{"MaxRounds 不带 Verdict", WorkflowDef{Nodes: []NodeDef{
			{Name: "A", Dispatch: true, Template: "feature-impl", MaxRounds: 3},
		}}, "Verdict"},
		{"MaxRounds 为负", WorkflowDef{Nodes: []NodeDef{
			{Name: "A", Dispatch: true, Verdict: true, Template: "review-generic", MaxRounds: -1},
		}}, "MaxRounds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.PutWorkflow("bad", tc.def)
			if err == nil {
				t.Fatalf("非法节点应被拒，却写成功了")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("错误里应提到 %q，实际: %v", tc.want, err)
			}
			if !errors.Is(err, ErrBadState) {
				t.Fatalf("应包装 ErrBadState，实际: %v", err)
			}
		})
	}
}

func TestPutWorkflowAcceptsGoodNodes(t *testing.T) {
	s := newTestStore(t)
	if err := s.EnsureDefaultTemplates(); err != nil {
		t.Fatalf("seed 模板: %v", err)
	}
	version, err := s.PutWorkflow("good", WorkflowDef{Nodes: []NodeDef{
		{Name: "待办", Next: "进行中"},
		{Name: "进行中", Next: "待审阅", Dispatch: true, Template: "feature-impl", CarryCardContext: true},
		{Name: "待审阅", Dispatch: true, Verdict: true, Template: "review-generic",
			CarryCardContext: true, MaxRounds: 3, Next: "已完成", OnFail: "进行中"},
		{Name: "已完成"},
	}})
	if err != nil {
		t.Fatalf("合法节点定义被拒: %v", err)
	}
	if version != 1 {
		t.Fatalf("version = %d, want 1", version)
	}
}
```

如果 `workflows_test.go` 还没 import `strings` / `errors`，补上。

- [ ] **Step 2: 跑测试确认它红**

Run: `go test ./internal/ledger/ -run TestPutWorkflow -v`
Expected: FAIL——非法定义全部被写进去了。

- [ ] **Step 3: 实现校验（含注释）**

在 `internal/ledger/workflows.go` 的 `withStatesFromNodes` 之后插入：

```go
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
```

- [ ] **Step 4: 接进 PutWorkflow**

`PutWorkflow` 里 `def = def.withStatesFromNodes()` **之后**、`json.Marshal` 之前插入：

```go
	if err := s.validateNodes(def.Nodes); err != nil {
		log().Warn("工作流节点校验未过", "name", name, "cause", err)
		return 0, err
	}
```

- [ ] **Step 5: 跑测试确认绿**

Run: `go test ./internal/ledger/ -run TestPutWorkflow -v`
Expected: 全部 PASS。

- [ ] **Step 6: 全量回归 + 格式**

Run: `go build ./... && go test ./... && gofmt -l .`
Expected: 全绿、无格式输出。**注意**：`EnsureDefaultWorkflows` 现在写的还是老 def（无 Nodes），校验会跳过，不受影响。

- [ ] **Step 7: Commit**

```bash
git add internal/ledger/workflows.go internal/ledger/workflows_test.go
git commit -m "feat(ledger): PutWorkflow 校验节点名/模板/路由/开关组合"
```

---

### Task 3: Prompt 三段拼装

**Files:**
- Modify: `internal/ledgerstep/dispatch.go`
- Test: `internal/ledgerstep/dispatch_test.go`

**Interfaces:**
- Consumes: 既有 `TemplateDispatch`、`Dispatcher.ViaTemplate`、`ledger.Card`、`Store.EffectiveBaseBranch`
- Produces: `TemplateDispatch` 增 `CarryCardContext bool` 与 `Extra string`；包内函数 `buildPrompt(body string, c ledger.Card, base string, carry bool, extra string) string`

- [ ] **Step 1: 写失败的测试**

追加到 `internal/ledgerstep/dispatch_test.go`：

```go
func TestBuildPromptThreeSections(t *testing.T) {
	card := ledger.Card{
		ID: "B9.1", Title: "做点什么", AcceptanceCriteria: "测试全绿",
		Attachments: []ledger.Attachment{
			{Kind: "spec", Path: "docs/spec.md"},
			{Kind: "plan", Path: "docs/plan.md"},
		},
	}
	t.Run("全关时只有模板正文", func(t *testing.T) {
		got := buildPrompt("模板正文", card, "feat/x", false, "")
		if got != "模板正文" {
			t.Fatalf("不该有多余段落:\n%s", got)
		}
	})
	t.Run("带卡上下文", func(t *testing.T) {
		got := buildPrompt("模板正文", card, "feat/x", true, "")
		for _, want := range []string{
			"模板正文", "## 本卡上下文", "B9.1", "做点什么",
			"feat/x", "合并目标以此为准", "测试全绿",
			"spec: docs/spec.md", "plan: docs/plan.md",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("缺 %q:\n%s", want, got)
			}
		}
	})
	t.Run("带本次补充", func(t *testing.T) {
		got := buildPrompt("模板正文", card, "feat/x", false, "这次只看并发安全")
		if !strings.Contains(got, "## 本次补充") || !strings.Contains(got, "这次只看并发安全") {
			t.Fatalf("补充段没拼进去:\n%s", got)
		}
	})
	t.Run("空基线不写死 main", func(t *testing.T) {
		got := buildPrompt("模板正文", card, "", true, "")
		if strings.Contains(got, "有效基线分支：main") {
			t.Fatalf("基线为空时不得替用户猜一个:\n%s", got)
		}
		if !strings.Contains(got, "有效基线分支：（未设置") {
			t.Fatalf("基线为空时应显式说明未设置:\n%s", got)
		}
	})
	t.Run("无附件不留空标题", func(t *testing.T) {
		bare := ledger.Card{ID: "B9.2", Title: "无附件"}
		got := buildPrompt("模板正文", bare, "feat/x", true, "")
		if strings.Contains(got, "- 附件：") {
			t.Fatalf("没有附件时不该出现附件小节:\n%s", got)
		}
	})
}
```

- [ ] **Step 2: 跑测试确认它红**

Run: `go test ./internal/ledgerstep/ -run TestBuildPromptThreeSections -v`
Expected: 编译失败，`undefined: buildPrompt`。

- [ ] **Step 3: 实现 buildPrompt（含注释）**

在 `internal/ledgerstep/dispatch.go` 的 `ViaTemplate` **之后**插入：

```go
// buildPrompt 把 executor 收到的 prompt 按三段拼起来。
//
// 参数：
//   - body:  模板正文（占位符已替换完）
//   - c:     卡
//   - base:  卡的有效基线分支，可为空
//   - carry: 是否拼入卡上下文段（节点的 CarryCardContext 开关）
//   - extra: 本次派发的临时补充说明，可为空
//
// 返回：拼好的 prompt。三段之间用空行分隔，缺席的段不留空标题。
//
// why 分三段而不是全塞进模板：模板是**复用**的（同一份审阅模板给所有卡用），
// 卡上下文是**每张卡不同的事实**，补充说明是**这一次才有的话**。混在一起就
// 只能靠占位符硬塞，而占位符加一个就要改所有模板。
//
// 注意：**这里绝不拼纪律块正文**。纪律块只传名字，正文由 agentd 按 B129 注入；
// 两份纪律同场会让审阅的「只读」被实现块的「完成即 commit」推翻（2026-08-19
// 真机出过一次）。
func buildPrompt(body string, c ledger.Card, base string, carry bool, extra string) string {
	sections := []string{body}
	if carry {
		var b strings.Builder
		b.WriteString("## 本卡上下文\n\n")
		fmt.Fprintf(&b, "- 卡号：%s\n", c.ID)
		fmt.Fprintf(&b, "- 标题：%s\n", c.Title)
		if base != "" {
			// 明写「合并目标以此为准」是这一段的核心用途：合并环节要合到哪条
			// 分支每张卡都不同，节点配置里没有也不该有这个值，它只能从卡带进来。
			fmt.Fprintf(&b, "- 有效基线分支：%s（本卡的合并目标以此为准，不要越过它碰别的分支）\n", base)
		} else {
			b.WriteString("- 有效基线分支：（未设置，需要合并时先向协调者确认，不要自行假定 main）\n")
		}
		if c.AcceptanceCriteria != "" {
			fmt.Fprintf(&b, "- 验收判据：\n%s\n", indentLines(c.AcceptanceCriteria, "  "))
		}
		if len(c.Attachments) > 0 {
			b.WriteString("- 附件（仓内相对路径）：\n")
			for _, att := range c.Attachments {
				fmt.Fprintf(&b, "  - %s: %s\n", att.Kind, att.Path)
			}
		}
		sections = append(sections, strings.TrimRight(b.String(), "\n"))
	}
	if strings.TrimSpace(extra) != "" {
		sections = append(sections, "## 本次补充\n\n"+strings.TrimSpace(extra))
	}
	return strings.Join(sections, "\n\n")
}

// indentLines 给多行文本每行加前缀，让验收判据在 markdown 列表下缩进对齐。
func indentLines(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
```

- [ ] **Step 4: 接进 ViaTemplate**

在 `TemplateDispatch` 结构体里加两个字段：

```go
	// CarryCardContext 为真时把卡上下文段拼进 prompt（来自节点的同名开关）。
	CarryCardContext bool
	// Extra 是本次派发的临时补充说明，可为空。
	Extra string
```

`ViaTemplate` 里 `prompt := body` 这一行**删掉**，改到 `base` 算完之后（因为拼装要用 `base`）。具体：把原来的

```go
	body := strings.NewReplacer(...).Replace(tpl.Def.Prompt)
	prompt := body
```

改成只保留 `body := ...`；然后在 `base` 计算完毕（`if reviewBase != "" { base = reviewBase }` 之后）插入：

```go
	// 三段拼装要用到有效基线，所以必须排在 base 算完之后。审阅轮的 base 被
	// 换成了工作分支，但卡上下文里要写的是**卡的**基线（合并目标），两者不同，
	// 因此这里重新取一次而不是复用上面的 base。
	cardBase, err := d.St.EffectiveBaseBranch(c.ID)
	if err != nil {
		return zero, fmt.Errorf("取卡上下文基线: %w", err)
	}
	prompt := buildPrompt(body, c, cardBase, req.CarryCardContext, req.Extra)
```

- [ ] **Step 5: 加关键节点日志**

在 `d.Transport(...)` 调用**之前**插入（外部调用前必打，且要能看出拼了哪几段）：

```go
	slog.Default().Info("按模板派发",
		"card", c.ID, "template", req.Template, "target", target,
		"executor", tpl.Def.Executor, "discipline", disciplineName,
		"branch", branch, "base", base,
		"carry_card_context", req.CarryCardContext, "has_extra", strings.TrimSpace(req.Extra) != "",
		"prompt_bytes", len(prompt))
```

- [ ] **Step 6: 跑测试确认绿**

Run: `go test ./internal/ledgerstep/ -v`
Expected: 全部 PASS（既有 dispatch 用例不该受影响——两个新字段零值时行为与改动前一致）。

- [ ] **Step 7: 变异测试证明网有牙齿**

把 `buildPrompt` 里 `if carry {` 临时改成 `if false {`，跑 `go test ./internal/ledgerstep/ -run TestBuildPromptThreeSections`，确认**变红**；改回来确认变绿。这一步是为了证明测试真的罩住了拼装逻辑，不是恒真断言。改回后不要留任何痕迹。

- [ ] **Step 8: 全量回归 + 格式**

Run: `go build ./... && go test ./... && gofmt -l .`

- [ ] **Step 9: Commit**

```bash
git add internal/ledgerstep/dispatch.go internal/ledgerstep/dispatch_test.go
git commit -m "feat(ledgerstep): prompt 三段拼装——模板正文+卡上下文+本次补充"
```

---

### Task 4: 通用节点执行器 NodeStep

**Files:**
- Modify: `internal/ledgerstep/node.go`（`ReviewStep` → `NodeStep`，行为由 `NodeDef` 驱动）
- Modify: `internal/ledgerstep/node_test.go`（既有 `ReviewStep` 用例改用 `NodeStep`）
- Test: `internal/ledgerstep/node_test.go`

**Interfaces:**
- Consumes: Task 1 的 `ledger.NodeDef`；既有 `Verdict`、`ParseVerdict`、`CountRounds`、`MaxRounds`、`Store.MarkNeedsHuman`、`Store.ClearNeedsHumanFrom`、`Store.RecordReviewVerdict`、`Store.AddComment`、`Store.MoveCard`、`Store.EffectiveBaseBranch`
- Produces: `NodeStep{St, Node, Dispatch, Await}`、`(*NodeStep).RunOnce(ctx, cardID) (Outcome, error)`、新增 `ActionDispatched Action = "dispatched"`

- [ ] **Step 1: 写失败的测试**

追加到 `internal/ledgerstep/node_test.go`：

```go
// newNodeStep 组一个注入了假 Dispatch/Await 的 NodeStep。
func newNodeStep(t *testing.T, st *ledger.Store, node ledger.NodeDef, message string, dispatchErr error) *NodeStep {
	t.Helper()
	return &NodeStep{
		St:   st,
		Node: node,
		Dispatch: func(ctx context.Context, c ledger.Card, n ledger.NodeDef) (string, string, error) {
			if dispatchErr != nil {
				return "", "", dispatchErr
			}
			return "linux-01", "task-1", nil
		},
		Await: func(ctx context.Context, target, taskID string) (string, error) {
			return message, nil
		},
	}
}

func TestNodeStepRejectsManualColumn(t *testing.T) {
	st, c := nodeLedger(t)
	card := c.ID
	step := newNodeStep(t, st, ledger.NodeDef{Name: "待办"}, "", nil)
	if _, err := step.RunOnce(context.Background(), card); err == nil {
		t.Fatalf("纯人工列没有可执行能力，应报错")
	}
}

func TestNodeStepDispatchOnlyReturnsDispatched(t *testing.T) {
	st, c := nodeLedger(t)
	card := c.ID
	step := newNodeStep(t, st, ledger.NodeDef{
		Name: "进行中", Dispatch: true, Template: "feature-impl",
	}, "", nil)
	out, err := step.RunOnce(context.Background(), card)
	if err != nil {
		t.Fatalf("派发型节点执行失败: %v", err)
	}
	if out.Action != ActionDispatched {
		t.Fatalf("Action = %q, want %q", out.Action, ActionDispatched)
	}
}

func TestNodeStepVerdictRoutesOnPass(t *testing.T) {
	st, c := nodeLedger(t)
	card := c.ID
	step := newNodeStep(t, st, ledger.NodeDef{
		Name: "待审阅", Dispatch: true, Verdict: true, Template: "review-generic",
		Next: "已完成", OnFail: "进行中",
	}, "报告\n```handoff-verdict\n{\"verdict\":\"pass\"}\n```", nil)
	out, err := step.RunOnce(context.Background(), card)
	if err != nil {
		t.Fatalf("裁决节点执行失败: %v", err)
	}
	if out.Action != ActionPass {
		t.Fatalf("Action = %q, want %q", out.Action, ActionPass)
	}
	got, err := st.GetCard(card)
	if err != nil {
		t.Fatalf("读卡: %v", err)
	}
	if got.Status != "已完成" {
		t.Fatalf("通过后应移到 Next «已完成»，实际 %q", got.Status)
	}
}

func TestNodeStepVerdictRoutesOnFail(t *testing.T) {
	st, c := nodeLedger(t)
	card := c.ID
	step := newNodeStep(t, st, ledger.NodeDef{
		Name: "待审阅", Dispatch: true, Verdict: true, Template: "review-generic",
		Next: "已完成", OnFail: "进行中",
	}, "报告\n```handoff-verdict\n{\"verdict\":\"fail\",\"findings\":[]}\n```", nil)
	out, err := step.RunOnce(context.Background(), card)
	if err != nil {
		t.Fatalf("裁决节点执行失败: %v", err)
	}
	if out.Action != ActionContinue {
		t.Fatalf("Action = %q, want %q", out.Action, ActionContinue)
	}
	got, _ := st.GetCard(card)
	if got.Status != "进行中" {
		t.Fatalf("未过应退到 OnFail «进行中»，实际 %q", got.Status)
	}
}

func TestNodeStepHumanBaseSkipsDispatch(t *testing.T) {
	st, _ := nodeLedger(t)
	mainCard, err := st.CreateCard(ledger.NewCard{
		Title: "主线卡", Project: "p", Workflow: "bug", BaseBranch: "main", Actor: "t",
	})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	card := mainCard.ID
	dispatched := false
	step := &NodeStep{
		St: st,
		Node: ledger.NodeDef{
			Name: "收尾合并", Dispatch: true, Verdict: true,
			Template: "review-generic", HumanBases: []string{"main"},
		},
		Dispatch: func(ctx context.Context, c ledger.Card, n ledger.NodeDef) (string, string, error) {
			dispatched = true
			return "linux-01", "task-1", nil
		},
		Await: func(ctx context.Context, target, taskID string) (string, error) { return "", nil },
	}
	out, err := step.RunOnce(context.Background(), card)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if dispatched {
		t.Fatalf("基线在 HumanBases 里，绝不允许派发")
	}
	if out.Action != ActionNeedsHuman {
		t.Fatalf("Action = %q, want %q", out.Action, ActionNeedsHuman)
	}
	views, err := st.ListCards(ledger.CardFilter{IncludeTerminal: true})
	if err != nil {
		t.Fatalf("列卡: %v", err)
	}
	marked := false
	for _, view := range views {
		if view.ID == card && view.NeedsReason != "" {
			marked = true
		}
	}
	if !marked {
		t.Fatalf("应打上等人标记")
	}
}

func TestNodeStepMaxRoundsFromNode(t *testing.T) {
	st, c := nodeLedger(t)
	card := c.ID
	node := ledger.NodeDef{
		Name: "待审阅", Dispatch: true, Verdict: true, Template: "review-generic",
		MaxRounds: 1, Next: "已完成", OnFail: "进行中",
	}
	fail := "报告\n```handoff-verdict\n{\"verdict\":\"fail\",\"findings\":[]}\n```"
	// 第一轮：正常跑，落一条 review_verdict
	if _, err := newNodeStep(t, st, node, fail, nil).RunOnce(context.Background(), card); err != nil {
		t.Fatalf("第一轮失败: %v", err)
	}
	// 第二轮：MaxRounds=1 已到顶，应直接转等人且不再派发
	dispatched := false
	step := &NodeStep{
		St: st, Node: node,
		Dispatch: func(ctx context.Context, c ledger.Card, n ledger.NodeDef) (string, string, error) {
			dispatched = true
			return "linux-01", "task-2", nil
		},
		Await: func(ctx context.Context, target, taskID string) (string, error) { return fail, nil },
	}
	out, err := step.RunOnce(context.Background(), card)
	if err != nil {
		t.Fatalf("第二轮失败: %v", err)
	}
	if dispatched {
		t.Fatalf("已到轮次上限，不该再派发")
	}
	if out.Action != ActionNeedsHuman {
		t.Fatalf("Action = %q, want %q", out.Action, ActionNeedsHuman)
	}
}
```

**helper 说明**（已对齐 `node_test.go` 的现状，不要另起炉灶）：
- `nodeLedger(t) (*ledger.Store, ledger.Card)` 已存在：开临时库、seed 出厂工作流与模板、建一张 `workflow=bug` 的卡。
- `seedMergeableCard(t, s) ledger.Card` 已存在（基线 `integration/y`），本 task 用不上但别删——Task 5 退役 MergeStep 时会连同它的用例一起清理。
- `ledger.Store` **没有** `GetCardView`，等人标记只能从 `ListCards` 返回的 `CardView.NeedsReason` 读，上面的用例已经这么写了。
- `bug` 工作流的 States 是 `待办/进行中/待审阅/已完成` 且无 gate，所以上面用例里的 `Next: "已完成"` / `OnFail: "进行中"` 都能移动成功。

- [ ] **Step 2: 跑测试确认它红**

Run: `go test ./internal/ledgerstep/ -run TestNodeStep -v`
Expected: 编译失败，`undefined: NodeStep` / `undefined: ActionDispatched`。

- [ ] **Step 3: 把 ReviewStep 改写成 NodeStep（含注释）**

在 `internal/ledgerstep/node.go` 的 `Action` 常量块里追加：

```go
	// ActionDispatched 表示本节点只负责把任务派出去，不等结果（Verdict=false）。
	ActionDispatched Action = "dispatched"
)
```

把 `ReviewStep` 结构体整体替换为：

```go
// NodeStep 通用节点执行体：一个节点该怎么跑，完全由 Node 上的能力开关决定，
// 本类型不认识「审阅」「合并」这些名字。
//
// 依赖经函数字段注入（Dispatch/Await），决策逻辑与副作用分离——单测覆盖
// 决策，真机判据覆盖副作用。
type NodeStep struct {
	St   *ledger.Store
	Node ledger.NodeDef
	// Dispatch 按节点配置把卡派出去，返回目标机与 task id。
	Dispatch func(ctx context.Context, card ledger.Card, node ledger.NodeDef) (target, taskID string, err error)
	// Await 等该 task 跑到回合终态并取回最终报文。只在 Node.Verdict 时调用。
	Await func(ctx context.Context, target, taskID string) (message string, err error)
}

// maxRounds 返回本节点的轮次封顶：节点没配就用包内默认。
func (n *NodeStep) maxRounds() int {
	if n.Node.MaxRounds > 0 {
		return n.Node.MaxRounds
	}
	return MaxRounds
}

// actor 返回本节点写事件时的署名，形如 node:待审阅。
func (n *NodeStep) actor() string { return "node:" + n.Node.Name }

// haltForHuman 落一条说明性 comment（body 非空时）并打等人标记，返回统一的 Outcome。
//
// why 抽出来：这条「留痕 + 打旗 + 返回」三件套在本文件里出现五次，每次漏掉
// 其中任何一件，卡在看板上都会看着一切正常而实际没人在推它。
func (n *NodeStep) haltForHuman(cardID, reason, body string) (Outcome, error) {
	if body != "" {
		if _, err := n.St.AddComment(cardID, body, "普通", n.actor()); err != nil {
			return Outcome{}, err
		}
	}
	if err := n.St.MarkNeedsHuman(cardID, reason, n.actor()); err != nil {
		return Outcome{}, err
	}
	return Outcome{Action: ActionNeedsHuman, Reason: reason}, nil
}

// routeTo 把卡移到 to 列；to 为空表示停在本列（不是错误）。
//
// 返回：移动失败时返回错误原文，由调用方转等人——门槛没过（如「待合并」要求
// 验收判据非空）是常态而不是异常，硬失败会让已经落账的裁决白跑。
func (n *NodeStep) routeTo(cardID, to string) error {
	if to == "" {
		return nil
	}
	card, err := n.St.GetCard(cardID)
	if err != nil {
		return err
	}
	if card.Status == to {
		return nil
	}
	return n.St.MoveCard(cardID, to, card.Status, n.actor())
}

// RunOnce 跑一次本节点。
//
// 参数：cardID 卡。
// 返回：Outcome（下一步动作 + 裁决 + 理由）；只有「本节点根本不该被执行」
// （纯人工列）和账本写失败才返回 error，其余异常一律转成 needs_human 并留痕。
//
// 阻塞行为：Node.Verdict 为真时会阻塞到被派出去的 task 跑到回合终态——几分钟
// 到几十分钟，executor 挂在 waiting_answer 时更久。调用方自行决定要不要放
// goroutine 里跑。
func (n *NodeStep) RunOnce(ctx context.Context, cardID string) (Outcome, error) {
	logger := slog.Default().With("node", n.Node.Name, "card", cardID)
	logger.Info("进入节点",
		"dispatch", n.Node.Dispatch, "verdict", n.Node.Verdict,
		"template", n.Node.Template, "max_rounds", n.maxRounds())

	if !n.Node.Dispatch {
		// 纯人工列没有可执行能力。这不是「什么都不做」而是配置错误——
		// 界面上不该给这种列画执行按钮，走到这里说明调用方绕过了判断。
		logger.Warn("纯人工列被要求执行")
		return Outcome{}, fmt.Errorf("节点 %q 没有 Dispatch 能力，不可执行", n.Node.Name)
	}
	card, err := n.St.GetCard(cardID)
	if err != nil {
		return Outcome{}, err
	}
	base, err := n.St.EffectiveBaseBranch(cardID)
	if err != nil {
		return Outcome{}, err
	}
	for _, human := range n.Node.HumanBases {
		if base != human {
			continue
		}
		reason := fmt.Sprintf("基线 %s 在本节点的人工清单里：不自动执行", base)
		logger.Info("基线命中人工清单，跳过派发", "base", base)
		return n.haltForHuman(cardID, reason, "")
	}
	if n.Node.Verdict {
		events, err := n.St.EventsFromAsc([]string{cardID}, 0, 10000)
		if err != nil {
			return Outcome{}, err
		}
		rounds := CountRounds(events, n.Node.Name)
		logger.Info("读取裁决回合数", "rounds", rounds, "max_rounds", n.maxRounds())
		if rounds >= n.maxRounds() {
			reason := fmt.Sprintf("裁决超轮（%d/%d）", rounds, n.maxRounds())
			logger.Info("回合封顶转等人", "rounds", rounds)
			return n.haltForHuman(cardID, reason, "")
		}
	}

	target, taskID, err := n.Dispatch(ctx, card, n.Node)
	if err != nil {
		// 派发失败同样要上浮到「需要你」：卡上不留痕 = 这张卡在看板上看着
		// 一切正常，而实际没人在推它。原文落 timeline 供取证。
		logger.Warn("派发失败，转等人", "cause", err)
		return n.haltForHuman(cardID, "派发失败", "本节点派发失败：\n"+err.Error())
	}
	logger.Info("已派发", "target", target, "task", taskID)
	if !n.Node.Verdict {
		// 不裁决的节点到此为止：任务在对端跑，进展看卡的事件流与 handoff task。
		logger.Info("节点结束（只派发不裁决）", "action", string(ActionDispatched))
		return Outcome{Action: ActionDispatched}, nil
	}

	message, err := n.Await(ctx, target, taskID)
	if err != nil {
		logger.Warn("未取到报文，转等人", "cause", err)
		return n.haltForHuman(cardID, "未取到裁决报文", "本节点未取到裁决报文：\n"+err.Error())
	}
	verdict, parseErr := ParseVerdict(message)
	if parseErr != nil {
		logger.Info("裁决解析失败转等人", "cause", parseErr)
		return n.haltForHuman(cardID, "裁决解析失败", "裁决解析失败，报文原文：\n"+message)
	}
	if err := n.St.RecordReviewVerdict(cardID, n.Node.Name, verdict.Pass, verdict.Raw, n.actor()); err != nil {
		return Outcome{}, err
	}
	logger.Info("裁决落账", "pass", verdict.Pass, "findings", len(verdict.Findings))
	// 裁决落账即代表本节点这一轮真的跑通了。此前若因派发失败、报文取不到或
	// 裁决解析不了打过等人标记，那条标记已被这一轮推翻，由打它的同一个节点撤回
	// ——不撤的话卡上会一直挂着一面已经不成立的红旗，而看板的「需要你」筛选
	// 正是靠它（2026-08-20 真机看到过陈标记挂在抽屉顶上且 Web 无撤除入口）。
	//
	// 失败只告警不中断：裁决已落账，为一次收尾清理失败而让整个节点报错，
	// 代价比留一条陈标记大。
	if cleared, cerr := n.St.ClearNeedsHumanFrom(cardID, n.actor()); cerr != nil {
		logger.Warn("撤回本节点旧等人标记失败", "cause", cerr)
	} else if cleared {
		logger.Info("已撤回本节点此前的等人标记")
	}

	to, action := n.Node.OnFail, ActionContinue
	if verdict.Pass {
		to, action = n.Node.Next, ActionPass
	}
	if err := n.routeTo(cardID, to); err != nil {
		// 门槛没过是常态（例如「待合并」要求验收判据非空），转等人而不是硬失败：
		// 裁决已经落账了，为一次移动失败把整轮判成错误会让人看不出发生了什么。
		reason := fmt.Sprintf("裁决已落账但移到 %q 失败", to)
		logger.Warn("路由失败转等人", "to", to, "cause", err)
		return n.haltForHuman(cardID, reason, reason+"：\n"+err.Error())
	}
	logger.Info("节点结束", "action", string(action), "moved_to", to)
	return Outcome{Action: action, Verdict: verdict}, nil
}
```

- [ ] **Step 4: 把 node_test.go 里的既有 ReviewStep 用例改成 NodeStep**

既有用例构造的是 `&ReviewStep{St: ..., Step: "review", RunReview: fn}`。逐个改成：

```go
	&NodeStep{
		St:   st,
		Node: ledger.NodeDef{Name: "review", Dispatch: true, Verdict: true, Template: "review-generic"},
		Dispatch: func(ctx context.Context, c ledger.Card, n ledger.NodeDef) (string, string, error) {
			return "t", "task", nil
		},
		Await: func(ctx context.Context, target, taskID string) (string, error) { return fn(ctx, card) },
	}
```

其中 `fn` 是原来那个 `RunReview`。**注意两处语义差**，改的时候要一并调整断言：
1. 原 `ReviewStep` 通过/失败都**不移动卡**；`NodeStep` 在 `Next`/`OnFail` 非空时会移。上面构造里两者都留空，行为与原来一致，断言不用改。
2. 原 `RunReview` 返回 error 对应现在的 `Await` 返回 error（派发成功、等报文失败），语义一致。

- [ ] **Step 5: 跑测试确认绿**

Run: `go test ./internal/ledgerstep/ -v`
Expected: 新老用例全部 PASS。若 `MergeStep` 相关用例因编译顺序报错，**先不要动它们**——Task 5 才退役 MergeStep。

- [ ] **Step 6: 变异测试证明路由网有牙齿**

把 `RunOnce` 里 `to, action := n.Node.OnFail, ActionContinue` 临时改成 `to, action := n.Node.Next, ActionContinue`，跑
`go test ./internal/ledgerstep/ -run TestNodeStepVerdictRoutesOnFail`，确认**变红**；改回确认变绿。同样对 `HumanBases` 循环：把 `if base != human { continue }` 改成 `if true { continue }`，确认 `TestNodeStepHumanBaseSkipsDispatch` 变红后改回。

- [ ] **Step 7: 全量回归 + 格式**

Run: `go build ./... && go vet ./... && go test ./... && gofmt -l .`

- [ ] **Step 8: Commit**

```bash
git add internal/ledgerstep/node.go internal/ledgerstep/node_test.go
git commit -m "feat(ledgerstep): ReviewStep 通用化为 NodeStep，行为由节点能力开关驱动"
```

---

### Task 5: StepRunner 通用化，本地合并退役

**Files:**
- Modify: `internal/ledgerstep/runner.go`（`Run` 按节点名取 `NodeDef`）
- Modify: `internal/ledgerstep/wire.go`（`NewDispatchReview` 拆成 `nodeDispatch` + `nodeAwait`）
- Modify: `internal/ledgerstep/node.go`（删 `MergeStep`、`mergeFailureReason`、`defaultMainLine`、`ActionMerged`）
- Delete: `internal/ledgerstep/gitscript.go`、`internal/ledgerstep/gitscript_test.go`
- Modify: `internal/agentd/cardstep.go`、`cmd/card_node.go`
- Test: `internal/ledgerstep/runner_test.go`（若无则新建）、`internal/ledgerstep/wire_test.go`

**Interfaces:**
- Consumes: Task 4 的 `NodeStep`；Task 3 的 `TemplateDispatch{CarryCardContext, Extra}`；`ledger.Store.GetCard/GetWorkflow`
- Produces: `StepRunner{St, Dispatcher, Clients, Target, Extra}`、`(*StepRunner).Run(ctx, cardID, nodeName) (Outcome, error)`；`StepRunner` 不再有 `RepoDir` / `MainLine`

- [ ] **Step 1: 写失败的测试**

新建 `internal/ledgerstep/runner_test.go`：

```go
package ledgerstep

import (
	"context"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func TestRunnerRejectsUnknownNode(t *testing.T) {
	st, c := nodeLedger(t)
	card := c.ID
	runner := &StepRunner{St: st}
	_, err := runner.Run(context.Background(), card, "查无此节点")
	if err == nil {
		t.Fatalf("节点名不在卡钉的工作流里，应报错")
	}
	if !strings.Contains(err.Error(), "查无此节点") {
		t.Fatalf("错误里应带上节点名，实际: %v", err)
	}
}

func TestRunnerFindsNodeInPinnedWorkflowVersion(t *testing.T) {
	st, _ := nodeLedger(t)
	if _, err := st.PutWorkflow("nodeflow", ledger.WorkflowDef{Nodes: []ledger.NodeDef{
		{Name: "待办", Next: "进行中"},
		{Name: "进行中"},
	}}); err != nil {
		t.Fatalf("写工作流: %v", err)
	}
	card, err := st.CreateCard(ledger.NewCard{
		Title: "找节点", Project: "p", Workflow: "nodeflow", Actor: "t",
	})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	runner := &StepRunner{St: st}
	// 「待办」在工作流里存在但没有 Dispatch 能力：应报「不可执行」而不是「找不到」。
	_, err = runner.Run(context.Background(), card.ID, "待办")
	if err == nil || !strings.Contains(err.Error(), "Dispatch") {
		t.Fatalf("应报缺 Dispatch 能力，实际: %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认它红**

Run: `go test ./internal/ledgerstep/ -run TestRunner -v`
Expected: FAIL（`Run` 还在 switch review|merge，会报「环节只认 review|merge」）。

- [ ] **Step 3: 重写 StepRunner.Run（含注释）**

把 `internal/ledgerstep/runner.go` 的 `StepRunner` 与 `Run` / `reviewDispatch` 整体替换为：

```go
// StepRunner 节点执行的装配器。依赖全部显式注入，调用方各填各的。
//
// 本地仓路径与主线名已随本地合并退役一并删除：合并现在是普通派发节点，
// 由 executor 在任务分支上完成，协调机不再执行任何 git 写操作。
type StepRunner struct {
	St         *ledger.Store
	Dispatcher *Dispatcher
	// Clients 按 target 名取一个已装配好的 agentd 客户端。
	//
	// why 这里要的是客户端而不是 (addr, token)：relay 形态的机器根本没有 addr，
	// 拿地址自己 client.New 对它们恒失败（会退化成一个没有 Host 的 URL）。
	// 选路归 agentd 的 target 客户端池管，本包只消费。
	Clients func(target string) (*client.Client, error)
	// Target 覆盖节点/模板里的目标机；空则用节点覆盖或模板的 target。
	Target string
	// Extra 本次执行的临时补充说明，透传进 prompt 的第三段；可为空。
	Extra string
}

// Run 跑一次节点。
//
// 参数：cardID 卡；nodeName 节点名（= 看板的列名），从卡钉的工作流版本里查。
// 返回：Outcome；节点不存在、没有 Dispatch 能力或执行内部失败时返回错误。
//
// 阻塞行为：节点开了 Verdict 时会阻塞到被派出去的 task 跑到回合终态
// （几分钟到几十分钟）。调用方要么放 goroutine 里跑（agentd 就是这么做的），
// 要么接受前台阻塞（CLI）。
func (r *StepRunner) Run(ctx context.Context, cardID, nodeName string) (Outcome, error) {
	slog.Default().Info("进入节点执行", "card", cardID, "node", nodeName)
	node, err := r.nodeFor(cardID, nodeName)
	if err != nil {
		return Outcome{}, err
	}
	step := &NodeStep{
		St:       r.St,
		Node:     node,
		Dispatch: r.dispatchNode(),
		Await:    r.awaitNode(),
	}
	return step.RunOnce(ctx, cardID)
}

// nodeFor 在卡**钉住的**工作流版本里按名字找节点。
//
// why 一定要用卡钉的版本而不是最新版：工作流是不可变版本化的，卡在建卡时
// 钉了版本号。拿最新版去解释一张老卡，等于用今天的流程图判昨天的卡走到哪了。
func (r *StepRunner) nodeFor(cardID, nodeName string) (ledger.NodeDef, error) {
	card, err := r.St.GetCard(cardID)
	if err != nil {
		return ledger.NodeDef{}, err
	}
	workflow, err := r.St.GetWorkflow(card.WorkflowName, card.WorkflowVersion)
	if err != nil {
		return ledger.NodeDef{}, fmt.Errorf("取卡 %s 钉住的工作流 %s v%d: %w",
			cardID, card.WorkflowName, card.WorkflowVersion, err)
	}
	for _, node := range workflow.Def.Nodes {
		if node.Name == nodeName {
			return node, nil
		}
	}
	return ledger.NodeDef{}, fmt.Errorf("节点 %q 不在卡 %s 的工作流 %s v%d 里",
		nodeName, cardID, card.WorkflowName, card.WorkflowVersion)
}

// dispatchNode 生产 NodeStep.Dispatch：按节点的模板引用 + 单字段覆盖派发。
func (r *StepRunner) dispatchNode() func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
	return func(ctx context.Context, card ledger.Card, node ledger.NodeDef) (string, string, error) {
		target := r.Target
		if target == "" {
			target = node.Override.Target
		}
		result, err := r.Dispatcher.ViaTemplate(ctx, card, TemplateDispatch{
			Template:           node.Template,
			Target:             target,
			DisciplineOverride: node.Override.Discipline,
			ExecutorOverride:   node.Override.Executor,
			ModelOverride:      node.Override.Model,
			CarryCardContext:   node.CarryCardContext,
			Extra:              r.Extra,
		})
		if err != nil {
			return "", "", err
		}
		return result.Target, result.Task, nil
	}
}

// awaitNode 生产 NodeStep.Await：等回合终态并取最终报文，取到后归档该 task。
func (r *StepRunner) awaitNode() func(context.Context, string, string) (string, error) {
	return func(ctx context.Context, target, taskID string) (string, error) {
		cl, err := r.Clients(target)
		if err != nil {
			return "", err
		}
		if err := waitForTurnEnd(ctx, func(ctx context.Context) (*proto.Event, error) {
			return cl.WaitEvent(ctx, taskID, false)
		}); err != nil {
			return "", fmt.Errorf("等回合终态: %w", err)
		}
		message, err := clientFinalMessage(ctx, cl, taskID)
		if err != nil {
			return "", err
		}
		// 报文已经拿到，归档只是回收资源；失败不该把报文丢掉，所以带着报文一起返回错误。
		if _, err := cl.Done(ctx, taskID, ""); err != nil {
			slog.Default().Warn("归档节点 task 失败（报文已取到）", "task", taskID, "cause", err)
		}
		return message, nil
	}
}
```

`runner.go` 的 import 增补 `"github.com/Xsxdot/handoff/internal/proto"`。

- [ ] **Step 4: 给 TemplateDispatch 补执行者/模型覆盖**

`internal/ledgerstep/dispatch.go` 的 `TemplateDispatch` 增两个字段：

```go
	// ExecutorOverride / ModelOverride 是节点对模板的单字段覆盖；空 = 用模板的。
	ExecutorOverride string
	ModelOverride    string
```

`ViaTemplate` 里，`model` 计算之后插入：

```go
	executor := tpl.Def.Executor
	if req.ExecutorOverride != "" {
		executor = req.ExecutorOverride
	}
	if req.ModelOverride != "" {
		model = req.ModelOverride
	}
```

并把 `d.Transport(...)` 的 `Executor: tpl.Def.Executor` 改成 `Executor: executor`。Step 5 的日志行里 `"executor", tpl.Def.Executor` 同步改成 `"executor", executor`。

- [ ] **Step 5: 删掉 wire.go 里的 NewDispatchReview**

`internal/ledgerstep/wire.go` 里删除 `NewDispatchReview` 整个函数（`waitForTurnEnd` / `clientFinalMessage` / `finalMessageFromEvents` **保留**，`runner.go` 现在用它们）。`wire_test.go` 里针对 `NewDispatchReview` 的用例一并删除；`finalMessageFromEvents` 的用例保留。

- [ ] **Step 6: 退役本地合并**

- `internal/ledgerstep/node.go`：删除 `MergeStep` 结构体、`defaultMainLine`、`isMainline`、`mergeFailureReason`、`MergeStep.RunOnce`，以及 `Action` 常量块里的 `ActionMerged`。
- 删文件：`git rm internal/ledgerstep/gitscript.go internal/ledgerstep/gitscript_test.go`
- `internal/ledgerstep/wire.go`：删除 `NewLocalObjective`、`NewLocalMerge` 及只被它们用到的辅助（`ErrWorkBranchMissing` 若别处还在用则保留，用 `grep -rn ErrWorkBranchMissing` 确认）。
- `internal/ledgerstep/node_test.go` / `wire_test.go`：删除全部 `MergeStep` / 本地合并相关用例。

在 `node.go` 的包注释里补一句说明，避免后人以为是漏删：

```go
// 本地合并已于 2026-08-21 退役：合并改为普通派发节点（Dispatch+Verdict +
// finishing 纪律块），由 executor 在任务分支上完成，协调机不再执行 git 写操作。
```

- [ ] **Step 7: 更新两个装配点**

`internal/agentd/cardstep.go`：
- `startCardStep` 的签名与校验里，删掉 `if step != "review" && step != "merge"` 那段（节点名的合法性由 `StepRunner.Run` 判，它才知道卡钉的工作流有哪些节点）。
- 删掉 `s.st.GetProjectLocationByName(card.Project)` 那段与 `loc`：本地仓路径随本地合并一起退役了。**同时删掉现在已经用不上的 `card` 变量赋值**（`GetCard` 若只为取 Project，一并删；若还用于日志则保留并在日志里带上）。
- `runner := &ledgerstep.StepRunner{...}` 去掉 `RepoDir` 字段。
- 参数名 `step` 全部改叫 `node`，日志键从 `"step"` 改成 `"node"`，注释里的「环节只认 review|merge」改成「节点名由卡钉的工作流决定」。
- 文件头注释里「不做实现类派发：那要挂 plan 文件，浏览器里没有」这条**保留**（本 plan 不改这一点）。

`cmd/card_node.go`：
- `runner := &ledgerstep.StepRunner{...}` 去掉 `RepoDir: cardDispatchRepo`。
- 参数 `step` 改名 `node`，注释同步。
- 若 `cardDispatchRepo` 变量因此无人使用，用 `grep -rn cardDispatchRepo cmd/` 确认后删掉它及其 flag 注册；**如果别的子命令还在用就保留**。

- [ ] **Step 8: 跑测试确认绿**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: 全绿。编译报错多半来自漏删的 `MergeStep` 引用——按报错逐个清。

- [ ] **Step 9: 全量格式**

Run: `gofmt -l .`
Expected: 无输出。

- [ ] **Step 10: Commit**

```bash
git add -A internal/ledgerstep internal/agentd/cardstep.go cmd/card_node.go
git commit -m "refactor(ledgerstep): StepRunner 按节点名通用执行，本地合并退役"
```

---

### Task 6: 出厂 seed——三份纪律块与节点形默认工作流

**Files:**
- Create: `internal/discipline/builtin/spec-draft.md`、`internal/discipline/builtin/plan-writing.md`、`internal/discipline/builtin/finishing.md`
- Modify: `internal/discipline/discipline.go`
- Modify: `internal/ledger/workflows.go`（`EnsureDefaultWorkflows` 改节点形）
- Test: `internal/discipline/resolver_test.go`、`internal/ledger/workflows_test.go`

**Interfaces:**
- Consumes: 既有 `discipline.NameImplement`/`NameReview`、`builtinByName`、`Builtins()`；Task 1/2 的 `NodeDef` 与校验
- Produces: `discipline.NameSpecDraft = "spec-draft"`、`NamePlanWriting = "plan-writing"`、`NameFinishing = "finishing"`；节点形的 `feature`/`bug` 出厂工作流

- [ ] **Step 1: 写失败的测试**

追加到 `internal/discipline/resolver_test.go`：

```go
func TestBuiltinByNameCoversNewRoles(t *testing.T) {
	for _, name := range []string{NameImplement, NameReview, NameSpecDraft, NamePlanWriting, NameFinishing} {
		block, ok := builtinByName(name, "codex")
		if !ok {
			t.Fatalf("角色 %q 没有内置纪律块", name)
		}
		if strings.TrimSpace(block.Text) == "" {
			t.Fatalf("角色 %q 的内置纪律块是空的", name)
		}
		if block.Source == "" {
			t.Fatalf("角色 %q 的 Source 为空", name)
		}
	}
}

func TestBuiltinsListStableAndComplete(t *testing.T) {
	got := Builtins()
	if len(got) != 6 {
		t.Fatalf("内置纪律块应有 6 份（subagent/single-context/review/spec-draft/plan-writing/finishing），得到 %d", len(got))
	}
	// 顺序固定：控制台用 builtins[0] 当默认选中项，换位置会静默改掉用户看到的内容。
	if got[0].Tier != TierSubagent || got[1].Tier != TierSingleContext || got[2].Tier != NameReview {
		t.Fatalf("前三项顺序被改动: %+v", got[:3])
	}
}

func TestFinishingBlockCarriesBaseDiscipline(t *testing.T) {
	block, _ := builtinByName(NameFinishing, "codex")
	for _, want := range []string{"基线", "不要", "裁决"} {
		if !strings.Contains(block.Text, want) {
			t.Fatalf("收尾纪律块缺关键约束 %q", want)
		}
	}
}
```

追加到 `internal/ledger/workflows_test.go`：

```go
func TestDefaultWorkflowsAreNodeForm(t *testing.T) {
	s := newTestStore(t)
	if err := s.EnsureDefaultTemplates(); err != nil {
		t.Fatalf("seed 模板: %v", err)
	}
	if err := s.EnsureDefaultWorkflows(); err != nil {
		t.Fatalf("seed 工作流: %v", err)
	}
	feature, err := s.GetWorkflow("feature", 0)
	if err != nil {
		t.Fatalf("读 feature 流: %v", err)
	}
	if len(feature.Def.Nodes) == 0 {
		t.Fatalf("出厂工作流应是节点形")
	}
	var review, merge *NodeDef
	for i := range feature.Def.Nodes {
		switch feature.Def.Nodes[i].Name {
		case "待审阅":
			review = &feature.Def.Nodes[i]
		case "待合并":
			merge = &feature.Def.Nodes[i]
		}
	}
	if review == nil || !review.Dispatch || !review.Verdict || !review.CarryCardContext {
		t.Fatalf("待审阅应是派发+裁决+带卡上下文: %+v", review)
	}
	if review.OnFail != "进行中" {
		t.Fatalf("审阅未过应退回进行中，实际 %q", review.OnFail)
	}
	if merge == nil || !merge.Dispatch {
		t.Fatalf("待合并应是派发型节点: %+v", merge)
	}
	// main 上的合并必须留人工——出厂默认不能自动往主线合。
	found := false
	for _, base := range merge.HumanBases {
		if base == "main" {
			found = true
		}
	}
	if !found {
		t.Fatalf("出厂合并节点必须把 main 列入人工清单: %+v", merge.HumanBases)
	}
	// States 投影必须仍在，看板与 MoveCard 靠它。
	if len(feature.Def.States) != len(feature.Def.Nodes) {
		t.Fatalf("States 投影缺失: %v", feature.Def.States)
	}
}

func TestEnsureDefaultWorkflowsDoesNotOverwrite(t *testing.T) {
	s := newTestStore(t)
	if err := s.EnsureDefaultTemplates(); err != nil {
		t.Fatalf("seed 模板: %v", err)
	}
	if _, err := s.PutWorkflow("feature", WorkflowDef{Nodes: []NodeDef{{Name: "我自己的列"}}}); err != nil {
		t.Fatalf("写用户版本: %v", err)
	}
	if err := s.EnsureDefaultWorkflows(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, _ := s.GetWorkflow("feature", 0)
	if len(got.Def.Nodes) != 1 || got.Def.Nodes[0].Name != "我自己的列" {
		t.Fatalf("seed 覆盖了用户改过的工作流: %+v", got.Def.Nodes)
	}
}
```

- [ ] **Step 2: 跑测试确认它红**

Run: `go test ./internal/discipline/ ./internal/ledger/ -run 'Builtin|Finishing|DefaultWorkflows' -v`
Expected: 编译失败 `undefined: NameSpecDraft` 等。

- [ ] **Step 3: 写三份纪律块正文**

`internal/discipline/builtin/spec-draft.md`：

```markdown
# 执行纪律：出 spec

你这一轮的产出是**一份设计文档（spec）**，不是代码。

## 必须做

1. 先读懂现状：把卡上下文里给的基线分支、验收判据、已有附件读完，再动笔。
2. 遇到「有两种做法、选哪种取决于用户偏好」的岔口，**发工单问**，不要自己选一个然后往下写。
3. spec 写进 `docs/superpowers/specs/YYYY-MM-DD-<主题>-design.md`，内容至少覆盖：
   目标、架构、数据模型、错误处理、测试策略、明确不做的事（YAGNI）。
4. 写完自查一遍：有没有 TBD/TODO、有没有前后矛盾、有没有一句话能有两种理解。
   有就当场改掉。
5. 提交这份文档，commit message 用 `docs(spec): …`。

## 不要做

- **不要写实现代码**。这一轮只出文档。
- **不要改基线分支以外的任何分支**，也不要 push 到卡上下文给出的基线之外的地方。
- 不要派发任务、不要调用 handoff CLI、不要起任何新的 executor 进程。
- 没跑到结果不许写结论：凡是「我认为应该能」的判断，要么去验证，要么如实写「未验证」。

## 收尾

最后一条消息里说明：文档路径、覆盖了哪些决策、哪些地方还悬而未决。
```

`internal/discipline/builtin/plan-writing.md`：

```markdown
# 执行纪律：写实现计划

你这一轮的产出是**一份实现计划（plan）**，不是代码。

## 必须做

1. 读完卡上下文里挂的 spec 附件再动笔。spec 里没写的需求不要自己加。
2. 计划写进 `docs/superpowers/plans/YYYY-MM-DD-<主题>.md`，按 task 拆分，
   每个 task 都要有：改哪些文件、失败的测试（**贴真实代码，不是描述**）、
   最小实现、跑测试的确切命令与预期、commit。
3. **每个实现类 task 必须显式包含两步**：
   - 「加关键节点日志」：进入关键操作、外部调用前后、每个错误分支、状态变更、
     成功路径的结果。用项目自己的结构化 logger，**不许用 print/fmt.Printf**。
   - 「加注释」：新文件的职责+边界头注释、导出方法的参数/返回/注意事项、
     复杂分支解释「为什么」而不是「做了什么」。
   缺这两步的计划是不合格的计划。
4. 计划里不许出现占位符：TBD、TODO、「适当处理错误」、「类似 Task N」、
   「为上面写测试」而不给测试代码——这些都是计划失败。
5. 写完自查：spec 的每条需求都能指到某个 task 吗？后面 task 用到的类型和函数名
   在前面 task 里定义过吗？名字前后一致吗？

## 不要做

- **不要写实现代码**，不要顺手把计划里的第一个 task 做掉。
- 不要改基线分支以外的分支。
- 不要派发任务、不要调用 handoff CLI、不要起任何新的 executor 进程。

## 收尾

最后一条消息里说明：计划路径、拆了几个 task、哪些地方风险最高。
```

`internal/discipline/builtin/finishing.md`：

```markdown
# 执行纪律：收尾与合并

你这一轮要把一条已经做完的分支收干净并合进它的基线。

## 合并目标只有一个来源

**合并目标 = 卡上下文里写明的「有效基线分支」。** 它每张卡都不同，不要自己推断，
不要默认 main，不要因为「看起来像主线」就改目标。卡上下文里没给基线时，
**发工单问协调者**，不要自己挑一个。

## 顺序（不许跳步）

1. **先验证再合并**：在当前分支上跑项目的完整测试与格式检查，全绿才继续。
   有红就停下——把失败原文写进裁决，不要修完再合（修是另一轮的事）。
2. 确认工作区干净、该提交的都提交了。
3. 把基线分支拉到最新，再把本分支合进去。有冲突就停下，把冲突文件清单写进裁决，
   **不要自己猜着解冲突**。
4. 合并后在基线上再跑一次完整测试。这一步不能省——合并本身会引入语义冲突。

## 不要做

- **不要 push 到卡上下文给出的基线之外的任何分支**，尤其不要 push main。
- 不要 force push、不要改写已推送的历史、不要删远端分支。
- 不要派发任务、不要调用 handoff CLI、不要起任何新的 executor 进程。
- 没跑到结果不许写结论：每一条「已验证」都要有真实执行过的命令与输出支撑。

## 裁决块（必须输出）

最后一条消息里必须带一个裁决块，协调者靠它决定卡往哪走：

```handoff-verdict
{"verdict":"pass","notes":"已合入 <基线分支>，合并后全量测试 N 项全绿"}
```

未通过时用 `"verdict":"fail"`，并在 `findings` 里逐条写清什么没过、在哪个文件。
```

- [ ] **Step 4: 注册三份纪律块（含注释）**

`internal/discipline/discipline.go`：在 `builtinReview` 的 embed 附近追加：

```go
// builtinSpecDraft / builtinPlanWriting / builtinFinishing 是 superpowers
// 三个阶段的纪律块改写版。它们是**数据**：用户可以在控制台以此为模板新建
// 并任意微调，出厂内置只保证「开箱就有一份能用的」。
//
//go:embed builtin/spec-draft.md
var builtinSpecDraft string

//go:embed builtin/plan-writing.md
var builtinPlanWriting string

//go:embed builtin/finishing.md
var builtinFinishing string
```

名字常量块里追加：

```go
	NameSpecDraft   = "spec-draft"   // 出 spec 角色；只出文档，不写代码
	NamePlanWriting = "plan-writing" // 写 plan 角色；只出计划，不写代码
	NameFinishing   = "finishing"    // 收尾合并角色；合并目标取自卡的有效基线
```

`builtinByName` 的 switch 里追加三个 case：

```go
	case NameSpecDraft:
		return Block{Text: builtinSpecDraft, Source: "内置:" + NameSpecDraft}, true
	case NamePlanWriting:
		return Block{Text: builtinPlanWriting, Source: "内置:" + NamePlanWriting}, true
	case NameFinishing:
		return Block{Text: builtinFinishing, Source: "内置:" + NameFinishing}, true
```

`Builtins()` 的返回值**追加在末尾**（不要插在前面，注释已说明原因）：

```go
		{Tier: NameSpecDraft, Content: builtinSpecDraft},
		{Tier: NamePlanWriting, Content: builtinPlanWriting},
		{Tier: NameFinishing, Content: builtinFinishing},
```

- [ ] **Step 5: 改 EnsureDefaultWorkflows 为节点形（含注释）**

`internal/ledger/workflows.go` 的 `defaults` 替换为：

```go
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
	}
```

`workflows.go` 需要 import `"github.com/Xsxdot/handoff/internal/discipline"`。**这不会成环**：`internal/ledger/templates.go` 已经 import 了它（用 `discipline.NameImplement`），方向是 ledger → discipline，单向。

**必须同时做的一件事——否则会连坐炸掉 11 处调用点。** 节点形 seed 引用了
`feature-impl` / `review-generic` 两个模板，而 Task 2 的校验会去查模板存在性；
仓库里现存的 **11 处** `EnsureDefaultWorkflows` 调用点（`cmd/agentd.go:270`、
`cmd/ledgercli.go:42`、`internal/ledger/workflows_test.go`、`internal/ledger/cards_test.go`、
`internal/ledger/store_pg_test.go`、`internal/ledgermirror/mirror_test.go`、
`internal/ledgerstep/node_test.go`、`internal/ledgerstep/dispatch_test.go`、
`internal/ledgerstep/wire_test.go`、`internal/agentd/ledgerapi_test.go` ×2）
**无一例外都是先 seed 工作流、后 seed 模板**，改完全都会开始 seed 失败。

**不要去逐处调换顺序**——漏一处就是一片红，而且下次谁新写一个调用点还会再踩。
正确做法是让 `EnsureDefaultWorkflows` 自己保证前置条件：

```go
// EnsureDefaultWorkflows 幂等 seed 出厂工作流。已存在同名的不动（不覆盖用户改过的版本）。
//
// 注意：**本方法会先调 EnsureDefaultTemplates**。出厂工作流的节点引用出厂模板，
// 而 PutWorkflow 会校验模板存在性——把顺序要求写进文档等于把它留给每个调用点
// 各自记住，仓库里 11 处调用点原本全是反的。两个 seed 都幂等，合并调用没有代价。
func (s *Store) EnsureDefaultWorkflows() error {
	if err := s.EnsureDefaultTemplates(); err != nil {
		return fmt.Errorf("seed 出厂工作流前置的模板: %w", err)
	}
	// ...原有 defaults 与循环...
}
```

改完后 `grep -rn "EnsureDefaultWorkflows" --include="*.go" .` 的调用点**一处都不用改**，
但要跑一遍全量测试确认确实没有遗漏的依赖。

- [ ] **Step 6: 加关键节点日志**

`EnsureDefaultWorkflows` 的 seed 失败路径要带上下文——把

```go
		if _, err := s.PutWorkflow(name, def); err != nil {
			return err
		}
```

改成

```go
		if _, err := s.PutWorkflow(name, def); err != nil {
			log().Error("seed 默认工作流失败", "name", name, "cause", err)
			return fmt.Errorf("seed 默认工作流 %s: %w", name, err)
		}
```

- [ ] **Step 7: 跑测试确认绿**

Run: `go test ./internal/discipline/ ./internal/ledger/ -v -count=1`
Expected: 全部 PASS。

- [ ] **Step 8: 变异测试**

把 `feature` 流里 `HumanBases: []string{"main"}` 临时删掉，跑
`go test ./internal/ledger/ -run TestDefaultWorkflowsAreNodeForm`，确认**变红**；改回确认变绿。

- [ ] **Step 9: 全量回归 + 格式**

Run: `go build ./... && go vet ./... && go test ./... -count=1 && gofmt -l .`

- [ ] **Step 10: Commit**

```bash
git add internal/discipline internal/ledger/workflows.go internal/ledger/workflows_test.go
git commit -m "feat(discipline,ledger): seed 出 spec/写 plan/收尾三份纪律块与节点形出厂工作流"
```

---

### Task 7: HTTP API——工作流读写、纪律块名列表、节点名执行

**Files:**
- Modify: `internal/agentd/ledgerapi.go`
- Test: `internal/agentd/ledgerapi_test.go`

**Interfaces:**
- Consumes: Task 1/2 的 `PutWorkflow`/`GetWorkflow`/`ErrBadState`；Task 6 的 `discipline.Builtins()`、`discipline.List(dir)`；既有 `s.withLedger`、`writeJSON`、`s.ledger`
- Produces: `GET /api/flows/{name}`、`PUT /api/flows/{name}`、`GET /api/disciplines`；`POST /api/cards/{id}/step` 的 `step` 参数改收任意节点名

- [ ] **Step 1: 写失败的测试**

追加到 `internal/agentd/ledgerapi_test.go`。**先照 `ledgerPost` 的形状补一个 `ledgerPut`**（文件里现在只有 GET/POST）：

```go
func ledgerPut(t *testing.T, env *testAgentdEnv, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, env.ts.URL+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(data)
}
```

然后追加用例。注意三处既有约定：`ledgerGet`/`ledgerPost`/`ledgerPut` 收的是
`*testAgentdEnv`（`ledgerEnv` 内嵌了它，传 `env.testAgentdEnv`）；它们返回
`(状态码, 响应体字符串)` 而不是 recorder；`seedCard` 收 title 且返回
`ledger.Card`。

```go
func TestFlowGetReturnsNodes(t *testing.T) {
	env := newLedgerEnv(t)
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/flows/feature")
	if code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", code, body)
	}
	var got struct {
		Name    string `json:"name"`
		Version int    `json:"version"`
		Nodes   []struct {
			Name     string `json:"name"`
			Dispatch bool   `json:"dispatch"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("解码: %v（原文 %s）", err, body)
	}
	if got.Name != "feature" || got.Version < 1 || len(got.Nodes) == 0 {
		t.Fatalf("响应不完整: %+v", got)
	}
}

func TestFlowPutCreatesNewVersion(t *testing.T) {
	env := newLedgerEnv(t)
	payload := `{"nodes":[{"name":"待办","next":"进行中"},{"name":"进行中"}]}`
	code, body := ledgerPut(t, env.testAgentdEnv, "/api/flows/feature", payload)
	if code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", code, body)
	}
	var got struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("解码: %v（原文 %s）", err, body)
	}
	if got.Version < 2 {
		t.Fatalf("应发出新版本，得到 v%d", got.Version)
	}
}

func TestFlowPutRejectsBadNodes(t *testing.T) {
	env := newLedgerEnv(t)
	// Next 指向不存在的节点：校验应在 Store 层拦下，HTTP 翻成 400 而不是 500。
	code, body := ledgerPut(t, env.testAgentdEnv, "/api/flows/feature",
		`{"nodes":[{"name":"A","next":"查无此节点"}]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d（想要 400），body = %s", code, body)
	}
}

func TestDisciplinesListIncludesBuiltins(t *testing.T) {
	env := newLedgerEnv(t)
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/disciplines")
	if code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", code, body)
	}
	var got struct {
		Names []string `json:"names"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("解码: %v（原文 %s）", err, body)
	}
	for _, want := range []string{"implement", "review", "spec-draft", "plan-writing", "finishing"} {
		found := false
		for _, name := range got.Names {
			if name == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("纪律块清单缺 %q: %v", want, got.Names)
		}
	}
}

func TestCardStepAcceptsNodeName(t *testing.T) {
	env := newLedgerEnv(t)
	card := seedCard(t, env, "节点名透传")
	// 占住环节槽位，挡住真派发——本用例只验「节点名不再被白名单拦掉」。
	release := holdCardStep(t, env.srv, card.ID)
	defer release()
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/step", `{"step":"待审阅"}`)
	if code == http.StatusBadRequest && strings.Contains(body, "review|merge") {
		t.Fatalf("节点名仍被写死的白名单拦掉: %s", body)
	}
	if code != http.StatusConflict {
		t.Logf("注意：槽位已占用时预期 409，实际 %d（%s）", code, body)
	}
}
```

- [ ] **Step 2: 跑测试确认它红**

Run: `go test ./internal/agentd/ -run 'TestFlow|TestDisciplines|TestCardStepAccepts' -v`
Expected: 404 / 编译失败。

- [ ] **Step 3: 注册路由并实现 handler（含注释）**

`internal/agentd/ledgerapi.go` 的路由块里，在 `GET /api/flows` 之后追加：

```go
	api.HandleFunc("GET /api/flows/{name}", s.withLedger(s.handleFlowGet))
	api.HandleFunc("PUT /api/flows/{name}", s.withLedger(s.handleFlowPut))
	api.HandleFunc("GET /api/disciplines", s.withLedger(s.handleDisciplineNames))
```

在文件末尾追加三个 handler：

```go
// handleFlowGet 取单条工作流的最新版本（含节点定义）。
//
// 老 def（只有 states）读出时会被补出等价的纯人工节点序列，所以前端永远
// 只需要看 nodes 一个字段。
func (s *Server) handleFlowGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	workflow, err := s.ledger.GetWorkflow(name, 0)
	if err != nil {
		s.log.Warn("取工作流失败", "name", name, "cause", err)
		writeErr(w, http.StatusNotFound, err)
		return
	}
	s.log.Info("读出工作流", "name", name, "version", workflow.Version, "nodes", len(workflow.Def.Nodes))
	writeJSON(w, http.StatusOK, map[string]any{
		"name":    workflow.Name,
		"version": workflow.Version,
		"nodes":   workflow.Def.Nodes,
		"states":  workflow.Def.States,
	})
}

// handleFlowPut 发布该工作流的**下一个版本**。
//
// 注意：这不是「改」——工作流不可变版本化，每次保存都是插一个新版本，
// 已经钉在老版本上的卡完全不受影响。想让老卡用新流程要显式迁移
// （MigrateCardWorkflow），那是另一个动作。
func (s *Server) handleFlowPut(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Nodes []ledger.NodeDef `json:"nodes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("解析请求体: %w", err))
		return
	}
	if len(body.Nodes) == 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("nodes 不能为空"))
		return
	}
	version, err := s.ledger.PutWorkflow(name, ledger.WorkflowDef{Nodes: body.Nodes})
	if err != nil {
		// 节点校验不过是用户输入问题（400），不是服务器故障（500）。
		if errors.Is(err, ledger.ErrBadState) {
			s.log.Warn("工作流节点校验未过", "name", name, "cause", err)
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		s.log.Error("写工作流失败", "name", name, "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.log.Info("已发布工作流新版本", "name", name, "version", version, "nodes", len(body.Nodes))
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "version": version})
}

// handleDisciplineNames 列出可选的纪律块名：内置角色 + DataDir 下的自定义文件。
//
// 给节点配置的下拉用。返回去重升序的名字列表，不带正文——正文有专门的
// 纪律块读写接口，列表接口没必要驮着几十 KB。
func (s *Server) handleDisciplineNames(w http.ResponseWriter, r *http.Request) {
	seen := map[string]bool{}
	names := []string{}
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		names = append(names, name)
	}
	for _, name := range []string{
		discipline.NameImplement, discipline.NameReview,
		discipline.NameSpecDraft, discipline.NamePlanWriting, discipline.NameFinishing,
	} {
		add(name)
	}
	files, err := discipline.List(discipline.Dir(s.cfg.DataDir))
	if err != nil {
		// 目录读不了不该让整个下拉空掉——内置的那几个仍然可用，如实告警即可。
		s.log.Warn("列自定义纪律块失败，只返回内置", "cause", err)
	}
	for _, file := range files {
		add(strings.TrimSuffix(file.Name, ".md"))
	}
	sort.Strings(names)
	s.log.Info("列出纪律块名", "count", len(names), "custom", len(files))
	writeJSON(w, http.StatusOK, map[string]any{"names": names})
}
```

import 增补 `"errors"`、`"sort"`、`"strings"`、`"github.com/Xsxdot/handoff/internal/discipline"`、`"github.com/Xsxdot/handoff/internal/ledger"`（按实际缺什么补）。`s.cfg.DataDir` 的确切取法用 `grep -n "discipline.Dir(" internal/agentd/*.go` 对齐既有写法，不要自己发明。`writeErr` 若不存在则照文件里既有的错误响应写法来。

- [ ] **Step 4: 放开 step 的白名单**

`handleCardStep` 里，把只认 `review`/`merge` 的判断改成：只拒 `implement`（保留既有理由注释：实现派发通常要挂 plan 文件，浏览器里没有那个文件，它留在 CLI），其余节点名一律透传给 `startCardStep`——合法性由 `StepRunner.nodeFor` 按卡钉的工作流判，错了会带上「节点 %q 不在卡 %s 的工作流 … 里」的真因。

- [ ] **Step 5: 跑测试确认绿**

Run: `go test ./internal/agentd/ -v -count=1`
Expected: 全部 PASS。

- [ ] **Step 6: 全量回归 + 格式**

Run: `go build ./... && go vet ./... && go test ./... -count=1 && gofmt -l .`

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/ledgerapi.go internal/agentd/ledgerapi_test.go
git commit -m "feat(agentd): 工作流读写 API、纪律块名列表，step 收任意节点名"
```

---

### Task 8: CLI 与文档收口

**Files:**
- Modify: `cmd/card.go`（或 `card dispatch --step` 的 flag 定义所在文件，用 `grep -rn '"step"' cmd/` 定位）
- Modify: `README.md`
- Test: `cmd/` 下既有的相关测试文件

**Interfaces:**
- Consumes: Task 5 的 `StepRunner.Run(ctx, cardID, nodeName)`
- Produces: `handoff card dispatch --step <节点名>` 收任意节点名；README 补节点化工作流一节

- [ ] **Step 1: 写失败的测试**

用 `grep -rn 'step' cmd/*_test.go` 找到既有的 step 相关用例。若存在断言「只认 review|merge」的用例，改成断言「未知节点名的报错里带上节点名与工作流名」。若一个都没有，新建 `cmd/card_node_test.go`：

```go
package cmd

import "testing"

func TestStepFlagHelpMentionsNodeName(t *testing.T) {
	cmd := newCardCmd() // 用 cmd 包里实际的构造函数名，grep 'card dispatch' 确认
	flag := cmd.PersistentFlags().Lookup("step")
	if flag == nil {
		if sub, _, err := cmd.Find([]string{"dispatch"}); err == nil {
			flag = sub.Flags().Lookup("step")
		}
	}
	if flag == nil {
		t.Fatalf("找不到 --step flag")
	}
	if flag.Usage == "" {
		t.Fatalf("--step 的说明为空")
	}
	for _, stale := range []string{"review|merge", "环节只认"} {
		if contains(flag.Usage, stale) {
			t.Fatalf("--step 的说明还写着写死的白名单 %q: %s", stale, flag.Usage)
		}
	}
}
```

（`contains` 用 `strings.Contains`；`newCardCmd` 换成实际名字。）

- [ ] **Step 2: 跑测试确认它红**

Run: `go test ./cmd/ -run TestStepFlag -v`

- [ ] **Step 3: 更新 flag 说明与相关注释**

把 `--step` 的 usage 从「环节名：review|merge」改成：

```
节点名（= 看板列名），从卡钉住的工作流里查；不给则不跑节点
```

同步更新 `cmd` 里所有提到「环节只认 review|merge」的注释与错误文案。

- [ ] **Step 4: 补 README**

在 README 里工作项账本相关章节追加一节：

```markdown
### 节点化工作流

工作流是一串**节点**，每个节点既是看板的一列，也是「卡走到这列时怎么办」的
配置。系统**不预设任何节点类型**——「审阅」「合并」这些语义由下面几个能力
开关组合出来：

| 开关 | 含义 |
|------|------|
| `dispatch` | 进入这一列时派发一个任务 |
| `verdict` | 等回合终态、解析 `handoff-verdict` 块并按结果路由（蕴含 `dispatch`） |
| `carry_card_context` | 把卡上下文（卡号/标题/有效基线/验收判据/附件）拼进 prompt |
| `max_rounds` | 裁决的轮次封顶，到顶转「需要你」 |
| `next` / `on_fail` | 通过/未过分别移到哪一列，**按节点名指向** |
| `human_bases` | 卡的有效基线落在其中时不自动执行，直接转「需要你」 |
| `gate` | 进入这一列的门槛（要求某类附件 / 要求验收判据非空） |

节点用 `template` 引一份派发模板（执行者、目标机、模型、纪律块、prompt 正文），
再用 `override` 覆盖其中单个字段——想让审阅这一列换个执行者，只改这一个节点。

**节点配的是规矩，不是具体要干什么。** 「合并到哪条分支」这种每张卡都不同的
值来自卡本身的**有效基线分支**（子卡自动继承父卡的），由 `carry_card_context`
带进 prompt；节点上的纪律块只规定「合并目标以卡的基线为准，不要越过它碰别的
分支」。

工作流不可变版本化：每次保存都是发布一个新版本，卡钉着建卡时的版本，
老卡完全不受影响。
```

- [ ] **Step 5: 跑测试确认绿 + 全量回归**

Run: `go build ./... && go vet ./... && go test ./... -count=1 && gofmt -l .`
Expected: 全绿、无格式输出。

- [ ] **Step 6: Commit**

```bash
git add cmd README.md
git commit -m "docs(cli,readme): --step 收任意节点名，README 补节点化工作流一节"
```

---

## 收尾自检（全部 task 完成后）

- [ ] `go build ./... && go vet ./... && go test ./... -count=1` 全绿
- [ ] `gofmt -l .` 无输出
- [ ] `grep -rn "MergeStep\|NewLocalMerge\|NewLocalObjective\|gitscript" --include="*.go" .` 无残留
- [ ] `grep -rn "review|merge" --include="*.go" .` 无残留的写死白名单
- [ ] `grep -rn "fmt.Printf" --include="*.go" internal/ cmd/` 没有新增（既有的不管）
- [ ] 每个新建文件都有职责+边界的头注释；每个新导出函数都有参数/返回/注意事项的 doc 注释
- [ ] 每个错误分支都带上下文（`%w` 或结构化日志的 `cause`）；成功路径有结果日志
- [ ] 最后一条消息里如实报告：哪些 task 完成、跑了哪些命令、有没有未验证的部分
