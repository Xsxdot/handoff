# W4 控制台形态修复 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 W4 控制台左栏、看板弹层与文件树上六处与形态基准对不上的地方掰回去，并给整棵树补上原型本来就有的三套配色。

**Architecture:** 纯前端改动。六条各自独立，互不依赖，可以任意顺序做——但每条都要独立可验，所以一条一个 task、一条一次提交。没有任何 Go 侧改动。

**Tech Stack:** React 19 + TypeScript + Tailwind v4 + vitest + @testing-library/react。图标用 lucide-react（项目已依赖，不要引新图标库）。

## Global Constraints

以下每一条都是硬约束，每个 task 隐含包含它们：

- **形态基准是 `prototypes/desktop-console/`**，不是你的审美。拿不准就去看原型源码（`prototypes/desktop-console/src/App.jsx` 与 `styles.css`），不要自由发挥。
- **一行 Go 都不许改。** 交活前跑 `git diff --stat -- '*.go' internal/ cmd/`，必须**无输出**。有输出就是跑偏了。
- **不许改 `web/src/app/workbench/WorkbenchPage.tsx` 的 `renderContent` 签名**，那是另一份计划的地盘。本计划根本不需要碰 workbench 目录。
- **禁止 `console.log` 当日志。** 前端没有结构化 logger，所以本计划的可观测性靠的是**测试覆盖 + 语义化 DOM**（可查询的 `title` / `aria-label` / `data-testid`），不是打印。每个 task 的「注释」step 是强制的；「日志」step 只在真有运行期分支的 task 上出现（Task 5 的哈希取色），其余 task 属于 `instrumenting-code` 明示的豁免情形（纯呈现、无 I/O、无状态变更、无外部调用）——**这是有意的判断，不是漏写**。
- **颜色一律走 CSS 变量 token**，不许写裸 `text-amber-600` / `bg-amber-500` 这类 Tailwind 调色板类名。`web/src/index.css` 里已有一套 `--state-*` token 且**成对定义了深色值**（浅色 :root 第 51-54 行，深色第 95-98 行）；新增的项目色必须照这个样子成对定义，只给浅色值是不合格的。
- **改了组件接口就要改它的测试**，但**不许为了让旧断言变绿而保留旧形态**。断言该按语义查（`getByTitle` / `getByLabelText`），不该按拼好的字符串查。
- **测试里的 `props()` 一律用该测试文件已有的那个工厂。** `ProjectTree.test.tsx` 里已有一个（文件开头有注释说明它返回一套完整可用的 props）。若某个测试文件没有现成工厂，就把 props 内联展开写在用例里——**不要新造一个共享 helper**，本计划不该引入新的测试基础设施。
- 每个 task 结束时前端四条必须全绿：

```bash
cd web && npx vitest run && npm run typecheck && npm run lint && npm run build
```

---

## 文件结构

| 文件 | 动作 | 职责 |
|---|---|---|
| `web/src/app/tree/RowCounts.tsx` | 新建 | 行尾计数的图标形态（从 ProjectTree 里抽出来独立成文件，它现在有了自己的形态规则与三种用法，内联在 500 行的 ProjectTree 里读不清） |
| `web/src/app/tree/RowCounts.test.tsx` | 新建 | 三种行的计数渲染与语义可查性 |
| `web/src/app/tree/projectColor.ts` | 新建 | `project_id → 调色板下标` 的稳定哈希（纯函数） |
| `web/src/app/tree/projectColor.test.ts` | 新建 | 稳定性、与顺序无关、分布 |
| `web/src/app/tree/ProjectTree.tsx` | 修改 | 「项目 N」间距、三段式滚动、注销按钮定位、接入 RowCounts 与项目色、机器行连接态圆点 |
| `web/src/app/tree/ProjectTree.test.tsx` | 修改 | 跟随上面的接口变化 |
| `web/src/app/shell/Shell.tsx` | 修改 | `<aside>` 去掉整体滚动，交给 ProjectTree 自己分段 |
| `web/src/app/overlay/Overlay.tsx` | 修改 | 新增 `tall` 选项：面板固定 70vh |
| `web/src/app/overlay/Overlay.test.tsx` | 修改 | `tall` 的断言 |
| `web/src/app/overlay/BoardOverlay.tsx` | 修改 | 传 `tall` |
| `web/src/app/board/BoardPage.tsx` | 修改 | 四列各自内部滚动、列头不动 |
| `web/src/app/files/FileTree.tsx` | 修改 | 文件图标着蓝、M 标记改用 token |
| `web/src/index.css` | 修改 | 新增项目色 token（浅色 + 深色成对） |

---

## Task 1: 行尾计数改图标形态

**Files:**
- Create: `web/src/app/tree/RowCounts.tsx`
- Create: `web/src/app/tree/RowCounts.test.tsx`
- Modify: `web/src/app/tree/ProjectTree.tsx`（删掉内联的 `RowCounts`，改为 import；三处调用点改传结构化参数）
- Modify: `web/src/app/tree/ProjectTree.test.tsx`（原来断言 `'2·0·0'` 字符串的用例）

**Interfaces:**
- Produces: `RowCounts({ dirs?, running, pending })` —— `dirs` 省略时不渲染目录段。Task 5 之后 ProjectTree 仍按同一签名调用，不受影响。

