# 实现计划：耗时聚合与 TUI 展示（需求 A · T6 + T7）

日期 2026-08-22 · 节点 `charter:plan` · 前置 [spec §A](2026-08-22-executor-timing-and-custom-launchers-design.md) + [契约冻结物](2026-08-22-executor-timing-contract.md) + [拆解 §4.4 T6/T7](2026-08-22-executor-timing-breakdown.md)

采集侧（T1..T5）已全部落地并合入本分支。本计划只做剩下两张卡：

| 卡 | 子系统 | 类型 | 内容 |
|---|---|---|---|
| **T6** | `d_ledger`（`internal/store`） | 逻辑型 | `Store.TaskTiming` 聚合实现 + `GetTask` 接线 + 契约夹具 |
| **T7** | `d_web`（`web/src/app/task` + `web/src/api`） | 逻辑型 | `ToolCard` 单次耗时 + 页头任务级耗时面板 + TS 契约断言 |

拆解 §4.1 的 DAG：`T6 → T7`。本计划把它铺成 **4 个 task**（T7 拆成纯函数层与组件层两步，便于逐步跑红/跑绿），**严格串行**。

---

## 0. 基线核验（动手前已在本分支复核，2026-08-22）

判据写下时对、隔几天就不对，所以下面每条都是**本轮实跑**的结果，不是引用早前的绿。

```
$ go build ./... && go test ./internal/store/ ./internal/proto/
ok  github.com/Xsxdot/handoff/internal/store   (cached)
ok  github.com/Xsxdot/handoff/internal/proto   (cached)

$ cd web && npx tsc -b
（无输出，退出码 0）

$ cd web && npx vitest run src/app/task src/api
Test Files  23 passed (23)
     Tests  214 passed (214)
```

**基线上的三个事实**（本卡要改掉/依赖的东西，逐条指认过）：

1. `internal/store/store.go:661` 的 `TaskTiming` 方法体逐字是 `_ = taskID` + `return nil, nil`——**空壳**，且没有任何调用方（`grep -rn '\.TaskTiming(' internal cmd --include='*.go' | grep -v _test.go` 只命中注释）。
2. 写路径**已经通了**：`internal/agentd/manager.go:2925` 调 `m.st.UpsertTiming(taskID, *ev.Timing)`。所以库里已经有真实账目行，T6 是纯读侧。
3. `web/src/api/contract.test.ts` 里 **没有任何 `timing` 字样**（`grep -in timing` 无输出），`internal/proto/contract_fixture_test.go` 的 `taskSample` 不设 `Timing`、`frameSample` 不设 `DurMS`。→ 拆解 §6.6 那条「本卡最容易漏的一项」在基线上**确实是缺的**，不是已经有人补过了。

---

## 1. 代码与库事实（带出处，实现时不要凭记忆改）

### 1.1 账本表结构（`internal/store/store.go:126-146`）

```
task_timing_ledger(
  task_id TEXT NOT NULL, entry_key TEXT NOT NULL,
  kind TEXT NOT NULL,               -- api / tool / turn
  turn INTEGER NOT NULL DEFAULT 0,
  dur_ms INTEGER NOT NULL DEFAULT 0,
  offset_ms INTEGER NOT NULL DEFAULT 0,   -- 仅 kind=tool 有意义，回合内相对偏移
  label TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMP NOT NULL,
  PRIMARY KEY (task_id, entry_key))
```

六个要读的列**全部 NOT NULL 带默认值**，所以 `rows.Scan` 直接扫进 `string` / `int64` 安全，不需要 `sql.NullString`。

### 1.2 账目是怎么产出来的（`internal/executor/turn/timing.go`）

读实现而不是猜，四条与聚合直接相关：

- `turnEntryLocked`（:236）**每次段事件都刷一条** `turn/<turn>` 键的条目，按同键覆盖 → 同一回合库里**只有一行** `kind=turn`，其 `dur_ms` 是最新的回合墙钟。**聚合对 turn 行必须赋值而不是累加。**
- `closeAPILocked`（:216）产 `api/<turn>/<batches>` 条目，一个回合内**可能多条**（每批工具之间一条）→ **api 必须累加。**
- `ToolEnd`（:150）产 `tool/<turn>/<part>` 条目，带 `OffsetMS = ot.start.Sub(s.turnStart)` → **偏移是回合内相对量，跨回合不可比**，区间并集必须**分回合做完再相加**。
- `EndTurn`（:184）的注释写死：**还开着的工具不产条目**——「它造成的缺口由聚合层的 Partial 标出来，这正是 Partial 存在的理由」。

推论（重要，写进注释）：一个**正在跑**的回合，`BeginTurn` 已写 `turn/<n>`，但模型段要到第一次 `ToolStart` 或 `EndTurn` 才关闭 → 该回合**没有 api 行**。所以**运行中的任务几乎总是 `Partial=true`**，这不是 bug，正是「other 偏大」的诚实表达。

### 1.3 `Detail` 里装的是什么（决定下钻层规则）

- `claudecode/adapter.go:1095 timingDetail`：`Bash` 工具取 `input.command` 原文，其余工具回落 `compactJSON(input)`。
- `opencode/adapter.go:1634 toolDetail`：有 `command` 取 `command`，否则 `string(input)`（JSON）。
- `codex/adapter.go:1151`：`fileChange` 用 `it.renderLine()`（路径清单）。
- `grok/adapter.go:1043`：`rawCommand(tu.RawInput)`。

→ **Detail 一半是命令串、一半是 JSON**。下钻层（P4=(b)「前两段」）必须显式排除 JSON 开头的那一半，否则排行里会长出 `{"path":` 这种对「哪条命令慢」毫无价值的格子。

### 1.4 `Frame.DurMS` 的线格式（`internal/proto/frames.go:69-75`，`turn/frames.go:225-241`）

```go
DurMS int64 `json:"dur_ms,omitempty"`
```

`durMS(dur)` 把负数（未知）折算成 0，`omitempty` 于是让字段缺席。**代价：一次真实的 0ms 调用与「未报耗时」在线上不可分**——这是既有契约的取舍，T7 **不要试图区分**，一律按「缺席 = 未知」渲染。

### 1.5 前端既有纪律（照抄，不要另立）

- `web/src/app/task/UsageChip.tsx:39`：`if (!usage && !cumulative) return null`——**账目缺席时整体不渲染，不画空表**。同一条纪律适用于新的耗时面板。
- 同文件 :24-27 的注释写死：**早退必须在所有 hook 之后**，否则「账目从有到无的那一帧 hook 数量会变，React 直接报错」。新组件照此办理。
- `web/src/app/task/frames.ts:154` 的 `switch` + `:204` 的 `default: unknown`：`FrameType` **没有新增取值**，`dur_ms` 挂在既有的 `tool_result` 上 → 这个 switch 不受影响。

### 1.6 契约夹具机制（`internal/proto/contract_fixture_test.go`）

- `TestContractFixtures`（:52）逐字节比对 `web/src/api/testdata/*.json`；`-update` 重写。
- `tasksRespSample`（:378）**复用 `taskSample`** → 给 `taskSample` 加 `Timing` 会连带让 `TasksResp.json` 里的任务带上 `timing`。而 `ListTasks` **不填** `Timing`。**夹具会因此编码一个不存在的世界**（测试全绿地验证一个编出来的现实）。→ `tasksRespSample` 必须显式清空 Timing，见 Task 1 步骤 6。

---

## 2. 口径（契约 §2.1，实现时逐条对照）

| 量 | 算法 |
|---|---|
| `TotalMS` | Σ 各回合 `kind=turn` 行的 `dur_ms`（每回合一行，赋值不累加） |
| `APIMS` | Σ 全部 `kind=api` 行的 `dur_ms` |
| `ToolMS` | Σ 全部 `kind=tool` 行的 `dur_ms`（可大于 ToolSpanMS） |
| `ToolSpanMS` | **分回合**求 `[offset, offset+dur)` 的区间并集长度，再跨回合相加 |
| `OtherMS` | `max(0, TotalMS − APIMS − ToolSpanMS)` |
| `Partial` | 下列任一成立：① 某回合有 turn 行但无 api 行；② 某回合有段但无 turn 行；③ 出现未知 `kind`；④ 上面的差额为负 |
| `Buckets` | 按 `label` 聚合（空 label → `(未知工具)`），下钻一层按命令首词；两层各自降序、各自截断到 `TimingBucketCap` |

**turn 不是段**（`internal/proto/timing.go:22-27`）：它是 other 的分母，绝不加进三分。

---

## 3. Task 1 · T6 聚合纯函数 + 接线 + 夹具（`d_ledger`）

**Interfaces**

- Consumes：`proto.TimingKind{API,Tool,Turn}`、`proto.TimingEntry`、`proto.TaskTiming`、`proto.TimingBucket`、`proto.TimingBucketCap`（均已冻结，**不得增删字段**）；`Store.UpsertTiming(taskID string, e proto.TimingEntry) error`（已实现）。
- Produces：`func (s *Store) TaskTiming(taskID string) (*proto.TaskTiming, error)`（签名不变，实现落地）；`GetTask` 返回的 `*proto.Task` 的 `Timing` 字段被填充。
- 对 T7 的承诺：REST 上 `Task.timing` 的形状 = `web/src/api/types.ts:631` 的 `TaskTiming`，且 `web/src/api/testdata/Task.json` 里能看到一个 `tool_ms > tool_span_ms`、`partial=false` 的真实样本。

### 步骤

**步骤 1 — 建文件并写失败测试（先红）**

新建 `internal/store/timing_agg_test.go`（`package store`，白盒，可直接测未导出的 `aggregateTiming`）：

