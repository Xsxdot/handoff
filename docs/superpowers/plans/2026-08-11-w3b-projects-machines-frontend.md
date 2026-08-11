# W3b：项目与机器控制面（前端）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 `web/` 里把 `prototypes/desktop-console/` 原型中「W3a 能供数」的那一块实现出来——app shell、左栏项目树、看板筛选与卡片元信息、开发机页（只读）、项目登记向导。

**Architecture:** 在 W2 已有的 React 19 + Tailwind 4 + shadcn 基座上加一层 `<Shell>`（顶部 tab + 常驻左栏 + `<Outlet>`），三条独立节奏的数据流（任务 2.5s / 项目树 30s / 机器 15s）由三个 hook 各自持有，看板筛选是一个 `BoardFilter` 对象、左栏与顶部下拉是它的两个编辑入口。所有筛选在客户端完成，不新增后端往返。

**Tech Stack:** React 19、TypeScript 5.8、Tailwind CSS 4、shadcn/ui（`@radix-ui/react-slot` + cva）、react-router-dom 7、Vite 6、vitest 4 + @testing-library/react。

## Global Constraints

- **只动 `web/`**。不改 `internal/`、`cmd/`。实现中若发现后端缺数据，**停下来上报**，不在前端硬编码兜底（W3b spec §12）。
- **不新增 npm 依赖**。下拉菜单、向导弹窗一律手写，风格对齐已有的 `web/src/app/lib/ConfirmDialog.tsx`。需要新依赖时停下来上报。
- **不引任何 CDN 资源**（字体、图标、CSS）。agentd 托管的控制台可能跑在无外网环境（spec §7）。
- 视觉对照基准视口固定 **1440×1024**，与 `prototypes/desktop-console/design-qa.md` 同基准。
- **未实现的功能整块不渲染，不留置灰入口**（spec §0）。唯一例外：「可用执行者」以只读列表呈现，仅开关不渲染。
- **禁止静默失败**：任何请求失败都要有用户可见的降级展示（复用 `web/src/app/lib/Banners.tsx`）。`console.error` 只作开发期辅助，**不作为错误处理手段**（spec §10）。
- 三条数据流互不影响：机器探活失败不得让看板空掉。
- 每个新建文件写文件头注释（职责 + 边界）；每个导出函数写用途注释；非显然分支写「为什么」注释。范本是 `web/src/app/board/BoardPage.tsx` 顶部那段。
- W2 已有的行为测试（`columns.test.ts`、`review.test.ts`、`TicketsPanel.test.tsx`、`contract.test.ts`、`ws.test.ts`）**不得变红**。若因换皮变红，说明它测错了层，顺手修掉并在提交信息里说明。
- 命令一律在 `web/` 目录下执行：`npm test`、`npm run typecheck`、`npm run lint`。

---

## 契约附录（规范性）

**这一节是本计划的契约真相。** `internal/proto/` 的 Go 结构体与 `web/src/api/testdata/*.json` 由审核者在派发前对齐到此形状。实现时**以本附录为准**；若发现分支上的 fixture 与此不符，**停下来上报，不要自行改形状**——两侧各改一半会让契约测试测了个寂寞。

来源：W3a spec §2 / §3 / §4 / §5.3，B62 spec §6.3。

```ts
// ---------- 项目树 ----------

// Workspace 是一个 git 工作树（含主工作区自身）。W3a §2。
export interface Workspace {
  path: string
  branch: string    // detached 时为空串
  head: string      // 短 sha
  is_main: boolean
  managed: boolean  // true = agentd 自建的任务工作树
}

// ProjectLocationNode 是一个项目在一台机器上的位置。
// 不变式：单机响应里每个项目的 locations 恒为 0 或 1 条（W3a §1.1）。
export interface ProjectLocationNode {
  machine: string          // ""=本机；否则为 cfg.Targets 的键
  name: string             // 登记名
  path: string
  workspaces: Workspace[]
  probe_error: string      // 探测失败的人话说明，空串=正常
}

export interface ProjectNode {
  project_id: string
  origin_url: string
  name: string
  locations: ProjectLocationNode[]
}

// MachineStatus 是跨机汇总信封里的每台机器的应答情况。W3a §5.3。
// 硬约束：任何一台没答上来都必须出现在这里且 ok=false 带原因。
export interface MachineStatus {
  name: string
  ok: boolean
  fetched_at: string
  error: string
}

// ProjectTreeResp 是 GET /api/projects/tree 的响应；
// machines 仅在 ?scope=all 时出现。
export interface ProjectTreeResp {
  projects: ProjectNode[]
  unowned: string[]           // 算不出 project_id 的脏行（登记名），诚实列出不吞
  machines?: MachineStatus[]
}

// ---------- 机器 ----------

// Machine 是 GET /api/machines 的单台投影。W3a §4。
export interface Machine {
  name: string              // ""=本机
  addr: string
  reachable: boolean
  version: string
  executors: string[]
  default_executor: string
  probe_ms: number          // 本机恒 0（进程内直查）
  active_tasks: number
  error: string             // reachable=false 时必非空
}

export interface MachinesResp {
  machines: Machine[]
}

// ---------- Task 的两个注解字段（加进已有的 Task 接口）----------
//   machine:    string   ""=本机；否则为本机 cfg.Targets 的键，由汇总方盖章
//   project_id: string   归属项目；未归属为 ""

// ---------- 项目登记 ----------

// CreateProjectReq 是 POST /api/projects 的请求体。B62 spec §6.3。
//   带 path  = 登记该机器上已有目录（本机永远走这条）
//   不带 path = 由该机器 clone 到自己的 repo_root/<name>
export interface CreateProjectReq {
  origin_url: string
  name?: string
  path?: string
}

// CreateProjectResp 是 201 响应体。
export interface CreateProjectResp {
  project_id: string
  name: string
  path: string
}
```

**跨机路由**（W3a spec §5.1.1，写本计划时发现缺口后补入）：

```
POST   /api/projects?machine=<name>          往指定机器登记
DELETE /api/projects/{name}?machine=<name>   注销指定机器上的位置
GET    /api/projects/tree?scope=all          跨机汇总
```

`machine` 省略或为空串 = 本机。本机 agentd 收到非空 `machine` 时按 `cfg.Targets` 转发，**响应状态码与中文报错原文原样透传**。理由：W3a §5.1 的透明转发是按任务 id 路由的，项目登记不是任务，没有 id 可路由，必须显式给机器名。

---

## Task 1: 契约类型与 API 客户端扩展

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/api/client.ts`
- Test: `web/src/api/contract.test.ts`（已存在，追加用例）

**Interfaces:**
- Consumes: 无（本计划起点）
- Produces: `Workspace`、`ProjectLocationNode`、`ProjectNode`、`MachineStatus`、`ProjectTreeResp`、`Machine`、`MachinesResp`、`CreateProjectReq`、`CreateProjectResp` 类型；`Task.machine` / `Task.project_id` 字段；函数 `fetchProjectTree(scope?: 'all')`、`fetchMachines()`、`createProject(req, machine?)`、`deleteProject(name, machine?)`

- [ ] **Step 1: 先看一眼 fixture 是否已到位**

Run: `ls web/src/api/testdata/`

期望看到 `ProjectTreeResp.json`、`MachinesResp.json`，且 `Task.json` 含 `machine` 与 `project_id` 两个键；`Repo.json` 应已消失（B62 删了 `proto.Repo`）。

**若不符合：停下来上报，不要自己造 fixture。** fixture 由 Go 侧测试生成并逐字节钉住，手写一份只会让两侧契约测试各自为政。

- [ ] **Step 2: 写失败的契约测试**

追加到 `web/src/api/contract.test.ts`（沿用文件里已有的 fixture 读取风格）：

```ts
import projectTreeFixture from './testdata/ProjectTreeResp.json'
import machinesFixture from './testdata/MachinesResp.json'
import taskFixture from './testdata/Task.json'
import type { MachinesResp, ProjectTreeResp, Task } from './types'

