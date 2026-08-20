// restore.ts —— 把「落盘状态」与「服务端会话列表」合成一次可直接灌入的恢复结果。
//
// 职责：
//   - 解码每一行基准状态，坏行整行丢弃
//   - 抹掉已死的 sessionId（spec §2 规则二），tab 留在原位
//   - 把「列表里有、状态里没有」的孤儿会话补进去：工作树进对应目录，home 进悬浮窗
//   - 悬浮窗几何按当前视口夹紧
//
// 边界：
//   - **纯函数，不碰 React、不发请求、不读 window**：视口尺寸由调用方传进来。
//     这一层全部的判断都用表驱动测试一条条钉住，靠 mock fetch 是测不动的
//   - 不决定「选中哪个目录」：selected 原样透传，校验它还在不在树上是 Shell 的事
//     （要等项目树，spec §6）
//   - 不打日志：它不知道自己跑在什么上下文里；统计量以返回值给出，由调用方记录
import type { PtySession, WorkbenchStateResp } from '../../api/types'
import { clampGeom, decodeDock, pruneDeadDockSessions, type DockSnapshot } from '../homedock/dockPersist'
import type { HomeTab } from '../homedock/useHomeDock'
import { decodeBase, pruneDeadSessions } from './persist'
import { EMPTY_WORKBENCH, nextTerminalSeq, openTab, type Workbench } from './tabs'
import { HOME_BASE, type BaseDir } from './useWorkbench'

// baseOfSession 把一个会话反解成它所属的基准目录。
//
// 参数：s 是服务端会话列表中的一条会话。
// 返回：与 ProjectTree.workspaceBase 同口径的 BaseDir。
// 注意：只计算归组身份，不判断目录是否仍在项目树中。
// 工作树的 key 必须与 ProjectTree.workspaceBase 完全一致（含机器维度）——
// 两边对不上就会出现「左栏点进这个目录，恢复出来的终端却在另一个组里」。
//
// label 退回目录名：会话不带分支信息，而树上的 label 优先用分支名。这只影响
// 标题文字，**不影响归组**（key 相同），用户点一下左栏就会换成带分支名的那个。
export function baseOfSession(s: PtySession): BaseDir {
  if (s.base_kind === 'home') {
    // 远端 home 与本机 home 必须分开：路径都叫「~」，但它们是两台机器上的两个目录
    if (s.machine !== '') {
      return { key: `~@${s.machine}`, kind: 'home', path: '~', label: `home@${s.machine}`, projectName: '', machine: s.machine }
    }
    return HOME_BASE
  }
  const name = s.base_path.split('/').filter(Boolean).pop() ?? s.base_path
  return {
    key: s.machine ? `${s.base_path}@${s.machine}` : s.base_path,
    kind: 'workspace',
    path: s.base_path,
    label: name,
    projectName: '',
    machine: s.machine,
  }
}

// RestoreInput 是合成恢复结果所需的全部输入。
export interface RestoreInput {
  state: WorkbenchStateResp
  sessions: PtySession[]
  // 视口宽高与顶部让位，用于夹紧悬浮窗几何。由调用方读 window 后传进来
  vw: number
  vh: number
  inset: number
}

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
  // 下面三个是给日志用的统计量，不参与渲染
  dropped: string[]
  pruned: number
  adopted: number
}

// liveSessionIds 挑出还活着的会话 id。exit_code 缺席 = 还活着，出现 = 已退出。
//
// 为什么用 `!= null` 而不是 `!== undefined`：它同时挡住 undefined 与 null。
// 今天 Go 侧 `ExitCode *int` 带 omitempty，nil 是**缺键**而不是 null，所以两种写法
// 等价；但这条断言的正确性不该依赖某个 json tag 上的 omitempty 还在不在。
function liveSessionIds(sessions: PtySession[]): Set<string> {
  const live = new Set<string>()
  for (const s of sessions) {
    if (s.exit_code != null) continue
    live.add(s.id)
  }
  return live
}

