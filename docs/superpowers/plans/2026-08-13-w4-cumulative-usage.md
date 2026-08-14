# W4e 累计用量与花费 实现计划（B83）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 任务详情页的「执行器」行加一个切换按钮，切过去后显示这个任务的累计消耗——总量、输入、缓存输入、输出、花费，四家 executor 全覆盖，跨进程恢复不算错。

**Architecture:** adapter 把「这一次新增了多少」作为 `SpendEntry` 上报（带幂等键），manager 落进新的 `task_usage_ledger` 表（同键**覆盖**写），读任务时对该表求和得到累计值。不依赖任何 executor 自己的累计字段——实测那些字段在 `--resume` 后归零，是「进程累计」不是「会话累计」。

**Tech Stack:** Go 1.23（标准库 + `modernc.org/sqlite`）、React 19 + TypeScript + Vite、vitest、shadcn/ui。

## Global Constraints

- **基线分支是 `feat/b80-executor-model-usage`，不是 `main`。** 本计划依赖 B80 引入的 `proto.Usage`、`Task.ActualModel`、`formatExecutorLine`、`TaskHeader` 的「执行器」行。
- **「当前占用」与「累计消耗」是两个口径，两套帧，绝不互相赋值。** `proto.Usage` 描述前者（最后一次模型调用的输入侧），本计划新增的 `proto.Cumulative` 描述后者（跨全部调用累加）。数量级差几倍到几十倍。任何一处把 `Usage` 的值写进 ledger、或拿 ledger 的和去填 `Usage`，都是本计划最严重的缺陷。
- **`SetTaskUsage` 绝不用于写累计。** 它整体覆盖 `(actual_model, usage_context_tokens, usage_context_window)` 三元组，是 B80 的当前占用通道。累计走 `UpsertSpend`，两条路径不得交叉。
- **花费内部单位是 ticks，1 USD = 10^10 ticks，整数累加。** 只在展示的最后一步转美元。理由是浮点求和对不上服务端的账（grok 官方文档的原话）。
- **花费的缺席绝不显示成 `$0.00`。** 缺席意味着 "unreported or incomplete, never free"。
- **日志用各包已有的 structured logger（`a.log` / `m.log`），禁止 `fmt.Printf`。**
- **所有新文件写文件头注释（职责 + 边界），所有导出标识符写文档注释（参数、返回、空值语义）。**
- **每个 task 完成即 commit。**

---

## 文件结构

| 文件 | 职责 |
|---|---|
| `internal/proto/proto.go`（改） | `Cumulative` / `Cost` / `CostState` 类型；`Task.Cumulative` 字段 |
| `internal/store/store.go`（改） | `task_usage_ledger` 建表；`UpsertSpend`；`TaskCumulative`；`GetTask` 填充 |
| `internal/executor/executor.go`（改） | `AdapterEvent.Spend` 字段（类型本身在 `proto` 包） |
| `internal/agentd/manager.go`（改） | `handleSpend`：把 `ev.Spend` 落库 |
| `internal/executor/claudecode/spend.go`（新） | result 行 → `SpendEntry`；花费的进程内差分 |
| `internal/executor/codex/pricing.go`（新） | 模型 → 牌价表；按 token 估算 ticks |
| `internal/executor/codex/spend.go`（新） | `tokenUsage.total` 的回合级差分 → `SpendEntry` |
| `internal/executor/grok/spend.go`（新） | `session/prompt` 响应 `_meta` → `SpendEntry` |
| `internal/executor/opencode/spend.go`（新） | `message.updated` 的 `info` → `SpendEntry` |
| `web/src/api/types.ts`（改） | `Cumulative` / `Cost` 前端类型 |
| `web/src/app/lib/format.ts`（改） | `formatCumulativeLine` / `formatCost` |
| `web/src/app/task/TaskHeader.tsx`（改） | 切换按钮与两个视图 |

每家 executor 的解析放**独立的 `spend.go`**，与已有的 `usage.go`（当前占用）并列。分开是刻意的：两个口径的公式经常相反（缓存有的要加有的要减），放同一个文件里迟早有人复制错。

---

## Task 1: proto 类型与 store 的账本表

**Files:**
- Modify: `internal/proto/proto.go`（在 `Usage` 结构之后）
- Modify: `internal/store/store.go`（建表语句数组、新增两个方法、`GetTask`）
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: 无（本计划的第一个 task）
- Produces:
  - `proto.Cumulative{InputTokens, CachedTokens, OutputTokens, TotalTokens int; Cost *proto.Cost}`
  - `proto.Cost{Ticks int64; State proto.CostState}`
  - `proto.CostState` 常量：`CostReported` / `CostEstimated` / `CostPartial` / `CostUnknown`
  - `proto.SpendEntry{Key string; InputTokens, CachedTokens, OutputTokens int; CostTicks int64; CostState CostState}`
  - `(*Store).UpsertSpend(taskID string, e proto.SpendEntry) error`
  - `(*Store).TaskCumulative(taskID string) (*proto.Cumulative, error)`
  - `proto.Task.Cumulative *Cumulative`（json tag `cumulative,omitempty`）

- [ ] **Step 1: 写 proto 类型**

在 `internal/proto/proto.go` 的 `Usage` 结构**之后**追加：

```go
// CostState 是花费的可信度。
//
// 取值范围**分两级**：单条账目（SpendEntry / ledger 行）只可能是
// CostReported / CostEstimated / CostUnknown；CostPartial 只在**求和之后**
// 产生（部分行有花费、部分行没有），任何 adapter 都不会产出它。
// 别去找「哪个 adapter 报 partial」——没有。
type CostState string

const (
	// CostReported：执行器自报了花费且完整。
	CostReported CostState = "reported"
	// CostEstimated：执行器不报花费，由 handoff 按 API 牌价估算（只有 codex）。
	CostEstimated CostState = "estimated"
	// CostPartial：**仅聚合级**。有已知部分，但有调用没拿到花费——所以它是
	// **下界**，真实值只会更高。展示时必须能读出这一点。
	CostPartial CostState = "partial"
	// CostUnknown：一次都没拿到。展示成「—」，**绝不是 $0.00**：
	// 花费的缺席意味着 "unreported or incomplete, never free"。
	CostUnknown CostState = "unknown"
)

// Cost 是累计花费及其可信度。
//
// 注意：State 为 CostPartial 时，Ticks 只是**已知部分**的和，是下界不是总额。
type Cost struct {
	// Ticks 是花费，单位 1 USD = 10^10 ticks。
	//
	// 为什么用整数 ticks 而不是浮点美元：grok 原生就给 ticks，且它的文档明说
	// 浮点求和对不上服务端的账。统一整数累加，只在展示的最后一步转美元。
	Ticks int64 `json:"ticks"`
	// State 见 CostState 的注释。CostUnknown 时 Ticks 恒为 0。
	State CostState `json:"state"`
}

// Cumulative 是任务的累计消耗快照。
//
// 与 Usage 的区别（**改错了不会报错，只会显示错的数**）：Usage 描述
// 「现在占用多少 context」（最后一次模型调用的输入侧），本结构描述
// 「这个任务一共烧了多少」（跨全部调用累加）。两者数量级差几倍到几十倍，
// 不要因为字段名像就互相赋值。
//
// 边界：本结构由 Store.TaskCumulative 对 task_usage_ledger 求和产出，
// 只在**单任务读取**时填充；列表接口不填（见 Store.ListTasks 的注释）。
type Cumulative struct {
	// InputTokens 是未命中缓存的输入（口径见 Store.UpsertSpend 的注释）。
	InputTokens int `json:"input_tokens"`
	// CachedTokens 是命中缓存的输入（读缓存 + 写缓存）。
	CachedTokens int `json:"cached_tokens"`
	// OutputTokens 是模型产出，含 reasoning。
	OutputTokens int `json:"output_tokens"`
	// TotalTokens 是上面三项之和，由 store 算好，前端不再自己加。
	TotalTokens int `json:"total_tokens"`
	// Cost 是累计花费；nil = 还没有任何一条账目带花费信息。
	Cost *Cost `json:"cost,omitempty"`
}

// SpendEntry 是一条待入账的消耗（adapter 产出，store 消费）。
//
// Key 必须在同一个任务内**稳定且唯一**——它是幂等的全部依据。同 Key 重复上报
// 按**覆盖**处理（不是累加），所以流式增长的值可以放心重复报：
// opencode 对同一条 message 会随生成推很多次、id 相同而 tokens 在涨，
// 覆盖天然取到最终值；重复推同值则是无操作。
type SpendEntry struct {
	Key          string
	InputTokens  int
	CachedTokens int
	OutputTokens int
	CostTicks    int64
	// CostState 只能是 CostReported / CostEstimated / CostUnknown 三者之一。
	CostState CostState
}
```

在 `proto.Task` 结构里，紧挨 `Usage` 字段之后加：

```go
	// Cumulative 是任务的累计消耗；nil = 没有任何账目（或本次是列表读取，
	// 列表不填充——见 Store.ListTasks）。与 Usage 是两个口径，别混。
	Cumulative *Cumulative `json:"cumulative,omitempty"`
```

- [ ] **Step 2: 写失败的 store 测试**

在 `internal/store/store_test.go` 末尾追加：

```go
// TestUpsertSpendOverwritesByKey 验幂等的核心语义：同键覆盖、异键累加。
func TestUpsertSpendOverwritesByKey(t *testing.T) {
	s := newTestStore(t)
	task := seedTask(t, s)

	// 同一个 key 报两次，第二次值更大（opencode 流式增长的形态）
	must(t, s.UpsertSpend(task.ID, proto.SpendEntry{
		Key: "k1", InputTokens: 10, CachedTokens: 20, OutputTokens: 5,
		CostTicks: 100, CostState: proto.CostReported}))
	must(t, s.UpsertSpend(task.ID, proto.SpendEntry{
		Key: "k1", InputTokens: 30, CachedTokens: 40, OutputTokens: 7,
		CostTicks: 300, CostState: proto.CostReported}))
	// 另一个 key：累加
	must(t, s.UpsertSpend(task.ID, proto.SpendEntry{
		Key: "k2", InputTokens: 1, CachedTokens: 2, OutputTokens: 3,
		CostTicks: 50, CostState: proto.CostReported}))

	c, err := s.TaskCumulative(task.ID)
	if err != nil {
		t.Fatalf("TaskCumulative: %v", err)
	}
	if c.InputTokens != 31 || c.CachedTokens != 42 || c.OutputTokens != 10 {
		t.Fatalf("同键应覆盖、异键应累加，实得 in=%d cached=%d out=%d",
			c.InputTokens, c.CachedTokens, c.OutputTokens)
	}
	if c.TotalTokens != 83 {
		t.Fatalf("TotalTokens 应为三项之和 83，实得 %d", c.TotalTokens)
	}
	if c.Cost == nil || c.Cost.Ticks != 350 || c.Cost.State != proto.CostReported {
		t.Fatalf("花费应为 350/reported，实得 %+v", c.Cost)
	}
}

// TestTaskCumulativeCostStates 逐一验花费聚合的五种条件。
func TestTaskCumulativeCostStates(t *testing.T) {
	cases := []struct {
		name    string
		entries []proto.SpendEntry
		want    *proto.Cost
	}{
		{
			name:    "没有任何账目",
			entries: nil,
			want:    nil,
		},
		{
			name: "全部自报",
			entries: []proto.SpendEntry{
				{Key: "a", CostTicks: 100, CostState: proto.CostReported},
				{Key: "b", CostTicks: 200, CostState: proto.CostReported},
			},
			want: &proto.Cost{Ticks: 300, State: proto.CostReported},
		},
		{
			name: "含估算",
			entries: []proto.SpendEntry{
				{Key: "a", CostTicks: 100, CostState: proto.CostReported},
				{Key: "b", CostTicks: 200, CostState: proto.CostEstimated},
			},
			want: &proto.Cost{Ticks: 300, State: proto.CostEstimated},
		},
		{
			name: "有已知也有缺席——是下界",
			entries: []proto.SpendEntry{
				{Key: "a", CostTicks: 100, CostState: proto.CostReported},
				{Key: "b", CostTicks: 0, CostState: proto.CostUnknown},
			},
			want: &proto.Cost{Ticks: 100, State: proto.CostPartial},
		},
		{
			name: "全部缺席——绝不能是 $0.00",
			entries: []proto.SpendEntry{
				{Key: "a", CostTicks: 0, CostState: proto.CostUnknown},
				{Key: "b", CostTicks: 0, CostState: proto.CostUnknown},
			},
			want: &proto.Cost{Ticks: 0, State: proto.CostUnknown},
		},
		{
			name: "估算与缺席同时——按缺席（漏账比不准要紧）",
			entries: []proto.SpendEntry{
				{Key: "a", CostTicks: 100, CostState: proto.CostEstimated},
				{Key: "b", CostTicks: 0, CostState: proto.CostUnknown},
			},
			want: &proto.Cost{Ticks: 100, State: proto.CostPartial},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			task := seedTask(t, s)
			for _, e := range tc.entries {
				must(t, s.UpsertSpend(task.ID, e))
			}
			c, err := s.TaskCumulative(task.ID)
			if err != nil {
				t.Fatalf("TaskCumulative: %v", err)
			}
			if tc.entries == nil {
				if c != nil {
					t.Fatalf("没有账目时应返回 nil，实得 %+v", c)
				}
				return
			}
			if c.Cost == nil {
				t.Fatalf("期望 %+v，实得 Cost=nil", tc.want)
			}
			if *c.Cost != *tc.want {
				t.Fatalf("期望 %+v，实得 %+v", *tc.want, *c.Cost)
			}
		})
	}
}

// TestGetTaskFillsCumulativeListDoesNot 锁住「单读填、列表不填」这条契约。
func TestGetTaskFillsCumulativeListDoesNot(t *testing.T) {
	s := newTestStore(t)
	task := seedTask(t, s)
	must(t, s.UpsertSpend(task.ID, proto.SpendEntry{
		Key: "k", InputTokens: 7, CostTicks: 10, CostState: proto.CostReported}))

	got, err := s.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Cumulative == nil || got.Cumulative.InputTokens != 7 {
		t.Fatalf("GetTask 应填充累计，实得 %+v", got.Cumulative)
	}

	list, err := s.ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	for _, tk := range list {
		if tk.ID == task.ID && tk.Cumulative != nil {
			t.Fatalf("ListTasks 不应填充累计（避免每行一次 SUM），实得 %+v", tk.Cumulative)
		}
	}
}
```

