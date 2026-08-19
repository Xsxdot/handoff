# B100 设计：把「回合失败」与「执行失败」在事件层面分开

**日期**：2026-08-15
**基线**：`w4-delivery`（含 B92/B93/B99/B102/B103/B104）

> **本 spec 由审核者独立撰写**。用户已裁决方向：**走事件层面的可判别字段，不做
> `fail_reason` 字符串嗅探**。§3 的「新增事件类型」与「follow 对齐 completed」
> 是我在该方向内做的两处具体选择，写明了代价，可推翻。

---

## 1. 问题

`wait --follow` 一见到 `type:failed` 就判任务终结，打出

```
follow 结束：任务已失败
follow 正常结束：任务已终结
```

并以 **0 退出**——而此刻 `handoff show` 报的是 `waiting_review`，任务好端端等着审。
两次真机实测：任务 `47ac0abb` seq 870、`f8049b63` seq 877。

### 1.1 四个生产者，只有一个非终态

把 `EventTypeFailed` 的生产者数了一遍（代码坐标已核）：

| 生产者 | 之后任务落到哪 | 终态？ |
|---|---|---|
| `manager.go:1216` `Stop`（协调者主动中止） | `failed` | ✅ |
| `manager.go:2696` `handleResult`（回合失败） | **先 `transitToReview`** → `waiting_review` | ❌ |
| `reconcile.go:163` `reconcileExecutorGone` | `failed` | ✅ |
| `watchdog.go:361` | `failed` | ✅ |

两个消费者——`cmd/wait.go:243`（自动同步的触发集）与
`internal/client/client.go:1463`（follow 收流）——把这四个**一律当终态**。
这就是 B100 的全部。

### 1.2 为什么不能靠 `fail_reason`

`fail_reason` 是**给人看的散文**：由十来处各自措辞、取值集合不封闭、改一句文案
就能静默改掉客户端行为。更要命的是**它压根不携带这个事实**——`handleResult` 的
reason 与 `Stop` 的 reason 只是碰巧不同，没有任何东西保证它们必须不同。

而**生产者本来就知道答案**：它下一行调的是 `transitToReview` 还是迁 `failed`。
事件层面分开，只是把这个已知事实写成一个封闭取值的字段让客户端 switch。

## 2. 顺带暴露的一处不一致

`client.go:1459-1465` 的注释自己写着「回合失败已迁 waiting_review，可 continue，
但 continue 后需要重新挂 follow」——**作者当时就知道这是回合失败**，仍然选择收流。

可是 `completed` 走的是**同一个状态迁移**（也进 `waiting_review`），follow 却
**不收流**。两个后果完全相同的事件，在 follow 里行为相反。这个不一致必须一并
消除，否则修完 B100 还会留下「为什么 completed 不断我，turn_failed 断我」。

## 3. 方案

### 3.1 新增事件类型 `turn_failed`（可推翻）

`proto` 增 `EventTypeTurnFailed EventType = "turn_failed"`。
`handleResult` 的 `!OK` 分支改发它；其余三个生产者**不动**，`failed` 从此**收窄
为「任务真的终结了」**。payload 结构复用现有的 `newFailedPayload`，不变。

**为什么选新类型，而不是「`failed` 保留 + payload 加 `scope` 字段」**：差别在
**旧客户端**。旧客户端遇到未知 type 会当成普通事件继续跟随——于是它不再假终态
退出，**bug 对旧 CLI 自动消失**；而加 payload 字段的话，没升级的客户端照旧误判。

**代价，如实写在这里**：

1. 所有枚举事件类型的地方都要认这个新类型——`mirror.go:261`、`backlog_summary`
   的对账、`handoff show` 的展示、控制台事件流。漏一处就是一条不显示的事件。
2. 旧客户端会「什么都不显示地干等」：它收到 `turn_failed` 不认识，不退出也不
   提示，直到人自己去 `show`。这**比现在的假终态好**（假终态会让人以为任务死了
   而放弃），但不是零代价。
