# 实际模型名与 context 用量回读 实现计划（B80 / W4d）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让任务详情页显示 executor **实际**在用的模型名与当前 context 占用，有分母的执行者一并显示百分比。

**Architecture:** 四家 adapter 各自从**已经在读的那一帧**里多解几个字段，经 `executor.AdapterEvent` 的新类型 `usage` 报给 manager；manager 去重后只写任务字段（不追加事件、不广播），落 SQLite 三个新列；web 靠既有的 4 秒详情轮询自然拿到。

**Tech Stack:** Go 1.26（`internal/executor/*`、`internal/proto`、`internal/store`、`internal/agentd`）+ React/TypeScript + vitest（`web/src/app/task`）。

**Spec:** [2026-08-13-w4-executor-model-usage-design.md](../specs/2026-08-13-w4-executor-model-usage-design.md)
**探针取证:** [2026-08-12-w4-tui-model-token-probe.md](../notes/2026-08-12-w4-tui-model-token-probe.md)（§1.1 codex、§3.1 opencode、§4.1/§4.2 grok 均为 08-13 实抓）

---

## Global Constraints

以下约束是**每个 task 的隐含需求**，任何一条被违反即为实现失败。

1. **分子一律取「最后一次模型调用的输入侧」，绝不取回合或会话累加。** grok 的
   `modelCalls=4` 实验里累加值 138637 是真实占用 34752 的 4 倍，长回合会超过 100%。
2. **缓存怎么算每家不同，不许按字段名类推**：
   - codex：`last.inputTokens` **已含**缓存，`cachedInputTokens` 是它的子集，**不再加**。
   - grok：`input_tokens + cache_read_input_tokens + cache_creation_input_tokens`，**要加**。
   - claudecode：`input_tokens + cache_read_input_tokens + cache_creation_input_tokens`，**要加**。
   - opencode：`tokens.input + tokens.cache.read + tokens.cache.write`，**要加**。
3. **nil 纪律（B69/B70）**：指针 + `omitempty`，`nil` = 取不到；**绝不用 0 冒充**。
   分母取不到就不显示百分比，**绝不维护「模型→窗口」对照表**。
4. **日志用 `a.log` / `m.log`（slog），禁止 `fmt.Printf` / `println`。**
5. **注释用中文写「为什么」**：新文件写文件头（职责 + 边界），导出函数写参数/返回/注意事项。
6. **不动 `grok/adapter.go` 的 `feedRaw`**，不动任何 executor 的正文/渲染链路。
7. **不新增 `proto.EventType`**，不因用量向事件表写任何一行。
8. 每个 task 结束时 `go test ./...`（涉及 web 的 task 另跑 `npm test`）必须全绿。

---

## File Structure

| 文件 | 责任 | 动作 |
|---|---|---|
| `internal/proto/proto.go` | `Usage` 结构与 `Task` 两个新字段 | 修改 |
| `internal/store/store.go` | 三个新列、迁移、`taskColumns`、`scanTaskRow`、`SetTaskUsage` | 修改 |
| `internal/executor/executor.go` | `AdapterEvent` 两个新字段 | 修改 |
| `internal/agentd/manager.go` | `adapterEventUsage` 常量、`handleEvent` 分支、`handleUsage` | 修改 |
| `internal/executor/codex/usage.go` | codex 的用量解析（纯函数） | **新建** |
| `internal/executor/grok/usage.go` | grok 的用量与模型解析（纯函数） | **新建** |
| `internal/executor/claudecode/usage.go` | claudecode 的用量解析（纯函数） | **新建** |
| `internal/executor/opencode/usage.go` | opencode 的用量解析（纯函数） | **新建** |
| 四家的 `adapter.go` | 接线 | 修改 |
| `codex/` `grok/` 的 `export_test.go` | 测试缝 | 修改（已存在，追加） |
| `claudecode/` `opencode/` 的 `export_test.go` | 测试缝 | **新建** |
| `internal/proto/contract_fixture_test.go` + `web/src/api/testdata/*.json` | 契约 fixture（逐字节钉死，加字段必须刷新） | 修改 |
| `web/src/api/contract.test.ts` | 契约的 web 侧镜像断言 | 修改 |
| `web/src/app/lib/format.test.ts` | 格式化函数单测 | **新建** |
| `web/src/api/types.ts` | `Task` 的两个新字段与 `Usage` 类型 | 修改 |
| `web/src/app/lib/format.ts` | `formatTokens` / `formatUsage` | 修改 |
| `web/src/app/task/TaskHeader.tsx` | 「执行器」行 | 修改 |

**解析一律做成纯函数放独立文件**：它们是这次最容易出错的地方（四套字段名、两套缓存规则），纯函数能用真实报文直接测，不需要起 adapter、不需要 runState。

---

### Task 1: 数据地基（proto.Usage + Task 字段 + store 三列）

**Files:**
- Modify: `internal/proto/proto.go`（`Task` 结构末尾追加两个字段 + 新增 `Usage` 类型）
- Modify: `internal/store/store.go:80`（DDL）、`:183`（迁移 map）、`:246`（`taskColumns`）、`:258`（`scanTaskRow`）、`:436` 附近（新增 `SetTaskUsage`）
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces: `proto.Usage{ContextTokens int, ContextWindow *int}`；`proto.Task.ActualModel string`；`proto.Task.Usage *proto.Usage`；`(*store.Store).SetTaskUsage(id, model string, ctxTokens int, ctxWindow *int) error`

- [ ] **Step 1: 写失败测试**

追加到 `internal/store/store_test.go`：

```go
// TestSetTaskUsageWritesAndRestoresNil 覆盖三件事：写入回读一致、空值语义是
// 「不更新」而非「清空」、0/空列还原成 nil（绝不冒充 0）。
func TestSetTaskUsageWritesAndRestoresNil(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := s.CreateTask(&proto.Task{
		ID: "t1", RepoPath: "/r", State: proto.TaskStateRunning,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// 新任务：三列都是零值 → Usage 必须是 nil，ActualModel 必须是空串
	got, err := s.GetTask("t1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Usage != nil {
		t.Fatalf("零值任务的 Usage 应为 nil，得到 %+v", got.Usage)
	}
	if got.ActualModel != "" {
		t.Fatalf("零值任务的 ActualModel 应为空串，得到 %q", got.ActualModel)
	}

	// 带分母写入
	win := 258400
	if err := s.SetTaskUsage("t1", "gpt-5.6-sol", 24668, &win); err != nil {
		t.Fatalf("SetTaskUsage: %v", err)
	}
	got, _ = s.GetTask("t1")
	if got.ActualModel != "gpt-5.6-sol" {
		t.Fatalf("ActualModel = %q，期望 gpt-5.6-sol", got.ActualModel)
	}
	if got.Usage == nil || got.Usage.ContextTokens != 24668 {
		t.Fatalf("ContextTokens 回读不一致: %+v", got.Usage)
	}
	if got.Usage.ContextWindow == nil || *got.Usage.ContextWindow != 258400 {
		t.Fatalf("ContextWindow 回读不一致: %+v", got.Usage)
	}

	// 空值 = 不更新（不是清空）：只更新分子，模型名与分母必须原样保留
	if err := s.SetTaskUsage("t1", "", 30000, nil); err != nil {
		t.Fatalf("SetTaskUsage 二次: %v", err)
	}
	got, _ = s.GetTask("t1")
	if got.ActualModel != "gpt-5.6-sol" {
		t.Fatalf("空模型名不该清空既有值，得到 %q", got.ActualModel)
	}
	if got.Usage.ContextWindow == nil || *got.Usage.ContextWindow != 258400 {
		t.Fatalf("nil 分母不该清空既有值: %+v", got.Usage)
	}
	if got.Usage.ContextTokens != 30000 {
		t.Fatalf("分子应更新为 30000，得到 %d", got.Usage.ContextTokens)
	}
}

// TestUsageWithoutWindowStaysNil 覆盖不报分母的执行者（claudecode/opencode）：
// 有分子无分母时 ContextWindow 必须是 nil，界面据此不显示百分比。
func TestUsageWithoutWindowStaysNil(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := s.CreateTask(&proto.Task{
		ID: "t2", RepoPath: "/r", State: proto.TaskStateRunning,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := s.SetTaskUsage("t2", "k3-256k", 121801, nil); err != nil {
		t.Fatalf("SetTaskUsage: %v", err)
	}
	got, _ := s.GetTask("t2")
	if got.Usage == nil || got.Usage.ContextTokens != 121801 {
		t.Fatalf("分子回读不一致: %+v", got.Usage)
	}
	if got.Usage.ContextWindow != nil {
		t.Fatalf("无分母时 ContextWindow 必须是 nil，得到 %d", *got.Usage.ContextWindow)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/store/ -run 'TestSetTaskUsage|TestUsageWithoutWindow' -v
```
Expected: FAIL，`s.SetTaskUsage undefined` / `got.Usage undefined`。

- [ ] **Step 3: 加 `proto.Usage` 与 `Task` 两个字段**

在 `internal/proto/proto.go` 的 `Task` 结构定义**之前**加类型，在 `Task` 的 `Machine` 字段**之前**加两个字段：

