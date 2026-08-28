// search.ts —— 左栏项目树的过滤（纯函数，无 React 依赖）。
//
// 职责：
//   - 把一次查询串归一化，按四类字段（项目名 / 机器名 / 目录名 / 任务名）
//     在树上求出可见集合
//   - 给出「项目 N」的计数与「是否零结果」的判定
//
// 边界：
//   - 不管 UI：不知道搜索框长什么样、⌘K 是什么、折叠态怎么存
//   - 不改入参：返回的是裁剪后的新对象，原树不被修改
//   - 不做高亮、不做模糊匹配、不做拼音——includes 够用（spec §11）
//
// 为什么单独成文件而不塞进 ProjectTree.tsx：可见性是一条递归规则，塞在
// 组件里只能靠渲染断言间接测。仓库既有同款模式——board/filter.ts（看板
// 筛选）、tree/counts.ts（树计数）都是「纯函数 + 独立测试文件」。
import type { ProjectLocationNode, ProjectNode, ProjectTreeResp, Task, Workspace } from '../../api/types'
import { archivedKey, archivedTasks } from './archived'
import type { OpenedWorkbenchItem } from '../workbench/tabs'
// TreeFilter 是一次过滤的完整结果。projects 已按可见性裁剪，
// 调用方直接遍历即可，不需要再判一次。
export interface TreeFilter {
  query: string
  projects: ProjectNode[]
  projectCount: number
  unassignedTasks: Task[]
  unownedNames: string[]
  isEmpty: boolean
}

// hit 是全文唯一的匹配判据：大小写不敏感的子串包含。
// 提成一个函数是为了让「四类字段用的是同一条判据」这件事在代码里看得见。
function hit(text: string, q: string): boolean {
  return text.toLowerCase().includes(q)
}

// machineText 是机器名参与匹配时的文本。
// 用「本机」而不是空串：机器名为 "" 表示本机，界面上显示的也是「本机」，
// 搜索面必须与眼睛看到的一致——否则用户搜「本机」会一无所获。
function machineText(machine: string): string {
  return machine === '' ? '本机' : machine
}

// dirText 是目录参与匹配时的文本，口径与 ProjectTree 的 dirLabel 一致：
// 有 branch 用 branch，否则用路径末段。
function dirText(ws: Workspace): string {
  if (ws.branch !== '') return ws.branch
  const seg = ws.path.split('/').filter(Boolean)
  return seg.length > 0 ? seg[seg.length - 1] : ws.path
}

// taskText 是任务参与匹配时的文本，口径与 ProjectTree 的 taskName 一致。
function taskText(t: Task): string {
  return t.name || t.plan_summary || '（无名称）'
}

// openedText 把工作台项的可见标题与内容自身的定位字段合在一起。
// 文件 tab 的标题可能被用户入口改写，但相对路径仍是左栏搜索应能找到的真实祖先线索；
// 终端的 rel 与 TUI taskId 同理。只读投影数据，不改变 tab 或树节点。
function openedText(item: OpenedWorkbenchItem): string {
  const detail = item.content.kind === 'file'
    ? item.content.rel
    : item.content.kind === 'terminal'
      ? item.content.rel ?? ''
      : item.content.kind === 'tui'
        ? item.content.taskId
        : ''
  return [item.label, detail, item.base.key, item.base.path, item.base.label, machineText(item.base.machine)].join(' ')
}

// tasksOfWorkspace 挑出挂在某个目录下的任务。
// 判据是 work_dir 与 workspace.path 路径等值；work_dir 为空表示原地模式，
// 挂到主目录——与 proto.Task.Workdir() 的回退语义一致。
function tasksOfWorkspace(tasks: Task[], project: ProjectNode, machine: string, ws: Workspace): Task[] {
  return tasks.filter((t) => {
    if (t.project_id !== project.project_id || t.machine !== machine) return false
    if (t.work_dir === '') return ws.is_main
    return t.work_dir === ws.path
  })
}

