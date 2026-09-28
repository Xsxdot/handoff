import { act, createEvent, fireEvent, render, renderHook, within } from '@testing-library/react'
import { useEffect } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { registerSessionDetailOpener } from '../rooms/sessionDetailOpener'
import { WorkbenchPage } from './WorkbenchPage'
import { DRAG_BASE_MIME, DRAG_DIR_MIME, DRAG_SESSION_MIME, DRAG_TAB_MIME, DRAG_TASK_MIME } from './paneDrop'
import { useWorkbench, sessionBase, type BaseDir } from './useWorkbench'
import type { Workbench } from './tabs'

const local: BaseDir = { key: '/local', kind: 'workspace', path: '/local', label: 'local', projectName: 'handoff', machine: '' }
const remote: BaseDir = { key: '/remote@linux-01', kind: 'workspace', path: '/remote', label: 'remote', projectName: 'aim', machine: 'linux-01' }

const panesOf = (view: ReturnType<typeof render>) =>
  [...view.container.querySelectorAll('[data-testid="workbench-pane"]')] as HTMLElement[]

function setRect(element: Element, width = 400, height = 400) {
  element.getBoundingClientRect = () => ({ left: 0, top: 0, right: width, bottom: height, width, height, x: 0, y: 0, toJSON: () => ({}) }) as DOMRect
}

function page(api: ReturnType<typeof useWorkbench>, singleFocus = false) {
  return <WorkbenchPage
    api={api}
    tree={null}
    tasks={[]}
    onAddProject={vi.fn()}
    singleFocus={singleFocus}
    renderContent={(content, base) => <div>{content.kind === 'file' ? content.rel : `${content.kind}:${base.projectName}`}</div>}
  />
}

// sessionDropPayload 造左栏会话行的拖放载荷（B358.8 #1）。
const sessionDropPayload = (sessionId = 'session:1', title = '架构物理化') => ({
  types: [DRAG_SESSION_MIME],
  getData: (key: string) => key === DRAG_SESSION_MIME ? JSON.stringify({ sessionId, title }) : '',
  setData: vi.fn(),
  effectAllowed: '',
  dropEffect: '',
})

