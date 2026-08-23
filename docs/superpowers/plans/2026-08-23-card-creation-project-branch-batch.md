# B179 实现计划：建卡入口——项目显式可选、分支接现成端点、标题支持批量

> spec：`docs/superpowers/specs/2026-08-23-card-creation-project-branch-batch.md`（已批准，L2，
> 单子系统 web 前端，**后端零改动**）。本计划由 plan 节点执行者 2026-08-23 写于分支
> `cards/B179-charter-2`（起点 `615532e2d`），全部改动只落 `web/`。
>
> L2 无扇出：不产生子卡，跨卡审计（对照冻结物 / 跨卡签名比对 / 故事归属）不适用；
> spec 覆盖与签名一致性在本文件 §7 自审三查内收口。

## 0. 判据先在基线跑（2026-08-23 实测，HEAD = 615532e2d）

| # | 承重事实 | 出处 | 复核方式 |
|---|---|---|---|
| 1 | `GET /api/projects` 返回 `ProjectLocation[]`，空列表序列化成 `[]` 不是 `null` | 路由 `internal/agentd/server.go:471`，处理器 `handleProjectList` `internal/agentd/server.go:1251-1268`（`locs == nil` 时填空切片再写回） | 读源码确认 |
| 2 | TS 侧类型 `ProjectLocation { project_id; name; path; origin_url; created_at; status? }` | `web/src/api/types.ts:116-123`；Go fixture 已被 `web/src/api/contract.test.ts:31,188` 双侧钉住 | 读源码确认 |
| 3 | client.ts 只有 createProject/deleteProject/patchProject，**没有列项目的函数** | `web/src/api/client.ts:565-589` | `grep fetchProjects web/src internal` 零命中 |
| 4 | 分支端点已存在且已封装：`fetchProjectBranches(name, machine?) → ProjectBranchesResp` | `web/src/api/client.ts:591-598`；响应类型 `{ branches: ProjectBranch[]; default: string; worktree_root: string }` 在 `web/src/api/types.ts:198-203` | 读源码确认 |
| 5 | 项目未登记时分支端点返回 **404** + `{"error":"项目 X 未登记"}` | `internal/agentd/projectadmin.go:859-864`（`store.ErrNotFound` 分支）；`ApiError.status` 可判别（`web/src/api/client.ts:67-84`） | 读源码确认 |
| 6 | 「default 并进选项」先例与坑的原文注释 | `web/src/app/tree/NewWorktreeDialog.tsx:42-52`（`baseOptions`） | 抄形态即抄这段 |
| 7 | 静默兜底与卡值候选的现状行 | 兜底 `web/src/app/cards/CardsPage.tsx:144`；候选 `CardsPage.tsx:90` | 读源码确认 |
| 8 | localStorage 安全读写的仓库先例（隐私模式 try/catch、写失败 console.warn 不抛） | `web/src/app/tree/treePrefs.ts:66-92` | 沿用该形态 |
| 9 | **基线绿**：触及的两个测试文件在基线上全过 | `cd web && npm ci && npx vitest run src/app/cards/NewCardDialog.test.tsx src/app/cards/CardsPage.test.tsx` → `Test Files 2 passed (2) · Tests 6 passed (6)`（本次实跑输出） | ✅ 已跑 |
| 10 | 测试底座：vitest + jsdom（`vite.config.ts` 的 `test.environment`）、setup `src/test/setup.ts`、脚本 `npm test`=`vitest run`、`npm run typecheck`=`tsc -b`、`npm run lint`=`eslint .`；tsc 的 include 是整个 `src`（**测试文件参与 typecheck**，`noUnusedLocals` 生效——代码块里的 import 必须个个都被用上） | `web/vite.config.ts:41-44`、`web/tsconfig.app.json`、`web/package.json` | 读配置确认 |

**可观测性映射（纪律块步骤 3 的子系统适配声明）**：web 子系统没有组件级结构化
logger，本卡的可观测面按仓库既有惯例落三处——①每条失败分支都在界面上留下人话
原因（项目登记表读取失败一行、分支降级说明一行、批量失败行的原因原文）；②成功路径
不静默（「将建 N 张卡」实时计数、结果面板列已建卡号）；③localStorage 写失败
`console.warn` 一条（沿 treePrefs 先例）。禁新增裸 `console.log`。

## 1. Interfaces（全卡 Consumes / Produces，逐字对齐）

**Consumes（全部已存在，零改动）**

```ts
// web/src/api/client.ts:591-598 —— name 是登记名；未登记 404
fetchProjectBranches(name: string, machine?: string): Promise<ProjectBranchesResp>
// web/src/api/client.ts:67-84 —— 404 判别用 err.status
class ApiError extends Error { readonly status: number; readonly body: unknown }
// web/src/api/ledger.ts:232-234 —— title/project 必填，其余可选
createCard(req: NewCardReq): Promise<CardCreateResp>   // CardCreateResp = { id: string }
// GET /api/projects → ProjectLocation[]（见 §0 表 #1）
```

**Produces（本计划新增/变更的对外签名）**

