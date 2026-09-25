# B402 契约增量：回合零文本失败的单次自动续接与生命周期不早归档

**上游状态：已批准**（源：`docs/superpowers/specs/b402.md` 头部「状态：已批准」；用户 2026-09-25 批准范围 B）
**级别：L3 轻档**（跨 proto/executor/orchestration/ledgerstep 四域，单轮实现可闭合；不新增子系统）
**有效基线：`cards/B402-spec` @ `5d6607fcf344f0d3e533b67b40e174f2e4223f4f`**
**架构形态：按子系统分域的平铺领域包，无横向 controller/service/dao 分层**（`codegraph/best.json`，`codegraph domains` 实测 26 领域）
**冻结状态：本提交随 `codegraph/target.json`（本轮无新增方向/预算，未改）与 `codegraph/diffs/cards-B402-charter.json` 冻结**
**Ticket 0 骨架：本提交同批落码并通过编译（命令与原始输出见 §7）**

本文件把 spec 的语义翻译成精确签名/类型/常量，每个签名都对照现状代码查证。查证期顺手确立的实现级决定落 §6 移交区，不占冻结条目名额。

## 0. 判据收口

1. 失败分类必须先成为**跨进程封闭分类**（`proto.FailureClass`），再从「明确的零文本事实」提升为「可自动续接一次」的行为；不得从 `FailReason` 文案、事件顺序或 git 状态反推。
2. 自动续接只在**节点等待器**里发生，且一次 `RunOnce` 最多一次；续接失败或外部抢先续接（409）一律 fail-closed。
3. `FinishTask` 不再是无条件 defer：只有业务裁决落账与全部收口动作（只读闸/产出/发布/路由）完成后才显式调用；任何早退都把任务留在 `waiting_review`。
4. 旧 agentd / 旧 adapter / 缺字段的历史事件语义是「不可自动重试」，不是「零文本」。

## 1. 现状签名查证（代码事实，非设计发挥）

| 现状签名/字面值 | 代码事实出处 | 本契约变化 |
| --- | --- | --- |
| `type Result struct { OK bool; FailReason string; VoidReason string; … }` | `internal/executor/executor.go#Result`（`:82`） | 新增字段 `FailureClass proto.FailureClass`（`:96`） |
| `func waitForTurnEnd(ctx, wait) error` | `internal/ledgerstep/wire.go#waitForTurnEnd`（`:53`） | 返回 `(TurnEnd, error)`（`:88`），失败终态带 `FailureClass` |
| `func waitForTurnEndGrace(ctx, wait) error` | `internal/ledgerstep/wire.go#waitForTurnEndGrace`（`:96`） | 返回 `(TurnEnd, error)`，并接收首个残缺 completed 的 `TurnEnd`（`:134`） |
| `func finalMessageFromEvents(events) (string, error)` | `internal/ledgerstep/wire.go#finalMessageFromEvents`（`:157`） | **签名不变**；completed 优先与显式空 `final_text` 语义不回归 |
| `func clientFinalMessage(ctx, cl, taskID) (string, error)` | `internal/ledgerstep/wire.go#clientFinalMessage`（`:139`） | **签名不变**（B233.16 编译期锁；`internal/ledgerstep/execution_signature_lock_test.go:20`） |
| `func (r *StepRunner) awaitNode() func(ctx,target,taskID) (string, error)` | `internal/ledgerstep/runner.go#StepRunner.awaitNode`（`:414`） | 内部新增「零文本 → Continue 恰一次 → 再等」编排；对外注入签名不变（`:426`） |
| `func (n *NodeStep) RunOnce(ctx, cardID) (Outcome, error)` | `internal/ledgerstep/node.go#NodeStep.RunOnce`（`:187`） | 签名不变；`FinishTask` 由 Await 后无条件 defer 改为收口末尾显式调用 |
| `FinishTask` defer 现状 | `internal/ledgerstep/node.go`（B341 引入，`:283-290`） | 删除 `defer`；仅 `routeTo` 成功后调用一次 |
| `type FailedPayload struct { FailReason; Branch; CommitHash; ProcUsage }` | `internal/orchestration/contracts.go#FailedPayload`（`:137`） | 新增 `FailureClass proto.FailureClass \`json:"failure_class,omitempty"\``（`:142`） |
| `func NewFailedPayload(reason, branch, commit string) FailedPayload` | `internal/orchestration/contracts.go#NewFailedPayload`（`:158`） | 加第 4 参数 `class proto.FailureClass`（`:163`），写入 payload |
| `handleResult` 的 `!OK` 分支 | `internal/orchestration/manager.go#Manager.handleResult`（`:3414`，失败事件构造 `:3497`） | 透传 `r.FailureClass` 给 `NewFailedPayload` |
| `func (m *Manager) Continue(ctx, taskID, instructions string) error` | `internal/orchestration/manager.go#Manager.Continue`（`:1406`）；状态门 `:1420`（需 `waiting_review`）；HTTP `internal/agentd/handlers.go:824-853` | 复用既有能力，**不新增端点**；空 instructions 被 400 拒 |
| `Continue` 客户端面 | `internal/client/execution.go:38`（`ExecutionClient.Continue`） | 复用；不新增接口 |
| 五家 adapter 零文本分支 | `internal/executor/opencode/adapter.go:2204-2239`（`mapIdle`）、`codex/adapter.go:907-915`（`finishTurn`）、`grok/adapter.go:675-683`（`finishTurn`）、`claudecode/adapter.go:889-897`（`fallbackClassify`）、`agy/adapter.go:683-695`（`fallbackClassify`） | 各加一行 `FailureClass: proto.FailureClassZeroText`（`:2232/:912/:679/:893/:691`） |
| `NoTrailerResult`（无 trailer 但有新提交） | `internal/executor/turn/fallback.go#NoTrailerResult`（`:68`） | **不写分类**：已有工作必须先由协调者裁决，避免重复执行 |
| 权限被拒空回合 → `question` | `internal/executor/opencode/adapter.go:2208-2218` | **不写分类**：那个现场有内容可问 |
| `EventTypeTurnFailed` / `EventTypeFailed` 语义分离 | `internal/proto/proto.go`（`:47-59`）、`internal/orchestration/manager.go:3494-3498` | 沿用；只给 `turn_failed` 增可选分类，不重开终态设计 |

