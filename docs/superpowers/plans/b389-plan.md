# B389 实现计划：协调者席位的承载记录与唤醒跨机路由（第 2–7 片）

## 0. 执行边界与已拍板裁决

本计划以 `docs/superpowers/specs/b389-contract.md`（已批准，32 条原子断言 + 4 项
拍板记录）为冻结接口，以 `docs/superpowers/specs/b389.md` 为范围与验收来源。实现
只在当前分支 `cards/B389-charter-6` 完成，不切分支、不改 git 配置、不 push。

**本计划不重造契约内容**。契约已把七个切片与欠账写死在 §5/§7：第 1 片（认领原语 +
终局前缀水位，提交 `4747a7a1`）与 Ticket 0（`seat_bearings`/`wake_claims` 两方言
DDL、`BindSeat`/`RebindSeat` 承载参数与同事务落盘、`SeatBearingOf`/`ClearSeat`、
金样本 `internal/ledger/bearing_test.go`，提交 `734caefc`）已落地。本计划只做第 2–7
片的实现排序与代码级展开。

契约逐字命名的切片号只有三个：第 1 片（§5）、第 2 片「承载记录接进拉起/唤醒与修复
命令」（spec §5 末句）、第 3 片「判据收口与终态」（§5 第 11 条归属）。第 4–7 片的
内容由 §5 欠账组（21–22、23–29、30–31）与 spec §3 的领域枚举确定；本计划的编号
即排序，断言归属见 §2.4 覆盖矩阵，不静默漏项。

开头固定本回合已落卡的三个决策，后续任务不得把它们当成待拍板项：

- **D1 第 2 片引入 `AdmitSeatCarrier`。** 「后续唤醒不重选载体」与「从承载记录构造
  spec」是同一动作的两面：`LaunchAdmit` 会做候选遍历并返回它自己挑中的载体，与
  `bearing.Carrier` 可能不一致，两者拼出来的 `Binding`/`SessionSpec` 自相矛盾。
  故第 2 片必须同时落 §3.4 的冻结载体准入（新增方法，非重复建设，理由见契约 §3.4.3）。
- **D2 第 3 片只清 `CloseCard` 的近端终态，不改 `MoveCard`。** 契约 §1 表格只把
  `CloseCard` 列为「终态转移在同一事务内清席位与承载」；`MoveCard`→`已完成` 的席位
  残留由唤醒路径的终态卡闸（§3.2 第 1 步）兜住。这是显式边界，不是静默缺项。
- **D3 认领只能在消费循环做，不在 `wakeCoordinatorRound` 做。** 因为转交要带上本批
  `seq` 集合（§4-25），而认领者（转交方）必须在转交完成后 `CompleteWake` 收尾
  （§3.3.4）；对端执行时不再认领。认领与收尾因此都落在 `consumeAutomationEventsOnce`
  的批次边界上，`wakeCoordinatorRound` 只负责归属判定与执行。
- **D4 队列出队的合成唤醒不转交（显式边界 + 欠账）。** `drainIgnitionRequest`
  （`scheddrain.go:171`）用合成 `keystone.WakeEvent{Kind: WakeQueueRelease}`
  直接调 `wakeCoordinatorRound`，它没有对应的 `card_events` 行；冻结的
  `CoordinatorWakeReq.Events` 只装 `[]proto.LedgerEvent`，装不下它。因此
  `wakeCoordinatorRoundRaw` 在 `raws == nil` 且承载在远端时，返回带指路文案的错误
  （而非转交），`drainIgnitionRequest` 走既有 `requeueAutomation` 回填队列并留
  「需要人」痕迹。这是**显式声明的边界**，记入 §2.4 欠账，不由实现者自由发挥；
  单机（承载在本机）的队列出队行为完全不变。真实外部唤醒（`@卡号` 消息、
  `task_mirrored` 等）都有 `card_events` 行，转交面完整覆盖，spec §4.3 的真机验收链
  不经过 queue_release。

本节点只提交本计划及同批台账，不实现运行时代码。后续实现者按任务顺序执行；每个任务
的步骤是一个 2–5 分钟的动作，步骤中的命令、原始结果和判断追加到
`docs/superpowers/ledgers/2026-09-21-b389-plan-ledger.md`。

## 1. 基线、图证据与可执行判据

### 1.1 已在基线真实运行的命令与结果

工作树 `cards/B389-charter-6` @ `552664c0`（已含 `origin/cards/B233.1-charter-7` 的
合并提交）。本节点亲自跑过：

```text
$ go build ./...                                              → exit 0
$ go vet ./internal/ledger/ ./internal/scheduling/ ./internal/keystone/ → （无输出，exit 0）
$ gofmt -l internal cmd                                       → （空）
$ go test ./internal/ledger/... -count=1
    ok  github.com/Xsxdot/handoff/internal/ledger      22.937s
    ok  github.com/Xsxdot/handoff/internal/ledger/api   1.240s
$ go test ./internal/scheduling/... ./internal/keystone/... -count=1
    ok  github.com/Xsxdot/handoff/internal/scheduling           6.861s
    ok  github.com/Xsxdot/handoff/internal/scheduling/internal/logging 0.002s
    ok  github.com/Xsxdot/handoff/internal/keystone             0.197s
$ go test ./internal/collab/... ./internal/ledgerstep/... -count=1
    ok  github.com/Xsxdot/handoff/internal/collab       8.986s
    ok  github.com/Xsxdot/handoff/internal/collab/room  0.002s
    ok  github.com/Xsxdot/handoff/internal/ledgerstep  15.421s
$ go test ./cmd/... -count=1
    ok  github.com/Xsxdot/handoff/cmd                   65.291s
$ go test ./internal/ledger/ -run 'TestSeatBearing|TestClaimWake|TestCompleteWake|TestCursorWatermark|TestDDLDialectParity' -count=1
    ok  github.com/Xsxdot/handoff/internal/ledger       0.971s
```

### 1.2 基线缺陷（实现前必须先修）

`go test ./internal/agentd/... -count=1` **编译失败**，与 B389 的 Ticket 0 提交
`734caefc` 直接相关——那次机械补齐 `BindSeat`/`RebindSeat` 参数时，往两个测试文件
插入了 `ledger.SeatBearing{...}` 字面量，却没有加 `internal/ledger` 导入：

```text
$ go test ./internal/agentd/... -count=1
# github.com/Xsxdot/handoff/internal/agentd [github.com/Xsxdot/handoff/internal/agentd.test]
internal/agentd/scheddrain_test.go:271:78: undefined: ledger
internal/agentd/wakeconsumer_b358_test.go:204:106: undefined: ledger
internal/agentd/wakeconsumer_b358_test.go:232:106: undefined: ledger
internal/agentd/wakeconsumer_b358_test.go:291:98: undefined: ledger
internal/agentd/wakeconsumer_b358_test.go:294:129: undefined: ledger
FAIL	github.com/Xsxdot/handoff/internal/agentd [build failed]
```

本节点在 `$TMPDIR/bsrc`（HEAD 的 `git archive` 副本）只加这两处导入后复跑：

```text
$ go vet ./internal/agentd/        → exit 0
$ go test ./internal/agentd/... -count=1
    ok  github.com/Xsxdot/handoff/internal/agentd  168.139s
```

结论：缺陷就是缺两行导入，不影响生产代码；Task 0 修它并作为本卡全部 agentd 测试
（含新增断言）的前置。**这是白名单内改动，不越节点**。

### 1.3 代码图结论与图覆盖债

已用仓内 `codegraph --repo . <子命令>` 实跑：

```text
$ codegraph --repo . sym LaunchAdmit      → n_scheduling_Service_LaunchAdmit（ok，scheduling.go:712）
$ codegraph --repo . sym Server.wakeCoordinatorRound → n_agentd_Server_wakeCoordinatorRound，d_gateway，scheddrain.go:335
$ codegraph --repo . who-calls LaunchAdmit → Error: 节点 "LaunchAdmit" 不在图中；
    近似候选: n_scheduling_Service_LaunchAdmit(Service.LaunchAdmit)（确因须带容器名）
$ codegraph --repo . flow consumeAutomationEventsOnce → degraded=true steps=0
    （missing: 基线没有 flows 段），callers=[Server.runAutomationPass]，channels=[e_cli_agentd]
$ codegraph --repo . context d_orchestration → outputNodes=18 outputEdges=15 truncated=false
```

`context agentd` 被拒并给出最优树领域候选，说明本仓图按 `d_*` 领域命名（agentd 系
`d_gateway`/`d_orchestration`）。以下符号 `codegraph sym` 确认为图未覆盖（退出码 1，
近似候选为空），记入**图覆盖债**，实现者以源码与测试为准：

```text
ClaimWake / CompleteWake / WakeClaimsBefore / CursorWatermark        （internal/ledger/wakeclaim.go）
SeatBearingOf / ClearSeat                                            （internal/ledger/binding.go）
AdmitSeatCarrier / CoordinatorWake / EvSeatBearingMissing            （本卡新增）
```

`flow` 对消费循环返回 `degraded`（基线没有 flows 段），故本计划不引用 `flow` 结论，
控制流以 `internal/agentd/wakeconsumer.go` 源码为准；未命中一律已记债，不拿 `chain`
冒充流程。

**已核对的现状签名与调用面**（本计划直接引用，全部来自源码/图）：

- `func (s *Service) LaunchAdmit(squadName string) (Binding, error)` —— `internal/scheduling/scheduling.go:712`。
- `func (s *Service) AdmitFrozen(binding Binding) (Binding, error)` —— `:618`，明确拒绝
  协调者小队（`q.Role != RoleExecutor` → `ErrRoleMismatch`，`:647-651`）。
- `func (s *Service) acquire(q Squad, member SquadMember, c Carrier, req IgnitionRequest) (Binding, error)` —— `:968`。
- `func OccupancyKeys(squad, carrier string) (memberKey, carrierKey string)` —— `internal/scheduling/occupancy.go:28`。
- `func (s *Service) Squad(name string) (Squad, error)` —— `:480`；`Carrier` —— `:329`。
- `func (s *Server) wakeCoordinatorRound(ctx, card string, evs []keystone.WakeEvent) (keystone.RoundResult, error)` —— `internal/agentd/scheddrain.go:335`，`:359` 现 `LaunchAdmit`。
- `func (s *Server) clientForTarget(target string) (*client.Client, error)` —— `internal/agentd/server.go:439`。
- `func (c *Client) MarkForwarded() *Client` —— `internal/client/client.go:326`（逐字段复制并置 `X-Handoff-Forwarded: 1`）。
- `func (c *Client) do(ctx, method, path string, body any) (*http.Response, error)` —— `:382`；`httpError` —— `:437`（读限 `io.LimitReader(resp.Body, 4096)`）。
- `func (s *Store) mutate(func(*sql.Tx, *eventSink) error) error` —— `internal/ledger/store.go:156`。
- `func (s *Store) appendEvent(tx *sql.Tx, sink *eventSink, cardID, typ, actor string, payload any) (int64, error)` —— `internal/ledger/events.go:19`。
- `func (s *Store) CloseCard(id, reason, actor string) error` —— `internal/ledger/cards.go:692`（只改 `status`/`terminate_reason`，不清席位）。
- `EvNeedsHuman = "needs_human"` —— `internal/ledger/types.go:63`；`EvSeatBearingMissing` 尚不存在。
- `func (s *Server) SetupAutomation(st *ledger.Store) error` —— `internal/agentd/server.go:975`（装配 `autoLedger`/`automationCursorStore`）。
- `SchedulingClient` 接口在 `internal/agentd/scheduling_client.go:24`；新增 `AdmitSeatCarrier` 必须同时进接口与编译期断言。

### 1.4 依赖行为已核对的基线事实

实现不得假设未核对的行为。以下出处已逐一读过：

- `client.do` 非空 body 走 `json.Marshal` 并设 `Content-Type: application/json`；
  传输错误在 `relayBacked` 时包 `ErrTunnelDisconnected`，否则 `ErrUnreachable`
  （`internal/client/client.go:382-430`）。转交失败据此判别，不猜状态。
- `agentd` 路由用 Go 1.22 method-pattern（`api.HandleFunc("POST /api/cards/{id}/coordinator/launch", ...)`，
  `internal/agentd/coordapi.go:42-48`），新端点照抄该注册风格。
- `Server.auth` 在 `server.go:846` 包裹整棵 `mux`（`server.go:813`），新端点落在
  `s.auth(mux)` 之内即继承 Bearer 鉴权，不新增鉴权形态（契约 §3.3.5）。
- `IsLocalMachine` 把 `""`/`local`/`本机`/当前 `os.Hostname()` 视为本机
  （`internal/scheduling/scheduling.go:182-189`）；`IsSelfTarget` 额外认
  `config.Targets` 里 `config.IsSelfTarget` 命中的名字（`server.go:412-422`）。归属判定
  两者都要用（契约 §3.2 第 4 步）。
- `ClaimWake` 认领键是 `seq` 本身、排他靠 `mutate` 事务串行化、过期可接管
  （`internal/ledger/wakeclaim.go:27`；测试 `wakeclaim_test.go:11`）。
- `CursorWatermark(from, candidate)` 把候选水位收紧到「终局前缀」（`wakeclaim.go:133`）。

### 1.5 既有测试夹具（实现者照抄，不新造 harness）

- `internal/agentd/ledgerapi_test.go:158` `newNoPTYLedgerEnv(t) *ledgerEnv`（真实 SQLite
  `ledger.db` + agentd httptest + `SeatBearing` 可写的 `env.ledger`）。
- `internal/agentd/b23314_scheduling_testhelpers_test.go:74` `SetupAutomationForTest`。
- `internal/agentd/scheddrain_test.go:60` `seedQueueCoordinator`；`:25` `queueTraceRunner`。
- `internal/agentd/coordapi_test.go:146` `createCoordCard`；`:51` `fakeCoordRunner`；
  `:116` `newNoPTYCoordEnv`；`:71` `allowCarrierMachines`。
- `internal/agentd/wakeconsumer_test.go:28` `newNoPTYAutomationEnv`；`:344` `prebindConsumerSession`；
  `:35` `appendMirroredForConsumer`；`:112` 起 `appendWorkflowMirroredForConsumer`。
- `internal/scheduling/scheduling_test.go:520` `newFrozenFixture`；`:77` `newCASFixture`；
  `:100` `putOnlineCarrier`；`:120` `runningCount`。
- `cmd/ledgercli_test.go:33` `runLedgerCLI`；`cmd/console_test.go:96` `runSubcommandForTest`。

## 2. 跨任务接口合同与文件边界

### 2.1 Consumes / Produces（逐字签名）

