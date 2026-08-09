/**
 * Handoff WorkbenchShell：三栏工作台骨架。
 *
 * 职责：
 *   - 左栏项目树 / 中栏工作台 / 右栏文件根占位
 *   - 连接状态展示
 *
 * 边界：
 *   - 三栏初始尺寸：左 336px、中自适应、右 288px（拖拽调整留待计划 02）
 *   - 不导入 Orca 全局 store
 */
import type { ReactNode } from 'react'
import type { CatalogState } from '../catalog/catalog-store'

export type WorkbenchShellProps = {
  state: CatalogState
  left: ReactNode
  center: ReactNode
  right: ReactNode
}

/** 三栏工作台外壳。 */
export function WorkbenchShell({ state, left, center, right }: WorkbenchShellProps): React.JSX.Element {
  return (
    <div className="handoff-workbench">
      <div className="handoff-connection-bar" data-connection={state.connection}>
        {state.connection === 'connected'
          ? '已连接本机 agentd'
          : state.connection === 'unavailable'
            ? '本机 agentd 不可用'
            : '连接中…'}
      </div>
      <div className="handoff-panes">
        <aside className="handoff-pane-left" style={{ width: 336 }}>
          {left}
        </aside>
        <main className="handoff-pane-center" data-testid="handoff-center-pane">
          {center}
        </main>
        <aside className="handoff-pane-right" style={{ width: 288 }} data-testid="handoff-right-pane">
          {right}
        </aside>
      </div>
    </div>
  )
}
