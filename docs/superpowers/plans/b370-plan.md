# B370 实现计划：镜像身份闸降级判定（能判定才拦）

状态：可执行实现计划（2026-09-15）
卡：B370
标题：镜像身份闸降级判定：旧 envelope 缺 node/attempt 投影时改用 source 列匹配
定级：**L3 轻档**；路由：contract → breakdown →（单轮）implement → review → acceptance → finish
有效基线：`cards/B233.1-charter-7`（本卡合并目标；当前分支 `cards/B370-charter-7`，不切换、不越过）
输入：`docs/superpowers/specs/b370.md`、`docs/superpowers/specs/b370-contract.md`、`docs/superpowers/specs/b370-breakdown.md`
拍板：breakdown §9 F1–F7 **全 A**，本计划逐条吸收，不重开
台账：`docs/superpowers/ledgers/2026-09-15-b370-plan-ledger.md`

本计划把 B349 冻结的「缺身份即拒绝」反转为「能证明迟到才拦」：身份缺失/为空时改用 source 列与当前派发快照比对；判定正文收在 `internal/client` 一处共享符号；两份近重复快照扫描退役；两个消费点（`card wait` stdout 与 agentd 自动化唤醒）各补按 reason 的日志。本计划不新增事件类型、不加 wire 字段、不改 HTTP/前端契约、不扩 `mirrorSkip`、不把判定下沉成账本派生查询。

---

## 0. 执行者须知

- **不派发、不调用 handoff CLI、不起任何新 executor 进程或子任务。** 本计划的所有验收步骤都在本机 Go/codegraph/命令内完成。
- 只在当前分支 `cards/B370-charter-7` 工作；不切分支、不改 git 配置、不 push。
- 有图先查图：本仓有 `codegraph/`，查符号用
  `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . sym <名字>`（本计划已亲跑，见 §2）。图未覆盖的符号按源码锚，记入 §2.3「图覆盖债」，不改写符号名。
- **边干边落台账**：每确立一个事实追加一行到 `docs/superpowers/ledgers/2026-09-15-b370-plan-ledger.md`（本节点已建；实现轮追加到同目录实现台账，命名 `2026-09-15-b370-implement-ledger.md`）。台账是仓内文件，与产出物同批提交。提交事实的记法：把提交当时的命令与原始输出追加进台账，随后只 amend 一次收进同批提交；amend 会换 hash，收口判据是工作树干净，不是文件里的 hash 等于 HEAD。
- 生产文件只许改 §3.1 列出的集合；出现集合外生产改动，停在当前 task 退回协调者重核边界，不得以「同目录」放宽。

---

## 1. 目标、冻结边界与执行顺序

本轮实现四件事，顺序固定在单卡单轮内（F6=A）：

1. **`internal/client/wakegate.go`**：激活降级三分支；新增包内未导出三态取数；`CurrentWorkflowAttempt` 语义保持不变但改为委托同一取数口；`JudgeMirroredWake` 显式判 `st == nil`。
2. **两消费点接线**：`cmd/card_wait.go` 与 `internal/agentd/wakeconsumer.go` 退役各自的旧快照扫描（`cardWaitCurrentWorkflowAttempt` / `Server.currentWorkflowAttempt`），改为只调共享符号，并按 `decision.Reason` 各补一条日志。
3. **两消费点穿缝矩阵测试 + B349 既有测试保绿**：同一断言矩阵在 `cmd/card_wait_test.go` 与 `internal/agentd/wakeconsumer_test.go` 各跑一遍；`TestB2336StaleAttemptDoesNotWake` 不许转红。
4. **全接缝回归 + F7 图产出**：本分支重扫并落 `codegraph/diffs/cards-B370-charter-7.json`（含 `nodesDeleted`/`nodesAdded`），使 `codegraph --view cards-B370-charter-7 check` 成为对照判据。

```text
T0 Ticket 0 前置复核（只读，不重开）
 └──> T1 internal/client：降级三分支 + 包内三态取数 + CurrentWorkflowAttempt 委托 + 直测
        └──> T2 两消费点：退役旧扫描 + reason 日志 + WaitDeliveryPolicy 顺序不变
               └──> T3 两消费点穿缝矩阵 + B349 重定基线核对（保绿）
                      └──> T4 全接缝回归 + 图/文档门禁 + 真机清单交接
```

**范围红线**（越界即停）：不得新增事件类型/HTTP 端点/ledger 派生 API；不得改 `mirrorSkip`、镜像写入、`WaitDeliveryPolicy` 集合、`codegraph/target.json`/`best.json`/`domains/*`；不得把判定下沉到 ledger；不得往 envelope 加身份字段。

---

## 2. 基线判据与已核对的库行为

### 2.1 动手前已亲跑的基线命令（原始读数）

以下命令在本计划落稿前于当前 HEAD（`cards/B370-charter-7`）运行并写入台账：

```text
go build ./...
→ 退出码 0

go vet ./internal/client/ ./internal/agentd/ ./cmd/
→ 退出码 0

go test ./cmd/ -run 'TestB349CardWaitSourceIdentity|TestB349CardWaitSubtreeUsesEventCardIdentity|TestB353CardWait' -count=1
→ ok  github.com/Xsxdot/handoff/cmd  20.435s

go test ./internal/agentd/ -run 'TestB349AutomationSourceIdentity|TestB2336StaleAttemptDoesNotWake|TestB2336WakeconsumerEnvelopeJSONBoundaries|TestB353AutomationUsesWaitDeliveryPolicy' -count=1
→ ok  github.com/Xsxdot/handoff/internal/agentd  2.445s

go test ./internal/client/ -count=1
→ ok  github.com/Xsxdot/handoff/internal/client  9.386s

go test ./internal/ledger/ -count=1
→ ok  github.com/Xsxdot/handoff/internal/ledger  19.174s

go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . --view cards-B370-charter-4 check
→ 退出码 0，fails 为空

go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . check
→ 退出码 1，fails 1 条：dead-contract d_transport→d_ledger（baseline 未重扫的已知红，见 contract §8）

go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . validate
→ 退出码 0

go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . sym cardWaitCurrentWorkflowAttempt
→ 命中 n_cmd_cardWaitCurrentWorkflowAttempt（d_cli，cmd/card_wait.go:195）

go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . sym currentWorkflowAttempt
→ 命中 n_agentd_Server_currentWorkflowAttempt（d_gateway，internal/agentd/wakeconsumer.go:56）

go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . sym JudgeMirroredWake
→ Error: 符号 "JudgeMirroredWake" 不在图中（图未覆盖或名字有误）；近似候选: []
```

这些读数只证明 T0 与既有夹具可运行；**基线仍对空身份镜像返回 `Deliver=false`**（骨架保守结果），不得把降级三分支写成已生效。

### 2.2 实现必须依赖的库/现状事实（带出处）

| 事实 | 出处 | 对实现的影响 |
| --- | --- | --- |
| `Store.EventsFromAsc(cardIDs []string, fromSeq int64, limit int)` 以 `fromSeq` 排他、`seq ASC` 升序、`LIMIT limit` 截尾；`limit<=0` 取 1000 | `internal/ledger/events.go#Store.EventsFromAsc`（`:63`），doc 注释 `:60-62` | 三态取数必须分页到不足一页为止，不能把首页 500 截断当成「无当前快照」 |
| `DispatchSnapshot` 有 `Target string`、`TaskID string`、`Node string`（`omitempty`）、`Attempt string`（`omitempty`） | `internal/ledger/events.go#DispatchSnapshot`（`:115-139`） | 合格条件 `Node != "" && Attempt != "" && TaskID == Attempt`；空 Node/Attempt 的派发仍是 `EvDispatched`（用于分支 2 判定） |
| `AppendMirroredEvent` 用 `fmt.Sprintf("{\"node\":%q,\"attempt\":%q,...}")` 写 payload，**键恒存在**（值可空串） | `internal/ledger/mirror.go#Store.AppendMirroredEvent`（`:62`） | 「缺键」形态只能由旧写入者/裸 `INSERT` 产生；测试要用 raw `INSERT` 造缺键，不能靠 `AppendMirroredEvent` |
| SQLite `card_events` 有可空列 `source_target`/`source_task`/`source_seq` | `internal/ledger/store.go:328-331` | raw 夹具可只填部分 source 列 |
| Go `encoding/json`：结构体缺失字段对 `*string` 保持 `nil`；显式 `""` → 指向空串的指针；显式 `null` → `nil` | 标准库既成行为，本仓既有测试 `TestB2336WakeconsumerEnvelopeJSONBoundaries` 已锁 | envelope `node`/`attempt` 用 `*string` 区分「缺键」与「空值」；三态取数只看派发快照，不看 envelope |
| `WaitDeliveryPolicy(t proto.EventType) bool` 是可交付任务类型的唯一事实源 | `internal/client/delivery.go#WaitDeliveryPolicy`（`:23`） | 身份闸通过后才调用；不复制集合 |
| `runCardWait` 用 `st.Follow(ctx, members, start, 2*time.Second, ...)` 轮询 | `cmd/card_wait.go:124` | 夹具写手先 `time.Sleep(250ms)`，2s 轮询会把整批写入一起交付；这是既有 B349 夹具的时序前提，新夹具照抄 |
| agentd 消费点用 `s.ledger`（`SetLedger` 注入）——`SetupAutomation` 装配 `autoLedger`，`cmd/agentd.go#setupLedger` 先 `SetLedger` 再 `SetupAutomation` | `internal/agentd/wakeconsumer.go:120`、`internal/agentd/server.go#SetupAutomation`（`:980`）、`cmd/agentd.go:485-489` | 共享判定读 `s.ledger`（不是 `autoLedger`），签名不变 |

### 2.3 图覆盖债（不改写符号名）

`codegraph sym` 实测：`JudgeMirroredWake`、`CurrentWorkflowAttempt`、`WakeGateReason`、`WakeGateDecision`、`WakeGateEvent` 不在基线图（内部符号不在图中）；`cardWaitCurrentWorkflowAttempt`、`Server.currentWorkflowAttempt` 在基线图（`d_cli`/`d_gateway`），实现删除后由 T4 的分支重扫以 `nodesDeleted` 反映。本计划一律用 `file#Symbol` 源码锚。

---

## 3. 文件范围与跨 task Interfaces

### 3.1 有界文件集

生产文件（只许改这些）：

- `internal/client/wakegate.go`（改判定正文 + 新增包内取数）
- `cmd/card_wait.go`（退役旧扫描 + reason 日志）
- `internal/agentd/wakeconsumer.go`（退役旧扫描 + reason 日志）

测试文件：

- `internal/client/wakegate_test.go`（新建）
- `cmd/card_wait_test.go`（追加）
- `internal/agentd/wakeconsumer_test.go`（追加）

图产出（T4/F7=A）：

- `codegraph/diffs/cards-B370-charter-7.json`（新建）

文档/台账：`docs/superpowers/plans/b370-plan.md`（本文件）、`docs/superpowers/ledgers/2026-09-15-b370-implement-ledger.md`（实现轮新建）。

只读引用：`internal/ledger/events.go`、`internal/ledger/mirror.go`、`internal/ledger/store.go`、`internal/client/delivery.go`、`internal/proto/ledger.go`、`internal/agentd/server.go`、`cmd/agentd.go`、`codegraph/target.json`、`codegraph/best.json`。**不得**改 `codegraph/target.json`/`best.json`/`domains/*`/`baseline.json`。

### 3.2 Consumes / Produces 精确签名（跨 task 对齐）

