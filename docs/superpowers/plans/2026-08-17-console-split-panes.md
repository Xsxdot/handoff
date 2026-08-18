# 控制台分屏（三栏封顶 / 可拖拽 / ⌘D）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把控制台中央工作区的分屏上限从两栏提到三栏、比例可拖拽、并补上 ⌘D 快捷键与「到顶了」的可见反馈。

**Architecture:** 全部改动在前端 `web/src/app/`，不动后端与 API。分栏的宽度进 `tabs.ts` 的纯函数模型（新增 `sizes: number[]`），拖拽交互是一个新的 `GroupDivider` 组件——它自己量父容器宽度、把像素位移换算成无量纲比例交给纯函数，于是纯函数测试不需要 mock 布局。⌘D 是 Shell 上的 window 级监听。

**Tech Stack:** React 19 + TypeScript + Vite + Tailwind v4 + shadcn/ui；测试 Vitest + @testing-library/react（jsdom）。

**Spec:** `docs/superpowers/specs/2026-08-17-console-split-panes-design.md`

## Global Constraints

- **`MAX_GROUPS = 3`**，`MIN_PANE_PX = 240`。两个都是 `tabs.ts` 导出的常量，不许在别处写字面量。
- **不变式 `sizes.length === groups.length` 恒成立**，任何改动组数的纯函数都要维持它。
- **组数一变，`sizes` 一律重置为等分**（每项都是 `1`）。不做「从当前栏借一半」。
- **新组 push 到数组末尾**，不做「当前栏右侧插入」（spec §1.2：会让 `Shell` 里存下的 group 下标失效）。
- **⌘D 只认 `metaKey`，绝不接受 `ctrlKey`**。Ctrl+D 在终端里是 EOF，绑上去等于毁掉终端。这与 `BlankTab.tsx:44-48` 已确立的口径一致。
- **不持久化**：`sizes` 跟 tab 组一样只活在内存里，刷新即丢。
- **日志与注释纪律**（instrumenting-code 在本项目前端的落法）：
  - 前端没有结构化 logger，既有约定是**只在「降级/不该发生」的路径上 `console.warn` 带上下文**（见 `usePtyRestore.ts:79`、`useMachineCaps.ts:58`）。照这个来。
  - **不许**给正常路径加 `console.log` —— 用户每拖一次分隔条刷几十行日志是噪音，不是可观测性。
  - 本轮唯一该 warn 的点：`resizeGroups` 收到越界的 `dividerIndex`，或 `sizes` 与 `groups` 长度对不上 —— 那是不变式被破坏，必须留下痕迹。
  - 新文件必须有文件头注释（职责 + 边界）；导出函数必须有说明参数/返回/坑的注释；非显然的分支要写「为什么」。既有文件的注释风格（大段中文讲理由）照抄。
- 每个 Task 结束前跑一遍 `npx tsc -b` 与 `npx eslint`，不许留新的 error。

---

### Task 1: `tabs.ts` 模型层——三栏上限、sizes、拖拽纯函数

**Files:**
- Modify: `web/src/app/workbench/tabs.ts`
- Test: `web/src/app/workbench/tabs.test.ts`

**Interfaces:**
- Consumes: 无（本 Task 是最底层）
- Produces:
  - `export const MAX_GROUPS = 3`
  - `export const MIN_PANE_PX = 240`
  - `interface Workbench` 新增 `sizes: number[]`
  - `export function resizeGroups(wb: Workbench, dividerIndex: number, delta: number, minRatio: number): Workbench`
  - `splitGroup(wb: Workbench): Workbench` 签名不变，行为改为三栏封顶 + 重置 sizes
  - `closeTab(wb, group, tabId)` 签名不变，行为改为焦点接替相邻组 + 重置 sizes
  - `EMPTY_WORKBENCH` 变为 `{ groups: [{ tabs: [], activeId: null }], active: 0, sizes: [1] }`

- [ ] **Step 1: 写失败的测试——三栏封顶**

改 `web/src/app/workbench/tabs.test.ts` 里 `describe('splitGroup')` 整块（现在只有一条「已经两组时是空操作」，上限变了，那条断言必须跟着改）：

```ts
describe('splitGroup', () => {
  it('连续分屏到三组封顶，第四次是空操作', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    wb = splitGroup(wb)
    expect(wb.groups).toHaveLength(2)
    expect(wb.active).toBe(1)
    wb = splitGroup(wb)
    expect(wb.groups).toHaveLength(3)
    expect(wb.active).toBe(2)
    // 到顶时原样返回同一个对象引用：调用方据此可以跳过一次无谓的 setState
    const again = splitGroup(wb)
    expect(again).toBe(wb)
  })

  it('每次分屏后 sizes 与 groups 等长且等分', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    expect(wb.sizes).toEqual([1])
    wb = splitGroup(wb)
    expect(wb.sizes).toEqual([1, 1])
    wb = splitGroup(wb)
    expect(wb.sizes).toEqual([1, 1, 1])
  })
})
```

同文件顶部的 import 加上 `resizeGroups`：

```ts
import {
  EMPTY_WORKBENCH,
  activateTab,
  closeTab,
  dedupKey,
  nextTerminalSeq,
  openTab,
  resizeGroups,
  setTabContent,
  splitGroup,
  tabTitle,
} from './tabs'
```

> import 清单按文件现有内容对齐——只**补上** `resizeGroups`，不要删掉已有的任何一个。

