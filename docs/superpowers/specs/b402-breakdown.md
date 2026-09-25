# B402 拆解稿：派发节点零文本回合的单次自动续接与生命周期不早归档

状态：**待拍板**（2026-09-25；handoff 派发形态——executor 出稿，本地协调者拍板；裁决逐项回填 §8 后头部改「已拍板（日期）」）
卡：B402
标题：派发节点回合零文本收尾（流中断）：裁决解析失败 + 落等人，每起丢一轮（今晚两起实录）
定级：**L3 轻档**（spec/contract 定级；本稿按 `codegraph/best.json` 实测触及域，见 §2.4 澄清 1）
路由：contract → breakdown → plan → implement → review → acceptance → finish
有效基线：`cards/B402-spec` @ `5d6607fc`（本卡合并目标）；当前工作分支 `cards/B402-charter-2`（不切换、不越过）
上游 spec：`docs/superpowers/specs/b402.md` 头部「状态：已批准」（用户 2026-09-25 批准范围 B，本稿逐字核对文件头）
冻结 contract：`docs/superpowers/specs/b402-contract.md` 头部「上游状态：已批准」「冻结状态：本提交随 `codegraph/target.json` 与 `codegraph/diffs/cards-B402-charter.json` 冻结」
本稿台账：`docs/superpowers/ledgers/2026-09-25-b402-breakdown-ledger.md`
图依据：`codegraph/best.json`（`parent` 为空的顶层领域 = 子系统清单，`type` 即逻辑/边界标注）+ `codegraph/diffs/cards-B402-charter.json`（view=`cards-B402-charter`）
角色边界：本文是**提案**；不写实现代码、不建卡、不派发、不调用 handoff CLI、不起新 executor。扇出与拍板归协调者。

---

## 0. 待拍板岔口清单（集中，拍板者按此裁决）

| 编号 | 岔口 | 方案与取舍 | 本稿倾向 |
| --- | --- | --- | --- |
| **P1** | 代码面是否单轮闭合：是否还有可扇出的**实现**子卡 | **甲（推荐）：单轮闭合**——contract Ticket 0 已落全部运行时代码（proto 词表 / `executor.Result` / 五家 adapter 分类 / `FailedPayload` 透传 / ledgerstep `TurnEnd`+`awaitNode` 单次续接 / `RunOnce` 去 defer 收口）与主红测，含三条变异可红；本稿复核 `go build ./...`、`go vet ./...`、相关包子集测试**本轮新鲜绿**（§1.3、台账 5/6）。**乙**：另开一张实现复核卡复跑已绿判据（多一次往返，产出与 Ticket 0 逐字重复）。 | **甲**。与 B380 同款：修复面已被 contract 冻结时一并落地并红→绿；再开实现卡是复跑。 |
| **P2** | 跨进程端到端回路的范围与载体（spec §9.1 主红测 / §9.3 步 4） | **甲（推荐）：落在本仓机内**——用真 `*Manager` + `fake` executor + `httptest` 做 spec §9.1 的阶段化回路（`zero_text→completed`、`zero_text→zero_text`），不依赖真实供应商；已有 `internal/orchestration/gateway_http_test.go` 的「真 Server 挂真 Manager」外部测试包先例。**乙**：只写进真机清单交协调者。取舍：甲把 spec 明列的**主判据**变成机内可验，成本约一个新测试文件；乙省一次实现但把主红测推给真机，偏离 spec §9.1 原意。 | **甲**。spec §9.1 明说主回路「完全由测试推进事件，不依赖 sleep」，是机内可验承诺。 |
| **P3** | 图重扫 / absorb 的范围（contract §8 扫描欠账） | **甲（推荐）：本卡只保持结构性最小视图 diff + 把全量重扫列扫描欠账交后续扫描轮**（contract §8 原意）。**乙**：本卡内对触碰文件做一次全量重扫并 absorb 回 baseline。取舍：乙成本大，且会把 `validate` 里既存的他卡图债（B272/B374 两条）搅进本卡边界。 | **甲**。contract §8 已把「absorb 前重扫」显式列为欠账，本稿不擅自升格。 |
| **P4** | 日志断言（spec §9.2 末条）的归属 | **甲（推荐）：并入 S2**（ledgerstep 测试扩展，与生命周期早退路径同文件、同夹具）。**乙**：单开日志断言子卡。取舍：甲少一次派发，但 S2 文件集略大；乙文件边界更纯。 | **甲**。日志生成点（`awaitNode`/`RunOnce`）与早退路径同属 `internal/ledgerstep`，同夹具更省。 |
| **P5** | spec §10「后续项须登记 `docs/roadmap.md`」的归属 | **甲（推荐）：单开一张极小文档子卡 S4**（`docs/roadmap.md` 新增「来自 B402 spec」段，三条后续项），判据可 grep、可独立验收。**乙**：并入 S2/S3 或交 finish 收口。取舍：甲可追溯、判据独立；乙省卡但登记动作容易被漏（spec §10 明令「不得只留在本段」）。 | **甲**。spec §10 是显式承诺，给有界文件集的最小卡最稳。 |

> 若协调者裁决改变契约冻结面（如 P1 选乙、P3 选乙需改验收判据），应先回写 `b402-contract.md` 修订记录再扇出；本稿不自行吸收。

---

## 1. 触及子系统清单与派卡资格核

子系统 id 与类型逐字取自 `codegraph/best.json` 的 `domains`（`parent` 缺省 = 顶层子系统，`type` 即逻辑/边界标注）。域归属由 `codegraph --repo . --view cards-B402-charter sym <符号>` 实读（台账 4）。