**注意**：`newTestStore` / `seedTask` / `must` 若在 `store_test.go` 里不存在同名辅助函数，就照该文件已有的建库与建任务写法内联展开——**不要新造一套 helper 覆盖已有的**。先读 `store_test.go` 顶部现有的辅助函数再动手。

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/store/ -run 'Spend|Cumulative' -v`
Expected: 编译失败，`undefined: proto.SpendEntry` / `s.UpsertSpend undefined`

- [ ] **Step 4: 建表**

在 `internal/store/store.go` 的建表语句数组里，`CREATE TABLE IF NOT EXISTS tasks (...)` 之后追加一条：

```go
		`CREATE TABLE IF NOT EXISTS task_usage_ledger (
  -- B83 账本：一行 = 一次「新增消耗」的账目，累计值由对本表求和得到。
  -- 为什么不在 tasks 表上冗余累计列：冗余就有一致性问题（漏写一次永久偏差），
  -- 而行数是回合数量级（几十到几百），一次 SUM 的成本可以忽略。
  task_id TEXT NOT NULL,
  -- entry_key 是 adapter 给的幂等键（claudecode=result.uuid、codex=turnId、
  -- grok=promptId、opencode=message.id）。同键**覆盖**而非累加：流式推送的
  -- 同一条消息会推多次且值在涨，覆盖才拿到最终值。
  entry_key TEXT NOT NULL,
  input INTEGER NOT NULL DEFAULT 0,
  cached_input INTEGER NOT NULL DEFAULT 0,
  output INTEGER NOT NULL DEFAULT 0,
  -- cost_ticks 单位 1 USD = 10^10 ticks；cost_state 只可能是
  -- reported / estimated / unknown（partial 是聚合级状态，不落库）。
  cost_ticks INTEGER NOT NULL DEFAULT 0,
  cost_state TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMP NOT NULL,
  PRIMARY KEY (task_id, entry_key))`,
```

- [ ] **Step 5: 写 UpsertSpend 与 TaskCumulative**

在 `internal/store/store.go` 的 `SetTaskUsage` 之后追加：

```go
// UpsertSpend 记一条消耗账目；同 (taskID, e.Key) **覆盖**既有行。
//
// 参数：
//   - taskID: 所属任务
//   - e: 账目。三个 token 分项的口径是归一化后的值——**输入不含缓存**、
//     **缓存输入 = 读缓存 + 写缓存**、**输出含 reasoning**。四家 executor 的
//     原始字段含义互不相同（codex/grok 的输入含缓存要减，claudecode/opencode
//     的要加；opencode 的 reasoning 与 output 平行要加，codex/grok 的是子集
//     不能加），归一化在各 adapter 的 spend.go 里完成，本方法不做换算。
//
// 注意：
//   - e.Key 为空时直接返回错误——没有键就没有幂等，宁可报错也不写一行永远
//     去不掉重的账
//   - 覆盖而非累加是刻意的，理由见 proto.SpendEntry 的注释
func (s *Store) UpsertSpend(taskID string, e proto.SpendEntry) error {
	if e.Key == "" {
		return fmt.Errorf("记任务 %s 的消耗：幂等键为空", taskID)
	}
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO task_usage_ledger
   (task_id, entry_key, input, cached_input, output, cost_ticks, cost_state, updated_at)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
 ON CONFLICT(task_id, entry_key) DO UPDATE SET
   input = excluded.input, cached_input = excluded.cached_input,
   output = excluded.output, cost_ticks = excluded.cost_ticks,
   cost_state = excluded.cost_state, updated_at = excluded.updated_at`,
		taskID, e.Key, e.InputTokens, e.CachedTokens, e.OutputTokens,
		e.CostTicks, string(e.CostState), fmtTime(time.Now())); err != nil {
		return fmt.Errorf("记任务 %s 消耗 %s: %w", taskID, e.Key, err)
	}
	return nil
}

// TaskCumulative 对该任务的全部账目求和，得到累计消耗。
//
// 返回：
//   - 没有任何账目行时返回 (nil, nil)。**不返回零值结构**——0 会被读成
//     「一共花了 0」，而真相是「还不知道」
//   - 花费状态按四条规则定（known=非 unknown 行的 ticks 之和，
//     missing=unknown 行的条数，est=是否含 estimated 行）：
//     missing==0 && !est → reported；missing==0 && est → estimated；
//     missing>0 && known>0 → partial（**下界**）；missing>0 && known==0 → unknown
//
// 注意：estimated 与 missing 同时成立时按 partial——漏账比不准要紧，
// 而 partial 的展示（下界）也已经隐含了「别当真」。
func (s *Store) TaskCumulative(taskID string) (*proto.Cumulative, error) {
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT input, cached_input, output, cost_ticks, cost_state
   FROM task_usage_ledger WHERE task_id = ?`, taskID)
	if err != nil {
		return nil, fmt.Errorf("读任务 %s 消耗账本: %w", taskID, err)
	}
	defer rows.Close()

	var (
		c       proto.Cumulative
		known   int64
		missing int
		est     bool
		n       int
	)
	for rows.Next() {
		var in, cached, out int
		var ticks int64
		var state string
		if err := rows.Scan(&in, &cached, &out, &ticks, &state); err != nil {
			return nil, fmt.Errorf("扫描任务 %s 的消耗行: %w", taskID, err)
		}
		n++
		c.InputTokens += in
		c.CachedTokens += cached
		c.OutputTokens += out
		switch proto.CostState(state) {
		case proto.CostUnknown:
			missing++
		case proto.CostEstimated:
			est = true
			known += ticks
		default:
			known += ticks
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历任务 %s 的消耗账本: %w", taskID, err)
	}
	if n == 0 {
		return nil, nil
	}
	c.TotalTokens = c.InputTokens + c.CachedTokens + c.OutputTokens
	c.Cost = &proto.Cost{Ticks: known}
	switch {
	case missing == 0 && !est:
		c.Cost.State = proto.CostReported
	case missing == 0:
		c.Cost.State = proto.CostEstimated
	case known > 0:
		c.Cost.State = proto.CostPartial
	default:
		c.Cost.State = proto.CostUnknown
	}
	return &c, nil
}
```

- [ ] **Step 6: GetTask 填充，ListTasks 显式不填**

在 `GetTask` 里 `scanTaskRow` 成功之后、`return` 之前插入：

```go
	// 累计消耗来自另一张表，单读时一并带上；列表刻意不带（见下方注释）。
	cum, err := s.TaskCumulative(id)
	if err != nil {
		return nil, err
	}
	task.Cumulative = cum
```

并在 `ListTasks` 的方法注释里补一行：

```go
// 注意：**不填充 Task.Cumulative**。列表页不显示累计消耗，为每一行做一次
// SUM 是纯浪费；要拿累计值请用 GetTask。这不是 bug，改之前先想清楚代价。
```

- [ ] **Step 7: 跑测试确认通过**

Run: `go test ./internal/store/ ./internal/proto/ -v`
Expected: PASS（含新加的三个测试）

- [ ] **Step 8: 加关键节点日志**

- `UpsertSpend`：**不打成功日志**（频率与 assistant 消息同级，会刷屏）；错误由调用方 `handleSpend` 打（见 Task 2）。
- `TaskCumulative`：只在 `rows.Scan` / `rows.Err` 出错时由调用方打；本方法自己不打（读路径高频，成功不打）。

这一步的产出是**确认不该加的地方没加**，以及在两个方法的注释里各写一行说明为什么这里刻意不打日志。

- [ ] **Step 9: 加注释**

- `proto.CostState` / `Cost` / `Cumulative` / `SpendEntry`：Step 1 已含。
- `UpsertSpend` / `TaskCumulative`：Step 5 已含。
- 建表语句里的 `-- B83 账本` 注释块：Step 4 已含。
- 确认 `ListTasks` 的「不填充」注释已加（Step 6）。

- [ ] **Step 10: Commit**

```bash
git add internal/proto/proto.go internal/store/store.go internal/store/store_test.go
git commit -m "feat(store): B83 消耗账本表与累计求和，同键覆盖保证幂等"
```

---

## Task 2: adapter 契约与 manager 接线

**Files:**
- Modify: `internal/executor/executor.go`（`AdapterEvent` 结构，`Usage` 字段之后）
- Modify: `internal/agentd/manager.go`（`handleEvent` 的 usage 分支附近；`handleUsage` 之后）
- Test: `internal/agentd/manager_test.go`

**Interfaces:**
- Consumes: `proto.SpendEntry`、`(*Store).UpsertSpend`（Task 1）
- Produces: `executor.AdapterEvent.Spend *proto.SpendEntry`；`(*Manager).handleSpend(taskID string, ev executor.AdapterEvent)`

- [ ] **Step 1: 加 AdapterEvent 字段**

在 `internal/executor/executor.go` 的 `AdapterEvent` 结构里，`Usage *proto.Usage` 之后追加：

```go
	// Spend 是这一次调用/回合**新增**的消耗；nil = 本帧不带消耗信息。
	//
	// 与 Usage 的区别：Usage 是「当前占用」的快照（后到的覆盖先到的），
	// Spend 是「新增消耗」的账目（按 Key 覆盖后**求和**）。数量级完全不同，
	// 一个帧可以两者都带，但**绝不能互相赋值**。
	Spend *proto.SpendEntry
