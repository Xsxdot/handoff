// Shell —— 控制台的三段外框：顶部 tab 条 + 常驻左栏项目树 + 中央内容区。
//
// 职责：
//   - 提供三条路由共用的外框，内容区用 <Outlet> 承载
//   - 持有跨页共享的两条数据流（任务流 2.5s、项目树流 30s）与看板筛选 filter，
//     一并下发——避免每页各拉一份、左栏与顶部下拉各存一套筛选（spec §6 / filter.ts）。
//     任务流以 tasksState（PollState）下发，子页面自己决定何时取 data、何时取
//     disconnected/sessionExpired/errorText（如看板首载失败态用 errorText）
//   - 左栏渲染 ProjectTree：树流断开时用 compact 横幅占位，不让左栏空白；
//     会话失效落终止态横幅
//
// 边界：
//   - 不渲染任何未实现功能的入口（左栏齿轮、设置页、配对开发机）——
//     置灰控件承诺"以后能用"，用户会反复点；缺一个按钮反而诚实（spec §0）
//   - 机器流只随登记向导开表（useMachines(wizardOpen)）：探活会向每台远程机发
//     GET /api/status，没人看的时候没有理由持续打扰它们（spec §6）
//   - 注销入口在树的位置行（onUnregister → deleteProject + refresh），
//     向导打开/登记成功也走 treeState.refresh 让左栏即出新项目
import { useState } from 'react'
import { Outlet, useNavigate, useOutletContext } from 'react-router-dom'
import { deleteProject } from '../../api/client'
import type { ProjectTreeResp, Task } from '../../api/types'
import { EMPTY_FILTER, type BoardFilter } from '../board/filter'
import { useMachines } from '../data/useMachines'
import { useProjectTree } from '../data/useProjectTree'
import { useTasks } from '../data/useTasks'
import type { PollState } from '../data/usePoll'
import { DisconnectedBanner, SessionExpiredBanner } from '../lib/Banners'
import { AddProjectWizard } from '../projects/AddProjectWizard'
import { ProjectTree } from '../tree/ProjectTree'
import { TopTabs } from './TopTabs'

// ShellContext 是 Shell 通过 Outlet 下发到各子页面的共享上下文：任务流状态、
// 任务列表、树、filter 及其写入入口。子页面用 useShellContext 取用，不自己再拉数据。
export interface ShellContext {
  tasksState: PollState<Task[]>
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

  const [wizardOpen, setWizardOpen] = useState(false)
  // 机器流只在向导打开时开表：探活会向每台远程机发 GET /api/status，没人看的时候
  // 没有理由持续打扰它们（spec §6）；向导打开即首拉，关闭即停表。
  const machinesState = useMachines(wizardOpen)

  const onUnregister = async (name: string, machine: string) => {
    await deleteProject(name, machine)
    treeState.refresh()
  }

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
            onAddProject={() => setWizardOpen(true)}
            onUnregister={onUnregister}
          />
        )}
      </aside>
      <main className="min-h-0 overflow-y-auto bg-muted/40">
        <Outlet
          context={{
            tasksState,
            tasks: tasksState.data ?? [],
            tree: treeState.data,
            filter,
            setFilter,
            onOpenTask,
          }}
        />
      </main>
      <AddProjectWizard
        open={wizardOpen}
        machines={machinesState.data?.machines ?? []}
        onClose={() => setWizardOpen(false)}
        onDone={() => treeState.refresh()}
      />
    </div>
  )
}
