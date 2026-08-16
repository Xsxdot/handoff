# B94 控制台走查第三轮四条交互缺陷 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修掉 08-15 走查暴露的四条交互缺陷——悬浮入口的多余中间层、注销按钮与行内计数的位置冲突、tab 焦点被后台内容回写抢走、关 home 终端时 machine 传成了 MouseEvent 对象。

**Architecture:** 四条互不依赖，可按 Task 顺序独立提交。其中第三条（焦点）是**单点根因修复**——只改 `tabs.ts` 的 `setTabContent`，不在任何调用方打补丁；第二条需要先造一个仓库里目前不存在的轻量 `ContextMenu` 组件。

**Tech Stack:** React 19 + TypeScript + Vite + Tailwind v4 + vitest / @testing-library/react + lucide-react

**Spec:** [2026-08-15-b94-console-interaction-defects-design.md](../specs/2026-08-15-b94-console-interaction-defects-design.md)

## Global Constraints

- 分支基线：`w4-delivery` @ `4f86c41ff`。**所有改动都在 `web/` 下**，不碰 Go 代码。
- 颜色一律走既有 token，**禁止**新写十六进制色值。选中态用 `bg-sidebar-accent`、悬停态用 `hover:bg-accent/60`（`ProjectTree.tsx:360` 的既有约定，08-14 因把两态写成同色返过工一次）。
- 本仓库前端**没有** logger，也不用 `console.log` 记流程。项目既定口径是「错误呈现给用户，不吞、不往 console 记流水账」——唯一的 `console.warn` 在 `usePtyRestore.ts:79`，用于一条用户看不见后果的后台失败。**本计划四个 Task 全部是纯 UI 状态变更，不新增任何 I/O、请求或错误分支，所以不加日志步骤**；对应的可观测性要求由「注释说明为什么」+「测试钉死行为」承担，每个 Task 都有这两步，缺任一不算完。
- 每个 Task 结束即 commit，提交信息说清做了什么。
- 全部完成后跑一次总回归（Task 5）。

## 文件结构

| 文件 | 责任 | 本计划的改动 |
|---|---|---|
| `web/src/app/workbench/tabs.ts` | tab 模型层纯函数 | `setTabContent` 不再改 `activeId`/`active` |
| `web/src/app/homedock/HomeWindow.tsx` | home 终端浮窗 | `onClick={onNew}` → `onClick={() => onNew()}` |
| `web/src/app/homedock/HomeDock.tsx` | 右下角入口 + 挂浮窗 | 删整张面板与 `panelOpen`；FAB 三分支 |
| `web/src/app/tree/ContextMenu.tsx` | **新建**：轻量右键菜单 | 新组件 |
| `web/src/app/tree/ProjectTree.tsx` | 左栏项目树 | 删 absolute 注销按钮；机器行接右键菜单 |

---

### Task 1: `setTabContent` 不再抢焦点

**Files:**
- Modify: `web/src/app/workbench/tabs.ts:192-211`
- Test: `web/src/app/workbench/tabs.test.ts`（在既有 `describe('setTabContent')` 内追加）

**Interfaces:**
- Consumes: 无（本计划第一个 Task）
- Produces: `setTabContent(wb, group, tabId, content)` 语义收窄为「只换内容」。调用方若需要同时切过去，必须自己再调 `activateTab`。去重分支（目标已在别处打开）的激活行为**不变**。

- [ ] **Step 1: 写失败的测试**

在 `web/src/app/workbench/tabs.test.ts` 的 `describe('setTabContent', () => { ... })` 内追加：

