# B283 实现计划：悬浮窗终端 tab 每次打开累积——收编判据与误判死亡根治

> 读者：对 handoff 仓零上下文的执行者。所有行号按工作树 HEAD `91680a494`（分支 `fix/B283-float-terminal-dup`）；动手前先核对符号存在，行号漂了以符号为准。
> 卡 B283 · L2 · spec `docs/superpowers/specs/b283.md`（r1，已批准）。
> 台账：`docs/superpowers/specs/b283-ledger.md`（plan 节点行已含基线跑测原始输出与本计划的全部关键决定）。
> 红线：本计划只改 `web/src/app/workbench/restore.ts`、`web/src/app/workbench/useWorkbenchSync.ts`、`web/src/app/workbench/restore.test.ts`、`web/src/app/shell/Shell.tsx`、`web/src/app/workbench/TerminalTab.tsx`，并删除 `web/src/app/workbench/b283-redloop.test.ts`（转正）。`persist.ts`、`dockPersist.ts`、`useHomeDock.ts`、`types.ts` 一字不动；不 bump `DOCK_PERSIST_VERSION`；不碰 Go。

## 0. 基线事实（所有 task 共享，动手前不必重跑，除非怀疑环境变了）

判据基线（2026-08-28，本计划出稿人在本工作树 `web/` 下亲手跑，原始输出已记台账）：

- `npx vitest run src/app/workbench/b283-redloop.test.ts` → **1 failed**，失败行：
  `AssertionError: expected [ { id: 'h1', …(4) }, …(1) ] to have a length of 1 but got 2`（`b283-redloop.test.ts:58`）。
  这就是本卡要转绿的红色回路：打开 N 剥引用 → 打开 N+1 把活会话当孤儿收编 → tab 1→2。
- `npx vitest run src/app/workbench src/app/homedock` → Test Files **1 failed | 22 passed (23)**；Tests **1 failed | 358 passed (359)**。唯一 failed 就是红色回路。restore.test.ts（14 条）、persist.test.ts、dockPersist.test.ts、useWorkbenchSync.test.ts 等全部绿——这是每个 task「跑红/跑绿」步骤的基线参照：除本计划显式标注「基线预期绿」的新测试外，任何一条既有测试翻红都先停下查原因，不许顺手改断言。

服务端事实（判据出处，出稿人亲手读码复核）：

- `internal/agentd/pty_api.go:186-241`：`ptySessionsAll` 构造 `Machines` 恒以 `{Name:"", Ok:true}` 领衔（189 行）；远端扇出失败只写该机器行的 `Error`（`Ok` 缺省 false），HTTP 仍 200，该机器的会话整体缺席名单。会话行的 machine 戳与 machines 行在同一循环盖章（233-237）。
- 前端类型：`PtySessionsResp.machines?: MachineStatus[]`（`web/src/api/types.ts:728-731`，可选字段）；`MachineStatus { name: string; ok: boolean; fetched_at: string; error: string }` 四字段全必填（`types.ts:173-178`）；`WorkbenchStateResp { selected: string; dock: string; bases: WorkbenchBaseRow[] }`（`types.ts:107-111`）。
- 悬浮窗 tab 的 `machine` 字段由 `decodeDock` 强制为 string（`dockPersist.ts:97`）；修复后悬浮窗终端 tab 的 machine 恒为空串（新建 `newTerminal()` 不带机器、收编仅限本机），真机快照里的 `machine:"mac-02"` tab 只能来自修复前的 ③ 收编。
- vitest 用 esbuild 转译、**不做类型检查**：红测阶段引用尚未存在的 `machines` / `purged` 字段能正常跑出运行时红，不会卡在编译错误；`npm run typecheck`（tsc -b）在 task 收口时必须绿。

图覆盖债：本节点未跑 codegraph——核对对象全是 spec/审查点到行的引用，直接读码 + 跑测复核（照 b286 先例记债）。执行者同样不需要查图：本计划的文件集就是下面五个 task 圈定的这几个。

测试范围约定：每个 task 只跑自己声明的测试；全量测试不属于任何单个 task（implement 三段律管）。

## Task DAG

```
Task 1（machines 入参 + 中央区门控）
  └→ Task 2（home 收编仅限本机）        [同文件次序约束]
       └→ Task 3（悬浮窗门控 + 存量清除） [依赖 Task 1 的 machineOkSet、Task 2 的收编门控]
            └→ Task 4（红色回路转正）     [1+2+3 全部落地后才可能绿]
Task 5（话术订正）                       [独立，可与 Task 1–4 任意并行，不同文件无接缝]
```

次序承重：Task 3 的清除用例断言 `adopted === 0` 与 `tabs 长 0`，两者都要求方案 1（收编门控）已在位——所以方案 1（Task 2）必须先于清除（Task 3）。

没有需要驱动派发系统自身的验收步骤，无需标注「本 task 由协调者执行」。

---

## Task 1：machines 入参 + 门控表 + 中央区 prune 门控

覆盖 spec 方案 3 的「入参 + 中央区半边」。落点：`restore.ts`（RestoreInput、machineOkSet、① 循环门控）+ `useWorkbenchSync.ts`（把 `sessResp.machines` 传进来）+ `restore.test.ts`（门控三例）。

**文件集**：`web/src/app/workbench/restore.ts`、`web/src/app/workbench/useWorkbenchSync.ts`、`web/src/app/workbench/restore.test.ts`。

### Interfaces

Consumes（既有，不改）：

- `pruneDeadSessions(wb: Workbench, liveIds: Set<string>): Workbench`（`persist.ts:203`，导出）。
- `MachineStatus { name: string; ok: boolean; fetched_at: string; error: string }`（`types.ts:173-178`）。
- `PtySessionsResp.machines?: MachineStatus[]`（`types.ts:730`）；`fetchPtySessions(scope: string)` 已在 `useWorkbenchSync.ts:100` 被调用。

Produces：

- `RestoreInput` 新增可选字段 `machines?: MachineStatus[]`——唯一生产调用方 `useWorkbenchSync.ts:105` 的调用面随之加一行。
- `machineOkSet(machines: MachineStatus[] | undefined): Set<string>`——**模块私有**（不导出），只服务 buildRestore 内两处门控（本 task 用中央区一处，Task 3 用悬浮窗一处）。

### 步骤

1. **判据先在基线跑**（已由出稿人完成，记录于台账；执行者动手前复核这一条即可）：`npx vitest run src/app/workbench/restore.test.ts src/app/workbench/useWorkbenchSync.test.ts` 基线全绿（358 passed 的一部分）。
2. **（红）在 restore.test.ts 加共享夹具与门控测试**。先在文件头部 import 区加两处（其余 import 不动）：