| 子系统（图 id） | 图类型 | 本卡角色 | 有界文件集 / 暴露面 | 派卡资格四条核验 |
| --- | --- | --- | --- | --- |
| `d_protocol` | 逻辑型 | **契约词表主面**（Ticket 0 已落，零剩余代码） | 已落：`internal/proto/failure.go#FailureClass`（新增）+ `FailureClassZeroText`。暴露面 = 两个导出符号，契约已冻结。 | ①单文件规则圈得出；②导出面已冻结；③对同级域无新边（proto 是词表唯一处）；④纯类型，机内编译期可验。 |
| `d_execution_contract` | 逻辑型 | **结果契约主面**（Ticket 0 已落） | 已落：`internal/executor/executor.go#Result` 新增 `FailureClass`；`internal/executor/turn/fallback.go#NoTrailerResult`（**不写分类**）。暴露面 = `Result` 字段，零新增函数。 | ①按目录规则（`internal/executor` 顶层 + `turn` 子包）；②字段 additive；③对 `d_protocol` 走既有方向；④纯类型/纯构造，机内单测闭环。 |
| `d_execution_adapters` | **边界型**（接缝对面是外部 executor 进程与供应商流中断的现实） | **分类生产主面**：五家 adapter 明确零文本分支写 `zero_text`（Ticket 0 已落）；**邻近分支反例断言未落**（S1） | 已落：`internal/executor/opencode/adapter.go#Adapter.mapIdle`、`internal/executor/codex/adapter.go#Adapter.finishTurn`、`internal/executor/grok/adapter.go#Adapter.finishTurn`、`internal/executor/claudecode/adapter.go#Adapter.fallbackClassify`、`internal/executor/agy/adapter.go#Adapter.fallbackClassify`。测试：五家 `*_test.go`（S1 扩展）。暴露面零变化（只改函数体）。 | ①按目录规则（`internal/executor/<adapter>`）；②无新导出符号；③对 `d_execution_contract`/`d_protocol` 走既有方向；④分类正例机内可验；**真实供应商流中断是否走该分支归真机**（§6 #1）。 |
| `d_orchestration` | 逻辑型 | **透传与状态门主面**（Ticket 0 已落） | 已落：`internal/orchestration/contracts.go#FailedPayload`（`failure_class,omitempty`）、`#NewFailedPayload`（第 4 参数）、`internal/orchestration/manager.go#Manager.handleResult`（透传）、`#Manager.Continue`（`waiting_review` 状态门，零改动）。测试：`handleresult_notrailer_test.go`（S1 扩展）、S3 e2e。 | ①单文件 + manager 既有函数体；②字段 additive、签名加参（编译期锁）；③无新跨域边；④`handleResult` 用真 `*Manager`+`*Hub` 机内闭环；`Continue→adapter.Send` 对面是外部执行器，归真机。 |
| `d_ledger` | 逻辑型 | **等待/生命周期主面**（Ticket 0 已落）；测试矩阵与日志断言未补全（S2） | 已落：`internal/ledgerstep/wire.go#TurnEnd`/`#failureClassOf`/`#waitForTurnEnd`/`#waitForTurnEndGrace`、`internal/ledgerstep/runner.go#StepRunner.awaitNode`、`#ZeroTextContinueInstruction`、`internal/ledgerstep/node.go#NodeStep.RunOnce`（去无条件 defer）。测试：`b402_retry_test.go`、`wire_test.go`（S2 扩展）。 | ①`internal/ledgerstep` 单包圈得出；②`clientFinalMessage` 签名被 B233.16 编译期锁死；③无新跨域边；④分阶段替身机内闭环，无 sleep。 |
| `d_gateway` | **边界型**（HTTP/WS 对面是浏览器/CLI） | **驱动与端到端接缝**（零生产改动；S3 测试面） | 只读：`internal/agentd/cardstep.go#Server.runStep`（驱动 `StepRunner.Run`）、`internal/agentd/handlers.go#Server.handleContinue`（`/api/tasks/{id}/continue`，`waiting_review` 门）、`#Server.handleEvents`（`/ws/events`）。本卡不改生产代码。 | ①不派生产卡（仅测试）；②路由/帧契约不变；③无新边；④机内 `httptest` 面既有，本卡沿用；真实浏览器/CLI 归真机。 |
| `d_transport` / `d_transport_channel` | **边界型**（对面是跨机 relay/直连 agentd 的现实） | **续接/等待能力链（零改动）** | 只读：`internal/client/client.go#Client.Continue`、`internal/client/execution.go`（`ExecutionClient.Continue` 接口面）、`WaitEvent` 路径。本卡不改。 | ①不派卡；②客户端接口面冻结；③无新边；④跨机续接与等待行为归真机（§6）。 |

**不列为本卡实现域（零改动）**：`d_cli`（`handoff continue` 命令是既有消费面）、`d_sessions`、`d_workspace`、`d_policy`、`d_scheduling`、`d_execution_host`、`d_keystone`、`d_collab`、`d_maintenance`、`d_web`。

### 1.1 竖切债核对（架构法第三条）

- `internal/ledgerstep`、`internal/orchestration`、`internal/executor` 顶层与五家 adapter 包：本卡**只改既有函数体 / 加字段**，不新增生产源文件、不扩前缀族；`b402_retry_test.go`、S1/S2 扩展测试文件是测试面，不触发升格。
- `internal/agentd`（图内 57 文件平铺大包，`codegraph check` 有 oversized-package/prefix-family warns）+ `cmd`：本卡在这两个包**零改动**（`d_gateway` 只读）。**能圈出有界文件集，不插竖切还债卡。**
- 实现若需改上表「只读」面之外的生产文件，必须退回协调者重核边界，不得以「同目录」放宽。

### 1.2 图覆盖债

- **本卡新符号已入分支视图**：`codegraph/diffs/cards-B402-charter.json`（view=`cards-B402-charter`，base=`5d6607fc`）登记 `nodesAdded` 3（`m_proto_FailureClass`、`m_ledgerstep_TurnEnd`、`n_ledgerstep_failureClassOf`）、`nodesModified` 6（`m_executor_Result`、`m_orchestration_FailedPayload`、`n_orchestration_NewFailedPayload`、`n_ledgerstep_waitForTurnEnd`、`n_ledgerstep_waitForTurnEndGrace`、`n_ledgerstep_StepRunner_awaitNode`）。实测四条 `sym` 均命中新视图、`anchor=ok`（台账 4）。
- **未随视图登记的本卡触碰符号**（`codegraph sym` 在视图/基线命中 `status=None`，即沿用 baseline 定义、无 diff 变更）：`NodeStep.RunOnce`（函数体改了但节点未登记为 modified）、`Manager.handleResult`、`Manager.Continue`、`ZeroTextContinueInstruction`。这是 contract §8 明说的「最小结构增量、非全量重扫」的欠账，**归 S5 显式认账**；本稿对这些只用普通路径或已入图锚。
- **图外符号**：`internal/agentd/cardstep.go#Server.runStep` 在基线命中 `d_gateway`，但不在本卡 diff；`internal/executor/executor.go#Result` 在视图内。
- 本稿收口前亲跑 `codegraph resolve --doc docs/superpowers/specs/b402-breakdown.md`（结果落台账 8）；坏锚即修。

### 1.3 本轮新鲜复核事实（不写没跑到的结论）

```text
$ go build ./...                                             → BUILD_EXIT=0
$ go vet ./...                                               → VET_EXIT=0
$ go test ./internal/ledgerstep/ ./internal/orchestration/ ./internal/proto/ ./internal/executor/... -count=1
    ok  internal/ledgerstep 17.163s / internal/orchestration 62.039s / internal/proto 0.054s
    ok  internal/executor（含 agy/claudecode/codex/grok/opencode/fake/rawtap/turn）全绿 → TEST_EXIT=0
$ codegraph --repo . check                                  → CHECK_EXIT=0（fails=[]）
$ codegraph --repo . --view cards-B402-charter check        → VIEW_CHECK_EXIT=0（fails=[]）
$ codegraph --repo . validate                               → VALIDATE_EXIT=1，2 个既存问题（B272/B374 视图，非本卡引入）
$ codegraph --repo . resolve --doc docs/superpowers/specs/b402-contract.md → RESOLVE_EXIT=0（锚全 ok/moved）
```

