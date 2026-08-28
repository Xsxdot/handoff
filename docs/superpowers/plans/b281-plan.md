# B281 工作台 IA：左栏项目×机器 + 中央标签组分屏实施计划

## 0. 依据、边界与执行顺序

本计划只实现 `docs/superpowers/specs/b281.md`，以已抓取的
`7e13dd0bb` 中 `prototypes/b264-tab-groups/index.html` 为形态基准。原型中
`.cols` / `.col` / `.pane` 的结构在该文件第 40–65 行，中央组栏、布局条、可关闭文件抽屉
在第 198–208 行，全球查找/新组/目录终端在第 397–460 行，四向投放在第 485–546 行，
项目→机器→「任务」「目录」双同级组在第 654–767 行，组栏在第 769–801 行，列与窗格在
第 821–872 行。原型第 564–567 行的固定 `g4` 形状不实现，因为 B281 已明确弃选五/六宫格。

不改 `internal/agentd`、`internal/proto`、PTY/TUI 协议、账本、跨机同步协议、HomeDock、
`NewWorktreeDialog` 的请求形状或原型目录。现有 `/api/workbench/state/base` 仍是唯一布局写入
入口：`internal/agentd/workbench_api.go:64-103` 实际只拒绝空 `base_key`，payload 是不解释的
字符串；因此前端用非空保留键存全局布局，不添加后端字段或端点。

执行顺序是：

~~~text
Task 1 tabs.ts + paneDrop.ts 的全局布局模型
        ↓
Task 2 useWorkbench + persist + restore + useWorkbenchSync 的单一状态与持久化
        ↓                         ↓
Task 3 WorkbenchPage + TabBar + GroupDivider 的中央渲染/拖放
        ↓                         ↓
Task 4 ProjectTree + search + Shell + FileTree + CardsPage 的左栏、抽屉和路由
        ↓
协调者最终门禁：最小回归、构建、diff 检查、真实浏览器/桌面壳走查
~~~

计划事实台账：`docs/superpowers/ledgers/2026-08-28-b281-plan-ledger.md`。本计划节点不写实现代码；
下面的代码块是实施者必须照着落地的完整接口、数据形状与关键纯函数，不是本节点直接写入源码。

基线判据已经在改动前实际跑通：

| 范围 | 命令 | 基线结果 |
|---|---|---|
| 布局、持久化、恢复、中央 | `cd web && npm test -- --run src/app/workbench/tabs.test.ts src/app/workbench/paneDrop.test.ts src/app/workbench/persist.test.ts src/app/workbench/restore.test.ts src/app/workbench/useWorkbench.test.ts src/app/workbench/useWorkbenchSync.test.ts src/app/workbench/WorkbenchPage.test.tsx` | 7 files / 151 tests passed，退出 0 |
| 左树、搜索、文件、壳、账本页 | `cd web && npm test -- --run src/app/tree/ProjectTree.test.tsx src/app/tree/search.test.ts src/app/files/FileTree.test.tsx src/app/shell/Shell.test.tsx src/app/cards/CardsPage.test.tsx` | 5 files / 136 tests passed，退出 0 |
| 类型 | `cd web && npm run typecheck` | 无输出，退出 0 |

每个 Task 开始仍要先重跑它自己的最小基线命令；预期是沿用上表对应的通过结果。依赖缺失时先按
`web/package-lock.json` 执行 `npm ci --ignore-scripts`，不得把 `web/node_modules` 加入提交。

实施者可依赖的现状库/接口事实已在基线源码核对：最小列宽是 `web/src/app/workbench/tabs.ts:70-71`
的 `MIN_PANE_PX = 240`；同步写回去抖是 `web/src/app/workbench/useWorkbenchSync.ts:33-38`
的 `WRITE_DEBOUNCE_MS = 500`；跨机会话列表的 `scope=all` 由
`web/src/api/client.ts:628-634` 的 `fetchPtySessions(scope?: 'all')` 拼接；布局写入的
`putWorkbenchBase(baseKey: string, payload: string | null)` 是
`web/src/api/client.ts:665-672`。计划不凭库记忆增加其它默认值。

## 1. 跨 Task 冻结接口

### 1.1 `tabs.ts` 的新模型

`web/src/app/workbench/tabs.ts` 是布局接缝唯一真相。`BaseDir` 从
`useWorkbench.ts` 移到这里，由 `useWorkbench.ts` 重新导出，避免 Tab 反向依赖状态 hook。
每个 Tab 自带自己的目录，当前左栏选中态不再决定中央显示什么。

~~~ts
export interface BaseDir {
  key: string
  kind: 'workspace' | 'home' | 'scratch'
  path: string
  label: string
  projectName: string
  machine: string
}

export type TabContent =
  | { kind: 'blank' }
  | {
      kind: 'terminal'
      seq: number
      sessionId?: string
      rel?: string
      incompatible?: boolean
      launcher?: string
    }
  | { kind: 'file'; rel: string; draft?: string; baseSha?: string }
  | { kind: 'tui'; taskId: string }

export interface Tab {
  id: string
  base: BaseDir
  content: TabContent
}

export interface TabColumn {
  panes: Array<Tab | null>
}

export interface TabGroup {
  id: string
  name: string
  autoName: boolean
  columns: TabColumn[]
  sizes: number[]
  focus: [number, number]
}

export interface Workbench {
  groups: TabGroup[]
  activeGroupId: string
}

export const MAX_PANES_PER_COLUMN = 2
export const MIN_PANE_PX = 240

export interface PaneTarget {
  groupId: string
  column: number
  row: number
  zone: DropZone
}

export type WorkbenchSource =
  | { kind: 'new'; base: BaseDir; content: TabContent }
  | { kind: 'tab'; groupId: string; tabId: string }

export interface OpenedWorkbenchItem {
  tabId: string
  groupId: string
  column: number
  row: number
  base: BaseDir
  content: TabContent
  label: string
}

export const EMPTY_WORKBENCH: Workbench = {
  groups: [{
    id: 'g1',
    name: '组 1',
    autoName: true,
    columns: [{ panes: [null] }],
    sizes: [1],
    focus: [0, 0],
  }],
  activeGroupId: 'g1',
}

export function openTab(wb: Workbench, base: BaseDir, content: TabContent, groupId?: string): Workbench
export function openOrFocus(wb: Workbench, base: BaseDir, content: TabContent): Workbench
export function closeTab(wb: Workbench, groupId: string, tabId: string): Workbench
export function activateTab(wb: Workbench, groupId: string, tabId: string): Workbench
export function activateGroup(wb: Workbench, groupId: string): Workbench
export function setTabContent(wb: Workbench, groupId: string, tabId: string, content: TabContent): Workbench
export function createGroup(wb: Workbench, name?: string): Workbench
export function closeGroup(wb: Workbench, groupId: string): Workbench
export function addColumn(wb: Workbench, groupId?: string): Workbench
export function placeSource(wb: Workbench, source: WorkbenchSource, target: PaneTarget): Workbench
export function resizeColumns(
  wb: Workbench,
  groupId: string,
  dividerIndex: number,
  delta: number,
  minRatio: number,
): Workbench
export function appendRestoredTab(wb: Workbench, base: BaseDir, content: TabContent): Workbench
export function nextTerminalSeq(wb: Workbench): number
export function isEmptyWorkbench(wb: Workbench): boolean
export function openedWorkbenchItems(wb: Workbench): OpenedWorkbenchItem[]
export function tabTitle(content: TabContent, baseLabel: string): string
~~~

模型不变量必须在所有纯函数中维持：至少一个 group；至少一个 column；每个 column 有 1 或 2
个 pane；空 column 只保留一个 `null` pane；`sizes.length === columns.length` 且每个值为正数；
`activeGroupId` 指向现有 group；`focus` 指向现有 column 与 pane。关闭 group 是显式动作，最后
一组重置为一个空组；清空 pane 不自动关闭 group。这样顶栏的组和组内的空窗格与原型一致。

`openTab` 的落点也固定：先在目标 group 里找 focus 指向的空 pane，再按列/行顺序找其它
`null` pane；找到就填入并聚焦。目标 group 没有空 pane 时，在最右追加一列单空 pane，再把
Tab 放进去；它不覆盖已有内容，也不凭空给列增加第三格。`openOrFocus` 的未命中路径则另建
一个 group（第一列第一格放入 Tab），因为左栏“未打开任务”与顶栏新组的行为必须可观察地区分。
因此 `openTab` 的显式 `groupId` 只决定上述搜索/追加发生在哪个组，非法 id 原样返回并记录
上下文；`placeSource` 的 center 替换仍是唯一会因 drop 覆盖已有 pane 的纯函数路径。