```go
// timing_agg_test.go —— 耗时聚合纯函数的穷举测试 + 接线的真实 SQLite 验证。
//
// 为什么纯函数与接线分开测：聚合的分支（并发、缺段、负差、截断）在 SQL 之上
// 穷举成本极低；而接线只有两条断言（GetTask 填 / ListTasks 不填），却必须
// 走真库才算数——两者混在一起会让穷举那部分被建库开销拖慢几十倍。
package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// tr 造一条账目行，省掉每个用例重复写字段名。
func tr(kind string, turn int, dur, off int64, label, detail string) timingRow {
	return timingRow{Kind: kind, Turn: turn, DurMS: dur, OffsetMS: off, Label: label, Detail: detail}
}

func TestAggregateTimingEmpty(t *testing.T) {
	if got := aggregateTiming(nil); got != nil {
		t.Fatalf("空账目应返回 nil（「还不知道」），实际 %+v", got)
	}
	if got := aggregateTiming([]timingRow{}); got != nil {
		t.Fatalf("空切片应返回 nil，实际 %+v", got)
	}
}

func TestAggregateTimingSingleTurnNoConcurrency(t *testing.T) {
	got := aggregateTiming([]timingRow{
		tr("turn", 1, 1000, 0, "", ""),
		tr("api", 1, 400, 0, "", ""),
		tr("api", 1, 300, 0, "", ""),
		tr("tool", 1, 200, 400, "Bash", "go test ./..."),
	})
	if got == nil {
		t.Fatal("有账目却返回 nil")
	}
	if got.TotalMS != 1000 || got.APIMS != 700 || got.ToolMS != 200 || got.ToolSpanMS != 200 {
		t.Fatalf("三分法算错: %+v", got)
	}
	if got.OtherMS != 100 {
		t.Fatalf("OtherMS 应为 1000-700-200=100，实际 %d", got.OtherMS)
	}
	if got.Partial {
		t.Fatalf("账目齐全不应 Partial: %+v", got)
	}
}

// 并发工具：Σtool 大于墙钟跨度。这是 OffsetMS 存在的唯一理由，
// 也是「取其一冒充另一个」会静默算错的那个分支。
func TestAggregateTimingConcurrentTools(t *testing.T) {
	got := aggregateTiming([]timingRow{
		tr("turn", 1, 1000, 0, "", ""),
		tr("api", 1, 100, 0, "", ""),
		// [100,600) 与 [300,700) 重叠 → 并集 [100,700) = 600
		tr("tool", 1, 500, 100, "Bash", "go build ./..."),
		tr("tool", 1, 400, 300, "Bash", "go vet ./..."),
	})
	if got.ToolMS != 900 {
		t.Fatalf("ToolMS 应为时长之和 900，实际 %d", got.ToolMS)
	}
	if got.ToolSpanMS != 600 {
		t.Fatalf("ToolSpanMS 应为区间并集 600，实际 %d", got.ToolSpanMS)
	}
	if got.OtherMS != 300 {
		t.Fatalf("OtherMS 应为 1000-100-600=300（用 ToolMS 算会得 0），实际 %d", got.OtherMS)
	}
}

// 跨回合：offset 是回合内相对量，两个回合的 [0,500) 不能并成一个 500。
func TestAggregateTimingAcrossTurns(t *testing.T) {
	got := aggregateTiming([]timingRow{
		tr("turn", 1, 800, 0, "", ""), tr("api", 1, 300, 0, "", ""),
		tr("tool", 1, 500, 0, "Bash", "ls"),
		tr("turn", 2, 900, 0, "", ""), tr("api", 2, 400, 0, "", ""),
		tr("tool", 2, 500, 0, "Bash", "ls"),
	})
	if got.TotalMS != 1700 || got.APIMS != 700 {
		t.Fatalf("跨回合求和错: %+v", got)
	}
	if got.ToolSpanMS != 1000 {
		t.Fatalf("两个回合各 500，并集必须分回合算，应为 1000，实际 %d", got.ToolSpanMS)
	}
}

func TestAggregateTimingPartialMissingAPI(t *testing.T) {
	got := aggregateTiming([]timingRow{
		tr("turn", 1, 1000, 0, "", ""),
		tr("tool", 1, 200, 0, "Bash", "ls"),
	})
	if !got.Partial {
		t.Fatalf("回合缺 api 条目必须 Partial: %+v", got)
	}
	if got.OtherMS != 800 {
		t.Fatalf("缺段时 other 偏大是预期行为，应为 800，实际 %d", got.OtherMS)
	}
}

func TestAggregateTimingPartialMissingTurn(t *testing.T) {
	got := aggregateTiming([]timingRow{tr("api", 3, 500, 0, "", "")})
	if !got.Partial {
		t.Fatalf("有段却无回合墙钟必须 Partial: %+v", got)
	}
}

func TestAggregateTimingPartialUnknownKind(t *testing.T) {
	got := aggregateTiming([]timingRow{
		tr("turn", 1, 1000, 0, "", ""), tr("api", 1, 900, 0, "", ""),
		tr("wormhole", 1, 5000, 0, "", ""),
	})
	if !got.Partial {
		t.Fatalf("未知 kind 必须计入 Partial: %+v", got)
	}
	if got.TotalMS != 1000 || got.APIMS != 900 || got.ToolMS != 0 {
		t.Fatalf("未知 kind 不得计进任何一档: %+v", got)
	}
}

func TestAggregateTimingNegativeResidual(t *testing.T) {
	got := aggregateTiming([]timingRow{
		tr("turn", 1, 100, 0, "", ""),
		tr("api", 1, 900, 0, "", ""),
		tr("tool", 1, 300, 0, "Bash", "ls"),
	})
	if got.OtherMS != 0 {
		t.Fatalf("负差必须夹到 0，实际 %d", got.OtherMS)
	}
	if !got.Partial {
		t.Fatalf("负差说明采集有 bug，必须 Partial: %+v", got)
	}
}

// 截断：反向断言「第 21 名不在结果里」，并配一条正面断言锁住留下的是最大的那些。
func TestAggregateTimingBucketCap(t *testing.T) {
	var rows []timingRow
	rows = append(rows, tr("turn", 1, 1_000_000, 0, "", ""), tr("api", 1, 1, 0, "", ""))
	// 造 25 个工具名，耗时 100..2500 递增；截断后应只剩最大的 20 个（600..2500）
	for i := 1; i <= 25; i++ {
		rows = append(rows, tr("tool", 1, int64(i)*100, 0, fmt.Sprintf("T%02d", i), ""))
	}
	got := aggregateTiming(rows)
	if len(got.Buckets) != proto.TimingBucketCap {
		t.Fatalf("应截断到 %d 格，实际 %d", proto.TimingBucketCap, len(got.Buckets))
	}
	if got.Buckets[0].Label != "T25" || got.Buckets[0].DurMS != 2500 {
		t.Fatalf("排行第一应是最大的 T25/2500，实际 %+v", got.Buckets[0])
	}
	if last := got.Buckets[len(got.Buckets)-1]; last.Label != "T06" {
		t.Fatalf("留下的应是最大的 20 个（末位 T06），实际末位 %s", last.Label)
	}
	for _, b := range got.Buckets {
		// 反向断言：被截掉的必须是最小的那五个，一个都不许混进来
		if b.Label == "T01" || b.Label == "T05" {
			t.Fatalf("被截断的应是最小的那些，%s 不该出现", b.Label)
		}
	}
}

// 下钻层：命令首词按前两段取，JSON 入参不下钻，环境赋值前缀跳过。
func TestAggregateTimingSubBuckets(t *testing.T) {
	got := aggregateTiming([]timingRow{
		tr("turn", 1, 10_000, 0, "", ""), tr("api", 1, 1, 0, "", ""),
		tr("tool", 1, 300, 0, "Bash", "go test ./internal/store/"),
		tr("tool", 1, 200, 0, "Bash", "go test ./internal/proto/"),
		tr("tool", 1, 100, 0, "Bash", "go build ./..."),
		tr("tool", 1, 50, 0, "Bash", "TOKEN=s3cret go test ./..."),
		tr("tool", 1, 900, 0, "Read", `{"file_path":"/a/b.go"}`),
	})
	var bash, read *proto.TimingBucket
	for i := range got.Buckets {
		switch got.Buckets[i].Label {
		case "Bash":
			bash = &got.Buckets[i]
		case "Read":
			read = &got.Buckets[i]
		}
	}
	if bash == nil || read == nil {
		t.Fatalf("两个工具名都该有一格: %+v", got.Buckets)
	}
	if read.Sub != nil {
		t.Fatalf("入参是 JSON 不下钻，实际 %+v", read.Sub)
	}
	if len(bash.Sub) != 2 {
		t.Fatalf("go test / go build 两格（TOKEN= 前缀应并进 go test），实际 %+v", bash.Sub)
	}
	if bash.Sub[0].Label != "go test" || bash.Sub[0].DurMS != 550 || bash.Sub[0].Count != 3 {
		t.Fatalf("go test 应聚成 300+200+50=550 / 3 次，实际 %+v", bash.Sub[0])
	}
	// 反向断言：凭据不得被抬进排行标签
	for _, s := range bash.Sub {
		if s.Label == "TOKEN=s3cret go" {
			t.Fatalf("环境赋值必须跳过，不得成为标签: %+v", s)
		}
	}
	// 下钻只有一层：sub 的 sub 恒为 nil
	for _, s := range bash.Sub {
		if s.Sub != nil {
			t.Fatalf("下钻只许一层，实际 %+v", s)
		}
	}
}

// 排序确定性：同耗时的两格按 Label 升序，不随 map 迭代顺序漂。
// 偶发翻红会被当噪音忽略，于是判据实际失效——这条是防那个的。
func TestAggregateTimingDeterministicOrder(t *testing.T) {
	rows := []timingRow{tr("turn", 1, 5000, 0, "", ""), tr("api", 1, 1, 0, "", "")}
	for _, name := range []string{"Zeta", "Alpha", "Mike"} {
		rows = append(rows, tr("tool", 1, 100, 0, name, ""))
	}
	want := []string{"Alpha", "Mike", "Zeta"}
	for i := 0; i < 50; i++ {
		got := aggregateTiming(rows)
		for j, b := range got.Buckets {
			if b.Label != want[j] {
				t.Fatalf("第 %d 次调用次序漂了: %v", i, got.Buckets)
			}
		}
	}
}

// 接线：GetTask 填 Timing，ListTasks 不填。真实 SQLite 上跑。
func TestTaskTimingWiring(t *testing.T) {
	s := openTestStore(t)
	t0 := time.Now().UTC().Truncate(time.Second)
	id := "11111111-2222-3333-4444-555555555555"
	if err := s.CreateTask(&proto.Task{
		ID: id, RepoPath: "/r", State: proto.TaskStatePending, CreatedAt: t0, UpdatedAt: t0,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// 还没有账目：GetTask 的 Timing 必须是 nil，不是零值结构
	got, err := s.GetTask(id)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Timing != nil {
		t.Fatalf("无账目时 Timing 应为 nil（「还不知道」），实际 %+v", got.Timing)
	}

	for _, e := range []proto.TimingEntry{
		{Key: "turn/1", Kind: proto.TimingKindTurn, Turn: 1, DurMS: 1000},
		{Key: "api/1/0", Kind: proto.TimingKindAPI, Turn: 1, DurMS: 700},
		{Key: "tool/1/p1", Kind: proto.TimingKindTool, Turn: 1, DurMS: 200,
			OffsetMS: 700, Label: "Bash", Detail: "go test ./..."},
	} {
		if err := s.UpsertTiming(id, e); err != nil {
			t.Fatalf("UpsertTiming %s: %v", e.Key, err)
		}
	}

	got, err = s.GetTask(id)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Timing == nil {
		t.Fatal("有账目时 GetTask 必须填 Timing —— 只接线不实现会让前端显示「—」，看起来完全正常")
	}
	if got.Timing.TotalMS != 1000 || got.Timing.APIMS != 700 || got.Timing.OtherMS != 100 {
		t.Fatalf("接线后的聚合值不对: %+v", got.Timing)
	}

	// 反向断言：列表刻意不填（store.go:396 的既有纪律），配一条正面断言锁住
	// 「它搬去了 GetTask」——上面那条已经锁住了
	list, err := s.ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("应有 1 个任务，实际 %d", len(list))
	}
	if list[0].Timing != nil {
		t.Fatalf("ListTasks 不得填 Timing（每行做一次 SUM 是纯浪费），实际 %+v", list[0].Timing)
	}

	// 覆盖语义：同键重报 turn 取最终值而非累加
	if err := s.UpsertTiming(id, proto.TimingEntry{
		Key: "turn/1", Kind: proto.TimingKindTurn, Turn: 1, DurMS: 2000,
	}); err != nil {
		t.Fatalf("UpsertTiming 覆盖: %v", err)
	}
	got, err = s.GetTask(id)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Timing.TotalMS != 2000 {
		t.Fatalf("同键重报应覆盖成 2000（不是累加成 3000），实际 %d", got.Timing.TotalMS)
	}
}
```

