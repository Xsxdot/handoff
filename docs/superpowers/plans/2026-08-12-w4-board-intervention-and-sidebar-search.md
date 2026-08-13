# W4 看板干预态标记 + 左栏搜索 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给看板卡片补上原型要求的干预态琥珀标记（并消掉 `waiting_answer` 卡片上重复渲染的第二个红徽章），给左栏补上搜索框与「项目 N」小标题。

**Architecture:** 纯前端改动，分三段。① 先在 `index.css` 建四个状态语义 token，收敛现有三种各写各的裸琥珀写法；② 把 `columns.ts` 里被看板与详情页共用的 `stateBadgeVariant` 拆成 `stateTone`（看板卡片用「圆点 + 文字」）与 `stateBadgeVariant`（详情页保留胶囊 Badge），看板卡片按原型形态重做；③ 左栏搜索的过滤算法抽成纯函数 `tree/search.ts`，`ProjectTree.tsx` 只管状态与渲染。

**Tech Stack:** React 19 + TypeScript + Tailwind CSS v4（`@theme inline` token）+ shadcn/ui（cva 变体）+ vitest + @testing-library/react。

**Spec:** [2026-08-12-w4-board-intervention-and-sidebar-search-design.md](../specs/2026-08-12-w4-board-intervention-and-sidebar-search-design.md)
**Backlog:** B75（看板干预态 + 重复徽章）、B74（左栏搜索），两条合用上面这一份 spec，均已在 `🔨 doing`。

## Global Constraints

- **工作目录是 `web/`。** 所有 `npx vitest` / `npm run` 命令都在 `web/` 下执行。
- **不引入任何新依赖。** 不装 `cmdk`，不装 icon 包之外的东西（`lucide-react` 已在依赖里）。
- **不碰这些文件**（与 PTY 并行线的红线，见 [交接文档](../notes/2026-08-12-w4-parallel-handoff.md) §2）：`web/src/app/shell/Shell.tsx`、`web/src/app/workbench/` 下的任何文件（**尤其不要动 `WorkbenchPage.tsx` 的 `renderContent` 签名**）、任何 Go 文件、`internal/proto/`、`web/src/api/`。本计划全部改动都在 `web/src/index.css`、`web/src/components/ui/badge.tsx`、`web/src/app/board/`、`web/src/app/tree/` 之内。
- **不重生成契约 fixture。** 本计划不动契约，`web/src/api/testdata/*.json` 一个字都不改。
- **颜色一律走 token 工具类**（`bg-state-intervention` / `text-state-intervention-text` / `border-state-intervention`），**禁止** `bg-[var(--state-intervention)]` 这类任意值写法。唯一例外是左侧竖条的 `box-shadow`（无对应工具类），写作 `shadow-[inset_3px_0_var(--state-intervention)]`。
- **前端没有日志层。** `web/src/` 生产代码中 `console.*` 零命中，本期不引入。`instrumenting-code` 的义务在这里由**意图注释 + 测试**兑现（spec §7 的显式立场）。**禁止用 `console.log` 充当日志。** 每个实现任务都带一个「加意图注释」步骤，写的是**为什么**而不是做了什么。
- **提交粒度**：一个 task 一个提交，中文提交信息。
- **每个 task 结束前** `npx vitest run` 必须全绿。

---

### Task 1: 状态语义 token + Badge 的 intervention 变体

**Files:**
- Modify: `web/src/index.css`（`:root` / `.dark` / `@theme inline` 三段）
- Modify: `web/src/components/ui/badge.tsx:10-18`
- Test: `web/src/components/ui/badge.test.tsx`（新建）

**Interfaces:**
- Consumes: 无（第一个 task）
- Produces: CSS 工具类 `bg-state-active` / `bg-state-intervention` / `text-state-intervention-text` / `bg-state-failed` / `text-state-failed` / `border-state-intervention`；`<Badge variant="intervention">` 变体。后续 Task 3、4、8 依赖这些工具类，Task 2 的 `stateBadgeVariant` 返回值 `'intervention'` 依赖这个变体存在。

- [ ] **Step 1: 写失败的测试**

创建 `web/src/components/ui/badge.test.tsx`：

```tsx
// Badge intervention 变体的契约测试。
//
// 为什么要单独钉这个变体：它是任务详情页顶栏干预态（等你答复 / Review）的
// 唯一视觉出口，类名一旦改错，页面不会报错、只会静默变回灰色。
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Badge } from './badge'

describe('Badge', () => {
  it('intervention 变体用琥珀实色背景 + 白字', () => {
    render(<Badge variant="intervention">等你答复</Badge>)
    const el = screen.getByText('等你答复')
    expect(el.className).toContain('bg-state-intervention')
    expect(el.className).toContain('text-white')
  })

  it('既有四档保持不变', () => {
    const { rerender } = render(<Badge variant="destructive">失败</Badge>)
    expect(screen.getByText('失败').className).toContain('bg-destructive')
    rerender(<Badge variant="outline">Review</Badge>)
    expect(screen.getByText('Review').className).toContain('text-foreground')
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/components/ui/badge.test.tsx`
Expected: FAIL —— TypeScript 报 `variant="intervention"` 不在联合类型里，或断言 `bg-state-intervention` 找不到。

- [ ] **Step 3: 在 `index.css` 加四个 token**

在 `:root` 块内，`--font-sans` 之前加：

```css
  /* 状态色板：取自形态基准 prototypes/desktop-console/src/styles.css 的
     --green / --amber / --red。intervention 单独给一个文字色，是因为
     #e79b18 当 7px 圆点够醒目，当小字对白底只有约 2.4:1，读不清 */
  --state-active: oklch(0.653 0.144 158.5);         /* ≈ #18a86b */
  --state-intervention: oklch(0.735 0.153 68.5);    /* ≈ #e79b18 */
  --state-intervention-text: oklch(0.565 0.117 65.8); /* ≈ #a66c09 */
  --state-failed: oklch(0.638 0.163 24.8);          /* ≈ #df554f */
```

在 `.dark` 块内，`--sidebar-ring` 之后加（暗色背景下整体提亮，文字色尤其要够亮才够 4.5:1）：

```css
  /* 暗色档：同色相提亮。本轮不做主题切换（见文件顶部注释），但 shadcn 契约
     要求 :root 与 .dark 成对，缺一档将来开主题切换时会静默塌成浅色值 */
  --state-active: oklch(0.745 0.152 158.5);
  --state-intervention: oklch(0.812 0.146 72.0);
  --state-intervention-text: oklch(0.845 0.132 76.0);
  --state-failed: oklch(0.715 0.165 22.0);
```

在 `@theme inline` 块内，`--color-sidebar-ring` 之后加：

```css
  --color-state-active: var(--state-active);
  --color-state-intervention: var(--state-intervention);
  --color-state-intervention-text: var(--state-intervention-text);
  --color-state-failed: var(--state-failed);
```

- [ ] **Step 4: 在 `badge.tsx` 加 intervention 变体**

把 `web/src/components/ui/badge.tsx:17` 的 `outline` 那行改成两行：

```ts
        outline: "text-foreground",
        intervention:
          "border-transparent bg-state-intervention text-white shadow hover:bg-state-intervention/80",
```

- [ ] **Step 5: 跑测试确认它通过**

Run: `cd web && npx vitest run src/components/ui/badge.test.tsx`
Expected: PASS，两条用例全绿。

- [ ] **Step 6: 验证 token 真的被 Tailwind 生成了**

这一步不能省。Tailwind v4 遇到**未在 `@theme` 注册**的颜色名时，`bg-state-intervention` 不会报错，只会**静默不产出任何 CSS**——单测断言的是类名字符串，捕捉不到这种失败。

Run:
```bash
cd web && npm run build && grep -c "state-intervention" dist/assets/*.css
```
Expected: 输出的计数 **大于 0**。若为 0，说明 `@theme inline` 那段没写对，回 Step 3 检查键名是不是带了 `--color-` 前缀。

- [ ] **Step 7: 加意图注释**

- `index.css` 两处新增块的上方各一条注释（Step 3 的代码块里已含），说明**为什么** intervention 要分实色与文字色两个值、**为什么** `.dark` 必须成对给。
- `badge.tsx` 的 `intervention` 行上方加一条：

```ts
        // intervention（干预态）：等你答复 / 等待 Review。用实色胶囊而非
        // destructive 的红——红色在这套界面里专属「失败」，把「等你处理」
        // 也涂成红会让两种性质完全不同的事看起来一样急
```

- [ ] **Step 8: 提交**

