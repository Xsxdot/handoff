# B92 grok 续接回合事件被吞 —— 执行 ledger

- 分支: fix/b92-grok-continue-drops-events
- 起点 commit: 2080f5bb（docs(spec,plan): B92 修复——拆开「回合终结」与「执行终结」）
- 恢复现场以本文件 + git log 为准，不信记忆。

## 任务状态

| # | 内容 | 状态 | commit |
|---|------|------|--------|
| T1 | 拆 emitFailed → emitTurnFailed / emitFatal，改五个 call site | 完成（审查 APPROVE） | aade64ee |
| T2 | Send 的 evClosed 守卫 | 完成（审查 APPROVE） | 7451e07f |
| T3 | 回归 + 变异测试 + ledger 交接说明 | 待办 | - |
| FINAL | 整分支终审 | 待办 | - |

## 修复轮次

（每轮修复各追加一行）

## Minor 观察（终审统一 triage，不进修复回路）

- T1-复审 minor①：TestFatalFailureClosesEventChannel / TestTurnFailureThenFatalStillCloses 对「通道已关」用阻塞 `<-r.evCh` 断言，若 fatal 回归为不关通道会挂到超时才失败；可改 select+time.After fail-fast。
- T1-复审 minor②：emitFatal doc「后到者的 emit 被 evClosed 丢弃」表述略强，emit 与 closeEvents 间存在极窄竞态窗口（原 emitFailed 既有行为，非本次回归），closeEvents 幂等保证无双重终结。
- T2-复审 minor①：Send 守卫错误文案与「任务 %s 无运行态」分支措辞风格略异，但属 spec 给定文案，无影响。

## 变异测试记录

变异 1（还原 B92 bug：`emitTurnFailed` 函数体末尾加 `r.closeEvents()`）：
- 用例：`TestTurnFailureKeepsEventChannelOpen`（`go test -run 'TestTurnFailureKeepsEventChannelOpen' ./internal/executor/grok/`）
- 结果：**FAIL**
- 实际输出：
  ```
  2026/08/15 01:45:07 ERROR grok 回合失败 task=t1 reason="回合非正常收尾 stopReason=cancelled"
  --- FAIL: TestTurnFailureKeepsEventChannelOpen (0.00s)
      adapter_turnfail_internal_test.go:37: 回合失败不该关闭事件通道
  FAIL
  FAIL	github.com/Xsxdot/handoff/internal/executor/grok	0.590s
  ```
- 断言「回合失败不该关闭事件通道」如预期变红。改后 `git checkout internal/executor/grok/adapter.go` 还原。

变异 2（摘掉 Send 的 evClosed 守卫：注释掉 Send 内 `r.emitMu.Lock()` 到 `return fmt.Errorf(...)` 整段）：
- 用例：`TestSendRefusesOnClosedChannel`（`go test -run 'TestSendRefusesOnClosedChannel' ./internal/executor/grok/`）
- 结果：**FAIL（panic）**
- 实际输出（首部）：
  ```
  --- FAIL: TestSendRefusesOnClosedChannel (0.00s)
  panic: runtime error: invalid memory address or nil pointer dereference [recovered, repanicked]
  [signal SIGSEGV: segmentation violation code=0x2 addr=0x0 pc=0x101269c9c]

  goroutine 8 [running]:
  testing.tRunner.func1.2({0x101610e20, 0x1016eef60})
  	/usr/local/go/src/testing/testing.go:1974 +0x1a0
  testing.tRunner.func1()
  	/usr/local/go/src/testing/testing.go:1977 +0x318
  panic({0x101610e20?, 0x1016eef60?})
  	/usr/local/go/src/runtime/panic.go:860 +0x12c
  github.com/Xsxdot/handoff/internal/executor/grok.(*ACPClient).CallAsync(0x10161ca60?, {0x1012d7b1c?, 0x1012d449e?}, {0x10161ca60?, 0x719f2bb79560?})
  	/Users/sycm/.handoff/worktrees/f0102f2f/internal/executor/grok/acp.go:121 +0x2c
  ...
  FAIL	github.com/Xsxdot/handoff/internal/executor/grok	0.551s
  ```
- 守卫被摘后 `Send` 在已关闭通道上继续走 `CallAsync`，对 nil 连接指针触发 SIGSEGV。改后 `git checkout internal/executor/grok/adapter.go` 还原。

两条变异后 `go test -count=1 ./internal/executor/grok/` 全绿（`ok github.com/Xsxdot/handoff/internal/executor/grok 2.491s`），`git status` 工作区干净，无 adapter.go 变异残留。

## 真机复验交接说明（B92 spec §4 第 6 条）

1. **改了哪几个 call site**：按五处 call site 归属表——adapter.go 的 `finishTurn` 中 `res.Err` 分支（回合异常终止，含 ACP -32603）→ `emitTurnFailed`；`finishTurn` 中 `stopReason != end_turn` 分支 → `emitTurnFailed`；`onClosed` 权限应答通道中断 → `emitFatal`；`onClosed` ACP 连接断开 → `emitFatal`；resume.go 看门狗判 serve 死亡 → `emitFatal`。原 `emitFailed` 已删除。
2. **怎么用 grep 验证没有遗漏**：`grep -rn "emitFailed" internal/executor/grok/` 应零命中。注意 `internal/executor/codex/` 也有同名 `emitFailed`，那是另一个 adapter，不在本次范围（验证范围限定 grok 目录）。
3. **真机复验怎么构造 stopReason=cancelled 回合**（这步不由执行者做，由 mac-02 上人做）：先在 mac-02 `grok login` 恢复凭据（该机 `~/.grok/auth.json` 缺失，所有 grok 任务派不出去，是 B98，与本次无关）；然后派一个 grok 任务，协调者故意**拒绝一次权限请求**（RespondPermission reject），模型被拒后 grok 会以 `stopReason=cancelled` 收尾该回合 → `emitTurnFailed`（不关通道）；随后 `continue`，确认续接回合的事件能到达协调者、任务最终走到 waiting_review（不要再卡 running 等 2h 看门狗）。现成复验对象：标本任务 `398259b7` 修好后对其 `resume --force` 收口即可。
