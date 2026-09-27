# B412 plan：看板合并视图按工作流当前版本看板配置归列

> 卡 B412 · 入口节点 charter:plan · spec `docs/superpowers/specs/b412.md`（已批准，2026-09-27，L2）
> 基线分支 `cards/B412-spec`（本工作树分支名 `cards/B412-charter`，HEAD `0eafd398` 与
> `origin/cards/B412-spec` 同点；未做任何合并）。
> 台账 `docs/superpowers/ledgers/2026-09-27-b412-plan-ledger.md`（含亲跑命令与原始输出）。
> **本节点只出计划，不写实现**。「未验证」= 本节点没亲自跑到结果，实现卡必须自己复核。
> 读者假设：对 handoff 仓零上下文的执行者。凡引用行号者动手前重核，漂了以符号为准。

---

## 0. 待拍板清单：无阻塞岔口

spec 的 D1/D2/D3 已由用户/协调者冻结（`spec:b412.md:18-23`），本计划只落实读法。唯一需要在出稿时
钉死的读法如下，它直接从 spec 推出、不作为岔口上报：

| 读法 | 依据 | 落点 |
|---|---|---|
| **呈现面用「流当前版本」的 board，动作面用「卡钉版本」** | spec D1 推论明写「呈现（看板列归属、节点标签显形、抽屉列胶囊）随流当前版本；动作（可派发节点）随卡钉版本」`spec:b412.md:20,39` | 看板归列、卡上 `nodeTag` 的显形判定、抽屉列胶囊都改用 `cardLayoutResolver`（当前版本）；抽屉 `nodes`（可派发节点集）与转移态仍走卡钉版本 |
| **合并视图列集合恒默认五列；单流视图用该流自身列名** | D2 `spec:b412.md:21`；实现决定 `spec:b412.md:38` | `displayedColumns`：合并视图 = `DEFAULT_BOARD_COLUMNS`；单流视图 = `boardColumns(workflowStates, boardLayout)`（现状不变） |
| **合并视图内某流映射目标不在默认五列 ⇒ 落「进行中」** | D3 `spec:b412.md:23` | 新增 `mergeIntoDefaultColumns`：列固定默认五列、越界目标改「进行中」、fallback「进行中」 |

---

## 1. 问题与现状（证据驱动；原始读数见台账 §3/§4/§5）

### R1（根因）：合并视图把整个「每流 board 解析」跳过了

`web/src/app/cards/CardsPage.tsx:153-157` 现状：

```ts
const boardLayout = useMemo(
  () => normalizeBoardLayout(workflow ? selectedWorkflow?.def.board : undefined, workflowStates),
  [selectedWorkflow, workflow, workflowStates],
)
```

顶部「工作流」筛选器为「全部工作流」时 `workflow === ''`，三元取 `undefined` 传给
`normalizeBoardLayout`。`columns.ts:24-36` 落 `undefined` 后回退 `defaultBoardLayout(states)`，
而 `columns.ts:14-22` 的映射只认 7 个老中文状态（待办/已出spec/已出 spec/进行中/待审阅/待合并/
已完成/终止），其余状态一律落 fallback「进行中」。

可见后果（charter 当前版本 v12 的 board：spec→沟通中、review/acceptance/integrate/图对账→审核中、
finish→结束、待办→代办）：看板把 spec/contract/breakdown/plan/implement/review/acceptance/
integrate/图对账/finish 全堆进「进行中」，「沟通中」「审核中」恒空。
（spec `b412.md:12`；本节点用同形探针实跑确认，台账 §4。）

### R2：改动的全部输入现成，不需要新请求

- 卡的 `workflow`（`web/src/api/ledger.ts:13`）与 `status`（`:10`）在列表响应里就有。
- `fetchFlows()` 返回的 `FlowsResp.workflows[].def`（`web/src/api/ledger.ts:227-231`）**已带
  `board`**：后端 `handleFlows` 在 `def.Board == nil` 时补 `DefaultBoardLayout`（`internal/agentd/ledgerapi.go:612-615`），
  flow 详情同理（`:661-665`）。故 spec「不新增请求」成立。

### R3：`cardsInColumn` 现在只能吃一份 layout，挡住逐卡解析

`columns.ts:56-59` 对全部卡用同一个 layout 归列；`columns.ts:116-119` 的 `visibleColumns`
把 layout 透传给 `cardsInColumn`。要支持「同一视图内不同流的卡各按其流映射落列」，必须让归列
入口接受**逐卡解析器**。这是 spec 缝 2 的承重点。

### R4：卡片节点标签与抽屉列胶囊目前用卡钉版本，与 spec 呈现面口径不符

- `CardsPage.tsx:314` 卡上 `nodeTag`：`cardBoard = detail ? normalizeBoardLayout(detail.board, …) : boardLayout`
  —— 卡钉版本优先（detail 来自 `fetchFlow(card.workflow, card.workflow_version)`）。
- `CardsPage.tsx:316` 抽屉 `boardLayout`/`workflowStates`：`selectedPinnedWorkflow` 优先。
- spec D1 推论要求呈现面随当前版本（消除「同一张卡在看板与抽屉列不一致」，`b412.md:20,30,39`）。

### R5：后端约束与兜底（不动）

- `validateBoardLayout` 强制恰好 5 列、列名非空唯一、fallback 与映射目标都在列序内
  （`internal/ledger/workflows.go:38-60`）；flows 列表/详情在 `Board == nil` 时补默认（R2）。
- 本卡不改后端、不发新 wire 字段（spec `b412.md:36,54`）。

---

## 2. 分流决定

| 事项 | 归属 | 处置 |
|---|---|---|
| 合并视图逐卡按流当前版本 board 归列 | 本卡 / `web/src/app/cards/columns.ts`（纯逻辑） | **T2**：`mergeIntoDefaultColumns` + `mergedLayoutFor` + `CardLayoutResolver` |
| `cardsInColumn`/`visibleColumns` 支持逐卡解析 | 本卡 / `columns.ts` | **T2**：第三参兼容 `BoardLayout \| CardLayoutResolver` |
| 页面组合：当前版本解析器、列集合、`nodeTag`、抽屉胶囊 | 本卡 / `CardsPage.tsx` | **T3**：接线 + 告警日志 |
| 后端五列校验 / flows 补默认 board | —— | **零改动**（R5） |
| 列名/列序编辑入口 | 不在本卡 | spec Out of Scope（`b412.md:55`） |
| 真机验证 | 协调者执行 | 见 §11；本 task 由协调者执行，不派发 |