```bash
cd web && npx vitest run
git add web/src/index.css web/src/components/ui/badge.tsx web/src/components/ui/badge.test.tsx
git commit -m "feat(web): 状态语义 token 与 Badge 干预态变体

index.css 新增 --state-active/intervention/intervention-text/failed 四个
token（取自原型 styles.css 的 --green/--amber/--red），并在 @theme inline
注册以生成工具类。badge.tsx 新增 intervention 变体。

intervention 分实色与文字色两个值：实色当圆点够醒目，当小字对白底只有
约 2.4:1 读不清，原型自己就分了这两个值。"
```

---

### Task 2: `columns.ts` 拆开视觉映射

**Files:**
- Modify: `web/src/app/board/columns.ts:71-90`（`stateBadgeVariant` 段）
- Test: `web/src/app/board/columns.test.ts`（追加）

**Interfaces:**
- Consumes: Task 1 的 `Badge` `intervention` 变体（`stateBadgeVariant` 会返回这个字符串）
- Produces:
  - `export type StateTone = 'idle' | 'active' | 'intervention' | 'done' | 'failed'`
  - `export function stateTone(state: string): StateTone`
  - `export function needsIntervention(state: string): boolean`
  - `export function stateBadgeVariant(state: string): 'default' | 'secondary' | 'destructive' | 'outline' | 'intervention'`（返回类型加宽）

  Task 3 消费 `StateTone` 与 `stateTone`，Task 4 消费 `stateTone` + `needsIntervention`，Task 8 消费 `stateTone`。

- [ ] **Step 1: 写失败的测试**

在 `web/src/app/board/columns.test.ts` 的 `import` 行追加 `needsIntervention, stateBadgeVariant, stateTone`，然后在文件末尾（最外层 `describe` 之后）追加：

```ts
describe('状态的视觉基调（硬契约）', () => {
  it('六个状态各有其基调', () => {
    expect(stateTone('pending')).toBe('idle')
    expect(stateTone('running')).toBe('active')
    expect(stateTone('waiting_answer')).toBe('intervention')
    expect(stateTone('waiting_review')).toBe('intervention')
    expect(stateTone('completed')).toBe('done')
    expect(stateTone('failed')).toBe('failed')
  })

  // 刻意与 stateToColumn 的回退值不同：分列回退 active 是为了让未知状态
  // 显眼地出现在「进行中」列（看得见）；染色回退 idle 是因为把一个没见过
  // 的状态涂成绿色或琥珀色，等于替它编造语义。两者共同保证的是「不消失」。
  it('未知状态基调回退 idle，不编造语义', () => {
    expect(stateTone('new_unknown_state')).toBe('idle')
    expect(stateToColumn('new_unknown_state')).toBe('active')
  })

  // 这条同时钉住「干预态口径与 filter.ts 的 pendingOnly、counts.ts 的
  // pending 三处一致」。三处任何一处改了这个集合，这条会红。
  it('干预态只认 waiting_answer 与 waiting_review', () => {
    expect(needsIntervention('waiting_answer')).toBe(true)
    expect(needsIntervention('waiting_review')).toBe(true)
    expect(needsIntervention('pending')).toBe(false)
    expect(needsIntervention('running')).toBe(false)
    expect(needsIntervention('completed')).toBe(false)
    expect(needsIntervention('failed')).toBe(false)
  })

  it('详情页 Badge：两个干预态改用 intervention 档', () => {
    expect(stateBadgeVariant('waiting_answer')).toBe('intervention')
    expect(stateBadgeVariant('waiting_review')).toBe('intervention')
    expect(stateBadgeVariant('failed')).toBe('destructive')
    expect(stateBadgeVariant('running')).toBe('default')
    expect(stateBadgeVariant('completed')).toBe('secondary')
    expect(stateBadgeVariant('new_unknown_state')).toBe('secondary')
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/board/columns.test.ts`
Expected: FAIL —— `stateTone`/`needsIntervention` 未导出；且 `stateBadgeVariant('waiting_answer')` 返回 `'destructive'` 而非 `'intervention'`。

- [ ] **Step 3: 实现**

把 `web/src/app/board/columns.ts` 末尾的 `stateBadgeVariant` 整段（:71-90）替换为：

```ts
// StateTone 是任务状态的视觉基调。看板卡片消费它决定圆点与文字的颜色。
//
// 与看板列（BoardColumn）是两个维度，不要混：列回答「归到哪一堆」，
// 基调回答「要不要抓你的眼睛」。completed 与 failed 同列不同调，
// waiting_answer 与 waiting_review 不同列却同调。
export type StateTone = 'idle' | 'active' | 'intervention' | 'done' | 'failed'

const STATE_TONES: Record<string, StateTone> = {
  pending: 'idle',
  running: 'active',
  waiting_answer: 'intervention',
  waiting_review: 'intervention',
  completed: 'done',
  failed: 'failed',
}

// stateTone 返回一个任务状态的视觉基调。
//
// 参数：
//   - state: 任务状态机的状态字符串，可能是本前端还不认识的新状态
//
// 返回：
//   - 该状态的视觉基调；未知状态回退 idle
//
// 注意：
//   - 未知状态回退 idle 而不是 active，刻意与 stateToColumn 的回退值不同。
//     分列回退 active 是为了让未知状态显眼地出现在「进行中」列（不消失）；
//     染色回退 idle 是因为把没见过的状态涂成绿色或琥珀色等于替它编造语义。
export function stateTone(state: string): StateTone {
  return STATE_TONES[state] ?? 'idle'
}

// needsIntervention 报告任务是否处于干预态——卡在你这儿、等你动手。
//
// 参数：
//   - state: 任务状态机的状态字符串
//
// 返回：
//   - waiting_answer / waiting_review 为 true，其余为 false
//
// 注意：
//   - 这个集合是**跨模块的口径**，仓库里另有三处依赖同一定义：
//     filter.ts 的 pendingOnly 筛选、counts.ts 的 pending 计数、
//     ProjectTree 的 wsCounts。改这里必须四处同改，columns.test.ts
//     有一条用例专门钉这个口径。
//   - failed 不在其中：它是终态，没有「等你动手就能继续」这回事，
//     想接着干的路径是重新 dispatch。
export function needsIntervention(state: string): boolean {
  return state === 'waiting_answer' || state === 'waiting_review'
}

// stateBadgeVariant 把任务状态映射成 Badge 的视觉变体。
//
// 参数：
//   - state: 任务状态机的状态字符串
//
// 返回：
//   - Badge 的 variant 名
//
// 注意：
//   - **只有任务详情页顶栏（TaskHeader）消费它。** 看板卡片改用
//     stateTone + StateDot 的「圆点 + 文字」形态（形态基准见 spec §1.1）。
//     两者曾共用这一个函数，那是耦合失误：看板是密集列表，行内标记的
//     视觉噪声本就该低于胶囊；详情页只有一个状态，胶囊才恰当。
export function stateBadgeVariant(
  state: string,
): 'default' | 'secondary' | 'destructive' | 'outline' | 'intervention' {
  switch (state) {
    case 'failed':
      return 'destructive'
    case 'waiting_answer':
    case 'waiting_review':
      return 'intervention'
    case 'running':
      return 'default'
    default:
      return 'secondary'
  }
}
```

注意 `waiting_review` 从原来的 `'outline'` 改成了 `'intervention'`，`'outline'` 分支随之消失——返回类型里仍保留 `'outline'` 字面量是刻意的：`Badge` 支持这一档，将来别的状态可能用得上，收窄类型只会逼后来人再改一次签名。

- [ ] **Step 4: 跑测试确认它通过**

Run: `cd web && npx vitest run src/app/board/columns.test.ts`
Expected: PASS，新增四条 + 原有六条全绿。

- [ ] **Step 5: 加意图注释**

Step 3 的代码块里已经把注释写全了，这一步是**核对**，逐条确认下面五处都在：

1. `StateTone` 类型上方：基调与列是两个维度，不要混
2. `stateTone` 的「注意」：回退 idle 而非 active 的理由
3. `needsIntervention` 的「注意」：跨模块口径的四处联动、failed 为何不算
4. `stateBadgeVariant` 的「注意」：为何只剩详情页消费、当初共用是耦合失误
5. `'outline'` 保留在返回类型里的理由（写在 Step 3 正文，实现时落成代码注释）

另外：**不要删 `isWaitingAnswer`**。Task 4 会删掉它在 `BoardPage` 的唯一调用点，但它是状态机契约的一部分（文件头把本文件定义为「硬契约，vitest 钉死」），保留函数与钉它的用例。

- [ ] **Step 6: 提交**

```bash
cd web && npx vitest run
git add web/src/app/board/columns.ts web/src/app/board/columns.test.ts
git commit -m "refactor(web): columns 拆开视觉映射，新增 stateTone 与 needsIntervention

stateBadgeVariant 原被看板卡片与任务详情页共用，是耦合失误——看板是密集
列表，行内标记的视觉噪声本就该低于胶囊。拆成 stateTone（看板）与
stateBadgeVariant（详情页），后者两个干预态改返回 intervention 档。

needsIntervention 的集合与 filter.ts 的 pendingOnly、counts.ts 的 pending
是同一口径，测试里有一条专门钉它。"
```

