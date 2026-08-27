// WorkbenchPage —— 中央内容承载区。
//
// 职责：
//   - 按当前基准目录渲染一到三组 tab 与它们之间可拖拽的分隔条
//   - 空白 tab 的种类选择与任务选择器的生命周期
//   - 没有 tab / 没有基准目录时的两种空态
//
// 边界：
//   - 不认识任何一种 tab 的具体内容：renderContent 由 Shell 注入，
//     这样中央区的布局与「终端/文件/TUI 各自怎么画」互不牵连
//   - 不持有状态：全部经 WorkbenchApi
//
// 「选了种类之后」的分流（spec §2.2.1）：
//   - 终端：直接就位，序号由 tabs.ts 算
//   - 新文件：Task 10 接入创建流程
//   - 任务 TUI：打开任务选择器，在当前项目内挑选任务
import { Fragment, useState, type ReactNode } from 'react'
import { MAX_GROUPS, MIN_PANE_PX, nextTerminalSeq, type TabContent } from './tabs'
import { BlankTab, type PickKind } from './BlankTab'
import { EmptyWorkbench } from './EmptyWorkbench'
import { GroupDivider } from './GroupDivider'
import { TabBar } from './TabBar'
import { TaskPickerDialog } from './TaskPickerDialog'
import { DRAG_BASE_MIME, DRAG_TASK_MIME, dropZoneAt, readDragBase, type DropZone } from './paneDrop'
import { createUntitledFile } from './newFile'
import type { Launcher, ProjectTreeResp, Task } from '../../api/types'
import type { BaseDir, WorkbenchApi } from './useWorkbench'
import { cn } from '@/lib/utils'
import { errorMessage } from '../lib/format'

export interface WorkbenchPageProps {
  api: WorkbenchApi
  onAddProject: () => void
  // renderContent 多收 group 与 tabId：终端 tab 建出会话之后要把 id 写回
  // **它自己**（setContent(group, tabId, …)），而中央区是唯一知道自己在哪一组、
  // 哪个 tab 的地方。
  renderContent: (content: TabContent, base: BaseDir, group: number, tabId: string) => ReactNode
  // terminalUnavailable：当前基准目录所在机器不能开终端时的原因原文
  terminalUnavailable?: string
  // onBeforeClose 返回 false = 这次关闭由上层接管（要先弹确认、先删服务端会话）。
  // 返回 true 或不提供 = 直接关。
  onBeforeClose?: (c: TabContent, tabId: string) => boolean
  // tree / tasks 只为任务选择器而收。中央区自己不消费它们——这是刻意的
  // 转手：选择器的生命周期属于某个具体 tab，挂在 Shell 上就得再往下传一个
  // 「现在是哪个 tab 在等」，那个状态本来就该住在这里。
  tree: ProjectTreeResp | null
  tasks: Task[]
  // onFileCreated 在新建文件成功后触发，让右栏文件树把新文件显示出来。
  // 可选：没有右栏时（home 基准）不需要它
  onFileCreated?: () => void
  // 完整启动项只在这里用于把名字换算成 TabContent；展示态由 BlankTab 消费。
  launchers?: Launcher[]
}

// END_INDEX 表示「插到末尾」：跨基准拖放时中央区不知道目标基准有几栏
// （那套 tab 组住在 useWorkbench 的 byBase 里），传一个必然越界的下标，
// 由 splitGroupAt 夹到合法范围。
const END_INDEX = Number.MAX_SAFE_INTEGER