去重键必须是 `dedupKey(baseKey: string, content: TabContent): string | null`：文件为
`file:${baseKey}:${rel}`，TUI 为 `tui:${taskId}`，带 session 的终端为 `pty:${sessionId}`，
无 session 的终端和 blank 不去重。`openOrFocus` 找遍全部 group；命中就更新 group/focus，
未命中才创建新 group。普通 `open` 只在指定 group（缺省当前 group）使用。`placeSource` 的
center 替换目标 pane、left/right 插入新 column、top/bottom 在目标 column 插入新 pane；
column 已有两格时 top/bottom 退化为 center 替换，不生成第三格。拖动已有 Tab 时先从源位置移除，
再按目标位置插入；center 直接同一位置命中时只聚焦，不能制造重复 Tab。

### 1.2 `paneDrop.ts` 的投影接口

`web/src/app/workbench/paneDrop.ts` 消费 DOM 坐标，不能把 DOM 判断塞进 `tabs.ts`。

~~~ts
export const DRAG_TASK_MIME = 'text/handoff-task'
export const DRAG_BASE_MIME = 'text/handoff-base'
export const DRAG_DIR_MIME = 'text/handoff-dir'
export const DRAG_TAB_MIME = 'text/handoff-tab'
export const DRAG_GROUP_MIME = 'text/handoff-group'

export type DropZone = 'left' | 'right' | 'top' | 'bottom' | 'center'

export function dropZoneAt(
  offsetX: number,
  offsetY: number,
  width: number,
  height: number,
  canAddColumn: boolean,
  canAddPane: boolean,
): DropZone

export function readDragBase(raw: string): BaseDir | null
export function readDragTab(raw: string): { groupId: string; tabId: string } | null
export function readDragGroup(raw: string): { groupId: string } | null
~~~

`dropZoneAt` 使用原型四向 28% 阈值：先判断 left/right；再判断 top/bottom；剩余为 center。
`canAddColumn=false` 时 left/right 退化为 center，`canAddPane=false` 时 top/bottom 退化为
center，宽或高小于等于 0 一律 center。B281 没有列数上限，但保留参数让调用方在横向无法再
分配时能明确退化。JSON 读取必须校验字符串字段齐全，失败返回 null，不抛异常。

## Task 1：建立全局组、列、窗格的纯模型与坐标接缝

### 文件范围

只改：

- `web/src/app/workbench/tabs.ts`
- `web/src/app/workbench/paneDrop.ts`
- `web/src/app/workbench/tabs.test.ts`
- `web/src/app/workbench/paneDrop.test.ts`

不在本 Task 改 React、同步、HTTP、目录树或测试配置。

### Interfaces

Consumes：`BaseDir`、`TabContent`、`DropZone`、`WorkbenchSource`、`PaneTarget` 的上面精确签名；
`tabs.ts` 不消费 `ProjectTreeResp`、React、API client。

Produces：上面 `Workbench` 全部纯函数；Task 2 的 `useWorkbench` 消费
`openTab`、`openOrFocus`、`closeTab`、`activateTab`、`setTabContent`、`createGroup`、`closeGroup`、
`addColumn`、`placeSource`、`appendRestoredTab`、`resizeColumns`、`nextTerminalSeq`、
`isEmptyWorkbench`；Task 3 消费 `openedWorkbenchItems`、`tabTitle` 与 `DropZone`；Task 4 消费
`BaseDir`、`OpenedWorkbenchItem` 与拖放 MIME 常量。

### 1. 基线、测试范围与锁缝红测

先运行：

~~~text
cd web && npm test -- --run src/app/workbench/tabs.test.ts src/app/workbench/paneDrop.test.ts
~~~

预期为基线 2 files、现有布局相关测试通过；本节点已在更宽的 7 files/151 tests 范围实际取得
通过。测试只触及 `tabs.ts`、`paneDrop.ts` 的纯模型，不跑全仓。

在基线后替换旧的按 BaseDir fixture，并先加入以下缝级失败测试。代码块是完整的新增断言，
`base` 是测试文件顶部按现有形状定义的 `BaseDir`；第二个 base 的项目、机器、路径均不同。

~~~ts
it('不同项目的 TUI 与终端可以落在同一全局组的左右两列', () => {
  const handoff: BaseDir = {
    key: '/repo/handoff', kind: 'workspace', path: '/repo/handoff', label: 'main',
    projectName: 'handoff', machine: '',
  }
  const aim: BaseDir = {
    key: '/srv/aim@linux-01', kind: 'workspace', path: '/srv/aim', label: 'eval',
    projectName: 'aim', machine: 'linux-01',
  }
  let wb = openTab(EMPTY_WORKBENCH, handoff, { kind: 'tui', taskId: 'B281' })
  wb = addColumn(wb)
  const groupId = wb.activeGroupId
  wb = placeSource(wb, {
    kind: 'new', base: aim, content: { kind: 'terminal', seq: 1, rel: '' },
  }, { groupId, column: 1, row: 0, zone: 'center' })
  expect(wb.groups[0].columns[0].panes[0]).toMatchObject({
    base: { projectName: 'handoff', machine: '' },
    content: { kind: 'tui', taskId: 'B281' },
  })
  expect(wb.groups[0].columns[1].panes[0]).toMatchObject({
    base: { projectName: 'aim', machine: 'linux-01' },
    content: { kind: 'terminal', rel: '' },
  })
  expect(wb.groups[0].columns[0].panes).toHaveLength(1)
  expect(wb.groups[0].columns[1].panes).toHaveLength(1)
})
~~~

再加入以下完整的列/两格/中间替换断言：

~~~ts
it('一列最多上下两格，第三次上下投放替换目标格', () => {
  const baseA: BaseDir = {
    key: '/a', kind: 'workspace', path: '/a', label: 'a', projectName: 'A', machine: '',
  }
  const baseB: BaseDir = {
    key: '/b', kind: 'workspace', path: '/b', label: 'b', projectName: 'B', machine: '',
  }
  let wb = openTab(EMPTY_WORKBENCH, baseA, { kind: 'tui', taskId: 'A' })
  const groupId = wb.activeGroupId
  wb = placeSource(wb, { kind: 'new', base: baseB, content: { kind: 'tui', taskId: 'B' } }, {
    groupId, column: 0, row: 0, zone: 'bottom',
  })
  wb = placeSource(wb, { kind: 'new', base: baseA, content: { kind: 'tui', taskId: 'C' } }, {
    groupId, column: 0, row: 1, zone: 'bottom',
  })
  expect(wb.groups[0].columns[0].panes).toHaveLength(2)
  expect(wb.groups[0].columns[0].panes[1]).toMatchObject({ content: { kind: 'tui', taskId: 'C' } })
})

it('openOrFocus 命中全局已有 tab，不创建新组', () => {
  const baseA: BaseDir = {
    key: '/a', kind: 'workspace', path: '/a', label: 'a', projectName: 'A', machine: '',
  }
  let wb = openTab(EMPTY_WORKBENCH, baseA, { kind: 'tui', taskId: 'A' })
  wb = createGroup(wb, '第二组')
  const before = wb.groups.length
  wb = openOrFocus(wb, baseA, { kind: 'tui', taskId: 'A' })
  expect(wb.groups).toHaveLength(before)
  expect(wb.activeGroupId).toBe('g1')
  expect(wb.groups[0].focus).toEqual([0, 0])
})

it('没有列数上限，连续加列仍保留每列最小宽度所需的权重', () => {
  const baseA: BaseDir = {
    key: '/a', kind: 'workspace', path: '/a', label: 'a', projectName: 'A', machine: '',
  }
  let wb = openTab(EMPTY_WORKBENCH, baseA, { kind: 'terminal', seq: 1 })
  wb = addColumn(wb)
  wb = addColumn(wb)
  wb = addColumn(wb)
  expect(wb.groups[0].columns).toHaveLength(4)
  expect(wb.groups[0].sizes).toEqual([1, 1, 1, 1])
})
~~~

`paneDrop.test.ts` 复用现有纯函数 describe/fixture，并加入以下完整断言；foreign MIME 由
Task 3 的真实 WorkbenchPage drop seam 断言，不在纯坐标函数里伪造：

