# B448 实现台账（implement 节点）

- 分支：`cards/B448-charter-1`，基线 `08baeae1`（plan 已拍板，Q1–Q5 全取甲）
- 执行者：implement subagent（本会话内）
- 纪律：charter implement（先红后绿、三段律、日志与注释、修复熔断、结论纪律）

## 事实流水

### F-1 承重假设（plan §4 第 1 步）成立

- 夹具：`internal/agentd/b448_pair_test.go`——两台 `Server` 共用一个 `*ledger.Store`（sqlite 文件），各自独立
  store / DataDir / httptest 监听 / 捕获式 slog；A `cfg.Name=t448-mac`、`targets={t448-linux: B 监听地址}`，
  B `cfg.Name=t448-linux`、无 console_user。两台各自 `SetupAutomationForTest`。
- 命令：`go test ./internal/agentd/ -run TestB448Pair -count=1 -v`
- 输出：`--- PASS: TestB448PairSharedLedgerOverRealHTTP (0.03s)`
- 证明三件事：A `clientForTarget("t448-linux").BaseURL()==B.ts.URL` 且经池 http.Client 打 B `/api/ledger/health` 200；
  `IsSelfTarget("t448-linux")` B 真 A 假；A `LaunchAdmit` 后 B 读 `carrier/t448-oc`=1，B `Release` 后两级归零。
- 判断：scheduling.Service 无进程内状态（repo 直读），共享账本下两台计数天然同一份；不需要 mock 对端。

### F-2 DTO 与 client 出站（plan §4 第 2 步）

- 新文件：`internal/proto/firstround.go`（`FirstRoundTransferReq` / `CoordinatorFirstRoundResp` / `ResidentFirstRoundResp` /
  `FirstRoundTransferError` + kind 词表）；`internal/client/firstround.go`（`CoordinatorFirstRound`、`ResidentFirstRound`、
  哨兵 `ErrFirstRoundUnsupported`、类型化 `*FirstRoundError{Status,Kind,Message,SessionID,Pointed}`）。
- 错误体读取上限取 64KiB（不走 httpError 的 4096），保证 kind 解码先于截断（plan §8「依赖库既成行为」第 4 条）。
- 命令：`go test ./internal/client/ -run FirstRound -count=1 -v` → 4 个用例 PASS。
- **诚实记录**：client 出站方法的单测是在实现写完之后补的，没有单独见红（属于纪律偏离，已进「偏离与自主决定」）。
  agentd 侧所有 V 测试均先红后绿（见 F-3）。

### F-3 agentd 卡路径先红（S1/S5/S6/V4/V6）

- 先写 `internal/agentd/b448_card_test.go`（8 个用例）并改写 `TestCoordLaunchRejectsRemoteCarrier`。
- 首跑撞夹具问题：`seedAgentdLedger(t, st, "bug")` 不含 `attachment-gates` 流 → 建卡失败；改为
  `seedAgentdLedger(t, st, "bug", "attachment-gates")`（夹具修正，不是功能红）。
- 修正后见**断言红**（功能缺失）：卡用例全部得到旧文案 502「print 协调者只支持本机载体，请把协调者小队的成员指向本机载体」；
  收件端用例 `缺转交头` 得 `404 page not found`（端点不存在）；改写后的 `TestCoordLaunchRejectsRemoteCarrier` 红在
  「失败理由应点名载体与目标机」。

### F-4 实现骨架与两条收件端，转绿

- `runOnCoordinatorCarrier` 改收 `coordinatorCarrierPlan`：`frozenCarrier` 空 = 受理端 `LaunchAdmit`，非空 = 收件端
  `AdmitSeatCarrier`；远端分支先显式 `release()`（released 标志防重复 Release）再调 `transfer`，收件端远端即
  `*firstRoundNotLocalError`、零出站；本机分支只把 `workdir`/`launch` 改从 plan 取，其余逐字不动；删除旧 fail-closed 文案。
- 卡：`launchCoordinatorRoundWithExpect` 提供 transfer 回调；新增 `launchCoordinatorRoundFrozen`（卡锁内重查席位 →
  冻结准入 → `bindLaunchedCoordinator` 原样，含 B446 兜底）；席位判定抽成 `coordinatorSeatGate` 两边共用。
- 驻场：`launchSessionResident` 变成 `launchSessionResidentOn(ctx, session, squad, frozenCarrier)` 的薄壳；本体加 transfer
  回调与指针归属 `residentFailureIsMine`（Q3 甲）；`pointResidentLaunchFailure` 改为返回是否已落（收件端回执 pointed）。
- 新文件 `internal/agentd/firstround.go`：受理端出站 + 错误还原 `firstRoundFailure`、两个收件 handler、
  `decodeFirstRoundReq`（指针字段区分缺失与空）、`extendFirstRoundWriteDeadline`（`http.NewResponseController`）。
- 路由：`coordapi.go` 注册 `POST /api/cards/{id}/coordinator/first-round`（withLedger+withCoordinator）；`ledgerapi.go`
  注册 `POST /api/sessions/{id}/resident/first-round`（withRooms+withCoordinator，不包 requireConsoleIdentity）。
- 命令：`go build ./...`、`go vet ./internal/agentd/ ./internal/client/ ./internal/proto/` 均无输出；
  `go test ./internal/agentd/ -run 'B448|TestCoordLaunchRejectsRemoteCarrier' -count=1` → `ok ... 1.614s`。
- S4 回归：`go test ./internal/agentd/ -run 'B448|CoordLaunch|CoordRebind|B444|B446|Coord|Drain|Queue|Wake' -count=1`
  → `ok ... 7.437s`；`git diff --stat` 既有测试文件只动了 `coordapi_test.go`（即唯一允许改写的那条）。

### F-5 驻场路径 V2 / V2a–c / V6 驻场腿

- 新文件 `internal/agentd/b448_resident_test.go`（4 例）。驻场实现已在 ea75943f 随骨架一并落下（收件 handler 共用
  错误映射，拆不开），所以这 4 例在分支上首跑即绿——**先红是在基线上补证的**：
  `git worktree add --detach <scratchpad>/b448-base 08baeae1`，拷入 `b448_pair_test.go` 与 `b448_resident_test.go`，
  `go test ./internal/agentd/ -run TestB448Resident -count=1` → 4 例全红，均为断言红，原文
  `驻场者应在执行机坐下: &{... Error:协调者载体 t448-oc 在机器 t448-linux（远端）：print 协调者只支持本机载体，请把协调者小队的成员指向本机载体}`；
  随后 `git worktree remove --force` 清掉临时 worktree。
- 分支上：`go test ./internal/agentd/ -run TestB448Resident -count=1 -v` → 4 例 PASS。

### F-6 V7 超时与日志

- 新文件 `internal/agentd/b448_timeout_log_test.go`：`TestB448TransferCtxDeadlineCoversTurnTimeout`（F34）、
  `TestB448ReceiptOutlivesServerWriteTimeout`（F35：`coordWakeTurnTimeout` 覆盖成 3s，B 的 server WriteTimeout=200ms，
  B 回合 600ms，A 入口仍 200）、`TestB448TransferLogsOnBothEnds`（卡与驻场两端各一行 ≥Warn 的「…转交完成」日志，
  带 accepted_by/handled_by/carrier/card|session）。三例首跑即绿。
- **放弃的尝试**：想在临时 worktree 里把 `extendFirstRoundWriteDeadline` 置空看 F35 用例变红，命令被沙箱拒绝
  （worktree 隔离会话不许拼装运行期脚本改另一 worktree）。没有再绕；F35 用例的「牙」未由实现侧观测，留给 review 变异复验。

### F-7 触及包整包测试暴露的夹具纪律