describe('W3a 契约', () => {
  it('ProjectTreeResp 的字段与类型一致', () => {
    const resp: ProjectTreeResp = projectTreeFixture
    expect(Array.isArray(resp.projects)).toBe(true)
    expect(Array.isArray(resp.unowned)).toBe(true)
    const loc = resp.projects[0].locations[0]
    // 单机响应的不变式：每个项目在每台机器上至多一个位置（W3a §1.1）
    expect(resp.projects[0].locations.length).toBeLessThanOrEqual(1)
    expect(typeof loc.machine).toBe('string')
    expect(typeof loc.probe_error).toBe('string')
    expect(Array.isArray(loc.workspaces)).toBe(true)
    expect(typeof loc.workspaces[0].is_main).toBe('boolean')
    expect(typeof loc.workspaces[0].managed).toBe('boolean')
  })

  it('MachinesResp 带 W3b 需要的三个只读投影', () => {
    const resp: MachinesResp = machinesFixture
    const m = resp.machines[0]
    expect(Array.isArray(m.executors)).toBe(true)
    expect(typeof m.default_executor).toBe('string')
    expect(typeof m.probe_ms).toBe('number')
    expect(typeof m.reachable).toBe('boolean')
    expect(typeof m.error).toBe('string')
  })

  it('Task 带 machine 与 project_id 两个注解字段', () => {
    const t: Task = taskFixture
    expect(typeof t.machine).toBe('string')
    expect(typeof t.project_id).toBe('string')
  })
})
```

- [ ] **Step 3: 运行测试确认失败**

Run: `npm test -- contract`
Expected: FAIL，报类型不存在 / fixture 导入失败。

- [ ] **Step 4: 把契约附录的类型抄进 types.ts**

原样落入本计划「契约附录」里的全部 `export interface`，每个都带上附录里的注释。同时给已有的 `Task` 接口末尾加两行：

```ts
  machine: string      // ""=本机；否则为本机 cfg.Targets 的键，由汇总方盖章（W3a §3）
  project_id: string   // 归属项目；未归属为 ""（W3a §1.3）
```

- [ ] **Step 5: 在 client.ts 加四个函数**

```ts
// machineQuery 把机器名编码成查询串；空串（本机）不带参数。
//
// 为什么用查询参数而不是请求体字段：登记请求体是 B62 定的、由本机 agentd
// 原样转发给目标机，往里塞路由字段会污染 B62 的契约。
function machineQuery(machine?: string, sep: '?' | '&' = '?'): string {
  return machine ? `${sep}machine=${encodeURIComponent(machine)}` : ''
}

// fetchProjectTree 取项目树（GET /api/projects/tree）。
//
// 参数：
//   - scope: 传 'all' 取跨机汇总版（响应多一个 machines 字段，见 §5.3）
//
// 注意：本接口带 git worktree 现场探测，**不要放进 2.5s 热路径**。
export function fetchProjectTree(scope?: 'all'): Promise<ProjectTreeResp> {
  return request<ProjectTreeResp>(`/api/projects/tree${scope === 'all' ? '?scope=all' : ''}`)
}

// fetchMachines 取机器投影与探活结果（GET /api/machines）。
// 单台不可达是数据不是错误：整体仍 200，该台 reachable=false 且 error 带原文。
export function fetchMachines(): Promise<MachinesResp> {
  return request<MachinesResp>('/api/machines')
}

// createProject 登记一个项目位置（POST /api/projects）。
//
// 参数：
//   - req: 带 path = 登记该机已有目录；不带 path = 由该机 clone 到自己的 repo_root
//   - machine: 目标机器名；省略或空串 = 本机
export function createProject(req: CreateProjectReq, machine?: string): Promise<CreateProjectResp> {
  return postJSON<CreateProjectResp>(`/api/projects${machineQuery(machine)}`, req)
}

// deleteProject 注销一个项目位置（DELETE /api/projects/{name}）。
// 只解除登记，不删除磁盘上的代码。
export function deleteProject(name: string, machine?: string): Promise<{ ok: boolean }> {
  return request<{ ok: boolean }>(
    `/api/projects/${encodeURIComponent(name)}${machineQuery(machine)}`,
    { method: 'DELETE' },
  )
}
```

补齐 `import type` 里的新类型。

- [ ] **Step 6: 运行测试确认通过**

Run: `npm test -- contract && npm run typecheck`
Expected: PASS。

- [ ] **Step 7: 提交**

```bash
git add web/src/api/types.ts web/src/api/client.ts web/src/api/contract.test.ts
git commit -m "feat(web): W3a 契约类型与项目树/机器/登记四个客户端函数"
```

---

## Task 2: 三条数据流的轮询 hook

**Files:**
- Create: `web/src/app/data/usePoll.ts`
- Create: `web/src/app/data/useTasks.ts`
- Create: `web/src/app/data/useProjectTree.ts`
- Create: `web/src/app/data/useMachines.ts`
- Test: `web/src/app/data/usePoll.test.ts`

**Interfaces:**
- Consumes: Task 1 的 `fetchProjectTree` / `fetchMachines`；已有的 `fetchTasks`
- Produces:
  - `usePoll<T>(fetcher: () => Promise<T>, intervalMs: number, opts?: { enabled?: boolean }): PollState<T>`
  - `type PollState<T> = { data: T | null; disconnected: boolean; sessionExpired: boolean; errorText: string; refresh: () => void }`
  - `useTasks(): PollState<Task[]>`（2500ms）
  - `useProjectTree(): PollState<ProjectTreeResp>`（30000ms，`scope=all`）
  - `useMachines(enabled: boolean): PollState<MachinesResp>`（15000ms）

- [ ] **Step 1: 写失败的测试**

`web/src/app/data/usePoll.test.ts`：

```ts
import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../../api/client'
import { usePoll } from './usePoll'