**背景（不要跳过）：** 现在的 `RowCounts` 收一个已经拼好的字符串 `\`${dirs}·${running}·${pending}\``，渲染成 `2·0·0`。三个数字挤在一起、没有任何东西说明它们各自是什么，语义只藏在整段的 `title` 里。原型 `SummaryCounts`（`prototypes/desktop-console/src/App.jsx:247-255`）是三段「图标 + 数字」，每段自带 `title`。

- [ ] **Step 1: 写失败的测试**

新建 `web/src/app/tree/RowCounts.test.tsx`：

```tsx
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { RowCounts } from './RowCounts'

describe('RowCounts', () => {
  it('项目/机器行：三段各带语义与数字', () => {
    render(<RowCounts dirs={2} running={1} pending={3} />)
    expect(screen.getByTitle('开发目录')).toHaveTextContent('2')
    expect(screen.getByTitle('运行中的 handoff')).toHaveTextContent('1')
    expect(screen.getByTitle('需要处理')).toHaveTextContent('3')
  })

  it('目录行：省略 dirs 时不渲染目录段', () => {
    render(<RowCounts running={1} pending={0} />)
    expect(screen.queryByTitle('开发目录')).toBeNull()
    expect(screen.getByTitle('运行中的 handoff')).toHaveTextContent('1')
    expect(screen.getByTitle('需要处理')).toHaveTextContent('0')
  })

  it('计数为 0 也渲染，不省略', () => {
    // why：0 与「没有这个概念」是两回事。目录数为 0 的项目仍要显示 0，
    // 省略会让人以为数据没取到
    render(<RowCounts dirs={0} running={0} pending={0} />)
    expect(screen.getByTitle('开发目录')).toHaveTextContent('0')
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/tree/RowCounts.test.tsx`
Expected: FAIL，报找不到 `./RowCounts` 模块

- [ ] **Step 3: 写实现**

新建 `web/src/app/tree/RowCounts.tsx`：

```tsx
// RowCounts —— 左栏树里每一行右端的计数。
//
// 形态基准：原型的 SummaryCounts（prototypes/desktop-console/src/App.jsx:247）
// ——三段「图标 + 数字」，等宽字体、次要灰、段间 gap 7px。
//
// 边界：
//   - 只负责呈现。数字怎么算是 counts.ts 的事，这里不查任何任务状态
//   - 不接受预拼好的字符串。改成结构化入参正是本次修复的核心：裸 `2·0·0`
//     没有任何东西说明三个数字各自是什么
import { Activity, Folders, TriangleAlert } from 'lucide-react'

export interface RowCountsProps {
  // dirs 省略 = 这一行没有「目录数」这个概念（目录行本身就是目录，不嵌套统计）。
  // 注意与 dirs={0} 区分：0 是「有这个概念，值为零」，要照常渲染。
  dirs?: number
  running: number
  pending: number
}

export function RowCounts({ dirs, running, pending }: RowCountsProps) {
  return (
    <span className="ml-auto flex shrink-0 items-center gap-[7px] font-mono text-[9.5px] tabular-nums text-muted-foreground">
      {dirs !== undefined && (
        <span title="开发目录" className="flex items-center gap-0.5">
          <Folders className="size-3" />
          {dirs}
        </span>
      )}
      <span title="运行中的 handoff" className="flex items-center gap-0.5">
        <Activity className="size-3" />
        {running}
      </span>
      <span title="需要处理" className="flex items-center gap-0.5">
        <TriangleAlert className="size-3" />
        {pending}
      </span>
    </span>
  )
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/tree/RowCounts.test.tsx`
Expected: PASS（3 个用例）

- [ ] **Step 5: 接进 ProjectTree**

在 `ProjectTree.tsx` 里：

1. 删掉内联的 `RowCounts` 函数（约 74-86 行，含它上面那行注释）。
2. 顶部加 `import { RowCounts } from './RowCounts'`。
3. 三处调用点改成结构化传参：

```tsx
// 项目行（原：text={`${pCounts.dirs}·${pCounts.running}·${pCounts.pending}`}）
<RowCounts dirs={pCounts.dirs} running={pCounts.running} pending={pCounts.pending} />

// 机器行（同上，用 mCounts）
<RowCounts dirs={mCounts.dirs} running={mCounts.running} pending={mCounts.pending} />

// 目录行（原：text={`${under.running}·${under.pending}`}）——不传 dirs
<RowCounts running={under.running} pending={under.pending} />
```

**机器行保留三段，这是对原型的有意偏离。** 原型的机器行只有两段（`SummaryCounts` 收到 `machine` 标记时不渲染 attention）。我们保留待处理段，理由：待处理是「你还欠哪些没答」的信号，机器是任务的实际落点，在这层藏掉等于逼用户展开到目录才看得见。**这条要写进走查记录，验收时不要当成"没对齐原型"。**

- [ ] **Step 6: 修 ProjectTree 的旧断言**

`ProjectTree.test.tsx` 里凡是断言 `'2·0·0'` 这类拼接字符串的用例，改成按语义查：

```tsx
// 改前：expect(screen.getByText('2·0·0')).toBeInTheDocument()
// 改后：
const counts = screen.getAllByTitle('开发目录')
expect(counts[0]).toHaveTextContent('2')
```

