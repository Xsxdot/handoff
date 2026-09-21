# B390 implement 节点台账（唤醒消费轮静默停摆：T1 回路 / T2 重试可见 / T3 观察面）

> 分支 `cards/B390-charter-4`。台账随产出物同批提交；提交事实以命令原文记录。
> 记录原则：只写亲跑到结果的读数；未跑的写「未验证」与原因。

- 2026-09-21 开工：`git status` 干净；HEAD `9a10cb4c`（`Merge remote-tracking branch 'origin/cards/B389-charter-7' into cards/B390-charter-3`）。
- 硬性第一步（B389 合并）：`git fetch origin cards/B389-charter-7` → `6f20dfce`；`git merge --no-edit -X ours origin/cards/B389-charter-7` → `Already up to date.`（`6f20dfce` 已由 `9a10cb4c` 并入）。**但协调者要求的 `130a6634` 不在 `cards/B389-charter-7` 上**（`git ls-remote origin cards/B389-charter-7` = `6f20dfce`）；`130a6634` 在 `origin/cards/B389-charter-9`，`git merge-base --is-ancestor 130a6634 HEAD` → NOT ancestor。
- 按协调者意图（把 `130a6634` 的 `(card,seq)` 复合认领键并进来）：`git merge --no-edit 130a6634`（本地分支 `cards/B389-charter-9`）→ 无冲突（`git merge-tree --write-tree HEAD 130a6634` 无冲突标记），合并提交 `07f6252a`。合入内容：`internal/ledger/wakeclaim.go` 改 `(card,seq)` 复合键、`wakeclaim_migrate.go` 迁移、`wakeconsumer.go` `completeWakeBatch(card, seqs)` 三参、`wakeclaim_test.go`/`scheddrain_test.go` 同步。
  - 命令原文：`git merge --no-edit 130a6634` → 尾部 `Merge commit '130a6634' into cards/B390-charter-4`，`git log --oneline -1` = `07f6252a`。
- 基线编译：`go build ./...` → 退出 0（无输出）。
- 基线测试（合 130a6634 后，全包）：`go test ./internal/agentd/ -count=1` → `ok github.com/Xsxdot/handoff/internal/agentd 180.201s`。基线可编译可绿（协调者硬性要求达成）。

## T1 红色回路转正

- 计划原文 T1 的 `alwaysNoSlotScheduling` 覆写 `LaunchAdmit`，但合并 B389 后唤醒路径改走 `AdmitSeatCarrier`（`scheddrain.go:449`，`wakeCoordinatorRoundRaw` 本地分支），`LaunchAdmit` 只在 `launchCoordinatorRoundWithExpect`（HTTP 手动拉起 / launch_queue）用。**偏差**：测试桩改覆写 `AdmitSeatCarrier`，否则驱动不到唤醒准入。断言、入口缝（`runAutomationPass`）、三条判据不变。
- 新建 `internal/agentd/b390_wake_stall_test.go`（照 plan T1 代码块，仅把 `LaunchAdmit`→`AdmitSeatCarrier`；import 去掉 `mustScheduling` 需要的无变化）。
- 跑红（先红）：`go test ./internal/agentd/ -run TestB390WakeStallRedLoop -count=1 -v` → 原文：
  ```
      b390_wake_stall_test.go:75: RED(a) 准入持续失败时未按节拍重试：3 轮只尝试 1 次（want ≥ 3）
      b390_wake_stall_test.go:79: RED(b) 准入持续失败未落成 needs_human（账本无卡级可见事件）
  --- FAIL: TestB390WakeStallRedLoop (0.19s)
  ```
  与 plan 台账预期一致（RED(a)+(b)）。
- T1 提交：`git add internal/agentd/b390_wake_stall_test.go && git commit` → `d086ac10 test(B390): 唤醒停摆红色回路转正——runAutomationPass 三条断言（现状红）`。说明：因 T2 实现与 T1 测试在同一工作树内顺序完成，此提交只含测试文件（T1 台账记为「先红后绿」的完整读数见上）；T2 生产码随后单独提交。

## T2 准入持续失败：保留认领按节拍重试 + 恰一次落 needs_human（P2/P4）

- 协调者四条裁决：P2 重试用 B389 认领+终局前缀水位（准入失败不 `completeWakeBatch`、不标 seen，保留认领挡水位，下一轮自然重试），**不**新造内存重试表；P4 节拍沿用 `automationPollInterval=2s` 不退避，连续失败 3 次落一次 needs_human 且去重；P3 @前缀另立 B391，本卡不做。
- 因此**偏离 plan T2 的甲案**（plan 写的 `wakeRetryState`/`scheduleWakeRetry`/`retryPendingWakes` 内存重试表未落地，按裁决改为认领挡水位 + `automationStall` 计数表）。新增：`Server.automationStall map[string]int`、`admissionStalled(err)`、`recordAdmissionStall(card)`、`clearWakeStall(card)`、常量 `automationStallEscalateAfter=3`；`wakeconsumer.go` 失败分支按 `admissionStalled` 分流；`scheddrain.go` `runAutomationPass` 成功轮改 Warn 可见（R4）。
- 绿（T2 后）：`go test ./internal/agentd/ -run 'TestB390' -count=1 -v` →
  ```
  --- PASS: TestB390NonAdmissionErrorDoesNotStall (0.15s)
  --- PASS: TestB390WakeStallRedLoop (0.15s)
  ok  github.com/Xsxdot/handoff/internal/agentd 0.310s
  ```
