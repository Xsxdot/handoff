# B93 任务进程失控 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 handoff 在 executor 进程失控时自己站得住——任务落终态即清扫残留进程，watchdog 按任务点名进程数并在失控时强制回收，`done` 在 agentd 被压垮时幂等。

**Architecture:** 三条互相独立的缺口，各自补一层，**不动既有的 `RLIMIT_NPROC` 围栏与准入闸**（spec §4 论证过它们在自己的范围内是对的）。每任务预算靠 watchdog 用既有的 `prochost.Footprint` 点名实现，**不是**靠调小围栏值——内核的 `RLIMIT_NPROC` 是 uid 级计数，结构上表达不了「这棵进程树最多 N 个」（spec §2.2）。

**Tech Stack:** Go 1.26 / 标准库 + `log/slog` / SQLite（`modernc.org/sqlite`）

**Spec:** [2026-08-15-b93-task-process-runaway-design.md](../specs/2026-08-15-b93-task-process-runaway-design.md)

## Global Constraints

- 基线 `main` @ `fb7f88d2c`。**只改 Go 代码**，不碰 `web/`（这条分支上根本没有 `web/`）。
- 日志一律 `m.log` / 传入的 `*slog.Logger`，**禁止 `fmt.Printf`**。
- 测试缝沿用本仓库既有写法：包级 `var xxxFn = 真实实现`（见 `prochost/fence.go` 的 `procLimitFn` / `getNprocLimitFn` / `admissionFn`），注释必须写「**生产路径恒为 X**，非测试代码不得赋值」。
- 新事件类型**必须 `hub.Publish`**，不能只 `AppendEvent`。B91 刚修过一条「只落库不广播、审核者永远不知道」的缺陷（`deny_guidance_dropped`），同一个坑不踩第二次。
- 新文件写文件头注释（职责 + 边界）；新导出函数写 doc 注释（参数、返回、注意事项）；非显然的分支写「为什么」的中文注释。
- 每个 Task 结束即 commit。
- **不要 `git push`**，改动留在任务分支上由审核者 pull。

## 已核实的既有事实（照抄，不要再去猜）

| 事实 | 坐标 |
|---|---|
| `SweepTaskProcs(taskID string)` 是 `*Manager` 的方法，全 best-effort，内部已处理 `ErrExecutorAlive` / verdict 非 OK / 出错三种分支 | `internal/agentd/reconcile.go:201` |
| 它当前**只有三个调用方**，全是「事后发现 executor 不在了」的补救路径 | `cmd/agentd.go:175`、`internal/agentd/reconcile.go:58`、`internal/agentd/manager.go:2398` |
| `handleResult` 的顺序是：`transitToReview` → `AppendEvent(completed 或 failed)` → `clearApproverState` → `hub.Publish(evt)`。失败分支还会 `voidTicketsWithAudit` | `internal/agentd/manager.go` 约 2500–2540 |
| `prochost.Footprint(h) (members []int, v Verdict, err error)`，`VerdictOK` 才可信 | `internal/prochost/footprint.go:142` |
| 取进程句柄的既有写法：`adapterFor` → 断言 `footprinter` 接口 → `fp.ProcHandle(taskID, taskDir)`，`taskDir = filepath.Join(m.cfg.DataDir, "tasks", taskID)` | `internal/agentd/reconcile.go:202-217` |
| watchdog 的扫描函数签名范式：`scanPressure(st *store.Store, hub *Hub, active bool, log *slog.Logger) bool`，置位状态由调用方持有并回传，**不用包级变量** | `internal/agentd/watchdog.go:193` |
| `watchdogTick = time.Minute`；`RunWatchdog(ctx, st, hub, stallTimeout, log)` 由 `cmd/agentd.go:183` 起 goroutine | `internal/agentd/watchdog.go:37` |
| 筛活跃任务用 `t.State.IsTerminal()` 取反，**不要枚举活跃态** | `internal/agentd/watchdog.go:214` |
| 高水位事件的既有形态：`proto.EventTypeResourcePressure` + `resourcePressurePayload{Used, Limit int}` | `internal/agentd/watchdog.go:45-48`、`internal/proto/proto.go` |
| 围栏配置结构：`cfg.ProcFence` 是 `config.ProcFenceConfig{Disabled bool; ReserveRatio float64}`，yaml key `proc_fence`，字段 tag 必须显式写（不写 yaml.v3 会映射成小写连写） | `internal/config/config.go:101,165` |
| 默认值与兜底在 `config.go:187` 与 `:239` 两处 | `internal/config/config.go` |
| `handleDone` 要求任务处于 `waiting_review` | `internal/agentd/server.go:935` |

---

### Task 1: 任务落终态即清扫残留进程

**Files:**
- Modify: `internal/agentd/manager.go`（`Manager` 结构体加一个字段；`handleResult` 两个分支各加一行）
- Test: `internal/agentd/manager_test.go`

**Interfaces:**
- Produces: `Manager` 新增未导出字段 `sweepProcs func(taskID string)`。**为 nil 时视为 `m.SweepTaskProcs`**，这样既有的所有构造路径不用改；测试注入替身。Task 4 会复用同一个字段。

- [ ] **Step 1: 写失败的测试**

