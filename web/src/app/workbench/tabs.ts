// tabs.ts —— 中央 tab 系统的模型层（纯函数，无 React 依赖）。
//
// 职责：
//   - 定义 tab 的身份、去重规则、开关与激活、左右分屏的全部状态迁移
//   - 每个写入函数都「拷贝再改」，返回新对象
//
// 边界：
//   - 不碰 React、不发请求、不认识具体渲染组件
//   - 不持有基准目录：一个 Workbench 对象**属于**某个基准目录，是谁由
//     useWorkbench 的 Map 决定。这里只管一组 tab 内部的事
//
// 只有三种 tab：终端、文件、TUI（spec §2.2）。`blank` 不是第四种，它是
// 「这个 tab 还没选种类」的中间状态——用户点 `+` 先得到一个空白 tab，
// 在里面选一种（spec §2.2.1）。把它建模成 content 的一支而不是单独的字段，
// 是为了让「选了种类」变成一次原地 setTabContent，位置与 id 都不动。

// TabContent 是一个 tab 承载的东西。
//
// 三种正式种类的「目标」各不相同，这决定了去重规则（见 dedupKey）：
//   - terminal 的目标是序号；有会话后目标是那个服务端会话的 id
//   - file 的目标是基准目录内的相对路径
//   - tui 的目标是 task id
export type TabContent =
  | { kind: 'blank' }
  // sessionId 是服务端会话的 id，**建出来之后**才有。
  //
  // 为什么可选而不是必填：tab 先出现、会话后建立——用户点「终端」的那一刻
  // 界面就该有反应，不能等一次网络往返。会话建成后由 TerminalTab 回填。
  | { kind: 'terminal'; seq: number; sessionId?: string }
  // file 的 draft / baseSha 是**草稿寄存**，不是文件内容本身。
  //
  // 为什么必须放在这里：WorkbenchPage 只渲染 activeTab，切到别的 tab 会把 FileTab
  // 整个卸载掉。草稿活在组件 state 里的话，「点一下隔壁终端再切回来」改的字就全没了。
  // 沿用终端 tab 回写 sessionId 的同一条路（setTabContent）。
  //
  // 两个字段一起存：只存 draft 不存 baseSha，切回来之后就不知道这份草稿是从哪一版
  // 改出来的，保存时只能瞎猜一个基线
  | { kind: 'file'; rel: string; draft?: string; baseSha?: string }
  | { kind: 'tui'; taskId: string }

export interface Tab {
  id: string
  content: TabContent
}

// TabGroup 是一组 tab（分屏后的一侧）。activeId 为 null 表示该组为空。
export interface TabGroup {
  tabs: Tab[]
  activeId: string | null
}

// Workbench 是一个基准目录下的全部 tab：一组或两组，外加「哪一组是焦点」。
//
// 为什么最多两组：原型就是左右两组（左 TUI、右编辑器），再多的分屏在 1280px
// 宽度下每列都窄到没法读代码。真需要时改这里的不变式，而不是改调用方。
export interface Workbench {
  groups: TabGroup[]
  active: number
}

export const EMPTY_WORKBENCH: Workbench = { groups: [{ tabs: [], activeId: null }], active: 0 }

// dedupKey 返回一个 tab 内容的去重键；返回 null 表示这种内容**永不去重**。
//
// 终端分两种情况：
//   - 还没有 sessionId：没有「目标」，永不去重——再开一个终端就是真的想要
//     第二个终端，把它折叠到已有终端上是把用户的意图吃掉了
//   - 已有 sessionId：目标就是那个服务端会话。刷新页面时会话列表与残留 tab
//     可能同时命中同一个会话，不去重就会长出两个连着同一个 PTY 的 tab
export function dedupKey(c: TabContent): string | null {
  switch (c.kind) {
    case 'file':
      // 草稿不参与去重：draft 是**同一份文件**的编辑中间态，不是打开目标的组成部分。
      // 同一 rel 的 tab 无论脏不脏都是同一个目标，去重键只看 rel
      return `file:${c.rel}`
    case 'tui':
      return `tui:${c.taskId}`
    case 'terminal':
      return c.sessionId ? `pty:${c.sessionId}` : null
    default:
      return null
  }
}

// cloneWorkbench 深拷贝到「组与 tab 数组」这一层；Tab 对象本身不可变，可共享引用。
function cloneWorkbench(wb: Workbench): Workbench {
  return {
    groups: wb.groups.map((g) => ({ tabs: [...g.tabs], activeId: g.activeId })),
    active: wb.active,
  }
}

// nextTabId 生成整个 workbench 内唯一的 tab id。
//
// 为什么不用随机数/时间戳：纯函数要可测。这里按已有 id 的数字后缀取 max+1，
// 同一串操作永远得到同一串 id。
function nextTabId(wb: Workbench): string {
  let max = 0
  for (const g of wb.groups) {
    for (const t of g.tabs) {
      const n = Number(t.id.slice(1))
      if (Number.isFinite(n) && n > max) max = n
    }
  }
  return `t${max + 1}`
}

// findByKey 在整个 workbench 里找去重键相同的 tab，返回 [组下标, tab id]。
function findByKey(wb: Workbench, key: string): [number, string] | null {
  for (let gi = 0; gi < wb.groups.length; gi++) {
    for (const t of wb.groups[gi].tabs) {
      if (dedupKey(t.content) === key) return [gi, t.id]
    }
  }
  return null
}

