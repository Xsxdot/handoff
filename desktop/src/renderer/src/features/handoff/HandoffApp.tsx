/**
 * HandoffApp：桌面控制面的 renderer root。
 *
 * 职责：
 *   - 启动时 bootstrap 得 R，再 subscribe after R
 *   - 断线显示不可用；gap 或 CURSOR_EXPIRED 重新 bootstrap 原子替换
 *   - 组装三栏工作台
 *
 * 边界：
 *   - 独立 Handoff Workbench，不导入 Orca 全局 store/Worktree
 *   - renderer 不做网络重试日志（Main 记录），UI 只显示可行动状态
 */
import { useEffect } from 'react'
import { createCatalogStore } from './catalog/catalog-store'
import { ProjectTree } from './components/ProjectTree'
import { WorkbenchShell } from './components/WorkbenchShell'

// 模块级单例 CatalogStore（见 HandoffApp 的 why 注释）。
const catalogStore = createCatalogStore()

/** 从 window.handoff 读取窄 IPC（preload 暴露）。 */
function useHandoffApi(): {
  bootstrap: () => Promise<unknown>
  subscribeControl: (after: number) => Promise<void>
  onControlEvent: (cb: (ev: unknown) => void) => () => void
  onConnectionStatus: (cb: (status: string) => void) => () => void
} | null {
  const api = (window as unknown as { handoff?: unknown }).handoff
  if (!api) {
    return null
  }
  return api as ReturnType<typeof useHandoffApi>
}

/** Handoff 桌面控制面应用。 */
export function HandoffApp(): React.JSX.Element {
  // 为什么单实例：createCatalogStore 每次调用新建独立 store；在组件体内每次
  // render 重建会让 effect 闭包持有的 store 与渲染读取的 store 分叉——
  // hydrate 写到旧 store、DOM 读新 store，永远停在 connecting。用模块级单例。
  // 为什么用 hook 而非 getState()：组件必须订阅 store 才能在 hydrate/apply 后
  // 重渲染；getState() 只读一次快照，DOM 永不更新。
  const state = catalogStore()
  const api = useHandoffApi()

  useEffect(() => {
    if (!api) {
      return
    }
    let cancelled = false
    const { hydrate, apply, setConnection, resetFromGap } = catalogStore.getState()

    const bootstrap = async (): Promise<void> => {
      const snapshot = (await api.bootstrap()) as {
        control_revision: number
      }
      if (cancelled) {
        return
      }
      hydrate(snapshot as never)
      const revision = snapshot.control_revision
      await api.subscribeControl(revision)
    }

    // 订阅控制流事件与连接状态（gap 由 resetFromGap 重 bootstrap）
    const offControl = api.onControlEvent((ev) => {
      apply(ev as never)
    })
    const offStatus = api.onConnectionStatus((status) => {
      if (status === 'connecting') {
        setConnection('connecting')
      } else if (status === 'connected') {
        setConnection('connected')
      } else {
        setConnection('unavailable')
      }
    })
    // gap 或 CURSOR_EXPIRED：重新 bootstrap 原子替换
    const originalReset = resetFromGap
    catalogStore.setState({
      resetFromGap: () => {
        originalReset()
        void bootstrap()
      }
    })
    void bootstrap()

    return () => {
      cancelled = true
      offControl()
      offStatus()
    }
  }, [api])

  const onWorkspaceSelect = (workspaceId: string): void => {
    catalogStore.getState().selectWorkspace(workspaceId)
  }

  return (
    <WorkbenchShell
      state={state}
      left={<ProjectTree state={state} onWorkspaceSelect={onWorkspaceSelect} />}
      center={
        <div className="handoff-center-placeholder">
          {state.selectedWorkspaceId ? (
            <div className="handoff-breadcrumb">工作区 {state.selectedWorkspaceId}</div>
          ) : (
            <div className="handoff-center-empty">选择左侧工作区开始</div>
          )}
        </div>
      }
      right={
        <div className="handoff-right-placeholder">
          {state.selectedWorkspaceId ? (
            <div className="handoff-right-root">文件根: 未实现</div>
          ) : (
            <div className="handoff-right-empty">未选择工作区</div>
          )}
        </div>
      }
    />
  )
}