---

## 3. 任务 DAG

```
T1（红锚·缝3）：CardsPage 组件级用例落仓——「全部工作流」下 mock charter v12 board + 多状态卡，
    断言各卡落配置列、列恒五列、抽屉胶囊与看板一致。基线可编译、跑起来红。
      └→ T2（实现·缝1/2）：columns.ts 纯逻辑层（mergeIntoDefaultColumns / mergedLayoutFor /
            CardLayoutResolver）+ columns.test.ts 单测（多流夹具）。T1 仍红。
            └→ T3（实现·缝3）：CardsPage 接线（解析器、列集合、nodeTag、抽屉 boardLayout、
                  告警日志）；T1 转绿。
                  └→ T4（收口）：不误伤回归 + 变异复验 + typecheck/lint。
```

- **最薄路径条**：T1 是 spec 缝 3 的最薄可跑路径，**今天编译得过且会红**（合并视图 `boardLayout =
  undefined` ⇒ `defaultBoardLayout` ⇒ spec 卡落「进行中」；本节点探针已实跑确认，台账 §4）。
  红线只走既有生产符号（`CardsPage` + mock API），不引用本卡新符号，故红可当场看见。
- **次序承重**：T1 的红必须先出现，才能证明 T2+T3 是它转绿的原因；T4 的变异复验依赖实现在场。
- T2 的单测引用新增符号（`mergeIntoDefaultColumns` 等），**基线不可编译故无红**——缝 1/2 的
  红证据由 T1 之外无须重复，T2 单测是纯逻辑层的附加缝级锁（此点在 §12 显式声明）。

---

## 4. 基线事实（实现卡共享，动手前复核；原始输出见台账）

**本节点亲跑（`web/`，HEAD `0eafd398`）**：

- `npm ci` → `added 290 packages, and audited 291 packages in 3s`（`node_modules` 原本缺失）。
- `npx vitest run src/app/cards` → `Test Files 10 passed (10)` / `Tests 103 passed (103)`。
- `npx tsc -b` → `TSC_EXIT=0`。
- 现状探针（同形临时文件，跑完已删）：
  `cardsInColumn([spec 卡], '沟通中', undefined)` → `[]`（期望 `['Bs']`）；
  `normalizeBoardLayout(undefined, ['spec']).state_to_column['spec']` → `'进行中'`（期望 `'沟通中'`）。

**库/框架行为事实（带出处）**：

- `flows` 列表响应已带 `def.board`：`internal/agentd/ledgerapi.go:612-615`；详情 `:661-665`。
- 后端 board 约束恰好 5 列、fallback/映射目标在列序内：`internal/ledger/workflows.go:38-60`。
- 前端测试栈：vitest 4.1.10 + jsdom + @testing-library/react（`web/vite.config.ts` 的 `test` 段、
  `web/src/test/setup.ts` 显式 `cleanup`）；`web/package.json` scripts：`test`=`vitest run`、
  `typecheck`=`tsc -b`、`lint`=`eslint .`。

**现状签名与调用面（codegraph + grep 复核）**：

- `cardsInColumn(cards: CardView[], column: string, layout?: BoardLayout): CardView[]`
  — `web/src/app/cards/columns.ts:56`。调用方：`visibleColumns`（同文件 `:118`）、`CardsPage.tsx:314`、
  `columns.test.ts:28,84,87`。
- `normalizeBoardLayout(layout, states)` — `columns.ts:24`；`boardColumns(states, layout?)` — `:52`；
  `boardColumnFor(status, layout)` — `:38`；`nodeLabelFor(status, nodes, layout)` — `:45`。
- `CardsPage({ onOpenCoordinatorTerminal })` — `CardsPage.tsx:82`。
- `CardsPage.tsx:145-157`：`selectedWorkflow`/`workflowStates`/`boardLayout`/`displayedColumns`。
- `CardDrawer` props：`workflowStates?: string[]`、`boardLayout?: BoardLayout`（`CardDrawer.tsx:277-288`）；
  抽屉胶囊用 `boardColumns(states, resolvedBoardLayout)` 渲染（`:654`），`detail` 加载后才渲染（`:641`）。
- 既有测试夹具：`CardView` 桩 `card(over)`（`columns.test.ts:5-10`）；`CardsPage.test.tsx` 的
  `renderPage(entry)`（`:41-50`）与 `vi.mock('../../api/ledger', …)`（`:16-26`），`fetchCardDetail`
  需显式 mock 否则抽屉不渲染 capsule（`CardDrawer.tsx:641`）。

---

## 5. 接口契约（执行者只看本 task，故这里列全）

### Consumes（既有签名，一字不改）

```ts
// web/src/api/ledger.ts
export interface CardView { id: string; title: string; status: string; priority: string; project: string;
  workflow: string; workflow_version?: number; /* … */ following: string }
export interface BoardLayout { columns: string[]; state_to_column: Record<string, string>; fallback: string }
export interface WorkflowWire { name: string; version: number;
  def: { states: string[]; gates?: Record<string, unknown>; nodes?: NodeDef[]; board?: BoardLayout } }

// web/src/app/cards/columns.ts（既有导出）
export const DEFAULT_BOARD_COLUMNS: string[]
export function defaultBoardLayout(states: string[]): BoardLayout
export function normalizeBoardLayout(layout: BoardLayout | undefined, states: string[]): BoardLayout
export function boardColumnFor(status: string, layout: BoardLayout): string
export function nodeLabelFor(status: string, nodes: readonly string[], layout: BoardLayout): string | undefined
export function boardColumns(states: string[], layout?: BoardLayout): string[]
export function cardsInColumn(cards: CardView[], column: string, layout?: BoardLayout): CardView[]
export function visibleColumns(columns: string[], cards: CardView[], collapseEmpty: boolean, layout?: BoardLayout): string[]
```

