// filter.ts —— 看板筛选状态的唯一真相。
//
// 职责：
//   - 定义 BoardFilter 的形状与全部写入规则（纯函数，无 React 依赖）
//   - 把任务列表按 filter 过滤（全部在客户端做）
//
// 边界：
//   - 不碰 React state、不发请求；组件层只负责调用这些纯函数
//   - 不做后端过滤：看板已 2.5s 全量拉 /api/tasks，改走后端只会让筛选
//     变成一次网络往返、并与轮询节奏打架（spec §3.1）
//
// 为什么左栏与顶部下拉共用同一个对象而不是两套联动状态：两套状态一定会
// 出现"左栏选了 A、顶部显示 B、看板按 C 筛"的第三种状态，用户看到的是
// "筛了个项目结果一个任务都没有"。一个对象两个编辑入口，永远不会打架。
// W4 引入 workbench 后点目录应切换工作区而非筛看板，本文件届时须重写。
import type { ProjectNode, Task } from '../../api/types'

export type BoardFilter = {
  projects: Set<string>     // project_id 集合；空集 = 不按项目筛（全部），不是"一个都不选"
  machine: string | null    // 机器名（""=本机）；null = 不按机器筛
  workspace: string | null  // 工作树路径；null = 不按工作树筛
  search: string
  pendingOnly: boolean
}

export const EMPTY_FILTER: BoardFilter = {
  projects: new Set<string>(),
  machine: null,
  workspace: null,
  search: '',
  pendingOnly: false,
}

// cloneFilter 复制一份 filter——所有写入函数都以「拷贝再改」的方式返回新对象，
// 保证组件层 React state 里永远是新引用，联动不会产生第三条路。
function cloneFilter(f: BoardFilter): BoardFilter {
  return {
    projects: new Set(f.projects),
    machine: f.machine,
    workspace: f.workspace,
    search: f.search,
    pendingOnly: f.pendingOnly,
  }
}

// setProjects 设项目多选集（顶部下拉）。若当前 machine 不再属于任一选中项目，
// 一并清空 machine 与 workspace——否则会出现"筛了个项目结果一个任务都没有"。
export function setProjects(f: BoardFilter, ids: Set<string>, tree: ProjectNode[]): BoardFilter {
  const next = cloneFilter(f)
  next.projects = new Set(ids)
  if (next.machine !== null) {
    const ownsMachine = ids.size > 0 && tree.some((p) => ids.has(p.project_id) && p.locations.some((l) => l.machine === next.machine))
    if (!ownsMachine) {
      next.machine = null
      next.workspace = null
    }
  }
  return next
}

// setMachine 设机器筛选（顶部下拉）。空串 = 本机，不是"不筛"。保留项目，清空 workspace。
export function setMachine(f: BoardFilter, machine: string): BoardFilter {
  const next = cloneFilter(f)
  next.machine = machine
  next.workspace = null
  return next
}

// setSearch 设搜索词（大小写不敏感）。
export function setSearch(f: BoardFilter, search: string): BoardFilter {
  const next = cloneFilter(f)
  next.search = search
  return next
}

// setPendingOnly 切换「只看待处理」：waiting_answer + waiting_review。
export function setPendingOnly(f: BoardFilter, pendingOnly: boolean): BoardFilter {
  const next = cloneFilter(f)
  next.pendingOnly = pendingOnly
  return next
}

// projectNameById 在项目树里反查 project_id 的显示名；找不到原样返回空串。
function projectNameById(tree: ProjectNode[], id: string): string {
  return tree.find((p) => p.project_id === id)?.name ?? ''
}

// applyFilter 把任务列表按 filter 过滤。关键分支各带「为什么」注释：
//
//   - machine 用 f.machine !== null 判断而不是真值——"" 是「本机」的合法取值，
//     用真值判断会把本机任务永远筛没（本机是绝大多数任务的去处，这是头号 bug 位）
//   - projects 为空集时不筛项目：此时未归属任务（project_id === ""）必须保留，
//     不静默丢弃（spec §8）
//   - pendingOnly 只认 waiting_answer / waiting_review
export function applyFilter(tasks: Task[], f: BoardFilter, tree: ProjectNode[]): Task[] {
  return tasks.filter((task) => {
    if (f.projects.size > 0 && !f.projects.has(task.project_id)) return false
    if (f.machine !== null && task.machine !== f.machine) return false
    if (f.workspace !== null && task.work_dir !== f.workspace) return false
    if (f.pendingOnly && task.state !== 'waiting_answer' && task.state !== 'waiting_review') return false
    if (f.search) {
      const q = f.search.toLowerCase()
      const projectName = projectNameById(tree, task.project_id)
      const hit =
        task.name.toLowerCase().includes(q) ||
        task.plan_summary.toLowerCase().includes(q) ||
        projectName.toLowerCase().includes(q) ||
        task.executor.toLowerCase().includes(q)
      if (!hit) return false
    }
    return true
  })
}