在 `internal/agentd/manager_test.go` 追加。参照该文件里既有的 Manager 构造辅助函数（找 `newTestManager` 或同类，照它的写法造）：

```go
func TestHandleResultSweepsProcsOnFail(t *testing.T) {
	// why：executor 报告自己死了是主路径，而 SweepTaskProcs 的三个既有调用方
	// 全是「事后发现 executor 不在了」的补救路径。主路径不清扫，2100 个
	// setsid 逃逸出去的后代就一直挂到审核者手动 done（B93 事故实录）
	m, taskID := newManagerWithRunningTask(t)
	var swept []string
	var sweptAtSeq int64
	m.sweepProcs = func(id string) {
		swept = append(swept, id)
		if ev, err := m.st.LatestEvent(taskID); err == nil {
			sweptAtSeq = ev.Seq
		}
	}

	m.handleResult(taskID, executor.Result{OK: false, FailReason: "opencode 事件流意外中断"})

	if len(swept) != 1 || swept[0] != taskID {
		t.Fatalf("失败分支应清扫一次，实际 %v", swept)
	}
	// 清扫必须在 failed 事件之后：事件先落库，审核者的 wait 才第一时间醒；
	// 清扫是 best-effort 的善后，不该挡在唤醒前面
	ev, err := m.st.LatestEvent(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != proto.EventTypeFailed {
		t.Fatalf("最新事件应为 failed，实际 %s", ev.Type)
	}
	if sweptAtSeq != ev.Seq {
		t.Fatalf("清扫时最新事件 seq=%d，failed 事件 seq=%d —— 清扫跑在事件之前了", sweptAtSeq, ev.Seq)
	}
}

func TestHandleResultSweepsProcsOnSuccess(t *testing.T) {
	// why：executor 正常收尾同样会留下 setsid 逃逸的后代。Sweep 遇到 executor
	// 仍存活会返回 ErrExecutorAlive 并自行放弃（reconcile.go 的 switch 第一支），
	// 所以「回合结束但 executor 还活着」不会被误杀——这条保护是既有的
	m, taskID := newManagerWithRunningTask(t)
	var swept []string
	m.sweepProcs = func(id string) { swept = append(swept, id) }

	m.handleResult(taskID, executor.Result{OK: true, Branch: "b", CommitHash: "c"})

	if len(swept) != 1 {
		t.Fatalf("成功分支也应清扫一次，实际 %v", swept)
	}
}
```

若 `manager_test.go` 里没有现成的「造一个处于 running 的任务 + Manager」辅助函数，本步顺带写一个 `newManagerWithRunningTask(t *testing.T) (*Manager, string)`，照该文件既有测试的构造方式（临时 DataDir + `store.Open` + `NewManager`）。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test -run 'TestHandleResultSweepsProcs' ./internal/agentd/`
Expected: 编译失败——`m.sweepProcs undefined`。

- [ ] **Step 3: 加字段与调用**

`Manager` 结构体加字段：

```go
	// sweepProcs 是「清扫某任务残留进程」的测试缝。**生产路径恒为 nil**，
	// 由 sweep 方法退回 m.SweepTaskProcs；非测试代码不得赋值。
	//
	// 为什么用可空字段而不是包级 var：清扫是 Manager 的方法（要 m.cfg、m.log、
	// m.adapterFor），包级 var 拿不到实例。而既有的所有 NewManager 调用点
	// 不必改——nil 就是「用真的那个」
	sweepProcs func(taskID string)
```

加一个内部转发方法：

```go
// sweep 调用清扫，走测试缝或真实实现。
func (m *Manager) sweep(taskID string) {
	if m.sweepProcs != nil {
		m.sweepProcs(taskID)
		return
	}
	m.SweepTaskProcs(taskID)
}
```

在 `handleResult` 的**末尾**（`m.hub.Publish(evt)` 之后）加：

```go
	// 回合结束即清扫这一回合留下的孤儿后代。
	//
	// 放在 Publish 之后：事件先落库并广播，审核者的 wait 第一时间醒；清扫是
	// best-effort 的善后（SweepTaskProcs 内部每个失败分支都只记日志或发
	// orphan_risk，从不返回错误），不该挡在唤醒前面。
	//
	// 成功分支也清扫：executor 正常收尾同样可能留下 setsid 逃逸的后代
	// （opencode 的 Bash 工具把每条命令都 setsid 成新会话）。executor 本体
	// 不会被误杀——Sweep 遇到它仍存活会返回 ErrExecutorAlive 并自行放弃。
	//
	// 依赖提醒：本调用挂在 handleResult 上。B92（failed 事件落库但状态没迁移）
	// 若走的是别的路径，那条路径上这里不会执行——两条修复合起来才闭环，
	// watchdog 的每任务点名（scanTaskProcs）是那种情况下的兜底。
	m.sweep(taskID)
```

**注意**：`handleResult` 里有几处 `return` 提前退出（`transitToReview` 失败、`AppendEvent` 失败）。**那些提前退出的路径不加清扫**——它们连事件都没落，属于「这次什么都没发生」，清扫会把一个可能还在正常工作的 executor 的后代扫掉。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -run 'TestHandleResultSweepsProcs' ./internal/agentd/`
Expected: 两条 PASS。