```go
// Usage 是任务当前的 context 占用快照。
//
// 「当前占用」= 最后一次模型调用的输入侧（含缓存命中），**不是**回合或会话的
// 累加。两者差别巨大：实测一个 4 次模型调用的 grok 回合，累加值是真实占用的
// 4 倍，且工具调用越多越离谱，长回合会超过 100%（探针笔记 §4.2）。
//
// 边界：本结构只描述「占用」，不描述「消耗」。累计 token 与花费是另一个口径，
// 将来以新增字段的形式加进来，形状不变、不需要重新设计。
type Usage struct {
	// ContextTokens 是当前 context 占用的 token 数。永远 > 0——取不到时整个
	// Usage 为 nil，不用 0 冒充「没用 token」（B69/B70 纪律）。
	ContextTokens int `json:"context_tokens"`
	// ContextWindow 是该模型的上下文窗口上限（百分比的分母）。
	// nil = 该 executor 不在协议里报窗口（claudecode / opencode），此时界面
	// 只显绝对值。**绝不由 handoff 猜**：猜错是静默错误，百分比照常显示只是错的。
	ContextWindow *int `json:"context_window,omitempty"`
}
```

```go
	// ActualModel 是 executor 报回的**实际**模型名；空=执行者还没报（回合未
	// 开始）或该任务跑在不报模型名的旧版执行者上。
	//
	// 它与 Model 是两件事：Model 是 dispatch --model 发下去的**入参**（常为空，
	// 意思是「用执行者自己的默认」），ActualModel 是执行者实际在用的那个。
	// 二者不一致时以 ActualModel 为准，界面不并列显示。
	ActualModel string `json:"actual_model,omitempty"`
	// Usage 是当前 context 占用；nil=还没有任何一次模型调用完成。
	Usage *Usage `json:"usage,omitempty"`
```

- [ ] **Step 4: 加三个 SQLite 列与迁移**

`internal/store/store.go` 的建表 DDL（`repo_dirty_files` 之后）追加：

```go
  -- B80 三列：actual_model=executor 报回的实际模型名（与入参 model 不是一回事）；
  -- usage_context_tokens=当前 context 占用；usage_context_window=该模型的窗口上限。
  -- 0/空一律表示「取不到」——真实的模型调用输入与真实的窗口都必然 > 0，
  -- 所以 0 可以安全地当哨兵，读取时还原成 nil（绝不冒充 0）。
  actual_model TEXT NOT NULL DEFAULT '',
  usage_context_tokens INTEGER NOT NULL DEFAULT 0,
  usage_context_window INTEGER NOT NULL DEFAULT 0)`,
```

（注意：原 DDL 以 `repo_dirty_files TEXT NOT NULL DEFAULT '')` 结尾，加列时把那个右括号挪到新的最后一列。）

迁移 map（`store.go:183` 那个 `for col, typ := range map[string]string{...}`）追加三项：

```go
		"actual_model":         "TEXT NOT NULL DEFAULT ''",
		"usage_context_tokens": "INTEGER NOT NULL DEFAULT 0",
		"usage_context_window": "INTEGER NOT NULL DEFAULT 0",
```

`taskColumns` 追加三列（顺序必须与 `scanTaskRow` 一致）：

```go
const taskColumns = `id, target, repo_path, branch, plan_path, plan_summary, executor_session, state, created_at, updated_at,
  name, executor, model, work_dir, worktree_managed, base_commit, base_ahead, repo_dirty_count, repo_dirty_files, done_note,
  actual_model, usage_context_tokens, usage_context_window`