```ts
  it('回写内容不抢焦点——这是「点不进新标签页」的根因', () => {
    // why：setTabContent 是「换这个 tab 的内容」，不是「切到这个 tab」。
    // 焊在一起的话，FileTab 卸载时回写草稿（Shell.tsx onDraftChange）
    // 会把焦点从用户刚点开的空白 tab 拽回文件 tab
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'go.mod' })
    const fileId = wb.groups[0].tabs[0].id
    wb = openTab(wb, { kind: 'blank' })
    const blankId = wb.groups[0].tabs[1].id
    expect(wb.groups[0].activeId).toBe(blankId)

    // 模拟 FileTab 卸载时的草稿回写：写的是**非激活**的那个 tab
    wb = setTabContent(wb, 0, fileId, { kind: 'file', rel: 'go.mod', draft: 'x', baseSha: 'h' })

    expect(wb.groups[0].activeId).toBe(blankId)
    expect(wb.groups[0].tabs[0].content).toEqual({
      kind: 'file', rel: 'go.mod', draft: 'x', baseSha: 'h',
    })
  })

  it('回写非焦点组的内容不把焦点组抢过去', () => {
    // why：终端会话 id 回写（Shell.tsx onSession）可能发生在用户已经切到
    // 另一组之后。next.active 一起改掉的话，分屏时焦点会莫名跳组
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'terminal', seq: 1 })
    const termId = wb.groups[0].tabs[0].id
    wb = splitGroup(wb)
    wb = openTab(wb, { kind: 'blank' }, 1)
    expect(wb.active).toBe(1)

    wb = setTabContent(wb, 0, termId, { kind: 'terminal', seq: 1, sessionId: 'S1' })

    expect(wb.active).toBe(1)
    expect(wb.groups[0].tabs[0].content).toEqual({ kind: 'terminal', seq: 1, sessionId: 'S1' })
  })
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/workbench/tabs.test.ts`
Expected: 两条新用例 FAIL——第一条 `expected 't2' but got 't1'`（activeId 被拽走），第二条 `expected 1 but got 0`（active 被拽走）。

- [ ] **Step 3: 改实现**

`web/src/app/workbench/tabs.ts` 的 `setTabContent`，把非去重分支末尾这两行删掉：

```ts
  next.groups[gi].tabs[idx] = { id: tabId, content }
  next.groups[gi].activeId = tabId   // ← 删
  next.active = gi                   // ← 删
  return next
```

改成：

```ts
  next.groups[gi].tabs[idx] = { id: tabId, content }
  return next
```

**去重分支（函数开头 `if (key !== null) { ... }` 那一段）一行不动**——那里的 `activateTab` 是用户选目标后的导航，不是回写。

- [ ] **Step 4: 加注释说明为什么**

把 `setTabContent` 上方的函数注释改成（保留既有的「边界情形」段落，在它前面补一段）：

```ts
// setTabContent 把一个 tab 的内容原地换掉（空白 tab 选了种类、终端回写会话 id、
// 文件回写草稿都走它）。
//
// **它只换内容，不动 activeId / active。** 这两件事曾经焊在一起，代价是任何一次
// 后台回写都变成一次导航：FileTab 在**卸载时**回写草稿（Shell 的 onDraftChange），
// 于是用户点开一个空白 tab → FileTab 卸载 → 回写 → 焦点被拽回刚离开的文件 tab，
// 表现为「点不进新标签页」。干净文件也一样（回调无条件触发），终端的 onSession
// 同一条路。切 tab 有专门的 activateTab，调用方需要时自己调。
//
// 边界情形：选中的目标已经在别的 tab 里打开了。此时正确的行为是激活那个 tab
// 并把这个空白 tab 关掉——否则用户会得到两个标着同一个文件的 tab，其中一个
// 是刚才的空白页。**这一支保留激活**：它是用户刚做完一次选择动作，跳过去是他要的。
```

- [ ] **Step 5: 跑全量前端测试**

Run: `cd web && npx vitest run`
Expected: 全绿。**若有既有用例因此变红**，逐条判断是「测试写死了错误行为」还是「真有第四个调用方依赖顺带激活」——spec §2.3 的表格已核对过三处调用（`WorkbenchPage.tsx:69`、`Shell.tsx:279`、`Shell.tsx:295`）都不依赖，出现第四处必须停下来上报，**不许直接改绿**。

- [ ] **Step 6: 变异测试——证明用例真的会红**

Run:
```bash
cd web && sed -i '' 's|^  next.groups\[gi\].tabs\[idx\] = { id: tabId, content }$|  next.groups[gi].tabs[idx] = { id: tabId, content }\n  next.groups[gi].activeId = tabId\n  next.active = gi|' src/app/workbench/tabs.ts && npx vitest run src/app/workbench/tabs.test.ts; git checkout src/app/workbench/tabs.ts
```
Expected: 加回那两行后两条新用例 FAIL，`git checkout` 还原后再跑一次全绿。