describe('WorkbenchPage', () => {
  it('最外容器裁掉横向溢出：列压进窗口，不允许横滑', () => {
    const hook = renderHook(() => useWorkbench())
    const view = render(page(hook.result.current))
    expect((view.container.firstElementChild as HTMLElement).className).toContain('overflow-hidden')
    // 列不再带 240px 硬下限：下限由拖拽分隔的 minRatio 夹紧负责（spec §3）
    expect(view.container.firstElementChild!.innerHTML).not.toContain('min-w-[240px]')
  })

  it('中央只在顶栏渲染 group tab，pane 内没有一排文件 tab', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'file', rel: 'README.md' }, local))
    const view = render(page(hook.result.current))
    expect(view.getByRole('tablist')).toBeInTheDocument()
    const pane = view.container.querySelector('[data-testid="workbench-pane"]') as HTMLElement
    expect(pane).not.toBeNull()
    expect(within(pane).queryByRole('tab')).toBeNull()
    expect(within(pane).getAllByText('README.md').length).toBeGreaterThan(0)
  })

  it('从远端项目拖目录到窗格，在同一 group 形成远端 terminal pane', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.select(local))
    act(() => hook.result.current.open({ kind: 'tui', taskId: 'local' }, local))
    const groupId = hook.result.current.wb.activeGroupId
    act(() => hook.result.current.place({ kind: 'new', base: local, content: { kind: 'tui', taskId: 'second' } }, {
      groupId, column: 0, row: 0, zone: 'right',
    }))
    const view = render(page(hook.result.current))
    const panes = view.container.querySelectorAll('[data-testid="workbench-pane"]')
    expect(panes.length).toBe(2)
    const target = panes[1] as HTMLElement
    setRect(target)
    const dataTransfer = {
      types: [DRAG_DIR_MIME],
      getData: (key: string) => key === DRAG_DIR_MIME ? JSON.stringify(remote) : '',
      setData: vi.fn(), dropEffect: '',
    }
    const event = createEvent.drop(target, { dataTransfer })
    Object.defineProperty(event, 'clientX', { value: 200 })
    Object.defineProperty(event, 'clientY', { value: 200 })
    fireEvent(target, event)
    expect(hook.result.current.wb.groups[0].id).toBe(groupId)
    expect(hook.result.current.wb.groups[0].columns.flatMap((column) => column.panes).some((pane) =>
      pane?.base.projectName === 'aim' && pane.content.kind === 'terminal' && pane.content.rel === undefined,
    )).toBe(true)
  })

  it('消费已打开 Tab 的 DRAG_TAB_MIME 并把 pane 移到目标窗格', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.select(local))
    act(() => hook.result.current.open({ kind: 'terminal', seq: 1 }, local))
    const sourceGroupId = hook.result.current.wb.activeGroupId
    act(() => hook.result.current.addGroup())
    const targetGroupId = hook.result.current.wb.activeGroupId
    const sourceTab = hook.result.current.wb.groups
      .find((group) => group.id === sourceGroupId)!.columns[0].panes[0]!

    const view = render(page(hook.result.current))
    const panes = view.container.querySelectorAll('[data-testid="workbench-pane"]')
    const sourcePane = Array.from(panes).find((pane) => pane.querySelector(`[draggable="true"]`)) as HTMLElement
    const targetPane = Array.from(panes).find((pane) => pane !== sourcePane) as HTMLElement
    expect(sourcePane).not.toBeUndefined()
    expect(targetPane).not.toBeUndefined()
    setRect(targetPane)

    const dataTransfer = {
      types: [DRAG_TAB_MIME],
      getData: (key: string) => key === DRAG_TAB_MIME
        ? JSON.stringify({ groupId: sourceGroupId, tabId: sourceTab.id })
        : '',
      setData: vi.fn(),
      effectAllowed: '',
      dropEffect: '',
    }
    const event = createEvent.drop(targetPane, { dataTransfer })
    Object.defineProperty(event, 'clientX', { value: 200 })
    Object.defineProperty(event, 'clientY', { value: 200 })
    fireEvent(targetPane, event)

    const targetGroup = hook.result.current.wb.groups.find((group) => group.id === targetGroupId)!
    expect(targetGroup.columns[0].panes[0]).toMatchObject({ id: sourceTab.id, content: { kind: 'terminal', seq: 1 } })
    expect(hook.result.current.wb.groups.find((group) => group.id === sourceGroupId)!.columns[0].panes[0]).toBeNull()
  })

  it('窗格下半边最多增加第二格，第三次投放替换而不增加第三格', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'tui', taskId: 'one' }, local))
    const view = render(page(hook.result.current))
    const pane = view.container.querySelector('[data-testid="workbench-pane"]') as HTMLElement
    setRect(pane)
    const drop = (base: BaseDir, taskId: string) => {
      const dataTransfer = { types: [DRAG_DIR_MIME], getData: () => JSON.stringify({ ...base, path: `${base.path}/${taskId}` }), setData: vi.fn(), dropEffect: '' }
      const event = createEvent.drop(pane, { dataTransfer })
      Object.defineProperty(event, 'clientX', { value: 200 })
      Object.defineProperty(event, 'clientY', { value: 380 })
      fireEvent(pane, event)
    }
    drop(local, 'two')
    drop(local, 'three')
    const column = hook.result.current.wb.groups[0].columns[0]
    expect(column.panes).toHaveLength(2)
    expect(column.panes[1]?.content.kind).toBe('terminal')
  })

  it('满两格的上下投放给出可见退化提示', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'tui', taskId: 'one' }, local))
    const view = render(page(hook.result.current))
    let pane = view.container.querySelector('[data-testid="workbench-pane"]') as HTMLElement
    const setPaneRect = () => {
      pane.getBoundingClientRect = () => ({ left: 0, top: 0, right: 400, bottom: 400, width: 400, height: 400, x: 0, y: 0, toJSON: () => ({}) }) as DOMRect
    }
    setPaneRect()
    const drop = (taskId: string) => {
      const event = createEvent.drop(pane, {
        dataTransfer: {
        types: [DRAG_DIR_MIME],
        getData: () => JSON.stringify({ ...local, path: `/local/${taskId}` }),
        setData: vi.fn(), dropEffect: '',
        },
      })
      Object.defineProperty(event, 'clientX', { value: 200 })
      Object.defineProperty(event, 'clientY', { value: 380 })
      fireEvent(pane, event)
    }
    drop('two')
    view.rerender(page(hook.result.current))
    pane = view.container.querySelector('[data-testid="workbench-pane"]') as HTMLElement
    setPaneRect()
    drop('three')
    expect(view.getByRole('alert')).toHaveTextContent('这一列最多两格')
  })

  it('目录拖放来源无效时错误日志带当前项目、机器和路径', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.select(local))
    const view = render(page(hook.result.current))
    const pane = view.container.querySelector('[data-testid="workbench-pane"]') as HTMLElement
    setRect(pane)
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const event = createEvent.drop(pane, {
      dataTransfer: {
        types: [DRAG_DIR_MIME, DRAG_TASK_MIME],
        getData: (key: string) => key === DRAG_TASK_MIME ? '' : '{bad-json',
        setData: vi.fn(),
        dropEffect: '',
      },
    })
    Object.defineProperty(event, 'clientX', { value: 200 })
    Object.defineProperty(event, 'clientY', { value: 200 })
    fireEvent(pane, event)
    expect(warn).toHaveBeenCalledWith('workbench.drop.invalid_source', expect.objectContaining({
      project: 'handoff', machine: '', path: '/local',
    }))
    warn.mockRestore()
  })

  it('拖到右半区显示半区预览并通过 place 增加列', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'tui', taskId: 'local' }, local))
    const view = render(page(hook.result.current))
    const pane = view.container.querySelector('[data-testid="workbench-pane"]') as HTMLElement
    setRect(pane, 400, 400)
    const dataTransfer = {
      types: [DRAG_TASK_MIME, DRAG_BASE_MIME],
      getData: (key: string) => key === DRAG_TASK_MIME ? 'TASK-R' : JSON.stringify(remote),
      setData: vi.fn(), effectAllowed: '', dropEffect: '',
    }
    const dragOver = createEvent.dragOver(pane, { dataTransfer })
    Object.defineProperty(dragOver, 'clientX', { value: 360 })
    Object.defineProperty(dragOver, 'clientY', { value: 200 })
    fireEvent(pane, dragOver)
    expect(view.getByTestId('drop-right')).toHaveAttribute('data-zone', 'right')
    expect(view.getByTestId('drop-right')).toHaveClass('w-1/2')
    const drop = createEvent.drop(pane, { dataTransfer })
    Object.defineProperty(drop, 'clientX', { value: 360 })
    Object.defineProperty(drop, 'clientY', { value: 200 })
    fireEvent(pane, drop)
    expect(hook.result.current.wb.groups[0].columns).toHaveLength(2)
    expect(hook.result.current.wb.groups[0].columns[1].panes[0]).toMatchObject({
      base: remote, content: { kind: 'tui', taskId: 'TASK-R' },
    })
  })

  it('top / bottom 落点 1:1 原型：半区蓝遮罩 + 上下 4px 内边条', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'tui', taskId: 'local' }, local))
    const view = render(page(hook.result.current))
    const pane = view.container.querySelector('[data-testid="workbench-pane"]') as HTMLElement
    setRect(pane, 400, 400)
    const dataTransfer = { types: [DRAG_TASK_MIME], getData: () => '', setData: vi.fn(), dropEffect: '' }

    const dragOverTop = createEvent.dragOver(pane, { dataTransfer })
    Object.defineProperty(dragOverTop, 'clientX', { value: 200 })
    Object.defineProperty(dragOverTop, 'clientY', { value: 60 })
    fireEvent(pane, dragOverTop)
    expect(view.getByTestId('drop-top')).toHaveClass('bg-[rgba(37,99,235,0.32)]')
    expect(view.getByTestId('drop-top')).toHaveClass('shadow-[inset_0_4px_0_#2563eb]')

    const dragOverBottom = createEvent.dragOver(pane, { dataTransfer })
    Object.defineProperty(dragOverBottom, 'clientX', { value: 200 })
    Object.defineProperty(dragOverBottom, 'clientY', { value: 380 })
    fireEvent(pane, dragOverBottom)
    expect(view.getByTestId('drop-bottom')).toHaveClass('bg-[rgba(37,99,235,0.32)]')
    expect(view.getByTestId('drop-bottom')).toHaveClass('shadow-[inset_0_-4px_0_#2563eb]')
  })

  it('黑底终端窗格的落点遮罩用浅一档的蓝（原型 .pane.pane-term.drop-*）', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'terminal', seq: 1 }, local))
    const view = render(page(hook.result.current))
    const pane = view.container.querySelector('[data-testid="workbench-pane"]') as HTMLElement
    setRect(pane, 400, 400)
    const dataTransfer = { types: [DRAG_TASK_MIME], getData: () => '', setData: vi.fn(), dropEffect: '' }
    const dragOver = createEvent.dragOver(pane, { dataTransfer })
    Object.defineProperty(dragOver, 'clientX', { value: 200 })
    Object.defineProperty(dragOver, 'clientY', { value: 200 })
    fireEvent(pane, dragOver)
    expect(view.getByTestId('drop-center')).toHaveClass('bg-[rgba(147,197,253,0.5)]')
    expect(view.getByTestId('drop-center')).not.toHaveClass('bg-[rgba(37,99,235,0.18)]')
  })

  it('left 落点 1:1 原型：半区蓝遮罩 + 4px 内边条', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'tui', taskId: 'local' }, local))
    const view = render(page(hook.result.current))
    const pane = view.container.querySelector('[data-testid="workbench-pane"]') as HTMLElement
    setRect(pane, 400, 400)
    const dataTransfer = { types: [DRAG_TASK_MIME], getData: () => '', setData: vi.fn(), dropEffect: '' }
    const dragOver = createEvent.dragOver(pane, { dataTransfer })
    Object.defineProperty(dragOver, 'clientX', { value: 40 })
    Object.defineProperty(dragOver, 'clientY', { value: 200 })
    fireEvent(pane, dragOver)
    expect(view.getByTestId('drop-left')).toHaveClass('bg-[rgba(37,99,235,0.32)]')
    expect(view.getByTestId('drop-left')).toHaveClass('shadow-[inset_4px_0_0_#2563eb]')
  })

  it('center 落点 1:1 原型：18% 内缩、2px 描边、淡蓝底', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'tui', taskId: 'local' }, local))
    const view = render(page(hook.result.current))
    const pane = view.container.querySelector('[data-testid="workbench-pane"]') as HTMLElement
    setRect(pane, 400, 400)
    const dataTransfer = { types: [DRAG_TASK_MIME], getData: () => '', setData: vi.fn(), dropEffect: '' }
    const dragOver = createEvent.dragOver(pane, { dataTransfer })
    Object.defineProperty(dragOver, 'clientX', { value: 200 })
    Object.defineProperty(dragOver, 'clientY', { value: 200 })
    fireEvent(pane, dragOver)
    expect(view.getByTestId('drop-center')).toHaveClass('inset-[18%]')
    expect(view.getByTestId('drop-center')).toHaveClass('outline-[#2563eb]')
    expect(view.getByTestId('drop-center')).toHaveClass('bg-[rgba(37,99,235,0.18)]')
  })

  it('任务拖动进行期间内容层 pointer-events 关闭，dragend / drop 后恢复', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'tui', taskId: 'local' }, local))
    const view = render(page(hook.result.current))
    const contentLayer = () => view.container.querySelector('[data-testid="pane-content"]') as HTMLElement
    expect(contentLayer().className).not.toContain('pointer-events-none')

    // 模拟从左栏任务行（带 data-drag-task）开始的拖动：事件冒泡到 window
    act(() => {
      const source = document.createElement('span')
      source.setAttribute('data-drag-task', '1')
      document.body.appendChild(source)
      source.dispatchEvent(new Event('dragstart', { bubbles: true }))
      source.remove()
    })
    expect(contentLayer().className).toContain('pointer-events-none')

    act(() => { window.dispatchEvent(new Event('dragend')) })
    expect(contentLayer().className).not.toContain('pointer-events-none')

    act(() => {
      const source = document.createElement('span')
      source.setAttribute('data-drag-task', '1')
      document.body.appendChild(source)
      source.dispatchEvent(new Event('dragstart', { bubbles: true }))
      source.remove()
    })
    act(() => { window.dispatchEvent(new Event('drop')) })
    expect(contentLayer().className).not.toContain('pointer-events-none')
  })

  it('非任务来源的 dragstart 不触发内容层放行', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'tui', taskId: 'local' }, local))
    const view = render(page(hook.result.current))
    act(() => {
      const source = document.createElement('span')
      document.body.appendChild(source)
      source.dispatchEvent(new Event('dragstart', { bubbles: true }))
      source.remove()
    })
    const contentLayer = view.container.querySelector('[data-testid="pane-content"]') as HTMLElement
    expect(contentLayer.className).not.toContain('pointer-events-none')
  })

  it.each([
    { label: '缺失', types: [DRAG_TASK_MIME], basePayload: '' },
    { label: '损坏', types: [DRAG_TASK_MIME, DRAG_BASE_MIME], basePayload: '{bad-json' },
  ])('任务 MIME 的 DRAG_BASE_MIME $label 时拒绝放置，不回退到当前选中目录', ({ types, basePayload }) => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.select(local))
    const view = render(page(hook.result.current))
    const pane = view.container.querySelector('[data-testid="workbench-pane"]') as HTMLElement
    setRect(pane)
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const event = createEvent.drop(pane, {
      dataTransfer: {
        types,
        getData: (key: string) => key === DRAG_TASK_MIME ? 'TASK-BAD-BASE' : basePayload,
        setData: vi.fn(),
        dropEffect: '',
      },
    })
    Object.defineProperty(event, 'clientX', { value: 200 })
    Object.defineProperty(event, 'clientY', { value: 200 })

    fireEvent(pane, event)

    expect(hook.result.current.wb.groups[0].columns[0].panes[0]).toBeNull()
    expect(warn).toHaveBeenCalledWith('workbench.drop.invalid_source', expect.objectContaining({
      project: 'handoff', machine: '', path: '/local',
    }))
    warn.mockRestore()
  })

  it('空 pane 的关闭按钮穿过 WorkbenchPage 并删除该格', () => {
    const hook = renderHook(() => useWorkbench())
    const view = render(page(hook.result.current))
    fireEvent.click(view.getByRole('button', { name: '关闭 空窗格' }))
    expect(hook.result.current.wb.groups).toHaveLength(1)
    expect(hook.result.current.wb.groups[0].columns).toEqual([{ panes: [null] }])
  })

  it.each<[string, string]>([
    ['JSON 损坏', '{bad-json'],
    ['字段缺失', JSON.stringify({ key: local.key, kind: local.kind })],
  ])('单独目录 MIME %s 时拒绝放置，不回退到当前选中目录', (_label, directoryPayload) => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.select(local))
    const view = render(page(hook.result.current))
    const pane = view.container.querySelector('[data-testid="workbench-pane"]') as HTMLElement
    setRect(pane)
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const event = createEvent.drop(pane, {
      dataTransfer: {
        types: [DRAG_DIR_MIME],
        getData: (key: string) => key === DRAG_DIR_MIME ? directoryPayload : '',
        setData: vi.fn(),
        dropEffect: '',
      },
    })
    Object.defineProperty(event, 'clientX', { value: 200 })
    Object.defineProperty(event, 'clientY', { value: 200 })

    fireEvent(pane, event)

    expect(hook.result.current.wb.groups[0].columns[0].panes[0]).toBeNull()
    expect(warn).toHaveBeenCalledWith('workbench.drop.invalid_source', expect.objectContaining({
      project: 'handoff', machine: '', path: '/local',
    }))
    warn.mockRestore()
  })

  it('后台终端组叠在原位：不移出视口，但让出命中，且能缩到小于画布固有宽', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'terminal', seq: 1 }, local))
    const firstGroup = hook.result.current.wb.activeGroupId
    act(() => hook.result.current.addGroup())
    act(() => hook.result.current.open({ kind: 'terminal', seq: 2 }, local))
    const view = render(page(hook.result.current))
    const panes = view.container.querySelectorAll('[data-testid="workbench-pane"]')
    expect(panes.length).toBe(2)
    const groups = () => [...view.container.querySelectorAll('[data-testid="workbench-group"]')] as HTMLElement[]
    expect(groups()).toHaveLength(2)
    const hiddenRoot = groups().find((el) => el.getAttribute('aria-hidden') === 'true')
    expect(hiddenRoot).toBeDefined()
    // 后台组必须让出命中：z-0 的 WebGL 画布会从激活组手里抢走滚轮，
    // 看起来就是「眼前这条 TUI 划不动」。不移出视口、不 opacity-0。
    expect(hiddenRoot!.className).toContain('pointer-events-none')
    expect(hiddenRoot!.hasAttribute('inert')).toBe(true)
    expect(hiddenRoot!.className).not.toContain('-left-[10000px]')
    expect(hiddenRoot!.className).not.toContain('opacity-0')
    expect(hiddenRoot!.className).not.toContain('invisible')
    expect(hiddenRoot!.className).toContain('z-0')
    expect(hiddenRoot!.className).toContain('inset-0')
    expect(hiddenRoot!.className).toContain('min-w-0')
    act(() => hook.result.current.activateGroup(firstGroup))
    view.rerender(page(hook.result.current))
    const revealed = groups().find((el) => el.getAttribute('aria-hidden') === 'false')
    expect(revealed).toBeDefined()
    expect(revealed!.className).toContain('z-10')
    expect(revealed!.className).not.toContain('pointer-events-none')
    expect(revealed!.hasAttribute('inert')).toBe(false)
  })

  // B367 回归：换组只许翻 visible 标记，不许重挂终端子树。
  // 双条件 `{a && el} {b && el}` 把同一 div 放在两个 keyless 槽位，
  // visible 一翻 React 就按槽位卸载重建——xterm 全套 teardown/replay、
  // Viewport 定时器打在已 dispose 的 RenderService 上刷 dimensions、
  // PTY 重连定时器变孤儿。keep-alive 名存实亡。
  function MountProbe({ id, mounts }: { id: string; mounts: Map<string, number> }) {
    useEffect(() => {
      mounts.set(id, (mounts.get(id) ?? 0) + 1)
    }, [id, mounts])
    return <div data-mount-probe={id} />
  }

  it('换组只翻 visible 标记：组容器是同一 DOM 节点，终端内容只挂载一次', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'terminal', seq: 1 }, local))
    const firstGroup = hook.result.current.wb.activeGroupId
    act(() => hook.result.current.addGroup())
    act(() => hook.result.current.open({ kind: 'terminal', seq: 2 }, local))
    const secondGroup = hook.result.current.wb.activeGroupId
    const mounts = new Map<string, number>()
    const view = render(
      <WorkbenchPage
        api={hook.result.current}
        tree={null}
        tasks={[]}
        onAddProject={vi.fn()}
        renderContent={(content, _base, groupId, tabId) => (
          <MountProbe id={`${groupId}:${tabId}:${content.kind}`} mounts={mounts} />
        )}
      />,
    )
    const rerenderPage = () => view.rerender(
      <WorkbenchPage
        api={hook.result.current}
        tree={null}
        tasks={[]}
        onAddProject={vi.fn()}
        renderContent={(content, _base, groupId, tabId) => (
          <MountProbe id={`${groupId}:${tabId}:${content.kind}`} mounts={mounts} />
        )}
      />,
    )
    const groups = () => [...view.container.querySelectorAll('[data-testid="workbench-group"]')] as HTMLElement[]
    expect(groups()).toHaveLength(2)
    const before = groups()
    act(() => hook.result.current.activateGroup(firstGroup))
    rerenderPage()
    act(() => hook.result.current.activateGroup(secondGroup))
    rerenderPage()
    const after = groups()
    expect(after).toHaveLength(2)
    // 组容器必须是同一批 DOM 节点：换组只改类名/aria/inert，不替换子树
    expect(after[0]).toBe(before[0])
    expect(after[1]).toBe(before[1])
    // 每个 tab 内容只挂载一次：重挂 = xterm 重建 + backlog 重放 + 重连风暴
    expect(mounts.size).toBe(2)
    for (const count of mounts.values()) expect(count).toBe(1)
  })

  it('纯文件组切走即卸，不占终端 keep-alive', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'file', rel: 'a.md' }, local))
    act(() => hook.result.current.addGroup())
    act(() => hook.result.current.open({ kind: 'file', rel: 'b.md' }, local))
    const view = render(page(hook.result.current))
    expect(view.container.querySelectorAll('[data-testid="workbench-group"]')).toHaveLength(1)
    expect(view.container.querySelectorAll('[data-testid="workbench-pane"]')).toHaveLength(1)
  })

  it('会话拖到窗格右缘 → 新列分屏出会话 tab，base 为会话基准（B358.8 #1 主诉求）', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'tui', taskId: 'local' }, local))
    const view = render(page(hook.result.current))
    const pane = view.container.querySelector('[data-testid="workbench-pane"]') as HTMLElement
    setRect(pane)
    const dataTransfer = sessionDropPayload()

    const dragOver = createEvent.dragOver(pane, { dataTransfer })
    Object.defineProperty(dragOver, 'clientX', { value: 360 })
    Object.defineProperty(dragOver, 'clientY', { value: 200 })
    fireEvent(pane, dragOver)
    expect(view.getByTestId('drop-right')).toBeInTheDocument()

    const drop = createEvent.drop(pane, { dataTransfer })
    Object.defineProperty(drop, 'clientX', { value: 360 })
    Object.defineProperty(drop, 'clientY', { value: 200 })
    fireEvent(pane, drop)
    expect(hook.result.current.wb.groups[0].columns).toHaveLength(2)
    const placed = hook.result.current.wb.groups[0].columns[1].panes[0]
    expect(placed).toMatchObject({ base: sessionBase('session:1'), content: { kind: 'session', sessionId: 'session:1', title: '架构物理化' } })
  })

  it('会话已开在别的组时投到窗格 center 整支移动过去，不复制出第二个 tab（去重前置）', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'session', sessionId: 'session:1', title: '架构物理化' }, sessionBase('session:1')))
    const sourceTabId = hook.result.current.wb.groups[0].columns[0].panes[0]!.id
    act(() => hook.result.current.addGroup()) // g2 空组
    const targetGroupId = hook.result.current.wb.activeGroupId
    const view = render(page(hook.result.current))
    const panes = view.container.querySelectorAll('[data-testid="workbench-pane"]')
    const target = panes[panes.length - 1] as HTMLElement
    setRect(target)
    const drop = createEvent.drop(target, { dataTransfer: sessionDropPayload() })
    Object.defineProperty(drop, 'clientX', { value: 200 })
    Object.defineProperty(drop, 'clientY', { value: 200 })
    fireEvent(target, drop)

    const sessionTabs = hook.result.current.wb.groups
      .flatMap((group) => group.columns.flatMap((column) => column.panes))
      .filter((tab) => tab?.content.kind === 'session')
    expect(sessionTabs).toHaveLength(1)
    expect(sessionTabs[0]!.id).toBe(sourceTabId)
    expect(sessionTabs[0]!.base.projectName).toBe('')
    expect(hook.result.current.wb.groups.find((group) => group.id === targetGroupId)!.columns[0].panes[0]!.id).toBe(sourceTabId)
  })

  it('会话已开在本组时投到本组窗格只激活，不改变布局', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'session', sessionId: 'session:1', title: '架构物理化' }, sessionBase('session:1')))
    const view = render(page(hook.result.current))
    const pane = view.container.querySelector('[data-testid="workbench-pane"]') as HTMLElement
    setRect(pane)
    const drop = createEvent.drop(pane, { dataTransfer: sessionDropPayload() })
    Object.defineProperty(drop, 'clientX', { value: 200 })
    Object.defineProperty(drop, 'clientY', { value: 200 })
    fireEvent(pane, drop)
    const sessionTabs = hook.result.current.wb.groups
      .flatMap((group) => group.columns.flatMap((column) => column.panes))
      .filter((tab) => tab?.content.kind === 'session')
    expect(sessionTabs).toHaveLength(1)
    expect(hook.result.current.wb.groups).toHaveLength(1)
    expect(hook.result.current.wb.activeGroupId).toBe('g1')
  })

  it('会话 MIME 载荷损坏时拒绝放置，布局不变', () => {
    const hook = renderHook(() => useWorkbench())
    const view = render(page(hook.result.current))
    const pane = view.container.querySelector('[data-testid="workbench-pane"]') as HTMLElement
    setRect(pane)
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const dataTransfer = { types: [DRAG_SESSION_MIME], getData: () => '{bad-json', setData: vi.fn(), dropEffect: '' }
    const drop = createEvent.drop(pane, { dataTransfer })
    Object.defineProperty(drop, 'clientX', { value: 200 })
    Object.defineProperty(drop, 'clientY', { value: 200 })
    fireEvent(pane, drop)
    expect(hook.result.current.wb.groups[0].columns[0].panes[0]).toBeNull()
    expect(warn).toHaveBeenCalledWith('workbench.drop.invalid_mime', expect.objectContaining({ reason: 'session MIME payload is missing or invalid' }))
    warn.mockRestore()
  })

  it('会话窗格标题行含 ⋯：与标题/× 同行，点击开启该会话详情（走查 09-17 #3）', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'session', sessionId: 'session:1', title: '架构物理化' }, sessionBase('session:1')))
    const view = render(page(hook.result.current))
    const pane = view.container.querySelector('[data-testid="workbench-pane"]') as HTMLElement
    const more = within(pane).getByRole('button', { name: '会话详情' })
    expect(more).toHaveTextContent('⋯')
    // 与标题、关闭钮同处窗格标题行（内容区 stub 不渲染 ⋯，按钮只能来自标题行）
    const close = within(pane).getByRole('button', { name: '关闭 会话 · 架构物理化' })
    expect(more.parentElement).toContainElement(close)
    const opener = vi.fn()
    const unregister = registerSessionDetailOpener('session:1', opener)
    fireEvent.click(more)
    expect(opener).toHaveBeenCalledTimes(1)
    unregister()
  })
})