---

## 2. 契约增量核对

### 2.1 上游状态位核对（读文件头，不靠会话记忆）

- **spec**：`docs/superpowers/specs/b402.md:3` 实读「**状态：已批准**（用户 2026-09-25 批准；范围 B：仅 `zero_text` 自动重试一次）」——逐字核对通过，引用有效。
- **contract**：`docs/superpowers/specs/b402-contract.md:3` 「**上游状态：已批准**」+ `:7`「**冻结状态：本提交随 `codegraph/target.json`… 与 `codegraph/diffs/cards-B402-charter.json` 冻结**」+ `:5`「有效基线 `cards/B402-spec` @ `5d6607fc`」——与提交 `d83657fd`（contract 提交）一致，核对通过。
- **图门禁**（本轮新鲜，台账 5/6）：`codegraph check` 基线 exit 0、`--view cards-B402-charter check` exit 0；`codegraph validate` exit 1，`issues` 恰为既存两条（`[cards-B272-charter] 新增容器 k_dropdir_fn 引用不存在的基线领域 d_coordination_api`、`[cards-B374-charter] 新增容器 k_collab_model 已存在于基线`），**非本卡引入、不修**。

### 2.2 契约 §3 原子冻结清单逐条对照（拆解吸收位置）

contract §3 共 22 条，逐条核过现状代码后归属如下（「T0」= contract Ticket 0 已落并自测，非新子卡）：

| 契约 §3 条目 | 现状核对 | 本稿归属 / 越界结论 |
| --- | --- | --- |
| 1 `FailureClassZeroText == "zero_text"`、底层 string | `internal/proto/failure.go:23` 逐字在位 | T0；不越界 |
| 2 `executor.Result.FailureClass` 零值=未分类 | `internal/executor/executor.go#Result` 字段在位 | T0；不越界 |
| 3–5 `FailedPayload.failure_class` omitempty / 精确值 / `NewFailedPayload` 第 4 参数 | `contracts.go` 字段与构造器在位；`handleresult_notrailer_test.go` additive 测试在位 | T0；不越界 |
| 6–7 `handleResult` 透传 / 无分类不出键 | `manager.go:3497-3498` 透传 `r.FailureClass`；测试断言旧 wire 无键 | T0（正例）；**无分类的独立夹具**归 S1 |
| 8–11 `waitForTurnEnd` 的 completed / turn_failed(zero_text) / failed 不分类 / 缺字段未知值不重试 | `internal/ledgerstep/wire.go#failureClassOf` 只认 `turn_failed`；`wire_test.go` 三例全绿 | T0；不越界 |
| 12 `finalMessageFromEvents` 三条语义不回归 | `wire.go` 签名不变，既有测试保持绿 | T0；不越界 |
| 13–17 `awaitNode` 主回路恰一次 / 耗尽恰一次 / 无分类不续接 / 续接失败 fail-closed / 指令固定非空 | `b402_retry_test.go` 四例 + `ZeroTextContinueInstruction` 在位 | T0；**`zero_text→普通 turn_failed` 与更细分支**归 S2 |
| 18 `RunOnce` 解析失败 FinishTask=0、Outcome needs_human | `internal/ledgerstep/b402_retry_test.go#TestNodeStepDoesNotFinishTaskOnParseFailure` | T0；**diff/产出/发布/写闸/路由早退**归 S2 |
| 19 `RunOnce` FinishTask 失败转人工 Reason=task 归档失败 | `internal/ledgerstep/b402_retry_test.go#TestNodeStepFinishTaskFailureHaltsForHuman` | T0；不越界 |
| 20 FinishTask 仍在 PublishWorkBranch 之后 | `node.go:477-493` 顺序 + `internal/ledgerstep/node_test.go#TestNodeStepPublishesWorkBranchBeforeFinishTask` | T0；不越界 |
| 21 五家 adapter 零文本分支写 zero_text | 五家 `*_test.go` 正例各 +3 行 | T0（正例）；**邻近分支反例**归 S1 |
| 22 缺字段/未知分类旧 wire 不触发续接 | `wire_test.go` + `awaitNode` 无分类例汇合 | T0；不越界 |

**§6 移交区（不计冻结条目）逐条归属**：邻近分支反例断言→S1；`handleResult` failed 分支无独立夹具→S1；跨进程 e2e 两条回路→S3；日志字段断言→S2；固定续接指令改文本走契约修订（无常驻动作）。

**§8/§9 欠账逐条归属**：分支视图最小增量非全量扫描→S5；移交区三项→S1/S2/S3；`cardstep.go`/`types.go` 既存未 gofmt（非本卡）→不认领。

> **本稿与 contract §6 的口径差**：contract §6 只列「邻近反例 / 跨进程 e2e / 日志」三项欠账，但 spec §9.2 必测矩阵还含「`zero_text→普通 turn_failed`」「`NoTrailerResult` 有新提交不续接」「旧 wire 缺字段形状」「diff/产出/发布/写闸失败均不调用 Done」四项。**这不是新接缝**（都有冻结载体），本稿按矩阵补齐为 S1/S2/S3 验收，见 §2.4 澄清 3。

### 2.3 退回 contract（不许边拆边加）

**无。** 逐条核对 contract §3（22 条）与 §6/§8/§9 后，未发现「spec 承诺了行为、冻结物里没有载体」的新接缝：

- 失败分类的载体（`proto.FailureClass`、`Result.FailureClass`、`FailedPayload.failure_class`、`TurnEnd.FailureClass`）**全部已冻结**；
- 自动续接的能力来源（`client.ExecutionClient.Continue`）与状态门（`Manager.Continue` 需 `waiting_review`）**复用既有、已冻结**；
- 生命周期收口（`FinishTask` 显式调用）已在 `RunOnce` 落地；
- 端到端 e2e 与日志断言是**加测试**，不是新接缝（P2/P4 只决定归属）。

### 2.4 边界澄清（不退回，已回写 `b402-contract.md` 末尾修订记录一行）

1. **域归属按图修正**：contract 头部写「跨 proto/executor/orchestration/ledgerstep 四域」。图上实为：`proto.FailureClass → d_protocol`；`executor.Result → d_execution_contract`；五家 adapter → `d_execution_adapters`；`FailedPayload`/`handleResult`/`Continue → d_orchestration`；`ledgerstep`（`TurnEnd`/`waitForTurnEnd`/`awaitNode`/`RunOnce`）→ `d_ledger`。即 **5 个逻辑域/子域**（`d_execution` 拆分出 contract/adapters 子域），另加零改动的边界面 `d_gateway`/`d_transport`。**不改 spec/contract 定级**（定级是上游拍板），仅记图事实。
2. **驱动接缝域归属补记**：contract 未点名 `agentd` 驱动面域；图上 `Server.runStep`（`internal/agentd/cardstep.go`）与 `Server.handleContinue`/`Server.handleEvents`（`internal/agentd/handlers.go`）均归 `d_gateway`。本卡在这三处**零生产改动**，仅作 S3 的 e2e 接缝。
3. **续接客户端面域归属补记**：contract §1「Continue 客户端面 `internal/client/execution.go:38`」未标域；图上 `Client.Continue` 归 `d_transport_channel`（父 `d_transport`），接口 `ExecutionClient.Continue` 未入图。本卡零改动。
4. **§6 移交区与 spec §9.2 矩阵的差额显性化**：见 §2.2 末注——四项矩阵项非新接缝，本稿落 S1/S2/S3 验收，不退回 contract。