~~~ts
it('28% 四向边缘与 center 的坐标投影逐项正确', () => {
  expect(dropZoneAt(20, 200, 400, 400, true, true)).toBe('left')
  expect(dropZoneAt(380, 200, 400, 400, true, true)).toBe('right')
  expect(dropZoneAt(200, 20, 400, 400, true, true)).toBe('top')
  expect(dropZoneAt(200, 380, 400, 400, true, true)).toBe('bottom')
  expect(dropZoneAt(200, 200, 400, 400, true, true)).toBe('center')
})

it('不可加列或不可加第二格时，边缘投放退化为 center', () => {
  expect(dropZoneAt(20, 200, 400, 400, false, true)).toBe('center')
  expect(dropZoneAt(380, 200, 400, 400, false, true)).toBe('center')
  expect(dropZoneAt(200, 20, 400, 400, true, false)).toBe('center')
  expect(dropZoneAt(200, 380, 400, 400, true, false)).toBe('center')
})

it('无布局尺寸时一律 center，并严格区分合法与坏 JSON 源', () => {
  expect(dropZoneAt(0, 0, 0, 0, true, true)).toBe('center')
  const base = {
    key: '/repo/a', kind: 'workspace', path: '/repo/a', label: 'a', projectName: 'handoff', machine: '',
  }
  expect(readDragBase(JSON.stringify(base))).toEqual(base)
  expect(readDragBase('not-json')).toBeNull()
  expect(readDragBase(JSON.stringify({ ...base, path: 1 }))).toBeNull()
  expect(readDragTab(JSON.stringify({ groupId: 'g1', tabId: 't1' }))).toEqual({ groupId: 'g1', tabId: 't1' })
  expect(readDragTab(JSON.stringify({ groupId: 'g1' }))).toBeNull()
  expect(readDragGroup(JSON.stringify({ groupId: 'g1' }))).toEqual({ groupId: 'g1' })
  expect(readDragGroup('null')).toBeNull()
})
~~~

先跑：

~~~text
cd web && npm test -- --run src/app/workbench/tabs.test.ts src/app/workbench/paneDrop.test.ts
~~~

新 seam 测试必须先红；若意外先绿，仍保留并检查旧按目录 fixture 是否偷偷满足了断言，不能改成
直接测私有 helper。

### 2. 最小实现、日志与注释

1. 把 `Tab`、`TabGroup`、`Workbench` 从旧 `tabs/active/sizes` 改为上面的全局模型；所有复制函数
   只复制数组与外层对象，不突变输入。
2. 用 `findTabByDedupKey` 与 `findTabLocation` 搜整个 `groups[].columns[].panes[]`；Tab ID
   继续用确定性的数字后缀分配，但必须跨所有 group 唯一。
3. 实现 `placeSource` 的拆移顺序：解析源 → 若为 tab 源移除源并修正目标索引 → 处理 center/左右/上下
   → 规范空列/焦点/权重。错误的 group、column、row 或 JSON 源全部返回原对象并带上下文。
4. `resizeColumns` 只调整目标 group 的相邻 `sizes`，沿用 `MIN_PANE_PX` 转比例和窄容器拒绝
   规则；不能再引用 `MAX_GROUPS`。`availablePaneWidth` 如仍被 `GroupDivider` 使用，保留其
   精确签名 `availablePaneWidth(parentWidth: number, separatorWidths: number[]): number`。
5. `paneDrop.ts` 只读坐标与 JSON，不调 API。所有 JSON 解析失败都返回 null。
6. 纯模型无外部调用；不在每帧成功投放上打印日志。错误分支使用现有结构化 `console.warn`：
   事件名固定，第二参数为 `{ groupId, column, row, zone }`；成功路径的可见日志由 Task 2/3 的
   hook/DOM 入口负责，避免把纯函数变成不可测的 IO。
7. 更新文件头、导出函数注释，明确“全局 group、每列最多两 pane、Tab 自带 BaseDir”；解释
   `Tab` 自带 BaseDir 是为了跨项目同组和文件跨项目去重，不得恢复旧的 by-base 归属。

### 3. 绿测与 Task 验收

只跑 Task 1 范围的两个测试文件；通过条件是：跨项目 TUI/终端确实在同一 `activeGroupId` 的两列；
`openOrFocus` 不增组；列可超过三列；每列永远最多两格；center 替换、边缘插列、移除源不会留下
空列；`dropZoneAt` 四向与退化值准确。然后 `git diff --check`，再把本 Task 的事实/红绿输出追加
台账，才进入 Task 2。

## Task 2：把 hook、持久化与会话恢复改成单一全局工作台

### 文件范围

只改：

- `web/src/app/workbench/useWorkbench.ts`
- `web/src/app/workbench/persist.ts`
- `web/src/app/workbench/restore.ts`
- `web/src/app/workbench/useWorkbenchSync.ts`
- `web/src/app/workbench/useWorkbench.test.ts`
- `web/src/app/workbench/persist.test.ts`
- `web/src/app/workbench/restore.test.ts`
- `web/src/app/workbench/useWorkbenchSync.test.ts`

不改 `web/src/api/client.ts` 的请求签名；允许更新注释，把 `baseKey` 说明为存储行键而不再暗示
它一定是工作树路径。agentd/proto 继续原样接受字符串 payload。

### Interfaces

Consumes：Task 1 的 `Workbench`/纯函数；现有 `fetchWorkbenchState(): Promise<WorkbenchStateResp>`、
`fetchPtySessions(scope?: 'all'): Promise<PtySessionsResp>`、
`putWorkbenchBase(baseKey: string, payload: string | null): Promise<void>`、
`putWorkbenchSelected(baseKey: string): Promise<void>`、`putWorkbenchDock(payload: string | null): Promise<void>`；
现有 `DockSnapshot`、`HomeTab`。

Produces：`useWorkbench()` 必须完整返回以下精确 API：

~~~ts
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
  closeById: (tabId: string) => void
  resize: (groupId: string, dividerIndex: number, delta: number, minRatio: number) => void
  restoreTerminal: (base: BaseDir, sessionId: string, incompatible?: boolean) => void
  hydrate: (workbench: Workbench) => void
  openedItems: OpenedWorkbenchItem[]
}
~~~

`base` 只表示左栏/文件抽屉/新内容默认落点的选中目录，不再控制 `wb`。`open` 缺省用当前
selected base；没有 base 时空操作并带事件上下文。`openOrFocus` 缺省同样用 selected base，命中
已打开项就激活所在 group/cell，未命中才调用 `openOrFocus` 纯函数创建新 group。拖放使用
`place(source, target)` 一次变异，不能让调用方先 split 再 open。

### 1. 基线、测试范围与持久化红测

先运行：

~~~text
cd web && npm test -- --run src/app/workbench/useWorkbench.test.ts src/app/workbench/persist.test.ts src/app/workbench/restore.test.ts src/app/workbench/useWorkbenchSync.test.ts
~~~

预期沿用基线通过；本 Task 不跑中央 JSX 或全仓。

把现有按目录测试改成全局行为，并先加入以下锁缝断言：

~~~ts
it('切换左栏选中目录不会切换中央全局组，跨项目项仍在原 cell', () => {
  const a: BaseDir = {
    key: '/a', kind: 'workspace', path: '/a', label: 'a', projectName: 'handoff', machine: '',
  }
  const b: BaseDir = {
    key: '/b@linux-01', kind: 'workspace', path: '/b', label: 'b', projectName: 'aim', machine: 'linux-01',
  }
  const { result } = renderHook(() => useWorkbench())
  act(() => result.current.select(a))
  act(() => result.current.open({ kind: 'tui', taskId: 'local' }))
  const groupId = result.current.wb.activeGroupId
  act(() => result.current.splitColumn(groupId))
  act(() => result.current.place({
    kind: 'new', base: b, content: { kind: 'terminal', seq: 1, rel: '' },
  }, { groupId, column: 1, row: 0, zone: 'center' }))
  act(() => result.current.select(b))
  expect(result.current.wb.activeGroupId).toBe(groupId)
  expect(result.current.wb.groups[0].columns[0].panes[0]).toMatchObject({
    base: { projectName: 'handoff' }, content: { kind: 'tui', taskId: 'local' },
  })
  expect(result.current.wb.groups[0].columns[1].panes[0]).toMatchObject({
    base: { projectName: 'aim', machine: 'linux-01' }, content: { kind: 'terminal', rel: '' },
  })
})