- [ ] **Step 7: Commit**

```bash
git add web/src/app/workbench/tabs.ts web/src/app/workbench/tabs.test.ts
git commit -m "fix(web): setTabContent 只换内容不再抢焦点（B94③）"
```

---

### Task 2: `onNew` 不再吞 MouseEvent

**Files:**
- Modify: `web/src/app/homedock/HomeWindow.tsx:128`
- Test: `web/src/app/homedock/HomeWindow.test.tsx`

**Interfaces:**
- Consumes: 无
- Produces: 无新接口。`HomeWindowProps.onNew` 签名不变（仍是 `() => void`）。

- [ ] **Step 1: 写失败的测试**

在 `web/src/app/homedock/HomeWindow.test.tsx` 里追加。先读该文件已有的 props 构造辅助函数并复用它；若没有，按下面这份自带的：

```tsx
  it('点 + 调 onNew 时不传任何实参——传了就会变成 machine', () => {
    // why：onClick={onNew} 会把 MouseEvent 当第一个实参喂进
    // useHomeDock.newTerminal(machine?: string)，HomeTab.machine 存成事件对象，
    // 关会话时拼出 ?machine=[object Object] 当场炸。
    // TS 拦不住：(machine?: string) => void 对 () => void 是合法赋值
    const onNew = vi.fn()
    render(
      <HomeWindow
        tabs={[{ id: 'a', seq: 1, machine: '' }]}
        activeId="a"
        geom={{ x: 0, y: 0, w: 600, h: 300 }}
        onGeom={vi.fn()}
        onActivate={vi.fn()}
        onNew={onNew}
        onKill={vi.fn()}
        onCollapse={vi.fn()}
        renderTab={() => <div />}
      />,
    )
    fireEvent.click(screen.getByLabelText('新终端'))
    expect(onNew).toHaveBeenCalledTimes(1)
    expect(onNew.mock.calls[0]).toHaveLength(0)
  })
```

若 `HomeWindow.tsx:128` 那个按钮没有 `aria-label="新终端"`，本步顺带加上（它现在只有一个 `Plus` 图标，对读屏用户是个没有名字的按钮）。

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/homedock/HomeWindow.test.tsx`
Expected: FAIL，`expected length 0 but got 1`（那一个实参就是 MouseEvent）。

- [ ] **Step 3: 改实现**

`web/src/app/homedock/HomeWindow.tsx:128`：

```tsx
          onClick={() => onNew()}
```

并在这一行上方补一句 why：

```tsx
          {/* 必须包一层箭头：onClick={onNew} 会把 MouseEvent 当实参传下去，
              而下游 newTerminal 的第一个形参是 machine，事件对象会被存进
              HomeTab.machine，关会话时发出 ?machine=[object Object] */}
```

- [ ] **Step 4: 在 HomeDock 侧也显式化**

`web/src/app/homedock/HomeDock.tsx` 传给 `HomeWindow` 的 `onNew` 由 `dock.newTerminal` 改为 `() => dock.newTerminal()`，让「这里刻意不传 machine」是写出来的一行而不是碰巧的引用传递。（本步与 Task 3 会改同一个文件，Task 3 里那份 FAB 改动以本步的结果为基础。）

- [ ] **Step 5: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/homedock/`
Expected: PASS。

- [ ] **Step 6: 变异测试**

Run:
```bash
cd web && sed -i '' 's|onClick={() => onNew()}|onClick={onNew}|' src/app/homedock/HomeWindow.tsx && npx vitest run src/app/homedock/HomeWindow.test.tsx; git checkout src/app/homedock/HomeWindow.tsx
```
Expected: 改回去后新用例 FAIL；`git checkout` 后全绿。**注意 `git checkout` 会把 Step 3 的注释一起还原**，还原后要重新应用 Step 3（或改用 `git stash` 方式做变异）。

- [ ] **Step 7: Commit**

