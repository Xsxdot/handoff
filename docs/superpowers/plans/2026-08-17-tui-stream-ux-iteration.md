# TUI 会话流 UX 迭代（真机走查五项反馈）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复真机走查发现的五项问题：回合跳转失灵、滚不到头、连续工具行刷屏、工具名英文裸奔、进行中无状态反馈。

**Architecture:** 全部在 `web/src/app/task/` 内：跳转逻辑下沉进 ConversationStream（imperative handle + 未加载回合自动回翻）；近顶自动加载更早；新增 `streamGroups.ts` 纯函数做连续工具块分组；ToolCard 加中文名映射；流末尾加运行中指示行。不动后端、不动数据钩子。

**Tech Stack:** React 19 + TypeScript + Tailwind + vitest（现状同栈）。

## Global Constraints

- 注释中文写「为什么」；新文件带文件头（职责+边界）；导出函数带 doc 注释
- 前端禁止 `console.log`；不引入新依赖；视觉沿用现有 token 与元数据行语言
- 验证命令：`cd web && npx vitest run src/app/task/ && npm run typecheck && npm run lint`
- 每 task 完成即 commit
- 基线分支：`feat/tui-conversational-redesign-2`（对话式重构已合入的状态）

---

### Task 1: streamGroups——连续工具块分组纯函数

**Files:**
- Create: `web/src/app/task/streamGroups.ts`
- Create: `web/src/app/task/streamGroups.test.ts`

**Interfaces:**
- Consumes: `Block` / `ToolBlock`（`frames.ts` 现有导出）
- Produces（Task 2 消费）:

```ts
export interface ToolGroupBlock { kind: 'toolGroup'; key: string; tools: ToolBlock[]; failed: number; pending: number }
export type StreamItem = Block | ToolGroupBlock
export const minGroupSize = 3
export function groupBlocks(blocks: Block[]): StreamItem[]
```

- [ ] **Step 1: 写失败测试**——`web/src/app/task/streamGroups.test.ts`：

```ts
// streamGroups.test.ts —— 连续工具块分组：≥3 折组、打断重计、失败/未回音计数。
import { describe, expect, it } from 'vitest'
import { groupBlocks, minGroupSize } from './streamGroups'
import type { Block } from './frames'

// tool 造一个工具块；status 缺省 'ok'
function tool(key: string, status: string | null = 'ok'): Block {
  return {
    kind: 'tool', key, turn: 1, tool: 'commandExecution', input: 'x',
    inputTruncated: false, inputBytes: 0, status, output: '',
    outputTruncated: false, outputBytes: 0,
  }
}
const text = (key: string): Block => ({ kind: 'text', key, turn: 1, text: '正文' })

describe('groupBlocks', () => {
  it('连续 ≥3 个工具块折成一组，计数正确', () => {
    const items = groupBlocks([tool('a'), tool('b', 'error'), tool('c', null)])
    expect(items).toHaveLength(1)
    expect(items[0]).toMatchObject({ kind: 'toolGroup', failed: 1, pending: 1 })
    expect((items[0] as { tools: unknown[] }).tools).toHaveLength(3)
  })
  it('不足 minGroupSize 不折组，原样透出', () => {
    const items = groupBlocks([tool('a'), tool('b')])
    expect(items.map((i) => i.kind)).toEqual(['tool', 'tool'])
    expect(minGroupSize).toBe(3)
  })
  it('被非工具块打断的两段各自独立计数', () => {
    const items = groupBlocks([tool('a'), tool('b'), tool('c'), text('t'), tool('d'), tool('e')])
    expect(items.map((i) => i.kind)).toEqual(['toolGroup', 'text', 'tool', 'tool'])
  })
  it('组 key 取首个成员 key，稳定不随重渲染变化', () => {
    const items = groupBlocks([tool('f9'), tool('f10'), tool('f11')])
    expect((items[0] as { key: string }).key).toBe('g-f9')
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/task/streamGroups.test.ts`
Expected: FAIL（模块不存在）