| 入口 | Consumes | Produces / 约束 |
| --- | --- | --- |
| `internal/client/wakegate.go#WakeGateReason` | string 类型 | 6 个常量值不变（含 `WakeGateEmptyEnvelopeIdentity`） |
| `internal/client/wakegate.go#WakeGateDecision` | — | `Deliver bool`；`Reason WakeGateReason` |
| `internal/client/wakegate.go#WakeGateEvent` | — | `CardID string`、`Seq int64`、`Node *string`、`Attempt *string`、`TaskType string`、`SourceTask string`、`SourceTarget string`、`SourceSeq int64` |
| `internal/client/wakegate.go#CurrentWorkflowAttempt` | `st *ledger.Store`、`cardID, node string` | `func(st *ledger.Store, cardID, node string) (snapshot ledger.DispatchSnapshot, found bool, err error)`；`node == ""` 恒 `found=false`；签名不变 |
| `internal/client/wakegate.go` 内新未导出 `scanCardDispatchSnapshots` | `st *ledger.Store`、`cardID, node string` | `func(st *ledger.Store, cardID, node string) (snapshot ledger.DispatchSnapshot, qualified bool, anyDispatch bool, err error)`；`node == ""` 表示不过滤节点 |
| `internal/client/wakegate.go` 内新未导出 `judgeEmptyEnvelopeIdentity` | `st *ledger.Store`、`ev WakeGateEvent` | `func(st *ledger.Store, ev WakeGateEvent) (WakeGateDecision, error)` |
| `internal/client/wakegate.go#JudgeMirroredWake` | `st *ledger.Store`、`ev WakeGateEvent` | `func(st *ledger.Store, ev WakeGateEvent) (WakeGateDecision, error)`；签名不变；`st == nil` 返回带上下文错误 |
| `cmd/card_wait.go#cardWaitEventActionable` | `st *ledger.Store`、`ev ledger.Event` | 签名不变；`EvTaskMirrored` 分支解包后填 `client.WakeGateEvent` 调 `client.JudgeMirroredWake`，再按 `deliver`/`reason` 落日志，`Deliver=true` 才走 `client.WaitDeliveryPolicy` |
| `cmd/card_wait.go#runCardWait` | — | 签名不变；事件所属 `e.CardID` 仍是查快照的卡号（`--subtree` 也用事件卡） |
| `internal/agentd/wakeconsumer.go#Server.acceptsCurrentWorkflowAttempt` | `ev proto.LedgerEvent` | 签名不变；解包后调 `client.JudgeMirroredWake(s.ledger, ...)`，按 `deliver`/`reason` 落日志，把 `Deliver` 翻成布尔出口 |
| `internal/agentd/wakeconsumer.go#Server.consumeAutomationEventsOnce` | `ctx context.Context` | 签名不变；被拦仍记 `seen` + 推进游标 |
| `internal/ledger/events.go#Store.EventsFromAsc` | `cardIDs []string, fromSeq int64, limit int` | 只读；签名不变 |
| `internal/ledger/events.go#DispatchSnapshot` | `Target/TaskID/Node/Attempt` | 只读；签名不变 |
| `internal/client/delivery.go#WaitDeliveryPolicy` | `t proto.EventType` | 只读；身份闸通过后调用 |

---

## 4. T0：Ticket 0 前置复核（只读，不重开）

**目标文件**：无（只跑命令）。
**测试范围声明**：本 task 只跑 §2.1 已列命令，不新增测试，不改任何文件。

步骤：

1. （2 分钟）运行 `go build ./...` 与 `go vet ./internal/client/ ./internal/agentd/ ./cmd/`，预期退出码 0（基线读数见 §2.1）。
2. （3 分钟）运行 §2.1 的四条测试命令，预期全部 `ok` 且命中具体测试名（不能以 `no tests to run` 当绿）。
3. （2 分钟）确认 Ticket 0 事实：`internal/client/wakegate.go` 已导出 §3.2 全部符号；两处消费点已调 `client.JudgeMirroredWake`；`grep -rn "cardWaitCurrentWorkflowAttempt\|currentWorkflowAttempt" --include=*.go .` 只命中定义（各一处），无其它调用方。
4. （2 分钟）把上述命令与原始输出追加进实现台账。

**生命周期/状态机中断：无，因为** T0 只读代码与规格、只跑本机命令，不创建 goroutine/进程/临时目录。
**静默失败/误导报错：无新增，因为** T0 不改错误路径。
**跨平台假设：无，因为** 只跑本机 Go 构建/单测。
**假红/假绿测试：有防线。** 判据命中具体测试名；Ticket 0 对空身份返回保守 `Deliver=false` 是过渡态，不得当成本卡行为已生效。
**门禁绕过：无。序列化边界：无新增字段。枚举新值：无新增枚举。**

---

## 5. T1：`internal/client` 共享判定激活 + 包内三态取数

### 5.1 目标文件与边界

文件：`internal/client/wakegate.go`（改）、`internal/client/wakegate_test.go`（新建）。
不改：`internal/ledger` schema/`mirror.go`/`mirrorSkip`、`WaitDeliveryPolicy` 集合、两消费点。
**测试范围声明**：本 task 只跑 `go test ./internal/client/ -run 'TestB370' -count=1`，只触及 `internal/client` 包。

### 5.2 红绿实现步骤

1. **基线判据（2 分钟）**：运行 `go test ./internal/client/ -run 'TestB370' -count=1`，预期 `ok` 但实际是 `no tests to run`（新测试尚未写）。这是「红」的起点，本 task 的测试必须先写。

2. **先写失败的直测（5 分钟）**：新建 `internal/client/wakegate_test.go`，完整内容如下（本包测试首次引入 `internal/ledger`，`internal/client` 依赖 `internal/ledger` 已在契约 §4 冻结）：

```go
package client

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// b370Store 起一个真实 SQLite 账本并返回一张已建好的卡。
// 边界：只服务 B370 直测；不写镜像事件（JudgeMirroredWake 直接吃投影结构）。
func b370Store(t *testing.T) (*ledger.Store, string) {
	t.Helper()
	st, err := ledger.Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("打开账本: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.PutWorkflow("bug", ledger.WorkflowDef{Nodes: []ledger.NodeDef{
		{Name: ledger.StatusTodo, Next: ledger.StatusDone},
		{Name: ledger.StatusDone},
	}}); err != nil {
		t.Fatalf("写测试工作流: %v", err)
	}
	card, err := st.CreateCard(ledger.NewCard{Title: "B370 身份闸", Project: "p", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	return st, card.ID
}

// b370Dispatch 写一条 EvDispatched 快照；TaskID 由 task 决定，用于构造 TaskID != Attempt 的不合格快照。
func b370Dispatch(t *testing.T, st *ledger.Store, cardID, target, task, node, attempt string) {
	t.Helper()
	if err := st.RecordDispatch(cardID, ledger.DispatchSnapshot{
		Target: target, TaskID: task, Node: node, Attempt: attempt,
		Branch: "cards/" + cardID + "-" + attempt, Purpose: ledger.PurposeReview, Actor: "test",
	}); err != nil {
		t.Fatalf("写派发快照 target=%s task=%s node=%s attempt=%s: %v", target, task, node, attempt, err)
	}
}

func b370Ptr(s string) *string { return &s }

func TestB370CurrentWorkflowAttempt(t *testing.T) {
	t.Run("latest qualified snapshot wins and TaskID != Attempt is skipped", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "a1", "review", "a1")
		b370Dispatch(t, st, cardID, "t", "task-x", "review", "a2") // TaskID != Attempt，不合格
		b370Dispatch(t, st, cardID, "t", "a3", "review", "a3")
		snap, found, err := CurrentWorkflowAttempt(st, cardID, "review")
		if err != nil || !found || snap.Attempt != "a3" {
			t.Fatalf("CurrentWorkflowAttempt=%+v found=%v err=%v，want attempt=a3", snap, found, err)
		}
	})
	t.Run("node mismatch is not identity", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "a1", "review", "a1")
		_, found, err := CurrentWorkflowAttempt(st, cardID, "implement")
		if err != nil || found {
			t.Fatalf("异节点查询 found=%v err=%v，want false/nil", found, err)
		}
	})
	t.Run("empty node query is never identity", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "a1", "review", "a1")
		_, found, err := CurrentWorkflowAttempt(st, cardID, "")
		if err != nil || found {
			t.Fatalf("空节点查询 found=%v err=%v，want false/nil", found, err)
		}
	})
	t.Run("no dispatch means not found", func(t *testing.T) {
		st, cardID := b370Store(t)
		_, found, err := CurrentWorkflowAttempt(st, cardID, "review")
		if err != nil || found {
			t.Fatalf("无派发 found=%v err=%v，want false/nil", found, err)
		}
	})
	t.Run("pagination reaches tail beyond first page", func(t *testing.T) {
		st, cardID := b370Store(t)
		for i := 0; i < 501; i++ {
			if _, err := st.AddComment(cardID, fmt.Sprintf("audit-%d", i), "普通", "test"); err != nil {
				t.Fatalf("写审计事件 %d: %v", i, err)
			}
		}
		b370Dispatch(t, st, cardID, "t", "a-tail", "review", "a-tail")
		snap, found, err := CurrentWorkflowAttempt(st, cardID, "review")
		if err != nil || !found || snap.Attempt != "a-tail" {
			t.Fatalf("分页尾部快照=%+v found=%v err=%v，want attempt=a-tail", snap, found, err)
		}
	})
}

func TestB370JudgeMirroredWakeEmptyIdentity(t *testing.T) {
	t.Run("missing keys and source equals current dispatch delivers", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "a1", "review", "a1")
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 1, TaskType: "question", SourceTask: "a1", SourceTarget: "t",
		})
		if err != nil || !dec.Deliver || dec.Reason != WakeGateCurrentAttempt {
			t.Fatalf("缺键空身份 dec=%+v err=%v，want deliver/current_attempt", dec, err)
		}
	})
	t.Run("empty string keys and source equals current dispatch delivers", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "a1", "review", "a1")
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 2, Node: b370Ptr(""), Attempt: b370Ptr(""),
			TaskType: "question", SourceTask: "a1", SourceTarget: "t",
		})
		if err != nil || !dec.Deliver || dec.Reason != WakeGateCurrentAttempt {
			t.Fatalf("空串空身份 dec=%+v err=%v，want deliver/current_attempt", dec, err)
		}
	})
	t.Run("snapshot without workflow identity delivers", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "task-no-identity", "", "")
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 3, TaskType: "question", SourceTask: "task-no-identity", SourceTarget: "t",
		})
		if err != nil || !dec.Deliver || dec.Reason != WakeGateSnapshotNoWorkflowIdentity {
			t.Fatalf("无身份快照 dec=%+v err=%v，want deliver/snapshot_without_workflow_identity", dec, err)
		}
	})
	t.Run("no dispatch snapshot delivers", func(t *testing.T) {
		st, cardID := b370Store(t)
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 4, TaskType: "question", SourceTask: "a1", SourceTarget: "t",
		})
		if err != nil || !dec.Deliver || dec.Reason != WakeGateSnapshotNotFound {
			t.Fatalf("无派发 dec=%+v err=%v，want deliver/snapshot_not_found", dec, err)
		}
	})
	t.Run("source mismatch blocks", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "a-new", "review", "a-new")
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 5, TaskType: "question", SourceTask: "a-old", SourceTarget: "t",
		})
		if err != nil || dec.Deliver || dec.Reason != WakeGateStaleAttempt {
			t.Fatalf("source 不等 dec=%+v err=%v，want block/stale_attempt", dec, err)
		}
	})
	t.Run("nonempty node locates by that node", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "a-impl", "implement", "a-impl")
		b370Dispatch(t, st, cardID, "t", "a-review", "review", "a-review")
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 6, Node: b370Ptr("review"), Attempt: b370Ptr(""),
			TaskType: "question", SourceTask: "a-review", SourceTarget: "t",
		})
		if err != nil || !dec.Deliver || dec.Reason != WakeGateCurrentAttempt {
			t.Fatalf("按 node 定位 dec=%+v err=%v，want deliver/current_attempt", dec, err)
		}
	})
}

func TestB370JudgeMirroredWakeNonEmptyIdentity(t *testing.T) {
	t.Run("current attempt with matching source passes and ignores source_seq", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "a1", "review", "a1")
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 7, Node: b370Ptr("review"), Attempt: b370Ptr("a1"),
			TaskType: "question", SourceTask: "a1", SourceTarget: "t", SourceSeq: 999999,
		})
		if err != nil || !dec.Deliver || dec.Reason != WakeGateCurrentAttempt {
			t.Fatalf("当前身份 dec=%+v err=%v，want deliver/current_attempt", dec, err)
		}
	})
	t.Run("both empty target passes", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "", "a1", "review", "a1")
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 8, Node: b370Ptr("review"), Attempt: b370Ptr("a1"),
			TaskType: "question", SourceTask: "a1", SourceTarget: "",
		})
		if err != nil || !dec.Deliver || dec.Reason != WakeGateCurrentAttempt {
			t.Fatalf("双空 target dec=%+v err=%v，want deliver/current_attempt", dec, err)
		}
	})
	t.Run("one empty target blocks", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "a1", "review", "a1")
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 9, Node: b370Ptr("review"), Attempt: b370Ptr("a1"),
			TaskType: "question", SourceTask: "a1", SourceTarget: "",
		})
		if err != nil || dec.Deliver || dec.Reason != WakeGateStaleAttempt {
			t.Fatalf("一空一非空 dec=%+v err=%v，want block/stale_attempt", dec, err)
		}
	})
	t.Run("stale attempt blocks", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "a-new", "review", "a-new")
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 10, Node: b370Ptr("review"), Attempt: b370Ptr("a-old"),
			TaskType: "question", SourceTask: "a-new", SourceTarget: "t",
		})
		if err != nil || dec.Deliver || dec.Reason != WakeGateStaleAttempt {
			t.Fatalf("旧 attempt dec=%+v err=%v，want block/stale_attempt", dec, err)
		}
	})
	t.Run("missing source task has its own reason", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "a1", "review", "a1")
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 11, Node: b370Ptr("review"), Attempt: b370Ptr("a1"),
			TaskType: "question", SourceTask: "", SourceTarget: "t",
		})
		if err != nil || dec.Deliver || dec.Reason != WakeGateMissingSourceTask {
			t.Fatalf("缺 source_task dec=%+v err=%v，want block/missing_source_task", dec, err)
		}
	})
	t.Run("no qualified snapshot blocks", func(t *testing.T) {
		st, cardID := b370Store(t)
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 12, Node: b370Ptr("review"), Attempt: b370Ptr("a1"),
			TaskType: "question", SourceTask: "a1", SourceTarget: "t",
		})
		if err != nil || dec.Deliver || dec.Reason != WakeGateStaleAttempt {
			t.Fatalf("无合格快照 dec=%+v err=%v，want block/stale_attempt", dec, err)
		}
	})
}

func TestB370JudgeMirroredWakeNilStore(t *testing.T) {
	if _, err := JudgeMirroredWake(nil, WakeGateEvent{CardID: "X1", Seq: 1, TaskType: "question"}); err == nil {
		t.Fatal("st == nil 必须返回带上下文的错误，不得 panic")
	}
}
```

