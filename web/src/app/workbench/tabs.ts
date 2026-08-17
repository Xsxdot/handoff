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
  // rel 是终端要起的工作树子目录（空串/缺席 = 工作树根），右键「在终端中
  // 打开」时带上，其余入口不带。
  | { kind: 'terminal'; seq: number; sessionId?: string; rel?: string }
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

// MAX_GROUPS 是中央区最多能分出的栏数。
//
// 为什么是 3 而不是「无限」：1280px 宽度下减去左树 260 与文件树 280，中央区
// 只剩 740px；三栏时每栏 ~245px，刚够放下一个 tab 标题加关闭按钮（这也是
// MIN_PANE_PX 的来源）。第四栏必然要靠横向滚动或折叠 tab 条才能存在，那是
// 另一个形态问题，不是把这个数字改大就能解决的。
export const MAX_GROUPS = 3

// MIN_PANE_PX 是单栏的最小宽度：拖拽时两侧都夹在它之上，不允许把一栏压成一条缝。
export const MIN_PANE_PX = 240

// Workbench 是一个基准目录下的全部 tab：一到 MAX_GROUPS 组，外加「哪一组是焦点」
// 与各组的宽度权重。
export interface Workbench {
  groups: TabGroup[]
  active: number
  // sizes 是各栏的宽度权重，与 groups **等长**（这条不变式所有改组数的函数都要维持），
  // 渲染时作为 flexGrow 用。
  //
  // 为什么是相对权重不是像素：容器宽度会随窗口大小、随左右两栏的显隐变化，存像素
  // 等于每次 resize 都要重算一遍，还要处理「加起来不等于容器宽」这种对不上的状态。
  sizes: number[]
}

export const EMPTY_WORKBENCH: Workbench = {
  groups: [{ tabs: [], activeId: null }],
  active: 0,
  sizes: [1],
}

// evenSizes 返回 n 等分的权重数组。
//
// 组数一变（分屏、关空一组）就调它重置，**不做「从当前栏借一半」**：借了之后
// 关掉该还给谁？还原主还是均摊？两种都能自圆其说，于是就有了第三种状态。
// 等分是唯一不需要记「这份宽度是从谁那儿来的」的规则。
function evenSizes(n: number): number[] {
  return Array.from({ length: n }, () => 1)
}

// availablePaneWidth 返回中央区各栏实际可以瓜分的宽度。
//
// 参数：
//   - parentWidth: 外层 flex 容器的布局宽度（包含分隔条）
//   - separatorWidths: 容器内所有分隔条的实际宽度
//
// 返回：扣除分隔条后的可分配宽度；如果布局数据异常导致结果为负，返回 0，
// 让调用方走「量不到宽度」的退化路径，而不是把负数传给比例换算。
export function availablePaneWidth(parentWidth: number, separatorWidths: number[]): number {
  const separatorWidth = separatorWidths.reduce((total, width) => total + width, 0)
  return Math.max(0, parentWidth - separatorWidth)
}

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
    sizes: [...wb.sizes],
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
    // 焦点接替**相邻**组，不是写死回第 0 组。两组时 min(gi, 0) 恒等于 0，所以这个
    // 写死一直没暴露；三栏时它会让焦点从被关掉的最右栏莫名跳到最左边。
    next.active = Math.min(gi, next.groups.length - 1)
    next.sizes = evenSizes(next.groups.length)
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

// setTabContent 把一个 tab 的内容原地换掉（空白 tab 选了种类、终端回写会话 id、
// 文件回写草稿都走它）。
//
// **它只换内容，不动 activeId / active。** 这两件事曾经焊在一起，代价是任何一次
// 后台回写都变成一次导航：FileTab 在**卸载时**回写草稿（Shell 的 onDraftChange），
// 于是用户点开一个空白 tab → FileTab 卸载 → 回写 → 焦点被拽回刚离开的文件 tab，
// 表现为「点不进新标签页」。干净文件也一样（回调无条件触发），终端的 onSession
// 同一条路。切 tab 有专门的 activateTab，调用方需要时自己调。
//
// 边界情形：选中的目标已经在别的 tab 里打开了。此时正确的行为是激活那个 tab
// 并把这个空白 tab 关掉——否则用户会得到两个标着同一个文件的 tab，其中一个
// 是刚才的空白页。**这一支保留激活**：它是用户刚做完一次选择动作，跳过去是他要的。
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
  return next
}