- [ ] **Step 2: 跑它，确认失败**

Run: `cd web && npx vitest run src/app/workbench/tabs.test.ts`
Expected: FAIL —— `splitGroup` 第二次调用仍返回两组；`wb.sizes` 是 `undefined`；`resizeGroups` 不存在（import 报错）。

- [ ] **Step 3: 加常量与 `sizes` 字段**

`web/src/app/workbench/tabs.ts`，把 `Workbench` 那段整体换成：

```ts
// MAX_GROUPS 是中央区最多能分出的栏数。
//
// 为什么是 3 而不是「无限」：1280px 宽度下减去左树 260 与文件树 280，中央区
// 只剩 740px；三栏时每栏 ~245px，刚够放下一个 tab 标题加关闭按钮（这也是
// MIN_PANE_PX 的来源）。第四栏必然要靠横向滚动或折叠 tab 条才能存在，那是
// 另一个形态问题，不是把这个数字改大就能解决的。
export const MAX_GROUPS = 3

// MIN_PANE_PX 是单栏的最小宽度：拖拽时两侧都夹在它之上，不允许把一栏压成一条缝。
export const MIN_PANE_PX = 240

// Workbench 是一个基准目录下的全部 tab：一到 MAX_GROUPS 组，外加「哪一组是焦点」
// 与各组的宽度权重。
export interface Workbench {
  groups: TabGroup[]
  active: number
  // sizes 是各栏的宽度权重，与 groups **等长**（这条不变式所有改组数的函数都要维持），
  // 渲染时作为 flexGrow 用。
  //
  // 为什么是相对权重不是像素：容器宽度会随窗口大小、随左右两栏的显隐变化，存像素
  // 等于每次 resize 都要重算一遍，还要处理「加起来不等于容器宽」这种对不上的状态。
  sizes: number[]
}

export const EMPTY_WORKBENCH: Workbench = {
  groups: [{ tabs: [], activeId: null }],
  active: 0,
  sizes: [1],
}

// evenSizes 返回 n 等分的权重数组。
//
// 组数一变（分屏、关空一组）就调它重置，**不做「从当前栏借一半」**：借了之后
// 关掉该还给谁？还原主还是均摊？两种都能自圆其说，于是就有了第三种状态。
// 等分是唯一不需要记「这份宽度是从谁那儿来的」的规则。
function evenSizes(n: number): number[] {
  return Array.from({ length: n }, () => 1)
}
```

同文件的 `cloneWorkbench` 补上 `sizes`（它现在只拷到 groups 与 tabs 两层，漏了会让拖拽改到上一份状态上）：

```ts
function cloneWorkbench(wb: Workbench): Workbench {
  return {
    groups: wb.groups.map((g) => ({ tabs: [...g.tabs], activeId: g.activeId })),
    active: wb.active,
    sizes: [...wb.sizes],
  }
}
```

- [ ] **Step 4: 改 `splitGroup`**

```ts
// splitGroup 再开一栏；已经到 MAX_GROUPS 时**原样返回同一个对象**（调用方可据此
// 跳过一次无谓的 setState）。新栏为空并成为焦点，宽度重置为等分。
//
// 新栏 push 到末尾而不是插在当前栏右边：`(group 下标, tabId)` 是全代码库定位一个
// tab 的方式，而 Shell 的 closingPty / closingDirtyFile 在确认弹层打开期间把这个
// 下标**存进了 state**。紧邻插入会让后面所有组的下标 +1，于是弹层里存着的下标指向
// 别的组——点「确认关闭」关掉的是另一栏的 tab。见 spec §1.2。
export function splitGroup(wb: Workbench): Workbench {
  if (wb.groups.length >= MAX_GROUPS) return wb
  const next = cloneWorkbench(wb)
  next.groups.push({ tabs: [], activeId: null })
  next.active = next.groups.length - 1
  next.sizes = evenSizes(next.groups.length)
  return next
}
```

- [ ] **Step 5: 改 `closeTab` 的焦点接替与 sizes**

把 `closeTab` 里那段收尾换成：

```ts
  if (g.tabs.length === 0 && next.groups.length > 1) {
    next.groups.splice(gi, 1)
    // 焦点接替**相邻**组，不是写死回第 0 组。两组时 min(gi, 0) 恒等于 0，所以这个
    // 写死一直没暴露；三栏时它会让焦点从被关掉的最右栏莫名跳到最左边。
    next.active = Math.min(gi, next.groups.length - 1)
    next.sizes = evenSizes(next.groups.length)
  } else if (next.active >= next.groups.length) {
    next.active = next.groups.length - 1
  }
```

- [ ] **Step 6: 跑测试，确认上限与 sizes 两条过**

Run: `cd web && npx vitest run src/app/workbench/tabs.test.ts`
Expected: `splitGroup` 两条 PASS；`resizeGroups` 相关仍因函数不存在而 FAIL。

- [ ] **Step 7: 写 `resizeGroups` 的失败测试**

`tabs.test.ts` 末尾追加：

