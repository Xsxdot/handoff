# B396 debug 计划：陈旧环节运行位不自愈——根因、分流与修复任务

> 卡 B396 · 入口节点 charter:debug · spec `docs/superpowers/specs/b396.md`（已批准）
> 基线分支 `cards/B396-charter-2`，起手 HEAD `5a1d70cb`（已按硬性第一步
> `git fetch origin cards/B233.1-charter-7 && git merge --no-edit -X ours origin/cards/B233.1-charter-7`
> → `Already up to date`）。凡引用行号者动手前重核，漂了以符号为准。
> 台账 `docs/superpowers/ledgers/2026-09-22-b396-plan-ledger.md`（含全部亲跑命令、
> 原始输出、图查询记录）。
> **本节点只出计划，不写实现**。「未验证」= 本节点没亲自跑到结果，实现卡必须自己复核。
> 读者假设：对 handoff 仓零上下文的执行者。

---

## 0. 待拍板清单（阻塞实施，交协调者裁决）

| # | 岔口 | 选项 | 影响 |
|---|------|------|------|
| **P1** | **陈旧运行位怎么自愈** | (甲) **归档路径收口**：`waitForTurnEnd` 认 `archived` 为终态 → `Run` 返回 → `defer releaseCardStep` + `ReleaseRunLock` 释放（落在 `internal/ledgerstep`）；(乙) **409 判据加兜底**：`startCardStep` 的 `claimCardStep` 之外再读账本运行锁/过期/持有者存活；(丙) 清陈旧槽位：`startCardStep` 发现槽位在飞但账本无未过期锁就抢占槽位 | **推荐 (甲)**：(甲) 已在副本实跑红→绿（台账 §2/§3），是唯一同时释放**内存槽位**与**账本运行锁**、且不动 409 判据的路径；(乙) 只让 409 放行，卡住的回合 goroutine 与运行锁仍泄漏，且要改 409 判据（互斥语义面，spec §2「不做」）；(丙) 需要新写「槽位↔锁」一致性判据，等价于让 agentd 内存成为第二真相源，B239 契约 §2.5 明令权威归账本。**代价（须知晓）**：(甲) 归档后卡上会落一条 `needs_human`（现有 `haltForHuman` 形状），它不阻断重派（`startCardStep` 不读该标记，已亲跑验证 202）。 |
| **P2** | **`archived` 收口的语义** | (甲) **终态错误 → needs_human**（`Await` 失败既有形状）；(乙) 当 pass/continue 路由 | **推荐 (甲)**：归档发生在本节点等待途中，本节点**没拿到裁决报文**，无法解析 verdict、无法路由（`NodeStep.RunOnce` 的 `ParseVerdict` 拿不到输入）；把它当 pass 等于伪造裁决。`needs_human` + 原文评论是既有 `Await` 失败路径（`internal/ledgerstep/node.go:278-282`）的同形处置，`card wait` 可见。 |
| **P3** | **`waitForTurnEndGrace`（宽限循环）要不要一起认 `archived`** | (甲) 一起加；(乙) 只改主循环 | **推荐 (甲)**，但**承重的是主循环那条**：主循环不返回才是挂死主因；宽限循环有 1s 硬期限（`wire.go:85` `context.WithTimeout(ctx, turnEndGrace)`），不会永久挂死。两条同加是为语义一致（归档后在宽限窗内也不该再等），代价一行 case。变异复验**只需打主循环那条**（台账 §3 已证）。 |

---

## 1. 根因（证据驱动，全部亲跑或亲读；原始输出见台账）

### R1（根因，决定性）：`waitForTurnEnd` 不把 `archived` 当终态 ⇒ 节点回合永不返回 ⇒ 环节槽位与运行锁永不释放

409「该卡已有环节在运行」的判据**只在 agentd 进程内存里**，且只看槽位是否存在：

```go
// internal/agentd/cardstep.go:128-131
func (s *Server) startCardStep(cardID string, req proto.CardStepReq) error {
	if !s.claimCardStep(cardID) {
		return fmt.Errorf("%w: %s 的 %s 节点正在运行", errStepInFlight, cardID, req.Step)
	}
// internal/agentd/cardstep.go:285-293
func (s *Server) claimCardStep(cardID string) bool {
	s.cardStepMu.Lock(); defer s.cardStepMu.Unlock()
	if s.cardStepFlight[cardID] { return false }
	s.cardStepFlight[cardID] = true
	return true
}
// internal/agentd/cardstep.go:33-34
var errStepInFlight = errors.New("该卡已有环节在运行")
// internal/agentd/ledgerapi.go:532-535
case errors.Is(err, errStepInFlight):
	writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
```