| 任务 | Consumes | Produces |
| --- | --- | --- |
| T0 基线修复 | 无 | `internal/agentd/scheddrain_test.go`、`internal/agentd/wakeconsumer_b358_test.go` 补 `"github.com/Xsxdot/handoff/internal/ledger"` 导入 |
| T1 第 2 片 | `ledger.SeatBearing`（`binding.go:149`）；`ledger.SeatBearingOf(id) (SeatBearing,bool,error)`（`:208`）；`scheduling.Service.acquire`/`Squad`/`Carrier`；`ledger.BindSeat`/`RebindSeat` 承载体代价 | `internal/scheduling/scheduling.go`：`func (s *Service) AdmitSeatCarrier(squad, carrier string) (Binding, error)`；`internal/agentd/scheduling_client.go` 接口加同签名；`internal/ledger/binding.go`：`func (s *Store) SetSeatBearing(id, expect string, bearing SeatBearing) error`；`internal/agentd/scheddrain.go`：`wakeCoordinatorRoundRaw`（见 T3）从 `SeatBearingOf` 构造 spec；`cmd/card_bearing.go` + `cmd/card.go`：`handoff card seat bearing set <id> --carrier <carrier>` |
| T2 第 3 片 | `ledger.CloseCard`；`ledger.EvNeedsHuman`；`ledger.deleteSeatBearing`（`binding.go:199`）；`ledger.CursorWatermark`（`wakeclaim.go:133`） | `internal/ledger/types.go`：`EvSeatBearingMissing = "seat_bearing_missing"`；`internal/ledger/cards.go`：`func (s *Store) clearSeatTx(tx *sql.Tx, card string) error`（`binding.go` 内）+ `CloseCard` 同事务调用；`internal/agentd/wakeconsumer.go`：`automationWakeEvent` 移除 `EvNeedsHuman`、删自生 seen 补丁、终态卡闸 |
| T3 第 4 片 | T1 的 `SeatBearingOf` 读面；`ledger.ClaimWake/CompleteWake/CursorWatermark`；`scheduling.IsLocalMachine`；`s.IsSelfTarget` | `internal/agentd/wakeconsumer.go`：`claimWakeBatch`/`completeWakeBatch`/`advanceAutomationCursorWatermark`；`internal/agentd/scheddrain.go`：`wakeCoordinatorRoundRaw`（归属判定）；`internal/ledger/events.go`：`func (s *Store) ReportSeatBearingMissing(cardID, seat string) (bool, error)` |
| T4 第 5 片 | T3 的 `wakeCoordinatorRoundRaw` 远端分支；`s.clientForTarget`；`client.MarkForwarded` | `internal/proto/ledger.go`：`CoordinatorWakeReq`/`CoordinatorWakeResp`；`internal/client/coordinator.go`：`func (c *Client) CoordinatorWake(ctx, cardID string, req proto.CoordinatorWakeReq) (*proto.CoordinatorWakeResp, error)`；`internal/agentd/coordapi.go`：路由 `POST /api/cards/{id}/coordinator/wake` + `handleCoordWake` |
| T5 第 6 片 | T1 的 `AdmitSeatCarrier`；T3 的归属判定 | 仅测试与接口断言：`internal/scheduling/admit_seat_carrier_test.go`、`internal/agentd` 路由矩阵测试 |
| T6 第 7 片 | T3 的批次循环；T4 的转交失败路径 | `internal/agentd/server.go`：`automationBackoff map[string]wakeBackoff`；`internal/agentd/wakeconsumer.go`：单卡失败 `continue`、退避常量 |

### 2.2 不变与禁止变更

- 席位真源仍是 `cards.driver_session`/`driver_source`；`seat_bearings.identity` 只是
  同事务见证。不复用、不读不写 `driver_carrier`。
- `RewnewDriverLease`/`DropDriverLease`/`DriverLeaseOf` 不改；活性租约与承载记录无关。
- `DriverLeaseTTL`/`RewnewDriverLease` 不接入唤醒认领；认领是独立表 `wake_claims`。
- `Client.CoordinatorRebind`/`CoordinatorForget` 现签名与本卡无关，不改。
- 转交出站**只走** `s.clientForTarget` + `MarkForwarded`，**不使用**入站 `?machine=`
  转发面（`forwardIfRequested`），否则防环头会把请求改向（契约 §3.3.1）。
- 不改 `needs_human` 的两条展示通路（卡级订阅 `cmd/card_wait.go:235`、会话列表
  `internal/collab/sessions.go:430` 与 `cmd/session.go:331`）——只允许它们「仍绿」，
  不允许改判据。
- 不碰 B381 四条余项；不动主线；不 push。

### 2.3 认领与退避常量

```go
// internal/agentd/wakeconsumer.go
const (
	// wakeClaimTTL 是唤醒认领租期：短于 agentd stalltimeout（默认 2h），
	// 够一台机器崩溃后被另一台接管；不设无限期，避免认领行永久挡住宿主游标。
	wakeClaimTTL = 5 * time.Minute
	// wakeRetryBackoff 是同卡同 seq 失败后的退避窗（契约 §3.5.4）。
	wakeRetryBackoff = 30 * time.Second
	// wakeRetryMax 是同卡连续失败的重试上限，达到后不再自动试跑，转人工。
	wakeRetryMax = 3
)
```

`wakeClaimTTL` 与既有 `DriverLeaseTTL = 5 * time.Minute`（`binding.go:24`）同量级，
理由一致：租期只用于崩溃接管，长于单次 print 回合。

### 2.4 冻结断言（契约 §4）覆盖矩阵

| 断言 | 归属切片/任务 | 锁点 |
| --- | --- | --- |
| 1–10、12、13 | 已落（Ticket 0） | `internal/ledger/bearing_test.go` |
| 11 终态原子清席位 | 第 3 片 / T2 | `TestCloseCardClearsSeatAndBearing` |
| 14–17 认领与水位 | 已落（第 1 片） | `internal/ledger/wakeclaim_test.go` |
| 18 needs_human 不唤醒 | 第 3 片 / T2 | `TestB389NeedsHumanDoesNotWake` |
| 19 卡级订阅仍收到 needs_human | 第 3 片 / T2（回归，不改码） | `cmd` 既有 `TestCardWait*` 仍绿 + `TestB389NeedsHumanStillActionableInCardWait` |
| 20 会话列表待办仍显示 | 第 3 片 / T2（回归，不改码） | `internal/collab` 既有 `TestSessionTimelineNeedsHuman` 仍绿 + `TestB389SessionListNeedsHumanTagKept` |
| 21 无承载不发起回合不占名额 | 第 4 片 / T3 | `TestB389MissingBearingSkipsWithoutSlots` |
| 22 恰一条 missing 事件、幂等 | 第 4 片 / T3 | `TestB389SeatBearingMissingIdempotent` |
| 23 远端本机 0 resume/launch | 第 4 片 / T3 + 第 5 片 / T4 | `TestB389RemoteBearingDoesNotRunLocally` |
| 24 远端本机两键计数不变 | 第 4 片 / T3 | `TestB389RemoteBearingKeepsLocalSlotsUnchanged` |
| 25 恰一次 POST，body 含 seq 集合 | 第 5 片 / T4 | `TestB389TransferPostsOnceWithBatchSeqs` |
| 26 对端 Seat 不符 409 不改状态 | 第 5 片 / T4 | `TestB389WakeEndpointSeatMismatch409` |
| 27 对端成功后 RebindSeat(expect=旧) | 第 5 片 / T4 | `TestB389WakeEndpointExecutesAndRebinds` |
| 28 载体非小队成员拒绝 | 第 6 片 / T5 | `TestAdmitSeatCarrierRejectsNonMember` |
| 29 不走 LaunchAdmit 候选遍历 | 第 6 片 / T5 | `TestB389WakeUsesFrozenCarrierNotLaunchAdmit` |
| 30 单卡失败不断同批 | 第 7 片 / T6 | `TestB389WakeFailureDoesNotBlockSiblingCard` |
| 31 退避：相邻两轮不重复试跑 | 第 7 片 / T6 | `TestB389WakeBackoffSkipsSameSeqNextRound` |
| 32 终态卡不再唤醒 | 第 3 片 / T2 | `TestB389TerminalCardDoesNotWake` |

**显式欠账（不静默）**：

- **D4（queue_release 不转交）**：`drainIgnitionRequest` 的合成
  `WakeQueueRelease` 无 `card_events` 行，远端归属时不做转交，返回指路错误并由
  `drainIgnitionRequest` 回填队列。单机行为不变；真实外部唤醒（有账本事件）的转交
  面完整。若后续要覆盖远端队列出队，需扩 DTO 支持合成事件——属另卡。
- **断言 23 的切片归属**：本计划的 DAG 让 T3 先以
  `errRemoteBearingNotTransferable` 占位出口点亮「本机不跑」（§4-23/24 的核心），
  T4 接转交后断言不变、语义升级为「转交而非本机跑」。T4 完成后必须复跑 T3 的
  `TestB389RemoteBearing*` 确认仍绿。

## 3. 任务 DAG

```text
T0 基线修复（agentd 测试包编译）
T1 第 2 片：承载记录接进唤醒 + AdmitSeatCarrier + 修复命令
  └─> T3 第 4 片：认领接线 + 归属解析 + 承载缺失显式路径
        └─> T4 第 5 片：转交面（agentd→agentd）
              ├─> T5 第 6 片：冻结载体准入的路由矩阵与名额断言
              └─> T6 第 7 片：不堵流与退避
T2 第 3 片：判据收口与终态（独立，可与 T1 并行）
```

**最薄路径**：今天 `SeatBearingOf` 在生产代码里**从未被调用**（全仓仅
`binding.go`/`bearing_test.go` 出现），唤醒路径在 `scheddrain.go:359` 直接
`LaunchAdmit` 重选载体——「远端席位路由 / 不重选载体」从声明缝
`Server.wakeCoordinatorRound` 构造不出可断言的预期结果，写下去必红。故 DAG 第一条
可跑路径是 T1：点亮 `SeatBearingOf` 进入生产唤醒路径、本机分支用承载记录构造 spec
并走冻结准入。T1 单独**不足以**断言远端行为（转交面在 T4），因此 T1 的测试只锁
本机分支与「spec 来自承载」，远端不本机执行由紧随其后的 T3 点亮
（`TestB389RemoteBearingDoesNotRunLocally` 在 T3 变红→绿）。

## 4. T0：基线修复（agentd 测试包编译）

### 精确文件集

- `internal/agentd/scheddrain_test.go`
- `internal/agentd/wakeconsumer_b358_test.go`

### 精确改动

两个测试文件在导入块补一行（其它不动）：

```go
// internal/agentd/scheddrain_test.go —— 在 ledgerstep 之前
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/ledgerstep"

// internal/agentd/wakeconsumer_b358_test.go —— 在 collab 之后
	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/ledger"
```

### 步骤与验收

1. 复跑 `go test ./internal/agentd/... -count=1`，确认红（原始报错见 §1.2）。把
   命令与原文追加台账。
2. 加两行导入；跑 `gofmt -l internal cmd`（空）与
   `go test ./internal/agentd/... -count=1`，预期 `ok ... 16x s`。台账记原始结果。
3. 不需要新增测试：本任务是把被 §1.2 破坏的既有包恢复到可编译，既有断言即验收。

## 5. T1（第 2 片）：承载记录接进唤醒 + 冻结载体准入 + 修复命令

### 精确文件集

生产文件：

- `internal/scheduling/scheduling.go`（新增 `AdmitSeatCarrier`）
- `internal/agentd/scheduling_client.go`（接口加一方法）
- `internal/agentd/scheddrain.go`（唤醒读承载、写 spec、走冻结准入）
- `internal/ledger/binding.go`（新增 `SetSeatBearing`）
- `cmd/card_bearing.go`（新增 `card seat bearing set`）
- `cmd/card.go`（注册 `seat` 子命令组）

测试文件：

- `internal/scheduling/admit_seat_carrier_test.go`（新增）
- `internal/ledger/bearing_test.go`（追加 `SetSeatBearing` 用例）
- `internal/agentd/scheddrain_test.go`（追加承载唤醒用例）
- `cmd/card_bearing_test.go`（新增）

### Interfaces（Consumes / Produces）

消费：`ledger.SeatBearing`（`binding.go:149`）、`ledger.SeatBearingOf`（`:208`）、
`Store.GetCard`（`cards.go:410`）、`scheduling.Service.acquire/Squad/Carrier`、
`client.Squads(ctx) (*proto.SquadsResp, error)`（`internal/client/squads.go:34`）、
`newTargetClient()`（`cmd/root.go:237`）、`openLedger()`（`cmd/ledgercli.go:29`）、
`runLedgerCLI`（`cmd/ledgercli_test.go:33`）。

产出：见 §2.1 T1 行；`CoordinatorBearing` 已有 `scheddrain.go:419`，本任务不改其形状，
只让唤醒读它。

### 精确实现形状

**（a）`internal/scheduling/scheduling.go` 新增冻结载体协调者准入**

```go
// AdmitSeatCarrier 按冻结载体对协调者小队做一次两级 CAS 准入：载体由调用方
// 指定（来自承载记录），不做候选遍历与打分。与 LaunchAdmit 的唯一差别是
// 「载体由调用方冻结指定」；与 AdmitFrozen 的差别是协调者小队合法（后者明确
// 拒绝协调者小队）。carrier 必须是该小队成员，否则 ErrRoleMismatch。
func (s *Service) AdmitSeatCarrier(squad, carrier string) (Binding, error) {
	logger := statusLog().With("squad", squad, "carrier", carrier)
	logger.Info("冻结载体协调者准入开始", "error_kind", "admit_seat_start")
	if strings.TrimSpace(squad) == "" || strings.TrimSpace(carrier) == "" {
		err := fmt.Errorf("%w: 冻结载体协调者准入需要小队与载体", ErrInvalid)
		logger.Error("冻结载体协调者准入参数缺失", "error_kind", "params_missing", "cause", err)
		return Binding{}, err
	}
	q, err := s.Squad(squad)
	if err != nil {
		logger.Error("冻结载体协调者小队读取失败", "error_kind", "squad_read", "cause", err)
		return Binding{}, err
	}
	if q.Role != RoleCoordinator {
		err := fmt.Errorf("%w: %s 是执行者小队", ErrRoleMismatch, q.Name)
		logger.Error("冻结载体协调者准入角色不符", "error_kind", "role_mismatch", "cause", err)
		return Binding{}, err
	}
	member := SquadMember{Carrier: carrier}
	found := false
	for _, candidate := range q.Members {
		if candidate.Carrier == carrier {
			member, found = candidate, true
			break
		}
	}
	if !found {
		err := fmt.Errorf("%w: 载体 %s 不在小队 %s", ErrRoleMismatch, carrier, q.Name)
		logger.Warn("冻结载体不是协调者小队成员", "error_kind", "member_mismatch", "cause", err)
		return Binding{}, err
	}
	c, err := s.Carrier(carrier)
	if err != nil {
		logger.Error("冻结载体读取失败", "error_kind", "carrier_read", "cause", err)
		return Binding{}, err
	}
	if c.Status != StatusOnline {
		err := fmt.Errorf("%w: 载体 %s 当前状态为 %s", ErrNoHealthy, carrier, c.Status)
		logger.Warn("冻结载体不在线", "error_kind", "carrier_offline", "cause", err)
		return Binding{}, err
	}
	binding, err := s.acquire(q, member, c, IgnitionRequest{Squad: q.Name, Actor: "admit_seat_carrier"})
	if errors.Is(err, errMemberFull) {
		err = ErrNoSlot
	}
	if err != nil {
		logger.Error("冻结载体协调者准入失败", "error_kind", admissionErrorKind(err), "cause", err)
		return Binding{}, err
	}
	logger.Info("冻结载体协调者准入成功", "member_key", OccupancyMemberKey(binding.Squad, binding.Carrier),
		"carrier_key", OccupancyCarrierKey(binding.Carrier),
		"target", binding.Target, "executor", binding.Executor, "error_kind", "admit_seat_success")
	return binding, nil
}
```

