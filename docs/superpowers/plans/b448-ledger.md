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
