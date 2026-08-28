# B282：前端调度控制台实现计划

## 0. 目标、边界与执行顺序

本计划把已走查定稿的 `prototypes/b279-automation-proto/` 落入真实 `web/`，并接上 kai/K6 已冻结的后端 wire。形态以原型源码为唯一权威：

- 设置页的 `?section=automation`、载体/小队卡与弹窗照 `prototypes/b279-automation-proto/pages/settings.html:227-510`。
- `/cards` 工具条排队开关、横带、队列 chip、节点胶囊、抽屉协调者三态、attach 确认框、开卡即绑照 `prototypes/b279-automation-proto/pages/board.html:334-384,422-655,673-684`。
- flows 的一张「节点编排」表照 `prototypes/b279-automation-proto/pages/flows.html:112-126`。
- 原型里的 mock 请求、静态 session 命令和独立旧路由不能复制到产品代码；服务端返回的 `CoordinatorAttachInfo.command` 是唯一命令来源。

本卡只在当前分支工作，不切分支、不改 git 配置、不 push、不调用 handoff CLI、不启动 executor。实现者按 Task 0→5 顺序执行；Task 6 只做最终集成验收。

### 0.1 基线事实与已复核判据

当前分支实测是 `cards/B282-charter`，HEAD `eea12d202aea16500726ff4aa44f46f152fec908`，与 `refs/remotes/origin/acc/b156.2-156.3` 同提交，工作区在本计划节点开始时干净。要并入的对象已解析为：

```text
kai = 17bf788543265876caff9f5de71f651ed1d7bb7e
K5  = d66da8b8342d73474e1d18b5d565ce3f945917ea
K6  = ccfe0f09f5d3c0c0cd54d4ac194e67dcd599a57b
```

计划节点先按平台规定尝试过代码图：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . context frontend` 的原始错误是：

```text
Error: 领域 "frontend" 不在最优树词表中；context 已按最优树词表取声明，最优树领域候选: d_cli, d_collab, d_execution, d_execution_adapters, d_execution_contract, d_execution_host, d_gateway, d_keystone, d_ledger, d_maintenance, d_orchestration, d_policy, d_protocol, d_scheduling, d_sessions, d_transport, d_transport_channel, d_transport_tunnel, d_web, d_web_admin, d_web_cards, d_web_command, d_web_contract, d_web_shell, d_web_workbench, d_workspace
```

随后改用 best 词表查 `d_web`、`d_web_cards`、`d_scheduling`、`d_web_workbench`、`d_web_contract`；`d_web` 返回 `truncated=false`，但提示 6 个未扫描入口、缺失 `codegraph/domains/d_web_contract.json`，且 `actual` 报告 API 容器实在 `d_web_contract` 的 misplaced。未命中图符号时才可 grep；这笔覆盖债记录在 `docs/superpowers/ledgers/2026-08-28-b279-spec-ledger.md`，不能把图未覆盖解释成“没有调用者”。

动手前已经在当前基线实跑：

```text
go test ./internal/proto ./internal/agentd
ok  github.com/Xsxdot/handoff/internal/proto (cached)
ok  github.com/Xsxdot/handoff/internal/agentd 174.475s

cd web && npm ci
added 290 packages, and audited 291 packages in 2s
found 0 vulnerabilities

cd web && npm test -- --run
Test Files 110 passed (110)
Tests 1135 passed (1135)

cd web && npm run typecheck
exit 0

cd web && npm run build
1970 modules transformed
exit 0

