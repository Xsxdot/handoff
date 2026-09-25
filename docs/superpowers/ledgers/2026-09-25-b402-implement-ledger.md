# B402 实现轮台账（charter-7 · 执行者亲跑）

> 卡 B402 · 分支 `cards/B402-charter-7` · 起手 HEAD `07ae00ef`（plan 节点产物）
> plan `docs/superpowers/plans/b402-plan.md`；本台账只记**亲跑到结果**的命令与原始输出。
> 约定：临时输出走 `$TMPDIR`（=`/root/.handoff/tmp/628aefdc`）或 stdout，不写 `/tmp`。

---

## T0 门禁（2026-09-25 18:42 起，go1.26.1 linux/amd64）

| # | 命令 | 读数 |
|---|---|---|
| 1 | `go build ./...` | `BUILD_EXIT=0` |
| 2 | `go vet ./...` | `VET_EXIT=0` |
| 3 | `go test ./internal/ledgerstep/ ./internal/proto/ ./internal/executor/... ./internal/orchestration/ -count=1` | `TEST_EXIT=0`；ledgerstep 16.543s、proto 0.016s、executor 0.206s、agy 0.307s、claudecode 4.093s、codex 6.012s、fake 0.004s、grok 1.413s、opencode 21.150s、rawtap 0.005s、turn 0.104s、orchestration 59.002s（全 ok） |
| 4 | `codegraph --repo . check` | `CHECK_EXIT=0`（`viewContainers=338`、`crossDomainEdges=1254`、`misplacedSkipped=0`） |
| 5 | `codegraph --repo . --view cards-B402-charter check` | `VIEW_CHECK_EXIT=0`（同上三数） |
| 6 | `codegraph --repo . validate` | `VALIDATE_EXIT=1`；`issues` 恰为两条既存：`[cards-B272-charter] 新增容器 k_dropdir_fn 引用不存在的基线领域 d_coordination_api`、`[cards-B374-charter] 新增容器 k_collab_model 已存在于基线，containersAdded 只接受新容器`；`nodes: 5315`。**无本卡新增 issue** |
| 7 | `grep -n "FailureClass" internal/proto/failure.go internal/executor/executor.go internal/orchestration/contracts.go internal/orchestration/manager.go internal/ledgerstep/wire.go internal/ledgerstep/runner.go` | 命中非空，与 plan §R1 行号表一致（`failure.go:15/:23`、`executor.go:96`、`contracts.go:142/:163`、`manager.go:3498`、`wire.go:44/:50/:55/:126`、`runner.go:450/:465`） |
| 8 | `grep -n "FailureClass: proto.FailureClassZeroText" internal/executor/{opencode,codex,grok,claudecode,agy}/adapter.go` | 恰 5 处：`opencode:2232`、`codex:912`、`grok:679`、`claudecode:893`、`agy:691` |
| 9 | `grep -n "defer.*FinishTask" internal/ledgerstep/node.go` | 无命中（`GREP_EXIT=1`）；显式调用在 `node.go:486-487` |

**T0 结论：九条全符预期，Ticket 0 冻结实现在场且绿，进入 T1–T5。**

---

## 事件记录（逐条追加）

- 2026-09-25 18:42 起手：工作树干净，HEAD `07ae00ef`，分支 `cards/B402-charter-7`。

## T1（2026-09-25 18:45–18:52）

改动（**仅测试，生产零改动**）：`turn/fallback_test.go` +1 用例、`codex/grok/claudecode/agy fallback_verdict_test.go` 各 +1 断言、`opencode/adapter_test.go` +2 断言、`orchestration/handleresult_notrailer_test.go` +1 用例（import 增 `context`）。合计 +44 行，全在 `*_test.go`。

`gofmt -l <7 个改动文件>` → 无输出（FMT_EXIT=0）。

`go test ./internal/executor/turn/ ./internal/executor/codex/ ./internal/executor/grok/ ./internal/executor/claudecode/ ./internal/executor/agy/ ./internal/executor/opencode/ ./internal/orchestration/ -count=1` → `T1_EXIT=0`（7 包全 ok；turn 0.151s、codex 6.027s、grok 1.364s、claudecode 3.871s、agy 0.347s、opencode 21.182s、orchestration 58.921s）。

**变异复验（6 发，全部先验编译再验行为；跑完逐个精确反向补丁还原）**：