```

- [ ] **Step 2: 写失败的 manager 测试**

在 `internal/agentd/manager_test.go` 追加：

```go
// TestHandleSpendWritesLedger 验 Spend 事件落进账本，且不碰当前占用三元组。
func TestHandleSpendWritesLedger(t *testing.T) {
	m, task := newTestManagerWithTask(t)

	// 先落一次当前占用（B80 通道）
	m.handleUsage(task.ID, executor.AdapterEvent{
		Type: "usage", ActualModel: "m1",
		Usage: &proto.Usage{ContextTokens: 999},
	})
	// 再落一条累计账目（B83 通道）
	m.handleSpend(task.ID, executor.AdapterEvent{
		Type: "usage",
		Spend: &proto.SpendEntry{Key: "k1", InputTokens: 10, CachedTokens: 20,
			OutputTokens: 5, CostTicks: 100, CostState: proto.CostReported},
	})

	got, err := m.st.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	// 累计写进去了
	if got.Cumulative == nil || got.Cumulative.InputTokens != 10 {
		t.Fatalf("累计应落库，实得 %+v", got.Cumulative)
	}
	// 当前占用没被累计通道冲掉——这是两条通道必须互不干扰的证据
	if got.ActualModel != "m1" || got.Usage == nil || got.Usage.ContextTokens != 999 {
		t.Fatalf("累计通道不得影响当前占用，实得 model=%q usage=%+v",
			got.ActualModel, got.Usage)
	}
}

// TestHandleSpendNilIsNoop 验没有 Spend 的帧不写库、不报错。
func TestHandleSpendNilIsNoop(t *testing.T) {
	m, task := newTestManagerWithTask(t)
	m.handleSpend(task.ID, executor.AdapterEvent{Type: "usage"})
	got, err := m.st.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Cumulative != nil {
		t.Fatalf("空 Spend 不应产生账目，实得 %+v", got.Cumulative)
	}
}
```

**注意**：`newTestManagerWithTask` 若不存在，照 `manager_test.go` 里已有的建 Manager + 建任务写法内联展开，**不要新造 helper**。先读该文件顶部。

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'HandleSpend' -v`
Expected: 编译失败，`m.handleSpend undefined`

- [ ] **Step 4: 写 handleSpend 并接线**

在 `internal/agentd/manager.go` 的 `handleUsage` 之后追加：

```go
// handleSpend 把 executor 报回的「本次新增消耗」记进账本。
//
// 与 handleUsage 的区别（**两条通道必须互不干扰**）：
//   - handleUsage 走 SetTaskUsage，**整体覆盖** (model, tokens, window) 三元组，
//     描述「当前占用」；
//   - 本方法走 UpsertSpend，按幂等键**覆盖单行**后求和，描述「累计消耗」。
//
// 拿任何一条的值去写另一条，都会产生一个不报错、只是数字错的故障。
//
// 与 handleUsage 相同的两点：**只写库，不追加事件行、不广播**（频率同样高，
// 进事件日志会淹没审核者真正要看的 permission/question/completed）；
// 落库失败仅 Warn（用量属可修复的辅助字段，不影响主流程）。
//
// 幂等不做内存去重：内存态在 agentd 重启后为空，首帧必写一次——对「当前占用」
// 无害（覆盖成同值），对「累计」就是重复计数。所以幂等只能落在库里。
func (m *Manager) handleSpend(taskID string, ev executor.AdapterEvent) {
	if ev.Spend == nil {
		return
	}
	if err := m.st.UpsertSpend(taskID, *ev.Spend); err != nil {
		m.log.Warn("记任务消耗失败", "task", taskID, "key", ev.Spend.Key, "cause", err)
		return
	}
	m.log.Debug("任务消耗已入账", "task", taskID, "key", ev.Spend.Key,
		"input", ev.Spend.InputTokens, "cached", ev.Spend.CachedTokens,
		"output", ev.Spend.OutputTokens, "cost_state", ev.Spend.CostState)
}
```

在 `handleEvent` 里调用 `m.handleUsage(taskID, ev)` 的那一行**之后**紧接着加：

```go
		m.handleSpend(taskID, ev)
```

（同一个 `usage` 类型的分支里，两条通道各走各的。）

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ ./internal/executor/ -v`
Expected: PASS

- [ ] **Step 6: 加关键节点日志**

Step 4 已含：入账成功打 Debug（高频，不用 Info），失败打 Warn 且带 `task` / `key` / `cause`。确认**没有**在这里追加事件行或广播——那是这条通道明确不做的事。

- [ ] **Step 7: 加注释**

Step 1 与 Step 4 已含。额外确认 `handleEvent` 里新加的那一行上方有一句说明「两条通道各走各的」。

- [ ] **Step 8: Commit**

```bash
git add internal/executor/executor.go internal/agentd/manager.go internal/agentd/manager_test.go
git commit -m "feat(agentd): B83 消耗事件通道，与当前占用通道完全分离"
```

---

## Task 3: claudecode 的账目

**Files:**
- Create: `internal/executor/claudecode/spend.go`
- Create: `internal/executor/claudecode/spend_test.go`
- Modify: `internal/executor/claudecode/stream.go`（`streamMsg` 加两个字段）
- Modify: `internal/executor/claudecode/adapter.go`（`runState` 加一个字段；`mapResult` 接线）

**Interfaces:**
- Consumes: `proto.SpendEntry`、`executor.AdapterEvent.Spend`（Task 1、2）
- Produces: `parseResultSpend(m streamMsg, prevCostUSD float64) (proto.SpendEntry, float64, bool)`

**背景（实抓，不要按字段名猜）**：claudecode 的 `result` 行同时给两个口径——
`usage.*` 是**本轮**，`modelUsage.*` 是**进程累计**。本任务要的是本轮，取 `usage.*`。
花费只有 `total_cost_usd`，且它是**进程内累计**，所以本轮花费 = 本次 − 上次。
`result` 行带 `uuid`，就是幂等键。

三轮实测（`usage` 列是本轮，`total_cost_usd` 列是进程累计）：

| 轮 | `usage.input_tokens` | `usage.cache_read_input_tokens` | `usage.output_tokens` | `total_cost_usd` |
|---|---|---|---|---|
| 1 | 2776 | 29952 | 26 | 0.029506 |
| 2 | 265 | 32512 | 19 | 0.047562 |
| 3 | 54 | 32768 | 18 | 0.064666 |

- [ ] **Step 1: 写失败的测试**

创建 `internal/executor/claudecode/spend_test.go`：

```go
package claudecode