以上四条已回写 `b402-contract.md` 末尾「## 10. 修订记录（breakdown 出稿轮，2026-09-25）」。

---

## 3. 子卡清单与依赖 DAG

序号 S1–S5 是提案编号，真实卡号由协调者扇出时分配。S1–S3 均为**测试/验证子卡**，不新增生产代码（P1=甲 时）。

### 3.0 DAG

```text
[Ticket 0 已落，不重开]  contract @d83657fd：
  proto.FailureClass + executor.Result.FailureClass + 五家 adapter 分类
  + FailedPayload/handleResult 透传 + ledgerstep TurnEnd/awaitNode/RunOnce 生命周期
  + 主红测/耗尽反例/wire 分类/payload additive/五家 adapter 正例 + 三条变异可红
       │
       ├──> S1 邻近反例 + 未分类透传断言（d_execution_adapters / d_execution_contract / d_orchestration）
       ├──> S2 等待/生命周期矩阵补齐 + 日志断言（d_ledger）
       ├──> S3 跨进程端到端回路 + wire 保真（d_orchestration / d_gateway）
       ├──> S4 roadmap Out of Scope 登记（文档面）
       └──> S5 图对账/重扫欠账认账（codegraph）
                 （S1–S3 完成后）
                       │
                       └──> review / acceptance / finish
真机验收（真实供应商流中断 / 旧 agentd / 跨机续接）──────────> 协调者执行（§6）
```

S1–S4 相互独立、可并行（S3 的 e2e 依赖生产代码，而生产代码已在 Ticket 0 落地，不依赖 S1/S2 的测试夹具）；S5 在 S1–S3 落测试后做视图对账。Ticket 0 不是可独立派发的子卡（P1=甲）。

### 3.1 S1（`d_execution_adapters` + `d_execution_contract` + `d_orchestration`）：零文本分类的邻近分支反例与未分类透传断言

**①契约引用**：contract §3-6/7、§3-21、§4（永不做：不给权限被拒/内容过滤/进程退出/`NoTrailerResult` 自动续接）、§6 移交区第 1/2 条；spec §9.2「五家 adapter 的明确零文本分支分别断言分类；question/权限/内容过滤/进程退出邻近分支断言不分类」；本稿 §2.4 澄清 1/4。

**②意图与为什么**：Ticket 0 只锁了五家 adapter 零文本分支的**正例**（写了 `zero_text`）。「邻近分支不得写分类」目前**只在实现里恰好为真**，没有能变红的测试——一次「顺手统一分类」就会把权限被拒/内容过滤/无 trailer 有提交也送进自动续接，重复执行已落地工作。本卡把这条承重安全属性用反例断言锁住，并补上 `NoTrailerResult`（有提交无 trailer）与 `handleResult` 无分类路径的独立断言。

**③验收（行为化，逻辑型机内闭环；`d_execution_adapters` 的真实供应商面归真机）**：

- `go test ./internal/executor/... ./internal/orchestration/ -count=1` 退出 0，且新增/扩展以下断言（**每条都要有能变红的反面**）：
  - 五家 adapter 零文本正例仍断言 `Result.FailureClass == proto.FailureClassZeroText`。
  - 五家 adapter **邻近分支**断言 `Result.FailureClass == ""`：question/权限被拒转 question、内容过滤、进程退出/serve 死亡、`NoTrailerResult`（无 trailer 但有新提交）。落在各自既有用例（如 `opencode` 的 `TestMapIdleRejectedTurnStillAsks`/`TestIdleClassifyAsk`/`TestIdleFallbackNoTrailer`/`TestServeDeathEmitsFailed`，`codex`/`grok`/`claudecode`/`agy` 的 `TestNoTrailerWithNewCommitDoesNotDeclareCompletion` 等）内加字段断言。
  - `internal/executor/turn`：`NoTrailerResult(...)` 返回 `FailureClass == ""`（新增断言）。
  - `internal/orchestration`：`handleResult` 对 `Result{OK:false, FailureClass:""}` 产出的 `turn_failed` payload **不含** `failure_class` 键；对 `Result{OK:false, FailureClass:zero_text}` 精确含之（additive 正例已在，补无分类负例夹具）；`Stop`/对账产生的 `failed` 终态事件不带分类。
  - **变异验证**：把任一邻近分支的 `FailureClass` 改成 `proto.FailureClassZeroText` → 对应反例断言必须变红；删掉某家零文本分支的 `FailureClass` 赋值 → 正例必须变红。（跑后还原，`git diff --stat` 复核。）
- 不修改任何生产代码；若发现某邻近分支**实际**写错分类（非测试问题），停止并退回协调者（属 contract 冻结面）。
- 真实供应商流中断是否真的走零文本分支 → **未验证，需真机**（§6 #1）。

**④入口指针与有界文件集**：`internal/executor/opencode/adapter_test.go`、`internal/executor/codex/fallback_verdict_test.go`、`internal/executor/grok/fallback_verdict_test.go` + `internal/executor/grok/askquestion_internal_test.go`、`internal/executor/claudecode/fallback_verdict_test.go`、`internal/executor/agy/fallback_verdict_test.go`、`internal/executor/turn/fallback_test.go`、`internal/orchestration/handleresult_notrailer_test.go`。符号锚：`internal/executor/turn/fallback.go#NoTrailerResult`、`internal/orchestration/manager.go#Manager.handleResult`、`internal/orchestration/contracts.go#NewFailedPayload`、`internal/proto/failure.go#FailureClass`。

**缺陷族对抗（验收栏）**：
1. **生命周期/状态机中断**：无，因为纯结果分类断言，无宿主状态、无并发。
2. **静默失败/误导报错**：反例断言正是防「静默把邻近失败当可重试」。存在「报成功但没做」的窗口吗——无：分类由 adapter 明确分支写死，测试锁住。
3. **跨平台假设**：无，因为断言在 adapter 事件对象层，不涉路径/进程组/webview；真实平台差异归真机。
4. **假红/假绿**：反面断言（邻近分支必须**不**带分类）是关键；正例与反例都要求变异可红；夹具文案与 `FailReason` 解耦（用任意文案）。本卡锁的是**调用方依赖的字段行为**（`FailureClass`），换实现只要字段不变仍绿。
5. **门禁绕过**：无写路径/执行路径新增，不涉权限门。
6. **序列化边界**：`Result.FailureClass` → payload 已在 contract 锁（additive/omitempty）；本卡补无分类键缺席断言，覆盖「字段缺失 vs 零值」。
7. **枚举新值过既有白名单**：`zero_text` 是唯一新值；本卡核对的「白名单」是各 adapter 的邻近 `switch`/分类分支**不得误登记**新值——反例断言即此白名单的负向锁。
8. **承重安全属性有测试锁住**：本卡主体就是「无提交/邻近失败不重试」这条安全属性的**可变红测试**；`NoTrailerResult` 不分类是一票否决点。
9. **webview 候选族**：无，因为不触 `d_web`。