跑红：

```bash
go test ./internal/store/ -run 'TestAggregateTiming|TestTaskTimingWiring'
```

预期：编译失败（`aggregateTiming`、`timingRow` 未定义）。**编译失败就是本步的红**，不要跳过这次运行——它证明测试确实在测新代码而不是碰巧绿。

**步骤 2 — 写聚合纯函数（最小实现）**

新建 `internal/store/timing_agg.go`：

```go
// timing_agg.go —— 耗时账本的聚合（账目行 → 三分法结果）。
//
// 职责：
//   - Store.TaskTiming：从 task_timing_ledger 取行，交给纯函数聚合
//   - aggregateTiming：纯函数，账目集合 → proto.TaskTiming
//
// 边界：
//   - **SQL 只负责取行**。求和、区间并集、排行全在纯函数里——把区间并集写进
//     SQL 会让这段逻辑失去穷举测试的可能（拆解 T6 的硬要求）
//   - **不做截断**：Detail 的 200 rune 上限由采集侧负责（UpsertTiming 的注释
//     已写死这一点，两处都以为对方管了是这类字段最常见的失守方式）
//   - 不认识任何具体 executor：四家喂进来的账目在这里已经同构
package store

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Xsxdot/handoff/internal/proto"
)

// subLabelRunes 是下钻层标签的 rune 上限。
//
// 命令首词按 P4=(b) 取前两段，但「两段」在极端命令上仍可能很长
// （`docker run --rm -v <长路径>` 的第二段就是个长路径）。排行是给人扫一眼
// 用的，一格标签撑满整行就失去了排行的意义。
const subLabelRunes = 40

// unknownToolLabel 是工具名缺席时的桶名。
//
// 不拿空串当桶名：空串在界面上是一格看不见的行，读者会以为渲染坏了。
const unknownToolLabel = "(未知工具)"

// timingRow 是账本里的一行，聚合纯函数的输入。
//
// 刻意不复用 proto.TimingEntry：那个类型带着 Key（幂等键），而聚合根本不看
// Key——把它摆进入参会让读者以为聚合对 Key 有要求。
type timingRow struct {
	Kind     string
	Turn     int
	DurMS    int64
	OffsetMS int64
	Label    string
	Detail   string
}

// span 是一次工具调用在回合内占用的区间（左闭右开，单位毫秒）。
type span struct{ start, end int64 }

// turnAcc 是单个回合的累加中间态。
type turnAcc struct {
	turnMS  int64
	apiMS   int64
	hasTurn bool
	hasAPI  bool
	spans   []span
}

// bucketAcc 是排行的累加中间态。
//
// 下钻只许一层，这条由结构保证而非靠自律：下钻层的 bucketAcc 一律以
// subs=nil 构造，于是 rankBuckets 递归到第二层自然终止。
type bucketAcc struct {
	durMS int64
	count int
	subs  map[string]*bucketAcc
}

// TaskTiming 对该任务的全部耗时账目求和，得到三分法聚合。
//
// 参数：taskID —— 任务 ID
//
// 返回：
//   - 没有任何账目行时返回 (nil, nil)。**不返回零值结构**——0 会被读成
//     「一共没花时间」，而真相是「还不知道」（与 TaskCumulative 同款纪律）
//   - 取行/扫描出错时返回 (nil, err)。**出错绝不返回 (nil, nil)**：那与
//     「没有账目」形状相同，会把一次读库失败伪装成一个正常的空账本
//
// 注意：**本方法读路径高频，成功不打日志**（与 TaskCumulative 同款）。
func (s *Store) TaskTiming(taskID string) (*proto.TaskTiming, error) {
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT kind, turn, dur_ms, offset_ms, label, detail
   FROM task_timing_ledger WHERE task_id = ?`, taskID)
	if err != nil {
		return nil, fmt.Errorf("读任务 %s 耗时账本: %w", taskID, err)
	}
	defer rows.Close()

	var acc []timingRow
	for rows.Next() {
		var r timingRow
		if err := rows.Scan(&r.Kind, &r.Turn, &r.DurMS, &r.OffsetMS, &r.Label, &r.Detail); err != nil {
			return nil, fmt.Errorf("扫描任务 %s 耗时账目: %w", taskID, err)
		}
		acc = append(acc, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历任务 %s 耗时账目: %w", taskID, err)
	}
	return aggregateTiming(acc), nil
}

// aggregateTiming 把账目行聚合成三分法结果。
//
// **纯函数**：无 I/O、无时钟、结果不依赖 map 迭代顺序（次序由显式排序决定）。
//
// 参数：rows —— 同一个任务的全部账目行，顺序无关
// 返回：聚合结果；rows 为空返回 nil（不是零值结构，理由见 TaskTiming）
//
// 三条口径（契约 §2.1，改之前先读那一节）：
//   - TotalMS = Σ kind=turn。**turn 不是段**，是 other 的分母；加进三分会让
//     总时长被计两遍且不报错
//   - ToolSpanMS 分回合求区间并集再相加：OffsetMS 是回合内相对偏移，跨回合的
//     区间不可比，混在一起并集会把两个回合的 [0,500) 并成一个 500
//   - OtherMS = max(0, Total − API − ToolSpan)。取 max 是**防御不是语义**，
//     真出现负数说明采集有 bug，此时 Partial 必为真
//
// 关于 Partial 的一个反直觉推论：**运行中的任务几乎总是 Partial**。回合开始时
// 就写了 turn 行，而模型段要到第一次 ToolStart 或 EndTurn 才关闭——所以在跑的
// 那个回合没有 api 行。这不是 bug，正是「other 此刻偏大」的诚实表达。
func aggregateTiming(rows []timingRow) *proto.TaskTiming {
	if len(rows) == 0 {
		return nil
	}

	turns := map[int]*turnAcc{}
	tools := map[string]*bucketAcc{}
	var out proto.TaskTiming

	for _, r := range rows {
		acc := turns[r.Turn]
		if acc == nil {
			acc = &turnAcc{}
			turns[r.Turn] = acc
		}
		switch proto.TimingKind(r.Kind) {
		case proto.TimingKindTurn:
			// 同一回合库里只有一行 turn（键是 turn/<turn>，按同键覆盖），
			// 所以这里赋值而不是累加——累加会把「反复刷新」读成「多个回合」
			acc.turnMS, acc.hasTurn = r.DurMS, true
		case proto.TimingKindAPI:
			acc.apiMS += r.DurMS
			acc.hasAPI = true
		case proto.TimingKindTool:
			acc.spans = append(acc.spans, span{r.OffsetMS, r.OffsetMS + r.DurMS})
			out.ToolMS += r.DurMS
			addToolRow(tools, r)
		default:
			// 未知 kind：既不 panic 也不静默当 0。库里出现本版不认识的 kind
			// 是常态（部署顺序不保证），唯一诚实的处置是「不计入任何一档，
			// 并把账目标成不全」
			out.Partial = true
		}
	}

	for _, acc := range turns {
		out.TotalMS += acc.turnMS
		out.APIMS += acc.apiMS
		out.ToolSpanMS += unionMS(acc.spans)
		// 有回合墙钟却一条模型段都没有、或有段却没有回合墙钟，两者都说明这个
		// 回合的账缺了一块，OtherMS 会因此偏大
		if !acc.hasTurn || !acc.hasAPI {
			out.Partial = true
		}
	}

	if residual := out.TotalMS - out.APIMS - out.ToolSpanMS; residual < 0 {
		out.OtherMS, out.Partial = 0, true
	} else {
		out.OtherMS = residual
	}

	out.Buckets = rankBuckets(tools)
	return &out
}

// unionMS 求区间并集的总长度。
//
// 它存在的唯一理由是并发工具：一个回合里同时发出的多个工具调用，Σdur_ms 会
// 大于它们实际占用的墙钟。拿 Σdur_ms 当墙钟用，OtherMS 会被系统性地吃成 0，
// 而且不报错。
//
// 边界：end <= start 的区间（0ms 调用、时钟回拨、脏数据）跳过而不是当成负
// 长度——负长度会把并集算小，进而把 OtherMS 算大，是一个静默错误。
func unionMS(spans []span) int64 {
	valid := make([]span, 0, len(spans))
	for _, s := range spans {
		if s.end > s.start {
			valid = append(valid, s)
		}
	}
	if len(valid) == 0 {
		return 0
	}
	sort.Slice(valid, func(i, j int) bool { return valid[i].start < valid[j].start })
	var total int64
	cur := valid[0]
	for _, s := range valid[1:] {
		if s.start > cur.end {
			total += cur.end - cur.start
			cur = s
			continue
		}
		if s.end > cur.end {
			cur.end = s.end
		}
	}
	return total + cur.end - cur.start
}