```ts
// web/src/api/client.ts（Task 1）
export function fetchProjects(): Promise<ProjectLocation[]>

// web/src/app/cards/NewCardDialog.tsx（Task 5 导出直测）
export function parseTitles(raw: string): string[]

// NewCardDialog 对外 props（Task 3 定型；调用方只有 CardsPage 一处）
{
  open: boolean
  // project：顶部筛选当前值，「全部项目」= 空串。只是第一档预选建议，
  // 提交值以下拉当前值为准。约定它必须来自 CardsPage 的筛选器——其候选恒含
  // 卡上历史值 ⊆ 本组件候选并集，预选值因此总能落在某个 option 上。
  project: string
  // cardProjects：现有卡上出现过的 project 值（去重排序与否皆可，组件会再处理）
  cardProjects: string[]
  workflows: string[]
  parent?: string
  onClose: () => void
  onCreated: (id: string) => void   // 全部成功时回调最后一张的 id；有失败不回调
}
```

## 2. Task 1：`fetchProjects()` 客户端函数

文件集：`web/src/api/client.ts`、`web/src/api/client.test.ts`。测试范围：只跑
`npx vitest run src/api/client.test.ts`。

1. **写失败测试**（加进 `client.test.ts`，harness 照抄同文件的 `vi.spyOn(globalThis, 'fetch')`
   先例，如 `建树接口` describe）：

```ts
describe('列项目接口', () => {
  it('fetchProjects GET /api/projects，返回位置数组', async () => {
    const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify([
        { project_id: 'a1b2c3d4e5f60718', name: 'handoff', path: '/home/dev/handoff',
          origin_url: '', created_at: '', status: '有效' },
        { project_id: 'p2', name: 'sq', path: '/d/sq', origin_url: '', created_at: '' },
      ]), { status: 200, headers: { 'Content-Type': 'application/json' } }),
    )
    const locs = await fetchProjects()
    expect(spy.mock.calls[0][0]).toBe('/api/projects')
    expect(locs.map((l) => l.name)).toEqual(['handoff', 'sq'])
    spy.mockRestore()
  })
})
```

   （顶部 import 列表补 `fetchProjects`。）跑红：函数不存在，编译期即报错。

2. **最小实现**（插在 `patchProject` 之后、`fetchProjectBranches` 之前，与邻居同款注释规格）：

```ts
// fetchProjects 列全部项目位置（GET /api/projects）。
//
// 返回 ProjectLocation[]。服务端保证空列表序列化成 [] 而不是 null
// （internal/agentd/server.go handleProjectList），调用方不必再做 null 归一。
export function fetchProjects(): Promise<ProjectLocation[]> {
  return request<ProjectLocation[]>('/api/projects')
}
```

   跑绿。提交：`feat(web): api client 补 fetchProjects——建卡对话框的项目下拉数据源`。

## 3. Task 2：CardsPage 拔掉静默兜底（先立回归网）

文件集：`web/src/app/cards/CardsPage.test.tsx`、`web/src/app/cards/CardsPage.tsx`。
测试范围：只跑 `npx vitest run src/app/cards/CardsPage.test.tsx`。

这条守的正是 B187 那个缺陷（spec §6 第二个缝），是整张卡的回归锚点。

1. **写失败测试**（追加到 `CardsPage.test.tsx`）：

```tsx
// 子组件换成探针桩，把收到的 props 摆到 DOM 上——本文件只验「接线」，对话框
// 自身行为由 NewCardDialog.test.tsx 负责。props 收宽成 Record：这里不关心
// 其余字段，只透出 project。
vi.mock('./NewCardDialog', () => ({
  NewCardDialog: (props: Record<string, unknown>) => (
    <div data-testid="new-card-dialog-stub" data-project={String(props.project)} />
  ),
}))

describe('建卡入口接线', () => {
  const cardView = {
    id: 'B187', title: '现场铁证', status: '待办', priority: '中', project: 'benchmarking',
    workflow: 'feature', parent: '', base_branch: '', attachments: [], following: '',
    blocked: false, blocked_by: [], merged_count: 0, needs: '', open_decisions: 0,
    children_total: 0, children_done: 0, conflict: false, open_tickets: 0,
  }

  it('「全部项目」下传给对话框的 project 是空串——不再拿 cards[0].project 当兜底（B187 回归网）', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCards).mockResolvedValue({
      cards: [cardView],
      unlinked: { count: 0, tasks: [], unknown_targets: [] },
    })
    render(<CardsPage />)
    const stub = await screen.findByTestId('new-card-dialog-stub')
    expect(stub.dataset.project).toBe('')
  })
})
```

   注意：顶部既有 `vi.mock('../../api/ledger', …)` 保持不动；新用例用 `vi.mocked`
   覆写返回值。跑红：现状传的是 `'benchmarking'`。

2. **最小实现**（`CardsPage.tsx`）：

```tsx
// 删除这一行（144 行）：
const newCardProject = project || cards[0]?.project || 'handoff'
```

   并把 165 行附近的调用改为：

```tsx
      <NewCardDialog
        open={newCardOpen} project={project} workflows={newCardWorkflows}
        onClose={() => setNewCardOpen(false)}
        onCreated={(id) => { setNewCardOpen(false); cardsPoll.refresh(); openDrawer(id) }}
      />
```

   跑绿。提交：`fix(web): 建卡不再静默兜底 cards[0].project，「全部项目」下空值直传对话框`。

## 4. Task 3：NewCardDialog 重写骨架——项目显式可见、可选、选不出来不让建

