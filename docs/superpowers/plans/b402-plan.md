# B402 plan：派发节点零文本回合的单次自动续接——测试矩阵补齐、跨进程回路与文档/图收口

> 卡 B402 · 入口节点 charter:plan · 产出口节点
> 上游 spec `docs/superpowers/specs/b402.md`（已批准）· contract `docs/superpowers/specs/b402-contract.md`（已批准/冻结）· breakdown `docs/superpowers/specs/b402-breakdown.md`（已拍板 P1–P5 全甲）
> 有效基线 `cards/B402-spec` @ `5d6607fc`；本节点工作分支 `cards/B402-charter-6`，起手 HEAD `2fb6a9d7`。
> 台账 `docs/superpowers/ledgers/2026-09-25-b402-plan-ledger.md`（含亲跑命令、原始输出、图查询记录）。
> **本节点只出计划，不写实现。** 凡引用行号者动手前重核，漂了以符号/引文为准。
> 「未验证」= 本节点没亲自跑到结果，实现节点必须自己复核。
> 读者假设：对 handoff 仓零上下文的执行者。

---

## 0. 待拍板清单

**无新增岔口。** 本卡五项岔口 P1–P5 已由协调者在 `b402-breakdown.md` §8 逐项拍板（全甲），本 plan 逐条吸收：

| 编号 | 裁定 | 对 plan 的约束 |
|---|---|---|
| P1 | **甲：代码单轮闭合** | `proto.FailureClass` / `executor.Result.FailureClass` / 五家 adapter 分类 / `FailedPayload` 透传 / `WaitForTurnEnd`+`TurnEnd` / `awaitNode` 单次续接 / `RunOnce` 去 defer 已随 contract Ticket 0 落地。本 plan **不含任何 `internal/**` 生产代码改动 task**；T0 只做「复核其在场且绿」的门禁，T1–T3 只加测试，T4 只改文档，T5 只碰 codegraph 产物。 |
| P2 | **甲：跨进程 e2e 机内验** | T3 用真 `*Manager` + 真 `agentd.Server` + `fake` 脚本 adapter + `httptest`，照抄 `internal/orchestration/gateway_http_test.go` 的环境/harness；不依赖真实供应商。 |
| P3 | **甲：视图保持最小增量** | T5 **不做**全量重扫/absorb；只做 `codegraph` 三闸 + 逐符号对账 + 把扫描欠账显式记入本卡台账。 |
| P4 | **甲：日志断言并入 S2** | T2 内含日志捕获断言，与 `awaitNode`/`RunOnce` 早退矩阵同文件同夹具。 |
| P5 | **甲：roadmap 独立 S4** | T4 是唯一文档 task，只改 `docs/roadmap.md`。 |

执行者若认为需要越出上表（例如改生产代码修「顺手发现」的问题），**停下提问，不自作主张**：单行 JSON `{"ask":"..."}`。

---

## 1. 问题与现状（证据驱动；原始读数见台账）

### R1：Ticket 0 已落地全部运行时代码，本卡剩余面 = 测试补齐 + 文档 + 图对账

contract Ticket 0 已把 spec §4 的生产路径全部落码（本节点亲验在位，文件清单与符号见表）：

- 分类词表：`internal/proto/failure.go#FailureClass` + `FailureClassZeroText="zero_text"`（`:15/:23`）。
- 结果字段：`internal/executor/executor.go:96`（`Result.FailureClass`，符号锚 `Result.FailureClass` 不在图，见「图覆盖债」）。
- 五家 adapter 明确零文本分支写分类：opencode `adapter.go:2232`、codex `:912`、grok `:679`、claudecode `:893`、agy `:691`（本节点 `grep -n FailureClass` 实读）。
- 透传：`internal/orchestration/contracts.go:142`（`FailedPayload.FailureClass`，omitempty）、`NewFailedPayload(..., class)`（`:163`）；`internal/orchestration/manager.go#Manager.handleResult` 在 `!OK` 分支把 `r.FailureClass` 传入（`:3498`）。
- 等待层类型化：`internal/ledgerstep/wire.go#TurnEnd`（`:41`）、`#failedPayload`（`:48`）、`#failureClassOf`（`:55`，只认 `turn_failed`）、`waitForTurnEnd (TurnEnd,error)`（`:88`）、`waitForTurnEndGrace(...,completed TurnEnd)`（`:135`）。
- 单次续接编排：`internal/ledgerstep/runner.go:416`（`ZeroTextContinueInstruction`，不在图）、`#StepRunner.awaitNode`（`:426`，`:450` 判 `turn_failed && FailureClass==zero_text` → `Continue` 一次 → 再等）。
- 生命周期：`internal/ledgerstep/node.go#NodeStep.RunOnce` 删无条件 defer，`:486` 在 `routeTo` 成功后显式 `FinishTask`；归档失败 `haltForHuman("task 归档失败")`（`:489`）。

⇒ P1=甲，**没有可扇出的代码实现 task**。plan 的代码侧只剩「复核在场且绿」（T0）与三类测试补齐（T1/T2/T3）。

### R2：现状测试覆盖了正例，但承重安全属性的**反例**尚缺可变红断言

Ticket 0 已加的零文本**正例**（本节点实读）：

| adapter | 正例测试（断言 `FailureClass=="zero_text"`） | 断言行 |
|---|---|---|
| opencode | `TestApprovedPermissionEmptyTurnEmitsFailedResult`（`adapter_test.go:751`） | `:772` |
| codex | `TestNoTrailerZeroTextStillFailsWithLiveExecutor`（`fallback_verdict_test.go:145`） | `:157` |
| grok | `TestNoTrailerZeroTextStillFailsWithLiveExecutor`（`fallback_verdict_test.go:154`）+ `TestFinishTurnEmptyTextEmitsFailedResult`（`askquestion_internal_test.go:45`） | `:61` |
| claudecode | `TestFallbackZeroTextGuardStillFires`（`fallback_verdict_test.go:151`） | — |
| agy | `TestFallbackClassifyWithoutNewCommitEmptyText`（`fallback_verdict_test.go:106`） | `:121` |

缺的是**邻近分支不得写分类**的负例断言（一次「顺手统一分类」就会把「无 trailer 但有新提交」的 `NoTrailerResult` 送进自动续接，重复执行已落地工作）：

- `NoTrailerResult`（`internal/executor/turn/fallback.go:68`）无 `FailureClass` 断言。
- codex/grok/claudecode/agy 的「有新提交无 trailer」结果分支（`TestNoTrailerWithNewCommitDoesNotDeclareCompletion` / `TestFallbackWithNewCommitDoesNotDeclareCompletion` / `TestFallbackClassifyWithNewCommit`）只断言 `!OK` 与 `VoidReason`，无 `FailureClass==""`。
- opencode 的 `TestIdleFallbackNoTrailer/with_new_commit`（`adapter_test.go:598`）与 `TestServeDeathEmitsFailed`（`:1037`）结果分支无 `FailureClass==""`。
- `failed` 终态（Stop/对账，`manager.go:1808` 用字面 `""` 构造）无「不得带 `failure_class`」断言。

### R3：`awaitNode` 只覆盖四条主路径，早退矩阵与日志断言缺

`internal/ledgerstep/b402_retry_test.go` 现有四例：`zero_text→completed`、`zero_text→zero_text`、无分类不续接、续接失败 fail-closed；`RunOnce` 现有：解析失败不归档、归档失败转人工。spec §9.2 还要求（**未覆盖**）：