```ts
import type { MachineStatus, PtySession, WorkbenchStateResp } from '../../api/types'
import type { HomeTab } from '../homedock/useHomeDock'
```

（第一行原为 `import type { PtySession, WorkbenchStateResp } from '../../api/types'`，只增 `MachineStatus`。）

在 `const VIEW = ...` 之前加四个夹具（Task 2/3/4 复用）：

```ts
const baseM: BaseDir = {
  key: '/repo/m@mac-02',
  kind: 'workspace',
  path: '/repo/m',
  label: 'm',
  projectName: 'p',
  machine: 'mac-02',
}

// machine 造一条扇出应答行。MachineStatus 四个字段全必填（types.ts:173-178）。
function machine(name: string, ok: boolean): MachineStatus {
  return { name, ok, fetched_at: '2026-08-28T00:00:00Z', error: '' }
}

// homeSess 造一条 home 会话；machineName 缺省 = 本机。
function homeSess(id: string, machineName = ''): PtySession {
  return sess(id, { machine: machineName, base_kind: 'home', base_path: '' })
}

// dockRaw 把一组悬浮窗 tab 编成落盘 payload；activeId / windowOpen 可显式指定。
function dockRaw(tabs: HomeTab[], activeId: string | null = tabs[0]?.id ?? null, windowOpen = false): string {
  return encodeDock({ tabs, activeId, windowOpen, geom: { x: 10, y: 10, w: 620, h: 340 }, maximized: false })
}
```

在 `describe('buildRestore', ...)` 内追加三条用例：

```ts
  it('机器扇出 ok 时本行死会话照常剥——门控只挡「没答上来」，不挡真死', () => {
    const r = buildRestore({
      state: state({ bases: [{ base_key: baseM.key, payload: encodeBase(baseM, wbWith('S1')), updated_at: 1 }] }),
      sessions: [sess('S1', { machine: 'mac-02', base_path: '/repo/m', exit_code: 0 })],
      machines: [machine('', true), machine('mac-02', true)],
      ...VIEW,
    })
    expect(r.entries[0].wb.groups[0].tabs[0].content).toEqual({ kind: 'terminal', seq: 1 })
    expect(r.pruned).toBe(1)
  })

  it('机器扇出没答上来（ok=false）时它名下基准行的引用不剥——缺席不判死', () => {
    const r = buildRestore({
      state: state({ bases: [{ base_key: baseM.key, payload: encodeBase(baseM, wbWith('S1')), updated_at: 1 }] }),
      sessions: [], // mac-02 没答上来：S1 可能活着，只是没进名单
      machines: [machine('', true), machine('mac-02', false)],
      ...VIEW,
    })
    expect(r.entries[0].wb.groups[0].tabs[0].content).toEqual({ kind: 'terminal', seq: 1, sessionId: 'S1' })
    expect(r.pruned).toBe(0)
  })

  it('machines 整个缺席时同样保守：远端基准行的引用不剥', () => {
    const r = buildRestore({
      state: state({ bases: [{ base_key: baseM.key, payload: encodeBase(baseM, wbWith('S1')), updated_at: 1 }] }),
      sessions: [],
      ...VIEW,
    })
    expect(r.entries[0].wb.groups[0].tabs[0].content).toEqual({ kind: 'terminal', seq: 1, sessionId: 'S1' })
    expect(r.pruned).toBe(0)
  })
```

跑 `npx vitest run src/app/workbench/restore.test.ts`，基线颜色预期：第一条**绿**（基线没有门控，剥引用本来就发生；它是回归锁，锁门控不许过度拦截——写下去就绿，按最薄路径条豁免红绿）；第二、三条**红**（基线把 S1 剥掉，断言 `sessionId: 'S1'` 拿到 undefined；第三条对基线等价于第二——基线根本不认识 machines 字段）。其余 14 条既有用例必须仍绿。

3. **（绿）最小实现**。restore.ts 四处：

(a) import 行改为（见步骤 2 开头的形状）：

```ts
import type { MachineStatus, PtySession, WorkbenchStateResp } from '../../api/types'
```

(b) `RestoreInput` 加 `machines` 字段（整个 interface 替换为）：

```ts
// RestoreInput 是合成恢复结果所需的全部输入。
export interface RestoreInput {
  state: WorkbenchStateResp
  sessions: PtySession[]
  // scope=all 扇出的 machines 行（PtySessionsResp.machines，types.ts:730）。方案3 的
  // 门控数据：某台机器的行缺席或 ok=false 时，它名下的会话引用不剥——扇出缺席 ≠ 会话
  // 死亡。整个字段缺席（undefined）按空表处理。本机（machine===''）恒 ok：本机行由
  // 汇总端点恒以 ok=true 领衔（internal/agentd/pty_api.go:189），本机会话名单从不缺席。
  machines?: MachineStatus[]
  // 视口宽高与顶部让位，用于夹紧悬浮窗几何。由调用方读 window 后传进来
  vw: number
  vh: number
  inset: number
}
```

(c) 在 `liveSessionIds` 函数之后、`collectUsedSessionIds` 之前加私有函数：

```ts
// machineOkSet 把扇出应答折成「本次答上来了」的机器名集合——方案3 两处 prune 的门控表。
//
// 参数：machines 是 PtySessionsResp.machines；undefined 按空表处理。
// 返回：ok=true 的机器名集合；本机（machine===''）无条件在内——本机行由
// internal/agentd/pty_api.go:189 恒以 {Name:"", Ok:true} 领衔返回，本机会话名单从不
// 缺席，门控对本机没有存在意义；集合里查不到的机器一律按「没答上来」处理（保守：
// 宁可留一个连不上可显式重开的 tab，不静默造孤儿）。
// 注意：不导出——它只服务 buildRestore 内的两处门控，外面没有第二个消费者。
function machineOkSet(machines: MachineStatus[] | undefined): Set<string> {
  const ok = new Set<string>([''])
  for (const m of machines ?? []) {
    if (m.ok) ok.add(m.name)
  }
  return ok
}
```

(d) `buildRestore` 开头加一行，① 循环里 prune 调用按门控跳过（只列触及的两段，其余不动）：

```ts
export function buildRestore(input: RestoreInput): RestoreResult {
  const live = liveSessionIds(input.sessions)
  const incompatible = incompatibleSessionIds(input.sessions)
  const machineOk = machineOkSet(input.machines)
```