1. `turn/fallback.go` `NoTrailerResult` 加 `FailureClass: "zero_text"`（锚点 `grep -c`=1）→ `BUILD_OK` → `--- FAIL: TestNoTrailerResultCarriesNoFailureClass`（`FailureClass="zero_text"`）。
2. 同上变异下扩跑四家 adapter → 全红：codex `fallback_verdict_test.go:126`、grok `:134`、claudecode `:140`、agy `:88`，均报「无 trailer 有新提交不得分类」。
3. 同上变异下 `opencode -run 'TestIdleFallbackNoTrailer|TestServeDeathEmitsFailed'` → `--- FAIL: TestIdleFallbackNoTrailer/with_new_commit`（`adapter_test.go:630`）。
4. `opencode/adapter.go` serve 死亡分支加 `FailureClass: "zero_text"`（`grep -c`=1）→ `BUILD_OK` → `--- FAIL: TestServeDeathEmitsFailed`（`adapter_test.go:1075`）。
5. `orchestration/manager.go:1808` failed 终态第 4 参改 `proto.FailureClassZeroText`（`grep -c`=1）→ `BUILD_OK` → `--- FAIL: TestFailedEventTerminalCarriesNoFailureClass`，原始报文 `{"fail_reason":"协调者主动中止（handoff stop）","failure_class":"zero_text",...}`。
6. `codex/adapter.go` 零文本分支删 `FailureClass:` 赋值（`grep -c`=1）→ `BUILD_OK` → 正例变红 `fallback_verdict_test.go:161: 明确零文本分支必须写 failure_class=zero_text，实际 ""`（正例护栏仍有牙）。

> 过程记录：变异还原最初尝试 `git checkout <生产文件>` 被协调者权限门拒绝（理由：可能覆盖冻结实现）。改用编辑器精确反向补丁还原，`git diff` 证实 4 个生产文件与 HEAD 无差异，工作树只剩 7 个测试文件 +44 行。

## T2（2026-09-25 18:54–19:00）

改动（**仅 `internal/ledgerstep/b402_retry_test.go`，生产零改动**）：+3 用例
1. `TestAwaitNodeSecondTerminalOrdinaryFailDoesNotContinue`（第二终态普通 turn_failed：不二次续接、返回第二终态报文）。
2. `TestNodeStepEarlyExitsNeverFinishTask`（9 行早退矩阵：await 错误/解析失败/review diff 失败/产出 diff 失败/缺产出物/写闸拒绝/publish 失败/route 失败/pass 对照；每行独立断言 Action、Reason、FinishTask 次数）。
3. `TestAwaitNodeZeroTextLogsContext`（日志含 `task=t`/`seq=7`/`failure_class=zero_text`，且不含完整正文）。
import 增 `bytes`、`log/slog`、`strings`；`gofmt -l` 无输出。

- 局部跑：`go test ./internal/ledgerstep/ -run '<三个新用例>' -count=1 -v` → PASS（矩阵 9 个子测试全 PASS，`pass_control` 行 FinishTask=1 证明不是恒 0 假绿）。
- `go test ./internal/ledgerstep/ -count=1` → `ok 16.406s`，EXIT=0。
- `go test -race ./internal/ledgerstep/ -count=1` → 见下方补记（亲跑读数）。

**变异复验（3 组，全部先 `go build` 再验行为，跑完精确反向补丁还原）**：

1. `node.go` 在 `logger.Info("已派发",…)` 后插「无条件 `defer FinishTask`」（模拟改回旧的无条件 defer；锚 `grep -c`=1）→ `BUILD_OK` → 矩阵 **9 行全红**（含 `pass_control`：FinishTask 变 2 次）。
2. `runner.go:463-465` 删 `failure_class` 日志键（锚 `grep -c`=1）→ `BUILD_OK` → **测试仍绿（变异存活）**。排查：`wire.go:122` 的 `waitForTurnEnd` 也打 `failure_class=zero_text`，同一字段类别有第二个来源——**plan 预设的单一变异点不足以让断言变红**。追加第二发（同时删 `wire.go:122` 的 `failure_class`，锚 `grep -c`=1）→ `BUILD_OK` → `--- FAIL: TestAwaitNodeZeroTextLogsContext`（`日志缺 "failure_class=zero_text"`，并打印了完整日志原文）。两发均已还原。
3. 二次续接方向：(a) 只把 `runner.go:450` 的 `&& end.FailureClass == proto.FailureClassZeroText` 去掉 → `BUILD_OK` → 我的第二终态用例**仍绿**（原因是当前实现是单次 `if` 而非循环，「不二次续接」是结构性的；该发同时让既有的 `TestAwaitNodeDoesNotContinueWithoutClass` 变红，证明变异生效而非没打中）；(b) 把该 `if` 改成 `for end.EventType == proto.EventTypeTurnFailed {`（模拟循环续接、丢分类闸）→ `BUILD_OK` → `--- FAIL: TestAwaitNodeSecondTerminalOrdinaryFailDoesNotContinue` 与 `--- FAIL: TestAwaitNodeContinuesAtMostOnce`。

