# Plan：B181 卡详情带上运行中的任务并能跳过去

> 节点：charter-plan（产出物=本计划文档）。冻结物：`docs/superpowers/specs/2026-08-23-card-running-task-jump.md`（已批准，L2）。
> 执行者假设：对本仓库零上下文。本计划自包含——每一步给全代码块与命令，不需要猜邻居的实现。
> 级别：**L2**，单子系统（web 前端）。红线：后端零改动、零 wire DTO 变化、抽屉内禁止另起轮询（spec §5）、跳转只许 `navigate('/tasks/' + id)` 不许复制目录切换逻辑（spec §3.3）。

## 0. 开工前须知

- 只在当前分支工作，不切分支；每个 task 独立 commit，不 push。
- 所有前端命令都在 `web/` 目录下跑（依赖清单 `web/package.json`；本计划出稿时已在 `web/` 装好依赖）。下文写「跑定向测试」一律指：`cd web && npm run test -- <文件路径>`。
- 前端可观测性惯例：web 侧没有结构化 logger 库，既有惯例是带 `[tag]` 前缀的 `console.debug/warn`（先例：`web/src/app/tree/treePrefs.ts:77-79` 的 `[treePrefs]`、`web/src/app/workbench/terminalDebug.ts:70-78` 的 `[term:input]`）；eslint 没开 `no-console`（`web/eslint.config.js`），禁 print 在 web 侧无对应物。
- vitest 配置没有 `clearMocks`（`web/vite.config.ts:38-41`）：mock 跨用例泄漏，每个用例必须自己重设 `mockResolvedValue`；DOM cleanup 已由 `web/src/test/setup.ts:17` 注册，不用自理。

## 1. 基线复核（出稿时已实跑；执行者动手前再跑一遍确认起点没漂移）

| 命令（在 `web/` 下） | 基线结果 |
|---|---|
| `npm run test -- src/app/cards/CardDrawer.test.tsx` | 28 passed |
| `npm run test -- src/app/cards/CardsPage.test.tsx` | 2 passed |
| `npm run typecheck`（tsc -b） | 无输出，退出码 0 |
| `npm run lint` | 0 errors / 17 warnings（全部存量） |
| `npm run test`（全量） | 116 files / 1108 tests passed |

基线 commit `31acac41`，node v24.16.0。若你动手前数字对不上，停下向协调者报告，不要在漂移的基线上开工。

## 2. 冻结契约（Consumes——每条都已在基线核实出处）

| 名字 | 精确签名 / 行为 | 出处 |
|---|---|---|
| TaskStateRow | `interface TaskStateRow { Target: string; TaskID: string; Purpose: string; LastType: string; LastSeq: number }` | `web/src/api/ledger.ts:58-64` |
| Task | `interface Task { id: string; state: string; … }`（本卡只消费 `id`、`state` 两字段） | `web/src/api/types.ts:15-42` |
| isTerminalState | `export function isTerminalState(state: string): boolean`，终态集合 `{completed, failed}` | `web/src/app/workbench/TaskPickerDialog.tsx:35-40` |
| TaskState | `export function TaskState({ state }: { state: string })`——状态圆点+中文文案；文案映射 `stateLabel`（未知状态原样透出）、色映射 `stateTone` | `web/src/app/board/StateDot.tsx:75-83`、`web/src/app/board/columns.ts:59-71,101-103` |
| useTasks | `export function useTasks(): ReturnType<typeof usePoll<Task[]>>`，2.5s 全量任务流 | `web/src/app/data/useTasks.ts:7-10` |
| PollState | `{ data: T \| null; disconnected: boolean; sessionExpired: boolean; errorText: string; refresh: () => void }` | `web/src/app/data/usePoll.ts:19-31` |
| 深链路由 | `<Route path="/tasks/:id" element={<TaskDeepLink …>} />`；TaskDeepLink = findBaseOfTask 解析目录 → openTaskTui 开 TUI tab → navigate('/')；解析不到时 tab 开在当前目录下（既有降级，不拦截） | `web/src/app/shell/Shell.tsx:493,697-724,400-412`；findBaseOfTask 返回 null 的语义见 `web/src/app/tree/ProjectTree.tsx:187-208` |
| LastType 不是状态 | `LatestTaskStates` 取的是最后一条镜像事件 payload 的 `task_type` | `internal/ledger/taskstate.go:13-17,40-46`；单一数据源决定见同文件 `:1-3` 头注 |
| 行内嵌按钮先例 | 外层 `role="button"`+`tabIndex={0}`+onKeyDown(Enter/Space)，内层真 `<button>` 用 `event.stopPropagation()` 掐冒泡 | `web/src/app/cards/CardItem.tsx:35-60` |

Produces（唯一对外接口变化，四个 task 合起来就是这一处签名）：

```tsx
// CardDrawer 新增两个可选 props（web/src/app/cards/CardDrawer.tsx）
tasks?: Task[]
onJumpToTask?: (taskId: string) => void
```

不新增导出函数、不改 wire 类型、不动 Go 侧。`CardDetail.task_states` 保持原样——它少一个 state 字段不是缺陷，是「不跨机拨号」决定的正确结果（spec §8）。

## 3. 任务总览

| Task | 内容 | 触及文件 |
|---|---|---|
| 1 | 抽屉任务行改吃任务流真 state + 「实况未知」降级 | `web/src/app/cards/CardDrawer.tsx`、`CardDrawer.test.tsx` |
| 2 | 运行中排前 + 区块标题计数 | 同上 |
| 3 | 行尾 ↗ 跳转按钮（stopPropagation） | 同上 |
| 4 | CardsPage 数据通路接线（useTasks + useNavigate）+ 管线回归 | `web/src/app/cards/CardsPage.tsx`、`CardsPage.test.tsx`、`web/src/app/shell/Shell.tsx`（仅注释） |