槽位**只有一个释放点**——节点 goroutine 返回时：

```go
// internal/agentd/cardstep.go:240-248
go func() {
	defer s.releaseCardStep(cardID)                         // ← 唯一释放点
	s.log.Info("卡节点回合开始", ...)
	s.runStepFn(context.Background(), runner, cardID, req.Step)  // ctx=Background，永不取消
	s.log.Info("卡节点回合返回", ...)
}()
```

节点 goroutine 的等待链：
`runStep` → `StepRunner.Run`（`internal/ledgerstep/runner.go:254` `nodeStep.RunOnce`）
→ `NodeStep.RunOnce`（`node.go:187`）→ `n.Await`（`node.go:278`）
→ `awaitNode`（`runner.go:414-445`）→ `waitForTurnEnd`（`runner.go:431`）。

而 `waitForTurnEnd` 的终态集合只有 `completed` 与 `turn_failed`/`failed`：

```go
// internal/ledgerstep/wire.go:53-82
func waitForTurnEnd(ctx context.Context, wait func(context.Context) (*proto.Event, error)) error {
	for {
		event, err := wait(ctx)
		...
		switch event.Type {
		case proto.EventTypeCompleted:            // 带 final_text → 返回 nil
			...
			return waitForTurnEndGrace(ctx, wait)
		case proto.EventTypeTurnFailed, proto.EventTypeFailed:
			return nil
		}   // ← archived 落空：不返回，回到 for 再 wait
	}
}
```

`archived` 是**可交付**事件（`internal/client/delivery.go:23-35` 的
`WaitDeliveryPolicy` 只对 progress/审批类返回 false），所以 `WaitEvent` 会把
`archived` 交回；`waitForTurnEnd` 忽略它、再次 `wait`。任务归档后不再产出任何
事件（`internal/orchestration/manager.go:1663` 发 `archived`，
`:1674` `hub.CloseTask` 关闭订阅），`wait` 永久阻塞（ctx 是 Background，不取消）
⇒ `Run` 永不返回 ⇒ `cardStepFlight[cardID]` 永不 delete ⇒ 同卡同节点此后**永远 409**。

**红回路亲跑**（`internal/agentd` 缝级，走真实 `startCardStep` → 真实
`StepRunner.Run` → 真实 `waitForTurnEnd`；原始输出见台账 §2/R3）：

```text
$ go test ./internal/agentd/ -run TestB396ArchivedTaskReleasesCardStepSlot -count=1 -timeout 60s
--- FAIL: TestB396ArchivedTaskReleasesCardStepSlot (2.98s)
    b396_archived_step_test.go:129: 等待条件超时
FAIL
```

### R2（为何不自愈）：运行锁被续租，且 409 根本不读它

```go
// internal/ledger/runlock.go:27-32
RunLockTTL          = 5 * time.Minute
RunLockRenewInterval = 2 * time.Minute
// internal/ledgerstep/runner.go:209-235：续租 goroutine，每 2 分钟 RenewRunLock
// internal/ledgerstep/runner.go:154-160：defer ReleaseRunLock(cardID, r.RunHolder)
```

`Run` 不返回 ⇒ 续租 goroutine 一直活着，每 2 分钟把 `expires_at` 推后 5 分钟 ⇒
账本运行锁也永不过期，这解释了「25 分钟后仍被拒」。**即便**运行锁过期也不救：
409 判据（`cardStepFlight`）读的是内存槽位，不读 `card_run_locks`、不读租期。

### R3（触发链）：外部归档发生在节点等待途中

卡上事件（`handoff card show`）链条：本节点派发 task → 本节点 `WaitEvent`
等待 → SSE 故障 ⇒ `resume --force`（`internal/agentd/handlers.go:1039`
`handleResume` → `Manager.RecoverStuck`，任务收口到 `waiting_review`）⇒ `done`
（`handlers.go:861` `handleDone` → `Manager.Done`）⇒ task 发 `archived` 并关闭订阅
⇒ 本节点等待收口失败（R1）⇒ 重派 409。

**注意**：`resume --force` 撤的是**任务**的卡死态，不碰**卡节点编排**的槽位——
槽位属于仍在 `waitForTurnEnd` 里的那个 goroutine，只有它能释放。所以修复点必须在
节点等待链（`waitForTurnEnd`），不在 resume/done 路径。

---

## 2. 分流决定