```ts
describe('resizeGroups', () => {
  // 两栏起手：sizes 是 [1, 1]，总和 2，各占一半
  const twoGroups = () => splitGroup(openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' }))

  it('分隔条右移，左栏变宽右栏变窄，权重总和不变', () => {
    const wb = resizeGroups(twoGroups(), 0, 0.1, 0.2)
    expect(wb.sizes[0]).toBeCloseTo(1.2)
    expect(wb.sizes[1]).toBeCloseTo(0.8)
    expect(wb.sizes[0] + wb.sizes[1]).toBeCloseTo(2)
  })

  it('拖过头时夹在 minRatio，不把一栏压成一条缝', () => {
    // 从各占 0.5 出发想推 0.9 过去，右栏会变成 -0.4——必须被夹回 0.2
    const wb = resizeGroups(twoGroups(), 0, 0.9, 0.2)
    const total = wb.sizes[0] + wb.sizes[1]
    expect(wb.sizes[1] / total).toBeCloseTo(0.2)
    expect(wb.sizes[0] / total).toBeCloseTo(0.8)
  })

  it('已经贴着下限还继续往同一方向拖，是空操作（返回同一个对象）', () => {
    const wb = resizeGroups(twoGroups(), 0, 0.9, 0.2)
    expect(resizeGroups(wb, 0, 0.5, 0.2)).toBe(wb)
  })

  it('容器窄到两栏都放不下 minRatio 时，拒绝改动而不是算出负宽度', () => {
    const wb = twoGroups()
    expect(resizeGroups(wb, 0, 0.1, 0.6)).toBe(wb)
  })

  it('越界的 dividerIndex 是空操作并留下一条 warn', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const wb = twoGroups()
    expect(resizeGroups(wb, 1, 0.1, 0.2)).toBe(wb)
    expect(warn).toHaveBeenCalled()
    warn.mockRestore()
  })
})
```

`tabs.test.ts` 顶部的 vitest import 要含 `vi`：

```ts
import { describe, expect, it, vi } from 'vitest'
```

- [ ] **Step 8: 跑它，确认失败**

Run: `cd web && npx vitest run src/app/workbench/tabs.test.ts -t resizeGroups`
Expected: FAIL —— `resizeGroups is not a function`。

- [ ] **Step 9: 实现 `resizeGroups`（含越界 warn）**

`tabs.ts` 里 `splitGroup` 之后追加：

```ts
// resizeGroups 把第 dividerIndex 个分隔条左右两栏的宽度重新分配。
//
// 参数：
//   - dividerIndex: 分隔条下标，左邻是 groups[dividerIndex]、右邻是 [dividerIndex+1]
//   - delta: 本次位移占容器宽度的**比例**，正数 = 分隔条右移（左栏变宽）
//   - minRatio: 单栏允许的最小占比，由调用方用 MIN_PANE_PX / 容器宽度算好传进来
//
// 为什么 minRatio 由调用方算：容器宽度只有 DOM 知道。纯函数拿到的是无量纲比例，
// 测试里直接给 0.2 就能断言夹紧行为，不用 mock 布局。
//
// 无事可做时**返回原对象**（引用相等），调用方可据此跳过一次 setState：拖拽是
// 高频事件，贴着下限还在拖时不该每帧都重渲染一次整个中央区。
export function resizeGroups(
  wb: Workbench,
  dividerIndex: number,
  delta: number,
  minRatio: number,
): Workbench {
  const i = dividerIndex
  const j = dividerIndex + 1
  if (i < 0 || j >= wb.sizes.length) {
    // 不变式被破坏才会走到这里（渲染层的分隔条数量恒等于 groups.length - 1）。
    // 静默返回会让「拖了没反应」查无对证，留一条带上下文的 warn
    console.warn('分隔条下标越界，本次拖拽忽略', { dividerIndex, groups: wb.groups.length, sizes: wb.sizes.length })
    return wb
  }
  const total = wb.sizes.reduce((a, b) => a + b, 0)
  const left = wb.sizes[i] / total
  const right = wb.sizes[j] / total
  const min = Math.max(0, minRatio)
  // 两栏加起来都容不下两个下限：容器已经窄到没有可分配的空间，此时任何夹紧规则
  // 都会算出自相矛盾的结果。拒绝改动，交给容器横向滚动（spec §2.3）
  if (min * 2 > left + right) return wb
  let d = delta
  if (left + d < min) d = min - left
  if (right - d < min) d = right - min
  if (d === 0) return wb
  const next = cloneWorkbench(wb)
  next.sizes[i] = (left + d) * total
  next.sizes[j] = (right - d) * total
  return next
}
```

- [ ] **Step 10: 补三栏关空一组的焦点测试**

`tabs.test.ts` 的 `describe('closeTab')`（或相同名字的那一块）里追加：

```ts
  it('三组时关空最右一组，焦点接替相邻组而不是跳回最左，sizes 重新等分', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    wb = splitGroup(wb)
    wb = openTab(wb, { kind: 'file', rel: 'b.go' }, 1)
    wb = splitGroup(wb)
    wb = openTab(wb, { kind: 'file', rel: 'c.go' }, 2)
    const cId = wb.groups[2].tabs[0].id

    wb = closeTab(wb, 2, cId)

    expect(wb.groups).toHaveLength(2)
    expect(wb.active).toBe(1)
    expect(wb.sizes).toEqual([1, 1])
  })
```

> 同一个 describe 里已有的「两组时关掉一组的最后一个 tab，该组消失，另一组占满」**不要动**：它断言 `active === 0`，在新规则下 `Math.min(1, 0)` 仍是 0，行为逐字节不变。留着正好锁住「Step 5 的改动不许波及两组的既有行为」。