顺序执行 1→2→3→4；每个 task 的定向测试绿了才允许提交。

---

## Task 1：行状态真实化——显示任务流的 state，「关联不上」如实降级

**Interfaces**：Consumes 见 §2（TaskStateRow / Task / TaskState）。本 task 只加 `tasks?: Task[]` prop；`onJumpToTask` 属 Task 3，此处不加。

**为什么不能从 LastType 推（spec §3.1，唯一不可妥协项）**：`LastType` 是最后一条镜像事件的 `task_type`，不是任务状态。`failed` 历史上已拆成 `failed`/`turn_failed`（后者可 continue、不是终态），codex 收尾时 `completed` 事件早于落态 `waiting_review`——按最后事件判「跑没跑完」会得出与看板相反的结论。

### 步骤 1.1：写失败测试（预期红）

改 `web/src/app/cards/CardDrawer.test.tsx`：

1a. import 区两处——RTL 导入补 `within`，类型导入补 `Task`（替换现有第 2-3 行）：

```tsx
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import type { Card, CardDetail } from '../../api/ledger'
import type { Task } from '../../api/types'
```

1b. 紧跟既有 `card()` 夹具函数之后新增 `task()` 夹具（必填字段集照抄 `web/src/api/types.ts:15-42`，风格照抄同文件 `card()`）：

```tsx
// task 造一个字段齐全的线格式 Task：缺一个必填字段 tsc 就红。
function task(over: Partial<Task> = {}): Task {
  return {
    id: 'task-x', target: 'local', repo_path: '/repo/handoff', branch: '',
    plan_path: '', plan_summary: '', executor_session: '', state: 'running',
    created_at: '', updated_at: '', name: '', executor: '', model: '',
    work_dir: '', worktree_managed: false, base_commit: '', base_ahead: 0,
    repo_dirty_count: 0, repo_dirty_files: '', done_note: '', ...over,
  }
}
```

1c. 文件末尾追加整个 describe 块（完整代码，直接粘贴）：

```tsx
describe('抽屉里的关联执行实况', () => {
  // detailWithRows 造一张带挂账行的卡详情。大小写与线格式一致：
  // task_states 是账本的 Go 风格 PascalCase（api/ledger.ts:58-64），
  // 任务流是 snake_case（api/types.ts:15-42）——这条接缝正是被测对象。
  const detailWithRows = (rows: CardDetail['task_states']): CardDetail => ({
    card: card({ id: 'B30', title: '在跑的卡', status: '进行中' }),
    relations: [], events: [], effective_base_branch: '', decisions: [], needs: '',
    children: [], task_states: rows,
  })

  it('行上显示任务流的真实 state，而不是最后一条镜像事件的类型', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detailWithRows([
      { Target: 'linux-01', TaskID: 'task-run', Purpose: 'implement', LastType: 'turn_end', LastSeq: 12 },
    ]))
    render(
      <CardDrawer
        id="B30" onClose={() => {}} onOpenCard={() => {}}
        tasks={[task({ id: 'task-run', state: 'running' })]}
      />,
    )
    // 卡头部的状态 chip 也叫「进行中」，断言必须收在任务行里
    const row = await screen.findByRole('button', { name: /^task-run/ })
    expect(within(row).getByText('进行中')).toBeInTheDocument()
    expect(within(row).queryByText('turn_end')).not.toBeInTheDocument()
  })

  it('关键回归：state=running 而最后一条镜像是 turn_failed 时，行上仍显示进行中', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detailWithRows([
      { Target: 'linux-01', TaskID: 'task-run', Purpose: 'implement', LastType: 'turn_failed', LastSeq: 13 },
    ]))
    render(
      <CardDrawer
        id="B30" onClose={() => {}} onOpenCard={() => {}}
        tasks={[task({ id: 'task-run', state: 'running' })]}
      />,
    )
    const row = await screen.findByRole('button', { name: /^task-run/ })
    // turn_failed 可 continue 不是终态；把它当状态渲染出来 = 和看板打架
    expect(within(row).queryByText(/turn_failed/)).not.toBeInTheDocument()
    expect(within(row).getByText('进行中')).toBeInTheDocument()
  })

  it('任务已不在任务流里时如实显示「实况未知」，把 LastType 当线索列出，不冒充状态', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detailWithRows([
      { Target: 'linux-01', TaskID: 'task-gone', Purpose: 'implement', LastType: 'question', LastSeq: 3 },
    ]))
    // tasks=[]：流已接入但查无此任务（归档清出流是真实情形）
    render(<CardDrawer id="B30" onClose={() => {}} onOpenCard={() => {}} tasks={[]} />)
    const row = await screen.findByRole('button', { name: /^task-gone/ })
    expect(within(row).getByText('实况未知 · 最后事件 question')).toBeInTheDocument()
    // 六个已知状态标签一个都不许出现：不知道就说不知道
    for (const label of ['等待执行', '进行中', '等你答复', 'Review', '已完成', '失败']) {
      expect(within(row).queryByText(label)).not.toBeInTheDocument()
    }
  })

  it('连最后事件类型都没有时只说「实况未知」，不再沿用旧的「未知」占位', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detailWithRows([
      { Target: 'local', TaskID: 'task-fresh', Purpose: 'plan', LastType: '', LastSeq: 0 },
    ]))
    render(<CardDrawer id="B30" onClose={() => {}} onOpenCard={() => {}} tasks={[]} />)
    const row = await screen.findByRole('button', { name: /^task-fresh/ })
    // getByText 默认整串精确匹配，不会误中带线索的长串
    expect(within(row).getByText('实况未知')).toBeInTheDocument()
    expect(within(row).queryByText(/^最后事件/)).not.toBeInTheDocument()
  })
})
```

跑定向测试。预期：新增 4 条红（组件还没有 `tasks` prop，运行时表现为找不到目标文本；`/^task-run/` 的行也匹配不到），既有 28 条仍绿。**既有用例若红了，停下修认知，不要继续。**