文件集：`web/src/app/cards/NewCardDialog.tsx`（主体重写）、
`web/src/app/cards/NewCardDialog.test.tsx`（harness 扩展 + 新用例）、
`web/src/app/cards/CardsPage.tsx`（一行接线 `cardProjects`）。
测试范围：只跑 `npx vitest run src/app/cards/NewCardDialog.test.tsx src/app/cards/CardsPage.test.tsx`。

1. **harness 先改**（`NewCardDialog.test.tsx` 顶部）：在既有 `vi.mock('../../api/ledger', …)`
   旁边加 client 的部分 mock（照抄同文件 ledger mock 的 `importOriginal` 形态）；
   vitest 的 import 行补 `beforeEach`：

```tsx
vi.mock('../../api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/client')>()),
  fetchProjects: vi.fn().mockResolvedValue([
    { project_id: 'p1', name: 'handoff', path: '/h', origin_url: '', created_at: '' },
    { project_id: 'p2', name: 'sq', path: '/s', origin_url: '', created_at: '' },
  ]),
}))

beforeEach(() => localStorage.clear())
```

   并把共享 props 补上新字段：

```tsx
const props = { open: true, project: 'handoff', cardProjects: ['handoff'], workflows: ['feature', 'bug'], onClose: () => {} }
```

2. **写失败测试**（新增 describe；五条用例对应 spec §6 表的候选并集、三档预选与按钮门）：

```tsx
describe('项目选择', () => {
  it('候选是 /api/projects 与卡上历史值的并集，去重排序；无筛选值无历史时停在空', async () => {
    render(<NewCardDialog {...props} project="" cardProjects={['benchmarking', 'handoff']} onCreated={() => {}} />)
    const sel = await screen.findByLabelText('项目') as HTMLSelectElement
    await waitFor(() =>
      expect([...sel.options].map((o) => o.value)).toEqual(['', 'benchmarking', 'handoff', 'sq']),
    )
    expect(sel.value).toBe('')                 // 三档皆空：不预选（beforeEach 已清 localStorage）
  })

  it('一档预选：顶部筛选值显示在下拉里，用户扫一眼就知道对不对', async () => {
    render(<NewCardDialog {...props} project="handoff" onCreated={() => {}} />)
    const sel = await screen.findByLabelText('项目') as HTMLSelectElement
    await waitFor(() => expect(sel.value).toBe('handoff'))
  })

  it('二档预选：无筛选值时取上次建卡项目（localStorage），且必须还在候选里', async () => {
    localStorage.setItem('handoff.cards.lastProject', 'handoff')
    render(<NewCardDialog {...props} project="" onCreated={() => {}} />)
    const sel = await screen.findByLabelText('项目') as HTMLSelectElement
    await waitFor(() => expect(sel.value).toBe('handoff'))
  })

  it('三档皆无：不预选、按钮禁用、提示「请先选择项目」、不调 createCard', async () => {
    const ledger = await import('../../api/ledger')
    render(<NewCardDialog {...props} project="" cardProjects={[]} onCreated={() => {}} />)
    expect((await screen.findByLabelText('项目') as HTMLSelectElement).value).toBe('')
    fireEvent.change(screen.getByLabelText('标题'), { target: { value: '某件事' } })
    expect(screen.getByRole('button', { name: '建卡' })).toBeDisabled()
    expect(screen.getByText('请先选择项目')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '建卡' }))
    expect(vi.mocked(ledger.createCard)).not.toHaveBeenCalled()
  })

  it('选了项目后按钮解禁，提交值以对话框下拉为准（不是调用方传入的预选建议）', async () => {
    const ledger = await import('../../api/ledger')
    render(<NewCardDialog {...props} project="" cardProjects={['benchmarking']} onCreated={() => {}} />)
    fireEvent.change(await screen.findByLabelText('项目'), { target: { value: 'sq' } })
    fireEvent.change(screen.getByLabelText('标题'), { target: { value: '第一张 sq 卡' } })
    expect(screen.getByRole('button', { name: '建卡' })).toBeEnabled()
    fireEvent.click(screen.getByRole('button', { name: '建卡' }))
    await waitFor(() => expect(vi.mocked(ledger.createCard)).toHaveBeenCalledWith(
      expect.objectContaining({ title: '第一张 sq 卡', project: 'sq' }),
    ))
  })
})
```

   跑红。
   **注**：占位符纪律例外声明——以上测试复用本文件既有 harness（`vi.mock` + `fireEvent`
   形态），未整段照抄处已把每条断言写全，可直接判定 pass/fail。

3. **最小实现**：`NewCardDialog.tsx` 按下述整体替换（这是本卡的骨架版：分支输入仍是
   纯手输原样保留，Task 4/5 在此之上做定点替换）。完整代码：