- `go test ./internal/agentd/ -count=1` 首跑：`--- FAIL: TestNoDirectHttptestServers`，原文
  `b448_card_test.go:83 仍直接调用 httptest.NewServer(，请改用 internal/testhttp`。
- 判断：仓内既有守卫，B448 测试违反夹具纪律（不是功能问题）。把 b448 测试里 4 处 `httptest.NewServer(` 改成
  `testhttp.NewServer(t, ...)`（自带 Cleanup，删掉对应 `t.Cleanup(x.Close)`）。
- 复跑：`go test ./internal/agentd/ -count=1` → `ok  github.com/Xsxdot/handoff/internal/agentd 75.334s`（整包全绿）。
- `go build ./...` → 无输出；`go vet ./...` → 无输出；`gofmt -l internal/agentd/ internal/client/ internal/proto/` → 空。
- 残留检查：`/usr/bin/grep -rn -e "指向本机载体" -e "只支持本机载体" internal cmd web/src` 只剩改写测试头注释里的一句历史说明。

### F-8 受理端还原表 + 收尾复跑

- 补 `TestB448FirstRoundFailureRestoresTypedErrors`（`b448_timeout_log_test.go`）：F21–F29 的 kind → 类型化错误还原表，
  覆盖竖切里需要跨机竞态才出现的 resident_occupied / read_only / no_room / admission_rejected / cas_conflict。实现之后补写，首跑即绿。
- 收尾命令（本节为最终一次实跑）：
  - `go build ./...` → `BUILD_OK`；`go vet ./...` → `VET_OK`；`gofmt -l internal/agentd/ internal/client/ internal/proto/` → 空
  - `go test ./internal/agentd/ -run 'B448|CoordLaunch|CoordRebind|B444|B446' -count=1` → `ok  github.com/Xsxdot/handoff/internal/agentd 3.478s`
  - `go test ./internal/client/ ./internal/proto/ -count=1` → `ok .../internal/client 9.390s`、`ok .../internal/proto 0.392s`
  - `go test ./internal/agentd/ -run TestB448 -count=1 -v` → 17 例全 PASS
  - 整包 `go test ./internal/agentd/ -count=1` 在 F-7 跑过一次全绿（75.334s），之后只加了一个纯函数测试。

---

# 交回物

## 偏离与自主决定

1. **纪律偏离：client 出站单测与驻场实现未按「先红」顺序。** client `firstround_test.go` 写在实现之后、未单独见红；驻场实现
   随骨架同一提交落下，V2 系测试在分支上首跑即绿——为此在基线 `08baeae1` 的临时 worktree 上补跑了同一组驻场测试，4 例均为
   断言红（旧 fail-closed 文案），见 F-5。不在 plan 决策权限内（这是纪律而非设计决定），如实披露。
2. **纪律偏离：F35（写截止）用例的「红」未观测。** 想在临时副本里置空 `extendFirstRoundWriteDeadline` 看它变红，命令被沙箱拒绝，
   没有绕。用例是否有牙留给 review 变异复验。不在授权内，如实披露。
3. **不可达文案在 <原因> 之后追加提示**「（若请求已送达，首轮可能仍在该机执行或已完成，请刷新查看）」，只在
   ErrUnreachable / ErrTunnelDisconnected / ctx 超时时追加。前缀与 spec 文案逐字一致；提示来自 plan §7「部分失败」行的要求。
   **边界情形**：plan §6 规定入口文案改动须升级，除 spec 三条之外；我判断它落在 spec 文案的 <原因> 槽内，请协调者确认或要求删掉。
4. **F31 的读法：「pointed 为假才写」理解为必要条件，不是充分条件。** 受理端拿到回执、kind 为 resident_occupied / read_only /
   no_room（请求错）时，即使 pointed=false 也不写指针行，与 B444「请求错不落指针行」一致。不改冻结条目，只是解释它；请协调者确认。
5. **收件端 not_local 的驻场路径由执行机写指针行**（回执 pointed=true，受理端不写），与「谁跑了骨架谁写」一致。F30 是
   当且仅当的关系，仍然成立。属实现细节，自主决定。
6. **受理端对 kind=not_found / round_failed / invalid / 未知 kind 的入口文案**：not_local → 收件端文案 +「（执行机 M）」；
   round_failed →「执行机 M 首轮失败：<原因>」；invalid →「执行机 M 拒绝了不成形的首轮转交请求（受理机缺陷）：…」；
   not_found / 未知 →「执行机 M 回 HTTP n：<原文>」，全部映射到入口 502（满足 F19/F28/F29「透出原因」）。措辞自主决定。
7. **V3 夹具构造与 plan 字面不同。** plan 写的是「B 侧先手动 acquire 一次」，但两机共享账本，这样做 A 的 `LaunchAdmit` 在本机就
   直接 NoSlot，根本走不到转交。改为在 B 前挂透传代理，在 A 已归还、B 准入之前 acquire，模拟跨机竞态，这样 B 侧 no_slot 的 wire
   路径才真正走到。属测试设计，自主决定。
8. **骨架签名改为结构体 `coordinatorCarrierPlan`；席位判定抽成 `coordinatorSeatGate`**，供首轮与收件变体共用。在授权内
   （§6「骨架用回调还是策略结构体」「私有函数与文件拆分」）。
9. **余量 `firstRoundTransferSlack = 1m`**，出站截止与收件写截止都取 `coordWakeTurnTimeout + 1m`。在授权内（§6 余量）。
10. **handled_by / accepted_by 取配置自名，取不到时用 hostname**（`firstRoundSelfName`）；续轮 wake 的 handled_by 仍取 hostname，
    没有改动。自主决定（DTO 与日志字段值的取法）。
11. **client 错误体读取上限 64KiB**，不复用 httpError 的 4096 截断，以保证 kind 解码先于截断。落在 plan §8「依赖库既成行为」第 4 条
    给的两个选项之内。
12. **收件端读卡出现非 NotFound 错误时回 502 round_failed**；squad 名不能作驻场身份（lookupErr）时回 400 invalid。
    自主决定（kind 词表内选值）。
13. **改写的 `TestCoordLaunchRejectsRemoteCarrier` 额外断言受理机 Runner 零次 Launch。** 在授权内（唯一允许改写的既有测试，计数断言保留）。
14. 未改 `cmd/`、`web/`、scheduling / ledger / collab / keystone、`cmd/agentd.go` 超时；`handleCoordLaunch` / `handleCoordRebind` /
    `handleSessionResidentLaunch` 的错误映射一字未改；§8 冻结条目未改。
15. **补报原漏报：F20 中「会话不存在」实现为 409 `kind=no_room`，不是 plan 原文的 not_found。** 收件端 `launchSessionResidentOn`
    在会话不存在时返回 `collab.ErrNoRoom`，`firstRoundErrorKind` 映射为 no_room（即 F26），受理端还原为 `collab.ErrNoRoom`
    → 入口 404，与本机路径 `sessionsapi.go` 一致。原交回物漏报此项；协调者已裁决取 no_room 并回写 plan §8 F20 修订 r1
    （6a32d2e8）。review 修复轮补了 wire 断言，见文末「review 修复轮」节。

## 提交列表

- `7df9d43f` feat(B448): 首轮转交 wire DTO 与 client 出站方法
- `ea75943f` feat(B448): 协调者首轮按载体机转交（骨架收口 + 卡/驻场收件端）
- `b8d0e8ba` test(B448): 驻场首轮转交竖切、超时预算与两端转交日志
- （本提交）test(B448): 受理端 kind 还原表 + 台账交回物