- [ ] **Step 11: 跑整个 workbench 测试目录**

Run: `cd web && npx vitest run src/app/workbench`
Expected: 全绿。若 `useWorkbench.test.ts` / `WorkbenchPage.test.tsx` 因 `EMPTY_WORKBENCH` 多了 `sizes` 而报类型错，那是 Task 2 的事——**本 Task 只保证 `tabs.test.ts` 全绿且其余文件的运行时断言不变**。

- [ ] **Step 12: 类型与 lint**

Run: `cd web && npx tsc -b && npx eslint src/app/workbench/tabs.ts src/app/workbench/tabs.test.ts`
Expected: 0 error。

- [ ] **Step 13: 注释自检**

对照本 Task 已写入的内容逐条确认，缺哪条补哪条：
- `MAX_GROUPS` / `MIN_PANE_PX` 各有「为什么是这个值」的注释
- `Workbench.sizes` 写清了「等长不变式」与「为什么是权重不是像素」
- `evenSizes` 写清了「为什么不做借一半」
- `splitGroup` 写清了「为什么 push 到末尾而不是紧邻插入」
- `closeTab` 的焦点接替写清了「为什么原来写死 0 一直没暴露」
- `resizeGroups` 有完整的参数说明、「minRatio 为什么由调用方算」、「为什么返回原对象」

- [ ] **Step 14: Commit**

```bash
git add web/src/app/workbench/tabs.ts web/src/app/workbench/tabs.test.ts
git commit -m "feat(web): 分屏模型三栏封顶，新增各栏宽度权重与拖拽纯函数"
```

---

### Task 2: 拖拽分隔条——`GroupDivider` + `WorkbenchPage` 按权重渲染 + `useWorkbench.resize`

**Files:**
- Create: `web/src/app/workbench/GroupDivider.tsx`
- Modify: `web/src/app/workbench/useWorkbench.ts`
- Modify: `web/src/app/workbench/WorkbenchPage.tsx`
- Test: `web/src/app/workbench/WorkbenchPage.test.tsx`

**Interfaces:**
- Consumes（Task 1）：`MAX_GROUPS`、`MIN_PANE_PX`、`resizeGroups(wb, dividerIndex, delta, minRatio)`、`Workbench.sizes`
- Produces:
  - `GroupDivider({ onResize }: { onResize: (delta: number, containerWidth: number) => void })`
  - `WorkbenchApi` 新增 `resize: (dividerIndex: number, delta: number, minRatio: number) => void`

- [ ] **Step 1: 写失败的测试——分隔条数量与键盘调宽**

`web/src/app/workbench/WorkbenchPage.test.tsx`：先给 `api()` helper 补上 `resize`（不补的话新代码调 `api.resize` 会 undefined 崩）：

```ts
function api(overrides: Partial<WorkbenchApi> = {}): WorkbenchApi {
  return {
    base,
    wb: EMPTY_WORKBENCH,
    select: vi.fn(),
    open: vi.fn(),
    openTerminal: vi.fn(),
    close: vi.fn(),
    activate: vi.fn(),
    setContent: vi.fn(),
    split: vi.fn(),
    resize: vi.fn(),
    restoreTerminal: vi.fn(),
    ...overrides,
  }
}
```

文件顶部 import 补 `splitGroup`：

```ts
import { EMPTY_WORKBENCH, openTab, splitGroup } from './tabs'
```

追加一个 describe：

```ts
describe('分屏分隔条', () => {
  const twoGroups = () => splitGroup(openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' }))

  it('单组时没有分隔条', () => {
    render(<WorkbenchPage api={api()} onAddProject={vi.fn()} renderContent={() => null} />)
    expect(screen.queryByRole('separator')).not.toBeInTheDocument()
  })

  it('两组之间有一条分隔条，三组有两条', () => {
    const { unmount } = render(
      <WorkbenchPage api={api({ wb: twoGroups() })} onAddProject={vi.fn()} renderContent={() => null} />,
    )
    expect(screen.getAllByRole('separator')).toHaveLength(1)
    unmount()

    render(
      <WorkbenchPage api={api({ wb: splitGroup(twoGroups()) })} onAddProject={vi.fn()} renderContent={() => null} />,
    )
    expect(screen.getAllByRole('separator')).toHaveLength(2)
  })

  it('分隔条按 → 键调宽左栏，按 ← 键调窄', () => {
    const resize = vi.fn()
    render(
      <WorkbenchPage api={api({ wb: twoGroups(), resize })} onAddProject={vi.fn()} renderContent={() => null} />,
    )
    const sep = screen.getByRole('separator')

    fireEvent.keyDown(sep, { key: 'ArrowRight' })
    // jsdom 的 getBoundingClientRect 恒为 0，量不到容器宽度时 minRatio 传 0
    expect(resize).toHaveBeenLastCalledWith(0, 0.02, 0)

    fireEvent.keyDown(sep, { key: 'ArrowLeft' })
    expect(resize).toHaveBeenLastCalledWith(0, -0.02, 0)
  })

  it('各栏按 sizes 的权重铺开', () => {
    const wb = { ...twoGroups(), sizes: [3, 1] }
    render(<WorkbenchPage api={api({ wb })} onAddProject={vi.fn()} renderContent={() => null} />)
    const panes = screen.getAllByRole('tablist').map((tl) => tl.closest('section') as HTMLElement)
    expect(panes[0]).toHaveStyle({ flexGrow: '3' })
    expect(panes[1]).toHaveStyle({ flexGrow: '1' })
  })
})
```

