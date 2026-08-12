# W4b Web 回合渲染 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在任务详情页新增一个结构化回合时间线，消费 W4a 的 `frames.jsonl` 契约与 `GET /api/tasks/{id}/frames`，让审阅者一眼分清模型「在想什么 / 说什么 / 做了什么 / 得到了什么」。

**Architecture:** 解析、I/O、展示三分。`frames.ts` 是纯函数（ndjson → 可渲染块），能脱离浏览器被穷举测试；`useFramesStream.ts` 只管加载 / 跟随 / 回翻；`TimelinePanel.tsx` 与五个块组件只负责「给定块，画出来」。时间线与现有「实况正文」并存、一个开关切换，`RenderPanel.tsx` 与 `useRenderStream.ts` 一行不改。

**Tech Stack:** React 19 + TypeScript 5.8 + Vite 6 + vitest 4 + @testing-library/react + Tailwind 4 + shadcn 风格的 `@/components/ui/*`。**不新增任何依赖**。

## Global Constraints

- **前置：本计划必须等 W4a 合并后才能开工。** `GET /api/tasks/{id}/frames` 与 `web/src/api/testdata/Frame.json` 都是 W4a 的产出，没有它们无法真机验证，Task 1 也无从写起。
- **只动 `web/`。** 不碰 `internal/`、不碰 `cmd/`，本期零后端改动（spec §1.2）。
- **不新增第三方依赖。** 不引 markdown 渲染器、不引虚拟化库、不引高亮库（spec §6）。`web/package.json` 与 `package-lock.json` 必须零改动。
- **`web/src/app/task/RenderPanel.tsx` 与 `web/src/app/task/useRenderStream.ts` 一行不改**（spec §2）。它们原样作为切换开关的另一半保留。
- **`internal/proto/` 与 `web/src/api/testdata/` 只读。** 那是 W4a 的独占范围；`Frame.json` 是生成物，**绝不手改、绝不跑 `-update`**。发现契约不够用要停下上报，不要自行改结构体。
- **配对键是 `(turn, part)`，不是 `ref_seq`。** W4a §3.2 把 `ref_seq` 留给 `event` 帧指向 events 表的库级 seq；`tool_call` 与其 `tool_result` 用同一个 `part`，而 `part` 只在回合内唯一。
- **已加载帧数硬上限 `maxLoadedFrames = 5000`**（spec §5.5）。
- **单页请求量 `pageBytes = 65536`**（spec §5.2 / §5.3），`offset` 与 `tail` 的单位都是**字节**。
- **文案一律简体中文**，与既有面板同风格（`实况正文` / `事件流` / `等待模型输出…`）。
- **可观测性的落法（instrumenting-code 在前端的映射）**：这里没有日志文件，`console.log` / `console.warn` **不算**观测手段，也不要用。前端的「关键节点日志」等价物是**把状态暴露到 UI 且绝不静默吞**——每个错误分支、每次降级、每个边界到顶都必须在界面上有对应的可见状态（错误条 + 重试、坏行计数、上限提示、跟随/已结束徽章、「未返回」而非假装「进行中」）。纯函数侧的等价物是**把降级计入返回值**（`scanLines` 的 `bad` 计数）。每个实现任务都带一个「可观测性」步骤，逐条点名要暴露哪些状态；漏一条即为未完成。
- 每个实现任务都带一个「注释」步骤：新文件写职责与边界的文件头注释，导出函数写参数/返回/注意事项，非显然的分支写「为什么」。

---

## File Structure

| 文件 | 职责 | 边界 |
|---|---|---|
| `web/src/api/types.ts`（改） | 新增 `Frame` 与 `FrameType` 类型 | 只镜像线格式，不含任何渲染概念 |
| `web/src/api/contract.test.ts`（改） | 新增 `Frame.json` 的契约断言 | 只断言字段与 omitempty 边界 |
| `web/src/app/task/frames.ts`（新） | 纯函数：`scanLines` / `buildBlocks` / `toolState` / `turnsOf` | 不碰 DOM、不发请求、不 import react |
| `web/src/app/task/frames.test.ts`（新） | 上面四个函数的穷举边界测试 | — |
| `web/src/app/task/codeText.tsx`（新） | `splitFences` / `splitInline` 纯函数 + `CodeText` 组件 | 范围钉死在三段围栏 + 行内 code |
| `web/src/app/task/codeText.test.tsx`（新） | 围栏切分的边界测试 | — |
| `web/src/app/task/TextBlock.tsx`（新） | 正文段落块 | 永远展开 |
| `web/src/app/task/ThinkingBlock.tsx`（新） | 思维链块 | 默认折叠 |
| `web/src/app/task/ToolCard.tsx`（新） | 工具卡（名字 / 参数摘要 / 状态徽章 / 输入输出） | 默认折叠 |
| `web/src/app/task/EventMark.tsx`（新） | 事件打断标记 | **不可展开、不可操作** |
| `web/src/app/task/UnknownBlock.tsx`（新） | 未知 type 的中性条目 | 可展开看原始 JSON |
| `web/src/app/task/blocks.test.tsx`（新） | 五个块组件的行为测试 | 只测行为不测样式 |
| `web/src/app/task/useFramesStream.ts`（新） | 加载 / 跟随 / 回翻的 hook | 只管 I/O 与状态，不决定长什么样 |
| `web/src/app/task/TimelinePanel.tsx`（新） | 容器：锚点条、加载更早、跟随徽章、原始视图开关 | 不解析原始文本 |
| `web/src/app/task/TimelinePanel.test.tsx`（新） | 容器行为测试（hook 用 `vi.mock` 打桩） | — |
| `web/src/app/task/TaskPage.tsx`（改） | 左列槽位从 `RenderPanel` 换成 `TimelinePanel` | 只改这一处 |

**`UnknownBlock.tsx` 是对 spec §7 文件表的一处补充**：§7 列了四个块组件，但 §3.2 要求未知 type 也渲染成一种块。给它一个独立小文件，与其余四个同构，而不是塞进容器里。

**切换开关的归属**：`TimelinePanel` 顶部渲染开关；切到原始视图时它渲染 `<RenderPanel taskId={taskId} />`，`RenderPanel` 保持零改动。代价是切换会卸载/重建对侧的流（原始视图重新从 `tail=65536` 开始）——这是刻意的：两个视图各自维护加载位置（spec §2），而让两条常驻连接同时开着换取「切回去还在原位」是更坏的交易。

---

## Task 1: Frame 类型与契约断言

**Files:**
- Modify: `web/src/api/types.ts`（在文件末尾追加）
- Modify: `web/src/api/contract.test.ts`

**Interfaces:**
- Consumes: `web/src/api/testdata/Frame.json`（W4a Task 1 的生成物，只读）
- Produces: `Frame` interface 与 `FrameType` union，从 `web/src/api/types.ts` 导出。字段：`seq: number`、`ts: string`、`turn: number`、`type: string`、`part?: string`、`delta?: string`、`tool?: string`、`input?: string`、`output?: string`、`status?: string`、`truncated?: boolean`、`bytes?: number`、`ref_seq?: number`、`event?: string`、`reason?: string`

- [ ] **Step 1: 确认前置已就位**

Run:
```bash
cd web && cat src/api/testdata/Frame.json
```
Expected: 文件存在，内容形如
```json
{
  "seq": 42,
  "ts": "2026-08-11T10:30:00+08:00",
  "turn": 2,
  "type": "tool_result",
  "part": "toolu_01ABCdefGHIjklMNOpqrs",
  "output": "go: downloading …\n…（已截断）…\nFAIL\texit status 1",
  "status": "error",
  "truncated": true,
  "bytes": 193422
}
```
文件不存在说明 W4a 还没合并——**停下上报，不要自己造这个 fixture**。

- [ ] **Step 2: 写失败的契约测试**

在 `web/src/api/contract.test.ts` 顶部的 import 区加：
```ts
import frameFixture from './testdata/Frame.json'
```
并在 `import { type ActiveTask, …` 那个类型 import 列表里加上 `type Frame,`（保持字母序，插在 `type Event,` 之后）。

在文件末尾追加：
```ts
describe('W4a 帧契约', () => {
  it('Frame：可解析为 Frame 类型，omitempty 字段缺席', () => {
    const f: Frame = frameFixture
    expect(f.seq).toBe(42)
    expect(f.turn).toBe(2)
    expect(f.type).toBe('tool_result')
    expect(f.part).toBe('toolu_01ABCdefGHIjklMNOpqrs')
    expect(f.status).toBe('error')
    expect(f.truncated).toBe(true)
    expect(f.bytes).toBe(193422)
    expect(f.ts).toMatch(/^2026-/)
    // omitempty 的边界：这六个键必须**缺席**而不是空值。
    // 前端据此可以用 `f.delta ?? ''` 安全兜底；若它们变成 "" 或 null，
    // 说明 Go 侧丢了 omitempty，解析侧的假设就塌了。
    for (const key of ['delta', 'tool', 'input', 'ref_seq', 'event', 'reason']) {
      expect(Object.keys(frameFixture)).not.toContain(key)
    }
  })

  it('Frame：可选字段可以显式赋 undefined（指针语义镜像）', () => {
    const f: Frame = { ...frameFixture, part: undefined, status: undefined, bytes: undefined }
    expect(f.part).toBeUndefined()
  })
})
```

- [ ] **Step 3: 跑测试确认失败**

Run: `cd web && npx vitest run src/api/contract.test.ts`
Expected: FAIL —— `'./types'` 没有导出 `Frame`（TS 解析错误）

- [ ] **Step 4: 加类型**

在 `web/src/api/types.ts` 末尾追加：
```ts
// FrameType 是结构化回合帧的类型（W4a §3.2）。
//
// 刻意用 `string` 而不是收窄的 union：前端比后端晚部署是常态，契约新增一种帧
// 时旧前端必须还能解析并渲染成中性条目，而不是在类型层就把它判为非法。
// 已知取值集中在 KNOWN_FRAME_TYPES，供渲染层分发。
export type FrameType = string

// KNOWN_FRAME_TYPES 是本前端版本认识的帧类型。不在其中的一律走「未知类型」分支。
export const KNOWN_FRAME_TYPES = ['text', 'reasoning', 'tool_call', 'tool_result', 'event', 'turn_start'] as const

// Frame 是 frames.jsonl 的一行，也是 GET /api/tasks/{id}/frames 流的一行。
//
// 与 internal/proto.Frame 一一对应。Go 侧带 omitempty 的字段在这里都是可选（?）
// 而不是 `| null`——它们缺席而不是取空值（contract.test.ts 钉住了这一点）。
//
// 两套 seq 不要混用：`seq` 是**任务内**从 1 开始的帧行号；`ref_seq` 只出现在
// `event` 帧上，指向 events 表的**库级**自增 seq。
//
// 配对与拼接都靠 `part`：`text`/`reasoning` 按 part 拼接增量，`tool_call` 与其
// `tool_result` 用同一个 part 配对。part 只在**同一回合内**唯一，跨回合会重复，
// 所以任何以 part 为键的索引都必须带上 turn。
export interface Frame {
  seq: number
  ts: string
  turn: number
  type: FrameType
  part?: string
  delta?: string
  tool?: string
  input?: string
  output?: string
  status?: string
  truncated?: boolean
  bytes?: number
  ref_seq?: number
  event?: string
  reason?: string
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `cd web && npx vitest run src/api/contract.test.ts`
Expected: PASS（含既有全部用例）

- [ ] **Step 6: typecheck**

Run: `cd web && npx tsc -b`
Expected: 无输出

- [ ] **Step 7: 注释自检**

确认 `Frame` 与 `FrameType` 都带了导出注释，且注释解释了三件非显然的事：为什么 `FrameType` 不收窄、两套 seq 不混用、part 只在回合内唯一。缺任一条补上。

- [ ] **Step 8: Commit**

```bash
git add web/src/api/types.ts web/src/api/contract.test.ts
git commit -m "feat(web): Frame 帧类型与契约断言"
```

---

## Task 2: frames.ts 的行扫描（半行缓冲 + 坏行计数）

**Files:**
- Create: `web/src/app/task/frames.ts`
- Create: `web/src/app/task/frames.test.ts`

**Interfaces:**
- Consumes: `Frame` from `../../api/types`（Task 1）
- Produces:
  - `export const maxLoadedFrames = 5000`
  - `export interface ScanResult { frames: Frame[]; bad: number; rest: string }`
  - `export function scanLines(buffered: string, chunk: string): ScanResult`

- [ ] **Step 1: 写失败的测试**

创建 `web/src/app/task/frames.test.ts`：
```ts
// frames.ts 的边界测试：半行切分、坏行、保活空行。
//
// 这些是本期唯一有真实逻辑复杂度的地方，所以按穷举写：一行坏数据不该让整条
// 时间线白屏，但也不能静默——每条降级都要能从返回值里数出来。
import { describe, expect, it } from 'vitest'
import { scanLines } from './frames'