### Produces（本卡新增，跨 task 逐字对齐）

```ts
// web/src/app/cards/columns.ts（T2 新增）
export function mergeIntoDefaultColumns(layout: BoardLayout): BoardLayout
export type WorkflowBoardLookup = (workflow: string) => BoardLayout | undefined
export function mergedLayoutFor(card: Pick<CardView, 'workflow' | 'status'>, lookup: WorkflowBoardLookup): BoardLayout
export type CardLayoutResolver = (card: CardView) => BoardLayout
// cardsInColumn / visibleColumns 第三/第四参类型扩为：BoardLayout | CardLayoutResolver
```

> **序列化边界**：本卡**不新增任何数据字段**、不改 DTO/json tag、不改 wire、不新增命令。
> `BoardLayout`/`CardView`/`FlowsResp` 的 wire 形状逐字节不动；前端只在内存里按卡换用不同 layout。
> 故无新增手写序列化/投影点（见 §7 追加设问一）。

---

## 6. 任务详情

### T1 红锚（缝 3）：合并视图按各流当前版本归列的组件级用例

**文件**：`web/src/app/cards/CardsPage.test.tsx`（在文件末尾追加一个 `describe`；不新建文件）。

**动作**：把下面整段追加到 `CardsPage.test.tsx` 末尾。它只用既有生产符号（`CardsPage`）与既有
mock 基座，**不引用本卡新符号**，故基线可编译、跑起来红。

```tsx
describe('B412 合并视图按各流当前版本看板配置归列', () => {
  const charterCard = (over: Record<string, unknown> = {}) => ({
    id: 'B1', title: '卡', status: '待办', priority: '中', project: 'p', workflow: 'charter',
    parent: '', base_branch: '', attachments: [], following: '', blocked: false, blocked_by: [],
    merged_count: 0, needs: '', open_decisions: 0, children_total: 0, children_done: 0,
    conflict: false, open_tickets: 0, ...over,
  })
  const charterFlows = {
    workflows: [{
      name: 'charter', version: 12,
      def: {
        states: ['待办', 'spec', 'plan', 'implement', 'review', 'acceptance', 'integrate', '图对账', 'finish'],
        board: {
          columns: ['代办', '沟通中', '进行中', '审核中', '结束'],
          state_to_column: {
            spec: '沟通中', review: '审核中', acceptance: '审核中', integrate: '审核中',
            图对账: '审核中', finish: '结束', 待办: '代办', plan: '进行中', implement: '进行中',
          },
          fallback: '进行中',
        },
      },
    }],
    templates: [],
  }
  const columnSection = (name: string): HTMLElement => {
    const section = screen.getByText(name).closest('section')
    if (!section) throw new Error(`找不到看板列 ${name}`)
    return section
  }

  it('全部工作流视图按各流当前版本 board 归列、列恒五列，抽屉列胶囊与看板一致', async () => {
    const ledger = await import('../../api/ledger')
    // 卡钉 v9 → 会拉 v9 的节点集（动作面）；board 归属仍看当前 v12。这里给一份可解析的 v9 详情，
    // 避免 fetchFlow 解析到 undefined 触发未处理拒绝。
    vi.mocked(ledger.fetchFlow).mockResolvedValue({
      name: 'charter', version: 9,
      states: ['待办', 'spec', 'plan', 'implement', 'review', 'acceptance', 'integrate', '图对账', 'finish'],
      nodes: ['待办', 'spec', 'plan', 'implement', 'review', 'acceptance', 'integrate', '图对账', 'finish']
        .map((name) => ({ name })),
    })
    vi.mocked(ledger.fetchFlows).mockResolvedValue(charterFlows)
    // spec 卡钉 v9（v9 无 board）——D1：列归属看当前 v12，不看钉版本。
    vi.mocked(ledger.fetchCards).mockResolvedValue({
      cards: [
        charterCard({ id: 'Bs', title: 'spec 卡', status: 'spec', workflow_version: 9 }),
        charterCard({ id: 'Br', title: 'review 卡', status: 'review', workflow_version: 12 }),
        charterCard({ id: 'Bf', title: 'finish 卡', status: 'finish', workflow_version: 12 }),
        charterCard({ id: 'Bp', title: 'plan 卡', status: 'plan', workflow_version: 12 }),
      ],
      unlinked: { count: 0, tasks: [], unknown_targets: [] },
    })
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ...charterCard({ id: 'Bs', title: 'spec 卡', status: 'spec' }), workflow_version: 9, acceptance_criteria: '', created_at: '', updated_at: '' },
      relations: [], events: [], task_states: [], effective_base_branch: '', decisions: [], needs: '',
    })

    renderPage()
    expect(await screen.findByText('spec 卡')).toBeInTheDocument()
    expect(within(columnSection('沟通中')).getByText('spec 卡')).toBeInTheDocument()
    expect(within(columnSection('审核中')).getByText('review 卡')).toBeInTheDocument()
    expect(within(columnSection('结束')).getByText('finish 卡')).toBeInTheDocument()
    expect(within(columnSection('进行中')).getByText('plan 卡')).toBeInTheDocument()

    // 抽屉列胶囊与看板一致：点开 spec 卡 → 「沟通中」高亮。
    fireEvent.click(screen.getByText('spec 卡'))
    const drawer = await screen.findByRole('dialog', { name: '工作项详情' })
    expect(within(drawer).getByText('沟通中').className).toContain('bg-primary')
  })
})
```

- **Interfaces**
  - Consumes：`CardsPage`、`renderPage`、既有 mock 基座（`fetchCards`/`fetchFlows`/`fetchFlow`/`fetchCardDetail`）。
  - Produces：本 describe 一个用例；不新增生产符号。
- **步骤**
  1. 追加该 describe。
  2. 跑红（**实现卡的第一步**）：
     `cd web && npx vitest run src/app/cards/CardsPage.test.tsx -t 'B412'`
     → 预期 FAIL，报错在 `within(columnSection('沟通中')).getByText('spec 卡')`（spec 卡此刻在「进行中」列）。
     把原文抄进实现台账。