- [ ] **Step 3: 实现**——`web/src/app/task/streamGroups.ts`：

```ts
// streamGroups.ts —— 会话流的连续工具块分组（纯函数）。
//
// 职责：把连续 ≥minGroupSize 个 tool 块折成一个 toolGroup，供渲染层做
// 「执行了 N 步操作」的折叠展示——真机走查发现执行器动辄连发十几条命令，
// 逐行平铺会把正文淹掉。
// 边界：
//   - 只分组 tool 块；text/thinking/event/turn 一律打断分组（它们承载因果，
//     不能被折进组里）
//   - 不做展开状态管理（那是渲染层的事），也不判断任务状态
import type { Block, ToolBlock } from './frames'

// minGroupSize 是折组的最小连续条数：1-2 条平铺反而更直观，3 条起才值得折。
export const minGroupSize = 3

// ToolGroupBlock 是一组连续工具块。failed/pending 供组行摘要与色调用：
// failed = status 为非 ok 非 null 的条数；pending = 尚无回音（null）的条数。
export interface ToolGroupBlock {
  kind: 'toolGroup'
  key: string
  tools: ToolBlock[]
  failed: number
  pending: number
}

// StreamItem 是渲染层消费的流单元：原始块或工具组。
export type StreamItem = Block | ToolGroupBlock

// groupBlocks 把块序列折成流单元序列。组 key 取首成员 key 加 g- 前缀，
// 保证同一段数据重渲染时 key 稳定（React 不重挂）。
export function groupBlocks(blocks: Block[]): StreamItem[] {
  const items: StreamItem[] = []
  let run: ToolBlock[] = []

  const flush = () => {
    if (run.length >= minGroupSize) {
      items.push({
        kind: 'toolGroup',
        key: `g-${run[0].key}`,
        tools: run,
        failed: run.filter((t) => t.status !== null && t.status !== 'ok').length,
        pending: run.filter((t) => t.status === null).length,
      })
    } else {
      items.push(...run)
    }
    run = []
  }

  for (const b of blocks) {
    if (b.kind === 'tool') {
      run.push(b)
      continue
    }
    flush()
    items.push(b)
  }
  flush()
  return items
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/task/streamGroups.test.ts`
Expected: PASS（4 用例）

- [ ] **Step 5: 自检注释**：文件头/导出注释齐，minGroupSize 的 why 写了。

- [ ] **Step 6: Commit**

```bash
git add web/src/app/task/streamGroups.ts web/src/app/task/streamGroups.test.ts
git commit -m "feat(web): 连续工具块分组纯函数"
```

---

### Task 2: ConversationStream——跳转下沉、近顶自动回翻、分组渲染、运行中指示

**Files:**
- Modify: `web/src/app/task/ConversationStream.tsx`
- Modify: `web/src/app/task/ConversationStream.test.tsx`
- Modify: `web/src/app/workbench/TuiTab.tsx`（跳转改走 ref）

**Interfaces:**
- Consumes: `groupBlocks`/`ToolGroupBlock`（Task 1）、`ToolCard`（Task 3 改中文名，本 task 不动它）
- Produces:

```ts
export interface ConversationStreamHandle { jumpToTurn(turn: number): void }
// ConversationStream 改为 forwardRef<ConversationStreamHandle, ConversationStreamProps>
// Props 新增：active: boolean（frames 流是否仍在跟随，来自 useFramesStream）
```

TuiTab 改动：`const streamRef = useRef<ConversationStreamHandle>(null)`；`onJumpTurn={(t) => streamRef.current?.jumpToTurn(t)}`；把 `active` 从 useFramesStream 透传给 ConversationStream。

**行为规格（全部实现在 ConversationStream 内）：**