> 结论：新断言有牙（1/2b/3b 三发红证）；3a 的存活是实现结构使然，已在台账如实记为「该发不红」并给出替代红证，未把它写成通过。

## T3（2026-09-25 19:05–19:20）

新增 `internal/orchestration/b402_e2e_test.go`（`package orchestration_test`，**生产零改动**）：
- `newB402Env(t, script)`：照抄 `newHTTPEnv` 装配（真 store/Hub/Manager/agentd.Server），唯一差别是 `fake.New(script)` 带初始脚本；返回 `*httpEnv` 以复用 `doReq`/`initRepo`/`gitAt`。
- `b402RegisterProject`：照抄 `manager_test.go` 的 `registerTestProject`（origin 由仓库路径派生）。
- 三个用例：
  1. `TestB402E2EZeroTextContinueRoundtrip`——主回路（turn_failed payload 精确含 `"failure_class":"zero_text"` 且 `FailedPayload` 解码相等、FailReason 脱敏原文透传、状态 waiting_review）→ Add 第二步 → `/continue` 200 且 `fake.Sends()` 对应本任务恰 1 条非空且文案为「继续」→ 轮询 completed 且 `final_text` 含 handoff-verdict → `/done` 200 且终态 completed；另造 running 任务同请求 `/continue` → **409**。
  2. `TestB402E2EZeroTextTwiceStaysReviewable`——反例：第二条 turn_failed 后状态非 completed/failed、无 completed 事件；再次 `/continue` 非 404、报文不含「归档」。
  3. `TestB402E2EWireOmitsEmptyFailureClass`——未分类失败的 wire payload **不出现** `failure_class` 键。
- 关键顺序事实（读 `fake.go#nextStep` 得到）：**Add 必须在 `/continue` 之前**——`Send` 是非阻塞投递（`select…default` 丢弃），先 continue 再 Add 会永久卡住。

亲跑：
- `gofmt -l b402_e2e_test.go` → 无输出；`go vet ./internal/orchestration/` → `VET_EXIT=0`。
- `go test ./internal/orchestration/ -run 'TestB402E2E' -count=1 -v` → 三用例全 PASS（`0.739s`），`E2E_EXIT=0`。
- `go test -race ./internal/orchestration/ -run 'TestB402E2E' -count=1` → `ok 1.929s`，`RACE_EXIT=0`。

**变异复验（3 发）**：

1. **plan 预设点不成立（如实记）**：`ledgerstep/wire.go#failureClassOf` 改恒返回 `""`（末行 `return payload.FailureClass` → `return ""`，编译过）→ e2e 主回路**仍绿**。排查：`failureClassOf` 属等待层（`waitForTurnEnd` 判分类），HTTP e2e 不经过它；断言 1 锁的是 `handleResult → FailedPayload` 的 wire 字段。**该发不算 e2e 的红证**。同发下改跑 `go test ./internal/ledgerstep/ -run 'ZeroText|TurnEnd|FailureClass'` → 三红：`TestAwaitNodeAutoContinuesZeroTextOnce`、`TestAwaitNodeZeroTextLogsContext`、`TestWaitForTurnEndCarriesZeroTextClass`——该符号的牙在 ledgerstep 包内（既有用例 + T2 新用例）。已还原。
2. **e2e 断言 1 的真变异点**：`manager.go:3498` 的 `NewFailedPayload(..., r.FailureClass)` 末参改 `""`（`grep -c`=1）→ `BUILD_OK` → `--- FAIL: TestB402E2EZeroTextContinueRoundtrip`：`turn_failed wire payload 缺 failure_class=zero_text: {"fail_reason":"供应商文案随便写","proc_usage":{...}}`。已还原。
3. **e2e 断言 2**：`Manager.Continue` 状态门放宽为也接受 `running`（锚含 `continue 状态不允许` 唯一化，`grep -c`=1——裸 `if cur.State != …` 在 manager.go 有 2 处，另一处是 `Done` 门，故加下一行上下文）→ `BUILD_OK` → `--- FAIL`：`running 上 continue 应 409，实得 200 {"ok":true}`。已还原。

