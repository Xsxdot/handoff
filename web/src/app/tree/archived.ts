// archived.ts —— 左栏「已结束」分组的数据源（纯函数，无 React 依赖）。
//
// 这个分组最初只为「目录已被回收的终态任务」兜底（`handoff done` 会删掉 agentd
// 自建的 worktree，分支保留），否则这些任务会从界面上静默消失。
//
// B288 起口径收紧为「终态一律进已结束，不论目录在不在」：任务组只留未终态任务
// 和已打开项，completed / failed 不再混在普通任务行里——历史派发堆积在任务组
// 里会淹掉正在做的活（真机实测：几条 completed 混在一屏 running 之间无从分辨）。
// 因此本模块不再读项目树、不再判目录是否还在，只按终态过滤、按项目归集。
//
// 职责：
//   - 找出项目内全部终态（completed / failed）任务
//   - 按项目归集，供 ProjectTree 在任务分组尾部挂一个可折叠的「已结束」行
//
// 边界：
//   - 只收终态。未终态任务不管状态多旧都留在任务组
//   - 未归属任务（project_id===""）不收：树末尾的「未归属」分组在管它们
//   - 不做展示、不排序：任务流本身按 created_at 降序，原样保留
import type { Task } from '../../api/types'

// ARCHIVED_LABEL 是分组行的名字。用「已结束」而不是「已完成」：这里同时收
// completed 与 failed，叫「已完成」会把失败的任务讲成成功的。
export const ARCHIVED_LABEL = '已结束'

// ARCHIVED_TITLE 是分组行的 tooltip。B288 起目录在不在不再参与判定，
// 文案去掉「工作目录已被回收」的旧解释。
export const ARCHIVED_TITLE = '已完成 / 已失败的任务'

// isTerminalState 判定任务是否已终结。口径与后端 proto.TaskState.IsTerminal 一致。
export function isTerminalState(state: string): boolean {
  return state === 'completed' || state === 'failed'
}

// archivedKey 是归集键：项目维度。
//
// B288 前是「项目+机器」双键（\u0000 分隔），因为分组挂在机器行下面；现在
// 「已结束」行挂在项目的任务分组尾部，键收敛为 projectID 本身。
export function archivedKey(projectID: string): string {
  return projectID
}

// archivedTasks 求出每个项目下全部终态任务。
//
// 参数：tasks 任务流快照。
// 返回：archivedKey(projectID) → 该项目全部终态任务（保持任务流原顺序）。
// 没有终态任务的项目不出现在结果里（调用方按「取不到就不渲染分组」处理）。
export function archivedTasks(tasks: Task[]): Map<string, Task[]> {
  const out = new Map<string, Task[]>()
  for (const t of tasks) {
    if (t.project_id === '' || !isTerminalState(t.state)) continue
    const key = archivedKey(t.project_id)
    const list = out.get(key)
    if (list) list.push(t)
    else out.set(key, [t])
  }
  return out
}