- **测试范围声明**：只跑 `web/src/app/cards/CardsPage.test.tsx`（`-t 'B412'` 收窄到本用例）。
- **加关键节点日志**：本 task 只加测试，无生产日志。
- **加注释**：describe 内已写 why（v9 无 board 的卡仍按 v12 归列 = D1；抽屉胶囊 = 用户故事 4）。

### T2 实现（缝 1/2）：`columns.ts` 纯逻辑层

**文件**：`web/src/app/cards/columns.ts`（只改此文件）+ `web/src/app/cards/columns.test.ts`（追加）。

#### 改动一：新增三个导出（插在 `normalizeBoardLayout` 之后、`boardColumnFor` 之前）

```ts
// mergeIntoDefaultColumns 把某条流的布局收敛到固定默认五列（合并视图，spec D2/D3）。
//
// 参数：layout 是某条流当前版本的看板布局（调用方负责先 normalize）。
// 返回：列集合恒为 DEFAULT_BOARD_COLUMNS；映射目标不在默认五列的状态落「进行中」；
//       未映射状态走 fallback「进行中」。
// 注意：只在「全部工作流」合并视图用；单流视图保留该流自身列名（含自定义），不调本函数。
export function mergeIntoDefaultColumns(layout: BoardLayout): BoardLayout {
  const state_to_column: Record<string, string> = {}
  for (const [state, column] of Object.entries(layout.state_to_column)) {
    state_to_column[state] = DEFAULT_BOARD_COLUMNS.includes(column) ? column : '进行中'
  }
  return { columns: [...DEFAULT_BOARD_COLUMNS], state_to_column, fallback: '进行中' }
}

// WorkflowBoardLookup 按工作流名取该流**当前版本**的看板布局；未知流返回 undefined。
export type WorkflowBoardLookup = (workflow: string) => BoardLayout | undefined

// mergedLayoutFor 是合并视图下单张卡的呈现布局（spec D1/D3）。
//
// 参数：card 只读其 workflow 与 status；lookup 是流当前版本 board 的解析器（flows 列表已带）。
// 返回：该流布局收敛到默认五列；流未知（flows 未加载完/已删改名）时退回默认映射——
//       与现状同形的诚实兜底，不新增请求。
export function mergedLayoutFor(
  card: Pick<CardView, 'workflow' | 'status'>,
  lookup: WorkflowBoardLookup,
): BoardLayout {
  return mergeIntoDefaultColumns(lookup(card.workflow) ?? defaultBoardLayout([card.status]))
}
```

#### 改动二：`cardsInColumn` 接受逐卡解析器（改前 → 改后）

改前（`columns.ts:56-59`）：

```ts
export function cardsInColumn(cards: CardView[], column: string, layout?: BoardLayout): CardView[] {
  const resolved = normalizeBoardLayout(layout, cards.map((card) => card.status))
  return cards.filter((card) => boardColumnFor(card.status, resolved) === column && !card.following)
}
```

改后：

```ts
// CardLayoutResolver 给一张卡解析它的呈现布局：合并视图逐卡按流解析，单流视图所有卡共用一份。
export type CardLayoutResolver = (card: CardView) => BoardLayout

// asLayoutResolver 兼容两种 layout 入参形态：既有调用点传 BoardLayout（或 undefined）零改动；
// 合并视图传 CardLayoutResolver 逐卡解析。BoardLayout 是对象、resolver 是函数，用 typeof 区分。
function asLayoutResolver(
  layout: BoardLayout | CardLayoutResolver | undefined,
  states: string[],
): CardLayoutResolver {
  if (typeof layout === 'function') return layout
  const resolved = normalizeBoardLayout(layout, states)
  return () => resolved
}

export function cardsInColumn(cards: CardView[], column: string, layout?: BoardLayout | CardLayoutResolver): CardView[] {
  const resolve = asLayoutResolver(layout, cards.map((card) => card.status))
  return cards.filter((card) => boardColumnFor(card.status, resolve(card)) === column && !card.following)
}
```

#### 改动三：`visibleColumns` 第三参类型同步（只改签名）

改前（`columns.ts:116`）：

```ts
export function visibleColumns(columns: string[], cards: CardView[], collapseEmpty: boolean, layout?: BoardLayout): string[] {
```

改后：

```ts
export function visibleColumns(columns: string[], cards: CardView[], collapseEmpty: boolean, layout?: BoardLayout | CardLayoutResolver): string[] {
```

（函数体不动，仍把 `layout` 透传给 `cardsInColumn`。）

#### 改动四：`columns.test.ts` 追加单测

import 行（第 3 行）改为：

```ts
import { boardColumnFor, boardColumns, cardsInColumn, DEFAULT_BOARD_COLUMNS, defaultBoardLayout, filterNeeds, mergeIntoDefaultColumns, mergeStateOrder, mergedLayoutFor, needsAttention, nodeLabelFor, normalizeBoardLayout, visibleColumns } from './columns'
import type { BoardLayout, CardLayoutResolver } from './columns'
```

文件末尾追加：

