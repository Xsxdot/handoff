# home 基准终端浮窗 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 home 基准的终端从中央工作区拆出来，跑在一个独立的、可拖可拉伸的浮窗里；右下角悬浮按钮改成「清单 + 新建」的小面板。

**Architecture:** 新增一套与 `useWorkbench` 完全独立的 home tab 状态（`useHomeDock`），一个浮窗容器（`HomeWindow`），一个入口面板（`HomeDock`，替换现有的 `FloatingNewPane`）。终端内容**复用现成的 `TerminalTab`**，一行都不重写。

**Tech Stack:** React 19 + TypeScript + Tailwind v4 + vitest + @testing-library/react。拖动/拉伸用原生 Pointer Events，**不引任何拖拽库**。

## Global Constraints

- **形态基准**：`prototypes/desktop-console/src/App.jsx` 的 `HomeDock` / `HomeWindow` 两个组件与 `styles.css` 末尾那段 `/* ---------- home 基准悬浮入口与浮窗 ---------- */`。用户已在原型里点过并确认。**拿不准就去看原型源码，不要自由发挥。**
- **不许改 `WorkbenchPage.tsx` 的 `renderContent` 签名。** 这条是 W4 交接文档点名的红线，PTY 那期已经扩过一次。浮窗是 Shell 层的兄弟节点，根本不经过 WorkbenchPage。
- **一行 Go 都不许改。** 交活前 `git diff --stat -- '*.go' internal/ cmd/` 必须无输出。本计划不需要任何后端改动——PTY 的建/连/删接口都已存在。
- **浮窗里只有终端，不许放文件浏览或任务 TUI。** 以 `$HOME` 为根浏览文件会让控制台会话（刻意做得比主令牌弱的凭据）读到 `~/.handoff/config.yaml` 里的主令牌与 `~/.ssh/`，弱凭据当场提权成强凭据。这是安全边界，不是排期取舍。
- **禁止 `console.log`。** 前端没有结构化 logger，可观测性走**语义化 DOM + 错误可见**：状态用可查询的 `aria-label` / `data-*` 暴露，失败路径必须在界面上留下痕迹而不是静默吞掉。
- 每个 task 结束时前端四条全绿：

```bash
cd web && npx vitest run && npm run typecheck && npm run lint && npm run build
```

## 三条已核实的既有事实——**别按 spec 的旧估计做**

写 spec 时我高估了两处成本。实现前先读懂这三条，否则会写出冗余甚至有害的代码：

1. **PTY 尺寸同步已经自动跟随 DOM，不需要你接任何东西。**
   `TerminalTab.tsx:143` 已经挂了 `ResizeObserver` → `fit.fit()`，:134 又有 `term.onResize(({cols,rows}) => handle?.resize(cols,rows))`。**只要 `TerminalTab` 渲染在浮窗内部，拉伸浮窗就会自动重算行列并上报服务端。**
   👉 **不要**在浮窗的拉伸回调里再调一次 resize。重复调用会和 ResizeObserver 打架，产生可见的抖动。spec §3.2 说「拉伸结束后要重新 resize」是**错的**，以本条为准。

2. **卸载 `TerminalTab` 不会杀会话。**
   它的 cleanup 里写着「只断连接，不发 DELETE：服务端会话继续跑」（:149）。所以**「收起浮窗 = 卸载 = 会话继续活着」是免费的**，你不需要为收起做任何会话侧处理。

3. **杀会话要显式调 `deletePtySession`。**
   `api/client.ts:259` 提供 `deletePtySession(id, machine?)`。中央 tab 走的是 Shell 里的 `beforeCloseTab`（`Shell.tsx:192` 传给 WorkbenchPage 的那个）。浮窗的 `×` 要走同一套语义——**优先把 `beforeCloseTab` 里那段逻辑抽成可复用函数，两边共用**；抽不动就照抄它的语义（含失败时的错误呈现），不要只发一个 fire-and-forget 的 DELETE 就完事。

---

## 文件结构