**不许为了让旧断言绿而在 RowCounts 里保留裸文本形态。** 旧断言测的是已经被判定为缺陷的形态，它红是对的。

- [ ] **Step 7: 加注释**

- `RowCounts.tsx` 文件头注释已在 Step 3 写好（职责 + 边界 + 形态基准出处）
- `dirs?: number` 上方的「省略 vs 0」注释已在 Step 3 写好——这是本组件唯一的边界条件，必须留
- 在 ProjectTree 机器行的调用点上方补一行 why：

```tsx
{/* 机器行保留三段（原型只有两段）：待处理是「你还欠什么」的信号，
    机器是任务的实际落点，在这层藏掉等于逼人展开到目录才看得见 */}
```

- [ ] **Step 8: 跑全量前端回归**

Run: `cd web && npx vitest run && npm run typecheck && npm run lint && npm run build`
Expected: 全绿。用例总数应当**比改动前多 3 个**（新增的 RowCounts.test.tsx）。

- [ ] **Step 9: 提交**

```bash
git add web/src/app/tree/RowCounts.tsx web/src/app/tree/RowCounts.test.tsx web/src/app/tree/ProjectTree.tsx web/src/app/tree/ProjectTree.test.tsx
git commit -m "fix(web): 行尾计数改回图标形态，三段各自带语义"
```

---

## Task 2: 左栏三段式滚动与「项目 N」间距

**Files:**
- Modify: `web/src/app/shell/Shell.tsx:150`
- Modify: `web/src/app/tree/ProjectTree.tsx`（根容器、标题、底部三入口）
- Modify: `web/src/app/tree/ProjectTree.test.tsx`

**Interfaces:**
- Consumes: 无
- Produces: 无新接口，纯布局

**背景（含实测数据）：** `<aside>` 现在是 `overflow-y-auto`，实测 `scrollHeight` 1137 > `clientHeight` 1024，「添加项目」按钮的 `getBoundingClientRect().top` 是 **1100**——它在滚动内容的最下面，项目一多就被推出视野。原型是三段式（`prototypes/desktop-console/src/styles.css:79-86, 190-195, 400-407`）：外层 flex 列自身不滚，只有中间的树滚，footer 钉在底部。

另外 `ProjectTree.tsx:273-274` 渲染 `<span>项目</span><span>{count}</span>`，两个 span 之间没有任何间隔，浏览器里就是「项目2」。

- [ ] **Step 1: 写失败的测试**

在 `ProjectTree.test.tsx` 里加：

```tsx
it('「项目 N」的标签与数字之间有间隔，数字更浅', () => {
  render(<ProjectTree {...props()} />)
  const count = screen.getByTestId('project-count')
  // 数字与标签必须是两个可区分的元素，且数字带独立的浅色类
  expect(count.className).toMatch(/text-muted-foreground|opacity/)
  // 间隔靠父容器的 gap 或数字自身的 margin，两者取一即可
  const parent = count.parentElement!
  expect(parent.className + count.className).toMatch(/gap-|ml-/)
})

it('树独立滚动，底部入口不在滚动区内', () => {
  const { container } = render(<ProjectTree {...props()} />)
  const scroller = container.querySelector('[data-testid="tree-scroll"]')!
  expect(scroller.className).toMatch(/overflow-y-auto/)
  expect(scroller.className).toMatch(/min-h-0/) // 缺这句 overflow 在 flex 子项里不生效
  // 「添加项目」必须在滚动容器之外
  const addBtn = screen.getByRole('button', { name: /添加项目/ })
  expect(scroller.contains(addBtn)).toBe(false)
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/tree/ProjectTree.test.tsx`
Expected: FAIL——找不到 `tree-scroll`，且间隔断言不通过

- [ ] **Step 3: 改 Shell 的 aside**

`Shell.tsx:150`，去掉 `overflow-y-auto`：

```tsx
{/* 左栏自身不滚：滚动交给 ProjectTree 内部的树区，好让底部入口钉在底部。
    min-h-0 是必须的——flex 子项默认 min-height:auto，缺它内部的
    overflow-y-auto 不会生效，树会把父容器撑高、footer 照样被顶出去 */}
<aside role="complementary" className="flex min-h-0 w-[260px] shrink-0 flex-col border-r bg-sidebar">
```

- [ ] **Step 4: 改 ProjectTree 的三段式**

根容器从 `<div className="py-2">` 改为占满高度的 flex 列，并把树的部分包进滚动容器：

```tsx
<div className="flex min-h-0 flex-1 flex-col py-2">
  {/* 第一段：不滚——任务看板入口 + 搜索框 + 「项目 N」 */}
  ...任务看板按钮、搜索框、标题原样...

  {/* 第二段：只有它滚 */}
  <div data-testid="tree-scroll" className="min-h-0 flex-1 overflow-y-auto">
    ...filtered.projects.map(...) 与「未归属」分组、空态提示原样搬进来...
  </div>

  {/* 第三段：钉在底部 */}
  <div className="mt-1 flex items-center gap-1 border-t px-2 pt-2">
    ...添加项目 / 工单 / 设置三入口原样...
  </div>
</div>
```

**只搬结构，不改任何一行行内容。** 项目/机器/目录/任务的渲染逻辑一个字都不动。