## 故事 / 验收 → 测试（本轮实跑：`go test ./internal/agentd/ -run TestB448 -count=1 -v` 17 例 PASS；client 4 例 PASS）

- S1 / V1：`TestB448CardLaunchTransfersToCarrierMachine`；V1 反例：`TestB448CardSelfSeatAdoptedOnCarrierMachine`、`TestB448CardSeatStolenOnCarrierMachine`；
  Q1 换绑：`TestB448TransferWireShapeAndRebind`
- S2 / V2：`TestB448ResidentCreateTransfersToCarrierMachine`（建会话）、`TestB448ResidentFailureOnCarrierPointsOnceThenRetry`
  （V2a + resident/launch 入口 + V2c）、`TestB448ResidentUnreachablePointsOnceByAcceptor`（V2b）
- S3：CLI 未改，三条命令都打本机 HTTP，随 S1/S2 生效；**机内未验**，交真机 R5
- S4：`go test ./internal/agentd/ -count=1` 整包全绿；既有测试只改写了 `TestCoordLaunchRejectsRemoteCarrier`
- S5 / V5：`TestB448CardNotLocalRejectsWithoutSecondHop`
- S6 / V3：`TestB448CardNoSlotOnCarrierMachine`（入口 409 同文案 + launch_queue 回填 + F22 直证）；回合中计数 1/1 在 V1 断言；
  每例收尾 `assertZeroCounts`
- V4：`TestB448TransferWireShapeAndRebind`（4a）、`TestB448FirstRoundWireValidation`（4b/4c + F20），以及 client 的 `TestFirstRound*`
- V6：`TestB448CardOldPeerReportsUpgrade`、`TestB448ResidentOldPeerPointsOnce`
- V7：`TestB448TransferCtxDeadlineCoversTurnTimeout`、`TestB448ReceiptOutlivesServerWriteTimeout`、`TestB448TransferLogsOnBothEnds`
- 承重假设：`TestB448PairSharedLedgerOverRealHTTP`；还原表：`TestB448FirstRoundFailureRestoresTypedErrors`
- 改写的既有测试：`TestCoordLaunchRejectsRemoteCarrier`（PASS）

## §8 冻结条目落点

| 条目 | 代码 | 测试 |
|---|---|---|
| F1 | `coordapi.go` 路由 + `handleCoordFirstRound` | CardLaunch / TransferWireShape（路径）/ WireValidation |
| F2 | `ledgerapi.go` 路由 + `handleResidentFirstRound` | Resident* / WireValidation |
| F3 | `transfer*FirstRound` 的 `MarkForwarded()` | TransferWireShape（头为 1） |
| F4 | `decodeFirstRoundReq` | WireValidation「缺转交头」 |
| F5 | 路由不包 requireConsoleIdentity | B 无 console_user，Resident* 全绿 |
| F6/F7 | `proto.FirstRoundTransferReq` 无 omitempty | client `TestFirstRoundRequestShapeAndSuccess`；TransferWireShape |
| F8–F11 | `decodeFirstRoundReq` 指针字段 | WireValidation（缺失 / 为空 各一条，两个端点） |
| F12 | accepted_by 不校验 | TransferWireShape（值）；WireValidation not_found 例不带该键也能过校验 |
| F13 | `launchCoordinatorRoundWithExpect` 的 transferExpect | TransferWireShape；client RequestShape |
| F14 | `launchCoordinatorRoundFrozen` rebind=expect!="" | TransferWireShape（第二回合落座） |
| F15 | skeleton `frozenCarrier` → `AdmitSeatCarrier` | CardNoSlot（B 准入看到满员） |
| F16/F17 | 两个收件 handler 回执 | CardLaunch、Resident*（经 client 解码） |
| F18 | 受理端只返回 RoundResult / Outcome | CardLaunch、`b448CreateSession`、FailureThenRetry |
| F19 | `firstRoundFailure` default | 还原表「未知 kind」；client `WithoutKindKeepsRawText` |
| F20 | 收件端 not_found 回 409 | WireValidation |
| F21 | 404 哨兵 → 版本过旧文案 | CardOldPeer、ResidentOldPeer、client 404、还原表 |
| F22 | no_slot → AdmissionError(ErrNoSlot) | CardNoSlot、还原表 |
| F23/F24 | admission_rejected / cas_conflict | 还原表（**无竖切**） |
| F25 | seat_conflict + session_id；needs_human 只由 B 写 | CardSeatStolen、还原表 |
| F26 | resident_occupied / read_only / no_room | 还原表（**无竖切**） |
| F27/F37 | `firstRoundNotLocalError`，零出站 | CardNotLocal（hops=0） |
| F28 | round_failed | ResidentFailureOnCarrier（文案透出） |
| F29 | invalid → 502 | 还原表 |
| F30/F31 | `launchSessionResidentOn` pointed + `residentFailureIsMine` | FailureThenRetry / Unreachable / OldPeer（恰一条） |
| F32 | 远端分支先 `release()` | CardLaunch 回合中计数 1/1 |
| F33 | 骨架 `defer release()` + released 标志 | 全部用例 `assertZeroCounts` |
| F34 | `firstRoundTransferCtx` | TransferCtxDeadline |
| F35 | `extendFirstRoundWriteDeadline` | ReceiptOutlivesServerWriteTimeout（红未观测，见偏离 2） |
| F36 | 出站只调一次，无重试 | TransferWireShape（两次拉起恰两次转交） |

## 未验项

- 全量 `go test ./...`：按派发约束没有跑，留给 review / finish。
- PG：`LEDGER_TEST_PG_DSN` 未设置，PG 用例没有跑，未验（本卡没有改 ledger）。
- 真机 R1–R11（含 S3 CLI、长回合穿 Tailscale / relay、执行机重启清扫、混版本）：一律未验，由协调者执行。
- F35 用例的区分力（偏离 2）；F23 / F24 / F26 只有映射表单测，没有竖切。
- `IsLocalMachine` 的 hostname 分支没有被隔离靶子覆盖（Q5 甲），留给生产复核。

## 给协调者的真机提示

- 新端点（两台都必须是新构建）：`POST /api/cards/{id}/coordinator/first-round`、`POST /api/sessions/{id}/resident/first-round`。
  探活方式：不带 `X-Handoff-Forwarded` 头 POST，回 400 `{"kind":"invalid"}` 说明已是新版；回 404 说明还是旧版。
- 日志关键字均为 WARN 级，缺省级别可见：
  - 卡：`协调者首轮转交：受理机发出`、`协调者首轮转交完成：受理机收到回执`、`协调者首轮转交失败：受理机`、`协调者首轮转交：执行机收件`、
    `协调者首轮转交完成：执行机`、`协调者首轮转交失败：执行机`
  - 驻场：同形的 `驻场者首轮转交…`
  - 字段：`accepted_by` / `handled_by` / `carrier` / `card` 或 `session`，两端可按这些字段对账。
- 防环：执行机会打 `转交来的协调者首轮载体不在本机，防环拒绝且不再转交`（WARN），受理机入口 502，文案「转交到本机的协调者首轮，但载体 X 不在本机（设备表不一致？）」。
- R8 老版本：老执行机回 404，受理机报「<机器> 的 agentd 版本过旧，不支持首轮转交，请升级」。老受理机不会发起转交，仍是旧的 fail-closed 文案。
- R4：对端停机时入口文案为「协调者载体 t448-oc 在 t448-linux，转交到该机失败：…」，会话里指针行恰一条，由受理机写入。

## 协调者裁决与真机记录（2026-10-19/20，主会话）

### 交回物「偏离与自主决定」裁决

