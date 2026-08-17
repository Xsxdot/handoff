// sortWorkspaces —— 左栏目录行的排序规则（纯函数，无 React 依赖）。
//
// 职责：把一个机器节点下的工作树按「现在最该看哪个」排好序。
//
// 边界：
//   - 不认识任务、不认识工单：三个排序键由调用方经 metricsOf 回调提供。
//     这样测试可以用手写数字驱动，不必造一整棵项目树加一批任务
//   - 不改入参，返回新数组
//
// 排序规则（spec §1.1）：主工作树恒第一，其余按
//   工单数 ↓ → 任务数 ↓ → 创建时间 ↓ → path ↑
import type { Workspace } from '../../api/types'

// WorkspaceMetrics 是一个目录行的三个排序键。
//
// createdAt 是 RFC3339Nano 字符串；空串表示 agentd 取不到，按**最旧**处理
// （见 createdRank）。
export interface WorkspaceMetrics {
  tickets: number
  tasks: number
  createdAt: string
}

// createdRank 把创建时间换成可比较的毫秒数；取不到时返回 -Infinity。
//
// 为什么空串与非法值都当最旧而不是当最新：这个字段的缺席只有两种来源——老
// agentd 不上报，或 stat 失败。两种都意味着「这个工作树的时间信息不可信」，
// 而把不可信的东西排到最前面会挤掉真正新建的分支。
function createdRank(createdAt: string): number {
  if (createdAt === '') return -Infinity
  const t = Date.parse(createdAt)
  return Number.isNaN(t) ? -Infinity : t
}

// sortWorkspaces 返回排好序的新数组。
//
// 参数：
//   - list: 一个机器节点下的工作树（原样，不要求已排序）
//   - metricsOf: 给一个工作树算出它的三个排序键
//
// 返回：新数组；入参不被修改。
//
// 主工作树（is_main）恒排第一且不参与其余比较：它不是一个任务分支，是这个
// 项目在这台机器上的家。让它被别的分支的工单顶下去，用户对「主目录在第一行」
// 的肌肉记忆当场失效，而那条记忆比「主目录也参与排序」更值钱（spec §1.1）。
//
// 末位的 path 升序不是排序意图，是**稳定性兜底**：前三个键全等时若不给确定
// 次序，不同引擎的 sort 结果可能不同，行会随每次 2.5s 任务流心跳无缘无故重排。
export function sortWorkspaces(
  list: Workspace[],
  metricsOf: (ws: Workspace) => WorkspaceMetrics,
): Workspace[] {
  return [...list].sort((a, b) => {
    if (a.is_main !== b.is_main) return a.is_main ? -1 : 1
    if (a.is_main && b.is_main) return a.path < b.path ? -1 : a.path > b.path ? 1 : 0
    const ma = metricsOf(a)
    const mb = metricsOf(b)
    if (ma.tickets !== mb.tickets) return mb.tickets - ma.tickets
    if (ma.tasks !== mb.tasks) return mb.tasks - ma.tasks
    const ra = createdRank(ma.createdAt)
    const rb = createdRank(mb.createdAt)
    if (ra !== rb) return rb - ra
    return a.path < b.path ? -1 : a.path > b.path ? 1 : 0
  })
}
