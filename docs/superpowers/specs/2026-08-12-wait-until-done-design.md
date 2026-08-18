# 静默等待依赖任务归档（B67）设计

## 1. 范围与动机

真实工作流里会并行开启两个会话：后一个会话的实现依赖前一个 handoff 任务完成并由审核者执行 `handoff done`。后一个会话现在没有一个准确的等待原语，只能：

- 挂普通 `handoff wait`，被 `question`、`permission_request`、每轮 `completed` 等与自己无关的事件反复唤醒；或
- 自己写 `show + sleep` 轮询，重复请求、延迟归档发现，并复制一套容易漂移的状态判断。

B67 增加一个纯等待门闩：

```bash
handoff wait <task> --until-done [--target devbox] [--timeout 3h]
```

它只回答一个问题：**这个依赖任务是否已经被审核者归档？**

成功时只输出 B68 的原始 `archived` 事件；等待期间不输出任何中间事件，也不触碰普通审核者使用的 cursor。依赖任务失败时立即结束本次门闩，不把未来可能发生的人工恢复静默算进同一次等待。

**不在范围内**：

- 不自动派发、执行或唤醒后续任务。调用方拿到 `archived` 后做什么，由调用方决定。
- 不替代审核用的 `handoff wait` / `wait --follow`，不回答工单，不做 `continue` / `done`。
- 不设计任意事件表达式（如 `--until question`、组合状态条件）。当前只有“等归档”一个明确需求。
- 不新增 agentd、store 或事件协议。B67 只消费已经存在的快照与事件流。

## 2. 开工前置条件与分支事实

B67 依赖两项已经分别完成的能力：

1. **W3a Task 8**：`internal/client.StreamEventsOnce`，一次连接、无 cursor、无重连的事件流原语。
2. **B68**：`EventTypeArchived`、`ArchivedPayload`、`Task.DoneNote`，以及 `done` 在关闭订阅前发布并落库 `archived`。

截至 2026-08-12，两项能力尚未位于同一开发线：

- W3a 已完成在 `handoff/web-console`；
- B68 与本 backlog 位于 `main`。

因此实现计划开工前必须先选定一个**同时包含两项能力**的基线。不得在缺少其中一项的分支上复制实现：缺 W3a 会重新造一套 WS 拨号与 cursor 隔离；缺 B68 则没有可靠的成功信号。

本 spec 只记录设计，不擅自合并两条开发线。

## 3. 已确认的产品契约

### 3.1 命令形态

采用现有 `wait` 的专用模式：

```bash
handoff wait <task> --until-done
```

没有新增 `await-done` 顶层命令，也不做 `--until <event>` 通用化：前者为一个条件扩张 CLI，后者会引出任意事件、状态与组合条件，均超出当前需求。

`--until-done`：

- 复用全局 `--target` / `--agentd` / `--config`；
- 支持 `--timeout`；
- 支持 `--notify`，但只在成功收到 `archived` 时通知；
- 与 `--follow` 互斥，参数校验阶段直接拒绝。

### 3.2 stdout、stderr 与退出码

| 结果 | stdout | stderr | 退出码 |
|---|---|---|---:|
| 收到或补查到 `archived` | 一行原始 `proto.Event` JSON | 默认无额外人读输出 | `0` |
| 依赖任务已/新进入 `failed` | 空 | 明确说明依赖任务失败 | `1` |
| 整体等待超过 `--timeout` | 空 | 等待超时说明 | `124` |
| 任务不存在、401、协议异常、已归档却缺事件 | 空 | 带处置方向的错误 | `1` |

成功输出示例：

```json
{"seq":42,"task_id":"…","type":"archived","payload":{"note":"W3a 已合并并验收"},"created_at":"…"}
```

必须输出**原始事件**，不能只打印 note，也不能合成一个假事件。`seq` 与 `created_at` 是事件身份的一部分，脚本可据此审计这次唤醒来自哪次真实归档。

### 3.3 `--timeout` 是总时限

本模式下 `--timeout` 表示从命令启动到归档成功的**整体最大等待时间**，不是 `wait --follow` 的“多久没收到任何帧”。

原因：B67 会刻意静默跳过中间事件；如果 question/progress 持续重置空闲计时，一个永远没有被审核者 `done` 的任务可以无限延长，失去门闩的兜底意义。调用方只关心“在约定时间内是否归档”，中间是否活跃不改变这个问题。

## 4. 架构边界

### 4.1 改动面

| 层 | 改动 |
|---|---|
| `internal/client` | 新增 `WaitArchived` 及结构化错误；只编排快照、无 cursor 流与重连 |
| `cmd/wait.go` | 新增 `--until-done` flag、冲突校验、总超时、退出码与单行 JSON 输出 |
| `cmd/*_test.go` / `internal/client/*_test.go` | 覆盖竞态、静默、cursor 隔离、失败与超时 |
| `README.md`、`skills/handoff/SKILL.md` | 记录用途、命令、退出码以及它不替代审核订阅 |