## 2. 契约增量（精确到可编译）

### 2.1 分类词表（`internal/proto/failure.go`，新增）

```go
type FailureClass string

const FailureClassZeroText FailureClass = "zero_text"
```

规则：封闭词表；空值=未分类，消费方一律 fail-closed。只允许 adapter 的明确零文本分支产出 `zero_text`。

### 2.2 executor 结果（`internal/executor/executor.go`）

```go
type Result struct {
    // …既有字段不变…
    FailureClass proto.FailureClass // 空=未分类；只有零文本分支写 zero_text
}
```

### 2.3 失败 payload（`internal/orchestration/contracts.go`）

```go
type FailedPayload struct {
    FailReason   string             `json:"fail_reason"`
    FailureClass proto.FailureClass `json:"failure_class,omitempty"` // additive、omitempty
    Branch       string             `json:"branch,omitempty"`
    CommitHash   string             `json:"commit,omitempty"`
    ProcUsage    *prochost.Admission `json:"proc_usage,omitempty"`
}

func NewFailedPayload(reason, branch, commit string, class proto.FailureClass) FailedPayload
```

`failure_class` 是 additive/omitempty 的 wire 字段：不新增事件类型、不新增 HTTP 端点、不迁移账本表。空分类不在 payload 中出现（旧 wire 形状不变）。

### 2.4 类型化终态接缝（`internal/ledgerstep/wire.go`）

```go
type TurnEnd struct {
    EventType    proto.EventType
    Seq          int64
    FailureClass proto.FailureClass // 仅 turn_failed 且精确值时有意义
}

type failedPayload struct { // wire 字段子集，wait 层与 final 层共用同一解析形状
    FailReason   string             `json:"fail_reason"`
    FailureClass proto.FailureClass `json:"failure_class"`
}

func failureClassOf(event *proto.Event) proto.FailureClass
func waitForTurnEnd(ctx context.Context, wait func(context.Context) (*proto.Event, error)) (TurnEnd, error)
func waitForTurnEndGrace(ctx context.Context, wait func(context.Context) (*proto.Event, error), completed TurnEnd) (TurnEnd, error)
```

规则：

