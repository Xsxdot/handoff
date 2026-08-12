// counts.ts —— 左栏项目树的聚合计数（纯函数）。
//
// 为什么计数从任务流算而不从树算：树 30s 才刷一次，计数挂在它上面就会有 30 秒
// 的滞后；挂在 2.5s 的任务流上，绿点与数字跟着一起跳（spec §2.2 / §6）。
//
// 计数口径：
//   - dirs：该项目所有机器下的 workspace 总数（探测失败的 location 视为 0）
//   - running：running 状态的任务数（按 project_id + machine 归集）
//   - pending：waiting_answer + waiting_review 的任务数
import type { ProjectLocationNode, ProjectNode, Task } from '../../api/types'

export interface NodeCounts {
  dirs: number
  running: number
  pending: number
}

// workspacesOf 汇总一个位置下的工作树目录数；探测失败的位置没有 workspaces。
function workspacesOf(loc: ProjectLocationNode): number {
  return loc.probe_error ? 0 : loc.workspaces.length
}

// countsForProject 返回一个项目跨所有机器的聚合计数。
export function countsForProject(tasks: Task[], project: ProjectNode): NodeCounts {
  const dirs = project.locations.reduce((n, loc) => n + workspacesOf(loc), 0)
  const mine = tasks.filter((t) => t.project_id === project.project_id)
  return {
    dirs,
    running: mine.filter((t) => t.state === 'running').length,
    pending: mine.filter((t) => t.state === 'waiting_answer' || t.state === 'waiting_review').length,
  }
}

// countsForMachine 返回一个项目在某台机器上的计数（machine ""=本机）。
export function countsForMachine(tasks: Task[], project: ProjectNode, machine: string): NodeCounts {
  const loc = project.locations.find((l) => l.machine === machine)
  const mine = tasks.filter((t) => t.project_id === project.project_id && t.machine === machine)
  return {
    dirs: loc ? workspacesOf(loc) : 0,
    running: mine.filter((t) => t.state === 'running').length,
    pending: mine.filter((t) => t.state === 'waiting_answer' || t.state === 'waiting_review').length,
  }
}