it('左栏未打开任务通过 openOrFocus 只新建一组，第二次点同一任务只聚焦', () => {
  const base: BaseDir = {
    key: '/a', kind: 'workspace', path: '/a', label: 'a', projectName: 'handoff', machine: '',
  }
  const { result } = renderHook(() => useWorkbench())
  act(() => result.current.openOrFocus({ kind: 'tui', taskId: 'T1' }, base))
  const firstGroup = result.current.wb.activeGroupId
  act(() => result.current.openOrFocus({ kind: 'tui', taskId: 'T1' }, base))
  expect(result.current.wb.groups).toHaveLength(1)
  expect(result.current.wb.activeGroupId).toBe(firstGroup)
})
~~~

持久化采用明确的非空保留键，完整格式如下：

~~~ts
export const GLOBAL_WORKBENCH_KEY = '__global_workbench__'
export const PERSIST_VERSION = 2

interface PersistedWorkbench {
  v: number
  wb: Workbench
}

export function encodeWorkbench(wb: Workbench): string {
  const persisted: PersistedWorkbench = { v: PERSIST_VERSION, wb: stripWorkbench(wb) }
  return JSON.stringify(persisted)
}

export function decodeWorkbench(raw: string): Workbench | null
export function isEmptyWorkbench(wb: Workbench): boolean
export function pruneDeadSessions(wb: Workbench, liveIds: ReadonlySet<string>): Workbench
export function markIncompatibleSessions(wb: Workbench, ids: ReadonlySet<string>): Workbench
export function diffPayloads(
  previous: Record<string, string>,
  next: Record<string, string>,
): { changed: string[]; removed: string[] }
~~~

`stripWorkbench` 只去掉文件 Tab 的 `draft/baseSha` 和终端的 `incompatible`，保留每个 Tab 的完整
BaseDir、`rel`、`launcher`、`seq`、`sessionId`，并保留 group id/name/autoName、列顺序、sizes、focus。
`decodeWorkbench` 对版本、group、column、pane、focus、sizes、BaseDir、TabContent 逐字段白名单校验；
任何一处坏掉返回 null。禁止从旧的 per-base payload 拼出新布局。

真实序列化边界的 roundtrip 测试必须覆盖：两个项目、两台机器、同名相对文件、`seq: 0`、
`rel: ''` 与缺席 `rel`、`sessionId: ''` 与缺席 `sessionId`、文件 draft 被剥离、终端 incompatible
被剥离、launcher 被保留。断言必须同时看 `JSON.parse(raw)` 的字段缺席和 decode 后的对象，不能
只看最终对象而漏掉“写了但读端丢掉”的问题。

`RestoreResult` 改为以下精确形状：

~~~ts
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
~~~

`restore.ts` 的输入与导出也固定如下，不能让实施者从旧文件猜测返回行形状：

~~~ts
export interface RestoreInput {
  state: WorkbenchStateResp
  sessions: PtySession[]
  vw: number
  vh: number
  inset: number
}

export function baseOfSession(session: PtySession): BaseDir
export function buildRestore(input: RestoreInput): RestoreResult
~~~

`buildRestore(input: RestoreInput): RestoreResult` 只解码 `GLOBAL_WORKBENCH_KEY` 的一行；其余
`state.bases` 的 key 进入 `legacy`，不当作新布局。全局行坏了进入 `dropped`，workbench 回到
`EMPTY_WORKBENCH`。死 session 清掉 id 但保留 pane 位置；活着但 incompatible 的 session 保留 id
并打标。未落盘的 workspace session 用 `baseOfSession` + `appendRestoredTab` 加入全局工作台，
不改变 `selected`、activeGroup 或 focus；没有空 pane 时追加新 group 但不抢 active。home session
继续走 dock 规则，HomeDock 不变。

### 2. 最小实现与同步策略

1. `useWorkbench.ts` 只持有 `[base, setBase]` 与 `[workbench, setWorkbench]`；删除 `byBase`、
   `baseDirs`、按目录 `mutate`。`select` 只登记当前选中 BaseDir；显式 `base` 参数只写入 Tab
   的 base，不因拖放而替用户切换左栏。
2. `restoreTerminal` 使用 `appendRestoredTab`，不 select、不改 active；`hydrate` 整体替换
   workbench，保持选中 base 不动。
3. `useWorkbenchSync` 的 `WorkbenchSyncDeps` 改为：

~~~ts
export interface WorkbenchSyncDeps {
  workbench: Workbench
  selectedKey: string
  dockSnapshot: DockSnapshot
  hydrateWorkbench: (workbench: Workbench) => void
  hydrateDock: (snapshot: DockSnapshot) => void
  adoptDockTab: (tab: HomeTab) => void
}

export function useWorkbenchSync(deps: WorkbenchSyncDeps): {
  error: string
  restoredSelected: string
}
~~~

   恢复时仍 `Promise.all([fetchWorkbenchState(), fetchPtySessions('all')])`，先以服务端原始
   `state.bases` 播种 `sentRef`，再灌入 `r.workbench`；拉取失败保持 ready=false，后续不写回。
4. `flush` 的 `next` 只有一项：workbench 非空时
   `{ [GLOBAL_WORKBENCH_KEY]: encodeWorkbench(d.workbench) }`，为空时 `{}`。对 `sentRef` 做
   `diffPayloads`，因此旧 per-base 行、坏的 global 行和过期 legacy 行在一次成功状态变更后
   发 `putWorkbenchBase(key, null)` 清理；成功才更新 ref，失败下次继续重试。selected 与 dock
   仍各自沿现有 endpoint 去抖 500ms。
5. 同步日志沿 Web 现有结构化 console 口径：恢复入口带 `global_key`、`legacy`、`dropped`、
   `pruned`、`adopted`；HTTP 写入前后带 key/bytes，错误带同一 key 与原始 error；成功恢复不能
   静默。纯 restore 继续不打日志，由 sync 统一记录。
6. 每个新导出接口写参数、返回值、空值/错误注意事项；`persist.ts` 文件头写“只负责前端全局
   payload 编解码，不发 HTTP”；`restore.ts` 文件头写“只合成 workbench/dock，不选树节点”。

### 3. 红绿测试与 Task 验收

红测后最小实现，再运行：

~~~text
cd web && npm test -- --run src/app/workbench/useWorkbench.test.ts src/app/workbench/persist.test.ts src/app/workbench/restore.test.ts src/app/workbench/useWorkbenchSync.test.ts
cd web && npm run typecheck
~~~

验收必须是行为：切目录不丢跨项目同组；已打开任务聚焦不增组；restore 不抢 selected；global
payload roundtrip 保留列、机器、空字符串与缺席字段差异；旧/坏 payload 只丢坏行并清理；sync
只 PUT `GLOBAL_WORKBENCH_KEY`，空工作台发 null，selected/dock 仍原样写回；死 session 不会被
重新建成另一个 shell。`byBase`、`baseDirs`、`MAX_GROUPS` 在受影响实现和测试中均不得残留。

## Task 3：按原型重做中央组栏、列、窗格与拖放

### 文件范围

只改：

- `web/src/app/workbench/WorkbenchPage.tsx`
- `web/src/app/workbench/TabBar.tsx`
- `web/src/app/workbench/GroupDivider.tsx`
- `web/src/app/workbench/WorkbenchPage.test.tsx`
- `web/src/app/workbench/launchers.test.tsx`

`BlankTab.tsx`、`TerminalTab.tsx`、`FileTab.tsx`、`TuiTab.tsx` 的内容协议不改；它们只通过新的
`renderContent` 位置参数接入。

### Interfaces

Consumes：Task 2 的 `WorkbenchApi`、Task 1 的 `PaneTarget`/`DropZone`/MIME、现有
`createUntitledFile(base: BaseDir): Promise<string>`、`TaskPickerDialog`。

`WorkbenchPage` 的完整入参改为：

~~~ts
export interface WorkbenchPageProps {
  api: WorkbenchApi
  onAddProject: () => void
  renderContent: (
    content: TabContent,
    base: BaseDir,
    groupId: string,
    tabId: string,
    active?: boolean,
  ) => ReactNode
  terminalUnavailable?: string
  onBeforeClose?: (content: TabContent, tabId: string, base: BaseDir) => boolean
  tree: ProjectTreeResp | null
  tasks: Task[]
  onFileCreated?: () => void
  launchers?: Launcher[]
}
~~~