// openTab 在指定组（默认当前焦点组）开一个 tab 并激活它。
//
// 同身份的 tab 已存在时**不重复打开**：激活已有的那个，哪怕它在另一组
// （spec §2.2）。跨组去重是刻意的——同一个文件在左右两屏各开一份，
// 编辑时哪份是真的会当场变成一个问题。
export function openTab(wb: Workbench, content: TabContent, group?: number): Workbench {
  const key = dedupKey(content)
  if (key !== null) {
    const hit = findByKey(wb, key)
    if (hit) {
      const [gi, id] = hit
      const next = activateTab(wb, gi, id)
      next.active = gi
      return next
    }
  }
  const next = cloneWorkbench(wb)
  const gi = clampGroup(next, group ?? next.active)
  const tab: Tab = { id: nextTabId(next), content }
  next.groups[gi].tabs.push(tab)
  next.groups[gi].activeId = tab.id
  next.active = gi
  return next
}

// clampGroup 把组下标夹到合法范围，避免调用方传了一个已被合并掉的组号。
function clampGroup(wb: Workbench, group: number): number {
  if (group < 0) return 0
  if (group >= wb.groups.length) return wb.groups.length - 1
  return group
}

// closeTab 关掉一个 tab。
//
// 激活项的接替规则：关掉的是激活项时接替右邻，没有右邻取左邻——这是所有
// 编辑器的共同习惯，用户不需要重新学。
//
// 两组时关空一组，该组消失、另一组占满（spec §2.1）。单组时组保留但变空，
// 由渲染层显示空态。
export function closeTab(wb: Workbench, group: number, tabId: string): Workbench {
  const next = cloneWorkbench(wb)
  const gi = clampGroup(next, group)
  const g = next.groups[gi]
  const idx = g.tabs.findIndex((t) => t.id === tabId)
  if (idx === -1) return wb
  g.tabs.splice(idx, 1)
  if (g.activeId === tabId) {
    const heir = g.tabs[idx] ?? g.tabs[idx - 1] ?? null
    g.activeId = heir ? heir.id : null
  }
  if (g.tabs.length === 0 && next.groups.length > 1) {
    next.groups.splice(gi, 1)
    next.active = 0
  } else if (next.active >= next.groups.length) {
    next.active = next.groups.length - 1
  }
  return next
}

// activateTab 把某个 tab 设为其所在组的激活项，并把焦点移到该组。
export function activateTab(wb: Workbench, group: number, tabId: string): Workbench {
  const next = cloneWorkbench(wb)
  const gi = clampGroup(next, group)
  if (!next.groups[gi].tabs.some((t) => t.id === tabId)) return wb
  next.groups[gi].activeId = tabId
  next.active = gi
  return next
}

// setTabContent 把一个 tab 的内容原地换掉（空白 tab 选了种类时用）。
//
// 边界情形：选中的目标已经在别的 tab 里打开了。此时正确的行为是激活那个 tab
// 并把这个空白 tab 关掉——否则用户会得到两个标着同一个文件的 tab，其中一个
// 是刚才的空白页。
export function setTabContent(wb: Workbench, group: number, tabId: string, content: TabContent): Workbench {
  const key = dedupKey(content)
  if (key !== null) {
    const hit = findByKey(wb, key)
    if (hit && hit[1] !== tabId) {
      const closed = closeTab(wb, group, tabId)
      const again = findByKey(closed, key)
      if (again) return activateTab(closed, again[0], again[1])
      return closed
    }
  }
  const next = cloneWorkbench(wb)
  const gi = clampGroup(next, group)
  const idx = next.groups[gi].tabs.findIndex((t) => t.id === tabId)
  if (idx === -1) return wb
  next.groups[gi].tabs[idx] = { id: tabId, content }
  next.groups[gi].activeId = tabId
  next.active = gi
  return next
}

// splitGroup 开启左右分屏；已经是两组时是空操作。新组为空并成为焦点。
export function splitGroup(wb: Workbench): Workbench {
  if (wb.groups.length >= 2) return wb
  const next = cloneWorkbench(wb)
  next.groups.push({ tabs: [], activeId: null })
  next.active = next.groups.length - 1
  return next
}

// nextTerminalSeq 返回下一个终端序号（跨组取最大值 +1，从 1 起）。
export function nextTerminalSeq(wb: Workbench): number {
  let max = 0
  for (const g of wb.groups) {
    for (const t of g.tabs) {
      if (t.content.kind === 'terminal' && t.content.seq > max) max = t.content.seq
    }
  }
  return max + 1
}

// tabTitle 生成 tab 条上显示的标题。
//
// 参数：
//   - c: tab 内容
//   - baseLabel: 基准目录的短名（工作树取分支名或目录名，home 取 'home'）
//
// 终端第一个不带序号（"bash · b2-b3"），第二个起才带——只有一个的时候
// 标个 (1) 是纯噪音。
export function tabTitle(c: TabContent, baseLabel: string): string {
  switch (c.kind) {
    case 'terminal':
      return c.seq <= 1 ? `bash · ${baseLabel}` : `bash · ${baseLabel} (${c.seq})`
    case 'file':
      return c.rel.split('/').pop() || c.rel
    case 'tui':
      return `TUI · ${c.taskId.slice(0, 8)}`
    default:
      return '新建标签页'
  }
}
