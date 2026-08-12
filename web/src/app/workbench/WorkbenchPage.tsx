// WorkbenchPage —— 中央内容承载区。
//
// 职责：
//   - 按当前基准目录渲染一组或两组 tab（tab 条 + 激活 tab 的内容）
//   - 空白 tab 的种类选择、以及「选了种类之后要不要再选目标」的分流
//   - 没有 tab / 没有基准目录时的两种空态
//
// 边界：
//   - 不认识任何一种 tab 的具体内容：renderContent 由 Shell 注入，
//     这样中央区的布局与「终端/文件/TUI 各自怎么画」互不牵连
//   - 不持有状态：全部经 WorkbenchApi
//
// 「选了种类之后」的分流（spec §2.2.1）：
//   - 终端：直接就位，序号由 tabs.ts 算
//   - 文件：需要再选一个文件。本期不做独立的文件选择器——右栏文件树就是
//     那个选择器，所以这里给一句指路，不造第二个入口
//   - 任务 TUI：需要再选一个任务。同理指向左栏该目录下的任务行
import { useState, type ReactNode } from 'react'
import { nextTerminalSeq, type TabContent } from './tabs'
import { BlankTab, type PickKind } from './BlankTab'
import { EmptyWorkbench } from './EmptyWorkbench'
import { TabBar } from './TabBar'
import type { BaseDir, WorkbenchApi } from './useWorkbench'

export interface WorkbenchPageProps {
  api: WorkbenchApi
  onAddProject: () => void
  renderContent: (content: TabContent, base: BaseDir) => ReactNode
}

// PICK_HINT 是「种类选好了但还缺一个目标」时的指路文案。
//
// 为什么只指路不弹选择器：右栏文件树本身就是文件选择器，左栏任务行本身就是
// 任务选择器。再造一个模态选择器等于同一件事有两个入口，而且那个入口还得
// 自己再实现一遍目录列举与任务列表。
export const PICK_HINT: Record<Exclude<PickKind, 'terminal'>, string> = {
  file: '在右侧文件树里点一个文件，它会在这里打开。',
  tui: '在左侧该目录下点一个任务，它的 TUI 会在这里打开。',
}

export function WorkbenchPage({ api, onAddProject, renderContent }: WorkbenchPageProps) {
  const { base, wb } = api
  // awaiting 记「哪个空白 tab 已经选了种类、正在等目标」。
  // 它是本组件的本地 UI 状态，**不进 TabContent**：TabContent 多一支就等于
  // 承认了第四种 tab，与「只有三种 tab」的硬约束冲突。tab 被关掉后这里会残留
  // 一条键，无害——下一个 tab 的 id 是新的，不会命中。
  const [awaiting, setAwaiting] = useState<Record<string, Exclude<PickKind, 'terminal'>>>({})

  if (!base) return <EmptyWorkbench onAddProject={onAddProject} />

  // 选了种类之后：终端直接就位；文件与 TUI 缺目标，把该 tab 停在提示态
  const pick = (group: number, tabId: string, kind: PickKind) => {
    if (kind === 'terminal') {
      api.setContent(group, tabId, { kind: 'terminal', seq: nextTerminalSeq(wb) })
      return
    }
    setAwaiting((prev) => ({ ...prev, [tabId]: kind }))
  }

  const back = (tabId: string) =>
    setAwaiting((prev) => {
      const next = { ...prev }
      delete next[tabId]
      return next
    })

  // startFromEmpty 处理「组里一个 tab 都没有」时直接在空态面板上选种类：
  // 此时没有可原地改内容的 tab，终端直接开一个，其余先开一个空白 tab
  // 承接（用户随即会在它上面看到指路）。
  //
  // group 必须显式传：分屏后被清空的那一组仍然渲染这块空态面板，而焦点很可能
  // 在另一组上。不传的话新 tab 会开到焦点组去，用户点的是这一侧却在那一侧长出
  // 一个 tab。
  const startFromEmpty = (group: number, kind: PickKind) => {
    if (kind === 'terminal') {
      api.openTerminal(base, group)
      return
    }
    api.open({ kind: 'blank' }, undefined, group)
  }

  return (
    <div className="flex h-full min-h-0 gap-px bg-border">
      {wb.groups.map((g, gi) => {
        const activeTab = g.tabs.find((t) => t.id === g.activeId) ?? null
        return (
          <section key={gi} className="flex min-w-0 flex-1 flex-col bg-background">
            <TabBar
              group={gi}
              tabs={g.tabs}
              activeId={g.activeId}
              baseLabel={base.label}
              onActivate={api.activate}
              onClose={api.close}
              onNew={(g) => api.open({ kind: 'blank' }, undefined, g)}
            />
            {/*
              两处 BlankTab 的 key 必须区分开。它们在三元的相邻分支上，同类型同位置，
              React 默认会把「空组面板」原地复用成「空白 tab 面板」——DOM 节点不换，
              于是面板的「挂载即聚焦」不会重跑，点了 + 之后焦点还留在 + 按钮上，
              印在面板上的 ⌘T 按下去没反应（走查实测）。给出各自的身份，让它真的重挂。
            */}
            <div className="min-h-0 flex-1 overflow-auto">
              {activeTab === null ? (
                <BlankTab key={`empty-${gi}`} base={base} onPick={(k) => startFromEmpty(gi, k)} />
              ) : activeTab.content.kind === 'blank' ? (
                <BlankTab
                  key={activeTab.id}
                  base={base}
                  onPick={(k) => pick(gi, activeTab.id, k)}
                  hint={awaiting[activeTab.id] ? PICK_HINT[awaiting[activeTab.id]] : undefined}
                  onBack={() => back(activeTab.id)}
                />
              ) : (
                renderContent(activeTab.content, base)
              )}
            </div>
          </section>
        )
      })}
    </div>
  )
}