```

- [ ] **Step 5: 改 `scanTaskRow` 还原 nil**

```go
func scanTaskRow(sc rowScanner) (proto.Task, error) {
	var (
		task            proto.Task
		createdAt       string
		updatedAt       string
		worktreeManaged int
		ctxTokens       int
		ctxWindow       int
	)
	if err := sc.Scan(&task.ID, &task.Target, &task.RepoPath, &task.Branch, &task.PlanPath,
		&task.PlanSummary, &task.ExecutorSession, &task.State, &createdAt, &updatedAt,
		&task.Name, &task.Executor, &task.Model, &task.WorkDir, &worktreeManaged,
		&task.BaseCommit, &task.BaseAhead, &task.RepoDirtyCount, &task.RepoDirtyFiles,
		&task.DoneNote, &task.ActualModel, &ctxTokens, &ctxWindow); err != nil {
		return proto.Task{}, err
	}
	task.CreatedAt = parseTime(createdAt)
	task.UpdatedAt = parseTime(updatedAt)
	task.WorktreeManaged = worktreeManaged != 0
	// 0 还原成 nil：任何一次真实的模型调用输入都 > 0，所以 0 只可能是
	// 「还没有任何一次调用完成」。用 0 表示「占用为零」是编造。
	if ctxTokens > 0 {
		task.Usage = &proto.Usage{ContextTokens: ctxTokens}
		if ctxWindow > 0 {
			w := ctxWindow
			task.Usage.ContextWindow = &w
		}
	}
	return task, nil
}
```

- [ ] **Step 6: 加 `SetTaskUsage`**

放在 `SetTaskField` 之后：

```go
// SetTaskUsage 一次性更新任务的实际模型名与 context 占用。
//
// 参数：
//   - id: 任务 ID
//   - model: 实际模型名；**空串表示本次不更新该列**（保留既有值）
//   - ctxTokens: 当前 context 占用；**0 表示不更新**
//   - ctxWindow: 上下文窗口上限；**nil 表示不更新**
//
// 为什么空值语义是「不更新」而不是「清空」：用量与模型名往往来自**不同的帧**
// （grok 的窗口在会话建立时到、占用在每次模型调用后到），若空值等于清空，
// 后到的那一帧会把先到的那一半抹掉。
//
// 注意：
//   - 三个参数全为空时是空操作，不打库
//   - 任务不存在时不报错（与 SetTaskField 一致，不影响其他行即返回 nil）
func (s *Store) SetTaskUsage(id, model string, ctxTokens int, ctxWindow *int) error {
	sets := make([]string, 0, 3)
	args := make([]any, 0, 5)
	if model != "" {
		sets = append(sets, "actual_model = ?")
		args = append(args, model)
	}
	if ctxTokens > 0 {
		sets = append(sets, "usage_context_tokens = ?")
		args = append(args, ctxTokens)
	}
	if ctxWindow != nil {
		sets = append(sets, "usage_context_window = ?")
		args = append(args, *ctxWindow)
	}
	if len(sets) == 0 {
		return nil // 无事可做，不打库
	}
	args = append(args, fmtTime(time.Now()), id)
	if _, err := s.db.ExecContext(context.Background(),
		"UPDATE tasks SET "+strings.Join(sets, ", ")+", updated_at = ? WHERE id = ?",
		args...); err != nil {
		return fmt.Errorf("更新任务 %s 用量: %w", id, err)
	}
	return nil
}
```

- [ ] **Step 7: 更新契约 fixture**

`internal/proto/contract_fixture_test.go` 把每个对外类型的序列化产物**逐字节**钉在
`web/src/api/testdata/*.json` 里。给 `Task` 加字段会让 `TestContractFixtures` 当场变红
——这是设计如此（契约漂移必须有人看见），处理办法是显式刷新并 review 差异。

先给 `taskSample`（`contract_fixture_test.go:129`）补上新字段。**必须给非零值**：
两个新字段都带 `omitempty`，留空的话 fixture 里根本不出现，等于没钉住。

```go
		Machine:         "",
		ProjectID:       "a1b2c3d4e5f60718",
		// B80：给非零值才能把线格式钉进 fixture（两个字段都带 omitempty）。
		// context_window 给值是为了钉住「有分母」的形状；无分母时该键缺席，
		// 由 web 侧的 Usage 可选字段与 TaskHeader 测试覆盖。
		ActualModel: "gpt-5.6-sol",
		Usage:       &Usage{ContextTokens: 24668, ContextWindow: &sampleCtxWindow},
```

在 `taskSample` 函数上方加包级变量（`&258400` 不能直接写）：

```go
// sampleCtxWindow 是 fixture 用的窗口上限；单独取变量只因为 Go 不能对字面量取址。
var sampleCtxWindow = 258400
```

然后刷新并 review：

```bash
go test ./internal/proto/ -run TestContractFixtures -update
git diff web/src/api/testdata/
```
Expected: 只有 `Task.json` 与 `TasksResp.json`（它内嵌 `TaskView{Task}`）发生变化，
且变化只是各多出 `actual_model` 与 `usage` 两个键。**出现其他文件或其他键的改动就是
改错了**，停下来查，不要直接接受。

- [ ] **Step 8: 跑测试确认通过**

```bash
go test ./internal/store/ ./internal/proto/ -run 'TestSetTaskUsage|TestUsageWithoutWindow|TestTaskLifecycle|TestContractFixtures' -v
```
Expected: PASS（`TestTaskLifecycle` 一并跑，确认加列没有破坏既有扫描）。

- [ ] **Step 9: 全量回归**

```bash
go test ./...
```
Expected: PASS。列数不匹配是加列时最典型的运行期错误，全量跑一次才能发现。

- [ ] **Step 10: 加注释**（本 task 的注释已在 Step 3-6 的代码里给全，此处只做核对）

核对清单：`proto.Usage` 的类型注释写清「占用 ≠ 消耗」；两个 `Task` 字段各自说明「与 `Model` 的区别」与 nil 含义；DDL 里说明「0 为什么可以安全当哨兵」；`SetTaskUsage` 的 doc 注释说明「空值=不更新」的理由。

- [ ] **Step 11: 提交**

```bash
git add internal/proto/ internal/store/ web/src/api/testdata/
git commit -m "feat(proto,store): 加 Usage 结构与三列，0/空还原成 nil"
```

---

### Task 2: 通路（AdapterEvent 两字段 + manager handleUsage）

**Files:**
- Modify: `internal/executor/executor.go`（`AdapterEvent` 追加两个字段）
- Modify: `internal/agentd/manager.go:71-74`（常量）、`:1309`（`handleEvent` 分支）、`handleProgress` 附近（新增 `handleUsage`）
- Test: `internal/agentd/manager_test.go`

**Interfaces:**
- Consumes: Task 1 的 `proto.Usage`、`(*store.Store).SetTaskUsage`
- Produces: `executor.AdapterEvent.ActualModel string`、`executor.AdapterEvent.Usage *proto.Usage`；manager 常量 `adapterEventUsage = "usage"`；四家 adapter 从此可以发 `Type: "usage"` 的事件

- [ ] **Step 1: 写失败测试**

追加到 `internal/agentd/manager_test.go`：

```go
// TestHandleUsageWritesTaskFieldsWithoutEvents 覆盖 usage 事件的两条契约：
// ①落到任务字段上；②**不**产生任何事件行——用量刷新频率高（claudecode 一个回合
// 几百条 assistant 消息），进事件日志会淹掉审核者真正要看的 permission/question。
func TestHandleUsageWritesTaskFieldsWithoutEvents(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	now := time.Now().UTC()
	mustCreateTask(t, st, &proto.Task{
		ID: "u1", RepoPath: t.TempDir(), State: proto.TaskStateRunning,
		CreatedAt: now, UpdatedAt: now,
	})

	before, err := st.EventsFrom("u1", 0, 100)
	if err != nil {
		t.Fatalf("EventsFrom: %v", err)
	}

	win := 258400
	m.handleEvent(context.Background(), "u1", executor.AdapterEvent{
		Type: "usage", ActualModel: "gpt-5.6-sol",
		Usage: &proto.Usage{ContextTokens: 24668, ContextWindow: &win},
	})

	task, err := st.GetTask("u1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.ActualModel != "gpt-5.6-sol" {
		t.Fatalf("ActualModel = %q，期望 gpt-5.6-sol", task.ActualModel)
	}
	if task.Usage == nil || task.Usage.ContextTokens != 24668 {
		t.Fatalf("Usage 未落库: %+v", task.Usage)
	}
	after, _ := st.ListEvents("u1", 0, 100)
	if len(after) != len(before) {
		t.Fatalf("usage 事件不该产生事件行，前 %d 条后 %d 条", len(before), len(after))
	}
}

// TestHandleUsageDedupesRepeatedValues 覆盖去重：同值连续多次只打库一次。
// 这是写库风暴的唯一防线——claudecode 每条 assistant 消息都带 usage。
func TestHandleUsageDedupesRepeatedValues(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	now := time.Now().UTC()
	mustCreateTask(t, st, &proto.Task{
		ID: "u2", RepoPath: t.TempDir(), State: proto.TaskStateRunning,
		CreatedAt: now, UpdatedAt: now,
	})

	ev := executor.AdapterEvent{Type: "usage", ActualModel: "k3-256k",
		Usage: &proto.Usage{ContextTokens: 121801}}
	m.handleEvent(context.Background(), "u2", ev)
	first, _ := st.GetTask("u2")
	firstUpdated := first.UpdatedAt

	// 同值再来两次：updated_at 不该再动（说明没打库）
	m.handleEvent(context.Background(), "u2", ev)
	m.handleEvent(context.Background(), "u2", ev)
	again, _ := st.GetTask("u2")
	if !again.UpdatedAt.Equal(firstUpdated) {
		t.Fatalf("同值重复不该再打库，updated_at 从 %v 变成 %v", firstUpdated, again.UpdatedAt)
	}

	// 值变了就必须落库
	ev2 := executor.AdapterEvent{Type: "usage", ActualModel: "k3-256k",
		Usage: &proto.Usage{ContextTokens: 130000}}
	m.handleEvent(context.Background(), "u2", ev2)
	changed, _ := st.GetTask("u2")
	if changed.Usage.ContextTokens != 130000 {
		t.Fatalf("值变化后应落库，得到 %d", changed.Usage.ContextTokens)
	}
}
```

> 注：`st.ListEvents(taskID, fromSeq, limit)` 的签名以 `internal/store/store.go` 现有实现为准；若参数形态不同，按现有签名调整调用，断言逻辑不变。

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/agentd/ -run TestHandleUsage -v
```
Expected: FAIL，`unknown field ActualModel in struct literal`。

- [ ] **Step 3: 加 `AdapterEvent` 两个字段**

`internal/executor/executor.go` 的 `AdapterEvent` 里，`Result` 字段之后：

```go
	// ActualModel 是 executor 报回的**实际**模型名；空=本帧没带模型信息。
	// 与 Task.Model（dispatch 的入参）是两件事，manager 落 task.ActualModel。
	ActualModel string
	// Usage 是当前 context 占用快照；nil=本帧没带用量。
	// 语义见 proto.Usage：只描述占用不描述消耗，且绝不用 0 冒充「没有」。
	Usage *proto.Usage
```

同时把 `Type` 字段的注释补上新类型：

```go
	Type string // "permission" | "question" | "progress" | "result" | "usage"
```

- [ ] **Step 4: 加 manager 常量与分支**

`internal/agentd/manager.go:71-74` 的常量块追加：

```go
	adapterEventUsage = "usage"
```

`handleEvent` 的 switch 里，`adapterEventResult` 分支之后、`default` 之前追加：

```go
	case adapterEventUsage:
		// 不打 Info：用量事件频率高（claudecode 一个回合几百条），
		// 每条都打入口日志就是刷屏。首次落库的日志在 handleUsage 里打。
		m.handleUsage(taskID, ev)
```

- [ ] **Step 5: 实现 `handleUsage`**

放在 `handleProgress` 之后：

```go
// handleUsage 落 executor 报回的实际模型名与 context 占用。
//
// 与 handleProgress 的区别：**只写任务字段，不追加事件行、不广播**。
// 用量刷新频率高（claudecode 一个回合几百条 assistant 消息），进事件日志会淹没
// 审核者真正要看的 permission/question/completed；界面靠详情轮询自然拿到，
// 不需要事件推送。
//
// 去重是写库风暴的唯一防线：与内存里上一次的三元组全等就直接返回。
// agentd 重启后内存态为空，首帧必写一次，这是可接受的代价。
//
// 落库失败仅 Warn：用量属可修复的辅助字段，与 executor_session 同级，
// 不影响主流程。
func (m *Manager) handleUsage(taskID string, ev executor.AdapterEvent) {
	tokens, window := 0, (*int)(nil)
	if ev.Usage != nil {
		tokens = ev.Usage.ContextTokens
		window = ev.Usage.ContextWindow
	}
	if m.takeUsageUnchanged(taskID, ev.ActualModel, tokens, window) {
		return
	}
	if err := m.st.SetTaskUsage(taskID, ev.ActualModel, tokens, window); err != nil {
		m.log.Warn("落库任务用量失败", "task", taskID, "model", ev.ActualModel,
			"tokens", tokens, "cause", err)
		return
	}
	m.log.Info("任务用量已更新", "task", taskID, "model", ev.ActualModel,
		"tokens", tokens, "window", window)
}

// takeUsageUnchanged 判定这次上报与上一次是否完全相同（相同返回 true，
// 调用方据此跳过打库），并在不同的情况下就地记下新值。
//
// 为什么把窗口也纳入比较：不报窗口的执行者每次都传 nil，只比模型名与分子会让
// 「窗口首次到达」这一帧被误判成重复而丢掉。
func (m *Manager) takeUsageUnchanged(taskID, model string, tokens int, window *int) bool {
	key := fmt.Sprintf("%s|%d|%v", model, tokens, derefOrNil(window))
	m.usageMu.Lock()
	defer m.usageMu.Unlock()
	if m.lastUsage == nil {
		m.lastUsage = map[string]string{}
	}
	if m.lastUsage[taskID] == key {
		return true
	}
	m.lastUsage[taskID] = key
	return false
}

// derefOrNil 把 *int 摊平成可比较的展示值：nil 记作 -1（真实窗口必然 > 0，
// 不会与之相撞）。
func derefOrNil(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}
```

在 `Manager` 结构体里加两个字段（放在既有的锁与 map 附近，遵循该结构现有的排布）：

```go
	usageMu   sync.Mutex        // 保护 lastUsage
	lastUsage map[string]string // taskID → 上一次上报的用量指纹，去重用
```

- [ ] **Step 6: 跑测试确认通过**

```bash
go test ./internal/agentd/ -run TestHandleUsage -v
```
Expected: PASS。

- [ ] **Step 7: 加日志**（已在 Step 5 写入，此处核对）

- 落库失败 → `Warn`，带 `task` / `model` / `tokens` / `cause`。
- 落库成功 → `Info`，带 `task` / `model` / `tokens` / `window`。
- 去重命中 → **不打日志**（高频）。
- `handleEvent` 的 usage 分支 → **不打入口日志**（与其余四类不同，理由写在代码注释里）。

- [ ] **Step 8: 加注释**（已在 Step 3-5 写入，此处核对）

核对：`handleUsage` 的 doc 说清「为什么不发事件」与「去重是什么的防线」；`takeUsageUnchanged` 说清「为什么窗口也要纳入比较」；`AdapterEvent` 两个字段各自说清 nil/空的含义。

- [ ] **Step 9: 全量回归并提交**

```bash
go test ./...
git add internal/executor/executor.go internal/agentd/manager.go internal/agentd/manager_test.go
git commit -m "feat(agentd): 新增 usage 适配器事件，只写任务字段不进事件日志"
```

---

### Task 3: codex adapter（分子分母 + 实际模型名）

**Files:**
- Create: `internal/executor/codex/usage.go`
- Modify: `internal/executor/codex/adapter.go:40-45`（通知常量）、`:279-317`（`openThread`）、`:785` 附近（通知 switch 的 `default` 之前）
- Modify: `internal/executor/codex/export_test.go`（测试缝）
- Test: `internal/executor/codex/usage_test.go`（新建）

**Interfaces:**
- Consumes: Task 1 的 `proto.Usage`、Task 2 的 `AdapterEvent.ActualModel` / `.Usage`
- Produces: `parseTokenUsage(params json.RawMessage) (*proto.Usage, bool)`；`ParseTokenUsageForTest`（export_test 缝）

- [ ] **Step 1: 写失败测试**

新建 `internal/executor/codex/usage_test.go`：

```go
// codex 用量解析测试：输入是 08-13 从 codex app-server 实抓的真实报文
// （探针笔记 §1.1），不是手编的。
package codex_test

import (
	"testing"

	"github.com/xushixin/handoff/internal/executor/codex"
)

// TestParseTokenUsageTakesLastAndKeepsCacheAsSubset 覆盖 codex 的两条特殊规则：
// ①取 last 不取 total（total 是整个 thread 的累加，不是当前占用）；
// ②cachedInputTokens 是 inputTokens 的**子集**，绝不能再加一次。
func TestParseTokenUsageTakesLastAndKeepsCacheAsSubset(t *testing.T) {
	raw := []byte(`{"threadId":"019ffb3d","turnId":"019ffb3d","tokenUsage":{
      "total":{"totalTokens":99999,"inputTokens":99000,"cachedInputTokens":50000,
               "outputTokens":999,"reasoningOutputTokens":0},
      "last":{"totalTokens":24673,"inputTokens":24668,"cachedInputTokens":9984,
              "outputTokens":5,"reasoningOutputTokens":0},
      "modelContextWindow":258400}}`)

	u, ok := codex.ParseTokenUsageForTest(raw)
	if !ok || u == nil {
		t.Fatalf("应解析成功，得到 ok=%v u=%v", ok, u)
	}
	if u.ContextTokens != 24668 {
		t.Fatalf("必须取 last.inputTokens 24668（不是 total 的 99000，也不是加了缓存的 34652），得到 %d", u.ContextTokens)
	}
	if u.ContextWindow == nil || *u.ContextWindow != 258400 {
		t.Fatalf("分母应为 258400，得到 %v", u.ContextWindow)
	}
}

// TestParseTokenUsageRejectsEmpty 覆盖宽容解析：坏报文/零值不产生 Usage，
// 绝不用 0 冒充「占用为零」。
func TestParseTokenUsageRejectsEmpty(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(`not json`),
		[]byte(`{"tokenUsage":{"last":{"inputTokens":0},"modelContextWindow":258400}}`),
		[]byte(`{}`),
	} {
		if u, ok := codex.ParseTokenUsageForTest(raw); ok || u != nil {
			t.Fatalf("报文 %s 不该产生 Usage，得到 %+v", raw, u)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/executor/codex/ -run TestParseTokenUsage -v
```
Expected: FAIL，`undefined: codex.ParseTokenUsageForTest`。

- [ ] **Step 3: 写 `usage.go`**

```go
// usage.go —— codex 的 token 用量解析。
//
// 职责：把 thread/tokenUsage/updated 通知的 params 解析成 proto.Usage。
// 边界：纯函数，不碰 runState、不发事件、不写日志——接线在 adapter.go。
package codex

import (
	"encoding/json"

	"github.com/xushixin/handoff/internal/proto"
)

// parseTokenUsage 从 thread/tokenUsage/updated 的 params 里取当前 context 占用。
//
// 参数：
//   - params: 通知的 params 原文
//
// 返回：
//   - 解析成功且占用 > 0 时返回快照与 true；否则返回 nil 与 false
//
// 注意（两条 codex 独有的规则，改错了不会报错、只会显示错的数）：
//   - **取 `last` 不取 `total`**：`total` 是整个 thread 的累加，随回合单调增长，
//     不是「当前占用」。
//   - **`cachedInputTokens` 是 `inputTokens` 的子集，不再相加**：实抓佐证
//     `last.totalTokens 24673 = inputTokens 24668 + outputTokens 5`，
//     再加缓存就是重复计数。这一点与 grok/claudecode/opencode **相反**。
func parseTokenUsage(params json.RawMessage) (*proto.Usage, bool) {
	var p struct {
		TokenUsage struct {
			Last struct {
				InputTokens int `json:"inputTokens"`
			} `json:"last"`
			ModelContextWindow int `json:"modelContextWindow"`
		} `json:"tokenUsage"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, false
	}
	tokens := p.TokenUsage.Last.InputTokens
	if tokens <= 0 {
		return nil, false // 0 不是「占用为零」，是「还没有数」——不编造
	}
	u := &proto.Usage{ContextTokens: tokens}
	if w := p.TokenUsage.ModelContextWindow; w > 0 {
		u.ContextWindow = &w
	}
	return u, true
}
```

- [ ] **Step 4: 加测试缝**

`internal/executor/codex/export_test.go` 追加：

```go
// ParseTokenUsageForTest 暴露 token 用量解析，供 codex_test 包用真实报文断言。
func ParseTokenUsageForTest(params json.RawMessage) (*proto.Usage, bool) {
	return parseTokenUsage(params)
}
```

（同时在该文件的 import 块补 `"github.com/xushixin/handoff/internal/proto"`。）

- [ ] **Step 5: 跑测试确认通过**

```bash
go test ./internal/executor/codex/ -run TestParseTokenUsage -v
```
Expected: PASS。

- [ ] **Step 6: 接线——通知 switch**

`adapter.go` 的通知常量块（`:40-45`）追加：

```go
	ntfTokenUsage = "thread/tokenUsage/updated"
```

通知 switch 的 `default` 分支**之前**追加：

```go
	case method == ntfTokenUsage:
		// 这条通知排在 turn/completed 之前到达，回合结束时数据已在手。
		// turn/completed 的报文里没有任何用量字段，别去那儿找。
		if u, ok := parseTokenUsage(params); ok {
			a.emit(r, executor.AdapterEvent{Type: "usage", Usage: u})
		} else {
			a.log.Debug("codex 用量通知解析失败，跳过", "task", r.taskID)
		}
```

- [ ] **Step 7: 接线——实际模型名**

`openThread`（`:279-317`）里，把 `thread/start` 响应的解析从「只取 `thread.id`」扩成也取顶层 `model`：

```go
	var res struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		// Model 是本 thread **实际**使用的模型（如 "gpt-5.6-sol"）。
		// 它就在我们已经在读的这一帧的顶层，此前被整块丢弃。
		Model string `json:"model"`
	}
```

拿到 `res.Model` 后（在会话就绪的 progress 事件**之后**）补发一条：

```go
	if res.Model != "" {
		a.log.Info("codex 实际模型", "task", r.taskID, "model", res.Model)
		a.emit(r, executor.AdapterEvent{Type: "usage", ActualModel: res.Model})
	}
```

- [ ] **Step 8: 加日志与注释**（已在 Step 3/6/7 写入，此处核对）

核对：解析失败 → `Debug`（对端输出不可信，宽容跳过，与既有「未处理的通知」同级）；首次拿到模型名 → `Info` 带 `task` + `model`；`usage.go` 有文件头注释；`parseTokenUsage` 的 doc 写明两条 codex 独有规则**且注明与其余三家相反**。

- [ ] **Step 9: 全量回归并提交**

```bash
go test ./...
git add internal/executor/codex/
git commit -m "feat(codex): 解析 thread/tokenUsage/updated 与 thread/start 的实际模型名"
```

---

### Task 4: grok adapter（改早返回 + 两条私有通知 + 暂存窗口）

**Files:**
- Create: `internal/executor/grok/usage.go`
- Modify: `internal/executor/grok/adapter.go:815-838`（`OnNotify` 的早返回与分流）、`runState` 结构
- Modify: `internal/executor/grok/export_test.go`（若不存在则新建）
- Test: `internal/executor/grok/usage_test.go`（新建）

**Interfaces:**
- Consumes: Task 1、Task 2 的产出
- Produces: `parseResponseCompleted(params json.RawMessage) (*proto.Usage, bool)`；`parseModelsUpdate(params json.RawMessage) (model string, window int, ok bool)`；对应两个 `...ForTest` 缝

**这个 task 是全计划最容易改坏的一个**，三条硬约束：

1. **不动 `feedRaw`**（`adapter.go:626-661`）。它的职责是拼正文。
2. 改的是 `acpHandler.OnNotify` 的第一行早返回 `if method != "session/update" { return }`
   （`adapter.go:816`）——私有通知在这里就没了，压根到不了 `feedRaw`。
   **`session/update` 的原有处理逻辑必须一字不变地保留**，只是从「早返回后的直线」
   变成 switch 的一个分支。
3. **分子取 `response_completed`，不取 `turn_completed`。** 后者是跨模型调用的累加，
   实测 `modelCalls=4` 时是真实占用的 4 倍。

- [ ] **Step 1: 写失败测试**

新建 `internal/executor/grok/usage_test.go`：

```go
// grok 用量解析测试：输入是 08-13 从 grok agent serve 实抓的真实报文
// （探针笔记 §4.1 / §4.2），不是手编的。
package grok_test

import (
	"testing"

	"github.com/xushixin/handoff/internal/executor/grok"
)

// TestParseResponseCompletedAddsCache 覆盖 grok 的缓存规则：snake_case 的
// input_tokens **不含**缓存命中，必须相加——与 codex 的规则相反。
func TestParseResponseCompletedAddsCache(t *testing.T) {
	raw := []byte(`{"sessionId":"019ffb4e","update":{
      "sessionUpdate":"response_completed",
      "usage":{"input_tokens":64,"output_tokens":34,
               "cache_read_input_tokens":34688,"cache_creation_input_tokens":0,
               "reasoning_tokens":19}}}`)

	u, ok := grok.ParseResponseCompletedForTest(raw)
	if !ok || u == nil {
		t.Fatalf("应解析成功，得到 ok=%v u=%v", ok, u)
	}
	if u.ContextTokens != 34752 {
		t.Fatalf("应为 64+34688+0=34752（缓存要相加），得到 %d", u.ContextTokens)
	}
	if u.ContextWindow != nil {
		t.Fatalf("这一帧不带分母，ContextWindow 必须是 nil")
	}
}

// TestParseResponseCompletedIgnoresTurnCompleted 是本计划最重要的一条回归：
// turn_completed 是**跨模型调用的累加**，拿它当分子会静默显示 4 倍的错值。
// 实测 modelCalls=4 的回合里它是 138637，而真实占用只有 34752。
func TestParseResponseCompletedIgnoresTurnCompleted(t *testing.T) {
	raw := []byte(`{"sessionId":"019ffb4e","update":{
      "sessionUpdate":"turn_completed","stop_reason":"end_turn",
      "usage":{"inputTokens":138637,"outputTokens":219,"totalTokens":138856,
               "cachedReadTokens":109568,"modelCalls":4}}}`)

	if u, ok := grok.ParseResponseCompletedForTest(raw); ok || u != nil {
		t.Fatalf("turn_completed 绝不能产生 Usage，得到 %+v", u)
	}
}

// TestParseModelsUpdateMatchesCurrentModel 覆盖分母：availableModels 是数组，
// 必须按 currentModelId 匹配，不能取第 0 个。
func TestParseModelsUpdateMatchesCurrentModel(t *testing.T) {
	raw := []byte(`{"currentModelId":"grok-4.6","availableModels":[
      {"modelId":"grok-3","name":"Grok 3","_meta":{"totalContextTokens":128000}},
      {"modelId":"grok-4.6","name":"Grok 4.6","_meta":{"totalContextTokens":500000}}]}`)

	model, window, ok := grok.ParseModelsUpdateForTest(raw)
	if !ok {
		t.Fatalf("应解析成功")
	}
	if model != "grok-4.6" {
		t.Fatalf("model = %q，期望 grok-4.6", model)
	}
	if window != 500000 {
		t.Fatalf("window = %d，期望 500000（不是第 0 个的 128000）", window)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/executor/grok/ -run 'TestParseResponseCompleted|TestParseModelsUpdate' -v
```
Expected: FAIL，`undefined: grok.ParseResponseCompletedForTest`。

- [ ] **Step 3: 写 `usage.go`**

```go
// usage.go —— grok 的 token 用量与实际模型名解析。
//
// 职责：把两条 _x.ai/* 私有通知解析成 proto.Usage 与模型名/窗口。
// 边界：纯函数，不碰 runState、不发事件、不写日志——接线在 adapter.go。
//
// 为什么用量在私有通知上：grok 的 ACP 线把用量放在 _x.ai/session_notification
// 与 _x.ai/models/update 上，标准的 session/update 变体一个都不带计数。
package grok

import (
	"encoding/json"

	"github.com/xushixin/handoff/internal/proto"
)

// parseResponseCompleted 从 _x.ai/session_notification 的 params 里取当前
// context 占用；只认 sessionUpdate == "response_completed"。
//
// 参数：
//   - params: 通知的 params 原文
//
// 返回：
//   - 是 response_completed 且占用 > 0 时返回快照与 true；否则 nil 与 false
//
// 注意（两条规则，改错了不会报错、只会显示错的数）：
//   - **只认 response_completed，绝不认 turn_completed**：后者的 usage 是
//     整回合**跨模型调用的累加**。实测 modelCalls=4 的回合里
//     `inputTokens 138637` 恰等于四次调用的 input_tokens 之和加 cache_read 之和，
//     而真实占用只有 34752——差 4 倍，长回合会超过 100%（探针笔记 §4.2）。
//     turn_completed 是将来做「累计消耗」的正确来源，不是这里的。
//   - **snake_case 的 input_tokens 不含缓存，必须相加**；而 turn_completed 的
//     camelCase inputTokens 已含缓存。同一条线两套约定，**按字段名模糊匹配必错**。
func parseResponseCompleted(params json.RawMessage) (*proto.Usage, bool) {
	var p struct {
		Update struct {
			Kind  string `json:"sessionUpdate"`
			Usage struct {
				InputTokens         int `json:"input_tokens"`
				CacheReadTokens     int `json:"cache_read_input_tokens"`
				CacheCreationTokens int `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		} `json:"update"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, false
	}
	if p.Update.Kind != "response_completed" {
		return nil, false
	}
	tokens := p.Update.Usage.InputTokens + p.Update.Usage.CacheReadTokens +
		p.Update.Usage.CacheCreationTokens
	if tokens <= 0 {
		return nil, false
	}
	return &proto.Usage{ContextTokens: tokens}, true
}

// parseModelsUpdate 从 _x.ai/models/update 的 params 里取当前模型名与窗口上限。
//
// 返回：
//   - model: currentModelId；window: 该模型的 _meta.totalContextTokens（取不到为 0）
//   - ok: currentModelId 非空时为 true
//
// 注意：availableModels 是**数组且可能含多个模型**，必须按 currentModelId 匹配，
// 取第 0 个会在多模型场景下拿到别的模型的窗口——又一个静默错误。
func parseModelsUpdate(params json.RawMessage) (string, int, bool) {
	var p struct {
		CurrentModelID  string `json:"currentModelId"`
		AvailableModels []struct {
			ModelID string `json:"modelId"`
			Meta    struct {
				TotalContextTokens int `json:"totalContextTokens"`
			} `json:"_meta"`
		} `json:"availableModels"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.CurrentModelID == "" {
		return "", 0, false
	}
	for _, m := range p.AvailableModels {
		if m.ModelID == p.CurrentModelID {
			return p.CurrentModelID, m.Meta.TotalContextTokens, true
		}
	}
	return p.CurrentModelID, 0, true // 有模型名没窗口，也是有效信息
}
```

- [ ] **Step 4: 加测试缝**

`internal/executor/grok/export_test.go`（**已存在**，直接追加，不要重写文件头）：

```go
// ParseResponseCompletedForTest 暴露用量解析，供 grok_test 包用真实报文断言。
func ParseResponseCompletedForTest(params json.RawMessage) (*proto.Usage, bool) {
	return parseResponseCompleted(params)
}

// ParseModelsUpdateForTest 暴露模型/窗口解析。
func ParseModelsUpdateForTest(params json.RawMessage) (string, int, bool) {
	return parseModelsUpdate(params)
}
```

- [ ] **Step 5: 跑测试确认通过**

```bash
go test ./internal/executor/grok/ -run 'TestParseResponseCompleted|TestParseModelsUpdate' -v
```
Expected: PASS。

- [ ] **Step 6: 接线——改 `OnNotify` 的早返回**

`runState` 结构追加两个字段（受既有的 `turnMu` 保护）：

```go
	// ctxWindow 是当前模型的上下文窗口上限，由 _x.ai/models/update 带来（0=未知）。
	// 为什么要暂存：分子与分母来自**不同的帧**——窗口在会话建立后立刻到，
	// 占用在每次模型调用后到。只发分子的话分母永远补不上
	//（manager 的「nil=不更新」保护的是已落库的值，不是从没落过库的值）。
	ctxWindow int
	// actualModel 是 grok 报回的实际模型名（同上，随用量一起发出去）。
	actualModel string
```

`OnNotify` 改成分流（**`session/update` 的原有 7 行逻辑一字不变地搬进第一个分支**）：

```go
// OnNotify 分流对方通知。
//
// 三类：session/update（正文与工具调用，原有链路）、_x.ai/session_notification
// （用量）、_x.ai/models/update（模型名与窗口）。其余私有通知继续忽略。
//
// 为什么这里要认 _x.ai/*：grok 把用量放在私有通知上，标准的 session/update
// 变体一个都不带计数。此前这个函数的第一行是 `if method != "session/update"
// { return }`，私有通知在那里就没了——它们压根到不了 feedRaw。
func (h *acpHandler) OnNotify(method string, params json.RawMessage) {
	switch method {
	case "session/update":
		h.onSessionUpdate(params)
	case "_x.ai/session_notification":
		h.onUsageNotification(params)
	case "_x.ai/models/update":
		h.onModelsUpdate(params)
	}
}

// onSessionUpdate 是原 OnNotify 的正文链路，一字未改，只是从早返回后的直线
// 变成 switch 的一个分支。
func (h *acpHandler) onSessionUpdate(params json.RawMessage) {
	h.r.turnMu.Lock()
	raw := append([]byte(`{"method":"session/update","params":`), append(params, '}')...)
	h.r.acc.feedRaw(raw)
	// W4a：turnAccumulator 是纯累积器，不该带 I/O——帧在它的调用方分流。
	// bodyBuf / renderBuf 的两股走向一字不改，这里只是多一路输出
	if kind, text := updateFrameFields(raw); text != "" {
		switch updateFrameKind(kind) {
		case updateText:
			if err := h.r.frames.Text(h.r.textPart, text); err != nil {
				h.a.log.Warn("写 text 帧失败，不影响回合", "task", h.r.taskID, "cause", err)
			}
		case updateReasoning:
			if err := h.r.frames.Reasoning(h.r.textPart, text); err != nil {
				h.a.log.Warn("写 reasoning 帧失败，不影响回合", "task", h.r.taskID, "cause", err)
			}
		}
	}
	h.r.turnMu.Unlock()
	h.a.flushRender(h.r)
}

// onUsageNotification 处理 _x.ai/session_notification：只有 response_completed
// 会产出用量，其余（turn_completed 等）一律忽略——理由见 parseResponseCompleted。
func (h *acpHandler) onUsageNotification(params json.RawMessage) {
	u, ok := parseResponseCompleted(params)
	if !ok {
		return // 不是 response_completed，或没有有效数字：静默跳过，不是错误
	}
	h.r.turnMu.Lock()
	if h.r.ctxWindow > 0 {
		w := h.r.ctxWindow
		u.ContextWindow = &w
	}
	model := h.r.actualModel
	h.r.turnMu.Unlock()
	h.a.emit(h.r, executor.AdapterEvent{Type: "usage", ActualModel: model, Usage: u})
}

// onModelsUpdate 处理 _x.ai/models/update：记下模型名与窗口，供后续用量帧带上。
func (h *acpHandler) onModelsUpdate(params json.RawMessage) {
	model, window, ok := parseModelsUpdate(params)
	if !ok {
		h.a.log.Debug("grok 模型通知解析失败，跳过", "task", h.r.taskID)
		return
	}
	h.r.turnMu.Lock()
	changed := h.r.actualModel != model || h.r.ctxWindow != window
	h.r.actualModel, h.r.ctxWindow = model, window
	h.r.turnMu.Unlock()
	if changed {
		h.a.log.Info("grok 实际模型", "task", h.r.taskID, "model", model, "window", window)
	}
	// 模型名先单发一次：回合还没开始就能显示，不必等第一次模型调用完成
	h.a.emit(h.r, executor.AdapterEvent{Type: "usage", ActualModel: model})
}
```

- [ ] **Step 7: 跑 grok 全包测试确认没改坏正文链路**

```bash
go test ./internal/executor/grok/ -v
```
Expected: PASS，特别是 `TestMapUpdateRoutesByKind` 与 `TestTurnTextEndsWithTrailerSoParseWorks`——它们守的是被搬动的那段正文逻辑。

- [ ] **Step 8: 加日志与注释**（已在 Step 3/6 写入，此处核对）

核对：模型/窗口首次到达或变化 → `Info` 带 `task` / `model` / `window`；解析失败 → `Debug`；用量帧本身**不打日志**（每次模型调用一条，高频）；`parseResponseCompleted` 的 doc 必须明写「不取 turn_completed 及其理由」并指向探针笔记 §4.2——这是最容易被后人「顺手改对称」改坏的一处。

- [ ] **Step 9: 全量回归并提交**

```bash
go test ./...
git add internal/executor/grok/
git commit -m "feat(grok): OnNotify 认两条 _x.ai 私有通知，取 response_completed 做分子"
```

---

### Task 5: claudecode adapter

**Files:**
- Create: `internal/executor/claudecode/usage.go`
- Modify: `internal/executor/claudecode/adapter.go:512-516`（`init` 分支）、`:567`（`mapAssistant`）
- Modify/Create: `internal/executor/claudecode/export_test.go`
- Test: `internal/executor/claudecode/usage_test.go`（新建）

**Interfaces:**
- Produces: `parseAssistantUsage(msg json.RawMessage) (model string, u *proto.Usage, ok bool)`；`ParseAssistantUsageForTest`

- [ ] **Step 1: 写失败测试**

新建 `internal/executor/claudecode/usage_test.go`：

```go
// claudecode 用量解析测试：输入取自 out.jsonl 的真实 assistant 消息形状
// （探针笔记 §2）。注意 model 与 usage 在 message 对象内部，与 content 同级。
package claudecode_test

import (
	"testing"

	"github.com/xushixin/handoff/internal/executor/claudecode"
)

// TestParseAssistantUsageAddsCache 覆盖 claudecode 的缓存规则：三项相加。
func TestParseAssistantUsageAddsCache(t *testing.T) {
	// mapAssistant 收到的正是 message 对象本身
	msg := []byte(`{"model":"k3-256k","content":[{"type":"text","text":"hi"}],
      "usage":{"input_tokens":121801,"cache_creation_input_tokens":2000,
               "cache_read_input_tokens":5000,"output_tokens":42}}`)

	model, u, ok := claudecode.ParseAssistantUsageForTest(msg)
	if !ok {
		t.Fatalf("应解析成功")
	}
	if model != "k3-256k" {
		t.Fatalf("model = %q，期望 k3-256k", model)
	}
	if u.ContextTokens != 128801 {
		t.Fatalf("应为 121801+5000+2000=128801，得到 %d", u.ContextTokens)
	}
	if u.ContextWindow != nil {
		t.Fatalf("claudecode 不报窗口，ContextWindow 必须是 nil")
	}
}

// TestParseAssistantUsageSkipsZero 覆盖零值：没有有效数字时不产生 Usage，
// 但模型名仍然有效（模型名与用量是两件事）。
func TestParseAssistantUsageSkipsZero(t *testing.T) {
	msg := []byte(`{"model":"k3-256k","usage":{"input_tokens":0,
      "cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`)
	model, u, ok := claudecode.ParseAssistantUsageForTest(msg)
	if !ok || model != "k3-256k" {
		t.Fatalf("模型名应仍然有效，得到 ok=%v model=%q", ok, model)
	}
	if u != nil {
		t.Fatalf("零值不该产生 Usage，得到 %+v", u)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/executor/claudecode/ -run TestParseAssistantUsage -v
```
Expected: FAIL，`undefined: claudecode.ParseAssistantUsageForTest`。

- [ ] **Step 3: 写 `usage.go`**

```go
// usage.go —— claudecode 的 token 用量与实际模型名解析。
//
// 职责：把 assistant 消息里的 model 与 usage 解析出来。
// 边界：纯函数，不碰 runState、不发事件、不写日志——接线在 adapter.go。
package claudecode

import (
	"encoding/json"

	"github.com/xushixin/handoff/internal/proto"
)

// parseAssistantUsage 从一条 assistant 的 **message 对象**里取模型名与 context 占用。
//
// 参数：
//   - msg: streamMsg.Message 的原文（model / content / usage 都在这一层，
//     与 content 同级；**不在 stream 行的顶层**）
//
// 返回：
//   - model: 该条消息的模型名（可能为空）
//   - u: context 占用；零值时为 nil（**不用 0 冒充**）
//   - ok: 报文可解析时为 true
//
// 注意：**缓存两项要相加**（`input_tokens` 不含缓存），与 codex 的规则相反。
// claudecode 的协议里没有任何字段给窗口上限，所以 ContextWindow 恒为 nil，
// 界面据此只显绝对值——不去猜、不查表。
func parseAssistantUsage(msg json.RawMessage) (string, *proto.Usage, bool) {
	var m struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens         int `json:"input_tokens"`
			CacheCreationTokens int `json:"cache_creation_input_tokens"`
			CacheReadTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(msg, &m); err != nil {
		return "", nil, false
	}
	tokens := m.Usage.InputTokens + m.Usage.CacheCreationTokens + m.Usage.CacheReadTokens
	if tokens <= 0 {
		return m.Model, nil, true // 模型名与用量是两件事，前者仍然有效
	}
	return m.Model, &proto.Usage{ContextTokens: tokens}, true
}
```

- [ ] **Step 4: 加测试缝**

`internal/executor/claudecode/export_test.go` —— **该文件不存在，新建**，整份内容如下
（形态照抄 `internal/executor/codex/export_test.go`：同包、只做转发、不含逻辑）：

```go
// export_test.go —— 把包内未导出的解析函数暴露给 claudecode_test 外部测试包。
//
// 边界：只做转发，不含任何逻辑。它随测试一起编译，不进产物。
package claudecode

import (
	"encoding/json"

	"github.com/xushixin/handoff/internal/proto"
)

// ParseAssistantUsageForTest 暴露 assistant 消息的模型/用量解析。
func ParseAssistantUsageForTest(msg json.RawMessage) (string, *proto.Usage, bool) {
	return parseAssistantUsage(msg)
}
```

- [ ] **Step 5: 跑测试确认通过**

```bash
go test ./internal/executor/claudecode/ -run TestParseAssistantUsage -v
```
Expected: PASS。

- [ ] **Step 6: 接线**

`mapAssistant`（`:567`）开头、既有 `content` 解析**之前**插入：

```go
	// 模型名与用量与 content 同级，就在这条消息里——此前整块丢弃。
	// 每条 assistant 消息都带，manager 侧靠去重防写库风暴。
	if model, u, ok := parseAssistantUsage(msg); ok && (model != "" || u != nil) {
		a.emit(r, executor.AdapterEvent{Type: "usage", ActualModel: model, Usage: u})
	}
```

`mapMessage` 的 `init` 分支（`:512-516`）里，在既有的 progress 事件之后补发模型名。
`streamMsg` 需要多一个字段来接 init 行的顶层 `model`：

```go
	Model     string          `json:"model"` // system/init 行携带的实际模型名
```

```go
	case m.Type == "system" && m.Subtype == "init":
		a.log.Info("claude 会话就绪", "task", r.taskID, "session", m.SessionID)
		r.session = m.SessionID
		r.markReady()
		a.emit(r, executor.AdapterEvent{Type: "progress", SessionID: m.SessionID, Text: "会话就绪"})
		// init 行就带实际模型名，比第一条 assistant 消息早。
		// result 行的 model 是 null，别去那儿取。
		if m.Model != "" {
			a.log.Info("claude 实际模型", "task", r.taskID, "model", m.Model)
			a.emit(r, executor.AdapterEvent{Type: "usage", ActualModel: m.Model})
		}
```

- [ ] **Step 7: 全量回归并提交**

```bash
go test ./...
git add internal/executor/claudecode/
git commit -m "feat(claudecode): 解析 assistant 的 model 与 usage，init 行补模型名"
```

---

### Task 6: opencode adapter

**Files:**
- Create: `internal/executor/opencode/usage.go`
- Modify: `internal/executor/opencode/adapter.go:1390`（`mapMessageUpdated`）
- Modify/Create: `internal/executor/opencode/export_test.go`
- Test: `internal/executor/opencode/usage_test.go`（新建）

**Interfaces:**
- Produces: `parseMessageUsage(props json.RawMessage) (model string, u *proto.Usage, ok bool)`；`ParseMessageUsageForTest`

- [ ] **Step 1: 写失败测试**

新建 `internal/executor/opencode/usage_test.go`：

```go
// opencode 用量解析测试：输入是 08-13 旁听 mac-02 一个 running 任务的
// /event SSE 抓到的真实帧（探针笔记 §3.1）。
package opencode_test

import (
	"testing"

	"github.com/xushixin/handoff/internal/executor/opencode"
)

// TestParseMessageUsageAddsCacheNotTotal 覆盖两条规则：缓存要相加；
// **不能取 tokens.total**——total 含 output 与 reasoning，不是 context 占用。
func TestParseMessageUsageAddsCacheNotTotal(t *testing.T) {
	props := []byte(`{"sessionID":"ses_x","info":{"id":"msg_1","role":"assistant",
      "cost":0.0001408596,
      "tokens":{"total":47071,"input":131,"output":182,"reasoning":294,
                "cache":{"write":0,"read":46464}},
      "modelID":"deepseek-v4-flash","providerID":"opencode-go",
      "time":{"created":1786628040082,"completed":1786628048168}}}`)

	model, u, ok := opencode.ParseMessageUsageForTest(props)
	if !ok {
		t.Fatalf("应解析成功")
	}
	if model != "deepseek-v4-flash" {
		t.Fatalf("model = %q", model)
	}
	if u == nil || u.ContextTokens != 46595 {
		t.Fatalf("应为 131+46464+0=46595（不是 total 的 47071），得到 %+v", u)
	}
	if u.ContextWindow != nil {
		t.Fatalf("opencode 不报窗口，ContextWindow 必须是 nil")
	}
}

// TestParseMessageUsageSkipsFreshMessage 覆盖界面陷阱：新建的 assistant 消息
// tokens 全是 0，同一条消息随后才被补完。若不跳过零值帧，界面会在每条新消息
// 开头闪回 0。
func TestParseMessageUsageSkipsFreshMessage(t *testing.T) {
	props := []byte(`{"sessionID":"ses_x","info":{"id":"msg_2","role":"assistant",
      "cost":0,"tokens":{"input":0,"output":0,"reasoning":0,
      "cache":{"read":0,"write":0}},"modelID":"deepseek-v4-flash",
      "time":{"created":1786628048172}}}`)

	model, u, ok := opencode.ParseMessageUsageForTest(props)
	if !ok || model != "deepseek-v4-flash" {
		t.Fatalf("模型名应仍然有效，得到 ok=%v model=%q", ok, model)
	}
	if u != nil {
		t.Fatalf("零值帧不该产生 Usage（否则界面闪回 0），得到 %+v", u)
	}
}

// TestParseMessageUsageIgnoresUserMessage 覆盖角色过滤：只算模型输出侧。
func TestParseMessageUsageIgnoresUserMessage(t *testing.T) {
	props := []byte(`{"info":{"id":"msg_3","role":"user",
      "tokens":{"input":99,"cache":{"read":1}},"modelID":"x"}}`)
	if _, u, _ := opencode.ParseMessageUsageForTest(props); u != nil {
		t.Fatalf("user 消息不该产生 Usage，得到 %+v", u)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/executor/opencode/ -run TestParseMessageUsage -v
```
Expected: FAIL，`undefined: opencode.ParseMessageUsageForTest`。

- [ ] **Step 3: 写 `usage.go`**

```go
// usage.go —— opencode 的 token 用量与实际模型名解析。
//
// 职责：把 message.updated 的 properties.info 解析成模型名与 proto.Usage。
// 边界：纯函数，不碰 runState、不发事件、不写日志——接线在 adapter.go。
package opencode

import (
	"encoding/json"

	"github.com/xushixin/handoff/internal/proto"
)

// parseMessageUsage 从 message.updated 的 properties 里取模型名与 context 占用。
//
// 参数：
//   - props: 事件的 properties 原文（info 是完整的 message 对象）
//
// 返回：
//   - model: info.modelID；u: context 占用，零值或非 assistant 时为 nil；
//     ok: 报文可解析时为 true
//
// 注意（两条，都会导致界面显示错的数）：
//   - **不能取 `tokens.total`**：实测 `total 47071 = input 131 + output 182 +
//     reasoning 294 + cache.read 46464`，它含产出侧，不是占用。
//     占用是 `input + cache.read + cache.write`。
//   - **零值帧必须跳过**：同一条消息会被推多次，且**新建的 assistant 消息
//     tokens 全是 0**。不跳过的话，界面会在每条新消息开头闪回 0
//     （探针笔记 §3.1）。
func parseMessageUsage(props json.RawMessage) (string, *proto.Usage, bool) {
	var p struct {
		Info struct {
			Role    string `json:"role"`
			ModelID string `json:"modelID"`
			Tokens  struct {
				Input int `json:"input"`
				Cache struct {
					Read  int `json:"read"`
					Write int `json:"write"`
				} `json:"cache"`
			} `json:"tokens"`
		} `json:"info"`
	}
	if err := json.Unmarshal(props, &p); err != nil {
		return "", nil, false
	}
	if p.Info.Role != "assistant" {
		return "", nil, true // user 消息没有模型侧用量，不是错误
	}
	tokens := p.Info.Tokens.Input + p.Info.Tokens.Cache.Read + p.Info.Tokens.Cache.Write
	if tokens <= 0 {
		return p.Info.ModelID, nil, true // 新建的消息，还没数字
	}
	return p.Info.ModelID, &proto.Usage{ContextTokens: tokens}, true
}
```

- [ ] **Step 4: 加测试缝**

`internal/executor/opencode/export_test.go` —— **该文件不存在，新建**，整份内容如下：

```go
// export_test.go —— 把包内未导出的解析函数暴露给 opencode_test 外部测试包。
//
// 边界：只做转发，不含任何逻辑。它随测试一起编译，不进产物。
package opencode

import (
	"encoding/json"

	"github.com/xushixin/handoff/internal/proto"
)

// ParseMessageUsageForTest 暴露 message.updated 的模型/用量解析。
func ParseMessageUsageForTest(props json.RawMessage) (string, *proto.Usage, bool) {
	return parseMessageUsage(props)
}
```

- [ ] **Step 5: 跑测试确认通过**

```bash
go test ./internal/executor/opencode/ -run TestParseMessageUsage -v
```
Expected: PASS。

- [ ] **Step 6: 接线**

`mapMessageUpdated`（`:1390`）里，在既有的 `userMsgs` 登记逻辑**之前**插入：

```go
	// 模型名与用量就在这一帧的 info 里——此前只解了 id 与 role。
	// 零值帧由 parseMessageUsage 内部跳过，否则界面会在每条新消息开头闪回 0。
	if model, u, ok := parseMessageUsage(props); ok && (model != "" || u != nil) {
		a.emit(r, executor.AdapterEvent{Type: "usage", ActualModel: model, Usage: u})
	}
```

（注意：`mapMessageUpdated` 现有实现在解析失败时会 `Debug` 并 `return`，
用量解析必须放在那之前，否则解析失败的帧会连用量一起丢——两者的解析是独立的。）

- [ ] **Step 7: 全量回归并提交**

```bash
go test ./...
git add internal/executor/opencode/
git commit -m "feat(opencode): 解析 message.updated 的 modelID 与 tokens，跳过零值帧"
```

---

### Task 7: 界面（types.ts + format.ts + TaskHeader）

**Files:**
- Modify: `web/src/api/types.ts:15-38`（`Task` 追加两个字段 + 新增 `Usage`）
- Modify: `web/src/app/lib/format.ts`（新增 `formatTokens` / `formatExecutorLine`）
- Modify: `web/src/app/task/TaskHeader.tsx:60-61`（「执行器」行）
- Modify: `web/src/api/contract.test.ts:50`（契约断言补两个新键）
- Test: `web/src/app/lib/format.test.ts`（**新建**——该目录下目前没有 format 的测试）、
  `web/src/app/task/TaskHeader.test.tsx`（追加）

**Interfaces:**
- Consumes: Task 1 的 JSON 形状（`actual_model` / `usage.context_tokens` / `usage.context_window`）
- Produces: `formatTokens(n: number): string`；`formatExecutorLine(task: Task): string`

- [ ] **Step 1: 写失败测试**

新建 `web/src/app/lib/format.test.ts`（该目录下目前没有 format 的测试，这是新文件，
文件头与 import 都要写全）：

```ts
// format.ts 的单元测试：这些是纯函数，不需要 DOM，直接喂值断言。
//
// 覆盖重点是「缺省怎么显示」——B80 的产品决策是「如实缺席」：没有分母就不显
// 百分比，没有用量就不显用量，绝不用 0 或 — 占位。
import { describe, expect, it } from 'vitest'

import { formatExecutorLine, formatTokens } from './format'

describe('formatTokens', () => {
  it('千位以上用 k 缩写并保留一位小数', () => {
    expect(formatTokens(24673)).toBe('24.7k')
    expect(formatTokens(258400)).toBe('258.4k')
  })
  it('千位以下显示原值', () => {
    expect(formatTokens(999)).toBe('999')
  })
})

describe('formatExecutorLine', () => {
  const base = { executor: 'codex', model: 'ignored' } as never

  it('有分子分母时显示百分比', () => {
    expect(
      formatExecutorLine({
        ...base,
        actual_model: 'gpt-5.6-sol',
        usage: { context_tokens: 24673, context_window: 258400 },
      } as never),
    ).toBe('codex · gpt-5.6-sol · 24.7k / 258.4k (10%)')
  })

  it('只有分子时只显绝对值，不猜分母', () => {
    expect(
      formatExecutorLine({
        ...base,
        executor: 'claudecode',
        actual_model: 'k3-256k',
        usage: { context_tokens: 121801 },
      } as never),
    ).toBe('claudecode · k3-256k · 121.8k tokens')
  })

  it('回合未开始时只显执行器与模型名', () => {
    expect(
      formatExecutorLine({ ...base, actual_model: 'gpt-5.6-sol' } as never),
    ).toBe('codex · gpt-5.6-sol')
  })

  it('什么都没有时只显执行器；入参 model 不再显示', () => {
    expect(formatExecutorLine({ executor: 'opencode', model: 'deepseek-v4-flash' } as never))
      .toBe('opencode')
  })

  it('连执行器都没有的老任务显示（缺省）', () => {
    expect(formatExecutorLine({} as never)).toBe('（缺省）')
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

```bash
cd web && npm test -- format
```
Expected: FAIL，`formatTokens is not exported`。

- [ ] **Step 3: 加 TS 类型**

`web/src/api/types.ts` 的 `Task` 里，`machine` 之前追加：

```ts
  actual_model?: string   // executor 报回的实际模型名；缺省=还没报（与入参 model 不是一回事）
  usage?: Usage           // 当前 context 占用；缺省=还没有任何一次模型调用完成
```

在 `Task` 之后新增：

```ts
// Usage 是任务当前的 context 占用。
// context_window 缺省表示该 executor 不在协议里报窗口（claudecode / opencode），
// 此时只显绝对值——前端绝不自己猜分母。
export interface Usage {
  context_tokens: number
  context_window?: number
}
```

- [ ] **Step 4: 写格式化函数**

`web/src/app/lib/format.ts` 追加：

```ts
// formatTokens 把 token 数格式化成人眼可读的短串：千位以上用 k 并保留一位小数。
export function formatTokens(n: number): string {
  if (n < 1000) return String(n)
  return `${(n / 1000).toFixed(1)}k`
}

// formatExecutorLine 组装任务详情页「执行器」行的整行文案。
//
// 三段各自「有才显示」，不占位、不显示 — 或 0：
//   执行器 · 实际模型名 · 24.7k / 258.4k (10%)
//
// 两条产品决策写死在这里：
//   - **只显实际模型名**（task.actual_model）。task.model 是 dispatch 的入参，
//     用户要知道的是「现在实际跑在什么上」，二者不一致也不提示。
//   - **只有拿到分母才显百分比**。分母缺席就只显绝对值，绝不由前端猜一个
//     ——猜错是静默错误，百分比照常显示只是错的。
export function formatExecutorLine(task: Task): string {
  const parts: string[] = [task.executor || '（缺省）']
  if (task.actual_model) parts.push(task.actual_model)
  const u = task.usage
  if (u && u.context_tokens > 0) {
    if (u.context_window && u.context_window > 0) {
      const pct = Math.round((u.context_tokens / u.context_window) * 100)
      parts.push(`${formatTokens(u.context_tokens)} / ${formatTokens(u.context_window)} (${pct}%)`)
    } else {
      parts.push(`${formatTokens(u.context_tokens)} tokens`)
    }
  }
  return parts.join(' · ')
}
```

（`format.ts` 顶部补 `import type { Task } from '../../api/types'`。）

- [ ] **Step 5: 跑测试确认通过**

```bash
cd web && npm test -- format
```
Expected: PASS。

- [ ] **Step 6: 接进 TaskHeader**

`web/src/app/task/TaskHeader.tsx:60-61` 改成：

```tsx
        <dt className="text-muted-foreground">执行器</dt>
        <dd>{formatExecutorLine(task)}</dd>
```

（import 从 `../lib/format` 补 `formatExecutorLine`；原来的 `task.model` 拼接整行删除。）

- [ ] **Step 7: 补组件测试**

追加到 `web/src/app/task/TaskHeader.test.tsx`：

```tsx
  it('执行器行显示实际模型名与 context 占用', () => {
    const withUsage = {
      ...task,
      actual_model: 'gpt-5.6-sol',
      usage: { context_tokens: 24673, context_window: 258400 },
    } as unknown as Task
    render(<TaskHeader task={withUsage} />)
    expect(screen.getByText('opencode · gpt-5.6-sol · 24.7k / 258.4k (10%)')).toBeInTheDocument()
  })

  it('旧任务没有用量字段时不报错，只显执行器', () => {
    render(<TaskHeader task={task} />)
    expect(screen.getByText('opencode')).toBeInTheDocument()
  })
```

- [ ] **Step 8: 补契约断言**

`web/src/api/contract.test.ts` 的文件头写着「任何一方改动线格式（字段改名/增删）
都必须同步 Go 结构体、fixture 与本文件」。Task 1 已经改了前两样，这里补第三样。

`'Task：可解析为 Task 类型，关键字段齐全'` 用例（`:50`）里，那个 `for (const key of [...])`
数组末尾追加两个键：

```ts
'repo_dirty_files', 'actual_model', 'usage']) {
```

并在该 `describe` 末尾（`'Task 带 machine 与 project_id 两个注解字段'` 之后）追加：

```ts
  it('Task 的 usage：分子必填、分母可选', () => {
    const t: Task = taskFixture
    expect(t.actual_model).toBe('gpt-5.6-sol')
    expect(t.usage?.context_tokens).toBe(24668)
    // 分母在 fixture 里有值；不报窗口的 executor 会让这个键整个缺席，
    // 而不是给 0——那是「如实缺席」的线格式约定
    expect(t.usage?.context_window).toBe(258400)
  })
```

- [ ] **Step 9: 跑 web 全量**

```bash
cd web && npm test
```
Expected: PASS。

- [ ] **Step 10: 加注释**（已在 Step 3/4 写入，此处核对）

核对：`Usage` 的 TS 类型注释说明「缺省=不报窗口」；`formatExecutorLine` 的注释写明两条产品决策**及其理由**（尤其「绝不猜分母」）。

- [ ] **Step 11: 提交**

```bash
git add web/src/api/ web/src/app/lib/ web/src/app/task/
git commit -m "feat(web): 执行器行显示实际模型名与 context 占用"
```

---

## 完工前自检（instrumenting-code 清单）

声明完成之前逐项核对，任一未过就修：

- [ ] 每个错误分支都带上下文与 cause（`Warn`/`Debug`，四家解析失败 + manager 落库失败）
- [ ] 成功路径不静默：模型名首次到达打 `Info`，用量首次落库打 `Info`
- [ ] 高频路径**不打日志**：用量帧本身、去重命中、`handleEvent` 的 usage 入口
- [ ] 没有 `fmt.Printf` / `println` 当日志用
- [ ] 四个新建的 `usage.go` 都有文件头注释（职责 + 边界）
- [ ] 每个导出/包级函数有 doc 注释，写清参数、返回、注意事项
- [ ] 四家的缓存规则各自在代码注释里写明「加还是不加」**以及为什么**
- [ ] grok 的 `parseResponseCompleted` 注释里明写「不取 turn_completed」并指向探针笔记 §4.2

## 最终验收（对齐 spec §10）

- [ ] `go test ./...` 全绿
- [ ] `cd web && npm test` 全绿
- [ ] 四家 executor 各派一个真实任务，详情页显示实际模型名；codex 与 grok 另显百分比，claudecode 与 opencode 只显绝对值
- [ ] grok 在一个含多次工具调用的回合后百分比**仍然合理**（不超过 100%）——§4.2 那个 4 倍偏差的回归检查
- [ ] 事件日志里没有因用量新增的事件行
- [ ] 旧任务详情页不报错，只显执行器名