**（a2）`internal/scheduling/admit_seat_carrier_test.go` 的协调者小队夹具**（T1 新建
该测试文件时一并落；`newFrozenFixture` 的 `S` 是 executor，不能直接用它）：

```go
// newSeatCarrierFixture 是 AdmitSeatCarrier 的协调者小队夹具：与 newFrozenFixture
// 同形（载体 A→B 各一格、独立物理身份），只把小队角色换成 coordinator——后者是
// executor，调 AdmitSeatCarrier 只会命中角色拒绝。T5 的路由矩阵复用它。
func newSeatCarrierFixture(t *testing.T) (*scheduling.Service, *ledgerapi.Facade) {
	t.Helper()
	st, err := ledger.Open(filepath.Join(t.TempDir(), "seatcarrier.db"))
	if err != nil {
		t.Fatalf("打开冻结席位账本: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	facade := ledgerapi.New(st)
	svc := scheduling.New(facadeRegistry{f: facade})
	for _, c := range []scheduling.Carrier{
		{Name: "A", Machine: "machine-A", CLI: "cli-A", HomeDir: "/home/A", Model: "model-A",
			Credential: scheduling.CredentialStandalone, MaxConcurrency: 1},
		{Name: "B", Machine: "machine-B", CLI: "cli-B", HomeDir: "/home/B", Model: "model-B",
			Credential: scheduling.CredentialStandalone, MaxConcurrency: 1},
	} {
		putOnlineCarrier(t, svc, c)
	}
	if err := svc.PutSquad(scheduling.Squad{Name: "CS", Role: scheduling.RoleCoordinator,
		Members: []scheduling.SquadMember{{Carrier: "A", MaxConcurrency: 1}, {Carrier: "B", MaxConcurrency: 1}},
	}, 0); err != nil {
		t.Fatalf("登记冻结席位小队: %v", err)
	}
	return svc, facade
}
```

**（b）`internal/agentd/scheduling_client.go` 接口加方法**（编译期断言
`var _ SchedulingClient = (*scheduling.Service)(nil)` 会强制实现存在）：

```go
	// —— 冻结载体准入（scheddrain 唤醒路径，B389 §3.4）——
	AdmitSeatCarrier(squad, carrier string) (scheduling.Binding, error)
```

**（c）`internal/ledger/binding.go` 新增修复写面**

```go
// SetSeatBearing 为已有 coordinate 席位补写承载记录（契约 §2.3 存量修复路径：
// 会话已知在哪台机器）。CAS 断言卡上席位身份未变；只接受 coordinate 来源；
// 只 upsert 承载行，不改 cards 席位两列（席位真源不动）。补齐后该卡恢复可唤醒。
func (s *Store) SetSeatBearing(id, expect string, bearing SeatBearing) error {
	log().Info("补写承载记录", "card", id, "has_expect", expect != "", "carrier", bearing.Carrier)
	if strings.TrimSpace(bearing.Carrier) == "" || strings.TrimSpace(bearing.Machine) == "" {
		err := fmt.Errorf("补写承载记录必须给载体与机器归属: %w", ErrBadState)
		log().Warn("补写承载记录被拒：归属缺失", "card", id, "cause", err)
		return err
	}
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		card, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("补写承载读卡 %s: %w", id, err)
		}
		if card.DriverSource != string(proto.SeatSourceCoordinate) || card.DriverSession == "" {
			err := fmt.Errorf("卡 %s 不是 coordinate 席位，不能补写承载: %w", id, ErrBadState)
			log().Warn("补写承载被拒：非叫机器人席位", "card", id, "source", card.DriverSource, "cause", err)
			return err
		}
		if card.DriverSession != expect {
			err := fmt.Errorf("卡 %s 当前席位与期望不符，请重读后补写: %w", id, ErrCASConflict)
			log().Warn("补写承载被拒：CAS 冲突", "card", id, "cause", err)
			return err
		}
		return s.writeSeatBearing(tx, id, card.DriverSession, bearing)
	})
	if err != nil {
		log().Warn("补写承载记录失败", "card", id, "cause", err)
		return err
	}
	log().Info("补写承载记录完成", "card", id, "carrier", bearing.Carrier)
	return nil
}
```

**（d）`internal/agentd/scheddrain.go` `wakeCoordinatorRound` 改造**（归属判定前移、
从承载构造 spec、走冻结准入；本任务先落本机分支，远端分支在 T3/T4 展开——T1 阶段
远端分支返回 `errRemoteBearingNotTransferable` 供 T3 替换，**不得静默本机执行**）：

```go
// errRemoteBearingNotTransferable 是 T1 的显式占位出口：承载机器非本机时，
// 唤醒必须转交而非本机执行；转交面在 T3/T4 接入。宁可显式失败，不跑错机器。
var errRemoteBearingNotTransferable = errors.New("协调者承载在远端，转交面尚未接入")
```

`wakeCoordinatorRound`（保留契约 §1 现有签名，委托新实现）：
```go
func (s *Server) wakeCoordinatorRound(ctx context.Context, card string,
	evs []keystone.WakeEvent) (keystone.RoundResult, error) {
	return s.wakeCoordinatorRoundRaw(ctx, card, evs, nil)
}

// wakeCoordinatorRoundRaw 是唤醒回合的现行实现：先读承载记录定归属，本机分支用
// 承载记录构造 SessionSpec 并按冻结载体申请名额（不再 LaunchAdmit 重选载体），
// 远端分支转交（T4）。raws 是本批原始账本事件，转交面需要它（T4）。
func (s *Server) wakeCoordinatorRoundRaw(ctx context.Context, card string,
	evs []keystone.WakeEvent, raws []proto.LedgerEvent) (keystone.RoundResult, error) {
	var zero keystone.RoundResult
	current, err := s.ledger.GetCard(card)
	if err != nil {
		return zero, fmt.Errorf("读取唤醒席位: %w", err)
	}
	if current.DriverSession == "" && current.DriverSource == "" {
		s.keystone.Forget(card)
		s.log.Info("空座跳过协调者唤醒", "card", card, "event_count", len(evs))
		return zero, nil
	}
	if current.DriverSource == string(proto.SeatSourceBind) {
		s.keystone.Forget(card)
		s.log.Info("bind 席位跳过协调者唤醒", "card", card, "event_count", len(evs))
		return zero, nil
	}
	if err := proto.ValidateSeat(current.DriverSession, proto.SeatSource(current.DriverSource)); err != nil {
		return zero, fmt.Errorf("唤醒席位非法: %w", err)
	}
	bearing, hasBearing, err := s.ledger.SeatBearingOf(card)
	if err != nil {
		return zero, fmt.Errorf("读取唤醒承载记录: %w", err)
	}
	if !hasBearing {
		// 存量 coordinate 席位无承载：显式修复路径（T3 落事件与展示），本任务先跳过。
		return zero, fmt.Errorf("卡 %s 的 coordinate 席位缺承载记录，请先 seat bearing set: %w",
			card, ledger.ErrNotFound)
	}
	if !scheduling.IsLocalMachine(bearing.Machine) && !s.IsSelfTarget(bearing.Machine) {
		return zero, errRemoteBearingNotTransferable
	}
	squad, err := s.resolveCoordinatorSquad()
	if err != nil {
		return zero, &coordinatorLookupError{err: err}
	}
	binding, err := s.scheduling.AdmitSeatCarrier(squad.Name, bearing.Carrier)
	if err != nil {
		return zero, &coordinatorAdmissionError{squad: squad.Name, err: err}
	}
	defer s.releaseSchedulingBinding(card, binding)
	carrier, err := s.scheduling.Carrier(bearing.Carrier)
	if err != nil {
		s.log.Error("读协调者载体失败", "card", card,
			"squad", binding.Squad, "carrier", binding.Carrier, "cause", err)
		return zero, fmt.Errorf("读载体 %s: %w", binding.Carrier, err)
	}
	cli, _, err := proto.ParseSeatIdentity(current.DriverSession)
	if err != nil {
		return zero, fmt.Errorf("解析唤醒席位 CLI: %w", err)
	}
	spec := keysclient.SessionSpec{
		CLI: cli, HomeDir: bearing.HomeDir, Model: bearing.Model, Workdir: bearing.Workdir,
	}
	normalized, err := orchestration.NormalizeCoordinatorSpec(spec)
	if err != nil {
		s.log.Error("规范化协调者 SessionSpec 失败", "card", card,
			"squad", binding.Squad, "carrier", binding.Carrier, "cause", err)
		return zero, err
	}
	spec = normalized
	s.log.Info("自动化唤醒协调者回合", "card", card,
		"event_count", len(evs), "squad", binding.Squad, "carrier", binding.Carrier,
		"cli", spec.CLI, "home_dir", spec.HomeDir, "frozen_from_bearing", true)
	result, err := s.keystone.Wake(ctx, card, evs, spec)
	if err != nil {
		s.log.Error("自动化唤醒协调者回合失败", "card", card,
			"event_count", len(evs), "squad", binding.Squad,
			"carrier", binding.Carrier, "cause", err)
		return result, fmt.Errorf("唤醒协调者回合失败: %w", err)
	}
	if result.Rebuilt && result.SessionID != "" && result.SessionID != current.DriverSession {
		identity, encodeErr := proto.EncodeSeatIdentity(cli, result.SessionID)
		if encodeErr != nil {
			return result, fmt.Errorf("重建后编码新席位: %w", encodeErr)
		}
		if rebindErr := s.ledger.RebindSeat(card, identity, proto.SeatSourceCoordinate, current.DriverSession,
			coordinatorBearing(binding, carrier, spec)); rebindErr != nil {
			if errors.Is(rebindErr, ledger.ErrCASConflict) {
				s.log.Error("协调者重建后席位 CAS 冲突，新会话保留待人工回收", "card", card,
					"event_count", len(evs), "session", result.SessionID, "cause", rebindErr)
				return result, &coordinatorSeatConflict{result: result}
			}
			return result, fmt.Errorf("重建后写协调者席位: %w", rebindErr)
		}
		s.log.Info("协调者重建后席位已更新", "card", card, "event_count", len(evs), "session", result.SessionID)
	}
	s.log.Info("自动化唤醒协调者回合结束", "card", card,
		"event_count", len(evs), "session", result.SessionID,
		"rebuilt", result.Rebuilt, "escalated", result.Escalated)
	return result, nil
}
```

**（e）`cmd/card_bearing.go` 新增修复命令**（新增文件，文件头写职责+边界）：

```go
// card_bearing.go 把「存量 coordinate 席位补写承载记录」接出 CLI（B389 §2.3）。
// 边界：机器名与恢复环境一律从载体登记读出，不接受手输；写面走本机账本
// 的 SetSeatBearing（CAS 断言席位未变）。只修复承载，不重选载体、不换会话。
package cmd

import (
	"fmt"
	"log/slog"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

var cardSeatBearingCarrier string

var cardSeatCmd = &cobra.Command{
	Use:   "seat",
	Short: "协调者席位的承载记录维护（存量 coordinate 席位缺承载时的人工修复路径）",
}

var cardSeatBearingCmd = &cobra.Command{
	Use:   "bearing set <id> --carrier <carrier>",
	Short: "为卡上已有 coordinate 席位补写承载记录（机器名从载体登记读出，不手输）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		if cardSeatBearingCarrier == "" {
			return fmt.Errorf("--carrier 必填")
		}
		slog.Default().Info("CLI 补写承载入口", "card", id, "carrier", cardSeatBearingCarrier)
		cl, done, err := newTargetClient()
		if err != nil {
			slog.Default().Warn("CLI 补写承载取目标客户端失败", "card", id, "cause", err)
			return err
		}
		defer done()
		reg, err := cl.Squads(cmd.Context())
		if err != nil {
			return fmt.Errorf("读载体登记: %w", err)
		}
		var carrier *proto.CarrierView
		for i := range reg.Carriers {
			if reg.Carriers[i].Name == cardSeatBearingCarrier {
				carrier = &reg.Carriers[i]
				break
			}
		}
		if carrier == nil {
			return fmt.Errorf("载体 %q 未登记", cardSeatBearingCarrier)
		}
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		card, err := st.GetCard(id)
		if err != nil {
			return err
		}
		if _, _, err := proto.ParseSeatIdentity(card.DriverSession); err != nil {
			return fmt.Errorf("卡 %s 席位不可用，不能补写承载: %w", id, err)
		}
		// 载体的 CLI 必须与席位身份解出的 CLI 一致，否则会把恢复环境指到错误的执行器。
		seatCLI, _, _ := proto.ParseSeatIdentity(card.DriverSession)
		if seatCLI != carrier.CLI {
			return fmt.Errorf("载体 %s 的 CLI %q 与席位 CLI %q 不一致",
				carrier.Name, carrier.CLI, seatCLI)
		}
		bearing := ledger.SeatBearing{
			Carrier: carrier.Name, Machine: carrier.Machine,
			HomeDir: carrier.HomeDir, Model: carrier.Model,
		}
		if err := st.SetSeatBearing(id, card.DriverSession, bearing); err != nil {
			slog.Default().Warn("CLI 补写承载失败", "card", id, "cause", err)
			return fmt.Errorf("补写卡 %s 承载: %w", id, err)
		}
		slog.Default().Info("CLI 补写承载完成", "card", id, "carrier", carrier.Name)
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

func init() {
	cardSeatBearingCmd.Flags().StringVar(&cardSeatBearingCarrier, "carrier", "", "载体登记名（机器名从登记读出，不手输）")
	cardSeatCmd.AddCommand(cardSeatBearingCmd)
	cardCmd.AddCommand(cardSeatCmd)
}
```

**（f）`cmd/card.go` 无需改动**（`cardSeatCmd` 在 `card_bearing.go` 的 `init()` 里
挂到 `cardCmd`；同一包多文件 `init()` 执行，`card.go:563` 的 `AddCommand` 不冲突）。

**（g）唤醒夹具的承载对齐（T1 的既有测试迁移，不可省）**：改造后唤醒走
`AdmitSeatCarrier(squad, bearing.Carrier)`，要求承载里的 `Carrier` **是协调者小队的
在线成员**。而 agentd 既有唤醒夹具的承载写的是 `Carrier:"test-carrier"`——它不是
`seedQueueCoordinator` 登记的成员（成员是 `coord-carrier`），改造后所有走唤醒路径的
既有测试都会在准入处 `ErrRoleMismatch` 而红。必须把下列承载字面量从
`test-carrier` 改成 `coord-carrier`（`Machine` 保持 `"local"`，与
`seedQueueCoordinator` 的 `coord-carrier` 一致）：

```text
internal/agentd/wakeconsumer_test.go:354      （prebindConsumerSession 的唯一 BindSeat）
internal/agentd/wakeconsumer_test.go:842      （TestAutomationFallbackResumeRebuildFailure）
internal/agentd/wakeconsumer_b358_test.go:204 （TestB3583ReplyToReboundOldSeatDoesNotWake）
internal/agentd/wakeconsumer_b358_test.go:232 （TestB3583ReboundCardMentionHitsNewSeat）
internal/agentd/wakeconsumer_b358_test.go:291 （TestB3583StructureEventsNeverWake）
internal/agentd/wakeconsumer_b358_test.go:294 （TestB3583StructureEventsNeverWake）
internal/agentd/scheddrain_test.go:271        （TestAutomationIgnitionDrainWakesBeforeTrueDispatch）
```

