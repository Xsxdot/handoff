# B285 工作台收口实现计划

## 0. 计划边界与基线

- 卡号：B285；标题：B281 后续：关 tab 收分屏、左栏点开新组、拖放示意与顶栏跟焦。
- 有效基线：`origin/cards/B281-charter-9`，工作树已在开工时执行
  `git fetch origin cards/B281-charter-9 && git reset --hard origin/cards/B281-charter-9`，当前
  分支为 `cards/B285-charter-2`。不切分支、不改 git 配置、不 push。
- 已确认输入：`docs/superpowers/specs/b285.md`（已批准，L2，2026-08-28）与
  `prototypes/b285-workbench-polish/index.html` 存在。
- 本卡只改 `d_web`：全局 Workbench 纯模型、其持久化/恢复、Workbench React 接缝、左树与
  Shell 展示。不得改 agentd、PTY 协议、`/api/workbench/state/base` 字段、后端迁移、旧
  payload 猜测迁移、文件抽屉的当前组语义。
- 当前代码图已用
  `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . context d_web_workbench`
  查过：`truncated=false`，但 `fociTruncated.total=10 shown=5`，8 个相关容器在
  `actual.misplaced` 中仍落在现状 `d_web`；`who-calls` 还报告 6 个未扫描入口。因此
  下列源码调用面以实际源码为准，不能把空图结果解释成“没有调用方”。

### 基线判据（已在动手前真实复核）

依赖安装由 `web/package-lock.json` 决定；`web/package.json:6-12` 将 `npm test` 映射为
`vitest run`，`web/package.json:47` 固定声明 `vitest`。校正基线执行过：

```text
npm ci --ignore-scripts
=> added 290 packages, and audited 291 packages in 2s
=> found 0 vulnerabilities
exit 0

npm test -- --run src/app/workbench/tabs.test.ts src/app/workbench/paneDrop.test.ts src/app/workbench/persist.test.ts src/app/workbench/restore.test.ts src/app/workbench/useWorkbench.test.ts src/app/workbench/useWorkbenchSync.test.ts src/app/workbench/WorkbenchPage.test.tsx src/app/tree/ProjectTree.test.tsx src/app/tree/search.test.ts src/app/files/FileTree.test.tsx src/app/shell/Shell.test.tsx src/app/cards/CardsPage.test.tsx
=> Test Files  12 passed (12)
=> Tests  202 passed (202)
exit 0

npm run typecheck
=> npm 启动行后无错误输出
exit 0

npm test -- --run src/app/workbench/tabs.test.ts src/app/workbench/persist.test.ts src/app/workbench/restore.test.ts
=> Test Files  3 passed (3)
=> Tests  30 passed (30)
exit 0

npm test -- --run src/app/workbench/useWorkbench.test.ts src/app/workbench/WorkbenchPage.test.tsx src/app/workbench/TerminalTab.test.tsx
=> Test Files  3 passed (3)
=> Tests  58 passed (58)
exit 0

npm test -- --run src/app/tree/ProjectTree.test.tsx src/app/shell/Shell.test.tsx
=> Test Files  2 passed (2)
=> Tests  96 passed (96)
exit 0
```

第一次在未安装依赖的校正基线运行目标测试得到原始错误 `sh: 1: vitest: not found`，
退出码 127；该失败已经通过上述 `npm ci --ignore-scripts` 后重跑确认，不把它当代码
失败。实现者在改动后仍须按任务范围重跑对应命令，最后再由协调者跑全量测试。
执行顺序固定为 Task 1 → Task 3 → Task 2：Task 3 先去掉 Shell 的 ⌘D 调用，Task 2
再在 WorkbenchPage 不再引用后删除 `splitColumn` API，避免中间阶段类型检查悬空。

### 统一接口约定

现有 `BaseDir`、`TabContent`、`Tab`、`TabColumn`、`TabGroup`、`Workbench`、`PaneTarget`、
`WorkbenchSource`、`OpenedWorkbenchItem` 的定义以 `web/src/app/workbench/tabs.ts:1-68`
为准，不复制一份新类型。拖放 `DropZone` 的 DOM 解析仍使用
`web/src/app/workbench/paneDrop.ts:1-77` 的
`dropZoneAt(offsetX: number, offsetY: number, width: number, height: number,
canAddColumn: boolean, canAddPane: boolean): DropZone` 和四个 MIME 常量。

## Task 1：统一布局生命周期、解码压缩与孤儿恢复

### 目标与有界文件集

只触及以下文件：

- `web/src/app/workbench/tabs.ts`
- `web/src/app/workbench/persist.ts`
- `web/src/app/workbench/restore.ts`
- `web/src/app/workbench/tabs.test.ts`
- `web/src/app/workbench/persist.test.ts`
- `web/src/app/workbench/restore.test.ts`

当前事实：`tabs.ts:234-246` 的 `closeTab` 留下 `null`，`tabs.ts:414-431` 的
`appendRestoredTab` 会寻找任何空格；`persist.ts:100-145` 只验证布局、不压缩布局；
`restore.ts:123-139` 通过该函数领养工作区孤儿。Task 1 只改这些纯函数/编解码路径，
不让 React 再维护第二份布局。

### Interfaces

Consumes：

```ts
// web/src/app/workbench/tabs.ts 已有类型，签名不得改名或改参数顺序。
export interface Workbench {
  groups: TabGroup[]
  activeGroupId: string
}

export interface PaneTarget {
  groupId: string
  column: number
  row: number
  zone: DropZone
}

export type WorkbenchSource =
  | { kind: 'new'; base: BaseDir; content: TabContent }
  | { kind: 'tab'; groupId: string; tabId: string }

// web/src/app/workbench/restore.ts 已有输入/输出契约。
export interface RestoreInput {
  state: WorkbenchStateResp
  sessions: PtySession[]
  vw: number
  vh: number
  inset: number
}

export interface RestoreResult {
  workbench: Workbench
  dock: DockSnapshot | null
  dockOrphans: HomeTab[]
  selected: string
  dropped: string[]
  legacy: string[]
  pruned: number
  adopted: number
}
```

Produces：

```ts
// web/src/app/workbench/tabs.ts
export function normalizeWorkbench(wb: Workbench): Workbench
export function closePane(wb: Workbench, groupId: string, column: number, row: number): Workbench
export function closeTab(wb: Workbench, groupId: string, tabId: string): Workbench
export function appendRestoredTab(wb: Workbench, base: BaseDir, content: TabContent): Workbench

// web/src/app/workbench/persist.ts
export function decodeWorkbench(raw: string): Workbench | null

// web/src/app/workbench/restore.ts（对外签名保持现状）
export function buildRestore(input: RestoreInput): RestoreResult
```

`normalizeWorkbench` 是新的布局唯一压缩入口；`decodeWorkbench` 解码成功后必须调用
它，`buildRestore` 不得绕过 `decodeWorkbench` 自己拼列。`closeTab` 只负责按 tab id
定位，找到后调用与坐标关闭相同的生命周期规则；`closePane` 是 UI 关闭空 pane 的
声明缝。`appendRestoredTab` 保持现有参数与返回类型，不增加 session/API 参数。

### 行为契约与完整实现形状

执行者按下面的纯函数形状实现，字段名、边界值和返回语义不可另造一套：