### 3.2 S2（`d_ledger`）：等待/生命周期矩阵补齐与日志断言

**①契约引用**：contract §3-13..20、§2.5、§2.6、§6 移交区第 4 条；spec §4.2/§4.3、§9.2（`zero_text→普通 turn_failed`；解析/diff/产出/发布/写闸失败均不调用 Done；日志字段）；本稿 §2.4 澄清 4。

**②意图与为什么**：Ticket 0 的 `awaitNode` 单测只覆盖 `zero_text→completed`、`zero_text→zero_text`、无分类、续接失败；`RunOnce` 只覆盖解析失败与 FinishTask 失败。spec §9.2 还要求：第二次是**普通 `turn_failed`** 时不二次续接；**diff/产出/发布/写闸/路由**失败路径同样 FinishTask=0；日志能看见 task/seq/failure_class/attempt/continue 结果且不含正文/凭据。这些是「任何早退都不归档」这条生命周期修复的完整边界，缺了就等于只在最显眼的两条路径上为真。

**③验收（行为化，逻辑型机内闭环）**：

- `go test ./internal/ledgerstep/ -count=1` 与 `go test -race ./internal/ledgerstep/ -count=1` 退出 0，且新增：
  - `awaitNode`：`zero_text → 普通 turn_failed`（第二终态无分类或非零文本）→ `Continue` 恰 1 次、`WaitEvent` 恰 2 次、返回第二终态的报文。
  - `RunOnce` 早退矩阵：`Await` 返回错误、裁决解析失败、review 只读 diff 失败/违规、写闸（`WriteGate` 返回 false / `gatedWrite` 失败）、产出物 diff 失败/缺产出物、`PublishWorkBranch` 失败、`routeTo` 失败——**每条**断言 `Outcome.Action == ActionNeedsHuman` 且 `FinishTask` 调用 0 次。（pass 路径 1 次、FinishTask 自身失败 1 次且 Reason=`task 归档失败`，已有。）
  - **日志断言**：用 `slog` 的 `bytes.Buffer`/自定义 handler 捕获 `awaitNode` 零文本路径输出，断言含 `task`、`seq`、`failure_class`(=`zero_text`)、续接结果；且**不含**完整最终正文与凭据串。反面：把日志字段删除后断言变红。
  - **变异验证**：把 `node.go` 末尾的显式 `FinishTask` 改回 `defer` 无条件调用 → 至少一条早退 no-Done 断言变红；去掉 `awaitNode` 的日志字段 → 日志断言变红。（跑后还原。）
- `go build ./...` 与 `go vet ./...` 退出 0。

**④入口指针与有界文件集**：`internal/ledgerstep/b402_retry_test.go`（扩展）、`internal/ledgerstep/node_test.go`（扩展早退矩阵）、`internal/ledgerstep/wire_test.go`（如需）。符号锚：`internal/ledgerstep/runner.go#StepRunner.awaitNode`、`internal/ledgerstep/node.go#NodeStep.RunOnce`、`internal/ledgerstep/wire.go#waitForTurnEnd`、`internal/ledgerstep/wire.go#TurnEnd`、`internal/ledgerstep/runner.go#ZeroTextContinueInstruction`。

**缺陷族对抗（验收栏）**：
1. **生命周期/状态机中断**：本卡主体——宿主重启不自动重放已消费失败事件（awaitNode 单次、无持久计数）在 contract 已决；本卡断言「一次 `RunOnce` 最多一次」「任何早退不归档」。中途重启的恢复语义归真机复验（§6 #2）。
2. **静默失败/误导报错**：每条早退路径都转 `needs_human` 并留可行动 reason；`FinishTask` 失败显式 Warn + Reason；本卡用断言锁「失败必须留等人、不得伪装成已归档」。
3. **跨平台假设**：无，因为纯状态机与内存夹具，不涉平台面。
4. **假红/假绿**：阶段化替身按序吐事件、不依赖 sleep；早退矩阵每格独立夹具；反面断言（FinishTask 必须 0 次）是稳定假绿的解药；日志断言锁**调用方依赖的可观测行为**，不绑私有日志实现（只断言键与内容类别）。
5. **门禁绕过**：写闸路径本身要被覆盖——`WriteGate` 返回 false 时 `gatedWrite` 拒写、`RunOnce` 转等人且不归档；检查与动作之间的窗口（TOCTOU）本卡不改，归既有锁语义。
6. **序列化边界**：无新增字段；已有 wire 编解码由 contract 与 S3 覆盖。
7. **枚举新值过既有白名单**：`TurnEnd.EventType` 的 `completed/turn_failed/failed/archived` 分支由既有 `waitForTurnEnd` switch 处理，本卡补 `turn_failed→普通` 分支断言。
8. **承重安全属性有测试锁住**：`FinishTask` 恰好一次且只在收口末尾——用「早退 0 次 + pass 1 次 + publish 前不 done」三面锁住。
9. **webview 候选族**：无。

### 3.3 S3（`d_orchestration` + `d_gateway`）：跨进程端到端回路与 wire 保真

**①契约引用**：contract §3-6/7、§3-22、§2.4、§6 移交区第 3 条；spec §9.1（主红测/主反例）、§9.3 步 4、§7.2 测试接缝清单（Manager.handleResult / FailedPayload JSON / HTTP/WS 消费）；本稿 §2.4 澄清 2/3。

**②意图与为什么**：spec §9.1 的主红测是 `httptest.Server` + 阶段化 task 状态、完全由测试推进事件、不依赖 sleep。contract 只锁了 `awaitNode` 单测层，跨进程链路（`handleResult` 写事件 → `/ws/events` 或 attach → `/continue` 状态门 → 第二终态 → `done`）**没有一条真实序列化边界的回归**。本卡把它补成机内可验，同时证明 `failure_class` 经 HTTP/WS/JSON 不丢、空值不出现。

**③验收（行为化，边界型——机内验契约形状；真实供应商/真实跨机归真机）**：