```tsx
// NewCardDialog —— 建卡对话框（单张是批量的 n=1 特例）。
//
// 职责：
//   - 收集建卡字段：项目 / 标题（多行=批量）/ 工作流 / 优先级 / 父卡 / 基线分支
//   - 项目下拉自己渲染：候选 = GET /api/projects 登记名 ∪ 调用方传入的卡上
//     历史值（cardProjects）；预选三档全部可见——筛选值 > 上次使用
//     (localStorage) > 不预选。静默选错项目的成因是不可见，不是没得选
//   - 选定项目后取分支列表做「下拉+手输」（Task 4 接 datalist）
//   - 标题多行批量提交（Task 5）
//
// 边界：
//   - 不管建完之后干什么——打开抽屉、刷新列表都由调用方决定（onCreated）
//   - 项目与基线分支**建卡后不可改**：表单上写明，而不是让人建完才发现改不了
import { useEffect, useState } from 'react'
import { createCard } from '../../api/ledger'
import { fetchProjects } from '../../api/client'
import { errorMessage } from '../lib/format'

// 只存一个项目名字符串；读不到或值不在候选里就当没有，不做迁移、不做过期。
const LAST_PROJECT_KEY = 'handoff.cards.lastProject'

function loadLastProject(): string {
  try {
    return localStorage.getItem(LAST_PROJECT_KEY) ?? ''
  } catch {
    return ''   // 隐私模式下 localStorage 可能直接抛（同 treePrefs 先例）
  }
}

function saveLastProject(name: string): void {
  try {
    localStorage.setItem(LAST_PROJECT_KEY, name)
  } catch (err) {
    console.warn('[NewCardDialog] 上次建卡项目写入失败，本次只在内存生效', err)
  }
}

export function NewCardDialog({
  open, project, cardProjects, workflows, parent, onClose, onCreated,
}: {
  open: boolean
  // 顶部筛选当前值：「全部项目」= 空串。只是第一档预选建议，提交值以下拉为准；
  // 约定它来自 CardsPage 的筛选器（候选恒含卡上历史值 ⊆ 本组件并集），
  // 因此预选值总能落在某个 option 上、显示得出来。
  project: string
  // 现有卡上出现过的 project 值。卡的 project 是自由字符串，只认登记表会让
  // 历史卡所属的项目从下拉里消失，所以并集而不是取代。
  cardProjects: string[]
  workflows: string[]
  parent?: string
  onClose: () => void
  onCreated: (id: string) => void
}) {
  const [title, setTitle] = useState('')
  const [workflow, setWorkflow] = useState(workflows[0] ?? 'feature')
  const [priority, setPriority] = useState('中')
  const [baseBranch, setBaseBranch] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  // picked 是用户手动动过的选择；null = 还没动过，让位给三档预选派生
  const [picked, setPicked] = useState<string | null>(null)
  const [registered, setRegistered] = useState<string[]>([])
  const [projectsError, setProjectsError] = useState('')

  useEffect(() => {
    if (!open) return
    let alive = true
    fetchProjects()
      .then((locs) => { if (alive) setRegistered(locs.map((loc) => loc.name)) })
      .catch((err) => {
        if (!alive) return
        // 登记表读不到只降级候选来源（卡上历史值仍在），不弹错堵门
        setRegistered([])
        setProjectsError(errorMessage(err))
      })
    return () => { alive = false }   // 挡住「弹层已关但请求才回来」的 setState
  }, [open])

  useEffect(() => {
    if (!open) {
      // 关闭即复位：下次打开重新按三档预选，不带上一轮的残值
      setPicked(null); setTitle(''); setBaseBranch(''); setError('')
    }
  }, [open])

  const candidates = [...new Set([...registered, ...cardProjects])].sort()

  // 预选三档（spec §3.1）：筛选值 > 上次使用 > 空。派生而非 effect 写 state——
  // 候选异步到齐的时序不会把中间态钉死进 state；用户一旦动过下拉即让位。
  let projectValue = picked ?? ''
  if (picked === null && project !== '') projectValue = project
  if (picked === null && project === '') {
    const last = loadLastProject()
    if (last !== '' && candidates.includes(last)) projectValue = last
  }

  if (!open) return null

  const submit = async () => {
    const trimmed = title.trim()
    if (!trimmed || projectValue === '') return
    setBusy(true)
    setError('')
    try {
      const result = await createCard({
        title: trimmed, project: projectValue, workflow, priority,
        ...(parent ? { parent } : {}),
        ...(baseBranch.trim() ? { base_branch: baseBranch.trim() } : {}),
      })
      setTitle('')
      setBaseBranch('')
      saveLastProject(projectValue)
      onCreated(result.id)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-4">
      <div className="w-full max-w-md rounded-lg border bg-background p-4 shadow-lg">
        <h2 className="text-base font-semibold">新建工作项</h2>
        <label className="mt-3 block text-xs text-muted-foreground" htmlFor="new-card-project">项目</label>
        <select
          id="new-card-project" className="mt-1 w-full rounded border px-2 py-1.5 text-sm"
          value={projectValue} onChange={(e) => setPicked(e.target.value)}
        >
          <option value="">请选择项目</option>
          {candidates.map((name) => <option key={name} value={name}>{name}</option>)}
        </select>
        {projectValue === '' && <p className="mt-1 text-xs text-amber-700">请先选择项目</p>}
        {projectsError !== '' && (
          <p className="mt-1 text-xs text-muted-foreground">登记位置读取失败，候选只剩现有卡上的项目：{projectsError}</p>
        )}
        <p className="mt-1 text-xs text-muted-foreground">建卡后不可改</p>
        <label className="mt-3 block text-xs text-muted-foreground" htmlFor="new-card-title">标题</label>
        <input
          id="new-card-title" className="mt-1 w-full rounded border px-2 py-1.5 text-sm"
          value={title} onChange={(e) => setTitle(e.target.value)} autoFocus
        />
        <div className="mt-3 grid grid-cols-2 gap-3">
          <div>
            <label className="block text-xs text-muted-foreground" htmlFor="new-card-workflow">工作流</label>
            <select
              id="new-card-workflow" className="mt-1 w-full rounded border px-2 py-1.5 text-sm"
              value={workflow} onChange={(e) => setWorkflow(e.target.value)}
            >
              {workflows.map((name) => <option key={name} value={name}>{name}</option>)}
            </select>
          </div>
          <div>
            <label className="block text-xs text-muted-foreground" htmlFor="new-card-priority">优先级</label>
            <select
              id="new-card-priority" className="mt-1 w-full rounded border px-2 py-1.5 text-sm"
              value={priority} onChange={(e) => setPriority(e.target.value)}
            >
              {['高', '中', '低'].map((level) => <option key={level} value={level}>{level}</option>)}
            </select>
          </div>
        </div>
        <label className="mt-3 block text-xs text-muted-foreground" htmlFor="new-card-base">基线分支</label>
        <input
          id="new-card-base" className="mt-1 w-full rounded border px-2 py-1.5 font-mono text-sm"
          placeholder={parent ? '留空 = 继承父卡' : '留空 = 项目主线'}
          value={baseBranch} onChange={(e) => setBaseBranch(e.target.value)}
        />
        <p className="mt-1 text-xs text-muted-foreground">
          这张卡的合并目标。<b>建卡后不可改</b>——已派出去的任务会按它工作。
        </p>
        {error !== '' && <p className="mt-3 rounded border border-amber-500/40 bg-amber-500/10 p-2 text-xs">{error}</p>}
        <div className="mt-4 flex justify-end gap-2">
          <button type="button" className="rounded border px-3 py-1.5 text-sm" onClick={onClose}>取消</button>
          <button
            type="button" className="rounded bg-primary px-3 py-1.5 text-sm text-primary-foreground disabled:opacity-50"
            disabled={busy || title.trim() === '' || projectValue === ''}
            onClick={() => void submit()}
          >建卡</button>
        </div>
      </div>
    </div>
  )
}
```

   同步在 `CardsPage.tsx` 给 `<NewCardDialog …>` 补一行 `cardProjects={projectOptions}`
   （90 行那个 memo 就是去重排序后的卡上历史值，直接复用，不改）。跑绿。提交：
   `feat(web): 建卡对话框自渲染项目下拉——三档预选全可见，未选不让建`。