- 1、2（先红纪律偏离，已披露）：认账；F35 的牙交独立 review 变异复验。
- 3（不可达文案追加「若请求已送达…请刷新查看」）：保留——如实覆盖部分失败。真机 R4 发现 connection refused（必未送达）也带这句，记 Minor 落 roadmap，不阻塞。
- 4（请求类错不落指针行）、5（防环拒绝由执行机写指针行）、6（受理端失败入口措辞）、7（V3 透传代理构造）：授权内或合理，认账。

### 真机靶子

- PG 测试库 `handoff_b448`（linux-01，专属账号已登记 credentials.md「已分配」）；两台隔离 agentd 共库：`t448-linux` = 100.81.114.119:7794（HOME/datadir `/root/b448-iso`），`t448-mac` = 100.91.215.29:7795（HOME/datadir 在会话 scratchpad）；构建 `05aff9dc`（mac `9527c56a…`，linux `9ad77510…`）。未碰在役 7777 与生产账本。
- 载体 `t448-oc`（machine=t448-linux，opencode，`opencode/deepseek-v4.1-flash-free`），协调小队 `t448-coord`；演示项目 `t448demo`（linux 侧假 origin 仓），卡 T1–T9。

### R1–R10 读数

- R1：两端 `/api/machines` 互见、均 `05aff9dc3edd`；载体须先在载体机 `POST /api/squads/carriers/t448-oc/detect` 才 online（首次叫机器人 400「小队内没有已上线且有空的载体」，探测后正常）。
- R2 S1：mac `POST /api/cards/T1/coordinator/launch` 30s 返回 `{"woke":true,"session_id":"ses_eab555562ffe…"}`；T1 `driver_mode=push`；`seat_bearings` T1 carrier=t448-oc machine=t448-linux；mac「受理机发出/收到回执」与 linux「执行机收件/完成」四行按 card/accepted_by/handled_by 对上；续轮：会话 session:3（含 T1）里 `@T1` 人话 → linux「唤醒回合开始 card=T1 … 结束 session=ses_eab555562ffe…」13s，同一 CLI 会话；计数 `carrier/t448-oc`、`squad/t448-coord/t448-oc` 均 0。（卡房间自 B358 起只读归档，续轮须从会话房间 @ 卡。）
- R3 S2：mac `POST /api/sessions`（squad t448-coord，卡 T2）返回 resident `carrier_machine=t448-linux`；驻场行 push、push_session 已补写；首轮发言 #11 可见；人无 @ 发言 → linux「驻场者续轮完成」、名额归还。驻场者首轮里自行 `card coordinate T2`（另起一席，B444 设计内，非自坐）。
- R4 S2 失败：停 linux 实例后建会话 session:5 → `resident.error`「协调者载体 t448-oc 在 t448-linux，转交到该机失败：对端 agentd 够不着 … connection refused（若请求已送达…）」；会话里恰一条 `system:pointer`（#27）；会话与 T4 保留；恢复后 `session resident launch session:5` 成功，驻场者 @t448-linux。
- R5 S3：mac CLI `card coordinate T3` 88s woke；`session create --project t448demo --squad t448-coord` 成功 resident@t448-linux；`session resident launch session:5` 成功（见 R4）。
- R6 S6：成员名额改 1，两张新卡 T5/T6 并行叫机器人 → T6 200 woke，T5 409「协调者并发已满（小队 t448-coord）…」；结束计数 0/0。
- R7 执行机重启：session:6 首轮在途时停 linux 实例（重启脚本撞 15s 优雅退出的 datadir 锁未起，随后手起）→ mac 侧 `resident.error` …EOF；受理机按 Q3 重读驻场行仍在途 → 不写指针（正确）；linux 启动清扫「已请下被重启打断的在途行」→ 会话恰一条指针行 #37「首轮被 agentd 重启打断…重试：handoff session resident launch session:6 …」。
  - **发现（既有缺陷，非 B448 引入）**：被杀实例的两个 opencode 子进程成孤儿跑完，名额计数永久停在 2；启动对账「保留无本机 owner 的运行计数（跨机占用不得清零）」。本机拉起路径同样会漏（计数无 owner），spec S6 失败子句「执行机既有启动对账照常 → 不留永久占用」的假设不成立。测试库内手工清零后继续。待人裁决。
- R8a 老版本：mac 实例换在役 `f44646ec` 二进制（只拷贝、不动在役进程），载体临时改登记 t448-mac，linux 叫 T8 → 502「t448-mac 的 agentd 版本过旧，不支持首轮转交，请升级」；T8 无席位，计数 0。
- R8b 防环：linux 实例 name 临时改 t448-linux-x 重启（设备表并存 t448-linux / t448-linux-x 同地址），mac 叫 T8 → 502「转交到本机的协调者首轮，但载体 t448-oc 不在本机（设备表不一致？）（执行机 t448-linux）」；linux 只有「执行机收件 → 防环拒绝 → 执行机失败 kind=not_local」，无出站行。已改回原名。
- R9 PG 腿（DSN 改用专用测试库，见下）：`handoff_b448` 正被靶子使用且 PG 用例会建表，改在 `handoff_b409_test`（多个 PG 用例硬性要求此库名）上 DROP 重建后跑 `go test ./internal/ledger/`：
  - B444 PG：`TestB444ResidentPushMigrationPG` 首跑红（夹具直写 session:1 绕过会话号计数器，CreateSession 再分到 session:1 撞主键），修夹具（7fef97de）后与 `TestB444ResidentSeatExclusionPG`、`TestB444SessionHomeProjectMigrationPG` 全 PASS。
  - schema 版本闸：`TestPGOpenSkipsSchemaWhenVersionCurrent`、`TestPGOpenFreshSchemaWritesMarker`、`TestSchemaVersionPinsDDL` PASS。
  - 既有、与 B448 无关（本分支未改 `internal/ledger` 业务代码）：`TestB439DevicesPG` 恒红（attrs 字节比对，PG jsonb 规范化成 `{"k": "v"}`）；`TestPGSmokeEndToEnd` / `TestB435DriverModeMigrationPG` 顺序相关红（共享 public schema 里项目自动前缀 P 互相占用，单跑通过）。落 roadmap。
  - `go test ./internal/agentd/ -run PG` ok。
- R10 长回合：linux 侧 opencode shim 先睡 720s，mac 叫 T9：转交出站 12m13s 后 mac「受理机收到回执」、T9 push 落座、计数 0——**跨机一跳（Tailscale 直连）撑过 11 分钟成立**；但调用方 curl→mac 既有入口在同刻断连 `[000]`（入口 server 写超时 ~11 分钟，Q4 甲已明定不改、落 roadmap），首轮结果照落、人刷新可见。shim 已删。
- R11（生产复核）：待合入、推送、换版获批后执行。

## review 修复轮（I-2 / I-3 / M-1）

基线 HEAD `6a32d2e8`。本轮只加测试，不改生产代码。变异验牙都是临时用 `sed` 改生产文件，跑完立即还原；还原后
`git status` 只剩 `internal/agentd/b448_card_test.go`（改）和 `internal/agentd/b448_review_test.go`（新）。

### 偏离与自主决定

1. **测试命令没有执行 `unset CLAUDE_CODE_SESSION_ID`。** 沙箱拒绝 `env -u …` 和 `cd … && unset …` 这类复合命令，只能直接运行
   `go test`。整包仍全绿，已知两条环境敏感用例这次没有触发，如实记录。