```bash
git add web/src/app/homedock/
git commit -m "fix(web): 浮窗 + 按钮不再把 MouseEvent 当 machine 传下去（B94④）"
```

---

### Task 3: 悬浮入口删掉中间层

**Files:**
- Modify: `web/src/app/homedock/HomeDock.tsx`
- Test: `web/src/app/homedock/HomeDock.test.tsx`（既有三条面板用例要**替换**，不是新增）

**Interfaces:**
- Consumes: Task 2 的 `onNew={() => dock.newTerminal()}`
- Produces: `HomeDock` 的 props 签名不变（`{ dock, renderTab, onKill }`）。组件内部不再有 `panelOpen` 状态。

- [ ] **Step 1: 写失败的测试**

`web/src/app/homedock/HomeDock.test.tsx`：**删掉**「点圆钮出面板…」「点清单某项…」「点「新终端」…」三条（面板已不存在，它们测的是被移除的形态），**追加**：

```tsx
  it('零终端时点圆钮直接开一个终端，不弹中间面板', () => {
    const d = dock()
    render(<HomeDock {...props(d)} />)
    fireEvent.click(screen.getByLabelText('home 基准终端'))
    expect(d.newTerminal).toHaveBeenCalledTimes(1)
    expect(d.activate).not.toHaveBeenCalled()
  })

  it('已有终端且浮窗收起时，点圆钮重开到收起前那个', () => {
    // why：collapse 刻意不动 activeId，所以「收起前你在看哪个」这个信息还在。
    // 永远取最后一个会让「收起→重开」把用户挪到别的终端上
    const d = dock({ tabs: [TAB_A, TAB_B], windowOpen: false } as never)
    d.activeId = 'a'
    render(<HomeDock {...props(d)} />)
    fireEvent.click(screen.getByLabelText('home 基准终端'))
    expect(d.activate).toHaveBeenCalledWith('a')
    expect(d.newTerminal).not.toHaveBeenCalled()
  })

  it('activeId 为 null 时兜底到最后一个 tab，不把 null 送进 activate', () => {
    const d = dock({ tabs: [TAB_A, TAB_B], windowOpen: false } as never)
    render(<HomeDock {...props(d)} />)
    fireEvent.click(screen.getByLabelText('home 基准终端'))
    expect(d.activate).toHaveBeenCalledWith('b')
  })

  it('浮窗开着时点圆钮 = 收起', () => {
    const d = dock({ tabs: [TAB_A], windowOpen: true, activeId: 'a' } as never)
    render(<HomeDock {...props(d)} />)
    fireEvent.click(screen.getByLabelText('home 基准终端'))
    expect(d.collapse).toHaveBeenCalledTimes(1)
    expect(d.newTerminal).not.toHaveBeenCalled()
    expect(d.activate).not.toHaveBeenCalled()
  })

  it('浮窗开着时圆钮仍在——它是开合开关，不是只在收起时出现', () => {
    const d = dock({ tabs: [TAB_A], windowOpen: true, activeId: 'a' } as never)
    render(<HomeDock {...props(d)} />)
    expect(screen.getByLabelText('home 基准终端')).toBeInTheDocument()
  })
```