## 5. Task 4：基线分支接现成端点，拿不到就诚实降级

文件集：`web/src/app/cards/NewCardDialog.tsx`、`web/src/app/cards/NewCardDialog.test.tsx`。
测试范围：同 Task 3 的两个文件。

1. **harness**：client mock 补分支函数，两个项目回不同的 default（证明切项目真的重取重填）：

```tsx
fetchProjectBranches: vi.fn().mockImplementation((name: string) =>
  name === 'sq'
    ? Promise.resolve({ branches: [{ name: 'develop', worktree: '' }], default: 'develop', worktree_root: '/w' })
    : Promise.resolve({ branches: [{ name: 'main', worktree: '' }], default: 'origin/main', worktree_root: '/w' })),
```

2. **写失败测试**：

```tsx
describe('基线分支', () => {
  it('选定项目即取分支：default 是远端跟踪名也并进选项并被填入，切项目重取并换值', async () => {
    const api = await import('../../api/client')
    render(<NewCardDialog {...props} onCreated={() => {}} />)
    const base = await screen.findByLabelText('基线分支') as HTMLInputElement
    await waitFor(() => {
      expect(base.value).toBe('origin/main')                       // 服务端推导的默认基线被显式填入
      expect(vi.mocked(api.fetchProjectBranches)).toHaveBeenCalledWith('handoff')
    })
    const options = document.querySelectorAll('#new-card-base-options option')
    expect([...options].some((o) => (o as HTMLOptionElement).value === 'origin/main')).toBe(true)
    fireEvent.change(screen.getByLabelText('项目'), { target: { value: 'sq' } })
    await waitFor(() => {
      expect(base.value).toBe('develop')                           // 重取成功，旧值不得残留
      expect(vi.mocked(api.fetchProjectBranches)).toHaveBeenLastCalledWith('sq')
    })
  })

  it('项目未登记（404）降级纯手输并说明原因，不弹错；手输名照常随卡提交', async () => {
    const api = await import('../../api/client')
    const ledger = await import('../../api/ledger')
    vi.mocked(api.fetchProjectBranches).mockImplementation((name: string) =>
      name === 'ghost'
        ? Promise.reject(new ApiError(404, '项目 ghost 未登记'))
        : Promise.resolve({ branches: [{ name: 'main', worktree: '' }], default: 'main', worktree_root: '/w' }))
    render(<NewCardDialog {...props} onCreated={() => {}} />)
    fireEvent.change(await screen.findByLabelText('项目'), { target: { value: 'ghost' } })
    expect(await screen.findByText(/未登记位置，分支需手输/)).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('基线分支'), { target: { value: 'feat/from-scratch' } })
    fireEvent.change(screen.getByLabelText('标题'), { target: { value: '给未登记项目建卡' } })
    fireEvent.click(screen.getByRole('button', { name: '建卡' }))
    await waitFor(() => expect(vi.mocked(ledger.createCard)).toHaveBeenCalledWith(
      expect.objectContaining({ project: 'ghost', base_branch: 'feat/from-scratch' }),
    ))
  })
})
```

   （`ApiError` 从被 mock 的 client 模块取——importOriginal 展开保留真实类，测试文件顶部
   补 `import { ApiError } from '../../api/client'` 即可。）
   跑红：现状没有分支下拉也没有降级行。