```ts
// web/src/app/workbench/tabs.ts
export function normalizeWorkbench(wb: Workbench): Workbench {
  const groups = wb.groups.flatMap((group) => {
    const kept = group.columns
      .map((column, index) => ({ column, index }))
      .filter(({ column }) => column.panes.some((pane) => pane !== null))
    if (kept.length === 0) return []
    const focusEntry = kept.find(({ index }) => index >= group.focus[0]) ?? kept[kept.length - 1]
    const focusColumn = kept.indexOf(focusEntry)
    const columns = kept.map(({ column }) => ({
      panes: column.panes.map((tab) => tab ? cloneTab(tab) : null),
    }))
    return [{
      id: group.id,
      name: group.name,
      autoName: group.autoName,
      columns,
      sizes: kept.map(({ index }) => group.sizes[index]),
      focus: [focusColumn, Math.min(group.focus[1], columns[focusColumn].panes.length - 1)] as [number, number],
    }]
  })
  if (groups.length === 0) return cloneWorkbench(EMPTY_WORKBENCH)
  return {
    groups,
    activeGroupId: groups.some((group) => group.id === wb.activeGroupId)
      ? wb.activeGroupId
      : groups[0].id,
  }
}

export function closePane(wb: Workbench, groupId: string, column: number, row: number): Workbench {
  const groupIndexValue = groupIndex(wb, groupId)
  const currentGroup = groupIndexValue < 0 ? undefined : wb.groups[groupIndexValue]
  if (!currentGroup || column < 0 || column >= currentGroup.columns.length ||
      row < 0 || row >= currentGroup.columns[column].panes.length) {
    warnInvalid('workbench.close.invalid_pane', { groupId, column, row, zone: 'center' })
    return wb
  }
  const next = cloneWorkbench(wb)
  const group = next.groups[groupIndexValue]
  const oldFocus = [...group.focus] as [number, number]
  group.columns[column].panes.splice(row, 1)
  if (group.columns[column].panes.length === 0) {
    group.columns.splice(column, 1)
    group.sizes.splice(column, 1)
  }
  if (group.columns.length === 0 || !group.columns.some((item) => item.panes.some((pane) => pane !== null))) {
    if (next.groups.length === 1) {
      next.groups[0] = emptyGroup(group.id, group.name, group.autoName)
      next.activeGroupId = group.id
      return next
    }
    next.groups.splice(groupIndexValue, 1)
    if (next.activeGroupId === groupId) {
      next.activeGroupId = next.groups[Math.min(groupIndexValue, next.groups.length - 1)].id
    }
    return normalizeWorkbench(next)
  }
  const focusColumn = oldFocus[0] > column
    ? oldFocus[0] - 1
    : Math.min(oldFocus[0], group.columns.length - 1)
  group.focus = [focusColumn, Math.min(oldFocus[1], group.columns[focusColumn].panes.length - 1)]
  return normalizeWorkbench(next)
}

export function closeTab(wb: Workbench, groupId: string, tabId: string): Workbench {
  const location = locationOf(wb, tabId, groupId)
  if (location === null) {
    console.warn('workbench.close.invalid_tab', { groupId, tabId })
    return wb
  }
  return closePane(wb, groupId, location.column, location.row)
}

export function appendRestoredTab(wb: Workbench, base: BaseDir, content: TabContent): Workbench {
  const next = normalizeWorkbench(wb)
  const tab: Tab = { id: nextId(next, 't'), base: { ...base }, content: { ...content } as TabContent }
  if (isEmptyWorkbench(next) && next.groups.length === 1) {
    const group = next.groups[0]
    group.columns = [{ panes: [tab] }]
    group.sizes = [1]
    group.focus = [0, 0]
    return next
  }
  const id = nextId(next, 'g')
  next.groups.push({
    id, name: `组 ${next.groups.length + 1}`, autoName: true,
    columns: [{ panes: [tab] }], sizes: [1], focus: [0, 0],
  })
  return next
}
```

`normalizeWorkbench` 必须在 `decodeWorkbench` 的严格字段校验之后调用：非法 JSON、
版本、BaseDir、TabContent、重复 id、越界 focus 仍返回 `null`，只有合法但含空列/空组的
布局才被压缩。序列化版本仍是 `PERSIST_VERSION = 2`，不得借机放宽额外字段或改变
`encodeWorkbench` 对 `draft`、`baseSha`、`incompatible` 的剥离。

关闭规则的具体结果必须是：

1. 两格列关闭一格后列只剩另一格；
2. 单格列关闭后从 group 删除该列，并同步删除权重；
3. 组内最后内容关闭后，非唯一 group 被删除；
4. 唯一 group 被重置为一个可见空 pane；这是唯一允许保留 `[null]` 的结果；
5. 显式 `closeGroup` 的最后组规则仍保留，并复用同一空组构造函数。

### 2–5 分钟步骤

1. **基线判据复核**：在 `web/` 运行本计划第 0 节的三文件命令；预期原始结果为
   已实测原始结果为 `Test Files 3 passed (3)`、`Tests 30 passed (30)`。若结果不同，先把完整输出追加
   到 `docs/superpowers/ledgers/2026-08-28-b285-spec-ledger.md`，再停止猜测。
2. **写锁缝红测**：在既有 `tabs.test.ts` 的 `handoff`/`aim` fixture 上增加如下断言
   集，并先运行三文件命令。测试入口必须是 `closePane`、`closeTab`、`decodeWorkbench`
   或 `buildRestore`，不能只测私有 helper。

   ```ts
   // 复用 tabs.test.ts 既有 handoff/aim、openTab/addColumn/placeSource fixture。
   // 每条 expect 都是必须保留的 pass/fail 判据。
   it('关闭 pane 收列、收组，唯一组只重置为空组', () => {
     let wb = openTab(EMPTY_WORKBENCH, handoff, { kind: 'tui', taskId: 'A' })
     const groupId = wb.activeGroupId
     wb = addColumn(wb)
     wb = placeSource(wb, { kind: 'new', base: aim, content: { kind: 'tui', taskId: 'B' } }, {
       groupId, column: 1, row: 0, zone: 'center',
     })
     wb = closeTab(wb, groupId, 't1')
     expect(wb.groups[0].columns).toHaveLength(1)
     expect(wb.groups[0].columns[0].panes[0]).toMatchObject({ content: { kind: 'tui', taskId: 'B' } })
     wb = closeTab(wb, groupId, 't2')
     expect(wb.groups).toHaveLength(1)
     expect(wb.groups[0].columns).toEqual([{ panes: [null] }])
     expect(wb.activeGroupId).toBe(groupId)
   })

   it('空 pane 也可关，关闭非法坐标不改变布局', () => {
     let wb = openTab(EMPTY_WORKBENCH, handoff, { kind: 'tui', taskId: 'A' })
     wb = placeSource(wb, { kind: 'new', base: aim, content: { kind: 'tui', taskId: 'B' } }, {
       groupId: 'g1', column: 0, row: 0, zone: 'bottom',
     })
     wb = closePane(wb, 'g1', 0, 1)
     expect(wb.groups[0].columns[0].panes).toHaveLength(1)
     expect(wb.groups[0].columns[0].panes[0]?.content).toEqual({ kind: 'tui', taskId: 'A' })
     const before = wb
     expect(closePane(wb, 'g1', 9, 0)).toBe(before)
   })
   ```

   `tabs.test.ts` 已有测试中的 “清 pane 不自动关组” 断言必须改成上述生命周期，
   不得保留与 B285 冲突的旧期望。
3. **跑红并记录**：只运行
   `npm test -- --run src/app/workbench/tabs.test.ts src/app/workbench/persist.test.ts src/app/workbench/restore.test.ts`；
   新锁缝在未实现时应红。将命令和原始失败行追加台账，不把预期红写成已验证。
4. **实现最小模型改动**：先抽出可复用的空组重置与 focus 夹取逻辑；再改
   `closePane`/`closeTab`，最后改 `appendRestoredTab`。每条非法目标分支带结构化
   `console.warn` 上下文，成功写入路径用 `console.debug` 记录 group/column/row 或
   session 归属；禁止 `print`、`console.log`。
5. **实现解码压缩**：在 `decodeWorkbench` 的 `parseWorkbench` 返回值上调用
   `normalizeWorkbench`；不要在解析阶段删除用户仍有内容的 column。注释写明“为何
   空列必须在恢复前删除、为何唯一空组是同步层删除的哨兵”，导出函数补参数/返回/注意事项。