| 根因 | 归属 | 处置 |
|---|---|---|
| R1 `archived` 不是 `waitForTurnEnd` 的终态 | 本卡，L2 / `internal/ledgerstep` | **T2**：主循环 + 宽限循环各认 `archived`，收口为包内错误 |
| R1 的触发（外部 done/resume 发生在等待途中） | 上一条的机制解释 | 随 T2 一并解决 |
| R2 运行锁被续租 + 409 不读锁 | 上一条的后果 | 随 T2 一并解决（Run 返回 → defer 释放锁与槽位） |
| 409 判据本身（只看内存槽位） | 互斥语义面 | **不在本卡**（spec §2「不改互斥语义」）；P1 讨论 |
| 真机重放（spec §4-4） | 本 task 由协调者执行，不派发 | 见 §11 |

> 遵 spec §「不改互斥语义、不手改锁数据、不动线上状态」。

---

## 3. 任务 DAG

```
T1（红色回路转正：TestB396ArchivedTaskReleasesCardStepSlot 落仓，今天红）
  └→ T2（实现：waitForTurnEnd 认 archived，T1 转绿）
        └→ T3（变异复验 + 不误伤回归 + 并发反例在场）

（独立）P1 乙/丙 备选、409 判据改判 → 回 spec / 另立卡，不在本 DAG
```

次序承重：T1 的回路必须先红，才能证明 T3 的护栏是它转绿的原因；T3 的变异复验
依赖 T2 的护栏在场。

---

## 4. 基线事实（实现卡共享，动手前复核）

**亲跑读数（原始输出见台账）**：

- `go build ./...` → `BUILD_EXIT=0`；`go vet ./internal/ledgerstep/ ./internal/agentd/` → `VET_EXIT=0`。
- `go test ./internal/ledgerstep/ -count=1 -timeout 300s` → `ok ... 14.181s`。
- 红回路基线：见 §1/R1（`TestB396ArchivedTaskReleasesCardStepSlot` FAIL）。

**预存在 flake（非本卡引入，勿误判为回归）**：`internal/agentd` 全量跑时
`TestPtyWSAttachedBacklogBytesKeyPresent` 偶发失败（`新会话 backlog_bytes = 24，期望 0`）。
在**完全干净基线**上 `-count=5` 仍 2/5 失败（台账 §3）。本卡的测试范围不覆盖它。

**库行为事实（带出处）**：

- `archived` 事件可交付：`internal/client/delivery.go:23-35`（未列即为 true）。
- 归档时先发 `archived` 再关订阅：`internal/orchestration/manager.go:1663`、`:1674`
  （顺序硬约束，见 `handleEvents` 注释 `internal/agentd/handlers.go:1742-1751`）。
- `WaitEvent` 对正常关闭码（`StatusNormalClosure`）按**断线重连**处理：
  `internal/client/client.go:1605-1608`（`errArchived` 不是永久失败，外层退避重连）。
  这正是「归档后 wait 不会自己 errored 返回、而是继续等」的机制出处。

**现状签名与调用面（`codegraph sym` 命中，行号以实源为准；图覆盖债见 §12）**：

- `waitForTurnEnd(ctx context.Context, wait func(context.Context) (*proto.Event, error)) error`
  — `internal/ledgerstep/wire.go:53`；
- `waitForTurnEndGrace(ctx context.Context, wait func(context.Context) (*proto.Event, error)) error`
  — `internal/ledgerstep/wire.go:84`；
- `Server.startCardStep(cardID string, req proto.CardStepReq) error`
  — `internal/agentd/cardstep.go:128`；
- `Server.claimCardStep(cardID string) bool` — `internal/agentd/cardstep.go:285`；
- `Server.releaseCardStep(cardID string)` — `internal/agentd/cardstep.go:295`；
- `Store.AcquireRunLock/RenewRunLock/ReleaseRunLock/RunLockOf`
  — `internal/ledger/runlock.go:39/106/129/145`。

**既有测试夹具（T1 复用，签名逐字）**：

- `newLedgerEnv(t *testing.T) *ledgerEnv` — `internal/agentd/ledgerapi_test.go:140`；
- `seedCardWithProject(t *testing.T, s *Server, project string)` — `internal/agentd/cardstep_test.go:30`；
- `seedDisciplineOnLedger(t *testing.T, env *ledgerEnv, name, body string) int`
  — `internal/agentd/cardstep_discipline_test.go:131`；
- `ledgerPost(t *testing.T, env *testAgentdEnv, path, body string) (int, string)`
  — `internal/agentd/ledgerapi_test.go:44`；
- `waitFor(t *testing.T, predicate func() bool)` — `internal/agentd/cardstep_test.go:88`；
- `testhttp.NewServer(t testing.TB, handler http.Handler) *httptest.Server`
  — `internal/testhttp/server.go`（**必须用它**，见 §9 门禁守卫）。

---