`TabBar.tsx` 保留文件名但职责变为顶栏全局组栏，不再渲染 pane 内 tab row：

~~~ts
export interface TabBarProps {
  groups: TabGroup[]
  activeGroupId: string
  base: BaseDir | null
  onActivateGroup: (groupId: string) => void
  onCloseGroup: (groupId: string) => void
  onNew: (groupId: string, kind: PickKind) => void
  onNewLauncher?: (groupId: string, name: string) => void
  launchers?: LauncherItem[]
  terminalUnavailable?: string
  onNewGroup: () => void
  onMoveGroup: (sourceGroupId: string, targetGroupId: string, zone: 'left' | 'right' | 'center') => void
}
~~~

组栏只显示 `group.name`、激活态、关闭、内容 `+` 菜单、新组按钮；内容 `+` 仍调用现有
`pickItemsFor(base, terminalUnavailable)` 和 `launchersFor`，但没有任何 pane 内 tab 条。组栏 tab
自身可拖动，drop 只接受 `DRAG_GROUP_MIME`，单 tab 组才执行移动；多 pane 组显示一次可见提示且
不改变布局。`GroupDividerProps` 保持精确签名：

~~~ts
export interface GroupDividerProps {
  onResize: (delta: number, containerWidth: number) => void
}
~~~

它现在用于同一 active group 的相邻 columns，`aria-orientation="vertical"`、键盘左右与 pointer
增量行为保留；调用方改为 `api.resize(groupId, columnIndex, delta, minRatio)`。

### 1. 基线、测试范围与中央 seam 红测

先运行：

~~~text
cd web && npm test -- --run src/app/workbench/WorkbenchPage.test.tsx src/app/workbench/launchers.test.tsx
~~~

红测必须从真实 `render(<WorkbenchPage api={renderHook(() => useWorkbench()).result.current} ... />)`
或现有 JSX harness 进入 `api.place/openOrFocus`，不能只调用 `dropZoneAt` 顶替中央接缝。需加入以下
完整行为断言：

~~~tsx
it('中央只在顶栏渲染 group tab，pane 内没有一排文件 tab', () => {
  const base: BaseDir = {
    key: '/a', kind: 'workspace', path: '/a', label: 'a', projectName: 'handoff', machine: '',
  }
  const hook = renderHook(() => useWorkbench())
  act(() => hook.result.current.open({ kind: 'file', rel: 'README.md' }, base))
  const view = render(
    <WorkbenchPage
      api={hook.result.current}
      tree={null}
      tasks={[]}
      onAddProject={vi.fn()}
      renderContent={() => <div>文件内容</div>}
    />,
  )
  expect(view.getByRole('tablist')).toBeInTheDocument()
  const pane = view.container.querySelector('[data-testid="workbench-pane"]')
  expect(pane).not.toBeNull()
  expect(within(pane as HTMLElement).queryByRole('tab')).toBeNull()
  expect(within(pane as HTMLElement).getByText('文件内容')).toBeInTheDocument()
})

it('从远端项目拖终端到右边，在同一 group 形成两列', () => {
  const local: BaseDir = {
    key: '/local', kind: 'workspace', path: '/local', label: 'local', projectName: 'handoff', machine: '',
  }
  const remote: BaseDir = {
    key: '/remote@linux-01', kind: 'workspace', path: '/remote', label: 'remote', projectName: 'aim', machine: 'linux-01',
  }
  const hook = renderHook(() => useWorkbench())
  act(() => hook.result.current.open({ kind: 'tui', taskId: 'local' }, local))
  const groupId = hook.result.current.wb.activeGroupId
  act(() => hook.result.current.splitColumn(groupId))
  const view = render(
    <WorkbenchPage
      api={hook.result.current}
      tree={null}
      tasks={[]}
      onAddProject={vi.fn()}
      renderContent={() => <div>内容</div>}
    />,
  )
  const panes = view.container.querySelectorAll('[data-testid="workbench-pane"]')
  expect(panes.length).toBeGreaterThanOrEqual(2)
  const second = panes[1] as HTMLElement
  const dataTransfer = {
    types: [DRAG_DIR_MIME],
    getData: (key: string) => key === DRAG_DIR_MIME ? JSON.stringify(remote) : '',
    setData: () => {},
    dropEffect: '',
  }
  const event = createEvent.drop(second, { dataTransfer })
  Object.defineProperty(event, 'clientX', { value: 200 })
  Object.defineProperty(event, 'clientY', { value: 200 })
  second.getBoundingClientRect = () => ({ left: 0, top: 0, right: 400, bottom: 400, width: 400, height: 400, x: 0, y: 0, toJSON: () => ({}) }) as DOMRect
  fireEvent(second, event)
  expect(hook.result.current.wb.groups[0].columns.flatMap((column) => column.panes).some((pane) =>
    pane?.base.projectName === 'aim' && pane.content.kind === 'terminal' && pane.content.rel === undefined,
  )).toBe(true)
})
~~~

目录拖放的 `dataTransfer` 必须真的走 `DRAG_DIR_MIME` JSON 到 `place`，不能直接把 `remote` 喂给
hook；该测试就是序列化边界回归。旧测试中使用 `clientX` 的 helper 必须扩展为 `clientY`，并明确
给 rect 宽高，否则 jsdom 的 0×0 会假绿。

### 2. 最小实现与可观测性

1. `WorkbenchPage` 顶层顺序照原型：`TabBar` → 可选形态提示 → layout strip（只保留 active group
   的 `＋分屏`）→ `.cols`。layout strip 的 `＋分屏` 调 `api.splitColumn(activeGroupId)`，每次把
   一个 `{ panes: [null] }` 放到当前组右侧并聚焦空 pane；不提供原型的 `g4` 固定预设。
2. `.cols` 横向遍历 active group columns；每个 `.col` 纵向遍历 1–2 panes；每个 `.pane` 有
   `data-testid="workbench-pane"`、标题头、项目/机器 meta、关闭按钮和一个内容 body。标题头
   draggable 写 `{ groupId, tabId }` 到 `DRAG_TAB_MIME`；关闭调用 `onBeforeClose(content,tabId,base)`，
   返回 false 时不变异。
3. pane drop 量 `clientX/clientY`，按 pane 是否已有两格传 `canAddPane`，按布局实际传
   `canAddColumn`；task 源构成 `{ kind:'new', base, content:{kind:'tui',taskId} }`，directory
   源构成 `{ kind:'new', base, content:{kind:'terminal',seq:nextTerminalSeq(wb)} }`，工作树根 cwd
   不写 `rel`，既有“在子目录打开”则保留 `rel`。两者都只调 `api.place` 一次。
4. group tab drop 读取 `DRAG_GROUP_MIME`，仅把单 tab 源转为 `WorkbenchSource.tab`，多 pane 源
   给用户可见短提示；单 tab 源以目标组 focus pane 为 center、以目标组首/末列为 left/right
   的 `PaneTarget` 调同一个 `api.place`，由模型在成功移动后只规范源组的空列并保留空组（只有
   `closeGroup` 能关组）；不能把整组隐式复制成两个内容或丢失源。
5. terminal keep-alive 的 key 直接用全局 `tab.id`；已见过的有 sessionId 的终端跨 group 隐藏挂载，
   关闭后从 seen 删除。切整页路由时仍不卸载 TerminalTab；非 active 传 `active=false`，不能发
   1004 focus。文件/TUI 仍只在对应 pane 渲染。
6. 空 pane 有 `BlankTab`，其 base 为 selected base；没有 selected base 时显示“请从左栏选择项目
   或目录”，不能制造一个假的 BaseDir。当前组中的已存在 pane 仍可在没有 selected base 时显示。
7. 日志与错误可见性：入口 drop、成功 place、非法 MIME、满两格退化、关闭失败均使用 `console.debug/warn` 加对象
   上下文（groupId/tabId/base.key/zone），不使用 print。成功打开内容必须可从 pane title/body 看见，
   错误 toast 必须包含“这一列最多两格”或原始创建文件错误。
8. 更新 `TabBar.tsx`/`WorkbenchPage.tsx`/`GroupDivider.tsx` 文件头和导出 props 注释，明确
   “组栏在顶层、pane 不含 tab row、column 无死上限、每列最多两格”。

### 3. 绿测与 Task 验收

红测后运行：

~~~text
cd web && npm test -- --run src/app/workbench/WorkbenchPage.test.tsx src/app/workbench/launchers.test.tsx
cd web && npm run typecheck
~~~