3. **运行红测试（2 分钟）**：运行
   `go test ./internal/client/ -run 'TestB370' -count=1`。
   预期当前骨架下**失败**，失败点应是：
   - `TestB370JudgeMirroredWakeEmptyIdentity` 的 branch 1/2/3 用例（当前返回 `WakeGateEmptyEnvelopeIdentity` 且 `Deliver=false`）；
   - `TestB370JudgeMirroredWakeNilStore`（当前 `st == nil` 会在 `st.EventsFromAsc` 处 panic，Go test 报 panic 而非 error，也算红）。
   `TestB370CurrentWorkflowAttempt` 与 `TestB370JudgeMirroredWakeNonEmptyIdentity` 除 `missing_source_task` 用例（当前落 `WakeGateStaleAttempt`）外应已绿。**不许把「无测试可跑」当红或绿**。

4. **最小实现（10 分钟）**：把 `internal/client/wakegate.go` 整体改为下述内容（保留契约 §3.1 的导出面与值，删除骨架注释中「留待 implement」的段落）。完整文件：

```go
// 本文件承载 task_mirrored 的镜像身份闸判定正文（B370）。
//
// 职责：把「一条 task_mirrored 该不该叫醒消费者」从两处消费点（cmd/card_wait.go
// 与 internal/agentd/wakeconsumer.go）收成一处共享实现；判定输入 = 事件投影 +
// 该事件所属卡的派发快照读取口，输出 = 交付/不交付 + reason。
// 边界：不碰账本 schema、不改 envelope、不删账本行、不吞错误；调用点仍负责自身
// 的交付动作（card wait Encode / wakeconsumer 记 seen 与推进游标）与 envelope 解码。
//
// 降级方向（B370「能判定才拦」）：身份缺失或为空时不再一律拒绝，改用 source 列
// 与当前派发快照比对，只在能证明是旧 attempt 时才拦。判定正文只此一处，
// 两份近重复的快照扫描（cmd 侧 cardWaitCurrentWorkflowAttempt / agentd 侧
// Server.currentWorkflowAttempt）已退役，取数归本文件的 scanCardDispatchSnapshots。
//
// 归属：本包（d_transport）已是 WaitDeliveryPolicy 的宿主，两处消费点都依赖它；
// 身份闸与类型表是同一条「是否叫醒」规则的两半，住一处。代价是新增契约方向
// d_transport→d_ledger（读 ledger.Store 与 ledger.DispatchSnapshot），已在
// codegraph/target.json 显式声明。
//
// 详见 docs/superpowers/specs/b370-contract.md 与 docs/superpowers/plans/b370-plan.md。
package client

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// WakeGateReason 是一次镜像身份闸判定的结论枚举。
// 放行与拦截都必须携带 reason，供两个消费点按分支落日志。
type WakeGateReason string

const (
	// WakeGateEmptyEnvelopeIdentity 是身份缺失/为空这一输入形态的分类标签
	// （用于降级判定入口日志的 identity_state），不再是判定结论。
	WakeGateEmptyEnvelopeIdentity WakeGateReason = "empty_workflow_identity"
	// WakeGateSnapshotNoWorkflowIdentity 表示有派发但快照无工作流身份 = 无从判迟到 = 放行。
	WakeGateSnapshotNoWorkflowIdentity WakeGateReason = "snapshot_without_workflow_identity"
	// WakeGateSnapshotNotFound 表示找不到该 task 的派发快照 = 无从判迟到 = 放行。
	WakeGateSnapshotNotFound WakeGateReason = "snapshot_not_found"
	// WakeGateCurrentAttempt 表示身份（envelope 或 source 列）等于当前派发（放行）。
	WakeGateCurrentAttempt WakeGateReason = "current_attempt"
	// WakeGateStaleAttempt 表示能证明是旧 attempt（拦截，防迟到）。
	WakeGateStaleAttempt WakeGateReason = "stale_attempt"
	// WakeGateMissingSourceTask 表示身份非空但缺 source_task（闭集拒绝）。
	WakeGateMissingSourceTask WakeGateReason = "missing_source_task"
)

// WakeGateDecision 是一次判定的结果：是否放行 + 判定依据。
type WakeGateDecision struct {
	// Deliver 为 true 表示该 task_mirrored 可以进入类型表判断（叫醒面）。
	Deliver bool
	Reason  WakeGateReason
}

// WakeGateEvent 是两处消费点喂给身份闸的事件最小投影。
// 消费点的既有 wire 类型不同（cmd 用 ledger.Event、agentd 用 proto.LedgerEvent），
// 各自解码 envelope 后填本结构；Node/Attempt 用指针保留「键缺失」与「空串」之别。
type WakeGateEvent struct {
	CardID       string
	Seq          int64
	Node         *string
	Attempt      *string
	TaskType     string
	SourceTask   string
	SourceTarget string
	SourceSeq    int64
}

// CurrentWorkflowAttempt 从事件所属卡的全量事件流取指定节点当前的有效派发快照。
// 只有 Node、Attempt 非空且 TaskID == Attempt 的快照才是身份事实；事件按 seq 升序
// 分页读到尾，最后一个合格快照是当前身份。旧快照与不自洽快照保留为审计数据。
//
// node 为空不是有效的身份查询键，恒返回 found=false（B349 契约保留）。
func CurrentWorkflowAttempt(st *ledger.Store, cardID, node string) (snapshot ledger.DispatchSnapshot, found bool, err error) {
	if node == "" {
		return ledger.DispatchSnapshot{}, false, nil
	}
	snapshot, found, _, err = scanCardDispatchSnapshots(st, cardID, node)
	return snapshot, found, err
}

// scanCardDispatchSnapshots 是身份闸唯一的快照取数口（B370 F2）。
//
// 参数：cardID 是事件所属卡；node 非空时只接受 Node == node 的快照，
// node 为空表示不过滤节点（F1 空身份定位用同一张卡上的最新合格快照）。
//
// 返回：
//   - snapshot/qualified：最新（最大事件 seq）合格快照与「存在合格快照」；
//   - anyDispatch：该卡是否存在任何 EvDispatched（区分降级分支 2「有派发无身份」
//     与分支 3「无派发」）；
//   - err：读账或 payload 解码失败时带 card/seq/type 上下文返回，不吞。
func scanCardDispatchSnapshots(st *ledger.Store, cardID, node string) (
	snapshot ledger.DispatchSnapshot, qualified bool, anyDispatch bool, err error,
) {
	const pageSize = 500
	from := int64(0)
	for {
		events, readErr := st.EventsFromAsc([]string{cardID}, from, pageSize)
		if readErr != nil {
			slog.Error("读取当前 workflow attempt 失败", "card", cardID,
				"node", node, "from_seq", from, "cause", readErr)
			return ledger.DispatchSnapshot{}, false, false,
				fmt.Errorf("卡 %s 当前派发快照读取: %w", cardID, readErr)
		}
		for _, event := range events {
			if event.Seq > from {
				from = event.Seq
			}
			if event.Type != ledger.EvDispatched {
				continue
			}
			anyDispatch = true
			var candidate ledger.DispatchSnapshot
			if decodeErr := json.Unmarshal(event.Payload, &candidate); decodeErr != nil {
				slog.Error("派发快照解码失败", "card", cardID,
					"seq", event.Seq, "type", event.Type, "node", node, "cause", decodeErr)
				return ledger.DispatchSnapshot{}, false, false,
					fmt.Errorf("卡 %s 派发快照 seq=%d type=%s 解码: %w", cardID, event.Seq, event.Type, decodeErr)
			}
			if node != "" && candidate.Node != node {
				continue
			}
			if candidate.Node == "" || candidate.Attempt == "" || candidate.TaskID != candidate.Attempt {
				continue
			}
			snapshot = candidate
			qualified = true
		}
		if len(events) < pageSize {
			return snapshot, qualified, anyDispatch, nil
		}
	}
}

// JudgeMirroredWake 是 task_mirrored 的身份闸判定正文（B370）。
//
// B370 冻结的判定方向是「能判定才拦」：身份非空走 A 路径（B349 正文保留），
// 身份缺失或为空走降级三分支（见 judgeEmptyEnvelopeIdentity）。本函数只做判定，
// 副作用（记 seen、推进游标、日志等级）归消费点。
//
// 参数：st 为账本；st == nil 时显式返回带上下文错误（F4），不 panic。
func JudgeMirroredWake(st *ledger.Store, ev WakeGateEvent) (WakeGateDecision, error) {
	if st == nil {
		slog.Error("镜像身份闸无法判定：账本未装配", "card", ev.CardID,
			"seq", ev.Seq, "type", ledger.EvTaskMirrored, "task_type", ev.TaskType)
		return WakeGateDecision{}, fmt.Errorf("卡 %s task_mirrored seq=%d: 身份闸账本未装配", ev.CardID, ev.Seq)
	}
	if ev.Node == nil || *ev.Node == "" || ev.Attempt == nil || *ev.Attempt == "" {
		return judgeEmptyEnvelopeIdentity(st, ev)
	}
	// A. 身份非空：按 B349 正文保留。
	if ev.SourceTask == "" {
		slog.Info("task_mirrored 因缺 source_task 跳过", "card", ev.CardID,
			"seq", ev.Seq, "type", ledger.EvTaskMirrored, "node", *ev.Node,
			"attempt", *ev.Attempt, "task_type", ev.TaskType,
			"reason", string(WakeGateMissingSourceTask))
		return WakeGateDecision{Deliver: false, Reason: WakeGateMissingSourceTask}, nil
	}
	snapshot, found, err := CurrentWorkflowAttempt(st, ev.CardID, *ev.Node)
	if err != nil {
		return WakeGateDecision{}, err
	}
	if !found || snapshot.TaskID != snapshot.Attempt || snapshot.Attempt != *ev.Attempt ||
		ev.SourceTask != snapshot.Attempt || ev.SourceTarget != snapshot.Target {
		slog.Info("task_mirrored 因 source identity 不匹配跳过", "card", ev.CardID,
			"seq", ev.Seq, "type", ledger.EvTaskMirrored, "node", *ev.Node,
			"attempt", *ev.Attempt, "current_attempt", snapshot.Attempt,
			"source_task", ev.SourceTask, "source_target", ev.SourceTarget,
			"current_target", snapshot.Target, "source_seq", ev.SourceSeq,
			"task_type", ev.TaskType, "reason", string(WakeGateStaleAttempt))
		return WakeGateDecision{Deliver: false, Reason: WakeGateStaleAttempt}, nil
	}
	slog.Debug("task_mirrored 通过 source identity 闸", "card", ev.CardID,
		"seq", ev.Seq, "type", ledger.EvTaskMirrored, "node", *ev.Node,
		"attempt", *ev.Attempt, "source_task", ev.SourceTask,
		"source_target", ev.SourceTarget, "current_target", snapshot.Target,
		"source_seq", ev.SourceSeq, "task_type", ev.TaskType,
		"reason", string(WakeGateCurrentAttempt))
	return WakeGateDecision{Deliver: true, Reason: WakeGateCurrentAttempt}, nil
}

// judgeEmptyEnvelopeIdentity 是身份缺失或为空时的降级判定（B370 三分支）。
//
//  1. 能定位快照且快照带工作流身份：与当前快照比对 source 列，相等放行，不等拦截；
//  2. 能定位快照但快照无工作流身份：无从判迟到 = 放行；
//  3. 找不到快照：无从判迟到 = 放行。
//
// 定位规则（F1=A）：node 非空按 (cardID, node) 用最新合格快照定位；node 缺失或空
// 时以同一张卡上的最新合格快照为当前身份。source 三列是权威身份，不读 envelope。
func judgeEmptyEnvelopeIdentity(st *ledger.Store, ev WakeGateEvent) (WakeGateDecision, error) {
	var node string
	if ev.Node != nil {
		node = *ev.Node
	}
	slog.Info("task_mirrored 身份缺失或为空，进入降级判定", "card", ev.CardID,
		"seq", ev.Seq, "type", ledger.EvTaskMirrored, "task_type", ev.TaskType,
		"node_present", ev.Node != nil, "attempt_present", ev.Attempt != nil,
		"node", node, "identity_state", string(WakeGateEmptyEnvelopeIdentity))
	snapshot, qualified, anyDispatch, err := scanCardDispatchSnapshots(st, ev.CardID, node)
	if err != nil {
		return WakeGateDecision{}, err
	}
	if !anyDispatch {
		slog.Info("task_mirrored 身份为空且该卡无派发快照，降级放行", "card", ev.CardID,
			"seq", ev.Seq, "type", ledger.EvTaskMirrored, "task_type", ev.TaskType,
			"source_task", ev.SourceTask, "source_target", ev.SourceTarget,
			"reason", string(WakeGateSnapshotNotFound))
		return WakeGateDecision{Deliver: true, Reason: WakeGateSnapshotNotFound}, nil
	}
	if !qualified {
		slog.Info("task_mirrored 身份为空且派发快照无工作流身份，降级放行", "card", ev.CardID,
			"seq", ev.Seq, "type", ledger.EvTaskMirrored, "task_type", ev.TaskType,
			"source_task", ev.SourceTask, "source_target", ev.SourceTarget,
			"reason", string(WakeGateSnapshotNoWorkflowIdentity))
		return WakeGateDecision{Deliver: true, Reason: WakeGateSnapshotNoWorkflowIdentity}, nil
	}
	if ev.SourceTask == snapshot.Attempt && ev.SourceTarget == snapshot.Target {
		slog.Debug("task_mirrored 空身份按 source 列匹配当前派发放行", "card", ev.CardID,
			"seq", ev.Seq, "type", ledger.EvTaskMirrored, "task_type", ev.TaskType,
			"source_task", ev.SourceTask, "source_target", ev.SourceTarget,
			"current_attempt", snapshot.Attempt, "current_target", snapshot.Target,
			"reason", string(WakeGateCurrentAttempt))
		return WakeGateDecision{Deliver: true, Reason: WakeGateCurrentAttempt}, nil
	}
	slog.Info("task_mirrored 空身份且 source 列不等于当前派发，拦截", "card", ev.CardID,
		"seq", ev.Seq, "type", ledger.EvTaskMirrored, "task_type", ev.TaskType,
		"source_task", ev.SourceTask, "source_target", ev.SourceTarget,
		"current_attempt", snapshot.Attempt, "current_target", snapshot.Target,
		"reason", string(WakeGateStaleAttempt))
	return WakeGateDecision{Deliver: false, Reason: WakeGateStaleAttempt}, nil
}
```