---

### Task 3: `StateDot` 与 `TaskState` 组件

**Files:**
- Create: `web/src/app/board/StateDot.tsx`
- Test: `web/src/app/board/StateDot.test.tsx`（新建）

**Interfaces:**
- Consumes: Task 1 的工具类；Task 2 的 `StateTone` / `stateTone` / `stateLabel`
- Produces:
  - `export function StateDot({ tone }: { tone: StateTone }): JSX.Element`
  - `export function TaskState({ state }: { state: string }): JSX.Element`

  Task 4 消费 `TaskState`，Task 8 消费 `StateDot`。

- [ ] **Step 1: 写失败的测试**

创建 `web/src/app/board/StateDot.test.tsx`：

```tsx
// StateDot / TaskState 的呈现契约测试。
//
// 形态基准是 prototypes/desktop-console 的 .task-state + .status-dot：
// 圆点 + 文字，不是填充胶囊（spec §1.1）。
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { StateDot, TaskState } from './StateDot'

describe('StateDot', () => {
  it('干预态是琥珀圆点', () => {
    const { container } = render(<StateDot tone="intervention" />)
    expect(container.firstChild).toHaveClass('bg-state-intervention')
  })

  it('running / completed 是绿点，failed 是红点，pending 是灰点', () => {
    const { container: a } = render(<StateDot tone="active" />)
    expect(a.firstChild).toHaveClass('bg-state-active')
    const { container: d } = render(<StateDot tone="done" />)
    expect(d.firstChild).toHaveClass('bg-state-active')
    const { container: f } = render(<StateDot tone="failed" />)
    expect(f.firstChild).toHaveClass('bg-state-failed')
    const { container: i } = render(<StateDot tone="idle" />)
    expect(i.firstChild).toHaveClass('bg-muted-foreground/40')
  })
})

describe('TaskState', () => {
  it('干预态的文字染琥珀', () => {
    render(<TaskState state="waiting_answer" />)
    const el = screen.getByText('等你答复')
    expect(el.className).toContain('text-state-intervention-text')
  })

  it('waiting_review 同为干预态', () => {
    render(<TaskState state="waiting_review" />)
    expect(screen.getByText('Review').className).toContain('text-state-intervention-text')
  })

  it('failed 的文字染红', () => {
    render(<TaskState state="failed" />)
    expect(screen.getByText('失败').className).toContain('text-state-failed')
  })

  // 只有需要你注意的才染色——全都染色等于都不染色
  it('其余状态文字保持次要色', () => {
    render(<TaskState state="running" />)
    expect(screen.getByText('进行中').className).toContain('text-muted-foreground')
  })

  it('未知状态原文透出，不吞数据', () => {
    render(<TaskState state="new_unknown_state" />)
    expect(screen.getByText('new_unknown_state')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/board/StateDot.test.tsx`
Expected: FAIL —— `Failed to resolve import "./StateDot"`。

- [ ] **Step 3: 实现**

创建 `web/src/app/board/StateDot.tsx`：

```tsx
// StateDot / TaskState —— 任务状态的行内呈现（圆点 + 文字）。
//
// 职责：
//   - StateDot：把 StateTone 渲染成一个 7px 的实色圆点
//   - TaskState：圆点 + 状态文案，干预态与失败态额外染文字色
//
// 边界：
//   - 不查任何状态语义，全部委托 columns.ts 的 stateTone / stateLabel；
//     这里只负责「基调 → 类名」这一层映射
//   - 不是通用 UI 原语，所以不放 components/ui/：它消费 columns.ts 的领域
//     映射。放在 app/board/ 与 columns.ts 同居，跨目录消费的先例是
//     TaskHeader.tsx 的 `import ... from '../board/columns'`
//   - 不管布局：外边距、排列由调用方给
//
// 形态基准：prototypes/desktop-console 的 .status-dot（7px 圆点）与
// .task-state.attention（文字色 #a66c09）。看板卡片刻意不用填充胶囊
// Badge——密集列表里胶囊的视觉噪声太高（spec §1.1、§3.1）。
import { type StateTone, stateLabel, stateTone } from './columns'
import { cn } from '@/lib/utils'

// DOT_CLASS 是基调到圆点底色的映射。
// done 与 active 共用绿色：原型里「已完成」和「进行中」都是绿点，
// 区分靠列与文案，不靠点的颜色。
const DOT_CLASS: Record<StateTone, string> = {
  idle: 'bg-muted-foreground/40',
  active: 'bg-state-active',
  intervention: 'bg-state-intervention',
  done: 'bg-state-active',
  failed: 'bg-state-failed',
}

// TEXT_CLASS 是基调到文字色的映射。
// 只有 intervention 与 failed 染色——全都染色等于都不染色，原型里
// 其余状态的文案一律是次要灰。
const TEXT_CLASS: Record<StateTone, string> = {
  idle: 'text-muted-foreground',
  active: 'text-muted-foreground',
  intervention: 'text-state-intervention-text',
  done: 'text-muted-foreground',
  failed: 'text-state-failed',
}

// StateDot 渲染一个状态圆点。
//
// 参数：
//   - tone: 视觉基调，由 columns.ts 的 stateTone 得出
//
// 返回：
//   - 一个 7px 的圆形 span，aria-hidden（它是纯装饰，语义由相邻文字承载）
export function StateDot({ tone }: { tone: StateTone }) {
  return (
    <span
      aria-hidden
      className={cn('inline-block size-[7px] shrink-0 rounded-full', DOT_CLASS[tone])}
    />
  )
}

// TaskState 渲染「圆点 + 状态文案」。
//
// 参数：
//   - state: 任务状态机的状态字符串，未知状态原样透出（不吞数据）
//
// 返回：
//   - 一个 inline-flex 的 span，含圆点与文案
export function TaskState({ state }: { state: string }) {
  const tone = stateTone(state)
  return (
    <span className={cn('inline-flex items-center gap-1.5 text-xs', TEXT_CLASS[tone])}>
      <StateDot tone={tone} />
      {stateLabel(state)}
    </span>
  )
}
```

- [ ] **Step 4: 跑测试确认它通过**

Run: `cd web && npx vitest run src/app/board/StateDot.test.tsx`
Expected: PASS，七条用例全绿。

- [ ] **Step 5: 加意图注释**

Step 3 的代码块里已写全，核对这四处都在：

1. **文件头**：职责（两个组件各干什么）+ 边界（不查语义、为何不放 `components/ui/`、不管布局）+ 形态基准出处
2. `DOT_CLASS` 上方：`done` 与 `active` 为何共用绿色
3. `TEXT_CLASS` 上方：为何只有两档染色
4. 两个导出函数各自的参数 / 返回注释；`StateDot` 的 `aria-hidden` 理由

- [ ] **Step 6: 提交**

```bash
cd web && npx vitest run
git add web/src/app/board/StateDot.tsx web/src/app/board/StateDot.test.tsx
git commit -m "feat(web): 新增 StateDot 与 TaskState 行内状态标记

形态取自原型的 .status-dot + .task-state：圆点 + 文字，不是填充胶囊。
密集列表里胶囊的视觉噪声太高，这是看板卡片放弃 Badge 的理由。

只有 intervention 与 failed 染文字色——全都染色等于都不染色。"
```

---

### Task 4: 看板卡片改造（删重复徽章 + 换形态 + 卡片级干预态）

**Files:**
- Modify: `web/src/app/board/BoardPage.tsx:145-180`（`TaskCard` 整个函数）
- Test: `web/src/app/board/BoardPage.test.tsx`（新建；已核实 `app/board/` 下当前只有 `FilterBar.test.tsx` / `columns.test.ts` / `filter.test.ts`）

**Interfaces:**
- Consumes: Task 2 的 `needsIntervention`、Task 3 的 `TaskState`
- Produces: 无对外接口，是叶子改动

- [ ] **Step 1: 写失败的测试**

创建 `web/src/app/board/BoardPage.test.tsx`。先看 `BoardPage.tsx` 顶部导出了什么——`TaskCard` 目前是模块内私有函数，测试要通过 `BoardPage` 整体渲染来覆盖它。**若 `BoardPage` 的 props 需要 router 或 provider 才能渲染**，就把 `TaskCard` 改成 `export function TaskCard`（仅为测试导出，加一行注释说明），直接测它。**优先选后者**——`TaskCard` 是纯展示组件，直接测比拉起整页便宜得多。