### 步骤 1.2：最小实现（转绿）

改 `web/src/app/cards/CardDrawer.tsx`，五处：

2a. import 区替换为（新增三个来源）：

```tsx
import { useEffect, useMemo, useRef, useState } from 'react'
import { X } from 'lucide-react'
import { fetchTaskDetail, replyTicket } from '../../api/client'
import type { Task, TaskDetail, Ticket } from '../../api/types'
import { acceptCard, answerDecision, attachFile, clearCardNeeds, detachFile, fetchCardDetail, moveCard, noteCard, patchCard, runCardStep } from '../../api/ledger'
import type { CardDetail, Decision, LedgerEvent, NodeDef, TaskStateRow } from '../../api/ledger'
import { errorMessage } from '../lib/format'
import { TicketsPanel } from '../task/TicketsPanel'
import { boardColumns } from './columns'
import { TaskState } from '../board/StateDot'
import { isTerminalState } from '../workbench/TaskPickerDialog'
```

跨目录消费 `app/board/` 有先例：`StateDot.tsx:10-12` 文件头注释写明这是它的设计边界。

2b. 模块级（放在 `type DrawerTaskDetail …` 定义之前）加两个纯函数：

```tsx
// linkedTaskOf 把账本挂账行关联到任务流里的真实任务；关联不上返回 undefined。
// 关联不上是真实情形（任务已归档清出流 / 流首拉未回），调用方按「实况未知」
// 如实降级，不猜不冒充——与 internal/ledger/taskstate.go 文件头「滞后要显性化，
// 不拿陈旧实况冒充新鲜」是同一纪律在前端的落法。
function linkedTaskOf(row: TaskStateRow, tasks: Task[] | undefined): Task | undefined {
  return tasks?.find((t) => t.id === row.TaskID)
}

// isRunningRow 判「这一行此刻在不在跑」。口径刻意与看板分栏、任务选择弹层同源：
// 非 isTerminalState 即在跑（waiting_answer/waiting_review 是「等你动手」，不是
// 「结束」；spec §5 明令复用这一个终态集合，不许自造第三套）。关联不上的一律
// 不算在跑：不知道的事不能报成「活着」。n 是几十量级，不做 memo 化。
function isRunningRow(row: TaskStateRow, tasks: Task[] | undefined): boolean {
  const live = linkedTaskOf(row, tasks)
  return live !== undefined && !isTerminalState(live.state)
}
```

2c. props 签名扩展（解构参数与类型各加一行 `tasks`，注释写清数据通路纪律）：

```tsx
export function CardDrawer({
  id,
  onClose,
  onOpenCard,
  workflowStates,
  initialSection,
  nodes,
  tasks,
}: {
  id: string
  onClose: () => void
  onOpenCard: (id: string) => void
  workflowStates?: string[]
  initialSection?: 'merge'
  nodes?: NodeDef[]
  // 任务实况快照：调用方（CardsPage）把页面级 useTasks() 的结果原样传下来。
  // 抽屉不自起第二条轮询——同页两条 2.5s 流会各自跳动，卡上的状态会和看板
  // 在不同时刻更新（spec §5）。undefined = 流未接入或首拉未回。
  tasks?: Task[]
})
```

2d. 「关联执行」区块 map 回调：变量 `task` 改名 `row`（它其实是 TaskStateRow，改名避免和真任务混淆），取 `linked`，替换右侧状态列。把现状 `CardDrawer.tsx:744-756` 的：

```tsx
                {(detail.task_states ?? []).map((task) => {
                  const open = expandedTask === task.TaskID
                  const taskDetail = taskDetails[task.TaskID]
                  return (
                    <div key={`${task.Target}/${task.TaskID}`} className="mb-1 rounded-md border text-xs">
                      <button
                        type="button"
                        aria-expanded={open}
                        onClick={() => toggleTask(task.TaskID)}
                        className="flex w-full items-center gap-2 px-2 py-1.5 text-left"
                      >
                        <span className="font-mono">{task.TaskID}</span><span>{task.Purpose}</span><span className="ml-auto text-muted-foreground">{task.LastType || '未知'}</span><span className="text-muted-foreground">{task.Target}</span>
                      </button>
```

换成：

```tsx
                {(detail.task_states ?? []).map((row) => {
                  const open = expandedTask === row.TaskID
                  const taskDetail = taskDetails[row.TaskID]
                  const linked = linkedTaskOf(row, tasks)
                  return (
                    <div key={`${row.Target}/${row.TaskID}`} className="mb-1 rounded-md border text-xs">
                      <button
                        type="button"
                        aria-expanded={open}
                        onClick={() => toggleTask(row.TaskID)}
                        className="flex w-full items-center gap-2 px-2 py-1.5 text-left"
                      >
                        {/* 实况来自页面级 2.5s 任务流的真 state，渲染与看板同一套
                            圆点+文案。LastType 只是镜像事件的类型不是状态：
                            turn_failed 可 continue、completed 事件早于落态，拿它判
                            「跑没跑完」会和看板得出相反结论（spec §3.1）。关联不上
                            就写「实况未知」并把 LastType 当线索列出。 */}
                        <span className="font-mono">{row.TaskID}</span><span>{row.Purpose}</span>
                        {linked ? (
                          <span className="ml-auto"><TaskState state={linked.state} /></span>
                        ) : (
                          <span className="ml-auto text-muted-foreground">实况未知{row.LastType !== '' && ` · 最后事件 ${row.LastType}`}</span>
                        )}
                        <span className="text-muted-foreground">{row.Target}</span>
                      </button>
```

map 回调其余部分同步把 `task.` 改成 `row.`（共剩 4 处：`taskLoading === row.TaskID`、`taskErrors[row.TaskID]`、`{taskDetail && (`、`replyTaskTicket(row.TaskID, ticket, answer)`），逻辑零变化。

