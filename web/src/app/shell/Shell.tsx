// Shell —— 控制台的三栏外框：左栏导航树 / 中央 tab 工作台 / 右栏文件树。
//
// 职责：
//   - 持有跨栏共享的数据流（任务流 2.5s、项目树流 30s）与**当前基准目录**这一
//     唯一全局选中态（useWorkbench）
//   - 把三栏接起来：左栏点目录 → 切基准；左栏点任务 → 切基准 + 开 TUI tab；
//     右栏点文件 → 开 file tab；中央按 tab 种类分发渲染
//   - 承载弹出层（看板、工单）、设置页与右下角悬浮按钮
//
// 边界：
//   - 不自己取目录内容（归 FileTree）、不自己取任务会话（归 TuiTab）
//   - 中央 tab 的具体渲染经 renderContent 注入 WorkbenchPage，Shell 只做分发
//   - 机器流只随登记向导开表（useMachines(wizardOpen)）：探活会向每台远程机发
//     GET /api/status，没人看的时候没有理由持续打扰它们（spec §6）
//
// 关于 ShellContext 的移除：W3 用 <Outlet context> 给三个子页面下发共享数据。
// 新 IA 里中央不再是路由页面而是 tab，Outlet 没有了消费者；看板与工单改为弹层，
// 它们要的数据直接由 Shell 以 props 传下去。留一个没人用的 context 只会误导。
import { useEffect, useMemo, useState } from 'react'
import { Navigate, Route, Routes, useNavigate, useParams } from 'react-router-dom'
import { deleteProject } from '../../api/client'
import type { ProjectTreeResp, Task } from '../../api/types'
import { useMachines } from '../data/useMachines'
import { useProjectTree } from '../data/useProjectTree'
import { useTasks } from '../data/useTasks'
import { DisconnectedBanner, SessionExpiredBanner } from '../lib/Banners'
import { AddProjectWizard } from '../projects/AddProjectWizard'
import { findBaseOfTask, ProjectTree } from '../tree/ProjectTree'
import { FileTree } from '../files/FileTree'
import { WorkbenchPage } from '../workbench/WorkbenchPage'
import { TerminalTab } from '../workbench/TerminalTab'
import { FileTab } from '../workbench/FileTab'
import { TuiTab } from '../workbench/TuiTab'
import { FloatingNewPane } from '../workbench/FloatingNewPane'
import { HOME_BASE, useWorkbench, type BaseDir } from '../workbench/useWorkbench'
import { BoardOverlay } from '../overlay/BoardOverlay'
import { TicketsOverlay } from '../overlay/TicketsOverlay'
import { useGlobalTickets } from '../overlay/useGlobalTickets'
import { SettingsPage } from '../settings/SettingsPage'
import { Breadcrumb } from './Breadcrumb'

// OverlayKind 是当前打开的弹层。同时只允许一个（spec §0）：两个叠在一起时
// Esc 该关哪个会变得含糊。
type OverlayKind = 'none' | 'board' | 'tickets'