6. **补序列化边界红测**：在既有 `persist.test.ts` fixture 中覆盖如下完整边界：

   ```ts
   it('解码先压缩空列空组，并区分缺失字段与零/空值', () => {
     const source = {
       v: PERSIST_VERSION,
       wb: {
         activeGroupId: 'g1',
         groups: [
           {
             id: 'g1', name: '一组', autoName: true,
             columns: [
               { panes: [null] },
               { panes: [{ id: 't1', base: { key: '', kind: 'workspace', path: '', label: '', projectName: '', machine: '' }, content: { kind: 'terminal', seq: 0, sessionId: '', rel: '', launcher: '' } }] },
               { panes: [null] },
             ],
             sizes: [2, 3, 4], focus: [1, 0],
           },
           { id: 'g2', name: '空组', autoName: true, columns: [{ panes: [null] }], sizes: [1], focus: [0, 0] },
         ],
       },
     }
     const decoded = decodeWorkbench(JSON.stringify(source))!
     expect(decoded.groups).toHaveLength(1)
     expect(decoded.groups[0].columns).toHaveLength(1)
     expect(decoded.groups[0].sizes).toEqual([3])
     expect(decoded.groups[0].columns[0].panes[0]?.content).toEqual({ kind: 'terminal', seq: 0, sessionId: '', rel: '', launcher: '' })
     expect(decoded.groups[0].columns[0].panes[0]?.base).toEqual({ key: '', kind: 'workspace', path: '', label: '', projectName: '', machine: '' })
   })

   it('所有解码后的组都为空时回到唯一空组', () => {
     const raw = encodeWorkbench({
       activeGroupId: 'g2',
       groups: [
         { id: 'g1', name: '一组', autoName: true, columns: [{ panes: [null] }], sizes: [1], focus: [0, 0] },
         { id: 'g2', name: '二组', autoName: false, columns: [{ panes: [null] }], sizes: [1], focus: [0, 0] },
       ],
     })
     expect(decodeWorkbench(raw)).toEqual(EMPTY_WORKBENCH)
   })
   ```

   上述片段复用 `persist.test.ts` 已有 `PERSIST_VERSION`/`EMPTY_WORKBENCH` import；若
   当前 import 未包含它们，先按该文件现有命名补 import。必须同时保留现有 draft、
   baseSha、incompatible 被剥离的断言。该 roundtrip 锁直接穿过
   `encodeWorkbench -> JSON -> decodeWorkbench`，并以 `seq: 0`、空字符串和字段缺失
   区分“存在但为零/空”与“未提供”。
7. **补恢复接缝红测**：在 `restore.test.ts` 既有 `state`、`session`、`withSession`、
   `VIEW` fixture 上改写孤儿测试，逐条断言：

   ```ts
   it('恢复孤儿不填现有空列，每个工作区 PTY 独立成组', () => {
     const layout: Workbench = {
       activeGroupId: 'g1',
       groups: [{
         id: 'g1', name: '组 1', autoName: true,
         columns: [
           { panes: [{ id: 't1', base: baseA, content: { kind: 'terminal', seq: 1, sessionId: 'LIVE' } }] },
           { panes: [null] },
         ],
         sizes: [2, 1], focus: [0, 0],
       }],
     }
     const r = buildRestore({
       state: state({ bases: [{ base_key: '__global_workbench__', payload: encodeWorkbench(layout), updated_at: 1 }] }),
       sessions: [session('LIVE'), session('S2', { base_path: '/repo/b' }), session('S3', { base_path: '/repo/c' })],
       ...VIEW,
     })
     expect(r.adopted).toBe(2)
     expect(r.workbench.groups).toHaveLength(3)
     expect(r.workbench.groups[0].columns).toHaveLength(1)
     expect(r.workbench.groups[1].columns[0].panes[0]?.content).toMatchObject({ sessionId: 'S2' })
     expect(r.workbench.groups[2].columns[0].panes[0]?.content).toMatchObject({ sessionId: 'S3' })
     expect(r.workbench.groups.every((group) => group.columns.every((column) => column.panes.some(Boolean)))).toBe(true)
   })
   ```

   既有“无空 pane 时追加组”断言改为每个孤儿一个组；legacy 行继续断言进入
   `r.legacy`，不拼旧按目录 payload。若 fixture 需要一个唯一空哨兵，必须通过真实
   `buildRestore` 走入 `appendRestoredTab`，不能直接调用私有函数替代接缝。
8. **跑绿**：再次运行 Task 1 三文件命令，记录完整输出；然后运行
   `npm run typecheck`，记录实际退出码。测试范围只含本 Task 六个文件对应的三份
   test 文件，禁止把全量测试当本 Task 的判据。
9. **变异复核**：临时把 `closeTab` 的删除改回 `null` 后只跑新的 close 生命周期测试，
   必须出现“列仍在”失败；恢复实现后再跑绿。该变异结果与命令原文写入台账后撤销。

### Task 1 验收

- 行为：关闭一个 pane 收列、关闭最后内容收组、唯一组只保留一个空组、空 pane 可关；
  非法坐标不修改引用且有上下文 warning。
- 恢复：解码合法布局先去空列/空组；全空只得到 `EMPTY_WORKBENCH`；每个孤儿 workspace
  PTY 进入独立组，不填现有空列，不重复成列+组；home PTY 仍走 dock，legacy 仍丢弃。
- 序列化：`encodeWorkbench` 与 `decodeWorkbench` 的实际 JSON 边界保留空字符串/零值，
  区分 optional 缺失，继续剥离运行时字段；没有后端字段变化。
- 可观测性：入口、非法目标、pane limit、孤儿领养等分支均有结构化上下文；注释说明
  删除/压缩的原因，导出 API 有参数、返回和注意事项。
- 缝双向：`tabs.test.ts` 入口覆盖 `closePane`/`closeTab`，`persist.test.ts` 入口穿过
  encode/decode，`restore.test.ts` 入口是 `buildRestore`；清单中的生命周期、恢复、
  序列化三条子缝各至少一条断言。

## Task 2：Workbench API、关闭按钮、半区预览与单层终端标题

### 目标与有界文件集

只触及以下文件：

- `web/src/app/workbench/useWorkbench.ts`
- `web/src/app/workbench/WorkbenchPage.tsx`
- `web/src/app/workbench/TerminalTab.tsx`
- `web/src/app/workbench/useWorkbench.test.ts`
- `web/src/app/workbench/WorkbenchPage.test.tsx`
- `web/src/app/workbench/TerminalTab.test.tsx`

`WorkbenchPage` 当前约 249–251 行有“＋分屏”条，约 204 行用 1px 边条；当前 pane
header 只给非空 tab 渲染关闭按钮。`TerminalTab.tsx:595-605` 另画 label/path
header。Task 2 将这些 UI 接缝接到 Task 1 的模型，不改 `TabBar` 的“新建内容写入当前组”
语义。

### Interfaces

Consumes：

```ts
// web/src/app/workbench/useWorkbench.ts
export interface WorkbenchApi {
  base: BaseDir | null
  wb: Workbench
  select: (base: BaseDir) => void
  open: (content: TabContent, base?: BaseDir, groupId?: string) => void
  openOrFocus: (content: TabContent, base?: BaseDir) => void
  openTerminal: (base?: BaseDir, groupId?: string, rel?: string) => void
  close: (groupId: string, tabId: string) => void
  activate: (groupId: string, tabId: string) => void
  activateGroup: (groupId: string) => void
  setContent: (groupId: string, tabId: string, content: TabContent) => void
  addGroup: () => void
  closeGroup: (groupId: string) => void
  splitColumn: (groupId?: string) => void
  place: (source: WorkbenchSource, target: PaneTarget) => void
  closePane: (groupId: string, column: number, row: number) => void
  closeById: (tabId: string) => void
  resize: (groupId: string, dividerIndex: number, delta: number, minRatio: number) => void
  restoreTerminal: (base: BaseDir, sessionId: string, incompatible?: boolean) => void
  hydrate: (workbench: Workbench) => void
  openedItems: OpenedWorkbenchItem[]
}

// web/src/app/workbench/WorkbenchPage.tsx
export interface WorkbenchPageProps {
  api: WorkbenchApi
  onAddProject: () => void
  renderContent: (content: TabContent, base: BaseDir, groupId: string, tabId: string, active?: boolean) => ReactNode
  terminalUnavailable?: string
  onBeforeClose?: (content: TabContent, tabId: string, base: BaseDir) => boolean
  tree: ProjectTreeResp | null
  tasks: Task[]
  onFileCreated?: () => void
  launchers?: Launcher[]
}

// web/src/app/workbench/TerminalTab.tsx（签名保持不变，只删内部第二层视觉）
export interface TerminalTabProps {
  base: BaseDir
  seq: number
  sessionId?: string
  incompatible?: boolean
  rel?: string
  envFile?: string
  initCommand?: string
  onSession: (id: string) => void
  active?: boolean
}
```