- `go test ./internal/orchestration/ ./internal/agentd/ -count=1` 退出 0，且新增（外部测试包，真 `*Manager` 挂真 `agentd.Server` + `fake` executor + `httptest`）：
  - **主回路 `zero_text→completed`**：第一终态 `turn_failed{failure_class:zero_text, fail_reason:<任意文案>}` → `POST /api/tasks/{id}/continue` 仅在 `waiting_review` 成功并切 `running` → 第二终态 `completed` 带有效 verdict → 断言 `continue=1`、`done=1`、裁决解析成功。
  - **主反例 `zero_text→zero_text`**：`continue=1`；第二次失败后 `done=0`、**无 `archived`**、节点 `Outcome=needs_human`；随后**显式** `POST /continue` 仍能成功（不得 409）。
  - `turn_failed` **无** `failure_class` → 不触发续接（`continue=0`）；未知值 → 不续接；`failed` 终态即使带字段 → 不续接。
  - **wire 保真**：`FailedPayload` JSON roundtrip——`failure_class=zero_text` 精确保留、空值键不出现；经 `/ws/events`（或 attach 快照）取回的事件仍能解出 `zero_text`（穿过真实 HTTP/JSON 边界，而非只测 `json.Marshal`）。
  - **夹具脱敏**：第一次失败的 `fail_reason` 用与实现无关的文案，证明分支只依赖结构化字段（沿用 `b402_retry_test.go` 的写法）。
  - **变异验证**：把 `failureClassOf` 改成忽略 payload → 主回路断言变红；把 `Continue` 状态门放宽 → 「无分类/未知值不续接」与「后显式 continue 不 409」之一变红。（跑后还原。）
- 真实供应商流中断、真实跨机 relay/直连续接、旧 agentd 二进制 → **未验证，需真机**（§6 #1–#3）。

**④入口指针与有界文件集**：新增/扩展 `internal/orchestration/b402_e2e_test.go`（外部测试包 `orchestration_test`，可仿 `gateway_http_test.go` 的自足夹具）、必要时扩展 `internal/agentd/server_test.go` 或 `internal/agentd/receiver_occupancy_test.go`。符号锚：`internal/orchestration/manager.go#Manager.handleResult`、`internal/orchestration/manager.go#Manager.Continue`、`internal/orchestration/contracts.go#FailedPayload`、`internal/agentd/handlers.go#Server.handleContinue`、`internal/agentd/handlers.go#Server.handleEvents`、`internal/client/client.go#Client.Continue`（`ExecutionClient.Continue` 未入图，按普通路径引用）。

**缺陷族对抗（验收栏）**：
1. **生命周期/状态机中断**：e2e 用阶段化状态机完全由测试推进（无 sleep），中途无进程重启面；外部抢先续接（409）由主反例覆盖；跨 agentd 重启的自动计数恢复是 spec §10 明确 OOS，归真机/后续卡。
2. **静默失败/误导报错**：`/continue` 失败（409/400/网络）必须 fail-closed，不进入第二次等待——由主反例与 awaitNode fail-closed 例共同锁；错误必须可行动（状态门信息）。
3. **跨平台假设**：HTTP/JSON 中立；真实网络/跨机行为归真机。
4. **假红/假绿**：夹具必须穿**真** HTTP 序列化边界（`httptest`），不得只测包内 `json.Marshal`；反面断言（`done=0`、`archived` 缺席、`continue` 恰一次）是防假绿关键；夹具文案与实现解耦。
5. **门禁绕过**：**承重**——`/continue` 只在 `waiting_review` 放行是「不重复执行已落地工作」的门；本卡断言状态门在并发/错误路径仍关闭；检查与动作之间的窗口由 `Manager.Continue` 的 `transit` 保证，本卡不改但要在 e2e 里验「非 `waiting_review` → 拒绝」。
6. **序列化边界**：本卡主战场之一——`Result.FailureClass → FailedPayload → JSON → HTTP/WS → TurnEnd.FailureClass` 一条链路穿真边界，且用可空类型区分「字段缺失」与「值为零」。
7. **枚举新值过既有白名单**：`zero_text` 流经 `failureClassOf` 的精确匹配、`awaitNode` 的 `==` 判定；e2e 锁「未知值不重试」这条白名单行为。
8. **承重安全属性有测试锁住**：一次续接、无分类不续接、二次失败不归档——三条均在 e2e 层可变红。
9. **webview 候选族**：无（不触 `d_web`）。

### 3.4 S4（文档面）：spec §10 Out of Scope 后续项登记 `docs/roadmap.md`

**①契约引用**：spec §10「本期不做、后续要做」三条 + 「后续项须登记到 `docs/roadmap.md`，不得只留在本段」；本稿 §3.0 DAG。

**②意图与为什么**：spec §10 明列三条后续项（首个零文本事件的唤醒抑制与 retry ownership 全局可见性；跨 agentd 重启的自动续接计数与恢复；供应商流中断本身的根因治理），并硬性要求落到 `docs/roadmap.md`。现状 `grep -n B402 docs/roadmap.md` 无命中（台账 7）。本卡只做登记，不改任何运行时代码。

**③验收（行为化）**：

- `docs/roadmap.md` 新增可检索段（建议标题「## 来自 B402 spec（2026-09-25，本期不做、后续要做）」），`grep -n 'B402' docs/roadmap.md` 命中；段内三条分别含关键词：唤醒抑制 / retry ownership；跨重启自动续接计数；供应商流中断根因。
- 每条注明来源（`docs/superpowers/specs/b402.md` §10；`b402-contract.md`）。不得借本卡改 `internal/**`。

**④入口指针与有界文件集**：`docs/roadmap.md`（新增一段）。符号锚：无（纯文档）。

**缺陷族对抗（验收栏）**：
1. **生命周期**：无。
2. **静默失败**：无运行时代码；登记动作漏做即承诺失守，靠 `grep` 判据兜住。
3. **跨平台假设**：无。
4. **假红/假绿**：判据是可 grep 的文本，非测试；无假绿温床。
5. **门禁绕过**：无。
6. **序列化边界**：无。
7. **枚举白名单**：无。
8. **承重安全属性**：无。
9. **webview**：无。

### 3.5 S5（`codegraph` 图对账）：视图对账与重扫欠账认账

**①契约引用**：contract §7.3、§8、§9-2；spec §12 图覆盖债；`docs/codegraph-scan-recipe.md`；本稿 §1.2。

**②意图与为什么**：contract 的分支视图是**结构性最小增量**（只登记 3 added + 6 modified），不是一次全量重扫：`NodeStep.RunOnce`、`Manager.handleResult`、`Manager.Continue`、`ZeroTextContinueInstruction` 等本卡触碰但未登记为 modified 的符号，以及新增测试 `b402_retry_test.go` 与 `internal/proto/failure.go` 的文件级完整性，都还没进图。contract §8 已把「absorb 前的重扫」列为欠账。本卡把它显式认账为**扫描欠账**并执行 `codegraph` 三闸，避免「自己冻结的符号自己欠图覆盖」被静默带走。

**③验收（行为化）**：