- [ ] **Step 5: 全包回归**

Run: `go build ./... && go vet ./... && go test -count=1 ./internal/agentd/`
Expected: 全过。

- [ ] **Step 6: Commit**

```bash
git add internal/agentd/manager.go internal/agentd/manager_test.go
git commit -m "fix(agentd): 回合落终态即清扫残留进程（B93 §3.1）"
```

---

### Task 2: 每任务进程预算的配置项

**Files:**
- Modify: `internal/config/config.go:153-167`（`ProcFenceConfig`）、`:187`（默认值）、`:239`（兜底）
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces:
  ```go
  type ProcFenceConfig struct {
      Disabled     bool    `yaml:"disabled"`
      ReserveRatio float64 `yaml:"reserve_ratio"`
      TaskBudget    int `yaml:"task_budget"`     // 每任务进程数告警线，0 = 不启用该档
      TaskHardLimit int `yaml:"task_hard_limit"` // 每任务进程数硬上限，0 = 不启用该档
  }
  ```
  默认 `TaskBudget: 400`、`TaskHardLimit: 1200`。Task 4 读这两个值。

- [ ] **Step 1: 写失败的测试**

```go
func TestProcFenceTaskLimitsDefaults(t *testing.T) {
	cfg := defaultConfig() // 用本文件既有的默认值构造函数，名字以实际为准
	if cfg.ProcFence.TaskBudget != 400 {
		t.Fatalf("TaskBudget 默认应为 400，实际 %d", cfg.ProcFence.TaskBudget)
	}
	if cfg.ProcFence.TaskHardLimit != 1200 {
		t.Fatalf("TaskHardLimit 默认应为 1200，实际 %d", cfg.ProcFence.TaskHardLimit)
	}
}

func TestProcFenceTaskLimitsSanitized(t *testing.T) {
	// why：0 是「关掉这一档」的合法表达，必须原样保留，不能被兜底改回默认值；
	// 负数是配置写错，归零（= 关掉）而不是取绝对值
	for _, c := range []struct {
		name                   string
		budget, hard           int
		wantBudget, wantHard   int
	}{
		{"零表示关掉，原样保留", 0, 0, 0, 0},
		{"负数归零", -5, -1, 0, 0},
		{"硬上限小于告警线时抬到告警线", 400, 100, 400, 400},
		{"正常值原样", 200, 800, 200, 800},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := &Config{ProcFence: ProcFenceConfig{TaskBudget: c.budget, TaskHardLimit: c.hard}}
			sanitize(cfg) // 用本文件既有的兜底函数，名字以实际为准
			if cfg.ProcFence.TaskBudget != c.wantBudget || cfg.ProcFence.TaskHardLimit != c.wantHard {
				t.Fatalf("got (%d,%d) want (%d,%d)",
					cfg.ProcFence.TaskBudget, cfg.ProcFence.TaskHardLimit, c.wantBudget, c.wantHard)
			}
		})
	}
}

func TestProcFenceTaskLimitsYamlKeys(t *testing.T) {
	// why：不加 yaml tag 时 yaml.v3 会把 TaskBudget 映射成 taskbudget，
	// 与 README 里写的 task_budget 对不上——同一个坑 ReserveRatio 已经踩过一次
	var pf ProcFenceConfig
	if err := yaml.Unmarshal([]byte("task_budget: 7\ntask_hard_limit: 9\n"), &pf); err != nil {
		t.Fatal(err)
	}
	if pf.TaskBudget != 7 || pf.TaskHardLimit != 9 {
		t.Fatalf("yaml key 未按 snake_case 映射：%+v", pf)
	}
}
```

先读 `internal/config/config.go` 与既有测试，确认默认值构造函数与兜底函数的**真实名字**，把上面的 `defaultConfig()` / `sanitize(cfg)` 换成真名。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test -run 'TestProcFenceTaskLimits' ./internal/config/`
Expected: 编译失败——字段不存在。

- [ ] **Step 3: 加字段、默认值、兜底**

`ProcFenceConfig` 加两个字段并写注释：

```go
	// TaskBudget 是**单个任务**名下的进程数告警线，超过即发一次
	// task_proc_pressure 事件唤醒审核者。0 = 关掉这一档。
	//
	// 为什么不是把围栏值调小：RLIMIT_NPROC 的内核判定是「该 uid 当前进程总数
	// 是否超过调用者软限」，不是「这棵进程树的后代数」。给每个 shim 装 300 的
	// 效果是「uid 总数一过 300 所有 shim 一起 fork 失败」，第二个任务会被第一个
	// 饿死——表达不了每任务额度，只能换成 watchdog 按任务点名（B93 spec §2.2）
	TaskBudget int `yaml:"task_budget"`
	// TaskHardLimit 是单个任务的进程数硬上限，超过即强制清扫并落 failed。
	// 0 = 关掉这一档。
	//
	// 两档的分工：TaskBudget 是「叫醒人」，TaskHardLimit 是「不等人了」。
	// 只有一档要么太吵（每次都杀）要么太晚（人没醒机器就没了）。
	TaskHardLimit int `yaml:"task_hard_limit"`