```ts
    const before = countTerminalsWithSession(decoded.wb)
    // 方案3（中央区侧）：本行基准的机器这次扇出没答上来时，不做死亡判决——它名下
    // 的会话可能活着只是没进名单。跳过 prune 保住引用，机器回来照常接上；真死了走
    // 挂载时的连接错误 / 1008 出口，两条路都有「重开」终态，不会静默自建新 shell。
    // 归属按 base 行的 machine 取：TabContent 没有 machine 字段，被判死的会话也
    // 不在会话行里，base 行是唯一可靠来源。
    const wb = machineOk.has(decoded.base.machine) ? pruneDeadSessions(decoded.wb, live) : decoded.wb
    pruned += before - countTerminalsWithSession(wb)
```

4. **useWorkbenchSync 传出 machines**（`useWorkbenchSync.ts:105` 的 buildRestore 调用加一行）：

```ts
          const r = buildRestore({
            state,
            sessions: sessResp.sessions,
            machines: sessResp.machines,
            vw: vw > 0 ? vw : 1280,
            vh: vh > 0 ? vh : 800,
            inset: topInset(),
          })
```

5. **测试范围声明**：只跑 `npx vitest run src/app/workbench/restore.test.ts src/app/workbench/useWorkbenchSync.test.ts`。预期：restore.test.ts 17 条全绿（14 旧 + 3 新），useWorkbenchSync.test.ts 全绿（它 mock 的 `{ sessions: [] }` 不带 machines 字段，`machines: sessResp.machines` 传 undefined 合法）。红绿模板到第 5 步收口。
6. **日志步骤**：本 task 不加新日志——machines 是既有扇出响应的只读投影，无成功/失败分支；统计量（purged）在 Task 3 落地时随 console.debug 一起加。
7. **注释步骤**：machineOkSet 头注、RestoreInput.machines 字段注、① 循环门控行的「为什么」注——全部已写进上面的代码块，落地照抄，不许删。

---

## Task 2：home 收编仅限本机

覆盖 spec 方案 1。落点：`restore.ts` ③ 循环 + `restore.test.ts` 一条用例。

**文件集**：`web/src/app/workbench/restore.ts`、`web/src/app/workbench/restore.test.ts`。

### Interfaces

Consumes（既有，不改）：`baseOfSession(s: PtySession): BaseDir`（`restore.ts:38`，导出；其 home@machine 分类分支保留不动——spec 备注明说它分类的是 wire 事实，roadmap 的「显式远程 home 入口」直接复用）。

Produces：无新符号。③ 循环内加一行守卫。

### 步骤

1. **判据先在基线跑**（出稿人已跑，台账有记录）：restore.test.ts 基线含两条 home 收编既有用例（「home 会话不进工作台，落到悬浮窗」「没有落盘的悬浮窗现场时，孤儿 home 会话走 dockOrphans」），**都是本机会话（machine=''），Task 2 落地后必须仍绿**——这是「只挡外来、不误伤本机」的既有锁。
2. **（红）在 restore.test.ts 的 `describe('buildRestore', ...)` 内追加**：

```ts
  it('home 收编仅限本机：外来机器的 home 会话不进悬浮窗，本机的照常收', () => {
    const r = buildRestore({
      state: state({ dock: dockRaw([], null, false) }),
      sessions: [homeSess('H1', 'mac-02'), homeSess('H2')],
      machines: [machine('', true), machine('mac-02', true)],
      ...VIEW,
    })
    expect(r.dock).not.toBeNull()
    expect(r.dock!.tabs).toHaveLength(1)
    expect(r.dock!.tabs[0].sessionId).toBe('H2')
    expect(r.adopted).toBe(1)
    expect(r.dockOrphans).toHaveLength(0)
  })
```

基线颜色预期：**红**（基线把 H1、H2 都收编进 dock.tabs，长 2、adopted 2）。

3. **（绿）最小实现**。③ 循环替换为（守卫行 + 注释是新增，其余逐字保留原样）：

```ts
  for (const s of input.sessions) {
    if (!live.has(s.id) || used.has(s.id)) continue
    // 方案1：悬浮窗是本机面。外来机器的 home 会话归它自己机器的悬浮窗管，不收编
    // ——跨机收编正是 B283「tab 只增不减」的放大器。中央区（workspace）会话的
    // 收编语义不变（2026-08-20 状态同步 spec 的既有决定）。baseOfSession 的
    // home@machine 分类分支保留不动：它分类的是 wire 事实，将来「显式远程 home
    // 入口」（roadmap）直接复用。
    if (s.base_kind === 'home' && s.machine !== '') continue
    adopted++
    const b = baseOfSession(s)
    if (b.kind === 'home') {
      // 孤儿 home 会话的 tab id 直接用 sessionId：与 Shell 里 dock.adopt 的既有
      // 调用一致，且天然唯一。它不是 h<n> 形状，不参与 useHomeDock 的计数器播种
      const tab: HomeTab = { id: s.id, kind: 'terminal', seq: ++dockSeq, sessionId: s.id, machine: s.machine }
      if (s.incompatible) tab.incompatible = true
      if (dock === null) dockOrphans.push(tab)
      else dock.tabs = [...dock.tabs, tab]
      continue
    }
    const found = entries.find((e) => e.base.key === b.key)
    if (found) {
      found.wb = openTab(found.wb, orphanTerminal(nextTerminalSeq(found.wb), s))
    } else {
      entries.push({ base: b, wb: openTab(EMPTY_WORKBENCH, orphanTerminal(1, s)) })
    }
  }
```

（`adopted++` 留在守卫之后：被跳过的外来 home 会话不算收编。）

4. **测试范围声明**：只跑 `npx vitest run src/app/workbench/restore.test.ts`。预期 18 条全绿（17 + 1 新；两条既有本机收编用例仍绿）。红绿模板收口。
5. **日志步骤**：不加——被跳过的外来 home 会话是「按设计不收」，成功路径的 adopted 计数（既有日志键「补进来的孤儿会话」）只反映真实收编；是否加「跳过」计数已在台账记为不做（debug 级日志对 acceptance 无增量价值）。
6. **注释步骤**：守卫行的四行「为什么」注释已写进代码块，照抄。

---

## Task 3：悬浮窗 prune 门控 + 存量外来 tab 一次性清除

覆盖 spec 方案 2 全部 + 方案 3 的悬浮窗半边。落点：`restore.ts` ② 区 + `RestoreResult.purged` + `useWorkbenchSync.ts` 日志行 + `restore.test.ts` 三条用例。