- `codegraph --repo . check` exit 0；`codegraph --repo . --view cards-B402-charter check` exit 0（fails=[]）。
- `codegraph --repo . validate` 仍是 exit 1，且 `issues` **恰为**既存两条（`cards-B272-charter`、`cards-B374-charter`），**不得新增**本卡 issue。
- 按 `docs/codegraph-scan-recipe.md` 对 `internal/ledgerstep`、`internal/proto`、`internal/orchestration`、`internal/executor` 的本卡触碰文件做一次扫描/对账：把 `NodeStep.RunOnce`、`Manager.handleResult`、`Manager.Continue`、`ZeroTextContinueInstruction` 及新增测试节点补进视图（或按配方 absorb 回 baseline），未覆盖符号逐条记入 `docs/superpowers/ledgers/` 对应台账，**不以 grep 结果冒充图覆盖**。
- 真机无关；纯工具链，机内可验。

**④入口指针与有界文件集**：`codegraph/diffs/cards-B402-charter.json`、`codegraph/best.json`、`docs/codegraph-scan-recipe.md`、对应 ledger。符号锚：`codegraph --repo . sym` 四符号（`FailureClass`/`TurnEnd`/`failureClassOf`/`StepRunner.awaitNode` 已在视图，另四个待补）。

**缺陷族对抗（验收栏）**：
1. **生命周期**：无，因为图是静态产物。
2. **静默失败**：扫描若漏符号只会在图查询里表现为 NONE，不报错——故判据含「逐符号 `sym` 命中 + `validate` 不新增 issue」；漏记即不合格。
3. **跨平台假设**：无（图工具跨平台由脚本配方管）。
4. **假红/假绿**：`check` exit 0 不等于符号已登记（`sym` 命中才算）；判据同时要求 `sym` 实测与 validate 对照，避免「只跑 check 就宣称覆盖」。
5. **门禁绕过**：无权限面。
6. **序列化边界**：无。
7. **枚举白名单**：`FailureClassZeroText` 作为新枚举值在图里是否登记（`m_proto_FailureClass` 已 added，常量若单独成节点需补）。
8. **承重安全属性**：无。
9. **webview**：无。

---

## 4. 跨子系统行为闭环核对

只核 spec 承诺的跨子系统可观察行为（spec §3 目标 1–5、§6 用户故事、§9.1）。五格齐全，归属存在；「T0」= contract Ticket 0 已落实现 + 本稿复核，非新子卡。

| 触发者 | 权威事实/载体 | 消费者 | 可观察结果 | 归属子卡 |
| --- | --- | --- | --- | --- |
| adapter 明确零文本分支（供应商流中断） | `Result.FailureClass=zero_text`（`d_execution_adapters`）→ `handleResult` 透传 → `turn_failed.failure_class`（`d_orchestration`） | `waitForTurnEnd`/`failureClassOf` 读分类（`d_ledger`） | 首个终态为 `TurnEnd{turn_failed, seq, zero_text}`，不再压成裸字符串 | T0；真实中断归真机 #1 |
| 节点等待器收到 `zero_text` | `StepRunner.awaitNode` 调 `client.Continue`（`d_ledger`/`d_transport_channel`） | `Manager.Continue` 的 `waiting_review` 状态门（`d_orchestration`） | 同一 task 收到一次固定非空续接指令，`continue=1` | T0（单测）+ S3（e2e） |
| 第二次终态为有效 `completed` | `clientFinalMessage` 取报文 → `ParseVerdict` → `RecordReviewVerdict` → 收口动作 → `FinishTask`（`d_ledger`） | 卡账本 + task 生命周期（`d_orchestration`） | 正常路由并归档，不吵醒人工 | T0 + S3 |
| 第二次仍 `zero_text` / 续接失败 / 解析失败 | `awaitNode` 返回错误 / `RunOnce` 解析失败（`d_ledger`） | 节点 Outcome | 任务保持 `waiting_review`，落 `needs_human`，无 `Done`/`archived`，人工 `continue` 不再 409 | T0（单测）+ S2（早退矩阵）+ S3（e2e 反例） |
| 权限被拒 / 内容过滤 / 进程退出 / 无 trailer 有提交 | adapter 相应分支 `Result.FailureClass=""` | `failureClassOf` 返回零值 | 不自动续接，按既有流程交协调者 | T0（正例）+ S1（反例） |
| 旧 agentd / 旧 adapter / 缺字段历史事件 | payload 无 `failure_class` 键（`d_orchestration`） | `failureClassOf` 空值 | 语义是「不可自动重试」，不是「零文本」 | T0 + S1（无分类键缺席断言）+ S3（未知值 e2e） |
| 排障者读日志/事件 | `awaitNode`/`RunOnce`/`handleResult` 的 slog（`d_ledger`/`d_orchestration`） | 人 | 能区分首次自动续接 / 续接后成功 / 续接耗尽或外部竞争，不靠失败文案 | S2（日志断言）+ 真机 #4 |
| 协调者/外部观察者挂 `wait --follow` | 首个 `turn_failed` 仍 Publish/镜像（本卡 OOS 不抑制） | wait 消费者 | 首个零文本事件仍可能被看到（本卡不改变） | spec §10 后续项 → S4 登记 + 真机 #5 |

**闭环结论**：每条 spec 承诺行为五格齐、有归属；无「只活在接口、测试或无人认领格子里的承诺」。续接指令文本、`source_seq` 等内部细节不单独造闭环。

---

## 5. 缺陷族对抗审查（逐族正面回答）

覆盖面 = Ticket 0 的生产改动（proto/executor/adapters/orchestration/ledgerstep）+ S1–S5。逐族结论已嵌入各子卡验收栏，此处给跨子卡总答。

**1. 生命周期 / 状态机中断**
- 中途宿主重启：`awaitNode` 的单次续接**不做持久计数**，进程重启不自动重放已消费失败事件（spec §4.2 明确）；重启后由既有显式恢复/重新派发流程接管，无自动孤儿。任务不再被早退路径 `Done` 归档——这是本卡修的生命周期根因。
- 孤儿资源：本卡不创建 goroutine/进程/临时目录；`Continue` 复用既有能力，其回迁/恢复阶梯（`resumeForContinue`）是既有语义。
- 残余（真机观察）：跨 agentd 重启的计数与恢复未做（spec §10 OOS，S4 登记）；真实重启窗口行为**未验证，需真机**（#2）。

**2. 静默失败 / 误导报错**
- 传播契约：`awaitNode` 续接失败 fail-closed 返回错误 → `RunOnce` 转 `needs_human`；`FinishTask` 失败显式 Warn + `Reason="task 归档失败"`，不把「已移卡」伪装成「已归档」。
- 存在「报成功但没做」的窗口吗：**无**——`FinishTask` 只在全部收口动作后调用；未知/缺分类一律不重试（不回退到文案匹配）。
- 可行动性：所有早退落 `needs_human` 带 reason；日志含 task/seq/failure_class/attempt/continue 结果（S2 断言）。

**3. 跨平台假设**
- 生产改动是 Go 类型/JSON/内存状态机，不依赖路径、进程组、权限模型、webview。**无，因为**不引入平台相关调用。
- 边界面 `d_execution_adapters`/`d_gateway`/`d_transport` 对面是外部现实，真实供应商流中断/跨机续接**未验证，需真机**（§6 #1–#3）。