2e. 可观测性说明（本步骤的日志决策）：渲染路径**刻意不加日志**——抽屉随 2.5s 轮询重渲染，行级日志每轮刷屏，噪声大于价值；「关联不上」的降级已经按仓库纪律**显性化在界面上**（「实况未知」文案），不存在静默吞错。本 task 没有新增网络调用或事件分支。真正的新交互入口（跳转）在 Task 3/4 落日志。

### 步骤 1.3：验证并提交

```bash
cd web
npm run test -- src/app/cards/CardDrawer.test.tsx   # 预期 32 passed
npm run typecheck                                    # 无输出
```

测试范围声明：只跑 `web/src/app/cards/CardDrawer.test.tsx` 与 typecheck；全量测试不属于单个 task。

提交：`git add web/src/app/cards/CardDrawer.tsx web/src/app/cards/CardDrawer.test.tsx && git commit -m "feat(cards): 抽屉任务行改吃任务流真状态，关联不上如实降级"`

---

## Task 2：排序与标题计数——运行中排最前，标题带「N 个在跑 / 共 M 个」

**Interfaces**：无新接口面；消费本组件 Task 1 已加的 `tasks?: Task[]` 与 §2 的 `isTerminalState`。

### 准备动作：把测试夹具提升为模块级

Task 1 的 `detailWithRows` 定义在 describe 块内，Task 2/3 的用例也要用。把它从「抽屉里的关联执行实况」describe 里剪切出来，放到模块级 `task()` 夹具旁边（内容一字不变），原位置删除。

### 步骤 2.1：写失败测试（预期红）

文件末尾追加：

```tsx
describe('抽屉里的关联执行排序与计数', () => {
  it('运行中的行排在前面，其余按最后事件序号倒序', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detailWithRows([
      { Target: 'local', TaskID: 'task-old-done', Purpose: 'implement', LastType: 'turn_end', LastSeq: 20 },
      { Target: 'linux-01', TaskID: 'task-live', Purpose: 'implement', LastType: 'question', LastSeq: 5 },
      { Target: 'local', TaskID: 'task-new-done', Purpose: 'review', LastType: 'review_verdict', LastSeq: 40 },
    ]))
    render(
      <CardDrawer
        id="B30" onClose={() => {}} onOpenCard={() => {}}
        tasks={[
          task({ id: 'task-old-done', state: 'completed' }),
          task({ id: 'task-live', state: 'running' }),
          task({ id: 'task-new-done', state: 'failed' }),
        ]}
      />,
    )
    const section = screen.getByText(/关联执行/).closest('section') as HTMLElement
    const names = within(section).getAllByRole('button').map((el) => el.textContent ?? '')
    expect(names[0]).toMatch(/^task-live/)
    expect(names[1]).toMatch(/^task-new-done/) // 已结束里 LastSeq 40 的在前
    expect(names[2]).toMatch(/^task-old-done/)
  })

  it('区块标题的在跑计数与任务流一致（waiting_review 也算在跑，completed 不算）', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detailWithRows([
      { Target: 'local', TaskID: 'task-a', Purpose: 'implement', LastType: 'question', LastSeq: 1 },
      { Target: 'local', TaskID: 'task-b', Purpose: 'review', LastType: 'review_requested', LastSeq: 2 },
      { Target: 'local', TaskID: 'task-c', Purpose: 'plan', LastType: 'turn_end', LastSeq: 3 },
    ]))
    render(
      <CardDrawer
        id="B30" onClose={() => {}} onOpenCard={() => {}}
        tasks={[
          task({ id: 'task-a', state: 'waiting_answer' }),
          task({ id: 'task-b', state: 'waiting_review' }),
          task({ id: 'task-c', state: 'completed' }),
        ]}
      />,
    )
    expect(await screen.findByText('关联执行 · 2 个在跑 / 共 3 个')).toBeInTheDocument()
  })

  it('任务流未接入时标题不带计数——不知道就说不知道，不谎报「0 个在跑」', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detailWithRows([
      { Target: 'local', TaskID: 'task-x', Purpose: 'plan', LastType: '', LastSeq: 0 },
    ]))
    render(<CardDrawer id="B30" onClose={() => {}} onOpenCard={() => {}} />)
    expect(await screen.findByText('关联执行（task）')).toBeInTheDocument()
  })
})
```

跑定向测试。预期前两条红（现在既不排序也无计数标题）、第三条绿（旧标题本来就是这个文案）。既有用例仍全绿。

### 步骤 2.2：最小实现（转绿）

改 `web/src/app/cards/CardDrawer.tsx` 两处：

2-A. 组件体内、`const groups = timelineGroups(filteredEvents)` 之后加派生值：

```tsx
  // 关联执行的展示序与计数：在跑的排最前（扫一眼就知道有没有活着的），其余按
  // 最后事件序号倒序；平局保持账本返回的原序（Array.prototype.sort 自 ES2019 起
  // 稳定）。tasks===undefined 时不动序也不计数：「在跑」无从判定，标题退回旧
  // 形态——不知道就说不知道，不谎报「0 个在跑」（spec §3.1/§3.2 的诚实降级）。
  const taskRows = useMemo(() => {
    const rows = [...(detail?.task_states ?? [])]
    if (tasks === undefined) return rows
    return rows.sort((a, b) => {
      const ra = isRunningRow(a, tasks)
      const rb = isRunningRow(b, tasks)
      if (ra !== rb) return ra ? -1 : 1
      return b.LastSeq - a.LastSeq
    })
  }, [detail, tasks])
  const runningCount = tasks === undefined ? null : taskRows.filter((row) => isRunningRow(row, tasks)).length
```

2-B. 「关联执行」区块两处替换——标题改为计数形态，遍历源从 `detail.task_states` 换成 `taskRows`：