- [ ] **Step 5: 修「项目 N」的间距**

`ProjectTree.tsx:272-275`：

```tsx
{/* 数字紧跟标签、比标签浅一档——形态基准是原型的
    .sidebar-section-title span { margin-left:3px; color:#969696; font-weight:500 } */}
<div className="flex items-center gap-1 px-3 pb-1 pt-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
  <span>项目</span>
  <span data-testid="project-count" className="font-normal text-muted-foreground/70">
    {filtered.projectCount}
  </span>
</div>
```

- [ ] **Step 6: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/tree/ProjectTree.test.tsx`
Expected: PASS

- [ ] **Step 7: 加注释**

Step 3 与 Step 5 的 why 注释已在代码块里。另外在 ProjectTree 根容器上方补文件级说明：

```tsx
{/* 三段式：顶部（导航+搜索+标题）不滚 · 中间树独滚 · 底部入口钉死。
    为什么不让整个 aside 滚：项目一多，「添加项目」会被推到 scrollHeight
    的最下面（实测 top:1100 / 视口 1024），要滚到底才找得到入口 */}
```

- [ ] **Step 8: 全量回归**

Run: `cd web && npx vitest run && npm run typecheck && npm run lint && npm run build`
Expected: 全绿

- [ ] **Step 9: 提交**

```bash
git add web/src/app/shell/Shell.tsx web/src/app/tree/ProjectTree.tsx web/src/app/tree/ProjectTree.test.tsx
git commit -m "fix(web): 左栏改三段式，树独滚、底部入口钉底；补回「项目 N」间距"
```

---

## Task 3: 注销按钮的定位上下文

**Files:**
- Modify: `web/src/app/tree/ProjectTree.tsx:308`（`<div key={mKey} className="group relative">`）与 :328-334（按钮）
- Modify: `web/src/app/tree/ProjectTree.test.tsx`

**Interfaces:** 无变化

**根因（已浏览器实测）：** 注销按钮的类名是 `absolute right-2 top-1/2 hidden -translate-y-1/2 … group-hover:inline-flex`。它靠最近的定位祖先决定位置，而带 `group relative` 的那个 `<div key={mKey}>` 包的是**整台机器的整个子树**（机器行 + 它下面所有目录行 + 任务行）。实测该块高 **578px**，于是 `top-1/2` 把按钮放到了 289px 处——视觉上一个删除按钮凭空浮在列表中间，不挨着任何一行。

**改法：把定位上下文收到机器行本身。**

- [ ] **Step 1: 写失败的测试**

```tsx
it('注销按钮的定位上下文是机器行本身，不是整棵子树', () => {
  const { container } = render(<ProjectTree {...props({ onUnregister: vi.fn() })} />)
  const btn = container.querySelector('[aria-label="注销"]')!
  // 最近的 relative 祖先必须是机器行那一层，而不是包着子树的外层 div
  const posParent = btn.closest('.relative')!
  // 机器行内部不含目录行/任务行——用「不包含展开出来的目录行」来钉住这一点
  expect(posParent.querySelector('[data-testid="workspace-row"]')).toBeNull()
})
```

（若 `ProjectTree` 现在没给目录行 `data-testid`，本 step 一并加上 `data-testid="workspace-row"`——它同时是本用例的判据。）

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/tree/ProjectTree.test.tsx -t 注销按钮的定位上下文`
Expected: FAIL——`posParent` 里能查到目录行

- [ ] **Step 3: 改定位上下文**

把 `group relative` 从子树外层挪到机器行的直接包裹层：

```tsx
{/* 外层只负责分组，不再是定位祖先 */}
<div key={mKey}>
  {/* 定位上下文收在机器行这一层：注销的对象是「项目在这台机器上的位置」，
      按钮必须长在它作用的那一行上。挂在外层时 top-1/2 会以整棵子树
      （实测 578px）为基准，把按钮放到列表正中间 */}
  <div className="group relative">
    <button ...机器行原样... />
    {onUnregister && (
      <button
        type="button"
        aria-label="注销"
        onClick={...原样...}
        className="absolute right-2 top-1/2 hidden -translate-y-1/2 rounded p-1 text-muted-foreground group-hover:inline-flex hover:text-destructive"
      >
        ...原样...
      </button>
    )}
  </div>
  {/* 目录行、任务行留在外层，不进定位上下文 */}
  {mOpen && ...原样...}
</div>
```