// addToolRow 把一条工具账目累加进排行（工具名一格 + 命令首词下钻一层）。
func addToolRow(tools map[string]*bucketAcc, r timingRow) {
	label := r.Label
	if label == "" {
		label = unknownToolLabel
	}
	b := tools[label]
	if b == nil {
		b = &bucketAcc{subs: map[string]*bucketAcc{}}
		tools[label] = b
	}
	b.durMS += r.DurMS
	b.count++

	head := commandHead(r.Detail)
	if head == "" {
		return // 入参不是命令：编一格 `{"path":` 出来对读者毫无价值
	}
	s := b.subs[head]
	if s == nil {
		s = &bucketAcc{} // subs 留 nil：下钻只许一层，由结构保证
		b.subs[head] = s
	}
	s.durMS += r.DurMS
	s.count++
}

// rankBuckets 把累加态排成降序排行并截断到 proto.TimingBucketCap。
//
// 排序键是 (DurMS 降序, Label 升序)。第二关键字不是美观，是**确定性**：
// Go 的 map 迭代顺序随机，只按 DurMS 排会让同耗时的两格在两次调用间换位，
// 于是契约夹具与断言会偶发翻红——而偶发红会被当噪音忽略，判据就此失效。
func rankBuckets(m map[string]*bucketAcc) []proto.TimingBucket {
	if len(m) == 0 {
		return nil
	}
	out := make([]proto.TimingBucket, 0, len(m))
	for label, acc := range m {
		out = append(out, proto.TimingBucket{
			Label: label, DurMS: acc.durMS, Count: acc.count,
			Sub: rankBuckets(acc.subs),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DurMS != out[j].DurMS {
			return out[i].DurMS > out[j].DurMS
		}
		return out[i].Label < out[j].Label
	})
	if len(out) > proto.TimingBucketCap {
		out = out[:proto.TimingBucketCap]
	}
	return out
}

// commandHead 从 Detail 取「命令首词」，作为排行下钻层的标签。
// 返回 "" 表示这条 Detail 不适合下钻（调用方跳过，不建空格子）。
//
// 三条规则（P4=(b) 的落地）：
//  1. 以 { 或 [ 开头 → ""。那是入参 JSON（非 Bash 工具的 Detail 回落成
//     compactJSON），它的「首词」是 `{"path":` 之类，对「哪条命令慢」无价值
//  2. 跳过前导的 VAR=value 环境赋值：它们不是命令，`TOKEN=… go test` 应当与
//     `go test` 落进同一格；顺带避免把赋值右边的凭据抬进排行标签
//  3. 取剩下的前两段（`go test ./...` → `go test`，把 go build/vet/test 分开），
//     再按 subLabelRunes 截断
func commandHead(detail string) string {
	s := strings.TrimSpace(detail)
	if s == "" || s[0] == '{' || s[0] == '[' {
		return ""
	}
	fields := strings.Fields(s)
	for len(fields) > 0 && isEnvAssign(fields[0]) {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return ""
	}
	head := fields[0]
	if len(fields) > 1 {
		head += " " + fields[1]
	}
	if r := []rune(head); len(r) > subLabelRunes {
		return string(r[:subLabelRunes])
	}
	return head
}

// isEnvAssign 判断一段是不是 VAR=value 形式的环境赋值。
//
// 只认 [A-Za-z_][A-Za-z0-9_]*= 这一种形状：宽一点会把 `--flag=v` 这类真正的
// 命令参数误判成赋值，从而把命令首词吃掉一段。
func isEnvAssign(f string) bool {
	eq := strings.IndexByte(f, '=')
	if eq <= 0 {
		return false
	}
	for i, c := range f[:eq] {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c == '_':
		case i > 0 && c >= '0' && c <= '9':
		default:
			return false
		}
	}
	return true
}
```

**步骤 3 — 删掉 `store.go` 里的空壳**

`internal/store/store.go:650-664` 的整段（`// TaskTiming 对该任务的全部耗时账目求和…` 注释 + `func (s *Store) TaskTiming` 的空壳）**整块删除**。注释已随实现搬进 `timing_agg.go`（含那条 `TODO(contract Ticket 0)` —— 它交待的事已经做完，**不要**把 TODO 也搬过去）。

删完跑一次编译，确认没有重复定义：

```bash
go build ./internal/store/
```

**步骤 4 — 接线 `GetTask`**

`internal/store/store.go:381-387`，在 `task.Cumulative = cum` 之后补：

```go
	// 耗时聚合同样来自另一张表，与 Cumulative 同进同出：列表刻意不带。
	tm, err := s.TaskTiming(id)
	if err != nil {
		return nil, err
	}
	task.Timing = tm
```

同时把 `ListTasks`（:392-395）的注释从「**不填充 Task.Cumulative**」改成「**不填充 Task.Cumulative 与 Task.Timing**」，并把「为每一行做一次 SUM 是纯浪费」改成「为每一行做两次 SUM 是纯浪费」。这条注释是承重的（它是那条反向断言的出处），漂了就等于没有。

**步骤 5 — 跑绿**

```bash
go test ./internal/store/ -run 'TestAggregateTiming|TestTaskTimingWiring' -count=1
```

全部通过后跑触及包全量：

```bash
go test ./internal/store/ -count=1
```

**测试范围声明**：本 task 只跑 `./internal/store/`（新增/改动全在这个包内）。`internal/proto` 的夹具在步骤 6 单独跑。**全量测试不属于本 task**，归 Task 4。

**步骤 6 — 契约夹具（拆解 §6.6 的硬要求）**

改 `internal/proto/contract_fixture_test.go`：

6a. `taskSample`（:203）在 `Usage:` 那一行之后补：

```go
		// B85/需求 A：Timing 必须进 fixture —— 「两端各自有测试」≠「这条链路有
		// 测试」，账本域两次 wire 缺陷都是这个形状（CardView.ChildrenTotal）。
		// 数字刻意造成 tool_ms > tool_span_ms（并发工具），把「两个数互不冒充」
		// 这条契约钉进线格式；三分法自洽：184300 − 121500 − 58400 = 4400。
		Timing: &TaskTiming{
			TotalMS: 184300, APIMS: 121500, ToolMS: 71200, ToolSpanMS: 58400,
			OtherMS: 4400, Partial: false,
			Buckets: []TimingBucket{
				{Label: "Bash", DurMS: 52100, Count: 9, Sub: []TimingBucket{
					{Label: "go test", DurMS: 41800, Count: 4},
					{Label: "git status", DurMS: 10300, Count: 5},
				}},
				{Label: "Read", DurMS: 19100, Count: 23},
			},
		},
```

6b. `tasksRespSample`（:378）——**这一步不能省**。它复用 `taskSample`，而 `ListTasks` 不填 Timing；不清空的话 fixture 会编码一个不存在的世界（列表响应带 `timing`），且两侧测试都会全绿地验证它：

```go
func tasksRespSample(now time.Time, taskID string) TasksResp {
	remote := taskSample(now, "9c1f0b47-2f5a-4a6e-8f3b-5d7c1e2a4b90")
	remote.Machine = "devbox"
	remote.State = "waiting_review"
	remote.Name = "B12 远端派发"
	// 列表响应由 ListTasks 产出，它**不填** Timing（store.go 的 ListTasks 注释
	// 写死了这条纪律）。夹具必须与那个事实一致——留着 taskSample 带来的
	// timing，就是让契约夹具去描述一个不存在的响应。
	remote.Timing = nil
	local := taskSample(now, taskID)
	local.Timing = nil
	return TasksResp{
		Machines: []MachineStatus{
			{Name: "", Ok: true, FetchedAt: now, Error: ""},
			{Name: "devbox", Ok: false, FetchedAt: now,
				Error: "dial tcp 10.0.0.8:7777: connect: connection refused"},
		},
		Tasks: []TaskView{
			{Task: local, Watchers: 1},
```

（`Tasks` 里第二项原本就是 `remote`，保持不变；只把第一项的 `taskSample(now, taskID)` 换成 `local`。）

6c. `frameSample`（:499）补 `DurMS`，并把文档注释里的「五个字段」改成「六个字段」：

```go
// frameSample 返回 Frame 的代表性样本（被截断的 tool_result）。
//
// 为什么选 tool_result 而不是 text：它是字段最多的一种帧，能同时钉住
// Part/Status/Output/Truncated/Bytes/DurMS 六个字段的序列化结果；text 帧只有
// Part+Delta，钉不住 omitempty 的边界。
//
// DurMS 只出现在 tool_result（契约 §2.5）：tool_call 上带 dur_ms 是无意义的，
// 那条反向断言在 frames_test.go 与 web 的 frames.test.ts 里各锁一次。
func frameSample(now time.Time) Frame {
	return Frame{
		Seq:       42,
		TS:        now,
		Turn:      2,
		Type:      FrameToolResult,
		Part:      "toolu_01ABCdefGHIjklMNOpqrs",
		Status:    "error",
		Output:    "go: downloading …\n…（已截断）…\nFAIL\texit status 1",
		Truncated: true,
		Bytes:     193422,
		DurMS:     1500,
	}
}
```

6d. 重生成并检视 diff：

```bash
go test ./internal/proto/ -run TestContractFixtures -update -count=1
git diff --stat web/src/api/testdata/
```

预期变更**恰好三个文件**：`Task.json`（新增 `timing`）、`Frame.json`（新增 `dur_ms`）、`TasksResp.json`（**无 `timing` 键**）。若 `TasksResp.json` 里出现了 `timing`，说明 6b 没做对，**当场停下修**，别提交。

再跑一次不带 `-update` 确认逐字节稳定：

```bash
go test ./internal/proto/ -count=1
```

**步骤 7 — 日志与注释自查**