2. **I-2 的夹具构造（自主决定，属测试设计）：** 小队改为 `[Y, X]`，两个成员都在 B、上限都是 1。X = `t448-oc`，HOME 为
   `/home/t448`，模型为 `prov/model-x`；Y = `t448-oc-y`，HOME 为 `/home/t448-y`，模型为 `prov/model-y`。A 准入前先占住 Y 的名额，
   让 A 的 `LaunchAdmit` 跳过 Y、选中 X 转交。在 B 前面挂透传代理，等 A 已归还名额、B 尚未准入时释放 Y，使 B 若用 `LaunchAdmit`
   就必然按声明顺序改选 Y。反例在同一时机把 X 置为离线（`ApplyDetect Reachable=false`）或占满。
3. **I-3 的验牙多做了一次变异：** 除了把收件端的映射改成 not_found，还单独变异了受理端还原表（`firstround.go:101` 的 case），
   证明 client 还原这条断言也有区分力。

### 新测试

- `TestB448FrozenCarrierNotReselectedInTwoMemberSquad`（`b448_review_test.go`）：在 I-2 正例构造下，A 入口返回 200，转交体
  `carrier=t448-oc`；B 的 Runner 恰好拉起一次，HOME 和模型都是 X 的；回合中 Y 的计数为 0/0；承载记为 X；两级计数归零。
- `TestB448FrozenCarrierUnavailableRejectsWithoutReselect`（`b448_review_test.go`，两个子例）：X 离线时 B 回
  admission_rejected，A 入口 400，文案含 `t448-oc`；X 满员时 B 回 no_slot，A 入口 409「协调者并发已满（小队 t448-coord）」。
  两个子例都零拉起、零席位，Y 的计数为 0。
- `TestB448FirstRoundWireValidation`（`b448_card_test.go`）追加两条断言：向 B 发 `POST /api/sessions/S99999/resident/first-round`，
  得 409 `kind=no_room`；A 经 `clientForTarget(t448-linux).MarkForwarded().ResidentFirstRound` 拿到错误后，经 `firstRoundFailure`
  还原，满足 `errors.Is(err, collab.ErrNoRoom)`。
- `TestB448ResidentReceiptOutlivesServerWriteTimeout`（`b448_review_test.go`）：B 的 WriteTimeout 为 200ms，回合耗时 600ms，
  `coordWakeTurnTimeout` 为 3s。建会话入口的 `resident` 成功，`carrier_machine=t448-linux`，计数归零。

### 验牙记录

- **I-2**：把 `scheddrain.go:448` 的 `s.scheduling.AdmitSeatCarrier(squad, plan.frozenCarrier)` 改为 `s.scheduling.LaunchAdmit(squad)`，
  执行 `go test ./internal/agentd/ -run 'B448FrozenCarrier' -count=1`，结果为红：
  ```
  --- FAIL: TestB448FrozenCarrierNotReselectedInTwoMemberSquad (0.03s)
      b448_review_test.go:121: 执行机应只用冻结载体 X（HOME /home/t448 / 模型 prov/model-x）拉起，不改选 Y: [{CLI:opencode HomeDir:/home/t448-y Model:prov/model-y Workdir:/repo/t448-linux Env:[]}]
      --- FAIL: TestB448FrozenCarrierUnavailableRejectsWithoutReselect/X_离线 (0.02s)
          b448_review_test.go:176: 冻结载体 X 不可用应 400 含 "t448-oc"（不改用 Y），得 200 {"woke":true,"session_id":"ses_t448-linux_1","rebuilt":false,"escalated":false}
      --- FAIL: TestB448FrozenCarrierUnavailableRejectsWithoutReselect/X_满员 (0.02s)
          b448_review_test.go:176: 冻结载体 X 不可用应 409 含 "协调者并发已满（小队 t448-coord）"（不改用 Y），得 200 {"woke":true,"session_id":"ses_t448-linux_1","rebuilt":false,"escalated":false}
  ```
  同一变异下，既有用例 `go test ./internal/agentd/ -run 'B448' -skip 'B448FrozenCarrier' -count=1` 全部为 `ok`，复现了 review 指出的
  「F15 无牙」。还原后重跑，结果为绿。
- **I-3（收件端）**：把 `firstround.go:253` 的 `proto.FirstRoundKindNoRoom` 改为 `proto.FirstRoundKindNotFound`，执行
  `-run 'B448FirstRoundWireValidation'`，结果为红：
  ```
  b448_card_test.go:410: 会话不存在应 409 no_room（F20 r1 不回 404），得 409 {"error":"会话 S99999: collab: 房间不存在","kind":"not_found"}
  ```
- **I-3（受理端还原）**：把 `firstround.go:101` 的 `case proto.FirstRoundKindNoRoom:` 改为 `case "mutated-no-room":`，结果为红：
  ```
  b448_card_test.go:419: 会话不存在经 client 还原应满足 errors.Is(collab.ErrNoRoom)，得 wire=首轮转交被拒（HTTP 409，no_room）：会话 S99999: collab: 房间不存在 restored=执行机 t448-linux 回 HTTP 409：会话 S99999: collab: 房间不存在
  ```
  两处都已还原，重跑为绿。
- **M-1**：把 `firstround.go:306` 的 `s.extendFirstRoundWriteDeadline(w, "session", id)` 删掉，执行
  `-run 'B448ResidentReceipt|B448ReceiptOutlives'`，结果为红（卡端点的同形用例不受影响）：
  ```
  b448_review_test.go:200: 驻场首轮长于执行机 WriteTimeout 时回执仍应送达，得 &{Identity: Carrier: CarrierMachine: Error:协调者载体 t448-oc 在 t448-linux，转交到该机失败：对端 agentd 够不着: POST /api/sessions/session:1/resident/first-round: Post "http://127.0.0.1:60876/api/sessions/session:1/resident/first-round": read tcp 127.0.0.1:60879->127.0.0.1:60876: read: connection reset by peer（若请求已送达，首轮可能仍在该机执行或已完成，请刷新查看）}
  ```
  还原后重跑，结果为绿。这一条也补上了原偏离 2「F35 红未观测」的驻场端点那一半。

### 收尾复跑（生产代码已还原）

- `go test ./internal/agentd/ -run 'B448|CoordLaunch|B444|B446' -count=1`：`ok`（4.4s）
- `go test ./internal/agentd/ -count=1`：`ok`（88.6s）
- `go build ./...`、`go vet ./...`：无输出
- `gofmt -l internal/agentd/b448_card_test.go internal/agentd/b448_review_test.go`：空

## 增补 r1 实现（S7）

- 执行者：implement 节点 subagent；基线 `9efc09c5`（分支 `cards/B448-charter-1`）。
- 输入：plan 末节「增补 r1」（Q-r1-1..5 全甲）、spec 末节「修订 r1」。
- 读码确认：plan「r1 核实读数」与 `9efc09c5` 一致——`sched_running` 写者只有 `casCount` / `stepRunning`；`StartAutomation` 在 `setupLedger` 内、早于 `RecoverOnStartup`；`ReconcileSchedRunning` 只读。

### r1 步 1–3：scheduling 持有分布与归还方法（T1–T8）

- 新测 `internal/scheduling/b448_holders_test.go`（经 ledger SQLite + `facadeRegistry` 真实门面）。
- 首红（编译红）：`go test ./internal/scheduling/ -run B448R1 -count=1` → `a.ReclaimLocalOccupancy undefined`。
- 空壳（`ReclaimLocalOccupancy` 返回空报告）后断言红，摘录：
  - `squad/sq/c1 body={Count:2 Holders:map[]}，want {Count:2 Holders:map[A:1 B:1]}`（T1）
  - `直派 body={Count:1 Holders:map[]}，want count=1 holders={A:1}`（T1 直派）
  - `旧 body 上新写 body={Count:4 Holders:map[]}，want count=4 holders={A:1}`（T2）
  - `新 body 应带 holders（否则金样本无意义）: {"count":2}`（T3）
  - `对账后 body={Count:3 Holders:map[A:2 B:1]} v=1，want count=1 holders={B:1} v=2`（T4）
  - T5/T7/F49/T8 两条同样断言红；T6（不写）在空壳下天然绿——它是守卫，不是新行为。
