# B394 契约增量：needs_cleared 移出唤醒映射（回声自激收口）

**上游状态：已批准**（源：B394 spec §2 + 用户 2026-09-22 建卡决定）
**级别：L2 单子系统**（`internal/agentd` 唤醒判据面；不动 keystone 与路由）
**本增量触碰的冻结物**：`b358-contract.md` 规则 31（扩清单）、`b353-contract.md` 条目 40（取代）。

## 1. 判据收口

1. `automationWakeEvent` 把 `EvNeedsCleared` **移出唤醒映射**（返回 `yes=false`）；`needs_cleared` 不再是唤醒源。
   理由：清标是 `needs_human` 的状态翻转（注意力平面），不是可动作事实；唤醒它使协调者自己的
   账务动作把自己叫醒（B382 真机 17726 needs_cleared → 新一轮唤醒 → 17727 keystone 重打 needs_human）。
2. **保留**两条展示通路：`card wait` 对 `needs_cleared` 仍编码 stdout（b353 条目 15）；
   会话列表 `needsHumanByCard` 仍按 `needs_cleared` 翻灭标签。以这两条既有测试仍绿为准。
3. 不并入 keystone 的失败重打（17717/17727 是唤醒失败产物，另一归因）。
4. `EvDecisionOpened`/`EvDecisionAnswered` 的唤醒行为**本卡不动**（其合法唤醒面=真人开裁决；
   自回声需来源区分，另立卡/另审）。

## 2. 原子冻结条目（每条独立 pass/fail）

1. `automationWakeEvent(EvNeedsCleared)` 返回 `yes=false`；消费轮 `processed` 不因 needs_cleared 增加。
2. `needs_cleared` 事件仍被消费轮标 seen、游标照推（不堵流）。
3. `card wait` 对 `needs_cleared` 仍编码 stdout 行（b353 条目 15 不回归）。
4. 会话列表 `needsHumanByCard` 清白标后 `NeedsHuman` 翻 false（b358.2 测试不回归）。
5. 正常唤醒源不回归：`task_mirrored`（策略真）、真人寻址 `room_message`、`decision_opened`/`decision_answered` 仍能唤醒。
6. 变异复验：把 `EvNeedsCleared` 放回唤醒映射，`TestB394ClearNeedsDoesNotWake` 重新变红。