- **日志**：本 task **刻意不加任何日志**。理由要写进代码注释（已写在 `TaskTiming` 的注释里）：这是读路径高频方法，与 `TaskCumulative` 同级，成功打日志会刷屏；错误路径全部走 `fmt.Errorf` 带上下文（任务 ID + 环节）向上抛，由调用方决定记不记。**这不是漏了，是与既有做法对齐的显式决定。**
- **注释**：`timing_agg.go` 文件头已写职责 + 边界；`TaskTiming` / `aggregateTiming` / `unionMS` / `rankBuckets` / `commandHead` / `isEnvAssign` 六个函数都有参数/返回/注意事项；四处「为什么」注释（turn 赋值不累加、跨回合并集、未知 kind 的处置、排序第二关键字）已就位。

**步骤 8 — 提交**

```bash
gofmt -l internal/store internal/proto   # 必须无输出
git add -A && git commit -m "feat(store): 耗时账本三分法聚合与 GetTask 接线（T6）"
```

---

## 4. Task 2 · T7 纯函数层：`dur_ms` 进 `ToolBlock` + `formatDuration`（`d_web`）

**Interfaces**

- Consumes：`Frame.dur_ms?: number`（`web/src/api/types.ts:624`，缺席=未知）。
- Produces：
  - `ToolBlock` 新增 `durMS?: number`（`web/src/app/task/frames.ts`）；
  - `export function formatDuration(ms: number): string`（`web/src/app/lib/format.ts`）。
- 对 Task 3 的承诺：`ToolCard` 拿 `block.durMS`，`TimingChip` 拿 `formatDuration`。

### 步骤

**步骤 1 — 先写失败测试（纯函数，穷举）**

在 `web/src/app/lib/format.test.ts` 末尾追加：

```ts
describe('formatDuration', () => {
  it('毫秒档：不足一秒直接给 ms', () => {
    expect(formatDuration(0)).toBe('0ms')
    expect(formatDuration(1)).toBe('1ms')
    expect(formatDuration(999)).toBe('999ms')
  })
  it('秒档：保留一位小数', () => {
    expect(formatDuration(1000)).toBe('1.0s')
    expect(formatDuration(1500)).toBe('1.5s')
    expect(formatDuration(59_940)).toBe('59.9s')
  })
  it('分档与时档：升档判据用四舍五入后的值', () => {
    // 59.95s 四舍五入是 60.0s —— 一个永远不该出现的读数（那已经是 1m0s）
    expect(formatDuration(59_950)).toBe('1m0s')
    expect(formatDuration(60_000)).toBe('1m0s')
    expect(formatDuration(90_000)).toBe('1m30s')
    expect(formatDuration(3_599_000)).toBe('59m59s')
    expect(formatDuration(3_600_000)).toBe('1h0m')
    expect(formatDuration(7_500_000)).toBe('2h5m')   // 125 分整 → 2h5m
    expect(formatDuration(7_530_000)).toBe('2h6m')   // 125.5 分四舍五入到 126
  })
  it('负数夹到 0：调用方用「缺席」表达未知，不用负数', () => {
    expect(formatDuration(-1)).toBe('0ms')
  })
})
```

（`formatDuration` 要加进该文件顶部的 `import { … } from './format'`。）

在 `web/src/app/task/frames.test.ts` 的 `describe('buildBlocks 工具配对', …)` 内追加三条：

```ts
  it('dur_ms 从 tool_result 带进 ToolBlock', () => {
    const blocks = buildBlocks([
      f({ seq: 1, type: 'tool_call', part: 'p04', tool: 'bash', input: 'go test ./...' }),
      f({ seq: 2, type: 'tool_result', part: 'p04', status: 'ok', output: 'ok', dur_ms: 1500 }),
    ])
    expect((blocks[0] as ToolBlock).durMS).toBe(1500)
  })

  // 反向断言（tool_call 上的 dur_ms 无意义，契约 §2.5）+ 配套的正面断言，
  // 锁住「它只从 tool_result 来」而不是「它从哪都不来」。
  it('tool_call 上的 dur_ms 被忽略，只认 tool_result 的', () => {
    const blocks = buildBlocks([
      f({ seq: 1, type: 'tool_call', part: 'p05', tool: 'bash', input: 'ls', dur_ms: 999_999 }),
    ])
    expect((blocks[0] as ToolBlock).durMS).toBeUndefined()

    const paired = buildBlocks([
      f({ seq: 1, type: 'tool_call', part: 'p05', tool: 'bash', input: 'ls', dur_ms: 999_999 }),
      f({ seq: 2, type: 'tool_result', part: 'p05', status: 'ok', output: '', dur_ms: 42 }),
    ])
    expect((paired[0] as ToolBlock).durMS).toBe(42)
  })

  it('没报耗时时是 undefined 而不是 0（0ms 与「没报」不能混）', () => {
    const blocks = buildBlocks([
      f({ seq: 1, type: 'tool_call', part: 'p06', tool: 'bash', input: 'ls' }),
      f({ seq: 2, type: 'tool_result', part: 'p06', status: 'ok', output: 'a' }),
    ])
    expect((blocks[0] as ToolBlock).durMS).toBeUndefined()
    // 结果先到的那条路径同样不许把缺席写成 0
    const first = buildBlocks([f({ seq: 1, type: 'tool_result', part: 'p07', status: 'ok', output: 'a' })])
    expect((first[0] as ToolBlock).durMS).toBeUndefined()
  })
```

跑红：

```bash
cd web && npx vitest run src/app/lib/format.test.ts src/app/task/frames.test.ts
```

预期红（`formatDuration` 不存在、`durMS` 不在类型上）。

**步骤 2 — `formatDuration` 实现**

`web/src/app/lib/format.ts` 末尾追加：

```ts
// formatDuration 把毫秒折算成人眼可读的短串。
//
// 四档：<1s → `340ms`；<1min → `12.3s`；<1h → `4m17s`；否则 `2h5m`。
// 越大的量级越不需要精度——读者在小时档问的是「跑了两个多小时」，
// 而秒档要能分辨 1.2s 与 1.9s。
//
// 边界：
//   - 升档判据用**四舍五入后**的值，否则 59_950ms 会显示成 `60.0s`，
//     一个永远不该出现的读数（与 formatTokens 的既有纪律同源）
//   - 负数夹到 0。**「未知」用缺席表达，不用负数**——线格式上 dur_ms 缺席即
//     未知（omitempty），调用方在缺席时根本不该调本函数
export function formatDuration(ms: number): string {
  const v = Math.max(0, ms)
  if (v < 1000) return `${Math.round(v)}ms`
  const s = v / 1000
  if (Math.round(s * 10) / 10 < 60) return `${(Math.round(s * 10) / 10).toFixed(1)}s`
  const totalSec = Math.round(s)
  if (totalSec < 3600) return `${Math.floor(totalSec / 60)}m${totalSec % 60}s`
  const totalMin = Math.round(totalSec / 60)
  return `${Math.floor(totalMin / 60)}h${totalMin % 60}m`
}
```

**步骤 3 — `ToolBlock` 加字段并接线**

`web/src/app/task/frames.ts`：

3a. `Block` 联合里的 `tool` 分支（:86-101），在 `outputBytes: number` 之后补：

```ts
      // durMS 是这次调用的耗时（毫秒）。**缺席 = 没报出耗时，不是 0ms**：
      // 线格式上 dur_ms 是 omitempty，0 与未知不可分（契约 §2.5 的既有取舍），
      // 所以渲染层只做「有就显示」，绝不把缺席画成 0ms
      durMS?: number
```

3b. `case 'tool_result'`（:180-200）两条路径各补一行：

```ts
      case 'tool_result': {
        const k = `${turn}/${fr.part ?? ''}`
        const hit = tools.get(k)
        if (hit) {
          hit.status = fr.status ?? ''
          hit.output = fr.output ?? ''
          hit.outputTruncated = fr.truncated ?? false
          hit.outputBytes = fr.bytes ?? 0
          hit.durMS = fr.dur_ms
          break
        }
        const b: ToolBlock = {
          kind: 'tool', key, turn,
          tool: '', input: '',
          inputTruncated: false, inputBytes: 0,
          status: fr.status ?? '', output: fr.output ?? '',
          outputTruncated: false, outputBytes: fr.bytes ?? 0,
          durMS: fr.dur_ms,
        }
```

（注意：`?? 0` 兜底**不要**加在 `durMS` 上——那正是「缺席变成 0」的入口。）

3c. `case 'tool_call'` **不动**。它今天就没读 `fr.dur_ms`，反向断言锁的就是这一点。在该分支上方补一行注释说明这是刻意的：

```ts
      case 'tool_call': {
        // 刻意不读 fr.dur_ms：耗时只在 tool_result 上有意义（契约 §2.5）。
        // 这里顺手读一下不会报错，但会让「调用刚发出就显示耗时 999s」成为可能
        const k = `${turn}/${fr.part ?? ''}`
```

**步骤 4 — 跑绿**

```bash
cd web && npx tsc -b && npx vitest run src/app/lib/format.test.ts src/app/task/frames.test.ts
```

**测试范围声明**：本 task 只跑上面两个测试文件 + `tsc -b`（类型改动会波及全仓，`tsc` 是最便宜的全局网）。整包 vitest 归 Task 4。

**步骤 5 — 注释自查**

`durMS` 字段有「缺席≠0」说明；`formatDuration` 有参数/返回/四档规则/两条边界；`tool_call` 分支有「为什么刻意不读」。**日志**：前端纯函数层不打日志（仓库既有做法：`frames.ts` / `format.ts` 全文无 console），本 task 无新增日志——这是与既有做法一致的显式决定。

**步骤 6 — 提交**

```bash
git add -A && git commit -m "feat(web): tool_result 的 dur_ms 进 ToolBlock，新增 formatDuration（T7-1）"
```

---

## 5. Task 3 · T7 组件层：`ToolCard` 耗时 + `TimingChip` 面板 + TS 契约断言（`d_web`）

**Interfaces**

- Consumes：`ToolBlock.durMS?`、`formatDuration`（Task 2 产出）；`TaskTiming` / `TimingBucket`（`web/src/api/types.ts:631,646`）；`Task.timing?`（`:39`）；`Task.json` / `Frame.json` 夹具（Task 1 产出）。
- Produces：`TimingChip`（`web/src/app/task/TimingChip.tsx`），挂进 `TuiHeader` 的遥测行。

P5=(a)：**独立 chip**，不并进 `UsageChip`。理由（拆解 §5.5）：耗时与 token 是两件事，塞进同一个弹出会让 `UsageChip` 那条「两者都缺席时整体不渲染」的规则变成三元判断。

### 步骤