- [ ] **Step 2: 跑它，确认失败**

Run: `cd web && npx vitest run src/app/workbench/WorkbenchPage.test.tsx -t 分屏分隔条`
Expected: FAIL —— 找不到 `separator` 角色（现在的分隔线只是 `gap-px bg-border` 的背景色）。

- [ ] **Step 3: 新建 `GroupDivider.tsx`**

```tsx
// GroupDivider —— 中央工作区两栏之间那条可拖拽的分隔条。
//
// 职责：
//   - 画出 1px 的分隔线（命中区放宽到 5px，1px 的把手鼠标够不着）
//   - 把鼠标拖拽与 ← → 键换算成「本次位移占容器宽度的比例」交给上层
//
// 边界：
//   - **不认识分栏模型**：不知道自己是第几条、两侧是谁、有没有到下限。它只报
//     「移动了多少」，夹紧与分配都在 tabs.ts 的 resizeGroups 里
//   - 不持有宽度状态：宽度的唯一真相在 Workbench.sizes
//
// 为什么量的是 parentElement 的宽度：分隔条自己只有 5px，换算比例要的是**容器**
// 宽度，而容器就是它和各栏共同的那个 flex 父节点。在事件里现量而不是存进 state：
// 窗口 resize、左右栏显隐都会改容器宽，存下来的值随时会过期。
import { useRef } from 'react'

// KEY_STEP 是键盘每次调整的比例。2% 在 740px 的中央区里约合 15px，
// 连按可达且不会一步跨过整栏。
const KEY_STEP = 0.02

export interface GroupDividerProps {
  // onResize 收到本次位移比例（正数 = 分隔条右移）与当前容器宽度（px）。
  // 容器宽度一并交出去，是因为「最小栏宽」是像素量、只有拿到宽度才能换成比例，
  // 而量宽度这件事只有这里有 DOM。量不到时给 0，由上层决定怎么退化。
  onResize: (delta: number, containerWidth: number) => void
}

export function GroupDivider({ onResize }: GroupDividerProps) {
  // 拖拽中的起点：上一次派发位置的 clientX 与容器宽度。null = 没在拖
  const drag = useRef<{ lastX: number; width: number } | null>(null)

  const containerWidthOf = (el: HTMLElement): number => el.parentElement?.getBoundingClientRect().width ?? 0

  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label="调整栏宽"
      tabIndex={0}
      className="w-[5px] shrink-0 cursor-col-resize bg-border hover:bg-primary/40 focus-visible:bg-primary/40 focus-visible:outline-none"
      onPointerDown={(e) => {
        e.preventDefault()
        e.currentTarget.setPointerCapture(e.pointerId)
        drag.current = { lastX: e.clientX, width: containerWidthOf(e.currentTarget) }
      }}
      onPointerMove={(e) => {
        const d = drag.current
        if (d === null) return
        // 派发**增量**而不是「相对起点的总位移」：增量在被 resizeGroups 夹住之后
        // 不会累积出一个看不见的欠账，往回拖立刻就有反应
        if (d.width > 0) onResize((e.clientX - d.lastX) / d.width, d.width)
        d.lastX = e.clientX
      }}
      onPointerUp={(e) => {
        e.currentTarget.releasePointerCapture(e.pointerId)
        drag.current = null
      }}
      onPointerCancel={() => {
        drag.current = null
      }}
      onKeyDown={(e) => {
        if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return
        e.preventDefault()
        onResize(e.key === 'ArrowRight' ? KEY_STEP : -KEY_STEP, containerWidthOf(e.currentTarget))
      }}
    />
  )
}
```

- [ ] **Step 4: `useWorkbench` 加 `resize`**

`web/src/app/workbench/useWorkbench.ts`：import 补 `resizeGroups`，`WorkbenchApi` 补一条，实现挂在 `mutate` 上。

interface 里，紧跟 `split` 之后：

```ts
  split: () => void
  // resize 调整第 dividerIndex 条分隔条两侧的栏宽。三个参数逐字透传给
  // tabs.ts 的 resizeGroups——这里不做夹紧也不认识像素，只负责把它接到当前基准的
  // Workbench 上。
  resize: (dividerIndex: number, delta: number, minRatio: number) => void
```

实现，紧跟 `const split = ...` 之后：

```ts
  const resize = useCallback(
    (dividerIndex: number, delta: number, minRatio: number) =>
      mutate((w) => resizeGroups(w, dividerIndex, delta, minRatio)),
    [mutate],
  )
```

返回值那行加上 `resize`：

```ts
  return { base, wb, select, open, openTerminal, close, activate, setContent, split, resize, restoreTerminal }
```

- [ ] **Step 5: `WorkbenchPage` 按权重渲染并插入分隔条**

`web/src/app/workbench/WorkbenchPage.tsx`：

import 补上（`nextTerminalSeq` 与 `TabContent` 已在，只加新的）：

```ts
import { MIN_PANE_PX, nextTerminalSeq, type TabContent } from './tabs'
import { GroupDivider } from './GroupDivider'
```

把 `return` 里最外层那个容器与 `map` 换成：