```ts
describe('B412 合并视图按各流当前版本 board 归列', () => {
  const charterBoard: BoardLayout = {
    columns: ['代办', '沟通中', '进行中', '审核中', '结束'],
    state_to_column: { spec: '沟通中', review: '审核中', finish: '结束', 待办: '代办', plan: '进行中', implement: '进行中' },
    fallback: '进行中',
  }
  const customBoard: BoardLayout = {
    columns: ['收集', '沟通', '实现', '验收', '完成'],
    state_to_column: { spec: '收集' }, fallback: '实现',
  }

  it('按卡的流当前版本 board 解析列，列集合恒默认五列', () => {
    const layout = mergedLayoutFor(card({ workflow: 'charter', status: 'spec' }),
      (name) => (name === 'charter' ? charterBoard : undefined))
    expect(boardColumnFor('spec', layout)).toBe('沟通中')
    expect(layout.columns).toEqual(DEFAULT_BOARD_COLUMNS)
  })

  it('映射目标不在默认五列的状态落「进行中」（D3）', () => {
    const layout = mergedLayoutFor(card({ workflow: 'x', status: 'spec' }), () => customBoard)
    expect(layout.columns).toEqual(DEFAULT_BOARD_COLUMNS)
    expect(boardColumnFor('spec', layout)).toBe('进行中')
    expect(mergeIntoDefaultColumns(customBoard).columns).toEqual(DEFAULT_BOARD_COLUMNS)
  })

  it('流未知（flows 未加载/已删改名）退回默认映射，不抛错', () => {
    const layout = mergedLayoutFor(card({ workflow: 'gone', status: '待审阅' }), () => undefined)
    expect(boardColumnFor('待审阅', layout)).toBe('审核中')
    expect(layout.columns).toEqual(DEFAULT_BOARD_COLUMNS)
  })

  it('合并视图列集合恒默认五列，单流视图列集合用该流自身列名', () => {
    expect(mergedLayoutFor(card({ workflow: 'x', status: '待办' }), () => customBoard).columns)
      .toEqual(DEFAULT_BOARD_COLUMNS)
    expect(normalizeBoardLayout(customBoard, ['待办']).columns).toEqual(customBoard.columns)
  })

  it('同一视图内不同流的卡各按其流映射落列（多流夹具）；被并卡不成列', () => {
    const resolver: CardLayoutResolver = (item) => mergedLayoutFor(
      item, (name) => (name === 'charter' ? charterBoard : undefined))
    const cards = [
      card({ id: 'Bs', workflow: 'charter', status: 'spec' }),
      card({ id: 'Br', workflow: 'charter', status: 'review' }),
      card({ id: 'Bp', workflow: 'charter', status: 'plan' }),
      card({ id: 'Bx', workflow: 'other', status: 'spec' }),
      card({ id: 'Bf', workflow: 'charter', status: 'spec', following: 'Bs' }),
    ]
    expect(cardsInColumn(cards, '沟通中', resolver).map((item) => item.id)).toEqual(['Bs'])
    expect(cardsInColumn(cards, '审核中', resolver).map((item) => item.id)).toEqual(['Br'])
    expect(cardsInColumn(cards, '进行中', resolver).map((item) => item.id)).toEqual(['Bp', 'Bx'])
  })
})
```

- **Interfaces**
  - Consumes：§5 Consumes 全部既有符号 + `columns.test.ts:5-10` 的 `card(over)` 夹具。
  - Produces：§5 Produces 四条。
- **步骤**
  1. 按改动一~三改 `columns.ts`。
  2. 按改动四改 `columns.test.ts`。
  3. 跑绿：`cd web && npx vitest run src/app/cards/columns.test.ts` → 预期全 PASS。
  4. 静态检查：`cd web && npx tsc -b` → 预期 `TSC_EXIT=0`。
- **测试范围声明**：只跑 `web/src/app/cards/columns.test.ts`（本 task 只触及该纯逻辑模块与它的单测）。
- **加关键节点日志**：**无（显式声明）**。`columns.ts` 文件头即声明它是纯逻辑契约模块（无 IO、无副作用），
  对纯函数记日志会把模块拖进运行时噪声；本卡的可观测性由 T3 在页面层承担（`cards.flows.loaded`、
  `cards.board.workflow.missing`），不在纯逻辑层重复。
- **加注释**：三个新导出各带「参数/返回/为什么」（为什么合并视图要收敛五列、为什么要诚实兜底、
  为什么兼容两种 layout 入参），文件头职责不变。

### T3 实现（缝 3）：`CardsPage.tsx` 接线

**文件**：`web/src/app/cards/CardsPage.tsx`（只改此文件）。

#### 改动五：import（第 15 行）

改前：

```ts
import { boardColumns, cardsInColumn, filterNeeds, mergeStateOrder, needsAttention, nodeLabelFor, normalizeBoardLayout, visibleColumns } from './columns'
```

改后：

```ts
import { boardColumns, cardsInColumn, DEFAULT_BOARD_COLUMNS, filterNeeds, mergeStateOrder, mergedLayoutFor, needsAttention, nodeLabelFor, normalizeBoardLayout, visibleColumns } from './columns'
import type { CardLayoutResolver, WorkflowBoardLookup } from './columns'
```

#### 改动六：`fetchFlows` 成功路径加日志（改 `132-136` 的 effect）

改前：

```ts
    void fetchFlows().then((result) => { if (!cancelled) setFlows(result) }).catch((err: unknown) => { if (!cancelled) setFlowsError(errorMessage(err)) })
```

改后：

```ts
    void fetchFlows()
      .then((result) => {
        if (cancelled) return
        setFlows(result)
        console.info('cards.flows.loaded', {
          workflows: result.workflows.length,
          withBoard: result.workflows.filter((flow) => flow.def.board).length,
        })
      })
      .catch((err: unknown) => { if (!cancelled) setFlowsError(errorMessage(err)) })
```

#### 改动七：插入当前版本解析器与列集合（替换 `153-157` 的 `boardLayout`/`displayedColumns` 两块）

改前（`153-157`）：

```ts
  const boardLayout = useMemo(
    () => normalizeBoardLayout(workflow ? selectedWorkflow?.def.board : undefined, workflowStates),
    [selectedWorkflow, workflow, workflowStates],
  )
  const displayedColumns = useMemo(() => boardColumns(workflowStates, boardLayout), [boardLayout, workflowStates])
```

改后：

```ts
  const boardLayout = useMemo(
    () => normalizeBoardLayout(workflow ? selectedWorkflow?.def.board : undefined, workflowStates),
    [selectedWorkflow, workflow, workflowStates],
  )
  // 呈现布局按流**当前版本**的 board 解析（D1）：合并视图逐卡解析，单流视图所有卡用选中流。
  const layoutForWorkflow = useMemo<WorkflowBoardLookup>(
    () => (name) => {
      const flow = flows?.workflows.find((item) => item.name === name)
      return flow ? normalizeBoardLayout(flow.def.board, flow.def.states) : undefined
    },
    [flows],
  )
  const cardLayoutResolver = useMemo<CardLayoutResolver>(
    () => (workflow ? () => boardLayout : (card) => mergedLayoutFor(card, layoutForWorkflow)),
    [boardLayout, layoutForWorkflow, workflow],
  )
  // 合并视图列集合恒默认五列（D2）；单流视图沿用该流自身列名（现状不变）。
  const displayedColumns = useMemo(
    () => (workflow ? boardColumns(workflowStates, boardLayout) : [...DEFAULT_BOARD_COLUMNS]),
    [boardLayout, workflow, workflowStates],
  )
  // 卡挂了 flows 里没有的流（已删/改名）时留一条可查告警；归列仍按默认映射诚实回落。
  const missingWorkflows = useMemo(() => {
    if (!flows) return []
    const known = new Set(flows.workflows.map((flow) => flow.name))
    return [...new Set(cards.map((card) => card.workflow).filter((name) => name !== '' && !known.has(name)))]
  }, [cards, flows])
  useEffect(() => {
    if (missingWorkflows.length > 0) console.warn('cards.board.workflow.missing', { workflows: missingWorkflows })
  }, [missingWorkflows])
```