**步骤 1 — 先写失败测试**

新建 `web/src/app/task/TimingChip.test.tsx`：

```tsx
// TimingChip 的行为测试。重点三条：缺席时不渲染、partial 必须读得出来、
// tool_ms 与 tool_span_ms 同时可见（取其一当另一个用就是在撒谎）。
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { TimingChip } from './TimingChip'
import type { TaskTiming } from '../../api/types'

const timing: TaskTiming = {
  total_ms: 184_300, api_ms: 121_500, tool_ms: 71_200, tool_span_ms: 58_400,
  other_ms: 4_400, partial: false,
  buckets: [
    { label: 'Bash', dur_ms: 52_100, count: 9, sub: [
      { label: 'go test', dur_ms: 41_800, count: 4 },
      { label: 'git status', dur_ms: 10_300, count: 5 },
    ] },
    { label: 'Read', dur_ms: 19_100, count: 23 },
  ],
}

function openPanel(t: TaskTiming = timing) {
  render(
    <div>
      <button type="button">外部</button>
      <TimingChip timing={t} />
    </div>,
  )
  fireEvent.click(screen.getByRole('button', { name: /耗时/ }))
}

describe('TimingChip', () => {
  it('timing 缺席时整体不渲染，不画空表', () => {
    const { container } = render(<TimingChip />)
    expect(container).toBeEmptyDOMElement()
  })

  it('折叠态给总时长', () => {
    render(<TimingChip timing={timing} />)
    expect(screen.getByRole('button', { name: /耗时 3m4s/ })).toBeInTheDocument()
  })

  it('展开后三分法各档可读', () => {
    openPanel()
    expect(screen.getByText('模型')).toBeInTheDocument()
    expect(screen.getByText('2m2s')).toBeInTheDocument()   // api 121.5s
    expect(screen.getByText('58.4s')).toBeInTheDocument()  // tool_span
    expect(screen.getByText('4.4s')).toBeInTheDocument()   // other
  })

  it('tool_ms > tool_span_ms 时两个数同时可见，不取其一', () => {
    openPanel()
    expect(screen.getByText('58.4s')).toBeInTheDocument()  // 墙钟跨度
    expect(screen.getByText('1m11s')).toBeInTheDocument()  // 时长合计 71.2s
  })

  it('partial=true 时能读出「未归类偏大」', () => {
    openPanel({ ...timing, partial: true })
    expect(screen.getByText(/账目不全/)).toBeInTheDocument()
    expect(screen.getByText(/未归类偏大/)).toBeInTheDocument()
  })

  it('partial=false 时不出现那句提示（不制造无谓的警报）', () => {
    openPanel()
    expect(screen.queryByText(/账目不全/)).not.toBeInTheDocument()
  })

  it('排行列出工具名与下钻的命令首词', () => {
    openPanel()
    expect(screen.getByText('Bash')).toBeInTheDocument()
    expect(screen.getByText('go test')).toBeInTheDocument()
    expect(screen.getByText('git status')).toBeInTheDocument()
    expect(screen.getByText('Read')).toBeInTheDocument()
  })

  it('没有 buckets 时不画排行区（历史任务/刚起步的任务）', () => {
    openPanel({ ...timing, buckets: undefined })
    expect(screen.queryByText('工具排行')).not.toBeInTheDocument()
  })

  // 关闭路径拆成两条：同一个 it 里 render 两次会让文档里出现两个「耗时」按钮，
  // openPanel 的 getByRole 当场抛「multiple elements」——那是测试自己的 bug，
  // 排查起来却长得像组件坏了
  it('点外部关掉浮层', () => {
    openPanel()
    fireEvent.mouseDown(screen.getByText('外部'))
    expect(screen.queryByText('模型')).not.toBeInTheDocument()
  })

  it('按 Esc 关掉浮层', () => {
    openPanel()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByText('模型')).not.toBeInTheDocument()
  })
})
```

在 `web/src/app/task/TuiHeader.test.tsx` 追加两条（`task` 常量不动，用 spread 注入 `timing`）：

```tsx
  it('遥测行：有 timing 时挂出耗时 chip', () => {
    const withTiming = {
      ...base,
      task: { ...task, timing: {
        total_ms: 184_300, api_ms: 121_500, tool_ms: 71_200,
        tool_span_ms: 58_400, other_ms: 4_400, partial: false,
      } } as unknown as Task,
    }
    render(<TuiHeader {...withTiming} />)
    expect(screen.getByRole('button', { name: /耗时 3m4s/ })).toBeInTheDocument()
  })

  // 分隔点跟着 chip 一起有无：只断言「没有耗时二字」证明不了没留下悬空的「·」，
  // 所以直接数点。两次 render 各读自己的 container，互不干扰
  it('分隔点跟着耗时 chip 一起有无，不留悬空的「·」', () => {
    const dots = (t: string) => (t.match(/·/g) ?? []).length
    const plain = render(<TuiHeader {...base} />).container.textContent ?? ''
    expect(plain).not.toContain('耗时')

    const withTiming = render(
      <TuiHeader {...base} task={{ ...task, timing: {
        total_ms: 1000, api_ms: 700, tool_ms: 200,
        tool_span_ms: 200, other_ms: 100, partial: false,
      } } as unknown as Task} />,
    ).container.textContent ?? ''
    expect(withTiming).toContain('耗时')
    expect(dots(withTiming)).toBe(dots(plain) + 1)
  })
```

`ToolCard` 的测试**已经有了**——在 `web/src/app/task/blocks.test.tsx` 的 `describe('ToolCard', …)` 里，并且那里已有一个 `tool(o: Partial<ToolBlock>)` 工厂（:16）。**不要新建 `ToolCard.test.tsx`**，在既有 describe 末尾追加两条即可（工厂的默认 `input` 是 `'go test ./...'` 而非 JSON，`argSummary` 会原样当摘要用）：

```tsx
  it('有 durMS 时折叠态显示单次耗时', () => {
    render(<ToolCard block={tool({ durMS: 1500 })} taskState="completed" />)
    expect(screen.getByText('1.5s')).toBeInTheDocument()
  })

  it('durMS 缺席时一个字都不多画，尤其不画 0ms', () => {
    render(<ToolCard block={tool({})} taskState="completed" />)
    expect(screen.queryByText('0ms')).not.toBeInTheDocument()
    expect(screen.queryByText(/^\d+(\.\d+)?(ms|s)$/)).not.toBeInTheDocument()
  })
```

（`status: null` 的卡不需要单独用例：`durMS` 只从 `tool_result` 帧来，没有结果就必然没有耗时——这是结构保证，不是 `ToolCard` 的判断，给它写用例等于测一个不可能到达的状态。）

最后在 `web/src/api/contract.test.ts` 补契约断言（**拆解 §6.6 点名「本卡最容易漏的一项」**）。

在 `it('Task：可解析为 Task 类型，关键字段齐全', …)` 的字段清单里追加 `'timing'`，并在该 `it` 之后新增：

```ts
  it('Task.timing：三分法自洽，tool_ms 与 tool_span_ms 互不冒充', () => {
    const task: Task = taskFixture
    const t = task.timing!
    expect(t).toBeDefined()
    // 线格式上的三分法必须自洽，否则前端画出来的条形图是假的
    expect(t.total_ms - t.api_ms - t.tool_span_ms).toBe(t.other_ms)
    // 并发工具的形状：Σ时长 > 墙钟跨度。夹具刻意造成这一条，
    // 因为「取其一当另一个用」是这个契约唯一会静默出错的地方
    expect(t.tool_ms).toBeGreaterThan(t.tool_span_ms)
    expect(t.partial).toBe(false)
    // 下钻只有一层：sub 的 sub 必须缺席
    const bash = t.buckets!.find((b) => b.label === 'Bash')!
    expect(bash.sub!.map((s) => s.label)).toEqual(['go test', 'git status'])
    for (const s of bash.sub!) expect(s.sub).toBeUndefined()
  })

  // 注意 TS 侧 TasksResp.tasks 是扁平的 Task[]（Go 的 TaskView 内嵌 Task，
  // JSON 把它摊平了），不是 { task, watchers } 的嵌套形状
  it('TasksResp 的任务不带 timing（ListTasks 不填，夹具必须与那个事实一致）', () => {
    const resp = tasksRespFixture as TasksResp
    expect(resp.tasks.length).toBeGreaterThan(0)
    for (const t of resp.tasks) {
      expect(Object.keys(t)).not.toContain('timing')
    }
  })
```

在 `describe('W4a 帧契约', …)` 的第一个 `it` 里补一行正面断言：

```ts
    expect(f.dur_ms).toBe(1500)
```

跑红：

```bash
cd web && npx vitest run src/app/task/TimingChip.test.tsx src/app/task/TuiHeader.test.tsx src/api/contract.test.ts
```

**步骤 2 — `TimingChip` 实现**

新建 `web/src/app/task/TimingChip.tsx`：