```tsx
            {taskRows.length > 0 && (
              <section className="mb-5">
                <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">
                  {/* 计数与行渲染同源（同一个 taskRows/isRunningRow 派生），不会各说各话 */}
                  {runningCount === null ? '关联执行（task）' : `关联执行 · ${runningCount} 个在跑 / 共 ${taskRows.length} 个`}
                </h3>
                {taskRows.map((row) => {
```

（map 回调体保持 Task 1 完成后的样子，不动。）

### 步骤 2.3：验证并提交

```bash
cd web
npm run test -- src/app/cards/CardDrawer.test.tsx   # 预期 35 passed
npm run typecheck                                    # 无输出
```

测试范围声明：只跑上述定向测试与 typecheck。

提交：`git add web/src/app/cards/CardDrawer.tsx web/src/app/cards/CardDrawer.test.tsx && git commit -m "feat(cards): 抽屉关联执行在跑排前并带计数标题"`

---

## Task 3：行尾 ↗ 跳转按钮——独立入口，掐冒泡，不动整行点击

**Interfaces**：新增 prop `onJumpToTask?: (taskId: string) => void`（语义由调用方注入，实现固定为深链导航——见 Task 4）。§2 的「行内嵌按钮先例」（`CardItem.tsx:35-60`）是本 task 的 DOM 结构依据。

**为什么外层要从 `<button>` 换成 `div[role=button]`**：HTML 不允许 button 嵌套 button；照抄 CardItem 先例——外层 `role="button"`+`tabIndex={0}`+键盘处理保住可访问性，内层真按钮负责跳转。

### 步骤 3.1：写失败测试（预期红）

文件末尾追加：

```tsx
describe('抽屉里的任务跳转', () => {
  it('点 ↗ 发起跳转回调，且不触发展开', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detailWithRows([
      { Target: 'linux-01', TaskID: 'task-j', Purpose: 'implement', LastType: 'turn_end', LastSeq: 7 },
    ]))
    const onJump = vi.fn()
    render(
      <CardDrawer
        id="B31" onClose={() => {}} onOpenCard={() => {}}
        tasks={[task({ id: 'task-j' })]} onJumpToTask={onJump}
      />,
    )
    fireEvent.click(await screen.findByRole('button', { name: '跳到 task-j' }))
    expect(onJump).toHaveBeenCalledTimes(1)
    expect(onJump).toHaveBeenCalledWith('task-j')
    // 展开没被误触：aria-expanded 还是 false，工单加载占位也没出现
    expect(screen.getByRole('button', { name: /^task-j/ })).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByText('正在读取工单…')).not.toBeInTheDocument()
  })

  it('没给跳转回调时不画 ↗ 按钮', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detailWithRows([
      { Target: 'local', TaskID: 'task-nojump', Purpose: 'plan', LastType: '', LastSeq: 0 },
    ]))
    render(<CardDrawer id="B34" onClose={() => {}} onOpenCard={() => {}} tasks={[]} />)
    await screen.findByRole('button', { name: /^task-nojump/ })
    expect(screen.queryByRole('button', { name: /跳到/ })).not.toBeInTheDocument()
  })

  it('整行点击仍然展开工单面板——跳转按钮不抢走既有入口', async () => {
    const ledger = await import('../../api/ledger')
    const client = await import('../../api/client')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detailWithRows([
      { Target: 'linux-01', TaskID: 'task-tk', Purpose: 'implement', LastType: 'question', LastSeq: 9 },
    ]))
    vi.mocked(client.fetchTaskDetail).mockResolvedValue({
      task: { id: 'task-tk', state: 'waiting_answer' },
      tickets: [{ id: 'tk-9', kind: 'ask', request: '这里要用哪个基线？' }],
      events: [],
    } as never)
    render(
      <CardDrawer
        id="B33" onClose={() => {}} onOpenCard={() => {}}
        tasks={[task({ id: 'task-tk', state: 'waiting_answer' })]} onJumpToTask={vi.fn()}
      />,
    )
    fireEvent.click(await screen.findByRole('button', { name: /^task-tk/ }))
    expect(await screen.findByText('这里要用哪个基线？')).toBeInTheDocument()
  })
})
```

说明：`/^task-j/` 锚定行首所以不会误匹配「跳到 task-j」这个名字。跑定向测试，预期三条全红（没有 ↗ 按钮、prop 不存在）。

### 步骤 3.2：最小实现（转绿）

改 `web/src/app/cards/CardDrawer.tsx` 两处：

3-A. props 追加（`tasks` 之后）：

```tsx
  // 提供时每行渲染 ↗ 跳转按钮。语义固定为深链 navigate('/tasks/{taskId}')，
  // 由调用方注入（CardsPage 用 useNavigate 实现）；抽屉自己绝不解析目录或切
  // tab——那是 Shell 既有 TaskDeepLink 的职责，复制它等于养第二份会漂移的逻辑
  // （spec §3.3 明令禁止）。缺省不画按钮。
  onJumpToTask?: (taskId: string) => void
```

（解构参数列表同步加 `onJumpToTask,`。）

3-B. 「关联执行」整个区块替换为下面的最终形态（含 Task 1/2 成果，以此块为准整段粘贴覆盖 `{taskRows.length > 0 && (…)}` 到对应 `)}` 的全部内容）：

