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

（Task 3 填入：两条变异各自的实际输出）