1. `completed` 终态返回 `TurnEnd{EventType: completed, Seq: 事件 seq}`；`turn_failed`/`failed` 终态返回对应类型与 seq。
2. `failureClassOf` 只认 `turn_failed`；`failed` 事件、缺字段、JSON 不可解析一律返回零值。
3. 未知分类值原样带出（消费方只认 `== zero_text` 的精确匹配，故未知值天然不可重试）。
4. `finalMessageFromEvents` / `clientFinalMessage` 签名与语义不动；分类的**唯一权威是 `waitForTurnEnd`**，避免两个来源漂移（见 §5-D3）。

### 2.5 节点等待器的单次续接（`internal/ledgerstep/runner.go`）

```go
const ZeroTextContinueInstruction = "上一回合以零文本结束（可能是供应商流中断）。请在不重复已完成工作的前提下继续当前任务，" +
    "并在回合结束时按纪律输出 handoff-verdict 裁决块。"

func (r *StepRunner) awaitNode() func(context.Context, string, string) (string, error)
```

编排顺序（在生产注入函数内部）：

1. `waitForTurnEnd` 取首个终态 `end`。
2. 若 `end.EventType == turn_failed && end.FailureClass == FailureClassZeroText`：调 `cl.Continue(ctx, taskID, ZeroTextContinueInstruction)`；失败即返回错误（fail-closed），不再等。
3. 成功则再 `waitForTurnEnd` 一次，第二个终态才用于取报文。
4. `clientFinalMessage` 取最终报文返回。

约束：一次 `RunOnce` 最多一次；不沿用自动计数到下一次 `RunOnce`；进程重启不重放已消费的失败事件（spec §4.2）。

### 2.6 节点生命周期（`internal/ledgerstep/node.go`）

`FinishTask` 从 Await 后的无条件 `defer` 改为函数末尾、`routeTo` 成功之后的显式调用：

- 只在「裁决已解析并落账 + 只读闸/产出/发布/路由全部完成」后调用一次；
- 解析失败、`Await` 失败、diff/产出/发布/写闸失败、路由失败：一律不调用，任务留 `waiting_review`；
- `FinishTask` 自身失败：记 Warn 并 `haltForHuman(reason="task 归档失败")`，不得把「已移卡」伪装成「task 已归档」。

## 3. 原子冻结清单（每条独立可判 pass/fail）

1. `proto.FailureClassZeroText` 精确等于字面值 `"zero_text"`，底层类型为 `string`。
2. `executor.Result` 含 `FailureClass` 字段，零值默认「未分类」。
3. `FailedPayload.failure_class` 为空时 JSON 不出现该键（omitempty 生效）。
4. `FailedPayload.failure_class` 为 `zero_text` 时 JSON 精确呈现该键值。
5. `NewFailedPayload` 第 4 参数写入 payload 的 `FailureClass`；传空串时字段省略。
6. `handleResult` 的 `!OK` 分支把 `Result.FailureClass` 透传进 `turn_failed` payload（`Result` 携带分类 → payload 携带分类）。
7. `Result` 无分类时 `handleResult` 产出的 `turn_failed` payload 不含 `failure_class` 键。
8. `waitForTurnEnd` 对带 `final_text` 的 `completed` 返回 `TurnEnd{completed, seq}`。
9. `waitForTurnEnd` 对 `turn_failed` payload 带 `failure_class:"zero_text"` 返回 `TurnEnd.FailureClass == zero_text` 且保留 seq。
10. `failed` 事件即使带 `failure_class:"zero_text"` 字段，`TurnEnd.FailureClass` 仍为零值。
11. `turn_failed` 缺 `failure_class` 或值为未知串时，`TurnEnd.FailureClass != zero_text`。
12. `finalMessageFromEvents` 的 completed 优先、非空 `final_text` 优先、显式空 `final_text` 报错三条语义不回归。
13. `awaitNode`：`zero_text → completed` 时 `Continue` 恰 1 次、`WaitEvent` 恰 2 次、返回第二个终态的报文。
14. `awaitNode`：`zero_text → zero_text` 时 `Continue` 恰 1 次（不二次续接）。
15. `awaitNode`：`turn_failed` 无分类时不调用 `Continue`。
16. `awaitNode`：`Continue` 返回错误时不进入第二次等待，函数返回错误。
17. `awaitNode` 续接指令恒为非空、固定文本（与 `ZeroTextContinueInstruction` 一致，不携带现场或用户输入）。
18. `RunOnce` 裁决解析失败时 `FinishTask` 调用 0 次，Outcome 为 `needs_human`。
19. `RunOnce` 的 `FinishTask` 失败时 Outcome 为 `needs_human` 且 Reason 为 `task 归档失败`。
20. `RunOnce` pass 路径中 `FinishTask` 仍在 `PublishWorkBranch` 之后（B341 顺序不回归）。
21. 五家 adapter 的明确零文本分支产出的 `Result.FailureClass == zero_text`。
22. 缺字段/未知分类的旧 wire 事件不触发自动续接（跨 1/10/11/15 的端到端汇合判据）。