// filterTree 按查询串裁剪项目树。
//
// 参数：
//   - tree: GET /api/projects/tree 的响应
//   - tasks: 任务流的当前快照（用于任务名匹配与任务归属判定）
//   - rawQuery: 用户输入的原始查询串
//
// 返回：
//   - TreeFilter，projects 已裁剪；rawQuery 去空白后为空时原样返回全树
//
// 注意：
//   - **可见性只有一条规则：节点可见 ⟺ 自身命中 或 任一后代命中。**
//     且自身命中时整棵子树都保留——搜一个项目名，是要看它下面的全部
//     机器、目录、任务，而不是只看到一个光秃秃的项目行。
//   - 「未归属」分组参与过滤但**不计入 projectCount**：它不是一个项目，
//     是个收纳箱。算进去会出现「项目 3」但下面只有 2 个能展开的项目行。
export function filterTree(
  tree: ProjectTreeResp,
  tasks: Task[],
  rawQuery: string,
  openedItems: ReadonlyArray<OpenedWorkbenchItem> = [],
): TreeFilter {
  const q = rawQuery.trim().toLowerCase()
  const unassignedAll = tasks.filter((t) => t.project_id === '')

  if (q === '') {
    return {
      query: '',
      projects: tree.projects,
      projectCount: tree.projects.length,
      unassignedTasks: unassignedAll,
      unownedNames: tree.unowned,
      isEmpty: tree.projects.length === 0 && unassignedAll.length === 0 && tree.unowned.length === 0,
    }
  }

  // 已结束任务（B288 起为项目内全部终态）按项目归集。它们可能不再挂任何
  // workspace，但搜到它们时项目行必须可见——「已结束」行就挂在项目任务组尾部。
  const archived = archivedTasks(tasks)

  const projects: ProjectNode[] = []
  for (const project of tree.projects) {
    const projectHit = hit(project.name, q)
    const archivedHit = (archived.get(archivedKey(project.project_id)) ?? [])
      .some((t) => hit(taskText(t), q))
    const locations: ProjectLocationNode[] = []
    for (const loc of project.locations) {
      const machineOpenedHit = openedItems.some((item) =>
        item.base.projectName === project.name && item.base.machine === loc.machine && hit(openedText(item), q),
      )
      const machineHit = projectHit || hit(machineText(loc.machine), q)

      // 项目或机器自身命中 → 整层目录原样保留；否则逐个目录判
      const workspaces = machineHit
        ? loc.workspaces
        : loc.workspaces.filter((ws) =>
            hit(dirText(ws), q) ||
            tasksOfWorkspace(tasks, project, loc.machine, ws).some((t) => hit(taskText(t), q)),
          )

      const openedWorkspaces = loc.workspaces.filter((ws) =>
        openedItems.some((item) =>
          item.base.projectName === project.name && item.base.machine === loc.machine && item.base.path === ws.path && (
            hit(openedText(item), q)
          ),
        ),
      )
      const mergedWorkspaces = machineHit
        ? workspaces
        : [...workspaces, ...openedWorkspaces.filter((ws) => !workspaces.some((visible) => visible.path === ws.path))]

      if (machineHit || machineOpenedHit || mergedWorkspaces.length > 0) {
        locations.push({ ...loc, workspaces: mergedWorkspaces })
      }
    }

    if (projectHit || archivedHit || locations.length > 0) projects.push({ ...project, locations })
  }

  const unassignedTasks = unassignedAll.filter((t) => hit(taskText(t), q))
  const unownedNames = tree.unowned.filter((name) => hit(name, q))

  return {
    query: q,
    projects,
    projectCount: projects.length,
    unassignedTasks,
    unownedNames,
    isEmpty: projects.length === 0 && unassignedTasks.length === 0 && unownedNames.length === 0,
  }
}