```tsx
// 看板卡片的呈现契约测试。
//
// 第一条用例钉的是 B75 的既有缺陷：waiting_answer 曾在同一张卡上并排渲染
// 两个一模一样的红徽章（waitingAnswer 那个 + stateLabel 那个）。它是回归
// 防线，删不得。
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { Task } from '../../api/types'
import { TaskCard } from './BoardPage'

function task(over: Partial<Task>): Task {
  return {
    id: 'T1', target: '', repo_path: '', branch: 'feat/x', plan_path: '', plan_summary: '',
    executor_session: '', state: 'running', created_at: '', updated_at: '2026-08-12T00:00:00Z',
    name: '重构工单通道', executor: 'opencode', model: '', work_dir: '', worktree_managed: false,
    base_commit: '', base_ahead: 0, repo_dirty_count: 0, repo_dirty_files: '',
    done_note: '', machine: '', project_id: 'p1', ...over,
  }
}

function renderCard(state: string) {
  return render(<TaskCard task={task({ state })} projectName="handoff" onOpen={vi.fn()} />)
}

describe('TaskCard', () => {
  // B75 的回归防线：这条红了说明重复徽章又回来了
  it('waiting_answer 的「等你答复」只出现一次', () => {
    renderCard('waiting_answer')
    expect(screen.getAllByText('等你答复')).toHaveLength(1)
  })

  it('两个干预态都带卡片级干预标记', () => {
    const { container: a } = renderCard('waiting_answer')
    expect(a.firstChild).toHaveClass('border-state-intervention/45')
    const { container: r } = renderCard('waiting_review')
    expect(r.firstChild).toHaveClass('border-state-intervention/45')
  })

  it('failed 保持红色区分，且不带干预标记', () => {
    const { container } = renderCard('failed')
    expect(container.firstChild).toHaveClass('border-destructive/40')
    expect(container.firstChild).not.toHaveClass('border-state-intervention/45')
  })

  it('running / completed 两种标记都不带', () => {
    const { container: a } = renderCard('running')
    expect(a.firstChild).not.toHaveClass('border-state-intervention/45')
    expect(a.firstChild).not.toHaveClass('border-destructive/40')
    const { container: c } = renderCard('completed')
    expect(c.firstChild).not.toHaveClass('border-state-intervention/45')
  })

  it('状态用圆点 + 文字呈现，文案与状态对得上', () => {
    renderCard('waiting_review')
    expect(screen.getByText('Review').className).toContain('text-state-intervention-text')
  })

  it('未归属项目显示「未归属」，本机显示「本机」', () => {
    render(<TaskCard task={task({ state: 'running' })} projectName="" onOpen={vi.fn()} />)
    expect(screen.getByText('未归属')).toBeInTheDocument()
    expect(screen.getByText('本机')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/board/BoardPage.test.tsx`
Expected: FAIL —— `TaskCard` 未导出；导出后第一条会红（`getAllByText('等你答复')` 长度是 2，正是要修的缺陷）。

- [ ] **Step 3: 改 `TaskCard`**

把 `web/src/app/board/BoardPage.tsx:145-180` 的整个 `TaskCard` 替换为：

```tsx
// TaskCard 是一张任务卡片：名称、三行元信息（项目 / 工作树 / 机器）、
// 底部一行状态与执行器。
//
// 卡片级视觉分三档，互斥：
//   - 干预态（waiting_answer / waiting_review）：琥珀边框 + 左侧竖条。
//     这是「哪张卡真的卡着你」的唯一线索——只染状态文字的话，Review 列
//     两张卡看起来一样重
//   - failed：红边 + 淡红底，终态的视觉区分
//   - 其余：普通边框
//
// 导出仅为单测直接渲染（整页渲染需要 router，测一个纯展示组件不值当）。
export function TaskCard({ task, projectName, onOpen }: { task: Task; projectName: string; onOpen: () => void }) {
  const intervention = needsIntervention(task.state)
  const failed = isFailed(task.state)
  return (
    <button
      type="button"
      onClick={onOpen}
      className={cn(
        'flex flex-col gap-1.5 rounded-md border bg-background p-2.5 text-left shadow-sm transition-colors hover:bg-accent/60',
        intervention && 'border-state-intervention/45 shadow-[inset_3px_0_var(--state-intervention)]',
        failed && 'border-destructive/40 bg-destructive/5',
      )}
    >
      <span className="min-w-0 truncate text-sm font-medium">
        {task.name || task.plan_summary || '（无名称）'}
      </span>
      <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
        <span className="min-w-0 truncate font-mono">{task.branch}</span>
        <span className="shrink-0">{formatRelative(task.updated_at)}</span>
      </div>
      <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
        <span className="min-w-0 truncate">{projectName || '未归属'}</span>
        <span aria-hidden>·</span>
        <span className="shrink-0">{task.machine === '' ? '本机' : task.machine}</span>
      </div>
      {/* 底部一行：状态 + 执行器，对齐原型的 board-card-footer */}
      <div className="flex items-center justify-between gap-2 border-t pt-1.5">
        <TaskState state={task.state} />
        <span className="font-mono text-xs text-muted-foreground">{task.executor}</span>
      </div>
    </button>
  )
}
```

三处关键改动，对照原文件核对：

1. **删掉了 `{waitingAnswer && <Badge variant="destructive">等你答复</Badge>}`**（原 :163）。删这一个而不是删下面那个 `stateLabel` 的：留下的那行是全状态通用的，删了会让其余五个状态一起失去标记。
2. `<Badge variant={stateBadgeVariant(...)}>{stateLabel(...)}</Badge>` 换成 `<TaskState state={task.state} />`，位置从卡片中部挪到底部一行（与 executor 同行）。
3. 卡片容器改用 `cn()` 组合三档互斥样式。

同步改 import：`BoardPage.tsx` 顶部把 `isWaitingAnswer`、`stateBadgeVariant`、`stateLabel` 从 `./columns` 的 import 里移除（若它们在本文件已无其它调用点），加入 `needsIntervention`；新增 `import { TaskState } from './StateDot'` 与 `import { cn } from '@/lib/utils'`（若尚未引入）。`Badge` 若在本文件其它地方还有用（列头的计数徽章 `<Badge variant="secondary">{tasks.length}</Badge>` 在用），**保留** 该 import。

- [ ] **Step 4: 跑测试确认它通过**

Run: `cd web && npx vitest run src/app/board/`
Expected: PASS —— `BoardPage.test.tsx` 六条 + `columns.test.ts` + `filter.test.ts` + `FilterBar.test.tsx` 全绿。

- [ ] **Step 5: 确认没留下未使用的 import**

Run: `cd web && npm run lint && npm run typecheck`
Expected: 两条都无输出/无错误。有 `no-unused-vars` 就把对应 import 删掉。

- [ ] **Step 6: 加意图注释**

Step 3 的代码块里已含，核对这三处：

1. `TaskCard` 上方的文件内注释：三档卡片级视觉互斥、干预态边框「是哪张卡真的卡着你的唯一线索」的理由
2. `export` 的理由（仅为单测）
3. 底部一行的 `{/* 对齐原型的 board-card-footer */}`

- [ ] **Step 7: 提交**

```bash
cd web && npx vitest run
git add web/src/app/board/BoardPage.tsx web/src/app/board/BoardPage.test.tsx
git commit -m "fix(web): 看板卡片补干预态标记，并删掉重复渲染的第二个红徽章

B75：waiting_answer 曾在同一张卡上并排渲染两个一模一样的红徽章
（waitingAnswer 那个和 stateLabel('waiting_answer') 那个）。删前者——
后者是全状态通用的，删了其余五个状态会一起失去标记。

卡片状态改用 TaskState 的「圆点 + 文字」形态并挪到底部一行（对齐原型的
board-card-footer）；两个干预态加琥珀边框 + 左侧竖条，failed 保持红色
区分，三档互斥。

新增 BoardPage.test.tsx，第一条用例是重复徽章的回归防线。"
```

---

### Task 5: 左栏过滤纯函数 `tree/search.ts`

**Files:**
- Create: `web/src/app/tree/search.ts`
- Test: `web/src/app/tree/search.test.ts`（新建）

**Interfaces:**
- Consumes: `ProjectTreeResp` / `Task` / `Workspace`（`web/src/api/types`，只读不改）
- Produces:
  ```ts
  export interface TreeFilter {
    query: string                              // 归一化后的查询串（trim + toLowerCase），空串表示无过滤
    projects: ProjectNode[]                    // 过滤后可见的项目（子树已按可见性裁剪）
    projectCount: number                       // 「项目 N」的 N
    unassignedTasks: Task[]                    // 过滤后可见的未归属任务
    unownedNames: string[]                     // 过滤后可见的「未登记为项目」目录名
    isEmpty: boolean                           // 可见项目为 0 且未归属分组也空
  }
  export function filterTree(tree: ProjectTreeResp, tasks: Task[], rawQuery: string): TreeFilter
  ```
  Task 6 消费 `filterTree` 与 `TreeFilter`。

  **注意 `filterTree` 返回的 `projects` 是裁剪后的新对象**（`locations` / `workspaces` 数组被过滤过），不是原树的引用。`ProjectTree.tsx` 直接遍历它即可，不需要再判可见性。