## 4. 邻近分支（永不做，防越界）

- 不按 `FailReason` 文案匹配，不要求改供应商文案。
- 不给 `failed` 终态、权限被拒、内容过滤、进程退出、`NoTrailerResult`（有提交无 trailer）自动续接。
- 不新增 retry 事件类型、数据库列、HTTP retry 端点，不做跨重启自动重放。
- 不在本卡抑制首个 `turn_failed` 的唤醒投递。

## 5. 三重闸门拍板记录

1. **失败分类只由 adapter 明确零文本分支生产，经 `Result → payload → wire` additive 透传，不从文案/事件顺序/git 反推。** 难逆转：它定义了一条新的跨进程 wire 字段与四域生产链，回改要动 proto/executor/orchestration/ledgerstep；无上下文会惊讶：后人看到零文本有一条专门的路径，会想「直接匹配 fail_reason 里的零文本不就行了」——那正是 B100 禁止的静默漂移；真取舍：被否方案是文案匹配、以及把 `NoTrailerResult`（已有提交）一并归为可重试（会重复执行已落地修改）。
2. **自动续接编排落在 `StepRunner.awaitNode`（节点等待器），`NodeStep.RunOnce` 只保留「不早归档」与正常裁决路由。** 难逆转：等待/续接/归档三层职责就此划分，回改要么搬动重试、要么再切一次接缝；无上下文会惊讶：spec §4.2 写的是「节点执行体处理顺序」，后人会把重试搬进 `RunOnce`；真取舍：被否方案是把 `NodeStep.Await` 返回值改成类型化 `TurnResult` 让 `RunOnce` 直接编排——那会改动 40+ 处既有测试注入签名，且 `clientFinalMessage` 已被 B233.16 冻结。**此条「反过来写仍能通过行为测试」（把同一段代码放进 `RunOnce` 行为等价），必须记录，否则后人一次「顺手归位」就无声推翻接缝归属。** 归属真源由 `codegraph` 视图与本节冻结。
3. **分类权威只在 `waitForTurnEnd` 的 `TurnEnd`，`finalMessageFromEvents`/`clientFinalMessage` 签名不变。** 难逆转：`clientFinalMessage` 的 `(string,error)` 被 `internal/ledgerstep/execution_signature_lock_test.go:20` 编译期锁死，改它会先红；无上下文会惊讶：spec §7.2 把 `finalMessageFromEvents` 列为「失败终态可携带分类」的接缝，后人会去给它加返回字段；真取舍：被否方案是新增一个类型化的 `finalMessageFromEventsWithClass` 兄弟函数——那会制造第二个分类来源，与「只认精确值、单一权威」冲突。

## 6. 移交 plan 附区（不计冻结条目，待 plan 吸收后销区）

- 五家 adapter 的**邻近分支反例断言**（question/权限被拒/内容过滤/进程退出/`NoTrailerResult` 不得写 `zero_text`）：本轮只锁了各 adapter 零文本分支的**正例**（§3-21），未逐条加负例断言；归 plan。
- `handleResult` 的 `failed` 终态分支不写分类（结构上 `failed` 分支不经过零文本路径），本轮未加独立夹具。
- 端到端夹具（spec §9.1 的 `httptest` 主红测与阶段化 task）：`zero_text→completed`、`zero_text→zero_text` 的两条**跨进程**回路归实现节点，本轮只锁了 `awaitNode` 单测层。
- 日志字段（task/seq/failure_class/attempt/continue 结果，且不含完整正文/凭据）的断言归 plan。
- 固定续接指令文本若后续要调整，走契约修订而非顺手改。

## 7. 本节点法定产出与本轮验证

### 7.1 Ticket 0 骨架产出

