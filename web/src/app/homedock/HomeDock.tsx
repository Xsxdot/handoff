// HomeDock —— home 基准终端的右下角悬浮入口：圆钮 + 小面板，浮窗由它渲染。
//
// 职责：
//   - 收起时是右下角圆钮（带存活数角标），点开是一张小面板——标题「home 基准」、
//     一句说明、已开终端清单（每项带存活点）、底部「新终端 ⌘T」
//   - 面板开合是本地 UI 状态（useState），不进 dock；点清单项 / 点新终端后收起面板
//   - 浮窗（HomeWindow）由本组件渲染：dock.windowOpen 为真时挂载，Shell 只需挂
//     这一个组件即可同时获得入口与浮窗
//
// 边界：
//   - **与 useWorkbench 完全无关**：home 终端不挂在任何目录上，这个入口与其浮窗
//     刻意独立于中央工作区（见 useHomeDock 的职责注释）
//   - 不持有 tabs / 浮窗开合 / 几何任何状态，全部从 dock（useHomeDock 的返回值）读；
//     dock.activeId 可能为 null，仅影响激活项高亮，不能假定它非空
//   - onKill 只向上抛 id，真的删服务端会话是调用方的事

import { useState, type ReactNode } from 'react'
import { House, Plus, SquareTerminal, X } from 'lucide-react'
import { HomeWindow } from './HomeWindow'
import type { HomeDockApi, HomeTab } from './useHomeDock'

function tabLabel(seq: number): string {
  // 面板清单固定带序号（bash · home 1 / bash · home 2 …）：与浮窗 tab 条里
  // seq===1 不显号（HomeWindow 的 tabLabel）不同，这里是清单入口，用户靠号
  // 认「哪一个是第几个」，一律显号更直接
  return `bash · home ${seq}`
}

export function HomeDock({ dock, renderTab, onKill }: {
  dock: HomeDockApi
  renderTab: (t: HomeTab) => ReactNode
  onKill: (id: string) => void
}) {
  // panelOpen 只管「小面板开不开」——纯本地 UI 状态，不进 dock。
  // dock 只管 tab / 浮窗开合 / 几何，面板只是入口，不该污染它
  const [panelOpen, setPanelOpen] = useState(false)

  const openTerminal = () => {
    dock.newTerminal()
    setPanelOpen(false)
  }

  const focusTab = (id: string) => {
    dock.activate(id)
    setPanelOpen(false)
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
          onNew={dock.newTerminal}
          onKill={onKill}
          onCollapse={dock.collapse}
          renderTab={renderTab}
        />
      )}

      {panelOpen ? (
        <section className="fixed right-5 bottom-11 z-40 w-[258px] rounded-[10px] border border-border bg-popover p-2.5 text-popover-foreground shadow-xl">
          <header className="flex items-center justify-between text-[12px] font-semibold text-popover-foreground">
            <span className="inline-flex items-center gap-1.5">
              <House className="size-3.5" />
              home 基准
            </span>
            <button
              type="button"
              aria-label="收起面板"
              onClick={() => setPanelOpen(false)}
              className="inline-flex cursor-pointer p-0.5 text-muted-foreground hover:text-foreground"
            >
              <X className="size-3.5" />
            </button>
          </header>
          <p className="mt-1 mb-2 text-[10.5px] leading-[1.45] text-muted-foreground">不挂在任何项目上，与中央工作区互不影响。</p>
          <div className="flex max-h-[190px] flex-col gap-0.5 overflow-auto">
            {dock.tabs.length === 0 && <p className="px-1.5 py-2.5 text-[11.5px] text-muted-foreground">还没有开过终端</p>}
            {dock.tabs.map((tab) => {
              const isActive = tab.id === dock.activeId
              return (
                <button
                  key={tab.id}
                  type="button"
                  onClick={() => focusTab(tab.id)}
                  className={`flex cursor-pointer items-center gap-1.5 rounded-md px-1.5 py-1.5 text-left text-[12px] text-popover-foreground ${
                    isActive ? 'bg-accent' : 'hover:bg-accent'
                  }`}
                >
                  <SquareTerminal className="size-3.5 shrink-0" />
                  <span className="min-w-0 flex-1 truncate">{tabLabel(tab.seq)}</span>
                  <span aria-hidden="true" className="size-1.5 shrink-0 rounded-full bg-state-active" />
                </button>
              )
            })}
          </div>
          <button
            type="button"
            onClick={openTerminal}
            className="mt-[7px] flex w-full cursor-pointer items-center gap-1.5 rounded-[7px] border border-border bg-background px-2 py-[7px] text-[12px] hover:bg-accent"
          >
            <Plus className="size-3.5" />
            新终端
            <kbd className="ml-auto font-mono text-[10px] text-muted-foreground">⌘T</kbd>
          </button>
        </section>
      ) : (
        <button
          type="button"
          aria-label="home 基准终端"
          onClick={() => setPanelOpen(true)}
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
      )}
    </>
  )
}