- [ ] **Step 1: 写失败的测试**

创建 `web/src/app/tree/search.test.ts`：

```ts
// 左栏过滤的契约测试。
//
// 可见性只有一条规则：节点可见 ⟺ 自身命中 或 任一后代命中。自身命中时
// 整棵子树都显示（搜项目名要能看到它下面的全部内容）。这些用例逐条钉住
// 这条规则的四个层级与两个方向。
import { describe, expect, it } from 'vitest'
import type { ProjectTreeResp, Task } from '../../api/types'
import { filterTree } from './search'

function task(over: Partial<Task>): Task {
  return {
    id: 'i', target: '', repo_path: '', branch: '', plan_path: '', plan_summary: '',
    executor_session: '', state: 'running', created_at: '', updated_at: '', name: '',
    executor: 'opencode', model: '', work_dir: '', worktree_managed: false,
    base_commit: '', base_ahead: 0, repo_dirty_count: 0, repo_dirty_files: '',
    done_note: '', machine: '', project_id: 'p1', ...over,
  }
}

// 两个项目：handoff（本机，主目录 /w + 工作树 /w/b2-b3）、nova（devbox，主目录 /srv/n）
const tree: ProjectTreeResp = {
  projects: [
    {
      project_id: 'p1', origin_url: '', name: 'handoff',
      locations: [{
        machine: '', name: 'handoff', path: '/w', probe_error: '',
        workspaces: [
          { path: '/w', branch: 'main', head: 'a', is_main: true, managed: false },
          { path: '/w/b2-b3', branch: 'integration/b2-b3', head: 'b', is_main: false, managed: true },
        ],
      }],
    },
    {
      project_id: 'p2', origin_url: '', name: 'nova',
      locations: [{
        machine: 'devbox', name: 'nova', path: '/srv/n', probe_error: '',
        workspaces: [{ path: '/srv/n', branch: 'main', head: 'c', is_main: true, managed: false }],
      }],
    },
  ],
  unowned: ['scratchpad'],
}

const tasks: Task[] = [
  task({ id: 'T1', project_id: 'p1', machine: '', work_dir: '/w/b2-b3', name: '重构工单通道' }),
  task({ id: 'T2', project_id: 'p2', machine: 'devbox', work_dir: '/srv/n', name: '补齐图像校验' }),
  task({ id: 'T9', project_id: '', machine: '', work_dir: '', name: '孤儿任务' }),
]

describe('filterTree', () => {
  it('空查询：全部可见，N 等于项目总数', () => {
    const r = filterTree(tree, tasks, '')
    expect(r.projectCount).toBe(2)
    expect(r.projects).toHaveLength(2)
    expect(r.unassignedTasks).toHaveLength(1)
    expect(r.unownedNames).toEqual(['scratchpad'])
    expect(r.isEmpty).toBe(false)
  })

  it('命中项目名：该项目整棵子树可见，另一个项目消失', () => {
    const r = filterTree(tree, tasks, 'handoff')
    expect(r.projectCount).toBe(1)
    expect(r.projects[0].name).toBe('handoff')
    expect(r.projects[0].locations[0].workspaces).toHaveLength(2)
  })

  it('命中机器名：其祖先项目可见，非该机器的项目消失', () => {
    const r = filterTree(tree, tasks, 'devbox')
    expect(r.projectCount).toBe(1)
    expect(r.projects[0].name).toBe('nova')
  })

  // "" 的机器要能用「本机」搜到——这是 machineLabel 的口径，不是原始字段
  it('命中「本机」：本机上的项目可见', () => {
    const r = filterTree(tree, tasks, '本机')
    expect(r.projectCount).toBe(1)
    expect(r.projects[0].name).toBe('handoff')
  })

  it('命中目录名：只留下命中的那个目录，兄弟目录消失', () => {
    const r = filterTree(tree, tasks, 'b2-b3')
    expect(r.projectCount).toBe(1)
    expect(r.projects[0].locations[0].workspaces).toHaveLength(1)
    expect(r.projects[0].locations[0].workspaces[0].branch).toBe('integration/b2-b3')
  })

  it('命中任务名：祖先链全部可见，兄弟目录消失', () => {
    const r = filterTree(tree, tasks, '重构工单')
    expect(r.projectCount).toBe(1)
    expect(r.projects[0].name).toBe('handoff')
    expect(r.projects[0].locations[0].workspaces).toHaveLength(1)
    expect(r.projects[0].locations[0].workspaces[0].path).toBe('/w/b2-b3')
  })

  it('未归属分组参与过滤，且不计入 N', () => {
    const r = filterTree(tree, tasks, '孤儿')
    expect(r.projectCount).toBe(0)
    expect(r.unassignedTasks).toHaveLength(1)
    expect(r.unownedNames).toHaveLength(0)
    expect(r.isEmpty).toBe(false)   // 未归属有货，不算空
  })

  it('未登记目录名也能搜到', () => {
    const r = filterTree(tree, tasks, 'scratch')
    expect(r.projectCount).toBe(0)
    expect(r.unownedNames).toEqual(['scratchpad'])
    expect(r.isEmpty).toBe(false)
  })

  it('零命中：isEmpty 为真', () => {
    const r = filterTree(tree, tasks, 'zzzz-nothing')
    expect(r.projectCount).toBe(0)
    expect(r.unassignedTasks).toHaveLength(0)
    expect(r.unownedNames).toHaveLength(0)
    expect(r.isEmpty).toBe(true)
  })

  it('大小写不敏感，首尾空白被 trim', () => {
    expect(filterTree(tree, tasks, '  HANDOFF  ').projectCount).toBe(1)
    expect(filterTree(tree, tasks, '  ').projectCount).toBe(2)   // 全空白等同空查询
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/tree/search.test.ts`
Expected: FAIL —— `Failed to resolve import "./search"`。

- [ ] **Step 3: 实现**

创建 `web/src/app/tree/search.ts`：