- `zero_text → 普通 turn_failed`：第二终态非零文本时不得二次续接，且返回第二终态报文。
- `RunOnce` 早退矩阵：`Await` 返回错误 / review 只读 diff 失败或违规 / 写闸拒绝 / 产出物读取失败或缺产出物 / `PublishWorkBranch` 失败 / `routeTo` 失败——**每条** `FinishTask=0` 且 `Outcome=needs_human`（写闸拒绝例外，走 `Outcome{}, error`）。
- 日志断言：`awaitNode` 零文本路径日志含 `task`/`seq`/`failure_class`/续接结果，且不含完整正文/凭据。

### R4：无跨进程/序列化边界的回路，roadmap 无 B402 登记，图有扫描欠账

- 现有测试都在包内单测层；`FailureClass` 经 `Result → FailedPayload → JSON → HTTP/WS/attach → TurnEnd` 的**一条穿真序列化边界的回归**缺失（spec §7.2 / breakdown S3）。
- `docs/roadmap.md` 现状：`grep -n 'B402' docs/roadmap.md` → 无命中（exit 1，本节点亲跑）；spec §10 三条后续项未登记。
- `codegraph/diffs/cards-B402-charter.json` 为结构性最小增量：`NodeStep.RunOnce` 在视图内 `status=None`（函数体改了但未登记 modified），`Manager.handleResult`/`Manager.Continue`/`ZeroTextContinueInstruction` 未登记；新增测试文件不在图。见 §图覆盖债。

---

## 2. 分流决定

| 事项 | 归属 | 处置 |
|---|---|---|
| Ticket 0 生产代码（proto/executor/adapters/orchestration/ledgerstep） | 已冻结（contract） | **零改动**：T0 只复核在场且绿；执行者**不得**改任何 `internal/**` 生产代码。发现实际写错分类 ⇒ 停下提问（越契约冻结面）。 |
| 五家 adapter 邻近分支反例 + `turn.NoTrailerResult` + `failed` 终态无分类 | 本卡 / T1（测试面） | 加负例断言，正例保留。 |
| `awaitNode` 第二终态普通失败、`RunOnce` 早退矩阵、日志断言 | 本卡 / T2（测试面） | 扩展 `b402_retry_test.go`。 |
| 跨进程 e2e + wire 保真 | 本卡 / T3（测试面） | 新增 `internal/orchestration/b402_e2e_test.go`（外部测试包）。 |
| spec §10 三条后续项登记 | 本卡 / T4（文档面） | 只改 `docs/roadmap.md`。 |
| codegraph 视图对账 + 扫描欠账认账 | 本卡 / T5（图工具链） | 只碰 `codegraph/` 与台账。 |
| 真实供应商流中断 / 旧 agentd / 跨机续接 / 真实日志现场 | 协调者 | §11；**本 task 由协调者执行，不派发**。 |

---

## 3. 任务 DAG

```text
T0（门禁·零改动）：复核 Ticket 0 在场 + 基线四闸绿（build/vet/子集测试/codegraph）
   │  （不绿即停下提问，整卡前提失效）
   ├─> T1 = S1：五家 adapter 邻近反例 + turn.NoTrailerResult + orchestration failed 终态无分类
   ├─> T2 = S2：awaitNode 第二终态普通失败 + RunOnce 早退矩阵 + 日志断言
   ├─> T3 = S3：跨进程 e2e（zero_text→completed / zero_text→zero_text）+ wire 保真
   ├─> T4 = S4：roadmap 登记 spec §10 三条
   └─> T5 = S5：codegraph 逐符号对账 + 扫描欠账记台账（T1–T3 落测试后做）
         │
         └─> review / acceptance / finish
真机清单（真实供应商/跨机/旧 agentd/日志现场）──────────> 协调者（§11）
```

- **次序**：T0 是 T1–T5 的入场门。T1–T4 相互独立可并行；T5 在 T1–T3 之后（要对账新增测试节点与触碰符号）。
- **最薄路径条（免除声明）**：本卡要锁的运行时行为（零文本分类、一次续接、早退不归档）**今天从声明缝调用已得到断言的预期结果**——Ticket 0 已落地，`go test` 相关包本轮全绿（§4 R5），本计划新增的测试**写下去即绿**。故按「写下去就会绿的才免除」不设点亮行为的最薄路径 task；负面/边界断言靠**变异复验**证明有牙（`git diff` 还原）。
- **本 plan 不写生产代码**，所有 task 的红绿模板只作用在「锁缝断言的测试」上，且因实现已在场，实际形态是「写断言（绿）→ 变异（红）→ 还原」，变异复验即等价的红证。

---

## 4. 基线事实（实现节点共享；本节点亲跑，原始输出见台账）

工作树 `cards/B402-charter-6` @ `2fb6a9d7`，go1.26.1 linux/amd64。

| 命令 | 本节点读数 |
|---|---|
| `go build ./...` | `BUILD_EXIT=0` |
| `go vet ./...` | `VET_EXIT=0` |
| `go test ./internal/ledgerstep/ ./internal/proto/ ./internal/executor/... -count=1` | `SUBSET_EXIT=0`（ledgerstep 14.932s、proto 0.030s、executor 全集含 fake/rawtap/turn 全绿） |
| `codegraph --repo . check` | `CHECK_EXIT=0`（`viewContainers=338`、`crossDomainEdges=1254`、`misplacedSkipped=0`） |
| `codegraph --repo . --view cards-B402-charter check` | `VIEW_CHECK_EXIT=0` |
| `codegraph --repo . validate` | `VALIDATE_EXIT=1`；`issues` 恰为既存两条：`[cards-B272-charter] 新增容器 k_dropdir_fn 引用不存在的基线领域 d_coordination_api`、`[cards-B374-charter] 新增容器 k_collab_model 已存在于基线，containersAdded 只接受新容器`（**非本卡引入，不修**） |
| `grep -n 'B402' docs/roadmap.md` | 无命中，`GREP_EXIT=1` |
| `codegraph --view cards-B402-charter sym FailureClass / TurnEnd / failureClassOf / awaitNode` | 全命中：`m_proto_FailureClass`(added)、`m_ledgerstep_TurnEnd`(added)、`n_ledgerstep_failureClassOf`(added)、`n_ledgerstep_StepRunner_awaitNode`(modified)，`anchor=ok` |
| `codegraph --view cards-B402-charter sym RunOnce` | 命中 `n_ledgerstep_NodeStep_RunOnce`，`anchor=ok`，`status=None`（**触碰但未登记 modified——扫描欠账**） |
| `codegraph --view cards-B402-charter sym ZeroTextContinueInstruction` | **不在图中**（`近似候选: []`）→ 记入图覆盖债 |
| `go test ./internal/orchestration/ -count=1` | 本节点未单独复跑（contract 台账记 78s 全绿）；T0 必须复跑（见 T0）。 |

**已知既有红（非本卡引入，不修）**：`codegraph validate` 的两条他卡视图问题；开工前既存未 `gofmt` 文件 `internal/agentd/cardstep.go`、`internal/ledger/types.go`（非本卡触碰）。

**关键代码事实（写断言要用）**：

