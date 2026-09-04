// restore.ts —— 将全局工作台 payload、悬浮窗现场和服务端会话合成为恢复结果。
//
// 职责：只合成 workbench/dock，并返回 selected/统计；不选树节点、不发请求、不打日志。
// 边界：只认 GLOBAL_WORKBENCH_KEY，旧按目录行进入 legacy，不猜测式迁移。
import type { MachineStatus, PtySession, WorkbenchStateResp } from '../../api/types'
import {
  clampGeom,
  decodeDock,
  markIncompatibleDockTabs,
  pruneDeadDockSessions,
  type DockSnapshot,
} from '../homedock/dockPersist'
import type { HomeTab } from '../homedock/useHomeDock'
import {
  decodeWorkbench,
  GLOBAL_WORKBENCH_KEY,
  markIncompatibleSessions,
  pruneDeadSessions,
} from './persist'
import { EMPTY_WORKBENCH, type BaseDir, type Workbench } from './tabs'
import { HOME_BASE } from './useWorkbench'

export interface RestoreInput {
  state: WorkbenchStateResp
  sessions: PtySession[]
  // scope=all 扇出的 machines 行（PtySessionsResp.machines，types.ts:198）。B283 方案3 的
  // 门控数据：某台机器的行缺席或 ok=false 时，它名下的会话引用不剥——扇出缺席 ≠ 会话
  // 死亡。整个字段缺席（undefined）按空表处理。本机（machine===''）恒 ok：本机行由
  // 汇总端点恒以 ok=true 领衔（internal/agentd/pty_api.go:189），本机会话名单从不缺席。
  machines?: MachineStatus[]
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
  // purged 是 B283 方案2 清掉的外来悬浮窗 tab 数（终端与文件都算）。升级后首启它通常
  // 一次性格外，之后每次恢复恒 0；调用方把它记进日志，acceptance 对照
  // 「升级后首启外来 tab 消失属预期」时以此为凭，勿当回归报。
  purged: number
  adopted: number
}

/** 从 PtySession 生成树可复用的 BaseDir；同一路径按 machine 区分。 */
export function baseOfSession(session: PtySession): BaseDir {
  if (session.base_kind === 'home') {
    return session.machine === ''
      ? HOME_BASE
      : { key: `~@${session.machine}`, kind: 'home', path: '~', label: `home@${session.machine}`, projectName: '', machine: session.machine }
  }
  const label = session.base_path.split('/').filter(Boolean).pop() ?? session.base_path
  return {
    key: session.machine === '' ? session.base_path : `${session.base_path}@${session.machine}`,
    kind: 'workspace', path: session.base_path, label, projectName: '', machine: session.machine,
  }
}

function liveIds(sessions: PtySession[]): Set<string> {
  return new Set(sessions.filter((session) => session.exit_code == null).map((session) => session.id))
}

function incompatibleIds(sessions: PtySession[]): Set<string> {
  return new Set(sessions.filter((session) => session.exit_code == null && session.incompatible).map((session) => session.id))
}

function countSessions(wb: Workbench): number {
  return wb.groups.reduce((count, group) => count + group.columns.reduce((columnCount, column) => columnCount + column.panes.filter((tab) => tab?.content.kind === 'terminal' && tab.content.sessionId !== undefined).length, 0), 0)
}

function usedSessionIds(wb: Workbench, dock: DockSnapshot | null): Set<string> {
  const ids = new Set<string>()
  for (const group of wb.groups) for (const column of group.columns) for (const tab of column.panes) {
    if (tab?.content.kind === 'terminal' && tab.content.sessionId !== undefined) ids.add(tab.content.sessionId)
  }
  for (const tab of dock?.tabs ?? []) if (tab.sessionId !== undefined) ids.add(tab.sessionId)
  return ids
}

// machineOkSet 把扇出应答折成「本次答上来了」的机器名集合——B283 方案3 两处 prune 的门控表。
//
// 参数：machines 是 PtySessionsResp.machines；undefined 按空表处理。
// 返回：ok=true 的机器名集合；本机（machine===''）无条件在内——本机行由
// internal/agentd/pty_api.go:189 恒以 {Name:"", Ok:true} 领衔返回，本机会话名单从不
// 缺席，门控对本机没有存在意义；集合里查不到的机器一律按「没答上来」处理（保守：
// 宁可留一个连不上可显式重开的 tab，不静默造孤儿）。
// 注意：不导出——它只服务 buildRestore 内的两处门控，外面没有第二个消费者。
function machineOkSet(machines: MachineStatus[] | undefined): Set<string> {
  const ok = new Set<string>([''])
  for (const machine of machines ?? []) {
    if (machine.ok) ok.add(machine.name)
  }
  return ok
}

