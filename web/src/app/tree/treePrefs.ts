// treePrefs —— 左栏显示偏好：读写 localStorage + 三个纯函数（spec §1）。
//
// 职责：
//   - 偏好的形状、默认值与持久化（单键 handoff.tree.prefs）
//   - 项目排序、项目隐藏、空闲目录折叠、已结束分组隐藏、文件夹数量隐藏五条规则本身
//
// 边界：
//   - **不认识 React、不认识项目树类型**：三个函数都收泛型 + metrics/info 回调，
//     测试可以用手写数字驱动，不必造一整棵树加一批任务
//   - 不管「搜索期间要不要旁路」：那是调用方的取舍（ProjectTree 决定传不传），
//     规则本身不该知道有搜索这回事
//   - 不碰目录行的既有排序（sortWorkspaces）：这里只折叠，绝不重排
export type ProjectSort = 'active' | 'name' | 'recent'

// TreePrefs 是落盘的全部偏好。v 用于将来改形状时判断要不要整份丢弃。
export interface TreePrefs {
  v: 1
  hideIdleWorktrees: boolean
  // hideArchived：藏掉机器行下的「已结束」分组。默认关——那一组本来就是
  // 为「done 回收了 worktree 之后任务从树上消失」兜的底，默认藏等于把兜底拆掉。
  hideArchived: boolean
  // hideDirCounts：藏掉项目行/机器行右侧的文件夹数量。默认开——这段是「有几个
  // 开发目录」的固有属性，实测是噪声（B198），跟另外两个 hide 默认关相反。
  hideDirCounts: boolean
  projectSort: ProjectSort
  // hiddenProjects 存的是**隐藏名单**（project_id）而不是显示名单：
  // 新登记的项目必须默认可见，否则刚登记完在左栏找不到，看起来像登记失败
  hiddenProjects: string[]
}

export const PREFS_KEY = 'handoff.tree.prefs'

export const DEFAULT_PREFS: TreePrefs = {
  v: 1,
  hideIdleWorktrees: false,
  hideArchived: false,
  hideDirCounts: true,
  // 默认按「谁在动」而不是按名称：左栏的本职是回答「我该看哪」，
  // 按名字找项目已经有搜索框了
  projectSort: 'active',
  hiddenProjects: [],
}

// isPrefs 校验一份解析出来的对象是否真是 TreePrefs。
//
// 逐字段查类型而不是信 as：这份数据落在用户可手改的 localStorage 里，
// 一个 hiddenProjects: null 就能让整棵树渲染时崩掉。
function isPrefs(v: unknown): v is TreePrefs {
  if (typeof v !== 'object' || v === null) return false
  const p = v as Record<string, unknown>
  return (
    p.v === 1 &&
    typeof p.hideIdleWorktrees === 'boolean' &&
    (p.hideArchived === undefined || typeof p.hideArchived === 'boolean') &&
    (p.hideDirCounts === undefined || typeof p.hideDirCounts === 'boolean') &&
    (p.projectSort === 'active' || p.projectSort === 'name' || p.projectSort === 'recent') &&
    Array.isArray(p.hiddenProjects) &&
    p.hiddenProjects.every((x) => typeof x === 'string')
  )
}

// 旧盘没有 hideArchived / hideDirCounts。不 bump v：bump 会把用户的排序和隐藏
// 名单整份丢掉。两条缺省极性相反：已结束分组缺字段当显示，文件夹数量缺字段当藏。
function withOptionalDefaults(p: TreePrefs): TreePrefs {
  return {
    ...p,
    hideArchived: p.hideArchived === true,
    hideDirCounts: p.hideDirCounts !== false,
  }
}

// loadPrefs 读偏好；任何异常都静默回退默认值。
//
// 读不出偏好的正确反应是「按默认显示」而不是报错打断——它是视图偏好，
// 不是业务数据。但会 console.warn 一次带上被丢弃的原文，坏偏好是真实排查线索。
export function loadPrefs(): TreePrefs {
  let raw: string | null = null
  try {
    raw = localStorage.getItem(PREFS_KEY)
  } catch {
    return DEFAULT_PREFS   // 隐私模式下 localStorage 可能直接抛
  }
  if (raw === null) return DEFAULT_PREFS
  try {
    const parsed: unknown = JSON.parse(raw)
    if (isPrefs(parsed)) return withOptionalDefaults(parsed)
    console.warn('[treePrefs] 偏好形状不认识，已回退默认值：', raw.slice(0, 200))
  } catch (err) {
    console.warn('[treePrefs] 偏好不是合法 JSON，已回退默认值：', raw.slice(0, 200), err)
  }
  return DEFAULT_PREFS
}