## 5. 接口契约（执行者只看本 task，故这里列全）

### Consumes（本卡要用的既有签名，一字不改）

```go
// internal/ledgerstep/runner.go
func (r *StepRunner) Run(ctx context.Context, cardID, nodeName string) (Outcome, error)
type StepClient interface {
	client.ExecutionClient            // Dispatch/Reply/Continue/Stop/WaitEvent/FollowEvents
	Diff(ctx context.Context, taskID, base string) (string, error)
	Attach(ctx context.Context, taskID string) (*client.AttachInfo, error)
	Done(ctx context.Context, taskID, note string) (bool, error)
}

// internal/agentd/server.go（测试经 runStepFn 替换落点；生产恒为 s.runStep）
runStepFn func(ctx context.Context, runner *ledgerstep.StepRunner, cardID, step string)

// internal/proto/proto.go
EventTypeArchived EventType = "archived"
```

### Produces（本卡新增，跨 task 逐字对齐）

```go
// internal/ledgerstep/wire.go（包内，未导出）
var errTurnArchived = errors.New("等待的 task 已归档，回合不会再产出裁决报文")

// internal/agentd/b396_archived_step_test.go（新文件）
func TestB396ArchivedTaskReleasesCardStepSlot(t *testing.T)
```

> 序列化边界：本卡**不新增任何数据字段**，不改任何 DTO/json tag、不改 `archived`
> payload 形状。唯一跨界是 `proto.Event.Type` 的取值判断，已被 T1 的缝级测试穿过
> 真实 `proto.Event` 解码。故无新增手写序列化/投影点（见 §9 追加设问一）。

---

## 6. 任务详情

### T1 红色回路转正：归档后同卡同节点必须可重派（先红）

**动作**：新建测试文件 `internal/agentd/b396_archived_step_test.go`，内容如下
（完整，无占位；本节点已在副本实跑：基线红、T2 后绿、变异复红，原文见台账 §2/§3）：

```go
// b396_archived_step_test.go —— B396 陈旧环节运行位不自愈的缝级回归回路。
//
// 职责：锁住「节点等待的 task 被外部归档（done）后，卡节点回合必须收口、
// 释放进程内环节槽位与账本运行锁，使同卡同节点可重派」；同时锁住反向断言
// ——真在跑时同卡同节点并发重派仍必须 409（不许把互斥一起放开）。
// 缝：HTTP `POST /api/cards/{id}/step`（`card dispatch --step` 落到同一入口），
// 即 409「该卡已有环节在运行」的判据面；不直调 waitForTurnEnd。
// 边界：不复制 keystone/编排规则；不测 task 生命周期本身（另有既有测试）。
package agentd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/discipline"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/testhttp"
	"github.com/coder/websocket"
)

// b396ArchivedTaskServer 造一个 dispatch 目标：每条连接都只产出 archived
// （外部 done 的形态），永不产出 completed/failed。第 1 条连接由 release 控制
// 何时归档——它对应「首次派发仍在飞」的窗口；release 之后所有连接（重派）立即
// 归档并关闭，避免测试结束时留下重试 goroutine。
func b396ArchivedTaskServer(t *testing.T, taskID string) (url string, connected <-chan struct{}, release func()) {
	t.Helper()
	var mu sync.Mutex
	conns := 0
	connReady := make(chan struct{})
	rel := make(chan struct{})
	var relOnce sync.Once
	ts := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/ws/events":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("Accept WS: %v", err)
				return
			}
			mu.Lock()
			conns++
			n := conns
			mu.Unlock()
			if n == 1 {
				close(connReady)
				<-rel
			}
			ev := proto.Event{Seq: 1, TaskID: taskID, Type: proto.EventTypeArchived,
				Payload: json.RawMessage(`{"note":""}`)}
			body, _ := json.Marshal(ev)
			_ = conn.Write(r.Context(), websocket.MessageText, body)
			_ = conn.Close(websocket.StatusNormalClosure, "task archived")
		case r.URL.Path == "/api/tasks/"+taskID && r.Method == http.MethodGet:
			_, _ = io.WriteString(w, `{"recent_events":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	return ts.URL, connReady, func() { relOnce.Do(func() { close(rel) }) }
}