通过条件：顶栏 group 切换改变 layout；pane 内无 tablist；中间替换、左右加列、上下加第二格
准确；第三格退化替换；列数超过 3 仍可加；拖远端目录/任务保留项目、机器、cwd；拖已打开项不
复制；组关闭最后一组回到空组；终端 keep-alive；自定义 launcher 仍通过 BlankTab/`+` 同一
过滤；`⌘D` 仍只响应 meta、不抢 Ctrl+D EOF。Task 完成后将真实输出写入台账。

## Task 4：项目×机器双 peer group、可关闭文件抽屉、项目路由与 Shell 接线

### 文件范围

只改：

- `web/src/app/tree/ProjectTree.tsx`
- `web/src/app/tree/search.ts`
- `web/src/app/tree/search.test.ts`
- `web/src/app/tree/ProjectTree.test.tsx`
- `web/src/app/shell/Shell.tsx`
- `web/src/app/shell/Shell.test.tsx`
- `web/src/app/files/FileTree.tsx`
- `web/src/app/files/FileTree.test.tsx`
- `web/src/app/cards/CardsPage.tsx`
- `web/src/app/cards/CardsPage.test.tsx`

仍复用 `workspaceBase`、`findBaseByKey`、`findBaseOfTask`、`NewWorktreeDialog`、现有 FileTree
目录/文件 API、Cards/Codegraph 页面；不改这些 API 的线格式。

### Interfaces

`ProjectTreeProps` 保留现有所有计数、偏好、注销、编辑和建树回调，并改为以下完整签名；删除
旧的 `onSelectDir`，目录点击统一交给 `onOpenDirectory`：

~~~ts
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
~~~

`FileTreeProps` 在现有六个必填字段上加一个可选关闭回调，精确为：

~~~ts
export interface FileTreeProps {
  base: BaseDir
  taskId: string | null
  onOpenFile: (rel: string) => void
  onOpenTerminal: (rel: string) => void
  revealSupported: boolean | null
  refreshKey?: number
  onClose?: () => void
}
~~~

`filterTree` 改为 `filterTree(tree: ProjectTreeResp, tasks: Task[], rawQuery: string, openedItems: ReadonlyArray<OpenedWorkbenchItem> = []): TreeFilter`；
打开的 terminal/file/TUI 必须参与项目、机器、目录过滤，但不改变既有任务/归属/隐藏偏好的口径。

### 1. 基线、测试范围与左栏/壳 seam 红测

先运行：

~~~text
cd web && npm test -- --run src/app/tree/ProjectTree.test.tsx src/app/tree/search.test.ts src/app/files/FileTree.test.tsx src/app/shell/Shell.test.tsx src/app/cards/CardsPage.test.tsx
~~~

预期为上表 5 files/136 tests 的已通过基线。`CardsPage.test.tsx` 已有现存账本页夹具，本 Task 只在
该夹具中追加 project query 投影断言；先加入以下真实 UI 断言：

~~~tsx
it('机器下有同级任务与目录，目录默认只露主目录，展开后只显示分支名', () => {
  const onOpenDirectory = vi.fn()
  const onOpenDirectoryTerminal = vi.fn()
  const onOpenItem = vi.fn()
  const opened: OpenedWorkbenchItem[] = [{
    tabId: 't1', groupId: 'g1', column: 0, row: 0,
    base: { key: '/w/b2', kind: 'workspace', path: '/w/b2', label: 'feat/b2', projectName: 'handoff', machine: '' },
    content: { kind: 'terminal', seq: 1 }, label: '终端 · feat/b2',
  }]
  const p = props({ onOpenDirectory, onOpenDirectoryTerminal, onOpenItem, openedItems: opened })
  render(<ProjectTree {...p} />)
  expect(screen.getByText('任务')).toBeInTheDocument()
  expect(screen.getByText('目录')).toBeInTheDocument()
  expect(screen.getByText('main')).toBeInTheDocument()
  expect(screen.queryByText('integration/b2-b3')).not.toBeInTheDocument()
  fireEvent.click(screen.getByText('目录'))
  expect(screen.getByText('integration/b2-b3')).toBeInTheDocument()
  fireEvent.click(screen.getByText('integration/b2-b3'))
  expect(onOpenDirectory).toHaveBeenCalledWith(expect.objectContaining({ path: '/w/b2-b3', label: 'integration/b2-b3' }))
  fireEvent.click(screen.getByRole('button', { name: '在此打开终端' }))
  expect(onOpenDirectoryTerminal).toHaveBeenCalledWith(expect.objectContaining({ path: '/w/b2-b3' }))
  fireEvent.click(screen.getByText('终端 · feat/b2'))
  expect(onOpenItem).toHaveBeenCalledWith(opened[0])
})
~~~

上面的 `props` 测试工厂必须把 `openedItems` 及三个回调的默认值补齐；它复用现有
`ProjectTree.test.tsx` 的真实树 fixture，不创建第二套树。

`search.test.ts` 复用该文件既有的 `tree`/`tasks` fixture，并加入这条完整投影断言；它覆盖
打开项自己的 label、文件相对路径、机器与目录祖先链，任何一项不进过滤结果都算失败：

~~~ts
it('打开的文件/终端/TUI 也能把项目、机器和目录祖先带入搜索结果', () => {
  const opened: OpenedWorkbenchItem[] = [
    {
      tabId: 'file-1', groupId: 'g1', column: 0, row: 0,
      base: {
        key: '/srv/n', kind: 'workspace', path: '/srv/n', label: 'main',
        projectName: 'nova', machine: 'devbox',
      },
      content: { kind: 'file', rel: 'src/opened-file.ts' }, label: 'src/opened-file.ts',
    },
    {
      tabId: 'term-1', groupId: 'g1', column: 1, row: 0,
      base: {
        key: '/w/b2-b3', kind: 'workspace', path: '/w/b2-b3', label: 'integration/b2-b3',
        projectName: 'handoff', machine: '',
      },
      content: { kind: 'terminal', seq: 0 }, label: '终端 · opened-terminal',
    },
  ]
  const byFile = filterTree(tree, tasks, 'opened-file', opened)
  expect(byFile.projects[0].name).toBe('nova')
  expect(byFile.projects[0].locations[0].machine).toBe('devbox')
  expect(byFile.projects[0].locations[0].workspaces[0].path).toBe('/srv/n')
  const byTerminal = filterTree(tree, tasks, 'opened-terminal', opened)
  expect(byTerminal.projects[0].name).toBe('handoff')
  expect(byTerminal.projects[0].locations[0].workspaces[0].path).toBe('/w/b2-b3')
})
~~~

Shell seam 必须有一条穿过真实拖放序列化的测试：本机 TUI 已在 group 中，再从左栏另一项目/机器
任务把 `DRAG_TASK_MIME` 与 `DRAG_BASE_MIME` 拖到右列，断言同一个 `wb.groups[0].id` 下两项
分别保留 `projectName` / `machine`，不是只断言 DOM 文案。目录下半边拖放同样必须断言 terminal
Tab 的 base.path 与 `rel`（根目录为缺席或空串的约定）到达 `TerminalTab` 的真实 mock props。

CardsPage URL 初始化的完整代码形状为：

~~~tsx
const [searchParams] = useSearchParams()
const projectFromUrl = searchParams.get('project') ?? ''
const [project, setProject] = useState(projectFromUrl)

useEffect(() => {
  setProject(projectFromUrl)
}, [projectFromUrl])
~~~

测试在 `MemoryRouter initialEntries={['/cards?project=handoff%2Fweb']}` 下断言 `<select aria-label="项目">`
的 value 为 `handoff/web`；空 query 仍为全项目。这个 route 投影必须实际穿过 `URLSearchParams`，
不能只测一个直接传 prop 的 CardsPage。

### 2. 最小实现、结构与日志

1. ProjectTree 维持项目→机器的展开/断开/偏好/排序现状；machine 内按原型增加两个同级 peer
   group：先「任务」，后「目录」，都缩进在 machine 下。任务组内容为该 base 上的 openedItems
   加后端 tasks；同 taskId 的 opened TUI 替代重复的 executor row，terminal/file 以打开项额外
   一行显示。打开项点击调 `onOpenItem`，后端任务点击仍调 `onOpenTask(base,taskId)`。
