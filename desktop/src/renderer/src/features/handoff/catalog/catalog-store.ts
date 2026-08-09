/**
 * Handoff CatalogStore：bootstrap + control stream 的纯投影。
 *
 * 职责：
 *   - hydrate(snapshot)：bootstrap 原子替换
 *   - apply(event)：按 revision 严格递增增量；gap 触发 resetFromGap
 *   - 独立 vanilla Zustand store，不挂进 useAppStore 巨型 slice
 *
 * 边界：
 *   - 不保存项目/机器/任务业务真相的持久化（桌面只存纯 UI 状态）
 *   - 计数直接从 TaskSummary/Workspace 投影推导，不由 renderer 遍历临时推导
 */
import { create } from 'zustand'
import type { BootstrapResponse, ControlEventEnvelope } from '../../../../../shared/handoff/contracts'

export type CatalogConnectionStatus = 'connecting' | 'connected' | 'unavailable'

export type CatalogState = {
  controlRevision: number
  machines: BootstrapResponse['machines']
  projects: BootstrapResponse['projects']
  locations: BootstrapResponse['locations']
  workspaces: BootstrapResponse['workspaces']
  gitRefs: BootstrapResponse['git_refs']
  activeTaskSummaries: BootstrapResponse['active_task_summaries']
  operations: BootstrapResponse['operations']
  connection: CatalogConnectionStatus
  selectedWorkspaceId: string | null
  // 计数：直接从投影推导（spec §4.1：权威计数由本机 agentd 提供）
  workspaceCount: number
  runningTaskCount: number
  attentionCount: number
  projectCount: number
  machineCount: number
  // 动作
  hydrate: (snapshot: BootstrapResponse) => void
  apply: (event: ControlEventEnvelope) => void
  setConnection: (status: CatalogConnectionStatus) => void
  selectWorkspace: (id: string) => void
  resetFromGap: () => void
}

/** 计算投影计数。 */
function deriveCounts(
  state: Pick<CatalogState, 'workspaces' | 'activeTaskSummaries' | 'projects' | 'machines'>
): Pick<CatalogState, 'workspaceCount' | 'runningTaskCount' | 'attentionCount' | 'projectCount' | 'machineCount'> {
  const runningTaskCount = state.activeTaskSummaries.filter((t) => t.state === 'running').length
  const attentionCount = state.activeTaskSummaries.reduce((acc, t) => acc + t.attention, 0)
  return {
    workspaceCount: state.workspaces.length,
    runningTaskCount,
    attentionCount,
    projectCount: state.projects.length,
    machineCount: state.machines.length
  }
}

const initialState: Omit<
  CatalogState,
  'hydrate' | 'apply' | 'setConnection' | 'selectWorkspace' | 'resetFromGap'
> = {
  controlRevision: 0,
  machines: [],
  projects: [],
  locations: [],
  workspaces: [],
  gitRefs: [],
  activeTaskSummaries: [],
  operations: [],
  connection: 'connecting',
  selectedWorkspaceId: null,
  workspaceCount: 0,
  runningTaskCount: 0,
  attentionCount: 0,
  projectCount: 0,
  machineCount: 0
}

/**
 * 创建独立 CatalogStore 实例（测试可各自新建）。
 * 返回 zustand hook（useStore），并附带 getState/setState/subscribe 与动作代理，
 * 便于测试直接调用 store.hydrate() 等。
 */
export function createCatalogStore() {
  const useStore = create<CatalogState>()((set, get) => ({
    ...initialState,

    hydrate: (snapshot) => {
      const base = {
        controlRevision: snapshot.control_revision,
        machines: snapshot.machines,
        projects: snapshot.projects,
        locations: snapshot.locations,
        workspaces: snapshot.workspaces,
        gitRefs: snapshot.git_refs,
        activeTaskSummaries: snapshot.active_task_summaries,
        operations: snapshot.operations
      }
      set({ ...base, ...deriveCounts(base), connection: 'connected' })
    },

    apply: (event) => {
      const { controlRevision, resetFromGap } = get()
      // 严格递增：重复事件（revision <= 当前）幂等忽略
      if (event.revision <= controlRevision) {
        return
      }
      // gap：中间 revision 缺失 → 重新 bootstrap 而非猜补
      if (event.revision > controlRevision + 1) {
        resetFromGap()
        return
      }
      const patch = applyEventToState(get(), event)
      set({ ...patch, controlRevision: event.revision })
    },

    setConnection: (status) => set({ connection: status }),

    selectWorkspace: (id) => set({ selectedWorkspaceId: id }),

    // gap 或 CURSOR_EXPIRED 时由上层重新 bootstrap 原子替换
    resetFromGap: () => set({ connection: 'connecting' })
  }))

  // 动作代理：store.hydrate(...) 直接可用（与 getState() 等价）
  return Object.assign(useStore, {
    hydrate: (snapshot: BootstrapResponse) => useStore.getState().hydrate(snapshot),
    apply: (event: ControlEventEnvelope) => useStore.getState().apply(event),
    setConnection: (status: CatalogConnectionStatus) => useStore.getState().setConnection(status),
    selectWorkspace: (id: string) => useStore.getState().selectWorkspace(id),
    resetFromGap: () => useStore.getState().resetFromGap()
  })
}

/**
 * 把一条控制事件应用到投影（不递增 revision——由调用方设置）。
 * 只处理已知 kind；未知 kind 忽略（向前兼容）。
 */
function applyEventToState(state: CatalogState, event: ControlEventEnvelope): Partial<CatalogState> {
  switch (event.kind) {
    case 'workspace.upsert': {
      const ws = event.payload as BootstrapResponse['workspaces'][number]
      const exists = state.workspaces.some((w) => w.id === ws.id)
      const workspaces = exists
        ? state.workspaces.map((w) => (w.id === ws.id ? ws : w))
        : [...state.workspaces, ws]
      return { workspaces, ...deriveCounts({ ...state, workspaces }) }
    }
    case 'workspace.remove': {
      const workspaces = state.workspaces.filter((w) => w.id !== event.resource_id)
      return { workspaces, ...deriveCounts({ ...state, workspaces }) }
    }
    case 'task_summary.upsert': {
      const ts = event.payload as BootstrapResponse['active_task_summaries'][number]
      const exists = state.activeTaskSummaries.some((t) => t.task_id === ts.task_id)
      const activeTaskSummaries = exists
        ? state.activeTaskSummaries.map((t) => (t.task_id === ts.task_id ? ts : t))
        : [...state.activeTaskSummaries, ts]
      return { activeTaskSummaries, ...deriveCounts({ ...state, activeTaskSummaries }) }
    }
    case 'task_summary.remove': {
      const activeTaskSummaries = state.activeTaskSummaries.filter(
        (t) => t.task_id !== event.resource_id
      )
      return { activeTaskSummaries, ...deriveCounts({ ...state, activeTaskSummaries }) }
    }
    default:
      return {}
  }
}

export type CatalogStore = ReturnType<typeof createCatalogStore>