`internal/agentd/coordapi_test.go` 的 `test-carrier`（`:348/517/698/788/843`）**不动**：
它们走 `handleCoordRebind`/`handleCoordForget`/`handleCoordStatus`/launch 抢占跑步，
不经过 `wakeCoordinatorRoundRaw` 的承载准入，改反而会破坏这些用例的语义。
`internal/collab/*_test.go` 与 `internal/keystone/slice_test.go` 的 `test-carrier`
也不动——它们不经 agentd 的唤醒路径。

两处细节，避免误改：

- `TestAutomationFallbackResumeRebuildFailure`（`wakeconsumer_test.go:842`）自登记的
  `coord-carrier` 机器是 `"ftm"`（并 `allowCarrierMachines(t, env.srv, "ftm")`），但
  **承载的 `Machine` 必须保持 `"local"`**，否则归属判定会走远端转交而不是本机分支，
  该用例的 escalate 断言就没了。`AdmitSeatCarrier` 只查成员/在线/名额，不做
  `AdmitFrozen` 那样的物理身份核验，故「承载 machine=local、载登记 machine=ftm」不会
  在准入处报错。
- `TestAutomationIgnitionDrainWakesBeforeTrueDispatch`（`scheddrain_test.go:271`）走的是
  `drainIgnitionRequest → wakeCoordinatorRound → wakeCoordinatorRoundRaw`，同样需要
  `coord-carrier`/`local`。

步骤：先做本迁移，跑
`go test ./internal/agentd/ -run 'TestB3583|TestAutomation|TestB349|TestB369|TestB370|TestAutomationWake|TestDrainQueues' -count=1`
确认**迁移本身不引入红**（此时唤醒实现还没改，承载字段对唤醒路径尚无意义，
迁移应为纯字符串替换、全绿），再进入下面的红绿。

### 步骤与红绿

0. **先做 §（g）的承载夹具迁移**（纯字符串替换，7 处 `test-carrier`→`coord-carrier`，
   `Machine` 保持 `"local"`），跑
   `go test ./internal/agentd/ -run 'TestB3583|TestAutomation|TestB349|TestB369|TestB370|TestAutomationWake|TestDrainQueues' -count=1`，
   预期仍全绿（此时唤醒实现未改，承载字段对唤醒路径尚无意义）；台账记原文。
   若此步已红，先修到绿再继续——它验证的是「迁移没改坏基线」。
1. 复跑基线 `go test ./internal/scheduling/... ./internal/ledger/... -count=1`，预期
   §1.1 的 `ok`；追加台账。在 `internal/scheduling/admit_seat_carrier_test.go` 先落
   §9 给定的 `newSeatCarrierFixture`（协调者小队 `CS`，照抄 `newFrozenFixture` 的
   载体登记形态；注意 `newFrozenFixture` 的 `S` 是 executor，不能直接用它）与一支
   最小失败测试：
   - `TestAdmitSeatCarrierAdmitsFrozenCoordinatorCarrier`：`AdmitSeatCarrier("CS","B")`
     成功且 `Binding.Carrier=="B"`，`runningCount(t, facade, "squad/CS/B")==1`、
     `runningCount(t, facade, "carrier/B")==1`。
   运行 `go test ./internal/scheduling/ -run TestAdmitSeatCarrierAdmitsFrozenCoordinatorCarrier -count=1`，
   记录红色（`AdmitSeatCarrier` 尚未实现，编译失败即红）。
2. 实现 `AdmitSeatCarrier`，再跑同一命令，预期绿；台账记原文。§4-28/29 的完整
   路由矩阵（非成员拒绝、执行者小队拒绝、不重选载体）归 T5 补，本 task 不重复。
3. 写 `SetSeatBearing` 的失败测试（`internal/ledger/bearing_test.go` 追加，复用
   `seedStore`/`TestSeatBearingFollowsSeatLifecycle` 的形态）：`TestSetSeatBearingRepairsMissingRecord`
   逐条断言——空座调用 → `ErrBadState`；`bind` 席位调用 → `ErrBadState`；CAS
   不符 → `ErrCASConflict` 且承载仍不存在；成功 → `SeatBearingOf` 返回该承载且
   `cards.driver_session` 原样不变。先跑
   `go test ./internal/ledger/ -run TestSetSeatBearing -count=1` 取红，再实现取绿。
4. 在 `internal/agentd/scheddrain_test.go` 写唤醒读承载的失败测试。先补一个记录
   `SessionRef` 的 runner（既有 `queueTraceRunner` 的 `Resume` 忽略 ref，锁不住
   spec 是否来自承载）：

```go
// bearingTraceRunner 记录 Resume 收到的 SessionRef，用来断言唤醒 spec 来自承载记录
// （既有 queueTraceRunner.Resume 丢弃 ref，锁不住 HomeDir/Model 来源）。
type bearingTraceRunner struct {
	mu   sync.Mutex
	refs []keysclient.SessionRef
}

func (r *bearingTraceRunner) Launch(keysclient.SessionSpec, string) (keysclient.TurnResult, error) {
	return keysclient.TurnResult{SessionID: "bearing-session"}, nil
}

func (r *bearingTraceRunner) Resume(ref keysclient.SessionRef, _ string) (keysclient.TurnResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refs = append(r.refs, ref)
	return keysclient.TurnResult{SessionID: ref.SessionID}, nil
}

func (r *bearingTraceRunner) snapshot() []keysclient.SessionRef {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]keysclient.SessionRef(nil), r.refs...)
}
```

   测试体（夹具：`seedQueueCoordinator`（登记 `coord` 小队 + `coord-carrier`）+ 把
   keystone 换成 `bearingTraceRunner`）：

   - `TestB389WakeUsesBearingForSpec`：`createCoordCard` 建卡 →
     `env.srv.keystone.LaunchForCard(ctx, card, "coordinate", keysclient.SessionSpec{CLI:"opencode"})`
     取 session → `env.ledger.BindSeat(card, identity, proto.SeatSourceCoordinate, ledger.SeatBearing{
     Carrier:"coord-carrier", Machine:"local", HomeDir:"/tmp/coord-home", Model:"m-bearing"})`
     → 调 `env.srv.wakeCoordinatorRoundRaw(ctx, card, []keystone.WakeEvent{{Kind:keystone.WakeTaskTerminal, Card:card, Summary:"terminal"}}, nil)`
     → 断言 `refs` 恰一条，`refs[0].HomeDir=="/tmp/coord-home"`、
     `refs[0].Model=="m-bearing"`、`refs[0].CLI=="opencode"`。
   - `TestB389WakeWithoutBearingFailsExplicitly`：同上但 `BindSeat` 传
     `ledger.SeatBearing{}` 走不了（coordinate 必填承载）——故改用
     `BindSeat(...SeatBearing{Carrier:"coord-carrier",Machine:"local"})` 后用
     `env.ledger.ClearSeat` 之外的手段删承载行不可行；改为：`BindSeat` 正常写承载后
     `db.Exec("DELETE FROM seat_bearings WHERE card_id=?")`（照抄
     `bearing_test.go:147` 的拆表手法），再调 `wakeCoordinatorRoundRaw` →
     断言返回非 nil 错误、`refs` 为空（不静默本机执行）。

   运行 `go test ./internal/agentd/ -run 'TestB389WakeUsesBearing|TestB389WakeWithoutBearing' -count=1` 取红。

   > 注意 `TestB389WakeWithoutBearingFailsExplicitly` 直接删承载行后调用，
   > T1 的实现分支返回错误；T3 会把该分支替换为 `reportSeatBearingMissing` 并
   > 返回 `nil`——届时本测试的期望要随之改为「零 Resume 且落一条
   > `EvSeatBearingMissing`」，这个改动归 T3（见 T3 步骤 5）。
5. 改造 `wakeCoordinatorRound` 为委托 + `wakeCoordinatorRoundRaw`，改
   `scheddrain.go:359` 起的 `LaunchAdmit` 段为承载读取 + `AdmitSeatCarrier`；跑第 4
   步命令取绿；再跑 `go test ./internal/agentd/ -run 'TestDrainQueues|TestAutomation' -count=1`
   确认既有清队/唤醒测试不回归（`drainIgnitionRequest` 仍走 `wakeCoordinatorRound`，
   其签名未变）。
6. 写 CLI 修复命令的失败测试（新增 `cmd/card_bearing_test.go`，复用
   `runLedgerCLI` 与 `seedCardCLIForTest` 的账本夹具）：`TestCardSeatBearingSetRepairs`
   从 root command 调用 `card seat bearing set <id> --carrier c1`，断言：
   成功输出 `{"ok":true}`；`SeatBearingOf` 返回登记里的 machine/home/model；席位
   未变。载体登记需先经 agentd HTTP 写入，用 `newTargetClient` 指向 httptest 的
   夹具（照抄 `cmd/session_test.go:44` 的 `ledgerapi.New` + `collab.New` 形态与
   `cmd/console_test.go:96` `runSubcommandForTest` 的 server 装配）。先跑红。
7. 加日志与注释核对：`AdmitSeatCarrier` 入口/成员校验/成功/失败分支均有结构化
   `statusLog` 日志；`SetSeatBearing` 入口/拒绝/成功有 `log()`；CLI 命令入口/成功/
   失败有 `slog`；导出函数注释含参数/返回/事务边界/为什么必须冻结载体。跑
   `gofmt -l internal cmd`（空）与
   `go test ./internal/scheduling/ ./internal/ledger/ ./internal/agentd/ ./cmd/ -count=1`。

### T1 测试范围与接缝

最小测试范围：`./internal/scheduling/`、`./internal/ledger/`、`./internal/agentd/`、
`./cmd/`，不跑全仓、不跑 Web。

缝级入口：`scheduling.Service.AdmitSeatCarrier`（生产新面，T1 新测试直接调用——
它同时是本卡 §3.4 的声明缝）；`Server.wakeCoordinatorRoundRaw`（唤醒声明缝，
T1 测试从其入口进入，不调内部 helper）；`handoff card seat bearing set`（CLI 声明缝，
经 root command + 真实 agentd HTTP + 真实 SQLite）。三条缝各有至少一支缝级断言。
本任务无「从声明缝构造不出」的内部锁，故无内部锁声明。

对应对抗审查：非法载体名（成员校验族）、空座/bind 席位补承载（状态族）、CAS 竞争
（并发族）、CLI 载体与席位 CLI 不一致（身份族）、无承载唤醒（显式失败族）。

## 6. T2（第 3 片）：判据收口与终态

### 精确文件集

生产文件：

- `internal/agentd/wakeconsumer.go`
- `internal/ledger/cards.go`
- `internal/ledger/binding.go`（抽出 `clearSeatTx`）
- `internal/ledger/types.go`（新增 `EvSeatBearingMissing`）

测试文件：

- `internal/agentd/wakeconsumer_test.go`（改一条既有断言 + 新增）
- `internal/ledger/bearing_test.go`（追加 `CloseCard` 清座用例）
- `internal/collab/sessions_test.go`（既有 `TestSessionTimelineNeedsHuman` 不改，作回归）
- `cmd/card_wait_*_test.go`（既有断言不改，作回归）

### 精确实现形状

**（a）`internal/ledger/types.go`** 在事件常量块加：

```go
	// EvSeatBearingMissing 是 coordinate 席位缺承载记录的显式修复信号（B389 §2.3）。
	// 它不唤醒协调者（判据收口），只进卡事件流与「需要人」展示。
	EvSeatBearingMissing = "seat_bearing_missing"
```

**（b）`internal/ledger/binding.go`** 抽出事务内清座助手，供 `ClearSeat` 与
`CloseCard` 共用：

```go
// clearSeatTx 在调用方事务内清空席位三列并删除承载行（幂等）。终态转移与
// 人工清座共用，保证「关单」与「清座」不会分成两步。
func (s *Store) clearSeatTx(tx *sql.Tx, card string) error {
	if _, err := tx.Exec(s.q(`UPDATE cards SET driver_session = '', driver_source = '', driver_heartbeat_at = ? WHERE id = ?`),
		s.tval(time.Time{}), card); err != nil {
		return fmt.Errorf("清席位写卡 %s: %w", card, err)
	}
	return s.deleteSeatBearing(tx, card)
}
```

`ClearSeat` 改为调用它（替换 `binding.go:224-246` 内的事务体），行为不变。

**（c）`internal/ledger/cards.go` `CloseCard`** 在写状态与落事件之间插入同事务清座：

```go
		if err := s.clearSeatTx(tx, id); err != nil {
			return fmt.Errorf("终止清席位 %s: %w", id, err)
		}
```

**（d）`internal/agentd/wakeconsumer.go`** 三处收口：

1. `automationWakeEvent`（`:88`）把 `ledger.EvNeedsHuman` 移出唤醒清单：

```go
	case ledger.EvNeedsCleared,
		ledger.EvDecisionOpened, ledger.EvDecisionAnswered:
		return keystone.WakeEvent{
			Kind: keystone.WakeTaskTerminal, Card: ev.CardID,
			Summary: fmt.Sprintf("%s: %s", ev.Type, truncateRunes(string(ev.Payload), 400)),
		}, true, nil
	case ledger.EvNeedsHuman, ledger.EvSeatBearingMissing:
		// 判据收口（契约 §3.5.1）：等人与缺承载不唤醒协调者；两条展示通路
		// （卡级订阅 / 会话列表待办）由既有消费者承担，本函数只保证不唤醒。
		return keystone.WakeEvent{}, false, nil
```

2. 删除「把自生 needs_human 标 seen」的补丁（现 `:431-448` 的 `if result.Escalated { ... }`
   整段），因为 `needs_human` 已不在唤醒映射里，这段没有存在理由（契约 §3.5.3）。
   删除后 `wakeErr` 分支仍推进游标（B274 约束不可丢）。
3. 在批次唤醒前的决策段加终态卡闸（§3.2 第 1 步、§4-32）。**必须把该批 seq
   标 seen 并 `continue`，不能用 `return`**——终态卡的 pending 若不消费，游标
   不推进、下一轮重复读到同一条，形成活锁：

```go
		if cardRow, readErr := s.ledger.GetCard(card); readErr == nil &&
			(cardRow.Status == ledger.StatusDone || cardRow.Status == ledger.StatusClosed) {
			s.keystone.Forget(card)
			for _, item := range batch {
				s.automationMu.Lock()
				s.automationSeen[item.seq] = struct{}{}
				s.automationMu.Unlock()
			}
			s.log.Info("终态卡不再唤醒", "card", card, "status", cardRow.Status,
				"event_count", len(batch))
			continue
		}
```

放在 `decision := s.keystone.Decide(evs[0])` 之前。（T3 会在此段之前插入认领；
终态闸放在认领之前，终态事件根本不产生 `wake_claims` 行。）

### 步骤与红绿

1. 在 `internal/agentd/wakeconsumer_test.go` 把既有
   `TestB353AutomationMapsCardActionEvents`（`:495`）改成收口后的期望：`needs_human`
   不再进入唤醒批次，`needs_cleared` 仍进；断言 `processed==3`（原 4）且 briefing
   含 `"needs_cleared"`、不含 `"needs human payload"`。先跑该测试取红（预期 3 得 4）。
