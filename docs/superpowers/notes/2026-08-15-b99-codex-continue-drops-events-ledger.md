# B99 codex 续接回合事件被吞（B92 同型缺陷）执行 ledger

**分支**：`fix/b99-codex-continue-drops-events`
**基线**：`14a1c923 docs(plan): B99 codex 同型缺陷——把 B92 的修复搬到 codex`
**日期**：2026-08-15

## 纪律摘要

- 只改 `internal/executor/codex/`，不碰 `grok/`、`internal/agentd/`。
- 每 task 完成后独立审查 subagent 双裁决（spec 符合性 + 代码质量），全过才 commit。
- 日志一律 `a.log`，禁止打印 token 值。
- 恢复现场以本 ledger + git log 为准。

## Task 记录

### Task 1: 拆分 emitTurnFailed/emitFatal 并改六个 call site

**状态**：完成，审查 PASS（0 轮修复）
**commit**：`1e0bafc2`（fix(codex): 回合失败不再关闭事件通道，拆出 emitTurnFailed / emitFatal（B99））
**改动文件**：adapter.go、resume.go、adapter_turnfail_internal_test.go（新建）
**审查结论**：六处归属全对、三处形态差异全遵守、emitFailed 代码符号零残留（仅测试 why 注释含旧名，属需求原样提供）、4 新用例 + 既有用例全 PASS、grok/agentd 零改动。minor：测试文件无包级 doc 注释（风格项，需求未要求）。

### Task 2: Send 的 evClosed 守卫

**状态**：完成，审查 PASS（0 轮修复）
**commit**：`e1dab7b4`（fix(codex): Send 拒绝在已关闭的事件通道上开新回合（B99））
**改动文件**：adapter.go（Send 内 +15 行守卫）、adapter_turnfail_internal_test.go（+TestSendRefusesOnClosedChannel）
**审查结论**：守卫位置正确（Send 内 lookup 后 log 前）、startTurn 未动、错误包 ErrTaskNotRunning、新用例红→绿（先 panic 后过）、既有用例全 PASS、grok/agentd 零改动。minor：测试注释末句疑似截断（纯文字）。

### Task 3: 回归、变异测试、claudecode 同型排查

**状态**：完成，审查 PASS
**commit**：`70e12e90`（chore: B99 回归、变异测试与 claudecode 同型排查记录）
**回归结果**：`go build ./...` && `go vet ./...` && `go test -count=1 ./...` → 全部包 PASS，0 FAIL（29 个包全绿，含 codex / claudecode / grok / opencode 各 executor）
**变异 1**（在 `emitTurnFailed` 函数体末尾加回一行 `r.closeEvents()`）→ **TestTurnFailureKeepsEventChannelOpen FAIL**，adapter_turnfail_internal_test.go:28 `回合失败不该关闭事件通道`（关闭权一旦回归到 turn 失败侧，红变立刻被捕获，随后 `git checkout` 还原）
**变异 2**（整段注释掉 `Send` 内 `r.emitMu.Lock() / closed := r.evClosed / r.emitMu.Unlock()` 与 `if closed { return ... }` 守卫）→ **TestSendRefusesOnClosedChannel panic**（SIGSEGV nil pointer dereference）：守卫摘掉后 `Send` 直通 `startTurn`，`r.cli` 为 nil → `Client.CallAsync` appserver.go:123 崩溃（stack：adapter_turnfail_internal_test.go:83 → Send adapter.go:362 → startTurn adapter.go:287 → appserver.go:123）；随后 `git checkout` 还原
**变异后还原确认**：`git checkout internal/executor/codex/adapter.go` 后 `go test -count=1 ./internal/executor/codex/` → 全绿（含 4 个 B99 用例），工作区无变异残留
**claudecode 排查**：
- 问题 1（有没有「回合失败关事件通道」路径）：**没有**。evCh 的关闭唯一发生在 `streamLoop` 的 defer（adapter.go:443-444 `r.closeOnce.Do(r.closeEvents)`；closeEvents 本体 :846-854 `close(r.evCh)`）。回合级失败走 `mapResult`（adapter.go:617-626，subtype!=success）只 `a.emit` result{OK:false}，**不关通道**、streamLoop 继续跑——正是 B92 修复后的语义。其余 `close(` 调用点均非事件通道：:149 close(r.ready)（就绪信号）、:289 close(ch)（Events() 对非运行任务返回已关通道的契约）、:386 close(r.stopCh)（Stop）。执行级终结（流损坏 :482-487、流中断 :489-493、进程退出 mapExit→runCancel :695-714、看门狗判死 :430-432、Stop :384-388）才经 streamLoop 退出→defer 关通道，且关通道前已先投出失败 result
- 问题 2（Send 会不会在同一个 runstate 上复用已关闭通道）：**不会**。Send（adapter.go:300-325）先 lookup（:301-304，运行态已 drop 则 ErrTaskNotRunning）、再查 stopCh（:305-309）、再查 proc（:318-320）。通道关闭后 `streamLoop` 的 defer 要么 drop 运行态（:448，非 Stop 终结）→ lookup 命中 nil；要么仅当 stopCh 已关才保留运行态（:445-447）→ Send 的 stopCh 检查拦截。唯一残余是 defer 内 closeEvents（:444）到 drop（:448）之间的一小段竞态窗口，但该窗口内进程已死/被杀（mapExit :709-713、看门狗判死、流断），WriteInput 会 O_NONBLOCK 打开失败→ErrTaskNotRunning；且失败 result 已在关通道前投出，manager 不丢终局。claudecode 的 emit 另有 `<-r.stopCh` 让路分支（:840-841）兜底
- 结论：**不同型**。claudecode 的回合失败（mapResult）从不关事件通道；通道关闭只随执行级终结经 `streamLoop` defer 发生，并与 drop/stopCh 成对出现，Send 结构上够不着 B92 的「关通道后同 runstate 开新回合静默吞事件」形态。无需改动：codex Task 2 的显式 `evClosed` 守卫是纵深防御，claudecode 靠「关通道即失活运行态」的结构性不变量达成同等效果（仅存上述微小竞态窗口，不构成 B92 症状，记入已知偏离备查）

## 已知偏离

- **claudecode 微小竞态窗口**：`streamLoop` defer 内 `closeEvents`（adapter.go:444）到 `drop`（:448）之间，`Send` 可能 lookup 到一条已关通道的 runstate。不构成 B92 症状（窗口内进程已死、失败 result 已先投出），且 claudecode 未在本次 B99 范围内改动，仅备查。