5. **运行绿测试（2 分钟）**：`go test ./internal/client/ -run 'TestB370' -count=1`，预期 `ok` 且命中 5 支测试（`TestB370CurrentWorkflowAttempt`、`TestB370JudgeMirroredWakeEmptyIdentity`、`TestB370JudgeMirroredWakeNonEmptyIdentity`、`TestB370JudgeMirroredWakeNilStore`；子测试不计入 `-run` 名）。
6. **关键节点日志与注释（2 分钟）**：确认上文件中：入口（降级入口 / A 路径）带 `card/seq/type/task_type`；每条错误分支（读账、解码、nil store、缺 source_task）带上下文；放行与拦截都不静默（Info/Debug + reason）；无 `fmt.Print`/`print`。新函数写参数/返回/失败语义注释，文件头写职责+边界。
7. **跑更宽一步（3 分钟）**：`go test ./internal/client/ -count=1`，预期 `ok`；把命令与原始输出追加进台账。

### 5.3 T1 可判定验收

- `CurrentWorkflowAttempt` 只接受 `Node != "" && Attempt != "" && TaskID == Attempt`，同 node 取最大 seq，`TaskID != Attempt` 不作身份，>500 行分页到尾，无合格 `found=false`，`node == ""` 恒 `found=false`（契约 §5.2 #20–#24）。
- 降级三分支与 A 路径逐条由直测锁定（契约 §5.3 #25–#35、§5.4 #36–#42）：缺键/空串空身份 source 相等 → 交付；无身份快照 → 交付；无派发 → 交付；source 不等 → 拦截；A 路径当前/旧 attempt/source 不匹配/一空一非空/缺 source_task/无快照各有正反断言。
- 拦截 reason=`stale_attempt`；缺 source_task reason=`missing_source_task`；所有放行 reason 非空。
- `st == nil` 返回 error 不 panic。

---

## 6. T2：两消费点退役旧扫描 + reason 日志

### 6.1 目标文件与边界

文件：`cmd/card_wait.go`（改）、`internal/agentd/wakeconsumer.go`（改）。
调用方接缝：`runCardWait` → `cardWaitEventActionable`；`consumeAutomationEventsOnce` → `acceptsCurrentWorkflowAttempt`。
不改：命令/flag、HTTP 端点、ledger 派生 API、`WaitDeliveryPolicy` 集合、`mirrorSkip`。
**测试范围声明**：本 task 只跑 `go build ./...`、`go vet ./cmd/ ./internal/agentd/` 与两包编译期测试，不新增测试（穿缝测试在 T3）。

### 6.2 实现步骤

1. **退役 cmd 旧扫描（3 分钟）**：删除 `cmd/card_wait.go` 中整个 `cardWaitCurrentWorkflowAttempt` 函数（现状 `:192-235`，含其 doc 注释）。删除后 `grep -n "cardWaitCurrentWorkflowAttempt" cmd/` 无命中。
2. **改 `cardWaitEventActionable` 的 `EvTaskMirrored` 尾段（3 分钟）**：把现状 `:276-287` 的判定调用改为：

```go
		decision, err := client.JudgeMirroredWake(st, client.WakeGateEvent{
			CardID: ev.CardID, Seq: ev.Seq, Node: envelope.Node, Attempt: envelope.Attempt,
			TaskType: envelope.TaskType, SourceTask: ev.SourceTask,
			SourceTarget: ev.SourceTarget, SourceSeq: ev.SourceSeq,
		})
		if err != nil {
			return false, err
		}
		slog.Info("card wait task_mirrored 身份闸判定", "card", ev.CardID, "seq", ev.Seq,
			"type", ev.Type, "task_type", envelope.TaskType, "deliver", decision.Deliver,
			"reason", string(decision.Reason))
		if !decision.Deliver {
			return false, nil
		}
		return client.WaitDeliveryPolicy(proto.EventType(envelope.TaskType)), nil
```

   `cardWaitEventActionable` 的签名、卡原生四类与 room 分支保持原样；`--subtree` 参数透传不改事件卡（`runCardWait` 已把 `e.CardID` 传给分类器，不改）。同时把该函数 doc 注释里的「身份闸」说明改为指向 `client.JudgeMirroredWake`，并说明「身份闸通过后才问策略」。

3. **退役 agentd 旧扫描（3 分钟）**：删除 `internal/agentd/wakeconsumer.go` 中整个 `Server.currentWorkflowAttempt` 方法（现状 `:53-103`，含其 doc 注释）。删除后 `grep -n "currentWorkflowAttempt" internal/agentd/` 无命中。
4. **改 `acceptsCurrentWorkflowAttempt`（3 分钟）**：把现状 `:120-128` 的判定尾段改为：