```tsx
            {taskRows.length > 0 && (
              <section className="mb-5">
                <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">
                  {/* 计数与行渲染同源（同一个 taskRows/isRunningRow 派生），不会各说各话 */}
                  {runningCount === null ? '关联执行（task）' : `关联执行 · ${runningCount} 个在跑 / 共 ${taskRows.length} 个`}
                </h3>
                {taskRows.map((row) => {
                  const open = expandedTask === row.TaskID
                  const taskDetail = taskDetails[row.TaskID]
                  const linked = linkedTaskOf(row, tasks)
                  return (
                    <div key={`${row.Target}/${row.TaskID}`} className="mb-1 rounded-md border text-xs">
                      {/* 整行点击=展开工单（现状职责，spec §3.3 不动它）。外层从
                          <button> 换成 div[role=button] 是为了容纳行内的 ↗ 真
                          按钮（button 不能嵌 button）；role/tabIndex/键盘处理
                          照抄 CardItem.tsx:35-44 的行内可点先例，cursor-pointer
                          补回原生 button 自带的指针。 */}
                      <div
                        role="button"
                        tabIndex={0}
                        aria-expanded={open}
                        onClick={() => toggleTask(row.TaskID)}
                        onKeyDown={(event) => {
                          if (event.key === 'Enter' || event.key === ' ') {
                            event.preventDefault()
                            toggleTask(row.TaskID)
                          }
                        }}
                        className="flex w-full cursor-pointer items-center gap-2 px-2 py-1.5 text-left"
                      >
                        {/* 实况来自页面级 2.5s 任务流的真 state，渲染与看板同一套
                            圆点+文案。LastType 只是镜像事件的类型不是状态：
                            turn_failed 可 continue、completed 事件早于落态，拿它判
                            「跑没跑完」会和看板得出相反结论（spec §3.1）。关联不上
                            就写「实况未知」并把 LastType 当线索列出。 */}
                        <span className="font-mono">{row.TaskID}</span><span>{row.Purpose}</span>
                        {linked ? (
                          <span className="ml-auto"><TaskState state={linked.state} /></span>
                        ) : (
                          <span className="ml-auto text-muted-foreground">实况未知{row.LastType !== '' && ` · 最后事件 ${row.LastType}`}</span>
                        )}
                        <span className="text-muted-foreground">{row.Target}</span>
                        {onJumpToTask && (
                          <button
                            type="button"
                            aria-label={`跳到 ${row.TaskID}`}
                            title="去该任务所在的目录并打开它的 TUI 标签页；目录解析不到时会开在当前目录下"
                            onClick={(event) => {
                              // 跳转必须掐掉冒泡：整行的点击语义是展开工单，
                              // 一次点击不能又跳走又把面板拉出来（spec §3.3；
                              // 验收含「去掉 stopPropagation 必须红」的变异复验）
                              event.stopPropagation()
                              onJumpToTask(row.TaskID)
                            }}
                            className="shrink-0 rounded border px-1.5 py-0.5 text-[11px] hover:bg-accent"
                          >↗</button>
                        )}
                      </div>
                      {open && (
                        <div className="border-t px-2 py-2">
                          {/* 远程 task 的工单在这里也答得了：agentd 的 byTask 中间件会把
                              /api/tasks/{id}/* 透明代理到该 task 的属主机器。所以这一段
                              是纯前端复用，不需要任何新后端。 */}
                          {taskLoading === row.TaskID && <p className="text-xs text-muted-foreground">正在读取工单…</p>}
                          {taskErrors[row.TaskID] && <p role="alert" className="break-words text-xs text-destructive">{taskErrors[row.TaskID]}</p>}
                          {taskDetail && (
                            <TicketsPanel
                              bare
                              tickets={pendingTickets(taskDetail)}
                              disabled={false}
                              onReply={(ticket, answer) => replyTaskTicket(row.TaskID, ticket, answer)}
                            />
                          )}
                        </div>
                      )}
                    </div>
                  )
                })}
              </section>
            )}
```

3-C. 可观测性说明：↗ 的动作日志落在调用方（Task 4 的 `jumpToTask`，带 taskId 输入）；抽屉侧只转发回调，无外部调用，不加重复日志。

### 步骤 3.3：验证并提交

```bash
cd web
npm run test -- src/app/cards/CardDrawer.test.tsx   # 预期 38 passed
npm run typecheck                                    # 无输出
npm run lint                                         # 0 errors，warning 数不多于基线 17
```

测试范围声明：只跑上述定向测试、typecheck、lint。

提交：`git add web/src/app/cards/CardDrawer.tsx web/src/app/cards/CardDrawer.test.tsx && git commit -m "feat(cards): 抽屉任务行尾 ↗ 跳深链且不抢整行展开"`

---

## Task 4：CardsPage 接线——useTasks 进、useNavigate 出、管线回归钉死

**Interfaces**：消费 CardDrawer 的两个可选 props（§2 Produces，逐字一致）与 §2 的 `useTasks`/`PollState`；产出无新接口。附带一处**仅注释**改动（spec §8 备注）：`Shell.tsx` 的 TaskDeepLink 文档头补第二个消费者。

### 步骤 4.1：写失败测试（预期红）

`web/src/app/cards/CardsPage.test.tsx` 全文替换为：

