// TabBar.test.tsx —— 顶部组标签条（option-1 chrome 换皮）的测试。
//
// setup 照抄 WorkbenchPage.test.tsx：renderHook(useWorkbench) 构造组/列/格布局，
// 再把 groups/activeGroupId 交给 TabBar 渲染。tab = 组（基线语义），样式换皮。
import { act, fireEvent, render, renderHook, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { TabBar } from './TabBar'
import { DRAG_GROUP_MIME } from './paneDrop'
import { EMPTY_WORKBENCH, createGroup as createGroupLayout, openTab } from './tabs'
import { useWorkbench, type BaseDir } from './useWorkbench'

const local: BaseDir = { key: '/local', kind: 'workspace', path: '/local', label: 'local', projectName: 'handoff', machine: '' }

type Api = ReturnType<typeof useWorkbench>

function renderBar(api: Api, over: { taskName?: (id: string) => string | undefined } = {}) {
  return render(
    <TabBar
      groups={api.wb.groups}
      activeGroupId={api.wb.activeGroupId}
      base={api.base}
      onActivateGroup={vi.fn()}
      onCloseGroup={vi.fn()}
      onNew={vi.fn()}
      onNewGroup={vi.fn()}
      onMoveGroup={vi.fn()}
      {...over}
    />,
  )
}

describe('TabBar（组标签条）', () => {
  it('autoName 组显示焦点内容名：tui 用 resolver 的任务原名，无 resolver 回退 TUI · 前缀', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'tui', taskId: 'f22ed520abc' }, local))

    const withResolver = renderBar(hook.result.current, { taskName: () => '审 B264' })
    expect(withResolver.getByRole('tab', { name: '审 B264' })).toBeInTheDocument()
    withResolver.unmount()

    const withoutResolver = renderBar(hook.result.current)
    expect(withoutResolver.getByRole('tab', { name: 'TUI · f22ed520' })).toBeInTheDocument()
  })

  it('显式命名的组保留组名；空 autoName 组显示基线组名', () => {
    const hook = renderHook(() => useWorkbench())
    // 显式命名组：hydrate 用 tabs.ts 的 createGroup 纯函数构造
    act(() => {
      const wb = createGroupLayout(openTab(EMPTY_WORKBENCH, local, { kind: 'file', rel: 'a.ts' }), '第二组')
      hook.result.current.hydrate(wb)
    })
    const named = renderBar(hook.result.current)
    expect(named.getByRole('tab', { name: 'a.ts' })).toBeInTheDocument()
    expect(named.getByRole('tab', { name: '第二组' })).toBeInTheDocument()
    named.unmount()

    // 空 autoName 组（addGroup 造出的「组 2」）显示基线组名，不是「新建标签页」
    const hook2 = renderHook(() => useWorkbench())
    act(() => hook2.result.current.open({ kind: 'file', rel: 'a.ts' }, local))
    act(() => hook2.result.current.addGroup())
    const view = renderBar(hook2.result.current)
    expect(view.getByRole('tab', { name: 'a.ts' })).toBeInTheDocument()
    expect(view.getByRole('tab', { name: '组 2' })).toBeInTheDocument()
  })

  it('图标按组焦点内容种类：tui=资产图、terminal/file=线性图标、空组=加号', () => {
    const hook = renderHook(() => useWorkbench())
    // 每组只渲染焦点内容的图标：四组分别聚焦 tui/terminal/file/空
    act(() => hook.result.current.open({ kind: 'tui', taskId: 'T1' }, local))
    act(() => hook.result.current.addGroup())
    act(() => hook.result.current.open({ kind: 'terminal', seq: 1 }, local))
    act(() => hook.result.current.addGroup())
    act(() => hook.result.current.open({ kind: 'file', rel: 'web/README.md' }, local))
    act(() => hook.result.current.addGroup())

    const view = renderBar(hook.result.current)
    expect(view.container.querySelector('img[src*="dispatch-task"]')).not.toBeNull()
    expect(view.container.querySelector('.lucide-terminal')).not.toBeNull()
    expect(view.container.querySelector('.lucide-file-text')).not.toBeNull()
    expect(view.container.querySelector('.lucide-plus')).not.toBeNull()
  })

  it('所有标签都有状态面，只有激活组使用深一档药丸色', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'file', rel: 'a.ts' }, local))
    act(() => hook.result.current.addGroup())

    const view = renderBar(hook.result.current)
    const surfaces = view.getAllByTestId('tab-surface')
    expect(surfaces).toHaveLength(2)
    // 激活组是最后新建的空组
    expect(surfaces.find((surface) => surface.dataset.active === 'true')?.textContent).toContain('组 2')
    expect(surfaces.find((surface) => surface.dataset.active === 'false')).toBeInTheDocument()
  })

  it('3 个组时组间短分隔线为 2 条，标签栏不渲染 + 按钮', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'file', rel: 'a.ts' }, local))
    act(() => hook.result.current.addGroup())
    act(() => hook.result.current.addGroup())

    const view = renderBar(hook.result.current)
    expect(view.getAllByTestId('tab-sep')).toHaveLength(2)
    expect(view.getByRole('button', { name: '新建标签组' })).toHaveClass('sr-only')
    expect(view.getAllByRole('button', { name: '新建内容' }).every((button) => button.classList.contains('sr-only'))).toBe(true)
  })

  it('点击组触发激活回调；关闭钮位于标签状态面内', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'file', rel: 'a.ts' }, local))
    act(() => hook.result.current.addGroup())
    const onActivateGroup = vi.fn()
    const onCloseGroup = vi.fn()
    render(
      <TabBar
        groups={hook.result.current.wb.groups}
        activeGroupId={hook.result.current.wb.activeGroupId}
        base={hook.result.current.base}
        onActivateGroup={onActivateGroup}
        onCloseGroup={onCloseGroup}
        onNew={vi.fn()}
        onNewGroup={vi.fn()}
        onMoveGroup={vi.fn()}
      />,
    )
    // 第一组是 autoName 且焦点内容是 a.ts：标签显示 a.ts
    fireEvent.click(screen.getByRole('tab', { name: 'a.ts' }))
    expect(onActivateGroup).toHaveBeenCalledWith('g1')
    const close = screen.getByRole('button', { name: '关闭 a.ts' })
    expect(close.closest('[data-testid="tab-surface"]')).not.toBeNull()
    fireEvent.click(close)
    expect(onCloseGroup).toHaveBeenCalledWith('g1')
    expect(screen.getByRole('button', { name: '新建标签组' })).toHaveClass('sr-only')
    expect(screen.getAllByRole('button', { name: '新建内容' }).every((button) => button.classList.contains('sr-only'))).toBe(true)
  })

  it('组拖动写入 DRAG_GROUP_MIME；多窗格来源投放显示告警，不调用 onMoveGroup', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'file', rel: 'a.ts' }, local))
    act(() => hook.result.current.open({ kind: 'file', rel: 'b.ts' }, local)) // g1 变两列（多窗格）
    act(() => hook.result.current.addGroup()) // g2
    const onMoveGroup = vi.fn()
    const view = render(
      <TabBar
        groups={hook.result.current.wb.groups}
        activeGroupId={hook.result.current.wb.activeGroupId}
        base={hook.result.current.base}
        onActivateGroup={vi.fn()}
        onCloseGroup={vi.fn()}
        onNew={vi.fn()}
        onNewGroup={vi.fn()}
        onMoveGroup={onMoveGroup}
      />,
    )
    const source = view.getByRole('tab', { name: 'b.ts' }).parentElement as HTMLElement
    const dragData = { setData: vi.fn(), effectAllowed: '', dropEffect: '', types: [] as string[], getData: () => '' }
    fireEvent.dragStart(source, { dataTransfer: dragData })
    expect(dragData.setData).toHaveBeenCalledWith(DRAG_GROUP_MIME, JSON.stringify({ groupId: 'g1' }))

    const target = view.getByRole('tab', { name: '组 2' }).parentElement as HTMLElement
    fireEvent.drop(target, {
      clientX: 40,
      dataTransfer: {
        types: [DRAG_GROUP_MIME],
        getData: (key: string) => key === DRAG_GROUP_MIME ? JSON.stringify({ groupId: 'g1' }) : '',
        setData: vi.fn(), effectAllowed: '', dropEffect: '',
      },
    })
    expect(screen.getByRole('alert')).toHaveTextContent('多窗格标签组不能整体移动，请拖动窗格标题')
    expect(onMoveGroup).not.toHaveBeenCalled()
  })

  it('单窗格组拖到另一组上调用 onMoveGroup', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'file', rel: 'a.ts' }, local))
    act(() => hook.result.current.addGroup())
    const onMoveGroup = vi.fn()
    const view = render(
      <TabBar
        groups={hook.result.current.wb.groups}
        activeGroupId={hook.result.current.wb.activeGroupId}
        base={hook.result.current.base}
        onActivateGroup={vi.fn()}
        onCloseGroup={vi.fn()}
        onNew={vi.fn()}
        onNewGroup={vi.fn()}
        onMoveGroup={onMoveGroup}
      />,
    )
    const target = view.getByRole('tab', { name: '组 2' }).parentElement as HTMLElement
    fireEvent.drop(target, {
      clientX: 40,
      dataTransfer: {
        types: [DRAG_GROUP_MIME],
        getData: (key: string) => key === DRAG_GROUP_MIME ? JSON.stringify({ groupId: 'g1' }) : '',
        setData: vi.fn(), effectAllowed: '', dropEffect: '',
      },
    })
    expect(onMoveGroup).toHaveBeenCalledWith('g1', 'g2', 'center')
  })
})
