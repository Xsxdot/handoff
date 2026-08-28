import { act, createEvent, fireEvent, render, renderHook, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { WorkbenchPage } from './WorkbenchPage'
import { DRAG_DIR_MIME, DRAG_TAB_MIME, DRAG_TASK_MIME } from './paneDrop'
import { useWorkbench, type BaseDir } from './useWorkbench'

const local: BaseDir = { key: '/local', kind: 'workspace', path: '/local', label: 'local', projectName: 'handoff', machine: '' }
const remote: BaseDir = { key: '/remote@linux-01', kind: 'workspace', path: '/remote', label: 'remote', projectName: 'aim', machine: 'linux-01' }

function setRect(element: Element, width = 400, height = 400) {
  element.getBoundingClientRect = () => ({ left: 0, top: 0, right: width, bottom: height, width, height, x: 0, y: 0, toJSON: () => ({}) }) as DOMRect
}

function page(api: ReturnType<typeof useWorkbench>) {
  return <WorkbenchPage
    api={api}
    tree={null}
    tasks={[]}
    onAddProject={vi.fn()}
    renderContent={(content, base) => <div>{content.kind === 'file' ? content.rel : `${content.kind}:${base.projectName}`}</div>}
  />
}

describe('WorkbenchPage', () => {
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
    act(() => hook.result.current.splitColumn(groupId))
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
})