// b396FlowEnv 建一张钉在「pl/impl 一个派发裁决节点」上的卡，并让该节点真的
// 走 StepRunner.Run（生产 runStepFn），只替换 transport 与 client。
func b396FlowEnv(t *testing.T, taskURL, taskID string) (*ledgerEnv, string) {
	t.Helper()
	env := newLedgerEnv(t)
	seedCardWithProject(t, env.srv, "handoff")
	seedDisciplineOnLedger(t, env, discipline.NameImplement, "本机测试实现纪律")
	if _, err := env.ledger.PutWorkflow("b396-flow", ledger.WorkflowDef{Nodes: []ledger.NodeDef{
		{Name: ledger.StatusTodo, Next: "impl"},
		{Name: "impl", Dispatch: true, Verdict: true, Template: "feature-impl", MaxRounds: 3, Next: ledger.StatusDone},
		{Name: ledger.StatusDone},
	}}); err != nil {
		t.Fatalf("写工作流: %v", err)
	}
	card, err := env.ledger.CreateCard(ledger.NewCard{
		Title: "B396 归档卡", Project: "handoff", Workflow: "b396-flow", Actor: "test",
	})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	env.srv.runStepFn = func(ctx context.Context, runner *ledgerstep.StepRunner, cardID, step string) {
		runner.Dispatcher.Transport = func(context.Context, ledgerstep.DispatchOpts) (string, string, error) {
			return taskID, "", nil
		}
		runner.Clients = func(string) (ledgerstep.StepClient, error) {
			return client.New(taskURL, "test-token"), nil
		}
		env.srv.runStep(ctx, runner, cardID, step)
	}
	return env, card.ID
}