// —— B369.9 单焦点投影（plan §5 T1）：phone 档两层叠层 + 三件套随焦点翻转。
// 变异锁「结构分支即红」落在这一节：两窗格必须都在 DOM（①），非焦点格用
// z-0 pointer-events-none + aria-hidden + inert 压层（②），不得出现
// hidden / invisible / opacity-0 / display:none 任一实现（③）。——
describe('B369.9 单焦点投影', () => {
  // 双列布局：终端落 (0,0)，place zone right 追加第二列并把焦点带到 (1,0)。
  function renderTwoColumns(singleFocus = true) {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'terminal', seq: 1 }, local))
    const groupId = hook.result.current.wb.activeGroupId
    act(() => hook.result.current.place({ kind: 'new', base: remote, content: { kind: 'terminal', seq: 2 } }, { groupId, column: 0, row: 0, zone: 'right' }))
    const view = render(page(hook.result.current, singleFocus))
    const panes = [...view.container.querySelectorAll('[data-testid="workbench-pane"]')] as HTMLElement[]
    return { hook, view, groupId, panes }
  }

  // 双格布局：终端落 (0,0)，place zone bottom 追加第二格并把焦点带到 (0,1)。
  function renderTwoPanes(singleFocus = true) {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'terminal', seq: 1 }, local))
    const groupId = hook.result.current.wb.activeGroupId
    act(() => hook.result.current.place({ kind: 'new', base: local, content: { kind: 'terminal', seq: 2 } }, { groupId, column: 0, row: 0, zone: 'bottom' }))
    const view = render(page(hook.result.current, singleFocus))
    const panes = [...view.container.querySelectorAll('[data-testid="workbench-pane"]')] as HTMLElement[]
    return { hook, view, groupId, panes }
  }

  it('phone 双列：非焦点列/格叠层三件套，焦点列/格 z-10，GroupDivider 不在场', () => {
    const { view, panes } = renderTwoColumns()
    expect(panesOf(view)).toHaveLength(2) // 变异锁①：两窗格都在 DOM，谁也不许被卸载
    expect(view.container.querySelectorAll('[data-testid="workbench-group"]')).toHaveLength(1)
    // place 后焦点在第二列 (1,0)：panes[0] = col0 非焦点，panes[1] = col1 焦点
    const [col0Pane, col1Pane] = panes
    expect(col0Pane.className).toBe('absolute inset-0 flex min-h-0 flex-col bg-background z-0 pointer-events-none')
    expect(col0Pane.getAttribute('aria-hidden')).toBe('true')
    expect(col0Pane.hasAttribute('inert')).toBe(true)
    expect(col1Pane.className).toBe('absolute inset-0 flex min-h-0 flex-col bg-background z-10')
    expect(col1Pane.getAttribute('aria-hidden')).toBe('false')
    expect(col1Pane.hasAttribute('inert')).toBe(false)
    // 列层同构：焦点判据 group.focus[0] === columnIndex
    const col0 = col0Pane.parentElement as HTMLElement
    const col1 = col1Pane.parentElement as HTMLElement
    expect(col0.className).toBe('absolute inset-0 flex min-w-0 min-h-0 flex-col z-0 pointer-events-none')
    expect(col0.getAttribute('aria-hidden')).toBe('true')
    expect(col0.hasAttribute('inert')).toBe(true)
    expect(col1.className).toBe('absolute inset-0 flex min-w-0 min-h-0 flex-col z-10')
    expect(col1.getAttribute('aria-hidden')).toBe('false')
    expect(col1.hasAttribute('inert')).toBe(false)
    // 列容器追加 relative 作叠层锚
    expect((col0.parentElement as HTMLElement).className).toBe('flex min-h-0 flex-1 overflow-hidden bg-border relative')
    // GroupDivider 摘除（phone 组内没有并排列可拖）
    expect(view.queryByRole('separator')).toBeNull()
  })

  it('phone 双格（同列两层）：非焦点格三件套，焦点格 z-10，焦点列整体可达', () => {
    const { view, panes } = renderTwoPanes()
    expect(panesOf(view)).toHaveLength(2)
    const [topPane, bottomPane] = panes // 焦点在 (0,1)：top 非焦点，bottom 焦点
    expect(topPane.className).toBe('absolute inset-0 flex min-h-0 flex-col bg-background z-0 pointer-events-none')
    expect(topPane.getAttribute('aria-hidden')).toBe('true')
    expect(topPane.hasAttribute('inert')).toBe(true)
    expect(bottomPane.className).toBe('absolute inset-0 flex min-h-0 flex-col bg-background z-10')
    expect(bottomPane.getAttribute('aria-hidden')).toBe('false')
    expect(bottomPane.hasAttribute('inert')).toBe(false)
    // 同列双格：列是焦点列，列层不加三件套
    const column = topPane.parentElement as HTMLElement
    expect(column.className).toBe('absolute inset-0 flex min-w-0 min-h-0 flex-col z-10')
    expect(column.hasAttribute('inert')).toBe(false)
    expect(view.queryByRole('separator')).toBeNull()
  })

  it('变异锁·结构分支即红：非焦点格不得用 hidden/invisible/opacity-0/display:none 实现', () => {
    const { panes } = renderTwoColumns()
    const nonFocus = panes[0]
    // ③ 词边界匹配（按类名 token 精确比对）：display:none / visibility /
    // opacity-0 任一实现转红；inert 层靠属性闸，不靠捏尺寸
    const tokens = nonFocus.className.split(/\s+/)
    expect(tokens).not.toContain('hidden')
    expect(tokens).not.toContain('invisible')
    expect(tokens).not.toContain('opacity-0')
    expect(nonFocus.style.display).toBe('')
    expect(nonFocus.style.visibility).toBe('')
  })

  it('act(activate) 切焦点后三件套随焦点翻转（类与属性双侧）', () => {
    const { hook, view, groupId, panes } = renderTwoPanes()
    const [topPane, bottomPane] = panes
    const firstTabId = hook.result.current.wb.groups[0].columns[0].panes[0]!.id
    act(() => hook.result.current.activate(groupId, firstTabId))
    view.rerender(page(hook.result.current, true))
    // 焦点回到 (0,0)：top 变焦点层，bottom 挂三件套
    expect(topPane.className).toBe('absolute inset-0 flex min-h-0 flex-col bg-background z-10')
    expect(topPane.getAttribute('aria-hidden')).toBe('false')
    expect(topPane.hasAttribute('inert')).toBe(false)
    expect(bottomPane.className).toBe('absolute inset-0 flex min-h-0 flex-col bg-background z-0 pointer-events-none')
    expect(bottomPane.getAttribute('aria-hidden')).toBe('true')
    expect(bottomPane.hasAttribute('inert')).toBe(true)
    // DOM 节点身份不变：跨档/跨焦点只翻类与属性，零重挂
    const panesAfter = [...view.container.querySelectorAll('[data-testid="workbench-pane"]')]
    expect(panesAfter[0]).toBe(topPane)
    expect(panesAfter[1]).toBe(bottomPane)
  })

  it('空槽焦点格：focus 落 null 格时该格是焦点层（无三件套），非空格全 inert', () => {
    const hook = renderHook(() => useWorkbench())
    // restore 同款入口 hydrate 造 [非空, null] 双格、焦点落在 null 格（(0,1)）
    const layout: Workbench = {
      activeGroupId: 'g1',
      groups: [{
        id: 'g1', name: '', autoName: false,
        columns: [{ panes: [{ id: 't1', base: local, content: { kind: 'terminal', seq: 1 } }, null] }],
        sizes: [1],
        focus: [0, 1],
      }],
    }
    act(() => hook.result.current.hydrate(layout))
    const view = render(page(hook.result.current, true))
    const panes = [...view.container.querySelectorAll('[data-testid="workbench-pane"]')] as HTMLElement[]
    expect(panes).toHaveLength(2)
    const [filled, empty] = panes
    // null 格是焦点层：z-10、无三件套，窗格头照常渲染「空窗格」
    expect(empty.className).toBe('absolute inset-0 flex min-h-0 flex-col bg-background z-10')
    expect(empty.getAttribute('aria-hidden')).toBe('false')
    expect(empty.hasAttribute('inert')).toBe(false)
    expect(within(empty).getByText('空窗格')).toBeInTheDocument()
    // 非空格全 inert
    expect(filled.className).toBe('absolute inset-0 flex min-h-0 flex-col bg-background z-0 pointer-events-none')
    expect(filled.getAttribute('aria-hidden')).toBe('true')
    expect(filled.hasAttribute('inert')).toBe(true)
  })

  it('pane-switcher：只在焦点格头渲染，option 按列序行序只含非空格', () => {
    const { panes } = renderTwoPanes()
    // 只在焦点格头（非焦点格在 inert 层且渲染条件不含它）
    // 非焦点格头不渲染（条件不含它，且整体在 inert 层内）
    expect(panes[0].querySelectorAll('[data-testid="pane-switcher"]')).toHaveLength(0)
    const all = [...document.querySelectorAll('[data-testid="pane-switcher"]')] as HTMLSelectElement[]
    expect(all).toHaveLength(1)
    expect(all[0].getAttribute('aria-label')).toBe('切换窗格')
    // option = 本组非空格按列序行序枚举（同列双格，无空槽）
    const options = [...all[0].querySelectorAll('option')]
    expect(options).toHaveLength(2)
    expect(options[0]!.textContent).toContain('bash · local')
  })

  it('pane-switcher：fireEvent.change 选中另一格 → 焦点翻转 + 三件套随焦点翻转', () => {
    const { hook, view, groupId, panes } = renderTwoPanes()
    const switcher = document.querySelector('[data-testid="pane-switcher"]') as HTMLSelectElement
    const options = [...switcher.querySelectorAll('option')]
    const focusedTabId = hook.result.current.wb.groups[0].columns[0].panes[1]!.id
    const other = options.find((option) => option.value !== focusedTabId)!
    fireEvent.change(switcher, { target: { value: other.value } })
    view.rerender(page(hook.result.current, true))
    // 焦点落到第一格：类与属性翻转
    expect(panes[0].className).toBe('absolute inset-0 flex min-h-0 flex-col bg-background z-10')
    expect(panes[0].hasAttribute('inert')).toBe(false)
    expect(panes[1].className).toBe('absolute inset-0 flex min-h-0 flex-col bg-background z-0 pointer-events-none')
    expect(panes[1].hasAttribute('inert')).toBe(true)
    expect(hook.result.current.wb.activeGroupId).toBe(groupId)
  })

  it('pane-switcher：空槽不占切换位（option 数 = 非空格数）', () => {
    const hook = renderHook(() => useWorkbench())
    // col0 [t1, null] + col1 [t2]：tabCount=2 → switcher 在场；null 槽不是切换目标
    const layout: Workbench = {
      activeGroupId: 'g1',
      groups: [{
        id: 'g1', name: '', autoName: false,
        columns: [
          { panes: [{ id: 't1', base: local, content: { kind: 'terminal', seq: 1 } }, null] },
          { panes: [{ id: 't2', base: remote, content: { kind: 'terminal', seq: 2 } }] },
        ],
        sizes: [1, 1],
        focus: [0, 0],
      }],
    }
    act(() => hook.result.current.hydrate(layout))
    const view = render(page(hook.result.current, true))
    const switcher = view.container.querySelector('[data-testid="pane-switcher"]') as HTMLSelectElement
    expect(switcher).not.toBeNull()
    const options = [...switcher.querySelectorAll('option')]
    expect(options).toHaveLength(2)
    expect(options.map((option) => option.value)).toEqual(['t1', 't2'])
  })

  it('pane-switcher：单格组与桌面档都不渲染', () => {
    const single = renderHook(() => useWorkbench())
    act(() => single.result.current.open({ kind: 'terminal', seq: 1 }, local))
    const singleView = render(page(single.result.current, true))
    expect(singleView.container.querySelector('[data-testid="pane-switcher"]')).toBeNull()
    const { view } = renderTwoPanes(false)
    expect(view.container.querySelector('[data-testid="pane-switcher"]')).toBeNull()
  })

  it('pane-switcher onClick stopPropagation 不触发窗格冗余 activate', () => {
    const { hook } = renderTwoPanes()
    const switcher = document.querySelector('[data-testid="pane-switcher"]') as HTMLSelectElement
    const wbBefore = hook.result.current.wb
    fireEvent.click(switcher)
    // activate 若被调用会 cloneWorkbench 换 wb 引用；wb 不变 = 冒泡被拦下
    expect(hook.result.current.wb).toBe(wbBefore)
  })

  it('桌面/pad 守卫：不传 singleFocus，列/窗格类串逐字节现状、separator 在场', () => {
    const { view, panes } = renderTwoColumns(false)
    // 窗格类串逐字节等于现状（最高纪律：非投影档渲染输出零漂移）
    for (const pane of panes) {
      expect(pane.className).toBe('relative flex min-h-0 flex-1 flex-col bg-background')
      expect(pane.hasAttribute('inert')).toBe(false)
      expect(pane.getAttribute('aria-hidden')).toBeNull()
    }
    const column = panes[0].parentElement as HTMLElement
    expect(column.className).toBe('flex min-w-0 min-h-0 flex-1 flex-col')
    expect(column.getAttribute('aria-hidden')).toBeNull()
    // 列容器不加 relative、双列 separator 在场
    expect((column.parentElement as HTMLElement).className).toBe('flex min-h-0 flex-1 overflow-hidden bg-border')
    expect(view.getAllByRole('separator')).toHaveLength(1)
  })
})