- 反例（T2 plan 缺陷族表「准入满员与真实故障分流」）：`TestB390NonAdmissionErrorDoesNotStall` 用 `ErrNoHealthy` 桩，断言 3 轮只尝试 1 次、不落 needs_human。
- 触及包回归：`go test ./internal/agentd/ -run 'TestB390|TestB3583|TestAutomation|TestDrainQueues|TestCoord|TestB389' -count=1` → `ok ... 15.235s`。

### 变异自验（先确认编译过、命中唯一，再数失败）

- 变异①（分流失效）：`if admissionStalled(wakeErr) {` → `if true {`（文件内 count=1，唯一）。`go build ./internal/agentd/` 退出 0。`go test -run TestB390NonAdmissionErrorDoesNotStall` → FAIL：`非准入错误不得按节拍重试：3 轮尝试 3 次（want 1）` / `非准入错误不得落 needs_human`。**变异有牙**。已还原（`grep -c "if admissionStalled(wakeErr) {"` = 1）。
- 变异②（准入判据改错哨兵）：`errors.Is(err, scheduling.ErrNoSlot)` → `ErrNoHealthy`（count=1，唯一）。编译过。`go test -run TestB390` → 两条全红（回路线 `RED(a)+(b)` + 反例两条）。**变异有牙**。已还原（`ErrNoSlot` count=1）。
- 变异③（升级阈值守卫改早退）：`if attempts != automationStallEscalateAfter {` → `if attempts >= 1 {`（count=1，唯一）。该变异让守卫在第一次失败就 `return`，永远走不到 `MarkNeedsHuman`（即「永不落」）。编译过。`go test -run TestB390WakeStallRedLoop` → 红：`RED(b)` + `RETRY(d) 停摆等人应恰落一次，实得 0`。**变异有牙**。已还原。
- 变异④（去重失效-每次落）：同锚 → `if false {`。编译过。`go test -run TestB390WakeStallRedLoop` → 红：`RETRY(d) 停摆等人应恰落一次，实得 3`。**变异有牙**。已还原（`if attempts != automationStallEscalateAfter {` count=1）。

## T3 名额键残留观察面（CLI/HTTP，红→绿）

- 文件集：`internal/scheduling/registry_read.go`、`internal/agentd/scheduling_client.go`、`internal/proto/scheduling.go`、`internal/agentd/schedapi.go`、`cmd/squad.go`、`web/src/api/scheduling.ts` + 各自测试/fixture；`internal/proto/contract_fixture_test.go` 与 web 页测试的 mock 同步补 `running`。
- 契约触碰声明（plan T3 Interfaces 要求）：`SchedulingClient` 增 `RunningCounts()` 一行（只读观察面，schedapi 读面延伸；plan 要求「执行者动手前须把此接口增量记入本卡台账并请协调者确认」——本行即该记录，协调者四条裁决已授权本卡做 T3）。
- 先红：`go test ./internal/scheduling/ -run TestRunningCounts -count=1` → `undefined: svc.RunningCounts`（编译红，新建缝符号首红允许）；`go test ./internal/agentd/ -run TestSquadsGetReportsRunning -count=1` → `resp.Running undefined`；`go test ./cmd/ -run TestSquadListRendersTableAndJSON` → 表格缺内容 "运行位"。
- 绿：三包各自绿 + `go test ./internal/proto/ -run TestContractFixtures` 绿（fixture 用 `-update` 显式刷新后复跑绿）。
- TS：`cd web && npm run typecheck` 绿（需先补 `web/node_modules`——本工作树缺依赖，从既有工作树 `ce48ca83` 复制同 lock 的 `node_modules`；`web/node_modules` 已被 gitignore）；`npx vitest run src/api/contract.test.ts src/app/settings/SchedulingPage.test.tsx src/app/settings/SettingsPage.test.tsx src/app/flows/FlowsPage.test.tsx` → `Test Files 4 passed (4) / Tests 78 passed (78)`。
- 触及包全量：`go test ./internal/agentd/ ./internal/scheduling/ ./internal/proto/ ./cmd/ -count=1` → 四个包全 `ok`（agentd 175.205s / scheduling 7.016s / proto 0.014s / cmd 71.191s）。

### T3 变异自验