> `boardLayout` 保留原义（单流视图当前布局）；`selectedWorkflow` 仍是第 145 行的既有 memo。

#### 改动八：看板渲染改用逐卡解析器（第 314 行）

改前（行内关键片段）：

```tsx
{visibleColumns(displayedColumns, filtered, needsOnly, boardLayout).map((column) => { const inColumn = cardsInColumn(filtered, column, boardLayout); …
  {inColumn.map((card) => { const pinned = pinnedWorkflowKey(card); const detail = pinned ? pinnedWorkflows[pinned] : undefined; const cardStates = detail?.states ?? []; const cardBoard = detail ? normalizeBoardLayout(detail.board, cardStates) : boardLayout; const cardNodes = detail?.nodes?.map((node) => node.name) ?? []; return <CardItem … nodeTag={detail ? nodeLabelFor(card.status, cardNodes, cardBoard) : undefined} … />
```

改后（整行替换为下面这行；其余结构不变）：

```tsx
      {cardsPoll.data === null ? <p className="p-4 text-sm text-muted-foreground">正在读取账本…</p> : view === 'list' ? <ListView cards={filtered} includeArchived={includeArchived} onIncludeArchivedChange={setIncludeArchived} onOpen={(id) => openDrawer(id)} /> : <div className="flex min-h-0 flex-1 gap-2 overflow-x-auto px-4 py-3">{visibleColumns(displayedColumns, filtered, needsOnly, cardLayoutResolver).map((column) => { const inColumn = cardsInColumn(filtered, column, cardLayoutResolver); return <section key={column} className="flex min-h-0 w-60 shrink-0 flex-col"><header className="flex items-center gap-1.5 px-1 pb-2 text-xs font-semibold"><span>{column}</span><span className="font-normal text-muted-foreground">{inColumn.length}</span></header><div className="min-h-0 flex-1 space-y-2 overflow-y-auto pb-2">{inColumn.map((card) => { const pinned = pinnedWorkflowKey(card); const detail = pinned ? pinnedWorkflows[pinned] : undefined; const cardLayout = cardLayoutResolver(card); const cardNodes = detail?.nodes?.map((node) => node.name) ?? []; return <CardItem key={card.id} card={card} queuePosition={queuePositions.get(card.id)} nodeTag={detail ? nodeLabelFor(card.status, cardNodes, cardLayout) : undefined} onOpen={(focus) => openDrawer(card.id, focus)} onMigrate={() => setMigrateCardId(card.id)} /> })}{inColumn.length === 0 && <p className="px-1 py-2 text-xs text-muted-foreground">（空）</p>}</div></section> })}</div>}
```

> 变化点：`visibleColumns`/`cardsInColumn` 的 `boardLayout` → `cardLayoutResolver`；删除
> `cardStates`/`cardBoard`，改 `const cardLayout = cardLayoutResolver(card)`；`nodeTag` 用 `cardLayout`。
> `cardNodes` 仍取卡钉版本 `detail.nodes`（动作面/节点集不随呈现面改，B392 纪律不破）。

#### 改动九：抽屉列胶囊改用当前版本呈现布局（第 316 行）

改前（关键片段）：

```tsx
boardLayout={selectedPinnedWorkflow ? normalizeBoardLayout(selectedPinnedWorkflow.board, selectedPinnedWorkflow.states) : selectedWorkflowVersion !== undefined && selectedWorkflowVersion > 0 ? boardLayout : undefined}
```

改后（整行替换；`workflowStates` 与 `nodes` 保持卡钉版本优先不变）：

```tsx
      {selected && <CardDrawer id={selected} onClose={closeDrawer} onOpenCard={(id) => openDrawer(id)} workflowStates={selectedPinnedWorkflow?.states ?? (selectedWorkflowVersion !== undefined && selectedWorkflowVersion > 0 ? workflowStates : undefined)} boardLayout={selectedCard ? cardLayoutResolver(selectedCard) : undefined} initialSection={drawerFocus} nodes={drawerNodes} tasks={tasksPoll.data ?? undefined} onJumpToTask={jumpToTask} onOpenCoordinatorTerminal={onOpenCoordinatorTerminal} />}
```

> `selectedCard` 是第 187 行既有 memo（`selected ? cards.find(…) : undefined`）。抽屉里
> `resolvedBoardLayout = normalizeBoardLayout(boardLayout, states)`（`CardDrawer.tsx:366`），
> `boardColumns(states, resolvedBoardLayout)` 取 `boardLayout.columns`——合并视图 = 默认五列、
> 单流视图 = 该流列名，与看板一致（用户故事 4）。`normalizeBoardLayout` 在本文件仍被
> `boardLayout`（改动七）与 `layoutForWorkflow` 使用，import 不删。

- **Interfaces**
  - Consumes：`mergedLayoutFor`、`DEFAULT_BOARD_COLUMNS`、`CardLayoutResolver`、`WorkflowBoardLookup`
    （T2 产出，签名见 §5）；`CardDrawer` 既有 props。
  - Produces：无新导出（页面组合）。
- **步骤**
  1. 按改动五~九改 `CardsPage.tsx`。
  2. 跑绿：`cd web && npx vitest run src/app/cards/CardsPage.test.tsx -t 'B412'` → T1 预期 PASS。
  3. 跑本目录全量：`cd web && npx vitest run src/app/cards` → 预期全 PASS（T1 的 `fetchFlows` 覆盖
     在本用例内自洽；既有 103 条不得回归）。
  4. 静态检查：`cd web && npx tsc -b` → `TSC_EXIT=0`；`cd web && npm run lint` → 预期无新增 error。
