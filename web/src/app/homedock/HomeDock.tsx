// HomeDock —— home 基准终端的右下角悬浮入口：一个 FAB 圆钮 + 浮窗，由它渲染。
//
// 职责：
//   - FAB 是「开/收」开关，一次点击直达终端，中间不再隔一层清单面板
//   - 收起时是右下角圆钮（带存活数角标），浮窗（HomeWindow）由本组件渲染：
//     dock.windowOpen 为真时挂载，Shell 只需挂这一个组件即可同时获得入口与浮窗
//
// 边界：
//   - **与 useWorkbench 完全无关**：home 终端不挂在任何目录上，这个入口与其浮窗
//     刻意独立于中央工作区（见 useHomeDock 的职责注释）
//   - 不持有 tabs / 浮窗开合 / 几何任何状态，全部从 dock（useHomeDock 的返回值）读；
//     dock.activeId 可能为 null，仅影响激活项高亮，不能假定它非空
//   - onKill 只向上抛 id，真的删服务端会话是调用方的事
//   - 临时文件入口放在浮窗 tab 条而不是 FAB：FAB 的职责是开/收，另加清单会把
//     已经删掉的第二层导航重新请回来

import type { ReactNode } from 'react'
import { Plus } from 'lucide-react'
import { HomeWindow } from './HomeWindow'
import type { HomeDockApi, HomeTab } from './useHomeDock'

export function HomeDock({ dock, renderTab, onKill, onNewFile }: {
  dock: HomeDockApi
  renderTab: (t: HomeTab, active?: boolean) => ReactNode
  onKill: (id: string) => void
  onNewFile?: () => void
}) {
  // FAB 是「开/收」开关，一次点击直达终端，中间不再隔一层清单面板。
  //
  // 为什么删掉那张面板：浮窗自己就有 tab 条和 +，面板是同一批终端的第二套清单，
  // 而且挡在第一套前面——用户要点两次才拿得到终端。删掉之后「有几个」由角标说，
  // 「分别是哪几个」由浮窗 tab 条说，第一次点击就都看得见。
  const onFab = () => {
    if (dock.windowOpen) {
      // 浮窗就在眼前时点悬浮球，最可能的意图是收起它
      dock.collapse()
      return
    }
    if (dock.tabs.length === 0) {
      dock.newTerminal()
      return
    }
    // 重开到收起前那个：collapse 刻意不动 activeId，那个信息还在。
    // ?? 兜底实际不会命中（activeId 为 null 只可能在 tabs 为空时，
    // 而那条分支上面已经吃掉了），写它是为了不让 null 进 activate
    dock.activate(dock.activeId ?? dock.tabs[dock.tabs.length - 1].id)
  }

  return (
    <>
      {dock.windowOpen && (
        <HomeWindow
          tabs={dock.tabs}
          activeId={dock.activeId}
          geom={dock.geom}
          onGeom={dock.setGeom}
          onActivate={dock.activate}
          onNew={() => dock.newTerminal()}
          onNewFile={onNewFile}
          onKill={onKill}
          onCollapse={dock.collapse}
          maximized={dock.maximized}
          onToggleMaximize={dock.toggleMaximize}
          renderTab={renderTab}
        />
      )}

      <button
        type="button"
        aria-label="home 基准终端"
        onClick={onFab}
        className="fixed right-5 bottom-11 z-40 flex size-11 cursor-pointer items-center justify-center rounded-full border border-[#2b3542] bg-[#10151b] text-white shadow-lg hover:opacity-90"
      >
        <Plus className="size-5" />
        {dock.tabs.length > 0 && (
          /* 角标是浮窗收起后「还有几个会话活着」的唯一可见证据。
             没有它，「收起不杀」这条口径在界面上就不成立——用户会以为会话没了 */
          <span
            data-testid="home-badge"
            className="absolute -top-0.5 -right-0.5 flex h-[17px] min-w-[17px] items-center justify-center rounded-full bg-[#18a86b] px-1 font-mono text-[10px] leading-none text-white"
          >
            {dock.tabs.length}
          </span>
        )}
      </button>
    </>
  )
}