- `b402Client` 分阶段替身：`internal/ledgerstep/b402_retry_test.go:21`（`waits []*proto.Event`、`waitIdx`、`attachEvents`、`continues`、`lastInstr`、`continueErr`；`WaitEvent` 按序吐、`Attach` 返回固定快照、`Continue` 计数）。
- 事件构造助手：同文件 `zeroTextEvent()`（`:60`，payload `{"fail_reason":"与实现无关的文案","failure_class":"zero_text"}`）、`completedEvent(text)`（`:65`）。
- 节点夹具：`nodeLedger(t)`（`node_test.go:14`）返回 `(*ledger.Store, ledger.Card)`；`nodePassMessage()`（`node_test.go:658`）返回带 `handoff-verdict` 的 pass 报文。
- `NodeStep` 字段：`St`/`Node`/`Dispatch`/`Await`/`OutputPath`/`Diff`/`Attach`/`WriteGate func() bool`/`PublishWorkBranch`/`FinishTask`（`node.go:46-67`）。
- e2e 环境：`newHTTPEnv(t)` + `doReq` + `initRepo` + `seedTask` 都在 `internal/orchestration/gateway_http_test.go`（`package orchestration_test`），新文件同包可直接复用；`fake.New(script)` 支持 `Step{Finish: executor.Result{...}}` 与 `Add(taskID, steps...)` + `Send` 续接门禁（`internal/executor/fake/fake.go:35,120,164`）。
- HTTP 路由：`POST /api/tasks/{id}/continue`（`server.go:728`，`handleContinue` 要求 `waiting_review` 且 instructions 非空）、`POST /api/tasks/{id}/done`（`:729`）、`GET /api/tasks/{id}`（`:726`，attach 数据源含 `RecentEvents`）。**无** `/api/tasks/{id}/events` 路由。

---

## 5. 接口契约

### Consumes（只读既有生产符号，精确签名）

| 消费物 | 精确签名/路径 | 用途 |
|---|---|---|
| `proto.FailureClass` | `type FailureClass string`（`internal/proto/failure.go:15`） | 断言分类零值/精确值 |
| `proto.FailureClassZeroText` | `const FailureClassZeroText FailureClass = "zero_text"`（`:23`） | 断言精确等值 |
| `executor.Result.FailureClass` | 字段 `FailureClass proto.FailureClass`（`internal/executor/executor.go:96`） | adapter/透传断言 |
| `turn.NoTrailerResult` | `func NoTrailerResult(sessionID, branch, commit, text string) *executor.Result`（`internal/executor/turn/fallback.go:68`） | 负例断言 |
| `orchestration.FailedPayload` | 字段 `FailureClass proto.FailureClass \`json:"failure_class,omitempty"\``（`contracts.go:142`） | JSON roundtrip |
| `orchestration.Manager.handleResult` | 包内 `func (m *Manager) handleResult(taskID string, ev executor.AdapterEvent)`（`manager.go:3414`） | failed 终态无分类夹具 |
| `ledgerstep.TurnEnd` | `struct{ EventType proto.EventType; Seq int64; FailureClass proto.FailureClass }`（`wire.go:41`） | 等待层断言 |
| `ledgerstep.waitForTurnEnd` | `func waitForTurnEnd(ctx, wait func(context.Context)(*proto.Event,error)) (TurnEnd,error)`（`wire.go:88`） | 缝级断言 |
| `ledgerstep.StepRunner.awaitNode` | `func (r *StepRunner) awaitNode() func(context.Context,string,string)(string,error)`（`runner.go:426`） | 续接编排断言 |
| `ledgerstep.ZeroTextContinueInstruction` | `const ... = "上一回合以零文本结束…"`（`runner.go:416`） | 指令固定非空断言 |
| `ledgerstep.NodeStep.RunOnce` | `func (n *NodeStep) RunOnce(ctx, cardID string) (Outcome,error)`（`node.go:187`） | 生命周期断言 |
| `agentd.NewServer`/`SetManager`/`Handler` | `gateway_http_test.go:64-65,87` | e2e HTTP 面 |
| `fake.New`/`Fake.Add`/`Fake.Sends` | `internal/executor/fake/fake.go:107,120,220` | e2e 脚本/续接门禁 |

### Produces（本卡新增/改动的仓内文件）

```text
docs/superpowers/plans/b402-plan.md                              （本节点，已落）
docs/superpowers/ledgers/2026-09-25-b402-plan-ledger.md           （本节点台账）
（实现节点产出：）
internal/executor/turn/fallback_test.go            +1 断言（NoTrailerResult 无分类）        T1
internal/executor/codex/fallback_verdict_test.go   +1 断言（有新提交无 trailer 无分类）    T1
internal/executor/grok/fallback_verdict_test.go    +1 断言                                  T1
internal/executor/claudecode/fallback_verdict_test.go +1 断言                              T1
internal/executor/agy/fallback_verdict_test.go     +1 断言                                  T1
internal/executor/opencode/adapter_test.go         +2 断言（fallback/with_new_commit、serve death） T1
internal/orchestration/handleresult_notrailer_test.go +1 用例（failed 终态无分类）         T1
internal/ledgerstep/b402_retry_test.go             +3 用例（第二终态普通失败/早退矩阵/日志） T2
internal/orchestration/b402_e2e_test.go            新增（外部测试包 e2e + wire 保真）       T3
docs/roadmap.md                                    +1 小节（来自 B402 spec 三条）           T4
codegraph/diffs/cards-B402-charter.json            视对账结果补登记（或记欠账）             T5
docs/superpowers/ledgers/2026-09-25-b402-plan-ledger.md  追加 T5 对账读数                 T5
docs/superpowers/ledgers/2026-09-25-b402-implement-ledger.md （实现轮台账，命名随实现节点）
```

**无新增生产 Go 符号、无新字段、无 DTO/wire/tag 变化、无新命令、无新 HTTP 端点。**

---

## 6. 任务详情

### T0 门禁：复核 Ticket 0 在场 + 基线绿（零改动）

**动作**：逐条跑下表命令，把原始输出抄进实现轮台账。

| # | 命令 | 预期 |
|---|---|---|
| 1 | `go build ./...` | `BUILD_EXIT=0` |
| 2 | `go vet ./...` | `VET_EXIT=0` |
| 3 | `go test ./internal/ledgerstep/ ./internal/proto/ ./internal/executor/... ./internal/orchestration/ -count=1` | `TEST_EXIT=0`（全绿） |
| 4 | `codegraph --repo . check` | `CHECK_EXIT=0` |
| 5 | `codegraph --repo . --view cards-B402-charter check` | `VIEW_CHECK_EXIT=0` |
| 6 | `codegraph --repo . validate` | `VALIDATE_EXIT=1` 且 `issues` **恰为** §4 两条既存问题，不得新增 |
| 7 | `grep -n "FailureClass" internal/proto/failure.go internal/executor/executor.go internal/orchestration/contracts.go internal/orchestration/manager.go internal/ledgerstep/wire.go internal/ledgerstep/runner.go` | 命中非空，且与 R1 行号表一致 |
| 8 | `grep -n "FailureClass: proto.FailureClassZeroText" internal/executor/opencode/adapter.go internal/executor/codex/adapter.go internal/executor/grok/adapter.go internal/executor/claudecode/adapter.go internal/executor/agy/adapter.go` | 恰 5 处命中，行号 `:2232/:912/:679/:893/:691` |
| 9 | `grep -n "defer.*FinishTask" internal/ledgerstep/node.go` | **无命中**（无条件 defer 已删；`node.go:486` 是显式调用） |

**失败处置**：任一条与预期不符，或冻结实现片段缺失 ⇒ **停下提问，不自行补实现**（P1=甲，代码已冻结，缺失是上游事故）。请求格式：单行 JSON `{"ask":"..."}`。

- **测试范围声明**：只跑上表 9 条；全量 `go test ./...` 不在本 task（属卡级集成）。
- **关键节点日志 / 注释步骤**：**不适用**——T0 零代码改动（无可观测性面）。豁免理由：它不改任何文件。
- **红绿周期**：不适用，见 §3 最薄路径条免除声明。

### T1 = S1：零文本分类的邻近分支反例 + `NoTrailerResult` 与 `failed` 终态无分类

**目标**：把「只有明确零文本分支写 `zero_text`，邻近分支不得写」这条承重安全属性从「实现里恰好为真」变成**能变红的测试**。

**有界文件集**：上表 T1 七条，不得扩展。