**文件集**：`web/src/app/workbench/restore.ts`、`web/src/app/workbench/useWorkbenchSync.ts`、`web/src/app/workbench/restore.test.ts`。

### Interfaces

Consumes（既有，不改）：

- `decodeDock(raw: string): DockSnapshot | null`、`pruneDeadDockSessions(tabs: HomeTab[], liveIds: Set<string>): HomeTab[]`、`markIncompatibleDockTabs(tabs: HomeTab[], ids: Set<string>): HomeTab[]`、`clampGeom(g: Geom, vw: number, vh: number, inset: number): Geom`、`DockSnapshot { tabs; activeId: string | null; windowOpen: boolean; geom; maximized }`（均在 `dockPersist.ts`，签名不动）。
- Task 1 的 `machineOk: Set<string>`（buildRestore 局部变量）与 Task 2 的收编门控（清除用例的 `adopted === 0` 断言依赖它）。

Produces：

- `RestoreResult` 新增字段 `purged: number`（方案2 清除的外来 tab 计数；in-process 统计量，不落任何序列化边界）。
- dock 侧门控不新增任何导出符号。

### 步骤

1. **判据先在基线跑**（出稿人已跑，台账有记录）：`npx vitest run src/app/homedock/dockPersist.test.ts` 基线全绿——其 `pruneDeadDockSessions` 直测两条是回归锁（本 task 不改该函数）。
2. **（红）在 restore.test.ts 的 `describe('buildRestore', ...)` 内追加三条**：

```ts
  it('悬浮窗本地 tab 的死会话照常剥引用——门控不挡真死', () => {
    const r = buildRestore({
      state: state({ dock: dockRaw([{ id: 'h1', kind: 'terminal', seq: 1, machine: '', sessionId: 'H1' }], 'h1', true) }),
      sessions: [{ ...homeSess('H1'), exit_code: 0 }],
      machines: [machine('', true)],
      ...VIEW,
    })
    expect(r.dock!.tabs[0].sessionId).toBeUndefined()
    expect(r.dock!.activeId).toBe('h1') // tab 留位，激活不变
    expect(r.pruned).toBe(1)
  })

  it('存量外来 tab 一次性清除：全外来快照清空后 tabs 为空、activeId 为 null、windowOpen 收为 false', () => {
    const r = buildRestore({
      state: state({
        dock: dockRaw(
          [
            { id: 'u1', kind: 'terminal', seq: 1, machine: 'mac-02', sessionId: 'H1' },
            { id: 'u2', kind: 'file', seq: 2, machine: 'linux-01', rel: 'notes.md' },
          ],
          'u1',
          true,
        ),
      }),
      sessions: [homeSess('H1', 'mac-02')], // 它还活着，也照样清——悬浮窗是本机面
      machines: [machine('', true), machine('mac-02', true), machine('linux-01', true)],
      ...VIEW,
    })
    expect(r.dock).not.toBeNull()
    expect(r.dock!.tabs).toHaveLength(0)
    expect(r.dock!.activeId).toBeNull()
    expect(r.dock!.windowOpen).toBe(false)
    expect(r.purged).toBe(2)
    // 清除是整个 tab 走，不是剥引用留壳：活着的 H1 也不计入 prune
    expect(r.pruned).toBe(0)
    // 活着的外来 home 会话不再被收编回来（方案1，Task 2 已落地）
    expect(r.adopted).toBe(0)
  })

  it('清除命中 activeId 时显式置 null，既有兜底把它重指到剩下的第一个 tab', () => {
    const r = buildRestore({
      state: state({
        dock: dockRaw(
          [
            { id: 'u1', kind: 'terminal', seq: 1, machine: 'mac-02', sessionId: 'H1' },
            { id: 'h2', kind: 'terminal', seq: 2, machine: '', sessionId: 'H2' },
          ],
          'u1',
          true,
        ),
      }),
      sessions: [homeSess('H2')],
      machines: [machine('', true), machine('mac-02', true)],
      ...VIEW,
    })
    expect(r.dock!.tabs).toHaveLength(1)
    expect(r.dock!.tabs[0].id).toBe('h2')
    expect(r.dock!.activeId).toBe('h2')
    expect(r.purged).toBe(1)
    expect(r.dock!.windowOpen).toBe(true) // 没清空就不收窗
  })
```

基线颜色预期：第一条**绿**（基线无门控无清除，死引用本来就剥——回归锁，豁免红绿）；第二条**红**（基线 tabs 长 2、activeId 'u1'、windowOpen true、purged undefined、pruned 1、adopted 1——多处断言不中）；第三条**红**（基线 u1 只被剥成壳留在原地，tabs 长 2）。既有 18 条（Task 2 后）必须仍绿。

3. **（绿）最小实现**。restore.ts 四处：

(a) `RestoreResult` 加 `purged` 字段（整个 interface 替换为）：

```ts
// RestoreResult 是可以直接灌进两个 hook 的恢复结果。
export interface RestoreResult {
  entries: Array<{ base: BaseDir; wb: Workbench }>
  // dock 为 null = 没有可用的落盘现场（从没存过，或存的那份是坏数据）。
  // 此时**不该** hydrate 悬浮窗，让它保持自己的默认几何。
  dock: DockSnapshot | null
  // dockOrphans 只在 dock 为 null 时非空：这些孤儿 home 会话要走 adopt
  //（不开窗、不改几何）。dock 非 null 时它们已被并进 dock.tabs，这里恒为空数组
  dockOrphans: HomeTab[]
  selected: string
  // 下面四个是给日志用的统计量，不参与渲染
  dropped: string[]
  pruned: number
  // purged 是方案2 清掉的外来悬浮窗 tab 数（终端与文件都算）。升级后首启它通常
  // 一次性格外，之后每次恢复恒 0；调用方把它记进日志，acceptance 对照
  // 「升级后首启外来 tab 消失属预期」时以此为凭，勿当回归报。
  purged: number
  adopted: number
}
```

(b) ② 区整段替换为（从 `// ② 解码悬浮窗现场` 注释到该 if 块结束）：