Produces：

```ts
// web/src/app/workbench/useWorkbench.ts：Task 3 已移除 Shell 的调用后，Task 2 的最终 API。
export interface WorkbenchApi {
  base: BaseDir | null
  wb: Workbench
  select: (base: BaseDir) => void
  open: (content: TabContent, base?: BaseDir, groupId?: string) => void
  openOrFocus: (content: TabContent, base?: BaseDir) => void
  openTerminal: (base?: BaseDir, groupId?: string, rel?: string) => void
  close: (groupId: string, tabId: string) => void
  activate: (groupId: string, tabId: string) => void
  activateGroup: (groupId: string) => void
  setContent: (groupId: string, tabId: string, content: TabContent) => void
  addGroup: () => void
  closeGroup: (groupId: string) => void
  place: (source: WorkbenchSource, target: PaneTarget) => void
  closePane: (groupId: string, column: number, row: number) => void
  closeById: (tabId: string) => void
  resize: (groupId: string, dividerIndex: number, delta: number, minRatio: number) => void
  restoreTerminal: (base: BaseDir, sessionId: string, incompatible?: boolean) => void
  hydrate: (workbench: Workbench) => void
  openedItems: OpenedWorkbenchItem[]
}

// useWorkbench.ts
export function useWorkbench(): WorkbenchApi

// WorkbenchPage.tsx
export function WorkbenchPage(props: WorkbenchPageProps): JSX.Element

// TerminalTab.tsx
export function TerminalTab(props: TerminalTabProps): JSX.Element
```

Task 2 的产出 `WorkbenchApi` 移除 `splitColumn` 字段；纯模型的 `addColumn` 仍可保留给
布局测试/内部模型，不得再有用户可见按钮或键盘入口调用它。`place` 是唯一从当前 pane
增加分屏的 UI 写入接口。Task 2 开工时仍按上面的 Consumes 签名接收该字段，因而 Task 3
先删掉 Shell 的最后一个调用是必要的顺序约束。

### 视觉与事件契约

1. 每个 pane（包括 `tab === null`）都渲染一个关闭按钮；空 pane 按钮调用
   `api.closePane(group.id, columnIndex, row)`。非空 tab 仍先经过 `onBeforeClose`，
   关闭按钮 `stopPropagation`，避免触发 pane focus。
2. pane 自身点击激活非空 tab：`api.activate(group.id, tab.id)`；空 pane 点击至少
   `api.activateGroup(group.id)`，确保 focus/active group 不靠左树选中态推断。
3. `dragOver` 状态必须带 `groupId`：

   ```ts
   type DragOver = {
     groupId: string
     column: number
     row: number
     zone: DropZone
   }
   ```

   overlay 必须 `data-testid="drop-preview"`、`data-zone={zone}`，并用半区尺寸而非
   1px 条：left/right 使用 `w-1/2`，top/bottom 使用 `h-1/2`；center 使用覆盖中央
   区域的半透明背景和 ring。预览坐标、`dropZoneAt` 的可加列/可加 pane 限制与真正
   `PaneTarget` 必须来自同一次 DOM rect 计算。`dragleave` 只在相关目标离开 pane
   时清理；drop 成功、无效 MIME、列已满均清理预览。

   ```tsx
   const previewClass: Record<DropZone, string> = {
     left: 'inset-y-0 left-0 w-1/2',
     right: 'inset-y-0 right-0 w-1/2',
     top: 'inset-x-0 top-0 h-1/2',
     bottom: 'inset-x-0 bottom-0 h-1/2',
     center: 'inset-1/4',
   }

   {dragOver?.groupId === group.id && dragOver.column === columnIndex && dragOver.row === row && (
     <span
       data-testid="drop-preview"
       data-zone={dragOver.zone}
       aria-hidden="true"
       className={cn(
         'pointer-events-none absolute z-20 rounded-sm bg-primary/15',
         previewClass[dragOver.zone],
         dragOver.zone === 'center' && 'ring-2 ring-primary/50',
       )}
     />
   )}
   ```

4. 保留当前四种 MIME 的读取与 `place` 调用：task/dir 的 new source、opened tab 的
   tab source；invalid source 不调用 `api.place`，warning 带 project/machine/path、
   group/column/row/zone/reason。成功 drop 写 `console.debug`，限制与错误写
   `console.warn`，禁止静默成功。
5. 删除 WorkbenchPage 的“＋分屏”条以及其 `api.splitColumn` 引用。`TabBar` 里的
   `onNew` 仍调用 `api.openTerminal(base, groupId)` 或文件/TUI 当前组路径，不改为新组。
6. `TerminalTab` 保留 pty host、连接/退出/错误/重开按钮和所有 PTY 生命周期；删除
   `TerminalSquare` import 与内部 label/path header，避免窗格内第二道标题。WorkbenchPage
   pane header 是唯一标题层，仍显示 tabTitle、project/machine 和 close。

### 2–5 分钟步骤

1. **基线判据复核**：在 `web/` 运行
   `npm test -- --run src/app/workbench/useWorkbench.test.ts src/app/workbench/WorkbenchPage.test.tsx src/app/workbench/TerminalTab.test.tsx`，
   已实测原始结果为 `Test Files 3 passed (3)`、`Tests 58 passed (58)`；再检查
   `npm run typecheck` 已实测退出 0。把任何偏差原样写入台账。
2. **写锁缝红测**：复用 `useWorkbench.test.ts` 的 `renderHook`、`WorkbenchPage.test.tsx`
   的 `page`/`setRect`、`TerminalTab.test.tsx` 的 PTY mock。完整判据如下：

   ```ts
   // useWorkbench.test.ts：复用已有 local/base 与 renderHook。
   it('closePane 暴露空 pane 关闭并委托统一布局生命周期', () => {
     const hook = renderHook(() => useWorkbench())
     act(() => hook.result.current.select(local))
     act(() => hook.result.current.open({ kind: 'tui', taskId: 'A' }, local))
     act(() => hook.result.current.place({ kind: 'new', base: remote, content: { kind: 'tui', taskId: 'B' } }, {
       groupId: 'g1', column: 0, row: 0, zone: 'bottom',
     }))
     act(() => hook.result.current.closePane('g1', 0, 1))
     expect(hook.result.current.wb.groups[0].columns[0].panes).toHaveLength(1)
     expect(hook.result.current.wb.groups[0].columns[0].panes[0]?.content).toEqual({ kind: 'tui', taskId: 'A' })
   })
   ```

   ```tsx
   // WorkbenchPage.test.tsx：复用 page(api)、setRect、既有 local/remote。
   it('拖到右半区显示半区预览并通过 place 增加列', () => {
     const hook = renderHook(() => useWorkbench())
     act(() => hook.result.current.open({ kind: 'tui', taskId: 'local' }, local))
     const view = render(page(hook.result.current))
     const pane = view.container.querySelector('[data-testid="workbench-pane"]') as HTMLElement
     setRect(pane, 400, 400)
     const dataTransfer = {
       types: [DRAG_TASK_MIME],
       getData: (key: string) => key === DRAG_TASK_MIME ? 'TASK-R' : JSON.stringify(remote),
       setData: vi.fn(), effectAllowed: '', dropEffect: '',
     }
     fireEvent.dragOver(pane, { dataTransfer, clientX: 360, clientY: 200 })
     expect(view.getByTestId('drop-preview')).toHaveAttribute('data-zone', 'right')
     expect(view.getByTestId('drop-preview')).toHaveClass('w-1/2')
     fireEvent.drop(pane, { dataTransfer, clientX: 360, clientY: 200 })
     expect(hook.result.current.wb.groups[0].columns).toHaveLength(2)
     expect(hook.result.current.wb.groups[0].columns[1].panes[0]).toMatchObject({
       base: remote, content: { kind: 'tui', taskId: 'TASK-R' },
     })
   })

   it('空 pane 的关闭按钮穿过 WorkbenchPage 并删除该格', () => {
     const hook = renderHook(() => useWorkbench())
     const view = render(page(hook.result.current))
     fireEvent.click(view.getByRole('button', { name: '关闭 空窗格' }))
     expect(hook.result.current.wb.groups).toHaveLength(1)
     expect(hook.result.current.wb.groups[0].columns).toEqual([{ panes: [null] }])
   })
   ```

   ```tsx
   // TerminalTab.test.tsx：沿用现有 create/connect mock 与 WS harness。
   it('终端只提供 pty host，不重复渲染基准路径标题', async () => {
     render(<TerminalTab base={WS} seq={1} onSession={vi.fn()} />)
     expect(screen.getByTestId('pty-host')).toBeInTheDocument()
     expect(screen.queryByText(WS.path)).toBeNull()
   })
   ```

   以上是既有 harness 复用例外：harness 形态已经由三个文件固定，计划不复制 PTY mock
   和 DOM rect 工厂；每个新断言、入口符号和 pass/fail 结果已逐条列出。不得把纯
   `dropZoneAt` 单测当作 `WorkbenchPage` seam 的替代。