export function Shell() {
  const tasksState = useTasks()
  const treeState = useProjectTree()
  const tasks = useMemo(() => tasksState.data ?? [], [tasksState.data])
  const wb = useWorkbench()
  const navigate = useNavigate()

  const [overlay, setOverlay] = useState<OverlayKind>('none')
  const [wizardOpen, setWizardOpen] = useState(false)
  const machinesState = useMachines(wizardOpen)
  const tickets = useGlobalTickets(tasks)

  const onUnregister = async (name: string, machine: string) => {
    await deleteProject(name, machine)
    treeState.refresh()
  }

  // openTaskTui 是「点一个任务 → 在它所在目录开 TUI tab」的唯一实现。
  // 左栏任务行、看板卡片、/tasks/:id 深链三条路径都走它，避免三份各自漂移。
  const openTaskTui = (base: BaseDir | null, taskId: string) => {
    setOverlay('none')
    wb.open({ kind: 'tui', taskId }, base ?? undefined)
  }

  // currentTaskId 是当前目录上「最该看的那个任务」，只用于右栏 M 角标的数据源。
  // 一个目录下可能有多个任务，取第一个正在跑的，没有就取第一个——角标是装饰，
  // 选谁都不影响正确性，但要稳定（不随渲染抖动）。
  const currentTaskId = useMemo(() => {
    if (!wb.base || wb.base.kind !== 'workspace') return null
    const under = tasks.filter((t) => t.work_dir === wb.base?.path)
    return under.find((t) => t.state === 'running')?.id ?? under[0]?.id ?? null
  }, [tasks, wb.base])

  return (
    <div className="flex h-dvh bg-background">
      <aside role="complementary" className="flex w-[260px] shrink-0 flex-col overflow-y-auto border-r bg-sidebar">
        {treeState.sessionExpired && <SessionExpiredBanner />}
        {treeState.disconnected && !treeState.sessionExpired && (
          <DisconnectedBanner message={treeState.errorText} compact />
        )}
        {treeState.data && (
          <ProjectTree
            tree={treeState.data}
            tasks={tasks}
            selectedKey={wb.base?.key ?? null}
            ticketCount={tickets.count}
            onSelectDir={wb.select}
            onOpenTask={openTaskTui}
            onOpenBoard={() => setOverlay('board')}
            onOpenTickets={() => setOverlay('tickets')}
            onOpenSettings={() => navigate('/settings')}
            onAddProject={() => setWizardOpen(true)}
            onUnregister={onUnregister}
          />
        )}
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        {wb.base && <Breadcrumb base={wb.base} onSplit={wb.split} />}
        <main className="min-h-0 flex-1">
          <Routes>
            <Route
              path="/settings"
              element={<SettingsPage onClose={() => navigate('/')} />}
            />
            <Route path="/machines" element={<Navigate to="/settings" replace />} />
            <Route path="/tasks/:id" element={<TaskDeepLink tree={treeState.data} tasks={tasks} onOpen={openTaskTui} />} />
            <Route
              path="*"
              element={
                <WorkbenchPage
                  api={wb}
                  onAddProject={() => setWizardOpen(true)}
                  renderContent={(c, base) => {
                    switch (c.kind) {
                      case 'terminal':
                        return <TerminalTab base={base} seq={c.seq} />
                      case 'file':
                        return <FileTab base={base} rel={c.rel} />
                      case 'tui':
                        return <TuiTab taskId={c.taskId} />
                      default:
                        return null
                    }
                  }}
                />
              }
            />
          </Routes>
        </main>
      </div>

      {wb.base && wb.base.kind === 'workspace' && (
        <div className="w-[280px] shrink-0">
          <FileTree
            base={wb.base}
            taskId={currentTaskId}
            onOpenFile={(rel) => wb.open({ kind: 'file', rel })}
          />
        </div>
      )}

      <FloatingNewPane onNewTerminal={() => wb.openTerminal(HOME_BASE)} />

      {overlay === 'board' && (
        <BoardOverlay
          tasksState={tasksState}
          tree={treeState.data}
          onOpenTask={openTaskTui}
          onClose={() => setOverlay('none')}
        />
      )}
      {overlay === 'tickets' && (
        <TicketsOverlay
          tickets={tickets}
          onOpenTask={openTaskTui}
          onClose={() => setOverlay('none')}
        />
      )}

      <AddProjectWizard
        open={wizardOpen}
        machines={machinesState.data?.machines ?? []}
        onClose={() => setWizardOpen(false)}
        onDone={() => treeState.refresh()}
      />
    </div>
  )
}

// TaskDeepLink 承接 /tasks/:id 这条 W3b 留下的深链。
//
// 为什么保留：已有书签与 --notify 的通知文案里都可能带这个地址，直接删路由会
// 让它们 404。行为改为「选中该任务所在目录 + 开它的 TUI tab + 换回 /」——地址栏
// 不停在一个不再有对应页面的路径上。
function TaskDeepLink({
  tree,
  tasks,
  onOpen,
}: {
  tree: ProjectTreeResp | null
  tasks: Task[]
  onOpen: (base: BaseDir | null, taskId: string) => void
}) {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [done, setDone] = useState(false)

  useEffect(() => {
    // 等树到位再解析目录：树还没来时目录解析不出来，会把 tab 开在错的基准上
    if (!id || done || !tree) return
    onOpen(findBaseOfTask(tree, tasks, id), id)
    setDone(true)
    navigate('/', { replace: true })
  }, [id, done, tree, tasks, onOpen, navigate])

  return <p className="p-4 text-sm text-muted-foreground">正在打开任务…</p>
}