- 步 1 金样本（承重假设）判断：旧形 `struct{Count}` 解新 body 不报错、count 正确由 T3 断言；新解码器解 `{"count":3}` 由 T2 断言。实现后两者皆绿，不阻塞。
- 实现：`scheduling.go` 新增 `runningBody{Count, Holders omitempty}`、`decodeRunning`（错误带 key/version）、`stepped`、`reclaimed`、`holderName`、`readRunning`；`casCount` 改为吃本次读到的 body 并在同一次 Put 里 `count+1/holders[self]+1`；`stepRunning` 同步维护；`AdmitCarrier`/`acquire` 改传 body；`ReclaimLocalOccupancy(owned)` 新增。`registry_read.go` `RunningCounts` 改用 `decodeRunning`，仍只投影 count。
- 绿：`go test ./internal/scheduling/ -run B448R1 -count=1` → `ok`；整包 `go test ./internal/scheduling/ -count=1` → `ok  github.com/Xsxdot/handoff/internal/scheduling 0.957s`。
- `go build ./...`、`go vet ./...`：无输出（exit 0）；`gofmt -l internal/scheduling/` 空。
- 提交：`af77f6e0 feat(B448): sched_running 计数记持有机，新增本机遗留归还规则（r1 T1–T8）`。

### r1 步 4：orchestration owned + agentd 启动序（T3 观察面 / T5 / T9 / T10）

- 新测 `internal/agentd/b448_r1_reclaim_test.go`；`recover_occupancy_test.go` 五条用例改打新入口 `startAutomationForTest`（= `StartAutomation(ctx)`），断言语义不变，`RecoverOnStartup` 仍随后调用以保留原探活断言；malformed 用例改断言「StartAutomation 返回带 key 错误 + 日志带 key/version=2 + 无『自动化清队与事件唤醒循环启动』日志」。
- 首红（编译红）：`recover_occupancy_test.go:75:9: srv.StartAutomation(ctx) (no value) used as value`。
- 空壳（`StartAutomation` 只改签名返回 nil）后断言红：
  - `TestB448R1StartAutomationReclaimsBeforeFirstAdmission`：`b448_r1_reclaim_test.go:90: 等待条件超时`（T9：载体满员，首轮 ErrNoSlot 回填，Runner 无 Launch）
  - `TestB448R1ReclaimKeepsLiveLocalTasks`：`载体键对账后 {Count:4 Holders:map[B:1 DF-ZIDONGdeMac-mini.local:3]}，want {Count:3 ...:2}`（T5）
  - `TestB448R1RecoverOnStartupNoLongerReconciles`：`err=启动对账解码 carrier/bad: invalid character 'o' in literal null (expecting 'u') probes=0，want nil/1`
  - `TestB23310RecoverRejectsMalformedSchedRunning`：`malformed sched_running 应返回带 key 错误，实际 <nil>`（T10）
  - `TestB448R1SquadsRunningViewHidesHolders`（T3 观察面）在空壳下即绿：wire 只投影 count，是守卫。
- cmd 组装点守卫 `TestB448R1SetupLedgerAbortsWhenReconcileFails`（源码断言）：临时把 `cmd/agentd.go` 改回 `srv.StartAutomation(ctx)` 裸调用，`cd cmd; go test -run B448R1 -count=1 .` → `agentd_ledger_test.go:226: setupLedger 必须检查 StartAutomation 的返回错误`；还原后 `ok github.com/Xsxdot/handoff/cmd 0.575s`。
- 实现：
  - `orchestration/watchdog.go`：删 `schedRunningKind`/`schedRunningBody` 解码副本；新增窄接口 `OccupancyReclaimer`；`ReconcileSchedRunning(st, reclaimer, log)` 按 `OccupancyKeys(task.Squad, task.Carrier)` 计 owned（计数），交 `ReclaimLocalOccupancy`；有归还/跳过时完成日志用 Warn。
  - `agentd/scheduling_client.go`：接口加 `ReclaimLocalOccupancy`。
  - `agentd/scheddrain.go`：`StartAutomation(ctx) error`，`automationStartOnce` 内先同步对账，失败记 Error、不起循环、返回 error。
  - `agentd/server.go`：`RecoverOnStartup` 去掉对账（保留 autoLedger 缺失检查）。
  - `cmd/agentd.go`：`setupLedger` 检查 `StartAutomation` 错误，关账本库后返回错误（走 `abortBoot`）。
- 绿（本轮实跑）：
  - `go test ./internal/agentd/ -run B448R1 -count=1 -v`：4 条 PASS，`ok ... 0.595s`
  - `go test ./internal/agentd/ -run B2331 -count=1 -v`：B23310/B23311 五条迁移用例全 PASS
  - `go test ./internal/scheduling/ ./internal/orchestration/ ./internal/agentd/ -count=1`：
    `ok github.com/Xsxdot/handoff/internal/scheduling 0.836s` / `ok .../internal/orchestration 41.855s` / `ok .../internal/agentd 87.735s`
  - `cd cmd; go test -count=1 .`：首跑红 1 条 `TestSessionSendActorResolution`（`session_test.go:1298: ... --cli/--session 与当前环境会话不一致`）——即 memory 记载的环境敏感测试（读 `CLAUDE_CODE_SESSION_ID`），与本轮无关；`unset CLAUDE_CODE_SESSION_ID` 后复跑 `ok github.com/Xsxdot/handoff/cmd 77.269s`。
  - `go build ./...` exit 0；`go vet ./...` exit 0；改过的 11 个文件 `gofmt -l` 空。
- 提交：`e1c787e3 feat(B448): 启动对账前置到 StartAutomation 并归还本机遗留名额（r1 T3/T5/T9/T10）`。

### 交回物

#### 偏离与自主决定

1. **新增 cmd 源码守卫测试** `TestB448R1SetupLedgerAbortsWhenReconcileFails`（T1–T10 之外）：锁「`setupLedger` 不丢弃 `StartAutomation` 的错误」。理由：`StartAutomation` 改成返回 error 后，Go 允许裸调用丢弃返回值，没有其它测试能挡住。属「测试文件组织」，在授权内。
2. **五条迁移用例的改法**：对账入口换成 `StartAutomation`，并保留随后的 `RecoverOnStartup` 调用以维持原探活次数断言；malformed 用例原断言「对账失败后不探活」改为「对账失败后不起循环」（运行期阻断点已从 RecoverOnStartup 移到 StartAutomation，cmd 侧由第 1 条守卫）。断言没有放宽。属 r1.6「迁移方式」，在授权内。
3. **T8 落在调度域层**：「重试耗尽时启动不失败」用 `ReclaimLocalOccupancy` 返回 nil error 断言（ReconcileSchedRunning 只在 err 非 nil 时返回错误），没有另经 StartAutomation 打一遍。属测试组织，在授权内。
4. **日志级别**：单键归还 `scheduling.reclaim.reclaimed` 用 Warn；汇总行在有归还或跳过时用 Warn，否则 Info。理由：agentd 默认 warn 级别，Info 级别的归还会被吞掉（B390 R4 先例），R7′ 需要能在日志里看到。属「日志措辞」，在授权内。
5. **holders 为空时省略字段**（`omitempty`），无持有的 body 与旧格式 `{"count":N}` 逐字节相同。在授权内（F39）。
6. **F43 字面执行**：他人条目原值照抄，包括非正值；只把自名条目规范化（减到 0 即删键）。
7. **错误文案**：`decodeRunning` 的错误统一为 `运行计数 <key>（version=<v>）解码失败: …`。`RunningCounts` 原文案是 `运行计数 <key> 解码失败: …`，因此损坏行时 `/api/squads` 500 的文案多了 version。wire 结构不变，只是文案，判断不属于 r1.6「修改 /api/squads 的 wire」。
8. **新增依赖缺失错误分支**：`ReconcileSchedRunning` 在 store 或 reclaimer 为 nil 时返回 error。这是防御分支，生产组装下走不到。
9. **持有者名取不到时**（自名为空且 `os.Hostname()` 失败）：准入只动 count、不记持有；对账记 Warn 后跳过。是 Q-r1-3 甲的边界补全。
10. **依赖未装配时不对账**：`StartAutomation` 在依赖未装配（scheduling / keystone / autoLedger 任一为 nil）时，沿用今日行为，返回 nil，不对账也不起循环。提请协调者留意：若生产出现「scheduling 已装、keystone 未装」，`handleDispatch` 的准入会在没有对账的情况下发生。cmd 组装点里三者同批装配，今天不存在这种情形。本轮未改。