- **测试范围声明**：只跑 `web/src/app/cards`（页面接线只影响本目录）。
- **加关键节点日志**：`fetchFlows` 成功记 `cards.flows.loaded`（带 `workflows`/`withBoard` 计数，
  成功路径不静默）；卡挂了未知流记 `cards.board.workflow.missing`（Warn，去重后的流名列表）。
  失败路径沿用既有 `setFlowsError` 错误横幅（`CardsPage.tsx:311`），不新增吞错。
- **加注释**：解析器/列集合/告警三处「为什么」已写全（D1 呈现随当前版本、D2 合并固定五列、
  兜底不静默且归列仍诚实回落）。

### T4 收口：不误伤回归 + 变异复验

**不误伤回归（跑既有，不新增）**：

```text
cd web && npx vitest run src/app/cards
cd web && npx tsc -b
cd web && npm run lint
```

本节点已在基线实跑 `npx vitest run src/app/cards`（103 passed）与 `tsc -b`（exit 0）；
实现卡在改动后必须复跑到结果并抄原文进台账（本计划不替它写结论）。

**变异复验（手动，不留代码）**：

1. 把 T3 改动七的 `cardLayoutResolver` 合并视图分支临时改成 `() => boardLayout`
   （即退回「合并视图用默认布局」）→ `cd web && npx vitest run src/app/cards/CardsPage.test.tsx -t 'B412'`
   → **预期重新红**（与 T1 基线红同形）；撤回临改。
2. 把 T2 的 `mergeIntoDefaultColumns` 临时改成 `return { ...layout }`（不收敛五列、不越界兜底）
   → `cd web && npx vitest run src/app/cards/columns.test.ts`
   → **预期红**（`映射目标不在默认五列的状态落「进行中」` 与 `列集合恒默认五列` 两条灭）；撤回临改。
3. 确认工作树只剩正式改动（`git status`）。

- **测试范围声明**：`web/src/app/cards`（页面 + 纯逻辑 + 类型）。

---

## 7. 缺陷族对抗审查（逐族设问）

**族 1 生命周期/状态机中断**
改动是渲染期纯计算（每次 render 由 `useMemo` 重算解析器/列集合），无异步、无 goroutine、无锁、
无文件句柄。新增的 `useEffect` 只做一次条件 `console.warn`。无半状态风险。

**族 2 静默失败 / 误导报错**
- flows 加载成功不再静默（`cards.flows.loaded`），失败沿用既有 `setFlowsError` 横幅。
- 卡挂未知流不再静默：`cards.board.workflow.missing` Warn；且归列并不静默丢失——按默认映射
  诚实落列（用户故事 6「任何卡都落在某一列」）。
- 反例保护：`mergedLayoutFor` 对未知流**不抛错**、不返回空列，恒返回合法五列布局（T2 单测锁定）。

**族 3 跨平台假设**
纯前端 DOM/TS，无平台分支；无新增平台假设。

**族 4 假红 / 假绿测试**
- T1 走真实 `CardsPage` 渲染 + 真实生产归列路径，只 mock API，不直调 `mergedLayoutFor`；
  v9 卡 fixture 让「钉版本」与「当前版本」的列结果分岔，能证伪「按钉版本落列」的错实现。
- 假绿防护：`列集合恒默认五列`、`越界落进行中`、`流未知回落`、`多流夹具`、`被并卡不成列`
  五条并列；删掉收敛会红（T4 变异 2），退回默认布局会红（T4 变异 1）。
- T2 单测断言 `layout.columns` 与逐卡列归属，不是只断言函数被调用。

**族 5 门禁绕过**
本卡不改后端、不新增命令、不放宽任何校验；前端只换内存 layout。无旁路。

**追加设问一：序列化边界**
不新增数据字段、不改 DTO/json tag/wire、不新增命令。`BoardLayout` 从 `def.board` 读入即用，
不产生新的手写投影点。**无新增序列化边界**。

**追加设问二：枚举新值过既有白名单**
无新增枚举/状态值。**无风险**。

**追加设问三：承重安全属性有测试锁住**
承重属性 = 「合并视图按卡的流当前版本 board 归列、列恒默认五列、越界落进行中、未知流诚实回落」，
由 T2 五条单测 + T1 组件用例 + T4 两处变异锁定。

**残余风险（明示）**：
- 卡的 `status` 不在其流当前版本的 `state_to_column` 里时，落 merged 布局 fallback「进行中」——
  这是 D3 的兜底语义，不是缺陷。
- flows 未回时首帧按默认映射归列，flows 到达后重渲染纠正——与现状同形的诚实降级，spec
  `b412.md:37` 已定。

---

## 8. 上下文预算检查

有界文件集：`web/src/app/cards/columns.ts`、`web/src/app/cards/columns.test.ts`、
`web/src/app/cards/CardsPage.tsx`、`web/src/app/cards/CardsPage.test.tsx`、
`docs/superpowers/plans/b412-plan.md`、`docs/superpowers/ledgers/2026-09-27-b412-plan-ledger.md`（本节点）。
不越出 `web/src/app/cards`（`CardDrawer`/`CardItem` 零改动）。**通过**。

## 9. 类型标注 / 边界型子系统

非 wire 边界：纯前端内存计算，无跨进程/跨语言契约。边界由 `BoardLayout` 的 5 列约束表达，
真机行为验收见 §11（本 task 由协调者执行，不派发）。

## 10. 接缝覆盖（双向，对照 spec `b412.md:42-48` 接缝清单）

spec 的缝：① `columns.ts` 列归属解析（导出符号）；② `columns.ts#cardsInColumn`；
③ `CardsPage`（组件级）。

- **测试 → 缝**：
  - 缝①：T2 单测入口 = `mergedLayoutFor` / `mergeIntoDefaultColumns`（均为 `columns.ts` 导出符号）
    与 `boardColumnFor`/`normalizeBoardLayout`，逐条落在缝①；
  - 缝②：T2 单测 `同一视图内不同流的卡各按其流映射落列` 入口 = `cardsInColumn`（缝②本体）；
  - 缝③：T1 入口 = `renderPage()` 渲染 `CardsPage`（缝③本体）。