#### 改动一：`internal/executor/turn/fallback_test.go`（新增一个用例）

追加到文件末尾（`package turn`）：

```go
// TestNoTrailerResultCarriesNoFailureClass 锁 B402：无 trailer 但有新提交不得
// 分类为 zero_text——那不是「流中断」，是「已有工作等协调者裁决」，自动续接会
// 重复执行已落地修改（contract §2.3 / spec §4.1）。
func TestNoTrailerResultCarriesNoFailureClass(t *testing.T) {
	r := NoTrailerResult("sess-1", "handoff/T1", "abc1234def", "干完了，已提交。")
	if r.FailureClass != "" {
		t.Fatalf("NoTrailerResult 不得带失败分类，FailureClass=%q", r.FailureClass)
	}
}
```

#### 改动二~五：四家 adapter 的「有新提交无 trailer」结果分支各加一句断言

在各文件既有用例的结果断言之后追加（**生产代码零改动**）：

`internal/executor/codex/fallback_verdict_test.go` 的 `TestNoTrailerWithNewCommitDoesNotDeclareCompletion`（`:97`）在 `:118-120` 的 `VoidReason` 断言后：

```go
	if ev.Result.FailureClass != "" {
		t.Fatalf("无 trailer 有新提交不得分类（避免重复执行），FailureClass=%q", ev.Result.FailureClass)
	}
```

`internal/executor/grok/fallback_verdict_test.go` 的同名用例（`:107`）、`internal/executor/claudecode/fallback_verdict_test.go` 的 `TestFallbackWithNewCommitDoesNotDeclareCompletion`（`:114`）、`internal/executor/agy/fallback_verdict_test.go` 的 `TestFallbackClassifyWithNewCommit`（`:68`）：各在「结果分支已断言 `!OK`」之后，加同形断言（变量名随该文件既有命名，如 `ev.Result.FailureClass != ""`）。

> **不变量分支说明**：`question` 分支（`TestNoTrailerWithoutNewCommitStillAsks` / `TestFallbackWithoutNewCommitStillAsks` / `TestFallbackClassifyWithoutNewCommitWithText`）产出的是 `question` 事件，事件无 `executor.Result`（`ev.Result == nil`），结构上不可能带分类；这些用例已断言 `ev.Type == "question"`，本 task 不另加断言（加了也只是重复 `Result==nil`）。**这不是漏项，是分类只存在于 result 事件**（`executor.AdapterEvent.Result *Result`）。

#### 改动六：`internal/executor/opencode/adapter_test.go`（两处）

1. `TestIdleFallbackNoTrailer` 的 `with_new_commit` 子测试（`:598`）在既有 `FailReason` 断言后加：

```go
		if ev.Result.FailureClass != "" {
			t.Fatalf("无 trailer 有新提交不得分类（避免重复执行），FailureClass=%q", ev.Result.FailureClass)
		}
```

2. `TestServeDeathEmitsFailed`（`:1037`）在既有 `FailReason` 断言后加：

```go
	if ev.Result.FailureClass != "" {
		t.Fatalf("serve 死亡是进程退出，不是零文本流中断，不得分类，FailureClass=%q", ev.Result.FailureClass)
	}
```

#### 改动七：`internal/orchestration/handleresult_notrailer_test.go`（新增一个用例）

`handleResult` 无分类负例已由 `TestFailedPayloadCarriesFailureClassAdditively`（`:106`，legacy 分支断言 payload 不含 `failure_class`）覆盖；本改动补 **`failed` 终态**（Stop/对账路径）无分类。文件已 `package orchestration`，追加：

```go
// TestFailedEventTerminalCarriesNoFailureClass 锁 B402：failed 终态（Stop /
// 对账，manager.go 用字面 "" 构造）不得带 failure_class——只有 turn_failed 的
// 明确零文本分支才可自动续接（contract §3-10 / §4）。
func TestFailedEventTerminalCarriesNoFailureClass(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustTaskWithTicket(t, st, "t-failed-fc", proto.TaskStateRunning)
	if _, err := m.Stop(context.Background(), "t-failed-fc"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	ev := lastEventOfType(t, m, "t-failed-fc", string(proto.EventTypeFailed))
	if strings.Contains(string(ev.Payload), "failure_class") {
		t.Fatalf("failed 终态不得带 failure_class: %s", ev.Payload)
	}
}
```

需在 import 块补 `"context"`（现有 `encoding/json`/`strings`/`testing`/`executor`/`proto` 保留）。

> **若 `m.Stop` 因任务无运行态而报错**：改用既有 Stop 用例夹具（`grep -n "func Test.*Stop" internal/orchestration/*_test.go`）造出可 Stop 的运行态；**不得**转而删掉该断言。此退路不改变测试入口符号（仍是 `m.Stop`），不触发内部锁闸。

- **Interfaces**：Consumes 见表（`Result.FailureClass`、`NoTrailerResult`、`FailedPayload.failure_class`、`Manager.Stop`）；Produces = 上表 T1 七文件的断言。
- **步骤**（每步 2~5 分钟）：
  1. 逐字落改动一（`turn/fallback_test.go`）。
  2. 逐字落改动二~五（四家 adapter 各一句）。
  3. 逐字落改动六（opencode 两处）。
  4. 逐字落改动七（orchestration 一例）。
  5. `gofmt -l` 确认新增行无格式问题；跑下面测试范围。
  6. **变异复验**（见下），跑后立即还原。
- **测试范围声明**：`go test ./internal/executor/turn/ ./internal/executor/codex/ ./internal/executor/grok/ ./internal/executor/claudecode/ ./internal/executor/agy/ ./internal/executor/opencode/ ./internal/orchestration/ -count=1`，退出 0。只跑触及包；全量不属本 task。
- **关键节点日志 / 注释**：本 task 不新增生产可观测性面；新用例均带职责注释（上面代码块已含）。
- **变异复验（等价红证，证明断言有牙）**：
  - 把 codex `adapter.go:912` 的零文本分类临时改成 `FailureClass: proto.FailureClassZeroText` **放到**「有新提交」分支（或直接给 `NoTrailerResult` 加 `FailureClass: proto.FailureClassZeroText`）→ 改动二中该断言必须变红。
  - 删掉某家零文本分支的 `FailureClass` 赋值 → 该家正例必须变红（回归护栏仍在）。
  - 把 `turn.NoTrailerResult` 加分类 → 改动一变红。
  - 跑后 `git diff --stat` 复核只剩预期测试改动。

### T2 = S2：`awaitNode` 第二终态普通失败 + `RunOnce` 早退矩阵 + 日志断言

**目标**：补齐「任何早退都不归档」这条生命周期修复的完整边界，并锁住零文本路径的可观测性。

**有界文件集**：`internal/ledgerstep/b402_retry_test.go`（主），必要时 `internal/ledgerstep/node_test.go`（复用 `nodeLedger`/`nodePassMessage`，不新增 helper）。

**import 增补**：`bytes`、`log/slog`、`strings`（现有 `context`/`encoding/json`/`errors`/`testing`/`time`/`client`/`ledger`/`proto` 保留）。

#### 改动一：`awaitNode` 第二终态为普通 `turn_failed`（不二次续接，返回第二终态报文）