以上都没有触及 r1.6 的「须升级」项：扣减口径、count 语义与准入判据、orchestration/agentd 直写 sched_running、/api/squads wire、DDL、F1–F37、准入机 ≠ 归还机，均未改动。

#### 提交

- `af77f6e0` feat(B448): sched_running 计数记持有机，新增本机遗留归还规则（r1 T1–T8）
- `e1c787e3` feat(B448): 启动对账前置到 StartAutomation 并归还本机遗留名额（r1 T3/T5/T9/T10）
- （本台账节随第三个 docs 提交落盘）

#### T / F 落点

| 条目 | 测试 | 代码 |
|---|---|---|
| T1 / F43 / F44 | `scheduling/b448_holders_test.go` `TestB448R1HoldersTrackAdmitAndRelease`、`TestB448R1AdmitCarrierRecordsHolder` | `scheduling.go` `runningBody.stepped`、`casCount`、`stepRunning`、`acquire`、`AdmitCarrier` |
| T2 / F42 | `TestB448R1NewWriterOnLegacyBody` | `readRunning` / `decodeRunning` |
| T3 / F41 / Q-r1-5 | `TestB448R1LegacyReaderDecodesNewBody`（金样本 + RunningCounts）；`agentd/b448_r1_reclaim_test.go` `TestB448R1SquadsRunningViewHidesHolders` | `registry_read.go` `RunningCounts` |
| T4 / F46 / F47 | `TestB448R1ReclaimDeductsLocalOnly` | `runningBody.reclaimed`、`ReclaimLocalOccupancy` |
| T5 | `TestB448R1ReclaimKeepsOwned`（调度侧）；`TestB448R1ReclaimKeepsLiveLocalTasks`（agentd：pending + waiting_review 计入，completed 不计） | `orchestration/watchdog.go` `ReconcileSchedRunning` |
| T6 / F39 | `TestB448R1ReclaimNoopWithoutLocalHolder`；既有 B23310/B23311 五条 | `reclaimed` changed=false 不写 |
| T7 | `TestB448R1ReclaimCappedByCount` | `reclaimed` |
| T8 | `TestB448R1ReclaimRetriesOnConflict`、`TestB448R1ReclaimRetryExhaustedLogsAndContinues` | `ReclaimLocalOccupancy` CAS 重读重算 / 耗尽记 Error |
| T9 / F48 | `TestB448R1StartAutomationReclaimsBeforeFirstAdmission` | `scheddrain.go` `StartAutomation` |
| T10 / F49 | `TestB23310RecoverRejectsMalformedSchedRunning`（改打 StartAutomation）、`TestB448R1ReclaimRejectsMalformed`、cmd `TestB448R1SetupLedgerAbortsWhenReconcileFails` | `decodeRunning`、`StartAutomation`、`cmd/agentd.go` `setupLedger` |
| Q-r1-1 去对账 | `TestB448R1RecoverOnStartupNoLongerReconciles` | `server.go` `RecoverOnStartup` |
| F38 | T1–T4 断言的 body 形状 | `runningBody` |
| F40 | 准入判据未改（`acquire`/`AdmitCarrier` 只比 `.Count`）；既有并发准入测试全绿 | — |
| F45 | T9 / T5 用 hostname 回退；T1 用 SetSelfName | `holderName` |

本轮实跑结果见上方「步 1–3」「步 4」两节原文。

#### 未验

- **PG 账本路径**：未验，原因是没有 DSN。机内测试全用 SQLite ledger 门面；PG 上 body 的 JSON 往返与 CAS 由 R7′ 兜底。
- **变异两处**（删 owned 扣减 → T5；casCount 不保留他机 holders → T1）以及 T9 的「对账挪到首轮后」变异：留给 review，本节点未做。T9 的红只在「完全不对账」的空壳下观察到。
- **真机 R7′ / R7′b**：未做，由协调者执行。
- **全仓 `go test ./...`**：未跑，按三段律留给 review / finish。

#### 给协调者真机 R7′ 的提示

- **持有者名**：等于隔离实例 config 的 `name:`（即 `cfg.Name`，如 `t448-linux`），不是 hostname。在途读数应为 `{"count":1,"holders":{"t448-linux":1}}`，两把键（`squad/t448-coord/t448-oc`、`carrier/t448-oc`）同形。
- **对账后 body**：持有清空时 holders 字段被省略，行不删。`t448-oc` 两把键应为 `{"count":0}`，version 各 +1。他机行 `{"count":1,"holders":{"t448-mac":1}}` 与旧格式行 `{"count":1}` 的 body 和 version 都不变。
- **日志关键字**（默认 warn 级别下可见的几行）：
  - `scheduling.reclaim.reclaimed`（WARN），字段：`self=`、`key=`、`version=`、`deducted=`、`kept=`（写后 count）、`held_before=`、`owned=`；
  - `scheduling.reclaim.done`（有归还时 WARN），字段：`rows=`、`reclaimed_keys=`、`deducted=`、`skipped=`；
  - `启动调度占用对账完成`（有归还时 WARN），字段：`self=`、`task_count=`、`owned_keys=`、`record_count=`。
  - 失败时的关键字：`自动化循环未启动：启动调度占用对账失败`、`自动化编排启动失败：启动调度占用对账未通过`；写失败跳过时是 `scheduling.reclaim.error error_kind=retry_exhausted|write|reread`。
  - 无可扣的键只打 Info 级 `scheduling.reclaim.keep`，warn 级别下看不到，属正常。
- **对账时点**：在 `setupLedger` 内，早于 `RecoverOnStartup` 与 HTTP 就绪。B444 清扫照常在循环 goroutine 里、对账之后执行。
- **R7′b（混版本）**：旧二进制写过之后，该行 body 退回 `{"count":N}`，没有 holders。linux 重启后，这一行应走 `scheduling.reclaim.keep`、不被改写。

## 协调者：增补 r1 裁决与真机 R7′（2026-10-20）

