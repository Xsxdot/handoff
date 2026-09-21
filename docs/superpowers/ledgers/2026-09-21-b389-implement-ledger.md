# B389 implement 节点台账（T0–T7，2026-09-21）

产出物：第 2–7 片实现代码（`internal/ledger`、`internal/scheduling`、`internal/agentd`、
`internal/client`、`internal/proto`、`cmd`），契约 `docs/superpowers/specs/b389-contract.md`
的 §4 第 11、14–32 条落测。
工作树：`/root/.handoff/worktrees/e4d189cb`，分支 `cards/B389-charter-7`，起手 HEAD `75babc22`。
计划来源：`docs/superpowers/plans/b389-plan.md`（冻结接口）。

## 起手状态

```text
$ git status --short                → （空，工作树干净）
$ git branch --show-current          → cards/B389-charter-7
$ git log --oneline -1               → 75babc22 docs(B389): 第 2–7 片实现计划 + plan 节点台账
$ go build ./...                     → BUILD_EXIT=0
```

## 图查询记录（codegraph，只读）

```text
$ codegraph --repo . sym LaunchAdmit → n_scheduling_Service_LaunchAdmit（ok，scheduling.go:712）
$ codegraph --repo . sym AdmitSeatCarrier        → MISS
$ codegraph --repo . sym SetSeatBearing          → MISS
$ codegraph --repo . sym SeatBearingOf           → MISS
$ codegraph --repo . sym ClearSeat               → MISS
$ codegraph --repo . sym ReportSeatBearingMissing→ MISS
$ codegraph --repo . sym wakeCoordinatorRoundRaw → MISS
$ codegraph --repo . sym CoordinatorWake         → MISS
$ codegraph --repo . sym ClaimWake               → MISS
$ codegraph --repo . check                        → CHECK_EXIT=0（跨域边 ⊆ target.json 契约面）
```

图覆盖债（`codegraph sym` 退出码非 0）：`ClaimWake`、`SetSeatBearing`、`SeatBearingOf`、
`ClearSeat`、`AdmitSeatCarrier`、`CoordinatorWake`、`ReportSeatBearingMissing`、
`wakeCoordinatorRoundRaw` 均未被图覆盖；实现以源码与测试为准，未拿 chain 冒充流程。
`codegraph check` 退出 0，本轮未新增跨域依赖方向（新增符号均落在既有域内）。

## T0：基线修复（agentd 测试包编译）

起手复跑确认基线红（Ticket 0 机械补齐参数时漏两行导入）：

```text
$ go vet ./internal/agentd/
internal/agentd/scheddrain_test.go:271:78: undefined: ledger
internal/agentd/wakeconsumer_b358_test.go:204:106: undefined: ledger
internal/agentd/wakeconsumer_b358_test.go:232:106: undefined: ledger
internal/agentd/wakeconsumer_b358_test.go:291:98: undefined: ledger
internal/agentd/wakeconsumer_b358_test.go:294:129: undefined: ledger
```

在 `scheddrain_test.go` 与 `wakeconsumer_b358_test.go` 各补一行
`"github.com/Xsxdot/handoff/internal/ledger"` 导入后：

```text
$ go vet ./internal/agentd/    → VET_EXIT=0（无输出）
```

## T1（第 2 片）：承载记录接进唤醒 + AdmitSeatCarrier + 修复命令

先做 plan §（g）承载夹具迁移（7 处 `test-carrier`→`coord-carrier`，`Machine` 保持 `local`）：

```text
$ go test ./internal/agentd/ -run 'TestB3583|TestAutomation|TestB349|TestB369|TestB370|TestAutomationWake|TestDrainQueues' -count=1
ok  github.com/Xsxdot/handoff/internal/agentd  10.340s
```

红绿：

- `internal/scheduling/admit_seat_carrier_test.go` 新建，`AdmitSeatCarrier` 未实现 →
  `undefined: ... AdmitSeatCarrier`（编译红，证明符号缺席）；实现后
  `go test ./internal/scheduling/ -run TestAdmitSeatCarrier -count=1` → `ok ... 0.370s`。
- `internal/ledger/bearing_test.go` 追加 `TestSetSeatBearingRepairsMissingRecord`：
  实现前 `s.SetSeatBearing undefined`（编译红），实现后
  `go test ./internal/ledger/ -run TestSetSeatBearing -count=1` → `ok ... 0.120s`。