```ts
// search.ts —— 左栏项目树的过滤（纯函数，无 React 依赖）。
//
// 职责：
//   - 把一次查询串归一化，按四类字段（项目名 / 机器名 / 目录名 / 任务名）
//     在树上求出可见集合
//   - 给出「项目 N」的计数与「是否零结果」的判定
//
// 边界：
//   - 不管 UI：不知道搜索框长什么样、⌘K 是什么、折叠态怎么存
//   - 不改入参：返回的是裁剪后的新对象，原树不被修改
//   - 不做高亮、不做模糊匹配、不做拼音——includes 够用（spec §11）
//
// 为什么单独成文件而不塞进 ProjectTree.tsx：可见性是一条递归规则，塞在
// 组件里只能靠渲染断言间接测。仓库既有同款模式——board/filter.ts（看板
// 筛选）、tree/counts.ts（树计数）都是「纯函数 + 独立测试文件」。
import type { ProjectLocationNode, ProjectNode, ProjectTreeResp, Task, Workspace } from '../../api/types'

// TreeFilter 是一次过滤的完整结果。projects 已按可见性裁剪，
// 调用方直接遍历即可，不需要再判一次。
export interface TreeFilter {
  query: string
  projects: ProjectNode[]
  projectCount: number
  unassignedTasks: Task[]
  unownedNames: string[]
  isEmpty: boolean
}

// hit 是全文唯一的匹配判据：大小写不敏感的子串包含。
// 提成一个函数是为了让「四类字段用的是同一条判据」这件事在代码里看得见。
function hit(text: string, q: string): boolean {
  return text.toLowerCase().includes(q)
}

// machineText 是机器名参与匹配时的文本。
// 用「本机」而不是空串：机器名为 "" 表示本机，界面上显示的也是「本机」，
// 搜索面必须与眼睛看到的一致——否则用户搜「本机」会一无所获。
function machineText(machine: string): string {
  return machine === '' ? '本机' : machine
}

// dirText 是目录参与匹配时的文本，口径与 ProjectTree 的 dirLabel 一致：
// 有 branch 用 branch，否则用路径末段。
function dirText(ws: Workspace): string {
  if (ws.branch !== '') return ws.branch
  const seg = ws.path.split('/').filter(Boolean)
  return seg.length > 0 ? seg[seg.length - 1] : ws.path
}

// taskText 是任务参与匹配时的文本，口径与 ProjectTree 的 taskName 一致。
function taskText(t: Task): string {
  return t.name || t.plan_summary || '（无名称）'
}

// tasksOfWorkspace 挑出挂在某个目录下的任务。
// 判据是 work_dir 与 workspace.path 路径等值；work_dir 为空表示原地模式，
// 挂到主目录——与 proto.Task.Workdir() 的回退语义一致。
function tasksOfWorkspace(tasks: Task[], project: ProjectNode, machine: string, ws: Workspace): Task[] {
  return tasks.filter((t) => {
    if (t.project_id !== project.project_id || t.machine !== machine) return false
    if (t.work_dir === '') return ws.is_main
    return t.work_dir === ws.path
  })
}

// filterTree 按查询串裁剪项目树。
//
// 参数：
//   - tree: GET /api/projects/tree 的响应
//   - tasks: 任务流的当前快照（用于任务名匹配与任务归属判定）
//   - rawQuery: 用户输入的原始查询串
//
// 返回：
//   - TreeFilter，projects 已裁剪；rawQuery 去空白后为空时原样返回全树
//
// 注意：
//   - **可见性只有一条规则：节点可见 ⟺ 自身命中 或 任一后代命中。**
//     且自身命中时整棵子树都保留——搜一个项目名，是要看它下面的全部
//     机器、目录、任务，而不是只看到一个光秃秃的项目行。
//   - 「未归属」分组参与过滤但**不计入 projectCount**：它不是一个项目，
//     是个收纳箱。算进去会出现「项目 3」但下面只有 2 个能展开的项目行。
export function filterTree(tree: ProjectTreeResp, tasks: Task[], rawQuery: string): TreeFilter {
  const q = rawQuery.trim().toLowerCase()
  const unassignedAll = tasks.filter((t) => t.project_id === '')

  if (q === '') {
    return {
      query: '',
      projects: tree.projects,
      projectCount: tree.projects.length,
      unassignedTasks: unassignedAll,
      unownedNames: tree.unowned,
      isEmpty: tree.projects.length === 0 && unassignedAll.length === 0 && tree.unowned.length === 0,
    }
  }

  const projects: ProjectNode[] = []
  for (const project of tree.projects) {
    const projectHit = hit(project.name, q)

    const locations: ProjectLocationNode[] = []
    for (const loc of project.locations) {
      const machineHit = projectHit || hit(machineText(loc.machine), q)

      // 项目或机器自身命中 → 整层目录原样保留；否则逐个目录判
      const workspaces = machineHit
        ? loc.workspaces
        : loc.workspaces.filter((ws) =>
            hit(dirText(ws), q) ||
            tasksOfWorkspace(tasks, project, loc.machine, ws).some((t) => hit(taskText(t), q)),
          )

      if (machineHit || workspaces.length > 0) locations.push({ ...loc, workspaces })
    }

    if (projectHit || locations.length > 0) projects.push({ ...project, locations })
  }

  const unassignedTasks = unassignedAll.filter((t) => hit(taskText(t), q))
  const unownedNames = tree.unowned.filter((name) => hit(name, q))

  return {
    query: q,
    projects,
    projectCount: projects.length,
    unassignedTasks,
    unownedNames,
    isEmpty: projects.length === 0 && unassignedTasks.length === 0 && unownedNames.length === 0,
  }
}
```

- [ ] **Step 4: 跑测试确认它通过**

Run: `cd web && npx vitest run src/app/tree/search.test.ts`
Expected: PASS，十条用例全绿。

- [ ] **Step 5: 加意图注释**

Step 3 已写全，核对这六处：

1. **文件头**：职责 + 边界（不管 UI、不改入参、不做高亮/模糊/拼音）+ **为什么单独成文件**（可见性是递归规则，塞组件里只能间接测；仓库既有同款模式）
2. `hit`：为何提成函数（让「四类字段同一判据」看得见）
3. `machineText`：为何用「本机」而不是空串（搜索面必须与眼睛看到的一致）
4. `tasksOfWorkspace`：`work_dir` 为空的回退语义出处
5. `filterTree` 的「注意」：可见性那一条规则 + 自身命中保留全子树的理由 + 未归属不计入 N 的理由
6. `machineHit ? loc.workspaces : ...` 那一行上方的行内注释

- [ ] **Step 6: 提交**

```bash
cd web && npx vitest run
git add web/src/app/tree/search.ts web/src/app/tree/search.test.ts
git commit -m "feat(web): 左栏过滤纯函数 tree/search.ts

可见性只有一条规则：节点可见 ⟺ 自身命中 或 任一后代命中；自身命中时
整棵子树保留——搜项目名是要看它下面的全部内容。

匹配四类字段：项目名 / 机器名（\"\" 用「本机」，与界面显示一致）/ 目录名
/ 任务名。「未归属」参与过滤但不计入「项目 N」——它不是项目是收纳箱。

抽成纯函数是为了能直接建契约测试，与 board/filter.ts、tree/counts.ts
的既有模式一致。"
```

---

### Task 6: 左栏接入搜索框、「项目 N」与空态

**Files:**
- Modify: `web/src/app/tree/ProjectTree.tsx`（`ProjectTree` 组件的 state 与 return）
- Test: `web/src/app/tree/ProjectTree.test.tsx`（追加）

**Interfaces:**
- Consumes: Task 5 的 `filterTree` / `TreeFilter`
- Produces: 无对外接口；`ProjectTreeProps` **不变**（不新增 prop——这是「不进 `Shell.tsx`」的前提）

- [ ] **Step 1: 写失败的测试**

在 `web/src/app/tree/ProjectTree.test.tsx` 的 `describe('ProjectTree', ...)` 内追加：

```tsx
  it('渲染搜索框与「项目 N」，N 默认是项目总数', () => {
    render(<ProjectTree {...props()} />)
    expect(screen.getByPlaceholderText('搜索项目、机器或任务')).toBeInTheDocument()
    expect(screen.getByText('项目')).toBeInTheDocument()
    expect(screen.getByTestId('project-count')).toHaveTextContent('1')
  })

  it('搜任务名：该任务可见，无关目录不可见', () => {
    render(<ProjectTree {...props()} />)
    fireEvent.change(screen.getByPlaceholderText('搜索项目、机器或任务'), {
      target: { value: '重构工单' },
    })
    expect(screen.getByText('重构工单通道')).toBeInTheDocument()
    expect(screen.queryByText('main')).not.toBeInTheDocument()
  })

  it('搜项目名：N 仍是 1，整棵子树可见', () => {
    render(<ProjectTree {...props()} />)
    fireEvent.change(screen.getByPlaceholderText('搜索项目、机器或任务'), {
      target: { value: 'handoff' },
    })
    expect(screen.getByTestId('project-count')).toHaveTextContent('1')
    expect(screen.getByText('main')).toBeInTheDocument()
    expect(screen.getByText('integration/b2-b3')).toBeInTheDocument()
  })

  it('零结果时出空态文案，N 归 0', () => {
    render(<ProjectTree {...props()} />)
    fireEvent.change(screen.getByPlaceholderText('搜索项目、机器或任务'), {
      target: { value: 'zzzz-nothing' },
    })
    expect(screen.getByText('没有匹配的项目或任务')).toBeInTheDocument()
    expect(screen.getByTestId('project-count')).toHaveTextContent('0')
  })

  // 钉住「旁路而非清空」：搜索期间强制展开，清空后手动折叠的状态原样回来
  it('清空搜索后，此前手动折叠的节点仍是折叠的', () => {
    render(<ProjectTree {...props()} />)
    const input = screen.getByPlaceholderText('搜索项目、机器或任务')

    // 先手动折叠项目 handoff
    fireEvent.click(screen.getByText('handoff'))
    expect(screen.queryByText('main')).not.toBeInTheDocument()

    // 搜索期间强制展开
    fireEvent.change(input, { target: { value: 'handoff' } })
    expect(screen.getByText('main')).toBeInTheDocument()

    // 清空后折叠态原样回来
    fireEvent.change(input, { target: { value: '' } })
    expect(screen.queryByText('main')).not.toBeInTheDocument()
  })
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/tree/ProjectTree.test.tsx`
Expected: FAIL —— 找不到 placeholder 为「搜索项目、机器或任务」的元素。

- [ ] **Step 3: 实现**

改 `web/src/app/tree/ProjectTree.tsx`：

**(a) import 追加**（`lucide-react` 的 import 里加 `Search`）：

```ts
import { useMemo, useRef, useState } from 'react'
import {
  ChevronRight, FolderGit2, GitBranch, HardDrive, Home, LayoutGrid, Plus, Search, Settings, Ticket, Trash2, WifiOff,
} from 'lucide-react'
import { filterTree } from './search'
```

**(b) 组件内新增 state 与过滤结果**（放在既有 `collapsed` state 之后）：