1. **jumpToTurn(turn)**：
   - 锚点 `turn-${taskId}-${turn}` 已在 DOM：`stickBottom.current = false` **先置**，再 `scrollIntoView({ block: 'start' })`——先置是为了消除「跳上去又被新帧拽回底部」的竞态（这就是走查里「点了没反应」的一半根因）。
   - 锚点不在（回合还没加载）：记 `pendingTurnRef.current = turn`，触发一次 `handleLoadEarlier()`；在 blocks 变化的 effect 里检查 pendingTurn 锚点是否已出现——出现就跳并清 pending；没出现且 `startOffset > 0 && !atCap` 就继续触发回翻；到头/到上限仍没有就清 pending 放弃（锚点条只覆盖已加载范围的既有语义，此时什么都不做是诚实的）。
2. **近顶自动回翻**：`onScroll` 里 `el.scrollTop < 200 && startOffset > 0 && !atCap && !loadingEarlier` 时自动 `handleLoadEarlier()`（prepend 滚动补偿已有，复用）。「↑ 加载更早」按钮保留（可达性兜底）。
3. **分组渲染**：`const items = useMemo(() => groupBlocks(blocks), [blocks])`，渲染 switch 增加 `toolGroup` 分支：

```tsx
case 'toolGroup': {
  // 组内有未回音工具且任务在跑：正在发生的事不能折起来，整组平铺
  if (b.pending > 0 && taskState === 'running') {
    return b.tools.map((t) => <ToolCard key={t.key} block={t} taskState={taskState} />)
  }
  return <ToolGroupRow key={b.key} group={b} taskState={taskState} />
}
```

`ToolGroupRow` 定义在本文件内（不单独建文件——它只被这里用）：

```tsx
// ToolGroupRow 渲染一组折叠的连续工具行：一行摘要，点开平铺原行。
function ToolGroupRow({ group, taskState }: { group: ToolGroupBlock; taskState: string }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="my-1 text-xs">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-2 py-0.5 text-muted-foreground hover:text-foreground"
      >
        <span className={cn('w-3.5 shrink-0 text-center transition-transform', open && 'rotate-90')}>▸</span>
        执行了 {group.tools.length} 步操作
        {group.failed > 0 && <span className="text-destructive">（{group.failed} 失败）</span>}
        {group.pending > 0 && <span className="text-amber-600 dark:text-amber-500">（{group.pending} 未回音）</span>}
      </button>
      {open && (
        <div className="ml-[7px] border-l-2 border-border pl-3">
          {group.tools.map((t) => <ToolCard key={t.key} block={t} taskState={taskState} />)}
        </div>
      )}
    </div>
  )
}
```

4. **运行中指示**：流末尾（blocks 渲染之后）按状态渲染一行：

```tsx
{taskState === 'running' && active && (
  <div className="my-2 flex items-center gap-2 text-xs text-muted-foreground">
    <span className="size-[7px] shrink-0 animate-pulse rounded-full bg-green-600" />
    模型工作中…
    {staleSeconds >= 15 && <span>（已 {staleSeconds}s 没有新输出）</span>}
  </div>
)}
{taskState === 'waiting_answer' && (
  <MetaRow glyph="⚠" tone="warn">等待工单裁决——入口在左栏底部的工单面板</MetaRow>
)}
```

`staleSeconds` 实现：`lastFrameAtRef` 在 blocks 变化 effect 里记 `Date.now()`；running 时每秒 `setInterval` 刷一个 `now` state（非 running 不挂 interval，卸载清理）。为什么用本地时钟不用帧 ts：保活换行不产帧，本地「收到最后一块」的时刻才是用户关心的「它还活着吗」。

- [ ] **Step 1: 写失败测试**——`ConversationStream.test.tsx` 追加（现有用例保留；props 增加 `active: false` 到 base）：