> 还原核对：`git diff --name-only | grep -v _test.go` → 无非测试文件改动。

## T4（2026-09-25 19:22）

`docs/roadmap.md` 末尾追加「## 来自 B402 spec（2026-09-25，本期不做、后续要做）」小节，三条逐字取自 plan §T4（唤醒抑制/retry ownership、跨 agentd 重启/自动续接计数、供应商流中断根因）。

判据实跑：
- `grep -n 'B402' docs/roadmap.md` → 命中 `:864`（小节标题）与 `:866/:867/:868`（三条），`GREP_EXIT=0`（此前为 1）。
- 关键词计数：`唤醒抑制`=1、`retry ownership`=1、`跨 agentd 重启`=1、`自动续接计数`=1、`供应商流中断`=1。
- `git status --short`：本 task 只多出 `M docs/roadmap.md`，未触 `internal/**`。

## T5（2026-09-25 19:24）codegraph 逐符号对账（选 **(乙)**：保持最小增量）

三闸实跑：

| 命令 | 读数 |
|---|---|
| `codegraph --repo . check` | `CHECK_EXIT=0` |
| `codegraph --repo . --view cards-B402-charter check` | `VIEW_CHECK_EXIT=0` |
| `codegraph --repo . validate` | `VALIDATE_EXIT=1`；`issues` 恰为 B272/B374 两条既存，`nodes: 5315`，**无本卡新增 issue** |
| `codegraph --repo . resolve --doc docs/superpowers/plans/b402-plan.md` | `RESOLVE_PLAN_EXIT=0`（坏锚 0；`wire.go#failureClassOf` 报 `anchor=moved` 但解析成功） |
| `codegraph --repo . resolve --doc docs/superpowers/specs/b402-contract.md` | `RESOLVE_CONTRACT_EXIT=0` |

逐符号 `--view cards-B402-charter sym` 读数：

| 符号 | id | anchor | status |
|---|---|---|---|
| `FailureClass` | `m_proto_FailureClass` | ok | **added** |
| `TurnEnd` | `m_ledgerstep_TurnEnd` | ok | **added** |
| `failureClassOf` | `n_ledgerstep_failureClassOf` | ok | **added** |
| `awaitNode` | `n_ledgerstep_StepRunner_awaitNode` | ok | **modified** |
| `NodeStep.RunOnce` | `n_ledgerstep_NodeStep_RunOnce` | ok | **无 status 键（=None，触碰未登记——扫描欠账）** |
| `Manager.handleResult` | `n_orchestration_Manager_handleResult` | **moved** | 无 status 键（同上欠账；anchor 从 plan 记的 ok 漂到 moved，以本次实测为准） |
| `Manager.Continue` | `n_orchestration_Manager_Continue` | **moved** | 无 status 键（同上欠账） |
| `NoTrailerResult` | `n_turn_NoTrailerResult` | ok | 无 status 键（基线节点，本卡未改其体） |
| `ZeroTextContinueInstruction` | — | **不在图中**（`近似候选: []`） | 图覆盖债，已回落普通文件路径 `runner.go:416`，**未以 grep 冒充图覆盖** |

**裁定 (乙)**：不改 `codegraph/diffs/cards-B402-charter.json`。理由：`nodesModified` 要求补录「修改后的完整 Node」（signature/signatureOld/params/returns/summary/tests 全字段，recipe §摘要抓取红线要求逐字转录源码 doc 注释），本卡该符号的源码改动属 contract Ticket 0 已冻结面，手工补录有把摘要写成生成式概括的红线风险，且 P3=甲 明确不做全量重扫、不搅既存他卡图债。欠账逐符号记于上表 + 本台账，**absorb 前需重扫**。
新增测试文件（`b402_e2e_test.go` 等）不在图（图不收测试节点），一并记为扫描欠账，不冒充图覆盖。