```ts
  // ② 解码悬浮窗现场：坏数据或从没存过都得到 null
  let dock: DockSnapshot | null = null
  let purged = 0
  if (input.state.dock !== '') {
    const d = decodeDock(input.state.dock)
    if (d !== null) {
      // 方案3（悬浮窗侧）：扇出没答上来的机器，名下会话「可能活着只是没进名单」。
      // 把这些 tab 的 sessionId 并进 live 副本，prune 就不会剥它们的引用——机器回来
      // 照常接上；真死了走挂载时的连接错误 / 1008 出口，两条路都有「重开」终态。
      // 归属按 tab.machine 取：decodeDock 强制该字段为 string，是悬浮窗侧唯一可靠的
      // 机器归属来源。修复后悬浮窗终端 tab 的 machine 恒为空串（新建不带机器、收编
      // 仅限本机），这个分支平时不命中；保留它是给 roadmap 的「远程机器 home 终端
      // 显式入口」预留的正确语义，也让 pruned 统计不被清除误计成剥引用。
      const effectiveLive = new Set(live)
      for (const t of d.tabs) {
        if (t.sessionId !== undefined && !machineOk.has(t.machine)) effectiveLive.add(t.sessionId)
      }
      const beforeTabs = d.tabs.filter((t) => t.sessionId).length
      const gated = pruneDeadDockSessions(d.tabs, effectiveLive)
      pruned += beforeTabs - gated.filter((t) => t.sessionId).length
      // 方案2：存量外来 tab 一次性清除（终端与文件同规则）。decode 照旧接受旧数据、
      // 不 bump DOCK_PERSIST_VERSION——丢弃发生在合成层；清过的 dock 随首次写回落盘，
      // 之后每次恢复都是幂等 no-op。清除命中 activeId 时显式置 null（函数末尾的既有
      // 兜底只认 null、不认悬空）；tabs 清空时把 windowOpen 一并收为 false——升级后
      // 首启不该凭空弹一个只有 tab 条的空壳浮窗（decode 出来本来就空的退化现场一并
      // 兜住，closeTab 不会写出那种形状）。
      const kept = gated.filter((t) => t.machine === '')
      purged = d.tabs.length - kept.length
      const activeId = d.activeId !== null && !kept.some((t) => t.id === d.activeId) ? null : d.activeId
      dock = {
        ...d,
        tabs: markIncompatibleDockTabs(kept, incompatible),
        activeId,
        windowOpen: kept.length === 0 ? false : d.windowOpen,
        geom: clampGeom(d.geom, input.vw, input.vh, input.inset),
      }
    }
  }
```

(c) `buildRestore` 的 return 行改为：

```ts
  return { entries, dock, dockOrphans, selected: input.state.selected, dropped, pruned, purged, adopted }
```

(d) `useWorkbenchSync.ts` 的 console.debug 对象加一行（恢复完成日志）：

```ts
          console.debug('工作台状态恢复完成', {
            目录数: r.entries.length,
            抹掉的死会话: r.pruned,
            清除的外来悬浮窗 tab: r.purged,
            补进来的孤儿会话: r.adopted,
            丢弃的坏行: r.dropped.length,
            悬浮窗: r.dock !== null ? '已恢复' : '无落盘现场',
          })
```

4. **测试范围声明**：只跑 `npx vitest run src/app/workbench/restore.test.ts src/app/homedock/dockPersist.test.ts src/app/workbench/useWorkbenchSync.test.ts`。预期全绿（restore.test.ts 21 条：18 + 3 新）。红绿模板收口。
5. **日志步骤**：console.debug 新增「清除的外来悬浮窗 tab」键（见 3(d)），首启恢复时该值非 0 即方案2 生效的证据，acceptance 据此区分「设计内清除」与「回归」。
6. **注释步骤**：② 区两段「为什么」注释与 RestoreResult.purged 字段注已写进代码块，照抄。

**次序承重说明**（为什么 prune 在 purge 之前）：purge 无条件删整 tab，两种次序的最终 tabs 相同；但 `pruned` 统计的语义（「剥了多少个引用」）只有在 prune 先跑、purge 后跑时才诚实——先 purge 会让「清整 tab」和「剥引用留壳」在统计上无法区分，第二条测试的 `pruned === 0` 断言就是钉这个的。

---

## Task 4：B283 红色回路转正

覆盖 spec 方案测试决定的缝 1④。把 `b283-redloop.test.ts` 转正进 `restore.test.ts` 并转绿；**不是原样搬**——open1 的「sessionId 被剥」断言反转为「保引用」，夹具按修复后的世界重构（原夹具的外来 dock tab 会被方案2 整个清掉，「保引用」改由夹具里新增的本机 tab 承载；外来 tab 改为承载清除断言）。

**文件集**：`web/src/app/workbench/restore.test.ts`（追加）、`web/src/app/workbench/b283-redloop.test.ts`（删除）。

### Interfaces

Consumes：`buildRestore`（缝 1）+ Task 1–3 的全部行为。Produces：无新符号。

### 步骤

1. **红色形态的基线证据**（出稿人已跑、台账有原始输出）：原 `b283-redloop.test.ts` 在基线 1 failed（open2 收编 → tab 1→2）。转正测试的每一半缝级断言各自由 Task 1–3 的跑红步骤背书（同缝同断言方向），本 task 在 1–3 落地后写下去即绿——属最薄路径条「写下去就会绿的才免除」的豁免，豁免理由在此声明。
2. **在 restore.test.ts 文件末尾（既有 describe 之后）追加**，并同步把文件头注释的「职责」行补一句「B283 红色回路的转正锁」：

```ts
describe('B283 红色回路转正：同一份现场连续两次打开，tab 数与引用都不得漂移', () => {
  it('两次打开之间悬浮窗 tab 数不增长，本地 tab 的引用保得住（修复前 1→2）', () => {
    // 打开 N：快照里一个外来 tab（mac-02 的 H9，扇出没带回它——ok=false）＋一个
    // 本机 tab（H1，活着且在名单里）。
    const open1 = buildRestore({
      state: state({
        dock: dockRaw(
          [
            { id: 'u1', kind: 'terminal', seq: 1, machine: 'mac-02', sessionId: 'H9' },
            { id: 'h1', kind: 'terminal', seq: 2, machine: '', sessionId: 'H1' },
          ],
          'u1',
          true,
        ),
      }),
      sessions: [homeSess('H1')],
      machines: [machine('', true), machine('mac-02', false)],
      ...VIEW,
    })
    // 外来 tab 整个清除（不是剥引用留壳），本机 tab 原样保引用——修复前这里是
    // 「H9 被剥成壳 + H1 留引用」两个 tab，壳会在两次打开之间自建新会话。
    expect(open1.dock!.tabs).toHaveLength(1)
    expect(open1.dock!.tabs[0]).toMatchObject({ id: 'h1', sessionId: 'H1' }) // 保引用（反转自「被剥」）
    expect(open1.dock!.activeId).toBe('h1') // u1 被清 → 显式置 null → 既有兜底重指
    expect(open1.purged).toBe(1)
    expect(open1.pruned).toBe(0) // mac-02 没答上来：恢复层没有把 H9 判死剥引用

    // 两次打开之间：h1 有 sessionId 可 attach，TerminalTab 不自建（TerminalTab.tsx
    // 的 `if (!id)` 支不进），u1 已随首次写回落盘消失。修复前的循环是：壳自建 H9'
    // 写回 → 下一轮 H9 活着回来被当孤儿收编 → tab +1。

    // 打开 N+1：H9（mac-02 home）活着回到列表——不收编（方案1：悬浮窗是本机面）。
    const open2 = buildRestore({
      state: state({
        dock: dockRaw([{ id: 'h1', kind: 'terminal', seq: 2, machine: '', sessionId: 'H1' }], 'h1', true),
      }),
      sessions: [homeSess('H1'), homeSess('H9', 'mac-02')],
      machines: [machine('', true), machine('mac-02', true)],
      ...VIEW,
    })
    expect(open2.dock!.tabs).toHaveLength(1)
    expect(open2.dock!.tabs[0].sessionId).toBe('H1')
    expect(open2.adopted).toBe(0)
    expect(open2.purged).toBe(0)
  })
})
```