```tsx
it('连续 ≥3 工具块折叠成组行，点开平铺', () => {
  const tools = ['a', 'b', 'c'].map((k) => ({
    kind: 'tool', key: k, turn: 1, tool: 'commandExecution', input: `cmd-${k}`,
    inputTruncated: false, inputBytes: 0, status: 'ok', output: '',
    outputTruncated: false, outputBytes: 0,
  })) as Block[]
  render(<ConversationStream {...base} blocks={tools} />)
  const row = screen.getByRole('button', { name: /执行了 3 步操作/ })
  expect(screen.queryByText('cmd-a')).not.toBeInTheDocument()
  fireEvent.click(row)
  expect(screen.getByText('cmd-a')).toBeInTheDocument()
})

it('组内含失败时摘要标红失败数', () => {
  const mk = (k: string, status: string) => ({
    kind: 'tool', key: k, turn: 1, tool: 'x', input: k, inputTruncated: false,
    inputBytes: 0, status, output: '', outputTruncated: false, outputBytes: 0,
  }) as Block
  render(<ConversationStream {...base} blocks={[mk('a', 'ok'), mk('b', 'error'), mk('c', 'ok')]} />)
  expect(screen.getByText(/1 失败/)).toBeInTheDocument()
})

it('running 且流活跃时显示运行中指示', () => {
  render(<ConversationStream {...base} blocks={[]} taskState="running" active />)
  expect(screen.getByText(/模型工作中/)).toBeInTheDocument()
})

it('jumpToTurn 对未加载回合触发回翻', () => {
  const onLoadEarlier = vi.fn()
  const ref = createRef<ConversationStreamHandle>()
  render(
    <ConversationStream {...base} ref={ref} blocks={[]} startOffset={100} onLoadEarlier={onLoadEarlier} />,
  )
  ref.current!.jumpToTurn(1)
  expect(onLoadEarlier).toHaveBeenCalled()
})
```

（`createRef` 从 react import；`fireEvent` 从 @testing-library/react。近顶自动回翻不在 jsdom 里断言——jsdom 无真实布局，scrollTop/scrollHeight 恒 0，硬造断言只会测到 mock 自己；该行为走 Task 5 真机走查。）

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/task/ConversationStream.test.tsx`
Expected: FAIL（active prop 不存在、组行不存在、handle 未导出）

- [ ] **Step 3: 实现**：按上面行为规格改 ConversationStream（forwardRef + useImperativeHandle；文件头注释同步更新职责清单），TuiTab 接 ref 与 active。jsdom 无 `scrollIntoView`：跳转处写 `el.scrollIntoView?.({ block: 'start' })` 可选调用，测试不用 mock。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/task/ && npm run typecheck`
Expected: PASS 全绿

- [ ] **Step 5: 自检注释**：jumpToTurn 的竞态注释（stickBottom 先置 false 的 why）、pendingTurn 放弃条件的 why、staleSeconds 用本地时钟的 why 都要在。

- [ ] **Step 6: Commit**

```bash
git add web/src/app/task/ConversationStream.tsx web/src/app/task/ConversationStream.test.tsx web/src/app/workbench/TuiTab.tsx
git commit -m "feat(web): 会话流跳转下沉与近顶自动回翻、工具组折叠、运行中指示"
```

---

### Task 3: ToolCard 中文工具名

**Files:**
- Modify: `web/src/app/task/ToolCard.tsx`
- Modify: `web/src/app/task/blocks.test.tsx`

**Interfaces:**
- Consumes: 现有 ToolCard 结构
- Produces: 展示层映射，无接口变化

- [ ] **Step 1: 写失败测试**——`blocks.test.tsx` 追加：

```tsx
describe('ToolCard 工具名中文化', () => {
  const mk = (toolName: string) => ({
    kind: 'tool', key: 'k1', turn: 1, tool: toolName, input: 'x', inputTruncated: false,
    inputBytes: 0, status: 'ok', output: '', outputTruncated: false, outputBytes: 0,
  }) as ToolBlock
  it('已知工具名映射为中文', () => {
    render(<ToolCard block={mk('commandExecution')} taskState="waiting_review" />)
    expect(screen.getByText('跑命令')).toBeInTheDocument()
  })
  it('未知工具名原样透出', () => {
    render(<ToolCard block={mk('someNewTool')} taskState="waiting_review" />)
    expect(screen.getByText('someNewTool')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/task/blocks.test.tsx`