```tsx
// TimingChip —— 页头的任务级耗时 chip + 三分法弹出（需求 A · T7）。
//
// 职责：一眼读总时长，点开看「模型 / 工具 / 未归类」三档与工具排行。
// 边界：
//   - timing 缺席时整体不渲染（返回 null）：没有账目不画空表，与 UsageChip 同款
//   - **不并进 UsageChip**（P5=(a)）：耗时问「花了多少时间」，ctx 问「花了多少
//     钱」，两组数字挤在一个弹出里互相干扰，还会把 UsageChip 那条「都缺席就
//     不渲染」的二元规则变成三元
//   - **tool_ms 与 tool_span_ms 必须同时显示**。前者是各次调用的时长之和，
//     后者是它们占用的墙钟；并发工具时前者更大。取其一当另一个用就是在撒谎
//   - partial=true 时必须说出「未归类偏大」，不得把 other_ms 当真实空档
import { useEffect, useRef, useState } from 'react'
import type { TaskTiming, TimingBucket } from '../../api/types'
import { formatDuration } from '../lib/format'

// Row 是弹出里的一行「名称 —— 时长」。
function Row({ label, ms, hint }: { label: string; ms: number; hint?: string }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="text-right font-mono">
        {formatDuration(ms)}
        {hint && <span className="ml-1 font-sans text-[10px] text-muted-foreground">{hint}</span>}
      </dd>
    </>
  )
}

// BucketRow 渲染排行的一格及其下钻层。下钻只有一层，所以这里刻意**不递归**
// ——写成递归组件等于给「将来加第三层」开了门，而那条规则不在契约里。
function BucketRow({ b }: { b: TimingBucket }) {
  return (
    <li>
      <div className="flex items-baseline gap-2">
        <span className="min-w-0 flex-1 truncate">{b.label}</span>
        <span className="shrink-0 text-[10px] text-muted-foreground">×{b.count}</span>
        <span className="shrink-0 font-mono">{formatDuration(b.dur_ms)}</span>
      </div>
      {b.sub && b.sub.length > 0 && (
        <ul className="ml-3 border-l pl-2 text-[11px] text-muted-foreground">
          {b.sub.map((s) => (
            <li key={s.label} className="flex items-baseline gap-2">
              <span className="min-w-0 flex-1 truncate font-mono">{s.label}</span>
              <span className="shrink-0 text-[10px]">×{s.count}</span>
              <span className="shrink-0 font-mono">{formatDuration(s.dur_ms)}</span>
            </li>
          ))}
        </ul>
      )}
    </li>
  )
}

// TimingChip 渲染任务级耗时。timing 缺席即不渲染。
export function TimingChip({ timing }: { timing?: TaskTiming }) {
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLSpanElement>(null)

  // 点外部 / Esc 关闭。挂 mousedown 而非 click：click 要等按键抬起，
  // 期间浮层还盖在你正要点的东西上面，点击会先被浮层吃掉一次。
  //
  // 这个 effect 必须在下面的 `return null` **之前**——早退在它后面的话，
  // 账目从有到无的那一帧 hook 数量会变，React 直接报错（UsageChip 同款）。
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  if (!timing) return null

  return (
    <span ref={rootRef} className="relative">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="inline-flex items-center gap-1.5 hover:text-foreground"
      >
        耗时 {formatDuration(timing.total_ms)}
      </button>
      {open && (
        <div className="absolute left-0 top-6 z-10 w-72 rounded-lg border bg-background p-3 text-xs shadow-lg">
          <div className="mb-1 font-semibold">耗时三分</div>
          <dl className="mb-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
            <Row label="模型" ms={timing.api_ms} />
            <Row label="工具（墙钟）" ms={timing.tool_span_ms} />
            <Row label="工具（时长合计）" ms={timing.tool_ms} hint="并发时大于墙钟" />
            <Row label="未归类" ms={timing.other_ms} hint="排队/等审批/框架开销" />
            <Row label="合计" ms={timing.total_ms} />
          </dl>
          {timing.partial && (
            <p className="mb-2 text-[11px] text-amber-600 dark:text-amber-500">
              账目不全：有回合缺少分段条目（多半是还在跑，或 executor 中途退出），
              未归类偏大，别把它当成真实空档。
            </p>
          )}
          {timing.buckets && timing.buckets.length > 0 && (
            <>
              <div className="mb-1 font-semibold">工具排行</div>
              <ul className="flex flex-col gap-0.5">
                {timing.buckets.map((b) => (
                  <BucketRow key={b.label} b={b} />
                ))}
              </ul>
            </>
          )}
        </div>
      )}
    </span>
  )
}
```

**步骤 3 — `ToolCard` 显示单次耗时**

`web/src/app/task/ToolCard.tsx`：顶部 import 补 `formatDuration`：

```tsx
import { formatDuration } from '../lib/format'
```

折叠态那一行的状态徽章**之前**插入耗时（放状态左边：状态是这一行的收尾，耗时是修饰它的）：

```tsx
        <span className="min-w-0 flex-1 truncate font-mono">{argSummary(block.input)}</span>
        {/* 耗时缺席就一个字都不画：0ms 与「没报」在线格式上不可分（契约 §2.5），
            画一个 0ms 出来会让「极快」与「没测到」变成同一句话 */}
        {block.durMS !== undefined && (
          <span className="shrink-0 font-mono text-[11px]">{formatDuration(block.durMS)}</span>
        )}
        <span className="shrink-0 text-[11px]">{STATE_LABEL[st]}</span>
```

同时把文件头「职责」段的第一条改成：

```
//   - 折叠态显示工具名、参数摘要、单次耗时（有才显示）、状态徽章
```

**步骤 4 — 挂进 `TuiHeader`**

`web/src/app/task/TuiHeader.tsx`：

4a. import 补 `import { TimingChip } from './TimingChip'`。

4b. 遥测行的 `<UsageChip … />` 之后补：

```tsx
        <UsageChip usage={task.usage} cumulative={task.cumulative} />
        {/* 分隔点跟着 chip 一起有无。TimingChip 自己在 timing 缺席时返回 null，
            这里再判一次是为了不留下一个悬空的「·」——上面 UsageChip 那个点是
            既有的同类瑕疵，不在本卡范围内，别顺手改（它牵动 UsageChip 的测试） */}
        {task.timing && <span className="opacity-50">·</span>}
        <TimingChip timing={task.timing} />
```

同时把文件头「职责」段的遥测行描述改成：`（executor·模型·回合下拉·ctx·耗时）`。

**步骤 5 — 跑绿**

```bash
cd web && npx tsc -b && npx vitest run src/app/task src/api
```

**测试范围声明**：本 task 跑 `src/app/task` 与 `src/api` 两个目录（改动全在其中）+ `tsc -b`。整仓 vitest 与 Go 全量归 Task 4。

**步骤 6 — 日志与注释自查**

- **日志**：前端组件层不打日志（仓库既有做法，`UsageChip` / `ToolCard` 全文无 console）。**显式决定，不是漏了**。
- **注释**：`TimingChip.tsx` 文件头有职责 + 四条边界；`Row` / `BucketRow` / `TimingChip` 各有说明；「effect 必须在 return null 之前」「BucketRow 刻意不递归」「耗时缺席一个字都不画」「分隔点为什么再判一次」四处「为什么」已就位。

**步骤 7 — 提交**

```bash
git add -A && git commit -m "feat(web): 工具卡单次耗时与页头耗时面板（T7-2）"
```

---

## 6. Task 4 · 收口

**步骤 1 — 全量测试（三段律的法定位置）**

```bash
gofmt -l . | grep -v '^web/' ; echo "gofmt 退出码 $?"
go build ./... && go test ./... 2>&1 | grep -v '^ok' | head -40
cd web && npx tsc -b && npx vitest run && npm run lint
```

`gofmt -l` 必须**无输出**（executor 的账本会写「测试全绿」但漏跑 gofmt，这一步是审核者必查项）。

**步骤 2 — 契约夹具二次确认**

```bash
go test ./internal/proto/ -run TestContractFixtures -count=1
git status --short web/src/api/testdata/
```

第二条必须无输出：夹具已在 Task 1 提交，此刻不该再有未提交变更（有的话说明 `-update` 的结果没随 Task 1 一起提交）。

**步骤 3 — 变异测试（本计划的验收判据，不是可选动作）**

逐条施加变异、跑指定测试、确认**变红**，然后**还原**。任何一条不红 = 该断言没有牙齿，必须补测试而不是放过。

| # | 变异 | 位置 | 跑什么 | 必须红 |
|---|---|---|---|---|
| 1 | `out.ToolSpanMS += unionMS(acc.spans)` 改成 `+= 0` 之外的形式：把 `unionMS(acc.spans)` 换成对 spans 长度求和 | `timing_agg.go` | `go test ./internal/store/ -run TestAggregateTimingConcurrentTools` | ✅ |
| 2 | `acc.turnMS, acc.hasTurn = r.DurMS, true` 改成 `acc.turnMS += r.DurMS` | 同上 | `-run TestTaskTimingWiring` | ✅ |
| 3 | `default: out.Partial = true` 改成 `default:`（空） | 同上 | `-run TestAggregateTimingPartialUnknownKind` | ✅ |
| 4 | `rankBuckets` 的第二关键字 `out[i].Label < out[j].Label` 改成 `return false` | 同上 | `-run TestAggregateTimingDeterministicOrder` | ✅ |
| 5 | 截断 `out = out[:proto.TimingBucketCap]` 整行删掉 | 同上 | `-run TestAggregateTimingBucketCap` | ✅ |
| 6 | `GetTask` 里 `task.Timing = tm` 整行删掉 | `store.go` | `-run TestTaskTimingWiring` | ✅ |
| 7 | 在 `ListTasks` 的循环里补一句填 `Timing`（模拟「顺手优化」） | `store.go` | `-run TestTaskTimingWiring` | ✅ |
| 8 | `hit.durMS = fr.dur_ms` 改成 `hit.durMS = fr.dur_ms ?? 0` | `frames.ts` | `vitest run src/app/task/frames.test.ts` | ✅ |
| 9 | `case 'tool_call'` 分支里补 `durMS: fr.dur_ms` | `frames.ts` | 同上 | ✅ |
| 10 | `ToolCard` 里 `{block.durMS !== undefined && (…)}` 整块删掉 | `ToolCard.tsx` | `vitest run src/app/task/blocks.test.tsx` | ✅ |
| 11 | `ToolCard` 的条件改成 `{true && …}` 并把 `formatDuration(block.durMS)` 换成 `formatDuration(block.durMS ?? 0)`（模拟「顺手兜底」） | 同上 | 同上 | ✅ |
| 12 | `TimingChip` 里「工具（时长合计）」那一行删掉 | `TimingChip.tsx` | `vitest run src/app/task/TimingChip.test.tsx` | ✅ |
| 13 | `{timing.partial && (…)}` 改成 `{false && (…)}` | 同上 | 同上 | ✅ |
| 14 | `if (!timing) return null` 改成 `if (false) return null` | 同上 | 同上 | ✅ |
| 15 | `taskSample` 的 `Timing` 整块删掉 | `contract_fixture_test.go` | `go test ./internal/proto/` **与** `vitest run src/api/contract.test.ts` | ✅ 两边都红 |
| 16 | `tasksRespSample` 里 `local.Timing = nil` 与 `remote.Timing = nil` 删掉 | 同上 | 同上 | ✅ 两边都红 |

变异 15/16 是拆解 §6.6 那条「两端各自有测试 ≠ 这条链路有测试」的直接检验：**两边都必须红**，只红一边说明链路上有一段没被锁住。

**账本必须逐条记录变异编号 + 实际观察到的失败输出首行**，不许只写「已做变异测试」。

**步骤 4 — 最终代码审阅清单**（全局规范 §5，逐项确认后写进账本）