3. **实现**（在 Task 3 骨架上五处定点改动）：

   a. import 区改为：

```tsx
import { ApiError, fetchProjectBranches, fetchProjects } from '../../api/client'
import type { ProjectBranchesResp } from '../../api/types'
import { useRef } from 'react'
```

   b. 组件外（`saveLastProject` 之后）加：

```tsx
// branchOptionsOf 与 NewWorktreeDialog.baseOptions 同款：本地分支表并上服务端
// 推导的 default。default 可能是 origin/main 这种远端跟踪分支名，不在本地分支
// 表里——不并进来，引用它的地方会落空，看起来像「没选基线」而实际有值且合法。
function branchOptionsOf(data: ProjectBranchesResp): string[] {
  const names = data.branches.map((b) => b.name)
  if (data.default !== '' && !names.includes(data.default)) return [data.default, ...names]
  return names
}
```

   c. 组件内状态区补：

```tsx
  const branchSeq = useRef(0)
  const [branchOptions, setBranchOptions] = useState<string[]>([])
  const [branchDefault, setBranchDefault] = useState('')
  const [branchHint, setBranchHint] = useState('')
```

   关闭复位 effect 的复位清单扩成：

```tsx
      setPicked(null); setTitle(''); setBaseBranch(''); setError('')
      setBranchOptions([]); setBranchDefault(''); setBranchHint('')
      branchSeq.current++
```

   d. 新增分支加载 effect（放在关闭复位 effect 之后）：

```tsx
  // 分支列表跟随有效项目重取：预选（含打开时的第一档）也要取，不只手动切换才取。
  // seq 防竞态：连切两个项目时，慢到的旧响应不得覆盖新项目的结果。
  // 成功时把服务端推导的默认基线显式填入输入框——它可能是不在本地分支表里的
  // 远端跟踪名，看不见就等于没选（spec §6 判据「出现在选项中且被预选」）。
  // 切项目在此清空已填分支：旧项目的分支名在新项目下无意义。
  useEffect(() => {
    if (!open || projectValue === '') return
    setBaseBranch('')
    setBranchOptions([]); setBranchDefault(''); setBranchHint('')
    const seq = ++branchSeq.current
    fetchProjectBranches(projectValue)
      .then((resp) => {
        if (seq !== branchSeq.current) return
        setBranchOptions(branchOptionsOf(resp))
        setBranchDefault(resp.default)
        setBaseBranch(resp.default)
      })
      .catch((err) => {
        if (seq !== branchSeq.current) return
        setBranchOptions([]); setBranchDefault('')
        // 未登记（404）是合法场景：给未登记项目建卡照常能完成，退回手输并说明；
        // 其余失败同样退回手输但透出原文——缩略成「加载失败」会把唯一线索弄丢
        setBranchHint(
          err instanceof ApiError && err.status === 404
            ? `项目 ${projectValue} 未登记位置，分支需手输`
            : errorMessage(err),
        )
      })
  }, [open, projectValue])
```

   e. 基线分支那块 JSX 替换为：

```tsx
        <label className="mt-3 block text-xs text-muted-foreground" htmlFor="new-card-base">基线分支</label>
        <input
          id="new-card-base" className="mt-1 w-full rounded border px-2 py-1.5 font-mono text-sm"
          list="new-card-base-options"
          placeholder={parent
            ? '留空 = 继承父卡'
            : branchDefault !== '' ? `留空 = ${branchDefault}` : '留空 = 项目主线'}
          value={baseBranch} onChange={(e) => setBaseBranch(e.target.value)}
        />
        <datalist id="new-card-base-options">
          {branchOptions.map((name) => <option key={name} value={name} />)}
        </datalist>
        {branchHint !== '' && <p className="mt-1 text-xs text-muted-foreground">{branchHint}</p>}
        <p className="mt-1 text-xs text-muted-foreground">
          这张卡的合并目标。<b>建卡后不可改</b>——已派出去的任务会按它工作。
        </p>
```

   跑绿。提交：`feat(web): 建卡基线分支接 branches 端点——datalist 可手输，404 诚实降级`。

## 6. Task 5：标题框变多行，字段整批共用，逐条提交逐条报结果

文件集：`web/src/app/cards/NewCardDialog.tsx`、`web/src/app/cards/NewCardDialog.test.tsx`。
测试范围：同 Task 3 的两个文件。

1. **写失败测试**：