3. **跑红并记录**：只运行 Task 2 的三文件命令；确认新断言在当前实现上分别因没有
   `closePane`、1px/无 preview、第二层 header而红。记录原始输出，不写“预期红”冒充
   实测结果。
4. **接 API 与关闭路径**：从 `useWorkbench.ts` 引入 Task 1 的 `closePane`，新增稳定
   callback，返回 `closePane`；移除 `splitColumn`。WorkbenchPage pane header 给 null
   也渲染 close，按钮路径与非空关闭分支分开，日志带 group/column/row/base/tab。
5. **接真实预览**：将 `dragOver` 扩成带 groupId 的 `DragOver`，在 dragover/drop 共用
   rect 和 zone，写出五种 `data-zone` overlay 类；对中心也渲染预览。注释说明半区
   阴影为何不能用 1px 边线以及列满时 top/bottom 如何退化。
6. **移除用户分屏入口**：删除 WorkbenchPage layout-strip；确认 Task 3 已清理 Shell
   的键盘调用后，再从 `WorkbenchApi`/`useWorkbench` 返回值删除 `splitColumn`。测试
   fixture 用 `place`/`hydrate` 构造列，不从被删的 API 假造用户行为。
7. **删终端第二层标题**：删除 `TerminalSquare` 及只用于内层标题的 JSX，保留 host、
   error、dead、exit 视觉与所有 effect。文件头/TerminalTabProps 注释明确标题责任
   在 WorkbenchPage，避免未来再把路径塞回终端组件。
8. **跑绿与局部类型检查**：重新运行 Task 2 三文件命令和 `npm run typecheck`，记录
   实际结果。测试范围只跑这三个 test 文件；全量测试不属于本 Task。
9. **锁缝变异复核**：临时让 pane close 只写 null，再跑 close pane/close group 测试，
   必须因列/组仍存在而红；临时把 preview 改回 `w-1`，拖右半区测试必须因缺少
   `w-1/2` 而红；撤销两处变异并重新跑绿。每次命令和原始输出追加台账。

### Task 2 验收

- UI：所有 pane（包括空 pane）都有关闭入口；关闭按钮不误触 focus；关闭结果遵守
  Task 1 的收列/收组/唯一组规则。
- 拖放：任务、目录、已打开 tab 都继续使用现有 MIME；dragover 真实显示左/右半幅、
  上/下半幅或中心阴影，drop 的 `PaneTarget` 与预览 zone 一致；列满退化仍有 warning。
- 标题：窗格只留 WorkbenchPage 一层标题，TerminalTab 不出现 label/path 第二层；PTY
  host 和连接生命周期没有卸载/删除改变。
- 入口：不存在“＋分屏”或 `splitColumn` 用户路线；分屏只由 drag/drop `place` 产生；
  TabBar 新建内容仍当前组。
- 可观测性/注释：drop start/invalid/limit/success 与 close rejection 有结构化上下文；
  新状态和导出函数注释写明边界。
- 缝双向：`WorkbenchPage.test.tsx` 入口穿过 `dragOver/drop`、空 pane close；
  `useWorkbench.test.ts` 入口穿过 `closePane`；`TerminalTab.test.tsx` 入口穿过组件
  输出。Task 2 每条声明缝都有至少一支缝级断言。

## Task 3：左树新组语义、方案 2 图标与 Shell 跟焦

### 目标与有界文件集

只触及以下文件：

- `web/src/app/shell/Shell.tsx`
- `web/src/app/shell/Breadcrumb.tsx`
- `web/src/app/shell/DesktopTitleBar.tsx`
- `web/src/app/tree/ProjectTree.tsx`
- `web/src/app/tree/ProjectTree.test.tsx`
- `web/src/app/shell/Shell.test.tsx`

现状：`Shell.tsx:405-410` 的目录终端走 `wb.openTerminal` 当前组；`Shell.tsx:492` 桌面
标题和 `Shell.tsx:544` 浏览器面包屑都传 `wb.base`；Shell 中还有 `splitColumn` 的
⌘D 监听。`ProjectTree.tsx:589` 给任务行统一 `onMouseDown(e.preventDefault())`；
`TaskAvatar` 在 `ProjectTree.tsx:132-145` 由 Lucide 图标充当头像。Task 3 只改这些
调用与投影，不改任务/项目数据接口和 search 过滤算法。

### Interfaces

Consumes：

```ts
// web/src/app/shell/Shell.tsx
export function Shell(): JSX.Element

// web/src/app/tree/ProjectTree.tsx
export interface ProjectTreeProps {
  tree: ProjectTreeResp
  tasks: Task[]
  selectedKey: string | null
  ticketCount: number
  ticketsByDir: Map<string, number>
  openedItems: ReadonlyArray<OpenedWorkbenchItem>
  onOpenDirectory: (base: BaseDir) => void
  onOpenDirectoryTerminal: (base: BaseDir) => void
  onOpenItem: (item: OpenedWorkbenchItem) => void
  onOpenTask: (base: BaseDir | null, taskId: string) => void
  onOpenBoard: () => void
  onOpenCards?: () => void
  onOpenProjectCards?: (project: ProjectNode) => void
  ledgerEnabled?: boolean
  onOpenFlows?: () => void
  unlinkedCount?: number
  cardNeedsCount?: number
  onOpenTickets: () => void
  onOpenSettings: () => void
  onOpenCodegraph?: () => void
  onOpenProjectCodegraph?: (project: ProjectNode) => void
  onAddProject?: () => void
  onUnregister?: (name: string, machine: string) => Promise<void> | void
  onEdit?: (project: ProjectNode) => void
  onWorktreeCreated?: (project: ProjectNode, machine: string, ws: Workspace) => void
}

// Breadcrumb.tsx 与 DesktopTitleBar.tsx 对外 props 保持不变。
export function Breadcrumb({ base }: { base: BaseDir }): JSX.Element
export function DesktopTitleBar({ base }: { base: BaseDir | null }): JSX.Element
```

Produces：

```ts
// Shell 内部纯投影 helper；不导出，不形成第二个状态源。
function focusedPaneBase(workbench: Workbench): BaseDir | null

// Existing callbacks keep these exact types.
const openDirectoryTerminal: (base: BaseDir) => void
const openWorkbenchItem: (item: { base: BaseDir; groupId: string; tabId: string }) => void
const openTaskTui: (base: BaseDir | null, taskId: string) => void
```

`focusedPaneBase` 从 `workbench.activeGroupId` 找 group，再读该 group 的 `focus` 坐标，
只有 pane 非 null 才返回 `pane.base`；越界/空 pane 返回 null。`wb.base` 仍是左栏选择、
文件抽屉和 TabBar “新建内容”的 selected base，不能用 focused base 取代它。

### 行为契约与完整实现形状

Shell 的焦点投影必须保持单向：

```ts
function focusedPaneBase(workbench: Workbench): BaseDir | null {
  const group = workbench.groups.find((candidate) => candidate.id === workbench.activeGroupId)
  if (!group) return null
  const [column, row] = group.focus
  return group.columns[column]?.panes[row]?.base ?? null
}

const focusedBase = focusedPaneBase(wb.wb)
// 桌面与浏览器顶部只消费 focusedBase；selected wb.base 继续给 ProjectTree/FileDrawer。
<DesktopTitleBar base={focusedBase} />
{focusedBase && !desktop && !fullPageRoute && <Breadcrumb base={focusedBase} />}
```

