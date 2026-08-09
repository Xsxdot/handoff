/**
 * Handoff ProjectTree：左栏项目树（Project → Machine/Location → main/worktree → task）。
 *
 * 职责：
 *   - 展示项目、机器、工作区与任务层级
 *   - 点击 Workspace 选中；点击 task 先选所属 Workspace
 *   - 远端 unavailable 时仍展示最后元数据，但明确标记「不可用」
 *
 * 边界：
 *   - 使用稳定 ID key；不依赖 Orca 全局 store
 *   - 计数由 catalog 投影提供，不由 renderer 临时推导
 */
import type { CatalogState } from '../catalog/catalog-store'
import { selectProjectTree, type TreeWorkspace } from '../catalog/catalog-selectors'

export type ProjectTreeProps = {
  state: CatalogState
  onWorkspaceSelect: (workspaceId: string) => void
}

/** 把 CatalogState 还原为 bootstrap 形状供选择器消费。 */
function toBootstrap(state: CatalogState): Parameters<typeof selectProjectTree>[0] {
  return {
    machines: state.machines,
    projects: state.projects,
    locations: state.locations,
    workspaces: state.workspaces,
    git_refs: state.gitRefs,
    active_task_summaries: state.activeTaskSummaries,
    operations: state.operations,
    control_revision: state.controlRevision
  }
}

/**
 * 项目树组件。
 * @param state catalog 投影状态
 * @param onWorkspaceSelect 选中 Workspace 回调
 */
export function ProjectTree({ state, onWorkspaceSelect }: ProjectTreeProps): React.JSX.Element {
  const tree = selectProjectTree(toBootstrap(state))

  const selectWorkspace = (ws: TreeWorkspace): void => {
    onWorkspaceSelect(ws.workspaceId)
  }

  const selectWorkspaceById = (workspaceId: string): void => {
    onWorkspaceSelect(workspaceId)
  }

  return (
    <div data-testid="handoff-project-tree" className="handoff-project-tree">
      {tree.map((project) => (
        <div key={project.projectId} className="handoff-tree-project">
          <div className="handoff-tree-project-header">
            <span className="handoff-tree-label">{project.name}</span>
            <span className="handoff-tree-counts">
              {project.workspaceCount} ws · {project.runningTaskCount} running
            </span>
          </div>
          {project.locations.map((loc) => (
            <div key={loc.locationId} className="handoff-tree-machine">
              <div className="handoff-tree-machine-header">
                <span className="handoff-tree-label">
                  {loc.machineName}
                  {loc.machineKind === 'remote' ? ' (远端)' : ''}
                </span>
                <span
                  className={`handoff-tree-status handoff-tree-status-${loc.machineStatus}`}
                  title={loc.machineStatus}
                >
                  {loc.machineStatus === 'connected'
                    ? '可用'
                    : loc.machineStatus === 'unavailable'
                      ? '不可用'
                      : loc.machineStatus}
                </span>
              </div>
              {loc.workspaces.map((ws) => (
                <div
                  key={ws.workspaceId}
                  className="handoff-tree-workspace"
                  onClick={() => selectWorkspace(ws)}
                >
                  <span className="handoff-tree-workspace-name">
                    {ws.kind === 'main' ? '主目录' : ws.branch || ws.path}
                  </span>
                  <span className="handoff-tree-workspace-path">{ws.path}</span>
                  {ws.tasks.map((task) => (
                    <div
                      key={task.taskId}
                      className="handoff-tree-task"
                      onClick={(e) => {
                        e.stopPropagation()
                        selectWorkspaceById(task.workspaceId)
                      }}
                    >
                      <span>{task.name}</span>
                      <span className="handoff-tree-task-meta">
                        {task.state}
                        {task.attention > 0 ? ` · ${task.attention}` : ''}
                      </span>
                    </div>
                  ))}
                </div>
              ))}
            </div>
          ))}
        </div>
      ))}
    </div>
  )
}
