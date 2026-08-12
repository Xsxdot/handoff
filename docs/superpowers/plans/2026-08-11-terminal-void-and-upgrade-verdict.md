# 终态工单作废 + 升级结论收敛（B63 + B64）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让任务终结时不再留下答不掉的幽灵工单，让 `handoff upgrade` 的两条命令对同一台机器给出同一个结论。

**Architecture:** 两块互不重叠的改动。B63 把作废动作收口到 `Manager.transit` 的终态分支，配一条只入库不投递的 `tickets_voided` 审计事件。B64 把 `machineState.Managed` 改成三态指针，并抽出唯一的 `classify` 纯函数，让 `renderCheckRow`（只读巡检）与 `process`（`--now`）从同一个结论出发。

**Tech Stack:** Go 1.2x、标准库 `log/slog`、SQLite（`internal/store`）、cobra（`cmd`）。测试为白盒（`package agentd` / `package cmd`），无新增依赖。

**Spec:** [2026-08-11-terminal-void-and-upgrade-verdict-design.md](../specs/2026-08-11-terminal-void-and-upgrade-verdict-design.md)

## Global Constraints

- 日志一律用 `slog`（`m.log` / 传入的 `*slog.Logger` / `slog.Default()`），**禁止 `fmt.Printf` 当日志**。`fmt.Fprintf(out, ...)` 是给操作者看的命令输出，不是日志，两者都要有。
- 注释与日志文案用中文，与仓库现状一致。新文件必须有文件头注释（职责 + 边界），导出与非平凡的非导出函数必须有 doc 注释。
- 事件类型字符串是**线格式契约**：`tickets_voided`，一经写死不得改名。
- `VoidPendingTickets` 已幂等（第二次起返回 0），依赖这一点，不要自己再加去重。
- 每个 task 结束前跑 `gofmt -l .`（须无输出）、`go build ./...`、`go vet ./...`、`go test ./...`。
- 本计划不改 `RespondPermission`、不改五动作契约、不改 busy 闸的判据（只改它的触发位置）。

---

## File Structure

| 文件 | 动作 | 责任 |
|---|---|---|
| `internal/proto/proto.go` | 改 | 新增 `EventTypeTicketsVoided` 常量 |
| `internal/client/client.go` | 改 | `isDeliverable` 把新事件列为不可交付 |
| `internal/client/client_internal_test.go` | 改 | 钉住不可交付 |
| `internal/agentd/ticketvoid.go` | 建 | `voidTicketsWithAudit` 助手 + payload 结构，B63 的唯一实现处 |
| `internal/agentd/ticketvoid_test.go` | 建 | B63 全部单测 |
| `internal/agentd/manager.go` | 改 | `transit` 终态收口；`Stop` 删除显式作废 |
| `internal/agentd/reconcile.go` | 改 | `reconcileExecutorGone` 改用助手 |
| `internal/agentd/manager_test.go` | 改 | `Stop` 既有测试跟随调整 |
| `cmd/upgrade_verdict.go` | 建 | `verdict` 类型 + `classify` 纯函数，B64 的唯一判据处 |
| `cmd/upgrade_verdict_test.go` | 建 | `classify` 表驱动测试 |
| `cmd/upgrade.go` | 改 | `Managed` 改 `*bool`；`renderCheckRow` 与 `process` 改为消费 verdict |
| `cmd/upgrade_test.go` | 改 | `fakeMachine` 支持「不上报 Update / 不上报平台」；新增两边一致性测试 |

---

## Task 1: `tickets_voided` 事件类型与不可交付

**Files:**
- Modify: `internal/proto/proto.go`（`EventType` 常量块末尾）
- Modify: `internal/client/client.go:116-124`（`isDeliverable`）
- Test: `internal/client/client_internal_test.go`

**Interfaces:**
- Consumes: 无
- Produces: `proto.EventTypeTicketsVoided`（值 `"tickets_voided"`），Task 2 用它写事件

- [ ] **Step 1: 写失败测试**

在 `internal/client/client_internal_test.go` 末尾追加：

```go
// tickets_voided 是纯审计事件，绝不能可交付：它与 completed/failed 同时刻产生，
// 一旦可交付，一次性 wait 可能拿到一条审计噪音而不是任务真正的结论。
func TestTicketsVoidedIsNotDeliverable(t *testing.T) {
	if isDeliverable(proto.EventTypeTicketsVoided) {
		t.Error("tickets_voided 不应可交付，否则会抢走一次性 wait 的收手权")
	}
	// 对照组：终态事件必须仍可交付，别把黑名单写宽了
	if !isDeliverable(proto.EventTypeCompleted) {
		t.Error("completed 必须可交付")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/client/ -run TestTicketsVoidedIsNotDeliverable -v`
Expected: 编译失败，`undefined: proto.EventTypeTicketsVoided`

- [ ] **Step 3: 加常量**

在 `internal/proto/proto.go` 的 `EventType` 常量块末尾（`EventTypeDenyGuidanceDropped` 之后）加：

```go
	// EventTypeTicketsVoided 表示任务终结时把剩余挂起工单一并作废了（B63）。
	//
	// 为什么必须留痕：pending_tickets 是审核者接管陌生会话时「我还欠哪些没答」
	// 的权威清单，工单凭空消失与工单凭空挂着一样难排查——show 里要能回答
	// 「那张单是何时、因为什么被作废的」。
	//
	// **只入库不 Publish**，且在客户端不可交付（见 client.isDeliverable）：它与
	// completed/failed 同时刻产生，可交付就会抢走一次性 wait 的收手权。
	EventTypeTicketsVoided EventType = "tickets_voided"
```

- [ ] **Step 4: 加进不可交付名单**

`internal/client/client.go` 的 `isDeliverable`：

```go
func isDeliverable(t proto.EventType) bool {
	switch t {
	case proto.EventTypeProgress,
		proto.EventTypeApproverDecision,
		proto.EventTypeApproverDisabled,
		proto.EventTypeTicketsVoided:
		return false
	}
	return true
}
```

同时把该函数上方注释里的那行口径改成：

```go
// 可交付 = 全部类型 − {progress, approver_decision, approver_disabled, tickets_voided}。
```

并在注释末尾补一句 why：

```go
// tickets_voided（B63）加入的理由与前两类略有不同：它同样只入库不 Publish，但它
// 的产生时刻**恰好压在 completed/failed 上**——终态迁移的同一次调用里。可交付就
// 意味着一次性 wait 有机会拿它收手，审核者看到的是「作废了 1 张单」而不是任务成败。
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/client/ ./internal/proto/`
Expected: PASS

- [ ] **Step 6: 加注释自检**