2. 实现 `automationWakeEvent` 收口；再跑该测试取绿。新增
   `TestB389NeedsHumanDoesNotWake`：`MarkNeedsHuman` 后跑消费循环，断言
   `runner.snapshot()` 的 resumes/launches 均为 0，且 `processed==0`（§4-18）。
3. 删除自生 seen 补丁；跑
   `go test ./internal/agentd/ -run 'TestAutomationWakeFailureAdvancesCursor|TestAutomationFallbackResumeRebuildFailure' -count=1`
   取绿（这两条锁失败路径，删补丁不得让它们回归）。
4. 写终态闸测试 `TestB389TerminalCardDoesNotWake`（§4-32）。**必须用
   `MoveCard` 到 `已完成`，不能用 `CloseCard`**：T2 的 `CloseCard` 已清席位，
   那样的卡根本进不了唤醒路径，测不到闸。而 `MoveCard`→`已完成` 保留席位
   （D2 边界），正是终态闸要兜住的场景：

```go
func TestB389TerminalCardDoesNotWake(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	// bug 流是 待办→进行中→待审阅→已完成，无附件门，能直接移到终态。
	seedAgentdLedger(t, env.ledger, "bug")
	card, err := env.ledger.CreateCard(ledger.NewCard{
		Title: "终态卡", Project: "handoff", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	prebindConsumerSession(t, env, card.ID) // 席位 + 承载 coord-carrier/local
	for _, to := range []string{ledger.StatusDoing, ledger.StatusReview, ledger.StatusDone} {
		if err := env.ledger.MoveCard(card.ID, to, "", "test"); err != nil {
			t.Fatalf("移列到 %s: %v", to, err)
		}
	}
	if got, _ := env.ledger.GetCard(card.ID); got.Status != ledger.StatusDone || got.DriverSession == "" {
		t.Fatalf("前置条件：终态卡仍带席位，status=%q session=%q", got.Status, got.DriverSession)
	}
	appendMirroredForConsumer(t, env.ledger, card.ID, "terminal", "question", 1, `{"ticket_id":"tk"}`)
	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
		t.Fatalf("消费: %v", err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 0 {
		t.Fatalf("终态卡不得唤醒，resumes=%v", resumes)
	}
	for _, key := range []string{"squad/coord/coord-carrier", "carrier/coord-carrier"} {
		if got := runningCountIn(t, env.srv.autoLedger, key); got != 0 {
			t.Fatalf("终态卡不得占名额 %s=%d", key, got)
		}
	}
}
```

   先红（当前会唤醒）后绿。另在 T2 的 `CloseCard` 清座金样本里补一条交叉断言：
   `CloseCard` 后席位已空，唤醒路径在 `resolveWakeCard` 就返回空，与终态闸构成
   双保险（两条都要断言，不互相替代）。
5. 写 `CloseCard` 清座金样本
   `TestCloseCardClearsSeatAndBearing`（`internal/ledger/bearing_test.go`，复用
   `TestSeatBearingFollowsSeatLifecycle` 形态）：叫机器人落席位与承载 → `CloseCard`
   → 断言 `SeatBearingOf` 不存在、`driver_session/driver_source` 为空；`ReviveCard`
   后可重新 `BindSeat`。先红后绿（§4-11）。
6. 回归两条展示通路（不改码，只跑既有测试并记原文）：
   `go test ./cmd/ -run TestCardWait -count=1` 与
   `go test ./internal/collab/ -run TestSessionTimelineNeedsHuman -count=1`；两条
   必须绿。若因 `EvSeatBearingMissing` 新增常量导致 `internal/collab` 需要处理该
   类型——**不改** `needsHumanByCard`（它只认 `needs_human`/`needs_cleared`），
   T3 的缺承载路径会额外 `MarkNeedsHuman` 让展示通路自然亮起，故 T2 不动 collab。
7. 注释与日志：`clearSeatTx` 写「为什么放事务内」；`wakeconsumer.go` 终态闸与
   收口分支带 `card`/`type`/`status` 上下文日志；成功路径不静默。跑
   `gofmt -l internal cmd`（空）与
   `go test ./internal/ledger/ ./internal/agentd/ ./internal/collab/ ./cmd/ -count=1`。

### T2 测试范围与接缝

最小范围：`./internal/ledger/`、`./internal/agentd/`、`./internal/collab/`、`./cmd/`。
不跑 Web、不跑全仓。

缝级入口：`internal/ledger#Store.CloseCard`（终态写面声明缝）；`automationWakeEvent`
经 `consumeAutomationEventsOnce` 进入（消费声明缝，测试不直调映射函数——既有
`TestB353AutomationMapsCardActionEvents` 即走消费循环）；卡级订阅与会话列表是
**回归缝**（`cmd/card wait` 与 `collab.ListSessions` 的既有测试，入口符号即声明缝）。

本任务无内部锁。

## 7. T3（第 4 片）：认领接线 + 归属解析与路由 + 承载缺失显式路径

### 精确文件集

生产文件：

- `internal/agentd/wakeconsumer.go`
- `internal/agentd/scheddrain.go`
- `internal/ledger/events.go`

测试文件：

- `internal/agentd/wakeconsumer_test.go`
- `internal/agentd/scheddrain_test.go`
- `internal/ledger/events_test.go`

### 精确实现形状

**（a）`internal/ledger/events.go` 新增缺承载报告写面**（检查+追加同事务，幂等）：

```go
// ReportSeatBearingMissing 落一条 EvSeatBearingMissing（契约 §2.3）；同一卡同一
// 席位在承载补齐前只落一次。返回是否本次真的写入，供调用方决定要不要一并打
// 「需要人」标记（避免重复展示）。检查与追加在同一 mutate 事务内，排他靠事务
// 串行化。payload 固定 {"seat":"<identity>"}。
func (s *Store) ReportSeatBearingMissing(cardID, seat string) (bool, error) {
	if cardID == "" || seat == "" {
		return false, fmt.Errorf("缺承载报告参数不完整（card=%q seat=%q）: %w", cardID, seat, ErrBadState)
	}
	written := false
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("缺承载报告读卡 %s: %w", cardID, err)
		}
		rows, err := tx.Query(s.q(`SELECT payload FROM card_events
			WHERE card_id = ? AND type = ? ORDER BY seq DESC`), cardID, EvSeatBearingMissing)
		if err != nil {
			return fmt.Errorf("缺承载报告查历史 %s: %w", cardID, err)
		}
		defer rows.Close()
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				return fmt.Errorf("缺承载报告扫描 %s: %w", cardID, err)
			}
			var payload struct {
				Seat string `json:"seat"`
			}
			if err := json.Unmarshal([]byte(raw), &payload); err != nil {
				return fmt.Errorf("缺承载报告解码 %s: %w", cardID, err)
			}
			if payload.Seat == seat {
				return nil // 同席位已报告过，幂等
			}
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("缺承载报告遍历结束 %s: %w", cardID, err)
		}
		if _, err := s.appendEvent(tx, sink, cardID, EvSeatBearingMissing, "agentd",
			map[string]string{"seat": seat}); err != nil {
			return fmt.Errorf("缺承载报告落事件 %s: %w", cardID, err)
		}
		written = true
		return nil
	})
	if err != nil {
		log().Warn("缺承载报告失败", "card", cardID, "cause", err)
		return false, err
	}
	if written {
		log().Info("缺承载报告已落", "card", cardID, "seat", seat)
	}
	return written, nil
}
```

**（b）`internal/agentd/scheddrain.go` 归属解析**：`wakeCoordinatorRoundRaw` 的
「无承载」分支替换为显式路径，远端分支替换为转交调用（T4 提供
`transferCoordinatorWake`；T3 先落 `reportSeatBearingMissing` 与远端判定，转交在 T4
接入——为保持 T3 可独立测试，T3 阶段远端分支仍返回
`errRemoteBearingNotTransferable`，T4 删除该占位）：

```go
// reportSeatBearingMissing 是缺承载的显式路径（契约 §2.3）：落恰一条
// EvSeatBearingMissing（幂等），并按「需要人」展示（额外 MarkNeedsHuman，
// 让卡级订阅与会话列表待办照常亮起），本轮该卡跳过。
func (s *Server) reportSeatBearingMissing(card, seat string) error {
	written, err := s.ledger.ReportSeatBearingMissing(card, seat)
	if err != nil {
		return err
	}
	if !written {
		s.log.Info("席位缺承载已报告过，跳过重复展示", "card", card)
		return nil
	}
	if err := s.ledger.MarkNeedsHuman(card,
		"协调者席位缺承载记录：请 handoff card seat bearing set <id> --carrier <carrier>，"+
			"或 card rebind --launch 重建会话", "agentd"); err != nil {
		s.log.Error("缺承载落地后打等人标记失败", "card", card, "cause", err)
		return err
	}
	s.log.Warn("协调者席位缺承载记录，已落需要人展示", "card", card, "seat", seat)
	return nil
}
```

`wakeCoordinatorRoundRaw` 无承载分支改为：

```go
	if !hasBearing {
		if err := s.reportSeatBearingMissing(card, current.DriverSession); err != nil {
			return zero, fmt.Errorf("卡 %s 缺承载显式路径: %w", card, err)
		}
		return zero, nil
	}
```

**（c）`internal/agentd/wakeconsumer.go` 认领接线**：新增三个助手，并把消费循环
改成「先认领、拿到的才处理、处理后收尾」，游标用终局前缀水位：

```go
// wakeClaimHolder 是本机器 + 本 agentd 实例的认领者标识（契约 §3.1 holder）。
func (s *Server) wakeClaimHolder() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "agentd"
	}
	return fmt.Sprintf("%s#%d", host, os.Getpid())
}

// claimWakeBatch 在占名额与试跑之前逐 seq 认领本批事件（契约 §3.1.4）；拿不到
// 的 seq 表示他机持有，本机跳过、不占名额、不发起回合。返回实际拿到的 seq。
func (s *Server) claimWakeBatch(card string, seqs []int64) ([]int64, error) {
	if s.ledger == nil {
		return nil, fmt.Errorf("唤醒认领缺少账本")
	}
	holder := s.wakeClaimHolder()
	claimed := make([]int64, 0, len(seqs))
	for _, seq := range seqs {
		got, err := s.ledger.ClaimWake(seq, card, holder, wakeClaimTTL)
		if err != nil {
			return claimed, fmt.Errorf("认领唤醒事件 seq=%d card=%s: %w", seq, card, err)
		}
		if !got {
			s.log.Info("唤醒事件已被他机认领，本机跳过", "seq", seq, "card", card, "holder", holder)
			continue
		}
		claimed = append(claimed, seq)
	}
	return claimed, nil
}

// completeWakeBatch 收尾本批认领；失败只留日志，不覆盖唤醒结果（认领行会在
// 租约到期后被他机接管，不会永久挡路）。
func (s *Server) completeWakeBatch(seqs []int64) {
	if s.ledger == nil {
		return
	}
	holder := s.wakeClaimHolder()
	for _, seq := range seqs {
		if err := s.ledger.CompleteWake(seq, holder); err != nil {
			s.log.Error("唤醒认领收尾失败", "seq", seq, "holder", holder, "cause", err)
			continue
		}
		s.log.Info("唤醒认领收尾完成", "seq", seq, "holder", holder)
	}
}

// advanceAutomationCursorWatermark 把候选水位收紧成终局前缀水位再推进：在飞的
// 认领挡住推进，避免对端崩溃后该事件被永久跳过（契约 §3.1.3）。
func (s *Server) advanceAutomationCursorWatermark(from, candidate int64) error {
	if s.ledger == nil {
		return s.advanceAutomationCursor(candidate)
	}
	w, err := s.ledger.CursorWatermark(from, candidate)
	if err != nil {
		s.log.Error("终局前缀水位计算失败，不推进游标", "from", from, "candidate", candidate, "cause", err)
		return err
	}
	return s.advanceAutomationCursor(w)
}
```

消费循环改造（`wakeconsumer.go` 的 `pending` 结构加 `raw proto.LedgerEvent`；
在 pendingByCard 填充后、逐卡处理时）：

```go
	for _, card := range cards {
		batch := pendingByCard[card]
		// 终态卡闸（§3.2 第 1 步 / §4-32）——T2 步骤 3 已落，逐字保留。
		if cardRow, readErr := s.ledger.GetCard(card); readErr == nil &&
			(cardRow.Status == ledger.StatusDone || cardRow.Status == ledger.StatusClosed) {
			s.keystone.Forget(card)
			for _, item := range batch {
				s.automationMu.Lock()
				s.automationSeen[item.seq] = struct{}{}
				s.automationMu.Unlock()
			}
			s.log.Info("终态卡不再唤醒", "card", card, "status", cardRow.Status,
				"event_count", len(batch))
			continue
		}
		// 认领在占名额与试跑之前（§3.1.4）。
		seqs := make([]int64, 0, len(batch))
		for _, item := range batch {
			seqs = append(seqs, item.seq)
		}
		claimed, claimErr := s.claimWakeBatch(card, seqs)
		if claimErr != nil {
			return processed, escalated, claimErr
		}
		if len(claimed) == 0 {
			continue
		}
		claimedSet := make(map[int64]bool, len(claimed))
		for _, seq := range claimed {
			claimedSet[seq] = true
		}
		evs := make([]keystone.WakeEvent, 0, len(claimed))
		raws := make([]proto.LedgerEvent, 0, len(claimed))
		claimedSeqs := make([]int64, 0, len(claimed))
		for _, item := range batch {
			if !claimedSet[item.seq] {
				continue
			}
			evs = append(evs, item.ev)
			raws = append(raws, item.raw)
			claimedSeqs = append(claimedSeqs, item.seq)
		}
		decision := s.keystone.Decide(evs[0])
		if !decision.Wake {
			s.log.Info("自动化事件因 attach 暂缓", "card", card, "event_count", len(evs), "reason", decision.Reason)
			// attach 暂缓不试跑、不收尾、不推进游标（与既有行为一致：
			// TestAutomationAttachDefersAndThenWakes 断言暂缓期 cursor 文件不写）。
			// 认领留到租约到期——它同时挡住水位推进，正好是我们要的。
			return processed, escalated, nil
		}
		result, wakeErr := s.wakeCoordinatorRoundRaw(ctx, card, evs, raws)
		s.completeWakeBatch(claimedSeqs)
		if wakeErr != nil {
			s.log.Error("自动化事件批次唤醒失败", "card", card,
				"event_count", len(evs), "cause", wakeErr)
			if result.Escalated {
				escalated = true
			}
			// T3 保持既有「失败即结束本轮」形态（B274：失败也推进游标，避免对
			// 同一条消息无限 Launch）。T6 会把它改成 continue + 退避。
			for _, seq := range claimedSeqs {
				s.automationMu.Lock()
				s.automationSeen[seq] = struct{}{}
				s.automationMu.Unlock()
			}
			cursorErr := s.advanceAutomationCursorWatermark(from, maxProcessed)
			if cursorErr != nil {
				return processed, escalated, errors.Join(wakeErr, cursorErr)
			}
			return processed, escalated, wakeErr
		}
		if s.automationRoundHook != nil {
			s.automationRoundHook(card, result)
		}
		if result.Escalated {
			escalated = true
		}
		for _, seq := range claimedSeqs {
			s.automationMu.Lock()
			s.automationSeen[seq] = struct{}{}
			s.automationMu.Unlock()
			processed++
		}
		s.log.Info("自动化事件批次已唤醒", "card", card,
			"event_count", len(evs), "session", result.SessionID,
			"rebuilt", result.Rebuilt, "escalated", result.Escalated)
	}
	if cursorErr := s.advanceAutomationCursorWatermark(from, maxProcessed); cursorErr != nil {
		return processed, escalated, cursorErr
	}
```