- 新增 `internal/proto/failure.go`：`FailureClass` + `FailureClassZeroText`。
- 新增 `internal/ledgerstep/wire.go` 的 `TurnEnd`/`failedPayload`/`failureClassOf`，并把 `waitForTurnEnd`/`waitForTurnEndGrace` 改为类型化返回。
- 新增 `internal/ledgerstep/runner.go` 的 `ZeroTextContinueInstruction` 与 `awaitNode` 单次续接编排。
- `internal/ledgerstep/node.go`：删 `FinishTask` 无条件 defer，改为收口末尾显式调用 + 失败转人工。
- `internal/executor/executor.go`：`Result.FailureClass`。
- 五家 adapter 零文本分支写分类；`internal/orchestration/contracts.go` 与 `manager.go` 透传分类；`NewFailedPayload` 加第 4 参数。
- 新增 `internal/ledgerstep/b402_retry_test.go`；扩展 `wire_test.go`、`handleresult_notrailer_test.go` 与五家 adapter 的零文本测试。

### 7.2 本轮亲跑命令与原始输出

```text
$ go build ./...                                            → BUILD_EXIT=0
$ gofmt -l internal cmd                                     → internal/agentd/cardstep.go
                                                              internal/ledger/types.go
    （上列两文件是本卡开工前既存未格式化文件，本节点未触碰；本卡改动文件 gofmt 干净）
$ go vet ./...                                              → VET_EXIT=0
$ go test ./... -count=1                                    → TEST_EXIT=0（全绿）
$ go test -race ./internal/ledgerstep/ ./internal/orchestration/ ./internal/proto/ ./internal/executor/... -count=1
    ok  github.com/Xsxdot/handoff/internal/ledgerstep       19.893s
    ok  github.com/Xsxdot/handoff/internal/orchestration    78.057s
    ok  github.com/Xsxdot/handoff/internal/proto             1.119s
    ok  github.com/Xsxdot/handoff/internal/executor          1.228s
    ok  github.com/Xsxdot/handoff/internal/executor/agy      1.335s
    ok  github.com/Xsxdot/handoff/internal/executor/claudecode 5.051s
    ok  github.com/Xsxdot/handoff/internal/executor/codex     7.006s
    ok  github.com/Xsxdot/handoff/internal/executor/grok      2.394s
    ok  github.com/Xsxdot/handoff/internal/executor/opencode 22.283s
    → RACE_EXIT=0
$ codegraph --repo . check                                  → CHECK_EXIT=0（fails=[]）
$ codegraph --repo . --view cards-B402-charter check        → CHECK_EXIT=0（fails=[]）
$ codegraph --repo . --view cards-B402-charter sym FailureClass / TurnEnd / failureClassOf / waitForTurnEnd
    → 四条都命中新视图节点，anchor=ok
$ codegraph --repo . validate                               → 退出 1，2 个既存问题：
    [cards-B272-charter] 新增容器 k_dropdir_fn 引用不存在的基线领域 d_coordination_api
    [cards-B374-charter] 新增容器 k_collab_model 已存在于基线，containersAdded 只接受新容器
    （均非本卡；本卡视图 cards-B402-charter 无 issue）
```

变异复验（改后立即还原）三条，证明冻结语义有牙：

```text
变异① awaitNode 的续接条件由 FailureClassZeroText 改成 "disabled"：
  go test ./internal/ledgerstep/ -run 'TestAwaitNodeAutoContinuesZeroTextOnce|TestAwaitNodeContinuesAtMostOnce'
  → FAIL b402_retry_test.go:109: 一个 RunOnce 最多自动续接一次，实际 0
变异② 解析失败早退路径补一次 FinishTask：
  go test ./internal/ledgerstep/ -run TestNodeStepDoesNotFinishTaskOnParseFailure
  → FAIL b402_retry_test.go:181: 解析失败不得归档 task，FinishTask 调用 1 次
变异③ failureClassOf 忽略 payload 字段（恒返回 ""）：
  go test ./internal/ledgerstep/ -run TestWaitForTurnEndCarriesZeroTextClass
  → FAIL wire_test.go:268: failure_class 必须透传 zero_text，实际 ""
```

三条变异后均从 `$TMPDIR` 备份还原，`git diff --stat` 复核只剩预期改动。

### 7.3 图与视图