**4. 假红 / 假绿测试**
- 主判据是行为化的：`awaitNode` 计数/终态报文、`RunOnce` FinishTask 次数、e2e 的 `continue/done/archived` 计数——不是中途副产物。
- 反面断言在位：邻近分支不分类、无分类不续接、耗尽后不归档、`NoTrailerResult` 不分类、无分类键缺席。
- 假绿温床核查：S1 主体就是给「只在实现里恰好为真」的邻近分支补反例；S3 明确要求穿**真 HTTP/JSON 边界**而非包内 `Marshal`；夹具文案与 `FailReason` 解耦。负载/并发：本卡不引入并发原语；`-race` 在 S2/S3 跑。
- 测试锁的是**调用方依赖的行为**（`FailureClass` 字段、FinishTask 时机、状态门），换实现只要行为不变仍绿——**是**，未绑死私有写法。

**5. 门禁绕过**
- 新增写路径/执行路径：**无独立于既有门的新门**。自动续接复用 `Manager.Continue` 的 `waiting_review` 状态门；`RunOnce` 的写动作仍走 `gatedWrite`。权限判定未放松。
- 同一规则的所有入口共享同一道门：续接仅经 `ExecutionClient.Continue` 一条路（contract §7.1 冻结），无第二入口。检查与动作之间的窗口（TOCTOU）：`transit` 状态迁移是并发安全的既有机制；S3 e2e 验「非 `waiting_review` → 拒」。

**6. 序列化边界**
- 新增字段 `failure_class` 的产生→消费链：`Result.FailureClass`(adapter) → `NewFailedPayload` → `FailedPayload` JSON（`omitempty`）→ `proto.Event.Payload` → `/ws/events`/attach → `failedPayload` 解析 → `TurnEnd.FailureClass` → `awaitNode` 判定。每一处手写序列化点都在 contract §7.2 与 S3 清单内。
- 一条穿过真实序列化边界的回归：**S3 强制**（httptest 主回路 + roundtrip），用可空类型区分「字段缺失」与「值为零」（无分类键缺席断言 + additive 断言）。
- 两端各自有测试 ≠ 链路有测试：contract 只锁了各端单测；S3 补链路。

**7. 枚举新值过既有白名单**
- 新值 `zero_text` 流经的白名单逐处核对：`failureClassOf` 的 `event.Type == turn_failed` 门 + 精确 `== FailureClassZeroText` 判定（`wire.go`）、`awaitNode` 的 `==` 判定（`runner.go`）。**中间无第三处白名单挡死**；未知值在 `failureClassOf` 原样带出后由 `== zero_text` 精确匹配天然 fail-closed。
- adapter 侧邻近分支不得误登记 `zero_text`（S1 反例锁）。

**8. 承重安全属性有测试锁住**
- 三条：①一次 `RunOnce` 最多自动续接一次（`TestAwaitNodeContinuesAtMostOnce` + S3 e2e）；②无分类/未知值不重试（`TestAwaitNodeDoesNotContinueWithoutClass` + `TestWaitForTurnEndClassIsClosedAndTurnFailedOnly` + S1/S3）；③任何早退不归档（`TestNodeStepDoesNotFinishTaskOnParseFailure` + S2 早退矩阵 + S3 反例）。
- 每条都有**能变红**的测试（contract §7.2 三条变异 + S1/S2/S3 新增变异），不是只在实现里恰好为真。

**9. webview / 平台表现差异候选族**
- **无，因为**不触 `d_web`、Wails、Chromium、cookie、剪贴板或浏览器 API；S4 是纯文档、S5 是图工具链。

---

## 6. 真机清单（全部「未验证，需真机」，归协调者执行）

1. **真实供应商流中断**：真实 executor（runner/mimo、pro/cmd 等四起实录载体）回合零文本时，adapter 确实走明确零文本分支、节点自动续接一次并完成，不吵醒人（穿真实 adapter 进程 + agentd + client）。
2. **续接耗尽 / 外部竞争**：第二次仍零文本、续接失败或外部协调者抢先续接（409）时，任务保持 `waiting_review`、`needs_human` 可见；人工显式 `continue` 不再 409（跨真实 task 生命周期与运行锁）。
3. **旧 agentd / 旧 adapter 缺分类**：跨版本灰度或回滚时，缺 `failure_class` 的历史/旧事件不自动重试（fail-closed），旧 wire 形状兼容（需旧二进制或构造跨版本事件）。
4. **日志现场**：真实 agentd 日志能看到 task/seq/failure_class/attempt/continue 结果，且**不含**完整正文/凭据（S2 机内只锁了本地 slog 契约；真实日志轮转/级别下可见性需真机）。
5. **首个事件仍唤醒观察者**：本卡 OOS 未抑制首个 `turn_failed` 的 Publish/唤醒；`wait --follow` 仍可能看到首个零文本事件（确认本卡未意外改变唤醒面；后续抑制另开卡）。

---

## 7. 出稿自检

- [x] **产出四样齐全**：§1 子系统清单每个带 best.json 类型；§2 契约增量逐条有结论（§2.3 无退回、§2.4 四条澄清）；§3 子卡 S1–S5 四段式且判据行为化；§5 缺陷族逐族含「无，因为……」。
- [x] **「待拍板」岔口集中**：P1–P5 全在 §0，正文岔口回指。
- [x] **「未验证，需真机」汇总**：§6 五条。
- [x] **每张子卡有界文件集核过**：S1（五家 adapter 测试 + `turn` + `handleresult_notrailer`）、S2（`internal/ledgerstep/*_test.go`）、S3（`internal/orchestration`/`internal/agentd` 测试）、S4（`docs/roadmap.md`）、S5（`codegraph/*` + ledger）均已圈；无圈不出的子卡，未插竖切还债卡（理由见 §1.1）。
- [x] **行为闭环每行五格完整**：§4，归属存在；无无人认领格子。
- [x] **契约状态位**：spec「已批准」、contract「上游状态：已批准 + 冻结状态」实读；边界澄清已回写 `b402-contract.md` 修订记录（§2.4）。
- [x] **未亲自跑到结果的命令未写成结论**：build/vet/相关包测试/`codegraph check`/`--view check`/`validate`/`resolve` 均本轮实跑（§1.3、台账 5/6/8）；真实供应商/跨机行为一律标真机。
- [x] **收尾**：`codegraph resolve --doc docs/superpowers/specs/b402-breakdown.md` 亲跑（结果落台账 8）；坏锚即修。

---

## 8. 拍板记录区（协调者回填）

| 编号 | 裁决 | 理由 |
| --- | --- | --- |
| **P1** | 待回填 | |
| **P2** | 待回填 | |
| **P3** | 待回填 | |
| **P4** | 待回填 | |
| **P5** | 待回填 | |

（回填后头部状态行改为「已拍板（日期）」，并与裁决同批提交。）