目录/机器的“开终端”必须是新组语义，且每次无 session 的 terminal 都是新内容：

```ts
const openDirectoryTerminal = (base: BaseDir) => {
  backToWorkbench()
  wb.select(base)
  wb.openOrFocus({ kind: 'terminal', seq: nextTerminalSeq(wb.wb) }, base)
  console.debug('shell.directory.terminal.new_group', {
    project: base.projectName, machine: base.machine, baseKey: base.key, path: base.path,
  })
}
```

`openTaskTui` 保持 `wb.openOrFocus`：opened TUI 命中既有 tab 只激活其 group，未打开
任务才新建 group；`openWorkbenchItem` 仍 `select + activate`，因此左栏点击已打开项
只改变 focus，不新建 tab/group。Shell 取消 ⌘D 分屏 effect、`split` ref 和相关注释；
Ctrl+D 不得被 Shell 监听，继续交给终端。

任务行方案 2 的类型图标不再依赖 Lucide 的圆钮。`TaskAvatar` 应直接包含下面三套
稳定 inline SVG（`data-testid` 继续保持 `task-avatar-terminal/file/tui`，状态点保留
`data-testid="task-status"`）：

```tsx
function TaskTypeIcon({ kind }: { kind: TaskRowKind }) {
  if (kind === 'terminal') return (
    <svg data-testid="task-avatar-icon" viewBox="0 0 16 16" aria-hidden="true" className="size-3.5">
      <rect x="1.5" y="3" width="13" height="10" rx="2" fill="none" stroke="currentColor" strokeWidth="1.3" />
      <path d="M4.5 7.5 L7 9 L4.5 10.5" fill="none" stroke="currentColor" strokeWidth="1.3" />
      <path d="M8.5 10.5h3" stroke="currentColor" strokeWidth="1.3" />
    </svg>
  )
  if (kind === 'file') return (
    <svg data-testid="task-avatar-icon" viewBox="0 0 16 16" aria-hidden="true" className="size-3.5">
      <path d="M5 2.5h4.5L12.5 6v7.5H5z" fill="none" stroke="currentColor" strokeWidth="1.3" />
      <path d="M9.5 2.5V6h3" fill="none" stroke="currentColor" strokeWidth="1.3" />
    </svg>
  )
  return (
    <svg data-testid="task-avatar-icon" viewBox="0 0 16 16" aria-hidden="true" className="size-3.5">
      <circle cx="8" cy="6" r="2.4" fill="currentColor" />
      <path d="M4 13c0-2.2 1.8-4 4-4s4 1.8 4 4" fill="none" stroke="currentColor" strokeWidth="1.4" />
    </svg>
  )
}

function TaskAvatar({ kind, tone }: { kind: TaskRowKind; tone: StateTone }) {
  return (
    <span data-testid={`task-avatar-${kind}`} className="relative flex size-5 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground">
      <TaskTypeIcon kind={kind} />
      <span data-testid="task-status" className="absolute -bottom-0.5 -right-0.5 rounded-full border-2 border-sidebar">
        <StateDot tone={tone} />
      </span>
    </span>
  )
}
```

去掉只为头像类型服务的 `CircleUserRound`/`FileText` import；保留 ProjectTree 其它地方
仍用的 `Terminal`（例如目录开终端按钮）。`renderTaskRow` 和未归属任务行保留
`draggable`、`DRAG_TASK_MIME`/`DRAG_BASE_MIME` payload 与 `effectAllowed`，删除会阻断
native drag 起始的 `onMouseDown={e => e.preventDefault()}`。点击仍调用原 `onClick`，
不把拖放改成自定义 pointer 手势。未归属任务也属于任务行：将现有裸 `StateDot` 替换
为 `<TaskAvatar kind="tui" tone={stateTone(t.state)} />`，保留其 `BaseDir` 为 null 的
点击回调和 `DRAG_BASE_MIME` 的 `null` payload，并在右侧显示 `machineLabel(t.machine)`。

### 2–5 分钟步骤

1. **基线判据复核**：在 `web/` 运行
   `npm test -- --run src/app/tree/ProjectTree.test.tsx src/app/shell/Shell.test.tsx`，
   已实测原始结果为 `Test Files 2 passed (2)`、`Tests 96 passed (96)`；再检查本计划列出的
   Shell 调用面。若源码与图不一致，以源码为准并入账。
2. **写锁缝红测**：复用 `ProjectTree.test.tsx` 已有 tree/tasks/`dataTransfer` fixture
   与 `Shell.test.tsx` 已有 `renderShell`、`openBranch`、真实 fetch mocks。保留/新增
   以下具体断言：

   ```tsx
   // ProjectTree.test.tsx：沿用 props()、其 T1 任务和已有 openedItems fixture；
   // 文件顶部补用 DRAG_BASE_MIME/DRAG_TASK_MIME 两个既有常量。
   it('任务行使用方案 2 的 SVG 类型头像并能开始 HTML5 drag', () => {
     const view = render(<ProjectTree {...props({ openedItems: [
       {
         tabId: 'term-1', groupId: 'g1', column: 0, row: 0,
         base: { key: '/w', kind: 'workspace', path: '/w', label: 'main', projectName: 'handoff', machine: '' },
         content: { kind: 'terminal', seq: 1 }, label: '终端 · main',
       },
       {
         tabId: 'file-1', groupId: 'g1', column: 1, row: 0,
         base: { key: '/w', kind: 'workspace', path: '/w', label: 'main', projectName: 'handoff', machine: '' },
         content: { kind: 'file', rel: 'README.md' }, label: 'README.md',
       },
     ] })} />)
     expect(view.getByTestId('task-avatar-terminal').querySelector('svg')).not.toBeNull()
     expect(view.getByTestId('task-avatar-file').querySelector('svg')).not.toBeNull()
     expect(view.getByTestId('task-avatar-tui').querySelector('svg')).not.toBeNull()
     const taskRow = view.getAllByTestId('task-row').find((row) => row.textContent?.includes('重构工单通道'))!
     expect(taskRow).toHaveAttribute('draggable', 'true')
     const dataTransfer = { setData: vi.fn(), effectAllowed: '' }
     fireEvent.dragStart(taskRow, { dataTransfer })
     expect(dataTransfer.setData).toHaveBeenCalledWith(DRAG_TASK_MIME, 'T1')
     expect(dataTransfer.setData).toHaveBeenCalledWith(DRAG_BASE_MIME, expect.any(String))
   })
   ```

   ```tsx
   // Shell.test.tsx：沿用 renderShell/openBranch 和已有 DataTransfer 工厂。
   it('左栏开目录终端创建新组，焦点面包屑跟当前 pane 而非 selected tree', async () => {
     renderShell()
     await openBranch()
     fireEvent.click(await screen.findByText('重构工单通道'))
     const beforeColumns = screen.getAllByTestId('workbench-pane').length
     const project = await screen.findByTestId('project-node-p1')
     fireEvent.click(within(project).getByTestId('machine-row'))
     await waitFor(() => expect(screen.getAllByRole('tab')).toHaveLength(2))
     expect(screen.getAllByTestId('workbench-pane').length).toBeGreaterThanOrEqual(beforeColumns)
     expect(screen.getByLabelText('当前位置')).toHaveTextContent('handoff')
     fireEvent.click(screen.getByText('integration/b2-b3'))
     expect(screen.getByLabelText('当前位置')).toHaveTextContent('handoff')
     expect(screen.getByLabelText('当前位置')).not.toHaveTextContent('integration/b2-b3')
   })
   ```

   `props()`、`renderShell`、`openBranch`、`within`、`waitFor` 均指已有测试 harness
   的名字；若现有测试以局部 helper 名称不同，直接在同一文件沿用那个已存在的 helper，
   不造第二套网络 mock。除上列断言外，Shell 测试还必须真实拖 task 到 pane 的右半部，
   断言 `DRAG_TASK_MIME` 与 `DRAG_BASE_MIME` 穿过 ProjectTree → WorkbenchPage 后新列
   的 base/taskId；并真实点击已打开 item，断言 group 数不增而 focus 坐标改变。再用
   `renderShell` 现有 global payload mock 预置一个含两列的 g1，点击未打开 T1，逐条断言
   `screen.getAllByRole('tab')` 从 1 变 2、g1 的两列仍存在、T1 所在新组只有首格；
   在既有未归属任务测试中断言 `task-avatar-tui`、`task-status` 和右侧机器文本均存在，
   且 null base MIME 仍为字符串 `null`。
