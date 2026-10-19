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