export function WorkbenchPage({
  api,
  onAddProject,
  renderContent,
  terminalUnavailable,
  onBeforeClose,
  tree,
  tasks,
  onFileCreated,
  launchers = [],
}: WorkbenchPageProps) {
  const { base, wb } = api
  // picking 记「谁正在选任务」。null = 弹层关闭。
  //
  // tabId 为 null = 这次是从 tab 条的 + 菜单发起的，还没有承接它的 tab，选完
  // 直接在 group 里新开一个；非 null = 某个空白 tab 在等，选完原地换内容。
  const [picking, setPicking] = useState<{ group: number; tabId: string | null } | null>(null)
  // newFileError 是建文件失败的原文（409 撞名、磁盘满、白名单拒绝）。
  // 显示在中央区顶部而不是弹层：它不需要用户做决定，只需要被看见
  const [newFileError, setNewFileError] = useState('')
  // dragOver 记「指针现在悬在哪一栏的哪个区」，只用于高亮。null = 没有拖放在进行
  const [dragOver, setDragOver] = useState<{ group: number; zone: DropZone } | null>(null)

  if (!base) return <EmptyWorkbench onAddProject={onAddProject} />

  const launcherItems = launchers.map((l) => ({ name: l.name, envMissing: l.env_missing }))

  const pickLauncher = (group: number, tabId: string, name: string) => {
    if (terminalUnavailable) return
    api.setContent(group, tabId, { kind: 'terminal', seq: nextTerminalSeq(wb), launcher: name })
  }

  const pick = (group: number, tabId: string, kind: PickKind) => {
    if (kind === 'terminal') {
      if (terminalUnavailable) return
      api.setContent(group, tabId, { kind: 'terminal', seq: nextTerminalSeq(wb) })
      return
    }
    if (kind === 'tui') {
      setPicking({ group, tabId })
      return
    }
    if (kind === 'newfile') {
      setNewFileError('')
      void createUntitledFile(base)
        .then((rel) => {
          api.setContent(group, tabId, { kind: 'file', rel })
          onFileCreated?.()
        })
        .catch((err: unknown) => setNewFileError(errorMessage(err)))
    }
  }

  // newIn 处理 tab 条上的 + 菜单：选中种类后**直接开出对应的 tab**，
  // 不再先落一个空白 tab 让用户在页面中间再选一次。
  //
  // 与 pick 的分工：pick 是「已经有一个空白 tab 在这儿了，把它变成什么」，
  // 走 setContent 原地改；newIn 手上没有 tab，走 open 新开一个。
  const newIn = (group: number, kind: PickKind) => {
    if (kind === 'terminal') {
      if (terminalUnavailable) return
      api.openTerminal(base, group)
      return
    }
    if (kind === 'tui') {
      // 先弹选择器、选中了才开 tab：任务没选定之前开一个 tab 出来，用户按 Esc
      // 取消后会留下一个空壳
      setPicking({ group, tabId: null })
      return
    }
    setNewFileError('')
    void createUntitledFile(base)
      .then((rel) => {
        api.open({ kind: 'file', rel }, undefined, group)
        onFileCreated?.()
      })
      .catch((err: unknown) => setNewFileError(errorMessage(err)))
  }

  // startFromEmpty 处理「组里一个 tab 都没有」时直接在空态面板上选种类：
  // 此时没有可原地改内容的 tab，终端直接开一个，其余先开一个空白 tab
  // 承接（用户随即会在它上面点「打开任务」或「新建文件」）。
  //
  // group 必须显式传：分屏后被清空的那一组仍然渲染这块空态面板，而焦点很可能
  // 在另一组上。不传的话新 tab 会开到焦点组去，用户点的是这一侧却在那一侧长出
  // 一个 tab。
  const startFromEmpty = (group: number, kind: PickKind) => {
    if (kind === 'terminal') {
      if (terminalUnavailable) return
      api.openTerminal(base, group)
      return
    }
    api.open({ kind: 'blank' }, undefined, group)
  }

  const newLauncherIn = (group: number, name: string) => {
    if (terminalUnavailable) return
    api.open({ kind: 'terminal', seq: nextTerminalSeq(wb), launcher: name }, undefined, group)
  }

  const startLauncherFromEmpty = (group: number, name: string) => {
    if (terminalUnavailable) return
    api.open({ kind: 'terminal', seq: nextTerminalSeq(wb), launcher: name }, undefined, group)
  }

  // onDropTask 处理一次任务拖放。
  //
  // 参数：
  //   - group: 落在哪一栏
  //   - zone: 落在该栏的哪个区
  //   - taskId: 拖来的任务
  //   - from: 该任务所属的基准目录；null = 未归属任务（用当前基准开）
  //
  // 跨基准拖放（from 与当前基准不是同一个）时**位置语义退化**：工作台整体切到
  // from，边缘投放变成「在末尾新开一栏」。理由是 group 这个下标是在**当前**
  // 基准的 tab 组里算的，切过去之后那一套组已经换了一批（byBase 那张 Map），
  // 下标不再指任何东西。硬要保留位置就得先切基准、等重渲染、再重新命中投放区，
  // 那是两帧之后的事，而拖放在落下的那一刻就要给出结果（spec §3.4）。
  const onDropTask = (group: number, zone: DropZone, taskId: string, from: BaseDir | null) => {
    const content: TabContent = { kind: 'tui', taskId }
    // from 为 null = 未归属任务，它没有自己的目录，用当前基准开——与在左栏
    // 点它的行为一致（Shell 的 openTaskTui 也是这条回退）
    if (from !== null && from.key !== base.key) {
      // 带显式基准的 open / openInNewPane 内部会先 select 过去，一步到位
      if (zone === 'center') {
        api.open(content, from)
        return
      }
      // 边缘投放退化成「末尾新开一栏」。END_INDEX 是个必然越界的大数，
      // openInNewPane 会把它夹到目标基准的末尾——那套 tab 组有几栏只有它知道
      api.openInNewPane(content, END_INDEX, from)
      return
    }
    if (zone === 'center') {
      api.open(content, undefined, group)
      return
    }
    // 插在左边时新栏就占据 group 这个下标，原来那栏被推到 group+1；
    // 插在右边时新栏是 group+1。两种情况下「新栏的下标」都等于插入位置。
    //
    // 走 openInNewPane 而不是 splitAt + open 两步：这个任务可能**已经在别的栏
    // 开着**，此时 openTab 的跨组去重会把它在原栏激活，先分出来的新栏就空在
    // 那儿——用户要的是「分屏并打开」，拿到一个空栏比不分屏更糟（走查实测）
    api.openInNewPane(content, zone === 'left' ? group : group + 1)
  }

  return (
    // gap 去掉了：分隔线不再靠 gap-px 透出背景色，而是 GroupDivider 这个真实元素——
    // 它要能被鼠标抓住、被键盘聚焦，那都不是背景色能做到的
    <div className="relative flex h-full min-h-0 bg-border">
      {newFileError !== '' && (
        <p className="absolute inset-x-0 top-0 z-20 bg-destructive/10 px-3 py-1.5 text-xs text-destructive">
          新建文件失败：{newFileError}
        </p>
      )}
      {wb.groups.map((g, gi) => {
        const activeTab = g.tabs.find((t) => t.id === g.activeId) ?? null
        return (
          <Fragment key={gi}>
            {gi > 0 && (
              <GroupDivider
                onResize={(delta, containerWidth) =>
                  // 最小栏宽是像素量，换成比例才能进纯函数。量不到宽度（jsdom、
                  // 尚未布局完成）时传 0：宁可这一次不夹紧，也不要因为除以 0 得到
                  // Infinity 而让拖拽整个失灵
                  api.resize(gi - 1, delta, containerWidth > 0 ? MIN_PANE_PX / containerWidth : 0)
                }
              />
            )}
            <section
              className="relative flex min-w-0 flex-col bg-background"
              onDragOver={(e) => {
                // 没有我们的数据类型就不接管：让浏览器按默认行为处理，
                // 否则从别处拖进来的东西会显示成「可以放在这里」却什么也不发生
                if (!e.dataTransfer.types.includes(DRAG_TASK_MIME)) return
                e.preventDefault()
                e.dataTransfer.dropEffect = 'copy'
                const r = e.currentTarget.getBoundingClientRect()
                const zone = dropZoneAt(e.clientX - r.left, r.width, wb.groups.length < MAX_GROUPS)
                setDragOver({ group: gi, zone })
              }}
              onDragLeave={(e) => {
                // 只在真的离开这一栏时清高亮：拖过子元素边界也会触发 dragleave，
                // 不加这个判断高亮会疯狂闪烁
                if (e.currentTarget.contains(e.relatedTarget as Node | null)) return
                setDragOver((prev) => (prev?.group === gi ? null : prev))
              }}
              onDrop={(e) => {
                if (!e.dataTransfer.types.includes(DRAG_TASK_MIME)) return
                const taskId = e.dataTransfer.getData(DRAG_TASK_MIME)
                setDragOver(null)
                if (taskId === '') return
                e.preventDefault()
                const r = e.currentTarget.getBoundingClientRect()
                const zone = dropZoneAt(e.clientX - r.left, r.width, wb.groups.length < MAX_GROUPS)
                const from = readDragBase(e.dataTransfer.getData(DRAG_BASE_MIME))
                onDropTask(gi, zone, taskId, from)
              }}
              // flexBasis 必须显式给 0：默认的 auto 会让内容宽度参与分配，
              // 于是 sizes 的权重被内容多少带偏，拖出来的比例对不上
              style={{ flexGrow: wb.sizes[gi] ?? 1, flexBasis: 0 }}
            >
              {dragOver?.group === gi && (
                <div
                  aria-hidden="true"
                  data-testid={`drop-${dragOver.zone}`}
                  className={cn(
                    'pointer-events-none absolute inset-0 z-10',
                    dragOver.zone === 'center' && 'ring-2 ring-inset ring-primary/60',
                  )}
                >
                  {dragOver.zone !== 'center' && (
                    <span
                      className={cn(
                        'absolute inset-y-0 w-[3px] bg-primary',
                        dragOver.zone === 'left' ? 'left-0' : 'right-0',
                      )}
                    />
                  )}
                </div>
              )}
              <TabBar
                group={gi}
                tabs={g.tabs}
                activeId={g.activeId}
                base={base}
                terminalUnavailable={terminalUnavailable}
                onActivate={api.activate}
                onClose={(g, id) => {
                  const tab = wb.groups[g]?.tabs.find((t) => t.id === id)
                  if (tab && onBeforeClose && !onBeforeClose(tab.content, id)) return
                  api.close(g, id)
                }}
                onNew={newIn}
                launchers={launcherItems}
                onNewLauncher={newLauncherIn}
                onSplit={(g) => api.splitAt(g + 1)}
                canSplit={wb.groups.length < MAX_GROUPS}
              />
              {/*
                两处 BlankTab 的 key 必须区分开。它们在三元的相邻分支上，同类型同位置，
                React 默认会把「空组面板」原地复用成「空白 tab 面板」——DOM 节点不换，
                于是面板的「挂载即聚焦」不会重跑，点了 + 之后焦点还留在 + 按钮上，
                印在面板上的 ⌘T 按下去没反应（走查实测）。给出各自的身份，让它真的重挂。
              */}
              {/* overflow-hidden 而不是 overflow-auto：终端 tab 的 xterm 在
                  凑不满一行滚轮时不会 preventDefault，父级再偷偷滑几像素就会
                  变成「网上滚一点然后卡住」。文件 / 会话流各自有自己的滚动区。 */}
              <div className="min-h-0 flex-1 overflow-hidden">
                {activeTab === null ? (
                  <BlankTab
                    key={`empty-${gi}`}
                    base={base}
                    onPick={(k) => startFromEmpty(gi, k)}
                    launchers={launcherItems}
                    onPickLauncher={(name) => startLauncherFromEmpty(gi, name)}
                    terminalUnavailable={terminalUnavailable}
                  />
                ) : activeTab.content.kind === 'blank' ? (
                  <BlankTab
                    key={activeTab.id}
                    base={base}
                    onPick={(k) => pick(gi, activeTab.id, k)}
                    launchers={launcherItems}
                    onPickLauncher={(name) => pickLauncher(gi, activeTab.id, name)}
                    terminalUnavailable={terminalUnavailable}
                  />
                ) : (
                  renderContent(activeTab.content, base, gi, activeTab.id)
                )}
              </div>
            </section>
          </Fragment>
        )
      })}
      {picking !== null && base !== null && (
        <TaskPickerDialog
          open
          base={base}
          tree={tree}
          tasks={tasks}
          onPick={(taskId) => {
            const content: TabContent = { kind: 'tui', taskId }
            // tabId 为 null = 从 + 菜单发起，没有等着被改写的 tab，新开一个
            if (picking.tabId === null) api.open(content, undefined, picking.group)
            else api.setContent(picking.group, picking.tabId, content)
            setPicking(null)
          }}
          onClose={() => setPicking(null)}
        />
      )}
    </div>
  )
}