3. **删除 `web/src/app/workbench/b283-redloop.test.ts`**（转正完成；其不变式与本条注释一并活在 restore.test.ts 里）。删除是本 task 文件集的一部分，不算越界。
4. **测试范围声明**：只跑 `npx vitest run src/app/workbench/restore.test.ts`。预期 22 条全绿（21 + 转正回路；`b283-redloop.test.ts` 已删，不再出现在清单里）。
5. **日志步骤**：无（纯测试 task）。
6. **注释步骤**：回路上方的两段「两次打开之间」叙述注释已写进代码块，照抄——它们是这条测试的说明书，删了下一个读码的人就得重推一遍根因链。

---

## Task 5：话术订正（方案 4，无缝级断言）

spec 明说「文案变更不设缝级断言，归 acceptance 真机一眼」。本 task 只改注释与字符串字面量，无红绿周期（纯映射/文案步骤不配独立红绿——plan 纪律原文）。**不改测试文件里的场景注释**（`Shell.test.tsx:530/547/577` 的「agentd 重启后的现场」是探针场景的历史描述，不在 spec 方案 4 的六处清单里，动它属于 scope drift）。

**文件集**：`web/src/app/shell/Shell.tsx`、`web/src/app/workbench/TerminalTab.tsx`。

### Interfaces

Consumes：无。Produces：无（用户可见文案与注释）。

### 步骤

六处逐字替换：

1. `Shell.tsx:655` 弹层文案（closingGone === true 分支的字符串）。原：

```tsx
              '这个终端会话在服务端已经不存在了（agentd 重启会清掉所有会话）。\n' +
              '关闭只是把这个 tab 收起来，不会再终止什么。'
```

改为：

```tsx
              '这个终端会话在服务端已经不存在了（终端会话跨 agentd 重启存活，只有机器重启、退出 shell 或显式停止才会让它消失）。\n' +
              '关闭只是把这个 tab 收起来，不会再终止什么。'
```

（必须保留子串「在服务端已经不存在了」——`Shell.test.tsx:586` 的 `/在服务端已经不存在了/` 断言锁着它；改丢即测试翻红。上方那行 `// 会话已经不在了：没有东西可终止，这一步只是把 tab 收掉` 注释不动。）

2. `Shell.tsx:217-219` 注释。原：

```tsx
  // closingGone：服务端已经查不到这个会话了（最常见是 agentd 重启把内存里的会话
  // 全清了）。只影响措辞——弹层不能一边说「会终止里面正在运行的命令」，一边
  // 关的其实是一个早就没了的会话。null = 还没问出来，按「可能还活着」说话
```

改为：

```tsx
  // closingGone：服务端已经查不到这个会话了（PTY 会话由 ptyhost 持有、跨 agentd
  // 重启存活——查不到说明它真的消失了：机器重启、退出 shell 或显式停止）。只影响
  // 措辞——弹层不能一边说「会终止里面正在运行的命令」，一边关的其实是一个早就
  // 没了的会话。null = 还没问出来，按「可能还活着」说话
```

3. `Shell.tsx:270-273` 注释。原：

```tsx
  // 查不到 = 会话已经不在（agentd 重启是最常见的一种）：那句「会终止正在运行的
  // 命令」对它是假话，而假话会让用户以为自己正在杀掉什么东西。问不出来
  // （请求本身失败）时一律退回 null，按「可能还活着」说话——宁可吓一跳，不可
  // 骗人说没事
```

改为：

```tsx
  // 查不到 = 会话已经不在（会话跨 agentd 重启存活，所以「不在」是真的不在——
  // 机器重启、退出 shell 或显式停止）：那句「会终止正在运行的命令」对它是假话，
  // 而假话会让用户以为自己正在杀掉什么东西。问不出来（请求本身失败）时一律退回
  // null，按「可能还活着」说话——宁可吓一跳，不可骗人说没事
```

4. `Shell.tsx:301-306` 注释。原：

```tsx
      // 404 是**成功**的一种：服务端根本没有这个会话，最常见的是 agentd 重启后
      // 内存里的会话全没了。此时「不许吞错误」那条纪律护的东西（别把还活着的
      // shell 从视野里抹掉）根本不存在——已经没有 shell 可留。照旧当失败处理的
      // 代价是这个 tab 被焊死：确认弹层每次都红字报「会话不存在」，关不掉，也
      // 没有第二个出口。删除对这一路是幂等的
```

改为：

```tsx
      // 404 是**成功**的一种：服务端根本没有这个会话。PTY 会话跨 agentd 重启存活，
      // 「没有」只能是机器重启、退出 shell 或显式停止之后的真消失。此时「不许吞
      // 错误」那条纪律护的东西（别把还活着的 shell 从视野里抹掉）根本不存在——
      // 已经没有 shell 可留。照旧当失败处理的代价是这个 tab 被焊死：确认弹层每次
      // 都红字报「会话不存在」，关不掉，也没有第二个出口。删除对这一路是幂等的
```

5. `TerminalTab.tsx:8-9` 文件头注释。原：

```tsx
//   - 订阅被判死（close 1008，最常见的是 agentd 重启后旧会话已不存在）时，
//     除了报出服务端给的原因，还给一个「重开一个终端」的出口——没有它，这个
```

改为：

```tsx
//   - 订阅被判死（close 1008：会话已不存在——机器重启、退出 shell 或显式停止
//     都会让它消失；会话本身跨 agentd 重启存活）时，
//     除了报出服务端给的原因，还给一个「重开一个终端」的出口——没有它，这个
```