```tsx
  return (
    // gap 去掉了：分隔线不再靠 gap-px 透出背景色，而是 GroupDivider 这个真实元素——
    // 它要能被鼠标抓住、被键盘聚焦，那都不是背景色能做到的
    <div className="flex h-full min-h-0 bg-border">
      {wb.groups.map((g, gi) => {
        const activeTab = g.tabs.find((t) => t.id === g.activeId) ?? null
        return (
          <Fragment key={gi}>
            {gi > 0 && (
              <GroupDivider
                onResize={(delta, containerWidth) =>
                  // 最小栏宽是像素量，换成比例才能进纯函数。量不到宽度（jsdom、
                  // 尚未布局完成）时传 0：宁可这一次不夹紧，也不要因为除以 0 得到
                  // Infinity 而让拖拽整个失灵
                  api.resize(gi - 1, delta, containerWidth > 0 ? MIN_PANE_PX / containerWidth : 0)
                }
              />
            )}
            <section
              className="flex min-w-0 flex-col bg-background"
              // flexBasis 必须显式给 0：默认的 auto 会让内容宽度参与分配，
              // 于是 sizes 的权重被内容多少带偏，拖出来的比例对不上
              style={{ flexGrow: wb.sizes[gi] ?? 1, flexBasis: 0 }}
            >
              <TabBar
                group={gi}
                tabs={g.tabs}
                activeId={g.activeId}
                baseLabel={base.label}
                onActivate={api.activate}
                onClose={(g, id) => {
                  const tab = wb.groups[g]?.tabs.find((t) => t.id === id)
                  if (tab && onBeforeClose && !onBeforeClose(tab.content, g, id)) return
                  api.close(g, id)
                }}
                onNew={(g) => api.open({ kind: 'blank' }, undefined, g)}
              />
              {/*
                两处 BlankTab 的 key 必须区分开。它们在三元的相邻分支上，同类型同位置，
                React 默认会把「空组面板」原地复用成「空白 tab 面板」——DOM 节点不换，
                于是面板的「挂载即聚焦」不会重跑，点了 + 之后焦点还留在 + 按钮上，
                印在面板上的 ⌘T 按下去没反应（走查实测）。给出各自的身份，让它真的重挂。
              */}
              <div className="min-h-0 flex-1 overflow-auto">
                {activeTab === null ? (
                  <BlankTab
                    key={`empty-${gi}`}
                    base={base}
                    onPick={(k) => startFromEmpty(gi, k)}
                    terminalUnavailable={terminalUnavailable}
                  />
                ) : activeTab.content.kind === 'blank' ? (
                  <BlankTab
                    key={activeTab.id}
                    base={base}
                    onPick={(k) => pick(gi, activeTab.id, k)}
                    hint={awaiting[activeTab.id] ? PICK_HINT[awaiting[activeTab.id]] : undefined}
                    onBack={() => back(activeTab.id)}
                    terminalUnavailable={terminalUnavailable}
                  />
                ) : (
                  renderContent(activeTab.content, base, gi, activeTab.id)
                )}
              </div>
            </section>
          </Fragment>
        )
      })}
    </div>
  )
```

顶部 React import 加 `Fragment`：

```ts
import { Fragment, useState, type ReactNode } from 'react'
```

`WorkbenchPageProps` 上方的文件头注释里，「按当前基准目录渲染一组或两组 tab」改成「渲染一到三组 tab 与它们之间可拖拽的分隔条」。

- [ ] **Step 6: 跑测试**

Run: `cd web && npx vitest run src/app/workbench`
Expected: 全绿（含 Task 1 的 tabs 测试与本 Task 四条新测试）。

- [ ] **Step 7: `useWorkbench.test.ts` 补一条 resize 透传**

`web/src/app/workbench/useWorkbench.test.ts` 追加（沿用该文件既有的 `renderHook` / `act` 写法，不要新造 helper）：

```ts
  it('resize 只改当前基准的栏宽，切走再切回来比例还在', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.select(wsA))
    act(() => result.current.open({ kind: 'file', rel: 'a.go' }))
    act(() => result.current.split())
    act(() => result.current.resize(0, 0.1, 0.2))
    const widened = result.current.wb.sizes[0]
    expect(widened).toBeGreaterThan(result.current.wb.sizes[1])

    act(() => result.current.select(wsB))
    expect(result.current.wb.sizes).toEqual([1])

    act(() => result.current.select(wsA))
    expect(result.current.wb.sizes[0]).toBeCloseTo(widened)
  })
```

> `wsA` / `wsB` 是该文件顶部已有的 fixture（`useWorkbench.test.ts:5` 与 `:13`），直接用，不要新建。

- [ ] **Step 8: 跑测试 + 类型 + lint**

Run: `cd web && npx vitest run src/app/workbench && npx tsc -b && npx eslint src/app/workbench`
Expected: 全绿、0 error。

- [ ] **Step 9: 日志与注释自检**

- `GroupDivider.tsx` 有文件头注释（职责 + 边界，写明「不认识分栏模型」）
- `GroupDivider` 的 props 注释说清了「为什么把容器宽度一起交出去」
- 拖拽派发增量而非总位移，有「为什么」注释
- `WorkbenchPage` 的 `flexBasis: 0` 与 `containerWidth > 0` 两处都有「为什么」注释
- **确认没有给正常拖拽路径加任何 `console.log`**（每帧一行日志是噪音）

- [ ] **Step 10: Commit**