```tsx
// 账本页的呈现契约：项目级请示要说得清自己是谁、且不能藏在筛选后面；
// 卡到任务深链的管线要真通（B181）。
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import type { Card } from '../../api/ledger'
import type { Task } from '../../api/types'
import { CardsPage } from './CardsPage'

vi.mock('../../api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/client')>()),
  // CardsPage 自 B181 起挂了 useTasks()；不 mock 会在 jsdom 里发真实请求
  fetchTasks: vi.fn().mockResolvedValue([]),
}))

vi.mock('../../api/ledger', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/ledger')>()),
  fetchCards: vi.fn().mockResolvedValue({ cards: [], unlinked: { count: 0, tasks: [], unknown_targets: [] } }),
  fetchFlows: vi.fn().mockResolvedValue({ workflows: [], templates: [] }),
  fetchLedgerHealth: vi.fn().mockResolvedValue({ mirror: [] }),
  fetchDecisions: vi.fn().mockResolvedValue([
    { id: 2, card_id: '', body: '要不要先把 acc/ 临时分支清掉？', options: null, status: 'open', answer: '', created_by: 'cli:me@box' },
  ]),
}))

// CardsPage 用 useNavigate，必须包在 Router 里渲染（生产态 Shell 把它挂在 <Routes> 下）
const renderPage = () =>
  render(
    <MemoryRouter initialEntries={['/cards']}>
      <Routes>
        <Route path="/cards" element={<CardsPage />} />
        {/* 深链探针：只断言导航真的发生了，不复刻 TaskDeepLink 的目录解析逻辑 */}
        <Route path="/tasks/:id" element={<p>deep-link-hit</p>} />
      </Routes>
    </MemoryRouter>,
  )

describe('项目级请示横幅', () => {
  it('不开「需要你」筛选也要显示——它被算进了徽标，藏起来等于数字对不上', async () => {
    renderPage()
    expect(await screen.findByText(/要不要先把 acc\/ 临时分支清掉？/)).toBeInTheDocument()
  })

  it('要标明它不挂卡，否则贴在卡片列上方像是某张卡的', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByText(/项目级请示/)).toBeInTheDocument())
    expect(screen.getByText(/不挂卡/)).toBeInTheDocument()
  })
})

describe('卡到任务深链的数据通路', () => {
  // 必填字段集与 CardDrawer.test.tsx 的同名夹具同一份真相（api/types.ts:15-42）
  const task = (over: Partial<Task> = {}): Task => ({
    id: 'task-wire', target: 'local', repo_path: '/repo/handoff', branch: '',
    plan_path: '', plan_summary: '', executor_session: '', state: 'running',
    created_at: '', updated_at: '', name: '', executor: '', model: '',
    work_dir: '', worktree_managed: false, base_commit: '', base_ahead: 0,
    repo_dirty_count: 0, repo_dirty_files: '', done_note: '', ...over,
  })
  const wireCard: Card = {
    id: 'B50', title: '管线卡', status: '进行中', priority: '中', project: 'handoff',
    parent: '', workflow: '', workflow_version: 1, attachments: [], acceptance_criteria: '',
    created_at: '', updated_at: '',
  }

  it('抽屉里的 ↗ 经由 CardsPage 注入的回调真的走到 /tasks/:id', async () => {
    const ledger = await import('../../api/ledger')
    const client = await import('../../api/client')
    vi.mocked(client.fetchTasks).mockResolvedValue([task()])
    vi.mocked(ledger.fetchCards).mockResolvedValue({ cards: [wireCard], unlinked: { count: 0, tasks: [], unknown_targets: [] } })
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: wireCard,
      relations: [],
      events: [],
      task_states: [{ Target: 'local', TaskID: 'task-wire', Purpose: 'implement', LastType: 'question', LastSeq: 3 }],
      effective_base_branch: '', decisions: [], needs: '',
    })
    renderPage()
    fireEvent.click(await screen.findByText('管线卡')) // 看板上点开抽屉
    fireEvent.click(await screen.findByRole('button', { name: '跳到 task-wire' }))
    // 整条管线：useTasks → CardDrawer.tasks → 行内 ↗ → navigate('/tasks/task-wire')
    expect(await screen.findByText('deep-link-hit')).toBeInTheDocument()
  })
})
```

跑定向测试。预期：两条既有横幅用例仍绿；新管线用例红——现在 CardsPage 根本没把 tasks 传进抽屉，行不渲染，「跳到 task-wire」找不到。

### 步骤 4.2：最小实现（转绿）

4-A. 改 `web/src/app/cards/CardsPage.tsx` 四处。

import 区追加两行（放在既有的 `usePoll` 导入附近）：

```tsx
import { useNavigate } from 'react-router-dom'
```

```tsx
import { useTasks } from '../data/useTasks'
```

组件体内、三个 `usePoll` 之后加：

```tsx
  const navigate = useNavigate()
  // 任务实况走页面级那条 2.5s 流（useTasks），抽屉只吃结果、不自起轮询：
  // 同页两条流会各自跳动，卡上与看板会在不同时刻更新（spec §5）。首拉未回
  // 时给 undefined，抽屉按「计数不可知」显示旧标题，不谎报「0 个在跑」。
  const tasksPoll = useTasks()
```

`return (` 之前（`newCardWorkflows` 之后）加跳转回调：

```tsx
  // 卡到任务的唯一出口是 /tasks/:id 深链：目录解析、开 TUI tab、跨机全由
  // Shell 既有的 TaskDeepLink 完成，这里绝不顺手做目录切换（spec §3.3 明令
  // 禁止复制那套逻辑）。跳转即离开 /cards 是接受的代价（spec §3.3 已弃选回退机制）。
  const jumpToTask = (taskId: string) => {
    console.debug('[cards] 从卡跳转任务深链', taskId)
    navigate(`/tasks/${taskId}`)
  }
```

`CardDrawer` 渲染行追加两个 props（整行替换）：

```tsx
      {selected && <CardDrawer id={selected} onClose={closeDrawer} onOpenCard={(id) => openDrawer(id)} workflowStates={workflowStates} initialSection={drawerFocus} nodes={drawerNodes} tasks={tasksPoll.data ?? undefined} onJumpToTask={jumpToTask} />}
```

4-B. 改 `web/src/app/shell/Shell.tsx` 仅注释：TaskDeepLink 的文档头（现 `:697-701`）末尾补两行，其余零改动：

```tsx
// B181 起 /cards 抽屉里每行任务的 ↗ 也落到这条深链——它是本路由的第二个消费者；
// 目录解析/开 TUI tab 的行为以这里为唯一实现，消费方不得复制。
```

### 步骤 4.3：验证并提交

```bash
cd web
npm run test -- src/app/cards/CardsPage.test.tsx src/app/cards/CardDrawer.test.tsx src/app/shell/Shell.test.tsx
# 预期：3 个文件全绿（Shell.test 一并跑是因为 Shell.tsx 被触及，哪怕只是注释）
npm run typecheck && npm run lint   # typecheck 无输出；lint 0 errors、warning 不多于 17
```