```go
// TestAwaitNodeSecondTerminalOrdinaryFailDoesNotContinue 锁 B402 §3-14 的近邻：
// 首个 zero_text 触发一次续接后，第二终态即使是普通 turn_failed 也不再续接；
// 第二终态的报文才是裁决输入（此处为 fail_reason）。
func TestAwaitNodeSecondTerminalOrdinaryFailDoesNotContinue(t *testing.T) {
	ordinary := &proto.Event{Type: proto.EventTypeTurnFailed,
		Payload: json.RawMessage(`{"fail_reason":"第二次是普通失败，与实现无关"}`)}
	c := &b402Client{
		waits:        []*proto.Event{zeroTextEvent(), ordinary},
		attachEvents: []proto.Event{*ordinary},
	}
	msg, err := newB402Runner(c).awaitNode()(context.Background(), "mac-02", "t")
	if err != nil {
		t.Fatalf("awaitNode: %v", err)
	}
	if c.continues != 1 {
		t.Fatalf("只对首个 zero_text 续接一次，实际 %d", c.continues)
	}
	if c.waitIdx != 2 {
		t.Fatalf("应恰好等两个终态，wait=%d", c.waitIdx)
	}
	if !strings.Contains(msg, "第二次是普通失败") {
		t.Fatalf("应返回第二终态的报文，实得 %q", msg)
	}
}
```

#### 改动二：`RunOnce` 早退矩阵（每条 `FinishTask=0`）

**断言逐条列全**（每条可独立判 pass/fail）。共用 `nodeLedger(t)` 与 `nodePassMessage()`（`node_test.go:14/:658`），构造 `NodeStep` 照抄 `b402_retry_test.go:153-183` 与 `node_test.go:26-44` 的既有形态（`St`/`Node{Dispatch:true,Verdict:true,Template:"feature-impl",Next:ledger.StatusReview,OnFail:ledger.StatusDoing}`/`Dispatch`/`Await`/`FinishTask` 计数）。表驱动，每行 `configure func(n *NodeStep)`，统一在 `RunOnce` 后断言：

| 行 | 路径 | `configure` 关键字段 | 断言 |
|---|---|---|---|
| 1 | `Await` 返回错误 | `Await` 返回 `errors.New("等待失败")` | `Action==ActionNeedsHuman`、`Reason=="未取到裁决报文"`、`FinishTask` 调用 0 次 |
| 2 | 裁决解析失败 | `Await` 返回 `"没有 handoff-verdict 的报文"` | `Action==ActionNeedsHuman`、`Reason=="裁决解析失败"`、0 次（现有 `TestNodeStepDoesNotFinishTaskOnParseFailure` 已覆盖，矩阵内可作对照行） |
| 3 | review 只读 diff 失败 | `Node.Override.Purpose=ledger.PurposeReview`、`Diff` 返回 `nil, errors.New("diff 失败")`、`Await` 返回 `nodePassMessage()` | `Action==ActionNeedsHuman`、`Reason=="读取审阅改动失败"`、0 次 |
| 4 | 产出物 diff 失败 | `Node.Produces=&ledger.Output{Kind:"plan",Path:"docs/x.md"}`、`OutputPath` 返回 `"docs/x.md"`、`Diff` 返回 `nil, errors.New("diff 失败")` | `Action==ActionNeedsHuman`、`Reason=="读取产出物改动失败"`、0 次 |
| 5 | 缺约定产出物 | 同 4 但 `Diff` 返回 `[]string{"docs/other.md"}, nil` | `Action==ActionNeedsHuman`、`Reason=="缺少约定产出物"`、0 次 |
| 6 | 写闸拒绝 | `Node.Override.Purpose=ledger.PurposeReview`（确保首个 `gatedWrite` 在 diff 前）或普通 implement；`WriteGate: func() bool { return false }` | **`err != nil`**（`RunOnce` 返回 `Outcome{}, err`，错误含 `ErrWriteGateClosed`）、`FinishTask` 0 次 |
| 7 | `PublishWorkBranch` 失败 | `RecordDispatch(card.ID, ledger.DispatchSnapshot{Template:"feature-impl",Target:"mac-02",TaskID:"task-b402",Branch:"cards/B402",Purpose:"implement",Actor:"t"})`；`PublishWorkBranch` 返回 `errors.New("push 失败")`；`Await` 返回 `nodePassMessage()` | `Action==ActionNeedsHuman`、`Reason=="工作分支未能推到 origin"`、0 次 |
| 8 | `routeTo` 失败 | `Node.Next="不存在的列"`、`Await` 返回 `nodePassMessage()` | `Action==ActionNeedsHuman`、`Reason` 含 `移到`、0 次 |
| 9（对照）| pass 路径 | 全依赖就绪，`PublishWorkBranch=nil`、`Next=ledger.StatusReview` | `Action==ActionPass`、`FinishTask` 恰 1 次（证明矩阵不是恒 0 的假绿） |

> 行 7 需要工作分支已登记：照抄 `node_test.go:665-673` 的 `RecordDispatch` 调用。行 3 的 review purpose 与 `WriteGate` 组合参照 `newReviewReadOnlyStep`（`node_test.go:26`）。**若某行夹具在实现时发现要改生产代码才能构造**，停下提问（越契约面）。

**该矩阵为「断言逐条列全 + 照抄既有 harness（`b402_retry_test.go:153-183` 的 NodeStep 装配、`node_test.go` 的 `nodeLedger`/`nodePassMessage`）」，属 §12 声明的 harness 复用例外。**

#### 改动三：日志断言