describe('usePoll', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('立即首拉，然后按间隔续拉', async () => {
    const fetcher = vi.fn().mockResolvedValue('v1')
    const { result } = renderHook(() => usePoll(fetcher, 1000))
    await waitFor(() => expect(result.current.data).toBe('v1'))
    expect(fetcher).toHaveBeenCalledTimes(1)
    await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
    expect(fetcher).toHaveBeenCalledTimes(2)
  })

  it('页面隐藏时停表，可见时立即补拉', async () => {
    const fetcher = vi.fn().mockResolvedValue('v')
    renderHook(() => usePoll(fetcher, 1000))
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1))

    Object.defineProperty(document, 'hidden', { value: true, configurable: true })
    act(() => { document.dispatchEvent(new Event('visibilitychange')) })
    await act(async () => { await vi.advanceTimersByTimeAsync(3000) })
    expect(fetcher).toHaveBeenCalledTimes(1) // 停表期间一次都没打

    Object.defineProperty(document, 'hidden', { value: false, configurable: true })
    act(() => { document.dispatchEvent(new Event('visibilitychange')) })
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2)) // 立即补拉
  })

  it('失败时保留上一次数据并标断开', async () => {
    const fetcher = vi.fn().mockResolvedValueOnce('good').mockRejectedValue(new ApiError(0, '连不上'))
    const { result } = renderHook(() => usePoll(fetcher, 1000))
    await waitFor(() => expect(result.current.data).toBe('good'))
    await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
    await waitFor(() => expect(result.current.disconnected).toBe(true))
    expect(result.current.data).toBe('good') // 断线不清空
    expect(result.current.errorText).toContain('连不上')
  })

  it('401 停表并落终止态', async () => {
    const fetcher = vi.fn().mockRejectedValue(new ApiError(401, '会话失效'))
    const { result } = renderHook(() => usePoll(fetcher, 1000))
    await waitFor(() => expect(result.current.sessionExpired).toBe(true))
    const calls = fetcher.mock.calls.length
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    expect(fetcher).toHaveBeenCalledTimes(calls) // 不再重试
  })

  it('enabled=false 时一次都不拉', async () => {
    const fetcher = vi.fn().mockResolvedValue('v')
    renderHook(() => usePoll(fetcher, 1000, { enabled: false }))
    await act(async () => { await vi.advanceTimersByTimeAsync(3000) })
    expect(fetcher).not.toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: 运行测试确认失败**

Run: `npm test -- usePoll`
Expected: FAIL，`usePoll` 不存在。

- [ ] **Step 3: 实现 usePoll**

`web/src/app/data/usePoll.ts`。文件头注释必须写清职责与边界：

```ts
// usePoll —— 三条数据流共用的轮询原语。
//
// 职责：
//   - 立即首拉 + 定时续拉，返回 { data, disconnected, sessionExpired, errorText, refresh }
//   - 复刻 W2 在 BoardPage 里验证过的三条实时性纪律：document.hidden 停表、
//     断线保留最后数据、401 落终止态不再重试
//
// 边界：
//   - 不关心具体接口，fetcher 由调用方给
//   - 不做请求去重与缓存：三条流各自独立，共享缓存只会让"机器探活失败"
//     污染看板（spec §10 要求三条流互不影响）
//
// 为什么把 W2 BoardPage 里的循环提取出来：W3b 有三条节奏不同的流，
// 复制三份轮询逻辑意味着三份各自会跑偏的 document.hidden 处理。
```

实现要点（照 `BoardPage.tsx:45-97` 的循环搬，加 `enabled` 与 `refresh`）：

```ts
export interface PollState<T> {
  data: T | null
  disconnected: boolean
  sessionExpired: boolean
  errorText: string
  refresh: () => void
}

export function usePoll<T>(
  fetcher: () => Promise<T>,
  intervalMs: number,
  opts?: { enabled?: boolean },
): PollState<T> {
  const enabled = opts?.enabled ?? true
  const [data, setData] = useState<T | null>(null)
  const [disconnected, setDisconnected] = useState(false)
  const [sessionExpired, setSessionExpired] = useState(false)
  const [errorText, setErrorText] = useState('')
  // fetcher 常是内联箭头函数，放进 ref 避免每次渲染重启轮询
  const fetcherRef = useRef(fetcher)
  fetcherRef.current = fetcher
  const [nonce, setNonce] = useState(0)

  const refresh = useCallback(() => setNonce((n) => n + 1), [])

  useEffect(() => {
    if (!enabled) return
    let stopped = false
    let timer: number | undefined
    const stopTimer = () => { if (timer !== undefined) { window.clearInterval(timer); timer = undefined } }

    const poll = async () => {
      try {
        const v = await fetcherRef.current()
        if (stopped) return
        setData(v)
        setDisconnected(false)
      } catch (err) {
        if (stopped) return
        if (err instanceof ApiError && err.status === 401) {
          // 会话失效是终止态：继续轮询只会刷 401
          stopTimer()
          setSessionExpired(true)
          return
        }
        // 断线保留 data 不清空——空看板比旧看板更误导
        setDisconnected(true)
        setErrorText(errorMessage(err))
      }
    }

    const startTimer = () => { if (timer === undefined) timer = window.setInterval(poll, intervalMs) }
    const onVisibility = () => {
      if (document.hidden) stopTimer()
      else { startTimer(); void poll() }
    }

    void poll()
    startTimer()
    document.addEventListener('visibilitychange', onVisibility)
    return () => {
      stopped = true
      stopTimer()
      document.removeEventListener('visibilitychange', onVisibility)
    }
  }, [enabled, intervalMs, nonce])

  return { data, disconnected, sessionExpired, errorText, refresh }
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `npm test -- usePoll`
Expected: PASS（5 个用例）。

- [ ] **Step 5: 三个具体 hook**

三个薄封装文件，每个带文件头注释说明**为什么是这个节奏**：

```ts
// useTasks.ts
// 任务流：2.5s。W2 已验证的节奏，W3b 不改——看板卡片、左栏任务节点、
// 全部聚合计数都从这条流算，它跳动其余才跟着跳动。
const TASKS_INTERVAL = 2500
export function useTasks(): PollState<Task[]> {
  return usePoll(fetchTasks, TASKS_INTERVAL)
}

// useProjectTree.ts
// 项目树：30s + 写操作后 refresh() 立即失效重拉。
// 为什么不进 2.5s 热路径：这个接口带 git worktree list 现场探测，
// 每 2.5s 对所有 location 探一遍纯属浪费；而结构（项目/机器/目录）变化极慢，
// 所有运行态都来自任务流，慢刷不影响体感（spec §6）。
const TREE_INTERVAL = 30000
export function useProjectTree(): PollState<ProjectTreeResp> {
  return usePoll(() => fetchProjectTree('all'), TREE_INTERVAL)
}

// useMachines.ts
// 机器探活：15s，且**仅在 /machines 可见时**开表。
// 探活会向每台远程机发 GET /api/status，没人看的时候没有理由持续打扰它们。
const MACHINES_INTERVAL = 15000
export function useMachines(enabled: boolean): PollState<MachinesResp> {
  return usePoll(fetchMachines, MACHINES_INTERVAL, { enabled })
}
```

- [ ] **Step 6: 类型检查与全量测试**

Run: `npm run typecheck && npm test`
Expected: 全部 PASS，W2 已有测试无变红。

- [ ] **Step 7: 提交**

```bash
git add web/src/app/data
git commit -m "feat(web): 三条数据流的轮询 hook（2.5s 任务 / 30s 树 / 15s 机器）"
```

---

## Task 3: 视觉令牌迁移与字体自托管

**Files:**
- Modify: `web/src/index.css`
- Create: `web/public/fonts/Geist-Variable.woff2`（从原型复制）
- Modify: `web/src/app/board/BoardPage.tsx`（密度与分隔线对齐）
- Modify: `web/src/app/task/TaskPage.tsx`（同上）

**Interfaces:**
- Consumes: 无
- Produces: 迁移后的 theme 令牌（后续所有任务的组件直接用 `bg-background` / `text-muted-foreground` 等语义 class，不写字面色值）

- [ ] **Step 1: 复制字体并确认体积**

```bash
mkdir -p web/public/fonts
cp prototypes/desktop-console/src/assets/fonts/Geist-Variable.woff2 web/public/fonts/
ls -lh web/public/fonts/Geist-Variable.woff2
```

- [ ] **Step 2: 读原型的视觉决策**

Run: `sed -n '1,120p' prototypes/desktop-console/src/styles.css`

从中抠出：中性色板的实际取值、圆角（7–9px）、hairline 分隔线的颜色与宽度、字号阶梯与行高。**只抄令牌，不抄那 3389 行组件 CSS**——W2 已建在 shadcn 上，整体移植会让 W2 部分变成样式孤岛（spec §7）。

- [ ] **Step 3: 改 index.css**

在文件顶部 `@import 'tailwindcss'` 之后加 `@font-face`：

```css
/* Geist 自托管：agentd 托管的控制台可能跑在无外网环境，不引 CDN（spec §7） */
@font-face {
  font-family: 'Geist';
  src: url('/fonts/Geist-Variable.woff2') format('woff2-variations');
  font-weight: 100 900;
  font-display: swap;
}
```

`:root` 里：
- `--font-sans` 改为 `'Geist', ui-sans-serif, system-ui, …, 'PingFang SC', 'Microsoft YaHei', sans-serif`（**保留中文回退**，Geist 不含中文字形）；
- `--radius` 按原型改为 `0.5rem`（8px，落在 7–9px 区间中点）；
- 中性色板按 Step 2 抠出的取值替换 `--background` / `--foreground` / `--muted` / `--muted-foreground` / `--border` / `--sidebar*`。

`.dark` 段同步调整（本轮不做主题切换，但保持 shadcn 契约完整）。

- [ ] **Step 4: 对齐 W2 两页的密度**

`BoardPage.tsx` 与 `TaskPage.tsx` 只动间距与分隔线（`gap-*`、`p-*`、`border-*`），**不动任何逻辑与文案**。shadcn 组件用的是语义 class，换令牌后自动跟随。

- [ ] **Step 5: 跑测试，确认换皮没弄红行为测试**

Run: `npm test`
Expected: 全部 PASS。

若 `columns.test.ts` / `review.test.ts` / `TicketsPanel.test.tsx` 变红——它们断言的是逻辑与文案，换皮不该影响。说明该测试测错了层（大概率断言了 class 名）。修掉它，并在提交信息里写清改了什么、为什么。

- [ ] **Step 6: 目视确认字体真的加载了**

```bash
npm run dev
```

浏览器开 `http://localhost:5173`，DevTools → Network 过滤 `woff2`，确认 `Geist-Variable.woff2` 200 且**不是从外部域**加载。

- [ ] **Step 7: 提交**

```bash
git add web/src/index.css web/public/fonts web/src/app/board/BoardPage.tsx web/src/app/task/TaskPage.tsx
git commit -m "style(web): 迁移原型视觉令牌，Geist 自托管，W2 两页密度对齐"
```

---

## Task 4: App Shell 与路由改造

**Files:**
- Create: `web/src/app/shell/Shell.tsx`
- Create: `web/src/app/shell/TopTabs.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/app/board/BoardPage.tsx`（去掉自带的整页外框）
- Test: `web/src/app/shell/Shell.test.tsx`

**Interfaces:**
- Consumes: 无
- Produces: `<Shell>` 组件（顶部 tab + 左栏插槽 + `<Outlet>`）；路由结构 `/` → BoardPage、`/machines` → MachinesPage、`/tasks/:id` → TaskPage，三者共用 Shell

- [ ] **Step 1: 写失败的测试**

`web/src/app/shell/Shell.test.tsx`：

```tsx
import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { Shell } from './Shell'

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route element={<Shell />}>
          <Route path="/" element={<div>看板内容</div>} />
          <Route path="/machines" element={<div>机器内容</div>} />
          <Route path="/tasks/:id" element={<div>详情内容</div>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  )
}

describe('Shell', () => {
  it('三条路由都嵌在 shell 里，左栏常驻', () => {
    for (const [path, text] of [['/', '看板内容'], ['/machines', '机器内容'], ['/tasks/abc', '详情内容']] as const) {
      const { unmount } = renderAt(path)
      expect(screen.getByText(text)).toBeInTheDocument()
      expect(screen.getByRole('complementary')).toBeInTheDocument() // 左栏
      unmount()
    }
  })

  it('当前 tab 有选中态', () => {
    renderAt('/machines')
    expect(screen.getByRole('link', { name: '开发机' })).toHaveAttribute('aria-current', 'page')
  })

  it('不渲染未实现功能的入口（齿轮/设置）', () => {
    renderAt('/')
    expect(screen.queryByRole('button', { name: /设置/ })).toBeNull()
  })
})
```

- [ ] **Step 2: 运行测试确认失败**

Run: `npm test -- Shell`
Expected: FAIL，`Shell` 不存在。

- [ ] **Step 3: 实现 Shell 与 TopTabs**

`Shell.tsx` 文件头注释：

```tsx
// Shell —— 控制台的三段外框：顶部 tab 条 + 常驻左栏项目树 + 中央内容区。
//
// 职责：
//   - 提供三条路由共用的外框，内容区用 <Outlet> 承载
//   - 持有跨页共享的两条数据流（任务流、项目树流）并下发，避免每页各拉一份
//
// 边界：
//   - 不渲染任何未实现功能的入口（左栏齿轮、设置页、配对开发机）——
//     置灰控件承诺"以后能用"，用户会反复点；缺一个按钮反而诚实（spec §0）
//   - 不持有机器流：那只在 /machines 可见时开表（spec §6）
```

布局：`grid grid-cols-[260px_1fr] grid-rows-[auto_1fr] h-dvh`，顶部 tab 条跨两列，左栏 `<aside>`（`role="complementary"`），右侧 `<main>` 放 `<Outlet>`。

`TopTabs.tsx`：两个 `<NavLink>`（任务看板 → `/`，开发机 → `/machines`），选中态用 `aria-current="page"`（`NavLink` 自带）。`/tasks/:id` 时保持「任务看板」高亮。

- [ ] **Step 4: 改 App.tsx 的路由结构**

```tsx
<BrowserRouter>
  <Routes>
    <Route element={<Shell />}>
      <Route path="/" element={<BoardPage />} />
      <Route path="/machines" element={<MachinesPage />} />
      <Route path="/tasks/:id" element={<TaskPage />} />
    </Route>
  </Routes>
</BrowserRouter>
```

同步更新 `App.tsx` 顶部的路由说明注释（现有注释只列了两条）。

本任务 `MachinesPage` 先放一个最小占位（`export function MachinesPage() { return <div /> }`），Task 8 填实。**占位不渲染任何假数据与假控件**。

- [ ] **Step 5: BoardPage 从「整页」降级为「内容区」**

去掉 `BoardPage.tsx` 里的 `min-h-dvh`、`bg-muted/40` 外层与自带 `<header>`（标题「handoff 控制台 · 任务看板」移入 Shell 的 tab 条）。四列看板与全部逻辑不动。

- [ ] **Step 6: 运行测试确认通过**

Run: `npm test && npm run typecheck`
Expected: 全部 PASS。

- [ ] **Step 7: 提交**

```bash
git add web/src/App.tsx web/src/app/shell web/src/app/board/BoardPage.tsx
git commit -m "feat(web): app shell——顶部 tab + 常驻左栏 + Outlet 内容区"
```

---

## Task 5: BoardFilter——筛选状态的单一真相

**Files:**
- Create: `web/src/app/board/filter.ts`
- Test: `web/src/app/board/filter.test.ts`

**Interfaces:**
- Consumes: `Task`、`ProjectNode`
- Produces:
  - `type BoardFilter`（见下）
  - `EMPTY_FILTER: BoardFilter`
  - `selectProject(f, projectId): BoardFilter`、`selectMachine(f, machine): BoardFilter`、`selectWorkspace(f, path): BoardFilter`、`setProjects(f, ids, tree): BoardFilter`、`setMachine(f, m): BoardFilter`、`setSearch(f, s)`、`setPendingOnly(f, b)`
  - `applyFilter(tasks: Task[], f: BoardFilter, tree: ProjectNode[]): Task[]`

- [ ] **Step 1: 写失败的测试**

`web/src/app/board/filter.test.ts`：

```ts
import { describe, expect, it } from 'vitest'
import type { ProjectNode } from '../../api/types'
import type { Task } from '../../api/types'
import {
  EMPTY_FILTER, applyFilter, selectMachine, selectProject, selectWorkspace,
  setMachine, setPendingOnly, setProjects, setSearch,
} from './filter'

const tree: ProjectNode[] = [
  { project_id: 'p1', origin_url: 'git@x:/a.git', name: 'alpha', locations: [
      { machine: '', name: 'alpha', path: '/a', probe_error: '', workspaces: [] },
      { machine: 'devbox', name: 'alpha', path: '/srv/a', probe_error: '', workspaces: [] } ] },
  { project_id: 'p2', origin_url: 'git@x:/b.git', name: 'beta', locations: [
      { machine: '', name: 'beta', path: '/b', probe_error: '', workspaces: [] } ] },
]

function t(over: Partial<Task>): Task {
  return {
    id: 'i', target: '', repo_path: '', branch: '', plan_path: '', plan_summary: '',
    executor_session: '', state: 'running', created_at: '', updated_at: '', name: '',
    executor: 'opencode', model: '', work_dir: '', worktree_managed: false,
    base_commit: '', base_ahead: 0, repo_dirty_count: 0, repo_dirty_files: '',
    machine: '', project_id: 'p1', ...over,
  }
}

describe('BoardFilter 写入规则', () => {
  it('点项目会清空 machine 与 workspace（换项目后旧的收窄没有意义）', () => {
    const f = selectWorkspace(selectMachine(selectProject(EMPTY_FILTER, 'p1'), 'devbox'), '/srv/a')
    const next = selectProject(f, 'p2')
    expect(next.projects).toEqual(new Set(['p2']))
    expect(next.machine).toBeNull()
    expect(next.workspace).toBeNull()
  })

  it('点机器保留项目、清空 workspace', () => {
    const f = selectWorkspace(selectMachine(selectProject(EMPTY_FILTER, 'p1'), 'devbox'), '/srv/a')
    const next = selectMachine(f, '')
    expect(next.projects).toEqual(new Set(['p1']))
    expect(next.machine).toBe('')
    expect(next.workspace).toBeNull()
  })

  it('顶部多选改 projects；若当前 machine 不再属于任一选中项目则一并清空', () => {
    const f = selectMachine(selectProject(EMPTY_FILTER, 'p1'), 'devbox')
    const next = setProjects(f, new Set(['p2']), tree) // p2 只有本机位置
    expect(next.machine).toBeNull()
  })

  it('顶部多选改 projects；machine 仍属于选中项目时保留', () => {
    const f = selectMachine(selectProject(EMPTY_FILTER, 'p1'), 'devbox')
    const next = setProjects(f, new Set(['p1', 'p2']), tree)
    expect(next.machine).toBe('devbox')
  })

  it('空集 = 全部，不是"一个都不选"', () => {
    const tasks = [t({ id: 'a', project_id: 'p1' }), t({ id: 'b', project_id: 'p2' })]
    expect(applyFilter(tasks, EMPTY_FILTER, tree)).toHaveLength(2)
  })
})

describe('applyFilter', () => {
  const tasks = [
    t({ id: 'a', project_id: 'p1', machine: '',       work_dir: '/a',    name: '重构登录', state: 'running' }),
    t({ id: 'b', project_id: 'p1', machine: 'devbox', work_dir: '/srv/a', name: '修 CI',   state: 'waiting_answer' }),
    t({ id: 'c', project_id: 'p2', machine: '',       work_dir: '/b',    name: '写文档',   state: 'completed' }),
    t({ id: 'd', project_id: '',   machine: '',       work_dir: '/x',    name: '游离任务', state: 'running' }),
  ]

  it('按项目筛', () => {
    expect(applyFilter(tasks, selectProject(EMPTY_FILTER, 'p1'), tree).map((x) => x.id)).toEqual(['a', 'b'])
  })

  it('按机器收窄（""=本机，不是"不筛"）', () => {
    const f = selectMachine(selectProject(EMPTY_FILTER, 'p1'), '')
    expect(applyFilter(tasks, f, tree).map((x) => x.id)).toEqual(['a'])
  })

  it('按工作树收窄', () => {
    const f = selectWorkspace(selectMachine(selectProject(EMPTY_FILTER, 'p1'), 'devbox'), '/srv/a')
    expect(applyFilter(tasks, f, tree).map((x) => x.id)).toEqual(['b'])
  })

  it('只看待处理 = waiting_answer + waiting_review', () => {
    expect(applyFilter(tasks, setPendingOnly(EMPTY_FILTER, true), tree).map((x) => x.id)).toEqual(['b'])
  })

  it('搜索匹配任务名、项目名、执行者名', () => {
    expect(applyFilter(tasks, setSearch(EMPTY_FILTER, '登录'), tree).map((x) => x.id)).toEqual(['a'])
    expect(applyFilter(tasks, setSearch(EMPTY_FILTER, 'beta'), tree).map((x) => x.id)).toEqual(['c'])
    expect(applyFilter(tasks, setSearch(EMPTY_FILTER, 'opencode'), tree)).toHaveLength(4)
  })

  it('未归属任务不被项目筛选吞掉——不选项目时它必须在', () => {
    expect(applyFilter(tasks, EMPTY_FILTER, tree).map((x) => x.id)).toContain('d')
  })
})
```

- [ ] **Step 2: 运行测试确认失败**

Run: `npm test -- filter`
Expected: FAIL，模块不存在。

- [ ] **Step 3: 实现 filter.ts**

文件头注释必须点明**为什么是单一真相**：

```ts
// filter.ts —— 看板筛选状态的唯一真相。
//
// 职责：
//   - 定义 BoardFilter 的形状与全部写入规则（纯函数，无 React 依赖）
//   - 把任务列表按 filter 过滤（全部在客户端做）
//
// 边界：
//   - 不碰 React state、不发请求；组件层只负责调用这些纯函数
//   - 不做后端过滤：看板已 2.5s 全量拉 /api/tasks，改走后端只会让筛选
//     变成一次网络往返、并与轮询节奏打架（spec §3.1）
//
// 为什么左栏与顶部下拉共用同一个对象而不是两套联动状态：两套状态一定会
// 出现"左栏选了 A、顶部显示 B、看板按 C 筛"的第三种状态，用户看到的是
// "筛了个项目结果一个任务都没有"。一个对象两个编辑入口，永远不会打架。
// W4 引入 workbench 后点目录应切换工作区而非筛看板，本文件届时须重写。

export type BoardFilter = {
  projects: Set<string>     // project_id 集合；空集 = 不按项目筛（全部），不是"一个都不选"
  machine: string | null    // 机器名（""=本机）；null = 不按机器筛
  workspace: string | null  // 工作树路径；null = 不按工作树筛
  search: string
  pendingOnly: boolean
}
```

写入规则严格按 spec §2.3：

| 入口 | `projects` | `machine` | `workspace` |
|---|---|---|---|
| `selectProject` | `{id}` | 清 `null` | 清 `null` |
| `selectMachine` | 保持 | 设 | 清 `null` |
| `selectWorkspace` | 保持 | 保持 | 设 |
| `setProjects`（顶部多选） | 设 | 若不再属于任一选中项目则清 | 同左 |
| `setMachine`（顶部下拉） | 保持 | 设 | 清 `null` |
| `setSearch` / `setPendingOnly` | 保持 | 保持 | 保持 |

`applyFilter` 的关键分支各写一句「为什么」注释，尤其这两处：
- `machine: ''` 是「本机」不是「不筛」——用 `f.machine !== null` 判断，**不能用真值判断**，否则本机永远筛不出来；
- 未归属任务（`project_id === ''`）在 `projects` 为空集时必须保留（spec §8「不静默丢弃」）。

搜索匹配任务名、项目名（经 tree 反查）、执行者名，大小写不敏感。

- [ ] **Step 4: 运行测试确认通过**

Run: `npm test -- filter`
Expected: PASS（11 个用例）。

- [ ] **Step 5: 提交**

```bash
git add web/src/app/board/filter.ts web/src/app/board/filter.test.ts
git commit -m "feat(web): BoardFilter——左栏与顶部下拉共用的筛选单一真相"
```

---

## Task 6: 左栏项目树

**Files:**
- Create: `web/src/app/tree/counts.ts`
- Create: `web/src/app/tree/ProjectTree.tsx`
- Test: `web/src/app/tree/counts.test.ts`
- Test: `web/src/app/tree/ProjectTree.test.tsx`
- Modify: `web/src/app/shell/Shell.tsx`（挂上树）

**Interfaces:**
- Consumes: Task 1 的类型、Task 2 的 `useTasks` / `useProjectTree`、Task 5 的 `BoardFilter` 与写入函数
- Produces:
  - `type NodeCounts = { dirs: number; running: number; pending: number }`
  - `countsForProject(tasks, project): NodeCounts`、`countsForMachine(tasks, project, machine): NodeCounts`
  - `<ProjectTree tree filter onFilterChange onOpenTask />`

- [ ] **Step 1: 写失败的计数测试**

`web/src/app/tree/counts.test.ts`：

```ts
import { describe, expect, it } from 'vitest'
import { countsForMachine, countsForProject } from './counts'
// 复用 Task 5 测试里的 t() 与 tree 构造（照抄到本文件，测试之间不互相 import）

describe('聚合计数', () => {
  it('目录数 = 该项目所有机器下的 workspace 总数', () => {
    const project = { project_id: 'p1', origin_url: '', name: 'alpha', locations: [
      { machine: '', name: 'alpha', path: '/a', probe_error: '', workspaces: [
        { path: '/a', branch: 'main', head: 'abc', is_main: true, managed: false },
        { path: '/a-wt', branch: 'feat', head: 'def', is_main: false, managed: true } ] },
      { machine: 'devbox', name: 'alpha', path: '/srv/a', probe_error: '', workspaces: [
        { path: '/srv/a', branch: 'main', head: 'abc', is_main: true, managed: false } ] },
    ] }
    expect(countsForProject([], project).dirs).toBe(3)
    expect(countsForMachine([], project, 'devbox').dirs).toBe(1)
  })

  it('运行数只数 running；待处理数 = waiting_answer + waiting_review', () => {
    const tasks = [
      t({ project_id: 'p1', machine: '', state: 'running' }),
      t({ project_id: 'p1', machine: 'devbox', state: 'running' }),
      t({ project_id: 'p1', machine: '', state: 'waiting_answer' }),
      t({ project_id: 'p1', machine: '', state: 'waiting_review' }),
      t({ project_id: 'p1', machine: '', state: 'completed' }),
      t({ project_id: 'p2', machine: '', state: 'running' }),   // 别的项目，不该被算进来
    ]
    const project = { project_id: 'p1', origin_url: '', name: 'alpha', locations: [] }
    expect(countsForProject(tasks, project)).toMatchObject({ running: 2, pending: 2 })
    expect(countsForMachine(tasks, project, '')).toMatchObject({ running: 1, pending: 2 })
  })

  it('计数从任务流算，探测失败的 location 不影响运行/待处理数', () => {
    const project = { project_id: 'p1', origin_url: '', name: 'alpha', locations: [
      { machine: '', name: 'alpha', path: '/a', probe_error: '目录不存在', workspaces: [] } ] }
    const tasks = [t({ project_id: 'p1', machine: '', state: 'running' })]
    expect(countsForProject(tasks, project)).toMatchObject({ dirs: 0, running: 1 })
  })
})
```

- [ ] **Step 2: 运行确认失败，然后实现 counts.ts**

Run: `npm test -- counts` → FAIL。

`counts.ts` 文件头注释要写清**为什么计数从任务流算而不从树算**：树 30s 才刷一次，计数挂在它上面就会有 30 秒的滞后；挂在 2.5s 的任务流上，绿点与数字跟着一起跳（spec §2.2 / §6）。

- [ ] **Step 3: 运行确认通过**

Run: `npm test -- counts`
Expected: PASS（3 个用例）。

- [ ] **Step 4: 写失败的树渲染测试**

`web/src/app/tree/ProjectTree.test.tsx` 覆盖四条硬约束：

```tsx
it('层级是 项目 → 机器 → 目录 → 任务', () => { /* 展开后逐层可见 */ })

it('不可达机器保持可见、标已断开、且不可展开', () => {
  // machines: [{ name: 'devbox', ok: false, error: 'dial tcp ...' }]
  // 断言：devbox 节点在 DOM 里；文案含「已断开」；点击不展开出子节点
  // 绝不静默少一台——这是 spec §8 的头号失败模式
})

it('probe_error 只影响该 location，不炸整棵树', () => {
  // 一个 location 带 probe_error，另一个正常
  // 断言：报错文案可见，且另一个项目的节点仍正常渲染
})

it('未归属任务挂在末尾的「未归属」分组，不被吞掉', () => {
  // 一个 project_id:'' 的任务
  // 断言：存在「未归属」分组且该任务在其中
})

it('点项目/机器/目录写 filter，点任务导航', () => {
  // 用 vi.fn() 断言 onFilterChange 收到的对象与 Task 5 的写入规则一致
})

it('多选时左栏不高亮单项，显示选中计数', () => {
  // filter.projects.size > 1 → 断言出现「已选 2 个项目」而没有单项高亮
})
```

- [ ] **Step 5: 运行确认失败，然后实现 ProjectTree.tsx**

Run: `npm test -- ProjectTree` → FAIL。

实现要点：
- 层级严格三层 + 任务层。**一台机器下不再分组**——W3a §1.1 保证一个项目在一台机器上至多一个 location，这条不变式可以依赖（spec §2.1）。
- 任务节点由 `useTasks` 的数据按 `project_id` + `machine` + `work_dir` 挂到对应目录下。
- 不可达机器（`machines[].ok === false`）渲染为可见、标「已断开」、`aria-disabled` 且不响应展开。原因原文可见（hover 或副标题，按截图）。
- `probe_error` 非空的 location：机器节点仍在，其下渲染报错说明，不渲染 workspaces。
- 「未归属」分组固定在树末尾。
- 左栏底部：「+ 添加项目」按钮（Task 9 接上）。**齿轮不渲染**。

- [ ] **Step 6: 挂进 Shell**

`Shell.tsx` 里持有 `useTasks()` 与 `useProjectTree()`，把 `BoardFilter` 提升为 Shell 的 state，通过 `<Outlet context={...} />` 下发给 BoardPage（react-router 的 `useOutletContext`）。

- [ ] **Step 7: 加关键节点的可观测性**

前端没有日志后端，可观测性落在错误可见性上（spec §10）：
- 树加载失败（`useProjectTree().disconnected`）→ 左栏顶部显示 `DisconnectedBanner` 的紧凑变体，**不让左栏空白**；空白无法与「一个项目都没登记」区分。
- `sessionExpired` → 复用 `SessionExpiredBanner`。
- 每个 `probe_error` 与每台不可达机器的 `error` **原文透出**，不缩略成「加载失败」——agentd 的报错里带着解法。

- [ ] **Step 8: 运行全量测试**

Run: `npm test && npm run typecheck && npm run lint`
Expected: 全部 PASS。

- [ ] **Step 9: 提交**

```bash
git add web/src/app/tree web/src/app/shell/Shell.tsx
git commit -m "feat(web): 左栏项目树——三层结构、任务流聚合计数、断开与未归属诚实展示"
```

---

## Task 7: 看板筛选栏与卡片元信息

**Files:**
- Create: `web/src/app/board/FilterBar.tsx`
- Create: `web/src/app/lib/Dropdown.tsx`（手写下拉，供筛选栏与向导共用）
- Modify: `web/src/app/board/BoardPage.tsx`
- Test: `web/src/app/board/FilterBar.test.tsx`

**Interfaces:**
- Consumes: Task 5 的 `BoardFilter` 与写入函数、Task 6 的树数据
- Produces: `<FilterBar filter onChange projects machines taskCounts />`；`<Dropdown label items multiple selected onSelect />`

- [ ] **Step 1: 写失败的测试**

```tsx
// 约定：交互一律用 fireEvent（与 W2 的 TicketsPanel.test.tsx 一致，
// 项目没有 @testing-library/user-event 依赖，也不要为此新增）。
// onChange 是 vi.fn()，断言它收到的 BoardFilter 对象，而不是组件内部
// DOM 结构——形态由截图对照兜底（Task 10），行为由这里兜底。

it('项目多选下拉每项带任务数', () => {
  render(<FilterBar {...base} />)
  fireEvent.click(screen.getByRole('button', { name: /项目/ }))
  expect(screen.getByRole('option', { name: /alpha/ })).toHaveTextContent('3')
})

it('勾选两个项目后 filter.projects 有两个 id', () => {
  const onChange = vi.fn()
  render(<FilterBar {...base} onChange={onChange} />)
  fireEvent.click(screen.getByRole('button', { name: /项目/ }))
  fireEvent.click(screen.getByRole('option', { name: /alpha/ }))
  fireEvent.click(screen.getByRole('option', { name: /beta/ }))
  expect(onChange.mock.calls.at(-1)![0].projects).toEqual(new Set(['p1', 'p2']))
})

it('开发机下拉写 machine；「本机」写的是空串不是 null', () => {
  // 这条容易写错：""=本机，null=不筛，两者语义不同；
  // 写成 null 的话选「本机」等于没筛，看板会把远程任务也列出来
  const onChange = vi.fn()
  render(<FilterBar {...base} onChange={onChange} />)
  fireEvent.click(screen.getByRole('button', { name: /开发机/ }))
  fireEvent.click(screen.getByRole('option', { name: '本机' }))
  expect(onChange.mock.calls.at(-1)![0].machine).toBe('')
})

it('「只看待处理」toggle 写 pendingOnly', () => {
  const onChange = vi.fn()
  render(<FilterBar {...base} onChange={onChange} />)
  fireEvent.click(screen.getByRole('switch', { name: /只看待处理/ }))
  expect(onChange.mock.calls.at(-1)![0].pendingOnly).toBe(true)
})

it('右侧显示筛选后的任务总数，不是筛选前的', () => {
  render(<FilterBar {...base} taskCount={2} />)
  expect(screen.getByText(/共\s*2\s*个任务/)).toBeInTheDocument()
})

it('左栏点击与顶部下拉互推，不产生第三种状态', () => {
  // 左栏选了 p1 + devbox 之后，顶部下拉改选 p2（p2 没有 devbox 位置）
  // → machine 必须被清空，否则会出现"筛了个项目结果一个任务都没有"
  const onChange = vi.fn()
  const filter = { ...EMPTY_FILTER, projects: new Set(['p1']), machine: 'devbox' }
  render(<FilterBar {...base} filter={filter} onChange={onChange} />)
  fireEvent.click(screen.getByRole('button', { name: /项目/ }))
  fireEvent.click(screen.getByRole('option', { name: /alpha/ })) // 取消 p1
  fireEvent.click(screen.getByRole('option', { name: /beta/ }))  // 选上 p2
  const next = onChange.mock.calls.at(-1)![0]
  expect(next.projects).toEqual(new Set(['p2']))
  expect(next.machine).toBeNull()
})
```

- [ ] **Step 2: 运行确认失败**

Run: `npm test -- FilterBar` → FAIL。

- [ ] **Step 3: 实现 Dropdown.tsx**

手写，**不新增 radix 依赖**。风格对齐 `web/src/app/lib/ConfirmDialog.tsx`。要点：
- 键盘可达（Esc 关闭、方向键移动、Enter 选中）；
- 点击外部关闭；
- `multiple` 时项前带 checkbox，不自动关闭。

文件头注释写清为什么手写：本项目当前只依赖 `@radix-ui/react-slot`，为一个下拉引入整套 `react-dropdown-menu` 不划算；控件行为简单且已被测试覆盖。

- [ ] **Step 4: 实现 FilterBar.tsx**

按原型 `implementation-board-project-multiselect.png` 补齐：搜索框（placeholder「搜索任务、项目或执行者」）、项目多选下拉（每项带任务数）、开发机下拉、「只看待处理」toggle、右侧任务总数。

- [ ] **Step 5: BoardPage 接上筛选与卡片元信息**

- 从 `useOutletContext()` 取 `filter` / `setFilter` / `tasks` / `tree`；
- 列渲染改为 `applyFilter(tasks, filter, tree)` 之后再 `stateToColumn` 分列；
- **四列与卡片级状态一个不改**（spec §3.3）；
- 卡片加三行：项目（`project_id` join 树取显示名，空则「未归属」）、工作树（已有的 `task.branch`）、机器（`machine`，`''` 显示「本机」）。

- [ ] **Step 6: 运行测试，确认 columns.test.ts 仍绿**

Run: `npm test`
Expected: 全部 PASS，特别确认 `columns.test.ts` 无变红——列映射是硬契约，本任务不该碰它。

- [ ] **Step 7: 提交**

```bash
git add web/src/app/board web/src/app/lib/Dropdown.tsx
git commit -m "feat(web): 看板筛选栏与卡片项目/工作树/机器元信息"
```

---

## Task 8: 开发机页（只读）

**Files:**
- Create: `web/src/app/machines/MachinesPage.tsx`
- Create: `web/src/app/machines/MachineDetail.tsx`
- Test: `web/src/app/machines/MachinesPage.test.tsx`
- Modify: `web/src/App.tsx`（换掉 Task 4 的占位）

**Interfaces:**
- Consumes: Task 2 的 `useMachines(enabled)`、Task 1 的 `Machine` 类型、项目树（算每台的项目目录数）
- Produces: `<MachinesPage />`

- [ ] **Step 1: 写失败的测试**

```tsx
it('顶部三个统计：台数 / 在线数 / 运行任务数', () => {
  // machines: 本机(可达, active_tasks:2) + devbox(可达, 1) + nas(不可达, 0)
  // 三个统计各用 <dl><dt>标题</dt><dd>数字</dd></dl>，靠 dt 文本定位 dd，
  // 避免 getByText('3') 在多处命中
  renderMachines(threeMachines)
  const stat = (label: string) =>
    screen.getByText(label).parentElement!.querySelector('dd')!.textContent
  expect(stat('开发机')).toBe('3')      // 台数含不可达的那台——少一台就是静默丢机器（spec §8）
  expect(stat('在线')).toBe('2')
  expect(stat('运行任务')).toBe('3')    // 2 + 1 + 0
})

it('不可达机器仍然渲染，标已断开并显示 error 原文', () => {
  // machines: [{ name:'nas', reachable:false, error:'dial tcp ...: connection refused' }]
  // 断言：卡片在；「已断开」可见；error 原文可见（不缩略成「加载失败」）
})

it('本机（name:""）显示「本机」且不显示延迟格', () => {
  // probe_ms 恒为 0（进程内直查），显示「0ms」会误导
})

it('可用执行者渲染为只读列表，默认执行者有标记，且没有任何开关', () => {
  expect(screen.queryByRole('switch')).toBeNull()
  expect(screen.queryByRole('checkbox')).toBeNull()
})

it('不渲染未实现功能：配对开发机 / 重启 agent / 打开终端 / Env 文件', () => {
  for (const name of [/配对/, /重启/, /终端/, /Env/]) {
    expect(screen.queryByRole('button', { name })).toBeNull()
  }
})

it('不渲染「操作系统」格（后端没有这个数据）', () => {
  expect(screen.queryByText(/操作系统/)).toBeNull()
})

it('离开 /machines 后停止探活', () => {
  // useMachines(enabled) 的 enabled 跟随页面挂载
})
```

- [ ] **Step 2: 运行确认失败，然后实现**

Run: `npm test -- MachinesPage` → FAIL。

布局照 `implementation-machines.png`：左侧机器卡片列表 + 右侧选中机器详情。字段来源严格按 spec §4 的表：

| 格子 | 来源 |
|---|---|
| 名称 / 地址 / 已连接·已断开 | `name` / `addr` / `reachable` |
| Agent 版本 | `version` |
| 延迟 | `probe_ms`（本机不显示） |
| 可用执行者 | `executors` + `default_executor`，只读列表 |
| 运行任务数 | `active_tasks` |
| 项目目录数 | 前端按机器聚合项目树 |
| 断开原因 | `error` |
| 最后心跳 | 前端记录上次探活成功时刻，`formatRelative` 显示 |

- [ ] **Step 3: 「最后心跳」的诚实实现**

后端没有这个字段。前端在每次探活成功时记下 `Date.now()`，显示相对时间。**必须标明这是"本页打开以来"的观测**，不能让它冒充服务端心跳——刚打开页面时显示「刚刚」而实际 agentd 已经跑了三天，是一种好看的假象。首次尚无观测时显示「—」。

- [ ] **Step 4: 接上路由**

`App.tsx` 里把 Task 4 的占位换成真的 `MachinesPage`。

- [ ] **Step 5: 运行全量测试**

Run: `npm test && npm run typecheck && npm run lint`
Expected: 全部 PASS。

- [ ] **Step 6: 提交**

```bash
git add web/src/app/machines web/src/App.tsx
git commit -m "feat(web): 开发机页（只读）——探活投影、断开原因原文、执行者只读列表"
```

---

## Task 9: 项目登记向导与注销

**Files:**
- Create: `web/src/app/projects/AddProjectWizard.tsx`
- Create: `web/src/app/projects/register.ts`
- Test: `web/src/app/projects/register.test.ts`
- Test: `web/src/app/projects/AddProjectWizard.test.tsx`
- Modify: `web/src/app/tree/ProjectTree.tsx`（接上「+ 添加项目」与右键/悬浮的注销）

**Interfaces:**
- Consumes: Task 1 的 `createProject` / `deleteProject`、Task 2 的 `useProjectTree().refresh`、`useMachines`
- Produces:
  - `type LocationChoice = { machine: string; gitUrl: string; path: string }`
  - `type RegisterOutcome = { machine: string; ok: boolean; error: string; result?: CreateProjectResp }`
  - `registerAll(choices: LocationChoice[]): Promise<RegisterOutcome[]>`
  - `<AddProjectWizard open machines onClose onDone />`

- [ ] **Step 1: 写失败的 registerAll 测试**

`web/src/app/projects/register.test.ts`：

```ts
describe('registerAll', () => {
  it('按选中位置数发起对应次数的 POST，每次带正确的 machine', async () => {
    const spy = vi.spyOn(client, 'createProject').mockResolvedValue({ project_id: 'p', name: 'a', path: '/a' })
    await registerAll([
      { machine: '',       gitUrl: 'git@x:/a.git', path: '/Users/me/a' },
      { machine: 'devbox', gitUrl: 'git@x:/a.git', path: '' },
    ])
    expect(spy).toHaveBeenCalledTimes(2)
    expect(spy).toHaveBeenNthCalledWith(1, { origin_url: 'git@x:/a.git', path: '/Users/me/a' }, '')
    // 不填目录 = 不带 path，让该机 clone 到自己的 repo_root（B62 的两种形态）
    expect(spy).toHaveBeenNthCalledWith(2, { origin_url: 'git@x:/a.git' }, 'devbox')
  })

  it('一成一败时逐位置返回结果，不整体抛错', async () => {
    vi.spyOn(client, 'createProject')
      .mockResolvedValueOnce({ project_id: 'p', name: 'a', path: '/a' })
      .mockRejectedValueOnce(new ApiError(500, 'clone 失败：Permission denied (publickey)'))
    const out = await registerAll([
      { machine: '', gitUrl: 'g', path: '/a' },
      { machine: 'devbox', gitUrl: 'g', path: '' },
    ])
    expect(out).toHaveLength(2)
    expect(out[0]).toMatchObject({ machine: '', ok: true })
    expect(out[1]).toMatchObject({ machine: 'devbox', ok: false })
    // agentd 的报错原文必须透传——里面带着解法
    expect(out[1].error).toContain('Permission denied')
  })

  it('全部失败也返回逐条结果而不是抛异常', async () => {
    vi.spyOn(client, 'createProject').mockRejectedValue(new ApiError(400, 'origin_url 不能为空'))
    const out = await registerAll([{ machine: '', gitUrl: '', path: '' }])
    expect(out[0].ok).toBe(false)
  })
})
```

- [ ] **Step 2: 运行确认失败，然后实现 register.ts**

Run: `npm test -- register` → FAIL。

`registerAll` 用 `Promise.allSettled` **逐位置**收集结果，绝不 `Promise.all`——后者会让第一个失败吞掉其余结果，用户就不知道本机到底登记上没有（spec §5.2）。

文件头注释写清：多位置登记是多次独立调用，"整体失败"的笼统提示是被明确禁止的展示方式。

- [ ] **Step 3: 写失败的向导测试**

```tsx
it('第一步至少选一个位置，未选时下一步禁用', () => {
  render(<AddProjectWizard open machines={[localMachine, devbox]} {...cb} />)
  expect(screen.getByRole('button', { name: '下一步' })).toBeDisabled()
  fireEvent.click(screen.getByRole('checkbox', { name: '本机' }))
  expect(screen.getByRole('button', { name: '下一步' })).toBeEnabled()
})

it('远程位置单选——选了 devbox 再选 nas 会替换而不是叠加（ADR-0008：至多一台远程）', () => {
  render(<AddProjectWizard open machines={[localMachine, devbox, nas]} {...cb} />)
  fireEvent.click(screen.getByRole('radio', { name: 'devbox' }))
  fireEvent.click(screen.getByRole('radio', { name: 'nas' }))
  expect(screen.getByRole('radio', { name: 'devbox' })).not.toBeChecked()
  expect(screen.getByRole('radio', { name: 'nas' })).toBeChecked()
})

it('不可达的机器可选，但给出「登记可能失败」的提示', () => {
  render(<AddProjectWizard open machines={[localMachine, nasDown]} {...cb} />)
  const opt = screen.getByRole('radio', { name: /nas/ })
  expect(opt).toBeEnabled()   // 可选：要不要试是用户的决定，不替他挡
  fireEvent.click(opt)
  expect(screen.getByText(/登记可能失败/)).toBeInTheDocument()
})

it('本机位置用粘贴路径输入框，没有 Finder 选择器', () => {
  // 浏览器里没有 Finder；File System Access API 故意不返回真实路径（spec §9）
  renderAtStepTwo(['', 'devbox'])
  expect(screen.queryByRole('button', { name: /选择.*文件夹|浏览/ })).toBeNull()
  expect(screen.getAllByPlaceholderText(/粘贴.*路径/)).toHaveLength(2)
})

it('clone 路径留空时提示由该机器 clone 到它自己的 repo_root，不硬编码 ~/.handoff/<name>', () => {
  renderAtStepTwo(['devbox'])
  expect(screen.getByText(/clone 到它自己的 repo_root/)).toBeInTheDocument()
  // 原型标的 ~/.handoff/<project-name> 与 B62 实际的 repo_root/<name> 不一致，
  // 显示一个可能是错的默认路径比不显示更糟（spec §9）
  expect(screen.queryByText(/~\/\.handoff\//)).toBeNull()
})
it('一成一败时逐位置显示结果，成功的保留、失败的可重试', () => {
  expect(screen.getByText(/本机/)).toBeInTheDocument()
  expect(screen.getByText(/Permission denied/)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /重试/ })).toBeInTheDocument()
})
```

- [ ] **Step 4: 运行确认失败，然后实现向导**

Run: `npm test -- AddProjectWizard` → FAIL。

两步，照 `implementation-project-location-selection.png` / `-local-finder.png` / `-remote-path.png`：
1. **选位置**：本机 checkbox + 远程机器**单选**（至多一台远程），至少选一个。远程候选来自 `useMachines`；不可达者可选，旁边给提示。
2. **配来源**：为每个选中位置填 Git 地址与可选目录。

提交后展示逐位置结果面板；有成功的就调 `useProjectTree().refresh()` 立即重拉树。

- [ ] **Step 5: 接上树里的入口与注销**

- 左栏底部「+ 添加项目」打开向导；
- 项目/位置节点的注销走 `deleteProject(name, machine)`，二次确认复用 `ConfirmDialog`；
- **确认文案必须说明这只解除登记、不删除磁盘上的代码**（spec §5.3）；
- 注销成功后 `refresh()`。

- [ ] **Step 6: 运行全量测试**

Run: `npm test && npm run typecheck && npm run lint`
Expected: 全部 PASS。

- [ ] **Step 7: 提交**

```bash
git add web/src/app/projects web/src/app/tree/ProjectTree.tsx
git commit -m "feat(web): 项目登记两步向导与注销，逐位置报告部分成功"
```

---

## Task 10: 逐屏对照与收口自检

**Files:**
- Modify: `web/README.md`（记已知缺口）
- 视情况修补前九个任务的产物

**Interfaces:**
- Consumes: 前九个任务的全部产物
- Produces: 通过验收的 W3b

- [ ] **Step 1: 起服务并把视口固定到基准尺寸**

```bash
npm run dev
```

浏览器窗口调到 **1440×1024**（DevTools 的 Device Toolbar 里设自定义尺寸，DPR 1），与 `prototypes/desktop-console/design-qa.md` 同基准。

- [ ] **Step 2: 逐屏对照**

四屏各截一张，与基准并排比对，逐项记录差异：

| 屏 | 基准截图 |
|---|---|
| 左栏项目树 | `prototypes/desktop-console/implementation-1440x1024-v6.png` |
| 看板与项目多选 | `prototypes/desktop-console/implementation-board-project-multiselect.png` |
| 开发机页 | `prototypes/desktop-console/implementation-machines.png` |
| 项目向导 | `implementation-project-location-selection.png`、`-local-finder.png`、`-remote-path.png` |

**已知且允许的偏离**只有 spec §9 那五条（无 Finder、clone 路径不硬编码、无操作系统格、未实现功能不渲染、左栏点击=筛看板）。其余差异一律修，或写进 README 的已知缺口并说明原因。

- [ ] **Step 3: 真机验收**

需要本机 agentd 在跑、且配置里有 devbox。

1. 树里本机与 devbox 两个节点都在，各自的任务归位；
2. 停掉 devbox 的 agentd（或断网），刷新 → devbox 节点与机器卡片**都标已断开且带原因原文**，**看板不空**；
3. 恢复后节点自动回到已连接。

如实记录每一步的实际观察。**任一步没做或没能验证，在报告里明说，不要写"应该没问题"。**

- [ ] **Step 4: instrumenting-code 自检清单**

逐项确认，任一项未过就回去补：

- [ ] 每个新建文件有文件头注释（职责 + 边界）
- [ ] 每个导出函数/组件有用途注释
- [ ] 非显然分支有「为什么」注释——尤其 `filter.ts` 里 `machine: ''` vs `null` 的判断、`counts.ts` 里计数为何从任务流算
- [ ] 没有任何 `catch` 后静默：每条失败路径都有用户可见的降级展示
- [ ] `console.log` / `console.error` 没有被当作错误处理手段
- [ ] 三条流的失败互不影响（可用 DevTools 把 `/api/machines` 打成 500 验证看板仍在）
- [ ] agentd 的中文报错原文全程透传，没有被吞成「加载失败」

- [ ] **Step 5: 全量检查**

```bash
npm test && npm run typecheck && npm run lint && npm run build
```

四条全绿。`build` 必须跑——`tsc -b` 会抓出 vitest 放过的类型问题。

- [ ] **Step 6: 更新 README 的已知缺口**

在 `web/README.md` 追加本轮结论：spec §9 的五条偏离、未实现功能清单（执行者开关、审批器配置、重启 agent、终端、Env、设置页、配对开发机）、以及 W5 的 history fallback 缺口仍在。

- [ ] **Step 7: 提交**

```bash
git add web/README.md web/src
git commit -m "chore(web): W3b 逐屏对照与收口自检，README 记已知缺口"
```

---

## Self-Review 记录

**Spec 覆盖**：spec §1 形态与路由 → Task 4；§2 左栏树 → Task 6（§2.3 的状态形状 → Task 5）；§3 看板改造 → Task 7；§4 开发机页 → Task 8；§5 登记注销 → Task 9；§6 数据节奏 → Task 2；§7 令牌迁移 → Task 3；§8 诚实展示 → 分散在 Task 6/8/9 的测试里，Task 10 兜底自检；§9 偏离 → Task 10 Step 2 显式核对；§10 可观测性 → Task 6 Step 7 + Task 10 Step 4；§11 测试与验收 → Task 10；§12 交付物 → 全部。

**已知缺口，需审核者在派发前解决**：
1. `internal/proto/` 与 `web/src/api/testdata/*.json` 必须先对齐到本计划的「契约附录」。Task 1 Step 1 是这道闸——fixture 不对就停。
2. `POST /api/projects?machine=` 与 `DELETE /api/projects/{name}?machine=` 这条跨机路由已回填进 W3a spec §5.1.1，但 **W3a 的实现必须覆盖它**。W3b 单独实现无法验证这条——W3a 没做的话，Task 10 的真机验收里远程登记会 404。

**关于组件测试的写法**：Task 2/5/9 的纯逻辑测试给了完整可运行的代码；Task 6/7/8/9 的组件测试给的是**断言契约**（测什么、断言什么值、为什么），选择器写到 role + 可访问名一层为止。再往下写就得凭空指定尚不存在的组件的 DOM 结构，那是编造而不是规范——形态由 Task 10 的截图对照兜底，行为由这些断言兜底。实现时按这些断言补齐即可，**不要削减断言条数**。

**类型一致性**：`BoardFilter` 在 Task 5 定义、Task 6/7 消费，字段名一致；`PollState<T>` 在 Task 2 定义、Task 6/7/8 消费；`ProjectNode` / `Machine` 在 Task 1 定义，后续全部引用同名。
