# `handoff done` 携带完成说明，并让归档在事件流上有声（B68）设计

## 1. 范围与动机

今天 `handoff done` 归档一个任务时，做两件事：改状态、回收资源。它**不记录任何关于「这次做完了什么」的信息**，也**不在事件流上留下任何痕迹**。

两个后果：

1. **审阅历史无从下手**。三天后回头看一个归档任务，只有一个 `completed` 状态位和一堆过程事件，没有一句「这次干成了什么、审核者为什么放行」。
2. **归档在事件流上完全无声**。`Manager.Done` 只做状态迁移、不追加事件（[manager.go:1017](../../../internal/agentd/manager.go) 的注释自陈这点），`wait --follow` 之所以能收场，靠的是 `hub.CloseTask` 把订阅关掉——是从「连接被关了」间接推断出的结束，而不是收到了一条「归档了」。

第二条是 B67（等另一个任务归档后再开工）的**前置阻塞**：B67 要等的信号在事件流里根本不存在。

本 spec 一次解决两条，因为它们是同一个动作的两面：给归档一个载体（说明）和一个信号（事件），载体就装在信号里。

**不在范围内**：

- `handoff stop` 加 `--reason`。stop 落 `failed` 本来就是有声的终态事件，下游能感知；给中止动作记录原因是独立需求，不塞进本 spec。
- B67 本身（静默等待命令）。本 spec 只负责把它要等的信号造出来。
- 让 executor 自动产出完成总结。评估过：executor 是被审查方，自述容易退化成「我已完成任务」的空话，且要动四家 adapter 的回合协议，成本高一档、收益不确定。归档是审核者的动作，说明由审核者写。

## 2. 事实基础：读码复核结论

实现前必须以之为准的事实，全部已复核。

### 2.1 `POST /api/tasks/{id}/done` 目前不读 body

[server.go:775](../../../internal/agentd/server.go) 的 `handleDone` 只取路径参数，响应固定 `{"ok":true}`。带说明必须动契约。

### 2.2 `CloseTask` 紧跟 `transit`，中间没有任何发布点

`Manager.Done`（[manager.go:994](../../../internal/agentd/manager.go)）的现有顺序是：

```
GetTask → 校验 waiting_review → transit(completed)
  → clearApproverState → hub.CloseTask → adapterFor/stopExecutor → worktree 清理
```

新事件必须插在 `transit` 之后、`CloseTask` 之前。这是本改动**唯一会静默失效**的地方，见 §4.2。

### 2.3 tasks 表的增量列迁移是一张 map 加一行

[store.go:144](../../../internal/store/store.go) 用 `map[col]type` 逐列 `ALTER TABLE ... ADD COLUMN`，容忍 `duplicate column` 报错以保证幂等。加一列是一行的事，不需要新的迁移机制。

### 2.4 `handoff show` 直接序列化 `AttachInfo`

[show.go](../../../cmd/show.go) 把 `client.Attach` 的返回整个 JSON 输出，其中含完整 `proto.Task`。因此在 `Task` 上加字段**自动**出现在 `show` 的输出里，不需要改 show。

### 2.5 「响应体缺字段按保守值处理」在本仓库已有先例

`stop` 的 `worktree_removed` 就是这个模式，[client.go:524](../../../internal/client/client.go) 的注释写明「响应体缺字段（旧版 agentd）按 false 处理」。本 spec 的 `note_saved` 与之同构，不新造模式。

## 3. 设计

### 3.1 改动面

| 层 | 改动 |
|---|---|
| `internal/proto` | 新增 `EventTypeArchived EventType = "archived"`；`Task` 新增 `DoneNote string \`json:"done_note"\``；新增 `ArchivedPayload{Note string \`json:"note"\`}` |
| `internal/store` | 迁移 map 加 `"done_note": "TEXT NOT NULL DEFAULT ''"`；新增 `SetTaskDoneNote(taskID, note) error` |
| `internal/agentd/manager.go` | `Done` 签名加 `note string`；插入落列与发事件两步 |
| `internal/agentd/server.go` | `handleDone` 读可选 body `{"note":"..."}`；响应加 `note_saved` |
| `internal/client` | `Done(ctx, taskID, note) (noteSaved bool, err error)` |
| `cmd/done.go` | 新增 `--note` flag |

`ArchivedPayload` 放 `internal/proto` 而不是 `internal/agentd`：CLI 侧（B67，以及任何解析事件流的脚本）要读 `note`，放在 agentd 包里会逼两边各写一份结构体，形态一漂就是解析不出来。这与 `progressPayload` 只在 agentd 内部使用的情况不同。

### 3.2 `Manager.Done` 的新顺序

```
GetTask → 校验 waiting_review
  → SetTaskDoneNote(note)                    ①  新增
  → transit(completed)
  → AppendEvent(archived) + hub.Publish      ②  新增 ★
  → clearApproverState                           （原位不动）
  → hub.CloseTask                                （原位不动）★
  → adapterFor / stopExecutor → worktree 清理    （原位不动）
```

**既有语句一句都不移动**，只在两处插入新步骤。`clearApproverState` 夹在 `Publish` 与 `CloseTask` 之间不影响 §4.2 的约束——它只清内存 map，与订阅无关。

① **先落列再迁移状态**：列写失败时任务还没进终态，可以整体失败返回、让审核者重试；反过来先迁移就会留下「已归档但说明丢了」且不可重试的状态（`done` 对 `completed` 任务会 409）。

② 见 §4.2 的顺序约束。

### 3.3 契约

**CLI**

```bash
handoff done <task> [--note "一句话说明这次做完了什么"]
```