3. **跑红并记录**：先运行 Task 3 两文件命令；新测试在旧实现上应分别暴露 Lucide/drag
   起始问题、目录终端当前组问题或 breadcrumb selected/focused 问题。只记录真实输出，
   不代替测试归因。
4. **加入 focused 投影**：在 Shell import `type Workbench`、`nextTerminalSeq`，添加
   `focusedPaneBase`；桌面 `DesktopTitleBar` 与浏览器 `Breadcrumb` 都改传 `focusedBase`。
   `Breadcrumb.tsx` 文件头/props 注释改为“显示焦点 pane base；Shell 在无焦点内容时
   不渲染”，`DesktopTitleBar.tsx` 注释改为 null 时显示应用名 fallback；不添加任何
   点击交互。
5. **改左栏打开语义**：目录/机器终端按完整代码形状调用 `openOrFocus`，并保留
   `wb.select(base)` 供左树 selectedKey；任务已打开项只 activate；未打开任务继续
   `openOrFocus` 新建 group。每条入口日志带 baseKey/project/machine/path 以及 group/tab
   上下文，错误路径仍沿现有 `ptyNote`/组件提示走。
6. **去除键盘分屏**：删除 Shell 的 split callback、⌘D effect 和注释；此时先保留
   `WorkbenchApi.splitColumn`，因为 WorkbenchPage 的旧 layout-strip 尚待 Task 2 删除。
   保留现有 Ctrl+D 不抢终端 EOF 的测试，但把“⌘D 分屏成功”测试改为断言 ⌘D 不创建
   pane、不 `preventDefault`；Task 2 删除 layout-strip 后再删除该 API 字段。
7. **实现方案 2 SVG 与可拖任务**：替换 TaskAvatar，保留状态点右下、机器名右侧、
   `data-testid`；删除两处 task row 的 mousedown preventDefault。注释写明 SVG 是
   类型语义、状态点是运行态，MIME payload 是跨组件边界；成功 drag 记录必要 debug
   上下文时使用项目已有 logger 约定，不使用 print。
8. **跑绿与局部类型检查**：运行 Task 3 两文件命令与 `npm run typecheck`；然后跑
   Task 2 的三文件命令，确保删除 split API 的邻接面编译通过。记录每条原始输出。
9. **变异复核**：临时把 Shell breadcrumb 恢复为 `wb.base`，focused seam 测试必须
   在切左树选中后红；临时恢复 `onMouseDown.preventDefault`，task drag seam 必须红；
   撤销变异后重跑绿，结果写入台账。

### Task 3 验收

- 左栏：未打开任务点击新 group；已打开 item 只聚焦原 pane；目录/机器“开终端”新建
  group，不改变当前组列数；组栏“新建内容”仍当前组（由 Task 2 既有测试守护）。
- 顶栏：桌面薄壳和浏览器 Breadcrumb 都读 active group 的 focused pane base；切换
  selected tree 不改变 focused pane 展示；无焦点内容不渲染浏览器 breadcrumb，桌面
  fallback 可为空/应用名。
- 任务行：terminal/file/TUI 各有 inline SVG，状态点右下，机器名右侧；正常 task 行
  native drag 可写出 task/base MIME；点击语义仍可用。
- 键盘：⌘D 不再分屏也不挂书签拦截逻辑；Ctrl+D 仍未被 Shell 监听。分屏唯一 UI 路径
  是真实拖放。
- 可观测性/注释：开目录、开任务、聚焦 opened item、拖放错误/成功均有上下文日志；
  focusedBase、SVG、native drag 的非显然理由写进相关文件头/导出注释。
- 缝双向：`Shell.test.tsx` 入口覆盖 Shell → ProjectTree → WorkbenchPage 的开目录、
  开任务、拖放、focused breadcrumb；`ProjectTree.test.tsx` 入口覆盖 task row SVG/MIME。
  每条左栏打开、拖放、顶栏跟焦声明缝至少一支真实 UI 断言。

## 4. 五项检查与跨任务审计

### 4.1 缺陷族对抗审查

| 缺陷族 | 对抗问题 | 计划结论/锁点 |
|---|---|---|
| 生命周期/资源 | 关闭最后 pane、空 pane、唯一 group、非唯一 group 是否分别可观察？ | Task 1 `closePane` seam + Task 2 空 pane UI；唯一组重置、非唯一组删除、列权重同步均有断言。PTY 只由 Shell 既有确认/TerminalTab 负责，关闭不改变 PTY 协议。 |
| 跨项目/跨机器身份 | 相同路径不同 machine 会不会误去重或顶栏串 base？ | 继续使用 `BaseDir.key`/`dedupKey`；Task 3 focusedBase 直接来自 tab.base；Shell 测试使用 local/remote DataTransfer 断言 project/machine/path。 |
| 拖放命中/退化 | 1px 预览、半区与列满退化是否互相混淆？ | Task 2 同时断言 `data-zone`、`w-1/2`/`h-1/2`、实际 `columns`；既有 pane limit warning 保留。 |
| 恢复/重复领养 | 解码空布局、旧行、dead/live/incompatible、多个孤儿会不会填错位置？ | Task 1 经过 `buildRestore` 的 global-only、legacy、normalize、每孤儿独立 group 测试；home 仍 dock，live used session 不重复。 |
| 序列化/运行时字段 | draft/baseSha/incompatible、零值与 optional 缺失是否被手工 map 丢掉？ | Task 1 roundtrip 真实穿过 `encodeWorkbench`/JSON/`decodeWorkbench`，显式断言空字符串、seq 0、字段缺失和运行时字段剥离。 |
| 静默失败/可观测性 | 无效 group/坐标/MIME、close rejection、PTY 领养是否能定位上下文？ | 每条错误分支指定 `console.warn` 的事件名与 group/base/reason；成功路径 debug；不使用 print。 |
| 误绿/绕闸 | 测试是否只测纯 helper 而绕过声明缝？ | seam 清单逐条指定入口；纯 `dropZoneAt` 仅保留辅助测试，不能顶替 WorkbenchPage drop；Shell/ProjectTree 使用真实 DOM/DataTransfer。 |
| 枚举/协议 | 新字段或 zone 是否扩大后端契约？ | 不改 API/persist version；DropZone 仍五值；SVG kind 仍 terminal/file/tui；unknown payload 继续拒绝。 |

### 4.2 序列化与投影边界清单

| 边界 | 产生 → 消费 | 文件 | 必须有的断言 |
|---|---|---|---|
| 全局 payload | `Workbench` → JSON → `Workbench` | `persist.ts`, `persist.test.ts` | encode/decode roundtrip 保留 BaseDir、layout、0/空 optional；draft/baseSha/incompatible 不落盘；合法空列/空组被 normalize。 |
| 恢复合成 | state row payload/session → `RestoreResult.workbench` | `restore.ts`, `restore.test.ts` | 真实 `buildRestore` 中 legacy 丢弃、dead prune、used session、每 workspace orphan 独立 group、home dock。 |
| 服务端写回 | `Workbench` → `encodeWorkbench` → `useWorkbenchSync.flush` | `useWorkbenchSync.ts`, 已有 `useWorkbenchSync.test.ts` | 空布局仍由 `isEmptyWorkbench` 写删除；非空 payload 只有 `__global_workbench__`；不增加 API 字段。 |
| 左树 MIME | `ProjectTree` task/base → `DataTransfer` JSON → `WorkbenchPage` parse | `ProjectTree.tsx`, `WorkbenchPage.tsx`, `ProjectTree.test.tsx`, `Shell.test.tsx` | 真实 dragStart/drop 保留 taskId 与完整 BaseDir；缺失/坏 JSON 不回退错误目录并有 warning。 |
| 已打开 pane MIME | pane title/opened row → tab JSON → `placeSource` | `WorkbenchPage.tsx`, `tabs.ts`, `WorkbenchPage.test.tsx` | 真正 tab move 经过 UI 读 MIME、空源列收列、目标 center/edge 结果正确。 |
| focused 投影 | global focus coordinates → `focusedPaneBase` → Breadcrumb/DesktopTitleBar 文本 | `Shell.tsx`, `Breadcrumb.tsx`, `DesktopTitleBar.tsx`, `Shell.test.tsx` | focused pane base 与 selected tree base 分离；切 selected 后 breadcrumb 文本仍 focused。 |
| URL/后端另一侧 | 本卡新增数据 → URL/API | 无新增边界 | 计划显式断言“不新增 URL/API/persist 字段”；Cards/FileDrawer 维持既有行为，不把 B285 字段投影进去。 |