6. `TerminalTab.tsx:616-617` 注释。原：

```tsx
                // 老会话不发 DELETE：它在服务端要么已经不存在（agentd 重启），要么是被
                // 判死的另一条订阅，替用户去删一个可能还活着的 shell 不是这个按钮的职责。
```

改为：

```tsx
                // 老会话不发 DELETE：它在服务端要么已经不存在（机器重启 / 退出 shell /
                // 显式停止之后），要么是被判死的另一条订阅，替用户去删一个可能还活着
                // 的 shell 不是这个按钮的职责。
```

7. **测试范围声明**：只跑 `npx vitest run src/app/shell/Shell.test.tsx src/app/workbench/TerminalTab.test.tsx`（两个直接耦合面；产物全绿为过）。本 task 不新增测试；全量编译与集成全量归 implement 三段律。
8. **日志步骤**：无——注释与字符串字面量不引入新执行路径。
9. **注释步骤**：本 task 的产出就是注释本身；六处均按上面逐字替换，不许自拟措辞（「跨 agentd 重启存活；消失途径 = 机器重启 / 退出 shell / 显式停止」是 spec 方案 4 钉死的表述）。

---

## 五项检查

### 1. 缺陷族对抗审查（逐族设问，按 defect-families 基线）

| 族 | 设问 | 结论 |
|---|---|---|
| 生命周期 / 状态机中断 | purge/prune 与写回之间宿主重启？谁收尾？ | 幂等：清除与门控发生在**每次**恢复，输入是服务端落盘原文；崩溃在写回前 → 下次恢复对原文重做一遍，无半态。机器注销后 machines 无行 → 按不 ok → 中央区引用永剥不了（保守，spec 已表态）；悬浮窗外来 tab 则被方案2 清掉（一次性，设计内）。写回收尾由既有 useWorkbenchSync flush 负责（本卡不改它的闸门逻辑）。 |
| 静默失败 / 误导报错 | 机器离线时保住的引用点开会看到什么？失败可行动吗？ | 引用在 → TerminalTab 不走 `if (!id)` 自建支（`TerminalTab.tsx:379`），attach 失败走既有连接错误 / 1008 呈现，都有「重开」出口——正是用户故事 3 的可见行为。新增可观测性：`purged` 计数进恢复完成日志；无新错误分支。 |
| 跨平台假设 | 本改动哪些假设在其他平台不成立？ | 无，因为纯 web TS 纯函数与字符串字面量，不涉及路径/进程组/webview 差异面。 |
| 假红 / 假绿 | 判据是不是中途副产物？有无反面断言？锁的是调用方依赖的行为吗？ | 各测试入口符号都是 `buildRestore`（缝 1）或既有 `pruneDeadSessions` 直测（缝 2），断言的是输出形状（sessionId 保留/剥离、tab 数、activeId、windowOpen、统计量）——正是调用方（useWorkbenchSync→hydrate）依赖的面；换实现不改需求不会无意义翻红。反面断言齐备：每条「不剥」都有配对的「照常剥」（Task 1 第一条、Task 3 第一条），防止门控过度拦截造成假绿温床。红色回路的「两次打开之间不自建」对应 TerminalTab.tsx:379 的真实行为（有 sessionId 不自建），acceptance 真机项有对应（见文末真机清单）。 |
| 门禁绕过 | 新增写路径？同一规则的所有入口共享同一道门吗？ | 无新写路径。门控数据 machines 来自唯一一次 `fetchPtySessions('all')` 响应，两处 prune 与 ③ 收编判据（machine!=='' 直接判，不经门控）读的是同一份输入的只读投影——单一真相，无 TOCTOU 窗口。悬浮窗清空与 activeId 置 null 在同一次合成内完成，写回闸（readyRef）语义未动。 |

追加设问：

- **序列化边界**：本卡不新增任何跨序列化边界的字段。machines 从 Go wire（`internal/proto/pty.go:42`）到 TS 类型（`types.ts:730`）的对应已有契约夹具 `web/src/api/testdata/PtySessionsResp.json` 锁着；`machineOkSet` 是只读折叠（Set），不是投影；`purged` 只进 console.debug，不落盘。dock 载荷编解码未动（不 bump DOCK_PERSIST_VERSION），清除发生在 decode 之后、encode 之前，既有 encodeDock/decodeDock roundtrip 测试继续覆盖该编解码器。
- **枚举新值过既有白名单**：无新枚举值（machine 是自由字符串；kind 取值未变）。
- **承重安全属性有测试锁住**：不涉及安全属性；「缺席不判死」这一承重行为属性由 Task 1 第二/三条 + Task 4 回路锁住，acceptance 变异复验有测试可红。

### 2. 序列化边界设问

新增数据字段清单：`RestoreInput.machines`（消费既有 wire 字段，无新手写投影）、`RestoreResult.purged`（进程内统计）。两者都不跨手写序列化/投影边界——「这条链路要有穿边界的回归测试」的要求由既有 `PtySessionsResp.json` 契约测试与 dockPersist roundtrip 测试覆盖（codec 未改）。**无需新增 roundtrip 属性测试**，理由如上；若将来给 DockSnapshot 加字段，再按此节要求补。

### 3. 上下文预算检查

每个 task 圈得出有界文件集：Task 1/2/3 各自只触 restore.ts（231 行）+ restore.test.ts + （Task 1/3）useWorkbenchSync.ts（239 行）；Task 4 只触两个测试文件；Task 5 只触两个组件文件的注释/字面量。无跨子系统蔓延，无需要先插竖切卡的失控面。

### 4. 类型标注（边界型子系统的真机清单）

本卡不是边界型子系统（无 CLI/跨语言面变化）；真机清单属 acceptance 节点，此处只列**acceptance 应对照的真机项**（非本计划验收标准）：① 升级后首启，悬浮窗里原属其他机器的外来 tab 消失、窗口不空壳弹开（方案2，勿报回归）；② 某机器离线时打开桌面端，中央区该机器的终端 tab 保留原会话身份、呈现连接错误与重开出口、不静默换新 shell；③ 关闭确认弹层文案与六处注释的新表述；④ 机器恢复后原 tab 接回原会话（sessionId 不变）。

### 5. 接缝覆盖（双向，对照 spec 测试决定）

**测试 → 缝**（看入口符号，不看标注）：