- **stdout 保持 `{"ok":true}` 不变**。`note_saved=false` 的警告、缺说明的提醒一律走 stderr，沿用 [wait.go:118](../../../cmd/wait.go) 确立的「stdout 是给脚本的契约、人读的信息走 stderr」惯例。改 stdout 会打断现有解析方。
- 省略 `--note` 时：正常归档，stderr 打一行 `本次归档未留说明（下次可加 --note "..."）`。这是本设计里唯一的「轻推」——不必填是为了不破坏现有所有 `handoff done <id>` 调用（handoff skill 文档、e2e-checklist、旧脚本），但完全不给反馈的可选字段等于永远为空。

**REST**

- 请求体可选：`{"note": "..."}`。**body 为空或不是合法 JSON 时不报错**，按无说明处理——旧版 CLI 不发 body，必须能照常归档。
- 响应体：`{"ok": true, "note_saved": true}`。`note_saved` 恒等于「本次请求带了非空 note 且已落库」。

**事件**

```json
{"seq": N, "type": "archived", "payload": {"note": "..."}}
```

**空 note 照发事件**。B67 等的是「归档了」这个信号，不是「有没有说明」；把发事件和写没写话绑定，等于让人忘写一句话就把下游会话永久冻住。

### 3.4 长度上限

`--note` 硬上限 **4096 字节**，超出**直接报错**，CLI 与 server 两侧都校验（服务端不信任客户端）。CLI 侧报错退出非 0 且**不发起请求**；server 侧返回 400。

不截断的理由是 B6 的直接教训：那条缺陷正是「静默截断让审核者盲信自己看到的是全文」。这里同理——审核者写了 6KB 说明、系统悄悄存 4KB，比直接拒绝糟糕得多。

4096 的取值：一句话说明的两个数量级以上，同时挡住「把整个 diff 粘进来」这类误用。

### 3.5 旧 agentd 兼容

新 CLI 打旧 agentd 时，旧 agentd 忽略 body、归档成功、说明静默消失，而审核者以为自己留了话。这正是 B30 单独立项修过的哑失败形态。

处置：客户端读响应体的 `note_saved`，**字段缺失按 false 处理**（§2.5 的既有模式）。CLI 在「传了非空 note 但 `note_saved` 为 false」时打 stderr 警告：

```
说明未保存：对端 agentd 版本较旧，不支持归档说明。任务已正常归档。
```

任务确实已归档，所以退出码仍为 0——警告的是说明丢失，不是归档失败。

## 4. 错误处理与边界

### 4.1 各失败点的处置

| 失败点 | 处置 | 理由 |
|---|---|---|
| `SetTaskDoneNote` 失败 | 整体返回错误，**不迁移状态** | 任务仍在 `waiting_review`，审核者可原样重试 |
| `AppendEvent(archived)` 失败 | Error 日志，**不阻塞归档** | 状态已经迁移完了，此时失败回不去；与同文件 worktree 清理失败的处置一致 |
| `hub.Publish` | 无返回值，不涉及 | — |
| note 超长 | CLI 不发请求直接报错；server 400 | §3.4 |
| body 非法 JSON / 为空 | 按无说明处理，正常归档 | 旧版 CLI 不发 body |

### 4.2 顺序约束（本改动唯一会静默失效的地方）

`hub.CloseTask` 一旦跑在 `Publish` 之前，正在 `wait --follow` 的人一条 `archived` 都收不到——而事件**已经进了库**，任何只断言「库里有这条事件」的测试都会全绿。这个 bug 能一路活到生产，且症状（B67 永远等不到）离原因很远。

因此必须有一条专门的回归钉：**挂一个真实订阅者，断言它在订阅被关闭之前收到了 `archived`**。断言事件入库不算数。

### 4.3 对 wait 的连带影响

| 场景 | 变化 |
|---|---|
| `wait --follow` | 多收一行 `archived`，随后订阅关闭、正常退出 0。「每行一个事件 JSON」的 stdout 契约不变 |
| 一次性 `wait` | 过去归档无声，现在会收到 `archived` 并退出 0。这是修复而非破坏 |
| `autoSyncAfterWait` | **不**对 `archived` 触发。归档时 managed worktree 已删，且 `completed` 时已同步过一次（[wait.go:243](../../../cmd/wait.go) 的类型判断保持原样即可） |

### 4.4 文档同步

`archived` 是一个**会唤醒 wait** 的新事件类型。仓库里「哪些事件唤醒 wait」的契约表必须同步——B60 那条 idea（`approver_decision` 会唤醒 wait，与文档契约相矛盾）正是这张表和实现对不上的产物。实现时需定位并更新 README 与 handoff skill 中的事件表，不能只改代码。

## 5. 测试

| 层 | 用例 |
|---|---|
| `store` | `done_note` 迁移幂等（重复 Open 不报错）；`SetTaskDoneNote` 往返；旧库无该列时 Open 后能读到空串 |
| `manager` | `Done(note)` 落列且发 `archived` 事件；**订阅者在 CloseTask 之前收到 archived**（§4.2 回归钉）；空 note 仍发事件；`SetTaskDoneNote` 失败时任务保持 `waiting_review` |
| `server` | 带 body 归档 → `note_saved=true`；空 body 归档成功 → `note_saved=false`；非法 JSON body 归档成功；超长 note → 400 |
| `client` | 响应缺 `note_saved` 字段 → 返回 false |
| `cmd` | `--note` 透传；省略时 stderr 有提醒且 stdout 仍为 `{"ok":true}`；超长时不发请求直接报错；`note_saved=false` 且传了 note 时 stderr 有警告且退出码 0 |

真机验收：一台远程执行机上跑完整回路——派发 → 完成 → 一个终端 `wait --follow`，另一个终端 `done --note`，确认 follow 侧**看到了** `archived` 行且带 note，随后正常退出；再 `show` 确认 `done_note` 在快照里。