// collectUsedSessionIds 收集恢复结果里已经被某个 tab 占用的会话 id。
// 孤儿判定就是「活着但不在这个集合里」。
function collectUsedSessionIds(entries: Array<{ base: BaseDir; wb: Workbench }>, dockTabs: HomeTab[]): Set<string> {
  const used = new Set<string>()
  for (const e of entries) {
    for (const g of e.wb.groups) {
      for (const t of g.tabs) {
        if (t.content.kind === 'terminal' && t.content.sessionId) used.add(t.content.sessionId)
      }
    }
  }
  for (const t of dockTabs) {
    if (t.sessionId) used.add(t.sessionId)
  }
  return used
}

// countTerminalsWithSession 数一个工作台里带会话的终端 tab 数，用于统计被抹掉多少个。
function countTerminalsWithSession(wb: Workbench): number {
  let n = 0
  for (const g of wb.groups) {
    for (const t of g.tabs) {
      if (t.content.kind === 'terminal' && t.content.sessionId) n++
    }
  }
  return n
}

// buildRestore 合成恢复结果。
//
// 参数：见 RestoreInput。
// 返回：见 RestoreResult。**不抛异常**——任何坏数据都降级为「丢掉那一份」，
// 因为这条路径失败意味着用户看到一个空界面，而空界面不该由一次 JSON.parse 决定。
// 注意：它不探活，只使用调用方已经拉到的会话列表。
export function buildRestore(input: RestoreInput): RestoreResult {
  const live = liveSessionIds(input.sessions)

  // ① 解码基准行，坏行整行丢弃；顺手抹掉死会话
  const entries: Array<{ base: BaseDir; wb: Workbench }> = []
  const dropped: string[] = []
  let pruned = 0
  for (const row of input.state.bases) {
    const decoded = decodeBase(row.base_key, row.payload)
    if (decoded === null) {
      dropped.push(row.base_key)
      continue
    }
    const before = countTerminalsWithSession(decoded.wb)
    const wb = pruneDeadSessions(decoded.wb, live)
    pruned += before - countTerminalsWithSession(wb)
    entries.push({ base: decoded.base, wb })
  }

  // ② 解码悬浮窗现场：坏数据或从没存过都得到 null
  let dock: DockSnapshot | null = null
  if (input.state.dock !== '') {
    const d = decodeDock(input.state.dock)
    if (d !== null) {
      const beforeTabs = d.tabs.filter((t) => t.sessionId).length
      const tabs = pruneDeadDockSessions(d.tabs, live)
      pruned += beforeTabs - tabs.filter((t) => t.sessionId).length
      dock = { ...d, tabs, geom: clampGeom(d.geom, input.vw, input.vh, input.inset) }
    }
  }

  // ③ 补孤儿会话
  const used = collectUsedSessionIds(entries, dock?.tabs ?? [])
  const dockOrphans: HomeTab[] = []
  let adopted = 0
  // 悬浮窗 tab 的 seq 接着现有最大值往下发，避免出现两个 'bash · home 3'
  let dockSeq = Math.max(0, ...(dock?.tabs ?? []).map((t) => t.seq))
  for (const s of input.sessions) {
    if (!live.has(s.id) || used.has(s.id)) continue
    adopted++
    const b = baseOfSession(s)
    if (b.kind === 'home') {
      // 孤儿 home 会话的 tab id 直接用 sessionId：与 Shell 里 dock.adopt 的既有
      // 调用一致，且天然唯一。它不是 h<n> 形状，不参与 useHomeDock 的计数器播种
      const tab: HomeTab = { id: s.id, kind: 'terminal', seq: ++dockSeq, sessionId: s.id, machine: s.machine }
      if (dock === null) dockOrphans.push(tab)
      else dock.tabs = [...dock.tabs, tab]
      continue
    }
    const found = entries.find((e) => e.base.key === b.key)
    if (found) {
      found.wb = openTab(found.wb, { kind: 'terminal', seq: nextTerminalSeq(found.wb), sessionId: s.id })
    } else {
      entries.push({ base: b, wb: openTab(EMPTY_WORKBENCH, { kind: 'terminal', seq: 1, sessionId: s.id }) })
    }
  }

  // 有 tab 却没有激活项时，浮窗会显示一片空白且没人能解释为什么。
  // 这个状态只可能由「往空现场里补了孤儿」产生，就地补上
  if (dock !== null && dock.activeId === null && dock.tabs.length > 0) {
    dock.activeId = dock.tabs[0].id
  }

  return { entries, dock, dockOrphans, selected: input.state.selected, dropped, pruned, adopted }
}