本 task 无新文件、无新函数，注释已在 Step 3/4 内联完成。确认：常量有 why 注释、`isDeliverable` 的口径注释与代码一致（名单四项，注释也是四项）。

- [ ] **Step 7: 提交**

```bash
gofmt -l . && go build ./... && go vet ./...
git add internal/proto/proto.go internal/client/client.go internal/client/client_internal_test.go
git commit -m "feat(proto): 加 tickets_voided 审计事件类型，客户端不交付"
```

---

## Task 2: `voidTicketsWithAudit` 助手 + `transit` 终态收口

**Files:**
- Create: `internal/agentd/ticketvoid.go`
- Create: `internal/agentd/ticketvoid_test.go`
- Modify: `internal/agentd/manager.go:2471-2484`（`transit`）

**Interfaces:**
- Consumes: `proto.EventTypeTicketsVoided`（Task 1）
- Produces: `func voidTicketsWithAudit(st *store.Store, taskID, reason string, log *slog.Logger) int` —— 返回被作废的工单数；Task 3 的两个调用点复用它

- [ ] **Step 1: 写失败测试**

新建 `internal/agentd/ticketvoid_test.go`：

```go
// 本文件覆盖 B63：任务走到终态时统一作废剩余挂起工单。
//
// 测试为白盒（package agentd）：直接驱动 m.transit，绕开 Done/Stop 的前置门禁，
// 让每条用例只钉住「终态迁移 ⇒ 作废」这一件事。
package agentd

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// mustTaskWithTicket 造一个指定状态的任务，并挂一张未回答的 gate 工单。
func mustTaskWithTicket(t *testing.T, st *store.Store, id string, state proto.TaskState) {
	t.Helper()
	mustCreateTask(t, st, &proto.Task{ID: id, RepoPath: t.TempDir(), Executor: "fake",
		State: state, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	if _, err := st.CreateTicket(&proto.Ticket{ID: id + ":p1", TaskID: id, Kind: "gate",
		Request: []byte(`{"kind":"gate","permission":"bash: ls"}`), CreatedAt: time.Now()}); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
}

// pendingCount 返回任务当前挂起（未回答）工单数。
func pendingCount(t *testing.T, st *store.Store, taskID string) int {
	t.Helper()
	pending, err := st.PendingTickets(taskID)
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	return len(pending)
}

// voidedEvents 返回任务的全部 tickets_voided 事件。
func voidedEvents(t *testing.T, st *store.Store, taskID string) []proto.Event {
	t.Helper()
	evs, err := st.EventsFromAsc(taskID, 0, 100)
	if err != nil {
		t.Fatalf("EventsFromAsc: %v", err)
	}
	var got []proto.Event
	for _, e := range evs {
		if e.Type == proto.EventTypeTicketsVoided {
			got = append(got, e)
		}
	}
	return got
}

// 终态迁移必须作废剩余工单并留痕：否则审核者接管时会被引去 reply 一个必然 404 的 id。
func TestTransitToTerminalVoidsPendingTickets(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustTaskWithTicket(t, st, "T-void", proto.TaskStateWaitingReview)

	if err := m.transit("T-void", proto.TaskStateCompleted, "done"); err != nil {
		t.Fatalf("transit: %v", err)
	}

	if n := pendingCount(t, st, "T-void"); n != 0 {
		t.Errorf("终态后挂起工单 = %d，期望 0", n)
	}
	evs := voidedEvents(t, st, "T-void")
	if len(evs) != 1 {
		t.Fatalf("tickets_voided 事件 = %d 条，期望 1 条", len(evs))
	}
	var p ticketsVoidedPayload
	if err := json.Unmarshal(evs[0].Payload, &p); err != nil {
		t.Fatalf("解析 payload: %v", err)
	}
	if p.Voided != 1 || p.Reason != "done" {
		t.Errorf("payload = %+v，期望 {Voided:1 Reason:done}", p)
	}
}

// 回合结束（waiting_review）**不得**作废：grok/opencode 的提问中继就是「回合已结束、
// 人稍后 reply --answer 补答」，B3/B49 真机验过 relayed=true。这条是护栏。
func TestTransitToWaitingReviewKeepsPendingTickets(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustTaskWithTicket(t, st, "T-keep", proto.TaskStateRunning)

	if err := m.transit("T-keep", proto.TaskStateWaitingReview, "result"); err != nil {
		t.Fatalf("transit: %v", err)
	}

	if n := pendingCount(t, st, "T-keep"); n != 1 {
		t.Errorf("回合结束后挂起工单 = %d，期望 1（跨回合中继依赖它）", n)
	}
	if evs := voidedEvents(t, st, "T-keep"); len(evs) != 0 {
		t.Errorf("回合结束不该产出 tickets_voided，实得 %d 条", len(evs))
	}
}

// 迁移失败（非法/并发 CAS 输）时不得作废：任务还活着，砸掉的是它的合法工单。
func TestFailedTransitDoesNotVoid(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustTaskWithTicket(t, st, "T-bad", proto.TaskStatePending)

	// pending → completed 不在 transitTable 里，必然被拒
	if err := m.transit("T-bad", proto.TaskStateCompleted, "done"); err == nil {
		t.Fatal("pending → completed 应被拒绝")
	}

	if n := pendingCount(t, st, "T-bad"); n != 1 {
		t.Errorf("迁移失败后挂起工单 = %d，期望 1", n)
	}
	if evs := voidedEvents(t, st, "T-bad"); len(evs) != 0 {
		t.Errorf("迁移失败不该产出 tickets_voided，实得 %d 条", len(evs))
	}
}

// 没有挂起工单的正常收尾不产出事件：绝大多数任务如此，无条件发事件等于给每个
// 正常任务的事件流添噪音。
func TestTerminalWithoutTicketsIsSilent(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{ID: "T-quiet", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateWaitingReview, CreatedAt: time.Now(), UpdatedAt: time.Now()})

	if err := m.transit("T-quiet", proto.TaskStateCompleted, "done"); err != nil {
		t.Fatalf("transit: %v", err)
	}

	if evs := voidedEvents(t, st, "T-quiet"); len(evs) != 0 {
		t.Errorf("无挂起工单时不该产出 tickets_voided，实得 %d 条", len(evs))
	}
}

// 重复进终态（transit 的幂等分支）不得产出第二条事件。
func TestRepeatedTerminalTransitIsIdempotent(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustTaskWithTicket(t, st, "T-twice", proto.TaskStateWaitingReview)

	if err := m.transit("T-twice", proto.TaskStateCompleted, "done"); err != nil {
		t.Fatalf("首次 transit: %v", err)
	}
	if err := m.transit("T-twice", proto.TaskStateCompleted, "done"); err != nil {
		t.Fatalf("重复 transit 应幂等返回 nil: %v", err)
	}

	if evs := voidedEvents(t, st, "T-twice"); len(evs) != 1 {
		t.Errorf("tickets_voided = %d 条，期望 1 条", len(evs))
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestTransitTo|TestFailedTransit|TestTerminalWithout|TestRepeatedTerminal' -v`
Expected: 编译失败，`undefined: ticketsVoidedPayload`