测试范围声明：只跑上述三个文件 + typecheck + lint。全量门在收尾验收统一跑。

提交：`git add web/src/app/cards/CardsPage.tsx web/src/app/cards/CardsPage.test.tsx web/src/app/shell/Shell.tsx && git commit -m "feat(cards): 卡页接入任务流与任务深链跳转通路"`

---

## 收尾验收（review/acceptance 节点照单执行）

### A. spec §6 判据表逐条落点（表格原文承接）

| 判据 | 落点 |
|---|---|
| 给定任务流里该 task 为 running，行上渲染 running 状态而非 LastType | Task 1 用例「行上显示任务流的真实 state…」 |
| **关键回归**：任务流 state=running 但最后一条事件是 turn_failed 时，行上显示 running | Task 1 用例「关键回归…」（变异 M1 的靶子） |
| 任务流里关联不上时显示「实况未知」并列出 LastType，不显示任何状态 | Task 1 用例「任务已不在任务流里…」 |
| 运行中的行排在已结束的行前面 | Task 2 用例「运行中的行排在前面…」 |
| 区块标题的在跑计数与任务流一致 | Task 2 用例「区块标题的在跑计数…」 |
| 点 ↗ 调用 navigate('/tasks/{id}')，且**不**触发展开 | Task 3 用例「点 ↗ 发起跳转回调…」＋ Task 4 管线用例（端到端穿过 CardsPage） |
| 点整行仍然展开工单面板（现状不回归） | Task 3 用例「整行点击仍然展开工单面板…」 |

### B. 变异复验（验收时必做两条，spec §6 原文承接）

- **M1** 把状态来源改回 LastType：把 TaskState 分支临时换成 `<span className="ml-auto text-muted-foreground">{row.LastType}</span>` → Task 1 关键回归用例必须转红；还原后转绿。
- **M2** 把 ↗ onClick 里的 `event.stopPropagation()` 注释掉 → Task 3 用例的 `aria-expanded === 'false'` 断言必须转红；还原后转绿。

### C. 全量门

```bash
cd web
npm run test       # 全绿（基线 116 文件/1108 用例 + 新增 ≥11 条）
npm run typecheck  # 无输出
npm run lint       # 0 errors；warnings 不多于基线 17
cd .. && go build ./...   # Go 侧零改动的旁证，应原样通过
```

### D. 真机走查清单（对应 spec §4 用户故事，逐条目击）

1. 打开一张有运行中任务的卡：标题写「N 个在跑」，最上面一行就是活的；
2. 该行的状态点颜色语义与看板一致（running=实心绿，无需心算翻译）；
3. 点行尾 ↗：控制台切到该任务所在目录并打开它的 TUI tab；
4. 任务跑在远程开发机上时同样点得开（目录解析带机器维度）;
5. 多任务卡的已结束任务仍在列表下部可见（审计线索不被藏掉）；
6. 找一个任务已被清出任务流的卡：那行诚实显示「实况未知 · 最后事件 …」。

---

## 四项检查结论（出稿自审，结论入档）

1. **缺陷族对抗**：状态混淆族→关键回归用例+变异 M1；数据缺失族→undefined（流未接入）/[]（查无此任务）/正常关联三态显式建模，各有专测；排序退化族→ES2019 stable sort 平局保序（代码注释声明），LastSeq 全异用例钉主序；DOM 嵌套/冒泡族→button 不嵌 button 照 CardItem 先例绕开，变异 M2 对抗冒泡回归；路由上下文缺失族→CardsPage 测试显式包 MemoryRouter 并注明原因；双流跳动族→「抽屉禁自起轮询」列为 review 必查红线（CardDrawer diff 里不得出现任何 fetch/usePoll）。
2. **序列化边界**：无新增 wire 字段。join 两侧形状各有既有契约测试钉住（`web/src/api/contract.test.ts:111-128` 断言 CardDetail 键集含 `task_states`；`web/src/api/client.test.ts:42-74` 钉 fetchTasks 信封拆包）；跨组件 props 管线有 Task 4 管线用例真实穿过（MemoryRouter+探针路由）；「字段缺失 vs 零值」以 undefined/[] 两态区分且有专测。
3. **上下文预算**：每 task 触及文件 ≤3 且全部预先圈定（§3 总览表），无竖切需求。
4. **类型标注**：本卡非边界型子系统；行为验收=vitest 断言清单（A 表）+ 真机走查清单（D 表）。

## 自审三查

1. **spec 覆盖**：§3.1→Task 1；§3.2→Task 2；§3.3→Task 3/4；§5 四条实现决定分别落于 Task 1（StateDot/stateLabel 复用）、Task 2（isTerminalState 复用）、Task 3（navigate 单一动作）、Task 4（数据流单一来源+不自起轮询）；§6 七判据与两变异→收尾验收 A/B 全部指到具体用例；§7 Out of Scope 未越界（无后端改动、无看板角标、无抽屉内停止/继续、无跳转返回栈）；§8 备注→Task 4 步骤 4-B。
2. **占位符扫描**：全文无 TBD／「同 Task N」／描述性占位；所有测试均给出完整可粘贴代码，未动用纪律块的「指认既有 harness」例外条款（夹具风格虽承自 CardDrawer.test.tsx 的 card()，仍给出了全文）。
3. **跨 task 签名一致性**：唯一接口面=§2 Produces 两行；Task 1 引入 `tasks?: Task[]`、Task 3 引入 `onJumpToTask?: (taskId: string) => void` 与之逐字一致；Task 4 消费处 `tasks={tasksPoll.data ?? undefined}`、`onJumpToTask={jumpToTask}`（`(taskId: string) => void`）对齐；无其他生产者/消费者。

## 派发前自审

本计划不含任何需要驱动派发系统自身的验收步骤——所有命令均为仓库内测试/构建命令，可整体安全派发给 implement 执行者。



