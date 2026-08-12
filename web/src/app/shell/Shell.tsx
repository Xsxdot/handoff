// Shell —— 控制台的三段外框：顶部 tab 条 + 常驻左栏项目树 + 中央内容区。
//
// 职责：
//   - 提供三条路由共用的外框，内容区用 <Outlet> 承载
//   - 持有跨页共享的两条数据流（任务流 2.5s、项目树流 30s）与看板筛选 filter，
//     一并下发——避免每页各拉一份、左栏与顶部下拉各存一套筛选（spec §6 / filter.ts）
//   - 左栏渲染 ProjectTree：树流断开时用 compact 横幅占位，不让左栏空白；
//     会话失效落终止态横幅
//
// 边界：
//   - 不渲染任何未实现功能的入口（左栏齿轮、设置页、配对开发机）——
//     置灰控件承诺"以后能用"，用户会反复点；缺一个按钮反而诚实（spec §0）
//   - 不持有机器流：那只在 /machines 可见时开表（spec §6）
import { useState } from 'react'
import { Outlet, useNavigate, useOutletContext } from 'react-router-dom'
import type { ProjectTreeResp, Task } from '../../api/types'
import { EMPTY_FILTER, type BoardFilter } from '../board/filter'
import { useProjectTree } from '../data/useProjectTree'
import { useTasks } from '../data/useTasks'
import { DisconnectedBanner, SessionExpiredBanner } from '../lib/Banners'
import { ProjectTree } from '../tree/ProjectTree'
import { TopTabs } from './TopTabs'

// ShellContext 是 Shell 通过 Outlet 下发到各子页面的共享上下文：任务流、树、
// filter 及其写入入口。子页面用 useShellContext 取用，不自己再拉数据。
export interface ShellContext {
  tasks: Task[]
  tree: ProjectTreeResp | null
  filter: BoardFilter
  setFilter: (f: BoardFilter) => void
  onOpenTask: (id: string) => void
}

// useShellContext 供子页面取用 Shell 下发的上下文（见 ShellContext）。
export function useShellContext(): ShellContext {
  return useOutletContext<ShellContext>()
}

export function Shell() {
  const tasksState = useTasks()
  const treeState = useProjectTree()
  const [filter, setFilter] = useState<BoardFilter>(EMPTY_FILTER)
  const navigate = useNavigate()
  const onOpenTask = (id: string) => navigate(`/tasks/${id}`)

  return (
    <div className="grid h-dvh grid-cols-[260px_1fr] grid-rows-[auto_1fr] bg-background">
      <div className="col-span-2 border-b bg-background">
        <TopTabs />
      </div>
      <aside role="complementary" className="min-h-0 overflow-y-auto border-r bg-sidebar">
        {treeState.sessionExpired && <SessionExpiredBanner />}
        {treeState.disconnected && !treeState.sessionExpired && (
          <DisconnectedBanner message={treeState.errorText} compact />
        )}
        {treeState.data && (
          <ProjectTree
            tree={treeState.data}
            tasks={tasksState.data ?? []}
            filter={filter}
            onFilterChange={setFilter}
            onOpenTask={onOpenTask}
          />
        )}
      </aside>
      <main className="min-h-0 overflow-y-auto bg-muted/40">
        <Outlet
          context={{
            tasks: tasksState.data ?? [],
            tree: treeState.data,
            filter,
            setFilter,
            onOpenTask,
          }}
        />
      </main>
    </div>
  )
}