| 测试 | 入口符号 / 链 | 归属缝 |
|---|---|---|
| Task 1「ok 时照常剥」 | buildRestore（链穿过 pruneDeadSessions） | 缝 2 正例 + 缝 1① 反面锁 |
| Task 1「ok=false 不剥」 | buildRestore（链在调用处**跳过** pruneDeadSessions——门控按设计在调用处） | 缝 1① workbench 反例 |
| Task 1「machines 缺席不剥」 | buildRestore（同上） | 缝 1① workbench 反例（缺席形状） |
| Task 3「dock 本地死会话照常剥」 | buildRestore | 缝 1① dock 反面锁 |
| Task 3「全外来清除」 | buildRestore | 缝 1③ + 缝 1⑤（+① dock 的 pruned=0 面） |
| Task 3「activeId 重指」 | buildRestore | 缝 1③ 边界形态（审查 I1 补的断言） |
| Task 2「home 收编仅本机」 | buildRestore | 缝 1② |
| Task 4 转正回路 | buildRestore | 缝 1④（兼 ①dock 本地保引用、③⑤、②） |
| persist.test.ts 既有直测（保留，不改） | pruneDeadSessions | 缝 2 |

**缝 → 测试**：

| 缝 | 锁它的测试 |
|---|---|
| 缝 1①（dock 半） | Task 4 open1（本机 tab 保引用）+ Task 3 第一条（真死照剥）+ Task 3 第二条 / Task 4 open1 的 `pruned===0`（外来 tab 未被判死剥引用） |
| 缝 1①（workbench 半） | Task 1 第二、三条（反面不剥）+ Task 1 第一条（正面照剥） |
| 缝 1② | Task 2 专测 + Task 4 open2 的 `adopted===0`；本机照收由两条既有用例继续锁 |
| 缝 1③ | Task 3 第二、三条 + Task 4 open1 |
| 缝 1④ | Task 4 转正回路（红色形态由台账基线原始输出背书） |
| 缝 1⑤ | Task 3 第二条（tabs 空 / activeId null / windowOpen false） |
| 缝 2 | Task 1 第一条（链穿过）+ persist.test.ts 既有直测；**反例按设计落在调用处**（缝 1①），pruneDeadSessions 函数本身零改动故无新直测——这是显式声明，不是遗漏 |

内部锁声明：**无**。本计划新增的每一条测试，入口符号都是 `buildRestore`（缝 1）；persist.test.ts / dockPersist.test.ts 的直测是改动前就存在的回归锁，不属新增内部锁。步骤正文无条件退路（没有任何「若意外先绿就改成直喂 X」形状的文字），退路同闸无对象。

---

## 占位符扫描

- 无 TBD /「加适当的错误处理」/「同 Task N」/只描述不给代码的步骤：五个 task 的实现代码给到最小实现级完整度，测试代码逐条断言写全，Task 5 六处文案逐字替换。
- 「断言列全 + 指认既有 harness」的正当出口：**未使用**（所有测试都给了完整代码）。
- 内部锁：无（见五项检查第 5 条的声明）。
- 基线颜色逐条声明：Task 1 第一条、Task 3 第一条为「写下去就绿」的回归锁（最薄路径条豁免，豁免已逐条标注）；其余新测试基线预期红；Task 4 转正回路的豁免理由在其步骤 1 显式声明。

---

## spec 覆盖表

| spec 条目 | 落点 |
|---|---|
| 方案 1：home 收编仅限本机（恢复 ③ 跳过 machine!==''） | Task 2（实现+测试） |
| 方案 2：存量外来 tab 一次性清除（解码后丢 machine!==''；activeId 显式置 null；清空收 windowOpen；不 bump 版本） | Task 3（实现+测试两条） |
| 方案 3：machines 入参 + 两处 prune 门控（dock 按 tab.machine，workbench 按 base 行 machine；本机恒 ok；查不到按不 ok） | Task 1（入参 + 门控表 + 中央区半）+ Task 3（悬浮窗半） |
| 方案 4：话术订正（Shell.tsx:655/217/270/301、TerminalTab.tsx:8/616） | Task 5（六处逐字） |
| 缝 1① 机器缺席/ok=false 不剥 dock 与 workbench 引用 | Task 1（workbench）+ Task 3 / Task 4（dock） |
| 缝 1② home 收编仅限本机 | Task 2 + Task 4 open2 |
| 缝 1③ 外来 dock tab 一次性清除 | Task 3 + Task 4 open1 |
| 缝 1④ 红色回路两次打开不增长（转绿，open1 断言反转） | Task 4 |
| 缝 1⑤ 全外来快照清除后 tabs 空 / activeId null / windowOpen false | Task 3 第二条 |
| 缝 2 pruneDeadSessions 门控正反例 | Task 1（正例经链、反例在调用处——显式声明）+ 既有 persist.test.ts |
| 文案不设缝级断言（acceptance 一眼） | Task 5 无测试步骤 |
| 实现决定：baseOfSession home@machine 分支保留 | Task 2 注释（不改该分支） |
| 实现决定：被清 tab 不参与 h<n> 计数器播种 | 无需改动（useHomeDock:247 的 `/^h(\d+)$/` 过滤既有，未触碰；Task 4 回路的清后恢复路径实际穿过 hydrate 播种逻辑） |
| Out of Scope 三条进 roadmap / ptyhost 回收 / 远程 home 入口 | 不在本计划（spec 节点义务；roadmap 落账见 spec 审查 I4 的处置） |

## 备注（执行者须知）

- **执行偏离回写（2026-08-29 审查 Minor-2）**：Task 1 步骤 2 的共享夹具（`homeSess`/`dockRaw`/`HomeTab` import）实际落在 **Task 2** 首次使用时（Task 1 落它们会触发 noUnusedLocals TS6133，与「每 task 收口 typecheck 绿」冲突）；Task 3 步骤 3(d) 日志键 `清除的外来悬浮窗 tab` 含空格，落地必须写成带引号的 `'清除的外来悬浮窗 tab': r.purged`（裸标识符是 TS1005）。零上下文重放按此两处执行，文件终态与计划一致。
- Task 次序不能换：Task 3 的清除用例断言 `adopted === 0` 与 `tabs 长 0`，两者要求方案 1（Task 2）已在位；倒序执行者这两条会红——这是依赖信号，不是测试错误。
- `useWorkbenchSync.test.ts` 与 `Shell.test.tsx` 对本卡改动是**兼容耦合**：前者 mock 形状宽松（`{ sessions: [] }`），后者锁「在服务端已经不存在了」子串——Task 5 文案保留该子串即可，两文件都无需改动。
- 全量测试（`npx vitest run`）不属于任何单个 task；implement 三段律的集成全量在收口时跑。