```

默认值（`config.go:187` 那个字面量）加 `ProcFence: ProcFenceConfig{ReserveRatio: 0.1, TaskBudget: 400, TaskHardLimit: 1200}`——**保持与既有 `ReserveRatio` 默认值的设置方式一致**（若既有默认是在 `:239` 的兜底里给的，就照那个位置加，不要引入第二套默认值来源）。

兜底（`config.go:239` 附近）加：

```go
	// 负数是配置写错，归零 = 关掉这一档；0 本身是合法的「关掉」，原样保留
	if cfg.ProcFence.TaskBudget < 0 {
		cfg.ProcFence.TaskBudget = 0
	}
	if cfg.ProcFence.TaskHardLimit < 0 {
		cfg.ProcFence.TaskHardLimit = 0
	}
	// 硬上限低于告警线是自相矛盾的配置（还没告警就先杀了），抬到告警线。
	// 只在两档都启用时校正——有一档是 0 说明用户刻意只要另一档
	if cfg.ProcFence.TaskBudget > 0 && cfg.ProcFence.TaskHardLimit > 0 &&
		cfg.ProcFence.TaskHardLimit < cfg.ProcFence.TaskBudget {
		cfg.ProcFence.TaskHardLimit = cfg.ProcFence.TaskBudget
	}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -run 'TestProcFenceTaskLimits' ./internal/config/ && go test -count=1 ./internal/config/`
Expected: 全过。

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): proc_fence 增加 task_budget / task_hard_limit（B93 §3.2）"
```

---

### Task 3: `task_proc_pressure` 事件类型

**Files:**
- Modify: `internal/proto/proto.go`（事件类型常量）
- Test: `internal/proto/proto_test.go`（若该文件有事件类型的枚举/往返测试就跟着加；没有就跳过测试，本 Task 并到 Task 4 一起验）

**Interfaces:**
- Produces: `proto.EventTypeTaskProcPressure EventType = "task_proc_pressure"`

- [ ] **Step 1: 加常量**

在 `internal/proto/proto.go` 里 `EventTypeResourcePressure` 的**紧邻位置**加：

```go
	// EventTypeTaskProcPressure 是**单个任务**的进程数越线告警。
	//
	// 与 EventTypeResourcePressure 的分工：后者说「这台机器快满了」（uid 级），
	// 前者说「是这个任务在吃」（任务级）。事故复盘时前者才能定位到人。
	EventTypeTaskProcPressure EventType = "task_proc_pressure"
```

- [ ] **Step 2: 检查有没有需要同步的枚举**

Run: `grep -rn "EventTypeResourcePressure" --include="*.go" . | grep -v _test`
Expected: 逐个看结果。若存在「所有事件类型的集合」（校验表、CLI 的渲染 switch、文档字符串），把新类型一并加进去。**特别检查 `cmd/` 下 `wait` / `show` 的事件渲染**——漏了会让新事件在 CLI 里显示成一行原始 JSON。

- [ ] **Step 3: 跑构建**

Run: `go build ./... && go vet ./...`
Expected: 干净。

- [ ] **Step 4: Commit**

```bash
git add internal/proto/ cmd/
git commit -m "feat(proto): 新增 task_proc_pressure 事件类型（B93 §3.2）"
```

---

### Task 4: watchdog 按任务点名进程数

**Files:**
- Modify: `internal/agentd/watchdog.go`（新增 `scanTaskProcs` 与 payload；`runWatchdog` 循环接线；`RunWatchdog` 签名加参数）、`cmd/agentd.go:183`（传配置）、`internal/agentd/manager.go`（暴露一个按任务取成员数的方法）
- Test: `internal/agentd/watchdog_taskprocs_test.go`（新建）

**Interfaces:**
- Consumes: Task 1 的 `m.sweep(taskID)`、Task 2 的 `cfg.ProcFence.TaskBudget/TaskHardLimit`、Task 3 的 `proto.EventTypeTaskProcPressure`
- Produces:
  ```go
  // taskProcPressurePayload 与 resourcePressurePayload 分开：多一个 budget 字段，
  // 且语义是任务级不是机器级
  type taskProcPressurePayload struct {
      Used   int `json:"used"`
      Budget int `json:"budget"`
  }

  // taskProcCountFn 是「数某任务名下有几个进程」的测试缝。
  // 返回 (n, ok)：ok 为 false 表示数不出来（句柄取不到 / Verdict 非 OK），
  // 此时 n 无意义，调用方必须什么都不做。
  // **生产路径恒为 Manager.TaskProcCount**，非测试代码不得赋值。
  var taskProcCountFn func(taskID string) (int, bool)

  func scanTaskProcs(st *store.Store, hub *Hub, budget, hardLimit int,
      fired map[string]bool, sweep func(string), log *slog.Logger)
  ```

- [ ] **Step 1: 写失败的测试**

新建 `internal/agentd/watchdog_taskprocs_test.go`，参照既有的 `watchdog_fence_test.go` 造 store/hub：