- `codegraph/target.json` **未改**：本卡未新增跨域依赖方向或超预算调用（`d_ledger→d_protocol`、`d_orchestration→d_protocol`、`d_execution_adapters→d_protocol` 均在既有契约面内），`codegraph check` 基线 fails=0。
- `codegraph/best.json` **未改**：不新增容器/领域；新符号分别归既有 `k_proto_model`、`k_ledgerstep_model`、`k_ledgerstep_fn`。
- 新增 `codegraph/diffs/cards-B402-charter.json`：`nodesAdded` 3（`m_proto_FailureClass`、`m_ledgerstep_TurnEnd`、`n_ledgerstep_failureClassOf`）、`nodesModified` 6（`m_executor_Result`、`m_orchestration_FailedPayload`、`n_orchestration_NewFailedPayload`、`n_ledgerstep_waitForTurnEnd`、`n_ledgerstep_waitForTurnEndGrace`、`n_ledgerstep_StepRunner_awaitNode`）。下游以 `codegraph --repo . --view cards-B402-charter sym <符号>` 可命中。

## 8. 图覆盖债

- 本卡新符号 `FailureClass`、`TurnEnd`、`failureClassOf` 在 baseline 中不存在；已随本提交写入 `codegraph/diffs/cards-B402-charter.json` 的分支视图，故不构成「自己冻结的符号自己欠图覆盖」。
- **视图为结构性最小增量**（只登记本卡新增/修改节点），不是一次全量重扫：本卡触碰文件的其余符号沿用 baseline 定义。若后续要把该视图 absorb 回基线，应按 `docs/codegraph-scan-recipe.md` 走一次扫描，并覆盖本卡新增测试节点（`b402_retry_test.go`）与 `internal/proto/failure.go` 的文件级完整性自检。此项作为**扫描欠账**交 plan/实现轮显式认账。
- `codegraph validate` 的 2 个既存问题（B272/B374 视图）非本卡引入，未在本节点修。

## 9. 欠账（显式，不静默）

1. §6 移交区的邻近分支反例断言、跨进程端到端回路、日志断言未在本节点落测试，交 plan。
2. 分支视图为最小结构增量，非全量扫描（§8），absorb 前的重扫交后续扫描轮。
3. `internal/agentd/cardstep.go`、`internal/ledger/types.go` 为开工前既存未 `gofmt` 文件，本节点未触碰（不属本卡欠账，仅记录事实）。

## 10. 修订记录（breakdown 出稿轮，2026-09-25）

以下四条为 breakdown 节点的**边界澄清**（均不改变本契约冻结面，不退回重冻），逐条对照 `codegraph/best.json` 与 `codegraph --repo . --view cards-B402-charter sym` 实读得出；产出见 `docs/superpowers/specs/b402-breakdown.md` §1/§2.4。

1. **域归属按图修正**：头部所称「跨 proto/executor/orchestration/ledgerstep 四域」在图上是 5 个逻辑域/子域——`proto.FailureClass → d_protocol`、`executor.Result → d_execution_contract`、五家 adapter → `d_execution_adapters`、`FailedPayload`/`handleResult`/`Continue → d_orchestration`、`ledgerstep`（`TurnEnd`/`waitForTurnEnd`/`awaitNode`/`RunOnce`）→ `d_ledger`；另有零改动的边界面 `d_gateway`/`d_transport`。定级不变，仅记图事实。
2. **驱动接缝域归属补记**：`internal/agentd/cardstep.go#Server.runStep` 与 `internal/agentd/handlers.go#Server.handleContinue`/`#Server.handleEvents` 归 `d_gateway`；本卡在这三处零生产改动，仅作端到端 e2e 接缝。
3. **续接客户端面域归属补记**：`internal/client/client.go#Client.Continue` 归 `d_transport_channel`（父 `d_transport`）；`ExecutionClient.Continue` 接口面未入图。本卡零改动。
4. **§6 移交区与 spec §9.2 矩阵的差额显性化**：spec §9.2 还含「`zero_text→普通 turn_failed`」「`NoTrailerResult` 有新提交不续接」「旧 wire 缺字段形状」「diff/产出/发布/写闸失败均不调用 Done」四项未落测试；它们**不是新接缝**（冻结载体齐备），由 breakdown 落为 S1/S2/S3 验收，不退回 contract。