```ts
  const [query, setQuery] = useState('')
  const searchRef = useRef<HTMLInputElement>(null)

  // 过滤结果。tasks 每 2.5s 刷新一次，useMemo 避免每次任务流心跳都重算整棵树。
  const filtered = useMemo(() => filterTree(tree, tasks, query), [tree, tasks, query])
  const searching = filtered.query !== ''
```

**(c) 展开判定改为旁路 `collapsed`**：把既有的

```ts
  const expanded = (key: string) => !collapsed.has(key)
```

改成

```ts
  // 搜索期间旁路 collapsed：搜到了却折叠着等于没搜到。
  // 注意是「旁路」不是「清空」——collapsed 原样保留，查询清空后用户手动
  // 折起来的布局立刻回来，搜索不破坏布局。
  const expanded = (key: string) => searching || !collapsed.has(key)
```

**(d) 遍历数据源换成过滤后的**：把 `tree.projects.map(...)` 改成 `filtered.projects.map(...)`；把 `const unassigned = tasks.filter((t) => t.project_id === '')` 与 `hasUnowned` 的计算改成用 `filtered.unassignedTasks` / `filtered.unownedNames`：

```ts
  const unassigned = filtered.unassignedTasks
  const hasUnowned = unassigned.length > 0 || filtered.unownedNames.length > 0
```

并把「未归属」分组里 `tree.unowned.map(...)` 改成 `filtered.unownedNames.map(...)`。

**(e) 在「任务看板」按钮之后、项目列表之前插入搜索框与小标题**：

```tsx
      {/* 搜索框与「项目 N」——形态基准是原型左栏的 sidebar-search +
          sidebar-section-title。N 跟随过滤，搜索时它就是「找到几个」的
          即时反馈；「未归属」不计入——它不是项目，是收纳箱 */}
      <div className="mb-1 px-2">
        <label className="flex items-center gap-1.5 rounded-md border bg-background px-2 py-1">
          <Search className="size-3.5 shrink-0 text-muted-foreground" />
          <input
            ref={searchRef}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="搜索项目、机器或任务"
            className="min-w-0 flex-1 bg-transparent text-[13px] outline-none placeholder:text-muted-foreground"
          />
          <kbd className="shrink-0 rounded border px-1 text-[10px] text-muted-foreground">⌘K</kbd>
        </label>
      </div>

      <div className="px-3 pb-1 pt-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
        项目 <span data-testid="project-count">{filtered.projectCount}</span>
      </div>
```

**(f) 在项目列表与「未归属」分组之后插入空态**：

```tsx
      {/* 左栏搜到全白会像加载失败，必须有话说 */}
      {filtered.isEmpty && searching && (
        <p className="px-3 py-4 text-[13px] text-muted-foreground">没有匹配的项目或任务</p>
      )}
```

- [ ] **Step 4: 跑测试确认它通过**

Run: `cd web && npx vitest run src/app/tree/`
Expected: PASS —— `ProjectTree.test.tsx` 原有用例 + 新增五条 + `search.test.ts` + `counts.test.ts` 全绿。

**若原有用例红了**：多半是新增的「项目」小标题让 `getByText` 撞上了 multiple matches。修法是把断言换成更精确的选择器，**不要**改小标题文案去迁就测试。

- [ ] **Step 5: 加意图注释**

核对这四处（Step 3 的代码块里已含）：

1. `filtered` 的 `useMemo` 上方：为何要 memo（任务流 2.5s 心跳）
2. `expanded` 上方：**旁路而非清空**的理由——这是最容易被后来人"顺手改成清空"的地方
3. 搜索框区块上方：形态基准出处 + N 的口径与「未归属不计入」的理由
4. 空态上方：为何必须有空态

- [ ] **Step 6: 提交**

```bash
cd web && npx vitest run && npm run typecheck && npm run lint
git add web/src/app/tree/ProjectTree.tsx web/src/app/tree/ProjectTree.test.tsx
git commit -m "feat(web): 左栏搜索框与「项目 N」小标题

B74。搜索期间旁路 collapsed 集合（搜到了却折叠着等于没搜到），注意是
旁路不是清空——查询清空后用户手动折起来的布局原样回来。

零结果给空态文案：左栏搜到全白会像加载失败。

ProjectTreeProps 不变，不新增 prop——这是不进 Shell.tsx 的前提。"
```

---

### Task 7: `⌘K` 聚焦与 `Esc` 清空

**Files:**
- Modify: `web/src/app/tree/ProjectTree.tsx`（新增一个 `useEffect` 与输入框的 `onKeyDown`）
- Test: `web/src/app/tree/ProjectTree.test.tsx`（追加）

**Interfaces:**
- Consumes: Task 6 的 `searchRef` / `query` / `setQuery`
- Produces: 无

- [ ] **Step 1: 写失败的测试**

在 `ProjectTree.test.tsx` 追加：

```tsx
  it('⌘K 聚焦搜索框', () => {
    render(<ProjectTree {...props()} />)
    const input = screen.getByPlaceholderText('搜索项目、机器或任务')
    expect(document.activeElement).not.toBe(input)
    fireEvent.keyDown(window, { key: 'k', metaKey: true })
    expect(document.activeElement).toBe(input)
  })

  it('Ctrl+K 同样聚焦（非 mac）', () => {
    render(<ProjectTree {...props()} />)
    const input = screen.getByPlaceholderText('搜索项目、机器或任务')
    fireEvent.keyDown(window, { key: 'K', ctrlKey: true })
    expect(document.activeElement).toBe(input)
  })

  it('输入框内 Esc 清空并失焦', () => {
    render(<ProjectTree {...props()} />)
    const input = screen.getByPlaceholderText('搜索项目、机器或任务') as HTMLInputElement
    fireEvent.change(input, { target: { value: 'handoff' } })
    expect(input.value).toBe('handoff')
    fireEvent.keyDown(input, { key: 'Escape' })
    expect(input.value).toBe('')
    expect(document.activeElement).not.toBe(input)
  })

  it('单独按 k 不聚焦（不劫持普通输入）', () => {
    render(<ProjectTree {...props()} />)
    const input = screen.getByPlaceholderText('搜索项目、机器或任务')
    fireEvent.keyDown(window, { key: 'k' })
    expect(document.activeElement).not.toBe(input)
  })
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/tree/ProjectTree.test.tsx`
Expected: FAIL —— 前三条红（没有键盘监听，焦点不动；Esc 不清空）。第四条会误绿，那是正常的（还没实现时它本来就不聚焦），实现后它才真正有意义。

- [ ] **Step 3: 实现**

在 `ProjectTree` 组件内，`filtered` 之后加：

```ts
  // ⌘K / Ctrl+K 聚焦搜索框。
  //
  // 刻意挂在**冒泡阶段**（addEventListener 第三参不传 true），不是捕获阶段。
  // 这是一条让位次序：将来中央的终端 tab 拿到焦点时，xterm 会吞掉自己的
  // 按键；冒泡阶段监听意味着「任何调用 stopPropagation 的组件优先」——
  // 在终端里按 ⌘K 不该把焦点抢到左栏来。改成 capture 会当场破坏这一点。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey) || e.key.toLowerCase() !== 'k') return
      e.preventDefault()
      searchRef.current?.focus()
      searchRef.current?.select()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])
```

`import { useMemo, useRef, useState } from 'react'` 改成 `import { useEffect, useMemo, useRef, useState } from 'react'`。

给搜索框的 `<input>` 加 `onKeyDown`：

```tsx
            onKeyDown={(e) => {
              // Esc 清空并失焦：一次按键回到无过滤状态，不用手动全选删除
              if (e.key === 'Escape') {
                setQuery('')
                e.currentTarget.blur()
              }
            }}
```

- [ ] **Step 4: 跑测试确认它通过**

Run: `cd web && npx vitest run src/app/tree/ProjectTree.test.tsx`
Expected: PASS，四条新增用例全绿。

- [ ] **Step 5: 加意图注释**

核对两处（Step 3 已含）：

1. `useEffect` 上方：**冒泡而非捕获的让位次序**，含「改成 capture 会当场破坏这一点」这句警告——这是全计划最容易被后来人误改的一处
2. `onKeyDown` 里 Esc 分支的行内注释

- [ ] **Step 6: 提交**

```bash
cd web && npx vitest run
git add web/src/app/tree/ProjectTree.tsx web/src/app/tree/ProjectTree.test.tsx
git commit -m "feat(web): ⌘K 聚焦搜索框，Esc 清空并失焦

监听刻意挂在冒泡阶段而非捕获阶段：将来终端 tab 拿到焦点时 xterm 会吞掉
自己的按键，冒泡意味着「任何 stopPropagation 的组件优先」——在终端里按
⌘K 不该把焦点抢到左栏来。

原型里那个 <kbd>⌘K</kbd> 是纯装饰（全文件无键盘监听），这里让它名副其实。"
```