### 4.3 上下文预算

每个 Task 的文件集在任务标题下逐个列出且不跨到 agentd/API；Task 1 是纯 model/persist/restore
六文件，Task 2 是 Workbench React/Terminal 六文件，Task 3 是 Shell/ProjectTree 六文件。
若实现中发现必须改未列文件，先暂停并把新增路径、调用链和理由追加台账，由协调者审边界；
不得“顺手”扩大到全仓。

### 4.4 类型与真机清单

- `npm run typecheck` 必须真实退出 0。
- Workbench 模型真机清单：两格列关一格、单格列收列、非唯一 group 收组、唯一 group
  重置、空 pane 可关、focus/activeGroupId 合法、sizes 与 columns 等长。
- 拖放真机清单：task/dir/opened tab 三种 MIME；左/右/上/下/center 五区；列满上下
  退化并提示；task 行实际 HTML5 drag；source/target group 坐标没有错位。
- 左栏/顶栏真机清单：未打开任务新组、已打开只聚焦、目录/机器终端新组、组栏新建
  当前组、focused pane base 驱动浏览器/桌面顶部、无 focus 时可空。
- 恢复真机清单：global 行合法布局、空列/空组、旧 legacy 行、dead session、live
  orphan workspace、home orphan、incompatible session；三个 workspace orphan 形成
  三个独立 group，不重复填列。
- 序列化真机清单：BaseDir 空字符串、terminal `seq: 0`、`sessionId: ''`、`rel: ''`、
  `launcher: ''`、optional 缺失、runtime draft/baseSha/incompatible；每个值的“缺失”
  与“存在但零/空”都通过真实 encode/decode 检查。

### 4.5 接缝覆盖双向矩阵

本卡 spec 已冻结为一条总缝“全局工作台布局生命周期”，拆成以下可定位子缝：

| 接缝 | 测试入口（测试 → 缝） | 缝 → 测试的最小断言 |
|---|---|---|
| 关闭 pane/收列/收组 | `tabs.test.ts` 的 `closePane`/`closeTab`；`WorkbenchPage.test.tsx` 空 pane close | Task 1 断言列/组生命周期，Task 2 断言真实 close button；变异留 null 必红。 |
| 左栏未打开/已打开/目录终端 | `Shell.test.tsx` 的 ProjectTree click 与 opened item click | 未打开 task/目录终端 group 数增加且原组列数不变；opened item group 数不增且 focus 改变。 |
| 拖放投影/半区预览 | `WorkbenchPage.test.tsx` dragOver/drop；`Shell.test.tsx` 真实 ProjectTree dragStart → pane drop | overlay 半区 class/zone、task MIME、new column、base/taskId；invalid MIME 不写布局。 |
| 恢复领养 | `restore.test.ts` 入口 `buildRestore` | 空列不填、每 orphan 独立 group、legacy/home 规则；真实 payload 解码。 |
| focused 顶栏 | `Shell.test.tsx` 入口真实 Shell/ProjectTree/WorkbenchPage | selected tree 变化不改变 focused breadcrumb；桌面 title 使用同一 focusedBase 投影。 |
| 单层标题/任务行视觉 | `TerminalTab.test.tsx`、`ProjectTree.test.tsx` 组件入口 | path 无第二层、pty host 仍在；三种 SVG、status dot、machine 文本、dragStart MIME。 |

每条测试的入口都在表中；只测 `normalizeWorkbench`、`dropZoneAt` 或 `focusedPaneBase`
的内部测试不能顶替上表。内部 helper 测试若保留，只能作为附加锁，理由是：从声明缝
无法直接构造“空列焦点重映射”或“纯坐标阈值边界”这两类单独不变量；它们不计入接缝覆盖。

## 5. Spec 故事归属与跨任务签名自审

| spec 用户故事 | 具体归属 |
|---|---|
| 1. 关分屏、关组、唯一组空组 | Task 1 `closePane`/`closeTab` 模型断言；Task 2 WorkbenchPage 空 pane close 真实接缝。 |
| 2. 左栏未打开任务新组 | Task 3 `Shell.test.tsx` 真实任务点击；Task 1 `openOrFocus` 既有全局去重契约保持。 |
| 3. 任务右半拖放加列并有阴影 | Task 2 `WorkbenchPage` preview/place；Task 3 `ProjectTree` MIME 与 Shell 真实跨组件测试。 |
| 4. 顶栏跟焦点窗格 | Task 3 `focusedPaneBase`、Breadcrumb/DesktopTitleBar 与 Shell seam。 |
| 5. 旧桌面三个终端按真实布局/独立组恢复 | Task 1 `normalizeWorkbench`/`appendRestoredTab`/`buildRestore` seam。 |
| 方案 2 任务行与单层终端标题 | Task 3 SVG/task row；Task 2 TerminalTab/WorkbenchPage title seam。 |

跨任务签名逐字自审：

- Task 1 Produces `closePane(wb: Workbench, groupId: string, column: number, row: number): Workbench`
  与 Task 2 Consumes `closePane: (groupId: string, column: number, row: number) => void` 对齐；
  Task 2 callback 返回 `void`，内部把当前 `Workbench` 传给 Task 1 纯函数。
- Task 1 Produces `normalizeWorkbench(wb: Workbench): Workbench`，Task 1 自己的
  `decodeWorkbench(raw: string): Workbench | null` 消费它；没有跨任务别名。
- Task 2 在 Task 3 已清理 Shell 调用后 Produces `WorkbenchApi`（含 `place` 与 `closePane`、
  不含 `splitColumn`）；Task 3 只消费当前存在的 `select/openOrFocus/activate` 精确签名，
  并在先行步骤删除 Shell 的 `splitColumn` 调用。
- Task 3 `ProjectTreeProps` 的所有 callback 精确沿用现有声明；Task 3 只改变 callback
  内部实现，不改 `BaseDir | null` 的未归属任务口径。

本卡没有第二张卡的 Produces/Consumes 或另一个 spec 输入；跨卡签名与故事审计在派发前
由协调者对冻结 spec 再复核，执行者不自行补充外部契约。

## 6. 台账、提交与收口

- 每确立一个事实、每次跑命令、每次红/绿/变异结果，都立即追加到
  `docs/superpowers/ledgers/2026-08-28-b285-spec-ledger.md`；不攒到最后。
- 计划节点不写实现代码、不建脚手架；本文件中的 TypeScript/TSX 是给实现节点的完整
  接口/断言/实现形状，不是在本节点落地产品实现。
- 所有实现类 Task 均只跑触及包的测试；实现节点完成后协调者再执行全量
  `npm test -- --run` 与最终审计。
- 派发系统相关的最终 gate（检查 worktree、五项审计、全量测试、提交状态）由协调者
  执行，不派发给 executor。
- 收口前逐项扫描计划正文中的常见占位词、模糊错误处理语句、未指向任务的引用；预期
  零命中，再运行 `git diff --check`（预期退出 0）。计划中的 harness 复用例外已在 Task 2/3
  明文声明，并逐条列出了断言与既有文件名；没有未声明的骨架测试或条件退路。
- 计划写入后只 `git add docs/superpowers/plans/b285-plan.md docs/superpowers/ledgers/2026-08-28-b285-spec-ledger.md`
  并 commit，不 push；提交 hash 和真实检查结果写回台账后 amend 同一提交，最终回报
  `branch/commit/summary`。