```go
func TestScanTaskProcsWarnsOnceAtBudget(t *testing.T) {
	st, hub, taskID := newWatchdogFixture(t) // 造一个 running 任务
	sub := hub.Subscribe(taskID)             // 用既有订阅方式，名字以实际为准
	defer hub.Unsubscribe(sub)
	taskProcCountFn = func(string) (int, bool) { return 500, true }
	t.Cleanup(func() { taskProcCountFn = nil })

	fired := map[string]bool{}
	scanTaskProcs(st, hub, 400, 1200, fired, func(string) {
		t.Fatal("500 未超硬上限 1200，不该清扫")
	}, slog.Default())

	// 必须 Publish，不能只 AppendEvent——B91 刚修过一条「只落库不广播、
	// 审核者永远不知道」的缺陷，同一个坑不踩第二次
	select {
	case evt := <-sub:
		if evt.Type != proto.EventTypeTaskProcPressure {
			t.Fatalf("事件类型 %s", evt.Type)
		}
		var p taskProcPressurePayload
		mustUnmarshalPayload(t, evt, &p)
		if p.Used != 500 || p.Budget != 400 {
			t.Fatalf("payload 应带真实数字，实际 %+v", p)
		}
	default:
		t.Fatal("事件没有被广播出来")
	}

	// 第二轮仍超预算：不重发（沿用 scanPressure 的边沿触发口径，
	// 理由相同——事件风暴会淹掉真正要处置的工单）
	scanTaskProcs(st, hub, 400, 1200, fired, func(string) {}, slog.Default())
	select {
	case evt := <-sub:
		t.Fatalf("第二轮不该重发，却收到 %s", evt.Type)
	default:
	}
}

func TestScanTaskProcsRearmsAfterFallback(t *testing.T) {
	st, hub, taskID := newWatchdogFixture(t)
	sub := hub.Subscribe(taskID)
	defer hub.Unsubscribe(sub)
	fired := map[string]bool{}

	taskProcCountFn = func(string) (int, bool) { return 500, true }
	t.Cleanup(func() { taskProcCountFn = nil })
	scanTaskProcs(st, hub, 400, 1200, fired, func(string) {}, slog.Default())
	drain(sub)

	// 回落到预算以下 → 复位
	taskProcCountFn = func(string) (int, bool) { return 300, true }
	scanTaskProcs(st, hub, 400, 1200, fired, func(string) {}, slog.Default())
	if fired[taskID] {
		t.Fatal("回落后应复位")
	}

	// 再次越线 → 重新发一次
	taskProcCountFn = func(string) (int, bool) { return 500, true }
	scanTaskProcs(st, hub, 400, 1200, fired, func(string) {}, slog.Default())
	select {
	case <-sub:
	default:
		t.Fatal("复位后再越线应重新告警")
	}
}

func TestScanTaskProcsSweepsAtHardLimit(t *testing.T) {
	st, hub, taskID := newWatchdogFixture(t)
	taskProcCountFn = func(string) (int, bool) { return 1500, true }
	t.Cleanup(func() { taskProcCountFn = nil })

	var swept []string
	scanTaskProcs(st, hub, 400, 1200, map[string]bool{}, func(id string) {
		swept = append(swept, id)
	}, slog.Default())

	if len(swept) != 1 || swept[0] != taskID {
		t.Fatalf("超硬上限应清扫，实际 %v", swept)
	}
	task, err := st.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != proto.TaskStateFailed {
		t.Fatalf("超硬上限应落 failed，实际 %s", task.State)
	}
	ev, err := st.LatestEvent(taskID)
	if err != nil {
		t.Fatal(err)
	}
	// 理由里必须带真实数字：这是不可逆动作，审核者事后要能判断杀得对不对
	if !strings.Contains(fmt.Sprint(ev), "1500") || !strings.Contains(fmt.Sprint(ev), "1200") {
		t.Fatalf("failed 理由应含 used 与 hard limit 两个数字，实际 %v", ev)
	}
}

func TestScanTaskProcsDisabledDoesNotCount(t *testing.T) {
	// why：不启用就该完全不产生开销，而不是「数了但不发事件」。
	// Footprint 每次都要枚举全系统进程表，白数是实打实的浪费
	st, hub, _ := newWatchdogFixture(t)
	called := 0
	taskProcCountFn = func(string) (int, bool) { called++; return 9999, true }
	t.Cleanup(func() { taskProcCountFn = nil })

	scanTaskProcs(st, hub, 0, 0, map[string]bool{}, func(string) {
		t.Fatal("两档都关时不该清扫")
	}, slog.Default())

	if called != 0 {
		t.Fatalf("两档都关时不该数进程，实际数了 %d 次", called)
	}
}

func TestScanTaskProcsUnknownCountDoesNothing(t *testing.T) {
	// why：数不出来就什么都不做。把「量不出来」当成「超了」会误杀，
	// 当成「没超」会让已置位状态错乱——两种都比不动更糟
	st, hub, _ := newWatchdogFixture(t)
	taskProcCountFn = func(string) (int, bool) { return 0, false }
	t.Cleanup(func() { taskProcCountFn = nil })

	fired := map[string]bool{"x": true}
	scanTaskProcs(st, hub, 400, 1200, fired, func(string) {
		t.Fatal("读数不可信时不该清扫")
	}, slog.Default())
	if !fired["x"] {
		t.Fatal("读数不可信时不该改动置位状态")
	}
}

func TestScanTaskProcsSkipsTerminalTasks(t *testing.T) {
	// why：终态任务已经不会再 fork 任何东西。沿用 scanPressure 的 IsTerminal
	// 取反写法，新增状态时自动跟上
	st, hub, taskID := newWatchdogFixture(t)
	mustTransit(t, st, taskID, proto.TaskStateCompleted)
	called := 0
	taskProcCountFn = func(string) (int, bool) { called++; return 9999, true }
	t.Cleanup(func() { taskProcCountFn = nil })

	scanTaskProcs(st, hub, 400, 1200, map[string]bool{}, func(string) {}, slog.Default())
	if called != 0 {
		t.Fatalf("终态任务不该被点名，实际 %d 次", called)
	}
}
```

