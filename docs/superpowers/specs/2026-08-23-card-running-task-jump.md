# 卡详情带上运行中的任务并能跳过去（B181）设计

> 状态：**已批准**（用户 2026-08-23 批准）
> 级别：**L2**（单子系统：web 控制台前端；不动契约层——后端零改动）
> 卡：B181 —— 「每张卡如果有正在运行的任务，在详情中带上，点击跳转到对应目录和tab」

## 1. 问题陈述

这张卡要的两样东西，各有一半已经存在，缺口比原话窄但比原话尖。

**「在详情中带上」——区块已有，但状态是错的那一列。**
`web/src/app/cards/CardDrawer.tsx:741` 已有「关联执行（task）」区块，每行显示
`TaskID / Purpose / LastType / Target`，点开还能就地答工单。但 `LastType` 不是任务状态，
它是**该 task 最后一条镜像事件的 `task_type`**（`internal/ledger/taskstate.go:44`）。
于是抽屉上你看得到一行任务，却看不出它此刻在不在跑——用户要的正是这个信息。

**「点击跳转到对应目录和 tab」——整条跳转链路已经建好了，只差一个入口。**
`/tasks/:id` 深链早已存在（`web/src/app/shell/Shell.tsx:493`）。`TaskDeepLink`
（`Shell.tsx:702`）做的正是这张卡描述的两件事：`findBaseOfTask` 从项目树解析出该任务
所在的**目录**，`openTaskTui` 在那个目录下开一个 **TUI tab**。跨机也支持——
`findBaseOfTask` 遍历的是 `loc.machine` 维度，返回的 `workspaceBase(project, loc.machine, ws)`
带机器信息。而 `CardDrawer` 里 task 行的 `onClick` 现在是 `toggleTask`（展开工单），
从卡到任务没有任何一条路。

## 2. 承重现状（改之前必须知道的）

| 事实 | 出处 | 为什么承重 |
|---|---|---|
| `LastType` 是最后一条镜像事件的类型，不是 task state | `internal/ledger/taskstate.go:44` | **本设计最大的坑，见 §3.1** |
| 卡上 task 实况刻意「单一数据源，不跨机拨号」，代价是滞后，滞后由 MirrorHealth 显性化 | `internal/ledger/taskstate.go` 文件头 | 这是已冻结的架构决定；取真实状态**不许**改成跨机拨号 |
| `useTasks()` 是 2.5s 的全量任务流，看板卡片、左栏任务节点、全部聚合计数都吃它 | `web/src/app/data/useTasks.ts` | 任务的真实 `state` 在这里，且跨机由汇总方盖章（`Task.machine`） |
| `Task` 有 `state` / `work_dir` / `repo_path` / `machine` / `target` | `web/src/api/types.ts` 的 `Task` | 判「在不在跑」与定位目录的素材齐备 |
| `/tasks/:id` 深链已存在并已接好 `findBaseOfTask` + `openTaskTui` | `web/src/app/shell/Shell.tsx:493,702,405` | 跳转是复用一条现成链路，不是新建 |
| `findBaseOfTask` 解析不到时返回 `null`，`openTaskTui` 会开在当前 base 下 | `web/src/app/tree/ProjectTree.tsx`；`Shell.tsx:405` | 工作树已删的历史任务会降级——要说清而不是假装不会发生 |
| `TERMINAL_STATES = {completed, failed}`，与看板分栏共用同一批字符串 | `web/src/app/workbench/TaskPickerDialog.tsx` | 「在跑」的判据必须与既有两处口径一致，不能自造第三套 |
| `/cards` 是 fullPageRoute，中央区被整页替换 | `web/src/app/shell/Shell.tsx:391` | 跳去任务 = 离开卡页面，这是形态上必须承认的代价 |
| `CardsPage` 目前不接收任何 task 数据 | `web/src/app/shell/Shell.tsx:483` | 需要新增一条数据通路（调 `useTasks()`），这是本设计唯一的结构改动 |

## 3. 方案

### 3.1 状态：关联真实任务流，**不要**从 LastType 推

把 `useTasks()` 的结果传进抽屉，按 `TaskStateRow.TaskID` 关联到 `Task`，
显示它的真实 `state`，并用与看板同一套 `StateDot` / `stateTone` 渲染。

**为什么不从 `LastType` 推**——这是本设计里唯一一条不能妥协的：事件类型与任务状态
不是一回事，而且历史上已经因为混淆它们付过代价。`failed` 早已拆成
`failed` / `turn_failed` 两种，`turn_failed` 不是终态、可以 continue；codex 收尾时
`completed` 事件已带 commit，任务 state 却仍是 `waiting_review`。**按最后一条事件判
「跑没跑完」会得出与看板相反的结论**——同一个任务，看板显示在跑，卡上显示已结束。
两个面自相矛盾比缺这个信息更糟。

关联不上时（任务已归档/清理出任务流）**如实降级**：显示「实况未知」并把 `LastType`
作为线索列出，不猜、不冒充。这与 `taskstate.go` 那条「滞后要显性化」的既有纪律同向。

**不改后端、不跨机拨号**：真实 state 来自那条本来就在跑的 2.5s 汇总流，
`taskstate.go` 的单一数据源决定原样成立。