```go
// TestAwaitNodeZeroTextLogsContext 锁 B402 §9.2 末条：零文本自动续接路径的
// 日志必须能看见 task / seq / failure_class / 续接结果，且不得泄露完整正文。
func TestAwaitNodeZeroTextLogsContext(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	const secret = "SUPERSECRET-BODY-must-not-be-logged"
	final := "```handoff-verdict\n{\"verdict\":\"pass\"}\n```" + secret
	first := zeroTextEvent()
	first.Seq = 7
	c := &b402Client{
		waits:        []*proto.Event{first, completedEvent(final)},
		attachEvents: []proto.Event{*completedEvent(final)},
	}
	if _, err := newB402Runner(c).awaitNode()(context.Background(), "mac-02", "t"); err != nil {
		t.Fatalf("awaitNode: %v", err)
	}
	logs := buf.String()
	for _, want := range []string{"task=t", "seq=7", "failure_class=zero_text"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("日志缺 %q:\n%s", want, logs)
		}
	}
	if strings.Contains(logs, secret) {
		t.Fatalf("日志不得含完整正文:\n%s", logs)
	}
}
```

（`failure_class=zero_text` 来自 `runner.go:463-465` 续接后终态日志；`seq=7` 来自 `:451`；`task=t` 来自 `:428` 的 `With`。断言的是**调用方依赖的字段类别**，不绑私有写法。）

- **Interfaces**：Consumes 见 §5；Produces = `b402_retry_test.go` 三新用例。
- **步骤**：
  1. 落改动一 → `go test ./internal/ledgerstep/ -run TestAwaitNodeSecondTerminalOrdinaryFailDoesNotContinue -count=1`（预期 ok）。
  2. 落改动二 → 逐行跑 `-run TestNodeStepEarlyExitsNeverFinishTask`（预期 ok）。
  3. 落改动三 → `go test ./internal/ledgerstep/ -run TestAwaitNodeZeroTextLogsContext -count=1`（预期 ok）。
  4. **变异复验**：
     - 把 `node.go:486` 的显式 `FinishTask` 改回「`Await` 后无条件 defer」→ 矩阵至少一行 0 次断言变红。
     - 删 `runner.go:463-465` 的 `failure_class` 日志键 → 改动三变红。
     - `grep -n "defer.*FinishTask"` 复核还原后无命中。
  5. `go test ./internal/ledgerstep/ -count=1` 与 `go test -race ./internal/ledgerstep/ -count=1` 退出 0。
- **测试范围声明**：只跑 `./internal/ledgerstep/`（含 `-race`）；不跑全量。
- **关键节点日志 / 注释**：本 task 的「关键节点日志」交付物即改动三的**断言**（生产日志已在 Ticket 0 写好，不新增生产日志）；新测试函数带职责注释。

### T3 = S3：跨进程端到端回路 + wire 保真

**目标**：补一条穿过真实 HTTP/JSON 序列化边界的回归，证明 `failure_class` 从 `handleResult` 写入到 `TurnEnd` 判定不丢；并锁 `/continue` 状态门。

**有界文件集**：新增 `internal/orchestration/b402_e2e_test.go`（`package orchestration_test`）；如需扩展，`internal/agentd/server_test.go`（仅当 `gateway_http_test.go` harness 不足）。**不得改生产**。

**harness（照抄，不重写）**：`internal/orchestration/gateway_http_test.go` 已提供本包可直接调用的
`newHTTPEnv(t)`（`:46`，真 store + 真 Hub + 真 Manager + 真 agentd.Server）、`doReq`（`:70`，请求进真 Handler，含回环 Host）、`initRepo`（`:207`）、`seedTask`（`:231`）。脚本 adapter 用 `internal/executor/fake`：`fake.New([]fake.Step{{Finish: executor.Result{...}}})`、`fake.Add(taskID, step)`、`fake.Sends()`（`fake.go:35,120,220`）。

**断言逐条列全**（每条可独立判 pass/fail；全部穿真 `httptest`/HTTP/JSON 边界，**不是**包内 `json.Marshal`）：

1. **主回路 `zero_text→completed`**：新环境注册 `fake` 脚本首步 `Finish: executor.Result{OK:false, FailureClass: proto.FailureClassZeroText, FailReason:"与实现无关的文案"}`；经 `mgr.Dispatch` 起一个任务（先 `mgr.RegisterProject`，照抄 `manager_test.go:276` 的 origin 派生法，`RegisterProject` 是导出方法）。轮询 `GET /api/tasks/{id}`（attach 的 `RecentEvents`）直到出现 `turn_failed`；断言其 JSON payload **精确含** `"failure_class":"zero_text"`。
2. **状态门**：此时任务 `waiting_review`，`POST /api/tasks/{id}/continue` body `{"instructions":"继续"}` → **200**；对另一个 `running` 任务同请求 → **409**（锁 `Manager.Continue` 的 `waiting_review` 门，spec §7.2）。
3. `fake.Add(taskID, fake.Step{Finish: executor.Result{OK:true, Summary:"完成", FinalText:"```handoff-verdict\n{\"verdict\":\"pass\",\"findings\":[]}\n```"}})` 后轮询到 `completed`；断言 `completed` payload 的 `final_text` 含 `handoff-verdict`（判据可解析）。
4. `POST /api/tasks/{id}/done` body `{"note":""}` → **200**；断言任务终态非 `waiting_review`（归档成功）。
5. **主反例 `zero_text→zero_text`**：同 1 起任务，首个 `zero_text` 后 `POST /continue` 一次成功；`fake.Add` 第二步仍 `Finish: {OK:false, FailureClass: zero_text}`；轮询出现**第二条** `turn_failed`；断言任务仍非归档、且此后再 `POST /continue` 不得因「已归档」得到 404/409（`Manager.Continue` 状态门仍放行或给出明确状态错误，不得是「task 已归档」）。
6. **wire 保真 roundtrip**：对 `Result{OK:false, FailureClass:zero_text}` 的任务，从 HTTP 取回的事件 JSON 解出 `failure_class=="zero_text"`；对 `Result{OK:false, FailureClass:""}` 的任务，事件 JSON **不含** `failure_class` 键（区分「字段缺失」与「值为零」）。空分类键缺席可由 `mgr.Dispatch` 的第二任务或既有 `TestFailedPayloadCarriesFailureClassAdditively` 的包内断言共同证明——e2e 必须至少覆盖「有字段穿真边界」。
7. **夹具脱敏**：第一次失败的 `FailReason` 用与实现无关文案（如「供应商文案随便写」），证明分支只依赖结构化字段。
8. **续接计数**：`fake.Sends()` 中与本次 `/continue` 对应的指令恰 1 条、非空（HTTP 层传的是测试给定的 `"继续"`；`ZeroTextContinueInstruction` 的固定文本由 T2 单测锁）。

**变异复验**（跑后还原）：
- 把 `internal/ledgerstep/wire.go#failureClassOf` 改成恒返回 `""` → 主回路断言 1 变红。
- 把 `Manager.Continue` 的状态门从 `waiting_review` 放宽 → 断言 2 的 `running→409` 变红。

**该 task 为「断言逐条列全 + 照抄既有 harness（`gateway_http_test.go` 的 `newHTTPEnv`/`doReq`/`initRepo` + `manager_test.go:276` 的 `RegisterProject` 法 + `fake.go` 脚本/Add/Send 门禁）」，属 §12 声明的 harness 复用例外。**

- **Interfaces**：Consumes 见 §5；Produces = `internal/orchestration/b402_e2e_test.go`。
- **步骤**：1 建 env+脚本；2 注册项目+Dispatch+等 `turn_failed`；3 断言 wire；4 `/continue` 与状态门；5 Add+等 `completed`+`/done`；6 反例回路；7 变异复验；8 `git diff --stat` 复核。
- **测试范围声明**：`go test ./internal/orchestration/ -run TestB402E2E -count=1` + `go test -race ./internal/orchestration/ -run TestB402E2E -count=1`；全量不属本 task。
- **关键节点日志 / 注释**：测试文件头写「职责 + 只在外部测试包可挂真 Server（B233.26 缘由，见 `gateway_http_test.go:4-8`）」。不新增生产日志。
- **未验证声明**：真实供应商流中断、真实跨机 relay/直连续接、旧 agentd 二进制 → 需真机（§11）。

### T4 = S4：`docs/roadmap.md` 登记 spec §10 三条后续项

**目标**：spec §10「本期不做、后续要做」三条硬性要求落 `docs/roadmap.md`（现状无命中）。

**有界文件集**：`docs/roadmap.md`（仅追加一节到文件末尾）。

**动作**：在文件末尾既有最后一节之后另起：

```markdown
## 来自 B402 spec（2026-09-25，本期不做、后续要做）

- **首个零文本事件的唤醒抑制与 retry ownership 全局可见性**：B402 不抑制首个 `turn_failed(zero_text)` 的 Publish/唤醒，`wait --follow` 仍可能看到；安全性靠「任何路径不早归档」与 `waiting_review` 状态门，而非隐藏事件。来源：`docs/superpowers/specs/b402.md` §10；`b402-contract.md` §3。
- **跨 agentd 重启的同一 task 自动续接计数与恢复**：B402 的一次续接不做持久计数，进程重启不重放已消费的失败事件；恢复语义需单独冻结持久化/恢复契约。来源：`docs/superpowers/specs/b402.md` §4.2/§10。
- **供应商流中断本身的根因治理**：B402 只处理已确认的零文本分类、一次续接与生命周期不早归档，不修供应商断流根因。来源：`docs/superpowers/specs/b402.md` §10。
```

**判据**：`grep -n 'B402' docs/roadmap.md` 命中；段内三条分别含关键词 `唤醒抑制`/`retry ownership`、`跨 agentd 重启`/`自动续接计数`、`供应商流中断`。不得借本 task 改 `internal/**`。

- **步骤**：1 追加小节；2 跑判据 grep；3 确认工作树只剩 `docs/roadmap.md` 改动。
- **测试范围声明**：无测试代码；判据是上述 grep（实现轮落 `$TMPDIR` 脚本或直接命令行，不入仓）。
- **关键节点日志 / 注释**：**不适用**（纯 Markdown，无运行时与代码）；豁免见表 §12。
- **红绿**：无代码红锚；判据是文本在场，无假绿温床。