import (
	"encoding/json"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

// TestParseResultSpendPerTurn 用三轮实抓值验：token 取本轮、花费取进程内差分。
func TestParseResultSpendPerTurn(t *testing.T) {
	rounds := []struct {
		uuid      string
		in, rd, o int
		totalCost float64
		wantTicks int64
	}{
		{"u1", 2776, 29952, 26, 0.029506, 295060000},
		{"u2", 265, 32512, 19, 0.047562, 180560000},
		{"u3", 54, 32768, 18, 0.064666, 171040000},
	}
	prev := 0.0
	for _, r := range rounds {
		m := streamMsg{UUID: r.uuid, TotalCostUSD: r.totalCost}
		m.Usage = json.RawMessage(`{"input_tokens":` + itoa(r.in) +
			`,"cache_read_input_tokens":` + itoa(r.rd) +
			`,"cache_creation_input_tokens":0,"output_tokens":` + itoa(r.o) + `}`)

		e, next, ok := parseResultSpend(m, prev)
		if !ok {
			t.Fatalf("轮 %s 应解析成功", r.uuid)
		}
		if e.Key != r.uuid {
			t.Fatalf("幂等键应是 result.uuid，实得 %q", e.Key)
		}
		// claudecode 的 input_tokens **不含**缓存，所以输入就是它本身
		if e.InputTokens != r.in {
			t.Fatalf("轮 %s 输入应为 %d，实得 %d", r.uuid, r.in, e.InputTokens)
		}
		if e.CachedTokens != r.rd {
			t.Fatalf("轮 %s 缓存输入应为 %d，实得 %d", r.uuid, r.rd, e.CachedTokens)
		}
		if e.OutputTokens != r.o {
			t.Fatalf("轮 %s 输出应为 %d，实得 %d", r.uuid, r.o, e.OutputTokens)
		}
		if e.CostTicks != r.wantTicks {
			t.Fatalf("轮 %s 花费应为差分 %d ticks，实得 %d", r.uuid, r.wantTicks, e.CostTicks)
		}
		if e.CostState != proto.CostReported {
			t.Fatalf("claudecode 自报花费，应为 reported，实得 %q", e.CostState)
		}
		prev = next
	}
}

// TestParseResultSpendNegativeDelta 验基线陈旧时取当前值，不写负数。
func TestParseResultSpendNegativeDelta(t *testing.T) {
	m := streamMsg{UUID: "u1", TotalCostUSD: 0.01}
	m.Usage = json.RawMessage(`{"input_tokens":1,"output_tokens":1}`)
	e, _, ok := parseResultSpend(m, 0.5) // 上次比这次还大
	if !ok {
		t.Fatal("应解析成功")
	}
	if e.CostTicks != 100000000 {
		t.Fatalf("负差分应退回当前值 100000000 ticks，实得 %d", e.CostTicks)
	}
}

// TestParseResultSpendNoUUID 验没有幂等键就不出账目。
func TestParseResultSpendNoUUID(t *testing.T) {
	m := streamMsg{TotalCostUSD: 0.01}
	m.Usage = json.RawMessage(`{"input_tokens":1}`)
	if _, _, ok := parseResultSpend(m, 0); ok {
		t.Fatal("没有 uuid 时不应产出账目——没有键就没有幂等")
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/claudecode/ -run 'ResultSpend' -v`
Expected: 编译失败，`m.UUID undefined` / `parseResultSpend undefined`

- [ ] **Step 3: streamMsg 加字段**

在 `internal/executor/claudecode/stream.go` 的 `streamMsg` 结构里追加三个字段（与已有的 `ModelUsage` 并列）：

```go
	UUID         string          `json:"uuid"`           // result 行的唯一标识（B83：账目的幂等键）
	TotalCostUSD float64         `json:"total_cost_usd"` // **进程内累计**花费，本轮花费要做差分
	Usage        json.RawMessage `json:"usage"`          // result 行的**本轮**用量（与 modelUsage 的进程累计不是一回事）
```

- [ ] **Step 4: 写 spend.go**

创建 `internal/executor/claudecode/spend.go`：

```go
// spend.go —— claudecode 的**累计消耗**账目解析。
//
// 职责：把 result 行解析成一条 proto.SpendEntry（本轮新增的 token 与花费）。
// 边界：纯函数，不碰 runState、不发事件、不写日志——接线在 adapter.go。
//
// 与同目录 usage.go 的关系：usage.go 解「当前 context 占用」（assistant 消息，
// 最后一次调用的输入侧），本文件解「一共烧了多少」（result 行，逐轮累加）。
// 两个口径的公式不同且都容易写错，刻意分文件，不要合并。
package claudecode

import (
	"encoding/json"
	"math"

	"github.com/xushixin/handoff/internal/proto"
)

// usdToTicks 把浮点美元换成整数 ticks（1 USD = 10^10）。
//
// 为什么统一到整数：花费要跨回合求和，浮点累加的误差对不上服务端的账
// （grok 官方文档的原话）。转换在**入账时**做一次，之后全程整数。
func usdToTicks(usd float64) int64 {
	if usd <= 0 {
		return 0
	}
	return int64(math.Round(usd * 1e10))
}

// parseResultSpend 从 result 行取本轮新增的消耗。
//
// 参数：
//   - m: result 行
//   - prevCostUSD: **同一个进程内**上一次 result 行的 total_cost_usd（首个回合传 0）
//
// 返回：
//   - e: 账目；Key 取 result 行的 uuid
//   - nextPrev: 本次的 total_cost_usd，调用方存回 runState 作下次的基线
//   - ok: uuid 非空且 usage 可解析时为 true
//
// 两条 claudecode 独有的规则（**改错了不会报错，只会显示错的数**）：
//   - **取 `usage.*` 不取 `modelUsage.*`**：前者是本轮，后者是**进程累计**。
//     进程累计在 --resume 后会归零（实测前一进程收尾 in=3095，新进程首轮 in=98），
//     所以它既不是会话累计也不能直接用。
//   - **`input_tokens` 不含缓存，缓存两项要相加**——与 codex/grok 相反。
//     实测轮 3 的 input_tokens 只有 54 而 cache_read 有 32768。
//
// 花费只有进程内累计值 `total_cost_usd`，所以本轮花费 = 本次 − 上次。
// runState 天生是进程级的，新进程基线自然从 0 起，首个回合的差分就是它自己——正确。
// 差分为负说明基线陈旧（不该发生），退回取当前值本身，绝不写负数进账本。
func parseResultSpend(m streamMsg, prevCostUSD float64) (proto.SpendEntry, float64, bool) {
	if m.UUID == "" {
		return proto.SpendEntry{}, prevCostUSD, false // 没有键就没有幂等，宁可不记
	}
	var u struct {
		InputTokens         int `json:"input_tokens"`
		CacheReadTokens     int `json:"cache_read_input_tokens"`
		CacheCreationTokens int `json:"cache_creation_input_tokens"`
		OutputTokens        int `json:"output_tokens"`
	}
	if len(m.Usage) == 0 {
		return proto.SpendEntry{}, prevCostUSD, false
	}
	if err := json.Unmarshal(m.Usage, &u); err != nil {
		return proto.SpendEntry{}, prevCostUSD, false
	}

	delta := m.TotalCostUSD - prevCostUSD
	if delta < 0 {
		delta = m.TotalCostUSD // 基线陈旧：退回当前值（调用方会打 Warn）
	}
	return proto.SpendEntry{
		Key:          m.UUID,
		InputTokens:  u.InputTokens,
		CachedTokens: u.CacheReadTokens + u.CacheCreationTokens,
		OutputTokens: u.OutputTokens,
		CostTicks:    usdToTicks(delta),
		CostState:    proto.CostReported,
	}, m.TotalCostUSD, true
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/executor/claudecode/ -v`
Expected: PASS

- [ ] **Step 6: 接线到 adapter**

在 `internal/executor/claudecode/adapter.go` 的 `runState` 结构里，`ctxWindow` 附近追加：

```go
	// prevCostUSD 是**本进程内**上一次 result 行的 total_cost_usd，用于把
	// 进程累计花费差分成本轮花费（见 parseResultSpend）。0 = 本进程还没有回合。
	prevCostUSD float64
```

在 `mapResult` 的开头（取窗口的那段**之后**、判 `m.Subtype` 之前）加：

```go
	// 累计消耗：result 行同时带本轮 token 与进程累计花费，差分成本轮账目。
	// 与上面的窗口是两回事——窗口进 Usage（当前占用），这里进 Spend（累计消耗）。
	if e, next, ok := parseResultSpend(m, r.prevCostUSD); ok {
		if m.TotalCostUSD < r.prevCostUSD {
			a.log.Warn("claude 花费基线陈旧，本轮按当前值入账",
				"task", r.taskID, "prev", r.prevCostUSD, "now", m.TotalCostUSD)
		}
		r.prevCostUSD = next
		a.emit(r, executor.AdapterEvent{Type: "usage", Spend: &e})
	}
```

- [ ] **Step 7: 跑全包测试**

Run: `go test ./internal/executor/claudecode/ ./internal/agentd/ -v`
Expected: PASS

- [ ] **Step 8: 加关键节点日志**

- 负差分：Step 6 已含 Warn，带 `task` / `prev` / `now`。
- 入账本身**不在 adapter 打**（`handleSpend` 已经打了 Debug，重复打是刷屏）。
- `parseResultSpend` 返回 `ok=false` 时打一行 Debug：

```go
	} else {
		a.log.Debug("claude result 行不带可入账的消耗，跳过", "task", r.taskID, "uuid", m.UUID)
	}
```

- [ ] **Step 9: 加注释**

`spend.go` 文件头、`usdToTicks`、`parseResultSpend`：Step 4 已含。
`runState.prevCostUSD`、`mapResult` 里新增块的「与窗口是两回事」：Step 6 已含。

- [ ] **Step 10: Commit**

```bash
git add internal/executor/claudecode/
git commit -m "feat(claudecode): B83 从 result 行记本轮消耗，花费按进程内差分"
```

---

## Task 4: codex 的账目与牌价表

**Files:**
- Create: `internal/executor/codex/pricing.go`
- Create: `internal/executor/codex/pricing_test.go`
- Create: `internal/executor/codex/spend.go`
- Create: `internal/executor/codex/spend_test.go`
- Modify: `internal/executor/codex/adapter.go`（`runState` 加两个字段；`ntfTokenUsage` 与 `ntfTurnCompleted` 两处接线）

**Interfaces:**
- Consumes: `proto.SpendEntry`（Task 1）、`usdToTicks` 的同名逻辑（本包自己实现一份，不跨包引用 claudecode 的私有函数）
- Produces:
  - `estimateTicks(model string, input, cached, output int) (int64, proto.CostState)`
  - `parseTurnSpend(params json.RawMessage, base spendBase) (proto.SpendEntry, spendBase, bool)`

**背景**：codex 是四家里**唯一花费一个字都不报**的，所以牌价估算表只服务它一家。
它的 `thread/tokenUsage/updated` 带 `total`（thread 累计）与 `last`（本次调用），
`params` 里有 `turnId`。回合级账目 = `total` 相对上一个回合结束时的差分，
key 取 `turnId`——一个回合内这条通知会来多次，但同键覆盖，最后一次即最终值。

实抓报文：

```json
{"method":"thread/tokenUsage/updated","params":{
  "threadId":"019ffb3d-…","turnId":"019ffb3d-…",
  "tokenUsage":{
    "total":{"totalTokens":24673,"inputTokens":24668,"cachedInputTokens":9984,
             "outputTokens":5,"reasoningOutputTokens":0},
    "last":{"…同结构…"},
    "modelContextWindow":258400}}}
```

- [ ] **Step 1: 写牌价表测试**

创建 `internal/executor/codex/pricing_test.go`：

```go
package codex

import (
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

// TestEstimateTicksKnownModel 用 gpt-5.6-sol 的牌价验算术。
// 牌价：input $5.00 / cached $0.50 / output $30.00 每百万 token。
// 1M 输入 + 1M 缓存 + 1M 输出 = 5 + 0.5 + 30 = $35.50 = 355000000000 ticks
func TestEstimateTicksKnownModel(t *testing.T) {
	ticks, state := estimateTicks("gpt-5.6-sol", 1_000_000, 1_000_000, 1_000_000)
	if state != proto.CostEstimated {
		t.Fatalf("表里有的模型应为 estimated，实得 %q", state)
	}
	if ticks != 355_000_000_000 {
		t.Fatalf("期望 355000000000 ticks（$35.50），实得 %d", ticks)
	}
}

// TestEstimateTicksUnknownModel 验不在表里的模型是 unknown，不是用默认价猜。
func TestEstimateTicksUnknownModel(t *testing.T) {
	ticks, state := estimateTicks("gpt-5-codex", 1_000_000, 0, 1_000_000)
	if state != proto.CostUnknown {
		t.Fatalf("表里没有的模型应为 unknown，实得 %q", state)
	}
	if ticks != 0 {
		t.Fatalf("unknown 时 ticks 必须为 0（不是猜一个数），实得 %d", ticks)
	}
}

// TestEstimateTicksEmptyModel 验模型名还没拿到时也是 unknown。
func TestEstimateTicksEmptyModel(t *testing.T) {
	if _, state := estimateTicks("", 100, 0, 100); state != proto.CostUnknown {
		t.Fatalf("空模型名应为 unknown，实得 %q", state)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/codex/ -run 'Estimate' -v`
Expected: 编译失败，`estimateTicks undefined`

- [ ] **Step 3: 写 pricing.go**

创建 `internal/executor/codex/pricing.go`：

```go
// pricing.go —— codex 的 API 牌价表与花费估算。
//
// 职责：把 token 数按公开牌价折成花费（ticks）。
// 边界：只服务 codex——四家 executor 里只有它一个字都不报花费，
// 其余三家自报，绝不拿这张表去覆盖它们的自报值。
//
// 纪律：**表里没有的模型一律 CostUnknown，不猜、不用默认价、不拿邻近型号顶。**
// 猜错是静默错误——数字照常显示，只是错的。缺失只是不显示，不会撒谎。
//
// 这张表算出来的是「等价 API 成本」，不一定是账单：codex 走 ChatGPT 订阅时
// 不按 token 另行计费。协议里看不出走的是哪条计费路径，所以本实现不区分，
// 也不隐藏这个数——它作为「烧了多少资源」的量度仍然成立，
// `CostEstimated` 的「估算」标记已经在提示别把它当账单。
package codex

import (
	"math"

	"github.com/xushixin/handoff/internal/proto"
)

// modelPrice 是单个模型的三档单价，单位：美元 / 百万 token。
type modelPrice struct {
	Input       float64 // 未命中缓存的输入
	CachedInput float64 // 命中缓存的输入
	Output      float64 // 产出（含 reasoning）
}

// modelPrices 是 OpenAI 公开 API 牌价。
//
// 数据来源：developers.openai.com/api/docs/pricing，**取价日期 2026-08-13**。
// 价格会变，表里的值只对写下它的那天负责；过期的后果是数字偏差，
// 而缺失的后果只是不显示——两种失效模式都不撒谎，所以宁缺毋滥。
//
// 刻意不收的两类：
//   - `-pro` 系列：官方页对它们的 cached input 是「—」（不适用），
//     三档缺一档就估不准；
//   - `gpt-5-codex` / `gpt-5.1-codex` / `gpt-5.2-codex`：官方 API 定价页当天
//     只列了 gpt-5.3-codex 一个 codex 型号，其余没有可引的公开单价。
var modelPrices = map[string]modelPrice{
	"gpt-5.6-sol":   {Input: 5.00, CachedInput: 0.50, Output: 30.00},
	"gpt-5.6-terra": {Input: 2.00, CachedInput: 0.20, Output: 12.00},
	"gpt-5.6-luna":  {Input: 0.20, CachedInput: 0.02, Output: 1.20},
	"gpt-5.5":       {Input: 5.00, CachedInput: 0.50, Output: 30.00},
	"gpt-5.4":       {Input: 2.50, CachedInput: 0.25, Output: 15.00},
	"gpt-5.4-mini":  {Input: 0.75, CachedInput: 0.075, Output: 4.50},
	"gpt-5.4-nano":  {Input: 0.20, CachedInput: 0.02, Output: 1.25},
	"gpt-5.3-codex": {Input: 1.75, CachedInput: 0.175, Output: 14.00},
	"gpt-5.2":       {Input: 1.75, CachedInput: 0.175, Output: 14.00},
	"gpt-5.1":       {Input: 1.25, CachedInput: 0.125, Output: 10.00},
	"gpt-5":         {Input: 1.25, CachedInput: 0.125, Output: 10.00},
	"gpt-5-mini":    {Input: 0.25, CachedInput: 0.025, Output: 2.00},
	"gpt-5-nano":    {Input: 0.05, CachedInput: 0.005, Output: 0.40},
}

// estimateTicks 按牌价估算这些 token 的花费。
//
// 参数：
//   - model: 实际模型名（空串或表里没有 → CostUnknown）
//   - input: 未命中缓存的输入；cached: 命中缓存的输入；output: 产出
//
// 返回：
//   - ticks: 花费（1 USD = 10^10 ticks）；CostUnknown 时恒为 0
//   - state: CostEstimated（表里有）或 CostUnknown（表里没有）
//
// 注意：先按美元算完再一次性转 ticks，不要三档各自取整——三次整除的误差会累积。
func estimateTicks(model string, input, cached, output int) (int64, proto.CostState) {
	p, ok := modelPrices[model]
	if !ok {
		return 0, proto.CostUnknown
	}
	usd := (float64(input)*p.Input + float64(cached)*p.CachedInput +
		float64(output)*p.Output) / 1e6
	if usd <= 0 {
		return 0, proto.CostEstimated // 量为 0 时花费确实是 0，这不是「不知道」
	}
	return int64(math.Round(usd * 1e10)), proto.CostEstimated
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/codex/ -run 'Estimate' -v`
Expected: PASS

- [ ] **Step 5: 写账目测试**

创建 `internal/executor/codex/spend_test.go`：

```go
package codex

import (
	"encoding/json"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

const tokenUsageFrame = `{"threadId":"t1","turnId":"turn-1","tokenUsage":{
  "total":{"totalTokens":24673,"inputTokens":24668,"cachedInputTokens":9984,
           "outputTokens":5,"reasoningOutputTokens":0},
  "last":{"totalTokens":24673,"inputTokens":24668,"cachedInputTokens":9984,
          "outputTokens":5,"reasoningOutputTokens":0},
  "modelContextWindow":258400}}`

// TestParseTurnSpendSubtractsCache 验 codex 的输入要**减**缓存（与 claudecode 相反）。
func TestParseTurnSpendSubtractsCache(t *testing.T) {
	e, _, ok := parseTurnSpend(json.RawMessage(tokenUsageFrame), spendBase{Model: "gpt-5.6-sol"})
	if !ok {
		t.Fatal("应解析成功")
	}
	if e.Key != "turn-1" {
		t.Fatalf("幂等键应是 turnId，实得 %q", e.Key)
	}
	// 24668 − 9984 = 14684：cachedInputTokens 是 inputTokens 的**子集**
	if e.InputTokens != 14684 {
		t.Fatalf("输入应为 24668−9984=14684，实得 %d", e.InputTokens)
	}
	if e.CachedTokens != 9984 {
		t.Fatalf("缓存输入应为 9984，实得 %d", e.CachedTokens)
	}
	// reasoningOutputTokens 是 outputTokens 的子集，不再相加
	if e.OutputTokens != 5 {
		t.Fatalf("输出应为 5（reasoning 已含在内），实得 %d", e.OutputTokens)
	}
	if e.CostState != proto.CostEstimated {
		t.Fatalf("codex 不自报花费，应为 estimated，实得 %q", e.CostState)
	}
}

// TestParseTurnSpendDeltaAcrossTurns 验回合级差分：第二个回合只记增量。
func TestParseTurnSpendDeltaAcrossTurns(t *testing.T) {
	base := spendBase{Model: "gpt-5.6-sol"}
	_, base, _ = parseTurnSpend(json.RawMessage(tokenUsageFrame), base)
	// 回合边界：调用方把 base 推进（模拟 turn/completed）
	base = base.commit()

	second := `{"turnId":"turn-2","tokenUsage":{"total":{"totalTokens":30000,
	  "inputTokens":29000,"cachedInputTokens":12000,"outputTokens":1000,
	  "reasoningOutputTokens":0}}}`
	e, _, ok := parseTurnSpend(json.RawMessage(second), base)
	if !ok {
		t.Fatal("应解析成功")
	}
	// 输入增量 = (29000−12000) − (24668−9984) = 17000 − 14684 = 2316
	if e.InputTokens != 2316 {
		t.Fatalf("第二回合输入增量应为 2316，实得 %d", e.InputTokens)
	}
	if e.CachedTokens != 2016 { // 12000 − 9984
		t.Fatalf("第二回合缓存增量应为 2016，实得 %d", e.CachedTokens)
	}
	if e.OutputTokens != 995 { // 1000 − 5
		t.Fatalf("第二回合输出增量应为 995，实得 %d", e.OutputTokens)
	}
}

// TestParseTurnSpendResetGoesPositive 验 total 归零（resume）时不产生负增量。
func TestParseTurnSpendResetGoesPositive(t *testing.T) {
	base := spendBase{Model: "gpt-5.6-sol", Input: 99999, Cached: 99999, Output: 99999}
	e, _, ok := parseTurnSpend(json.RawMessage(tokenUsageFrame), base)
	if !ok {
		t.Fatal("应解析成功")
	}
	if e.InputTokens < 0 || e.CachedTokens < 0 || e.OutputTokens < 0 {
		t.Fatalf("基线大于当前值时不得产生负增量，实得 %+v", e)
	}
	if e.InputTokens != 14684 {
		t.Fatalf("归零后应按当前值全量入账 14684，实得 %d", e.InputTokens)
	}
}

// TestParseTurnSpendNoTurnID 验没有幂等键就不出账目。
func TestParseTurnSpendNoTurnID(t *testing.T) {
	if _, _, ok := parseTurnSpend(json.RawMessage(`{"tokenUsage":{"total":{"inputTokens":5}}}`),
		spendBase{Model: "gpt-5"}); ok {
		t.Fatal("没有 turnId 时不应产出账目")
	}
}
```

- [ ] **Step 6: 跑测试确认失败**

Run: `go test ./internal/executor/codex/ -run 'TurnSpend' -v`
Expected: 编译失败，`parseTurnSpend undefined` / `spendBase undefined`

- [ ] **Step 7: 写 spend.go**

创建 `internal/executor/codex/spend.go`：

```go
// spend.go —— codex 的**累计消耗**账目解析。
//
// 职责：把 thread/tokenUsage/updated 的 total 差分成「本回合新增」的账目。
// 边界：纯函数 + 一个值类型的基线，不碰 runState、不发事件、不写日志——
// 接线在 adapter.go。
//
// 与同目录 usage.go 的关系：usage.go 取 `last.inputTokens` 解「当前 context 占用」，
// 本文件取 `total` 的差分解「一共烧了多少」。**同一帧、两个口径、不同字段**，
// 是本仓库最容易混的一处，刻意分文件。
package codex

import (
	"encoding/json"

	"github.com/xushixin/handoff/internal/proto"
)

// spendBase 是「上一个回合结束时的 total」，用来把 thread 累计差分成回合增量。
//
// Model 是实际模型名（牌价估算要用）。三个 token 字段是**已归一化**的口径
// （输入不含缓存）。零值 = 本进程还没有已结束的回合。
type spendBase struct {
	Model  string
	Input  int
	Cached int
	Output int
	// pending 是本回合最近一次看到的 total（尚未推进为基线）。
	pending struct {
		Input  int
		Cached int
		Output int
	}
}

// commit 把本回合最近一次看到的 total 推进为下一个回合的基线。
// 调用时机：收到 turn/completed 时（回合边界）。
func (b spendBase) commit() spendBase {
	b.Input, b.Cached, b.Output = b.pending.Input, b.pending.Cached, b.pending.Output
	return b
}

// parseTurnSpend 从 thread/tokenUsage/updated 取本回合新增的消耗。
//
// 参数：
//   - params: 通知的 params 原文（含 turnId 与 tokenUsage）
//   - base: 上一个回合结束时的基线
//
// 返回：
//   - e: 账目；Key 取 params.turnId
//   - next: 更新了 pending 的基线，调用方存回 runState
//   - ok: turnId 非空且 tokenUsage 可解析时为 true
//
// 三条 codex 独有的规则（**改错了不会报错，只会显示错的数**）：
//   - **取 `total` 不取 `last`**：这里要的是「一共烧了多少」，`last` 只是最后
//     一次调用。（usage.go 里的当前占用恰好相反，取 `last`。）
//   - **`cachedInputTokens` 是 `inputTokens` 的子集，要减不要加**——与
//     claudecode/opencode 相反。实抓佐证 `totalTokens 24673 = inputTokens 24668
//     + outputTokens 5`，缓存若是加项等式不成立。
//   - **`reasoningOutputTokens` 是 `outputTokens` 的子集，不再相加**（同一条等式）。
//
// 为什么一个回合内可以反复调用本函数：同一个 turnId 会来多条通知，但账本按键
// **覆盖**，最后一条即最终值。所以不需要判断「哪条是最后一条」。
//
// 差分为负说明 total 归零了（thread/resume 的形态，未实测），此时按当前值
// 全量入账并由调用方打 Warn——宁可某个回合多算一点，也不写负数进账本。
func parseTurnSpend(params json.RawMessage, base spendBase) (proto.SpendEntry, spendBase, bool) {
	var p struct {
		TurnID     string `json:"turnId"`
		TokenUsage struct {
			Total struct {
				InputTokens       int `json:"inputTokens"`
				CachedInputTokens int `json:"cachedInputTokens"`
				OutputTokens      int `json:"outputTokens"`
			} `json:"total"`
		} `json:"tokenUsage"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.TurnID == "" {
		return proto.SpendEntry{}, base, false
	}
	t := p.TokenUsage.Total
	// 归一化：输入减掉缓存这一子集
	curIn := t.InputTokens - t.CachedInputTokens
	if curIn < 0 {
		curIn = 0
	}
	curCached, curOut := t.CachedInputTokens, t.OutputTokens

	next := base
	next.pending.Input, next.pending.Cached, next.pending.Output = curIn, curCached, curOut

	in := nonNegDelta(curIn, base.Input)
	cached := nonNegDelta(curCached, base.Cached)
	out := nonNegDelta(curOut, base.Output)

	ticks, state := estimateTicks(base.Model, in, cached, out)
	return proto.SpendEntry{
		Key:          p.TurnID,
		InputTokens:  in,
		CachedTokens: cached,
		OutputTokens: out,
		CostTicks:    ticks,
		CostState:    state,
	}, next, true
}

// nonNegDelta 求 cur−base，base 大于 cur 时（计数器归零）退回 cur 本身。
func nonNegDelta(cur, base int) int {
	if cur < base {
		return cur
	}
	return cur - base
}
```

- [ ] **Step 8: 跑测试确认通过**

Run: `go test ./internal/executor/codex/ -v`
Expected: PASS

- [ ] **Step 9: 接线到 adapter**

在 `internal/executor/codex/adapter.go` 的 `runState` 结构里追加：

```go
	// spendBase 是累计消耗的回合基线（B83）。与 usage 的当前占用是两个口径，
	// 共用同一条 thread/tokenUsage/updated 通知但取不同字段，别混。
	spendBase spendBase
```

在 `case method == ntfTokenUsage:` 分支里，已有的 `parseTokenUsage` 之后追加：

```go
		// 累计消耗：同一帧的 total 做回合级差分。取 total 不取 last——
		// 上面那行当前占用恰好相反。
		if e, next, ok := parseTurnSpend(params, r.spendBase); ok {
			if next.pending.Input < r.spendBase.Input {
				a.log.Warn("codex 用量计数器疑似归零，本回合按当前值全量入账",
					"task", r.taskID, "base_input", r.spendBase.Input,
					"now_input", next.pending.Input)
			}
			r.spendBase = next
			a.emit(r, executor.AdapterEvent{Type: "usage", Spend: &e})
		}
```

在 `case method == ntfTurnCompleted:` 分支里，`a.finishTurn(...)` **之前**加：

```go
		// 回合边界：把本回合最后看到的 total 推进为下一个回合的基线。
		r.spendBase = r.spendBase.commit()
```

模型名要进 `spendBase.Model`。B80 在 `openThread` 里解出了它但只 emit、没有留存
（`runState` 上没有模型名字段）。在 `openThread` 里 `a.emit(...ActualModel: out.Model)`
那一行**之前**加：

```go
		// 牌价估算要用实际模型名，而 emit 之后这个值就没别处留存了。
		r.spendBase.Model = out.Model
```

`openThread` 早于任何 `thread/tokenUsage/updated`，所以估算时它一定已经就位。

- [ ] **Step 10: 跑全包测试**

Run: `go test ./internal/executor/codex/ ./internal/agentd/ -v`
Expected: PASS

- [ ] **Step 11: 加关键节点日志**

- 计数器归零：Step 9 已含 Warn。
- **模型不在牌价表**：这是用户看不到花费的唯一原因，日志里必须能查到。
  在 `ntfTokenUsage` 分支里，`e.CostState == proto.CostUnknown` 时打 Warn，
  但**同一个模型只打一次**（用 `runState` 上一个 `bool` 或已打过的模型名兜住），
  否则每回合刷一条：

```go
			if e.CostState == proto.CostUnknown && !r.pricingWarned {
				r.pricingWarned = true
				a.log.Warn("codex 模型不在牌价表，本任务不显示花费",
					"task", r.taskID, "model", r.spendBase.Model)
			}
```

（相应地在 `runState` 加 `pricingWarned bool`。）

- [ ] **Step 12: 加注释**

`pricing.go` / `spend.go` 的文件头与所有函数注释：Step 3、7 已含。
`runState.spendBase` / `pricingWarned`、三处接线的「取 total 不取 last」「回合边界」
说明：Step 9、11 已含。

- [ ] **Step 13: Commit**

```bash
git add internal/executor/codex/
git commit -m "feat(codex): B83 回合级消耗差分与 API 牌价估算，表外模型不猜价"
```

---

## Task 5: grok 的账目

**Files:**
- Create: `internal/executor/grok/spend.go`
- Create: `internal/executor/grok/spend_test.go`
- Modify: `internal/executor/grok/adapter.go`（`awaitTurn` 里解 `_meta`）

**Interfaces:**
- Consumes: `proto.SpendEntry`（Task 1）
- Produces: `parseTurnMetaSpend(meta json.RawMessage) (proto.SpendEntry, bool)`

**背景（重要，别走通知）**：grok 的回合级 usage 挂在 **`session/prompt` 的响应**
`result._meta` 上，而 `awaitTurn` 已经在读这个响应了（取 `stopReason` 当回合边界），
只是没解 `_meta`。同一份数据也在 `turn_completed` 通知里（实测逐回合完全一致），
但响应这一帧更好：幂等键 `promptId` 就在同一块 `_meta` 里，且不用新增 handler。

实抓报文：

```json
{"jsonrpc":"2.0","id":3,"result":{"stopReason":"end_turn","_meta":{
  "sessionId":"019ffb4e-…","promptId":"a95e4bff-…","modelId":"grok-4.6",
  "inputTokens":34502,"outputTokens":56,"totalTokens":34558,
  "cachedReadTokens":5888,"reasoningTokens":51,
  "usage":{"inputTokens":34502,"outputTokens":56,"totalTokens":34558,
    "cachedReadTokens":5888,"cacheCreationTokens":0,"reasoningTokens":51,
    "modelCalls":1,"apiDurationMs":3943,"costUsdTicks":605080000,
    "modelUsage":{"grok-4.6-build":{…}},"numTurns":1}}}}
```

- [ ] **Step 1: 写失败的测试**

创建 `internal/executor/grok/spend_test.go`：

```go
package grok

import (
	"encoding/json"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

const promptMeta = `{"sessionId":"s1","promptId":"p1","modelId":"grok-4.6",
  "inputTokens":34502,"outputTokens":56,"totalTokens":34558,
  "cachedReadTokens":5888,"reasoningTokens":51,
  "usage":{"inputTokens":34502,"outputTokens":56,"totalTokens":34558,
    "cachedReadTokens":5888,"cacheCreationTokens":0,"reasoningTokens":51,
    "modelCalls":1,"costUsdTicks":605080000,"numTurns":1}}`

// TestParseTurnMetaSpendSubtractsCache 验 grok 的 inputTokens **含缓存**，要减。
func TestParseTurnMetaSpendSubtractsCache(t *testing.T) {
	e, ok := parseTurnMetaSpend(json.RawMessage(promptMeta))
	if !ok {
		t.Fatal("应解析成功")
	}
	if e.Key != "p1" {
		t.Fatalf("幂等键应是 promptId，实得 %q", e.Key)
	}
	// 34502 − 5888 − 0 = 28614
	if e.InputTokens != 28614 {
		t.Fatalf("输入应为 34502−5888=28614，实得 %d", e.InputTokens)
	}
	if e.CachedTokens != 5888 {
		t.Fatalf("缓存输入应为 5888，实得 %d", e.CachedTokens)
	}
	// reasoningTokens 51 是 outputTokens 56 的子集，不相加
	if e.OutputTokens != 56 {
		t.Fatalf("输出应为 56（reasoning 已含在内），实得 %d", e.OutputTokens)
	}
	if e.CostTicks != 605080000 {
		t.Fatalf("花费应直接取 costUsdTicks，实得 %d", e.CostTicks)
	}
	if e.CostState != proto.CostReported {
		t.Fatalf("有 costUsdTicks 时应为 reported，实得 %q", e.CostState)
	}
}

// TestParseTurnMetaSpendNoCostIsUnknown 验花费缺席记 unknown，绝不是 0 元。
//
// grok 只对 API-key 流量打花费戳，pool/OAuth 路径经常整块没有；
// cost_is_partial 为真时它也会主动把所有花费字段一并省略。两种都归 unknown。
func TestParseTurnMetaSpendNoCostIsUnknown(t *testing.T) {
	noCost := `{"promptId":"p2","usage":{"inputTokens":100,"outputTokens":10,
	  "cachedReadTokens":40,"cacheCreationTokens":0}}`
	e, ok := parseTurnMetaSpend(json.RawMessage(noCost))
	if !ok {
		t.Fatal("应解析成功")
	}
	if e.CostState != proto.CostUnknown {
		t.Fatalf("没有 costUsdTicks 时应为 unknown，实得 %q", e.CostState)
	}
	if e.CostTicks != 0 {
		t.Fatalf("unknown 时 ticks 必须为 0，实得 %d", e.CostTicks)
	}
	// token 照常入账——不知道花多少钱不代表不知道烧了多少 token
	if e.InputTokens != 60 || e.CachedTokens != 40 || e.OutputTokens != 10 {
		t.Fatalf("token 应照常入账，实得 %+v", e)
	}
}

// TestParseTurnMetaSpendNoPromptID 验没有幂等键就不出账目。
func TestParseTurnMetaSpendNoPromptID(t *testing.T) {
	if _, ok := parseTurnMetaSpend(json.RawMessage(`{"usage":{"inputTokens":5}}`)); ok {
		t.Fatal("没有 promptId 时不应产出账目")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/grok/ -run 'TurnMetaSpend' -v`
Expected: 编译失败，`parseTurnMetaSpend undefined`

- [ ] **Step 3: 写 spend.go**

创建 `internal/executor/grok/spend.go`：

```go
// spend.go —— grok 的**累计消耗**账目解析。
//
// 职责：把 session/prompt 响应的 result._meta 解析成一条 proto.SpendEntry。
// 边界：纯函数，不碰 runState、不发事件、不写日志——接线在 adapter.go。
//
// 与同目录 usage.go 的关系（**grok 是四家里最容易搞错的一家**）：同一条 ACP 线
// 上有两套命名，且缓存的算法**相反**——
//   - usage.go 取 _x.ai/session_notification 的 response_completed，字段是
//     snake_case（input_tokens / cache_read_input_tokens），**不含**缓存要相加，
//     解的是「当前 context 占用」；
//   - 本文件取 session/prompt 响应的 _meta，字段是 camelCase
//     （inputTokens / cachedReadTokens），**已含**缓存要相减，
//     解的是「整个回合消耗了多少」。
// 按字段名模糊匹配必错，grok 官方文档已确认这是它有意为之的两套投影。
package grok

import (
	"encoding/json"

	"github.com/xushixin/handoff/internal/proto"
)

// parseTurnMetaSpend 从 session/prompt 响应的 _meta 取本回合的消耗。
//
// 参数：
//   - meta: result._meta 原文
//
// 返回：
//   - e: 账目；Key 取 promptId
//   - ok: promptId 非空时为 true
//
// 三条规则：
//   - **inputTokens 含缓存，要减**（见文件头）。
//   - **reasoningTokens 是 outputTokens 的子集，不再相加**：实抓
//     `totalTokens 34558 = inputTokens 34502 + outputTokens 56`，
//     而 reasoningTokens 是 51——加上就超过 outputTokens 了。
//   - **花费缺席记 CostUnknown，绝不是 0**：grok 只对 API-key 流量打花费戳，
//     pool/OAuth 路径经常整块没有；cost_is_partial 为真时它还会**主动**把所有
//     花费字段一并省略，就是为了不让消费者把分项加成一份假的完整账单。
//     照抄这个语义：拿不到就说不知道。
func parseTurnMetaSpend(meta json.RawMessage) (proto.SpendEntry, bool) {
	var m struct {
		PromptID string `json:"promptId"`
		Usage    struct {
			InputTokens         int   `json:"inputTokens"`
			OutputTokens        int   `json:"outputTokens"`
			CachedReadTokens    int   `json:"cachedReadTokens"`
			CacheCreationTokens int   `json:"cacheCreationTokens"`
			CostUsdTicks        int64 `json:"costUsdTicks"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(meta, &m); err != nil || m.PromptID == "" {
		return proto.SpendEntry{}, false
	}
	cached := m.Usage.CachedReadTokens + m.Usage.CacheCreationTokens
	in := m.Usage.InputTokens - cached
	if in < 0 {
		in = 0
	}
	e := proto.SpendEntry{
		Key:          m.PromptID,
		InputTokens:  in,
		CachedTokens: cached,
		OutputTokens: m.Usage.OutputTokens,
		CostState:    proto.CostUnknown,
	}
	if m.Usage.CostUsdTicks > 0 {
		e.CostTicks = m.Usage.CostUsdTicks
		e.CostState = proto.CostReported
	}
	return e, true
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/grok/ -v`
Expected: PASS

- [ ] **Step 5: 接线到 finishTurn**

`ACPResult.Result` 就是 `session/prompt` 响应的完整 result 原文（`json.RawMessage`），
`finishTurn` 已经在解它取 `stopReason`。把 `_meta` 并进同一个匿名结构，不新增解析帧。

在 `internal/executor/grok/adapter.go` 的 `finishTurn` 里，把

```go
	var out struct {
		StopReason string `json:"stopReason"`
	}
	_ = json.Unmarshal(res.Result, &out)
	if out.StopReason != "end_turn" {
```

改成

```go
	var out struct {
		StopReason string          `json:"stopReason"`
		Meta       json.RawMessage `json:"_meta"` // 整回合的 usage 与 costUsdTicks（B83）
	}
	_ = json.Unmarshal(res.Result, &out)
	// 累计消耗要**先于** stopReason 判定记账：回合没跑完（拒答、超限、被取消）
	// 这些 token 也已经烧掉了，漏记就成了系统性少算。
	// 注意这与 onUsageNotification 取的是**两套口径**、缓存算法相反（见 spend.go 文件头）。
	if len(out.Meta) > 0 {
		if e, ok := parseTurnMetaSpend(out.Meta); ok {
			if e.CostState == proto.CostUnknown {
				a.log.Info("grok 本回合没有花费戳（pool/OAuth 路径或 cost_is_partial），"+
					"token 照常入账、花费记未知", "task", r.taskID, "prompt", e.Key)
			}
			a.emit(r, executor.AdapterEvent{Type: "usage", Spend: &e})
		}
	}
	if out.StopReason != "end_turn" {
```

`res.Err != nil` 的早退分支在这之前，保持不动——那条路径连响应都没有，无账可记。

- [ ] **Step 6: 跑全包测试**

Run: `go test ./internal/executor/grok/ ./internal/agentd/ -v`
Expected: PASS

- [ ] **Step 7: 加关键节点日志**

- 花费缺席：Step 5 已含 Info（带 `task` / `prompt`）。这条要打 Info 不打 Debug——
  「为什么没有花费」是用户会问的问题，日志里要查得到。
- 解析失败不打（`awaitTurn` 每回合走一次，失败也只是没账目，不是故障）；
  但要在代码里留一行注释说明这是刻意的。

- [ ] **Step 8: 加注释**

`spend.go` 文件头与函数注释：Step 3 已含。`finishTurn` 接线处的「两套口径」与
「先记账后判成败」说明：Step 5 已含。

- [ ] **Step 9: Commit**

```bash
git add internal/executor/grok/
git commit -m "feat(grok): B83 从 session/prompt 响应 _meta 记回合消耗，花费缺席记未知"
```

---

## Task 6: opencode 的账目

**Files:**
- Create: `internal/executor/opencode/spend.go`
- Create: `internal/executor/opencode/spend_test.go`
- Modify: `internal/executor/opencode/adapter.go`（`mapMessageUpdated` 接线）

**Interfaces:**
- Consumes: `proto.SpendEntry`（Task 1）
- Produces: `parseMessageSpend(props json.RawMessage) (proto.SpendEntry, bool)`

**背景**：opencode 是四家里唯一**消息级**而非回合级的，幂等键取 `info.id`。
它的 `tokens.reasoning` 与 `tokens.output` **平行**（不是子集），要相加——
这一格是本计划最容易写错的地方。

实抓报文：

```json
{"type":"message.updated","properties":{"sessionID":"ses_…","info":{
  "id":"msg_…","role":"assistant","cost":0.0001408596,
  "tokens":{"total":47071,"input":131,"output":182,"reasoning":294,
            "cache":{"write":0,"read":46464}},
  "modelID":"deepseek-v4-flash","providerID":"opencode-go",
  "time":{"created":1786628040082,"completed":1786628048168},
  "finish":"tool-calls"}}}
```

`total 47071 = input 131 + output 182 + reasoning 294 + cache.read 46464` —— reasoning 是加项。

- [ ] **Step 1: 写失败的测试**

创建 `internal/executor/opencode/spend_test.go`：

```go
package opencode

import (
	"encoding/json"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

const msgUpdated = `{"sessionID":"ses_1","info":{
  "id":"msg_1","role":"assistant","cost":0.0001408596,
  "tokens":{"total":47071,"input":131,"output":182,"reasoning":294,
            "cache":{"write":0,"read":46464}},
  "modelID":"deepseek-v4-flash"}}`

// TestParseMessageSpendAddsReasoning 验 opencode 的 reasoning 与 output **平行**，要加。
//
// 这与 codex/grok 相反（那两家的 reasoning 是 output 的子集）。
// 实抓等式：total 47071 = input 131 + output 182 + reasoning 294 + cache.read 46464。
func TestParseMessageSpendAddsReasoning(t *testing.T) {
	e, ok := parseMessageSpend(json.RawMessage(msgUpdated))
	if !ok {
		t.Fatal("应解析成功")
	}
	if e.Key != "msg_1" {
		t.Fatalf("幂等键应是 info.id，实得 %q", e.Key)
	}
	// opencode 的 input **不含**缓存，直接用
	if e.InputTokens != 131 {
		t.Fatalf("输入应为 131，实得 %d", e.InputTokens)
	}
	if e.CachedTokens != 46464 { // read 46464 + write 0
		t.Fatalf("缓存输入应为 46464，实得 %d", e.CachedTokens)
	}
	if e.OutputTokens != 476 { // 182 + 294，reasoning 是加项
		t.Fatalf("输出应为 output 182 + reasoning 294 = 476，实得 %d", e.OutputTokens)
	}
	if e.CostTicks != 1408596 { // 0.0001408596 × 1e10
		t.Fatalf("花费应为 1408596 ticks，实得 %d", e.CostTicks)
	}
	if e.CostState != proto.CostReported {
		t.Fatalf("opencode 自报花费，应为 reported，实得 %q", e.CostState)
	}
}

// TestParseMessageSpendSkipsUserAndEmpty 验 user 消息与全零新建消息不入账。
func TestParseMessageSpendSkipsUserAndEmpty(t *testing.T) {
	user := `{"info":{"id":"msg_u","role":"user"}}`
	if _, ok := parseMessageSpend(json.RawMessage(user)); ok {
		t.Fatal("user 消息不应产出账目")
	}
	empty := `{"info":{"id":"msg_e","role":"assistant","cost":0,
	  "tokens":{"total":0,"input":0,"output":0,"reasoning":0,
	            "cache":{"write":0,"read":0}}}}`
	if _, ok := parseMessageSpend(json.RawMessage(empty)); ok {
		t.Fatal("新建的全零消息不应产出账目——它随后会被同 id 的真实值覆盖")
	}
}

// TestParseMessageSpendOverwriteShape 验流式增长的两帧同键、后者更大。
//
// 账本按键覆盖，所以这里只需保证两帧的 Key 相同、值取各自帧的当前值。
func TestParseMessageSpendOverwriteShape(t *testing.T) {
	first := `{"info":{"id":"msg_1","role":"assistant","cost":0.00001,
	  "tokens":{"input":10,"output":1,"reasoning":0,"cache":{"read":0,"write":0}}}}`
	a, ok1 := parseMessageSpend(json.RawMessage(first))
	b, ok2 := parseMessageSpend(json.RawMessage(msgUpdated))
	if !ok1 || !ok2 {
		t.Fatal("两帧都应解析成功")
	}
	if a.Key != b.Key {
		t.Fatalf("同一条消息的两帧应同键，实得 %q vs %q", a.Key, b.Key)
	}
	if b.OutputTokens <= a.OutputTokens {
		t.Fatalf("后一帧应是增长后的值，实得 %d → %d", a.OutputTokens, b.OutputTokens)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/opencode/ -run 'MessageSpend' -v`
Expected: 编译失败，`parseMessageSpend undefined`

- [ ] **Step 3: 写 spend.go**

创建 `internal/executor/opencode/spend.go`：

```go
// spend.go —— opencode 的**累计消耗**账目解析。
//
// 职责：把 message.updated 的 properties.info 解析成一条 proto.SpendEntry。
// 边界：纯函数，不碰 runState、不发事件、不写日志——接线在 adapter.go。
//
// 与同目录 usage.go 的关系：usage.go 取 `input + cache.read + cache.write` 解
// 「当前 context 占用」（只算输入侧），本文件还要算上产出侧，解「一共烧了多少」。
//
// opencode 是四家里唯一**消息级**而非回合级的，幂等键取 info.id。
// 服务端对同一条消息会随生成推很多次、id 相同而 tokens 在涨——账本按键**覆盖**，
// 最后一帧即最终值，所以这里不需要判断「哪一帧是最后一帧」。
package opencode

import (
	"encoding/json"
	"math"

	"github.com/xushixin/handoff/internal/proto"
)

// parseMessageSpend 从 message.updated 的 properties 取这条消息的消耗。
//
// 参数：
//   - props: 事件的 properties 原文（info 是完整的 message 对象）
//
// 返回：
//   - e: 账目；Key 取 info.id
//   - ok: 是 assistant 消息、有 id、且至少有一个非零计数时为 true
//
// 两条 opencode 独有的规则（**改错了不会报错，只会显示错的数**）：
//   - **`tokens.reasoning` 与 `tokens.output` 平行，要相加**——与 codex/grok
//     相反（那两家的 reasoning 是 output 的子集）。实抓等式：
//     `total 47071 = input 131 + output 182 + reasoning 294 + cache.read 46464`。
//     不加就少算，而且这里 reasoning 比 output 还大，少算得很显眼。
//   - **全零帧必须跳过**：新建的 assistant 消息 tokens 全是 0，入账会产生一行
//     空账目（虽然随后会被同 id 覆盖，但中间态会让界面闪一下 0）。
func parseMessageSpend(props json.RawMessage) (proto.SpendEntry, bool) {
	var p struct {
		Info struct {
			ID     string  `json:"id"`
			Role   string  `json:"role"`
			Cost   float64 `json:"cost"`
			Tokens struct {
				Input     int `json:"input"`
				Output    int `json:"output"`
				Reasoning int `json:"reasoning"`
				Cache     struct {
					Read  int `json:"read"`
					Write int `json:"write"`
				} `json:"cache"`
			} `json:"tokens"`
		} `json:"info"`
	}
	if err := json.Unmarshal(props, &p); err != nil {
		return proto.SpendEntry{}, false
	}
	if p.Info.Role != "assistant" || p.Info.ID == "" {
		return proto.SpendEntry{}, false
	}
	tk := p.Info.Tokens
	cached := tk.Cache.Read + tk.Cache.Write
	out := tk.Output + tk.Reasoning // 平行相加，见函数注释
	if tk.Input == 0 && cached == 0 && out == 0 && p.Info.Cost == 0 {
		return proto.SpendEntry{}, false // 新建的空消息，还没有数
	}
	e := proto.SpendEntry{
		Key:          p.Info.ID,
		InputTokens:  tk.Input,
		CachedTokens: cached,
		OutputTokens: out,
		CostState:    proto.CostUnknown,
	}
	if p.Info.Cost > 0 {
		e.CostTicks = int64(math.Round(p.Info.Cost * 1e10))
		e.CostState = proto.CostReported
	}
	return e, true
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/opencode/ -v`
Expected: PASS

- [ ] **Step 5: 接线到 adapter**

在 `internal/executor/opencode/adapter.go` 的 `mapMessageUpdated` 里，
已有的 `parseMessageUsage` 那一段**之后**追加：

```go
	// 累计消耗：同一帧还带这条消息的 cost 与产出侧 token。与上面的当前占用
	// 是两个口径——上面只算输入侧，这里连产出一起算，且 reasoning 要相加。
	if e, ok := parseMessageSpend(props); ok {
		a.emit(r, executor.AdapterEvent{Type: "usage", Spend: &e})
	}
```

- [ ] **Step 6: 跑全包测试**

Run: `go test ./internal/executor/opencode/ ./internal/agentd/ -v`
Expected: PASS

- [ ] **Step 7: 加关键节点日志**

opencode 的消息帧频率极高（一条消息推几十次），**这里刻意不打任何日志**——
入账的 Debug 已经由 `handleSpend` 统一打了，adapter 再打就是双份刷屏。
在接线处补一行注释说明这是刻意的，避免后人「补日志」时加回来。

- [ ] **Step 8: 加注释**

`spend.go` 文件头与函数注释：Step 3 已含。接线处的「两个口径」与「刻意不打日志」：
Step 5、7。

- [ ] **Step 9: Commit**

```bash
git add internal/executor/opencode/
git commit -m "feat(opencode): B83 按 message.id 记消耗，reasoning 与 output 相加"
```

---

## Task 7: 前端切换视图

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/app/lib/format.ts`
- Modify: `web/src/app/lib/format.test.ts`
- Modify: `web/src/app/task/TaskHeader.tsx`
- Modify: `web/src/app/task/TaskHeader.test.tsx`

**Interfaces:**
- Consumes: `Task.cumulative`（Task 1 的 JSON 契约）
- Produces: `formatCost(cost)`、`formatCumulativeLine(task)`

**形态基准**（原型 `prototypes/desktop-console/` 已确认，三条必须照做）：

1. 切换按钮在标题行右上角，文案是**要切去的那个视图**的名字（当前显占用时写「累计用量」，反之写「当前占用」）。
2. 累计行**整行铺开、跨掉标签列**（`grid-column: 1 / -1`），「累计」二字并进内容里当前缀。原因：内容比 Context 行长得多，关在第二列时宽度正好卡满、多一位数字就折行；跨列后腾出约 16% 余量，而**行数和框高不变**——切换视图时下面的正文不会跳。
3. 估算/不全的花费必须看得出不是自报值（`≈` + 小标）。和自报值长得一样，就是在暗示一个它没有的精度。

- [ ] **Step 1: 加前端类型**

在 `web/src/api/types.ts` 的 `Usage` 接口之后追加：

```ts
// Cost 是累计花费及其可信度。
// state 为 'partial' 时 ticks 只是**已知部分**的和，是下界不是总额。
export interface Cost {
  ticks: number   // 1 USD = 10^10 ticks
  state: 'reported' | 'estimated' | 'partial' | 'unknown'
}

// Cumulative 是任务的累计消耗。
// 与 Usage 是两个口径：Usage 是「现在占用多少」，本结构是「一共烧了多少」。
// 只在任务详情（GetTask）里有，列表接口不带。
export interface Cumulative {
  input_tokens: number   // 未命中缓存的输入
  cached_tokens: number  // 命中缓存的输入
  output_tokens: number  // 含 reasoning
  total_tokens: number   // 三项之和，后端算好
  cost?: Cost            // 缺省=还没有任何花费信息
}
```

并在 `Task` 接口的 `usage?: Usage` 之后加：

```ts
  cumulative?: Cumulative  // 累计消耗；缺省=还没有账目，或本次是列表读取
```

- [ ] **Step 2: 写失败的 format 测试**

在 `web/src/app/lib/format.test.ts` 追加：

```ts
import { formatCost, formatCumulativeLine } from './format'

describe('formatCost', () => {
  it('自报且完整：直接显金额，无 ≈、无小标', () => {
    expect(formatCost({ ticks: 42_000_000_000, state: 'reported' }))
      .toEqual({ text: '$4.20', hint: '' })
  })
  it('估算：带 ≈ 与「估算」小标', () => {
    expect(formatCost({ ticks: 42_000_000_000, state: 'estimated' }))
      .toEqual({ text: '≈$4.20', hint: '估算' })
  })
  it('不全：带 ≈ 与「不全」小标——它是下界不是近似值', () => {
    expect(formatCost({ ticks: 42_000_000_000, state: 'partial' }))
      .toEqual({ text: '≈$4.20', hint: '不全' })
  })
  it('未知：显 — 而不是 $0.00', () => {
    expect(formatCost({ ticks: 0, state: 'unknown' }))
      .toEqual({ text: '—', hint: '' })
  })
  it('金额不足一分也不显示成 $0.00', () => {
    // 0.004 美元 → 保留两位会变 $0.00，必须换更细的位数
    const r = formatCost({ ticks: 40_000_000, state: 'reported' })
    expect(r.text).not.toBe('$0.00')
  })
})

describe('formatCumulativeLine', () => {
  const base = { cumulative: {
    input_tokens: 340_200, cached_tokens: 820_500,
    output_tokens: 39_300, total_tokens: 1_200_000,
    cost: { ticks: 42_000_000_000, state: 'estimated' as const },
  } }
  it('五项齐全时按原型顺序排列', () => {
    expect(formatCumulativeLine(base as never))
      .toBe('1200.0k · 输入 340.2k · 缓存 820.5k · 输出 39.3k · ≈$4.20')
  })
  it('没有累计时返回空串，由调用方决定不渲染', () => {
    expect(formatCumulativeLine({} as never)).toBe('')
  })
  it('没有花费信息时只显四项 token', () => {
    const noCost = { cumulative: { ...base.cumulative, cost: undefined } }
    expect(formatCumulativeLine(noCost as never))
      .toBe('1200.0k · 输入 340.2k · 缓存 820.5k · 输出 39.3k')
  })
})
```

- [ ] **Step 3: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/lib/format.test.ts`
Expected: FAIL，`formatCost is not a function`

- [ ] **Step 4: 写 format 函数**

在 `web/src/app/lib/format.ts` 末尾追加：

```ts
// TICKS_PER_USD 是花费的内部单位换算：后端全程用整数 ticks 累加，
// 只在这里（展示的最后一步）转成美元。
const TICKS_PER_USD = 1e10

// formatCost 把累计花费格式化成「金额 + 可信度小标」。
//
// 返回 { text, hint }：text 进正文，hint 是紧跟其后的小字（空串=不显示小标）。
//
// 四种状态三种形态（用户已确认的形态决策）：
//   - reported  → `$4.20`，无标记：这是执行器自报的完整值
//   - estimated → `≈$4.20` +「估算」：handoff 按 API 牌价算的，可能不准
//   - partial   → `≈$4.20` +「不全」：**下界**，真实值只会更高
//   - unknown   → `—`：一次都没拿到
//
// 为什么 estimated 与 partial 不合并成一个「≈」：它们对用户的含义相反。
// 估算是近似值（可能高可能低），不全是下界（只会更高）。合并会把下界讲成
// 近似值——看到 `≈$4.20` 的人不会想到实际可能是 $8。
//
// 为什么 unknown 显「—」而不是 `$0.00`：花费的缺席意味着
// "unreported or incomplete, never free"。用 0 冒充「不知道」是在撒谎。
export function formatCost(cost: Cost): { text: string; hint: string } {
  if (cost.state === 'unknown') return { text: '—', hint: '' }
  const usd = cost.ticks / TICKS_PER_USD
  // 不足一分的金额用更细的位数，否则 $0.004 会显示成 $0.00——那和「免费」没区别
  const amount = usd >= 0.01 ? usd.toFixed(2) : usd.toFixed(4)
  if (cost.state === 'reported') return { text: `$${amount}`, hint: '' }
  return { text: `≈$${amount}`, hint: cost.state === 'estimated' ? '估算' : '不全' }
}

// formatCumulativeLine 组装「累计用量」视图的整行文案（不含「累计」前缀，
// 前缀由 TaskHeader 单独渲染成弱化样式）。
//
//   1200.0k · 输入 340.2k · 缓存 820.5k · 输出 39.3k · ≈$4.20
//
// 没有累计数据时返回空串，由调用方决定不渲染这一行。
// 花费缺席时只显四项 token——不知道花了多少钱，不代表不知道烧了多少 token。
export function formatCumulativeLine(task: Task): string {
  const c = task.cumulative
  if (!c) return ''
  const parts = [
    formatTokens(c.total_tokens),
    `输入 ${formatTokens(c.input_tokens)}`,
    `缓存 ${formatTokens(c.cached_tokens)}`,
    `输出 ${formatTokens(c.output_tokens)}`,
  ]
  if (c.cost) parts.push(formatCost(c.cost).text)
  return parts.join(' · ')
}
```

记得在文件顶部的 import 里补上 `Cost`：

```ts
import type { Cost, Task } from '../../api/types'
```

- [ ] **Step 5: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/lib/format.test.ts`
Expected: PASS

**注意**：若 `formatTokens(1_200_000)` 的现有实现输出的不是 `1200.0k`，
以**现有实现的输出**为准修改测试期望值——不要为了让期望值好看去改 `formatTokens`，
那会动到 B80 已经通过的用例。

- [ ] **Step 6: 写 TaskHeader 测试**

在 `web/src/app/task/TaskHeader.test.tsx` 追加：

```tsx
it('默认显当前占用，按钮文案是要切去的视图', () => {
  render(<TaskHeader task={taskWithCumulative} />)
  expect(screen.getByText('Context')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: '累计用量' })).toBeInTheDocument()
})

it('点按钮切到累计视图，按钮文案反转', async () => {
  render(<TaskHeader task={taskWithCumulative} />)
  await userEvent.click(screen.getByRole('button', { name: '累计用量' }))
  expect(screen.getByText(/输入 340\.2k/)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: '当前占用' })).toBeInTheDocument()
})

it('没有累计数据时不显示切换按钮', () => {
  render(<TaskHeader task={taskWithoutCumulative} />)
  expect(screen.queryByRole('button', { name: '累计用量' })).not.toBeInTheDocument()
})

it('估算花费带「估算」小标，自报花费不带', async () => {
  render(<TaskHeader task={taskWithCumulative} />)   // cost.state = 'estimated'
  await userEvent.click(screen.getByRole('button', { name: '累计用量' }))
  expect(screen.getByText('估算')).toBeInTheDocument()
})
```

`taskWithCumulative` / `taskWithoutCumulative` 照该文件已有的 fixture 写法构造
（复制现成的 task fixture 再加/去掉 `cumulative` 字段）。

- [ ] **Step 7: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/task/TaskHeader.test.tsx`
Expected: FAIL，找不到按钮

- [ ] **Step 8: 改 TaskHeader**

把 `web/src/app/task/TaskHeader.tsx` 里的「执行器」两行替换成：

```tsx
        <dt className="text-muted-foreground">执行器</dt>
        <dd className="flex items-center gap-2">
          <span className="min-w-0 flex-1">
            {view === 'context' ? formatExecutorLine(task) : formatCumulativeLine(task)}
          </span>
          {/* 有累计数据才给切换入口：没有账目时切过去是一片空白 */}
          {task.cumulative ? (
            <button
              type="button"
              className="shrink-0 rounded border px-1.5 py-0.5 text-[11px] text-muted-foreground hover:bg-muted"
              onClick={() => setView(view === 'context' ? 'cumulative' : 'context')}
            >
              {view === 'context' ? '累计用量' : '当前占用'}
            </button>
          ) : null}
        </dd>
```

在组件顶部加状态：

```tsx
  // 「当前占用」与「累计消耗」是两个口径，同一行两个视图切换显示。
  // 切换不改行数与框高——原型量过：累计行跨掉标签列后有约 16% 余量，
  // 不会折行，下面的正文不会跳。
  const [view, setView] = useState<'context' | 'cumulative'>('context')
```

花费的小标要单独渲染（不能并进 `formatCumulativeLine` 的字符串，否则没法给它
弱化样式）。在累计视图的 span 里追加：

```tsx
          {view === 'cumulative' && task.cumulative?.cost
            ? (() => {
                const { hint } = formatCost(task.cumulative.cost)
                return hint ? (
                  <em className="ml-1 not-italic text-[10px] text-muted-foreground">{hint}</em>
                ) : null
              })()
            : null}
```

**布局约束**：`dd` 用 `flex` 已经占满第二列。若实测文案在窄屏折行，
按原型的做法让这一行跨掉标签列（把 `dt`/`dd` 换成一个 `grid-column: 1 / -1`
的单元素），**不要靠缩短标签文案来腾地方**——原型已经验证过跨列是更好的解法。

- [ ] **Step 9: 跑测试确认通过**

Run: `cd web && npx vitest run && npx tsc --noEmit`
Expected: PASS，无类型错误

- [ ] **Step 10: 加注释**

`types.ts` 的两个接口、`formatCost` / `formatCumulativeLine`、`TaskHeader` 的
`view` 状态与「有累计才给按钮」：Step 1、4、8 已含。
前端不打日志（浏览器控制台不是本项目的日志通道），这一步只确认注释齐备。

- [ ] **Step 11: Commit**

```bash
git add web/src/api/types.ts web/src/app/lib/format.ts web/src/app/lib/format.test.ts web/src/app/task/TaskHeader.tsx web/src/app/task/TaskHeader.test.tsx
git commit -m "feat(web): B83 累计用量切换视图，估算与不全的花费带可信度小标"
```

---

## 收尾自检

全部 task 完成后，跑一遍并逐项确认：

```bash
go test ./internal/... && cd web && npx vitest run && npx tsc --noEmit
```

- [ ] 四家的换算方向各自正确：**codex 减缓存、grok 减缓存、claudecode 加缓存、opencode 加缓存**；**opencode 的 reasoning 加、codex/grok 的 reasoning 不加**。
- [ ] `SetTaskUsage` 与 `UpsertSpend` 两条路径没有任何交叉调用。
- [ ] 花费缺席的三条路径（codex 表外模型、grok 无花费戳、grok cost_is_partial）全部落到 `unknown`，界面显 `—` 而不是 `$0.00`。
- [ ] 每个新文件有文件头注释（职责 + 边界）；每个导出标识符有文档注释。
- [ ] 每个错误分支都带上下文（task id、key、cause）；成功路径的入账有 Debug 日志；高频路径刻意不打的地方有注释说明。
- [ ] 没有任何 `fmt.Printf` / `console.log` 充当日志。