## 收尾自审（2026-09-25 19:30）

亲跑读数：

| 命令 | 读数 |
|---|---|
| `go build ./...` | `BUILD_EXIT=0` |
| `go vet ./...` | `VET_EXIT=0` |
| `go test ./internal/ledgerstep/ ./internal/orchestration/ ./internal/executor/... ./internal/proto/ -count=1` | `TOUCHED_EXIT=0`（12 包全 ok；ledgerstep 17.849s、orchestration 59.870s、opencode 21.092s…） |
| `go test ./... -count=1`（全量，卡级集成口径） | `FULL_EXIT=0`（过滤 `ok`/`no test files` 后无任何输出 = 无 FAIL） |
| `go test -race ./internal/ledgerstep/ -count=1` | `RACE_EXIT=0`（ok 21.993s） |
| `go test -race ./internal/orchestration/ -run TestB402E2E -count=1` | `RACE_EXIT=0`（ok 1.929s） |
| `gofmt -l <全部改动 Go 文件>` | 无输出 |

自审四问：
1. **错误分支日志**：本卡零生产改动；零文本路径的关键日志（`task`/`seq`/`failure_class`/续接结果）由 `TestAwaitNodeZeroTextLogsContext` 断言在场（并有红证）。不适用项：T0/T4/T5 无运行时代码。
2. **新文件头注释 / 导出符号文档**：`b402_e2e_test.go` 有文件头（职责 + 为何只在外部测试包 + 边界）；新增均为非导出测试助手与测试函数，逐个带职责注释。无新增导出 Go 符号。
3. **触及包测试绿、全量编译过**：见上表（build/vet/触及包/全量四绿 + 两处 -race 绿）。
4. **与 plan Interfaces 一致**：Consumes 全部为 §5 既有符号；Produces 落在 plan §5 清单内。**偏差三条（均为 plan 行文与实现类型名不符，非越界）**：
   - plan 写 `ledger.Output{Kind,Path}`，实际类型是 **`ledger.NodeOutput`**（`internal/ledger/types.go:265` `Produces *NodeOutput`）；按真实类型写，未改生产。
   - plan T2 早退矩阵「行 6 写闸拒绝」的首个 `gatedWrite` 落点：普通 implement 路径首个是 `裁决落账`（node.go:349），断言 `errors.Is(err, ErrWriteGateClosed)` 成立，无需 review purpose。
   - plan T3 计划一个 `TestB402E2E*`；实落 **三个** 同前缀用例（主回路 / 反例 / 空分类 wire），`-run TestB402E2E` 覆盖不变。
5. **变异还原核对**：`git diff --name-only | grep -v _test.go` → 无非测试文件改动（唯一非测试改动是 `docs/roadmap.md` 与本台账）。

## 提交事实（历史读数，amend 前）

```
$ git add docs/roadmap.md docs/superpowers/ledgers/2026-09-25-b402-implement-ledger.md \
    internal/executor/turn/fallback_test.go internal/executor/codex/fallback_verdict_test.go \
    internal/executor/grok/fallback_verdict_test.go internal/executor/claudecode/fallback_verdict_test.go \
    internal/executor/agy/fallback_verdict_test.go internal/executor/opencode/adapter_test.go \
    internal/orchestration/handleresult_notrailer_test.go internal/orchestration/b402_e2e_test.go \
    internal/ledgerstep/b402_retry_test.go
$ git commit -m "test(B402): 零文本分类邻近反例 + awaitNode/RunOnce 早退矩阵与日志断言 + 跨进程 e2e + roadmap/图对账"
7e98d0ac test(B402): 零文本分类邻近反例 + awaitNode/RunOnce 早退矩阵与日志断言 + 跨进程 e2e + roadmap/图对账
$ git log --oneline -3
7e98d0ac test(B402): …
07ae00ef plan(B402): …
2fb6a9d7 breakdown(B402): …
$ git rev-parse HEAD
7e98d0acb36c07d3f1a04f246fab78aabc54ddbb
```

> 本行之后按纪律 amend 一次收进同批提交：amend 会换 hash（git 的事实），不把 amend 后的 HEAD 回写台账再 chase；收口判据是**工作树干净**。