**注意：** 如果原来的 `group` 还承担了别的 hover 效果（例如整块背景变化），挪之前先确认；本次只挪定位相关的两个类，不要把别的 hover 弄丢。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/tree/ProjectTree.test.tsx`
Expected: PASS

- [ ] **Step 5: 加注释**

Step 3 代码块里的两段 why 注释即为本 step 的产出，确认它们留在最终代码里。

- [ ] **Step 6: 全量回归 + 提交**

```bash
cd web && npx vitest run && npm run typecheck && npm run lint && npm run build
```

```bash
git add web/src/app/tree/ProjectTree.tsx web/src/app/tree/ProjectTree.test.tsx
git commit -m "fix(web): 注销按钮定位上下文收到机器行，不再浮在子树正中"
```

---

## Task 4: 看板弹层固定 70vh

**Files:**
- Modify: `web/src/app/overlay/Overlay.tsx`（新增 `tall` 选项）
- Modify: `web/src/app/overlay/Overlay.test.tsx`
- Modify: `web/src/app/overlay/BoardOverlay.tsx`（传 `tall`）
- Modify: `web/src/app/board/BoardPage.tsx`（列内滚动）

**Interfaces:**
- Produces: `OverlayProps.tall?: boolean` —— 面板高度固定 70vh 而非贴合内容。工单弹层不传，行为不变。

**背景：** `Overlay.tsx` 的面板是 `max-h-full`，没有下界，所以高度贴着内容走。零任务时四列缩成薄薄一条，每次打开尺寸都不一样。原型的看板是顶层页（没有弹层这个形态），所以高度是本轮现定的：**固定 70vh，四列各自内部滚动，列头不动。**

- [ ] **Step 1: 写失败的测试**

`Overlay.test.tsx` 加：

```tsx
it('tall 时面板高度固定，不随内容伸缩', () => {
  render(<Overlay title="x" onClose={() => {}} tall><p>短内容</p></Overlay>)
  const panel = screen.getByRole('dialog')
  expect(panel.className).toMatch(/h-\[70vh\]/)
})