### T5 = S5：codegraph 逐符号对账 + 扫描欠账显式认账

**目标**：把 contract §8 的「最小结构增量、非全量重扫」欠账显式对账，避免「自己冻结的符号自己欠图覆盖」被静默带走。**不做**全量重扫/absorb（P3=甲）。

**有界文件集**：`codegraph/diffs/cards-B402-charter.json`（必要时补登记）、对应台账（追加读数）。**不碰** `codegraph/target.json`/`best.json` 的语义（除非对账发现本卡新符号确实缺登记，且只增 modified 条目）。

**判据**：

1. `codegraph --repo . check` exit 0；`codegraph --repo . --view cards-B402-charter check` exit 0（fails=[]）。
2. `codegraph --repo . validate` 仍 exit 1，`issues` **恰为** §4 的 B272/B374 两条，**不得新增**本卡 issue。
3. 逐符号 `codegraph --repo . --view cards-B402-charter sym <符号>` 实测并把读数写台账：
   - 已入视图（应命中 added/modified、`anchor=ok`）：`FailureClass`、`TurnEnd`、`failureClassOf`、`awaitNode`。
   - 本节点实测 `status=None`（沿用基线定义、未登记 modified）的触碰符号：`NodeStep.RunOnce`、`Manager.handleResult`、`Manager.Continue`。
   - 图外符号：`ZeroTextContinueInstruction`（本节点 `sym` 返回「不在图中」）。
4. 对第 3 条中「本卡触碰但未登记」的符号，二选一并在台账写明：**(甲)** 按 `docs/codegraph-scan-recipe.md` 把它们补进 `cards-B402-charter.json` 的 `nodesModified`（若配方要求全量扫描则不做，记欠账）；**(乙)** 保持最小增量，把「absorb 前需重扫」逐符号记入 `docs/superpowers/ledgers/2026-09-25-b402-plan-ledger.md` 与实现轮台账，**不以 grep 结果冒充图覆盖**。
5. `codegraph --repo . resolve --doc docs/superpowers/plans/b402-plan.md` 与 `--doc docs/superpowers/specs/b402-contract.md` 均 exit 0（坏锚即修）。

> P3=甲 倾向 **(乙)**：不把既存他卡图债搅进本卡。执行者若认为 (甲) 可安全完成且不扩围，可在台账说明后执行 (甲)；两者都以「`sym` 实测读数 + `validate` 不新增 issue」为准。

- **步骤**：1 跑三闸；2 逐符号 `sym`；3 落对账/欠账进台账；4 `resolve` 核锚。
- **测试范围声明**：无 Go 测试；判据是 codegraph 命令退出码与 `sym` 读数。
- **关键节点日志 / 注释**：不适用（静态产物）。
- **未验证声明**：真实供应商/跨机行为需真机（§11）。

---

## 7. 缺陷族对抗审查（逐族正面回答）

覆盖面 = Ticket 0 生产改动（已在场，T0 复核）+ T1–T5。逐族结论：

**族 1 生命周期/状态机中断**：T2 早退矩阵（9 行）与 T3 反例回路锁「任何早退不归档」；自动续接一次、无持久计数（进程重启不重放）已在 `awaitNode` 单测（T2 改动一）覆盖。残余：跨 agentd 重启的计数恢复是 spec §10 OOS，T4 登记 + §11 真机。

**族 2 静默失败/误导报错**：T2 日志断言锁 `task/seq/failure_class` 可见；`FinishTask` 失败转 `Reason="task 归档失败"` 已有用例；T1 负例防「把邻近失败静默当可重试」。无「报成功但没做」：`FinishTask` 只在收口末尾。

**族 3 跨平台假设**：T1/T2/T5 是纯类型/内存/静态产物；T3 HTTP/JSON 中立；真实平台/供应商差异归 §11。

**族 4 假红/假绿测试**：本 plan 每条新断言都配**变异复验**（T1/T2/T3 各有明确变异点）；T3 强制穿真 HTTP 边界而非包内 `Marshal`；夹具文案与 `FailReason` 解耦；T2 矩阵含 pass 对照行防「恒 0 假绿」。

**族 5 门禁绕过**：T3 断言 `/continue` 只在 `waiting_review` 放行（`running→409`）；无新门、无第二续接入口（唯一来源 `ExecutionClient.Continue`）。

**族 6 序列化边界**：T3 是本卡序列化边界主战场——`Result.FailureClass → FailedPayload → JSON → HTTP/attach → TurnEnd.FailureClass` 一条链路穿真边界，且用键缺席/精确保留区分「字段缺失」与「值为零」；T1 的 `turn`/`orchestration` 单测补两端。两端各自有测试 ≠ 链路有测试：T3 补链路。

**族 7 枚举新值过既有白名单**：T1 负例锁「邻近分支不得误登记 `zero_text`」；T2/T3 锁 `failureClassOf` 精确匹配与未知值 fail-closed。

**族 8 承重安全属性有测试锁住**：三条——①一次续接（T2 改动一 + 既有）；②无分类/未知值不重试（T1 反例 + T3 断言 6）；③任何早退不归档（T2 矩阵 + T3 反例）。每条有能变红的变异。

**族 9 webview / 平台表现差异**：无（不触 `d_web`/Wails/浏览器 API；T4 纯文档、T5 图工具链）。

---

## 8. 上下文预算检查

有界文件集（圈得出）：

- T1：7 个测试文件（5 家 adapter 的 `*_test.go` 之 4 + opencode `adapter_test.go` + `turn/fallback_test.go` + `handleresult_notrailer_test.go`）。
- T2：`internal/ledgerstep/b402_retry_test.go`（必要时 `node_test.go`）。
- T3：`internal/orchestration/b402_e2e_test.go`（新增）+ 只读参考 `gateway_http_test.go`/`manager_test.go`/`fake.go`。
- T4：`docs/roadmap.md`。
- T5：`codegraph/diffs/cards-B402-charter.json` + 台账。
- 本节点产出：`docs/superpowers/plans/b402-plan.md`、`docs/superpowers/ledgers/2026-09-25-b402-plan-ledger.md`。

不越出上述集合；**不触及 `internal/**` 生产代码**（T1–T3 只加测试）。**通过**。

---

## 9. 类型标注 / 边界型子系统

`d_execution_adapters`、`d_gateway`、`d_transport` 是边界型域：其**机内可验**部分是「契约形状」（分类字段、HTTP 状态码、JSON 键），由 T1/T3 锁住；**对面是外部现实**的部分（真实供应商流中断是否走该分支、跨机续接、旧 agentd 二进制）构造不出机内夹具，归 §11 真机清单，**未验证**。

---

## 10. 接缝覆盖（对照 spec §7.2 接缝清单）

spec §7.2 共 7 条缝，逐条映射（测试→缝 / 缝→测试 双向）：

| # | 缝（生产调用方） | 锁它的测试 | 入口符号是否在缝上 |
|---|---|---|---|
| 1 | 五家 adapter 零文本收尾分支 | 正例：各自 `*_test.go`（Ticket 0）；**负例：T1** | 是（adapter 事件循环/测试导出入口） |
| 2 | `Manager.handleResult` | `TestFailedPayloadCarriesFailureClassAdditively`（正）+ **T1 `TestFailedEventTerminalCarriesNoFailureClass`** + **T3 e2e** | 是 |
| 3 | `waitForTurnEnd` / typed terminal seam | `wire_test.go`（`TestWaitForTurnEndCarriesZeroTextClass` 等）+ T2 | 是 |
| 4 | `finalMessageFromEvents` | `wire_test.go` 既有 5 例（签名不变、completed 优先） | 是 |
| 5 | `NodeStep.RunOnce` | `b402_retry_test.go` 现有 + **T2 早退矩阵** + **T3 反例** | 是 |
| 6 | `client.ExecutionClient.Continue` | `awaitNode` 单测（`b402Client.Continue` 计数）+ **T3 HTTP `/continue`** | 是 |
| 7 | `FailedPayload` JSON 编解码边界 | `handleresult_notrailer_test.go`（包内）+ **T3 穿真 HTTP 边界** | 是 |

