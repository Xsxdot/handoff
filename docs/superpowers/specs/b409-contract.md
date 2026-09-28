# B409 S1 远端未挂账摘要 wire contract v1

> **状态：协调者冻结（2026-09-25）**。依据已批准的 [B409 r2](2026-09-24-ledger-read-performance.md) 与 S1 拆解；在 S1 API/Web 实现前冻结。生产者：`agentd /api/cards` 的未挂账摘要组装。消费者：Web `UnlinkedSummary`、卡片摘要行、Shell 角标与未挂账过滤器。基线源码：`75c518f235b1abdda0e9156de51fd1154e39f78d`。

## 冻结理由与边界

`/api/cards` 现在返回 `unlinked.count/tasks/unknown_targets`，Web 同时把 `tasks` 用于摘要展示、dock 角标和“仅看未挂账”过滤。r2 已批准：远端摘要不得拖慢本地账本；缺失目标不能当零；只有完整且 30 秒内的观测可作为当前数量/筛选事实；陈旧观测继续作为带时间的历史参考。新增状态字段横跨 Go JSON 生产者和 Web 消费者，是独立外部接缝，必须先冻结。

不改现有目标鉴权、调用池、超时、task 行字段和本地卡片响应。状态只描述远端摘要，不影响 `/api/cards` 的本地 cards 主数据。`30 秒`沿用已批准 spec 与现有刷新缓存窗口；它约束“当前值”的资格，不是隐藏历史观测的期限。

## Wire 形状

`unlinked` 保留三个既有键并新增 `status` 与 `observed_at`：

```json
{
  "status": "latest",
  "observed_at": "2026-09-25T03:00:00Z",
  "count": 1,
  "tasks": [
    {"target": "linux-01", "task_id": "task-123", "title": "实现查询", "state": "running"}
  ],
  "unknown_targets": []
}
```

- `status` 必须为 `latest`、`partial`、`stale`、`unavailable` 之一。Web 遇到缺失或未知状态按 `unavailable` 处理，fail closed：不显示为当前零、不计入当前角标、不启用未挂账筛选。
- `observed_at` 是 `tasks/count` 这份观测的 UTC RFC3339 时间；`unavailable` 时为 JSON `null`。其它状态必须有非空时间。
- `count` 继续为整数且等于 `tasks.length`。`tasks`、`unknown_targets` 继续输出数组，不因失败改成 `null`。既有 task 行继续使用 `target`、`task_id`、`title`、`state`。
- `latest` 表示当前配置的所有 target 查询成功，且 `observed_at` 年龄不超过 30 秒；`unknown_targets` 为空。此时 `count/tasks` 可作为当前数量、角标和过滤集合。
- `partial` 表示最近摘要不完整；`tasks/count` 仅表示可观测子集，`unknown_targets` 明确列出未获得结果的 target。它们只能作为带“部分”状态的观测，不得代表全量当前数量或驱动过滤。
- `stale` 表示最后一份可用完整或部分观测已超过 30 秒。保留原 `count/tasks/unknown_targets` 和 `observed_at`，仅作历史参考显示；没有固定隐藏时限。它不得驱动当前角标或过滤。
- `unavailable` 表示从未取得任何 target 观测；`count` 为 0、`tasks` 为空、`observed_at` 为 `null`。失败 target 可列在 `unknown_targets`，这与合法的最新空结果不同。
- 所有 target 查询成功且当前配置没有 target 时，结果是合法的 `latest` 空摘要，可驱动空集合筛选。
- 观察年龄 `<= 30s` 才可能是 `latest` 或 `partial`；`> 30s` 一律为 `stale`。此边界与 r2 的“最近一次刷新不超过 30 秒”一致。

状态金样：

| 场景 | status | observed_at | count/tasks | unknown_targets | 当前数量/过滤 |
|---|---|---|---|---|---|
| 全部成功且没有未挂账 task | `latest` | 非空 | `0` / `[]` | `[]` | 可用，确认为空 |
| 全部成功且有 task | `latest` | 非空 | 等于 task 数 / 全量数组 | `[]` | 可用 |
| 部分 target 失败 | `partial` | 非空 | 成功观测子集 | 失败 target | 不可用 |
| 观测超过 30 秒 | `stale` | 保留旧时间 | 保留旧观测 | 保留旧观测信息 | 不可用，仍可查看 |
| 从未取得结果 | `unavailable` | `null` | `0` / `[]` | 当前未知 target | 不可用，不冒充合法空 |

## 消费方断言与兼容

- `/api/cards` 的 cards 响应必须先成功返回本地账本事实；target 超时/失败只改变 `unlinked` 状态，不改变 cards 主结果的错误/延迟边界。
- Go handler 金样逐键断言以上四种状态，验证旧三键形状保留；缺少 target 结果不得合成为完整零。
- Web `UnlinkedSummary` 必须建模状态与可空 `observed_at`。`Shell` 只有 `latest` 能创建 `unlinkedTaskIds`；`partial/stale/unavailable` 一律传 `null`，使 `unlinkedOnly` 不过滤、dock 不把观测子集显示成当前总数。
- 卡片摘要行对 `latest` 显示当前数量；`partial` 显示部分观测和未知 target；`stale` 显示最后观测数量、时间与年龄；`unavailable` 显示尚无摘要/未知状态，不显示合法的 0。陈旧/部分历史观察可继续展开查看。
- 新字段为 additive；`count/tasks/unknown_targets` 的既有类型与语义保留。当前 Web 与 agentd 随 embedweb 同版本发布。新 Web 遇到旧 agentd（没有 `status`）时必须按 `unavailable` 退化；旧 Web 忽略新字段不会获得 r2 的安全筛选保证，因此部署验收必须使用同版 Web/agentd。

## 拍板记录

- **保留旧字段并增加状态/观测时间**：相比把失败压成 `count: 0`，可区分真实空、部分、陈旧与从未观测；相比直接删除陈旧值，能留住故障排查线索。多端消费者按状态门控，保证新增状态不会悄悄变成筛选事实。
- **30 秒后继续显示，不设置 5 分钟隐藏上限**：时间戳和 stale 状态已阻止历史值冒充当前值；隐藏会把“旧但有记录”与“从未有记录”重新混为一类，违背 r2 的可诊断要求。
- 三项均由已批准 r2 的外部行为推导，本契约没有另改用户结果；未命中需额外用户拍板的产品取舍。

## 源码锚点与实现交棒

- 生产：`internal/agentd/ledgerapi.go#Server.handleCardsList`、`internal/agentd/ledgerapi.go#Server.unlinkedSummary`。
- 消费：`web/src/api/ledger.ts#UnlinkedSummary`、`web/src/app/cards/CardsPage.tsx#UnlinkedRow`、`web/src/app/board/columns.ts#unlinkedOnly`。Shell 局部值 `unlinkedTaskIds` 在 `web/src/app/shell/Shell.tsx:191`；`codegraph sym unlinkedTaskIds` 未命中，已回落源码确认，记图覆盖债。
- 不改变 `codegraph/target.json` 的方向、组装点或预算；不新增 Go/TS 协议类型之外的运行时符号骨架，不需要 Ticket 0，不创建空视图 diff。
- 实现交棒：U2 改 handler、Web 类型与消费，并以此处 JSON 金样做双侧断言；S1 集成时以真实 agentd + 内置浏览器复核。此契约不证明任何行为已实现或验收。