```tsx
describe('标题批量', () => {
  it('parseTitles：列表前缀、空行、trim；负数样标题不被误伤', () => {
    expect(parseTitles('- 一\n* 二\n1. 三\n2) 四\n3、五\n\n  六  \n-40ms')).toEqual(
      ['一', '二', '三', '四', '五', '六', '-40ms'],
    )
    expect(parseTitles('   ')).toEqual([])
    expect(parseTitles('')).toEqual([])
  })

  it('N 行提交按输入顺序串行调 createCard N 次，标题各异其余字段相同；成功后回调最后一张', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.createCard)
      .mockResolvedValueOnce({ id: 'B201' })
      .mockResolvedValueOnce({ id: 'B202' })
      .mockResolvedValueOnce({ id: 'B203' })
    const onCreated = vi.fn()
    render(<NewCardDialog {...props} project="" onCreated={onCreated} />)
    fireEvent.change(await screen.findByLabelText('项目'), { target: { value: 'handoff' } })
    fireEvent.change(screen.getByLabelText('标题'), { target: { value: '- 一\n二\n3. 三' } })
    expect(screen.getByText(/将建 3 张卡/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '建卡' }))
    await waitFor(() => expect(ledger.createCard).toHaveBeenCalledTimes(3))
    const calls = vi.mocked(ledger.createCard).mock.calls.map((c) => c[0])
    expect(calls.map((r) => r.title)).toEqual(['一', '二', '三'])       // 顺序 = 用户写下顺序
    for (const req of calls) {
      expect(req).toMatchObject({ project: 'handoff', workflow: 'feature', priority: '中' })
    }
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith('B203')) // 最后一张
  })

  it('部分失败不回滚：成功的列卡号、失败的列行内容与原因，留在原地可改行重试', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.createCard)
      .mockResolvedValueOnce({ id: 'B204' })
      .mockRejectedValueOnce(new Error('title 与 workflow 都是必填'))
      .mockResolvedValueOnce({ id: 'B205' })
    const onCreated = vi.fn()
    render(<NewCardDialog {...props} project="" onCreated={onCreated} />)
    fireEvent.change(await screen.findByLabelText('项目'), { target: { value: 'handoff' } })
    fireEvent.change(screen.getByLabelText('标题'), { target: { value: '甲\n乙\n丙' } })
    fireEvent.click(screen.getByRole('button', { name: '建卡' }))
    expect(await screen.findByText(/已建/)).toBeInTheDocument()
    expect(screen.getByText(/B204/)).toBeInTheDocument()
    expect(screen.getByText(/乙.*title 与 workflow 都是必填/)).toBeInTheDocument()
    expect(onCreated).not.toHaveBeenCalled()          // 有失败就不交还调用方收口
  })
})
```

   跑红（textarea/计数/结果面板都不存在）。**注**：同 Task 3——测试形态照抄本文件既有
   harness，断言逐条列全。

2. **实现**（Task 4 版本上六处定点改动）：

   a. `parseTitles` 导出函数，放在 `LAST_PROJECT_KEY` 声明之前：

```tsx
// parseTitles 把多行标题文本解析成建卡清单：一行一张。
//
// 容错规则（spec §3.3）：逐行 trim、跳过空行、去掉行首的 `-` `*` `•` 或
// `1.` `1)` `1、` 式列表前缀——从聊天记录或文档粘一份清单进来就能直接用。
// 数字前缀允许零空格（「1.标题」也认）；`-`/`*` 后面必须跟空白才算前缀，
// 否则负数样标题（-40ms）会被剥得缺胳膊少腿。
export function parseTitles(raw: string): string[] {
  return raw
    .split('\n')
    .map((line) => line.replace(/^\s*(?:[-*•]\s+|\d+[.)、]\s*)/, '').trim())
    .filter((line) => line !== '')
}
```

   b. 组件内加解析与结果状态（放在 `candidates` 派生之前）：

```tsx
  const [result, setResult] = useState<{ succeeded: { title: string; id: string }[]; failed: { title: string; reason: string }[] } | null>(null)
  const titles = parseTitles(title)
```

   关闭复位 effect 再补一项 `setResult(null)`。

   c. 标题输入替换为 textarea 并带实时计数（替换原 `<input id="new-card-title">`）：

```tsx
        <label className="mt-3 block text-xs text-muted-foreground" htmlFor="new-card-title">标题</label>
        <textarea
          id="new-card-title" rows={3} className="mt-1 w-full rounded border px-2 py-1.5 text-sm"
          placeholder={'一行一张卡；粘贴多行时 - / * / 1. 前缀和空行会被忽略'}
          value={title} onChange={(e) => setTitle(e.target.value)} autoFocus
        />
        <p className="mt-1 text-xs text-muted-foreground">
          {titles.length > 0 ? `将建 ${titles.length} 张卡${titles.length > 1 ? '，共用下方字段' : ''}` : '一行一张卡'}
        </p>
```

   d. submit 整体替换为批量版；同时删掉 `error` state（useState 行）、关闭复位里的
      `setError('')`、旧版错误 JSX 段 `{error !== '' && …}` 三处残留：

```tsx
  const submit = async () => {
    if (titles.length === 0 || projectValue === '') return
    setBusy(true)
    setResult(null)
    const succeeded: { title: string; id: string }[] = []
    const failed: { title: string; reason: string }[] = []
    // 串行不并发：并发会让卡号顺序与用户写下顺序对不上，而 B 号顺序是人读
    // 账本时的隐含线索（spec §5）。逐条提交逐条记账，已成功的不回滚。
    for (const one of titles) {
      try {
        const created = await createCard({
          title: one,
          project: projectValue,
          workflow,
          priority,
          ...(parent ? { parent } : {}),
          ...(baseBranch.trim() ? { base_branch: baseBranch.trim() } : {}),
        })
        succeeded.push({ title: one, id: created.id })
      } catch (err) {
        failed.push({ title: one, reason: errorMessage(err) })
      }
    }
    setBusy(false)
    if (succeeded.length > 0) saveLastProject(projectValue)
    if (failed.length > 0) {
      // 有失败就留在原地展示结果：成功列卡号、失败列原因；用户改掉失败那几行
      // 直接再点一次，不用从头重来（spec 故事 7）
      setResult({ succeeded, failed })
      return
    }
    setTitle('')
    setBaseBranch('')
    onCreated(succeeded[succeeded.length - 1].id)
  }
```

   e. 结果面板 JSX（放在原 error 段的位置）：