保留既有的三条：「无会话时圆钮不带角标」「有会话时角标显示数量」「浮窗收起后角标仍在」「windowOpen 时渲染浮窗内容」。

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/homedock/HomeDock.test.tsx`
Expected: 五条新用例全 FAIL（现在点圆钮只会 `setPanelOpen(true)`，三个 dock 动作一个都不会被调）。

- [ ] **Step 3: 改实现**

`web/src/app/homedock/HomeDock.tsx` 改成：

```tsx
export function HomeDock({ dock, renderTab, onKill }: {
  dock: HomeDockApi
  renderTab: (t: HomeTab) => ReactNode
  onKill: (id: string) => void
}) {
  // FAB 是「开/收」开关，一次点击直达终端，中间不再隔一层清单面板。
  //
  // 为什么删掉那张面板：浮窗自己就有 tab 条和 +，面板是同一批终端的第二套清单，
  // 而且挡在第一套前面——用户要点两次才拿得到终端。删掉之后「有几个」由角标说，
  // 「分别是哪几个」由浮窗 tab 条说，第一次点击就都看得见。
  const onFab = () => {
    if (dock.windowOpen) {
      // 浮窗就在眼前时点悬浮球，最可能的意图是收起它
      dock.collapse()
      return
    }
    if (dock.tabs.length === 0) {
      dock.newTerminal()
      return
    }
    // 重开到收起前那个：collapse 刻意不动 activeId，那个信息还在。
    // ?? 兜底实际不会命中（activeId 为 null 只可能在 tabs 为空时，
    // 而那条分支上面已经吃掉了），写它是为了不让 null 进 activate
    dock.activate(dock.activeId ?? dock.tabs[dock.tabs.length - 1].id)
  }

  return (
    <>
      {dock.windowOpen && (
        <HomeWindow
          tabs={dock.tabs}
          activeId={dock.activeId}
          geom={dock.geom}
          onGeom={dock.setGeom}
          onActivate={dock.activate}
          onNew={() => dock.newTerminal()}
          onKill={onKill}
          onCollapse={dock.collapse}
          renderTab={renderTab}
        />
      )}

      <button
        type="button"
        aria-label="home 基准终端"
        onClick={onFab}
        className="fixed right-5 bottom-11 z-40 flex size-11 cursor-pointer items-center justify-center rounded-full border border-[#2b3542] bg-[#10151b] text-white shadow-lg hover:opacity-90"
      >
        <Plus className="size-5" />
        {dock.tabs.length > 0 && (
          /* 角标是浮窗收起后「还有几个会话活着」的唯一可见证据。
             没有它，「收起不杀」这条口径在界面上就不成立——用户会以为会话没了 */
          <span
            data-testid="home-badge"
            className="absolute -top-0.5 -right-0.5 flex h-[17px] min-w-[17px] items-center justify-center rounded-full bg-[#18a86b] px-1 font-mono text-[10px] leading-none text-white"
          >
            {dock.tabs.length}
          </span>
        )}
      </button>
    </>
  )
}
```

同时：
- 删掉 `useState` 与 `panelOpen`、`openTerminal`、`focusTab`、`tabLabel` 函数
- import 收成 `import type { ReactNode } from 'react'` + `import { Plus } from 'lucide-react'`（`House`/`SquareTerminal`/`X`/`useState` 都不再用）
- 更新文件头注释：删掉「点开是一张小面板…」「面板开合是本地 UI 状态」两段，改成描述三分支的 FAB

- [ ] **Step 4: 确认面板真的没了**

Run: `cd web && grep -rn "panelOpen\|home 基准</\|还没有开过终端" src/ || echo "面板已彻底移除"`
Expected: 打印「面板已彻底移除」。

- [ ] **Step 5: 跑测试与类型检查**

Run: `cd web && npx vitest run src/app/homedock/ && npm run typecheck`
Expected: 全绿、无类型错误（`useState` 等未使用 import 若漏删会在这里报出来）。

- [ ] **Step 6: Commit**

```bash
git add web/src/app/homedock/
git commit -m "fix(web): 悬浮入口一次点击直达终端，删掉重复的中间清单面板（B94①）"
```

---

### Task 4: 轻量 ContextMenu 组件

**Files:**
- Create: `web/src/app/tree/ContextMenu.tsx`
- Test: `web/src/app/tree/ContextMenu.test.tsx`

**Interfaces:**
- Consumes: 无
- Produces:
  ```ts
  export interface ContextMenuItem {
    label: string
    onSelect: () => void
    danger?: boolean // 破坏性动作，文字走 text-destructive
  }
  export function ContextMenu(props: {
    x: number
    y: number
    items: ContextMenuItem[]
    onClose: () => void
  }): ReactNode
  ```
  Task 5 按这个签名调用。

- [ ] **Step 1: 写失败的测试**

新建 `web/src/app/tree/ContextMenu.test.tsx`：

```tsx
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { ContextMenu } from './ContextMenu'

const items = [{ label: '注销', onSelect: vi.fn(), danger: true }]