2. 目录 peer group 单独用 `directoryOpen` 集合，初始收起；收起只渲染 `loc.workspaces[0]`
   （主目录按既有排序取第一），展开平铺所有 workspace，行文案只用 `dirLabel(ws)`，不显示绝对
   路径。组头最右 hover `＋` 调 `onWorktreeCreated` 同一 `NewWorktreeDialog` 路径；目录行 hover
   terminal icon 调 `onOpenDirectoryTerminal(base)`；目录行 draggable 写 `DRAG_DIR_MIME` 的
   `JSON.stringify(base)`；点击调 `onOpenDirectory(base)`。
3. 项目标题行保持展开按钮，但包在一个相对定位 wrapper 中；hover 右端显示“工作项”和“代码图”
   两个不嵌套于 button 的按钮，分别回调 `onOpenProjectCards(project)` 与
   `onOpenProjectCodegraph(project)`。底部全局工作项/代码图按钮仍保留原回调，不能因项目入口而
   消失。项目按钮必须使用 `encodeURIComponent` 的 route 由 Shell 负责。
4. `filterTree` 继续“自身命中或后代命中保留整层”，新增后代是 openedItems 的 base key / label；
   搜项目名或机器名仍保留完整目录/任务，搜打开文件名能展开它所属 project/machine/directory。
   原有隐藏 idle、archived、unassigned、ticket 计数测试全部保留。
5. Shell 用 `const [fileDrawer, setFileDrawer] = useState<BaseDir | null>(null)`；
   `onOpenDirectory` 先 `backToWorkbench()`、`wb.select(base)`、再设置 drawer；工作台仍常驻。右侧
   不再无条件渲染 `wb.base` 的 FileTree，只在 `fileDrawer !== null && !fullPageRoute` 渲染带
   `onClose` 的 FileTree。drawer 中 `onOpenFile` 显式带 `fileDrawer` 调
   `wb.open({ kind:'file', rel }, fileDrawer)`，`onOpenTerminal` 显式带 `fileDrawer` 调
   `wb.openTerminal(fileDrawer, undefined, rel)`；文件点击不自动关闭抽屉，X 才关闭。
6. Shell 将 `wb.openedItems` 按树 key 通过 `findBaseByKey(tree,key)` enrich；找得到的用树上
   最新 branch label/projectName/machine，找不到的保留 Tab 自带 base 但不伪造项目归属。传给
   ProjectTree 的 `selectedKey` 改为 `fileDrawer?.key ?? wb.base?.key ?? null`，保证抽屉路径有
   清楚选中态。
7. `openTaskTui` 改为 `wb.openOrFocus({ kind:'tui', taskId }, target ?? wb.base ?? undefined)`；
   target 非 null 时只为该新 Tab 携带 base，不让全局组因切目录而重建。打开项点击走
   `wb.select(item.base)` + `wb.activate(item.groupId,item.tabId)`，并先 backToWorkbench；不会新增组。
8. 项目路由回调明确选项目：Shell 的 `selectProject(project)` 找第一个可达 main workspace，
   `wb.select(workspaceBase(project, loc.machine, main))`；无 main 时仍导航但不伪造目录。
   `openProjectCards` 导航 `/cards?project=${encodeURIComponent(project.name)}`；
   `openProjectCodegraph` 导航 `/codegraph?project=${encodeURIComponent(project.name)}`。Codegraph
   route 的 project 使用 query 值优先、否则 selected base.projectName，仍只进入现有
   `CodegraphFrame` 的 `encodeURIComponent` query。CardsPage 读取同一 query。
9. `FileTree` 顶部“文件/刷新”旁加入 `onClose` 时才显示的 X，aria-label 为“关闭文件抽屉”；
   默认测试不传 onClose 时不改变既有右栏语义。文件树所有既有目录操作、错误原文、Finder 三态
   不变。抽屉根外观沿原型 260px、border-left、独立滚动，但尺寸由 Shell layout 控制。
10. 关键入口按 Web 现有结构化 console 约定记录：目录 drawer 打开/关闭、项目 route 导航、
    task/tab focus、directory/task drop、FileTree API 错误均带 `{ project, machine, baseKey,
    tabId, groupId, path }` 中适用字段；成功动作有可见 pane/drawer/route 结果，错误透传原文。
    新/改导出函数与 Shell 回调都补参数、返回、跨机注意事项注释；非显然的“目录点击不再只选中，
    而是 select + drawer”写为什么。

### 3. 绿测与 Task 验收

红测后运行：

~~~text
cd web && npm test -- --run src/app/tree/ProjectTree.test.tsx src/app/tree/search.test.ts src/app/files/FileTree.test.tsx src/app/shell/Shell.test.tsx src/app/cards/CardsPage.test.tsx
cd web && npm run typecheck
~~~

行为验收：

- 项目→机器→任务/目录的层级与缩进符合原型；目录默认一行、展开平铺 branch；任务/目录 hover
  行为和真实 BaseDir 对齐。
- 左栏已打开任务只 focus 旧 cell；未打开任务 openOrFocus 新 group；不同项目/机器可以同组并排。
- 目录 icon 创建 cwd 正确的 terminal；目录拖到底部得到同列第二格且最多两格；路径点击只打开
  可关闭 FileTree 抽屉，固定右栏消失；整页路由覆盖时不显示 drawer，但回到 `/` 可恢复且终端
  未卸载。
- 项目 hover 工作项/代码图选择对应项目；`project` 含斜杠、空格、中文时 URL 与 Cards select/
  Codegraph iframe query 解码正确；无主目录项目不崩且不假选。
- 本机与远端 machine/base/path 始终分开；HomeDock、scratch、Finder、任务未归属和旧底部入口
  无回归。

## 2. 五项强制自审

### 2.1 缺陷族对抗审查

| 缺陷族 | 设问 | 本计划锁法 |
|---|---|---|
| 生命周期/状态机中断 | 切组、切目录、整页路由、拖放、关闭确认期间是否卸载/错写？ | Tab 自带 base；hook 只保存一个全局 wb；tabId/groupId 反查；TerminalTab seen keep-alive；Shell fullPageCover 继续常驻；restore/sync 先双请求后 hydrate，失败 ready 闸门关闭。 |
| 静默失败/误导报错 | 坏 payload、无 PTY、旧行、API 错误、满两格是否能解释？ | decode null + dropped/legacy；sync warn 带 key；PTY 原有三态不改；两格退化有 toast；FileTree/建树/API 原文透传；成功 focus/drawer/route 有可见结果。 |
| 跨平台假设 | pointer/drag/URL/desktop shell 是否拿一个浏览器绿推广？ | jsdom helper 钉 clientX/clientY 非零尺寸；真机清单分别覆盖 Chromium、WKWebView/Wails 桌面壳；只响应 meta 的 ⌘D，保留 Ctrl+D EOF；`URLSearchParams` 不拼裸 query。 |
| 假红/假绿 | 测试是否只测 helper 或旧按目录模型？ | seam 测试通过真实 `useWorkbench`→`WorkbenchPage`/`Shell`；DataTransfer JSON 穿过真实 drop handler；跨项目同组反例；mutation 将 `Tab.base` 改回按目录 Map 时必须使该测试失败。 |
| 门禁绕过 | 是否借 route、MIME、home/scratch 或后端新接口绕开边界？ | 只接受自有 MIME；home 仍 HomeDock；scratch 不进入 ProjectTree；不改 agentd/client endpoint；项目路由先 selectProject 再导航，不能只靠 iframe query 假装已选。 |
| 序列化边界 | Tab/base/column/group/optional 值是否在每一处 map/JSON/query 保真？ | `encodeWorkbench↔decodeWorkbench` 真实 roundtrip；sync sentinel raw payload；DataTransfer base JSON；baseOfSession；`CardsPage` URLSearchParams；每处缺失/零值断言。 |
| 新枚举白名单 | 新 `top/bottom`、drag MIME、source kind 是否漏进 switch/校验？ | `DropZone` 五值集中定义，`dropZoneAt` 与 WorkbenchPage 全分支测试；`WorkbenchSource` 两值集中解析；persist 白名单只允许既有 TabContent kind；不新增 agentd 枚举。 |

### 2.2 序列化/投影边界清单

1. `tabs.ts` Tab/BaseDir → `persist.ts#stripWorkbench` → `encodeWorkbench` JSON → `decodeWorkbench`：
   base key/path/project/machine、group/column/focus/sizes、terminal rel/session/launcher、file optional
   draft/baseSha 均逐项断言；`undefined` 与 `0`/`''` 不混淆。