- **缝 → 测试**：
  - 缝①被 T2 的 `按卡的流当前版本 board 解析列`、`越界落进行中`、`流未知回落`、
    `合并视图列集合恒默认五列/单流用自身列名` 四条锁住；
  - 缝②被 T2 多流夹具用例锁住（不同流各落其列）+ 既有「被并卡不在看板成列」保持；
  - 缝③被 T1 组件用例锁住（多状态卡归列 + 抽屉胶囊与看板一致）。
- **内部锁（附加，不顶替）**：无。本卡全部测试入口都在三条缝的入口符号上
  （`mergedLayoutFor`/`mergeIntoDefaultColumns`/`cardsInColumn` 是缝①②声明符号；`CardsPage` 是缝③）。
  `asLayoutResolver` 未导出、不在任何测试入口上，是纯内部实现。
- **条件退路**：无（T4 的两个变异是显式复验步骤，不改任何测试的入口符号）。

## 11. 真机清单（归协调者执行；本 task 由协调者执行，不派发）

1. 打开 `/cards`，顶部「工作流」保持「全部工作流」：charter 的 spec 卡在**沟通中**，
   review/acceptance/integrate/图对账 卡在**审核中**，finish 卡在**结束**，待办卡在**代办**，
   plan/implement/contract/breakdown 卡在**进行中**（spec 用户故事 1）。
2. 看板列恒为 代办/沟通中/进行中/审核中/结束 五列，不随各流配置增减（用户故事 5）。
3. 卡总数不因改动丢失：任何卡都落在某一列（用户故事 6）。
4. 点开一张 spec 卡：抽屉列胶囊里**沟通中**高亮，与看板一致（用户故事 4）。
5. 改某条流的看板列映射并保存出新版本：该流的卡立即按新映射归列，不需逐卡迁移（用户故事 2）。
6. 顶部选中单条流：仍显示该流自己的列名与映射（用户故事 3，现状不变）。

> 需要跑有 T2+T3 改动的 web；机内夹具验不了真机账本里 charter v12 的配置，故单列交协调者。

## 12. 占位符扫描

- 无 TBD / 「加适当的错误处理」/「同 Task N 而略」；T1 的测试块与 T2/T3 的改动块均完整可抄。
- **例外声明（无）**：不依赖「形态因包而异」的夹具复用——复用既有 `card(over)`（`columns.test.ts:5-10`）
  与 `renderPage`（`CardsPage.test.tsx:41-50`），签名已逐字核过。
- **红基线声明**：T1 是唯一红锚（基线可编译、会红，台账 §4 同形探针已实跑）；T2 单测引用新增符号，
  基线不可编译故无红——缝 1/2 的红证据由 T1 承担。
- **内部锁声明**：无（§10）。
- 条件退路：无。

## 13. 自审三查

1. **spec 覆盖（逐条指到 task）**：
   - 问题陈述（合并视图不读配置、全堆进行中）→ T1 红锚 + T2 归列；
   - D1（落列用流当前版本）→ T2 `mergedLayoutFor` + T3 `cardLayoutResolver` + T1 的 v9 卡 fixture；
   - D2（合并视图固定默认五列）→ T2 `mergeIntoDefaultColumns` + T3 `displayedColumns`；
   - D3（越界兜底进行中）→ T2 `mergeIntoDefaultColumns` + 单测；
   - 用户故事 1/2/4/5/6 → T1/T2 + §11 真机；故事 3（单流视图不变）→ T3 保留 `boardColumns(workflowStates, boardLayout)`；
   - 实现决定「不新增请求」（flows 列表已带 board）→ T3 `layoutForWorkflow` 只读 `flows`；
   - 呈现面随当前版本（节点标签显形、抽屉胶囊）→ T3 改动八/九；
   - 动作面保持卡钉版本 → T3 保留 `drawerNodes`（pinned）与抽屉 `workflowStates`（pinned 优先）；
   - 测试决定接缝 1/2/3 → §10 三条缝全覆盖；
   - Out of Scope（不做列名并集/后端字段/列名编辑/列表列呈现/迁移提示）→ §2 确认不做。
2. **占位符扫描**：见 §12，无占位。
3. **跨 task 类型/签名一致性**：
   - `WorkflowBoardLookup` 定义于 T2（`(workflow: string) => BoardLayout | undefined`），
     T3 的 `layoutForWorkflow` 用 `useMemo<WorkflowBoardLookup>`，逐字一致；
   - `CardLayoutResolver` 定义于 T2（`(card: CardView) => BoardLayout`），T3 的 `cardLayoutResolver`
     用 `useMemo<CardLayoutResolver>`，并作为 `cardsInColumn`/`visibleColumns` 与 `CardDrawer.boardLayout`
     的实参；
   - `mergedLayoutFor(card, lookup)` 的 `card` 形参是 `Pick<CardView,'workflow'|'status'>`，T3 传完整
     `CardView`（赋值兼容）；
   - `mergeIntoDefaultColumns` 返回完整 `BoardLayout`，满足 `CardDrawer.boardLayout?: BoardLayout`。

## 图覆盖债（本节点偏差记录）

- `codegraph --repo . sym CardsPage / cardsInColumn / normalizeBoardLayout` 均命中 `d_web_cards`；
  `context d_web_cards` 返回领域声明与包成员（`web/src/app/cards`、`web/src/app/flows`），
  `actual.misplaced` 为空。
- **未命中项**：`who-calls cardsInColumn/boardColumns/CardsPage` 只回焦点本身（无调用边），
  `flow` 返回 `degraded=true, missing="基线没有 flows 段"`——即 baseline 图对 `d_web_cards` 的 TS
  函数**没有 call/flow 边**。故调用面改用 grep 直读源码复核（台账 §5）。本卡引用的既有符号无
  「图未命中」；缺的是边不是节点，记为本域图覆盖债。

> 未验证项：T1 的红锚已实跑确认（台账 §4）；T2/T3 的绿与 T4 的回归（本节点不写实现，未跑）；
> §11 真机清单未跑（归协调者）。