> **T3 与 T6 的边界**：T3 保持既有「失败即结束本轮并推进终局前缀水位」形态
> （上文的 `return`），**不做**「不断流」。T6 才把失败分支改成 `continue` 并加退避；
> 这样两个 task 各自的红绿互不污染。T3 的测试只断言认领/水位/归属，不断言跨卡不阻塞。

游标推进处把 `s.advanceAutomationCursor(maxProcessed)` 换成
`s.advanceAutomationCursorWatermark(from, maxProcessed)`（两处：失败早返回处与轮末）。

### 步骤与红绿

1. 写认领接线的失败测试（`internal/agentd/wakeconsumer_test.go`）：
   - `TestB389ClaimExcludesOtherMachine`：预绑定席位后，先用 `env.ledger.ClaimWake(seq, card, "other#1", time.Minute)`
     认领该事件 seq（seq 由 `appendMirroredForConsumer` 返回），再跑消费循环，
     断言 `resumes==0`、游标未越过该 seq（`CursorWatermark` 挡住）。
   - `TestB389LocalWakeCompletesClaim`：正常本机唤醒后，`WakeClaimsBefore(seq+1)` 为空
     （已终局）。
   先跑红（当前无认领）。
2. 实现 `claimWakeBatch/completeWakeBatch/advanceAutomationCursorWatermark` 与循环
   接线；跑
   `go test ./internal/agentd/ -run 'TestB389.*Claim|TestAutomationCursor|TestB349AutomationCursorPersistence|TestAutomationAttachDefers' -count=1`
   取绿。**注意**：`TestAutomationAttachDefersAndThenWakes` 断言 attach 暂缓时
   `cursor < seq` 且不写 cursor 文件——认领接线后 attach 分支必须仍在认领**之前或**
   不收尾且不推进，保持该断言绿。
3. 写归属解析的失败测试（`internal/agentd/scheddrain_test.go`）：
   - `TestB389RemoteBearingDoesNotRunLocally`（§4-23）：承载 `Machine="linux-01"`
     （非本机，`allowCarrierMachines(t, env.srv, "linux-01")`），调用
     `wakeCoordinatorRoundRaw`；断言 `runner.snapshot()` 的 resume/launch 均为 0。
   - `TestB389RemoteBearingKeepsLocalSlotsUnchanged`（§4-24）：同上，断言
     `runningCountIn(t, env.srv.autoLedger, "squad/coord/coord-carrier")` 与
     `"carrier/coord-carrier"` 均为 0。
   先跑红（当前走本机 `LaunchAdmit` 会占名额并 Resume）。
4. 实现归属判定与远端占位出口；跑第 3 步命令取绿（远端断言此时靠
   `errRemoteBearingNotTransferable` 的失败路径保证「本机不跑」）。**T4 接转交后本
   步的占位出口被替换，必须复跑这两支测试确认仍绿**——`errRemoteBearingNotTransferable`
   的失败路径换成转交 HTTP 调用，但「本机零 resume/launch、零名额」不变。
5. 写缺承载显式路径测试。**注意**：`BindSeat(coordinate)` 现在必填承载
   （`validateSeatBearing`），造不出「有 coordinate 席位但无承载行」的合法态——必须先
   正常写承载，再删承载行模拟存量（与 `bearing_test.go:147` 拆表同款手法）：

```go
func TestB389MissingBearingSkipsWithoutSlots(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t) // seedQueueCoordinator 已登记 coord 小队
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID) // 写席位 + 承载（coord-carrier/local）
	deleteSeatBearingRow(t, env, cardID)   // 模拟存量：有 coordinate 席位、无承载行
	appendMirroredForConsumer(t, env.ledger, cardID, "missing", "completed", 1, `{"text":"done"}`)
	processed, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil {
		t.Fatalf("消费: %v", err)
	}
	// 跳过是「消费了但没有回合」：wakeCoordinatorRoundRaw 返回 zero,nil，批次照常
	// 收尾并计 processed（否则该 seq 会一直重试）。断言钉住这一语义。
	if processed != 1 {
		t.Fatalf("缺承载事件应被消费一次（无回合），processed=%d", processed)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 0 {
		t.Fatalf("缺承载不应发起回合: %v", resumes)
	}
	for _, key := range []string{"squad/coord/coord-carrier", "carrier/coord-carrier"} {
		if got := runningCountIn(t, env.srv.autoLedger, key); got != 0 {
			t.Fatalf("缺承载不应占名额 %s=%d", key, got)
		}
	}
	// 认领已收尾：不得让该 seq 永久挡住宿主游标。
	inFlight, err := env.ledger.WakeClaimsBefore(math.MaxInt64)
	if err != nil {
		t.Fatalf("读在飞认领: %v", err)
	}
	if len(inFlight) != 0 {
		t.Fatalf("缺承载事件应已收尾，在飞=%v", inFlight)
	}
}

// deleteSeatBearingRow 用 raw sqlite 删承载行（模拟契约 §2.3 的存量态：
// 有 coordinate 席位、无承载记录）。照抄 wakeconsumer_test.go:92
// appendRawMirroredWithoutSourceTask 的开库手法。
func deleteSeatBearingRow(t *testing.T, env *ledgerEnv, cardID string) {
	t.Helper()
	db, err := sql.Open("sqlite", env.ledgerPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("打开 raw ledger: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`DELETE FROM seat_bearings WHERE card_id = ?`, cardID); err != nil {
		t.Fatalf("删承载行: %v", err)
	}
}
```

   - `TestB389SeatBearingMissingIdempotent`（§4-22）：同上删承载后，直接调用
     `env.srv.reportSeatBearingMissing(cardID, seat)` 两次（`seat` 从
     `prebindConsumerSession` 后读卡取 `DriverSession`），断言卡事件流里
     `EvSeatBearingMissing` 恰一条（用 `env.ledger.EventsFromAsc([]string{cardID},0,10000)`
     计数），且 `NeedsOf(cardID)` 非空（需要人展示亮起）。
   先跑红后绿。
6. 注释与日志：`claimWakeBatch`/`completeWakeBatch`/`advanceAutomationCursorWatermark`
   /`reportSeatBearingMissing` 均有入口+结果日志；导出 `ReportSeatBearingMissing`
   注释写参数/返回/事务边界/幂等；`os` 导入加入 `wakeconsumer.go`。跑
   `gofmt -l internal cmd`（空）与
   `go test ./internal/agentd/ ./internal/ledger/ -count=1`。

### T3 测试范围与接缝

最小范围：`./internal/agentd/`、`./internal/ledger/`。

缝级入口：`consumeAutomationEventsOnce`（消费声明缝，认领与归属都从它进入）；
`Server.wakeCoordinatorRoundRaw`（唤醒声明缝）；`Store.ClaimWake/CompleteWake/WakeClaimsBefore`
（认领声明缝，§4-14–17 已在 `internal/ledger` 锁，本任务只锁其在生产循环里被消费）。
内部锁：无。`reportSeatBearingMissing` 是 `wakeCoordinatorRoundRaw` 的内部分支，
不另设独立缝，测试从 `wakeCoordinatorRoundRaw` 进入。

对应对抗审查：他机持有认领（并发族）、游标前缀阻挡（丢事件族）、远端误本机执行
（路由族）、缺承载重复落事件（幂等族）、席位身份不可解析（非法输入族）。

## 8. T4（第 5 片）：转交面与名额

### 精确文件集

生产文件：

- `internal/proto/ledger.go`
- `internal/client/coordinator.go`
- `internal/agentd/coordapi.go`
- `internal/agentd/scheddrain.go`

测试文件：

- `internal/proto/ledger_wire_test.go`（或既有 wire 测试文件）
- `internal/client/client_test.go`
- `internal/agentd/coordapi_test.go`
- `internal/agentd/scheddrain_test.go`

### 精确实现形状

**（a）`internal/proto/ledger.go` 新增 DTO**：

```go
// CoordinatorWakeReq 是 agentd→agentd 协调者唤醒转交请求（B389 §3.3）。Seat 是
// 承载记录里的席位身份（对端 CAS 见证）；Events 是本批唤醒事件（含 seq），供对端
// 组装简报；Holder 是认领者标识，仅用于对端日志串联。
type CoordinatorWakeReq struct {
	Seat   string        `json:"seat"`
	Events []LedgerEvent `json:"events"`
	Holder string        `json:"holder,omitempty"`
}

// CoordinatorWakeResp 复用拉起响应形状并回报实际执行机器名（不造第二套）。
type CoordinatorWakeResp struct {
	CoordinatorLaunchResp
	HandledBy string `json:"handled_by,omitempty"`
}
```

**（b）`internal/client/coordinator.go`**：

```go
// CoordinatorWake 把本批唤醒事件转交给承载席位所在机器的 agentd 执行（B389 §3.3）。
// 出站方负责加防环头（调用方用 MarkForwarded）；本方法只发一次请求、原样透出
// 非 2xx 错误正文。请求/返回/错误边界见 proto.CoordinatorWakeReq/Resp。
func (c *Client) CoordinatorWake(ctx context.Context, cardID string,
	req proto.CoordinatorWakeReq) (*proto.CoordinatorWakeResp, error) {
	resp, err := c.do(ctx, http.MethodPost,
		"/api/cards/"+url.PathEscape(cardID)+"/coordinator/wake", req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("coordinator wake", resp)
	}
	var out proto.CoordinatorWakeResp
	if err := decodeWire(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
```

**（c）`internal/agentd/coordapi.go` 端点**：路由加一行，处理函数：

```go
	api.HandleFunc("POST /api/cards/{id}/coordinator/wake",
		s.withLedger(s.withCoordinator(s.handleCoordWake)))
```

```go
// handleCoordWake 接收另一台 agentd 转交的协调者唤醒请求（契约 §3.3）。执行前
// 再次校验 Seat 与账本当前 driver_session 相等（CAS），不符返回 409 且不改任何
// 状态；相符则按本机执行分支跑一轮，结果只回执——席位重建由本端自己用
// RebindSeat(expect=旧身份) 落库（共库下请求方读卡自然看到新身份）。
func (s *Server) handleCoordWake(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req proto.CoordinatorWakeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("转交唤醒请求体解码失败", "card", id, "cause", err)
		writeErr(w, http.StatusBadRequest, fmt.Errorf("解析请求体: %w", err))
		return
	}
	if req.Seat == "" || len(req.Events) == 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("seat 与 events 必填"))
		return
	}
	card, err := s.ledger.GetCard(id)
	if err != nil {
		ledgerErr(w, err)
		return
	}
	if card.DriverSession != req.Seat {
		s.log.Warn("转交唤醒席位不符，拒绝且不改状态", "card", id, "holder", req.Holder)
		writeErr(w, http.StatusConflict, fmt.Errorf("席位与账本不符，拒绝转交唤醒"))
		return
	}
	var evs []keystone.WakeEvent
	for _, ev := range req.Events {
		wakes, mapErr := s.automationWakeEvents(ev)
		if mapErr != nil {
			s.log.Warn("转交唤醒事件映射失败", "card", id, "seq", ev.Seq, "cause", mapErr)
			writeErr(w, http.StatusBadRequest, mapErr)
			return
		}
		evs = append(evs, wakes...)
	}
	if len(evs) == 0 {
		writeJSON(w, http.StatusOK, proto.CoordinatorWakeResp{HandledBy: s.localMachineName()})
		return
	}
	s.log.Info("转交唤醒本端执行", "card", id, "holder", req.Holder, "event_count", len(evs))
	result, err := s.wakeCoordinatorRoundRaw(r.Context(), id, evs, req.Events)
	if err != nil {
		s.log.Error("转交唤醒执行失败", "card", id, "holder", req.Holder, "cause", err)
		writeErr(w, http.StatusBadGateway, fmt.Errorf("转交唤醒失败: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, proto.CoordinatorWakeResp{
		CoordinatorLaunchResp: proto.CoordinatorLaunchResp{
			Woke: result.Woke, SessionID: result.SessionID,
			Rebuilt: result.Rebuilt, Escalated: result.Escalated, Output: result.Output,
		},
		HandledBy: s.localMachineName(),
	})
}
```

**（d）`internal/agentd/scheddrain.go` 转交实现**（替换 T3 的占位出口）：

```go
// localMachineName 返回本机名（与 IsLocalMachine 的 hostname 判据同源），用于
// 转交响应回报实际执行机器。
func (s *Server) localMachineName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "local"
	}
	return host
}

// transferCoordinatorWake 把唤醒请求转交给承载席位所在机器的 agentd（契约 §3.3）：
// 出站走 target 客户端池并带防环头；对端不可达/超时/409 都视为失败，不重选载体。
func (s *Server) transferCoordinatorWake(ctx context.Context, card, seat, machine string,
	raws []proto.LedgerEvent, holder string) (keystone.RoundResult, error) {
	target, err := s.clientForTarget(machine)
	if err != nil {
		s.log.Error("转交唤醒取目标客户端失败", "card", card, "machine", machine, "cause", err)
		return keystone.RoundResult{}, fmt.Errorf("转交唤醒取目标客户端 %s: %w", machine, err)
	}
	resp, err := target.MarkForwarded().CoordinatorWake(ctx, card, proto.CoordinatorWakeReq{
		Seat: seat, Events: raws, Holder: holder,
	})
	if err != nil {
		s.log.Error("转交唤醒到目标机失败", "card", card, "machine", machine, "cause", err)
		return keystone.RoundResult{}, fmt.Errorf("转交唤醒到 %s: %w", machine, err)
	}
	s.log.Info("协调者唤醒已转交", "card", card, "machine", machine,
		"handled_by", resp.HandledBy, "event_count", len(raws))
	return keystone.RoundResult{
		Woke: resp.Woke, SessionID: resp.SessionID,
		Rebuilt: resp.Rebuilt, Escalated: resp.Escalated, Output: resp.Output,
	}, nil
}
```

`wakeCoordinatorRoundRaw` 的远端分支改为：

```go
	if !scheduling.IsLocalMachine(bearing.Machine) && !s.IsSelfTarget(bearing.Machine) {
		if len(raws) == 0 {
			// D4：队列出队的合成唤醒没有 card_events 行，装不进冻结 DTO。显式失败，
			// 由 drainIgnitionRequest 回填队列并留需要人痕迹；不静默本机执行。
			return keystone.RoundResult{}, fmt.Errorf(
				"协调者承载在远端 %s，但本批唤醒无账本事件（queue_release 合成唤醒）不可转交",
				bearing.Machine)
		}
		return s.transferCoordinatorWake(ctx, card, current.DriverSession, bearing.Machine,
			raws, s.wakeClaimHolder())
	}
```

删除 `errRemoteBearingNotTransferable` 与其在 T3 的引用。

### 步骤与红绿