- [ ] **Step 3: 写助手（新文件）**

新建 `internal/agentd/ticketvoid.go`：

```go
// 本文件实现「工单作废 + 留痕」这一个动作（B63）。
//
// 职责：
//   - 把一个任务的全部未回答工单作废，并按需产出一条 tickets_voided 审计事件
//   - 作为 transit 终态分支与 reconcileExecutorGone 的唯一共用实现
//
// 边界：
//   - 不判断「该不该作废」——时机由调用方决定（终态 / executor 已死）
//   - 不 Publish：tickets_voided 是纯审计事件，实时流上不出现（见 proto 常量注释）
//   - 不因作废或写事件失败而中断调用方：状态迁移已经发生，为一条审计写失败回滚
//     终态得不偿失
package agentd

import (
	"log/slog"

	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// ticketsVoidedPayload 是 tickets_voided 事件的 payload。
//
// Reason 直接沿用调用方的迁移原因（"done" / "stop" / 对账的那句人话），
// 让 show 里能回答「这批单是因为什么被作废的」。
type ticketsVoidedPayload struct {
	Voided int    `json:"voided"`
	Reason string `json:"reason"`
}

// voidTicketsWithAudit 作废任务的全部挂起工单，并在确实作废了东西时留一条审计事件。
//
// 参数：
//   - st: 存储
//   - taskID: 任务 ID
//   - reason: 作废原因，进事件 payload 与日志
//   - log: 日志入口
//
// 返回：
//   - 本次被作废的工单数；出错或无单可作废时为 0
//
// 注意：
//   - voided == 0 时**不产出事件**：绝大多数任务终结时本就没有挂起工单，
//     无条件写事件等于给每条正常事件流添噪音
//   - 依赖 VoidPendingTickets 的幂等（第二次起返回 0）来天然去重，本函数不另做判重
//   - 失败一律只记日志：见文件头「不中断调用方」
func voidTicketsWithAudit(st *store.Store, taskID, reason string, log *slog.Logger) int {
	voided, err := st.VoidPendingTickets(taskID)
	if err != nil {
		log.Error("作废挂起工单失败", "task", taskID, "reason", reason, "cause", err)
		return 0
	}
	if voided == 0 {
		return 0
	}
	log.Warn("挂起工单已作废", "task", taskID, "reason", reason, "voided", voided)
	if _, err := st.AppendEvent(taskID, proto.EventTypeTicketsVoided,
		ticketsVoidedPayload{Voided: voided, Reason: reason}); err != nil {
		log.Error("追加工单作废审计事件失败", "task", taskID, "voided", voided, "cause", err)
	}
	return voided
}
```

- [ ] **Step 4: 在 `transit` 里收口**

`internal/agentd/manager.go` 的 `transit`，把 `m.log.Info("任务状态迁移", ...)` 之后的 `return nil` 改成：

```go
	m.log.Info("任务状态迁移", "task", taskID, "from", cur.State, "to", to, "reason", reason)
	// 终态收口（B63）：done / stop / 各处 transitBestEffort 全部经过本函数，作废挂
	// 在这里才能覆盖**将来新增的**终态路径——B63 本身就是「新增一条路径时忘了补」
	// 漏出来的。
	//
	// 必须排在 UpdateTaskState 成功之后：该迁移可能因并发 CAS 失败（ErrBadTransit），
	// 那时任务仍然活着，先作废等于砸掉它的合法挂起工单。
	//
	// 幂等分支（cur.State == to）在上面已经 return，不会重复作废。
	if to.IsTerminal() {
		voidTicketsWithAudit(m.st, taskID, reason, m.log)
	}
	return nil
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestTransitTo|TestFailedTransit|TestTerminalWithout|TestRepeatedTerminal' -v`
Expected: 5 条全 PASS

- [ ] **Step 6: 加关键节点日志**

确认 `voidTicketsWithAudit` 的三条日志都在（本 task 的日志全部集中在这里，`transit` 自己的迁移日志已存在，不重复打）：

- 作废失败 → `Error`，带 task / reason / cause
- 确实作废了 → `Warn`，带 task / reason / voided（**成功路径不静默**：审核者的挂起清单被改动过，这件事必须能在日志里查到）
- 写审计事件失败 → `Error`，带 task / voided / cause

无单可作废时不打日志：那是绝大多数任务的正常路径，打了就是刷屏。

- [ ] **Step 7: 加注释**

确认：新文件 `ticketvoid.go` 有文件头（职责 + 三条边界）；`voidTicketsWithAudit` 有参数/返回/注意；`ticketsVoidedPayload` 说明 Reason 的来源；`transit` 里那段 why 注释解释了「为什么挂这里」与「为什么必须在 CAS 之后」。

- [ ] **Step 8: 提交**

```bash
gofmt -l . && go build ./... && go vet ./... && go test ./internal/agentd/
git add internal/agentd/ticketvoid.go internal/agentd/ticketvoid_test.go internal/agentd/manager.go
git commit -m "feat(agentd): 终态迁移统一作废挂起工单并留审计痕迹（B63）"
```

---

## Task 3: 收敛两处既有调用点

**Files:**
- Modify: `internal/agentd/manager.go:1094-1098`（`Stop` 的显式作废）
- Modify: `internal/agentd/reconcile.go:173-177`（`reconcileExecutorGone` 的显式作废）
- Modify: `internal/agentd/manager_test.go:1062+`（`TestStopEndsRunningTask`）

**Interfaces:**
- Consumes: `voidTicketsWithAudit`（Task 2）
- Produces: 无新接口

- [ ] **Step 1: 先改测试（`Stop` 现在也该留痕）**

`internal/agentd/manager_test.go` 的 `TestStopEndsRunningTask`，在既有断言之后追加：

```go
	// stop 走的是终态 failed，作废由 transit 收口完成，并且必须留下审计痕迹——
	// 否则 stop 与 done 两条终态路径的痕迹不一致，而痕迹不一致正是 B63 要修的东西
	if evs := voidedEvents(t, st, task.ID); len(evs) != 1 {
		t.Errorf("stop 后 tickets_voided = %d 条，期望 1 条", len(evs))
	}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestStopEndsRunningTask -v`