**不改** `internal/agentd`、`internal/store`、`internal/proto`。如果实现中发现必须改这些层，说明现有前置能力不完整，应停下来回到设计，而不是把协议扩张偷偷塞进 B67。

### 4.2 客户端接口

```go
// WaitArchived 静默等待任务归档，返回服务端真实 archived 事件。
//
// 它不读写审核者 cursor，不交付中间事件，也不改变远端任务状态。
func (c *Client) WaitArchived(ctx context.Context, taskID string) (*proto.Event, error)
```

结构化错误至少区分：

- `ErrDependencyFailed`：快照或事件表明任务为 `failed`；
- `ErrArchivedEventMissing`：权威状态已是 `completed`，但最近事件中没有 `archived`。

命令层据此映射人读文案与退出码；client 层不拼 CLI 文案。

## 5. 数据流与竞态闭合

### 5.1 单轮算法

每轮执行以下步骤：

1. `Attach(taskID)` 读取权威快照。
2. 检查任务状态与 `RecentEvents`：
   - 状态为 `failed`：返回 `ErrDependencyFailed`；
   - 最近事件含 `archived`：返回该事件；
   - 状态为 `completed` 却没有 `archived`：返回 `ErrArchivedEventMissing`；
   - 其余状态：取 `RecentEvents` 最后一条的 seq，空列表则取 `0`。
3. 调 W3a `StreamEventsOnce(ctx, taskID, fromSeq, onEvent)` 建立一次无 cursor 事件流。
4. 每收到一帧，先在内存推进 `fromSeq`，再分类：
   - `archived`：立即收手并返回该原始事件；
   - `failed`：立即收手并返回 `ErrDependencyFailed`；
   - 其他类型：返回 nil，继续读，不输出。

这里“不中间消费”的精确定义是：传输层会读取帧并推进**本进程内**水位，但不写 `~/.handoff/cursor-<task>`、不确认工单、不改变任务状态、不向调用方交付中间事件。agentd 没有消费确认或竞争消费者语义，因此这个旁观者不会抢走原审核者的事件。

### 5.2 启动时已经归档

不能假设命令总在 `done` 之前挂上。B68 把 `archived` 落库后才关闭 Hub，所以已归档任务的 `RecentEvents` 中应能找到真实事件；命令直接返回，不建立一条注定无新实时事件的连接。

不能仅凭 `Task.DoneNote` 合成事件：空 note 也是合法归档，且合成物没有真实 seq / created_at，会把兼容性问题伪装成成功。

### 5.3 快照与订阅之间恰好归档

窗口如下：

```text
Attach 读到 running ─────── done 落 archived ─────── StreamEventsOnce 建连
```

不会丢失：客户端用快照最新 seq 作为 `from_seq`；`/ws/events` 会重放 `seq > from_seq` 的落库事件，并且服务端已经具备“先订阅实时 Hub、再补历史、按 seq 归并去重”的契约。`archived` 要么在历史重放里，要么在实时流里。

### 5.4 断线与重连

`StreamEventsOnce` 故意不重连，`WaitArchived` 负责外层循环：

1. 临时断线后按现有 wait 的 `1s → 2s → … → 60s` 退避；稳定连接后的断线复位退避。
2. 每次重连前重新 `Attach`，先查权威状态与最近 `archived`，再从最新水位建流。
3. 401、403、任务不存在等永久错误立即返回，不盲重试。
4. 整个循环始终受调用方 ctx 控制；CLI 的总超时不会因重连或收到中间帧重置。

“每次重连前重取快照”不是多余请求：B68 的归档会关闭当时存在的订阅；断线期间归档后，重新订阅一个已完成任务不会再收到 Hub 的关闭动作，只能靠落库事件/快照识别终态。

## 6. 错误处理

### 6.1 依赖失败

`failed` 是**本次 B67 调用**的失败终点。无论从快照还是事件流观察到，都立即返回非零；不继续等待 `done`，因为 `done` 不能直接作用于 failed 状态。即使操作者日后通过其他流程恢复或重新派发，那也应由调用方显式启动一次新的门闩，不能把两段生命周期悄悄粘成一次成功等待。

错误必须包含 task ID；快照路径若能取得最近失败事件，可带其 payload 作为上下文，但不得把任意 executor 输出拼成 shell 指令或自动处置。

### 6.2 已完成但没有 `archived`

这通常意味着：

- 对端是 B68 之前的旧 agentd；或
- 数据/事件顺序契约已经损坏。

命令明确报错并提示升级/检查，不继续阻塞，也不伪造事件。任务已经 `completed`，再等不会凭空产生归档事件。

### 6.3 正常关闭但没见到 `archived`