1. 在 `internal/proto` 写 DTO 的失败 wire 测试（照抄 `internal/proto` 既有
   `ledger_wire_test.go` 的 `json.Marshal`/`Unmarshal` 形态）：
   `TestCoordinatorWakeWire`——`CoordinatorWakeResp{CoordinatorLaunchResp:{Woke:true,SessionID:"s1"},HandledBy:"linux-01"}`
   序列化必须含 `woke`/`session_id`/`handled_by`；`CoordinatorWakeReq` 的 `events`
   数组元素含 `seq`。先红后绿。
2. 在 `internal/client/client_test.go` 写 `Client.CoordinatorWake` 的失败测试
   （照抄既有 `CoordinatorRebind` 测试的 httptest 夹具）：断言方法 `POST`、路径
   `/api/cards/<id>/coordinator/wake`、body 含 `seat` 与 `events`、非 2xx 保留
   错误正文。先红后绿。
3. 写端点接缝测试（`internal/agentd/coordapi_test.go`，照抄 `newNoPTYCoordEnv`
   + `createCoordCard` + `seedCoordinatorSquad`；`prebindConsumerSession` 在本包
   test 文件里可复用，它会写 `coord-carrier`/`local` 承载——但 `newNoPTYCoordEnv`
   的协调者小队是 `seedCoordinatorSquad` 的 `c1`，两者成员名不同。测试必须让承载的
   `Carrier` 与实际登记的小队成员一致：统一用 `seedCoordinatorSquad` 的载体名写承载，
   即 `env.ledger.BindSeat(cardID, "cli:opencode#sess-old", proto.SeatSourceCoordinate,
   ledger.SeatBearing{Carrier:"c1", Machine:"local"})`）：
   - `TestB389WakeEndpointSeatMismatch409`（§4-26）：body 的 `seat` 与账本不符 →
     409，且卡状态/席位不变、`runner` 零调用。
   - `TestB389WakeEndpointExecutesAndRebinds`（§4-27）：body `seat` 与账本相符。
     席位重建只在「resume 失败 → 重建成功且新 session 不同」时发生，故用一个
     `Resume` 返回错误、`Launch` 返回新 `SessionID` 的 runner（照抄
     `coordapi_test.go:51` `fakeCoordRunner` 加两个开关，或直接复用
     `wakeconsumer_test.go:813` `fallbackConsumerRunner` 的 failResume/failLaunch
     形态并让 Launch 返回 `"sess-new"`）→ 跑 `handleCoordWake` 后断言：响应
     `handled_by` 非空；`env.ledger.GetCard(id).DriverSession == "cli:opencode#sess-new"`
     （本机读卡看到新身份，即对端 `RebindSeat(expect=旧身份)` 落库）。先红后绿。
     另加一条不重建的对照：`Resume` 成功返回原 session 时，`Rebuilt=false` 且本机
     读卡席位不变。
4. 写转交出站测试（`internal/agentd/scheddrain_test.go`）：
   - `TestB389TransferPostsOnceWithBatchSeqs`（§4-25）：用 `httptest` 起一台
     「对端 agentd」（照抄 `forward_test.go:24` 的双 env 形态，
     `newTestAgentdEnvWithCfg` + `Targets{"linux-01": {Addr: remote.ts.URL}}`），
     在 `wakeCoordinatorRoundRaw` 入口进入，断言对端恰收到一次
     `POST /api/cards/{id}/coordinator/wake`、body 的 `events[].seq` 集合等于本批。
   - `TestB389RemoteTransferDoesNotRunLocallyEndToEnd`：本机 runner 计数为 0。
   先红后绿。
5. 注释与日志：DTO 注释含字段语义与 CAS 见证；`CoordinatorWake` 注释含请求/返回/
   错误边界；`handleCoordWake` 注释含 409 语义与「只回执不改本机」；转交函数注释
   含「不重选载体」与防环头。跑 `gofmt -l internal cmd`（空）与
   `go test ./internal/proto/ ./internal/client/ ./internal/agentd/ -count=1`。

### T4 测试范围与接缝

最小范围：`./internal/proto/`、`./internal/client/`、`./internal/agentd/`。

缝级入口：`Client.CoordinatorWake`（出站声明缝）；`POST /api/cards/{id}/coordinator/wake`
（入站声明缝，经真实 httptest + auth + mux）；`Server.wakeCoordinatorRoundRaw`
（转交触发缝）。三条缝各有缝级断言。

对应对抗审查：body 席位不符（CAS 族）、对端不可达（网络族）、重复发 POST（恰好一次
族）、序列化字段缺失/零值（wire 族）、防环头缺失（转发族）。

## 9. T5（第 6 片）：冻结载体准入的路由矩阵与名额断言

### 精确文件集

测试文件：

- `internal/scheduling/admit_seat_carrier_test.go`（补齐成员/角色/名额矩阵）
- `internal/agentd/scheddrain_test.go`（唤醒路径断言不重选载体）

生产文件：无（`AdmitSeatCarrier` 在 T1 已落、接口在 T1 已加）。本片是把 §3.4 的
行为从声明缝锁死——若 T1 的测试已覆盖，本片只补 §4-28/29 的**路由矩阵**。

### 精确实现形状（测试）

`internal/scheduling/admit_seat_carrier_test.go` 追加（复用 T1 已落的
`newSeatCarrierFixture`；反例用 `newFrozenFixture` 的 executor 小队 `S`）：

```go
// 依赖 newFrozenFixture 的 executor 小队 S 作反例。
func TestAdmitSeatCarrierRejectsNonMember(t *testing.T) {
	svc, facade := newSeatCarrierFixture(t)
	if _, err := svc.AdmitSeatCarrier("CS", "B"); err != nil {
		t.Fatalf("冻结成员应准入成功: %v", err)
	}
	if got := runningCount(t, facade, "squad/CS/B"); got != 1 {
		t.Fatalf("冻结成员计数 squad/CS/B=%d，want 1", got)
	}
	if _, err := svc.AdmitSeatCarrier("CS", "NOPE"); !errors.Is(err, scheduling.ErrRoleMismatch) {
		t.Fatalf("非成员载体应 ErrRoleMismatch，得 %v", err)
	}
	for _, key := range []string{"squad/CS/NOPE", "carrier/NOPE"} {
		if got := runningCount(t, facade, key); got != 0 {
			t.Fatalf("拒绝后计数 %s=%d，want 0", key, got)
		}
	}
}

func TestAdmitSeatCarrierRejectsExecutorSquad(t *testing.T) {
	// S 是 executor 小队（newFrozenFixture）：协调者准入必须拒绝它。
	svc, _ := newFrozenFixture(t)
	if _, err := svc.AdmitSeatCarrier("S", "A"); !errors.Is(err, scheduling.ErrRoleMismatch) {
		t.Fatalf("执行者小队应 ErrRoleMismatch，得 %v", err)
	}
}
```

> 外部测试包 `package scheduling_test` 里 `ErrRoleMismatch` 写
> `scheduling.ErrRoleMismatch`；`admit_seat_carrier_test.go` 与 `scheduling_test.go`
> 同包，`facadeRegistry`/`putOnlineCarrier`/`runningCount`/`newFrozenFixture` 可直接
> 复用（同包测试文件共享符号），无需新导入 harness。

`internal/agentd/scheddrain_test.go` 追加（复用 `seedQueueCoordinator`，把其
`coord-carrier` 的 `MaxConcurrency` 保持 1，并额外登记第二个成员 `coord-carrier-2`）：

```go
// TestB389WakeUsesFrozenCarrierNotLaunchAdmit 锁 §4-29：唤醒只打承载记录里的
// 载体，即使小队里另有可用成员也不做候选遍历。
// 本测试自建双成员协调者小队，不复用 seedQueueCoordinator（后者已登记单成员
// coord 小队，再 PutSquad(expect=0) 会 CAS 冲突）。
func TestB389WakeUsesFrozenCarrierNotLaunchAdmit(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	SetupAutomationForTest(t, env.srv, env.ledger)
	allowCarrierMachines(t, env.srv, "ftm")
	svc := mustScheduling(t, env.srv)
	putOnlineCarrier(t, svc, scheduling.Carrier{
		Name: "coord-carrier", Machine: "local", CLI: "opencode",
		HomeDir: "/tmp/coord-home", Credential: scheduling.CredentialStandalone,
		MaxConcurrency: 1, Status: scheduling.StatusOnline,
	})
	putOnlineCarrier(t, svc, scheduling.Carrier{
		Name: "coord-carrier-2", Machine: "local", CLI: "opencode",
		HomeDir: "/tmp/coord-home-2", Credential: scheduling.CredentialStandalone,
		MaxConcurrency: 1, Status: scheduling.StatusOnline,
	})
	if err := svc.PutSquad(scheduling.Squad{Name: "coord", Role: scheduling.RoleCoordinator,
		Members: []scheduling.SquadMember{
			{Carrier: "coord-carrier", MaxConcurrency: 1},
			{Carrier: "coord-carrier-2", MaxConcurrency: 1},
		}}, 0); err != nil {
		t.Fatalf("登记双成员协调者小队: %v", err)
	}
	runner := &bearingTraceRunner{}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	cardID := createCoordCard(t, env)
	result, err := env.srv.keystone.LaunchForCard(context.Background(), cardID, "coordinate",
		keysclient.SessionSpec{CLI: "opencode"})
	if err != nil {
		t.Fatalf("预绑定协调者会话: %v", err)
	}
	identity, err := proto.EncodeSeatIdentity("opencode", result.SessionID)
	if err != nil {
		t.Fatalf("编码席位: %v", err)
	}
	if err := env.ledger.BindSeat(cardID, identity, proto.SeatSourceCoordinate,
		ledger.SeatBearing{Carrier: "coord-carrier", Machine: "local"}); err != nil {
		t.Fatalf("写初始承载: %v", err)
	}
	// 把承载改冻结到第二个成员：唤醒必须打它，而不是按小队顺序回落到第一个。
	if err := env.ledger.SetSeatBearing(cardID, identity, ledger.SeatBearing{
		Carrier: "coord-carrier-2", Machine: "local", HomeDir: "/tmp/coord-home-2",
	}); err != nil {
		t.Fatalf("写冻结承载: %v", err)
	}
	appendMirroredForConsumer(t, env.ledger, cardID, "frozen", "completed", 1, `{"text":"done"}`)
	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
		t.Fatalf("消费: %v", err)
	}
	if refs := runner.snapshot(); len(refs) != 1 {
		t.Fatalf("应唤醒一次，实得 %d", len(refs))
	}
	// 冻结载体是 coord-carrier-2：名额计数应落在它，而不是首个成员 coord-carrier。
	if got := runningCountIn(t, env.srv.autoLedger, "squad/coord/coord-carrier"); got != 0 {
		t.Fatalf("不得回落到首个成员 coord-carrier，计数=%d", got)
	}
	if got := runningCountIn(t, env.srv.autoLedger, "squad/coord/coord-carrier-2"); got != 0 {
		t.Fatalf("回合结束应归还冻结载体名额，计数=%d", got)
	}
}
```

### 步骤与验收

1. 先确认 T1 已落 `newSeatCarrierFixture` 与
   `TestAdmitSeatCarrierAdmitsFrozenCoordinatorCarrier` 为绿；再补
   `TestAdmitSeatCarrierRejectsNonMember` 与 `TestAdmitSeatCarrierRejectsExecutorSquad`
   （代码见本节前文），跑
   `go test ./internal/scheduling/ -run TestAdmitSeatCarrier -count=1` 取绿。
2. 补 `TestB389WakeUsesFrozenCarrierNotLaunchAdmit`，先确认在「唤醒走承载」的实现
   下为绿（T1/T3 已落）；若为红则说明 T1 的 `AdmitSeatCarrier` 接错了载体，回 T1 修。
3. 断言名额释放：唤醒后 `carrier/coord-carrier-2` 计数为 0（回合结束即归还）。
4. 跑 `go test ./internal/scheduling/ ./internal/agentd/ -run 'AdmitSeatCarrier|TestB389Wake' -count=1`。

### T5 测试范围与接缝

最小范围：`./internal/scheduling/`、`./internal/agentd/`。

缝级入口：`scheduling.Service.AdmitSeatCarrier`（§3.4 声明缝，测试直接调用）；
`Server.wakeCoordinatorRoundRaw` 经 `consumeAutomationEventsOnce`（路由声明缝）。
无内部锁。

## 10. T6（第 7 片）：不堵流与退避

### 精确文件集

生产文件：

- `internal/agentd/server.go`（新增字段）
- `internal/agentd/wakeconsumer.go`（失败改 `continue` + 退避）

测试文件：

- `internal/agentd/wakeconsumer_test.go`

### 精确实现形状

**（a）`internal/agentd/server.go`** Server 结构体在 `automationSeen` 附近加：

```go
	// automationBackoff 记录每张卡最近一次失败唤醒的 seq 与退避截止时刻（B389
	// §3.5.4）：同卡同 seq 在退避窗内不重复试跑，防止失败事件反复引发唤醒。
	// 进程内存：重启后重试一次是安全方向（不丢事件）。
	automationBackoff map[string]wakeBackoff
```

类型定义放 `wakeconsumer.go`：

```go
// wakeBackoff 记录一张卡的退避状态。
type wakeBackoff struct {
	Seq      int64
	Until    time.Time
	Attempts int
}
```

**（b）`wakeconsumer.go`** 失败分支从「推进游标并 return」改为「退避 + 继续下一卡」，
轮末统一推进水位：

```go
		if wakeErr != nil {
			s.log.Error("自动化事件批次唤醒失败", "card", card,
				"event_count", len(evs), "cause", wakeErr)
			s.recordWakeBackoff(card, maxSeqOf(claimedSeqs))
			if result.Escalated {
				escalated = true
			}
			// 单卡失败不断整轮（契约 §3.5.4 / §4-30）：继续处理同批与后续卡，
			// 轮末统一推进终局前缀水位。失败已 CompleteWake，游标不会被它永久挡。
			for _, seq := range claimedSeqs {
				s.automationMu.Lock()
				s.automationSeen[seq] = struct{}{}
				s.automationMu.Unlock()
			}
			continue
		}
```

新增退避助手：

```go
// shouldSkipByBackoff 判断该卡本批最大 seq 是否仍在退避窗内（同 seq 不重复试跑）。
func (s *Server) shouldSkipByBackoff(card string, seq int64) bool {
	s.automationMu.Lock()
	defer s.automationMu.Unlock()
	state, ok := s.automationBackoff[card]
	if !ok || state.Seq != seq {
		return false
	}
	return time.Now().Before(state.Until)
}

// recordWakeBackoff 记录一次失败退避；超过 wakeRetryMax 后不再自动试跑——退避窗口
// 按指数延长，且失败已由 keystone 的 Escalated 路径落 needs_human 展示。
func (s *Server) recordWakeBackoff(card string, seq int64) {
	s.automationMu.Lock()
	defer s.automationMu.Unlock()
	if s.automationBackoff == nil {
		s.automationBackoff = make(map[string]wakeBackoff)
	}
	state := s.automationBackoff[card]
	if state.Seq != seq {
		state = wakeBackoff{Seq: seq}
	}
	state.Attempts++
	delay := wakeRetryBackoff
	if state.Attempts > wakeRetryMax {
		delay = wakeRetryBackoff * time.Duration(state.Attempts)
	}
	state.Until = time.Now().Add(delay)
	s.automationBackoff[card] = state
	s.log.Info("唤醒失败进入退避", "card", card, "seq", seq,
		"attempts", state.Attempts, "until", state.Until.Format(time.RFC3339))
}
```

