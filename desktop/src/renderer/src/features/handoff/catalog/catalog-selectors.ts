/**
 * Handoff catalog 选择器：从 bootstrap 派生 Project → Machine/Location → Workspace → Task 树。
 *
 * 职责：
 *   - selectProjectTree：按稳定 ID 归组，供 ProjectTree 消费
 *   - 层级固定：Project → Machine/Location → main/worktree → handoff task
 *
 * 边界：
 *   - 纯函数，无副作用；消费方（ProjectTree）负责交互
 */
import type { BootstrapResponse } from '../../../../../shared/handoff/contracts'

export type TreeLocation = {
  locationId: string
  machineId: string
  machineName: string
  machineKind: string
  machineStatus: string
  role: string
  workspaces: TreeWorkspace[]
}

export type TreeWorkspace = {
  workspaceId: string
  kind: string
  path: string
  branch: string
  availability: string
  machineId: string
  tasks: TaskRow[]
}

export type TaskRow = {
  taskId: string
  name: string
  executor: string
  state: string
  attention: number
  workspaceId: string
}

export type TreeProject = {
  projectId: string
  name: string
  workspaceCount: number
  runningTaskCount: number
  locations: TreeLocation[]
}

/** 把 bootstrap 投影为项目树（Project → Machine/Location → Workspace → Task）。 */
export function selectProjectTree(bootstrap: BootstrapResponse): TreeProject[] {
  const machineById = new Map(bootstrap.machines.map((m) => [m.id, m]))

  return bootstrap.projects.map((project) => {
    const locations = bootstrap.locations
      .filter((l) => l.project_id === project.id)
      .map((loc): TreeLocation => {
        const machine = machineById.get(loc.machine_id)
        const workspaces = bootstrap.workspaces
          .filter((w) => w.location_id === loc.id)
          .sort((a, b) => (a.kind === 'main' ? -1 : b.kind === 'main' ? 1 : 0))
          .map((ws): TreeWorkspace => ({
            workspaceId: ws.id,
            kind: ws.kind,
            path: ws.path,
            branch: ws.branch ?? '',
            availability: ws.availability,
            machineId: ws.machine_id,
            tasks: bootstrap.active_task_summaries
              .filter((t) => t.workspace_id === ws.id)
              .map((t): TaskRow => ({
                taskId: t.task_id,
                name: t.name,
                executor: t.executor,
                state: t.state,
                attention: t.attention,
                workspaceId: t.workspace_id
              }))
          }))
        return {
          locationId: loc.id,
          machineId: loc.machine_id,
          machineName: machine?.display_name ?? loc.machine_id,
          machineKind: machine?.kind ?? 'remote',
          machineStatus: machine?.status ?? 'unavailable',
          role: loc.role,
          workspaces
        }
      })

    const allWorkspaces = locations.flatMap((l) => l.workspaces)
    const runningTaskCount = allWorkspaces.reduce(
      (acc, ws) => acc + ws.tasks.filter((t) => t.state === 'running').length,
      0
    )

    return {
      projectId: project.id,
      name: project.name,
      workspaceCount: allWorkspaces.length,
      runningTaskCount,
      locations
    }
  })
}