- `internal/agentd/scheddrain_test.go` 追加 `TestB389WakeUsesBearingForSpec`、
  `TestB389WakeWithoutBearingFailsExplicitly`（含 `bearingTraceRunner` 记录
  `SessionRef`）：实现前 `env.srv.wakeCoordinatorRoundRaw undefined`（编译红），
  实现后两条绿。
- `cmd/card_bearing.go` 新增 `card seat bearing set`；`cmd/card_bearing_test.go`
  两条命令级测试（真 root command + 假 agentd `/api/squads` + 真 SQLite）：

```text
$ go test ./cmd/ -run TestCardSeatBearing -count=1     → ok  github.com/Xsxdot/handoff/cmd  0.325s
```

## T2（第 3 片）：判据收口与终态

红绿：

- `TestB353AutomationMapsCardActionEvents` 先改期望取红：
  `processed=4 ... want 3`；实现 `automationWakeEvent` 收口后绿。
- 新增 `TestB389NeedsHumanDoesNotWake`（resumes=0、processed=0）、
  `TestB389TerminalCardDoesNotWake`（MoveCard→已完成 保席位，零 resume、零名额）：
  两条绿。终态闸变异自验（把 gate 条件改成永假）：

```text
--- FAIL: TestB389TerminalCardDoesNotWake   （变异生效，测试有牙）
恢复后 → ok
```

- `TestCloseCardClearsSeatAndBearing`（§4-11）：实现 `clearSeatTx` + `CloseCard`
  同事务清座。变异自验（删掉 `CloseCard` 内 `clearSeatTx` 调用，命中唯一）：

```text
--- FAIL: TestCloseCardClearsSeatAndBearing  （终态后席位非空）
恢复后 → ok
```

- 回归两条展示通路：`go test ./cmd/ -run TestCardWait -count=1` →
  `ok ... 6.556s`；`go test ./internal/collab/ -run TestSessionTimelineNeedsHuman -count=1`
  → `ok ... 0.159s`。新增 §4-19/20 回归钉
  `TestB389NeedsHumanStillActionableInCardWait`、`TestB389SessionListNeedsHumanTagKept`

## T3（第 4 片）：认领接线、归属解析、缺承载显式路径

- `internal/ledger/events.go` 新增 `ReportSeatBearingMissing`（检查+追加同事务，幂等）。
- `internal/agentd/wakeconsumer.go` 新增 `wakeClaimHolder`/`claimWakeBatch`/
  `completeWakeBatch`/`advanceAutomationCursorWatermark`，消费循环改为「先认领、
  拿到的才处理、处理后收尾」，游标改终局前缀水位。
- `scheddrain.go` 缺承载分支改为 `reportSeatBearingMissing`（落恰一条
  `EvSeatBearingMissing` + `MarkNeedsHuman` 展示，返回 nil 跳过）。

红绿与自验：

```text
$ go test ./internal/agentd/ -run 'TestB389ClaimExcludesOtherMachine|TestB389LocalWakeCompletesClaim|TestB389MissingBearingSkipsWithoutSlots|TestB389SeatBearingMissingIdempotent' -count=1
ok  github.com/Xsxdot/handoff/internal/agentd
```

既有 `TestB349AutomationCursorPersistence/save_failure` 语义按 §3.1 更新：事件已
CompleteWake 后即使 cursor 未落盘也不得重放（认领表是 exactly-once 权威）——
processed 1→0、resumes 2→1，`ok`。

## T4（第 5 片）：转交面

- `internal/proto/ledger.go` 新增 `CoordinatorWakeReq`/`CoordinatorWakeResp`（内嵌
  `CoordinatorLaunchResp`）；`internal/client/coordinator.go` 新增 `CoordinatorWake`；
  `internal/agentd/coordapi.go` 新增 `POST /api/cards/{id}/coordinator/wake` +
  `handleCoordWake`（CAS 409、只回执）；`scheddrain.go` 新增
  `transferCoordinatorWake`/`localMachineName`，远端分支由占位出口替换为转交
  （`raws==nil` 的 queue_release 合成唤醒显式失败，D4）。

红绿：

```text
$ go test ./internal/proto/ -run TestCoordinatorWake -count=1    → ok
$ go test ./internal/client/ -run TestCoordinatorWake -count=1   → ok
$ go test ./internal/agentd/ -run TestB389WakeEndpoint -count=1  → ok（3 条）
$ go test ./internal/agentd/ -run 'TestB389TransferPostsOnceWithBatchSeqs|TestB389RemoteBearing' -count=1 → ok
```