describe('ContextMenu', () => {
  it('渲染成 menu，项是 menuitem', () => {
    render(<ContextMenu x={10} y={20} items={items} onClose={vi.fn()} />)
    expect(screen.getByRole('menu')).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: '注销' })).toBeInTheDocument()
  })

  it('点菜单项：先执行动作，再关闭', () => {
    const onSelect = vi.fn()
    const onClose = vi.fn()
    render(<ContextMenu x={10} y={20} items={[{ label: '注销', onSelect }]} onClose={onClose} />)
    fireEvent.click(screen.getByRole('menuitem', { name: '注销' }))
    expect(onSelect).toHaveBeenCalledTimes(1)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('按 Esc 关闭', () => {
    const onClose = vi.fn()
    render(<ContextMenu x={10} y={20} items={items} onClose={onClose} />)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('点菜单外关闭，点菜单内不关', () => {
    const onClose = vi.fn()
    render(<ContextMenu x={10} y={20} items={items} onClose={onClose} />)
    fireEvent.pointerDown(screen.getByRole('menu'))
    expect(onClose).not.toHaveBeenCalled()
    fireEvent.pointerDown(document.body)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('贴着视口右/下缘弹出时向内翻转，不被裁掉', () => {
    // why：右键点在窗口右边缘时，菜单从点击点向右展开会有一半在视口外，
    // 而它是 fixed 定位，页面滚动也拉不回来
    Object.defineProperty(window, 'innerWidth', { value: 800, configurable: true })
    Object.defineProperty(window, 'innerHeight', { value: 600, configurable: true })
    render(<ContextMenu x={790} y={590} items={items} onClose={vi.fn()} />)
    const menu = screen.getByRole('menu')
    expect(Number.parseFloat(menu.style.left)).toBeLessThan(790)
    expect(Number.parseFloat(menu.style.top)).toBeLessThan(590)
  })

  it('打开时焦点落到第一项', () => {
    render(<ContextMenu x={10} y={20} items={items} onClose={vi.fn()} />)
    expect(screen.getByRole('menuitem', { name: '注销' })).toHaveFocus()
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/tree/ContextMenu.test.tsx`
Expected: FAIL —— `Failed to resolve import "./ContextMenu"`。

- [ ] **Step 3: 写实现**

新建 `web/src/app/tree/ContextMenu.tsx`：

```tsx
// ContextMenu —— 右键菜单。
//
// 职责：在鼠标位置弹一份菜单项，处理关闭（点项 / 点外部 / Esc）与键盘移动。
//
// 边界：
//   - 不认识菜单项的语义，`onSelect` 干什么由调用方决定；也不做二次确认，
//     破坏性动作的确认弹层归调用方（`danger` 只影响配色）
//   - 不管「同时只能有一个菜单」：那由调用方用一份状态承担，本组件挂载即显示
//
// 为什么自己写而不是引依赖：`components/ui/` 只有 badge/button/card，本仓库
// 至今零处右键菜单。为了一个单项菜单引一整套 dropdown 依赖不划算。
import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { cn } from '@/lib/utils'

export interface ContextMenuItem {
  label: string
  onSelect: () => void
  danger?: boolean
}

export interface ContextMenuProps {
  x: number
  y: number
  items: ContextMenuItem[]
  onClose: () => void
}

export function ContextMenu({ x, y, items, onClose }: ContextMenuProps) {
  const ref = useRef<HTMLDivElement>(null)
  // pos 先用点击坐标，挂载后按实测尺寸向内翻转。
  // 为什么不在渲染前算：菜单宽高取决于最长的那条文案，只有量过才知道
  const [pos, setPos] = useState({ left: x, top: y })

  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const { width, height } = el.getBoundingClientRect()
    // 4px 是贴边留白，纯观感；越界时改成「从点击点向左/上展开」
    setPos({
      left: x + width > window.innerWidth ? Math.max(4, x - width) : x,
      top: y + height > window.innerHeight ? Math.max(4, y - height) : y,
    })
    el.querySelector<HTMLButtonElement>('[role="menuitem"]')?.focus()
  }, [x, y])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation()
        onClose()
      }
    }
    // 捕获阶段：菜单外的任意 pointerdown 都关掉它。菜单内的由下面那句挡住
    const onDown = (e: PointerEvent) => {
      if (ref.current?.contains(e.target as Node)) return
      onClose()
    }
    document.addEventListener('keydown', onKey)
    document.addEventListener('pointerdown', onDown, true)
    return () => {
      document.removeEventListener('keydown', onKey)
      document.removeEventListener('pointerdown', onDown, true)
    }
  }, [onClose])

  return (
    <div
      ref={ref}
      role="menu"
      style={{ left: pos.left, top: pos.top }}
      className="fixed z-50 min-w-32 rounded-md border border-border bg-popover p-1 text-popover-foreground shadow-lg"
    >
      {items.map((it) => (
        <button
          key={it.label}
          type="button"
          role="menuitem"
          onClick={() => {
            // 先执行再关：反过来的话调用方在 onSelect 里 setState 会撞上
            // 本组件正在卸载，React 会警告「更新一个未挂载的组件」
            it.onSelect()
            onClose()
          }}
          className={cn(
            'flex w-full cursor-pointer items-center rounded px-2 py-1.5 text-left text-[12.5px] hover:bg-accent',
            it.danger && 'text-destructive',
          )}
        >
          {it.label}
        </button>
      ))}
    </div>
  )
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/tree/ContextMenu.test.tsx`
Expected: 六条全 PASS。翻转那条若因 jsdom 的 `getBoundingClientRect` 恒返回 0 而失败，在该用例里 `vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect')` 打桩返回 `{ width: 140, height: 40 }` 再断言。

- [ ] **Step 5: Commit**

```bash
git add web/src/app/tree/ContextMenu.tsx web/src/app/tree/ContextMenu.test.tsx
git commit -m "feat(web): 轻量 ContextMenu 组件（B94② 的前置件）"
```

---

### Task 5: 机器行改右键菜单 + 总回归

**Files:**
- Modify: `web/src/app/tree/ProjectTree.tsx:319-353`
- Test: `web/src/app/tree/ProjectTree.test.tsx:363`（既有那条要**替换**）

**Interfaces:**
- Consumes: Task 4 的 `ContextMenu` / `ContextMenuItem`
- Produces: 无对外接口变化。`ProjectTreeProps.onUnregister` 签名不变。

- [ ] **Step 1: 写失败的测试**

`web/src/app/tree/ProjectTree.test.tsx`：**删掉** `it('注销按钮的定位上下文是机器行本身，不是整棵子树', ...)`（那个 absolute 按钮已不存在），**追加**：

```tsx
  it('机器行右端只剩计数，没有常驻的注销按钮压在上面', () => {
    // why：absolute right-2 的注销按钮与同一行右端的 RowCounts 抢位置。
    // 08-14 只修了垂直（定位上下文从 578px 子树收进机器行），水平仍然重叠
    const { container } = render(<ProjectTree {...props({ onUnregister: vi.fn() })} />)
    expect(container.querySelector('[aria-label="注销"]')).toBeNull()
  })

  it('右键机器行弹出菜单，含「注销」', () => {
    const { container } = render(<ProjectTree {...props({ onUnregister: vi.fn() })} />)
    const row = container.querySelectorAll('.group.relative')[0]
    fireEvent.contextMenu(row)
    expect(screen.getByRole('menuitem', { name: '注销' })).toBeInTheDocument()
  })

  it('菜单里点「注销」进既有确认弹层，文案不变', () => {
    const { container } = render(<ProjectTree {...props({ onUnregister: vi.fn() })} />)
    fireEvent.contextMenu(container.querySelectorAll('.group.relative')[0])
    fireEvent.click(screen.getByRole('menuitem', { name: '注销' }))
    expect(screen.getByText(/只解除登记，不删除磁盘上的代码/)).toBeInTheDocument()
  })

  it('未传 onUnregister 时右键不弹菜单——没有可做的操作', () => {
    const { container } = render(<ProjectTree {...props({ onUnregister: undefined })} />)
    fireEvent.contextMenu(container.querySelectorAll('.group.relative')[0])
    expect(screen.queryByRole('menu')).toBeNull()
  })
```

跑之前先确认 `.group.relative` 选到的确实是机器行；`ProjectTree.tsx:319` 就是这个类名组合，若与别处冲突，改用给机器行加一个 `data-testid="machine-row"` 再选（顺带把这个 testid 加进实现）。

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/tree/ProjectTree.test.tsx`
Expected: 四条新用例 FAIL（注销按钮还在、右键无反应）。

- [ ] **Step 3: 改实现**

在 `ProjectTree` 组件顶部加菜单状态：

```tsx
  // 同时只允许一个右键菜单，所以状态挂在树这一层而不是每行一份。
  // null = 没有菜单打开
  const [menu, setMenu] = useState<{ x: number; y: number; name: string; machine: string } | null>(null)
```

机器行那段（`:319-353`）改成：

```tsx
                    {/* 定位上下文收在机器行这一层：右键菜单按鼠标坐标 fixed 定位，
                        不依赖它；但目录行/任务行仍不该进这个分组容器 */}
                    <div
                      className="group relative"
                      data-testid="machine-row"
                      onContextMenu={
                        onUnregister
                          ? (e) => {
                              // 阻止浏览器原生菜单，换成我们这份。
                              // Shift+F10 与 ContextMenu 键也派发这个事件，
                              // 所以键盘用户走的是同一条路，不需要额外快捷键
                              e.preventDefault()
                              setMenu({ x: e.clientX, y: e.clientY, name: loc.name, machine: loc.machine })
                            }
                          : undefined
                      }
                    >
                      <button
                        type="button"
                        /* …既有内容一字不改… */
                      >
                        {/* … */}
                      </button>
                    </div>
```

即：**删掉** `{onUnregister && (<button aria-label="注销" … />)}` 整块，把 `onContextMenu` 挂到外层 `div` 上。

在组件 return 的最外层末尾（与既有确认弹层同级）挂菜单：

```tsx
      {menu && (
        <ContextMenu
          x={menu.x}
          y={menu.y}
          onClose={() => setMenu(null)}
          items={[
            {
              label: '注销',
              danger: true,
              // 走的仍是既有的确认弹层，一字不改——右键只是换了个入口，
              // 不是换一条注销路径
              onSelect: () => setUnregisterTarget({ name: menu.name, machine: menu.machine }),
            },
          ]}
        />
      )}
```

**菜单里不放「编辑」占位**：后端没有改登记的端点（spec §2.2），一个永远点不动的菜单项只会让人反复去点。编辑记在 B95。

- [ ] **Step 4: 加注释说明为什么**

在 `menu` state 声明处已有一句；再在删掉按钮的位置补一句 why（否则下一个人会以为注销功能丢了）：

```tsx
                      {/* 注销入口在右键菜单里，不在行内。
                          行内 absolute 按钮与同一行右端的 RowCounts 抢位置——
                          08-14 修过一次垂直居中（定位上下文从 578px 子树收进本行），
                          但水平方向两者都要右端，改不出不重叠的排法 */}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/tree/`
Expected: 全绿。

- [ ] **Step 6: 总回归**

Run:
```bash
cd web && npx vitest run && npm run typecheck && npx eslint src --max-warnings=10 && npx vite build
```
Expected: 测试全绿、类型无错、eslint 0 error（既有 warning 数量不变，多出来要查）、build 通过。

- [ ] **Step 7: 浏览器实测四条判据**

起 dev server，逐条验 spec §4 的 1–4：

1. 零终端点 FAB 一次 → 浮窗直接带一个终端出现；收起再点 → 回到同一个 tab；浮窗开着点 FAB → 收起
2. 机器行右端只有计数；右键 → 菜单；点注销 → 确认弹层文案不变；Esc / 点外部可关
3. 开一个干净文件 tab → 点 `+` → **焦点留在空白 tab**；脏文件重复一遍，切回去草稿还在
4. 浮窗 `+` 新建 → 点 tab 的 `×` → 确认 → 会话真删、无报错；DevTools 里 `DELETE /api/pty/sessions/{id}` **不带** `machine` 参数

- [ ] **Step 8: Commit**

```bash
git add web/src/app/tree/
git commit -m "fix(web): 项目位置注销改右键菜单，行内按钮不再压住计数（B94②）"
```