```bash
git add web/src/app/workbench/GroupDivider.tsx web/src/app/workbench/WorkbenchPage.tsx web/src/app/workbench/WorkbenchPage.test.tsx web/src/app/workbench/useWorkbench.ts web/src/app/workbench/useWorkbench.test.ts
git commit -m "feat(web): 分屏比例可拖拽，分隔条支持鼠标与键盘"
```

---

### Task 3: 分屏按钮到顶时置灰

**Files:**
- Modify: `web/src/app/shell/Breadcrumb.tsx`
- Modify: `web/src/app/shell/Shell.tsx:261`
- Test: `web/src/app/shell/Shell.test.tsx`

**Interfaces:**
- Consumes（Task 1）：`MAX_GROUPS`
- Produces：`Breadcrumb({ base, onSplit, canSplit }: { base: BaseDir; onSplit: () => void; canSplit: boolean })`

- [ ] **Step 1: 写失败的测试**

`web/src/app/shell/Shell.test.tsx`，在既有的「面包屑的分屏按钮把中央分成两组」之后追加：

```ts
  it('连点两次分屏得到三栏，按钮随即 disabled', async () => {
    renderShell()
    fireEvent.click(await screen.findByText('integration/b2-b3'))
    fireEvent.click(screen.getByRole('button', { name: '分屏' }))
    fireEvent.click(screen.getByRole('button', { name: '分屏' }))
    await waitFor(() => expect(screen.getAllByRole('tablist')).toHaveLength(3))
    expect(screen.getByRole('button', { name: '分屏' })).toBeDisabled()
  })
```

- [ ] **Step 2: 跑它，确认失败**

Run: `cd web && npx vitest run src/app/shell/Shell.test.tsx -t 三栏`
Expected: FAIL 在最后一行 `toBeDisabled` —— 三栏本身已经能分出来（Task 1 放开了上限），缺的只是按钮没有 disabled 属性。**前面的 `toHaveLength(3)` 应该是过的**；如果它也红，说明 Task 1 的上限没生效，回去查 Task 1 而不是在这里改。

- [ ] **Step 3: `Breadcrumb` 收 `canSplit`**

`web/src/app/shell/Breadcrumb.tsx`：

```tsx
export function Breadcrumb({
  base,
  onSplit,
  canSplit,
}: {
  base: BaseDir
  onSplit: () => void
  // canSplit=false 时按钮置灰。**不是**隐藏：按钮消失会让人以为分屏功能没了，
  // 置灰 + title 才回答了真正的问题「为什么点了没反应」——已经到顶了
  canSplit: boolean
}) {
```

按钮那段：

```tsx
      <button
        type="button"
        aria-label="分屏"
        title={canSplit ? '分屏（⌘D）' : `最多 ${MAX_GROUPS} 栏`}
        disabled={!canSplit}
        onClick={onSplit}
        className="ml-auto rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground disabled:pointer-events-none disabled:opacity-40"
      >
        <Columns2 className="size-4" />
      </button>
```

import 补：

```ts
import { MAX_GROUPS } from '../workbench/tabs'
```

文件头注释里「右侧分屏按钮」一行补一句：「到 `MAX_GROUPS` 栏时置灰而不是隐藏——见按钮上的注释」。

- [ ] **Step 4: `Shell` 传 `canSplit`**

`web/src/app/shell/Shell.tsx:261`：

```tsx
        {wb.base && <Breadcrumb base={wb.base} onSplit={wb.split} canSplit={wb.wb.groups.length < MAX_GROUPS} />}
```

import 补：

```ts
import { MAX_GROUPS } from '../workbench/tabs'
```

> `wb.wb` 这个写法看着别扭但是对的：`wb` 是 `WorkbenchApi`，`wb.wb` 是它里面的 `Workbench`。文件里既有代码就是这么用的（如 `WorkbenchPage` 的 `const { base, wb } = api`），不要为了好看去改命名。

- [ ] **Step 5: 跑测试**

Run: `cd web && npx vitest run src/app/shell`
Expected: 全绿，包含既有的「面包屑的分屏按钮把中央分成两组」。

- [ ] **Step 6: 类型 + lint**

Run: `cd web && npx tsc -b && npx eslint src/app/shell`
Expected: 0 error。

- [ ] **Step 7: 注释自检**

- `canSplit` 的 prop 注释写清了「为什么置灰而不是隐藏」
- `Breadcrumb` 文件头注释提到了到顶的行为

- [ ] **Step 8: Commit**

```bash
git add web/src/app/shell/Breadcrumb.tsx web/src/app/shell/Shell.tsx web/src/app/shell/Shell.test.tsx
git commit -m "feat(web): 分屏到三栏上限时按钮置灰，不再静默无效"
```

---

### Task 4: ⌘D 分屏

**Files:**
- Modify: `web/src/app/shell/Shell.tsx`
- Test: `web/src/app/shell/Shell.test.tsx`

**Interfaces:**
- Consumes（Task 1、Task 3）：`wb.split()`、`MAX_GROUPS`
- Produces：无新导出

- [ ] **Step 1: 写失败的测试**

`web/src/app/shell/Shell.test.tsx` 追加：