变异自验（远端分支条件改 `if false`，命中唯一）：`TestB389TransferPostsOnceWithBatchSeqs`、
`TestB389RemoteBearingDoesNotRunLocally` 变红；恢复后绿。

## T5（第 6 片）：冻结载体准入路由矩阵

```text
$ go test ./internal/agentd/ -run TestB389WakeUsesFrozenCarrierNotLaunchAdmit -count=1 → ok
```

（`TestAdmitSeatCarrierRejectsNonMember`/`RejectsExecutorSquad` 已随 T1 落。）

## T6（第 7 片）：不堵流与退避

- `server.go` 新增 `automationBackoff map[string]wakeBackoff`；`wakeconsumer.go`
  新增 `wakeBackoff`/`maxSeqOf`/`shouldSkipByBackoff`/`recordWakeBackoff` 与三个
  常量，失败分支由 `return` 改 `continue` + 退避，轮末统一收集首个错误。

红绿与变异自验：

```text
$ go test ./internal/agentd/ -run 'TestB389WakeFailureDoesNotBlockSiblingCard|TestB389WakeBackoffSkipsSameSeqNextRound' -count=1 → ok
变异①（删退避闸）：TestB389WakeBackoffSkipsSameSeqNextRound 红
变异②（失败改回 return）：TestB389WakeFailureDoesNotBlockSiblingCard 红
恢复后 → ok
```

## 收口命令（原始输出）

```text
$ gofmt -l internal cmd                       → （空）
$ go build ./...                              → BUILD_OK
$ go vet ./...                                → VET_OK
$ git diff --check                            → DIFFCHECK_OK
$ codegraph --repo . check                    → CHECK_EXIT=0
$ go test ./... -count=1                       → FULL_TEST_EXIT=0
    ok  github.com/Xsxdot/handoff/internal/agentd   262.786s
    ok  github.com/Xsxdot/handoff/cmd               135.238s
    （其余全部 ok；无 FAIL）
```

## 显式欠账（不静默）

- `codegraph/target.json` 未追加本卡条目（同契约节点欠账）：本轮只改既有边的语义，
  未新增跨域依赖方向，`codegraph check` 退出 0；新符号入图由合并前视图 diff 补齐。
- D4：`drainIgnitionRequest` 的合成 `WakeQueueRelease` 无 `card_events` 行，远端
  归属时返回指路错误并由 `requeueAutomation` 回填队列；单机行为不变。
- 真机验收链（spec §4.3：`@B382` → 认领 → 路由 linux-01 → `cmd` 载体 resume）由
  协调者执行，本节点未跑，**未验证**。

## 收尾自审

- 每条错误分支带上下文日志？是——`AdmitSeatCarrier`/`SetSeatBearing`/`ReportSeatBearingMissing`/
  `claimWakeBatch`/`completeWakeBatch`/`recordWakeBackoff`/`transferCoordinatorWake`/
  `handleCoordWake`/CLI 均有入口+结果+失败上下文日志；成功路径不静默。
- 新文件有头注释、导出函数有文档注释？是——`cmd/card_bearing.go` 头注释 + 边界；
  `AdmitSeatCarrier`/`SetSeatBearing`/`ReportSeatBearingMissing` 等导出函数含参数/
  返回/事务边界/为什么。
- 触及包测试绿、全量编译过？是（见收口命令）。
- 与 plan 的 Interfaces 签名一致？是——逐字对齐 §2.1 表。

## 提交事实（历史读数）

```text
$ git add -A
$ git commit -m "feat(B389): 协调者承载记录与唤醒跨机路由（第 2–7 片）…"
[cards/B389-charter-7 2788e594] feat(B389): 协调者承载记录与唤醒跨机路由（第 2–7 片）
 27 files changed, 2227 insertions(+), 76 deletions(-)
$ git rev-parse HEAD
2788e594861fffbf792837aa4c5fb9256c4aceb8
$ git status --short --branch
## cards/B389-charter-7
```

本提交即本节点产出物；随后的 amend 只把这段读数收进同一提交（amend 换 hash 属
git 事实，收口判据是工作树干净，不是文件内 hash 等于 HEAD）。