| 文件 | 动作 | 职责 |
|---|---|---|
| `web/src/app/homedock/useHomeDock.ts` | 新建 | home 终端的全部状态：tab 列表、激活项、浮窗开合、位置尺寸。纯状态，不碰 DOM |
| `web/src/app/homedock/useHomeDock.test.ts` | 新建 | 新建/激活/关闭/收起 的状态迁移 |
| `web/src/app/homedock/HomeWindow.tsx` | 新建 | 浮窗容器：标题栏（可拖）、tab 条、内容区、拉伸角 |
| `web/src/app/homedock/HomeWindow.test.tsx` | 新建 | 拖动/拉伸改几何、收起不杀、× 杀 |
| `web/src/app/homedock/HomeDock.tsx` | 新建 | 右下角圆钮 + 小面板（清单 + 新建 + 角标） |
| `web/src/app/homedock/HomeDock.test.tsx` | 新建 | 角标计数、清单跳转、新建 |
| `web/src/app/workbench/FloatingNewPane.tsx` | 删除 | 被 HomeDock 取代 |
| `web/src/app/workbench/FloatingNewPane.test.tsx` | 删除 | 同上 |
| `web/src/app/shell/Shell.tsx` | 修改 | 渲染 HomeDock 取代 FloatingNewPane；home 会话恢复改路由到浮窗 |
| `web/src/app/workbench/usePtyRestore.ts` | 修改 | 恢复回调按 `base_kind` 分流 |

**为什么新开 `app/homedock/` 目录而不是塞进 `app/workbench/`**：这套东西的全部意义就是「不属于工作区」。放进 workbench 目录等于在文件结构上继续说它是工作区的一部分，下一个读代码的人会先被误导一次。

---

## Task 1: useHomeDock 状态

**Files:**
- Create: `web/src/app/homedock/useHomeDock.ts`
- Create: `web/src/app/homedock/useHomeDock.test.ts`

**Interfaces:**
- Produces:

```ts
export interface HomeTab {
  id: string          // 客户端生成的 tab 身份（不是服务端 sessionId）
  seq: number         // 第几个终端，用于标题 'bash · home N'
  sessionId?: string  // 服务端会话 id；建成之前是 undefined
  machine: string     // '' = 本机
}

export interface HomeDockApi {
  tabs: HomeTab[]
  activeId: string | null
  windowOpen: boolean
  geom: { x: number; y: number; w: number; h: number }
  newTerminal: (machine?: string) => void   // 建 tab、激活它、打开浮窗
  activate: (id: string) => void            // 激活并打开浮窗
  collapse: () => void                      // 收起浮窗，**不动 tabs**
  closeTab: (id: string) => void            // 从列表移除；杀会话由调用方负责
  setSession: (id: string, sessionId: string) => void
  setGeom: (g: Partial<{ x: number; y: number; w: number; h: number }>) => void
  adopt: (t: HomeTab) => void               // 恢复既有会话时把它收进来
}
```

- [ ] **Step 1: 写失败的测试**

新建 `web/src/app/homedock/useHomeDock.test.ts`：

```ts
import { act, renderHook } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { useHomeDock } from './useHomeDock'

describe('useHomeDock', () => {
  it('新建终端：进列表、被激活、浮窗打开', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newTerminal())
    expect(result.current.tabs).toHaveLength(1)
    expect(result.current.activeId).toBe(result.current.tabs[0].id)
    expect(result.current.windowOpen).toBe(true)
  })

  it('seq 递增，且关掉再开不复用旧号', () => {
    // why：标题里的编号是给人认的。复用号会让「home 2」在一次会话里指过两个
    // 不同的终端，用户按标题找会找错
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newTerminal())
    act(() => result.current.newTerminal())
    const second = result.current.tabs[1]
    act(() => result.current.closeTab(second.id))
    act(() => result.current.newTerminal())
    expect(result.current.tabs[1].seq).toBe(3)
  })

  it('收起浮窗不动 tabs——会话还在', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newTerminal())
    act(() => result.current.collapse())
    expect(result.current.windowOpen).toBe(false)
    expect(result.current.tabs).toHaveLength(1)
  })

  it('关掉激活项时激活权交给剩下的第一个', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newTerminal())
    act(() => result.current.newTerminal())
    const active = result.current.activeId!
    act(() => result.current.closeTab(active))
    expect(result.current.tabs).toHaveLength(1)
    expect(result.current.activeId).toBe(result.current.tabs[0].id)
  })

  it('关掉最后一个：浮窗自动收起，activeId 归 null', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newTerminal())
    act(() => result.current.closeTab(result.current.tabs[0].id))
    expect(result.current.tabs).toHaveLength(0)
    expect(result.current.windowOpen).toBe(false)
    expect(result.current.activeId).toBeNull()
  })

  it('activate 会把收起的浮窗重新打开', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newTerminal())
    act(() => result.current.collapse())
    act(() => result.current.activate(result.current.tabs[0].id))
    expect(result.current.windowOpen).toBe(true)
  })

  it('adopt 收编既有会话，但不抢焦点也不弹窗', () => {
    // why：恢复是后台动作。页面一加载就弹出浮窗，等于替用户点了一下
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.adopt({ id: 'r1', seq: 1, sessionId: 's1', machine: '' }))
    expect(result.current.tabs).toHaveLength(1)
    expect(result.current.windowOpen).toBe(false)
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/homedock/useHomeDock.test.ts`
Expected: FAIL，找不到模块

