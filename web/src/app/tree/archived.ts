// archived.ts —— 左栏「已结束」分组的数据源（纯函数，无 React 依赖）。
//
// 为什么需要这个分组：`handoff done` 会把 agentd 自建的 worktree 删掉（分支保留），
// 于是已完成任务的 work_dir 指向一个不再存在的目录——树上没有任何一行能挂住它，
// 任务就整个从界面上消失了。实测本机 5 条、mac-02 60 条终态任务全部处于这个状态。
// 消失不是「归档」，是**静默丢失**：想回看某次派发做了什么，界面上无从下手。
//
// 职责：
//   - 找出「属于某项目 + 某机器、但目录已不在树上」的终态任务
//   - 按 项目+机器 归集，供 ProjectTree 在机器节点下挂一个可折叠分组
//
// 边界：
//   - 只收终态（completed / failed）。非终态任务的目录不在，是另一类问题
//     （工作树被人手删、机器探测失败），不该被一个叫「已结束」的分组吞掉
//   - 未归属任务（project_id===""）不收：树末尾的「未归属」分组已经在管它们
//   - 不做展示、不排序：任务流本身按 created_at 降序，原样保留
import type { ProjectTreeResp, Task } from '../../api/types'

// ARCHIVED_LABEL 是分组行的名字。用「已结束」而不是「已完成」：这里同时收
// completed 与 failed，叫「已完成」会把失败的任务讲成成功的。
export const ARCHIVED_LABEL = '已结束'

// ARCHIVED_TITLE 是分组行的 tooltip，说清楚这些任务为什么不在目录下面。
export const ARCHIVED_TITLE = '已完成 / 已失败的任务，工作目录已被回收（分支仍在仓库里）'

// isTerminalState 判定任务是否已终结。口径与后端 proto.TaskState.IsTerminal 一致。
export function isTerminalState(state: string): boolean {
  return state === 'completed' || state === 'failed'
}

// archivedKey 是归集键：项目 + 机器。
//
// 分隔符用 \u0000 而不是 ':'：机器名来自用户配置，真出现冒号时 `a:b` + `c` 与
// `a` + `b:c` 会撞成同一个键；NUL 不可能出现在这两个值里。
export function archivedKey(projectID: string, machine: string): string {
  return `${projectID}\u0000${machine}`
}

// archivedTasks 求出每个「项目+机器」下目录已不在的终态任务。
//
// 参数：
//   - tree: 项目树（**必须是未经搜索裁剪的原树**——用裁剪过的树会把被搜索
//     过滤掉的目录误判为「已不在」，于是正常任务突然涌进这个分组）
//   - tasks: 任务流快照
//
// 返回：archivedKey → 任务列表。没有这类任务的键不出现在结果里（调用方按
// 「取不到就不渲染分组」处理）。
export function archivedTasks(tree: ProjectTreeResp, tasks: Task[]): Map<string, Task[]> {
  // 先把树上每个位置的目录路径摊平成集合，避免对每个任务做一次 O(目录数) 扫描
  const known = new Map<string, { paths: Set<string>; hasMain: boolean }>()
  for (const project of tree.projects) {
    for (const loc of project.locations) {
      known.set(archivedKey(project.project_id, loc.machine), {
        paths: new Set(loc.workspaces.map((ws) => ws.path)),
        hasMain: loc.workspaces.some((ws) => ws.is_main),
      })
    }
  }

  const out = new Map<string, Task[]>()
  for (const t of tasks) {
    if (t.project_id === '' || !isTerminalState(t.state)) continue
    const key = archivedKey(t.project_id, t.machine)
    const loc = known.get(key)
    // 项目在这台机器上根本没有位置：连机器行都没有，挂不上去。这类任务此处
    // 不收——它缺的是整个位置，不是一个目录，混进来只会让分组的含义变糊
    if (!loc) continue
    // work_dir 为空 = 原地模式，归属主目录；主目录不在了才算目录已回收
    const orphan = t.work_dir === '' ? !loc.hasMain : !loc.paths.has(t.work_dir)
    if (!orphan) continue
    const list = out.get(key)
    if (list) list.push(t)
    else out.set(key, [t])
  }
  return out
}