// TestB396ArchivedTaskReleasesCardStepSlot 锁 B396 冻结条目 1/2：
// 节点等待的 task 被归档后，回合收口、槽位与运行锁释放、同卡同节点可重派；
// 且真在跑时并发重派仍 409（反例断言在场）。
//
// 红（当前 HEAD）：archived 不在 waitForTurnEnd 的终态集合里，Run 永不返回，
// cardStepFlight 永不清，第二次派发此后永远 409（本节点已在基线实跑）。
// 绿（T2）：waitForTurnEnd 把 archived 收口为终态错误，Run 落等人并释放锁与槽位。
// 变异复验：把 archived case 从 waitForTurnEnd 移除 → 本测试重新变红。
func TestB396ArchivedTaskReleasesCardStepSlot(t *testing.T) {
	const taskID = "task-b396-archived"
	url, connected, release := b396ArchivedTaskServer(t, taskID)
	env, cardID := b396FlowEnv(t, url, taskID)

	post := func() (int, string) {
		return ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/step",
			`{"step":"impl","actor":"cli:u@h#1"}`)
	}

	if code, body := post(); code != http.StatusAccepted {
		t.Fatalf("首次派发应 202，实得 %d（%s）", code, body)
	}
	<-connected
	if code, body := post(); code != http.StatusConflict {
		t.Fatalf("真在跑时并发重派应 409（互斥不得放开），实得 %d（%s）", code, body)
	}

	release()
	waitFor(t, func() bool { return !env.srv.cardStepInFlight(cardID) })
	if _, ok, err := env.ledger.RunLockOf(cardID); err != nil || ok {
		t.Fatalf("归档后运行锁行应已释放: ok=%v err=%v", ok, err)
	}
	// 归档不得静默：卡上要留一条 needs_human 说明（haltForHuman 的产物）。
	events, err := env.ledger.EventsFromAsc([]string{cardID}, 0, 100)
	if err != nil {
		t.Fatalf("读卡事件: %v", err)
	}
	var sawNeedsHuman bool
	for _, e := range events {
		if e.Type == ledger.EvNeedsHuman {
			sawNeedsHuman = true
		}
	}
	if !sawNeedsHuman {
		t.Fatalf("归档后回合应落 needs_human 说明（不得静默挂死），事件: %+v", events)
	}

	if code, body := post(); code != http.StatusAccepted {
		t.Fatalf("归档后同卡同节点重派应被受理，实得 %d（%s）", code, body)
	}
	waitFor(t, func() bool { return !env.srv.cardStepInFlight(cardID) })
}
```

- **Interfaces**
  - Consumes：见 §4 夹具清单与 §5 Consumes（全部既有符号）。
  - Produces：`TestB396ArchivedTaskReleasesCardStepSlot`（本文件）。
- **步骤**
  1. 落文件。
  2. 跑红：`go test ./internal/agentd/ -run TestB396ArchivedTaskReleasesCardStepSlot -count=1 -timeout 60s`
     → 预期 `--- FAIL ... b396_archived_step_test.go:129: 等待条件超时`（本节点已在副本实跑）。
     把原文抄进实现台账。
- **测试范围声明**：只跑 `./internal/agentd/`（本 task 只新增测试文件）。
- **日志与注释**：测试文件头写职责/边界/缝；两个 helper 写 why（为何每条连接都归档、
  为何必须走 `testhttp.NewServer`）；测试本体写红/绿/变异。

### T2 实现：`waitForTurnEnd` 认 `archived` 为终态

**文件**：`internal/ledgerstep/wire.go`。

**改动一（`:17` 之后新增包内哨兵）**：

```go
// errTurnArchived 表示节点等待的 task 已被归档（done 等外部动作），此后不会再
// 产出 completed/failed 裁决报文。它是一类明确的终止信号，不是普通断线——必须
// 让等待收口，否则运行锁与卡槽位永不释放（B396 陈旧运行锁不自愈）。
var errTurnArchived = errors.New("等待的 task 已归档，回合不会再产出裁决报文")
```

**改动二（`:64` 主循环 switch 首位插入 `archived` case）**。改前：

```go
		switch event.Type {
		case proto.EventTypeCompleted:
```

改后（完整，替换上块首两行）：

```go
		switch event.Type {
		case proto.EventTypeArchived:
			// B396：task 已被归档（外部 done/resume --force 后再 done），此后不会
			// 再产出 completed/failed 裁决报文。继续等只会把运行锁与卡节点槽位
			// 钉死，使同卡同节点永远 409。收口为可行动错误，由上游落等人标记。
			slog.WarnContext(ctx, "等待的 task 已归档，回合不会再有终态报文",
				"seq", event.Seq, "task", event.TaskID, "cause", errTurnArchived)
			return errTurnArchived
		case proto.EventTypeCompleted:
```

**改动三（`:114` 宽限循环插入一致性 case；P3 推荐甲）**。改前：

```go
		if event.Type == proto.EventTypeTurnFailed || event.Type == proto.EventTypeFailed {
			slog.InfoContext(ctx, "宽限内忽略失败事件，继续等待 completed", "event_type", event.Type)
		}
```

改后（在其前插入）：

```go
		if event.Type == proto.EventTypeArchived {
			slog.WarnContext(ctx, "宽限期内 task 已归档，回合不会再有终态报文",
				"seq", event.Seq, "task", event.TaskID, "cause", errTurnArchived)
			return errTurnArchived
		}
		if event.Type == proto.EventTypeTurnFailed || event.Type == proto.EventTypeFailed {
			slog.InfoContext(ctx, "宽限内忽略失败事件，继续等待 completed", "event_type", event.Type)
		}
```

- **Interfaces**
  - Consumes：`proto.EventTypeArchived`（`internal/proto/proto.go:115`）、
    `proto.Event`、`slog`、`errors`（均已 import）。
  - Produces：包内 `errTurnArchived`；`waitForTurnEnd` 对 `archived` 由「忽略」变
    「返回 `errTurnArchived`」（签名不变）。
- **步骤**
  1. 判据先在基线跑（复核）：T1 已红（原文在台账 §2）。
  2. 改代码块（三处）。
  3. 跑绿：`go test ./internal/agentd/ -run TestB396ArchivedTaskReleasesCardStepSlot -count=1 -timeout 60s`
     → 预期 `ok`（本节点已在副本实跑）。
  4. 跑编译与静态检查：`go build ./...`、`go vet ./internal/ledgerstep/ ./internal/agentd/`（预期 `EXIT=0`）。
- **加关键节点日志**：新分支各一条 `Warn`（如上），带 `seq`/`task`/`cause`——归档是
  外部动作、非本节点错误，级别取 Warn（可见性不缺；不误报为 Error）。**成功路径不静默**：
  收口后上游 `haltForHuman` 会落 `needs_human` + 评论（T1 已断言该事件在场）。
- **加注释**：`errTurnArchived` 与两个 case 的「为什么」（外部归档、永不产终态、否则钉死
  槽位与锁）已写进代码注释；`waitForTurnEnd` 函数头注释（`:42-52`）的终态集合描述需同步
  补一句「以及 task 归档（`archived`，B396）」，避免注释与行为漂移。
- **测试范围声明**：只跑 `./internal/agentd/`（行为断言在缝上）与 `./internal/ledgerstep/`（回归）。

### T3 变异复验 + 不误伤回归 + 并发反例在场

**变异复验（手动，不留代码）**：

1. 把 T2 改动二的主循环 `case proto.EventTypeArchived` 块**临时删掉**（改回忽略）；
2. `go test ./internal/agentd/ -run TestB396ArchivedTaskReleasesCardStepSlot -count=1 -timeout 60s`
   → **预期重新红**（本节点已证基线=红：台账 §3 原文 `等待条件超时`）；
3. 撤回临改，确认工作树只剩 T2 的正式改动。

**不误伤回归（跑既有测试，不新增）**：

```text
go test ./internal/ledgerstep/ -count=1 -timeout 300s
go test ./internal/agentd/ -run 'TestStartCardStep|TestCardStep|TestB23310|TestB393' -count=1 -timeout 300s
```

本节点已在副本实跑：`internal/ledgerstep` 全量绿；`internal/agentd` 目标子集绿。
`TestStartCardStepRejectsSecondInFlight` / `TestStartCardStepReleasesSlotOnFinish` /
`TestCardStepSecondReturns409` 均 PASS ⇒ 并发互斥与槽位释放语义不回归。

**并发反例在场**：T1 测试内已含 `真在跑时并发重派应 409` 断言（第二次 POST 在
`release()` 之前，此时槽位确在飞），无需另写；它就是「不许把互斥一起放开」的锁。

- **Interfaces**：无新增；改的是 T2 的同一处。
- **测试范围声明**：`./internal/ledgerstep/ ./internal/agentd/`。

---

## 7. 缺陷族对抗审查（五族 + 追加设问）

**族 1 生命周期/状态机中断**
- 归档到达节点等待后，回合收口路径与既有 `Await` 失败路径完全同形
  （`node.go:278-282` `haltForHuman`）：落评论 + `needs_human` + `Run` 返回 →
  `defer ReleaseRunLock` → goroutine `defer releaseCardStep`。T1 已断言
  `RunLockOf` 无行 + `cardStepInFlight` 为 false。
- **无新增运行时资源**：只多一个包内错误值。续租 goroutine 随 `Run` 返回的
  `defer close(done)`（`runner.go:237`）停止，不长留。
- 边角：归档与「正常 completed」竞态（同时到）时，`waitForTurnEnd` 先收到谁就先返回：
  先 completed 照常走裁决；先 archived 收口为等人。两者都是安全收口，不双写。

**族 2 静默失败 / 误导报错**
- 归档收口**不静默**：卡上落 `needs_human` + 评论（T1 断言）；日志 Warn。
- 反例保护：若误把 `completed` 也当成 archived 处理，T1 的归档后重派会过但既有
  `TestRunnerLocalClientUsesWaitAndDiffWire`/`TestB2336TerminalRunDoesNotBecomeBusinessVerdict`
  会红（它们要求 completed 走裁决/等人而非被 archived 抢先）——T3 回归覆盖。

**族 3 跨平台假设**
- 纯 switch case 增补，不引入平台假设。**无**。

**族 4 假红 / 假绿测试**
- T1 走真实 `startCardStep` → 真实 `StepRunner.Run` → 真实 `waitForTurnEnd`（假
  transport/client 只喂事件），非只测映射函数。
- 假绿防护：测试有三段——① 首次 202；② **真在飞时并发 409**（正例，防「什么都不拒」）；
  ③ 归档后槽位释放 + 运行锁无行 + needs_human + 重派 202。若有人把整个判据删成恒放行，
  ② 会红（互斥被放开）。
- 变异复验（T3 步骤）证明护栏可红。

**族 5 门禁绕过**
- 409 判据单一入口 `claimCardStep`（`cardstep.go:285`，唯二调用点
  `startCardStep` 与测试 helper `holdCardStep`）；`startCardStep` 是 HTTP 与小队
  准入共用的唯一节点入口。T2 不改判据，只让上游正确释放，无绕过面。
- 归档路径另有 `NodeStep.RunOnce` 的终态遗留裁决补解析（`node.go:207-229`），
  与本卡无交集（本卡是「等待途中归档」，不是「终态卡被再驱动」）。

**追加设问一：序列化边界**——本卡**不新增数据字段**，不改 `archived` payload
（`proto.ArchivedPayload{Note}` 不变）、不改任何 DTO/tag。唯一跨界是
`proto.Event.Type == proto.EventTypeArchived` 的取值判断，已被 T1 穿过真实
`proto.Event` 的解码（假 server 用 `json.Marshal(proto.Event{...})` 写、真实
WS 客户端解）。**无新增手写投影点，无风险**。

**追加设问二：枚举新值过既有白名单**——`EventTypeArchived` 是既有常量
（`proto.go:115`），无新增枚举，不触白名单。**无风险**。

**追加设问三：承重安全属性有测试锁住**——本卡承重属性是「归档后槽位/锁释放且可重派」
与「并发仍拒」：前者 T1 断言，后者 T1 的第二段断言；变异复验给前者可红证据（T3）。

---

## 8. 上下文预算检查

有界文件集（圈得出）：`internal/ledgerstep/wire.go`、
`internal/agentd/b396_archived_step_test.go`（新）、
`docs/superpowers/ledgers/2026-09-22-b396-plan-ledger.md`（本节点）。
不越出 `internal/ledgerstep` 与 `internal/agentd`。**通过**。

## 9. 类型标注 / 边界型子系统

非边界型子系统（单进程内终态集合的 switch 增补）。行为验收仍以显式真机清单给出
（§11，本 task 由协调者执行）。

## 10. 接缝覆盖（双向，对照 spec 测试决定的接缝清单）

spec 的接缝 = **409「该卡已有环节在运行」判据面**，即 HTTP
`POST /api/cards/{id}/step`（`internal/agentd/ledgerapi.go:447 handleCardStep`
→ `cardstep.go:128 startCardStep`）。

- **测试 → 缝**：`TestB396ArchivedTaskReleasesCardStepSlot`（T1）的**入口调用符号** =
  `ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/step", ...)`，落在该缝上；
  它不直调 `waitForTurnEnd`、不直调 `claimCardStep`。
- **缝 → 测试**：该缝被 T1 的缝级断言锁住（真在飞 → 409；归档后 → 202 且槽位/锁释放）。
  T3 回归里的 `TestCardStepSecondReturns409` / `TestStartCardStepRejectsSecondInFlight`
  同缝，锁并发互斥。
- **内部锁**：无。不存在「入口不在缝上」的测试。**通过**。

## 11. 真机清单（归协调者执行；本 task 由协调者执行，不派发）

1. 升级带 T2 改动的 agentd 到被锁死的机器后，对 B393（当前被锁死的卡）执行
   `handoff card dispatch B393 --step <节点>`：**应被受理（202）**，不再 409
   （spec §4-4）。
2. 对照：在对同一张卡、同一节点的真在跑回合中再次 `card dispatch --step`，**仍应
   409**（spec §4-2 反例不误伤）。
3. 观察卡事件流：归档收口后 `needs_human` 出现、`card wait` 可见；运行锁行消失。

> 真机需要在跑有 T2 改动的 agentd 的机器上执行，且依赖 B393 的卡/席位现状；
> 机内夹具验不了「线上 B393 真能重派」，故单列交协调者。

---

## 12. 占位符扫描

- 无 TBD / 「加适当的错误处理」/「同 Task N 而略」；T1–T3 的代码块与改动块均完整可抄。
- **例外声明（无）**：本计划不依赖「形态因包而异」的夹具复用，T1 的测试代码写全
  （夹具签名已在 §4 列出并亲跑），故不申请骨架测试例外。
- 条件退路：无（T3 的变异复验是显式步骤，不改变任何测试的入口符号）。
- **内部锁声明（无）**：T1 的测试入口在缝上，无内部锁。

## 13. 自审三查

1. **spec 覆盖（逐条指到 task）**：
   - §3-1 锁的写/清/TTL/owner、done/stop/resume --force 是否释放 → §1 R1/R2 逐条取证
     （写/清/TTL 见 `runlock.go`；409 判据见 `cardstep.go`；归档/resume 路径见 §1 R3）；
   - §3-2 409 判据读什么 → §1 R1（只读内存 `cardStepFlight`）；
   - §3-3 为何 25 分钟后仍拒 → §1 R2（续租保持锁 + 判据不读锁）；
   - §4-1 红色回路 → T1；§4-2 修后可重派 + 并发仍拒 → T2 + T1 第二段断言；
   - §4-3 变异复验 → T3；§4-4 真机 → §11（协调者执行，不派发）。
   - §2 不做：不改互斥语义（T2 不碰 409 判据）✓；不动其他卡/线上 ✓；不手改锁数据 ✓。
2. **占位符扫描**：见 §12，无占位。
3. **跨 task 类型/签名一致性**：T1 Produces `TestB396ArchivedTaskReleasesCardStepSlot`；
   T2 Produces `errTurnArchived`，T1 不直接消费它（只断言行为）；T2 不改
   `waitForTurnEnd` 签名，T1 经真实调用链到达。`proto.EventTypeArchived`、
   `ledgerstep.StepClient`、`ledger.EventsFromAsc/RunLockOf` 在 §5 与 T1 逐字一致。

## 图覆盖债（本节点发现，记入台账 §1）

- `codegraph --repo . sym runlock` / `sym errStepInFlight` 未命中（回落 grep 取
  源码与签名）；`flow waitForTurnEnd` degraded（基线无 flows 段），按纪律读源码，
  未拿 chain 冒充 flow。
- `codegraph context 运行锁` 不接受中文领域词（最优树候选为 d_gateway/d_ledger 等）。

> 未验证项：真机重放（§11）本节点未跑；`internal/agentd` 全量
> `TestPtyWSAttachedBacklogBytesKeyPresent` 的预存在 flake（干净基线 2/5 失败）
> 未定位，不属本卡。