/** 只恢复 global 行；清理 session、补孤儿，并保持 selected/active 语义。 */
export function buildRestore(input: RestoreInput): RestoreResult {
  let workbench: Workbench = EMPTY_WORKBENCH
  const dropped: string[] = []
  const legacy: string[] = []
  let globalSeen = false
  for (const row of input.state.bases) {
    if (row.base_key !== GLOBAL_WORKBENCH_KEY) {
      legacy.push(row.base_key)
      continue
    }
    if (globalSeen) continue
    globalSeen = true
    const decoded = decodeWorkbench(row.payload)
    if (decoded === null) dropped.push(row.base_key)
    else workbench = decoded
  }

  const live = liveIds(input.sessions)
  const machineOk = machineOkSet(input.machines)
  const before = countSessions(workbench)
  // B283 方案3（中央区侧）：机器这次扇出没答上来时，它名下的 tab 不做死亡判决——
  // 会话可能活着只是没进名单。全局 workbench 一个 payload 装多台机器的 tab，门控
  // 逐 tab 走（keep 谓词按 tab.base.machine 判归属，TabContent 没有 machine 字段，
  // base 是唯一可靠来源）；真死了走挂载时的连接错误 / 1008 出口，两条路都有
  // 「重开」终态，不会静默自建新 shell。
  workbench = markIncompatibleSessions(
    pruneDeadSessions(workbench, live, (tab) => !machineOk.has(tab.base.machine)),
    incompatibleIds(input.sessions),
  )
  const pruned = before - countSessions(workbench)

  let dock: DockSnapshot | null = null
  let dockPruned = 0
  let purged = 0
  if (input.state.dock !== '') {
    const decoded = decodeDock(input.state.dock)
    if (decoded !== null) {
      // B283 方案3（悬浮窗侧）：扇出没答上来的机器，名下会话「可能活着只是没进名单」。
      // 把这些 tab 的 sessionId 并进 live 副本，prune 就不会剥它们的引用——机器回来
      // 照常接上；真死了走挂载时的连接错误 / 1008 出口。归属按 tab.machine 取：
      // decodeDock 强制该字段为 string，是悬浮窗侧唯一可靠的机器归属来源。修复后
      // 悬浮窗终端 tab 的 machine 恒为空串（新建不带机器、收编仅限本机），这个分支
      // 平时不命中；保留它是给 roadmap 的「远程机器 home 终端显式入口」预留的正确
      // 语义，也让 pruned 统计不被清除误计成剥引用。
      const effectiveLive = new Set(live)
      for (const tab of decoded.tabs) {
        if (tab.sessionId !== undefined && !machineOk.has(tab.machine)) effectiveLive.add(tab.sessionId)
      }
      const beforeDock = decoded.tabs.filter((tab) => tab.sessionId !== undefined).length
      const gated = pruneDeadDockSessions(decoded.tabs, effectiveLive)
      // B283 方案2：存量外来 tab 一次性清除（终端与文件同规则）。decode 照旧接受旧
      // 数据、不 bump DOCK_PERSIST_VERSION——丢弃发生在合成层；清过的 dock 随首次
      // 写回落盘，之后每次恢复都是幂等 no-op。清除命中 activeId 时显式置 null
      // （函数末尾的既有兜底只认 null、不认悬空）；tabs 清空时把 windowOpen 一并收
      // 为 false——升级后首启不该凭空弹一个只有 tab 条的空壳浮窗（decode 出来本就
      // 空的退化现场一并兜住，closeTab 不会写出那种形状）。
      const kept = gated.filter((tab) => tab.machine === '')
      purged = decoded.tabs.length - kept.length
      const activeId = decoded.activeId !== null && !kept.some((tab) => tab.id === decoded.activeId) ? null : decoded.activeId
      dock = {
        ...decoded,
        tabs: markIncompatibleDockTabs(kept, incompatibleIds(input.sessions)),
        activeId,
        windowOpen: kept.length === 0 ? false : decoded.windowOpen,
        geom: clampGeom(decoded.geom, input.vw, input.vh, input.inset),
      }
      // dock session pruning is part of the same user-visible statistic.
      const afterDock = gated.filter((tab) => tab.sessionId !== undefined).length
      dockPruned = beforeDock - afterDock
    }
  }

  const used = usedSessionIds(workbench, dock)
  const dockOrphans: HomeTab[] = []
  let adopted = 0
  let dockSeq = Math.max(0, ...(dock?.tabs ?? []).map((tab) => tab.seq))
  for (const session of input.sessions) {
    if (!live.has(session.id) || used.has(session.id)) continue
    // B322：只收本机 home。workspace 活孤儿收成新组是「打开一次多一组」的泵
    // ——剥 id 后 TerminalTab 再建会话，下一轮这些会话又被收编。B283 已经挡住
    // 外来 home；本期连 workspace 收编一起停。无 tab 的 workspace 会话留在
    // ptyhost，用户要开终端走「新终端」。baseOfSession 的 home@machine 分支仍
    // 保留（分类的是 wire 事实，roadmap 的远程 home 入口会用到）。
    if (session.base_kind !== 'home' || session.machine !== '') continue
    adopted++
    const tab: HomeTab = { id: session.id, kind: 'terminal', seq: ++dockSeq, sessionId: session.id, machine: session.machine }
    if (session.incompatible) tab.incompatible = true
    if (dock === null) dockOrphans.push(tab)
    else dock = { ...dock, tabs: [...dock.tabs, tab] }
  }
  if (dock !== null && dock.activeId === null && dock.tabs.length > 0) dock = { ...dock, activeId: dock.tabs[0].id }

  return {
    workbench,
    dock,
    dockOrphans,
    selected: input.state.selected,
    dropped,
    legacy,
    pruned: pruned + dockPruned,
    purged,
    adopted,
  }
}