### 3.2 排序与范围：不过滤，运行中排前面

- 已结束的任务**不隐藏**——一张卡跑过哪几轮是审计线索，藏掉等于让抽屉说谎。
- 排序改为：运行中的在前，其余按 `LastSeq` 倒序。用户扫一眼最上面就是「现在在跑的」。
- 区块标题从「关联执行（task）」改为带计数的形式（如「关联执行 · 1 个在跑 / 共 4 个」），
  这样不展开也知道有没有活着的任务。

### 3.3 跳转：复用深链，独立按钮，不动现有点击

- 每行右侧加一个独立的跳转按钮（↗），点它 `navigate('/tasks/' + taskID)`。
  剩下的（解析目录、开 TUI tab、跨机）全部由既有的 `TaskDeepLink` 完成——**本设计
  不复制它的任何逻辑**。
- **整行点击维持现状**（展开/收起工单面板）。工单答复是这块区域已有的职责，
  把它换成跳转会破坏一个正在用的入口。
- 跳转即离开卡页面（`/cards` 是整页路由），这是接受的代价：点「去任务」的意图本来
  就是去干活，而卡随时可以从左栏回来。**弃选**：为保住卡上下文做一个「跳转后可返回」
  的机制（回退栈/浮层/分屏）——它要引入一套 UI 状态，而换回来的只是少点一次左栏。
- 目录解析不到时（工作树已删）沿用 `openTaskTui` 既有的降级：tab 开在当前目录下。
  按钮上用 title 说明这一点，不额外拦截——拦截等于把一个能看日志的入口关掉。

## 4. 用户故事

1. 我点开一张卡，最上面就写着「1 个在跑」，我不用去看板也知道这张卡此刻有活在动。
2. 我看到那一行带着和看板上一模一样的状态点，颜色语义一致，不用在心里做翻译。
3. 我点行尾的 ↗，控制台切到该任务所在的目录并打开它的 TUI tab，我直接看到它在干什么。
4. 那个任务跑在 linux-01 上，我照样点得开——目录解析带机器维度。
5. 一张卡上有 4 个 task、3 个已结束，我仍然看得到那 3 个（审计线索），但它们排在下面。
6. 某个老任务已经被清出任务流，那一行诚实写着「实况未知」，而不是显示一个我信不过的状态。

## 5. 实现决定

- 数据通路：`CardsPage` 调 `useTasks()`，把 `Task[]` 传给 `CardDrawer`。
  **不**在 `CardDrawer` 内部另起一条轮询——同一页面两条 2.5s 流会各自跳动，
  卡上的状态与看板的状态会在不同的时刻更新，用户会看到两处短暂不一致。
- 「在跑」的判据复用 `isTerminalState`（`TaskPickerDialog.tsx` 已导出），不新写一套字符串集合。
- 状态渲染复用 `StateDot` / `stateTone`（`web/src/app/board/`），不新写颜色映射。
- 跳转只做一件事：`navigate('/tasks/' + id)`。任何「顺手把目录也切一下」的想法都是重复
  `TaskDeepLink` 的逻辑，明确禁止。

## 6. 测试决定（接缝清单）

**接缝就一个：`CardDrawer` 组件**（既有 `CardDrawer.test.tsx` 已在这个缝上）。

| 判据 | 落点 |
|---|---|
| 给定任务流里该 task 为 running，行上渲染 running 状态而非 LastType | `CardDrawer.test.tsx` |
| **关键回归**：任务流 state=running 但最后一条事件是 `turn_failed` 时，行上显示 running | 同上 |
| 任务流里关联不上时显示「实况未知」并列出 LastType，不显示任何状态 | 同上 |
| 运行中的行排在已结束的行前面 | 同上 |
| 区块标题的在跑计数与任务流一致 | 同上 |
| 点 ↗ 调用 navigate('/tasks/{id}')，且**不**触发展开 | 同上 |
| 点整行仍然展开工单面板（现状不回归） | 同上 |

**变异复验（验收时必做两条）**：把状态来源改回 `LastType` → 那条关键回归用例必须转红；
把 ↗ 的 `stopPropagation` 去掉 → 「不触发展开」那条必须转红。

## 7. Out of Scope

**永不做**
- 跨机拨号取任务实时状态（违反 `taskstate.go` 已冻结的单一数据源决定）。
- 从 `LastType` 推导运行状态（§3.1，这正是本设计要消灭的东西）。
- 跳转后返回卡上下文的回退机制（§3.3 弃选）。

**本期不做、后续可能要做**
- 看板卡片上的「有任务在跑」角标（用户原话只要详情；要做也是另一张卡的范围）。
- 在抽屉里直接停止/继续一个运行中的任务（那是任务页的职责，不该在卡上长出第二个操作面）。
- 目录解析不到时提示「工作树已删」（现在只在 title 里说明，够用了再说）。

## 8. 备注

- 本设计不新增任何后端端点、不改任何 wire DTO。`TaskStateRow` 保持原样——
  它少一个 state 字段不是缺陷，是「不跨机拨号」那条决定的正确结果。
- `/tasks/:id` 深链的注释写着它是「W3b 留下的」。本卡是它的第二个消费者；
  实现后值得在 `TaskDeepLink` 的注释里补一句谁在用它。