```tsx
        {result !== null && (
          <div className="mt-3 space-y-1 rounded border p-2 text-xs">
            {result.succeeded.map((row, i) => (
              <p key={`${row.id}-${i}`}>已建 <span className="font-mono">{row.id}</span> · {row.title}</p>
            ))}
            {result.failed.map((row, i) => (
              <p key={`${row.title}-${i}`} className="text-destructive">「{row.title}」未建成：{row.reason}</p>
            ))}
          </div>
        )}
```

   f. 提交按钮 disabled 表达式同步改为 `disabled={busy || titles.length === 0 || projectValue === ''}`。
      既有第四条用例（「project 与 workflow 都是必填」原文透出）无需改动——失败原因现在
      出现在失败行的文本里，正则仍命中。

   跑绿。提交：`feat(web): 标题多行批量建卡——串行逐条提交，成功列卡号失败列原因`。

## 7. 收尾门与四项检查

### 收尾门（implement 节点最后一步）

```bash
cd web && npm run typecheck && npm run lint && npx vitest run
```

全绿后由 implement 节点按其纪律做变异复验并把证据写进任务报文：

| 变异 | 预期 |
|---|---|
| 把 CardsPage 兜底表达式改回 `cards[0]?.project` | Task 2 那条回归网转红 |
| 把按钮 disabled 里的 `projectValue === ''` 删掉 | 「三档皆无」用例转红 |

### 缺陷族对抗审查（结论进验收栏）

| 族 | 设问 | 处置 |
|---|---|---|
| 静默默认值 | 还有没有看不见的兜底？ | 三档预选全部可见于下拉；第三档显式空+禁用+文案。Task 2 回归网钉死调用侧 |
| 竞态 | 连切项目/关弹层后的迟到响应？ | 分支加载 seq 计数器丢弃过期响应；projects 加载 alive 标志挡 setState-after-close |
| 双击/重复提交 | busy 期间按钮？ | `disabled={busy || …}` 且 submit 入口再判 titles/projectValue |
| 部分失败数据丢失 | 已成功的会不会被吞？ | 结果面板列出全部 succeeded/failed，state 不清空输入，可直接改行重试 |
| localStorage 投毒 | 手改/隐私模式？ | 读写全 try/catch；读出的值必须 ∈ 候选才用（treePrefs 同款哲学） |
| 空态 | 登记表空/读取失败、分支列表空、标题全空行 | 分别落到：候选只剩历史值+说明行、branchHint 行、「将建 0 张卡」禁用 |
| 时序依赖渲染 | 预选会不会被异步到货覆盖用户手选？ | picked 非 null 即让位；派生而非 effect 写死 |

### 序列化边界设问

本卡**不新增任何跨边界字段**：fetchProjects 消费的 `ProjectLocation` 已有 Go fixture ↔
TS contract.test.ts 双侧镜像（§0 表 #2）；批量提交每条请求体走既有 `NewCardReq`（fixture
`web/src/api/testdata/NewCardReq.json`）。前端内部的手工投影只有一处——`registered`
数组 ∪ `cardProjects` 的并集排序，它被 Task 3 第一条用例整表断言（含去重），不需要
另立边界回归。

### 上下文预算检查

每个 task 的文件集有界且已列出：T1=2 文件；T2=2 文件；T3=3 文件；T4=2 文件；T5=2 文件。
全部落在 web/src 一个子系统内，无竖切需求。

### 类型标注（边界型判据）

本卡非边界型子系统；行为验收 = §4-§6 各任务的显式用例清单 + §7 变异复验两条，
均为真机可执行命令（vitest），无「目测即可」项。

## 8. 自审三查（出稿者声明）

1. **spec 覆盖**：§6 测试决定九条判据 + CardsPage 回归网一条 → T5(解析/N次/部分失败)、
   T3(禁用/三档/并集)、T4(切项目/404/default 预选)、T2(CardsPage)；故事 1-7 全部指到
   上述用例；Out of Scope 三条「永不做」未出现在任何 task。
2. **占位符扫描**：全文无 TBD/「适当处理」/「同 Task N」。两处声明过的 harness 复用
   （client.test.ts 的 spyOn 先例、NewCardDialog.test.tsx 的 vi.mock 先例）符合纪律例外
   条款且断言逐条列全。Task 3 骨架是完整文件清单，Task 4/5 是对它的定点替换块，
   每处都给了替换前锚点与替换后全文。
3. **跨 task 签名一致性**：Produces 三个签名与各 task 实现代码逐字一致；Consumes 四条
   与 §0 出处逐字核对过（`fetchProjectBranches(name, machine?)` 的 machine 省略 = 本机，
   测试断言用单参形式与之对应，见 client.ts:596）。

**派发前自审**：本计划所有验收步骤均为本地 vitest/tsc/eslint，无一步需要驱动 handoff
派发系统自身，不存在与执行者纪律冲突的验收步骤。