`newWatchdogFixture` / `drain` / `mustUnmarshalPayload` / `mustTransit` 若既有测试文件里没有，本步一并写；有同功能的就复用既有的，**不要造第二套**。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test -run 'TestScanTaskProcs' ./internal/agentd/`
Expected: 编译失败——`scanTaskProcs` 未定义。

- [ ] **Step 3: 实现 `Manager.TaskProcCount`**

在 `internal/agentd/reconcile.go`（与 `SweepTaskProcs` 同文件，它们共用取句柄的那段逻辑）加：

```go
// TaskProcCount 数一个任务名下当前有几个进程。
//
// 参数：taskID 为完整任务 id
//
// 返回：(n, ok)。**ok 为 false 时 n 无意义**，调用方必须什么都不做——
// 取不到句柄、adapter 不支持、Footprint 判定不可信（Verdict 非 OK）都归此类。
// 把「量不出来」当成「超了」会误杀，当成「没超」会让告警的置位状态错乱。
//
// 导出是因为 watchdog 的接线点在 cmd/agentd.go（与 SweepTaskProcs 同理），
// 不是给外部当通用 API 用。
func (m *Manager) TaskProcCount(taskID string) (int, bool) {
	ad, err := m.adapterFor(taskID)
	if err != nil {
		m.log.Debug("点名解析执行者失败", "task", taskID, "cause", err)
		return 0, false
	}
	fp, ok := ad.(footprinter)
	if !ok {
		return 0, false
	}
	h, err := fp.ProcHandle(taskID, filepath.Join(m.cfg.DataDir, "tasks", taskID))
	if err != nil {
		m.log.Debug("点名取进程句柄失败", "task", taskID, "cause", err)
		return 0, false
	}
	members, v, err := prochost.Footprint(h)
	if err != nil || v != prochost.VerdictOK {
		m.log.Debug("点名读数不可信", "task", taskID, "verdict", string(v), "cause", err)
		return 0, false
	}
	return len(members), true
}
```

- [ ] **Step 4: 实现 `scanTaskProcs`**

在 `internal/agentd/watchdog.go` 加 payload 与扫描函数：

```go
// taskProcPressurePayload 是任务级进程越线事件的载荷。
//
// 与 resourcePressurePayload 分开而不是复用：那个是机器级（used/limit 都是 uid
// 维度），这个是任务级，两者叠在一起时审核者要能一眼分清是谁在吃。
type taskProcPressurePayload struct {
	Used   int `json:"used"`
	Budget int `json:"budget"`
}

// taskProcCountFn 是「数某任务名下有几个进程」的测试缝。
// **生产路径恒为 Manager.TaskProcCount**（接线在 cmd/agentd.go），
// 非测试代码不得赋值。
var taskProcCountFn func(taskID string) (int, bool)