- 交回物「偏离与自主决定」1–10 认账（均在 r1.6 授权内）；第 10 条（依赖未装齐时 StartAutomation 不对账也不起循环）今天组装下不可达，不修，记此。
- 构建 `d4684fc2`（mac `b6692af1…`，linux `2db08a81…`），两台隔离实例换版；在役 7777 未动。
- 对照行：测试库手工植入 `carrier/t448-foreign` `{"count":1,"holders":{"t448-other":1}}` 与旧格式 `carrier/t448-legacy` `{"count":2}`。（首次植入时持有名写成 t448-mac，mac 实例随后重启即按 S7 归还：`scheduling.reclaim.reclaimed self=t448-mac key=carrier/t448-foreign deducted=1`——行为正确，改用第三机名重做。）
- R7′：mac 建会话 session:7（T10），10s 时读在途 body：`carrier/t448-oc` 与 `squad/t448-coord/t448-oc` 均 `{"count": 1, "holders": {"t448-linux": 1}}`（v56）；`kill -9` linux 隔离实例 → mac 侧 `resident.error` …EOF；杀后、重启前计数仍 1（遗留）；重启后启动日志 `scheduling.reclaim.reclaimed self=t448-linux … deducted=1 kept=0 held_before=1 owned=0`（两键）、`scheduling.reclaim.done reclaimed_keys=2 deducted=2`，随后驻场者启动清扫请下 session:7 在途行；body 变 `{"count": 0}`（v57，holders 省略）；对照行 `t448-foreign` v3 与 `t448-legacy` v1 body/version 均不变。**S7 通过。**
- 操作失误披露：r7p.sh 用了 `pkill -9 -f "opencode run"`，匹配范围是 linux-01 全机而非只限隔离实例。核对在役 `/root/.handoff/agentd.log`：最后一行在 2026-10-19T22:57Z，03:00–03:02Z 无任何记录，判定当时无在役回合被波及。后续脚本不再用全机 pkill。
- R7′b（混版本旧二进制写回）未跑：机内 T6/F44 已覆盖旧写者丢 holders 后只少扣不多扣；按 plan 为可选。
- R7′ 补记（final review M-r1-3）：重启后 `session resident launch session:7` 成功（12:14:11→12:16:30，resident@t448-linux），回合后 `carrier/t448-oc`、`squad/t448-coord/t448-oc` 均 `{"count": 0}`（v63）；session:7 恰一条 `system:pointer`（被重启打断）。对照行不变。
- final review I-r1-1（F48 测试无牙）已写入卡验收栏，修复轮 2 进行中。roadmap 已销「名额无 owner」并补三条 r1 残余（永久下线、改名、孤儿子进程）。

## review 修复轮 2（I-r1-1 F48）

### 偏离与自主决定

- 无与 plan/spec 的偏离；只改测试（`internal/agentd/b448_r1_reclaim_test.go`），生产代码未动。
- 自行拍板：取方向一（断言首轮准入不回填），不取方向二（在途阻塞危害场景）。理由：错序时首轮在 `automationStartOnce.Do` 内同步跑完，no_slot 回填在 StartAutomation 返回前就已落账，断言确定、无需阻塞 Runner 或 sleep；方向二要让 Launch 阻塞，而⑤c 下首轮同步执行，会把 StartAutomation 本身挂住，变成超时而非断言红。属测试设计，在执行者授权内。

### 改动

- T9 `TestB448R1StartAutomationReclaimsBeforeFirstAdmission`：把编制域换成 `scheduling.New(r1LaunchQueuePutCounter{facadeAsRegistry})`。这个包装只数 `KindLaunchQueue` 的 Put，读写一律透传。入队后记基数，准入成功后断言 launch 队列再无写入，也就是没有 no_slot 回填。

### 验牙

- 施加⑤c（在 `automationStartOnce.Do` 里、对账之前插 `s.runAutomationPass(ctx)`）后跑 `go test ./internal/agentd/ -run TestB448R1StartAutomationReclaimsBeforeFirstAdmission -count=5`，结果 5/5 红：
  ```
  ERROR scheduling.launch_admit.error squad=coord carrier=coord-carrier member_policy=1 carrier_cap=1 error_kind=no_slot cause="scheduling: 小队或载体并发已满"
  --- FAIL: TestB448R1StartAutomationReclaimsBeforeFirstAdmission (0.03s)
      b448_r1_reclaim_test.go:141: 首轮准入应直接成功、请求不回填：launch 队列入队后又被写 1 次（对账晚于首轮准入，F48）
  ```
- 用 `git checkout -- internal/agentd/scheddrain.go` 还原后，同一命令 `-count=5` 为 `ok`。
- `go test ./internal/scheduling/ ./internal/agentd/ ./internal/orchestration/ -count=1`：三包均 ok（0.8s / 66.8s / 37.3s）。
- `go build ./...`、`go vet ./...` 通过；`gofmt -l` 对改过的文件输出为空。

## finish：合入、换版与 R11 生产复核（2026-10-20，用户批准「B448 合入 main 并推送，三台换版（旧二进制留备份），然后做 R11」）

- 合并：detached origin/main（f44646ec）上 `git merge --no-ff cards/B448-charter-1` → 417b68ba；合并树门禁：build/vet ok、gofmt 0、`go test ./...` 65 包 ok 0 FAIL、web tsc ok、vitest 145 文件 1951 例 pass。`git push origin HEAD:main` f44646ec..417b68ba（分支 cards/B448-charter-1 也推到了 origin，非必需）。
- 换版：linux-01 → macbook-pro → mac-mini 依次换 417b68ba（旧二进制留 `~/.local/bin/handoff.bak-pre-b448`；回滚 = mv 回去并重启服务）；三台 `/api/cards` 200、版本 417b68bac323。
- R11（mac-mini 在役受理）：现役协调小队 `leader` 成员已被改为 `cline`（linux-01，opencode，模型 `cline-pass/cline-pass/glm-5.3-flash`，home 空→/root），不是计划写的 mbp@macbook-pro。
  - 叫机器人（一次性卡 B449）：mac-mini「协调者首轮转交：受理机发出 handled_by=linux-01 carrier=cline」→ linux-01「执行机收件」→ opencode 2.7s exit 1 → 回执 `kind=round_failed` → CLI 502「执行机 linux-01 首轮失败：…exit status 1 stderr 尾部: 」；卡未落席位；计数 `carrier/cline`、`squad/leader/cline` 0。
  - 建会话（session:32）：同形转交 → 驻场者首轮失败 → `resident.error` 点名执行机 linux-01；会话里恰一条指针行。
  - 根因（手工复现于 linux-01）：`opencode run -m cline-pass/cline-pass/glm-5.3-flash` 返回 `403 ENTITLEMENT_ERROR: the user is not subscribed to required model plan`——cline 账号订阅问题，非 B448；且 opencode 把错误写在 stdout JSON，hostapi 只截 stderr，所以失败原因显示为空。
  - 结论：B448 跨机转交、回执映射、名额归还、指针行在生产成立；载体本身不可用，成功闭环（首轮在载体机跑完并落座）未在生产复核。B449 已废弃关闭、session:32 已归档。
- 生产 `squad/leader/cmd` 与 `carrier/cmd` 计数各为 1（载体 cmd 已不在登记表），是 r1 之前的旧格式无主遗留，按 S7 规则保留不动。
- R11 重跑（2026-10-20 12:46，用户重配小队后，「不要用 mbp」）：leader 仍为 `cline`@linux-01，模型改 `opencode-go/deepseek-v4.1-flash`。
  - 叫机器人（一次性卡 B450，mac-mini 受理）：21s 返回 `{"woke":true,"session_id":"ses_ea894d594ffe…"}`；卡 `driver_mode=push`；mac-mini「受理机发出 / 收到回执」handled_by=linux-01、linux-01「执行机收件 / 完成」对上。
  - 建会话（session:33，`session create --project handoff --squad leader`）：41s 返回 resident `cline@linux-01`；驻场者首轮发言 #28241、不改名；人发话 #28242 → 驻场者续轮回「收到。」#28244 并改名「新会话」→「B448 生产复核」（#28243 renamed）。
  - 计数 `carrier/cline`、`squad/leader/cline` 回合后均 0。**R11 成功闭环通过。** B450 已废弃关闭、session:33 已归档。