- [ ] **Step 3: 写实现**

新建 `web/src/app/homedock/useHomeDock.ts`，文件头注释必须写清职责与边界：

```ts
// useHomeDock —— home 基准终端的状态。
//
// 职责：持有一组 home 终端 tab、当前激活项、浮窗的开合与几何。
//
// 边界（这是它存在的全部理由）：
//   - **与 useWorkbench 完全独立**。home 终端不挂在任何目录上，而中央工作区的
//     tab 组是按「当前选中目录」组织的（byBase 那张 Map）。把 home 塞进去，
//     它就会跟着目录切换走——「我刚才那个 ssh 到 devbox 的终端去哪了」正是
//     这么来的
//   - 不碰 DOM、不发任何请求。建会话是 TerminalTab 的事，杀会话由调用方在
//     closeTab 前后自己做（见计划 Global 的第 3 条既有事实）
```

关键实现点（每一条都有对应用例钉着）：

- `seq` 用一个只增不减的计数器，**不要**用 `tabs.length + 1`——关掉中间一个再新建就会撞号。
- `collapse()` 只写 `windowOpen = false`，**绝不碰 `tabs`**。
- `closeTab()` 移除后：若删的是激活项且还有剩余，激活权给剩下的第一个；若删空了，`windowOpen = false` 且 `activeId = null`。
- `adopt()` 只 push 进 tabs，**不改 `windowOpen`、不改 `activeId`**（若 `activeId` 为 null 可以设成它，但不许打开浮窗）。
- `geom` 的初值给一个明显不挡住左栏的位置，例如 `{ x: 320, y: 140, w: 620, h: 340 }`；`setGeom` 做下界钳制（`w >= 360`、`h >= 200`、`x >= 8`、`y >= 8`）。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/homedock/useHomeDock.test.ts`
Expected: PASS（7 个用例）

- [ ] **Step 5: 加注释**

- 文件头注释（Step 3 已给）
- `seq` 计数器上方写清「为什么不用 tabs.length」
- `collapse` 上方写清「为什么不动 tabs」（收起 ≠ 关闭，会话在服务端活着）
- `adopt` 上方写清「为什么不打开浮窗」（恢复是后台动作）

- [ ] **Step 6: 全量回归 + 提交**

```bash
cd web && npx vitest run && npm run typecheck && npm run lint && npm run build
```

```bash
git add web/src/app/homedock/
git commit -m "feat(web): home 终端状态 useHomeDock，与工作区 tab 完全独立"
```

---

## Task 2: HomeWindow 浮窗容器

**Files:**
- Create: `web/src/app/homedock/HomeWindow.tsx`
- Create: `web/src/app/homedock/HomeWindow.test.tsx`

**Interfaces:**
- Consumes: Task 1 的 `HomeDockApi`
- Produces:

```tsx
export interface HomeWindowProps {
  tabs: HomeTab[]
  activeId: string | null
  geom: { x: number; y: number; w: number; h: number }
  onGeom: (g: Partial<{ x: number; y: number; w: number; h: number }>) => void
  onActivate: (id: string) => void
  onNew: () => void
  onKill: (id: string) => void      // 由调用方负责真的删服务端会话
  onCollapse: () => void
  renderTab: (t: HomeTab) => ReactNode  // 内容由调用方给，本组件不认识 TerminalTab
}
```

**为什么内容用 `renderTab` 注入而不是直接 import `TerminalTab`**：让浮窗容器可以脱离 PTY 单测（终端要 canvas/WebGL，jsdom 里跑不动）。这是**可测性驱动的边界**，不是过度设计。

- [ ] **Step 1: 写失败的测试**

```tsx
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { HomeWindow } from './HomeWindow'