3. 事件类型是对外契约的一部分，加了就撤不回来。

### 3.2 follow 对 `turn_failed` 的行为：与 `completed` 完全一致（可推翻）

**不收流，只投递。** 理由见 §2：两者是同一个状态迁移，行为必须一样。
`failed` 保持收流不变。

这条把 skill 里那句「follow 进程退出本身就是信号，0=任务已终结」重新变成真的。

### 3.3 `autoSyncAfterWait` 的触发集加上 `turn_failed`

`cmd/wait.go:243` 现在是 `{completed, failed}`。加 `turn_failed`——理由就是该函数
自己的注释：「失败恰恰是最需要把代码拉到本地翻的时候」。回合失败同样如此。

### 3.4 不改状态机

任务状态一个不加、迁移规则一条不改。本次只动事件类型。

## 4. 影响面

| 文件 | 改动 |
|---|---|
| `internal/proto/proto.go` | 加 `EventTypeTurnFailed` 常量与注释 |
| `internal/agentd/manager.go` | `handleResult` 的 `!OK` 分支改发 `turn_failed` |
| `internal/agentd/mirror.go` | 终态事件判定处认这个新类型（**它不是终态**，别镜像成终态） |
| `internal/client/client.go` | follow 收流判定只认 `failed`；`turn_failed` 走普通投递 |
| `cmd/wait.go` | 自动同步触发集加 `turn_failed` |
| `web/`（若有事件类型枚举/文案映射） | 加一条，文案「回合失败」 |
| `README.md` + `~/.claude/skills/handoff/SKILL.md` | 事件分诊表加一行（skill 由审核者改，不在本次范围） |

## 5. 风险

**漏改一处消费端 = 一条静默消失的事件。** 这是本次唯一的真风险，且它不会
编译失败。对策是 §6 第 3 条的穷举检索——**必须把所有 `EventTypeFailed` 的
读取点逐个列出来判一遍**，不是只改我上面列的那几个。

**`backlog_summary` 的 `actionable` / `stale` 分类**：`turn_failed` 既不是工单也
不是终态，它应当**既不进 `actionable` 也不导致对账把任务当死**。这一处要专门看。

## 6. 验收

1. `go build ./... && go vet ./... && go test -count=1 ./...` 全绿、0 FAIL；
   `web/` 三件套（`vitest` / `tsc -b` / `vite build`）全绿。
2. **单测**（每条都要能被对应的变异咬住）：
   - `TestHandleResultEmitsTurnFailedOnTurnFailure`：回合失败落的是 `turn_failed`，
     且任务状态是 `waiting_review`；
   - `TestStopEmitsFailed`：`Stop` 仍落 `failed` 且任务是 `failed`；
   - `TestFollowDoesNotStopOnTurnFailed`：follow 收到 `turn_failed` **不收流**，
     后续事件仍能投递（这条是 B100 的正身）；
   - `TestFollowStopsOnFailed`：`failed` 仍收流（防止改过头）；
   - `TestAutoSyncTriggersOnTurnFailed`。
3. **穷举检索并在 ledger 里列出结论**：`grep -rn "EventTypeFailed" internal/ cmd/ web/`
   的**每一个**读取点，逐个写明「改了 / 不用改，为什么」。少列一个即为未完成。
4. **不得有 `fail_reason` 的字符串匹配**：`grep -rn "fail_reason" internal/client cmd/`
   不得出现按文案判断分支的代码。
5. 真机复验由审核者做（派一个必然回合失败的任务，断言 follow 不退出、
   `show` 是 `waiting_review`、`continue` 后事件继续流入同一个 follow）。

## 7. 明确不做

- 不改任务状态机、不加新状态。
- 不动其余三个 `failed` 生产者的语义。
- 不改 `fail_reason` 的任何文案（改了会掩盖「客户端不许嗅探文案」这条纪律是否被遵守）。
- 不顺手修 B101/B105。