// splitGroupAt 在 index 处插入一个空栏并聚焦它；已到 MAX_GROUPS 时**原样返回
// 同一个对象**（调用方可据此跳过一次无谓的 setState）。宽度重置为等分。
//
// 参数：
//   - index: 新栏插在哪个位置（0 = 最左）。越界时夹到 [0, groups.length]
// 返回：插入后的新 Workbench；达到 MAX_GROUPS 时返回原对象。
//
// 关于「插入会不会打乱谁的下标」：曾经不能这么做——Shell 把 (组下标, tabId)
// 存进了确认弹层的 state，中间插入会让存着的下标指向别的栏。那条耦合已经在
// 本 task 里拔掉了（Shell 改为按 tabId 反查），所以插入现在是安全的。
// **如果你在别处又看到有人把组下标存进跨事件的 state，那是在把这个坑挖回来。**
export function splitGroupAt(wb: Workbench, index: number): Workbench {
  if (wb.groups.length >= MAX_GROUPS) return wb
  const next = cloneWorkbench(wb)
  const at = Math.max(0, Math.min(index, next.groups.length))
  next.groups.splice(at, 0, { tabs: [], activeId: null })
  next.active = at
  next.sizes = evenSizes(next.groups.length)
  return next
}

// splitGroup 在末尾再开一栏。⌘D 与面包屑的分屏按钮走它，行为与本函数存在
// 之前逐字节一致。
export function splitGroup(wb: Workbench): Workbench {
  return splitGroupAt(wb, wb.groups.length)
}

// resizeGroups 把第 dividerIndex 个分隔条左右两栏的宽度重新分配。
//
// 参数：
//   - dividerIndex: 分隔条下标，左邻是 groups[dividerIndex]、右邻是 [dividerIndex+1]
//   - delta: 本次位移占容器宽度的**比例**，正数 = 分隔条右移（左栏变宽）
//   - minRatio: 单栏允许的最小占比，由调用方用 MIN_PANE_PX / 容器宽度算好传进来
//
// 为什么 minRatio 由调用方算：容器宽度只有 DOM 知道。纯函数拿到的是无量纲比例，
// 测试里直接给 0.2 就能断言夹紧行为，不用 mock 布局。
//
// 无事可做时**返回原对象**（引用相等），调用方可据此跳过一次 setState：拖拽是
// 高频事件，贴着下限还在拖时不该每帧都重渲染一次整个中央区。
export function resizeGroups(
  wb: Workbench,
  dividerIndex: number,
  delta: number,
  minRatio: number,
): Workbench {
  const i = dividerIndex
  const j = dividerIndex + 1
  if (i < 0 || j >= wb.sizes.length || wb.sizes.length !== wb.groups.length) {
    // 不变式被破坏才会走到这里（渲染层的分隔条数量恒等于 groups.length - 1）。
    // 静默返回会让「拖了没反应」查无对证，留一条带上下文的 warn
    console.warn('分隔条下标越界或栏宽数量不匹配，本次拖拽忽略', {
      dividerIndex,
      groups: wb.groups.length,
      sizes: wb.sizes.length,
    })
    return wb
  }
  const total = wb.sizes.reduce((a, b) => a + b, 0)
  const left = wb.sizes[i] / total
  const right = wb.sizes[j] / total
  const min = Math.max(0, minRatio)
  // 两栏加起来都容不下两个下限：容器已经窄到没有可分配的空间，此时任何夹紧规则
  // 都会算出自相矛盾的结果。拒绝改动，交给容器横向滚动（spec §2.3）
  if (min * 2 > left + right) return wb
  let d = delta
  if (left + d < min) d = min - left
  if (right - d < min) d = right - min
  if (d === 0) return wb
  const next = cloneWorkbench(wb)
  next.sizes[i] = (left + d) * total
  next.sizes[j] = (right - d) * total
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