// scanTaskProcs 按任务点名进程数，两档处置。
//
// 参数：
//   - budget: 告警线，<=0 表示该档关闭
//   - hardLimit: 硬上限，<=0 表示该档关闭
//   - fired: 每任务的告警置位状态，由调用方持有并跨轮传递（**不用包级变量**，
//     那会让两个 agentd 实例互相踩状态——沿用 scanPressure 的同一条理由）
//   - sweep: 清扫某任务残留进程的入口
//
// 三条语义（与 scanPressure 同构）：
//   - 越线且未置位：发一次 task_proc_pressure 并置位
//   - 仍越线且已置位：不重发。事件风暴会把协调者的会话刷爆
//   - 回落到预算以下：复位，下次越线可再发
//
// 硬上限档是本仓库第一次让 agentd 在无人裁决的情况下杀进程，所以：读数不可信
// 一律什么都不做；理由里必须写上 used 与 hardLimit 两个真实数字，让审核者
// 事后能判断杀得对不对。
func scanTaskProcs(st *store.Store, hub *Hub, budget, hardLimit int,
	fired map[string]bool, sweep func(string), log *slog.Logger) {
	// 两档都关 = 完全不启用。这里直接返回而不是往下走到「数了但不处置」——
	// Footprint 每次都要枚举全系统进程表，白数是实打实的开销
	if budget <= 0 && hardLimit <= 0 {
		return
	}
	if taskProcCountFn == nil {
		return
	}
	tasks, err := st.ListTasks()
	if err != nil {
		log.Error("任务进程点名读取任务列表失败", "cause", err)
		return
	}
	for _, t := range tasks {
		// 终态任务已经不会再 fork 任何东西。用 IsTerminal 取反而不是枚举活跃态：
		// 新增状态时这里自动跟上
		if t.State.IsTerminal() {
			continue
		}
		n, ok := taskProcCountFn(t.ID)
		if !ok {
			continue // 数不出来就什么都不做，连置位状态都不动
		}
		if hardLimit > 0 && n > hardLimit {
			log.Error("任务进程数超过硬上限，强制回收", "task", t.ID, "used", n, "hard_limit", hardLimit)
			sweep(t.ID)
			reason := fmt.Sprintf("任务进程数 %d 超过硬上限 %d，已强制回收", n, hardLimit)
			if err := transitFailedWithEvent(st, hub, t.ID, reason, log); err != nil {
				log.Error("强制回收后落 failed 失败", "task", t.ID, "cause", err)
			}
			delete(fired, t.ID)
			continue
		}
		if budget <= 0 {
			continue
		}
		if n <= budget {
			if fired[t.ID] {
				log.Info("任务进程数已回落到预算以下", "task", t.ID, "used", n, "budget", budget)
			}
			delete(fired, t.ID)
			continue
		}
		if fired[t.ID] {
			continue // 仍越线，已告警过，不重发
		}
		evt, aerr := st.AppendEvent(t.ID, proto.EventTypeTaskProcPressure,
			taskProcPressurePayload{Used: n, Budget: budget})
		if aerr != nil {
			log.Error("追加任务进程越线事件失败", "task", t.ID, "cause", aerr)
			continue // 没发出去就不置位，下一轮重试
		}
		// 必须广播：只落库的话审核者要主动 show 才看得见，等于没告警（B91 先例）
		hub.Publish(evt)
		fired[t.ID] = true
		log.Warn("任务进程数超过预算，已告警", "task", t.ID, "used", n, "budget", budget)
	}
}
```

`transitFailedWithEvent` 若仓库里已有等价物（找 `reconcile.go` 里 `reconcileExecutorGone` 用的那条「追加 failed 事件 + 迁状态 + 广播」的路径，`reconcile.go:163` 附近），**直接复用它，不要写第二份**。没有等价物才新写，并且**必须先迁状态再追加事件**，与 `handleResult` 的顺序保持一致。

- [ ] **Step 5: 接线**

- `RunWatchdog` 的签名加 `budget, hardLimit int` 与 `sweep func(string)` 参数，在它的 tick 循环里调 `scanTaskProcs`，`fired` map 在循环外声明（跨 tick 存活）。
- `cmd/agentd.go:183` 改成传 `cfg.ProcFence.TaskBudget, cfg.ProcFence.TaskHardLimit, mgr.SweepTaskProcs`。
- `cmd/agentd.go` 里在 `SetFencePolicy` 附近赋一次测试缝：`agentd.SetTaskProcCounter(mgr.TaskProcCount)`（在 `watchdog.go` 加这个导出的 setter，doc 注释写明「由 agentd 启动时调用一次；测试直接赋 `taskProcCountFn`」）。
- `cmd/agentd.go:195` 那行启动日志里把两个新配置一并打出来，与既有的 `proc_fence_reserve_ratio` 并列。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test -run 'TestScanTaskProcs' ./internal/agentd/ && go build ./... && go vet ./...`
Expected: 六条全 PASS，构建干净。

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/ cmd/agentd.go
git commit -m "feat(agentd): watchdog 按任务点名进程数，两档告警与强制回收（B93 §3.2）"
```

---

### Task 5: `done` 幂等

**Files:**
- Modify: `internal/agentd/server.go:935`（`handleDone`）
- Test: `internal/agentd/server_test.go`

**Interfaces:**
- Consumes: 无
- Produces: 无签名变化。`POST /api/tasks/{id}/done` 在任务已是 `completed` 时返回 200 + 与首次相同的响应体。

- [ ] **Step 1: 写失败的测试**

```go
func TestDoneIsIdempotentOnCompleted(t *testing.T) {
	// why：事故里 done 第一次返回 read: operation timed out，但请求其实已落库；
	// 重发拿到 409，看起来像「状态不对」。客户端分不清「超时 = 请求没到」和
	// 「超时 = 请求到了但响应没回来」——这是服务端才有的信息
	srv, taskID := newServerWithReviewTask(t) // 造一个 waiting_review 任务

	first := doPost(t, srv, "/api/tasks/"+taskID+"/done", `{"note":"收口"}`)
	if first.Code != http.StatusOK {
		t.Fatalf("首次 done 应 200，实际 %d", first.Code)
	}
	second := doPost(t, srv, "/api/tasks/"+taskID+"/done", `{"note":"收口"}`)
	if second.Code != http.StatusOK {
		t.Fatalf("重发 done 应 200（幂等），实际 %d：%s", second.Code, second.Body.String())
	}
	if second.Body.String() != first.Body.String() {
		t.Fatalf("重发的响应体应与首次相同\n首次: %s\n重发: %s", first.Body.String(), second.Body.String())
	}
}