// savePrefs 落盘一份偏好；写失败只警告不抛（配额满/隐私模式）。
export function savePrefs(p: TreePrefs): void {
  try {
    localStorage.setItem(PREFS_KEY, JSON.stringify(p))
  } catch (err) {
    console.warn('[treePrefs] 偏好写入失败，本次改动只在内存里生效', err)
  }
}

// ProjectMetrics 是一个项目行的三个排序键。
// updatedAt 是该项目下任务 updated_at 的最大值；空串 = 一条任务都没有，视为最旧。
export interface ProjectMetrics {
  active: number
  updatedAt: string
  name: string
}

// timeRank 把 RFC3339 字符串换成可比较的毫秒；空串与非法值都当最旧。
function timeRank(s: string): number {
  if (s === '') return -Infinity
  const t = Date.parse(s)
  return Number.isNaN(t) ? -Infinity : t
}

// sortProjects 返回排好序的新数组，不改入参。
//
// 三档的末位一律以名称升序兜底：这不是排序意图，是**稳定性**。前键全等时若不给
// 确定次序，行会随每次 2.5s 任务流心跳无缘无故重排（与 sortWorkspaces 末位的
// path ↑ 同一条理由）。
export function sortProjects<T>(list: T[], metricsOf: (x: T) => ProjectMetrics, mode: ProjectSort): T[] {
  return [...list].sort((a, b) => {
    const ma = metricsOf(a)
    const mb = metricsOf(b)
    if (mode === 'active' && ma.active !== mb.active) return mb.active - ma.active
    if (mode === 'recent') {
      const ra = timeRank(ma.updatedAt)
      const rb = timeRank(mb.updatedAt)
      if (ra !== rb) return rb - ra
    }
    return ma.name.localeCompare(mb.name)
  })
}

// splitHiddenProjects 按隐藏名单剔除项目，并报出剔了几个。
//
// 返回 hiddenCount 而不是 hidden 列表：界面只需要在「项目 N」旁说一句
// 「已隐藏 2」，被藏起来的项目不在树上出现（要拿回来去菜单里勾）。
export function splitHiddenProjects<T>(
  list: T[],
  idOf: (x: T) => string,
  hidden: string[],
): { shown: T[]; hiddenCount: number } {
  if (hidden.length === 0) return { shown: list, hiddenCount: 0 }
  const set = new Set(hidden)
  const shown = list.filter((x) => !set.has(idOf(x)))
  return { shown, hiddenCount: list.length - shown.length }
}

// WorkspaceIdleInfo 是折叠判据的三个输入。
// active = 该目录下 running + waiting_answer + waiting_review 的任务数。
export interface WorkspaceIdleInfo {
  isMain: boolean
  selected: boolean
  active: number
}

// splitIdleWorkspaces 把无活跃任务的目录拆到 hidden 里，保持原顺序。
//
// 两条恒不折叠的豁免：
//   - 主工作树：它是项目在这台机器上的家，不是任务分支。藏掉它，用户对
//     「主目录在第一行」的肌肉记忆当场失效
//   - 当前选中目录：选中态的行凭空消失是 bug 观感，不是「界面变干净了」
//
// hideIdle=false 时原样返回（连数组都不新建），调用方在搜索期间靠传 false 旁路。
export function splitIdleWorkspaces<T>(
  list: T[],
  infoOf: (x: T) => WorkspaceIdleInfo,
  hideIdle: boolean,
): { shown: T[]; hidden: T[] } {
  if (!hideIdle) return { shown: list, hidden: [] }
  const shown: T[] = []
  const hidden: T[] = []
  for (const x of list) {
    const info = infoOf(x)
    if (info.isMain || info.selected || info.active > 0) shown.push(x)
    else hidden.push(x)
  }
  return { shown, hidden }
}