Expected: FAIL，`stop 后 tickets_voided = 0 条，期望 1 条`（此时 `Stop` 里的显式作废抢先把单清空了，轮到 `transit` 时 `voided == 0`，不产出事件）

- [ ] **Step 3: 删掉 `Stop` 里的显式作废**

`internal/agentd/manager.go` 的 `Stop`，删除这一段：

```go
	if voided, verr := m.st.VoidPendingTickets(taskID); verr != nil {
		m.log.Error("作废挂起工单失败", "task", taskID, "cause", verr)
	} else if voided > 0 {
		m.log.Warn("任务被中止，挂起工单作废", "task", taskID, "voided", voided)
	}
```

在原位置留一行 why 注释，防止后人「看着少了一步」又加回来：

```go
	// 挂起工单的作废交由 transit 的终态收口统一完成（B63）——在这里再做一遍会
	// 抢在收口之前把单清空，导致 stop 路径永远拿不到 tickets_voided 审计事件。
```

同步更新 `Stop` 的 doc 注释与文件头第 10 行的那句「stop：审核者主动中止任务（停 executor、作废工单、落 failed）」：把「作废工单」改成「作废工单（随终态迁移收口完成）」，别让注释与代码分叉。

- [ ] **Step 4: `reconcileExecutorGone` 改用助手**

`internal/agentd/reconcile.go`，把：

```go
	if voided, verr := st.VoidPendingTickets(taskID); verr != nil {
		log.Error("对账作废挂起工单失败，继续追加事件", "task", taskID, "cause", verr)
	} else if voided > 0 {
		log.Warn("对账作废挂起工单", "task", taskID, "voided", voided)
	}
```

换成：

```go
	// 复用终态收口的同一个助手（B63）：这条路径迁的是 waiting_review（非终态），
	// 走不到 transit 的终态分支，但「executor 已死 ⇒ 挂起工单不可能再被回答」的
	// 语义与终态一致，审计痕迹也该一致
	voidTicketsWithAudit(st, taskID, reason, log)
```

`reconcileExecutorGone` doc 注释里「作废工单排在事件之前，且作废失败只记日志不中断」那条保持不变——助手的行为与它一致，无需改。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/`
Expected: 全 PASS（重点看 `TestStopEndsRunningTask` 与 `reconcile_test.go` 既有用例）

- [ ] **Step 6: 加关键节点日志**

本 task 是**删除**重复实现，日志由助手统一提供。确认删除后没有丢失可观测性：

- `Stop` 路径：助手打的 `挂起工单已作废` 带 `reason=stop`，比原来那句 `任务被中止，挂起工单作废` 多了 reason 字段，信息不减。
- 对账路径：原来的 `对账作废挂起工单` 变成 `挂起工单已作废` + `reason=<对账那句人话>`，同样不减。
- `grep -rn "VoidPendingTickets" internal/ --include=*.go | grep -v _test` 应只剩 `ticketvoid.go` 一处调用。

- [ ] **Step 7: 加注释**

确认：`Stop` 原位置留了 why 注释、`Stop` doc 与 `manager.go` 文件头的措辞已同步、`reconcile.go` 替换处有 why 注释说明「非终态为何也用同一个助手」。

- [ ] **Step 8: 提交**

```bash
gofmt -l . && go build ./... && go vet ./... && go test ./internal/agentd/
git add internal/agentd/manager.go internal/agentd/reconcile.go internal/agentd/manager_test.go
git commit -m "refactor(agentd): 两处工单作废收敛到统一助手（B63）"
```

---

## Task 4: `Managed` 三态 + `classify` 纯函数

**Files:**
- Create: `cmd/upgrade_verdict.go`
- Create: `cmd/upgrade_verdict_test.go`
- Modify: `cmd/upgrade.go:117-125`（`machineState`）、`cmd/upgrade.go:245-250`（`probeMachine`）

本 task **只**引入判据，不改两个消费方——接线在 Task 5。改完 `Managed` 类型后 `process` 那处 `if !ms.Managed` 会编译不过，本 task 顺手把它改成 `if ms.Managed != nil && !*ms.Managed` 保持现状行为，Task 5 再整体替换。

**Interfaces:**
- Consumes: 无
- Produces:
  - `type verdict int` 与 7 个常量：`verdictUnreachable` / `verdictTooOld` / `verdictLatest` / `verdictAgentdDown` / `verdictUnmanaged` / `verdictManagedUnknown` / `verdictNeedsUpgrade`
  - `func (v verdict) String() string`
  - `func classify(ms *machineState, latest string) verdict`
  - `machineState.Managed` 类型变为 `*bool`

- [ ] **Step 1: 写失败测试**

新建 `cmd/upgrade_verdict_test.go`：

```go
// 本文件覆盖 B64 的判据层：classify 是两个消费方（renderCheckRow / process）
// 唯一的结论来源，优先级只在它里面定义一次。
package cmd

import (
	"errors"
	"testing"

	"github.com/xushixin/handoff/internal/client"
)

func boolPtr(b bool) *bool { return &b }