```go
	decision, err := client.JudgeMirroredWake(s.ledger, client.WakeGateEvent{
		CardID: ev.CardID, Seq: ev.Seq, Node: envelope.Node, Attempt: envelope.Attempt,
		TaskType: envelope.TaskType, SourceTask: ev.SourceTask,
		SourceTarget: ev.SourceTarget, SourceSeq: ev.SourceSeq,
	})
	if err != nil {
		s.log.Error("task_mirrored 身份闸判定失败", "card", ev.CardID, "seq", ev.Seq,
			"type", ev.Type, "cause", err)
		return false, err
	}
	s.log.Info("task_mirrored 身份闸判定", "card", ev.CardID, "seq", ev.Seq,
		"type", ev.Type, "task_type", envelope.TaskType, "deliver", decision.Deliver,
		"reason", string(decision.Reason))
	return decision.Deliver, nil
```

   保留方法开头对 `s.ledger == nil` 的既有守卫（与 F4 的 `JudgeMirroredWake` 判空互为双保险，契约 §3.3 的既有错误路径不变）。

5. **消歧义日志（1 分钟）**：`consumeAutomationEventsOnce` 现状 `:357-359` 的
   `s.log.Debug("自动化事件因当前 attempt 闸被标记 seen", ..., "reason", "stale_attempt")`
   改为 `"reason", "wake_gate_rejected"`——精确 reason 已由第 4 步在 `acceptsCurrentWorkflowAttempt` 落，这里不再冒称 `stale_attempt`（否则缺 `source_task` 的拦截会被记错）。

6. **编译与静态检查（2 分钟）**：运行 `go build ./...`（退出码 0）、`go vet ./cmd/ ./internal/agentd/`（退出码 0）、`gofmt -l cmd/card_wait.go internal/agentd/wakeconsumer.go`（只允许既有 `wakeconsumer.go:230` 注释空行差异，不得新增其它差异；若新增，顺手格式化自己改过的区块）。
7. **跑既有消费面测试（3 分钟）**：`go test ./cmd/ -run 'TestB349CardWait|TestB353CardWait' -count=1` 与 `go test ./internal/agentd/ -run 'TestB349AutomationSourceIdentity|TestB2336StaleAttemptDoesNotWake|TestB353Automation|TestAutomationEventMappingThroughConsumer' -count=1`，预期均 `ok`（这两条是 T3 的前置回归网）。
8. **日志与注释（1 分钟）**：确认两消费点放行与拦截都留痕、字段含 `card/seq/type/task_type/deliver/reason`；无 `print`；把命令与原始输出追加进台账。

### 6.3 T2 可判定验收

- 两处旧扫描符号全仓无定义/引用（`grep` 为证）。
- `card wait` 被拦不 `Encode`；wakeconsumer 被拦仍记 `seen` 并推进游标（既有路径不变，T3 断言）。
- `WaitDeliveryPolicy` 仍在身份闸之后调用，集合未复制。
- 两消费点按 `decision.Reason` 各落一条日志，放行与拦截都可见。

---

## 7. T3：两消费点穿缝矩阵 + B349 重定基线核对

### 7.1 目标文件与边界

文件：`cmd/card_wait_test.go`（追加）、`internal/agentd/wakeconsumer_test.go`（追加）。
调用方接缝：`runCardWait` → `Store.Follow` → `json.Encoder`；`consumeAutomationEventsOnce` → `automationWakeEvent`/keystone runner。
**测试范围声明**：本 task 只跑 `go test ./cmd/ -run 'TestB370' -count=1` 与 `go test ./internal/agentd/ -run 'TestB370' -count=1`，只触及这两包；全量回归归 T4。
**禁只测 helper**：所有断言从 `runCardWait` 与 `consumeAutomationEventsOnce` 进入。

### 7.2 cmd 侧穿缝测试

1. **加 raw 夹具 helper（2 分钟）**：在 `cmd/card_wait_test.go` 追加（与既有 `insertRawCardWaitEvent` 同形，但填 source 三列）：

```go
func insertRawCardWaitMirrored(dir, cardID, target, task string, sourceSeq int64, payload string) (int64, error) {
	db, err := sql.Open("sqlite", filepath.Join(dir, "ledger.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)")
	if err != nil {
		return 0, err
	}
	defer db.Close()
	result, err := db.Exec(`INSERT INTO card_events
		(card_id, type, actor, payload, source_target, source_task, source_seq, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		cardID, ledger.EvTaskMirrored, "mirror", payload, target, task, sourceSeq, time.Now())
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func recordB370CardWaitDispatch(st *ledger.Store, cardID, target, node, attempt string) error {
	return st.RecordDispatch(cardID, ledger.DispatchSnapshot{
		Target: target, TaskID: attempt, Node: node, Attempt: attempt,
		Branch: "cards/" + cardID + "-" + attempt, Purpose: ledger.PurposeReview, Actor: "test",
	})
}