正常 WS close 在普通 `FollowEvents` 中可代表任务归档，但 B67 的成功契约更强：必须拿到真实 `archived` JSON。因此遇到正常关闭而本轮没看到事件时，回到快照复核；快照仍找不到则走 `ErrArchivedEventMissing`，不能仅凭 close 成功退出。

### 6.4 输出失败

事件 JSON 序列化或 stdout 写入失败均返回 `1`。不能先退出 `0` 再让调用方拿到半行 JSON。

## 7. 日志与注释

本功能是“静默门闩”，可观测性不能靠默认级别刷屏破坏契约：

- 正常开始、跳过事件、临时断线与重连只打 `Debug`，包含 task、addr、from_seq、attempt；
- 依赖失败、永久连接错误、兼容性错误由最终 CLI 错误输出，包含 task、target/addr、最后水位和 cause；
- 成功路径以 stdout 的真实 `archived` 事件作为一等结果，同时可打 Debug 完成日志；
- 不为每个被跳过事件打 Info/Warn，避免长任务高频日志。

新增代码文件（若有）必须写中文职责/边界文件头；新增导出方法与错误类型必须有中文注释，说明参数、返回、cursor 隔离和竞态原因。复杂逻辑重点解释“为什么要先快照再订阅、为什么正常 close 不能直接算成功”，不重复代码表面动作。

## 8. 测试与验收

### 8.1 client 层

| 用例 | 必须证明 |
|---|---|
| 启动前已归档 | 从快照返回原始 `archived`，不挂住 |
| 快照与建连之间归档 | 依靠 `from_seq` 重放返回，不漏事件 |
| 中间事件静默 | question / permission_request / completed / progress 均不触发返回 |
| cursor 隔离 | 重定向 HOME，执行前后不存在或不改变 `cursor-<task>` |
| 等待中 failed | 立即返回 `ErrDependencyFailed` |
| 启动时 failed | 不建 WS，立即返回 `ErrDependencyFailed` |
| 临时断线后归档 | 退避重连，快照/重放最终返回真实事件 |
| 永久错误 | 401 / 不存在不重试 |
| completed 缺 archived | 返回 `ErrArchivedEventMissing`，不伪造、不死等 |
| ctx 取消 | 及时返回 `ctx.Err()`，无 goroutine 泄漏 |

### 8.2 cmd 层

| 用例 | 必须证明 |
|---|---|
| 成功 | stdout 严格一行 `archived` JSON，退出 `0` |
| 成功 + `--notify` | 只在成功时通知 |
| failed | stdout 空，退出 `1` |
| `--timeout` | 是总时限；即便持续收到中间帧仍在期限到达时退出 `124` |
| flag 冲突 | `--follow --until-done` 在发网络请求前拒绝 |
| JSON/写出错误 | 非零退出，不产生半条成功契约 |

### 8.3 竞态回归钉与变异验证

仅写一个“最终能收到 archived”的顺序测试不够，它可能恰好没有命中窗口。必须设置可控闸门，让测试严格发生：

```text
Attach 已返回活跃快照
  → 测试触发 done / 发布 archived
  → 允许 StreamEventsOnce 建连
```

然后断言仍从历史重放拿到该事件。

实现完成后做一次变异验证：临时把订阅起点错误地推进到 `archived.Seq`（或等价制造跳过该帧的错误），上述测试必须变红；还原后复绿。若变异后仍绿，说明回归钉没有真正覆盖 B67 最危险的竞态，不能验收。

### 8.4 真机验收

在隔离 agentd/DataDir 上至少跑三条：

1. **活任务门闩**：先挂 `--until-done`，制造 question / permission / completed，确认等待方 stdout 始终为空；审核者 `done --note` 后只出现一行 `archived`，note 原样可见。
2. **迟到门闩**：先 `done --note`，后启动 `--until-done`，应立即返回同一条落库事件。
3. **失败门闩**：任务 `stop`/自然失败后，等待方立即非零退出且 stdout 为空。

额外核对真实审核者 cursor 文件在三条验收前后字节不变。

## 9. 文档与兼容性

README 与 handoff skill 增加一小节，明确：

- `--until-done` 供“另一个会话等依赖归档”，不是审核主循环；
- 中间事件不会输出，原审核者仍须照常处理工单与 completed；
- `0 / 124 / 1` 的含义；
- 只有 `handoff done` 才算成功，executor 的 `completed` 只表示进入 `waiting_review`；
- 需要同时具备 W3a 无 cursor 流与 B68 archived 的新版 CLI/agentd。

旧 CLI 不认识 flag，会在参数阶段失败；旧 agentd 没有 `archived` 时，新 CLI 会在看到 completed 快照后明确报兼容性错误。两者都不能静默降级成“凭 completed 当 done”，否则会让后续任务在代码尚未审核、资源尚未归档时提前开工。