Expected: FAIL（显示的是 commandExecution 原文）

- [ ] **Step 3: 实现**——ToolCard.tsx 顶部加映射，渲染工具名处改 `TOOL_LABEL[block.tool] ?? block.tool`：

```ts
// TOOL_LABEL 把各家 adapter 的工具名翻成中文。未知名字原样透出（契约会演进，
// 前端比后端旧是常态——与 eventPhrase 同一条纪律）。
// 名单来源：codex（commandExecution/fileChange）、claude（Bash/Read/Edit/Write/
// Grep/Glob）、opencode 与 grok 的常见工具名；四家帧质量走查时按需补。
const TOOL_LABEL: Record<string, string> = {
  commandExecution: '跑命令',
  fileChange: '文件变更',
  Bash: '跑命令',
  bash: '跑命令',
  Read: '读文件',
  read: '读文件',
  Edit: '改文件',
  edit: '改文件',
  Write: '写文件',
  write: '写文件',
  Grep: '搜索',
  grep: '搜索',
  Glob: '找文件',
  glob: '找文件',
  webSearch: '搜网页',
  todowrite: '记待办',
  todoread: '看待办',
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/task/blocks.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add web/src/app/task/ToolCard.tsx web/src/app/task/blocks.test.tsx
git commit -m "feat(web): 工具名中文化映射，未知名原样透出"
```

---

### Task 4: TuiHeader 回合下拉的加载语义提示

**Files:**
- Modify: `web/src/app/task/TuiHeader.tsx`
- Modify: `web/src/app/task/TuiHeader.test.tsx`

**Interfaces:** 无签名变化。下拉行为对齐 Task 2：点已加载回合直接跳；`turnsPartial` 时下拉底部的提示文案改为「更早的回合会边跳边加载」（Task 2 的 jumpToTurn 已支持自动回翻，旧文案「更早的需先加载」已不成立）。

- [ ] **Step 1: 改测试**——TuiHeader.test.tsx 中 turnsPartial 相关断言（若无则新增）：

```tsx
it('turnsPartial 时下拉给出自动加载提示', () => {
  render(<TuiHeader {...base} turnsPartial />)
  fireEvent.click(screen.getByRole('button', { name: /回合 2/ }))
  expect(screen.getByText(/边跳边加载/)).toBeInTheDocument()
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/task/TuiHeader.test.tsx`
Expected: FAIL

- [ ] **Step 3: 实现**：下拉底部 `turnsPartial` 提示文案改为「仅列出已加载回合；更早的回合会边跳边加载」。

- [ ] **Step 4: 跑测试确认通过 + Commit**

Run: `cd web && npx vitest run src/app/task/TuiHeader.test.tsx`

```bash
git add web/src/app/task/TuiHeader.tsx web/src/app/task/TuiHeader.test.tsx
git commit -m "fix(web): 回合下拉提示对齐自动回翻语义"
```

---

### Task 5: 全量回归

- [ ] **Step 1: 全量验证**

Run: `cd web && npx vitest run && npm run typecheck && npm run lint`
Expected: 全绿（lint 仅既有 warnings）

- [ ] **Step 2: 终审自检**（instrumenting-code 清单）：新文件头注释、竞态/放弃条件的 why 注释、无 console.log。

- [ ] **Step 3: Commit（若终审有修）后停在 waiting_review**——真机走查（回合跳转、滚到头、分组折叠、中文名、运行中指示五项）由审核者本地执行。

## Self-Review 记录

- 五项反馈全覆盖：①②→Task 2（+Task 4 文案）；③→Task 1+2；④→Task 3；⑤→Task 2。
- 无占位；`ConversationStreamHandle`/`ToolGroupBlock`/`groupBlocks`/`active` 跨任务签名一致。
- jsdom 测不了的滚动行为显式声明走真机走查，不造假断言。