it('不传 tall 时保持贴合内容（工单弹层的既有行为）', () => {
  render(<Overlay title="x" onClose={() => {}}><p>短内容</p></Overlay>)
  expect(screen.getByRole('dialog').className).not.toMatch(/h-\[70vh\]/)
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/overlay/Overlay.test.tsx`
Expected: FAIL

- [ ] **Step 3: 给 Overlay 加 tall**

```tsx
export interface OverlayProps {
  title: string
  onClose: () => void
  children: ReactNode
  // wide 给看板用：四列横排需要更宽的面板
  wide?: boolean
  // tall 给看板用：高度固定 70vh，不随任务数伸缩。
  // why：看板是"扫一眼就走"的总览，每次打开尺寸都不一样会让人重新找位置；
  // 零任务时贴合内容还会缩成一条，看着像出错了。工单弹层不需要——它的
  // 内容是一份长度有意义的清单，贴合内容反而是对的
  tall?: boolean
}
```

面板类名：

```tsx
className={cn(
  'relative flex max-h-full w-full flex-col rounded-lg border bg-background shadow-xl outline-none',
  wide ? 'max-w-6xl' : 'max-w-3xl',
  tall && 'h-[70vh]',
)}
```

- [ ] **Step 4: BoardOverlay 传 tall**

`BoardOverlay.tsx` 里给 `<Overlay ... wide>` 加上 `tall`。

- [ ] **Step 5: 四列各自滚动**

`BoardPage.tsx`：

1. 列容器（`<section className="flex w-64 shrink-0 flex-col rounded-lg border bg-background/60">`）加 `min-h-0`：

```tsx
<section className="flex min-h-0 w-64 shrink-0 flex-col rounded-lg border bg-background/60">
```

2. 列内容区加滚动（原 `<div className="flex flex-1 flex-col gap-2 p-2">`）：

```tsx
{/* 列头在滚动区之外：列名与计数要始终可见，否则滚到一半就不知道这是哪一列 */}
<div className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto p-2">
```

3. 包着四列的那一行容器（`BoardPage` 里 `<main>` 下的列排容器）加 `min-h-0 flex-1`，让列拿得到高度。若 `<main className="flex w-full flex-col gap-3 p-3">` 是最外层，改成 `flex h-full w-full min-h-0 flex-col gap-3 p-3`。

- [ ] **Step 6: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/overlay/ src/app/board/`
Expected: PASS

- [ ] **Step 7: 加注释**

Step 3 的 `tall` 注释与 Step 5 的列头注释即为产出，确认留在最终代码里。

- [ ] **Step 8: 全量回归 + 提交**

```bash
cd web && npx vitest run && npm run typecheck && npm run lint && npm run build
```

```bash
git add web/src/app/overlay/ web/src/app/board/BoardPage.tsx
git commit -m "feat(web): 看板弹层固定 70vh，四列各自内部滚动"
```

---

## Task 5: 项目色（稳定哈希取色）

**Files:**
- Create: `web/src/app/tree/projectColor.ts`
- Create: `web/src/app/tree/projectColor.test.ts`
- Modify: `web/src/index.css`（新增 5 组项目色 token，浅色 + 深色成对）
- Modify: `web/src/app/tree/ProjectTree.tsx`（项目行图标着色）

**Interfaces:**
- Produces: `projectColorClass(projectId: string): string` —— 返回一个 Tailwind 文字色类名（如 `'text-project-3'`），供项目行图标使用。

**背景与裁决：** 原型给每个项目一个固定色画在项目行图标上（`<Boxes color={project.color} />`），但**真实 API 里没有颜色字段**（`api/types.ts` 的 `ProjectNode` 查无此项）。spec §6.1 裁决走「前端按 `project_id` 稳定哈希取色」，不加后端字段。

**这一条有运行期分支，所以它是本计划里唯一带可观测性要求的 task**（见 Step 6）。

- [ ] **Step 1: 写失败的测试**

新建 `web/src/app/tree/projectColor.test.ts`：

```ts
import { describe, expect, it } from 'vitest'
import { PROJECT_COLOR_COUNT, projectColorClass } from './projectColor'

describe('projectColorClass', () => {
  it('同一个 id 永远同一色', () => {
    const a = projectColorClass('proj-handoff')
    const b = projectColorClass('proj-handoff')
    expect(a).toBe(b)
  })

  it('与列表顺序无关——插入新项目不会让已有项目换色', () => {
    // why：这是本函数存在的核心理由。按数组下标取色的话，
    // 在列表头部插入一个项目会让后面所有项目集体换色，用户只会当成 bug
    const before = ['p1', 'p2', 'p3'].map(projectColorClass)
    const after = ['p0', 'p1', 'p2', 'p3'].map(projectColorClass)
    expect(after.slice(1)).toEqual(before)
  })

  it('返回值落在调色板内', () => {
    for (const id of ['a', 'b', 'c', 'x-y-z', '中文项目', '']) {
      const cls = projectColorClass(id)
      const idx = Number(cls.replace('text-project-', ''))
      expect(idx).toBeGreaterThanOrEqual(1)
      expect(idx).toBeLessThanOrEqual(PROJECT_COLOR_COUNT)
    }
  })

  it('不同 id 会用到多于一个色（不是所有东西都撞成同一色）', () => {
    const ids = Array.from({ length: 30 }, (_, i) => `proj-${i}`)
    expect(new Set(ids.map(projectColorClass)).size).toBeGreaterThan(1)
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/tree/projectColor.test.ts`
Expected: FAIL，找不到模块

- [ ] **Step 3: 写实现**

新建 `web/src/app/tree/projectColor.ts`：

```ts
// projectColor —— 项目身份色的取色规则。
//
// 为什么是哈希而不是后端字段：真实 API 的 ProjectNode 上没有颜色字段
// （原型的 project.color 是 mock 数据）。为了配色去加一个后端字段 +
// 存储 + 设置 UI 不划算，所以由前端从固定调色板里按项目 id 取一色。
//
// 边界：
//   - **绝不能按数组下标取色**。下标取色时，在列表头部插入一个新项目会让
//     后面所有项目集体换色——用户只会当成 bug。所以取色必须只依赖 id 本身
//   - 不保证不撞色。项目数超过调色板容量必然有重复，这是可接受的：
//     色是辅助识别，不是唯一标识，项目名才是

// CLASSES 必须是**字面量**数组，不能用模板字符串拼类名。
// 两个理由，缺一不可：
//   1. Tailwind v4 按需产出——拼出来的类名构建器扫不到，产物 CSS 里根本没有
//      这条规则，颜色会**静默失效**（不报错，就是没颜色）
//   2. 顺手消掉越界：下标只能落在数组内
// 改这里必须同步改 index.css 的 --project-N，两边组数要对齐。
const CLASSES = [
  'text-project-1',
  'text-project-2',
  'text-project-3',
  'text-project-4',
  'text-project-5',
] as const

// PROJECT_COLOR_COUNT 是调色板容量，供测试与调用方判断。
export const PROJECT_COLOR_COUNT = CLASSES.length

// hash 用 FNV-1a：短、无依赖、对短字符串分布够用。
// 取 >>> 0 是为了让结果落在无符号 32 位，避免负数取模得到负下标。
function hash(input: string): number {
  let h = 0x811c9dc5
  for (let i = 0; i < input.length; i++) {
    h ^= input.charCodeAt(i)
    h = Math.imul(h, 0x01000193)
  }
  return h >>> 0
}

// projectColorClass 返回项目行图标该用的文字色类名。
// 参数：projectId —— 项目的稳定标识（不是名字，改名不该换色）
// 返回：形如 'text-project-3' 的 Tailwind 类名
export function projectColorClass(projectId: string): string {
  return CLASSES[hash(projectId) % CLASSES.length]
}
```

- [ ] **Step 4: 加 token（浅色 + 深色成对）**

`web/src/index.css`。在 `--state-*` 那一组旁边加：

```css
/* 项目身份色。取自原型 projectRows 的五个色值。
   深色一组不是浅色的机械换算：同一个 hex 在深底上对比度会掉，
   所以整体提亮并略降饱和——与 --state-* 那一组的做法一致 */
--project-1: oklch(0.606 0.233 303.9);  /* ≈ #9b5de5 紫 */
--project-2: oklch(0.665 0.124 240.5);  /* ≈ #4aa3df 蓝 */
--project-3: oklch(0.686 0.140 152.2);  /* ≈ #44b678 绿 */
--project-4: oklch(0.652 0.150 260.8);  /* ≈ #619df0 靛 */
--project-5: oklch(0.240 0.000 0);      /* ≈ #171717 近黑 */
```

深色块（与 `--state-*` 的深色定义同一处）：

```css
--project-1: oklch(0.720 0.180 303.9);
--project-2: oklch(0.760 0.105 240.5);
--project-3: oklch(0.775 0.120 152.2);
--project-4: oklch(0.745 0.125 260.8);
--project-5: oklch(0.880 0.000 0);      /* 近黑在深底上必须翻成近白 */
```

Tailwind v4 的 `@theme` 映射段（跟 `--color-state-*` 同一处）加：

```css
--color-project-1: var(--project-1);
--color-project-2: var(--project-2);
--color-project-3: var(--project-3);
--color-project-4: var(--project-4);
--color-project-5: var(--project-5);
```

**这一步做完后必须验证 Tailwind 真的产出了这五条规则**，验证放在 Step 9。Step 3 的 `CLASSES` 已经是字面量数组正是为了这个——类名一旦是拼出来的，构建器扫不到，颜色会静默失效。

- [ ] **Step 5: 接进项目行**

`ProjectTree.tsx` 项目行的图标：

```tsx
{/* 项目身份色：让整棵树不至于只有一个灰。取色只依赖 project_id，
    与列表顺序无关（见 projectColor.ts 的边界说明） */}
<FolderGit2 className={cn('size-4 shrink-0', projectColorClass(project.project_id))} />
```

- [ ] **Step 6: 可观测性——让取色结果可查**

前端没有结构化 logger，`console.log` 是禁止的。本 task 的可观测性通过**语义化 DOM** 落地：给项目行图标加 `data-project-color`，让"某个项目现在是几号色"能被测试与浏览器直接查到，而不是只能靠肉眼比对：

```tsx
<FolderGit2
  data-project-color={projectColorClass(project.project_id)}
  className={cn('size-4 shrink-0', projectColorClass(project.project_id))}
/>
```

并在 `ProjectTree.test.tsx` 加一条：

```tsx
it('项目图标带取色标记，同名项目刷新后同色', () => {
  const { unmount } = render(<ProjectTree {...props()} />)
  const first = document.querySelector('[data-project-color]')!.getAttribute('data-project-color')
  unmount()
  render(<ProjectTree {...props()} />)
  expect(document.querySelector('[data-project-color]')!.getAttribute('data-project-color')).toBe(first)
})
```

- [ ] **Step 7: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/tree/`
Expected: PASS

- [ ] **Step 8: 加注释**

Step 3、4、5 的 why 注释即为产出。另外确认 `PROJECT_COLOR_COUNT`（或 `CLASSES`）上方那句「改这里必须同步改 index.css」留着——它是这个模块最容易踩的坑。

- [ ] **Step 9: 构建验证（这一步不能省）**

Run: `cd web && npm run build`

构建完成后**确认产出的 CSS 里真的有这五个类**：

```bash
grep -o "text-project-[1-5]" web/dist/assets/*.css | sort -u
```

Expected: 五个都在。**少任何一个说明 Tailwind 没扫到该类名**，颜色会静默失效——回到 Step 4 检查字面量映射。

- [ ] **Step 10: 全量回归 + 提交**

```bash
cd web && npx vitest run && npm run typecheck && npm run lint && npm run build
```

```bash
git add web/src/app/tree/projectColor.ts web/src/app/tree/projectColor.test.ts web/src/index.css web/src/app/tree/ProjectTree.tsx web/src/app/tree/ProjectTree.test.tsx
git commit -m "feat(web): 项目行按 project_id 稳定哈希取身份色"
```

---

## Task 6: 文件树配色与机器行连接态

**Files:**
- Modify: `web/src/app/files/FileTree.tsx`（文件图标着蓝、M 标记改 token）
- Modify: `web/src/app/files/FileTree.test.tsx`
- Modify: `web/src/index.css`（新增 `--file-accent` token，浅色 + 深色）
- Modify: `web/src/app/tree/ProjectTree.tsx`（机器行连接态圆点）

**Interfaces:** 无新接口

**背景：** 原型 `.file-row > svg:nth-of-type(2) { color: var(--blue) }`——**文件**图标着蓝、**文件夹**保持灰，这个对比正是右栏不显得平的原因。实现里两者都是 `text-muted-foreground`。

另外 `FileTree.tsx:190` 的 M 标记用的是裸 `text-amber-600`，而同一个仓库的 `ProjectTree.tsx` 自己写着「角标用状态 token 而非裸 bg-amber-500：同一个左栏里两种橙看起来像 bug」——**这条规矩它自己没守住**，一并收掉。

机器行目前没有任何连接态指示，而 `StateDot` 组件与 `--state-*` token 都已存在（B75 那期建的），接上即可，不要新造。

- [ ] **Step 1: 写失败的测试**

`FileTree.test.tsx` 加：

```tsx
it('文件图标着强调色，文件夹图标保持次要灰', () => {
  const { container } = render(<FileTree {...props()} />)
  const fileIcon = container.querySelector('[data-testid="file-icon"]')!
  const dirIcon = container.querySelector('[data-testid="dir-icon"]')!
  expect(fileIcon.getAttribute('class')).toMatch(/text-file-accent/)
  expect(dirIcon.getAttribute('class')).toMatch(/text-muted-foreground/)
})

it('M 标记用状态 token，不用裸 Tailwind 调色板类', () => {
  const { container } = render(<FileTree {...props({ changed: ['a.go'] })} />)
  const mark = screen.getByText('M')
  expect(mark.className).toMatch(/text-state-intervention-text/)
  expect(mark.className).not.toMatch(/amber-\d/)
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/files/FileTree.test.tsx`
Expected: FAIL

- [ ] **Step 3: 加 file accent token**

`index.css`，浅色：

```css
--file-accent: oklch(0.596 0.135 245.6);  /* ≈ #2187d7，原型的 --blue */
```

深色：

```css
--file-accent: oklch(0.720 0.115 245.6);
```

`@theme` 映射：

```css
--color-file-accent: var(--file-accent);
```

- [ ] **Step 4: 改 FileTree**

文件夹图标（两处，`FileTree.tsx:169` 与 :171）保持 `text-muted-foreground`，只加 testid：

```tsx
{open ? (
  <FolderOpen data-testid="dir-icon" className="size-3.5 shrink-0 text-muted-foreground" />
) : (
  <FolderClosed data-testid="dir-icon" className="size-3.5 shrink-0 text-muted-foreground" />
)}
```

文件图标（:187）着蓝：

```tsx
{/* 文件着强调色、文件夹保持灰——原型正是靠这个对比让右栏不显得平
    （.file-row > svg:nth-of-type(2) { color: var(--blue) }） */}
<File data-testid="file-icon" className="ml-3 size-3.5 shrink-0 text-file-accent" />
```

M 标记（:189-192）改 token：

```tsx
{/* 用状态 token 而非裸 amber：同一个界面里两种橙看起来像 bug
    ——这条规矩 ProjectTree 的工单角标已经在守，这里补齐 */}
<span title={CHANGED_TITLE} className="ml-auto shrink-0 font-mono text-[10px] text-state-intervention-text">
  M
</span>
```

- [ ] **Step 5: 机器行接上连接态圆点**

`ProjectTree.tsx` 机器行，在机器图标之后加一个圆点。**复用已有的 `StateDot`**，不要新造组件；不可达的判据用文件里已有的 `probe_error`（:65 已有取值逻辑）：

```tsx
{/* 连接态用与任务状态同一套圆点：一个界面里两套"绿点"含义不同会更糊涂。
    probe_error 非空 = 这个位置探测失败 = 机器当前不可达 */}
<StateDot tone={loc.probe_error !== '' ? 'failed' : 'active'} />
```

并在 `ProjectTree.test.tsx` 加一条：探测失败的位置渲染 failed 基调的圆点。

- [ ] **Step 6: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/files/ src/app/tree/`
Expected: PASS

- [ ] **Step 7: 加注释**

Step 4、5 的 why 注释即为产出。

- [ ] **Step 8: 构建验证**

```bash
cd web && npm run build && grep -o "text-file-accent" web/dist/assets/*.css | head -1
```

Expected: 有命中。没有就是 Tailwind 没扫到，颜色会静默失效。

- [ ] **Step 9: 全量回归 + 提交**

```bash
cd web && npx vitest run && npm run typecheck && npm run lint && npm run build
```

```bash
git add web/src/app/files/ web/src/index.css web/src/app/tree/ProjectTree.tsx web/src/app/tree/ProjectTree.test.tsx
git commit -m "feat(web): 文件图标着强调色、M 标记改状态 token、机器行补连接态圆点"
```

---

## Task 7: 走查记录与收口

**Files:**
- Create: `docs/superpowers/notes/2026-08-14-w4-console-form-fixes-check.md`

**这个 task 不改代码。** 它把 spec §8 的八条验收逐条落成一份可查的记录。

- [ ] **Step 1: 建走查记录骨架**

八条逐条列出，每条写「判据 / 结果 / 背书用例或证据」。**你没有浏览器，所以凡是要肉眼看页面的条目一律如实写「未验（无浏览器）」，并列出替它把关的自动化用例。绝对不许猜通过。**

需要肉眼看的（照实标未验）：条 3（hover 出现位置）、条 5（零任务时弹层高度）、条 6（项目色不撞、刷新不变的实际观感）、条 7（蓝/灰对比）。

能自动化背书的（写出用例名）：条 1、2、4、8。

- [ ] **Step 2: 记下那条有意偏离**

单开一节写清楚：**机器行保留三段计数，原型只有两段**。写明理由（待处理是"你还欠什么"的信号，机器是任务的落点）。这条不写进去，下次走查的人会把它当成没对齐。

- [ ] **Step 3: 贴回归原文**

把四条前端命令的**实际输出**贴进去（用例数、通过数）。不要只写"全绿"。同时贴 `git diff --stat -- '*.go' internal/ cmd/` 的输出证明它是空的。

- [ ] **Step 4: 提交**

```bash
git add docs/superpowers/notes/2026-08-14-w4-console-form-fixes-check.md
git commit -m "docs(notes): W4 控制台形态修复走查记录"
```

---

## 收尾自检

全部 task 完成后逐项确认：

- [ ] 六条形态问题各有一个提交，提交信息说清做了什么
- [ ] `git diff --stat -- '*.go' internal/ cmd/` **无输出**
- [ ] `git diff --stat -- web/src/app/workbench/` **无输出**（本计划不该碰 workbench）
- [ ] 前端四条全绿，且用例总数比开工前**多**（新增 RowCounts / projectColor 两个测试文件）
- [ ] 构建产物里 `text-project-1..5` 与 `text-file-accent` 都能 grep 到
- [ ] 新建的三个文件都有文件头注释（职责 + 边界）
- [ ] 没有任何 `console.log`：`grep -rn "console\." web/src --include=*.ts --include=*.tsx | grep -v test` 无输出
- [ ] 没有裸 Tailwind 调色板类名残留：`grep -rn "amber-\|text-blue-\|bg-green-" web/src/app` 无输出
- [ ] 走查记录里，未验的条目如实标未验，有意偏离单列一节