```ts
  it('⌘D 分屏，并拦掉浏览器的「加入书签」', async () => {
    renderShell()
    fireEvent.click(await screen.findByText('integration/b2-b3'))

    const ev = new KeyboardEvent('keydown', { key: 'd', metaKey: true, bubbles: true, cancelable: true })
    window.dispatchEvent(ev)

    await waitFor(() => expect(screen.getAllByRole('tablist')).toHaveLength(2))
    expect(ev.defaultPrevented).toBe(true)
  })

  it('Ctrl+D 不分屏：终端里那是 EOF，抢走会毁掉终端', async () => {
    renderShell()
    fireEvent.click(await screen.findByText('integration/b2-b3'))

    const ev = new KeyboardEvent('keydown', { key: 'd', ctrlKey: true, bubbles: true, cancelable: true })
    window.dispatchEvent(ev)

    await waitFor(() => expect(screen.getAllByRole('tablist')).toHaveLength(1))
    expect(ev.defaultPrevented).toBe(false)
  })
```

- [ ] **Step 2: 跑它，确认失败**

Run: `cd web && npx vitest run src/app/shell/Shell.test.tsx -t ⌘D`
Expected: FAIL —— 仍是一组 tablist（⌘D 没人监听）。

- [ ] **Step 3: 在 Shell 里挂 window 级监听**

`web/src/app/shell/Shell.tsx`，放在 `const dock = useHomeDock()` 之后、`usePtyRestore` 之前：

```tsx
  // ⌘D 分屏。
  //
  // 挂 window 而不是像 BlankTab 的 ⌘T 那样挂面板：那里必须区分「按的是哪一栏的
  // 空白面板」，window 级会让一次 ⌘T 开出两个终端（BlankTab.tsx:75）。⌘D 没有这个
  // 问题——它只作用于当前焦点组，全局唯一。
  //
  // **只认 metaKey，绝不接 ctrlKey**：Ctrl+D 在终端里是 EOF，绑上去等于让用户
  // 没法退出 shell。这与 BlankTab.tsx:44 已确立的口径一致（本控制台只在 macOS 用，
  // 将来上 Windows 时这两处要一起改，而且要另选一个不撞 EOF 的键）。
  //
  // 必须 preventDefault：macOS 浏览器的 ⌘D 是「加入书签」，不拦会在分屏的同时弹
  // 书签面板。不排除输入框——⌘D 在 input/textarea 里没有默认语义，排除它只会让
  // 「光标在 Composer 里时 ⌘D 不好使」变成一个要解释的例外。
  //
  // 冒泡阶段监听（第三参不传 true），与 ProjectTree 的 ⌘K 同一条让位次序。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!e.metaKey || e.ctrlKey || e.key.toLowerCase() !== 'd') return
      e.preventDefault()
      wb.split()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [wb])
```

> 到顶（已三栏）时 `wb.split()` 是空操作，⌘D 静默无效——快捷键没地方挂提示，弹 toast 属于反应过度。「到顶了」由 Task 3 的置灰按钮回答。

- [ ] **Step 4: 跑测试**

Run: `cd web && npx vitest run src/app/shell`
Expected: 全绿。

- [ ] **Step 5: 全量测试 + 类型 + lint + 构建**

Run: `cd web && npx vitest run && npx tsc -b && npx eslint src && npx vite build`
Expected: 全绿、0 error、构建通过。已有的 warning 数量不许增加。

- [ ] **Step 6: 真机走查**

在浏览器里对着真实控制台逐条确认（`handoff console` 拿一次性链接打开）：

1. 选一个目录 → 点分屏图标 → 两栏；再点 → 三栏；按钮置灰、hover 显示「最多 3 栏」
2. 按 ⌘D → 分屏，且**没有弹出浏览器书签框**
3. 拖两条分隔条 → 比例跟手；往一侧拖到底 → 停在约 240px 不再变窄
4. 分隔条按 Tab 能聚焦，← → 能调宽
5. 某一栏开一个终端 → 拖分隔条 → 终端内容跟着回流、不错行
6. 关掉最右一栏的最后一个 tab → 该栏消失、剩两栏等分，焦点落在**相邻**那栏而不是最左
7. 切到另一个目录再切回来 → 栏数与比例都还在；刷新页面 → 回到单栏（不持久化，符合预期）

- [ ] **Step 7: 注释自检（收口）**

- Shell 的 ⌘D 监听有完整注释：为什么 window 级、为什么只认 metaKey、为什么 preventDefault、为什么不排除输入框
- 全轮改动里没有正常路径的 `console.log`；唯一的 `console.warn` 在 `resizeGroups` 的越界分支且带上下文

- [ ] **Step 8: Commit**

```bash
git add web/src/app/shell/Shell.tsx web/src/app/shell/Shell.test.tsx
git commit -m "feat(web): ⌘D 分屏，只认 metaKey 以免抢走终端的 Ctrl+D"
```

---

## 收尾

四个 Task 完成后，对着 spec 逐条核一遍 §0 的判据表：

| # | 判据 | 由谁保证 |
|---|---|---|
| 1 | 连点三次分屏得到三栏；第四次按钮已 disabled | Task 1 Step 1、Task 3 Step 1 |
| 2 | 拖分隔条改变两侧宽度，任一侧不小于 240px | Task 1 Step 7、Task 2 Step 1 |
| 3 | ⌘D 分屏且不弹书签框 | Task 4 Step 1 |
| 4 | 三栏时按钮置灰、`title="最多 3 栏"` | Task 3 Step 1 |

spec §6「不做」里的五条一条都不许顺手做：挪图标、无限分屏、新栏预填内容、上下分屏 / 拖 tab 跨栏、比例持久化。
