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