- 变异①（count 投影）：`out[rec.ID] = body.Count` → `body.Count + 1`（count=1，唯一）。编译过。`go test ./internal/scheduling/ -run TestRunningCounts` → 红 `运行计数不符: map[carrier/cmd:2 squad/pro/cmd:3]`。已还原。
- 变异②（HTTP 投影）：`Count: counts[key]}` → `Count: counts[key] + 100}`（count=1，唯一）。编译过。`go test ./internal/agentd/ -run TestSquadsGetReportsRunning` → 红 `运行位投影不符: [{Key:carrier/cmd Count:101} ...]`。已还原。
- 变异③（CLI 表格段）：删掉 `fmt.Fprintf(w, "运行位...", r.Key, r.Count)` 行（count=1，唯一）。编译过。`go test ./cmd/ -run TestSquadListRendersTableAndJSON` → 红 `表格缺内容 "运行位"`。已还原（`grep -c 运行位` = 2：注释+渲染）。

## 收尾自审

- 错误分支带上下文日志：T2 的 `recordAdmissionStall`（Warn + attempts，落等人失败再 Error）、非准入错误 Error 带 cause；T3 `RunningCounts` 解码失败带 id、`handleSquadsGet` 读计数失败带 cause。成功路径出口日志：`runAutomationPass` 有事发生打 Warn；`handleSquadsGet` Info 带 running_keys；CLI `squad.list succeeded` 带 running_count。
- 新文件头注释：`b390_wake_stall_test.go`、`b390_running_counts_test.go` 均有职责+边界；导出函数 `RunningCounts` 有参数/返回/边界注释。
- 服务端 `automationStall` 字段、`admissionStalled`/`recordAdmissionStall`/`clearWakeStall` 均有「为什么」注释。
- 与 plan Interfaces 签名：T3 完全一致；T2 按协调者 P2 裁决偏离 plan 甲案（内存重试表未落地，改认领挡水位），已在台账显式声明；T1 桩方法由 `LaunchAdmit` 改 `AdmitSeatCarrier`（B389 后路径），已声明。
- 未做：P3 `@` 前缀（另立 B391）；R3 TTL/自愈（回 spec）；真机清单（归协调者）。

## 提交事实（命令原文与提交时读数）

```
$ git add internal/agentd/b390_wake_stall_test.go && git commit -m "test(B390): ..."
[cards/B390-charter-4 d086ac10] test(B390): 唤醒停摆红色回路转正——runAutomationPass 三条断言（现状红）
 1 file changed, 145 insertions(+)

$ git add internal/agentd/wakeconsumer.go internal/agentd/scheddrain.go internal/agentd/server.go docs/superpowers/ledgers/2026-09-21-b390-implement-ledger.md && git commit -m "fix(B390): ..."
[cards/B390-charter-4 ab7d8df2] fix(B390): 准入持续失败保留认领按节拍重试并恰一次落 needs_human，消费轮不再静默
 4 files changed, 129 insertions(+), 6 deletions(-)

$ git add <T3 17 文件> && git commit -m "feat(B390): ..."
[cards/B390-charter-4 9d15d2e5] feat(B390): 名额键残留观察面——GET /api/squads 与 squad list 暴露运行计数
 17 files changed, 235 insertions(+), 17 deletions(-)
```

- 收尾全仓检查：`go build ./...` 退出 0；`go vet ./internal/agentd/ ./internal/scheduling/ ./internal/proto/ ./cmd/` 退出 0；`web && npm run typecheck`（`tsc -b`）退出 0。
- 硬性第一步合并提交 `07f6252a`（`git merge --no-edit 130a6634`）。
- 工作树在 amend 前应干净（除本台账的提交事实段本身，随 amend 收进同批提交）。
- 注：本段在提交后追加，随后 amend 一次收进 `9d15d2e5` 的同批提交；amend 会换 hash——这是 git 的事实，收口判据是工作树干净。

## 图覆盖债（codegraph 亲跑读数）

- `codegraph --repo . sym runAutomationPass`（EXIT 0）命中 `n_agentd_Server_runAutomationPass`，domain=`d_gateway`，container=`k_agentd_Server`，file=`internal/agentd/scheddrain.go:83`——**但图中 signature 是修改前版本**（`if _, _, err := s.consumeAutomationEventsOnce...`），说明图是改动前 baseline，不反映本卡新增。
- `codegraph --repo . sym consumeAutomationEventsOnce` 命中 `Server.consumeAutomationEventsOnce`。
- `codegraph --repo . sym admissionStalled` → `Error: 符号 "admissionStalled" 不在图中`（近似候选空）；`sym RunningCounts` 同样未命中（无输出）。原因：两者是**本卡新增**符号，图未重建（baseline 视图不含新符号）。
- 债务登记：`admissionStalled` / `recordAdmissionStall` / `clearWakeStall` / `RunningCounts` 未在图中；本节点以 grep 复核唯一命中（变异脚本的 `grep -c` =1 即证据），图重建后应补。未对新增符号跑 `flow`（二进制支持但 baseline 不含新点，跑了也无意义）；旧符号靠读源码复核。