func TestDoneStillRejectsNonReviewStates(t *testing.T) {
	// why：幂等只覆盖 completed 这一种。其余状态仍要 409——放行等于让 done
	// 变成万能收口，审核者会失去「我操作错了」这个信号
	for _, state := range []proto.TaskState{
		proto.TaskStateRunning,
		proto.TaskStateWaitingAnswer,
		proto.TaskStateFailed,
		proto.TaskStatePending,
	} {
		t.Run(string(state), func(t *testing.T) {
			srv, taskID := newServerWithTaskInState(t, state)
			resp := doPost(t, srv, "/api/tasks/"+taskID+"/done", `{}`)
			if resp.Code != http.StatusConflict {
				t.Fatalf("%s 状态下 done 应 409，实际 %d", state, resp.Code)
			}
		})
	}
}
```

`newServerWithReviewTask` / `newServerWithTaskInState` / `doPost` 照 `server_test.go` 既有的辅助函数写法造或复用。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test -run 'TestDone' ./internal/agentd/`
Expected: `TestDoneIsIdempotentOnCompleted` FAIL（重发得到 409）；`TestDoneStillRejectsNonReviewStates` 应该已经 PASS（既有行为），**若它也 FAIL 说明既有行为与预期不符，停下来上报，不要改测试**。

- [ ] **Step 3: 改实现**

`handleDone` 在做状态迁移之前先读一次任务，命中 `completed` 就直接返回既有结果：

```go
	// done 幂等：agentd 被压垮时（B93 事故），第一次 done 的请求已落库但响应
	// 读超时，审核者的自然反应是重发，而重发拿到的 409 看起来像「状态不对」。
	// 客户端分不清「超时 = 请求没到」和「超时 = 请求到了但响应没回来」——
	// 这是只有服务端才有的信息，只能在这里解决。
	//
	// **判据要严**：只有 completed 转 200。其余非 waiting_review 的状态仍然
	// 409——那些是真的状态不对，一并放行等于让 done 变成万能收口，审核者会
	// 失去「我操作错了」这个信号。
	if cur, err := s.mgr.Store().GetTask(taskID); err == nil && cur.State == proto.TaskStateCompleted {
		writeJSON(w, http.StatusOK, doneRespFor(cur))
		return
	}
```

`s.mgr.Store()` / `writeJSON` / 响应体构造函数用该文件**既有**的写法（读 `handleDone` 当前是怎么写响应的，照抄那一份，确保重发与首次的响应体逐字节相同）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -run 'TestDone' ./internal/agentd/`
Expected: 两条 PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/agentd/server.go internal/agentd/server_test.go
git commit -m "fix(agentd): done 在任务已归档时幂等返回 200（B93 §3.3）"
```

---

### Task 6: 总回归与变异测试

**Files:** 无新改动，只验证。

- [ ] **Step 1: 全量回归**

Run: `go build ./... && go vet ./... && go test -count=1 ./...`
Expected: 全部包 PASS，0 FAIL。

- [ ] **Step 2: 变异测试三条，各自单独红**

**不要写一个脚本一次改三处**——要证明的是「三条用例 1:1 对应三处实现，没有交叉兜底」，所以必须一次只改一处。

```bash
# 变异 1：摘掉 handleResult 末尾的清扫
git stash list >/dev/null
sed -i '' 's|^\tm\.sweep(taskID)$|\t// m.sweep(taskID)|' internal/agentd/manager.go
go test -run 'TestHandleResultSweepsProcs' ./internal/agentd/   # 期望：两条 FAIL
git checkout internal/agentd/manager.go

# 变异 2：摘掉 scanTaskProcs 里的 hub.Publish
sed -i '' 's|^\t\thub\.Publish(evt)$|\t\t_ = evt|' internal/agentd/watchdog.go
go test -run 'TestScanTaskProcsWarnsOnce' ./internal/agentd/    # 期望：FAIL（事件没广播）
git checkout internal/agentd/watchdog.go

# 变异 3：摘掉 done 的幂等分支
# 手工把 Task 5 Step 3 那个 if 块注释掉，然后：
go test -run 'TestDoneIsIdempotent' ./internal/agentd/          # 期望：FAIL
git checkout internal/agentd/server.go
```

**变异 2 的 sed 若匹配不到**（缩进或写法不同），用 `grep -n "hub.Publish" internal/agentd/watchdog.go` 找到真实行号，按行号 sed，不要放弃这一步。

每条变异后都要 `git checkout` 还原，并在**全部变异做完后**再跑一次 `go test -count=1 ./internal/agentd/` 确认还原干净。

- [ ] **Step 3: 把变异结果写进 ledger**

三条变异各自的实际输出（哪些用例 FAIL）逐条记进 ledger 文件。**只写「做了变异测试」不算**——要有 FAIL 的用例名。

- [ ] **Step 4: 真机复验的交接说明**

spec §5 第 8 条要求在 mac-02 上真机复验（派一个刻意 fork 500+ 进程的任务，确认收到 `task_proc_pressure`；再超 1200，确认被清扫并落 `failed`）。**这一条不由你执行**——它要在装了新 agentd 的机器上做，而升级 agentd 是审核者的事（要先停旧的再起新的，见 handoff 铁律）。

你要做的是在 ledger 末尾写一段交接说明：新增了哪两个配置项、默认值多少、怎么关掉、真机复验该怎么构造那个 fork 炸弹任务。写完即视为本 Task 完成。

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "chore: B93 总回归与变异测试记录"
```