---

### Task 8: 左栏任务行圆点上色 + 工单角标换 token

**Files:**
- Modify: `web/src/app/tree/ProjectTree.tsx`（任务行两处 + 未归属分组任务行 + 工单角标）
- Test: `web/src/app/tree/ProjectTree.test.tsx`（追加）

**Interfaces:**
- Consumes: Task 2 的 `stateTone`、Task 3 的 `StateDot`、Task 1 的 `bg-state-intervention`
- Produces: 无

- [ ] **Step 1: 写失败的测试**

在 `ProjectTree.test.tsx` 追加。注意默认的 `props()` 工厂里 T1 的 `state` 是 `'running'`，要测干预态得自己构造：

```tsx
  it('左栏任务行的圆点跟随任务状态', () => {
    const p = props()
    p.tasks = [
      task({ id: 'T1', project_id: 'p1', machine: '', work_dir: '/w/b2-b3', name: '跑测试', state: 'running' }),
      task({ id: 'T2', project_id: 'p1', machine: '', work_dir: '/w/b2-b3', name: '等你答复的活', state: 'waiting_answer' }),
    ]
    const { container } = render(<ProjectTree {...p} />)
    expect(container.querySelectorAll('.bg-state-active')).toHaveLength(1)
    expect(container.querySelectorAll('.bg-state-intervention')).toHaveLength(1)
  })

  it('工单角标用状态 token，不用裸 amber', () => {
    const { container } = render(<ProjectTree {...props({ ticketCount: 3 })} />)
    const badge = screen.getByText('3')
    expect(badge.className).toContain('bg-state-intervention')
    expect(container.innerHTML).not.toContain('bg-amber-500')
  })
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/tree/ProjectTree.test.tsx`
Expected: FAIL —— 圆点仍是 `bg-muted-foreground/40`，角标仍是 `bg-amber-500`。

- [ ] **Step 3: 实现**

**(a) import 追加**：

```ts
import { stateTone } from '../board/columns'
import { StateDot } from '../board/StateDot'
```

**(b) 任务行圆点**（`ProjectTree.tsx` 里工作树任务行那处，约 :323）：把

```tsx
                                <span className="inline-block size-1.5 shrink-0 rounded-full bg-muted-foreground/40" />
```

换成

```tsx
                                <StateDot tone={stateTone(t.state)} />
```

**(c) 未归属分组的任务行**（约 :350）同样替换。

**(d) 工单角标**（约 :380）：把 `bg-amber-500` 换成 `bg-state-intervention`：

```tsx
            <span className="absolute -right-0.5 -top-0.5 min-w-4 rounded-full bg-state-intervention px-1 text-center text-[10px] leading-4 text-white">
```

- [ ] **Step 4: 跑测试确认它通过**

Run: `cd web && npx vitest run src/app/tree/`
Expected: PASS。

- [ ] **Step 5: 加意图注释**

在任务行圆点那处加一条：

```tsx
                                {/* 圆点跟随任务状态：同一个任务在看板上标着琥珀、
                                    在左栏是灰点的话，两个面自相矛盾 */}
```

在工单角标那处加一条：

```tsx
          {/* 角标用状态 token 而非裸 bg-amber-500：同一个左栏里两种橙
              （工单角标一种、干预态圆点另一种）看起来像 bug */}
```

- [ ] **Step 6: 提交**

```bash
cd web && npx vitest run
git add web/src/app/tree/ProjectTree.tsx web/src/app/tree/ProjectTree.test.tsx
git commit -m "feat(web): 左栏任务行圆点跟随状态，工单角标换状态 token

同一个任务在看板上标着琥珀、在左栏是灰点，两个面自相矛盾。工单角标的
裸 bg-amber-500 换成 --state-intervention，同一个左栏里两种橙看起来像 bug。"
```

---

### Task 9: 全量回归与原型对照

**Files:** 无代码改动（除非回归红了）

**Interfaces:**
- Consumes: Task 1-8 的全部产出
- Produces: 一份回归结果，交回时原文附上

- [ ] **Step 1: 跑全套回归**

Run:
```bash
cd web && npx vitest run && npm run typecheck && npm run lint && npm run build
```
Expected: 四条全绿。**红的不要绕过**——记下是哪条、报错原文是什么，修掉；确实修不掉就在交回时如实说明是哪条、为什么。

- [ ] **Step 2: 确认 Go 侧没被误伤**

本计划一行 Go 都不该改，这一步是核对。

Run:
```bash
cd .. && git diff --stat main -- '*.go' internal/ cmd/ | tail -5
```
Expected: **无输出**。有输出说明改错了文件，立刻还原。

- [ ] **Step 3: 确认没碰红线文件**

Run:
```bash
git status --porcelain && git diff --name-only main
```
Expected: 变更文件列表**不含** `web/src/app/shell/Shell.tsx`、`web/src/app/workbench/` 下任何文件、`web/src/api/` 下任何文件、`internal/proto/` 下任何文件。

含了就是撞了 PTY 并行线的红线（见 Global Constraints），必须还原。

- [ ] **Step 4: 逐条核对 spec §12 的验收判据**

打开 [spec §12](../specs/2026-08-12-w4-board-intervention-and-sidebar-search-design.md)，十条逐条核对。**第 1、3、5、9 条需要肉眼看页面**——执行者若没有浏览器，就把这四条如实标「未验（无浏览器）」，**不要**猜一个「通过」。其余六条能由测试覆盖的，写出对应的用例名。

- [ ] **Step 5: 写走查记录**

创建 `docs/superpowers/notes/2026-08-12-w4-board-search-check.md`，逐条列 spec §12 的十条判据 + 每条的结论（`已验` / `未验（原因）`）+ 回归命令的原文输出。

- [ ] **Step 6: 提交**

```bash
git add docs/superpowers/notes/2026-08-12-w4-board-search-check.md
git commit -m "docs(notes): W4 看板干预态 + 左栏搜索走查记录

十条验收判据逐条结论，回归命令输出原文附上。需肉眼看页面的四条
（第 1/3/5/9 条）如实标注验证状态。"
```

---

## Self-Review

**Spec 覆盖核对**（spec 各节 → 实现它的 task）：

| Spec 节 | Task |
|---|---|
| §2 状态色板 token | Task 1 |
| §2.3 顺带收敛 `bg-amber-500` | Task 8 |
| §3 `columns.ts` 拆分 | Task 2 |
| §3.4 `isWaitingAnswer` 保留 | Task 2 Step 5 |
| §4.1 `StateDot.tsx` | Task 3 |
| §4.2 `TaskCard` 三处改动 | Task 4 |
| §4.3 `TaskHeader` / `badge.tsx` | Task 1（badge 变体）+ Task 2（`stateBadgeVariant` 改档）；`TaskHeader.tsx` 本身不改代码，符合 spec |
| §4.4 左栏任务行圆点 | Task 8 |
| §5.1 落点与 `search.ts` | Task 5 + Task 6 |
| §5.3 匹配口径 | Task 5 |
| §5.4 可见性规则 | Task 5 |
| §5.5 旁路 `collapsed` | Task 6 |
| §5.6 「项目 N」 | Task 5（计数）+ Task 6（渲染） |
| §5.7 未归属与空态 | Task 5 + Task 6 |
| §6 `⌘K` | Task 7 |
| §7 注释义务 | 每个 task 的「加意图注释」步骤 |
| §8 测试 | 各 task 的 Step 1 |
| §10 冲突面 | Task 9 Step 3 |
| §12 验收判据 | Task 9 Step 4-5 |

无遗漏。

**类型一致性核对**：`StateTone` 在 Task 2 定义，Task 3（`DOT_CLASS` / `TEXT_CLASS` 的 `Record<StateTone, string>`、`StateDot` 的 prop）、Task 8（`stateTone(t.state)` 的返回）消费，名字与成员一致。`TreeFilter` 在 Task 5 定义，Task 6 消费 `filtered.projects` / `projectCount` / `unassignedTasks` / `unownedNames` / `isEmpty` / `query`，六个字段全部在接口里。`filterTree(tree, tasks, rawQuery)` 三参签名在 Task 5、Task 6 一致。`TaskCard` 在 Task 4 从私有改为导出，仅 Task 4 的测试消费。

**已知的执行期风险**（不是 placeholder，是需要执行者当场判断的两处）：

1. **Task 4 Step 1** 里 `TaskCard` 是否需要导出，取决于 `BoardPage` 能否脱离 router 直接渲染。计划已给出判据与推荐（导出 `TaskCard` 直接测）。
2. **Task 6 Step 4** 提示原有 `ProjectTree` 用例可能因新增「项目」小标题撞上 `getByText` 的 multiple matches。计划已明确修法方向（收紧选择器，**不要**改文案迁就测试）。