完成目标 / 架构一致 / 文件头注释 / 方法注释 / 中文注释 / 合理日志 / 无跨层调用 / 跨模块走 Facade / 优先复用 / 无硬编码 / 事务透传。

其中两项本卡有特殊情况，**必须在账本里写明理由而不是打勾了事**：

- **合理日志**：Task 1/2/3 均**无新增日志**，理由见各自的步骤（读路径高频、前端不打 console）。
- **事务透传**：`TaskTiming` 用 `s.db.QueryContext`，与同文件的 `TaskCumulative` 逐字一致。本包不是 DAO 分层（无 `mvc.ExtractDB`），照既有做法。

---

## 7. 四项检查（plan 出稿自审）

### 7.1 缺陷族对抗审查

拆解 §6 已逐族审过并把结论分派进各卡。本计划复核其中**落在 T6/T7 身上的六条**，逐条指认它变成了哪个步骤：

| 族 | 本计划的落点 |
|---|---|
| 生命周期 / 状态机中断 | 半条 tool 记录 → 该回合 `Σtool` 偏小、`Partial=true`。→ Task 1 步骤 1 的 `TestAggregateTimingPartialMissingAPI`；且 `aggregateTiming` 的注释写明「运行中的任务几乎总是 Partial，这不是 bug」 |
| 静默失败 / 误导报错 | 三处：① 聚合出错**必须返回 error，绝不返回 (nil,nil)**（`TaskTiming` 注释写死，三处 `fmt.Errorf` 带任务 ID）；② 「只接线不实现」的假象由 `TestTaskTimingWiring` 的「有账目时 Timing 不得为 nil」直接拦；③ 变异 6 检验这条断言有牙齿 |
| 跨平台假设 | **无，因为**本次前端改动的表面只有文本渲染与数字格式化，不碰剪贴板/cookie/拖放/下载——三类已实证的 webview 差异均不适用（拆解 §6.3 的结论，本计划不推翻）。后端只有 SQLite + 整数运算，无平台差异 |
| 假红 / 假绿测试 | ① **无时钟依赖**：T6/T7 全是纯函数 + 常量夹具，不取 `time.Now`，因此不存在 T1 那类计时竞态；② **五条反向断言各配一条正面断言**：`tool_call` 无 dur_ms ↔ `tool_result` 有 42；`ListTasks` 不填 ↔ `GetTask` 填；被截断的是最小的 ↔ 留下的末位是 T06；`TasksResp` 无 timing ↔ `Task` 有 timing；sub 的 sub 缺席 ↔ sub 有两格；③ **排序确定性**由 `TestAggregateTimingDeterministicOrder` 跑 50 次锁住，防 map 迭代顺序造成的偶发红；④ 整份计划的验收判据是**步骤 3 的 16 条变异**，不是「测试通过」 |
| 门禁绕过 | **无，因为**本次不新增任何用户可触发的写路径或执行路径。T6 是纯读，读路径复用 `GetTask` 的既有鉴权（与 `Cumulative` 同一条）；T7 是纯渲染 |
| 凭据边界（本项目特有） | `Detail` 的命令文本会经 `commandHead` 变成排行标签**上网**（今天它只在 SQLite 里）。两条处置：① 跳过 `VAR=value` 前缀，不把赋值右边抬进标签；② 标签截到 40 rune。**不是新的暴露类**——同一份命令原文今天已经通过 `frames.jsonl` 渲染在 `ToolCard` 的输入区里 |

### 7.2 序列化边界设问

`Task.Timing` / `Frame.DurMS` 从产生到消费的**每一处手写序列化/投影**：

| 环节 | 手写投影？ | 本计划的断言 |
|---|---|---|
| SQLite 行 → `timingRow` | **是**（`rows.Scan` 的列顺序） | 列顺序与 `SELECT` 同处一屏；`TestTaskTimingWiring` 走真库，列错位会当场把值串到别的字段 |
| `timingRow` → `proto.TaskTiming` | **是**（聚合） | Task 1 步骤 1 的九个纯函数用例 |
| `proto.Task.Timing` → REST JSON | 否（`encoding/json` tag） | **但必须进夹具** → Task 1 步骤 6a + 变异 13 |
| REST JSON → TS `TaskTiming` | **是**（手写 interface） | `contract.test.ts` 的两条新用例（强类型承接 + 三分法自洽）→ 变异 13 |
| `ListTasks` → `TasksResp` | 否，**但夹具会撒谎** | Task 1 步骤 6b + 变异 14 |
| `Frame` → `frames.jsonl` → TS `Frame` | 否 + 手写 interface | `Frame.json` 夹具（步骤 6c）+ `contract.test.ts` 的 `expect(f.dur_ms).toBe(1500)` |
| TS `Frame` → `ToolBlock` | **是**（`buildBlocks`） | Task 2 步骤 1 的三条用例 + 变异 8/9 |
| `ToolBlock` → `ToolCard` DOM | **是**（渲染） | `blocks.test.tsx` 的 ToolCard 两条 + 变异 10/11 |
| `TaskTiming` → `TimingChip` DOM | **是**（渲染） | `TimingChip.test.tsx` 十条 + 变异 12/13/14 |

**穿过真实序列化边界的回归测试**：`TestContractFixtures`（Go 真序列化 → JSON 文件）+ `contract.test.ts`（同一批 JSON 文件 → TS 类型），两边读**同一份**产物。**用可空类型区分「字段缺失」与「值为零」**：Go 侧 `*TaskTiming` + `omitempty`、`DurMS int64` + `omitempty`；TS 侧 `timing?` / `dur_ms?` / `durMS?`，且断言全部用 `toBeUndefined()` 而不是 `toBe(0)`。

**`AttachInfo` 里的 Task（`handoff show`）** 仍是拆解 §6.6 表里那个「未知——可能命中 15 处手搭 map 之一」的环节。它**不在本计划范围内**，是真机清单第 1 条；真机若发现拿不到 `timing`，那是一张新卡（d_controlplane 的序列化边界修补），不是本计划的遗漏。

### 7.3 上下文预算检查

| Task | 有界文件集 | 规模 |
|---|---|---|
| 1 | `internal/store/{timing_agg.go,timing_agg_test.go,store.go}` + `internal/proto/contract_fixture_test.go` | 2 个新文件 + 2 处小改 |
| 2 | `web/src/app/task/frames.{ts,test.ts}` + `web/src/app/lib/format.{ts,test.ts}` | 4 个文件，改动各 <30 行 |
| 3 | `web/src/app/task/{TimingChip.tsx,TimingChip.test.tsx,ToolCard.tsx,blocks.test.tsx,TuiHeader.tsx,TuiHeader.test.tsx}` + `web/src/api/contract.test.ts` | 2 个新文件 + 5 处小改 |
| 4 | 无新文件 | 纯验证 |

四个 task 都圈得出有界文件集，**无需插竖切还债卡**。架构法第三条三条判据：`internal/store` 6 个源文件（不命中判据 2）、`web/src/app/task` 已是子目录且本次只加 1 个文件（不命中判据 1 的升格义务）、两处都远低于 2~3 万行（不命中判据 3）。

### 7.4 类型标注

T6（`d_ledger`）与 T7（`d_web`）**都是逻辑型**——接缝对面是自有代码，测试可闭环，所以行为验收全部机内完成，本计划的 14 条变异即其判据。

`d_executor`（边界型）本计划**零改动**。§8 的真机清单承接的是拆解 §7 里**尚未验完**的条目，不是本计划新产生的。

---

## 8. 真机清单（**本节归协调者执行，不派发**）

`handoff` 自身的驱动（起 agentd、派任务、调 CLI）与执行者纪律块的「不派发、不调用 handoff CLI、不起新 executor 进程」**直接冲突**，派出去等于没验。以下四条**不写进派发给执行者的范围**。

隔离实例仍在 linux-01 上跑（`127.0.0.1:7788`，DataDir `/tmp/hoav/data`），复用即可，不用重搭。

1. **`handoff show <id> | jq .task.timing` 真的带 timing**（承拆解 §3 与 §6.6 的「未知」环节）。拿不到即说明 `AttachInfo` 中间有手搭投影层 → 另立卡，不是本计划的遗漏。
2. **Web 控制台的 TUI tab 上耗时 chip 与工具卡耗时都出得来**，且三分法数字与 `handoff show` 的一致。
3. **`partial` 在运行中的任务上为 true、归档后为 false**（承 `aggregateTiming` 那条反直觉推论）。跑一个任务，跑到一半看一次、`done` 之后再看一次。
4. **承接需求 A 第一批真机清单的两条余账**：(a) claudecode / codex 的 `dur_ms` 量级在真机上从未采过样；(b) 并发工具（`ToolMS > ToolSpanMS`）在真实数据上到底出不出现——若从不出现，`OffsetMS` 的存在理由要重新审视（**但不因此删它**，契约已冻结，删要重走 contract）。

---

## 9. 自审三查

1. **spec 覆盖**：spec §A.4 的四条用户故事逐条指到 task ——故事 1（各命令耗多久）→ Task 1 的 Buckets + Task 3 的排行区；故事 2（API 一共多久）→ Task 1 的 `APIMS` + Task 3 的三分表；故事 3（按命令首词聚合的排行，P4 未选 (c) 故留在范围内）→ Task 1 的 `commandHead` + 下钻层 + Task 3 的 `BucketRow`；故事 4（`handoff show` 带 timing）→ Task 1 的 `GetTask` 接线，验证归真机清单第 1 条。TUI 每段耗时展示 → Task 2 + Task 3。**Out of Scope 照旧**：回合级汇总不做。
2. **占位符扫描**：全文无 TBD / 「同上」/「加适当的错误处理」/ 只描述不给代码。四个 task 的每处改动都给了完整代码块或精确的行内替换文本。
3. **跨 task 类型/签名一致性**：`ToolBlock.durMS?: number`（Task 2 定义 → Task 3 消费）、`formatDuration(ms: number): string`（Task 2 定义 → Task 3 两处消费）、`TimingChip({ timing?: TaskTiming })`（Task 3 内部自洽）、`Store.TaskTiming(string) (*proto.TaskTiming, error)`（签名与契约冻结物逐字一致，未改）、夹具数值三分法自洽（184300 − 121500 − 58400 = 4400；52100 + 19100 = 71200 = `tool_ms`；41800 + 10300 = 52100）——已逐个核过。