const tabs = [
  { id: 'a', seq: 1, machine: '' },
  { id: 'b', seq: 2, machine: '' },
]
const geom = { x: 100, y: 100, w: 600, h: 300 }
const base = () => ({
  tabs, activeId: 'a', geom,
  onGeom: vi.fn(), onActivate: vi.fn(), onNew: vi.fn(),
  onKill: vi.fn(), onCollapse: vi.fn(),
  renderTab: (t: { id: string }) => <div data-testid={`content-${t.id}`} />,
})

describe('HomeWindow', () => {
  it('只渲染激活 tab 的内容', () => {
    render(<HomeWindow {...base()} />)
    expect(screen.getByTestId('content-a')).toBeInTheDocument()
    expect(screen.queryByTestId('content-b')).toBeNull()
  })

  it('收起走 onCollapse，不走 onKill', () => {
    const p = base()
    render(<HomeWindow {...p} />)
    fireEvent.click(screen.getByLabelText('收起（会话保留）'))
    expect(p.onCollapse).toHaveBeenCalledTimes(1)
    expect(p.onKill).not.toHaveBeenCalled()
  })

  it('tab 上的 × 走 onKill，且不误触发激活', () => {
    const p = base()
    render(<HomeWindow {...p} />)
    fireEvent.click(screen.getByLabelText('关闭 bash · home 2'))
    expect(p.onKill).toHaveBeenCalledWith('b')
    expect(p.onActivate).not.toHaveBeenCalled()
  })

  it('拖标题栏改位置', () => {
    const p = base()
    render(<HomeWindow {...p} />)
    const title = screen.getByTestId('home-window-title')
    fireEvent.pointerDown(title, { clientX: 0, clientY: 0 })
    fireEvent.pointerMove(document, { clientX: 40, clientY: 25 })
    fireEvent.pointerUp(document)
    expect(p.onGeom).toHaveBeenCalledWith(expect.objectContaining({ x: 140, y: 125 }))
  })

  it('拉右下角改尺寸', () => {
    const p = base()
    render(<HomeWindow {...p} />)
    fireEvent.pointerDown(screen.getByTestId('home-window-corner'), { clientX: 0, clientY: 0 })
    fireEvent.pointerMove(document, { clientX: 30, clientY: 20 })
    fireEvent.pointerUp(document)
    expect(p.onGeom).toHaveBeenCalledWith(expect.objectContaining({ w: 630, h: 320 }))
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/homedock/HomeWindow.test.tsx`
Expected: FAIL

- [ ] **Step 3: 写实现**

形态照原型的 `HomeWindow`（深色壳、31px 标题栏、29px tab 条、右下 15px 拉伸角）。要点：

- 外框 `position: fixed`，`left/top/width/height` 走 `geom` 的行内样式；`z-index` 取一个**低于 Overlay（z-50）**的值，例如 `z-40`——看板弹层打开时该盖住浮窗，否则弹层的遮罩会露出一个洞。
- 拖动与拉伸共用一个 `grab(event, apply)`：按下记起点，`pointermove` 算增量并调 `onGeom`，`pointerup` 一次性解绑。**用 `document` 上的监听而不是元素上的**，否则指针移出窗口就丢事件。
- `× ` 按钮必须 `e.stopPropagation()`，否则点它会连带触发所在 tab 的激活。
- **只渲染激活 tab 的内容**（见测试第一条）。非激活的终端卸载 = 断连接但会话继续活着（既有事实 2），切回来时 `TerminalTab` 会用同一个 `sessionId` 重连。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/homedock/HomeWindow.test.tsx`
Expected: PASS（5 个用例）

- [ ] **Step 5: 加注释**

文件头写职责与边界（含「内容用 renderTab 注入是为了可测」）。另外三处 why 必须留：

```tsx
// z-40：必须低于 Overlay 的 z-50。看板/工单弹层打开时应当盖住浮窗，
// 否则弹层遮罩上会露出一个亮洞

// 监听挂在 document 上而不是元素上：指针拖出窗口时元素收不到 move，
// 窗口会卡在半路

// stopPropagation：不加的话点 × 会连带激活这个 tab，
// 于是"关闭"变成"先切过去再关掉"，看起来像闪了一下
```

- [ ] **Step 6: 全量回归 + 提交**

```bash
git add web/src/app/homedock/HomeWindow.tsx web/src/app/homedock/HomeWindow.test.tsx
git commit -m "feat(web): home 终端浮窗容器，可拖可拉伸，收起不杀会话"
```

---

## Task 3: HomeDock 入口面板

**Files:**
- Create: `web/src/app/homedock/HomeDock.tsx`
- Create: `web/src/app/homedock/HomeDock.test.tsx`

**Interfaces:**
- Consumes: Task 1 的 `HomeDockApi`、Task 2 的 `HomeWindow`
- Produces: `<HomeDock dock={HomeDockApi} renderTab={...} onKill={...} />`

**形态**：收起时是右下角圆钮（带存活数角标）；展开是一张小面板——标题「home 基准」、一句说明、已开终端清单（每项带存活点）、底部「新终端 ⌘T」。

- [ ] **Step 1: 写失败的测试**

```tsx
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { HomeDock } from './HomeDock'
import type { HomeTab } from './useHomeDock'

// dock 造一个 HomeDockApi 替身。只给本组件真正读写的字段，
// 其余动作用 vi.fn() —— 本用例测的是入口面板，不是状态机（那是 Task 1 的事）
function dock(over: Partial<{ tabs: HomeTab[]; windowOpen: boolean }> = {}) {
  return {
    tabs: [] as HomeTab[],
    activeId: null,
    windowOpen: false,
    geom: { x: 0, y: 0, w: 600, h: 300 },
    newTerminal: vi.fn(),
    activate: vi.fn(),
    collapse: vi.fn(),
    closeTab: vi.fn(),
    setSession: vi.fn(),
    setGeom: vi.fn(),
    adopt: vi.fn(),
    ...over,
  }
}

const TAB_A: HomeTab = { id: 'a', seq: 1, machine: '' }
const TAB_B: HomeTab = { id: 'b', seq: 2, machine: '' }
const props = (d: ReturnType<typeof dock>) => ({
  dock: d,
  onKill: vi.fn(),
  renderTab: () => <div data-testid="term" />,
})

describe('HomeDock', () => {
  it('无会话时圆钮不带角标', () => {
    render(<HomeDock {...props(dock())} />)
    expect(screen.getByLabelText('home 基准终端')).toBeInTheDocument()
    expect(screen.queryByTestId('home-badge')).toBeNull()
  })

  it('有会话时角标显示数量', () => {
    render(<HomeDock {...props(dock({ tabs: [TAB_A, TAB_B] }))} />)
    expect(screen.getByTestId('home-badge')).toHaveTextContent('2')
  })

  it('点圆钮出面板，列出已开终端与「新终端」', () => {
    render(<HomeDock {...props(dock({ tabs: [TAB_A] }))} />)
    fireEvent.click(screen.getByLabelText('home 基准终端'))
    expect(screen.getByText('bash · home 1')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /新终端/ })).toBeInTheDocument()
    // 圆钮与面板互斥：面板出来了圆钮就该消失
    expect(screen.queryByLabelText('home 基准终端')).toBeNull()
  })

  it('点清单某项 → activate 并收起面板', () => {
    const d = dock({ tabs: [TAB_A] })
    render(<HomeDock {...props(d)} />)
    fireEvent.click(screen.getByLabelText('home 基准终端'))
    fireEvent.click(screen.getByText('bash · home 1'))
    expect(d.activate).toHaveBeenCalledWith('a')
    expect(screen.queryByRole('button', { name: /新终端/ })).toBeNull()
  })

  it('点「新终端」走 newTerminal 并收起面板', () => {
    const d = dock()
    render(<HomeDock {...props(d)} />)
    fireEvent.click(screen.getByLabelText('home 基准终端'))
    fireEvent.click(screen.getByRole('button', { name: /新终端/ }))
    expect(d.newTerminal).toHaveBeenCalledTimes(1)
  })

  it('浮窗收起后角标仍在——这是「收起不杀」的唯一可见证据', () => {
    // why：浮窗收起后，圆钮角标是用户唯一能看到「还有几个会话活着」的地方。
    // 没有它，「收起不杀」这条口径在界面上就不成立——用户会以为会话没了
    render(<HomeDock {...props(dock({ tabs: [TAB_A, TAB_B], windowOpen: false }))} />)
    expect(screen.getByTestId('home-badge')).toHaveTextContent('2')
  })

  it('windowOpen 时渲染浮窗内容，收起时不渲染', () => {
    const { rerender } = render(<HomeDock {...props(dock({ tabs: [TAB_A], windowOpen: true, activeId: 'a' } as never))} />)
    expect(screen.getByTestId('term')).toBeInTheDocument()
    rerender(<HomeDock {...props(dock({ tabs: [TAB_A], windowOpen: false }))} />)
    expect(screen.queryByTestId('term')).toBeNull()
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/homedock/HomeDock.test.tsx`
Expected: FAIL，找不到 `./HomeDock`

- [ ] **Step 3: 写实现**（形态照原型的 `HomeDock`）

要点：

- 圆钮与面板**互斥渲染**（原型就是这么做的）：面板展开时圆钮消失，面板收起时圆钮回来。
- 角标只在 `tabs.length > 0` 时渲染。
- 清单项点击 → `dock.activate(id)` 并把面板收起。
- 浮窗由 `HomeDock` 渲染（`dock.windowOpen && <HomeWindow ... />`），这样 Shell 只需挂一个组件。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/homedock/HomeDock.test.tsx`
Expected: PASS（7 个用例）

- [ ] **Step 5: 加注释**

文件头写职责与边界。角标那处必须留一句 why：

```tsx
{/* 角标是浮窗收起后「还有几个会话活着」的唯一可见证据。
    没有它，「收起不杀」这条口径在界面上就不成立——用户会以为会话没了 */}
```

- [ ] **Step 6: 全量回归 + 提交**

```bash
cd web && npx vitest run && npm run typecheck && npm run lint && npm run build
```

```bash
git add web/src/app/homedock/HomeDock.tsx web/src/app/homedock/HomeDock.test.tsx
git commit -m "feat(web): home 悬浮入口面板，圆钮角标反映存活会话数"
```

---

## Task 4: 接线 Shell，退役 FloatingNewPane

**Files:**
- Modify: `web/src/app/shell/Shell.tsx`
- Delete: `web/src/app/workbench/FloatingNewPane.tsx`
- Delete: `web/src/app/workbench/FloatingNewPane.test.tsx`
- Modify: `web/src/app/shell/Shell.test.tsx`

- [ ] **Step 1: 写失败的测试**

`Shell.test.tsx` 加：

```tsx
it('home 终端不进中央 tab 条', () => {
  // 从悬浮入口新建一个终端后，中央 tab 条上不应出现它
  // 断言方式：中央 TabBar 内查不到 'home' 标题的 tab
})

it('对端不支持 PTY 时不渲染圆钮', () => {
  // 既有行为，必须保住：走查项 9 立的规矩是「说实话而不是给个死按钮」
})
```

- [ ] **Step 2: 跑测试确认它失败**

- [ ] **Step 3: 换掉渲染点**

`Shell.tsx:237`，把

```tsx
{ptySupport.supported('') !== false && <FloatingNewPane onNewTerminal={() => wb.openTerminal(HOME_BASE)} />}
```

换成

```tsx
{/* home 终端走独立浮窗，不进 wb 的 tab 组——它不挂在任何目录上，
    塞进按目录组织的容器里就会跟着目录切换走 */}
{ptySupport.supported('') !== false && (
  <HomeDock
    dock={dock}
    onKill={killHomeSession}
    renderTab={(t) => (
      <TerminalTab
        base={HOME_BASE}
        seq={t.seq}
        sessionId={t.sessionId}
        onSession={(id) => dock.setSession(t.id, id)}
      />
    )}
  />
)}
```

`dock` 来自 `const dock = useHomeDock()`。

- [ ] **Step 4: 杀会话走既有语义**

`killHomeSession(id)` 要和中央 tab 的关闭语义一致。**先看 `Shell.tsx` 里 `beforeCloseTab` 是怎么做的**（它是 :192 传给 WorkbenchPage 的那个），把其中「删服务端会话 + 失败时呈现错误」那段抽成一个可复用函数，两边共用。

**不许**写成 `void deletePtySession(id).catch(() => {})`——失败被吞掉的话，用户会以为会话关了、实际服务端还留着一个 shell。失败必须在界面上留痕（沿用中央 tab 现有的错误呈现方式，不要新造一套）。

- [ ] **Step 5: 删掉 FloatingNewPane**

删两个文件，并清掉 `Shell.tsx` 里对它的 import。

`git grep -n FloatingNewPane` 必须**无输出**。

- [ ] **Step 6: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/shell/ src/app/homedock/`
Expected: PASS。注意用例总数会因删掉 `FloatingNewPane.test.tsx` 而减少几个，但 homedock 新增的应当远多于它——**总数必须比开工前多**。

- [ ] **Step 7: 加注释**（Step 3 的 why 已给；`killHomeSession` 上方补一句「为什么不能吞错误」）

- [ ] **Step 8: 全量回归 + 提交**

```bash
git add web/src/app/shell/ web/src/app/workbench/ web/src/app/homedock/
git commit -m "feat(web): home 终端接入浮窗，退役 FloatingNewPane"
```

---

## Task 5: home 会话恢复改路由到浮窗

**Files:**
- Modify: `web/src/app/shell/Shell.tsx`（只改传给 `usePtyRestore` 的那个回调）
- Modify: `web/src/app/shell/Shell.test.tsx`

**背景：** `usePtyRestore.ts:29-37` 已经会判断 `s.base_kind === 'home'` 并造出 `HOME_BASE`（或 `~@<machine>`），但恢复出来的会话目前一律交给 `wb.restoreTerminal` —— 也就是**恢复到中央工作区**。Task 4 之后这会造成分裂：新建的 home 终端在浮窗里，刷新页面后恢复出来的却在中央。

**注意：`usePtyRestore.ts` 一个字都不用改。** 它的签名是
`usePtyRestore(restore: (b: BaseDir, sessionId: string) => void)`，而 `BaseDir`
上已经带着 `kind: 'workspace' | 'home'`。分流在**调用方给的那个回调里**做就行——
不要去改 hook 的签名，那是无谓地扩大改动面。

- [ ] **Step 1: 写失败的测试**

在 `Shell.test.tsx` 里加（沿用该文件已有的 `fetchPtySessions` mock 方式；若没有就照 `usePtyRestore.test.ts` 里的 mock 写法）：

```tsx
it('恢复时 home 会话进浮窗、工作树会话进中央', async () => {
  mockPtySessions([
    { id: 's-home', base_kind: 'home', base_path: '~', machine: '' },
    { id: 's-ws', base_kind: 'workspace', base_path: '/repo/x', machine: '' },
  ])
  render(<Shell />)

  // home 那条：圆钮角标出现 1
  expect(await screen.findByTestId('home-badge')).toHaveTextContent('1')
  // 且浮窗没有被自动弹出——恢复是后台动作
  expect(screen.queryByTestId('home-window-title')).toBeNull()

  // 工作树那条：不该计进 home 角标
  expect(screen.getByTestId('home-badge')).not.toHaveTextContent('2')
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/shell/Shell.test.tsx`
Expected: FAIL——恢复出来的 home 会话现在进了中央工作区，角标是 0

- [ ] **Step 3: 在回调里分流**

`Shell.tsx` 里找到 `usePtyRestore(...)` 的调用点，把回调改成：

```tsx
// 恢复出来的会话按基准分流：home 的收进浮窗，工作树的回中央工作区。
// 不分流的话，Task 4 之后会出现「新建的在浮窗、刷新后恢复的却在中央」
// 这种自相矛盾的状态。
//
// 用 adopt 而不是 newTerminal：adopt 不打开浮窗、不抢焦点——页面一加载
// 就弹出浮窗，等于替用户点了一下
const ptyRestore = usePtyRestore((b, sessionId) => {
  if (b.kind === 'home') {
    dock.adopt({ id: sessionId, seq: dock.tabs.length + 1, sessionId, machine: b.machine })
    return
  }
  wb.restoreTerminal(b, sessionId)
})
```

**`seq` 这里用 `tabs.length + 1` 是可以的**——恢复发生在挂载时、一次性、按顺序，不存在 Task 1 里那个「关掉中间一个再新建会撞号」的场景。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/shell/Shell.test.tsx`
Expected: PASS

- [ ] **Step 5: 加注释**

Step 3 代码块里的两段 why 即为产出，确认留在最终代码里。

- [ ] **Step 6: 全量回归 + 提交**

```bash
cd web && npx vitest run && npm run typecheck && npm run lint && npm run build
```

```bash
git add web/src/app/shell/Shell.tsx web/src/app/shell/Shell.test.tsx
git commit -m "fix(web): home 会话恢复到浮窗而非中央工作区"
```

---

## Task 6: 走查记录与收口

**Files:**
- Create: `docs/superpowers/notes/2026-08-14-w4-home-floating-terminal-check.md`

**不改代码。** 把 spec §5 的十条验收逐条落成可查的记录。

- [ ] **Step 1: 十条逐条写「判据 / 结果 / 证据」**

**你没有浏览器，凡是要肉眼看或要真实 PTY 的条目一律如实标「未验（无浏览器）」，并列出替它把关的自动化用例。不许猜通过。**

必然未验的：条 5（拉伸后 `stty size` 与窗口一致——要真终端）、条 6（收起再打开输出连续）、条 9（不支持 PTY 的对端，手上没有）。

能自动化背书的：条 1、2、3、4、7、8、10。

- [ ] **Step 2: 记下计划期发现的两处 spec 更正**

单开一节写清楚——这是给下一个读 spec 的人看的：

1. spec §3.2 说「浮窗拉伸结束后要重新 resize 该 PTY」是**错的**。`TerminalTab` 已有 `ResizeObserver`，尺寸自动跟随；额外 resize 会与之打架。
2. spec §2.3「收起不杀」在实现上是**免费**的，不需要专门处理——`TerminalTab` 卸载时本来就只断连接不发 DELETE。

- [ ] **Step 3: 贴回归原文 + 空 diff 证据**

四条前端命令的实际输出（用例数、通过数），以及：

```bash
git diff --stat -- '*.go' internal/ cmd/          # 必须空
git grep -n FloatingNewPane                        # 必须空
git diff -- web/src/app/workbench/WorkbenchPage.tsx | grep renderContent   # 必须空
```

- [ ] **Step 4: 提交**

```bash
git add docs/superpowers/notes/2026-08-14-w4-home-floating-terminal-check.md
git commit -m "docs(notes): home 终端浮窗走查记录"
```

---

## 收尾自检

- [ ] `git diff --stat -- '*.go' internal/ cmd/` 无输出
- [ ] `renderContent` 签名未变（红线）
- [ ] `git grep -n FloatingNewPane` 无输出
- [ ] 浮窗里只有终端，没有文件/TUI 入口（安全边界）
- [ ] 前端四条全绿，用例总数比开工前**多**
- [ ] **没有新增** `console.*`。注意 `web/src/app/workbench/usePtyRestore.ts` 里本来就有一处 `console.warn`（拉不到会话列表时的告警，是有意留的），**那是既有代码，不在本计划范围内，不要顺手改它**。判据是 `git diff` 里没有新增的 `console.` 行，不是全仓 grep 为空
- [ ] 新建的四个文件都有文件头注释（职责 + 边界）
- [ ] 没有在拉伸回调里额外调 PTY resize（会与 ResizeObserver 打架）
- [ ] 杀会话失败时界面有痕迹，不是静默吞掉
- [ ] 走查记录里未验的如实标未验，两处 spec 更正单列一节