// runB370CardWaitScenario 起一张真实卡、在写手里执行 setup、再以 --follow 跑 runCardWait，
// 返回 stdout 解出的 ledger.Event 序列与卡号。入口是 runCardWait，不是分类 helper。
func runB370CardWaitScenario(t *testing.T, setup func(st *ledger.Store, dir, cardID string) error) ([]ledger.Event, string) {
	t.Helper()
	dir := t.TempDir()
	cardID := createCardWaitFixture(t, dir)
	writerErr := make(chan error, 1)
	go func() {
		time.Sleep(250 * time.Millisecond)
		st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
		if err != nil {
			writerErr <- err
			return
		}
		defer st.Close()
		if err := setup(st, dir, cardID); err != nil {
			writerErr <- err
			return
		}
		writerErr <- moveCardWaitFixtureToDone(st, cardID)
	}()
	out, _, err := runLedgerCLI(t, dir, "card", "wait", cardID, "--follow", "--timeout", "5s")
	if we := <-writerErr; we != nil {
		t.Fatalf("写 B370 card wait 夹具: %v", we)
	}
	if err != nil {
		t.Fatalf("card wait --follow: %v; output=%q", err, out)
	}
	var got []ledger.Event
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var ev ledger.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("stdout 不是 ledger.Event JSON: %v; line=%q", err, line)
		}
		got = append(got, ev)
	}
	return got, cardID
}
```

2. **加矩阵测试（8 分钟）**：追加 `TestB370CardWaitDegradedIdentity`，逐条覆盖 spec §测试决定矩阵：

```go
func TestB370CardWaitDegradedIdentity(t *testing.T) {
	cases := []struct {
		name  string
		setup func(st *ledger.Store, dir, cardID string) error
		want  bool // true = 应输出一行 task_mirrored
	}{
		{
			name: "missing node/attempt keys with source equal current dispatch delivers",
			setup: func(st *ledger.Store, dir, cardID string) error {
				if err := recordB370CardWaitDispatch(st, cardID, "target-current", "review", "attempt-current"); err != nil {
					return err
				}
				if _, err := insertRawCardWaitMirrored(dir, cardID, "target-current", "attempt-current", 1,
					`{"task_type":"question","payload":{"ticket_id":"legacy-missing-keys"}}`); err != nil {
					return err
				}
				return nil
			},
			want: true,
		},
		{
			name: "empty string keys with source equal current dispatch delivers",
			setup: func(st *ledger.Store, dir, cardID string) error {
				if err := recordB370CardWaitDispatch(st, cardID, "target-current", "review", "attempt-current"); err != nil {
					return err
				}
				if _, err := st.AppendMirroredEvent(cardID, ledger.MirroredEvent{
					Target: "target-current", Task: "attempt-current", Node: "", Attempt: "",
					SourceSeq: 2, Type: "question", Payload: []byte(`{"ticket_id":"empty-keys"}`), CreatedAt: time.Now(),
				}); err != nil {
					return err
				}
				return nil
			},
			want: true,
		},
		{
			name: "snapshot without workflow identity delivers",
			setup: func(st *ledger.Store, dir, cardID string) error {
				if err := st.RecordDispatch(cardID, ledger.DispatchSnapshot{
					Target: "target-current", TaskID: "task-no-identity", Node: "", Attempt: "",
					Branch: "cards/" + cardID + "-legacy", Purpose: ledger.PurposeReview, Actor: "test",
				}); err != nil {
					return err
				}
				if _, err := st.AppendMirroredEvent(cardID, ledger.MirroredEvent{
					Target: "target-current", Task: "task-no-identity", Node: "", Attempt: "",
					SourceSeq: 3, Type: "question", Payload: []byte(`{"ticket_id":"no-identity-snapshot"}`), CreatedAt: time.Now(),
				}); err != nil {
					return err
				}
				return nil
			},
			want: true,
		},
		{
			name: "no dispatch snapshot delivers",
			setup: func(st *ledger.Store, dir, cardID string) error {
				if _, err := st.AppendMirroredEvent(cardID, ledger.MirroredEvent{
					Target: "target-current", Task: "attempt-current", Node: "", Attempt: "",
					SourceSeq: 4, Type: "question", Payload: []byte(`{"ticket_id":"no-dispatch"}`), CreatedAt: time.Now(),
				}); err != nil {
					return err
				}
				return nil
			},
			want: true,
		},
		{
			name: "empty identity with source not matching current dispatch is blocked",
			setup: func(st *ledger.Store, dir, cardID string) error {
				if err := recordB370CardWaitDispatch(st, cardID, "target-current", "review", "attempt-new"); err != nil {
					return err
				}
				if _, err := st.AppendMirroredEvent(cardID, ledger.MirroredEvent{
					Target: "target-current", Task: "attempt-old", Node: "", Attempt: "",
					SourceSeq: 5, Type: "question", Payload: []byte(`{"ticket_id":"stale-empty"}`), CreatedAt: time.Now(),
				}); err != nil {
					return err
				}
				return nil
			},
			want: false,
		},
		{
			name: "nonempty identity equal current dispatch delivers",
			setup: func(st *ledger.Store, dir, cardID string) error {
				if err := recordB370CardWaitDispatch(st, cardID, "target-current", "review", "attempt-current"); err != nil {
					return err
				}
				if _, err := st.AppendMirroredEvent(cardID, ledger.MirroredEvent{
					Target: "target-current", Task: "attempt-current", Node: "review", Attempt: "attempt-current",
					SourceSeq: 6, Type: "question", Payload: []byte(`{"ticket_id":"current-identity"}`), CreatedAt: time.Now(),
				}); err != nil {
					return err
				}
				return nil
			},
			want: true,
		},
		{
			name: "nonempty old attempt is blocked",
			setup: func(st *ledger.Store, dir, cardID string) error {
				if err := recordB370CardWaitDispatch(st, cardID, "target-current", "review", "attempt-new"); err != nil {
					return err
				}
				if _, err := st.AppendMirroredEvent(cardID, ledger.MirroredEvent{
					Target: "target-current", Task: "attempt-new", Node: "review", Attempt: "attempt-old",
					SourceSeq: 7, Type: "question", Payload: []byte(`{"ticket_id":"stale-identity"}`), CreatedAt: time.Now(),
				}); err != nil {
					return err
				}
				return nil
			},
			want: false,
		},
		{
			name: "delivery policy false is still blocked after identity passes",
			setup: func(st *ledger.Store, dir, cardID string) error {
				if err := recordB370CardWaitDispatch(st, cardID, "target-current", "review", "attempt-current"); err != nil {
					return err
				}
				if _, err := st.AppendMirroredEvent(cardID, ledger.MirroredEvent{
					Target: "target-current", Task: "attempt-current", Node: "review", Attempt: "attempt-current",
					SourceSeq: 8, Type: string(proto.EventTypePermissionAutoAllow), Payload: []byte(`{"rule":"safe"}`), CreatedAt: time.Now(),
				}); err != nil {
					return err
				}
				return nil
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, cardID := runB370CardWaitScenario(t, tc.setup)
			if tc.want && (len(got) != 1 || got[0].Type != ledger.EvTaskMirrored || got[0].CardID != cardID) {
				t.Fatalf("want 一行 task_mirrored(card=%s)，got=%+v", cardID, got)
			}
			if !tc.want && len(got) != 0 {
				t.Fatalf("want 无输出，got=%+v", got)
			}
			if tc.want && got[0].SourceTask == "" {
				t.Fatalf("stdout 丢失 source_task 列: %+v", got[0])
			}
		})
	}
}
```

3. **`--subtree` 事件卡回归（1 分钟）**：确认 `TestB349CardWaitSubtreeUsesEventCardIdentity` 仍在（T3 step 6 会跑）。它锁的是「用事件所属子卡取快照，不用 wait 根卡」。
4. **payload 边界回归（1 分钟）**：确认 `TestB353CardWaitPreservesNullAndRejectsMissingMirroredPayload` 仍在（跑红/绿同批），它锁 null payload 原样输出与缺 `task_type/payload` 带上下文报错。

### 7.3 agentd 侧穿缝测试

1. **加 raw 夹具 helper（2 分钟）**：在 `internal/agentd/wakeconsumer_test.go` 追加：

```go
func appendRawMirroredWithSource(t *testing.T, env *ledgerEnv, cardID, target, task, payload string, sourceSeq int64) int64 {
	t.Helper()
	db, err := sql.Open("sqlite", env.ledgerPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("打开 raw mirrored ledger: %v", err)
	}
	defer db.Close()
	result, err := db.Exec(`INSERT INTO card_events
		(card_id, type, actor, payload, source_target, source_task, source_seq, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		cardID, ledger.EvTaskMirrored, "mirror", payload, target, task, sourceSeq, time.Now())
	if err != nil {
		t.Fatalf("写 raw mirrored 事件: %v", err)
	}
	seq, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("读取 raw mirrored seq: %v", err)
	}
	return seq
}
```

2. **加矩阵测试（8 分钟）**：追加 `TestB370AutomationDegradedIdentity`，复用 `newNoPTYAutomationEnv`、`createCoordCard`、`prebindConsumerSession`、`appendWorkflowDispatchForConsumerWithTarget`、`appendWorkflowMirroredForConsumerWithTarget`，从 `consumeAutomationEventsOnce` 进入：

```go
func TestB370AutomationDegradedIdentity(t *testing.T) {
	cases := []struct {
		name          string
		setup         func(t *testing.T, env *ledgerEnv, cardID string)
		wantProcessed int
		wantWake      string // 非空表示 expects 一次 Resume 且 briefing 含该子串
	}{
		{
			name: "missing keys with source equal current dispatch wakes",
			setup: func(t *testing.T, env *ledgerEnv, cardID string) {
				appendWorkflowDispatchForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-current", "review", "attempt-current")
				appendRawMirroredWithSource(t, env, cardID, "target-current", "attempt-current",
					`{"task_type":"question","payload":{"ticket_id":"legacy-missing-keys"}}`, 1)
			},
			wantProcessed: 1,
			wantWake:      "legacy-missing-keys",
		},
		{
			name: "empty string keys with source equal current dispatch wakes",
			setup: func(t *testing.T, env *ledgerEnv, cardID string) {
				appendWorkflowDispatchForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-current", "review", "attempt-current")
				appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-current", "", "", "question", 2, `{"ticket_id":"empty-keys"}`)
			},
			wantProcessed: 1,
			wantWake:      "empty-keys",
		},
		{
			name: "snapshot without workflow identity wakes",
			setup: func(t *testing.T, env *ledgerEnv, cardID string) {
				appendWorkflowDispatchForConsumerWithTarget(t, env.ledger, cardID, "target-current", "task-no-identity", "", "")
				appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "target-current", "task-no-identity", "", "", "question", 3, `{"ticket_id":"no-identity-snapshot"}`)
			},
			wantProcessed: 1,
			wantWake:      "no-identity-snapshot",
		},
		{
			name: "no dispatch snapshot wakes",
			setup: func(t *testing.T, env *ledgerEnv, cardID string) {
				appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-current", "", "", "question", 4, `{"ticket_id":"no-dispatch"}`)
			},
			wantProcessed: 1,
			wantWake:      "no-dispatch",
		},
		{
			name: "empty identity source mismatch is blocked",
			setup: func(t *testing.T, env *ledgerEnv, cardID string) {
				appendWorkflowDispatchForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-new", "review", "attempt-new")
				appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-old", "", "", "question", 5, `{"ticket_id":"stale-empty"}`)
			},
			wantProcessed: 0,
		},
		{
			name: "nonempty identity equal current dispatch wakes",
			setup: func(t *testing.T, env *ledgerEnv, cardID string) {
				appendWorkflowDispatchForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-current", "review", "attempt-current")
				appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-current", "review", "attempt-current", "question", 6, `{"ticket_id":"current-identity"}`)
			},
			wantProcessed: 1,
			wantWake:      "current-identity",
		},
		{
			name: "nonempty old attempt is blocked",
			setup: func(t *testing.T, env *ledgerEnv, cardID string) {
				appendWorkflowDispatchForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-new", "review", "attempt-new")
				appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-new", "review", "attempt-old", "question", 7, `{"ticket_id":"stale-identity"}`)
			},
			wantProcessed: 0,
		},
		{
			name: "delivery policy false is blocked after identity passes",
			setup: func(t *testing.T, env *ledgerEnv, cardID string) {
				appendWorkflowDispatchForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-current", "review", "attempt-current")
				appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-current", "review", "attempt-current", "permission_auto_allow", 7, `{"rule":"safe"}`)
			},
			wantProcessed: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, runner := newNoPTYAutomationEnv(t)
			cardID := createCoordCard(t, env)
			prebindConsumerSession(t, env, cardID)
			tc.setup(t, env, cardID)
			processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
			if err != nil || escalated || processed != tc.wantProcessed {
				t.Fatalf("processed=%d escalated=%v err=%v，want %d/false/nil", processed, escalated, err, tc.wantProcessed)
			}
			_, resumes, _ := runner.snapshot()
			if tc.wantWake == "" {
				if len(resumes) != 0 {
					t.Fatalf("不应唤醒，resumes=%v", resumes)
				}
				return
			}
			if len(resumes) != 1 || !strings.Contains(resumes[0], tc.wantWake) {
				t.Fatalf("应唤醒一次且含 %q，resumes=%v", tc.wantWake, resumes)
			}
		})
	}
}
```

3. **加「被拦仍记 seen 且消费循环继续」（3 分钟）**：追加 `TestB370AutomationBlockedEventSeenAndContinues`：

```go
func TestB370AutomationBlockedEventSeenAndContinues(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)
	appendWorkflowDispatchForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-new", "review", "attempt-new")
	blockedSeq := appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-new", "review", "attempt-old", "question", 1, `{"ticket_id":"stale"}`)

	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || escalated || processed != 0 {
		t.Fatalf("被拦事件 processed=%d escalated=%v err=%v，want 0/false/nil", processed, escalated, err)
	}
	env.srv.automationMu.Lock()
	_, seen := env.srv.automationSeen[blockedSeq]
	cursor := env.srv.automationCursor
	env.srv.automationMu.Unlock()
	if !seen {
		t.Fatalf("被拦事件 seq=%d 应记 seen", blockedSeq)
	}
	if cursor < blockedSeq {
		t.Fatalf("被拦事件应推进游标：cursor=%d seq=%d", cursor, blockedSeq)
	}

	appendWorkflowMirroredForConsumerWithTarget(t, env.ledger, cardID, "target-current", "attempt-new", "review", "attempt-new", "question", 2, `{"ticket_id":"current-after-block"}`)
	processed, escalated, err = env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || escalated || processed != 1 {
		t.Fatalf("被拦后消费循环应继续，processed=%d escalated=%v err=%v", processed, escalated, err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 || !strings.Contains(resumes[0], "current-after-block") {
		t.Fatalf("后续合法事件未唤醒: %v", resumes)
	}
}
```

4. **运行红/绿（3 分钟）**：先跑 `go test ./internal/agentd/ -run 'TestB370' -count=1`。**在 T1/T2 实现前它是红的**；实现后应 `ok` 且命中 3 支测试名。同理 `go test ./cmd/ -run 'TestB370' -count=1`。
5. **`TestB2336StaleAttemptDoesNotWake` 保绿（2 分钟）**：`go test ./internal/agentd/ -run '^TestB2336StaleAttemptDoesNotWake$' -count=1`，预期 `ok`。该夹具的 `task-empty`（node/attempt 空串，source_task=`task-empty`）在 F1=A 下，与卡上最新合格快照 `attempt-new` 不等 → 仍拦截。若它转红，**不许改夹具放宽**，先核对 F1 定位是否确实取「最新合格快照」。
6. **B349 既有测试核对（3 分钟）**：运行

```text
go test ./cmd/ -run 'TestB349CardWaitSourceIdentity|TestB349CardWaitSubtreeUsesEventCardIdentity' -count=1
go test ./internal/agentd/ -run '^TestB349AutomationSourceIdentity$' -count=1
```

   预期 `ok`。**本计划对 breakdown「重定基线」的处理**：三支 B349 夹具的空身份用例（cmd 侧 `missing-identity`、agentd 侧 `task-empty`）在新规则下仍落在「source 列与当前派发不等 → 拦截」，因此其既有断言无需改写即可继续保绿；**反转语义（空身份 source 相等 → 交付）由本 task 新增的 `TestB370CardWaitDegradedIdentity` / `TestB370AutomationDegradedIdentity` 独立锁死**，不靠 B349 夹具兼锁。若这三支在实现后转红，说明实现偏离了 F1/F2/F3，先修实现，不得改 B349 夹具。

7. **把命令与原始输出追加进台账。**

### 7.4 T3 接缝双向核对（对照 breakdown §3 入口指针）

| 声明接缝 | 入口测试（入口调用符号） | 断言 |
| --- | --- | --- |
| cmd 身份闸 × stdout | `TestB370CardWaitDegradedIdentity`（入口 `runCardWait` → `Store.Follow` → `json.Encoder`） | 缺键/空串空身份 source 相等交付；无身份快照交付；无派发交付；source 不等拦截；旧 attempt 拦截；策略假拦截；source 三列不丢 |
| agentd 身份闸 × Wake | `TestB370AutomationDegradedIdentity`、`TestB370AutomationBlockedEventSeenAndContinues`（入口 `consumeAutomationEventsOnce`） | 同上 + 被拦记 seen/推进游标/循环继续 |
| `--subtree` 事件卡取数 | `TestB349CardWaitSubtreeUsesEventCardIdentity`（入口 `runCardWait`） | 子卡事件按子卡快照判定 |
| 类型表顺序 | `TestB353AutomationUsesWaitDeliveryPolicy`、`TestB370AutomationDegradedIdentity`（策略假行） | 身份闸通过后 `WaitDeliveryPolicy` 仍只有一处事实源 |
| 共享实现 × 消费点 | 上述四条 + `internal/client/wakegate_test.go` 直测 | 两消费点都调 `client.JudgeMirroredWake`（编译期；`grep` 为证） |

**内部锁声明**：`internal/client/wakegate_test.go` 的直测入口是 `CurrentWorkflowAttempt` / `JudgeMirroredWake`，不在两消费点接缝上，属**附加内部锁**。合法理由：`st == nil`、分页到尾、`CurrentWorkflowAttempt` 的 `node==""` 恒假这些分支**从声明缝构造不出**——消费点的 `--follow` 夹具无法注入 nil store，也无法在一次消费中稳定制造 500+ 分页边界与同 node 多快照的精确 seq 关系；这些是判定层属性，只能直测。它们不顶替 §7.4 的缝级断言。

---

## 8. T4：全接缝回归、图/文档门禁与 F7 图产出

T4 只在 T1–T3 绿后执行，不新增生产路径。

### 8.1 回归命令与判据

按顺序运行并保存原始输出到台账：

```text
go build ./...
go vet ./internal/client/ ./internal/agentd/ ./cmd/
go test ./internal/client ./internal/ledger ./internal/agentd ./cmd -count=1
go test ./... -count=1
git diff --check
go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . resolve --doc docs/superpowers/plans/b370-plan.md
go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . validate
```

判定规则：

- 四个触及包必须 `ok`；全量 `go test ./...` **允许且仅允许** `cmd` 的 `TestRepoContractGate` 报一条 `dead-contract d_transport→d_ledger`（baseline 未重扫的已知红，contract §8）——原文记录，不改 target/测试/本卡范围。出现其它失败即 fail。
- `resolve --doc` 退出码 0；`validate` 退出码 0；`git diff --check` 退出码 0。
- `git diff --name-only` 只能出现 §3.1 列出的生产/测试/图/台账文件，不得出现上游 spec/contract/breakdown 改动、`target.json`/`best.json`/`baseline.json` 改动。

### 8.2 F7=A 本分支视图 diff 重扫（生成 `codegraph/diffs/cards-B370-charter-7.json`）

1. **全量重扫工作树（只落临时目录，不落库）**：

```bash
cd <repo根>
mkdir -p /tmp/b370-rescan
(cd scripts/codegraph-rescan && go run . --repo ../.. --old ../../codegraph/baseline.json --out /tmp/b370-rescan/baseline.new.json)
```

   预期 stderr 末行形如 `nodes=... containers=338 edges=... implements=... projections=... lifecycle=... packages=...`（本计划落稿前亲跑，得到 `nodes=5364 containers=338 edges=6741 ...`）。`scripts/codegraph-rescan/out/` 被 `.gitignore` 忽略，临时产物不入库。

2. **按 B370 归属符号派生 diff（只取本卡符号，不吸收 B358.3 等其它未 absorb 漂移）**：

```bash
python3 - <<'PY'
import json
base = json.load(open("codegraph/baseline.json"))
new = json.load(open("/tmp/b370-rescan/baseline.new.json"))
branch = "cards/B370-charter-7"
# 归属集合必须与 contract 视图 cards-B370-charter-4 同源，并加上 implement 新增/删除的符号。
ids = {
    "m_client_WakeGateReason", "m_client_WakeGateDecision", "m_client_WakeGateEvent",
    "n_client_CurrentWorkflowAttempt", "n_client_JudgeMirroredWake",
    "n_client_scanCardDispatchSnapshots", "n_client_judgeEmptyEnvelopeIdentity",
    "n_cmd_cardWaitEventActionable", "n_agentd_Server_acceptsCurrentWorkflowAttempt",
    "n_cmd_cardWaitCurrentWorkflowAttempt", "n_agentd_Server_currentWorkflowAttempt",
}
bn, nn = base["nodes"], new["nodes"]
be = {tuple(e) for e in base["edges"]}
ne = {tuple(e) for e in new["edges"]}

added = {k: nn[k] for k in ids if k not in bn and k in nn}
deleted = sorted(k for k in ids if k in bn and k not in nn)
modified = {}
for k in ids:
    if k in bn and k in nn:
        a, b = bn[k], nn[k]
        if a.get("signature") != b.get("signature") or a.get("file") != b.get("file") or a.get("line") != b.get("line"):
            b["signatureOld"] = a.get("signature", "")
            modified[k] = b
known = set(bn) | set(added)
eadd = sorted(e for e in (ne - be) if (e[0] in ids or e[1] in ids) and e[0] in known and e[1] in known)
edel = sorted(e for e in (be - ne) if e[0] in ids or e[1] in ids)

diff = {"view": branch, "base": base["meta"]["commit"],
        "summary": "B370 implement：降级三分支激活 + 包内三态取数 + 两消费点 reason 日志 + 旧快照扫描退役"}
if added:    diff["nodesAdded"] = added
if modified: diff["nodesModified"] = modified
if deleted:  diff["nodesDeleted"] = deleted
if eadd:     diff["edgesAdded"] = [list(e) for e in eadd]
if edel:     diff["edgesDeleted"] = [list(e) for e in edel]
json.dump(diff, open(f"codegraph/diffs/{branch.replace('/', '-')}.json", "w"),
          ensure_ascii=False, indent=1)
print("added", sorted(added)); print("modified", sorted(modified)); print("deleted", deleted)
print("edgesAdded", eadd); print("edgesDeleted", edel)
PY
```

   预期打印（本计划落稿前已用真实实现代码亲跑一次验证）：
   - `deleted` = `["n_agentd_Server_currentWorkflowAttempt", "n_cmd_cardWaitCurrentWorkflowAttempt"]`；
   - `added` = 契约 5 符号 + `n_client_scanCardDispatchSnapshots` + `n_client_judgeEmptyEnvelopeIdentity`（共 7）；
   - `modified` = `n_agentd_Server_acceptsCurrentWorkflowAttempt`、`n_cmd_cardWaitEventActionable`；
   - `edgesAdded` 含契约 8 条，另加新私有符号的 5 条（`CurrentWorkflowAttempt→scanCardDispatchSnapshots`、`judgeEmptyEnvelopeIdentity→scanCardDispatchSnapshots`、`judgeEmptyEnvelopeIdentity→m_client_WakeGateDecision`、`scanCardDispatchSnapshots→m_ledger_DispatchSnapshot`、`scanCardDispatchSnapshots→n_ledger_Store_EventsFromAsc`），共 13 条；
   - `edgesDeleted` 6 条（两条旧调用边 + 两个退役符号自身的出边 `currentWorkflowAttempt→m_ledger_DispatchSnapshot`/`→n_ledger_api_Facade_EventsFromAsc`、`cardWaitCurrentWorkflowAttempt→m_ledger_DispatchSnapshot`/`→n_ledger_Store_EventsFromAsc`）。
   **若某个新私有符号未出现在 `added`**：先 `go run ... sym <名字>` 确认节点 id（id 由 `m_/n_ + 包前缀 + 名字` 组成），把实际 id 补进 `ids` 重跑；不得把无关节点的漂移塞进本视图。
   **若 python3 不可用**：用等价的 `jq` 或一次性 Go 程序（`/tmp/b370-diffgen/main.go`，仅标准库）实现同一集合逻辑，不把派生程序提交入库。

3. **图门禁（本分支对照判据）**：

```text
go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . validate
→ 退出码 0（views 列表含 cards-B370-charter-7，且本视图引用完整）
go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . --view cards-B370-charter-7 check
→ 退出码 0，fails 为空
```

   无视图 `check` 仍报 `dead-contract d_transport→d_ledger`（baseline 未重扫），原文记录，不归因本卡、不绕闸；absorb（分支合回主线后）统一重扫后转绿。

4. **确认删除的符号已被视图声明删除**：`codegraph sym cardWaitCurrentWorkflowAttempt` 在**无视图**下仍命中基线（baseline 未重扫，符合预期）；`--view cards-B370-charter-7 sym cardWaitCurrentWorkflowAttempt` 应因 `nodesDeleted` 不再作为活跃节点返回（或只在 deleted 集合），原文记录读数。

5. **路径/符号锚核对**：`resolve --doc docs/superpowers/plans/b370-plan.md` 的坏锚（`vanished`/`file_missing`）必须为零；`moved` 不判失败。

6. 把全部命令与原始输出追加进台账，然后 `git add` 本节点产出（三个生产文件、三个测试文件、`codegraph/diffs/cards-B370-charter-7.json`、实现台账）并 commit（不 push）。若 amend 回填台账提交事实，只 amend 一次。

### 8.3 T4 真机清单（全部「未验证，需真机」，归协调者执行，实现轮不执行）

1. 现网旧写入者（如 mac-02 旧构建）真实产出的 `task_mirrored` envelope 形态：缺键 vs 空串；确认降级放行分支实际命中，`card wait --follow` 在 Claude Code/grok Monitor 上真能逐行唤醒。
2. 确凿旧 attempt 的真实迟到事件：确认 `card wait` 无 stdout、agentd 不唤醒、事件留账本且 reason 日志可查。
3. 同卡多节点/多 workflow 并发的真实账本流：确认 F1 的定位键语义不误杀合法事件、也不放行旧 attempt。
4. 本机 target 为空的真实派发与镜像：确认双空 target 仍唤醒/输出，空串不当缺身份。
5. `--subtree` 场景下事件子卡与 wait 根卡不同的真实流：确认按事件子卡查快照。
6. 真实 agentd 装配（`cmd/agentd.go#setupLedger` 先 `SetLedger` 后 `SetupAutomation`）与重启：确认 `s.ledger` 非 nil、消费循环被拦后 seen/游标推进、重启不重放已叫人事件。
7. 跨机 `target` 命名与 relay/断线：确认 source 列比较语义在真实命名下仍成立。
8. 历史 4289 条无 `node` 键镜像：本卡**不回溯唤醒**（消费从不回放）；确认真实消费从新事件起按新规则行为，旧账本可查。
9. 真实并发派发换 attempt 与 attach 变更：确认「读快照后动作」的 TOCTOU 结果符合产品接受语义。

---

## 9. 缺陷族对抗审查（逐族）

**1. 生命周期 / 状态机中断**
- T1 共享函数是纯同步读，不创建 goroutine/进程/临时目录；进程中途重启只重跑普通读，无孤儿资源。
- T2 退役旧扫描不改变消费循环退出、`seen`、游标推进与 `Store.Follow` 取消路径；被拦事件由既有 `automationMu`/游标路径承载。
- **未验证，需真机**：真实 agentd 重启、Keystone attach 变更、executor/mirror 在重启与断线下的收尾（§8.3 #6）。

**2. 静默失败 / 误导报错**
- 快照读/解码错误 → `scanCardDispatchSnapshots` 返回带 card/seq/type/from_seq 的错误 → 消费点带 ctx 上抛，不静默降级审计；解码失败/缺 `task_type`/`payload` 仍报错。
- reason 让「降级放行」与「按迟到拦截」都从日志对质（§3.2 C）；`consumeAutomationEventsOnce` 的粗粒度 `wake_gate_rejected` 不再冒称具体 reason。
- 无「报成功但没做」新窗口：本卡不新增写入事实。

**3. 跨平台假设**
- payload 键缺失/空串、source 列比较、JSON/stdout 不依赖 OS 路径/进程组/权限模型/webview。
- **未验证，需真机**：跨机 `target` 命名、真实旧写入者版本、网络/relay 行为（§8.3 #1/#7）。

**4. 假红 / 假绿测试**
- T1 直测断 `Deliver`+`reason` 两面，且放行/拦截两面都有；分页用例真写 501 行真实账本；`nil` store、`node==""`、`TaskID!=Attempt` 均有反面。
- T3 穿过真实调用方（`runCardWait`→`Store.Follow`→encoder；`consumeAutomationEventsOnce`→runner），禁只测 helper；每条正断言配反面（旧 attempt、source 不匹配、策略假、无派发、无身份快照）。
- 夹具补匹配的 `RecordDispatch`，不让「只有 AppendMirroredEvent」伪造当前 attempt；缺键形态用 raw `INSERT` 真造（`AppendMirroredEvent` 键恒存在）。
- 命令命中具体测试名，禁止 `no tests to run` 当绿。
- **未验证，需真机**：真实负载/并发下 `Store.Follow` seq 顺序、同卡合并、seen/cursor 不重复（§8.3 #9）。

**5. 门禁绕过**
- 新增写路径/执行路径？**无**：本卡只改判定、删死代码、加日志；不新增 CLI/HTTP/共享表入口，不新增 executor 调用。
- 覆盖全部表面？两处消费点是全仓唯一身份闸消费点，同一规则经同一共享符号，无「通道分裂」（`grep -rn "JudgeMirroredWake"` 两个调用点）。
- 检查与动作之间有窗口吗：快照查询与 Encode/Wake 非事务，**真实并发下 TOCTOU 未验证，需真机**（§8.3 #9）。
- 权限门：不触碰审批门，`WaitDeliveryPolicy` 集合不复制。

**6. 序列化边界**
- 新增 wire 字段？**无**。必须核对手写投影链：`card_events` 列 → `ledger.Event` → `eventWire`/`ledgerEventWire` → `proto.LedgerEvent` → `WakeGateEvent` → `JudgeMirroredWake`；`ledger.Event` → stdout JSON；envelope JSON `node`/`attempt` → `*string`。
- 每条链路有真实边界回归：缺键 vs `""` vs `null` vs 非空（`TestB2336WakeconsumerEnvelopeJSONBoundaries` 已锁，T3 新增缺键 raw 用例）；`source_target` 缺席/空串/非空；卡原生零值不伪造 source 键（`TestB349CardWaitSourceIdentity` 已锁）。
- 「两端各自有测试」≠「这条链路有测试」：T3 的 `TestB370AutomationDegradedIdentity` 有一条穿过真实消费调用方 + 真实 raw `INSERT` 的矩阵回归。

**7. 枚举新值过既有白名单**
- `WakeGateReason` 6 值流经 `WakeGateDecision.Reason` → 两消费点日志分支；两消费点都按任意 reason 打印，无中间 switch 丢弃。`WakeGateEmptyEnvelopeIdentity` 作为降级入口 `identity_state` 分类标签落日志，不再是判决结论（已声明，防「枚举值从未返回」误判）。
- 既有白名单核对：`WaitDeliveryPolicy` 七项假集合、`mirrorSkip` 三项、agentd 卡原生 switch、`proto.EventType` 词表——本卡不改其集合；身份闸在类型表之前，顺序不承重。
- `EvTaskMirrored`/`EvDispatched` 字面值不变。

**8. 承重安全属性有测试锁住**
- 「确凿旧 attempt 不叫醒」（`TestB370*` 拦截行 + `TestB2336StaleAttemptDoesNotWake` 保绿）、「双空 target 仍叫醒」（`TestB370CardWaitDegradedIdentity` 当前身份行 + `TestB349CardWaitSourceIdentity` 双空行）、「被拦不停消费循环」（`TestB370AutomationBlockedEventSeenAndContinues`）各有能变红的穿缝测试。
- 一次性 token/唯一性/隔离：**不命中**，本卡不新增 token/ticket/session/权限凭据。

**9. webview / 平台表现差异候选族**
- **无**，本卡不触及 `d_web`、Wails、Chromium、cookie、剪贴板、拖放或浏览器 API。

---

## 10. 序列化边界清单（实现轮逐条保留可失败断言）

1. `task_mirrored` envelope JSON `node`/`attempt` 四态（缺键 / `""` / `null` / 非空）：`internal/agentd/wakeconsumer_test.go#TestB2336WakeconsumerEnvelopeJSONBoundaries`（既有，保持）；T3 新增 raw 缺键用例穿过 `runCardWait`/`consumeAutomationEventsOnce`。
2. `card_events` source 三列 → `ledger.Event` → stdout JSON：`TestB370CardWaitDegradedIdentity` 断言交付行的 `SourceTask`/`SourceTarget` 不丢；`TestB349CardWaitSourceIdentity` 断言卡原生事件不伪造 source 键。
3. `ledger.Event` → `eventWire` → `proto.LedgerEvent` → `client.WakeGateEvent`：`TestB370AutomationDegradedIdentity` 逐字段核对 `CardID/Seq/Node/Attempt/TaskType/SourceTask/SourceTarget/SourceSeq`；`Node/Attempt` 指针形态在投影处保留。
4. `EvDispatched` payload → `DispatchSnapshot`：`TestB370CurrentWorkflowAttempt` 断言 `TaskID != Attempt` 不作身份、分页尾部仍可见；`TestB370JudgeMirroredWakeEmptyIdentity` 断言空 Node/Attempt 的派发落分支 2。
5. `JudgeMirroredWake` 返回值 → 两消费点日志：T2/T3 断言两消费点按 `reason` 落日志（放行与拦截都留痕），无中间白名单。

---

## 11. spec/contract 归属与接缝双向核对

### 11.1 用户故事逐条归属

| 用户故事（spec §用户故事） | 归属 task 与断言 |
| --- | --- |
| 1. 主会话 `card wait --follow`：旧写入者/无身份派发的镜像仍一行一醒；只有确凿旧 attempt 不叫醒 | T1 降级三分支直测；T3 `TestB370CardWaitDegradedIdentity`（入口 `runCardWait`）+ `TestB2336StaleAttemptDoesNotWake` 保绿 |
| 2. 小队自动化：同上，且被拦仍记 `seen` 并推进游标，不打死消费循环 | T3 `TestB370AutomationDegradedIdentity` + `TestB370AutomationBlockedEventSeenAndContinues`（入口 `consumeAutomationEventsOnce`） |
| 3. 排查者：每次判定带 `reason`，降级放行与按迟到拦截都能在日志里看到分支与依据 | T1 判定带 reason；T2 两消费点按 reason 落日志；T3 断言拦截/放行结果（日志字段由 T2 落盘） |

### 11.2 契约冻结清单覆盖

- §5.1 导出面（#1–#19）：T1 保持符号、签名、常量值与类型不变（Ticket 0 已满足，不重开）。
- §5.2 `CurrentWorkflowAttempt` 行为（#20–#24）：T1 `TestB370CurrentWorkflowAttempt`。
- §5.3 身份非空路径（#25–#35）：T1 `TestB370JudgeMirroredWakeNonEmptyIdentity`。
- §5.4 身份缺失/为空降级路径（#36–#42）：T1 `TestB370JudgeMirroredWakeEmptyIdentity`。
- §5.5 消费点接缝（#43–#50）：T3 穿缝矩阵 + `TestB2336StaleAttemptDoesNotWake` 保绿 + 卡原生事件不因缺 source 列过滤（`TestB349CardWaitSourceIdentity` 已锁）。
- §4 依赖方向与预算：不改 `target.json`；`d_transport→d_ledger` 活跃边由 `scanCardDispatchSnapshots` 的 `EventsFromAsc`/`DispatchSnapshot` 调用边承载，T4 视图 diff 落盘。
- §8 known-red：T4 如实记录无视图 `dead-contract`，不绕闸。

### 11.3 拍板 F1–F7 逐条对齐

| 拍板 | 本计划落点 |
| --- | --- |
| F1=A 空身份以卡上最新合格快照为当前身份比 source 列 | T1 `judgeEmptyEnvelopeIdentity`：`scanCardDispatchSnapshots(st, cardID, node)`，`node==""` 表示不过滤节点 |
| F2=A 包内新增未导出三态取数 | T1 `scanCardDispatchSnapshots` 返回 `(snapshot, qualified, anyDispatch, err)`，导出面不变 |
| F3=A 缺 `source_task` 返回 `WakeGateMissingSourceTask` | T1 非空身份路径在 `ev.SourceTask == ""` 时返回该 reason（先于快照比对） |
| F4=A `st==nil` 显式返回错误 | T1 `JudgeMirroredWake` 入口判 nil；T1 `TestB370JudgeMirroredWakeNilStore` |
| F5=A 两消费点各补按 reason 的日志 | T2 两处各一条 Info 日志带 `deliver`/`reason` |
| F6=A 单卡单轮 T0–T4 | 本计划 §1 执行顺序 |
| F7=A 实现轮本分支重扫并落 diff | T4 §8.2 |

---

## 12. 占位符扫描与自审

- **占位符扫描**：本计划无 TBD/「同 Task N」/「加适当错误处理」。生产改动给定完整代码块（`wakegate.go` 全文件）与精确替换位置（`card_wait.go`、`wakeconsumer.go`）。测试代码在 `internal/client/wakegate_test.go` 与两处追加均给完整代码。**声明例外**：`internal/client/wakegate_test.go` 与两消费点测试复用既有 harness（`ledger.Open`、`createCardWaitFixture`、`runLedgerCLI`、`newNoPTYAutomationEnv`、`createCoordCard`、`prebindConsumerSession`）——这些 harness 形态因包而异，本计划以「完整测试代码 + 命名既有 harness」给出，无骨架空测试。
- **内部锁自我声明**：`internal/client/wakegate_test.go` 直测是附加内部锁，合法理由已写在 §7.4（nil store / 分页 / `node==""` 从声明缝构造不出），不顶替缝级断言。
- **跨 task 类型/签名一致性**：T1 产出的 `WakeGateEvent` 字段与 T2 两消费点填入的字段逐字对齐（§3.2 表）；两消费点都消费 `client.WakeGateDecision{Deliver, Reason}`；无参数类型别名差异。
- **上下文预算**：T1 圈 `internal/client` 两文件；T2 圈三生产文件；T3 圈两测试文件；T4 圈图产出与命令。均能圈出有界文件集，不插竖切卡。
- **类型标注**：`d_transport`/`d_cli`/`d_gateway` 的机内行为用真实 SQLite 账本 + 真实消费调用方闭环；真实网络/旧写入者/目标 OS 行为在 §8.3 标「未验证，需真机」，不以单测外推。
- **派发自审**：本计划没有驱动 handoff/派发系统自身的验收步骤；不派发、不调用 handoff CLI、不起新 executor。

### 12.1 自审三查

1. **spec 覆盖**：§11.1 三条用户故事均指到 T1/T3 的具体穿缝测试；§11.2 覆盖契约 §5 全部条目。✔
2. **占位符扫描**：§12 首条，已声明内部锁例外；无未完成标记。✔
3. **跨 task 类型/签名一致性**：§3.2 表逐条对齐，两消费点填入字段与 `WakeGateEvent` 一致。✔

---

## 13. 实现交接的最终 pass 条件

只有以下事实全部真实满足才可把 implement 回合判 pass：

1. `go build ./...`、`go vet ./internal/client/ ./internal/agentd/ ./cmd/` 退出码 0；`gofmt` 无本卡新增差异。
2. `go test ./internal/client/ -run 'TestB370' -count=1`、`go test ./cmd/ -run 'TestB370|TestB349CardWait|TestB353CardWait' -count=1`、`go test ./internal/agentd/ -run 'TestB370|TestB349AutomationSourceIdentity|TestB2336StaleAttemptDoesNotWake|TestB353Automation' -count=1` 命中具体测试名并 `ok`。
3. 降级三分支、A 路径六项条件、双空/一空 target、`missing_source_task`、分页到尾、`node==""`、`st==nil` 均由直测或穿缝测试锁定。
4. 两处旧扫描符号全仓无定义/引用；两消费点按 `decision.Reason` 落日志；`WaitDeliveryPolicy` 顺序不变。
5. T4 的触及包回归、`git diff --check`、文件集合满足 §8.1；无视图 `codegraph check` 只保留已记录的 `dead-contract d_transport→d_ledger`，`--view cards-B370-charter-7 check` fails 为空、退出码 0；`codegraph validate` 退出码 0。
6. `codegraph/diffs/cards-B370-charter-7.json` 已落盘并含 `nodesDeleted`（两个退役符号）与 `nodesAdded`（新私有符号 + 契约 5 符号）。
7. 真机清单（§8.3）仍明确标为「未验证，需真机」，不用单测、fake runner 或日志替代真实 agentd/executor/目标 OS 结论。