cd web && npm run lint
20 problems (0 errors, 20 warnings)
exit 0
```

Web 测试只出现既有 jsdom canvas `getContext()` 警告；build 只有既有大 chunk 警告；lint 的 20 条全是既有 warning。未安装依赖时四个 web 命令曾分别报 `sh: 1: vitest not found`、`sh: 1: tsc not found`、`sh: 1: eslint not found`，依赖安装后判据以这里的结果为准。

### 0.2 接缝和文件 ownership

| 接缝 | 声明缝入口 | 本卡锁定行为 | 主要文件 |
| --- | --- | --- | --- |
| S1 | `web/src/api/scheduling.ts` 的导出 fetch 函数 | URL、方法、JSON 投影、CAS/空值/零值 | `web/src/api/scheduling.ts`, `scheduling.fetch.test.ts`, `testdata/*.json` |
| S2 | `SettingsPage` 的 `section=automation` 分支 | 深链、载体/小队列表、弹窗保存与 409 | `SettingsPage.tsx`, `SchedulingPage.tsx` |
| S3 | `CardsPage` 的 toolbar queue toggle | 轮询、横带、卡回跳、queue chip | `CardsPage.tsx`, `QueuePanel.tsx`, `CardItem.tsx` |
| S4 | `CardItem`/`CardDrawer` 调用 `nodeLabelFor` | 多对一派生显示，映射变化后显示集变化 | `columns.ts`, `CardItem.tsx`, `CardDrawer.tsx` |
| S5 | `CardDrawer` 渲染 `CoordinatorPanel` | Bound/AttachActive/Attach 三态、launch、attach/release、终端 | `CoordinatorPanel.tsx`, `CardDrawer.tsx`, `Shell.tsx` |
| S6 | `FlowsPage` 编辑表格 | 固定工作流节点、映射、小队角色过滤、`Override.Squad` | `FlowsPage.tsx` |
| S7 | Go/TS fixture contract tests | kai/K6 金样本形状完整、`attach:null` 与缺失字段可区分 | `internal/proto/*`, `web/src/api/contract.test.ts` |

S1→S2/S3/S5 是真实序列化链路；S6 的 `putFlow` 是另一处手写 `NodeDef` 投影；S5 的 `CoordinatorAttachInfo.command` 还要穿过 `TabContent.initCommand` → `TerminalTab` 的 `init_command` 投影。每一条都必须有穿过边界的断言，不能只测两端纯函数。

## 1. 冻结接口与数据形状

以下是任务之间逐字对齐的 `Consumes/Produces`。实现者不得把 `null` 改成缺席、把 `0` 改成字符串，也不得为 flow 级 launch squad 自造字段。

### 1.1 Go 与 TS wire 类型

`internal/proto/scheduling.go`（kai 已有）及 K6 尾部增量的等价 TypeScript 形状如下：

```ts
export interface CarrierView {
  name: string
  machine: string
  cli: string
  home_dir: string
  model?: string
  credential: string
  max_concurrency?: number
  healthy: boolean
  version: number
}

export interface SquadView {
  name: string
  role: string
  members: string[]
  max_concurrency?: number
  version: number
}

export interface SquadsResp {
  carriers: CarrierView[]
  squads: SquadView[]
}

export interface CarrierInput {
  name?: string
  machine: string
  cli: string
  home_dir: string
  model?: string
  credential: string
  max_concurrency?: number
}

export interface SquadInput {
  name?: string
  role: string
  members: string[]
  max_concurrency?: number
}

export interface SquadPutResp {
  name: string
  version: number
}

export interface QueueEntry {
  kind: string
  id: string
  card: string
  node?: string
  squad: string
  target?: string
  executor?: string
  model?: string
  priority?: string
  ready: boolean
  actor: string
  seq: number
  position: number
}

export interface QueueResp {
  queue: QueueEntry[]
}

export interface CoordinatorLaunchResp {
  woke: boolean
  session_id?: string
  rebuilt: boolean
  escalated: boolean
  output?: string
}

export interface CoordinatorAttachInfo {
  machine: string
  dir: string
  command: string
}

export interface CoordinatorStatus {
  bound: boolean
  attach_active: boolean
  attach: CoordinatorAttachInfo | null
}

export interface CoordinatorAttachReleaseResp {
  ok: boolean
}
```

### 1.2 API 导出签名

`web/src/api/scheduling.ts` 产出下列精确函数；`putJSON`/`postJSON` 继续复用 `web/src/api/client.ts`：

```ts
export type CoordinatorLaunchSource = 'manual' | 'card_create'

export const getSquads: () => Promise<SquadsResp>
export const getQueue: () => Promise<QueueResp>
export const putCarrier: (name: string, expect: number, input: CarrierInput) => Promise<SquadPutResp>
export const putSquad: (name: string, expect: number, input: SquadInput) => Promise<SquadPutResp>
export const launchCoordinator: (
  cardId: string,
  source?: CoordinatorLaunchSource,
) => Promise<CoordinatorLaunchResp>
export const getCoordinatorStatus: (cardId: string) => Promise<CoordinatorStatus>
export const attachCoordinator: (cardId: string, workdir: string) => Promise<CoordinatorAttachInfo>
export const releaseCoordinator: (cardId: string) => Promise<CoordinatorAttachReleaseResp>
```

序列化决定：`launchCoordinator(cardId)` 保持 K6 现有的空对象 `{}`，让服务端默认 `manual`；只有 `launchCoordinator(cardId, 'card_create')` 发送 `{ "source": "card_create" }`。这样保留既有手工入口的请求形状，又让开卡即绑在审计上区别于手工拉起。其他函数的完整实现形状固定为：

```ts
export const getSquads = () => request<SquadsResp>('/api/squads')
export const getQueue = () => request<QueueResp>('/api/queue')
export const putCarrier = (name: string, expect: number, input: CarrierInput) =>
  putJSON<SquadPutResp>(`/api/squads/carriers/${encodeURIComponent(name)}?expect=${expect}`, input)
export const putSquad = (name: string, expect: number, input: SquadInput) =>
  putJSON<SquadPutResp>(`/api/squads/squads/${encodeURIComponent(name)}?expect=${expect}`, input)
export const launchCoordinator = (cardId: string, source?: CoordinatorLaunchSource) =>
  postJSON<CoordinatorLaunchResp>(
    `/api/cards/${encodeURIComponent(cardId)}/coordinator/launch`,
    source === undefined ? {} : { source },
  )
export const getCoordinatorStatus = (cardId: string) =>
  request<CoordinatorStatus>(`/api/cards/${encodeURIComponent(cardId)}/coordinator`)
export const attachCoordinator = (cardId: string, workdir: string) =>
  postJSON<CoordinatorAttachInfo>(`/api/cards/${encodeURIComponent(cardId)}/attach`, { active: true, workdir })
export const releaseCoordinator = (cardId: string) =>
  postJSON<CoordinatorAttachReleaseResp>(`/api/cards/${encodeURIComponent(cardId)}/attach`, { active: false })
```

### 1.3 跨任务组件接口

```ts
// web/src/app/cards/QueuePanel.tsx
export interface QueuePanelProps {
  entries: readonly QueueEntry[]
  open: boolean
  loading: boolean
  disconnected: boolean
  sessionExpired: boolean
  errorText: string
  onToggle: () => void
  onOpenCard: (cardId: string) => void
}
export function QueuePanel(props: QueuePanelProps): JSX.Element
export function queuePositionByCard(entries: readonly QueueEntry[]): ReadonlyMap<string, number>

// web/src/app/cards/CoordinatorPanel.tsx
export interface CoordinatorPanelProps {
  cardId: string
  onOpenTerminal: (info: CoordinatorAttachInfo) => void
}
export function CoordinatorPanel(props: CoordinatorPanelProps): JSX.Element

// web/src/app/cards/CardsPage.tsx
export interface CardsPageProps {
  onOpenCoordinatorTerminal?: (info: CoordinatorAttachInfo) => void
}
export function CardsPage(props: CardsPageProps): JSX.Element

// web/src/app/cards/CardDrawer.tsx，原有 props 保留，新增一项
export interface CardDrawerProps {
  id: string
  onClose: () => void
  onOpenCard: (id: string) => void
  workflowStates?: string[]
  boardLayout?: BoardLayout
  initialSection?: 'merge'
  nodes?: NodeDef[]
  tasks?: Task[]
  onJumpToTask?: (taskId: string) => void
  onOpenCoordinatorTerminal?: (info: CoordinatorAttachInfo) => void
}

// web/src/app/workbench/useWorkbench.ts
export interface WorkbenchApi {
  openTerminalWithCommand: (command: string, b?: BaseDir, group?: number) => void
}

// web/src/app/flows/FlowsPage.tsx，保留现有无参入口
export function FlowsPage(): JSX.Element
```

### 1.4 不新增的 wire

`NewCardReq` 仍是 `title/project/workflow/priority/parent/base_branch`；`web/src/api/ledger.ts:153-160` 与 `internal/proto/ledger.go:180-189` 不加 `coordinate`。开卡即绑是：`createCard(req)` 成功拿到 `CardCreateResp.id` 后，逐卡调用 `launchCoordinator(id, 'card_create')`。该顺序与 kai `cmd/card.go` 的 `coordinateAfterCreate` 一致；拉起失败只显示失败卡和原因，不能删除/回滚已建出的卡。

flows 没有 flow 级 launch-squad wire。节点小队写到现有 `NodeDef.override.squad`，而原型额外的「拉起通道」行只从 `/api/squads` 展示 coordinator roster：若 0 个显示“未登记协调者队”，若 1 个显示该队，若多于 1 个显示“协调者小队不唯一”并列出候选名。它不调用 `putFlow`，否则会把不存在的字段伪造进版本数据；服务端 `resolveCoordinatorSquad`（kai `internal/agentd/coordapi.go:57-84`）才是唯一选择规则。

## 2. Task 0：合并后端基线，保留 wire、舍弃 K6 旧 UI

### 文件范围

- 合并对象：`17bf788543265876caff9f5de71f651ed1d7bb7e`、`d66da8b8342d73474e1d18b5d565ce3f945917ea`、`ccfe0f09f5d3c0c0cd54d4ac194e67dcd599a57b`。
- 必须保留/合并：`internal/proto/scheduling.go`、`internal/proto/contract_fixture_test.go`、`internal/agentd/schedapi.go`、`internal/agentd/cardstep.go`、`internal/agentd/scheddrain.go`、`internal/agentd/wakeconsumer.go`、`internal/agentd/coordapi.go`、相关 `internal/keystone` 文件、`web/src/api/scheduling.ts` 及 scheduling 金样本。
- K6 旧 UI 不成为本卡实现依据：`web/src/app/settings/SchedulingPage.tsx`、K6 的 `web/src/app/cards/QueuePanel.tsx`、`web/src/app/cards/CoordinatorPanel.tsx` 及其旧 UI 测试在后续对应 task 中以原型形态完整替换；不能保留独立调度路由或旧式内联表格。
- K6 修改过的既有 B275 `web/src/app/cards/*`、`web/src/app/settings/SettingsPage.tsx`、`web/src/app/flows/FlowsPage.tsx` 冲突全部以当前 acc 侧为基础手工重放，不把 K6 旧 UI 改动覆盖掉 B275。

### Interfaces

- Consumes：当前 acc 后端与 web 基线；三个 commit SHA；Git merge index。
- Produces：当前分支上可编译的 kai+K5+K6 后端、API 类型/金样本原材料；不产生最终 B282 UI。

### 步骤与基线判据

1. 先在未改动基线复跑 `go test ./internal/proto ./internal/agentd` 与 `cd web && npm test -- --run`；预期分别是上面记录的 Go 两行 `ok`、Web `110/110` 文件与 `1135/1135` 测试。若不是，保存原始输出并停止该 task，不用本卡改动解释基线差异。
2. 执行 `git merge --no-ff 17bf788543265876caff9f5de71f651ed1d7bb7e -m "merge kai B156.3 automation baseline"`，立即执行 `go test ./internal/proto ./internal/agentd`，确认 kai 的 proto、scheduling API、coordapi、cardstep 与 CLI 先进入。
3. 执行 `git merge --no-ff d66da8b8342d73474e1d18b5d565ce3f945917ea -m "merge K5 scheduling wake consumer"`。若 `internal/agentd/coordapi.go` 冲突，先保留 K5 的 wake/drain/coordapi 变更，再以 K5 合入后的文件为基底重放 K6 attach/status 增量；不能整文件取一边。用 `git diff --cc -- internal/agentd/coordapi.go` 逐段确认 `forwardIfRequested`、`withCoordinator`、launch 路径和 K5 的唤醒/排空分支同时存在。
4. 执行 `git merge --no-ff ccfe0f09f5d3c0c0cd54d4ac194e67dcd599a57b -m "merge K6 coordinator attach contract"`。`internal/proto/scheduling.go` 的 K6 三个类型、`internal/proto/contract_fixture_test.go` 的 attach fixture、K6 attach handler 均保留；K6 的 old web UI 不作为实现。
5. 对 K6 冲突按路径处置：`web/src/api/scheduling.ts` 取 K6 完整 API 文件；新增 `CoordinatorStatus.json`、`CoordinatorAttachReleaseResp.json` 取 K6；已有 `FlowDetail.json`、`RoomsFixture.json` 和 `web/src/api/contract.test.ts` 以 acc/B275 为底，再按 Task 1 手工加入 K6 断言，避免 K6 基于 kai 的旧 contract test 覆盖 B275 金样本。`web/src/app/cards/*`、`web/src/app/settings/*`、`web/src/app/flows/FlowsPage.tsx` 冲突取 acc 形态后按 Task 3–5 改写。
6. 执行 `git status --short`，逐个清空 unmerged path；运行 `gofmt -w` 只作用于发生后端冲突的 Go 文件，禁止格式化或删除无关改动。运行：

```bash
go test ./internal/proto ./internal/agentd
cd web && npm test -- --run web/src/api/contract.test.ts
```

预期：两条 Go `ok`；contract test 退出 0。若 K5/K6 合并后失败，保留真实报错文本在台账，先修合并，不进入前端 task。

### 日志与注释要求

该 task 不新增运行时入口，但合并后的新 Go 文件/导出 DTO 若缺包头职责注释，补充职责与边界注释；不得用 `fmt.Print*` 添加诊断。检查 `coordapi.go` 的现有结构化 logger 分支仍带 card/source/squad/cause 上下文。

## 3. Task 1：API fetch 层和双侧金样本接缝

### 文件范围

- `web/src/api/scheduling.ts`：新增/整理 1.1、1.2 的类型与函数。
- `web/src/api/scheduling.fetch.test.ts`：新增 fetch 边界测试，复用 `web/src/api/client.test.ts` 的 `mockFetchJSON` 形态。
- `web/src/api/contract.test.ts`：保留现有 B275 断言，加入 scheduling 与 K6 attach fixture 解析。
- `web/src/api/testdata/SquadsResp.json`、`QueueResp.json`、`CoordinatorLaunchResp.json`、`CoordinatorStatus.json`、`CoordinatorAttachReleaseResp.json`。
- Go 侧只验证/保留合并进来的 `internal/proto/scheduling.go`、`internal/proto/contract_fixture_test.go`；不创造新的契约字段。

K6/kai 金样本的新增文件内容按已读 commit 原样保留，至少包括下面两份 K6 样本；kai 的三份样本不能以手写 mock 替代：

```json
{
  "bound": true,
  "attach_active": false,
  "attach": {
    "machine": "",
    "dir": "/repo/handoff",
    "command": "opencode --session sess-coord"
  }
}
```

```json
{
  "ok": true
}
```

`SquadsResp.json` 必须仍含 `carriers[0].max_concurrency=2`、`healthy=true`、`version=1` 与 coordinator `squads[0]`；`QueueResp.json` 必须仍含 `kind=ignition_queue`、`ready=false`、`position=1`；`CoordinatorLaunchResp.json` 必须仍含 `woke=true`、`session_id`、`rebuilt=false`、`escalated=false`。这些 exact fixture 值与 kai/K6 commit 中的 JSON 对齐。

### Interfaces

- Consumes：`request<T>`, `putJSON<T>`, `postJSON<T>` from `web/src/api/client.ts`；kai/K6 proto shapes；S1 seam。
- Produces：1.1/1.2 的所有 TypeScript 类型和 API 函数，供 `SchedulingPage`、`QueuePanel`、`CoordinatorPanel`、`NewCardDialog` 调用。

### 步骤与锁缝红绿

1. 在编辑前执行 `cd web && npm test -- --run web/src/api/client.test.ts web/src/api/contract.test.ts`；预期基线 API/contract 测试退出 0。该命令不替代 Task 0 的全量记录，只证明本 task 的 harness 可用。
2. 先在 `scheduling.fetch.test.ts` 写失败断言并运行 `cd web && npm test -- --run web/src/api/scheduling.fetch.test.ts`，锁定这些请求：

```ts
it('reads squads and queue without changing empty arrays', async () => {
  const squadsFetchMock = mockFetchJSON({ carriers: [], squads: [] })
  await expect(getSquads()).resolves.toEqual({ carriers: [], squads: [] })
  expect(squadsFetchMock).toHaveBeenLastCalledWith('/api/squads')

  const queueFetchMock = mockFetchJSON({ queue: [] })
  await expect(getQueue()).resolves.toEqual({ queue: [] })
  expect(queueFetchMock).toHaveBeenLastCalledWith('/api/queue')
})

it('serializes CAS writes and preserves zero/omitted optional fields', async () => {
  const fetchMock = mockFetchJSON({ name: 'carrier-a', version: 4 })
  await putCarrier('carrier a', 3, {
    name: 'carrier a', machine: 'local', cli: 'opencode', home_dir: '/h',
    credential: 'standalone', max_concurrency: 0,
  })
  const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
  expect(url).toBe('/api/squads/carriers/carrier%20a?expect=3')
  expect(init.method).toBe('PUT')
  expect(JSON.parse(String(init.body))).toEqual({
    name: 'carrier a', machine: 'local', cli: 'opencode', home_dir: '/h', credential: 'standalone',
  })

  const squadFetchMock = mockFetchJSON({ name: 'squad-a', version: 1 })
  await putSquad('squad a', 0, { role: 'executor', members: [], max_concurrency: 0 })
  const [, squadInit] = squadFetchMock.mock.calls[0] as [string, RequestInit]
  expect(JSON.parse(String(squadInit.body))).toEqual({ role: 'executor', members: [] })
})

it('keeps manual launch body empty but serializes card-create source', async () => {
  const fetchMock = mockFetchJSON({ woke: true, rebuilt: false, escalated: false })
  await launchCoordinator('B1')
  expect((fetchMock.mock.calls[0][1] as RequestInit).body).toBe('{}')

  mockFetchJSON({ woke: true, rebuilt: false, escalated: false })
  await launchCoordinator('B1', 'card_create')
  expect(JSON.parse(String((fetchMock.mock.calls[0][1] as RequestInit).body))).toEqual({ source: 'card_create' })
})

it('preserves attach null, false, zero-like and service-generated command', async () => {
  const status = {
    bound: true,
    attach_active: false,
    attach: { machine: '', dir: '/repo/handoff', command: 'opencode --session sess-coord' },
  }
  mockFetchJSON(status)
  await expect(getCoordinatorStatus('B1')).resolves.toEqual(status)
  expect(JSON.parse(JSON.stringify({ bound: false, attach_active: false, attach: null }))).toHaveProperty('attach', null)

  const fetchMock = mockFetchJSON({ ok: true })
  await releaseCoordinator('B1')
  expect(JSON.parse(String((fetchMock.mock.calls[0][1] as RequestInit).body))).toEqual({ active: false })
})
```

以上断言要使用当前 `client.test.ts` 的 `jsonResp`/`mockFetchJSON` harness；如果 harness 导出形态不同，照抄 `web/src/api/rooms.fetch.test.ts:60-152` 的 `vi.stubGlobal('fetch', fetchMock)` 方式，仍须逐条保留 URL、method、body、null 与缺席字段断言。红测试的入口必须是 API 导出函数，属于 S1，不得改成直接测 `JSON.stringify` 的内部锁。

3. 最小实现只添加固定接口；`model?:`、`max_concurrency?:`、`node?:` 等 optional 字段保持 JSON 缺席，`ready:false` 与 `attach:null` 必须保留。`CoordinatorAttachInfo.command` 不解析、不拼接、不替换。
4. 运行同一条 scheduling fetch 测试至绿，再运行：

```bash
cd web && npm test -- --run web/src/api/scheduling.fetch.test.ts web/src/api/contract.test.ts
```

5. 在 `contract.test.ts` 从真实 fixture `JSON.parse` 后逐字段断言：空 `carriers/squads/queue` 是数组；queue 有 `ready:false`、`position:1`、`priority`；status 有 `attach:null` 与 attach object 两例；release `ok:true`；command 包含 fixture 的 `sess-coord`。同时保留 Go `TestContractFixtures` 的对应断言，形成 Go JSON→fixture→TS 消费的边界锁。

### 日志与注释

- `scheduling.ts` 文件头注明：它是 `/api/squads`、`/api/queue`、coordinator 生命周期 wire 的唯一 web fetch 面，不负责业务状态或命令拼接。
- 每个导出函数写参数/返回/注意事项注释，特别说明 `launchCoordinator` 的 `source` 省略值和 attach command 服务端权威性。
- API 测试不打印；请求失败由既有 client 错误对象向组件传递，组件 task 负责带上下文记录。

## 4. Task 2：设置页「自动化」分区、载体/小队 CAS 弹窗

### 文件范围

- 新增 `web/src/app/settings/SchedulingPage.tsx`、`web/src/app/settings/SchedulingPage.test.tsx`。
- 修改 `web/src/app/settings/SettingsPage.tsx`、`web/src/app/settings/SettingsPage.test.tsx`。
- 消费 Task 1 的 `web/src/api/scheduling.ts`；不得复制一套 fetch 类型。

### Interfaces

- Consumes：`getSquads(): Promise<SquadsResp>`、`putCarrier(name, expect, input)`、`putSquad(name, expect, input)`；`errorMessage`；`SettingsPage` 的既有 `onClose`/tree/latest props；S2 seam。
- Produces：

```ts
export interface SchedulingPageProps {}
export function SchedulingPage(_props: SchedulingPageProps): JSX.Element
```

内部草稿显式使用：

```ts
type CarrierDraft = Omit<CarrierInput, 'max_concurrency'> & { name: string; maxConcurrencyText: string }
type SquadDraft = Omit<SquadInput, 'max_concurrency'> & { name: string; maxConcurrencyText: string }
type EntityDialog =
  | { kind: 'carrier'; value: CarrierView | null }
  | { kind: 'squad'; value: SquadView | null }
  | null
```

### 步骤与锁缝红绿

1. 编辑前执行 `cd web && npm test -- --run web/src/app/settings/SettingsPage.test.tsx`；预期基线退出 0。随后先补 S2 失败测试，入口必须是 `render(<SettingsPage ... />)`，不是只测 `SchedulingPage` 的 helper：

```tsx
it('opens automation directly from the query string', async () => {
  window.history.pushState({}, '', '/settings?section=automation')
  vi.mocked(getSquads).mockResolvedValue({ carriers: [], squads: [] })
  render(<SettingsPage onClose={vi.fn()} />)
  expect(await screen.findByRole('heading', { name: '自动化' })).toBeVisible()
  expect(screen.getByRole('button', { name: '自动化' })).toHaveAttribute('aria-current', 'true')
})
```

同文件新增 `SchedulingPage.test.tsx`，入口是公开组件并锁以下每条可判定结果：

```tsx
it('renders carrier and squad fields and empty-state guidance', async () => {
  vi.mocked(getSquads).mockResolvedValue({
    carriers: [{ name: 'mbp', machine: 'local', cli: 'opencode', home_dir: '/h', credential: 'standalone', healthy: true, version: 3 }],
    squads: [{ name: 'coord', role: 'coordinator', members: ['mbp'], version: 2 }],
  })
  render(<SchedulingPage />)
  expect(await screen.findByText('mbp')).toBeVisible()
  expect(screen.getByText('/h')).toBeVisible()
  expect(screen.getByText('coord')).toBeVisible()
  expect(screen.getByText('拉起通道（开卡即绑 / 一键拉起）')).toBeVisible()
})

it('creates with expect zero and edits with the row version', async () => {
  vi.mocked(getSquads).mockResolvedValue({ carriers: [], squads: [] })
  vi.mocked(putCarrier).mockResolvedValue({ name: 'mbp', version: 1 })
  render(<SchedulingPage />)
  await userEvent.click(screen.getByRole('button', { name: '登记载体' }))
  await userEvent.type(screen.getByLabelText('载体名'), 'mbp')
  await userEvent.type(screen.getByLabelText('机器'), 'local')
  await userEvent.type(screen.getByLabelText('CLI'), 'opencode')
  await userEvent.type(screen.getByLabelText('HOME 档案'), '/h')
  await userEvent.type(screen.getByLabelText('凭据来源'), 'standalone')
  await userEvent.click(screen.getByRole('button', { name: '保存' }))
  expect(putCarrier).toHaveBeenCalledWith('mbp', 0, expect.objectContaining({ machine: 'local', home_dir: '/h' }))

  vi.mocked(getSquads).mockResolvedValue({ carriers: [], squads: [{ name: 'exec', role: 'executor', members: [], version: 7 }] })
  render(<SchedulingPage />)
  await userEvent.click(await screen.findByRole('button', { name: '编辑' }))
  await userEvent.click(screen.getByRole('button', { name: '保存' }))
  expect(putSquad).toHaveBeenCalledWith('exec', 7, expect.objectContaining({ role: 'executor', members: [] }))
})

it('shows an actionable CAS conflict and retains the modal', async () => {
  vi.mocked(getSquads).mockResolvedValue({ carriers: [{ name: 'mbp', machine: 'local', cli: 'opencode', home_dir: '/h', credential: 'standalone', healthy: true, version: 3 }], squads: [] })
  vi.mocked(putCarrier).mockRejectedValue(new Error('409: 版本冲突，请刷新后重试'))
  render(<SchedulingPage />)
  await userEvent.click(await screen.findByRole('button', { name: '编辑 mbp' }))
  await userEvent.click(screen.getByRole('button', { name: '保存' }))
  expect(await screen.findByText(/版本冲突/)).toBeVisible()
  expect(screen.getByRole('dialog')).toBeVisible()
})
```

如果已有测试的 user-event 初始化方法不同，复用 `NewCardDialog.test.tsx` 的 setup；不得用仅调用 `putCarrier` mock 的 helper 测试替代 UI 接缝。第二个用例须分别验证新建 `expect=0` 与编辑读取行 `version`，并验证并发空值 `0` 在输入转换中变为 undefined，从而不误发 `max_concurrency:0`（服务端以字段缺席表达“不限”）。

3. 最小实现：

```tsx
const [section, setSection] = useState<SectionKey>(() => {
  const value = new URLSearchParams(window.location.search).get('section')
  return value === 'automation' ? 'automation' : value === 'update' ? 'update' : 'machines'
})

const optionalConcurrency = (raw: string): number | undefined => {
  const value = raw.trim()
  if (value === '') return undefined
  const parsed = Number(value)
  return Number.isInteger(parsed) && parsed > 0 ? parsed : undefined
}

async function saveCarrier(draft: CarrierDraft, current: CarrierView | null): Promise<void> {
  await putCarrier(draft.name, current?.version ?? 0, {
    name: draft.name,
    machine: draft.machine,
    cli: draft.cli,
    home_dir: draft.home_dir,
    model: draft.model?.trim() || undefined,
    credential: draft.credential,
    max_concurrency: optionalConcurrency(draft.maxConcurrencyText),
  })
}

async function saveSquad(draft: SquadDraft, current: SquadView | null): Promise<void> {
  await putSquad(draft.name, current?.version ?? 0, {
    name: draft.name,
    role: draft.role,
    members: draft.members,
    max_concurrency: optionalConcurrency(draft.maxConcurrencyText),
  })
}
```

组件必须按原型顺序展示：自动化说明 → 载体卡（HOME、CLI/机器、model、credential、健康点、`在跑/上限 · v版本`）→ 小队卡（role badge、member chips、政策位、绑定对象）→ 弹窗。载体和小队名称编辑时 read-only；新建用 `expect=0`。保存成功关闭弹窗并重新 `getSquads`；失败保留草稿和弹窗，409 显示“版本冲突，请刷新后重试”及服务端错误。小队角色只能发 `executor`/`coordinator`，成员 checkbox 顺序写入 `members`。

4. 将 `SettingsPage` 的 `SectionKey` 加 `automation`，nav 插在 discipline 后面，分支渲染 `<SchedulingPage />`；既有 `update` 深链和其他页面行为保持。
5. 运行：

```bash
cd web && npm test -- --run web/src/app/settings/SchedulingPage.test.tsx web/src/app/settings/SettingsPage.test.tsx
```

### 日志与注释

- `SchedulingPage.tsx` 文件头写职责（编制展示/CAS 编辑）与边界（不探活、不发现机器、不写 flow）。所有导出组件写 props/返回/注意事项。
- `load` 入口记录 `console.info('scheduling.load', { scope: 'settings' })`；GET 前后分别记录 scope 与 carrier/squad 数量；GET/PUT 错误用 `console.error('scheduling.load.error', { scope, cause })` 或 `console.warn('scheduling.save.conflict', { kind, name, expect, cause })`，成功记录新版本。使用结构化 console 参数，禁止 `print`。
- 非显然规则写注释：并发输入空/0为何转 optional、CAS 版本为何取行快照、成员顺序为何不排序。

## 5. Task 3：看板队列横带、queue chip 与派生节点标签

### 文件范围

- 新增/替换 `web/src/app/cards/QueuePanel.tsx`、`QueuePanel.test.tsx`。
- 修改 `web/src/app/cards/CardsPage.tsx`、`CardItem.tsx`、`columns.ts`、`columns.test.ts`、`CardsPage.test.tsx`。
- 消费 `getQueue()`、`usePoll`（`web/src/app/data/usePoll.ts` 已有签名 `usePoll<T>(fetcher:()=>Promise<T>, intervalMs:number, opts?:{enabled?:boolean}): PollState<T>`）。

### Interfaces

- Consumes：`QueueResp`；`CardItem` 的现有 `card/onOpen`；`BoardLayout` 的 `boardColumnFor`；S3/S4 seams。
- Produces：1.3 的 `QueuePanelProps`、`queuePositionByCard`；`nodeLabelFor`：

```ts
export function nodeLabelFor(
  status: string,
  nodes: readonly string[],
  layout: BoardLayout,
): string | undefined
```

### 步骤与锁缝红绿

1. 编辑前执行 `cd web && npm test -- --run web/src/app/cards/CardsPage.test.tsx web/src/app/cards/columns.test.ts`；预期基线退出 0。
2. 先在 `QueuePanel.test.tsx` 写红测试，入口必须是 `<QueuePanel ... />`：

```tsx
it('renders toolbar count, all queue fields, and opens the card', async () => {
  const onOpenCard = vi.fn()
  render(<QueuePanel
    entries={[
      { kind: 'launch_queue', id: 'q1', card: 'B1', node: '进行中', squad: 'exec', priority: '高', ready: false, actor: 'wake', seq: 7, position: 1 },
      { kind: 'ignition_queue', id: 'q2', card: 'B2', squad: 'coord', target: 'local', executor: 'opencode', model: 'm', priority: '中', ready: true, actor: 'card_step', seq: 8, position: 2 },
    ]}
    open={false} loading={false} disconnected={false} sessionExpired={false} errorText=""
    onToggle={vi.fn()} onOpenCard={onOpenCard}
  />)
  expect(screen.getByRole('button', { name: '⧗ 排队中 2' })).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: '⧗ 排队中 2' }))
  expect(screen.getByText('B1')).toBeVisible()
  expect(screen.getByText('拉起')).toBeVisible()
  expect(screen.getByText('进行中 · exec')).toBeVisible()
  expect(screen.getByText('高')).toBeVisible()
  expect(screen.getByText('未就绪')).toBeVisible()
  expect(screen.getByText('wake')).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: '打开 B1' }))
  expect(onOpenCard).toHaveBeenCalledWith('B1')
})

it('shows stale/disconnected and expired errors without hiding last data', () => {
  render(<QueuePanel entries={[{ kind: 'launch_queue', id: 'q1', card: 'B1', squad: 'exec', ready: true, actor: 'x', seq: 1, position: 1 }]}
    open loading={false} disconnected sessionExpired={false} errorText="网络断开"
    onToggle={vi.fn()} onOpenCard={vi.fn()} />)
  expect(screen.getByText('B1')).toBeVisible()
  expect(screen.getByText('网络断开')).toBeVisible()
})

it('derives node labels from a many-to-one mapping, never from a column name', () => {
  const layout = { columns: ['代办', '沟通中', '进行中', '审核中', '结束'], fallback: '代办', state_to_column: {
    待审阅: '审核中', 待合并: '审核中', 进行中: '进行中',
  } }
  expect(nodeLabelFor('待审阅', ['待审阅', '待合并', '进行中'], layout)).toBe('待审阅')
  expect(nodeLabelFor('进行中', ['待审阅', '待合并', '进行中'], layout)).toBeUndefined()
  const oneToOne = { ...layout, state_to_column: { 待审阅: '审核中', 待合并: '结束', 进行中: '进行中' } }
  expect(nodeLabelFor('待审阅', ['待审阅', '待合并', '进行中'], oneToOne)).toBeUndefined()
})
```

3. 最小实现：

```tsx
export function queuePositionByCard(entries: readonly QueueEntry[]): ReadonlyMap<string, number> {
  return new Map(entries.map((entry) => [entry.card, entry.position]))
}

export function nodeLabelFor(status: string, nodes: readonly string[], layout: BoardLayout): string | undefined {
  if (!nodes.includes(status)) return undefined
  const column = boardColumnFor(status, layout)
  const sameColumn = nodes.filter((node) => node !== '' && boardColumnFor(node, layout) === column)
  return sameColumn.length > 1 ? status : undefined
}
```

`QueuePanel` 的按钮文字必须是 `⧗ 排队中 ${entries.length}`；展开后行次序直接按 `QueueEntry.position`/响应顺序，字段依次是位次、卡号、`launch_queue`→“拉起”/其他→“点火”、节点·小队、priority、ready 的“就绪/未就绪”、actor。卡号是按钮回到 `onOpenCard(card)`；网络错误、401/session expired、disconnect 显示出口，但 stale entries 仍保留。`CardsPage` 用 `usePoll(getQueue, 5000)`，把最新 entries 传入面板和 `queuePositionByCard`；按 `card` 将位次传给 `CardItem`。

`CardItem` 新增 optional props：

```ts
queuePosition?: number
nodeTag?: string
```

只在存在时渲染 `⧗ 排队 #${queuePosition}` chip 和深色节点胶囊。`CardDrawer` 标题同样调用 `nodeLabelFor(status, nodes.map(n => n.name), layout)`，不得硬编码“审核中”。nodes 缺失时不猜节点，不显示胶囊；layout 缺失时沿用 `normalizeBoardLayout` 的既有默认值。

4. 运行锁缝测试至绿：

```bash
cd web && npm test -- --run web/src/app/cards/QueuePanel.test.tsx web/src/app/cards/columns.test.ts web/src/app/cards/CardsPage.test.tsx
```

### 日志与注释

- `QueuePanel.tsx` 文件头写它是瞬时调度横带，不是第六列、不改变卡生命周期；导出 props/helper 写参数/返回/边界。
- CardsPage 轮询入口记录 `console.info('queue.poll.start', { intervalMs: 5000 })`，成功记录 `{ count, stale: false }`，错误记录 `{ count: previous.length, stale: true, status/cause }`；401 记录 `console.warn('queue.poll.expired', { status: 401 })`。成功路径不静默。
- `nodeLabelFor` 注释写“同一看板列有多个工作流节点才显形”，并解释使用映射派生而非中文列名，防止未来改列名引入硬编码。

## 6. Task 4：协调者抽屉三态、attach 终端与开卡即绑

### 文件范围

- 新增/替换 `web/src/app/cards/CoordinatorPanel.tsx`、`CoordinatorPanel.test.tsx`。
- 修改 `web/src/app/cards/CardDrawer.tsx`、`CardDrawer.test.tsx`、`CardsPage.tsx`、`CardsPage.test.tsx`。
- 修改 `web/src/app/shell/Shell.tsx`、`Shell.test.tsx`。
- 修改 `web/src/app/workbench/useWorkbench.ts`、`useWorkbench.test.ts`；不改已有 `tabs.ts` 的 `initCommand` 类型。
- 修改 `web/src/app/cards/NewCardDialog.tsx`、`NewCardDialog.test.tsx`。
- 消费 Task 1 API 与 `ConfirmDialog`（`open,title,description,confirmLabel,destructive?,busy?,error?,onConfirm,onCancel`）。

### Interfaces

- Consumes：`getCoordinatorStatus(cardId)`、`launchCoordinator(cardId, 'manual')`、`attachCoordinator(cardId, info.dir)`、`releaseCoordinator(cardId)`；`CoordinatorAttachInfo`；S5 seam。
- Produces：1.3 `CoordinatorPanelProps`；Shell 向 CardsPage 提供 `(info: CoordinatorAttachInfo) => void`；Workbench 新动作：

```ts
openTerminalWithCommand: (command: string, b?: BaseDir, group?: number) => void
```

### 步骤与锁缝红绿

1. 编辑前执行：

```bash
cd web && npm test -- --run web/src/app/cards/CardDrawer.test.tsx web/src/app/cards/NewCardDialog.test.tsx web/src/app/shell/Shell.test.tsx web/src/app/workbench/useWorkbench.test.ts
```

预期基线退出 0。
2. 先写 `CoordinatorPanel.test.tsx` 的红测试；入口必须是 `render(<CoordinatorPanel ... />)`，每个 API 调用通过真实按钮触发：

```tsx
it('maps unbound, bound, and attach-active status to the three prototype states', async () => {
  vi.mocked(getCoordinatorStatus)
    .mockResolvedValueOnce({ bound: false, attach_active: false, attach: null })
    .mockResolvedValueOnce({ bound: true, attach_active: false, attach: { machine: '', dir: '/repo', command: 'opencode --session s' } })
  const { rerender } = render(<CoordinatorPanel cardId="B1" onOpenTerminal={vi.fn()} />)
  expect(await screen.findByText('未绑定')).toBeVisible()
  expect(screen.getByRole('button', { name: '▶ 拉起协调者' })).toBeVisible()
  rerender(<CoordinatorPanel cardId="B2" onOpenTerminal={vi.fn()} />)
  expect(await screen.findByText('已绑定')).toBeVisible()
  expect(screen.getByRole('button', { name: '打开终端' })).toBeVisible()
})

it('confirms service command and workdir, then attaches without rewriting command', async () => {
  const info = { machine: '', dir: '/repo/handoff', command: 'opencode --session sess-coord' }
  vi.mocked(getCoordinatorStatus).mockResolvedValue({ bound: true, attach_active: false, attach: info })
  vi.mocked(attachCoordinator).mockResolvedValue(info)
  const onOpenTerminal = vi.fn()
  render(<CoordinatorPanel cardId="B1" onOpenTerminal={onOpenTerminal} />)
  await userEvent.click(await screen.findByRole('button', { name: '打开终端' }))
  expect(screen.getByText(/attach 与自动唤醒互斥/)).toBeVisible()
  expect(screen.getByText('/repo/handoff')).toBeVisible()
  expect(screen.getByText('opencode --session sess-coord')).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: '确认 attach' }))
  expect(attachCoordinator).toHaveBeenCalledWith('B1', '/repo/handoff')
  expect(onOpenTerminal).toHaveBeenCalledWith(info)
})

it('releases attach and exposes errors instead of pretending success', async () => {
  const info = { machine: '', dir: '/repo', command: 'opencode --session s' }
  vi.mocked(getCoordinatorStatus).mockResolvedValue({ bound: true, attach_active: true, attach: info })
  vi.mocked(releaseCoordinator).mockResolvedValue({ ok: true })
  render(<CoordinatorPanel cardId="B1" onOpenTerminal={vi.fn()} />)
  expect(await screen.findByText('人工接管中')).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: '交回无头' }))
  expect(releaseCoordinator).toHaveBeenCalledWith('B1')
})
```

3. 最小实现按状态渲染：

```tsx
const poll = usePoll(() => getCoordinatorStatus(cardId), 5000)
const status = poll.data
const coordinatorView: 'loading' | 'unbound' | 'bound' | 'attach-active' | 'invalid' =
  status === null && poll.loading
    ? 'loading'
    : status !== null && status.attach_active && status.bound && status.attach !== null
    ? 'attach-active'
    : status !== null && status.bound && !status.attach_active && status.attach !== null
      ? 'bound'
      : status !== null && !status.bound && !status.attach_active && status.attach === null
        ? 'unbound'
        : 'invalid'
```

不得把 `bound` 和 `attach_active` 合并成一个 boolean；`bound=true, attach=null` 显示明确错误/刷新出口而非读取空字段。ConfirmDialog 的 description 完整包含 `目录：${info.dir}`、`命令：${info.command}`、`attach 与自动唤醒互斥`；确认调用 `attachCoordinator(cardId, info.dir)`，用返回的 `CoordinatorAttachInfo` 原样回调 `onOpenTerminal`。release 返回 `{ok:true}` 后刷新，失败保留人工接管态并显示错误。

4. 在 `CardDrawer` 删除旧“节点动作/拉起协调者”按钮及其 `runCardStep` 路径，保留其他 card action；标题和状态旁加入 Task 3 的 `nodeLabelFor`；在原型对应位置渲染：

```tsx
<CoordinatorPanel
  cardId={id}
  onOpenTerminal={onOpenCoordinatorTerminal ?? (() => undefined)}
/>
```

`CardsPage` 将 `onOpenCoordinatorTerminal` 原样传入 drawer。测试中不得把 CoordinatorPanel 逻辑复制进 CardDrawer 测试；至少一支 CardDrawer seam test 断言真实 `<CoordinatorPanel>` 的三态入口可见。

5. 在 Workbench 新增动作，完整实现只是在已有 `mutate`/`openTab` 路径设置 `initCommand`：

```ts
const openTerminalWithCommand = useCallback(
  (command: string, b?: BaseDir, group?: number) => mutate((w) => {
    const seq = nextTerminalSeq(w)
    return openTab(w, { kind: 'terminal', seq, initCommand: command }, group)
  }, b),
  [mutate],
)
```

返回对象加入 `openTerminalWithCommand`。`useWorkbench.test.ts` 从 hook 公开动作调用它，断言目标 base/group 的 tab `content` 是 `{ kind:'terminal', seq:1, initCommand:'opencode --session sess-coord' }`；再让 `TerminalTab` 既有测试/`web/src/app/workbench/tabs` 持久化测试确认 `init_command` 仍由现有 launcherFields 投影，不能另发 attach endpoint。命令字符串禁止 trim、加 shell wrapper 或替换成原型 mock。

6. 在 Shell 增加终端回调。树中按 machine+path 查真实 workspace；查不到时使用明确的 synthetic workspace，避免 attach 目录被静默丢掉：

```ts
function coordinatorBase(tree: ProjectTreeResp | null, info: CoordinatorAttachInfo): BaseDir {
  const match = tree?.projects
    .flatMap((project) => project.locations.flatMap((location) =>
      location.workspaces.map((workspace) => ({ project, location, workspace }))))
    .find(({ location, workspace }) => workspace.path === info.dir && location.machine === info.machine)
  if (match) return workspaceBase(match.project, match.location.machine, match.workspace)
  const label = info.dir.split('/').filter(Boolean).pop() || info.dir
  return {
    key: info.machine === '' ? info.dir : `${info.dir}@${info.machine}`,
    kind: 'workspace',
    path: info.dir,
    label,
    projectName: '',
    machine: info.machine,
  }
}

const openCoordinatorTerminal = useCallback((info: CoordinatorAttachInfo) => {
  console.info('coordinator.terminal.open', { machine: info.machine, dir: info.dir })
  wb.openTerminalWithCommand(info.command, coordinatorBase(treeState.data, info))
}, [treeState.data, wb])
```

把 `<CardsPage onOpenCoordinatorTerminal={openCoordinatorTerminal} />` 接到现有 `/cards` 路由；不改变 `/board` 任务看板。`info.command` 不进入日志，避免把凭据/命令参数泄漏到日志；成功记录 machine/dir 和“opened”，错误记录 cause。

7. 在 `NewCardDialog` 增加状态 `coordinate: boolean` 和原型文案“创建后拉起协调者并绑定（开卡即绑）”及开场评估说明。提交逻辑保留现有 `NewCardReq`：每次 `createCard(req)` 成功后立即：

```ts
const created = await createCard(req)
if (coordinate) {
  try {
    await launchCoordinator(created.id, 'card_create')
    results.push({ id: created.id, coordinator: 'launched' })
  } catch (error) {
    console.warn('card-create.coordinator.error', { card: created.id, cause: error })
    results.push({ id: created.id, coordinator: 'failed', error: errorMessage(error) })
  }
}
```

批量创建时每张成功卡各调用一次；创建失败不调用 launch，launch 失败不回滚卡；对用户展示成功卡与协调者失败卡的逐条结果。新增测试入口仍是 `NewCardDialog`：

```tsx
it('does not add a coordinate field to createCard and launches each successful card after creation', async () => {
  vi.mocked(createCard)
    .mockResolvedValueOnce({ id: 'B1' })
    .mockResolvedValueOnce({ id: 'B2' })
  vi.mocked(launchCoordinator).mockResolvedValue({ woke: true, rebuilt: false, escalated: false })
  render(<NewCardDialog {...props} />)
  await userEvent.click(screen.getByRole('checkbox', { name: /开卡即绑/ }))
  await userEvent.click(screen.getByRole('button', { name: '建卡' }))
  expect(createCard).toHaveBeenCalledWith(expect.not.objectContaining({ coordinate: true }))
  expect(launchCoordinator).toHaveBeenNthCalledWith(1, 'B1', 'card_create')
  expect(launchCoordinator).toHaveBeenNthCalledWith(2, 'B2', 'card_create')
})

it('keeps a created card when coordinator launch fails', async () => {
  vi.mocked(createCard).mockResolvedValue({ id: 'B3' })
  vi.mocked(launchCoordinator).mockRejectedValue(new Error('409: 协调者队列已满'))
  render(<NewCardDialog {...props} />)
  await userEvent.click(screen.getByRole('checkbox', { name: /开卡即绑/ }))
  await userEvent.click(screen.getByRole('button', { name: '建卡' }))
  expect(screen.getByText(/B3/)).toBeVisible()
  expect(screen.getByText(/协调者/)).toBeVisible()
  expect(screen.getByText(/队列已满/)).toBeVisible()
})
```

8. 运行锁缝测试至绿：

```bash
cd web && npm test -- --run web/src/app/cards/CoordinatorPanel.test.tsx web/src/app/cards/CardDrawer.test.tsx web/src/app/cards/NewCardDialog.test.tsx web/src/app/workbench/useWorkbench.test.ts web/src/app/shell/Shell.test.tsx
```

### 日志与注释

- `CoordinatorPanel.tsx` 文件头写三态来源、轮询边界、command 不可改写；导出组件写 cardId/callback 参数与 attach 注意事项。
- coordinator 请求入口/前后都用结构化 console：`coordinator.status.start/done/error`、`coordinator.launch.start/done/error`、`coordinator.attach.start/done/error`、`coordinator.release.start/done/error`，字段包含 card、active、status/cause；command 本体不记日志。
- `CardDrawer` 注释说明旧节点动作不是协调者生命周期入口，必须由 CoordinatorPanel 统一消费 status。
- `useWorkbench` 新动作注释说明它仅包装已有 `initCommand` 通道；Shell synthetic base 注释说明“服务端给出的 dir 不在当前树时仍必须可打开”，不是把目录宣称已登记。

## 7. Task 5：flows 一张节点编排表

### 文件范围

- 修改 `web/src/app/flows/FlowsPage.tsx`、`web/src/app/flows/FlowsPage.test.tsx`。
- 继续消费 `web/src/api/ledger.ts` 的 `fetchFlow`/`putFlow`、`NodeDef.override.squad`、`BoardLayout`；消费 Task 1 的 `getSquads`。
- 不修改 `NodeEditor.tsx` 作为第二套节点编辑器；flows 中原有“加一列/删节点/节点 editor”区块改为表格。

### Interfaces

- Consumes：`FlowDetail.nodes: NodeDef[]`、`FlowDetail.board?: BoardLayout`、`SquadView[]`、`putFlow(name, nodes, board?)`；S6 seam。
- Produces：同一 workflow version 的 `putFlow` body：

```ts
type OrchestrationRow = {
  node: NodeDef
  boardColumn: string
  squad: string
}

function setNodeSquad(node: NodeDef, squad: string): NodeDef
function orchestrationRows(nodes: readonly NodeDef[], board: BoardLayout): OrchestrationRow[]
```

`setNodeSquad` 只改 `override.squad`：空值删除 squad；其他 override（executor/discipline/target/model）逐一保留；结果仍传入既有 `putFlow(name, nodes, board)`。

### 步骤与锁缝红绿

1. 编辑前执行 `cd web && npm test -- --run web/src/app/flows/FlowsPage.test.tsx`；预期基线退出 0。
2. 先写 S6 红测试，入口必须是 `render(<FlowsPage />)` 并模拟真实 fetch/按钮：

```tsx
it('uses fetched workflow nodes as fixed rows and never renders add/remove node controls', async () => {
  vi.mocked(fetchFlows).mockResolvedValue({ flows: [{ name: 'feature', version: 2, def: { states: ['待办', '进行中'], board: undefined } }] })
  vi.mocked(fetchFlow).mockResolvedValue({ name: 'feature', version: 2, states: ['待办', '进行中'], nodes: [
    { name: '待办', override: { executor: 'old' } },
    { name: '进行中' },
  ], board: { columns: ['代办', '沟通中', '进行中', '审核中', '结束'], fallback: '代办', state_to_column: { 待办: '代办', 进行中: '进行中' } } })
  vi.mocked(getSquads).mockResolvedValue({ carriers: [], squads: [{ name: 'exec', role: 'executor', members: [], version: 1 }, { name: 'coord', role: 'coordinator', members: [], version: 1 }] })
  render(<FlowsPage />)
  await userEvent.click(await screen.findByRole('button', { name: '编辑' }))
  expect(await screen.findByText('节点编排')).toBeVisible()
  expect(screen.getByText('待办')).toBeVisible()
  expect(screen.getByText('进行中')).toBeVisible()
  expect(screen.queryByRole('button', { name: '加一列' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /删除/ })).not.toBeInTheDocument()
  expect(screen.getByRole('combobox', { name: '节点 进行中 的派发小队' })).toHaveValue('')
})

it('writes squad to NodeDef.override, preserves other override fields, and filters launch row to coordinators', async () => {
  vi.mocked(fetchFlows).mockResolvedValue({ flows: [{ name: 'feature', version: 2, def: { states: ['待办', '进行中'] } }] })
  vi.mocked(fetchFlow).mockResolvedValue({ name: 'feature', version: 2, states: ['待办', '进行中'], nodes: [
    { name: '待办', override: { executor: 'old' } }, { name: '进行中' },
  ], board: { columns: ['代办', '沟通中', '进行中', '审核中', '结束'], fallback: '代办', state_to_column: { 待办: '代办', 进行中: '进行中' } } })
  vi.mocked(getSquads).mockResolvedValue({ carriers: [], squads: [{ name: 'exec', role: 'executor', members: [], version: 1 }, { name: 'coord', role: 'coordinator', members: [], version: 1 }] })
  render(<FlowsPage />)
  await userEvent.click(await screen.findByRole('button', { name: '编辑' }))
  await userEvent.selectOptions(await screen.findByRole('combobox', { name: '节点 进行中 的派发小队' }), 'exec')
  expect(screen.getByRole('combobox', { name: '拉起通道 的派发小队' })).toHaveValue('coord')
  expect(screen.getByRole('combobox', { name: '拉起通道 的派发小队' })).not.toHaveValue('exec')
  await userEvent.click(screen.getByRole('button', { name: '保存为新版本' }))
  expect(putFlow).toHaveBeenCalledWith('feature', [
    { name: '待办', override: { executor: 'old' } },
    { name: '进行中', override: { squad: 'exec' } },
  ], expect.anything())
})

it('derives the launch row from coordinator roster and never persists a flow launch field', async () => {
  vi.mocked(fetchFlows).mockResolvedValue({ flows: [{ name: 'feature', version: 2, def: { states: ['待办'] } }] })
  vi.mocked(fetchFlow).mockResolvedValue({ name: 'feature', version: 2, states: ['待办'], nodes: [{ name: '待办' }], board: undefined })
  vi.mocked(getSquads).mockResolvedValue({ carriers: [], squads: [
    { name: 'coord-a', role: 'coordinator', members: [], version: 1 },
    { name: 'coord-b', role: 'coordinator', members: [], version: 1 },
  ] })
  render(<FlowsPage />)
  await userEvent.click(await screen.findByRole('button', { name: '编辑' }))
  expect(await screen.findByText(/协调者小队不唯一/)).toBeVisible()
  expect(putFlow).not.toHaveBeenCalledWith(expect.anything(), expect.anything(), expect.objectContaining({ launch_squad: expect.anything() }))
})
```

以上三例每条都从 `FlowsPage` 的真实编辑入口起步；若测试当前按 case 独立 setup，保留各自的完整 fixture，不用直接调用内部 helper 代替 S6。`putFlow` 断言必须检查旧 `override.executor` 不丢，且空 squad 不产生 `squad: ''`。

3. 最小实现的行来源和保存投影：

```tsx
const nodes = detail.nodes // 只读工作流详情；不从 board.columns 造节点，不允许 add/remove
const executorSquads = squads.filter((squad) => squad.role === 'executor')
const coordinatorSquads = squads.filter((squad) => squad.role === 'coordinator')

function setNodeSquad(node: NodeDef, squad: string): NodeDef {
  const override = { ...(node.override ?? {}) }
  if (squad === '') delete override.squad
  else override.squad = squad
  return Object.keys(override).length === 0
    ? { ...node, override: undefined }
    : { ...node, override }
}

async function saveOrchestration(rows: readonly OrchestrationRow[]): Promise<void> {
  const nextNodes = rows.map(({ node, squad }) => setNodeSquad(node, squad))
  setSaveError('')
  try {
    const result = await putFlow(workflow.name, nextNodes, board)
    setVersion(result.version)
    setSavedBoard(board)
    setEditing(false)
  } catch (cause) {
    console.error('flows.orchestration.error', { workflow: workflow.name, phase: 'save', cause })
    setSaveError(errorMessage(cause))
  }
}
```

表格列固定为“节点 / 看板列 / 派发小队 / 说明”。节点 cell 是 plain text；看板列 select 复用 B275 `board.columns` 和 `board.state_to_column`；小队 select 首项 `value=""`、文案“无（不派发）”，其余只放 `role === 'executor'`。说明和 hint 按原型提示设置·自动化、版本化、空队语义。额外“（拉起通道）”行不属于 `nodes`，看板列显示“—（不动状态）”，小队 select 只放 coordinator；只有单 coordinator 时显示当前值，多于一个或零个按上面的提示，不进 `putFlow`。

4. 运行锁缝测试至绿：

```bash
cd web && npm test -- --run web/src/app/flows/FlowsPage.test.tsx
```

### 日志与注释

- 在 `FlowsPage.tsx` 相关区域写注释：节点是 workflow `NodeDef.Name` 的版本化集合，不是 board column，不可在此增删；`override.squad` 只表达执行者小队。
- fetchFlow/getSquads 入口、保存前后、错误分支使用 `console.info('flows.orchestration.load', { workflow })`、`console.info('flows.orchestration.save', { workflow, nodeCount })`、`console.error('flows.orchestration.error', { workflow, phase, cause })`；不记录完整 credential。
- 保存成功显示新版本并刷新 detail；失败保留表格编辑态和错误文案，不静默关闭。

## 8. Task 6：最终集成与真机验收（只在各 task 绿后执行）

本 task 不再新增功能代码；它的全量测试不归任何单个实现 task。先确认 `git diff --name-only` 只含本卡计划范围和台账，再执行：

```bash
gofmt -l internal/proto internal/agentd internal/keystone
go vet ./...
go test ./...

cd web
npm test -- --run
npm run typecheck
npm run build
npm run lint
```

判据：`gofmt -l` 无输出；`go vet ./...`、`go test ./...` 退出 0；Web 测试/typecheck/build/lint 退出 0。lint 允许既有 warning，但新增 error 或新增与本卡文件相关的 warning 必须修复；不要把既有 canvas/chunk warning 伪装成失败。

### 真机行为清单

使用真实 agentd、真实 ledger 与已登记配置逐条走，不以 React mock 代替：

1. `GET /api/squads` 空库返回 `{"carriers":[],"squads":[]}`；UI 打开 `/settings?section=automation` 后显示引导，登记载体和 executor/coordinator 小队，刷新可见 version；用旧 version PUT 必须看到 409 且弹窗保留。
2. 配置两个可入队场景后 `GET /api/queue` 验证 `position` 从 1 开始、`launch_queue` 在 `ignition_queue` 前；`ready:false`、priority、actor 在横带原样显示，卡 chip 位次与行一致；点击卡号打开相同 drawer。
3. `GET /api/cards/{id}/coordinator` 真机验证 `{bound:false,attach_active:false,attach:null}` 与 bound status；`POST .../coordinator/launch` 分别验证省略 source=manual 与 body source=card_create；未登记/多 coordinator/满位时 UI 有错误出口。
4. 真机 attach：`POST /api/cards/{id}/attach` active=true 后 status 为 `attach_active:true`；确认框显示服务端返回的 machine/dir/command，确认后 terminal tab 的首次请求带该 command 的 `init_command`；命令不能来自前端拼接。active=false 返回 `ok:true` 后显示“绑定中/已绑定”，自动唤醒互斥文案可见。
5. 新建工作项不勾选时只发送既有 `NewCardReq`；勾选时每张成功卡恰好调用一次 source=card_create，拉起失败不删除卡并逐条显示失败原因。
6. 让 `待审阅`/`待合并` 映射同一看板列，卡和抽屉标题显示各自节点；把其中一个改到独立列后重新加载，标签消失；不能通过改列名触发硬编码逻辑。
7. flows 编辑工作流，节点行数量/名称与 `FlowDetail.nodes` 相同且不可增删；executor squad 可写 `NodeDef.Override.Squad`，空值恢复存量直绑；拉起通道只显示 coordinator 且不会污染 `putFlow` body。

## 9. 五项自审结论与收口规则

### 9.1 缺陷族对抗审查

- 错 endpoint/方法/编码：S1 对每个 URL、encodeURIComponent、method/body 断言；真机逐端点走。
- 字段缺席与零值混淆：fixture/API 测试断言 optional 缺席、`ready:false`、`attach:null`；并发 0 转 undefined 的 UI 测试覆盖。
- CAS 并发丢写：S2 用创建 `expect=0`、编辑行 version、409 留在弹窗三例锁定。
- 轮询/断网/401：S3 保留 stale 数据，显示断开和 session expired，不把空数组当成功；真机刷新验证。
- 状态组合遗漏：S5 明确覆盖 unbound/bound/attachActive 以及 bound+attach null；release/launch 错误有出口。
- 角色越权：S2 小队 role 只接受 executor/coordinator；S6 节点仅列 executor，拉起通道仅列 coordinator；真机多队歧义不自动选。
- 映射硬编码：S4 只从 `BoardLayout.state_to_column` 派生，多对一→显示、一对一→沉默，测试改映射后再断言。
- 部分成功回滚错误：S5 开卡即绑断言“建卡成功、拉起失败仍保留卡”，批量按卡调用而非整体回滚。
- 终端路径/命令污染：S5 命令从服务端原样穿过 `initCommand`，dir machine 找不到时仍有 synthetic base；日志不泄漏 command。
- 旧 UI 复活/范围漂移：Task 0 和最终 diff 检查确保没有独立 Scheduling 路由、旧 Queue/Coordinator 信息架构，也没有规则引擎、历史队列、自动发现。

### 9.2 序列化边界清单

| 边界 | 手写投影 | 必须有的断言 |
| --- | --- | --- |
| Go proto → JSON fixture → TS | scheduling DTO、K6 attach DTO、`scheduling.ts` 类型 | `contract.test.ts` 逐字段含 null/缺席/false/0 |
| TS API → HTTP | `putCarrier/putSquad/launch/attach/release` body | `scheduling.fetch.test.ts` 检查 URL/method/body |
| Flow UI → `putFlow` | `setNodeSquad` 与 `putFlow` `{nodes,board}` | FlowsPage seam 检查 `override` 保留/删除与无 launch field |
| Queue JSON → row/chip | `QueueEntry.position` map 与 kind 文案 | QueuePanel seam 检查行位次、chip、ready/actor |
| Status JSON → terminal | `CoordinatorAttachInfo.command` → `initCommand` → `init_command` | Panel + useWorkbench + terminal 请求的跨边界断言 |
| Card create → coordinator launch | `CardCreateResp.id` → source body | NewCardDialog + API body 检查不修改 NewCardReq、逐卡调用 |

### 9.3 接缝双向覆盖

测试入口到接缝：Task 1 的 API 导出、Task 2 的 SettingsPage、Task 3 的 QueuePanel/CardsPage、Task 4 的 CoordinatorPanel/CardDrawer/NewCardDialog/Shell/Workbench、Task 5 的 FlowsPage，均直接落在 S1–S6；Go/fixture contract 落在 S7。纯 helper 测试只作为附加，不顶替这些 seam tests。

接缝到测试：S1 `scheduling.fetch.test.ts`+contract、S2 SettingsPage/SchedulingPage、S3 QueuePanel/CardsPage、S4 columns+CardDrawer、S5 CoordinatorPanel/CardDrawer/NewCardDialog/Workbench/Shell、S6 FlowsPage、S7 Go/TS contract，七条均有至少一条真实调用链断言。

### 9.4 spec 故事归属

| spec 用户故事 | 具体 task/入口 |
| --- | --- |
| 1. 设置自动化分区与编制卡 | Task 2：`SettingsPage` → `SchedulingPage` |
| 2. 登记/编辑与 CAS 冲突 | Task 2：carrier/squad dialog → `putCarrier`/`putSquad` |
| 3. queue 横带、chip、回卡 | Task 3：`CardsPage` → `QueuePanel`/`CardItem` |
| 4. 审核中节点标签 | Task 3：`CardItem`/`CardDrawer` → `nodeLabelFor` |
| 5. 抽屉协调者三态 | Task 4：`CardDrawer` → `CoordinatorPanel` |
| 6. attach 确认与 terminal init command | Task 4：`CoordinatorPanel` → `Shell` → `useWorkbench` |
| 7. 新建后拉起并绑定 | Task 4：`NewCardDialog` → `launchCoordinator(id, 'card_create')` |
| 8. flows 节点编排 | Task 5：`FlowsPage` → `putFlow` |

本卡没有拆出的其他子卡 plan，故不存在 A/B 卡之间未审签的 Produces/Consumes；Task 1 的冻结接口表承担本卡内部跨 task 逐字比对。

### 9.5 占位符扫描声明

本计划没有模糊占位语句或改变测试入口的条件退路；每个文件、接口、命令和断言都已落名。测试均指定既有 harness 文件；若实现时 harness 导出不同，只能照 `web/src/api/client.test.ts`、`web/src/api/rooms.fetch.test.ts`、相应现有组件测试的 setup 改写，断言列表不得减少。没有内部锁替代 seam 的条件退路。

### 9.6 计划收口

每确立一个事实、每跑一条命令、每放弃一种合并/实现尝试，都追加到 `docs/superpowers/ledgers/2026-08-28-b279-spec-ledger.md`；台账与本计划同批提交。计划节点不实现代码、不建脚手架。实现完成并通过 Task 6 后，执行：

```bash
git status --short
git diff --check
git add docs/superpowers/plans/b282-plan.md docs/superpowers/ledgers/2026-08-28-b279-spec-ledger.md
git commit -m "docs(plan): B282 scheduling console implementation plan"
```

本计划节点自身的法定产出是本文件和已经追加的台账；其 commit 不 push。实现者后续提交必须继续包含台账，不得把计划文档或 ledger 留在工作区。