2. `useWorkbenchSync.ts` `Workbench` → `{ [GLOBAL_WORKBENCH_KEY]: raw }` → `putWorkbenchBase(baseKey,payload)`；
   测试 mock 只验证真实调用参数，同时 `WorkbenchStateResp.bases[].payload` 仍是字符串；旧 rows
   在 `diffPayloads` 后逐个 null 删除。
3. `restore.ts#baseOfSession` `PtySession` → BaseDir → orphan Tab；测试同一 path 在空 machine
   与 `linux-01` 产生不同 key，home 不进入 global。
4. `ProjectTree.tsx` BaseDir → `JSON.stringify`/`DRAG_DIR_MIME` → `WorkbenchPage` `readDragBase`；
   Shell 的 task/base MIME 与 pane 的 source 同样有真实 drop 回归，不能只各测 producer/consumer。
5. `ProjectTree.tsx` ProjectNode → project route callback → `encodeURIComponent` → CardsPage
   `URLSearchParams` / CodegraphFrame query；使用含 `/`、空格、中文的值穿边界断言。
6. `openedWorkbenchItems` Tab → Shell 通过 `findBaseByKey` enrich → ProjectTree 行；树找不到时不
   伪造项目名，且不把 unknown item 误归本机。

### 2.3 上下文预算与文件边界

每个 Task 文件集有界：Task 1 为 4 个文件、Task 2 为 8 个文件、Task 3 为 5 个文件、Task 4 为
10 个文件；没有需要另插竖切的无界包。`d_web_workbench` 的 codegraph best 领域实际有 8 个
容器错挂在 `d_web`，`fociTruncated.total=10 shown=5` 且实体 projection 未扫描；这只是图覆盖债，
不能拿图结果证明无调用方。已用源码和 `rg` 明确列出所有受影响消费者，实施者若发现新增消费者必须
先追加台账并扩大有界文件清单，不得默默越界。

### 2.4 类型标注与真机清单

边界型子系统的实现完成后必须逐项报告真实结果（本计划不预写 pass）：

- `tabs.ts/useWorkbench`：opened TUI focus、未打开 task 新 group、不同 project/machine 同组、
  center replace、left/right column、top/bottom 2-pane、第三格退化、close/group/focus/persist。
- `ProjectTree`：项目/机器/任务/目录层级；目录折叠、branch-only、hover plus、terminal icon、
  directory drag cwd；已打开项 focus；远端 machine 不串。
- `Shell`：本机 TUI + linux-01 terminal 同组；切 selected base 不切 layout；drawer open/close；
  `/cards?project=` 与 `/codegraph?project=` 选项目；full-page cover 下 TerminalTab 不卸载。
- 浏览器/桌面壳：Chromium 与 WKWebView/Wails 各实拖 HTML5 DataTransfer；窄宽度横向 overflow
  仍可访问；pointer capture 分隔条、键盘方向键、⌘D/Ctrl+D、Escape/关闭抽屉；中文/空格/斜杠
  route query；无真实 PTY 时记录 UI 可见的 501/能力位文案。

### 2.5 接缝双向矩阵

唯一法定 seam 是 `tabs.ts/useWorkbench` 的布局模型，调用方为 `WorkbenchPage`、`ProjectTree`
拖放与 `Shell`。测试→缝的入口与缝→测试一一对应：

| seam 行为 | 测试入口（必须穿过的真实符号） |
|---|---|
| 已打开 focus / 未打开新组 | `useWorkbench().openOrFocus`，`ProjectTree` 点击回调，`WorkbenchPage` 渲染真实 pane；`useWorkbench.test.ts`、`Shell.test.tsx` |
| 中间替换 / 左右加列 | `WorkbenchPage` pane `onDrop` → `api.place` → `placeSource`；`WorkbenchPage.test.tsx` |
| 上下最多两格 | 同一 pane `onDrop` 的 y 坐标 → `dropZoneAt` → `api.place`；`WorkbenchPage.test.tsx` |
| 跨项目同组 | Shell/WorkbenchPage 真实 DataTransfer task/dir MIME → `place`；`useWorkbench.test.ts`、`Shell.test.tsx` |
| directory cwd | ProjectTree `onDragStart` JSON → WorkbenchPage `readDragBase` → terminal Tab；`ProjectTree.test.tsx`、`WorkbenchPage.test.tsx` |
| 持久化往返 | `useWorkbenchSync`真实 hydrate/flush + `encodeWorkbench/decodeWorkbench`；`persist.test.ts`、`restore.test.ts`、`useWorkbenchSync.test.ts` |

每条缝都有测试；每支列出的测试入口都穿过缝。`dropZoneAt` 单独的坐标边界测试属于附加内部锁，
唯一合法理由是坐标投影无法从纯布局声明构造，且不顶替上述真实 drop 测试。

## 3. 用户故事归属与最终门禁

1. 本机 handoff TUI + linux-01 另一项目终端同组并排：Task 1 跨项目模型、Task 3 pane drop、Task 4 Shell/ProjectTree DataTransfer 回归。
2. 点击已打开任务只跳原格：Task 2 `openOrFocus`、Task 4 openedItems focus、Shell 回归。
3. 工作树拖下半边产生同列第二格且终端 cwd 正确：Task 1 two-pane invariant、Task 3 四向 drop、Task 4 directory MIME/TerminalTab props。
4. 目录组 hover plus 走既有 NewWorktreeDialog：Task 4 ProjectTree peer group 与既有 dialog 回调。
5. 项目旁工作项/代码图进入目标页且项目已选中：Task 4 `selectProject`、CardsPage query、CodegraphFrame query。

最终门禁由协调者执行，不派发：

~~~text
cd web && npm test -- --run src/app/workbench/tabs.test.ts src/app/workbench/paneDrop.test.ts src/app/workbench/persist.test.ts src/app/workbench/restore.test.ts src/app/workbench/useWorkbench.test.ts src/app/workbench/useWorkbenchSync.test.ts src/app/workbench/WorkbenchPage.test.tsx src/app/workbench/launchers.test.tsx
cd web && npm test -- --run src/app/tree/ProjectTree.test.tsx src/app/tree/search.test.ts src/app/files/FileTree.test.tsx src/app/shell/Shell.test.tsx src/app/cards/CardsPage.test.tsx
cd web && npm run typecheck
cd web && npm run build
git diff --check
rg -n 'byBase|baseDirs|MAX_GROUPS|\.tabs\]|\.activeId|sizes: \[1\]' web/src/app/workbench web/src/app/shell web/src/app/tree web/src/app/files
~~~

最后一条只作残留旧模型审查；若命中，必须逐项确认是测试迁移残留还是实现残留，不能以计数达标
代替行为判据。协调者还要实测至少一条完整浏览器拖放链和一条桌面壳/窄宽度链，并把原始命令、
红测、绿测、浏览器结果追加台账。只有所有测试/构建实际退出 0、无未解释旧模型、计划文件和
台账都已提交，才可判定本卡实现完成；本计划节点自身只提交计划与台账。

## 4. 占位符扫描与复用 harness 声明

本计划不含待决策问题，不使用 TBD、TODO、“适当的错误处理”或“同 Task N”替代实现内容；
所有跨 Task 接口、参数、返回和关键数据形状均已写出。测试代码复用例外仅限现有测试夹具形态：

- `tabs.test.ts` / `useWorkbench.test.ts` 的 `BaseDir` 与 `renderHook(() => useWorkbench())` fixture；
- `WorkbenchPage.test.tsx` 的 `dt`、`dropAt`、`layout`、`api` JSX harness，需按本计划新增 MIME/坐标字段；
- `ProjectTree.test.tsx` 的 `props`/项目树 fixture；
- `FileTree.test.tsx` 的 `renderTree` 与 API mock；
- `Shell.test.tsx` 的 `renderShell`、Router、Terminal/File/TUI mock；
- `CardsPage.test.tsx` 的 MemoryRouter 与账本/任务 mock。

例外不是骨架测试：每个复用 harness 的断言已逐条列明——全局同组、focus/new group、两格上限、
四向 drop、directory cwd、global payload roundtrip、坏/legacy fallback、drawer close、project
route query、full-page keep-alive。不得另造只测私有 helper 的内部锁替代这些入口。

本节点未调用 handoff CLI、未派发 executor、未起新的 executor/子任务；实现者完成后由协调者按
最终门禁审核并提交。