- **测试 → 缝**：每条新测试的入口符号都落上表某条缝（adapter 事件/`handleResult`/`awaitNode`/`RunOnce`/`Continue` HTTP），无「只在测试包内、无生产调用方」的 helper 占名额。
- **缝 → 测试**：7 条缝均有缝级断言；无锁不住的缝，无需修订缝清单。
- **内部锁**：无（T1–T3 的入口均在缝上）。
- **条件退路**：T1 改动七「若 `m.Stop` 报错改用既有 Stop 夹具」不改变入口符号（仍 `m.Stop`），不触发本闸；T5 的 (甲)/(乙) 是关于图登记范围的分流，非测试入口，已按 P3 声明。

---

## 11. 真机清单（归协调者执行；**本 task 由协调者执行，不派发**）

承 breakdown §6：

1. **真实供应商流中断**：真实 executor（runner/mimo、pro/cmd 等实录载体）回合零文本时，adapter 确实走明确零文本分支、节点自动续接一次并完成，不吵醒人。
2. **续接耗尽 / 外部竞争**：第二次仍零文本、续接失败或外部抢先续接（409）时，任务保持 `waiting_review`、`needs_human` 可见；人工显式 `continue` 不再 409。
3. **旧 agentd / 旧 adapter 缺分类**：跨版本灰度/回滚时，缺 `failure_class` 的历史/旧事件不自动重试（fail-closed），旧 wire 形状兼容。
4. **日志现场**：真实 agentd 日志能看到 `task/seq/failure_class/attempt/continue` 结果，且不含完整正文/凭据（S2 机内只锁本地 slog 契约）。
5. **首个事件仍唤醒观察者**：本卡 OOS 未抑制首个 `turn_failed` 的 Publish；`wait --follow` 仍可能看到首个零文本事件（确认未意外改变唤醒面）。

---

## 12. 占位符扫描

- **无 TBD /「加适当的错误处理」/「同 Task N 而略」**。T1/T2/T4 的代码/文本逐字可抄；T3 与 T2 矩阵为**声明式 harness 复用例外**（见下）。
- **harness 复用例外声明（2 处）**：
  1. **T2 早退矩阵**：9 行夹具各需不同的 `NodeStep` 装配与 `ledger` 状态，形态因失败路径而异；已列全每行断言（每条可判 pass/fail）并指认照抄的既有 harness（`b402_retry_test.go:153-183` 的 NodeStep 装配、`node_test.go:14` 的 `nodeLedger`、`:658` 的 `nodePassMessage`、`:665-673` 的 `RecordDispatch`）。
  2. **T3 e2e**：跨包真 Server/Manager 夹具形态复杂；已列全 8 条断言并指认照抄的既有 harness（`gateway_http_test.go` 的 `newHTTPEnv`/`doReq`/`initRepo`/`seedTask`、`manager_test.go:276` 的 `RegisterProject` 法、`fake.go` 的脚本/`Add`/`Send` 门禁）。
  两处均无「描述做什么却不给代码」的空骨架；未声明处一律给了完整代码。
- **红基线声明**：Ticket 0 已落地，新增测试**写下去即绿**；「有牙」由各 task 的**变异复验**证明（T1/T2/T3 均列明确变异点），等价红证。故 §3 免除「最薄路径条」。
- **实现类步骤缺项豁免声明**：
  - T0/T4/T5「关键节点日志 / 注释」不适用——零生产代码改动（T0 零改动、T4 纯 Markdown、T5 静态产物）。
  - T1/T2/T3「关键节点日志」不新增**生产**日志面：生产日志已在 Ticket 0 写好，T2 的交付物是对它的**断言**；新测试文件均带职责注释。
  - 无「内部锁」顶替缝级断言（§10 逐条在缝上）。
- **条件退路**：仅 T1 改动七的 Stop 夹具退路（不改入口符号）与 T5 的 (甲)/(乙)（图登记范围），均已在正文声明。

---

## 13. 自审三查

1. **spec 覆盖（逐条指到 task）**：
   - spec §3 目标 1（一次自动续接）→ T0 复核 + T2/T3；目标 2（第二次有效裁决归档）→ T3 主回路；目标 3（第二次失败/续接失败/解析失败不 Done）→ T2 矩阵 + T3 反例；目标 4（旧 wire 不重试）→ T1/T3 wire 断言；目标 5（`FailReason` 不参与分支）→ T1/T3 用例文案脱敏。
   - spec §4.1 类型化分类 → T1（negative）+ T3（链路）；§4.2 等待与重试边界 → T2；§4.3 生命周期 → T2；§4.4 并发所有权 → T3 状态门。
   - spec §6 用户故事 1/2/3/4 → 目标与日志断言（T2/T3）+ §11。
   - spec §7.2 七条缝 → §10 逐条归属。
   - spec §9.1 主红测/主反例 → T3；§9.2 必测矩阵 → T1/T2/T3 逐条；§9.3 验收顺序 → T0 + 各 task 测试范围。
   - spec §10 Out of Scope 三条 → **T4**；§12 图覆盖债 → T5；§11 缺陷族 → §7。
   - spec §12 图覆盖债「contract/breakdown 后须跑 `codegraph check` 并记未覆盖符号」→ T5。
2. **占位符扫描**：见 §12，无占位；2 处 harness 例外已逐条声明并指认照抄来源。
3. **跨 task 类型/签名一致性**：本 plan 无跨 task 的 Go 签名传递（T1–T3 各自用既有导出/包内符号）。逐字核对引用签名：`TurnEnd{EventType,Seq,FailureClass}`、`awaitNode() func(context.Context,string,string)(string,error)`、`RunOnce(ctx,cardID)(Outcome,error)`、`NoTrailerResult(sessionID,branch,commit,text)*executor.Result`、`FailedPayload.FailureClass`、`fake.Step{Finish executor.Result}` 与 §5 一致；T1 七文件与 T2/T3 无契约冲突。

---

## 图覆盖债（本节点实测）

按纪律先查图（`codegraph --repo . --view cards-B402-charter sym`），未命中再 grep：

- 命中且 `anchor=ok`：`FailureClass`（`m_proto_FailureClass`，added）、`TurnEnd`（`m_ledgerstep_TurnEnd`，added）、`failureClassOf`（`n_ledgerstep_failureClassOf`，added）、`awaitNode`（`n_ledgerstep_StepRunner_awaitNode`，modified）。
- 命中但 `status=None`（本卡触碰、未登记 modified，扫描欠账）：`NodeStep.RunOnce`（`n_ledgerstep_NodeStep_RunOnce`）。`Manager.handleResult`/`Manager.Continue` 在 `sym` 上按普通路径命中基线。
- **未命中**：`ZeroTextContinueInstruction` → `codegraph sym` 返回「不在图中（近似候选: []）」，记入图覆盖债，本 plan 对该常量只用普通路径（文件:行 `runner.go:416`），未以 grep 冒充图覆盖。
- contract §8 的「视图为最小结构增量、absorb 前重扫」欠账 **归 T5 显式对账**，不在本节点修。

> 未验证项：T1–T5 的实现（本节点只读 plan，未做）。T0 的基线四闸**已亲跑到结果**（§4，原始输出见台账）。