const line = (o: Record<string, unknown>) => JSON.stringify(o) + '\n'

describe('scanLines 半行与坏行', () => {
  it('完整行全部解析，rest 为空', () => {
    const text = line({ seq: 1, ts: 't', turn: 1, type: 'turn_start', reason: 'dispatch' }) +
      line({ seq: 2, ts: 't', turn: 1, type: 'text', part: 'p01', delta: '你好' })
    const r = scanLines('', text)
    expect(r.frames.map((f) => f.seq)).toEqual([1, 2])
    expect(r.bad).toBe(0)
    expect(r.rest).toBe('')
  })

  it('一帧被拆在两次 chunk 之间：先留在 rest，补齐后解析', () => {
    const whole = line({ seq: 7, ts: 't', turn: 1, type: 'text', part: 'p01', delta: '半行' })
    const cut = Math.floor(whole.length / 2)
    const first = scanLines('', whole.slice(0, cut))
    expect(first.frames).toHaveLength(0)
    expect(first.bad).toBe(0)
    expect(first.rest).toBe(whole.slice(0, cut))

    const second = scanLines(first.rest, whole.slice(cut))
    expect(second.frames.map((f) => f.seq)).toEqual([7])
    expect(second.rest).toBe('')
  })

  it('非 JSON 的坏行：跳过并计数，不影响同批其余帧', () => {
    const text = line({ seq: 1, ts: 't', turn: 1, type: 'text', delta: 'a' }) +
      '这不是 JSON\n' +
      line({ seq: 2, ts: 't', turn: 1, type: 'text', delta: 'b' })
    const r = scanLines('', text)
    expect(r.frames.map((f) => f.seq)).toEqual([1, 2])
    expect(r.bad).toBe(1)
  })

  it('是 JSON 但缺 seq / 缺 type / 不是对象：都算坏行', () => {
    const text = line({ ts: 't', turn: 1, type: 'text' }) +
      line({ seq: 3, ts: 't', turn: 1 }) +
      '[1,2,3]\n' +
      'null\n'
    const r = scanLines('', text)
    expect(r.frames).toHaveLength(0)
    expect(r.bad).toBe(4)
  })

  it('保活空行与纯空白行不计坏行（agentd follow 空闲每 20s 发一个换行）', () => {
    const text = '\n' + '   \n' + line({ seq: 9, ts: 't', turn: 1, type: 'text', delta: 'x' }) + '\n'
    const r = scanLines('', text)
    expect(r.frames.map((f) => f.seq)).toEqual([9])
    expect(r.bad).toBe(0)
  })

  it('没有任何换行时整段留在 rest，不误判为坏行', () => {
    const r = scanLines('', '{"seq":1')
    expect(r.frames).toHaveLength(0)
    expect(r.bad).toBe(0)
    expect(r.rest).toBe('{"seq":1')
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/task/frames.test.ts`
Expected: FAIL —— 找不到模块 `./frames`

- [ ] **Step 3: 写实现**

创建 `web/src/app/task/frames.ts`：
```ts
// frames.ts —— frames.jsonl 的解析与聚合（纯函数）。
//
// 职责：
//   - scanLines: ndjson 文本增量 → 帧数组 + 坏行计数 + 残留半行
//   - buildBlocks: 帧数组 → 可渲染块（delta 按 part 合并、tool_call/tool_result
//     按 (turn, part) 配对、未知 type 保留成中性块）
//   - toolState: 工具卡状态判定
//   - turnsOf: 已加载帧里出现过的回合号
//
// 边界：
//   - 不碰 DOM、不发请求、不 import react——它必须能脱离浏览器被穷举测试
//   - 不认识任何具体 executor：四家的差异在 W4a 侧已经抹平成 Frame
//   - 不做截断、不做上限裁剪：那是 useFramesStream 的事
//
// 为什么坏行是「跳过并计数」而不是抛异常：一行坏数据不该让整条时间线白屏；
// 但也不能静默——计数会显示在面板顶部，那是「采集侧出问题了」的唯一信号。
import type { Frame } from '../../api/types'

// maxLoadedFrames 是已加载帧数的硬上限。
//
// 本期不做虚拟化（YAGNI，且会让 stick-bottom 与 prepend 补偿都变复杂），
// 那就必须有一个说得出口的边界：没有边界的后果不是「偶尔慢」，而是在最长、
// 最需要审阅的那些任务上悄悄卡死。到顶后停止回翻并提示改用 handoff frames。
export const maxLoadedFrames = 5000

// ScanResult 是一次增量扫描的产物。
export interface ScanResult {
  // frames 是本次解析成功的帧，保持到达顺序
  frames: Frame[]
  // bad 是本次跳过的坏行数（累加由调用方负责）
  bad: number
  // rest 是本次仍不完整的尾行，下次调用必须原样带回来
  rest: string
}

// scanLines 把「上次残留的半行 + 本次新到的文本」切成完整帧。
//
// 参数：
//   - buffered: 上次调用返回的 rest（首次传 ''）
//   - chunk: 本次到达的文本增量
//
// 返回：ScanResult
//
// 注意：
//   - 服务端保证只在完整行边界切（W4a §7.2）。这层缓冲是**防御**不是依赖——
//     服务端的保证是契约，客户端仍要能扛住半行
//   - follow 空闲时 agentd 每 20s 发一个换行保活，空行必须当正常跳过、不计坏行
//   - 缺 seq 或缺 type 的 JSON 也算坏行：没有 seq 无法排序与去重，
//     没有 type 无法分发渲染，留着它只会在下游炸得更远
export function scanLines(buffered: string, chunk: string): ScanResult {
  const text = buffered + chunk
  const nl = text.lastIndexOf('\n')
  // 一个换行都没有：整段都还是半行，原样留到下次
  if (nl < 0) return { frames: [], bad: 0, rest: text }

  const frames: Frame[] = []
  let bad = 0
  for (const raw of text.slice(0, nl).split('\n')) {
    if (raw.trim() === '') continue // 保活换行，正常
    let parsed: unknown
    try {
      parsed = JSON.parse(raw)
    } catch {
      bad++
      continue
    }
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
      bad++
      continue
    }
    const f = parsed as Partial<Frame>
    if (typeof f.seq !== 'number' || typeof f.type !== 'string') {
      bad++
      continue
    }
    frames.push(parsed as Frame)
  }
  return { frames, bad, rest: text.slice(nl + 1) }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/task/frames.test.ts`
Expected: PASS（6 个用例）

- [ ] **Step 5: 可观测性自检**

纯函数没有 UI，它的观测等价物是**把降级计入返回值**。逐条确认：

- 非 JSON 行 → `bad` +1，不抛异常、不吞掉同批其余帧
- 缺 `seq` / 缺 `type` / 非对象 → `bad` +1（三条各有用例）
- 保活空行 → 既不进 `frames` 也不进 `bad`（它不是降级，是正常态）
- 半行 → 进 `rest`，**不**计 `bad`（半行不是坏数据，是还没到齐）

任一条没有对应用例，补测试。

- [ ] **Step 6: 注释自检**

确认：文件头写了职责与边界（含「不 import react」这条，它是这个文件存在的理由）；`maxLoadedFrames` 写了为什么要有上限；`scanLines` 写了参数/返回/三条注意；`nl < 0` 分支有「为什么」注释。

- [ ] **Step 7: Commit**

```bash
git add web/src/app/task/frames.ts web/src/app/task/frames.test.ts
git commit -m "feat(web): frames.ts 行扫描（半行缓冲 + 坏行计数）"
```

---

## Task 3: frames.ts 的块聚合（delta 合并 + (turn, part) 配对）

**Files:**
- Modify: `web/src/app/task/frames.ts`（追加，不改 Task 2 已有内容）
- Modify: `web/src/app/task/frames.test.ts`（追加 describe 块）

**Interfaces:**
- Consumes: `Frame`、`scanLines`（Task 2）
- Produces:
  - `export type Block`（六个变体，见下方实现）
  - `export type ToolBlock = Extract<Block, { kind: 'tool' }>`
  - `export function buildBlocks(frames: Frame[]): Block[]`
  - `export type ToolState = 'ok' | 'error' | 'running' | 'gone'`
  - `export function toolState(status: string | null, taskState: string): ToolState`
  - `export function turnsOf(frames: Frame[]): number[]`

- [ ] **Step 1: 写失败的测试**

在 `web/src/app/task/frames.test.ts` 末尾追加：
```ts
import { buildBlocks, toolState, turnsOf, type ToolBlock } from './frames'
import type { Frame } from '../../api/types'

const f = (o: Partial<Frame> & { seq: number; type: string }): Frame =>
  ({ ts: '2026-08-12T10:00:00+08:00', turn: 1, ...o }) as Frame

describe('buildBlocks delta 合并', () => {
  it('同 (turn, type, part) 的连续帧拼成一块', () => {
    const blocks = buildBlocks([
      f({ seq: 1, type: 'text', part: 'p01', delta: '我来' }),
      f({ seq: 2, type: 'text', part: 'p01', delta: '实现它。' }),
    ])
    expect(blocks).toHaveLength(1)
    expect(blocks[0]).toMatchObject({ kind: 'text', text: '我来实现它。' })
  })

  it('part 变化开新块', () => {
    const blocks = buildBlocks([
      f({ seq: 1, type: 'text', part: 'p01', delta: 'a' }),
      f({ seq: 2, type: 'text', part: 'p02', delta: 'b' }),
    ])
    expect(blocks.map((b) => b.kind)).toEqual(['text', 'text'])
  })

  it('turn 变化开新块（part 跨回合会重复）', () => {
    const blocks = buildBlocks([
      f({ seq: 1, turn: 1, type: 'text', part: 'p01', delta: 'a' }),
      f({ seq: 2, turn: 2, type: 'text', part: 'p01', delta: 'b' }),
    ])
    expect(blocks).toHaveLength(2)
  })

  it('type 变化开新块：思维链绝不并进正文', () => {
    const blocks = buildBlocks([
      f({ seq: 1, type: 'reasoning', part: 'p01', delta: '先想想' }),
      f({ seq: 2, type: 'text', part: 'p01', delta: '我说' }),
    ])
    expect(blocks.map((b) => b.kind)).toEqual(['thinking', 'text'])
  })

  it('中间插入其它帧后同 part 不再续接（规则是「连续」）', () => {
    const blocks = buildBlocks([
      f({ seq: 1, type: 'text', part: 'p01', delta: 'a' }),
      f({ seq: 2, type: 'tool_call', part: 'p02', tool: 'bash', input: 'ls' }),
      f({ seq: 3, type: 'text', part: 'p01', delta: 'b' }),
    ])
    expect(blocks.map((b) => b.kind)).toEqual(['text', 'tool', 'text'])
  })
})

describe('buildBlocks 工具配对', () => {
  it('正常配对：调用与结果合成一张卡', () => {
    const blocks = buildBlocks([
      f({ seq: 1, type: 'tool_call', part: 'p03', tool: 'bash', input: 'go test ./...' }),
      f({ seq: 2, type: 'tool_result', part: 'p03', status: 'ok', output: 'ok\t0.2s' }),
    ])
    expect(blocks).toHaveLength(1)
    expect(blocks[0]).toMatchObject({ kind: 'tool', tool: 'bash', input: 'go test ./...', status: 'ok', output: 'ok\t0.2s' })
  })

  it('结果先于调用到达：仍合成一张卡，字段互补', () => {
    const blocks = buildBlocks([
      f({ seq: 1, type: 'tool_result', part: 'p03', status: 'ok', output: 'done' }),
      f({ seq: 2, type: 'tool_call', part: 'p03', tool: 'bash', input: 'ls' }),
    ])
    expect(blocks).toHaveLength(1)
    expect(blocks[0]).toMatchObject({ kind: 'tool', tool: 'bash', input: 'ls', status: 'ok', output: 'done' })
  })

  it('不同回合复用同一个 part 不串台', () => {
    const blocks = buildBlocks([
      f({ seq: 1, turn: 1, type: 'tool_call', part: 'p01', tool: 'read', input: 'a.go' }),
      f({ seq: 2, turn: 1, type: 'tool_result', part: 'p01', status: 'ok', output: 'A' }),
      f({ seq: 3, turn: 2, type: 'tool_call', part: 'p01', tool: 'read', input: 'b.go' }),
    ])
    expect(blocks).toHaveLength(2)
    expect((blocks[1] as ToolBlock).status).toBeNull() // 第二回合那次还没有结果
  })

  it('输入与输出的截断各自独立记账', () => {
    const blocks = buildBlocks([
      f({ seq: 1, type: 'tool_call', part: 'p01', tool: 'write', input: '大段…', truncated: true, bytes: 90000 }),
      f({ seq: 2, type: 'tool_result', part: 'p01', status: 'ok', output: '更大段…', truncated: true, bytes: 141882 }),
    ])
    expect(blocks[0]).toMatchObject({
      inputTruncated: true, inputBytes: 90000,
      outputTruncated: true, outputBytes: 141882,
    })
  })
})

describe('buildBlocks 其余帧型', () => {
  it('turn_start 渲染成回合块，reason 原样带出', () => {
    const blocks = buildBlocks([f({ seq: 1, type: 'turn_start', reason: 'send' })])
    expect(blocks[0]).toMatchObject({ kind: 'turn', turn: 1, reason: 'send' })
  })

  it('event 渲染成事件块，带 event 类型名', () => {
    const blocks = buildBlocks([f({ seq: 1, type: 'event', ref_seq: 88, event: 'permission_request' })])
    expect(blocks[0]).toMatchObject({ kind: 'event', event: 'permission_request' })
  })

  it('未知 type：不丢弃、不抛异常，保留原始 JSON', () => {
    const blocks = buildBlocks([f({ seq: 1, type: 'checkpoint', part: 'p01' })])
    expect(blocks).toHaveLength(1)
    expect(blocks[0]).toMatchObject({ kind: 'unknown', type: 'checkpoint' })
    expect((blocks[0] as { raw: string }).raw).toContain('checkpoint')
  })

  it('缺 delta / 缺 tool 的帧不抛异常，按空串兜底', () => {
    const blocks = buildBlocks([
      f({ seq: 1, type: 'text', part: 'p01' }),
      f({ seq: 2, type: 'tool_call', part: 'p02' }),
    ])
    expect(blocks[0]).toMatchObject({ kind: 'text', text: '' })
    expect(blocks[1]).toMatchObject({ kind: 'tool', tool: '', input: '' })
  })

  it('每个块的 key 唯一（React 列表键）', () => {
    const blocks = buildBlocks([
      f({ seq: 1, type: 'text', part: 'p01', delta: 'a' }),
      f({ seq: 2, type: 'text', part: 'p02', delta: 'b' }),
      f({ seq: 3, type: 'tool_call', part: 'p03', tool: 'ls' }),
    ])
    expect(new Set(blocks.map((b) => b.key)).size).toBe(blocks.length)
  })
})

describe('toolState', () => {
  it('有结果：ok → ok，其余 → error', () => {
    expect(toolState('ok', 'completed')).toBe('ok')
    expect(toolState('error', 'completed')).toBe('error')
    expect(toolState('上游原文', 'running')).toBe('error')
  })

  it('无结果 + 任务 running/waiting_answer → 进行中', () => {
    expect(toolState(null, 'running')).toBe('running')
    // waiting_answer 归入「进行中」是刻意的：回合被工单挡住了，调用确实还在等
    expect(toolState(null, 'waiting_answer')).toBe('running')
  })

  it('无结果 + 任务已停 → 未返回（不许假装还在跑）', () => {
    for (const s of ['waiting_review', 'completed', 'failed', 'pending']) {
      expect(toolState(null, s)).toBe('gone')
    }
  })
})

describe('turnsOf', () => {
  it('升序去重', () => {
    expect(turnsOf([
      f({ seq: 1, turn: 2, type: 'text' }),
      f({ seq: 2, turn: 2, type: 'text' }),
      f({ seq: 3, turn: 3, type: 'text' }),
    ])).toEqual([2, 3])
  })

  it('空输入返回空数组', () => {
    expect(turnsOf([])).toEqual([])
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/task/frames.test.ts`
Expected: FAIL —— `buildBlocks` / `toolState` / `turnsOf` 未导出

- [ ] **Step 3: 写实现**

在 `web/src/app/task/frames.ts` 末尾追加：
```ts
// Block 是时间线上的一个可渲染单元。
//
// 它比 Frame 高一层：多条 delta 帧合成一个 text/thinking 块，一对 tool_call +
// tool_result 合成一张 tool 卡。渲染层只认识 Block，不再碰 Frame。
export type Block =
  | { kind: 'turn'; key: string; turn: number; reason: string; ts: string }
  | { kind: 'text'; key: string; turn: number; text: string }
  | { kind: 'thinking'; key: string; turn: number; text: string }
  | {
      kind: 'tool'
      key: string
      turn: number
      tool: string
      input: string
      inputTruncated: boolean
      inputBytes: number
      // status 为 null 表示「还没有配上 tool_result」——它与 status: '' 是两回事，
      // 前者是没有回音，后者是上游给了个空状态。判定交给 toolState。
      status: string | null
      output: string
      outputTruncated: boolean
      outputBytes: number
    }
  | { kind: 'event'; key: string; turn: number; event: string; ts: string }
  | { kind: 'unknown'; key: string; turn: number; type: string; raw: string }

// ToolBlock 是工具卡块，单独取出来供组件签名使用。
export type ToolBlock = Extract<Block, { kind: 'tool' }>

// buildBlocks 把帧序列聚合成可渲染块。
//
// 参数：
//   - frames: 按到达顺序排列的帧（调用方保证 seq 升序）
//
// 返回：块序列，顺序即渲染顺序
//
// 两条聚合规则：
//   1. delta 合并——**连续**的同 (turn, type, part) 帧拼成一个块。中间插入任何
//      别的帧就断开，之后同 part 再来也另起一块（W4b spec §8.1 的「连续」）
//   2. 工具配对——键是 (turn, part)，不是 ref_seq。ref_seq 是 event 帧指向
//      events 表的库级 seq；part 才是调用与结果的唯一纽带，且只在回合内唯一
//
// 谁先到谁建块：正常是 tool_call 先到，但结果先于调用到达时反过来也成立，
// 后到的一半把自己的字段填进已建的块，不会裂成两张卡。
export function buildBlocks(frames: Frame[]): Block[] {
  const blocks: Block[] = []
  // open 记录「上一个块是不是一个还能续接的 text/thinking 块」。
  // 只要来了别的帧就置空——这是「连续」二字的实现。
  let open: { type: string; turn: number; part: string } | null = null
  const tools = new Map<string, ToolBlock>()

  for (const fr of frames) {
    const turn = fr.turn ?? 0
    const key = `f${fr.seq}`

    if (fr.type === 'text' || fr.type === 'reasoning') {
      const part = fr.part ?? ''
      if (open && open.type === fr.type && open.turn === turn && open.part === part) {
        const prev = blocks[blocks.length - 1] as Extract<Block, { kind: 'text' | 'thinking' }>
        prev.text += fr.delta ?? ''
      } else {
        blocks.push(
          fr.type === 'text'
            ? { kind: 'text', key, turn, text: fr.delta ?? '' }
            : { kind: 'thinking', key, turn, text: fr.delta ?? '' },
        )
        open = { type: fr.type, turn, part }
      }
      continue
    }

    open = null // 以下任何一种帧都打断 delta 续接

    switch (fr.type) {
      case 'turn_start':
        blocks.push({ kind: 'turn', key, turn, reason: fr.reason ?? '', ts: fr.ts })
        break
      case 'tool_call': {
        const k = `${turn}/${fr.part ?? ''}`
        const hit = tools.get(k)
        if (hit) {
          // 结果先到过：补上调用侧的字段，不新建卡
          hit.tool = fr.tool ?? ''
          hit.input = fr.input ?? ''
          hit.inputTruncated = fr.truncated ?? false
          hit.inputBytes = fr.bytes ?? 0
          break
        }
        const b: ToolBlock = {
          kind: 'tool', key, turn,
          tool: fr.tool ?? '', input: fr.input ?? '',
          inputTruncated: fr.truncated ?? false, inputBytes: fr.bytes ?? 0,
          status: null, output: '',
          outputTruncated: false, outputBytes: 0,
        }
        tools.set(k, b)
        blocks.push(b)
        break
      }
      case 'tool_result': {
        const k = `${turn}/${fr.part ?? ''}`
        const hit = tools.get(k)
        if (hit) {
          hit.status = fr.status ?? ''
          hit.output = fr.output ?? ''
          hit.outputTruncated = fr.truncated ?? false
          hit.outputBytes = fr.bytes ?? 0
          break
        }
        const b: ToolBlock = {
          kind: 'tool', key, turn,
          tool: '', input: '',
          inputTruncated: false, inputBytes: 0,
          status: fr.status ?? '', output: fr.output ?? '',
          outputTruncated: fr.truncated ?? false, outputBytes: fr.bytes ?? 0,
        }
        tools.set(k, b)
        blocks.push(b)
        break
      }
      case 'event':
        blocks.push({ kind: 'event', key, turn, event: fr.event ?? '', ts: fr.ts })
        break
      default:
        // 未知 type 必须渲染而不是丢弃：契约会演进，而前端比后端晚部署是常态。
        // 遇到新类型就白屏或静默吞掉，都是不可接受的失败模式。
        blocks.push({ kind: 'unknown', key, turn, type: fr.type, raw: JSON.stringify(fr) })
    }
  }
  return blocks
}

// ToolState 是工具卡的四种展示状态。
export type ToolState = 'ok' | 'error' | 'running' | 'gone'

// toolState 判定一张工具卡该显示成什么状态。
//
// 参数：
//   - status: 块的 status（null 表示没有配上 tool_result）
//   - taskState: 任务当前状态（running / waiting_answer / waiting_review / …）
//
// 返回：ok（成功）/ error（失败）/ running（进行中）/ gone（未返回）
//
// 未配上结果时分两种，不含糊成同一种：
//   - running / waiting_answer → 进行中。waiting_answer 归到这里是刻意的：
//     那说明回合被工单挡住了，工具调用确实还在等，不是没有回音
//   - 其余 → 未返回。这是真实信号：executor 半路死掉时，工具调用就是发出去
//     没有回音。把它显示成「进行中」是在撒谎——W4a 的第一轮派发就是这么死的，
//     审阅者当时从页面上看不出任何异常
export function toolState(status: string | null, taskState: string): ToolState {
  if (status === null) {
    return taskState === 'running' || taskState === 'waiting_answer' ? 'running' : 'gone'
  }
  return status === 'ok' ? 'ok' : 'error'
}

// turnsOf 返回已加载帧里出现过的回合号，升序去重。
//
// 锚点条据此生成，不需要任何新接口。注意它只覆盖**已加载**范围——
// 面板必须把这一点写在界面上，不能假装是全量目录。
export function turnsOf(frames: Frame[]): number[] {
  return [...new Set(frames.map((f) => f.turn ?? 0))].sort((a, b) => a - b)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/task/frames.test.ts`
Expected: PASS（本任务新增 15 个用例，加 Task 2 的 6 个共 21 个）

- [ ] **Step 5: 可观测性自检**

逐条确认这些「不许静默」的点都有用例钉住：

- 未知 type **进入结果**（`kind: 'unknown'` 且 `raw` 保留原文），不是被过滤掉
- 未配对的 tool_call 的 `status` 是 `null`（而不是 `''`），下游据此能区分「没有回音」与「上游给了空状态」
- `toolState(null, 'waiting_review' | 'completed' | 'failed' | 'pending')` 一律 `gone`，绝不回退成 `running`
- 缺字段的帧按空串兜底而不是抛异常（一帧不全不该炸掉整条时间线）

- [ ] **Step 6: 注释自检**

确认：`Block` 写了「它比 Frame 高一层」；`status: null` 与 `''` 的区别有行内注释；`buildBlocks` 的两条聚合规则和「谁先到谁建块」写在导出注释里；`open = null` 那行有「打断 delta 续接」的注释；`default` 分支有「未知 type 为什么必须渲染」；`toolState` 写了 `waiting_answer` 归类的理由。

- [ ] **Step 7: Commit**

```bash
git add web/src/app/task/frames.ts web/src/app/task/frames.test.ts
git commit -m "feat(web): frames.ts 块聚合（delta 合并 + (turn, part) 配对）"
```

---

## Task 4: codeText —— 只管代码围栏

**Files:**
- Create: `web/src/app/task/codeText.tsx`
- Create: `web/src/app/task/codeText.test.tsx`

**Interfaces:**
- Consumes: 无
- Produces:
  - `export interface Segment { code: boolean; text: string }`
  - `export function splitFences(text: string): Segment[]`
  - `export function splitInline(text: string): Segment[]`
  - `export function CodeText({ text }: { text: string }): React.ReactElement`

- [ ] **Step 1: 写失败的测试**

创建 `web/src/app/task/codeText.tsx` 的测试 `web/src/app/task/codeText.test.tsx`：
```ts
// codeText 的切分边界：闭合围栏、未闭合围栏（流式中途）、行内 code。
//
// 关键不变式：**未闭合时按纯文本降级**。增量是逐字到达的，若围栏未闭合就先按
// 代码块渲染，闭合瞬间整块会重排——那正是本方案不引 markdown 渲染器的理由之一，
// 自己实现时更不能把这个坑重新挖出来。
import { describe, expect, it } from 'vitest'
import { splitFences, splitInline } from './codeText'

describe('splitFences', () => {
  it('无围栏：整段一个纯文本段', () => {
    expect(splitFences('普通文字')).toEqual([{ code: false, text: '普通文字' }])
  })

  it('闭合围栏：切成 文本 / 代码 / 文本 三段，去掉语言行', () => {
    expect(splitFences('前\n```go\nfmt.Println()\n```\n后')).toEqual([
      { code: false, text: '前\n' },
      { code: true, text: 'fmt.Println()\n' },
      { code: false, text: '\n后' },
    ])
  })

  it('无语言标注的围栏也能切', () => {
    expect(splitFences('```\nls -la\n```')).toEqual([
      { code: false, text: '' },
      { code: true, text: 'ls -la\n' },
      { code: false, text: '' },
    ])
  })

  it('未闭合围栏：整段按纯文本降级（流式中途不抖）', () => {
    const partial = '我来写：\n```go\nfunc main() {'
    expect(splitFences(partial)).toEqual([{ code: false, text: partial }])
  })

  it('两个闭合围栏都识别', () => {
    const segs = splitFences('a\n```\nx\n```\nb\n```\ny\n```\nc')
    expect(segs.filter((s) => s.code).map((s) => s.text)).toEqual(['x\n', 'y\n'])
  })

  it('空串返回单个空文本段，不抛异常', () => {
    expect(splitFences('')).toEqual([{ code: false, text: '' }])
  })
})

describe('splitInline', () => {
  it('行内 code 切出来', () => {
    expect(splitInline('用 `go test` 跑')).toEqual([
      { code: false, text: '用 ' },
      { code: true, text: 'go test' },
      { code: false, text: ' 跑' },
    ])
  })

  it('未闭合的反引号按纯文本', () => {
    expect(splitInline('未闭合 `go test')).toEqual([{ code: false, text: '未闭合 `go test' }])
  })

  it('反引号内不跨行（跨行的那是围栏的事）', () => {
    expect(splitInline('a `b\nc` d')).toEqual([{ code: false, text: 'a `b\nc` d' }])
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/task/codeText.test.tsx`
Expected: FAIL —— 找不到模块 `./codeText`

- [ ] **Step 3: 写实现**

创建 `web/src/app/task/codeText.tsx`：
```tsx
// codeText —— 正文里的代码渲染，范围钉死在三段围栏与行内 code。
//
// 职责：
//   - splitFences: 按闭合的三段围栏切分
//   - splitInline: 按行内反引号切分（不跨行）
//   - CodeText: 把上面两级切分渲染成 <pre> / <code> / 纯文本
//
// 边界：
//   - **不是 markdown 渲染器**。标题、粗体、列表、链接一律当纯文本
//   - 不做语法高亮，不引任何依赖
//   - 不接受 HTML：文本原样进 React 文本节点，天然没有 XSS 面
//
// 为什么不引 react-markdown：收益分布很不均匀。审阅时真正需要和散文区分开的
// 是代码块；标题/粗体/列表不渲染也完全读得懂。为这点边际收益引一个体积不小的
// 依赖，外加 XSS 面（必须禁 raw HTML）和**流式抖动**（增量逐字到达，围栏闭合
// 瞬间整块重排）——不划算。范围被钉死在围栏上，所以它也不会长成一个必须维护的
// 半吊子 markdown 实现；将来真需要，换成 react-markdown 是纯替换。

// Segment 是一段切分结果：code 为真表示按代码样式渲染。
export interface Segment {
  code: boolean
  text: string
}

// splitFences 按三段围栏 ``` 切分文本。
//
// 参数：text 原始文本（可能是流式中途的半截）
// 返回：交替的纯文本段与代码段
//
// 注意：**围栏未闭合时整段按纯文本降级**。这是流式渲染不抖的关键——增量是
// 逐字到达的，闭合前先按代码块画，闭合瞬间就会整块重排。判据是围栏标记的
// 个数为奇数（首段之后每两个标记围出一个代码段）。
export function splitFences(text: string): Segment[] {
  const parts = text.split('```')
  // 1 段 = 没有围栏；偶数段 = 有未闭合的围栏。两种都整段降级为纯文本。
  if (parts.length < 3 || parts.length % 2 === 0) return [{ code: false, text }]
  return parts.map((p, i) => {
    if (i % 2 === 0) return { code: false, text: p }
    // 代码段的首行是语言标注（可能为空），不属于代码内容
    const nl = p.indexOf('\n')
    return { code: true, text: nl >= 0 ? p.slice(nl + 1) : p }
  })
}

// splitInline 按行内反引号切分单段纯文本。
//
// 参数：text 一段不含围栏的纯文本
// 返回：交替的纯文本段与行内代码段
//
// 注意：行内 code 不跨行——跨行的那是围栏的事。未闭合时整段按纯文本降级，
// 理由与 splitFences 相同。
export function splitInline(text: string): Segment[] {
  const out: Segment[] = []
  let rest = text
  for (;;) {
    const open = rest.indexOf('`')
    if (open < 0) break
    const close = rest.indexOf('`', open + 1)
    if (close < 0) break
    const inner = rest.slice(open + 1, close)
    if (inner.includes('\n')) break // 跨行不算行内 code，整段降级
    out.push({ code: false, text: rest.slice(0, open) })
    out.push({ code: true, text: inner })
    rest = rest.slice(close + 1)
  }
  if (out.length === 0) return [{ code: false, text }]
  out.push({ code: false, text: rest })
  return out
}

// CodeText 渲染一段正文：围栏成 <pre>，行内反引号成 <code>，其余纯文本保留换行。
//
// 参数：text 正文（可能是流式中途的半截）
// 返回：可直接放进块组件的 React 元素
export function CodeText({ text }: { text: string }) {
  return (
    <>
      {splitFences(text).map((seg, i) =>
        seg.code ? (
          <pre
            key={i}
            className="my-1.5 overflow-x-auto rounded-md bg-muted/60 p-2 font-mono text-xs leading-relaxed"
          >
            {seg.text}
          </pre>
        ) : (
          <span key={i} className="whitespace-pre-wrap break-words">
            {splitInline(seg.text).map((s, j) =>
              s.code ? (
                <code key={j} className="rounded bg-muted/60 px-1 py-0.5 font-mono text-xs">
                  {s.text}
                </code>
              ) : (
                <span key={j}>{s.text}</span>
              ),
            )}
          </span>
        ),
      )}
    </>
  )
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/task/codeText.test.tsx`
Expected: PASS（9 个用例）

- [ ] **Step 5: 可观测性自检**

这一层没有错误分支——它的纪律是**降级必须是可预测的、且被测试钉住**：

- 未闭合围栏 → 纯文本（有用例）
- 未闭合行内反引号 → 纯文本（有用例）
- 跨行反引号 → 纯文本（有用例）
- 空串 → 不抛异常（有用例）

没有任何 `try/catch` 吞掉输入的路径，也不要加——这里任何「静默修正」都会让人搞不清屏幕上的内容到底是模型说的还是渲染器编的。

- [ ] **Step 6: 注释自检**

确认：文件头写了「不是 markdown 渲染器」这条边界，以及不引 react-markdown 的三条理由（边际收益 / XSS 面 / 流式抖动）；`splitFences` 的奇偶判据有「为什么」注释；「首行是语言标注」有注释。

- [ ] **Step 7: Commit**

```bash
git add web/src/app/task/codeText.tsx web/src/app/task/codeText.test.tsx
git commit -m "feat(web): codeText 代码围栏渲染（零依赖，未闭合降级）"
```

---

## Task 5: 五个块组件

**Files:**
- Create: `web/src/app/task/TextBlock.tsx`
- Create: `web/src/app/task/ThinkingBlock.tsx`
- Create: `web/src/app/task/ToolCard.tsx`
- Create: `web/src/app/task/EventMark.tsx`
- Create: `web/src/app/task/UnknownBlock.tsx`
- Create: `web/src/app/task/blocks.test.tsx`

**Interfaces:**
- Consumes: `Block` / `ToolBlock` / `ToolState` / `toolState` from `./frames`（Task 3）；`CodeText` from `./codeText`（Task 4）
- Produces:
  - `export function TextBlock({ text }: { text: string })`
  - `export function ThinkingBlock({ text }: { text: string })`
  - `export function ToolCard({ block, taskState }: { block: ToolBlock; taskState: string })`
  - `export function EventMark({ event, ts }: { event: string; ts: string })`
  - `export function UnknownBlock({ type, raw }: { type: string; raw: string })`

- [ ] **Step 1: 写失败的测试**

创建 `web/src/app/task/blocks.test.tsx`：
```tsx
// 块组件的行为测试：只断言行为，不测样式。
//
// 四条硬要求：思维链默认折叠；工具卡默认折叠且三种状态各自可辨；事件标记
// **不可点**（审批入口唯一在工单区）；未知类型可展开看原始 JSON。
import { describe, expect, it } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import type { ToolBlock } from './frames'
import { TextBlock } from './TextBlock'
import { ThinkingBlock } from './ThinkingBlock'
import { ToolCard } from './ToolCard'
import { EventMark } from './EventMark'
import { UnknownBlock } from './UnknownBlock'

const tool = (o: Partial<ToolBlock>): ToolBlock => ({
  kind: 'tool', key: 'f1', turn: 1,
  tool: 'bash', input: 'go test ./...',
  inputTruncated: false, inputBytes: 0,
  status: 'ok', output: 'ok\t0.2s',
  outputTruncated: false, outputBytes: 0,
  ...o,
})

describe('TextBlock', () => {
  it('正文永远展开', () => {
    render(<TextBlock text="我来实现它。" />)
    expect(screen.getByText('我来实现它。')).toBeInTheDocument()
  })
})

describe('ThinkingBlock', () => {
  it('默认折叠：只显示字数摘要，正文不可见', () => {
    render(<ThinkingBlock text="先看一下测试怎么写的" />)
    expect(screen.getByRole('button', { name: /思维链/ })).toBeInTheDocument()
    expect(screen.queryByText('先看一下测试怎么写的')).not.toBeInTheDocument()
  })

  it('点开后正文可见', () => {
    render(<ThinkingBlock text="先看一下测试怎么写的" />)
    fireEvent.click(screen.getByRole('button', { name: /思维链/ }))
    expect(screen.getByText('先看一下测试怎么写的')).toBeInTheDocument()
  })
})

describe('ToolCard', () => {
  it('默认折叠：显示工具名与参数摘要，输入输出不可见', () => {
    render(<ToolCard block={tool({})} taskState="completed" />)
    expect(screen.getByText('bash')).toBeInTheDocument()
    expect(screen.queryByText('ok\t0.2s')).not.toBeInTheDocument()
  })

  it('展开后能看到输入与输出', () => {
    render(<ToolCard block={tool({})} taskState="completed" />)
    fireEvent.click(screen.getByRole('button', { name: /bash/ }))
    expect(screen.getByText(/go test \.\/\.\.\./)).toBeInTheDocument()
    expect(screen.getByText(/ok/)).toBeInTheDocument()
  })

  it('三种状态各自可辨：成功 / 失败 / 未返回', () => {
    const { unmount: u1 } = render(<ToolCard block={tool({ status: 'ok' })} taskState="completed" />)
    expect(screen.getByText('成功')).toBeInTheDocument()
    u1()
    const { unmount: u2 } = render(<ToolCard block={tool({ status: 'error' })} taskState="completed" />)
    expect(screen.getByText('失败')).toBeInTheDocument()
    u2()
    render(<ToolCard block={tool({ status: null, output: '' })} taskState="waiting_review" />)
    expect(screen.getByText('未返回')).toBeInTheDocument()
  })

  it('任务还在跑时未配对的调用显示「进行中」而不是「未返回」', () => {
    render(<ToolCard block={tool({ status: null, output: '' })} taskState="running" />)
    expect(screen.getByText('进行中')).toBeInTheDocument()
  })

  it('截断提示带原始字节数', () => {
    render(<ToolCard block={tool({ outputTruncated: true, outputBytes: 141882 })} taskState="completed" />)
    fireEvent.click(screen.getByRole('button', { name: /bash/ }))
    expect(screen.getByText(/141882/)).toBeInTheDocument()
    expect(screen.getByText(/已截断/)).toBeInTheDocument()
  })
})

describe('EventMark', () => {
  it('是不可操作的标记：没有任何按钮', () => {
    const { container } = render(<EventMark event="permission_request" ts="2026-08-12T10:31:02+08:00" />)
    expect(container.querySelectorAll('button')).toHaveLength(0)
    expect(screen.getByText(/权限工单/)).toBeInTheDocument()
  })

  it('明确指向工单区，不在时间线里开第二个审批入口', () => {
    render(<EventMark event="permission_request" ts="2026-08-12T10:31:02+08:00" />)
    expect(screen.getByText(/工单区/)).toBeInTheDocument()
  })

  it('未知事件名原样显示，不吞掉', () => {
    render(<EventMark event="some_new_event" ts="2026-08-12T10:31:02+08:00" />)
    expect(screen.getByText(/some_new_event/)).toBeInTheDocument()
  })
})

describe('UnknownBlock', () => {
  it('默认折叠，展开后能看到原始 JSON', () => {
    render(<UnknownBlock type="checkpoint" raw='{"seq":20,"type":"checkpoint"}' />)
    expect(screen.getByText(/checkpoint/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /未知帧类型/ }))
    expect(screen.getByText(/"seq":20/)).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/task/blocks.test.tsx`
Expected: FAIL —— 找不到 `./TextBlock` 等模块

- [ ] **Step 3: 写 TextBlock 与 ThinkingBlock**

创建 `web/src/app/task/TextBlock.tsx`：
```tsx
// TextBlock —— 时间线上的模型正文段落。
//
// 职责：渲染一段合并后的 text 增量，代码围栏交给 CodeText
// 边界：永远展开、不可折叠。回合审阅需要的因果链里，正文是最不该被藏起来的一环
import { CodeText } from './codeText'

// TextBlock 渲染一段正文。
//
// 参数：text 已按 part 合并好的完整段落
export function TextBlock({ text }: { text: string }) {
  return (
    <div className="text-sm leading-relaxed">
      <CodeText text={text} />
    </div>
  )
}
```

创建 `web/src/app/task/ThinkingBlock.tsx`：
```tsx
// ThinkingBlock —— 时间线上的思维链块。
//
// 职责：默认折叠成一行摘要（🧠 思维链 · N 字），点开才展开全文
// 边界：
//   - 不做任何加工：思维链是模型的原始推理，改写或摘要都会让它失去证据价值
//   - 默认折叠是刻意的：审阅时先看「说了什么 / 做了什么」，思维链是需要时才下钻
//     的深层证据；默认展开会把因果链淹掉
import { useState } from 'react'
import { Brain, ChevronDown, ChevronRight } from 'lucide-react'

// ThinkingBlock 渲染一段思维链。
//
// 参数：text 已按 part 合并好的完整思维链
export function ThinkingBlock({ text }: { text: string }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="rounded-r-md border-l-2 border-violet-400 bg-violet-500/5 px-2.5 py-1.5">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-1.5 text-xs text-violet-600 hover:underline dark:text-violet-400"
      >
        {open ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
        <Brain className="size-3.5" />
        思维链 · {text.length} 字
      </button>
      {open && (
        <p className="mt-1.5 whitespace-pre-wrap break-words text-xs leading-relaxed text-muted-foreground">
          {text}
        </p>
      )}
    </div>
  )
}
```

- [ ] **Step 4: 写 ToolCard**

创建 `web/src/app/task/ToolCard.tsx`：
```tsx
// ToolCard —— 时间线上的一次工具调用（调用与结果合成一张卡）。
//
// 职责：
//   - 折叠态显示工具名、参数摘要、状态徽章
//   - 展开态显示完整输入与输出，截断时标出原始字节数
//
// 边界：
//   - 不提供「看全文」的旁路。要看被截断的全文，出口是 handoff diff / fetch / run
//   - 不解析工具语义：input 是 executor 给的原文（多为 JSON），原样显示
import { useState } from 'react'
import { ChevronDown, ChevronRight } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { toolState, type ToolBlock, type ToolState } from './frames'

// STATE_LABEL 是四种工具状态的中文标签。
//
// 「未返回」与「进行中」必须分开：前者是 executor 半路死掉、调用发出去没有回音，
// 把它显示成后者是在撒谎。
const STATE_LABEL: Record<ToolState, string> = {
  ok: '成功',
  error: '失败',
  running: '进行中',
  gone: '未返回',
}

const STATE_VARIANT: Record<ToolState, 'default' | 'secondary' | 'destructive' | 'outline'> = {
  ok: 'default',
  error: 'destructive',
  running: 'secondary',
  gone: 'outline',
}

// argSummary 从工具入参里挑一行可读摘要。
//
// 只读已知形状的字段，其余回退原文；绝不因为解析失败而吞掉整张卡
// （与 EventsPanel.eventSummary 同一条纪律）。
function argSummary(input: string): string {
  try {
    const o = JSON.parse(input) as Record<string, unknown>
    for (const k of ['path', 'cmd', 'command', 'pattern', 'file_path', 'query']) {
      const v = o[k]
      if (typeof v === 'string' && v !== '') return v
    }
  } catch {
    // 入参不是 JSON（grok 给的是人类摘要），原样当摘要用
  }
  return input
}

// truncNote 生成截断提示文案。
function truncNote(bytes: number): string {
  return `已截断（原始 ${bytes} 字节，保留头 4KB + 尾 4KB）；要看全文用 handoff diff / fetch / run`
}

// ToolCard 渲染一次工具调用。
//
// 参数：
//   - block: 已配对好的工具块（status 为 null 表示还没有结果）
//   - taskState: 任务当前状态，决定未配对时显示「进行中」还是「未返回」
export function ToolCard({ block, taskState }: { block: ToolBlock; taskState: string }) {
  const [open, setOpen] = useState(false)
  const st = toolState(block.status, taskState)
  return (
    <div className="overflow-hidden rounded-md border bg-background">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-xs hover:bg-muted/40"
      >
        {open ? <ChevronDown className="size-3.5 shrink-0" /> : <ChevronRight className="size-3.5 shrink-0" />}
        <span className="shrink-0 font-mono font-medium text-sky-700 dark:text-sky-400">
          {block.tool || '(未知工具)'}
        </span>
        <span className="min-w-0 flex-1 truncate font-mono text-muted-foreground">
          {argSummary(block.input)}
        </span>
        <Badge variant={STATE_VARIANT[st]} className="shrink-0">{STATE_LABEL[st]}</Badge>
      </button>
      {open && (
        <div className="border-t bg-muted/20 text-xs">
          <div className="px-2.5 py-2">
            <span className="mb-1 block text-[11px] text-muted-foreground">输入</span>
            <pre className="overflow-x-auto whitespace-pre-wrap break-words font-mono">{block.input}</pre>
            {block.inputTruncated && (
              <p className="mt-1 text-[11px] text-amber-600 dark:text-amber-500">{truncNote(block.inputBytes)}</p>
            )}
          </div>
          <div className="border-t border-dashed px-2.5 py-2">
            <span className="mb-1 block text-[11px] text-muted-foreground">输出</span>
            {block.status === null ? (
              // 没有回音是真实信号，必须写出来，不能留空让人以为「输出为空」
              <p className="text-amber-600 dark:text-amber-500">
                {st === 'running' ? '仍在等待结果…' : 'executor 已不在，此调用没有回音'}
              </p>
            ) : (
              <pre className="overflow-x-auto whitespace-pre-wrap break-words font-mono">{block.output}</pre>
            )}
            {block.outputTruncated && (
              <p className="mt-1 text-[11px] text-amber-600 dark:text-amber-500">{truncNote(block.outputBytes)}</p>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 5: 写 EventMark 与 UnknownBlock**

创建 `web/src/app/task/EventMark.tsx`：
```tsx
// EventMark —— 时间线上的事件打断标记。
//
// 职责：说明因果——模型正说着话，在这里被一张工单打断了，批复后才接上
// 边界：
//   - **不可操作**。同一张工单在三个地方出现是有意的：时间线管因果、
//     EventsPanel 管实时、TicketsPanel 管裁决。审批不可逆且要当真，
//     给它开第二个入口、还开在一条摘要旁边，风险和收益不成比例
//   - 不显示 payload 全文。全文只在工单区（EventsPanel 头注释的既有纪律）
import { CircleDot } from 'lucide-react'
import { formatFull } from '../lib/format'

// EVENT_LABEL 是已知事件类型的中文标签。未知类型原样显示类型名，不吞掉。
const EVENT_LABEL: Record<string, string> = {
  permission_request: '权限工单：等待审核者裁决',
  question: '提问工单：等待审核者回答',
  completed: '一轮结束，进入待审',
  failed: '任务失败',
  delivery_failed: '裁决已落库但没送到 executor',
  stalled: '看门狗：长时间无产出',
}

// EventMark 渲染一行事件标记。
//
// 参数：
//   - event: 事件类型名（W4a 刻意冗余在帧里，前端不查 events 表就能画）
//   - ts: 帧时间戳（RFC3339）
export function EventMark({ event, ts }: { event: string; ts: string }) {
  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 rounded-md border border-amber-500/40 bg-amber-500/5 px-2.5 py-1.5 text-xs">
      <CircleDot className="size-3.5 shrink-0 text-amber-600 dark:text-amber-500" />
      <span>{EVENT_LABEL[event] ?? event}</span>
      <span className="text-[11px] text-muted-foreground">
        {formatFull(ts)} · 裁决入口在右侧工单区
      </span>
    </div>
  )
}
```

创建 `web/src/app/task/UnknownBlock.tsx`：
```tsx
// UnknownBlock —— 本前端版本不认识的帧类型。
//
// 职责：把未知帧渲染成中性条目，可展开看原始 JSON
// 边界：不猜测语义、不尝试渲染成别的块
//
// 为什么必须有这个组件：契约会演进，而前端比后端晚部署是常态。遇到新类型就
// 白屏或静默吞掉，都是不可接受的失败模式——尤其静默吞掉，会让审阅者以为
// 「模型这段时间什么也没做」。
import { useState } from 'react'
import { ChevronDown, ChevronRight, HelpCircle } from 'lucide-react'

// UnknownBlock 渲染一个未知类型的帧。
//
// 参数：
//   - type: 帧的 type 字段原文
//   - raw: 整帧的 JSON 原文
export function UnknownBlock({ type, raw }: { type: string; raw: string }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="rounded-md border border-dashed px-2.5 py-1.5 text-xs text-muted-foreground">
      <button type="button" onClick={() => setOpen((v) => !v)} className="flex items-center gap-1.5 hover:underline">
        {open ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
        <HelpCircle className="size-3.5" />
        未知帧类型 <span className="font-mono">{type}</span>（本前端版本尚不认识，已原样保留）
      </button>
      {open && <pre className="mt-1.5 overflow-x-auto whitespace-pre-wrap break-words font-mono text-[11px]">{raw}</pre>}
    </div>
  )
}
```

- [ ] **Step 6: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/task/blocks.test.tsx`
Expected: PASS（11 个用例）

- [ ] **Step 7: 可观测性自检**

逐条确认这些状态在界面上**看得见**：

- 工具卡四态各有独立文案（成功 / 失败 / 进行中 / 未返回），`gone` 不复用 `running` 的文案
- 未配对时输出区写明原因（「executor 已不在，此调用没有回音」），**不留空**——留空会被读成「输出为空」
- 截断有提示且带原始字节数，并点明全文出口是 `handoff diff / fetch / run`
- 未知事件类型原样显示类型名，不显示成「未知事件」这种把信息抹掉的文案
- 未知帧类型有可见条目且可展开看原文
- 工具名缺失时显示 `(未知工具)` 而不是空白

- [ ] **Step 8: 注释自检**

确认五个文件都有职责/边界的文件头注释；`ThinkingBlock` 写了为什么默认折叠；`EventMark` 写了为什么不可操作（三处各司其职）；`UnknownBlock` 写了为什么必须存在；`ToolCard` 的 `STATE_LABEL` 写了「未返回」与「进行中」为什么必须分开；`argSummary` 的 catch 分支有注释说明为什么不算错误。

- [ ] **Step 9: Commit**

```bash
git add web/src/app/task/TextBlock.tsx web/src/app/task/ThinkingBlock.tsx web/src/app/task/ToolCard.tsx web/src/app/task/EventMark.tsx web/src/app/task/UnknownBlock.tsx web/src/app/task/blocks.test.tsx
git commit -m "feat(web): 时间线的五个块组件"
```

---

## Task 6: useFramesStream —— 进入与跟随

**Files:**
- Create: `web/src/app/task/useFramesStream.ts`

**Interfaces:**
- Consumes: `scanLines` / `maxLoadedFrames` from `./frames`（Task 2）；`Frame` from `../../api/types`（Task 1）；`errorMessage` from `../lib/format`
- Produces:
  - `export const pageBytes = 65536`
  - `export interface FramesStream { frames: Frame[]; badLines: number; startOffset: number; error: string | null; active: boolean; atCap: boolean; loadingEarlier: boolean; loadEarlier: () => void; retry: () => void }`
  - `export function useFramesStream(taskId: string | undefined): FramesStream`

  本任务先把 `loadEarlier` 实现成空操作占位（`startOffset` 恒为初始值、`loadingEarlier` 恒 false），Task 7 补齐。

- [ ] **Step 1: 写实现**

创建 `web/src/app/task/useFramesStream.ts`：
```ts
// useFramesStream —— 结构化回合帧流的读取 hook。
//
// 数据源：GET /api/tasks/{id}/frames?tail=65536&follow=1，application/x-ndjson。
// 与 useRenderStream 同一套 I/O 形状（fetch + ReadableStream + AbortController），
// 差别只在解析：那边是纯文本，这边每行是一个 Frame。
//
// 职责：加载 / 跟随 / 回翻 / 坏行计数 / 帧数上限
// 边界：只管 I/O 与状态，不决定长什么样；不做块聚合（那是 frames.ts 的纯函数）
//
// 语义注意（与 /render 完全一致）：
//   - 文件不存在时返回 200 空内容（任务刚派发、模型还没吐第一帧是正常态），不是错误
//   - 响应头 X-Handoff-Frames-Size 是响应开始时的文件大小
//   - offset 与 tail 的单位都是**字节**，且两者互斥
//   - follow 空闲时 agentd 每 20s 发一个换行保活，会出现空行（scanLines 已按正常跳过）
//
// 组件卸载必须 AbortController 中止——否则每次进出详情页都泄漏一条常驻连接。
// 这个坑 RenderPanel 的头注释已经记了一次，这里是第二条常驻连接，同样适用。
import { useCallback, useEffect, useRef, useState } from 'react'
import type { Frame } from '../../api/types'
import { errorMessage } from '../lib/format'
import { maxLoadedFrames, scanLines } from './frames'

// pageBytes 是一页的字节数：进入时的 tail 量，也是「加载更早」每次往前的量。
// 与 RenderPanel 的 tail=65536 同量级——两个接口的「默认看多少」不该无缘无故不同。
export const pageBytes = 65536

// FramesStream 是 hook 的返回形状。
export interface FramesStream {
  // frames 是已加载帧，按 seq 升序
  frames: Frame[]
  // badLines 是累计跳过的坏行数（面板必须把它显示出来，见下方「不静默」一节）
  badLines: number
  // startOffset 是已加载区间的起始字节偏移；0 表示已到文件头，「加载更早」应消失
  startOffset: number
  // error 是流错误的人类可读原因；非 null 时面板显示错误条 + 重试
  error: string | null
  // active 表示是否仍在跟随（流未结束）
  active: boolean
  // atCap 表示已加载帧数触到 maxLoadedFrames：停止回翻并提示改用 handoff frames
  atCap: boolean
  // loadingEarlier 表示一次回翻正在进行中
  loadingEarlier: boolean
  loadEarlier: () => void
  retry: () => void
}

// 不静默：这个 hook 的所有降级路径都必须有一个对应的返回字段，让面板画出来。
//   - 网络/协议错 → error（面板给错误条 + 重试按钮）
//   - 坏行        → badLines（面板顶部计数）
//   - 帧数到顶    → atCap（面板停用「加载更早」并提示）
//   - 流结束      → active=false（面板把「跟随中」换成「已结束」）
// 少任何一条，界面上就会出现「看着正常、其实不是」的状态——那是最坏的一种。

export function useFramesStream(taskId: string | undefined): FramesStream {
  const [frames, setFrames] = useState<Frame[]>([])
  const [badLines, setBadLines] = useState(0)
  const [startOffset, setStartOffset] = useState(0)
  const [error, setError] = useState<string | null>(null)
  const [active, setActive] = useState(true)
  // Task 7 会把它换成带 setter 的形式；此刻没人写它，带上 setter 会触发 lint 未使用告警
  const [loadingEarlier] = useState(false)
  // reloadKey 只用来强制重跑主 effect（重试按钮）
  const [reloadKey, setReloadKey] = useState(0)

  useEffect(() => {
    if (!taskId) return
    const ac = new AbortController()
    let cancelled = false
    setFrames([])
    setBadLines(0)
    setStartOffset(0)
    setError(null)
    setActive(true)

    const run = async () => {
      try {
        const resp = await fetch(
          `/api/tasks/${encodeURIComponent(taskId)}/frames?tail=${pageBytes}&follow=1`,
          { credentials: 'same-origin', signal: ac.signal },
        )
        if (cancelled) return
        if (resp.status === 401) {
          setError('未授权：会话已失效，请重新打开控制台')
          setActive(false)
          return
        }
        if (!resp.ok) {
          // 非 2xx 时尽量透出 agentd 的错误原文，读不到再回退状态码文案
          let msg = `agentd 返回 ${resp.status} ${resp.statusText}`
          try {
            const body = (await resp.json()) as { error?: string }
            if (body.error) msg = body.error
          } catch {
            // 响应体不是 JSON，保留兜底文案
          }
          setError(msg)
          setActive(false)
          return
        }
        const hdr = resp.headers.get('X-Handoff-Frames-Size')
        const total = hdr !== null && hdr !== '' ? Number(hdr) : null
        // 文件大小本身不对外暴露（没有界面用它，YAGNI），只用来推起始偏移。
        // tail=pageBytes 时服务端的起点就是 max(0, size - pageBytes)（再向后对齐到
        // 下一个换行）。没有专门的响应头告诉我们起点，但它可以从 size 推出来，
        // 用来判断「还有没有更早的」——推小了只会多请求一次，不会漏。
        setStartOffset(total === null ? 0 : Math.max(0, total - pageBytes))
        if (!resp.body) {
          setError('响应没有可读流（浏览器不支持 ReadableStream？）')
          setActive(false)
          return
        }
        const reader = resp.body.getReader()
        const decoder = new TextDecoder()
        let buffered = ''
        for (;;) {
          const { done, value } = await reader.read()
          if (cancelled || done) break
          const scan = scanLines(buffered, decoder.decode(value, { stream: true }))
          buffered = scan.rest
          if (scan.bad > 0) setBadLines((n) => n + scan.bad)
          if (scan.frames.length > 0) {
            setFrames((prev) => {
              const next = prev.concat(scan.frames)
              // 到顶后丢最旧的：长跑任务不该把 DOM 撑爆。丢弃后 startOffset
              // 不再准确，但那时「加载更早」已被 atCap 停用，不会被用到。
              return next.length > maxLoadedFrames ? next.slice(next.length - maxLoadedFrames) : next
            })
          }
        }
        if (!cancelled) setActive(false)
      } catch (err) {
        if (cancelled) return // 组件卸载中止是预期收尾，不算错误
        if (err instanceof DOMException && err.name === 'AbortError') return
        setError(errorMessage(err))
        setActive(false)
      }
    }

    void run()
    return () => {
      cancelled = true
      ac.abort() // 离开页面必须中止常驻连接，否则每次进出都泄漏一条
    }
  }, [taskId, reloadKey])

  const retry = useCallback(() => setReloadKey((k) => k + 1), [])

  // loadEarlier 由 Task 7 实现；此刻是占位，保证接口形状稳定。
  const loadEarlier = useCallback(() => {}, [])

  return {
    frames,
    badLines,
    startOffset,
    error,
    active,
    atCap: frames.length >= maxLoadedFrames,
    loadingEarlier,
    loadEarlier,
    retry,
  }
}
```

注意 `loadingEarlier` 此刻恒为 `false`，`loadEarlier` 是个空函数——两者都是**接口形状占位**，让 Task 8 的容器能照最终签名写，而不是等 Task 7 才知道 hook 长什么样。Task 7 会把这两处换成真实实现，其余代码一行不动。

- [ ] **Step 2: typecheck 与 lint**

Run: `cd web && npx tsc -b && npx eslint src/app/task/useFramesStream.ts`
Expected: 两条都无输出

- [ ] **Step 3: 手工验证形状（临时脚本，不提交）**

Run:
```bash
cd web && npx vitest run src/app/task/frames.test.ts src/app/task/codeText.test.tsx src/app/task/blocks.test.tsx
```
Expected: PASS。hook 的行为验证放在 Task 8 的容器测试里（那里用 `vi.mock` 打桩，不需要真发请求）；本步骤只确认已有测试没被这次改动打破。

- [ ] **Step 4: 可观测性自检**

对照文件里那段「不静默」注释逐条确认代码真的做到了：

- 401 → `error` 有专门文案，且 `active` 置 false（不许一边报错一边显示「跟随中」）
- 非 2xx → 优先透出 agentd 的错误原文，读不到才回退状态码
- 无 `resp.body` → 有独立错误文案，不静默返回
- 坏行 → `badLines` 累加
- 到达上限 → `atCap` 为真（由 `frames.length` 推出，不需要额外状态）
- 流正常结束 → `active` 置 false
- `AbortError` 与 `cancelled` → **不**置 error（卸载中止是预期收尾，报错才是 bug）

- [ ] **Step 5: 注释自检**

确认：文件头写了数据源、职责、边界、四条语义注意、以及 AbortController 那条坑；`pageBytes` 写了为什么与 render 同量级；`FramesStream` 每个字段都有注释；「不静默」清单在文件里；`startOffset` 的推算方式与「推小了只会多请求一次」的理由有注释；丢最旧那段有注释说明为什么此时 `startOffset` 失准无害。

- [ ] **Step 6: Commit**

```bash
git add web/src/app/task/useFramesStream.ts
git commit -m "feat(web): useFramesStream 进入与跟随"
```

---

## Task 7: useFramesStream —— 加载更早

**Files:**
- Modify: `web/src/app/task/useFramesStream.ts`

**Interfaces:**
- Consumes: Task 6 的全部内容
- Produces: `loadEarlier()` 的真实实现；`loadingEarlier` 在回翻期间为 true；`startOffset` 每次回翻减少 `pageBytes` 直到 0

- [ ] **Step 1: 把占位换成实现**

在 `useFramesStream.ts` 里：

1. 把 `const [loadingEarlier] = useState(false)` 改回 `const [loadingEarlier, setLoadingEarlier] = useState(false)`。

2. 在主 effect 之后、`retry` 之前，加一个中止控制器 ref 与实现：

```ts
  // earlierAcRef 持有正在进行的回翻请求，卸载时一并中止。
  // 与主流的 AbortController 分开：回翻是一次性请求，中止它不该影响跟随。
  const earlierAcRef = useRef<AbortController | null>(null)
  useEffect(() => () => earlierAcRef.current?.abort(), [])

  // loadEarlier 往前取一页并 prepend。
  //
  // 为什么用 offset 而不是 tail：接口的 offset 与 tail 互斥，且 tail 只能从文件尾
  // 回溯——回翻要的是「从更早的某个字节开始」，只有 offset 能表达。
  //
  // 为什么按 seq 去重而不是按字节数截断：服务端会跳过 offset 落进的那半行
  // （W4a §7.2），所以实际起点会比请求的 offset 靠后一点，按字节算会错位。
  // 帧的 seq 是任务内单调递增的，用它去重既精确又能直接当停止条件——
  // 一旦读到 seq >= 当前最小 seq，说明已经追上已加载区间，可以中止请求了。
  const loadEarlier = useCallback(() => {
    if (!taskId) return
    if (loadingEarlier) return
    if (startOffset <= 0) return // 已到文件头
    if (frames.length >= maxLoadedFrames) return // 到顶，改用 handoff frames

    const from = Math.max(0, startOffset - pageBytes)
    const minSeq = frames.length > 0 ? frames[0].seq : Number.MAX_SAFE_INTEGER
    const ac = new AbortController()
    earlierAcRef.current?.abort()
    earlierAcRef.current = ac
    setLoadingEarlier(true)

    const run = async () => {
      try {
        const resp = await fetch(
          `/api/tasks/${encodeURIComponent(taskId)}/frames?offset=${from}`,
          { credentials: 'same-origin', signal: ac.signal },
        )
        if (resp.status === 401) {
          setError('未授权：会话已失效，请重新打开控制台')
          return
        }
        if (!resp.ok) {
          let msg = `agentd 返回 ${resp.status} ${resp.statusText}`
          try {
            const body = (await resp.json()) as { error?: string }
            if (body.error) msg = body.error
          } catch {
            // 响应体不是 JSON，保留兜底文案
          }
          setError(msg)
          return
        }
        if (!resp.body) {
          setError('响应没有可读流（浏览器不支持 ReadableStream？）')
          return
        }
        const reader = resp.body.getReader()
        const decoder = new TextDecoder()
        let buffered = ''
        let bad = 0
        const earlier: Frame[] = []
        for (;;) {
          const { done, value } = await reader.read()
          if (done) break
          const scan = scanLines(buffered, decoder.decode(value, { stream: true }))
          buffered = scan.rest
          bad += scan.bad
          let caughtUp = false
          for (const fr of scan.frames) {
            if (fr.seq >= minSeq) {
              caughtUp = true // 已追上已加载区间，后面的都是重复
              break
            }
            earlier.push(fr)
          }
          if (caughtUp || earlier.length >= maxLoadedFrames) {
            ac.abort() // 主动收线：不把整个文件读完
            break
          }
        }
        if (bad > 0) setBadLines((n) => n + bad)
        setFrames((prev) => {
          const next = earlier.concat(prev)
          // 回翻方向到顶时丢最新的：用户正在往前看，保留他要看的那一头
          return next.length > maxLoadedFrames ? next.slice(0, maxLoadedFrames) : next
        })
        setStartOffset(from)
        setError(null)
      } catch (err) {
        if (err instanceof DOMException && err.name === 'AbortError') return
        setError(errorMessage(err))
      } finally {
        setLoadingEarlier(false)
      }
    }
    void run()
  }, [taskId, loadingEarlier, startOffset, frames])
```

3. 删掉原来的 `const loadEarlier = useCallback(() => {}, [])` 占位行。

4. 在文件头注释的「语义注意」列表末尾补一条：
```
//   - 回翻用 offset（与 tail 互斥），并按帧 seq 去重——服务端会跳过 offset 落进的
//     那半行，实际起点比请求的 offset 靠后，按字节算会错位
```

- [ ] **Step 2: typecheck 与 lint**

Run: `cd web && npx tsc -b && npx eslint src/app/task/useFramesStream.ts`
Expected: 两条都无输出

- [ ] **Step 3: 跑既有测试确认没打破**

Run: `cd web && npx vitest run`
Expected: PASS（全部既有用例）

- [ ] **Step 4: 可观测性自检**

- 回翻期间 `loadingEarlier` 为真（面板据此把按钮变成「加载中…」并禁用），`finally` 保证它一定复位
- 回翻失败 → `error` 有值，且 `startOffset` **不**前移（失败不该让按钮消失）
- 回翻成功 → `setError(null)` 清掉上一次的错误条，不留过期错误
- 回翻遇到坏行 → 同样累加 `badLines`，不因为是回翻就静默
- `startOffset <= 0` / `atCap` / 正在回翻 → 直接 return，不发重复请求
- 主动 `ac.abort()` 收线时不置 error（那是预期收尾）

- [ ] **Step 5: 注释自检**

确认：`loadEarlier` 写了「为什么用 offset 而不是 tail」和「为什么按 seq 去重而不是按字节」；`earlierAcRef` 写了为什么与主流的控制器分开；`caughtUp` 与主动 abort 有注释；回翻方向丢最新那行有注释。

- [ ] **Step 6: Commit**

```bash
git add web/src/app/task/useFramesStream.ts
git commit -m "feat(web): useFramesStream 加载更早（offset 分页 + seq 去重）"
```

---

## Task 8: TimelinePanel 容器

**Files:**
- Create: `web/src/app/task/TimelinePanel.tsx`
- Create: `web/src/app/task/TimelinePanel.test.tsx`

**Interfaces:**
- Consumes: `useFramesStream` / `FramesStream`（Task 6-7）；`buildBlocks` / `turnsOf`（Task 3）；五个块组件（Task 5）；`RenderPanel`（既有，零改动）
- Produces: `export function TimelinePanel({ taskId, taskState }: { taskId: string; taskState: string })`

- [ ] **Step 1: 写失败的测试**

创建 `web/src/app/task/TimelinePanel.test.tsx`：
```tsx
// TimelinePanel 的容器行为测试：hook 用 vi.mock 打桩，不发真实请求。
//
// 断言的都是「不静默」相关的行为：坏行计数在顶部、错误条带重试、到达文件头时
// 「加载更早」消失、上限提示、事件标记不可操作。样式一概不测。
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import type { Frame } from '../../api/types'
import type { FramesStream } from './useFramesStream'
import { useFramesStream } from './useFramesStream'
import { TimelinePanel } from './TimelinePanel'

vi.mock('./useFramesStream', async (orig) => ({
  ...(await orig<typeof import('./useFramesStream')>()),
  useFramesStream: vi.fn(),
}))
// RenderPanel 会真发 fetch，切到原始视图的用例里用一个哑桩顶替
vi.mock('./RenderPanel', () => ({ RenderPanel: () => <div>原始实况桩</div> }))

afterEach(cleanup)

const frame = (o: Partial<Frame> & { seq: number; type: string }): Frame =>
  ({ ts: '2026-08-12T10:00:00+08:00', turn: 1, ...o }) as Frame

const stream = (o: Partial<FramesStream> = {}): FramesStream => ({
  frames: [], badLines: 0, startOffset: 0, error: null,
  active: true, atCap: false, loadingEarlier: false,
  loadEarlier: vi.fn(), retry: vi.fn(),
  ...o,
})

describe('TimelinePanel', () => {
  it('无帧时显示「等待模型输出…」而不是报错', () => {
    vi.mocked(useFramesStream).mockReturnValue(stream())
    render(<TimelinePanel taskId="t1" taskState="running" />)
    expect(screen.getByText(/等待模型输出/)).toBeInTheDocument()
  })

  it('坏行计数显示在顶部', () => {
    vi.mocked(useFramesStream).mockReturnValue(stream({ badLines: 2 }))
    render(<TimelinePanel taskId="t1" taskState="running" />)
    expect(screen.getByText(/2 行无法解析/)).toBeInTheDocument()
  })

  it('错误时给错误条 + 重试按钮，点了会调 retry', () => {
    const retry = vi.fn()
    vi.mocked(useFramesStream).mockReturnValue(stream({ error: '连接中断', retry }))
    render(<TimelinePanel taskId="t1" taskState="running" />)
    expect(screen.getByRole('alert')).toHaveTextContent('连接中断')
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(retry).toHaveBeenCalled()
  })

  it('startOffset=0 时「加载更早」消失（已到文件头）', () => {
    vi.mocked(useFramesStream).mockReturnValue(stream({
      startOffset: 0,
      frames: [frame({ seq: 1, type: 'text', part: 'p01', delta: 'a' })],
    }))
    render(<TimelinePanel taskId="t1" taskState="running" />)
    expect(screen.queryByRole('button', { name: /加载更早/ })).not.toBeInTheDocument()
  })

  it('startOffset>0 时「加载更早」出现，点了会调 loadEarlier', () => {
    const loadEarlier = vi.fn()
    vi.mocked(useFramesStream).mockReturnValue(stream({ startOffset: 65536, loadEarlier }))
    render(<TimelinePanel taskId="t1" taskState="running" />)
    fireEvent.click(screen.getByRole('button', { name: /加载更早/ }))
    expect(loadEarlier).toHaveBeenCalled()
  })

  it('到达帧数上限时提示改用 handoff frames，且不再提供「加载更早」', () => {
    vi.mocked(useFramesStream).mockReturnValue(stream({ atCap: true, startOffset: 65536 }))
    render(<TimelinePanel taskId="t1" taskState="running" />)
    expect(screen.getByText(/handoff frames/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /加载更早/ })).not.toBeInTheDocument()
  })

  it('回合锚点从已加载帧生成，并标注只覆盖已加载范围', () => {
    vi.mocked(useFramesStream).mockReturnValue(stream({
      startOffset: 65536,
      frames: [
        frame({ seq: 1, turn: 7, type: 'turn_start', reason: 'send' }),
        frame({ seq: 2, turn: 8, type: 'turn_start', reason: 'send' }),
      ],
    }))
    render(<TimelinePanel taskId="t1" taskState="running" />)
    expect(screen.getByRole('button', { name: '7' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '8' })).toBeInTheDocument()
    expect(screen.getByText(/更早的需先加载/)).toBeInTheDocument()
  })

  it('已到文件头时锚点提示改成「已覆盖全部回合」', () => {
    vi.mocked(useFramesStream).mockReturnValue(stream({
      startOffset: 0,
      frames: [frame({ seq: 1, turn: 1, type: 'turn_start', reason: 'dispatch' })],
    }))
    render(<TimelinePanel taskId="t1" taskState="running" />)
    expect(screen.getByText(/已覆盖全部回合/)).toBeInTheDocument()
  })

  it('时间线里的事件标记不可操作（整个面板里没有审批按钮）', () => {
    vi.mocked(useFramesStream).mockReturnValue(stream({
      frames: [frame({ seq: 1, type: 'event', ref_seq: 88, event: 'permission_request' })],
    }))
    render(<TimelinePanel taskId="t1" taskState="running" />)
    expect(screen.queryByRole('button', { name: /批准|拒绝/ })).not.toBeInTheDocument()
    expect(screen.getByText(/裁决入口在右侧工单区/)).toBeInTheDocument()
  })

  it('开关能切到原始实况正文，并能切回来', () => {
    vi.mocked(useFramesStream).mockReturnValue(stream())
    render(<TimelinePanel taskId="t1" taskState="running" />)
    fireEvent.click(screen.getByRole('button', { name: /原始正文/ }))
    expect(screen.getByText('原始实况桩')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /回合时间线/ }))
    expect(screen.getByText(/等待模型输出/)).toBeInTheDocument()
  })

  it('流结束后徽章从「跟随中」变「已结束」', () => {
    vi.mocked(useFramesStream).mockReturnValue(stream({ active: false }))
    render(<TimelinePanel taskId="t1" taskState="completed" />)
    expect(screen.getByText('已结束')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/task/TimelinePanel.test.tsx`
Expected: FAIL —— 找不到模块 `./TimelinePanel`

- [ ] **Step 3: 写实现**

创建 `web/src/app/task/TimelinePanel.tsx`：
```tsx
// TimelinePanel —— 结构化回合时间线（任务详情页左列的主视图）。
//
// 职责：
//   - 把 useFramesStream 的帧交给 frames.ts 聚合成块，逐块渲染
//   - 回合分隔与顶部锚点条、加载更早、跟随徽章、坏行与上限提示
//   - 一个开关切回原始 render.log 流（RenderPanel）
//
// 边界：
//   - 不解析原始文本（那是 frames.ts），不发请求（那是 useFramesStream）
//   - 不提供任何审批入口。同一张工单在三处出现是有意的：时间线管因果、
//     EventsPanel 管实时、TicketsPanel 管裁决——审批不可逆，只留一个入口
//
// 为什么保留原始视图：这批帧是四个 adapter 各自分流出来的，质量并不齐平
// （grok 的工具信息只是人类摘要、codex 没有真实抓包因而没有黄金基线）。
// 撞上「某家 adapter 的帧不完整」时，能一键退回原始文本是区分「渲染错了」
// 还是「采集错了」的关键证据。等四家帧质量都被真机验证过，再谈取代。
//
// 切换会卸载对侧的流（原始视图重新从 tail=65536 开始）。这是刻意的：两个视图
// 各自维护加载位置，而让两条常驻连接同时开着换取「切回去还在原位」是更坏的交易。
import { useLayoutEffect, useMemo, useRef, useState } from 'react'
import { Eye, List } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { buildBlocks, turnsOf } from './frames'
import { useFramesStream } from './useFramesStream'
import { RenderPanel } from './RenderPanel'
import { TextBlock } from './TextBlock'
import { ThinkingBlock } from './ThinkingBlock'
import { ToolCard } from './ToolCard'
import { EventMark } from './EventMark'
import { UnknownBlock } from './UnknownBlock'

// TURN_REASON 把 turn_start 的 reason 映射成中文。
// 只有两个取值（W4a §3.2：dispatch = Adapter.Start，send = Adapter.Send），
// 未知取值原样显示，不吞掉。
const TURN_REASON: Record<string, string> = { dispatch: '派发', send: '续发指令' }

// stickThreshold 是「算作在底部」的像素阈值，与 RenderPanel 保持一致：
// 距底这么近才自动跟随，用户往上翻则停止跟随，不抢滚轮。
const stickThreshold = 40

// TimelinePanel 渲染一个任务的结构化回合时间线。
//
// 参数：
//   - taskId: 任务完整 UUID
//   - taskState: 任务当前状态，用于判定未配对工具卡是「进行中」还是「未返回」
export function TimelinePanel({ taskId, taskState }: { taskId: string; taskState: string }) {
  const [raw, setRaw] = useState(false)
  const { frames, badLines, startOffset, error, active, atCap, loadingEarlier, loadEarlier, retry } =
    useFramesStream(raw ? undefined : taskId)

  const blocks = useMemo(() => buildBlocks(frames), [frames])
  const turns = useMemo(() => turnsOf(frames), [frames])

  const scrollRef = useRef<HTMLDivElement>(null)
  const stickBottom = useRef(true)
  // prependRef 记录 prepend 之前的 scrollHeight；prepend 后按差值补偿滚动位置，
  // 否则每次「加载更早」都会把用户弹到别处。
  const prependRef = useRef<number | null>(null)

  useLayoutEffect(() => {
    const el = scrollRef.current
    if (!el) return
    if (prependRef.current !== null) {
      // 本次变化来自 prepend：补偿高度差，视线停在原处，且不触发跟随
      el.scrollTop += el.scrollHeight - prependRef.current
      prependRef.current = null
      return
    }
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < stickThreshold
    if (stickBottom.current || nearBottom) {
      el.scrollTop = el.scrollHeight
      stickBottom.current = true
    }
  }, [blocks])

  const onScroll = () => {
    const el = scrollRef.current
    if (!el) return
    stickBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < stickThreshold
  }

  const onLoadEarlier = () => {
    prependRef.current = scrollRef.current?.scrollHeight ?? 0
    loadEarlier()
  }

  const gotoTurn = (t: number) => {
    document.getElementById(`turn-${taskId}-${t}`)?.scrollIntoView({ block: 'start' })
  }

  return (
    <section className="flex flex-col gap-2 rounded-lg border bg-background p-4">
      <header className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="flex items-center gap-2 text-sm font-medium">
          {raw ? <Eye className="size-4" /> : <List className="size-4" />}
          {raw ? '实况正文（原始）' : '回合时间线'}
        </h2>
        <div className="flex items-center gap-2">
          {!raw && <Badge variant={active ? 'default' : 'secondary'}>{active ? '跟随中' : '已结束'}</Badge>}
          <Button variant="outline" size="sm" onClick={() => setRaw((v) => !v)}>
            {raw ? '回合时间线' : '原始正文'}
          </Button>
        </div>
      </header>

      {raw ? (
        // 原始视图：RenderPanel 零改动地整块放进来，自带它自己的头与流
        <RenderPanel taskId={taskId} />
      ) : (
        <>
          {turns.length > 0 && (
            <div className="flex flex-wrap items-center gap-1.5 text-xs">
              <span className="text-muted-foreground">回合</span>
              {turns.map((t) => (
                <button
                  key={t}
                  type="button"
                  onClick={() => gotoTurn(t)}
                  className="rounded border px-2 py-0.5 hover:border-foreground"
                >
                  {t}
                </button>
              ))}
              {/* 锚点只覆盖已加载范围，必须写出来——不假装是全量目录 */}
              <span className="ml-auto text-[11px] text-muted-foreground">
                {startOffset > 0 ? '仅覆盖已加载范围，更早的需先加载' : '已覆盖全部回合'}
              </span>
            </div>
          )}

          {badLines > 0 && (
            <p className="rounded border border-amber-500/40 bg-amber-500/5 px-2.5 py-1.5 text-xs text-amber-600 dark:text-amber-500">
              ⚠ {badLines} 行无法解析，已跳过（其余帧不受影响；帧文件可能被截断或采集侧有 bug）
            </p>
          )}

          {atCap && (
            <p className="rounded border px-2.5 py-1.5 text-xs text-muted-foreground">
              已加载帧数到上限，不再往前加载——更早的内容请用 <span className="font-mono">handoff frames</span> 回看
            </p>
          )}

          {error && (
            <p role="alert" className="flex flex-wrap items-center gap-2 break-words text-sm text-destructive">
              {error}
              <Button variant="outline" size="sm" onClick={retry}>重试</Button>
            </p>
          )}

          <div ref={scrollRef} onScroll={onScroll} className="h-96 overflow-y-auto rounded-md bg-muted/30 p-3">
            {startOffset > 0 && !atCap && (
              <div className="mb-2 flex justify-center">
                <Button variant="ghost" size="sm" disabled={loadingEarlier} onClick={onLoadEarlier}>
                  {loadingEarlier ? '加载中…' : '↑ 加载更早'}
                </Button>
              </div>
            )}
            {blocks.length === 0 && error === null ? (
              <p className="text-sm text-muted-foreground">等待模型输出…（frames.jsonl 尚为空属正常）</p>
            ) : (
              <div className="flex flex-col gap-1.5">
                {blocks.map((b) => {
                  switch (b.kind) {
                    case 'turn':
                      return (
                        <div
                          key={b.key}
                          id={`turn-${taskId}-${b.turn}`}
                          className="mt-2 flex items-center gap-2 text-[11px] text-muted-foreground first:mt-0"
                        >
                          <span className="h-px flex-1 bg-border" />
                          <span>
                            <b className="font-semibold text-foreground">回合 {b.turn}</b>
                            {' · '}
                            {TURN_REASON[b.reason] ?? b.reason}
                          </span>
                          <span className="h-px flex-1 bg-border" />
                        </div>
                      )
                    case 'text':
                      return <TextBlock key={b.key} text={b.text} />
                    case 'thinking':
                      return <ThinkingBlock key={b.key} text={b.text} />
                    case 'tool':
                      return <ToolCard key={b.key} block={b} taskState={taskState} />
                    case 'event':
                      return <EventMark key={b.key} event={b.event} ts={b.ts} />
                    case 'unknown':
                      return <UnknownBlock key={b.key} type={b.type} raw={b.raw} />
                  }
                })}
              </div>
            )}
          </div>
        </>
      )}
    </section>
  )
}
```

注意 `useFramesStream(raw ? undefined : taskId)`：切到原始视图时给 hook 传 `undefined`，它的 effect 直接 return 并在清理时 abort，帧流连接就断开了——不留第二条常驻连接。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/task/TimelinePanel.test.tsx`
Expected: PASS（11 个用例）

- [ ] **Step 5: 可观测性自检**

逐条确认这些状态在界面上看得见：

- 坏行计数在顶部，且文案点明可能的原因（截断 / 采集侧 bug），不是干巴巴一个数字
- 错误条带 `role="alert"` 与重试按钮——断了却看着像「还在跟随」是最坏的一种
- 到达上限有提示，且明确给出出口（`handoff frames`）
- 跟随中 / 已结束两种徽章互斥可见
- 锚点条明确标注「仅覆盖已加载范围」，到头后才改成「已覆盖全部回合」
- 空帧显示「等待模型输出…」并说明这属正常，**不**当成错误
- 回翻进行中按钮显示「加载中…」并禁用，不让用户连点

- [ ] **Step 6: 注释自检**

确认：文件头写了职责、两条边界（不解析、不提供审批入口）、为什么保留原始视图、切换会卸载对侧流；`stickThreshold` 写了与 RenderPanel 一致；`prependRef` 写了补偿的理由；锚点提示那行有「不假装是全量目录」的注释；`TURN_REASON` 写了只有两个取值且未知原样显示。

- [ ] **Step 7: Commit**

```bash
git add web/src/app/task/TimelinePanel.tsx web/src/app/task/TimelinePanel.test.tsx
git commit -m "feat(web): TimelinePanel 容器（锚点 / 回翻 / 跟随 / 原始视图开关）"
```

---

## Task 9: 接进 TaskPage 并全量验收

**Files:**
- Modify: `web/src/app/task/TaskPage.tsx`（第 29 行 import、第 204 行左列槽位）

**Interfaces:**
- Consumes: `TimelinePanel`（Task 8）
- Produces: 无（终点任务）

- [ ] **Step 1: 换掉左列槽位**

在 `web/src/app/task/TaskPage.tsx` 里：

1. 把 `import { RenderPanel } from './RenderPanel'` 改成 `import { TimelinePanel } from './TimelinePanel'`。

2. 把左列的
```tsx
              <RenderPanel taskId={id} />
```
改成
```tsx
              <TimelinePanel taskId={id} taskState={detail.task.state} />
```

3. 更新 `TaskPage.tsx` 头注释的「数据编排」小节：把
```
//   - GET /api/tasks/{id}/render 实况流（RenderPanel 内自管 AbortController）
```
改成
```
//   - GET /api/tasks/{id}/frames 结构化回合流（TimelinePanel → useFramesStream
//     内自管 AbortController）；切到原始视图时改用 /render（RenderPanel），
//     两条流互斥，任一时刻只开一条
```

- [ ] **Step 2: typecheck**

Run: `cd web && npx tsc -b`
Expected: 无输出

- [ ] **Step 3: lint**

Run: `cd web && npx eslint .`
Expected: 无 error（warning 若有，逐条看是不是新引入的；新引入的必须修掉）

- [ ] **Step 4: 全量测试**

Run: `cd web && npx vitest run`
Expected: 全绿，且既有用例一个不红。本计划新增的用例数：`frames.test.ts` 21、`codeText.test.tsx` 9、`blocks.test.tsx` 11、`TimelinePanel.test.tsx` 11、`contract.test.ts` +2 —— 合计 54。少于这个数说明前面哪个任务漏写了用例，回去补。

- [ ] **Step 5: 构建**

Run: `cd web && npx vite build`
Expected: 成功，无 error

- [ ] **Step 6: 确认零依赖改动与零后端改动**

Run:
```bash
cd /Users/xushixin/workspace/handoff/.claude/worktrees/web-console
git diff --stat main -- web/package.json web/package-lock.json internal/ cmd/
```
Expected: **无输出**。有输出说明违反了 Global Constraints，回退那部分改动。

Run:
```bash
git diff --stat main -- web/src/app/task/RenderPanel.tsx web/src/app/task/useRenderStream.ts
```
Expected: **无输出**（spec §2 要求这两个文件一行不改）。

- [ ] **Step 7: 真机验收（对照原型）**

在一台有真实跑过的任务的机器上启 agentd 与 dev server：

```bash
cd web && npm run dev -- --host 127.0.0.1
```

用 `handoff` 生成的控制台链接登录后打开一个**已完成**任务的详情页，逐条对照
`prototypes/w4b-timeline/index.html`（在浏览器里并排打开它）：

1. 回合分隔线出现，`reason` 显示成「派发」/「续发指令」
2. 顶部锚点条列出已加载回合，点击能跳转；提示文案与是否到头一致
3. 思维链默认折叠成「🧠 思维链 · N 字」，点开有全文
4. 工具卡默认折叠；成功 / 失败两种状态在同一个任务里都能找到实例
5. 找一个 executor 半路死掉的任务（或 `handoff stop` 造一个），确认未配对的
   工具卡显示「未返回」而不是「进行中」
6. 「加载更早」能一路翻到回合 1，翻的过程中视线不被弹走
7. 切到「原始正文」，内容与 `handoff frames <task>` 的输出对得上（帧的原文）；
   再切回时间线，两边不互相污染
8. 开一个**正在跑**的任务，确认新帧实时追加、底部自动跟随，往上翻则停止跟随

任一条对不上就记下来，不要「差不多就行」——这份原型就是为了让「差不多」无处可藏。

- [ ] **Step 8: 最终代码审阅清单**

逐项确认（用户全局 CLAUDE.md §5）：

- 完成目标：spec §2–§9 每一条都有对应实现
- 架构一致：解析 / I/O / 展示三分，`frames.ts` 不 import react（`grep -n "from 'react'" web/src/app/task/frames.ts` 应无输出）
- 文件头注释：8 个新文件（frames.ts / codeText.tsx / 五个块组件 / useFramesStream.ts / TimelinePanel.tsx）都有职责与边界
- 方法注释：所有导出函数有参数 / 返回 / 注意事项
- 中文注释：非显然分支都写了「为什么」
- 可观测性：无 `console.log` / `console.warn`（`grep -rn "console\." web/src/app/task/` 应只命中既有文件或无输出）；每条降级都有对应的 UI 状态
- 优先复用：stick-bottom 抄的是 RenderPanel 已验证的逻辑；错误处理形状抄的是 useRenderStream；`vi.mock` 打桩抄的是 MachinesPage.test.tsx
- 无硬编码：`maxLoadedFrames` / `pageBytes` / `stickThreshold` 都是具名常量

- [ ] **Step 9: Commit**

```bash
git add web/src/app/task/TaskPage.tsx
git commit -m "feat(web): 任务详情页左列换成结构化回合时间线"
```

---

## 附录：真机排障速查

| 症状 | 多半是 | 先看哪 |
|---|---|---|
| 时间线永远「等待模型输出…」，但实况正文有内容 | 该任务是 W4a 合并**之前**跑的，没有 frames.jsonl | 切原始视图确认；换一个新任务 |
| 顶部一直显示 N 行无法解析 | 采集侧写坏了行，或响应被中间层改写 | `handoff frames <task> \| head`，看那几行长什么样 |
| 工具卡全是「未知工具」 | 某家 adapter 的 tool_call 没填 `tool` | 切原始视图对照，判断是渲染错还是采集错——这正是保留开关的理由 |
| 「加载更早」点了没反应 | `startOffset` 已经是 0，或 `atCap` 为真 | 看按钮在不在；不在就是这两种之一 |
| 切换视图后滚动位置丢了 | 预期行为（两个视图各自维护加载位置） | 不是 bug，见 spec §2 |
| 进出详情页几次后请求变慢 | AbortController 没接上，连接泄漏 | 浏览器 Network 面板看有几条 pending 的 frames 请求，应恒为 0 或 1 |