在逐卡处理的认领之后、试跑之前插入退避闸：

```go
		if s.shouldSkipByBackoff(card, maxSeqOf(claimedSeqs)) {
			s.log.Info("唤醒退避窗内，跳过同 seq 重复试跑", "card", card)
			s.completeWakeBatch(claimedSeqs)
			continue
		}
```

`maxSeqOf` 是三行纯函数：

```go
func maxSeqOf(seqs []int64) int64 {
	var max int64
	for _, seq := range seqs {
		if seq > max {
			max = seq
		}
	}
	return max
}
```

**（c）轮末**：把失败早返回移除后，`consumeAutomationEventsOnce` 在所有卡处理完后
统一 `advanceAutomationCursorWatermark(from, maxProcessed)`（T3 已落）。若本轮有卡
失败，函数末尾返回 `errors.Join` 收集的第一个错误，但**不影响**其他卡已被处理与
游标已推进。

### 步骤与红绿

1. 写跨卡不阻塞的失败测试 `TestB389WakeFailureDoesNotBlockSiblingCard`（§4-30）：
   两张卡各预绑定席位；用「第一张卡的 Resume 抛错、第二张正常」的 runner
   （照抄 `fallbackConsumerRunner` 加一个按 card 判定的失败开关），断言第二张卡
   `resumes` 命中、`processed==1`。先跑红（现有实现失败即 return，第二张不处理）。
2. 写退避失败测试 `TestB389WakeBackoffSkipsSameSeqNextRound`（§4-31）：同卡失败一次
   后把游标重置到该事件之前（模拟他机/重放），再跑一轮，断言 runner 调用次数不增加。
   先红后绿。
3. 实现 `continue` + 退避映射；跑
   `go test ./internal/agentd/ -run 'TestB389|TestAutomationWakeFailure|TestAutomationFallback' -count=1`
   取绿。核对既有 `TestAutomationWakeFailureAdvancesCursor` 仍绿（游标必须推进）。
4. 注释与日志：退避常量注释含为什么取 30s/3 次；`recordWakeBackoff`/`shouldSkipByBackoff`
   入口与结果日志带 `card`/`seq`/`attempts`；成功路径不静默。跑
   `gofmt -l internal cmd`（空）与 `go test ./internal/agentd/ -count=1`。

### T6 测试范围与接缝

最小范围：`./internal/agentd/`。

缝级入口：`consumeAutomationEventsOnce`（消费声明缝，跨卡不阻塞与退避都从它进入）。
无内部锁。

对应对抗审查：A 卡失败拖死 B 卡（隔离族）、同 seq 无限重试（自激族）、游标被失败卡
卡死（游标族）、退避跨卡串扰（状态族）。

## 11. 五项法定自审

### 11.1 缺陷族对抗审查

| 缺陷族 | 设问 | 本计划的具体结论/锁点 |
| --- | --- | --- |
| 身份/旧数据 | 存量 coordinate 席位无承载会怎样？ | T3 的 `reportSeatBearingMissing` 落恰一条事件 + 打等人 + 跳过；T1 的 `SetSeatBearing` 是修复出口；测试 `TestB389MissingBearingSkipsWithoutSlots`、`TestB389SeatBearingMissingIdempotent`、`TestSetSeatBearingRepairsMissingRecord`。 |
| 并发/CAS | 共库两台同时消费同一事件？ | T3 的 `claimWakeBatch` 在占名额前逐 seq `ClaimWake`；认领键是 `seq` 本身（第 1 片已锁）；对端转交时 T4 再校验 `Seat`（CAS 409）。测试 `TestB389ClaimExcludesOtherMachine`、`TestB389WakeEndpointSeatMismatch409`。 |
| 来源分支 | bind/空座/非法席位会不会被本机跑？ | T3 保留空座/bind/非法三分支（跳过或报错），不为它们落 `EvSeatBearingMissing`；测试 `TestAutomationWakeDoesNotWakeBindParent` 仍绿 + T2 终态闸。 |
| 权限/出示 | 转交端点的 Seat 从哪来？ | 只有承载记录里的 `DriverSession`（T3 从账本读）；对端 409 不改状态；端点落 `s.auth(mux)` 内，Bearer 一致。 |
| 兼容/回退 | 远端载体旧行为是否复活？ | T1 远端分支显式失败；T4 才接转交。旧 `TestCoordLaunchRejectsRemoteCarrier`（本机 print 拒远端载体）不改、必须仍绿。 |
| 序列化 | `CoordinatorWakeResp` 的复用形状会不会丢字段？ | T4 的 `TestCoordinatorWakeWire` 锁 `woke`/`session_id`/`handled_by`；`Events` 的 `seq` 非零可辨。 |
| 进程重启 | 认领与 cursor 在重启后？ | 认领落共享账本（第 1 片），`advanceAutomationCursorWatermark` 只推进到终局前缀；`TestB389ClaimExcludesOtherMachine` 断言在飞认领挡游标。 |
| 资源/N+1 | 唤醒会不会泄漏名额或进程？ | T1/T5 断言 `carrier/*` 与 `squad/*` 计数在回合结束后归零；`TestB389RemoteBearingKeepsLocalSlotsUnchanged` 断言远端不占名额。 |
| 自激 | 失败事件会不会再引发唤醒？ | T2 把 `needs_human`/`seat_bearing_missing` 移出唤醒映射 + 删自生 seen 补丁；T6 退避。测试 `TestB389NeedsHumanDoesNotWake`、`TestB389WakeBackoffSkipsSameSeqNextRound`。 |

### 11.2 序列化边界设问

新增字段/类型的每一处手写序列化或投影，逐一列进文件清单并加断言：

1. `proto.CoordinatorWakeReq`（`internal/proto/ledger.go`）→ `Client.do` 的
   `json.Marshal` → 对端 `json.NewDecoder`；**另一侧**是 agentd 的
   `handleCoordWake` 解码。锁点：`TestCoordinatorWakeWire`（req 的 `events[].seq`
   非零）+ `TestB389TransferPostsOnceWithBatchSeqs`（穿过真实 HTTP 的 encode/decode）。
2. `proto.CoordinatorWakeResp`（内嵌 `CoordinatorLaunchResp`）→ `writeJSON` 的编码 →
   `Client` 的 `decodeWire`。锁点：`TestCoordinatorWakeWire` 断言
   `woke`/`session_id`/`handled_by` 三键；`TestB389WakeEndpointExecutesAndRebinds`
   穿过真实 HTTP 往返。
3. `EvSeatBearingMissing` payload 手搭 map `{"seat": "<identity>"}`（
   `ReportSeatBearingMissing`）→ JSONB/TEXT 列 → `ReportSeatBearingMissing` 的历史
   扫描解码。锁点：`TestB389SeatBearingMissingIdempotent`（同一 seat 重复不落第二条，
   即解码回来的 seat 与写入一致）；`seat` 用非空可辨字符串，不用零值。
4. 承载记录 `SeatBearing` 的列投影（`writeSeatBearing`/`SeatBearingOf`）——第 1 片
   已锁（`bearing_test.go`）；本计划新增的 `SetSeatBearing` 复用同一写读面，锁点
   `TestSetSeatBearingRepairsMissingRecord` 断言往返恒等。
5. `CoordinatorWakeResp.HandledBy` 的「字段缺失 vs 值为零」：用 `omitempty` 时缺失
   与空串不可分；本计划对 `handled_by` 也保留 `omitempty`，但断言只在**实际执行**
   路径要求非空（`TestB389WakeEndpointExecutesAndRebinds`），空 events 分支不要求——
   两者用不同测试区分，不用可空类型硬造。

推荐武器 roundtrip 属性测试的适用性：`CoordinatorWakeReq` 是纯数据 DTO，适合一条
`reflect.DeepEqual(req, decode(encode(req)))` 属性；本计划在 T4 步骤 1 落这条属性
（随机构造 `Seat`/`Events`/`Holder`，含空 `Events` 与含 `seq` 两种），一条顶一族。

### 11.3 上下文预算检查

- T0：2 个测试文件（改导入）。
- T1：6 生产 + 4 测试，全部有界（`scheduling` 一个方法、`ledger` 一个方法、`agentd`
  一个函数分支、`cmd` 一个新文件）。
- T2：4 生产 + 4 测试；`collab`/`cmd` 只作回归，不改。
- T3：3 生产 + 3 测试；`wakeconsumer` 的批次循环是唯一焦点。
- T4：4 生产 + 4 测试；转交面是单一职责。
- T5：2 测试（无生产）。
- T6：2 生产 + 1 测试。

每个 task 都圈得出有界文件集；`internal/agentd` 与 `cmd` 是扁平大包，但本卡只落
「唤醒切片」（`wakeconsumer.go`/`scheddrain.go`/`coordapi.go` 的协调者路径）与
「seat 命令切片」（`card_bearing.go`），不按目录整包切。不插额外竖切还债卡。

### 11.4 类型标注（边界型子系统的真机清单）

边界类型显式列出：`ledger.SeatBearing`、`scheduling.Binding`、`keysclient.SessionSpec`、
`proto.LedgerEvent`、`proto.CoordinatorWakeReq/Resp`、`proto.SeatSource`、
`ledger.EvSeatBearingMissing`。最终卡级真机验收（**本 task 由协调者执行，不派发**）：

- 本机侧 `resumes==0 && launches==0` 且协调者两键计数不变（远端归属）；
- 对端 `linux-01` 用 `cmd` 载体 resume `ses_f45bc164…`，协调者回话，主 agent 在 IM
  里看到（spec §4.3 真机验收链）；
- `go build ./...`、`go vet ./...`、`go test ./...` 绿；`gofmt -l` 为空（spec §4.4）。

### 11.5 接缝覆盖（双向）

**测试 → 缝**：每支测试的入口调用符号都在下列声明缝上或穿过它：

- T1：`AdmitSeatCarrier`、`wakeCoordinatorRoundRaw`、`card seat bearing set`。
- T2：`Store.CloseCard`、`consumeAutomationEventsOnce`（内含 `automationWakeEvent`）、
  `cmd/card wait` 与 `collab.ListSessions`（回归）。
- T3：`consumeAutomationEventsOnce`、`wakeCoordinatorRoundRaw`。
- T4：`Client.CoordinatorWake`、`POST .../coordinator/wake`、`wakeCoordinatorRoundRaw`。
- T5：`AdmitSeatCarrier`、`consumeAutomationEventsOnce`。
- T6：`consumeAutomationEventsOnce`。

**缝 → 测试**：本卡声明的每条缝至少被一支缝级断言锁住——`AdmitSeatCarrier`（T1/T5）、
`wakeCoordinatorRoundRaw`（T1/T3/T4/T5）、`CoordinatorWake` + 端点（T4）、
`card seat bearing set`（T1）、`CloseCard`（T2）、`consumeAutomationEventsOnce`
（T2/T3/T6）。无「锁不住的缝」，故不提请删缝。

**内部锁**：本计划不新增内部锁。`reportSeatBearingMissing`、`claimWakeBatch`、
`recordWakeBackoff` 等均从声明的消费/唤醒缝进入测试，不单独立缝替代。

**退路同闸**：本计划步骤中无「若意外先绿就改成直喂 X」这类会改变测试入口符号的
条件退路；T3 的「远端占位出口」与 T6 的「先保留 return 形态」是**跨 task 的显式
边界**（已在 §3 DAG 与各 task 正文声明），不是测试入口的退路。

### 11.6 跨卡审计的适用性

本节点是**单份 plan**（一张卡第 2–7 片），不是多子卡扇出，故不触发 charter-plan
的「跨卡审计」法定步骤（它只约束「各子卡 plan 齐稿后、派发前」）。本计划已用
§2.1 的 Consumes/Produces 表逐字对齐各 task 间的签名（T1→T3 的 `SeatBearingOf`、
T3→T4 的 `wakeCoordinatorRoundRaw`/`raws`、T4→T2 的 `CoordinatorLaunchResp` 复用），
即跨卡审计三样中的第 2 样；对照冻结物（契约）逐条见 §2.4 覆盖矩阵；spec 故事逐条
归属见 §2.4。出稿者即本节点，单上下文自审结论按纪律标「待拍板」。

## 12. 自审三查、占位符声明与收口顺序

### 12.1 spec 覆盖（逐条能指到 task）

- 契约 §4 第 1–10、12、13：Ticket 0 已落（`bearing_test.go`）。
- 第 11 条：T2。第 14–17：第 1 片已落（`wakeclaim_test.go`）。
- 第 18–20：T2。第 21–22：T3。第 23–24：T3。第 25–27：T4。第 28–29：T1/T5。
- 第 30–31：T6。第 32：T2。
- 契约 §7 移交项：`seat_bearings`/`wake_claims` DDL 与迁移（Ticket 0 已落）；
  `BindSeat`/`RebindSeat` 生产与测试调用点（Ticket 0 已落）；
  `handoff card seat bearing set`（T1）；转交客户端退避常量与「需要人」文案（T4/T6）。
- spec §2 用户前提：1/3（首次记录、后续不重选）→ T1/T5；2（共库只处理一次、路由到
  载体机器）→ T3/T4；4（不接受远端跳过）→ T3/T4；5（needs_human 不唤醒但保留两条
  展示）→ T2；两个自激缺陷 → T2/T6。

### 12.2 占位符扫描声明

本稿不使用 TBD、无「加适当的错误处理」、无「同 Task N」式引用、无「描述做什么却不
给代码」。所有测试给出具体测试名与逐条断言；测试复用仓库既有 harness
（`newNoPTYAutomationEnv`、`seedQueueCoordinator`、`newNoPTYLedgerEnv`、`newNoPTYCoordEnv`、
`newFrozenFixture`、`putOnlineCarrier`、`runningCount`、`runLedgerCLI`、
`newTestAgentdEnvWithCfg`、`forward_test.go` 的双 env 形态），因这些 harness 的构造字段
随包定义，本稿采用允许的「既有 harness + 逐条列全断言」形式，并在此自我声明；没有
用内部锁替代声明缝。

### 12.3 收口命令

实现者完成 T6 后依次执行：

```text
go test ./internal/scheduling/... ./internal/ledger/... ./internal/keystone/... ./internal/collab/... ./internal/client/... ./internal/proto/... ./internal/agentd/... ./cmd/... -count=1
go build ./... && go vet ./...
gofmt -l internal cmd
git diff --check
git status --short --branch
```

全量命令只作为卡级收口，不归任何单个 task 的最小测试范围；它必须在所有 task 局部
绿测完成后运行。提交前将实际 commit 命令及原始输出追加台账，再只 amend 一次收进
同批提交；最终判据是工作树干净，台账不追写 amend 后 hash。

### 12.4 本节点收口自查

① 有没有把没亲自跑到结果的命令写成结论？——§1.1/§1.2 全部是本节点亲自跑的原始输出；
其余为设计推演，未冒充实测。② 这一轮碰过 handoff CLI 或起过新 executor 吗？——没有；
只用 `git` 与 `codegraph` 只读查询，未派发、未起子任务。