func TestClassify(t *testing.T) {
	const latest = "v0.1.1"
	cases := []struct {
		name string
		ms   machineState
		want verdict
	}{
		{
			name: "远端够不着：版本无从得知，其余判据一概不成立",
			ms:   machineState{Ep: Endpoint{Name: "devbox"}, Err: errors.New("dial tcp: connection refused")},
			want: verdictUnreachable,
		},
		{
			name: "本机 agentd 未运行：不是失败，敲命令的人知道要不要起回来",
			ms:   machineState{Ep: Endpoint{Name: "本机", Local: true}, Bin: "v0.1.0", Err: client.ErrStatusUnsupported},
			want: verdictAgentdDown,
		},
		{
			name: "本机 agentd 未运行但二进制已最新：没事可做，不该重下重换",
			ms:   machineState{Ep: Endpoint{Name: "本机", Local: true}, Bin: latest, Err: errors.New("connection refused")},
			want: verdictLatest,
		},
		{
			name: "远端过旧未上报平台：排在托管判定之前",
			ms:   machineState{Ep: Endpoint{Name: "devbox"}, Agentd: "v0.1.0", Platform: ""},
			want: verdictTooOld,
		},
		{
			name: "远端过旧且未上报托管：仍报过旧，不得报非托管（B64 原始症状）",
			ms:   machineState{Ep: Endpoint{Name: "devbox"}, Agentd: "v0.1.0", Platform: "", Managed: nil},
			want: verdictTooOld,
		},
		{
			name: "远端非托管但已是最新：没事可做，不该催人装 service",
			ms: machineState{Ep: Endpoint{Name: "devbox"}, Agentd: latest,
				Platform: "linux/amd64", Managed: boolPtr(false)},
			want: verdictLatest,
		},
		{
			name: "远端有活跃任务但已是最新：busy 不参与判据，只在 needsUpgrade 后成为闸",
			ms: machineState{Ep: Endpoint{Name: "devbox"}, Agentd: latest,
				Platform: "linux/amd64", Managed: boolPtr(true), Busy: 3},
			want: verdictLatest,
		},
		{
			name: "远端明确上报非托管且落后：换完没人拉起，硬拒",
			ms: machineState{Ep: Endpoint{Name: "devbox"}, Agentd: "v0.1.1-rc", Platform: "linux/amd64",
				Managed: boolPtr(false)},
			want: verdictUnmanaged,
		},
		{
			name: "上报了平台却没上报托管：不知道就是不知道，不猜",
			ms: machineState{Ep: Endpoint{Name: "devbox"}, Agentd: "v0.1.1-rc", Platform: "linux/amd64",
				Managed: nil},
			want: verdictManagedUnknown,
		},
		{
			name: "远端托管且落后：正常升级路径",
			ms: machineState{Ep: Endpoint{Name: "devbox"}, Agentd: "v0.1.1-rc", Platform: "linux/amd64",
				Managed: boolPtr(true)},
			want: verdictNeedsUpgrade,
		},
		{
			name: "本机落后：二进制与 agentd 都要对齐才算最新",
			ms: machineState{Ep: Endpoint{Name: "本机", Local: true}, Bin: latest, Agentd: "v0.1.0",
				Platform: "darwin/arm64", Managed: boolPtr(true)},
			want: verdictNeedsUpgrade,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classify(&c.ms, latest); got != c.want {
				t.Errorf("classify = %s，期望 %s", got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run TestClassify -v`
Expected: 编译失败，`undefined: classify` / `undefined: verdict`

- [ ] **Step 3: 写判据（新文件）**

新建 `cmd/upgrade_verdict.go`：

```go
// 本文件是 handoff upgrade 的**唯一判据来源**（B64）。
//
// 职责：
//   - 把一台机器的探测结果收敛成单一结论（verdict）
//   - 结论之间的优先级在此定义一次，供只读巡检与 --now 两个消费方共用
//
// 边界：
//   - 纯函数：不做 I/O、不打日志、不产出面向操作者的文案
//   - 不判 busy：活跃任务是「要不要现在换」的闸，不是「这台机器是什么状态」的
//     结论；它只在 verdictNeedsUpgrade 之后由 process 施加（spec §4.3）
//
// 为什么必须只有一处：B64 的病根是 renderCheckRow 与 process 各维护一套分支表，
// 两套的分支集合与优先级不一致，于是同一台机器有两套说法。
package cmd

// verdict 是一台机器的唯一结论。
type verdict int

const (
	// verdictUnreachable 远端够不着：版本、平台、托管状态一概无从得知。
	verdictUnreachable verdict = iota
	// verdictAgentdDown 本机 agentd 没在跑。不是失败——敲命令的人知道自己
	// 要不要把它起回来。
	verdictAgentdDown
	// verdictTooOld 远端过旧，连平台都不上报。
	verdictTooOld
	// verdictLatest 已是最新，无事可做。
	verdictLatest
	// verdictUnmanaged 对端**明确上报**非托管：换完 exit(0) 没人拉起。
	verdictUnmanaged
	// verdictManagedUnknown 对端上报了平台却没上报托管状态：不知道就是不知道。
	verdictManagedUnknown
	// verdictNeedsUpgrade 该升级，且没有已知障碍。
	verdictNeedsUpgrade
)

// String 让 verdict 在日志与测试失败信息里可读。
func (v verdict) String() string {
	switch v {
	case verdictUnreachable:
		return "unreachable"
	case verdictAgentdDown:
		return "agentd_down"
	case verdictTooOld:
		return "too_old"
	case verdictLatest:
		return "latest"
	case verdictUnmanaged:
		return "unmanaged"
	case verdictManagedUnknown:
		return "managed_unknown"
	case verdictNeedsUpgrade:
		return "needs_upgrade"
	}
	return "unknown"
}

// classify 把探测结果收敛成单一结论。
//
// 参数：
//   - ms: 一台机器的探测结果（probeMachine 的产物）
//   - latest: 最新发布的 tag
//
// 返回：
//   - 该机器的唯一结论
//
// 优先级（顺序即判据，改动前先读完这段）：
//  1. 够不着 / 本机 agentd 没跑——探测本身没拿到东西，但两者含义不同：远端够不着
//     连版本都不知道；本机 agentd 没跑时二进制版本仍然已知，所以下一条还能生效
//  2. 远端过旧（未上报平台）——排在托管判定之前：连平台都不上报的对端，它的托管
//     状态同样不可信，报「非托管」会把人引去装一个救不了它的 service（B64 原症状）
//  3. 已是最新——排在托管判定之前：没事可做时不该催人装 service，也不该重下重换
//  4. 明确非托管 → 不知道是否托管 → 该升级
func classify(ms *machineState, latest string) verdict {
	if ms.Err != nil && !ms.Ep.Local {
		return verdictUnreachable
	}
	if !ms.Ep.Local && ms.Platform == "" {
		return verdictTooOld
	}
	if ms.isLatest(latest) {
		return verdictLatest
	}
	// 排在 isLatest 之后：本机 agentd 没跑时 Agentd 为空、isLatest 只比二进制，
	// 已经最新就没事可做——不必为了「把它起回来」先重下一遍同版本再换一次文件
	if ms.Err != nil {
		return verdictAgentdDown
	}
	if ms.Managed != nil && !*ms.Managed {
		return verdictUnmanaged
	}
	if ms.Managed == nil {
		return verdictManagedUnknown
	}
	return verdictNeedsUpgrade
}
```

- [ ] **Step 4: `Managed` 改三态**

`cmd/upgrade.go` 的 `machineState`：

```go
type machineState struct {
	Ep       Endpoint
	Bin      string
	Agentd   string
	Platform string // 对端上报的平台；空 = 对端过旧未上报
	// Managed 是对端上报的「agentd 由进程管理器拉起」状态。
	//
	// 为什么是指针：nil 表示**对端没给这个字段**（老 agentd 不上报 Update），
	// 与「对端说 false」是两回事。用 bool 零值把前者塌成后者，就会把「我不知道」
	// 讲成「它非托管」，并据此给出一条注定白折腾的处置建议——B64 就是这么来的。
	// 与同结构里 ActiveTask.Watchers *int 是同一条纪律。
	Managed *bool
	Busy    int
	Err     error
}
```

`probeMachine` 的 default 分支：

```go
		if st.Update != nil {
			managed := st.Update.Managed
			ms.Managed = &managed
		}
```

- [ ] **Step 5: 让 `process` 先编译过（临时保持现状行为）**

`cmd/upgrade.go:352` 的闸二判断改为：

```go
	if ms.Managed != nil && !*ms.Managed {
```

Task 5 会把整个分支表换掉，这里只是让本 task 可独立编译、可独立提交。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./cmd/ -run TestClassify -v && go test ./cmd/`
Expected: `TestClassify` 全部子用例 PASS；`cmd` 包既有测试保持 PASS

- [ ] **Step 7: 加关键节点日志**

`classify` 是纯函数，**不在它内部打日志**（打了就是在 `--check` 的每台机器上刷屏）。判据的可观测性由 Task 5 在 `process` 入口打一行带 verdict 的 Info 提供。本 step 只需确认：`probeMachine` 既有的「开始探测机器」「对端 agentd 过旧，未上报平台」「探测机器失败」三条日志未被本次改动破坏。

- [ ] **Step 8: 加注释**

确认：新文件有文件头（职责 + 两条边界 + 为什么必须只有一处）；7 个 verdict 常量每个一句含义；`classify` doc 里的优先级四条按顺序写清 why；`machineState.Managed` 的指针 why 注释到位。

- [ ] **Step 9: 提交**

```bash
gofmt -l . && go build ./... && go vet ./... && go test ./cmd/
git add cmd/upgrade_verdict.go cmd/upgrade_verdict_test.go cmd/upgrade.go
git commit -m "feat(cmd): 升级结论收敛为单一 classify 判据，Managed 改三态（B64）"
```

---

## Task 5: 两个消费方接线 + 两边一致性测试

**Files:**
- Modify: `cmd/upgrade.go:267-292`（`renderCheckRow`）、`cmd/upgrade.go:326-398`（`process`）
- Modify: `cmd/upgrade_test.go:24-56`（`fakeMachine` / `fakePeer`）、末尾新增一致性测试

**Interfaces:**
- Consumes: `classify` / `verdict`（Task 4）
- Produces: 无新接口（`renderCheckRow` 与 `process` 签名不变）

- [ ] **Step 1: 扩 fake 并写失败测试**

`cmd/upgrade_test.go` 的 `fakeMachine` 加两个字段：

```go
type fakeMachine struct {
	platform  string
	version   string
	managed   bool
	noUpdate  bool // 不上报 Update 字段（模拟只报平台不报托管的对端）
	busy      int
	statusErr error
	pushErr   error
	waitErr   error
	pushed    bool
}
```

`fakePeer.Status` 改为按 `noUpdate` 决定是否带 `Update`：

```go
	resp := &proto.StatusResp{
		Version: proto.BuildInfo{Version: p.m.version, Platform: p.m.platform},
	}
	if !p.m.noUpdate {
		resp.Update = &proto.UpdateStatus{Managed: p.m.managed}
	}
```

在 import 块里补一行（`strings` 已在，`client` 未在）：

```go
	"github.com/xushixin/handoff/internal/client"
```

在文件末尾追加一致性测试：

```go
// 两边一致性：同一台机器，handoff upgrade（只读）与 handoff upgrade --now 必须
// 从同一个结论出发。B64 的病根就是两套分支表各活各的——这条测试是它的护栏，
// 其余用例是它的分解。
func TestCheckAndNowAgreeOnEveryMachine(t *testing.T) {
	const plat = "linux/amd64"
	cases := []struct {
		name      string
		m         fakeMachine
		wantCheck string // 巡检行必须含
		wantNow   string // --now 输出必须含
		denyNow   string // --now 输出**不得**含；空串表示不检查
	}{
		{
			name:      "过旧未上报平台（B64 原始症状：曾被报成非托管）",
			m:         fakeMachine{statusErr: client.ErrStatusUnsupported},
			wantCheck: "对端过旧",
			wantNow:   "对端 agentd 过旧",
			denyNow:   "service install",
		},
		{
			name:      "上报平台但不上报托管：不知道就说不知道",
			m:         fakeMachine{platform: plat, version: "v0.1.0", noUpdate: true},
			wantCheck: "需要升级",
			wantNow:   "未上报托管状态",
			denyNow:   "service install",
		},
		{
			name:      "非托管且落后：硬拒并给对症建议",
			m:         fakeMachine{platform: plat, version: "v0.1.0", managed: false},
			wantCheck: "需要升级",
			wantNow:   "service install",
		},
		{
			name:      "非托管但已是最新：没事可做，不催装 service",
			m:         fakeMachine{platform: plat, version: "v0.1.1", managed: false},
			wantCheck: "已是最新",
			wantNow:   "已是最新",
			denyNow:   "service install",
		},
		{
			name:      "有活跃任务但已是最新：不建议一条注定白跑的 --force",
			m:         fakeMachine{platform: plat, version: "v0.1.1", managed: true, busy: 2},
			wantCheck: "已是最新",
			wantNow:   "已是最新",
			denyNow:   "--force",
		},
		{
			name:      "托管且落后：正常升级",
			m:         fakeMachine{platform: plat, version: "v0.1.0", managed: true},
			wantCheck: "需要升级",
			wantNow:   "成功",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			machines := map[string]*fakeMachine{
				"__本机": {platform: plat, version: "v0.1.1", managed: true},
				"devbox": &c.m,
			}
			check := runUpgradeCheck(t, machines)
			if !strings.Contains(check, c.wantCheck) {
				t.Errorf("check 输出缺 %q：\n%s", c.wantCheck, check)
			}
			machines2 := map[string]*fakeMachine{
				"__本机": {platform: plat, version: "v0.1.1", managed: true},
				"devbox": &c.m,
			}
			now := runUpgradeNow(t, machines2)
			if !strings.Contains(now, c.wantNow) {
				t.Errorf("--now 输出缺 %q：\n%s", c.wantNow, now)
			}
			if c.denyNow != "" && strings.Contains(now, c.denyNow) {
				t.Errorf("--now 输出不该含 %q：\n%s", c.denyNow, now)
			}
		})
	}
}
```

（`runUpgradeNow` 在失败时会 `t.Fatal`，「托管且落后」那条走 fake 的成功换版路径，其余条目全部在闸上收手，不会真的推送。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run TestCheckAndNowAgree -v`
Expected: FAIL —— 至少「过旧未上报平台」（`--now` 输出含 `service install`）、「非托管但已是最新」、「有活跃任务但已是最新」三条不过

- [ ] **Step 3: `renderCheckRow` 改为消费 verdict**

`cmd/upgrade.go`：

```go
// renderCheckRow 渲染巡检表的一行（--check 默认行为）。
//
// 行内三段：名字（定宽）、信息、结论。结论一律来自 classify，本函数只负责把
// verdict 翻译成一句话——判据不在这里，改判据请改 classify（B64）。
//
// 本机必须分别显示二进制与 agentd 两个版本——换掉磁盘上的文件后正在跑的 agentd
// 仍是旧进程，这是正常且常见的中间态，合成一个数字就必然骗人（B59 spec §4.1）。
func renderCheckRow(w io.Writer, s machineState, latest string) {
	name := s.Ep.Name
	v := classify(&s, latest)
	if v == verdictUnreachable {
		fmt.Fprintf(w, "%-8s 够不着（%s）\n", name, s.Err)
		return
	}
	info := s.Agentd
	if s.Ep.Local {
		info = "二进制 " + dispVer(s.Bin)
		if s.Agentd == "" {
			info += " · agentd 未运行"
		} else {
			info += " · agentd " + s.Agentd
		}
	}
	fmt.Fprintf(w, "%-8s %s%s%s\n", name, info, checkPad(info), checkConclusion(v))
}

// checkConclusion 把结论翻译成巡检表里的一句话。
//
// 只有「已是最新」与「对端过旧」值得单独措辞：其余几格（非托管、未上报托管、
// 该升级）在只读巡检下的行动含义相同——都是「这台机器落后了」，差别体现在
// --now 的处置里，不该在巡检表上提前吓人。
func checkConclusion(v verdict) string {
	switch v {
	case verdictLatest:
		return "已是最新"
	case verdictTooOld:
		return "对端过旧（未上报平台）"
	default:
		return "需要升级"
	}
}
```

- [ ] **Step 4: `process` 改为消费 verdict**

`cmd/upgrade.go` 的 `process` 整体替换为：

```go
// process 对一台机器执行升级，返回结果分类。
//
// 结论来自 classify（唯一判据），本函数只负责把结论翻译成动作与处置建议。
// busy 闸不在 classify 里：它是「要不要现在换」的闸，只在确实需要换版时才成立
// ——否则会对一台已是最新的忙机器建议 --force，而那条命令跑完只会说「已是最新」。
func (ms *machineState) process(ctx context.Context, cmd *cobra.Command, rel release.Release) outcome {
	out := cmd.OutOrStdout()
	name := ms.Ep.Name
	peer := newAgentdClient(ms.Ep)
	v := classify(ms, rel.Tag)
	slog.Default().Info("开始处理机器", "name", name, "addr", ms.Ep.Addr, "local", ms.Ep.Local,
		"verdict", v.String(), "platform", ms.Platform, "busy", ms.Busy)

	switch v {
	case verdictUnreachable:
		// 只报原始错误原文，不编处置——编一条建议就是在猜，而猜出来的建议会把人
		// 引到错误的方向
		slog.Default().Warn("机器够不着", "name", name, "cause", ms.Err)
		fmt.Fprintf(out, "%-8s 失败   %s\n", name, ms.Err)
		return outcomeFail

	case verdictLatest:
		slog.Default().Info("跳过：已是最新", "name", name)
		fmt.Fprintf(out, "%-8s 跳过   已是最新\n", name)
		return outcomeSkip

	case verdictTooOld:
		// 明确拒绝而不是猜一个默认平台——猜错就是给一台 linux 机器推 darwin 二进制
		slog.Default().Info("跳过：对端未上报平台", "name", name)
		fmt.Fprintf(out, "%-8s 跳过   对端 agentd 过旧，未上报平台，需先手工升级到 ≥v0.1.1\n", name)
		return outcomeSkip

	case verdictAgentdDown:
		return ms.swapAndTell(ctx, out, rel, "agentd 未运行，请自行重启它")

	case verdictUnmanaged:
		if ms.Ep.Local {
			return ms.swapAndTell(ctx, out, rel, "agentd 非托管启动，请自行重启它")
		}
		slog.Default().Info("跳过：agentd 非托管", "name", name)
		fmt.Fprintf(out, "%-8s 跳过   agentd 非托管启动，重启后不会被拉起\n", name)
		// 不给 --force：它不越过闸二，给了就是让用户跑一条注定失败的命令
		fmt.Fprintf(out, "         先在该机器上 handoff service install\n")
		return outcomeSkip

	case verdictManagedUnknown:
		if ms.Ep.Local {
			return ms.swapAndTell(ctx, out, rel, "无法确认 agentd 是否托管启动，请自行重启它")
		}
		// 不猜托管（猜错=换完没人拉起，这台机器就此没有 agentd 且无人知晓），
		// 也不猜非托管（猜错=把人引去装一个可能早已装好的 service，即 B64 原症状）
		slog.Default().Info("跳过：对端未上报托管状态", "name", name)
		fmt.Fprintf(out, "%-8s 跳过   对端未上报托管状态，无法确认换版后能否被拉起\n", name)
		return outcomeSkip
	}

	// verdictNeedsUpgrade：闸一（活跃任务，--force 可越过）
	if ms.Busy > 0 && !upgradeForce {
		slog.Default().Info("跳过：有活跃任务", "name", name, "busy", ms.Busy)
		fmt.Fprintf(out, "%-8s 跳过   %d 个活跃任务\n", name, ms.Busy)
		if ms.Ep.Local {
			fmt.Fprintf(out, "         handoff upgrade --now --force\n")
		} else {
			fmt.Fprintf(out, "         handoff upgrade --now --target %s --force\n", name)
		}
		return outcomeSkip
	}
	if ms.Ep.Local {
		return ms.localUpgrade(ctx, out, peer, rel)
	}
	return ms.remoteUpgrade(ctx, out, peer, rel)
}

// swapAndTell 只换本机文件、不触发重启，并如实说明「为什么要你自己重启」。
//
// 三种本机情形共用（agentd 未运行 / 非托管 / 托管状态未知）：都不能靠接口重启，
// 差别只在给操作者的那句理由，所以理由由调用方传入。
func (ms *machineState) swapAndTell(ctx context.Context, out io.Writer, rel release.Release, why string) outcome {
	oc := ms.localSwap(ctx, out, rel)
	if oc == outcomeOK {
		fmt.Fprintf(out, "%-8s 成功   已换文件；%s\n", ms.Ep.Name, why)
	}
	return oc
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./cmd/ -v`
Expected: `TestCheckAndNowAgreeOnEveryMachine` 全部子用例 PASS；`cmd` 包既有测试全部 PASS。

若 `TestUpgradeRefusesUnknownPlatform` / `TestUpgradeUnmanagedNeverOffersForce` 因文案变化而失败，按新文案更新它们的断言——但**先确认文案变化是设计意图**（过旧那条现在带「≥v0.1.1」）而不是判据写错了。

- [ ] **Step 6: 加关键节点日志**

确认 `process` 每条出口都有日志，且入口那行带 verdict：

- 入口 `开始处理机器`：新增 `verdict` / `platform` / `busy` 字段 —— 这一行是排障时最想要的一行，「为什么这台机器被这样处置」一眼可答
- 够不着 → `Warn`；其余四条跳过分支各一条 `Info`（带 name，非托管额外带原因）
- `localUpgrade` / `remoteUpgrade` 内部既有的成功/失败日志保持不变，不重复打

**成功路径不静默**：升级成功由 `localUpgrade` / `remoteUpgrade` 各自打 Info（`本机升级完成` / `新版本已上线`），跳过路径由本 step 的 Info 覆盖，无出口静默。

- [ ] **Step 7: 加注释**

确认：`renderCheckRow` doc 说明「判据不在这里」；`checkConclusion` 说明为什么只有两格单独措辞；`process` doc 说明 busy 闸为何不进 classify；`swapAndTell` doc 说明三种情形共用与理由外传；`verdictManagedUnknown` 分支有「两个方向都不猜」的 why。

- [ ] **Step 8: 提交**

```bash
gofmt -l . && go build ./... && go vet ./... && go test ./...
git add cmd/upgrade.go cmd/upgrade_test.go
git commit -m "fix(cmd): 巡检与 --now 共用同一结论，过旧不再被报成非托管（B64）"
```

---

## Task 6: 全量自检与真机验收

**Files:** 无代码改动（只在验收失败时才回到前面的 task）

**Interfaces:**
- Consumes: Task 1-5 的全部产物
- Produces: 回填 backlog 所需的验收证据

- [ ] **Step 1: 本地全量自检**

```bash
gofmt -l . && go build ./... && go vet ./... && go test ./...
```

Expected: `gofmt -l .` 无输出；其余全绿。

- [ ] **Step 2: 竞态自检**

```bash
go test -race ./internal/agentd/ ./cmd/
```

Expected: 全绿。（`internal/agentd` 是本批唯一有并发的改动面。）

- [ ] **Step 3: 造两台「过旧」旧实例（B64 真机）**

在远程执行机上，两份旧二进制现编，**各自独立端口与 DataDir，不碰生产的 7777 与 `~/.handoff`**：

```bash
git worktree add --detach /tmp/handoff-old-p1 c558f240 && (cd /tmp/handoff-old-p1 && go build -o /tmp/handoff-p1 .)
git worktree add --detach /tmp/handoff-old-p2 v0.1.0 && (cd /tmp/handoff-old-p2 && go build -o /tmp/handoff-p2 .)
```

P1（`c558f240`，`70d147d3` 之前，**没有 `/api/status`**）起在 7788 + `~/.handoff-b64-p1`；P2（`v0.1.0`，有 status、有 `Update.Managed`、无 `Platform`）起在 7789 + `~/.handoff-b64-p2`。两台按各自版本的 agentd 启动参数拉起（旧版参数名可能与当前不同，以 `--help` 为准）。

- [ ] **Step 4: B64 两画像验收**

在本机 target 配置里临时加 `b64p1` / `b64p2` 两个 target 指向上面两个端口，然后：

```bash
handoff upgrade
handoff upgrade --now --target b64p1
handoff upgrade --now --target b64p2
```

**`--now` 一律带 `--target` 指名道姓，绝不裸跑**——裸跑会把本机与生产 devbox 一起换版。

逐项核对：

1. P1 在 `upgrade`（只读）与 `--now` 两边**都**报「对端过旧」；`--now` 的输出里**没有** `service install`（这是 B64 的原始错误建议，也是本次修复的判定点）。
2. P2 同样两边都报「对端过旧」（回归护栏：它在修复前就是对的，修复后不得变坏）。
3. 两台都没有被真的推送二进制（旧实例的日志里没有 `/api/update` 请求；`handoff --version` 在旧实例侧不变）。

- [ ] **Step 5: B63 真机验收**

在生产实例上：派发一个任务 → 让它提一个问题、**不作答** → 等它进 `waiting_review` → `handoff done <id>`。核对三件事：

1. `handoff show <id>` 的 `pending_tickets` 为空；
2. 事件流里有一条 `tickets_voided`，payload 的 `voided` 与实际挂起数一致、`reason` 为 `done`；
3. 另开一个终端在 `done` 之前挂 `handoff wait <id>`，它收到的是 `completed` 而**不是** `tickets_voided`。

- [ ] **Step 6: 清理**

```bash
handoff stop <残留任务 id>   # 如有
```

停掉两台旧实例进程，删除 `~/.handoff-b64-p1` / `~/.handoff-b64-p2` 与 `/tmp/handoff-old-p*` 两个 worktree，并从 target 配置里移除 `b64p1` / `b64p2`。

- [ ] **Step 7: 回填 backlog**

`docs/superpowers/backlog.md` 的 B63 与 B64 两行：状态转 `✅ done(已验)`，`Spec` 列填本 spec 链接，`验收` 列写 Step 1/2 的命令与结果 + Step 4/5 的真机观察。

**如实记录 `managedUnknown` 未被真机覆盖**：该分支在已发布版本里不可达（`Update` 在 v0.1.0 就有、`Platform` 到 v0.1.1 才加，不存在只报前者的版本），由 `TestClassify` 的「上报了平台却没上报托管」一条钉住。不为它伪造真机证据。

- [ ] **Step 8: 提交**

```bash
git add docs/superpowers/backlog.md
git commit -m "docs(backlog): B63/B64 真机验收通过，转 done(已验)"
```

---

## 自检记录

**Spec 覆盖**：§3.1-3.4 → Task 1/2；§3.6 → Task 3；§3.5 的「不做」由 Task 2 Step 1 的护栏用例钉住；§4.1 → Task 4 Step 4；§4.2 → Task 4 Step 3；§4.3/4.4 → Task 5；§4.5（`remoteUpgrade` 自愈）无需代码改动，由 Task 4 的 `classify` 保证 `Platform == ""` 到不了那里，并由 Task 5 的一致性测试「过旧」一条间接覆盖；§5 → Task 1/2/4/5 各自的测试步；§6 → Task 6。

**已知的口径变化**（实现时会看到，不是 bug）：`process` 里「对端过旧」的文案新增了「≥v0.1.1」，`cmd` 包既有测试若断言旧文案需同步更新。
